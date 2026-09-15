// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package envtest

import (
	"context"
	"strings"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
)

// Design 02 A76 bounds and constrains spec.card.path, and rests the cost of
// that on CRD validation ratcheting: an Agent stored with `/card.json?v=1`
// before the rule keeps working. That sentence was prose with nothing behind
// it. This measures it, the way TestAnAgentStoredWithABudgetCanStillBeFinalized
// measures ADR-0034 B2's.
//
// Measured at Kubernetes 1.36.2, which is what `make envtest` pins
// (ENVTEST_K8S in the Makefile). Ratcheting is on by default from 1.30, and
// charts/assayd/Chart.yaml declares kubeVersion ">=1.30.0-0", so the oldest
// cluster the chart admits is the oldest one on which this holds. No cluster
// between 1.30 and 1.36.1 is measured here.
//
// The CRD is swapped inside the shared control plane, as the budget test does.
// No envtest here runs in parallel, and t.Cleanup reinstalls the current CRD
// and waits until it refuses an odd path again, so no sibling sees the older
// schema.
func TestAStoredCardPathStaysEditable(t *testing.T) {
	ctx := context.Background()
	ns := newNamespace(t)

	// The path the new rule refuses and the old schema took: A75's own example.
	const storedPath = "/card.json?v=1"

	var installed apiextensionsv1.CustomResourceDefinition
	if err := k8s.Get(ctx, client.ObjectKey{Name: "agents.assayd.dev"}, &installed); err != nil {
		t.Fatalf("get the Agent CRD: %v", err)
	}
	current := installed.Spec.DeepCopy()

	oddPathAgent := func(name string) *assaydv1alpha1.Agent {
		return &assaydv1alpha1.Agent{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
			Spec: assaydv1alpha1.AgentSpec{
				Runtime: &assaydv1alpha1.AgentRuntime{Image: admissionImage},
				Card:    assaydv1alpha1.CardSpec{Path: storedPath},
			},
		}
	}

	// installCRD writes spec and waits until the API server both reports the
	// CRD Established and applies the card-path rule as asked. A CRD can be
	// Established before its new schema serves, and the second is what this
	// test depends on.
	installCRD := func(spec *apiextensionsv1.CustomResourceDefinitionSpec, oddPathAdmitted bool) {
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
			err := k8s.Create(ctx, oddPathAgent("probe"), client.DryRunAll)
			if oddPathAdmitted {
				return established && err == nil
			}
			// Refused FOR THE PATH, not merely refused: a half-served schema
			// or a transient failure would otherwise read as the rule being
			// back.
			return established && err != nil && strings.Contains(err.Error(), cardPathRefusedMessage)
		})
	}
	t.Cleanup(func() { installCRD(current, false) })

	// The CRD as it was before A76: the same schema with the field's bound and
	// rule removed.
	before := current.DeepCopy()
	removed := 0
	for i := range before.Versions {
		root := before.Versions[i].Schema.OpenAPIV3Schema
		spec := root.Properties["spec"]
		card := spec.Properties["card"]
		path := card.Properties["path"]
		if path.MaxLength != nil {
			path.MaxLength = nil
			removed++
		}
		removed += len(path.XValidations)
		path.XValidations = nil
		card.Properties["path"] = path
		spec.Properties["card"] = card
		root.Properties["spec"] = spec
	}
	if removed == 0 {
		t.Fatal("the installed Agent CRD bounds and constrains no card path, so there is nothing to ratchet past")
	}
	installCRD(before, true)

	// Written UNSTRUCTURED, with only what a manifest says, for the reason the
	// budget test gives: a typed create serializes `runtime.resources: {}` into
	// the stored spec, so a later typed round-trip would change nothing and a
	// ratcheting failure would hide.
	stored := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "assayd.dev/v1alpha1",
		"kind":       "Agent",
		"metadata":   map[string]any{"name": "odd-path", "namespace": ns},
		"spec": map[string]any{
			"runtime": map[string]any{"image": admissionImage},
			"card":    map[string]any{"path": storedPath},
		},
	}}
	if err := k8s.Create(ctx, stored); err != nil {
		t.Fatalf("store an odd card path under the pre-A76 CRD: %v", err)
	}

	installCRD(current, false)

	// 1. A status write is accepted. This is the one that matters most: the
	//    operator writes status on every pass, and an Agent that cannot write
	//    status reports nothing at all.
	var got assaydv1alpha1.Agent
	if err := k8s.Get(ctx, types.NamespacedName{Namespace: ns, Name: "odd-path"}, &got); err != nil {
		t.Fatalf("read back: %v", err)
	}
	got.Status.Phase = assaydv1alpha1.PhasePending
	if err := k8s.Status().Update(ctx, &got); err != nil {
		t.Errorf("a status write on an Agent stored with %q was refused: %v\n"+
			"Ratcheting is what makes A76's schema change safe for stored objects.", storedPath, err)
	}

	// 2. An unrelated spec edit is accepted, because the invalid field is
	//    unchanged by it.
	edit := client.RawPatch(types.MergePatchType, []byte(`{"spec":{"runtime":{"replicas":2}}}`))
	if err := k8s.Patch(ctx, got.DeepCopy(), edit); err != nil {
		t.Errorf("an unrelated spec edit on an Agent stored with %q was refused: %v", storedPath, err)
	}

	// 3. The rule is not merely dormant: changing the path to another invalid
	//    one is still refused, and with A76's message.
	repath := client.RawPatch(types.MergePatchType, []byte(`{"spec":{"card":{"path":"/other.json#frag"}}}`))
	err := k8s.Patch(ctx, got.DeepCopy(), repath)
	if err == nil || !strings.Contains(err.Error(), cardPathRefusedMessage) {
		t.Errorf("a NEW invalid card path on a ratcheted Agent was not refused with A76's message: %v", err)
	}
}

// cardPathRefusedMessage is the distinctive half of the CEL rule's message
// (api/v1alpha1/agent_types.go, CardSpec). Asserting the whole message here
// would break on every wording change; asserting "spec.card.path" would pass
// on the length error and on any future rule on the field.
const cardPathRefusedMessage = "never be read as a query or a fragment"
