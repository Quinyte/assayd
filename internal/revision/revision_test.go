package revision

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"

	plumev1alpha1 "github.com/ejs-5/plume/api/v1alpha1"
)

// The revision hash decides when an agent pays for a full eval-and-canary
// cycle. Design 02 §3.3 (A12) splits the spec into a behaviour surface, where a
// change must pass a gate, and a policy surface, applied in place. These tests
// are that table, executable.

// withGraph gives a spec one knowledge binding, so a knowledge subfield test can
// vary that subfield alone.
func withGraph(s *plumev1alpha1.AgentSpec) {
	s.Knowledge = []plumev1alpha1.KnowledgeBinding{{Name: "policies", Version: "v12"}}
}

func baseSpec() plumev1alpha1.AgentSpec {
	return plumev1alpha1.AgentSpec{
		Runtime: &plumev1alpha1.AgentRuntime{
			Image:    "ghcr.io/acme/agent:1.0.0",
			Replicas: 1,
			Port:     8080,
		},
		Card: plumev1alpha1.CardSpec{Path: "/.well-known/agent-card.json"},
	}
}

func TestHashIsDeterministic(t *testing.T) {
	a, b := baseSpec(), baseSpec()
	if Hash(a) != Hash(b) {
		t.Fatal("equal specs must hash equally, or every reconcile mints a revision")
	}
	// Stable across calls: a hash that varies per invocation would orphan the
	// workload it names — the class of defect 02-review caught as a blocker.
	first := Hash(a)
	for i := 0; i < 100; i++ {
		if Hash(a) != first {
			t.Fatal("hash is not stable across invocations")
		}
	}
}

func TestHashShape(t *testing.T) {
	h := Hash(baseSpec())
	if len(h) != 10 {
		t.Errorf("hash is %q (len %d); want 10 chars — it becomes a DNS label suffix", h, len(h))
	}
	for _, r := range h {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
			t.Errorf("hash %q contains %q; must be DNS-1123 safe", h, r)
		}
	}
}

// Design 02 §3.3 A12, behaviour surface: each of these must mint a new revision,
// because each can change what the agent does.
func TestBehaviourSurfaceMintsARevision(t *testing.T) {
	for _, tc := range []struct {
		field string
		// base establishes the surrounding structure so that mutate can change
		// exactly one field. Without this, "add a knowledge binding to a spec that
		// has none" would pass even if the projection dropped .version entirely.
		base   func(*plumev1alpha1.AgentSpec)
		mutate func(*plumev1alpha1.AgentSpec)
		why    string
	}{
		{field: "runtime.image", mutate: func(s *plumev1alpha1.AgentSpec) {
			s.Runtime.Image = "ghcr.io/acme/agent:2.0.0"
		}, why: "different code"},

		{field: "runtime.env",
			base:   func(s *plumev1alpha1.AgentSpec) { s.Runtime.Env = []corev1.EnvVar{{Name: "MODE", Value: "lax"}} },
			mutate: func(s *plumev1alpha1.AgentSpec) { s.Runtime.Env = []corev1.EnvVar{{Name: "MODE", Value: "strict"}} },
			why:    "configuration changes behaviour"},

		{field: "runtime.env.valueFrom",
			base: func(s *plumev1alpha1.AgentSpec) {
				s.Runtime.Env = []corev1.EnvVar{{Name: "KEY", ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: "creds-a"}, Key: "k"}}}}
			},
			mutate: func(s *plumev1alpha1.AgentSpec) {
				s.Runtime.Env = []corev1.EnvVar{{Name: "KEY", ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: "creds-b"}, Key: "k"}}}}
			},
			why: "repointing at a different Secret is a behaviour change"},

		{field: "runtime.envFrom",
			base: func(s *plumev1alpha1.AgentSpec) {
				s.Runtime.EnvFrom = []corev1.EnvFromSource{{ConfigMapRef: &corev1.ConfigMapEnvSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: "cfg-a"}}}}
			},
			mutate: func(s *plumev1alpha1.AgentSpec) {
				s.Runtime.EnvFrom = []corev1.EnvFromSource{{ConfigMapRef: &corev1.ConfigMapEnvSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: "cfg-b"}}}}
			},
			why: "a different ConfigMap is different configuration"},

		{field: "runtime.sandbox.profile",
			base:   func(s *plumev1alpha1.AgentSpec) { s.Runtime.Sandbox = &plumev1alpha1.SandboxSpec{Profile: "kata"} },
			mutate: func(s *plumev1alpha1.AgentSpec) { s.Runtime.Sandbox = &plumev1alpha1.SandboxSpec{Profile: "gvisor"} },
			why:    "isolation boundary"},

		{field: "card.path", mutate: func(s *plumev1alpha1.AgentSpec) {
			s.Card.Path = "/custom-card.json"
		}, why: "the contract surface is fetched from here"},

		{field: "knowledge.name", base: withGraph,
			mutate: func(s *plumev1alpha1.AgentSpec) { s.Knowledge[0].Name = "other-graph" },
			why:    "different domain"},

		{field: "knowledge.version", base: withGraph,
			mutate: func(s *plumev1alpha1.AgentSpec) { s.Knowledge[0].Version = "v13" },
			why:    "THE critical one: an ungated graph roll would hollow out ADR-0005"},

		{field: "knowledge.scope", base: withGraph,
			mutate: func(s *plumev1alpha1.AgentSpec) {
				s.Knowledge[0].Scope = &plumev1alpha1.KGScope{EntityTypes: []string{"Policy"}}
			},
			why: "narrowing scope changes what the agent can see"},

		{field: "tools.name",
			base:   func(s *plumev1alpha1.AgentSpec) { s.Tools = []plumev1alpha1.ToolBinding{{Name: "claims-db"}} },
			mutate: func(s *plumev1alpha1.AgentSpec) { s.Tools = []plumev1alpha1.ToolBinding{{Name: "payments-db"}} },
			why:    "a capability grant"},

		{field: "llm.providers",
			base: func(s *plumev1alpha1.AgentSpec) {
				s.LLM = &plumev1alpha1.LLMSpec{Providers: []string{"internal/small"}}
			},
			mutate: func(s *plumev1alpha1.AgentSpec) { s.LLM = &plumev1alpha1.LLMSpec{Providers: []string{"openai/gpt-x"}} },
			why:    "THE critical one: an ungated model swap would hollow out ADR-0006"},

		{field: "llm.fallback",
			base: func(s *plumev1alpha1.AgentSpec) {
				s.LLM = &plumev1alpha1.LLMSpec{
					Providers: []string{"openai/gpt-x"},
					Fallback:  &plumev1alpha1.ModelRef{Provider: "internal", Model: "small"},
				}
			},
			mutate: func(s *plumev1alpha1.AgentSpec) {
				s.LLM.Fallback = &plumev1alpha1.ModelRef{Provider: "internal", Model: "tiny"}
			},
			why: "the fallback is what serves traffic when the primary drifts (design 20)"},

		{field: "external.endpoint",
			base: func(s *plumev1alpha1.AgentSpec) {
				s.Runtime, s.External = nil, &plumev1alpha1.ExternalAgent{Endpoint: "https://a.example.com"}
			},
			mutate: func(s *plumev1alpha1.AgentSpec) {
				s.Runtime, s.External = nil, &plumev1alpha1.ExternalAgent{Endpoint: "https://b.example.com"}
			},
			why: "a different agent entirely"},
	} {
		t.Run(tc.field, func(t *testing.T) {
			before, after := baseSpec(), baseSpec()
			if tc.base != nil {
				tc.base(&before)
				tc.base(&after)
			}
			tc.mutate(&after)
			if Hash(before) == Hash(after) {
				t.Errorf("changing %s did not mint a revision, so it would reach production "+
					"through no gate at all — %s", tc.field, tc.why)
			}
		})
	}
}

// Design 02 §3.3 A12, policy surface: none of these may mint a revision, because
// each is compiled to gateway config (§3.6) and applied in place. Minting one
// would make routine operations pay for an eval-and-canary cycle.
func TestPolicySurfaceDoesNotMintARevision(t *testing.T) {
	tokens := int64(2000000)
	for _, tc := range []struct {
		field  string
		mutate func(*plumev1alpha1.AgentSpec)
		why    string
	}{
		{"runtime.replicas", func(s *plumev1alpha1.AgentSpec) {
			s.Runtime.Replicas = 3
		}, "scaling is not a behaviour change"},

		{"runtime.resources", func(s *plumev1alpha1.AgentSpec) {
			s.Runtime.Resources = corev1.ResourceRequirements{}
		}, "capacity is not behaviour"},

		{"budget", func(s *plumev1alpha1.AgentSpec) {
			s.Budget = &plumev1alpha1.BudgetSpec{TokensPerDay: &tokens}
		}, "a policy knob, enforced live at the gateway"},

		{"gates", func(s *plumev1alpha1.AgentSpec) {
			s.Gates = []plumev1alpha1.GateRef{{EvalSuiteRef: "pa-regression"}}
		}, "a gate that re-gated itself on edit could not converge"},

		{"expose", func(s *plumev1alpha1.AgentSpec) {
			s.Expose = &plumev1alpha1.ExposeSpec{A2A: &plumev1alpha1.ExposeProtocol{Visibility: "org"}}
		}, "gateway visibility"},

		{"tools.requiresApproval", func(s *plumev1alpha1.AgentSpec) {
			s.Tools = []plumev1alpha1.ToolBinding{{Name: "claims-db", RequiresApproval: true}}
		}, "approval policy, not a capability change"},

		{"loop", func(s *plumev1alpha1.AgentSpec) {
			s.Loop = &plumev1alpha1.LoopSpec{AllowReentry: true, MaxVisits: 2}
		}, "lineage governance, enforced at the gateway"},
	} {
		t.Run(tc.field, func(t *testing.T) {
			// Both sides carry the tool so tools[].name is constant and only
			// requiresApproval varies — otherwise this would test the wrong field.
			before := baseSpec()
			if tc.field == "tools.requiresApproval" {
				before.Tools = []plumev1alpha1.ToolBinding{{Name: "claims-db"}}
			}
			after := baseSpec()
			if tc.field == "tools.requiresApproval" {
				after.Tools = []plumev1alpha1.ToolBinding{{Name: "claims-db"}}
			}
			tc.mutate(&after)
			if Hash(before) != Hash(after) {
				t.Errorf("changing %s minted a revision, so a routine operation now pays for "+
					"an eval and canary cycle — %s", tc.field, tc.why)
			}
		})
	}
}

// A12's allowlist rule: a field added to the CRD later is policy-surface until
// the table says otherwise. The safe failure is a missing gate on a policy knob,
// never a missing gate on behaviour — so the projection must name what it
// includes rather than what it excludes.
func TestProjectionIsAllowlistShaped(t *testing.T) {
	src, err := projectionSource()
	if err != nil {
		t.Fatalf("read projection source: %v", err)
	}
	for _, forbidden := range []string{"reflect.", "json.Marshal(spec)", "DeepCopy()"} {
		if strings.Contains(src, forbidden) {
			t.Errorf("projection uses %q, which would sweep in future fields automatically; "+
				"A12 requires an explicit allowlist", forbidden)
		}
	}
}
