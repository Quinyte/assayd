// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// gatewayPolicyReads is the receiver of design 03's Gateway and policy reads —
// A75's gatewayAuthPolicies and §3.2's foreignTrafficPolicies, and the
// listPolicies and servingRouteLabels they call — so that a controller other
// than the Agent reconciler can make the same reads with the same code. Design
// 16 §1.1 owes this for the eval controller's clause 5 ("What clause 5
// costs"): the two functions moved here with their bodies unchanged.
//
// It carries the client, the uncached reader and the GatewayConfig, and
// nothing else, so it reads exactly what the functions read where they were.
type gatewayPolicyReads struct {
	// Client is the manager's cached client: servingRouteLabels' Get.
	client.Client
	// uncached is the uncached reader the live Gateway get and policy lists
	// use. In envtest the client itself is uncached and this may be nil, which
	// reader resolves to the client.
	uncached client.Reader
	// Gateway is the assayd Gateway (design 03 §3.1).
	Gateway GatewayConfig
}

// reader is the uncached reader for the live lists the protocol requires,
// under the name the moved bodies call: uncached, or the client when uncached
// is nil. (*AgentReconciler).reader resolves through it, so the fallback is
// written once.
func (r gatewayPolicyReads) reader() client.Reader {
	if r.uncached != nil {
		return r.uncached
	}
	return r.Client
}

// policyReads is the Agent reconciler's gatewayPolicyReads: its client, its
// uncached reader and its Gateway.
func (r *AgentReconciler) policyReads() gatewayPolicyReads {
	return gatewayPolicyReads{Client: r.Client, uncached: r.Reader, Gateway: r.Gateway}
}
