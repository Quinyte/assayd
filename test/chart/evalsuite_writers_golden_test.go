// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package chart

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The golden rule for assayd-evalsuite-writers: the RENDERED policy and
// binding equal, byte for byte, the committed fixture for each value profile
// (test/chart/testdata/evalsuite-writers-*.yaml), comment lines aside. The
// fixtures hold the exact expected YAML, checked by hand when written. Two
// reviews each found a field that narrowed the check -- matchConditions, a
// binding's matchResources, a rule's resourceNames -- passing every test
// that enumerated refusals, so nothing is enumerated here: ANY added, removed
// or changed field fails, and a change to the policy changes its fixtures in
// the same commit. The semantic assertions in evalsuite_writers_test.go stay
// as a second line. test/envtest's T61 holds the default render to the same
// fixture before it installs it.

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
			if got := renderedEvalSuiteWriters(t, p.args...); got != withoutHeader(string(want)) {
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
