// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

// The operator's value must win a duplicate, and the mechanism is order: the
// kubelet resolves a repeated env name last-wins, so appending is what makes
// the operator authoritative.
//
// This is a unit test on purpose. Admission rejects a user-set ASSAYD_ name, so
// no duplicate can arrive through the API and no envtest can reach this code
// path — a mutation reversing the order SURVIVED the whole envtest suite. That
// made the ordering a claim in a comment rather than a property. It is
// defence in depth for a reason: if the CRD rule is ever relaxed, softened to a
// warning, or bypassed by a path that writes a workload without admission, this
// ordering is the only thing left standing between a user's env and the
// gateway the platform enforces at.
func TestTheOperatorsInjectedValueWinsADuplicate(t *testing.T) {
	user := []corev1.EnvVar{
		{Name: "SAFE", Value: "keep"},
		{Name: EnvGatewayURL, Value: "http://attacker.example/"},
	}
	got := withInjectedEnv(user, InjectedEnvConfig{GatewayURL: "http://gateway.assayd:8080"})

	// Last occurrence is what the container sees.
	var last string
	for _, e := range got {
		if e.Name == EnvGatewayURL {
			last = e.Value
		}
	}
	if last != "http://gateway.assayd:8080" {
		t.Errorf("the container would see %s=%q; the operator's value must be last, or a user "+
			"env entry redirects the agent's egress off the gateway", EnvGatewayURL, last)
	}
	if got[0].Name != "SAFE" || got[0].Value != "keep" {
		t.Errorf("user env was not preserved: %v", got)
	}
}

func TestNothingIsInjectedWithoutAProducer(t *testing.T) {
	if got := withInjectedEnv(nil, InjectedEnvConfig{}); len(got) != 0 {
		t.Errorf("injected %v with no configuration; a placeholder address makes an absent "+
			"tier look like an outage", got)
	}
}
