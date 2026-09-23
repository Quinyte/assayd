// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

// Package refgen derives the public reference under docs/reference/ from the
// code, the CRD and the chart, so that the reference cannot drift from them.
//
// The repository has already paid for the alternative. docs/architecture.md and
// docs/architecture.html were two hand-maintained copies of one document with no
// generator between them, and they drifted. A reference published on a website
// is the same shape of liability, one blast radius larger: whoever reads it has
// no way to check it against the code.
//
// What this package will and will not claim is the whole design:
//
//   - The CRD reference is rendered from config/crd/assayd.dev_agents.yaml,
//     which controller-gen writes from the Go markers. Nothing here parses Go
//     types for it.
//   - The chart reference is rendered from charts/assayd/values.yaml, comments
//     and all, by the same YAML parser the project already depends on.
//   - The condition vocabulary is EXTRACTED from internal/controller with
//     go/packages and go/types — call site, condition, status and reason — and
//     ANNOTATED from internal/refgen/reasons.yaml for the three things no
//     compiler knows: what state produces a reason, what the operator does, and
//     whether traffic is withdrawn. The extraction and the annotation must agree
//     exactly, or generation fails. That is what keeps the prose honest: a new
//     reason cannot reach main without someone writing its three answers, and an
//     annotation whose reason has been deleted cannot survive either.
package refgen

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

// Resolution says how the generator learned a reason string, because the three
// ways are not equally strong and a reader deserves to know which one produced
// the row they are reading.
type Resolution string

const (
	// ResolvedConstant — the call site passes a constant: a string literal, or
	// one of the package's Reason* constants. Exact.
	ResolvedConstant Resolution = "constant"
	// ResolvedField — the call site passes a struct field (failure.reason and
	// its siblings). go/types names the struct; the generator then collects
	// every constant written into that field anywhere in the package. Exact for
	// the SET of reasons, approximate for which one a given pass picks.
	ResolvedField Resolution = "field"
	// ResolvedLocal — the call site passes a local whose assignments in the
	// enclosing function (and, one hop, the in-package function it calls) are
	// constants.
	ResolvedLocal Resolution = "local"
	// ResolvedCarried — the reason is read back off a condition an EARLIER pass
	// stored. It introduces no new reason; it re-asserts one.
	ResolvedCarried Resolution = "carried"
	// Unresolved — the generator could not fold the expression. It is reported
	// rather than guessed: a reference that invents a reason is worse than one
	// that says it does not know.
	Unresolved Resolution = "unresolved"
)

// Site is one place in the operator that writes a condition.
type Site struct {
	Condition  string // the condition type, e.g. "Ready"
	Status     string // "True", "False", "Unknown", or "" when computed at run time
	Reasons    []string
	Resolution Resolution
	Expr       string // the reason expression as written, for the unresolved and carried rows
	Func       string // the enclosing function
	File       string // base name, never a path from the machine that generated this
	Line       int
	// FieldOpen marks a `field` row whose struct field has at least one write
	// the resolver could not fold, so the listed reasons are not the closed set.
	// Without it the page said "one of N reasons that field can hold" for a set
	// that might be larger, which is the shape of overclaim this generator
	// exists to refuse.
	FieldOpen bool
	// FieldKey names the struct field a `field` row read, as "Type.field".
	FieldKey string
}

// ConditionType is one member of the closed vocabulary in api/v1alpha1.
type ConditionType struct {
	Name    string
	Doc     string
	Owned   bool // the reconciler is its sole author; see conditions.go
	Sticky  bool // stays in the list once set, flipped to False rather than removed
	Sites   []Site
	Reasons []string
}

// ReasonConst is a Reason* constant declared in internal/controller.
type ReasonConst struct {
	Ident string
	Value string
	Doc   string
	File  string
	Line  int
}

// Vocabulary is everything the extractor found.
type Vocabulary struct {
	Phases       []NamedDoc
	PhaseDoc     string
	Conditions   []ConditionType
	ReasonConsts []ReasonConst
	Sites        []Site
	// CarrySites counts the calls that re-assert a condition an earlier pass
	// stored, verbatim. They introduce no reason and so appear in no site list;
	// the count is published so their existence is not invisible.
	CarrySites int
	// OpenFields names the struct reason fields that a PUBLISHED row reads and
	// whose writes did not all fold. Fields nothing on the page reads are left
	// out: naming them would be a warning about rows that do not exist.
	OpenFields []string
	// ReasonsByName maps a reason string to every site that can set it.
	ReasonsByName map[string][]Site
}

// NamedDoc is a constant with its doc comment.
type NamedDoc struct {
	Name, Value, Doc string
}

const (
	apiPkg        = "github.com/Quinyte/assayd/api/v1alpha1"
	controllerPkg = "github.com/Quinyte/assayd/internal/controller"
)

// ExtractVocabulary type-checks the API and controller packages and reads the
// condition vocabulary out of them.
//
// go/packages does the parsing and type-checking rather than a hand-rolled AST
// walk. The difference is not style: `w.reason` and `fails[0].reason` are the
// same three characters and different structs, and only a type checker knows
// which. A walker that guessed would publish a union of every reason every
// struct can carry, under a condition that can carry a quarter of them — the
// loud-and-wrong failure AGENTS.md rule 8 names.
func ExtractVocabulary(root string) (*Vocabulary, error) {
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax |
			packages.NeedTypes | packages.NeedTypesInfo | packages.NeedDeps |
			packages.NeedImports,
		Dir: root,
	}
	pkgs, err := packages.Load(cfg, apiPkg, controllerPkg)
	if err != nil {
		return nil, fmt.Errorf("load packages: %w", err)
	}
	var loadErrs []string
	for _, p := range pkgs {
		for _, e := range p.Errors {
			loadErrs = append(loadErrs, e.Error())
		}
	}
	if len(loadErrs) > 0 {
		return nil, fmt.Errorf("the packages the reference is generated from do not type-check, "+
			"so nothing it said could be trusted:\n  %s", strings.Join(loadErrs, "\n  "))
	}

	var api, ctrl *packages.Package
	for _, p := range pkgs {
		switch p.PkgPath {
		case apiPkg:
			api = p
		case controllerPkg:
			ctrl = p
		}
	}
	if api == nil || ctrl == nil {
		return nil, fmt.Errorf("loaded %d packages but not both %s and %s", len(pkgs), apiPkg, controllerPkg)
	}

	v := &Vocabulary{ReasonsByName: map[string][]Site{}}
	v.Phases = constantsWithPrefix(api, "Phase")
	v.PhaseDoc = typeDoc(api, "AgentPhase")
	condTypes, err := conditionTypes(api)
	if err != nil {
		return nil, err
	}
	owned, sticky := conditionSetMembership(ctrl)
	v.ReasonConsts = reasonConstants(ctrl)

	ex := &extractor{pkg: ctrl, fieldReasons: map[string][]string{}, unfoldedField: map[string]bool{}}
	ex.collectFieldReasons()
	v.Sites = ex.sites()
	v.CarrySites = ex.carrySites
	openSeen := map[string]bool{}
	for _, s := range v.Sites {
		if s.FieldOpen && s.FieldKey != "" && !openSeen[s.FieldKey] {
			openSeen[s.FieldKey] = true
			v.OpenFields = append(v.OpenFields, s.FieldKey)
		}
	}
	sort.Strings(v.OpenFields)

	byCond := map[string][]Site{}
	for _, s := range v.Sites {
		byCond[s.Condition] = append(byCond[s.Condition], s)
		for _, r := range s.Reasons {
			v.ReasonsByName[r] = append(v.ReasonsByName[r], s)
		}
	}
	for _, ct := range condTypes {
		c := ConditionType{Name: ct.Value, Doc: ct.Doc, Owned: owned[ct.Value], Sticky: sticky[ct.Value]}
		c.Sites = byCond[ct.Value]
		seen := map[string]bool{}
		for _, s := range c.Sites {
			for _, r := range s.Reasons {
				if !seen[r] {
					seen[r] = true
					c.Reasons = append(c.Reasons, r)
				}
			}
		}
		sort.Strings(c.Reasons)
		v.Conditions = append(v.Conditions, c)
	}

	// A condition written by a call site that names no declared type would mean
	// the extractor and the API have parted company. Report it rather than drop
	// the rows on the floor.
	declared := map[string]bool{}
	for _, c := range v.Conditions {
		declared[c.Name] = true
	}
	var stray []string
	for cond := range byCond {
		if !declared[cond] {
			stray = append(stray, cond)
		}
	}
	if len(stray) > 0 {
		sort.Strings(stray)
		return nil, fmt.Errorf("the operator writes condition type(s) %s that api/v1alpha1 does not "+
			"declare; add them to designConditions() or fix the writer", strings.Join(stray, ", "))
	}
	return v, nil
}

// conditionTypes reads the Cond* constants out of api/v1alpha1. It reads the
// CONSTANTS rather than designConditions(), and then checks the two agree:
// designConditions is the closure test's list, and a constant missing from it
// is exactly the drift that list exists to catch.
func conditionTypes(api *packages.Package) ([]NamedDoc, error) {
	var out []NamedDoc
	for _, nd := range constantsOfType(api, "ConditionType") {
		out = append(out, nd)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Value < out[j].Value })

	inList := map[string]bool{}
	for _, f := range api.Syntax {
		ast.Inspect(f, func(n ast.Node) bool {
			fd, ok := n.(*ast.FuncDecl)
			if !ok || fd.Name.Name != "designConditions" {
				return true
			}
			ast.Inspect(fd, func(m ast.Node) bool {
				id, ok := m.(*ast.Ident)
				if !ok {
					return true
				}
				if c, ok := api.TypesInfo.Uses[id].(*types.Const); ok && c.Val().Kind() == constant.String {
					inList[constant.StringVal(c.Val())] = true
				}
				return true
			})
			return false
		})
	}
	var missing []string
	for _, nd := range out {
		if !inList[nd.Value] {
			missing = append(missing, nd.Value)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("condition type(s) %s are declared as constants but are not in "+
			"designConditions(), so the closure test does not cover them", strings.Join(missing, ", "))
	}
	return out, nil
}

func constantsOfType(p *packages.Package, typeName string) []NamedDoc {
	var out []NamedDoc
	for _, f := range p.Syntax {
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for _, name := range vs.Names {
					c, ok := p.TypesInfo.Defs[name].(*types.Const)
					if !ok || c.Val().Kind() != constant.String {
						continue
					}
					named, ok := c.Type().(*types.Named)
					if !ok || named.Obj().Name() != typeName {
						continue
					}
					out = append(out, NamedDoc{
						Name:  name.Name,
						Value: constant.StringVal(c.Val()),
						Doc:   docOf(vs.Doc, gd.Doc, len(gd.Specs)),
					})
				}
			}
		}
	}
	return out
}

// typeDoc is the doc comment on a named type declaration.
func typeDoc(p *packages.Package, name string) string {
	for _, f := range p.Syntax {
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, spec := range gd.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok || ts.Name.Name != name {
					continue
				}
				if ts.Doc != nil {
					return cleanDoc(ts.Doc.Text())
				}
				if gd.Doc != nil {
					return cleanDoc(gd.Doc.Text())
				}
			}
		}
	}
	return ""
}

func constantsWithPrefix(p *packages.Package, prefix string) []NamedDoc {
	var out []NamedDoc
	for _, f := range p.Syntax {
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for _, name := range vs.Names {
					if !strings.HasPrefix(name.Name, prefix) {
						continue
					}
					c, ok := p.TypesInfo.Defs[name].(*types.Const)
					if !ok || c.Val().Kind() != constant.String {
						continue
					}
					out = append(out, NamedDoc{
						Name:  name.Name,
						Value: constant.StringVal(c.Val()),
						Doc:   docOf(vs.Doc, gd.Doc, len(gd.Specs)),
					})
				}
			}
		}
	}
	return out
}

// reasonConstants reads the Reason* constants out of internal/controller.
func reasonConstants(p *packages.Package) []ReasonConst {
	var out []ReasonConst
	for _, f := range p.Syntax {
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for _, name := range vs.Names {
					if !strings.HasPrefix(name.Name, "Reason") {
						continue
					}
					c, ok := p.TypesInfo.Defs[name].(*types.Const)
					if !ok || c.Val().Kind() != constant.String {
						continue
					}
					pos := p.Fset.Position(name.Pos())
					out = append(out, ReasonConst{
						Ident: name.Name,
						Value: constant.StringVal(c.Val()),
						Doc:   docOf(vs.Doc, gd.Doc, len(gd.Specs)),
						File:  baseName(pos.Filename),
						Line:  pos.Line,
					})
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Value < out[j].Value })
	return out
}

// conditionSetMembership reads ownedTypes and stickyTypes out of conditions.go.
// Both decide what an operator's status list means over time, and both are
// composite literals of assaydv1alpha1.ConditionType keys.
func conditionSetMembership(p *packages.Package) (owned, sticky map[string]bool) {
	owned, sticky = map[string]bool{}, map[string]bool{}
	for _, f := range p.Syntax {
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.VAR {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok || len(vs.Names) != 1 || len(vs.Values) != 1 {
					continue
				}
				var into map[string]bool
				switch vs.Names[0].Name {
				case "ownedTypes":
					into = owned
				case "stickyTypes":
					into = sticky
				default:
					continue
				}
				cl, ok := vs.Values[0].(*ast.CompositeLit)
				if !ok {
					continue
				}
				for _, el := range cl.Elts {
					kv, ok := el.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					if s, ok := constString(p, kv.Key); ok {
						into[s] = true
					}
				}
			}
		}
	}
	return owned, sticky
}

func constString(p *packages.Package, e ast.Expr) (string, bool) {
	tv, ok := p.TypesInfo.Types[e]
	if !ok || tv.Value == nil || tv.Value.Kind() != constant.String {
		return "", false
	}
	return constant.StringVal(tv.Value), true
}

func docOf(own, group *ast.CommentGroup, groupSize int) string {
	if own != nil {
		return cleanDoc(own.Text())
	}
	// A doc comment above a const block belongs to the block, not to one member;
	// attributing it to a single constant would be a small lie repeated 40 times.
	if group != nil && groupSize == 1 {
		return cleanDoc(group.Text())
	}
	return ""
}

// cleanDoc folds a Go doc comment to one line and drops the marker lines.
//
// A `+kubebuilder:` or `+optional` line is an instruction to a generator, not
// prose. Left in, the enum marker for AgentPhase ended up in the middle of a
// sentence on the published page.
func cleanDoc(s string) string {
	var kept []string
	for _, l := range strings.Split(strings.TrimSpace(s), "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "+") {
			continue
		}
		kept = append(kept, l)
	}
	out := strings.TrimSpace(strings.Join(kept, " "))
	// A doc comment is prose, and Markdown hands anything in angle brackets to
	// the HTML parser: `<agent>-auth` renders as `-auth` with the name silently
	// gone. Escape both, once, here — every derived doc string passes through.
	out = strings.ReplaceAll(out, "&", "&amp;")
	out = strings.ReplaceAll(out, "<", "&lt;")
	return strings.ReplaceAll(out, ">", "&gt;")
}

func baseName(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}
