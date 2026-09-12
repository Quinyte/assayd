// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"reflect"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
)

// conditionSet accumulates the conditions one reconcile pass wants to assert.
//
// Design 02's twenty conditions are a closed vocabulary, and NFR-8 says a
// degraded path must never be silent. Both properties need conditions that
// STICK: a condition set on one pass and forgotten on the next would flap, and a
// flapping warning is one operators learn to ignore. So a pass declares only
// what it observed, and merge() carries forward anything it did not speak to
// while clearing the conditions it owns but did not raise.
type conditionSet struct {
	generation int64
	asserted   map[string]metav1.Condition
}

func newConditionSet(generation int64) *conditionSet {
	return &conditionSet{generation: generation, asserted: map[string]metav1.Condition{}}
}

func (c *conditionSet) set(condType assaydv1alpha1.ConditionType, status metav1.ConditionStatus, reason, message string) {
	c.asserted[string(condType)] = metav1.Condition{
		Type:               string(condType),
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: c.generation,
	}
}

// get returns what this pass has asserted for a type so far.
func (c *conditionSet) get(condType assaydv1alpha1.ConditionType) (metav1.Condition, bool) {
	cond, ok := c.asserted[string(condType)]
	return cond, ok
}

// unset withdraws what this pass asserted for a type, so that a later step
// that re-derives it is not overruled by an earlier default.
func (c *conditionSet) unset(condType assaydv1alpha1.ConditionType) {
	delete(c.asserted, string(condType))
}

// ownedTypes are the conditions this reconciler is the sole author of. A type
// here that the pass did not assert is cleared rather than carried forward,
// because a stale SandboxDowngraded on an agent that no longer requests a
// sandbox is a lie. Types NOT listed belong to other controllers (the gate
// controller, the budget backstop, design 20's drift controllers) and are left
// untouched — clearing another controller's condition would be a write race.
var ownedTypes = map[assaydv1alpha1.ConditionType]bool{
	assaydv1alpha1.CondReady:               true,
	assaydv1alpha1.CondProgressing:         true,
	assaydv1alpha1.CondGatesSkipped:        true,
	assaydv1alpha1.CondGatesPassed:         true,
	assaydv1alpha1.CondSandboxDowngraded:   true,
	assaydv1alpha1.CondTaskStateUnverified: true,
	assaydv1alpha1.CondDegraded:            true,
	// Owned and no longer asserted by anything: A42 closed the gap it announced,
	// so merge() CLEARS a stale True left by an operator from before A42.
	assaydv1alpha1.CondEnvSourceProtectionUnavailable: true,
	assaydv1alpha1.CondEnvSourceUnresolved:            true,
	// Owned so it can CLEAR. It was in neither map, which meant merge() treated
	// it as another controller's and carried it forward verbatim — forever, with
	// a stale message, on an agent that had gone back to Ready. No other
	// controller writes it.
	assaydv1alpha1.CondRevisionHashCollision: true,
	// Owned so it CLEARS: Terminating clears by itself when the namespace is
	// recreated, and LabelAuthorityAbsent when the policies appear. Left out of
	// this set, every Agent that ever waited carried it forever beside
	// Ready=True (found by the code review of A61).
	assaydv1alpha1.CondRunNamespaceUnavailable: true,
	// Owned for the same reason: the operator is its only writer, and an Agent
	// whose material was restored would otherwise report Ready=True beside a
	// stale RevisionMaterialUnavailable=True forever — merge()'s default arm
	// carries an unowned type forward as "another controller's".
	assaydv1alpha1.CondRevisionMaterialUnavailable: true,
	// Design 03 §3.1's tier condition. Owned AND sticky: see stickyTypes.
	assaydv1alpha1.CondGovernanceSkipped: true,
	// Design 03's compile failure. Owned, abnormal-true and NOT sticky (§1.1,
	// §8.1 case 9): it must clear when its cause goes, as when an owner reverts
	// the edit that raised it. Left out of this set, merge()'s default arm would
	// carry it forward as another controller's, forever, which is the
	// RevisionHashCollision bug above. The -auth step asserts it
	// (authtxn.go), and TestPolicyCompileFailedClearsWhenItsCauseGoes pins the
	// clearing.
	assaydv1alpha1.CondPolicyCompileFailed: true,
	// Abnormal-true and owned, NOT sticky: a route or policy this operator
	// could not write, or an -auth transaction past its deadline. Its absence
	// means neither.
	assaydv1alpha1.CondPolicyApplyIncomplete: true,
}

// stickyTypes are owned conditions that must stay in the list once set, flipped
// to False rather than removed.
//
// Dropping an abnormal-true condition (SandboxDowngraded, TaskStateUnverified)
// when it stops applying is idiomatic — its absence means "not degraded". But
// Ready, Progressing and GatesPassed are normal-true conditions whose absence is
// indistinguishable from "never evaluated". Dropping GatesPassed when an
// EvalSuite CRD is uninstalled would silently erase the record that a revision
// ever passed a gate.
var stickyTypes = map[assaydv1alpha1.ConditionType]bool{
	assaydv1alpha1.CondReady:       true,
	assaydv1alpha1.CondProgressing: true,
	assaydv1alpha1.CondGatesPassed: true,
	// GovernanceSkipped is NORMAL-true and design 03 §3.1 classifies it for the
	// whole type: `True` is the ordinary state of a declared-ungoverned tier, so
	// dropping it when it stops applying would erase the record of which tier an
	// install chose. It flips to False rather than disappearing — which is why
	// it must not share a type with GatewayIncompatible, an incident whose
	// absence correctly means "not degraded".
	assaydv1alpha1.CondGovernanceSkipped: true,
}

// merge folds this pass's assertions into the existing conditions.
func (c *conditionSet) merge(existing []metav1.Condition) []metav1.Condition {
	out := make([]metav1.Condition, 0, len(existing)+len(c.asserted))

	for _, prev := range existing {
		next, asserted := c.asserted[prev.Type]
		if !asserted {
			switch {
			case stickyTypes[assaydv1alpha1.ConditionType(prev.Type)]:
				// Keep the record; a later pass that has an opinion will overwrite it.
				out = append(out, prev)
			case ownedTypes[assaydv1alpha1.ConditionType(prev.Type)]:
				// Abnormal-true and no longer observed: its absence is the signal.
			default:
				out = append(out, prev) // another controller's; leave it alone
			}
			continue
		}
		// LastTransitionTime marks when the STATUS changed, not when it was last
		// observed. Refreshing it on every pass would make "how long has this been
		// broken?" unanswerable from the object.
		if prev.Status == next.Status {
			next.LastTransitionTime = prev.LastTransitionTime
		} else {
			next.LastTransitionTime = metav1.Now()
		}
		out = append(out, next)
		delete(c.asserted, prev.Type)
	}

	// Whatever is left is newly asserted.
	for _, cond := range sortedConditions(c.asserted) {
		cond.LastTransitionTime = metav1.Now()
		out = append(out, cond)
	}
	return out
}

// sortedConditions gives map iteration a stable order, so two reconciles of an
// unchanged object produce byte-identical status and writeStatus can no-op.
func sortedConditions(m map[string]metav1.Condition) []metav1.Condition {
	types := make([]string, 0, len(m))
	for t := range m {
		types = append(types, t)
	}
	sortStrings(types)
	out := make([]metav1.Condition, 0, len(types))
	for _, t := range types {
		out = append(out, m[t])
	}
	return out
}

func sortStrings(xs []string) {
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && xs[j] < xs[j-1]; j-- {
			xs[j], xs[j-1] = xs[j-1], xs[j]
		}
	}
}

// equalStatus reports whether a status write would be a no-op. Conditions are
// compared ignoring LastTransitionTime, which is derived rather than observed —
// comparing it would make every pass look like a change and churn the API
// server forever.
func equalStatus(a, b *assaydv1alpha1.AgentStatus) bool {
	x, y := a.DeepCopy(), b.DeepCopy()
	for i := range x.Conditions {
		x.Conditions[i].LastTransitionTime = metav1.Time{}
	}
	for i := range y.Conditions {
		y.Conditions[i].LastTransitionTime = metav1.Time{}
	}
	return reflect.DeepEqual(x, y)
}

// conditionTypeSource is the file scanned by TestNoConditionTypeIsALiteral.
const conditionTypeSource = "agent_controller.go"
