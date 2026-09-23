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

// TestTheTableCheckCatchesBothBreaks holds the check to the shapes that broke
// design 02, and to the legitimate shapes it must not flag, so it can pass
// neither by matching nothing nor by matching everything.
func TestTheTableCheckCatchesBothBreaks(t *testing.T) {
	for _, tc := range []struct {
		name, doc string
		want      int
	}{
		{"a blank line inside a cell", "| a | b |\n|---|---|\n| x | first\n\n  second |\n| y | z |\n", 2},
		{"a blank line inside the LAST row", "| a | b |\n|---|---|\n| x | first\n\n  second |\n\nprose\n", 1},
		{"a row at another indent", "- item\n\n  | a | b |\n  |---|---|\n  | x | y |\n| z | w |\n", 1},
		{"a well-formed table in a list item", "- item\n\n  | a | b |\n  |---|---|\n  | x | y |\n", 0},
		{"two tables, one after the other", "| a |\n|---|\n| x |\n\n| b |\n|---|\n| y |\n", 0},
		{"pipes in a backtick fence", "```\n| not | a table |\n\n| still not |\n```\n", 0},
		{"pipes in a tilde fence", "~~~\n| not | a table |\n\n| still not |\n~~~\n", 0},
		{"a top-level row indented three spaces", "| a | b |\n|---|---|\n| x | y |\n   | z | w |\n", 0},
		{"prose after a table", "| a |\n|---|\n| x |\n\nA sentence, with no pipe.\n", 0},
	} {
		if got := len(orphanedTableRows(tc.doc)); got != tc.want {
			t.Errorf("%s: %d orphaned rows, want %d", tc.name, got, tc.want)
		}
	}
}

var tableDelimiter = regexp.MustCompile(`^\s*\|?\s*:?-{3,}:?\s*(\|\s*:?-{3,}:?\s*)*\|?\s*$`)

// orphanedTableRows returns the 1-based line numbers of lines that GFM would
// render as raw pipes: a row that no table owns — neither a continuation of a
// row at the same indent nor a header — and the continuation of a cell that a
// blank line cut off, which is how a break in a table's LAST row shows,
// since no row follows it to be orphaned.
//
// A blank line inside a cell orphans two lines, the cut-off continuation and
// the row after it, and both are reported.
//
// Indents are compared against the table's HEADER. A table whose header is at
// column 0 is top-level, where GFM strips up to three leading spaces, so any
// row indented less than four spaces continues it. A table whose header is
// indented sits in a list item, whose indent is its container, so its rows
// must match that indent exactly — which is how design 02's §12 table broke.
func orphanedTableRows(doc string) []string {
	lines := strings.Split(doc, "\n")
	indent := func(s string) int { return len(s) - len(strings.TrimLeft(s, " \t")) }
	tableIndent := -1
	fits := func(s string) bool {
		if tableIndent == 0 {
			return indent(s) < 4
		}
		return indent(s) == tableIndent
	}
	trimmed := func(s string) string { return strings.TrimLeft(s, " \t") }
	isRow := func(s string) bool { return strings.HasPrefix(trimmed(s), "|") }
	var out []string
	fence := ""
	for i, l := range lines {
		if t := trimmed(l); fence == "" && (strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~")) {
			fence = t[:3]
			continue
		} else if fence != "" {
			if strings.HasPrefix(t, fence) {
				fence = ""
			}
			continue
		}
		if !isRow(l) {
			// A cell continued past a blank line: the line two above is a row,
			// the line above is blank, and this one carries on with a pipe.
			if i >= 2 && strings.TrimSpace(lines[i-1]) == "" && isRow(lines[i-2]) &&
				!strings.HasSuffix(strings.TrimSpace(lines[i-2]), "|") && strings.Contains(l, "|") {
				out = append(out, strconv.Itoa(i+1))
			}
			continue
		}
		header := i+1 < len(lines) && tableDelimiter.MatchString(lines[i+1]) && indent(lines[i+1]) == indent(l)
		if header {
			tableIndent = indent(l)
			continue
		}
		if !(i > 0 && isRow(lines[i-1]) && tableIndent >= 0 && fits(l)) {
			out = append(out, strconv.Itoa(i+1))
		}
	}
	return out
}
