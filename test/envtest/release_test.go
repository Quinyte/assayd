// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package envtest

import (
	"context"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/controller"
	"github.com/Quinyte/assayd/internal/revision"
)

// THE property. Design 02 promised instant rollback, retention delivered the
// material, and until ADR-0031 nothing could ask for one — reapplying the old
// YAML does not reach it, because env-source CONTENT is part of the revision
// identity. Once the referenced ConfigMap has drifted, the same spec computes a
// NEW digest and mints a NEW revision instead of returning to the evaluated
// one. This test is that exact sequence.
func TestARollbackUnderSourceDriftSelectsTheRetainedRevision(t *testing.T) {
	ns := newNamespace(t)
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "cfg"},
		Data:       map[string]string{"MODE": "safe"},
	}
	if err := k8s.Create(context.Background(), cm); err != nil {
		t.Fatalf("create configmap: %v", err)
	}
	a := mustCreateAgent(t, ns, "roll", func(a *assaydv1alpha1.Agent) {
		a.Spec.Runtime.Env = []corev1.EnvVar{{
			Name: "MODE",
			ValueFrom: &corev1.EnvVarSource{ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: "cfg"}, Key: "MODE"}},
		}}
	})
	r := newReconciler(false)
	got := settle(t, r, a)

	// R1: the evaluated release. Its name comes from STATUS, not from MustHash —
	// this spec references a ConfigMap, and hashing it without resolving that
	// content is exactly the mistake the rollback exists to prevent.
	r1 := got.Status.ActiveRevision
	if r1 == "" {
		r1 = got.Status.CandidateRevision
	}
	if r1 == "" {
		t.Fatalf("setup: no revision in status: %+v", got.Status)
	}
	var d1 appsv1.Deployment
	if err := k8s.Get(context.Background(), types.NamespacedName{
		Namespace: runNS(ns), Name: controller.WorkloadName("roll", r1)}, &d1); err != nil {
		t.Fatalf("setup: no R1 workload: %v", err)
	}
	r1Digest := d1.Annotations[controller.RevisionDigestAnnotation]
	if r1Digest == "" {
		t.Fatal("setup: R1 workload carries no digest")
	}
	markAvailable(t, ns, controller.WorkloadName("roll", r1), 1)
	settle(t, r, a)

	// The source drifts. This is what makes reapplying YAML useless: the spec
	// text is unchanged, but the CONTENT behind it is not.
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(cm), cm); err != nil {
		t.Fatalf("get cm: %v", err)
	}
	cm.Data["MODE"] = "unreviewed"
	if err := k8s.Update(context.Background(), cm); err != nil {
		t.Fatalf("drift the configmap: %v", err)
	}
	settle(t, r, a)

	// R2 must actually TAKE OVER, or this test is vacuous: with R2 merely minted
	// and never available, R1 stays active on its own and the assertion below
	// passes whether or not the pin does anything. A mutation deleting the pin's
	// only effect SURVIVED this test until R2 was promoted here.
	drifted := settle(t, r, a)
	r2 := drifted.Status.CandidateRevision
	if r2 == "" || r2 == r1 {
		t.Fatalf("fixture: drifting the ConfigMap did not mint a new revision, so there is "+
			"nothing to roll back FROM; status: %+v", drifted.Status)
	}
	markAvailable(t, ns, controller.WorkloadName("roll", r2), 1)
	promoted := settle(t, r, a)
	if promoted.Status.ActiveRevision != r2 {
		t.Fatalf("fixture: R2 did not become active, so there is nothing to roll back FROM; "+
			"active=%q candidate=%q", promoted.Status.ActiveRevision, promoted.Status.CandidateRevision)
	}
	if promoted.Status.ActiveRevisionDigest == r1Digest {
		t.Fatalf("fixture: the active digest is still R1's after R2 promoted")
	}

	// Now ask for R1 back, by full digest.
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), a); err != nil {
		t.Fatalf("get: %v", err)
	}
	a.Spec.Release = &assaydv1alpha1.ReleaseSpec{TargetRevisionDigest: r1Digest}
	if err := k8s.Update(context.Background(), a); err != nil {
		t.Fatalf("pin: %v", err)
	}
	got = settle(t, r, a)

	if got.Status.ActiveRevisionDigest != r1Digest {
		t.Errorf("after pinning R1's digest the active digest is %q, want %q: the rollback "+
			"either minted a new revision from the drifted source or did not take effect",
			got.Status.ActiveRevisionDigest, r1Digest)
	}
	// The evaluated material is what serves — not the drifted bytes.
	var d appsv1.Deployment
	if err := k8s.Get(context.Background(), types.NamespacedName{
		Namespace: runNS(ns), Name: controller.WorkloadName("roll", r1)}, &d); err != nil {
		t.Fatalf("R1's workload is gone after rolling back to it: %v", err)
	}
}

// Asking for the rollback must not itself mint a revision to roll back from.
// The field is control intent and is excluded from the projection; this proves
// the exclusion end to end rather than only in the unit fixture.
func TestPinningARevisionDoesNotMintOne(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "pinmint", nil)
	before := revision.MustHash(a.Spec)

	a.Spec.Release = &assaydv1alpha1.ReleaseSpec{TargetRevisionDigest: strings.Repeat("b", 64)}
	if after := revision.MustHash(a.Spec); after != before {
		t.Errorf("setting the pin moved the revision %s -> %s: asking for a rollback would "+
			"mint a new revision to roll back from", before, after)
	}
}

// An unresolvable pin must refuse loudly, never fall through. Serving the
// ordinary desired spec when the user asked for R1 is the opposite of a
// rollback, at the moment someone is most likely mid-incident.
func TestAnUnresolvablePinRefusesRatherThanServingCurrentSpec(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "badpin", nil)
	r := newReconciler(false)
	settle(t, r, a)

	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), a); err != nil {
		t.Fatalf("get: %v", err)
	}
	a.Spec.Release = &assaydv1alpha1.ReleaseSpec{TargetRevisionDigest: strings.Repeat("c", 64)}
	if err := k8s.Update(context.Background(), a); err != nil {
		t.Fatalf("pin: %v", err)
	}
	got := settle(t, r, a)

	if !meta.IsStatusConditionTrue(got.Status.Conditions, string(assaydv1alpha1.CondDegraded)) {
		t.Errorf("a pin naming a digest no retained revision carries did not degrade the "+
			"Agent; conditions: %v", got.Status.Conditions)
	}
	c := meta.FindStatusCondition(got.Status.Conditions, string(assaydv1alpha1.CondDegraded))
	if c == nil || c.Reason != "ReleasePinUnresolvable" {
		t.Errorf("degraded for the wrong reason: %+v — 'it was refused' is not evidence about "+
			"which rule refused", c)
	}
}

// The pin is a full SHA-256. A 40-bit workload name must not be accepted in its
// place: a chosen collision against one takes about a second (A57), so allowing
// the short form would let an attacker who can write the spec name a revision
// that is not the one the user meant.
func TestTheShortRevisionNameIsNotAcceptedAsAPin(t *testing.T) {
	ns := newNamespace(t)
	a := &assaydv1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "shortpin"},
		Spec: assaydv1alpha1.AgentSpec{
			Runtime: &assaydv1alpha1.AgentRuntime{
				Image: "ghcr.io/acme/agent@sha256:" + strings.Repeat("a", 64)},
			Release: &assaydv1alpha1.ReleaseSpec{TargetRevisionDigest: "1f119ef39b"},
		},
	}
	err := k8s.Create(context.Background(), a)
	if err == nil {
		t.Fatal("the API accepted a 40-bit revision name as a rollback target")
	}
	if !strings.Contains(err.Error(), "targetRevisionDigest") {
		t.Errorf("refused by something other than the digest pattern: %v", err)
	}
}

// BLOCKER, found by review (reviews/02-step2-fable-review.md, T7) and missed
// here. A66 claims a pinned revision is "selected, never recomputed". The test
// above proved that for ConfigMap CONTENT — the one case it covered — and it is
// false for everything else in the spec, because the workload is still rendered
// from `agent.Spec` whatever the pin says.
//
// The failure is worse than not rolling back. Status reports R1 active at R1's
// digest while R1's Deployment runs R2's IMAGE, stamped with R1's digest
// annotation — so the annotation that exists to prove which projection a
// workload came from now certifies a lie, and the operator's own collision
// guard would defend that lie on the next reconcile.
func TestARollbackServesThePinnedRevisionsImage(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "imgroll", nil)
	r := newReconciler(false)
	r1 := revision.MustHash(a.Spec)
	imageR1 := a.Spec.Runtime.Image
	settle(t, r, a)
	markAvailable(t, ns, controller.WorkloadName("imgroll", r1), 1)
	got := settle(t, r, a)

	var d1 appsv1.Deployment
	if err := k8s.Get(context.Background(), types.NamespacedName{
		Namespace: runNS(ns), Name: controller.WorkloadName("imgroll", r1)}, &d1); err != nil {
		t.Fatalf("setup: %v", err)
	}
	r1Digest := d1.Annotations[controller.RevisionDigestAnnotation]

	// R2: a different image, promoted.
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), a); err != nil {
		t.Fatalf("get: %v", err)
	}
	imageR2 := "ghcr.io/acme/agent@sha256:" + strings.Repeat("e", 64)
	a.Spec.Runtime.Image = imageR2
	if err := k8s.Update(context.Background(), a); err != nil {
		t.Fatalf("update: %v", err)
	}
	r2 := revision.MustHash(a.Spec)
	settle(t, r, a)
	markAvailable(t, ns, controller.WorkloadName("imgroll", r2), 1)
	got = settle(t, r, a)
	if got.Status.ActiveRevision != r2 {
		t.Fatalf("fixture: R2 did not promote; active=%q", got.Status.ActiveRevision)
	}

	// Roll back to R1.
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), a); err != nil {
		t.Fatalf("get: %v", err)
	}
	a.Spec.Release = &assaydv1alpha1.ReleaseSpec{TargetRevisionDigest: r1Digest}
	if err := k8s.Update(context.Background(), a); err != nil {
		t.Fatalf("pin: %v", err)
	}
	settle(t, r, a)

	var after appsv1.Deployment
	if err := k8s.Get(context.Background(), types.NamespacedName{
		Namespace: runNS(ns), Name: controller.WorkloadName("imgroll", r1)}, &after); err != nil {
		t.Fatalf("get R1 workload after rollback: %v", err)
	}
	if img := after.Spec.Template.Spec.Containers[0].Image; img != imageR1 {
		t.Errorf("after rolling back to R1, its workload runs %q; R1 was evaluated with %q.\n"+
			"The rollback re-rendered the pinned revision from the CURRENT spec, so it serves "+
			"the revision it was rolling back FROM — while status and the digest annotation "+
			"both say R1.", img, imageR1)
	}
	if img := after.Spec.Template.Spec.Containers[0].Image; img == imageR2 {
		t.Errorf("R1's workload is running R2's image %q", img)
	}
}

// The two refusals A66 claims and that nothing pinned until review found it
// (reviews/02-step2-fable-review.md X2/T5/T6, both SURVIVED as mutations).
// Measured behaviour was correct; the point is that no test would have noticed
// if it stopped being.
func TestAPinIsRefusedWhenItsMaterialCannotVouchForIt(t *testing.T) {
	mk := func(t *testing.T, name, cmName string) (string, string, *assaydv1alpha1.Agent, string) {
		t.Helper()
		ns := newNamespace(t)
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: cmName},
			Data:       map[string]string{"MODE": "safe"},
		}
		if err := k8s.Create(context.Background(), cm); err != nil {
			t.Fatalf("create cm: %v", err)
		}
		a := mustCreateAgent(t, ns, name, func(a *assaydv1alpha1.Agent) {
			a.Spec.Runtime.Env = []corev1.EnvVar{{Name: "MODE", ValueFrom: &corev1.EnvVarSource{
				ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: cmName}, Key: "MODE"}}}}
		})
		r := newReconciler(false)
		got := settle(t, r, a)
		rev := got.Status.ActiveRevision
		if rev == "" {
			rev = got.Status.CandidateRevision
		}
		var d appsv1.Deployment
		if err := k8s.Get(context.Background(), types.NamespacedName{
			Namespace: runNS(ns), Name: controller.WorkloadName(name, rev)}, &d); err != nil {
			t.Fatalf("setup: %v", err)
		}
		return ns, rev, a, d.Annotations[controller.RevisionDigestAnnotation]
	}

	t.Run("its immutable copy has been collected", func(t *testing.T) {
		ns, rev, a, dig := mk(t, "gonecopy", "cfg")
		r := newReconciler(false)
		var copies corev1.ConfigMapList
		if err := k8s.List(context.Background(), &copies, client.InNamespace(runNS(ns)),
			client.MatchingLabels{controller.MaterialRevisionLabel: rev}); err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(copies.Items) == 0 {
			t.Fatal("setup: no immutable copy to delete")
		}
		if err := k8s.Delete(context.Background(), &copies.Items[0]); err != nil {
			t.Fatalf("delete copy: %v", err)
		}
		if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), a); err != nil {
			t.Fatalf("get: %v", err)
		}
		a.Spec.Release = &assaydv1alpha1.ReleaseSpec{TargetRevisionDigest: dig}
		if err := k8s.Update(context.Background(), a); err != nil {
			t.Fatalf("pin: %v", err)
		}
		got := settle(t, r, a)
		c := meta.FindStatusCondition(got.Status.Conditions, string(assaydv1alpha1.CondDegraded))
		if c == nil || c.Reason != "ReleasePinUnresolvable" {
			t.Errorf("a revision whose evaluated bytes no longer exist was not refused: %+v.\n"+
				"Reading the user's object instead would serve content that revision never saw.", c)
		}
	})

	t.Run("the spec's source list changed shape", func(t *testing.T) {
		ns, _, a, dig := mk(t, "shapechange", "cfg1")
		r := newReconciler(false)
		other := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "cfg2"},
			Data:       map[string]string{"MODE": "other"},
		}
		if err := k8s.Create(context.Background(), other); err != nil {
			t.Fatalf("create cfg2: %v", err)
		}
		if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), a); err != nil {
			t.Fatalf("get: %v", err)
		}
		a.Spec.Runtime.Env[0].ValueFrom.ConfigMapKeyRef.Name = "cfg2"
		a.Spec.Release = &assaydv1alpha1.ReleaseSpec{TargetRevisionDigest: dig}
		if err := k8s.Update(context.Background(), a); err != nil {
			t.Fatalf("update: %v", err)
		}
		got := settle(t, r, a)
		c := meta.FindStatusCondition(got.Status.Conditions, string(assaydv1alpha1.CondDegraded))
		if c == nil || c.Reason != "ReleasePinUnresolvable" {
			t.Errorf("a pin whose spec declares different sources was not refused: %+v.\n"+
				"Copies are named by POSITION, so mapping them across a changed list would "+
				"hand the revision somebody else's bytes.", c)
		}
	})
}
