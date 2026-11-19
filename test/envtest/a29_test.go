// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package envtest

import (
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/controller"
)

func a29UnevaluatedPin(t *testing.T, a *assaydv1alpha1.Agent, rev, digest string) *metav1.Condition {
	t.Helper()
	if got := liveAgent(t, a).Status.ActiveRevisionDigest; got != digest {
		t.Fatalf("expected pinned revision %s serving, got %s", digest, got)
	}
	c := condIs(t, a, assaydv1alpha1.CondGatesPassed, metav1.ConditionFalse, controller.ReasonActiveRevisionNotEvaluated)
	const tail = ". It serves by pin, and while the pin stands nothing is evaluated in its place; " +
		"GatesBypassed or Degraded says what the pin does, except on a pass that stops earlier, " +
		"such as one on an unresolved env source"
	if !strings.HasSuffix(c.Message, tail) {
		t.Errorf("pinned GatesPassed does not end with the approved tail: %s", c.Message)
	}
	if want := "revision " + rev + " (digest " + digest + ") is serving"; !strings.Contains(c.Message, want) {
		t.Errorf("pinned GatesPassed is missing %q: %s", want, c.Message)
	}
	for _, stale := range []string{"spec.gates was removed", "because gates are policy surface", "the next revision this spec mints is held", "Q5 (a)"} {
		if strings.Contains(c.Message, stale) {
			t.Errorf("pinned GatesPassed contains %q: %s", stale, c.Message)
		}
	}
	return c
}

func a29PassedEntry(t *testing.T, a *assaydv1alpha1.Agent, rev, digest, suiteDigest string) {
	t.Helper()
	c := condIs(t, a, assaydv1alpha1.CondGatesPassed, metav1.ConditionTrue, controller.ReasonEvalPassed)
	for _, want := range []string{"revision " + rev + " (digest " + digest + ") passed", suiteDigest, "passed[] keeps no score"} {
		if !strings.Contains(c.Message, want) {
			t.Errorf("pinned GatesPassed from passed[] is missing %q: %s", want, c.Message)
		}
	}
}

// The first repeated pass can remove the transient pin-admission explanation.
// Once settled, further passes must not change the persisted Agent.
func a29NoPersistedStatusChanges(t *testing.T, r *controller.AgentReconciler, a *assaydv1alpha1.Agent) {
	t.Helper()
	settle(t, r, a)
	rv := liveAgent(t, a).ResourceVersion
	for i := 0; i < 3; i++ {
		reconcileOnce(t, r, a)
		if got := liveAgent(t, a).ResourceVersion; got != rv {
			t.Fatalf("steady pinned pass %d changed Agent resourceVersion from %s to %s", i+1, rv, got)
		}
	}
}

// R1 (#139): a gate removed and restored beside a pin must not leave the
// stale NoGatesDeclared claim. Moving the pin must then describe its new
// serving target, and clearing it resumes the ordinary candidate decision.
func TestA29RestoredGateRederivesPinnedStatus(t *testing.T) {
	ns := newNamespace(t)
	sd := mustCreateSuite(t, ns, "smoke")
	a, r := servedUngated(t, ns, "a29restore")
	serving := liveAgent(t, a).Status
	revB, dB := gatedCandidate(t, r, a, imageB, "smoke")
	writeAgentStatus(t, a, func(st *assaydv1alpha1.AgentStatus) {
		st.Eval = failedRecord(revB, dB, sd)
	})
	reconcileOnce(t, r, a)
	condIs(t, a, assaydv1alpha1.CondGatesPassed, metav1.ConditionFalse, "EvalFailed")
	mustEdit(t, a, func(a *assaydv1alpha1.Agent) { a.Spec.Gates = nil })
	reconcileOnce(t, r, a)
	condIs(t, a, assaydv1alpha1.CondGatesPassed, metav1.ConditionFalse, "NoGatesDeclared")

	mustEdit(t, a, func(a *assaydv1alpha1.Agent) {
		a.Spec.Gates = []assaydv1alpha1.GateRef{{EvalSuiteRef: "smoke"}}
		a.Spec.Release = &assaydv1alpha1.ReleaseSpec{TargetRevisionDigest: dB}
	})
	reconcileOnce(t, r, a)
	a29UnevaluatedPin(t, a, revB, dB)
	pinTo(t, a, serving.ActiveRevisionDigest)
	reconcileOnce(t, r, a)
	a29UnevaluatedPin(t, a, serving.ActiveRevision, serving.ActiveRevisionDigest)
	c := bypassed(t, a, serving.ActiveRevision, serving.ActiveRevisionDigest, pinServing)
	if !strings.Contains(c.Message, "GatesPassed is asserted for the revision serving on each pass that reaches the promotion branch") {
		t.Errorf("serving form still describes the old sticky rule: %s", c.Message)
	}
	a29NoPersistedStatusChanges(t, r, a)
	mustEdit(t, a, func(a *assaydv1alpha1.Agent) { a.Spec.Release = nil })
	reconcileOnce(t, r, a)
	condIs(t, a, assaydv1alpha1.CondGatesPassed, metav1.ConditionFalse, "EvalFailed")
}

// R2: a retained revision that never ran comes to serve; no candidate's
// superseded waiting status should remain.
func TestA29PinToRetainedRevisionReplacesCandidateStatus(t *testing.T) {
	_, a, r, revB, dB, _ := pinFixture(t, "a29retained")
	before := liveAgent(t, a).Status
	condIs(t, a, assaydv1alpha1.CondGatesPassed, metav1.ConditionFalse, "EvalAwaitingCard")
	if before.CandidateRevisionDigest == "" || before.CandidateRevisionDigest == dB {
		t.Fatal("setup: expected a different candidate")
	}
	pinTo(t, a, dB)
	reconcileOnce(t, r, a)
	c := a29UnevaluatedPin(t, a, revB, dB)
	if strings.Contains(c.Message, before.CandidateRevisionDigest) {
		t.Errorf("condition still names superseded candidate: %s", c.Message)
	}
	a29NoPersistedStatusChanges(t, r, a)
}

// R3 and R6 differ in order: the gate arrives beside an existing pin, or a
// pin arrives beside an existing gate. Neither pass promotes a revision.
func TestA29SameServingRevisionRederivesPinnedStatus(t *testing.T) {
	for _, pinFirst := range []bool{true, false} {
		name := "a29gatefirst"
		if pinFirst {
			name = "a29pinfirst"
		}
		t.Run(name, func(t *testing.T) {
			ns := newNamespace(t)
			mustCreateSuite(t, ns, "smoke")
			a, r := servedUngated(t, ns, name)
			active := liveAgent(t, a).Status
			addGate := func() {
				mustEdit(t, a, func(a *assaydv1alpha1.Agent) {
					a.Spec.Gates = []assaydv1alpha1.GateRef{{EvalSuiteRef: "smoke"}}
				})
				reconcileOnce(t, r, a)
			}
			pinActive := func() {
				pinTo(t, a, active.ActiveRevisionDigest)
				reconcileOnce(t, r, a)
			}
			if pinFirst {
				pinActive()
				if c := condition(liveAgent(t, a), assaydv1alpha1.CondGatesPassed); c != nil {
					t.Fatalf("ungated setup unexpectedly has GatesPassed: %+v", c)
				}
				addGate()
			} else {
				addGate()
				c := condIs(t, a, assaydv1alpha1.CondGatesPassed, metav1.ConditionFalse, controller.ReasonActiveRevisionNotEvaluated)
				if !strings.Contains(c.Message, "the next revision this spec mints is held") {
					t.Fatalf("expected original unpinned tail: %s", c.Message)
				}
				pinActive()
			}
			a29UnevaluatedPin(t, a, active.ActiveRevision, active.ActiveRevisionDigest)
			a29NoPersistedStatusChanges(t, r, a)
		})
	}
}
