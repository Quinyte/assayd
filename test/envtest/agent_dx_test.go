// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package envtest

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/controller-runtime/pkg/client"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
)

// The Agent CRD is the platform's front door. These tests hold it to a
// developer-experience contract against a real API server: the simplest useful
// agent is a handful of lines, the defaults carry the rest, and the mistakes a
// developer will actually make are rejected at `kubectl apply` with a message
// that says what to do — not at reconcile time, and not by a webhook we would
// have to run a pod for.

func applyYAML(t *testing.T, ns, doc string) (*assaydv1alpha1.Agent, error) {
	t.Helper()
	var a assaydv1alpha1.Agent
	if err := yaml.Unmarshal([]byte(doc), &a); err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	a.Namespace = ns
	return &a, k8s.Create(context.Background(), &a)
}

// The whole thing a developer must write to get a governed agent.
const minimalAgent = `
apiVersion: assayd.dev/v1alpha1
kind: Agent
metadata:
  name: my-agent
spec:
  runtime:
    image: ghcr.io/acme/my-agent@sha256:6d5d9666a268df6f000000000000000000000000000000000000000000000000
`

func TestMinimalAgentIsAccepted(t *testing.T) {
	ns := newNamespace(t)
	a, err := applyYAML(t, ns, minimalAgent)
	if err != nil {
		t.Fatalf("the minimal agent must be accepted as written: %v", err)
	}

	// Read it back: the API server, not the operator, must have filled in the
	// decisions a developer should not have to make.
	var got assaydv1alpha1.Agent
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
apiVersion: assayd.dev/v1alpha1
kind: Agent
metadata:
  name: claims-triage
spec:
  runtime:
    image: ghcr.io/acme/claims-triage@sha256:79b86a71cc2ef03d000000000000000000000000000000000000000000000000
  knowledge:
    - name: claims-policy
      version: "2026.3"
  tools:
    - name: claims-db
`

func TestRealisticAgentIsAccepted(t *testing.T) {
	ns := newNamespace(t)
	if _, err := applyYAML(t, ns, realisticAgent); err != nil {
		t.Fatalf("the realistic agent must be accepted as written: %v", err)
	}
	// How short this document is, is a schema property, asserted in
	// api/v1alpha1/schema_contract_test.go. Counting the lines of the fixture
	// above would only prove the fixture had not been edited.
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
apiVersion: assayd.dev/v1alpha1
kind: Agent
metadata: {name: empty}
spec: {}
`,
			expect: "exactly one of spec.runtime",
		},
		{
			name: "both runtime and external",
			doc: `
apiVersion: assayd.dev/v1alpha1
kind: Agent
metadata: {name: both}
spec:
  runtime: {image: ghcr.io/acme/a@sha256:3bda1c750240ee09000000000000000000000000000000000000000000000000}
  external: {endpoint: "https://agent.example.com"}
`,
			expect: "exactly one of spec.runtime",
		},
		{
			name: "sandbox scaled out",
			doc: `
apiVersion: assayd.dev/v1alpha1
kind: Agent
metadata: {name: scaled-sandbox}
spec:
  runtime:
    image: ghcr.io/acme/a@sha256:3bda1c750240ee09000000000000000000000000000000000000000000000000
    replicas: 3
    sandbox: {profile: gvisor}
`,
			expect: "stateful singleton",
		},
		{
			name: "card path is a URL, not a path",
			doc: `
apiVersion: assayd.dev/v1alpha1
kind: Agent
metadata: {name: card-url}
spec:
  runtime: {image: ghcr.io/acme/a@sha256:3bda1c750240ee09000000000000000000000000000000000000000000000000}
  card: {path: "https://elsewhere.example.com/card.json"}
`,
			expect: "not a URL",
		},
		{
			// The grammar and the leading slash are ONE CEL rule, so this
			// reads the same prose the URL case does — and the assertion is on
			// the prose, not on "spec.card.path", which the pattern, the CEL
			// rule and the length error all carry alike.
			name: "card path carries a query",
			doc: `
apiVersion: assayd.dev/v1alpha1
kind: Agent
metadata: {name: card-query}
spec:
  runtime: {image: ghcr.io/acme/a@sha256:3bda1c750240ee09000000000000000000000000000000000000000000000000}
  card: {path: "/card.json?whose=theirs"}
`,
			expect: "never be read as a query or a fragment",
		},
		{
			// maxLength's TooLong is BLOCKING in apiextensions: when it fires
			// the API server reports "some validation rules were not checked
			// because the object was invalid" and skips every CEL rule, so
			// this case can never read the grammar rule's prose. The bound
			// stays a maxLength anyway (CardSpec says why), so the assertion
			// is on the API server's own wording — which names the number, and
			// therefore dies with the number.
			name: "card path longer than the bound",
			doc: `
apiVersion: assayd.dev/v1alpha1
kind: Agent
metadata: {name: card-long}
spec:
  runtime: {image: ghcr.io/acme/a@sha256:3bda1c750240ee09000000000000000000000000000000000000000000000000}
  card: {path: "/` + strings.Repeat("a", 1024) + `"}
`,
			expect: "may not be more than 1024",
		},
		{
			name: "name too long for a derived workload name",
			doc: `
apiVersion: assayd.dev/v1alpha1
kind: Agent
metadata: {name: this-agent-name-is-deliberately-far-too-long-to-fit-a-label}
spec:
  runtime: {image: ghcr.io/acme/a@sha256:3bda1c750240ee09000000000000000000000000000000000000000000000000}
`,
			expect: "at most 52 characters",
		},
		{
			name: "plaintext external endpoint",
			doc: `
apiVersion: assayd.dev/v1alpha1
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

// A rule that only ever refuses is pinned from one side. Narrowing the card
// path's character class to `^/[A-Za-z0-9._~/-]*$`, and tightening its bound to
// 64, each survived a full envtest run of the branch that added them: nothing
// asked what the rule ADMITS. These cases ask (design 02 A76).
func TestTheCardPathGrammarAndBoundAdmitWhatTheySay(t *testing.T) {
	for _, tc := range []struct {
		name, path string
	}{
		// Every character the grammar claims beyond the alphanumerics: the
		// unreserved set, the sub-delimiters, ':' and '@'. A narrower class
		// refuses this path.
		{"the sub-delimiters, ':' and '@'", "/cards/v1:2@site/a!$&'()*+,;=-._~.json"},
		// The bound itself, exactly: 1024 characters including the slash.
		{"a path of exactly 1024 characters", "/" + strings.Repeat("a", 1023)},
		// Stated in the field's own documentation, so it is measured here
		// rather than asserted in prose alone: dot segments are NOT excluded.
		// internal/controller's TestTheCardPathCannotNameTheHost measures
		// where such a path actually goes.
		{"dot segments, which the grammar does not exclude", "/../../card.json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ns := newNamespace(t)
			a := &assaydv1alpha1.Agent{
				ObjectMeta: metav1.ObjectMeta{Name: "carded", Namespace: ns},
				Spec: assaydv1alpha1.AgentSpec{
					Runtime: &assaydv1alpha1.AgentRuntime{Image: admissionImage},
					Card:    assaydv1alpha1.CardSpec{Path: tc.path},
				},
			}
			if err := k8s.Create(context.Background(), a); err != nil {
				t.Fatalf("the card path %q must be admitted — the grammar and the bound say so, "+
					"and an author reading the field's documentation will write one:\n%v", tc.path, err)
			}
			var got assaydv1alpha1.Agent
			if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &got); err != nil {
				t.Fatalf("read back: %v", err)
			}
			if got.Spec.Card.Path != tc.path {
				t.Errorf("the stored path is %q, not the one written, %q", got.Spec.Card.Path, tc.path)
			}
		})
	}
}

// `path: ""` was accepted before design 02 A76 and is refused by it. A field
// present and empty is not a field absent, so defaulting never reaches it and
// the grammar rule sees "". It is written UNSTRUCTURED because the typed field
// is omitempty: a typed create drops it, the API server then applies the
// default, and the case would silently test nothing.
func TestAnEmptyCardPathIsRefused(t *testing.T) {
	ns := newNamespace(t)
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "assayd.dev/v1alpha1",
		"kind":       "Agent",
		"metadata":   map[string]any{"name": "empty-path", "namespace": ns},
		"spec": map[string]any{
			"runtime": map[string]any{"image": admissionImage},
			"card":    map[string]any{"path": ""},
		},
	}}
	err := k8s.Create(context.Background(), u)
	if err == nil {
		t.Fatal(`path: "" must be refused: it is not the default, and the operator would fetch "/"`)
	}
	if !strings.Contains(err.Error(), "not a URL") {
		t.Errorf("the error does not tell the developer what to do.\n got: %v", err)
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

	a.Status.Phase = assaydv1alpha1.PhaseHeld
	a.Status.CandidateRevision = "rev-2"
	if err := k8s.Status().Update(context.Background(), a); err != nil {
		t.Fatalf("status update — the status subresource must be enabled: %v", err)
	}

	var got assaydv1alpha1.Agent
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &got); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got.Status.Phase != assaydv1alpha1.PhaseHeld {
		t.Errorf("phase did not persist: %q", got.Status.Phase)
	}
	// Phase is the single field a developer reads first; it is a printer column
	// precisely so `kubectl get ag` answers the question on its own.
	if got.Status.CandidateRevision != "rev-2" {
		t.Errorf("candidate revision did not persist: %q", got.Status.CandidateRevision)
	}
}

// A tool binding names a server in the agent's own namespace. There is no
// namespace field, and this test is why: design 24 §4.1 derives the `can_call`
// ReBAC tuple FROM the binding, so a reference the schema accepts is a grant the
// platform mints. Reintroducing the field would let anyone who can create an
// Agent in one namespace reach a tool in another.
func TestToolBindingCannotReachAnotherNamespace(t *testing.T) {
	ns := newNamespace(t)
	doc := `
apiVersion: assayd.dev/v1alpha1
kind: Agent
metadata: {name: reacher}
spec:
  runtime: {image: ghcr.io/acme/a@sha256:3bda1c750240ee09000000000000000000000000000000000000000000000000}
  tools:
    - name: prod-payments-db
      namespace: finance
`
	// Sent unstructured on purpose. Unmarshalling into the Go type would drop the
	// field client-side and the request would never carry it — proving nothing
	// about what the cluster does with the YAML a developer actually writes.
	var obj unstructured.Unstructured
	if err := yaml.Unmarshal([]byte(doc), &obj.Object); err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	obj.SetNamespace(ns)

	// kubectl has sent fieldValidation=Strict since 1.25, so this is what a
	// developer gets back.
	err := k8s.Create(context.Background(), obj.DeepCopy(), client.FieldValidation("Strict"))
	if err == nil {
		t.Fatal("a cross-namespace tool reference was accepted — design 24 §4.1 mints a " +
			"can_call tuple from this binding, so this is privilege escalation")
	}
	if !strings.Contains(err.Error(), "namespace") {
		t.Errorf("rejected, but not for the namespace field: %v", err)
	}

	// Without strict validation the API server *prunes* the field instead. The
	// escalation is still impossible — a pruned field never reaches the operator,
	// so no tuple can be minted from it — but the developer is not told, which is
	// the silence NFR-8 forbids. Pin the pruning so that if `namespace` is ever
	// added back for another purpose, this test fails rather than going quiet.
	lenient := obj.DeepCopy()
	lenient.SetName("reacher-lenient")
	if err := k8s.Create(context.Background(), lenient); err != nil {
		t.Fatalf("lenient create: %v", err)
	}
	var got assaydv1alpha1.Agent
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(lenient), &got); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(got.Spec.Tools) != 1 || got.Spec.Tools[0].Name != "prod-payments-db" {
		t.Fatalf("unexpected tools after pruning: %+v", got.Spec.Tools)
	}
	raw, err := json.Marshal(got.Spec.Tools[0])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), "finance") {
		t.Error("the namespace field survived pruning — it is reaching the operator")
	}
}

// Design 02 §3.1's printer columns must resolve against a real object, not just
// exist as markers: a JSONPath naming a field that was never added renders empty
// forever and nobody notices.
func TestPrinterColumnsResolveAgainstRealStatus(t *testing.T) {
	ns := newNamespace(t)
	a, err := applyYAML(t, ns, minimalAgent)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	spent := "12.40"
	a.Status.Phase = assaydv1alpha1.PhaseCanary
	a.Status.ActiveRevision = "rev-1"
	a.Status.Eval = &assaydv1alpha1.EvalStatus{Score: "0.94", Suite: "pa-regression", Revision: "rev-2"}
	a.Status.Budget = &assaydv1alpha1.BudgetStatus{USDSpentToday: &spent}
	if err := k8s.Status().Update(context.Background(), a); err != nil {
		t.Fatalf("status update: %v", err)
	}

	var got assaydv1alpha1.Agent
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &got); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got.Status.Eval == nil || got.Status.Eval.Score != "0.94" {
		t.Errorf("status.eval.score is what the Eval column reads; it did not persist: %+v", got.Status.Eval)
	}
	if got.Status.Budget == nil || got.Status.Budget.USDSpentToday == nil || *got.Status.Budget.USDSpentToday != spent {
		t.Errorf("status.budget.usdSpentToday backs the Cost/Day column; it did not persist")
	}
}
