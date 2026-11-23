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
	"text/template/parse"
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
// binding, or any template it calls, may read. Each is one that bindingProfiles
// sets off its default, or one with a single value the chart renders at all.
//
// A value is allowed by its path, not by what is done with it. The four
// boolean or enumerated entries below take every value a render accepts
// across bindingProfiles. The rest are free strings: bindingProfiles renders
// one or two of them, and a gate such as `eq .Values.operator.image.tag
// "v9.9.9"` renders past every profile and is not seen here.
var rbacTemplateValues = map[string]bool{
	// The namespace of the operator ServiceAccount, every binding's one
	// subject. A free string; moved by the operator-moved profile.
	"namespace": true,
	// Whether the gateway Roles and their RoleBindings render at all. Off and
	// on across renderMatrix.
	"gateway.enabled": true,
	// Where the gateway Roles and RoleBindings are. A free string; moved by
	// the operator-moved profile.
	"gateway.namespace": true,
	// The one Gateway the gateway-labels Role names in resourceNames. A free
	// string; renamed by the operator-moved profile.
	"gateway.name": true,
	// assayd.validate refuses a value that is not a lowercase DNS name. A free
	// string; set by the operator-moved profile.
	"gateway.hostnameSuffix": true,
	// assayd.validate refuses an empty tag and `latest`. A free string; moved
	// by the operator-moved profile.
	"operator.image.tag": true,
	// assayd.validate and assayd.labels read it. prod and local across
	// renderMatrix, the only values assayd.validate accepts.
	"profile": true,
	// assayd.validate and assayd.labels read it. assayd.validate refuses
	// every value but core, so every render that succeeds sees the one value
	// an install can have.
	"tier": true,
}

// rbacKind matches any mention of a role or binding kind: Role, ClusterRole,
// RoleBinding and ClusterRoleBinding, as whole words, so a conditional one
// counts. ValidatingAdmissionPolicyBinding does not match, nor does a plural
// in prose.
var rbacKind = regexp.MustCompile(`\b(Cluster)?Role(Binding)?\b`)

// helmRootObjects are the fields of the root context Helm renders a template
// with.
var helmRootObjects = map[string]bool{
	"Values": true, "Release": true, "Capabilities": true, "Files": true,
	"Template": true, "Chart": true, "Subcharts": true,
}

// rootAsMap are the functions that, given the root context, reach any of
// helmRootObjects by a key: `index . "Release"`, `get $ "Files"`.
var rootAsMap = map[string]bool{"index": true, "get": true, "pluck": true, "dig": true}

// chartFilesNotInFiles are the files at the chart root that `.Files` cannot
// read: Helm loads them as the chart's metadata and values, not as files
// (measured with helm v4.2.3: `.Files.Get` of each returns ""). values.yaml
// mentions the gateway RoleBinding in a comment, and cannot be rendered.
var chartFilesNotInFiles = map[string]bool{"Chart.yaml": true, "values.yaml": true}

// notesFile is rendered only to print after an install, never applied as a
// manifest, so its top level is not a role tree. The templates it defines are
// global, and are read like any other.
const notesFile = "templates/NOTES.txt"

// chartTree is one parsed template: a file's top level, or one `define`.
type chartTree struct {
	// key is "templates/<file>" for a top level and the define's name for a
	// define.
	key   string
	top   bool
	tree  *parse.Tree
	calls []string
}

// mentionsRBACKind reports whether text renders a role or binding kind outside
// a YAML comment. A line that starts with `#` is a comment only where it is
// known to start a rendered line: after a newline in the same text, or at the
// very start of the file. Elsewhere it may follow an action's output on the
// same line, and is read as it stands.
func mentionsRBACKind(n *parse.TextNode) bool {
	lines := strings.Split(string(n.Text), "\n")
	for i, line := range lines {
		atLineStart := i > 0 || n.Pos == 0
		if atLineStart && strings.HasPrefix(strings.TrimLeft(line, " \t"), "#") {
			continue
		}
		if rbacKind.MatchString(line) {
			return true
		}
	}
	return false
}

// walk calls visit on n and, while visit returns true, on every node under it.
func walk(n parse.Node, visit func(parse.Node) bool) {
	if !visit(n) {
		return
	}
	branch := func(b *parse.BranchNode) {
		walk(b.Pipe, visit)
		walk(b.List, visit)
		if b.ElseList != nil {
			walk(b.ElseList, visit)
		}
	}
	switch n := n.(type) {
	case *parse.ListNode:
		for _, c := range n.Nodes {
			walk(c, visit)
		}
	case *parse.ActionNode:
		walk(n.Pipe, visit)
	case *parse.PipeNode:
		for _, d := range n.Decl {
			walk(d, visit)
		}
		for _, c := range n.Cmds {
			walk(c, visit)
		}
	case *parse.CommandNode:
		for _, a := range n.Args {
			walk(a, visit)
		}
	case *parse.IfNode:
		branch(&n.BranchNode)
	case *parse.RangeNode:
		branch(&n.BranchNode)
	case *parse.WithNode:
		branch(&n.BranchNode)
	case *parse.TemplateNode:
		if n.Pipe != nil {
			walk(n.Pipe, visit)
		}
	case *parse.ChainNode:
		walk(n.Node, visit)
	}
}

// identifier returns the name of n if it is a function identifier.
func identifier(n parse.Node) string {
	if id, ok := n.(*parse.IdentifierNode); ok {
		return id.Ident
	}
	return ""
}

// isRoot reports whether n is the dot or `$`, which outside a range or with is
// the root context.
func isRoot(n parse.Node) bool {
	switch n := n.(type) {
	case *parse.DotNode:
		return true
	case *parse.VariableNode:
		return len(n.Ident) == 1 && n.Ident[0] == "$"
	}
	return false
}

// rootPath returns the root-object path n reads, `.Values.gateway.name` or
// `$.Values.gateway.name` as [Values gateway name], or nil.
func rootPath(n parse.Node) []string {
	switch n := n.(type) {
	case *parse.FieldNode:
		return n.Ident
	case *parse.VariableNode:
		if len(n.Ident) > 1 && n.Ident[0] == "$" {
			return n.Ident[1:]
		}
	}
	return nil
}

// nilSafeRead returns the path `(.Values.gateway | default dict).hostnameSuffix`
// reads, [Values gateway hostnameSuffix], which assayd.validate uses to read a
// key under a map that may be null, or nil if n is not that form.
func nilSafeRead(n *parse.ChainNode) []string {
	pipe, ok := n.Node.(*parse.PipeNode)
	if !ok || len(pipe.Decl) != 0 || len(pipe.Cmds) != 2 || len(pipe.Cmds[0].Args) != 1 {
		return nil
	}
	path := rootPath(pipe.Cmds[0].Args[0])
	def := pipe.Cmds[1].Args
	if len(path) == 0 || path[0] != "Values" || len(def) != 2 || identifier(def[0]) != "default" ||
		identifier(def[1]) != "dict" {
		return nil
	}
	return append(append([]string{}, path...), n.Field...)
}

// isBareReleasePrint reports whether n only prints the release's name or its
// service, as assayd.labels does in two label values. It prints a string and
// gates nothing.
func isBareReleasePrint(n *parse.ActionNode) bool {
	if len(n.Pipe.Decl) != 0 || len(n.Pipe.Cmds) != 1 || len(n.Pipe.Cmds[0].Args) != 1 {
		return false
	}
	p := rootPath(n.Pipe.Cmds[0].Args[0])
	return len(p) == 2 && p[0] == "Release" && (p[1] == "Name" || p[1] == "Service")
}

// isFilesGet reports whether n is `.Files.Get "files/<path>"`, the one use of
// .Files a template may make: a literal path under files/, the directory the
// guard scans.
func isFilesGet(n *parse.CommandNode) bool {
	if len(n.Args) != 2 {
		return false
	}
	p := rootPath(n.Args[0])
	s, ok := n.Args[1].(*parse.StringNode)
	return len(p) == 2 && p[0] == "Files" && p[1] == "Get" && ok && strings.HasPrefix(s.Text, "files/")
}

// checkTree walks ct and reports, as where, every way it reaches Helm's root
// objects that the guard cannot follow. With rbac, ct is a role tree or one a
// role tree calls, and is also held to rbacTemplateValues and the forbidden
// set.
func checkTree(t *testing.T, ct *chartTree, where string, rbac bool) {
	t.Helper()
	at := func(n parse.Node) string {
		loc, _ := ct.tree.ErrorContext(n)
		return loc
	}
	checkPath := func(n parse.Node, path []string) {
		switch path[0] {
		case "Values":
			if rbac && !rbacTemplateValues[strings.Join(path[1:], ".")] {
				t.Errorf("%s reads .%s at %s, which bindingProfiles does not vary: a role or binding "+
					"templated on it renders past every value set the render tests read",
					where, strings.Join(path, "."), at(n))
			}
		case "Release":
			if rbac {
				t.Errorf("%s uses .%s at %s: it gates on the release, which no render here varies",
					where, strings.Join(path, "."), at(n))
			}
		case "Capabilities":
			if rbac {
				t.Errorf("%s uses .%s at %s: it gates on the cluster, which no render here varies",
					where, strings.Join(path, "."), at(n))
			}
		case "Files":
			t.Errorf("%s uses .%s at %s: a template reads chart files only as "+
				"`.Files.Get \"files/<path>\"`, the directory this guard scans", where, strings.Join(path, "."), at(n))
		}
	}
	walk(ct.tree.Root, func(n parse.Node) bool {
		switch n := n.(type) {
		case *parse.ActionNode:
			return !isBareReleasePrint(n)
		case *parse.CommandNode:
			if isFilesGet(n) {
				return false
			}
			if fn := identifier(n.Args[0]); rootAsMap[fn] {
				for _, a := range n.Args[1:] {
					if isRoot(a) {
						t.Errorf("%s reads the root context as a map with %s at %s: every root object is "+
							"reachable that way, past every check here", where, fn, at(n))
					}
				}
			}
		case *parse.ChainNode:
			if path := nilSafeRead(n); path != nil {
				checkPath(n, path)
				return false
			}
		case *parse.FieldNode, *parse.VariableNode:
			if path := rootPath(n); len(path) > 0 {
				checkPath(n, path)
			}
		case *parse.IdentifierNode:
			if rbac && n.Ident == "lookup" {
				t.Errorf("%s uses lookup at %s: it gates on a live cluster read, which a render does not make",
					where, at(n))
			}
		case *parse.StringNode:
			if helmRootObjects[n.Text] {
				t.Errorf("%s names the root object %s at %s: reached by key, it is past every check here",
					where, n.Text, at(n))
			}
		}
		return true
	})
}

// parseChartTemplates parses every file under templates/ the way Helm does,
// with Go's own template parser, into its top level and its defines, and
// records what each calls.
func parseChartTemplates(t *testing.T) (map[string]*chartTree, []string) {
	t.Helper()
	dir := filepath.Join(chartPath, "templates")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	trees := map[string]*chartTree{}
	var tops []string
	for _, e := range entries {
		if e.IsDir() {
			t.Errorf("templates/%s is a directory, which this guard does not read: Helm renders it", e.Name())
			continue
		}
		src, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		file := "templates/" + e.Name()
		p := parse.New(file)
		p.Mode = parse.SkipFuncCheck
		set := map[string]*parse.Tree{}
		if _, err := p.Parse(string(src), "{{", "}}", set); err != nil {
			t.Errorf("%s does not parse: %v", file, err)
			continue
		}
		trees[file] = &chartTree{key: file, top: true, tree: p}
		tops = append(tops, file)
		for name, tree := range set {
			if tree == p {
				continue
			}
			if _, dup := trees[name]; dup {
				t.Errorf("%s defines %q again: which body Helm uses is not what this guard reads", file, name)
			}
			trees[name] = &chartTree{key: name, tree: tree}
		}
	}
	for _, ct := range trees {
		walk(ct.tree.Root, func(n parse.Node) bool {
			switch n := n.(type) {
			case *parse.TemplateNode:
				ct.calls = append(ct.calls, n.Name)
			case *parse.CommandNode:
				switch identifier(n.Args[0]) {
				case "include":
					if len(n.Args) > 1 {
						if s, ok := n.Args[1].(*parse.StringNode); ok {
							ct.calls = append(ct.calls, s.Text)
							break
						}
					}
					loc, _ := ct.tree.ErrorContext(n)
					t.Errorf("%s includes a template by a name built at render time, at %s: what it calls "+
						"cannot be followed", ct.key, loc)
				case "tpl":
					loc, _ := ct.tree.ErrorContext(n)
					t.Errorf("%s uses tpl at %s: it renders text as a template, which this guard cannot read",
						ct.key, loc)
				}
			}
			return true
		})
	}
	sort.Strings(tops)
	return trees, tops
}

// The guard's threat model, stated first. It catches a careless chart change
// that hides a role or a binding behind a value no profile in bindingProfiles
// renders, which the render tests, seeing only those profiles, cannot. It is
// no defence against a determined author: anyone who can edit the chart can
// edit this test, and a kind, a key or a value path built at render time —
// with printf, cat, or a concatenated string, as in `Cluster{{ "Role" }}` —
// is not seen here. Nor is a packaged subchart under charts/, whose templates
// sit compressed inside a .tgz; the chart vendors none today. And a gate on a
// free-string value in rbacTemplateValues is not seen either.
//
// It parses every template with text/template/parse, so a define is its own
// tree and a `{{/* */}}` comment is gone. Then:
//
//   - A tree is a role tree if its text, outside YAML comment lines, or one of
//     its string literals names a role or binding kind, or if it calls a role
//     tree, by `template` or `include`. Every file's top level that is a role
//     tree is checked, except NOTES.txt's, which Helm never applies.
//   - A checked tree, and every tree it calls, transitively, may read only
//     rbacTemplateValues, and may not use .Capabilities, lookup, or .Release
//     beyond a bare print of its Name or Service.
//   - Every tree may reach Helm's root objects only by name: no string naming
//     one, and no root passed to index, get, pluck or dig. It may use .Files
//     only as `.Files.Get "files/<path>"`, and no chart file outside
//     templates/ and crds/ that .Files can read may mention a role or binding
//     kind. It may not use tpl, nor include a template by a name it builds.
//
// The root-as-map check cannot tell the root from a range or with element, so
// `get . "k"` inside one fails here too: it fails closed.
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

	trees, tops := parseChartTemplates(t)

	role := map[string]bool{}
	for key, ct := range trees {
		walk(ct.tree.Root, func(n parse.Node) bool {
			switch n := n.(type) {
			case *parse.TextNode:
				role[key] = role[key] || mentionsRBACKind(n)
			case *parse.StringNode:
				role[key] = role[key] || rbacKind.MatchString(n.Text)
			}
			return true
		})
	}
	for changed := true; changed; {
		changed = false
		for key, ct := range trees {
			for _, c := range ct.calls {
				if !role[key] && role[c] {
					role[key], changed = true, true
				}
			}
		}
	}

	checked := 0
	for _, key := range tops {
		ct := trees[key]
		if key == notesFile || !role[key] {
			checkTree(t, ct, key, false)
			continue
		}
		checked++
		checkTree(t, ct, key+", which writes a role or a binding,", true)
		seen := map[string]bool{key: true}
		queue := append([]string{}, ct.calls...)
		for len(queue) > 0 {
			name := queue[0]
			queue = queue[1:]
			if seen[name] {
				continue
			}
			seen[name] = true
			callee, ok := trees[name]
			if !ok {
				t.Errorf("%s calls %q, which no template under templates/ defines: the guard cannot read it",
					key, name)
				continue
			}
			checkTree(t, callee, fmt.Sprintf("the template %q, called from %s,", name, key), true)
			queue = append(queue, callee.calls...)
		}
	}
	for key, ct := range trees {
		if !ct.top {
			checkTree(t, ct, fmt.Sprintf("the template %q", key), false)
		}
	}
	if checked == 0 {
		t.Error("no template writes a role or a binding, but the chart binds the operator: the guard reads nothing")
	}
}
