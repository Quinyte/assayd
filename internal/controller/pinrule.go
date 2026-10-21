// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"fmt"
	"strings"

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
// the pinned digest may not be served, or "" when it may. It is derived from
// the spec and status on every pass and reads no stored judgement and no
// condition, so nothing can be lost or forged between passes.
//
//   - A pin to the revision serving has nothing to admit or refuse.
//   - The current candidate is the revision the gate is evaluating, which is
//     the one the current spec mints: specDigest, computed each pass from the
//     spec and the content of its env sources, as the rollout step computes
//     it. So a revision the current spec mints again after it served is
//     refused, however many writes brought the spec back to it (Q5 (a)'s
//     stated residue); and a pinned revision the rollout recorded in
//     status.candidateRevisionDigest while it comes up is not its own
//     candidate, because the spec mints another.
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
// edited to one it failed refuses it before it serves. Because specDigest
// follows the env sources' content, an edit to a referenced ConfigMap or
// Secret, with no write to the Agent, mints a new candidate and lifts a
// refusal of the old one, which is then a superseded candidate Q5 (a) admits.
func pinIneligible(pinDigest, specDigest string, status *assaydv1alpha1.AgentStatus,
	suiteName, suiteDigest string) string {
	if pinDigest == status.ActiveRevisionDigest {
		return ""
	}
	if pinDigest == specDigest {
		return fmt.Sprintf("it is the current candidate: the revision the current spec mints, which "+
			"the gate on EvalSuite %q evaluates before it serves. On a gated Agent a pin does not promote "+
			"the revision the gate is evaluating (design 16 Q5 (a)). While this pin stands the release, "+
			"its recorded candidate included, is left as it was, and the refusal does not stop the "+
			"recorded candidate's run. To serve it without a verdict, remove spec.gates and then pin it (design 16 Q9); "+
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

// pinRule applies pinIneligible to a resolved pin that does not name the
// revision serving; the pin step does not call it for one. It reads the suite to learn the current suite digest, and returns
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

// evalPassedFor reports whether status.eval records a Passed verdict for the
// digest.
func evalPassedFor(e *assaydv1alpha1.EvalStatus, digest string) bool {
	return e != nil && e.Verdict == assaydv1alpha1.EvalVerdictPassed && e.RevisionDigest == digest
}

// servingPinBypass is GatesBypassed's message for a pin that names the
// revision serving, derived from the spec and status alone, or "". It holds
// while the gate is enforced (the caller's to check) and status.eval records
// no Passed verdict for that revision. No stored condition is read, so a
// forged one is recomputed away, and a pass that does not enforce the gate
// costs nothing once the gate is enforced again.
//
// The message states only what the record holds (servingRecord), never a
// claim that the gate was bypassed or why it was not passed. why is the
// reason the pin was admitted, given only on the pass that admitted a pin not
// yet serving and promoted it in the same pass; every later pass, and
// assessGates, gives "", so the steady-state text is one text.
func servingPinBypass(agent *assaydv1alpha1.Agent, status *assaydv1alpha1.AgentStatus, why string) string {
	if agent.Spec.Release == nil || agent.Spec.Release.TargetRevisionDigest == "" {
		return ""
	}
	d := agent.Spec.Release.TargetRevisionDigest
	if d != status.ActiveRevisionDigest || evalPassedFor(status.Eval, d) {
		return ""
	}
	msg := fmt.Sprintf("spec.release.targetRevisionDigest pins revision %s (digest %s), the revision "+
		"serving, and it serves by pin", status.ActiveRevision, d)
	if why != "" {
		msg += "; design 16 Q5 (a) admitted the pin: " + why
	}
	return msg + ". " + servingRecord(status.Eval, d) + " While the pin stands nothing is evaluated " +
		"in its place, and GatesPassed is not asserted for it, so it keeps describing the revision " +
		"status.eval names"
}

// servingRecord is what status.eval records about digest d, which has no
// Passed verdict there, chosen from a fixed set of sentences by the record
// alone: status.eval's one pair, and the failed[] pairs for d. Only when the
// pair names another revision does it say that whether d passed or never
// ran is not recorded, because then nothing about d's own run is.
func servingRecord(e *assaydv1alpha1.EvalStatus, d string) string {
	var out string
	switch {
	case e == nil || e.RevisionDigest == "":
		out = "status.eval records no run."
	case e.RevisionDigest != d:
		out = fmt.Sprintf("status.eval records a run of another revision (digest %s), so whether this "+
			"revision passed its gate or never ran is not recorded.", e.RevisionDigest)
	case e.Verdict == assaydv1alpha1.EvalVerdictFailed:
		out = fmt.Sprintf("status.eval records a Failed verdict for it against suite digest %s.", e.SuiteDigest)
	case e.Verdict == assaydv1alpha1.EvalVerdictRunning:
		out = fmt.Sprintf("status.eval records a run of it against suite digest %s with no verdict yet.",
			e.SuiteDigest)
	default:
		out = fmt.Sprintf("status.eval names it against suite digest %s with its verdict cleared.", e.SuiteDigest)
	}
	var suites []string
	if e != nil {
		for _, p := range e.Failed {
			if p.RevisionDigest == d {
				suites = append(suites, p.SuiteDigest)
			}
		}
	}
	if len(suites) > 0 {
		out += fmt.Sprintf(" status.eval.failed[] records %d failed pair(s) for it, against suite digest(s) %s.",
			len(suites), strings.Join(suites, ", "))
	}
	return out
}

// pendingPinBypass is GatesBypassed's message for a pin admitted on this pass
// whose revision is not yet serving.
func pendingPinBypass(pin *releasePin, why string) string {
	return fmt.Sprintf("spec.release.targetRevisionDigest pins revision %s (digest %s), which design 16 "+
		"Q5 (a) admits: %s. It will be promoted once it is available, without its eval gate, and the eval "+
		"controller sends it no case. Until then it is judged again on each pass", pin.Revision, pin.Digest, why)
}
