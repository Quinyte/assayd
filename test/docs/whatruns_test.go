// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package docs

import (
	"errors"
	"fmt"
	"go/build"
	"io"
	"io/fs"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
	"testing"
	"testing/fstest"

	"go.yaml.in/yaml/v3"
)

// Every directory holding a test file — outside testdata, vendor, `_` and `.`
// directories and nested modules — is named by a package pattern that
// `make test` runs and by one that CI runs; `test/e2e`, which needs a cluster,
// is exempt from `make test` alone.
//
// `test/responder` and `test/mcpserver` carried unit tests that neither ran
// (#129): `hack/e2e.sh` built the fixtures and ran none of their tests.
//
// This is a change detector over package lists, built on simple text rules;
// it is not a shell or Make parser. It reads the literal `./x` and `./x/...`
// arguments of `go test` lines in the recipes `make test` and ci.yml's `make`
// lines reach, and in the hack/ scripts those recipes name. It also checks that
// `make race` names what `make unit` names, and that no directory's tests are
// all excluded from a linux/amd64 build with cgo on, as CI builds them. Its
// rules: a `#` after the line's start, whitespace or a shell metacharacter
// ends a line; a recipe line that runs `go test` or a script with make's `-`
// prefix is refused; a target may have one recipe.
//
// Shell and Make syntax beyond those rules — quoting, metacharacters, `cd`,
// `||`, pipes, variables, recursive make, `.IGNORE`, `-i`/`-k` — is not
// modelled, and neither are test flags, environment variables or CI
// conditions. Changes to the Makefile, ci.yml and hack/ scripts are reviewed
// by hand. `test/conformance`'s `cluster` files run only in
// `make conformance-cluster`, which CI does not run.
func TestEveryTestedPackageRunsInMakeTestAndCI(t *testing.T) {
	problems, err := inventory(os.DirFS(repoRoot), exemptions{
		notInMakeTest: map[string]string{
			"test/e2e": "needs a real cluster; `make e2e` runs it through hack/e2e.sh",
		},
		allExcluded: map[string]string{},
	}, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range problems {
		t.Error(p)
	}
}

// The check above is held here to synthetic trees, so each of its branches has
// a row that fails without it.
func TestTheWhatRunsCheckReadsWhatItSays(t *testing.T) {
	const (
		unitAndRace = "test: unit ## the gate\nunit:\n\tgo test ./internal/... -count=1\nrace:\n\tgo test -race ./internal/...\n"
		ciUnit      = "jobs:\n  j:\n    steps:\n      - run: make unit\n"
		untagged    = "package x\n"
		tagged      = "//go:build cluster\n\npackage x\n"
	)
	tree := func(extra map[string]string) fstest.MapFS {
		fsys := fstest.MapFS{
			"Makefile":                 {Data: []byte(unitAndRace)},
			".github/workflows/ci.yml": {Data: []byte(ciUnit)},
			"internal/a/a_test.go":     {Data: []byte(untagged)},
		}
		for name, data := range extra {
			if data == "" {
				delete(fsys, name)
				continue
			}
			fsys[name] = &fstest.MapFile{Data: []byte(data)}
		}
		return fsys
	}
	none := exemptions{notInMakeTest: map[string]string{}, allExcluded: map[string]string{}}

	for _, tc := range []struct {
		name    string
		files   map[string]string
		ex      *exemptions
		want    []string // one substring per expected problem
		wantErr string
	}{{
		name: "a tree every gate names is clean",
	}, {
		name:  "a directory no pattern names is reported to both gates",
		files: map[string]string{"test/x/x_test.go": untagged},
		want:  []string{"test/x holds tests that no `go test` line `make test` reaches names", "test/x holds tests that no `go test` line a CI target reaches names"},
	}, {
		name:  "a directory only CI names is reported to make test",
		files: map[string]string{"test/x/x_test.go": untagged, "Makefile": unitAndRace + "x:\n\tgo test ./test/x/...\n", ".github/workflows/ci.yml": ciUnit + "      - run: make x\n"},
		want:  []string{"test/x holds tests that no `go test` line `make test` reaches"},
	}, {
		name:  "an exact pattern names its directory and not a child",
		files: map[string]string{"internal/a/b/b_test.go": untagged, "Makefile": "test: unit\nunit:\n\tgo test ./internal/a\nrace:\n\tgo test ./internal/a\n"},
		want:  []string{"internal/a/b holds tests that no `go test` line `make test`", "internal/a/b holds tests that no `go test` line a CI"},
	}, {
		name:  "./... names everything",
		files: map[string]string{"test/x/x_test.go": untagged, "Makefile": "test: unit\nunit:\n\tgo test ./...\nrace:\n\tgo test ./...\n"},
	}, {
		name:  "a backslash-continued go test line is joined",
		files: map[string]string{"test/x/x_test.go": untagged, "Makefile": "test: unit\nunit:\n\tgo test ./internal/... \\\n\t\t./test/x/...\nrace:\n\tgo test ./internal/... ./test/x/...\n"},
	}, {
		name:  "a comment line names nothing",
		files: map[string]string{"test/x/x_test.go": untagged, "Makefile": "test: unit\nunit:\n\t# go test ./test/x/...\n\tgo test ./internal/...\nrace:\n\tgo test ./internal/...\n"},
		want:  []string{"test/x holds tests that no `go test` line `make test`", "test/x holds tests that no `go test` line a CI"},
	}, {
		name:  "a trailing comment on a go test line names nothing",
		files: map[string]string{"test/x/x_test.go": untagged, "Makefile": "test: unit\nunit:\n\tgo test ./internal/... # ./test/x/...\nrace:\n\tgo test ./internal/... ./test/x/...\n"},
		want:  []string{"test/x holds tests that no `go test` line `make test`", "test/x holds tests that no `go test` line a CI"},
	}, {
		name:  "a trailing comment that names a script calls nothing",
		files: map[string]string{"Makefile": "test: unit\nunit:\n\tgo test ./internal/... # see ./hack/ghost.sh\nrace:\n\tgo test ./internal/...\n"},
	}, {
		name:  "a comment after a shell metacharacter names nothing",
		files: map[string]string{"test/x/x_test.go": untagged, "Makefile": "test: unit\nunit:\n\tgo test ./internal/... -count=1;# ./test/x/...\nrace:\n\tgo test ./internal/... ./test/x/...\n"},
		want:  []string{"test/x holds tests that no `go test` line `make test`", "test/x holds tests that no `go test` line a CI"},
	}, {
		name:    "a go test line with make's ignore-errors prefix is refused",
		files:   map[string]string{"Makefile": "test: unit\nunit:\n\t-go test ./internal/...\nrace:\n\tgo test ./internal/...\n"},
		wantErr: "make's `-` prefix",
	}, {
		name:    "the ignore-errors prefix after @ is refused",
		files:   map[string]string{"Makefile": "test: unit\nunit:\n\t@-go test ./internal/...\nrace:\n\tgo test ./internal/...\n"},
		wantErr: "make's `-` prefix",
	}, {
		name:    "the ignore-errors prefix after @ and a space is refused",
		files:   map[string]string{"Makefile": "test: unit\nunit:\n\t@ -go test ./internal/...\nrace:\n\tgo test ./internal/...\n"},
		wantErr: "make's `-` prefix",
	}, {
		name:    "a script call with the ignore-errors prefix is refused",
		files:   map[string]string{"Makefile": unitAndRace + "x:\n\t-./hack/x.sh\n", ".github/workflows/ci.yml": ciUnit + "      - run: make x\n", "hack/x.sh": "go test ./test/x/...\n"},
		wantErr: "make's `-` prefix",
	}, {
		name:  "a script line is not a recipe line",
		files: map[string]string{"test/x/x_test.go": untagged, "Makefile": unitAndRace + "x:\n\t./hack/x.sh\n", ".github/workflows/ci.yml": ciUnit + "      - run: make x\n", "hack/x.sh": "  - go test ./test/x/...\n"},
		want:  []string{"test/x holds tests that no `go test` line `make test` reaches"},
	}, {
		name:    "a target with two recipes is refused",
		files:   map[string]string{"Makefile": unitAndRace + "unit:\n\tgo test ./test/x/...\n"},
		wantErr: "more than one recipe",
	}, {
		name:  "a recursive pattern does not name a sibling sharing its prefix",
		files: map[string]string{"internal/ab/ab_test.go": untagged, "Makefile": "test: unit\nunit:\n\tgo test ./internal/a/...\nrace:\n\tgo test ./internal/a/...\n"},
		want:  []string{"internal/ab holds tests that no `go test` line `make test`", "internal/ab holds tests that no `go test` line a CI"},
	}, {
		name:  "a rule after a recipe ends it",
		files: map[string]string{"test/x/x_test.go": untagged, "Makefile": "test: unit\nunit:\n\tgo test ./internal/...\n$(TOOL):\n\tgo test ./test/x/...\nrace:\n\tgo test ./internal/...\n"},
		want:  []string{"test/x holds tests that no `go test` line `make test`", "test/x holds tests that no `go test` line a CI"},
	}, {
		name:  "a script a recipe names is read",
		files: map[string]string{"test/x/x_test.go": untagged, "Makefile": unitAndRace + "x:\n\tbash hack/x.sh\n", ".github/workflows/ci.yml": ciUnit + "      - run: make x\n", "hack/x.sh": "#!/bin/bash\nFOO=1 go test ./test/x/... -v\n"},
		want:  []string{"test/x holds tests that no `go test` line `make test` reaches"},
	}, {
		name:    "a script that cannot be read is refused",
		files:   map[string]string{"Makefile": unitAndRace + "x:\n\t./hack/ghost.sh\n", ".github/workflows/ci.yml": ciUnit + "      - run: make x\n"},
		wantErr: "hack/ghost.sh, which cannot be read",
	}, {
		name:  "a comment that names a script calls nothing",
		files: map[string]string{"Makefile": "test: unit\nunit:\n\t# see ./hack/ghost.sh\n\tgo test ./internal/...\nrace:\n\tgo test ./internal/...\n"},
	}, {
		name:    "a go test line with no literal pattern is refused",
		files:   map[string]string{"Makefile": "test: unit\nunit:\n\tgo test $(PKGS)\nrace:\n\tgo test ./internal/...\n"},
		wantErr: "names no literal package pattern",
	}, {
		name:  "a CI make that does not start a line runs nothing",
		files: map[string]string{".github/workflows/ci.yml": "jobs:\n  j:\n    steps:\n      - run: make unit\n      - run: echo make ghost\n"},
	}, {
		name:  "an indented make in a run block is read",
		files: map[string]string{".github/workflows/ci.yml": "jobs:\n  j:\n    steps:\n      - run: |\n          if true; then\n            make unit\n          fi\n"},
	}, {
		name:  "a comment on a CI make line names no target",
		files: map[string]string{".github/workflows/ci.yml": "jobs:\n  j:\n    steps:\n      - run: |\n          make unit # ghost\n"},
	}, {
		name:    "a workflow that invokes no make target is refused",
		files:   map[string]string{".github/workflows/ci.yml": "jobs:\n  j:\n    steps:\n      - run: echo make unit\n"},
		wantErr: "invokes no make target",
	}, {
		name:  "a CI variable override is not a target",
		files: map[string]string{".github/workflows/ci.yml": "jobs:\n  j:\n    steps:\n      - run: make COUNT=3 unit\n"},
	}, {
		name:    "a CI target with no recipe is refused",
		files:   map[string]string{".github/workflows/ci.yml": ciUnit + "      - run: make ghost\n"},
		wantErr: "`make ghost`, and the Makefile has no recipe",
	}, {
		name:    "a Makefile with no test rule is refused",
		files:   map[string]string{"Makefile": "unit:\n\tgo test ./internal/...\nrace:\n\tgo test ./internal/...\n"},
		wantErr: "no `test:` rule",
	}, {
		name:  "race must name what unit names",
		files: map[string]string{"test/x/x_test.go": untagged, "Makefile": "test: unit\nunit:\n\tgo test ./internal/... ./test/x/...\nrace:\n\tgo test -race ./internal/...\n"},
		want:  []string{"test/x is named by `make unit` but not by `make race`"},
	}, {
		name:    "a missing race target is refused",
		files:   map[string]string{"Makefile": "test: unit\nunit:\n\tgo test ./internal/...\n"},
		wantErr: "no `race` target",
	}, {
		name:    "a missing unit target is refused",
		files:   map[string]string{"Makefile": "test: docs\ndocs:\n\tgo test ./internal/...\nrace:\n\tgo test ./internal/...\n", ".github/workflows/ci.yml": "jobs:\n  j:\n    steps:\n      - run: make docs\n"},
		wantErr: "no `unit` target",
	}, {
		name:  "a directory whose tests are all tagged is reported",
		files: map[string]string{"internal/b/b_test.go": tagged},
		want:  []string{"internal/b holds only tests a linux/amd64 build excludes"},
	}, {
		name:  "one untagged file is enough",
		files: map[string]string{"internal/b/a_test.go": untagged, "internal/b/b_test.go": tagged},
	}, {
		name:  "a tag after the package clause is not a constraint",
		files: map[string]string{"internal/b/b_test.go": "package x\n\n//go:build cluster\n"},
	}, {
		name:  "a directory whose tests are all // +build constrained is reported",
		files: map[string]string{"internal/b/b_test.go": "// +build cluster\n\npackage x\n"},
		want:  []string{"internal/b holds only tests a linux/amd64 build excludes"},
	}, {
		name:  "a directory whose tests are all for another GOOS is reported",
		files: map[string]string{"internal/b/b_windows_test.go": untagged},
		want:  []string{"internal/b holds only tests a linux/amd64 build excludes"},
	}, {
		name:  "two excluded files leave the directory excluded",
		files: map[string]string{"internal/b/a_test.go": tagged, "internal/b/b_windows_test.go": untagged},
		want:  []string{"internal/b holds only tests a linux/amd64 build excludes"},
	}, {
		name:    "a malformed constraint is refused",
		files:   map[string]string{"internal/b/b_test.go": "//go:build (\n\npackage x\n"},
		wantErr: "match internal/b/b_test.go against linux/amd64",
	}, {
		name:  "a cgo-constrained test builds on CI's runners",
		files: map[string]string{"internal/b/b_test.go": "//go:build cgo\n\npackage x\n"},
	}, {
		name:  "a linux test file builds on CI's runners",
		files: map[string]string{"internal/b/b_linux_amd64_test.go": untagged},
	}, {
		name:  "a named all-tagged directory passes",
		files: map[string]string{"internal/b/b_test.go": tagged},
		ex:    &exemptions{notInMakeTest: map[string]string{}, allExcluded: map[string]string{"internal/b": "a reason"}},
	}, {
		name:  "a stale all-tagged entry is reported",
		files: map[string]string{"internal/b/b_test.go": untagged},
		ex:    &exemptions{notInMakeTest: map[string]string{}, allExcluded: map[string]string{"internal/b": "a reason"}},
		want:  []string{"internal/b is listed as holding only excluded tests"},
	}, {
		name:  "a make test exemption covers a CI-only directory",
		files: map[string]string{"test/e/e_test.go": untagged, "Makefile": unitAndRace + "e:\n\tgo test ./test/e/...\n", ".github/workflows/ci.yml": ciUnit + "      - run: make e\n"},
		ex:    &exemptions{notInMakeTest: map[string]string{"test/e": "a reason"}, allExcluded: map[string]string{}},
	}, {
		name: "an exemption make test does not need is reported",
		ex:   &exemptions{notInMakeTest: map[string]string{"internal/a": "a reason"}, allExcluded: map[string]string{}},
		want: []string{"internal/a is exempt from `make test`"},
	}, {
		name: "an exemption for a directory with no tests is reported",
		ex:   &exemptions{notInMakeTest: map[string]string{"test/gone": "a reason"}, allExcluded: map[string]string{}},
		want: []string{"test/gone is exempt from `make test` but holds no tests"},
	}, {
		name: "an all-tagged entry for a directory with no tests is reported",
		ex:   &exemptions{notInMakeTest: map[string]string{}, allExcluded: map[string]string{"test/gone": "a reason"}},
		want: []string{"test/gone is listed as holding only excluded tests but holds no tests"},
	}, {
		name: "testdata, vendor, _ and . directories and nested modules are not walked",
		files: map[string]string{
			"test/testdata/t_test.go": untagged, "vendor/v/v_test.go": untagged,
			"_old/o_test.go": untagged, ".tool/t_test.go": untagged,
			"sub/go.mod": "module sub\n", "sub/s_test.go": untagged,
		},
	}, {
		name:    "too few test directories is refused",
		files:   map[string]string{"internal/a/a_test.go": ""},
		wantErr: "only 0 directories",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			ex := none
			if tc.ex != nil {
				ex = *tc.ex
			}
			problems, err := inventory(tree(tc.files), ex, 1)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("got error %v, want one containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(problems) != len(tc.want) {
				t.Fatalf("got %d problems, want %d:\n  %s", len(problems), len(tc.want), strings.Join(problems, "\n  "))
			}
			for i, w := range tc.want {
				if !strings.Contains(problems[i], w) {
					t.Errorf("problem %d is %q, want it to contain %q", i, problems[i], w)
				}
			}
		})
	}
}

// exemptions are the named, reasoned exceptions to the check.
type exemptions struct {
	notInMakeTest map[string]string // directories only CI runs
	allExcluded   map[string]string // directories whose tests may all be excluded on linux/amd64
}

// inventory reads fsys — a module root holding the Makefile and ci.yml — and
// returns what the check finds wrong. An error means it could not read what
// it needs, and a check that read nothing would pass.
func inventory(fsys fs.FS, ex exemptions, minDirs int) ([]string, error) {
	dirs, err := testDirs(fsys)
	if err != nil {
		return nil, err
	}
	if len(dirs) < minDirs {
		return nil, fmt.Errorf("only %d directories hold a test; the walk is broken", len(dirs))
	}
	mk, err := fs.ReadFile(fsys, "Makefile")
	if err != nil {
		return nil, fmt.Errorf("read Makefile: %w", err)
	}
	recipes, err := parseRecipes(string(mk))
	if err != nil {
		return nil, err
	}
	prereqs, err := makeTestPrerequisites(string(mk))
	if err != nil {
		return nil, err
	}
	ci, err := fs.ReadFile(fsys, ".github/workflows/ci.yml")
	if err != nil {
		return nil, fmt.Errorf("read ci.yml: %w", err)
	}
	ciTargets, err := ciMakeTargets(ci)
	if err != nil {
		return nil, err
	}
	patternsOf := func(gate string, targets []string) ([]string, error) {
		var out []string
		for _, target := range targets {
			recipe, ok := recipes[target]
			if !ok {
				return nil, fmt.Errorf("%s calls `make %s`, and the Makefile has no recipe for it", gate, target)
			}
			p, err := recipePatterns(fsys, target, recipe)
			if err != nil {
				return nil, err
			}
			out = append(out, p...)
		}
		return out, nil
	}
	byMakeTest, err := patternsOf("`make test`", prereqs)
	if err != nil {
		return nil, err
	}
	byCI, err := patternsOf("ci.yml", ciTargets)
	if err != nil {
		return nil, err
	}
	for _, target := range []string{"unit", "race"} {
		if _, ok := recipes[target]; !ok {
			return nil, fmt.Errorf("the Makefile has no `%s` target, so the race parity check cannot run", target)
		}
	}
	byUnit, err := patternsOf("the race parity check", []string{"unit"})
	if err != nil {
		return nil, err
	}
	byRace, err := patternsOf("the race parity check", []string{"race"})
	if err != nil {
		return nil, err
	}

	var problems []string
	names := make([]string, 0, len(dirs))
	for dir := range dirs {
		names = append(names, dir)
	}
	sort.Strings(names)
	for _, dir := range names {
		_, exempt := ex.notInMakeTest[dir]
		inMakeTest := matchesAny(byMakeTest, dir)
		if !inMakeTest && !exempt {
			problems = append(problems, fmt.Sprintf("%s holds tests that no `go test` line `make test` reaches names; "+
				"add it to one in a prerequisite of `test` (%s)", dir, strings.Join(prereqs, " ")))
		}
		if inMakeTest && exempt {
			problems = append(problems, fmt.Sprintf("%s is exempt from `make test` (%q) but `make test` names it; "+
				"drop the exemption", dir, ex.notInMakeTest[dir]))
		}
		if !matchesAny(byCI, dir) {
			problems = append(problems, fmt.Sprintf("%s holds tests that no `go test` line a CI target reaches names; "+
				"add it to one in a target .github/workflows/ci.yml invokes", dir))
		}
		_, listed := ex.allExcluded[dir]
		if dirs[dir] && !listed {
			problems = append(problems, fmt.Sprintf("%s holds only tests a linux/amd64 build excludes, which no "+
				"gate here runs; give it one that builds there, or name it with a reason", dir))
		}
		if !dirs[dir] && listed {
			problems = append(problems, fmt.Sprintf("%s is listed as holding only excluded tests (%q) but holds one "+
				"a linux/amd64 build includes; drop the entry", dir, ex.allExcluded[dir]))
		}
		if matchesAny(byUnit, dir) && !matchesAny(byRace, dir) {
			problems = append(problems, fmt.Sprintf("%s is named by `make unit` but not by `make race`; the race "+
				"run is review evidence, so name it in both", dir))
		}
	}
	for _, list := range []struct {
		entries map[string]string
		what    string
	}{{ex.notInMakeTest, "is exempt from `make test`"}, {ex.allExcluded, "is listed as holding only excluded tests"}} {
		for dir := range list.entries {
			if _, ok := dirs[dir]; !ok {
				problems = append(problems, fmt.Sprintf("%s %s but holds no tests; drop the entry", dir, list.what))
			}
		}
	}
	return problems, nil
}

// testDirs maps each directory holding a `*_test.go` file to whether every
// such file in it is excluded from a linux/amd64 build — by `//go:build`,
// `// +build` or a GOOS/GOARCH filename suffix. It skips testdata, vendor, `_`
// and `.` directories, and nested modules.
func testDirs(fsys fs.FS) (map[string]bool, error) {
	dirs := map[string]bool{}
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p == "." {
				return nil
			}
			name := d.Name()
			if name == "testdata" || name == "vendor" || strings.HasPrefix(name, "_") || strings.HasPrefix(name, ".") {
				return fs.SkipDir
			}
			if _, err := fs.Stat(fsys, path.Join(p, "go.mod")); err == nil {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, "_test.go") {
			return nil
		}
		dir := path.Dir(p)
		match, err := ciBuild(fsys).MatchFile(dir, d.Name())
		if err != nil {
			return fmt.Errorf("match %s against linux/amd64: %w", p, err)
		}
		allExcluded, seen := dirs[dir]
		dirs[dir] = !match && (!seen || allExcluded)
		return nil
	})
	return dirs, err
}

// ciBuild is the build context CI's runners use, linux/amd64 with no extra
// tags, reading through fsys.
func ciBuild(fsys fs.FS) *build.Context {
	ctx := build.Default
	ctx.GOOS, ctx.GOARCH, ctx.BuildTags = "linux", "amd64", nil
	ctx.CgoEnabled = true // CI's ubuntu runners build with cgo on
	ctx.JoinPath = path.Join
	ctx.OpenFile = func(name string) (io.ReadCloser, error) { return fsys.Open(name) }
	return &ctx
}

var (
	makeTargetLine = regexp.MustCompile(`^([a-z][a-z0-9-]*):`)
	goTestLine     = regexp.MustCompile(`\bgo test\b(.*)`)
	shellComment   = regexp.MustCompile(`(^|[\s;&|()<>])#.*$`)
	makeIgnoreErr  = regexp.MustCompile(`^[\s@+]*-`)
	hackScript     = regexp.MustCompile(`hack/[A-Za-z0-9_-]+\.sh`)
	packagePattern = regexp.MustCompile(`^\./(\.\.\.|[A-Za-z0-9_-]+(/[A-Za-z0-9_-]+)*(/\.\.\.)?)$`)
	ciMakeLine     = regexp.MustCompile(`^make\s+(.*)$`)
	targetName     = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
)

// logicalLines splits text into lines, joining a line that ends in `\` to the
// next one.
func logicalLines(text string) []string {
	var out []string
	var cur strings.Builder
	for _, line := range strings.Split(text, "\n") {
		if rest, ok := strings.CutSuffix(line, "\\"); ok {
			cur.WriteString(rest + " ")
			continue
		}
		cur.WriteString(line)
		out = append(out, cur.String())
		cur.Reset()
	}
	return out
}

// parseRecipes maps each target to its tab-indented recipe lines. It refuses
// a target given more than one recipe, which make would resolve by warning and
// taking the last.
func parseRecipes(makefile string) (map[string]string, error) {
	recipes := map[string]string{}
	blocks := map[string]int{}
	current, inBlock := "", false
	for _, line := range logicalLines(makefile) {
		if m := makeTargetLine.FindStringSubmatch(line); m != nil {
			current, inBlock = m[1], false
			recipes[current] += ""
			continue
		}
		if strings.HasPrefix(line, "\t") && current != "" {
			if !inBlock {
				inBlock = true
				if blocks[current]++; blocks[current] > 1 {
					return nil, fmt.Errorf("the Makefile gives target %s more than one recipe", current)
				}
			}
			recipes[current] += line + "\n"
			continue
		}
		if strings.TrimSpace(line) != "" && !strings.HasPrefix(line, "#") {
			current = ""
		}
	}
	return recipes, nil
}

// makeTestPrerequisites returns the targets `make test` depends on.
func makeTestPrerequisites(makefile string) ([]string, error) {
	for _, line := range logicalLines(makefile) {
		if rest, ok := strings.CutPrefix(line, "test:"); ok {
			deps, _, _ := strings.Cut(rest, "##")
			if fields := strings.Fields(deps); len(fields) > 0 {
				return fields, nil
			}
		}
	}
	return nil, errors.New("the Makefile has no `test:` rule with prerequisites")
}

// recipePatterns returns the literal package patterns of the `go test` lines
// in a recipe and in the hack/ scripts it names. It refuses a recipe line that
// runs either with make's `-` prefix, which ignores its failure.
func recipePatterns(fsys fs.FS, target, recipe string) ([]string, error) {
	out, err := goTestPatterns(target, recipe)
	if err != nil {
		return nil, err
	}
	for _, line := range logicalLines(recipe) {
		line = uncommented(line)
		scripts := hackScript.FindAllString(line, -1)
		if (goTestLine.MatchString(line) || len(scripts) > 0) && makeIgnoreErr.MatchString(line) {
			return nil, fmt.Errorf("%s: a recipe line carries make's `-` prefix, so its failure would "+
				"not fail the gate: %q", target, strings.TrimSpace(line))
		}
		for _, script := range scripts {
			b, err := fs.ReadFile(fsys, script)
			if err != nil {
				return nil, fmt.Errorf("target %s calls %s, which cannot be read: %w", target, script, err)
			}
			p, err := goTestPatterns(script, string(b))
			if err != nil {
				return nil, err
			}
			out = append(out, p...)
		}
	}
	return out, nil
}

func goTestPatterns(where, text string) ([]string, error) {
	var out []string
	for _, line := range logicalLines(text) {
		line = uncommented(line)
		m := goTestLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		found := false
		for _, field := range strings.Fields(m[1]) {
			if packagePattern.MatchString(field) {
				out = append(out, field)
				found = true
			}
		}
		if !found {
			return nil, fmt.Errorf("%s: a `go test` line names no literal package pattern, so this check "+
				"cannot read what it runs: %q", where, strings.TrimSpace(line))
		}
	}
	return out, nil
}

// uncommented cuts a line at the first `#` that follows the line's start,
// whitespace or a shell metacharacter.
func uncommented(line string) string {
	return shellComment.ReplaceAllString(line, "")
}

// ciMakeTargets returns the targets of every line of a step's `run` that
// starts with `make`, skipping a variable override.
func ciMakeTargets(ciYAML []byte) ([]string, error) {
	var wf struct {
		Jobs map[string]struct {
			Steps []struct {
				Run string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(ciYAML, &wf); err != nil {
		return nil, fmt.Errorf("parse ci.yml: %w", err)
	}
	seen := map[string]bool{}
	var targets []string
	for _, job := range wf.Jobs {
		for _, step := range job.Steps {
			for _, line := range logicalLines(step.Run) {
				m := ciMakeLine.FindStringSubmatch(strings.TrimSpace(uncommented(line)))
				if m == nil {
					continue
				}
				for _, f := range strings.Fields(m[1]) {
					if targetName.MatchString(f) && !seen[f] {
						seen[f] = true
						targets = append(targets, f)
					}
				}
			}
		}
	}
	if len(targets) == 0 {
		return nil, errors.New("ci.yml invokes no make target; the parse is broken")
	}
	sort.Strings(targets)
	return targets, nil
}

// matchesAny reports whether a `go test` package pattern names dir.
func matchesAny(patterns []string, dir string) bool {
	for _, p := range patterns {
		p = strings.TrimPrefix(p, "./")
		switch base, recursive := strings.CutSuffix(p, "/..."); {
		case p == "...":
			return true
		case recursive && (dir == base || strings.HasPrefix(dir, base+"/")):
			return true
		case !recursive && dir == p:
			return true
		}
	}
	return false
}
