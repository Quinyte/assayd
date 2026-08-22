package envtest

import (
	"context"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlconfig "sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	plumev1alpha1 "github.com/ejs-5/plume/api/v1alpha1"
	"github.com/ejs-5/plume/internal/controller"
	"github.com/ejs-5/plume/internal/revision"
)

// Every other envtest here drives Reconcile directly, which is right for
// determinism but hides three things: the watch and Owns wiring, whether the
// requeue after adding the finalizer actually re-triggers, and how the loop
// behaves when the API server is answering a real controller rather than a
// synchronous call.
//
// This test runs an actual manager. It is the only place SetupWithManager is
// exercised at all.

func startManager(t *testing.T) manager.Manager {
	t.Helper()
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme,
		// No metrics listener and no leader election: this is one process in a
		// test, and binding ports would make the suite flaky in CI.
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
		// Each test starts its own manager, so the controller name repeats. The
		// uniqueness check exists to stop two controllers reporting one metric,
		// which is a production concern rather than a test one.
		Controller: ctrlconfig.Controller{SkipNameValidation: ptrTo(true)},
	})
	if err != nil {
		t.Fatalf("build manager: %v", err)
	}

	r, err := controller.NewAgentReconciler(mgr.GetClient(), mgr.GetScheme(), func() bool { return false })
	if err != nil {
		t.Fatalf("build reconciler: %v", err)
	}
	if err := r.SetupWithManager(mgr); err != nil {
		t.Fatalf("SetupWithManager: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- mgr.Start(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			// mgr.Start returns nil on graceful cancellation, so any error here is
			// real. The previous `&& ctx.Err() == nil` guard could never fire.
			if err != nil {
				t.Errorf("manager exited with error: %v", err)
			}
		case <-time.After(30 * time.Second):
			t.Error("manager did not shut down within 30s")
		}
	})

	if !mgr.GetCache().WaitForCacheSync(ctx) {
		t.Fatal("cache never synced")
	}
	return mgr
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// The whole wiring, end to end through a manager: create an Agent, and the
// finalizer, the workload and the promotion must all happen without anyone
// calling Reconcile.
func TestManagerReconcilesAnAgentEndToEnd(t *testing.T) {
	startManager(t)
	ns := newNamespace(t)
	ctx := context.Background()

	a := &plumev1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{Name: "managed", Namespace: ns},
		Spec: plumev1alpha1.AgentSpec{
			Runtime: &plumev1alpha1.AgentRuntime{Image: "ghcr.io/acme/agent:1.0.0"},
		},
	}
	if err := k8s.Create(ctx, a); err != nil {
		t.Fatalf("create: %v", err)
	}
	rev := revision.Hash(a.Spec)

	// The finalizer is added on the first pass and the reconciler returns
	// Requeue rather than relying on its own write producing a watch event. If
	// that requeue were dropped, this would hang — which is the point.
	eventually(t, "the finalizer to be installed", func() bool {
		var got plumev1alpha1.Agent
		if err := k8s.Get(ctx, client.ObjectKeyFromObject(a), &got); err != nil {
			return false
		}
		return contains(got.Finalizers, controller.Finalizer)
	})

	eventually(t, "the workload to be created", func() bool {
		var d appsv1.Deployment
		key := types.NamespacedName{Namespace: ns, Name: controller.WorkloadName("managed", rev)}
		return k8s.Get(ctx, key, &d) == nil
	})

	// envtest has no kubelet, so availability is ours to declare.
	markAvailable(t, ns, controller.WorkloadName("managed", rev), 1)

	eventually(t, "the revision to promote", func() bool {
		var got plumev1alpha1.Agent
		if err := k8s.Get(ctx, client.ObjectKeyFromObject(a), &got); err != nil {
			return false
		}
		return got.Status.ActiveRevision == rev && got.Status.Phase == plumev1alpha1.PhaseReady
	})
}

// The Owns wiring: a change to a workload the operator owns must re-trigger
// reconcile of its Agent, with nobody touching the Agent itself. Without it,
// out-of-band drift would sit uncorrected until something else happened to
// enqueue the object.
func TestManagerWatchesOwnedWorkloads(t *testing.T) {
	startManager(t)
	ns := newNamespace(t)
	ctx := context.Background()

	a := &plumev1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{Name: "watched", Namespace: ns},
		Spec: plumev1alpha1.AgentSpec{
			Runtime: &plumev1alpha1.AgentRuntime{Image: "ghcr.io/acme/agent:1.0.0"},
		},
	}
	if err := k8s.Create(ctx, a); err != nil {
		t.Fatalf("create: %v", err)
	}
	rev := revision.Hash(a.Spec)
	key := types.NamespacedName{Namespace: ns, Name: controller.WorkloadName("watched", rev)}

	eventually(t, "the workload to exist", func() bool {
		var d appsv1.Deployment
		return k8s.Get(ctx, key, &d) == nil
	})

	// Tamper. Nothing touches the Agent — only the Deployment changes.
	var d appsv1.Deployment
	if err := k8s.Get(ctx, key, &d); err != nil {
		t.Fatalf("get: %v", err)
	}
	d.Spec.Template.Spec.Containers[0].Image = "ghcr.io/attacker/evil:latest"
	if err := k8s.Update(ctx, &d); err != nil {
		t.Fatalf("tamper: %v", err)
	}

	eventually(t, "drift to be corrected via the Owns watch", func() bool {
		var got appsv1.Deployment
		if err := k8s.Get(ctx, key, &got); err != nil {
			return false
		}
		return got.Spec.Template.Spec.Containers[0].Image == "ghcr.io/acme/agent:1.0.0"
	})
}

// Deleting an Agent must release the finalizer through the manager, not only
// when Reconcile is called by hand.
func TestManagerReleasesTheFinalizerOnDelete(t *testing.T) {
	startManager(t)
	ns := newNamespace(t)
	ctx := context.Background()

	a := &plumev1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{Name: "deleted", Namespace: ns},
		Spec: plumev1alpha1.AgentSpec{
			Runtime: &plumev1alpha1.AgentRuntime{Image: "ghcr.io/acme/agent:1.0.0"},
		},
	}
	if err := k8s.Create(ctx, a); err != nil {
		t.Fatalf("create: %v", err)
	}
	eventually(t, "the finalizer", func() bool {
		var got plumev1alpha1.Agent
		if err := k8s.Get(ctx, client.ObjectKeyFromObject(a), &got); err != nil {
			return false
		}
		return contains(got.Finalizers, controller.Finalizer)
	})

	if err := k8s.Delete(ctx, a); err != nil {
		t.Fatalf("delete: %v", err)
	}
	eventually(t, "the object to disappear", func() bool {
		var got plumev1alpha1.Agent
		return k8s.Get(ctx, client.ObjectKeyFromObject(a), &got) != nil
	})
}

func ptrTo[T any](v T) *T { return &v }

// The reconciler must be predicate-safe.
//
// GenerationChangedPredicate is the most common filter added to a controller,
// and finalizers are metadata: adding one does not bump metadata.generation. So
// a reconciler that added the finalizer and then relied on its own write
// producing a watch event would strand every new Agent forever the day someone
// added that predicate. The explicit requeue is what prevents it, and without a
// test like this that requeue looks redundant — it is redundant only for the
// wiring plume happens to ship today.
func TestReconcilerIsSafeUnderGenerationChangedPredicate(t *testing.T) {
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
		Controller:             ctrlconfig.Controller{SkipNameValidation: ptrTo(true)},
	})
	if err != nil {
		t.Fatalf("build manager: %v", err)
	}

	r, err := controller.NewAgentReconciler(mgr.GetClient(), mgr.GetScheme(), func() bool { return false })
	if err != nil {
		t.Fatalf("build reconciler: %v", err)
	}
	// The wiring plume does NOT ship, deliberately: if the reconciler only works
	// without this, it is one refactor from breaking.
	if err := ctrl.NewControllerManagedBy(mgr).
		For(&plumev1alpha1.Agent{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Owns(&appsv1.Deployment{}).
		Complete(r); err != nil {
		t.Fatalf("register: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- mgr.Start(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(30 * time.Second):
			t.Error("manager did not shut down within 30s")
		}
	})
	if !mgr.GetCache().WaitForCacheSync(ctx) {
		t.Fatal("cache never synced")
	}

	ns := newNamespace(t)
	a := &plumev1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{Name: "predicated", Namespace: ns},
		Spec: plumev1alpha1.AgentSpec{
			Runtime: &plumev1alpha1.AgentRuntime{Image: "ghcr.io/acme/agent:1.0.0"},
		},
	}
	if err := k8s.Create(ctx, a); err != nil {
		t.Fatalf("create: %v", err)
	}
	rev := revision.Hash(a.Spec)

	// Adding the finalizer changes only metadata, so the predicate suppresses the
	// resulting watch event. Only the explicit requeue gets us past this point.
	eventually(t, "the workload to be created despite the generation predicate", func() bool {
		var d appsv1.Deployment
		key := types.NamespacedName{Namespace: ns, Name: controller.WorkloadName("predicated", rev)}
		return k8s.Get(ctx, key, &d) == nil
	})
}
