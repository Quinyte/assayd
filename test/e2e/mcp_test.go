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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/controller"
	"github.com/Quinyte/assayd/internal/revision"
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
	gwNS, gwName := requireGateway(t)
	toolsNS := os.Getenv("ASSAYD_E2E_TOOLS_NS")
	if toolsNS == "" {
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

// ADR-0030's clause WITH its subject: "one **agent** completes one A2A task and
// one MCP tool call through the gateway."
//
// The test above makes the MCP call from a probe Pod, which is a client, not an
// agent. Here the caller asks an agent for a task over A2A, through the
// operator's route, and the agent completes it BY calling a tool over MCP —
// through the gateway at the ASSAYD_GATEWAY_URL the operator injected, which is
// the only address it has for tools. Two things prove the call went through the
// gateway and not around it: the agent has no other tool address, and a tool
// the fixture server would answer normally is refused once the gateway's
// allowlist excludes it.
func TestAnAgentCompletesATaskByCallingAToolThroughTheGateway(t *testing.T) {
	requireCluster(t)
	requireOperator(t)
	img := responderImage(t)
	mcpImg := os.Getenv("ASSAYD_E2E_MCP_IMAGE")
	if mcpImg == "" {
		t.Fatal("ASSAYD_E2E_MCP_IMAGE is unset; `make e2e` builds the MCP server")
	}
	gwNS, gwName := requireGateway(t)
	toolsNS, gwURL := os.Getenv("ASSAYD_E2E_TOOLS_NS"), os.Getenv("ASSAYD_E2E_GATEWAY_URL")
	if toolsNS == "" || gwURL == "" {
		t.Fatal("ASSAYD_E2E_TOOLS_NS or ASSAYD_E2E_GATEWAY_URL unset; the harness creates the " +
			"tools listener and tells the chart where agents' egress goes")
	}
	ctx := context.Background()
	ensureNamespace(t, ctx, "assayd-e2e")

	deployMCPServer(t, ctx, toolsNS, mcpImg)
	backend := applyMCPBackend(t, ctx, toolsNS)
	assertRouteAccepted(t, ctx, attachMCPRoute(t, ctx, toolsNS, gwNS, gwName), "mcp-tools")

	const name = "toolcaller"
	a := &assaydv1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "assayd-e2e"},
		Spec: assaydv1alpha1.AgentSpec{Runtime: &assaydv1alpha1.AgentRuntime{
			Image: img,
			Env: []corev1.EnvVar{
				{Name: "AGENT_NAME", Value: name},
				// The tool route's hostname. The gateway address is NOT set here:
				// it is the operator's to inject, and this test asserts it did.
				{Name: "MCP_TOOL_HOST", Value: "mcp.assayd.test"},
			},
		}},
	}
	// Wait for any previous run's Agent AND its workload to be GONE before
	// creating this one. The same spec mints the same revision, so a leftover
	// Deployment keeps its name, stays "available" on its old Pods, and answers
	// with whatever those Pods were given. The first version of this test did
	// not wait, and a mutation that stopped the chart passing --gateway-url
	// SURVIVED: the request reached a Pod the previous run's operator had
	// injected.
	_ = k8s.Delete(ctx, a)
	waitAgentGone(t, ctx, "assayd-e2e", name)
	if err := k8s.Create(ctx, a); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	t.Cleanup(func() { _ = k8s.Delete(context.Background(), a) })
	wl := controller.WorkloadName(name, revision.MustHash(a.Spec))
	waitAvailable(t, ctx, wl, 4*time.Minute)
	assertRouteAccepted(t, ctx, waitForEmittedRoute(t, ctx, name, 2*time.Minute), wl)

	// The operator's injection, read off the workload THIS run's operator
	// rendered, before anything is asked of the agent.
	var dep appsv1.Deployment
	if err := k8s.Get(ctx, client.ObjectKey{Namespace: controller.RunNamespaceName("assayd-e2e"), Name: wl},
		&dep); err != nil {
		t.Fatalf("read the agent's workload: %v", err)
	}
	injected := ""
	for _, c := range dep.Spec.Template.Spec.Containers {
		for _, e := range c.Env {
			if e.Name == controller.EnvGatewayURL {
				injected = e.Value
			}
		}
	}
	if injected != gwURL {
		t.Fatalf("the operator rendered %s=%q into the agent's workload; the chart was given %q. "+
			"Without it the agent has no address for tools at all", controller.EnvGatewayURL, injected, gwURL)
	}

	url := fmt.Sprintf("http://%s.%s.svc.cluster.local:8080", gatewayService(t, ctx, gwNS, gwName), gwNS) +
		a2aSendMessage
	host := emittedHostname(t, name, "assayd-e2e")
	ask := func(tag, tool, text string) toolTask {
		body := fmt.Sprintf(`{"message":{"messageId":"e2e-%d","role":"ROLE_USER","parts":[{"text":%q}],`+
			`"metadata":{"tool":%q}}}`, time.Now().UnixNano(), text, tool)
		return parseToolTask(t, httpInClusterHost(t, ctx, tag, url, host, body))
	}

	// The MCP backend answers 503 until it resolves a target, and the agent
	// reports that as a FAILED task — so the first completion is waited for.
	var got toolTask
	deadline := time.Now().Add(2 * time.Minute)
	for i := 0; time.Now().Before(deadline); i++ {
		if got = ask(fmt.Sprintf("tool-%d", i), "echo_text", "from the agent"); got.State == "TASK_STATE_COMPLETED" {
			break
		}
		time.Sleep(5 * time.Second)
	}
	if got.State != "TASK_STATE_COMPLETED" {
		t.Fatalf("the agent never completed a task by calling a tool through the gateway: %+v", got)
	}
	if got.Artifact != "tool echo_text: echo: from the agent" {
		t.Errorf("the task's artifact is %q, not the tool's answer relayed by the agent", got.Artifact)
	}
	if got.Agent != name {
		t.Errorf("the task was completed by %q, not %q", got.Agent, name)
	}
	// The operator's injection reached the agent, and it is the address the
	// call went to.
	if got.Gateway != gwURL {
		t.Errorf("the agent reports ASSAYD_GATEWAY_URL=%q; the chart was given %q, so the operator's "+
			"injection did not reach it", got.Gateway, gwURL)
	}

	// The gateway's allowlist, and the tool it excludes. The fixture server
	// answers delete_everything normally, so a refusal can only be the gateway's.
	applyToolAllowlist(t, ctx, toolsNS, backend, `mcp.tool.name == "echo_text"`)
	deadline = time.Now().Add(2 * time.Minute)
	for i := 0; time.Now().Before(deadline); i++ {
		if got = ask(fmt.Sprintf("tool-refused-%d", i), "delete_everything", "x"); got.State == "TASK_STATE_FAILED" &&
			strings.Contains(got.StatusText, "delete_everything") {
			break
		}
		time.Sleep(5 * time.Second)
	}
	if got.State != "TASK_STATE_FAILED" || !strings.Contains(got.StatusText, "Unknown tool: delete_everything") {
		t.Errorf("under an allowlist naming only echo_text the agent's delete_everything task is %+v; "+
			"want TASK_STATE_FAILED carrying the gateway's `Unknown tool` refusal", got)
	}
	if got = ask("tool-allowed", "echo_text", "still allowed"); got.State != "TASK_STATE_COMPLETED" ||
		got.Artifact != "tool echo_text: echo: still allowed" {
		t.Errorf("the allowed tool stopped working under the allowlist: %+v", got)
	}
}

// waitAgentGone waits until an Agent is deleted and no workload of it remains in
// its run namespace, so a test that recreates it measures a fresh revision.
func waitAgentGone(t *testing.T, ctx context.Context, ns, name string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		var a assaydv1alpha1.Agent
		agentGone := apierrors.IsNotFound(k8s.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, &a))
		var deps appsv1.DeploymentList
		_ = k8s.List(ctx, &deps, client.InNamespace(controller.RunNamespaceName(ns)),
			client.MatchingLabels{controller.LabelAgent: name})
		if agentGone && len(deps.Items) == 0 {
			return
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("a previous Agent %s/%s or its workload never went away", ns, name)
}

// toolTask is what a SendMessageResponse says about a call_tool task.
type toolTask struct {
	State, Artifact, StatusText, Agent, Gateway string
}

func parseToolTask(t *testing.T, body string) toolTask {
	t.Helper()
	var out struct {
		Task *struct {
			Status struct {
				State   string
				Message *struct{ Parts []struct{ Text string } }
			}
			Artifacts []struct{ Parts []struct{ Text string } }
			Metadata  map[string]string
		}
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil || out.Task == nil {
		t.Fatalf("the answer is not a SendMessageResponse carrying a task: %v\n%s", err, body)
	}
	tt := toolTask{State: out.Task.Status.State, Agent: out.Task.Metadata["agent"],
		Gateway: out.Task.Metadata["gateway"]}
	if len(out.Task.Artifacts) > 0 && len(out.Task.Artifacts[0].Parts) > 0 {
		tt.Artifact = out.Task.Artifacts[0].Parts[0].Text
	}
	if m := out.Task.Status.Message; m != nil && len(m.Parts) > 0 {
		tt.StatusText = m.Parts[0].Text
	}
	return tt
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
