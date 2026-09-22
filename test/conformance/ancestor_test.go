// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package conformance

import "testing"

// TestAncestorReportMatchesPolicyReport pins what the cluster cases MEAN by a
// broken and a healthy policy report, against design 03 §5's four shapes and
// against `internal/controller`'s `policyReport`, which is the code that has
// to agree with them.
//
// It is here, untagged, for statusIsCurrent's reason: two of the rows below —
// an ancestor carrying only one of the two conditions — are unreachable on
// agentgateway 1.5.0, which writes both, so nothing a cluster can do would
// exercise them and the arm that handles them would be defensive code no test
// can pin (rule 5). The rule they encode is `policyReport`'s: it counts the
// two conditions it knows and answers `reportUnknown` unless it has both, so
// an absent condition is NOT one of §5's four breaks. A helper that called it
// broken would be stricter than the code it models, on the fail-open half, and
// a heal wait written against it would pass on a report that says nothing.
func TestAncestorReportMatchesPolicyReport(t *testing.T) {
	ok := func(status, reason string) ancestorCondition {
		return ancestorCondition{Status: status, Reason: reason}
	}
	realGW := func(conds map[string]ancestorCondition) ancestorReport {
		return ancestorReport{
			Group: "gateway.networking.k8s.io", Kind: "Gateway",
			Name: "assayd", Namespace: "assayd-gateway", Conds: conds,
		}
	}
	for _, tc := range []struct {
		name            string
		rep             ancestorReport
		broken, healthy bool
	}{
		{
			name:    "converged: the real Gateway, Accepted=True/Valid and Attached=True",
			rep:     realGW(map[string]ancestorCondition{"Accepted": ok("True", "Valid"), "Attached": ok("True", "Attached")}),
			healthy: true,
		},
		{
			name:   "§5 shape 1: Accepted=False",
			rep:    realGW(map[string]ancestorCondition{"Accepted": ok("False", "Invalid"), "Attached": ok("True", "Attached")}),
			broken: true,
		},
		{
			name:   "§5 shape 2: Accepted=True with a reason other than Valid — measured as PartiallyValid",
			rep:    realGW(map[string]ancestorCondition{"Accepted": ok("True", "PartiallyValid"), "Attached": ok("True", "Attached")}),
			broken: true,
		},
		{
			name:   "§5 shape 3: Attached=False",
			rep:    realGW(map[string]ancestorCondition{"Accepted": ok("True", "Valid"), "Attached": ok("False", "Pending")}),
			broken: true,
		},
		{
			name: "§5 shape 4: the synthetic ancestor, even reporting Accepted=True/Valid",
			rep: ancestorReport{
				Group: "agentgateway.dev", Kind: "Gateway", Name: "StatusSummary", Synthetic: true,
				Conds: map[string]ancestorCondition{"Accepted": ok("True", "Valid"), "Attached": ok("False", "Pending")},
			},
			broken: true,
		},
		{
			name: "the synthetic ancestor is broken even with no conditions at all: its PRESENCE is the signal (§3.3.2, A81)",
			rep: ancestorReport{
				Group: "agentgateway.dev", Kind: "Gateway", Name: "StatusSummary", Synthetic: true,
				Conds: map[string]ancestorCondition{},
			},
			broken: true,
		},
		{
			name: "neither: no conditions on the real Gateway's ancestor is what policyReport calls unknown",
			rep:  realGW(map[string]ancestorCondition{}),
		},
		{
			name: "neither: Accepted alone, Attached absent",
			rep:  realGW(map[string]ancestorCondition{"Accepted": ok("True", "Valid")}),
		},
		{
			name: "neither: Attached alone, Accepted absent",
			rep:  realGW(map[string]ancestorCondition{"Attached": ok("True", "Attached")}),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.rep.broken(); got != tc.broken {
				t.Errorf("broken() = %v, want %v", got, tc.broken)
			}
			if got := tc.rep.healthy(); got != tc.healthy {
				t.Errorf("healthy() = %v, want %v", got, tc.healthy)
			}
			if tc.broken && tc.healthy {
				t.Fatal("this table may not declare a report both broken and healthy")
			}
		})
	}
}
