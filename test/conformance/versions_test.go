// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package conformance

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// defaultOf reads a shell default of the form `NAME="${NAME:-value}"`, at the
// start of a line, from a script in hack/.
func defaultOf(t *testing.T, script, name string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", "hack", script))
	if err != nil {
		t.Fatalf("read hack/%s: %v", script, err)
	}
	m := regexp.MustCompile(`(?m)^` + name + `="\$\{` + name + `:-([^}]+)\}"`).FindSubmatch(body)
	if m == nil {
		t.Fatalf("hack/%s sets no default for %s", script, name)
	}
	return string(m[1])
}

// TestTheSliceCasesRunTheE2EsAgentgatewayRelease holds the first slice's
// cluster cases to the agentgateway release the operator's e2e runs. Design
// 03 A74 says they are re-measured at every upgrade; this is what makes that
// true, because an upgrade of hack/e2e.sh alone now fails `make test` until
// hack/conformance-cluster.sh moves with it, and the slice cases run again.
func TestTheSliceCasesRunTheE2EsAgentgatewayRelease(t *testing.T) {
	slice := defaultOf(t, "conformance-cluster.sh", "SLICE_AGW_VERSION")
	e2e := defaultOf(t, "e2e.sh", "AGW_VERSION")
	if slice != e2e {
		t.Errorf("hack/e2e.sh runs agentgateway %s, and hack/conformance-cluster.sh measures the "+
			"first slice's cases against %s. The cases pin what the operator's transactions rest "+
			"on (design 03 §8.1), so they must be re-measured at the release the e2e runs: set "+
			"SLICE_AGW_VERSION to %s and run `make conformance-cluster`", e2e, slice, e2e)
	}
}
