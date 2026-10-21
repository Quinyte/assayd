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
