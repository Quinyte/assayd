// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package envtest

import (
	"context"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/controller"
	"github.com/Quinyte/assayd/internal/revision"
)

// Design 16 A25 (#133): while the gate is enforced, an active revision with
// no recorded Passed verdict — in status.eval's pair or in
// status.eval.passed[] — is named by ActiveRevisionPassUnrecorded=True,
// reason NoPassedVerdictRecorded, pinned or not; and the eval controller's
// run start keeps the Passed record it replaces in passed[].

const reasonNoPassedVerdictRecorded = "NoPassedVerdictRecorded"

// unpassed is the statement as stored, or nil.
func unpassed(t *testing.T, a *assaydv1alpha1.Agent) *metav1.Condition {
	t.Helper()
	return condition(liveAgent(t, a), assaydv1alpha1.CondActiveRevisionPassUnrecorded)
}

// namesUnpassed asserts the statement names the revision and its digest, and
// none of notNamed, which are digests of status.eval pairs.
func namesUnpassed(t *testing.T, a *assaydv1alpha1.Agent, rev, digest string, notNamed ...string) *metav1.Condition {
	t.Helper()
	c := unpassed(t, a)
	if c == nil || c.Status != metav1.ConditionTrue || c.Reason != reasonNoPassedVerdictRecorded {
		t.Fatalf("ActiveRevisionPassUnrecorded is %+v; want True/%s naming %s", c, reasonNoPassedVerdictRecorded, rev)
	}
	for _, w := range []string{"revision " + rev + " (digest " + digest + ") is the active revision"} {
		if !strings.Contains(c.Message, w) {
			t.Errorf("the statement does not say %q: %s", w, c.Message)
		}
	}
	for _, n := range notNamed {
		if n != "" && strings.Contains(c.Message, n) {
			t.Errorf("the statement names a status.eval pair's digest %s: %s", n, c.Message)
		}
	}
	return c
}

func noUnpassed(t *testing.T, a *assaydv1alpha1.Agent, when string) {
	t.Helper()
	if c := unpassed(t, a); c != nil {
		t.Errorf("%s: ActiveRevisionPassUnrecorded is %+v; want none", when, c)
	}
}

const (
	unpinnedSentence = "The next revision this spec mints is held for its gate before it takes traffic."
	pinnedSentence   = "While spec.release.targetRevisionDigest is set, no revision this spec mints is promoted"
)

// A25.1 row 1, A25.8 and A25.9: a revision active before the gate was added
// is named while the spec mints it, beside ActiveRevisionNotEvaluated, and
// throughout the next revision's run; the pass that promotes a revision with
// a recorded Passed verdict withdraws it on that same pass. Then (row 5)
// removing the gate withdraws it.
func TestTheUnpassedStatementNamesARevisionActiveBeforeTheGate(t *testing.T) {
	ns := newNamespace(t)
	sd := mustCreateSuite(t, ns, "smoke")
	a, r := servedUngated(t, ns, "unpassedlate")
	revA, dA := a.Status.ActiveRevision, a.Status.ActiveRevisionDigest
	noUnpassed(t, a, "an ungated Agent")

	mustEdit(t, a, func(a *assaydv1alpha1.Agent) { a.Spec.Gates = []assaydv1alpha1.GateRef{{EvalSuiteRef: "smoke"}} })
	settle(t, r, a)
	c := namesUnpassed(t, a, revA, dA)
	if !strings.Contains(c.Message, unpinnedSentence) || strings.Contains(c.Message, pinnedSentence) {
		t.Errorf("an unpinned Agent's statement does not close with the unpinned sentence: %s", c.Message)
	}
	condIs(t, a, assaydv1alpha1.CondGatesPassed, metav1.ConditionFalse, "ActiveRevisionNotEvaluated")
	if got := liveAgent(t, a); got.Status.Phase != assaydv1alpha1.PhaseReady {
		t.Errorf("the statement moved the phase to %s", got.Status.Phase)
	}

	revB, dB := gatedCandidate(t, r, a, imageB, "smoke")
	// B's run, as the eval controller records it: the statement names the
	// active revision and no status.eval pair, and its message is one text.
	writeAgentStatus(t, a, func(st *assaydv1alpha1.AgentStatus) {
		st.Eval = &assaydv1alpha1.EvalStatus{Suite: "smoke", Revision: revB, RevisionDigest: dB, SuiteDigest: sd,
			Verdict: assaydv1alpha1.EvalVerdictRunning}
		putCard(st, revB, dB, time.Now())
	})
	reconcileOnce(t, r, a)
	first := namesUnpassed(t, a, revA, dA, dB, sd).Message
	writeAgentStatus(t, a, func(st *assaydv1alpha1.AgentStatus) {
		st.Eval.Cases = []assaydv1alpha1.EvalCaseRecord{{ID: "arithmetic", Outcome: assaydv1alpha1.EvalCaseSending, Claim: "n"}}
	})
	reconcileOnce(t, r, a)
	if got := namesUnpassed(t, a, revA, dA, dB, sd).Message; got != first {
		t.Errorf("a case claim changed the statement:\n%s\n%s", first, got)
	}

	// The promoting pass, alone: B's Passed verdict is recorded, and the one
	// pass that promotes B writes no statement naming A.
	writeAgentStatus(t, a, func(st *assaydv1alpha1.AgentStatus) {
		st.Eval.Verdict, st.Eval.Score, st.Eval.Cases = assaydv1alpha1.EvalVerdictPassed, "1/1", nil
	})
	reconcileOnce(t, r, a)
	if got := liveAgent(t, a).Status.ActiveRevisionDigest; got != dB {
		t.Fatalf("setup: B was not promoted on its Passed verdict (active %s)", got)
	}
	noUnpassed(t, a, "the pass that promoted B, which passed")

	// Row 5: B's record replaced by hand so that the statement derives again,
	// then the gate removed.
	writeAgentStatus(t, a, func(st *assaydv1alpha1.AgentStatus) {
		st.Eval = &assaydv1alpha1.EvalStatus{Suite: "smoke", Revision: "other", RevisionDigest: "sha256:other",
			SuiteDigest: sd, Verdict: assaydv1alpha1.EvalVerdictRunning}
	})
	reconcileOnce(t, r, a)
	namesUnpassed(t, a, revB, dB, "sha256:other")
	mustEdit(t, a, func(a *assaydv1alpha1.Agent) { a.Spec.Gates = nil })
	reconcileOnce(t, r, a)
	noUnpassed(t, a, "the gate removed")
}

// A25.1 row 2 and A25.9: a revision promoted by pin with no Passed verdict is
// named, with the pin sentence, and once the pin is removed and the spec
// mints C, throughout C's run, with the unpinned sentence.
func TestTheUnpassedStatementNamesAPinnedRevisionOnceItIsUnpinned(t *testing.T) {
	_, a, r, revB, dB, sd := pinFixture(t, "unpassedpin")
	pinTo(t, a, dB)
	reconcileOnce(t, r, a)
	if got := liveAgent(t, a).Status.ActiveRevisionDigest; got != dB {
		t.Fatalf("setup: the pin did not promote B (active %s)", got)
	}
	bypassed(t, a, revB, dB, pinServing)
	c := namesUnpassed(t, a, revB, dB)
	if !strings.Contains(c.Message, pinnedSentence) || strings.Contains(c.Message, unpinnedSentence) {
		t.Errorf("a pinned Agent's statement does not close with the pin sentence: %s", c.Message)
	}

	mustEdit(t, a, func(a *assaydv1alpha1.Agent) { a.Spec.Release = nil })
	reconcileOnce(t, r, a)
	reconcileOnce(t, r, a)
	got := liveAgent(t, a)
	revC, dC := got.Status.CandidateRevision, got.Status.CandidateRevisionDigest
	if got.Status.ActiveRevisionDigest != dB || dC == "" || dC == dB {
		t.Fatalf("setup: want B active and C the candidate: %+v", got.Status)
	}
	if b := condition(got, assaydv1alpha1.CondGatesBypassed); b != nil {
		t.Fatalf("setup: GatesBypassed stands without the pin: %+v", b)
	}
	c = namesUnpassed(t, a, revB, dB, dC)
	if !strings.Contains(c.Message, unpinnedSentence) || strings.Contains(c.Message, pinnedSentence) {
		t.Errorf("the unpinned statement does not close with the unpinned sentence: %s", c.Message)
	}
	want := c.Message
	for _, edit := range []func(*assaydv1alpha1.AgentStatus){
		func(st *assaydv1alpha1.AgentStatus) {
			st.Eval = &assaydv1alpha1.EvalStatus{Suite: "smoke", Revision: revC, RevisionDigest: dC,
				SuiteDigest: sd, Verdict: assaydv1alpha1.EvalVerdictRunning}
		},
		func(st *assaydv1alpha1.AgentStatus) {
			st.Eval.Cases = []assaydv1alpha1.EvalCaseRecord{{ID: "arithmetic", Outcome: assaydv1alpha1.EvalCaseFailed,
				Reason: "Mismatch"}}
		},
	} {
		writeAgentStatus(t, a, edit)
		reconcileOnce(t, r, a)
		if got := namesUnpassed(t, a, revB, dB, dC).Message; got != want {
			t.Errorf("C's run changed the statement:\n%s\n%s", want, got)
		}
	}
}

// A25.1 row 6: a refused pin returns early on every pass, and the statement
// still names the active revision, as GatesBypassed's serving form survives
// an early exit.
func TestTheUnpassedStatementSurvivesARefusedPin(t *testing.T) {
	g := newEvalRig(t, "unpassedrefused", echo("a", "one"))
	namesUnpassed(t, g.a, g.activeName, g.active, g.dgst)
	pinTo(t, g.a, g.dgst)
	for i := 0; i < 2; i++ {
		g.agentPass(t)
		refusedPin(t, g.a, g.dgst, "current candidate")
		namesUnpassed(t, g.a, g.activeName, g.active, g.dgst)
	}
}

// A25.1 row 4: the condition written by hand on an Agent whose active
// revision passed is withdrawn on the next pass, because it is owned.
func TestAForgedUnpassedStatementIsWithdrawn(t *testing.T) {
	g := newEvalRig(t, "unpassedforged", echo("a", "one"))
	g.both(t, 1)
	if got := liveAgent(t, g.a).Status.ActiveRevisionDigest; got != g.dgst {
		t.Fatalf("setup: B did not promote: %+v", g.eval(t))
	}
	noUnpassed(t, g.a, "B promoted on its Passed verdict")
	writeAgentStatus(t, g.a, func(st *assaydv1alpha1.AgentStatus) {
		st.Conditions = append(st.Conditions, metav1.Condition{
			Type: string(assaydv1alpha1.CondActiveRevisionPassUnrecorded), Status: metav1.ConditionTrue,
			Reason: reasonNoPassedVerdictRecorded, Message: "forged", LastTransitionTime: metav1.Now()})
	})
	g.agentPass(t)
	noUnpassed(t, g.a, "the pass after a forged statement")
}

// A25.4: the upgrade residue. A Passed record replaced before passed[]
// existed leaves no entry, and the statement names the active revision,
// which passed. status.eval is written by hand, as an older operator left it.
func TestAPassReplacedBeforeTheUpgradeIsNotRecorded(t *testing.T) {
	ns := newNamespace(t)
	sd := mustCreateSuite(t, ns, "smoke")
	a, r := servedUngated(t, ns, "unpassedupgrade")
	revB, dB := gatedCandidate(t, r, a, imageB, "smoke")
	writeAgentStatus(t, a, func(st *assaydv1alpha1.AgentStatus) {
		st.Eval = &assaydv1alpha1.EvalStatus{Suite: "smoke", Revision: revB, RevisionDigest: dB,
			SuiteDigest: sd, Verdict: assaydv1alpha1.EvalVerdictPassed, Score: "1/1"}
		putCard(st, revB, dB, time.Now())
	})
	reconcileOnce(t, r, a)
	noUnpassed(t, a, "B promoted on its Passed verdict")
	revC, dC := gatedCandidate(t, r, a, imageOf('c'), "smoke")
	writeAgentStatus(t, a, func(st *assaydv1alpha1.AgentStatus) {
		st.Eval = &assaydv1alpha1.EvalStatus{Suite: "smoke", Revision: revC, RevisionDigest: dC,
			SuiteDigest: sd, Verdict: assaydv1alpha1.EvalVerdictRunning}
	})
	reconcileOnce(t, r, a)
	namesUnpassed(t, a, revB, dB, dC)
}

// A25.6: a CRD that does not keep status.eval.passed holds a gated
// candidate under EvalRecordNotKept, and its message says what is lost.
func TestAnAgentCRDWithoutPassedHoldsTheCandidate(t *testing.T) {
	ns := newNamespace(t)
	mustCreateSuite(t, ns, "smoke")
	a, r := servedUngated(t, ns, "nopassed")
	// Registered first, so it runs after the helper has put the release's
	// CRD back: the helper's own wait does not read passed.
	t.Cleanup(func() {
		eventually(t, "the release's Agent CRD to keep status.eval.passed again", func() bool {
			probe := liveAgent(t, a)
			probe.Status.Eval = &assaydv1alpha1.EvalStatus{Passed: []assaydv1alpha1.EvalPair{{RevisionDigest: "probe"}}}
			if err := k8s.Status().Update(context.Background(), probe, client.DryRunAll); err != nil {
				return false
			}
			return probe.Status.Eval != nil && len(probe.Status.Eval.Passed) == 1
		})
	})
	installAgentCRDWithoutEvalFields(t, a, "passed")
	rev, digest := gatedCandidate(t, r, a, imageB, "smoke")
	writeAgentStatus(t, a, func(st *assaydv1alpha1.AgentStatus) { putCard(st, rev, digest, time.Now()) })
	reconcileOnce(t, r, a)
	c := gatesPassed(t, a)
	if c.Status != metav1.ConditionFalse || c.Reason != "EvalRecordNotKept" {
		t.Fatalf("GatesPassed is %s/%s, want False/EvalRecordNotKept", c.Status, c.Reason)
	}
	for _, w := range []string{"status.eval.passed", "a Passed record that a later run start replaces is lost",
		"charts/assayd/crds/", "ActiveRevisionPassUnrecorded may name an active revision that passed"} {
		if !strings.Contains(c.Message, w) {
			t.Errorf("EvalRecordNotKept does not say %q: %s", w, c.Message)
		}
	}
	if got := liveAgent(t, a); got.Status.ActiveRevisionDigest == digest || got.Status.Phase != assaydv1alpha1.PhaseHeld {
		t.Errorf("the candidate was not held: active %s, phase %s", got.Status.ActiveRevisionDigest, got.Status.Phase)
	}
}

// driveUntil runs eval and Agent passes in turn until done holds.
func driveUntil(t *testing.T, g *evalRig, what string, done func(*assaydv1alpha1.Agent) bool) {
	t.Helper()
	for i := 0; i < 8; i++ {
		if done(liveAgent(t, g.a)) {
			return
		}
		g.evalPass(t)
		g.agentPass(t)
	}
	if !done(liveAgent(t, g.a)) {
		t.Fatalf("setup: %s never happened: %+v", what, liveAgent(t, g.a).Status)
	}
}

// toImage edits the spec to image and brings the revision it mints up.
func toImage(t *testing.T, g *evalRig, image string) (string, string) {
	t.Helper()
	mustEdit(t, g.a, func(a *assaydv1alpha1.Agent) { a.Spec.Runtime.Image = image; a.Spec.Release = nil })
	spec := liveAgent(t, g.a).Spec
	rev, d := revision.MustHash(spec), revision.MustDigest(spec)
	g.agentPass(t)
	g.agentPass(t)
	markAvailable(t, g.ns, controller.WorkloadName(g.name, rev), 1)
	g.agentPass(t)
	return rev, d
}

func activeIs(d string) func(*assaydv1alpha1.Agent) bool {
	return func(a *assaydv1alpha1.Agent) bool { return a.Status.ActiveRevisionDigest == d }
}

func hasPassed(e *assaydv1alpha1.EvalStatus, d, sd string) bool {
	if e == nil {
		return false
	}
	for _, p := range e.Passed {
		if p.RevisionDigest == d && (sd == "" || p.SuiteDigest == sd) {
			return true
		}
	}
	return false
}

const imageC = "ghcr.io/acme/agent@sha256:c3c3000000000000000000000000000000000000000000000000000000000000"

// passedTwice is B passed against S1 and promoted, then C passed against S1
// and promoted, all through the real eval controller: C's run start moved
// (B, S1) into passed[].
func passedTwice(t *testing.T, name string) (g *evalRig, revC, dC string) {
	t.Helper()
	g = newEvalRig(t, name, echo("a", "one"))
	driveUntil(t, g, "B promoted", activeIs(g.dgst))
	revC, dC = toImage(t, g, imageC)
	driveUntil(t, g, "C promoted", activeIs(dC))
	if e := g.eval(t); !hasPassed(e, g.dgst, g.suiteDgst) || hasPassed(e, dC, "") {
		t.Fatalf("setup: passed[] is %+v; want (B, S1) alone", e.Passed)
	}
	return g, revC, dC
}

// A25.11, the later failure: B's entry is removed by its Failed verdict
// against S2, so once B, pinned again under S3, is unpinned, the statement
// names it, and goes on naming it while C fails against S3. C, active while
// B's S2 run is recorded, is not named, because passed[] holds C.
func TestALaterFailureRemovesThePassedEntry(t *testing.T) {
	g, revC, dC := passedTwice(t, "laterfailure")
	sd2 := putSuite(t, g.ns, "smoke", evalCase("a", "one", "nope", ""))
	toImage(t, g, imageB)
	driveUntil(t, g, "B's Failed verdict against S2", func(a *assaydv1alpha1.Agent) bool {
		e := a.Status.Eval
		return e != nil && e.RevisionDigest == g.dgst && e.SuiteDigest == sd2 && e.Verdict == assaydv1alpha1.EvalVerdictFailed
	})
	if e := g.eval(t); hasPassed(e, g.dgst, "") || !hasPassed(e, dC, g.suiteDgst) {
		t.Fatalf("after B's Failed verdict passed[] is %+v; want (C, S1) alone", e.Passed)
	}
	noUnpassed(t, g.a, "C active while B's S2 run is recorded")

	toImage(t, g, imageC)
	g.both(t, 2)
	noUnpassed(t, g.a, "C active and minted again")
	putSuite(t, g.ns, "smoke", evalCase("a", "one", "never", ""))
	pinTo(t, g.a, g.dgst)
	g.agentPass(t)
	if got := liveAgent(t, g.a).Status.ActiveRevisionDigest; got != g.dgst {
		t.Fatalf("setup: the pin to B was not admitted (active %s)", got)
	}
	c := bypassed(t, g.a, g.rev, g.dgst, pinServing)
	if !strings.Contains(c.Message, sd2) {
		t.Errorf("the serving form does not name B's S2 record: %s", c.Message)
	}
	namesUnpassed(t, g.a, g.rev, g.dgst)

	mustEdit(t, g.a, func(a *assaydv1alpha1.Agent) { a.Spec.Release = nil })
	g.agentPass(t)
	namesUnpassed(t, g.a, g.rev, g.dgst, dC)
	driveUntil(t, g, "C's Failed verdict against S3", func(a *assaydv1alpha1.Agent) bool {
		e := a.Status.Eval
		return e != nil && e.RevisionDigest == dC && e.Verdict == assaydv1alpha1.EvalVerdictFailed
	})
	namesUnpassed(t, g.a, g.rev, g.dgst, dC)
	_ = revC
}

// A25.11's residue, the suite in force never run: B passed S1 and C was
// promoted on S1; the suite is edited to S2 and B pinned, then unpinned. B
// never ran S2, and its entry from S1 stands, so nothing names it.
func TestARevisionThatNeverRanTheSuiteInForceReadsAsPassed(t *testing.T) {
	g, _, dC := passedTwice(t, "neverran")
	putSuite(t, g.ns, "smoke", evalCase("a", "one", "nope", ""))
	pinTo(t, g.a, g.dgst)
	g.agentPass(t)
	if got := liveAgent(t, g.a).Status.ActiveRevisionDigest; got != g.dgst {
		t.Fatalf("setup: the pin to B was not admitted (active %s)", got)
	}
	noUnpassed(t, g.a, "B pinned, with (B, S1) in passed[]")
	mustEdit(t, g.a, func(a *assaydv1alpha1.Agent) { a.Spec.Release = nil })
	g.agentPass(t)
	noUnpassed(t, g.a, "B unpinned, never run against S2 (the residue)")
	_ = dC
}

// revertedToB is B passed and promoted; C's run start moved B's pass into
// passed[], and C failed; then the spec reverted to B, the revision active.
func revertedToB(t *testing.T, name string) (*evalRig, string) {
	t.Helper()
	g := newEvalRig(t, name, echo("a", "one"))
	driveUntil(t, g, "B promoted", activeIs(g.dgst))
	putSuite(t, g.ns, "smoke", evalCase("a", "one", "nope", ""))
	_, dC := toImage(t, g, imageC)
	driveUntil(t, g, "C's Failed verdict", func(a *assaydv1alpha1.Agent) bool {
		e := a.Status.Eval
		return e != nil && e.RevisionDigest == dC && e.Verdict == assaydv1alpha1.EvalVerdictFailed
	})
	if !hasPassed(g.eval(t), g.dgst, g.suiteDgst) {
		t.Fatalf("setup: passed[] is %+v; want (B, S1)", g.eval(t).Passed)
	}
	toImage(t, g, imageB)
	g.both(t, 2)
	if got := liveAgent(t, g.a).Status.ActiveRevisionDigest; got != g.dgst {
		t.Fatalf("setup: B is not active (%s)", got)
	}
	return g, dC
}

// A25.5's revert row: a revert to a revision that passed, after a later
// candidate's run start replaced its record, reads EvalPassed from the
// passed[] entry, with no score.
func TestARevertToARevisionThatPassedReadsEvalPassed(t *testing.T) {
	g, _ := revertedToB(t, "revertpassed")
	c := condIs(t, g.a, assaydv1alpha1.CondGatesPassed, metav1.ConditionTrue, controller.ReasonEvalPassed)
	if !strings.Contains(c.Message, g.suiteDgst) || !strings.Contains(c.Message, "keeps no score") ||
		strings.Contains(c.Message, "score 1/1") {
		t.Errorf("EvalPassed from passed[] does not name the entry's suite digest and no score: %s", c.Message)
	}
	noUnpassed(t, g.a, "B active again, in passed[]")
}

// A25.5's pin row: a pin to a revision that is not active and that passed[]
// holds reads GatesBypassed's serving form, naming the entry, and leaves
// GatesPassed's reason and message as they were before the pin.
func TestAPinToARevisionPassedHoldsKeepsGatesPassed(t *testing.T) {
	g, _, _ := passedTwice(t, "pinpassedentry")
	before := *gatesPassed(t, g.a)
	pinTo(t, g.a, g.dgst)
	g.agentPass(t)
	if got := liveAgent(t, g.a).Status.ActiveRevisionDigest; got != g.dgst {
		t.Fatalf("setup: the pin to B did not promote it (active %s)", got)
	}
	g.agentPass(t)
	c := bypassed(t, g.a, g.rev, g.dgst, pinServing)
	if !strings.Contains(c.Message, "status.eval.passed[] records a Passed verdict for it against suite digest "+g.suiteDgst) {
		t.Errorf("the serving form does not name B's passed[] entry: %s", c.Message)
	}
	after := *gatesPassed(t, g.a)
	if after.Reason != before.Reason || after.Message != before.Message {
		t.Errorf("GatesPassed moved under the pin: %s %q, was %s %q", after.Reason, after.Message,
			before.Reason, before.Message)
	}
}

// A25.5's revert-then-pin row: after the revert to B, which reads EvalPassed
// from passed[], a pin to B keeps that reading, and the serving form names
// the entry and says neither that whether it passed is not recorded nor that
// it keeps describing the revision status.eval names.
func TestARevertThenPinNamesThePassedEntry(t *testing.T) {
	g, _ := revertedToB(t, "revertpin")
	condIs(t, g.a, assaydv1alpha1.CondGatesPassed, metav1.ConditionTrue, controller.ReasonEvalPassed)
	pinTo(t, g.a, g.dgst)
	g.agentPass(t)
	g.agentPass(t)
	c := condIs(t, g.a, assaydv1alpha1.CondGatesPassed, metav1.ConditionTrue, controller.ReasonEvalPassed)
	if !strings.Contains(c.Message, g.dgst) {
		t.Errorf("EvalPassed does not name B: %s", c.Message)
	}
	b := bypassed(t, g.a, g.rev, g.dgst, pinServing)
	if !strings.Contains(b.Message, "status.eval.passed[] records a Passed verdict for it against suite digest "+g.suiteDgst) {
		t.Errorf("the serving form does not name B's passed[] entry: %s", b.Message)
	}
	for _, w := range []string{"is not recorded", "keeps describing the revision status.eval names"} {
		if strings.Contains(b.Message, w) {
			t.Errorf("the serving form says %q beside a passed[] entry and EvalPassed: %s", w, b.Message)
		}
	}
}

// A25.4's two-controller row, in one manager: A passes and is promoted; B's
// run starts and moves A's pass into passed[]; no statement names A, and the
// Agent reconciler writes no status while B's cases record; B fails, and the
// statement is still absent.
func TestThePassedRecordOutlivesTheNextRunBesideTheAgentReconciler(t *testing.T) {
	ns := newNamespace(t)
	sd1 := putSuite(t, ns, "smoke", echo("a", "one"))
	stub := newA2AStub()
	stub.delay = 300 * time.Millisecond
	mgr := newTestManager(t)
	spy := &statusSpy{Client: mgr.GetClient()}
	r, err := controller.NewAgentReconciler(spy, mgr.GetAPIReader(), mgr.GetScheme(), operatorNamespace,
		func() bool { return true }, labelAuthorityPresent, controller.InjectedEnvConfig{}, controller.GatewayConfig{})
	if err != nil {
		t.Fatal(err)
	}
	r.CardClient = stub
	r.CardFetchTimeout = time.Second
	if err := r.SetupWithManager(mgr); err != nil {
		t.Fatal(err)
	}
	addEvalController(t, mgr, stub, func() bool { return true }, nil)
	startStoppable(t, mgr)

	const name = "passedkept"
	a := mustCreateAgent(t, ns, name, nil)
	first := revision.MustHash(a.Spec)
	eventually(t, "the first workload", func() bool { return deploymentExists(t, ns, controller.WorkloadName(name, first)) })
	markAvailable(t, ns, controller.WorkloadName(name, first), 1)
	eventually(t, "the first revision to serve", func() bool { return liveAgent(t, a).Status.ActiveRevision == first })

	mustEdit(t, a, func(a *assaydv1alpha1.Agent) {
		a.Spec.Runtime.Image = imageB
		a.Spec.Gates = []assaydv1alpha1.GateRef{{EvalSuiteRef: "smoke"}}
	})
	specA := liveAgent(t, a).Spec
	revA, dA := revision.MustHash(specA), revision.MustDigest(specA)
	eventually(t, "A's workload", func() bool { return deploymentExists(t, ns, controller.WorkloadName(name, revA)) })
	markAvailable(t, ns, controller.WorkloadName(name, revA), 1)
	eventually(t, "A to pass and be promoted", func() bool { return liveAgent(t, a).Status.ActiveRevisionDigest == dA })
	eventually(t, "the statement withdrawn once A is promoted", func() bool { return unpassed(t, a) == nil })

	cases := []assaydv1alpha1.EvalCase{echo("a", "one"), echo("b", "two"), echo("c", "three"),
		echo("d", "four"), evalCase("e", "five", "nope", "")}
	putSuite(t, ns, "smoke", cases...)
	mustEdit(t, a, func(a *assaydv1alpha1.Agent) { a.Spec.Runtime.Image = imageC })
	specB := liveAgent(t, a).Spec
	revB, dB := revision.MustHash(specB), revision.MustDigest(specB)
	eventually(t, "B's workload", func() bool { return deploymentExists(t, ns, controller.WorkloadName(name, revB)) })
	markAvailable(t, ns, controller.WorkloadName(name, revB), 1)
	eventually(t, "B's run to start", func() bool {
		e := liveAgent(t, a).Status.Eval
		return e != nil && e.RevisionDigest == dB
	})
	if e := liveAgent(t, a).Status.Eval; !hasPassed(e, dA, sd1) {
		t.Errorf("B's run start did not move (A, S1) into passed[]: %+v", e.Passed)
	}
	noUnpassed(t, a, "B's run started")
	eventually(t, "B's Failed verdict", func() bool {
		e := liveAgent(t, a).Status.Eval
		return e != nil && e.RevisionDigest == dB && e.Verdict == assaydv1alpha1.EvalVerdictFailed
	})
	time.Sleep(time.Second) // the Agent reconciler's pass on the verdict
	noUnpassed(t, a, "B failed")
	if got := liveAgent(t, a).Status.ActiveRevisionDigest; got != dA {
		t.Errorf("A is no longer active (%s)", got)
	}
	if n := spy.count(); n != 0 {
		t.Errorf("the Agent reconciler wrote status %d times while B's run recorded its cases", n)
	}
}
