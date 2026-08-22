package envtest

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/yaml"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	plumev1alpha1 "github.com/ejs-5/plume/api/v1alpha1"
	"github.com/ejs-5/plume/internal/controller"
	"github.com/ejs-5/plume/internal/revision"
)

// Regression tests for the four blockers the independent review found. Each is
// written to fail against the code as reviewed, so the fix is demonstrated
// rather than asserted.

// B1 — the safety hook must not fail open.
//
// EvalSuiteInstalled was a nil-tolerant hook returning false, which made
// gatesSatisfied return true: an unwired reconciler promoted everything through
// declared gates, and set GatesSkipped with reason EvalSuiteCRDAbsent — a check
// it never performed. cmd/ is empty, so the first person to wire the manager
// would have decided ADR-0006's fate by whether they remembered one field.
func TestUnwiredReconcilerHoldsRatherThanPromotingUngated(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "unwired", func(a *plumev1alpha1.Agent) {
		a.Spec.Gates = []plumev1alpha1.GateRef{{EvalSuiteRef: "pa-regression"}}
	})

	// Constructed exactly as an unwired cmd/ would leave it.
	r := &controller.AgentReconciler{Client: k8s, Scheme: scheme}

	settle(t, r, a)
	markAvailable(t, ns, controller.WorkloadName("unwired", revision.Hash(a.Spec)), 1)
	got := settle(t, r, a)

	if got.Status.ActiveRevision != "" {
		t.Errorf("an unwired reconciler promoted revision %q through a declared gate. "+
			"Holding is recoverable; ungated promotion is not, so the unset default must hold.",
			got.Status.ActiveRevision)
	}
	if c := condition(&got, plumev1alpha1.CondGatesSkipped); c != nil && c.Status == metav1.ConditionTrue {
		t.Errorf("GatesSkipped=True with reason %q, but no CRD check was performed — "+
			"NFR-8 requires the degraded path be loud AND true", c.Reason)
	}
	// The condition must name the real cause. Asserting only the absence of
	// GatesSkipped would accept any other wrong answer.
	c := condition(&got, plumev1alpha1.CondGatesPassed)
	if c == nil || c.Status != metav1.ConditionFalse {
		t.Fatal("an unwired operator must report GatesPassed=False")
	}
	if c.Reason != "GateDetectionUnwired" {
		t.Errorf("reason is %q; it must say the operator cannot tell whether gating applies, "+
			"not invent a cause it never checked", c.Reason)
	}
}

// B2 + M6 — policy-surface fields are applied in place, and out-of-band drift is
// corrected.
//
// A12 puts runtime.resources and runtime.port on the policy surface, meaning
// "applied in place". ensureWorkload synced only replicas, so raising memory to
// stop OOM kills was a silent no-op on a green agent. The same gap let anyone
// with deployments/update rewrite the image of a gated revision: the gate passed
// on X, the pods ran Y, and the CR asserted X.
func TestPolicySurfaceEditsReachTheWorkload(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "inplace", func(a *plumev1alpha1.Agent) {
		a.Spec.Runtime.Resources = corev1.ResourceRequirements{
			Requests: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("64Mi")},
		}
	})
	r := newReconciler(false)
	rev := revision.Hash(a.Spec)
	settle(t, r, a)
	markAvailable(t, ns, controller.WorkloadName("inplace", rev), 1)
	settle(t, r, a)

	// Raise memory. This must NOT mint a revision (A12) and must reach the pods.
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), a); err != nil {
		t.Fatalf("get: %v", err)
	}
	a.Spec.Runtime.Resources.Requests[corev1.ResourceMemory] = resource.MustParse("512Mi")
	if err := k8s.Update(context.Background(), a); err != nil {
		t.Fatalf("update: %v", err)
	}
	if revision.Hash(a.Spec) != rev {
		t.Fatal("fixture: a resources edit minted a revision; A12 says it is policy-surface")
	}
	settle(t, r, a)

	var d appsv1.Deployment
	key := types.NamespacedName{Namespace: ns, Name: controller.WorkloadName("inplace", rev)}
	if err := k8s.Get(context.Background(), key, &d); err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	got := d.Spec.Template.Spec.Containers[0].Resources.Requests[corev1.ResourceMemory]
	if got.String() != "512Mi" {
		t.Errorf("memory request is %s after an in-place edit to 512Mi. An operator "+
			"raising memory to stop OOM kills got a green agent and no change.", got.String())
	}
}

func TestOutOfBandDriftIsCorrected(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "drift", nil)
	r := newReconciler(false)
	rev := revision.Hash(a.Spec)
	settle(t, r, a)
	markAvailable(t, ns, controller.WorkloadName("drift", rev), 1)
	settle(t, r, a)

	// Anyone with deployments/update in the namespace.
	var d appsv1.Deployment
	key := types.NamespacedName{Namespace: ns, Name: controller.WorkloadName("drift", rev)}
	if err := k8s.Get(context.Background(), key, &d); err != nil {
		t.Fatalf("get: %v", err)
	}
	d.Spec.Template.Spec.Containers[0].Image = "ghcr.io/attacker/evil:latest"
	d.Spec.Template.Spec.Containers[0].SecurityContext.ReadOnlyRootFilesystem = boolPtr(false)
	if err := k8s.Update(context.Background(), &d); err != nil {
		t.Fatalf("tamper: %v", err)
	}

	settle(t, r, a)

	if err := k8s.Get(context.Background(), key, &d); err != nil {
		t.Fatalf("get: %v", err)
	}
	if img := d.Spec.Template.Spec.Containers[0].Image; img != "ghcr.io/acme/agent:1.0.0" {
		t.Errorf("image is still %q after reconcile. The gate passed on one revision and "+
			"the pods run another, while the CR asserts the first — ADR-0006 bypassed by "+
			"anyone with deployments/update.", img)
	}
	if ro := d.Spec.Template.Spec.Containers[0].SecurityContext.ReadOnlyRootFilesystem; ro == nil || !*ro {
		t.Error("hardening was not restored: it is a create-time decoration, not an invariant")
	}
}

// B3 — a spec-less Agent must be rejected at admission, not dereferenced.
//
// spec was json:"spec,omitempty" with no required, and the CEL rule is scoped to
// spec, so it never evaluated on an object with no spec key at all.
func TestSpecIsRequired(t *testing.T) {
	ns := newNamespace(t)
	doc := `
apiVersion: plume.dev/v1alpha1
kind: Agent
metadata: {name: nospec}
`
	var obj unstructured.Unstructured
	if err := yaml.Unmarshal([]byte(doc), &obj.Object); err != nil {
		t.Fatalf("parse: %v", err)
	}
	obj.SetNamespace(ns)
	if err := k8s.Create(context.Background(), &obj); err == nil {
		t.Fatal("an Agent with no spec was admitted; the reconciler then dereferences nil " +
			"and any namespace user can wedge it in a hot backoff loop")
	}
}

// B4 — the collector must not delete Deployments it does not own.
//
// ownedWorkloads selected on a label alone. A stray plume.dev/agent label —
// copied from an example, applied by a Kustomize commonLabels, or set by anyone
// with deployment-create — turned this operator into a deleter of other
// people's workloads.
func TestGarbageCollectorIgnoresUnownedWorkloads(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "gcprobe", nil)
	r := newReconciler(false)
	settle(t, r, a)

	// Four unrelated Deployments carrying only the agent label.
	var names []string
	for i := 0; i < 4; i++ {
		name := "bystander-" + string(rune('a'+i))
		names = append(names, name)
		d := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name: name, Namespace: ns,
				Labels: map[string]string{controller.LabelAgent: "gcprobe"},
			},
			Spec: appsv1.DeploymentSpec{
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": name}},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": name}},
					Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "c", Image: "busybox"}}},
				},
			},
		}
		if err := k8s.Create(context.Background(), d); err != nil {
			t.Fatalf("create bystander: %v", err)
		}
	}

	settle(t, r, a)

	for _, name := range names {
		var d appsv1.Deployment
		err := k8s.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: name}, &d)
		if err != nil {
			t.Errorf("the operator deleted %s, a Deployment it does not own — a stray label "+
				"must not make this a deleter of other people's workloads: %v", name, err)
		}
	}
}

// The ownership guard alone, isolated: bystanders that carry BOTH plume labels,
// including a plausible revision hash, and differ only in having no controller
// reference. Without the UID check these are indistinguishable from real
// revisions and fall straight into the retention window.
func TestOwnershipIsDecidedByControllerRefNotLabels(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "ownercheck", nil)
	r := newReconciler(false)
	settle(t, r, a)

	var names []string
	for i := 0; i < 4; i++ {
		name := "impostor-" + string(rune('a'+i))
		names = append(names, name)
		labels := map[string]string{
			controller.LabelAgent:    "ownercheck",
			controller.LabelRevision: "deadbeef0" + string(rune('0'+i)),
		}
		d := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Labels: labels},
			Spec: appsv1.DeploymentSpec{
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": name}},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": name}},
					Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "c", Image: "busybox"}}},
				},
			},
		}
		if err := k8s.Create(context.Background(), d); err != nil {
			t.Fatalf("create impostor: %v", err)
		}
	}

	settle(t, r, a)

	for _, name := range names {
		var d appsv1.Deployment
		if err := k8s.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: name}, &d); err != nil {
			t.Errorf("the operator deleted %s: it carries this agent's labels and a revision "+
				"label but no controller reference, so ownership must be decided by the "+
				"reference and its UID, which nobody can forge by labelling: %v", name, err)
		}
	}
}

// The revision-label guard alone, isolated: a workload this Agent genuinely owns
// but which carries no revision label cannot be placed in the retention window,
// so it must never be a GC candidate.
func TestOwnedWorkloadWithoutARevisionLabelIsNotCollected(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "unlabelled", nil)
	r := newReconciler(false)
	settle(t, r, a)

	var agent plumev1alpha1.Agent
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &agent); err != nil {
		t.Fatalf("get: %v", err)
	}

	var names []string
	for i := 0; i < 4; i++ {
		name := "unrevisioned-" + string(rune('a'+i))
		names = append(names, name)
		d := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name: name, Namespace: ns,
				Labels: map[string]string{controller.LabelAgent: "unlabelled"},
			},
			Spec: appsv1.DeploymentSpec{
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": name}},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": name}},
					Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "c", Image: "busybox"}}},
				},
			},
		}
		// Genuinely owned, so only the missing revision label can save it.
		if err := ctrl.SetControllerReference(&agent, d, scheme); err != nil {
			t.Fatalf("set owner: %v", err)
		}
		if err := k8s.Create(context.Background(), d); err != nil {
			t.Fatalf("create: %v", err)
		}
	}

	settle(t, r, a)

	for _, name := range names {
		var d appsv1.Deployment
		if err := k8s.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: name}, &d); err != nil {
			t.Errorf("the operator collected %s, which it owns but which carries no revision "+
				"label — it cannot be placed in the retention window, so it must not be a "+
				"GC candidate: %v", name, err)
		}
	}
}

// m18: normal-true conditions must stay in the list once set. Dropping
// GatesPassed when an EvalSuite CRD is uninstalled would erase the record that a
// revision ever passed a gate.
func TestNormalTrueConditionsAreSticky(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "sticky", func(a *plumev1alpha1.Agent) {
		a.Spec.Gates = []plumev1alpha1.GateRef{{EvalSuiteRef: "s"}}
	})

	// With the CRD present and gates declared, GatesPassed is asserted False.
	settle(t, newReconciler(true), a)
	var got plumev1alpha1.Agent
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if condition(&got, plumev1alpha1.CondGatesPassed) == nil {
		t.Fatal("fixture: GatesPassed was never set")
	}

	// Now the CRD goes away: this pass asserts GatesSkipped instead and never
	// speaks to GatesPassed.
	settle(t, newReconciler(false), a)
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if condition(&got, plumev1alpha1.CondGatesPassed) == nil {
		t.Error("GatesPassed was dropped when a later pass stopped asserting it. Its absence " +
			"is indistinguishable from never-evaluated, so the record that this revision was " +
			"gated is gone.")
	}
}

// M5 — a routine spec edit on a serving agent must not report it unhealthy, and
// must not claim a canary that is not shifting any traffic.
func TestSpecEditOnAServingAgentStaysReady(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "rolling", nil)
	r := newReconciler(false)
	first := revision.Hash(a.Spec)
	settle(t, r, a)
	markAvailable(t, ns, controller.WorkloadName("rolling", first), 1)
	settle(t, r, a)

	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), a); err != nil {
		t.Fatalf("get: %v", err)
	}
	a.Spec.Runtime.Image = "ghcr.io/acme/agent:2.0.0"
	if err := k8s.Update(context.Background(), a); err != nil {
		t.Fatalf("update: %v", err)
	}
	got := settle(t, r, a)

	// The old revision still holds all traffic and is healthy.
	if got.Status.Phase == plumev1alpha1.PhaseCanary {
		t.Error("phase is Canary while zero traffic is shifting: §3.3 fixes Canary as " +
			"\"weights shift\", and there is no gateway yet")
	}
	if c := condition(&got, plumev1alpha1.CondReady); c == nil || c.Status != metav1.ConditionTrue {
		t.Error("Ready=False on an agent that is serving normally: every routine spec edit " +
			"would trip any alert keyed on the canonical condition")
	}
	if c := condition(&got, plumev1alpha1.CondProgressing); c == nil || c.Status != metav1.ConditionTrue {
		t.Error("a rollout in flight must be visible as Progressing=True (A13)")
	}
	if got.Status.ActiveRevision != first {
		t.Errorf("activeRevision moved to %q before the new revision could serve", got.Status.ActiveRevision)
	}
}

func boolPtr(b bool) *bool { return &b }

var _ = ctrl.Request{}
