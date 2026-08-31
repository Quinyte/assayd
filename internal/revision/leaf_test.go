package revision

import (
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	plumev1alpha1 "github.com/Quinyte/plume/api/v1alpha1"
)

// Codex r7 BLOCKER 3: every explicitly named arm of the env selectors dropped
// leaves. SecretKeyRef.Optional, ConfigMapKeyRef.Optional, FieldRef.APIVersion
// and ResourceFieldRef.Divisor were all absent from the projection, so flipping
// `optional: false` to `true` on a referenced prompt hashed identically. The
// reproduction was two lines.
//
// A per-ARM test would not have caught it — the arms were all named. Only a
// per-LEAF test does, so these walk the upstream types and assert that changing
// each transitive leaf, alone, mints a revision.
//
// Reflecting over an UPSTREAM type is safe in the way reflecting over our own
// registry is not: k8s.io/api decides this shape, so the enumeration cannot
// delete its own cases the way a registry-derived test does (design 03 A23).
// A leaf added by a dependency bump gets a case the day it appears.

// leafTypes are structs treated as single values rather than descended into,
// because their identity is their whole content and they carry custom JSON.
var leafTypes = map[reflect.Type]bool{
	reflect.TypeOf(resource.Quantity{}): true,
}

type leaf struct {
	path  string
	chain []int
	typ   reflect.Type // the leaf field's own type, pointer and slice included
}

// leaves walks a struct type and returns every transitive leaf. A slice is
// descended into as one element; a slice of non-structs is itself a leaf.
func leaves(t reflect.Type, prefix string, chain []int) []leaf {
	var out []leaf
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" {
			continue // unexported
		}
		path, next := prefix+"."+f.Name, append(append([]int{}, chain...), i)
		ft := f.Type
		for ft.Kind() == reflect.Ptr || ft.Kind() == reflect.Slice {
			if ft.Kind() == reflect.Slice {
				path += "[]"
			}
			ft = ft.Elem()
		}
		if ft.Kind() == reflect.Struct && !leafTypes[ft] && ft.PkgPath() != "time" && ft.NumField() > 0 {
			out = append(out, leaves(ft, path, next)...)
			continue
		}
		out = append(out, leaf{path: path, chain: next, typ: f.Type})
	}
	return out
}

// distinct writes the n-th of two different values of type ft into val.
func distinct(t *testing.T, val reflect.Value, ft reflect.Type, path string, n int) {
	t.Helper()
	switch {
	case ft == reflect.TypeOf(resource.Quantity{}):
		val.Set(reflect.ValueOf(resource.MustParse([]string{"1", "2"}[n])))
	case ft.Kind() == reflect.Map:
		m := reflect.MakeMap(ft)
		k := reflect.New(ft.Key()).Elem()
		k.SetString([]string{"cpu", "memory"}[n])
		ev := reflect.New(ft.Elem()).Elem()
		distinct(t, ev, ft.Elem(), path, n)
		m.SetMapIndex(k, ev)
		val.Set(m)
	case ft.Kind() == reflect.Slice:
		sl := reflect.MakeSlice(ft, 1, 1)
		distinct(t, sl.Index(0), ft.Elem(), path, n)
		val.Set(sl)
	case ft.Kind() == reflect.String:
		val.SetString([]string{"leaf-a", "leaf-b"}[n])
	case ft.Kind() == reflect.Bool:
		val.SetBool(n == 1)
	case ft.Kind() >= reflect.Int && ft.Kind() <= reflect.Int64:
		val.SetInt(int64(n + 1))
	default:
		t.Fatalf("leaf %s has kind %s with no distinct-value rule — add one rather than "+
			"skipping, or this leaf is silently unpinned", path, ft.Kind())
	}
}

// setLeaf allocates the pointer and slice chain down to l and writes value n.
// Both sides of a comparison run this, so the ENCLOSING objects are identical
// and only the leaf differs — without that, a test passes on a block appearing.
func setLeaf(t *testing.T, root reflect.Value, l leaf, n int) {
	t.Helper()
	v := root
	for si, i := range l.chain {
		for v.Kind() == reflect.Ptr {
			if v.IsNil() {
				v.Set(reflect.New(v.Type().Elem()))
			}
			v = v.Elem()
		}
		v = v.Field(i)
		last := si == len(l.chain)-1
		for !last && (v.Kind() == reflect.Ptr || v.Kind() == reflect.Slice) {
			if v.Kind() == reflect.Ptr {
				if v.IsNil() {
					v.Set(reflect.New(v.Type().Elem()))
				}
				v = v.Elem()
				continue
			}
			if v.Len() == 0 {
				v.Set(reflect.MakeSlice(v.Type(), 1, 1))
			}
			v = v.Index(0)
		}
	}
	// At the leaf: unwrap pointers, then write.
	ft := l.typ
	for ft.Kind() == reflect.Ptr {
		if v.IsNil() {
			v.Set(reflect.New(ft.Elem()))
		}
		v, ft = v.Elem(), ft.Elem()
	}
	distinct(t, v, ft, l.path, n)
}

func TestEveryEnvVarSourceLeafMintsARevision(t *testing.T) {
	ls := leaves(reflect.TypeOf(corev1.EnvVarSource{}), "valueFrom", nil)
	if len(ls) < 8 {
		t.Fatalf("walked only %d leaves of EnvVarSource; a partial walk would pass on the "+
			"handful it happened to reach", len(ls))
	}
	for _, l := range ls {
		t.Run(l.path, func(t *testing.T) {
			mk := func(n int) plumev1alpha1.AgentSpec {
				var src corev1.EnvVarSource
				setLeaf(t, reflect.ValueOf(&src).Elem(), l, n)
				s := baseSpec()
				s.Runtime.Env = []corev1.EnvVar{{Name: "X", ValueFrom: &src}}
				return s
			}
			if a, b := Hash(mk(0)), Hash(mk(1)); a == b {
				t.Errorf("changing ONLY %s did not mint a revision (%s == %s).\n"+
					"That leaf is a process input, so a principal can change what the agent "+
					"reads while status still names the revision an eval passed.", l.path, a, b)
			}
		})
	}
}

func TestEveryEnvFromSourceLeafMintsARevision(t *testing.T) {
	ls := leaves(reflect.TypeOf(corev1.EnvFromSource{}), "envFrom", nil)
	if len(ls) < 5 {
		t.Fatalf("walked only %d leaves of EnvFromSource", len(ls))
	}
	for _, l := range ls {
		t.Run(l.path, func(t *testing.T) {
			mk := func(n int) plumev1alpha1.AgentSpec {
				var src corev1.EnvFromSource
				setLeaf(t, reflect.ValueOf(&src).Elem(), l, n)
				s := baseSpec()
				s.Runtime.EnvFrom = []corev1.EnvFromSource{src}
				return s
			}
			if a, b := Hash(mk(0)), Hash(mk(1)); a == b {
				t.Errorf("changing ONLY %s did not mint a revision (%s == %s)", l.path, a, b)
			}
		})
	}
}

// Pointer PRESENCE is a leaf too: absent and explicitly-false are different
// documents and Kubernetes treats them differently, so they must not collapse.
func TestOptionalAbsentDiffersFromExplicitFalse(t *testing.T) {
	mk := func(opt *bool) plumev1alpha1.AgentSpec {
		s := baseSpec()
		s.Runtime.EnvFrom = []corev1.EnvFromSource{{ConfigMapRef: &corev1.ConfigMapEnvSource{
			LocalObjectReference: corev1.LocalObjectReference{Name: "prompt"}, Optional: opt}}}
		return s
	}
	f := false
	if Hash(mk(nil)) == Hash(mk(&f)) {
		t.Error("optional absent and optional:false hash identically; presence is part of the selector")
	}
}

// Codex r7 BLOCKER 2. The surrounding LLM block is identical in every pair, so
// this cannot pass on providers changing — the vacuity that hid the omission
// behind the aggregate `LLM` mutation for two rounds.
func TestEgressAllowlistChangesMintARevision(t *testing.T) {
	mk := func(allow ...string) plumev1alpha1.AgentSpec {
		s := baseSpec()
		s.LLM = &plumev1alpha1.LLMSpec{
			Providers:       []string{"internal/pa"},
			EgressAllowlist: allow,
			Fallback:        &plumev1alpha1.ModelRef{Provider: "internal", Model: "pa"},
		}
		return s
	}
	for _, tc := range []struct {
		name     string
		a, b     plumev1alpha1.AgentSpec
		wantSame bool
		why      string
	}{
		{name: "add", a: mk("internal/*"), b: mk("internal/*", "openai/*"),
			why: "widening egress is the ADR-0014 control; it must never reach production ungated"},
		{name: "remove", a: mk("internal/*", "openai/*"), b: mk("internal/*"),
			why: "the gate is symmetric (A16): narrowing changes what the agent can reach too"},
		{name: "absent vs empty", a: mk(), b: mk([]string{}...),
			why: "nothing defines whether an empty allowlist means no narrowing or reach nothing, " +
				"so collapsing them would make one of those an ungated grant change"},
		{name: "reorder", a: mk("a/*", "b/*"), b: mk("b/*", "a/*"), wantSame: true,
			why: "a re-serialized manifest must not re-gate; the set is sorted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			switch same := Hash(tc.a) == Hash(tc.b); {
			case tc.wantSame && !same:
				t.Errorf("%s minted a revision and must not — %s", tc.name, tc.why)
			case !tc.wantSame && same:
				t.Errorf("%s did not mint a revision — %s", tc.name, tc.why)
			}
		})
	}
}

// Codex r7 MAJOR 3. The projection tests proved AGGREGATES, not leaves: the
// budget case changed only tokensPerDay, the expose case only visibility, and
// the classification harness mutated whole blocks. Four valid, compiling
// production mutations survived the entire suite — deleting USDPerDay,
// TaskTimeout, MaxHops and expose.Auth from project() — two of which this
// session had itself introduced two commits earlier while adding aggregate
// tests for them.
//
// specLeafClass is the independent inventory. It is written from design 02
// §3.3's A12 table, not derived from project(), so deleting a line from the
// projection cannot delete its own check.
type leafClass int

const (
	mints   leafClass = iota // behaviour surface: changing it MUST mint a revision
	inPlace                  // policy surface: changing it must NOT
)

var specLeafClass = map[string]leafClass{
	// Behaviour: what the agent can do, produce, or reach.
	"spec.Runtime.Image":                                                     mints,
	"spec.Runtime.Sandbox.Profile":                                           mints,
	"spec.Runtime.Env[].Name":                                                mints,
	"spec.Runtime.Env[].Value":                                               mints,
	"spec.Runtime.Env[].ValueFrom.FieldRef.APIVersion":                       mints,
	"spec.Runtime.Env[].ValueFrom.FieldRef.FieldPath":                        mints,
	"spec.Runtime.Env[].ValueFrom.ResourceFieldRef.ContainerName":            mints,
	"spec.Runtime.Env[].ValueFrom.ResourceFieldRef.Resource":                 mints,
	"spec.Runtime.Env[].ValueFrom.ResourceFieldRef.Divisor":                  mints,
	"spec.Runtime.Env[].ValueFrom.ConfigMapKeyRef.LocalObjectReference.Name": mints,
	"spec.Runtime.Env[].ValueFrom.ConfigMapKeyRef.Key":                       mints,
	"spec.Runtime.Env[].ValueFrom.ConfigMapKeyRef.Optional":                  mints,
	"spec.Runtime.Env[].ValueFrom.SecretKeyRef.LocalObjectReference.Name":    mints,
	"spec.Runtime.Env[].ValueFrom.SecretKeyRef.Key":                          mints,
	"spec.Runtime.Env[].ValueFrom.SecretKeyRef.Optional":                     mints,
	"spec.Runtime.Env[].ValueFrom.FileKeyRef.VolumeName":                     mints,
	"spec.Runtime.Env[].ValueFrom.FileKeyRef.Path":                           mints,
	"spec.Runtime.Env[].ValueFrom.FileKeyRef.Key":                            mints,
	"spec.Runtime.Env[].ValueFrom.FileKeyRef.Optional":                       mints,
	"spec.Runtime.EnvFrom[].Prefix":                                          mints,
	"spec.Runtime.EnvFrom[].ConfigMapRef.LocalObjectReference.Name":          mints,
	"spec.Runtime.EnvFrom[].ConfigMapRef.Optional":                           mints,
	"spec.Runtime.EnvFrom[].SecretRef.LocalObjectReference.Name":             mints,
	"spec.Runtime.EnvFrom[].SecretRef.Optional":                              mints,
	"spec.External.Endpoint":                                                 mints,
	"spec.External.OAuthClientRef":                                           mints,
	"spec.Knowledge[].Name":                                                  mints,
	"spec.Knowledge[].Version":                                               mints,
	"spec.Knowledge[].Scope.EntityTypes[]":                                   mints,
	"spec.Tools[].Name":                                                      mints,
	"spec.Tools[].RequiresApproval":                                          mints, // A30
	"spec.LLM.Providers[]":                                                   mints,
	"spec.LLM.EgressAllowlist[]":                                             mints, // A16, and r7 BLOCKER 2
	"spec.LLM.Fallback.Provider":                                             mints,
	"spec.LLM.Fallback.Model":                                                mints,
	"spec.Budget.TokensPerDay":                                               mints, // A25
	"spec.Budget.USDPerDay":                                                  mints, // A25 — survived until r7
	"spec.Budget.TaskTimeout.Duration":                                       mints, // A25 — survived until r7
	"spec.Budget.MaxHops":                                                    mints, // A25 — survived until r7
	"spec.Expose.A2A.Visibility":                                             mints, // A25
	"spec.Expose.A2A.Auth":                                                   mints, // A25 — survived until r7

	// Policy: how much, how fast, who may call, or pure wiring.
	"spec.Runtime.Replicas":                   inPlace,
	"spec.Runtime.Port":                       inPlace,
	"spec.Runtime.Resources.Limits":           inPlace,
	"spec.Runtime.Resources.Requests":         inPlace,
	"spec.Runtime.Resources.Claims[].Name":    inPlace,
	"spec.Runtime.Resources.Claims[].Request": inPlace,
	"spec.External.InlineCard":                inPlace,
	"spec.Card.Path":                          inPlace,
	"spec.Loop.AllowReentry":                  inPlace,
	"spec.Loop.MaxVisits":                     inPlace,
	"spec.Gates[].EvalSuiteRef":               inPlace,
}

func TestEveryAgentSpecLeafBehavesAsClassified(t *testing.T) {
	ls := leaves(reflect.TypeOf(plumev1alpha1.AgentSpec{}), "spec", nil)
	if len(ls) < 45 {
		t.Fatalf("walked only %d leaves of AgentSpec; a partial walk would pass on the "+
			"handful it happened to reach", len(ls))
	}

	inventory := map[string]bool{}
	for _, l := range ls {
		inventory[l.path] = true
		t.Run(l.path, func(t *testing.T) {
			class, ok := specLeafClass[l.path]
			if !ok {
				t.Fatalf("leaf %s is not classified. Every leaf is behaviour or policy — an "+
					"unclassified one reaches production through whichever the projection "+
					"happens to do, which is how a SystemPrompt field once shipped ungated.", l.path)
			}
			mk := func(n int) plumev1alpha1.AgentSpec {
				var s plumev1alpha1.AgentSpec
				setLeaf(t, reflect.ValueOf(&s).Elem(), l, n)
				return s
			}
			a, b := Hash(mk(0)), Hash(mk(1))
			switch {
			case class == mints && a == b:
				t.Errorf("%s is BEHAVIOUR and changing it alone did not mint a revision.\n"+
					"It reaches production through no gate at all — the projection does not "+
					"carry this leaf.", l.path)
			case class == inPlace && a != b:
				t.Errorf("%s is POLICY and changing it alone minted a revision, so a routine "+
					"operation now pays for an eval-and-canary cycle.", l.path)
			}
		})
	}

	// The inventory must not outlive the type either: a classified path that no
	// longer exists is a rule guarding nothing, and hides that its field moved.
	for path := range specLeafClass {
		if !inventory[path] {
			t.Errorf("specLeafClass classifies %q, which is not a leaf of AgentSpec any more. "+
				"If the field moved, move its classification; do not leave a rule pointing at "+
				"nothing.", path)
		}
	}
}
