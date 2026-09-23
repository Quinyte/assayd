// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package refgen

import (
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// CRDSource is the generated CRD the reference is rendered from. It is
// controller-gen's output, not the Go types: the markers have already been
// resolved into the schema the API server enforces, defaults and CEL rules
// included, and re-parsing the Go types would be a second implementation of
// controller-gen that could disagree with it.
const CRDSource = "config/crd/assayd.dev_agents.yaml"

// crdocBin renders the FIELD TABLES — names, types, required, defaults, doc
// text, and the nested-type sections they link to. That is what it is adopted
// for, and it does it well.
//
// It is NOT adopted for the CEL rules, and an earlier version of this file said
// it was. It renders a Validations block for an object-typed field and none for
// an array-item schema or for the root, so it published 6 of this CRD's 9
// rule-carrying nodes and said nothing about the other 3 — the same silent
// dropping of `x-kubernetes-validations` that ruled out elastic/crd-ref-docs,
// found by the independent review. The complete rule list is extracted from the
// CRD by crdrules.go, and verifyEveryRuleIsPublished fails generation rather
// than letting the page under-report again.
//
// What crd-ref-docs would still have cost: it reads the Go types directly and
// resolves named types better, but it drops XValidation in its processor
// outright, so the same section would have been owed. ahmetb's generator emits
// HTML only; kubernetes-sigs/reference-docs is built for Kubernetes' own API.
// Apache-2.0, like this repository.
const crdocBin = "crdoc"

// renderCRD produces docs/reference/crd-agent.md.
func renderCRD(root, out string) error {
	src := filepath.Join(root, CRDSource)
	if _, err := os.Stat(src); err != nil {
		return fmt.Errorf("the CRD the reference is generated from is missing: %w — run `make manifests`", err)
	}
	rules, err := celRules(src)
	if err != nil {
		return err
	}

	tmp, err := os.MkdirTemp("", "refgen-crd")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	body := filepath.Join(tmp, "body.md")

	cmd := exec.Command(crdocBin, "--resources", src, "--output", body)
	cmd.Dir = root
	if b, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %w\n%s\n\nInstall the pinned version with `make tools`", crdocBin, err, b)
	}
	b, err := os.ReadFile(body)
	if err != nil {
		return err
	}

	// crdoc opens with its own "# API Reference" heading and a package index.
	// Both are replaced, so that the page has one title and the preamble sits
	// above the tables rather than under a heading that is not about it.
	text := string(b)
	if i := strings.Index(text, "# assayd.dev/v1alpha1"); i >= 0 {
		text = text[i:]
	}
	text = demoteHeadings(text)
	text, err = repairTableHTML(text)
	if err != nil {
		return err
	}

	var sb strings.Builder
	sb.WriteString(frontMatter("The Agent CRD",
		"Every field of the Agent custom resource: type, required, default, CEL validation and doc text."))
	sb.WriteString(generatedNotice())
	sb.WriteString(fmt.Sprintf(`
This page is the schema of the only custom resource assayd installs.

**`+"`Agent`"+` is the whole API surface today.** `+"`make manifests`"+` produces exactly one CRD, from
`+"`api/v1alpha1/agent_types.go`"+`, and the chart ships that one file. The generator checks this
before writing the page and refuses to write it if a second CRD appears, so the sentence cannot
outlive the fact. (`+"`AgentList`"+` carries the same root marker and is the list type of this CRD, not
a second one.) Documentation elsewhere that counts several assayd CRDs is describing designs, not
this build.

**Defaults are applied by the API SERVER, not by the operator.** Every `+"`Default`"+` below comes from a
`+"`+kubebuilder:default`"+` marker, which controller-gen writes into the structural schema as
`+"`default:`"+`. The API server substitutes it on write, so the value is present on the stored object
and in `+"`kubectl get -o yaml`"+` whether or not the field was typed. A field with no `+"`Default`"+` row is
absent when it is not set, and the operator's own behaviour for an absent field is described in
that field's text, not here.

**The field tables do NOT list every validation rule. [Every validation rule](#every-validation-rule)
does.** The tool that renders the tables emits a `+"`Validations`"+` row for object-typed fields only —
never for an array item's schema, and never for the root — so of this CRD's %d rules on %d schema
nodes the tables carry %d nodes' worth. A field with no `+"`Validations`"+` row in a table is therefore
NOT a field with no rules. The complete list is read out of the CRD itself and its presence on this
page is checked before the page is written.

**Required is the schema's `+"`required`"+` list**, not "the operator needs it". A field marked
`+"`false`"+` may still be one without which the operator cannot do anything useful.

`, len(rules), countNodes(rules), renderedValidationNodes(text)))
	sb.WriteString("---\n\n")
	sb.WriteString(text)
	if !strings.HasSuffix(text, "\n") {
		sb.WriteString("\n")
	}
	sb.WriteString("\n---\n\n")
	sb.WriteString(renderCELSection(rules, text))

	page := sb.String()
	// The gate re-reads the CRD itself; `rules` above is the render input and
	// deliberately not what it is checked against.
	if err := verifyEveryRuleIsPublished(page, src); err != nil {
		return err
	}
	return write(filepath.Join(out, "crd-agent.md"), page)
}

// demoteHeadings pushes the table renderer's headings down one level.
//
// Its top heading is an H1, and the front matter already gives the page one:
// a site that renders the front-matter title emits two `<h1>` elements, which
// is a document-outline defect and an accessibility one. Demoting also puts its
// sections at the same depth as this generator's own, so the page has one
// hierarchy rather than two interleaved.
//
// Heading ANCHORS are unaffected — a slug comes from a heading's text, not its
// level — so the several hundred internal links in the field tables keep
// landing. The deepest heading it emits is H3, so nothing is pushed past H6;
// the guard is here anyway, because silently clamping would merge two levels.
func demoteHeadings(md string) string {
	var out []string
	for _, line := range strings.Split(md, "\n") {
		if m := headingLine.FindStringSubmatch(line); m != nil && len(m[1]) < 6 {
			out = append(out, "#"+line)
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// renderedValidationNodes counts the Validations blocks the table renderer
// actually emitted, so the sentence above reports what this run produced rather
// than a number someone typed once.
func renderedValidationNodes(text string) int {
	return strings.Count(text, "<i>Validations</i>")
}

// validationItems is one crdoc Validations run: `<i>Validations</i>:` followed
// by one or more `<li>…</li>` on the same line, with no list around them.
var validationItems = regexp.MustCompile(`(<i>Validations</i>:)((?:<li>.*?</li>)+)`)

// listItem is one `<li>…</li>` inside a Validations run.
var listItem = regexp.MustCompile(`<li>(.*?)</li>`)

// repairTableHTML fixes three defects in the HTML crdoc emits inside its field
// tables, each of which the public site published (independent review of PR
// #67, measured with axe on the built /reference/crd-agent.html):
//
//   - A Validations run is bare `<li>` elements with no `<ul>`, which is
//     invalid HTML and which assistive technology announces as nothing in
//     particular (axe `listitem`). Each run is wrapped in one `<ul>`.
//   - A rule's text and message are written RAW. spec.runtime.image's message
//     reads `<registry>[:port]/<repo>[:tag]@sha256:<64 lowercase hex>`, and a
//     browser parses `<registry>` and `<repo>` as unknown elements, so the
//     published message lost its placeholders. Each item is HTML-escaped. If
//     crdoc ever starts escaping itself this would double-escape, so an item
//     that already carries an entity refuses generation instead.
//   - A wide table scrolls, and a scrolling region nothing can focus cannot be
//     panned from the keyboard (axe `scrollable-region-focusable`, WCAG
//     2.1.1). Each table is wrapped in a focusable, labelled region, the same
//     shape web/shared's Figure uses for a wide plate. The site's stylesheet
//     makes the wrapper, not the table, the thing that scrolls.
func repairTableHTML(md string) (string, error) {
	var bad string
	md = validationItems.ReplaceAllStringFunc(md, func(run string) string {
		m := validationItems.FindStringSubmatch(run)
		items := listItem.ReplaceAllStringFunc(m[2], func(li string) string {
			inner := listItem.FindStringSubmatch(li)[1]
			if entity.MatchString(inner) && bad == "" {
				bad = inner
			}
			return "<li>" + html.EscapeString(inner) + "</li>"
		})
		return m[1] + "<ul>" + items + "</ul>"
	})
	if bad != "" {
		return "", fmt.Errorf("a validation rule in the table renderer's output already carries an "+
			"HTML entity, so escaping it would publish the entity's text instead of the character: "+
			"%q. %s has started escaping its own output; drop the escape in repairTableHTML", bad, crdocBin)
	}

	var out []string
	heading := "the table"
	for _, line := range strings.Split(md, "\n") {
		if m := headingLine.FindStringSubmatch(line); m != nil {
			heading = strings.TrimSpace(strings.TrimLeft(line, "#"))
		}
		switch strings.TrimSpace(line) {
		case "<table>":
			out = append(out, fmt.Sprintf(`<div class="ref-table" role="region" tabindex="0" aria-label="%s">`,
				html.EscapeString("Fields of "+heading+". Scroll or use the arrow keys to pan.")))
			out = append(out, line)
			continue
		case "</table>":
			out = append(out, line, "</div>")
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n"), nil
}

// entity matches an HTML character reference: named, decimal or hex.
var entity = regexp.MustCompile(`&(#[0-9]+|#[xX][0-9a-fA-F]+|[A-Za-z][A-Za-z0-9]*);`)
