// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package refgen

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The three repairs repairTableHTML makes to crdoc's table HTML, asserted
// against the COMMITTED page. `make verify` holds that page to a fresh
// generation (crd-agent.md needs crdoc, so no unit test regenerates it); the
// unit test below exercises repairTableHTML itself, so a repair that stops
// happening fails without crdoc too.
func TestTheCRDTablesPublishValidHTML(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(repoRoot, "docs", "reference", "crd-agent.md"))
	if err != nil {
		t.Fatal(err)
	}
	page := string(b)

	// Every <li> sits inside a <ul>: no Validations run is a bare list item.
	if n := strings.Count(page, ":<li>"); n != 0 {
		t.Errorf("%d Validations run(s) open with a bare <li>; each must be wrapped in <ul>", n)
	}
	if strings.Count(page, "<ul><li>") != renderedValidationNodes(page) {
		t.Errorf("%d Validations runs, %d wrapped lists", renderedValidationNodes(page),
			strings.Count(page, "<ul><li>"))
	}

	// No list item carries a raw tag other than the markup itself: the
	// spec.runtime.image message's placeholders are text, not elements.
	items := regexp.MustCompile(`<li>(.*?)</li>`).FindAllStringSubmatch(page, -1)
	if len(items) == 0 {
		t.Fatal("no list items found; the page's shape changed under this test")
	}
	for _, m := range items {
		if strings.ContainsAny(m[1], "<>") {
			t.Errorf("a validation item publishes a raw angle bracket, which a browser parses as a "+
				"tag: %q", m[1])
		}
	}
	if !strings.Contains(page, "&lt;registry&gt;[:port]/&lt;repo&gt;") {
		t.Error("the image rule's placeholders are not published as text")
	}

	// Every table sits in a focusable, labelled region.
	tables := strings.Count(page, "<table>")
	regions := regexp.MustCompile(`<div class="ref-table" role="region" tabindex="0" aria-label="Fields of [^"]+">\n<table>`).
		FindAllString(page, -1)
	if tables == 0 || len(regions) != tables {
		t.Errorf("%d tables, %d of them in a focusable labelled region", tables, len(regions))
	}
}

// An item crdoc has already escaped would be double-escaped and publish
// "&amp;lt;" as text, so repairTableHTML refuses it rather than guessing.
func TestAnAlreadyEscapedValidationItemIsRefused(t *testing.T) {
	if _, err := repairTableHTML("<i>Validations</i>:<li>a &lt; b</li>\n"); err == nil {
		t.Fatal("an item carrying an HTML entity was escaped again instead of refused")
	}
	got, err := repairTableHTML("<i>Validations</i>:<li>a < b && <c></li><li>d</li>\n")
	if err != nil {
		t.Fatal(err)
	}
	if want := "<i>Validations</i>:<ul><li>a &lt; b &amp;&amp; &lt;c&gt;</li><li>d</li></ul>\n"; got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

// Each table is wrapped in a focusable, labelled region named for the heading
// above it, and nothing else is.
func TestEveryTableIsWrappedInAFocusableRegion(t *testing.T) {
	in := "#### Agent.spec\n\ntext\n<table>\n  <tr><td>x</td></tr>\n</table>\nafter\n"
	got, err := repairTableHTML(in)
	if err != nil {
		t.Fatal(err)
	}
	want := "#### Agent.spec\n\ntext\n" +
		`<div class="ref-table" role="region" tabindex="0" aria-label="Fields of Agent.spec. ` +
		`Scroll or use the arrow keys to pan.">` + "\n<table>\n  <tr><td>x</td></tr>\n</table>\n</div>\nafter\n"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}
