// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"strconv"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/evalsuite"
)

// Design 16 A25: status.eval.passed[], and the unpassed statement,
// ActiveRevisionPassUnrecorded=True/NoPassedVerdictRecorded.

func pair(rev, suite string) assaydv1alpha1.EvalPair {
	return assaydv1alpha1.EvalPair{RevisionDigest: rev, SuiteDigest: suite}
}

// passed[] is keyed by revision digest: an append replaces the revision's
// entry at the newest place. At the cap, the oldest entry that does not name
// the active revision is evicted, so the active revision's entry is never
// evicted, however old (A25.4).
func TestPassedIsKeyedByRevisionAndKeepsTheActiveEntry(t *testing.T) {
	var passed []assaydv1alpha1.EvalPair
	passed = appendPassed(passed, pair("a", "s1"), "")
	passed = appendPassed(passed, pair("b", "s1"), "")
	passed = appendPassed(passed, pair("a", "s2"), "")
	if len(passed) != 2 || passed[0] != pair("b", "s1") || passed[1] != pair("a", "s2") {
		t.Fatalf("passed[] is %+v; want b then a, with a's entry replaced at the newest place", passed)
	}

	// The active revision's entry is the oldest when the cap is reached.
	passed = []assaydv1alpha1.EvalPair{pair("active", "s")}
	for i := 0; i < maxPassedPairs-1; i++ {
		passed = appendPassed(passed, pair("r"+strconv.Itoa(i), "s"), "active")
	}
	if len(passed) != maxPassedPairs {
		t.Fatalf("setup: passed[] holds %d entries, want %d", len(passed), maxPassedPairs)
	}
	passed = appendPassed(passed, pair("new", "s"), "active")
	if len(passed) != maxPassedPairs {
		t.Fatalf("passed[] holds %d entries past the cap; the CRD's MaxItems is %d", len(passed), maxPassedPairs)
	}
	if passed[0] != pair("active", "s") {
		t.Errorf("the active revision's entry was evicted: %+v", passed)
	}
	if passed[1] != pair("r1", "s") || passed[maxPassedPairs-1] != pair("new", "s") {
		t.Errorf("passed[] is %+v; want the oldest non-active entry, r0, evicted and new at the end", passed)
	}
	for i, p := range passed {
		for _, q := range passed[i+1:] {
			if p.RevisionDigest == q.RevisionDigest {
				t.Errorf("two entries name %s: %+v", p.RevisionDigest, passed)
			}
		}
	}
}

// A Failed verdict removes the revision's entry from passed[], in the patch
// that appends to failed[]; a Passed verdict leaves passed[] alone (A25.11 (b)).
func TestAFailedVerdictRemovesTheRevisionsPassedEntry(t *testing.T) {
	suite := &assaydv1alpha1.EvalSuite{Spec: assaydv1alpha1.EvalSuiteSpec{
		Dataset: assaydv1alpha1.EvalDataset{Cases: []assaydv1alpha1.EvalCase{{ID: "a"}}}}}
	th, err := evalsuite.Threshold(suite.Spec)
	if err != nil {
		t.Fatal(err)
	}
	run := &evalRun{suite: suite, threshold: th, revisionDigest: "b", suiteDigest: "s2"}
	start := func() *assaydv1alpha1.EvalStatus {
		return &assaydv1alpha1.EvalStatus{Passed: []assaydv1alpha1.EvalPair{pair("a", "s1"), pair("b", "s1")}}
	}
	e := run.withOutcome(start(), "a", "n", assaydv1alpha1.EvalCaseFailed, "Mismatch")
	if e.Verdict != assaydv1alpha1.EvalVerdictFailed || len(e.Passed) != 1 || e.Passed[0] != pair("a", "s1") {
		t.Errorf("after b's Failed verdict passed[] is %+v; want a's entry alone", e.Passed)
	}
	e = run.withOutcome(start(), "a", "n", assaydv1alpha1.EvalCasePassed, "")
	if e.Verdict != assaydv1alpha1.EvalVerdictPassed || len(e.Passed) != 2 {
		t.Errorf("after b's Passed verdict passed[] is %+v; want it unchanged", e.Passed)
	}
}

// The run-start patch keeps passed[] and appends the replaced record's pair
// when that record holds a Passed verdict, and only then (A25.4 (b2)).
func TestTheRunStartMovesAPassedRecordIntoPassed(t *testing.T) {
	for _, tc := range []struct {
		name    string
		verdict assaydv1alpha1.EvalVerdict
		want    []assaydv1alpha1.EvalPair
	}{
		{"a Passed record", assaydv1alpha1.EvalVerdictPassed,
			[]assaydv1alpha1.EvalPair{pair("sha256:old", "s0"), pair("sha256:active", "s1")}},
		{"a Failed record", assaydv1alpha1.EvalVerdictFailed, []assaydv1alpha1.EvalPair{pair("sha256:old", "s0")}},
		{"a cleared record", "", []assaydv1alpha1.EvalPair{pair("sha256:old", "s0")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newEvalFixture(t, nil, echoCase("a", "one"))
			a := f.agent(t)
			a.Status.Eval = &assaydv1alpha1.EvalStatus{Suite: "smoke", Revision: "active0001",
				RevisionDigest: "sha256:active", SuiteDigest: "s1", Verdict: tc.verdict, Score: "1/1",
				Passed: []assaydv1alpha1.EvalPair{pair("sha256:old", "s0")}}
			if err := f.c.Status().Update(context.Background(), a); err != nil {
				t.Fatal(err)
			}
			// A Failed record for a revision the spec has left is cleared
			// first (#111), on a pass of its own.
			for i := 0; i < 2 && f.agent(t).Status.Eval.RevisionDigest != "sha256:candidate"; i++ {
				if _, err := f.pass(t); err != nil {
					t.Fatal(err)
				}
			}
			e := f.agent(t).Status.Eval
			if e.RevisionDigest != "sha256:candidate" {
				t.Fatalf("setup: no run started: %+v", e)
			}
			if len(e.Passed) != len(tc.want) {
				t.Fatalf("passed[] is %+v, want %+v", e.Passed, tc.want)
			}
			for i := range tc.want {
				if e.Passed[i] != tc.want[i] {
					t.Errorf("passed[] is %+v, want %+v", e.Passed, tc.want)
				}
			}
		})
	}
}

// The statement's whole message, with and without a pin, and when it does
// not derive (A25.1, A25.9).
func TestTheUnpassedStatementSaysOnlyWhatIsRecorded(t *testing.T) {
	const d = "sha256:active"
	agent := &assaydv1alpha1.Agent{}
	st := &assaydv1alpha1.AgentStatus{ActiveRevision: "rev", ActiveRevisionDigest: d,
		Eval: &assaydv1alpha1.EvalStatus{Revision: "cand", RevisionDigest: "sha256:cand", SuiteDigest: "s",
			Verdict: assaydv1alpha1.EvalVerdictRunning}}
	head := "revision rev (digest sha256:active) is the active revision, and no Passed verdict is recorded " +
		"for it in status.eval or status.eval.passed[]. A gate evaluates each new revision this spec mints " +
		"before it takes traffic, not the revision already active. This condition is withdrawn when a " +
		"revision with a recorded Passed verdict is promoted. "
	unpinned := head + "The next revision this spec mints is held for its gate before it takes traffic."
	if got := activeRevisionPassUnrecorded(agent, st); got != unpinned {
		t.Errorf("unpinned\n got %q\nwant %q", got, unpinned)
	}
	// It names no status.eval pair, so a run start, a claim and a case record
	// leave it unchanged.
	for _, w := range []string{"sha256:cand", "Running"} {
		if strings.Contains(activeRevisionPassUnrecorded(agent, st), w) {
			t.Errorf("the statement names status.eval's pair (%q)", w)
		}
	}
	st.Eval.Cases = []assaydv1alpha1.EvalCaseRecord{{ID: "a", Outcome: assaydv1alpha1.EvalCasePassed}}
	if got := activeRevisionPassUnrecorded(agent, st); got != unpinned {
		t.Errorf("a case record changed the statement: %q", got)
	}
	// release: {} sets no pin.
	agent.Spec.Release = &assaydv1alpha1.ReleaseSpec{}
	if got := activeRevisionPassUnrecorded(agent, st); got != unpinned {
		t.Errorf("an empty spec.release\n got %q\nwant %q", got, unpinned)
	}
	agent.Spec.Release = &assaydv1alpha1.ReleaseSpec{TargetRevisionDigest: "sha256:pinned"}
	pinned := head + "While spec.release.targetRevisionDigest is set, no revision this spec mints is " +
		"promoted; GatesBypassed or Degraded says what the pin does, except on a pass that stops earlier, " +
		"such as one on an unresolved env source."
	if got := activeRevisionPassUnrecorded(agent, st); got != pinned {
		t.Errorf("pinned\n got %q\nwant %q", got, pinned)
	}

	// Recorded in the pair, or in passed[]: it does not derive.
	st.Eval = &assaydv1alpha1.EvalStatus{RevisionDigest: d, Verdict: assaydv1alpha1.EvalVerdictPassed}
	if got := activeRevisionPassUnrecorded(agent, st); got != "" {
		t.Errorf("a Passed pair for the active revision still reads the statement: %q", got)
	}
	st.Eval = &assaydv1alpha1.EvalStatus{RevisionDigest: "sha256:cand", Verdict: assaydv1alpha1.EvalVerdictFailed,
		Passed: []assaydv1alpha1.EvalPair{pair(d, "s1")}}
	if got := activeRevisionPassUnrecorded(agent, st); got != "" {
		t.Errorf("a passed[] entry for the active revision still reads the statement: %q", got)
	}
	// Another revision's pass, a Failed pair of its own, or nothing at all:
	// it derives.
	for _, e := range []*assaydv1alpha1.EvalStatus{
		nil,
		{RevisionDigest: "sha256:cand", Verdict: assaydv1alpha1.EvalVerdictPassed,
			Passed: []assaydv1alpha1.EvalPair{pair("sha256:other", "s1")}},
		{RevisionDigest: d, Verdict: assaydv1alpha1.EvalVerdictFailed},
	} {
		st.Eval = e
		if got := activeRevisionPassUnrecorded(agent, st); got != pinned {
			t.Errorf("status.eval %+v: got %q", e, got)
		}
	}
	// No active revision digest: nothing is named.
	st.ActiveRevisionDigest = ""
	if got := activeRevisionPassUnrecorded(agent, st); got != "" {
		t.Errorf("an Agent with no active revision digest reads the statement: %q", got)
	}
}

// The promotion branch reads passed[] when the spec mints the active
// revision, and asserts EvalPassed from its entry, with no score (A25.5).
func TestAPassedEntryReadsEvalPassedWithNoScore(t *testing.T) {
	c := newConditionSet(1)
	e := &assaydv1alpha1.EvalStatus{Revision: "c", RevisionDigest: "sha256:c", SuiteDigest: "s3",
		Verdict: assaydv1alpha1.EvalVerdictFailed, Passed: []assaydv1alpha1.EvalPair{pair("sha256:b", "s1")}}
	assessPromotedGate(c, e, "b", "sha256:b")
	got, _ := c.get(assaydv1alpha1.CondGatesPassed)
	want := "revision b (digest sha256:b) passed against suite digest s1, as status.eval.passed[] records; " +
		"passed[] keeps no score"
	if got.Status != metav1.ConditionTrue || got.Reason != ReasonEvalPassed || got.Message != want {
		t.Errorf("GatesPassed is %s/%s %q; want True/EvalPassed %q", got.Status, got.Reason, got.Message, want)
	}
	// The pair outranks an entry: a Passed pair for b under S2 is named,
	// with its score, beside b's older entry under S1.
	c = newConditionSet(1)
	assessPromotedGate(c, &assaydv1alpha1.EvalStatus{Suite: "smoke", Revision: "b", RevisionDigest: "sha256:b",
		SuiteDigest: "s2", Verdict: assaydv1alpha1.EvalVerdictPassed, Score: "1/1",
		Passed: []assaydv1alpha1.EvalPair{pair("sha256:b", "s1")}}, "b", "sha256:b")
	got, _ = c.get(assaydv1alpha1.CondGatesPassed)
	if got.Reason != ReasonEvalPassed || !strings.Contains(got.Message, "(digest s2), score 1/1") ||
		strings.Contains(got.Message, "s1") {
		t.Errorf("the pair did not outrank the entry: %s %q", got.Reason, got.Message)
	}
	e.Passed = nil
	c = newConditionSet(1)
	assessPromotedGate(c, e, "b", "sha256:b")
	got, _ = c.get(assaydv1alpha1.CondGatesPassed)
	if got.Reason != ReasonActiveRevisionNotEvaluated ||
		!strings.Contains(got.Message, "status.eval.passed[] holds no entry for it") {
		t.Errorf("with no entry GatesPassed is %s %q", got.Reason, got.Message)
	}
}

// A CRD that drops only passed holds the candidate, and its message says what
// is lost; with another field missing beside it, the general message stands
// (A25.6).
func TestACRDWithoutPassedSaysWhatIsLost(t *testing.T) {
	in := gateInput{desired: gRev, desiredDigest: gDigest, missing: []string{"passed"},
		status: &assaydv1alpha1.AgentStatus{}}
	d := decideGate(in)
	if d.satisfied || d.reason != ReasonEvalRecordNotKept {
		t.Fatalf("a CRD without passed: %+v", d)
	}
	for _, w := range []string{"status.eval.passed", "a Passed record that a later run start replaces is lost",
		"until this release's CRDs are applied", "ActiveRevisionPassUnrecorded may name an active revision that passed"} {
		if !strings.Contains(d.message, w) {
			t.Errorf("the message does not say %q: %s", w, d.message)
		}
	}
	if strings.Contains(d.message, "no verdict could be kept") {
		t.Errorf("the message says no verdict could be kept, false when only passed is missing: %s", d.message)
	}
	in.missing = []string{"failed", "passed"}
	if d := decideGate(in); !strings.Contains(d.message, "no verdict could be kept") ||
		!strings.Contains(d.message, "failed, passed") {
		t.Errorf("with failed missing too: %s", d.message)
	}
}

// At the run start, the eviction reads the active revision digest from the
// Agent the patch is made against: with passed[] full and the active
// revision's entry the oldest, the run start that moves another revision's
// Passed record in evicts the oldest entry that is not the active one, so a
// run start never makes the statement appear (A25.4).
func TestTheRunStartNeverEvictsTheActiveRevisionsEntry(t *testing.T) {
	f := newEvalFixture(t, nil, echoCase("a", "one"))
	a := f.agent(t)
	passed := []assaydv1alpha1.EvalPair{pair("sha256:active", "s0")}
	for i := 0; i < maxPassedPairs-1; i++ {
		passed = append(passed, pair("sha256:r"+strconv.Itoa(i), "s0"))
	}
	a.Status.Eval = &assaydv1alpha1.EvalStatus{Suite: "smoke", Revision: "held000001",
		RevisionDigest: "sha256:held", SuiteDigest: "s1", Verdict: assaydv1alpha1.EvalVerdictPassed, Score: "1/1",
		Passed: passed}
	if err := f.c.Status().Update(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pass(t); err != nil {
		t.Fatal(err)
	}
	e := f.agent(t).Status.Eval
	if e.RevisionDigest != "sha256:candidate" {
		t.Fatalf("setup: no run started: %+v", e)
	}
	if len(e.Passed) != maxPassedPairs || e.Passed[0] != pair("sha256:active", "s0") ||
		e.Passed[1] != pair("sha256:r1", "s0") || e.Passed[maxPassedPairs-1] != pair("sha256:held", "s1") {
		t.Errorf("passed[] is %+v; want the active entry kept, r0 evicted, and held's pair newest", e.Passed)
	}
}
