// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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
		st          *assaydv1alpha1.AgentStatus
		suiteDigest string
		refused     string // a fragment of the reason, or "" for admitted
	}{
		{"the recorded candidate", cand, status(), s2, "current candidate"},
		{"the recorded candidate with no current suite", cand, status(), "", "current candidate"},
		{"a failure against the current suite", older, status(assaydv1alpha1.EvalPair{RevisionDigest: older, SuiteDigest: s2}), s2, "failed[]"},
		// failed[] alone holds the pair: the verdict was cleared when the
		// spec moved away from it (#111). The rule reads failed[], never
		// the verdict.
		{"a failure recorded only in failed[]", older, &assaydv1alpha1.AgentStatus{ActiveRevisionDigest: active,
			Eval: &assaydv1alpha1.EvalStatus{RevisionDigest: cand, SuiteDigest: s2,
				Failed: []assaydv1alpha1.EvalPair{{RevisionDigest: older, SuiteDigest: s2}}}}, s2, "failed[]"},
		{"a failure against an older suite", older, status(assaydv1alpha1.EvalPair{RevisionDigest: older, SuiteDigest: s1}), s2, ""},
		{"a failure, with no current suite", older, status(assaydv1alpha1.EvalPair{RevisionDigest: older, SuiteDigest: s2}), "", ""},
		{"another revision's failure", older, status(assaydv1alpha1.EvalPair{RevisionDigest: active, SuiteDigest: s2}), s2, ""},
		{"no eval record", older, &assaydv1alpha1.AgentStatus{ActiveRevisionDigest: active}, s2, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := pinIneligible(c.pin, c.st, "smoke", c.suiteDigest)
			switch {
			case c.refused == "" && got != "":
				t.Errorf("refused, want admitted: %s", got)
			case c.refused != "" && !strings.Contains(got, c.refused):
				t.Errorf("got %q, want a refusal naming %q", got, c.refused)
			}
		})
	}
}

// The admission record is read for the pin's own digest, and only from a
// True/PinnedRollback condition.
func TestPinAdmittedReadsTheRecordForThatDigest(t *testing.T) {
	pin := &releasePin{Digest: "abc", Revision: "r"}
	rec := metav1.Condition{Type: string(assaydv1alpha1.CondGatesBypassed), Status: metav1.ConditionTrue,
		Reason: ReasonPinnedRollback, Message: pinnedRollbackMessage(pin, false, "")}
	if pinAdmitted([]metav1.Condition{rec}, "abc") == nil {
		t.Error("the record for the pin's digest was not read as admitted")
	}
	if pinAdmitted([]metav1.Condition{rec}, "abcd") != nil || pinAdmitted([]metav1.Condition{rec}, "ab") != nil {
		t.Error("a record for another digest was read as admitting this one")
	}
	other := rec
	other.Reason = "SomethingElse"
	if pinAdmitted([]metav1.Condition{other}, "abc") != nil {
		t.Error("a GatesBypassed condition with another reason was read as an admission record")
	}
	other = rec
	other.Status = metav1.ConditionFalse
	if pinAdmitted([]metav1.Condition{other}, "abc") != nil {
		t.Error("a False GatesBypassed condition was read as an admission record")
	}
}
