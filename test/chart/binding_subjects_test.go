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
// TestRBACTemplatesReadOnlyTheValuesTheRenderTestVaries covers that half at
// the source: a template that writes a role or a binding, and every helper it
// includes, may read only the values bindingProfiles varies. The render tests
// that read roles, here and in role_aggregation_test.go, see only
// bindingProfiles; the guard covers the rest.

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
// templates/rbac.yaml reads, gateway.hostnameSuffix and operator.image.tag,
// which assayd.validate reads, and sets the operator and gateway values no
// role or binding template may read, so the render half sees them too.
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
				"--set", "gateway.hostnameSuffix=example.test", "--set", "operator.metrics.enabled=true",
				"--set", "operator.image.tag=0.0.0-moved"),
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

// rbacTemplateValues are the only values that a template writing a role or a
// binding, or any helper it includes, may read. Each is one that bindingProfiles
// sets off its default, or one that has a single value the chart renders at
// all, so whatever a role or binding does with it is rendered and held to the
// tests that read them.
var rbacTemplateValues = map[string]bool{
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
	// assayd.validate refuses a value that is not a lowercase DNS name. Set by
	// the operator-moved profile.
	"gateway.hostnameSuffix": true,
	// assayd.validate refuses an empty tag and `latest`. Moved by the
	// operator-moved profile.
	"operator.image.tag": true,
	// assayd.validate and assayd.labels read it. prod and local across
	// renderMatrix, which are the only values assayd.validate accepts.
	"profile": true,
	// assayd.validate and assayd.labels read it. assayd.validate refuses
	// every value but core, so every render that succeeds sees the one value
	// an install can have.
	"tier": true,
}

// rbacTemplateIncludes are the only helpers that a template writing a role or a
// binding may include. The guard reads each one's define body, transitively,
// and holds it to the same rules as the template.
var rbacTemplateIncludes = map[string]bool{
	"assayd.validate":      true,
	"assayd.operator.name": true,
	"assayd.labels":        true,
	"assayd.name":          true,
}

var (
	// rbacKind matches any mention of a role or binding kind: Role,
	// ClusterRole, RoleBinding and ClusterRoleBinding, as whole words, so a
	// conditional one counts. ValidatingAdmissionPolicyBinding does not match,
	// nor does a plural in prose.
	rbacKind = regexp.MustCompile(`\b(Cluster)?Role(Binding)?\b`)
	// yamlCommentLine is a YAML comment line with no template action on it. It
	// renders nothing a manifest can carry, so a kind it names is prose:
	// admission.yaml explains the operator's ClusterRole in one.
	yamlCommentLine = regexp.MustCompile(`(?m)^[ \t]*#[^{\n]*$`)
	// valuesRef matches every reference to .Values, with its dotted path:
	// `.Values.gateway.name`, `$.Values.namespace`, or bare `.Values`, whose
	// empty path no allowlist entry matches.
	valuesRef = regexp.MustCompile(`\.Values((?:\.[A-Za-z0-9_]+)*)`)
	// nilSafeRead is `(.Values.gateway | default dict).hostnameSuffix`, which
	// assayd.validate uses to read a key under a map that may be null. It
	// reads `.Values.gateway.hostnameSuffix`, and is checked as that.
	nilSafeRead = regexp.MustCompile(`\(\s*\.Values((?:\.[A-Za-z0-9_]+)*)\s*\|\s*default\s+dict\s*\)((?:\.[A-Za-z0-9_]+)+)`)
	includeOf   = regexp.MustCompile(`include\s+"([^"]+)"`)
	// filesRef matches every use of .Files; filesGet is the one form a
	// template may use, a literal path under files/, which the guard scans.
	filesRef = regexp.MustCompile(`\.Files\b[.A-Za-z]*(\s+"[^"]*")?`)
	filesGet = regexp.MustCompile(`^\.Files\.Get "files/[^"]+"$`)
	// releaseInterpolation is an action that only prints the release's name or
	// its service, as assayd.labels does in two label values. It prints a
	// string and gates nothing, so rbacTemplateForbidden's `.Release` does
	// not apply to it. Any other use of .Release does.
	releaseInterpolation = regexp.MustCompile(`\{\{-?\s*\.Release\.(Name|Service)\s*-?\}\}`)
	defineName           = regexp.MustCompile(`^define\s+"([^"]+)"`)
)

// rbacTemplateForbidden are what a file that writes a role or a binding, or a
// helper it includes, may not use. Each is a way to gate or write one that
// neither valuesRef nor the render test sees.
var rbacTemplateForbidden = []struct {
	re  *regexp.Regexp
	why string
}{
	// The API versions of the cluster the chart is installed into; a render
	// reports the defaults Helm assumes, so a gate on them is never taken here.
	{regexp.MustCompile(`\.Capabilities\b`), "gates on the cluster, which no render here varies"},
	// IsUpgrade, IsInstall, Revision, and the release's name and namespace:
	// every render here is one install of release assayd.
	{regexp.MustCompile(`\.Release\b`), "gates on the release, which no render here varies"},
	// A live read of the cluster; `helm template` gets an empty answer.
	{regexp.MustCompile(`\blookup\s`), "gates on a live cluster read, which a render does not make"},
	// Renders a string as a template, which can hold any of the above.
	{regexp.MustCompile(`(^|[\s({|])tpl\s`), "renders text as a template, which this guard cannot read"},
}

var (
	// templateAction is one `{{ … }}` action, where the checks below apply.
	templateAction = regexp.MustCompile(`(?s)\{\{.*?\}\}`)
	// actionKeyword is the first word of an action, past its delimiters.
	actionKeyword = regexp.MustCompile(`^\{\{-?\s*([a-z]+)`)
	// rootKeyLiteral is a string naming one of Helm's root objects. The root
	// context is a map, so `index . "Release"`, `get $ "Files"` or
	// `pluck "Values" .` reach that object without writing `.Release`.
	rootKeyLiteral = regexp.MustCompile("[\"`](Values|Release|Capabilities|Files|Template|Chart|Subcharts)[\"`]")
	// rootIndex is `index` applied to the root context.
	rootIndex = regexp.MustCompile(`\bindex\s+[$.][\s)]`)
	// mapFunc and rootArg together are the root context passed to a map
	// function.
	mapFunc = regexp.MustCompile(`\b(get|pluck|dig)\b`)
	rootArg = regexp.MustCompile(`(^|[\s(])[$.]([\s)]|$)`)
)

// chartFilesNotInFiles are the files at the chart root that `.Files` cannot
// read: Helm loads them as the chart's metadata and values, not as files
// (measured with helm v4.2.3: `.Files.Get` of each returns ""). values.yaml
// mentions the gateway RoleBinding in a comment, and cannot be rendered.
var chartFilesNotInFiles = map[string]bool{"Chart.yaml": true, "values.yaml": true}

// templatesNeverApplied are the templates Helm renders only to print after an
// install, never as manifests, so a kind they name is prose.
var templatesNeverApplied = map[string]bool{"NOTES.txt": true}

// defineBodies returns the body of every `define` in src, by name. Actions that
// open a block — define, if, range, with, block — are matched with their
// `end`, so a define's body runs to its own end and not to the first one in it.
func defineBodies(src string) (map[string]string, error) {
	type frame struct {
		name  string
		start int
	}
	bodies := map[string]string{}
	var stack []frame
	for _, loc := range templateAction.FindAllStringIndex(src, -1) {
		action := src[loc[0]:loc[1]]
		kw := actionKeyword.FindStringSubmatch(action)
		if kw == nil {
			continue
		}
		switch kw[1] {
		case "define":
			m := defineName.FindStringSubmatch(strings.TrimLeft(strings.TrimPrefix(action, "{{"), "- \t\n"))
			if m == nil {
				return nil, fmt.Errorf("cannot read the define name in %s", action)
			}
			stack = append(stack, frame{name: m[1], start: loc[1]})
		case "if", "range", "with", "block":
			stack = append(stack, frame{start: loc[1]})
		case "end":
			if len(stack) == 0 {
				return nil, fmt.Errorf("%s closes nothing", action)
			}
			f := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if f.name != "" {
				if _, dup := bodies[f.name]; dup {
					return nil, fmt.Errorf("%q is defined twice", f.name)
				}
				bodies[f.name] = src[f.start:loc[0]]
			}
		}
	}
	if len(stack) != 0 {
		return nil, fmt.Errorf("%d blocks are never closed", len(stack))
	}
	return bodies, nil
}

// checkRBACSource holds src, a template that writes a role or a binding or a
// helper it includes, to rbacTemplateForbidden, rbacTemplateValues and
// rbacTemplateIncludes, then does the same for each helper it includes,
// once each. where names src in a message; root is the template that writes
// the role or binding.
func checkRBACSource(t *testing.T, where, root, src string, defines map[string]string, seen map[string]bool) {
	t.Helper()
	gated := releaseInterpolation.ReplaceAllString(src, "")
	for _, f := range rbacTemplateForbidden {
		if loc := f.re.FindString(gated); loc != "" {
			t.Errorf("%s uses %q: it %s", where, strings.TrimSpace(loc), f.why)
		}
	}
	for _, m := range valuesRef.FindAllStringSubmatch(nilSafeRead.ReplaceAllString(src, ".Values$1$2"), -1) {
		path := strings.TrimPrefix(m[1], ".")
		if !rbacTemplateValues[path] {
			t.Errorf("%s reads %s, which bindingProfiles does not vary: a role or binding templated on it "+
				"renders past every value set the render tests read", where, m[0])
		}
	}
	for _, m := range includeOf.FindAllStringSubmatch(src, -1) {
		name := m[1]
		if !rbacTemplateIncludes[name] {
			t.Errorf("%s includes %q, which is not in rbacTemplateIncludes: what it reads is not checked here",
				where, name)
			continue
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		body, ok := defines[name]
		if !ok {
			t.Errorf("%s includes %q, which no template under templates/ defines: the guard cannot read it",
				where, name)
			continue
		}
		checkRBACSource(t, fmt.Sprintf("the helper %q, included by %s,", name, root), root, body, defines, seen)
	}
}

// The render tests see only bindingProfiles, so this guard reads the chart's
// source for what the profiles cannot vary.
//
//   - Every template may reach Helm's root objects only by name, as
//     `.Values`, `.Release` and so on: no string naming one, no `index` of the
//     root, and no root passed to get, pluck or dig.
//   - Every template may use .Files only as `.Files.Get "files/<path>"`, and no
//     chart file outside templates/ and crds/ that .Files can read may mention
//     a role or binding kind.
//   - Every template that mentions a role or binding kind outside a YAML
//     comment line may read only rbacTemplateValues, include only
//     rbacTemplateIncludes, and use none of rbacTemplateForbidden. So may the
//     define body of each helper it includes, and of each helper those
//     include. The check is per file, not per document, because a conditional
//     that wraps a role opens before its `---`. NOTES.txt is exempt, because
//     Helm never applies it.
//
// The limit, stated plainly: this is a lexical check of source text. A key, a
// kind or a path built at render time — with printf, cat, or a concatenated
// string, as in `Cluster{{ "Role" }}` or `index . (printf "Rel%s" "ease")` —
// is not seen here, and is seen only by the render tests, under
// bindingProfiles. Nor is a packaged subchart under charts/, whose templates
// sit compressed inside a .tgz this check reads as bytes; the chart vendors
// none today. A value is allowed or not by its path, so a template that reads
// an allowed one to write a role is caught only where bindingProfiles renders
// what it writes. And the root-as-map check cannot tell the root from a range
// or with element, so `get . "k"` inside one fails here too: it fails closed.
func TestRBACTemplatesReadOnlyTheValuesTheRenderTestVaries(t *testing.T) {
	err := filepath.WalkDir(chartPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(chartPath, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel == "templates" || rel == "crds" {
				return filepath.SkipDir
			}
			return nil
		}
		if chartFilesNotInFiles[rel] {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if rbacKind.Match(src) {
			t.Errorf("%s mentions a role or binding kind: a template can render it with .Files.Get, under a "+
				"gate this guard does not read", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(chartPath, "templates")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	sources := map[string]string{}
	defines := map[string]string{}
	for _, e := range entries {
		if e.IsDir() {
			t.Errorf("templates/%s is a directory, which this guard does not read: Helm renders it", e.Name())
			continue
		}
		src, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		sources[e.Name()] = string(src)
		bodies, err := defineBodies(string(src))
		if err != nil {
			t.Errorf("templates/%s: %v", e.Name(), err)
			continue
		}
		for name, body := range bodies {
			if _, dup := defines[name]; dup {
				t.Errorf("templates/%s defines %q again: which body Helm uses is not what this guard reads",
					e.Name(), name)
			}
			defines[name] = body
		}
	}

	scanned := 0
	for _, e := range entries {
		src, ok := sources[e.Name()]
		if !ok {
			continue
		}
		for _, action := range templateAction.FindAllString(src, -1) {
			if m := rootKeyLiteral.FindString(action); m != "" {
				t.Errorf("templates/%s names the root object %s in %s: reached by key, it is past every "+
					"check here", e.Name(), m, action)
			}
			if rootIndex.MatchString(action) || (mapFunc.MatchString(action) && rootArg.MatchString(action)) {
				t.Errorf("templates/%s reads the root context as a map in %s: every root object is reachable "+
					"that way, past every check here", e.Name(), action)
			}
		}
		for _, m := range filesRef.FindAllString(src, -1) {
			if !filesGet.MatchString(m) {
				t.Errorf("templates/%s uses %s: a template reads chart files only as "+
					"`.Files.Get \"files/<path>\"`, the directory this guard scans", e.Name(), m)
			}
		}
		if templatesNeverApplied[e.Name()] || !rbacKind.MatchString(yamlCommentLine.ReplaceAllString(src, "")) {
			continue
		}
		scanned++
		root := "templates/" + e.Name()
		checkRBACSource(t, root+", which writes a role or a binding,", root, src, defines, map[string]bool{})
	}
	if scanned == 0 {
		t.Error("no template mentions a role or binding kind, but the chart binds the operator: the guard reads nothing")
	}
}
