package envtest

import (
	"context"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/controller-runtime/pkg/client"

	plumev1alpha1 "github.com/ejs-5/plume/api/v1alpha1"
)

// The Agent CRD is the platform's front door. These tests hold it to a
// developer-experience contract against a real API server: the simplest useful
// agent is a handful of lines, the defaults carry the rest, and the mistakes a
// developer will actually make are rejected at `kubectl apply` with a message
// that says what to do — not at reconcile time, and not by a webhook we would
// have to run a pod for.

func applyYAML(t *testing.T, ns, doc string) (*plumev1alpha1.Agent, error) {
	t.Helper()
	var a plumev1alpha1.Agent
	if err := yaml.Unmarshal([]byte(doc), &a); err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	a.Namespace = ns
	return &a, k8s.Create(context.Background(), &a)
}

// The whole thing a developer must write to get a governed agent.
const minimalAgent = `
apiVersion: plume.dev/v1alpha1
kind: Agent
metadata:
  name: my-agent
spec:
  runtime:
    image: ghcr.io/acme/my-agent:1.0.0
`

func TestMinimalAgentIsAccepted(t *testing.T) {
	ns := newNamespace(t)
	a, err := applyYAML(t, ns, minimalAgent)
	if err != nil {
		t.Fatalf("the minimal agent must be accepted as written: %v", err)
	}

	// Read it back: the API server, not the operator, must have filled in the
	// decisions a developer should not have to make.
	var got plumev1alpha1.Agent
	if err := k8s.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: a.Name}, &got); err != nil {
		t.Fatalf("read back: %v", err)
	}

	for _, want := range []struct {
		field string
		got   any
		exp   any
	}{
		{"runtime.replicas", got.Spec.Runtime.Replicas, int32(1)},
		{"runtime.port", got.Spec.Runtime.Port, int32(8080)},
		{"card.path", got.Spec.Card.Path, "/.well-known/agent-card.json"},
	} {
		if want.got != want.exp {
			t.Errorf("%s defaulted to %v, want %v — a developer should not have to set this",
				want.field, want.got, want.exp)
		}
	}
}

// The realistic adoption case: an agent with domain knowledge and a tool. If
// this is not readable at a glance, the platform is too complex to adopt.
const realisticAgent = `
apiVersion: plume.dev/v1alpha1
kind: Agent
metadata:
  name: claims-triage
spec:
  runtime:
    image: ghcr.io/acme/claims-triage:2.1.0
  knowledge:
    - name: claims-policy
      version: "2026.3"
  tools:
    - name: claims-db
`

func TestRealisticAgentStaysReadable(t *testing.T) {
	ns := newNamespace(t)
	if _, err := applyYAML(t, ns, realisticAgent); err != nil {
		t.Fatalf("the realistic agent must be accepted as written: %v", err)
	}

	var typed int
	for _, l := range strings.Split(strings.TrimSpace(realisticAgent), "\n") {
		if strings.TrimSpace(l) != "" {
			typed++
		}
	}
	// An agent with pinned domain knowledge and a governed tool should still fit
	// on a screen. This ceiling is the DX budget, not an incidental measurement.
	if const_ceiling := 14; typed > const_ceiling {
		t.Errorf("a knowledge+tools agent is %d lines; the DX budget is %d", typed, const_ceiling)
	}
}

// The mistakes a developer will actually make, and what they should be told.
func TestMistakesAreRejectedWithActionableMessages(t *testing.T) {
	for _, tc := range []struct {
		name   string
		doc    string
		expect string // substring the error must contain, i.e. what we tell them to do
	}{
		{
			name: "neither runtime nor external",
			doc: `
apiVersion: plume.dev/v1alpha1
kind: Agent
metadata: {name: empty}
spec: {}
`,
			expect: "exactly one of spec.runtime",
		},
		{
			name: "both runtime and external",
			doc: `
apiVersion: plume.dev/v1alpha1
kind: Agent
metadata: {name: both}
spec:
  runtime: {image: ghcr.io/acme/a:1}
  external: {endpoint: "https://agent.example.com"}
`,
			expect: "exactly one of spec.runtime",
		},
		{
			name: "sandbox scaled out",
			doc: `
apiVersion: plume.dev/v1alpha1
kind: Agent
metadata: {name: scaled-sandbox}
spec:
  runtime:
    image: ghcr.io/acme/a:1
    replicas: 3
    sandbox: {profile: gvisor}
`,
			expect: "stateful singleton",
		},
		{
			name: "plaintext external endpoint",
			doc: `
apiVersion: plume.dev/v1alpha1
kind: Agent
metadata: {name: plaintext}
spec:
  external: {endpoint: "http://agent.example.com"}
`,
			expect: "spec.external.endpoint",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ns := newNamespace(t)
			_, err := applyYAML(t, ns, tc.doc)
			if err == nil {
				t.Fatal("must be rejected at apply time, not discovered at reconcile time")
			}
			if !strings.Contains(err.Error(), tc.expect) {
				t.Errorf("the error does not tell the developer what to do.\n got: %v\nwant substring: %q",
					err, tc.expect)
			}
		})
	}
}

// Status is where a developer looks when something is wrong. Twenty conditions
// is a lot to read, so the printer columns must answer "what state is this in?"
// without one.
func TestStatusIsLegibleWithoutReadingConditions(t *testing.T) {
	ns := newNamespace(t)
	a, err := applyYAML(t, ns, minimalAgent)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	a.Status.Phase = plumev1alpha1.PhaseHeld
	a.Status.CandidateRevision = "rev-2"
	if err := k8s.Status().Update(context.Background(), a); err != nil {
		t.Fatalf("status update — the status subresource must be enabled: %v", err)
	}

	var got plumev1alpha1.Agent
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &got); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got.Status.Phase != plumev1alpha1.PhaseHeld {
		t.Errorf("phase did not persist: %q", got.Status.Phase)
	}
	// Phase is the single field a developer reads first; it is a printer column
	// precisely so `kubectl get ag` answers the question on its own.
	if got.Status.CandidateRevision != "rev-2" {
		t.Errorf("candidate revision did not persist: %q", got.Status.CandidateRevision)
	}
}
