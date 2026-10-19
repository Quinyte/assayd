// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/types"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/evalsuite"
)

// The pin rule's two reasons (design 16 §1.1, Q5 (a)).
const (
	// ReasonPinnedRollback is GatesBypassed's reason for a pin the rule
	// admits on a gated Agent: the pinned revision is promoted, not
	// evaluated.
	ReasonPinnedRollback = "PinnedRollback"
	// ReasonPinnedRevisionNotEligible is Degraded's reason for a pin the rule
	// refuses, through the unresolvable-pin path: nothing is rolled back.
	ReasonPinnedRevisionNotEligible = "PinnedRevisionNotEligible"
)

// pinIneligible is design 16 Q5 (a), for a pin that resolved on an Agent
// whose gate is declared and whose EvalSuite CRD is installed. It says why
// the pinned digest may not be served, or "" when it may.
//
// Two digests are refused:
//
//   - the current candidate's. That is the revision the current spec mints,
//     specDigest, while it is not the one serving. It is read from the spec
//     and not from status.candidateRevisionDigest, because a pinned revision
//     that is not yet available is itself recorded there by the rollout
//     switch's not-ready branches, and read from status the next pass would
//     refuse the very pin it had admitted. In every other state the two
//     readings name the same revision: the switch records the spec's revision
//     as the candidate whenever it is not active, and a pin displaces it.
//   - one in status.eval.failed[] paired with the current suite digest. With
//     no current suite digest — the suite does not exist or cannot be read,
//     suiteDigest is "" — only the first refusal applies, and a digest in
//     failed[] is admitted: refusing would let a deleted or unreadable suite
//     block every rollback during an incident, which is when one is needed
//     (Q5 (a), A6).
func pinIneligible(pinDigest, specDigest string, status *assaydv1alpha1.AgentStatus,
	suiteName, suiteDigest string) string {
	if pinDigest == specDigest && specDigest != status.ActiveRevisionDigest {
		return fmt.Sprintf("it is the current candidate: the revision the current spec mints, which is "+
			"not serving and is held for its eval gate on EvalSuite %q. On a gated Agent a pin does not "+
			"promote the revision the gate is evaluating (design 16 Q5 (a)), and while this pin names "+
			"the recorded candidate the eval controller sends it no case. To serve it without a verdict, "+
			"remove spec.gates and then pin it (design 16 Q9); otherwise clear the pin and let the gate "+
			"decide", suiteName)
	}
	if suiteDigest != "" && status.Eval != nil && pairFailed(status.Eval.Failed, pinDigest, suiteDigest) {
		return fmt.Sprintf("status.eval.failed[] records it as failed against EvalSuite %q at digest "+
			"%s, which is the suite in force, and on a gated Agent a pin does not serve a revision that "+
			"fails the suite its owner declared (design 16 Q5 (a)). Pin another retained revision; or "+
			"edit the suite, which gives a new suite digest this record does not hold; or remove "+
			"spec.gates and then pin it (design 16 Q9)", suiteName, suiteDigest)
	}
	return ""
}

// pinRule applies pinIneligible to a resolved pin on a gated Agent. It reads
// the suite to learn the current suite digest, and returns the refusal as an
// unresolvablePinError, which takes the existing unresolvable-pin path, or
// GatesBypassed's message for an admitted pin.
func (r *AgentReconciler) pinRule(ctx context.Context, agent *assaydv1alpha1.Agent,
	status *assaydv1alpha1.AgentStatus, pin *releasePin, specDigest string) (*unresolvablePinError, string) {
	suiteName := agent.Spec.Gates[0].EvalSuiteRef
	// Any failure to learn the digest — NotFound, another read error, or a
	// suite that cannot be digested — leaves no current suite digest, which
	// Q5 (a) treats alike.
	suiteDigest, unread := "", ""
	var s assaydv1alpha1.EvalSuite
	err := r.reader().Get(ctx, types.NamespacedName{Namespace: agent.Namespace, Name: suiteName}, &s)
	if err == nil {
		suiteDigest, err = evalsuite.Digest(s.Spec)
	}
	if err != nil {
		suiteDigest, unread = "", fmt.Sprintf("EvalSuite %q could not be read (%v)", suiteName, err)
	}
	if why := pinIneligible(pin.Digest, specDigest, status, suiteName, suiteDigest); why != "" {
		return &unresolvablePinError{digest: pin.Digest, reason: why, ineligible: true}, ""
	}
	msg := fmt.Sprintf("spec.release.targetRevisionDigest pins revision %s (digest %s), and design 16 "+
		"Q5 (a) admits it: it is not the current candidate", pin.Revision, pin.Digest)
	if unread != "" {
		msg += fmt.Sprintf(", and %s, so there is no current suite digest and status.eval.failed[] "+
			"is not consulted", unread)
	} else {
		msg += fmt.Sprintf(", and status.eval.failed[] does not record it against EvalSuite %q at its "+
			"current digest %s", suiteName, suiteDigest)
	}
	return nil, msg + ". It serves without its eval gate: it is promoted once available, the eval " +
		"controller sends it no case, and GatesPassed keeps describing the revision status.eval names"
}
