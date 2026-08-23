// Package e2e runs against a real cluster (k3d), with a real kubelet.
//
// This is the layer envtest cannot reach. envtest runs etcd and kube-apiserver
// but schedules nothing, so every envtest assertion about availability is made
// against a Deployment status the test itself wrote. Here a pod is actually
// scheduled, pulled and started — so "the workload runs" is a claim this suite
// can make and the envtest suite cannot.
package e2e

import (
	"context"
	"os"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	plumev1alpha1 "github.com/ejs-5/plume/api/v1alpha1"
	"github.com/ejs-5/plume/internal/controller"
	"github.com/ejs-5/plume/internal/revision"
)

var (
	k8s    client.Client
	notRun bool
)

func TestMain(m *testing.M) {
	// Opt-in, deliberately. This suite CREATES objects in whatever cluster the
	// current context names — on a laptop pointed at a shared cluster that is not
	// a test, it is a write. `make e2e` sets this after creating a k3d cluster;
	// a bare `go test ./...` skips rather than writing somewhere real.
	if os.Getenv("PLUME_E2E") != "1" {
		// Exit 0 would print "ok" for a suite that ran nothing, which is one env
		// var away from silently green. Registering a failing placeholder instead
		// keeps `go test ./...` honest about what did not run, while still not
		// writing to whatever cluster the current context happens to name.
		notRun = true
		os.Exit(m.Run())
	}

	// Past the opt-in, a skipped e2e is an untested feature: fail loudly.
	cfg, err := config.GetConfig()
	if err != nil {
		println("e2e: no kubeconfig:", err.Error())
		os.Exit(1)
	}
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		println("e2e: scheme:", err.Error())
		os.Exit(1)
	}
	if err := apiextensionsv1.AddToScheme(scheme); err != nil {
		println("e2e: scheme:", err.Error())
		os.Exit(1)
	}
	if err := plumev1alpha1.AddToScheme(scheme); err != nil {
		println("e2e: scheme:", err.Error())
		os.Exit(1)
	}
	k8s, err = client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		println("e2e: client:", err.Error())
		os.Exit(1)
	}

	// Verify plume is installed BEFORE creating anything. Without this, a cluster
	// that simply lacks the CRD reports "the CRD did not accept a minimal Agent"
	// — an environment problem misattributed as a product defect.
	var crd apiextensionsv1.CustomResourceDefinition
	if err := k8s.Get(context.Background(),
		types.NamespacedName{Name: "agents.plume.dev"}, &crd); err != nil {
		println("e2e: the agents.plume.dev CRD is not installed on the current context — " +
			"run `make install-crds` or `make e2e`. Refusing to create objects in a " +
			"cluster that is not a plume cluster.")
		os.Exit(1)
	}

	os.Exit(m.Run())
}

// The CRD is installed and accepts the minimal Agent. This is deliberately the
// smallest possible real-cluster assertion: it proves the manifests apply to a
// distribution plume actually targets, which nothing else in the suite does.
func TestCRDInstallsAndAcceptsTheMinimalAgent(t *testing.T) {
	requireCluster(t)
	ctx := context.Background()
	ensureNamespace(t, ctx, "plume-e2e")

	a := &plumev1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{Name: "smoke", Namespace: "plume-e2e"},
		Spec: plumev1alpha1.AgentSpec{
			Runtime: &plumev1alpha1.AgentRuntime{Image: "ghcr.io/acme/agent:1.0.0"},
		},
	}
	_ = k8s.Delete(ctx, a)
	if err := k8s.Create(ctx, a); err != nil {
		t.Fatalf("the CRD did not accept a minimal Agent on a real cluster: %v", err)
	}
	t.Cleanup(func() { _ = k8s.Delete(context.Background(), a) })

	// Defaulting is the API server's job, so it must hold here too.
	var got plumev1alpha1.Agent
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(a), &got); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got.Spec.Runtime.Replicas != 1 || got.Spec.Runtime.Port != 8080 {
		t.Errorf("defaults did not apply on a real cluster: replicas=%d port=%d",
			got.Spec.Runtime.Replicas, got.Spec.Runtime.Port)
	}
}

// The claim envtest cannot make: a plume-created pod actually runs.
//
// envtest has no kubelet, so every availability assertion there is made against
// a Deployment status the test itself wrote. Here a real scheduler places a real
// pod and a real kubelet pulls a real image. This is the only test in the repo
// that can fail because the thing does not work, as opposed to because the
// operator decided wrongly.
//
// Requires the operator deployed (make e2e installs the chart).
func TestWorkloadActuallyRuns(t *testing.T) {
	requireCluster(t)
	requireOperator(t)
	ctx := context.Background()
	ensureNamespace(t, ctx, "plume-e2e")

	a := &plumev1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{Name: "runs", Namespace: "plume-e2e"},
		Spec: plumev1alpha1.AgentSpec{
			Runtime: &plumev1alpha1.AgentRuntime{
				// A real image that starts, serves a port and stays up. The agent
				// contract (an A2A card) is not exercised here — card fetch is
				// unimplemented — so this asserts the workload story only.
				Image: "registry.k8s.io/pause:3.10",
			},
		},
	}
	_ = k8s.Delete(ctx, a)
	if err := k8s.Create(ctx, a); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	t.Cleanup(func() { _ = k8s.Delete(context.Background(), a) })

	rev := revision.Hash(a.Spec)
	name := controller.WorkloadName("runs", rev)

	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		var d appsv1.Deployment
		if err := k8s.Get(ctx, types.NamespacedName{Namespace: "plume-e2e", Name: name}, &d); err == nil {
			if d.Status.AvailableReplicas > 0 {
				return
			}
		}
		time.Sleep(3 * time.Second)
	}

	// Say what went wrong, not just that it did.
	var d appsv1.Deployment
	if err := k8s.Get(ctx, types.NamespacedName{Namespace: "plume-e2e", Name: name}, &d); err != nil {
		t.Fatalf("the operator never created a workload for revision %s: %v", rev, err)
	}
	t.Fatalf("workload %s never became available: %d/%d replicas, conditions %+v",
		name, d.Status.AvailableReplicas, d.Status.Replicas, d.Status.Conditions)
}

// The operator's own PodSpec must survive a real cluster's admission defaulting
// without churning. envtest has no ServiceAccount admission plugin, so the
// mutation that removes ServiceAccountName from deploymentFor survives there —
// this is where that gap closes.
func TestOperatorDoesNotChurnAgainstRealAdmission(t *testing.T) {
	requireCluster(t)
	requireOperator(t)
	ctx := context.Background()
	ensureNamespace(t, ctx, "plume-e2e")

	a := &plumev1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{Name: "nochurn", Namespace: "plume-e2e"},
		Spec: plumev1alpha1.AgentSpec{
			Runtime: &plumev1alpha1.AgentRuntime{Image: "registry.k8s.io/pause:3.10"},
		},
	}
	_ = k8s.Delete(ctx, a)
	if err := k8s.Create(ctx, a); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	t.Cleanup(func() { _ = k8s.Delete(context.Background(), a) })

	name := controller.WorkloadName("nochurn", revision.Hash(a.Spec))
	key := types.NamespacedName{Namespace: "plume-e2e", Name: name}

	var first appsv1.Deployment
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		if err := k8s.Get(ctx, key, &first); err == nil {
			break
		}
		time.Sleep(2 * time.Second)
	}
	if first.Name == "" {
		t.Fatalf("the operator never created %s", name)
	}

	// Let several reconciles happen. A ServiceAccount admission plugin defaulting
	// a field the operator compares but does not set would show up as a
	// monotonically climbing generation.
	time.Sleep(30 * time.Second)

	var later appsv1.Deployment
	if err := k8s.Get(ctx, key, &later); err != nil {
		t.Fatalf("get: %v", err)
	}
	if later.Generation != first.Generation {
		t.Errorf("the workload's generation moved from %d to %d with no spec change. The "+
			"operator is rewriting it every reconcile, which means it compares a field a "+
			"real cluster defaults and it does not set — the exact failure envtest cannot "+
			"see, because envtest runs no ServiceAccount admission plugin.",
			first.Generation, later.Generation)
	}
}

// requireOperator fails when the chart is not installed, rather than letting a
// workload test time out and blame the operator's logic for its absence.
func requireOperator(t *testing.T) {
	t.Helper()
	var d appsv1.Deployment
	err := k8s.Get(context.Background(),
		types.NamespacedName{Namespace: "plume-system", Name: "plume-agent-operator"}, &d)
	if err != nil {
		t.Fatalf("the agent-operator is not installed: %v. Run `make e2e`, which helm-installs "+
			"the chart. Without it these tests would time out and read as operator bugs.", err)
	}
	if d.Status.AvailableReplicas == 0 {
		t.Fatalf("the agent-operator is installed but has no available replicas. If this "+
			"persists, check its readiness probe: readiness means leader election completed, "+
			"so a missing coordination.k8s.io/leases grant shows up here. Conditions: %+v",
			d.Status.Conditions)
	}
}

func ensureNamespace(t *testing.T, ctx context.Context, name string) {
	t.Helper()
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
	if err := k8s.Create(ctx, ns); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create namespace %s: %v", name, err)
	}
}

// TestE2EWasNotRun fails loudly when the suite was skipped, so a green `go test
// ./...` cannot be mistaken for e2e coverage. `make e2e` sets PLUME_E2E=1 and
// this never runs.
func TestE2EWasNotRun(t *testing.T) {
	if !notRun {
		t.Skip("the suite ran")
	}
	t.Fatal("e2e did not run: set PLUME_E2E=1, or use `make e2e`, which creates a " +
		"disposable k3d cluster first. This suite writes to the current kube context, " +
		"so it does not opt itself in.")
}

// requireCluster skips a test that needs a live cluster when the suite was not
// opted in. TestE2EWasNotRun is what makes that visible rather than silent.
func requireCluster(t *testing.T) {
	t.Helper()
	if notRun {
		t.Skip("no cluster: see TestE2EWasNotRun")
	}
}

// The ServiceAccount admission plugin runs on a real cluster and not in
// envtest, so it defaults spec.serviceAccountName to "default" on every pod
// template. The operator sets that field explicitly for exactly this reason: if
// it did not, a real cluster would default it, read-back would differ from what
// was rendered, and the operator would rewrite the Deployment on every single
// reconcile forever.
//
// This assertion cannot be made in envtest — the mutation that removes the
// field survives there — which is why it lives here.
func TestServiceAccountDefaultingDoesNotCauseChurn(t *testing.T) {
	requireCluster(t)
	t.Skip("pending: needs the operator deployed to the cluster (cmd/operator + chart, " +
		"design 07). The assertion is written so the gap is visible in the run output " +
		"rather than absent: envtest cannot cover ServiceAccount admission, so nothing " +
		"currently proves the operator does not churn against a real API server.")
}
