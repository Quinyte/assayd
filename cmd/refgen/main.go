// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

// Command refgen writes the generated reference under docs/reference/.
//
// It is run by `make reference`, and `make verify` runs it again and fails if
// the committed output differs — so a change to the CRD, the chart or the
// operator's condition vocabulary cannot reach main with the reference still
// describing the old one. The repository has already shipped two hand-kept
// copies of one document that drifted apart (docs/architecture.md and
// docs/architecture.html); a public API reference is the same liability with a
// larger blast radius.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/Quinyte/assayd/internal/refgen"
)

func main() {
	root := flag.String("root", ".", "repository root")
	out := flag.String("out", "docs/reference", "directory the reference is written to")
	dump := flag.Bool("dump", false, "print the extracted condition vocabulary and exit, for debugging")
	flag.Parse()

	if *dump {
		if err := refgen.Dump(*root, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "refgen:", err)
			os.Exit(1)
		}
		return
	}
	if err := refgen.Generate(*root, *out); err != nil {
		fmt.Fprintln(os.Stderr, "refgen:", err)
		os.Exit(1)
	}
}
