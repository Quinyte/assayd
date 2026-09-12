// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package envtest

import (
	"context"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/compiler"
)

// The fourth independent review of slice PR 5: its MINORs 2, 3 and 4(b).

// conflictRouteUpdateOnce refuses the first route Update with a Conflict, as
// a concurrent writer would.
type conflictRouteUpdateOnce struct {
	client.Client
	failed bool
}

func (c *conflictRouteUpdateOnce) Update(ctx context.Context, o client.Object, opts ...client.UpdateOption) error {
	if _, ok := o.(*gatewayv1.HTTPRoute); ok && !c.failed {
		c.failed = true
		return apierrors.NewConflict(schema.GroupResource{Group: gatewayv1.GroupName, Resource: "httproutes"},
			o.GetName(), nil)
	}
	return c.Client.Update(ctx, o, opts...)
}

// MINOR 2: a strip that is refused stops the abandonment's pass before it
// removes anything. The route keeps its backendRefs AND its policy, and the
// next pass finishes the abandonment.
func TestARefusedStripRemovesNothing(t *testing.T) {
	a, r := publishingCreate(t, "stripconflict")
	ns := a.Namespace
	r.Client = &conflictRouteUpdateOnce{Client: k8s}
	toMode(t, a, "oauth")
	if err := reconcileErr(t, r, a); err == nil || !apierrors.IsConflict(err) {
		t.Fatalf("the refused strip did not reach the caller: %v", err)
	}
	rt := servingRoute(t, ns, "stripconflict")
	if rt == nil || rt.DeletionTimestamp != nil {
		t.Fatalf("the pass whose strip was refused deleted the route: %v", rt)
	}
	if policyExists(t, runNS(ns), policyNameOf(a)) == nil {
		t.Fatal("the pass whose strip was refused deleted the policy; the route is published without it")
	}
	if tx := txOf(t, a); tx == nil || tx.Kind != "Create" {
		t.Fatalf("the pass whose strip was refused let go of the Create: %+v", authOf(t, a))
	}
	reconcileOnce(t, r, a)
	if servingRoute(t, ns, "stripconflict") != nil || policyExists(t, runNS(ns), policyNameOf(a)) != nil ||
		authOf(t, a) != nil {
		t.Errorf("the next pass did not finish the abandonment: auth=%+v", authOf(t, a))
	}
}

// MINOR 3: the NACK page-cap note reaches a Lock's own condition too, and a
// Lock reads every page up to the cap.
func TestTheNackPageCapIsSurfacedOnALock(t *testing.T) {
	ensureGatewayNamespace(t)
	a, r, _ := j2InProbingAfter(t, "nackcaplock")
	r.NackPageSize, r.NackMaxPages = 1, 1
	later := time.Now().Add(2 * time.Second)
	rawNack(t, "aaa-"+a.Namespace, runNS(a.Namespace)+"/someone-else-auth", later)
	rawNack(t, "zzz-"+a.Namespace, runNS(a.Namespace)+"/"+policyNameOf(a), later)

	lockPass(t, r, a)
	if tx := txOf(t, a); tx.Stage != "ProbingAfter" {
		t.Errorf("a NACK past the cap moved the Lock: %+v", tx)
	}
	g := condition(liveAgent(t, a), assaydv1alpha1.CondGovernanceSkipped)
	if g == nil || g.Reason != "AuthLockPending" || !strings.Contains(g.Message, "were not inspected") {
		t.Errorf("the Lock's condition does not say NACKs past the cap were not inspected: %+v", g)
	}

	r.NackMaxPages = 2
	lockPass(t, r, a)
	condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "AuthLockUnverified")
	if tx := txOf(t, a); tx.Stage != "Converging" {
		t.Errorf("the genuine NACK on page two was not read: %+v", tx)
	}
}

// MINOR 4(b): a served auth: none Agent has no policy to write, so a foreign
// policy at its own name is reported without the recovery that deleting it
// lets the operator write this Agent's policy there.
func TestAForeignPolicyAtANoneAgentsNameNamesNoRecoveryThatWritesNothing(t *testing.T) {
	a, r, _ := servedNoneAgent(t, "nonenote", nil)
	route, _ := compiler.ServingRouteName("nonenote")
	plantAtOwnName(t, a, "uid-of-a-deleted-predecessor", route)
	reconcileOnce(t, r, a)
	c := condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "ForeignTrafficPolicy")
	if c != nil && strings.Contains(c.Message, "delete it") {
		t.Errorf("a served auth: none Agent is told deleting the policy writes its own: %s", c.Message)
	}
}
