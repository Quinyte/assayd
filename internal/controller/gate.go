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

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/evalsuite"
)

// The GatesPassed reasons the Agent reconciler's gate branch sets (design 16
// §1.1, "Status, conditions and phase"). Three more of the table's are not
// here, and that is build step 5's scope rather than an omission:
// EvalRunning and EvalPaused describe a run, and nothing runs one until the
// eval controller lands (build step 6), so until then a gated candidate with
// no verdict keeps ReasonGateControllerUnimplemented and no pass ever says a
// run is under way. The pin rule's reasons are build step 8's.
const (
	// ReasonGateControllerUnimplemented: a gated candidate with its suite, its
	// card and no final verdict. Retired by build step 6, which replaces it
	// with EvalRunning.
	ReasonGateControllerUnimplemented = "GateControllerUnimplemented"
	ReasonEvalRecordNotKept           = "EvalRecordNotKept"
	ReasonEvalRecordUnverifiable      = "EvalRecordUnverifiable"
	ReasonEvalSuiteNotFound           = "EvalSuiteNotFound"
	ReasonEvalSuiteUnreadable         = "EvalSuiteUnreadable"
	ReasonEvalAwaitingCard            = "EvalAwaitingCard"
	ReasonEvalBindingUnsupported      = "EvalBindingUnsupported"
	ReasonEvalWaitingForAuth          = "EvalWaitingForAuth"
	ReasonEvalPairAlreadyFailed       = "EvalPairAlreadyFailed"
	ReasonEvalFailed                  = "EvalFailed"
	ReasonEvalPassed                  = "EvalPassed"
	ReasonEvalHeldForAuth             = "EvalHeldForAuth"
	ReasonActiveRevisionNotEvaluated  = "ActiveRevisionNotEvaluated"
	reasonAwaitingGates               = "AwaitingGates"

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
	now    time.Time
}

// decideGate answers the gate branch from the four promotion conditions,
// read in the same pass (design 16 §1.1, "Promotion"):
//
//   - status.eval.verdict is Passed;
//   - status.eval.revisionDigest equals the desired revision's full digest;
//   - status.eval.suiteDigest equals the digest of the suite read this pass;
//   - status.cards[] records a non-empty card digest for the desired digest.
//
// When any is false it says why, first match wins:
//
//  1. the Agent CRD would prune the eval record (EvalRecordNotKept,
//     EvalRecordUnverifiable). First, because a stale CRD prunes every new
//     status.eval field, and everything below reads them (A13).
//  2. the suite is missing or unreadable (EvalSuiteNotFound,
//     EvalSuiteUnreadable). The pair is unknown without it.
//  3. a final verdict for this pair: Passed with no recorded card
//     (EvalAwaitingCard), or Failed (EvalFailed). A written verdict outranks
//     every reason a run could be waiting for, because no run is pending.
//  4. the pair is in status.eval.failed[] (EvalPairAlreadyFailed): no run
//     starts for it again. After 3's EvalFailed, which names the cases while
//     the record still holds them.
//  5. no card recorded for the candidate (EvalAwaitingCard): a run cannot
//     start without one.
//  6. a Create or Lock recorded in status.auth.transaction. A refused Adopt is
//     neither, and does not count. Until build step 6 this gives
//     GateControllerUnimplemented with the transaction named, not
//     EvalWaitingForAuth: no eval controller exists to wait on it, and
//     nothing would run after it either.
//  7. status.eval.blocked, applied only while status.eval.blockedFor matches
//     the current pair and the current Lock's deadline (A12).
//  8. otherwise no final verdict: GateControllerUnimplemented.
//
// The exemption for a J2 or K2 Lock that can finish only through a promotion,
// and the missing-policy wedge's message, are build step 7's: until then
// every recorded Create or Lock holds at 6, which is fail-closed. Step 5
// sets EvalWaitingForAuth only from blocked (7), which nothing writes before
// step 6.
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
	if e != nil && e.RevisionDigest == in.desiredDigest && e.SuiteDigest == in.suiteDigest {
		switch e.Verdict {
		case assaydv1alpha1.EvalVerdictPassed:
			if hasCardFor(in.status, in.desiredDigest) {
				return gateDecision{satisfied: true}
			}
			return gateDecision{reason: ReasonEvalAwaitingCard, readyReason: reasonAwaitingGates,
				message: fmt.Sprintf(
					"%s passed, score %s, but status.cards[] no longer records a card digest for it, and "+
						"a gated revision is promoted only with its card recorded (design 16 Q10). It "+
						"holds at zero traffic, and is not promoted without one", pair, e.Score)}
		case assaydv1alpha1.EvalVerdictFailed:
			return gateDecision{reason: ReasonEvalFailed, readyReason: ReasonEvalFailed,
				message: fmt.Sprintf(
					"%s failed: score %s against threshold %s; failed cases: %s. It holds at zero traffic "+
						"and is not evaluated again for this pair: an edit to the suite gives a new pair, "+
						"and an edit to the spec supersedes it with a new revision", pair, e.Score,
					threshold(in.suite), failedCases(e.Cases))}
		}
	}
	if e != nil && pairFailed(e.Failed, in.desiredDigest, in.suiteDigest) {
		return gateDecision{reason: ReasonEvalPairAlreadyFailed, readyReason: ReasonEvalFailed,
			message: fmt.Sprintf(
				"%s already failed: status.eval.failed[] records the pair, so it is not evaluated again "+
					"and holds at zero traffic. An edit to the suite gives a new pair, which this record "+
					"does not hold", pair)}
	}
	if !hasCardFor(in.status, in.desiredDigest) {
		return gateDecision{reason: ReasonEvalAwaitingCard, readyReason: reasonAwaitingGates,
			message: fmt.Sprintf(
				"status.cards[] records no card digest for %s, and a gated revision is promoted only "+
					"with its card recorded (design 16 Q10). It holds at zero traffic, and is not "+
					"promoted without one", rev)}
	}
	// A recorded Create or Lock is a further reason no case would be claimed,
	// and in build step 6, where an eval controller exists to wait on it, it
	// becomes EvalWaitingForAuth here. Until then nothing runs after the
	// transaction either, and for a J2 or K2 Lock that can finish only
	// through a promotion (step 7's exemption) the gate is the real block, so
	// the reason stays GateControllerUnimplemented and the message names the
	// transaction beside it.
	if tx := claimHoldingTransaction(in.status); tx != nil {
		msg := noVerdictMessage(pair) + fmt.Sprintf(". An -auth %s is also recorded on this Agent, "+
			"in stage %s, and no case would be claimed while it is (design 16 §1.1)", tx.Kind, tx.Stage)
		if tx.Deadline != nil && !in.now.Before(tx.Deadline.Time) {
			msg += fmt.Sprintf("; its deadline passed at %s", tx.Deadline.UTC().Format(time.RFC3339))
		}
		return gateDecision{reason: ReasonGateControllerUnimplemented, readyReason: reasonAwaitingGates,
			message: msg}
	}
	if e != nil && e.Blocked != nil && blockedForMatches(e.BlockedFor, in.desiredDigest, in.suiteDigest, in.status) {
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
	return gateDecision{reason: ReasonGateControllerUnimplemented, readyReason: reasonAwaitingGates,
		message: noVerdictMessage(pair)}
}

// noVerdictMessage is GateControllerUnimplemented's message. It claims no run
// is under way or will be: none can be until the eval controller exists.
func noVerdictMessage(pair string) string {
	return fmt.Sprintf("status.eval records no final verdict for %s, and design 16's eval controller, "+
		"which would run the suite and write one, is not built in this release. It holds at zero "+
		"traffic rather than promoting ungated", pair)
}

// claimHoldingTransaction is the transaction §1.1 holds every new claim for:
// a Create or a Lock recorded in status.auth.transaction, at any stage. A
// refused Adopt is neither, and does not count. The J2/K2 exemption is build
// step 7's.
func claimHoldingTransaction(status *assaydv1alpha1.AgentStatus) *assaydv1alpha1.AuthTransaction {
	if tx := authTransaction(status); tx != nil && (tx.Kind == TxCreate || tx.Kind == TxLock) {
		return tx
	}
	return nil
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
	var lock *time.Time
	if tx := authTransaction(status); tx != nil && tx.Kind == TxLock && tx.Deadline != nil {
		t := tx.Deadline.Time
		lock = &t
	}
	switch {
	case lock == nil || bf.LockDeadline == nil:
		return lock == nil && bf.LockDeadline == nil
	default:
		return lock.Equal(bf.LockDeadline.Time)
	}
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

// threshold is the suite's threshold as written, or "1" when it declares no
// metric: absent means every case must pass (design 16 §1.1).
func threshold(s *assaydv1alpha1.EvalSuite) string {
	if s != nil && len(s.Spec.Metrics) > 0 {
		return s.Spec.Metrics[0].Threshold
	}
	return "1"
}

// gateFor gathers the gate branch's inputs and decides. It reads the suite
// uncached by a get, so no EvalSuite informer starts and the operator needs
// get alone, and the Agent CRD behind the schema check's TTL cache.
func (r *AgentReconciler) gateFor(ctx context.Context, agent *assaydv1alpha1.Agent,
	status *assaydv1alpha1.AgentStatus, desired, desiredDigest string) gateDecision {
	in := gateInput{desired: desired, desiredDigest: desiredDigest, status: status, now: time.Now(),
		namespace: agent.Namespace, suiteName: agent.Spec.Gates[0].EvalSuiteRef}
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
