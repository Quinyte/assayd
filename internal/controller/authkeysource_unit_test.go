// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/compiler"
)

// Design 03 §8.1 case 20's unit row: a key set in a run namespace maps to
// EVERY Agent whose run namespace it is, because in the slice every
// <agent>-auth in a run namespace selects the same key set — and to no Agent of
// another namespace. Since A87 the map reads the cached Agent list rather than
// listing policies live.
//
// Mutations, one per run: return only the first; drop the run-namespace
// comparison. Each compiles, and this must fail.
func TestAKeySetMapsToEveryAgentWithAPolicyThere(t *testing.T) {
	agent := func(ns, name string) assaydv1alpha1.Agent {
		return assaydv1alpha1.Agent{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name}}
	}
	got := keySetRequests(RunNamespaceName("payments"), []assaydv1alpha1.Agent{
		agent("payments", "billing"), agent("ledger", "books"), agent("payments", "refunds")})
	want := []types.NamespacedName{{Namespace: "payments", Name: "billing"}, {Namespace: "payments", Name: "refunds"}}
	if len(got) != len(want) {
		t.Fatalf("a key-set event maps to %v, want exactly the two Agents of its run namespace: %v", got, want)
	}
	for i := range want {
		if got[i].NamespacedName != want[i] {
			t.Errorf("request %d is %v, want %v", i, got[i].NamespacedName, want[i])
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

// Entries are counted across data AND binaryData, per ConfigMap, never
// parsed; and the empty-case message names at most five ConfigMaps.
func TestAKeySourceIsCountedNotParsed(t *testing.T) {
	cm := func(name string, data map[string]string, bin map[string][]byte) corev1.ConfigMap {
		return corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: name}, Data: data, BinaryData: bin}
	}
	if r, _ := countKeySource("ns", "a=b", "g", []corev1.ConfigMap{cm("x", nil, nil),
		cm("y", nil, map[string][]byte{"k": nil})}); r != keySourcePresent {
		t.Error("an entry held only under binaryData did not count")
	}
	if r, _ := countKeySource("ns", "a=b", "g", []corev1.ConfigMap{cm("x", map[string]string{"k": ""}, nil)}); r != keySourcePresent {
		t.Error("an entry with an empty value did not count; if agentgateway rejects it, that is the policy half's report")
	}
	r, msg := countKeySource("ns", "a=b", "g", nil)
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
