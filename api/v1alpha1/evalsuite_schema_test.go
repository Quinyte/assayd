// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	structuralschema "k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/cel"
	"k8s.io/apimachinery/pkg/util/validation/field"
	celconfig "k8s.io/apiserver/pkg/apis/cel"
	"sigs.k8s.io/yaml"
)

// These tests evaluate the GENERATED EvalSuite CRD's CEL rules in process, with
// the API server's own validator, so they need no control plane. The envtest
// rows (test/envtest/evalsuite_admission_test.go) prove the same CRD installs
// and refuses through a real API server; these prove the rules' logic at the
// unit layer, which is where design 16 §1.1 owes the echo refusal.

// generatedCRD reads a CRD controller-gen wrote under config/crd.
func generatedCRD(t *testing.T, file string) *apiextensionsv1.CustomResourceDefinition {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "config", "crd", file))
	if err != nil {
		t.Fatalf("read generated CRD %s (run `make manifests`): %v", file, err)
	}
	var crd apiextensionsv1.CustomResourceDefinition
	if err := yaml.Unmarshal(b, &crd); err != nil {
		t.Fatalf("parse generated CRD %s: %v", file, err)
	}
	return &crd
}

// celRefusals validates obj against the generated CRD's v1alpha1 schema with
// the API server's CEL validator, and returns each refusal's message.
func celRefusals(t *testing.T, file string, obj map[string]any) []string {
	t.Helper()
	crd := generatedCRD(t, file)
	var internal apiextensions.JSONSchemaProps
	if err := apiextensionsv1.Convert_v1_JSONSchemaProps_To_apiextensions_JSONSchemaProps(
		crd.Spec.Versions[0].Schema.OpenAPIV3Schema, &internal, nil); err != nil {
		t.Fatalf("convert schema: %v", err)
	}
	s, err := structuralschema.NewStructural(&internal)
	if err != nil {
		t.Fatalf("structural schema: %v", err)
	}
	v := cel.NewValidator(s, true, celconfig.PerCallLimit)
	if v == nil {
		t.Fatalf("%s carries no CEL rule at all", file)
	}
	errs, _ := v.Validate(context.Background(), field.NewPath(""), s, obj, nil, celconfig.RuntimeCELCostBudget)
	var out []string
	for _, e := range errs {
		if e.Type == field.ErrorTypeInternal {
			t.Fatalf("CEL evaluation failed, which is not a refusal: %v", e)
		}
		out = append(out, e.Error())
	}
	return out
}

func suiteWithCase(c map[string]any) map[string]any {
	return map[string]any{
		"apiVersion": "assayd.dev/v1alpha1",
		"kind":       "EvalSuite",
		"metadata":   map[string]any{"name": "smoke", "namespace": "team-a"},
		"spec": map[string]any{
			"dataset": map[string]any{"cases": []any{c}},
		},
	}
}

// T9: an agent that echoes its input would pass a case whose message already
// satisfies its expectation, and test/responder is such an agent. Admission
// refuses that case, for each kind of expectation; the design's minimal
// example, and a case of each kind that does not echo, are admitted.
func TestEvalSuiteRefusesACaseItsOwnMessageSatisfies(t *testing.T) {
	const echo = "already satisfies its own expectation"
	for _, tc := range []struct {
		name    string
		c       map[string]any
		refused bool
	}{
		{"the minimal example", map[string]any{"id": "arithmetic", "message": "What is 2 + 2?",
			"expect": map[string]any{"contains": "4"}}, false},
		{"an equals case", map[string]any{"id": "health", "message": "status",
			"expect": map[string]any{"equals": "ok"}}, false},
		{"a message containing its contains", map[string]any{"id": "echo", "message": "say hello",
			"expect": map[string]any{"contains": "hello"}}, true},
		{"a message equal to its equals", map[string]any{"id": "echo", "message": "hello",
			"expect": map[string]any{"equals": "hello"}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := celRefusals(t, "assayd.dev_evalsuites.yaml", suiteWithCase(tc.c))
			echoed := false
			for _, m := range got {
				echoed = echoed || strings.Contains(m, echo)
			}
			if tc.refused && !echoed {
				t.Errorf("not refused as an echo; refusals: %v", got)
			}
			if !tc.refused && len(got) > 0 {
				t.Errorf("a well-formed case was refused: %v", got)
			}
		})
	}
}

// The eight fields design 16 §1.1 adds to status.eval are fields of the AGENT
// CRD. One missing from the schema is pruned on every write without an error,
// which is the stale-CRD failure §1.1 step 11 exists to catch — so the
// generated schema is held to the list here, `claim` in cases' items included.
func TestStatusEvalCarriesTheSlicesRecord(t *testing.T) {
	eval := descend(t, agentSchema(t), "status.eval")
	props := eval["properties"].(map[string]any)
	for _, f := range []string{"suiteDigest", "endpointPath", "cardDigest", "blocked",
		"blockedFor", "verdict", "cases", "failed"} {
		if _, ok := props[f]; !ok {
			t.Errorf("status.eval carries no %q", f)
		}
	}
	for path, fields := range map[string][]string{
		"status.eval.blocked":    {"reason", "message"},
		"status.eval.blockedFor": {"revisionDigest", "suiteDigest", "lockDeadline"},
		"status.eval.cases":      {"id", "outcome", "reason", "claim"},
		"status.eval.failed":     {"revisionDigest", "suiteDigest"},
	} {
		node := descend(t, agentSchema(t), path)
		p, _ := node["properties"].(map[string]any)
		for _, f := range fields {
			if _, ok := p[f]; !ok {
				t.Errorf("%s carries no %q", path, f)
			}
		}
	}
	verdict := props["verdict"].(map[string]any)
	if got := verdict["enum"]; !sameStrings(got, []string{"Running", "Passed", "Failed"}) {
		t.Errorf("status.eval.verdict enum is %v, want Running, Passed, Failed", got)
	}
	for f, want := range map[string]string{"cases": "20", "failed": "10"} {
		if got := fmt.Sprint(props[f].(map[string]any)["maxItems"]); got != want {
			t.Errorf("status.eval.%s maxItems is %v, want %v", f, got, want)
		}
	}
}

func sameStrings(got any, want []string) bool {
	l, ok := got.([]any)
	if !ok || len(l) != len(want) {
		return false
	}
	for i := range want {
		if l[i] != want[i] {
			return false
		}
	}
	return true
}
