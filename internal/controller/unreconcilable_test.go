package controller

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	plumev1alpha1 "github.com/Quinyte/plume/api/v1alpha1"
)

// An Agent with neither runtime nor external cannot be created today — CEL
// rejects it, and spec is required. But an object stored before those rules
// existed is still readable, and an operator must not panic on something it was
// handed, whatever admission did or did not run when it was written.
//
// This test exists because the same standard was applied when deleting a
// redundant nil-check elsewhere: defensive code no test can pin reads as
// load-bearing and invites the next reader to trust it.
func TestUnreconcilableAgentDegradesRatherThanPanicking(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("scheme: %v", err)
	}
	if err := plumev1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("scheme: %v", err)
	}

	// A fake client bypasses admission, which is the point: this is the shape of
	// an object that predates the current CRD.
	stored := &plumev1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{
			Name: "legacy", Namespace: "default",
			Finalizers: []string{Finalizer},
		},
		// Neither Runtime nor External.
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(stored).
		WithStatusSubresource(stored).
		Build()

	r := &AgentReconciler{Client: c, Scheme: scheme, EvalSuiteInstalled: func() bool { return false }}

	defer func() {
		if p := recover(); p != nil {
			t.Fatalf("reconcile panicked on an Agent with no workload: %v. Any object the "+
				"operator can read, it must be able to report on.", p)
		}
	}()

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: ctrlKey("default", "legacy"),
	})
	if err != nil {
		t.Fatalf("reconcile returned an error rather than reporting the condition: %v", err)
	}

	var got plumev1alpha1.Agent
	if err := c.Get(context.Background(), ctrlKey("default", "legacy"), &got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status.Phase != plumev1alpha1.PhaseDegraded {
		t.Errorf("phase is %q, want Degraded", got.Status.Phase)
	}
	var found bool
	for _, cond := range got.Status.Conditions {
		if cond.Type == string(plumev1alpha1.CondDegraded) && cond.Reason == "NoWorkloadSpecified" {
			found = true
			if cond.Message == "" {
				t.Error("the condition must say what to do about it")
			}
		}
	}
	if !found {
		t.Errorf("no Degraded/NoWorkloadSpecified condition; an error loop with an empty "+
			"status says nothing to whoever has to fix it. Got: %+v", got.Status.Conditions)
	}
}

func ctrlKey(ns, name string) types.NamespacedName {
	return types.NamespacedName{Namespace: ns, Name: name}
}
