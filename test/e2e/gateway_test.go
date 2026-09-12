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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/compiler"
	"github.com/Quinyte/assayd/internal/controller"
	"github.com/Quinyte/assayd/internal/revision"
)

// ADR-0030 step 3, BOTH halves: author and execute the exact resources, then
// encode the mapping.
//
// The first half was design 07 A6: this test hand-authored its route while
// impersonating the operator's ServiceAccount, and an agent answered through a
// real Gateway. That proved the path and the identity reservation and — in
// A6.5's own words — "proves nothing about a compiler that does not exist".
//
// **The route is now EMITTED by the operator.** The test creates an Agent and
// waits; nothing here writes an HTTPRoute. What is emitted is one object, the
// serving route, and that is the whole of what was encoded: no
// AgentgatewayPolicy, no AgentgatewayBackend, no budget, no rate limit, no
// authentication, no tool allowlist. The request below is unauthenticated and
// the Agent says so, under GovernanceSkipped=PolicyCompilerAbsent.
//
// The negative control is KEPT, and is not decorative: it is what caught
// A6.2's fail-open reservation. It asserts the refusal NAMES
// `assayd-gateway-routes`, because "it was refused" is not evidence about
// which rule refused it.
func TestAnAgentAnswersThroughTheGateway(t *testing.T) {
	requireCluster(t)
	requireOperator(t)
	img := responderImage(t)
	gwNS, gwName := requireGateway(t)
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

	// **Design 07 A5.9's reservation is real, and this is where it was measured.**
	// The first version of this test authored the route as the test's own
	// identity and the API server refused it: "routes attaching to the assayd
	// Gateway are authored by the assayd operator only". That was the platform
	// working — a route attaching to this Gateway is a capability grant, and
	// anyone who could author one could publish an ungoverned path to any
	// workload. It is kept as the negative control, because a reservation
	// nothing tests is a reservation that will quietly lapse — and because it is
	// what caught A6.2's silently fail-open comparison.
	//
	// It names a route the operator would never emit, so that a refusal here is
	// about the AUTHOR and not about a name already taken.
	impostor := func() *unstructured.Unstructured {
		return &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "gateway.networking.k8s.io/v1",
			"kind":       "HTTPRoute",
			"metadata":   map[string]any{"name": "impostor-" + wl, "namespace": runNS},
			"spec": map[string]any{
				"parentRefs": []any{map[string]any{
					"name": gwName, "namespace": gwNS, "sectionName": "http",
				}},
				"hostnames": []any{"impostor." + name + ".assayd.test"},
				"rules": []any{map[string]any{
					"backendRefs": []any{map[string]any{"name": wl, "port": int64(8080)}},
				}},
			},
		}}
	}
	if err := k8s.Create(ctx, impostor()); err == nil {
		_ = k8s.Delete(ctx, impostor())
		t.Fatal("a non-operator identity attached a route to the assayd Gateway. Design 07 " +
			"A5.9 reserves that to the operator, because a route attaching here publishes a " +
			"path to a workload and anyone who can author one can publish an ungoverned path")
	} else if !strings.Contains(err.Error(), "assayd-gateway-routes") {
		t.Fatalf("the route was refused, but not by the reservation policy — so this proves "+
			"nothing about which rule refused it: %v", err)
	}

	// The route the OPERATOR emitted. Nothing in this test authors it: the
	// resource is waited for, not written, and if the operator emits none this
	// fails here rather than forty seconds later on a 404.
	route := waitForEmittedRoute(t, ctx, name, 2*time.Minute)
	if got := route.GetLabels()[controller.LabelAgentUID]; got != string(agentUID(t, ctx, "assayd-e2e", name)) {
		t.Errorf("the emitted route carries agent-uid %q, which is not this Agent's", got)
	}
	assertEmittedHostname(t, route, name, "assayd-e2e")

	// The listener must ACCEPT it. A route in a namespace the listener does not
	// admit is rejected, and the failure is silent from the client's side — it
	// simply 404s — so this is asserted before any request is sent.
	assertRouteAccepted(t, ctx, route, wl)

	// Through the Gateway's own Service, addressed by the hostname the OPERATOR
	// chose — derived here the same way the operator derives it, so a change to
	// either side breaks this rather than silently routing somewhere else.
	// Nothing here talks to the revision Service directly; if the gateway is not
	// carrying the traffic, this cannot pass.
	host := emittedHostname(t, name, "assayd-e2e")
	gwSvc := gatewayService(t, ctx, gwNS, gwName)
	url := fmt.Sprintf("http://%s.%s.svc.cluster.local:8080", gwSvc, gwNS) + a2aSendMessage
	body := httpInClusterHost(t, ctx, "gwask", url, host, sendMessage("through the gateway"))

	// An A2A task, completed, through the gateway: ADR-0030's clause, literally.
	agent, text := completedTask(t, body)
	if agent != name {
		t.Errorf("the task was completed by %q, not the agent behind the route; body %s", agent, body)
	}
	if !strings.Contains(text, "through the gateway") {
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

// requireGateway returns the Gateway the harness installed, or SKIPS.
//
// It skips rather than fails because a gateway-dependent test on a lane with no
// gateway is reporting on the harness, not on the platform — the same reason
// `requireCluster` and the responder tests skip. The kind lane caught this: these
// tests were written against k3d, where the harness installs Gateway API and
// agentgateway, and on kind they failed for want of a dependency that was never
// meant to be there. A red check for an unverified axis trains people to ignore
// red checks.
func requireGateway(t *testing.T) (ns, name string) {
	t.Helper()
	ns, name = os.Getenv("ASSAYD_E2E_GATEWAY_NS"), os.Getenv("ASSAYD_E2E_GATEWAY_NAME")
	if ns != "" && name != "" {
		return ns, name
	}
	if reason := os.Getenv("ASSAYD_E2E_GATEWAY_SKIP"); reason != "" {
		t.Skip("gateway path UNVERIFIED, not passed: " + reason)
	}
	t.Fatal("ASSAYD_E2E_GATEWAY_NS/NAME unset and no ASSAYD_E2E_GATEWAY_SKIP reason given. " +
		"`make e2e` installs agentgateway and creates the Gateway on k3d; set " +
		"ASSAYD_E2E_GATEWAY=0 to skip that deliberately.")
	return "", ""
}

// emittedHostname is the host the operator's route matches, COMPOSED HERE and
// deliberately not by calling the operator's own Hostname().
//
// Deriving it from the code under test was the first version and it was wrong:
// a mutation that changed what the operator emits changed what the test asked
// for in the same motion, so the two agreed on a hostname that was nobody's and
// the run stayed green. The suffix comes from the environment, because
// hack/e2e.sh is what told the chart; the SHAPE is spelled out, because that is
// the claim.
func emittedHostname(t *testing.T, agentName, agentNS string) string {
	t.Helper()
	suffix := os.Getenv("ASSAYD_E2E_GATEWAY_HOSTNAME_SUFFIX")
	if suffix == "" {
		// No fallback to the operator's own default. A fallback re-opens the
		// trap the comment above describes from the other side: the harness
		// tells the chart the suffix, so a run where the harness said nothing
		// and the test guessed would agree with a binary nobody configured.
		t.Fatal("ASSAYD_E2E_GATEWAY_HOSTNAME_SUFFIX is unset on a lane that has a Gateway. " +
			"hack/e2e.sh sets it beside the --set it passes to the chart; without it this " +
			"test would guess the hostname the operator was configured with.")
	}
	return agentName + "." + agentNS + "." + suffix
}

// assertEmittedHostname reads the host off the object the operator wrote and
// holds it to the shape above, so a drift is a named failure rather than a
// request that quietly goes nowhere.
func assertEmittedHostname(t *testing.T, route *unstructured.Unstructured, agentName, agentNS string) {
	t.Helper()
	hosts, _, _ := unstructured.NestedStringSlice(route.Object, "spec", "hostnames")
	want := emittedHostname(t, agentName, agentNS)
	if len(hosts) != 1 || hosts[0] != want {
		t.Fatalf("the emitted route matches %v, want exactly [%s]. A route with NO hostname "+
			"matches every request on the listener, so every Agent would answer for every "+
			"other; a route with the wrong one answers for nobody.", hosts, want)
	}
}

// emittedRouteKey locates the object by calling the operator's own naming
// function, which is the code under test — so this helper cannot detect a change
// to the name SHAPE. That is pinned elsewhere, and only in unit tests: by the
// literal `agent-serving` in internal/compiler's
// TestEmittedNameIsADNSLabelAndInjectiveAtTheLimit, and by `pricer-serving` in
// the golden files under internal/compiler/testdata and
// internal/controller/testdata.
func emittedRouteKey(t *testing.T, agentName string) types.NamespacedName {
	t.Helper()
	n, err := compiler.ServingRouteName(agentName)
	if err != nil {
		t.Fatalf("serving route name for %s: %v", agentName, err)
	}
	return types.NamespacedName{Namespace: runNS, Name: n}
}

func getEmittedRoute(ctx context.Context, t *testing.T, agentName string) (*unstructured.Unstructured, error) {
	t.Helper()
	var rt unstructured.Unstructured
	rt.SetGroupVersionKind(httpRouteGVK)
	err := k8s.Get(ctx, emittedRouteKey(t, agentName), &rt)
	return &rt, err
}

// waitForEmittedRoute polls for the operator's route. It never creates one: a
// helper that fell back to authoring the route would turn "the operator does
// not emit" into a green run, which is the whole failure this change exists to
// make impossible.
func waitForEmittedRoute(t *testing.T, ctx context.Context, agentName string, d time.Duration) *unstructured.Unstructured {
	t.Helper()
	key := emittedRouteKey(t, agentName)
	deadline := time.Now().Add(d)
	var last error
	for time.Now().Before(deadline) {
		rt, err := getEmittedRoute(ctx, t, agentName)
		if err == nil {
			return rt
		}
		last = err
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("the operator emitted no HTTPRoute %s within %s (%v). ADR-0030 step 3's second "+
		"half is exactly this object; the chart was installed with gateway.enabled=true, so "+
		"either the operator is not reading the flag or it is not emitting.", key, d, last)
	return nil
}

var httpRouteGVK = schema.GroupVersionKind{
	Group: "gateway.networking.k8s.io", Version: "v1", Kind: "HTTPRoute",
}

func agentUID(t *testing.T, ctx context.Context, ns, name string) types.UID {
	t.Helper()
	var a assaydv1alpha1.Agent
	if err := k8s.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, &a); err != nil {
		t.Fatalf("get agent %s/%s: %v", ns, name, err)
	}
	return a.UID
}

// The route is the OPERATOR's, and the proof is that it comes back.
//
// A route that merely EXISTS beside a gateway-enabled operator is not evidence
// the operator made it — this suite hand-authored one for weeks. Deleting it
// and watching it return is a transition, and a transition is what a value
// assertion cannot fake: nothing else in this cluster writes an HTTPRoute into
// a run namespace, and A5.9's policy means nothing else may.
func TestTheOperatorRecreatesARouteThatWasDeleted(t *testing.T) {
	requireCluster(t)
	requireOperator(t)
	img := responderImage(t)
	requireGateway(t)
	ctx := context.Background()
	ensureNamespace(t, ctx, "assayd-e2e")

	const name = "recreated"
	wl := deployResponder(t, ctx, name, img)
	_ = wl

	before := waitForEmittedRoute(t, ctx, name, 2*time.Minute)
	// Deleted as the suite's own identity, which is cluster-admin.
	//
	// An earlier version impersonated the operator here and said A5.9 refuses an
	// ordinary identity's delete. **It does not**: `assayd-gateway-routes`
	// matches `operations: ["CREATE", "UPDATE"]` and says nothing about DELETE,
	// so who may delete a route is an RBAC question and not that policy's. The
	// impersonation was unnecessary and the reason given for it was false —
	// which is worse in a test than in prose, because a test comment gets cited
	// as evidence about the policy. Whether the reservation SHOULD cover DELETE
	// is a real question for A5.9 and is recorded in A6.10, not decided here.
	if err := k8s.Delete(ctx, before); err != nil {
		t.Fatalf("delete the emitted route: %v", err)
	}
	if _, err := getEmittedRoute(ctx, t, name); err == nil {
		t.Fatal("the route was still present immediately after a successful delete; " +
			"this test cannot distinguish a recreate from a delete that did nothing")
	}

	after := waitForEmittedRoute(t, ctx, name, 2*time.Minute)
	assertEmittedHostname(t, after, name, "assayd-e2e")
	if after.GetUID() == before.GetUID() {
		t.Fatalf("the route came back with the same UID (%s), so it was never actually gone",
			after.GetUID())
	}
	assertRouteAccepted(t, ctx, after, "")

	// And it still carries traffic, so the recreated object is the real thing
	// rather than a shell with the right name.
	gwNS, gwName := requireGateway(t)
	gwSvc := gatewayService(t, ctx, gwNS, gwName)
	url := fmt.Sprintf("http://%s.%s.svc.cluster.local:8080", gwSvc, gwNS) + a2aSendMessage
	body := httpInClusterHost(t, ctx, "recreated", url, emittedHostname(t, name, "assayd-e2e"),
		sendMessage("after the delete"))
	if _, text := completedTask(t, body); !strings.Contains(text, "after the delete") {
		t.Errorf("the recreated route did not carry the request: %s", body)
	}
}

// Design 03 §3.1's `false` row, on a real cluster: the declared-ungoverned tier
// emits nothing and says so on every Agent.
//
// It runs on the lanes that have NO gateway — kind, and `ASSAYD_E2E_GATEWAY=0`
// on k3d — because those are the lanes where `gateway.enabled` is false, which
// is what P1 ships. It skips where the gateway IS enabled rather than failing:
// the two rows of the table cannot both be true of one install.
func TestTheDeclaredUngovernedTierEmitsNoRoute(t *testing.T) {
	requireCluster(t)
	requireOperator(t)
	if os.Getenv("ASSAYD_E2E_GATEWAY_NS") != "" {
		t.Skip("this lane installed the chart with gateway.enabled=true; the declared-off row " +
			"is measured on the lanes that ship P1's default (kind, or ASSAYD_E2E_GATEWAY=0)")
	}
	ctx := context.Background()
	ensureNamespace(t, ctx, "assayd-e2e")

	const name = "ungoverned"
	a := &assaydv1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "assayd-e2e"},
		Spec: assaydv1alpha1.AgentSpec{
			Runtime: &assaydv1alpha1.AgentRuntime{
				// A digest-pinned reference the CRD accepts. Nothing is asked to
				// RUN here — the condition under test is set before any replica is
				// available — so this lane needs no registry and no responder.
				Image: "registry.k8s.io/pause@sha256:" +
					"7031c1b283388d2c2e09b57badb803c05ebed362dc88d84b480cc47f72a21097",
			},
		},
	}
	// Deleted and WAITED FOR. An Agent from an earlier run is held by its
	// finalizer for as long as its teardown takes, so a create issued straight
	// after the delete fails with AlreadyExists and the run reports a product
	// bug where it has a fixture race.
	_ = k8s.Delete(ctx, a)
	gone := time.Now().Add(2 * time.Minute)
	for time.Now().Before(gone) {
		var prev assaydv1alpha1.Agent
		if err := k8s.Get(ctx, types.NamespacedName{Namespace: "assayd-e2e", Name: name}, &prev); apierrors.IsNotFound(err) {
			break
		}
		time.Sleep(2 * time.Second)
	}
	if err := k8s.Create(ctx, a); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	t.Cleanup(func() { _ = k8s.Delete(context.Background(), a) })

	// The condition, first: it is what an operator reads, and NFR-8 forbids a
	// degraded path being silent.
	deadline := time.Now().Add(2 * time.Minute)
	var last []metav1.Condition
	for time.Now().Before(deadline) {
		var live assaydv1alpha1.Agent
		if err := k8s.Get(ctx, types.NamespacedName{Namespace: "assayd-e2e", Name: name}, &live); err == nil {
			last = live.Status.Conditions
			for _, c := range last {
				if c.Type != string(assaydv1alpha1.CondGovernanceSkipped) {
					continue
				}
				// The literal design 03 §3.1's table writes, not the constant:
				// comparing the constant to itself would let a rename pass here
				// and at every other layer at the same time.
				if c.Status != metav1.ConditionTrue || c.Reason != "GatewayDisabled" {
					t.Fatalf("GovernanceSkipped is %s/%s; design 03 §3.1's table says "+
						"True/GatewayDisabled on the declared-ungoverned tier", c.Status, c.Reason)
				}
				goto found
			}
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("no GovernanceSkipped condition appeared on an Agent in a gateway.enabled=false "+
		"install. That is the row P1 ships, and a tier nothing announces is the silence NFR-8 "+
		"forbids; conditions=%+v", last)
found:

	// And no route. On a lane with no Gateway API CRDs at all there is no kind
	// to list, which IS "no route" — reported as such rather than as an error,
	// because the claim is about what the operator emitted.
	var routes unstructured.UnstructuredList
	routes.SetGroupVersionKind(schema.GroupVersionKind{
		Group: "gateway.networking.k8s.io", Version: "v1", Kind: "HTTPRouteList"})
	err := k8s.List(ctx, &routes, client.InNamespace(runNS))
	switch {
	case err != nil && (meta.IsNoMatchError(err) || apierrors.IsNotFound(err)):
		// No HTTPRoute kind on this cluster. Nothing was emitted, by construction.
	case err != nil:
		t.Fatalf("list routes in %s: %v", runNS, err)
	case len(routes.Items) != 0:
		names := make([]string, 0, len(routes.Items))
		for i := range routes.Items {
			names = append(names, routes.Items[i].GetName())
		}
		t.Errorf("the declared-ungoverned tier has %d route(s) in %s (%v). `gateway.enabled: "+
			"false` means the compiler does not run at all (design 03 §3.1).",
			len(routes.Items), runNS, names)
	}
}
