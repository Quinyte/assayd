// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package envtest

import (
	"context"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/controller"
	"github.com/Quinyte/assayd/internal/revision"
)

// A65. Design 02 §11 has always said this design owns the injected env
// contract and design 09 says templates "consume, never define" it. Nothing
// injected anything, and nothing reserved the prefix either.
func TestTheOperatorInjectsTheGatewayURL(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "inj", nil)
	r := newReconcilerWithEnv(false, controller.InjectedEnvConfig{GatewayURL: "http://assayd-gateway.assayd:8080"})
	rev := revision.MustHash(a.Spec)
	settle(t, r, a)

	env := containerEnv(t, ns, controller.WorkloadName("inj", rev))
	got, ok := env[controller.EnvGatewayURL]
	if !ok {
		t.Fatalf("%s was not injected; container env: %v", controller.EnvGatewayURL, env)
	}
	if got != "http://assayd-gateway.assayd:8080" {
		t.Errorf("%s is %q", controller.EnvGatewayURL, got)
	}
}

// The absent-producer rule. Two of the three variables §11 names have nothing
// to produce a value from — no KnowledgeGraph CR exists in any design, and no
// NATS ships in the chart — and a placeholder address would look like an
// outage to an agent that dials it, where an absent variable lets the agent
// take its documented fallback.
func TestNothingIsInjectedForAProducerThatDoesNotExist(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "noprod", nil)
	r := newReconciler(false) // no gateway configured either
	rev := revision.MustHash(a.Spec)
	settle(t, r, a)

	env := containerEnv(t, ns, controller.WorkloadName("noprod", rev))
	for _, name := range []string{"ASSAYD_KG_ENDPOINTS", "ASSAYD_NATS_URL", controller.EnvGatewayURL} {
		if v, ok := env[name]; ok {
			t.Errorf("%s was injected as %q with no producer for it; an agent that dials a "+
				"placeholder reports an outage instead of taking its fallback", name, v)
		}
	}
}

// The bypass the reservation closes. A container keeps the LAST duplicate, so
// before this rule a user could set ASSAYD_GATEWAY_URL in their own env and
// redirect their agent's egress away from the chokepoint that enforces every
// budget, tool grant and egress rule — written by exactly the person those
// rules constrain.
func TestAUserCannotSetAReservedEnvName(t *testing.T) {
	ns := newNamespace(t)
	a := &assaydv1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "reserved"},
		Spec: assaydv1alpha1.AgentSpec{Runtime: &assaydv1alpha1.AgentRuntime{
			Image: "ghcr.io/acme/agent@sha256:" + strings.Repeat("a", 64),
			Env: []corev1.EnvVar{{
				Name:  controller.EnvGatewayURL,
				Value: "http://attacker.example/",
			}},
		}},
	}
	err := k8s.Create(context.Background(), a)
	if err == nil {
		t.Fatal("the API accepted an Agent setting ASSAYD_GATEWAY_URL itself: a container keeps " +
			"the last duplicate, so this redirects the agent's egress off the gateway")
	}
	if !strings.Contains(err.Error(), "ASSAYD_") {
		t.Errorf("refused, but not by the reserved-prefix rule — the message does not mention "+
			"the prefix, so this test proves nothing about which rule fired: %v", err)
	}
}

// The same door, one field over: envFrom maps a whole ConfigMap or Secret under
// a prefix, so a ASSAYD_ prefix there shadows the injected name just as well.
func TestAUserCannotUseAReservedEnvFromPrefix(t *testing.T) {
	ns := newNamespace(t)
	a := &assaydv1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "reservedfrom"},
		Spec: assaydv1alpha1.AgentSpec{Runtime: &assaydv1alpha1.AgentRuntime{
			Image: "ghcr.io/acme/agent@sha256:" + strings.Repeat("b", 64),
			EnvFrom: []corev1.EnvFromSource{{
				Prefix:       "ASSAYD_",
				ConfigMapRef: &corev1.ConfigMapEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: "cfg"}},
			}},
		}},
	}
	err := k8s.Create(context.Background(), a)
	if err == nil {
		t.Fatal("the API accepted an envFrom prefix of ASSAYD_")
	}
	if !strings.Contains(err.Error(), "ASSAYD_") {
		t.Errorf("refused by something other than the prefix rule: %v", err)
	}
}

// Injected values are NOT revision material, and that is a decision. They are
// operator-owned and identical for every Agent, so gating them would make every
// Agent in the cluster mint at once and wait for an eval during an
// infrastructure migration — the worst moment to freeze every rollout.
func TestChangingTheInjectedGatewayURLDoesNotMintARevision(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "nomint", nil)
	before := revision.MustHash(a.Spec)

	r := newReconcilerWithEnv(false, controller.InjectedEnvConfig{GatewayURL: "http://one.assayd:8080"})
	settle(t, r, a)
	r2 := newReconcilerWithEnv(false, controller.InjectedEnvConfig{GatewayURL: "http://two.assayd:8080"})
	settle(t, r2, a)

	if after := revision.MustHash(a.Spec); after != before {
		t.Errorf("the revision moved from %s to %s when only the operator's injected gateway "+
			"changed; every Agent in the cluster would mint at once", before, after)
	}
	// And the running workload does follow the new address — it is converged in
	// place, which is what "not revision material" has to mean to be useful.
	env := containerEnv(t, ns, controller.WorkloadName("nomint", before))
	if got := env[controller.EnvGatewayURL]; got != "http://two.assayd:8080" {
		t.Errorf("the workload still carries %q after the operator was repointed", got)
	}
}

func containerEnv(t *testing.T, ns, name string) map[string]string {
	t.Helper()
	var d appsv1.Deployment
	key := types.NamespacedName{Namespace: runNS(ns), Name: name}
	if err := k8s.Get(context.Background(), key, &d); err != nil {
		t.Fatalf("get workload %s: %v", name, err)
	}
	out := map[string]string{}
	for _, e := range d.Spec.Template.Spec.Containers[0].Env {
		out[e.Name] = e.Value
	}
	return out
}
