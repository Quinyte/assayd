// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"fmt"
	"sort"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/Quinyte/assayd/internal/compiler"
)

// Design 03 A75 (ADR-0034 Amendment 5, the human's L1): a policy on the
// assayd Gateway can answer an anonymous request on an Agent's route with 401
// before `<agent>-auth` exists (A74 case 7), and neither `Create`'s probe nor
// `Lock`'s attribution can tell that 401 from `<agent>-auth`'s. So at every
// ProbingAfter pass the operator reads the Gateway and the policies on it, and
// again after a 401 it would credit, and credits it only when nothing there
// counts. An Agent already served is reported, not held.

// harmlessPolicyFields are the `spec` fields of an AgentgatewayPolicy, as
// `<section>.<field>`, that cannot answer the probe, an anonymous GET of the
// card path, with a status they choose: they set no response status, refuse
// no GET, and hand the request to nothing that can. Every other field counts,
// so a field nothing has classified counts, and the rule errs toward holding.
// `strategy.inheritance` is decided by its value (classifyPolicy).
//
// Design 03 A75's field table is the source of this list, and
// TestTheFieldClassificationIsDesign03sTable pins the two together and
// against every field of the vendored 1.4.1 and 1.5.0 CRDs.
var harmlessPolicyFields = map[string]bool{
	"traffic.buffer":                true,
	"traffic.cors":                  true,
	"traffic.csrf":                  true,
	"traffic.delay":                 true,
	"traffic.hostRewrite":           true,
	"traffic.phase":                 true,
	"traffic.retry":                 true,
	"traffic.timeouts":              true,
	"backend.ai":                    true,
	"backend.health":                true,
	"backend.http":                  true,
	"backend.mcp":                   true,
	"backend.tcp":                   true,
	"backend.tls":                   true,
	"frontend.accessLog":            true,
	"frontend.connect":              true,
	"frontend.http":                 true,
	"frontend.metrics":              true,
	"frontend.networkAuthorization": true,
	"frontend.proxyProtocol":        true,
	"frontend.tcp":                  true,
	"frontend.tls":                  true,
	"frontend.tracing":              true,
}

// wideningPolicyFields are the counting fields that could widen a served
// route beside its <agent>-auth (W1); strategy.inheritance: Override widens
// too, and classifyPolicy reads it by value. An authentication-only policy is
// not here: for A74 case 7's shape the route's authentication replaces it.
var wideningPolicyFields = map[string]bool{
	"traffic.authorization": true,
}

// gatewayAuth is what one read of the Gateway found that makes a 401 on an
// Agent's route unattributable. Any one field set means it stands.
type gatewayAuth struct {
	// policies are the counting AgentgatewayPolicies on the Gateway, as
	// `<namespace>/<name>`.
	policies []string
	// widening are those of them that could widen a served route beside its
	// <agent>-auth (W1): they set traffic.authorization, whose rules merge
	// across attachment points, or strategy.inheritance: Override, which
	// takes the route from its own traffic policy (both measured, A75).
	widening []string
	// listeners says which ListenerSets the Gateway admits, when it admits
	// any.
	listeners string
	// unreadable says why the Gateway or its policies could not be read:
	// nothing on it can then be ruled out.
	unreadable string
}

// stands reports whether a 401 on the route cannot be credited to
// `<agent>-auth`.
func (g gatewayAuth) stands() bool {
	return len(g.policies) > 0 || g.listeners != "" || g.unreadable != ""
}

// describe names every cause that stands, and what to do about each.
func (g gatewayAuth) describe(gw GatewayConfig) string {
	var parts []string
	if g.unreadable != "" {
		parts = append(parts, g.unreadable)
	}
	if len(g.policies) > 0 {
		parts = append(parts, fmt.Sprintf("the AgentgatewayPolicy %s targets Gateway %s/%s on a listener "+
			"that can take this Agent's traffic, and sets a field that can answer an anonymous request "+
			"with a status of its choosing, 401 included, or strategy.inheritance: Override. Remove it, "+
			"or scope it off that listener by a sectionName, or target the routes it is meant for",
			strings.Join(g.policies, ", "), gw.Namespace, gw.Name))
	}
	if g.listeners != "" {
		parts = append(parts, fmt.Sprintf("Gateway %s/%s admits ListenerSets (spec.allowedListeners."+
			"namespaces.from: %s), whether or not any exists. A ListenerSet's listener can capture this "+
			"Agent's traffic, and this operator does not inspect ListenerSets. Set from: None, or wait "+
			"until the slice inspects ListenerSets", gw.Namespace, gw.Name, g.listeners))
	}
	return strings.Join(parts, "; and ")
}

// holdMessage is why no answer of this pass is credited (A75). got401 says
// whether the probe's answer was the 401 that would otherwise be.
func (g gatewayAuth) holdMessage(gw GatewayConfig, runNS, policy string, got401 bool) string {
	lead := "no 401 is credited to " + runNS + "/" + policy + " while"
	if got401 {
		lead = "the last anonymous probe got a 401, and it is not credited to " + runNS + "/" + policy +
			", because"
	}
	return fmt.Sprintf("%s something above the route could answer it whether or not %s has taken: %s "+
		"(design 03 §3.3.3, A75)", lead, policy, g.describe(gw))
}

// wideningMessage is W1's message, on GovernanceSkipped and
// PolicyApplyIncomplete, for an API-key Agent already served beside a policy
// that could widen its route (A75).
func (g gatewayAuth) wideningMessage(gw GatewayConfig) string {
	where := "on a listener that can take this Agent's traffic"
	if g.unreadable != "" {
		where = "on a listener that may take this Agent's traffic (" + g.unreadable + ", so no sectionName " +
			"or selector is taken as keeping it off this route)"
	}
	return fmt.Sprintf("the AgentgatewayPolicy %s targets Gateway %s/%s %s, and sets traffic.authorization, "+
		"whose rules merge with this route's <agent>-auth "+
		"(measured: a Gateway-level Allow rule admitted on a route a group its <agent>-auth refuses), or "+
		"strategy.inheritance: Override, which was measured taking the route from <agent>-auth. "+
		"So this route admits whatever the Gateway-level authorization rule allows, beyond its own "+
		"<agent>-auth, until the policy is removed, or rescoped off the Gateway or off its serving "+
		"listener %q. The route keeps serving and is not withdrawn (design 03 A75)",
		strings.Join(g.widening, ", "), gw.Namespace, gw.Name, where, GatewayListenerName)
}

// servedNote is what an Agent already served says on GovernanceSkipped's
// message while a cause stands and none overrides: it is reported, not held
// (A75). It claims about the route only what has been measured.
func (g gatewayAuth) servedNote(gw GatewayConfig, mode string) string {
	head := "What would hold a new -auth transaction stands: " + g.describe(gw) + ". This Agent is served " +
		"and is not held; a re-creation of its route or its policy is (design 03 A75)"
	if mode == string(compiler.AuthModeNone) {
		return head + ". Its route carries no <agent>-auth, so a Gateway-level policy can apply there: " +
			"A74 case 7 measured one refusing an anonymous request with 401 on a route with no -auth, so " +
			"callers may be refused although status says auth: none"
	}
	if g.unreadable != "" {
		return head + ". Whether a policy on the Gateway sets authorization or Override, which would widen " +
			"this route beside its <agent>-auth, cannot be ruled out while that cannot be read"
	}
	return head + ". No policy on the Gateway sets authorization or Override: for A74 case 7's measured " +
		"shape, under inheritance Default, <agent>-auth's authentication replaces the Gateway's on this route"
}

// gatewayAuthPolicies is A75's read: the assayd Gateway, live, and every
// AgentgatewayPolicy in its namespace, live, uncached and paged, as §3.2's
// foreign detection lists. Only that namespace: a policy's targetRefs and
// targetSelectors are local to its own (§3.2). host is the Agent's hostname,
// which decides which of the Gateway's listeners can take its traffic.
//
// It never fails. A Gateway or a policy list it cannot read, for any reason,
// is a cause that stands, and says why: unlike foreignTrafficPolicies, a
// policy list the API server refuses is not read as empty. A Gateway it cannot
// read does not stop the list: every policy that targets it is then read as
// on a capturing listener, whatever its sectionName or selector, so that a
// policy that would widen a served route is still found (W1).
func (r *AgentReconciler) gatewayAuthPolicies(ctx context.Context, host string) gatewayAuth {
	gw := r.Gateway
	var out gatewayAuth
	var g gatewayv1.Gateway
	known := true
	if err := r.reader().Get(ctx, client.ObjectKey{Namespace: gw.Namespace, Name: gw.Name}, &g); err != nil {
		known = false
		why := fmt.Sprintf("Gateway %s/%s could not be read (%v)", gw.Namespace, gw.Name, err)
		switch {
		case meta.IsNoMatchError(err):
			why = fmt.Sprintf("the Gateway API is not served, so Gateway %s/%s cannot be read", gw.Namespace, gw.Name)
		case apierrors.IsNotFound(err):
			why = fmt.Sprintf("Gateway %s/%s does not exist; create it, or correct --gateway-name and "+
				"--gateway-namespace", gw.Namespace, gw.Name)
		case apierrors.IsForbidden(err):
			why = fmt.Sprintf("the operator may not read Gateway %s/%s (%v); grant get on it, as the chart's "+
				"gateway-labels Role in %s does", gw.Namespace, gw.Name, err, gw.Namespace)
		}
		out.unreadable = why + ", so nothing on it can be ruled out"
	} else {
		out.listeners = admitsListenerSets(&g)
	}
	items, err := r.listPolicies(ctx, gw.Namespace, "a Gateway-level auth policy", false)
	if err != nil {
		out.unreadable = joinCauses(out.unreadable, fmt.Sprintf("the AgentgatewayPolicies in %s could not be "+
			"listed (%v), so none on the Gateway can be ruled out", gw.Namespace, err))
		return out
	}
	var capture map[string]bool
	if known {
		capture = capturingListeners(&g, host)
	}
	for i := range items {
		p := &items[i]
		counts, widens := classifyPolicy(p)
		if !counts || !targetsGateway(p, gw.Name, g.Labels, capture, known) {
			continue
		}
		key := p.GetNamespace() + "/" + p.GetName()
		out.policies = append(out.policies, key)
		if widens {
			out.widening = append(out.widening, key)
		}
	}
	sort.Strings(out.policies)
	sort.Strings(out.widening)
	return out
}

func joinCauses(a, b string) string {
	if a == "" {
		return b
	}
	return a + "; and " + b
}

// admitsListenerSets is the Gateway's spec.allowedListeners.namespaces.from
// when it admits ListenerSets, or "". Absent at any level, `{}`, which the
// API server defaults to `from: None`, and `None` admit none. `Same`, `All`
// and every `Selector`, whatever it matches, admit.
func admitsListenerSets(g *gatewayv1.Gateway) string {
	al := g.Spec.AllowedListeners
	if al == nil || al.Namespaces == nil || al.Namespaces.From == nil ||
		*al.Namespaces.From == gatewayv1.NamespacesFromNone {
		return ""
	}
	return string(*al.Namespaces.From)
}

// classifyPolicy says whether a policy counts under A75, and whether it could
// widen a served route (W1). It counts when any field in any section of its
// spec is not harmless, or when it sets strategy.inheritance: Override,
// whatever else it sets. It widens when it sets a field in
// wideningPolicyFields, or Override.
func classifyPolicy(p *unstructured.Unstructured) (counts, widens bool) {
	spec, _, _ := unstructured.NestedMap(p.Object, "spec")
	for section, v := range spec {
		if section == "targetRefs" || section == "targetSelectors" {
			continue
		}
		fields, ok := v.(map[string]any)
		if !ok {
			counts = true
			continue
		}
		for f, fv := range fields {
			if section == "strategy" && f == "inheritance" {
				if s, _ := fv.(string); s == "Override" {
					counts, widens = true, true
				} else if s != "Default" && s != "" {
					counts = true
				}
				continue
			}
			if !harmlessPolicyFields[section+"."+f] {
				counts = true
			}
			if wideningPolicyFields[section+"."+f] {
				widens = true
			}
		}
	}
	return counts, widens
}

// capturingListeners are the names of the Gateway's listeners that can take
// the Agent's traffic: the serving listener, and any other on its port whose
// hostname is unset or matches host. A policy scoped by sectionName to one of
// them counts; one scoped to another, `tools` for one, does not.
func capturingListeners(g *gatewayv1.Gateway, host string) map[string]bool {
	out := map[string]bool{GatewayListenerName: true}
	var port gatewayv1.PortNumber
	for _, l := range g.Spec.Listeners {
		if string(l.Name) == GatewayListenerName {
			port = l.Port
		}
	}
	for _, l := range g.Spec.Listeners {
		if port != 0 && l.Port == port && (l.Hostname == nil || hostMatches(string(*l.Hostname), host)) {
			out[string(l.Name)] = true
		}
	}
	return out
}

// hostMatches is Gateway API's listener hostname match: exact, or a `*.`
// wildcard matching one or more leading labels.
func hostMatches(pattern, host string) bool {
	if pattern == "" || pattern == host {
		return true
	}
	if suffix, ok := strings.CutPrefix(pattern, "*"); ok {
		return strings.HasSuffix(host, suffix) && len(host) > len(suffix)
	}
	return false
}

// targetsGateway reports whether a policy in the Gateway's namespace targets
// the assayd Gateway on a listener that can take the Agent's traffic: a
// targetRefs entry naming it, or a targetSelectors entry of kind Gateway
// matching its labels, each with no sectionName or one in capture. When the
// Gateway could not be read (known false), its labels and listeners are
// unknown, so every sectionName captures and every Gateway selector matches.
func targetsGateway(p *unstructured.Unstructured, gwName string, gwLabels map[string]string,
	capture map[string]bool, known bool) bool {
	on := func(m map[string]any) bool {
		section, _ := m["sectionName"].(string)
		return section == "" || !known || capture[section]
	}
	refs, _, _ := unstructured.NestedSlice(p.Object, "spec", "targetRefs")
	for _, x := range refs {
		m, _ := x.(map[string]any)
		if name, _ := m["name"].(string); isGatewayRef(m) && name == gwName && on(m) {
			return true
		}
	}
	sels, _, _ := unstructured.NestedSlice(p.Object, "spec", "targetSelectors")
	for _, x := range sels {
		m, _ := x.(map[string]any)
		want, _ := m["matchLabels"].(map[string]any)
		if isGatewayRef(m) && on(m) && (!known || labelsMatch(want, gwLabels)) {
			return true
		}
	}
	return false
}

// isGatewayRef reports whether a target entry names a Gateway API Gateway.
// An empty group counts too, as isHTTPRouteRef reads it, toward holding.
func isGatewayRef(m map[string]any) bool {
	kind, _ := m["kind"].(string)
	group, _ := m["group"].(string)
	return kind == "Gateway" && (group == gatewayv1.GroupName || group == "")
}

func labelsMatch(want map[string]any, have map[string]string) bool {
	for k, v := range want {
		if s, _ := v.(string); have[k] != s {
			return false
		}
	}
	return true
}
