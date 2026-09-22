// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package refgen

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// CRDSource is the generated CRD the reference is rendered from. It is
// controller-gen's output, not the Go types: the markers have already been
// resolved into the schema the API server enforces, defaults and CEL rules
// included, and re-parsing the Go types would be a second implementation of
// controller-gen that could disagree with it.
const CRDSource = "config/crd/assayd.dev_agents.yaml"

// crdocBin is the generator. It is ADOPTED rather than written here.
//
// The survey that chose it: elastic/crd-ref-docs is better maintained and reads
// the Go types directly, but it DROPS the CEL rules — processor.go carries a
// live `case "XValidation": continue` — and this CRD's most load-bearing
// constraints are CEL. crdoc renders x-kubernetes-validations, emits Markdown,
// and reads the CRD YAML the repository already generates. ahmetb's generator
// emits HTML only and its own README points at crd-ref-docs;
// kubernetes-sigs/reference-docs is built for Kubernetes' own API.
//
// Apache-2.0, like this repository.
const crdocBin = "crdoc"

// renderCRD produces docs/reference/crd-agent.md.
//
// crdoc writes the field tables; this function writes the header in front of
// them. The header is not decoration: crdoc prints `Default: x` and says
// nothing about who applies it, and a reader who assumes the operator does
// would reason wrongly about what an object looks like the moment it is
// created. So the preamble answers that once, for every default in the file.
func renderCRD(root, out string) error {
	src := filepath.Join(root, CRDSource)
	if _, err := os.Stat(src); err != nil {
		return fmt.Errorf("the CRD the reference is generated from is missing: %w — run `make manifests`", err)
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

	var sb strings.Builder
	sb.WriteString(header("The Agent CRD"))
	sb.WriteString(`
This page is the schema of the only custom resource assayd installs.

**` + "`Agent`" + ` is the whole API surface today.** ` + "`make manifests`" + ` produces exactly one CRD, from
` + "`api/v1alpha1/agent_types.go`" + `, and the chart ships that one file. The generator checks this
before writing the page and refuses to write it if a second CRD appears, so the sentence cannot
outlive the fact. (` + "`AgentList`" + ` carries the same root marker and is the list type of this CRD, not
a second one.) Documentation elsewhere that counts several assayd CRDs is describing designs, not
this build.

**Defaults are applied by the API SERVER, not by the operator.** Every ` + "`Default`" + ` below comes from a
` + "`+kubebuilder:default`" + ` marker, which controller-gen writes into the structural schema as
` + "`default:`" + `. The API server substitutes it on write, so the value is present on the stored object
and in ` + "`kubectl get -o yaml`" + ` whether or not the field was typed. A field with no ` + "`Default`" + ` row is
absent when it is not set, and the operator's own behaviour for an absent field is described in
that field's text, not here.

**Validations are CEL, evaluated by the API server on every write.** Each ` + "`Validations`" + ` entry is one
` + "`x-kubernetes-validations`" + ` rule: the expression, then the message a rejected write is refused
with. They are refusals at admission, so an object that exists has already satisfied all of them —
except where validation ratcheting applies, which the affected fields say.

**Required is the schema's ` + "`required`" + ` list**, not "the operator needs it". A field marked
` + "`false`" + ` may still be one without which the operator cannot do anything useful.

`)
	sb.WriteString("---\n\n")
	sb.WriteString(text)
	if !strings.HasSuffix(text, "\n") {
		sb.WriteString("\n")
	}
	return write(filepath.Join(out, "crd-agent.md"), sb.String())
}
