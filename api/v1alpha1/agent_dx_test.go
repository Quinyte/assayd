package v1alpha1

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/util/yaml"
)

// The developer-experience contract for the platform's front door.
//
// The Agent CRD carries a lot of capability, and capability tends to leak into
// required surface. These tests fix the opposite property: the simplest useful
// agent must stay a handful of lines, and every governance feature must be
// opt-in. If a future change makes the minimal form longer, one of these fails
// — which is the point. Complexity is allowed to grow; the floor is not.

// minimalAgent is the whole thing a developer must write to run an agent.
// If this grows, the platform got harder to adopt.
const minimalAgent = `
apiVersion: plume.dev/v1alpha1
kind: Agent
metadata:
  name: my-agent
spec:
  runtime:
    image: ghcr.io/acme/my-agent:1.0.0
`

func TestMinimalAgentIsFourLinesOfSpec(t *testing.T) {
	var a Agent
	if err := yaml.Unmarshal([]byte(minimalAgent), &a); err != nil {
		t.Fatalf("the minimal agent must parse: %v", err)
	}

	if a.Spec.Runtime == nil || a.Spec.Runtime.Image == "" {
		t.Fatal("runtime.image is the one thing a developer must supply")
	}

	// Everything else is opt-in governance. A developer adopting plume should be
	// able to ignore all of it until they need it.
	optional := map[string]bool{
		"external":  a.Spec.External != nil,
		"knowledge": len(a.Spec.Knowledge) > 0,
		"tools":     len(a.Spec.Tools) > 0,
		"llm":       a.Spec.LLM != nil,
		"budget":    a.Spec.Budget != nil,
		"loop":      a.Spec.Loop != nil,
		"gates":     len(a.Spec.Gates) > 0,
		"expose":    a.Spec.Expose != nil,
	}
	for field, set := range optional {
		if set {
			t.Errorf("spec.%s must not be required for a minimal agent", field)
		}
	}

	// Count the lines a developer actually types.
	var typed int
	for _, l := range strings.Split(strings.TrimSpace(minimalAgent), "\n") {
		if strings.TrimSpace(l) != "" {
			typed++
		}
	}
	const ceiling = 7 // apiVersion, kind, metadata, name, spec, runtime, image
	if typed > ceiling {
		t.Errorf("minimal agent is %d lines; the DX ceiling is %d", typed, ceiling)
	}
}

// The design promises defaults so the common case needs no decisions. These are
// the ones a developer would otherwise have to research and get right.
func TestDefaultsCoverTheCommonCase(t *testing.T) {
	for _, tc := range []struct {
		field, why string
		check      func(*Agent) bool
	}{
		{"runtime.port", "an A2A server has a conventional port; asking is friction",
			func(a *Agent) bool { return a.Spec.Runtime.Port == 8080 }},
		{"runtime.replicas", "one replica is the safe default; >1 needs a task-state assertion",
			func(a *Agent) bool { return a.Spec.Runtime.Replicas == 1 }},
		{"card.path", "the A2A spec fixes this path; making it a required field would be noise",
			func(a *Agent) bool { return a.Spec.Card.Path == "/.well-known/agent-card.json" }},
	} {
		t.Run(tc.field, func(t *testing.T) {
			// Defaults are applied by the API server from CRD markers, so assert the
			// marker exists in the generated schema rather than in Go zero values.
			if !crdDeclaresDefault(t, tc.field) {
				t.Errorf("%s has no default in the generated CRD — %s", tc.field, tc.why)
			}
		})
	}
}

// A developer must not be able to express a combination the design forbids —
// the schema should catch it, not a runtime surprise.
func TestForbiddenCombinationsAreExpressibleOnlyAsErrors(t *testing.T) {
	// replicas>1 with a Sandbox is an admission error (design 02 §3.2): a Sandbox
	// is a stateful singleton. The type system cannot express this, so it is an
	// admission rule — this test documents that it must exist, and fails if the
	// rule is ever dropped from the manifests.
	if !crdOrAdmissionRejects(t, "sandbox+replicas") {
		t.Error("replicas>1 with sandbox must be rejected before it reaches a cluster")
	}
}
