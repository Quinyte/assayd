// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
	"time"
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
