// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// A gatewayPolicyReads with no uncached reader reads through its client, as
// (*AgentReconciler).reader did before the reads moved (design 16 §1.1 step
// 4): in envtest the client itself is uncached and Reader is nil. Without the
// fallback, the moved bodies would call a method on a nil reader and panic.
func TestGatewayPolicyReadsFallBackToTheClient(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := gatewayv1.Install(scheme); err != nil {
		t.Fatal(err)
	}
	gw := &gatewayv1.Gateway{ObjectMeta: metav1.ObjectMeta{Name: "assayd", Namespace: "assayd-gateway"}}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(gw).Build()

	reads := gatewayPolicyReads{Client: c,
		Gateway: GatewayConfig{Enabled: true, Name: "assayd", Namespace: "assayd-gateway"}}
	if got := reads.reader(); got != c {
		t.Fatalf("a nil uncached reader resolved to %v, not the client", got)
	}
	// The Gateway get goes through reader(): it finds the Gateway the client
	// holds, so the Gateway is not reported unreadable.
	out := reads.gatewayAuthPolicies(context.Background(), "agent.team.example")
	if strings.Contains(out.unreadable, "Gateway assayd-gateway/assayd") {
		t.Errorf("the Gateway was not read through the client: %s", out.unreadable)
	}

	r := &AgentReconciler{Client: c}
	if got := r.reader(); got != c {
		t.Errorf("(*AgentReconciler).reader with no Reader resolved to %v, not the client", got)
	}
	uncached := fake.NewClientBuilder().WithScheme(scheme).Build()
	r.Reader = uncached
	if got := r.reader(); got != uncached {
		t.Errorf("(*AgentReconciler).reader ignored its Reader")
	}
	if got := r.policyReads().reader(); got != uncached {
		t.Errorf("policyReads did not carry the reconciler's Reader")
	}
}
