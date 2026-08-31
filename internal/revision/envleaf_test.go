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
	index []int // field index chain from the root struct
	typ   reflect.Type
}

// leaves walks a struct type and returns every transitive leaf, allocating
// through pointers so a nested selector is reachable.
func leaves(t reflect.Type, prefix string, idx []int) []leaf {
	var out []leaf
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" {
			continue // unexported
		}
		ft, path, chain := f.Type, prefix+"."+f.Name, append(append([]int{}, idx...), i)
		if ft.Kind() == reflect.Ptr {
			ft = ft.Elem()
		}
		if ft.Kind() == reflect.Struct && !leafTypes[ft] {
			out = append(out, leaves(ft, path, chain)...)
			continue
		}
		out = append(out, leaf{path: path, index: chain, typ: f.Type})
	}
	return out
}

// setLeaf allocates the pointer chain to l and writes the n-th distinct value.
func setLeaf(t *testing.T, root reflect.Value, l leaf, n int) {
	t.Helper()
	v := root
	for _, i := range l.index {
		if v.Kind() == reflect.Ptr {
			if v.IsNil() {
				v.Set(reflect.New(v.Type().Elem()))
			}
			v = v.Elem()
		}
		v = v.Field(i)
	}
	ft := l.typ
	isPtr := ft.Kind() == reflect.Ptr
	if isPtr {
		ft = ft.Elem()
	}
	val := reflect.New(ft).Elem()
	switch {
	case ft == reflect.TypeOf(resource.Quantity{}):
		val.Set(reflect.ValueOf(resource.MustParse([]string{"1", "2"}[n])))
	case ft.Kind() == reflect.String:
		val.SetString([]string{"leaf-a", "leaf-b"}[n])
	case ft.Kind() == reflect.Bool:
		val.SetBool(n == 1)
	case ft.Kind() >= reflect.Int && ft.Kind() <= reflect.Int64:
		val.SetInt(int64(n + 1))
	default:
		t.Fatalf("leaf %s has kind %s with no distinct-value rule — add one rather than skipping, "+
			"or this leaf is silently unpinned", l.path, ft.Kind())
	}
	if isPtr {
		p := reflect.New(ft)
		p.Elem().Set(val)
		v.Set(p)
		return
	}
	v.Set(val)
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
