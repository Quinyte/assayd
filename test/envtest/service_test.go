// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package envtest

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/controller"
	"github.com/Quinyte/assayd/internal/revision"
)

// ADR-0030 step 2. Until 2026-09-05 no Service existed at all: a Pod could
// report Ready with no stable address, and design 03's route backendRef named
// an object nothing created. §5 recorded it as a guarantee nothing enforced.
func TestARevisionGetsAServiceThatSelectsOnlyItsOwnPods(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "svc", nil)
	r := newReconciler(false)
	rev := revision.MustHash(a.Spec)
	settle(t, r, a)

	var s corev1.Service
	key := types.NamespacedName{Namespace: runNS(ns), Name: controller.WorkloadName("svc", rev)}
	if err := k8s.Get(context.Background(), key, &s); err != nil {
		t.Fatalf("no Service for the revision: %v", err)
	}
	if got := s.Spec.Selector[controller.LabelRevision]; got != rev {
		t.Errorf("selector revision is %q, want %q: a Service that does not pin the revision "+
			"selects every revision's Pods, so design 03's per-revision weights would all "+
			"resolve to one endpoint set", got, rev)
	}
	if got := s.Spec.Selector[controller.LabelAgent]; got != "svc" {
		t.Errorf("selector agent is %q, want svc", got)
	}
	if len(s.Spec.Ports) != 1 || s.Spec.Ports[0].Port != 8080 {
		t.Errorf("ports are %v, want one on 8080", s.Spec.Ports)
	}
	if got := s.Spec.Ports[0].TargetPort.StrVal; got != "a2a" {
		t.Errorf("targetPort is %q, want the named port a2a", got)
	}
	if got := s.Labels[controller.LabelAgentUID]; got != string(a.UID) {
		t.Errorf("agent-uid label is %q, want %q — it is the corroboration the GC "+
			"name authority relies on", got, a.UID)
	}
}

// The property the per-revision shape exists for. Two revisions coexist during
// a rollout by construction — design 03 shifts backendRefs weights between them
// — so the gated revision's Service must not be able to reach the candidate's
// Pods, or a candidate serves production traffic the moment it goes Ready.
func TestTwoRevisionsGetSeparateServicesThatCannotReachEachOther(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "two", nil)
	r := newReconciler(false)
	r1 := revision.MustHash(a.Spec)
	settle(t, r, a)
	markAvailable(t, ns, controller.WorkloadName("two", r1), 1)
	settle(t, r, a)

	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), a); err != nil {
		t.Fatalf("get: %v", err)
	}
	// A behaviour-surface edit, so this mints rather than converging in place.
	a.Spec.Runtime.Image = "ghcr.io/acme/agent@sha256:" +
		"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	if err := k8s.Update(context.Background(), a); err != nil {
		t.Fatalf("update: %v", err)
	}
	r2 := revision.MustHash(a.Spec)
	if r1 == r2 {
		t.Fatal("fixture: the edit did not mint a revision")
	}
	settle(t, r, a)

	var s1, s2 corev1.Service
	if err := k8s.Get(context.Background(), types.NamespacedName{
		Namespace: runNS(ns), Name: controller.WorkloadName("two", r1)}, &s1); err != nil {
		t.Fatalf("the gated revision lost its Service when a candidate appeared: %v", err)
	}
	if err := k8s.Get(context.Background(), types.NamespacedName{
		Namespace: runNS(ns), Name: controller.WorkloadName("two", r2)}, &s2); err != nil {
		t.Fatalf("the candidate has no Service: %v", err)
	}
	if s1.Name == s2.Name {
		t.Fatal("both revisions share one Service")
	}
	if s1.Spec.Selector[controller.LabelRevision] == s2.Spec.Selector[controller.LabelRevision] {
		t.Errorf("both Services select revision %q: the candidate's Pods are reachable "+
			"through the gated revision's address, so a candidate would serve production "+
			"traffic with no gate", s1.Spec.Selector[controller.LabelRevision])
	}
}

// A Service outlives nothing: it carries no ownerReference (the Agent is in
// another namespace), so if the operator does not collect it, nothing does.
func TestDeletingAnAgentCollectsItsServices(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "gone", nil)
	r := newReconciler(false)
	rev := revision.MustHash(a.Spec)
	settle(t, r, a)

	key := types.NamespacedName{Namespace: runNS(ns), Name: controller.WorkloadName("gone", rev)}
	var s corev1.Service
	if err := k8s.Get(context.Background(), key, &s); err != nil {
		t.Fatalf("setup: no Service: %v", err)
	}
	if err := k8s.Delete(context.Background(), a); err != nil {
		t.Fatalf("delete agent: %v", err)
	}
	// The finalizer holds the object, so it is still readable; one reconcile runs
	// the teardown. Re-reading rather than reusing `a` keeps the resourceVersion
	// current, which the finalizer removal needs.
	var live assaydv1alpha1.Agent
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &live); err != nil {
		t.Fatalf("get agent after delete: %v", err)
	}
	reconcileOnce(t, r, &live)

	err := k8s.Get(context.Background(), key, &s)
	if !apierrors.IsNotFound(err) {
		t.Errorf("the Service survived its Agent (err=%v): it holds a name a later "+
			"revision of the same Agent would legitimately want, and nothing else will "+
			"collect it", err)
	}
}

// The collision rule, applied to the Service ITSELF.
//
// The first version of this test edited the Agent to a colliding spec and
// checked the Service was not restamped. It passed, and a mutation proved it
// vacuous: ensureWorkload refuses the collision first, so ensureService's own
// check was never reached and deleting that check changed nothing. "It was
// refused" is not evidence about WHICH rule refused.
//
// So this replaces the Service under the operator with one carrying a
// different revision digest, and reconciles. The workload is untouched, so its
// check passes and this one has to do the work. Converging here would point the
// gated revision's route at a selector somebody else chose.
func TestAServiceCarryingAnotherRevisionDigestIsRefusedRatherThanConverged(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "presvc", nil)
	rev := revision.MustHash(a.Spec)
	name := controller.WorkloadName("presvc", rev)
	r := newReconciler(false)
	settle(t, r, a)

	key := types.NamespacedName{Namespace: runNS(ns), Name: name}
	var mine corev1.Service
	if err := k8s.Get(context.Background(), key, &mine); err != nil {
		t.Fatalf("setup: no Service: %v", err)
	}
	if err := k8s.Delete(context.Background(), &mine); err != nil {
		t.Fatalf("delete: %v", err)
	}
	// The same name and revision label, a different projection behind it.
	squatter := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: runNS(ns),
			Name:      name,
			Labels: map[string]string{
				controller.LabelAgent:    "presvc",
				controller.LabelRevision: rev,
				controller.LabelAgentUID: string(a.UID),
			},
			Annotations: map[string]string{
				controller.RevisionDigestAnnotation: "sha256:" + strings.Repeat("f", 64),
			},
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "not-the-agent"},
			Ports:    []corev1.ServicePort{{Name: "a2a", Port: 8080, Protocol: corev1.ProtocolTCP}},
		},
	}
	if err := k8s.Create(context.Background(), squatter); err != nil {
		t.Fatalf("create squatter: %v", err)
	}

	got := settle(t, r, a)

	// It refuses by REPORTING, not by erroring forever: a bare error would retry
	// with no status and leave an operator nothing to read.
	if !meta.IsStatusConditionTrue(got.Status.Conditions, string(assaydv1alpha1.CondRevisionHashCollision)) {
		t.Errorf("no RevisionHashCollision condition after a Service claimed the revision "+
			"name with another digest; conditions: %v", got.Status.Conditions)
	}

	var after corev1.Service
	if err := k8s.Get(context.Background(), key, &after); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got := after.Spec.Selector["app"]; got != "not-the-agent" {
		t.Errorf("the operator converged a Service carrying a different revision digest "+
			"(selector app=%q): two projections share one revision name, so this address "+
			"would resolve to whichever of them the operator wrote last", got)
	}
	if got := after.Annotations[controller.RevisionDigestAnnotation]; got != "sha256:"+strings.Repeat("f", 64) {
		t.Errorf("the operator restamped the digest to %q", got)
	}
}
