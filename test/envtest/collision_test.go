// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package envtest

import (
	"context"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/controller"
	"github.com/Quinyte/assayd/internal/revision"
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
func pinnedCollidingSpecs() (safe, evil assaydv1alpha1.AgentSpec) {
	safe = assaydv1alpha1.AgentSpec{Runtime: &assaydv1alpha1.AgentRuntime{
		Image: "ghcr.io/acme/agent@sha256:a100000000000000000000000000000000000000000000000000000000000001",
		Port:  8080,
		Env:   []corev1.EnvVar{{Name: "PAD", Value: "1917962"}}}}
	evil = assaydv1alpha1.AgentSpec{Runtime: &assaydv1alpha1.AgentRuntime{
		Image: "ghcr.io/attacker/backdoor@sha256:b200000000000000000000000000000000000000000000000000000000000002",
		Port:  8080,
		Env:   []corev1.EnvVar{{Name: "PAD", Value: "x216079"}}}}
	return safe, evil
}

func TestACollidingSpecCannotRewriteAGatedWorkload(t *testing.T) {
	safe, evil := pinnedCollidingSpecs()
	if revision.MustHash(safe) != revision.MustHash(evil) {
		t.Fatalf("the pinned pair no longer collides; see internal/revision/collision_test.go")
	}
	name := controller.WorkloadName("collide", revision.MustHash(safe))

	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "collide", func(a *assaydv1alpha1.Agent) { a.Spec = safe })
	r := newReconciler(false)
	settle(t, r, a)
	markAvailable(t, ns, name, 1)
	got := settle(t, r, a)

	if got.Status.ActiveRevision != revision.MustHash(safe) {
		t.Fatalf("setup: safe revision did not become active, got %q", got.Status.ActiveRevision)
	}
	if got.Status.ActiveRevisionDigest != revision.MustDigest(safe) {
		t.Fatalf("setup: status did not persist the full digest, got %q", got.Status.ActiveRevisionDigest)
	}

	// The attack: same revision NAME, different image.
	var live assaydv1alpha1.Agent
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &live); err != nil {
		t.Fatalf("get agent: %v", err)
	}
	live.Spec = evil
	if err := k8s.Update(context.Background(), &live); err != nil {
		t.Fatalf("update agent to the colliding spec: %v", err)
	}
	reconcileOnce(t, r, &live)

	var d appsv1.Deployment
	if err := k8s.Get(context.Background(), types.NamespacedName{Namespace: runNS(ns), Name: name}, &d); err != nil {
		t.Fatalf("get workload: %v", err)
	}
	if img := d.Spec.Template.Spec.Containers[0].Image; img != safe.Runtime.Image {
		t.Errorf("the gated workload now runs %q.\n"+
			"A chosen 40-bit collision rewrote a revision that had already passed its gate — "+
			"ADR-0006 is bypassed entirely and status still names the revision that passed.", img)
	}

	var after assaydv1alpha1.Agent
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &after); err != nil {
		t.Fatalf("get agent: %v", err)
	}
	c := condition(&after, assaydv1alpha1.CondRevisionHashCollision)
	if c == nil || c.Status != metav1.ConditionTrue {
		t.Errorf("no RevisionHashCollision condition: the operator refused the write and said nothing, " +
			"which NFR-8 forbids — a degraded path names its consequence")
	}
	if ready := condition(&after, assaydv1alpha1.CondReady); ready == nil || ready.Status != metav1.ConditionFalse {
		t.Error("Ready stayed True while the operator was refusing to converge the agent's own spec")
	}
}

// Status, not the Deployment, is the authority. A code review found the first
// version of this guard read only a assayd.dev/revision-digest annotation off the
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
			key := types.NamespacedName{Namespace: runNS(ns), Name: workload}
			if err := k8s.Get(context.Background(), key, &d); err != nil {
				t.Fatalf("get workload: %v", err)
			}
			if err := k8s.Delete(context.Background(), &d); err != nil {
				t.Fatalf("delete workload: %v", err)
			}
		}},
		{"annotation stripped", func(t *testing.T, ns, workload string) {
			var d appsv1.Deployment
			key := types.NamespacedName{Namespace: runNS(ns), Name: workload}
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
			a := mustCreateAgent(t, ns, "guard", func(a *assaydv1alpha1.Agent) { a.Spec = safe })
			r := newReconciler(false)
			settle(t, r, a)
			workload := controller.WorkloadName("guard", revision.MustHash(safe))
			markAvailable(t, ns, workload, 1)
			got := settle(t, r, a)
			if got.Status.ActiveRevisionDigest != revision.MustDigest(safe) {
				t.Fatalf("setup: safe revision is not active")
			}

			tc.sabotage(t, ns, workload)

			var live assaydv1alpha1.Agent
			if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &live); err != nil {
				t.Fatalf("get agent: %v", err)
			}
			live.Spec = evil
			if err := k8s.Update(context.Background(), &live); err != nil {
				t.Fatalf("update to the colliding spec: %v", err)
			}
			reconcileOnce(t, r, &live)

			var after assaydv1alpha1.Agent
			if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &after); err != nil {
				t.Fatalf("get agent: %v", err)
			}
			if c := condition(&after, assaydv1alpha1.CondRevisionHashCollision); c == nil ||
				c.Status != metav1.ConditionTrue {
				t.Errorf("no collision reported with the %s: the guard depended on the "+
					"Deployment, which the attacking principal controls", tc.name)
			}
			if after.Status.ActiveRevisionDigest != revision.MustDigest(safe) {
				t.Error("the colliding spec became the active revision")
			}
			// Degraded is ASSERTED on this path, not merely implied by the phase.
			// CondDegraded is owned and non-sticky, so a branch that sets the phase
			// and says nothing actively clears it — and a suspected chosen collision
			// is the worst state this machine has.
			if d := condition(&after, assaydv1alpha1.CondDegraded); d == nil ||
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
	a := mustCreateAgent(t, ns, "repair", func(a *assaydv1alpha1.Agent) { a.Spec = safe })
	r := newReconciler(false)
	settle(t, r, a)
	markAvailable(t, ns, controller.WorkloadName("repair", revision.MustHash(safe)), 1)
	settle(t, r, a)

	set := func(t *testing.T, spec assaydv1alpha1.AgentSpec) assaydv1alpha1.Agent {
		t.Helper()
		var live assaydv1alpha1.Agent
		if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &live); err != nil {
			t.Fatalf("get agent: %v", err)
		}
		live.Spec = spec
		if err := k8s.Update(context.Background(), &live); err != nil {
			t.Fatalf("update: %v", err)
		}
		reconcileOnce(t, r, &live)
		var got assaydv1alpha1.Agent
		if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &got); err != nil {
			t.Fatalf("get agent: %v", err)
		}
		return got
	}

	if c := condition(ptr(set(t, evil)), assaydv1alpha1.CondRevisionHashCollision); c == nil ||
		c.Status != metav1.ConditionTrue {
		t.Fatal("setup: the colliding spec did not raise the condition")
	}
	// Repair: an ordinary spec that shares no name with the active revision.
	repaired := safe
	repaired.Runtime = safe.Runtime.DeepCopy()
	repaired.Runtime.Image = "ghcr.io/acme/agent@sha256:5669fbc273a09c85000000000000000000000000000000000000000000000000"
	after := set(t, repaired)

	if c := condition(&after, assaydv1alpha1.CondRevisionHashCollision); c != nil &&
		c.Status == metav1.ConditionTrue {
		t.Errorf("the collision condition survived the repair, with message: %s\n"+
			"A stale True on a healthy agent is the loud-and-wrong of rule 8, and it is what "+
			"teaches an operator that this condition means nothing.", c.Message)
	}
}

func ptr(a assaydv1alpha1.Agent) *assaydv1alpha1.Agent { return &a }

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
	a := mustCreateAgent(t, ns, "norecord", func(a *assaydv1alpha1.Agent) { a.Spec = safe })
	r := newReconciler(false)
	settle(t, r, a)
	markAvailable(t, ns, controller.WorkloadName("norecord", revision.MustHash(safe)), 1)
	settle(t, r, a)

	// Status forgets; the cluster does not.
	var live assaydv1alpha1.Agent
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

	var after assaydv1alpha1.Agent
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &after); err != nil {
		t.Fatalf("get agent: %v", err)
	}
	if c := condition(&after, assaydv1alpha1.CondRevisionHashCollision); c == nil ||
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
	active := controller.WorkloadName("lostactive", revision.MustHash(a.Spec))
	markAvailable(t, ns, active, 1)
	got := settle(t, r, a)
	if got.Status.ActiveRevision == "" {
		t.Fatal("setup: nothing became active")
	}

	// Delete the ACTIVE workload, then ask for a different revision.
	var d appsv1.Deployment
	if err := k8s.Get(context.Background(), types.NamespacedName{Namespace: runNS(ns), Name: active}, &d); err != nil {
		t.Fatalf("get workload: %v", err)
	}
	if err := k8s.Delete(context.Background(), &d); err != nil {
		t.Fatalf("delete workload: %v", err)
	}
	var live assaydv1alpha1.Agent
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &live); err != nil {
		t.Fatalf("get agent: %v", err)
	}
	live.Spec.Runtime.Image = "ghcr.io/acme/agent@sha256:1a0149e949ee9d40000000000000000000000000000000000000000000000000"
	if err := k8s.Update(context.Background(), &live); err != nil {
		t.Fatalf("update: %v", err)
	}
	reconcileOnce(t, r, &live)

	var after assaydv1alpha1.Agent
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &after); err != nil {
		t.Fatalf("get agent: %v", err)
	}
	if c := condition(&after, assaydv1alpha1.CondReady); c != nil && c.Status == metav1.ConditionTrue {
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
	key := types.NamespacedName{Namespace: runNS(ns), Name: controller.WorkloadName("stamped", revision.MustHash(a.Spec))}

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
	if got := d.Annotations[controller.RevisionDigestAnnotation]; got != revision.MustDigest(a.Spec) {
		t.Errorf("a freshly created workload carries digest %q, want %q — it was unguarded "+
			"until some later reconcile happened to stamp it", got, revision.MustDigest(a.Spec))
	}
}

// A stripped stamp on a workload that STATUS vouches for must self-heal, not
// wedge. This is the regression an independent code review reproduced: failing
// closed on a missing stamp was applied one step too wide, so a principal with
// only deployments/patch could strip the annotation AND rewrite the image, and
// the workload stayed terminally refused with the attacker's pods running —
// where before it self-corrected through the drift-correction rewrite.
//
// The sequence that fail-closed was meant to stop additionally needs
// agents/update to write a colliding spec, and collisionAgainstStatus refuses
// that before this code runs. So the wider rule protected nothing and traded a
// self-correcting drift for a permanent one.
func TestAStrippedStampOnAVouchedWorkloadSelfHeals(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "selfheal", nil)
	r := newReconciler(false)
	settle(t, r, a)
	name := controller.WorkloadName("selfheal", revision.MustHash(a.Spec))
	markAvailable(t, ns, name, 1)
	got := settle(t, r, a)
	if got.Status.ActiveRevisionDigest == "" {
		t.Fatal("setup: nothing became active, so status vouches for nothing")
	}
	want := a.Spec.Runtime.Image

	// deployments/patch only: rewrite the image AND remove the operator's mark.
	key := types.NamespacedName{Namespace: runNS(ns), Name: name}
	var d appsv1.Deployment
	if err := k8s.Get(context.Background(), key, &d); err != nil {
		t.Fatalf("get workload: %v", err)
	}
	d.Spec.Template.Spec.Containers[0].Image = "ghcr.io/attacker/backdoor@sha256:" +
		"deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	delete(d.Annotations, controller.RevisionDigestAnnotation)
	if err := k8s.Update(context.Background(), &d); err != nil {
		t.Fatalf("tamper: %v", err)
	}

	settle(t, r, a)

	if err := k8s.Get(context.Background(), key, &d); err != nil {
		t.Fatalf("get workload: %v", err)
	}
	if img := d.Spec.Template.Spec.Containers[0].Image; img != want {
		t.Errorf("the tampered image is still running: %q.\n"+
			"Stripping the stamp switched drift correction off permanently, so a WEAKER "+
			"principal than the one the rule was written for gets a persistent compromise.", img)
	}
	if d.Annotations[controller.RevisionDigestAnnotation] != revision.MustDigest(a.Spec) {
		t.Error("the workload was not re-stamped, so the next pass refuses it again")
	}
	var after assaydv1alpha1.Agent
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &after); err != nil {
		t.Fatalf("get agent: %v", err)
	}
	if c := condition(&after, assaydv1alpha1.CondRevisionHashCollision); c != nil &&
		c.Status == metav1.ConditionTrue {
		t.Error("a workload status vouches for was reported as a collision")
	}
}

// The other half: nothing vouches for it, so it stays refused. A squatter's
// object and a workload whose provenance the operator cannot establish are the
// same case.
func TestAnUnvouchedUnstampedWorkloadIsRefused(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "unvouched", nil)
	r := newReconciler(false)
	settle(t, r, a)

	name := controller.WorkloadName("unvouched", revision.MustHash(a.Spec))
	key := types.NamespacedName{Namespace: runNS(ns), Name: name}
	var d appsv1.Deployment
	if err := k8s.Get(context.Background(), key, &d); err != nil {
		t.Fatalf("get workload: %v", err)
	}
	before := d.Spec.Template.Spec.Containers[0].Image
	delete(d.Annotations, controller.RevisionDigestAnnotation)
	if err := k8s.Update(context.Background(), &d); err != nil {
		t.Fatalf("strip: %v", err)
	}
	// Status forgets, so nothing non-forgeable vouches for the pair.
	var live assaydv1alpha1.Agent
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &live); err != nil {
		t.Fatalf("get agent: %v", err)
	}
	live.Status.ActiveRevision, live.Status.ActiveRevisionDigest = "", ""
	live.Status.CandidateRevision, live.Status.CandidateRevisionDigest = "", ""
	if err := k8s.Status().Update(context.Background(), &live); err != nil {
		t.Fatalf("wipe status: %v", err)
	}
	reconcileOnce(t, r, a)

	var after assaydv1alpha1.Agent
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &after); err != nil {
		t.Fatalf("get agent: %v", err)
	}
	if c := condition(&after, assaydv1alpha1.CondRevisionHashCollision); c == nil ||
		c.Status != metav1.ConditionTrue {
		t.Error("a workload nothing vouches for was adopted")
	}
	if err := k8s.Get(context.Background(), key, &d); err != nil {
		t.Fatalf("get workload: %v", err)
	}
	if got := d.Spec.Template.Spec.Containers[0].Image; got != before {
		t.Errorf("the pod template was rewritten on an object whose provenance could not be "+
			"established: %q -> %q", before, got)
	}
}

// Availability authorizes PROMOTION// Availability authorizes PROMOTION, so it must answer "available workload of
// WHICH revision?". A same-named Deployment created by anyone with
// deployments/create is Available too, and promoting on replicas alone made the
// attacker's object the operator's evidence.
func TestAnUnstampedAvailableWorkloadDoesNotPromote(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "notmine", nil)
	r := newReconciler(false)
	settle(t, r, a)
	name := controller.WorkloadName("notmine", revision.MustHash(a.Spec))
	markAvailable(t, ns, name, 1)

	// Strip the operator's mark, keeping the object Available.
	var d appsv1.Deployment
	key := types.NamespacedName{Namespace: runNS(ns), Name: name}
	if err := k8s.Get(context.Background(), key, &d); err != nil {
		t.Fatalf("get workload: %v", err)
	}
	delete(d.Annotations, controller.RevisionDigestAnnotation)
	if err := k8s.Update(context.Background(), &d); err != nil {
		t.Fatalf("strip annotation: %v", err)
	}
	markAvailable(t, ns, name, 1)

	var live assaydv1alpha1.Agent
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &live); err != nil {
		t.Fatalf("get agent: %v", err)
	}
	live.Status.ActiveRevision, live.Status.ActiveRevisionDigest = "", ""
	live.Status.CandidateRevision, live.Status.CandidateRevisionDigest = "", ""
	if err := k8s.Status().Update(context.Background(), &live); err != nil {
		t.Fatalf("wipe status: %v", err)
	}
	reconcileOnce(t, r, a)

	var after assaydv1alpha1.Agent
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &after); err != nil {
		t.Fatalf("get agent: %v", err)
	}
	if after.Status.ActiveRevision != "" {
		t.Errorf("an unstamped but Available workload promoted revision %q; its own status was "+
			"the only evidence the operator consulted", after.Status.ActiveRevision)
	}
}

// The audit entry is `<name>@<digest>`, and removal compared the bare name — so
// a revision that came BACK read as permanently abandoned. Nothing covered the
// removal path; the only assertion on this field checked the append.
func TestARevisionThatBecomesActiveAgainLeavesTheAbandonedList(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "aba", nil)
	r := newReconciler(false)
	settle(t, r, a)
	first := revision.MustHash(a.Spec)
	firstSpec := *a.Spec.Runtime.DeepCopy()

	set := func(t *testing.T, image string) {
		t.Helper()
		var live assaydv1alpha1.Agent
		if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &live); err != nil {
			t.Fatalf("get agent: %v", err)
		}
		live.Spec.Runtime.Image = image
		if err := k8s.Update(context.Background(), &live); err != nil {
			t.Fatalf("update: %v", err)
		}
		settle(t, r, &live)
		markAvailable(t, ns, controller.WorkloadName("aba", revision.MustHash(live.Spec)), 1)
		settle(t, r, &live)
	}

	set(t, "ghcr.io/acme/agent@sha256:"+
		"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb") // A -> B
	set(t, firstSpec.Image) // B -> A

	var after assaydv1alpha1.Agent
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &after); err != nil {
		t.Fatalf("get agent: %v", err)
	}
	if after.Status.ActiveRevision != first {
		t.Fatalf("setup: the first revision is not active again, got %q", after.Status.ActiveRevision)
	}
	for _, e := range after.Status.SupersededCandidates {
		if strings.HasPrefix(e, first+"@") {
			t.Errorf("the ACTIVE, serving revision is still listed as abandoned: %v\n"+
				"An operator reading the audit trail during an incident sees the thing that is "+
				"running described as the thing that was given up on.", after.Status.SupersededCandidates)
		}
	}
}
