// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package envtest

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/controller"
)

// Design 16 §1.1 build step 8: the pin rule, Q5 (a). Only while a gate is
// declared, and the EvalSuite CRD is installed, a pin is refused when its
// digest is the current candidate's, or appears in status.eval.failed[]
// paired with the current suite digest. The refusal goes through the existing
// unresolvable-pin path, Degraded=True/PinnedRevisionNotEligible, and the
// current release is untouched. Any other pin promotes, not evaluated, under
// GatesBypassed=True/PinnedRollback. Verdicts are written by the test, as in
// gate_test.go, so that no run can overwrite the record under test.

const (
	reasonPinnedRollback            = "PinnedRollback"
	reasonPinnedRevisionNotEligible = "PinnedRevisionNotEligible"
)

// imageOf is a digest-pinned image whose digest is c repeated.
func imageOf(c byte) string {
	return "ghcr.io/acme/agent@sha256:" + strings.Repeat(string(c), 64)
}

func pinTo(t *testing.T, a *assaydv1alpha1.Agent, digest string) {
	t.Helper()
	mustEdit(t, a, func(a *assaydv1alpha1.Agent) {
		a.Spec.Release = &assaydv1alpha1.ReleaseSpec{TargetRevisionDigest: digest}
	})
}

// failedRecord is status.eval as the eval controller leaves a failed pair.
func failedRecord(rev, digest, sd string, failed ...assaydv1alpha1.EvalPair) *assaydv1alpha1.EvalStatus {
	return &assaydv1alpha1.EvalStatus{Suite: "smoke", Revision: rev, RevisionDigest: digest, SuiteDigest: sd,
		Verdict: assaydv1alpha1.EvalVerdictFailed, Score: "0/1",
		Cases: []assaydv1alpha1.EvalCaseRecord{{ID: "arithmetic", Outcome: assaydv1alpha1.EvalCaseFailed,
			Reason: "Mismatch"}},
		Failed: append(failed, assaydv1alpha1.EvalPair{RevisionDigest: digest, SuiteDigest: sd})}
}

// refusedPin asserts the refusal: Degraded=True/PinnedRevisionNotEligible,
// phase Degraded, and its message naming each of want.
func refusedPin(t *testing.T, a *assaydv1alpha1.Agent, want ...string) {
	t.Helper()
	got := liveAgent(t, a)
	c := condition(got, assaydv1alpha1.CondDegraded)
	if c == nil || c.Status != metav1.ConditionTrue || c.Reason != reasonPinnedRevisionNotEligible {
		t.Fatalf("Degraded is %+v; want True/%s", c, reasonPinnedRevisionNotEligible)
	}
	if got.Status.Phase != assaydv1alpha1.PhaseDegraded {
		t.Errorf("phase is %q, want Degraded, as for an unresolvable pin", got.Status.Phase)
	}
	for _, w := range want {
		if !strings.Contains(c.Message, w) {
			t.Errorf("the refusal does not name %q: %s", w, c.Message)
		}
	}
	if b := condition(got, assaydv1alpha1.CondGatesBypassed); b != nil {
		t.Errorf("a refused pin bypasses nothing, and GatesBypassed is %+v", b)
	}
}

const (
	pinPending = "will be promoted once it is available"
	pinServing = "serves by pin"
)

// bypassed asserts GatesBypassed=True/PinnedRollback naming the revision, in
// the state the message says it is in: pinPending or pinServing.
func bypassed(t *testing.T, a *assaydv1alpha1.Agent, rev, digest, state string) *metav1.Condition {
	t.Helper()
	c := condition(liveAgent(t, a), assaydv1alpha1.CondGatesBypassed)
	if c == nil || c.Status != metav1.ConditionTrue || c.Reason != reasonPinnedRollback {
		t.Fatalf("GatesBypassed is %+v; want True/%s", c, reasonPinnedRollback)
	}
	other := map[string]string{pinPending: pinServing, pinServing: pinPending}[state]
	if !strings.Contains(c.Message, state) || strings.Contains(c.Message, other) {
		t.Errorf("GatesBypassed's message does not say %q (and only that): %s", state, c.Message)
	}
	for _, w := range []string{rev, digest} {
		if !strings.Contains(c.Message, w) {
			t.Errorf("GatesBypassed does not name the pinned revision's %q: %s", w, c.Message)
		}
	}
	return c
}

// T28: pins on a gated Agent, and on an ungated one.
func TestAPinOnAGatedAgentIsAdmittedOrRefusedByQ5(t *testing.T) {
	// B fails suite S1, passes S2 and serves, then C serves, and a pin to B
	// promotes: B is in failed[], but against a suite that is no longer in
	// force. That is what a rule keyed on failed[] alone gets wrong.
	t.Run("a retained, served revision", func(t *testing.T) {
		ns := newNamespace(t)
		sd1 := mustCreateSuite(t, ns, "smoke")
		a, r := servedUngated(t, ns, "pinserved")
		revB, dB := gatedCandidate(t, r, a, imageB, "smoke")
		writeAgentStatus(t, a, func(st *assaydv1alpha1.AgentStatus) {
			st.Eval = failedRecord(revB, dB, sd1)
			putCard(st, revB, dB, time.Now())
		})
		reconcileOnce(t, r, a)
		sd2 := putSuite(t, ns, "smoke", evalCase("arithmetic", "What is 3 + 3?", "6", ""))
		writeAgentStatus(t, a, func(st *assaydv1alpha1.AgentStatus) {
			st.Eval.SuiteDigest, st.Eval.Verdict, st.Eval.Score, st.Eval.Cases = sd2,
				assaydv1alpha1.EvalVerdictPassed, "1/1", nil
		})
		reconcileOnce(t, r, a)
		if got := liveAgent(t, a).Status.ActiveRevisionDigest; got != dB {
			t.Fatalf("setup: B did not promote on its pass against S2 (active %s)", got)
		}
		revC, dC := gatedCandidate(t, r, a, imageOf('c'), "smoke")
		writeAgentStatus(t, a, func(st *assaydv1alpha1.AgentStatus) {
			st.Eval.Revision, st.Eval.RevisionDigest = revC, dC
			putCard(st, revC, dC, time.Now())
		})
		reconcileOnce(t, r, a)
		if got := liveAgent(t, a).Status.ActiveRevisionDigest; got != dC {
			t.Fatalf("setup: C did not promote (active %s)", got)
		}
		if e := liveAgent(t, a).Status.Eval; len(e.Failed) != 1 || e.Failed[0].RevisionDigest != dB ||
			e.Failed[0].SuiteDigest != sd1 {
			t.Fatalf("setup: failed[] is %+v, want B against S1", e.Failed)
		}
		before := *gatesPassed(t, a)

		// B's workload is not available when the pin arrives, so the pass
		// that resolves it records B as the candidate while C serves. The
		// next pass must not read that record as "the current candidate".
		markAvailable(t, ns, controller.WorkloadName("pinserved", revB), 0)
		pinTo(t, a, dB)
		reconcileOnce(t, r, a)
		reconcileOnce(t, r, a)
		got := liveAgent(t, a)
		if c := condition(got, assaydv1alpha1.CondDegraded); c != nil && c.Status == metav1.ConditionTrue {
			t.Fatalf("an admitted pin was refused once its own revision was recorded as the candidate: %+v", c)
		}
		if got.Status.ActiveRevisionDigest != dC || got.Status.CandidateRevisionDigest != dB {
			t.Fatalf("setup: want B coming up beside C; active %s, candidate %s",
				got.Status.ActiveRevisionDigest, got.Status.CandidateRevisionDigest)
		}
		bypassed(t, a, revB, dB, pinPending)

		markAvailable(t, ns, controller.WorkloadName("pinserved", revB), 1)
		reconcileOnce(t, r, a)
		got = liveAgent(t, a)
		if got.Status.ActiveRevisionDigest != dB || got.Status.Phase != assaydv1alpha1.PhaseReady {
			t.Fatalf("the admitted pin to B did not promote it: active %s, phase %s",
				got.Status.ActiveRevisionDigest, got.Status.Phase)
		}
		bypassed(t, a, revB, dB, pinServing)
		// GatesPassed is sticky and the pin does not assert it: it goes on
		// describing the revision status.eval names (§1.1).
		after := *gatesPassed(t, a)
		if after.Status != before.Status || after.Reason != before.Reason || after.Message != before.Message {
			t.Errorf("GatesPassed moved on a pinned rollback: %s/%s %q, was %s/%s %q", after.Status,
				after.Reason, after.Message, before.Status, before.Reason, before.Message)
		}

		// The pin cleared: nothing is bypassed any more.
		mustEdit(t, a, func(a *assaydv1alpha1.Agent) { a.Spec.Release = nil })
		reconcileOnce(t, r, a)
		if c := condition(liveAgent(t, a), assaydv1alpha1.CondGatesBypassed); c != nil {
			t.Errorf("GatesBypassed outlived the pin: %+v", c)
		}
	})

	t.Run("the current candidate", func(t *testing.T) {
		ns := newNamespace(t)
		mustCreateSuite(t, ns, "smoke")
		a, r := servedUngated(t, ns, "pincand")
		active := a.Status.ActiveRevisionDigest
		_, dB := gatedCandidate(t, r, a, imageB, "smoke")
		reconcileOnce(t, r, a)
		condIs(t, a, assaydv1alpha1.CondGatesPassed, metav1.ConditionFalse, "EvalAwaitingCard")

		pinTo(t, a, dB)
		reconcileOnce(t, r, a)
		reconcileOnce(t, r, a)
		refusedPin(t, a, dB, "current candidate", "remove spec.gates")
		got := liveAgent(t, a)
		if got.Status.ActiveRevisionDigest != active || got.Status.CandidateRevisionDigest != dB {
			t.Errorf("the refused pin moved the release: active %s, candidate %s",
				got.Status.ActiveRevisionDigest, got.Status.CandidateRevisionDigest)
		}
		// As for an unresolvable pin: Ready is not touched while the active
		// revision serves.
		condIs(t, a, assaydv1alpha1.CondReady, metav1.ConditionTrue, "Available")
	})

	t.Run("a failure against the current suite, gated and then ungated", func(t *testing.T) {
		ns := newNamespace(t)
		sd := mustCreateSuite(t, ns, "smoke")
		a, r := servedUngated(t, ns, "pinfailed")
		active := a.Status.ActiveRevisionDigest
		revB, dB := gatedCandidate(t, r, a, imageB, "smoke")
		writeAgentStatus(t, a, func(st *assaydv1alpha1.AgentStatus) { st.Eval = failedRecord(revB, dB, sd) })
		reconcileOnce(t, r, a)
		condIs(t, a, assaydv1alpha1.CondGatesPassed, metav1.ConditionFalse, "EvalFailed")
		_, dC := gatedCandidate(t, r, a, imageOf('c'), "smoke")
		// #111's state: the spec moved away from B, so its run's verdict was
		// cleared and only failed[] holds the pair. The rule reads failed[].
		writeAgentStatus(t, a, func(st *assaydv1alpha1.AgentStatus) {
			st.Eval.Verdict, st.Eval.Cases, st.Eval.Score = "", nil, ""
		})

		pinTo(t, a, dB)
		reconcileOnce(t, r, a)
		reconcileOnce(t, r, a)
		refusedPin(t, a, dB, sd, "failed[]")
		// A suite edit can lift the refusal, and nothing watches suites, so
		// the refusal comes back on the drift interval.
		if res, err := r.Reconcile(context.Background(),
			ctrl.Request{NamespacedName: client.ObjectKeyFromObject(a)}); err != nil ||
			res.RequeueAfter != controller.CardDriftInterval {
			t.Errorf("the refusal returned %+v, %v; want a requeue after CardDriftInterval", res, err)
		}
		got := liveAgent(t, a)
		if got.Status.ActiveRevisionDigest != active || got.Status.CandidateRevisionDigest != dC {
			t.Errorf("the refused pin moved the release: active %s, candidate %s",
				got.Status.ActiveRevisionDigest, got.Status.CandidateRevisionDigest)
		}

		// On an Agent with no gate, a pin to the same failed digest promotes,
		// with no GatesBypassed (Q5 (a), A5).
		mustEdit(t, a, func(a *assaydv1alpha1.Agent) { a.Spec.Gates = nil })
		reconcileOnce(t, r, a)
		reconcileOnce(t, r, a)
		got = liveAgent(t, a)
		if got.Status.ActiveRevisionDigest != dB {
			t.Fatalf("on an ungated Agent the pin to B did not promote it (active %s); the rule "+
				"applies only while a gate is declared", got.Status.ActiveRevisionDigest)
		}
		if c := condition(got, assaydv1alpha1.CondGatesBypassed); c != nil {
			t.Errorf("an ungated Agent's pin set GatesBypassed: %+v", c)
		}
		if c := condition(got, assaydv1alpha1.CondDegraded); c != nil && c.Status == metav1.ConditionTrue {
			t.Errorf("an ungated Agent's pin left Degraded standing: %+v", c)
		}
	})
}

// T53: a pin while the suite is missing, and with the EvalSuite CRD absent.
func TestAPinWithoutACurrentSuiteOrWithoutTheCRD(t *testing.T) {
	// With no current suite digest only the current-candidate refusal
	// applies, and a digest in failed[] is admitted: refusing would let a
	// deleted suite block every rollback during an incident (Q5 (a), A6).
	t.Run("the suite is missing", func(t *testing.T) {
		ns := newNamespace(t)
		a, r := servedUngated(t, ns, "pinnosuite")
		active := a.Status.ActiveRevisionDigest
		revB, dB := gatedCandidate(t, r, a, imageB, "absent")
		// B failed a suite of this name before it was deleted.
		writeAgentStatus(t, a, func(st *assaydv1alpha1.AgentStatus) {
			st.Eval = failedRecord(revB, dB, "0b5e7ed0000000000000000000000000000000000000000000000000000000000")
			st.Eval.Suite = "absent"
		})
		_, dC := gatedCandidate(t, r, a, imageOf('c'), "absent")
		condIs(t, a, assaydv1alpha1.CondGatesPassed, metav1.ConditionFalse, "EvalSuiteNotFound")

		pinTo(t, a, dC)
		reconcileOnce(t, r, a)
		refusedPin(t, a, dC, "current candidate")
		if got := liveAgent(t, a).Status.ActiveRevisionDigest; got != active {
			t.Fatalf("a refused pin moved the active revision to %s", got)
		}

		pinTo(t, a, dB)
		reconcileOnce(t, r, a)
		if got := liveAgent(t, a).Status.ActiveRevisionDigest; got != dB {
			t.Fatalf("with the suite missing, a pin to a digest in failed[] was not admitted (active %s)", got)
		}
		// The admitting pass says why failed[] was not consulted.
		c := bypassed(t, a, revB, dB, pinServing)
		if !strings.Contains(c.Message, "could not be read") || !strings.Contains(c.Message, "not consulted") {
			t.Errorf("GatesBypassed does not say the suite could not be read, which is why failed[] "+
				"was not consulted: %s", c.Message)
		}
		reconcileOnce(t, r, a)
		bypassed(t, a, revB, dB, pinServing)
	})

	// Under GatesSkipped=EvalSuiteCRDAbsent nothing is gated, so the rule does
	// not apply at all: a pin to a pair that failed the suite, and a pin to
	// the revision the spec mints, each behave as on an ungated Agent.
	t.Run("the EvalSuite CRD is absent", func(t *testing.T) {
		ns := newNamespace(t)
		sd := mustCreateSuite(t, ns, "smoke")
		a, r := servedUngated(t, ns, "pinnocrd")
		revB, dB := gatedCandidate(t, r, a, imageB, "smoke")
		writeAgentStatus(t, a, func(st *assaydv1alpha1.AgentStatus) { st.Eval = failedRecord(revB, dB, sd) })
		reconcileOnce(t, r, a)
		_, dC := gatedCandidate(t, r, a, imageOf('c'), "smoke")

		r.EvalSuiteInstalled = func() bool { return false }
		pinTo(t, a, dB)
		reconcileOnce(t, r, a)
		reconcileOnce(t, r, a)
		got := liveAgent(t, a)
		if got.Status.ActiveRevisionDigest != dB {
			t.Fatalf("with the EvalSuite CRD absent a pin to B did not promote it (active %s)",
				got.Status.ActiveRevisionDigest)
		}
		condIs(t, a, assaydv1alpha1.CondGatesSkipped, metav1.ConditionTrue, "EvalSuiteCRDAbsent")
		if c := condition(got, assaydv1alpha1.CondGatesBypassed); c != nil {
			t.Errorf("with nothing gated, a pin set GatesBypassed: %+v", c)
		}

		pinTo(t, a, dC)
		reconcileOnce(t, r, a)
		reconcileOnce(t, r, a)
		if got := liveAgent(t, a).Status.ActiveRevisionDigest; got != dC {
			t.Fatalf("with the EvalSuite CRD absent a pin to the spec's own revision did not promote "+
				"it (active %s)", got)
		}
	})
}

// The eval controller does not evaluate a pinned rollback (§1.1, Q5 (a):
// "promotes, not evaluated"). The window is the one between a pinned
// revision becoming available and the Agent reconciler's pass that promotes
// it: status then records it as the candidate, available and carded, which
// is every other send predicate.
func TestAPinnedRollbackIsSentNoCase(t *testing.T) {
	g := newEvalRig(t, "pinnoeval", echo("a", "one"))
	g.both(t, 2)
	if got := liveAgent(t, g.a).Status.ActiveRevisionDigest; got != g.dgst {
		t.Fatalf("setup: B did not promote on its run (active %s)", got)
	}
	gatedCandidate(t, g.r, g.a, imageOf('c'), "smoke")
	rollback, rollbackDigest := g.activeName, g.active
	markAvailable(t, g.ns, controller.WorkloadName(g.name, rollback), 0)
	writeAgentStatus(t, g.a, func(st *assaydv1alpha1.AgentStatus) {
		putCard(st, rollback, rollbackDigest, time.Now())
		for i := range st.Cards {
			if st.Cards[i].RevisionDigest == rollbackDigest {
				st.Cards[i].Digest = sha256Hex(stubCard(g.name))
			}
		}
	})
	pinTo(t, g.a, rollbackDigest)
	g.agentPass(t)
	if got := liveAgent(t, g.a).Status.CandidateRevisionDigest; got != rollbackDigest {
		t.Fatalf("setup: the pinned revision is not the recorded candidate (%s)", got)
	}
	markAvailable(t, g.ns, controller.WorkloadName(g.name, rollback), 1)
	evalBefore := liveAgent(t, g.a).Status.Eval.DeepCopy()
	for i := 0; i < 3; i++ {
		g.evalPass(t)
	}
	if n := g.stub.sentTo(g.name, rollback); n != 0 {
		t.Errorf("the eval controller sent a pinned rollback %d cases", n)
	}
	if e := liveAgent(t, g.a).Status.Eval; e.RevisionDigest != evalBefore.RevisionDigest ||
		e.Verdict != evalBefore.Verdict {
		t.Errorf("the eval controller started a run for a pinned rollback: %+v", e)
	}
	g.agentPass(t)
	if got := liveAgent(t, g.a).Status.ActiveRevisionDigest; got != rollbackDigest {
		t.Fatalf("the pinned rollback did not promote once available (active %s)", got)
	}
	bypassed(t, g.a, rollback, rollbackDigest, pinServing)
}

// An admitted pin is not refused while it stands: neither once its revision
// serves and the suite in force becomes one it failed, nor after the
// incident path T53 exists for, where a deleted suite let a failed revision
// be pinned and the suite was then restored. A refusal there would return
// before the rest of the Agent's reconcile on every pass while the pinned
// revision served.
func TestAnAdmittedPinIsNotRefusedWhileItServes(t *testing.T) {
	setup := func(t *testing.T, name string) (string, *assaydv1alpha1.Agent, *controller.AgentReconciler,
		string, string, string) {
		t.Helper()
		ns := newNamespace(t)
		sd1 := mustCreateSuite(t, ns, "smoke")
		a, r := servedUngated(t, ns, name)
		revB, dB := gatedCandidate(t, r, a, imageB, "smoke")
		writeAgentStatus(t, a, func(st *assaydv1alpha1.AgentStatus) { st.Eval = failedRecord(revB, dB, sd1) })
		reconcileOnce(t, r, a)
		gatedCandidate(t, r, a, imageOf('c'), "smoke")
		return ns, a, r, revB, dB, sd1
	}
	stillServes := func(t *testing.T, a *assaydv1alpha1.Agent, r *controller.AgentReconciler, revB, dB string) {
		t.Helper()
		for i := 0; i < 2; i++ {
			reconcileOnce(t, r, a)
		}
		got := liveAgent(t, a)
		if c := condition(got, assaydv1alpha1.CondDegraded); c != nil && c.Status == metav1.ConditionTrue {
			t.Fatalf("the admitted, serving pin was refused: %s/%s", c.Reason, c.Message)
		}
		if got.Status.ActiveRevisionDigest != dB || got.Status.Phase != assaydv1alpha1.PhaseReady {
			t.Fatalf("active %s, phase %s; want B serving", got.Status.ActiveRevisionDigest, got.Status.Phase)
		}
		bypassed(t, a, revB, dB, pinServing)
	}

	t.Run("the suite edited back to one it failed", func(t *testing.T) {
		ns, a, r, revB, dB, sd1 := setup(t, "pinback")
		putSuite(t, ns, "smoke", evalCase("arithmetic", "What is 3 + 3?", "6", ""))
		pinTo(t, a, dB)
		reconcileOnce(t, r, a)
		if got := liveAgent(t, a).Status.ActiveRevisionDigest; got != dB {
			t.Fatalf("setup: B, failed only against the old suite, was not promoted (active %s)", got)
		}
		if back := putSuite(t, ns, "smoke", evalCase("arithmetic", "What is 2 + 2?", "4", "")); back != sd1 {
			t.Fatalf("setup: the restored suite's digest is %s, want %s", back, sd1)
		}
		stillServes(t, a, r, revB, dB)
	})
	t.Run("the suite deleted, then restored", func(t *testing.T) {
		ns, a, r, revB, dB, sd1 := setup(t, "pinrestore")
		s := &assaydv1alpha1.EvalSuite{}
		s.Namespace, s.Name = ns, "smoke"
		if err := k8s.Delete(context.Background(), s); err != nil {
			t.Fatal(err)
		}
		pinTo(t, a, dB)
		reconcileOnce(t, r, a)
		if got := liveAgent(t, a).Status.ActiveRevisionDigest; got != dB {
			t.Fatalf("setup: with the suite gone, B was not promoted (active %s)", got)
		}
		if back := putSuite(t, ns, "smoke", evalCase("arithmetic", "What is 2 + 2?", "4", "")); back != sd1 {
			t.Fatalf("setup: the restored suite's digest is %s, want %s", back, sd1)
		}
		stillServes(t, a, r, revB, dB)
	})
}

// A pin naming the revision already serving has nothing to admit or refuse:
// the failed[] clause does not refuse it even when failed[] records it against
// the suite in force. GatesBypassed is derived from status: it is set while
// status.eval records no Passed verdict for the serving revision, which then
// serves by pin with nothing evaluated in its place, and not once one does.
func TestAPinToTheServingRevisionIsNeverRefused(t *testing.T) {
	ns := newNamespace(t)
	sd := mustCreateSuite(t, ns, "smoke")
	a, r := servedUngated(t, ns, "pinserving")
	active, activeName := a.Status.ActiveRevisionDigest, a.Status.ActiveRevision
	gatedCandidate(t, r, a, imageB, "smoke")
	writeAgentStatus(t, a, func(st *assaydv1alpha1.AgentStatus) {
		st.Eval = failedRecord(activeName, active, sd)
	})
	pinTo(t, a, active)
	reconcileOnce(t, r, a)
	reconcileOnce(t, r, a)
	got := liveAgent(t, a)
	if c := condition(got, assaydv1alpha1.CondDegraded); c != nil && c.Status == metav1.ConditionTrue {
		t.Fatalf("a pin to the serving revision was refused: %s/%s", c.Reason, c.Message)
	}
	if got.Status.ActiveRevisionDigest != active {
		t.Errorf("the active revision moved to %s", got.Status.ActiveRevisionDigest)
	}
	c := bypassed(t, a, activeName, active, pinServing)
	// failed[] records it against the suite in force, so the message must
	// not say the opposite; it says the pin names the revision serving.
	if strings.Contains(c.Message, "does not record it") || !strings.Contains(c.Message, "names the revision serving") {
		t.Errorf("GatesBypassed misstates why a pin to the serving revision is admitted: %s", c.Message)
	}

	// A Passed verdict for the serving revision: nothing is bypassed.
	writeAgentStatus(t, a, func(st *assaydv1alpha1.AgentStatus) {
		st.Eval = &assaydv1alpha1.EvalStatus{Suite: "smoke", Revision: activeName, RevisionDigest: active,
			SuiteDigest: sd, Verdict: assaydv1alpha1.EvalVerdictPassed, Score: "1/1"}
	})
	reconcileOnce(t, r, a)
	if c := condition(liveAgent(t, a), assaydv1alpha1.CondGatesBypassed); c != nil {
		t.Errorf("a pin to a serving revision with a Passed verdict says a gate is bypassed: %+v", c)
	}
}

// A revision the current spec mints is the current candidate, so a pin to it
// is refused (Q5 (a)'s stated residue: "a revision that is the current
// candidate again after serving earlier"), whether the spec is reverted to it
// and pinned in one write or in two.
func TestAPinToTheRevisionTheSpecMintsIsRefusedInOneWriteOrTwo(t *testing.T) {
	for _, one := range []bool{true, false} {
		t.Run(fmt.Sprintf("one write %v", one), func(t *testing.T) {
			ns := newNamespace(t)
			sd := mustCreateSuite(t, ns, "smoke")
			a, r := servedUngated(t, ns, fmt.Sprintf("pinrevert%v", one))
			imgA, dA := a.Spec.Runtime.Image, a.Status.ActiveRevisionDigest
			revB, dB := gatedCandidate(t, r, a, imageB, "smoke")
			writeAgentStatus(t, a, func(st *assaydv1alpha1.AgentStatus) {
				st.Eval = &assaydv1alpha1.EvalStatus{Suite: "smoke", Revision: revB, RevisionDigest: dB,
					SuiteDigest: sd, Verdict: assaydv1alpha1.EvalVerdictPassed, Score: "1/1"}
				putCard(st, revB, dB, time.Now())
			})
			reconcileOnce(t, r, a)
			if got := liveAgent(t, a).Status.ActiveRevisionDigest; got != dB {
				t.Fatalf("setup: B did not promote (active %s)", got)
			}
			if one {
				mustEdit(t, a, func(a *assaydv1alpha1.Agent) {
					a.Spec.Runtime.Image = imgA
					a.Spec.Release = &assaydv1alpha1.ReleaseSpec{TargetRevisionDigest: dA}
				})
			} else {
				mustEdit(t, a, func(a *assaydv1alpha1.Agent) { a.Spec.Runtime.Image = imgA })
				reconcileOnce(t, r, a)
				reconcileOnce(t, r, a)
				pinTo(t, a, dA)
			}
			reconcileOnce(t, r, a)
			reconcileOnce(t, r, a)
			refusedPin(t, a, dA, "current candidate")
			if got := liveAgent(t, a).Status.ActiveRevisionDigest; got != dB {
				t.Errorf("the refused pin moved the active revision to %s", got)
			}
		})
	}
}

// Q5 (a)'s "current candidate" is the revision the gate is evaluating, the
// one the spec mints. One write that edits the spec and pins the candidate
// it leaves behind therefore admits the pin: the recorded candidate is no
// longer the spec's revision. The coordinator's reading on review 2 of PR
// #119, recorded for the amendment.
func TestAPinToACandidateTheSpecHasLeftIsAdmitted(t *testing.T) {
	ns := newNamespace(t)
	mustCreateSuite(t, ns, "smoke")
	a, r := servedUngated(t, ns, "pinleft")
	revB, dB := gatedCandidate(t, r, a, imageB, "smoke")
	reconcileOnce(t, r, a)
	mustEdit(t, a, func(a *assaydv1alpha1.Agent) {
		a.Spec.Runtime.Image = imageOf('d')
		a.Spec.Release = &assaydv1alpha1.ReleaseSpec{TargetRevisionDigest: dB}
	})
	reconcileOnce(t, r, a)
	got := liveAgent(t, a)
	if c := condition(got, assaydv1alpha1.CondDegraded); c != nil && c.Status == metav1.ConditionTrue {
		t.Fatalf("the pin was refused: %s/%s", c.Reason, c.Message)
	}
	if got.Status.ActiveRevisionDigest != dB {
		t.Fatalf("B, which the spec no longer mints, was not promoted by the pin (active %s)",
			got.Status.ActiveRevisionDigest)
	}
	bypassed(t, a, revB, dB, pinServing)
}

// A refused pin pauses nothing: the eval controller goes on evaluating the
// candidate it names, since only an admitted pin's candidate is not
// evaluated.
func TestARefusedPinLeavesTheCandidatesRunGoing(t *testing.T) {
	g := newEvalRig(t, "pinrefusedrun", echo("a", "one"), echo("b", "two"))
	pinTo(t, g.a, g.dgst)
	g.agentPass(t)
	refusedPin(t, g.a, g.dgst, "current candidate")
	// A forged GatesBypassed pauses nothing: the eval controller derives
	// admission from the spec and status, and reads no condition.
	writeAgentStatus(t, g.a, func(st *assaydv1alpha1.AgentStatus) {
		st.Conditions = append(st.Conditions, metav1.Condition{Type: string(assaydv1alpha1.CondGatesBypassed),
			Status: metav1.ConditionTrue, Reason: reasonPinnedRollback, Message: "forged",
			LastTransitionTime: metav1.Now()})
	})
	for i := 0; i < 3; i++ {
		g.evalPass(t)
	}
	if n := g.stub.sentTo(g.name, g.rev); n == 0 {
		t.Error("the candidate a refused pin names was sent no case; its run should go on")
	}
}

// pinFixture is a served Agent with a ConfigMap env source, so each revision
// has retained material: A served ungated, then a gate, B available and
// retained, and C the gated candidate. Revision names come from status: the
// spec's names depend on the ConfigMap's content.
func pinFixture(t *testing.T, name string) (ns string, a *assaydv1alpha1.Agent, r *controller.AgentReconciler,
	revB, dB, sd string) {
	t.Helper()
	ns = newNamespace(t)
	sd = mustCreateSuite(t, ns, "smoke")
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "cfg"},
		Data: map[string]string{"MODE": "safe"}}
	if err := k8s.Create(context.Background(), cm); err != nil {
		t.Fatal(err)
	}
	a = mustCreateAgent(t, ns, name, func(a *assaydv1alpha1.Agent) {
		a.Spec.Runtime.Env = []corev1.EnvVar{{Name: "MODE", ValueFrom: &corev1.EnvVarSource{
			ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: "cfg"}, Key: "MODE"}}}}
	})
	r = newReconciler(true)
	bringUp := func(want string) (string, string) {
		t.Helper()
		reconcileOnce(t, r, a)
		reconcileOnce(t, r, a)
		got := liveAgent(t, a)
		rev, d := got.Status.CandidateRevision, got.Status.CandidateRevisionDigest
		if rev == "" {
			t.Fatalf("setup: no %s candidate recorded: %+v", want, got.Status)
		}
		markAvailable(t, ns, controller.WorkloadName(name, rev), 1)
		reconcileOnce(t, r, a)
		return rev, d
	}
	bringUp("first")
	if liveAgent(t, a).Status.ActiveRevision == "" {
		t.Fatal("setup: the first revision did not promote")
	}
	mustEdit(t, a, func(a *assaydv1alpha1.Agent) {
		a.Spec.Runtime.Image = imageB
		a.Spec.Gates = []assaydv1alpha1.GateRef{{EvalSuiteRef: "smoke"}}
	})
	revB, dB = bringUp("B")
	mustEdit(t, a, func(a *assaydv1alpha1.Agent) { a.Spec.Runtime.Image = imageOf('c') })
	bringUp("C")
	return ns, a, r, revB, dB, sd
}

// The retained material is verified before the eligibility rule, so a pin to
// a revision whose material is gone is refused for that, and is never told
// to edit a suite.
func TestAPinWhoseMaterialIsGoneIsRefusedForThatFirst(t *testing.T) {
	ns, a, r, revB, dB, sd := pinFixture(t, "pinmaterial")
	writeAgentStatus(t, a, func(st *assaydv1alpha1.AgentStatus) { st.Eval = failedRecord(revB, dB, sd) })
	var copies corev1.ConfigMapList
	if err := k8s.List(context.Background(), &copies, client.InNamespace(runNS(ns)),
		client.MatchingLabels{controller.MaterialRevisionLabel: revB}); err != nil || len(copies.Items) == 0 {
		t.Fatalf("setup: no immutable copy of B to delete (%v)", err)
	}
	if err := k8s.Delete(context.Background(), &copies.Items[0]); err != nil {
		t.Fatal(err)
	}
	pinTo(t, a, dB)
	reconcileOnce(t, r, a)
	c := condition(liveAgent(t, a), assaydv1alpha1.CondDegraded)
	if c == nil || c.Status != metav1.ConditionTrue || c.Reason != "ReleasePinUnresolvable" {
		t.Fatalf("Degraded is %+v; want True/ReleasePinUnresolvable — B's material is gone, which is the "+
			"refusal that holds whatever its eval record says", c)
	}
}

// An admitted pin's GatesBypassed survives a pass that returns before the pin
// step: here a referenced ConfigMap deleted while the pinned revision serves,
// which the source resolution reports before any revision is computed.
func TestAnAdmittedPinsBypassSurvivesAnEarlyExit(t *testing.T) {
	ns, a, r, revB, dB, _ := pinFixture(t, "pinearly")
	pinTo(t, a, dB)
	reconcileOnce(t, r, a)
	if got := liveAgent(t, a).Status.ActiveRevisionDigest; got != dB {
		t.Fatalf("setup: the pin to B did not promote it (active %s)", got)
	}
	bypassed(t, a, revB, dB, pinServing)
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "cfg"}}
	if err := k8s.Delete(context.Background(), cm); err != nil {
		t.Fatal(err)
	}
	reconcileOnce(t, r, a)
	condIs(t, a, assaydv1alpha1.CondEnvSourceUnresolved, metav1.ConditionTrue, "Unresolved")
	bypassed(t, a, revB, dB, pinServing)
}

// An admitted pin that stops resolving bypasses nothing: here the pinned
// revision is still coming up when its material is collected, so its
// GatesBypassed, which said it would be promoted once available, is withdrawn
// with the refusal.
func TestAnAdmittedPinThatStopsResolvingWithdrawsItsBypass(t *testing.T) {
	ns, a, r, revB, dB, _ := pinFixture(t, "pinlost")
	markAvailable(t, ns, controller.WorkloadName("pinlost", revB), 0)
	pinTo(t, a, dB)
	reconcileOnce(t, r, a)
	bypassed(t, a, revB, dB, pinPending)
	var copies corev1.ConfigMapList
	if err := k8s.List(context.Background(), &copies, client.InNamespace(runNS(ns)),
		client.MatchingLabels{controller.MaterialRevisionLabel: revB}); err != nil || len(copies.Items) == 0 {
		t.Fatalf("setup: no immutable copy of B to delete (%v)", err)
	}
	if err := k8s.Delete(context.Background(), &copies.Items[0]); err != nil {
		t.Fatal(err)
	}
	reconcileOnce(t, r, a)
	got := liveAgent(t, a)
	if c := condition(got, assaydv1alpha1.CondDegraded); c == nil || c.Reason != "ReleasePinUnresolvable" {
		t.Fatalf("setup: Degraded is %+v, want ReleasePinUnresolvable", c)
	}
	if c := condition(got, assaydv1alpha1.CondGatesBypassed); c != nil {
		t.Errorf("a pin that no longer resolves still says its gate is bypassed: %+v", c)
	}
}

// A pin admitted and still coming up stays admitted across passes on which
// nothing judges it, and is not then refused as its own candidate: the
// rollout switch recorded it as status.candidateRevisionDigest, but the spec
// mints another revision. Three such passes: an env-source edit that the
// pinned revision's material cannot serve, then reverted; spec.gates removed
// and restored; and the EvalSuite CRD undiscoverable for one pass.
func TestAComingUpPinStaysAdmittedAcrossPassesThatDoNotJudgeIt(t *testing.T) {
	cases := []struct {
		name    string
		disturb func(t *testing.T, ns string, a *assaydv1alpha1.Agent, r *controller.AgentReconciler)
	}{
		{"an env-source edit, reverted", func(t *testing.T, ns string, a *assaydv1alpha1.Agent,
			r *controller.AgentReconciler) {
			cm2 := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "cfg2"},
				Data: map[string]string{"LEVEL": "1"}}
			if err := k8s.Create(context.Background(), cm2); err != nil {
				t.Fatal(err)
			}
			mustEdit(t, a, func(a *assaydv1alpha1.Agent) {
				a.Spec.Runtime.Env = append(a.Spec.Runtime.Env, corev1.EnvVar{Name: "LEVEL",
					ValueFrom: &corev1.EnvVarSource{ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: "cfg2"}, Key: "LEVEL"}}})
			})
			reconcileOnce(t, r, a)
			condIs(t, a, assaydv1alpha1.CondDegraded, metav1.ConditionTrue, "ReleasePinUnresolvable")
			mustEdit(t, a, func(a *assaydv1alpha1.Agent) { a.Spec.Runtime.Env = a.Spec.Runtime.Env[:1] })
		}},
		{"spec.gates removed and restored", func(t *testing.T, _ string, a *assaydv1alpha1.Agent,
			r *controller.AgentReconciler) {
			mustEdit(t, a, func(a *assaydv1alpha1.Agent) { a.Spec.Gates = nil })
			reconcileOnce(t, r, a)
			mustEdit(t, a, func(a *assaydv1alpha1.Agent) {
				a.Spec.Gates = []assaydv1alpha1.GateRef{{EvalSuiteRef: "smoke"}}
			})
		}},
		{"the EvalSuite CRD absent for one pass", func(t *testing.T, _ string, a *assaydv1alpha1.Agent,
			r *controller.AgentReconciler) {
			prev := r.EvalSuiteInstalled
			r.EvalSuiteInstalled = func() bool { return false }
			reconcileOnce(t, r, a)
			r.EvalSuiteInstalled = prev
		}},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			name := fmt.Sprintf("pinstays%d", i)
			ns, a, r, revB, dB, _ := pinFixture(t, name)
			markAvailable(t, ns, controller.WorkloadName(name, revB), 0)
			pinTo(t, a, dB)
			reconcileOnce(t, r, a)
			reconcileOnce(t, r, a)
			bypassed(t, a, revB, dB, pinPending)
			c.disturb(t, ns, a, r)
			reconcileOnce(t, r, a)
			reconcileOnce(t, r, a)
			got := liveAgent(t, a)
			if d := condition(got, assaydv1alpha1.CondDegraded); d != nil && d.Status == metav1.ConditionTrue {
				t.Fatalf("the coming-up pin was refused after the disturbance: %s/%s", d.Reason, d.Message)
			}
			if got.Status.CandidateRevisionDigest != dB {
				t.Fatalf("setup: B is not the recorded candidate (%s)", got.Status.CandidateRevisionDigest)
			}
			bypassed(t, a, revB, dB, pinPending)
			markAvailable(t, ns, controller.WorkloadName(name, revB), 1)
			reconcileOnce(t, r, a)
			if got := liveAgent(t, a).Status.ActiveRevisionDigest; got != dB {
				t.Fatalf("the pin did not promote B once available (active %s)", got)
			}
			bypassed(t, a, revB, dB, pinServing)
		})
	}
}

// A pin admitted and not yet serving is judged again on each pass: a suite
// edited to one it failed refuses it before it serves.
func TestAComingUpPinIsJudgedAgainstAChangedSuite(t *testing.T) {
	ns, a, r, revB, dB, sd1 := pinFixture(t, "pinrejudge")
	writeAgentStatus(t, a, func(st *assaydv1alpha1.AgentStatus) { st.Eval = failedRecord(revB, dB, sd1) })
	putSuite(t, ns, "smoke", evalCase("arithmetic", "What is 3 + 3?", "6", ""))
	markAvailable(t, ns, controller.WorkloadName("pinrejudge", revB), 0)
	pinTo(t, a, dB)
	reconcileOnce(t, r, a)
	bypassed(t, a, revB, dB, pinPending)
	if back := putSuite(t, ns, "smoke", evalCase("arithmetic", "What is 2 + 2?", "4", "")); back != sd1 {
		t.Fatalf("setup: the restored suite's digest is %s, want %s", back, sd1)
	}
	reconcileOnce(t, r, a)
	refusedPin(t, a, dB, sd1, "failed[]")
}

// A forged GatesBypassed grants nothing: the rule is derived from the spec and
// status each pass and does not read it, so a pin to a revision that failed
// the suite in force is refused beside one that names it.
func TestAForgedGatesBypassedGrantsNothing(t *testing.T) {
	_, a, r, revB, dB, sd := pinFixture(t, "pinforged")
	writeAgentStatus(t, a, func(st *assaydv1alpha1.AgentStatus) {
		st.Eval = failedRecord(revB, dB, sd)
		st.Conditions = append(st.Conditions, metav1.Condition{Type: string(assaydv1alpha1.CondGatesBypassed),
			Status: metav1.ConditionTrue, Reason: reasonPinnedRollback, Message: "forged (digest " + dB + ")",
			LastTransitionTime: metav1.Now()})
	})
	active := liveAgent(t, a).Status.ActiveRevisionDigest
	if c := condition(liveAgent(t, a), assaydv1alpha1.CondGatesBypassed); c == nil || c.Reason != reasonPinnedRollback {
		t.Fatalf("setup: the forged condition was not stored: %+v", c)
	}
	pinTo(t, a, dB)
	reconcileOnce(t, r, a)
	reconcileOnce(t, r, a)
	refusedPin(t, a, dB, "failed[]")
	if got := liveAgent(t, a).Status.ActiveRevisionDigest; got != active {
		t.Fatalf("a forged GatesBypassed served B, which failed the suite in force (active %s)", got)
	}
}

// A pin changed from an admitted revision to the one serving withdraws
// GatesBypassed: it is derived each pass, and a pin to a serving revision
// that passed its gate bypasses nothing.
func TestRePinningToTheServingRevisionWithdrawsTheBypass(t *testing.T) {
	ns, a, r, revB, dB, sd := pinFixture(t, "pinrepin")
	live := liveAgent(t, a)
	active, activeName := live.Status.ActiveRevisionDigest, live.Status.ActiveRevision
	// The serving revision passed, so a pin to it bypasses nothing.
	writeAgentStatus(t, a, func(st *assaydv1alpha1.AgentStatus) {
		st.Eval = &assaydv1alpha1.EvalStatus{Suite: "smoke", Revision: activeName, RevisionDigest: active,
			SuiteDigest: sd, Verdict: assaydv1alpha1.EvalVerdictPassed, Score: "1/1"}
	})
	markAvailable(t, ns, controller.WorkloadName("pinrepin", revB), 0)
	pinTo(t, a, dB)
	reconcileOnce(t, r, a)
	bypassed(t, a, revB, dB, pinPending)
	pinTo(t, a, active)
	reconcileOnce(t, r, a)
	got := liveAgent(t, a)
	if c := condition(got, assaydv1alpha1.CondGatesBypassed); c != nil {
		t.Errorf("a pin re-pointed at the serving revision still says a gate is bypassed: %+v", c)
	}
	if got.Status.ActiveRevisionDigest != active {
		t.Errorf("the active revision moved to %s", got.Status.ActiveRevisionDigest)
	}
}

// On an ungated Agent the pin is handled exactly as before Q5 (A5): its
// material is verified after the supersession, so a refused pin's write
// records the superseded candidate, as it always did.
func TestAnUngatedPinKeepsTheOrderItHadBeforeQ5(t *testing.T) {
	ns := newNamespace(t)
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "cfg"},
		Data: map[string]string{"MODE": "safe"}}
	if err := k8s.Create(context.Background(), cm); err != nil {
		t.Fatal(err)
	}
	a := mustCreateAgent(t, ns, "pinungated", func(a *assaydv1alpha1.Agent) {
		a.Spec.Runtime.Env = []corev1.EnvVar{{Name: "MODE", ValueFrom: &corev1.EnvVarSource{
			ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: "cfg"}, Key: "MODE"}}}}
	})
	r := newReconciler(true)
	promote := func() string {
		reconcileOnce(t, r, a)
		reconcileOnce(t, r, a)
		rev := liveAgent(t, a).Status.CandidateRevision
		markAvailable(t, ns, controller.WorkloadName("pinungated", rev), 1)
		reconcileOnce(t, r, a)
		return rev
	}
	revA := promote()
	dA := liveAgent(t, a).Status.ActiveRevisionDigest
	mustEdit(t, a, func(a *assaydv1alpha1.Agent) { a.Spec.Runtime.Image = imageB })
	promote()
	mustEdit(t, a, func(a *assaydv1alpha1.Agent) { a.Spec.Runtime.Image = imageOf('c') })
	reconcileOnce(t, r, a)
	reconcileOnce(t, r, a)
	dC := liveAgent(t, a).Status.CandidateRevisionDigest
	if dC == "" {
		t.Fatal("setup: C is not the candidate")
	}
	var copies corev1.ConfigMapList
	if err := k8s.List(context.Background(), &copies, client.InNamespace(runNS(ns)),
		client.MatchingLabels{controller.MaterialRevisionLabel: revA}); err != nil || len(copies.Items) == 0 {
		t.Fatalf("setup: no immutable copy of A to delete (%v)", err)
	}
	if err := k8s.Delete(context.Background(), &copies.Items[0]); err != nil {
		t.Fatal(err)
	}
	pinTo(t, a, dA)
	reconcileOnce(t, r, a)
	got := liveAgent(t, a)
	condIs(t, a, assaydv1alpha1.CondDegraded, metav1.ConditionTrue, "ReleasePinUnresolvable")
	if got.Status.CandidateRevisionDigest != "" || !strings.Contains(strings.Join(got.Status.SupersededCandidates, ","), dC) {
		t.Errorf("an ungated pin's material refusal no longer records C's supersession, as it did before "+
			"Q5: candidate %q, superseded %v", got.Status.CandidateRevisionDigest, got.Status.SupersededCandidates)
	}
}

// The current candidate follows the env sources' content, as the rollout
// step's identity does: an edit to a referenced ConfigMap, with no write to
// the Agent, mints a new candidate, and a refused pin to the old one becomes a
// pin to a superseded candidate, which Q5 (a) admits. Recorded as a residue.
func TestASourceContentEditLiftsACandidateRefusal(t *testing.T) {
	ns, a, r, _, _, _ := pinFixture(t, "pincontent")
	live := liveAgent(t, a)
	revC, dC := live.Status.CandidateRevision, live.Status.CandidateRevisionDigest
	pinTo(t, a, dC)
	reconcileOnce(t, r, a)
	refusedPin(t, a, dC, "current candidate")
	gen := liveAgent(t, a).Generation
	var cm corev1.ConfigMap
	if err := k8s.Get(context.Background(), client.ObjectKey{Namespace: ns, Name: "cfg"}, &cm); err != nil {
		t.Fatal(err)
	}
	cm.Data["MODE"] = "other"
	if err := k8s.Update(context.Background(), &cm); err != nil {
		t.Fatal(err)
	}
	reconcileOnce(t, r, a)
	reconcileOnce(t, r, a)
	got := liveAgent(t, a)
	if got.Generation != gen {
		t.Fatalf("setup: the Agent was written (generation %d, was %d)", got.Generation, gen)
	}
	if got.Status.ActiveRevisionDigest != dC {
		t.Fatalf("after the content edit, C is no longer the revision the spec mints, and the pin to it "+
			"was not admitted (active %s)", got.Status.ActiveRevisionDigest)
	}
	bypassed(t, a, revC, dC, pinServing)
}

// A forged GatesBypassed beside a pin to a serving revision that passed is
// recomputed away, and GatesPassed goes on describing the verdict.
func TestAForgedGatesBypassedOnAServingPinIsRecomputedAway(t *testing.T) {
	ns := newNamespace(t)
	sd := mustCreateSuite(t, ns, "smoke")
	a, r := servedUngated(t, ns, "pinforgedserving")
	revB, dB := gatedCandidate(t, r, a, imageB, "smoke")
	writeAgentStatus(t, a, func(st *assaydv1alpha1.AgentStatus) {
		st.Eval = &assaydv1alpha1.EvalStatus{Suite: "smoke", Revision: revB, RevisionDigest: dB,
			SuiteDigest: sd, Verdict: assaydv1alpha1.EvalVerdictPassed, Score: "1/1"}
		putCard(st, revB, dB, time.Now())
	})
	reconcileOnce(t, r, a)
	if got := liveAgent(t, a).Status.ActiveRevisionDigest; got != dB {
		t.Fatalf("setup: B did not promote (active %s)", got)
	}
	writeAgentStatus(t, a, func(st *assaydv1alpha1.AgentStatus) {
		st.Conditions = append(st.Conditions, metav1.Condition{Type: string(assaydv1alpha1.CondGatesBypassed),
			Status: metav1.ConditionTrue, Reason: reasonPinnedRollback, Message: "forged (digest " + dB + ")",
			LastTransitionTime: metav1.Now()})
	})
	pinTo(t, a, dB)
	reconcileOnce(t, r, a)
	if c := condition(liveAgent(t, a), assaydv1alpha1.CondGatesBypassed); c != nil {
		t.Errorf("a forged GatesBypassed survived a pass: %+v", c)
	}
	condIs(t, a, assaydv1alpha1.CondGatesPassed, metav1.ConditionTrue, "EvalPassed")
}

// A serving pinned rollback's GatesBypassed costs nothing permanent when the
// EvalSuite CRD is undiscoverable for one pass: it is derived again on the
// next, and GatesPassed is not asserted for the pinned revision meanwhile.
func TestAServingPinsBypassIsDerivedAgainAfterTheCRDReturns(t *testing.T) {
	_, a, r, revB, dB, _ := pinFixture(t, "pinderive")
	pinTo(t, a, dB)
	reconcileOnce(t, r, a)
	bypassed(t, a, revB, dB, pinServing)
	before := *gatesPassed(t, a)
	prev := r.EvalSuiteInstalled
	r.EvalSuiteInstalled = func() bool { return false }
	reconcileOnce(t, r, a)
	r.EvalSuiteInstalled = prev
	reconcileOnce(t, r, a)
	reconcileOnce(t, r, a)
	bypassed(t, a, revB, dB, pinServing)
	after := *gatesPassed(t, a)
	if after.Reason != before.Reason || after.Message != before.Message {
		t.Errorf("GatesPassed moved for a revision serving by pin: %s %q, was %s %q",
			after.Reason, after.Message, before.Reason, before.Message)
	}
}
