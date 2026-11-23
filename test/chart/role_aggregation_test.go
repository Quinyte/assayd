// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package chart

import (
	"sort"
	"strings"
	"testing"
)

// Issue #170: TestEveryBindingTheChartRendersBindsOnlyTheOperatorToTheChartsOwnRoles
// pins who a chart role is bound to, but kube-controller-manager's
// clusterrole-aggregation controller grants a ClusterRole's rules with no
// binding. The built-in admin, edit and view roles carry aggregationRules that
// select labels under rbac.authorization.k8s.io/aggregate-to-, so
// `rbac.authorization.k8s.io/aggregate-to-edit: "true"` on the operator's
// ClusterRole has it selected by `edit`, and through `edit` by every role
// that aggregates that one. Another key under the prefix, or another value,
// is selected only if some role's aggregationRule selects it. An aggregationRule on a chart ClusterRole makes
// its rules whatever the ClusterRoles its selectors match say.
//
// What this pins, and no more: no Role or ClusterRole the chart renders carries
// a label key under that prefix, or an aggregationRule. The controller honours
// any label selector in any ClusterRole's aggregationRule, so a ClusterRole the
// cluster carries can select a third-party aggregation label, or one the chart
// already ships, such as app.kubernetes.io/part-of. What else aggregates a
// chart role depends on what the cluster carries, and no render can pin it.
//
// Roles are held to the same rule although the controller aggregates only
// ClusterRoles: the label on a Role is inert, and it is not inert once the
// Role is made a ClusterRole.
//
// The render sees only bindingProfiles. A role gated on a value no profile
// sets renders past it; TestRBACTemplatesReadOnlyTheValuesTheRenderTestVaries
// covers that half at the source, against a careless change and not a
// determined one, as its threat model says.

// aggregateLabelPrefix is the label key prefix that the built-in admin, edit
// and view ClusterRoles' aggregationRules select on.
const aggregateLabelPrefix = "rbac.authorization.k8s.io/aggregate-to-"

// No Role or ClusterRole the chart renders, under any value set the binding
// test renders, carries a label key under the prefix the built-in roles
// aggregate on, or an aggregationRule.
func TestNoRoleTheChartRendersCarriesABuiltInAggregationLabelOrAnAggregationRule(t *testing.T) {
	for _, p := range bindingProfiles() {
		docs := render(t, p.args...)
		seen := 0
		for _, kind := range []string{"ClusterRole", "Role"} {
			for _, d := range kindsOf(docs, kind) {
				seen++
				key := bindingKey(kind, toStr(dig(d, "metadata")["namespace"]), nameOf(d))
				var labels []string
				for label := range dig(d, "metadata", "labels") {
					if strings.HasPrefix(label, aggregateLabelPrefix) {
						labels = append(labels, label)
					}
				}
				sort.Strings(labels)
				for _, label := range labels {
					if kind == "ClusterRole" {
						t.Errorf("%s: %s carries the label %s, under the prefix the built-in roles aggregate on: "+
							"a built-in role may select it, and kube-controller-manager's clusterrole-aggregation "+
							"controller then copies its rules into that role, and through it into every role that "+
							"aggregates that one, with no binding the chart renders", p.name, key, label)
					} else {
						t.Errorf("%s: %s carries the label %s, under the prefix the built-in roles aggregate on: "+
							"the controller aggregates only ClusterRoles, so on a Role it is inert, and once the "+
							"Role is made a ClusterRole a built-in role may select it",
							p.name, key, label)
					}
				}
				if _, ok := d["aggregationRule"]; !ok {
					continue
				}
				if kind == "ClusterRole" {
					t.Errorf("%s: %s has an aggregationRule, so the clusterrole-aggregation controller replaces "+
						"its rules with those of the ClusterRoles its selectors match on the cluster, not what "+
						"the chart ships", p.name, key)
				} else {
					t.Errorf("%s: %s has an aggregationRule, which rbac/v1 Role has no field for: the API server "+
						"drops it or, under strict field validation, refuses the Role", p.name, key)
				}
			}
		}
		if seen == 0 {
			t.Errorf("%s: the chart renders no Role or ClusterRole, but it binds the operator to one: "+
				"this test reads nothing", p.name)
		}
	}
}
