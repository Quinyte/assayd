package revision

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
)

// The revision hash decides when an agent pays for a full eval-and-canary
// cycle. Design 02 §3.3 (A12) splits the spec into a behaviour surface, where a
// change must pass a gate, and a policy surface, applied in place. These tests
// are that table, executable.

// withGraph gives a spec one knowledge binding, so a knowledge subfield test can
// vary that subfield alone.
func withGraph(s *assaydv1alpha1.AgentSpec) {
	s.Knowledge = []assaydv1alpha1.KnowledgeBinding{{Name: "policies", Version: "v12"}}
}

func baseSpec() assaydv1alpha1.AgentSpec {
	return assaydv1alpha1.AgentSpec{
		Runtime: &assaydv1alpha1.AgentRuntime{
			Image:    "ghcr.io/acme/agent@sha256:1100000000000000000000000000000000000000000000000000000000000000",
			Replicas: 1,
			Port:     8080,
		},
		Card: assaydv1alpha1.CardSpec{Path: "/.well-known/agent-card.json"},
	}
}

func TestHashIsDeterministic(t *testing.T) {
	a, b := baseSpec(), baseSpec()
	if HashWithFixed(a) != HashWithFixed(b) {
		t.Fatal("equal specs must hash equally, or every reconcile mints a revision")
	}
	// Stable across calls: a hash that varies per invocation would orphan the
	// workload it names — the class of defect 02-review caught as a blocker.
	first := HashWithFixed(a)
	for i := 0; i < 100; i++ {
		if HashWithFixed(a) != first {
			t.Fatal("hash is not stable across invocations")
		}
	}
}

func TestHashShape(t *testing.T) {
	h := HashWithFixed(baseSpec())
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
		base   func(*assaydv1alpha1.AgentSpec)
		mutate func(*assaydv1alpha1.AgentSpec)
		why    string
	}{
		{field: "runtime.image", mutate: func(s *assaydv1alpha1.AgentSpec) {
			s.Runtime.Image = "ghcr.io/acme/agent@sha256:2200000000000000000000000000000000000000000000000000000000000000"
		}, why: "different code"},

		// A30. Both sides carry the tool so tools[].name is constant and only
		// requiresApproval varies — otherwise this would pass on the tool
		// appearing and prove nothing about the field.
		{field: "tools.requiresApproval",
			base: func(s *assaydv1alpha1.AgentSpec) {
				s.Tools = []assaydv1alpha1.ToolBinding{{Name: "claims-db"}}
			},
			mutate: func(s *assaydv1alpha1.AgentSpec) {
				s.Tools = []assaydv1alpha1.ToolBinding{{Name: "claims-db", RequiresApproval: true}}
			},
			why: "turning approval ON had no safe transaction while it was policy-surface: " +
				"an unknown tightening with no revision to mint (design 03 A31)"},

		// The same discipline in the other direction: turning approval OFF is a
		// widening, and a widening that reaches production ungated is the whole
		// failure this surface exists to prevent.
		{field: "tools.requiresApproval (off)",
			base: func(s *assaydv1alpha1.AgentSpec) {
				s.Tools = []assaydv1alpha1.ToolBinding{{Name: "claims-db", RequiresApproval: true}}
			},
			mutate: func(s *assaydv1alpha1.AgentSpec) {
				s.Tools = []assaydv1alpha1.ToolBinding{{Name: "claims-db"}}
			},
			why: "removing an approval requirement is a widening; both directions gate"},

		{field: "budget", mutate: func(s *assaydv1alpha1.AgentSpec) {
			tokens := int64(2000000)
			s.Budget = &assaydv1alpha1.BudgetSpec{TokensPerDay: &tokens}
		}, why: "A25: a budget is what stops a runaway loop"},

		{field: "expose", mutate: func(s *assaydv1alpha1.AgentSpec) {
			s.Expose = &assaydv1alpha1.ExposeSpec{A2A: &assaydv1alpha1.ExposeProtocol{Visibility: "org"}}
		}, why: "A25: who may reach the agent at all"},

		{field: "runtime.env",
			base:   func(s *assaydv1alpha1.AgentSpec) { s.Runtime.Env = []corev1.EnvVar{{Name: "MODE", Value: "lax"}} },
			mutate: func(s *assaydv1alpha1.AgentSpec) { s.Runtime.Env = []corev1.EnvVar{{Name: "MODE", Value: "strict"}} },
			why:    "configuration changes behaviour"},

		{field: "runtime.env.valueFrom",
			base: func(s *assaydv1alpha1.AgentSpec) {
				s.Runtime.Env = []corev1.EnvVar{{Name: "KEY", ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: "creds-a"}, Key: "k"}}}}
			},
			mutate: func(s *assaydv1alpha1.AgentSpec) {
				s.Runtime.Env = []corev1.EnvVar{{Name: "KEY", ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: "creds-b"}, Key: "k"}}}}
			},
			why: "repointing at a different Secret is a behaviour change"},

		{field: "runtime.envFrom",
			base: func(s *assaydv1alpha1.AgentSpec) {
				s.Runtime.EnvFrom = []corev1.EnvFromSource{{ConfigMapRef: &corev1.ConfigMapEnvSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: "cfg-a"}}}}
			},
			mutate: func(s *assaydv1alpha1.AgentSpec) {
				s.Runtime.EnvFrom = []corev1.EnvFromSource{{ConfigMapRef: &corev1.ConfigMapEnvSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: "cfg-b"}}}}
			},
			why: "a different ConfigMap is different configuration"},

		{field: "runtime.sandbox.profile",
			base:   func(s *assaydv1alpha1.AgentSpec) { s.Runtime.Sandbox = &assaydv1alpha1.SandboxSpec{Profile: "kata"} },
			mutate: func(s *assaydv1alpha1.AgentSpec) { s.Runtime.Sandbox = &assaydv1alpha1.SandboxSpec{Profile: "gvisor"} },
			why:    "isolation boundary"},

		{field: "knowledge.name", base: withGraph,
			mutate: func(s *assaydv1alpha1.AgentSpec) { s.Knowledge[0].Name = "other-graph" },
			why:    "different domain"},

		{field: "knowledge.version", base: withGraph,
			mutate: func(s *assaydv1alpha1.AgentSpec) { s.Knowledge[0].Version = "v13" },
			why:    "THE critical one: an ungated graph roll would hollow out ADR-0005"},

		{field: "knowledge.scope", base: withGraph,
			mutate: func(s *assaydv1alpha1.AgentSpec) {
				s.Knowledge[0].Scope = &assaydv1alpha1.KGScope{EntityTypes: []string{"Policy"}}
			},
			why: "narrowing scope changes what the agent can see"},

		{field: "tools.name",
			base:   func(s *assaydv1alpha1.AgentSpec) { s.Tools = []assaydv1alpha1.ToolBinding{{Name: "claims-db"}} },
			mutate: func(s *assaydv1alpha1.AgentSpec) { s.Tools = []assaydv1alpha1.ToolBinding{{Name: "payments-db"}} },
			why:    "a capability grant"},

		{field: "llm.providers",
			base: func(s *assaydv1alpha1.AgentSpec) {
				s.LLM = &assaydv1alpha1.LLMSpec{Providers: []assaydv1alpha1.LLMEndpoint{{Arm: assaydv1alpha1.ArmOpenAI, Model: "small"}}}
			},
			mutate: func(s *assaydv1alpha1.AgentSpec) {
				s.LLM = &assaydv1alpha1.LLMSpec{Providers: []assaydv1alpha1.LLMEndpoint{{Arm: assaydv1alpha1.ArmOpenAI, Model: "gpt-x"}}}
			},
			why: "THE critical one: an ungated model swap would hollow out ADR-0006"},

		{field: "llm.fallback",
			base: func(s *assaydv1alpha1.AgentSpec) {
				s.LLM = &assaydv1alpha1.LLMSpec{
					Providers: []assaydv1alpha1.LLMEndpoint{{Arm: assaydv1alpha1.ArmOpenAI, Model: "gpt-x"}},
					Fallback:  &assaydv1alpha1.LLMEndpoint{Arm: assaydv1alpha1.ArmAnthropic, Model: "small"},
				}
			},
			mutate: func(s *assaydv1alpha1.AgentSpec) {
				s.LLM.Fallback = &assaydv1alpha1.LLMEndpoint{Arm: assaydv1alpha1.ArmAnthropic, Model: "tiny"}
			},
			why: "the fallback is what serves traffic when the primary drifts (design 20)"},

		{field: "external.endpoint",
			base: func(s *assaydv1alpha1.AgentSpec) {
				s.Runtime, s.External = nil, &assaydv1alpha1.ExternalAgent{Endpoint: "https://a.example.com"}
			},
			mutate: func(s *assaydv1alpha1.AgentSpec) {
				s.Runtime, s.External = nil, &assaydv1alpha1.ExternalAgent{Endpoint: "https://b.example.com"}
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
			if HashWithFixed(before) == HashWithFixed(after) {
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
	for _, tc := range []struct {
		field  string
		mutate func(*assaydv1alpha1.AgentSpec)
		why    string
	}{
		{"runtime.replicas", func(s *assaydv1alpha1.AgentSpec) {
			s.Runtime.Replicas = 3
		}, "scaling is not a behaviour change"},

		{"runtime.resources", func(s *assaydv1alpha1.AgentSpec) {
			s.Runtime.Resources = corev1.ResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("500m")},
			}
		}, "capacity regressions surface through design 20's behavioural drift path, and gating them would block incident response"},

		{"gates", func(s *assaydv1alpha1.AgentSpec) {
			s.Gates = []assaydv1alpha1.GateRef{{EvalSuiteRef: "pa-regression"}}
		}, "a gate that re-gated itself on edit could not converge"},

		{"card.path", func(s *assaydv1alpha1.AgentSpec) {
			s.Card.Path = "/custom-card.json"
		}, "a path change is re-registration, exactly as card drift is (A12/A4)"},
	} {
		t.Run(tc.field, func(t *testing.T) {
			before, after := baseSpec(), baseSpec()
			tc.mutate(&after)
			if HashWithFixed(before) != HashWithFixed(after) {
				t.Errorf("changing %s minted a revision, so a routine operation now pays for "+
					"an eval and canary cycle — %s", tc.field, tc.why)
			}
		})
	}
}
