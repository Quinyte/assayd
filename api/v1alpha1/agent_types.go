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
type AgentSpec struct {
	// Runtime describes an in-cluster agent workload.
	// +optional
	Runtime *AgentRuntime `json:"runtime,omitempty"`

	// External registers an agent that runs outside this cluster. It receives no
	// SVID; it authenticates to the gateway with OAuth client credentials.
	// +optional
	External *ExternalAgent `json:"external,omitempty"`

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

	// Budget is enforced in two tiers (ADR-0020): the gateway applies a
	// conservative local approximation, and design 04's receipt aggregate is the
	// exact tier. Per-day windows reset at 00:00 UTC.
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

type GateRef struct {
	// +kubebuilder:validation:MinLength=1
	EvalSuiteRef string `json:"evalSuiteRef"`
}

type ExposeSpec struct {
	// +optional
	A2A *ExposeProtocol `json:"a2a,omitempty"`
}

type ExposeProtocol struct {
	// +kubebuilder:validation:Enum=cluster;org;public
	// +kubebuilder:default=cluster
	Visibility string `json:"visibility,omitempty"`
	// +kubebuilder:validation:Enum=none;oauth
	// +kubebuilder:default=oauth
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
	// CondEnvSourceProtectionUnavailable is SUPERSEDED by A35, which replaced the
	// seal with immutable revision-scoped copies. It is declared because §3.1
	// still lists it and this list must match §3.1 exactly; both go together in
	// A35's owed retirement sweep, not separately.
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
	// LabelAuthorityAbsent (the admission policies that reserve the plume.dev
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
