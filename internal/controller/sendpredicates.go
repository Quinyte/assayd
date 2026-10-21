// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/revision"
)

// The send predicates (design 16 §1.1, "The two controllers"). Both
// controllers decide whether a case may be sent with the same predicates,
// implemented once, here, and called by both. Which controller evaluates which:
//
//	predicate                                   Agent reconciler            eval controller
//	the gate is declared                        gateModeOf                  gateModeOf's first case
//	the EvalSuite CRD is installed              gateModeOf                  EvalSuiteInstalled
//	a candidate is pending                      the rollout switch's        candidatePending
//	                                            active != desired guard
//	the candidate is not an admitted pinned     the pin step, before the    pinnedRollbackCandidate
//	rollback                                    switch (GatesBypassed, or
//	                                            the refusal's early return)
//	the candidate is available                  workloadAvailableIn,        workloadAvailableIn
//	                                            before the gate (not-ready
//	                                            branches: EvalPaused)
//	no I1 hold stands                           i1Hold, before the gate     i1Hold
//	                                            (EvalHeldForAuth)
//	a card is recorded for the candidate        hasCardFor (decideGate's    hasCardFor
//	                                            EvalAwaitingCard)
//	no Create or Lock is recorded, or the       recordedTransaction and     recordedTransaction and
//	one recorded is exempt (clauses 1 to 4)     exemptTransaction           exemptTransaction
//	                                            (EvalWaitingForAuth)
//	the exempt Lock's clause 5: no Gateway-     —, read from                clause5, last
//	level or foreign traffic hold stands        status.eval.blocked
//	observedGeneration == generation            —                           evalGenerationCurrent
//
// The generation clause is the eval controller's alone: the Agent reconciler
// is the observer that sets observedGeneration in the same pass, so it does not
// evaluate it, and no GatesPassed row depends on it (A6). Clause 5 is the eval
// controller's alone in the same way: it makes the Gateway and policy reads,
// and the Agent reconciler reads its result from status.eval.blocked, applied
// only while status.eval.blockedFor matches (A11, A12).

// candidatePending is the third predicate: status records a candidate whose
// digest differs from the active one's. Every comparison is on the digest,
// never the 40-bit name (A2).
func candidatePending(status *assaydv1alpha1.AgentStatus) bool {
	return status.CandidateRevisionDigest != "" &&
		status.CandidateRevisionDigest != status.ActiveRevisionDigest
}

// pinnedRollbackCandidate is the eval controller's reading of design 16
// Q5 (a)'s "promotes, not evaluated": the candidate status records is a
// pinned revision the Agent reconciler admits, so no case of it is sent. A
// pinned revision that is not yet available is recorded as the candidate by
// the rollout switch's not-ready branches, and is promoted by the pass after
// it becomes available; between the two every other send predicate can hold,
// and a run started there could fail it into status.eval.failed[].
//
// It derives admission as pinIneligible does, from the spec and status, and
// reads no condition, so a forged GatesBypassed pauses nothing: the pin names
// the recorded candidate, and that is not the revision the current spec mints
// (specDigestOf). A pin the Agent reconciler refuses as the current candidate
// therefore leaves the candidate's run going. Its other refusal, a pair failed
// against the suite in force, needs no reading here: the eval controller
// starts no run for a failed pair whatever this says. When the spec's digest cannot be computed — a
// referenced source missing or unreadable — it reads the pin as admitted
// and holds, since the Agent reconciler judges no pin on such a pass.
func pinnedRollbackCandidate(ctx context.Context, reader client.Reader, agent *assaydv1alpha1.Agent) (bool, error) {
	if agent.Spec.Release == nil {
		return false, nil
	}
	d := agent.Spec.Release.TargetRevisionDigest
	if d == "" || d != agent.Status.CandidateRevisionDigest {
		return false, nil
	}
	spec, ok, err := specDigestOf(ctx, reader, agent)
	if err != nil || !ok {
		return true, err
	}
	return d != spec, nil
}

// specDigestOf is the digest the Agent's current spec mints, with the content
// of its env sources, computed as the Agent reconciler computes it. ok is
// false when a referenced source is missing or unreadable.
func specDigestOf(ctx context.Context, reader client.Reader, agent *assaydv1alpha1.Agent) (string, bool, error) {
	resolved, _, unresolved, err := resolveEnvSourcesFrom(ctx, reader, agent)
	if err != nil || len(unresolved) > 0 {
		return "", false, err
	}
	d, err := revision.Digest(agent.Spec, resolved)
	if err != nil {
		return "", false, nil
	}
	return d, true, nil
}

// recordedTransaction is a Create or a Lock recorded in
// status.auth.transaction, at any stage, being abandoned or not, or nil. A
// refused Adopt is neither: a standing record, not a transaction in flight,
// so it never holds the eval (design 16 §1.1's table).
func recordedTransaction(status *assaydv1alpha1.AgentStatus) *assaydv1alpha1.AuthTransaction {
	if tx := authTransaction(status); tx != nil && (tx.Kind == TxCreate || tx.Kind == TxLock) {
		return tx
	}
	return nil
}

// exemptTransaction is clauses 1 to 4 of the exemption for a J2 or K2 Lock
// that can finish only through a promotion (design 16 §1.1, "The exemption"),
// read from status alone. exempt is true when all four hold; why names the
// first that does not, for the message of a transaction the eval waits on.
// Clause 5 is the eval controller's, evaluated last, and only on a pass on
// which these four hold.
//
// A Lock being abandoned while it waits on a finalizer (abandonWaiting) is
// read by the same four clauses on its record: its table row claims exactly
// when its record meets them, so nothing here tells it from a Lock in
// progress.
func exemptTransaction(status *assaydv1alpha1.AgentStatus) (exempt bool, why string) {
	tx := recordedTransaction(status)
	switch {
	case tx == nil:
		return false, "no Create or Lock is recorded"
	case tx.Kind != TxLock:
		return false, "a Create writes and publishes the route, and its status writes must not " +
			"conflict with eval patches"
	// Clause 1: a J2 or K2 entry, not a re-creation — isReCreation's test,
	// negated. The human decided on 2026-09-17 to leave this boundary (A19,
	// option (a)): the missing-policy Lock is never exempt.
	case isReCreation(status.Auth):
		return false, "it is the Lock of a missing policy, a re-creation, which is never exempt"
	// Clause 2: the policy is written and converged, and only attribution is
	// left.
	case tx.Stage != StageProbingAfter:
		return false, "it is in stage " + tx.Stage + ", before " + StageProbingAfter + ", so the policy " +
			"that closes the route is not yet written, converged and probed"
	// Clause 3: nothing was observed before the Lock, so a 401 can be
	// attributed only on a recorded card.
	case tx.BeforeObserved:
		return false, "it observed the route open before it wrote its policy, so it can finish on " +
			"its own passes"
	// Clause 4: status.cards[] records no non-empty digest for
	// status.activeRevision, read by revision NAME (cardedRevision), the key
	// cardAttributes matches a card entry on, and not by digest (hasCardFor):
	// this clause must negate that attribution exactly (A19).
	case cardedRevision(status, status.ActiveRevision):
		return false, "status.cards[] records a card digest for status.activeRevision " +
			status.ActiveRevision + ", which the route names, so it can finish on its own passes"
	}
	return true, ""
}

// evalGenerationCurrent is the eval controller's own predicate: the Agent
// reconciler has observed the Agent's current generation, so the candidate
// status records is the one the current spec mints. It stops the sends to a
// candidate a spec edit is about to supersede, before the new one is recorded.
func evalGenerationCurrent(agent *assaydv1alpha1.Agent) bool {
	return agent.Status.ObservedGeneration == agent.Generation
}

// i1Hold says why a new revision may not be promoted, or "": a served Agent
// whose current -auth input does not compile keeps its route on the revision
// it names (design 03 §3.3.1, I1). A pinned rollback is not held: -auth
// follows the current spec, never a revision (ADR-0034, F2). That a pin
// "selects a revision that already served" is not enforced: resolveReleasePin
// checks selection only, and on a gated Agent design 16 Q5 (a)'s rule
// (pinRule) refuses the current candidate and a failure against the suite in
// force, and admits a revision that never served otherwise.
func i1Hold(gw GatewayConfig, status *assaydv1alpha1.AgentStatus, d authDesire, pinned bool) string {
	if !gw.Enabled || pinned || status.Auth == nil || d.compiles() {
		return ""
	}
	// A served Agent only, a route re-create's included. A never-served
	// Agent's Create whose target no longer matches is abandoned instead
	// (design 03 §3.3.3), so it has no route for the revision to reach, and a
	// refused Adopt's route keeps following promotion.
	if status.Auth.Mode == "" {
		return ""
	}
	return "its -auth input does not compile, and this Agent's route keeps the -auth it has or is " +
		"being given (design 03 §3.3.1, I1). PolicyCompileFailed says what to change"
}

// workloadAvailableIn reports whether a revision has at least one available
// replica, read through reader. Availability, not readiness of a single pod: a
// Deployment reporting availableReplicas is the closest signal the operator has
// to "this revision can serve". It authorizes promotion, and a case's send, so
// it also answers "available workload of WHICH revision?": a same-named object
// created by anyone with deployments/create is Available too, and the digest
// stamp and the Agent's UID label are the operator's own marks.
func workloadAvailableIn(ctx context.Context, reader client.Reader, agent *assaydv1alpha1.Agent,
	runNS, rev, digest string) (bool, error) {
	var d appsv1.Deployment
	key := types.NamespacedName{Namespace: runNS, Name: WorkloadName(agent.Name, rev)}
	if err := reader.Get(ctx, key, &d); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("get workload %s: %w", key, err)
	}
	if d.Annotations[RevisionDigestAnnotation] != digest || d.Labels[LabelAgentUID] != string(agent.UID) {
		return false, nil
	}
	return d.Status.AvailableReplicas > 0, nil
}
