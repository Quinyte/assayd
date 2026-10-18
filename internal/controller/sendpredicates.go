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
//	the candidate is available                  workloadAvailableIn,        workloadAvailableIn
//	                                            before the gate (not-ready
//	                                            branches: EvalPaused)
//	no I1 hold stands                           i1Hold, before the gate     i1Hold
//	                                            (EvalHeldForAuth)
//	a card is recorded for the candidate        hasCardFor (decideGate's    hasCardFor
//	                                            EvalAwaitingCard)
//	no Create or Lock is recorded               claimHoldingTransaction     claimHoldingTransaction
//	                                            (EvalWaitingForAuth)
//	observedGeneration == generation            —                           evalGenerationCurrent
//
// The generation clause is the eval controller's alone: the Agent reconciler
// is the observer that sets observedGeneration in the same pass, so it does not
// evaluate it, and no GatesPassed row depends on it (A6). The J2/K2 exemption
// from the transaction predicate is design 16 build step 7's; until it lands
// every recorded Create or Lock holds the eval, which is fail-closed.

// candidatePending is the third predicate: status records a candidate whose
// digest differs from the active one's. Every comparison is on the digest,
// never the 40-bit name (A2).
func candidatePending(status *assaydv1alpha1.AgentStatus) bool {
	return status.CandidateRevisionDigest != "" &&
		status.CandidateRevisionDigest != status.ActiveRevisionDigest
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
// "selects a revision that already served" is not enforced by
// resolveReleasePin, which checks selection only (design 16 §1.1 owes the
// correction, and the pin rule is build step 8's).
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
