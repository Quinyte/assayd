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
			Image: "ghcr.io/acme/pa-agent@sha256:1400000000000000000000000000000000000000000000000000000000000000", Replicas: 2, Port: 8080,
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
		// One entry per ARM, for the same reason the Env list carries one per union
		// arm: the arms are mutually exclusive within an entry, so a fixture with
		// two entries silently leaves four arms' instance leaves unpinned.
		LLM: &plumev1alpha1.LLMSpec{
			Providers: []plumev1alpha1.LLMEndpoint{
				{Arm: plumev1alpha1.ArmOpenAI, Model: "gpt-x"},
				{Arm: plumev1alpha1.ArmAnthropic, Model: "claude"},
				{Arm: plumev1alpha1.ArmAzureOpenAI, AzureOpenAI: &plumev1alpha1.AzureOpenAIInstance{
					Endpoint: "acme.openai.azure.com", DeploymentName: "gpt4o-prod", APIVersion: "2024-10-21"}},
				{Arm: plumev1alpha1.ArmVertexAI, Model: "gemini-pro", VertexAI: &plumev1alpha1.VertexAIInstance{
					ProjectID: "acme-prod", Region: "us-central1"}},
				{Arm: plumev1alpha1.ArmBedrock, Model: "claude-3", Bedrock: &plumev1alpha1.BedrockInstance{
					Region: "us-east-1", Guardrail: "gr-1"}},
				{Arm: plumev1alpha1.ArmCustom, Model: "pa", Custom: &plumev1alpha1.CustomInstance{
					Host: "llm.internal.example.com", Port: 8443, PathPrefix: "/v1"}},
			},
			EgressAllowlist: []plumev1alpha1.LLMAllowEntry{
				{Arm: plumev1alpha1.ArmOpenAI, Models: []string{"gpt-4o"}},
				{Arm: plumev1alpha1.ArmAnthropic},
				{Arm: plumev1alpha1.ArmAzureOpenAI, AzureOpenAI: &plumev1alpha1.AzureOpenAIInstance{
					Endpoint: "acme.openai.azure.com", DeploymentName: "gpt4o-prod", APIVersion: "2024-10-21"}},
				{Arm: plumev1alpha1.ArmVertexAI, VertexAI: &plumev1alpha1.VertexAIInstance{
					ProjectID: "acme-prod", Region: "us-central1"}},
				{Arm: plumev1alpha1.ArmBedrock, Bedrock: &plumev1alpha1.BedrockInstance{
					Region: "us-east-1", Guardrail: "gr-1"}},
				{Arm: plumev1alpha1.ArmCustom, Custom: &plumev1alpha1.CustomInstance{
					Host: "llm.internal.example.com", Port: 8443, PathPrefix: "/v1"}},
			},
			Fallback: &plumev1alpha1.LLMEndpoint{Arm: plumev1alpha1.ArmAnthropic, Model: "pa-classifier"},
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
// MIGRATION 4 (2026-08-31, design 02 A20 implemented) — the projection now
// carries a digest of the RESOLVED CONTENT of every referenced env source, so
// every revision hash moves again. This is the migration that closes the live
// bypass: before it, changing a referenced ConfigMap left the identity
// unchanged and the new content served under the old gate result.
//
// MIGRATION 3 (2026-08-31, design 02 A53 / Codex r8 BLOCKER 6) — a real
// encoding change. llm.providers, llm.egressAllowlist and llm.fallback became
// typed endpoint identities, so every revision hash moves. Free only because P1
// is unshipped; after the first install this needs a carry-forward.
//
// FIXTURE CHANGE (2026-08-31, Codex r8 BLOCKER 1) — again not a migration. The
// constant moved because every image in the fixtures is now digest-pinned, which
// design 02 A21 required and nothing enforced. The projection is unchanged.
//
// FIXTURE CHANGE (2026-08-31, Codex r7 MAJOR 3) — NOT a migration, and the
// difference matters. The constant moved because goldenSpec gained the leaves
// it always claimed to cover (usdPerDay, taskTimeout, maxHops, expose auth, one
// Env entry per union arm, resource limits and claims). The ENCODING did not
// change, so no existing revision's hash moves and no cluster re-mints. A
// migration is when project() changes; this is when the fixture does. Reading
// them as the same thing is how a genuine encoding change gets waved through as
// "just the fixture".
//
// MIGRATION 3 (2026-09-05, ADR-0031). Two fields entered the projection:
// runtime.port and the whole loop block. Both had been classified policy
// surface, and both decide behaviour a gate evaluated — an image may serve the
// evaluated A2A implementation on one port and a different handler on another,
// and design 22 compiles allowReentry/maxVisits into the in-proxy CEL that
// admits or denies each request by its lineage. Editing either in place
// published capability the revision was never evaluated with. Free for the same
// reason as the migrations below: P1 is unshipped, so the affected set is empty.
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
const goldenDigest = "bc8838ba7b"

// goldenExternalDigest pins the external-agent shape, under the same rule.
const goldenExternalDigest = "d998beaf43"

func TestGoldenDigest(t *testing.T) {
	if got := HashWith(goldenSpec(), "fixed"); got != goldenDigest {
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
	fixtures := append([]plumev1alpha1.AgentSpec{goldenSpec(), goldenExternalSpec()}, coverageSpecs()...)
	for _, l := range Leaves(t, reflect.TypeOf(plumev1alpha1.AgentSpec{}), "spec", nil) {
		covered := false
		for _, spec := range fixtures {
			if v, ok := readLeaf(reflect.ValueOf(spec), l); ok && !v.IsZero() {
				covered = true
				break
			}
		}
		if !covered {
			t.Errorf("no golden fixture sets %s, so a change to how that leaf is encoded "+
				"does not move a golden digest and would land unreviewed", l.Path)
		}
	}
}

// coverageSpecs exist for the leaf-coverage check and are deliberately NOT
// digest-pinned. `llm.fallback` is a single pointer, so one spec can carry one
// arm, and pinning four more digests would pin four more constants that move
// every time the encoding does — noise around the one that matters.
//
// What that costs is stated rather than glossed: these leaves are covered for
// CLASSIFICATION and not for encoding. The cost is small because the fallback
// arms go through the same endpointIdentity/instanceIdentity path as
// `llm.providers[]`, whose arms ARE in goldenSpec and therefore digest-pinned —
// so an encoding change reaches goldenDigest through the providers side. If
// fallback ever gets its own encoding, it needs its own pinned fixture.
func coverageSpecs() []plumev1alpha1.AgentSpec {
	fb := func(e plumev1alpha1.LLMEndpoint) plumev1alpha1.AgentSpec {
		return plumev1alpha1.AgentSpec{
			Runtime: &plumev1alpha1.AgentRuntime{Image: goldenSpec().Runtime.Image},
			LLM:     &plumev1alpha1.LLMSpec{Fallback: &e},
		}
	}
	// fileKeyRef reads from a VOLUME, and this API renders none — so the operator
	// cannot hash its content and A20 refuses to mint. It cannot appear in a
	// digest-pinned fixture for that reason, and it stays here so its four leaves
	// keep classification coverage. The refusal is asserted separately by
	// TestAFileKeyRefSpecCannotMintARevision.
	fileKey := plumev1alpha1.AgentSpec{
		Runtime: &plumev1alpha1.AgentRuntime{
			Image: goldenSpec().Runtime.Image,
			Env: []corev1.EnvVar{{Name: "FIL", ValueFrom: &corev1.EnvVarSource{
				FileKeyRef: &corev1.FileKeySelector{
					VolumeName: "vol", Path: "p.env", Key: "K", Optional: boolPtr(true)}}}},
		},
	}
	return []plumev1alpha1.AgentSpec{
		fileKey,
		fb(plumev1alpha1.LLMEndpoint{Arm: plumev1alpha1.ArmAzureOpenAI,
			AzureOpenAI: &plumev1alpha1.AzureOpenAIInstance{
				Endpoint: "acme.openai.azure.com", DeploymentName: "gpt4o-prod", APIVersion: "2024-10-21"}}),
		fb(plumev1alpha1.LLMEndpoint{Arm: plumev1alpha1.ArmVertexAI, Model: "gemini-pro",
			VertexAI: &plumev1alpha1.VertexAIInstance{ProjectID: "acme-prod", Region: "us-central1"}}),
		fb(plumev1alpha1.LLMEndpoint{Arm: plumev1alpha1.ArmBedrock, Model: "claude-3",
			Bedrock: &plumev1alpha1.BedrockInstance{Region: "us-east-1", Guardrail: "gr-1"}}),
		fb(plumev1alpha1.LLMEndpoint{Arm: plumev1alpha1.ArmCustom, Model: "pa",
			Custom: &plumev1alpha1.CustomInstance{Host: "llm.internal.example.com", Port: 8443, PathPrefix: "/v1"}}),
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
	if got := HashWith(goldenExternalSpec(), "fixed"); got != goldenExternalDigest {
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
func readLeaf(v reflect.Value, l Leaf) (reflect.Value, bool) {
	for v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return reflect.Value{}, false
		}
		v = v.Elem()
	}
	if len(l.Chain) == 0 {
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
	f := v.Field(l.Chain[0])
	rest := Leaf{Path: l.Path, Chain: l.Chain[1:], Typ: l.Typ}
	if len(rest.Chain) == 0 {
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
