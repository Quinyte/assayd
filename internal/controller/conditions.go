package controller

import (
	"reflect"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	plumev1alpha1 "github.com/Quinyte/plume/api/v1alpha1"
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

func (c *conditionSet) set(condType string, status metav1.ConditionStatus, reason, message string) {
	c.asserted[condType] = metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: c.generation,
	}
}

// ownedTypes are the conditions this reconciler is the sole author of. A type
// here that the pass did not assert is cleared rather than carried forward,
// because a stale SandboxDowngraded on an agent that no longer requests a
// sandbox is a lie. Types NOT listed belong to other controllers (the gate
// controller, the budget backstop, design 20's drift controllers) and are left
// untouched — clearing another controller's condition would be a write race.
var ownedTypes = map[string]bool{
	plumev1alpha1.CondReady:               true,
	plumev1alpha1.CondProgressing:         true,
	plumev1alpha1.CondGatesSkipped:        true,
	plumev1alpha1.CondGatesPassed:         true,
	plumev1alpha1.CondSandboxDowngraded:   true,
	plumev1alpha1.CondTaskStateUnverified: true,
	plumev1alpha1.CondDegraded:            true,
	// Owned because assessEnvSourceProtection is the only writer, and it must be
	// able to CLEAR the condition when the last env source is removed from a spec.
	plumev1alpha1.CondEnvSourceProtectionUnavailable: true,
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
var stickyTypes = map[string]bool{
	plumev1alpha1.CondReady:       true,
	plumev1alpha1.CondProgressing: true,
	plumev1alpha1.CondGatesPassed: true,
}

// merge folds this pass's assertions into the existing conditions.
func (c *conditionSet) merge(existing []metav1.Condition) []metav1.Condition {
	out := make([]metav1.Condition, 0, len(existing)+len(c.asserted))

	for _, prev := range existing {
		next, asserted := c.asserted[prev.Type]
		if !asserted {
			switch {
			case stickyTypes[prev.Type]:
				// Keep the record; a later pass that has an opinion will overwrite it.
				out = append(out, prev)
			case ownedTypes[prev.Type]:
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
func equalStatus(a, b *plumev1alpha1.AgentStatus) bool {
	x, y := a.DeepCopy(), b.DeepCopy()
	for i := range x.Conditions {
		x.Conditions[i].LastTransitionTime = metav1.Time{}
	}
	for i := range y.Conditions {
		y.Conditions[i].LastTransitionTime = metav1.Time{}
	}
	return reflect.DeepEqual(x, y)
}
