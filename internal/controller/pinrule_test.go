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
	const active, spec, older, s1, s2 = "sha256:active", "sha256:spec", "sha256:older", "sha256:s1", "sha256:s2"
	status := func(failed ...assaydv1alpha1.EvalPair) *assaydv1alpha1.AgentStatus {
		return &assaydv1alpha1.AgentStatus{ActiveRevisionDigest: active, CandidateRevisionDigest: spec,
			Eval: &assaydv1alpha1.EvalStatus{Failed: failed}}
	}
	cases := []struct {
		name        string
		pin, specD  string
		st          *assaydv1alpha1.AgentStatus
		suiteDigest string
		refused     string // a fragment of the reason, or "" for admitted
	}{
		{"the current candidate", spec, spec, status(), s2, "current candidate"},
		{"the current candidate with no current suite", spec, spec, status(), "", "current candidate"},
		{"the revision serving, which the spec also mints", active, active,
			&assaydv1alpha1.AgentStatus{ActiveRevisionDigest: active}, s2, ""},
		// The pinned revision recorded as the candidate while it comes up is
		// not "the current candidate": the spec mints another.
		{"a pinned revision recorded as the candidate", older, spec,
			&assaydv1alpha1.AgentStatus{ActiveRevisionDigest: active, CandidateRevisionDigest: older}, s2, ""},
		{"a failure against the current suite", older, spec,
			status(assaydv1alpha1.EvalPair{RevisionDigest: older, SuiteDigest: s2}), s2, "failed[]"},
		{"a failure against an older suite", older, spec,
			status(assaydv1alpha1.EvalPair{RevisionDigest: older, SuiteDigest: s1}), s2, ""},
		{"a failure, with no current suite", older, spec,
			status(assaydv1alpha1.EvalPair{RevisionDigest: older, SuiteDigest: s2}), "", ""},
		{"another revision's failure", older, spec,
			status(assaydv1alpha1.EvalPair{RevisionDigest: active, SuiteDigest: s2}), s2, ""},
		{"no eval record", older, spec, &assaydv1alpha1.AgentStatus{ActiveRevisionDigest: active}, s2, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := pinIneligible(c.pin, c.specD, c.st, "smoke", c.suiteDigest)
			switch {
			case c.refused == "" && got != "":
				t.Errorf("refused, want admitted: %s", got)
			case c.refused != "" && !strings.Contains(got, c.refused):
				t.Errorf("got %q, want a refusal naming %q", got, c.refused)
			}
		})
	}
}
