// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"strings"
	"testing"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
)

// Design 16 A26.1, A26.2 (b) and A26.5 (a): the pinned-rollback hold as a pure
// function. The envtest rows R1 to R9, RY and RZ measure it through both
// controllers; this enumerates its edges.
func TestPinnedRollbackHoldsWhileAPinIsSetAndTheSpecDoesNotMintTheCandidate(t *testing.T) {
	const cand, spec, other = "sha256:cand", "sha256:spec", "sha256:other"
	cases := []struct {
		name           string
		pin, spec      string
		computed, want bool
	}{
		{"no pin", "", spec, true, false},
		{"no pin, no digest", "", "", false, false},
		{"a pin to the candidate, which the spec mints", cand, cand, true, false},
		{"a pin to the candidate, which the spec does not mint", cand, spec, true, true},
		{"a pin to the spec's revision, not the candidate (A22.33 (b))", spec, spec, true, true},
		{"a pin to neither, the spec not minting the candidate (A26.5 (a))", other, spec, true, true},
		{"a pin to neither, the spec minting the candidate", other, cand, true, false},
		{"a pin to the candidate, no digest (A22.7)", cand, "", false, true},
		{"a pin to another revision, no digest (A26.2 (b))", other, "", false, false},
	}
	for _, c := range cases {
		if got := pinnedRollbackHolds(c.pin, cand, c.spec, c.computed); got != c.want {
			t.Errorf("%s: pinnedRollbackHolds = %v, want %v", c.name, got, c.want)
		}
	}
}

// A26.3: the sentence is written exactly when the gate is enforced, a
// candidate is pending and the hold holds against the pass's spec digest.
func TestThePinHoldSentenceIsScopedToAnEnforcedGateAndAPendingCandidate(t *testing.T) {
	const active, cand, spec = "sha256:active", "sha256:cand", "sha256:spec"
	agent := func(pin string) *assaydv1alpha1.Agent {
		return &assaydv1alpha1.Agent{Spec: assaydv1alpha1.AgentSpec{
			Release: &assaydv1alpha1.ReleaseSpec{TargetRevisionDigest: pin}}}
	}
	pending := &assaydv1alpha1.AgentStatus{ActiveRevisionDigest: active, CandidateRevision: "c0ffee",
		CandidateRevisionDigest: cand}
	cases := []struct {
		name   string
		a      *assaydv1alpha1.Agent
		st     *assaydv1alpha1.AgentStatus
		gating gateMode
		spec   string
		want   bool
	}{
		{"enforced, pending, held", agent(spec), pending, gateEnforced, spec, true},
		{"enforced, pending, the spec mints the candidate", agent(spec), pending, gateEnforced, cand, false},
		{"enforced, no candidate", agent(spec), &assaydv1alpha1.AgentStatus{ActiveRevisionDigest: active},
			gateEnforced, spec, false},
		{"enforced, the candidate is the active revision", agent(spec), &assaydv1alpha1.AgentStatus{
			ActiveRevisionDigest: cand, CandidateRevisionDigest: cand}, gateEnforced, spec, false},
		{"no gate", agent(spec), pending, gateNone, spec, false},
		{"the EvalSuite CRD absent", agent(spec), pending, gateCRDAbsent, spec, false},
		{"gate detection unwired", agent(spec), pending, gateUnwired, spec, false},
	}
	for _, c := range cases {
		got := pinHoldSentence(c.a, c.st, c.gating, c.spec)
		if (got != "") != c.want {
			t.Errorf("%s: sentence %q, want present %v", c.name, got, c.want)
		}
		if got != "" && (!strings.Contains(got, "c0ffee") || !strings.Contains(got, cand)) {
			t.Errorf("%s: the sentence does not name the recorded candidate: %s", c.name, got)
		}
	}
}
