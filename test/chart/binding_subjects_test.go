// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package chart

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
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
//
// A render sees only the value sets it is given. A binding templated on any
// other value — `{{ if .Values.operator.metrics.enabled }}`, say — renders
// past every one of them, so the render test alone cannot see it.
// TestBindingTemplatesReadOnlyTheValuesTheRenderTestVaries covers that half
// at the source: a template that writes a binding may read only the values
// bindingProfiles varies.

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

// identityArgs set every identity value the chart has to a non-default value.
var identityArgs = []string{
	"--set", "admission.extraOperators={system:serviceaccount:tenancy:tenant-operator,platform-bot}",
	"--set", "admission.toolRouteWriters.users={tool-author}",
	"--set", "admission.toolRouteWriters.groups={tool-authors}",
	"--set", "admission.apiKeyWriters.users={alice}",
	"--set", "admission.apiKeyWriters.groups={key-admins}",
}

// defaultBindingProfile is a value set that leaves namespace and
// gateway.namespace at their defaults, so the operator and, when the gateway
// is on, its Roles are in assayd-system.
func defaultBindingProfile(name string, args []string) bindingProfile {
	p := bindingProfile{name: name, args: args, namespace: "assayd-system"}
	if contains(args, "gateway.enabled=true") {
		p.gateway, p.gatewayNamespace = true, "assayd-system"
	}
	return p
}

// bindingProfiles are renderMatrix's four rows, every value set the
// EvalSuite-writer tests render the chart under, and the identity values set
// with the gateway on, off, in the operator's namespace and in another. The
// last of those also moves gateway.name off its default, which
// templates/rbac.yaml reads, and sets the operator and gateway values no
// binding template may read, so the render half sees them too.
func bindingProfiles() []bindingProfile {
	gatewayOn := []string{"--set", "gateway.enabled=true", "--set", "gateway.servingUrl=http://gw.example:8080"}
	local := []string{"-f", filepath.Join(chartPath, "values-local.yaml")}
	var ps []bindingProfile
	for _, row := range renderMatrix {
		ps = append(ps, defaultBindingProfile("renderMatrix "+row.name, row.args))
	}
	for i, args := range evalSuiteWriterValueSets() {
		ps = append(ps, defaultBindingProfile(fmt.Sprintf("EvalSuite-writer value set %d %v", i, args), args))
	}
	ps = append(ps,
		defaultBindingProfile("identities set", identityArgs),
		defaultBindingProfile("identities set, gateway enabled", append(append([]string{}, gatewayOn...), identityArgs...)),
		defaultBindingProfile("local, identities set, gateway enabled",
			append(append(append([]string{}, local...), gatewayOn...), identityArgs...)),
		bindingProfile{
			name: "identities set, gateway renamed in its own namespace, operator moved",
			args: append(append(append([]string{}, gatewayOn...), identityArgs...),
				"--set", "gateway.namespace=agentgateway-system", "--set", "namespace=assayd-elsewhere",
				"--set", "gateway.name=gw", "--set", "gateway.url=http://gw.example:8081",
				"--set", "gateway.hostnameSuffix=example.test", "--set", "operator.metrics.enabled=true"),
			namespace: "assayd-elsewhere", gatewayNamespace: "agentgateway-system", gateway: true,
		},
	)
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

// bindingTemplateValues are the only values a template that writes a binding
// may read. Each is one bindingProfiles sets off its default, so whatever a
// binding does with it is rendered and held to the table above.
var bindingTemplateValues = map[string]bool{
	// The namespace of the operator ServiceAccount, every binding's one
	// subject. Moved by the operator-moved profile.
	"namespace": true,
	// Whether the gateway Roles and their RoleBindings render at all. Off and
	// on across renderMatrix.
	"gateway.enabled": true,
	// Where the gateway Roles and RoleBindings are. Moved by the operator-moved
	// profile.
	"gateway.namespace": true,
	// The one Gateway the gateway-labels Role names in resourceNames. Renamed
	// by the operator-moved profile.
	"gateway.name": true,
}

// bindingTemplateIncludes are the only helpers a template that writes a binding
// may include. Each renders a fixed string or labels, or fails the render; a
// helper file that itself wrote a binding would be scanned below like any
// other template.
var bindingTemplateIncludes = map[string]bool{
	"assayd.validate":      true,
	"assayd.operator.name": true,
	"assayd.labels":        true,
}

var (
	// bindingKind matches any mention of a binding kind, so a commented or
	// conditional one counts; ValidatingAdmissionPolicyBinding does not match.
	bindingKind = regexp.MustCompile(`(Cluster)?RoleBinding`)
	// valuesRef matches every reference to .Values, with its dotted path:
	// `.Values.gateway.name`, `$.Values.namespace`, or bare `.Values`, whose
	// empty path no allowlist entry matches.
	valuesRef = regexp.MustCompile(`\.Values((?:\.[A-Za-z0-9_]+)*)`)
	includeOf = regexp.MustCompile(`include\s+"([^"]+)"`)
)

// The render test above sees only bindingProfiles. So every file under
// templates/, helpers and NOTES.txt included, that mentions a binding kind may
// read only bindingTemplateValues and include only bindingTemplateIncludes. The
// check is per file, not per document, because a conditional that wraps a
// binding opens before its `---`. A stated limit: a kind spelt out of pieces
// (`ClusterRole{{ "Binding" }}`), or read from a file with .Files.Get, is not
// seen here, and is seen by the render test only under bindingProfiles.
func TestBindingTemplatesReadOnlyTheValuesTheRenderTestVaries(t *testing.T) {
	dir := filepath.Join(chartPath, "templates")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	scanned := 0
	for _, e := range entries {
		if e.IsDir() {
			t.Errorf("templates/%s is a directory, which this guard does not read: Helm renders it", e.Name())
			continue
		}
		src, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if !bindingKind.Match(src) {
			continue
		}
		scanned++
		for _, m := range valuesRef.FindAllStringSubmatch(string(src), -1) {
			path := strings.TrimPrefix(m[1], ".")
			if !bindingTemplateValues[path] {
				t.Errorf("templates/%s writes a binding and reads %s, which bindingProfiles does not vary: "+
					"a binding templated on it renders past every value set the render test reads", e.Name(), m[0])
			}
		}
		for _, m := range includeOf.FindAllStringSubmatch(string(src), -1) {
			if !bindingTemplateIncludes[m[1]] {
				t.Errorf("templates/%s writes a binding and includes %q, which is not in bindingTemplateIncludes: "+
					"what it reads is not checked here", e.Name(), m[1])
			}
		}
	}
	if scanned == 0 {
		t.Error("no template mentions a binding kind, but the chart binds the operator: the guard reads nothing")
	}
}
