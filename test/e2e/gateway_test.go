// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/controller"
	"github.com/Quinyte/assayd/internal/revision"
)

// ADR-0030 step 3, first half: **author and execute the exact resources, then
// encode the mapping.**
//
// Everything this repository proved until now stopped at the revision Service.
// `gateway.enabled` defaults to false, the chart carries no agentgateway
// subchart (design 07 A1), and the policy compiler does not exist — so the
// platform's central claim, that governance becomes real AT THE GATEWAY, had
// never been exercised on any cluster. This is the test that ends that.
//
// **The route here is hand-authored by the test, not emitted by the operator,
// and that is the point rather than a shortcut.** Design 03 is RE-OPENED, so
// implementing its compiler now would be drift; ADR-0030 says to execute the
// resources first and encode them afterwards. What this proves is that the
// path is real and the listener rule is right — which is exactly what the
// compiler will need to be written against.
func TestAnAgentAnswersThroughTheGateway(t *testing.T) {
	requireCluster(t)
	requireOperator(t)
	img := responderImage(t)
	gwNS, gwName := os.Getenv("ASSAYD_E2E_GATEWAY_NS"), os.Getenv("ASSAYD_E2E_GATEWAY_NAME")
	if gwNS == "" || gwName == "" {
		t.Fatal("ASSAYD_E2E_GATEWAY_NS/NAME unset. `make e2e` installs agentgateway and " +
			"creates the Gateway; set ASSAYD_E2E_GATEWAY=0 only to skip that deliberately.")
	}
	ctx := context.Background()
	ensureNamespace(t, ctx, "assayd-e2e")

	const name = "throughgw"
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
		t.Fatalf("create agent: %v", err)
	}
	t.Cleanup(func() { _ = k8s.Delete(context.Background(), a) })

	rev := revision.MustHash(a.Spec)
	wl := controller.WorkloadName(name, rev)
	waitAvailable(t, ctx, wl, 4*time.Minute)

	// The route lives in the RUN namespace with the workload and Service, which
	// is design 02 A45's rule and design 03 §3.2's: a backendRef across
	// namespaces needs a ReferenceGrant in the target namespace, and leaving the
	// route behind would wedge every Agent at every install. The parentRef to a
	// Gateway in another namespace needs no grant — only backendRefs do — so the
	// listener's namespace selector is the whole of the consent.
	// **Design 07 A5.9's reservation is real, and this is where it was measured.**
	// The first version of this test authored the route as the test's own
	// identity and the API server refused it: "routes attaching to the assayd
	// Gateway are authored by the assayd operator only". That was the platform
	// working — a route attaching to this Gateway is a capability grant, and
	// anyone who could author one could publish an ungoverned path to any
	// workload. The accident is kept as the negative control below, because a
	// reservation nothing tests is a reservation that will quietly lapse.
	newRoute := func() *unstructured.Unstructured {
		return &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "gateway.networking.k8s.io/v1",
			"kind":       "HTTPRoute",
			"metadata":   map[string]any{"name": wl, "namespace": runNS},
			"spec": map[string]any{
				"parentRefs": []any{map[string]any{
					"name": gwName, "namespace": gwNS, "sectionName": "http",
				}},
				"hostnames": []any{name + ".assayd.test"},
				"rules": []any{map[string]any{
					"backendRefs": []any{map[string]any{"name": wl, "port": int64(8080)}},
				}},
			},
		}}
	}

	// Negative control first: an ordinary identity must NOT be able to attach.
	if err := k8s.Create(ctx, newRoute()); err == nil {
		_ = k8s.Delete(ctx, newRoute())
		t.Fatal("a non-operator identity attached a route to the assayd Gateway. Design 07 " +
			"A5.9 reserves that to the operator, because a route attaching here publishes a " +
			"path to a workload and anyone who can author one can publish an ungoverned path")
	} else if !strings.Contains(err.Error(), "assayd-gateway-routes") {
		t.Fatalf("the route was refused, but not by the reservation policy — so this proves "+
			"nothing about which rule refused it: %v", err)
	}

	// Now as the operator, which is what the compiler will be when it exists.
	cfg := rest.CopyConfig(restCfg)
	cfg.Impersonate = rest.ImpersonationConfig{
		UserName: "system:serviceaccount:assayd-system:assayd-agent-operator"}
	asOperator, err := client.New(cfg, client.Options{Scheme: k8s.Scheme()})
	if err != nil {
		t.Fatalf("impersonating the operator: %v", err)
	}
	route := newRoute()
	if err := asOperator.Create(ctx, route); err != nil {
		t.Fatalf("the operator could not author the route: %v", err)
	}
	t.Cleanup(func() { _ = asOperator.Delete(context.Background(), newRoute()) })

	// The listener must ACCEPT it. A route in a namespace the listener does not
	// admit is rejected, and the failure is silent from the client's side — it
	// simply 404s — so this is asserted before any request is sent.
	assertRouteAccepted(t, ctx, route, wl)

	// Through the Gateway's own Service, addressed by the route's hostname.
	// Nothing here talks to the revision Service directly; if the gateway is not
	// carrying the traffic, this cannot pass.
	gwSvc := gatewayService(t, ctx, gwNS, gwName)
	url := fmt.Sprintf("http://%s.%s.svc.cluster.local:8080/assayd-test/echo", gwSvc, gwNS)
	body := httpInClusterHost(t, ctx, "gwask", url, name+".assayd.test",
		`{"message":{"parts":[{"text":"through the gateway"}]}}`)

	var out struct {
		Status    struct{ State string }
		Agent     string
		Artifacts []struct {
			Parts []struct{ Text string }
		}
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("the gateway did not return the agent's answer: %v\n%s", err, body)
	}
	if out.Agent != name {
		t.Errorf("the answer came from %q, not the agent behind the route; body %s", out.Agent, body)
	}
	if out.Status.State != "ok" || len(out.Artifacts) == 0 ||
		!strings.Contains(out.Artifacts[0].Parts[0].Text, "through the gateway") {
		t.Errorf("the agent did not answer through the gateway: %s", body)
	}
}

// assertRouteAccepted fails with the listener's own reason rather than a 404
// forty seconds later, because "the route was not admitted" and "the agent is
// down" look identical from the client.
// assertRouteAccepted waits for the Gateway listener to accept the route.
//
// It reads the route's OWN namespace and name rather than the run namespace it
// was first written for: a second listener now admits the MCP tool namespace,
// and a helper that looked in one fixed place would have reported "never
// accepted" for a route that was accepted somewhere else.
func assertRouteAccepted(t *testing.T, ctx context.Context, route *unstructured.Unstructured, _ string) {
	ns, name := route.GetNamespace(), route.GetName()
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	var last string
	for time.Now().Before(deadline) {
		var got unstructured.Unstructured
		got.SetGroupVersionKind(route.GroupVersionKind())
		if err := k8s.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, &got); err == nil {
			parents, _, _ := unstructured.NestedSlice(got.Object, "status", "parents")
			for _, p := range parents {
				pm, ok := p.(map[string]any)
				if !ok {
					continue
				}
				conds, _, _ := unstructured.NestedSlice(pm, "conditions")
				for _, c := range conds {
					cm, ok := c.(map[string]any)
					if !ok {
						continue
					}
					if cm["type"] == "Accepted" {
						last = fmt.Sprintf("%v/%v: %v", cm["status"], cm["reason"], cm["message"])
						if cm["status"] == "True" {
							return
						}
					}
				}
			}
		}
		time.Sleep(3 * time.Second)
	}
	t.Fatalf("the Gateway listener never accepted route %s/%s (%s). A listener admits routes "+
		"only from namespaces its allowedRoutes selector matches; if %s does not match, the "+
		"route is rejected and every request 404s with no other signal.", ns, name, last, ns)
}

func gatewayService(t *testing.T, ctx context.Context, ns, name string) string {
	t.Helper()
	var svcs corev1.ServiceList
	if err := k8s.List(ctx, &svcs); err != nil {
		t.Fatalf("list services: %v", err)
	}
	for _, s := range svcs.Items {
		if s.Namespace == ns && strings.Contains(s.Name, name) {
			return s.Name
		}
	}
	t.Fatalf("no Service for Gateway %s/%s; agentgateway provisions one per Gateway", ns, name)
	return ""
}
