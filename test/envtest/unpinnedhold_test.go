// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package envtest

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/controller"
	"github.com/Quinyte/assayd/internal/revision"
)

// Design 16 A28.1: with no pin set, the eval controller holds the recorded
// candidate C unless it computes the digest the current spec mints and that
// digest is C's. Each row counts the cases sent to C over three eval passes,
// driven by hand. Design 02 A80: the Agent reconciler's EnvSourceUnresolved
// exit requeues at CardDriftInterval while a referenced source is
// unresolved, and its terminal run-namespace exit too (A80 (R)).

// reconcileResult runs one Agent pass and returns its result.
func reconcileResult(t *testing.T, r *controller.AgentReconciler, a *assaydv1alpha1.Agent) ctrl.Result {
	t.Helper()
	res, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(a)})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	return res
}

// modeFrom is an env var MODE read from the ConfigMap name.
func modeFrom(name string) []corev1.EnvVar {
	return []corev1.EnvVar{{Name: "MODE", ValueFrom: &corev1.EnvVarSource{
		ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: name}, Key: "MODE"}}}}
}

// putConfigMap creates the ConfigMap cfg with MODE set to mode.
func putConfigMap(t *testing.T, ns, mode string) {
	t.Helper()
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "cfg"},
		Data: map[string]string{"MODE": mode}}
	if err := k8s.Create(context.Background(), cm); err != nil {
		t.Fatal(err)
	}
}

func deleteConfigMap(t *testing.T, ns string) {
	t.Helper()
	if err := k8s.Delete(context.Background(),
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "cfg"}}); err != nil {
		t.Fatal(err)
	}
}

// envRig is an evalRig whose spec reads MODE from the ConfigMap cfg: a first
// revision served ungated, then a gated candidate C on imageB, available and
// carded, with no pin set.
func envRig(t *testing.T, name string, cases ...assaydv1alpha1.EvalCase) *evalRig {
	t.Helper()
	ns := newNamespace(t)
	sd := putSuite(t, ns, "smoke", cases...)
	putConfigMap(t, ns, "safe")
	a := mustCreateAgent(t, ns, name, func(a *assaydv1alpha1.Agent) { a.Spec.Runtime.Env = modeFrom("cfg") })
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
	rev, d := bringUp()
	if !cardRecorded(&liveAgent(t, a).Status, d) {
		t.Fatal("setup: C's card was not recorded")
	}
	return &evalRig{ns: ns, name: name, a: a, r: r, e: newEvalController(t, stub), stub: stub,
		rev: rev, dgst: d, suiteDgst: sd}
}

// superseded reports whether status.supersededCandidates names the revision.
func superseded(t *testing.T, a *assaydv1alpha1.Agent, rev, digest string) bool {
	t.Helper()
	for _, s := range liveAgent(t, a).Status.SupersededCandidates {
		if s == rev+"@"+digest {
			return true
		}
	}
	return false
}

// N1, #144's own shape: one write moves the spec off C and references a
// missing ConfigMap. The Agent pass at EnvSourceUnresolved requeues at
// CardDriftInterval (design 02 A80); C is sent nothing, before or after the
// ConfigMap is created, and the eval passes write no Agent status; the pass
// the requeue brings supersedes C.
func TestN1AMissingSourceBesideNoPinHoldsTheCandidateUntilTheRequeue(t *testing.T) {
	g := newEvalRig(t, "unpinnedn1", echo("a", "one"), echo("b", "two"))
	mustEdit(t, g.a, func(a *assaydv1alpha1.Agent) {
		a.Spec.Runtime.Image = imageOf('d')
		a.Spec.Runtime.Env = modeFrom("cfg")
	})
	res := reconcileResult(t, g.r, g.a)
	condIs(t, g.a, assaydv1alpha1.CondEnvSourceUnresolved, metav1.ConditionTrue, "Unresolved")
	if res.RequeueAfter != controller.CardDriftInterval {
		t.Errorf("the pass at EnvSourceUnresolved returned RequeueAfter %v, want %v (design 02 A80)",
			res.RequeueAfter, controller.CardDriftInterval)
	}
	rv := liveAgent(t, g.a).ResourceVersion
	if n := g.threeEvalPasses(t, g.rev); n != 0 {
		t.Errorf("C, which the spec no longer mints, was sent %d cases while a source is missing", n)
	}
	if got := liveAgent(t, g.a).ResourceVersion; got != rv {
		t.Errorf("the eval passes wrote the Agent (resourceVersion %s, was %s)", got, rv)
	}
	putConfigMap(t, g.ns, "safe")
	if n := g.threeEvalPasses(t, g.rev); n != 0 {
		t.Errorf("C was sent %d cases after the missing source was created", n)
	}
	g.agentPass(t)
	if !superseded(t, g.a, g.rev, g.dgst) {
		t.Errorf("the pass the requeue brings did not supersede C: %v",
			liveAgent(t, g.a).Status.SupersededCandidates)
	}
	if n := g.threeEvalPasses(t, g.rev); n != 0 {
		t.Errorf("C was sent %d cases after it was superseded", n)
	}
}

// N2: an env source's content edited with no Agent pass. The spec now mints
// another revision, so C is held; the next Agent pass supersedes it.
func TestN2AContentEditBesideNoPinHoldsTheCandidate(t *testing.T) {
	g := envRig(t, "unpinnedn2", echo("a", "one"), echo("b", "two"))
	var cm corev1.ConfigMap
	if err := k8s.Get(context.Background(), client.ObjectKey{Namespace: g.ns, Name: "cfg"}, &cm); err != nil {
		t.Fatal(err)
	}
	cm.Data["MODE"] = "other"
	if err := k8s.Update(context.Background(), &cm); err != nil {
		t.Fatal(err)
	}
	if n := g.threeEvalPasses(t, g.rev); n != 0 {
		t.Errorf("C was sent %d cases after its source's content was edited", n)
	}
	g.agentPass(t)
	if !superseded(t, g.a, g.rev, g.dgst) {
		t.Errorf("the next Agent pass did not supersede C: %v", liveAgent(t, g.a).Status.SupersededCandidates)
	}
}

// N3: a source C reads deleted with no spec write holds C; re-created with
// the same content, C is the revision the spec mints again and is sent its
// cases before any Agent pass.
func TestN3ADeletedSourceBesideNoPinHoldsTheCandidateUntilItReturns(t *testing.T) {
	g := envRig(t, "unpinnedn3", echo("a", "one"), echo("b", "two"))
	deleteConfigMap(t, g.ns)
	if n := g.threeEvalPasses(t, g.rev); n != 0 {
		t.Errorf("C was sent %d cases while the source it reads is missing", n)
	}
	putConfigMap(t, g.ns, "safe")
	if n := g.threeEvalPasses(t, g.rev); n != 2 {
		t.Errorf("C was sent %d cases after its source was re-created with the same content, want 2", n)
	}
}

// N4: one write moves the spec off C, with the run-namespace exit terminal,
// and, separately, with a revision-name collision written to status. Each
// pass stamps observedGeneration without reaching the supersession, and C is
// held at each.
func TestN4AnExitBeforeTheSupersessionHoldsTheCandidateWithNoPin(t *testing.T) {
	t.Run("run namespace", func(t *testing.T) {
		g := newEvalRig(t, "unpinnedn4ns", echo("a", "one"), echo("b", "two"))
		mustEdit(t, g.a, func(a *assaydv1alpha1.Agent) { a.Spec.Runtime.Image = imageOf('d') })
		g.r.LabelAuthorityPresent = func(context.Context) (bool, error) { return false, nil }
		g.agentPass(t)
		condIs(t, g.a, assaydv1alpha1.CondRunNamespaceUnavailable, metav1.ConditionTrue, "LabelAuthorityAbsent")
		if n := g.threeEvalPasses(t, g.rev); n != 0 {
			t.Errorf("C was sent %d cases at the run-namespace exit, with a spec that no longer mints it", n)
		}
	})
	t.Run("collision", func(t *testing.T) {
		g := newEvalRig(t, "unpinnedn4col", echo("a", "one"), echo("b", "two"))
		mustEdit(t, g.a, func(a *assaydv1alpha1.Agent) { a.Spec.Runtime.Image = imageOf('d') })
		// The active revision recorded under the name the new spec mints,
		// with its own digest: the status guard refuses before the
		// supersession.
		name := revision.MustHash(liveAgent(t, g.a).Spec)
		writeAgentStatus(t, g.a, func(st *assaydv1alpha1.AgentStatus) { st.ActiveRevision = name })
		g.agentPass(t)
		condIs(t, g.a, assaydv1alpha1.CondRevisionHashCollision, metav1.ConditionTrue, "DigestMismatch")
		if got := liveAgent(t, g.a); got.Status.ObservedGeneration != got.Generation ||
			got.Status.CandidateRevisionDigest != g.dgst {
			t.Fatalf("setup: the collision pass did not stamp the generation with C recorded: %+v", got.Status)
		}
		if n := g.threeEvalPasses(t, g.rev); n != 0 {
			t.Errorf("C was sent %d cases at the collision exit, with a spec that no longer mints it", n)
		}
	})
}

// rollbackRig is design 16 A28's sixth critique's ungated fixture: A served
// on cfg, B on another image promoted, A retained. It returns A's digest.
func rollbackRig(t *testing.T, name string) (string, *assaydv1alpha1.Agent, *controller.AgentReconciler, string) {
	t.Helper()
	ns := newNamespace(t)
	putConfigMap(t, ns, "safe")
	a := mustCreateAgent(t, ns, name, func(a *assaydv1alpha1.Agent) { a.Spec.Runtime.Env = modeFrom("cfg") })
	r := newReconciler(true)
	promote := func() {
		t.Helper()
		reconcileOnce(t, r, a)
		reconcileOnce(t, r, a)
		rev := liveAgent(t, a).Status.CandidateRevision
		if rev == "" {
			t.Fatalf("setup: no candidate recorded: %+v", liveAgent(t, a).Status)
		}
		markAvailable(t, ns, controller.WorkloadName(name, rev), 1)
		reconcileOnce(t, r, a)
	}
	promote()
	dA := liveAgent(t, a).Status.ActiveRevisionDigest
	mustEdit(t, a, func(a *assaydv1alpha1.Agent) { a.Spec.Runtime.Image = imageB })
	promote()
	if got := liveAgent(t, a).Status.ActiveRevisionDigest; got == dA || got == "" {
		t.Fatalf("setup: B did not promote (active %s)", got)
	}
	return ns, a, r, dA
}

// wantRolledBack asserts A is the active revision and the phase Ready.
func wantRolledBack(t *testing.T, a *assaydv1alpha1.Agent, dA string) {
	t.Helper()
	got := liveAgent(t, a)
	if got.Status.ActiveRevisionDigest != dA || got.Status.Phase != assaydv1alpha1.PhaseReady {
		t.Errorf("after the repair the active revision is %s in phase %s, want A (%s), Ready",
			got.Status.ActiveRevisionDigest, got.Status.Phase, dA)
	}
}

// Design 02 A80, §5's rollback row: the source A reads deleted and a pin to A
// written; the exit requeues at CardDriftInterval, and once the source is
// re-created, with content other than A's, the next pass judges and admits
// the pin with no spec write. The restoration half is driven by hand, so only
// the RequeueAfter assertion depends on the requeue.
func TestARestoredSourceEndsAPinnedRollbacksRefusal(t *testing.T) {
	ns, a, r, dA := rollbackRig(t, "restoredsource")
	deleteConfigMap(t, ns)
	pinTo(t, a, dA)
	res := reconcileResult(t, r, a)
	condIs(t, a, assaydv1alpha1.CondEnvSourceUnresolved, metav1.ConditionTrue, "Unresolved")
	if res.RequeueAfter != controller.CardDriftInterval {
		t.Errorf("the pass at EnvSourceUnresolved returned RequeueAfter %v, want %v (design 02 A80)",
			res.RequeueAfter, controller.CardDriftInterval)
	}
	putConfigMap(t, ns, "other")
	reconcileResult(t, r, a)
	wantRolledBack(t, a, dA)
}

// Design 02 A80's scope: the exit taken on a digest error with every source
// resolved — an envFrom entry that names no source, which the API server
// admits — keeps no requeue, since only a spec write ends it.
func TestADigestErrorAtTheExitDoesNotRequeue(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "digesterror", func(a *assaydv1alpha1.Agent) {
		a.Spec.Runtime.EnvFrom = []corev1.EnvFromSource{{Prefix: "X_"}}
	})
	r := newReconciler(false)
	reconcileOnce(t, r, a) // the finalizer
	res := reconcileResult(t, r, a)
	condIs(t, a, assaydv1alpha1.CondEnvSourceUnresolved, metav1.ConditionTrue, "Unresolved")
	if res.RequeueAfter != 0 || res.Requeue { //nolint:staticcheck // Requeue is what must not be set
		t.Errorf("the exit on a digest error returned %+v, want no requeue", res)
	}
}

// Design 02 A80 (R): a pin to A written while LabelAuthorityAbsent stands;
// the terminal exit requeues at CardDriftInterval, and once the authority is
// restored the next pass, driven by hand as the requeue would bring it,
// admits the pin. Only the RequeueAfter assertion depends on (R).
func TestARepairedTerminalRunNamespaceCauseIsFoundOnTheRequeue(t *testing.T) {
	_, a, r, dA := rollbackRig(t, "terminalrepair")
	r.LabelAuthorityPresent = func(context.Context) (bool, error) { return false, nil }
	pinTo(t, a, dA)
	res := reconcileResult(t, r, a)
	condIs(t, a, assaydv1alpha1.CondRunNamespaceUnavailable, metav1.ConditionTrue, "LabelAuthorityAbsent")
	if res.RequeueAfter != controller.CardDriftInterval {
		t.Errorf("the terminal run-namespace exit returned RequeueAfter %v, want %v (design 02 A80 (R))",
			res.RequeueAfter, controller.CardDriftInterval)
	}
	r.LabelAuthorityPresent = labelAuthorityPresent
	reconcileResult(t, r, a)
	wantRolledBack(t, a, dA)
}
