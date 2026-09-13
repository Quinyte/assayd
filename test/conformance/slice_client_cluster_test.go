//go:build cluster

// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package conformance

import (
	"context"
	"math/rand/v2"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/Quinyte/assayd/internal/compiler"
)

// policyClient writes AgentgatewayPolicies in sliceNS through client-go,
// using the same KUBECONFIG kubectl does.
//
// The latency case times the policy write, and `kubectl apply` put about
// 45 ms of process start and discovery around the one request that matters.
// A client-go Create brackets that request alone. warm is called before each
// timed write, so a connection's TLS handshake is not inside the bracket
// either.
func policyClient(t *testing.T) dynamic.ResourceInterface {
	t.Helper()
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(), &clientcmd.ConfigOverrides{}).ClientConfig()
	if err != nil {
		t.Fatalf("load the kubeconfig: %v", err)
	}
	dc, err := dynamic.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("build a dynamic client: %v", err)
	}
	gv, _ := schema.ParseGroupVersion(compiler.PolicyAPIVersion)
	return dc.Resource(gv.WithResource("agentgatewaypolicies")).Namespace(sliceNS)
}

// warm sends one cheap request, so the next one reuses an open connection.
func warm(t *testing.T, rc dynamic.ResourceInterface) {
	t.Helper()
	if _, err := rc.List(context.Background(), metav1.ListOptions{Limit: 1}); err != nil {
		t.Fatalf("list policies in %s: %v", sliceNS, err)
	}
}

// randomPause sleeps for a uniformly random time below max.
func randomPause(max time.Duration) { time.Sleep(rand.N(max)) }

// ms is a duration in milliseconds, to a microsecond.
func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }
