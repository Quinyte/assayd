// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package refgen

import (
	"fmt"
	"os"
	"sort"
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
	if s.Items != nil && s.Items.Schema != nil {
		// crdoc titles an array item section "<field>[index]"; matching that
		// spelling is what lets a rule link to the table it constrains.
		collectRules(node+"[index]", s.Items.Schema, out)
	}
	if s.AdditionalProperties != nil && s.AdditionalProperties.Schema != nil {
		collectRules(node+"[key]", s.AdditionalProperties.Schema, out)
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
			sb.WriteString(fmt.Sprintf("### Rules on `%s`\n\n", node))
			if target, label := nearestSection(node, haveSection); target != "" {
				sb.WriteString(fmt.Sprintf("[Fields of `%s`](#%s)\n\n", label, target))
			}
		}
		sb.WriteString("```cel\n" + r.Rule + "\n```\n\n")
		sb.WriteString("> " + strings.ReplaceAll(escapeMD(r.Message), "\n", " ") + "\n\n")
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

// verifyEveryRuleIsPublished is the gate the blocker asked for.
//
// It holds the page to the CRD rather than to the renderer: every rule and every
// message must appear in the bytes that are about to be written. If a future
// template change, or a crdoc upgrade, drops one, `make reference` fails and
// says which — instead of publishing a page that quietly under-reports what the
// API server enforces.
func verifyEveryRuleIsPublished(page string, rules []CELRule) error {
	var missing []string
	for _, r := range rules {
		if !strings.Contains(page, r.Rule) {
			missing = append(missing, fmt.Sprintf("%s: rule %q", r.Node, r.Rule))
			continue
		}
		if r.Message != "" && !strings.Contains(page, escapeMD(r.Message)) {
			missing = append(missing, fmt.Sprintf("%s: the message for %q", r.Node, r.Rule))
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("the CRD reference would have been written with %d of the CRD's %d "+
			"validation rule(s) missing, so a reader would conclude the API server enforces less "+
			"than it does:\n  %s", len(missing), len(rules), strings.Join(missing, "\n  "))
	}
	if len(rules) == 0 {
		return fmt.Errorf("no validation rules were found in %s at all; the CRD carries CEL and "+
			"the extractor is not reading it", CRDSource)
	}
	return nil
}

// escapeMD keeps a CRD message from being read as Markdown or HTML. The
// messages carry `<registry>[:port]` and similar, which a renderer would eat.
func escapeMD(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	return strings.ReplaceAll(s, ">", "&gt;")
}
