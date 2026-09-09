// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// The run namespace name is a DNS label, and a source namespace may itself be
// a full 63-character DNS label. Naive truncation is non-injective and would
// pool two tenants (design 02 A42); the rule is truncate-and-hash with 16 hex.
func TestRunNamespaceNameIsADNSLabelAndInjectiveAtTheLimit(t *testing.T) {
	if got := RunNamespaceName("team-a"); got != "assayd-run-team-a" {
		t.Errorf("short name: %s", got)
	}
	long1 := strings.Repeat("a", 60) + "xyz"
	long2 := strings.Repeat("a", 60) + "xyq" // same 36-char prefix, different name
	n1, n2 := RunNamespaceName(long1), RunNamespaceName(long2)
	if len(n1) > 63 || len(n2) > 63 {
		t.Errorf("names exceed a DNS label: %d, %d", len(n1), len(n2))
	}
	if n1 == n2 {
		t.Fatalf("two source namespaces sharing a prefix map to one run namespace %s — naive truncation "+
			"pools two tenants' material", n1)
	}
	if !strings.HasPrefix(n1, RunNamespacePrefix) {
		t.Errorf("prefix lost: %s", n1)
	}
	suffix := n1[strings.LastIndex(n1, "-")+1:]
	if len(suffix) != 16 {
		t.Errorf("suffix is %d hex, want 16 (64 bits): 32 bits is not enough for a name that is a "+
			"security boundary (design 03 A44)", len(suffix))
	}
	if RunNamespaceName(long1) != n1 {
		t.Error("not deterministic")
	}
}

func TestMirrorNameStaysWithinADNSSubdomain(t *testing.T) {
	long := strings.Repeat("q", 250)
	if got := MirrorName(long); len(got) > 253 || !strings.HasPrefix(got, MirrorPrefix) {
		t.Errorf("mirror name for a 250-char source is %d chars: %s", len(got), got)
	}
	if MirrorName("compute") != "assayd-mirror-compute" {
		t.Error("short mirror name")
	}
}

// decodeBinding is strict: the record decides namespace deletion and credential
// placement, so nothing defaults.
func TestBindingRecordDecodesStrictly(t *testing.T) {
	good := func() *corev1.ConfigMap {
		return &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "b"}, Data: map[string]string{
			"schemaVersion": "1", "sourceNamespace": "team-a", "sourceNamespaceUID": "u1",
			"runNamespace": "assayd-run-team-a", "runNamespaceUID": "u2", "nonce": "n", "state": "Bound"}}
	}
	if _, err := decodeBinding(good()); err != nil {
		t.Fatalf("a complete record was refused: %v", err)
	}
	cases := map[string]func(cm *corev1.ConfigMap){
		"unknown state":            func(cm *corev1.ConfigMap) { cm.Data["state"] = "Bouncing" },
		"missing state":            func(cm *corev1.ConfigMap) { delete(cm.Data, "state") },
		"missing source UID":       func(cm *corev1.ConfigMap) { delete(cm.Data, "sourceNamespaceUID") },
		"empty nonce":              func(cm *corev1.ConfigMap) { cm.Data["nonce"] = "" },
		"future schema version":    func(cm *corev1.ConfigMap) { cm.Data["schemaVersion"] = "2" },
		"bound without a run UID":  func(cm *corev1.ConfigMap) { delete(cm.Data, "runNamespaceUID") },
		"deleting without run UID": func(cm *corev1.ConfigMap) { cm.Data["state"] = "Deleting"; delete(cm.Data, "runNamespaceUID") },
	}
	for name, brk := range cases {
		cm := good()
		brk(cm)
		if _, err := decodeBinding(cm); err == nil {
			t.Errorf("%s: decoded without error; a permissive decoder reaches a decision arm on a "+
				"value nobody wrote", name)
		}
	}
	// Creating legitimately has no run UID yet.
	cm := good()
	cm.Data["state"] = "Creating"
	delete(cm.Data, "runNamespaceUID")
	if _, err := decodeBinding(cm); err != nil {
		t.Errorf("Creating without a run UID was refused: %v", err)
	}
}

// deleteRunNamespace is the one place the cluster-wide namespaces/delete grant
// is exercised, and it refuses any name outside the assayd-run- shape whatever
// the caller believes. Every caller today passes a run name, so this is the
// pin that keeps the check from being decorative.
func TestDeleteRunNamespaceRefusesAnyOtherName(t *testing.T) {
	victim := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "kube-system", UID: "u"}}
	c := fake.NewClientBuilder().WithObjects(victim).Build()
	r := &AgentReconciler{Client: c}
	if err := r.deleteRunNamespace(context.Background(), victim); err == nil {
		t.Fatal("a namespace outside the assayd-run- shape was deleted")
	}
	var still corev1.Namespace
	if err := c.Get(context.Background(), types.NamespacedName{Name: "kube-system"}, &still); err != nil {
		t.Fatalf("kube-system is gone: %v", err)
	}
}
