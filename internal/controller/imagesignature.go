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

// imageSignatureUnchecked is what assayd knows, and all it knows: it verifies
// no signature itself, and it does not look for a verifier someone installed
// beside it — a Sigstore policy-controller opted into by a namespace label, or
// a Kyverno verifyImages rule. So the message never says that NO signature is
// checked, only that assayd checks none and cannot tell whether anything else
// does (design 02 A78, design 07 A2's "absent or unverifiable").
const imageSignatureUnchecked = "assayd verifies no image signature, and does not detect an " +
	"admission verifier installed outside it"

// imageSignatureTail ends every message but an external Agent's.
const imageSignatureTail = "The Sigstore policy-controller binding design 07 A2 chose is not " +
	"built, so this condition is set from every exit that builds a pass's conditions and nothing " +
	"clears it yet. It is an announcement, not an incident: it changes no phase and does not " +
	"affect Ready or Degraded (design 02 A78)"

// externalTail ends an external Agent's message. Design 07 A2's binding
// covers the namespaces assayd creates workloads in, and an external Agent
// has none, so nothing designed would clear it there.
const externalTail = "Design 07 A2's verifier binding covers only the namespaces assayd creates " +
	"workloads in, and an external Agent has none, so nothing designed would clear this " +
	"condition for it. It is an announcement, not an incident: it changes no phase and does not " +
	"affect Ready or Degraded (design 02 A78)"

// assessImageSignature puts ImageSignatureUnverified on an Agent from every
// exit that builds a pass's conditions (design 02
// A78, the human's decision of 2026-09-29; design 07 A2). It is owned and not
// sticky, so every exit that builds its own condition set must call it, or
// merge() clears the announcement on that pass.
//
// Whether a digest pins the image is read from the spec, not assumed from the
// CEL rule: an install whose Agent CRD predates that rule admits a tag, because
// helm upgrade never updates crds/, and validation ratcheting keeps an Agent
// stored before the rule updatable.
func assessImageSignature(agent *assaydv1alpha1.Agent, c *conditionSet) {
	var msg string
	switch {
	case agent.Spec.Runtime != nil && digestPinned.MatchString(agent.Spec.Runtime.Image):
		msg = "spec.runtime.image is pinned by a sha256 digest, which fixes the bytes that " +
			"reference names but not who built them: " + imageSignatureUnchecked + ". " +
			imageSignatureTail
	case agent.Spec.Runtime != nil:
		msg = fmt.Sprintf("spec.runtime.image %q is not pinned by a sha256 digest, so a tag can "+
			"be repointed at other bytes; and %s. The current Agent CRD refuses an unpinned "+
			"image; an install whose CRD predates that rule, or an Agent stored before it, can "+
			"still hold one. %s", agent.Spec.Runtime.Image, imageSignatureUnchecked, imageSignatureTail)
	case agent.Spec.External != nil:
		msg = "this Agent runs outside this cluster (spec.external), so assayd runs no image for " +
			"it and pins no digest, and " + imageSignatureUnchecked + ". " + externalTail
	default:
		msg = "this Agent names no image, because neither spec.runtime nor spec.external is set; " +
			imageSignatureUnchecked + ". " + imageSignatureTail
	}
	c.set(assaydv1alpha1.CondImageSignatureUnverified, metav1.ConditionTrue,
		ReasonSignatureVerificationNotBuilt, msg)
}
