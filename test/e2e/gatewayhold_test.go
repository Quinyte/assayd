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

// Design 03 A75 (ADR-0034 Amendment 5, L1), against a real agentgateway: an
// API-key policy on the assayd Gateway answers the anonymous probe of a new
// Agent's prepared route with 401 before <agent>-auth exists (A74 case 7), so
// the operator must not credit it. A new Agent holds at ProbingAfter with
// PolicyApplyIncomplete=GatewayAuthPolicy, its route prepared and unpublished,
// and is published once the policy is removed.
//
// The policy is scoped to the serving listener by sectionName, so the MCP
// tests' `tools` listener never sees it, and it carries none of the
// operator's labels, so nothing maps it to an Agent.
func TestAGatewayLevelAuthPolicyHoldsANewAgent(t *testing.T) {
	requireCluster(t)
	requireOperator(t)
	img := responderImage(t)
	gwNS, gwName := requireGateway(t)
	ctx := context.Background()
	ensureNamespace(t, ctx, "assayd-e2e")
	ensureAPIKeys(t, ctx)

	gp, err := compiler.AuthPolicy(compiler.AuthInput{AgentName: "gwlevel", AgentNamespace: "assayd-e2e",
		AgentUID: types.UID("e2e-gwlevel"), RunNamespace: gwNS})
	if err != nil {
		t.Fatal(err)
	}
	gp.SetName("assayd-e2e-gateway-auth")
	gp.SetLabels(nil)
	if err := unstructured.SetNestedSlice(gp.Object, []any{map[string]any{"group": "gateway.networking.k8s.io",
		"kind": "Gateway", "name": gwName, "sectionName": controller.GatewayListenerName}},
		"spec", "targetRefs"); err != nil {
		t.Fatal(err)
	}
	_ = k8s.Delete(ctx, gp.DeepCopy())
	if err := k8s.Create(ctx, gp.DeepCopy()); err != nil {
		t.Fatalf("create the Gateway-level API-key policy: %v", err)
	}
	t.Cleanup(func() { _ = k8s.Delete(context.Background(), gp.DeepCopy()) })

	const name = "gwheld"
	a := &assaydv1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "assayd-e2e"},
		Spec: assaydv1alpha1.AgentSpec{Runtime: &assaydv1alpha1.AgentRuntime{
			Image: img, Env: []corev1.EnvVar{{Name: "AGENT_NAME", Value: name}}}},
	}
	_ = k8s.Delete(ctx, a)
	if err := k8s.Create(ctx, a); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	t.Cleanup(func() { _ = k8s.Delete(context.Background(), a) })
	waitAvailable(t, ctx, controller.WorkloadName(name, revision.MustHash(a.Spec)), 4*time.Minute)

	held := waitForReason(t, ctx, name, assaydv1alpha1.CondPolicyApplyIncomplete, "GatewayAuthPolicy", 3*time.Minute)
	if !strings.Contains(held.Message, gwNS+"/"+gp.GetName()) {
		t.Errorf("GatewayAuthPolicy does not name the policy: %s", held.Message)
	}
	var live assaydv1alpha1.Agent
	if err := k8s.Get(ctx, types.NamespacedName{Namespace: "assayd-e2e", Name: name}, &live); err != nil {
		t.Fatal(err)
	}
	if tx := live.Status.Auth.Transaction; tx == nil || tx.Kind != "Create" || tx.Stage != "ProbingAfter" ||
		tx.Probe == nil || tx.Probe.After == nil || *tx.Probe.After != 401 {
		t.Errorf("the Create is not held at ProbingAfter on the Gateway's 401: %+v", live.Status.Auth)
	}
	rt, err := getEmittedRoute(ctx, t, name)
	if err != nil {
		t.Fatalf("read the route: %v", err)
	}
	rules, _, _ := unstructured.NestedSlice(rt.Object, "spec", "rules")
	for _, rule := range rules {
		if refs, _, _ := unstructured.NestedSlice(rule.(map[string]any), "backendRefs"); len(refs) > 0 {
			t.Fatalf("the route was published on a 401 the Gateway-level policy answered: %v", refs)
		}
	}

	if err := k8s.Delete(ctx, gp.DeepCopy()); err != nil {
		t.Fatalf("remove the Gateway-level policy: %v", err)
	}
	waitForPublishedRoute(t, ctx, name, 3*time.Minute)
	waitForAuth(t, ctx, name, 3*time.Minute, "Served under apikey", func(s *assaydv1alpha1.AuthStatus) bool {
		return s != nil && s.Mode == "apikey" && s.Transaction == nil
	})

	gwSvc := gatewayService(t, ctx, gwNS, gwName)
	url := fmt.Sprintf("http://%s.%s.svc.cluster.local:8080", gwSvc, gwNS) + a2aSendMessage
	host := emittedHostname(t, name, "assayd-e2e")
	body := sendMessage("after the Gateway-level policy went")
	waitForCode(t, ctx, "gwheld-anon", url, host, body, "401", 2*time.Minute,
		"an anonymous caller of the published route must be refused by <agent>-auth")
	answer := askThroughGateway(t, ctx, "gwheld-key", url, host, body)
	if agent, _ := completedTask(t, answer); agent != name {
		t.Errorf("a key in the Agent's group got a 200 that is not the agent's answer: %s", answer)
	}
}

// waitForReason polls an Agent's condition until it carries reason, and fails
// with status.auth and every condition otherwise.
func waitForReason(t *testing.T, ctx context.Context, name string, typ assaydv1alpha1.ConditionType,
	reason string, d time.Duration) metav1.Condition {
	t.Helper()
	deadline := time.Now().Add(d)
	var a assaydv1alpha1.Agent
	for time.Now().Before(deadline) {
		if err := k8s.Get(ctx, types.NamespacedName{Namespace: "assayd-e2e", Name: name}, &a); err == nil {
			for _, c := range a.Status.Conditions {
				if c.Type == string(typ) && c.Reason == reason {
					return c
				}
			}
		}
		time.Sleep(2 * time.Second)
	}
	var conds []string
	for _, c := range a.Status.Conditions {
		conds = append(conds, fmt.Sprintf("%s=%s/%s: %s", c.Type, c.Status, c.Reason, c.Message))
	}
	t.Fatalf("%s never reported %s=%s within %s. status.auth=%+v conditions:\n%s", name, typ, reason, d,
		a.Status.Auth, strings.Join(conds, "\n"))
	return metav1.Condition{}
}
