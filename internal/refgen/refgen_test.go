// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package refgen

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// repoRoot is where the generator reads its inputs from. The tests run from
// internal/refgen.
const repoRoot = "../.."

// The gate that makes the generated reference honest.
//
// The three prose answers per reason live in reasons.yaml, and nothing in Go
// forces them to exist. This test is what forces it: add a reason to the
// operator without describing it and this fails, so `make unit` fails, so CI
// fails — before `make verify` ever runs the generator. Delete an entry whose
// reason is still set and it fails the same way.
func TestEveryReasonTheOperatorCanSetIsDescribed(t *testing.T) {
	v := vocabulary(t)
	if _, err := loadAnnotations(v); err != nil {
		t.Fatalf("the reference's prose and the operator's code have parted company:\n%v", err)
	}
}

// A reason the extractor cannot fold is a hole in the published vocabulary: the
// page would describe every reason but that one, and say nothing about the gap
// unless someone read the honesty note. The extractor reports such sites rather
// than dropping them; this holds the count at zero, so that a new call site
// shaped in a way the resolver cannot follow is a test failure and not a silent
// omission.
func TestTheExtractorFoldsEveryConditionWrite(t *testing.T) {
	v := vocabulary(t)
	var bad []string
	for _, s := range v.Sites {
		if s.Resolution == Unresolved {
			bad = append(bad, s.Condition+" at "+s.File+" reason "+s.Expr)
		}
	}
	if len(bad) > 0 {
		t.Fatalf("%d condition write(s) have a reason the generator could not fold, so the "+
			"published vocabulary is incomplete by exactly those rows:\n  %s",
			len(bad), strings.Join(bad, "\n  "))
	}
	if len(v.Sites) == 0 {
		t.Fatal("no condition writes found at all; the extractor is not looking where it thinks it is")
	}
}

// Reasons are resolved by VARIABLE, not by name.
//
// reconcileRuntime declares a local called `reason` more than once, in separate
// blocks, for unrelated causes. An earlier draft resolved by name and unioned
// them, which published MaterialUnavailable as something PolicyApplyIncomplete
// could carry and RouteApplyFailed as something RevisionMaterialUnavailable
// could — a reference naming a plausible cause that was never checked, which is
// the failure AGENTS.md rule 8 names, one level up from the operator.
//
// This asserts the narrow set, so widening it fails rather than passing quietly.
func TestReasonsResolveByVariableAndNotByName(t *testing.T) {
	v := vocabulary(t)
	got := reasonsOf(t, v, "RevisionMaterialUnavailable")
	want := map[string]bool{"MaterialUnavailable": true, "MaterialInvalid": true}
	if len(got) != len(want) {
		t.Fatalf("RevisionMaterialUnavailable carries %v; it is written from one local whose only "+
			"assignments are MaterialUnavailable and MaterialInvalid", got)
	}
	for _, r := range got {
		if !want[r] {
			t.Errorf("RevisionMaterialUnavailable must not carry %q: that reason belongs to a "+
				"different local of the same name in the same function", r)
		}
	}
}

// A struct-field reason is narrowed through the function that BUILT the value.
//
// PolicyCompileFailed's reason arrives as `fails[0].reason`, a failure.reason
// like any other. Answering it from every write to failure.reason anywhere in
// the package would say this condition can carry ServingRouteNotAccepted, which
// it cannot; answering it from compileFailures, which built the slice, gives
// the three it really can.
func TestAFieldReasonIsNarrowedThroughItsProducer(t *testing.T) {
	v := vocabulary(t)
	got := reasonsOf(t, v, "PolicyCompileFailed")
	want := map[string]bool{
		"AuthInputAbsent": true, "AuthTransitionNotBuilt": true, "ConcernNotBuilt": true,
	}
	if len(got) != len(want) {
		t.Fatalf("PolicyCompileFailed carries %v; compileFailures builds exactly %d", got, len(want))
	}
	for _, r := range got {
		if !want[r] {
			t.Errorf("PolicyCompileFailed must not carry %q: nothing compileFailures builds "+
				"carries that reason", r)
		}
	}
}

// The test-reference scan is what the "no test names this reason" list rests
// on, and an over-eager matcher would empty that list and make the page claim
// coverage it does not have. AuthLockPending and AuthLockUnverified share a
// prefix, so a substring match would cross-credit them.
func TestTheTestReferenceScanMatchesOnWordBoundaries(t *testing.T) {
	v := vocabulary(t)
	refs, err := ScanTestReferences(repoRoot, v)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(refs) != len(v.ReasonsByName) {
		t.Fatalf("scanned %d reasons, the operator can set %d", len(refs), len(v.ReasonsByName))
	}
	var referenced, unreferenced int
	for _, tr := range refs {
		if len(tr.Files) > 0 {
			referenced++
		} else {
			unreferenced++
		}
	}
	// Both halves must be non-empty. All-referenced would mean the matcher
	// matches anything; none-referenced would mean it matches nothing. Either
	// way the published list is worthless.
	if referenced == 0 || unreferenced == 0 {
		t.Fatalf("the scan found %d referenced and %d unreferenced reasons; one of those being zero "+
			"means the matcher is broken, not that the repository changed", referenced, unreferenced)
	}
	for _, f := range refs["AuthLockPending"].Files {
		if !mentions(t, f, "AuthLockPending") && !mentions(t, f, "ReasonAuthLockPending") {
			t.Errorf("%s was credited with AuthLockPending and names neither the string nor its "+
				"constant; the matcher is matching a prefix", f)
		}
	}
}

// Nothing about the machine that ran the generator may reach a public page.
//
// Generators embed source paths by habit, and this repository's reference is
// published. The check is on the OUTPUT, not on the generator, because that is
// where the leak would appear.
func TestTheGeneratedReferenceCarriesNoLocalPath(t *testing.T) {
	// The home-directory names are alternated rather than written out one by
	// one, so that this line does not itself match the repository's own "no
	// absolute path in a committed file" grep and turn the guard into noise.
	leak := regexp.MustCompile(`(?i)/(Users|home|private|var/folders)/`)
	dir := filepath.Join(repoRoot, "docs", "reference")
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v — run `make reference`", dir, err)
	}
	var seen int
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		seen++
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			if leak.MatchString(line) {
				t.Errorf("%s:%d carries a filesystem path from the machine that generated it:\n  %s",
					e.Name(), i+1, strings.TrimSpace(line))
			}
		}
	}
	if seen < 3 {
		t.Fatalf("found %d generated page(s) under docs/reference; expected the three the "+
			"generator writes", seen)
	}
}

// Two generations of unchanged inputs must be byte-identical, or `make verify`
// fails for whoever runs it second and the drift check becomes noise people
// learn to re-run until it passes.
func TestGenerationIsDeterministic(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	for _, dir := range []string{a, b} {
		if err := renderValues(repoRoot, dir); err != nil {
			t.Fatalf("render values: %v", err)
		}
		if err := renderConditions(repoRoot, dir); err != nil {
			t.Fatalf("render conditions: %v", err)
		}
	}
	for _, name := range []string{"helm-values.md", "conditions.md"} {
		x, err := os.ReadFile(filepath.Join(a, name))
		if err != nil {
			t.Fatal(err)
		}
		y, err := os.ReadFile(filepath.Join(b, name))
		if err != nil {
			t.Fatal(err)
		}
		if string(x) != string(y) {
			t.Errorf("%s differs between two generations of the same inputs", name)
		}
	}
}

// The committed pages must equal a fresh generation — the same property `make
// verify` enforces, asserted here too so that `make unit` catches it without a
// working crdoc install. crd-agent.md is not covered: it needs the pinned
// binary, and `make verify` is where that is checked.
func TestTheCommittedReferenceMatchesAFreshGeneration(t *testing.T) {
	dir := t.TempDir()
	if err := renderValues(repoRoot, dir); err != nil {
		t.Fatalf("render values: %v", err)
	}
	if err := renderConditions(repoRoot, dir); err != nil {
		t.Fatalf("render conditions: %v", err)
	}
	for _, name := range []string{"helm-values.md", "conditions.md"} {
		fresh, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		committed, err := os.ReadFile(filepath.Join(repoRoot, "docs", "reference", name))
		if err != nil {
			t.Fatalf("read the committed %s: %v — run `make reference`", name, err)
		}
		if string(fresh) != string(committed) {
			t.Errorf("docs/reference/%s is stale: it differs from what the generator writes today. "+
				"Run `make reference` and commit.", name)
		}
	}
}

// A generator nobody runs is worse than no generator: its output reads as
// current and is not. This holds the WIRING — not the generator — so that
// removing `make reference` from `make verify`, or dropping docs/reference from
// the paths it compares, fails a test rather than quietly turning the drift
// check off.
func TestTheDriftCheckIsWiredIntoVerify(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(repoRoot, "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	mk := string(b)
	verify := mk[strings.Index(mk, "\nverify:"):]
	if i := strings.Index(verify, "\n## "); i > 0 {
		verify = verify[:i]
	}
	if !strings.Contains(verify, "$(MAKE) reference") {
		t.Error("`make verify` does not regenerate the reference, so a stale docs/reference/ " +
			"would pass CI")
	}
	if !strings.Contains(verify, "docs/reference") {
		t.Error("`make verify` does not compare docs/reference, so a reference that regenerated " +
			"differently would pass CI")
	}
	if !strings.Contains(mk, "\nreference:") {
		t.Error("there is no `make reference` target")
	}
	if !strings.Contains(mk, "CRDOC_VERSION            ?= ") {
		t.Error("crdoc is not pinned to an exact version; a generator whose output depends on when " +
			"it ran makes `make verify` fail for whoever picks up a new version first")
	}
}

func vocabulary(t *testing.T) *Vocabulary {
	t.Helper()
	v, err := ExtractVocabulary(repoRoot)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	return v
}

func reasonsOf(t *testing.T, v *Vocabulary, condition string) []string {
	t.Helper()
	for _, c := range v.Conditions {
		if c.Name == condition {
			if len(c.Reasons) == 0 {
				t.Fatalf("%s has no reasons; the extractor found no write of it", condition)
			}
			return c.Reasons
		}
	}
	t.Fatalf("%s is not a declared condition type", condition)
	return nil
}

func mentions(t *testing.T, rel, token string) bool {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot, rel))
	if err != nil {
		t.Fatal(err)
	}
	return regexp.MustCompile(`\b` + regexp.QuoteMeta(token) + `\b`).Match(b)
}
