// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package conformance

import (
	"fmt"
	"strings"
)

// The reading of an `AgentgatewayPolicy`'s status that design 03 §3.3.2's
// convergence tuple and §5's policy row rest on, and that A80's policy half is
// judged by.
//
// It lives in an UNTAGGED file, separate from the cluster suite it serves, for
// statusIsCurrent's reason (status.go): the rules below decide what a report
// MEANS, and a rule only a `cluster`-tagged run can exercise is pinned by
// nothing that `make test` runs. agentgateway 1.5.0 writes both conditions
// with a `True`/`False` status on every ancestor, so the arms for an absent
// condition and for `Unknown` are unreachable from a cluster and would
// otherwise be defensive code no test can pin (rule 5). They are pinned by
// ancestor_test.go instead.
//
// **This is a TRANSCRIPTION of `internal/controller`'s `policyReport`, not a
// call into it**, because that function is unexported and exporting it is a
// production change to an approved slice. A transcription can drift, and one
// already had: an earlier version of `broken` called `Accepted=Unknown` a
// break where `policyReport` counts it as known and not broken, so a cluster
// case could have gone green on a state the operator treats as CLEARING. The
// table in ancestor_test.go is the code's own cases, row by row, for that
// reason. A real cross-check needs `policyReport` exported or moved to a
// shared package, and is owed (design 03 A82).

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

// ancestorReport is the ancestor of a policy's status that `policyReport`
// would read, and its conditions.
//
// The four ref fields are all carried because `policyReport` compares all
// four: the synthetic ancestor on `group == "agentgateway.dev"` AND
// `name == "StatusSummary"`, and the real one on `group`, `kind`, `name` and
// `namespace`. A case that asserted only the name would stay green through an
// upstream rename of either, while the operator's policy half went silent —
// no `AuthPolicyNotAttached`, ever — which is the regression this suite exists
// to catch and the exact claim A82 merges.
type ancestorReport struct {
	Group, Kind, Name, Namespace string
	Conds                        map[string]ancestorCondition
}

// Synthetic is agentgateway's `StatusSummary` ancestor, spelled as
// `policyReport` keys the fail-open signal on it: the GROUP and the NAME
// together. It is DERIVED rather than stored, so a caller — a test table
// included — cannot set it to something the two ref fields contradict, and the
// rule is exercised by every row rather than asserted in one.
func (a ancestorReport) Synthetic() bool {
	return a.Group == "agentgateway.dev" && a.Name == "StatusSummary"
}

// policyAnswer is `policyReport`'s three answers, spelled as §3.3.3 spells
// them: an explicit failure, an explicit not-failure, and everything else.
type policyAnswer int

const (
	// answerUnknown raises nothing and clears nothing (§3.3.3).
	answerUnknown policyAnswer = iota
	// answerBroken is one of §5's four shapes.
	answerBroken
	// answerHolding is the explicit not-broken reading that CLEARS a standing
	// claim. It is weaker than §3.3.2's convergence tuple: `policyReport`
	// counts a condition whose status is neither `True` nor `False` as known
	// and not broken, so an `Accepted=Unknown` beside `Attached=True` holds
	// rather than converges. `converged` is the stricter reading.
	answerHolding
)

// answer transcribes `policyReport`'s decision, including the order it takes
// it in: the synthetic ancestor first, whatever its conditions say, because
// the ancestor list is rewritten whole and its presence is the signal; then
// the two conditions, each counted only if present, each broken only on an
// explicit `False` or on an explicit `True` with the wrong reason.
//
// The caller has already restricted this to conditions at the policy's current
// generation, which is where `policyReport`'s generation gate lives.
func (a ancestorReport) answer() policyAnswer {
	if a.Synthetic() {
		return answerBroken
	}
	known := 0
	for _, want := range []struct{ typ, reason string }{{"Accepted", "Valid"}, {"Attached", ""}} {
		c, ok := a.Conds[want.typ]
		if !ok {
			continue
		}
		switch {
		case c.Status == "False":
			return answerBroken
		case want.reason != "" && c.Status == "True" && c.Reason != want.reason:
			return answerBroken
		}
		known++
	}
	if known == 2 {
		return answerHolding
	}
	return answerUnknown
}

// broken is §5's four shapes: `Accepted=False`, `Accepted=True` with a reason
// other than `Valid`, `Attached=False`, or the synthetic ancestor.
func (a ancestorReport) broken() bool { return a.answer() == answerBroken }

// converged is §3.3.2's tuple for an `AgentgatewayPolicy`: the real Gateway
// ancestor, `Accepted=True` with reason `Valid`, and `Attached=True`. It is
// what a case waits for when it wants the policy actually working, and it is
// deliberately stricter than answerHolding, which is only "not an explicit
// failure".
func (a ancestorReport) converged() bool {
	acc, hasAcc := a.Conds["Accepted"]
	att, hasAtt := a.Conds["Attached"]
	return !a.Synthetic() && hasAcc && hasAtt &&
		acc.Status == "True" && acc.Reason == "Valid" && att.Status == "True"
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
