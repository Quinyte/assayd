// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"fmt"
	"sort"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/compiler"
)

// foreignTrafficPolicies is design 03 §3.2's detection: the
// `AgentgatewayPolicy` objects in the run namespace that carry `traffic` and
// target this Agent's `<agent>-serving`, by a `targetRefs` entry naming it or
// a `targetSelectors` entry whose labels it carries, and that are not this
// Agent's `<agent>-auth` by the name-and-label rule: the name, and this
// Agent's `assayd.dev/agent-uid`. Sorted, as `<namespace>/<name>`.
//
// Listed LIVE, through the uncached reader: the gateway watch cache holds only
// policies carrying `assayd.dev/agent`, and a foreign policy is exactly one it
// may not hold (GatewayWatchCacheOptions).
//
// Route-level targets only. A policy on the assayd Gateway is A75's
// (authabove.go): read on every ProbingAfter pass and again after a 401, it
// holds a transaction, and on a served Agent it may be W1's, rather than being
// reported here. Measured on 1.5.0: for A74 case 7's shape
// the route's authentication replaces the Gateway's, but authorization rules
// merge across the two, so a Gateway-level Allow widens the route (§3.2, A75).
func (r *AgentReconciler) foreignTrafficPolicies(ctx context.Context, agent *assaydv1alpha1.Agent,
	runNS, route string) ([]string, error) {
	own, err := compiler.AuthPolicyName(agent.Name)
	if err != nil {
		return nil, err
	}
	items, err := r.listPolicies(ctx, runNS, "a foreign traffic policy", true)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, nil
	}
	labels, err := r.servingRouteLabels(ctx, agent, runNS, route)
	if err != nil {
		return nil, err
	}
	var out []string
	for i := range items {
		p := &items[i]
		if p.GetName() == own && p.GetLabels()[compiler.LabelAgentUID] == string(agent.UID) {
			continue
		}
		if _, ok, _ := unstructured.NestedFieldNoCopy(p.Object, "spec", "traffic"); !ok {
			continue
		}
		if policyTargetsRoute(p, route, labels) {
			out = append(out, p.GetNamespace()+"/"+p.GetName())
		}
	}
	sort.Strings(out)
	return out, nil
}

// policyPageSize is how many policies one list call returns.
const policyPageSize = 250

// listPolicies lists every AgentgatewayPolicy in ns, live, through the
// uncached reader, and paged, so one pass holds at most a page at a time. No
// label or field selector can narrow it: a policy this operator did not emit
// carries no label it chose, and a CRD serves no field selector but name and
// namespace (A72 gives the cost). With notServedIsEmpty, a kind that is not
// served lists nothing, as §3.2's foreign detection reads it; without, as
// A75's read needs, it is an error like any other, because a list that cannot
// be made holds there. `purpose` says what the list is for, in the error.
func (r *AgentReconciler) listPolicies(ctx context.Context, ns, purpose string,
	notServedIsEmpty bool) ([]unstructured.Unstructured, error) {
	var items []unstructured.Unstructured
	cont := ""
	for {
		list := &unstructured.UnstructuredList{}
		list.SetAPIVersion(compiler.PolicyAPIVersion)
		list.SetKind(compiler.PolicyKind + "List")
		if err := r.reader().List(ctx, list, client.InNamespace(ns), client.Limit(policyPageSize),
			client.Continue(cont)); err != nil {
			if notServedIsEmpty && apierrors.IsNotFound(err) {
				return nil, nil
			}
			return nil, fmt.Errorf("list the policies in %s to find %s: %w", ns, purpose, err)
		}
		items = append(items, list.Items...)
		if cont = list.GetContinue(); cont == "" {
			return items, nil
		}
	}
}

// servingRouteLabels are the labels a selector is matched against: the stored
// route's, or, while it does not exist, the provenance labels every serving
// route this operator writes carries.
func (r *AgentReconciler) servingRouteLabels(ctx context.Context, agent *assaydv1alpha1.Agent,
	runNS, route string) (map[string]string, error) {
	var rt gatewayv1.HTTPRoute
	switch err := r.Get(ctx, client.ObjectKey{Namespace: runNS, Name: route}, &rt); {
	case err == nil:
		return rt.Labels, nil
	case !apierrors.IsNotFound(err):
		return nil, fmt.Errorf("get route %s in %s: %w", route, runNS, err)
	}
	return map[string]string{
		LabelAgent:          agent.Name,
		LabelAgentNamespace: agent.Namespace,
		LabelAgentUID:       string(agent.UID),
	}, nil
}

// policyTargetsRoute is §3.2's route-level target test.
func policyTargetsRoute(p *unstructured.Unstructured, route string, labels map[string]string) bool {
	refs, _, _ := unstructured.NestedSlice(p.Object, "spec", "targetRefs")
	for _, x := range refs {
		m, _ := x.(map[string]any)
		if name, _ := m["name"].(string); isHTTPRouteRef(m) && name == route {
			return true
		}
	}
	sels, _, _ := unstructured.NestedSlice(p.Object, "spec", "targetSelectors")
	for _, x := range sels {
		m, _ := x.(map[string]any)
		if !isHTTPRouteRef(m) {
			continue
		}
		want, _ := m["matchLabels"].(map[string]any)
		matched := true
		for k, v := range want {
			if s, _ := v.(string); labels[k] != s {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

func isHTTPRouteRef(m map[string]any) bool {
	kind, _ := m["kind"].(string)
	group, _ := m["group"].(string)
	return kind == "HTTPRoute" && (group == gatewayv1.GroupName || group == "")
}
