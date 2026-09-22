// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package refgen

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	yaml "go.yaml.in/yaml/v3"
)

// The chart files the reference is rendered from.
const (
	ValuesSource      = "charts/assayd/values.yaml"
	ValuesLocalSource = "charts/assayd/values-local.yaml"
	ChartSource       = "charts/assayd/Chart.yaml"
)

// value is one key in values.yaml.
type value struct {
	Path     string
	Default  string // the value as YAML, on one line where it fits
	Doc      string // the comment block above the key, and the one beside it
	Leaf     bool
	Depth    int
	Children int
}

// renderValues produces docs/reference/helm-values.md.
//
// It is BUILT rather than adopted, and the survey is the reason. Every
// off-the-shelf values documenter — helm-docs, Bitnami's readme-generator,
// helm-schema — reads descriptions only out of ITS OWN annotation syntax
// (`# -- `, `## @param`, `# @schema`). This chart's comments are long free-form
// paragraphs that explain why a default is what it is and what breaks when it
// is changed; adopting any of those tools would mean rewriting every one of
// them into a tag syntax, which is a change to the chart to suit a documenter.
// helm-docs is also GPL-3.0, which AGENTS.md rule 6 keeps out of the platform
// path. Reading the comment block that precedes a key — which is what a reader
// of the file itself reads — needs no convention at all.
func renderValues(root, out string) error {
	vals, err := readValues(filepath.Join(root, ValuesSource))
	if err != nil {
		return err
	}
	local, err := readValues(filepath.Join(root, ValuesLocalSource))
	if err != nil {
		return err
	}
	chartVersion, appVersion, err := chartVersions(filepath.Join(root, ChartSource))
	if err != nil {
		return err
	}

	var sb strings.Builder
	sb.WriteString(header("Chart values"))
	sb.WriteString(fmt.Sprintf(`
Every value in `+"`%s`"+`, its default, and what the chart says about it.

Chart version **%s**, appVersion **%s**. The chart version IS the platform version, and the
appVersion is the operator image tag.

The prose under each key is the comment that sits above it in `+"`values.yaml`"+`, reproduced rather
than re-written — so this page cannot say something the file does not. Where a comment explains why
a default is what it is, or what breaks when it is changed, that is the chart's own text.

Defaults are the values as written, which is the `+"**prod profile at the core tier**"+`. The chart also
ships `+"`%s`"+`; the keys it overrides are listed at the end.

`, ValuesSource, chartVersion, appVersion, ValuesLocalSource))

	intro, err := fileIntro(filepath.Join(root, ValuesSource))
	if err != nil {
		return err
	}
	if intro != "" {
		sb.WriteString("The file opens by saying:\n\n> " +
			strings.ReplaceAll(intro, "\n\n", "\n>\n> ") + "\n\n")
	}

	sb.WriteString("## Every key\n\n")
	sb.WriteString("| Key | Default |\n|---|---|\n")
	for _, v := range vals {
		if !v.Leaf {
			continue
		}
		sb.WriteString(fmt.Sprintf("| [`%s`](#%s) | %s |\n", v.Path, anchor(v.Path), code(v.Default)))
	}
	sb.WriteString("\n---\n\n")

	for _, v := range vals {
		level := strings.Repeat("#", min(v.Depth+2, 6))
		sb.WriteString(fmt.Sprintf("%s `%s`\n\n", level, v.Path))
		if v.Leaf {
			sb.WriteString("**Default:** " + code(v.Default) + "\n\n")
		} else {
			sb.WriteString(fmt.Sprintf("A group of %d key(s).\n\n", v.Children))
		}
		if v.Doc != "" {
			sb.WriteString(v.Doc + "\n\n")
		}
	}

	sb.WriteString("---\n\n## The local profile\n\n")
	sb.WriteString("`" + ValuesLocalSource + "` overrides these keys. Install with `-f " +
		ValuesLocalSource + "` to take them.\n\n")
	sb.WriteString("| Key | Local value | Default |\n|---|---|---|\n")
	defaults := map[string]string{}
	for _, v := range vals {
		defaults[v.Path] = v.Default
	}
	for _, v := range local {
		if !v.Leaf {
			continue
		}
		d, ok := defaults[v.Path]
		if !ok {
			d = "_not set in values.yaml_"
		} else {
			d = code(d)
		}
		sb.WriteString(fmt.Sprintf("| `%s` | %s | %s |\n", v.Path, code(v.Default), d))
	}
	sb.WriteString("\n")
	return write(filepath.Join(out, "helm-values.md"), sb.String())
}

func readValues(path string) ([]value, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if len(doc.Content) == 0 {
		return nil, fmt.Errorf("%s is empty", path)
	}
	var out []value
	walkMapping(doc.Content[0], "", 0, &out)
	return out, nil
}

// fileIntro is the comment block at the very top of a values file, above the
// first key. yaml.v3 hangs it on the document node, so walkMapping never sees
// it — and in this chart it is the sentence that says which profile the
// defaults are.
func fileIntro(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return "", err
	}
	head := doc.HeadComment
	if head == "" && len(doc.Content) > 0 {
		head = doc.Content[0].HeadComment
	}
	return commentText(head), nil
}

// walkMapping emits one entry per key, in file order, descending into nested
// mappings so that `operator.image.tag` is a key in its own right.
func walkMapping(n *yaml.Node, prefix string, depth int, out *[]value) {
	if n == nil || n.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		path := k.Value
		if prefix != "" {
			path = prefix + "." + k.Value
		}
		// The comment above a key belongs to the key node; the one beside it is
		// on whichever of the two the parser reached first.
		doc := commentText(k.HeadComment, k.LineComment, v.LineComment, v.HeadComment)
		nested := v.Kind == yaml.MappingNode && len(v.Content) > 0
		*out = append(*out, value{
			Path:     path,
			Default:  renderScalar(v),
			Doc:      doc,
			Leaf:     !nested,
			Depth:    depth,
			Children: len(v.Content) / 2,
		})
		if nested {
			walkMapping(v, path, depth+1, out)
		}
	}
}

// renderScalar prints a value the way values.yaml sets it.
func renderScalar(n *yaml.Node) string {
	switch n.Kind {
	case yaml.ScalarNode:
		if n.Tag == "!!null" || n.Value == "" {
			return `""`
		}
		return n.Value
	case yaml.SequenceNode:
		if len(n.Content) == 0 {
			return "[]"
		}
		var parts []string
		for _, c := range n.Content {
			parts = append(parts, renderScalar(c))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case yaml.MappingNode:
		if len(n.Content) == 0 {
			return "{}"
		}
		return "{…}"
	}
	return ""
}

// commentText turns YAML comment lines into prose.
//
// A blank comment line is a paragraph break in the file and stays one here; a
// line that is indented further than its neighbours is code the author laid out
// (a cosign command, a manifest fragment) and is kept verbatim in a fenced
// block, because reflowing it would break it.
func commentText(groups ...string) string {
	var paras []string
	var cur []string
	var codeBlock []string
	flushText := func() {
		if len(cur) > 0 {
			paras = append(paras, strings.Join(cur, " "))
			cur = nil
		}
	}
	flushCode := func() {
		if len(codeBlock) > 0 {
			paras = append(paras, "```\n"+strings.Join(codeBlock, "\n")+"\n```")
			codeBlock = nil
		}
	}
	for _, g := range groups {
		if strings.TrimSpace(g) == "" {
			continue
		}
		for _, raw := range strings.Split(g, "\n") {
			line := strings.TrimPrefix(strings.TrimSpace(raw), "#")
			if strings.TrimSpace(line) == "" {
				flushText()
				flushCode()
				continue
			}
			if strings.HasPrefix(line, "   ") { // three spaces past the "# "
				flushText()
				codeBlock = append(codeBlock, strings.TrimPrefix(line, "  "))
				continue
			}
			flushCode()
			cur = append(cur, strings.TrimSpace(line))
		}
		flushText()
		flushCode()
	}
	return strings.Join(paras, "\n\n")
}

func chartVersions(path string) (version, appVersion string, err error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", "", err
	}
	var c struct {
		Version    string `yaml:"version"`
		AppVersion string `yaml:"appVersion"`
	}
	if err := yaml.Unmarshal(b, &c); err != nil {
		return "", "", err
	}
	if c.Version == "" || c.AppVersion == "" {
		return "", "", fmt.Errorf("%s declares no version or appVersion", path)
	}
	return c.Version, c.AppVersion, nil
}

func code(s string) string {
	if s == "" {
		return "`\"\"`"
	}
	return "`" + strings.ReplaceAll(s, "`", "'") + "`"
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
