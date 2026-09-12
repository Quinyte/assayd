// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package envtest

import (
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
)

// Found by the check of c2682bc: a Lock replaced by a route re-create in the
// same pass must not have its AuthPolicyMissing put back when that pass then
// loses a race. The re-create's Create clears it in the update that replaced
// the Lock (A62, A63), and the route it prepared is unpublished, so "the route
// serves with no key" would be false.
func TestALockReplacedMidPassDoesNotRestoreItsCondition(t *testing.T) {
	a, r, stub := servedAPIKeyAgent(t, "replaced")
	recordCard(t, a)
	stub.hold("replaced", true)
	deletePolicy(t, a)
	reconcileOnce(t, r, a)
	if tx := txOf(t, a); tx == nil || tx.Kind != "Lock" {
		t.Fatalf("want a Lock of the missing policy first: %+v", tx)
	}

	deleteRoute(t, a)
	deletePolicy(t, a)
	r.Client = &policyCreateRace{Client: k8s}
	if err := reconcileErr(t, r, a); err == nil || !apierrors.IsAlreadyExists(err) {
		t.Fatalf("the injected race did not reach the caller: %v", err)
	}
	if tx := txOf(t, a); tx == nil || tx.Kind != "Create" {
		t.Fatalf("the route re-create did not replace the Lock: %+v", authOf(t, a))
	}
	if routePublished(t, a.Namespace, "replaced") {
		t.Fatal("the re-created route is published")
	}
	if c := condition(liveAgent(t, a), assaydv1alpha1.CondPolicyApplyIncomplete); c != nil &&
		c.Reason == "AuthPolicyMissing" {
		t.Errorf("a Lock the route re-create replaced had its condition put back, saying the route "+
			"serves with no key while it is unpublished: %s", c.Message)
	}
}

// Found by the same check: a lost race at a served Agent's re-create's
// Publishing wrote Ready=True/Available and dropped Degraded for that pass,
// with no route proved and converged.
func TestALostRaceAtARecreatesPublishingKeepsItDegraded(t *testing.T) {
	a, r, stub := servedAPIKeyAgent(t, "racepub")
	stub.hold("racepub", true)
	deleteRoute(t, a)
	reconcileOnce(t, r, a)
	acceptRoute(t, a.Namespace, "racepub")
	reconcileOnce(t, r, a)
	if tx := txOf(t, a); tx == nil || tx.Kind != "Create" || tx.Stage != "ProbingAfter" {
		t.Fatalf("want the re-create waiting in ProbingAfter: %+v", authOf(t, a))
	}
	condIs(t, a, assaydv1alpha1.CondDegraded, metav1.ConditionTrue, "AuthEnforcementPending")

	// Released, the pass enters Publishing and its route write loses a race.
	stub.hold("racepub", false)
	r.Client = &refusingRouteClient{Client: k8s, err: apierrors.NewConflict(
		schema.GroupResource{Group: "gateway.networking.k8s.io", Resource: "httproutes"},
		"racepub-serving", nil)}
	if err := reconcileErr(t, r, a); err == nil || !apierrors.IsConflict(err) {
		t.Fatalf("the injected race did not reach the caller: %v", err)
	}
	if tx := txOf(t, a); tx == nil || tx.Stage != "Publishing" {
		t.Fatalf("want the re-create at Publishing: %+v", authOf(t, a))
	}
	live := liveAgent(t, a)
	if c := condition(live, assaydv1alpha1.CondReady); c == nil || c.Status != metav1.ConditionFalse {
		t.Errorf("Ready is %+v after a lost race at a re-create's Publishing; no route is proved "+
			"and converged", c)
	}
	if c := condition(live, assaydv1alpha1.CondDegraded); c == nil || c.Status != metav1.ConditionTrue {
		t.Errorf("a served Agent being re-created lost Degraded on a lost race: %+v", c)
	}
}
