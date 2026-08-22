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
//   - The hash is computed from spec ALONE. An earlier draft mixed in the card
//     digest, which is knowable only after the revision's workload runs, so the
//     hash would have changed after deploy and orphaned the workload it named
//     (02-review, blocker).
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
	"sort"

	corev1 "k8s.io/api/core/v1"

	plumev1alpha1 "github.com/ejs-5/plume/api/v1alpha1"
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
	Tools     []string        `json:"tools,omitempty"`
	LLM       *llm            `json:"llm,omitempty"`
	External  *external       `json:"external,omitempty"`
}

// Nested structs rather than joined strings, so JSON supplies the delimiters.
// Joining with "/" let {provider: azure, model: openai/gpt-4} and
// {provider: azure/openai, model: gpt-4} hash identically — an ungated swap of
// the model that answers requests.
type envVar struct {
	Name string `json:"name"`
	// Value is hashed; ValueFrom is represented by its REFERENT, since the
	// referent's contents change without a spec edit. Rotating the value inside a
	// Secret must not re-gate; repointing at a different Secret must.
	Value     string     `json:"value,omitempty"`
	ValueFrom *envSource `json:"valueFrom,omitempty"`
}

type envSource struct {
	Kind string `json:"kind"`
	Name string `json:"name,omitempty"`
	Key  string `json:"key,omitempty"`
	// Raw carries the marshalled union for any arm this code does not name, so an
	// upstream addition over-gates rather than collapsing to a constant.
	Raw string `json:"raw,omitempty"`
}

type envFromSource struct {
	ConfigMap string `json:"configMap,omitempty"`
	Secret    string `json:"secret,omitempty"`
	Prefix    string `json:"prefix,omitempty"`
	Raw       string `json:"raw,omitempty"`
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
	Providers []string  `json:"providers,omitempty"`
	Fallback  *modelRef `json:"fallback,omitempty"`
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

// Hash returns the revision identity of a spec: a DNS-safe, stable,
// 10-character digest of A12's behaviour projection.
func Hash(spec plumev1alpha1.AgentSpec) string {
	// json.Marshal emits struct fields in declaration order and map keys
	// lexically; the projection contains no maps, so this is canonical without a
	// separate canonicalizer. Lists that carry no order semantics are sorted in
	// project().
	encoded, err := json.Marshal(project(spec))
	if err != nil {
		// Unreachable: the projection is strings and slices of strings. Hashing the
		// error text keeps the function total and distinct rather than collapsing
		// every revision onto one constant.
		encoded = []byte("revision-projection-marshal-error:" + err.Error())
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])[:HashLength]
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
		// Name only: RequiresApproval is approval policy the gateway enforces, not
		// a change in what the agent can reach.
		b.Tools = append(b.Tools, t.Name)
	}

	if l := spec.LLM; l != nil {
		x := &llm{Providers: append([]string(nil), l.Providers...)}
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
	sort.Strings(b.Tools)
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
	switch {
	case v.SecretKeyRef != nil:
		return &envSource{Kind: "secret", Name: v.SecretKeyRef.Name, Key: v.SecretKeyRef.Key}
	case v.ConfigMapKeyRef != nil:
		return &envSource{Kind: "configMap", Name: v.ConfigMapKeyRef.Name, Key: v.ConfigMapKeyRef.Key}
	case v.FieldRef != nil:
		return &envSource{Kind: "field", Key: v.FieldRef.FieldPath}
	case v.ResourceFieldRef != nil:
		return &envSource{Kind: "resource", Name: v.ResourceFieldRef.ContainerName, Key: v.ResourceFieldRef.Resource}
	default:
		return &envSource{Kind: "raw", Raw: marshalOrEmpty(v)}
	}
}

// envFromRef names a whole-source import, with the same over-gate default.
func envFromRef(f corev1.EnvFromSource) envFromSource {
	out := envFromSource{Prefix: f.Prefix}
	switch {
	case f.ConfigMapRef != nil:
		out.ConfigMap = f.ConfigMapRef.Name
	case f.SecretRef != nil:
		out.Secret = f.SecretRef.Name
	default:
		out.Raw = marshalOrEmpty(f)
	}
	return out
}

func marshalOrEmpty(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "unmarshalable"
	}
	return string(b)
}
