// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package docs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stateParagraphs returns the part of an entry document that states the
// project's phase and what is and is not true of the system: from the Phase
// line to the first "## " heading after it. It returns "" when either end is
// missing.
func stateParagraphs(doc string) string {
	i := strings.Index(doc, "**Phase:")
	if i < 0 {
		return ""
	}
	j := strings.Index(doc[i:], "\n## ")
	if j < 0 {
		return ""
	}
	return doc[i : i+j]
}

// TestTheEntryDocumentsStateTheSameThings holds the state paragraphs of
// AGENTS.md and CLAUDE.md equal, byte for byte. Both files carry them, because
// a reader of either starts there; AGENTS.md is the model-neutral source, and
// a paragraph edited in one file and not the other would give two models two
// accounts of what the system does.
func TestTheEntryDocumentsStateTheSameThings(t *testing.T) {
	read := func(rel string) string {
		b, err := os.ReadFile(filepath.Join(repoRoot, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		return stateParagraphs(string(b))
	}
	agents, claude := read("AGENTS.md"), read("CLAUDE.md")
	if agents == "" || claude == "" {
		t.Fatalf("AGENTS.md or CLAUDE.md has no state paragraphs from a \"**Phase:\" line to a \"## \" heading; " +
			"this test reads them there")
	}
	if agents != claude {
		a, c := strings.Split(agents, "\n"), strings.Split(claude, "\n")
		for i := 0; i < len(a) || i < len(c); i++ {
			var x, y string
			if i < len(a) {
				x = a[i]
			}
			if i < len(c) {
				y = c[i]
			}
			if x != y {
				t.Errorf("the state paragraphs of AGENTS.md and CLAUDE.md differ at their line %d. Edit both the same way.\n"+
					"  AGENTS.md: %.200q\n  CLAUDE.md: %.200q", i+1, x, y)
				return
			}
		}
	}
}
