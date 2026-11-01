// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package docs

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Every package that holds a test runs in `make test` and in CI.
//
// `test/responder` and `test/mcpserver` carried unit tests that neither ran
// (#129): the fixtures were built and pushed by `hack/e2e.sh`, so their own
// rows — among them the one pinning the `ANSWER_PREFIX` and `ANSWER_MARKER`
// knobs an e2e depends on — were compiled by nothing and failed for no one. A
// test that no gate runs reads as coverage and is not.
//
// The inventory is derived, not listed: the tested packages come from
// `go list`, and what each gate reaches comes from the Makefile's recipes, the
// scripts they call, and the targets `.github/workflows/ci.yml` invokes. So a
// new package with a test, or a path dropped from a recipe, fails here without
// anyone remembering to edit this file.
func TestEveryTestedPackageRunsInMakeTestAndCI(t *testing.T) {
	tested := testedPackages(t, "./...")
	if len(tested) < 10 {
		t.Fatalf("only %d tested packages found; the listing is broken and this gate would "+
			"pass by having nothing to check", len(tested))
	}

	mk := makefileRecipes(t)
	testPrereqs := makeTestPrerequisites(t)
	ciTargets := ciMakeTargets(t)

	// A package `make test` may not reach, and why. Each entry must still be
	// needed and must still be reached by CI, so the list cannot rot into a
	// silent allowance.
	notInMakeTest := map[string]string{
		"test/e2e": "needs a real cluster; `make e2e` runs it through hack/e2e.sh",
	}

	byMakeTest := reachedBy(t, mk, testPrereqs)
	byCI := reachedBy(t, mk, ciTargets)

	for _, pkg := range tested {
		if _, ok := byCI[pkg]; !ok {
			t.Errorf("%s holds tests that no target in .github/workflows/ci.yml runs; "+
				"add it to a `go test` line of a target CI invokes", pkg)
		}
		_, exempt := notInMakeTest[pkg]
		_, ran := byMakeTest[pkg]
		switch {
		case !ran && !exempt:
			t.Errorf("%s holds tests that `make test` does not run; add it to a `go test` "+
				"line of one of the test target's prerequisites (%s)", pkg, strings.Join(testPrereqs, " "))
		case ran && exempt:
			t.Errorf("%s is exempted from `make test` (%q) but `make test` runs it; "+
				"drop the exemption", pkg, notInMakeTest[pkg])
		}
	}
	for pkg := range notInMakeTest {
		if !contains(tested, pkg) {
			t.Errorf("%s is exempted from `make test` but holds no tests; drop the exemption", pkg)
		}
	}
}

// testedPackages lists, relative to the module root, every package matched by
// the patterns that holds at least one test file.
func testedPackages(t *testing.T, patterns ...string) []string {
	t.Helper()
	args := append([]string{"list", "-f", "{{if or .TestGoFiles .XTestGoFiles}}{{.Dir}}{{end}}"}, patterns...)
	cmd := exec.Command("go", args...)
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list %s: %v", strings.Join(patterns, " "), err)
	}
	root, err := filepath.Abs(repoRoot)
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	var pkgs []string
	for _, dir := range strings.Fields(string(out)) {
		rel, err := filepath.Rel(root, dir)
		if err != nil {
			t.Fatalf("relativize %s: %v", dir, err)
		}
		pkgs = append(pkgs, filepath.ToSlash(rel))
	}
	sort.Strings(pkgs)
	return pkgs
}

var (
	makeTargetLine = regexp.MustCompile(`^([a-z][a-z0-9-]*):(.*)$`)
	goTestPatterns = regexp.MustCompile(`go test\b([^&|;]*)`)
	hackScript     = regexp.MustCompile(`\./hack/[a-z0-9-]+\.sh`)
	ciMakeCall     = regexp.MustCompile(`\bmake ([a-z][a-z0-9-]*)`)
	packagePattern = regexp.MustCompile(`^\./[A-Za-z0-9_/-]+(/\.\.\.)?$`)
)

// makefileRecipes maps each Makefile target to its recipe text, including a
// recipe given inline after `;`.
func makefileRecipes(t *testing.T) map[string]string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot, "Makefile"))
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}
	recipes := map[string]string{}
	current := ""
	for _, line := range strings.Split(string(b), "\n") {
		if m := makeTargetLine.FindStringSubmatch(line); m != nil && !strings.HasPrefix(m[2], "=") {
			current = m[1]
			if _, inline, ok := strings.Cut(m[2], ";"); ok {
				recipes[current] += inline + "\n"
			}
			continue
		}
		if strings.HasPrefix(line, "\t") && current != "" {
			recipes[current] += line + "\n"
			continue
		}
		if strings.TrimSpace(line) != "" && !strings.HasPrefix(line, "#") {
			current = ""
		}
	}
	return recipes
}

// makeTestPrerequisites returns the targets `make test` depends on.
func makeTestPrerequisites(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot, "Makefile"))
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		if rest, ok := strings.CutPrefix(line, "test:"); ok {
			deps, _, _ := strings.Cut(rest, "##")
			if fields := strings.Fields(deps); len(fields) > 0 {
				return fields
			}
		}
	}
	t.Fatal("the Makefile has no `test:` rule with prerequisites")
	return nil
}

// ciMakeTargets returns the make targets .github/workflows/ci.yml invokes,
// ignoring comments.
func ciMakeTargets(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot, ".github/workflows/ci.yml"))
	if err != nil {
		t.Fatalf("read ci.yml: %v", err)
	}
	seen := map[string]bool{}
	var targets []string
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		for _, m := range ciMakeCall.FindAllStringSubmatch(line, -1) {
			if !seen[m[1]] {
				seen[m[1]] = true
				targets = append(targets, m[1])
			}
		}
	}
	if len(targets) == 0 {
		t.Fatal("ci.yml invokes no make target; the parse is broken")
	}
	return targets
}

// reachedBy returns the tested packages that the `go test` lines of the given
// targets' recipes, and of the hack/ scripts those recipes call, run. Comment
// lines are skipped: a script that explains `go test` runs nothing.
func reachedBy(t *testing.T, recipes map[string]string, targets []string) map[string]bool {
	t.Helper()
	var patterns []string
	for _, target := range targets {
		text := recipes[target]
		for _, script := range hackScript.FindAllString(text, -1) {
			b, err := os.ReadFile(filepath.Join(repoRoot, script))
			if err != nil {
				t.Fatalf("target %s calls %s, which cannot be read: %v", target, script, err)
			}
			text += string(b)
		}
		for _, line := range strings.Split(text, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue
			}
			for _, m := range goTestPatterns.FindAllStringSubmatch(line, -1) {
				for _, field := range strings.Fields(m[1]) {
					if packagePattern.MatchString(field) {
						patterns = append(patterns, field)
					}
				}
			}
		}
	}
	reached := map[string]bool{}
	if len(patterns) == 0 {
		return reached
	}
	for _, pkg := range testedPackages(t, patterns...) {
		reached[pkg] = true
	}
	return reached
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
