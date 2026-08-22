package v1alpha1

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/util/yaml"
)

// These tests read the *generated* CRD, because that is what a cluster enforces
// and what a developer's kubectl talks to. They assert properties of the schema
// itself: a fixture-counting test proves only that the fixture is unchanged, so
// there are none here.

func agentCRD(t *testing.T) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "config", "crd", "plume.dev_agents.yaml"))
	if err != nil {
		t.Fatalf("read generated CRD (run `make manifests`): %v", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Fatalf("parse generated CRD: %v", err)
	}
	return doc
}

func agentVersion(t *testing.T) map[string]any {
	t.Helper()
	versions := agentCRD(t)["spec"].(map[string]any)["versions"].([]any)
	return versions[0].(map[string]any)
}

func agentSchema(t *testing.T) map[string]any {
	t.Helper()
	return agentVersion(t)["schema"].(map[string]any)["openAPIV3Schema"].(map[string]any)
}

// descend walks a dotted path through openAPI `properties`, transparently
// stepping into array `items`.
func descend(t *testing.T, node map[string]any, dotted string) map[string]any {
	t.Helper()
	cur := node
	for _, p := range strings.Split(dotted, ".") {
		props, ok := cur["properties"].(map[string]any)
		if !ok {
			t.Fatalf("no properties at %q while descending %q", p, dotted)
		}
		next, ok := props[p].(map[string]any)
		if !ok {
			t.Fatalf("no such field %q while descending %q", p, dotted)
		}
		if items, ok := next["items"].(map[string]any); ok {
			next = items
		}
		cur = next
	}
	return cur
}

// The front door's DX budget, stated as a schema property rather than as a line
// count: adding a required field anywhere on the common path fails this test the
// moment `make manifests` runs.
func TestOnlyTheUnavoidableFieldsAreRequired(t *testing.T) {
	spec := descend(t, agentSchema(t), "spec")

	if req, ok := spec["required"]; ok {
		t.Errorf("spec.required is %v — every top-level field must be optional so a "+
			"developer opts into governance rather than out of it", req)
	}

	// Once a developer opts into a block, these are the only things we may demand
	// of them. Anything else must be defaulted or optional.
	allowed := map[string]bool{
		"runtime.image":           true, // there is no agent without an image
		"external.endpoint":       true, // nor an external one without a URL
		"knowledge.name":          true,
		"knowledge.version":       true, // an unpinned graph is the thing we refuse
		"tools.name":              true,
		"gates.evalSuiteRef":      true,
		"llm.fallback.provider":   true,
		"llm.fallback.model":      true,
		"runtime.sandbox.profile": true,
	}

	// Every required field is a demand, whether its type is a scalar or an object:
	// requiring a block obliges the developer to fill the block in.
	var found []string
	var walk func(node map[string]any, path string)
	walk = func(node map[string]any, path string) {
		props, _ := node["properties"].(map[string]any)
		if req, ok := node["required"].([]any); ok {
			for _, r := range req {
				found = append(found, path+"."+r.(string))
			}
		}
		for k, v := range props {
			sub, ok := v.(map[string]any)
			if !ok {
				continue
			}
			if items, ok := sub["items"].(map[string]any); ok {
				sub = items
			}
			walk(sub, path+"."+k)
		}
	}

	// Only plume's own fields; corev1 EnvVar and ResourceRequirements carry their
	// own required leaves and are not ours to police.
	for _, top := range []string{"runtime", "external", "knowledge", "tools", "gates", "llm", "card", "loop", "expose"} {
		walk(descend(t, agentSchema(t), "spec."+top), top)
	}

	for _, f := range found {
		if strings.Contains(f, ".env.") || strings.Contains(f, ".envFrom.") || strings.Contains(f, ".resources.") {
			continue // corev1 subtrees
		}
		if !allowed[f] {
			t.Errorf("spec.%s is required and not on the allowed list — default it, make "+
				"it optional, or justify it by adding it to this test", f)
		}
	}
}

// Design 02 §3.1 fixes what `kubectl get ag` shows. The argument that a
// twenty-condition status stays legible rests entirely on these columns, so they
// are a contract, not a convenience.
func TestPrinterColumnsMatchTheDesign(t *testing.T) {
	want := []string{"Phase", "Active", "Eval", "Cost/Day", "Age"}

	raw, _ := agentVersion(t)["additionalPrinterColumns"].([]any)
	var got []string
	for _, c := range raw {
		got = append(got, c.(map[string]any)["name"].(string))
	}

	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("printer columns are %v, design 02 §3.1 specifies %v.\n"+
			"These columns are the whole reason 20 conditions is defensible: Eval and "+
			"Cost/Day are exactly what a developer would otherwise read conditions for.", got, want)
	}
}

// The condition vocabulary is a flat []metav1.Condition, so the schema cannot
// enforce it. This test is the enforcement.
func TestConditionVocabularyIsClosed(t *testing.T) {
	want := designConditions() // declared in agent_types.go beside the constants
	if len(want) != 20 {
		t.Fatalf("design 02 §3.1 declares 20 conditions; the constant list has %d", len(want))
	}
	seen := map[string]bool{}
	for _, c := range want {
		if seen[c] {
			t.Errorf("condition %q is declared twice", c)
		}
		seen[c] = true
	}
}
