// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package envtest

import (
	"context"
	"strings"
	"testing"
	"time"

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

// bypassed asserts GatesBypassed=True/PinnedRollback naming the revision.
func bypassed(t *testing.T, a *assaydv1alpha1.Agent, rev, digest string) {
	t.Helper()
	c := condition(liveAgent(t, a), assaydv1alpha1.CondGatesBypassed)
	if c == nil || c.Status != metav1.ConditionTrue || c.Reason != reasonPinnedRollback {
		t.Fatalf("GatesBypassed is %+v; want True/%s", c, reasonPinnedRollback)
	}
	for _, w := range []string{rev, digest} {
		if !strings.Contains(c.Message, w) {
			t.Errorf("GatesBypassed does not name the pinned revision's %q: %s", w, c.Message)
		}
	}
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
		bypassed(t, a, revB, dB)

		markAvailable(t, ns, controller.WorkloadName("pinserved", revB), 1)
		reconcileOnce(t, r, a)
		got = liveAgent(t, a)
		if got.Status.ActiveRevisionDigest != dB || got.Status.Phase != assaydv1alpha1.PhaseReady {
			t.Fatalf("the admitted pin to B did not promote it: active %s, phase %s",
				got.Status.ActiveRevisionDigest, got.Status.Phase)
		}
		bypassed(t, a, revB, dB)
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
		reconcileOnce(t, r, a)
		if got := liveAgent(t, a).Status.ActiveRevisionDigest; got != dB {
			t.Fatalf("with the suite missing, a pin to a digest in failed[] was not admitted (active %s)", got)
		}
		bypassed(t, a, revB, dB)
		c := condition(liveAgent(t, a), assaydv1alpha1.CondGatesBypassed)
		if !strings.Contains(c.Message, `"absent"`) {
			t.Errorf("GatesBypassed does not say the suite could not be read, which is why failed[] "+
				"was not consulted: %s", c.Message)
		}
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
	bypassed(t, g.a, rollback, rollbackDigest)
}
