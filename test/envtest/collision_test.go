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

// Status, not the Deployment, is the authority. A code review found the first
// version of this guard read only a plume.dev/revision-digest annotation off the
// Deployment — closing the collision against an agents/update principal and
// leaving it open to a WEAKER deployments/patch one, who could set the
// annotation to the digest of the spec they were about to write, or delete it
// and have the adoption rule bless the result.
//
// These two cases are what the annotation could not cover: no workload at all,
// and a workload whose annotation has been stripped.
func TestACollidingSpecIsRefusedWithNoWorkloadAndWithAStrippedAnnotation(t *testing.T) {
	safe, evil := pinnedCollidingSpecs()
	for _, tc := range []struct {
		name     string
		sabotage func(t *testing.T, ns, workload string)
	}{
		{"workload deleted", func(t *testing.T, ns, workload string) {
			var d appsv1.Deployment
			key := types.NamespacedName{Namespace: ns, Name: workload}
			if err := k8s.Get(context.Background(), key, &d); err != nil {
				t.Fatalf("get workload: %v", err)
			}
			if err := k8s.Delete(context.Background(), &d); err != nil {
				t.Fatalf("delete workload: %v", err)
			}
		}},
		{"annotation stripped", func(t *testing.T, ns, workload string) {
			var d appsv1.Deployment
			key := types.NamespacedName{Namespace: ns, Name: workload}
			if err := k8s.Get(context.Background(), key, &d); err != nil {
				t.Fatalf("get workload: %v", err)
			}
			delete(d.Annotations, controller.RevisionDigestAnnotation)
			if err := k8s.Update(context.Background(), &d); err != nil {
				t.Fatalf("strip annotation: %v", err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ns := newNamespace(t)
			a := mustCreateAgent(t, ns, "guard", func(a *plumev1alpha1.Agent) { a.Spec = safe })
			r := newReconciler(false)
			settle(t, r, a)
			workload := controller.WorkloadName("guard", revision.Hash(safe))
			markAvailable(t, ns, workload, 1)
			got := settle(t, r, a)
			if got.Status.ActiveRevisionDigest != revision.Digest(safe) {
				t.Fatalf("setup: safe revision is not active")
			}

			tc.sabotage(t, ns, workload)

			var live plumev1alpha1.Agent
			if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &live); err != nil {
				t.Fatalf("get agent: %v", err)
			}
			live.Spec = evil
			if err := k8s.Update(context.Background(), &live); err != nil {
				t.Fatalf("update to the colliding spec: %v", err)
			}
			reconcileOnce(t, r, &live)

			var after plumev1alpha1.Agent
			if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &after); err != nil {
				t.Fatalf("get agent: %v", err)
			}
			if c := condition(&after, plumev1alpha1.CondRevisionHashCollision); c == nil ||
				c.Status != metav1.ConditionTrue {
				t.Errorf("no collision reported with the %s: the guard depended on the "+
					"Deployment, which the attacking principal controls", tc.name)
			}
			if after.Status.ActiveRevisionDigest != revision.Digest(safe) {
				t.Error("the colliding spec became the active revision")
			}
			// Degraded is ASSERTED on this path, not merely implied by the phase.
			// CondDegraded is owned and non-sticky, so a branch that sets the phase
			// and says nothing actively clears it — and a suspected chosen collision
			// is the worst state this machine has.
			if d := condition(&after, plumev1alpha1.CondDegraded); d == nil ||
				d.Status != metav1.ConditionTrue {
				t.Error("phase went Degraded and CondDegraded did not: every alert keyed on the " +
					"condition goes quiet at exactly the wrong moment")
			}
		})
	}
}

// MAJOR 3 from the same review: the condition was in neither ownedTypes nor
// stickyTypes, so merge() carried it forward forever — a repaired agent kept a
// True collision condition with a stale message while reporting Ready.
func TestACollisionClearsWhenTheSpecIsRepaired(t *testing.T) {
	safe, evil := pinnedCollidingSpecs()
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "repair", func(a *plumev1alpha1.Agent) { a.Spec = safe })
	r := newReconciler(false)
	settle(t, r, a)
	markAvailable(t, ns, controller.WorkloadName("repair", revision.Hash(safe)), 1)
	settle(t, r, a)

	set := func(t *testing.T, spec plumev1alpha1.AgentSpec) plumev1alpha1.Agent {
		t.Helper()
		var live plumev1alpha1.Agent
		if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &live); err != nil {
			t.Fatalf("get agent: %v", err)
		}
		live.Spec = spec
		if err := k8s.Update(context.Background(), &live); err != nil {
			t.Fatalf("update: %v", err)
		}
		reconcileOnce(t, r, &live)
		var got plumev1alpha1.Agent
		if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &got); err != nil {
			t.Fatalf("get agent: %v", err)
		}
		return got
	}

	if c := condition(ptr(set(t, evil)), plumev1alpha1.CondRevisionHashCollision); c == nil ||
		c.Status != metav1.ConditionTrue {
		t.Fatal("setup: the colliding spec did not raise the condition")
	}
	// Repair: an ordinary spec that shares no name with the active revision.
	repaired := safe
	repaired.Runtime = safe.Runtime.DeepCopy()
	repaired.Runtime.Image = "ghcr.io/acme/agent:2.0.0"
	after := set(t, repaired)

	if c := condition(&after, plumev1alpha1.CondRevisionHashCollision); c != nil &&
		c.Status == metav1.ConditionTrue {
		t.Errorf("the collision condition survived the repair, with message: %s\n"+
			"A stale True on a healthy agent is the loud-and-wrong of rule 8, and it is what "+
			"teaches an operator that this condition means nothing.", c.Message)
	}
}

func ptr(a plumev1alpha1.Agent) *plumev1alpha1.Agent { return &a }

// The annotation is corroboration, and this is the one case where it is the ONLY
// evidence: the workload is stamped and status carries no digest for that name,
// so the status guard has nothing to compare. It is reachable whenever status
// and the cluster disagree about history — a status subresource restored from a
// backup that predates the revision, or an Agent recreated under the same name
// before its old Deployment is garbage-collected.
//
// Without this the annotation check is unreachable code, and rule 5 says
// unpinned defensive code is a liability: the next reader trusts it.
func TestAStampedWorkloadIsNotAdoptedWhenStatusHasNoRecord(t *testing.T) {
	safe, evil := pinnedCollidingSpecs()
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "norecord", func(a *plumev1alpha1.Agent) { a.Spec = safe })
	r := newReconciler(false)
	settle(t, r, a)
	markAvailable(t, ns, controller.WorkloadName("norecord", revision.Hash(safe)), 1)
	settle(t, r, a)

	// Status forgets; the cluster does not.
	var live plumev1alpha1.Agent
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &live); err != nil {
		t.Fatalf("get agent: %v", err)
	}
	live.Status.ActiveRevision, live.Status.ActiveRevisionDigest = "", ""
	live.Status.CandidateRevision, live.Status.CandidateRevisionDigest = "", ""
	if err := k8s.Status().Update(context.Background(), &live); err != nil {
		t.Fatalf("wipe status: %v", err)
	}
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &live); err != nil {
		t.Fatalf("get agent: %v", err)
	}
	live.Spec = evil
	if err := k8s.Update(context.Background(), &live); err != nil {
		t.Fatalf("update to the colliding spec: %v", err)
	}
	reconcileOnce(t, r, &live)

	var after plumev1alpha1.Agent
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &after); err != nil {
		t.Fatalf("get agent: %v", err)
	}
	if c := condition(&after, plumev1alpha1.CondRevisionHashCollision); c == nil ||
		c.Status != metav1.ConditionTrue {
		t.Error("a workload stamped with a DIFFERENT revision digest was adopted by name because " +
			"status had forgotten it; the new spec's image would install under the old identity")
	}
}

// MAJOR 6 from the same review: this branch reported "revision X is serving"
// from the presence of a name in status. `ready` is only ever computed for the
// DESIRED revision, so an agent whose active workload had been deleted read as a
// healthy rollout instead of the outage it is.
func TestARolloutIsNotReportedReadyWhenTheActiveRevisionHasNoWorkload(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "lostactive", nil)
	r := newReconciler(false)
	settle(t, r, a)
	active := controller.WorkloadName("lostactive", revision.Hash(a.Spec))
	markAvailable(t, ns, active, 1)
	got := settle(t, r, a)
	if got.Status.ActiveRevision == "" {
		t.Fatal("setup: nothing became active")
	}

	// Delete the ACTIVE workload, then ask for a different revision.
	var d appsv1.Deployment
	if err := k8s.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: active}, &d); err != nil {
		t.Fatalf("get workload: %v", err)
	}
	if err := k8s.Delete(context.Background(), &d); err != nil {
		t.Fatalf("delete workload: %v", err)
	}
	var live plumev1alpha1.Agent
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &live); err != nil {
		t.Fatalf("get agent: %v", err)
	}
	live.Spec.Runtime.Image = "ghcr.io/acme/agent:9.9.9"
	if err := k8s.Update(context.Background(), &live); err != nil {
		t.Fatalf("update: %v", err)
	}
	reconcileOnce(t, r, &live)

	var after plumev1alpha1.Agent
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &after); err != nil {
		t.Fatalf("get agent: %v", err)
	}
	if c := condition(&after, plumev1alpha1.CondReady); c != nil && c.Status == metav1.ConditionTrue {
		t.Errorf("Ready=True with reason %q while the active revision has no workload at all. "+
			"Nothing is serving, and the message says something is.", c.Reason)
	}
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

// A workload carrying no stamp is REFUSED, not adopted.
//
// An earlier version adopted and stamped it, reasoning that a workload predating
// the field would otherwise wedge an upgraded cluster — and this test pinned
// that. There is no such workload: plume is unreleased, so the migration had
// nothing to migrate and was purely an attack surface. Codex reproduced it:
// strip the annotation, update the Agent to a colliding spec, and the operator
// rewrites the pod template from the CURRENT spec and stamps the result as
// legitimate. Legacy identity reconstructed from current spec is the collision
// payload with an extra step.
func TestAWorkloadWithNoRecordedDigestIsRefused(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "unstamped", nil)
	r := newReconciler(false)
	settle(t, r, a)

	name := controller.WorkloadName("unstamped", revision.Hash(a.Spec))
	key := types.NamespacedName{Namespace: ns, Name: name}
	var d appsv1.Deployment
	if err := k8s.Get(context.Background(), key, &d); err != nil {
		t.Fatalf("get workload: %v", err)
	}
	before := d.Spec.Template.Spec.Containers[0].Image
	delete(d.Annotations, controller.RevisionDigestAnnotation)
	if err := k8s.Update(context.Background(), &d); err != nil {
		t.Fatalf("strip the digest annotation: %v", err)
	}

	reconcileOnce(t, r, a)

	var after plumev1alpha1.Agent
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &after); err != nil {
		t.Fatalf("get agent: %v", err)
	}
	if c := condition(&after, plumev1alpha1.CondRevisionHashCollision); c == nil ||
		c.Status != metav1.ConditionTrue {
		t.Error("an unstamped workload was adopted rather than refused")
	}
	if err := k8s.Get(context.Background(), key, &d); err != nil {
		t.Fatalf("get workload: %v", err)
	}
	if got := d.Spec.Template.Spec.Containers[0].Image; got != before {
		t.Errorf("the pod template was rewritten from current spec on an object whose provenance "+
			"the operator could not establish: image went %q -> %q", before, got)
	}
}

// Availability authorizes PROMOTION, so it must answer "available workload of
// WHICH revision?". A same-named Deployment created by anyone with
// deployments/create is Available too, and promoting on replicas alone made the
// attacker's object the operator's evidence.
func TestAnUnstampedAvailableWorkloadDoesNotPromote(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "notmine", nil)
	r := newReconciler(false)
	settle(t, r, a)
	name := controller.WorkloadName("notmine", revision.Hash(a.Spec))
	markAvailable(t, ns, name, 1)

	// Strip the operator's mark, keeping the object Available.
	var d appsv1.Deployment
	key := types.NamespacedName{Namespace: ns, Name: name}
	if err := k8s.Get(context.Background(), key, &d); err != nil {
		t.Fatalf("get workload: %v", err)
	}
	delete(d.Annotations, controller.RevisionDigestAnnotation)
	if err := k8s.Update(context.Background(), &d); err != nil {
		t.Fatalf("strip annotation: %v", err)
	}
	markAvailable(t, ns, name, 1)

	var live plumev1alpha1.Agent
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &live); err != nil {
		t.Fatalf("get agent: %v", err)
	}
	live.Status.ActiveRevision, live.Status.ActiveRevisionDigest = "", ""
	live.Status.CandidateRevision, live.Status.CandidateRevisionDigest = "", ""
	if err := k8s.Status().Update(context.Background(), &live); err != nil {
		t.Fatalf("wipe status: %v", err)
	}
	reconcileOnce(t, r, a)

	var after plumev1alpha1.Agent
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &after); err != nil {
		t.Fatalf("get agent: %v", err)
	}
	if after.Status.ActiveRevision != "" {
		t.Errorf("an unstamped but Available workload promoted revision %q; its own status was "+
			"the only evidence the operator consulted", after.Status.ActiveRevision)
	}
}
