// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package refgen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// crd-agent.md says which CRDs config/crd holds and that the chart ships only
// the Agent's. checkCRDs is what keeps that sentence true: the repository's own
// set passes, and adding a CRD the page does not account for, or ceasing to
// generate one it names, fails generation by file name.
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

// The page's scope paragraph is prose, and unrenderedCRDs is the list it must
// agree with: every kind named in the map appears in that paragraph, and the
// number of CRDs it says `make manifests` produces is the map's size plus the
// rendered Agent CRD. It reads the committed page, which `make verify` holds
// to a fresh generation.
func TestThePageNamesEveryUnrenderedCRD(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(repoRoot, "docs", "reference", "crd-agent.md"))
	if err != nil {
		t.Fatal(err)
	}
	page := string(b)
	start := strings.Index(page, "is the whole API surface the chart ships today.")
	end := strings.Index(page, "**Defaults are applied")
	if start < 0 || end < start {
		t.Fatal("crd-agent.md has no scope paragraph to check: the page and this test have parted company")
	}
	scope := page[start:end]

	for file, kind := range unrenderedCRDs {
		if !strings.Contains(scope, "`"+kind+"`") {
			t.Errorf("unrenderedCRDs names %s (%s), and the page's scope paragraph does not mention it", kind, file)
		}
	}
	words := map[int]string{1: "one", 2: "two", 3: "three", 4: "four", 5: "five"}
	want := words[len(unrenderedCRDs)+1]
	if want == "" || !strings.Contains(scope, "produces "+want+" CRDs") && !strings.Contains(scope, "produces "+want+"\nCRDs") {
		t.Errorf("config/crd holds %d CRDs by unrenderedCRDs, and the page's scope paragraph does not say "+
			"`make manifests` produces %q of them", len(unrenderedCRDs)+1, want)
	}
}
