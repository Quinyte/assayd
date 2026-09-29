// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
)

// Design 02 A78: the message says whether a digest pins the image, and it says
// so from the spec. A tag reaches the operator on an install whose Agent CRD
// predates the digest rule, which envtest cannot build because it installs the
// current CRD, so this branch is pinned here.
func TestImageSignatureMessageSaysWhatPinsTheImage(t *testing.T) {
	const digest = "@sha256:6d5d9666a268df6f000000000000000000000000000000000000000000000000"
	for _, tc := range []struct {
		name      string
		spec      assaydv1alpha1.AgentSpec
		want, not []string
	}{
		{
			name: "pinned",
			spec: assaydv1alpha1.AgentSpec{Runtime: &assaydv1alpha1.AgentRuntime{Image: "ghcr.io/acme/a:v1" + digest}},
			want: []string{"is pinned by a sha256 digest", "no image signature is checked"},
			not:  []string{"not pinned"},
		},
		{
			name: "a tag",
			spec: assaydv1alpha1.AgentSpec{Runtime: &assaydv1alpha1.AgentRuntime{Image: "ghcr.io/acme/a:v1"}},
			want: []string{`"ghcr.io/acme/a:v1" is not pinned by a sha256 digest`, "no image signature is checked"},
		},
		{
			name: "a digest that is not the tail",
			spec: assaydv1alpha1.AgentSpec{Runtime: &assaydv1alpha1.AgentRuntime{Image: "ghcr.io/acme/a" + digest + "x"}},
			want: []string{"is not pinned by a sha256 digest"},
		},
		{
			name: "external",
			spec: assaydv1alpha1.AgentSpec{External: &assaydv1alpha1.ExternalAgent{Endpoint: "https://a.example.com"}},
			want: []string{"runs outside this cluster", "no image signature is checked"},
			not:  []string{"pinned by a sha256 digest"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newConditionSet(7)
			assessImageSignature(&assaydv1alpha1.Agent{Spec: tc.spec}, c)
			got, ok := c.get(assaydv1alpha1.CondImageSignatureUnverified)
			if !ok {
				t.Fatal("ImageSignatureUnverified was not asserted")
			}
			if got.Status != metav1.ConditionTrue || got.Reason != ReasonSignatureVerificationNotBuilt ||
				got.ObservedGeneration != 7 {
				t.Errorf("got %+v", got)
			}
			for _, w := range append(tc.want, "nothing clears it yet", "does not affect Ready or Degraded") {
				if !strings.Contains(got.Message, w) {
					t.Errorf("message lacks %q:\n%s", w, got.Message)
				}
			}
			for _, n := range tc.not {
				if strings.Contains(got.Message, n) {
					t.Errorf("message says %q:\n%s", n, got.Message)
				}
			}
		})
	}
}

// ImageSignatureUnverified is owned and not sticky (design 02 A78): this
// operator is its only writer, so a pass that does not assert it CLEARS it.
// That is why every exit that builds a condition set calls
// assessImageSignature, and it leaves a later change free to retract it by no
// longer asserting it.
// Asserted through merge() rather than by reading ownedTypes, for the reason
// TestPolicyCompileFailedClearsOnAPassThatDoesNotAssertIt gives. Left out of
// ownedTypes, merge() would carry it forward as another controller's; listed
// in stickyTypes, it would survive as a record of a check that never ran.
func TestImageSignatureUnverifiedClearsOnAPassThatDoesNotAssertIt(t *testing.T) {
	prior := []metav1.Condition{{
		Type: string(assaydv1alpha1.CondImageSignatureUnverified), Status: metav1.ConditionTrue,
		Reason: ReasonSignatureVerificationNotBuilt, Message: "no image signature is checked",
	}}
	for _, c := range newConditionSet(2).merge(prior) {
		if c.Type == string(assaydv1alpha1.CondImageSignatureUnverified) {
			t.Errorf("ImageSignatureUnverified survived a pass that did not assert it, as %s/%s: "+
				"it must be classified owned and not sticky", c.Status, c.Reason)
		}
	}
}
