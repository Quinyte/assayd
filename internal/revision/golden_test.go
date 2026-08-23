package revision

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	plumev1alpha1 "github.com/Quinyte/plume/api/v1alpha1"
)

// goldenSpec exercises every projected field and every policy field at once.
func goldenSpec() plumev1alpha1.AgentSpec {
	tokens := int64(2000000)
	return plumev1alpha1.AgentSpec{
		Runtime: &plumev1alpha1.AgentRuntime{
			Image: "ghcr.io/acme/pa-agent:1.4.2", Replicas: 2, Port: 8080,
			Sandbox: &plumev1alpha1.SandboxSpec{Profile: "gvisor"},
			Env: []corev1.EnvVar{
				{Name: "MODE", Value: "strict"},
				{Name: "KEY", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: "creds"}, Key: "k"}}},
			},
			EnvFrom: []corev1.EnvFromSource{{Prefix: "P_", ConfigMapRef: &corev1.ConfigMapEnvSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: "cfg"}}}},
			Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("500m")}},
		},
		Card: plumev1alpha1.CardSpec{Path: "/.well-known/agent-card.json"},
		Knowledge: []plumev1alpha1.KnowledgeBinding{{Name: "payer-policies", Version: "v12",
			Scope: &plumev1alpha1.KGScope{EntityTypes: []string{"Procedure", "Policy"}}}},
		Tools: []plumev1alpha1.ToolBinding{{Name: "claims-system", RequiresApproval: true}},
		LLM: &plumev1alpha1.LLMSpec{
			Providers: []string{"openai/gpt-x", "internal/pa"},
			Fallback:  &plumev1alpha1.ModelRef{Provider: "internal", Model: "pa-classifier"},
		},
		Budget: &plumev1alpha1.BudgetSpec{TokensPerDay: &tokens},
		Gates:  []plumev1alpha1.GateRef{{EvalSuiteRef: "pa-regression"}},
		Loop:   &plumev1alpha1.LoopSpec{AllowReentry: true, MaxVisits: 2},
		Expose: &plumev1alpha1.ExposeSpec{A2A: &plumev1alpha1.ExposeProtocol{Visibility: "org"}},
	}
}

// goldenDigest pins the wire encoding of the behaviour projection.
//
// READ THIS BEFORE CHANGING THE VALUE. encoding/json emits struct fields in
// declaration order, so the field order, JSON tags and omitempty in behaviour
// are all part of the revision contract. Reordering them — a pure refactor with
// a green suite — changes every agent's hash, which on the next reconcile mints
// a candidate for every Agent in every cluster, orphans every active workload,
// and demands a platform-wide eval cycle.
//
// If this test fails, the encoding changed. That is a MIGRATION, not a test
// edit: either revert the change, or ship a migration that carries existing
// revisions forward. Updating the constant to make the test pass is how a
// cluster-wide rollout storm gets released.
const goldenDigest = "097ef5eedf"

func TestGoldenDigest(t *testing.T) {
	if got := Hash(goldenSpec()); got != goldenDigest {
		t.Errorf("behaviour projection encoding changed: got %q, pinned %q.\n"+
			"This is a migration, not a test edit — see the comment on goldenDigest.", got, goldenDigest)
	}
}
