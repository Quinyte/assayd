// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package envtest

import (
	"context"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/controller"
)

var routeConflict = apierrors.NewConflict(
	schema.GroupResource{Group: "gateway.networking.k8s.io", Resource: "httproutes"}, "route", nil)

// Review MINOR 4: a J2 Lock past its deadline withholds Ready on a pass that
// loses a race too, as §3.3.3's deadline table says every pass of it does.
// Before the deadline its route serves as before the edit, so a lost race
// withholds nothing.
func TestALostRaceInAJ2LockPastItsDeadlineWithholdsReady(t *testing.T) {
	a, r, stub := servedNoneAgent(t, "racej2", nil)
	r.AuthDeadline = time.Millisecond
	stub.hold("racej2", true)
	toMode(t, a, "apikey")
	lockPass(t, r, a)
	lockPass(t, r, a)
	condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "AuthLockUnverified")

	// Drift the route, so the Lock's re-assert must write it, and lose that race.
	rt := servingRoute(t, a.Namespace, "racej2")
	delete(rt.Labels, controller.LabelAuth)
	if err := k8s.Update(context.Background(), rt); err != nil {
		t.Fatal(err)
	}
	r.Client = &refusingRouteClient{Client: k8s, err: routeConflict}
	if err := reconcileErr(t, r, a); err == nil || !apierrors.IsConflict(err) {
		t.Fatalf("the injected race did not reach the caller: %v", err)
	}
	if c := condition(liveAgent(t, a), assaydv1alpha1.CondReady); c == nil ||
		c.Status != metav1.ConditionFalse || c.Reason != "AuthLockUnverified" {
		t.Errorf("Ready is %+v on a lost race of a J2 Lock past its deadline", c)
	}
}

// ProbingAfter takes no answer while a foreign traffic policy stands, and that
// is not redundant with Publishing's wait: a 401 the foreign policy produced
// would move the Create to Publishing, and once the foreign policy went,
// Publishing would publish on no 401 of the operator's own policy. So the
// Create stays in ProbingAfter, and after the foreign policy goes it probes
// again (§3.2, §3.3.3).
func TestAForeignPoliciesAnswerIsNeverTakenForTheCreates(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "foreignanswer", nil)
	r, stub := createReconciler()
	stub.hold("foreignanswer", true) // the operator's policy is not taken
	promote(t, r, a)
	acceptRoute(t, ns, "foreignanswer")
	acceptPolicy(t, ns, "foreignanswer")
	reconcileOnce(t, r, a)
	if tx := txOf(t, a); tx.Stage != "ProbingAfter" {
		t.Fatalf("want ProbingAfter: %+v", tx)
	}
	p := authPolicyFor(t, liveAgent(t, a))
	p.SetName("intruder")
	p.SetLabels(nil)
	if err := k8s.Create(context.Background(), p); err != nil {
		t.Fatalf("plant the foreign policy: %v", err)
	}
	stub.foreignAnswers("foreignanswer", true)
	reconcileOnce(t, r, a)
	if tx := txOf(t, a); tx.Stage != "ProbingAfter" {
		t.Fatalf("a 401 a foreign policy may have produced moved the Create to %s", tx.Stage)
	}
	if err := k8s.Delete(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	stub.foreignAnswers("foreignanswer", false)
	reconcileOnce(t, r, a)
	if routePublished(t, ns, "foreignanswer") {
		t.Fatal("the route was published on no 401 of the operator's own policy")
	}
}

// Review MINOR 2: a route not yet published is not published while a foreign
// traffic policy stands (§3.2), whichever stage saw it. Publishing waits too:
// a 401 taken before the foreign policy landed, and a none route, which runs
// no probe, are both held.
func TestAForeignPolicyHoldsPublishing(t *testing.T) {
	plant := func(t *testing.T, a *assaydv1alpha1.Agent) {
		t.Helper()
		p := authPolicyFor(t, liveAgent(t, a))
		p.SetName("intruder")
		p.SetLabels(nil)
		if err := k8s.Create(context.Background(), p); err != nil {
			t.Fatalf("plant the foreign policy: %v", err)
		}
	}
	t.Run("a 401 taken before it landed", func(t *testing.T) {
		ns := newNamespace(t)
		a := mustCreateAgent(t, ns, "foreignpub", nil)
		r, _ := createReconciler()
		promote(t, r, a)
		acceptRoute(t, ns, "foreignpub")
		acceptPolicy(t, ns, "foreignpub")
		r.Client = &refusingRouteClient{Client: k8s, err: routeConflict}
		if err := reconcileErr(t, r, a); err == nil || !apierrors.IsConflict(err) {
			t.Fatalf("the injected race did not reach the caller: %v", err)
		}
		if tx := txOf(t, a); tx == nil || tx.Stage != "Publishing" || routePublished(t, ns, "foreignpub") {
			t.Fatalf("want Publishing with the route still unpublished: %+v", tx)
		}
		plant(t, a)
		r.Client = k8s
		reconcileOnce(t, r, a)
		if routePublished(t, ns, "foreignpub") {
			t.Fatal("Publishing published a route under a foreign traffic policy")
		}
		condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "ForeignTrafficPolicy")
	})
	t.Run("a none route", func(t *testing.T) {
		ns := newNamespace(t)
		a := noneAgent(t, ns, "foreignnone")
		r, _ := createReconciler()
		reconcileOnce(t, r, a)
		reconcileOnce(t, r, a)
		plant(t, a)
		markAvailable(t, ns, controller.WorkloadName("foreignnone", liveAgent(t, a).Status.CandidateRevision), 1)
		reconcileOnce(t, r, a)
		if liveAgent(t, a).Status.ActiveRevision == "" {
			t.Fatal("the revision never promoted")
		}
		if routePublished(t, ns, "foreignnone") {
			t.Error("a none route was published at once under a foreign traffic policy")
		}
	})
}
