// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package envtest

import (
	"context"
	"strings"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/controller"
)

// An Agent stored with spec.budget before ADR-0034 B2 refused it must still be
// finalizable once the refusing CRD is installed. Found by the review of PR #27.
//
// The operator wrote its finalizer with a typed Update. A typed round-trip
// adds `card: {}` and `runtime.resources: {}` to spec, so spec CHANGES, CRD
// validation ratcheting stops exempting it, and `!has(self.budget)` refuses
// the write. The finalizer could then be neither added nor released, and a
// `kubectl delete` removed the workloads and left the Agent Terminating forever,
// retrying. A merge patch that touches only metadata leaves spec unchanged, so
// ratcheting exempts the stored budget.
//
// The CRD is swapped inside the shared control plane. No envtest here runs in
// parallel, and t.Cleanup reinstalls the current CRD and waits until it refuses
// a budget again, so no other case sees the older schema.
func TestAnAgentStoredWithABudgetCanStillBeFinalized(t *testing.T) {
	ctx := context.Background()
	ns := newNamespace(t)

	var installed apiextensionsv1.CustomResourceDefinition
	if err := k8s.Get(ctx, client.ObjectKey{Name: "agents.assayd.dev"}, &installed); err != nil {
		t.Fatalf("get the Agent CRD: %v", err)
	}
	current := installed.Spec.DeepCopy()

	// installCRD writes spec and waits until the API server both reports the CRD
	// Established and applies the budget rule as asked — the second is what the
	// test depends on, and a CRD can be Established before its new schema serves.
	installCRD := func(spec *apiextensionsv1.CustomResourceDefinitionSpec, budgetAdmitted bool) {
		t.Helper()
		if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			var c apiextensionsv1.CustomResourceDefinition
			if err := k8s.Get(ctx, client.ObjectKey{Name: "agents.assayd.dev"}, &c); err != nil {
				return err
			}
			c.Spec = *spec.DeepCopy()
			return k8s.Update(ctx, &c)
		}); err != nil {
			t.Fatalf("install the Agent CRD: %v", err)
		}
		eventually(t, "the swapped Agent CRD to serve", func() bool {
			var c apiextensionsv1.CustomResourceDefinition
			if err := k8s.Get(ctx, client.ObjectKey{Name: "agents.assayd.dev"}, &c); err != nil {
				return false
			}
			established := false
			for _, cond := range c.Status.Conditions {
				if cond.Type == apiextensionsv1.Established && cond.Status == apiextensionsv1.ConditionTrue {
					established = true
				}
			}
			err := k8s.Create(ctx, budgetedAgent(ns, "probe"), client.DryRunAll)
			if budgetAdmitted {
				return established && err == nil
			}
			// Refused FOR THE BUDGET, not merely refused: any other error
			// (a half-served schema, a transient failure) would otherwise read
			// as the rule being back.
			return established && err != nil && strings.Contains(err.Error(), budgetRefusedMessage)
		})
	}
	t.Cleanup(func() { installCRD(current, false) })

	// The CRD as it was before ADR-0034 B2: the same schema without the rule.
	before := current.DeepCopy()
	removed := 0
	for i := range before.Versions {
		root := before.Versions[i].Schema.OpenAPIV3Schema
		spec := root.Properties["spec"]
		kept := spec.XValidations[:0]
		for _, v := range spec.XValidations {
			if v.Rule == "!has(self.budget)" {
				removed++
				continue
			}
			kept = append(kept, v)
		}
		spec.XValidations = kept
		root.Properties["spec"] = spec
	}
	if removed == 0 {
		t.Fatal("the installed Agent CRD has no !has(self.budget) rule, so there is nothing to ratchet past")
	}
	installCRD(before, true)

	// Stored before the refusal: one the operator never reconciled, and one
	// that already carries the finalizer, as every served Agent does.
	//
	// They are written UNSTRUCTURED, with only what a manifest says. A typed
	// create would serialize `card: {}` and `runtime.resources: {}` into the
	// stored spec, so the operator's typed round-trip would change nothing and
	// the bug would hide: the first version of this test did exactly that, and
	// passed on the code it was written to catch. `kubectl apply` stores what
	// the manifest says, which is the object the review measured.
	stored := func(name string, finalizers ...string) *unstructured.Unstructured {
		u := &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "assayd.dev/v1alpha1",
			"kind":       "Agent",
			"metadata":   map[string]any{"name": name, "namespace": ns},
			"spec": map[string]any{
				"runtime": map[string]any{"image": admissionImage},
				"budget":  map[string]any{"tokensPerDay": int64(1000)},
			},
		}}
		if len(finalizers) > 0 {
			u.SetFinalizers(finalizers)
		}
		return u
	}
	for _, u := range []*unstructured.Unstructured{stored("fresh"), stored("held", controller.Finalizer)} {
		if err := k8s.Create(ctx, u); err != nil {
			t.Fatalf("store %s under the pre-refusal CRD: %v", u.GetName(), err)
		}
	}
	fresh := &assaydv1alpha1.Agent{ObjectMeta: metav1.ObjectMeta{Name: "fresh", Namespace: ns}}
	held := &assaydv1alpha1.Agent{ObjectMeta: metav1.ObjectMeta{Name: "held", Namespace: ns}}

	installCRD(current, false)
	r := newReconciler(false)
	req := func(a *assaydv1alpha1.Agent) ctrl.Request {
		return ctrl.Request{NamespacedName: client.ObjectKeyFromObject(a)}
	}

	if _, err := r.Reconcile(ctx, req(fresh)); err != nil {
		t.Errorf("the operator could not add its finalizer to an Agent stored with spec.budget: %v\n"+
			"Its teardown is then unordered: nothing collects its workloads from the run namespace.", err)
	}
	var got assaydv1alpha1.Agent
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(fresh), &got); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !contains(got.Finalizers, controller.Finalizer) {
		t.Errorf("no finalizer on the stored budgeted Agent: %v", got.Finalizers)
	}

	// What design 03 §3.1 states about such an Agent, measured: a status write
	// is accepted, and any spec edit is refused until spec.budget is removed.
	got.Status.Phase = assaydv1alpha1.PhasePending
	if err := k8s.Status().Update(ctx, &got); err != nil {
		t.Errorf("a status write on a stored budgeted Agent was refused: %v", err)
	}
	edit := client.RawPatch(types.MergePatchType, []byte(`{"spec":{"runtime":{"replicas":2}}}`))
	if err := k8s.Patch(ctx, got.DeepCopy(), edit); err == nil || !strings.Contains(err.Error(), budgetRefusedMessage) {
		t.Errorf("a spec edit on a stored budgeted Agent was not refused with the budget message: %v", err)
	}

	for _, a := range []*assaydv1alpha1.Agent{fresh, held} {
		if err := k8s.Delete(ctx, a); err != nil {
			t.Fatalf("delete %s: %v", a.Name, err)
		}
		if _, err := r.Reconcile(ctx, req(a)); err != nil {
			t.Errorf("finalizing %s failed: %v\nThe operator retries this forever and the Agent stays "+
				"Terminating.", a.Name, err)
			continue
		}
		if err := k8s.Get(ctx, client.ObjectKeyFromObject(a), &got); !apierrors.IsNotFound(err) {
			t.Errorf("%s still exists after its finalizer ran (%v)", a.Name, err)
		}
	}
}

func budgetedAgent(ns, name string) *assaydv1alpha1.Agent {
	tokens := int64(1000)
	return &assaydv1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: assaydv1alpha1.AgentSpec{
			Runtime: &assaydv1alpha1.AgentRuntime{Image: admissionImage},
			Budget:  &assaydv1alpha1.BudgetSpec{TokensPerDay: &tokens},
		},
	}
}
