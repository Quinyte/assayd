package revision

import (
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"

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

func TestEveryEnvVarSourceLeafMintsARevision(t *testing.T) {
	ls := Leaves(reflect.TypeOf(corev1.EnvVarSource{}), "valueFrom", nil)
	if len(ls) < 8 {
		t.Fatalf("walked only %d leaves of EnvVarSource; a partial walk would pass on the "+
			"handful it happened to reach", len(ls))
	}
	for _, l := range ls {
		t.Run(l.Path, func(t *testing.T) {
			mk := func(n int) plumev1alpha1.AgentSpec {
				var src corev1.EnvVarSource
				SetLeaf(t, reflect.ValueOf(&src).Elem(), l, n)
				s := baseSpec()
				s.Runtime.Env = []corev1.EnvVar{{Name: "X", ValueFrom: &src}}
				return s
			}
			if a, b := Hash(mk(0)), Hash(mk(1)); a == b {
				t.Errorf("changing ONLY %s did not mint a revision (%s == %s).\n"+
					"That leaf is a process input, so a principal can change what the agent "+
					"reads while status still names the revision an eval passed.", l.Path, a, b)
			}
		})
	}
}

func TestEveryEnvFromSourceLeafMintsARevision(t *testing.T) {
	ls := Leaves(reflect.TypeOf(corev1.EnvFromSource{}), "envFrom", nil)
	if len(ls) < 5 {
		t.Fatalf("walked only %d leaves of EnvFromSource", len(ls))
	}
	for _, l := range ls {
		t.Run(l.Path, func(t *testing.T) {
			mk := func(n int) plumev1alpha1.AgentSpec {
				var src corev1.EnvFromSource
				SetLeaf(t, reflect.ValueOf(&src).Elem(), l, n)
				s := baseSpec()
				s.Runtime.EnvFrom = []corev1.EnvFromSource{src}
				return s
			}
			if a, b := Hash(mk(0)), Hash(mk(1)); a == b {
				t.Errorf("changing ONLY %s did not mint a revision (%s == %s)", l.Path, a, b)
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

func TestEveryAgentSpecLeafBehavesAsClassified(t *testing.T) {
	ls := Leaves(reflect.TypeOf(plumev1alpha1.AgentSpec{}), "spec", nil)
	if len(ls) < 45 {
		t.Fatalf("walked only %d leaves of AgentSpec; a partial walk would pass on the "+
			"handful it happened to reach", len(ls))
	}

	inventory := map[string]bool{}
	for _, l := range ls {
		inventory[l.Path] = true
		t.Run(l.Path, func(t *testing.T) {
			class, ok := specLeafClass[l.Path]
			if !ok {
				t.Fatalf("leaf %s is not classified. Every leaf is behaviour or policy — an "+
					"unclassified one reaches production through whichever the projection "+
					"happens to do, which is how a SystemPrompt field once shipped ungated.", l.Path)
			}
			mk := func(n int) plumev1alpha1.AgentSpec {
				var s plumev1alpha1.AgentSpec
				SetLeaf(t, reflect.ValueOf(&s).Elem(), l, n)
				return s
			}
			a, b := Hash(mk(0)), Hash(mk(1))
			switch {
			case class == mints && a == b:
				t.Errorf("%s is BEHAVIOUR and changing it alone did not mint a revision.\n"+
					"It reaches production through no gate at all — the projection does not "+
					"carry this leaf.", l.Path)
			case class == inPlace && a != b:
				t.Errorf("%s is POLICY and changing it alone minted a revision, so a routine "+
					"operation now pays for an eval-and-canary cycle.", l.Path)
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
