package e2e

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
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
// **Hand-authored, per ADR-0030, and not the compiler.** Design 03 is RE-OPENED
// and its policy compiler does not exist; nothing in assayd emits an
// AgentgatewayPolicy, and after this test nothing still does. What this
// establishes is that the mapping the compiler will have to produce is one the
// gateway actually honours — the 401/403 split included — so it can be written
// against a measurement rather than a schema reading.
func TestTheGatewayRefusesADisallowedPrincipal(t *testing.T) {
	requireCluster(t)
	requireOperator(t)
	img := responderImage(t)
	gwNS, gwName := os.Getenv("ASSAYD_E2E_GATEWAY_NS"), os.Getenv("ASSAYD_E2E_GATEWAY_NAME")
	if gwNS == "" || gwName == "" {
		t.Fatal("ASSAYD_E2E_GATEWAY_NS/NAME unset; there is no gateway to enforce identity at")
	}
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
	runNS := controller.RunNamespaceName("assayd-e2e")
	host := name + ".assayd.test"

	route := attachRoute(t, ctx, runNS, wl, gwNS, gwName, host)
	assertRouteAccepted(t, ctx, route, wl)

	// Two principals, distinguished only by the metadata their credential
	// carries. Same route, same agent, same moment — so a difference in outcome
	// can only be the authorization rule.
	const permittedKey, refusedKey = "assayd-e2e-permitted", "assayd-e2e-refused"
	applyAPIKeys(t, ctx, runNS, map[string]string{
		permittedKey: "trusted",
		refusedKey:   "rogue",
	})
	policy := applyAuthzPolicy(t, ctx, runNS, wl, `apiKey.group == "trusted"`)
	assertPolicyAttached(t, ctx, policy)

	gwSvc := gatewayService(t, ctx, gwNS, gwName)
	url := fmt.Sprintf("http://%s.%s.svc.cluster.local:8080/assayd-test/echo", gwSvc, gwNS)
	const body = `{"message":{"parts":[{"text":"who am i"}]}}`

	// The policy is Attached before it is ENFORCING — the gateway is configured
	// through its own control plane, so there is a window in which the route is
	// live and unauthenticated requests still succeed. Waiting for the anonymous
	// refusal is what makes the assertions below about identity rather than about
	// timing: until this returns 401, a 200 for the permitted key proves nothing.
	waitForEnforcement(t, ctx, url, host, body)

	if code := probeCode(t, ctx, "authz-none", url, host, "", body); code != "401" {
		t.Errorf("an anonymous caller got %s, want 401 — with an Allow rule configured, "+
			"agentgateway denies unless a rule matches, and an unauthenticated caller "+
			"matches nothing", code)
	}

	// The disallowed principal. Not a bad credential — a GOOD one, belonging to
	// someone this route does not admit. 403 and not 401 is the whole distinction
	// between "I do not know you" and "I know you and no".
	if code := probeCode(t, ctx, "authz-refused", url, host, refusedKey, body); code != "403" {
		t.Errorf("the disallowed principal got %s, want 403. A valid credential for the "+
			"wrong group must be authenticated and then refused; anything else means the "+
			"gateway is not deciding on identity", code)
	}

	// The permitted control, on the same route in the same second.
	if code := probeCode(t, ctx, "authz-ok", url, host, permittedKey, body); code != "200" {
		t.Fatalf("the PERMITTED principal got %s, want 200 — the rule refuses the caller it "+
			"is supposed to admit, so the refusals above prove only that the route is shut", code)
	}
	answer := httpInClusterHostKey(t, ctx, "authz-body", url, host, permittedKey, body)
	if !strings.Contains(answer, `"agent":"`+name+`"`) {
		t.Errorf("the permitted principal got a 200 that is not the agent's answer, so the "+
			"gateway admitted the request without delivering it: %s", answer)
	}
}

// applyAPIKeys writes the credential set as agentgateway reads it: a ConfigMap
// whose every entry is a JSON object with a `keyHash` and arbitrary `metadata`.
//
// ConfigMaps are not confidential, so the API refuses a raw key here and takes
// only `sha256:<hex>` — which is why the test hashes rather than storing the
// credential it is about to present. A real deployment would use secretRef; this
// is a fixture whose keys are literals in a test file and are not secrets.
func applyAPIKeys(t *testing.T, ctx context.Context, runNS string, keys map[string]string) {
	t.Helper()
	data := map[string]string{}
	for key, group := range keys {
		sum := sha256.Sum256([]byte(key))
		data[key] = fmt.Sprintf(`{"keyHash":"sha256:%s","metadata":{"group":%q}}`,
			hex.EncodeToString(sum[:]), group)
	}
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "assayd-e2e-api-keys",
			Namespace: runNS,
			Labels:    map[string]string{"assayd.dev/api-keys": "e2e"},
		},
		Data: data,
	}
	_ = k8s.Delete(ctx, cm)
	if err := k8s.Create(ctx, cm); err != nil {
		t.Fatalf("write the API key set: %v", err)
	}
	t.Cleanup(func() { _ = k8s.Delete(context.Background(), cm) })
}

// applyAuthzPolicy attaches authentication and one authorization rule to the
// route. The policy lives in the run namespace because AgentgatewayPolicy's
// targetRefs "must be in the same namespace as the policy" — which is also
// where design 03 3.2 puts it, for the SAME reason and not a different one: its
// table gives "a policy attaches to a route; they cannot be split across
// namespaces" (03:161). The ReferenceGrant argument is the HTTPRoute row's, one
// line above it.
func applyAuthzPolicy(t *testing.T, ctx context.Context, runNS, wl, expr string) *unstructured.Unstructured {
	t.Helper()
	build := func() *unstructured.Unstructured {
		return &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "agentgateway.dev/v1alpha1",
			"kind":       "AgentgatewayPolicy",
			"metadata":   map[string]any{"name": "assayd-e2e-authz", "namespace": runNS},
			"spec": map[string]any{
				"targetRefs": []any{map[string]any{
					"group": "gateway.networking.k8s.io", "kind": "HTTPRoute", "name": wl,
				}},
				"traffic": map[string]any{
					"apiKeyAuthentication": map[string]any{
						"configMapSelector": map[string]any{
							"matchLabels": map[string]any{"assayd.dev/api-keys": "e2e"},
						},
					},
					// action Allow, and with any Allow rule configured agentgateway
					// denies whatever matches none of them. That default-deny is the
					// property being relied on, so it is stated rather than assumed.
					"authorization": map[string]any{
						"action": "Allow",
						"policy": map[string]any{"matchExpressions": []any{expr}},
					},
				},
			},
		}}
	}
	p := build()
	_ = k8s.Delete(ctx, build())
	if err := k8s.Create(ctx, p); err != nil {
		t.Fatalf("apply the authorization policy: %v", err)
	}
	t.Cleanup(func() { _ = k8s.Delete(context.Background(), build()) })
	return p
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
		"curl -sS --max-time 15 -X POST -H 'Host: %s' -H 'Content-Type: application/json' %s"+
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
			"-X POST -H 'Host: %s' -H 'Authorization: Bearer %s' -H 'Content-Type: application/json' "+
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
