// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package refgen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The CRD pages say which CRDs config/crd holds and that the chart ships them
// all. checkCRDs is what keeps that sentence true: the repository's own set
// passes, and adding a CRD no page accounts for, or ceasing to generate one a
// page renders, fails generation by file name.
func TestTheReferenceAccountsForEveryGeneratedCRD(t *testing.T) {
	if err := checkCRDs(repoRoot); err != nil {
		t.Fatalf("the repository's own config/crd fails the check: %v", err)
	}

	tree := func(files ...string) string {
		root := t.TempDir()
		dir := filepath.Join(root, "config", "crd")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			if err := os.WriteFile(filepath.Join(dir, f), []byte("---\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return root
	}

	for name, tc := range map[string]struct {
		files []string
		names string
	}{
		"a CRD the page does not account for": {
			[]string{"assayd.dev_agents.yaml", "assayd.dev_evalsuites.yaml", "assayd.dev_evalruns.yaml"},
			"assayd.dev_evalruns.yaml"},
		"a named CRD no longer generated": {
			[]string{"assayd.dev_agents.yaml"}, "assayd.dev_agents.yaml"},
		"the Agent CRD missing": {
			[]string{"assayd.dev_evalsuites.yaml"}, "assayd.dev_evalsuites.yaml"},
	} {
		err := checkCRDs(tree(tc.files...))
		if err == nil {
			t.Errorf("%s: generation proceeded, and the page would miscount the CRDs", name)
			continue
		}
		if !strings.Contains(err.Error(), tc.names) {
			t.Errorf("%s: the refusal does not name what config/crd holds: %v", name, err)
		}
	}
}

// Every page's scope paragraph is prose, and renderedCRDs is the list it must
// agree with: each page names every rendered kind, and says `make manifests`
// produces that many CRDs. It reads the committed pages, which `make verify`
// holds to a fresh generation.
func TestEveryPageNamesEveryRenderedCRD(t *testing.T) {
	words := map[int]string{1: "one", 2: "two", 3: "three", 4: "four", 5: "five"}
	want := words[len(renderedCRDs)]
	for _, p := range renderedCRDs {
		b, err := os.ReadFile(filepath.Join(repoRoot, "docs", "reference", p.page))
		if err != nil {
			t.Fatal(err)
		}
		page := string(b)
		start := strings.Index(page, "are the whole API surface the chart ships today.")
		end := strings.Index(page, "**Defaults are applied")
		if start < 0 || end < start {
			t.Fatalf("%s has no scope paragraph to check: the page and this test have parted company", p.page)
		}
		scope := page[start:end]
		for _, c := range renderedCRDs {
			if !strings.Contains(scope, "`"+c.kind+"`") || !strings.Contains(scope, c.page) {
				t.Errorf("%s's scope paragraph does not name %s and its page", p.page, c.kind)
			}
		}
		if want == "" || !strings.Contains(strings.Join(strings.Fields(scope), " "), "produces "+want+" CRDs") {
			t.Errorf("config/crd holds %d CRDs, and %s's scope paragraph does not say `make manifests` "+
				"produces %q of them", len(renderedCRDs), p.page, want)
		}
	}
}
