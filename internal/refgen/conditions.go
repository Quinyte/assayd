// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package refgen

import (
	_ "embed"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	yaml "go.yaml.in/yaml/v3"
)

// reasonsYAML carries the three answers no compiler knows.
//
//go:embed reasons.yaml
var reasonsYAML []byte

// AnnotationsSource is where a reader is sent to change the annotated prose.
const AnnotationsSource = "internal/refgen/reasons.yaml"

// Traffic says whether an Agent's serving traffic stops.
type Traffic string

const (
	// TrafficWithdrawn — the operator removes or does not publish the serving
	// route, strips its backendRefs, or takes the revision's weight to zero, so
	// requests stop reaching the agent.
	TrafficWithdrawn Traffic = "withdrawn"
	// TrafficKept — the route goes on serving; only status changes.
	TrafficKept Traffic = "not-withdrawn"
	// TrafficDepends — the answer was established and differs by arm: the same
	// reason is raised from two states, and traffic stops in one and not the
	// other. It is a separate value from `unknown` because collapsing "it
	// depends, here is how" into "not established" would throw away an answer
	// that was actually found.
	TrafficDepends Traffic = "depends"
	// TrafficUnknown — not established from the code. It is a permitted answer
	// on purpose. AGENTS.md rule 8 is about a condition naming a plausible cause
	// that was never checked; a reference that guessed here would do the same
	// thing one level up.
	TrafficUnknown Traffic = "unknown"
)

// Annotation is the curated half of one reason's entry.
type Annotation struct {
	Reason   string  `yaml:"reason"`
	State    string  `yaml:"state"`
	Operator string  `yaml:"operator"`
	Traffic  Traffic `yaml:"traffic"`
	// Disagreement records a place where the code and a design document say
	// different things about this reason. It is REPORTED, never reconciled:
	// picking a side in a generated document would hide the conflict from the
	// people whose job it is to settle it.
	Disagreement string `yaml:"disagreement,omitempty"`
	Note         string `yaml:"note,omitempty"`
}

type annotations struct {
	Reasons []Annotation `yaml:"reasons"`
}

// loadAnnotations reads reasons.yaml and holds it to the code.
//
// This is the join that keeps the prose honest. A reason added to the operator
// with no entry here fails generation, so `make verify` fails, so it cannot
// reach main undocumented. An entry whose reason no longer exists fails too, so
// the page cannot describe behaviour that has been deleted — which is exactly
// how docs/architecture.md and docs/architecture.html came apart.
func loadAnnotations(v *Vocabulary) (map[string]Annotation, error) {
	var a annotations
	if err := yaml.Unmarshal(reasonsYAML, &a); err != nil {
		return nil, fmt.Errorf("parse %s: %w", AnnotationsSource, err)
	}
	byReason := map[string]Annotation{}
	var dupes []string
	for _, e := range a.Reasons {
		if _, seen := byReason[e.Reason]; seen {
			dupes = append(dupes, e.Reason)
		}
		byReason[e.Reason] = e
	}
	var missing, extra, bad []string
	for reason := range v.ReasonsByName {
		e, ok := byReason[reason]
		if !ok {
			missing = append(missing, reason)
			continue
		}
		switch {
		case strings.TrimSpace(e.State) == "":
			bad = append(bad, reason+": no `state`")
		case strings.TrimSpace(e.Operator) == "":
			bad = append(bad, reason+": no `operator`")
		case e.Traffic != TrafficWithdrawn && e.Traffic != TrafficKept &&
			e.Traffic != TrafficDepends && e.Traffic != TrafficUnknown:
			bad = append(bad, fmt.Sprintf("%s: `traffic` is %q, not one of "+
				"withdrawn/not-withdrawn/depends/unknown", reason, e.Traffic))
		case e.Traffic == TrafficDepends && strings.TrimSpace(e.Note) == "":
			bad = append(bad, reason+": `traffic: depends` needs a `note` saying which arm is which")
		}
	}
	for reason := range byReason {
		if _, ok := v.ReasonsByName[reason]; !ok {
			extra = append(extra, reason)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	sort.Strings(bad)
	sort.Strings(dupes)

	var problems []string
	if len(missing) > 0 {
		problems = append(problems, fmt.Sprintf("the operator can set %d reason(s) that %s does not "+
			"describe — add an entry with `state`, `operator` and `traffic` for each:\n    %s",
			len(missing), AnnotationsSource, strings.Join(missing, "\n    ")))
	}
	if len(extra) > 0 {
		problems = append(problems, fmt.Sprintf("%s describes %d reason(s) no code path sets — delete "+
			"them, or restore the code that set them:\n    %s",
			AnnotationsSource, len(extra), strings.Join(extra, "\n    ")))
	}
	if len(dupes) > 0 {
		problems = append(problems, "duplicate entries in "+AnnotationsSource+": "+strings.Join(dupes, ", "))
	}
	if len(bad) > 0 {
		problems = append(problems, "incomplete entries in "+AnnotationsSource+":\n    "+strings.Join(bad, "\n    "))
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("%s and internal/controller disagree:\n\n  %s",
			AnnotationsSource, strings.Join(problems, "\n\n  "))
	}
	return byReason, nil
}

func renderConditions(root, out string) error {
	v, err := ExtractVocabulary(root)
	if err != nil {
		return err
	}
	ann, err := loadAnnotations(v)
	if err != nil {
		return err
	}
	refs, err := ScanTestReferences(root, v)
	if err != nil {
		return err
	}
	constOf := map[string]ReasonConst{}
	for _, rc := range v.ReasonConsts {
		constOf[rc.Value] = rc
	}

	var sb strings.Builder
	sb.WriteString(frontMatter("Conditions and reasons",
		"Every condition the assayd operator writes and every reason string it can set, with what "+
			"produces it, what the operator does, and whether traffic is withdrawn."))
	sb.WriteString(generatedNotice())
	sb.WriteString(conditionsPreamble(v))

	// Phases.
	sb.WriteString("## Phases\n\n`status.phase` is the one-word summary `kubectl get ag` prints. " +
		"It is a rollup; the conditions below are the detail.\n\n")
	if v.PhaseDoc != "" {
		sb.WriteString(v.PhaseDoc + "\n\n")
	}
	var phases []string
	for _, p := range v.Phases {
		phases = append(phases, "`"+p.Value+"`")
	}
	sb.WriteString(strings.Join(phases, " · ") + "\n\n")
	for _, p := range v.Phases {
		if p.Doc != "" {
			sb.WriteString(fmt.Sprintf("- `%s` — %s\n", p.Value, p.Doc))
		}
	}
	sb.WriteString("\n")

	// Conditions.
	sb.WriteString("## Condition types\n\n")
	sb.WriteString(conditionsTableNote())
	sb.WriteString("| Condition | Written here | Owned | Sticky | Reasons |\n|---|---|---|---|---|\n")
	for _, c := range v.Conditions {
		written := "no"
		if len(c.Sites) > 0 {
			written = fmt.Sprintf("%d site(s)", len(c.Sites))
		}
		reasons := "—"
		if len(c.Reasons) > 0 {
			var links []string
			for _, r := range c.Reasons {
				links = append(links, fmt.Sprintf("[`%s`](#%s)", r, anchor(r)))
			}
			reasons = strings.Join(links, ", ")
		}
		sb.WriteString(fmt.Sprintf("| `%s` | %s | %s | %s | %s |\n",
			c.Name, written, yesNo(c.Owned), yesNo(c.Sticky), reasons))
	}
	sb.WriteString("\n")

	var documented []ConditionType
	for _, c := range v.Conditions {
		if c.Doc != "" {
			documented = append(documented, c)
		}
	}
	if len(documented) > 0 {
		sb.WriteString("### What each type means\n\n")
		sb.WriteString("The doc comment attached to each type's declaration in `api/v1alpha1`, verbatim. " +
			"For the first constant of a group that is sometimes the comment introducing the GROUP rather " +
			"than that one type — the generator reproduces what is attached and does not re-attribute it.\n\n")
		for _, c := range documented {
			sb.WriteString(fmt.Sprintf("- **`%s`** — %s\n", c.Name, c.Doc))
		}
		sb.WriteString("\n")
	}

	var unwritten []string
	for _, c := range v.Conditions {
		if len(c.Sites) == 0 {
			unwritten = append(unwritten, c.Name)
		}
	}
	if len(unwritten) > 0 {
		sb.WriteString(fmt.Sprintf(`### Declared, and not written by this operator

%d of the %d condition types in the closed vocabulary have no call site in `+"`internal/controller`"+`.
They belong to designs that are not built, or to controllers this repository does not ship. An
Agent in this build never carries one unless something else wrote it.

%s

`, len(unwritten), len(v.Conditions), bulletsPlain(unwritten)))
	}

	// The reason vocabulary.
	sb.WriteString("## Reasons\n\n")
	sb.WriteString("One section per reason string the operator can set, in alphabetical order.\n\n")

	reasons := make([]string, 0, len(v.ReasonsByName))
	for r := range v.ReasonsByName {
		reasons = append(reasons, r)
	}
	sort.Strings(reasons)

	var unreferenced []string
	for _, r := range reasons {
		a := ann[r]
		sites := v.ReasonsByName[r]
		sb.WriteString(fmt.Sprintf("### `%s`\n\n", r))

		// Conditions and statuses, derived.
		type cs struct{ cond, status string }
		seen := map[cs]bool{}
		var pairs []cs
		for _, s := range sites {
			key := cs{s.Condition, s.Status}
			if !seen[key] {
				seen[key] = true
				pairs = append(pairs, key)
			}
		}
		sort.Slice(pairs, func(i, j int) bool {
			if pairs[i].cond != pairs[j].cond {
				return pairs[i].cond < pairs[j].cond
			}
			return pairs[i].status < pairs[j].status
		})
		var parts []string
		for _, p := range pairs {
			st := p.status
			if st == "" {
				st = "status computed at run time"
			}
			parts = append(parts, fmt.Sprintf("`%s=%s`", p.cond, st))
		}
		sb.WriteString("**Conditions:** " + strings.Join(parts, ", ") + "\n\n")

		if rc, ok := constOf[r]; ok {
			sb.WriteString(fmt.Sprintf("**Constant:** `%s` (`internal/controller/%s:%d`)\n\n", rc.Ident, rc.File, rc.Line))
			if rc.Doc != "" {
				sb.WriteString("> " + strings.ReplaceAll(rc.Doc, "\n", " ") + "\n\n")
			}
		} else {
			sb.WriteString("**Constant:** none — written as a string literal at the call site.\n\n")
		}

		sb.WriteString("**Set at:**\n\n")
		for _, s := range sites {
			sb.WriteString(fmt.Sprintf("- `internal/controller/%s:%d` in `%s()` — %s\n",
				s.File, s.Line, s.Func, resolutionNote(s)))
		}
		sb.WriteString("\n")

		if n := len(refs[r].Files); n == 0 {
			unreferenced = append(unreferenced, r)
			sb.WriteString("**Referenced by a test:** **no — no test file in this repository names it.** " +
				"Nothing here fails if it changes or stops being set.\n\n")
		} else {
			sb.WriteString("**Referenced by a test:** " + joinCode(refs[r].Files) + "\n\n")
		}

		sb.WriteString("**State:** " + a.State + "\n\n")
		sb.WriteString("**What the operator does:** " + a.Operator + "\n\n")
		sb.WriteString("**Traffic:** " + trafficText(a.Traffic) + "\n\n")
		if a.Disagreement != "" {
			sb.WriteString("**Code and design disagree:** " + a.Disagreement + "\n\n")
		}
		if a.Note != "" {
			sb.WriteString("**Note:** " + a.Note + "\n\n")
		}
	}

	// The list the reviewer asked for, in one place.
	sb.WriteString("---\n\n## Reasons no test in this repository names\n\n")
	if len(unreferenced) == 0 {
		sb.WriteString("None: every reason the operator can set is named by at least one test file.\n\n")
	} else {
		sb.WriteString(fmt.Sprintf(`%d of the %d reasons the operator can set are named by no test file
anywhere under `+"`internal/`"+`, `+"`api/`"+` or `+"`test/`"+`. Each is a string this operator will put on a user's
object, and no test in this repository fails if it changes, stops being set, or is set for the
wrong state. That is not a claim that the BEHAVIOUR is untested — a test can exercise a path and
assert only its phase — it is the narrower and exact claim that the reason string itself is
unpinned.

%s

`, len(unreferenced), len(reasons), bullets(unreferenced)))
	}

	sb.WriteString(honestyNote(v, refs, ann))
	return write(filepath.Join(out, "conditions.md"), sb.String())
}

func conditionsPreamble(v *Vocabulary) string {
	return fmt.Sprintf(`
The operator never degrades silently: every guarantee it cannot provide surfaces as a condition on
the Agent (NFR-8). This page is the whole vocabulary — %d condition types, and the %d reason
strings the operator can set on them — extracted from the code rather than written beside it.

**How this page is produced.** The condition types, their owned and sticky classification, the
reason strings, and every call site below are read out of `+"`api/v1alpha1`"+` and
`+"`internal/controller`"+` with `+"`go/packages`"+` and `+"`go/types`"+`, by `+"`cmd/refgen`"+`. Three things per reason
are NOT in the code and are written by hand in `+"`%s`"+`: what state produces it, what the operator
does about it, and whether traffic is withdrawn. Those three are held to the code by the generator
— a reason with no entry, or an entry with no reason, fails `+"`make reference`"+` and so fails CI.

**What this page does not claim.** It does not claim a condition and a reason are set together on
every pass that reaches a call site; a pass asserts what it observed. It does not claim the reason
a given pass picks where a call site can set several — those rows say which set. And "referenced by
a test" means a test file names the string, which is weaker than pinning it: nine tests in this
repository once passed with their subject deleted.

`, len(v.Conditions), len(v.ReasonsByName), AnnotationsSource)
}

func conditionsTableNote() string {
	return `**Owned** means the reconciler is this condition's sole author, so a pass that does not assert it
CLEARS it — its absence is the signal that it no longer applies. A type that is not owned belongs to
another controller and is carried forward untouched. **Sticky** means it stays in the list once set
and flips to ` + "`False`" + ` rather than disappearing, because for a normal-true condition like ` + "`Ready`" + ` an
absence is indistinguishable from "never evaluated". Both are read from ` + "`ownedTypes`" + ` and
` + "`stickyTypes`" + ` in ` + "`internal/controller/conditions.go`" + `.

`
}

func honestyNote(v *Vocabulary, refs map[string]TestReference, ann map[string]Annotation) string {
	counts := map[Resolution]int{}
	var unresolved []Site
	for _, s := range v.Sites {
		counts[s.Resolution]++
		if s.Resolution == Unresolved {
			unresolved = append(unresolved, s)
		}
	}
	var unknownTraffic, disagreements []string
	for r, a := range ann {
		if _, live := v.ReasonsByName[r]; !live {
			continue
		}
		if a.Traffic == TrafficUnknown {
			unknownTraffic = append(unknownTraffic, r)
		}
		if a.Disagreement != "" {
			disagreements = append(disagreements, r)
		}
	}
	sort.Strings(unknownTraffic)
	sort.Strings(disagreements)

	var sb strings.Builder
	sb.WriteString("---\n\n## Honesty notes\n\n")
	sb.WriteString(fmt.Sprintf(`**How each reason was resolved.** A call site passes its reason as a constant, a struct field or a
local, and the three are not equally strong:

| Resolution | Sites | What it means |
|---|---|---|
| `+"`constant`"+` | %d | A string literal or a `+"`Reason*`"+` constant at the call site. Exact. |
| `+"`field`"+` | %d | A struct field (`+"`failure.reason`"+` and its siblings). The listed reasons are every constant written into that field, narrowed to the function that built the value where the generator could follow it. One pass sets ONE of them; which one is not claimed. |
| `+"`local`"+` | %d | A local whose assignments — and, one hop, the in-package function they call — all fold to constants. |
| `+"`carried`"+` | %d | The reason is read back off a condition an earlier pass stored. It introduces no new reason and appears in no reason's site list. |
| `+"`unresolved`"+` | %d | The generator could not fold the expression. |

`, counts[ResolvedConstant], counts[ResolvedField], counts[ResolvedLocal],
		counts[ResolvedCarried], counts[Unresolved]))

	if len(unresolved) > 0 {
		sb.WriteString("**Call sites this page could not resolve.** Their reasons are NOT in the list above, " +
			"so the vocabulary here is incomplete by exactly these rows:\n\n")
		for _, s := range unresolved {
			sb.WriteString(fmt.Sprintf("- `%s` at `internal/controller/%s:%d` in `%s()` — reason expression `%s`\n",
				s.Condition, s.File, s.Line, s.Func, s.Expr))
		}
		sb.WriteString("\n")
	} else {
		sb.WriteString("**Every call site resolved.** No condition write in `internal/controller` has a reason " +
			"this page could not fold, so the list above is the whole vocabulary this build can set.\n\n")
	}

	if len(unknownTraffic) > 0 {
		sb.WriteString(fmt.Sprintf(`**Traffic not established.** For %d reason(s) the question "is traffic withdrawn?" was not settled
from the code, and the page says `+"`unknown`"+` rather than guessing:

%s

`, len(unknownTraffic), bullets(unknownTraffic)))
	}

	if len(disagreements) > 0 {
		sb.WriteString(fmt.Sprintf(`**Where the code and a design disagree.** %d reason(s) carry a recorded disagreement, quoted in
their own section above. This page reports them; it does not settle them.

%s

`, len(disagreements), bullets(disagreements)))
	}

	if v.CarrySites > 0 {
		sb.WriteString(fmt.Sprintf("**Conditions re-asserted rather than decided.** %d call site(s) "+
			"re-assert a condition an EARLIER pass stored, verbatim — the mechanism that keeps a "+
			"standing report from being retracted by a pass that learned nothing new. They introduce "+
			"no reason of their own, so they appear in no reason's site list above, and the reason "+
			"they carry is whichever one was stored.\n\n", v.CarrySites))
	}
	if len(v.OpenFields) > 0 {
		sb.WriteString(fmt.Sprintf(`**Reason sets that are not closed.** %d struct reason field(s) have at least one write the
resolver could not fold, so the reasons listed for the call sites that read them are the ones that
WERE folded and not necessarily all of them. Every such site says so where it appears.

%s

`, len(v.OpenFields), bulletsPlain(v.OpenFields)))
	} else {
		sb.WriteString("**Every reason set is closed.** Each struct reason field the page reads had all " +
			"of its writes folded, so a `field` row lists every reason that field can hold.\n\n")
	}

	withRef := 0
	for _, tr := range refs {
		if len(tr.Files) > 0 {
			withRef++
		}
	}
	sb.WriteString(fmt.Sprintf("**Coverage of the test reference scan.** %d of %d reasons are named by at least one "+
		"test file. The scan matches the reason string or its `Reason*` identifier on a word boundary, "+
		"across every `_test.go` under `internal/` and `api/` and every Go file under `test/`. "+
		"`internal/refgen` is excluded: its tests name reasons to check that the GENERATOR resolves "+
		"them, and counting those would let this page shrink its own unpinned list.\n\n",
		withRef, len(refs)))
	return sb.String()
}

func resolutionNote(s Site) string {
	switch s.Resolution {
	case ResolvedConstant:
		return "constant at the call site"
	case ResolvedField:
		if s.FieldOpen {
			return fmt.Sprintf("from `%s`; that field's writes did not all fold, so these %d reasons "+
				"are the ones that did and NOT necessarily all it can hold", s.Expr, len(s.Reasons))
		}
		return fmt.Sprintf("from `%s`, one of the %d reasons that field can hold", s.Expr, len(s.Reasons))
	case ResolvedLocal:
		if len(s.Reasons) == 1 {
			return fmt.Sprintf("from the local `%s`, which folds to this one reason", s.Expr)
		}
		return fmt.Sprintf("from the local `%s`, one of %d reasons it folds to", s.Expr, len(s.Reasons))
	}
	return string(s.Resolution)
}

func trafficText(t Traffic) string {
	switch t {
	case TrafficWithdrawn:
		return "**withdrawn** — requests stop reaching the agent."
	case TrafficKept:
		return "**not withdrawn** — the route goes on serving; only status changes."
	case TrafficDepends:
		return "**depends on the arm** — this reason is raised from more than one state, and traffic " +
			"stops in some of them. The note below says which."
	}
	return "**not established.** The code was read and the answer was not settled; this page will not guess."
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func cell(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "\n", " ")
	if s == "" {
		return "—"
	}
	return s
}

// bulletsPlain lists names WITHOUT linking them, for the condition types that
// have no section of their own on this page. Linking them emitted 23 dead
// anchors in the first version — an index entry for every declared-but-unwritten
// type, and no heading anywhere for any of them.
func bulletsPlain(xs []string) string {
	var sb strings.Builder
	for _, x := range xs {
		sb.WriteString("- `" + x + "`\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

func bullets(xs []string) string {
	var sb strings.Builder
	for _, x := range xs {
		sb.WriteString(fmt.Sprintf("- [`%s`](#%s)\n", x, anchor(x)))
	}
	return strings.TrimRight(sb.String(), "\n")
}

func joinCode(xs []string) string {
	out := make([]string, 0, len(xs))
	for _, x := range xs {
		out = append(out, "`"+x+"`")
	}
	return strings.Join(out, ", ")
}
