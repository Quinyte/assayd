// Package revision computes the identity of an Agent revision.
//
// A revision is what an eval gate gates and what a rollback rolls back to, so
// this hash decides when an agent pays for a full eval-and-canary cycle. Design
// 02 §3.3 (A12) splits the CR into a behaviour surface — a change to what the
// agent can do, produce, or reach, which must pass a gate — and a policy
// surface, which changes how much, how fast, or who may call, and is applied in
// place. Both surfaces compile to gateway config, so the compile path is not the
// discriminator; capability is.
//
// Three properties are load-bearing:
//
//   - The hash is computed from the spec projection alone — never from the card
//     digest, which is knowable only after the revision's workload runs, so
//     mixing it in would change the hash after deploy and orphan the workload it
//     named (02-review, blocker).
//
//     NOT YET TRUE, and stated so rather than implied: design 02 A20 extends the
//     projection with a digest of the RESOLVED CONTENTS of every ConfigMap and
//     Secret reachable through envFrom or env[].valueFrom. Referent identity
//     alone lets anyone with update on a referenced object replace a system
//     prompt and have it serve under the old revision's gate result. This code
//     still hashes the referent only; the content extension is unimplemented.
//
//   - Unrecognized inputs OVER-gate rather than collapse. Any arm of a k8s union
//     type this code does not name explicitly is hashed by its marshalled form,
//     so a field added upstream mints a spurious revision instead of silently
//     letting a repoint through ungated.
//
//   - Classification is COMPULSORY, not defaulted. Every AgentSpec field is
//     named in behaviourFields or policyFields, and a test reflects over the
//     struct to prove it; adding a CRD field breaks that test until someone
//     classifies it. An earlier version defaulted new fields to policy-surface
//     and called that safe — the critic disproved it by adding a SystemPrompt
//     field and watching the suite stay green while it reached production
//     ungated.
package revision

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	corev1 "k8s.io/api/core/v1"

	plumev1alpha1 "github.com/Quinyte/plume/api/v1alpha1"
)

// HashLength is how many hex characters of the digest name a revision. Ten hex
// characters is 40 bits — a birthday bound around 2^20, against a namespace
// that is per-agent (`<name>-<hash>`) and bounded to dozens of revisions by
// revisionHistoryLimit.
const HashLength = 10

// behaviour is the projection of AgentSpec that A12 declares revision-minting.
//
// Field ORDER, JSON TAGS and omitempty in this struct are part of the revision
// contract: encoding/json emits in declaration order, so a cosmetic reordering
// would re-mint every revision in every cluster. TestGoldenDigest pins it.
type behaviour struct {
	Image     string          `json:"image,omitempty"`
	Env       []envVar        `json:"env,omitempty"`
	EnvFrom   []envFromSource `json:"envFrom,omitempty"`
	Sandbox   string          `json:"sandbox,omitempty"`
	Knowledge []knowledgeBind `json:"knowledge,omitempty"`
	Tools     []toolBind      `json:"tools,omitempty"`
	Budget    *budget         `json:"budget,omitempty"`
	Expose    *expose         `json:"expose,omitempty"`
	LLM       *llm            `json:"llm,omitempty"`
	External  *external       `json:"external,omitempty"`
}

// Nested structs rather than joined strings, so JSON supplies the delimiters.
// Joining with "/" let {provider: azure, model: openai/gpt-4} and
// {provider: azure/openai, model: gpt-4} hash identically — an ungated swap of
// the model that answers requests.
type envVar struct {
	Name string `json:"name"`
	// Value is hashed. ValueFrom is currently represented by its REFERENT ONLY,
	// which is a KNOWN BYPASS, not a design choice.
	//
	// The sentence that stood here said "rotating the value inside a Secret must
	// not re-gate; repointing at a different Secret must." That rule is
	// WITHDRAWN. Design 02 A20 replaced it: anyone with update on a referenced
	// object can swap a system prompt, the pod restarts, and the new behaviour
	// serves under the old revision and the old gate result — with no permission
	// to touch the Agent at all. Kubernetes never refreshed a process
	// environment for envFrom/valueFrom anyway, so the no-re-gate property the
	// old rule protected did not exist.
	//
	// What is owed here is A20 + A35 + A42 together, and none of it is in this
	// package: the identity must be minted from RESOLVED CONTENT, the content
	// copied into immutable revision-scoped material, and the workload pointed
	// at the copy in an operator-owned namespace. Until then the referent is
	// what this hashes, and design 02 §3.3's content-hashing rule describes
	// something the code does not do.
	Value     string     `json:"value,omitempty"`
	ValueFrom *envSource `json:"valueFrom,omitempty"`
}

// envSource carries the COMPLETE upstream selector, canonically marshalled.
//
// It used to name four arms and copy two or three fields out of each, with a
// raw fallback for arms it did not recognise. The fallback was the safe part;
// the naming was the hole. Every named arm silently dropped leaves —
// SecretKeyRef.Optional, ConfigMapKeyRef.Optional, FieldRef.APIVersion,
// ResourceFieldRef.Divisor — so flipping `optional: false` to `true` on a
// referenced prompt hashed identically, and a replacement Pod would then start
// WITHOUT the prompt instead of failing, serving different behaviour under the
// old gate result.
//
// Marshalling the whole struct is not a shortcut, it is the stronger property:
// a leaf added upstream is covered the day it appears rather than the day
// someone remembers to name it. A quantity written "1000m" and one written "1"
// marshal differently and so over-gate, which is the direction this package has
// always chosen when it cannot be sure.
type envSource struct {
	Selector string `json:"selector"`
}

// envFromSource is the complete upstream EnvFromSource, for the same reason as
// envSource — and Prefix comes along with it rather than being copied out.
type envFromSource struct {
	Selector string `json:"selector"`
}

type knowledgeBind struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	// Scope is a pointer so that absent and present-but-empty are distinguishable:
	// design 01 does not define whether an empty entityTypes list means "no
	// narrowing" or "deny all", and collapsing them would make one of those a
	// silent, ungated grant change.
	Scope *kgScope `json:"scope,omitempty"`
}

type kgScope struct {
	EntityTypes []string `json:"entityTypes,omitempty"`
}

type llm struct {
	Providers []string `json:"providers,omitempty"`
	// EgressAllowlist is the set of endpoints the agent may reach. Design 02 A16
	// classified it as behaviour — symmetric, so any change mints — and A6
	// recorded that design 03 consumes it. It was never added HERE, so widening
	// a HIPAA agent from internal/* to an external provider hashed identically
	// and reached the compiler with no eval result for the reachability change.
	// Classified in one place, projected in none: the gap the aggregate `LLM`
	// mutation test could not see, because mutating the whole block changes
	// providers too.
	// A POINTER, so "no allowlist" and "an empty allowlist" stay distinguishable.
	// Nothing in the corpus defines whether an empty list means "no narrowing" or
	// "reach nothing", and collapsing them would make one of those a silent,
	// ungated grant change — the same reasoning kgScope records.
	EgressAllowlist *[]string `json:"egressAllowlist,omitempty"`
	Fallback        *modelRef `json:"fallback,omitempty"`
}

// toolBind carries RequiresApproval because design 02 A30 moved it to the
// behaviour surface. It was policy-surface, and design 03's closed comparator
// registry has no approval concern — so turning approval ON classified as an
// unknown tightening, which on the policy surface has no revision to mint and
// therefore no safe transaction at all. Both directions gate now: turning it off
// is a widening that must not reach production ungated either.
type toolBind struct {
	Name             string `json:"name"`
	RequiresApproval bool   `json:"requiresApproval,omitempty"`
}

// budget and expose are behaviour, per A25. They were classified policy-surface
// on the reasoning that they change "how much" and "who may call" rather than
// what the agent does — but a budget is what stops a runaway loop and expose
// decides who may reach the agent at all, so widening either without a gate is
// the ungated-widening this projection exists to prevent.
type budget struct {
	TokensPerDay *int64  `json:"tokensPerDay,omitempty"`
	USDPerDay    *string `json:"usdPerDay,omitempty"`
	TaskTimeout  string  `json:"taskTimeout,omitempty"`
	MaxHops      *int32  `json:"maxHops,omitempty"`
}

type expose struct {
	// A2A is a pointer so absent and present-but-defaulted stay distinguishable:
	// no expose at all is not the same grant as expose with schema defaults.
	A2A *exposeProtocol `json:"a2a,omitempty"`
}

type exposeProtocol struct {
	Visibility string `json:"visibility,omitempty"`
	Auth       string `json:"auth,omitempty"`
}

type modelRef struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

type external struct {
	Endpoint string `json:"endpoint,omitempty"`
	// OAuthClientRef selects WHICH CLIENT an external agent authenticates as —
	// its identity, on which design 24 keys can_invoke, can_call and the act-chain
	// check. Repointing it reaches a different set of tools, so it is a capability
	// change and must be gated. (Rotating the credential *behind* a client is a
	// Secret update this hash never sees, which is correct.)
	OAuthClientRef string `json:"oauthClientRef,omitempty"`
}

// Digest returns the full SHA-256 of the behaviour projection — the SECURITY
// identity of a revision, and the only value that may be compared to decide
// whether two specs are the same revision.
//
// Hash below truncates to 40 bits, which is a NAME, not an identity. Forty bits
// is not a security boundary against an attacker-controlled projection: a
// chosen collision between a safe and a malicious image took **1.2 seconds** on
// a laptop, and it is a total bypass of the eval gate — the safe member passes
// evaluation as revision H, the malicious member computes the same H, the
// controller sees activeRevision == desired, skips gating, and rewrites the
// Deployment named H to the malicious image while status still names the
// revision that passed. The old justification counted how many revisions
// coexist, which answers an accidental-collision question nobody was asking.
func Digest(spec plumev1alpha1.AgentSpec) string {
	sum := sha256.Sum256(encode(spec))
	return hex.EncodeToString(sum[:])
}

// Hash returns the revision NAME: a DNS-safe, stable, 10-character prefix of
// Digest, used to name workloads and to render the ACTIVE column.
//
// It is display and naming only. Never branch on it. Two specs sharing a Hash
// are not the same revision unless their Digests agree, and the controller
// treats a same-name/different-digest pair as a terminal RevisionHashCollision
// rather than as one revision.
func Hash(spec plumev1alpha1.AgentSpec) string {
	// json.Marshal emits struct fields in declaration order and map keys
	// lexically; the projection contains no maps, so this is canonical without a
	// separate canonicalizer. Lists that carry no order semantics are sorted in
	// project().
	return Digest(spec)[:HashLength]
}

// encode canonicalizes the projection. Both Digest and Hash go through it, so
// they can never disagree about what was hashed.
func encode(spec plumev1alpha1.AgentSpec) []byte {
	encoded, err := json.Marshal(project(spec))
	if err != nil {
		// Unreachable: the projection is strings and slices of strings. Encoding the
		// error keeps the function total and distinct rather than collapsing every
		// failing revision onto one constant.
		encoded = []byte(fmt.Sprintf("revision-projection-marshal-error:%v:%#v", err, spec))
	}
	return encoded
}

// project maps a spec onto A12's behaviour surface. Every included field is
// named here and in behaviourFields; every excluded one is named in
// policyFields. TestEveryFieldIsClassified proves the two lists are exhaustive.
func project(spec plumev1alpha1.AgentSpec) behaviour {
	var b behaviour

	if r := spec.Runtime; r != nil {
		b.Image = r.Image
		if r.Sandbox != nil {
			b.Sandbox = r.Sandbox.Profile
		}
		// Env order is preserved: it is semantic for interpolation.
		for _, e := range r.Env {
			b.Env = append(b.Env, envVar{Name: e.Name, Value: e.Value, ValueFrom: envSourceRef(e.ValueFrom)})
		}
		for _, f := range r.EnvFrom {
			b.EnvFrom = append(b.EnvFrom, envFromRef(f))
		}
	}

	if e := spec.External; e != nil {
		b.External = &external{Endpoint: e.Endpoint, OAuthClientRef: e.OAuthClientRef}
	}

	for _, k := range spec.Knowledge {
		kb := knowledgeBind{Name: k.Name, Version: k.Version}
		if k.Scope != nil {
			kb.Scope = &kgScope{EntityTypes: append([]string(nil), k.Scope.EntityTypes...)}
			sort.Strings(kb.Scope.EntityTypes)
		}
		b.Knowledge = append(b.Knowledge, kb)
	}

	for _, t := range spec.Tools {
		b.Tools = append(b.Tools, toolBind{Name: t.Name, RequiresApproval: t.RequiresApproval})
	}

	if bs := spec.Budget; bs != nil {
		nb := &budget{TokensPerDay: bs.TokensPerDay, USDPerDay: bs.USDPerDay, MaxHops: bs.MaxHops}
		if bs.TaskTimeout != nil {
			nb.TaskTimeout = bs.TaskTimeout.Duration.String()
		}
		b.Budget = nb
	}

	if e := spec.Expose; e != nil {
		ne := &expose{}
		if e.A2A != nil {
			ne.A2A = &exposeProtocol{Visibility: e.A2A.Visibility, Auth: e.A2A.Auth}
		}
		b.Expose = ne
	}

	if l := spec.LLM; l != nil {
		x := &llm{Providers: append([]string(nil), l.Providers...)}
		if l.EgressAllowlist != nil {
			allow := append([]string(nil), l.EgressAllowlist...)
			sort.Strings(allow) // a set; a re-serialized manifest must not re-gate
			x.EgressAllowlist = &allow
		}
		if l.Fallback != nil {
			x.Fallback = &modelRef{Provider: l.Fallback.Provider, Model: l.Fallback.Model}
		}
		b.LLM = x
	}

	// These lists carry no order semantics anywhere in the corpus — design 03
	// compiles tools to a tool-filter policy (a set), llm.providers to a
	// commutative max-price computation, knowledge to one route per binding. But
	// kustomize, helm and kubectl round-trips all re-serialize lists, so hashing
	// their order would re-gate on a no-op diff.
	sort.Slice(b.Tools, func(i, j int) bool { return b.Tools[i].Name < b.Tools[j].Name })
	sort.Strings(b.LLM.providersOrNil())
	sort.Slice(b.Knowledge, func(i, j int) bool {
		if b.Knowledge[i].Name != b.Knowledge[j].Name {
			return b.Knowledge[i].Name < b.Knowledge[j].Name
		}
		return b.Knowledge[i].Version < b.Knowledge[j].Version
	})
	return b
}

func (l *llm) providersOrNil() []string {
	if l == nil {
		return nil
	}
	return l.Providers
}

// envSourceRef names the referent of a valueFrom without reading it.
//
// The default arm is INJECTIVE, not a constant. corev1.EnvVarSource gains arms
// upstream (v0.36.4 has five), and an unnamed arm that collapsed to "unknown"
// let a repoint from one file source to another pass ungated.
func envSourceRef(v *corev1.EnvVarSource) *envSource {
	if v == nil {
		return nil
	}
	return &envSource{Selector: marshalOrEmpty(v)}
}

// envFromRef names a whole-source import, with the same over-gate default.
func envFromRef(f corev1.EnvFromSource) envFromSource {
	return envFromSource{Selector: marshalOrEmpty(f)}
}

// marshalOrEmpty is total. An earlier version returned the constant
// "unmarshalable" on error, which is the collapse-to-a-constant defect this
// package exists to avoid: two different unmarshalable values would have hashed
// the same. The error text is included so distinct failures stay distinct, and
// %#v so two values with the same error text still differ.
func marshalOrEmpty(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("unmarshalable:%v:%#v", err, v)
	}
	return string(b)
}
