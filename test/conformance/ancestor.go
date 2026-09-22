// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package conformance

import (
	"fmt"
	"strings"
)

// The reading of an `AgentgatewayPolicy`'s status that design 03 §3.3.2's
// convergence tuple and §5's policy row rest on, and that A80's policy half
// is judged by.
//
// It lives in an UNTAGGED file, separate from the cluster suite it serves, for
// statusIsCurrent's reason (status.go): the rules below decide what a report
// MEANS, and a rule only a `cluster`-tagged run can exercise is pinned by
// nothing that CI runs. agentgateway 1.5.0 writes both conditions on every
// ancestor, so the "one of them is absent" arm is unreachable from a cluster
// and would otherwise be defensive code no test can pin — rule 5. It is
// pinned by ancestor_test.go instead.

// ancestorCondition is one condition on one policy ancestor, carrying the two
// fields §3.3.2's tuple reads beyond the status: the REASON, which separates
// `Valid` from every translation failure agentgateway still calls accepted,
// and the GENERATION it was observed at, which is what makes the report
// evidence about the object as it stands rather than a stale echo of it.
type ancestorCondition struct {
	Status             string
	Reason             string
	Message            string
	ObservedGeneration int64
}

// ancestorReport is the ancestor of a policy's status that internal/controller's
// policyReport would read, and its conditions.
//
// The four ref fields are all carried because `policyReport` compares all four
// (`internal/controller/authserved.go`): the synthetic ancestor on
// `group == "agentgateway.dev" && name == "StatusSummary"`, and the real one on
// `group`, `kind`, `name` AND `namespace`. A case that asserted only the name
// would stay green through an upstream rename of either, while the operator's
// policy half went silent — no `AuthPolicyNotAttached`, ever — which is the
// regression this suite exists to catch and the exact claim A82 merges.
type ancestorReport struct {
	Group, Kind, Name, Namespace string
	Conds                        map[string]ancestorCondition
	// Synthetic is what policyReport keys the fail-open signal on, spelled the
	// same way, so the two cannot drift apart silently.
	Synthetic bool
}

// broken says whether this report is one of the four §5 calls a broken tuple:
// `Accepted=False`, `Accepted=True` with a reason other than `Valid`,
// `Attached=False`, or the synthetic `StatusSummary` ancestor.
//
// A MISSING condition is not one of them. `policyReport` counts the two it
// knows and answers `reportUnknown` unless it has both, and §5's four shapes
// are all explicit reports; treating absence as breakage would make this
// helper stricter than the code it models, in the fail-open direction, on the
// fail-open half. `healthy` is its complement with the same rule, so an
// ancestor that carries neither condition is neither broken nor healthy and a
// wait on either keeps waiting.
func (a ancestorReport) broken() bool {
	if !a.complete() {
		return a.Synthetic
	}
	return a.Synthetic || a.Conds["Accepted"].Status != "True" ||
		a.Conds["Accepted"].Reason != "Valid" || a.Conds["Attached"].Status != "True"
}

// healthy is §3.3.2's convergence tuple for an `AgentgatewayPolicy`: the real
// Gateway ancestor, `Accepted=True` with reason `Valid`, and `Attached=True`.
func (a ancestorReport) healthy() bool {
	return a.complete() && !a.Synthetic &&
		a.Conds["Accepted"].Status == "True" && a.Conds["Accepted"].Reason == "Valid" &&
		a.Conds["Attached"].Status == "True"
}

func (a ancestorReport) complete() bool {
	_, acc := a.Conds["Accepted"]
	_, att := a.Conds["Attached"]
	return acc && att
}

func (a ancestorReport) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "ancestor %s/%s %s", a.Group, a.Kind, a.Name)
	if a.Namespace != "" {
		fmt.Fprintf(&b, " in %s", a.Namespace)
	}
	for _, k := range []string{"Accepted", "Attached"} {
		if c, ok := a.Conds[k]; ok {
			fmt.Fprintf(&b, "; %s=%s reason=%s gen=%d (%s)", k, c.Status, c.Reason,
				c.ObservedGeneration, strings.TrimSpace(c.Message))
		} else {
			fmt.Fprintf(&b, "; %s ABSENT", k)
		}
	}
	return b.String()
}
