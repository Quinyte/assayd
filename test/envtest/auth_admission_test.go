// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package envtest

import (
	"context"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/types"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
)

// Design 03 §3.4.4 and §3.1, owed to design 02 and approved with the first
// slice (ADR-0034 C2 and B2). Both are admission rules, so this is the layer
// where they become true: a marker in agent_types.go is only a claim until a
// real API server refuses the object.
//
// The messages are LITERALS, not shared constants. They are user-facing text
// the design writes out exactly, and a constant compared with itself would let
// a rewording pass at every layer at once.
const (
	authRequiredMessage = "spec.expose.a2a.auth is required and has no default: set apikey " +
		"(keys in the group named for this namespace), none (an unauthenticated route), or " +
		"oauth (not compilable until design 06 ships)"
	budgetRefusedMessage = "spec.budget is not enforced yet: no gateway rate limit and no spend " +
		"backstop exist (ADR-0030 step 1). Remove spec.budget; it is accepted again when design " +
		"03's -ratelimit and design 04's spend aggregation ship."
)

const admissionImage = "ghcr.io/acme/a@sha256:3bda1c750240ee09000000000000000000000000000000000000000000000000"

func admissionAgent(name, field, value string) string {
	doc := "apiVersion: assayd.dev/v1alpha1\nkind: Agent\nmetadata: {name: " + name + "}\n" +
		"spec:\n  runtime: {image: " + admissionImage + "}\n"
	if field != "" {
		doc += "  " + field + ": " + value + "\n"
	}
	return doc
}

// An expose.a2a block states its authentication, because a default here would
// be a security decision nobody made (§3.4.4). The refusal cases are the ones
// that fail if +kubebuilder:default=oauth comes back: the API server defaults
// before it validates, so has(self.auth) would then always hold.
func TestAnExposedAgentMustStateItsAuthentication(t *testing.T) {
	for _, tc := range []struct{ name, expose string }{
		{"a2a without auth", "{a2a: {visibility: org}}"},
		{"an empty a2a block", "{a2a: {}}"},
	} {
		t.Run("refused/"+tc.name, func(t *testing.T) {
			_, err := applyYAML(t, newNamespace(t), admissionAgent("no-auth", "expose", tc.expose))
			if err == nil {
				t.Fatal("an expose.a2a block with no auth was admitted. Either a default filled it " +
					"in, which is the security decision §3.4.4 refuses to make for anyone, or the " +
					"rule is gone.")
			}
			if !strings.Contains(err.Error(), authRequiredMessage) {
				t.Errorf("refused, but not with the message that names the fix.\n got: %v\nwant: %q",
					err, authRequiredMessage)
			}
		})
	}

	// A value outside the enum is refused by the enum, not by the CEL rule, so
	// all this pins is that the set is closed.
	t.Run("refused/an unknown mode", func(t *testing.T) {
		if _, err := applyYAML(t, newNamespace(t), admissionAgent("jwt", "expose", "{a2a: {auth: jwt}}")); err == nil {
			t.Fatal("auth: jwt was admitted; the slice's modes are apikey, none and oauth")
		}
	})

	for _, mode := range []string{"apikey", "none", "oauth"} {
		t.Run("admitted/auth "+mode, func(t *testing.T) {
			ns := newNamespace(t)
			a, err := applyYAML(t, ns, admissionAgent("auth-"+mode, "expose", "{a2a: {auth: "+mode+"}}"))
			if err != nil {
				t.Fatalf("auth: %s must be admitted: %v", mode, err)
			}
			var got assaydv1alpha1.Agent
			if err := k8s.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: a.Name}, &got); err != nil {
				t.Fatalf("read back: %v", err)
			}
			if got.Spec.Expose == nil || got.Spec.Expose.A2A == nil || got.Spec.Expose.A2A.Auth != mode {
				t.Errorf("stored expose is %+v, want auth %q exactly as written", got.Spec.Expose, mode)
			}
		})
	}

	// No expose block at all is still admitted, and nothing is defaulted into
	// existence: the operator derives such an Agent's -auth from its namespace
	// (§3.4.4), which is a compile-time rule, not a stored value. An expose
	// block that names no protocol writes no a2a block, so it is admitted too.
	for _, tc := range []struct{ name, field, value string }{
		{"no expose block", "", ""},
		{"an expose block with no a2a", "expose", "{}"},
	} {
		t.Run("admitted/"+tc.name, func(t *testing.T) {
			ns := newNamespace(t)
			a, err := applyYAML(t, ns, admissionAgent("unexposed", tc.field, tc.value))
			if err != nil {
				t.Fatalf("must be admitted: %v", err)
			}
			var got assaydv1alpha1.Agent
			if err := k8s.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: a.Name}, &got); err != nil {
				t.Fatalf("read back: %v", err)
			}
			if got.Spec.Expose != nil && got.Spec.Expose.A2A != nil {
				t.Errorf("an a2a block was defaulted into existence: %+v", got.Spec.Expose.A2A)
			}
		})
	}
}

// A budget written today limits nothing, at the gateway or anywhere else, so it
// is refused where the developer writes it (§3.1, ADR-0034 B2). The empty block
// is a case of its own: has() is true of `budget: {}`, and a rule keyed on a
// populated field would let it through.
func TestABudgetIsRefusedWhileNothingEnforcesIt(t *testing.T) {
	for _, tc := range []struct{ name, budget string }{
		{"a token budget", "{tokensPerDay: 2000000}"},
		{"a usd budget", `{usdPerDay: "40"}`},
		{"an empty budget block", "{}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := applyYAML(t, newNamespace(t), admissionAgent("budgeted", "budget", tc.budget))
			if err == nil {
				t.Fatal("spec.budget was admitted. Nothing enforces it — no -ratelimit is " +
					"compiled and design 04's spend aggregation does not exist — so an admitted " +
					"budget is a limit the developer believes in and nothing applies.")
			}
			if !strings.Contains(err.Error(), budgetRefusedMessage) {
				t.Errorf("refused, but not with the message that names the fix.\n got: %v\nwant: %q",
					err, budgetRefusedMessage)
			}
		})
	}
}
