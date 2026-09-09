// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package revision

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
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
	ls := Leaves(t, reflect.TypeOf(corev1.EnvVarSource{}), "valueFrom", nil)
	if len(ls) < 8 {
		t.Fatalf("walked only %d leaves of EnvVarSource; a partial walk would pass on the "+
			"handful it happened to reach", len(ls))
	}
	for _, l := range ls {
		t.Run(l.Path, func(t *testing.T) {
			mk := func(n int) assaydv1alpha1.AgentSpec {
				var src corev1.EnvVarSource
				SetLeaf(t, reflect.ValueOf(&src).Elem(), l, n)
				s := baseSpec()
				s.Runtime.Env = []corev1.EnvVar{{Name: "X", ValueFrom: &src}}
				return s
			}
			a, aerr := HashOrRefusal(mk(0), "fixed")
			b, berr := HashOrRefusal(mk(1), "fixed")
			if aerr != nil || berr != nil {
				// An arm the operator cannot read is REFUSED, not classified: A20
				// blocks the revision rather than hashing what it happens to know.
				// Asserting "the hash moves" would be asserting about a hash that
				// does not exist.
				if aerr == nil || berr == nil {
					t.Errorf("%s: one specimen minted and the other was refused (%v / %v); the "+
						"refusal must not depend on the VALUE at a leaf", l.Path, aerr, berr)
				}
				return
			}
			if a == b {
				t.Errorf("changing ONLY %s did not mint a revision (%s == %s).\n"+
					"That leaf is a process input, so a principal can change what the agent "+
					"reads while status still names the revision an eval passed.", l.Path, a, b)
			}
		})
	}
}

func TestEveryEnvFromSourceLeafMintsARevision(t *testing.T) {
	ls := Leaves(t, reflect.TypeOf(corev1.EnvFromSource{}), "envFrom", nil)
	if len(ls) < 5 {
		t.Fatalf("walked only %d leaves of EnvFromSource", len(ls))
	}
	for _, l := range ls {
		t.Run(l.Path, func(t *testing.T) {
			mk := func(n int) assaydv1alpha1.AgentSpec {
				var src corev1.EnvFromSource
				SetLeaf(t, reflect.ValueOf(&src).Elem(), l, n)
				s := baseSpec()
				s.Runtime.EnvFrom = []corev1.EnvFromSource{src}
				return s
			}
			a, aerr := HashOrRefusal(mk(0), "fixed")
			b, berr := HashOrRefusal(mk(1), "fixed")
			if aerr != nil || berr != nil {
				if aerr == nil || berr == nil {
					t.Errorf("%s: one specimen minted and the other was refused (%v / %v)",
						l.Path, aerr, berr)
				}
				return // refused, not classified — see the EnvVarSource test above
			}
			if a == b {
				t.Errorf("changing ONLY %s did not mint a revision (%s == %s)", l.Path, a, b)
			}
		})
	}
}

// Pointer PRESENCE is a leaf too: absent and explicitly-false are different
// documents and Kubernetes treats them differently, so they must not collapse.
func TestOptionalAbsentDiffersFromExplicitFalse(t *testing.T) {
	mk := func(opt *bool) assaydv1alpha1.AgentSpec {
		s := baseSpec()
		s.Runtime.EnvFrom = []corev1.EnvFromSource{{ConfigMapRef: &corev1.ConfigMapEnvSource{
			LocalObjectReference: corev1.LocalObjectReference{Name: "prompt"}, Optional: opt}}}
		return s
	}
	f := false
	if HashWith(mk(nil), "fixed") == HashWith(mk(&f), "fixed") {
		t.Error("optional absent and optional:false hash identically; presence is part of the selector")
	}
}

// Providers are a SET: design 03 compiles them to a commutative max-price
// computation, and kustomize, helm and kubectl round-trips all re-serialize
// lists — so hashing their order would re-gate on a no-op diff. The allowlist
// was sorted and providers were not, which no test noticed.
func TestReorderingProvidersDoesNotMintARevision(t *testing.T) {
	mk := func(eps ...assaydv1alpha1.LLMEndpoint) assaydv1alpha1.AgentSpec {
		s := baseSpec()
		s.LLM = &assaydv1alpha1.LLMSpec{Providers: eps}
		return s
	}
	a := assaydv1alpha1.LLMEndpoint{Arm: assaydv1alpha1.ArmAnthropic, Model: "claude"}
	b := assaydv1alpha1.LLMEndpoint{Arm: assaydv1alpha1.ArmOpenAI, Model: "gpt-4o"}
	if HashWith(mk(a, b), "fixed") != HashWith(mk(b, a), "fixed") {
		t.Error("reordering providers minted a revision; a re-serialized manifest would pay for " +
			"an eval and canary cycle for a diff that changed nothing")
	}
	// And the set is still injective: two DIFFERENT sets must not collapse.
	c := assaydv1alpha1.LLMEndpoint{Arm: assaydv1alpha1.ArmOpenAI, Model: "gpt-4o-mini"}
	if HashWith(mk(a, b), "fixed") == HashWith(mk(a, c), "fixed") {
		t.Error("two different provider sets hash identically; sorting must canonicalise order, " +
			"not erase content")
	}
}

// Codex r7 BLOCKER 2. The surrounding LLM block is identical in every pair, so
// this cannot pass on providers changing — the vacuity that hid the omission
// behind the aggregate `LLM` mutation for two rounds.
func TestEgressAllowlistChangesMintARevision(t *testing.T) {
	arm := func(a assaydv1alpha1.LLMArm) assaydv1alpha1.LLMAllowEntry {
		return assaydv1alpha1.LLMAllowEntry{Arm: a}
	}
	mk := func(allow ...assaydv1alpha1.LLMAllowEntry) assaydv1alpha1.AgentSpec {
		s := baseSpec()
		s.LLM = &assaydv1alpha1.LLMSpec{
			Providers:       []assaydv1alpha1.LLMEndpoint{{Arm: assaydv1alpha1.ArmAnthropic, Model: "pa"}},
			EgressAllowlist: allow,
			Fallback:        &assaydv1alpha1.LLMEndpoint{Arm: assaydv1alpha1.ArmAnthropic, Model: "pa"},
		}
		return s
	}
	for _, tc := range []struct {
		name     string
		a, b     assaydv1alpha1.AgentSpec
		wantSame bool
		why      string
	}{
		{name: "add", a: mk(arm("anthropic")), b: mk(arm("anthropic"), arm("openai")),
			why: "widening egress is the ADR-0014 control; it must never reach production ungated"},
		{name: "remove", a: mk(arm("anthropic"), arm("openai")), b: mk(arm("anthropic")),
			why: "the gate is symmetric (A16): narrowing changes what the agent can reach too"},
		{name: "absent vs empty", a: mk(), b: mk([]assaydv1alpha1.LLMAllowEntry{}...),
			why: "nothing defines whether an empty allowlist means no narrowing or reach nothing, " +
				"so collapsing them would make one of those an ungated grant change"},
		{name: "reorder", a: mk(arm("anthropic"), arm("openai")), b: mk(arm("openai"), arm("anthropic")),
			wantSame: true,
			why:      "a re-serialized manifest must not re-gate; the set is sorted"},

		// The whole reason for the typed union (A53): two Azure resources share
		// the arm and differ only in the instance block. A flat string collapsed
		// them, so a HIPAA agent could be repointed at an endpoint with different
		// BAA posture with no revision minted.
		{name: "same arm, different Azure endpoint",
			a: mk(assaydv1alpha1.LLMAllowEntry{Arm: assaydv1alpha1.ArmAzureOpenAI,
				AzureOpenAI: &assaydv1alpha1.AzureOpenAIInstance{Endpoint: "a.openai.azure.com"}}),
			b: mk(assaydv1alpha1.LLMAllowEntry{Arm: assaydv1alpha1.ArmAzureOpenAI,
				AzureOpenAI: &assaydv1alpha1.AzureOpenAIInstance{Endpoint: "b.openai.azure.com"}}),
			why: "two Azure resources differ in endpoint and BAA posture; (provider, model) collapsed them"},
		{name: "same arm and endpoint, different deployment",
			a: mk(assaydv1alpha1.LLMAllowEntry{Arm: assaydv1alpha1.ArmAzureOpenAI,
				AzureOpenAI: &assaydv1alpha1.AzureOpenAIInstance{Endpoint: "a.openai.azure.com", DeploymentName: "gpt4o-prod"}}),
			b: mk(assaydv1alpha1.LLMAllowEntry{Arm: assaydv1alpha1.ArmAzureOpenAI,
				AzureOpenAI: &assaydv1alpha1.AzureOpenAIInstance{Endpoint: "a.openai.azure.com", DeploymentName: "gpt4o-dev"}}),
			why: "the deployment IS the model identity on this arm (design 03 §3.4.1.1)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			switch same := HashWith(tc.a, "fixed") == HashWith(tc.b, "fixed"); {
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
	ls := Leaves(t, reflect.TypeOf(assaydv1alpha1.AgentSpec{}), "spec", nil)
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
			mk := func(n int) assaydv1alpha1.AgentSpec {
				var s assaydv1alpha1.AgentSpec
				SetLeaf(t, reflect.ValueOf(&s).Elem(), l, n)
				return s
			}
			a, aerr := HashOrRefusal(mk(0), "fixed")
			b, berr := HashOrRefusal(mk(1), "fixed")
			if aerr != nil || berr != nil {
				if aerr == nil || berr == nil {
					t.Errorf("%s: one specimen minted and the other was refused (%v / %v); a "+
						"refusal must not depend on the VALUE at a leaf", l.Path, aerr, berr)
				}
				// A20 refuses the whole spec when an env arm cannot be read, so this
				// leaf has no hash to classify. The refusal is the behaviour and is
				// asserted by TestAFileKeyRefSpecCannotMintARevision.
				return
			}
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

// fakeFataler records a Fatalf instead of ending the test, so a test can assert
// that the walk REFUSES rather than asserting from a process that died.
type fakeFataler struct{ msg string }

func (f *fakeFataler) Helper()                        {}
func (f *fakeFataler) Fatalf(format string, a ...any) { f.msg = fmt.Sprintf(format, a...) }

// A self-recursive type must fail the walk immediately, naming the field that
// closed the cycle.
//
// Without the guard this is not a failure but a HANG: the walk re-enters the
// type forever and CI reports a timeout, which names nothing and proves
// nothing — an INVALID mutation rather than a killed one. apiextensions-apiserver
// is already a direct dependency and its JSONSchemaProps has twelve
// self-recursion points, so this is one wrong field away rather than
// hypothetical.
func TestASelfRecursiveTypeFailsTheWalkRatherThanHanging(t *testing.T) {
	type cyc struct {
		Name string
		Next *cyc
	}
	f := &fakeFataler{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		Leaves(f, reflect.TypeOf(cyc{}), "spec", nil)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the leaf walk did not terminate on a self-recursive type. It hangs, so CI reports a " +
			"timeout instead of naming the field that introduced the cycle — which is the failure mode " +
			"the guard exists to convert into a message")
	}
	if f.msg == "" {
		t.Fatal("the walk terminated without refusing a self-recursive type")
	}
	if !strings.Contains(f.msg, "self-recursive") || !strings.Contains(f.msg, "spec.Next") {
		t.Errorf("the refusal does not name the cycle and the field that closed it:\n%s", f.msg)
	}
}

// The guard must not fire on a type that merely APPEARS twice on different
// branches — AgentSpec has several — or every real walk fails.
func TestATypeReachedTwiceOnDifferentBranchesIsNotACycle(t *testing.T) {
	type inner struct{ A string }
	type outer struct {
		Left  inner
		Right inner
	}
	f := &fakeFataler{}
	ls := Leaves(f, reflect.TypeOf(outer{}), "spec", nil)
	if f.msg != "" {
		t.Fatalf("a type reached on two sibling branches was refused as a cycle:\n%s", f.msg)
	}
	if len(ls) != 2 {
		t.Errorf("want 2 leaves, got %d: %v", len(ls), ls)
	}
}
