// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"fmt"
	"regexp"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
)

// ReasonSignatureVerificationNotBuilt is ImageSignatureUnverified's one reason:
// nothing in this build verifies an image signature (design 02 A78).
const ReasonSignatureVerificationNotBuilt = "SignatureVerificationNotBuilt"

// digestPinned matches an image reference that ends in a sha256 digest, which
// is what makes the kubelet pull those bytes and no others. It is the tail of
// the CRD's CEL rule on spec.runtime.image, not the whole grammar: the question
// here is only whether a digest names the bytes.
var digestPinned = regexp.MustCompile(`@sha256:[0-9a-f]{64}$`)

// imageSignatureTail is the part of every message that is true of the whole
// install, whatever the Agent's shape.
const imageSignatureTail = "Nothing in this install verifies an image signature: the Sigstore " +
	"policy-controller binding design 07 A2 chose is not built, so this condition is set on every " +
	"Agent and nothing clears it yet. It is an announcement, not an incident: it changes no phase " +
	"and does not affect Ready or Degraded (design 02 A78)"

// assessImageSignature puts ImageSignatureUnverified on every Agent (design 02
// A78, the human's decision of 2026-09-29; design 07 A2). It is owned and not
// sticky, so every exit that builds its own condition set must call it, or
// merge() clears the announcement on that pass.
//
// Whether a digest pins the image is read from the spec, not assumed from the
// CEL rule: an install whose Agent CRD predates that rule admits a tag, because
// helm upgrade never updates crds/, and validation ratcheting keeps an Agent
// stored before the rule updatable.
func assessImageSignature(agent *assaydv1alpha1.Agent, c *conditionSet) {
	var lead string
	switch {
	case agent.Spec.Runtime != nil && digestPinned.MatchString(agent.Spec.Runtime.Image):
		lead = "spec.runtime.image is pinned by a sha256 digest, so the digest says which image " +
			"runs, but not who built it: no image signature is checked. "
	case agent.Spec.Runtime != nil:
		lead = fmt.Sprintf("spec.runtime.image %q is not pinned by a sha256 digest, so a tag can "+
			"be repointed at other bytes, and no image signature is checked either. The current "+
			"Agent CRD refuses an unpinned image; an install whose CRD predates that rule, or an "+
			"Agent stored before it, can still hold one. ", agent.Spec.Runtime.Image)
	case agent.Spec.External != nil:
		lead = "this Agent runs outside this cluster (spec.external), so assayd runs no image for " +
			"it: no image digest is pinned here and no image signature is checked. "
	default:
		lead = "this Agent names no image, because neither spec.runtime nor spec.external is set, " +
			"and no image signature is checked. "
	}
	c.set(assaydv1alpha1.CondImageSignatureUnverified, metav1.ConditionTrue,
		ReasonSignatureVerificationNotBuilt, lead+imageSignatureTail)
}
