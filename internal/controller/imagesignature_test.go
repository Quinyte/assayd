// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
)

// Design 02 A78: the message says whether a digest pins the image, and it says
// so from the spec. A tag reaches the operator on an install whose Agent CRD
// predates the digest rule, which envtest cannot build because it installs the
// current CRD, so this branch is pinned here.
func TestImageSignatureMessageSaysWhatPinsTheImage(t *testing.T) {
	const unchecked = "assayd verifies no image signature, and does not detect an admission verifier installed outside it"
	const digest = "@sha256:6d5d9666a268df6f000000000000000000000000000000000000000000000000"
	for _, tc := range []struct {
		name      string
		spec      assaydv1alpha1.AgentSpec
		want, not []string
	}{
		{
			name: "pinned",
			spec: assaydv1alpha1.AgentSpec{Runtime: &assaydv1alpha1.AgentRuntime{Image: "ghcr.io/acme/a:v1" + digest}},
			want: []string{"is pinned by a sha256 digest", unchecked, "nothing clears it yet"},
			not:  []string{"not pinned"},
		},
		{
			name: "a tag",
			spec: assaydv1alpha1.AgentSpec{Runtime: &assaydv1alpha1.AgentRuntime{Image: "ghcr.io/acme/a:v1"}},
			want: []string{`"ghcr.io/acme/a:v1" is not pinned by a sha256 digest`, unchecked, "nothing clears it yet"},
		},
		{
			name: "a digest that is not the tail",
			spec: assaydv1alpha1.AgentSpec{Runtime: &assaydv1alpha1.AgentRuntime{Image: "ghcr.io/acme/a" + digest + "x"}},
			want: []string{"is not pinned by a sha256 digest", unchecked, "nothing clears it yet"},
		},
		{
			name: "external",
			spec: assaydv1alpha1.AgentSpec{External: &assaydv1alpha1.ExternalAgent{Endpoint: "https://a.example.com"}},
			want: []string{"runs outside this cluster", unchecked,
				"nothing designed would clear this condition for it"},
			not: []string{"pinned by a sha256 digest", "nothing clears it yet"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newConditionSet(7)
			assessImageSignature(&assaydv1alpha1.Agent{Spec: tc.spec}, c)
			got, ok := c.get(assaydv1alpha1.CondImageSignatureUnverified)
			if !ok {
				t.Fatal("ImageSignatureUnverified was not asserted")
			}
			if got.Status != metav1.ConditionTrue || got.Reason != ReasonSignatureVerificationNotBuilt ||
				got.ObservedGeneration != 7 {
				t.Errorf("got %+v", got)
			}
			// The operator does not look for a verifier installed beside it, so
			// no message may say that nothing checks a signature (design 02 A78).
			for _, w := range append(tc.want, "does not affect Ready or Degraded") {
				if !strings.Contains(got.Message, w) {
					t.Errorf("message lacks %q:\n%s", w, got.Message)
				}
			}
			for _, n := range append(tc.not, "no image signature is checked") {
				if strings.Contains(got.Message, n) {
					t.Errorf("message says %q:\n%s", n, got.Message)
				}
			}
		})
	}
}

// ImageSignatureUnverified is owned and not sticky (design 02 A78): this
// operator is its only writer, so a pass that does not assert it CLEARS it.
// That is why every exit that builds a condition set calls
// assessImageSignature, and it leaves a later change free to retract it by no
// longer asserting it.
// Asserted through merge() rather than by reading ownedTypes, for the reason
// TestPolicyCompileFailedClearsOnAPassThatDoesNotAssertIt gives. Left out of
// ownedTypes, merge() would carry it forward as another controller's; listed
// in stickyTypes, it would survive as a record of a check that never ran.
func TestImageSignatureUnverifiedClearsOnAPassThatDoesNotAssertIt(t *testing.T) {
	prior := []metav1.Condition{{
		Type: string(assaydv1alpha1.CondImageSignatureUnverified), Status: metav1.ConditionTrue,
		Reason: ReasonSignatureVerificationNotBuilt, Message: "assayd verifies no image signature, and does not detect an admission verifier installed outside it",
	}}
	for _, c := range newConditionSet(2).merge(prior) {
		if c.Type == string(assaydv1alpha1.CondImageSignatureUnverified) {
			t.Errorf("ImageSignatureUnverified survived a pass that did not assert it, as %s/%s: "+
				"it must be classified owned and not sticky", c.Status, c.Reason)
		}
	}
}

// Every function that builds a fresh condition set is an exit that merges it,
// and an owned condition that exit does not assert is CLEARED there. The
// envtest cases drive the exits that exist today; this guards the next one.
// It scans this package's non-test source, so a new function calling
// newConditionSet without assessImageSignature fails here, before any
// fixture has been written for it (design 02 A78).
//
// Its limits: it sees DIRECT calls by name only, so a builder reached through
// a method value (f := newConditionSet) or a &conditionSet{} literal bypasses
// it; and it checks PRESENCE, not placement — a call placed after an early
// return passes here. Placement is pinned by the envtest cases in
// test/envtest/imagesignature_test.go, which drive the ordinary path's
// earliest exit and a later one.
func TestEveryConditionSetBuilderAssessesTheImageSignature(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var builders int
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			calls := map[string]bool{}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if c, ok := n.(*ast.CallExpr); ok {
					if id, ok := c.Fun.(*ast.Ident); ok {
						calls[id.Name] = true
					}
				}
				return true
			})
			if !calls["newConditionSet"] {
				continue
			}
			builders++
			if !calls["assessImageSignature"] {
				t.Errorf("%s: %s builds a condition set and does not call assessImageSignature, so "+
					"ImageSignatureUnverified is cleared on every pass that leaves through it",
					fset.Position(fn.Pos()), fn.Name.Name)
			}
		}
	}
	// A scan that finds nothing proves nothing: four builders exist today.
	if builders < 4 {
		t.Errorf("found %d functions that build a condition set; want at least 4. The scan no "+
			"longer sees the code it guards", builders)
	}
}
