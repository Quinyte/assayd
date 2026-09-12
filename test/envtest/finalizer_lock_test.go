// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package envtest

import (
	"context"
	"fmt"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/controller"
)

// The operator writes its finalizer with a JSON merge patch (design 03 §3.1:
// a typed Update of an Agent stored with spec.budget is refused). A merge
// patch REPLACES a list, so the finalizer list it sends is the one the
// operator read. The optimistic lock is what makes a stale read fail instead
// of overwriting: without it, a finalizer another controller changed between
// the operator's read and its patch is silently undone. Nothing pinned the lock
// until PR #27's re-review changed both writes to a plain MergeFrom and the
// whole envtest package still passed.

const foreignFinalizer = "example.com/another-controller"

// raceClient changes the Agent under the operator the first time the operator
// patches one, so the patch runs against a base that is already stale. That is
// the real code path — the reconciler's own Get, its own patch — with the race
// placed exactly where production would lose it.
type raceClient struct {
	client.Client
	change func(ctx context.Context, a *assaydv1alpha1.Agent) error
	fired  bool
}

func (c *raceClient) Patch(ctx context.Context, obj client.Object, p client.Patch, opts ...client.PatchOption) error {
	if a, ok := obj.(*assaydv1alpha1.Agent); ok && !c.fired {
		c.fired = true
		if err := c.change(ctx, a); err != nil {
			return fmt.Errorf("the test's concurrent write failed: %w", err)
		}
	}
	return c.Client.Patch(ctx, obj, p, opts...)
}

// setFinalizers writes the finalizer list the way another controller would,
// with its own read, independent of the operator's copy.
func setFinalizers(ctx context.Context, key client.ObjectKey, edit func([]string) []string) error {
	var cur assaydv1alpha1.Agent
	if err := k8s.Get(ctx, key, &cur); err != nil {
		return err
	}
	base := cur.DeepCopy()
	cur.Finalizers = edit(cur.Finalizers)
	return k8s.Patch(ctx, &cur, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}))
}

func TestTheFinalizerPatchNeverOverwritesAConcurrentFinalizerWrite(t *testing.T) {
	ctx := context.Background()

	// ADD: another controller adds its finalizer after the operator read the
	// Agent. Without the lock, the operator's patch sends [assayd] and erases it.
	t.Run("add", func(t *testing.T) {
		ns := newNamespace(t)
		a := mustCreateAgent(t, ns, "raced-add", nil)
		key := client.ObjectKeyFromObject(a)
		r := newReconciler(false)
		r.Client = &raceClient{Client: k8s, change: func(ctx context.Context, _ *assaydv1alpha1.Agent) error {
			return setFinalizers(ctx, key, func(f []string) []string { return append(f, foreignFinalizer) })
		}}

		_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		if !apierrors.IsConflict(err) {
			t.Errorf("the operator's finalizer patch against a stale read returned %v, want a Conflict", err)
		}
		var got assaydv1alpha1.Agent
		if err := k8s.Get(ctx, key, &got); err != nil {
			t.Fatalf("read back: %v", err)
		}
		if !contains(got.Finalizers, foreignFinalizer) {
			t.Fatalf("another controller's finalizer was erased by the operator's patch: %v. A merge "+
				"patch replaces the list, and only the optimistic lock stops a stale one landing.",
				got.Finalizers)
		}

		// The next pass reads afresh and adds its own beside it.
		if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
			t.Fatalf("second pass: %v", err)
		}
		if err := k8s.Get(ctx, key, &got); err != nil {
			t.Fatalf("read back: %v", err)
		}
		if !contains(got.Finalizers, foreignFinalizer) || !contains(got.Finalizers, controller.Finalizer) {
			t.Errorf("after a fresh pass, want both finalizers, got %v", got.Finalizers)
		}
	})

	// RELEASE: the API server refuses a NEW finalizer on an object being
	// deleted, so the race here is the other direction. Another controller
	// releases its own finalizer after the operator read the Agent, and the
	// operator's patch then sends the list it read minus its own entry, which
	// would write the other controller's finalizer back.
	//
	// Measured, the lock is NOT what stops that: the API server refuses the
	// write back as Invalid on metadata.finalizers, lock or no lock, because it
	// adds a finalizer to an object being deleted. What the lock changes is the
	// error the operator gets. With it, a Conflict, which the next pass resolves
	// by reading afresh. Without it, an Invalid naming metadata.finalizers,
	// which reads as a defect in the Agent rather than as a race. This half pins
	// that the release conflicts; the add half pins what the lock protects.
	t.Run("release", func(t *testing.T) {
		ns := newNamespace(t)
		a := &assaydv1alpha1.Agent{
			ObjectMeta: metav1.ObjectMeta{Name: "raced-release", Namespace: ns,
				Finalizers: []string{controller.Finalizer, foreignFinalizer}},
			Spec: assaydv1alpha1.AgentSpec{Runtime: &assaydv1alpha1.AgentRuntime{Image: admissionImage}},
		}
		if err := k8s.Create(ctx, a); err != nil {
			t.Fatalf("create: %v", err)
		}
		key := client.ObjectKeyFromObject(a)
		if err := k8s.Delete(ctx, a); err != nil {
			t.Fatalf("delete: %v", err)
		}
		r := newReconciler(false)
		r.Client = &raceClient{Client: k8s, change: func(ctx context.Context, _ *assaydv1alpha1.Agent) error {
			return setFinalizers(ctx, key, func(f []string) []string {
				out := []string{}
				for _, x := range f {
					if x != foreignFinalizer {
						out = append(out, x)
					}
				}
				return out
			})
		}}

		_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		if !apierrors.IsConflict(err) {
			t.Errorf("the operator's finalizer release against a stale read returned %v, want a Conflict "+
				"that the next pass resolves. An Invalid on metadata.finalizers means the optimistic lock "+
				"is gone: the API server, not the lock, then refuses the stale write back.", err)
		}
		var got assaydv1alpha1.Agent
		if err := k8s.Get(ctx, key, &got); err != nil {
			t.Fatalf("the Agent is gone after one raced pass (%v), so the release did not conflict", err)
		}
		if contains(got.Finalizers, foreignFinalizer) {
			t.Fatalf("a finalizer its owner released was written back by the operator's stale patch: "+
				"%v. The Agent now waits on a controller that has already let go of it.", got.Finalizers)
		}

		// The next pass reads afresh and releases; the Agent goes.
		if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
			t.Fatalf("second pass: %v", err)
		}
		if err := k8s.Get(ctx, key, &got); !apierrors.IsNotFound(err) {
			t.Errorf("the Agent still exists after a fresh release (%v): %v", err, got.Finalizers)
		}
	})
}
