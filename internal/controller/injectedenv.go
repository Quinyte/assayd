// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	corev1 "k8s.io/api/core/v1"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
)

// InjectedEnvPrefix is reserved to the operator. Design 02 §11 has always said
// this design owns the injected env contract and design 09 says templates
// "consume, never define" it — but nothing reserved the namespace, so a user
// could set ASSAYD_GATEWAY_URL in spec.runtime.env and Kubernetes would keep the
// LAST duplicate in the container. That is a redirect of the agent's egress
// away from the chokepoint every guarantee in this platform is enforced at,
// written by the person the chokepoint constrains. Admission now rejects the
// prefix (A65); injection appends after user env as defence in depth, so the
// operator's value wins even if a path ever reached here without admission.
const InjectedEnvPrefix = "ASSAYD_"

// EnvGatewayURL is the base URL of the agentgateway an agent's egress traverses.
const EnvGatewayURL = "ASSAYD_GATEWAY_URL"

// injectedEnv returns the operator-owned variables for one workload.
//
// **Only what has a producer is injected.** Design 02 §11 names three variables
// and design 09's reference loop consumes them, but two have nothing to produce
// a value from today and this function does not invent one:
//
//   - ASSAYD_KG_ENDPOINTS would carry the resolved endpoint per knowledge
//     binding. There is no KnowledgeGraph CR in any design to resolve one from
//     — design 01 is the KnowledgeGraphProvider protocol and design 13 the
//     Graphiti adapter; neither declares the kind. Owed there.
//   - ASSAYD_NATS_URL is the JetStream task store that makes replicas>1 safe.
//     No NATS ships in the chart at all, so there is no address and no
//     credential. Owed to design 07.
//
// Injecting a placeholder for either would be worse than omitting it: an agent
// that reads an address and fails to connect looks like an outage, while an
// agent that finds the variable absent can take its documented fallback. Design
// 02 §5 carries both as unavailable, and TaskStateUnverified is the condition
// that already exists to say so for the second.
//
// **These are not revision material, and that is a decision rather than an
// oversight (A65).** The values are operator-owned, identical for every Agent
// in the cluster, and not something a user can set. A gate exists to stop a
// user publishing capability an evaluation never saw; an administrator moving
// the cluster's gateway is a cluster operation, and gating it would make every
// Agent in the cluster mint a candidate simultaneously and wait for an eval —
// during an infrastructure migration, which is the worst moment to freeze every
// rollout. Stated as an exception in the same terms as runtime.resources: the
// gate covers named release material and capability configuration, and this is
// neither.
func injectedEnv(cfg InjectedEnvConfig) []corev1.EnvVar {
	var out []corev1.EnvVar
	if cfg.GatewayURL != "" {
		out = append(out, corev1.EnvVar{Name: EnvGatewayURL, Value: cfg.GatewayURL})
	}
	return out
}

// InjectedEnvConfig is what the operator knows that an Agent does not. It is
// operator configuration, not spec: a flag on the operator, so one cluster has
// one answer and no Agent can disagree with it.
type InjectedEnvConfig struct {
	// GatewayURL is empty when no gateway is installed — the chart defaults
	// gateway.enabled to false and ships no agentgateway subchart (design 07
	// §3). Empty means the variable is not set at all.
	GatewayURL string
}

// withInjectedEnv appends the operator's variables after the user's. Order is
// the mechanism: the kubelet resolves duplicates last-wins, so appending is
// what makes the operator authoritative.
func withInjectedEnv(user []corev1.EnvVar, cfg InjectedEnvConfig) []corev1.EnvVar {
	inj := injectedEnv(cfg)
	if len(inj) == 0 {
		return user
	}
	return append(append(make([]corev1.EnvVar, 0, len(user)+len(inj)), user...), inj...)
}

// UsesReservedEnvPrefix reports the first reserved name an Agent sets itself.
// Admission rejects these, so this is the operator-side belt to that braces —
// and the path that reports it on an object admitted before the rule existed.
func UsesReservedEnvPrefix(spec assaydv1alpha1.AgentSpec) string {
	if spec.Runtime == nil {
		return ""
	}
	for _, e := range spec.Runtime.Env {
		if len(e.Name) >= len(InjectedEnvPrefix) && e.Name[:len(InjectedEnvPrefix)] == InjectedEnvPrefix {
			return e.Name
		}
	}
	for _, f := range spec.Runtime.EnvFrom {
		if len(f.Prefix) >= len(InjectedEnvPrefix) && f.Prefix[:len(InjectedEnvPrefix)] == InjectedEnvPrefix {
			return f.Prefix
		}
	}
	return ""
}
