// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package conformance

import "testing"

// TestAncestorReportTranscribesPolicyReport pins what the cluster cases MEAN by
// a broken, a holding and a converged policy report, against design 03 §5's
// four shapes and against `internal/controller`'s `policyReport`, which is the
// code these predicates transcribe.
//
// It is a TRANSCRIPTION TEST, not a cross-check: `policyReport` is unexported,
// so nothing here calls it, and every row below is one of ITS cases written
// out by hand. That is worth stating plainly, because the transcription is the
// thing that can drift and it already had — an earlier `broken` called
// `Accepted=Unknown` a break where `policyReport` counts it as known and not
// broken, which would have let a cluster case go green on a state the operator
// treats as clearing. A real cross-check needs `policyReport` exported or
// moved to a shared package, and is owed (A82).
//
// It is here, untagged, for statusIsCurrent's reason: the `Unknown` and
// absent-condition rows are unreachable on agentgateway 1.5.0, which writes
// both conditions with a `True`/`False` status, so nothing a cluster can do
// would exercise the arms that handle them and they would be defensive code no
// test can pin (rule 5). Note that `make test` carries this file and no CI job
// carries `make test`.
func TestAncestorReportTranscribesPolicyReport(t *testing.T) {
	cond := func(status, reason string) ancestorCondition {
		return ancestorCondition{Status: status, Reason: reason}
	}
	realGW := func(conds map[string]ancestorCondition) ancestorReport {
		return ancestorReport{
			Group: "gateway.networking.k8s.io", Kind: "Gateway",
			Name: "assayd", Namespace: "assayd-gateway", Conds: conds,
		}
	}
	synthetic := func(conds map[string]ancestorCondition) ancestorReport {
		return ancestorReport{
			Group: "agentgateway.dev", Kind: "Gateway", Name: "StatusSummary",
			Synthetic: true, Conds: conds,
		}
	}
	for _, tc := range []struct {
		name      string
		rep       ancestorReport
		answer    policyAnswer
		converged bool
	}{
		{
			name:      "converged: the real Gateway, Accepted=True/Valid and Attached=True",
			rep:       realGW(map[string]ancestorCondition{"Accepted": cond("True", "Valid"), "Attached": cond("True", "Attached")}),
			answer:    answerHolding,
			converged: true,
		},
		{
			name:   "§5 shape 1: Accepted=False",
			rep:    realGW(map[string]ancestorCondition{"Accepted": cond("False", "Invalid"), "Attached": cond("True", "Attached")}),
			answer: answerBroken,
		},
		{
			name:   "§5 shape 2: Accepted=True with a reason other than Valid — measured as PartiallyValid",
			rep:    realGW(map[string]ancestorCondition{"Accepted": cond("True", "PartiallyValid"), "Attached": cond("True", "Attached")}),
			answer: answerBroken,
		},
		{
			name:   "§5 shape 3: Attached=False",
			rep:    realGW(map[string]ancestorCondition{"Accepted": cond("True", "Valid"), "Attached": cond("False", "Pending")}),
			answer: answerBroken,
		},
		{
			name:   "§5 shape 4: the synthetic ancestor, even reporting Accepted=True/Valid",
			rep:    synthetic(map[string]ancestorCondition{"Accepted": cond("True", "Valid"), "Attached": cond("False", "Pending")}),
			answer: answerBroken,
		},
		{
			name:   "the synthetic ancestor with no conditions at all: its PRESENCE is the signal (§3.3.2, A81)",
			rep:    synthetic(map[string]ancestorCondition{}),
			answer: answerBroken,
		},
		{
			name: "the synthetic ancestor reporting a PERFECT tuple is still broken and still not " +
				"converged — the ancestor outranks its conditions, which is the only row that " +
				"separates converged's ancestor check from its condition checks",
			rep:    synthetic(map[string]ancestorCondition{"Accepted": cond("True", "Valid"), "Attached": cond("True", "Attached")}),
			answer: answerBroken,
		},
		{
			name: "Accepted=Unknown is NOT a break: policyReport counts it as known and falls through both arms. " +
				"This is the row the first transcription got wrong",
			rep:    realGW(map[string]ancestorCondition{"Accepted": cond("Unknown", "Valid"), "Attached": cond("True", "Attached")}),
			answer: answerHolding,
		},
		{
			name: "Attached=Unknown is not a break either, and it holds — but it does not CONVERGE, " +
				"which is why the two predicates are not one",
			rep:    realGW(map[string]ancestorCondition{"Accepted": cond("True", "Valid"), "Attached": cond("Unknown", "Pending")}),
			answer: answerHolding,
		},
		{
			name:   "Accepted=Unknown with the WRONG reason still holds: the reason arm needs an explicit True",
			rep:    realGW(map[string]ancestorCondition{"Accepted": cond("Unknown", "PartiallyValid"), "Attached": cond("True", "Attached")}),
			answer: answerHolding,
		},
		{
			name:   "unknown: no conditions on the real Gateway's ancestor",
			rep:    realGW(map[string]ancestorCondition{}),
			answer: answerUnknown,
		},
		{
			name:   "unknown: Accepted alone, Attached absent",
			rep:    realGW(map[string]ancestorCondition{"Accepted": cond("True", "Valid")}),
			answer: answerUnknown,
		},
		{
			name:   "unknown: Attached alone, Accepted absent",
			rep:    realGW(map[string]ancestorCondition{"Attached": cond("True", "Attached")}),
			answer: answerUnknown,
		},
		{
			name:   "an absent condition does not hide a break in the other one",
			rep:    realGW(map[string]ancestorCondition{"Attached": cond("False", "Pending")}),
			answer: answerBroken,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.rep.answer(); got != tc.answer {
				t.Errorf("answer() = %v, want %v", got, tc.answer)
			}
			if got := tc.rep.broken(); got != (tc.answer == answerBroken) {
				t.Errorf("broken() = %v, want %v", got, tc.answer == answerBroken)
			}
			if got := tc.rep.converged(); got != tc.converged {
				t.Errorf("converged() = %v, want %v", got, tc.converged)
			}
			if tc.converged && tc.answer != answerHolding {
				t.Fatal("a converged report must also be holding, or the two predicates disagree " +
					"about the state the cluster cases wait for")
			}
		})
	}
}
