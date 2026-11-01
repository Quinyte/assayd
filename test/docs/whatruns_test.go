// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package docs

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// Every package that holds an untagged test is named by a `go test` line that
// `make test` runs and by one that CI runs.
//
// `test/responder` and `test/mcpserver` carried unit tests that neither ran
// (#129): the fixtures were built and pushed by `hack/e2e.sh`, so their own
// rows — among them the one pinning the `ANSWER_PREFIX` and `ANSWER_MARKER`
// knobs an e2e depends on — were compiled by nothing and failed for no one.
//
// What this checks, exactly. The tested packages are those `go list` reports
// with an untagged test file. A package counts as run when a package argument
// of a `go test` line — in the recipe of one of `make test`'s prerequisites, or
// of a target a step of `.github/workflows/ci.yml` invokes, or in a hack/
// script such a recipe calls — matches it. It also requires every package
// `make unit` names to be named by `make race` too, since the race run is
// review evidence.
//
// What it does not read, and so cannot catch. Build-tagged test files in a
// package that also holds untagged tests are invisible to it:
// `test/conformance`'s `cluster` files run only in `make conformance-cluster`,
// which CI does not run. A package whose every test is tagged is refused
// rather than missed. It does not follow a
// recursive `$(MAKE)`, a target's prerequisites, or a script called from a
// script, and it reads no step's `if:` and no workflow trigger. Where reading
// would be guesswork it refuses instead of guessing: a `go test` line with
// `-run`, a shell substitution, a make variable in package position, or any
// argument it cannot classify fails the test and names the argument, and so
// does a gating CI step or job marked `continue-on-error`.
func TestEveryTestedPackageRunsInMakeTestAndCI(t *testing.T) {
	tested := testedPackages(t, "./...")
	if len(tested) < 10 {
		t.Fatalf("only %d tested packages found; the listing is broken and this gate would "+
			"pass by having nothing to check", len(tested))
	}

	mk := readRepoFile(t, "Makefile")
	ci := readRepoFile(t, ".github/workflows/ci.yml")
	g, err := readGates(mk, ci, func(path string) (string, error) {
		b, err := os.ReadFile(filepath.Join(repoRoot, path))
		return string(b), err
	})
	if err != nil {
		t.Fatal(err)
	}

	// A package `make test` may not reach, and why. Each entry must still be
	// needed and must still be reached by CI, so the list cannot rot into a
	// silent allowance.
	notInMakeTest := map[string]string{
		"test/e2e": "needs a real cluster; `make e2e` runs it through hack/e2e.sh",
	}

	byMakeTest := resolvePatterns(t, g.makeTest)
	byCI := resolvePatterns(t, g.ci)

	for _, pkg := range tested {
		if !byCI[pkg] {
			t.Errorf("%s holds tests that no target in .github/workflows/ci.yml runs; "+
				"add it to a `go test` line of a target CI invokes", pkg)
		}
		_, exempt := notInMakeTest[pkg]
		switch ran := byMakeTest[pkg]; {
		case !ran && !exempt:
			t.Errorf("%s holds tests that `make test` does not run; add it to a `go test` "+
				"line of one of the test target's prerequisites (%s)", pkg, strings.Join(g.testPrereqs, " "))
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

	// A package whose every test carries a build tag is invisible to the
	// listing above, so it is refused here rather than passed silently.
	for _, pkg := range onlyTaggedTestPackages(t) {
		t.Errorf("%s holds tests only in files a build tag excludes, which this check cannot "+
			"see; give it an untagged test that `make test` and CI run", pkg)
	}

	byUnit := resolvePatterns(t, g.unit)
	if len(byUnit) == 0 {
		t.Fatal("`make unit` names no tested package; the parse is broken")
	}
	byRace := resolvePatterns(t, g.race)
	for _, pkg := range tested {
		if byUnit[pkg] && !byRace[pkg] {
			t.Errorf("%s runs in `make unit` but not in `make race`; the race run is review "+
				"evidence, so name it in both", pkg)
		}
	}
}

// The parser above is held to synthetic text here, so a change that lets it
// read too much — and pass everything — fails without the live Makefile ever
// having to break.
func TestTheWhatRunsParserReadsOnlyWhatRuns(t *testing.T) {
	const ciUnit = "jobs:\n  j:\n    steps:\n      - run: make unit\n"
	const unitTest = "test: unit\nunit:\n\tgo test ./internal/... -count=1\nrace:\n\tgo test -race ./internal/...\n"
	noScripts := func(path string) (string, error) { return "", fmt.Errorf("no script %s", path) }

	for _, tc := range []struct {
		name     string
		makefile string
		ci       string
		scripts  map[string]string
		makeTest []string
		ciRuns   []string
		wantErr  string
	}{{
		name:     "a plain recipe is read",
		makefile: unitTest,
		ci:       ciUnit,
		makeTest: []string{"./internal/..."},
		ciRuns:   []string{"./internal/..."},
	}, {
		name:     "a comment that mentions go test runs nothing",
		makefile: "test: unit\nunit:\n\t# go test ./ghost/...\n\tgo test ./internal/...\n",
		ci:       ciUnit,
		makeTest: []string{"./internal/..."},
		ciRuns:   []string{"./internal/..."},
	}, {
		name:     "a comment that names a script calls nothing",
		makefile: "test: unit\nunit:\n\t# see ./hack/ghost.sh\n\tgo test ./internal/...\n",
		ci:       ciUnit,
		makeTest: []string{"./internal/..."},
		ciRuns:   []string{"./internal/..."},
	}, {
		name:     "an echo that mentions go test runs nothing",
		makefile: "test: unit\nunit:\n\t@echo go test ./ghost/...\n\tgo test ./internal/...\n",
		ci:       ciUnit,
		makeTest: []string{"./internal/..."},
		ciRuns:   []string{"./internal/..."},
	}, {
		name:     "a CI step that only echoes make runs nothing",
		makefile: unitTest + "docs:\n\tgo test ./test/docs/...\n",
		ci:       "jobs:\n  j:\n    steps:\n      - run: make docs\n      - run: echo \"skip make unit\"\n",
		makeTest: []string{"./internal/..."},
		ciRuns:   []string{"./test/docs/..."},
	}, {
		name:     "a CI make call after && is read",
		makefile: unitTest,
		ci:       "jobs:\n  j:\n    steps:\n      - run: |\n          set -e && make unit\n",
		makeTest: []string{"./internal/..."},
		ciRuns:   []string{"./internal/..."},
	}, {
		name:     "a CI variable override is not a target",
		makefile: unitTest,
		ci:       "jobs:\n  j:\n    steps:\n      - run: make COUNT=3 unit\n",
		makeTest: []string{"./internal/..."},
		ciRuns:   []string{"./internal/..."},
	}, {
		name:     "a CI make flag is refused",
		makefile: unitTest,
		ci:       "jobs:\n  j:\n    steps:\n      - run: make -C sub unit\n",
		wantErr:  "passes -C",
	}, {
		name:     "$(GO) test is read when the Makefile defines GO",
		makefile: "GO := go\ntest: unit\nunit:\n\t$(GO) test ./internal/...\n",
		ci:       ciUnit,
		makeTest: []string{"./internal/..."},
		ciRuns:   []string{"./internal/..."},
	}, {
		name:     "$(GO) test is refused when the Makefile defines no GO",
		makefile: "test: unit\nunit:\n\t$(GO) test ./internal/...\n",
		ci:       ciUnit,
		wantErr:  "defines no GO",
	}, {
		name:     "a backslash-continued recipe is joined",
		makefile: "test: unit\nunit:\n\tgo test ./internal/... \\\n\t\t./api/... -count=1\n",
		ci:       ciUnit,
		makeTest: []string{"./internal/...", "./api/..."},
		ciRuns:   []string{"./internal/...", "./api/..."},
	}, {
		name:     "a script called through bash is read",
		makefile: "test: unit\nunit:\n\tbash hack/x.sh\n",
		ci:       ciUnit,
		scripts:  map[string]string{"hack/x.sh": "#!/bin/bash\n# go test ./ghost/...\nFOO=1 go test ./test/x/... -v\n"},
		makeTest: []string{"./test/x/..."},
		ciRuns:   []string{"./test/x/..."},
	}, {
		name:     "a script called by path is read",
		makefile: "test: unit\nunit:\n\t./hack/x.sh\n",
		ci:       ciUnit,
		scripts:  map[string]string{"hack/x.sh": "go test ./test/x/... 2>&1 | tee log\n"},
		makeTest: []string{"./test/x/..."},
		ciRuns:   []string{"./test/x/..."},
	}, {
		name:     "a shell-computed package list is refused",
		makefile: "test: unit\nunit:\n\tgo test ./internal/... $$(go list ./cmd/... | grep -v operator)\n",
		ci:       ciUnit,
		wantErr:  `unparsed ` + "`go test`" + ` argument "$$(go"`,
	}, {
		name:     "a shell-computed package list ahead of the paths is refused",
		makefile: "test: unit\nunit:\n\tgo test $$(go list ./internal/... | grep -v controller) ./api/...\n",
		ci:       ciUnit,
		wantErr:  `"$$(go"`,
	}, {
		name:     "a make variable in package position is refused",
		makefile: "test: unit\nunit:\n\tgo test $(UNIT_PKGS) -count=1\n",
		ci:       ciUnit,
		wantErr:  `"$(UNIT_PKGS)"`,
	}, {
		name:     "-run is refused",
		makefile: "test: unit\nunit:\n\tgo test ./internal/... -run \"^$$\"\n",
		ci:       ciUnit,
		wantErr:  "-run",
	}, {
		name:     "-run= is refused",
		makefile: "test: unit\nunit:\n\tgo test ./internal/... -run=Nothing\n",
		ci:       ciUnit,
		wantErr:  "-run",
	}, {
		name:     "a conditional -run CI never sets is empty in CI",
		makefile: "test: unit\nunit:\n\t./hack/x.sh\n",
		ci:       ciUnit,
		scripts:  map[string]string{"hack/x.sh": "go test ./test/x/... -v ${X_RUN:+-run \"${X_RUN}\"}\n"},
		makeTest: []string{"./test/x/..."},
		ciRuns:   []string{"./test/x/..."},
	}, {
		name:     "a conditional -run CI mentions is refused",
		makefile: "test: unit\nunit:\n\t./hack/x.sh\n",
		ci:       "jobs:\n  j:\n    steps:\n      - run: make unit\n        env:\n          X_RUN: Nothing\n",
		scripts:  map[string]string{"hack/x.sh": "go test ./test/x/... ${X_RUN:+-run \"${X_RUN}\"}\n"},
		wantErr:  "X_RUN",
	}, {
		name:     "continue-on-error on a gating step is refused",
		makefile: unitTest,
		ci:       "jobs:\n  j:\n    steps:\n      - run: make unit\n        continue-on-error: true\n",
		wantErr:  "continue-on-error",
	}, {
		name:     "continue-on-error on a gating job is refused",
		makefile: unitTest,
		ci:       "jobs:\n  j:\n    continue-on-error: true\n    steps:\n      - run: make unit\n",
		wantErr:  "continue-on-error",
	}, {
		name:     "a CI target with no recipe is refused",
		makefile: unitTest,
		ci:       "jobs:\n  j:\n    steps:\n      - run: make unit\n      - run: make ghost\n",
		wantErr:  "ghost",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			script := noScripts
			if tc.scripts != nil {
				script = func(path string) (string, error) {
					s, ok := tc.scripts[path]
					if !ok {
						return "", fmt.Errorf("no script %s", path)
					}
					return s, nil
				}
			}
			g, err := readGates(tc.makefile, tc.ci, script)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("got error %v, want one containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !sameSet(g.makeTest, tc.makeTest) {
				t.Errorf("make test runs %q, want %q", g.makeTest, tc.makeTest)
			}
			if !sameSet(g.ci, tc.ciRuns) {
				t.Errorf("CI runs %q, want %q", g.ci, tc.ciRuns)
			}
		})
	}
}

// gates holds the `go test` package patterns each gate runs.
type gates struct {
	testPrereqs []string
	makeTest    []string
	ci          []string
	unit        []string
	race        []string
}

// readGates derives, from the Makefile's text, ci.yml's text and the hack/
// scripts the recipes call, the package patterns each gate runs. It refuses
// what it cannot read rather than guessing at it.
func readGates(makefile, ciYAML string, script func(path string) (string, error)) (gates, error) {
	var g gates
	recipes, definesGO := parseMakefile(makefile)
	prereqs, err := makeTestPrerequisites(makefile)
	if err != nil {
		return g, err
	}
	g.testPrereqs = prereqs
	ciTargets, err := ciMakeTargets(ciYAML)
	if err != nil {
		return g, err
	}
	collect := func(gate string, targets []string) ([]string, error) {
		var out []string
		for _, target := range targets {
			recipe, ok := recipes[target]
			if !ok {
				return nil, fmt.Errorf("%s calls `make %s`, and the Makefile has no recipe for it", gate, target)
			}
			p, err := recipePatterns(target, recipe, definesGO, ciYAML, script)
			if err != nil {
				return nil, err
			}
			out = append(out, p...)
		}
		return out, nil
	}
	if g.makeTest, err = collect("`make test`", prereqs); err != nil {
		return g, err
	}
	if g.ci, err = collect("ci.yml", ciTargets); err != nil {
		return g, err
	}
	if _, ok := recipes["race"]; ok {
		if g.unit, err = collect("the race parity check", []string{"unit"}); err != nil {
			return g, err
		}
		if g.race, err = collect("the race parity check", []string{"race"}); err != nil {
			return g, err
		}
	}
	return g, nil
}

var (
	makeTargetLine = regexp.MustCompile(`^([a-z][a-z0-9-]*):(.*)$`)
	makeDefinesGO  = regexp.MustCompile(`(?m)^GO\s*(:=|\?=|=)`)
	// A `go test` at command position: after optional make prefixes and
	// environment assignments, a quoted value being one word.
	goTestCommand = regexp.MustCompile(`^[@-]*\s*(?:[A-Za-z_][A-Za-z0-9_]*=(?:"[^"]*"|'[^']*'|\S*)\s+)*(go|\$\(GO\))\s+test(?:\s+(.*))?$`)
	// `./hack/x.sh`, `bash hack/x.sh` or `sh hack/x.sh`.
	hackScript = regexp.MustCompile(`(?:^|\s)(?:\./|bash\s+|sh\s+)(hack/[A-Za-z0-9_-]+\.sh)\b`)
	// `${NAME:+...}`, at most one `${...}` deep.
	conditionalExpansion = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*):\+(?:[^{}]|\$\{[A-Za-z0-9_]+\})*\}`)
	packagePattern       = regexp.MustCompile(`^\./(\.\.\.|[A-Za-z0-9_-]+(/[A-Za-z0-9_-]+)*(/\.\.\.)?)$`)
	makeCommand          = regexp.MustCompile(`^(?:[A-Za-z_][A-Za-z0-9_]*=\S*\s+)*make(?:\s+(.*))?$`)
	// Flags of `go test` whose value may be the next word.
	goTestValueFlags = map[string]bool{"-timeout": true, "-tags": true, "-count": true, "-p": true,
		"-parallel": true, "-coverprofile": true, "-covermode": true, "-coverpkg": true}
)

// logicalLines splits text into lines, joining a line that ends in `\` to the
// next one.
func logicalLines(text string) []string {
	var out []string
	var cur strings.Builder
	for _, line := range strings.Split(text, "\n") {
		if strings.HasSuffix(line, "\\") {
			cur.WriteString(strings.TrimSuffix(line, "\\"))
			cur.WriteString(" ")
			continue
		}
		cur.WriteString(line)
		out = append(out, cur.String())
		cur.Reset()
	}
	return out
}

// parseMakefile maps each target to its recipe text, including a recipe given
// inline after `;`, and reports whether the Makefile defines GO.
func parseMakefile(text string) (map[string]string, bool) {
	recipes := map[string]string{}
	current := ""
	for _, line := range logicalLines(text) {
		if m := makeTargetLine.FindStringSubmatch(line); m != nil && !strings.HasPrefix(m[2], "=") {
			current = m[1]
			recipes[current] += ""
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
	return recipes, makeDefinesGO.MatchString(text)
}

// makeTestPrerequisites returns the targets `make test` depends on.
func makeTestPrerequisites(text string) ([]string, error) {
	for _, line := range logicalLines(text) {
		if rest, ok := strings.CutPrefix(line, "test:"); ok {
			deps, _, _ := strings.Cut(rest, "##")
			if fields := strings.Fields(deps); len(fields) > 0 {
				return fields, nil
			}
		}
	}
	return nil, errors.New("the Makefile has no `test:` rule with prerequisites")
}

// recipePatterns returns the package patterns a recipe's `go test` lines run,
// with those of the hack/ scripts it calls.
func recipePatterns(target, recipe string, definesGO bool, ciYAML string, script func(string) (string, error)) ([]string, error) {
	out, err := goTestPatternsIn(target, recipe, definesGO, ciYAML)
	if err != nil {
		return nil, err
	}
	for _, line := range logicalLines(recipe) {
		if isComment(line) {
			continue
		}
		for _, m := range hackScript.FindAllStringSubmatch(line, -1) {
			body, err := script(m[1])
			if err != nil {
				return nil, fmt.Errorf("target %s calls %s, which cannot be read: %w", target, m[1], err)
			}
			p, err := goTestPatternsIn(m[1], body, false, ciYAML)
			if err != nil {
				return nil, err
			}
			out = append(out, p...)
		}
	}
	return out, nil
}

// goTestPatternsIn returns the package arguments of every `go test` at command
// position in text. Comment lines run nothing and are skipped.
func goTestPatternsIn(where, text string, definesGO bool, ciYAML string) ([]string, error) {
	var out []string
	for _, line := range logicalLines(text) {
		if isComment(line) {
			continue
		}
		for _, seg := range commandSegments(line) {
			m := goTestCommand.FindStringSubmatch(strings.TrimSpace(seg))
			if m == nil {
				continue
			}
			if m[1] == "$(GO)" && !definesGO {
				return nil, fmt.Errorf("%s: `$(GO) test`, but the Makefile defines no GO", where)
			}
			p, err := goTestPackages(where, m[2], ciYAML)
			if err != nil {
				return nil, err
			}
			out = append(out, p...)
		}
	}
	return out, nil
}

// goTestPackages classifies each argument of one `go test` command and returns
// its package patterns, or refuses the first argument it cannot classify. A
// shell substitution or a make variable is never a literal package path, so
// it is refused by the same rule.
func goTestPackages(where, args, ciYAML string) ([]string, error) {
	unparsed := func(arg string) error {
		return fmt.Errorf("%s: unparsed `go test` argument %q in `go test %s`; this check reads "+
			"literal package paths only", where, arg, strings.TrimSpace(args))
	}
	for _, m := range conditionalExpansion.FindAllStringSubmatch(args, -1) {
		if strings.Contains(ciYAML, m[1]) {
			return nil, fmt.Errorf("%s: `go test` carries %q, and ci.yml mentions %s, so CI may set it "+
				"and change what runs", where, m[0], m[1])
		}
	}
	args = conditionalExpansion.ReplaceAllString(args, "")
	// Output redirection and a pipe end the arguments.
	if i := strings.IndexAny(args, "|>"); i >= 0 {
		args = args[:i]
		args = strings.TrimSuffix(strings.TrimSpace(args), "2")
	}
	var out []string
	fields := strings.Fields(args)
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		name, _, hasValue := strings.Cut(f, "=")
		switch {
		case name == "-run" || name == "-skip":
			return nil, fmt.Errorf("%s: `go test %s` filters tests with %s, which this check cannot "+
				"read; it would count a package as run while running none of its tests",
				where, strings.TrimSpace(args), name)
		case strings.HasPrefix(f, "-"):
			if !hasValue && goTestValueFlags[name] {
				i++
			}
		case packagePattern.MatchString(f):
			out = append(out, f)
		default:
			return nil, unparsed(f)
		}
	}
	return out, nil
}

// commandSegments splits a shell line on `&&`, `||` and `;`.
func commandSegments(line string) []string {
	line = strings.NewReplacer("&&", "\x00", "||", "\x00", ";", "\x00").Replace(line)
	return strings.Split(line, "\x00")
}

func isComment(line string) bool {
	return strings.HasPrefix(strings.TrimSpace(line), "#")
}

// ciMakeTargets returns the make targets the steps of a workflow invoke at
// command position. It refuses a step, or a job, that calls one and is marked
// `continue-on-error`, because that step's failure does not fail CI.
func ciMakeTargets(ciYAML string) ([]string, error) {
	var wf struct {
		Jobs map[string]struct {
			ContinueOnError any `yaml:"continue-on-error"`
			Steps           []struct {
				Name            string `yaml:"name"`
				Run             string `yaml:"run"`
				ContinueOnError any    `yaml:"continue-on-error"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(ciYAML), &wf); err != nil {
		return nil, fmt.Errorf("parse ci.yml: %w", err)
	}
	jobs := make([]string, 0, len(wf.Jobs))
	for name := range wf.Jobs {
		jobs = append(jobs, name)
	}
	sort.Strings(jobs)
	seen := map[string]bool{}
	var targets []string
	for _, job := range jobs {
		j := wf.Jobs[job]
		for _, step := range j.Steps {
			stepTargets, err := runMakeTargets(step.Run)
			if err != nil {
				return nil, fmt.Errorf("ci.yml job %s: %w", job, err)
			}
			if len(stepTargets) == 0 {
				continue
			}
			for _, coe := range []any{j.ContinueOnError, step.ContinueOnError} {
				if coe != nil && coe != false {
					return nil, fmt.Errorf("ci.yml job %s calls `make %s` with continue-on-error: %v, "+
						"so its failure would not fail CI", job, strings.Join(stepTargets, " "), coe)
				}
			}
			for _, target := range stepTargets {
				if !seen[target] {
					seen[target] = true
					targets = append(targets, target)
				}
			}
		}
	}
	if len(targets) == 0 {
		return nil, errors.New("ci.yml invokes no make target; the parse is broken")
	}
	return targets, nil
}

// runMakeTargets returns the targets of every `make` at command position in a
// step's script. A `make` inside a string or a substitution is not one.
func runMakeTargets(run string) ([]string, error) {
	var out []string
	for _, line := range logicalLines(run) {
		if isComment(line) {
			continue
		}
		for _, seg := range commandSegments(line) {
			m := makeCommand.FindStringSubmatch(strings.TrimSpace(seg))
			if m == nil {
				continue
			}
			for _, f := range strings.Fields(m[1]) {
				switch {
				case strings.HasPrefix(f, "-"):
					return nil, fmt.Errorf("`make %s` passes %s, which this check cannot read", m[1], f)
				case strings.Contains(f, "="):
					// A variable override, not a target.
				default:
					out = append(out, f)
				}
			}
		}
	}
	return out, nil
}

// testedPackages lists, relative to the module root, every package matched by
// the patterns that holds at least one untagged test file.
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

// onlyTaggedTestPackages lists the packages that hold test files only among
// the files the default build excludes, which is how `go list` reports a
// build-tagged test.
func onlyTaggedTestPackages(t *testing.T) []string {
	t.Helper()
	cmd := exec.Command("go", "list", "-f",
		`{{if not (or .TestGoFiles .XTestGoFiles)}}{{range .IgnoredGoFiles}}{{$.ImportPath}} {{.}}{{"\n"}}{{end}}{{end}}`, "./...")
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list ignored files: %v", err)
	}
	seen := map[string]bool{}
	var pkgs []string
	for _, line := range strings.Split(string(out), "\n") {
		pkg, file, ok := strings.Cut(line, " ")
		if ok && strings.HasSuffix(file, "_test.go") && !seen[pkg] {
			seen[pkg] = true
			pkgs = append(pkgs, strings.TrimPrefix(pkg, "github.com/Quinyte/assayd/"))
		}
	}
	return pkgs
}

// resolvePatterns returns the tested packages the patterns match.
func resolvePatterns(t *testing.T, patterns []string) map[string]bool {
	t.Helper()
	reached := map[string]bool{}
	if len(patterns) == 0 {
		return reached
	}
	for _, pkg := range testedPackages(t, patterns...) {
		reached[pkg] = true
	}
	return reached
}

func readRepoFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot, path))
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func sameSet(a, b []string) bool {
	x, y := append([]string(nil), a...), append([]string(nil), b...)
	sort.Strings(x)
	sort.Strings(y)
	return strings.Join(x, "\x00") == strings.Join(y, "\x00")
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
