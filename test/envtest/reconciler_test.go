package envtest

import (
	"context"
	"fmt"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	plumev1alpha1 "github.com/Quinyte/plume/api/v1alpha1"
	"github.com/Quinyte/plume/internal/controller"
	"github.com/Quinyte/plume/internal/revision"
)

// The agent-operator, against a real API server.
//
// envtest runs etcd and kube-apiserver but NO kubelet: nothing schedules pods,
// so a Deployment never reports availableReplicas on its own. Tests therefore
// drive Deployment status explicitly. That is standard practice and it bounds
// what this suite can claim — it proves the operator's decisions given a
// workload state, never that a workload actually runs. The e2e suite on k3d is
// where the second claim belongs.

// reconcileOnce runs one pass synchronously. Driving the reconciler directly,
// rather than starting a manager and polling, makes each assertion about a
// known number of passes instead of about a race.
func reconcileOnce(t *testing.T, r *controller.AgentReconciler, a *plumev1alpha1.Agent) {
	t.Helper()
	_, err := r.Reconcile(context.Background(),
		ctrl.Request{NamespacedName: client.ObjectKeyFromObject(a)})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
}

// settle runs passes until the status stops changing, so a test can assert the
// converged state without hard-coding how many passes convergence takes.
func settle(t *testing.T, r *controller.AgentReconciler, a *plumev1alpha1.Agent) plumev1alpha1.Agent {
	t.Helper()
	var prev plumev1alpha1.Agent
	for i := 0; i < 10; i++ {
		reconcileOnce(t, r, a)
		var got plumev1alpha1.Agent
		if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &got); err != nil {
			t.Fatalf("get agent: %v", err)
		}
		if i > 0 && got.Status.Phase == prev.Status.Phase &&
			got.Status.ActiveRevision == prev.Status.ActiveRevision &&
			got.Status.CandidateRevision == prev.Status.CandidateRevision {
			return got
		}
		prev = got
	}
	t.Fatal("reconcile did not converge in 10 passes")
	return prev
}

func newReconciler(gatesInstalled bool) *controller.AgentReconciler {
	return &controller.AgentReconciler{
		Client:             k8s,
		Scheme:             scheme,
		EvalSuiteInstalled: func() bool { return gatesInstalled },
	}
}

func mustCreateAgent(t *testing.T, ns, name string, mutate func(*plumev1alpha1.Agent)) *plumev1alpha1.Agent {
	t.Helper()
	a := &plumev1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: plumev1alpha1.AgentSpec{
			Runtime: &plumev1alpha1.AgentRuntime{Image: "ghcr.io/acme/agent:1.0.0"},
		},
	}
	if mutate != nil {
		mutate(a)
	}
	if err := k8s.Create(context.Background(), a); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	return a
}

// markAvailable fakes what a kubelet would report.
func markAvailable(t *testing.T, ns, name string, n int32) {
	t.Helper()
	var d appsv1.Deployment
	key := types.NamespacedName{Namespace: ns, Name: name}
	if err := k8s.Get(context.Background(), key, &d); err != nil {
		t.Fatalf("get deployment %s: %v", name, err)
	}
	d.Status.Replicas = n
	d.Status.ReadyReplicas = n
	d.Status.AvailableReplicas = n
	d.Status.ObservedGeneration = d.Generation
	if err := k8s.Status().Update(context.Background(), &d); err != nil {
		t.Fatalf("set deployment status: %v", err)
	}
}

func condition(a *plumev1alpha1.Agent, t string) *metav1.Condition {
	for i := range a.Status.Conditions {
		if a.Status.Conditions[i].Type == t {
			return &a.Status.Conditions[i]
		}
	}
	return nil
}

func TestReconcileMaterializesTheRevisionWorkload(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "materialize", nil)
	rev := revision.Hash(a.Spec)

	reconcileOnce(t, newReconciler(false), a) // installs the finalizer
	reconcileOnce(t, newReconciler(false), a) // creates the workload

	var d appsv1.Deployment
	key := types.NamespacedName{Namespace: ns, Name: controller.WorkloadName("materialize", rev)}
	if err := k8s.Get(context.Background(), key, &d); err != nil {
		t.Fatalf("the workload for revision %s was not created: %v", rev, err)
	}

	if got := d.Spec.Template.Spec.Containers[0].Image; got != "ghcr.io/acme/agent:1.0.0" {
		t.Errorf("image is %q, want the agent's image", got)
	}
	if d.Labels[controller.LabelRevision] != rev {
		t.Errorf("workload is not labelled with its revision: %v", d.Labels)
	}

	// Design 02 §6: every agent pod is hardened, not only sandboxed ones — the
	// sandbox fallback path must be no weaker than the default path.
	sc := d.Spec.Template.Spec.Containers[0].SecurityContext
	switch {
	case sc == nil:
		t.Fatal("container has no securityContext")
	case sc.ReadOnlyRootFilesystem == nil || !*sc.ReadOnlyRootFilesystem:
		t.Error("rootfs is writable")
	case sc.RunAsNonRoot == nil || !*sc.RunAsNonRoot:
		t.Error("container may run as root")
	case sc.AllowPrivilegeEscalation == nil || *sc.AllowPrivilegeEscalation:
		t.Error("privilege escalation is allowed")
	}

	// The workload must be owned, or deleting the Agent would orphan it.
	if len(d.OwnerReferences) != 1 || d.OwnerReferences[0].Name != "materialize" {
		t.Errorf("workload is not owned by its Agent: %v", d.OwnerReferences)
	}
}

func TestPromotionRequiresAnAvailableWorkload(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "promote", nil)
	rev := revision.Hash(a.Spec)
	r := newReconciler(false)

	got := settle(t, r, a)
	if got.Status.Phase != plumev1alpha1.PhasePending {
		t.Errorf("phase is %q before the workload is available; want Pending", got.Status.Phase)
	}
	if got.Status.ActiveRevision != "" {
		t.Errorf("revision %s was promoted before it could serve", got.Status.ActiveRevision)
	}

	markAvailable(t, ns, controller.WorkloadName("promote", rev), 1)

	got = settle(t, r, a)
	if got.Status.Phase != plumev1alpha1.PhaseReady {
		t.Errorf("phase is %q after the workload became available; want Ready", got.Status.Phase)
	}
	if got.Status.ActiveRevision != rev {
		t.Errorf("activeRevision is %q, want %q", got.Status.ActiveRevision, rev)
	}
	if got.Status.CandidateRevision != "" {
		t.Errorf("candidateRevision %q survived promotion", got.Status.CandidateRevision)
	}
}

// Design 02 §3.3 core tier: with the EvalSuite CRD absent the gate requirement
// does not apply, but the rollout must say so loudly rather than appearing gated.
func TestUngatedRolloutIsLoudAboutIt(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "ungated", nil)
	r := newReconciler(false)
	settle(t, r, a)
	markAvailable(t, ns, controller.WorkloadName("ungated", revision.Hash(a.Spec)), 1)
	got := settle(t, r, a)

	c := condition(&got, plumev1alpha1.CondGatesSkipped)
	if c == nil || c.Status != metav1.ConditionTrue {
		t.Fatal("an ungated rollout must set GatesSkipped=True (NFR-8: never silent)")
	}
	// This agent declares no gates, so that is the precise cause: even with the
	// EvalSuite CRD installed, nothing would gate it. Naming the CRD instead would
	// report a cause that is true but not the operative one.
	if c.Reason != "NoGatesDeclared" {
		t.Errorf("reason is %q; with no spec.gates the operative cause is that none were "+
			"declared, not the state of the CRD", c.Reason)
	}
}

// The other ungated path: gates ARE declared, but the CRD that would enforce
// them is absent, so the core tier rule applies (design 02 §3.3).
func TestGatesDeclaredWithoutTheCRDIsLoudAboutIt(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "gatesnocrd", func(a *plumev1alpha1.Agent) {
		a.Spec.Gates = []plumev1alpha1.GateRef{{EvalSuiteRef: "pa-regression"}}
	})
	r := newReconciler(false) // gates declared, EvalSuite CRD absent
	settle(t, r, a)
	markAvailable(t, ns, controller.WorkloadName("gatesnocrd", revision.Hash(a.Spec)), 1)
	got := settle(t, r, a)

	c := condition(&got, plumev1alpha1.CondGatesSkipped)
	if c == nil || c.Status != metav1.ConditionTrue {
		t.Fatal("declared gates with no EvalSuite CRD must set GatesSkipped=True")
	}
	if c.Reason != "EvalSuiteCRDAbsent" {
		t.Errorf("reason is %q; here the operative cause IS the missing CRD, because gates "+
			"were declared and would otherwise have applied", c.Reason)
	}
	if got.Status.ActiveRevision == "" {
		t.Error("core tier: with no EvalSuite CRD the rollout proceeds (loudly) rather than " +
			"blocking on a controller that was never installed")
	}
}

// The fail-closed direction: gates declared, gate controller absent. The agent
// must hold at zero traffic rather than promote ungated.
func TestDeclaredGatesHoldWhenNoGateControllerExists(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "gated", func(a *plumev1alpha1.Agent) {
		a.Spec.Gates = []plumev1alpha1.GateRef{{EvalSuiteRef: "pa-regression"}}
	})
	r := newReconciler(true) // EvalSuite CRD present
	settle(t, r, a)
	markAvailable(t, ns, controller.WorkloadName("gated", revision.Hash(a.Spec)), 1)
	got := settle(t, r, a)

	if got.Status.Phase != plumev1alpha1.PhaseHeld {
		t.Errorf("phase is %q with unsatisfied gates; want Held — promoting would be an "+
			"ungated production change", got.Status.Phase)
	}
	if got.Status.ActiveRevision != "" {
		t.Errorf("revision %q was promoted through an unsatisfied gate", got.Status.ActiveRevision)
	}
	if c := condition(&got, plumev1alpha1.CondGatesPassed); c == nil || c.Status != metav1.ConditionFalse {
		t.Error("GatesPassed must be False and say why")
	}
}

// Design 02 §3.3: a new generation supersedes the in-flight candidate, and the
// abandoned revision is recorded so the transition is auditable, not silent.
func TestNewGenerationSupersedesTheInFlightCandidate(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "supersede", nil)
	first := revision.Hash(a.Spec)
	r := newReconciler(false)
	settle(t, r, a)

	// Change a behaviour-surface field mid-flight.
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), a); err != nil {
		t.Fatalf("get: %v", err)
	}
	a.Spec.Runtime.Image = "ghcr.io/acme/agent:2.0.0"
	if err := k8s.Update(context.Background(), a); err != nil {
		t.Fatalf("update: %v", err)
	}
	second := revision.Hash(a.Spec)
	if first == second {
		t.Fatal("fixture error: the image change did not mint a new revision")
	}

	got := settle(t, r, a)
	if !contains(got.Status.SupersededCandidates, first) {
		t.Errorf("superseded candidate %q was not recorded in %v — the transition was silent",
			first, got.Status.SupersededCandidates)
	}
	if got.Status.CandidateRevision == first {
		t.Error("the superseded revision is still the candidate")
	}
}

// Design 02 §3.3: retention is N revisions IN ADDITION TO active and any
// candidate. Counting them inside the limit would let a rollout GC its own
// rollback target.
func TestRetentionNeverCollectsTheRollbackTarget(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "retain", nil)
	r := newReconciler(false)

	var revs []string
	for i := 0; i < 5; i++ {
		if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), a); err != nil {
			t.Fatalf("get: %v", err)
		}
		a.Spec.Runtime.Image = fmt.Sprintf("ghcr.io/acme/agent:%d.0.0", i)
		if err := k8s.Update(context.Background(), a); err != nil {
			t.Fatalf("update: %v", err)
		}
		rev := revision.Hash(a.Spec)
		revs = append(revs, rev)
		settle(t, r, a)
		markAvailable(t, ns, controller.WorkloadName("retain", rev), 1)
		settle(t, r, a)
		// Distinct creation timestamps, so "oldest" is well defined. envtest
		// timestamps have one-second granularity.
		time.Sleep(1100 * time.Millisecond)
	}

	active := revs[len(revs)-1]
	var got plumev1alpha1.Agent
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status.ActiveRevision != active {
		t.Fatalf("fixture: active is %q, want %q", got.Status.ActiveRevision, active)
	}

	// The active revision and the two most recent others must survive.
	for _, rev := range []string{active, revs[len(revs)-2], revs[len(revs)-3]} {
		var d appsv1.Deployment
		key := types.NamespacedName{Namespace: ns, Name: controller.WorkloadName("retain", rev)}
		if err := k8s.Get(context.Background(), key, &d); err != nil {
			t.Errorf("revision %s was collected but is within the retention window "+
				"(active + %d): rollback to it is now impossible: %v",
				rev, controller.DefaultRevisionHistoryLimit, err)
		}
	}
	// The oldest must not.
	var d appsv1.Deployment
	key := types.NamespacedName{Namespace: ns, Name: controller.WorkloadName("retain", revs[0])}
	if err := k8s.Get(context.Background(), key, &d); !apierrors.IsNotFound(err) {
		t.Errorf("revision %s is outside the retention window but was not collected", revs[0])
	}
}

// Design 02 §3.2 / NFR-8: a sandbox that cannot be honoured downgrades LOUDLY.
func TestSandboxDowngradeIsNeverSilent(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "sandboxed", func(a *plumev1alpha1.Agent) {
		a.Spec.Runtime.Sandbox = &plumev1alpha1.SandboxSpec{Profile: "gvisor"}
	})
	got := settle(t, newReconciler(false), a)

	c := condition(&got, plumev1alpha1.CondSandboxDowngraded)
	if c == nil || c.Status != metav1.ConditionTrue {
		t.Fatal("a sandbox request that could not be honoured must set SandboxDowngraded=True")
	}
	if c.Message == "" || c.Reason == "" {
		t.Error("the downgrade must name the reason and the consequence")
	}
}

// Design 02 §3.2: replicas>1 needs the card to assert shared task state.
// Card fetch is unimplemented, so it must be reported unverified — not assumed.
func TestReplicasAboveOneReportsUnverifiedTaskState(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "scaled", func(a *plumev1alpha1.Agent) {
		a.Spec.Runtime.Replicas = 3
	})
	got := settle(t, newReconciler(false), a)

	if c := condition(&got, plumev1alpha1.CondTaskStateUnverified); c == nil || c.Status != metav1.ConditionTrue {
		t.Error("replicas>1 without a verified card assertion must set TaskStateUnverified=True")
	}
}

// Reconcile runs many times over an unchanged object. It must converge, not
// churn: a controller that rewrites status every pass fights every other writer.
func TestReconcileIsIdempotent(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "idempotent", nil)
	r := newReconciler(false)
	settle(t, r, a)
	markAvailable(t, ns, controller.WorkloadName("idempotent", revision.Hash(a.Spec)), 1)
	settle(t, r, a)

	// Count writes rather than compare resourceVersions: the API server does not
	// bump the version on a no-op update, so a controller that writes status every
	// pass looks identical to one that does not.
	counter := &countingClient{Client: k8s}
	counting := &controller.AgentReconciler{
		Client: counter, Scheme: scheme, EvalSuiteInstalled: func() bool { return false },
	}
	for i := 0; i < 5; i++ {
		reconcileOnce(t, counting, a)
	}
	if n := counter.count(); n != 0 {
		t.Errorf("five reconciles of a converged agent issued %d writes; want 0. "+
			"An operator that rewrites unchanged status churns the API server and fights "+
			"every other writer of the object.", n)
	}

	// Exactly one workload: content-addressed revisions must never double-create.
	var list appsv1.DeploymentList
	if err := k8s.List(context.Background(), &list, client.InNamespace(ns)); err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list.Items) != 1 {
		t.Errorf("got %d workloads for one revision; want 1", len(list.Items))
	}
}

func TestFinalizerIsInstalledAndReleased(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "finalized", nil)
	r := newReconciler(false)
	settle(t, r, a)

	var got plumev1alpha1.Agent
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if !contains(got.Finalizers, controller.Finalizer) {
		t.Fatalf("finalizer was not installed: %v — teardown could not be ordered", got.Finalizers)
	}

	if err := k8s.Delete(context.Background(), &got); err != nil {
		t.Fatalf("delete: %v", err)
	}
	reconcileOnce(t, r, &got)

	err := k8s.Get(context.Background(), client.ObjectKeyFromObject(&got), &got)
	if !apierrors.IsNotFound(err) {
		t.Errorf("the agent still exists after finalization: the finalizer was not released (%v)", err)
	}
}

// An external agent has no workload to materialize. It must not be reported
// Ready by an operator that has not actually wired anything up.
func TestExternalAgentIsHeldNotFakedReady(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "external", func(a *plumev1alpha1.Agent) {
		a.Spec.Runtime = nil
		a.Spec.External = &plumev1alpha1.ExternalAgent{Endpoint: "https://agent.example.com"}
	})
	got := settle(t, newReconciler(false), a)

	if got.Status.Phase == plumev1alpha1.PhaseReady {
		t.Error("an external agent was reported Ready though nothing registered it")
	}
	var list appsv1.DeploymentList
	if err := k8s.List(context.Background(), &list, client.InNamespace(ns)); err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list.Items) != 0 {
		t.Errorf("an external agent materialized %d workloads; it runs elsewhere", len(list.Items))
	}
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
