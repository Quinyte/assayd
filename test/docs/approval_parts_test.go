// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package docs

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// grant is one part of a design the human approved: a design's first slice,
// or one numbered amendment to it, and the day the human approved it.
type grant struct {
	design string // two digits, "03"
	part   string // "first slice", or an amendment id such as "A84"
	date   string // YYYY-MM-DD
}

func (p grant) String() string {
	return fmt.Sprintf("design %s %s (%s)", p.design, p.part, p.date)
}

// humanApprovedParts is the human's decision, not a reading of the documents:
// every part of every design the human has approved, and the day. Designs 03
// and 16 are approved in part — 03's first slice (ADR-0034 Amendment 4,
// 2026-09-12) and its amendments A84 and A85 (both 2026-09-23, recorded in
// design 03's §11; neither adds an ADR-0034 amendment, on the reading that
// ADR-0034 is silent on what they change — a precedent, not a stated decision
// of the human's) and A86 (2026-09-24, with D6 answered K1, recorded as
// ADR-0034 Amendment 7) and A89 (2026-09-28, implementing D8's (R1); ADR-0034
// Amendment 7 carries an italic note and no new amendment), and 16's first
// slice (ADR-0024 Amendment 1, 2026-09-14). Every other design number,
// including one that does not exist yet, is not approved.
//
// Changing this table is recording a human decision. Do it only in the change
// that records that decision as an ADR amendment, and cite it here.
//
// It is the one place a new approval is recorded in this package: one row. The
// tests below then require the design's Status line and README row, the
// README's list, and the paragraph AGENTS.md and CLAUDE.md share, to name it.
var humanApprovedParts = []grant{
	{"03", "first slice", "2026-09-12"}, // ADR-0034 Amendment 4
	{"16", "first slice", "2026-09-14"}, // ADR-0024 Amendment 1
	{"03", "A84", "2026-09-23"},         // design 03 §11 A84; no ADR-0034 amendment (the precedent above)
	{"03", "A85", "2026-09-23"},         // design 03 §11 A85; no ADR-0034 amendment (the precedent above)
	{"03", "A86", "2026-09-24"},         // design 03 §11 A86, with D6 answered K1; ADR-0034 Amendment 7
	{"03", "A89", "2026-09-28"},         // design 03 §11 A89, implementing D8's (R1); a note in ADR-0034 Amendment 7
}

// humanApprovals reduces humanApprovedParts to the claim each design may make
// as a whole: a design with any approved part is approved in part. No design
// is approved whole. Every other design number is not approved.
var humanApprovals = func() map[string]approval {
	m := map[string]approval{}
	for _, p := range humanApprovedParts {
		m[p.design] = approvedPart
	}
	return m
}()

// approvalPointer is the one sentence every design that is not approved, and
// the template a design is copied from, carries in its Status line in place of
// a list of approvals. A list repeated in 23 Status lines, written on
// 2026-09-23, went stale in all 23 when A85, A86 and A89 were approved. The
// pointer cannot go stale: the list it names is held to humanApprovedParts.
const approvalPointer = "The human's approvals, by design and part, are listed in `docs/designs/README.md`."

// approvalListAnchor is the phrase the three canonical lists of approvals —
// docs/designs/README.md's, and the paragraph AGENTS.md and CLAUDE.md share —
// are written under. TestTheListsOfApprovalsNameEveryApprovedPart finds them by
// it; every other list is found by its structure.
const approvalListAnchor = "approvals the human has given"

// supersededApprovalLists are lists of approvals kept out of date on purpose,
// because the document is an ADR and an ADR is amended, never edited. Each
// names its file, the day its list was true, and the amendment that says so,
// which must exist as a heading and be cited in the ADR's Status line. A list
// in any other document is dated in place instead, with an "(as of
// YYYY-MM-DD …)" note in its own sentence; see TestNoDocumentCarriesAStaleListOfApprovals.
var supersededApprovalLists = []struct {
	file, amendment, asOf string
}{
	{"docs/decisions/0030-scope-reset-to-one-end-to-end-slice.md", "Amendment 2", "2026-09-23"},
}

func grantList(gs []grant) string {
	var s []string
	for _, g := range gs {
		s = append(s, g.String())
	}
	sort.Strings(s)
	return strings.Join(s, ", ")
}

var (
	// An amendment id, case-sensitive so that a review's file name
	// ("reviews/03-a89-critique-r2.md") is not read as one.
	amendmentID = regexp.MustCompile(`\bA\d+(?:\.\d+)?\b`)
	// A run of ids written as a list: "A84", "A84 and A85", "A84, A85 and A86".
	amendmentRun = regexp.MustCompile(`\bA\d+(?:\.\d+)?\b(?:(?:,\s*(?:and\s+)?|\s+and\s+)A\d+(?:\.\d+)?\b)*`)
	// An id the sentence rules out: "A90, not A88, was approved".
	negatedID = regexp.MustCompile(`\bnot\s+A\d+(?:\.\d+)?\b`)
	// The only phrasings read as a claim that the HUMAN approved something.
	// "approved on", "approved by a reviewer" and "the approved first slice"
	// are not: they do not say who approved.
	// One word may sit between "the human" and "approved" ("has", "had",
	// "also", "then"); "not" and "never" there withdraw the claim.
	humanApproval = regexp.MustCompile(`(?i)\bapproved,? by the human\b|\bthe human(?: (\w+))? approved\b|\bthe human['’]s approval(?: of)?\b`)
	// A bare approval by id, with no approver named: "A90 was approved on
	// DATE", "A20 approved on DATE". Read only in an approved design's own
	// Status line and README row, where an ADR's or a reviewer's approval
	// cannot be what it means.
	bareApproval = regexp.MustCompile(`(?i)\bapproved\b([^.;|]{0,40}?)\bon (\d{4}-\d{2}-\d{2})\b`)
	// An ADR as the subject of an approval: "ADR-0031 was approved by the
	// human on 2026-09-05" approves a decision record, not a part of a design.
	adrRef = regexp.MustCompile(`\bADR-\d{4}\b`)
	// The word just before the phrase that withdraws it: "not approved by the
	// human", "never approved by the human", "would be approved by the human".
	withdrawnBefore = regexp.MustCompile(`(?i)\b(?:not|never|be)\s*$`)
	// Sentence ends, semicolons and table-cell breaks: a claim's subject is
	// looked for in its own clause only.
	sentenceBreak = regexp.MustCompile(`\.\s|;|\|`)
	sliceWord     = regexp.MustCompile(`(?i)\bslice\b`)
	designRef     = regexp.MustCompile(`(?i)\bdesign (\d{2})\b`)
	isoDate       = regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}\b`)
	approvalWord  = regexp.MustCompile(`(?i)\bapprov`)
	approvalsWord = regexp.MustCompile(`(?i)\bapprovals\b`)
	listMarker    = regexp.MustCompile(`(?i)\bonly\b|\bapprovals\b`)
	asOfDate      = regexp.MustCompile(`(?i)\bas of (\d{4}-\d{2}-\d{2})\b`)
	// Sentence ends and table-cell breaks, without the semicolon: a list's
	// parts and its dates are often separated by one, as in "(both
	// 2026-09-23; A85 is recorded by PR #65)".
	listBreak = regexp.MustCompile(`\.\s|\|`)
)

// approvalProse strips what approvalClaim strips — emphasis, code marks and
// quoted spans — but keeps case, because an amendment id is read by its
// capital A.
func approvalProse(text string) string {
	s := strings.NewReplacer("*", "", "`", "", `\"`, "'").Replace(text)
	return quotedSpan.ReplaceAllString(s, " ")
}

// humanApprovalClaims returns every claim in a text that the human approved a
// part of a design on a day. It reads ONLY the phrasings humanApproval names —
// "approved by the human", "the human [one word] approved", "the human's
// approval of" — each with the first date after it in its clause. A clause
// whose subject is an ADR (adrSubject) is skipped. The subject is the ids
// between the phrase and the date ("the human approved A84 and A85 on"), or a
// slice there; failing both, the last run of ids before the phrase in the
// clause, every id in it ("A90 and A85, …, both approved by the human on"),
// or a slice there. An id after "not" is dropped. The design is a "design NN"
// in the same place the subject was found, else anywhere before the phrase in
// the clause, else def.
//
// What it does not read, stated because it is the limit of every test that
// uses it: a claim with no date after the phrase in its clause ("the human
// approved A84", "A84 was approved on 2026-09-23"), and any phrasing that does
// not say the human approved. A claim whose subject or design it cannot find
// ("the human approved it on 2026-09-24") is returned with part or design
// empty, and pinned holds it to the fields it does name.
func humanApprovalClaims(text, def string) []grant {
	var out []grant
	for _, clause := range sentenceBreak.Split(approvalProse(text), -1) {
		for _, loc := range humanApproval.FindAllStringSubmatchIndex(clause, -1) {
			head := clause[:loc[0]]
			if withdrawnBefore.MatchString(head) {
				continue
			}
			if loc[2] >= 0 && withdrawnWord[strings.ToLower(clause[loc[2]:loc[3]])] {
				continue // "the human never approved"
			}
			rest := clause[loc[1]:]
			d := isoDate.FindStringIndex(rest)
			if d == nil {
				continue // undated: not read
			}
			g := grant{design: def, date: rest[d[0]:d[1]]}
			gap := negatedID.ReplaceAllString(rest[:d[0]], " ")
			head = negatedID.ReplaceAllString(head, " ")
			var parts []string
			where := gap
			switch runs := amendmentRun.FindAllString(head, -1); {
			case adrSubject(gap, head):
				continue // an ADR was approved, not a part of a design
			case amendmentID.MatchString(gap):
				parts = amendmentID.FindAllString(gap, -1)
			case sliceWord.MatchString(gap):
				parts = []string{"first slice"}
			case runs != nil:
				parts, where = amendmentID.FindAllString(runs[len(runs)-1], -1), head
			case sliceWord.MatchString(head):
				parts, where = []string{"first slice"}, head
			default:
				parts, where = []string{""}, head
			}
			if m := designRef.FindAllStringSubmatch(where, -1); m != nil {
				g.design = m[len(m)-1][1]
			} else if m := designRef.FindAllStringSubmatch(head, -1); m != nil {
				g.design = m[len(m)-1][1]
			}
			for _, p := range parts {
				g.part = p
				out = append(out, g)
			}
		}
	}
	return out
}

// withdrawnWord is the word between "the human" and "approved" that
// withdraws the claim.
var withdrawnWord = map[string]bool{"not": true, "never": true}

// adrSubject reports whether an approval's subject is an ADR: one is named
// between the phrase and the date with no part beside it, or, failing a
// subject there, the last thing named before the phrase is an ADR rather than
// an amendment id or a slice.
func adrSubject(gap, head string) bool {
	if amendmentID.MatchString(gap) || sliceWord.MatchString(gap) {
		return false
	}
	if adrRef.MatchString(gap) {
		return true
	}
	last := func(re *regexp.Regexp) int {
		l := re.FindAllStringIndex(head, -1)
		if l == nil {
			return -1
		}
		return l[len(l)-1][0]
	}
	a := last(adrRef)
	return a >= 0 && a > last(amendmentID) && a > last(sliceWord)
}

// bareApprovalClaims returns every "<id> … approved on DATE" in a text, by
// amendment id only: the ids between "approved" and the date, else the last
// run of ids before it in its clause, every id in the run. A claim with no id
// is not returned. It names no approver, so it is read only where the
// approver can only be the human: an approved design's own Status line and
// README row (TestAPartlyApprovedDesignNamesTheApprovedParts).
func bareApprovalClaims(text, def string) []grant {
	var out []grant
	for _, clause := range sentenceBreak.Split(approvalProse(text), -1) {
		for _, m := range bareApproval.FindAllStringSubmatchIndex(clause, -1) {
			head := negatedID.ReplaceAllString(clause[:m[0]], " ")
			if withdrawnBefore.MatchString(clause[:m[0]]) {
				continue
			}
			gap := negatedID.ReplaceAllString(clause[m[2]:m[3]], " ")
			ids := amendmentID.FindAllString(gap, -1)
			if ids == nil {
				if runs := amendmentRun.FindAllString(head, -1); runs != nil {
					ids = amendmentID.FindAllString(runs[len(runs)-1], -1)
				}
			}
			g := grant{design: def, date: clause[m[4]:m[5]]}
			if d := designRef.FindAllStringSubmatch(head, -1); d != nil {
				g.design = d[len(d)-1][1]
			}
			for _, id := range ids {
				g.part = id
				out = append(out, g)
			}
		}
	}
	return out
}

// listedGrants reads a list of approvals written as prose — "design 03's
// first slice (2026-09-12), design 16's first slice (2026-09-14), design 03's
// amendments A84 and A85 (both 2026-09-23)" — by walking its tokens: each
// "design NN" sets the design, and each first slice or amendment id after it
// is paired with the next date. It reads tokens, not phrasing; what it
// requires is that each part is followed by its own date before the next
// part's. A part with no date after it keeps date "".
func listedGrants(text string) []grant {
	tok := regexp.MustCompile(`(?i)\bdesign (\d{2})\b|\bfirst slice\b|\bA\d+(?:\.\d+)?\b|\b\d{4}-\d{2}-\d{2}\b`)
	var out, pending []grant
	design := ""
	for _, m := range tok.FindAllStringSubmatch(approvalProse(text), -1) {
		switch {
		case m[1] != "":
			design = m[1]
		case isoDate.MatchString(m[0]):
			for i := range pending {
				pending[i].date = m[0]
			}
			out = append(out, pending...)
			pending = nil
		case strings.EqualFold(m[0], "first slice"):
			pending = append(pending, grant{design: design, part: "first slice"})
		case m[0][0] == 'A':
			pending = append(pending, grant{design: design, part: m[0]})
		}
	}
	return append(out, pending...)
}

// approvalLists returns each canonical list of approvals in a document: the
// paragraph that says approvalListAnchor, read from its start to the end of
// the sentence that says it, so the parts named in the sentences before it
// count.
func approvalLists(doc string) []string {
	var out []string
	for _, para := range strings.Split(doc, "\n\n") {
		if i := strings.Index(para, approvalListAnchor); i >= 0 {
			end := len(para)
			if e := sentenceBreak.FindStringIndex(para[i:]); e != nil {
				end = i + e[0]
			}
			out = append(out, para[:end])
		}
	}
	return out
}

// grantDiff reports, one line each, what got claims that want does not, and
// what want holds that got does not claim.
func grantDiff(where string, got, want []grant) []string {
	g, w := map[grant]bool{}, map[grant]bool{}
	for _, x := range got {
		g[x] = true
	}
	for _, x := range want {
		w[x] = true
	}
	var out []string
	for x := range g {
		if !w[x] {
			out = append(out, fmt.Sprintf("%s claims %s, which humanApprovedParts does not hold", where, describeClaim(x)))
		}
	}
	for x := range w {
		if !g[x] {
			out = append(out, fmt.Sprintf("%s does not name %s, which humanApprovedParts holds", where, x))
		}
	}
	sort.Strings(out)
	return out
}

func describeClaim(g grant) string {
	switch {
	case g.design == "":
		return fmt.Sprintf("an approval of %q on %q with no design named", g.part, g.date)
	case g.part == "":
		return fmt.Sprintf("an approval in design %s on %q with no slice or amendment id named", g.design, g.date)
	case g.date == "":
		return fmt.Sprintf("design %s %s with no date after it", g.design, g.part)
	}
	return g.String()
}

func grantsOf(design string) []grant {
	var out []grant
	for _, g := range humanApprovedParts {
		if g.design == design {
			out = append(out, g)
		}
	}
	return out
}

// pinned reports whether a claim matches a row of humanApprovedParts on every
// field it names. The date is always named.
func pinned(g grant) bool {
	for _, p := range humanApprovedParts {
		if p.date == g.date && (g.design == "" || g.design == p.design) && (g.part == "" || g.part == p.part) {
			return true
		}
	}
	return false
}

// approvedPartKey resolves a listed part to the (design, part) of the row it
// names, filling a missing design from the table when only one design has
// that part. It reports false for a part the human did not approve.
func approvedPartKey(g grant) (grant, bool) {
	var hit []grant
	for _, p := range humanApprovedParts {
		if p.part == g.part && (g.design == "" || g.design == p.design) {
			hit = append(hit, grant{design: p.design, part: p.part})
		}
	}
	if len(hit) != 1 {
		return grant{}, false
	}
	return hit[0], true
}

// corpusFile is one document the approval tests read, as a reader sees it.
type corpusFile struct {
	rel  string // relative to the repository root
	text string // HTML reduced by renderHTML; Markdown as written
	def  string // the design a claim defaults to: the file's number in docs/designs/, else ""
}

// approvalCorpus is every document an approval claim can be written in: the
// root README.md, AGENTS.md and CLAUDE.md, and every .md and .html under
// docs/ except docs/designs/reviews/, where a critique quotes and proposes
// text and asserts no approval.
func approvalCorpus(t *testing.T) []corpusFile {
	t.Helper()
	var out []corpusFile
	add := func(p string) {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		rel, _ := filepath.Rel(repoRoot, p)
		f := corpusFile{rel: filepath.ToSlash(rel), text: string(b)}
		if strings.HasSuffix(strings.ToLower(p), ".html") {
			f.text = renderHTML(f.text)
		}
		if m := designFile.FindStringSubmatch(filepath.Base(p)); m != nil && filepath.Dir(p) == filepath.Join(docsRoot, "designs") {
			f.def = m[1]
		}
		out = append(out, f)
	}
	for _, p := range []string{"README.md", "AGENTS.md", "CLAUDE.md"} {
		add(filepath.Join(repoRoot, p))
	}
	reviews := filepath.Join(docsRoot, "designs", "reviews")
	err := filepath.WalkDir(docsRoot, func(p string, d fs.DirEntry, err error) error {
		switch lower := strings.ToLower(p); {
		case err != nil:
			return err
		case d.IsDir() && p == reviews:
			return filepath.SkipDir
		case !d.IsDir() && (strings.HasSuffix(lower, ".md") || strings.HasSuffix(lower, ".html")):
			add(p)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", docsRoot, err)
	}
	return out
}

// TestEveryDesignFileIsNamedAsADesign keeps designStatusLines from skipping a
// design in silence: every Markdown file in docs/designs/ (any case of .md) is
// named NN-name.md or is one of designDirNonDesigns, and every directory is
// one of designDirSubdirs.
func TestEveryDesignFileIsNamedAsADesign(t *testing.T) {
	dir := filepath.Join(docsRoot, "designs")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	for _, e := range entries {
		switch {
		case e.IsDir() && !designDirSubdirs[e.Name()]:
			t.Errorf("docs/designs/%s/ is a directory no approval gate reads; a design filed there is one no "+
				"test compares. Move it up as NN-name.md, or add the directory to designDirSubdirs with the "+
				"reason it holds no design", e.Name())
		case !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".md") &&
			!designFile.MatchString(e.Name()) && !designDirNonDesigns[e.Name()]:
			t.Errorf("docs/designs/%s is not named NN-name.md, so no approval gate reads its Status line. "+
				"Name it as a design, or add it to designDirNonDesigns with the reason it is not one", e.Name())
		}
	}
}

// TestAPartlyApprovedDesignNamesTheApprovedParts pins WHICH parts. Design 03
// and design 16 are each "approved in part", and approvalClaim does not read
// which part: a Status line that dropped A86, or gained an amendment the human
// never approved, still reads as "part of it approved". So each design's Status
// line and its README row must claim exactly the parts humanApprovedParts
// gives that design, each on its day — no fewer and no more. Both readers
// apply here: humanApprovalClaims, and, for a design with an approved part,
// bareApprovalClaims, so "A20 approved on DATE" in design 16's row is read as
// a claim of design 16's even though it names no approver.
func TestAPartlyApprovedDesignNamesTheApprovedParts(t *testing.T) {
	seen := map[grant]bool{}
	for _, g := range humanApprovedParts {
		if seen[g] {
			t.Errorf("humanApprovedParts holds %s twice", g)
		}
		seen[g] = true
		if !designFile.MatchString(g.design+"-x.md") || !isoDate.MatchString(g.date) ||
			(g.part != "first slice" && !amendmentID.MatchString(g.part)) {
			t.Errorf("humanApprovedParts row %+v is not a two-digit design, a first slice or an amendment id, and a YYYY-MM-DD date", g)
		}
	}
	var wrong []string
	for where, texts := range map[string]map[string]string{"Status line": designStatusLines(t), "README row": indexRows(t)} {
		for n, text := range texts {
			var own []grant
			claims := humanApprovalClaims(text, n)
			if humanApprovals[n] != notApproved {
				claims = append(claims, bareApprovalClaims(text, n)...)
			}
			for _, g := range claims {
				if g.design == n && g.part != "" {
					own = append(own, g)
				}
			}
			wrong = append(wrong, grantDiff(fmt.Sprintf("design %s's %s", n, where), own, grantsOf(n))...)
		}
	}
	sort.Strings(wrong)
	if len(wrong) > 0 {
		t.Errorf("%d differences between what a design's Status line or README row says the human approved and "+
			"humanApprovedParts (%s). An approval is read as \"<part> … approved by the human on YYYY-MM-DD\", "+
			"\"the human approved <part> on YYYY-MM-DD\", or, here only, \"<id> … approved on YYYY-MM-DD\". Correct the document. Changing humanApprovedParts "+
			"instead requires a human decision, recorded as an ADR amendment in the same change:\n  %s",
			len(wrong), grantList(humanApprovedParts), strings.Join(wrong, "\n  "))
	}
}

// TestTheListsOfApprovalsNameEveryApprovedPart holds the three canonical lists
// of every approval — docs/designs/README.md's, and the "Do not read approved
// as settled" paragraph AGENTS.md and CLAUDE.md share — to humanApprovedParts.
// Each is found by approvalListAnchor and read by listedGrants. A list that
// drops an approval, adds one, or gives one the wrong day fails; so does a
// list the phrase no longer finds, rather than passing on nothing.
func TestTheListsOfApprovalsNameEveryApprovedPart(t *testing.T) {
	for _, rel := range []string{"docs/designs/README.md", "AGENTS.md", "CLAUDE.md"} {
		b, err := os.ReadFile(filepath.Join(repoRoot, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		found := approvalLists(string(b))
		if len(found) != 1 {
			t.Errorf("%s: %d paragraphs say %q; this test reads the one list of the human's approvals, so "+
				"there must be exactly one", rel, len(found), approvalListAnchor)
			continue
		}
		for _, d := range grantDiff(rel+"'s list of approvals", listedGrants(found[0]), humanApprovedParts) {
			t.Error(d)
		}
	}
}

// TestADesignThatIsNotApprovedPointsToTheList requires approvalPointer, byte
// for byte, in the Status line of every design the human has approved no part
// of and of docs/designs/TEMPLATE.md, so a design copied from it starts with
// the pointer. It forbids the pointer nowhere: it states no claim.
func TestADesignThatIsNotApprovedPointsToTheList(t *testing.T) {
	for n, head := range designStatusLines(t) {
		if humanApprovals[n] == notApproved && !strings.Contains(head, approvalPointer) {
			t.Errorf("design %s is not approved, and its Status line does not carry the sentence that points to "+
				"the list of approvals. Add, exactly:\n  %s", n, approvalPointer)
		}
	}
	b, err := os.ReadFile(filepath.Join(docsRoot, "designs", "TEMPLATE.md"))
	if err != nil {
		t.Fatalf("read TEMPLATE.md: %v", err)
	}
	if head, _ := headBullets(string(b)); !strings.Contains(head, approvalPointer) {
		t.Errorf("docs/designs/TEMPLATE.md's Status line does not carry approvalPointer, so a design copied "+
			"from it fails TestADesignThatIsNotApprovedPointsToTheList. Add, exactly:\n  %s", approvalPointer)
	}
}

// TestNoStatusLineCarriesAListOfApprovals forbids a Status line to name the
// parts of any design but its own beside an approval. A sentence that
// mentions approval and names another design's slice or amendment ("the only
// human approvals on record are design 03's first slice and its amendment
// A84") is a copy of the README's list, and it goes stale the day the human
// approves something else. It counts when it says "approvals", or when the part
// it names is one the human approved; so design 03's "whether design 10 is
// also unapproved … A80 records it" is a mention of design 10, not a list.
func TestNoStatusLineCarriesAListOfApprovals(t *testing.T) {
	for n, head := range designStatusLines(t) {
		for _, sent := range sentenceBreak.Split(approvalProse(head), -1) {
			if !approvalWord.MatchString(sent) {
				continue
			}
			plural := approvalsWord.MatchString(sent)
			for _, g := range listedGrants(sent) {
				_, approved := approvedPartKey(g)
				if g.design != "" && g.design != n && (plural || approved) {
					t.Errorf("design %s's Status line names design %s's %s beside an approval, in %q. "+
						"A Status line does not repeat the list of approvals; it carries approvalPointer instead",
						n, g.design, g.part, strings.TrimSpace(sent))
				}
			}
		}
	}
}

// TestNoDocumentCarriesAStaleListOfApprovals finds every other list of
// approvals by its structure (approvalList: a sentence that mentions approval
// and names three or more approved parts, or two beside "only" or
// "approvals") anywhere in approvalCorpus, and requires
// it to be current, or to say the day it was true. Inside a design, and in a
// design's README row, the design's own parts do not count: TestAPartly…
// holds those. The three canonical lists are held exactly by
// TestTheListsOfApprovalsNameEveryApprovedPart and are not read here.
//
// A list is accepted when it names exactly the parts humanApprovedParts holds,
// or when it was true on a day D it states: in its own sentence as "as of D"
// (the italic in-place note ADR-0034 Amendment 7 used), or, for an ADR, as a
// row of supersededApprovalLists whose amendment exists and is cited. "True on
// D" means it names every part approved before D, and nothing not approved by
// the end of D; a part approved on D itself may be in it or not, because the
// table does not record the hour. A list stated on no day, or wrong on its
// day, fails.
//
// Why a day and not a positional rule ("a list must be followed by an
// amendment"): position depends on where an amendment sits, any later date
// satisfies it whether or not it says anything about the list, and a list
// edited to name a new part still passes. A stated day is checked against the
// table, so the note is either true or the test fails.
func TestNoDocumentCarriesAStaleListOfApprovals(t *testing.T) {
	used := map[int]bool{}
	lists := 0
	for _, f := range approvalCorpus(t) {
		canonical := map[string]bool{}
		if f.rel == "docs/designs/README.md" || f.rel == "AGENTS.md" || f.rel == "CLAUDE.md" {
			for _, l := range approvalLists(f.text) {
				for _, s := range listBreak.Split(approvalProse(l), -1) {
					canonical[strings.Join(strings.Fields(s), " ")] = true
				}
			}
		}
		for _, u := range proseUnits(f) {
			for _, sent := range listBreak.Split(approvalProse(u.text), -1) {
				sent = strings.Join(strings.Fields(sent), " ")
				if canonical[sent] {
					continue
				}
				list, isList := approvalList(sent, u.def)
				if !isList {
					continue
				}
				lists++
				if listStands(list, sent) {
					continue
				}
				if m := asOfDate.FindStringSubmatch(sent); m != nil {
					t.Errorf("%s: a list of approvals says it is as of %s, and was not true that day: %q", f.rel, m[1], sent)
					continue
				}
				exempt := -1
				for i, x := range supersededApprovalLists {
					if x.file == f.rel {
						exempt = i
					}
				}
				if exempt < 0 {
					var names []string
					for k := range list {
						names = append(names, "design "+k.design+" "+k.part)
					}
					sort.Strings(names)
					t.Errorf("%s lists approvals (%s) that are not the current list, and says no day it was true: %q. "+
						"If it is a list of approvals, point to docs/designs/README.md instead, or mark the day in the "+
						"sentence, \"(as of YYYY-MM-DD)\". If it is not a list — it only mentions parts — reword it or "+
						"split it, so that no one sentence says \"only\" or \"approvals\" beside two approved parts",
						f.rel, strings.Join(names, ", "), sent)
					continue
				}
				used[exempt] = true
				x := supersededApprovalLists[exempt]
				if !trueOn(list, x.asOf) {
					t.Errorf("%s: supersededApprovalLists says its list was true on %s, and it was not: %q", f.rel, x.asOf, sent)
				}
				if !strings.Contains(f.text, "\n## "+x.amendment+" (") {
					t.Errorf("%s: supersededApprovalLists says %s marks its list dated, and it has no \"## %s (\" heading",
						f.rel, x.amendment, x.amendment)
				}
				if status, _ := headBullets(f.text); !strings.Contains(status, x.amendment+" (") {
					t.Errorf("%s: its Status line does not cite %s, which marks its list of approvals dated", f.rel, x.amendment)
				}
			}
		}
	}
	for i, x := range supersededApprovalLists {
		if !used[i] {
			t.Errorf("supersededApprovalLists names a list in %s that is not there; remove the row", x.file)
		}
	}
	if lists == 0 {
		t.Fatalf("found no list of approvals outside the canonical three; ADR-0030's is one, so the reader is broken")
	}
}

// trueOn reports whether a list named, on day, every part then approved of
// each design it names, and nothing approved after day. A part approved on
// day itself may be in it or not: the table does not record the hour.
func trueOn(list map[grant]bool, day string) bool {
	named := map[string]bool{}
	for k := range list {
		named[k.design] = true
	}
	for _, p := range humanApprovedParts {
		k := grant{design: p.design, part: p.part}
		if named[p.design] && ((p.date < day && !list[k]) || (p.date > day && list[k])) {
			return false
		}
	}
	return true
}

// listStands reports whether a list of approvals is accepted without an ADR
// exemption: it is the current list for the designs it names, or it says
// "as of D" and was true on D.
func listStands(list map[grant]bool, sent string) bool {
	if trueOn(list, "9999-12-31") {
		return true
	}
	m := asOfDate.FindStringSubmatch(sent)
	return m != nil && trueOn(list, m[1])
}

// approvalList reports whether a sentence is a list of approvals, and the
// approved parts it names. It is one when it mentions approval and names
// three or more parts the human approved, or two with a word that makes a
// list of them: "only" ("approved only in its first slice and its amendment
// A84") or "approvals". A sentence that mentions two approved parts in passing
// — "the approval of A89 changed text that A86 approved" — is not. Inside a
// design (def), the design's own parts do not count, nor does an id with no
// design named.
func approvalList(sent, def string) (map[grant]bool, bool) {
	list := map[grant]bool{}
	if !approvalWord.MatchString(sent) {
		return list, false
	}
	for _, g := range listedGrants(sent) {
		if k, ok := approvedPartKey(g); ok && (def == "" || (g.design != "" && k.design != def)) {
			list[k] = true
		}
	}
	return list, len(list) >= 3 || (len(list) == 2 && listMarker.MatchString(sent))
}

// proseUnit is a stretch of text a sentence cannot run out of, with the design
// a part in it defaults to.
type proseUnit struct{ text, def string }

// proseUnits splits a document into the stretches a sentence lives in. In
// Markdown, a paragraph is its lines joined, because a hard-wrapped sentence
// is still one sentence, and a table row is its own unit; in
// docs/designs/README.md a row's parts default to that row's design. A
// rendered HTML page is split at its block breaks.
func proseUnits(f corpusFile) []proseUnit {
	if strings.HasSuffix(strings.ToLower(f.rel), ".html") {
		var out []proseUnit
		for _, l := range strings.Split(f.text, "\n") {
			out = append(out, proseUnit{l, f.def})
		}
		return out
	}
	var out []proseUnit
	var para []string
	flush := func() {
		if para != nil {
			out = append(out, proseUnit{strings.Join(para, " "), f.def})
			para = nil
		}
	}
	for _, l := range strings.Split(f.text, "\n") {
		switch {
		case strings.TrimSpace(l) == "":
			flush()
		case strings.HasPrefix(strings.TrimSpace(l), "|"):
			flush()
			def := f.def
			if m := indexRow.FindStringSubmatch(l); m != nil && f.rel == "docs/designs/README.md" {
				n, _ := strconv.Atoi(m[1])
				def = fmt.Sprintf("%02d", n)
			}
			out = append(out, proseUnit{l, def})
		default:
			para = append(para, l)
		}
	}
	flush()
	return out
}

// TestTheListReaderTellsAListFromAMention pins what approvalList counts as a
// list of approvals, and what listStands accepts, on the shapes the corpus and
// a review of 2026-09-28 used.
func TestTheListReaderTellsAListFromAMention(t *testing.T) {
	for _, tc := range []struct {
		name, text   string
		list, stands bool
	}{
		{"two parts in passing", "The approval of A89 changed text that A86 approved.", false, false},
		{"two parts with only", "design 03 is approved only in its first slice and its amendment A84", true, false},
		{"two parts with only, dated", "design 03 is approved only in its first slice and its amendment A84 (as of 2026-09-23)", true, true},
		{"two parts with only, dated too early", "design 03 is approved only in its first slice and its amendment A84 (as of 2026-09-22)", true, false},
		{"three parts", "The human has approved design 03's first slice and its amendment A84, and design 16's first slice.", true, false},
		{"three parts, dated", "The human has approved design 03's first slice and its amendment A84, and design 16's first slice (as of 2026-09-23).", true, true},
		{"every part of design 03", "Design 03 is approved in its first slice and in A84, A85, A86 and A89.", true, true},
		{"no approval word", "design 03's first slice, A84 and A85 are built", false, false},
	} {
		list, isList := approvalList(approvalProse(tc.text), "")
		if isList != tc.list {
			t.Errorf("%s: %q: list %v, want %v (parts %v)", tc.name, tc.text, isList, tc.list, list)
			continue
		}
		if isList && listStands(list, tc.text) != tc.stands {
			t.Errorf("%s: %q: stands %v, want %v", tc.name, tc.text, !tc.stands, tc.stands)
		}
	}
}

// TestNoDocumentClaimsAnApprovedPartTheHumanDidNotGive reads every claim that
// the human approved something on a day, anywhere in approvalCorpus, and holds
// each to humanApprovedParts: "A90, approved by the human on 2026-09-29" fails
// wherever it is written, and so does "the human approved it on 2026-09-29",
// whose subject the reader cannot find but whose date the human approved
// nothing on. It reads only what humanApprovalClaims reads: an undated claim,
// or one that does not say the human approved, is not checked here.
func TestNoDocumentClaimsAnApprovedPartTheHumanDidNotGive(t *testing.T) {
	var wrong []string
	claims := 0
	files := approvalCorpus(t)
	for _, f := range files {
		for _, g := range humanApprovalClaims(f.text, f.def) {
			claims++
			if !pinned(g) {
				wrong = append(wrong, fmt.Sprintf("%s claims %s", f.rel, describeClaim(g)))
			}
		}
	}
	// Fail loudly rather than pass on nothing: the corpus holds dozens of
	// dated approvals, so a reader that finds a handful is broken.
	if claims < 20 {
		t.Fatalf("read only %d dated approvals in %d files; the corpus holds dozens, so the reader is broken", claims, len(files))
	}
	sort.Strings(wrong)
	if len(wrong) > 0 {
		t.Errorf("%d claims that the human approved something name a part or a day humanApprovedParts does not "+
			"hold (%s). If the approver is not the human, reword it so it does not say the human approved. If it "+
			"is the human, name the design and the part in the sentence, and add the row to humanApprovedParts in "+
			"the change that records the decision as an ADR amendment:\n  %s",
			len(wrong), grantList(humanApprovedParts), strings.Join(wrong, "\n  "))
	}
}

// TestTheApprovedPartReaderFindsTheSubject holds humanApprovalClaims and
// listedGrants to the shapes the corpus uses on 2026-09-28, and to the shapes
// an independent review of 2026-09-28 found an earlier reader got wrong, so
// neither can pass by reading nothing or by reading the wrong subject.
func TestTheApprovedPartReaderFindsTheSubject(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		want       []grant
	}{
		{"id then approved by", "A86 was approved by the human on 2026-09-24 on its independent critique's PASS", []grant{{"03", "A86", "2026-09-24"}}},
		{"human approved id", "The human approved A84 on 2026-09-23, after five independent critique rounds", []grant{{"03", "A84", "2026-09-23"}}},
		{"approved id and more", "the human approved A84 and decided D7 on 2026-09-23", []grant{{"03", "A84", "2026-09-23"}}},
		{"an id whose clause names the slice", "A85, an amendment to the approved first slice, was approved by the human on 2026-09-23", []grant{{"03", "A85", "2026-09-23"}}},
		{"the slice", "**Only §1.1's first slice is approved, by the human on 2026-09-12 (ADR-0034 Amendment 4).", []grant{{"03", "first slice", "2026-09-12"}}},
		{"slice after the verb", "The human approved the first slice on 2026-09-14, after twelve critiques", []grant{{"03", "first slice", "2026-09-14"}}},
		{"a design named before", "design 16's first slice, which the human approved on 2026-09-14", []grant{{"16", "first slice", "2026-09-14"}}},
		{"a design named after the verb", "The human approved design 16's first slice on 2026-09-12", []grant{{"16", "first slice", "2026-09-12"}}},
		{"every id of a run before", "A90 and A85, amendments to the approved first slice, both approved by the human on 2026-09-23", []grant{{"03", "A90", "2026-09-23"}, {"03", "A85", "2026-09-23"}}},
		{"every id of a run after", "the human approved A84, A85 and A90 on 2026-09-23", []grant{{"03", "A84", "2026-09-23"}, {"03", "A85", "2026-09-23"}, {"03", "A90", "2026-09-23"}}},
		{"a far date", "A86, the key-source half that reports an empty key set and withdraws nothing, was approved by the human after four independent rounds of critique on 2026-09-24", []grant{{"03", "A86", "2026-09-24"}}},
		{"an id ruled out", "A90, not A88, was approved by the human on 2026-09-29", []grant{{"03", "A90", "2026-09-29"}}},
		{"the human's approval of", "the human's approval of A89 on 2026-09-28", []grant{{"03", "A89", "2026-09-28"}}},
		{"a curly apostrophe", "the human’s approval of A89 on 2026-09-28", []grant{{"03", "A89", "2026-09-28"}}},
		{"a review path is not an id", "`reviews/03-a89-critique-r2.md` passed, and it was approved by the human on 2026-09-28", []grant{{"03", "", "2026-09-28"}}},
		{"a pronoun", "The human approved it on 2026-09-24, and A87 builds it", []grant{{"03", "", "2026-09-24"}}},
		{"the id in the previous sentence", "A87 builds it. Approved by the human on 2026-09-24", []grant{{"03", "", "2026-09-24"}}},
		{"a table cell ends the clause", "A84 | approved by the human on 2026-09-29", []grant{{"03", "", "2026-09-29"}}},
		{"not approved", "A80 was not approved by the human on 2026-09-23", nil},
		{"never approved", "A80 was never approved by the human on 2026-09-23", nil},
		{"a modal", "A90 would be approved by the human on 2026-10-01", nil},
		{"no date", "A84 was approved by the human after five rounds.", nil},
		{"not the human", "ADR-0031 was approved on 2026-09-05, and the approved first slice on 2026-09-12 stands", nil},
		{"a quoted claim", `this line read "A90, approved by the human on 2026-09-29"`, nil},
		{"a word between: also", "The human also approved A90 on 2026-09-29.", []grant{{"03", "A90", "2026-09-29"}}},
		{"a word between: then", "The human then approved A90 on 2026-09-29.", []grant{{"03", "A90", "2026-09-29"}}},
		{"a word between: never", "The human never approved A90 on 2026-09-29.", nil},
		{"an ADR before the phrase", "ADR-0031 was approved by the human on 2026-09-05", nil},
		{"an ADR after the phrase", "the human approved ADR-0031 on 2026-09-05", nil},
		{"an id after an ADR", "ADR-0034 records it: A86 was approved by the human on 2026-09-24", []grant{{"03", "A86", "2026-09-24"}}},
	} {
		got := humanApprovalClaims(tc.text, "03")
		if fmt.Sprint(got) != fmt.Sprint(tc.want) {
			t.Errorf("%s: %q reads as %v, want %v", tc.name, tc.text, got, tc.want)
		}
	}
	for _, tc := range []struct {
		name, text string
		want       []grant
	}{
		{"bare, id first", "A90 was approved on 2026-09-29 on its critique PASS.", []grant{{"16", "A90", "2026-09-29"}}},
		{"bare, no verb before", "A20 approved on 2026-09-29.", []grant{{"16", "A20", "2026-09-29"}}},
		{"bare, a run", "A84 and A85, both approved on 2026-09-23", []grant{{"16", "A84", "2026-09-23"}, {"16", "A85", "2026-09-23"}}},
		{"bare, no id", "the first slice was approved on 2026-09-14", nil},
		{"bare, withdrawn", "A20 was not approved on 2026-09-29", nil},
	} {
		if got := bareApprovalClaims(tc.text, "16"); fmt.Sprint(got) != fmt.Sprint(tc.want) {
			t.Errorf("bare %s: %q reads as %v, want %v", tc.name, tc.text, got, tc.want)
		}
	}
	for _, tc := range []struct {
		name, text string
		want       []grant
	}{
		{"the README's list", "The only approvals the human has given are design 03's first slice (2026-09-12), design 16's first slice (2026-09-14), design 03's amendments A84 and A85 (both 2026-09-23), design 03's amendment A86 (2026-09-24), and design 03's amendment A89 (2026-09-28).",
			[]grant{{"03", "first slice", "2026-09-12"}, {"16", "first slice", "2026-09-14"}, {"03", "A84", "2026-09-23"}, {"03", "A85", "2026-09-23"}, {"03", "A86", "2026-09-24"}, {"03", "A89", "2026-09-28"}}},
		{"AGENTS.md's order", "Design 03's amendments A84 and A85, both approved on 2026-09-23, A86, approved on 2026-09-24, and A89, approved on 2026-09-28, are the only other approvals the human has given.",
			[]grant{{"03", "A84", "2026-09-23"}, {"03", "A85", "2026-09-23"}, {"03", "A86", "2026-09-24"}, {"03", "A89", "2026-09-28"}}},
		{"a part with no date", "design 03's first slice and its amendment A84, and design 16's first slice.",
			[]grant{{"03", "first slice", ""}, {"03", "A84", ""}, {"16", "first slice", ""}}},
	} {
		if got := listedGrants(tc.text); fmt.Sprint(got) != fmt.Sprint(tc.want) {
			t.Errorf("%s: %q reads as %v, want %v", tc.name, tc.text, got, tc.want)
		}
	}
}
