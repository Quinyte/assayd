package envtest

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	plumev1alpha1 "github.com/Quinyte/plume/api/v1alpha1"
	"github.com/Quinyte/plume/internal/controller"
	"github.com/Quinyte/plume/internal/revision"
)

// The end-to-end shape of Codex r7 BLOCKER 1, against a real API server.
//
// Two specs naming different images share one ten-character revision name. The
// safe one is created, reconciled and promoted — it is the active revision. The
// spec is then replaced by the colliding one. An operator comparing NAMES sees
// activeRevision == desired, concludes there is nothing to gate, and converges
// the Deployment named by that revision: the attacker's image is now running
// under the gate result the safe spec earned, with status still reporting the
// revision that passed.
//
// This asserts the image on the cluster, not the condition alone. A test that
// only checked the condition would pass on an operator that reported the
// collision AND rewrote the pod template anyway, which is the failure that
// matters.
// pinnedCollidingSpecs is the pair from internal/revision/collision_test.go:
// two specs naming different images that project to one revision NAME.
func pinnedCollidingSpecs() (safe, evil plumev1alpha1.AgentSpec) {
	safe = plumev1alpha1.AgentSpec{Runtime: &plumev1alpha1.AgentRuntime{
		Image: "ghcr.io/acme/agent:1.0.0",
		Env:   []corev1.EnvVar{{Name: "PAD", Value: "654623"}}}}
	evil = plumev1alpha1.AgentSpec{Runtime: &plumev1alpha1.AgentRuntime{
		Image: "ghcr.io/attacker/backdoor:1.0.0",
		Env:   []corev1.EnvVar{{Name: "PAD", Value: "x1702559"}}}}
	return safe, evil
}

func TestACollidingSpecCannotRewriteAGatedWorkload(t *testing.T) {
	safe, evil := pinnedCollidingSpecs()
	if revision.Hash(safe) != revision.Hash(evil) {
		t.Fatalf("the pinned pair no longer collides; see internal/revision/collision_test.go")
	}
	name := controller.WorkloadName("collide", revision.Hash(safe))

	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "collide", func(a *plumev1alpha1.Agent) { a.Spec = safe })
	r := newReconciler(false)
	settle(t, r, a)
	markAvailable(t, ns, name, 1)
	got := settle(t, r, a)

	if got.Status.ActiveRevision != revision.Hash(safe) {
		t.Fatalf("setup: safe revision did not become active, got %q", got.Status.ActiveRevision)
	}
	if got.Status.ActiveRevisionDigest != revision.Digest(safe) {
		t.Fatalf("setup: status did not persist the full digest, got %q", got.Status.ActiveRevisionDigest)
	}

	// The attack: same revision NAME, different image.
	var live plumev1alpha1.Agent
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &live); err != nil {
		t.Fatalf("get agent: %v", err)
	}
	live.Spec = evil
	if err := k8s.Update(context.Background(), &live); err != nil {
		t.Fatalf("update agent to the colliding spec: %v", err)
	}
	reconcileOnce(t, r, &live)

	var d appsv1.Deployment
	if err := k8s.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: name}, &d); err != nil {
		t.Fatalf("get workload: %v", err)
	}
	if img := d.Spec.Template.Spec.Containers[0].Image; img != "ghcr.io/acme/agent:1.0.0" {
		t.Errorf("the gated workload now runs %q.\n"+
			"A chosen 40-bit collision rewrote a revision that had already passed its gate — "+
			"ADR-0006 is bypassed entirely and status still names the revision that passed.", img)
	}

	var after plumev1alpha1.Agent
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &after); err != nil {
		t.Fatalf("get agent: %v", err)
	}
	c := condition(&after, plumev1alpha1.CondRevisionHashCollision)
	if c == nil || c.Status != metav1.ConditionTrue {
		t.Errorf("no RevisionHashCollision condition: the operator refused the write and said nothing, " +
			"which NFR-8 forbids — a degraded path names its consequence")
	}
	if ready := condition(&after, plumev1alpha1.CondReady); ready == nil || ready.Status != metav1.ConditionFalse {
		t.Error("Ready stayed True while the operator was refusing to converge the agent's own spec")
	}
}

// The workload guard is not the only place a name can be mistaken for an
// identity, and a mutation proved the rest was unpinned: reverting the reconcile
// switch to compare NAMES left every test green, because the guard fired first.
//
// This is the case the guard cannot see. The gated workload is DELETED, so
// ensureWorkload creates a fresh one and finds no annotation to disagree with.
// Only the status comparison is left, and on names it says "the colliding spec
// is already the active revision" — skipping the gate for a spec that never
// passed one.
func TestACollidingSpecIsGatedEvenWhenNoWorkloadExists(t *testing.T) {
	safe := plumev1alpha1.AgentSpec{Runtime: &plumev1alpha1.AgentRuntime{
		Image: "ghcr.io/acme/agent:1.0.0",
		Env:   []corev1.EnvVar{{Name: "PAD", Value: "654623"}}},
		Gates: []plumev1alpha1.GateRef{{EvalSuiteRef: "regression"}}}
	evil := plumev1alpha1.AgentSpec{Runtime: &plumev1alpha1.AgentRuntime{
		Image: "ghcr.io/attacker/backdoor:1.0.0",
		Env:   []corev1.EnvVar{{Name: "PAD", Value: "x1702559"}}},
		Gates: []plumev1alpha1.GateRef{{EvalSuiteRef: "regression"}}}
	if revision.Hash(safe) != revision.Hash(evil) {
		t.Skip("gates changed the projection; this pair no longer collides")
	}

	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "nowork", func(a *plumev1alpha1.Agent) { a.Spec = safe })
	r := newReconciler(true) // gates installed, so a candidate must be HELD
	settle(t, r, a)
	name := controller.WorkloadName("nowork", revision.Hash(safe))
	markAvailable(t, ns, name, 1)
	settle(t, r, a)

	// Pretend the gate passed for the SAFE revision, so it is active.
	var live plumev1alpha1.Agent
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &live); err != nil {
		t.Fatalf("get agent: %v", err)
	}
	live.Status.ActiveRevision = revision.Hash(safe)
	live.Status.ActiveRevisionDigest = revision.Digest(safe)
	if err := k8s.Status().Update(context.Background(), &live); err != nil {
		t.Fatalf("seed active revision: %v", err)
	}

	// Remove the workload, so no recorded digest can contradict anything.
	var d appsv1.Deployment
	key := types.NamespacedName{Namespace: ns, Name: name}
	if err := k8s.Get(context.Background(), key, &d); err != nil {
		t.Fatalf("get workload: %v", err)
	}
	if err := k8s.Delete(context.Background(), &d); err != nil {
		t.Fatalf("delete workload: %v", err)
	}

	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &live); err != nil {
		t.Fatalf("get agent: %v", err)
	}
	live.Spec = evil
	if err := k8s.Update(context.Background(), &live); err != nil {
		t.Fatalf("update to the colliding spec: %v", err)
	}
	reconcileOnce(t, r, &live)
	// Make the recreated workload AVAILABLE, so the reconcile switch reaches the
	// gate branch rather than short-circuiting on "not ready yet". Every branch
	// that compares revisions has to be reached to be pinned; one that is not is
	// free to compare names again.
	markAvailable(t, ns, name, 1)
	reconcileOnce(t, r, &live)

	var after plumev1alpha1.Agent
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &after); err != nil {
		t.Fatalf("get agent: %v", err)
	}
	if after.Status.ActiveRevisionDigest == revision.Digest(evil) {
		t.Error("the colliding spec became the ACTIVE revision without passing a gate: " +
			"the reconcile switch compared forty-bit names, so it saw nothing to promote")
	}
	if after.Status.ActiveRevisionDigest != revision.Digest(safe) {
		t.Errorf("the safe revision stopped being active; digest is %q", after.Status.ActiveRevisionDigest)
	}
}

// Case 1 of the reconcile switch: a colliding spec arriving while the active
// revision is healthy and the new workload is not up yet. Comparing names, the
// operator cannot see a rollout at all — it concludes the ACTIVE revision has
// lost its pods and reports Degraded on an agent that is serving perfectly.
func TestACollidingCandidateIsReportedAsARolloutNotAsALostActive(t *testing.T) {
	safe, evil := pinnedCollidingSpecs()
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "rollout", func(a *plumev1alpha1.Agent) { a.Spec = safe })
	r := newReconciler(false)
	settle(t, r, a)
	name := controller.WorkloadName("rollout", revision.Hash(safe))
	markAvailable(t, ns, name, 1)
	settle(t, r, a)

	// The workload must be GONE, or the digest guard intercepts first and this
	// branch is never reached — which is how the first version of this test came
	// to assert on a state the guard had already produced. The guard covers the
	// case where a conflicting workload exists; this covers the case where one
	// does not, and the reconcile switch is the only thing left comparing
	// revisions.
	var d appsv1.Deployment
	if err := k8s.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: name}, &d); err != nil {
		t.Fatalf("get workload: %v", err)
	}
	if err := k8s.Delete(context.Background(), &d); err != nil {
		t.Fatalf("delete workload: %v", err)
	}

	var live plumev1alpha1.Agent
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &live); err != nil {
		t.Fatalf("get agent: %v", err)
	}
	live.Spec = evil
	if err := k8s.Update(context.Background(), &live); err != nil {
		t.Fatalf("update: %v", err)
	}
	reconcileOnce(t, r, &live) // recreates the workload; it is not available yet

	var after plumev1alpha1.Agent
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &after); err != nil {
		t.Fatalf("get agent: %v", err)
	}
	// Assert the ROLLOUT branch specifically, not merely the absence of Degraded.
	// Comparing names sends this to the "nothing is rolling out" branch, which is
	// also not Degraded — so a not-Degraded assertion passes either way and pins
	// nothing. Phase and the Progressing reason are what separate them.
	if after.Status.Phase != plumev1alpha1.PhaseReady {
		t.Errorf("phase is %q, want Ready: a healthy active revision is still serving while a "+
			"different revision comes up. On forty-bit names the operator sees one revision, "+
			"reports Pending, and every alert keyed on phase fires for a routine rollout.",
			after.Status.Phase)
	}
	p := condition(&after, plumev1alpha1.CondProgressing)
	if p == nil || p.Status != metav1.ConditionTrue || p.Reason != "CandidateNotAvailable" {
		t.Errorf("Progressing is %+v, want True/CandidateNotAvailable — the colliding spec is a "+
			"CANDIDATE rolling out, and comparing names makes it invisible as one", p)
	}
	if d := condition(&after, plumev1alpha1.CondDegraded); d != nil && d.Status == metav1.ConditionTrue {
		t.Error("a DIFFERENT revision failing to come up was reported as the active revision " +
			"losing its pods")
	}
}

// Candidate supersession, on the same comparison. A colliding candidate that
// replaces a held one must still be recorded, or the audit trail §3.3 promises
// silently drops a revision that was in flight.
func TestACollidingCandidateStillSupersedesTheHeldOne(t *testing.T) {
	safe, evil := pinnedCollidingSpecs()
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "supersede", func(a *plumev1alpha1.Agent) { a.Spec = safe })
	r := newReconciler(false)
	settle(t, r, a)

	var live plumev1alpha1.Agent
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &live); err != nil {
		t.Fatalf("get agent: %v", err)
	}
	live.Status.CandidateRevision = revision.Hash(safe)
	live.Status.CandidateRevisionDigest = revision.Digest(safe)
	if err := k8s.Status().Update(context.Background(), &live); err != nil {
		t.Fatalf("seed candidate: %v", err)
	}
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &live); err != nil {
		t.Fatalf("get agent: %v", err)
	}
	live.Spec = evil
	if err := k8s.Update(context.Background(), &live); err != nil {
		t.Fatalf("update: %v", err)
	}
	reconcileOnce(t, r, &live)

	var after plumev1alpha1.Agent
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &after); err != nil {
		t.Fatalf("get agent: %v", err)
	}
	if len(after.Status.SupersededCandidates) == 0 {
		t.Error("a colliding candidate replaced a held one and nothing was recorded: on names " +
			"the two look like the same candidate, so the abandoned revision vanishes from the " +
			"audit trail §3.3 says is never silent")
	}
}

// The reachability argument that makes one comparison in the reconcile switch
// safe rests on this: a revision NAME is never present without its digest, and
// the digest always begins with the name. If those ever came apart, a branch
// that reads one and a branch that reads the other would disagree about which
// revision is active.
func TestNameAndDigestAreAlwaysWrittenTogether(t *testing.T) {
	safe, evil := pinnedCollidingSpecs()
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "paired", func(a *plumev1alpha1.Agent) { a.Spec = safe })
	r := newReconciler(false)

	check := func(t *testing.T, at string) {
		t.Helper()
		var got plumev1alpha1.Agent
		if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &got); err != nil {
			t.Fatalf("get agent: %v", err)
		}
		for _, f := range []struct{ what, name, digest string }{
			{"active", got.Status.ActiveRevision, got.Status.ActiveRevisionDigest},
			{"candidate", got.Status.CandidateRevision, got.Status.CandidateRevisionDigest},
		} {
			if (f.name == "") != (f.digest == "") {
				t.Errorf("%s: %s revision name=%q digest=%q — one is set and the other is not",
					at, f.what, f.name, f.digest)
			}
			if f.name != "" && f.digest[:len(f.name)] != f.name {
				t.Errorf("%s: %s digest %q does not begin with its name %q", at, f.what, f.digest, f.name)
			}
		}
	}

	settle(t, r, a)
	check(t, "after first settle")
	markAvailable(t, ns, controller.WorkloadName("paired", revision.Hash(safe)), 1)
	settle(t, r, a)
	check(t, "after promotion")

	var live plumev1alpha1.Agent
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &live); err != nil {
		t.Fatalf("get agent: %v", err)
	}
	live.Spec = evil
	if err := k8s.Update(context.Background(), &live); err != nil {
		t.Fatalf("update: %v", err)
	}
	reconcileOnce(t, r, &live)
	check(t, "after a colliding spec arrives")
}

// The digest is stamped by the CREATE, not only by a later converge. A workload
// that exists unstamped for even one pass is a workload a colliding spec can
// adopt, and the guard above is exactly what cannot fire on it.
func TestTheDigestIsStampedOnCreation(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "stamped", nil)
	r := newReconciler(false)
	key := types.NamespacedName{Namespace: ns, Name: controller.WorkloadName("stamped", revision.Hash(a.Spec))}

	// Reconcile only until the workload FIRST exists, then look immediately.
	// Settling first would hide the window this test is about: a later pass
	// stamping the annotation is exactly the behaviour being ruled out.
	var d appsv1.Deployment
	var found bool
	for i := 0; i < 10 && !found; i++ {
		reconcileOnce(t, r, a)
		found = k8s.Get(context.Background(), key, &d) == nil
	}
	if !found {
		t.Fatal("workload never created")
	}
	if got := d.Annotations[controller.RevisionDigestAnnotation]; got != revision.Digest(a.Spec) {
		t.Errorf("a freshly created workload carries digest %q, want %q — it was unguarded "+
			"until some later reconcile happened to stamp it", got, revision.Digest(a.Spec))
	}
}

// An absent annotation is not a collision. A workload created before the digest
// field existed carries none, and treating unknown as a collision would wedge
// every cluster on upgrade — a fail-closed that fails the wrong thing.
func TestAWorkloadWithNoRecordedDigestIsAdoptedAndStamped(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "legacy", nil)
	r := newReconciler(false)
	settle(t, r, a)

	name := controller.WorkloadName("legacy", revision.Hash(a.Spec))
	key := types.NamespacedName{Namespace: ns, Name: name}
	var d appsv1.Deployment
	if err := k8s.Get(context.Background(), key, &d); err != nil {
		t.Fatalf("get workload: %v", err)
	}
	delete(d.Annotations, controller.RevisionDigestAnnotation)
	if err := k8s.Update(context.Background(), &d); err != nil {
		t.Fatalf("strip the digest annotation: %v", err)
	}

	reconcileOnce(t, r, a)

	if err := k8s.Get(context.Background(), key, &d); err != nil {
		t.Fatalf("get workload: %v", err)
	}
	if got := d.Annotations[controller.RevisionDigestAnnotation]; got != revision.Digest(a.Spec) {
		t.Errorf("an un-stamped workload was not adopted and stamped; annotation is %q", got)
	}
	var after plumev1alpha1.Agent
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &after); err != nil {
		t.Fatalf("get agent: %v", err)
	}
	if c := condition(&after, plumev1alpha1.CondRevisionHashCollision); c != nil && c.Status == metav1.ConditionTrue {
		t.Error("a missing annotation was reported as a collision; every upgraded cluster would wedge")
	}
}
