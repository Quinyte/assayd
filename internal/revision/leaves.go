// Package-level leaf machinery, in a non-test file because TWO test packages
// need it: internal/revision compares hashes, and test/envtest validates that
// each specimen is actually admissible before believing the comparison.
//
// Codex r8 MAJOR 2: every specimen used to start from a zero AgentSpec, so many
// violated exactly-one-of runtime/external, an enum, or a pattern. The test then
// proved that serialization contains a Go field — not that a reachable API
// transition is classified correctly. A leaf that admission rejects, or that
// defaulting normalises, cannot be gated or wasted in production whatever the
// hash does.
package revision

import (
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/api/resource"
)

// LeafTypes are structs treated as single values rather than descended into,
// because their identity is their whole content and they carry custom JSON.
var LeafTypes = map[reflect.Type]bool{
	reflect.TypeOf(resource.Quantity{}): true,
}

type Leaf struct {
	Path  string
	Chain []int
	Typ   reflect.Type // the leaf field's own type, pointer and slice included
}

// leaves walks a struct type and returns every transitive leaf. A slice is
// descended into as one element; a slice of non-structs is itself a leaf.
func Leaves(t reflect.Type, prefix string, chain []int) []Leaf {
	var out []Leaf
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
		if ft.Kind() == reflect.Struct && !LeafTypes[ft] && ft.PkgPath() != "time" && ft.NumField() > 0 {
			out = append(out, Leaves(ft, path, next)...)
			continue
		}
		out = append(out, Leaf{Path: path, Chain: next, Typ: f.Type})
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
func SetLeaf(t *testing.T, root reflect.Value, l Leaf, n int) {
	t.Helper()
	v := root
	for si, i := range l.Chain {
		for v.Kind() == reflect.Ptr {
			if v.IsNil() {
				v.Set(reflect.New(v.Type().Elem()))
			}
			v = v.Elem()
		}
		v = v.Field(i)
		last := si == len(l.Chain)-1
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
	ft := l.Typ
	for ft.Kind() == reflect.Ptr {
		if v.IsNil() {
			v.Set(reflect.New(ft.Elem()))
		}
		v, ft = v.Elem(), ft.Elem()
	}
	distinct(t, v, ft, l.Path, n)
}

// leafClass and the classification map live here, not in a _test.go file,
// because two packages must agree on them: the unit test compares hashes and
// the envtest proves the same classification against the real schema. A second
// copy is a second place to be wrong, which is the defect this whole area keeps
// producing.
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

// ClassifiedAsBehaviour reports whether a leaf is on the behaviour surface.
// Unknown paths report false and the unit test fails them separately, so a new
// field cannot slip through as "not behaviour" here.
func ClassifiedAsBehaviour(path string) bool {
	return specLeafClass[path] == mints && Classified(path)
}

// Classified reports whether a leaf has been classified at all.
func Classified(path string) bool {
	_, ok := specLeafClass[path]
	return ok
}

// SetLeafString writes an explicit value at a leaf, for leaves whose schema
// constrains the value — an enum, a pattern, a digest. The generic distinct()
// writes "leaf-a"/"leaf-b", which are distinct and INADMISSIBLE, so a test that
// only uses it can never prove anything about a reachable API transition.
func SetLeafString(t *testing.T, root reflect.Value, l Leaf, v string) {
	t.Helper()
	SetLeaf(t, root, l, 0) // allocate the pointer/slice chain
	cur := root
	for si, i := range l.Chain {
		for cur.Kind() == reflect.Ptr {
			cur = cur.Elem()
		}
		if cur.Kind() == reflect.Slice {
			cur = cur.Index(0)
		}
		cur = cur.Field(i)
		last := si == len(l.Chain)-1
		for !last && (cur.Kind() == reflect.Ptr || cur.Kind() == reflect.Slice) {
			if cur.Kind() == reflect.Slice {
				cur = cur.Index(0)
			} else {
				cur = cur.Elem()
			}
		}
	}
	for cur.Kind() == reflect.Ptr {
		cur = cur.Elem()
	}
	if cur.Kind() != reflect.String {
		t.Fatalf("SetLeafString on %s, which is a %s", l.Path, cur.Kind())
	}
	cur.SetString(v)
}
