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

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ADR-0030's last unmet clause: **one MCP tool call through the gateway.**
//
// No test here could make one, because there was no MCP server anywhere in the
// repository to call — `test/mcpserver` exists for this. The tool server is not
// an agent and does not live in an operator-owned run namespace; it sits in its
// own namespace behind a second Gateway listener, which is what a tool a tenant
// does not own looks like.
//
// **Hand-authored, and the allowlist is design 03's `toolAllowlist` measured
// rather than specified.** Nothing in assayd emits an AgentgatewayBackend or an
// AgentgatewayPolicy, before or after; no RBAC for either kind is granted. What
// this establishes is the shape a compiler would have to produce, and two
// behaviours of it that a schema reading would not have revealed — see the
// filtering assertion and the "Unknown tool" one below.
func TestAnAgentCallsAnMCPToolThroughTheGateway(t *testing.T) {
	requireCluster(t)
	requireOperator(t)
	img := os.Getenv("ASSAYD_E2E_MCP_IMAGE")
	if img == "" {
		if reason := os.Getenv("ASSAYD_E2E_RESPONDER_SKIP"); reason != "" {
			t.Skip("MCP tool call NOT EXERCISED: " + reason)
		}
		t.Fatal("ASSAYD_E2E_MCP_IMAGE is unset; `make e2e` builds the MCP server and pushes " +
			"it to the suite's registry")
	}
	gwNS, gwName := os.Getenv("ASSAYD_E2E_GATEWAY_NS"), os.Getenv("ASSAYD_E2E_GATEWAY_NAME")
	toolsNS := os.Getenv("ASSAYD_E2E_TOOLS_NS")
	if gwNS == "" || gwName == "" || toolsNS == "" {
		t.Fatal("ASSAYD_E2E_GATEWAY_NS/NAME or ASSAYD_E2E_TOOLS_NS unset; the harness creates " +
			"the Gateway, its `tools` listener and the namespace that listener admits")
	}
	ctx := context.Background()
	ensureNamespace(t, ctx, "assayd-e2e")

	deployMCPServer(t, ctx, toolsNS, img)
	backend := applyMCPBackend(t, ctx, toolsNS)
	route := attachMCPRoute(t, ctx, toolsNS, gwNS, gwName)
	assertRouteAccepted(t, ctx, route, "mcp-tools")

	gwSvc := gatewayService(t, ctx, gwNS, gwName)
	url := fmt.Sprintf("http://%s.%s.svc.cluster.local:8081/mcp", gwSvc, gwNS)
	const mcpHost = "mcp.assayd.test"

	// The gateway answers `mcp: no backends configured` with a 503 until its MCP
	// backend resolves a target, so the first exchange is retried rather than
	// asserted cold.
	var initRes map[string]any
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		initRes = mcpCall(t, ctx, "mcp-init", url, mcpHost, 1, "initialize", map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "assayd-e2e", "version": "0"},
		})
		if initRes["result"] != nil {
			break
		}
		time.Sleep(5 * time.Second)
	}
	res, _ := initRes["result"].(map[string]any)
	if res == nil {
		t.Fatalf("initialize never succeeded through the gateway: %+v", initRes)
	}
	if got := res["protocolVersion"]; got != "2025-06-18" {
		t.Errorf("the gateway negotiated protocolVersion %v, want the 2025-06-18 asked for", got)
	}
	if info, _ := res["serverInfo"].(map[string]any); info["name"] != "assayd-e2e-mcpserver" {
		t.Errorf("serverInfo names %v; the request did not reach this repository's MCP "+
			"fixture, so whatever answered is not the thing under test", info["name"])
	}

	// Unfiltered, before any policy. This is the baseline the filtering assertion
	// needs: without it, a tools/list showing one tool could mean the allowlist
	// worked or that the server only ever had one.
	if names := toolNames(t, ctx, "mcp-list-open", url, mcpHost); !names["echo_text"] || !names["delete_everything"] {
		t.Fatalf("with no policy the gateway listed %v; both tools must be visible first", names)
	}

	// **The clause itself: one MCP tool call through the gateway.**
	out := mcpCall(t, ctx, "mcp-call", url, mcpHost, 3, "tools/call", map[string]any{
		"name": "echo_text", "arguments": map[string]any{"text": "through the gateway"},
	})
	if got := mcpText(t, out); got != "echo: through the gateway" {
		t.Fatalf("the MCP tool call did not return the tool's answer: %q (%+v)", got, out)
	}

	// Now the allowlist — design 03's toolAllowlist, hand-authored.
	applyToolAllowlist(t, ctx, toolsNS, backend, `mcp.tool.name == "echo_text"`)

	// A refused tool is FILTERED FROM DISCOVERY, not merely refused on call. The
	// schema says list items are evaluated and non-matching ones removed, and it
	// is worth asserting because it is a stronger property than an allowlist
	// usually has: an agent never learns the tool exists.
	var names map[string]bool
	deadline = time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		names = toolNames(t, ctx, fmt.Sprintf("mcp-list-%d", time.Now().Unix()), url, mcpHost)
		if !names["delete_everything"] {
			break
		}
		time.Sleep(5 * time.Second)
	}
	if names["delete_everything"] {
		t.Errorf("tools/list still offers delete_everything under an allowlist that names "+
			"only echo_text: %v", names)
	}
	if !names["echo_text"] {
		t.Errorf("tools/list dropped the ALLOWED tool as well (%v), so the filtering above "+
			"is the policy refusing everything rather than refusing one thing", names)
	}

	// The permitted tool still works, on the same backend in the same second.
	out = mcpCall(t, ctx, "mcp-call-allowed", url, mcpHost, 5, "tools/call", map[string]any{
		"name": "echo_text", "arguments": map[string]any{"text": "still allowed"},
	})
	if got := mcpText(t, out); got != "echo: still allowed" {
		t.Fatalf("the allowed tool stopped working under the allowlist: %q (%+v)", got, out)
	}

	// The refused tool. The fixture answers this call normally when it reaches it
	// (its own unit test asserts that), so a refusal here is the gateway's.
	out = mcpCall(t, ctx, "mcp-call-refused", url, mcpHost, 6, "tools/call", map[string]any{
		"name": "delete_everything", "arguments": map[string]any{"text": "x"},
	})
	rpcErr, _ := out["error"].(map[string]any)
	if rpcErr == nil {
		t.Fatalf("the refused tool was CALLED: %+v — the allowlist filtered it from "+
			"discovery and then let it through, which is worse than not filtering", out)
	}
	// Recorded because it is a diagnosability trap, not because it is wrong: the
	// gateway reports a forbidden tool as "Unknown tool", indistinguishable from
	// one that does not exist. Good for non-disclosure, bad for the operator who
	// asks why their agent cannot call a tool they can see in the server.
	if msg, _ := rpcErr["message"].(string); !strings.Contains(msg, "delete_everything") {
		t.Errorf("the refusal does not name the tool: %v", rpcErr)
	}
}

func deployMCPServer(t *testing.T, ctx context.Context, ns, img string) {
	t.Helper()
	labels := map[string]string{"app": "mcpserver"}
	one := int32(1)
	d := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "mcpserver", Namespace: ns},
		Spec: appsv1.DeploymentSpec{
			Replicas: &one,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{Containers: []corev1.Container{{
					Name:  "mcpserver",
					Image: img,
					Ports: []corev1.ContainerPort{{ContainerPort: 8080}},
					ReadinessProbe: &corev1.Probe{ProbeHandler: corev1.ProbeHandler{
						HTTPGet: &corev1.HTTPGetAction{
							Path: "/healthz", Port: intstr.FromInt32(8080)},
					}},
				}}},
			},
		},
	}
	_ = k8s.Delete(ctx, d)
	if err := k8s.Create(ctx, d); err != nil {
		t.Fatalf("deploy the MCP server: %v", err)
	}
	t.Cleanup(func() { _ = k8s.Delete(context.Background(), d) })

	// appProtocol is REQUIRED and is not documented on the CRD. Without
	// `agentgateway.dev/mcp` the AgentgatewayBackend reaches Accepted=True, its
	// route resolves, and every request gets 503 `mcp: no backends configured` —
	// a healthy-looking backend with no targets. This line is the whole fix.
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "mcpserver", Namespace: ns, Labels: labels},
		Spec: corev1.ServiceSpec{
			Selector: labels,
			Ports: []corev1.ServicePort{{
				Port:        8080,
				TargetPort:  intstr.FromInt32(8080),
				Protocol:    corev1.ProtocolTCP,
				AppProtocol: ptr("agentgateway.dev/mcp"),
			}},
		},
	}
	_ = k8s.Delete(ctx, svc)
	if err := k8s.Create(ctx, svc); err != nil {
		t.Fatalf("expose the MCP server: %v", err)
	}
	t.Cleanup(func() { _ = k8s.Delete(context.Background(), svc) })

	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		var got appsv1.Deployment
		if err := k8s.Get(ctx, client.ObjectKeyFromObject(d), &got); err == nil &&
			got.Status.AvailableReplicas >= 1 {
			return
		}
		time.Sleep(3 * time.Second)
	}
	t.Fatal("the MCP server never became available")
}

func applyMCPBackend(t *testing.T, ctx context.Context, ns string) *unstructured.Unstructured {
	t.Helper()
	build := func() *unstructured.Unstructured {
		return &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "agentgateway.dev/v1alpha1",
			"kind":       "AgentgatewayBackend",
			"metadata":   map[string]any{"name": "mcp-tools", "namespace": ns},
			"spec": map[string]any{"mcp": map[string]any{
				// Stateless: no session to pin. Design 03 shifts traffic between
				// revisions by moving backendRefs weights, and a stateful MCP session
				// pinned to a revision either breaks when the weight moves or holds
				// traffic on the old one. agentgateway defaults this to Stateful;
				// assayd has not decided, and this fixture does not decide for it.
				"sessionRouting": "Stateless",
				"targets": []any{map[string]any{
					"name": "fixture",
					"selector": map[string]any{
						"services": map[string]any{
							"matchLabels": map[string]any{"app": "mcpserver"},
						},
					},
				}},
			}},
		}}
	}
	b := build()
	_ = k8s.Delete(ctx, build())
	if err := k8s.Create(ctx, b); err != nil {
		t.Fatalf("create the MCP backend: %v", err)
	}
	t.Cleanup(func() { _ = k8s.Delete(context.Background(), build()) })
	return b
}

// attachMCPRoute authors the route as the OPERATOR. A5.9's policy reserves every
// route naming the assayd Gateway to that identity, whichever listener it
// attaches to — the `tools` listener is not a way around the reservation.
func attachMCPRoute(t *testing.T, ctx context.Context, ns, gwNS, gwName string) *unstructured.Unstructured {
	t.Helper()
	build := func() *unstructured.Unstructured {
		return &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "gateway.networking.k8s.io/v1",
			"kind":       "HTTPRoute",
			"metadata":   map[string]any{"name": "mcp-tools", "namespace": ns},
			"spec": map[string]any{
				"parentRefs": []any{map[string]any{
					"name": gwName, "namespace": gwNS, "sectionName": "tools",
				}},
				"hostnames": []any{"mcp.assayd.test"},
				"rules": []any{map[string]any{
					"backendRefs": []any{map[string]any{
						"group": "agentgateway.dev",
						"kind":  "AgentgatewayBackend",
						"name":  "mcp-tools",
					}},
				}},
			},
		}}
	}
	cfg := rest.CopyConfig(restCfg)
	cfg.Impersonate = rest.ImpersonationConfig{
		UserName: "system:serviceaccount:assayd-system:assayd-agent-operator"}
	asOperator, err := client.New(cfg, client.Options{Scheme: k8s.Scheme()})
	if err != nil {
		t.Fatalf("impersonating the operator: %v", err)
	}
	r := build()
	_ = asOperator.Delete(ctx, build())
	if err := asOperator.Create(ctx, r); err != nil {
		t.Fatalf("author the MCP route as the operator: %v", err)
	}
	t.Cleanup(func() { _ = asOperator.Delete(context.Background(), build()) })
	return r
}

func applyToolAllowlist(t *testing.T, ctx context.Context, ns string, backend *unstructured.Unstructured, expr string) {
	t.Helper()
	build := func() *unstructured.Unstructured {
		return &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "agentgateway.dev/v1alpha1",
			"kind":       "AgentgatewayPolicy",
			"metadata":   map[string]any{"name": "mcp-allowlist", "namespace": ns},
			"spec": map[string]any{
				"targetRefs": []any{map[string]any{
					"group": "agentgateway.dev",
					"kind":  "AgentgatewayBackend",
					"name":  backend.GetName(),
				}},
				"backend": map[string]any{"mcp": map[string]any{
					"authorization": map[string]any{
						"action": "Allow",
						"policy": map[string]any{"matchExpressions": []any{expr}},
					},
				}},
			},
		}}
	}
	p := build()
	_ = k8s.Delete(ctx, build())
	if err := k8s.Create(ctx, p); err != nil {
		t.Fatalf("apply the MCP tool allowlist: %v", err)
	}
	t.Cleanup(func() { _ = k8s.Delete(context.Background(), build()) })
	assertPolicyAttached(t, ctx, p)
}

func toolNames(t *testing.T, ctx context.Context, tag, url, host string) map[string]bool {
	t.Helper()
	out := mcpCall(t, ctx, tag, url, host, 2, "tools/list", nil)
	res, _ := out["result"].(map[string]any)
	list, _ := res["tools"].([]any)
	names := map[string]bool{}
	for _, item := range list {
		if m, ok := item.(map[string]any); ok {
			if n, ok := m["name"].(string); ok {
				names[n] = true
			}
		}
	}
	return names
}

func mcpText(t *testing.T, out map[string]any) string {
	t.Helper()
	res, _ := out["result"].(map[string]any)
	content, _ := res["content"].([]any)
	if len(content) == 0 {
		return ""
	}
	first, _ := content[0].(map[string]any)
	s, _ := first["text"].(string)
	return s
}

// mcpCall sends one JSON-RPC message through the gateway and decodes the reply.
//
// The reply may be either shape and the caller cannot choose: agentgateway
// answers a successful MCP exchange as Server-Sent Events even when the upstream
// server returned plain JSON, and answers its OWN errors as bare JSON. A parser
// that assumed one would silently see nothing on the other.
func mcpCall(t *testing.T, ctx context.Context, tag, url, host string, id int, method string, params map[string]any) map[string]any {
	t.Helper()
	msg := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		msg["params"] = params
	}
	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal %s: %v", method, err)
	}
	cmd := fmt.Sprintf(
		"curl -sS --max-time 20 -X POST -H 'Host: %s' -H 'Content-Type: application/json' "+
			"-H 'Accept: application/json, text/event-stream' -d '%s' -o /dev/termination-log %s; exit 0",
		host, string(raw), url)
	body := runProbe(t, ctx, tag, cmd)

	payload := body
	for _, line := range strings.Split(body, "\n") {
		if after, ok := strings.CutPrefix(strings.TrimSpace(line), "data:"); ok {
			payload = strings.TrimSpace(after)
			break
		}
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(payload), &out); err != nil {
		// Not fatal: the caller retries initialize while the backend resolves, and
		// a 503 body is not JSON.
		return map[string]any{"raw": body}
	}
	return out
}

func ptr[T any](v T) *T { return &v }
