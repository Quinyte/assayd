// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package refgen

import (
	"go/ast"
	"go/constant"
	"go/types"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

// extractor walks internal/controller and finds every condition write.
//
// Two shapes write a condition today, and both are here because a generator
// that knew only the common one would publish a vocabulary with a hole in it:
//
//   - conditionSet.set(type, status, reason, message) — the reconciler's own
//     accumulator, used on every ordinary path.
//   - meta.SetStatusCondition(&status.Conditions, metav1.Condition{…}) — used by
//     the teardown report, which asserts Ready alone and leaves the rest of the
//     Agent's conditions as they are.
//
// conditionSet.carry is deliberately NOT a third shape: it re-asserts a
// condition an earlier pass stored, verbatim, and so introduces no reason of
// its own. The count of those sites is reported instead.
type extractor struct {
	pkg *packages.Package
	// fieldReasons maps "structName.fieldName" to every constant string written
	// into that field anywhere in the package. It is how failure.reason and its
	// siblings resolve: go/types names the struct at the call site, and this
	// says what that struct's reason field can hold.
	fieldReasons map[string][]string
	// unfoldedField records a struct reason field with at least one write the
	// resolver could not fold, so the rendered set is not presented as closed.
	unfoldedField map[string]bool
	carrySites    int
}

// collectFieldReasons fills fieldReasons. It runs TWICE by design: the first
// pass takes only the constants, and the second folds the non-constant writes
// (`&failure{reason, msg}`) with the full resolver, which by then has the
// first pass's answers to work from. One pass alone under-reports
// failure.reason by the four reasons the lost-race arm computes into a local
// before constructing the failure — and an under-reported field set is a
// reference that quietly omits a reason the operator really can set.
func (e *extractor) collectFieldReasons() {
	e.collectFieldReasonsPass(true)
	e.collectFieldReasonsPass(false)
	for k := range e.fieldReasons {
		sort.Strings(e.fieldReasons[k])
	}
}

func (e *extractor) collectFieldReasonsPass(constantsOnly bool) {
	add := func(key, val string) {
		for _, v := range e.fieldReasons[key] {
			if v == val {
				return
			}
		}
		e.fieldReasons[key] = append(e.fieldReasons[key], val)
	}
	for _, f := range e.pkg.Syntax {
		file := f
		fold := func(key string, ex ast.Expr) {
			if s, ok := constString(e.pkg, ex); ok {
				add(key, s)
				return
			}
			if constantsOnly {
				return
			}
			rs, res := e.resolveReason(enclosingFunc(file, ex), ex, 1)
			if res == Unresolved || len(rs) == 0 {
				// Recorded, not hidden: a field whose writes did not all fold
				// has a value set the reference must not present as complete.
				e.unfoldedField[key] = true
				return
			}
			for _, r := range rs {
				add(key, r)
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.CompositeLit:
				named := namedOf(e.pkg.TypesInfo.Types[v].Type)
				if named == nil {
					return true
				}
				st, ok := named.Underlying().(*types.Struct)
				if !ok {
					return true
				}
				for i, el := range v.Elts {
					if kv, ok := el.(*ast.KeyValueExpr); ok {
						id, ok := kv.Key.(*ast.Ident)
						if !ok || !isReasonField(id.Name) {
							continue
						}
						fold(named.Obj().Name()+"."+id.Name, kv.Value)
						continue
					}
					// Positional: failure{ReasonAuthInputAbsent, msg}.
					if i >= st.NumFields() || !isReasonField(st.Field(i).Name()) {
						continue
					}
					fold(named.Obj().Name()+"."+st.Field(i).Name(), el)
				}
			case *ast.AssignStmt:
				for i, lhs := range v.Lhs {
					sel, ok := lhs.(*ast.SelectorExpr)
					if !ok || !isReasonField(sel.Sel.Name) || i >= len(v.Rhs) {
						continue
					}
					recv := selectionStruct(e.pkg, sel)
					if recv == "" {
						continue
					}
					fold(recv+"."+sel.Sel.Name, v.Rhs[i])
				}
			}
			return true
		})
	}
}

func isReasonField(name string) bool {
	l := strings.ToLower(name)
	return l == "reason" || strings.HasSuffix(l, "reason")
}

func namedOf(t types.Type) *types.Named {
	if t == nil {
		return nil
	}
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	n, _ := t.(*types.Named)
	return n
}

// selectionStruct names the struct a selector selects a field from.
func selectionStruct(p *packages.Package, sel *ast.SelectorExpr) string {
	s, ok := p.TypesInfo.Selections[sel]
	if !ok {
		return ""
	}
	n := namedOf(s.Recv())
	if n == nil {
		return ""
	}
	return n.Obj().Name()
}

// sites finds every condition write in the package.
func (e *extractor) sites() []Site {
	var out []Site
	for _, f := range e.pkg.Syntax {
		var fnStack []string
		ast.Inspect(f, func(n ast.Node) bool {
			if fd, ok := n.(*ast.FuncDecl); ok {
				fnStack = append(fnStack, fd.Name.Name)
			}
			switch v := n.(type) {
			case *ast.CallExpr:
				if s, ok := e.setCall(v, currentFunc(fnStack), enclosingFunc(f, v)); ok {
					out = append(out, s)
				}
				if e.isCarry(v) {
					e.carrySites++
				}
			case *ast.CompositeLit:
				if s, ok := e.conditionLiteral(v, currentFunc(fnStack)); ok {
					out = append(out, s)
				}
			}
			return true
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Condition != out[j].Condition {
			return out[i].Condition < out[j].Condition
		}
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Line < out[j].Line
	})
	return out
}

func currentFunc(stack []string) string {
	if len(stack) == 0 {
		return ""
	}
	return stack[len(stack)-1]
}

// enclosingFunc finds the FuncDecl a node sits in, which local resolution needs.
func enclosingFunc(f *ast.File, target ast.Node) *ast.FuncDecl {
	var found *ast.FuncDecl
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if fd.Pos() <= target.Pos() && target.End() <= fd.End() {
			found = fd
		}
	}
	return found
}

func (e *extractor) isCarry(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "carry" {
		return false
	}
	s, ok := e.pkg.TypesInfo.Selections[sel]
	if !ok {
		return false
	}
	n := namedOf(s.Recv())
	return n != nil && n.Obj().Name() == "conditionSet"
}

// setCall recognises conditionSet.set(type, status, reason, message).
func (e *extractor) setCall(call *ast.CallExpr, fnName string, fn *ast.FuncDecl) (Site, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "set" || len(call.Args) != 4 {
		return Site{}, false
	}
	s, ok := e.pkg.TypesInfo.Selections[sel]
	if !ok {
		return Site{}, false
	}
	n := namedOf(s.Recv())
	if n == nil || n.Obj().Name() != "conditionSet" {
		return Site{}, false
	}
	cond, ok := constString(e.pkg, call.Args[0])
	if !ok {
		// A condition type that is not a constant would defeat the compile-time
		// spelling guarantee api/v1alpha1 documents; say so rather than skip it.
		cond = "<not a constant: " + exprText(e.pkg, call.Args[0]) + ">"
	}
	status, _ := constString(e.pkg, call.Args[1])
	reasons, res := e.resolveReason(fn, call.Args[2], 0)
	pos := e.pkg.Fset.Position(call.Pos())
	return Site{
		Condition:  cond,
		Status:     status,
		Reasons:    reasons,
		Resolution: res,
		Expr:       exprText(e.pkg, call.Args[2]),
		Func:       fnName,
		File:       baseName(pos.Filename),
		Line:       pos.Line,
	}, true
}

// conditionLiteral recognises a metav1.Condition composite literal, which the
// teardown report writes straight through meta.SetStatusCondition.
func (e *extractor) conditionLiteral(cl *ast.CompositeLit, fnName string) (Site, bool) {
	named := namedOf(e.pkg.TypesInfo.Types[cl].Type)
	if named == nil || named.Obj().Name() != "Condition" ||
		named.Obj().Pkg() == nil ||
		!strings.HasSuffix(named.Obj().Pkg().Path(), "apis/meta/v1") {
		return Site{}, false
	}
	site := Site{Func: fnName, Resolution: Unresolved}
	var any bool
	for _, el := range cl.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		id, ok := kv.Key.(*ast.Ident)
		if !ok {
			continue
		}
		switch id.Name {
		case "Type":
			if s, ok := constString(e.pkg, kv.Value); ok {
				site.Condition, any = s, true
			}
		case "Status":
			if s, ok := constString(e.pkg, kv.Value); ok {
				site.Status = s
			}
		case "Reason":
			site.Expr = exprText(e.pkg, kv.Value)
			if s, ok := constString(e.pkg, kv.Value); ok {
				site.Reasons, site.Resolution = []string{s}, ResolvedConstant
			}
		}
	}
	if !any {
		return Site{}, false
	}
	pos := e.pkg.Fset.Position(cl.Pos())
	site.File, site.Line = baseName(pos.Filename), pos.Line
	return site, true
}

const maxResolveDepth = 3

// resolveReason folds a reason expression to the set of strings it can hold.
//
// It never guesses. Every arm either returns strings it can prove or marks the
// site Unresolved, because a reference that invents a reason is worse than one
// that admits it does not know — AGENTS.md rule 8 in generator form.
func (e *extractor) resolveReason(fn *ast.FuncDecl, expr ast.Expr, depth int) ([]string, Resolution) {
	if depth > maxResolveDepth {
		return nil, Unresolved
	}
	if s, ok := constString(e.pkg, expr); ok {
		return []string{s}, ResolvedConstant
	}
	switch v := expr.(type) {
	case *ast.SelectorExpr:
		// A reason read back off a stored condition re-asserts what an earlier
		// pass wrote; it is not a new reason.
		if st := selectionStruct(e.pkg, v); st == "Condition" {
			return nil, ResolvedCarried
		} else if st != "" && isReasonField(v.Sel.Name) {
			key := st + "." + v.Sel.Name
			// Narrow through the PRODUCER first. `fails[0].reason` is a
			// failure.reason like any other, but the failures came from
			// compileFailures, which builds exactly three — and publishing the
			// whole failure.reason set against PolicyCompileFailed would say
			// that condition can carry ServingRouteNotAccepted, which it cannot.
			if rs, ok := e.narrowThroughProducer(fn, v, key, depth); ok {
				return rs, ResolvedField
			}
			if rs := e.fieldReasons[key]; len(rs) > 0 {
				return append([]string(nil), rs...), ResolvedField
			}
		}
	case *ast.Ident:
		if fn == nil {
			return nil, Unresolved
		}
		// By OBJECT, never by name. reconcileRuntime declares `reason` three
		// times in three blocks, for three unrelated causes; resolving by name
		// unioned them and published MaterialUnavailable as something
		// PolicyApplyIncomplete could carry. go/types tells the three apart.
		obj, ok := e.pkg.TypesInfo.Uses[v].(*types.Var)
		if !ok {
			return nil, Unresolved
		}
		if rs, ok := e.resolveLocal(fn, obj, depth); ok {
			return rs, ResolvedLocal
		}
	case *ast.CallExpr:
		if rs, ok := e.resolveCall(v, 0, depth); ok {
			return rs, ResolvedLocal
		}
	}
	return nil, Unresolved
}

// resolveLocal folds one local variable — identified by its go/types object,
// not by its name — or a parameter, inside one function.
func (e *extractor) resolveLocal(fn *ast.FuncDecl, obj *types.Var, depth int) ([]string, bool) {
	if depth > maxResolveDepth {
		return nil, false
	}
	seen := map[string]bool{}
	var out []string
	addAll := func(rs []string) {
		for _, r := range rs {
			if !seen[r] {
				seen[r] = true
				out = append(out, r)
			}
		}
	}
	resolved := false
	unresolved := false
	resolveExpr := func(ex ast.Expr) {
		rs, res := e.resolveReason(fn, ex, depth+1)
		if res == Unresolved || len(rs) == 0 {
			unresolved = true
			return
		}
		resolved = true
		addAll(rs)
	}

	ast.Inspect(fn, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for i, lhs := range as.Lhs {
			id, ok := lhs.(*ast.Ident)
			if !ok || e.varOf(id) != obj {
				continue
			}
			switch {
			case len(as.Rhs) == len(as.Lhs):
				resolveExpr(as.Rhs[i])
			case len(as.Rhs) == 1:
				// a, reason := f(…): follow f's return at this position.
				call, ok := as.Rhs[0].(*ast.CallExpr)
				if !ok {
					unresolved = true
					continue
				}
				rs, ok := e.resolveCall(call, i, depth+1)
				if !ok {
					unresolved = true
					continue
				}
				resolved = true
				addAll(rs)
			default:
				unresolved = true
			}
		}
		return true
	})

	if !resolved && isParam(fn, obj.Name()) {
		// The reason arrives as an argument; resolve it at every call site of
		// this function in the package.
		if rs, ok := e.resolveParam(fn, obj.Name(), depth+1); ok {
			return rs, true
		}
	}
	if !resolved || unresolved {
		// A partially folded local would publish a subset and read as the whole
		// set. Refuse it.
		return nil, false
	}
	sort.Strings(out)
	return out, true
}

// narrowThroughProducer answers `X.reason` from the function that BUILT X,
// rather than from every write to that field in the package.
//
// It applies only where the root of the selector is a local assigned from an
// in-package call, and it refuses unless every assignment to that local is such
// a call that yields a non-empty set — a partial narrowing would be a smaller
// set presented as the whole one, which is the wrong way to be wrong.
func (e *extractor) narrowThroughProducer(fn *ast.FuncDecl, sel *ast.SelectorExpr, key string, depth int) ([]string, bool) {
	if fn == nil || depth > maxResolveDepth {
		return nil, false
	}
	root := rootIdent(sel.X)
	if root == nil {
		return nil, false
	}
	obj := e.varOf(root)
	if obj == nil {
		return nil, false
	}
	seen := map[string]bool{}
	var out []string
	found, bad := false, false
	ast.Inspect(fn, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for i, lhs := range as.Lhs {
			id, ok := lhs.(*ast.Ident)
			if !ok || e.varOf(id) != obj {
				continue
			}
			var rhs ast.Expr
			switch {
			case len(as.Rhs) == len(as.Lhs):
				rhs = as.Rhs[i]
			case len(as.Rhs) == 1:
				rhs = as.Rhs[0]
			default:
				bad = true
				continue
			}
			call, ok := rhs.(*ast.CallExpr)
			if !ok {
				bad = true
				continue
			}
			decl := e.calleeDecl(call)
			if decl == nil || decl.Body == nil {
				bad = true
				continue
			}
			rs := e.fieldReasonsInFunc(decl, key)
			if len(rs) == 0 {
				bad = true
				continue
			}
			found = true
			for _, r := range rs {
				if !seen[r] {
					seen[r] = true
					out = append(out, r)
				}
			}
		}
		return true
	})
	if bad || !found {
		return nil, false
	}
	sort.Strings(out)
	return out, true
}

// fieldReasonsInFunc collects the constants one function writes into a struct's
// reason field.
func (e *extractor) fieldReasonsInFunc(decl *ast.FuncDecl, key string) []string {
	seen := map[string]bool{}
	var out []string
	ast.Inspect(decl.Body, func(n ast.Node) bool {
		cl, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		named := namedOf(e.pkg.TypesInfo.Types[cl].Type)
		if named == nil {
			return true
		}
		st, ok := named.Underlying().(*types.Struct)
		if !ok {
			return true
		}
		for i, el := range cl.Elts {
			name := ""
			var val ast.Expr
			if kv, ok := el.(*ast.KeyValueExpr); ok {
				id, ok := kv.Key.(*ast.Ident)
				if !ok {
					continue
				}
				name, val = id.Name, kv.Value
			} else if i < st.NumFields() {
				name, val = st.Field(i).Name(), el
			}
			if name == "" || named.Obj().Name()+"."+name != key {
				continue
			}
			if s, ok := constString(e.pkg, val); ok && !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
		return true
	})
	sort.Strings(out)
	return out
}

func rootIdent(e ast.Expr) *ast.Ident {
	for {
		switch v := e.(type) {
		case *ast.Ident:
			return v
		case *ast.IndexExpr:
			e = v.X
		case *ast.SelectorExpr:
			e = v.X
		case *ast.ParenExpr:
			e = v.X
		case *ast.StarExpr:
			e = v.X
		default:
			return nil
		}
	}
}

func (e *extractor) calleeDecl(call *ast.CallExpr) *ast.FuncDecl {
	var id *ast.Ident
	switch f := call.Fun.(type) {
	case *ast.Ident:
		id = f
	case *ast.SelectorExpr:
		id = f.Sel
	default:
		return nil
	}
	obj, ok := e.pkg.TypesInfo.Uses[id].(*types.Func)
	if !ok {
		return nil
	}
	return e.funcDecl(obj)
}

// varOf names the variable an identifier is, whether it is being declared
// (`reason :=`) or assigned (`reason =`).
func (e *extractor) varOf(id *ast.Ident) *types.Var {
	if v, ok := e.pkg.TypesInfo.Defs[id].(*types.Var); ok {
		return v
	}
	v, _ := e.pkg.TypesInfo.Uses[id].(*types.Var)
	return v
}

func isParam(fn *ast.FuncDecl, name string) bool {
	if fn.Type.Params == nil {
		return false
	}
	for _, f := range fn.Type.Params.List {
		for _, n := range f.Names {
			if n.Name == name {
				return true
			}
		}
	}
	return false
}

// resolveParam folds a parameter by looking at what every caller passes.
func (e *extractor) resolveParam(fn *ast.FuncDecl, name string, depth int) ([]string, bool) {
	if depth > maxResolveDepth {
		return nil, false
	}
	obj, ok := e.pkg.TypesInfo.Defs[fn.Name].(*types.Func)
	if !ok {
		return nil, false
	}
	idx := -1
	pos := 0
	for _, f := range fn.Type.Params.List {
		for _, n := range f.Names {
			if n.Name == name {
				idx = pos
			}
			pos++
		}
	}
	if idx < 0 {
		return nil, false
	}
	seen := map[string]bool{}
	var out []string
	found := false
	for _, file := range e.pkg.Syntax {
		var bad bool
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || !e.callsFunc(call, obj) || idx >= len(call.Args) {
				return true
			}
			rs, res := e.resolveReason(enclosingFunc(file, call), call.Args[idx], depth)
			if res == Unresolved || len(rs) == 0 {
				bad = true
				return true
			}
			found = true
			for _, r := range rs {
				if !seen[r] {
					seen[r] = true
					out = append(out, r)
				}
			}
			return true
		})
		if bad {
			return nil, false
		}
	}
	if !found {
		return nil, false
	}
	sort.Strings(out)
	return out, true
}

func (e *extractor) callsFunc(call *ast.CallExpr, want *types.Func) bool {
	var id *ast.Ident
	switch f := call.Fun.(type) {
	case *ast.Ident:
		id = f
	case *ast.SelectorExpr:
		id = f.Sel
	default:
		return false
	}
	return e.pkg.TypesInfo.Uses[id] == want
}

// resolveCall folds the value a package-local function returns at one position.
func (e *extractor) resolveCall(call *ast.CallExpr, pos, depth int) ([]string, bool) {
	if depth > maxResolveDepth {
		return nil, false
	}
	var id *ast.Ident
	switch f := call.Fun.(type) {
	case *ast.Ident:
		id = f
	case *ast.SelectorExpr:
		id = f.Sel
	default:
		return nil, false
	}
	obj, ok := e.pkg.TypesInfo.Uses[id].(*types.Func)
	if !ok {
		return nil, false
	}
	decl := e.funcDecl(obj)
	if decl == nil || decl.Body == nil {
		return nil, false
	}
	seen := map[string]bool{}
	var out []string
	found, bad := false, false
	ast.Inspect(decl.Body, func(n ast.Node) bool {
		ret, ok := n.(*ast.ReturnStmt)
		if !ok || pos >= len(ret.Results) {
			return true
		}
		rs, res := e.resolveReason(decl, ret.Results[pos], depth+1)
		if res == Unresolved || len(rs) == 0 {
			bad = true
			return true
		}
		found = true
		for _, r := range rs {
			if !seen[r] {
				seen[r] = true
				out = append(out, r)
			}
		}
		return true
	})
	if bad || !found {
		return nil, false
	}
	sort.Strings(out)
	return out, true
}

func (e *extractor) funcDecl(obj *types.Func) *ast.FuncDecl {
	for _, f := range e.pkg.Syntax {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok {
				continue
			}
			if e.pkg.TypesInfo.Defs[fd.Name] == obj {
				return fd
			}
		}
	}
	return nil
}

// exprText renders an expression the way it is written, for the rows the
// generator could not fold.
func exprText(p *packages.Package, e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return exprText(p, v.X) + "." + v.Sel.Name
	case *ast.IndexExpr:
		return exprText(p, v.X) + "[" + exprText(p, v.Index) + "]"
	case *ast.CallExpr:
		return exprText(p, v.Fun) + "(…)"
	case *ast.BasicLit:
		return v.Value
	case *ast.StarExpr:
		return "*" + exprText(p, v.X)
	case *ast.UnaryExpr:
		return v.Op.String() + exprText(p, v.X)
	}
	if tv, ok := p.TypesInfo.Types[e]; ok && tv.Value != nil && tv.Value.Kind() == constant.String {
		return constant.StringVal(tv.Value)
	}
	return "<expression>"
}
