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
