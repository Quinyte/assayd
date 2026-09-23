// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package docs

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/Quinyte/assayd/internal/refgen"
)

// Every in-page link in the docs tree must land.
//
// This gate exists because of a specific failure. docs/reference/ is generated,
// and its first version shipped 51 links to anchors that did not exist — 23
// index entries for condition types it gave no section to, and 28 chart keys
// slugged by an algorithm no Markdown renderer uses (`gateway-enabled` for a
// heading GitHub slugs `gatewayenabled`). They were live on GitHub, and the
// generator's own tests passed, because nothing anywhere asked whether a link
// landed. The generator now refuses to write such a page; this asks the same
// question of the whole tree, so the class cannot come back through a
// hand-written document either.
//
// It is the SAME implementation, not a second one: refgen.DeadAnchors is what
// the generator calls. Two copies of "does this link land" would be one more
// pair that drifts.
//
// Scope: in-page anchors only (`](#…)` and `href="#…"`). A link to another file
// or to the web is not checked — that needs a fetch, and a fetch in a unit test
// is a flake.
func TestEveryInPageLinkInTheDocsTreeLands(t *testing.T) {
	var files []string
	err := filepath.WalkDir(docsRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".md") {
			return nil
		}
		files = append(files, p)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", docsRoot, err)
	}
	sort.Strings(files)
	if len(files) < 20 {
		t.Fatalf("found only %d Markdown files under %s; the walk is broken and this gate would "+
			"pass by having nothing to check", len(files), docsRoot)
	}

	var broken []string
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		dead := refgen.DeadAnchors(string(b))
		if len(dead) == 0 {
			continue
		}
		rel, relErr := filepath.Rel(docsRoot, f)
		if relErr != nil {
			rel = f
		}
		broken = append(broken, rel+": #"+strings.Join(dead, ", #"))
	}
	if len(broken) > 0 {
		t.Errorf("%d document(s) link to an anchor that does not exist in them:\n  %s\n\n"+
			"Fix the link or add the heading. For a file under docs/reference/, fix the "+
			"GENERATOR — the page is rewritten by `make reference`.",
			len(broken), strings.Join(broken, "\n  "))
	}
}
