// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"strings"
	"testing"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
)

// Design 16 Q5 (a), one case per refusal and per admission the rule states.
// T28 and T53 measure the same rule end to end in envtest; this pins its
// edges, which are cheaper to enumerate here.
func TestPinIneligibleFollowsQ5(t *testing.T) {
	const active, cand, older, s1, s2 = "sha256:active", "sha256:cand", "sha256:older", "sha256:s1", "sha256:s2"
	status := func(failed ...assaydv1alpha1.EvalPair) *assaydv1alpha1.AgentStatus {
		return &assaydv1alpha1.AgentStatus{ActiveRevisionDigest: active, CandidateRevisionDigest: cand,
			Eval: &assaydv1alpha1.EvalStatus{Failed: failed}}
	}
	cases := []struct {
		name        string
		pin         string
		spec        string // the digest the current spec mints
		st          *assaydv1alpha1.AgentStatus
		suiteDigest string
		refused     string // a fragment of the reason, or "" for admitted
	}{
		{"the candidate the spec mints", cand, cand, status(), s2, "current candidate"},
		{"the candidate the spec mints, with no current suite", cand, cand, status(), "", "current candidate"},
		// The pin's own revision, recorded by the not-ready branches while it
		// comes up: the spec mints another, so it is not the candidate the
		// gate evaluates.
		{"a candidate the pin itself placed", cand, older, status(), s2, ""},
		// The revision the spec mints is the current candidate whether or
		// not a pass has recorded it yet: one write that reverts the spec to
		// it and pins it is refused as two writes are.
		{"the revision the spec mints, not yet recorded", older, older, status(), s2, "current candidate"},
		// The revision serving is never refused, even when failed[] records
		// it against the suite in force.
		{"the revision serving, failed against the current suite", active, cand,
			status(assaydv1alpha1.EvalPair{RevisionDigest: active, SuiteDigest: s2}), s2, ""},
		{"a failure against the current suite", older, cand, status(assaydv1alpha1.EvalPair{RevisionDigest: older, SuiteDigest: s2}), s2, "failed[]"},
		// failed[] alone holds the pair: the verdict was cleared when the
		// spec moved away from it (#111). The rule reads failed[], never
		// the verdict.
		{"a failure recorded only in failed[]", older, cand, &assaydv1alpha1.AgentStatus{ActiveRevisionDigest: active,
			Eval: &assaydv1alpha1.EvalStatus{RevisionDigest: cand, SuiteDigest: s2,
				Failed: []assaydv1alpha1.EvalPair{{RevisionDigest: older, SuiteDigest: s2}}}}, s2, "failed[]"},
		{"a failure against an older suite", older, cand, status(assaydv1alpha1.EvalPair{RevisionDigest: older, SuiteDigest: s1}), s2, ""},
		{"a failure, with no current suite", older, cand, status(assaydv1alpha1.EvalPair{RevisionDigest: older, SuiteDigest: s2}), "", ""},
		{"another revision's failure", older, cand, status(assaydv1alpha1.EvalPair{RevisionDigest: active, SuiteDigest: s2}), s2, ""},
		{"no eval record", older, cand, &assaydv1alpha1.AgentStatus{ActiveRevisionDigest: active}, s2, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := pinIneligible(c.pin, c.spec, c.st, "smoke", c.suiteDigest)
			switch {
			case c.refused == "" && got != "":
				t.Errorf("refused, want admitted: %s", got)
			case c.refused != "" && !strings.Contains(got, c.refused):
				t.Errorf("got %q, want a refusal naming %q", got, c.refused)
			}
		})
	}
}

// The serving form of GatesBypassed says only what the record holds. One row
// per branch of servingRecord, each asserting the exact sentence, so a clause
// added to any of them — "and it never failed", say — fails here.
func TestServingRecordSaysOnlyWhatStatusEvalHolds(t *testing.T) {
	const d, other, s1, s2 = "sha256:pinned", "sha256:other", "sha256:s1", "sha256:s2"
	failed := func(pairs ...assaydv1alpha1.EvalPair) []assaydv1alpha1.EvalPair { return pairs }
	cases := []struct {
		name string
		e    *assaydv1alpha1.EvalStatus
		want string
	}{
		{"no record", nil, "status.eval records no run."},
		{"a record with no pair", &assaydv1alpha1.EvalStatus{}, "status.eval records no run."},
		{"another revision's pair, failed[] naming none of its own", &assaydv1alpha1.EvalStatus{RevisionDigest: other, SuiteDigest: s1,
			Verdict: assaydv1alpha1.EvalVerdictFailed},
			"status.eval records a run of another revision (digest sha256:other), so whether this revision " +
				"passed its gate or never ran is not recorded."},
		{"its own Failed verdict", &assaydv1alpha1.EvalStatus{RevisionDigest: d, SuiteDigest: s1,
			Verdict: assaydv1alpha1.EvalVerdictFailed, Failed: failed(assaydv1alpha1.EvalPair{RevisionDigest: d, SuiteDigest: s1})},
			"status.eval records a Failed verdict for it against suite digest sha256:s1. status.eval.failed[] " +
				"records 1 failed pair(s) for it, against suite digest(s) sha256:s1."},
		{"its own run with no verdict", &assaydv1alpha1.EvalStatus{RevisionDigest: d, SuiteDigest: s2,
			Verdict: assaydv1alpha1.EvalVerdictRunning},
			"status.eval records a run of it against suite digest sha256:s2 with no verdict yet."},
		{"its own pair, verdict cleared (#111)", &assaydv1alpha1.EvalStatus{RevisionDigest: d, SuiteDigest: s2,
			Failed: failed(assaydv1alpha1.EvalPair{RevisionDigest: d, SuiteDigest: s1},
				assaydv1alpha1.EvalPair{RevisionDigest: other, SuiteDigest: s1},
				assaydv1alpha1.EvalPair{RevisionDigest: d, SuiteDigest: s2})},
			"status.eval names it against suite digest sha256:s2 with its verdict cleared. status.eval.failed[] " +
				"records 2 failed pair(s) for it, against suite digest(s) sha256:s1, sha256:s2."},
		// failed[] records a run of it, so "or never ran" would contradict
		// the next sentence (review 6's m1).
		{"another revision's pair, failed[] naming its own", &assaydv1alpha1.EvalStatus{RevisionDigest: other,
			SuiteDigest: s2, Verdict: assaydv1alpha1.EvalVerdictFailed,
			Failed: failed(assaydv1alpha1.EvalPair{RevisionDigest: d, SuiteDigest: s1})},
			"status.eval records a run of another revision (digest sha256:other), so whether this revision " +
				"passed its gate is not recorded. status.eval.failed[] records 1 failed pair(s) " +
				"for it, against suite digest(s) sha256:s1."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := servingRecord(c.e, d); got != c.want {
				t.Errorf("servingRecord\n got %q\nwant %q", got, c.want)
			}
		})
	}
}

// The whole serving message, once with the admitting pass's reason and once in
// steady state, and nothing for a pinned revision with a Passed verdict.
func TestServingPinBypassIsTheRecordAndNothingElse(t *testing.T) {
	const d = "sha256:pinned"
	agent := &assaydv1alpha1.Agent{Spec: assaydv1alpha1.AgentSpec{
		Release: &assaydv1alpha1.ReleaseSpec{TargetRevisionDigest: d}}}
	st := &assaydv1alpha1.AgentStatus{ActiveRevision: "rev", ActiveRevisionDigest: d,
		Eval: &assaydv1alpha1.EvalStatus{RevisionDigest: d, SuiteDigest: "sha256:s1",
			Verdict: assaydv1alpha1.EvalVerdictFailed}}
	tail := " While the pin stands nothing is evaluated in its place, and GatesPassed is not asserted for " +
		"it, so it keeps describing the revision status.eval names"
	head := "spec.release.targetRevisionDigest pins revision rev (digest sha256:pinned), the revision serving, " +
		"and it serves by pin"
	rec := ". status.eval records a Failed verdict for it against suite digest sha256:s1."
	if got, want := servingPinBypass(agent, st, ""), head+rec+tail; got != want {
		t.Errorf("steady state\n got %q\nwant %q", got, want)
	}
	if got, want := servingPinBypass(agent, st, "why"), head+"; design 16 Q5 (a) admitted the pin: why"+rec+tail; got != want {
		t.Errorf("admitting pass\n got %q\nwant %q", got, want)
	}
	st.Eval.Verdict = assaydv1alpha1.EvalVerdictPassed
	if got := servingPinBypass(agent, st, ""); got != "" {
		t.Errorf("a pinned revision with a Passed verdict still reads a serving form: %q", got)
	}
}
