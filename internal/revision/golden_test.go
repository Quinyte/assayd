package revision

import (
	"reflect"
	"time"

	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	plumev1alpha1 "github.com/Quinyte/plume/api/v1alpha1"
)

// goldenSpec exercises every projected field and every policy field at once —
// a claim TestTheGoldenSpecPopulatesEveryLeaf now checks, because it was false:
// usdPerDay, taskTimeout, maxHops, expose.a2a.auth and egressAllowlist were all
// absent while the comment said otherwise (Codex r7 MAJOR 3).
func goldenSpec() plumev1alpha1.AgentSpec {
	tokens, usd, hops, yes := int64(2000000), "12.50", int32(4), true
	return plumev1alpha1.AgentSpec{
		Runtime: &plumev1alpha1.AgentRuntime{
			Image: "ghcr.io/acme/pa-agent:1.4.2", Replicas: 2, Port: 8080,
			Sandbox: &plumev1alpha1.SandboxSpec{Profile: "gvisor"},
			// One entry per union ARM: the arms are mutually exclusive, so a single
			// EnvVar cannot populate them all and a fixture with one entry silently
			// covers a quarter of the surface.
			Env: []corev1.EnvVar{
				{Name: "MODE", Value: "strict"},
				{Name: "KEY", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: "creds"}, Key: "k", Optional: &yes}}},
				{Name: "CFG", ValueFrom: &corev1.EnvVarSource{ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: "cfg"}, Key: "c", Optional: &yes}}},
				{Name: "POD", ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{
					APIVersion: "v1", FieldPath: "metadata.name"}}},
				{Name: "CPU", ValueFrom: &corev1.EnvVarSource{ResourceFieldRef: &corev1.ResourceFieldSelector{
					ContainerName: "agent", Resource: "limits.cpu", Divisor: resource.MustParse("1")}}},
				{Name: "FIL", ValueFrom: &corev1.EnvVarSource{FileKeyRef: &corev1.FileKeySelector{
					VolumeName: "vol", Path: "p.env", Key: "K", Optional: &yes}}},
			},
			EnvFrom: []corev1.EnvFromSource{
				{Prefix: "P_", ConfigMapRef: &corev1.ConfigMapEnvSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: "cfg"}, Optional: &yes}},
				{Prefix: "S_", SecretRef: &corev1.SecretEnvSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: "sec"}, Optional: &yes}},
			},
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("500m")},
				Limits:   corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("1Gi")},
				Claims:   []corev1.ResourceClaim{{Name: "gpu", Request: "one"}},
			},
		},
		Card: plumev1alpha1.CardSpec{Path: "/.well-known/agent-card.json"},
		Knowledge: []plumev1alpha1.KnowledgeBinding{{Name: "payer-policies", Version: "v12",
			Scope: &plumev1alpha1.KGScope{EntityTypes: []string{"Procedure", "Policy"}}}},
		Tools: []plumev1alpha1.ToolBinding{{Name: "claims-system", RequiresApproval: true}},
		LLM: &plumev1alpha1.LLMSpec{
			Providers:       []string{"openai/gpt-x", "internal/pa"},
			EgressAllowlist: []string{"openai/*", "internal/*"},
			Fallback:        &plumev1alpha1.ModelRef{Provider: "internal", Model: "pa-classifier"},
		},
		Budget: &plumev1alpha1.BudgetSpec{
			TokensPerDay: &tokens, USDPerDay: &usd,
			TaskTimeout: &metav1.Duration{Duration: 90 * time.Second}, MaxHops: &hops,
		},
		Gates:  []plumev1alpha1.GateRef{{EvalSuiteRef: "pa-regression"}},
		Loop:   &plumev1alpha1.LoopSpec{AllowReentry: true, MaxVisits: 2},
		Expose: &plumev1alpha1.ExposeSpec{A2A: &plumev1alpha1.ExposeProtocol{Visibility: "org", Auth: "oauth"}},
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
// FIXTURE CHANGE (2026-08-31, Codex r7 MAJOR 3) — NOT a migration, and the
// difference matters. The constant moved because goldenSpec gained the leaves
// it always claimed to cover (usdPerDay, taskTimeout, maxHops, expose auth, one
// Env entry per union arm, resource limits and claims). The ENCODING did not
// change, so no existing revision's hash moves and no cluster re-mints. A
// migration is when project() changes; this is when the fixture does. Reading
// them as the same thing is how a genuine encoding change gets waved through as
// "just the fixture".
//
// MIGRATION 2 (2026-08-31, design 02 A37). The constant moved again because
// the projection changed three ways, each closing a reproduced bypass:
// llm.egressAllowlist entered it at all, the env selectors are now canonically
// marshalled in full rather than having four arms named and their leaves
// dropped, and the golden spec gained an allowlist so it exercises what it
// claims to. Same reasoning as MIGRATION 1 and the same reason it is free: P1
// is unshipped, so the set of affected revisions is empty.
//
// MIGRATION 1 (2026-08-29, design 02 A25 + A30). The constant moved from
// "097ef5eedf" to the value below because three fields entered the projection:
// tools[].requiresApproval, budget and expose. This is the rollout storm the
// comment above describes — every existing Agent's hash changes, so every Agent
// mints a candidate and every active workload is orphaned by name.
//
// It is being taken deliberately and now because plume has no production
// clusters: P1 is unshipped, so the set of affected revisions is empty and the
// migration costs nothing today. Taken after the first install it would need a
// carry-forward that maps old hashes to new ones. Design 02 §3.3 records the
// same thing so an implementer does not rediscover it from this file.
const goldenDigest = "77df3fe02d"

// goldenExternalDigest pins the external-agent shape, under the same rule.
const goldenExternalDigest = "d998beaf43"

func TestGoldenDigest(t *testing.T) {
	if got := Hash(goldenSpec()); got != goldenDigest {
		t.Errorf("behaviour projection encoding changed: got %q, pinned %q.\n"+
			"This is a migration, not a test edit — see the comment on goldenDigest.", got, goldenDigest)
	}
}

// The golden fixture's comment claimed it exercised every projected field. It
// did not, and nothing checked — five leaves were missing, four of them fields
// whose projection had no other test either. A fixture that silently covers
// less than it says is worse than a smaller honest one, because the golden is
// what a reviewer trusts when reading a diff to the encoding.
func TestTheGoldenSpecPopulatesEveryLeaf(t *testing.T) {
	// The UNION of the fixtures, because runtime and external are mutually
	// exclusive at admission — one spec cannot legally carry both, so demanding
	// one fixture cover every leaf would be unsatisfiable and the check would end
	// up deleted instead of believed.
	fixtures := []plumev1alpha1.AgentSpec{goldenSpec(), goldenExternalSpec()}
	for _, l := range leaves(reflect.TypeOf(plumev1alpha1.AgentSpec{}), "spec", nil) {
		covered := false
		for _, spec := range fixtures {
			if v, ok := readLeaf(reflect.ValueOf(spec), l); ok && !v.IsZero() {
				covered = true
				break
			}
		}
		if !covered {
			t.Errorf("no golden fixture sets %s, so a change to how that leaf is encoded "+
				"does not move a golden digest and would land unreviewed", l.path)
		}
	}
}

// goldenExternalSpec is the other legal Agent shape: an external agent, which
// admission requires to carry no runtime at all. It exists so External's three
// leaves are pinned by a digest like every other leaf.
func goldenExternalSpec() plumev1alpha1.AgentSpec {
	return plumev1alpha1.AgentSpec{
		External: &plumev1alpha1.ExternalAgent{
			Endpoint:       "https://partner.example.com/a2a",
			OAuthClientRef: "partner-client",
			InlineCard:     `{"name":"partner"}`,
		},
		Card: plumev1alpha1.CardSpec{Path: "/.well-known/agent-card.json"},
	}
}

func TestGoldenExternalDigest(t *testing.T) {
	if got := Hash(goldenExternalSpec()); got != goldenExternalDigest {
		t.Errorf("the external-agent projection encoding changed: got %q, pinned %q.\n"+
			"Same rule as goldenDigest: this is a migration, not a test edit.", got, goldenExternalDigest)
	}
}

// readLeaf follows a leaf's chain without allocating: a nil pointer or empty
// slice on the way means the leaf is genuinely unset.
// readLeaf follows a leaf's chain without allocating and reports whether ANY
// slice element sets it. Scanning only element [0] would demand that one
// EnvVar populate every mutually-exclusive union arm, which is unsatisfiable —
// so the fixture would have been declared incomplete forever and the check
// would have been deleted rather than believed.
func readLeaf(v reflect.Value, l leaf) (reflect.Value, bool) {
	for v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return reflect.Value{}, false
		}
		v = v.Elem()
	}
	if len(l.chain) == 0 {
		return v, !v.IsZero()
	}
	if v.Kind() == reflect.Slice {
		for i := 0; i < v.Len(); i++ {
			if got, ok := readLeaf(v.Index(i), l); ok && !got.IsZero() {
				return got, true
			}
		}
		return reflect.Value{}, false
	}
	f := v.Field(l.chain[0])
	rest := leaf{path: l.path, chain: l.chain[1:], typ: l.typ}
	if len(rest.chain) == 0 {
		for f.Kind() == reflect.Ptr {
			if f.IsNil() {
				return reflect.Value{}, false
			}
			f = f.Elem()
		}
		return f, true
	}
	return readLeaf(f, rest)
}
