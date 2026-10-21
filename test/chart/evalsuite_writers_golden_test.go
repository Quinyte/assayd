// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package chart

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The golden rule for assayd-evalsuite-writers: the RENDERED policy and
// binding equal, byte for byte, the committed fixture for each value profile
// (test/chart/testdata/evalsuite-writers-*.yaml), comment lines aside. The
// fixtures were generated from the render and then checked field by field
// against Q6 (c) and the chart's other policies.
//
// The fixtures are a CHANGE DETECTOR, not a specification: a fixture
// regenerated with a mutation in it would pass here. The specification is
// the semantic assertions in evalsuite_writers_test.go and T61's own
// enumerated refusals and rows (test/envtest/evalsuite_writers_test.go),
// which hold whatever a fixture says. What the golden adds is that ANY
// added, removed or changed field fails until someone changes a fixture by
// hand, in the commit that changes the policy. Two reviews each found a
// field that narrowed the check passing every test that enumerated
// refusals, which is why both lines exist.
//
// The golden sees only the profiles below. A field templated on any other
// value would escape it, so TestTheEvalSuiteWritersTemplateIsNotConditional
// pins the template's source: no template action but the labels include.
//
// The fixtures carry `app.kubernetes.io/version`, which the chart renders from
// Chart.yaml's appVersion. The release workflow rewrites appVersion at publish
// time without running these tests (.github/workflows/release.yml); a
// committed appVersion bump fails here, and the failure says so.

// evalSuiteWriterProfiles are the value profiles the chart has, each with the
// fixture its render must equal.
var evalSuiteWriterProfiles = []struct {
	name, fixture string
	args          []string
}{
	{"default", "evalsuite-writers-default.yaml", nil},
	{"extraOperators set", "evalsuite-writers-extra-operators.yaml",
		[]string{"--set", "admission.extraOperators={tenant-operator}"}},
	{"gateway enabled", "evalsuite-writers-gateway.yaml",
		[]string{"--set", "gateway.enabled=true", "--set", "gateway.servingUrl=http://assayd-gateway.agentgateway-system.svc"}},
	{"local profile", "evalsuite-writers-local.yaml",
		[]string{"-f", filepath.Join(chartPath, "values-local.yaml")}},
}

// renderedEvalSuiteWriters is the policy and the binding as Helm renders them,
// with comment lines dropped, joined by "---".
func renderedEvalSuiteWriters(t *testing.T, args ...string) string {
	t.Helper()
	out, err := helmTemplate(t, args...)
	if err != nil {
		t.Fatalf("helm template %v: %v", args, err)
	}
	return evalSuiteWritersDocs(t, out)
}

// evalSuiteWritersDocs picks assayd-evalsuite-writers' policy and binding out
// of a render, in that order.
func evalSuiteWritersDocs(t *testing.T, rendered string) string {
	t.Helper()
	var policy, binding string
	for _, chunk := range strings.Split(rendered, "\n---\n") {
		var kept []string
		for _, l := range strings.Split(chunk, "\n") {
			if !strings.HasPrefix(strings.TrimSpace(l), "#") && strings.TrimSpace(l) != "---" {
				kept = append(kept, l)
			}
		}
		doc := strings.TrimSpace(strings.Join(kept, "\n")) + "\n"
		if !strings.Contains(doc, "\n  name: assayd-evalsuite-writers\n") {
			continue
		}
		switch {
		case strings.Contains(doc, "kind: ValidatingAdmissionPolicy\n"):
			policy = doc
		case strings.Contains(doc, "kind: ValidatingAdmissionPolicyBinding\n"):
			binding = doc
		}
	}
	if policy == "" || binding == "" {
		t.Fatal("the chart renders no ValidatingAdmissionPolicy and binding named assayd-evalsuite-writers")
	}
	return policy + "---\n" + binding
}

func TestTheEvalSuiteWritersPolicyIsExactlyItsFixture(t *testing.T) {
	for _, p := range evalSuiteWriterProfiles {
		t.Run(p.name, func(t *testing.T) {
			want, err := os.ReadFile(filepath.Join("testdata", p.fixture))
			if err != nil {
				t.Fatalf("read the fixture: %v", err)
			}
			got, fixture := renderedEvalSuiteWriters(t, p.args...), withoutHeader(string(want))
			if got != fixture && versionless(got) == versionless(fixture) {
				t.Errorf("assayd-evalsuite-writers differs from testdata/%s only in app.kubernetes.io/version: "+
					"appVersion changed? Regenerate the version label in testdata/evalsuite-writers-*.yaml to "+
					"match Chart.yaml's appVersion; nothing else in the policy changed", p.fixture)
				return
			}
			if got != fixture {
				t.Errorf("assayd-evalsuite-writers renders differently from testdata/%s. Any field the "+
					"fixture does not hold can exempt someone from Q6 (c)'s check; change the fixture "+
					"only by hand, in the commit that changes the policy, and say why.\n--- rendered\n%s\n--- fixture\n%s",
					p.fixture, got, want)
			}
		})
	}
}

// withoutHeader drops a fixture's leading comment lines, its SPDX header, and
// nothing else: a comment further down stays and fails the comparison, since
// the render it is compared with carries none.
func withoutHeader(fixture string) string {
	for strings.HasPrefix(fixture, "#") {
		_, rest, found := strings.Cut(fixture, "\n")
		if !found {
			return ""
		}
		fixture = rest
	}
	return fixture
}

var versionLabel = regexp.MustCompile(`(?m)^(\s*app\.kubernetes\.io/version: ).*$`)

// versionless replaces the version label's value, so a difference in it alone
// can be told from any other.
func versionless(doc string) string { return versionLabel.ReplaceAllString(doc, "${1}VERSION") }

// The golden compares four value profiles, and a field templated on any other
// value -- `{{ if .Values.admission.toolRouteWriters.users }}`, which CI's e2e
// sets -- would render past all four. So the policy's SOURCE, from its first
// comment to the end of its binding, which is the end of the file, carries no
// template action but the chart's labels include, once in the policy and once
// in the binding. So its spec, and its binding's, render the same under every
// value set; only the labels change, carrying the profile and the appVersion.
func TestTheEvalSuiteWritersTemplateIsNotConditional(t *testing.T) {
	src, err := os.ReadFile(filepath.Join(chartPath, "templates", "admission.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	const marker = "# Design 16 §1.1, Q6 (c)"
	i := strings.Index(string(src), marker)
	if i < 0 {
		t.Fatalf("templates/admission.yaml no longer opens assayd-evalsuite-writers with %q", marker)
	}
	section := string(src[i:])
	if !strings.HasSuffix(strings.TrimSpace(section), "policyName: assayd-evalsuite-writers\n  validationActions: [\"Deny\"]") {
		t.Fatal("assayd-evalsuite-writers' binding is no longer the end of templates/admission.yaml, so this " +
			"cannot tell where its source ends")
	}
	const labels = `{{- include "assayd.labels" . | nindent 4 }}`
	if n := strings.Count(section, labels); n != 2 {
		t.Errorf("assayd-evalsuite-writers includes the chart's labels %d times, want 2 (policy and binding)", n)
	}
	if rest := strings.ReplaceAll(section, labels, ""); strings.Contains(rest, "{{") {
		t.Errorf("assayd-evalsuite-writers' source holds a template action besides the labels include, so it can "+
			"render a field under a value the golden fixtures do not cover:\n%s", rest)
	}
}
