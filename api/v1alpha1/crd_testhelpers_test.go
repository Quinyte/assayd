package v1alpha1

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/util/yaml"
)

// Helpers that read the *generated* CRD rather than the Go types, so the tests
// assert what a cluster will actually enforce.

func agentCRDPath() string {
	return filepath.Join("..", "..", "config", "crd", "plume.dev_agents.yaml")
}

func loadAgentCRD(t *testing.T) map[string]any {
	t.Helper()
	b, err := os.ReadFile(agentCRDPath())
	if err != nil {
		t.Fatalf("read generated CRD (run `make manifests`): %v", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Fatalf("parse generated CRD: %v", err)
	}
	return doc
}

// agentSpecSchema returns the openAPI properties for .spec.
func agentSpecSchema(t *testing.T) map[string]any {
	t.Helper()
	doc := loadAgentCRD(t)
	versions := doc["spec"].(map[string]any)["versions"].([]any)
	schema := versions[0].(map[string]any)["schema"].(map[string]any)["openAPIV3Schema"].(map[string]any)
	props := schema["properties"].(map[string]any)
	return props["spec"].(map[string]any)["properties"].(map[string]any)
}

// crdDeclaresDefault reports whether a dotted field path carries a default in
// the generated schema — i.e. whether the API server will fill it in.
func crdDeclaresDefault(t *testing.T, dotted string) bool {
	t.Helper()
	node := any(agentSpecSchema(t))
	parts := strings.Split(dotted, ".")
	for i, p := range parts {
		m, ok := node.(map[string]any)
		if !ok {
			return false
		}
		child, ok := m[p]
		if !ok {
			return false
		}
		if i == len(parts)-1 {
			cm, ok := child.(map[string]any)
			if !ok {
				return false
			}
			_, has := cm["default"]
			return has
		}
		cm, ok := child.(map[string]any)
		if !ok {
			return false
		}
		if inner, ok := cm["properties"]; ok {
			node = inner
		} else {
			node = cm
		}
	}
	return false
}

// crdOrAdmissionRejects reports whether a forbidden combination is blocked
// somewhere a developer will hit before it reaches a running cluster: either the
// CRD schema (CEL validation rules) or a shipped admission policy.
//
// Design 02 §3.2 makes sandbox+replicas>1 an admission error. Until the
// admission policy ships, the honest answer is false — and the DX test fails,
// which is the correct signal: the rule is designed but not yet enforced.
func crdOrAdmissionRejects(t *testing.T, rule string) bool {
	t.Helper()
	switch rule {
	case "sandbox+replicas":
		// CEL validation on the CRD is the cheapest enforcement point and needs no
		// webhook. Look for it in the generated schema.
		return strings.Contains(rawAgentCRD(t), "x-kubernetes-validations")
	default:
		t.Fatalf("unknown rule %q", rule)
		return false
	}
}

func rawAgentCRD(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(agentCRDPath())
	if err != nil {
		t.Fatalf("read generated CRD: %v", err)
	}
	return string(b)
}
