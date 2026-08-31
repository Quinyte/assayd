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
	// Image must be cosign-signed; admission rejects unsigned images.
	// +kubebuilder:validation:MinLength=1
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

type LLMSpec struct {
	// +optional
	Providers []string `json:"providers,omitempty"`
	// EgressAllowlist may be pinned by a compliance profile (ADR-0014).
	// +optional
	EgressAllowlist []string `json:"egressAllowlist,omitempty"`
	// Fallback is the ModelDrifted remediation target (design 20).
	// +optional
	Fallback *ModelRef `json:"fallback,omitempty"`
}

type ModelRef struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
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
	CondRegistered                  = "Registered"
	CondCardUnsigned                = "CardUnsigned"
	CondIdentityIssued              = "IdentityIssued"
	CondIdPUnavailable              = "IdPUnavailable"
	CondOnBehalfOfUnavailable       = "OnBehalfOfUnavailable"
	CondIdentityBootstrapIncomplete = "IdentityBootstrapIncomplete"
	CondKnowledgeBound              = "KnowledgeBound"
	CondGatesPassed                 = "GatesPassed"
	CondGatesSkipped                = "GatesSkipped"
	CondGatesBypassed               = "GatesBypassed"
	CondSandboxDowngraded           = "SandboxDowngraded"
	CondTaskStateUnverified         = "TaskStateUnverified"
	CondScratchpadDegraded          = "ScratchpadDegraded"
	CondBudgetExhausted             = "BudgetExhausted"
	CondBudgetEnforcementDegraded   = "BudgetEnforcementDegraded"
	CondPricingStale                = "PricingStale"
	CondReceiptsDegraded            = "ReceiptsDegraded"
	CondKilled                      = "Killed"
	CondReady                       = "Ready"
	CondDegraded                    = "Degraded"
	// CondProgressing reports a rollout in flight. Added by A13: without it, a
	// spec edit on a serving agent had to be reported either as Canary — whose
	// meaning §3.3 fixes as "weights are shifting", which is false before design
	// 03 exists — or as Ready=False on an agent that is serving normally, which
	// trips every alert keyed on the canonical condition.
	CondProgressing = "Progressing"

	// Thirteen conditions design 02 §3.1 declares that had no constant here.
	// Nine predate the round that added this comment; the vocabulary drifted
	// unnoticed because the closure test asserted a hard-coded count and never
	// compared a single name. Grouped by the design that raises each.
	CondPolicyCompileFailed      = "PolicyCompileFailed"      // design 03 §5
	CondPolicyApplyIncomplete    = "PolicyApplyIncomplete"    // design 03 §3.3.2
	CondPolicyInputDrifted       = "PolicyInputDrifted"       // design 03 A29
	CondCapabilityUnavailable    = "CapabilityUnavailable"    // design 03 A33
	CondRevisionRecordUnreadable = "RevisionRecordUnreadable" // design 03 A28
	CondLLMFallbackUnavailable   = "LLMFallbackUnavailable"   // design 03 A34 / design 20 A3
	CondGatewayIncompatible      = "GatewayIncompatible"
	CondModelDrifted             = "ModelDrifted" // design 20
	CondGovernanceSkipped        = "GovernanceSkipped"
	CondEnvSourceUnresolved      = "EnvSourceUnresolved"     // A20
	CondRevisionMaterialChanged  = "RevisionMaterialChanged" // A20/A23
	CondImageSignatureUnverified = "ImageSignatureUnverified"
	// CondEnvSourceProtectionUnavailable is SUPERSEDED by A35, which replaced the
	// seal with immutable revision-scoped copies. It is declared because §3.1
	// still lists it and this list must match §3.1 exactly; both go together in
	// A35's owed retirement sweep, not separately.
	CondEnvSourceProtectionUnavailable = "EnvSourceProtectionUnavailable"
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
	// SupersededCandidates records abandoned in-flight candidates so the
	// transition is auditable rather than silent. It is capped: the audit trail
	// belongs in events and receipts, which are durable, whereas an unbounded
	// status list grows an object nobody prunes.
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
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

type CardStatus struct {
	Revision string `json:"revision"`
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

// designConditions is the closed condition vocabulary of design 02 §3.1. A
// []metav1.Condition accepts any string, so this list and its test are the only
// things standing between the vocabulary and drift.
func designConditions() []string {
	return []string{
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
	}
}
