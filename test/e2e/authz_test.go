// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/compiler"
	"github.com/Quinyte/assayd/internal/controller"
	"github.com/Quinyte/assayd/internal/revision"
)

// ADR-0030 step 3, the identity leg — and the first time this repository has
// refused a PRINCIPAL rather than a packet.
//
// The network-rules leg (TestTheGatewayIsTheOnlyWayIn) proved the gateway
// cannot be bypassed, which is a precondition for governance and not governance
// itself: with only that policy in place, anything that reaches the gateway is
// served. This test is the other half. A caller presenting a valid credential
// that authenticates it as the wrong principal is refused **by the gateway, on
// the strength of who it is**, while a permitted principal is served on the
// same route in the same second.
//
// **The POLICY is the one the OPERATOR emitted**, `<agent>-auth`, through design
// 03's `Create` transaction (§3.3.3): API-key authentication over the key sets
// labelled `assayd.dev/api-keys: "true"`, and one CEL rule admitting the group
// named for the Agent's namespace (§3.4.4). This test used to hand-author the
// same shape, `assayd-e2e-authz`, and measured it first: 200 for the permitted
// principal, 403 for the wrong group, 401 with no key, the rule inverted
// inverting both, and authentication without authorization moving the
// wrong-group caller to 200. That measurement is what the compiler was written
// against. The hand-authored policy is gone: beside the emitted one it would be
// a second `traffic` policy on one route, and two same-level policies on one
// target resolve at random (§3.2).
//
// The ROUTE is the operator's too, and it had to be: a second route to the same
// agent would carry no policy at all.
func TestTheGatewayRefusesADisallowedPrincipal(t *testing.T) {
	requireCluster(t)
	requireOperator(t)
	img := responderImage(t)
	gwNS, gwName := requireGateway(t)
	ctx := context.Background()
	ensureNamespace(t, ctx, "assayd-e2e")

	const name = "principal"
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

	route := waitForPublishedRoute(t, ctx, name, 3*time.Minute)
	assertRouteAccepted(t, ctx, route, wl)
	assertServedUnderAPIKey(t, ctx, name)
	host := emittedHostname(t, name, "assayd-e2e")

	// The policy is the operator's, by its name and its UID label, not one this
	// test wrote.
	policyName, err := compiler.AuthPolicyName(name)
	if err != nil {
		t.Fatal(err)
	}
	policy := &unstructured.Unstructured{}
	policy.SetAPIVersion(compiler.PolicyAPIVersion)
	policy.SetKind(compiler.PolicyKind)
	if err := k8s.Get(ctx, types.NamespacedName{Namespace: runNS, Name: policyName}, policy); err != nil {
		t.Fatalf("the operator emitted no %s for a published API-key Agent: %v", policyName, err)
	}
	if got := policy.GetLabels()[compiler.LabelAgentUID]; got != string(agentUID(t, ctx, "assayd-e2e", name)) {
		t.Errorf("%s carries agent-uid %q, which is not this Agent's", policyName, got)
	}
	assertPolicyAttached(t, ctx, policy)

	// Two principals, distinguished only by the group their credential
	// carries. Same route, same agent, same moment — so a difference in
	// outcome can only be the authorization rule.
	ensureAPIKeys(t, ctx)

	gwSvc := gatewayService(t, ctx, gwNS, gwName)
	url := fmt.Sprintf("http://%s.%s.svc.cluster.local:8080", gwSvc, gwNS) + a2aSendMessage
	body := sendMessage("who am i")

	// The operator published the route only after an anonymous request got
	// 401, so the policy was enforcing on the replica that answered then.
	// Waiting again costs nothing and keeps the assertions below about identity
	// rather than timing on a gateway with more than one replica.
	waitForEnforcement(t, ctx, url, host, body)

	if code := probeCode(t, ctx, "authz-none", url, host, "", body); code != "401" {
		t.Errorf("an anonymous caller got %s, want 401 — with an Allow rule configured, "+
			"agentgateway denies unless a rule matches, and an unauthenticated caller "+
			"matches nothing", code)
	}

	// The permitted control, waited for: agentgateway reads the key set live.
	answer := askThroughGateway(t, ctx, "authz-ok", url, host, body)
	if agent, _ := completedTask(t, answer); agent != name {
		t.Errorf("the permitted principal got a 200 that is not the agent's answer, so the "+
			"gateway admitted the request without delivering it: %s", answer)
	}

	// The disallowed principal. Not a bad credential — a GOOD one, in a group
	// this route does not admit. 403 and not 401 is the whole distinction
	// between "I do not know you" and "I know you and no".
	if code := probeCode(t, ctx, "authz-refused", url, host, refusedKey, body); code != "403" {
		t.Errorf("the disallowed principal got %s, want 403. A valid credential for the "+
			"wrong group must be authenticated and then refused; anything else means the "+
			"gateway is not deciding on identity", code)
	}
}

// assertPolicyAttached requires the gateway's controller to have accepted the
// policy AND attached it to a target. Accepted alone is not enough: a policy
// whose targetRef names nothing is still valid, and it would enforce nothing
// while every assertion below it read as a successful refusal.
func assertPolicyAttached(t *testing.T, ctx context.Context, p *unstructured.Unstructured) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	var last string
	for time.Now().Before(deadline) {
		var got unstructured.Unstructured
		got.SetGroupVersionKind(p.GroupVersionKind())
		if err := k8s.Get(ctx, types.NamespacedName{
			Namespace: p.GetNamespace(), Name: p.GetName()}, &got); err == nil {
			ancestors, _, _ := unstructured.NestedSlice(got.Object, "status", "ancestors")
			for _, anc := range ancestors {
				m, ok := anc.(map[string]any)
				if !ok {
					continue
				}
				conds, _, _ := unstructured.NestedSlice(m, "conditions")
				state := map[string]string{}
				for _, c := range conds {
					cm, ok := c.(map[string]any)
					if !ok {
						continue
					}
					ct, _, _ := unstructured.NestedString(cm, "type")
					cs, _, _ := unstructured.NestedString(cm, "status")
					state[ct] = cs
				}
				last = fmt.Sprintf("%v", state)
				if state["Accepted"] == "True" && state["Attached"] == "True" {
					return
				}
			}
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("the authorization policy never reached Accepted+Attached (last: %s). An "+
		"unattached policy enforces nothing, and every refusal asserted after it would "+
		"be measuring something else", last)
}

// waitForEnforcement blocks until an anonymous request is refused, which is the
// only reliable signal that the policy has reached the data plane. Attached is a
// statement by the control plane about configuration, not about the proxy that
// serves the request.
func waitForEnforcement(t *testing.T, ctx context.Context, url, host, body string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	var last string
	for i := 0; time.Now().Before(deadline); i++ {
		last = probeCode(t, ctx, fmt.Sprintf("authz-wait-%d", i), url, host, "", body)
		if last == "401" || last == "403" {
			return
		}
		time.Sleep(5 * time.Second)
	}
	t.Fatalf("an anonymous request was still answered %s after two minutes, so the "+
		"authorization policy never took effect at the proxy", last)
}

// probeCode sends one request through the gateway and returns only the HTTP
// status. Deliberately not httpInClusterHost: that one treats a non-zero curl
// exit as a failure, and here a refusal is the expected result half the time.
func probeCode(t *testing.T, ctx context.Context, tag, url, host, apiKey, postBody string) string {
	t.Helper()
	auth := ""
	if apiKey != "" {
		auth = "-H 'Authorization: Bearer " + apiKey + "' "
	}
	cmd := fmt.Sprintf(
		"curl -sS --max-time 15 -X POST -H 'Host: %s' -H 'Content-Type: application/json' -H 'A2A-Version: 1.0' %s"+
			"-d '%s' -o /dev/null -w '%%{http_code}' %s > /dev/termination-log 2>/dev/null; exit 0",
		host, auth, postBody, url)
	return runProbe(t, ctx, "code-"+tag, cmd)
}

// httpInClusterHostKey is httpInClusterHost with a credential, for the one case
// that needs the body rather than the status.
func httpInClusterHostKey(t *testing.T, ctx context.Context, tag, url, host, apiKey, postBody string) string {
	t.Helper()
	cmd := fmt.Sprintf(
		"curl -sS --max-time 20 --retry 3 --retry-delay 2 --retry-all-errors "+
			"-X POST -H 'Host: %s' -H 'Authorization: Bearer %s' -H 'Content-Type: application/json' -H 'A2A-Version: 1.0' "+
			"-d '%s' -o /dev/termination-log %s; exit 0",
		host, apiKey, postBody, url)
	return runProbe(t, ctx, "body-"+tag, cmd)
}

// runProbe runs one shell command in a throwaway pod and returns whatever it
// left in the termination log.
func runProbe(t *testing.T, ctx context.Context, tag, cmd string) string {
	t.Helper()
	name := "probe-" + tag
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "assayd-e2e"},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{{
				Name:                     "curl",
				Image:                    "curlimages/curl:8.11.1",
				Command:                  []string{"sh", "-c", cmd},
				TerminationMessagePath:   "/dev/termination-log",
				TerminationMessagePolicy: corev1.TerminationMessageReadFile,
			}},
		},
	}
	_ = k8s.Delete(ctx, p)
	gone := time.Now().Add(time.Minute)
	for time.Now().Before(gone) {
		var scratch corev1.Pod
		if err := k8s.Get(ctx, types.NamespacedName{Namespace: "assayd-e2e", Name: name}, &scratch); err != nil {
			break
		}
		time.Sleep(time.Second)
	}
	if err := k8s.Create(ctx, p); err != nil {
		t.Fatalf("create probe %q: %v", name, err)
	}
	t.Cleanup(func() { _ = k8s.Delete(context.Background(), p) })

	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		var got corev1.Pod
		if err := k8s.Get(ctx, types.NamespacedName{Namespace: "assayd-e2e", Name: name}, &got); err == nil {
			for _, cs := range got.Status.ContainerStatuses {
				if cs.State.Terminated != nil {
					return strings.TrimSpace(cs.State.Terminated.Message)
				}
			}
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("probe %q never completed", name)
	return ""
}
