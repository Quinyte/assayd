// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package refgen

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// repoRoot is where the generator reads its inputs from. The tests run from
// internal/refgen.
const repoRoot = "../.."

// The gate that makes the generated reference honest.
//
// The three prose answers per reason live in reasons.yaml, and nothing in Go
// forces them to exist. This test is what forces it: add a reason to the
// operator without describing it and this fails, so `make unit` fails, so CI
// fails — before `make verify` ever runs the generator. Delete an entry whose
// reason is still set and it fails the same way.
func TestEveryReasonTheOperatorCanSetIsDescribed(t *testing.T) {
	v := vocabulary(t)
	if _, err := loadAnnotations(v); err != nil {
		t.Fatalf("the reference's prose and the operator's code have parted company:\n%v", err)
	}
}

// A reason the extractor cannot fold is a hole in the published vocabulary: the
// page would describe every reason but that one, and say nothing about the gap
// unless someone read the honesty note. The extractor reports such sites rather
// than dropping them; this holds the count at zero, so that a new call site
// shaped in a way the resolver cannot follow is a test failure and not a silent
// omission.
func TestTheExtractorFoldsEveryConditionWrite(t *testing.T) {
	v := vocabulary(t)
	var bad []string
	for _, s := range v.Sites {
		if s.Resolution == Unresolved {
			bad = append(bad, s.Condition+" at "+s.File+" reason "+s.Expr)
		}
	}
	if len(bad) > 0 {
		t.Fatalf("%d condition write(s) have a reason the generator could not fold, so the "+
			"published vocabulary is incomplete by exactly those rows:\n  %s",
			len(bad), strings.Join(bad, "\n  "))
	}
	if len(v.Sites) == 0 {
		t.Fatal("no condition writes found at all; the extractor is not looking where it thinks it is")
	}
}

// Reasons are resolved by VARIABLE, not by name.
//
// reconcileRuntime declares a local called `reason` more than once, in separate
// blocks, for unrelated causes. An earlier draft resolved by name and unioned
// them, which published MaterialUnavailable as something PolicyApplyIncomplete
// could carry and RouteApplyFailed as something RevisionMaterialUnavailable
// could — a reference naming a plausible cause that was never checked, which is
// the failure AGENTS.md rule 8 names, one level up from the operator.
//
// This asserts the narrow set, so widening it fails rather than passing quietly.
func TestReasonsResolveByVariableAndNotByName(t *testing.T) {
	v := vocabulary(t)
	got := reasonsOf(t, v, "RevisionMaterialUnavailable")
	want := map[string]bool{"MaterialUnavailable": true, "MaterialInvalid": true}
	if len(got) != len(want) {
		t.Fatalf("RevisionMaterialUnavailable carries %v; it is written from one local whose only "+
			"assignments are MaterialUnavailable and MaterialInvalid", got)
	}
	for _, r := range got {
		if !want[r] {
			t.Errorf("RevisionMaterialUnavailable must not carry %q: that reason belongs to a "+
				"different local of the same name in the same function", r)
		}
	}
}

// A struct-field reason is narrowed through the function that BUILT the value.
//
// PolicyCompileFailed's reason arrives as `fails[0].reason`, a failure.reason
// like any other. Answering it from every write to failure.reason anywhere in
// the package would say this condition can carry ServingRouteNotAccepted, which
// it cannot; answering it from compileFailures, which built the slice, gives
// the three it really can.
func TestAFieldReasonIsNarrowedThroughItsProducer(t *testing.T) {
	v := vocabulary(t)
	got := reasonsOf(t, v, "PolicyCompileFailed")
	want := map[string]bool{
		"AuthInputAbsent": true, "AuthTransitionNotBuilt": true, "ConcernNotBuilt": true,
	}
	if len(got) != len(want) {
		t.Fatalf("PolicyCompileFailed carries %v; compileFailures builds exactly %d", got, len(want))
	}
	for _, r := range got {
		if !want[r] {
			t.Errorf("PolicyCompileFailed must not carry %q: nothing compileFailures builds "+
				"carries that reason", r)
		}
	}
}

// The test-reference scan is what the "no test names this reason" list rests
// on, and an over-eager matcher would shrink that list and make the page claim
// coverage it does not have.
//
// Several reasons CONTAIN another as a substring — `EnvSourceUnresolved`
// contains `Unresolved` — so a matcher without word boundaries credits the
// inner one everywhere the outer one is mentioned. The first version of this
// test only counted referenced-versus-unreferenced, and the independent review
// removed the boundaries and still got `ok`, with the headline count moving
// 17 → 14 underneath it. So this now drives the matcher over a SYNTHETIC corpus
// that names only the outer reasons, where the inner one being credited is
// unambiguous.
func TestTheTestReferenceScanMatchesOnWordBoundaries(t *testing.T) {
	v := vocabulary(t)

	var inner, outer []string
	seen := map[string]bool{}
	for a := range v.ReasonsByName {
		for b := range v.ReasonsByName {
			if a != b && strings.Contains(b, a) && !seen[a] {
				seen[a] = true
				inner = append(inner, a)
				outer = append(outer, b)
			}
		}
	}
	if len(inner) == 0 {
		t.Fatal("no reason contains another as a substring, so this test proves nothing; if the " +
			"vocabulary really has no such pair, delete it rather than leaving it passing vacuously")
	}

	root := t.TempDir()
	dir := filepath.Join(root, "test")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var corpus strings.Builder
	corpus.WriteString("package fixture\n\n// Names only the OUTER reasons.\nvar _ = []string{\n")
	for _, o := range outer {
		corpus.WriteString("\t\"" + o + "\",\n")
	}
	corpus.WriteString("}\n")
	if err := os.WriteFile(filepath.Join(dir, "fixture_test.go"), []byte(corpus.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	refs, err := ScanTestReferences(root, v)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	for i, in := range inner {
		if n := len(refs[in].Files); n != 0 {
			t.Errorf("%q was credited to %d file(s) by a corpus that names only %q — the matcher is "+
				"matching a substring, so the unpinned list is shorter than the truth",
				in, n, outer[i])
		}
	}
	for _, o := range outer {
		if len(refs[o].Files) == 0 {
			t.Errorf("%q was NOT credited by a corpus that names it; the matcher matches nothing", o)
		}
	}

	// And over the real repository, both halves must be non-empty: all-referenced
	// means the matcher matches anything, none-referenced means it matches
	// nothing, and either way the published list is worthless.
	real, err := ScanTestReferences(repoRoot, v)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(real) != len(v.ReasonsByName) {
		t.Fatalf("scanned %d reasons, the operator can set %d", len(real), len(v.ReasonsByName))
	}
	var referenced, unreferenced int
	for _, tr := range real {
		if len(tr.Files) > 0 {
			referenced++
		} else {
			unreferenced++
		}
	}
	if referenced == 0 || unreferenced == 0 {
		t.Fatalf("the scan found %d referenced and %d unreferenced reasons; one of those being zero "+
			"means the matcher is broken, not that the repository changed", referenced, unreferenced)
	}
}

// Every in-page link this generator emits must land, and two headings must not
// slug alike. The first version shipped 51 dead anchors — 23 index entries for
// condition types it gave no section, and 28 chart keys slugged by an algorithm
// no renderer uses — so the check now runs inside write() and this pins that it
// really refuses.
func TestABrokenAnchorIsRefusedRatherThanWritten(t *testing.T) {
	if err := checkAnchors("x.md", "## A heading\n\n[ok](#a-heading)\n"); err != nil {
		t.Fatalf("a link that lands was refused: %v", err)
	}
	if err := checkAnchors("x.md", "## A heading\n\n[dead](#no-such-thing)\n"); err == nil {
		t.Error("a link to an anchor the page does not have was accepted")
	}
	// github-slugger drops dots and backticks rather than turning them into
	// hyphens: `gateway.enabled` is #gatewayenabled, never #gateway-enabled.
	if got := anchor("gateway.enabled"); got != "gatewayenabled" {
		t.Errorf("anchor(\"gateway.enabled\") = %q, want %q — links in the chart index are dead "+
			"on GitHub with any other answer", got, "gatewayenabled")
	}
	if err := checkAnchors("x.md", "## Same\n\n## Same\n"); err == nil {
		t.Error("two headings that slug alike were accepted; every link to the second lands on the first")
	}
}

// Nothing about the machine that ran the generator may reach a public page.
//
// Generators embed source paths by habit, and this repository's reference is
// published. The check is on the OUTPUT, not on the generator, because that is
// where the leak would appear.
func TestTheGeneratedReferenceCarriesNoLocalPath(t *testing.T) {
	// The home-directory names are alternated rather than written out one by
	// one, so that this line does not itself match the repository's own "no
	// absolute path in a committed file" grep and turn the guard into noise.
	leak := regexp.MustCompile(`(?i)/(Users|home|private|var/folders)/`)
	dir := filepath.Join(repoRoot, "docs", "reference")
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v — run `make reference`", dir, err)
	}
	var seen int
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		seen++
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			if leak.MatchString(line) {
				t.Errorf("%s:%d carries a filesystem path from the machine that generated it:\n  %s",
					e.Name(), i+1, strings.TrimSpace(line))
			}
		}
	}
	if seen < 3 {
		t.Fatalf("found %d generated page(s) under docs/reference; expected the three the "+
			"generator writes", seen)
	}
}

// Two generations of unchanged inputs must be byte-identical, or `make verify`
// fails for whoever runs it second and the drift check becomes noise people
// learn to re-run until it passes.
func TestGenerationIsDeterministic(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	for _, dir := range []string{a, b} {
		if err := renderValues(repoRoot, dir); err != nil {
			t.Fatalf("render values: %v", err)
		}
		if err := renderConditions(repoRoot, dir); err != nil {
			t.Fatalf("render conditions: %v", err)
		}
	}
	for _, name := range []string{"helm-values.md", "conditions.md"} {
		x, err := os.ReadFile(filepath.Join(a, name))
		if err != nil {
			t.Fatal(err)
		}
		y, err := os.ReadFile(filepath.Join(b, name))
		if err != nil {
			t.Fatal(err)
		}
		if string(x) != string(y) {
			t.Errorf("%s differs between two generations of the same inputs", name)
		}
	}
}

// The committed pages must equal a fresh generation — the same property `make
// verify` enforces, asserted here too so that `make unit` catches it without a
// working crdoc install. crd-agent.md is not covered: it needs the pinned
// binary, and `make verify` is where that is checked.
func TestTheCommittedReferenceMatchesAFreshGeneration(t *testing.T) {
	dir := t.TempDir()
	if err := renderValues(repoRoot, dir); err != nil {
		t.Fatalf("render values: %v", err)
	}
	if err := renderConditions(repoRoot, dir); err != nil {
		t.Fatalf("render conditions: %v", err)
	}
	for _, name := range []string{"helm-values.md", "conditions.md"} {
		fresh, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		committed, err := os.ReadFile(filepath.Join(repoRoot, "docs", "reference", name))
		if err != nil {
			t.Fatalf("read the committed %s: %v — run `make reference`", name, err)
		}
		if string(fresh) != string(committed) {
			t.Errorf("docs/reference/%s is stale: it differs from what the generator writes today. "+
				"Run `make reference` and commit.", name)
		}
	}
}

// A generator nobody runs is worse than no generator: its output reads as
// current and is not. This holds the WIRING — not the generator — so that
// removing `make reference` from `make verify`, or dropping docs/reference from
// the paths it compares, fails a test rather than quietly turning the drift
// check off.
func TestTheDriftCheckIsWiredIntoVerify(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(repoRoot, "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	mk := string(b)
	verify := mk[strings.Index(mk, "\nverify:"):]
	if i := strings.Index(verify, "\n## "); i > 0 {
		verify = verify[:i]
	}

	// RECIPE LINES ONLY. The first version of this test asked whether the
	// verify block CONTAINED the string "docs/reference", and a comment inside
	// that block says it — so deleting docs/reference from the real argument
	// list left the test green, and the independent review ran exactly that
	// mutation and got `ok`. A comment is not a check.
	var recipe []string
	for _, line := range strings.Split(verify, "\n") {
		if strings.HasPrefix(line, "\t") && !strings.HasPrefix(strings.TrimSpace(line), "#") {
			recipe = append(recipe, line)
		}
	}
	body := strings.Join(recipe, "\n")
	if len(recipe) == 0 {
		t.Fatal("no recipe lines found under `verify:`; this test is reading the wrong block")
	}
	if !strings.Contains(body, "$(MAKE) reference") {
		t.Error("no recipe line of `make verify` regenerates the reference, so a stale " +
			"docs/reference/ would pass CI")
	}
	// The path must be in the argument list of the porcelain comparison, not
	// merely somewhere in the block. crd-agent.md is covered by NOTHING else:
	// TestTheCommittedReferenceMatchesAFreshGeneration excludes it by design,
	// so without this argument a hand-edited CRD reference reaches main.
	compare := regexp.MustCompile(`(?m)^\t.*--porcelain\b[^\n]*\bdocs/reference\b`)
	if !compare.MatchString(body) {
		t.Errorf("`make verify` does not pass docs/reference to its --porcelain comparison, so a "+
			"reference that regenerated differently — crd-agent.md included, which nothing else "+
			"checks — would pass CI. Recipe:\n%s", body)
	}
	if !strings.Contains(mk, "\nreference:") {
		t.Error("there is no `make reference` target")
	}

	// An EXACT version, not a column position. The first version of this test
	// asked for the literal "CRDOC_VERSION            ?= " — twelve spaces — so
	// `CRDOC_VERSION            ?= latest` passed, which is verbatim the failure
	// its own message describes, and a correctly pinned but re-aligned
	// `CRDOC_VERSION ?= v0.6.4` failed with a message that was then false.
	pinned := regexp.MustCompile(`(?m)^CRDOC_VERSION\s*\?=\s*v\d+\.\d+\.\d+\s*$`)
	if !pinned.MatchString(mk) {
		t.Error("crdoc is not pinned to an exact vMAJOR.MINOR.PATCH; a generator whose output " +
			"depends on when it ran makes `make verify` fail for whoever picks up a new version first")
	}
}

// Two honesty mechanisms shipped WRITE-ONLY in the first version: the count of
// conditions re-asserted from an earlier pass, and the mark on a struct reason
// field whose writes did not all fold. Both were computed, neither was
// rendered, and the package doc promised both. The independent review found
// them. These pin the rendering, so deleting it fails rather than quietly
// restoring a page that over-claims.
func TestTheHonestyMechanismsAreRendered(t *testing.T) {
	v := vocabulary(t)
	if v.CarrySites == 0 {
		t.Fatal("no conditionSet.carry sites found; the extractor counts them and there are some, " +
			"so the counter is broken")
	}
	page := conditionsPage(t)
	if !strings.Contains(page, "re-assert a condition an EARLIER pass stored") {
		t.Error("the page does not say that some conditions are re-asserted rather than decided, " +
			"though the generator counts those sites")
	}

	// The open-field mark: pinned on the renderer, because producing a genuinely
	// open field needs a change in internal/controller that this test cannot
	// make. What is asserted is that an open row READS differently from a closed
	// one — so the mark cannot be silently dropped.
	open := resolutionNote(Site{Resolution: ResolvedField, Expr: "w.reason",
		Reasons: []string{"A", "B"}, FieldOpen: true})
	closed := resolutionNote(Site{Resolution: ResolvedField, Expr: "w.reason",
		Reasons: []string{"A", "B"}})
	if open == closed {
		t.Error("a call site whose reason field has unfolded writes reads exactly like one whose " +
			"set is closed, so the page presents a partial set as the whole one")
	}
	if !strings.Contains(open, "NOT necessarily all") {
		t.Errorf("an open field row does not say its set may be incomplete: %q", open)
	}
}

// The front matter of every committed page must PARSE, and carry a title.
//
// The first attempt at front matter emitted bare scalars, and one description
// contains ": " — YAML reads that as a nested mapping key. `make reference`,
// `make verify` and `make unit` were all green on a page no site could load,
// because reproducible invalid YAML is still reproducible; the only thing that
// caught it was a docs-site build in another repository. Nothing in this one
// had ever parsed what it wrote.
func TestTheFrontMatterOfEveryPageIsValidYAML(t *testing.T) {
	dir := filepath.Join(repoRoot, "docs", "reference")
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v — run `make reference`", dir, err)
	}
	var seen int
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		seen++
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		fm, err := FrontMatter(string(b))
		if err != nil {
			t.Errorf("%s: %v", e.Name(), err)
			continue
		}
		title, _ := fm["title"].(string)
		if strings.TrimSpace(title) == "" {
			t.Errorf("%s has no `title`, which is the one field an Astro content collection requires",
				e.Name())
		}
	}
	if seen < 3 {
		t.Fatalf("found %d page(s) under docs/reference; expected the three the generator writes", seen)
	}

	// And the emitter must survive the characters that broke it, plus the ones
	// that would break it next.
	for _, s := range []string{
		`a: b`, `he said "hi"`, `back\slash`, "line\nbreak", `#hash`, `- dash`, `{brace}`, `[bracket]`,
	} {
		page := frontMatter(s, s) + "\nbody\n"
		fm, err := FrontMatter(page)
		if err != nil {
			t.Errorf("front matter with title %q does not parse: %v", s, err)
			continue
		}
		if got, _ := fm["title"].(string); got != s {
			t.Errorf("title round-tripped as %q, want %q", got, s)
		}
	}
}

// The CRD page must carry every rule UNDER THE NODE IT BELONGS TO.
//
// Agent.spec.llm.fallback and Agent.spec.llm.providers[index] are the same Go
// struct, so their rules are byte-identical. A gate that asked whether a rule
// appeared anywhere on the page passed with every `providers` node deleted —
// the twin's text satisfied it — and the page then announced a rule count that
// was false about the CRD. That duplication is what hid the original blocker,
// so this drives the mutation that defeated the first gate.
func TestARuleMissingFromItsOwnNodeIsRefused(t *testing.T) {
	src := filepath.Join(repoRoot, CRDSource)
	rules, err := celRules(src)
	if err != nil {
		t.Fatalf("read the CRD: %v", err)
	}
	if len(rules) == 0 {
		t.Fatal("no CEL rules found in the CRD; this test would pass by having nothing to check")
	}

	page, err := os.ReadFile(filepath.Join(repoRoot, "docs", "reference", "crd-agent.md"))
	if err != nil {
		t.Fatalf("read the committed page: %v — run `make reference`", err)
	}
	if err := verifyEveryRuleIsPublished(string(page), src); err != nil {
		t.Fatalf("the committed CRD reference does not publish every rule under its own node:\n%v", err)
	}

	// Delete one node's section and the gate must object, even though its twin
	// still carries identical text elsewhere on the page.
	var twin string
	for _, r := range rules {
		if strings.Contains(r.Node, "providers") {
			twin = r.Node
			break
		}
	}
	if twin == "" {
		t.Skip("no providers node in this CRD; the duplication this guards is not present")
	}
	cut := dropSection(string(page), celHeading(twin))
	if cut == string(page) {
		t.Fatalf("could not find %q to remove; this test is not testing what it says", celHeading(twin))
	}
	if err := verifyEveryRuleIsPublished(cut, src); err == nil {
		t.Errorf("a page with %s removed entirely passed the gate — its rules are byte-identical to "+
			"another node's, which is exactly how the original omission hid", twin)
	}
}

// dropSection removes one `### ` section, heading and body.
func dropSection(page, heading string) string {
	i := strings.Index(page, heading)
	if i < 0 {
		return page
	}
	rest := page[i+len(heading):]
	end := len(rest)
	for _, mark := range []string{"\n### ", "\n## ", "\n# "} {
		if j := strings.Index(rest, mark); j >= 0 && j < end {
			end = j
		}
	}
	return page[:i] + rest[end:]
}

func conditionsPage(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := renderConditions(repoRoot, dir); err != nil {
		t.Fatalf("render conditions: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "conditions.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The extraction type-checks two packages, which is a second or so normally and
// well over ten under -race. Six tests wanted it, and re-running it six times
// put `make race` up by two minutes for no extra coverage: every caller treats
// the result as read-only.
var (
	vocabOnce sync.Once
	vocabVal  *Vocabulary
	vocabErr  error
)

func vocabulary(t *testing.T) *Vocabulary {
	t.Helper()
	vocabOnce.Do(func() { vocabVal, vocabErr = ExtractVocabulary(repoRoot) })
	if vocabErr != nil {
		t.Fatalf("extract: %v", vocabErr)
	}
	return vocabVal
}

func reasonsOf(t *testing.T, v *Vocabulary, condition string) []string {
	t.Helper()
	for _, c := range v.Conditions {
		if c.Name == condition {
			if len(c.Reasons) == 0 {
				t.Fatalf("%s has no reasons; the extractor found no write of it", condition)
			}
			return c.Reasons
		}
	}
	t.Fatalf("%s is not a declared condition type", condition)
	return nil
}
