// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// AgentSpec is the platform's front door: it turns a container that speaks A2A
// into a governed, discoverable, eval-gated workload.
//
// Design 02 §3.1 (r3, integrated). Exactly one of Runtime or External is set.
//
// +kubebuilder:validation:XValidation:rule="has(self.runtime) != has(self.external)",message="set exactly one of spec.runtime (an agent this cluster runs) or spec.external (an agent running elsewhere)"
// +kubebuilder:validation:XValidation:rule="!has(self.budget)",message="spec.budget is not enforced yet: no gateway rate limit and no spend backstop exist (ADR-0030 step 1). Remove spec.budget; it is accepted again when design 03's -ratelimit and design 04's spend aggregation ship."
type AgentSpec struct {
	// Runtime describes an in-cluster agent workload.
	// +optional
	Runtime *AgentRuntime `json:"runtime,omitempty"`

	// External registers an agent that runs outside this cluster. It receives no
	// SVID; it authenticates to the gateway with OAuth client credentials.
	// +optional
	External *ExternalAgent `json:"external,omitempty"`

	// Release selects which revision serves, when that is not simply the one
	// this spec computes to (ADR-0031 decision 2).
	// +optional
	Release *ReleaseSpec `json:"release,omitempty"`

	// Card locates the A2A Agent Card. The container is the source of truth
	// (ADR-0019): the operator fetches, validates and digests it per revision.
	// +optional
	Card CardSpec `json:"card,omitempty"`

	// Knowledge binds pinned KnowledgeGraph versions. Scope is injected by the
	// gateway and enforced by the provider (design 01 A1) — the gateway never
	// parses MCP bodies.
	// +optional
	Knowledge []KnowledgeBinding `json:"knowledge,omitempty"`

	// Tools grants MCP tool servers. This is the only agent-facing connector
	// surface (ADR-0009).
	// +optional
	Tools []ToolBinding `json:"tools,omitempty"`

	// LLM declares model access. Designs 03, 20 and 25 compile against this.
	// +optional
	LLM *LLMSpec `json:"llm,omitempty"`

	// Budget is REFUSED at admission (design 03 §3.1, ADR-0034 B2). Nothing
	// enforces a budget yet: no gateway rate limit is compiled and design 04's
	// spend aggregation does not exist, so an admitted budget would be a limit
	// the developer believes in and nothing applies. The type stays because an
	// Agent stored before the refusal may carry one, and the revision projection
	// still reads it. The refusal is removed in the change that ships
	// enforcement. An upgraded install refuses only once this CRD is applied:
	// the chart ships the CRD under crds/, which helm upgrade never updates.
	//
	// An Agent stored with a budget before the refusal accepts every write that
	// leaves its spec unchanged, and refuses any spec edit until the budget is
	// removed. A typed Update is a spec edit, because the round-trip adds empty
	// blocks, so the operator writes its finalizer with a metadata patch.
	// +optional
	Budget *BudgetSpec `json:"budget,omitempty"`

	// Loop bounds re-entry for this agent. Enforcement is stateless in-proxy CEL
	// over a gateway-owned lineage header (design 22).
	// +optional
	Loop *LoopSpec `json:"loop,omitempty"`

	// Gates must pass before a candidate revision receives traffic (ADR-0006).
	// At least one is required in prod, iff the EvalSuite CRD is installed;
	// otherwise rollouts proceed with a loud GatesSkipped condition.
	// +optional
	Gates []GateRef `json:"gates,omitempty"`

	// Expose publishes this agent beyond the cluster over open standards.
	// +optional
	Expose *ExposeSpec `json:"expose,omitempty"`
}

// AgentRuntime describes the in-cluster workload. Default materialization is a
// Deployment; setting Sandbox switches to an agent-sandbox Sandbox singleton.
//
// +kubebuilder:validation:XValidation:rule="!(has(self.sandbox) && self.replicas > 1)",message="spec.runtime.sandbox is a stateful singleton: set replicas to 1, or drop sandbox to scale out"
// +kubebuilder:validation:XValidation:rule="!has(self.env) || self.env.all(e, !has(e.valueFrom) || !has(e.valueFrom.resourceFieldRef))",message="spec.runtime.env[].valueFrom.resourceFieldRef is not supported. It reads a resource limit or request into the container, and spec.runtime.resources is editable in place without minting a revision — so a CPU or memory edit could change what the program reads while the revision digest, and the evaluation that gated it, stayed the same. The selector is hashed; the value it resolves to is not. Pass the value literally, or set spec.runtime.resources and read it from the downward API in a way that does not affect behaviour. See ADR-0031."
// +kubebuilder:validation:XValidation:rule="!has(self.env) || self.env.all(e, !e.name.startsWith('ASSAYD_'))",message="spec.runtime.env may not set a name beginning with ASSAYD_. That prefix is the operator's injected contract (design 02 §11) — ASSAYD_GATEWAY_URL and its siblings — and a container keeps the LAST duplicate, so setting one here would redirect the agent away from the gateway every budget, tool grant and egress rule is enforced at. Choose another name. See ADR-0031 and design 02 A65."
// +kubebuilder:validation:XValidation:rule="!has(self.envFrom) || self.envFrom.all(f, !has(f.prefix) || !f.prefix.startsWith('ASSAYD_'))",message="spec.runtime.envFrom may not use a prefix beginning with ASSAYD_: it is the operator's injected contract, and a ConfigMap or Secret mapped under it could shadow ASSAYD_GATEWAY_URL. See design 02 A65."
type AgentRuntime struct {
	// Image is digest-pinned: exactly one "@sha256:" followed by 64 lowercase hex
	// characters (A21). A tag can be repointed at other bytes after a revision is
	// gated — retag while `agent:prod` is serving, let a node drain, and
	// Kubernetes pulls the new image while the spec, the revision digest and the
	// gate result all still name the old one. No Agent write is involved, so
	// nothing in the revision machinery can see it.
	//
	// The rule this comment used to make — "must be cosign-signed; admission
	// rejects unsigned images" — was FALSE and shipped verbatim in the generated
	// CRD. Nothing verifies a signature: CEL cannot, and the chart ships no
	// admission policy. That arrives with the Sigstore policy-controller binding
	// design 07 A2 chose. Digest-pinning is what will make it meaningful, because
	// a signature is verified against a digest.
	//
	// Unconditional, with no local-profile relaxation: one schema applies
	// cluster-wide and CEL has no profile in its evaluation context, so an
	// alternative permitting `:dev` would permit a mutable tag in production.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:XValidation:rule="self.matches('^[a-z0-9]+([._-][a-z0-9]+)*(:[0-9]+)?(/[a-z0-9]+([._-][a-z0-9]+)*)+(:[a-zA-Z0-9._-]+)?@sha256:[0-9a-f]{64}$')",message="spec.runtime.image must be a lowercase OCI reference pinned by a sha256 digest: <registry>[:port]/<repo>[:tag]@sha256:<64 lowercase hex>. A tag can be repointed after the revision is gated, so the running code would no longer be the code that passed. Resolve the tag to a digest (docker buildx imagetools inspect, or the digest your CI already publishes)."
	Image string `json:"image"`

	// Replicas >1 requires the card to assert shared task state, else the
	// operator raises TaskStateUnverified (design 02 §3.2). Mutually exclusive
	// with Sandbox.
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=1
	// +optional
	Replicas int32 `json:"replicas,omitempty"`

	// Port is the A2A server port.
	// +kubebuilder:default=8080
	// +optional
	Port int32 `json:"port,omitempty"`

	// Sandbox opts into an isolated, stateful singleton with a persistent
	// scratchpad. Where the runtime class is absent the operator falls back to a
	// hardened Deployment and sets SandboxDowngraded — never silently.
	// +optional
	Sandbox *SandboxSpec `json:"sandbox,omitempty"`

	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`
	// Env carries non-secret values only; secrets arrive via EnvFrom.
	// +optional
	Env []corev1.EnvVar `json:"env,omitempty"`
	// +optional
	EnvFrom []corev1.EnvFromSource `json:"envFrom,omitempty"`
}

type SandboxSpec struct {
	// +kubebuilder:validation:Enum=gvisor;kata
	Profile string `json:"profile"`
}

type ExternalAgent struct {
	// +kubebuilder:validation:Pattern=`^https://`
	Endpoint string `json:"endpoint"`
	// +optional
	OAuthClientRef string `json:"oauthClientRef,omitempty"`
	// Card may be inlined only where the external endpoint cannot serve one.
	// +optional
	InlineCard string `json:"inlineCard,omitempty"`
}

type CardSpec struct {
	// +kubebuilder:default=/.well-known/agent-card.json
	// +optional
	Path string `json:"path,omitempty"`
}

// KnowledgeBinding pins one graph version. Binds are refused to versions that
// are not active or superseded (design 01 A5.2).
//
// The graph reference is inline rather than wrapped in a graphRef object: the
// wrapper named a protocol, not a choice, so it nested without discriminating.
// Compare ExposeSpec, whose arm does select between protocols and earns it.
// Graphs resolve in the agent's own namespace, for the reason ToolBinding gives.
type KnowledgeBinding struct {
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
	// Version is pinned. "active" is permitted only in the local profile.
	// +kubebuilder:validation:MinLength=1
	Version string `json:"version"`
	// Scope narrows what this agent may read: injected by the gateway, enforced
	// by the provider (design 01 A1).
	// +optional
	Scope *KGScope `json:"scope,omitempty"`
}

type KGScope struct {
	// +optional
	EntityTypes []string `json:"entityTypes,omitempty"`
}

// ToolBinding grants this agent one MCP tool server, resolved in the agent's own
// namespace. A name may be served by a Connector tool facet or by an MCPServer
// CR; names are unique across both kinds within a namespace, enforced at
// admission (design 11 §4), so the binding needs no kind discriminator — which
// is why the reference is inline rather than wrapped.
//
// There is deliberately no namespace field. Design 24 §4.1 derives the `can_call`
// ReBAC tuple FROM this binding, so a cross-namespace reference would authorize
// itself: whoever may create an Agent in one namespace could reach a tool in
// another. Cross-namespace tool use needs consent from the target namespace (the
// ReferenceGrant shape) — a design change, not a field.
type ToolBinding struct {
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
	// RequiresApproval routes calls through the approval interceptor, which
	// returns a retryable typed pending and issues a single-use voucher the
	// gateway consumes (design 22).
	// +optional
	RequiresApproval bool `json:"requiresApproval,omitempty"`
}

// LLMArm discriminates an endpoint identity. Design 02 A24: an endpoint is
// `{arm, that arm's instance fields, model}` — NOT `(provider, model)`, which is
// non-injective. Two Azure OpenAI resources share the arm and a model name while
// differing in endpoint, deployment, region and BAA posture, so a flat string
// cannot tell them apart: the compiler could not prove requested-is-a-subset-of-
// permitted, design 20 could not target the right fallback, and price and drift
// attribution had to guess (r8 BLOCKER 6).
// +kubebuilder:validation:Enum=anthropic;openai;azureopenai;vertexai;bedrock;custom
type LLMArm string

const (
	ArmAnthropic   LLMArm = "anthropic"
	ArmOpenAI      LLMArm = "openai"
	ArmAzureOpenAI LLMArm = "azureopenai"
	ArmVertexAI    LLMArm = "vertexai"
	ArmBedrock     LLMArm = "bedrock"
	ArmCustom      LLMArm = "custom"
)

// AzureOpenAIInstance is what distinguishes two Azure resources. Design 03
// §3.4.1.1: this arm carries no `model` field, and at `apiVersion: v1` the model
// may be supplied by the request — which is why a models-narrowed allowlist
// entry is rejected for it unless a non-v1 deploymentName pins the identity.
type AzureOpenAIInstance struct {
	// +kubebuilder:validation:MinLength=1
	Endpoint string `json:"endpoint"`
	// +optional
	DeploymentName string `json:"deploymentName,omitempty"`
	// +optional
	APIVersion string `json:"apiVersion,omitempty"`
}

type VertexAIInstance struct {
	// +kubebuilder:validation:MinLength=1
	ProjectID string `json:"projectId"`
	// +kubebuilder:validation:MinLength=1
	Region string `json:"region"`
}

type BedrockInstance struct {
	// +kubebuilder:validation:MinLength=1
	Region string `json:"region"`
	// +optional
	Guardrail string `json:"guardrail,omitempty"`
}

type CustomInstance struct {
	// +kubebuilder:validation:MinLength=1
	Host string `json:"host"`
	// +optional
	Port int32 `json:"port,omitempty"`
	// +optional
	PathPrefix string `json:"pathPrefix,omitempty"`
}

// LLMEndpoint is one endpoint identity: an arm, that arm's instance fields, and
// a model where the arm carries one.
//
// The instance block must match the arm, or the identity is not an identity —
// an azureopenai entry with no instance names every Azure resource in the
// tenant, which is the collapse this type exists to prevent.
// +kubebuilder:validation:XValidation:rule="(self.arm == 'azureopenai') == has(self.azureopenai)",message="arm azureopenai requires spec.llm…azureopenai (endpoint, and deploymentName where the model is pinned), and no other arm may set it: two Azure resources share the arm and a model name while differing in endpoint, deployment and BAA posture"
// +kubebuilder:validation:XValidation:rule="(self.arm == 'vertexai') == has(self.vertexai)",message="arm vertexai requires spec.llm…vertexai (projectId, region), and no other arm may set it"
// +kubebuilder:validation:XValidation:rule="(self.arm == 'bedrock') == has(self.bedrock)",message="arm bedrock requires spec.llm…bedrock (region), and no other arm may set it"
// +kubebuilder:validation:XValidation:rule="(self.arm == 'custom') == has(self.custom)",message="arm custom requires spec.llm…custom (host), and no other arm may set it"
// +kubebuilder:validation:XValidation:rule="self.arm != 'azureopenai' || !has(self.model)",message="arm azureopenai carries no model field; the deployment is the identity (design 03 §3.4.1.1). Set azureopenai.deploymentName instead."
type LLMEndpoint struct {
	Arm LLMArm `json:"arm"`
	// Model is carried by anthropic, openai, vertexai, bedrock and custom.
	// azureopenai does not have one — see the CEL rule above.
	// +optional
	Model string `json:"model,omitempty"`
	// +optional
	AzureOpenAI *AzureOpenAIInstance `json:"azureopenai,omitempty"`
	// +optional
	VertexAI *VertexAIInstance `json:"vertexai,omitempty"`
	// +optional
	Bedrock *BedrockInstance `json:"bedrock,omitempty"`
	// +optional
	Custom *CustomInstance `json:"custom,omitempty"`
}

// LLMAllowEntry is an endpoint identity with `models` in place of `model`: the
// permitted set, which requested providers must be a subset of.
// +kubebuilder:validation:XValidation:rule="(self.arm == 'azureopenai') == has(self.azureopenai)",message="arm azureopenai requires an azureopenai instance block; an arm-level entry names every Azure resource in the tenant"
// +kubebuilder:validation:XValidation:rule="(self.arm == 'vertexai') == has(self.vertexai)",message="arm vertexai requires a vertexai instance block"
// +kubebuilder:validation:XValidation:rule="(self.arm == 'bedrock') == has(self.bedrock)",message="arm bedrock requires a bedrock instance block"
// +kubebuilder:validation:XValidation:rule="(self.arm == 'custom') == has(self.custom)",message="arm custom requires a custom instance block"
// +kubebuilder:validation:XValidation:rule="self.arm != 'azureopenai' || !has(self.models) || (has(self.azureopenai) && has(self.azureopenai.deploymentName))",message="a models-narrowed entry is not enforceable on azureopenai unless azureopenai.deploymentName pins the identity: at apiVersion v1 the model may be supplied by the request, so nothing in the emitted provider block constrains it (design 03 §3.4.1.1, A19)"
type LLMAllowEntry struct {
	Arm LLMArm `json:"arm"`
	// Models narrows the entry. Absent means every model this identity serves.
	// +optional
	Models []string `json:"models,omitempty"`
	// +optional
	AzureOpenAI *AzureOpenAIInstance `json:"azureopenai,omitempty"`
	// +optional
	VertexAI *VertexAIInstance `json:"vertexai,omitempty"`
	// +optional
	Bedrock *BedrockInstance `json:"bedrock,omitempty"`
	// +optional
	Custom *CustomInstance `json:"custom,omitempty"`
}

type LLMSpec struct {
	// Providers are endpoint IDENTITIES, not strings (A53).
	// +optional
	Providers []LLMEndpoint `json:"providers,omitempty"`
	// EgressAllowlist may be pinned by a compliance profile (ADR-0014). It is the
	// permitted set; Providers must be a subset of it.
	// +optional
	EgressAllowlist []LLMAllowEntry `json:"egressAllowlist,omitempty"`
	// Fallback is the ModelDrifted remediation target (design 20). It is a full
	// endpoint identity because design 20 A2 keys drift on one: an arm plus a
	// model name would let a canary against a healthy deployment clear drift on
	// the one that was actually drifting.
	// +optional
	Fallback *LLMEndpoint `json:"fallback,omitempty"`
}

type BudgetSpec struct {
	// +optional
	TokensPerDay *int64 `json:"tokensPerDay,omitempty"`
	// USDPerDay compiles at the max price across allowed models; a model
	// matching no pricing pattern is a compile error (ADR-0020).
	// +optional
	USDPerDay *string `json:"usdPerDay,omitempty"`
	// +optional
	TaskTimeout *metav1.Duration `json:"taskTimeout,omitempty"`
	// +optional
	MaxHops *int32 `json:"maxHops,omitempty"`
}

type LoopSpec struct {
	// AllowReentry opts into bounded revisits. Default is any-revisit-denied.
	// +optional
	AllowReentry bool `json:"allowReentry,omitempty"`
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=1
	// +optional
	MaxVisits int32 `json:"maxVisits,omitempty"`
}

// ReleaseSpec is the rollback request surface. Design 02 promised instant
// rollback and retention delivered it — a retained revision's workload, Service
// and immutable material all survive — but until ADR-0031 nothing could ask for
// one. Reapplying the old YAML is not the same operation: env-source CONTENT is
// part of the revision identity, so once a referenced ConfigMap has drifted the
// same spec computes a NEW digest and mints a new revision rather than
// returning to the evaluated one. Status is operator-owned, so a user cannot
// set activeRevisionDigest either.
//
// A field rather than a request CR, because rollback is durable desired state:
// a transient request object races the spec and is reverted by the next
// reconcile, while a field survives GitOps. The consequence GitOps users must
// know is the same one: the pin belongs in the source of truth, or their
// reconciler will revert a manual patch.
type ReleaseSpec struct {
	// TargetRevisionDigest pins the release to a retained revision, named by its
	// FULL SHA-256 — never the 40-bit workload name, which a chosen collision can
	// forge (A57). Unset means follow the ordinary desired spec.
	//
	// The pinned revision is SELECTED, never recomputed: the operator serves the
	// retained material that revision was evaluated with, which is the whole
	// point under source drift.
	// +optional
	// +kubebuilder:validation:Pattern=`^[0-9a-f]{64}$`
	TargetRevisionDigest string `json:"targetRevisionDigest,omitempty"`
}

type GateRef struct {
	// +kubebuilder:validation:MinLength=1
	EvalSuiteRef string `json:"evalSuiteRef"`
}

type ExposeSpec struct {
	// +optional
	A2A *ExposeProtocol `json:"a2a,omitempty"`
}

// ExposeProtocol is one protocol's exposure. An a2a block must state its
// authentication, and auth has no default (design 03 §3.4.4, ADR-0034 C2): a
// default would choose the Agent's identity story — OAuth or shared bearer keys
// — for a developer who never chose one, and the API server would write it into
// every stored object, where nobody can tell it from a choice.
//
// An upgraded install enforces this only once this CRD is applied: the chart
// ships the CRD under crds/, which helm upgrade never updates, so until then
// auth still defaults to oauth and apikey is refused.
//
// +kubebuilder:validation:XValidation:rule="has(self.auth)",message="spec.expose.a2a.auth is required and has no default: set apikey (keys in the group named for this namespace), none (an unauthenticated route), or oauth (not compilable until design 06 ships)"
type ExposeProtocol struct {
	// +kubebuilder:validation:Enum=cluster;org;public
	// +kubebuilder:default=cluster
	Visibility string `json:"visibility,omitempty"`
	// Auth is how the Agent's route authenticates callers. It is required by a
	// CEL rule on the a2a block rather than by `required`, so the refusal names
	// the fix.
	//
	//   - apikey: API keys, admitting the one group named for this Agent's
	//     namespace. There is no field that names another group (design 03 §1.1).
	//   - none: an unauthenticated route.
	//   - oauth: admitted, and not compilable until design 06 ships.
	//
	// NOTHING ENFORCES ANY OF THESE YET. No controller reads this value except
	// to hash it into the revision, and the policy compiler that would turn
	// apikey into a gateway policy does not exist. With the gateway enabled,
	// every Agent's route is unauthenticated whatever this says, and
	// GovernanceSkipped reports it. An Agent stored while this field defaulted
	// carries auth: oauth whether or not a person chose it: removing a default
	// migrates nothing.
	// +kubebuilder:validation:Enum=none;oauth;apikey
	// +optional
	Auth string `json:"auth,omitempty"`
}

// AgentPhase renders in `kubectl get agents`. Guard states (BudgetHeld, Killed)
// win over rollout states.
// +kubebuilder:validation:Enum=Pending;Held;Canary;Ready;Degraded;BudgetHeld;Killed
type AgentPhase string

const (
	PhasePending    AgentPhase = "Pending"
	PhaseHeld       AgentPhase = "Held"
	PhaseCanary     AgentPhase = "Canary"
	PhaseReady      AgentPhase = "Ready"
	PhaseDegraded   AgentPhase = "Degraded"
	PhaseBudgetHeld AgentPhase = "BudgetHeld"
	PhaseKilled     AgentPhase = "Killed"
)

// Condition types. Every unavailable guarantee surfaces as one of these — the
// platform never degrades silently (NFR-8).
const (
	CondRegistered                  = ConditionType("Registered")
	CondCardUnsigned                = ConditionType("CardUnsigned")
	CondIdentityIssued              = ConditionType("IdentityIssued")
	CondIdPUnavailable              = ConditionType("IdPUnavailable")
	CondOnBehalfOfUnavailable       = ConditionType("OnBehalfOfUnavailable")
	CondIdentityBootstrapIncomplete = ConditionType("IdentityBootstrapIncomplete")
	CondKnowledgeBound              = ConditionType("KnowledgeBound")
	CondGatesPassed                 = ConditionType("GatesPassed")
	CondGatesSkipped                = ConditionType("GatesSkipped")
	CondGatesBypassed               = ConditionType("GatesBypassed")
	CondSandboxDowngraded           = ConditionType("SandboxDowngraded")
	CondTaskStateUnverified         = ConditionType("TaskStateUnverified")
	CondScratchpadDegraded          = ConditionType("ScratchpadDegraded")
	CondBudgetExhausted             = ConditionType("BudgetExhausted")
	CondBudgetEnforcementDegraded   = ConditionType("BudgetEnforcementDegraded")
	CondPricingStale                = ConditionType("PricingStale")
	CondReceiptsDegraded            = ConditionType("ReceiptsDegraded")
	CondKilled                      = ConditionType("Killed")
	CondReady                       = ConditionType("Ready")
	CondDegraded                    = ConditionType("Degraded")
	// CondProgressing reports a rollout in flight. Added by A13: without it, a
	// spec edit on a serving agent had to be reported either as Canary — whose
	// meaning §3.3 fixes as "weights are shifting", which is false before design
	// 03 exists — or as Ready=False on an agent that is serving normally, which
	// trips every alert keyed on the canonical condition.
	CondProgressing = ConditionType("Progressing")

	// Thirteen conditions design 02 §3.1 declares that had no constant here.
	// Nine predate the round that added this comment; the vocabulary drifted
	// unnoticed because the closure test asserted a hard-coded count and never
	// compared a single name. Grouped by the design that raises each.
	CondPolicyCompileFailed      = ConditionType("PolicyCompileFailed")      // design 03 §5
	CondPolicyApplyIncomplete    = ConditionType("PolicyApplyIncomplete")    // design 03 §3.3.2
	CondPolicyInputDrifted       = ConditionType("PolicyInputDrifted")       // design 03 A29
	CondCapabilityUnavailable    = ConditionType("CapabilityUnavailable")    // design 03 A33
	CondRevisionRecordUnreadable = ConditionType("RevisionRecordUnreadable") // design 03 A28
	CondLLMFallbackUnavailable   = ConditionType("LLMFallbackUnavailable")   // design 03 A34 / design 20 A3
	CondGatewayIncompatible      = ConditionType("GatewayIncompatible")
	CondModelDrifted             = ConditionType("ModelDrifted") // design 20
	CondGovernanceSkipped        = ConditionType("GovernanceSkipped")
	CondEnvSourceUnresolved      = ConditionType("EnvSourceUnresolved")     // A20
	CondRevisionMaterialChanged  = ConditionType("RevisionMaterialChanged") // A20/A23
	CondImageSignatureUnverified = ConditionType("ImageSignatureUnverified")
	// CondEnvSourceProtectionUnavailable is NO LONGER RAISED. It announced the
	// env-source bypass while A20, A35 and A42 were design; A42's run namespace
	// closed the last of it (design 02 A61) and a real-cluster test proves it.
	// The type stays in §3.1's closed vocabulary so an operator built after A42
	// can still CLEAR a stale True left by one built before; removing it from
	// the API is a later, separate amendment.
	CondEnvSourceProtectionUnavailable = ConditionType("EnvSourceProtectionUnavailable")
	// CondRevisionHashCollision reports two DIFFERENT projections sharing one
	// 40-bit revision name (A37). It is terminal: the operator will not adopt or
	// rewrite a workload whose recorded digest disagrees with the desired one,
	// because doing so is exactly the gate bypass the collision buys.
	CondRevisionHashCollision = ConditionType("RevisionHashCollision")
	// CondRevisionMaterialCollision reports a revision-scoped copy that already
	// exists under the expected name but does not satisfy the whole invariant —
	// kind, owner UID, immutability, bytes (A39). It is terminal for the same
	// reason as above: adopting by name is how a name that looks right comes to
	// hold content nobody hashed.
	CondRevisionMaterialCollision = ConditionType("RevisionMaterialCollision")
	// CondRevisionRecordUnsupported reports a retained revision whose record was
	// written by a schema version this operator no longer carries a decoder for
	// (design 03 A39). Deliberately NOT the corruption condition: "damaged" and
	// "written by a version I dropped" call for different human actions, and
	// collapsing them tells the operator to go looking for the wrong thing.
	CondRevisionRecordUnsupported = ConditionType("RevisionRecordUnsupported")
	// CondRevisionMaterialUnavailable reports a revision-scoped copy that is
	// missing, or whose owner UID or digest disagrees with the record (A41). The
	// revision's route goes to weight 0: losing availability is the right
	// direction when the alternative is serving material nobody can vouch for.
	CondRevisionMaterialUnavailable = ConditionType("RevisionMaterialUnavailable")
	// CondRunNamespaceUnavailable reports that the operator-owned run namespace
	// an Agent's workload and material must live in (design 02 A42/A60) cannot
	// be used. Reasons: NotCreatedByOperator (a namespace of that name exists
	// that the binding record does not vouch for — pre-created, or deleted and
	// recreated), NameCollision (two source namespaces truncate-and-hash to one
	// run name), Terminating (teardown in progress; clears by itself),
	// BindingRecordInvalid (the record is missing a field, carries an unknown
	// state, or a schema version this operator does not decode) and
	// LabelAuthorityAbsent (the admission policies that reserve the assayd.dev
	// namespace labels to the operator are not installed). All but Terminating
	// are terminal until a human acts, and the message says what to do.
	CondRunNamespaceUnavailable = ConditionType("RunNamespaceUnavailable")
)

type AgentStatus struct {
	// +optional
	Phase AgentPhase `json:"phase,omitempty"`
	// ActiveRevision serves traffic.
	// +optional
	ActiveRevision string `json:"activeRevision,omitempty"`
	// CandidateRevision is held at weight 0 until its gates pass. At most one is
	// ever in flight; a new generation supersedes it.
	// +optional
	CandidateRevision string `json:"candidateRevision,omitempty"`
	// ActiveRevisionDigest and CandidateRevisionDigest are the FULL SHA-256 of
	// each revision's behaviour projection, and are the only values compared to
	// decide revision identity (A37).
	//
	// The two fields above are 40-bit names. A chosen 40-bit collision against an
	// attacker-controlled projection takes about a second, and comparing names
	// let a malicious spec present itself as a revision that had already passed
	// its gate. The names stay, because a workload needs one and the ACTIVE
	// column needs to be readable; the decision moved to these.
	// +optional
	ActiveRevisionDigest string `json:"activeRevisionDigest,omitempty"`
	// +optional
	CandidateRevisionDigest string `json:"candidateRevisionDigest,omitempty"`
	// SupersededCandidates records abandoned in-flight candidates so the
	// transition is auditable rather than silent. It is capped: the audit trail
	// belongs in events and receipts, which are durable, whereas an unbounded
	// status list grows an object nobody prunes.
	//
	// Entries are `<name>@<digest>` (A50). A bare name is not an audit record of
	// WHICH revision was abandoned — two colliding projections share the name,
	// so the trail would be unable to distinguish the one that was superseded
	// from the one that superseded it.
	// +kubebuilder:validation:MaxItems=10
	// +optional
	SupersededCandidates []string `json:"supersededCandidates,omitempty"`
	// Cards carries one entry per live revision — two coexist during a rollout.
	// +optional
	Cards []CardStatus `json:"cards,omitempty"`
	// +optional
	Budget *BudgetStatus `json:"budget,omitempty"`
	// Auth is the per-Agent -auth's applied record: design 03 §3.3's
	// status.auth, in the first slice's subset (§1.1), owed to design 02. It is
	// separate from any per-revision record because -auth belongs to no
	// revision: it follows the Agent's current spec.
	//
	// NOTHING WRITES THIS FIELD YET, so it is absent on every Agent. The schema
	// ships first because the chart installs this CRD under crds/, which helm
	// upgrade never updates: a status field an operator writes before its CRD
	// carries it is pruned without an error.
	// +optional
	Auth *AuthStatus `json:"auth,omitempty"`
	// Eval carries the last gate result. It is a printer column because it answers
	// "why is this Held?", which a developer would otherwise reconstruct by
	// reading conditions.
	// +optional
	Eval *EvalStatus `json:"eval,omitempty"`
	// Conditions is a map-list keyed by type: the API server then rejects
	// duplicates outright, for every writer rather than only this operator.
	//
	// The type is closed by ConditionType in Go, NOT by CEL here (A52). A CEL
	// enum was tried and withdrawn: one unrecognised type rejects the ENTIRE
	// status write, so an operator asserting a condition its installed CRD does
	// not list loses `phase`, `activeRevisionDigest` and `Ready` for that Agent
	// and error-loops forever. The chart ships the CRD under `crds/`, which
	// `helm upgrade` never updates, and half the vocabulary belongs to
	// components that ship on their own cadence — so the first one to assert its
	// own condition would take out every Agent it touched. That is NFR-8 with a
	// wider blast radius than the typo the rule was meant to catch.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

type CardStatus struct {
	Revision string `json:"revision"`
	// RevisionDigest identifies which revision this card belongs to (A50); the
	// field above only names it.
	// +optional
	RevisionDigest string `json:"revisionDigest,omitempty"`
	// +optional
	Name string `json:"name,omitempty"`
	// +optional
	Version string `json:"version,omitempty"`
	// +optional
	FetchedAt *metav1.Time `json:"fetchedAt,omitempty"`
	// Digest detects card drift without minting a new revision.
	// +optional
	Digest string `json:"digest,omitempty"`
	// +optional
	Signed bool `json:"signed,omitempty"`
	// SharedTaskState is what the card DECLARED. §3.2 makes the card the
	// declaration point for shared task state, and TaskStateUnverified is keyed
	// off it — so the value has to be recorded rather than re-derived, and a
	// condition that claims to key on it must have something to read.
	// +optional
	SharedTaskState bool `json:"sharedTaskState,omitempty"`
}

// EvalStatus is the last gate result for whichever revision was gated most
// recently — the candidate during a rollout, the active revision otherwise.
type EvalStatus struct {
	// Score is the headline number the gate produced, rendered verbatim in the
	// Eval printer column.
	// +optional
	Score string `json:"score,omitempty"`
	// +optional
	Suite string `json:"suite,omitempty"`
	// Revision names what was gated, so a stale score is recognisable as stale.
	// +optional
	Revision string `json:"revision,omitempty"`
	// RevisionDigest identifies it (A50). The name is 40 bits and a chosen
	// collision against it costs about a second, so a gate controller comparing
	// evalStatus.revision alone can promote a DIFFERENT projection on the verdict
	// this one earned — a decision made before any workload is inspected, so the
	// workload-level collision guard never sees it.
	// +optional
	RevisionDigest string `json:"revisionDigest,omitempty"`
	// +optional
	At *metav1.Time `json:"at,omitempty"`
}

type BudgetStatus struct {
	// +optional
	TokensRemaining *int64 `json:"tokensRemaining,omitempty"`
	// +optional
	USDRemaining *string `json:"usdRemaining,omitempty"`
	// USDSpentToday backs the Cost/Day printer column. Spend is what an operator
	// scans a namespace for; remaining is what an agent is throttled on.
	// +optional
	USDSpentToday *string `json:"usdSpentToday,omitempty"`
	// +optional
	WindowResetsAt *metav1.Time `json:"windowResetsAt,omitempty"`
}

// AuthStatus is design 03 §3.3's status.auth in the first slice's subset
// (§1.1): the served -auth mode, what the last Served transaction verified, and
// the transaction in flight. The operator alone writes it, and nothing writes
// it yet.
//
// Three of §3.3's transaction fields are deliberately absent. targetGroups
// and targetKeySource are filled only by a group or key-source change, and in
// the slice the key source is a constant compiled into the operator and the
// admitted group is the one named for the Agent's namespace, so neither can
// change. afterShift is Loosen's flag for waiting on its edit's weight shift,
// and the slice runs no Loosen. They arrive with the scope that makes them
// reachable. An install that keeps this CRD prunes them without an error if a
// later operator writes them (design 03 A68).
//
// Every field is optional. The presence rules in the field comments are the
// design's, and THE SCHEMA DOES NOT ENFORCE THEM: a CEL rule on status rejects
// the whole status write when it fails, which is the failure A52 withdrew a
// condition enum for (see AgentStatus.Conditions). The writer must hold to
// them; no rule here does.
//
// The mode enums are safe where that condition enum was not. A mode is only
// ever recorded from a spec this same CRD admitted, so an operator never needs
// to write a mode its installed CRD lacks.
type AuthStatus struct {
	// Mode is the served -auth mode. It is absent until a transaction first
	// reaches Served; a Create of an auth: none route then records
	// {mode: none} and nothing beside it. So Mode present means a compiler has
	// recorded this Agent's served -auth state. Adopt keys on status.auth as a
	// whole, never on Mode alone (design 03 §3.3.3).
	// +kubebuilder:validation:Enum=apikey;none
	// +optional
	Mode string `json:"mode,omitempty"`
	// KeySource is the canonical selector the served policy selects. Present
	// exactly when Mode is apikey.
	// +optional
	KeySource string `json:"keySource,omitempty"`
	// AdmittedGroups is the sorted set of groups the last Served transaction
	// verified. Present exactly when Mode is apikey. In the slice it is the one
	// group named for the Agent's namespace.
	// +listType=set
	// +optional
	AdmittedGroups []string `json:"admittedGroups,omitempty"`
	// AppliedDigest is the SHA-256 over the canonical -auth policy last Served.
	// Present exactly when Mode is apikey.
	// +optional
	AppliedDigest string `json:"appliedDigest,omitempty"`
	// Verified records how much of the gateway the enforcement probe reached.
	// Present exactly when Mode is apikey.
	// +optional
	Verified *AuthVerification `json:"verified,omitempty"`
	// Transaction is the -auth transaction in flight, absent in steady state.
	// It is keyed on its target (TargetMode, plus TargetDigest for apikey),
	// never on the Agent's generation, so an edit that leaves the desired -auth
	// unchanged does not disturb it (design 03 §3.3).
	// +optional
	Transaction *AuthTransaction `json:"transaction,omitempty"`
}

// AuthVerification is how much of the gateway a passing -auth enforcement
// probe reached (design 03 §3.3.3, H2).
type AuthVerification struct {
	// ReplicasProbed is the number of gateway replicas the passing probe
	// reached.
	// +optional
	ReplicasProbed int32 `json:"replicasProbed,omitempty"`
	// ReplicasDeclared is the number of gateway replicas the install declares.
	// The design types it "int or unknown", and ABSENT MEANS UNKNOWN. That is
	// the slice's case: the declared replica count is not a prerequisite of the
	// slice, and no flag carries it. Under H2, a probe that passes with the
	// count unknown still means Ready=True, with an informational reason.
	// +optional
	ReplicasDeclared *int32 `json:"replicasDeclared,omitempty"`
}

// AuthTransaction is one -auth transaction's persisted state, so a restarted
// operator re-enters it where it stopped (design 03 §3.3, §3.3.3).
type AuthTransaction struct {
	// Kind is the transaction. The slice runs Create, Lock and Adopt, and an
	// Adopt only ever sits at stage Refused. Narrow and Loosen are design 03
	// §3.3's later scope: specified, not approved, and nothing can enter them.
	// They are in the enum anyway. The chart installs this CRD under crds/,
	// which helm upgrade never updates, so an enum naming only the slice's
	// kinds would make a later operator that writes Narrow against this CRD
	// lose its entire status write, the failure A52 withdrew a condition enum
	// for.
	// +kubebuilder:validation:Enum=Create;Lock;Narrow;Loosen;Adopt
	// +optional
	Kind string `json:"kind,omitempty"`
	// TargetMode is the mode being applied. A transaction is entered only for
	// a target that compiles (design 03 §3.3.1): none always does, and apikey
	// does when its canonical policy can be rendered. A refused Adopt applies
	// nothing and records no target.
	// +kubebuilder:validation:Enum=none;apikey
	// +optional
	TargetMode string `json:"targetMode,omitempty"`
	// TargetDigest is the SHA-256 of the canonical policy being applied.
	// Present exactly when TargetMode is apikey.
	// +optional
	TargetDigest string `json:"targetDigest,omitempty"`
	// Stage is one of design 03 §3.3's stage names: PreparingRoute,
	// ProbingBefore, ApplyingPolicies, Converging, ProbingAfter, Publishing,
	// Served, or Refused for an Adopt.
	// +optional
	Stage string `json:"stage,omitempty"`
	// Written is set in the status update that ENTERS ApplyingPolicies, before
	// the policy write, so a crash between the two reads as written. Every
	// re-entry then repeats the write, which is idempotent (design 03 §3.3.3).
	// +optional
	Written bool `json:"written,omitempty"`
	// Deadline is when the current stage's deadline outcome fires. It is a
	// condition, not a timeout: past it the transaction raises
	// PolicyApplyIncomplete and stays in its stage (design 03 §3.3).
	// +optional
	Deadline *metav1.Time `json:"deadline,omitempty"`
	// Probe holds the answers the probe last observed, never the keys.
	// +optional
	Probe *AuthProbe `json:"probe,omitempty"`
	// BeforeObserved is Lock's: its one before-answer was 200 with a recorded
	// card digest, so Served can say "transition observed".
	// +optional
	BeforeObserved bool `json:"beforeObserved,omitempty"`
	// BeforeRevision is Lock's: the revision the route's one backendRef named
	// when that answer came, whose recorded digest the card matched.
	// BeforeObserved counts only while the backendRef still names it, and a
	// promotion clears both.
	// +optional
	BeforeRevision string `json:"beforeRevision,omitempty"`
	// RefusedMode is Adopt's: the desired mode when the Agent was first
	// refused, kept on every later reconcile. An owner's later edit to apikey,
	// from a RefusedMode that is not apikey, is the consent that ends the
	// refusal (K2, design 03 §3.3.3). An Agent whose spec already said apikey
	// when it was refused records apikey, and stays refused.
	// +kubebuilder:validation:Enum=none;apikey;oauth
	// +optional
	RefusedMode string `json:"refusedMode,omitempty"`
}

// AuthProbe holds the anonymous answers an -auth probe last observed, as HTTP
// status codes. It never holds a key.
type AuthProbe struct {
	// Before is Lock's one before-answer: the status an anonymous request to
	// the Agent's card path got before the policy was written. Absent when no
	// answer came, including when the request timed out. The write never waits
	// on it.
	// +optional
	Before *int32 `json:"before,omitempty"`
	// After is the last anonymous answer after the policy was written. The
	// design trusts the policy only on a 401 (design 03 §3.3.3).
	// +optional
	After *int32 `json:"after,omitempty"`
}

// Agent names are capped so that `<name>-<revision>` — the workload name the
// operator derives — stays inside the 63-character DNS-1123 label limit. 52 + 1
// + 10 = 63 exactly. Without this an agent could be created and then fail at
// reconcile with an error about a name the user never wrote.
// +kubebuilder:validation:XValidation:rule="size(self.metadata.name) <= 52",message="an Agent name must be at most 52 characters: the operator appends a 10-character revision suffix to derive workload names, which must fit the 63-character Kubernetes label limit"
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=ag
// Printer columns are design 02 §3.1's list (A14), and they are a contract: the
// case for a twenty-one-condition status rests on `kubectl get ag` answering the
// common questions without reading one.
//
// Candidate earns its column because Phase no longer implies it. Since A13 a
// rollout in flight renders as Phase=Ready — correctly, the agent IS serving —
// so without this column a candidate that never becomes available shows as a
// completely healthy agent, with Progressing=True visible only to someone who
// thinks to read conditions. Pinned by TestPrinterColumnsMatchTheDesign.
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Active",type=string,JSONPath=`.status.activeRevision`
// +kubebuilder:printcolumn:name="Candidate",type=string,JSONPath=`.status.candidateRevision`
// +kubebuilder:printcolumn:name="Eval",type=string,JSONPath=`.status.eval.score`
// +kubebuilder:printcolumn:name="Cost/Day",type=string,JSONPath=`.status.budget.usdSpentToday`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Agent is any container that speaks A2A and serves an Agent Card.
type Agent struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AgentSpec   `json:"spec"`
	Status AgentStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

type AgentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Agent `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Agent{}, &AgentList{})
}

// ConditionType is the closed condition vocabulary of design 02 §3.1, as a Go
// type. conditionSet.set takes one, so a misspelled condition is a COMPILE
// error rather than a runtime silence: `c.set("PolicyApplyIncompelete", …)`
// used to build, run, and leave every consumer watching the correct spelling
// quiet through the degradation it was meant to announce.
type ConditionType string

// designConditions is the closed condition vocabulary of design 02 §3.1. A
// []metav1.Condition accepts any string, so this list and its test are the only
// things standing between the vocabulary and drift.
func designConditions() []ConditionType {
	return []ConditionType{
		CondRegistered,
		CondCardUnsigned,
		CondIdentityIssued,
		CondIdPUnavailable,
		CondOnBehalfOfUnavailable,
		CondIdentityBootstrapIncomplete,
		CondKnowledgeBound,
		CondGatesPassed,
		CondGatesSkipped,
		CondGatesBypassed,
		CondSandboxDowngraded,
		CondTaskStateUnverified,
		CondScratchpadDegraded,
		CondBudgetExhausted,
		CondBudgetEnforcementDegraded,
		CondPricingStale,
		CondReceiptsDegraded,
		CondKilled,
		CondReady,
		CondDegraded,
		CondProgressing,
		CondPolicyCompileFailed,
		CondPolicyApplyIncomplete,
		CondPolicyInputDrifted,
		CondCapabilityUnavailable,
		CondRevisionRecordUnreadable,
		CondLLMFallbackUnavailable,
		CondGatewayIncompatible,
		CondModelDrifted,
		CondGovernanceSkipped,
		CondEnvSourceUnresolved,
		CondRevisionMaterialChanged,
		CondImageSignatureUnverified,
		CondEnvSourceProtectionUnavailable,
		CondRevisionHashCollision,
		CondRevisionMaterialCollision,
		CondRevisionRecordUnsupported,
		CondRevisionMaterialUnavailable,
		CondRunNamespaceUnavailable,
	}
}
