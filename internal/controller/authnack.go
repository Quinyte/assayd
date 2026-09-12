// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
)

// A NACK drives status (design 03 §3.3, §3.3.2, A70). agentgateway reports a
// dataplane rejection only as a Warning Event on the Gateway, and a NACK'd
// policy keeps the previous configuration serving while its conditions read
// converged. So a genuine NACK naming this Agent's `<agent>-auth` raises
// PolicyApplyIncomplete at once and returns the transaction to `Converging`.
// It can advance nothing: its key carries no generation.
//
// What "genuine" means was read from agentgateway's source at v1.5.0 and
// measured on k3d (design 03 A72): the controller's recorder is created with
// `EventSource{Component: "agentgateway.dev/agentgateway"}`
// (`controller/pkg/syncer/nack/publisher.go`, `wellknown.DefaultAgwControllerName`),
// client-go's recorder copies that into `reportingController`, and the Event's
// `involvedObject` is the Gateway.

// AgentgatewayControllerName is the component agentgateway's controller
// reports its Events as, and the `controllerName` it writes into route and
// policy status.
const AgentgatewayControllerName = "agentgateway.dev/agentgateway"

// GenuineNack reports whether a NACK Event has the shape agentgateway gives
// one: a Warning with the NACK reason, in the Gateway's namespace, whose
// involvedObject is THE Gateway this operator publishes to, and whose source
// and reporting controller are agentgateway's controller.
//
// This refuses an Event written to look like a NACK by anything that does not
// copy those fields exactly, which is every other controller and every
// mistake. It does NOT refuse a writer who copies them: anyone who can create
// Events in the Gateway's namespace can. What such a forgery can do is a
// denial, never an opening: it raises PolicyApplyIncomplete, which pages, and
// holds a transaction short of `Served`. Restricting Event writes in that
// namespace is the administrator's RBAC (design 03 A72).
func GenuineNack(ev *corev1.Event, gw GatewayConfig) bool {
	if ev.Type != corev1.EventTypeWarning || ev.Reason != NackEventReason || ev.Namespace != gw.Namespace {
		return false
	}
	io := ev.InvolvedObject
	gv, err := schema.ParseGroupVersion(io.APIVersion)
	if err != nil || gv.Group != gatewayv1.GroupName || io.Kind != "Gateway" ||
		io.Name != gw.Name || io.Namespace != gw.Namespace {
		return false
	}
	return ev.Source.Component == AgentgatewayControllerName &&
		ev.ReportingController == AgentgatewayControllerName
}

// eventLastSeen is the latest time an Event says it was observed.
func eventLastSeen(ev *corev1.Event) time.Time {
	at := ev.FirstTimestamp.Time
	for _, t := range []time.Time{ev.LastTimestamp.Time, ev.EventTime.Time} {
		if t.After(at) {
			at = t
		}
	}
	if ev.Series != nil && ev.Series.LastObservedTime.Time.After(at) {
		at = ev.Series.LastObservedTime.Time
	}
	return at
}

// policyWrittenAt is when the policy's spec or metadata was last written, as
// the API server recorded it in managedFields: the latest entry that is not a
// status write. With none, its creation.
func policyWrittenAt(p *unstructured.Unstructured) time.Time {
	var at time.Time
	for _, mf := range p.GetManagedFields() {
		if mf.Subresource != "" || mf.Time == nil {
			continue
		}
		if mf.Time.After(at) {
			at = mf.Time.Time
		}
	}
	if at.IsZero() {
		at = p.GetCreationTimestamp().Time
	}
	return at
}

// nackPageSize and maxNackPages bound the NACK Events one pass reads: whoever
// can write Events in the Gateway's namespace chooses how many exist.
const (
	nackPageSize = 100
	maxNackPages = 10
)

// freshNack returns what a genuine NACK naming this policy says, when one was
// last seen strictly AFTER the policy's last write, or "".
//
// Strictly after: an Event carries no generation, so a NACK can be tied to a
// write only by time, and both clocks keep seconds. A NACK in the second of
// the write is not counted. Missing it costs nothing the probe does not catch,
// because a NACK'd policy keeps the previous configuration and the probe then
// never passes; counting a NACK of an earlier write would hold the
// transaction for as long as the Event lives (design 03 A72).
//
// Listed live, through the uncached reader, in the Gateway's namespace only,
// filtered by the API server on type and reason: the RBAC is a Role there. A
// failed list is logged, and the pass goes on as if no NACK was seen.
func (r *AgentReconciler) freshNack(ctx context.Context, agent *assaydv1alpha1.Agent, runNS string,
	policy *unstructured.Unstructured) string {
	if policy == nil || r.Gateway.Namespace == "" {
		return ""
	}
	var list corev1.EventList
	cont := ""
	for page := 0; page < maxNackPages; page++ {
		var chunk corev1.EventList
		if err := r.reader().List(ctx, &chunk, client.InNamespace(r.Gateway.Namespace),
			client.MatchingFields{"type": corev1.EventTypeWarning, "reason": NackEventReason},
			client.Limit(nackPageSize), client.Continue(cont)); err != nil {
			log.FromContext(ctx).Error(err, "could not list the Gateway's NACK Events; this pass takes "+
				"no NACK into account", "agent", agent.Namespace+"/"+agent.Name)
			return ""
		}
		list.Items = append(list.Items, chunk.Items...)
		if cont = chunk.Continue; cont == "" {
			break
		}
	}
	if cont != "" {
		log.FromContext(ctx).Info("the Gateway's namespace holds more NACK Events than one pass reads; "+
			"the rest are not taken into account", "read", len(list.Items))
	}
	written := policyWrittenAt(policy)
	key := types.NamespacedName{Namespace: runNS, Name: policy.GetName()}
	var found *corev1.Event
	var at time.Time
	for i := range list.Items {
		ev := &list.Items[i]
		if !GenuineNack(ev, r.Gateway) {
			continue
		}
		seen := eventLastSeen(ev)
		if !seen.After(written) || !seen.After(at) {
			continue
		}
		names, _ := NackedPolicies(ev.Message)
		for _, n := range names {
			if n == key {
				found, at = ev, seen
				break
			}
		}
	}
	if found == nil {
		return ""
	}
	detail := found.Message
	if len(detail) > 512 {
		detail = detail[:512] + "…"
	}
	return fmt.Sprintf("the gateway rejected %s at %s (Warning Event %s/%s, %s: %s). A NACK'd "+
		"policy keeps the previous configuration serving while its conditions read converged, so "+
		"the transaction is back in Converging, and no probe answer is taken while this Event is "+
		"newer than the policy's last write. Read the policy's status and the Event. To release the "+
		"hold, delete that Event, or wait for the API server to expire it, one hour after it was last "+
		"seen by default (design 03 §3.3.2)", key, at.UTC().Format(time.RFC3339),
		found.Namespace, found.Name, NackEventReason, detail)
}
