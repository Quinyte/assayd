// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/compiler"
)

// Design 03 §8.1 case 20's unit row, in the form review round 2 left it: the
// key-set map reads the cached Agents by AgentRunNamespaceIndex, so the index's
// extractor is what decides that a key set reaches EVERY Agent of its run
// namespace and no other's. A long source namespace is truncated and hashed,
// which is why the index is computed forwards rather than by inverting the name.
//
// Mutation: index an Agent by its own namespace. It compiles, and this must
// fail.
func TestAKeySetMapsToEveryAgentWithAPolicyThere(t *testing.T) {
	long := strings.Repeat("a", 60)
	for _, tc := range []struct{ ns, want string }{
		{"payments", "assayd-run-payments"},
		{long, RunNamespaceName(long)},
	} {
		a := &assaydv1alpha1.Agent{ObjectMeta: metav1.ObjectMeta{Namespace: tc.ns, Name: "billing"}}
		if got := agentRunNamespace(a); len(got) != 1 || got[0] != tc.want {
			t.Errorf("an Agent in %s is indexed under %v, want [%s]: a key set in its run namespace "+
				"would not reach it", tc.ns, got, tc.want)
		}
	}
}

// The selector is read only when the operator can know what it means:
// matchLabels alone. Anything else is UNKNOWN, never EMPTY (AGENTS.md rule 8).
func TestOnlyAMatchLabelsSelectorIsRead(t *testing.T) {
	p, err := compiler.AuthPolicy(compiler.AuthInput{AgentName: "billing", AgentNamespace: "payments",
		AgentUID: "0f0e0d0c-0000-0000-0000-00000000cafe", RunNamespace: "assayd-run-payments"})
	if err != nil {
		t.Fatal(err)
	}
	ns, sel, ok := keySourceSelector(p)
	if !ok || ns != "assayd-run-payments" || len(sel) != 1 || sel[compiler.APIKeySourceLabel] != compiler.APIKeySourceValue {
		t.Fatalf("the compiler's own selector was not read: %q %v %v", ns, sel, ok)
	}
	for name, edit := range map[string]func(*unstructured.Unstructured){
		"matchExpressions": func(u *unstructured.Unstructured) {
			_ = unstructured.SetNestedSlice(u.Object, []any{map[string]any{"key": "a", "operator": "Exists"}},
				"spec", "traffic", "apiKeyAuthentication", "configMapSelector", "matchExpressions")
		},
		"no matchLabels": func(u *unstructured.Unstructured) {
			unstructured.RemoveNestedField(u.Object, "spec", "traffic", "apiKeyAuthentication",
				"configMapSelector", "matchLabels")
		},
		"no apiKeyAuthentication": func(u *unstructured.Unstructured) {
			unstructured.RemoveNestedField(u.Object, "spec", "traffic", "apiKeyAuthentication")
		},
	} {
		c := p.DeepCopy()
		edit(c)
		if _, _, ok := keySourceSelector(c); ok {
			t.Errorf("%s: a selector the operator did not write was read as one it can list by", name)
		}
	}
}

// Entries are counted under data alone, per ConfigMap, never parsed: agentgateway
// 1.5.0 builds its key set from a ConfigMap's data and never reads binaryData
// (design 03 A88 (3), §9 D8 decided (R1), A89). The empty-case message names
// at most five ConfigMaps, and says how many hold entries only under
// binaryData.
//
// Mutation: count binaryData too (the binaryData-only rows must fail).
func TestAKeySourceIsCountedNotParsed(t *testing.T) {
	cm := func(name string, data map[string]string, bin map[string][]byte) corev1.ConfigMap {
		return corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: name}, Data: data, BinaryData: bin}
	}
	r, msg := countKeySource("ns", "a=b", "g", []corev1.ConfigMap{cm("x", nil, nil),
		cm("y", nil, map[string][]byte{"k": nil})})
	if r != keySourceEmpty {
		t.Error("an entry held only under binaryData counted as a key; agentgateway does not read binaryData")
	}
	if !strings.Contains(msg, "2 ConfigMap(s)") || !strings.Contains(msg, "none of which holds an entry in data") ||
		!strings.Contains(msg, "1 of them holds entries under binaryData") ||
		!strings.Contains(msg, "which agentgateway 1.5.0 does not read") ||
		!strings.Contains(msg, "under data rather than binaryData") {
		t.Errorf("the binaryData-only message does not say the binaryData entries exist and are not read: %s", msg)
	}
	if strings.Contains(msg, "adds entries") {
		t.Errorf("the binaryData-only message tells the administrator to add entries, which exist: %s", msg)
	}
	if _, msg := countKeySource("ns", "a=b", "g", []corev1.ConfigMap{cm("x", nil, map[string][]byte{"b": nil}),
		cm("y", nil, nil), cm("z", nil, map[string][]byte{"c": nil})}); !strings.Contains(msg,
		"3 ConfigMap(s)") || !strings.Contains(msg, "2 of them hold entries under binaryData") {
		t.Errorf("the binaryData clause does not count the ConfigMaps holding binaryData alone: %s", msg)
	}
	if r, _ := countKeySource("ns", "a=b", "g", []corev1.ConfigMap{cm("x", nil, map[string][]byte{"b": nil}),
		cm("y", map[string]string{"k": "v"}, map[string][]byte{"k2": nil})}); r != keySourcePresent {
		t.Error("a data entry beside binaryData entries did not count")
	}
	if _, msg := countKeySource("ns", "a=b", "g", []corev1.ConfigMap{cm("x", nil, nil)}); strings.Contains(msg,
		"binaryData") {
		t.Errorf("a key source with no binaryData entry names binaryData: %s", msg)
	}
	if r, _ := countKeySource("ns", "a=b", "g", []corev1.ConfigMap{cm("x", map[string]string{"k": ""}, nil)}); r != keySourcePresent {
		t.Error("an entry with an empty value did not count; if agentgateway rejects it, that is the policy half's report")
	}
	r, msg = countKeySource("ns", "a=b", "g", nil)
	if r != keySourceEmpty || !strings.Contains(msg, "a live list found none there") {
		t.Errorf("no labelled ConfigMap is not the absent report: %v %s", r, msg)
	}
	var many []corev1.ConfigMap
	for _, n := range []string{"k1", "k2", "k3", "k4", "k5", "k6", "k7"} {
		many = append(many, cm(n, nil, nil))
	}
	r, msg = countKeySource("ns", "a=b", "g", many)
	if r != keySourceEmpty || !strings.Contains(msg, "7 ConfigMap(s)") || !strings.Contains(msg, "and 2 more") ||
		strings.Contains(msg, "k6") || !strings.Contains(msg, "adds entries") {
		t.Errorf("the empty-case message does not name at most five ConfigMaps and the rest by count: %s", msg)
	}
}

// The pruning log's memory is bounded by the Agents still claiming an empty
// key source: a pass that finds its Agent gone forgets it (A87, review round
// 2). Mutation: drop the Delete on NotFound in Reconcile. It compiles, and this
// must fail.
func TestAGoneAgentIsForgottenByThePruneLog(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := assaydv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	r := &AgentReconciler{Client: fake.NewClientBuilder().WithScheme(scheme).Build(), Scheme: scheme}
	key := types.NamespacedName{Namespace: "payments", Name: "billing"}
	r.keySourcePruneLogged.Store(key, true)
	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key}); err != nil {
		t.Fatalf("reconcile of a gone Agent: %v", err)
	}
	if _, held := r.keySourcePruneLogged.Load(key); held {
		t.Error("a gone Agent is still remembered by the pruning log; the map grows with every Agent ever pruned")
	}
}
