// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

// Package docs holds the gate that keeps a superseded guarantee from surviving
// in the text an implementer actually builds from.
//
// It exists because of a specific, repeated failure. Design 03's guarantees were
// corrected four times — in an ADR, in a research note, and twice in amendment
// logs — and each time the *authoritative body* kept asserting the withdrawn
// version. A cross-family review (reviews/03-codex-review-r2.md, BLOCKER 8) put
// it plainly: two implementers reading the mapping table and the amendment log
// build different systems, and the mapping table is the one that reads as
// normative. Prose promising to have swept the document is what failed; this is
// the mechanism that replaces it.
//
// # What is scanned
//
// **The ROOT is `docs/` plus the Markdown at the top of the repository** —
// AGENTS.md, CLAUDE.md, README.md and their siblings. The root used to stop at
// `docs/`, undisclosed, and appending a planted claim to AGENTS.md left the gate
// green: the file this project tells every other model to read first, and which
// opens with the sentence the blanket-approval rule exists for, was outside it.
// Below the top level the walk stops, because vendored chart fixtures and test
// data live there and a corpus sweeping those would fail on text nobody here
// wrote. Nothing under `charts/` or `api/` is read at all — a false claim in a
// chart template or a CRD comment is not caught here.
//
// Scope is otherwise deliberate — it covers what an implementer builds FROM, and
// nothing whose job is to record or refute:
//
//   - Both `.md` and `.html` are scanned. `docs/architecture.html` is the
//     rendered face of the canonical document, hand-maintained beside it with no
//     generator between them; while the walk stopped at the file extension, that
//     page carried "27/27 designs approved · 26 ADRs" in its header and footer
//     for weeks after both bodies had been corrected. See renderHTML for what a
//     page is reduced to, and for the two things it deliberately does not read.
//   - Only the AUTHORITATIVE BODY is scanned — everything before a design's
//     amendment section. Amendment logs must be free to quote what they retract.
//   - docs/designs/reviews/** is never scanned. Reviews are history and are never
//     edited to match a later decision (write-spec).
//   - docs/research/** is never scanned. A research note is evidence, and the
//     note that supersedes another must quote every claim it refutes.
//   - Documents are skipped only by NAME, from the closed list in
//     frozenDocuments, because their bodies may no longer be edited. Deciding
//     that by pattern is what excluded `docs/architecture.md` — "superseded in
//     part" in its Status line — and a narrowed pattern still let a live design
//     out through "Replaces the superseded design 09". A skip decided by
//     matching fails silently in the one direction that matters.
//   - The superseded research note is not scanned but IS required to say so.
//
// Each rule bans an ASSERTION and permits a RETRACTION, because "X is withdrawn"
// is exactly the sentence a correction needs to make.
//
// # What this gate does not prove
//
// Stated here, at the top, because a gate that overstates its own reach is the
// same defect class it exists to catch — and a green run is exactly the moment
// nobody goes looking for the limits.
//
//   - **Every rule is a lexical tripwire, not a semantic guarantee.** A rule
//     catches the phrasings someone wrote down, and the corpus has already
//     defeated that twice from the inside: once by emphasis inside a banned
//     phrase, once because the live body said "revisionHash(spec) — spec only"
//     while the rule knew only a reviewer's paraphrase. A green run means no
//     KNOWN phrasing of a WITHDRAWN claim is present. It does not mean the
//     document is true. Nothing here reads a sentence nobody thought to ban.
//
//   - **renderHTML is not a browser.** It reduces a page to blocks of text; it
//     does not lay one out. A withdrawn guarantee typed into an inline `<svg>`'s
//     `aria-label`, or into any element dropped whole, is not scanned — see
//     renderHTML, which names what it drops and why. **This is not a corner:**
//     `docs/architecture.html` carries roughly 11,000 characters of
//     reader-visible prose inside ten SVG plates, about one per major section.
//     No rule matches any of it today, so the gap is latent rather than active —
//     but it is a tenth of the page, not an edge case, and a claim moved into a
//     plate's label would leave the gate silently.
//
//   - **A retraction anywhere in a block rescues everything in it, and an HTML
//     block is bigger than a table cell.** Markdown splits a table row into
//     cells, so a retraction on one side cannot launder a claim on the other.
//     HTML splits only on block-level tags, so a `<div>` of `<span>` pills — the
//     page's own `meta-row` — renders as ONE block and any retraction in it
//     rescues every claim beside it.
//
//     **Scope, stated because it was measured and is NOT closed: ONE of the
//     rules here is hardened against this.** blanket-approval-claim's rescue
//     clause was narrowed to explicit retractions after a replanted header claim
//     survived; planting four OTHER withdrawn guarantees into the same
//     `meta-row` launders all four, because every other rule accepts a bare
//     `supersed`. Neither general fix was taken, and both were measured first:
//     making `<span>` block-level splits eight mid-sentence uses and every
//     code-highlighting token, trading laundering for MISSED violations, which
//     is the worse direction for a gate; and requiring the retraction in the
//     same SENTENCE breaks legitimate corrections throughout this corpus, which
//     routinely quote a withdrawn claim and retract it in the next sentence.
//     So nineteen rules remain launderable inside an inline-only container.
package docs

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const docsRoot = "../../docs"

// The gate also reads the ENTRY DOCUMENTS at the repository root — AGENTS.md,
// CLAUDE.md, README.md and their siblings — through rootDocs below, which
// reuses licensing_test.go's repoRoot.
//
// They are not under docs/ and the gate did not read them: appending a planted
// claim to AGENTS.md left `make docs` green. That matters more than their
// location suggests. AGENTS.md is the file this project tells every other model
// to start from, and both it and CLAUDE.md open with the very sentence the
// blanket-approval rule exists for. A gate whose root stops at one directory
// protects the documents nobody lands on first.

// supersededNote is the research note that named a release which does not exist.
const supersededNote = "agentgateway-2.2-2026-08.md"

type rule struct {
	name string
	// onlyIn restricts a rule to one document. Used where a phrase is stale in
	// one design and correct elsewhere.
	onlyIn string
	// headRescue spares a document whose HEAD carries the correction, for a rule
	// whose claim lives in a body that may not be edited. Used exactly once, and
	// narrow on purpose — see blanket-approval-claim.
	headRescue *regexp.Regexp
	banned     *regexp.Regexp
	// allowed rescues a line that mentions the banned thing in order to retract
	// it. Nil means the mention is banned outright.
	allowed *regexp.Regexp
	why     string
}

var rules = []rule{
	{
		// The most-repeated false sentence in this corpus, and the one every
		// other rule here was written around rather than for.
		//
		// CLAUDE.md and AGENTS.md both open by recording it: "An earlier version
		// of this line said all 27 designs were approved and critique-passed.
		// That was false, and it was the first thing every contributor read."
		// It was corrected in architecture.md §18 and in the HTML body, and it
		// went on sitting in the HTML page's HEADER and FOOTER — because nothing
		// mechanical had ever looked for it. A withdrawn guarantee about a
		// gateway gets a rule; the withdrawn claim about the corpus itself did
		// not.
		//
		// headRescue, and NOT a whole-directory exemption. The first version of
		// this rule exempted `docs/decisions/` outright, on the argument that
		// enforcing it there would demand the ADR-body edit the adr skill
		// forbids. **That argument was wrong on a fact.** ADR-0020 and ADR-0033
		// are already out of the corpus as frozen documents, so the only file the
		// directory exemption shielded was **ADR-0026 — accepted and live, not
		// frozen** — and it shielded the correction too: deleting Amendment 2 and
		// the Status marker left the gate green, so nothing pinned the very fix
		// this branch made.
		//
		// The narrow form keeps the ADR's body untouched AND pins its marker: the
		// claim is rescued only while the document's HEAD says the Consequences
		// line is withdrawn. Remove the marker and the gate reports the ADR. No
		// ADR body is edited either way, which is the whole constraint.
		//
		// The rescue clause deliberately omits the bare word `supersed`, which
		// every other rule here carries. Measured: with it, re-planting the
		// header claim did NOT fail the gate. `<span>` is inline, so the page's
		// whole `meta-row` renders as ONE block, and the corrected pill beside
		// it — "superseded in part" — rescued the planted one. The word this
		// rule's own corrections use is "withdrawn", so the clause asks for
		// that and for the other explicit retractions, and nothing weaker.
		name:       "blanket-approval-claim",
		headRescue: regexp.MustCompile(`(?i)is withdrawn by ADR-0030`),
		banned:     regexp.MustCompile(`(?i)27/27|design phase is complete|all 27 designs|every component[^.]{0,80}approved`),
		allowed:    regexp.MustCompile(`(?i)withdraw|retract|w(as|ere) false|are now false|is false|is not true|not approved|first slice|no longer|never meant`),
		why:        "no central design is approved whole: design 02 is not approved by its own header, and 03 and 16 approve only their first slice (ADR-0030; docs/designs/README.md arbitrates)",
	},
	{
		name:    "superseded-research-note",
		banned:  regexp.MustCompile(regexp.QuoteMeta(supersededNote)),
		allowed: regexp.MustCompile(`(?i)supersed|no such|replaces|written against`),
		why:     "cites the superseded note; use agentgateway-v1.4.1-2026-08.md",
	},
	{
		name:    "nonexistent-release",
		banned:  regexp.MustCompile(`agentgateway[ -]2\.2`),
		allowed: regexp.MustCompile(`(?i)supersed|does not exist|no such|kgateway`),
		why:     "there is no agentgateway 2.x; the floor is v1.4.1 (ADR-0028)",
	},
	{
		name:    "cuts-early-guarantee",
		banned:  regexp.MustCompile(`(?i)cuts? early|never late`),
		allowed: regexp.MustCompile(`(?i)withdraw|retract|no longer|cannot|is false|supersed`),
		why:     "token limits apply to future requests only; the guarantee is withdrawn (ADR-0028)",
	},
	{
		name:    "exact-usd-tier",
		banned:  regexp.MustCompile("(?i)exact tier|exact budget tier|backstop is exact"),
		allowed: regexp.MustCompile(`(?i)withdraw|retract|not exact|no longer|supersed`),
		why:     "the receipt tier sums the same usd_est estimates the gateway uses; it is not exact in USD",
	},
	{
		name:    "nonexistent-tracing-field",
		banned:  regexp.MustCompile(`frontendPolicies`),
		allowed: regexp.MustCompile(`(?i)supersed|no such|there is no|instead of`),
		why:     "no such field at v1.4.1; tracing is AgentgatewayPolicy.spec.frontend.tracing, Gateway-scoped",
	},
	{
		name:    "superseded-adr",
		banned:  regexp.MustCompile(`ADR-0020`),
		allowed: regexp.MustCompile(`0028|supersed`),
		why:     "ADR-0020 is superseded by ADR-0028; cite the live one",
	},
	{
		name:    "egress-as-restriction",
		banned:  regexp.MustCompile(`(?i)egressRestricted|Backend restriction`),
		allowed: regexp.MustCompile(`(?i)no such|construction|not restriction|earlier draft|supersed`),
		why:     "the CRD has no egress field; the control is Backend construction (design 03 §3.4.1)",
	},
	{
		name:    "overshoot-bound",
		banned:  regexp.MustCompile(`(?i)overshoot[- ]bounds?|measured overshoot|excess is bounded`),
		allowed: regexp.MustCompile(`(?i)withdraw|retract|no bound|unbounded|refuted|is false|supersed`),
		why:     "the spike measured 100x a one-replica budget under concurrency; no bound is published (design 03 A13)",
	},
	{
		name:    "pricing-damage-bounded",
		banned:  regexp.MustCompile(`(?i)backstop bounds?|bounds? the damage|damage is bounded`),
		allowed: regexp.MustCompile(`(?i)withdraw|retract|not exact|no longer|supersed`),
		why:     "the receipt tier prices from the same table it is meant to backstop; it bounds nothing (ADR-0028)",
	},
	{
		name:    "in-worker-poll",
		banned:  regexp.MustCompile("(?i)acceptance poll(s|ing)?|poll(s|ing)? each resource|bounded: ?30 ?s|on the reconcile worker"),
		allowed: regexp.MustCompile(`(?i)earlier draft|supersed|no longer|replaced|state machine`),
		why:     "A15 replaced the blocking poll with an event-driven staged reconcile; a worker that sleeps starves the fleet",
	},
	{
		name:    "allowlist-equality",
		banned:  regexp.MustCompile(`(?i)set equals the allowlist|equals the allowlist|exactly the resolved allowlist`),
		allowed: regexp.MustCompile(`(?i)earlier draft|supersed|conflat|subset|not equality`),
		why:     "egressAllowlist is a ceiling, not a selection: requested must be a SUBSET of permitted (A16)",
	},
	{
		name: "spec-only-hash",
		// The first version of this rule knew only the two phrasings a reviewer
		// had written in prose. The live body said "revisionHash(spec) — spec
		// only", matched nothing, and the gate stayed green over the exact claim
		// it existed to ban. A rule must cover how the DOCUMENT says it.
		banned:  regexp.MustCompile(`(?i)computed from spec alone|hashed by referent|revisionHash\(spec\)[^;.]{0,12}spec only|hash covers spec only|spec only; the`),
		allowed: regexp.MustCompile(`(?i)no longer|earlier|retract|supersed|was never|A20`),
		why:     "A20 hashes env sources by content, so the digest is no longer spec-only",
	},
	{
		name: "env-referent-hash",
		// The sibling phrasing, and the dangerous one: it sits in the A12
		// classification TABLE, which is the cell an implementer copies from.
		banned:  regexp.MustCompile(`(?i)by referent,? not contents|referent(s)? rather than contents|referent identity is (the |)hash`),
		allowed: regexp.MustCompile(`(?i)was never sufficient|no longer|retract|supersed|earlier`),
		why:     "A20 hashes every env source by CONTENT; referent identity alone let an update replace a prompt under a gated revision",
	},
	{
		name: "seal-not-copy",
		// A23 chose to seal env sources in place and said so emphatically — "no
		// bytes are copied" was the design's shorthand for the whole mechanism.
		// A35 reversed it: a revision reads its own immutable copy, because a
		// seal is a control and a removed control stops controlling. The phrase
		// is memorable enough to be quoted back into a body by someone working
		// from the older half of the document.
		banned:  regexp.MustCompile(`(?i)no bytes are copied|sealed,? not snapshotted|seals? the source in place`),
		allowed: regexp.MustCompile(`(?i)supersed|revers|retract|no longer|A35|retired`),
		why:     "A35 replaced the seal with immutable revision-scoped copies; sealing cannot close unplanned guard loss",
	},
	{
		name: "referent-drift-refusal",
		// The pre-A35 rollback rule: refuse a rollback because the USER's object
		// changed since the revision was gated. A35 makes that irrelevant — the
		// revision reads its own copy, so original drift says nothing about
		// whether the revision can be rerun — and following the old rule extends
		// an incident by declining a recovery that is available.
		banned:  regexp.MustCompile(`(?i)rollback is refused[^.]*(referent|env content|original)|referent'?s? content has (changed|moved)`),
		allowed: regexp.MustCompile(`(?i)supersed|revers|retract|no longer|irrelevant|A38`),
		why:     "A38: rollback tests the retained COPY's digest and owner, never the original source's drift",
	},
	{
		name: "bare-revision-gate",
		// A gate keyed on the ten-character revision NAME can be spent on a
		// colliding projection, and it decides before any workload exists to
		// inspect — so design 02's workload guard is not in that path. A37 and
		// A50 separated name from identity; this stops the shorthand returning to
		// a body that grants production traffic.
		banned:  regexp.MustCompile(`(?i)evalStatus\.revision ==|verdict names the revision name|gate(s|d)? on the revision name`),
		allowed: regexp.MustCompile(`(?i)supersed|no longer|retract|digest|A37|A50`),
		why:     "a revision NAME is 40 bits and a chosen collision costs about a second; the digest decides (A37, A50, design 16 A2)",
	},
	{
		name:    "verifier-undecided",
		banned:  regexp.MustCompile(`(?i)Sigstore[^.]{0,40}or[^.]{0,10}Kyverno|Kyverno[^.]{0,40}or[^.]{0,10}Sigstore`),
		allowed: regexp.MustCompile(`(?i)not a design|earlier|supersed|chose|decided`),
		why:     "design 07 A2 chose Sigstore policy-controller; \"or Kyverno\" names two controllers, not a contract",
	},
	{
		name:    "inert-tightening",
		banned:  regexp.MustCompile(`(?i)make (the |every |its )?(dependent )?routes? inert|quiesc`),
		allowed: regexp.MustCompile(`(?i)withdraw|retract|earlier draft|no longer|supersed|A19`),
		why:     "A19 withdrew in-place tightening; a serving route is never made inert (design 03 §3.3.3)",
	},
	{
		// Scoped: routing, metering and budgeting on Mcp-Name is still valid at
		// v1.4.1 (SEP-2243), and designs 01/24 use it that way. Only design 03
		// claimed it as the TOOL FILTER, which is what backend.mcp.authorization
		// replaced — and only that claim is banned.
		name:    "header-tool-filter",
		onlyIn:  "03-policy-compiler.md",
		banned:  regexp.MustCompile(`Mcp-Name`),
		allowed: regexp.MustCompile(`(?i)supersed|SEP-2243|cannot filter|weaker`),
		why:     "tool filtering is backend.mcp.authorization CEL, which also filters tools/list (§3.4.2)",
	},
}

// frozenMarker identifies a document whose content may no longer be edited —
// a superseded ADR, or a note that has been replaced. Scanning one would demand
// an edit the corpus's own rules forbid.
var frozenMarker = regexp.MustCompile(`(?i)SUPERSEDED|superseded-by`)

// supersedes reports whether this document declares that it replaces the thing
// the rule bans. ADR-0028 must be able to say which ADR it supersedes.
//
// Restricted to ADRs on purpose. An earlier version exempted any file whose
// header mentioned the supersession, which silently exempted design 03 — the
// document most likely to carry a stale citation, and the reason this gate
// exists. A design's own header line is rescued by the per-line retraction
// clause instead, which is narrower.
func supersedes(path, ruleName string) bool {
	if ruleName != "superseded-adr" {
		return false
	}
	if filepath.Base(filepath.Dir(path)) != "decisions" {
		return false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	head := string(b)
	if len(head) > 800 {
		head = head[:800]
	}
	return strings.Contains(head, "supersedes ADR-0020")
}

// frozenDocuments is the CLOSED LIST of documents this gate does not read,
// because their bodies may no longer be edited and a violation in one would
// demand the edit the adr skill forbids.
//
// **A list, not a pattern, and that inversion is the whole point.** Freezing was
// decided by matching the word "superseded" in a document's first 800 bytes, and
// a pattern that decides what to SKIP fails silently in the one direction that
// matters: the skipped file reports nothing, which is indistinguishable from a
// clean one. It swallowed `docs/architecture.md` — the canonical document, whose
// Status line reads "superseded in part" at byte 608 — from the day the gate was
// written. Narrowing the pattern to spare "in part" fixed that ONE sentence and
// left the trapdoor: a live design whose Status says "Replaces the superseded
// design 09" still left the corpus, silently, through a different trigger word.
//
// Now nothing is skipped unless it is named here, so a new phrasing cannot
// remove a document from the gate. The cost is that superseding an ADR means
// adding a line here in the same commit — and the failure when you forget is
// LOUD: the gate reports the frozen body's stale claims and the message says to
// add it. That is the same "edit the ledger in the same change" discipline the
// pod budget uses, and it is the right direction for a skip list.
//
// Both directions are asserted: TestEveryFrozenDocumentSaysSo checks the list
// cannot hide a live document, and TestEveryDocumentDeclaringItselfSupersededIsListed
// checks the corpus cannot drift out from under the list.
var frozenDocuments = []string{
	"docs/decisions/0020-policy-compiler.md",
	"docs/decisions/0033-slice-auth-api-keys-per-agent-no-live-tightening.md",
	"docs/HANDOFF.md",
}

// relToRepo renders a path the way frozenDocuments spells one: relative to the
// repository root, forward slashes.
func relToRepo(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	root, err := filepath.Abs(repoRoot)
	if err != nil {
		return filepath.ToSlash(path)
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

// isFrozen reports whether this path is one of the named frozen documents.
//
// EXACT, not a suffix. The suffix form was reachable around: a file at
// `docs/designs/docs/HANDOFF.md` ends with a listed path and was skipped
// silently, which is the trapdoor again in the mechanism that replaced it. And
// the forward check in TestEveryDocumentDeclaringItselfSupersededIsListed
// already compared exact relative paths, so one list had two matchers and only
// one of them could be reached around.
//
// Anchoring to repoRoot is also what makes a temp-directory collision
// impossible — a fixture at `<tmp>/docs/HANDOFF.md` does not resolve to
// `docs/HANDOFF.md` relative to this repository. The comment here used to claim
// the opposite, crediting the suffix match for the property the anchor provides.
func isFrozen(path string) bool {
	rel := relToRepo(path)
	for _, f := range frozenDocuments {
		if rel == f {
			return true
		}
	}
	return false
}

// declaresItselfSuperseded matches the two ways this corpus says a WHOLE
// document is finished: an ADR's `superseded-by`, and the dated record's
// "superseded, not rewritten". It no longer decides anything — it is the
// consistency check between the list above and the documents themselves.
var declaresItselfSuperseded = regexp.MustCompile(`(?i)superseded-by|superseded, not rewritten`)

func headOf(content string) string {
	if len(content) > 800 {
		return content[:800]
	}
	return content
}

// headRescued reports whether the document's head carries the correction a rule
// accepts in place of an edit to the body.
//
// Restricted to ADRs, for the same reason supersedes() is: a head-level rescue
// is document-wide, and the document most likely to carry a stale claim is the
// one where that would do the most damage. An ADR's body is the only text in
// this corpus a repository rule forbids editing; everything else can simply be
// corrected in place, and must be.
func headRescued(path string, re *regexp.Regexp) bool {
	if filepath.Base(filepath.Dir(path)) != "decisions" {
		return false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return re.MatchString(headOf(string(b)))
}

// markdownNoise is inline formatting that must not let a superseded phrase
// through. A Codex review found the gate passing on "the acceptance **poll**"
// because the rule expected a contiguous "acceptance poll": emphasis inside the
// phrase defeated it. A lexical tripwire pretending to be a semantic guarantee.
//
// The pipe is deliberately NOT stripped. Adding it here changed no outcome any
// test could detect — blocks() already splits a table row into cells, so no
// pipe survives into a rendered block — and unpinned code that reads as
// load-bearing is worse than no code: the next reader trusts it.
var markdownNoise = regexp.MustCompile("[*`_~]+")

// collapseSpace folds newlines too, so a phrase broken across a wrapped line is
// still matched.
var collapseSpace = regexp.MustCompile(`\s+`)

// Inline constructs whose SOURCE differs from what a reader sees. A growing
// punctuation regex is not enough: the emphasis fix still let
// "the acceptance [poll](https://x) verifies" through, because the link
// destination sat inside the banned phrase. These reduce each construct to its
// rendered text before matching.
var (
	// ![alt](url) and [text](url) -> alt / text. Images first, or the leading
	// "!" is left behind on the rendered text.
	mdImage = regexp.MustCompile(`!\[([^\]]*)\]\([^)]*\)`)
	mdLink  = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	// [text][ref] and [text][] -> text
	mdRefLink = regexp.MustCompile(`\[([^\]]*)\]\[[^\]]*\]`)
	// <https://…> -> the bare URL, so an autolink cannot split a phrase.
	mdAutolink = regexp.MustCompile(`<(https?://[^>]*)>`)
)

// normalize reduces a line to the text a reader sees, so rules match meaning
// rather than source bytes.
//
// SCOPE, stated because the limit is the point: this handles INLINE constructs —
// images, links, reference links, autolinks, emphasis and code spans. It is not
// a full CommonMark renderer, so a banned phrase split across a block construct
// (a table cell boundary, a list item, an HTML comment) is not normalised. Each
// construct handled here is pinned by an independent fixture below; a construct
// that is not pinned is not claimed.
func normalize(s string) string {
	s = mdImage.ReplaceAllString(s, "$1")
	s = mdLink.ReplaceAllString(s, "$1")
	s = mdRefLink.ReplaceAllString(s, "$1")
	s = mdAutolink.ReplaceAllString(s, "$1")
	s = markdownNoise.ReplaceAllString(s, "")
	return collapseSpace.ReplaceAllString(s, " ")
}

// Block boundaries. Markdown joins soft-wrapped lines inside a paragraph into
// one rendered line, so a scan that splits on "\n" before matching cannot see a
// phrase a normal hard wrap happened to break — which is how "acceptance\npoll
// verifies convergence" passed this gate. These patterns mark where a rendered
// block genuinely ENDS, so wraps are joined and real boundaries are not.
var (
	bHeading  = regexp.MustCompile(`^\s{0,3}#{1,6}\s`)
	bListItem = regexp.MustCompile(`^\s*([-*+]|\d+[.)])\s`)
	bTableRow = regexp.MustCompile(`^\s*\|`)
	bQuote    = regexp.MustCompile(`^\s*>`)
	bRule     = regexp.MustCompile(`^\s*([-*_]\s*){3,}$`)
	bFence    = regexp.MustCompile("^\\s*(```|~~~)")
)

// blocks renders an authoritative body into the units a READER perceives: one
// block per paragraph, heading, list item, table CELL or line of code.
//
// A line inside a fenced code block stands alone because code has no soft
// wrapping, and joining two statements would manufacture a sentence nobody
// wrote.
//
// A table cell is its own block for a subtler reason: RESCUE SCOPE. Every rule
// may carry an `allowed` pattern that spares a block which retracts the claim it
// states. If a whole row were one block, the word "superseded" in the right-hand
// column would silently launder a live guarantee asserted in the left-hand one —
// and the A12 classification table is exactly a two-column document where one
// side is a claim and the other is commentary.
func blocks(text string) []string {
	var out []string
	var cur []string
	flush := func() {
		if len(cur) > 0 {
			out = append(out, normalize(strings.Join(cur, " ")))
			cur = nil
		}
	}
	inFence := false
	for _, line := range strings.Split(text, "\n") {
		if bFence.MatchString(line) {
			flush()
			inFence = !inFence
			continue
		}
		if inFence {
			flush()
			out = append(out, normalize(line))
			continue
		}
		if strings.TrimSpace(line) == "" || bRule.MatchString(line) {
			flush()
			continue
		}
		if bTableRow.MatchString(line) {
			flush()
			for _, cell := range strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|") {
				if n := normalize(cell); strings.TrimSpace(n) != "" {
					out = append(out, n)
				}
			}
			continue
		}
		if bHeading.MatchString(line) || bListItem.MatchString(line) || bQuote.MatchString(line) {
			flush()
		}
		cur = append(cur, strings.TrimSpace(line))
	}
	flush()
	return out
}

// HTML pages are scanned too, because `docs/architecture.html` is the rendered
// face of the canonical document and is hand-maintained beside it with no
// generator between them. Scanning only the Markdown is how "27/27 designs
// approved · 26 ADRs" — the exact sentence CLAUDE.md opens by recording as false
// and as "the first thing every contributor read" — survived in that page's
// header and footer for weeks after the body of both files had been corrected.
// A gate that stops at the file extension does not cover the document.
//
// SCOPE, stated because the limit is the point. This reduces a page to the text
// a reader sees; it is not a browser:
//
//   - `<script>`, `<style>` and inline `<svg>` plates are dropped WHOLE. Their
//     text nodes are code, declarations and diagram coordinates, not prose — and
//     an `<img src="data:…">` blob is not prose either, which is why tags are
//     removed with their attributes. The cost is real and is the reason it is
//     written here: a withdrawn guarantee typed into an SVG's `aria-label` is
//     NOT scanned.
//   - Block-level tags end a rendered block, so a table cell and a paragraph are
//     separate blocks exactly as in Markdown; every other tag is inline and is
//     removed without a break, so `<b>` inside a phrase cannot split it.
//   - Only the handful of entities this corpus actually uses are decoded.
var (
	htmlComment = regexp.MustCompile(`(?s)<!--.*?-->`)
	// Dropped whole, content included. One pattern per element because RE2 has
	// no back-reference, and a single alternation would let `<style>…</svg>`
	// swallow everything between two different elements.
	htmlDropped = []*regexp.Regexp{
		regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script\s*>`),
		regexp.MustCompile(`(?is)<style\b[^>]*>.*?</style\s*>`),
		regexp.MustCompile(`(?is)<svg\b[^>]*>.*?</svg\s*>`),
	}
	// Tags a reader perceives as a break between blocks.
	htmlBlockTag = regexp.MustCompile(`(?i)</?(p|div|li|tr|td|th|h[1-6]|br|hr|section|article|table|thead|tbody|tfoot|ul|ol|dl|dt|dd|pre|blockquote|figure|figcaption|nav|header|footer|main|aside|title)\b[^>]*>`)
	// Everything left is inline, and leaves no gap when it goes.
	htmlAnyTag  = regexp.MustCompile(`(?s)<[^>]*>`)
	htmlEntity  = strings.NewReplacer("&nbsp;", " ", "&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#39;", "'", "&mdash;", "—", "&ndash;", "–", "&hellip;", "…", "&rarr;", "→", "&times;", "×")
	htmlDoctype = regexp.MustCompile(`(?i)<!doctype[^>]*>`)
)

// renderHTML reduces a page to the blocks of text a reader sees.
func renderHTML(s string) string {
	s = htmlDoctype.ReplaceAllString(s, "\n")
	s = htmlComment.ReplaceAllString(s, "\n")
	for _, re := range htmlDropped {
		s = re.ReplaceAllString(s, "\n")
	}
	s = htmlBlockTag.ReplaceAllString(s, "\n")
	s = htmlAnyTag.ReplaceAllString(s, "")
	return htmlEntity.Replace(s)
}

// amendmentHeading marks where a design's authoritative body ends.
var amendmentHeading = regexp.MustCompile(`(?m)^## \d+\. Amendment`)

// body returns the part of a design an implementer is meant to build from.
func body(content string) string {
	if loc := amendmentHeading.FindStringIndex(content); loc != nil {
		return content[:loc[0]]
	}
	return content
}

// violation is one rule firing on one rendered block.
type violation struct {
	rule  string
	block string
	why   string
}

// scanBody is THE matcher. Production and every fixture call it, so a fixture
// can never prove a path production does not take — the defect that let a
// hard-wrapped banned phrase survive while a fixture calling normalize() on the
// same string reported the rule healthy.
func scanBody(path, text string) []violation {
	var out []violation
	for _, blk := range blocks(text) {
		for _, r := range rules {
			if r.onlyIn != "" && filepath.Base(path) != r.onlyIn {
				continue
			}
			if r.headRescue != nil && headRescued(path, r.headRescue) {
				continue // the document's head carries the correction
			}
			if supersedes(path, r.name) {
				continue // a document is allowed to name what it supersedes
			}
			if !r.banned.MatchString(blk) {
				continue
			}
			if r.allowed != nil && r.allowed.MatchString(blk) {
				continue // a retraction, which is what a correction must say
			}
			out = append(out, violation{rule: r.name, block: strings.TrimSpace(blk), why: r.why})
		}
	}
	return out
}

// discover returns the authoritative bodies under root. It reports an error
// rather than calling t.Fatalf so the empty-corpus guard — the one that stops
// this whole gate from passing because a path moved — is itself testable.
func discover(root string) (map[string]string, error) {
	out := map[string]string{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			// Reviews are history; research notes are evidence. Neither is rewritten
			// to match a later decision, and both must quote what they refute.
			if info.Name() == "reviews" || info.Name() == "research" {
				return filepath.SkipDir
			}
			return nil
		}
		isMD := strings.HasSuffix(path, ".md")
		isHTML := strings.HasSuffix(path, ".html")
		if !isMD && !isHTML {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if isFrozen(path) {
			return nil
		}
		if isHTML {
			out[path] = renderHTML(string(b))
			return nil
		}
		out[path] = body(string(b))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking %s: %w", root, err)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("scanned no documents under %s — the gate would pass vacuously", root)
	}
	return out, nil
}

func scanned(t *testing.T, root string) map[string]string {
	t.Helper()
	out, err := discover(root)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// rootDocs returns the entry documents at the top of the repository. Not a walk:
// see repoRoot for why the recursion stops here.
func rootDocs(t *testing.T) map[string]string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(repoRoot, "*.md"))
	if err != nil {
		t.Fatalf("globbing the repository root: %v", err)
	}
	out := map[string]string{}
	for _, path := range matches {
		if isFrozen(path) {
			continue
		}
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		out[path] = body(string(b))
	}
	if len(out) == 0 {
		t.Fatal("no Markdown at the repository root — AGENTS.md and CLAUDE.md are the entry " +
			"documents and the gate would silently stop covering them")
	}
	return out
}

// corpus is everything the gate reads: docs/ plus the entry documents.
func corpus(t *testing.T) map[string]string {
	t.Helper()
	out := scanned(t, docsRoot)
	for k, v := range rootDocs(t) {
		out[k] = v
	}
	return out
}

// TestAnEmptyCorpusIsAFailure pins the guard that stops this gate reporting
// success because a directory moved. Without it every rule passes vacuously and
// the suite is indistinguishable from one that works.
func TestAnEmptyCorpusIsAFailure(t *testing.T) {
	if _, err := discover(t.TempDir()); err == nil {
		t.Error("an empty corpus was accepted; every rule would then pass on nothing")
	}
	if _, err := discover(docsRoot); err != nil {
		t.Errorf("the real corpus must be discoverable: %v", err)
	}
}

// TestTheCanonicalDocumentIsScanned names the two files by path, because the
// corpus-level guard cannot see one file going missing.
//
// AGENTS.md calls `docs/architecture.md` canonical and `docs/architecture.html`
// its rendered face. Both were outside this gate: the Markdown by the frozen
// rule (see partiallySuperseded), the HTML by the file extension (see
// renderHTML). Either exclusion returning leaves every rule here passing on a
// corpus that no longer contains the documents most people read.
func TestTheCanonicalDocumentIsScanned(t *testing.T) {
	got := corpus(t)
	want := map[string]string{
		"architecture.md":   docsRoot,
		"architecture.html": docsRoot,
		// The entry documents. Dropping rootDocs from the corpus used to survive
		// the whole suite: the gate would stop reading the file this project
		// tells every other model to start from, and nothing would fail.
		"AGENTS.md": repoRoot,
		"CLAUDE.md": repoRoot,
		"README.md": repoRoot,
	}
	for name, root := range want {
		p := filepath.Join(root, name)
		if _, ok := got[p]; !ok {
			t.Errorf("%s is not in the scanned corpus. It is the canonical document, its "+
				"rendered face, or an entry document a contributor reads first; a gate that "+
				"skips it reports the same silence as a clean scan", p)
		}
	}
}

// TestEveryFrozenDocumentSaysSo stops the skip list from hiding a live document.
//
// The list decides what the gate does not read, so an entry added carelessly —
// or maliciously — would remove a document from the corpus with no other
// symptom. Every entry must exist and must declare its own supersession in its
// head, which is a claim the document makes about itself and not one the list
// makes about it.
func TestEveryFrozenDocumentSaysSo(t *testing.T) {
	for _, rel := range frozenDocuments {
		path := filepath.Join(repoRoot, rel)
		b, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("%s is listed as frozen and is not there: %v", rel, err)
			continue
		}
		if !declaresItselfSuperseded.MatchString(headOf(string(b))) {
			t.Errorf("%s is on the frozen list but its head does not declare it superseded. "+
				"The list removes a document from this gate entirely; an entry that the "+
				"document itself does not corroborate is a way to hide a live document", rel)
		}
	}
}

// TestTheFrozenListIsMatchedExactly pins MINOR E: the skip list is compared by
// exact repository-relative path, not by suffix.
//
// Measured: under the suffix form, a document at `docs/designs/docs/HANDOFF.md`
// ends with a listed path, was skipped silently, and carried two banned claims
// past a green run. That is the same trapdoor the list replaced, reopened inside
// the replacement — and the forward consistency check already used exact paths,
// so one list had two matchers and only one could be reached around.
func TestTheFrozenListIsMatchedExactly(t *testing.T) {
	if !isFrozen(filepath.Join(repoRoot, "docs/HANDOFF.md")) {
		t.Fatal("the genuinely frozen document is no longer matched at all; this test would " +
			"then pass for the wrong reason")
	}
	for _, reachAround := range []string{
		"docs/designs/docs/HANDOFF.md",
		"docs/decisions/copy/docs/decisions/0020-policy-compiler.md",
	} {
		if isFrozen(filepath.Join(repoRoot, reachAround)) {
			t.Errorf("%s is treated as frozen because its path ENDS with a listed one. A skip "+
				"decided by suffix can be reached around, which is the defect the list "+
				"replaced", reachAround)
		}
	}
	// And a fixture corpus must never collide with the list, whatever it is named.
	dir := t.TempDir()
	if isFrozen(filepath.Join(dir, "docs/HANDOFF.md")) {
		t.Error("a temp-directory fixture matched the frozen list; anchoring to repoRoot is " +
			"what is supposed to make that impossible")
	}
}

// TestEveryDocumentDeclaringItselfSupersededIsListed is the other direction, and
// it is the one that fails LOUDLY when someone supersedes an ADR and forgets.
//
// Without it the corpus drifts out from under the list silently in the safe
// direction and noisily in the wrong one: a newly superseded ADR would be
// scanned, its frozen body would report its stale claims, and the only guidance
// would be a violation the contributor is forbidden to fix by editing. This test
// names the real remedy instead.
func TestEveryDocumentDeclaringItselfSupersededIsListed(t *testing.T) {
	listed := map[string]bool{}
	for _, f := range frozenDocuments {
		listed[f] = true
	}
	check := func(path string) {
		b, err := os.ReadFile(path)
		if err != nil {
			return
		}
		if !declaresItselfSuperseded.MatchString(headOf(string(b))) {
			return
		}
		rel := relToRepo(path)
		if !listed[rel] {
			t.Errorf("%s declares itself superseded and is not on frozenDocuments. Its body "+
				"may no longer be edited, so this gate must not read it: add the path to "+
				"frozenDocuments in this same change", rel)
		}
	}
	_ = filepath.Walk(filepath.Join(repoRoot, "docs"), func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if info.Name() == "reviews" || info.Name() == "research" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".md") {
			check(path)
		}
		return nil
	})
	matches, _ := filepath.Glob(filepath.Join(repoRoot, "*.md"))
	for _, m := range matches {
		check(m)
	}
}

// TestTheCanonicalDocumentIsNotFrozen is the regression this whole mechanism
// exists for, named by path so it cannot be reached around.
//
// `docs/architecture.md` says "superseded in part" in its Status line, and the
// pattern that used to decide freezing read that as "superseded" and dropped the
// canonical document from the gate. Narrowing the pattern fixed that one
// sentence; a live design whose Status said "Replaces the superseded design 09"
// left the corpus through the same trapdoor with a different trigger word. No
// phrasing reaches a list of names — but a list is only as tight as the matcher
// that reads it, and the first version of this one matched by SUFFIX and was
// reachable around by a path ending in a listed one. TestTheFrozenListIsMatchedExactly
// pins the exact comparison that closed it.
func TestTheCanonicalDocumentIsNotFrozen(t *testing.T) {
	for _, p := range []string{"docs/architecture.md", "docs/architecture.html", "AGENTS.md", "CLAUDE.md"} {
		if isFrozen(filepath.Join(repoRoot, p)) {
			t.Errorf("%s is treated as frozen and would be skipped entirely", p)
		}
	}
	// A document that merely MENTIONS supersession anywhere, including its head,
	// is still read. This is the trapdoor closed.
	dir := t.TempDir()
	sub := filepath.Join(dir, "designs")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	live := "# Design 28\n\n- **Status**: accepted. Replaces the superseded design 09.\n\n" +
		"The limiter is conservative: it cuts early, never late.\n"
	if err := os.WriteFile(filepath.Join(sub, "28-live.md"), []byte(live), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "29-other.md"), []byte("# Design 29\n\nNothing.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	found := false
	for path, text := range scanned(t, dir) {
		for _, v := range scanBody(path, text) {
			if v.rule == "cuts-early-guarantee" {
				found = true
			}
		}
	}
	if !found {
		t.Error("a LIVE document whose head mentions a supersession left the corpus silently. " +
			"That is the architecture.md defect reached through a different trigger word, and " +
			"the second document in the fixture means the empty-corpus guard cannot catch it")
	}
}

// TestNoSupersededGuaranteeInAnAuthoritativeBody is the gate BLOCKER 8 asked for.
func TestNoSupersededGuaranteeInAnAuthoritativeBody(t *testing.T) {
	for path, text := range corpus(t) {
		for _, v := range scanBody(path, text) {
			t.Errorf("%s [%s]\n    %s\n    → %s", path, v.rule, v.block, v.why)
		}
	}
}

// ruleFixtures is written INDEPENDENTLY of the rules above: one sentence per
// rule that must be caught. It exists because a Codex review mutation-checked
// this gate and found the hole it was built to close — deleting a rule from the
// production slice deleted its own check and the suite stayed green, which is
// exactly the defect the same reviewer had already found in design 03's emitter
// registry. Cases derived from production data cannot pin production data.
//
// A deleted rule leaves its fixture uncaught. A weakened regex stops matching.
// Either fails here.
var ruleFixtures = map[string]string{
	"blanket-approval-claim":    "The design phase is complete — 27/27 approved, every one through independent critique.",
	"superseded-research-note":  "See `agentgateway-2.2-2026-08.md` for the OTLP claims.",
	"nonexistent-release":       "The compiler targets agentgateway 2.2 resources.",
	"cuts-early-guarantee":      "The limiter is conservative: it cuts early, never late.",
	"exact-usd-tier":            "The exact tier is the receipt backstop.",
	"nonexistent-tracing-field": "Receipts are configured through frontendPolicies.",
	"superseded-adr":            "Compilation follows ADR-0020's ordering.",
	"egress-as-restriction":     "The allowlist compiles to a Backend restriction.",
	"header-tool-filter":        "Tools are filtered by matching the Mcp-Name header.",
	"overshoot-bound":           "Status publishes a measured overshoot bound per replica.",
	"pricing-damage-bounded":    "A tampered table is safe because the backstop bounds the damage.",
	"in-worker-poll":            "The acceptance poll runs on the reconcile worker for up to 30s.",
	"allowlist-equality":        "The emitted Backend's provider set equals the allowlist.",
	"spec-only-hash":            "The revision digest is computed from spec alone.",
	"env-referent-hash":         "Env sources are hashed by referent, not contents.",
	"seal-not-copy":             "Every referenced source is sealed in place, and no bytes are copied.",
	"bare-revision-gate":        "The gate controller promotes when evalStatus.revision == the candidate.",
	"referent-drift-refusal":    "If the referent's content has moved, rollback is refused naming both digests.",
	"verifier-undecided":        "Ship a Sigstore policy-controller or a Kyverno verifyImages binding.",
	"inert-tightening":          "A tightening update must make the dependent routes inert first.",
}

// livePhrasingFixtures are the exact sentences that were sitting in an
// authoritative body while this gate reported green. They are kept verbatim.
//
// A rule written from a reviewer's paraphrase catches the paraphrase. Both of
// these state a withdrawn A20 guarantee in the words design 02 actually used —
// "spec only" in the numbered rule, "by referent, not contents" in the very
// classification table an implementer copies from — and the pre-r6 pattern
// matched neither. A rule must be pinned by how the corpus says it.
var livePhrasingFixtures = map[string]string{
	// The two that sat in docs/architecture.html's header and footer, kept as the
	// page wrote them. The body of both documents had been corrected; these had
	// not, so the page read as current to anyone who did not scroll to §18.
	"blanket-approval-claim": "<span><b>status</b> 27/27 designs approved · 26 ADRs</span>",
	"spec-only-hash":         "1. Spec change → `revisionHash(spec)` — **spec only**; the card digest is status.",
	"env-referent-hash":      "| `runtime.env`, `runtime.envFrom` (by **referent**, not contents) | `runtime.port` |\n|---|---|\n| a | b |",
}

// TestTheLivePhrasingsThatSlippedThroughAreCaught pins r6 MAJOR 5's second half.
func TestTheLivePhrasingsThatSlippedThroughAreCaught(t *testing.T) {
	for name, live := range livePhrasingFixtures {
		if !caught(t, name, live) {
			t.Errorf("rule %q does not catch the phrasing that was live in the corpus:\n    %s", name, live)
		}
	}
}

// evasionFixtures pin the NORMALISATION: real phrasings the raw byte match
// missed — emphasis inside the phrase, a singular where the rule wrote a plural,
// a line wrap. Written against what a reader sees.
var evasionFixtures = map[string]string{
	"in-worker-poll":         "the acceptance **poll** runs for up to 30s",
	"pricing-damage-bounded": "the receipt `backstop bound` covers a tampered table",
	"overshoot-bound":        "status publishes the measured *overshoot* bound",
	"cuts-early-guarantee":   "the gateway\ncuts early, never late",
}

// linkEvasionFixtures pin the constructs a punctuation regex cannot reach. Each
// is a sentence whose RENDERED text states a withdrawn design while its source
// bytes do not contain the banned phrase contiguously.
var linkEvasionFixtures = map[string]string{
	"in-worker-poll":       "the acceptance [poll](https://agentgateway.dev/x) verifies each emitted resource",
	"overshoot-bound":      "status publishes a measured ![overshoot](img.png) bound per replica",
	"cuts-early-guarantee": "the limiter [cuts early][ref], never late",
	"exact-usd-tier":       "the `exact` tier is the receipt backstop",
}

// caught runs a fixture through the ENTIRE production path — a real file on
// disk, discovered by the same walk, truncated at the same amendment heading,
// rendered by the same blocks(), matched by the same scanBody().
//
// It exists because the previous fixtures called normalize() directly. That
// proved the normaliser worked and proved nothing about the gate: production
// split the document into lines BEFORE normalising, so a phrase broken by an
// ordinary Markdown hard wrap was never assembled and never matched, while
// every fixture reported the rule healthy. A fixture that takes a shortcut the
// corpus cannot take is not evidence about the corpus.
func caught(t *testing.T, name, fixture string) bool {
	t.Helper()
	dir := t.TempDir()
	sub := filepath.Join(dir, "designs")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("fixture corpus: %v", err)
	}
	// Named as the onlyIn rules expect, with a body and an amendment log, so the
	// truncation rule is exercised rather than bypassed.
	doc := "# Design 03 — policy compiler\n\nStatus: fixture.\n\n" + fixture +
		"\n\n## 11. Amendment log\n\nA1. history, never scanned.\n"
	if err := os.WriteFile(filepath.Join(sub, "03-policy-compiler.md"), []byte(doc), 0o644); err != nil {
		t.Fatalf("fixture corpus: %v", err)
	}
	for path, text := range scanned(t, dir) {
		for _, v := range scanBody(path, text) {
			if v.rule == name {
				return true
			}
		}
	}
	return false
}

// TestAFixtureInTheAmendmentLogIsNotScanned pins the truncation the fixture
// harness relies on. Without it, caught() could report a rule healthy because
// it matched history rather than the authoritative body.
func TestAFixtureInTheAmendmentLogIsNotScanned(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "designs")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	doc := "# Design 03\n\nBody.\n\n## 11. Amendment log\n\nThe acceptance poll runs on the reconcile worker.\n"
	if err := os.WriteFile(filepath.Join(sub, "03-policy-compiler.md"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	for path, text := range scanned(t, dir) {
		if vs := scanBody(path, text); len(vs) != 0 {
			t.Errorf("a withdrawn claim in the AMENDMENT LOG was reported as a live guarantee: %+v", vs)
		}
	}
}

// TestABlockBoundaryDoesNotInventASentence pins the other direction, because a
// renderer that joined everything would be as wrong as one that joined nothing.
// Two adjacent table CELLS, and two lines of CODE, must not be spliced into a
// banned phrase their author never wrote — that would fail the gate on a
// document that says nothing of the kind, and a gate that cries wolf gets its
// rules deleted.
func TestABlockBoundaryDoesNotInventASentence(t *testing.T) {
	cases := map[string]string{
		"code lines": "```go\n// the acceptance\npolling(ctx) // verifies convergence\n```",
	}
	for name, fixture := range cases {
		if caught(t, "in-worker-poll", fixture) {
			t.Errorf("%s: the gate spliced a phrase across a block boundary and reported a claim nobody made", name)
		}
	}
}

// TestARetractionInOneCellDoesNotRescueTheNext pins the table-cell split. A
// two-column table where one side asserts and the other comments is the shape
// design 02's A12 classification table already has, so a row-wide rescue would
// let an unrelated "superseded" spare a live guarantee.
func TestARetractionInOneCellDoesNotRescueTheNext(t *testing.T) {
	row := "| The limiter cuts early, never late | a retracted note about something else |\n|---|---|\n| a | b |"
	if !caught(t, "cuts-early-guarantee", row) {
		t.Error("a retraction word in an ADJACENT CELL rescued a live guarantee; rescue must be per-cell")
	}
}

// TestLinkAndImageSyntaxCannotHideAWithdrawnClaim is the half MAJOR 13 found
// missing: emphasis was stripped, link destinations were not.
func TestLinkAndImageSyntaxCannotHideAWithdrawnClaim(t *testing.T) {
	for name, evasion := range linkEvasionFixtures {
		if !caught(t, name, evasion) {
			t.Errorf("rule %q is bypassed by inline markdown:\n    source:   %s\n    rendered: %s",
				name, evasion, normalize(evasion))
		}
	}
}

// caughtHTML is caught()'s twin for a rendered page: a real .html file on disk,
// discovered by the same walk, rendered by renderHTML and matched by the same
// scanBody. It takes no shortcut the corpus cannot take, for the reason caught()
// gives.
func caughtHTML(t *testing.T, name, page string) bool {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "architecture.html"), []byte(page), 0o644); err != nil {
		t.Fatalf("fixture corpus: %v", err)
	}
	for path, text := range scanned(t, dir) {
		for _, v := range scanBody(path, text) {
			if v.rule == name {
				return true
			}
		}
	}
	return false
}

// htmlPage wraps a fragment in the skeleton a real page has, so the fixture
// exercises the doctype, the head and a dropped <style> rather than a bare
// string the production walk would never see.
func htmlPage(fragment string) string {
	return "<!doctype html>\n<html><head><title>assayd — Architecture v1.0</title>\n" +
		"<style>:root { --bg: #F7F8FA; }</style></head>\n<body>\n" + fragment + "\n</body></html>\n"
}

// htmlFixtures pin that a withdrawn guarantee cannot hide in the RENDERED page.
// Each states its claim in markup the source bytes do not contain contiguously:
// inline emphasis inside the phrase, a soft wrap, an entity, and a link.
// Each tag sits INSIDE the banned phrase, which is the property that makes the
// fixture evidence: the source bytes do not contain the phrase contiguously, so
// a scanner that leaves inline markup in place matches none of them.
var htmlFixtures = map[string]string{
	"cuts-early-guarantee": `<p>The limiter is conservative: it cuts <b>early</b>, never late.</p>`,
	"overshoot-bound":      "<p>Status publishes a measured\novershoot bound per replica.</p>",
	"exact-usd-tier":       `<p>The <code>exact</code> <em>tier</em> &mdash; the receipt backstop.</p>`,
	"superseded-adr":       `<td><a href="decisions/0020-policy-compiler.md">ADR</a>-0020 governs compilation order</td>`,
}

// TestAWithdrawnGuaranteeCannotHideInTheRenderedPage is the gate's HTML half.
//
// It exists because `docs/architecture.html` was outside the corpus entirely:
// the walk returned early on any path not ending in `.md`, so the page carrying
// "27/27 designs approved · 26 ADRs" in its header and footer was never read by
// the gate whose whole job is that sentence. Deleting renderHTML, or narrowing
// the walk back to Markdown, fails here.
func TestAWithdrawnGuaranteeCannotHideInTheRenderedPage(t *testing.T) {
	for name, fragment := range htmlFixtures {
		if !caughtHTML(t, name, htmlPage(fragment)) {
			t.Errorf("rule %q is bypassed by HTML:\n    source:   %s\n    rendered: %q",
				name, fragment, renderHTML(fragment))
		}
	}
}

// TestTheCorrectedADRKeepsItsMarker pins the headRescue in BOTH directions, and
// it is what the whole-directory exemption it replaces could not do.
//
// ADR-0026's Consequences says "the design phase is complete — 27/27 approved…
// Implementation may begin". ADR-0030 withdrew that, and the adr skill forbids
// editing an ADR's body to match a later decision — so the correction lives in
// the Status line and in Amendment 2, and the gate accepts the head in place of
// the edit. Under the old directory-wide exemption, deleting BOTH the marker and
// the amendment left the suite green: the correction this branch made was pinned
// by nothing.
//
// Now: remove the Status marker and the ADR is reported. Remove Amendment 2 and
// this test fails. No ADR body is edited either way.
func TestTheCorrectedADRKeepsItsMarker(t *testing.T) {
	const rel = "docs/decisions/0026-p5-enterprise.md"
	path := filepath.Join(repoRoot, rel)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	text := string(b)

	if !strings.Contains(text, "## Amendment 2") {
		t.Errorf("%s has lost Amendment 2, which is where the withdrawal of its "+
			"\"27/27 approved … Implementation may begin\" consequence is recorded", rel)
	}
	if !headRescued(path, approvalRule(t).headRescue) {
		t.Fatalf("%s no longer carries the correction in its head, so nothing tells a reader "+
			"of the Consequences line that it is withdrawn", rel)
	}

	// The rescue must be doing real work. A rescue that spares a document with
	// nothing to spare is decoration, and would pass whatever the ADR said.
	if reportsApprovalClaim(path, body(text)) {
		t.Error("the head marker did not rescue the ADR, so the gate is demanding an edit " +
			"to a body the adr skill forbids editing")
	}
	if !reportsApprovalClaimWithoutRescue(t, path, body(text)) {
		t.Error("with the head rescue removed the ADR is still not reported, so the rescue " +
			"guards nothing and this test proves nothing")
	}
}

func approvalRule(t *testing.T) rule {
	t.Helper()
	for _, r := range rules {
		if r.name == "blanket-approval-claim" {
			return r
		}
	}
	t.Fatal("the blanket-approval-claim rule is gone")
	return rule{}
}

func reportsApprovalClaim(path, text string) bool {
	for _, v := range scanBody(path, text) {
		if v.rule == "blanket-approval-claim" {
			return true
		}
	}
	return false
}

// reportsApprovalClaimWithoutRescue re-runs the same scan with the head rescue
// disabled, so the test can tell "rescued" from "nothing to rescue".
func reportsApprovalClaimWithoutRescue(t *testing.T, path, text string) bool {
	t.Helper()
	for i := range rules {
		if rules[i].name != "blanket-approval-claim" {
			continue
		}
		saved := rules[i].headRescue
		rules[i].headRescue = nil
		defer func(n int, re *regexp.Regexp) { rules[n].headRescue = re }(i, saved)
		return reportsApprovalClaim(path, text)
	}
	return false
}

// TestTheHeadRescueIsRestrictedToADRs stops the narrow exemption widening back
// into the general one it replaced. A design or a page cannot buy silence with a
// sentence in its header; only an ADR can, because only an ADR's body is text a
// repository rule forbids correcting in place.
func TestTheHeadRescueIsRestrictedToADRs(t *testing.T) {
	dir := t.TempDir()
	for _, sub := range []string{"designs", "decisions"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
		doc := "# Doc\n\n- **Status**: the consequence is withdrawn by ADR-0030.\n\n" +
			"The design phase is complete — 27/27 approved, every one through independent critique.\n"
		if err := os.WriteFile(filepath.Join(dir, sub, "doc.md"), []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	reported := map[string]bool{}
	for path, text := range scanned(t, dir) {
		for _, v := range scanBody(path, text) {
			if v.rule == "blanket-approval-claim" {
				reported[filepath.Base(filepath.Dir(path))] = true
			}
		}
	}
	if !reported["designs"] {
		t.Error("a DESIGN bought silence with a header sentence. The head rescue exists only " +
			"because an ADR's body may not be edited; a design's body can and must be")
	}
	if reported["decisions"] {
		t.Error("the head rescue did not apply to an ADR, so the gate would demand an edit " +
			"the adr skill forbids")
	}
}

// TestTheHeadRescueNamesTheWithdrawal pins the marker's SPECIFICITY, which is a
// separate property from whether the rescue fires at all.
//
// Measured: broadening the pattern to a bare "withdraw" survived the rest of
// this suite. An ADR mentioning any withdrawal anywhere in its first 800 bytes
// would then buy silence for a blanket-approval claim in its body — the
// directory-wide exemption creeping back in a different shape. The rescue must
// name the decision that did the withdrawing.
func TestTheHeadRescueNamesTheWithdrawal(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "decisions")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	// A head that mentions a withdrawal of something ELSE, and a body that makes
	// the blanket claim. Nothing here says the claim below is withdrawn.
	doc := "# ADR-0099: Something else\n" +
		"- **Status**: accepted. An earlier clause about retries was withdrawn.\n\n" +
		"The design phase is complete \u2014 27/27 approved, every one through independent critique.\n"
	if err := os.WriteFile(filepath.Join(sub, "0099-other.md"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	found := false
	for path, text := range scanned(t, dir) {
		for _, v := range scanBody(path, text) {
			if v.rule == "blanket-approval-claim" {
				found = true
			}
		}
	}
	if !found {
		t.Error("an ADR bought silence with an unrelated withdrawal in its head. The rescue " +
			"must name the decision that withdrew THIS claim, or it is a directory-wide " +
			"exemption wearing a regex")
	}
}

// TestACorrectedPillDoesNotRescueTheOneBesideIt pins the narrowed rescue clause
// on blanket-approval-claim, and it is the only thing standing between that
// clause and a plausible tidy-up.
//
// This is the page's own header shape: a `<div class="meta-row">` of `<span>`
// pills. `<span>` is inline, so the whole row renders as ONE block — and the
// corrected pill next door says "superseded in part". Every other rule here
// carries a bare `supersed` in its rescue clause, so adding one back to this
// rule reads like harmonisation and is a silent reopening: the claim this gate
// exists for goes back to passing.
//
// Measured, not theorised. Re-planting the real header sentence beside the real
// corrected one did NOT fail the gate while that clause was broad; it is what
// sent the clause back to explicit retractions only. A later mutation run found
// nothing catching it, which is this test.
func TestACorrectedPillDoesNotRescueTheOneBesideIt(t *testing.T) {
	row := `<div class="meta-row">` +
		`<span><b>status</b> superseded in part · 34 ADRs</span>` + "\n" +
		`<span><b>status</b> 27/27 designs approved · 26 ADRs</span>` +
		`</div>`
	// Vacuity guard, checked at the layer that actually joins: renderHTML emits
	// a line per pill and blocks() is what merges them, because no blank line
	// separates them. If they ever became two blocks the per-block rescue would
	// make this fixture prove nothing, and it would pass for the wrong reason.
	rendered := renderHTML(row)
	var merged string
	for _, b := range blocks(rendered) {
		if strings.Contains(b, "27/27") {
			merged = b
		}
	}
	if !strings.Contains(merged, "superseded in part") {
		t.Fatalf("fixture is vacuous: the two pills are not in one block, so a per-block "+
			"rescue could not reach across them and this proves nothing:\n%q", blocks(rendered))
	}
	if !caughtHTML(t, "blanket-approval-claim", htmlPage(row)) {
		t.Errorf("the corrected pill rescued the claim beside it. The rescue clause on "+
			"blanket-approval-claim must not accept a bare \"supersed\": an inline-only "+
			"container renders as one block, so a retraction anywhere in the row would "+
			"launder every claim in it.\n    rendered: %q", rendered)
	}
}

// TestARenderedCellIsItsOwnBlock is the HTML twin of the Markdown per-cell
// rescue rule, and it is what pins htmlBlockTag. A page's §00 corrections table
// and §20 ADR index are both two-column tables where one side ASSERTS and the
// other COMMENTS — so if a whole row rendered as one block, the word "retracted"
// in the right-hand cell would launder a live guarantee stated in the left.
//
// Deleting htmlBlockTag merges the row and this test fails.
func TestARenderedCellIsItsOwnBlock(t *testing.T) {
	row := `<tr><td>The limiter cuts early, never late</td><td>a retracted note about something else</td></tr>`
	if !caughtHTML(t, "cuts-early-guarantee", htmlPage(row)) {
		t.Error("a retraction word in an ADJACENT CELL rescued a live guarantee; rescue must be per-cell")
	}
}

// TestTheRenderedPageDoesNotInventASentence pins the other direction, because a
// renderer that joined everything would fail the gate on documents nobody wrote,
// and a gate that cries wolf gets its rules deleted.
//
//   - Two cells must not be spliced into a phrase neither contains. Each half
//     here is innocuous alone, which is the whole point: replacing the block
//     boundary with a space fails this.
//   - A base64 `data:` image, a `<style>` body and an `<svg>` plate must
//     contribute no text at all. They are most of this page's bytes, and a
//     `quiesc` or an `ADR-0020` surfacing out of one would be a false report.
func TestTheRenderedPageDoesNotInventASentence(t *testing.T) {
	splice := `<tr><td>the acceptance</td><td>polling verifies convergence</td></tr>`
	if caughtHTML(t, "in-worker-poll", htmlPage(splice)) {
		t.Errorf("two cells were spliced into a claim neither states:\n    rendered: %q", renderHTML(splice))
	}

	noise := `<p>Nothing withdrawn is asserted here.</p>` +
		`<img alt="plate" src="data:image/webp;base64,UklGRpqbAABXRUJQVlA4II6bAACwWgSdASpABkAG">` +
		`<style>.pod { content: "it cuts early, never late"; }</style>` +
		`<svg role="img" aria-label="x"><text>the acceptance poll runs on the reconcile worker</text></svg>`
	if txt := renderHTML(noise); strings.Contains(txt, "UklGRp") || strings.Contains(txt, "cuts early") ||
		strings.Contains(txt, "acceptance poll") {
		t.Errorf("a data: blob, a <style> body or an <svg> plate reached the scanner as prose:\n%q", txt)
	}
	for _, r := range rules {
		if caughtHTML(t, r.name, htmlPage(noise)) {
			t.Errorf("rule %q fired on markup that asserts nothing", r.name)
		}
	}
}

// wrapFixtures pin the SOFT WRAP specifically, and each half is innocuous on
// its own. That property is the whole test: the previous wrap fixture ("the
// gateway\ncuts early, never late") had a second line matching the rule by
// itself, so it passed under a line-splitting scan and reported a capability
// the gate did not have. A wrap fixture that either half satisfies is vacuous.
var wrapFixtures = map[string]string{
	"in-worker-poll":  "the acceptance\npolling verifies convergence",
	"overshoot-bound": "under concurrency the excess\nis bounded by the replica count",
}

// TestAHardWrapCannotHideAWithdrawnClaim is r6 MAJOR 5's first half: Markdown
// joins soft-wrapped lines, so the gate must too.
func TestAHardWrapCannotHideAWithdrawnClaim(t *testing.T) {
	for name, fixture := range wrapFixtures {
		halves := strings.Split(fixture, "\n")
		for _, r := range rules {
			if r.name != name {
				continue
			}
			for _, h := range halves {
				if r.banned.MatchString(normalize(h)) {
					t.Fatalf("wrap fixture %q is vacuous: the half %q matches on its own, so this "+
						"would pass a scan that never joins lines", name, h)
				}
			}
		}
		if !caught(t, name, fixture) {
			t.Errorf("rule %q is bypassed by an ordinary Markdown hard wrap:\n    %q", name, fixture)
		}
	}
}

// TestNormalisationDefeatsFormattingEvasion is the half MAJOR 19 said was missing.
func TestNormalisationDefeatsFormattingEvasion(t *testing.T) {
	for name, evasion := range evasionFixtures {
		if !caught(t, name, evasion) {
			t.Errorf("rule %q misses a formatted phrasing through the production scan:\n    %q", name, evasion)
		}
	}
}

// TestEveryRuleIsPinnedByAnIndependentFixture kills the mutation that deleting a
// production rule survives.
func TestEveryRuleIsPinnedByAnIndependentFixture(t *testing.T) {
	byName := map[string]rule{}
	for _, r := range rules {
		byName[r.name] = r
	}
	for name, fixture := range ruleFixtures {
		r, ok := byName[name]
		if !ok {
			t.Errorf("rule %q has a fixture but no rule — deleting a rule must fail here, not pass silently", name)
			continue
		}
		if !caught(t, name, fixture) {
			t.Errorf("rule %q no longer catches its own fixture through the production scan:\n    %s\n    the pattern was weakened", name, fixture)
		}
		if r.allowed != nil && r.allowed.MatchString(fixture) {
			t.Errorf("rule %q rescues its own fixture — the retraction clause is too broad to catch an assertion", name)
		}
	}
	for _, r := range rules {
		if _, ok := ruleFixtures[r.name]; !ok {
			t.Errorf("rule %q has no independent fixture; add one so its deletion is detectable", r.name)
		}
	}
}

// TestSupersededNoteSaysSo pins the positive half: the note stays for the
// reviews that cite it, and must announce that it is not to be cited.
func TestSupersededNoteSaysSo(t *testing.T) {
	p := filepath.Join(docsRoot, "research", supersededNote)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("the superseded note must remain — seven review documents cite it: %v", err)
	}
	head := string(b)
	if i := strings.Index(head, "\n\n"); i > 0 {
		head = head[:i]
	}
	if !strings.Contains(head, "SUPERSEDED") || !strings.Contains(head, "DO NOT CITE") {
		t.Errorf("%s must open with SUPERSEDED and DO NOT CITE; a stale note that reads live is how this corpus encoded a release that does not exist", p)
	}
}
