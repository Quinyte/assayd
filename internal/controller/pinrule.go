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

// The admission record. Q5 (a) is judged once per pin, on the pass that first
// reads it, and the judgement is kept in the stored
// GatesBypassed=True/PinnedRollback condition, whose message names the pinned
// digest in the form pinnedDigestMarker writes. While the pin names that
// digest, the record stands for "this pin was admitted", and the rule is not
// applied again. Three things read it:
//
//   - the rule itself. The not-ready branches record an admitted pin's own
//     revision as status.candidateRevision while it comes up, and §1.1 reads
//     "the current candidate" from that record, so without this the next pass
//     would refuse the pin it had admitted as "the current candidate".
//   - the rule again, once the pinned revision serves: a suite edited back to
//     one the revision failed must not turn an admitted, serving pin into a
//     refusal, whose early return would stop the Agent's whole reconcile.
//   - the eval controller (pinNamesCandidate): it sends no case to a recorded
//     candidate that an admitted pin names, and goes on sending to one a
//     refused pin names.
//
// A principal that can write agents/status can forge the record, as it can
// already forge activeRevisionDigest or a Passed verdict (§1.1).
func pinnedDigestMarker(digest string) string { return "(digest " + digest + ")" }

// pinAdmitted reports whether the stored conditions hold the admission record
// for this digest.
func pinAdmitted(conds []metav1.Condition, digest string) *metav1.Condition {
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
// whose gate is declared and whose EvalSuite CRD is installed, that does not
// name the revision serving, and that no admission record covers. It says why
// the pinned digest may not be served, or "" when it may.
//
// Two digests are refused:
//
//   - the current candidate's: status.candidateRevisionDigest, the candidate
//     §1.1 records "while it is evaluated and while it is held". It is read as
//     stored at the start of the pass, before supersession clears it, so one
//     write that edits the spec and pins the candidate is refused too.
//   - one in status.eval.failed[] paired with the current suite digest. With
//     no current suite digest — the suite does not exist or cannot be read,
//     suiteDigest is "" — only the first refusal applies, and a digest in
//     failed[] is admitted: refusing would let a deleted or unreadable suite
//     block every rollback during an incident, which is when one is needed
//     (Q5 (a), A6). Only failed[] is read, never the current verdict: the
//     eval controller writes a Failed verdict and its failed[] entry in one
//     patch, and a verdict's pair can leave failed[] only after ten later
//     failures, each of which replaces the verdict first.
func pinIneligible(pinDigest string, status *assaydv1alpha1.AgentStatus, suiteName, suiteDigest string) string {
	if pinDigest == status.CandidateRevisionDigest {
		return fmt.Sprintf("it is the current candidate: status.candidateRevisionDigest records it, and "+
			"it is evaluated or held for its eval gate on EvalSuite %q. On a gated Agent a pin does not "+
			"promote the revision the gate is evaluating (design 16 Q5 (a)), and its run goes on while this "+
			"pin stands. To serve it without a verdict, remove spec.gates and then pin it (design 16 Q9); "+
			"otherwise clear the pin and let the gate decide", suiteName)
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

// pinRule applies pinIneligible to a resolved pin that no admission record
// covers. It reads the suite to learn the current suite digest, and returns
// the refusal as an unresolvablePinError, which takes the existing
// unresolvable-pin path, or why the pin was admitted, for GatesBypassed's
// message on the admitting pass.
func (r *AgentReconciler) pinRule(ctx context.Context, agent *assaydv1alpha1.Agent,
	status *assaydv1alpha1.AgentStatus, pin *releasePin) (*unresolvablePinError, string) {
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
	if why := pinIneligible(pin.Digest, status, suiteName, suiteDigest); why != "" {
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
// pending until it is available. why is the admitting pass's reason, and
// empty on a later pass, which does not judge the pin again.
func pinnedRollbackMessage(pin *releasePin, serving bool, why string) string {
	msg := fmt.Sprintf("spec.release.targetRevisionDigest pins revision %s %s, which design 16 Q5 (a) "+
		"admitted", pin.Revision, pinnedDigestMarker(pin.Digest))
	if why != "" {
		msg += ": " + why
	} else {
		msg += " on an earlier pass, and an admitted pin is not judged again while it stands"
	}
	if serving {
		return msg + ". It serves by pin, and its eval gate was bypassed: no verdict was required for it. " +
			"GatesPassed keeps describing the revision status.eval names"
	}
	return msg + ". It will be promoted once it is available, without its eval gate, and the eval " +
		"controller sends it no case"
}

// carryPinnedRollback re-asserts a stored admission record, as stored, on a
// pass that may return before the pin step, while the gate is enforced and
// the pin still names its digest. The pin step replaces it on a pass that
// reaches it, and a refusal or an unresolvable pin withdraws it.
func carryPinnedRollback(agent *assaydv1alpha1.Agent, c *conditionSet) {
	if agent.Spec.Release == nil {
		return
	}
	if rec := pinAdmitted(agent.Status.Conditions, agent.Spec.Release.TargetRevisionDigest); rec != nil {
		c.carry(*rec)
	}
}
