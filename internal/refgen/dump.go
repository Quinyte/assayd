// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package refgen

import (
	"fmt"
	"io"
	"strings"
)

// Dump prints what the extractor found. It exists so that a change to the
// resolution rules can be inspected directly rather than through the rendered
// document, and so that an unresolved site is visible while it is being fixed.
func Dump(root string, w io.Writer) error {
	v, err := ExtractVocabulary(root)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "== %d condition types, %d Reason* constants, %d call sites\n",
		len(v.Conditions), len(v.ReasonConsts), len(v.Sites))
	for _, s := range v.Sites {
		fmt.Fprintf(w, "%-34s %-8s %-10s %-40s %s:%d %s\n", s.Condition, s.Status, s.Resolution,
			strings.Join(s.Reasons, ","), s.File, s.Line, s.Expr)
	}
	refs, err := ScanTestReferences(root, v)
	if err != nil {
		return err
	}
	fmt.Fprintln(w, "== reasons")
	for r, sites := range v.ReasonsByName {
		conds := map[string]bool{}
		for _, s := range sites {
			conds[s.Condition] = true
		}
		names := make([]string, 0, len(conds))
		for c := range conds {
			names = append(names, c)
		}
		n := len(refs[r].Files)
		mark := fmt.Sprintf("%d test file(s)", n)
		if n == 0 {
			mark = "NO TEST REFERENCE"
		}
		fmt.Fprintf(w, "%-36s %-28s %s\n", r, mark, strings.Join(names, ","))
	}
	return nil
}
