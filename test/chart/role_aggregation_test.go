// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package chart

import (
	"strings"
	"testing"
)

// Issue #170: TestEveryBindingTheChartRendersBindsOnlyTheOperatorToTheChartsOwnRoles
// pins who a chart role is bound to, but a role can be granted without a
// binding. `rbac.authorization.k8s.io/aggregate-to-edit: "true"` on the
// operator's ClusterRole folds its rules into the built-in `edit`, held by
// every user an administrator lets edit a namespace; an `aggregationRule` on it
// makes its rules whatever any other ClusterRole carrying the selected label
// says. Both are literals in templates/rbac.yaml, past
// TestBindingTemplatesReadOnlyTheValuesTheRenderTestVaries, which reads only
// what a binding template takes from values, so the render has to be read.
// Roles are held to the same rule although the API server aggregates only
// ClusterRoles: the label on a Role is inert, and it is not inert once the
// Role is made a ClusterRole.

// aggregateLabelPrefix is the label key prefix the API server's ClusterRole
// aggregation controller selects on for the built-in admin, edit and view.
const aggregateLabelPrefix = "rbac.authorization.k8s.io/aggregate-to-"

// No Role or ClusterRole the chart renders, under any value set the binding
// test renders, carries an aggregate-to-* label or an aggregationRule: its
// rules reach only the subjects of its own binding, and say only what the
// chart's source says.
func TestNoRoleTheChartRendersAggregatesIntoOrFromAnotherRole(t *testing.T) {
	for _, p := range bindingProfiles() {
		docs := render(t, p.args...)
		seen := 0
		for _, kind := range []string{"ClusterRole", "Role"} {
			for _, d := range kindsOf(docs, kind) {
				seen++
				key := bindingKey(kind, toStr(dig(d, "metadata")["namespace"]), nameOf(d))
				for label := range dig(d, "metadata", "labels") {
					if !strings.HasPrefix(label, aggregateLabelPrefix) {
						continue
					}
					if kind == "ClusterRole" {
						t.Errorf("%s: %s carries the label %s, which grants its rules to every holder of the "+
							"built-in role it names, through no binding the chart renders", p.name, key, label)
					} else {
						t.Errorf("%s: %s carries the label %s: aggregation selects only ClusterRoles, so on a "+
							"Role it grants nothing, and it grants the role's rules to every holder of the "+
							"built-in role it names once the Role becomes a ClusterRole", p.name, key, label)
					}
				}
				if _, ok := d["aggregationRule"]; ok {
					t.Errorf("%s: %s has an aggregationRule, so its rules are whatever ClusterRoles the "+
						"selector matches say, not what the chart ships", p.name, key)
				}
			}
		}
		if seen == 0 {
			t.Errorf("%s: the chart renders no Role or ClusterRole, but it binds the operator to one: "+
				"this test reads nothing", p.name)
		}
	}
}
