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
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/controller"
	"github.com/Quinyte/assayd/internal/revision"
)

// ADR-0030 step 3, the network-rules leg: **authored by hand before it is
// compiled.**
//
// Governance at the gateway is theatre while the gateway is bypassable, and
// until this test the repository proved the opposite of what it wanted:
// TestAnAgentAnswersARequestThroughItsRevisionService reaches an agent from an
// ordinary namespace, directly, with no gateway involved. That test is correct
// and stays — it proves the workload half — but on its own it demonstrates that
// nothing in the platform forces traffic through the governed path.
//
// **This does NOT implement design 07 A5.4.** A5.4's policy is materialized by
// the OPERATOR into every run namespace, is shaped by values, and carries an
// egress matrix (gateway, DNS, and the tenant's NATS endpoint) that nothing
// here needs because no agent in this suite talks to JetStream. The operator
// still materializes nothing and the ClusterRole still grants no
// `networkpolicies`, which is rule 7 held: no verb ahead of the code that uses
// it. What this test does is execute A5.4's INGRESS half by hand, so that the
// eventual materializer is written against something that was observed to work
// rather than against a paragraph.
func TestTheGatewayIsTheOnlyWayIn(t *testing.T) {
	requireCluster(t)
	requireOperator(t)
	img := responderImage(t)

	// A5.4: "a run that cannot observe a blocked connection reports the axis as
	// unverified rather than green". Skipping is the honest result on a CNI that
	// accepts the policy and ignores it — a pass there would be a lie about the
	// one thing this test exists to establish.
	if os.Getenv("ASSAYD_E2E_NETPOL_ENFORCED") == "" {
		reason := os.Getenv("ASSAYD_E2E_NETPOL_SKIP")
		if reason == "" {
			reason = "ASSAYD_E2E_NETPOL_ENFORCED is unset; `make e2e` sets it on the k3d lane only"
		}
		t.Skip("NetworkPolicy enforcement UNVERIFIED, not passed: " + reason)
	}

	gwNS := os.Getenv("ASSAYD_E2E_GATEWAY_NS")
	gwName := os.Getenv("ASSAYD_E2E_GATEWAY_NAME")
	if gwNS == "" || gwName == "" {
		t.Fatal("ASSAYD_E2E_GATEWAY_NS/NAME unset; this test needs a real Gateway to be the " +
			"permitted path, and proving a denial without one would prove only that the " +
			"agent is unreachable")
	}

	ctx := context.Background()
	ensureNamespace(t, ctx, "assayd-e2e")

	const name = "onlygw"
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
	direct := fmt.Sprintf("http://%s.%s.svc.cluster.local:8080/healthz", wl, runNS)

	// The baseline matters more than it looks. Without it a denial proves only
	// that the agent was unreachable, which is also what a broken image, a
	// missing Service or a typo in the URL produce — and each of those would
	// make this test pass while measuring nothing.
	if code := probeSettled(t, ctx, "npbase", direct); code != "200" {
		t.Fatalf("the agent is not reachable directly BEFORE any policy (code=%s), so a "+
			"denial afterwards would prove nothing about the policy", code)
	}

	applyIngressPolicy(t, ctx, runNS, gwNS, "assayd-system")

	// 1. The permitted control: the gateway still gets through.
	route := attachRoute(t, ctx, runNS, wl, gwNS, gwName, name+".assayd.test")
	assertRouteAccepted(t, ctx, route, wl)
	gwSvc := gatewayService(t, ctx, gwNS, gwName)
	body := httpInClusterHost(t, ctx, "npgw",
		fmt.Sprintf("http://%s.%s.svc.cluster.local:8080/assayd-test/echo", gwSvc, gwNS),
		name+".assayd.test", `{"message":{"parts":[{"text":"through the only way in"}]}}`)
	if !strings.Contains(body, `"agent":"`+name+`"`) {
		t.Fatalf("the gateway did not answer while the policy was on, so the policy blocks the "+
			"path it is supposed to permit: %s", body)
	}

	// 2. The disallowed principal: the same agent, addressed directly, from an
	//    ordinary namespace. This is ADR-0030's stop criterion for the slice —
	//    "a disallowed principal failing against a permitted control" — and it is
	//    the first time this repository has measured one.
	if code := probeSettled(t, ctx, "npdirect", direct); code == "200" {
		t.Fatalf("an ordinary namespace reached the agent directly (code=%s) with the policy "+
			"applied. The gateway is bypassable, so nothing it enforces is a control", code)
	}

	// 3. A5.4's other required path, and the one a naive "gateway only" policy
	//    breaks: the OPERATOR fetches the Agent Card in-cluster and is not a
	//    gateway Pod. A policy that forgets it makes every candidate fail
	//    registration and looks like a card bug rather than a network rule.
	//
	//    Asserted with a SECOND agent, created while the policy is on. The first
	//    agent registered its card before the policy existed, so its status
	//    proves only what was true earlier — re-reading it would be reading a
	//    stale success and calling it a live one.
	assertOperatorRegistersUnderPolicy(t, ctx, img)
}

// assertOperatorRegistersUnderPolicy creates an agent while the ingress policy
// is in force and requires the operator to fetch and record its card. The
// operator reaches the candidate directly, from assayd-system, on the same port
// this policy governs (design 02 3.4), so this fails if the operator peer is
// missing or wrong.
func assertOperatorRegistersUnderPolicy(t *testing.T, ctx context.Context, img string) {
	t.Helper()
	const name = "underpolicy"
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
		t.Fatalf("create the under-policy agent: %v", err)
	}
	t.Cleanup(func() { _ = k8s.Delete(context.Background(), a) })

	rev := revision.MustHash(a.Spec)
	waitAvailable(t, ctx, controller.WorkloadName(name, rev), 4*time.Minute)

	var live assaydv1alpha1.Agent
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		if err := k8s.Get(ctx, types.NamespacedName{Namespace: "assayd-e2e", Name: name}, &live); err == nil {
			// A registered card, not merely an attempt: a FAILED fetch also writes an
			// entry, with an empty digest, and that is exactly what a policy blocking
			// the operator would produce. Breaking on any entry would turn this test
			// green on the failure it exists to catch.
			if len(live.Status.Cards) > 0 && live.Status.Cards[0].Digest != "" {
				return
			}
		}
		time.Sleep(3 * time.Second)
	}
	t.Fatalf("the operator never registered a card while the ingress policy was in force, so "+
		"the policy blocks the operator's card fetch (design 07 A5.4: a policy that forgets "+
		"the operator makes every candidate fail registration and looks like a card bug); "+
		"cards=%+v phase=%q", live.Status.Cards, live.Status.Phase)
}

// attachRoute authors the HTTPRoute as the OPERATOR, which is the only identity
// A5.9's admission policy permits, and returns it. The gateway test proves that
// reservation; here the route is only the permitted path's plumbing.
func attachRoute(t *testing.T, ctx context.Context, runNS, wl, gwNS, gwName, hostname string) *unstructured.Unstructured {
	t.Helper()
	build := func() *unstructured.Unstructured {
		return &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "gateway.networking.k8s.io/v1",
			"kind":       "HTTPRoute",
			"metadata":   map[string]any{"name": wl, "namespace": runNS},
			"spec": map[string]any{
				"parentRefs": []any{map[string]any{
					"name": gwName, "namespace": gwNS, "sectionName": "http",
				}},
				"hostnames": []any{hostname},
				"rules": []any{map[string]any{
					"backendRefs": []any{map[string]any{"name": wl, "port": int64(8080)}},
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
	route := build()
	_ = asOperator.Delete(ctx, build())
	if err := asOperator.Create(ctx, route); err != nil {
		t.Fatalf("author the route as the operator: %v", err)
	}
	t.Cleanup(func() { _ = asOperator.Delete(context.Background(), build()) })
	return route
}

// applyIngressPolicy hand-authors design 07 A5.4's ingress half into the run
// namespace: default-deny, then the two peers A5.4 names.
//
// podSelector is empty because the run namespace holds agent Pods and nothing
// else — that is what design 02 A42 made it for. A NetworkPolicyPeer can name
// Pods, namespaces or an ipBlock and NEVER a Service, so both peers are
// namespaces, selected by the `kubernetes.io/metadata.name` label the API
// server sets on every namespace.
func applyIngressPolicy(t *testing.T, ctx context.Context, runNS, gwNS, operatorNS string) {
	t.Helper()
	port := intstr.FromInt32(8080)
	peer := func(ns string) networkingv1.NetworkPolicyPeer {
		return networkingv1.NetworkPolicyPeer{
			NamespaceSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"kubernetes.io/metadata.name": ns},
			},
		}
	}
	np := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "assayd-e2e-ingress", Namespace: runNS},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
			Ingress: []networkingv1.NetworkPolicyIngressRule{{
				From:  []networkingv1.NetworkPolicyPeer{peer(gwNS), peer(operatorNS)},
				Ports: []networkingv1.NetworkPolicyPort{{Port: &port}},
			}},
		},
	}
	_ = k8s.Delete(ctx, np)
	if err := k8s.Create(ctx, np); err != nil {
		t.Fatalf("apply the hand-authored ingress policy: %v", err)
	}
	// Deleted on the way out because the run namespace is SHARED with every other
	// test in this package. A policy left behind would deny the direct-Service
	// tests that legitimately bypass the gateway, and it would do it by timing
	// out — which reads as a broken agent, not as this test's litter.
	t.Cleanup(func() { _ = k8s.Delete(context.Background(), np) })
}

// probeSettled asks for a URL from a pod that has been running for a while, and
// returns the HTTP code as a string ("000" when the connection never completed).
//
// **The settle is the whole point, and it was measured.** kube-router programs
// a NetworkPolicy's allow rules from the SOURCE Pod's address, so a Pod that
// connects the instant it starts can be refused before its own rule exists. In
// the spike behind this file an allow-listed namespace was refused three times
// in a row that way — including with `namespaceSelector: {}`, which permits
// everything — and was served immediately from a Pod that had been up for ten
// seconds. httpInClusterHost curls on start and papers over this with retries,
// which is right for a POSITIVE assertion and silently fatal for a negative
// one: without the settle, a denial proves the race, not the policy.
func probeSettled(t *testing.T, ctx context.Context, tag, url string) string {
	t.Helper()
	const settle = 15
	// Always exits 0, so the code lands in the termination message whether the
	// request was served or refused; a non-zero exit would abort the caller and
	// a refusal is this helper's expected result half the time.
	cmd := fmt.Sprintf(
		"sleep %d; curl -sS --max-time 10 -o /dev/null -w '%%{http_code}' %s "+
			"> /dev/termination-log 2>/dev/null; exit 0", settle, url)

	name := "settled-" + tag
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
		t.Fatalf("create settled probe: %v", err)
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
	t.Fatalf("the settled probe %q never completed", name)
	return ""
}
