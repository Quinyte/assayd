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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	plumev1alpha1 "github.com/ejs-5/plume/api/v1alpha1"
)

var k8s client.Client

func TestMain(m *testing.M) {
	// A skipped e2e is an untested feature, so this fails rather than skips when
	// no cluster is reachable — the same rule hack/e2e.sh states.
	cfg, err := config.GetConfig()
	if err != nil {
		println("e2e: no kubeconfig — run `make e2e`, which creates a k3d cluster:", err.Error())
		os.Exit(1)
	}
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
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
	os.Exit(m.Run())
}

// The CRD is installed and accepts the minimal Agent. This is deliberately the
// smallest possible real-cluster assertion: it proves the manifests apply to a
// distribution plume actually targets, which nothing else in the suite does.
func TestCRDInstallsAndAcceptsTheMinimalAgent(t *testing.T) {
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

// Placeholder for the claim this suite exists to make once the operator is
// deployed here rather than driven in-process: an Agent produces a Deployment
// whose pods actually reach Ready. Written as a skip with a stated reason rather
// than omitted, so the gap is visible in the run output.
func TestWorkloadActuallyRuns(t *testing.T) {
	t.Skip("pending: needs the operator deployed to the cluster (cmd/operator + chart, design 07). " +
		"Until then no suite proves a plume-created pod reaches Ready — envtest cannot, " +
		"because it has no kubelet.")

	ctx := context.Background()
	var d appsv1.Deployment
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		if err := k8s.Get(ctx, types.NamespacedName{Namespace: "plume-e2e", Name: "smoke"}, &d); err == nil &&
			d.Status.AvailableReplicas > 0 {
			return
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatal("no available replicas within the deadline")
}

func ensureNamespace(t *testing.T, ctx context.Context, name string) {
	t.Helper()
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
	if err := k8s.Create(ctx, ns); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create namespace %s: %v", name, err)
	}
}
