// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package chart

import (
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

// Issue #127: the rules of the chart's Roles and ClusterRoles are pinned
// (TestChartRBACMatchesGeneratedRules, TestTheOperatorOnlyReadsEvalSuites),
// but nothing pinned WHO they are bound to. `system:authenticated` added to the
// operator's ClusterRoleBinding, or a ClusterRoleBinding of the operator to the
// built-in `cluster-admin` templated under a value, passed every test. This
// pins both halves of every binding the chart renders: its subjects and its
// roleRef.
//
// The identity values — admission.extraOperators, admission.toolRouteWriters
// and admission.apiKeyWriters — reach admission policy CEL and no RBAC
// binding: the chart binds them to nothing. So under every value set the only
// subject of every binding is the operator's ServiceAccount, and a value set
// that names other identities is rendered precisely so that one templated into
// a binding fails here.

// bindingProfile is one value set the chart is rendered under, with the
// namespaces the expectation table below is resolved against.
type bindingProfile struct {
	name string
	args []string
	// namespace is .Values.namespace, where the operator's ServiceAccount is.
	namespace string
	// gatewayNamespace is where the gateway Roles are: gateway.namespace, or
	// namespace when unset. Read only when gateway is true.
	gatewayNamespace string
	gateway          bool
}

const gatewayServingURL = "http://gw.example:8080"

// identityArgs set every identity value the chart has to a non-default value.
var identityArgs = []string{
	"--set", "admission.extraOperators={system:serviceaccount:tenancy:tenant-operator,platform-bot}",
	"--set", "admission.toolRouteWriters.users={tool-author}",
	"--set", "admission.toolRouteWriters.groups={tool-authors}",
	"--set", "admission.apiKeyWriters.users={alice}",
	"--set", "admission.apiKeyWriters.groups={key-admins}",
}

// bindingProfiles are renderMatrix's four rows, every value set the
// EvalSuite-writer tests render the chart under, and the identity values set
// with the gateway on, off, in the operator's namespace and in another.
func bindingProfiles() []bindingProfile {
	gatewayOn := []string{"--set", "gateway.enabled=true", "--set", "gateway.servingUrl=" + gatewayServingURL}
	local := []string{"-f", filepath.Join(chartPath, "values-local.yaml")}
	ps := []bindingProfile{
		{"extraOperators set", []string{"--set", "admission.extraOperators={tenant-operator}"}, "assayd-system", "", false},
		{"toolRouteWriters.users set", []string{"--set", "admission.toolRouteWriters.users={tool-author}"}, "assayd-system", "", false},
		{"gateway enabled, served elsewhere", []string{"--set", "gateway.enabled=true",
			"--set", "gateway.servingUrl=http://assayd-gateway.agentgateway-system.svc"}, "assayd-system", "assayd-system", true},
		{"identities set", identityArgs, "assayd-system", "", false},
		{"identities set, gateway enabled", append(append([]string{}, gatewayOn...), identityArgs...),
			"assayd-system", "assayd-system", true},
		{"identities set, gateway in its own namespace, operator moved",
			append(append(append([]string{}, gatewayOn...), identityArgs...),
				"--set", "gateway.namespace=agentgateway-system", "--set", "namespace=assayd-elsewhere"),
			"assayd-elsewhere", "agentgateway-system", true},
		{"local, identities set, gateway enabled",
			append(append(append([]string{}, local...), gatewayOn...), identityArgs...),
			"assayd-system", "assayd-system", true},
	}
	for _, row := range renderMatrix {
		gw := contains(row.args, "gateway.enabled=true")
		ns := ""
		if gw {
			ns = "assayd-system"
		}
		ps = append(ps, bindingProfile{"renderMatrix " + row.name, row.args, "assayd-system", ns, gw})
	}
	return ps
}

// expectedBinding is one row of the table: a binding the chart renders, the
// role it must name, and its subjects, exactly.
type expectedBinding struct {
	kind, name, namespace string
	roleKind, roleName    string
	subjects              []map[string]any
}

const operatorName = "assayd-agent-operator"

// expectedBindings is the table, resolved for p. A binding the chart renders
// that is not here fails, as does one here the chart does not render.
func expectedBindings(p bindingProfile) []expectedBinding {
	operator := []map[string]any{{"kind": "ServiceAccount", "name": operatorName, "namespace": p.namespace}}
	want := []expectedBinding{
		{"ClusterRoleBinding", operatorName, "", "ClusterRole", operatorName, operator},
	}
	if p.gateway {
		want = append(want,
			expectedBinding{"RoleBinding", operatorName + "-gateway-events", p.gatewayNamespace,
				"Role", operatorName + "-gateway-events", operator},
			expectedBinding{"RoleBinding", operatorName + "-gateway-labels", p.gatewayNamespace,
				"Role", operatorName + "-gateway-labels", operator},
		)
	}
	return want
}

func bindingKey(kind, namespace, name string) string {
	if namespace == "" {
		return fmt.Sprintf("%s %q", kind, name)
	}
	return fmt.Sprintf("%s %s/%s", kind, namespace, name)
}

// Every ClusterRoleBinding and RoleBinding the chart renders, under every
// value set above, is a row of the table: its subjects are exactly the
// operator's ServiceAccount — no `system:authenticated`, no
// `system:serviceaccounts`, no configured identity — and its roleRef names
// the Role or ClusterRole the table says, which the same render must contain,
// so a binding to the built-in `cluster-admin` fails twice over.
func TestEveryBindingTheChartRendersBindsOnlyTheOperatorToTheChartsOwnRoles(t *testing.T) {
	for _, p := range bindingProfiles() {
		docs := render(t, p.args...)

		roles := map[string]bool{}
		for _, d := range kindsOf(docs, "ClusterRole") {
			roles[bindingKey("ClusterRole", "", nameOf(d))] = true
		}
		for _, d := range kindsOf(docs, "Role") {
			roles[bindingKey("Role", toStr(dig(d, "metadata")["namespace"]), nameOf(d))] = true
		}

		want := map[string]expectedBinding{}
		for _, b := range expectedBindings(p) {
			want[bindingKey(b.kind, b.namespace, b.name)] = b
		}

		got := map[string]bool{}
		for _, kind := range []string{"ClusterRoleBinding", "RoleBinding"} {
			for _, d := range kindsOf(docs, kind) {
				namespace := toStr(dig(d, "metadata")["namespace"])
				key := bindingKey(kind, namespace, nameOf(d))
				if got[key] {
					t.Errorf("%s: the chart renders %s twice; the second replaces the first at install", p.name, key)
				}
				got[key] = true

				ref := dig(d, "roleRef")
				refKind, refName := toStr(ref["kind"]), toStr(ref["name"])
				refNamespace := ""
				if refKind == "Role" {
					refNamespace = namespace
				}
				if ref["apiGroup"] != "rbac.authorization.k8s.io" || !roles[bindingKey(refKind, refNamespace, refName)] {
					t.Errorf("%s: %s binds %s %q, which this render does not contain: a binding grants only "+
						"what a role the chart ships and pins grants, never a built-in or a role from elsewhere",
						p.name, key, refKind, refName)
				}

				subjects := make([]map[string]any, 0)
				for _, s := range toList(d["subjects"]) {
					subjects = append(subjects, dig2(s))
				}

				w, ok := want[key]
				if !ok {
					t.Errorf("%s: the chart renders %s, binding %s %q to %v, and the table has no such binding: "+
						"add it to expectedBindings only if the chart is meant to grant it", p.name, key, refKind, refName, subjects)
					continue
				}
				if refKind != w.roleKind || refName != w.roleName {
					t.Errorf("%s: %s binds %s %q, want %s %q", p.name, key, refKind, refName, w.roleKind, w.roleName)
				}
				if !reflect.DeepEqual(subjects, w.subjects) {
					t.Errorf("%s: %s binds subjects %v, want exactly %v: every subject of a chart binding "+
						"holds the operator's rights", p.name, key, subjects, w.subjects)
				}
			}
		}

		var missing []string
		for key := range want {
			if !got[key] {
				missing = append(missing, key)
			}
		}
		sort.Strings(missing)
		for _, key := range missing {
			t.Errorf("%s: the chart renders no %s, which the table expects: the expectation is stale "+
				"or the operator has lost a grant it needs", p.name, key)
		}
	}
}
