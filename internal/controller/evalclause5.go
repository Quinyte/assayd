// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"errors"
	"fmt"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/compiler"
)

// Clause 5 of the exemption (design 16 §1.1, "The exemption", "Clause 5's
// reads", "Clause 5 is the eval controller's"): no Gateway-level or foreign
// traffic hold stands, as design 03's own reads find it. The eval controller
// evaluates it last, only on a pass on which clauses 1 to 4 and every other
// send predicate hold, and afresh before every claim, so a hold stops new
// claims only: a case in flight is not stopped by it, and a Passed verdict
// already written still promotes, because the promoting pass checks only the
// four promotion conditions.

// clause5 makes clause 5's reads through the uncached reader (policyReads):
// A75's gatewayAuthPolicies for the Agent's hostname, and §3.2's
// foreignTrafficPolicies for its `<agent>-serving`. It returns the hold to
// record in status.eval.blocked, or nil when none stands, and, when a read
// failed, the error, which the pass returns after recording the hold so that
// controller-runtime's per-item backoff governs the retry (A11, A12).
func (r *EvalReconciler) clause5(ctx context.Context, agent *assaydv1alpha1.Agent,
	runNS string) (*assaydv1alpha1.EvalBlocked, error) {
	reads := r.policyReads()
	above := reads.gatewayAuthPolicies(ctx, r.Gateway.Hostname(agent.Name, agent.Namespace))
	// EmittedName errors only when the concern's own suffix leaves no room
	// for the Agent's name; "-serving" leaves 38 characters, so this cannot
	// fail.
	route, _ := compiler.ServingRouteName(agent.Name)
	// foreignTrafficPolicies lists the run namespace with notServedIsEmpty, so
	// a NotFound there reads as no foreign policies and holds nothing; any
	// other error comes back here, and holds.
	foreign, ferr := reads.foreignTrafficPolicies(ctx, agent, runNS, route)
	hold := clause5Hold(r.Gateway, runNS, route, above, foreign, ferr)
	switch {
	case ferr != nil:
		return hold, fmt.Errorf("clause 5: %w", ferr)
	case above.unreadable != "":
		return hold, fmt.Errorf("clause 5: %s", above.unreadable)
	}
	return hold, nil
}

// clause5Hold is clause 5's verdict from its reads: nil when no hold stands,
// or EvalWaitingForAuth naming every cause that does. The message is built
// from the hold's stable causes — the policies' names, the Gateway's
// allowedListeners, and the class of a read error — and never from an API
// error's raw text, so the value is the same on every pass a cause stands and
// blocked is rewritten only when the hold changes (§1.1, "`blocked`: one
// slot, one computation").
func clause5Hold(gw GatewayConfig, runNS, route string, above gatewayAuth, foreign []string,
	ferr error) *assaydv1alpha1.EvalBlocked {
	var causes []string
	if above.gatewayErr != nil {
		causes = append(causes, fmt.Sprintf("Gateway %s/%s could not be read (%s), so nothing on it can "+
			"be ruled out", gw.Namespace, gw.Name, readErrorClass(above.gatewayErr)))
	}
	if above.listErr != nil {
		causes = append(causes, fmt.Sprintf("the AgentgatewayPolicies in %s could not be listed (%s), so "+
			"none on the Gateway can be ruled out", gw.Namespace, readErrorClass(above.listErr)))
	}
	if len(above.policies) > 0 {
		// With the Gateway unread its listeners are unknown, and every policy
		// that targets it was counted whatever its sectionName or selector.
		where := "on a listener that can take this Agent's traffic"
		if above.gatewayErr != nil {
			where = "whose listeners could not be read, so no sectionName or selector is taken as " +
				"keeping it off this Agent's traffic"
		}
		causes = append(causes, fmt.Sprintf("the AgentgatewayPolicy %s targets Gateway %s/%s %s, and "+
			"could answer an anonymous request itself", strings.Join(above.policies, ", "), gw.Namespace,
			gw.Name, where))
	}
	if above.listeners != "" {
		causes = append(causes, fmt.Sprintf("Gateway %s/%s admits ListenerSets (spec.allowedListeners."+
			"namespaces.from: %s), whose listeners this operator does not inspect", gw.Namespace, gw.Name,
			above.listeners))
	}
	if ferr != nil {
		causes = append(causes, fmt.Sprintf("the AgentgatewayPolicies in %s could not be listed to find "+
			"a foreign traffic policy on %s/%s (%s), so none can be ruled out", runNS, runNS, route,
			readErrorClass(ferr)))
	}
	if len(foreign) > 0 {
		causes = append(causes, fmt.Sprintf("the traffic policy %s targets %s/%s and is not this "+
			"Agent's, so a 401 through the route says nothing about which policy answered",
			strings.Join(foreign, ", "), runNS, route))
	}
	if len(causes) == 0 {
		return nil
	}
	return &assaydv1alpha1.EvalBlocked{Reason: ReasonEvalWaitingForAuth, Message: "an -auth Lock that " +
		"can finish only through a promotion is recorded on this Agent, and the eval claims a case " +
		"beside it only while no Gateway-level or foreign traffic policy holds it (design 16 §1.1, the " +
		"exemption's clause 5). A hold stands: " + strings.Join(causes, "; and ") + holdClaimsTail}
}

// holdClaimsTail ends a hold's message on a pass that would otherwise claim:
// what the hold stops and what it does not.
const holdClaimsTail = ". No case is claimed until it clears; a case already in flight is not stopped, " +
	"and a Passed verdict already written still promotes"

// holdFailedTail is what the gate branch puts in holdClaimsTail's place when
// the pair has already failed: no case of it is claimed whether or not the
// hold clears, and what records the failure stands behind the hold — a Failed
// verdict, or, once #111's clear has taken the verdict, the pair's entry in
// status.eval.failed[]. The gate branch words it from the pass's own state, so
// the eval controller writes no second value when a verdict lands under a
// standing hold.
func holdFailedTail(behind string) string {
	return ". This pair has already failed, so no case of it is claimed whether or not the hold clears; " +
		behind + " stands behind this hold, and is shown again once the hold clears"
}

// readErrorClass names an API read error's class, which is stable from pass
// to pass where its text is not.
func readErrorClass(err error) string {
	switch {
	case apierrors.IsNotFound(err):
		return "NotFound"
	case apierrors.IsForbidden(err):
		return "Forbidden"
	case apierrors.IsUnauthorized(err):
		return "Unauthorized"
	case meta.IsNoMatchError(err):
		return "the kind is not served"
	case apierrors.IsTimeout(err) || apierrors.IsServerTimeout(err) || errors.Is(err, context.DeadlineExceeded):
		return "a timeout"
	case apierrors.IsTooManyRequests(err):
		return "throttled"
	case apierrors.IsInternalError(err) || apierrors.IsServiceUnavailable(err) || apierrors.IsUnexpectedServerError(err):
		return "a server error"
	}
	return "an error of another class"
}
