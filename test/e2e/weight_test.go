// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/controller"
	"github.com/Quinyte/assayd/internal/revision"
)

// Design 02 uses **weight 0 as a containment action** on five paths: a revision
// whose material cannot be reproduced (`02:186`, `RevisionMaterialUnavailable`),
// a superseded candidate draining (`02:261`), a candidate held for gates
// (`02:272`, `02:496`), and a missing material copy (`02:366`). Each says, in
// effect, "send this revision to weight 0 and it stops serving."
//
// Nothing had ever checked that. The design's containment story rests entirely
// on the claim that a Gateway API `backendRef` weight of 0 means no traffic, and
// `research/mcp-session-statefulness-2026-09.md` found a specification that says
// it does not always: **GEP-1619 states session persistence "MUST be maintained
// ... even if the weight is set to 0"** and takes precedence over traffic split
// weights. That research is desk work. This is the measurement.
//
// **What this test does and does not cover.** Two Agents stand in for two
// revisions: the mechanism under test is the Gateway API weight itself, which
// design 02 applies identically to two revision Services of one Agent, and using
// two Agents removes the revision-retention machinery from a test that is not
// about it. It establishes the BASE case — no session persistence anywhere. The
// persistence interaction is a separate question and is recorded at the bottom
// of this file, because `sessionPersistence` is not a field this cluster has.
func TestWeightZeroActuallyContains(t *testing.T) {
	requireCluster(t)
	requireOperator(t)
	img := responderImage(t)
	gwNS, gwName := os.Getenv("ASSAYD_E2E_GATEWAY_NS"), os.Getenv("ASSAYD_E2E_GATEWAY_NAME")
	if gwNS == "" || gwName == "" {
		t.Fatal("ASSAYD_E2E_GATEWAY_NS/NAME unset; weights are a gateway mechanism and there " +
			"is nothing to measure without one")
	}
	ctx := context.Background()
	ensureNamespace(t, ctx, "assayd-e2e")

	const nameA, nameB = "weightblue", "weightgreen"
	wlA := deployResponder(t, ctx, nameA, img)
	wlB := deployResponder(t, ctx, nameB, img)

	runNS := controller.RunNamespaceName("assayd-e2e")
	const host = "weight.assayd.test"
	route := attachWeightedRoute(t, ctx, runNS, gwNS, gwName, host, wlA, 100, wlB, 0)
	assertRouteAccepted(t, ctx, route, "")

	gwSvc := gatewayService(t, ctx, gwNS, gwName)
	url := fmt.Sprintf("http://%s.%s.svc.cluster.local:8080/assayd-test/echo", gwSvc, gwNS)

	// 40 requests, not one. A single request landing on the 100-weight backend
	// proves nothing about a 0-weight one — that is what a coin flip looks like
	// when it comes up heads. The count is what makes "never" measurable.
	const n = 40
	counts := countBackends(t, ctx, "w-blue", url, host, n)
	if counts[nameB] != 0 {
		t.Errorf("the backend at WEIGHT 0 served %d of %d requests. Design 02 sends a revision "+
			"to weight 0 to contain it on five separate failure paths (02:186, 261, 272, 366, "+
			"496); if weight 0 does not contain, none of those five paths contains: %v",
			counts[nameB], n, counts)
	}
	if counts[nameA] != n {
		t.Errorf("the backend at weight 100 served %d of %d; the rest went nowhere identifiable, "+
			"so this run cannot speak to containment either: %v", counts[nameA], n, counts)
	}

	// Flip it. Without this, a green run could mean "weight 0 contains" or it
	// could mean "the second backend was never reachable at all" — and the second
	// is exactly what a broken fixture looks like.
	setWeights(t, ctx, route, wlA, 0, wlB, 100)
	deadline := time.Now().Add(time.Minute)
	var flipped map[string]int
	for time.Now().Before(deadline) {
		flipped = countBackends(t, ctx, fmt.Sprintf("w-green-%d", time.Now().Unix()), url, host, 5)
		if flipped[nameB] > 0 {
			break
		}
		time.Sleep(5 * time.Second)
	}
	flipped = countBackends(t, ctx, "w-green", url, host, n)
	if flipped[nameA] != 0 {
		t.Errorf("after the flip the ORIGINAL backend, now at weight 0, served %d of %d: %v",
			flipped[nameA], n, flipped)
	}
	if flipped[nameB] != n {
		t.Errorf("after the flip the weight-100 backend served %d of %d: %v",
			flipped[nameB], n, flipped)
	}
}

// TestGatewayAPIHasNoSessionPersistenceHere is the other half of the GEP-1619
// question, and it is a fact about the install rather than about behaviour.
//
// GEP-1619's rule — persistence outranks weight, including weight 0 — can only
// bite if something can turn persistence on. `sessionPersistence` is an
// EXPERIMENTAL-channel field: `hack/e2e.sh` installs `standard-install.yaml`,
// and the v1 `HTTPRoute` it defines has no such field. So on a standard install
// the hazard is not mitigated, it is **unreachable** — and this test fails the
// day that stops being true, which is the day design 02's weight-0 containment
// needs re-examining.
func TestGatewayAPIHasNoSessionPersistenceHere(t *testing.T) {
	requireCluster(t)
	ctx := context.Background()

	var crd unstructured.Unstructured
	crd.SetGroupVersionKind(schema.GroupVersionKind{
		Group: "apiextensions.k8s.io", Version: "v1", Kind: "CustomResourceDefinition"})
	if err := k8s.Get(ctx, client.ObjectKey{Name: "httproutes.gateway.networking.k8s.io"}, &crd); err != nil {
		t.Skipf("no Gateway API installed: %v", err)
	}
	versions, _, _ := unstructured.NestedSlice(crd.Object, "spec", "versions")
	for _, v := range versions {
		vm, ok := v.(map[string]any)
		if !ok {
			continue
		}
		name, _, _ := unstructured.NestedString(vm, "name")
		served, _, _ := unstructured.NestedBool(vm, "served")
		if !served {
			continue
		}
		rule, found, _ := unstructured.NestedMap(vm,
			"schema", "openAPIV3Schema", "properties", "spec", "properties",
			"rules", "items", "properties")
		if !found {
			continue
		}
		if _, has := rule["sessionPersistence"]; has {
			t.Errorf("HTTPRoute %s carries sessionPersistence, so this cluster is on the "+
				"EXPERIMENTAL channel and GEP-1619's rule is now reachable: persistence "+
				"outranks weight and is maintained even at weight 0. Design 02's five "+
				"weight-0 containment paths (02:186, 261, 272, 366, 496) need a second "+
				"mechanism — route withdrawal — before anything enables it", name)
		}
	}
}

// deployResponder creates an Agent, waits for it, and returns its workload name.
func deployResponder(t *testing.T, ctx context.Context, name, img string) string {
	t.Helper()
	a := &assaydv1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "assayd-e2e"},
		Spec: assaydv1alpha1.AgentSpec{
			Runtime: &assaydv1alpha1.AgentRuntime{
				Image: img,
				Env:   []corev1.EnvVar{{Name: "AGENT_NAME", Value: name}},
			},
		},
	}
	_ = k8s.Delete(ctx, a)
	if err := k8s.Create(ctx, a); err != nil {
		t.Fatalf("create agent %s: %v", name, err)
	}
	t.Cleanup(func() { _ = k8s.Delete(context.Background(), a) })
	wl := controller.WorkloadName(name, revision.MustHash(a.Spec))
	waitAvailable(t, ctx, wl, 4*time.Minute)
	return wl
}

func weightedRule(wlA string, wA int64, wlB string, wB int64) []any {
	return []any{map[string]any{
		"backendRefs": []any{
			map[string]any{"name": wlA, "port": int64(8080), "weight": wA},
			map[string]any{"name": wlB, "port": int64(8080), "weight": wB},
		},
	}}
}

func attachWeightedRoute(t *testing.T, ctx context.Context, runNS, gwNS, gwName, host,
	wlA string, wA int64, wlB string, wB int64) *unstructured.Unstructured {
	t.Helper()
	build := func() *unstructured.Unstructured {
		return &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "gateway.networking.k8s.io/v1",
			"kind":       "HTTPRoute",
			"metadata":   map[string]any{"name": "weights", "namespace": runNS},
			"spec": map[string]any{
				"parentRefs": []any{map[string]any{
					"name": gwName, "namespace": gwNS, "sectionName": "http",
				}},
				"hostnames": []any{host},
				"rules":     weightedRule(wlA, wA, wlB, wB),
			},
		}}
	}
	asOperator := operatorClient(t)
	r := build()
	_ = asOperator.Delete(ctx, build())
	if err := asOperator.Create(ctx, r); err != nil {
		t.Fatalf("author the weighted route: %v", err)
	}
	t.Cleanup(func() { _ = asOperator.Delete(context.Background(), build()) })
	return r
}

func setWeights(t *testing.T, ctx context.Context, route *unstructured.Unstructured,
	wlA string, wA int64, wlB string, wB int64) {
	t.Helper()
	asOperator := operatorClient(t)
	var live unstructured.Unstructured
	live.SetGroupVersionKind(route.GroupVersionKind())
	if err := asOperator.Get(ctx, client.ObjectKeyFromObject(route), &live); err != nil {
		t.Fatalf("read the route back: %v", err)
	}
	if err := unstructured.SetNestedSlice(live.Object, weightedRule(wlA, wA, wlB, wB), "spec", "rules"); err != nil {
		t.Fatalf("set weights: %v", err)
	}
	if err := asOperator.Update(ctx, &live); err != nil {
		t.Fatalf("update the route weights: %v", err)
	}
}

func operatorClient(t *testing.T) client.Client {
	t.Helper()
	cfg := rest.CopyConfig(restCfg)
	cfg.Impersonate = rest.ImpersonationConfig{
		UserName: "system:serviceaccount:assayd-system:assayd-agent-operator"}
	c, err := client.New(cfg, client.Options{Scheme: k8s.Scheme()})
	if err != nil {
		t.Fatalf("impersonating the operator: %v", err)
	}
	return c
}

// countBackends sends n requests through the gateway from ONE pod and tallies
// which agent answered each. One pod rather than n, because n pods would measure
// pod scheduling as much as routing, and because the responder names itself in
// every answer — which is exactly why it does.
func countBackends(t *testing.T, ctx context.Context, tag, url, host string, n int) map[string]int {
	t.Helper()
	cmd := fmt.Sprintf(
		"for i in $(seq 1 %d); do "+
			"curl -sS --max-time 10 -X POST -H 'Host: %s' -H 'Content-Type: application/json' "+
			"-d '{\"message\":{\"parts\":[{\"text\":\"w\"}]}}' %s "+
			"| tr ',' '\\n' | grep '\"agent\"' ; done > /dev/termination-log 2>/dev/null; exit 0",
		n, host, url)
	body := runProbe(t, ctx, tag, cmd)
	counts := map[string]int{}
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if _, after, ok := strings.Cut(line, `"agent":`); ok {
			name := strings.Trim(after, `"`)
			if name != "" {
				counts[name]++
			}
		}
	}
	total := 0
	for _, c := range counts {
		total += c
	}
	if total == 0 {
		t.Fatalf("no request identified a backend; the probe returned %q", body)
	}
	t.Logf("%s: %d/%d requests identified a backend: %v", tag, total, n, counts)
	return counts
}
