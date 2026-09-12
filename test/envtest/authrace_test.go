// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package envtest

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/compiler"
)

// Found by the re-review of slice PR 4: a route deleted in the middle of a
// pass, between the check that saw it and the write, came back published.

// vanishAfterCheck answers the first Get of the serving route with the route,
// and deletes it from the API server before returning: the label-selected
// prune that lands between a caller's check and its write.
type vanishAfterCheck struct {
	client.Client
	name  string
	fired bool
}

func (v *vanishAfterCheck) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	err := v.Client.Get(ctx, key, obj, opts...)
	if rt, ok := obj.(*gatewayv1.HTTPRoute); ok && err == nil && !v.fired && key.Name == v.name {
		v.fired = true
		if derr := k8s.Delete(ctx, rt.DeepCopy()); derr != nil {
			return derr
		}
	}
	return err
}

func armVanish(t *testing.T, a *assaydv1alpha1.Agent) *vanishAfterCheck {
	t.Helper()
	name, _ := compiler.ServingRouteName(a.Name)
	return &vanishAfterCheck{Client: k8s, name: name}
}

// At reconcileServed: the route is read present, then gone at the write. It is
// re-created prepared, never published.
func TestARouteDeletedMidPassIsNotCreatedPublished(t *testing.T) {
	a, r, stub := servedAPIKeyAgent(t, "midpass")
	stub.hold("midpass", true)
	v := armVanish(t, a)
	r.Client = v
	_ = reconcileErr(t, r, a)
	if !v.fired {
		t.Fatal("the route was never read, so the race was not exercised")
	}
	assertNeverOpen(t, a)
	if routePublished(t, a.Namespace, "midpass") {
		t.Fatalf("a route deleted mid-pass came back with backendRefs, on an object no probe saw "+
			"refuse: %+v", servingRoute(t, a.Namespace, "midpass").Spec.Rules)
	}
	if tx := txOf(t, a); tx == nil || tx.Kind != "Create" {
		t.Errorf("the vanished route did not enter the re-create: %+v", tx)
	}
}

// At Publishing, after the 401: the route is read present, then gone at the
// write. It goes back to PreparingRoute; a new object no probe saw refuse is
// never published.
func TestARouteDeletedAtPublishingIsNotCreatedPublished(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "midpub", nil)
	r, stub := createReconciler()
	promote(t, r, a)
	acceptRoute(t, ns, "midpub")
	acceptPolicy(t, ns, "midpub")
	reconcileOnce(t, r, a)
	if tx := txOf(t, a); tx == nil || tx.Stage != "Publishing" {
		t.Fatalf("want the Create waiting in Publishing: %+v", tx)
	}
	stub.hold("midpub", true)
	v := armVanish(t, a)
	r.Client = v
	_ = reconcileErr(t, r, a)
	if !v.fired {
		t.Fatal("the route was never read, so the race was not exercised")
	}
	if routePublished(t, ns, "midpub") {
		t.Fatalf("a route deleted at Publishing came back with backendRefs: %+v",
			servingRoute(t, ns, "midpub").Spec.Rules)
	}
	if tx := txOf(t, a); tx == nil || tx.Stage == "Publishing" {
		t.Errorf("the Create did not go back to prepare the route: %+v", tx)
	}
}

// Review MAJOR 2: an I1 Agent whose route is being re-created must not be
// told its route keeps serving (§3.3.3: "Until the re-created route is
// published, the Agent says so where it can").
func TestAnI1AgentWhoseRouteIsRecreatedIsToldItIsUnpublished(t *testing.T) {
	a, r, stub := servedAPIKeyAgent(t, "i1recreate")
	mustEdit(t, a, func(x *assaydv1alpha1.Agent) {
		x.Spec.Expose = &assaydv1alpha1.ExposeSpec{A2A: &assaydv1alpha1.ExposeProtocol{Auth: "oauth"}}
	})
	reconcileOnce(t, r, a)
	stub.hold("i1recreate", true)
	deleteRoute(t, a)
	reconcileOnce(t, r, a)
	if routePublished(t, a.Namespace, "i1recreate") {
		t.Fatal("the re-created route was published with the switch held")
	}
	for _, typ := range []assaydv1alpha1.ConditionType{assaydv1alpha1.CondPolicyCompileFailed, assaydv1alpha1.CondReady} {
		c := condition(liveAgent(t, a), typ)
		if c == nil || strings.Contains(c.Message, "keeps serving") || !strings.Contains(c.Message, "re-created") {
			t.Errorf("%s says the route keeps serving, or does not say it is being re-created: %+v", typ, c)
		}
	}
}

// Review MINOR 1 (mutation R6): the missing-policy Lock's convergence gate. The
// stub answers 401 whatever the policy's status says, so only the gate stops a
// Served on a policy the gateway has not accepted.
func TestTheLockWaitsForThePolicyToConverge(t *testing.T) {
	a, r, stub := servedAPIKeyAgent(t, "lockgate")
	recordCard(t, a)
	stub.hold("lockgate", false)
	deletePolicy(t, a)
	reconcileOnce(t, r, a)
	reconcileOnce(t, r, a)
	reconcileOnce(t, r, a)
	tx := txOf(t, a)
	if tx == nil || tx.Kind != "Lock" || tx.Stage != "Converging" {
		t.Fatalf("a Lock whose re-created policy nobody accepted left Converging: %+v", authOf(t, a))
	}
	condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "AuthPolicyMissing")
}

// Review MINOR 3: a pass that returns before the -auth step still carries
// PolicyApplyIncomplete=AuthPolicyMissing through a Lock, which is raised
// until an attributed 401. An unresolved env source is such a pass.
func TestAnEarlyReturnDoesNotClearTheLocksCondition(t *testing.T) {
	a, r, stub := servedAPIKeyAgent(t, "earlylock")
	stub.hold("earlylock", true)
	deletePolicy(t, a)
	reconcileOnce(t, r, a)
	mustEdit(t, a, func(x *assaydv1alpha1.Agent) {
		x.Spec.Runtime.EnvFrom = []corev1.EnvFromSource{{ConfigMapRef: &corev1.ConfigMapEnvSource{
			LocalObjectReference: corev1.LocalObjectReference{Name: "not-there"}}}}
	})
	reconcileOnce(t, r, a)
	if c := condition(liveAgent(t, a), assaydv1alpha1.CondEnvSourceUnresolved); c == nil {
		t.Fatal("the pass did not take the early return this test is about")
	}
	condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "AuthPolicyMissing")
	condIs(t, a, assaydv1alpha1.CondGovernanceSkipped, metav1.ConditionTrue, "AuthPolicyMissing")
}
