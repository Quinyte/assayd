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
// Scope is deliberate — it covers what an implementer builds FROM, and nothing
// whose job is to record or refute:
//
//   - Only the AUTHORITATIVE BODY is scanned — everything before a design's
//     amendment section. Amendment logs must be free to quote what they retract.
//   - docs/designs/reviews/** is never scanned. Reviews are history and are never
//     edited to match a later decision (write-spec).
//   - docs/research/** is never scanned. A research note is evidence, and the
//     note that supersedes another must quote every claim it refutes.
//   - A document already marked superseded is never scanned. An ADR's content is
//     frozen when it is superseded, by the rule in the adr skill.
//   - The superseded research note is not scanned but IS required to say so.
//
// Each rule bans an ASSERTION and permits a RETRACTION, because "X is withdrawn"
// is exactly the sentence a correction needs to make.
package docs

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const docsRoot = "../../docs"

// supersededNote is the research note that named a release which does not exist.
const supersededNote = "agentgateway-2.2-2026-08.md"

type rule struct {
	name string
	// onlyIn restricts a rule to one document. Used where a phrase is stale in
	// one design and correct elsewhere.
	onlyIn string
	banned *regexp.Regexp
	// allowed rescues a line that mentions the banned thing in order to retract
	// it. Nil means the mention is banned outright.
	allowed *regexp.Regexp
	why     string
}

var rules = []rule{
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
		banned:  regexp.MustCompile(`(?i)overshoot bound|measured overshoot|excess is bounded`),
		allowed: regexp.MustCompile(`(?i)withdraw|retract|no bound|unbounded|refuted|is false|supersed`),
		why:     "the spike measured 100x a one-replica budget under concurrency; no bound is published (design 03 A13)",
	},
	{
		name:    "pricing-damage-bounded",
		banned:  regexp.MustCompile(`(?i)backstop bounds|bounds the damage|damage is bounded`),
		allowed: regexp.MustCompile(`(?i)withdraw|retract|not exact|no longer|supersed`),
		why:     "the receipt tier prices from the same table it is meant to backstop; it bounds nothing (ADR-0028)",
	},
	{
		name:    "in-worker-poll",
		banned:  regexp.MustCompile("(?i)acceptance poll|poll each resource|bounded: 30s|on the reconcile worker"),
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
		name:    "spec-only-hash",
		banned:  regexp.MustCompile(`(?i)computed from spec alone|hashed by referent`),
		allowed: regexp.MustCompile(`(?i)no longer|earlier|retract|supersed|was never|A20`),
		why:     "A20 hashes env sources by content, so the digest is no longer spec-only",
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

// isFrozen reports whether the document announces its own supersession up front.
func isFrozen(content string) bool {
	head := content
	if len(head) > 800 {
		head = head[:800]
	}
	return frozenMarker.MatchString(head)
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

func scanned(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(docsRoot, func(path string, info os.FileInfo, err error) error {
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
		if !strings.HasSuffix(path, ".md") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if isFrozen(string(b)) {
			return nil
		}
		out[path] = body(string(b))
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", docsRoot, err)
	}
	if len(out) == 0 {
		t.Fatalf("scanned no documents under %s — the gate would pass vacuously", docsRoot)
	}
	return out
}

// TestNoSupersededGuaranteeInAnAuthoritativeBody is the gate BLOCKER 8 asked for.
func TestNoSupersededGuaranteeInAnAuthoritativeBody(t *testing.T) {
	for path, text := range scanned(t) {
		for _, line := range strings.Split(text, "\n") {
			for _, r := range rules {
				if r.onlyIn != "" && filepath.Base(path) != r.onlyIn {
					continue
				}
				if supersedes(path, r.name) {
					continue // a document is allowed to name what it supersedes
				}
				if !r.banned.MatchString(line) {
					continue
				}
				if r.allowed != nil && r.allowed.MatchString(line) {
					continue // a retraction, which is what a correction must say
				}
				t.Errorf("%s [%s]\n    %s\n    → %s", path, r.name, strings.TrimSpace(line), r.why)
			}
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
		if !r.banned.MatchString(fixture) {
			t.Errorf("rule %q no longer catches its own fixture:\n    %s\n    the pattern was weakened", name, fixture)
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
