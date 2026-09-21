// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
)

// Design 03 A80: a served Agent's route and its `<agent>-auth` are judged
// again after `Served`.
//
// The defect this answers was measured on k3d on 2026-09-14: once
// status.auth.transaction holds nothing a stage advances, nothing re-reads the
// serving route's status — routeConverged is called only from inside `Create`
// and `Lock` — so an Agent that reached `Served` reports Ready=True, phase
// Ready and GovernanceSkipped=False for as long as it exists, whatever the
// Gateway says about its route. Renaming the assayd Gateway's listener makes
// the route report Accepted=False, reason NoMatchingParent, and the state does
// not heal, because the route's sectionName is a compile-time constant the
// operator re-asserts.
//
// The judgement adds no object, no watch, no requeue and no read: both objects
// are already read on the passes that already happen.

// gatewayReport is what the assayd Gateway's status says about one object, in
// the THREE answers this judgement needs rather than routeConverged's two.
// The human decided (A1) on 2026-09-21: only an explicit `False` at the
// object's current generation is a failure, and an unknown reading holds.
type gatewayReport int

const (
	// reportUnknown is no entry for the assayd Gateway, no condition of the
	// type, an empty ancestor list, or a condition whose observedGeneration is
	// behind the object's. It raises nothing AND CLEARS NOTHING. The stronger
	// reading is not implementable: every write bumps the object's generation
	// and the Gateway's report necessarily lags it, so a promotion would flip a
	// served Agent to Degraded, and a clearing rule would retract a true report
	// on every promotion.
	reportUnknown gatewayReport = iota
	// reportBroken is an explicit failure at the object's current generation.
	reportBroken
	// reportHolding is the tuple reported at the current generation and not
	// broken. Only this clears a standing report.
	reportHolding
)

// routeReport is §3.3.2's route tuple in A80's three answers: an explicit
// Accepted=False or ResolvedRefs=False in the assayd Gateway's own
// status.parents entry, at the route's current generation, is BROKEN; both legs
// reported there and neither False is HOLDING; anything else is UNKNOWN.
//
// It is deliberately not routeConverged, which folds "not reported at this
// generation" into "does not hold" — correct for a transaction's stage, and
// the fail-open reading here, because it would report a served Agent Degraded
// on every rollout.
func routeReport(rt *gatewayv1.HTTPRoute, gw GatewayConfig) (gatewayReport, string) {
	if rt == nil {
		return reportUnknown, ""
	}
	where := fmt.Sprintf("route %s/%s at generation %d", rt.Namespace, rt.Name, rt.Generation)
	for _, p := range rt.Status.Parents {
		ns := rt.Namespace
		if p.ParentRef.Namespace != nil {
			ns = string(*p.ParentRef.Namespace)
		}
		if string(p.ParentRef.Name) != gw.Name || ns != gw.Namespace {
			continue
		}
		known := 0
		for _, t := range []string{"Accepted", "ResolvedRefs"} {
			c := meta.FindStatusCondition(p.Conditions, t)
			if c == nil || c.ObservedGeneration != rt.Generation {
				continue
			}
			if c.Status == metav1.ConditionFalse {
				return reportBroken, fmt.Sprintf("%s: Gateway %s/%s reports %s=False, reason %s: %s",
					where, gw.Namespace, gw.Name, t, c.Reason, c.Message)
			}
			known++
		}
		if known == 2 {
			return reportHolding, ""
		}
		return reportUnknown, ""
	}
	return reportUnknown, ""
}

// policyReport is §3.3.2's policy tuple in the same three answers: an explicit
// Accepted=False, an Accepted=True whose reason is not Valid, an
// Attached=False — each at the policy's current generation — or agentgateway's
// synthetic StatusSummary ancestor, is BROKEN.
//
// An EMPTY ancestor list is UNKNOWN here, where policyConverged calls it a
// failure. That difference is deliberate and §5 states it: a transaction's
// tuple must not pass on silence, and this judgement must not raise on it.
func policyReport(p *unstructured.Unstructured, gw GatewayConfig) (gatewayReport, string) {
	if p == nil {
		return reportUnknown, ""
	}
	where := fmt.Sprintf("policy %s/%s at generation %d", p.GetNamespace(), p.GetName(), p.GetGeneration())
	ancestors, _, _ := unstructured.NestedSlice(p.Object, "status", "ancestors")
	if len(ancestors) == 0 {
		return reportUnknown, ""
	}
	var ours map[string]any
	for _, a := range ancestors {
		m, _ := a.(map[string]any)
		ref, _ := m["ancestorRef"].(map[string]any)
		group, _ := ref["group"].(string)
		kind, _ := ref["kind"].(string)
		name, _ := ref["name"].(string)
		ns, _ := ref["namespace"].(string)
		if group == "agentgateway.dev" && name == "StatusSummary" {
			// The ancestor list is rewritten whole on every status write, so
			// the presence of the synthetic ancestor is a current fact about
			// the object rather than a generation-stamped claim.
			return reportBroken, where + ": it carries agentgateway's synthetic StatusSummary " +
				"ancestor, which is written when the policy attached to nothing (" +
				describeConditions(m) + ")"
		}
		if group == gatewayv1.GroupName && kind == "Gateway" && name == gw.Name && ns == gw.Namespace {
			ours = m
		}
	}
	if ours == nil {
		return reportUnknown, ""
	}
	known := 0
	for _, want := range []struct{ typ, reason string }{{"Accepted", "Valid"}, {"Attached", ""}} {
		c, ok := findUnstructuredCondition(ours, want.typ)
		if !ok || c.ObservedGeneration != p.GetGeneration() {
			continue
		}
		switch {
		case c.Status == metav1.ConditionFalse:
			return reportBroken, fmt.Sprintf("%s: Gateway %s/%s reports %s=False, reason %s: %s",
				where, gw.Namespace, gw.Name, want.typ, c.Reason, c.Message)
		case want.reason != "" && c.Status == metav1.ConditionTrue && c.Reason != want.reason:
			return reportBroken, fmt.Sprintf("%s: Gateway %s/%s reports %s=True with reason %s, not %s",
				where, gw.Namespace, gw.Name, want.typ, c.Reason, want.reason)
		}
		known++
	}
	if known == 2 {
		return reportHolding, ""
	}
	return reportUnknown, ""
}

// servedClaims is A80's claim store: which of the two reports is standing.
// It lives on status.auth rather than in a condition message because the only
// other store would be a concatenated human-readable string whose " | "
// separator raiseIncomplete and reportAboveServed both reuse, so a claim's
// extent in it is ambiguous — and because in the ordinary case a standing
// claim is a FRAGMENT under another reason, which a reason match cannot find.
type servedClaims struct{ routeRefused, policyUnattached bool }

// storedClaims reads the claim store from STORED status, which must happen
// BEFORE the -auth step: refuseAdopt assigns status.auth wholesale on every
// pass of a refused `Adopt` — one of the three classes this judgement admits,
// and the one it calls least covered — so a judgement that read the flags
// where it uses them would read them wiped.
func storedClaims(agent *assaydv1alpha1.Agent) servedClaims {
	if a := agent.Status.Auth; a != nil {
		return servedClaims{routeRefused: a.RouteRefused, policyUnattached: a.PolicyUnattached}
	}
	return servedClaims{}
}

func (c servedClaims) writeTo(status *assaydv1alpha1.AgentStatus) {
	if status.Auth == nil {
		return
	}
	status.Auth.RouteRefused = c.routeRefused
	status.Auth.PolicyUnattached = c.policyUnattached
}

// judgesServed is A80's gate: a served Agent with no transaction in the slot,
// or with a refused `Adopt` in it.
//
// A transaction that runs stages has its own checks and its own reasons and
// must not be second-guessed here. A refused `Adopt` runs no stage, calls no
// routeConverged, sets no withhold and returns an empty outcome, and its
// record is permanent by design — so it is the state with the LEAST coverage,
// not the most, and A75's read skips it too. Only the route half applies to
// it: it has no `<agent>-auth`.
func judgesServed(status *assaydv1alpha1.AgentStatus) bool {
	auth := status.Auth
	if auth == nil {
		return false
	}
	if auth.Transaction == nil {
		return auth.Mode != ""
	}
	return auth.Transaction.Kind == TxAdopt
}

// heldMark and erroredMark are the STABLE prefixes of the two notes a held
// report carries. A held message is cut at whichever marker it already has and
// REBUILT, never appended to, and the marker is what makes that cut possible:
// erroredNote interpolates the error, whose text moves — errStaleAgent names
// the resourceVersions that the errored pass's own write then bumps — so a
// suffix comparison matches nothing, and A80's change of the caller's
// non-transient arm to raiseIncomplete appends the write error AFTER the note,
// so it matches nothing even for a constant error.
//
// Measured on this suite before the cut: the message grew 240 bytes a pass on
// the transient arm and 475 on the non-transient one, and at pass 133 the API
// server refused EVERY status write for that Agent — "Too long: may not be
// more than 32768 bytes" — so phase, Ready, Degraded, WorkloadUnavailable and
// the card conditions all froze, during exactly the incident this judgement
// exists to report, and it did not heal. §3.3.3 asks only that a held report
// keep its LastTransitionTime and its ObservedGeneration, and rebuilding keeps
// both.
const heldMark = " | carried from the last pass that got a report at this object's generation"

// heldNote marks a report this pass could not re-derive because the Gateway
// has not reported at the object's current generation. It is NOT carriedNote,
// whose words say the pass returned before the -auth step: this pass reached
// the step and read the object.
const heldNote = heldMark + "; the Gateway has not reported since (design 03 A80)"

const erroredMark = " | carried across a pass whose -auth step could not complete"

// erroredNote marks a report held across a pass whose -auth step could not
// complete, which read nothing and says so, naming the error.
const erroredNote = erroredMark + "; the objects were not re-read (%v) (design 03 A80)"

// heldMessage is what a held report says: the fallback, cut at any note a
// previous pass left, plus this pass's note. One note, once, whatever the
// sequence of held, errored and early-return passes that got here.
func heldMessage(fallback, note string) string {
	cut := len(fallback)
	for _, m := range []string{heldMark, erroredMark, carriedNote} {
		if i := strings.Index(fallback, m); i >= 0 && i < cut {
			cut = i
		}
	}
	return fallback[:cut] + note
}

func routeRefusedMessage(why string) string {
	return "the assayd Gateway is not accepting this Agent's serving route, so nothing reaches this " +
		"Agent through the gateway: " + why + ". The route is NOT withdrawn and its backendRefs are " +
		"not stripped: the Gateway is already refusing to carry it, and stripping it would enter a " +
		"Create whose Publishing waits on an anonymous 401 through a route the Gateway will not " +
		"accept. Check the assayd Gateway's listener and this route's parentRef (design 03 §3.3.3, §5)"
}

func policyUnattachedMessage(why string) string {
	return "this Agent's route is accepted and SERVING while the assayd Gateway reports that it does " +
		"not attach the <agent>-auth policy, so the route may be answering with no credential " +
		"required: " + why + ". Nothing is withdrawn, deleted or rewritten — doing so would hand " +
		"anyone who can edit the Gateway a switch that takes this Agent off the air — so the hole is " +
		"announced, not closed. This is not AuthPolicyMissing: the policy is present, carries this " +
		"Agent's UID and renders to status.auth.appliedDigest, and the operator is not re-creating " +
		"it (design 03 §3.3.3, §5)"
}

// judgeServed is A80's re-derivation, on a pass whose -auth step returned no
// error. It runs AFTER the step, beside reportForeign and reportAboveServed,
// and never inside it: inside or before, the transaction and served paths
// write PolicyApplyIncomplete with conds.set directly and would overwrite
// reason and message.
//
// It writes FIVE things, because omitting any one produces a status that
// contradicts this design: PolicyApplyIncomplete through raiseIncomplete;
// GovernanceSkipped, for the policy half only, through a composition of its
// own; gatewayOutcome.withhold, which is the only way Ready is ever taken back;
// gatewayOutcome.served, which is what makes withholdReady say Degraded rather
// than a bare phase Pending with Degraded actively CLEARED; and the claim
// store.
func (r *AgentReconciler) judgeServed(agent *assaydv1alpha1.Agent, status *assaydv1alpha1.AgentStatus,
	conds *conditionSet, out *gatewayOutcome, stored servedClaims) {
	if !r.Gateway.Enabled || !judgesServed(status) {
		return
	}
	claims := stored

	// The route half, fail-CLOSED: nothing reaches the agent. It applies to a
	// served apikey Agent, a served `auth: none` Agent and a refused `Adopt`,
	// all three of which reported Ready=True on a refused route before A81.
	switch rep, why := routeReport(out.judgeRoute, r.Gateway); rep {
	case reportBroken:
		claims.routeRefused = true
		msg := routeRefusedMessage(why)
		raiseIncomplete(conds, ReasonServingRouteNotAccepted, msg)
		withholdInOrder(out, ReasonServingRouteNotAccepted, msg)
		out.served = true
		// GovernanceSkipped is not moved AT ALL: it keeps the value the served
		// policy earned — CompilerUpgradeUnsupported for a refused Adopt, whose
		// message is the only one naming that Agent's way out — and gains a
		// note. Nothing reaches this Agent through the gateway, so no
		// governance claim is widened, and an availability failure must not
		// erase the record of which tier it runs in.
		noteGovernance(conds, ReasonServingRouteNotAccepted+": the assayd Gateway is not accepting "+
			"this Agent's route, so nothing reaches it through the gateway; PolicyApplyIncomplete "+
			"says so, and this condition keeps the value the served policy earned")
	case reportHolding:
		claims.routeRefused = false
	default:
		if stored.routeRefused {
			holdIncomplete(agent, conds, out, ReasonServingRouteNotAccepted,
				routeRefusedMessage("the Gateway has not reported on it since"), heldNote)
			noteGovernance(conds, ReasonServingRouteNotAccepted+": the assayd Gateway was last "+
				"reported not to be accepting this Agent's route, and has not reported at its "+
				"current generation since; PolicyApplyIncomplete says so, and this condition keeps "+
				"the value the served policy earned")
		}
	}

	// The policy half, fail-OPEN, and the one worth more: the route is accepted
	// and serving while its authentication is attached to nothing. Only a
	// served apikey Agent has a policy, and out.judgePolicy is set only where
	// §3.2's name-and-label rule and the appliedDigest comparison both passed.
	switch rep, why := policyReport(out.judgePolicy, r.Gateway); rep {
	case reportBroken:
		claims.policyUnattached = true
		msg := policyUnattachedMessage(why)
		raiseIncomplete(conds, ReasonAuthPolicyNotAttached, msg)
		appendGovernance(conds, ReasonAuthPolicyNotAttached, msg)
		withholdInOrder(out, ReasonAuthPolicyNotAttached, msg)
		out.served = true
	case reportHolding:
		claims.policyUnattached = false
	default:
		if stored.policyUnattached {
			held := policyUnattachedMessage("the Gateway has not reported on it since")
			holdIncomplete(agent, conds, out, ReasonAuthPolicyNotAttached, held, heldNote)
			holdGovernance(agent, conds, held, heldNote)
		}
	}
	claims.writeTo(status)
}

// holdServedJudgement is what a pass whose -auth step ERRORED does instead of
// the judgement. That scope governs RAISING, never clearing: a pass that
// errored re-derived nothing, so a standing report is re-asserted from stored
// status exactly as an unknown reading holds it — AND the restoration
// repopulates gatewayOutcome.withhold and served with it, because the
// condition alone does not withhold Ready. withholdReady is the only writer of
// Ready=False on this path and it reads those two fields, and the rollout
// switch two hundred lines above sets Ready=True on every pass. Without both
// halves this ships PolicyApplyIncomplete=ServingRouteNotAccepted beside
// Ready=True, phase Ready — the defect on a third consecutive path.
func (r *AgentReconciler) holdServedJudgement(agent *assaydv1alpha1.Agent,
	status *assaydv1alpha1.AgentStatus, conds *conditionSet, out *gatewayOutcome,
	stored servedClaims, err error) {
	if !r.Gateway.Enabled || !judgesServed(status) {
		return
	}
	note := fmt.Sprintf(erroredNote, err)
	if stored.routeRefused {
		holdIncomplete(agent, conds, out, ReasonServingRouteNotAccepted,
			routeRefusedMessage("this pass could not re-read it"), note)
	}
	if stored.policyUnattached {
		held := policyUnattachedMessage("this pass could not re-read it")
		holdIncomplete(agent, conds, out, ReasonAuthPolicyNotAttached, held, note)
		holdGovernance(agent, conds, held, note)
	}
	stored.writeTo(status)
}

// holdIncomplete re-asserts a standing PolicyApplyIncomplete claim this pass
// could not re-derive, and the withhold and served flag that make it withhold
// Ready.
//
// What is held is the reason's CLAIM, not the condition object, and that
// distinction is load-bearing: A80's two reasons sit at the END of
// incompleteOrder, so in the states where the appended ranks are exercised at
// all the stored condition's REASON belongs to another cause and a reason
// match finds nothing to restore. So the stored condition is put back WHOLE
// only when its reason is this one and nothing outranks it on this pass —
// keeping its ObservedGeneration, and its LastTransitionTime through merge,
// which is what makes design 10's duration rule meaningful when it lands.
// Otherwise the claim is re-appended to whatever reason this pass derived.
func holdIncomplete(agent *assaydv1alpha1.Agent, conds *conditionSet, out *gatewayOutcome,
	reason, fallback, note string) {
	msg := heldMessage(fallback, note)
	c := meta.FindStatusCondition(agent.Status.Conditions, string(assaydv1alpha1.CondPolicyApplyIncomplete))
	if _, set := conds.get(assaydv1alpha1.CondPolicyApplyIncomplete); !set && c != nil &&
		c.Status == metav1.ConditionTrue && c.Reason == reason {
		// The stored condition WHOLE means its LastTransitionTime (through
		// merge, both being True) and its ObservedGeneration. The MESSAGE is
		// rebuilt: carrying the stored one forward is what grew it without
		// bound.
		held := *c
		held.Message = msg
		conds.carry(held)
		withholdInOrder(out, reason, msg)
		out.served = true
		return
	}
	raiseIncomplete(conds, reason, msg)
	withholdInOrder(out, reason, msg)
	out.served = true
}

// holdGovernance is holdIncomplete's GovernanceSkipped half. raiseIncomplete
// cannot be the vehicle: it hardcodes PolicyApplyIncomplete at every one of its
// writes. And the condition is owned AND sticky, so a stored value always
// exists and assessGovernance has already rewritten the PASS's assertion — for
// a served API-key Agent, to False/AuthVerifiedOnOneReplica. So a stored
// GovernanceSkipped whose reason IS AuthPolicyNotAttached is re-asserted WHOLE,
// on seedStoredAbove's precedent; only a claim it carries as a fragment under
// another reason is re-appended.
//
// Getting this backwards is fail-open: a rule that only ever appends leaves the
// Agent reading GovernanceSkipped=False, "auth verified on one replica", while
// its -auth is attached to nothing.
func holdGovernance(agent *assaydv1alpha1.Agent, conds *conditionSet, fallback, note string) {
	msg := heldMessage(fallback, note)
	c := meta.FindStatusCondition(agent.Status.Conditions, string(assaydv1alpha1.CondGovernanceSkipped))
	if c != nil && c.Status == metav1.ConditionTrue && c.Reason == ReasonAuthPolicyNotAttached {
		// Whole means its times, not its message: see holdIncomplete.
		held := *c
		held.Message = msg
		conds.carry(held)
		return
	}
	appendGovernance(conds, ReasonAuthPolicyNotAttached, msg)
}

// appendGovernance is §3.2's reason order on GovernanceSkipped, by hand:
// nothing orders that condition and the last writer wins, so a standing
// ForeignTrafficPolicy or GatewayAuthPolicy keeps the REASON and the new claim
// is appended to its message; with neither standing, the claim is the reason.
// Without it this judgement, which runs last, would silently retire W1's
// GatewayAuthPolicy or §3.2's ForeignTrafficPolicy from the condition that
// records the tier.
func appendGovernance(conds *conditionSet, reason, msg string) {
	if c, ok := conds.get(assaydv1alpha1.CondGovernanceSkipped); ok &&
		c.Status == metav1.ConditionTrue &&
		(c.Reason == ReasonForeignTrafficPolicy || c.Reason == ReasonGatewayAuthPolicy) {
		conds.set(assaydv1alpha1.CondGovernanceSkipped, metav1.ConditionTrue, c.Reason,
			c.Message+" | "+reason+": "+msg)
		return
	}
	conds.set(assaydv1alpha1.CondGovernanceSkipped, metav1.ConditionTrue, reason, msg)
}

// noteGovernance appends a note to GovernanceSkipped, keeping its status and
// its reason. The route half writes a note and never moves the condition.
func noteGovernance(conds *conditionSet, note string) {
	if c, ok := conds.get(assaydv1alpha1.CondGovernanceSkipped); ok {
		conds.set(assaydv1alpha1.CondGovernanceSkipped, c.Status, c.Reason, c.Message+" | "+note)
	}
}
