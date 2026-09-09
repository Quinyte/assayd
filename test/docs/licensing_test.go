// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package docs

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const repoRoot = "../.."

// Every tracked file must carry an SPDX identifier or be covered by REUSE.toml.
//
// The coverage was established by hand when the headers landed, and a coverage
// claim nothing enforces is the shape this project keeps finding: the next file
// added carries no header, nobody notices, and the repository quietly stops
// being machine-readable at exactly the moment a foundation or a downstream
// consumer scans it. This is the check that was owed.
//
// The Linux Foundation's guidance asks for per-file identifiers rather than only
// a top-level LICENSE, because a scanner is materially more accurate against an
// identifier than against pasted licence text.
func TestEveryTrackedFileIsLicensed(t *testing.T) {
	files := trackedFiles(t)
	if len(files) < 50 {
		t.Fatalf("only %d tracked files found; the listing is broken and this gate would "+
			"pass by having nothing to check", len(files))
	}
	patterns := reusePatterns(t)
	if len(patterns) == 0 {
		t.Fatal("REUSE.toml declared no paths; every file would have to carry its own header")
	}

	var uncovered []string
	for _, f := range files {
		if hasSPDX(t, f) || coveredByREUSE(f, patterns) {
			continue
		}
		uncovered = append(uncovered, f)
	}
	if len(uncovered) > 0 {
		t.Errorf("%d tracked file(s) carry no SPDX identifier and are not covered by "+
			"REUSE.toml:\n  %s\n\nAdd a header, or a REUSE.toml annotation if the file "+
			"cannot carry one — a Helm template's comments are rendered into the user's "+
			"manifests, and config/ is rewritten wholesale by `make manifests`.",
			len(uncovered), strings.Join(uncovered, "\n  "))
	}
}

// The generated deepcopy must get its header from the GENERATOR, not from an
// edit. controller-gen rewrites that file completely, so a header committed into
// it and not into the Makefile's object:headerFile has a half-life of one
// `make generate` — and would then be caught by the test above only after
// someone regenerated and committed, which is too late to be useful.
func TestGeneratedCodeGetsItsHeaderFromTheGenerator(t *testing.T) {
	mk, err := os.ReadFile(filepath.Join(repoRoot, "Makefile"))
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}
	if !strings.Contains(string(mk), "object:headerFile=hack/boilerplate.go.txt") {
		t.Error("the generate rule does not pass object:headerFile, so `make generate` " +
			"will strip the SPDX header out of zz_generated.deepcopy.go")
	}
	hdr, err := os.ReadFile(filepath.Join(repoRoot, "hack/boilerplate.go.txt"))
	if err != nil {
		t.Fatalf("the generate rule names hack/boilerplate.go.txt and it is not there: %v", err)
	}
	if !strings.Contains(string(hdr), "SPDX-License-Identifier") {
		t.Error("hack/boilerplate.go.txt carries no SPDX identifier, so generated files " +
			"would be emitted unlicensed")
	}
}

// Build constraints survived the header insertion.
//
// **Two rules, and only one of them is Go's.** Go honours a `//go:build` line
// "preceded only by blank lines and other line comments", so a licence header
// above it is legal and the constraint still binds — measured, by putting the
// header above `test/conformance`'s constraint and watching Go still exclude the
// file untagged and include it tagged. An earlier version of this test claimed
// otherwise in its failure message, which would have sent someone chasing a
// non-bug.
//
// What IS Go's rule is the blank line AFTER the constraint: without it the line
// is package documentation, the constraint does not bind, and a file meant to be
// excluded is silently compiled into every build. `test/conformance` is tagged
// `cluster` so it does not run without one, and it would then fail for the wrong
// reason in every CI run.
//
// The line-1 check is kept as house style, because one placement is easier to
// scan than two — it is a convention, not a correctness claim, and it says so.
func TestBuildConstraintsSurvivedTheHeaders(t *testing.T) {
	for _, f := range trackedFiles(t) {
		if !strings.HasSuffix(f, ".go") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(repoRoot, f))
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		lines := strings.Split(string(b), "\n")
		for i, line := range lines {
			if !strings.HasPrefix(line, "//go:build") {
				continue
			}
			if i != 0 {
				t.Errorf("%s: //go:build is on line %d. Go would still honour it there — it is "+
					"preceded only by comments — but this repository keeps constraints on "+
					"line 1 so there is one place to look. Convention, not correctness", f, i+1)
			}
			if i+1 >= len(lines) || strings.TrimSpace(lines[i+1]) != "" {
				t.Errorf("%s: //go:build is not followed by a blank line. THIS one is Go's "+
					"rule, not a convention: without the blank line the constraint is read "+
					"as package documentation, does not bind, and the file is compiled into "+
					"every build including ones meant to exclude it", f)
			}
			break
		}
	}
}

func trackedFiles(t *testing.T) []string {
	t.Helper()
	cmd := exec.Command("git", "ls-files")
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		t.Skipf("git ls-files unavailable, so file coverage cannot be checked: %v", err)
	}
	var files []string
	for _, f := range strings.Fields(string(out)) {
		files = append(files, f)
	}
	return files
}

func hasSPDX(t *testing.T, rel string) bool {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot, rel))
	if err != nil {
		return false
	}
	// Only the head: a licence identifier is a header, and matching anywhere in
	// the file would let this document — which names the string repeatedly —
	// vouch for itself.
	head := b
	if len(head) > 2048 {
		head = head[:2048]
	}
	return strings.Contains(string(head), "SPDX-License-Identifier")
}

// reusePatterns reads the path globs out of REUSE.toml. Parsed by hand rather
// than with a TOML library so the test tree gains no dependency for one file.
func reusePatterns(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot, "REUSE.toml"))
	if err != nil {
		t.Fatalf("REUSE.toml is missing; it is what covers the files that cannot carry a "+
			"header: %v", err)
	}
	var pats []string
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "path") || !strings.Contains(line, "=") {
			continue
		}
		_, list, _ := strings.Cut(line, "=")
		for _, part := range strings.Split(strings.Trim(strings.TrimSpace(list), "[]"), ",") {
			if p := strings.Trim(strings.TrimSpace(part), `"`); p != "" {
				pats = append(pats, p)
			}
		}
	}
	return pats
}

func coveredByREUSE(file string, patterns []string) bool {
	for _, p := range patterns {
		if dir, ok := strings.CutSuffix(p, "/**"); ok {
			if strings.HasPrefix(file, dir+"/") {
				return true
			}
			continue
		}
		if ok, _ := filepath.Match(p, file); ok {
			return true
		}
		// A bare "*.md" in REUSE.toml means root-level markdown; filepath.Match
		// does not cross separators, which is the behaviour wanted here.
		if ok, _ := filepath.Match(p, filepath.Base(file)); ok && !strings.Contains(file, "/") {
			return true
		}
	}
	return false
}
