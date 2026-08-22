// Package revision computes the identity of an Agent revision.
//
// A revision is what an eval gate gates and what a rollback rolls back to, so
// this hash decides when an agent pays for a full eval-and-canary cycle. Design
// 02 §3.3 (A12) splits the CR into a behaviour surface, where a change can alter
// what the agent does and must therefore pass a gate, and a policy surface,
// which the compiler applies in place.
//
// Two properties are load-bearing. The hash is computed from spec ALONE — an
// earlier draft mixed in the card digest, which is only knowable after the
// revision's workload runs, so the hash would have changed after deploy and
// orphaned the workload it named (02-review, blocker). And the projection is an
// explicit ALLOWLIST: a field added to the CRD later is policy-surface until
// A12's table says otherwise, because the safe failure is a missing gate on a
// policy knob, never a missing gate on behaviour.
package revision

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"

	corev1 "k8s.io/api/core/v1"

	plumev1alpha1 "github.com/ejs-5/plume/api/v1alpha1"
)

// HashLength is how many hex characters of the digest name a revision. Ten
// characters is 40 bits: collision-safe for the revisions one agent will ever
// have, and short enough that `<name>-<hash>` stays inside the 63-character
// DNS-1123 label limit for a workload name.
const HashLength = 10

// behaviour is the projection of AgentSpec that A12 declares revision-minting.
// Field names are stable and independent of the CRD's JSON tags: renaming a CRD
// field must not silently re-hash every existing revision.
type behaviour struct {
	Image     string          `json:"image,omitempty"`
	Env       []envVar        `json:"env,omitempty"`
	EnvFrom   []envFromSource `json:"envFrom,omitempty"`
	Sandbox   string          `json:"sandbox,omitempty"`
	CardPath  string          `json:"cardPath,omitempty"`
	Knowledge []knowledgeBind `json:"knowledge,omitempty"`
	Tools     []string        `json:"tools,omitempty"`
	LLM       *llm            `json:"llm,omitempty"`
	External  string          `json:"external,omitempty"`
}

type envVar struct {
	Name string `json:"name"`
	// Value is included; ValueFrom is represented by its reference, since the
	// referent's *contents* are not knowable here and change without a spec edit.
	Value     string `json:"value,omitempty"`
	ValueFrom string `json:"valueFrom,omitempty"`
}

type envFromSource struct {
	ConfigMap string `json:"configMap,omitempty"`
	Secret    string `json:"secret,omitempty"`
	Prefix    string `json:"prefix,omitempty"`
}

type knowledgeBind struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	// Scope narrows what the agent may read, so widening or narrowing it changes
	// behaviour and must be gated.
	EntityTypes []string `json:"entityTypes,omitempty"`
}

type llm struct {
	Providers []string `json:"providers,omitempty"`
	Fallback  string   `json:"fallback,omitempty"`
}

// Hash returns the revision identity of a spec: a DNS-safe, stable, 10-character
// digest of A12's behaviour projection.
func Hash(spec plumev1alpha1.AgentSpec) string {
	b := project(spec)

	// json.Marshal sorts struct fields by declaration order and map keys
	// lexically, and the projection contains no maps, so this encoding is
	// canonical without a separate canonicalizer. List order is preserved
	// deliberately: a reordered tools list is a different grant sequence.
	encoded, err := json.Marshal(b)
	if err != nil {
		// The projection is composed only of strings and slices of strings, which
		// cannot fail to marshal. Panicking here would be a lie about reachability;
		// returning a constant would silently collapse every revision into one. So
		// hash the error text: distinct, stable, and it shows up loudly in a name.
		encoded = []byte("revision-projection-marshal-error:" + err.Error())
	}

	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])[:HashLength]
}

// project maps a spec onto A12's behaviour surface. Every field is named
// explicitly. Adding a case here is a deliberate act with a design amendment
// behind it; forgetting to add one leaves a new field policy-surface, which is
// the safe direction.
func project(spec plumev1alpha1.AgentSpec) behaviour {
	var b behaviour

	if r := spec.Runtime; r != nil {
		b.Image = r.Image
		if r.Sandbox != nil {
			b.Sandbox = r.Sandbox.Profile
		}
		for _, e := range r.Env {
			b.Env = append(b.Env, envVar{Name: e.Name, Value: e.Value, ValueFrom: envSourceRef(e)})
		}
		for _, f := range r.EnvFrom {
			b.EnvFrom = append(b.EnvFrom, envFromRef(f))
		}
		// NOT projected, per A12: Replicas (a scale operation), Resources
		// (capacity), Port (wiring, not behaviour).
	}

	if e := spec.External; e != nil {
		b.External = e.Endpoint
		// NOT projected: OAuthClientRef — credential rotation must not re-gate.
	}

	b.CardPath = spec.Card.Path

	for _, k := range spec.Knowledge {
		kb := knowledgeBind{Name: k.Name, Version: k.Version}
		if k.Scope != nil {
			kb.EntityTypes = k.Scope.EntityTypes
		}
		b.Knowledge = append(b.Knowledge, kb)
	}

	for _, t := range spec.Tools {
		// Name only: RequiresApproval is approval policy the gateway enforces, not
		// a change in what the agent can reach.
		b.Tools = append(b.Tools, t.Name)
	}

	if l := spec.LLM; l != nil {
		x := &llm{Providers: l.Providers}
		if l.Fallback != nil {
			x.Fallback = l.Fallback.Provider + "/" + l.Fallback.Model
		}
		b.LLM = x
		// NOT projected: EgressAllowlist — a compliance control compiled to gateway
		// policy (ADR-0014), enforced live rather than gated.
	}

	// NOT projected, per A12: Budget, Gates, Expose, Loop.
	return b
}

// envSourceRef names the referent of a valueFrom without reading it. The
// contents of a Secret or ConfigMap change without a spec edit, so hashing them
// would mint revisions nobody asked for; naming the reference means repointing
// an agent at a *different* Secret is correctly a behaviour change, while
// rotating the value inside one is correctly not.
func envSourceRef(e corev1.EnvVar) string {
	v := e.ValueFrom
	if v == nil {
		return ""
	}
	switch {
	case v.SecretKeyRef != nil:
		return "secret:" + v.SecretKeyRef.Name + "/" + v.SecretKeyRef.Key
	case v.ConfigMapKeyRef != nil:
		return "configMap:" + v.ConfigMapKeyRef.Name + "/" + v.ConfigMapKeyRef.Key
	case v.FieldRef != nil:
		return "field:" + v.FieldRef.FieldPath
	case v.ResourceFieldRef != nil:
		return "resource:" + v.ResourceFieldRef.ContainerName + "/" + v.ResourceFieldRef.Resource
	default:
		// An unrecognized source is a corev1 kind added after this was written.
		// Naming it as unknown keeps the hash stable and total rather than
		// silently projecting it as empty, which would drop it from the gate.
		return "unknown"
	}
}

// envFromRef names a whole-source import the same way, for the same reason.
func envFromRef(f corev1.EnvFromSource) envFromSource {
	out := envFromSource{Prefix: f.Prefix}
	if f.ConfigMapRef != nil {
		out.ConfigMap = f.ConfigMapRef.Name
	}
	if f.SecretRef != nil {
		out.Secret = f.SecretRef.Name
	}
	return out
}

// projectionSource returns this file's text, so the allowlist test can assert
// the projection does not reach for reflection or whole-spec marshalling.
func projectionSource() (string, error) {
	_, self, _, ok := runtime.Caller(0)
	if !ok {
		return "", os.ErrNotExist
	}
	b, err := os.ReadFile(filepath.Clean(self))
	return string(b), err
}
