// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package refgen

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// TestReference records where the test suites mention a reason.
//
// What this can and cannot claim matters, and the generated document says both.
// A reason NO test file mentions is definitely unpinned: no test in this
// repository can fail when it changes, so it is a string the operator can put
// on a user's object that nothing holds it to. That is the finding worth
// publishing, and it is exact.
//
// The converse is weaker. A mention is not an assertion: a test may name a
// reason in a comment, a table it never reads, or a helper that skips. So the
// column is "referenced by", never "pinned by", and the note says so. AGENTS.md
// rule 1 is the reason for the caution — nine tests in this repository once
// passed with their subject deleted.
type TestReference struct {
	Reason string
	Files  []string // repository-relative, sorted
}

// testTrees are the directories scanned. internal/ and api/ contribute their
// _test.go files; everything under test/ is a test.
var testTrees = []string{"internal", "api", "test"}

// excludedTrees are tests that name reasons without testing the operator.
//
// This package's own tests name a dozen reasons as fixtures — they assert that
// the GENERATOR resolves them, not that the operator sets them for the right
// state. Counting those as "referenced by a test" would credit six reasons to
// this file and shrink the unpinned list by writing the reference, which is the
// reference lying about itself. Found by the drift check on the pass that added
// those tests.
var excludedTrees = []string{filepath.Join("internal", "refgen")}

// ScanTestReferences finds, for every reason, the test files that mention it —
// either as the quoted string or as the Reason* identifier that carries it.
func ScanTestReferences(root string, v *Vocabulary) (map[string]TestReference, error) {
	identOf := map[string]string{}
	for _, rc := range v.ReasonConsts {
		identOf[rc.Value] = rc.Ident
	}

	var files []string
	for _, tree := range testTrees {
		dir := filepath.Join(root, tree)
		err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "testdata" {
					return fs.SkipDir
				}
				rel, relErr := filepath.Rel(root, p)
				if relErr == nil {
					for _, ex := range excludedTrees {
						if rel == ex {
							return fs.SkipDir
						}
					}
				}
				return nil
			}
			if !strings.HasSuffix(p, ".go") {
				return nil
			}
			// Under test/ every Go file is part of a suite (helpers included);
			// elsewhere only _test.go files are.
			if tree != "test" && !strings.HasSuffix(p, "_test.go") {
				return nil
			}
			files = append(files, p)
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}
	sort.Strings(files)

	out := map[string]TestReference{}
	for reason := range v.ReasonsByName {
		out[reason] = TestReference{Reason: reason}
	}

	// Word boundaries matter: without them AuthLockPending would count every
	// mention of AuthLock, and the unpinned list — the point of the exercise —
	// would come back empty and wrong.
	pats := map[string]*regexp.Regexp{}
	for reason := range out {
		alt := regexp.QuoteMeta(reason)
		if id := identOf[reason]; id != "" {
			alt += "|" + regexp.QuoteMeta(id)
		}
		pats[reason] = regexp.MustCompile(`\b(?:` + alt + `)\b`)
	}

	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		body := string(b)
		rel, err := filepath.Rel(root, f)
		if err != nil {
			rel = filepath.Base(f)
		}
		rel = filepath.ToSlash(rel)
		for reason, re := range pats {
			if re.MatchString(body) {
				tr := out[reason]
				tr.Files = append(tr.Files, rel)
				out[reason] = tr
			}
		}
	}
	for r, tr := range out {
		sort.Strings(tr.Files)
		out[r] = tr
	}
	return out, nil
}
