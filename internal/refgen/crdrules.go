// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package refgen

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/yaml"
)

// CELRule is one x-kubernetes-validations entry, with the schema node it sits on.
type CELRule struct {
	// Node is the schema path, spelled the way crdoc titles its sections
	// ("Agent.spec.llm.providers[index]"), so a rule can link to the field table
	// it belongs to.
	Node    string
	Rule    string
	Message string
	// Reason and FieldPath are set by the marker where it sets them; the API
	// server returns them on a refusal.
	Reason    string
	FieldPath string
}

// celRules reads EVERY x-kubernetes-validations entry out of the generated CRD.
//
// This exists because crdoc does not render them all, and the omission is
// silent. crdoc emits a Validations block for an object-typed field and none for
// an ARRAY ITEM schema or for the root — so of this CRD's 25 rules across 9
// nodes it renders 6 nodes' worth, and a reader of a field with no Validations
// row concludes the field has no rules. Two of the missing sets are the
// egressAllowlist and providers item schemas, and one is
// `size(self.metadata.name) <= 52`, which a user meets as a refusal from
// `kubectl apply` with nothing on the page to explain it.
//
// The independent review of the first version of this generator found it, and
// it was the same defect this generator rejected elastic/crd-ref-docs for — a
// tool silently dropping the CRD's CEL. Reading the rules here, from the schema
// the API server actually enforces, is the answer that does not depend on a
// renderer's taste.
func celRules(path string) ([]CELRule, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var crd apiextv1.CustomResourceDefinition
	if err := yaml.Unmarshal(b, &crd); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	var out []CELRule
	for _, v := range crd.Spec.Versions {
		if v.Schema == nil || v.Schema.OpenAPIV3Schema == nil {
			continue
		}
		collectRules(crd.Spec.Names.Kind, v.Schema.OpenAPIV3Schema, &out)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Node != out[j].Node {
			// The root first, then by depth and name, so the page reads from the
			// whole object inwards.
			return nodeLess(out[i].Node, out[j].Node)
		}
		return i < j
	})
	return out, nil
}

func collectRules(node string, s *apiextv1.JSONSchemaProps, out *[]CELRule) {
	if s == nil {
		return
	}
	for _, v := range s.XValidations {
		r := CELRule{Node: node, Rule: v.Rule, Message: v.Message}
		if v.Reason != nil {
			r.Reason = string(*v.Reason)
		}
		r.FieldPath = v.FieldPath
		*out = append(*out, r)
	}
	names := make([]string, 0, len(s.Properties))
	for name := range s.Properties {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		p := s.Properties[name]
		collectRules(node+"."+name, &p, out)
	}
	if s.Items != nil {
		// crdoc titles an array item section "<field>[index]"; matching that
		// spelling is what lets a rule link to the table it constrains.
		if s.Items.Schema != nil {
			collectRules(node+"[index]", s.Items.Schema, out)
		}
		// A tuple-typed array. controller-gen emits no such schema today, so
		// this arm is unreachable from the markers — it is here because the
		// alternative is a traversal that silently skips a branch of the schema
		// it claims to read completely, and "completely" is this file's whole
		// claim.
		for i := range s.Items.JSONSchemas {
			collectRules(fmt.Sprintf("%s[%d]", node, i), &s.Items.JSONSchemas[i], out)
		}
	}
	if s.AdditionalProperties != nil && s.AdditionalProperties.Schema != nil {
		collectRules(node+"[key]", s.AdditionalProperties.Schema, out)
	}
	// Likewise unreachable from controller-gen's markers today, and likewise
	// traversed rather than assumed away. A rule under one of these would be
	// enforced by the API server whether or not this generator went looking.
	for i := range s.AllOf {
		collectRules(fmt.Sprintf("%s{allOf %d}", node, i), &s.AllOf[i], out)
	}
	for i := range s.AnyOf {
		collectRules(fmt.Sprintf("%s{anyOf %d}", node, i), &s.AnyOf[i], out)
	}
	for i := range s.OneOf {
		collectRules(fmt.Sprintf("%s{oneOf %d}", node, i), &s.OneOf[i], out)
	}
	if s.Not != nil {
		collectRules(node+"{not}", s.Not, out)
	}
}

func nodeLess(a, b string) bool {
	da, db := strings.Count(a, "."), strings.Count(b, ".")
	if da != db {
		return da < db
	}
	return a < b
}

// renderCELSection writes the complete list of CEL rules, one heading per schema
// node, each linking to the field table crdoc wrote for that node.
func renderCELSection(rules []CELRule, tableBody string) string {
	// Which nodes the table renderer gave a section of their own. A scalar field
	// never gets one — Agent.spec.card.path carries a rule and has no heading —
	// so a link to it would be dead, and the nearest ANCESTOR that does have one
	// is where a reader goes to see the field.
	haveSection := map[string]bool{}
	for _, m := range headingLine.FindAllStringSubmatch(tableBody, -1) {
		haveSection[anchor(inlineFormat.ReplaceAllString(m[2], ""))] = true
	}

	var sb strings.Builder
	sb.WriteString("## Every validation rule\n\n")
	sb.WriteString(fmt.Sprintf(`All %d `+"`x-kubernetes-validations`"+` rules in the CRD, on all %d schema nodes that carry any.

**This section, and not the field tables above, is the complete list.** The tables carry a
`+"`Validations`"+` row for object-typed fields only: an ARRAY ITEM schema's rules and the root
schema's do not appear there at all, so a field with no `+"`Validations`"+` row in a table is not a
field with no rules. The generator reads every rule out of the CRD itself and fails rather than
writing this page if any of them is missing from it.

Each rule is CEL, evaluated by the API server on every write, and a write that fails one is
REFUSED with the message beside it. `+"`self`"+` is the node the rule sits on.

`, len(rules), countNodes(rules)))

	var node string
	for _, r := range rules {
		if r.Node != node {
			node = r.Node
			// "Rules on X" rather than "X": the field tables already have a
			// heading per node, and two headings that slug alike send every
			// link to whichever came first.
			sb.WriteString(celHeading(node) + "\n\n")
			if target, label := nearestSection(node, haveSection); target != "" {
				sb.WriteString(fmt.Sprintf("[Fields of `%s`](#%s)\n\n", label, target))
			}
		}
		sb.WriteString("```cel\n" + r.Rule + "\n```\n\n")
		sb.WriteString("> " + renderedMessage(r.Message) + "\n\n")
		if r.Reason != "" || r.FieldPath != "" {
			var extra []string
			if r.Reason != "" {
				extra = append(extra, "reason `"+r.Reason+"`")
			}
			if r.FieldPath != "" {
				extra = append(extra, "fieldPath `"+r.FieldPath+"`")
			}
			sb.WriteString("Refused with " + strings.Join(extra, ", ") + ".\n\n")
		}
	}
	return sb.String()
}

func rulesOn(rules []CELRule, node string) int {
	n := 0
	for _, r := range rules {
		if r.Node == node {
			n++
		}
	}
	return n
}

func countNodes(rules []CELRule) int {
	seen := map[string]bool{}
	for _, r := range rules {
		seen[r.Node] = true
	}
	return len(seen)
}

// nearestSection finds the closest node at or above this one that the field
// tables gave a heading to, and returns that heading's anchor and its name.
//
// It walks UP rather than giving up, because a rule on a scalar field
// (Agent.spec.card.path, Agent.spec.runtime.image) has no section of its own and
// a link to one would be dead — the very defect this pass is fixing. The root
// node stops the walk: it has no section either.
func nearestSection(node string, have map[string]bool) (target, label string) {
	for n := node; strings.Contains(n, "."); n = n[:strings.LastIndex(n, ".")] {
		if a := anchor(n); have[a] {
			return a, n
		}
	}
	return "", ""
}

// verifyEveryRuleIsPublished is the gate on the CRD page, and it re-reads the
// CRD to get its own answer.
//
// Two things were wrong with the first version, and both are the same mistake
// in different places.
//
// It asked whether each rule appeared ANYWHERE on the page. Agent.spec.llm
// .fallback and Agent.spec.llm.providers[index] are the same Go struct, so
// their five rules and five messages are byte-identical — and an independent
// review deleted every `providers` node from the render and the gate passed,
// because the twin's text was still there. The page then announced "All 20
// rules on 8 nodes", a false sentence about the CRD. That duplication is what
// hid the ORIGINAL blocker too, and the reviewer had been caught by it once
// itself. So the check is now PER NODE: the node's own heading must exist, and
// the rule and its message must be inside that heading's section.
//
// And its truth came from the same slice the renderer was handed, so a filter
// applied before both would have been invisible to it. It now parses the CRD
// again.
//
// What that re-read is and is not independent of, stated exactly, because the
// first version of this comment overreached. It is independent of the render
// path's FILTERING — which is what catches a node dropped between the read and
// the page — and not of its READING: it calls celRules, so it shares
// collectRules with the renderer, and a traversal that under-read the schema
// would hide a node from both. An independent review walked the CRD without
// using collectRules, descending every subschema blindly, and got the same
// 9 nodes and 25 rules; it also confirmed that the shapes collectRules does not
// descend — patternProperties, definitions, dependencies, additionalItems — are
// forbidden in a CRD structural schema. So the reader is correct today. The
// claim this comment makes is only that it is correct, not that it is checked.
func verifyEveryRuleIsPublished(page, crdPath string) error {
	rules, err := celRules(crdPath)
	if err != nil {
		return fmt.Errorf("re-read %s to check the page against it: %w", crdPath, err)
	}
	if len(rules) == 0 {
		return fmt.Errorf("no validation rules were found in %s at all; the CRD carries CEL and "+
			"the extractor is not reading it", CRDSource)
	}
	if err := verifyCensus(page, rules); err != nil {
		return err
	}
	sections := ruleSections(page)

	var missing []string
	missingRules := 0
	reportedNode := map[string]bool{}
	for _, r := range rules {
		// The renderer transforms a message before publishing it, and the gate
		// looks for the transformed form — so the two share renderedMessage,
		// which is what closed a false FAILURE over newline handling. The cost
		// is that a transformation bug then computes the same wrong string on
		// both sides and passes. A review truncated every message to twelve
		// characters and the gate stayed green.
		//
		// The check is a ROUND TRIP, not a length bound. A length bound was the
		// first attempt and it only killed the shortening subclass: reversing
		// the rendered bytes preserves length, and widening an em dash to five
		// hyphens grows it, and both publish a corrupted message under a green
		// gate. escapeMD is invertible, so the exact property is available for
		// the same few lines — and it still lets `\n` → " " through, which is
		// what the false FAILURE this replaced was about.
		if got, want := unescapeMD(renderedMessage(r.Message)),
			strings.ReplaceAll(r.Message, "\n", " "); got != want {
			return fmt.Errorf("the renderer's message transformation does not round-trip for the "+
				"rule %q on %s. Un-escaping what would be published gives:\n    %q\nand the CRD "+
				"says:\n    %q\nThe published message is what a user reads when the API server "+
				"refuses their write, so it must be the CRD's message and nothing else",
				r.Rule, r.Node, got, want)
		}
		body, ok := sections[r.Node]
		if !ok {
			// Once per node, not once per rule: a node dropped whole is one
			// fact, and five copies of it buries the other four causes.
			if !reportedNode[r.Node] {
				reportedNode[r.Node] = true
				n := rulesOn(rules, r.Node)
				missingRules += n
				missing = append(missing, fmt.Sprintf("%s: the whole node, %d rule(s) — no %q "+
					"heading on the page", r.Node, n, celHeading(r.Node)))
			}
			continue
		}
		if !strings.Contains(body, r.Rule) {
			missingRules++
			missing = append(missing, fmt.Sprintf("%s: rule %q", r.Node, r.Rule))
			continue
		}
		if r.Message != "" && !strings.Contains(body, renderedMessage(r.Message)) {
			missingRules++
			missing = append(missing, fmt.Sprintf("%s: the message for %q", r.Node, r.Rule))
		}
	}
	if len(missing) > 0 {
		// RULES missing, not findings reported. A node dropped whole is one
		// finding and five rules, and the first version's headline said "1 of
		// 25" for exactly that — a fivefold understatement in the one sentence
		// whose job is to say how much is absent.
		return fmt.Errorf("the CRD reference would have been written with %d of the CRD's %d "+
			"validation rule(s) missing from the node they belong to, so a reader would conclude "+
			"the API server enforces less than it does:\n  %s",
			missingRules, len(rules), strings.Join(missing, "\n  "))
	}
	return nil
}

// celSectionHeading opens the complete rule list, and bounds where the census
// sentence is looked for.
const celSectionHeading = "## Every validation rule"

// censusLine matches the sentence the page opens its rule section with.
var censusLine = regexp.MustCompile(
	"All (\\d+) `x-kubernetes-validations` rules in the CRD, on all (\\d+) schema nodes")

// censusScope is the rule section's PREAMBLE: from its heading to the first
// per-node heading under it, or to the next section if it has none.
//
// The preamble and not the whole section, because the section renders every CEL
// MESSAGE, and a message is author-controlled text out of the CRD. Scoping to
// the section still counted a message containing the census pattern as a second
// census, so a contributor who wrote one would have had `make reference` refuse
// a correct page — which is the failure this scoping exists to prevent, not to
// relocate. Nothing author-controlled reaches the preamble.
func censusScope(page string) (string, bool) {
	i := strings.Index(page, celSectionHeading)
	if i < 0 {
		return "", false
	}
	rest := page[i+len(celSectionHeading):]
	end := len(rest)
	for _, mark := range []string{"\n### ", "\n## ", "\n# "} {
		if j := strings.Index(rest, mark); j >= 0 && j < end {
			end = j
		}
	}
	return rest[:end], true
}

// verifyCensus holds the page's own count of itself to the CRD.
//
// The counts were rendered from the slice the renderer was handed and checked
// by nothing. A review left every rule correctly published and changed only the
// numbers: the page announced "All 32 rules … on all 12 schema nodes", a false
// census about the CRD, with every gate green. It is the visible symptom of the
// original blocker — the page that said "All 20 rules on 8 nodes" — and it was
// the last thing the render path could assert unchecked.
//
// The integers are PARSED back out of the rendered page rather than compared to
// a sentence built here, so that the check does not depend on the renderer's
// wording being reproduced correctly in two places.
func verifyCensus(page string, rules []CELRule) error {
	// SCOPED to the rule section, and then required to be UNIQUE within it.
	// Taking the first match over the whole page failed both ways. A decoy
	// sentence carrying the true numbers, placed before the real one, was
	// validated while the sentence a reader gets went unchecked. And a CEL
	// message that happened to contain this pattern would match FIRST, because
	// the field tables render messages earlier in the page than this section —
	// so a correct page was refused, with the gate naming a cause that was
	// never true. That is rule 8 committed by the gate that exists to catch it.
	scope, ok := censusScope(page)
	if !ok {
		return fmt.Errorf("the CRD reference would have been written without its %q section, which "+
			"is where the complete list of validation rules lives", celSectionHeading)
	}
	all := censusLine.FindAllStringSubmatch(scope, -1)
	switch len(all) {
	case 1:
	case 0:
		return fmt.Errorf("the CRD reference would have been written without the sentence that " +
			"states how many validation rules the CRD has, which is the claim the rest of the " +
			"section is the evidence for")
	default:
		return fmt.Errorf("the CRD reference would have been written with %d sentences matching the "+
			"census pattern inside %q, and a gate that took the first would be checking whichever "+
			"came first rather than the one a reader gets. Emit one, or narrow censusLine",
			len(all), celSectionHeading)
	}
	m := all[0]
	gotRules, err := strconv.Atoi(m[1])
	if err != nil {
		return fmt.Errorf("the rule count in the page's own census is not a number: %q", m[1])
	}
	gotNodes, err := strconv.Atoi(m[2])
	if err != nil {
		return fmt.Errorf("the node count in the page's own census is not a number: %q", m[2])
	}
	wantRules, wantNodes := len(rules), countNodes(rules)
	if gotRules != wantRules || gotNodes != wantNodes {
		return fmt.Errorf("the CRD reference would have been written announcing %d rule(s) on %d "+
			"schema node(s) while the CRD carries %d on %d. Publishing a false census is the "+
			"symptom the original omission was caught by; the counts must come from the CRD",
			gotRules, gotNodes, wantRules, wantNodes)
	}
	return nil
}

// celHeading is the heading renderCELSection writes for a node.
func celHeading(node string) string { return "### Rules on `" + node + "`" }

// ruleSections slices the page into the body under each `### Rules on ...`
// heading, so a rule can be looked for where it is supposed to be rather than
// wherever its twin happens to appear.
func ruleSections(page string) map[string]string {
	out := map[string]string{}
	const prefix = "### Rules on `"
	for idx := 0; ; {
		i := strings.Index(page[idx:], prefix)
		if i < 0 {
			return out
		}
		start := idx + i
		nameStart := start + len(prefix)
		nameEnd := strings.Index(page[nameStart:], "`")
		if nameEnd < 0 {
			return out
		}
		node := page[nameStart : nameStart+nameEnd]
		bodyStart := nameStart + nameEnd
		// The section runs to the next heading of this level or shallower.
		rest := page[bodyStart:]
		end := len(rest)
		for _, mark := range []string{"\n### ", "\n## ", "\n# "} {
			if j := strings.Index(rest, mark); j >= 0 && j < end {
				end = j
			}
		}
		out[node] = rest[:end]
		idx = bodyStart + end
		if end == 0 {
			idx = bodyStart + 1
		}
	}
}

// renderedMessage is how a CRD message appears on the page. ONE function, used
// by the renderer and by the gate.
//
// They diverged in the first version: the renderer escaped the message and then
// flattened its newlines, while the gate only escaped it. A message containing
// a newline would have failed the gate for a page that published it correctly —
// a check that is loud and wrong, which is the operator defect this generator
// exists to report, committed by the generator itself.
func renderedMessage(msg string) string {
	return strings.ReplaceAll(escapeMD(msg), "\n", " ")
}

// unescapeMD is escapeMD's inverse, and exists so that the gate can assert the
// exact round trip rather than a one-sided bound on length.
//
// The substitutions are undone in the REVERSE order they were applied, which is
// what makes it an inverse rather than an approximation: escapeMD rewrites `&`
// first, so a message containing the literal text `&lt;` becomes `&amp;lt;`,
// and undoing `&amp;` last turns that back into `&lt;` rather than into `<`.
func unescapeMD(s string) string {
	s = strings.ReplaceAll(s, "&gt;", ">")
	s = strings.ReplaceAll(s, "&lt;", "<")
	return strings.ReplaceAll(s, "&amp;", "&")
}

// escapeMD keeps a CRD message from being read as Markdown or HTML. The
// messages carry `<registry>[:port]` and similar, which a renderer would eat.
func escapeMD(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	return strings.ReplaceAll(s, ">", "&gt;")
}
