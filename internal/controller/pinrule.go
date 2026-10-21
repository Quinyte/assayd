// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

// pinnedDigestMarker is how GatesBypassed's message names the pinned digest.
// It is for the reader, and for one display rule (keptBypass); no decision
// reads it.
func pinnedDigestMarker(digest string) string { return "(digest " + digest + ")" }

// keptBypass is the stored GatesBypassed=True/PinnedRollback condition, when
// its message names this digest, or nil. It decides nothing: it lets a pass
// that does not judge the pin — one that returns before the pin step, or one
// whose pin names the revision serving — keep the condition while the pin
// still names the digest it describes. A pin changed to another digest, or
// removed, withdraws it.
func keptBypass(conds []metav1.Condition, digest string) *metav1.Condition {
	if digest == "" {
		return nil
	}
	c := findCondition(conds, string(assaydv1alpha1.CondGatesBypassed))
	if c == nil || c.Status != metav1.ConditionTrue || c.Reason != ReasonPinnedRollback ||
		!strings.Contains(c.Message, pinnedDigestMarker(digest)) {
		return nil
	}
	return c
}

// pinIneligible is design 16 Q5 (a), for a pin that resolved on an Agent
// whose gate is declared and whose EvalSuite CRD is installed. It says why
// the pinned digest may not be served, or "" when it may. It is derived from
// the spec and status on every pass, and reads no stored judgement, so
// nothing can be lost or forged between passes.
//
//   - A pin to the revision serving has nothing to admit or refuse.
//   - The current candidate is the revision the gate is evaluating: the one
//     status.candidateRevisionDigest records, when the current spec also
//     mints it (specDigest). A pinned revision that is not yet available is
//     recorded there by the rollout switch's not-ready branches, and is not
//     the revision the gate evaluates, so it is not refused as its own
//     candidate.
//   - A digest in status.eval.failed[] paired with the current suite digest is
//     refused. With no current suite digest — the suite does not exist or
//     cannot be read, suiteDigest is "" — only the candidate refusal applies,
//     and a digest in failed[] is admitted: refusing would let a deleted or
//     unreadable suite block every rollback during an incident, which is when
//     one is needed (Q5 (a), A6). Only failed[] is read, never the current
//     verdict: the eval controller writes a Failed verdict and its failed[]
//     entry in one patch, and a verdict's pair can leave failed[] only after
//     ten later failures, each of which replaces the verdict first. An Agent
//     CRD that prunes status.eval (EvalRecordNotKept) keeps no failed[], so
//     there a pin is never refused on it.
//
// A pin admitted and not yet serving is judged again on each pass, so a suite
// edited to one it failed refuses it before it serves.
func pinIneligible(pinDigest, specDigest string, status *assaydv1alpha1.AgentStatus,
	suiteName, suiteDigest string) string {
	if pinDigest == status.ActiveRevisionDigest {
		return ""
	}
	if pinDigest == status.CandidateRevisionDigest && specDigest == status.CandidateRevisionDigest {
		return fmt.Sprintf("it is the current candidate: the revision the current spec mints, which "+
			"status.candidateRevisionDigest records, and it is evaluated or held for its eval gate on "+
			"EvalSuite %q. On a gated Agent a pin does not promote the revision the gate is evaluating "+
			"(design 16 Q5 (a)), and its run goes on while this pin stands. To serve it without a verdict, "+
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

// pinRule applies pinIneligible to a resolved pin that does not name the
// revision serving. It reads the suite to learn the current suite digest, and returns
// the refusal as an unresolvablePinError, which takes the existing
// unresolvable-pin path, or why the pin was admitted, for GatesBypassed's
// message.
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
	if unread != "" {
		return nil, fmt.Sprintf("it is not the current candidate, and %s, so there is no current suite "+
			"digest and status.eval.failed[] was not consulted", unread)
	}
	return nil, fmt.Sprintf("it is not the current candidate, and status.eval.failed[] does not record "+
		"it against EvalSuite %q at its current digest %s", suiteName, suiteDigest)
}

// pinnedRollbackMessage is GatesBypassed's message, by state: serving, or
// pending until it is available, with why the pass admitted it.
func pinnedRollbackMessage(pin *releasePin, serving bool, why string) string {
	msg := fmt.Sprintf("spec.release.targetRevisionDigest pins revision %s %s, which design 16 Q5 (a) "+
		"admits: %s", pin.Revision, pinnedDigestMarker(pin.Digest), why)
	if serving {
		return msg + ". It serves by pin, and its eval gate was bypassed: no verdict was required for it. " +
			"GatesPassed keeps describing the revision status.eval names"
	}
	return msg + ". It will be promoted once it is available, without its eval gate, and the eval " +
		"controller sends it no case. Until then it is judged again on each pass"
}

// carryPinnedRollback keeps the stored GatesBypassed, as stored, on a pass
// that may not judge the pin, while the gate is enforced and the pin still
// names the digest it describes (keptBypass). A pass that judges the pin
// replaces it; a refusal or an unresolvable pin withdraws it.
func carryPinnedRollback(agent *assaydv1alpha1.Agent, c *conditionSet) {
	if agent.Spec.Release == nil {
		return
	}
	if kept := keptBypass(agent.Status.Conditions, agent.Spec.Release.TargetRevisionDigest); kept != nil {
		c.carry(*kept)
	}
}
