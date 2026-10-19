// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/log"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/compiler"
	"github.com/Quinyte/assayd/internal/evalsuite"
)

// The GatesPassed reasons the Agent reconciler's gate branch sets (design 16
// §1.1, "Status, conditions and phase"). The pin rule's reasons are build
// step 8's.
const (
	// ReasonEvalRunning: a gated candidate with its suite and its card, no
	// final verdict, and nothing recorded that stops the eval controller
	// claiming its next case. It replaced GateControllerUnimplemented, which
	// held every such candidate while no eval controller existed.
	ReasonEvalRunning = "EvalRunning"
	// ReasonEvalPaused: the same, with the candidate not available, so the
	// eval controller sends it nothing until it is again (step 8).
	ReasonEvalPaused                 = "EvalPaused"
	ReasonEvalRecordNotKept          = "EvalRecordNotKept"
	ReasonEvalRecordUnverifiable     = "EvalRecordUnverifiable"
	ReasonEvalSuiteNotFound          = "EvalSuiteNotFound"
	ReasonEvalSuiteUnreadable        = "EvalSuiteUnreadable"
	ReasonEvalAwaitingCard           = "EvalAwaitingCard"
	ReasonEvalBindingUnsupported     = "EvalBindingUnsupported"
	ReasonEvalWaitingForAuth         = "EvalWaitingForAuth"
	ReasonEvalPairAlreadyFailed      = "EvalPairAlreadyFailed"
	ReasonEvalFailed                 = "EvalFailed"
	ReasonEvalPassed                 = "EvalPassed"
	ReasonEvalHeldForAuth            = "EvalHeldForAuth"
	ReasonActiveRevisionNotEvaluated = "ActiveRevisionNotEvaluated"
	reasonAwaitingGates              = "AwaitingGates"

	// agentCRDName is the CRD the schema check reads.
	agentCRDName = "agents.assayd.dev"
	// DefaultEvalRecordCheckTTL is how long the schema check's answer is
	// reused. The Agent CRD is about 74 KB, so it is read once per TTL rather
	// than once per pass, and no CRD informer is started (design 16 §1.1
	// step 11). The same value as the EvalSuite detector's production TTL.
	DefaultEvalRecordCheckTTL = 30 * time.Second
)

// gateMode is what a pass does with an Agent's spec.gates, decided once per
// pass so that assessGates and the rollout switch cannot read the EvalSuite
// detector's cache on two sides of its expiry.
type gateMode int

const (
	// gateNone: no gate declared.
	gateNone gateMode = iota
	// gateUnwired: gates declared, and the reconciler was built with no
	// EvalSuite detection. It holds, under assessGates' GateDetectionUnwired.
	gateUnwired
	// gateCRDAbsent: gates declared, the EvalSuite CRD not installed. The
	// core tier promotes ungated, loudly (design 02 §3.3).
	gateCRDAbsent
	// gateEnforced: gates declared and the CRD installed. The gate branch
	// answers from the four promotion conditions.
	gateEnforced
)

func (r *AgentReconciler) gateModeOf(agent *assaydv1alpha1.Agent) gateMode {
	switch {
	case len(agent.Spec.Gates) == 0:
		return gateNone
	case r.EvalSuiteInstalled == nil:
		return gateUnwired
	case !r.EvalSuiteInstalled():
		return gateCRDAbsent
	default:
		return gateEnforced
	}
}

// gateDecision is the gate branch's answer for one pass. satisfied falls
// through to the promotion branch; otherwise the candidate holds, with
// GatesPassed=False/reason, and, while no earlier revision serves,
// Ready=False/readyReason (design 16 §1.1's table).
type gateDecision struct {
	satisfied   bool
	reason      string
	message     string
	readyReason string
}

// report sets GatesPassed from a decision that holds and names a reason. The
// gate branch calls it, and so do the not-ready branches, which come before
// it: a candidate that is not available yet, or no longer, is still described
// by the gate's answer for it, and GatesPassed would otherwise stay absent on
// a new Agent, or keep an earlier candidate's reason (design 16 §1.1). A
// satisfied decision, or an unwired reconciler's, which carries no reason,
// sets nothing.
func (d gateDecision) report(c *conditionSet) {
	if !d.satisfied && d.reason != "" {
		c.set(assaydv1alpha1.CondGatesPassed, metav1.ConditionFalse, d.reason, d.message)
	}
}

// gateInput is everything decideGate reads, gathered by the reconciler.
type gateInput struct {
	desired, desiredDigest string
	namespace, suiteName   string
	// missing is the schema check's answer; recordErr is set when the check
	// has no answer at all.
	missing   []string
	recordErr error
	// suite is the EvalSuite as read this pass; suiteErr when it could not be.
	suite       *assaydv1alpha1.EvalSuite
	suiteErr    error
	suiteDigest string
	// status is the pass's working status: the card step has already run on
	// it, so a card a failed re-read cleared this pass reads cleared.
	status *assaydv1alpha1.AgentStatus
	// available is whether the candidate has an available replica this pass.
	// The not-ready branches report the decision too, and a candidate that is
	// not available is sent nothing (step 8).
	available bool
	now       time.Time
	// agent, runNS and route word the missing-policy wedge's message. route
	// is `<agent>-serving` as the gate branch read it through the manager's
	// client, read only when wedgeNeedsRoute, and nil when it was not read,
	// is gone, or could not be read.
	agent *assaydv1alpha1.Agent
	runNS string
	route *gatewayv1.HTTPRoute
}

// decideGate answers the gate branch from the four promotion conditions,
// read in the same pass (design 16 §1.1, "Promotion"):
//
//   - status.eval.verdict is Passed;
//   - status.eval.revisionDigest equals the desired revision's full digest;
//   - status.eval.suiteDigest equals the digest of the suite read this pass;
//   - status.cards[] records a non-empty card digest for the desired digest.
//
// When any is false it says why, first match wins. §1.1 fixes two orderings:
// the schema reasons come first (A13), and a written Passed verdict outranks a
// recorded transaction ("Clause 5 stops new claims only"). The rest is this
// function's, and it has two arms, by whether a Lock exempt by clauses 1 to 4
// is recorded (exemptTransaction):
//
//  1. the Agent CRD would prune the eval record (EvalRecordUnverifiable, then
//     EvalRecordNotKept). First, because a stale CRD prunes every new
//     status.eval field, and everything below reads them (A13).
//  2. the suite is missing or unreadable (EvalSuiteNotFound, then
//     EvalSuiteUnreadable). The pair is unknown without it.
//  3. a Passed verdict for this pair: satisfied with its card recorded, and
//     EvalAwaitingCard without it. A written Passed verdict outranks every
//     reason a run could be waiting for, the transaction included.
//
// Outside the exempt state — no Create or Lock recorded, a refused Adopt, or
// a recorded Create or Lock that is not exempt:
//
//  4. a Failed verdict for this pair (EvalFailed), then the pair in
//     status.eval.failed[] (EvalPairAlreadyFailed): no run starts for it
//     again. EvalFailed first, which names the cases while the record still
//     holds them (#111).
//  5. no card recorded for the candidate (EvalAwaitingCard): a run cannot
//     start without one.
//  6. a recorded Create or Lock that is not exempt (EvalWaitingForAuth),
//     naming its kind, its stage and why it holds the eval, the missing-policy
//     wedge's exits where "When it names the exits" says, and when its
//     deadline passed if it has: on Q12 (a) the eval keeps waiting after it.
//  7. status.eval.blocked, applied only while status.eval.blockedFor matches
//     the current pair and the current Lock's deadline (A12): a refusal
//     written while an exempt Lock stood stops applying the moment it ends.
//
// In the exempt state — a J2 or K2 Lock that meets clauses 1 to 4 — §1.1's
// "What the gate branch derives in the exempt state":
//
//  4. no card recorded for the candidate (EvalAwaitingCard). Before the
//     exempt rows, because the eval controller evaluates clause 5 only on a
//     pass on which the card predicate holds, so blockedFor could not match.
//  5. the candidate not available: EvalFailed, then EvalPairAlreadyFailed,
//     else EvalPaused. Before the exempt rows for the card row's reason: the
//     eval controller evaluates clause 5 only on a pass on which the
//     candidate is available, so a hold recorded before it went away is not
//     re-read while it is away, and applied would name a cause that may be
//     gone for as long as it stays away (§1.1 step 8: a candidate that is not
//     available is EvalPaused). The next clause-5 pass after it comes back
//     rewrites blocked.
//  6. blockedFor does not match: EvalWaitingForAuth, saying the eval
//     controller has not yet evaluated clause 5 for this Lock.
//  7. blocked set: its reason, with blocked.message — clause 5's hold
//     (EvalWaitingForAuth), or a run-start refusal.
//  8. EvalFailed, then EvalPairAlreadyFailed: blocked comes before them here.
//
// And last, in either arm — 8 outside the exempt state, 9 in it:
//
//   - no final verdict: EvalPaused while the candidate is not available, and
//     EvalRunning while it is. Neither names how many cases are done while the
//     run goes on: EvalRunning names the pair and N, and status.eval.cases[]
//     carries progress, so an eval patch that records a case leaves the Agent
//     reconciler's status write a no-op (A5). EvalPaused names k of N, which no
//     send changes while the candidate is unavailable.
//
// The card row, the transaction rows and clauses 1 to 4 are shared send
// predicates (sendpredicates.go), so this branch and the eval controller
// cannot disagree about them. Clause 5 is not: this branch makes no Gateway or
// policy read, and reads its result from blocked (A11).
func decideGate(in gateInput) gateDecision {
	rev := fmt.Sprintf("revision %s (digest %s)", in.desired, in.desiredDigest)

	switch {
	case in.recordErr != nil:
		return gateDecision{reason: ReasonEvalRecordUnverifiable, readyReason: ReasonEvalRecordUnverifiable,
			message: fmt.Sprintf(
				"the installed Agent CRD could not be read to check that it keeps status.eval's eval "+
					"record: %v. %s holds at zero traffic until it can be: a verdict the API server "+
					"prunes would be lost, and its case claims with it (design 16 §1.1 step 11)",
				in.recordErr, rev)}
	case len(in.missing) > 0:
		return gateDecision{reason: ReasonEvalRecordNotKept, readyReason: ReasonEvalRecordNotKept,
			message: fmt.Sprintf(
				"the installed Agent CRD does not carry status.eval's %s, so the API server prunes the "+
					"eval record from every write, without an error, and no verdict could be kept. %s "+
					"holds at zero traffic. `helm upgrade` never updates a chart's crds/, so apply this "+
					"release's charts/assayd/crds/ with kubectl (design 16 §1.1 step 11)",
				strings.Join(in.missing, ", "), rev)}
	case in.suiteErr != nil && apierrors.IsNotFound(in.suiteErr):
		return gateDecision{reason: ReasonEvalSuiteNotFound, readyReason: reasonAwaitingGates,
			message: fmt.Sprintf(
				"spec.gates names EvalSuite %q, which does not exist in namespace %s. %s holds at zero "+
					"traffic until the suite is created or the gate changed", in.suiteName,
				in.namespace, rev)}
	case in.suiteErr != nil:
		return gateDecision{reason: ReasonEvalSuiteUnreadable, readyReason: reasonAwaitingGates,
			message: fmt.Sprintf(
				"EvalSuite %q, which spec.gates names, could not be read: %v. %s holds at zero traffic "+
					"until it can be: a gate whose suite cannot be read cannot be passed",
				in.suiteName, in.suiteErr, rev)}
	}

	pair := fmt.Sprintf("%s against suite %s (digest %s)", rev, in.suiteName, in.suiteDigest)
	e := in.status.Eval
	matched := e != nil && e.RevisionDigest == in.desiredDigest && e.SuiteDigest == in.suiteDigest
	if matched && e.Verdict == assaydv1alpha1.EvalVerdictPassed {
		if hasCardFor(in.status, in.desiredDigest) {
			return gateDecision{satisfied: true}
		}
		return gateDecision{reason: ReasonEvalAwaitingCard, readyReason: reasonAwaitingGates,
			message: fmt.Sprintf(
				"%s passed, score %s, but status.cards[] no longer records a card digest for it, and "+
					"a gated revision is promoted only with its card recorded (design 16 Q10). It "+
					"holds at zero traffic, and is not promoted without one", pair, e.Score)}
	}
	tx := recordedTransaction(in.status)
	exempt, why := false, ""
	if tx != nil {
		exempt, why = exemptTransaction(in.status)
	}
	if !exempt {
		if d, ok := failedRows(in, pair); ok {
			return d
		}
	}
	if !hasCardFor(in.status, in.desiredDigest) {
		return gateDecision{reason: ReasonEvalAwaitingCard, readyReason: reasonAwaitingGates,
			message: fmt.Sprintf(
				"status.cards[] records no card digest for %s, and a gated revision is promoted only "+
					"with its card recorded (design 16 Q10). It holds at zero traffic, and is not "+
					"promoted without one", rev)}
	}
	bfMatched := e != nil && blockedForMatches(e.BlockedFor, in.desiredDigest, in.suiteDigest, in.status)
	switch {
	case tx != nil && !exempt:
		// A recorded Create or Lock that is not exempt: the eval controller
		// claims no case while it is (design 16 §1.1, "On the Agent being
		// evaluated"). On Q12 (a) one past its deadline still holds the eval.
		return gateDecision{reason: ReasonEvalWaitingForAuth, readyReason: reasonAwaitingGates,
			message: waitingMessage(in, tx, why, pair)}
	case exempt && !in.available:
		// Ahead of blocked: no clause-5 pass reaches an unavailable candidate,
		// so a hold recorded before it went away is not re-read meanwhile.
		if d, ok := failedRows(in, pair); ok {
			return d
		}
		return noVerdictRows(in, pair)
	case exempt && !bfMatched:
		// The exempt state, before the eval controller has evaluated clause 5
		// for this Lock: fail-closed, never EvalRunning on a blocked that was
		// not evaluated for the current state (§1.1, "What the gate branch
		// derives in the exempt state").
		return gateDecision{reason: ReasonEvalWaitingForAuth, readyReason: reasonAwaitingGates,
			message: fmt.Sprintf("an -auth Lock is recorded on this Agent, in stage %s, that can finish "+
				"only through a promotion, so the eval controller may claim cases of %s beside it once "+
				"it has found no Gateway-level or foreign traffic policy holding it (design 16 §1.1, "+
				"the exemption's clause 5). It has not yet evaluated clause 5 for this Lock — it does "+
				"so on a pass on which every other send predicate holds — and claims nothing until it "+
				"has. It holds at zero traffic meanwhile", tx.Stage, pair)}
	}
	if e != nil && e.Blocked != nil && bfMatched {
		var d gateDecision
		switch e.Blocked.Reason {
		case ReasonEvalBindingUnsupported:
			d = gateDecision{reason: ReasonEvalBindingUnsupported, readyReason: reasonAwaitingGates}
		case ReasonEvalAwaitingCard:
			d = gateDecision{reason: ReasonEvalAwaitingCard, readyReason: reasonAwaitingGates}
		case ReasonEvalWaitingForAuth:
			d = gateDecision{reason: ReasonEvalWaitingForAuth, readyReason: reasonAwaitingGates}
		}
		if d.reason != "" {
			d.message = fmt.Sprintf("the eval controller recorded for %s that no case can be sent: %s",
				pair, e.Blocked.Message)
			return d
		}
	}
	if exempt {
		// In the exempt state blocked comes before the table's rows,
		// EvalFailed and EvalPairAlreadyFailed among them.
		if d, ok := failedRows(in, pair); ok {
			return d
		}
	}
	return noVerdictRows(in, pair)
}

// noVerdictRows are the table's rows with no final verdict: EvalPaused while
// the candidate is not available, naming k of N, and EvalRunning while it is.
func noVerdictRows(in gateInput, pair string) gateDecision {
	e := in.status.Eval
	n := len(in.suite.Spec.Dataset.Cases)
	if !in.available {
		progress := "no run has started for it"
		if e != nil && e.RevisionDigest == in.desiredDigest && e.SuiteDigest == in.suiteDigest &&
			e.Verdict == assaydv1alpha1.EvalVerdictRunning {
			progress = fmt.Sprintf("%d of its %d cases have an outcome, and the run resumes at the next "+
				"case by id once it is available again", recordedOutcomes(e.Cases, in.suite), n)
		}
		return gateDecision{reason: ReasonEvalPaused, readyReason: reasonAwaitingGates,
			message: fmt.Sprintf("%s is not available, so the eval controller sends it no case: %s. "+
				"Nothing times a paused run out; it holds at zero traffic", pair, progress)}
	}
	return gateDecision{reason: ReasonEvalRunning, readyReason: reasonAwaitingGates,
		message: fmt.Sprintf("status.eval records no final verdict for %s, a run of the suite's %d "+
			"cases; status.eval.cases[] records the outcomes it has. It holds at zero traffic until a "+
			"verdict is written", pair, n)}
}

// failedRows are the table's two rows for a pair that failed: a Failed
// verdict for it (EvalFailed), which names the cases while the record still
// holds them, and then the pair in failed[] (EvalPairAlreadyFailed), for which
// no run starts again.
func failedRows(in gateInput, pair string) (gateDecision, bool) {
	e := in.status.Eval
	if e == nil {
		return gateDecision{}, false
	}
	if e.RevisionDigest == in.desiredDigest && e.SuiteDigest == in.suiteDigest &&
		e.Verdict == assaydv1alpha1.EvalVerdictFailed {
		return gateDecision{reason: ReasonEvalFailed, readyReason: ReasonEvalFailed,
			message: fmt.Sprintf(
				"%s failed: score %s against threshold %s; failed cases: %s. It holds at zero traffic "+
					"and is not evaluated again for this pair: an edit to the suite gives a new pair, "+
					"and an edit to the spec supersedes it with a new revision", pair, e.Score,
				threshold(in.suite), failedCases(e.Cases))}, true
	}
	if pairFailed(e.Failed, in.desiredDigest, in.suiteDigest) {
		return gateDecision{reason: ReasonEvalPairAlreadyFailed, readyReason: ReasonEvalFailed,
			message: fmt.Sprintf(
				"%s already failed: status.eval.failed[] records the pair, so it is not evaluated again "+
					"and holds at zero traffic. An edit to the suite gives a new pair, which this record "+
					"does not hold", pair)}, true
	}
	return gateDecision{}, false
}

// waitingMessage is EvalWaitingForAuth's message for a recorded Create or
// Lock that is not exempt: its kind and stage, why it holds the eval, and, on
// Q12 (a), when its deadline passed if it has. For the missing-policy wedge
// it names the exits instead ("When it names the exits"), from the route the
// gate branch read.
func waitingMessage(in gateInput, tx *assaydv1alpha1.AuthTransaction, why, pair string) string {
	msg := fmt.Sprintf("an -auth %s is recorded on this Agent, in stage %s, and the eval controller "+
		"claims no case of %s while it is, because %s (design 16 §1.1). It holds at zero traffic "+
		"until the transaction ends", tx.Kind, tx.Stage, pair, why)
	if note := wedgeExits(in); note != "" {
		msg += ". " + note
	}
	if tx.Deadline != nil && !in.now.Before(tx.Deadline.Time) {
		msg += fmt.Sprintf("; its deadline passed at %s, and the eval keeps waiting after it "+
			"(design 16 Q12 (a))", tx.Deadline.UTC().Format(time.RFC3339))
	}
	return msg
}

// wedgeNeedsRoute reports whether the gate branch must read `<agent>-serving`
// to word EvalWaitingForAuth: the first and third of "When it names the
// exits"' clauses, which status alone answers — the recorded transaction is a
// missing-policy Lock at ProbingAfter, and status.cards[] records no digest
// for status.activeRevision. The second needs the route.
func wedgeNeedsRoute(status *assaydv1alpha1.AgentStatus) bool {
	tx := recordedTransaction(status)
	return tx != nil && tx.Kind == TxLock && isReCreation(status.Auth) && tx.Stage == StageProbingAfter &&
		!cardedRevision(status, status.ActiveRevision)
}

// wedgeExits is the missing-policy wedge's part of EvalWaitingForAuth, or ""
// (design 16 §1.1, "When it names the exits"). All three clauses must hold:
// the recorded transaction is a missing-policy Lock at ProbingAfter; the route
// does not carry exactly one backendRef, or the revision its single
// backendRef names has no recorded card digest; and status.cards[] records
// none for status.activeRevision either. Those are cardAttributes' reading
// and the re-point's, composed as missingPolicyRouteNote composes them, and
// the note is that function's own wording, so one state has one phrasing. R's
// name is the backendRef's, with the `<agent>-` prefix trimmed, so it
// survives R's Service being collected. A route that was not read — gone, or
// unreadable — has no R, and the message is the generic one.
func wedgeExits(in gateInput) string {
	if !wedgeNeedsRoute(in.status) || in.route == nil || in.agent == nil {
		return ""
	}
	if attributed, _, _ := cardAttributes(in.agent, in.status, in.route); attributed {
		return ""
	}
	note := missingPolicyRouteNote(in.agent, in.status, in.route, in.runNS, false, false, nil)
	return "The eval waits on the Lock of a missing policy for which no 401 through the route can be " +
		"attributed, which is design 03's state and not the gate's; deleting the route is an " +
		"administrator's of the run namespace, where by default no user holds any right. " + note +
		" On this gated Agent the eval claims no case while the Lock is recorded, so a candidate's " +
		"promotion, which would end it with nobody acting, waits on the Lock in turn, unless its " +
		"verdict was written before the Lock began or comes from the case in flight when it began; " +
		"and status.activeRevision's own card recording would end it too, though no fetch of that " +
		"card runs while another revision is the desired one"
}

// recordedOutcomes counts the suite ids whose entry in cases[] carries an
// outcome, which a claim in flight (Sending) does not.
func recordedOutcomes(cases []assaydv1alpha1.EvalCaseRecord, s *assaydv1alpha1.EvalSuite) int {
	k := 0
	for _, c := range s.Spec.Dataset.Cases {
		if rec := caseRecord(cases, c.ID); rec != nil && outcomeRecorded(rec.Outcome) {
			k++
		}
	}
	return k
}

// blockedForMatches is A12's fail-closed reading: blocked applies only while
// blockedFor names the current candidate's digest, the current suite's digest
// and the deadline of the Lock recorded now. Absent or unmatched reads as
// not evaluated.
func blockedForMatches(bf *assaydv1alpha1.EvalBlockedFor, revDigest, suiteDigest string,
	status *assaydv1alpha1.AgentStatus) bool {
	if bf == nil || bf.RevisionDigest != revDigest || bf.SuiteDigest != suiteDigest {
		return false
	}
	lock := recordedLockDeadline(status)
	switch {
	case lock == nil || bf.LockDeadline == nil:
		return lock == nil && bf.LockDeadline == nil
	default:
		return lock.Equal(bf.LockDeadline)
	}
}

// recordedLockDeadline is the deadline of the Lock recorded in
// status.auth.transaction, truncated to the second, or nil: the third part of
// blockedFor, which the eval controller writes and this branch matches with
// the same reading. Design 03 sets it truncated (enterLock,
// lockMissingPolicy) and the API server stores it to the second; truncating
// here too keeps a deadline held in memory at a finer grain from reading as
// another Lock. So two Locks entered inside one second share it: they would
// also need the same pair, and the spec edit that re-enters a Lock changes the
// candidate, so the residue is at most one pass of a wrong condition, with
// sends still fail-closed (A13).
func recordedLockDeadline(status *assaydv1alpha1.AgentStatus) *metav1.Time {
	if tx := authTransaction(status); tx != nil && tx.Kind == TxLock && tx.Deadline != nil {
		d := metav1.NewTime(tx.Deadline.Truncate(time.Second))
		return &d
	}
	return nil
}

func pairFailed(failed []assaydv1alpha1.EvalPair, revDigest, suiteDigest string) bool {
	for _, p := range failed {
		if p.RevisionDigest == revDigest && p.SuiteDigest == suiteDigest {
			return true
		}
	}
	return false
}

// failedCases names each case that carries a failure reason. A passing case
// records none, so this reads no outcome spelling.
func failedCases(cases []assaydv1alpha1.EvalCaseRecord) string {
	var out []string
	for _, c := range cases {
		if c.Reason != "" {
			out = append(out, fmt.Sprintf("%s (%s)", c.ID, c.Reason))
		}
	}
	if len(out) == 0 {
		return "none recorded"
	}
	return strings.Join(out, ", ")
}

// threshold is the suite's threshold as the verdict rule reads it,
// evalsuite.Threshold rendered by evalsuite.FormatThreshold, so the EvalFailed
// message and the rule that wrote the verdict cannot disagree about it. A
// suite with no metric reads "1.000": every case must pass (design 16 §1.1).
func threshold(s *assaydv1alpha1.EvalSuite) string {
	th, err := evalsuite.Threshold(s.Spec)
	if err != nil {
		return fmt.Sprintf("unreadable (%v)", err)
	}
	return evalsuite.FormatThreshold(th)
}

// gateFor gathers the gate branch's inputs and decides. It reads the suite
// uncached by a get, so no EvalSuite informer starts and the operator needs
// get alone, and the Agent CRD behind the schema check's TTL cache.
func (r *AgentReconciler) gateFor(ctx context.Context, agent *assaydv1alpha1.Agent,
	status *assaydv1alpha1.AgentStatus, desired, desiredDigest string, available bool) gateDecision {
	in := gateInput{desired: desired, desiredDigest: desiredDigest, status: status, now: time.Now(),
		namespace: agent.Namespace, suiteName: agent.Spec.Gates[0].EvalSuiteRef, available: available,
		agent: agent, runNS: RunNamespaceName(agent.Namespace)}
	// The wedge's message is computed from this read of the route, through
	// the manager's client, and from status.auth.transaction as recorded at
	// the start of the pass: the gate branch runs before reconcileGateway, so
	// it can be one pass stale (§1.1, "It can be one pass stale"). A route
	// that is gone, or a read that fails, leaves the generic text.
	if wedgeNeedsRoute(status) {
		if name, err := compiler.ServingRouteName(agent.Name); err == nil {
			if rt, err := r.currentServingRoute(ctx, agent, in.runNS, name); err == nil {
				in.route = rt
			}
		}
	}
	in.missing, in.recordErr = r.evalRecordMissing(ctx)
	if in.recordErr == nil && len(in.missing) == 0 {
		var s assaydv1alpha1.EvalSuite
		in.suiteErr = r.reader().Get(ctx, types.NamespacedName{Namespace: agent.Namespace,
			Name: in.suiteName}, &s)
		if in.suiteErr == nil {
			in.suite = &s
			if in.suiteDigest, in.suiteErr = evalsuite.Digest(s.Spec); in.suiteErr != nil {
				in.suiteErr = fmt.Errorf("digest it: %w", in.suiteErr)
			}
		}
	}
	return decideGate(in)
}

// evalRecordFields are the status.eval fields design 16 §1.1 adds, which the
// installed Agent CRD must carry for a verdict to be kept. blockedFor is in
// the set because the fail-closed reading of blocked rests on it (A13).
var evalRecordFields = []string{"suiteDigest", "endpointPath", "cardDigest", "blocked", "blockedFor",
	"verdict", "cases", "failed"}

// evalRecordMissingFrom names each eval-record field the CRD's v1alpha1
// schema does not carry under status.eval, and cases' claim, in that order.
func evalRecordMissingFrom(crd *apiextensionsv1.CustomResourceDefinition) []string {
	var eval *apiextensionsv1.JSONSchemaProps
	for i := range crd.Spec.Versions {
		v := crd.Spec.Versions[i]
		if v.Name != assaydv1alpha1.GroupVersion.Version || v.Schema == nil || v.Schema.OpenAPIV3Schema == nil {
			continue
		}
		if st, ok := v.Schema.OpenAPIV3Schema.Properties["status"]; ok {
			if ev, ok := st.Properties["eval"]; ok {
				eval = &ev
			}
		}
	}
	var missing []string
	for _, f := range evalRecordFields {
		var prop apiextensionsv1.JSONSchemaProps
		ok := false
		if eval != nil {
			prop, ok = eval.Properties[f]
		}
		switch {
		case !ok:
			missing = append(missing, f)
		case f == "cases" && (prop.Items == nil || prop.Items.Schema == nil):
			missing = append(missing, "cases[].claim")
		case f == "cases":
			if _, ok := prop.Items.Schema.Properties["claim"]; !ok {
				missing = append(missing, "cases[].claim")
			}
		}
	}
	return missing
}

// evalRecordCheck caches the schema check's last answer, as the EvalSuite
// detector caches its own: an answer is reused for the TTL, and a failed read
// leaves the last answer standing, however old. With none cached, the failure
// is the answer.
type evalRecordCheck struct {
	mu      sync.Mutex
	warmed  bool
	checked time.Time
	missing []string
	// failing is whether the last read failed, so that a cached answer kept
	// over a failed read is logged once per transition, not once per pass.
	failing bool
}

func (r *AgentReconciler) evalRecordMissing(ctx context.Context) ([]string, error) {
	ttl := r.EvalRecordCheckTTL
	if ttl == 0 {
		ttl = DefaultEvalRecordCheckTTL
	}
	c := &r.evalRecord
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.warmed && time.Since(c.checked) < ttl {
		return c.missing, nil
	}
	var crd apiextensionsv1.CustomResourceDefinition
	if err := r.reader().Get(ctx, types.NamespacedName{Name: agentCRDName}, &crd); err != nil {
		if !c.failing {
			log.FromContext(ctx).Error(err, "could not read the Agent CRD to check that it keeps "+
				"status.eval's record; the last answer stands, or, with none, gated candidates hold "+
				"under EvalRecordUnverifiable", "crd", agentCRDName, "lastAnswerCached", c.warmed)
			c.failing = true
		}
		if c.warmed {
			return c.missing, nil
		}
		return nil, err
	}
	if c.failing {
		log.FromContext(ctx).Info("the Agent CRD can be read again for the eval-record check",
			"crd", agentCRDName)
		c.failing = false
	}
	c.warmed, c.checked, c.missing = true, time.Now(), evalRecordMissingFrom(&crd)
	return c.missing, nil
}
