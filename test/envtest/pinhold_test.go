// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package envtest

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/controller"
	"github.com/Quinyte/assayd/internal/revision"
)

// Design 16 A26: while a pin is set, the eval controller holds a recorded
// candidate the current spec does not mint (A22.33 (b), widened by A26.5 (a));
// where the spec's digest cannot be computed, it holds only beside a pin that
// names the candidate (A26.2 (b)). A pin refusal's Degraded message carries
// one sentence saying the candidate is held, exactly when the gate is
// enforced, a candidate is pending and the hold holds against the pass's own
// spec digest (A26.3). Each row asserts the cases the candidate is sent over
// three eval passes and how often the sentence appears; the rows are A26.1's
// table, named by its row labels.

// heldSentence is a fragment of A26.3's sentence and of nothing else.
const heldSentence = "is held: the eval controller sends it no case until"

// holdSentences is how often the Agent's Degraded message carries the held
// sentence.
func holdSentences(t *testing.T, a *assaydv1alpha1.Agent) int {
	t.Helper()
	c := condition(liveAgent(t, a), assaydv1alpha1.CondDegraded)
	if c == nil {
		return 0
	}
	return strings.Count(c.Message, heldSentence)
}

// wantHold asserts the sentence count, and, when it is one, that it names
// the recorded candidate.
func wantHold(t *testing.T, a *assaydv1alpha1.Agent, n int) {
	t.Helper()
	if got := holdSentences(t, a); got != n {
		t.Fatalf("the held sentence appears %d times in Degraded, want %d: %+v", got, n,
			condition(liveAgent(t, a), assaydv1alpha1.CondDegraded))
	}
	if n == 0 {
		return
	}
	live := liveAgent(t, a)
	c := condition(live, assaydv1alpha1.CondDegraded)
	for _, w := range []string{live.Status.CandidateRevision, live.Status.CandidateRevisionDigest} {
		if w == "" || !strings.Contains(c.Message, w) {
			t.Errorf("the held sentence does not name the recorded candidate's %q: %s", w, c.Message)
		}
	}
}

// specDigestWith is the digest the Agent's spec mints after edit, for a spec
// with no env source.
func specDigestWith(t *testing.T, a *assaydv1alpha1.Agent, edit func(*assaydv1alpha1.AgentSpec)) string {
	t.Helper()
	spec := liveAgent(t, a).Spec.DeepCopy()
	edit(spec)
	return revision.MustDigest(*spec)
}

// threeEvalPasses runs three eval passes and returns the cases sent to the
// revision over them.
func (g *evalRig) threeEvalPasses(t *testing.T, rev string) int {
	t.Helper()
	before := g.stub.sentTo(g.name, rev)
	for i := 0; i < 3; i++ {
		g.evalPass(t)
	}
	return g.stub.sentTo(g.name, rev) - before
}

// R1: A22.10's revert-and-pin. A, then B, served; C the gated candidate; one
// write reverts the spec to A and pins A, which is refused as the current
// candidate. C is held.
func TestR1ARevertAndPinHoldsTheRecordedCandidate(t *testing.T) {
	g := newEvalRig(t, "holdr1", echo("a", "one"), echo("b", "two"))
	// A's image is mustCreateAgent's; the rig's spec already carries B's.
	const imgA = "ghcr.io/acme/agent@sha256:6d5d9666a268df6f000000000000000000000000000000000000000000000000"
	dA := g.active
	g.both(t, 3)
	if got := liveAgent(t, g.a).Status.ActiveRevisionDigest; got != g.dgst {
		t.Fatalf("setup: B did not promote on its run (active %s)", got)
	}
	revC, dC := gatedCandidate(t, g.r, g.a, imageOf('c'), "smoke")
	if !cardRecorded(&liveAgent(t, g.a).Status, dC) {
		t.Fatal("setup: C's card was not recorded")
	}
	mustEdit(t, g.a, func(a *assaydv1alpha1.Agent) {
		a.Spec.Runtime.Image = imgA
		a.Spec.Release = &assaydv1alpha1.ReleaseSpec{TargetRevisionDigest: dA}
	})
	g.agentPass(t)
	refusedPin(t, g.a, dA, "current candidate")
	if got := liveAgent(t, g.a).Status.CandidateRevisionDigest; got != dC {
		t.Fatalf("setup: C is not the recorded candidate after the refusal (%s)", got)
	}
	wantHold(t, g.a, 1)
	if n := g.threeEvalPasses(t, revC); n != 0 {
		t.Errorf("C, which the reverted spec does not mint, was sent %d cases beside a refused pin", n)
	}
}

// R2: one write moves the spec to D and pins D's digest before D is minted;
// resolveReleasePin refuses it as unresolvable. The pin is the spec's
// revision and not the recorded candidate's: A22.33 (b)'s clause.
func TestR2APinToTheSpecsUnmintedRevisionHoldsTheCandidate(t *testing.T) {
	g := newEvalRig(t, "holdr2", echo("a", "one"), echo("b", "two"))
	dD := specDigestWith(t, g.a, func(s *assaydv1alpha1.AgentSpec) { s.Runtime.Image = imageOf('d') })
	mustEdit(t, g.a, func(a *assaydv1alpha1.Agent) {
		a.Spec.Runtime.Image = imageOf('d')
		a.Spec.Release = &assaydv1alpha1.ReleaseSpec{TargetRevisionDigest: dD}
	})
	g.agentPass(t)
	condIs(t, g.a, assaydv1alpha1.CondDegraded, metav1.ConditionTrue, "ReleasePinUnresolvable")
	wantHold(t, g.a, 1)
	if n := g.threeEvalPasses(t, g.rev); n != 0 {
		t.Errorf("the candidate was sent %d cases beside a pin to the spec's own revision", n)
	}
}

// R3: one write moves the spec off C and pins a digest no revision carries.
// The pin names neither C nor the spec's revision; A26.5 (a) holds C.
func TestR3ATypoPinBesideASpecThatLeftTheCandidateHoldsIt(t *testing.T) {
	g := newEvalRig(t, "holdr3", echo("a", "one"), echo("b", "two"))
	mustEdit(t, g.a, func(a *assaydv1alpha1.Agent) {
		a.Spec.Runtime.Image = imageOf('d')
		a.Spec.Release = &assaydv1alpha1.ReleaseSpec{TargetRevisionDigest: strings.Repeat("e", 64)}
	})
	g.agentPass(t)
	condIs(t, g.a, assaydv1alpha1.CondDegraded, metav1.ConditionTrue, "ReleasePinUnresolvable")
	wantHold(t, g.a, 1)
	if n := g.threeEvalPasses(t, g.rev); n != 0 {
		t.Errorf("the candidate was sent %d cases beside a pin, with a spec that no longer mints it", n)
	}
}

// R4: one write adds an env source and pins C, whose retained material has
// no copy of it: verifyRetainedMaterial refuses the pin before the rule.
func TestR4APinRefusedForItsMaterialHoldsTheCandidate(t *testing.T) {
	g := newEvalRig(t, "holdr4", echo("a", "one"), echo("b", "two"))
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: g.ns, Name: "cfg"},
		Data: map[string]string{"MODE": "safe"}}
	if err := k8s.Create(context.Background(), cm); err != nil {
		t.Fatal(err)
	}
	mustEdit(t, g.a, func(a *assaydv1alpha1.Agent) {
		a.Spec.Runtime.Env = []corev1.EnvVar{{Name: "MODE", ValueFrom: &corev1.EnvVarSource{
			ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: "cfg"}, Key: "MODE"}}}}
		a.Spec.Release = &assaydv1alpha1.ReleaseSpec{TargetRevisionDigest: g.dgst}
	})
	g.agentPass(t)
	c := condIs(t, g.a, assaydv1alpha1.CondDegraded, metav1.ConditionTrue, "ReleasePinUnresolvable")
	if !strings.Contains(c.Message, "immutable copy") {
		t.Fatalf("setup: the pin was not refused for its material: %s", c.Message)
	}
	wantHold(t, g.a, 1)
	if n := g.threeEvalPasses(t, g.rev); n != 0 {
		t.Errorf("the candidate was sent %d cases beside a pin to it the spec no longer mints", n)
	}
}

// R5: one write moves the spec off C and pins C, whose pair is in failed[]
// against the suite in force: the rule refuses it on failed[].
func TestR5APinRefusedOnFailedHoldsTheCandidate(t *testing.T) {
	g := newEvalRig(t, "holdr5", echo("a", "one"), echo("b", "two"))
	writeAgentStatus(t, g.a, func(st *assaydv1alpha1.AgentStatus) {
		st.Eval = failedRecord(g.rev, g.dgst, g.suiteDgst)
	})
	mustEdit(t, g.a, func(a *assaydv1alpha1.Agent) {
		a.Spec.Runtime.Image = imageOf('d')
		a.Spec.Release = &assaydv1alpha1.ReleaseSpec{TargetRevisionDigest: g.dgst}
	})
	g.agentPass(t)
	refusedPin(t, g.a, g.dgst, "status.eval.failed[]")
	wantHold(t, g.a, 1)
	if n := g.threeEvalPasses(t, g.rev); n != 0 {
		t.Errorf("a failed pair was sent %d cases", n)
	}
}

// R7: gated, with no candidate pending, one write pins the spec's own
// unminted revision. There is no recorded candidate to hold, so no sentence.
func TestR7APinRefusalWithNoCandidatePendingNamesNoHold(t *testing.T) {
	ns := newNamespace(t)
	putSuite(t, ns, "smoke", echo("a", "one"))
	a, r := servedUngated(t, ns, "holdr7")
	mustEdit(t, a, func(a *assaydv1alpha1.Agent) {
		a.Spec.Gates = []assaydv1alpha1.GateRef{{EvalSuiteRef: "smoke"}}
	})
	reconcileOnce(t, r, a)
	reconcileOnce(t, r, a)
	if st := liveAgent(t, a).Status; st.CandidateRevisionDigest != "" &&
		st.CandidateRevisionDigest != st.ActiveRevisionDigest {
		t.Fatalf("setup: a candidate is pending: %s", st.CandidateRevisionDigest)
	}
	dD := specDigestWith(t, a, func(s *assaydv1alpha1.AgentSpec) { s.Runtime.Image = imageOf('d') })
	mustEdit(t, a, func(a *assaydv1alpha1.Agent) {
		a.Spec.Runtime.Image = imageOf('d')
		a.Spec.Release = &assaydv1alpha1.ReleaseSpec{TargetRevisionDigest: dD}
	})
	reconcileOnce(t, r, a)
	condIs(t, a, assaydv1alpha1.CondDegraded, metav1.ConditionTrue, "ReleasePinUnresolvable")
	wantHold(t, a, 0)
}

// pendingCandidate serves an Agent and records a candidate that is
// not available, with the gates given; r's EvalSuite detection is as the
// caller set it.
func pendingCandidate(t *testing.T, name string, gates []assaydv1alpha1.GateRef,
	crd bool) (*assaydv1alpha1.Agent, *controller.AgentReconciler) {
	t.Helper()
	ns := newNamespace(t)
	putSuite(t, ns, "smoke", echo("a", "one"))
	a, r := servedUngated(t, ns, name)
	r.EvalSuiteInstalled = func() bool { return crd }
	mustEdit(t, a, func(a *assaydv1alpha1.Agent) {
		a.Spec.Runtime.Image = imageB
		a.Spec.Gates = gates
	})
	reconcileOnce(t, r, a)
	reconcileOnce(t, r, a)
	if st := liveAgent(t, a).Status; st.CandidateRevisionDigest == "" ||
		st.CandidateRevisionDigest == st.ActiveRevisionDigest {
		t.Fatalf("setup: no candidate is pending: %+v", st)
	}
	return a, r
}

// pinTheUnmintedSpec is one write that moves the spec to D and pins D's
// digest, then one Agent pass, which refuses the pin as unresolvable.
func pinTheUnmintedSpec(t *testing.T, a *assaydv1alpha1.Agent, r *controller.AgentReconciler) {
	t.Helper()
	dD := specDigestWith(t, a, func(s *assaydv1alpha1.AgentSpec) { s.Runtime.Image = imageOf('d') })
	mustEdit(t, a, func(a *assaydv1alpha1.Agent) {
		a.Spec.Runtime.Image = imageOf('d')
		a.Spec.Release = &assaydv1alpha1.ReleaseSpec{TargetRevisionDigest: dD}
	})
	reconcileOnce(t, r, a)
	condIs(t, a, assaydv1alpha1.CondDegraded, metav1.ConditionTrue, "ReleasePinUnresolvable")
}

// R8: ungated, a candidate pending, one write pins the spec's own unminted
// revision. No eval controller runs, so no sentence.
func TestR8AnUngatedPinRefusalNamesNoHold(t *testing.T) {
	a, r := pendingCandidate(t, "holdr8", nil, true)
	pinTheUnmintedSpec(t, a, r)
	wantHold(t, a, 0)
}

// R8b: R8's shape with gates declared and the EvalSuite CRD absent, where the
// eval controller returns before any predicate. The writer is scoped by the
// gate being enforced, not by gates being declared.
func TestR8bAPinRefusalWithTheEvalSuiteCRDAbsentNamesNoHold(t *testing.T) {
	a, r := pendingCandidate(t, "holdr8b", []assaydv1alpha1.GateRef{{EvalSuiteRef: "smoke"}}, false)
	pinTheUnmintedSpec(t, a, r)
	wantHold(t, a, 0)
}

// R9: A26.2's row. A typo pin beside a spec that left C holds C, and the
// sentence says so; then a referenced source is deleted with no Agent pass.
// The spec's digest can no longer be computed, and the pin does not name C,
// so under A26.2 (b) C is not held: it is sent every case while the sentence
// still stands.
func TestR9ADeletedSourceBesideATypoPinLiftsTheHold(t *testing.T) {
	ns := newNamespace(t)
	sd := putSuite(t, ns, "smoke", echo("a", "one"), echo("b", "two"))
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "cfg"},
		Data: map[string]string{"MODE": "safe"}}
	if err := k8s.Create(context.Background(), cm); err != nil {
		t.Fatal(err)
	}
	const name = "holdr9"
	a := mustCreateAgent(t, ns, name, func(a *assaydv1alpha1.Agent) {
		a.Spec.Runtime.Env = []corev1.EnvVar{{Name: "MODE", ValueFrom: &corev1.EnvVarSource{
			ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: "cfg"}, Key: "MODE"}}}}
	})
	r := newReconciler(true)
	stub := newA2AStub()
	r.CardClient = stub
	bringUp := func() (string, string) {
		t.Helper()
		reconcileOnce(t, r, a)
		reconcileOnce(t, r, a)
		got := liveAgent(t, a)
		rev, d := got.Status.CandidateRevision, got.Status.CandidateRevisionDigest
		if rev == "" {
			t.Fatalf("setup: no candidate recorded: %+v", got.Status)
		}
		markAvailable(t, ns, controller.WorkloadName(name, rev), 1)
		reconcileOnce(t, r, a)
		return rev, d
	}
	bringUp()
	if liveAgent(t, a).Status.ActiveRevision == "" {
		t.Fatal("setup: the first revision did not promote")
	}
	mustEdit(t, a, func(a *assaydv1alpha1.Agent) {
		a.Spec.Runtime.Image = imageB
		a.Spec.Gates = []assaydv1alpha1.GateRef{{EvalSuiteRef: "smoke"}}
	})
	revC, dC := bringUp()
	if !cardRecorded(&liveAgent(t, a).Status, dC) {
		t.Fatal("setup: C's card was not recorded")
	}
	g := &evalRig{ns: ns, name: name, a: a, r: r, e: newEvalController(t, stub), stub: stub,
		rev: revC, dgst: dC, suiteDgst: sd}
	mustEdit(t, a, func(a *assaydv1alpha1.Agent) {
		a.Spec.Runtime.Image = imageOf('d')
		a.Spec.Release = &assaydv1alpha1.ReleaseSpec{TargetRevisionDigest: strings.Repeat("e", 64)}
	})
	g.agentPass(t)
	condIs(t, a, assaydv1alpha1.CondDegraded, metav1.ConditionTrue, "ReleasePinUnresolvable")
	wantHold(t, a, 1)
	if err := k8s.Delete(context.Background(), cm); err != nil {
		t.Fatal(err)
	}
	if n := g.threeEvalPasses(t, revC); n != 2 {
		t.Errorf("C was sent %d cases after the source was deleted beside a pin not naming it, want "+
			"every case, 2 (A26.2 (b))", n)
	}
	wantHold(t, a, 1)
}

// RY and RZ: R2's and R3's shapes, with the run-namespace exit failing
// terminally on the Agent pass. The pass returns before the pin step, so no
// sentence is written, and the eval controller holds C all the same: a hold
// named by nothing (A26's stated property, #145).
func TestRYAndRZAHoldAtTheRunNamespaceExitIsNamedByNothing(t *testing.T) {
	noAuthority := func(context.Context) (bool, error) { return false, nil }
	for _, tc := range []struct {
		row, name string
		pin       func(*evalRig) string
	}{
		{"RY", "holdry", func(g *evalRig) string {
			return specDigestWith(t, g.a, func(s *assaydv1alpha1.AgentSpec) { s.Runtime.Image = imageOf('d') })
		}},
		{"RZ", "holdrz", func(*evalRig) string { return strings.Repeat("e", 64) }},
	} {
		t.Run(tc.row, func(t *testing.T) {
			g := newEvalRig(t, tc.name, echo("a", "one"), echo("b", "two"))
			pin := tc.pin(g)
			mustEdit(t, g.a, func(a *assaydv1alpha1.Agent) {
				a.Spec.Runtime.Image = imageOf('d')
				a.Spec.Release = &assaydv1alpha1.ReleaseSpec{TargetRevisionDigest: pin}
			})
			g.r.LabelAuthorityPresent = noAuthority
			g.agentPass(t)
			condIs(t, g.a, assaydv1alpha1.CondRunNamespaceUnavailable, metav1.ConditionTrue, "LabelAuthorityAbsent")
			wantHold(t, g.a, 0)
			if n := g.threeEvalPasses(t, g.rev); n != 0 {
				t.Errorf("the candidate was sent %d cases beside a pin, with a spec that no longer mints it", n)
			}
		})
	}
}
