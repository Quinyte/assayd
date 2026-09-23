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
// one of 04–15 and 17–27 opened with `**Status**: **approved** — critique
// PASS`, while its README row claimed no approval — twenty said "awaiting user
// approval", and rows 21, 26 and 27 named a precondition owed before their ADR
// instead — and the human had approved none of them. The only human approvals on record
// are design 03's first slice (2026-09-12), design 16's first slice
// (2026-09-14) and design 03 amendments A84 and A85 (both 2026-09-23). AGENTS.md tells every
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
// how a correction mentions the claim it retracts. A green run of THIS test
// means the two documents make the same KIND of claim. It does not mean either
// is true: both could say "approved" of a design the human never approved.
// TestNoDesignClaimsAnApprovalTheHumanDidNotGive closes that, by holding each
// side to humanApprovals, the human's decision written down as a table. What
// neither test reads is the decision's record — an ADR amendment
// (ADR-0024 Amendment 1, ADR-0034 Amendment 4) or a design's amendment log —
// so the table is only as true as the change that last edited it.
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
		{"amendments listed after a whole approval", "approved, with amendments A1–A6 below", approvedWhole},
		{"a negation in an earlier phrase", "critique PASS with no findings, approved by the human", approvedWhole},
		{"a quote inside a quote", `this line is read: "\"Approved\" no longer means buildable."`, notApproved},
		{"accepted by the human", "critique PASS, accepted by the human on 2026-09-30", approvedWhole},
		{"signed off", "signed off by the human on 2026-09-30", approvedWhole},
		{"sign-off given", "sign-off given 2026-09-30", approvedWhole},
		{"ratified", "ratified on 2026-09-30", approvedWhole},
		{"greenlit", "greenlit for implementation", approvedWhole},
		{"approval granted", "Approval: granted", approvedWhole},
		{"a negated synonym", "not signed off", notApproved},
	} {
		if got := approvalClaim(tc.text); got != tc.want {
			t.Errorf("%s: %q reads as %s, want %s", tc.name, tc.text, got, tc.want)
		}
	}
}

// TestTheHeadReaderReadsTheWholeBullet pins what designStatusLines hands the
// reader: the Status bullet with its wrapped lines, and an Approval bullet
// beside it. Both shapes passed silently before, because only the Status
// bullet's first line was read.
func TestTheHeadReaderReadsTheWholeBullet(t *testing.T) {
	for _, tc := range []struct {
		name, doc string
		want      approval
	}{
		{"a claim wrapped onto the next line", "# 10\n\n- **Status**: critique PASS at r2,\n  approved by the human\n- **ADRs**: 0022\n", approvedWhole},
		{"a separate Approval bullet", "# 10\n\n- **Status**: critique PASS at r2\n- **Approval**: granted by the human\n", approvedWhole},
		{"the next bullet is not the Status's", "# 10\n\n- **Status**: critique PASS at r2\n- **Owner**: approved by nobody in particular\n", notApproved},
		{"a blank line ends the bullet", "# 10\n\n- **Status**: critique PASS at r2\n\napproved elsewhere\n", notApproved},
	} {
		head, ok := headBullets(tc.doc)
		if !ok {
			t.Errorf("%s: no Status bullet found", tc.name)
			continue
		}
		if got := approvalClaim(head); got != tc.want {
			t.Errorf("%s: head %q reads as %s, want %s", tc.name, head, got, tc.want)
		}
	}
}

// humanApprovals is the human's decision, not a reading of the documents: the
// approval each design may claim. Designs 03 and 16 are approved in part —
// 03's first slice (ADR-0034 Amendment 4, 2026-09-12) and its amendments A84
// and A85 (both 2026-09-23, recorded in design 03's §11, each with no ADR
// amendment because neither makes ADR-0034 false), and 16's first slice (ADR-0024 Amendment 1, 2026-09-14). Every
// other design number, including one that does not exist yet, is not approved.
//
// Changing this table is recording a human decision. Do it only in the change
// that records that decision as an ADR amendment, and cite it here.
var humanApprovals = map[string]approval{
	"03": approvedPart,
	"16": approvedPart,
}

// TestNoDesignClaimsAnApprovalTheHumanDidNotGive pins the decision itself.
//
// Agreement between a Status line and its README row is not enough: setting
// both to "approved" agrees, and is exactly the drift the human corrected on
// 2026-09-23. So each side is held to humanApprovals as well. A new design
// that claims approval on both sides fails here, not silently.
func TestNoDesignClaimsAnApprovalTheHumanDidNotGive(t *testing.T) {
	designs := designStatusLines(t)
	rows := indexRows(t)
	if len(designs) < 27 {
		t.Fatalf("read only %d design Status lines; there are at least 27, so this gate would pass by reading nothing", len(designs))
	}
	var wrong []string
	check := func(n, where, text string) {
		want := humanApprovals[n] // absent: notApproved
		if got := approvalClaim(text); got != want {
			wrong = append(wrong, fmt.Sprintf("design %s: its %s says %s; the human's decision is %s", n, where, got, want))
		}
	}
	for n, text := range designs {
		check(n, "Status line", text)
	}
	for n, text := range rows {
		check(n, "README row", text)
	}
	for n := range humanApprovals {
		if _, ok := designs[n]; !ok {
			wrong = append(wrong, fmt.Sprintf("humanApprovals names design %s, and no such design has a Status line", n))
		}
	}
	sort.Strings(wrong)
	if len(wrong) > 0 {
		t.Errorf("%d claims of approval differ from what the human decided. The only approvals the human has "+
			"given are design 03's first slice, A84 and A85, and design 16's first slice. Correct the document. "+
			"Changing humanApprovals instead requires a human decision, recorded as an ADR amendment in the "+
			"same change:\n  %s", len(wrong), strings.Join(wrong, "\n  "))
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
	// An affirmative approval, in the words this corpus has used for one or
	// might: "approved", and the synonyms a reviewer showed would otherwise
	// slip past as "not approved".
	approvedTok = regexp.MustCompile(`\bapprove[ds]\b|\baccepted by the human\b|\bsigned[- ]off\b|\bsign-off (given|granted)\b|\bratified\b|\bgreen-?lit\b|\bapproval:? (granted|given)\b`)
	// A part, not the whole: a slice, or a numbered amendment such as A84.
	partMarker = regexp.MustCompile(`\bslices?\b|\ba\d+(\.\d+)?\b`)
	// A part marker directly after the approval ("approved the first slice",
	// "approved A84", "approves only its first slice"), with no comma between:
	// "approved, with amendments A1–A6" approves the whole design.
	partAfter = regexp.MustCompile(`^[^,:]{0,25}?(\bslices?\b|\ba\d+(\.\d+)?\b)`)
	// Where the words that can negate an approval stop: a comma or a colon
	// ends the phrase, so "critique PASS with no findings, approved by the
	// human" is an approval, not a negated one.
	phraseBreak = regexp.MustCompile(`[,:]`)
	// A word that, in the three before the approval and in its own phrase,
	// withdraws the claim: a negation, a modal ("can be approved"), a
	// condition, or reported speech.
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
// the claim it retracts asserts nothing. An approval is partial when a part
// marker is its subject — before it in the clause — or its object, directly
// after it. A whole-design claim anywhere outranks a partial one, because a
// design that is approved whole AND has an approved slice is approved whole.
//
// It is a reader, and it misses shapes nobody wrote down. That matters less
// than it would alone: TestNoDesignClaimsAnApprovalTheHumanDidNotGive turns
// every claim the reader DOES recognise into a failure unless the human's
// table allows it. What still passes is a phrasing the reader does not
// recognise as an approval at all.
func approvalClaim(text string) approval {
	s := strings.ToLower(text)
	// A backslash-escaped quote is a quote inside a quote; it must not pair
	// with the outer ones, or the word it quotes escapes as a claim.
	s = strings.NewReplacer("*", "", "_", "", "`", "", `\"`, "'").Replace(s)
	s = quotedSpan.ReplaceAllString(s, " ")
	best := notApproved
	for _, clause := range clauseBreak.Split(s, -1) {
		for _, loc := range approvedTok.FindAllStringIndex(clause, -1) {
			head := clause[:loc[0]]
			if b := phraseBreak.FindAllStringIndex(head, -1); b != nil {
				head = head[b[len(b)-1][1]:]
			}
			before := wordTok.FindAllString(head, -1)
			if len(before) > 3 {
				before = before[len(before)-3:]
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
			if partMarker.MatchString(clause[:loc[0]]) || partAfter.MatchString(clause[loc[1]:]) {
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
	headBullet = regexp.MustCompile(`^\s*-\s*\*\*(Status|Approval)\*\*\s*:\s*(.+)$`)
	newBlock   = regexp.MustCompile(`^\s*([-*+]|\d+[.)])\s|^\s*#|^\s*\|`)
	indexRow   = regexp.MustCompile(`^\|\s*(\d{1,2})\s*\|`)
)

// designStatusLines returns each design's approval-bearing head, keyed by its
// two-digit number: the `- **Status**:` bullet, joined with its continuation
// lines, and any `- **Approval**:` bullet beside it, read the same way. Only
// the first 20 lines are the head. A design without a Status bullet is
// reported, not skipped, because a design this test cannot read is a design
// it silently stops comparing.
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
		if _, dup := out[m[1]]; dup {
			t.Errorf("two design files are numbered %s", m[1])
		}
		head, found := headBullets(string(b))
		if !found {
			t.Errorf("%s has no `- **Status**:` bullet in its first 20 lines", e.Name())
			continue
		}
		out[m[1]] = head
	}
	return out
}

// headBullets joins the Status and Approval bullets of a document's first 20
// lines, each with the lines that continue it: a Markdown list item runs on
// until a blank line or the next block, so a claim wrapped onto the second
// line of the bullet is still the bullet's claim.
func headBullets(doc string) (string, bool) {
	lines := strings.Split(doc, "\n")
	if len(lines) > 20 {
		lines = lines[:20]
	}
	var parts []string
	status := false
	for i := 0; i < len(lines); i++ {
		m := headBullet.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		if m[1] == "Status" {
			status = true
		}
		text := []string{m[2]}
		if m[1] == "Approval" {
			// Keep the label: "Approval: granted" is the claim, "granted" is not.
			text[0] = "Approval: " + m[2]
		}
		for i+1 < len(lines) && strings.TrimSpace(lines[i+1]) != "" && !newBlock.MatchString(lines[i+1]) {
			i++
			text = append(text, strings.TrimSpace(lines[i]))
		}
		parts = append(parts, strings.Join(text, " "))
	}
	// Each bullet is its own clause: a claim cannot run from one into the next.
	return strings.Join(parts, " · "), status
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
