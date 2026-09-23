// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package docs

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// No Markdown table in the docs tree is broken by its own layout.
//
// A GFM table ends at the first line that is not a row, so a blank line inside
// a multi-paragraph cell, or a row indented differently from the one above it,
// silently turns every later row into a paragraph of raw pipes. Design 02 A77
// shipped both: a blank line inside one §5 row, and a §12 table whose rows sat
// at three indents. `pandoc -f gfm` rendered the DELETE, name-window,
// owner-edit and not-ours rows of §5 — the rows a reader consults to learn
// what is NOT guaranteed — as `<p>| …`, and nothing noticed until the third
// review rendered it.
//
// The rule is structural, not a renderer: a row must either continue a row at
// the same indent, or be a header, which the delimiter row directly below it
// at the same indent marks. That is exactly the condition GFM uses to keep a
// table going, and the check was run against the broken revision of design 02
// before it was committed: it flagged the row after the blank line and every
// row at a stray indent, and nothing else in the tree.
func TestNoMarkdownTableIsBrokenByItsOwnLayout(t *testing.T) {
	var files []string
	err := filepath.WalkDir(docsRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(p, ".md") {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", docsRoot, err)
	}
	sort.Strings(files)
	if len(files) < 20 {
		t.Fatalf("found only %d Markdown files under %s; the walk is broken and this gate would "+
			"pass by having nothing to check", len(files), docsRoot)
	}

	var broken []string
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		for _, line := range orphanedTableRows(string(b)) {
			broken = append(broken, f+":"+line)
		}
	}
	if len(broken) > 0 {
		t.Errorf("%d table rows render as paragraphs of raw pipes, because the line above each "+
			"is neither a row at the same indent nor a header's delimiter. Remove the blank "+
			"line inside the cell (join its paragraphs with <br><br>) or match the indent of "+
			"the row above:\n  %s", len(broken), strings.Join(broken, "\n  "))
	}
}

// TestTheTableCheckCatchesBothBreaks holds the check to the two shapes that
// broke design 02, so it cannot pass by matching nothing.
func TestTheTableCheckCatchesBothBreaks(t *testing.T) {
	for _, tc := range []struct {
		name, doc string
		want      int
	}{
		{"a blank line inside a cell", "| a | b |\n|---|---|\n| x | first\n\n  second |\n| y | z |\n", 1},
		{"a row at another indent", "- item\n\n  | a | b |\n  |---|---|\n  | x | y |\n| z | w |\n", 1},
		{"a well-formed table in a list item", "- item\n\n  | a | b |\n  |---|---|\n  | x | y |\n", 0},
		{"two tables, one after the other", "| a |\n|---|\n| x |\n\n| b |\n|---|\n| y |\n", 0},
		{"pipes in a fenced block", "```\n| not | a table |\n\n| still not |\n```\n", 0},
	} {
		if got := len(orphanedTableRows(tc.doc)); got != tc.want {
			t.Errorf("%s: %d orphaned rows, want %d", tc.name, got, tc.want)
		}
	}
}

var tableDelimiter = regexp.MustCompile(`^\s*\|?\s*:?-{3,}:?\s*(\|\s*:?-{3,}:?\s*)*\|?\s*$`)

// orphanedTableRows returns the 1-based line numbers of rows that no table
// owns: neither a continuation of a row at the same indent nor a header.
func orphanedTableRows(doc string) []string {
	lines := strings.Split(doc, "\n")
	indent := func(s string) int { return len(s) - len(strings.TrimLeft(s, " \t")) }
	isRow := func(s string) bool { return strings.HasPrefix(strings.TrimLeft(s, " \t"), "|") }
	var out []string
	fenced := false
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimLeft(l, " \t"), "```") {
			fenced = !fenced
			continue
		}
		if fenced || !isRow(l) {
			continue
		}
		header := i+1 < len(lines) && tableDelimiter.MatchString(lines[i+1]) && indent(lines[i+1]) == indent(l)
		continues := i > 0 && isRow(lines[i-1]) && indent(lines[i-1]) == indent(l)
		if !header && !continues {
			out = append(out, strconv.Itoa(i+1))
		}
	}
	return out
}
