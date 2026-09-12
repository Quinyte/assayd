// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package compiler

import (
	"regexp"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

// dns1123Label is the shape every object name here must have. A name that is
// not one is rejected by the API server at create time, which is a failure the
// operator discovers per Agent rather than in a test.
var dns1123Label = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// Design 03 §3.2's naming rule, at and past the limit.
//
// The rule matters for the same reason design 02 A42's does: it names an object
// that is a boundary. Two Agents whose emitted names collided would have one
// Agent's serving route deleted by the other's sweep — the sweep's authority is
// the NAME, so a name that is not injective hands one Agent authority over
// another's traffic.
func TestEmittedNameIsADNSLabelAndInjectiveAtTheLimit(t *testing.T) {
	short, err := EmittedName("agent", ConcernServing, "")
	if err != nil {
		t.Fatalf("short name: %v", err)
	}
	if short != "agent-serving" {
		t.Errorf("a name that fits is not hashed: got %q, want %q", short, "agent-serving")
	}
	withRev, err := EmittedName("agent", ConcernServing, "0123456789")
	if err != nil {
		t.Fatalf("name with revision: %v", err)
	}
	if withRev != "agent-serving-0123456789" {
		t.Errorf("the optional <rev> is not appended as `<name>-<concern>-<rev>`: %q", withRev)
	}

	// Two long names that share every character the truncation keeps. Under an
	// unhashed truncation these are one name.
	prefix := strings.Repeat("a", 60)
	one, err := EmittedName(prefix+"-one", ConcernServing, "")
	if err != nil {
		t.Fatalf("long name: %v", err)
	}
	two, err := EmittedName(prefix+"-two", ConcernServing, "")
	if err != nil {
		t.Fatalf("long name: %v", err)
	}
	for _, n := range []string{short, withRev, one, two} {
		if len(n) > 63 {
			t.Errorf("emitted name %q is %d characters; a DNS label is at most 63", n, len(n))
		}
		if !dns1123Label.MatchString(n) {
			t.Errorf("emitted name %q is not a DNS-1123 label", n)
		}
	}
	if one == two {
		t.Errorf("two different names truncate to one emitted name (%q). The sweep's authority is "+
			"the name, so a collision hands one Agent the power to delete another's serving route.",
			one)
	}
	// The concern must survive truncation, or two concerns of one Agent collide
	// with each other — which is the same failure one level down.
	if !strings.Contains(one, "-"+ConcernServing+"-") {
		t.Errorf("the concern did not survive truncation: %q", one)
	}

	// A concern and revision that leave no room is an ERROR, never a mangled
	// name. Design 03 §3.2: never a silent reuse.
	if _, err := EmittedName("a", strings.Repeat("c", 70), ""); err == nil {
		t.Error("a concern longer than the whole limit produced a name instead of an error")
	}
}

// `<agent>-auth` at the 63-character boundary, where §3.2's rule switches from
// the plain name to the hashed one. 58 characters of Agent name make exactly
// 63; 59 make 64 and must hash.
//
// No Agent reaches this boundary today: the CRD caps an Agent name at 52
// characters (api/v1alpha1/agent_types.go, so its workload name fits), which
// makes `<agent>-auth` at most 57. The test guards the rule against that cap
// being raised; it is not a failure that can happen now.
func TestTheAuthPolicyNameAtTheBoundary(t *testing.T) {
	at := strings.Repeat("p", 58)
	got, err := AuthPolicyName(at)
	if err != nil {
		t.Fatalf("name at the limit: %v", err)
	}
	if got != at+"-auth" || len(got) != 63 {
		t.Errorf("a 63-character `<agent>-auth` was changed: got %q (%d characters)", got, len(got))
	}

	past := strings.Repeat("p", 59)
	got, err = AuthPolicyName(past)
	if err != nil {
		t.Fatalf("name past the limit: %v", err)
	}
	if len(got) > 63 || !dns1123Label.MatchString(got) {
		t.Errorf("a 64-character `<agent>-auth` was not brought inside a DNS label: %q (%d)",
			got, len(got))
	}
	if got == past+"-auth" || !strings.Contains(got, "-"+ConcernAuth+"-") {
		t.Errorf("a 64-character `<agent>-auth` was not truncated and hashed with its concern "+
			"kept: %q", got)
	}

	// The two per-Agent names of one Agent never collide with each other, at
	// any length: the concern is kept and the hash covers it.
	for _, n := range []string{"pricer", at, past, strings.Repeat("q", 200)} {
		a, _ := AuthPolicyName(n)
		s, _ := ServingRouteName(n)
		if a == s {
			t.Errorf("Agent %q: the -auth policy and the serving route share the name %q", n, a)
		}
	}
}

// The policy targets the route the EMITTER names, at every length — including
// both sides of the serving route's own boundary, which is not the policy's:
// `-serving` is 8 characters and `-auth` is 5, so a 55-character Agent name is
// plain for both, and a 56-character one hashes the route and not the policy.
// A targetRef built by appending "-serving" by hand agrees with the emitter
// below 56 characters and names a route that does not exist above it: a policy
// on an absent target is `Accepted`, attaches to nothing, and enforces nothing.
//
// Under today's 52-character cap on Agent names, no name reaches 56, so a
// hand-built targetRef would agree with the emitter for every Agent that can
// exist. The test holds the policy to the emitter's own function so that
// raising the cap cannot open that gap.
func TestThePolicyTargetsTheRouteTheEmitterNames(t *testing.T) {
	for _, n := range []string{"pricer", strings.Repeat("r", 55), strings.Repeat("r", 56),
		strings.Repeat("r", 58), strings.Repeat("r", 59), strings.Repeat("r", 120)} {
		p, err := AuthPolicy(AuthInput{AgentName: n, AgentNamespace: "payments",
			AgentUID: types.UID("uid-1"), RunNamespace: "assayd-run-payments"})
		if err != nil {
			t.Fatalf("compile for a %d-character Agent name: %v", len(n), err)
		}
		want, err := ServingRouteName(n)
		if err != nil {
			t.Fatalf("serving route name: %v", err)
		}
		refs, _, _ := unstructured.NestedSlice(p.Object, "spec", "targetRefs")
		if len(refs) != 1 {
			t.Fatalf("a %d-character Agent name: want exactly one targetRef, got %d", len(n), len(refs))
		}
		ref, _ := refs[0].(map[string]any)
		if ref["name"] != want {
			t.Errorf("a %d-character Agent name: the policy targets %q, but the emitter names the "+
				"serving route %q", len(n), ref["name"], want)
		}
		wantName, _ := AuthPolicyName(n)
		if p.GetName() != wantName {
			t.Errorf("a %d-character Agent name: the policy is named %q, want %q",
				len(n), p.GetName(), wantName)
		}
	}
}

// TestTheTruncatedNameIsPinnedByteForByte pins the bytes a long name truncates
// to, so no refactor renames a resource the operator already emitted. The
// expected values were computed outside this package, from §3.2's rule: keep
// what fits of the name, trim a trailing '-', then append the tail and 16 hex
// of SHA-256 over the WHOLE untruncated name, tail included. Each value fails
// if the hash is shortened, taken over the name alone, or the trim is dropped.
func TestTheTruncatedNameIsPinnedByteForByte(t *testing.T) {
	for _, c := range []struct{ name, concern, want string }{
		{strings.Repeat("a", 60), ConcernServing, strings.Repeat("a", 38) + "-serving-463ff832ba9d2a0d"},
		{strings.Repeat("a", 60), ConcernAuth, strings.Repeat("a", 41) + "-auth-72772c4bc9151db2"},
		// The kept prefix ends in '-', which the rule trims before the tail.
		{strings.Repeat("x", 37) + "-" + strings.Repeat("y", 30), ConcernServing, strings.Repeat("x", 37) + "-serving-a76b2da5be413331"},
	} {
		got, err := EmittedName(c.name, c.concern, "")
		if err != nil {
			t.Fatalf("EmittedName(%q, %q): %v", c.name, c.concern, err)
		}
		if got != c.want {
			t.Errorf("EmittedName(%q, %q) = %q, want %q: an emitted name changed, which renames a resource the operator already wrote", c.name, c.concern, got, c.want)
		}
	}
}
