// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package docs

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// A design's Status line and its row in docs/designs/README.md agree about
// whether the human approved it.
//
// This exists because they did not, for weeks, in twenty-three designs. Every
// one of 04–15 and 17–27 opened with `**Status**: **approved** — critique PASS
// at r2`, while its README row said "critique PASS … awaiting user approval",
// and the human had approved none of them. The only human approvals on record
// are design 03's first slice (2026-09-12), design 16's first slice
// (2026-09-14) and design 03 amendment A84 (2026-09-23). AGENTS.md tells every
// reader to trust a design's own Status line over any summary, so the line that
// was wrong was the one a reader was told to believe. The human corrected it on
// 2026-09-23; this test is what keeps the two from drifting apart again.
//
// # What it compares
//
// Each side is reduced to one of three claims (see approvalClaim):
//
//   - approvedWhole: the design, unqualified, is approved.
//   - approvedPart: only part of it is — a first slice, or one amendment.
//   - notApproved: no affirmative approval at all.
//
// The two sides must reduce to the same claim. So a Status line that says
// "approved" beside a row that says "awaiting user approval" fails, and so
// does the reverse; and a whole-design claim on one side cannot hide behind a
// first-slice claim on the other.
//
// # What it does not prove
//
// It is a lexical reading, not a semantic one. It finds the word "approved" (or
// "approves") used affirmatively, and discounts it when a negation or a modal
// sits just before it ("not approved", "can be approved", "nothing is
// approved"), or when it is quoted ("this line said \"approved\""), which is
// how a correction mentions the claim it retracts. A green run means the two
// documents make the same KIND of claim. It does not mean either is true: both
// could say "approved" of a design the human never approved, and this test
// would pass. Whether an approval happened is recorded in an ADR amendment
// (ADR-0024 Amendment 1, ADR-0034 Amendment 4) or a design's amendment log,
// and nothing here reads those.
func TestADesignsStatusLineAndItsIndexRowAgreeAboutApproval(t *testing.T) {
	designs := designStatusLines(t)
	rows := indexRows(t)

	// Fail loudly rather than pass on nothing. The corpus has 27 designs; a
	// glob or a parser that silently matched fewer would compare fewer pairs
	// and report green.
	if len(designs) < 27 {
		t.Fatalf("read only %d design Status lines under %s/designs; there are at least 27, "+
			"so the reader is broken and this gate would pass by comparing nothing", len(designs), docsRoot)
	}
	var names []string
	for n := range designs {
		names = append(names, n)
	}
	sort.Strings(names)
	for n := range rows {
		if _, ok := designs[n]; !ok {
			t.Errorf("docs/designs/README.md has a row for design %s, and no %s-*.md carries a Status line", n, n)
		}
	}

	var disagree []string
	for _, n := range names {
		row, ok := rows[n]
		if !ok {
			t.Errorf("design %s has no row in docs/designs/README.md's table", n)
			continue
		}
		s, r := approvalClaim(designs[n]), approvalClaim(row)
		if s != r {
			disagree = append(disagree, fmt.Sprintf("design %s: its Status line says %s, its README row says %s", n, s, r))
		}
	}
	if len(disagree) > 0 {
		t.Errorf("%d designs' Status lines and README rows disagree about whether the human approved them. "+
			"A design's own Status line is the source of truth and the README summarises it; fix whichever "+
			"is wrong, and record a correction rather than deleting the old claim:\n  %s",
			len(disagree), strings.Join(disagree, "\n  "))
	}
}

// TestTheApprovalReaderTellsTheClaimsApart holds approvalClaim to the shapes the
// corpus actually uses, so it can pass neither by matching nothing nor by
// matching everything. Most cases are phrasings found in the corpus
// on or before 2026-09-23.
func TestTheApprovalReaderTellsTheClaimsApart(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		want       approval
	}{
		{"the false Status line", "**approved** — critique PASS at r2 (reviews/06-review.md) · ADR-0022", approvedWhole},
		{"approval with an uncritiqued amendment beside it", "**approved** · **A7 (2026-09-04) uncritiqued** — the fallback witness is withdrawn", approvedWhole},
		{"design 01's old README row", "**approved** (`01-…`, ADR-0017/0018, review PASS)", approvedWhole},
		{"the README's awaiting row", "r2 — critique **PASS** (`06-…`, `reviews/06-review.md`; awaiting user approval, ADR-0022)", notApproved},
		{"a plain negation", "critique pending — not approved", notApproved},
		{"negation with emphasis between", "**not** **approved** by the human", notApproved},
		{"a modal", "the first slice is the only part that can be approved", notApproved},
		{"nothing is approved", "Nothing is approved until the human approves it after a passing critique.", notApproved},
		{"a quoted retraction", `this line read "approved" until 2026-09-23`, notApproved},
		{"a curly-quoted retraction", "this line read “approved” until 2026-09-23", notApproved},
		{"a quoted word with no reporting verb", `the PASS was recorded as "approved" by mistake`, notApproved},
		{"a curly-quoted word with no reporting verb", "the PASS was recorded as “approved” by mistake", notApproved},
		{"a first slice", "Only §1.1's first slice is approved, by the human on 2026-09-14. The rest of this document is not approved", approvedPart},
		{"a slice approved in the README's order", "**First slice approved by the human on 2026-09-14** (ADR-0024 Amendment 1); **the rest of the design not approved**", approvedPart},
		{"one amendment", "A84 (2026-09-23) is a rule-7 correction, approved by the human on 2026-09-23", approvedPart},
		{"approves only", "design 03 approves only its first slice", approvedPart},
		{"a part beside a whole", "**approved** · first slice approved on 2026-09-12", approvedWhole},
		{"reported speech", "design 10's Status line says approved while the README says awaiting", notApproved},
		{"no mention at all", "revised r2 — independent re-critique (reviews/01-recritique.md)", notApproved},
	} {
		if got := approvalClaim(tc.text); got != tc.want {
			t.Errorf("%s: %q reads as %s, want %s", tc.name, tc.text, got, tc.want)
		}
	}
}

type approval int

const (
	notApproved approval = iota
	approvedPart
	approvedWhole
)

func (a approval) String() string {
	switch a {
	case approvedWhole:
		return "\"approved\" (the whole design)"
	case approvedPart:
		return "\"part of it approved\" (a slice or an amendment)"
	default:
		return "\"not approved\""
	}
}

var (
	// Straight and curly quoted spans. A quoted word is mentioned, not claimed.
	quotedSpan = regexp.MustCompile(`"[^"\n]*"|“[^”\n]*”`)
	// Clause boundaries: sentence ends, semicolons, the middle dot and the em
	// dash the Status lines use to separate their parts.
	clauseBreak = regexp.MustCompile(`\.\s|;|·|—`)
	approvedTok = regexp.MustCompile(`\bapprove[ds]\b`)
	// A part, not the whole: a slice, or a numbered amendment such as A84.
	partMarker = regexp.MustCompile(`\bslices?\b|\ba\d+(\.\d+)?\b`)
	// A word that, in the four before "approved", withdraws the claim: a
	// negation, a modal ("can be approved"), a condition, or reported speech.
	negation = map[string]bool{
		"not": true, "never": true, "nothing": true, "no": true, "none": true,
		"neither": true, "nor": true, "be": true, "until": true, "unless": true,
		"whether": true, "if": true, "awaiting": true, "pending": true,
		// Reported speech: "its Status line says approved" is a mention of
		// another document's claim, not this one's.
		"says": true, "said": true, "read": true, "reads": true, "opened": true,
	}
	wordTok = regexp.MustCompile(`[a-z0-9§.]+`)
)

// approvalClaim reduces a Status line or a README cell to the approval it
// asserts. Emphasis and code marks are removed first, so `**not** **approved**`
// reads as "not approved"; quoted spans are removed, so a correction quoting
// the claim it retracts asserts nothing. A whole-design claim anywhere
// outranks a partial one, because a design that is approved whole AND has an
// approved slice is approved whole.
func approvalClaim(text string) approval {
	s := strings.ToLower(text)
	s = strings.NewReplacer("*", "", "_", "", "`", "").Replace(s)
	s = quotedSpan.ReplaceAllString(s, " ")
	best := notApproved
	for _, clause := range clauseBreak.Split(s, -1) {
		for _, loc := range approvedTok.FindAllStringIndex(clause, -1) {
			before := wordTok.FindAllString(clause[:loc[0]], -1)
			if len(before) > 4 {
				before = before[len(before)-4:]
			}
			negated := false
			for _, w := range before {
				if negation[w] {
					negated = true
				}
			}
			if negated {
				continue
			}
			if partMarker.MatchString(clause) {
				if best < approvedPart {
					best = approvedPart
				}
				continue
			}
			best = approvedWhole
		}
	}
	return best
}

var (
	designFile = regexp.MustCompile(`^(\d{2})-.*\.md$`)
	statusLine = regexp.MustCompile(`^\s*-\s*\*\*Status\*\*\s*:\s*(.+)$`)
	indexRow   = regexp.MustCompile(`^\|\s*(\d{1,2})\s*\|`)
)

// designStatusLines returns each design's Status line, keyed by its two-digit
// number. The Status line is the first `- **Status**:` bullet in the file's
// head; a design without one is reported, not skipped, because a design this
// test cannot read is a design it silently stops comparing.
func designStatusLines(t *testing.T) map[string]string {
	t.Helper()
	dir := filepath.Join(docsRoot, "designs")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	out := map[string]string{}
	for _, e := range entries {
		m := designFile.FindStringSubmatch(e.Name())
		if e.IsDir() || m == nil {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		lines := strings.Split(string(b), "\n")
		if len(lines) > 20 {
			lines = lines[:20]
		}
		found := false
		for _, l := range lines {
			if sm := statusLine.FindStringSubmatch(l); sm != nil {
				if _, dup := out[m[1]]; dup {
					t.Errorf("two design files are numbered %s", m[1])
				}
				out[m[1]] = sm[1]
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s has no `- **Status**:` line in its first 20 lines", e.Name())
		}
	}
	return out
}

// indexRows returns the status cell — the last cell — of each numbered row of
// docs/designs/README.md's table, keyed by the design's two-digit number.
func indexRows(t *testing.T) map[string]string {
	t.Helper()
	p := filepath.Join(docsRoot, "designs", "README.md")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	out := map[string]string{}
	for _, l := range strings.Split(string(b), "\n") {
		m := indexRow.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		n, _ := strconv.Atoi(m[1])
		key := fmt.Sprintf("%02d", n)
		cells := strings.Split(strings.TrimSuffix(strings.TrimSpace(l), "|"), "|")
		if len(cells) < 6 {
			t.Errorf("README row for design %s has %d cells; the table has 5 columns", key, len(cells)-1)
			continue
		}
		if _, dup := out[key]; dup {
			t.Errorf("README has two rows for design %s", key)
		}
		// The status cell is everything after the fourth column: a cell may
		// itself contain a pipe inside code, so rejoin rather than take the last.
		out[key] = strings.Join(cells[5:], "|")
	}
	if len(out) < 27 {
		t.Fatalf("read only %d numbered rows from %s; there are at least 27", len(out), p)
	}
	return out
}
