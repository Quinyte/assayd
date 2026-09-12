// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package envtest

import (
	"context"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/compiler"
)

// policyCreateRace answers every policy Create with AlreadyExists: the
// stale-cache race a real operator meets, which the reconciler treats as
// transient and retries without changing a condition of its own.
type policyCreateRace struct{ client.Client }

func (c *policyCreateRace) Create(ctx context.Context, o client.Object, opts ...client.CreateOption) error {
	if u, ok := o.(*unstructured.Unstructured); ok && u.GetKind() == compiler.PolicyKind {
		return apierrors.NewAlreadyExists(schema.GroupResource{Group: "agentgateway.dev",
			Resource: "agentgatewaypolicies"}, u.GetName())
	}
	return c.Client.Create(ctx, o, opts...)
}

// Found by the third review of slice PR 4: a transient error in a Lock's pass
// wrote Ready=True and cleared PolicyApplyIncomplete, while the route served
// with no policy. Both stay raised until an attributed 401.
func TestATransientErrorMidLockKeepsItsConditionsRaised(t *testing.T) {
	a, r, stub := servedAPIKeyAgent(t, "transient")
	stub.hold("transient", true)
	deletePolicy(t, a)
	reconcileOnce(t, r, a)
	condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "AuthPolicyMissing")
	condIs(t, a, assaydv1alpha1.CondReady, metav1.ConditionFalse, "AuthPolicyMissing")

	// The Lock must write the policy again, and its write loses a race.
	deletePolicy(t, a)
	r.Client = &policyCreateRace{Client: k8s}
	if err := reconcileErr(t, r, a); err == nil || !apierrors.IsAlreadyExists(err) {
		t.Fatalf("the injected race did not reach the caller as a transient error: %v", err)
	}
	if tx := txOf(t, a); tx == nil || tx.Kind != "Lock" {
		t.Fatalf("the Lock left the slot: %+v", authOf(t, a))
	}
	condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "AuthPolicyMissing")
	if c := condition(liveAgent(t, a), assaydv1alpha1.CondReady); c == nil || c.Status != metav1.ConditionFalse {
		t.Errorf("Ready is %+v after a transient error mid-Lock, while the route serves with no "+
			"policy; it must stay False", c)
	}
}
