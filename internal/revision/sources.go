package revision

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"

	corev1 "k8s.io/api/core/v1"

	plumev1alpha1 "github.com/Quinyte/plume/api/v1alpha1"
)

// SourceRef names a ConfigMap or Secret an Agent's runtime reads from.
//
// Design 02 A20: the revision identity must cover the resolved CONTENT of these,
// not their names. Referent identity alone lets anyone with `update` on a
// referenced object replace a system prompt or a provider endpoint, and the new
// behaviour then serves under the old revision and the old gate result — with no
// permission to touch the Agent at all.
type SourceRef struct {
	Kind string // "ConfigMap" or "Secret"
	Name string
}

func (r SourceRef) String() string { return r.Kind + "/" + r.Name }

// Resolved carries each source's content digest, keyed by reference.
type Resolved map[SourceRef]string

// ContentDigest hashes a source's data. Both maps are folded in sorted key
// order so the digest does not depend on Go map iteration, and the two maps are
// tagged separately: a key in `data` and the same key in `binaryData` are
// different objects, and Kubernetes forbids the overlap precisely because they
// would otherwise be indistinguishable.
func ContentDigest(data map[string]string, binary map[string][]byte) string {
	h := sha256.New()
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(h, "data:%d:%s=%d:%s\n", len(k), k, len(data[k]), data[k])
	}
	bkeys := make([]string, 0, len(binary))
	for k := range binary {
		bkeys = append(bkeys, k)
	}
	sort.Strings(bkeys)
	for _, k := range bkeys {
		fmt.Fprintf(h, "binaryData:%d:%s=%d:", len(k), k, len(binary[k]))
		h.Write(binary[k])
		h.Write([]byte("\n"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// EnvSources enumerates every ConfigMap and Secret the spec's runtime reads.
//
// It is deliberately total over the union arms it understands and silent about
// the rest: fieldRef and resourceFieldRef read from the Pod, which the operator
// owns, so they are not external material. An arm this does not recognise is
// NOT skipped — it is reported through UnknownEnvArms, because a silently
// skipped source is a source whose content is ungated.
func EnvSources(spec plumev1alpha1.AgentSpec) []SourceRef {
	if spec.Runtime == nil {
		return nil
	}
	seen := map[SourceRef]bool{}
	var out []SourceRef
	add := func(kind, name string) {
		r := SourceRef{Kind: kind, Name: name}
		if !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	for _, f := range spec.Runtime.EnvFrom {
		if f.ConfigMapRef != nil {
			add("ConfigMap", f.ConfigMapRef.Name)
		}
		if f.SecretRef != nil {
			add("Secret", f.SecretRef.Name)
		}
	}
	for _, e := range spec.Runtime.Env {
		if e.ValueFrom == nil {
			continue
		}
		if e.ValueFrom.ConfigMapKeyRef != nil {
			add("ConfigMap", e.ValueFrom.ConfigMapKeyRef.Name)
		}
		if e.ValueFrom.SecretKeyRef != nil {
			add("Secret", e.ValueFrom.SecretKeyRef.Name)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

// UnknownEnvArms reports env sources this package cannot enumerate — an arm
// added by a Kubernetes release after this code was written.
//
// It exists because the alternative is silence: a source the operator cannot
// name is a source whose content cannot be hashed, and hashing "everything I
// happened to recognise" is a guarantee with a hole in it that nothing reports.
// A20's rule is that a source whose content is unknown blocks the revision, and
// an unrecognised arm is the case where the operator does not even know what to
// read.
func UnknownEnvArms(spec plumev1alpha1.AgentSpec) []string {
	if spec.Runtime == nil {
		return nil
	}
	var out []string
	for _, f := range spec.Runtime.EnvFrom {
		if f.ConfigMapRef == nil && f.SecretRef == nil {
			out = append(out, "envFrom entry with no recognised source")
		}
	}
	for _, e := range spec.Runtime.Env {
		v := e.ValueFrom
		if v == nil {
			continue
		}
		known := v.ConfigMapKeyRef != nil || v.SecretKeyRef != nil ||
			v.FieldRef != nil || v.ResourceFieldRef != nil
		if !known {
			out = append(out, "env."+e.Name+" valueFrom arm not recognised by this operator")
		}
	}
	return out
}

// MissingSources returns the references not present in resolved.
func MissingSources(spec plumev1alpha1.AgentSpec, resolved Resolved) []SourceRef {
	var missing []SourceRef
	for _, r := range EnvSources(spec) {
		if _, ok := resolved[r]; !ok {
			missing = append(missing, r)
		}
	}
	return missing
}

var _ = corev1.EnvVar{}

// MustHash is the test-only convenience for specs with no env sources. It
// panics rather than returning an error, and it panics HARDER if the spec
// actually references a source — a test that silently hashed without content
// would be asserting about an identity production can never produce.
func MustHash(spec plumev1alpha1.AgentSpec) string {
	if refs := EnvSources(spec); len(refs) > 0 {
		panic(fmt.Sprintf("MustHash on a spec referencing %v: resolve them and call Hash, "+
			"or this asserts about an identity the operator cannot mint", refs))
	}
	h, err := Hash(spec, nil)
	if err != nil {
		panic(err)
	}
	return h
}

// MustDigest is MustHash's full-width counterpart.
func MustDigest(spec plumev1alpha1.AgentSpec) string {
	if refs := EnvSources(spec); len(refs) > 0 {
		panic(fmt.Sprintf("MustDigest on a spec referencing %v", refs))
	}
	d, err := Digest(spec, nil)
	if err != nil {
		panic(err)
	}
	return d
}

// HashWith resolves every source to a FIXED digest, for tests that need a spec
// with env sources to hash deterministically without an API server. The digest
// is a constant, so it pins structure and not content — which is exactly what a
// leaf-classification test wants and exactly what a content test must not use.
func HashWith(spec plumev1alpha1.AgentSpec, digest string) string {
	h, err := HashOrRefusal(spec, digest)
	if err != nil {
		panic(err)
	}
	return h
}

// HashOrRefusal is HashWith for callers that must distinguish "this leaf mints"
// from "this whole spec is refused" — a leaf under an arm the operator cannot
// read is not classified by whether its hash moves, because there is no hash.
func HashOrRefusal(spec plumev1alpha1.AgentSpec, digest string) (string, error) {
	res := Resolved{}
	for _, r := range EnvSources(spec) {
		res[r] = digest
	}
	return Hash(spec, res)
}

// HashWithFixed is HashWith with the digest this repository's tests use, so a
// classification test can perturb env-source STRUCTURE without an API server
// while never asserting anything about content.
func HashWithFixed(spec plumev1alpha1.AgentSpec) string { return HashWith(spec, "fixed") }
