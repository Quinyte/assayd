// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
)

// T39 (design 16 §1.1, A7). The eval controller sends a case and records its
// answer on a context shutdown does not cancel, bounded at 5 s + 10 s + 5 s =
// 20 s, and a graceful stop lets that finish only because the manager waits for
// reconciles in flight. How long it waits is GracefulShutdownTimeout. Left
// unset, it is controller-runtime's default, and a bound that rests on a
// library's unset default is rule 7's case: nothing here would notice the
// default moving. So the option must be SET, and at 25 s or more.
func TestManagerWaitsForReconcilesInFlightOnShutdown(t *testing.T) {
	opts := managerOptions(":8080", ":8081", true)
	if opts.GracefulShutdownTimeout == nil {
		t.Fatal("the manager options leave GracefulShutdownTimeout unset, so the shutdown bound " +
			"is controller-runtime's default and not this operator's (design 16 A7)")
	}
	if got, floor := *opts.GracefulShutdownTimeout, 25*time.Second; got < floor {
		t.Errorf("GracefulShutdownTimeout is %v; it must be at least %v so a case in flight at "+
			"shutdown, bounded at 20 s, can finish and record what the candidate answered "+
			"(design 16 A7)", got, floor)
	}
}

// T39's other half. The test above reads managerOptions, and it proves nothing
// about the binary if run() builds its manager from anything else: replacing
// the call with ctrl.Options{Scheme: scheme} compiles and leaves it green. So
// the source is held to exactly one ctrl.NewManager call, whose options
// argument is a call to managerOptions.
func TestTheManagerIsBuiltFromManagerOptions(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "main.go", nil, 0)
	if err != nil {
		t.Fatalf("parse main.go: %v", err)
	}
	var calls []*ast.CallExpr
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "NewManager" {
			if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "ctrl" {
				calls = append(calls, call)
			}
		}
		return true
	})
	if len(calls) != 1 {
		t.Fatalf("main.go has %d ctrl.NewManager calls; want exactly one, built from managerOptions", len(calls))
	}
	if len(calls[0].Args) != 2 {
		t.Fatalf("ctrl.NewManager is called with %d arguments; want (cfg, managerOptions(...))", len(calls[0].Args))
	}
	opts, ok := calls[0].Args[1].(*ast.CallExpr)
	if !ok {
		t.Fatal("ctrl.NewManager's options are not a call to managerOptions, so the shutdown bound " +
			"the test above pins is not the one the binary runs with (design 16 A7)")
	}
	if fn, ok := opts.Fun.(*ast.Ident); !ok || fn.Name != "managerOptions" {
		t.Fatal("ctrl.NewManager's options are built by something other than managerOptions, so the " +
			"shutdown bound the test above pins is not the one the binary runs with (design 16 A7)")
	}
}

// The gate branch reads the Agent CRD through the manager's uncached reader,
// which resolves the kind through this scheme (design 16 §1.1 step 11). An
// unregistered kind fails that read on every gated Agent, and each would hold
// under EvalRecordUnverifiable for a cause in this binary, not the cluster.
func TestTheSchemeKnowsTheCustomResourceDefinition(t *testing.T) {
	gvk := apiextensionsv1.SchemeGroupVersion.WithKind("CustomResourceDefinition")
	if !scheme.Recognizes(gvk) {
		t.Fatalf("the operator's scheme does not register %s, so the gate branch's schema check "+
			"cannot read the Agent CRD", gvk)
	}
}

// The eval controller is wired into the binary (design 16 §1.1): run() builds
// it with NewEvalReconciler from the manager's uncached reader, and registers
// it on the manager. A binary that built only the Agent reconciler would hold
// every gated candidate at EvalRunning with nothing running — loud and wrong
// — and every other test here would pass. So the source is held to one
// NewEvalReconciler call whose reader is mgr.GetAPIReader(), and to a
// SetupWithManager call on what it returned.
func TestTheBinaryRunsTheEvalController(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "main.go", nil, 0)
	if err != nil {
		t.Fatalf("parse main.go: %v", err)
	}
	var built []*ast.AssignStmt
	setups := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.AssignStmt:
			if len(n.Rhs) != 1 {
				return true
			}
			if call, ok := n.Rhs[0].(*ast.CallExpr); ok {
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "NewEvalReconciler" {
					built = append(built, n)
				}
			}
		case *ast.CallExpr:
			if sel, ok := n.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "SetupWithManager" {
				if id, ok := sel.X.(*ast.Ident); ok {
					setups[id.Name] = true
				}
			}
		}
		return true
	})
	if len(built) != 1 {
		t.Fatalf("main.go calls NewEvalReconciler %d times; want exactly one", len(built))
	}
	call := built[0].Rhs[0].(*ast.CallExpr)
	if len(call.Args) < 2 {
		t.Fatalf("NewEvalReconciler is called with %d arguments", len(call.Args))
	}
	reader, ok := call.Args[1].(*ast.CallExpr)
	if sel, isSel := func() (*ast.SelectorExpr, bool) {
		if !ok {
			return nil, false
		}
		s, ok := reader.Fun.(*ast.SelectorExpr)
		return s, ok
	}(); !isSel || sel.Sel.Name != "GetAPIReader" {
		t.Error("the eval controller's reader is not mgr.GetAPIReader(): it must read the Agent " +
			"uncached before every write (design 16 §1.1 step 10)")
	}
	id, ok := built[0].Lhs[0].(*ast.Ident)
	if !ok || !setups[id.Name] {
		t.Error("main.go builds the eval controller and never registers it with SetupWithManager")
	}
}
