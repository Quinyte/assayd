// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/revision"
)

// Design 16 A26.1, A26.2 (b), A26.5 (a) and A28.1: the hold of a candidate
// the current spec does not mint, pinned or not, as a pure function. The
// envtest rows R1 to R9, RY and RZ, and N1 to N4, measure it through both
// controllers; this enumerates its edges.
func TestTheCandidateHoldRuleHoldsWhenTheSpecDoesNotMintTheCandidate(t *testing.T) {
	const cand, spec, other = "sha256:cand", "sha256:spec", "sha256:other"
	cases := []struct {
		name           string
		pin, spec      string
		computed, want bool
	}{
		{"no pin, the spec minting the candidate", "", cand, true, false},
		{"no pin, the spec not minting the candidate (A28.1)", "", spec, true, true},
		{"no pin, no digest (A28.1)", "", "", false, true},
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

// A26.3's sentence, word for word, with failed[] written plainly as other
// condition messages write it.
func TestThePinHoldSentenceIsA26sDecidedSentence(t *testing.T) {
	a := &assaydv1alpha1.Agent{Spec: assaydv1alpha1.AgentSpec{
		Release: &assaydv1alpha1.ReleaseSpec{TargetRevisionDigest: "sha256:spec"}}}
	st := &assaydv1alpha1.AgentStatus{ActiveRevisionDigest: "sha256:active", CandidateRevision: "c0ffee",
		CandidateRevisionDigest: "sha256:cand"}
	const want = "As of this pass, the recorded candidate, revision c0ffee (digest sha256:cand), which the " +
		"current spec does not mint, is held: the eval controller sends it no case until a change to the spec " +
		"or its env sources ends the hold, and none after that while its pair has a final verdict or is in " +
		"failed[]; clearing the pin supersedes it, unless the next pass stops first on a missing env source, " +
		"an unavailable run namespace or a revision-name collision, each of which leaves it recorded."
	if got := pinHoldSentence(a, st, gateEnforced, "sha256:spec"); got != want {
		t.Errorf("the held sentence is\n%s\nwant\n%s", got, want)
	}
}

// A26.3's one separator: a refusal text that ends in a full stop gets a
// space, and one that does not gets a full stop and a space.
func TestTheHoldSentenceIsAppendedWithOneSeparator(t *testing.T) {
	for _, c := range []struct{ msg, held, want string }{
		{"refused.", "Held.", "refused. Held."},
		{"refused", "Held.", "refused. Held."},
		{"refused", "", "refused"},
	} {
		if got := withHoldSentence(c.msg, c.held); got != c.want {
			t.Errorf("withHoldSentence(%q, %q) = %q, want %q", c.msg, c.held, got, c.want)
		}
	}
}

// failingReader fails every Get with err, as an unavailable API server does.
type failingReader struct{ err error }

func (f failingReader) Get(context.Context, client.ObjectKey, client.Object, ...client.GetOption) error {
	return f.err
}

func (f failingReader) List(context.Context, client.ObjectList, ...client.ListOption) error {
	return f.err
}

// A read error of an env source other than NotFound or Forbidden, on a pass
// with a pin set, is returned and holds the pass, so nothing is sent on it —
// beside a pin that names the recorded candidate or not.
func TestATransientEnvSourceReadErrorHoldsAPinnedPass(t *testing.T) {
	unavailable := apierrors.NewServiceUnavailable("the API server is restarting")
	for _, pin := range []string{"sha256:cand", "sha256:other"} {
		a := &assaydv1alpha1.Agent{
			ObjectMeta: metav1.ObjectMeta{Namespace: "team", Name: "a"},
			Spec: assaydv1alpha1.AgentSpec{
				Runtime: &assaydv1alpha1.AgentRuntime{Image: "ghcr.io/acme/agent@sha256:" + strings.Repeat("a", 64),
					Env: []corev1.EnvVar{{Name: "MODE", ValueFrom: &corev1.EnvVarSource{
						ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{Name: "cfg"}, Key: "MODE"}}}}},
				Release: &assaydv1alpha1.ReleaseSpec{TargetRevisionDigest: pin}},
			Status: assaydv1alpha1.AgentStatus{CandidateRevisionDigest: "sha256:cand"},
		}
		held, err := pinnedRollbackCandidate(context.Background(), failingReader{unavailable}, a)
		if !held || !apierrors.IsServiceUnavailable(err) {
			t.Errorf("pin %s: pinnedRollbackCandidate = (%v, %v), want (true, the read error)", pin, held, err)
		}
	}
}

// A28.1 with no pin set, read through pinnedRollbackCandidate: no release
// block, and a release block whose targetRevisionDigest is empty — which a
// merge patch clearing it to null leaves as release: {} — are both no pin,
// and the recorded candidate the spec does not mint is held; the candidate
// the spec mints is not.
func TestAnEmptyReleaseIsNoPinAndHoldsAStaleCandidate(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(testScheme()).Build()
	spec := assaydv1alpha1.AgentSpec{Runtime: &assaydv1alpha1.AgentRuntime{
		Image: "ghcr.io/acme/agent@sha256:" + strings.Repeat("a", 64)}}
	minted := revision.MustDigest(spec)
	for _, rel := range []*assaydv1alpha1.ReleaseSpec{nil, {}} {
		for _, c2 := range []struct {
			cand string
			want bool
		}{{"sha256:stale", true}, {minted, false}} {
			s := spec.DeepCopy()
			s.Release = rel
			a := &assaydv1alpha1.Agent{ObjectMeta: metav1.ObjectMeta{Namespace: "team", Name: "a"}, Spec: *s,
				Status: assaydv1alpha1.AgentStatus{CandidateRevisionDigest: c2.cand}}
			held, err := pinnedRollbackCandidate(context.Background(), c, a)
			if err != nil || held != c2.want {
				t.Errorf("release %+v, candidate %s: pinnedRollbackCandidate = (%v, %v), want (%v, nil)",
					rel, c2.cand, held, err, c2.want)
			}
		}
	}
}
