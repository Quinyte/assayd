// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"fmt"

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
		// Gateway API keys a RouteParentStatus by (parentRef, controllerName),
		// so a second controller may write its own entry for the SAME
		// parentRef. routeConverged matches on the ref alone, which is
		// survivable for a transaction's stage; here that entry is a CLEARING
		// signal, and another controller reporting Accepted=True at the current
		// generation would retract a true refusal.
		//
		// IT IS NOT FAIL-SAFE, and calling it that was wrong. The comparison
		// gates RAISING as well as clearing, so on a cluster whose agentgateway
		// controller is renamed this loop matches nothing, every route reads
		// unknown, and the route half raises nothing at all: A80's defect
		// stands, unreported, exactly as before this judgement existed.
		// agentgateway 1.5.0's chart exposes controllerName for running several
		// controllers, and assayd exposes no knob for it — GatewayConfig has no
		// such field and the chart's `gateway:` map none. Measured: one edit to
		// the constant and case 19 (a) fails with Ready=True on a refused
		// route. The fix that would not trade one for the other — telling "a
		// parent entry exists for our parentRef but none from our controller"
		// apart from "no entry at all" — adds vocabulary to an approved slice
		// and is recorded as OWED in A81, not built here.
		if string(p.ControllerName) != AgentgatewayControllerName {
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

// policyClause names WHICH of policyReport's broken answers fired, so that
// the message can say what the Gateway actually said rather than one sentence
// for all four (design 03 A83, the human's (B4) on 2026-09-23).
//
// It does NOT reach the condition REASON, which stays AuthPolicyNotAttached on
// every clause: (B1)'s condition set is kept, and D5(b) — whether the
// partly-valid clause deserves a reason of its own — is left open, because the
// claim store on status.auth is ONE BOOLEAN PER HALF and carries no clause, so
// a second reason could not be re-asserted on a pass that re-derives nothing
// (§3.3.3, §9 D5).
type policyClause int

const (
	// clauseUnknown is "no clause was derived on this pass": a holding or
	// unknown reading, and — the case that matters — a HELD report, which
	// restates a claim it cannot narrow.
	clauseUnknown policyClause = iota
	// clauseUnattached is the synthetic StatusSummary ancestor, or an explicit
	// Attached=False on the real Gateway ancestor. It is the only clause in
	// which the Gateway itself says the policy is attached to nothing, and it
	// is the one A81's message was written for.
	clauseUnattached
	// clauseRejected is Accepted=False: the Gateway refused the policy
	// outright, so none of it is in force. Nothing tried has produced it on
	// agentgateway 1.5.0 (A82), and the arm is live because the CRD asserts
	// the shape.
	clauseRejected
	// clausePartlyValid is Accepted=True with a reason other than Valid —
	// PartiallyValid on 1.5.0 — which is the ONE shape of this half A82
	// measured reachable with a byte-unchanged policy, and in which the
	// Gateway reports Attached=True and the route is measured still refusing.
	clausePartlyValid
)

// policyReport is §3.3.2's policy tuple in the same three answers: an explicit
// Accepted=False, an Accepted=True whose reason is not Valid, an
// Attached=False — each at the policy's current generation — or agentgateway's
// synthetic StatusSummary ancestor, is BROKEN. It also says WHICH of those
// fired, because they do not all mean the same thing and A81's one message for
// all four named a cause that was never checked in the only one an
// administrator reaches without editing anything assayd wrote (A82, A83).
//
// An EMPTY ancestor list is UNKNOWN here, where policyConverged calls it a
// failure. That difference is deliberate and §5 states it: a transaction's
// tuple must not pass on silence, and this judgement must not raise on it.
func policyReport(p *unstructured.Unstructured, gw GatewayConfig) (gatewayReport, policyClause, string) {
	if p == nil {
		return reportUnknown, clauseUnknown, ""
	}
	// THE ASYMMETRY WITH routeReport IS DELIBERATE AND IS STATED RATHER THAN
	// TIDIED: an ancestor carries a controllerName and this function does not
	// compare it, where routeReport now does. Making it would widen the
	// fail-open hole that comparison already opens (above) to the policy half
	// as well, on a cluster whose controller is renamed, for a case nobody has
	// seen: an ancestor list is keyed by ancestorRef, and a second controller
	// writing an ancestor for the assayd GATEWAY on a policy it does not own is
	// not a shape §3.3.2 records. It goes with A81's owed fix, not before it.
	where := fmt.Sprintf("policy %s/%s at generation %d", p.GetNamespace(), p.GetName(), p.GetGeneration())
	ancestors, _, _ := unstructured.NestedSlice(p.Object, "status", "ancestors")
	if len(ancestors) == 0 {
		return reportUnknown, clauseUnknown, ""
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
			// THE ONE EXCEPTION TO "at the object's current generation", and it
			// is stated in §3.3.3 rather than left here: the ancestor list is
			// rewritten WHOLE on every status write, so the presence of the
			// synthetic ancestor is a current fact about the object and not a
			// generation-stamped claim — there is no generation to compare it
			// against. §3.3.2 already calls its presence the signal. It is the
			// fail-safe direction on the fail-OPEN half: the last thing the
			// Gateway said is that this policy attached to nothing (A81).
			return reportBroken, clauseUnattached, where + ": it carries agentgateway's synthetic " +
				"StatusSummary ancestor, which is written when the policy attached to nothing (" +
				describeConditions(m) + ")"
		}
		if group == gatewayv1.GroupName && kind == "Gateway" && name == gw.Name && ns == gw.Namespace {
			ours = m
		}
	}
	if ours == nil {
		return reportUnknown, clauseUnknown, ""
	}
	// Both conditions are read BEFORE any of them is answered, where an
	// earlier cut returned on the first break it met. That is not tidying: the
	// loop reads Accepted first, so a policy the Gateway reports BOTH
	// partly-valid and Attached=False would have taken the partly-valid
	// clause — the weaker claim — and its message would have stopped short of
	// saying the route may be answering with no credential required. The
	// answer is reportBroken either way, so only the message moves, and it
	// moves towards the stronger claim (A83).
	var (
		known                          int
		unattached, rejected, unwanted string
	)
	for _, want := range []struct{ typ, reason string }{{"Accepted", "Valid"}, {"Attached", ""}} {
		c, ok := findUnstructuredCondition(ours, want.typ)
		if !ok || c.ObservedGeneration != p.GetGeneration() {
			continue
		}
		switch {
		case c.Status == metav1.ConditionFalse:
			why := fmt.Sprintf("%s: Gateway %s/%s reports %s=False, reason %s: %s",
				where, gw.Namespace, gw.Name, want.typ, c.Reason, c.Message)
			if want.typ == "Attached" {
				unattached = why
			} else {
				rejected = why
			}
		case want.reason != "" && c.Status == metav1.ConditionTrue && c.Reason != want.reason:
			// c.Message IS CARRIED, and dropping it was a defect this
			// amendment's own review caught: on the partly-valid clause the
			// REASON is `PartiallyValid` for every cause 1.5.0 has been
			// measured producing — a rejected key-ConfigMap entry, an
			// unparseable CEL expression, an extAuth Service that does not
			// exist — and the MESSAGE is the only field that says which
			// (`research/a80-policy-half-conformance-2026-09.md` rows 1, 7b,
			// 10). A condition that sends the reader to the wrong object is
			// rule 8 whichever half of the report withholds the right one.
			unwanted = fmt.Sprintf("%s: Gateway %s/%s reports %s=True with reason %s, not %s: %s",
				where, gw.Namespace, gw.Name, want.typ, c.Reason, want.reason, c.Message)
		default:
			// known counts the conditions that broke nothing. Its PLACEMENT in
			// this arm cannot change the answer — the ranking switch below
			// returns first on every path where a clause fired, so nothing
			// ever reads the counter there.
			//
			// THE COUNTER ITSELF IS LOAD-BEARING, and an earlier version of
			// this comment said otherwise by generalising from the placement.
			// `known == 2` is the ONLY path that reaches reportHolding, and
			// reportHolding is the only thing that clears a standing
			// AuthPolicyNotAttached: `_ = known` compiles and fails a unit row
			// at once (A83's second review, MINOR 1).
			known++
		}
	}
	// Strongest claim first: non-attachment is the one that says the route may
	// be unauthenticated, rejection the one that says none of the policy is in
	// force, partial acceptance the one that says neither.
	//
	// Only the pairs involving NON-ATTACHMENT can actually occur, and saying so
	// is the difference between a rank and a decoration (A83's review, MINOR
	// 1): `rejected` and `unwanted` are set by mutually exclusive arms of one
	// switch over one `Accepted` condition, so they are never both set and
	// their order here is unfalsifiable. The two reachable pairs each have a
	// unit row.
	switch {
	case unattached != "":
		return reportBroken, clauseUnattached, unattached
	case rejected != "":
		return reportBroken, clauseRejected, rejected
	case unwanted != "":
		return reportBroken, clausePartlyValid, unwanted
	}
	if known == 2 {
		return reportHolding, clauseUnknown, ""
	}
	return reportUnknown, clauseUnknown, ""
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
// report carries. A held message is REBUILT from the report's own text with one note
// appended, never appended to the stored one, and the markers are what the tests read.
// They were also what an earlier cut made possible, before that cut was found to be dead
// code:
// erroredNote interpolates the error, whose text moves — errStaleAgent names
// the resourceVersions that the errored pass's own write then bumps — so a
// suffix comparison matches nothing, and A80's change of the caller's
// non-transient arm to raiseIncomplete appends the write error AFTER the note,
// so it matches nothing even for a constant error.
//
// Measured on this suite before the fix: the message grew 240 bytes a pass on
// the transient arm and 475 on the non-transient one, and at pass 133 the API
// server refused EVERY status write for that Agent — "Too long: may not be
// more than 32768 bytes" — so observedGeneration, phase, Ready, Degraded and
// the card conditions all stopped moving, during exactly the incident this
// judgement exists to report. It heals on the first pass that neither errors
// nor holds, which rebuilds the message. The independent review reproduced it
// on its own fixture at +244 and +479 bytes a pass, saturating at pass 129;
// the few bytes of difference are the Agent's own name and namespace, which
// the message carries. §3.3.3 asks only that a held report keep its
// LastTransitionTime and its ObservedGeneration, and rebuilding keeps both,
// with one exception the design states: in the both-held state the claim goes
// through raiseIncomplete, which re-stamps the generation.
const heldMark = " | carried from the last pass that got a report at this object's generation"

// heldNote marks a report this pass could not re-derive because the Gateway
// has not reported at the object's current generation. It is NOT carriedNote,
// whose words say the pass returned before the -auth step: this pass reached
// the step and read the object.
//
// "READ THE OBJECT" IS WHY THIS NOTE IS NOT THE ONLY ONE. On the policy half
// A83 added a third held path, in which §5's precondition excluded the policy
// and the pass read no policy status at all — and this note, appended
// unconditionally, then put "the Gateway has not reported since" into the same
// message as a `why` saying nothing here will ever clear the claim. One
// message, two contradictory claims, and the second was never checked: the
// same rule-8 shape A83 exists to remove, surviving in the note after being
// removed from the lead (A83's second review, MAJOR 1). The route half has
// only the one path and keeps this note.
const heldNote = heldMark + "; the Gateway has not reported since (design 03 A80)"

// unjudgedNote is heldNote for the path that read NOTHING to re-derive from:
// the policy is not this Agent's by §3.2's name-and-label rule, or this build
// renders a digest status.auth does not record. It claims nothing about what
// the Gateway has or has not done, because this pass did not look.
const unjudgedNote = heldMark + "; this pass judged no policy, so nothing was re-read (design 03 A83)"

const erroredMark = " | carried across a pass whose -auth step could not complete"

// erroredNote marks a report held across a pass whose -auth step could not
// complete, which read nothing and says so, naming the error.
const erroredNote = erroredMark + "; the objects were not re-read (%v) (design 03 A80)"

// heldMessage is what a held report says: the report's own text, built fresh
// on this pass, plus this pass's note. One note, once, whatever the sequence of
// held, errored and early-return passes got here.
//
// **Rebuilding is the whole fix**, and an earlier version of this function also
// cut the fallback at any marker it carried — dead code, because every caller
// passes a freshly built constant that has never been through a note. The
// markers are kept: they are what the tests read, and erroredMark is the stable
// half of a note whose other half moves.
func heldMessage(fallback, note string) string { return fallback + note }

func routeRefusedMessage(why string) string {
	return "the assayd Gateway is not accepting this Agent's serving route, so nothing reaches this " +
		"Agent through the gateway: " + why + ". The route is NOT withdrawn and its backendRefs are " +
		"not stripped: the Gateway is already refusing to carry it, and stripping it would enter a " +
		"Create whose Publishing waits on an anonymous 401 through a route the Gateway will not " +
		"accept. Check the assayd Gateway's listener and this route's parentRef (design 03 §3.3.3, §5)"
}

// policyBrokenTail is what is true of EVERY clause and of a held report,
// including one held over a pass that read no policy at all: nothing is
// touched, and the reader is told which condition this is not.
// "the hole is announced, not closed" is NOT in it, because a hole is what
// only two of the four clauses saw (A83).
const policyBrokenTail = ". Nothing is withdrawn, deleted or rewritten — doing so would hand " +
	"anyone who can edit the Gateway a switch that takes this Agent off the air. This is not " +
	"AuthPolicyMissing: the operator is not re-creating the policy (design 03 §3.3.3, §5)"

// policyJudgedNote is §5's precondition, stated as a FACT THIS PASS ESTABLISHED
// — so it is appended only where the pass actually established it.
//
// It used to live in the tail, and that was wrong on exactly the path A83
// added a lead for: a held claim can be re-asserted on a pass that read no
// policy at all, because `reassertServedPolicy` returns nothing for a policy
// that lost this Agent's UID and for one whose render digest no longer matches
// `status.auth` — the second of which every operator upgrade that changes
// `compiler.AuthPolicy` produces, permanently. Asserting there that the policy
// "is present, carries this Agent's UID and renders to appliedDigest" is a
// claim about an object the pass never looked at (A83's review, MAJOR 3).
const policyJudgedNote = ". The policy is present, carries this Agent's UID and renders to " +
	"status.auth.appliedDigest, which is what put it inside this judgement (§5)"

// announcedNotClosed is the clause-specific half of the tail, for the two
// clauses in which the Gateway's own report is consistent with the route
// answering unauthenticated.
const announcedNotClosed = ". The hole is announced, not closed"

// policyBrokenMessage says what the assayd Gateway ACTUALLY SAID about this
// Agent's <agent>-auth, which is two branchings and not one.
//
// It takes THE ROUTE'S READING, because the two halves fire together in A80's
// own measured incident: renaming the Gateway's listener detaches the route,
// and agentgateway then writes the synthetic StatusSummary ancestor on
// <agent>-auth, so the same pass reports Accepted=False AND a policy attached
// to nothing. Opening unconditionally with "accepted and SERVING" then
// announces a security incident — a route answering with no credential
// required — that the same pass has just refuted, when the real incident is
// that nothing reaches the agent at all (A81).
//
// And it takes THE CLAUSE, which is A83 and the human's (B4) of 2026-09-23.
// A81's single lead asserted non-attachment for all four of policyReport's
// broken answers. In the ONE of them A82 measured reachable with a
// byte-unchanged policy — Accepted=True with reason PartiallyValid, because an
// administrator put an entry the controller rejects in the labelled key
// ConfigMap — the Gateway reports Attached=True, "Attached to all targets",
// and the route is measured still refusing anonymous requests 401. So the lead
// named a cause that was never checked and drove every served API-key Agent in
// the run namespace, which shares one key source, to Ready=False and Degraded
// with its authentication intact: AGENTS.md rule 8, one clause over from the
// one A81 already fixed with routeOK.
//
// **This is a CONTROL-PLANE fix and the human took it knowing so.** It reads
// the Gateway's report and makes no request, so it cannot tell a PartiallyValid
// that left authentication working — as the measured one does — from a future
// translation failure that reported the same way while disabling it. The
// message therefore does not claim authentication is intact; it says the
// judgement did not check. (B2)'s re-probe is the mechanism that could, and it
// was offered and not taken (§9 D5, §11 A82/A83).
//
// The REASON does not branch. AuthPolicyNotAttached stays on both conditions
// for every clause, which is (B1) kept, and D5(b) stays open: see policyClause.
// judged says whether THIS PASS read a policy that passed §5's precondition.
// It is true on every raised clause by construction — `out.judgePolicy` is set
// only where the three guards passed — and false on a held claim whose pass
// read no policy, which is what `policyJudgedNote` exists to keep honest.
func policyBrokenMessage(clause policyClause, why string, routeOK, judged bool) string {
	var lead, consequence string
	switch clause {
	case clauseUnattached:
		consequence = announcedNotClosed
		lead = "this Agent's route is accepted and SERVING while the assayd Gateway reports that it " +
			"does not attach the <agent>-auth policy, so the route may be answering with no credential " +
			"required: "
		if !routeOK {
			lead = "the assayd Gateway reports that it does not attach this Agent's <agent>-auth " +
				"policy. Whether the route is answering with no credential required depends on the " +
				"route, and THIS PASS DID NOT READ IT AS ACCEPTED: PolicyApplyIncomplete names the " +
				"route's own reading first, and if it says the route is refused then nothing is " +
				"reaching this Agent at all and this half is the smaller of the two problems: "
		}
	case clauseRejected:
		consequence = announcedNotClosed
		lead = "this Agent's route is accepted and SERVING while the assayd Gateway reports that it " +
			"REJECTED the <agent>-auth policy outright, so none of this Agent's authentication or " +
			"authorization is in force at the gateway and the route may be answering with no " +
			"credential required: "
		if !routeOK {
			lead = "the assayd Gateway reports that it REJECTED this Agent's <agent>-auth policy " +
				"outright, so none of this Agent's authentication or authorization is in force at " +
				"the gateway. Whether the route is answering with no credential required depends on " +
				"the route, and THIS PASS DID NOT READ IT AS ACCEPTED: PolicyApplyIncomplete names " +
				"the route's own reading first, and if it says the route is refused then nothing is " +
				"reaching this Agent at all and this half is the smaller of the two problems: "
		}
	case clausePartlyValid:
		// No routeOK branch, and its absence is the point rather than an
		// omission: this lead makes no claim about the route at all, because
		// the Gateway did not report non-attachment and the judgement issues
		// no request, so neither reading of the route would let it say more.
		lead = "the assayd Gateway ACCEPTED this Agent's <agent>-auth policy but not the whole of " +
			"it, so a rule the compiler wrote may have been dropped in translation and this Agent's " +
			"governance may be weaker than its spec asks for. The Gateway is NOT reporting the " +
			"policy unattached at the policy's current generation, and this judgement reads the " +
			"Gateway's report and makes no request of its own, so whether a credential is still " +
			"required is not established here either way. THE GATEWAY'S OWN REASON AND MESSAGE, " +
			"BELOW, NAME WHAT IT WOULD NOT TRANSLATE, and they are where to start: agentgateway " +
			"1.5.0 reports the same `PartiallyValid` for causes as different as a rejected entry " +
			"in the API-key ConfigMap an administrator wrote, an authorization expression that " +
			"does not parse, and an extAuth Service that does not exist. IF it is the key " +
			"ConfigMap, that one is shared by every Agent in this run namespace, so expect this " +
			"on all of them at once; the other causes are this policy's alone. Here is what the " +
			"Gateway said: "
	default:
		// A HELD report: this pass re-derived nothing, so it has no clause.
		// Restating A81's non-attachment lead here would re-enter the defect
		// one pass later, for a claim that may have been the partly-valid one.
		//
		// It says NOTHING about what the Gateway has or has not done since,
		// and nothing about what would clear it. Two paths reach this lead and
		// neither can support either claim: an errored pass read nothing at
		// all, and an unknown reading includes the case where §5's
		// precondition excluded the policy, in which case no later Gateway
		// report is ever read and "it stands until the Gateway reports again"
		// would be a promise nothing keeps (A83's review, MAJOR 3). The `why`
		// this pass passes in is what names its own situation.
		lead = "a claim from an earlier pass is standing, and this pass re-derived nothing, so it " +
			"cannot say WHICH of the Gateway's answers produced it — status.auth stores one flag " +
			"for this half and not the clause. This pass restates the claim and cannot narrow it: "
	}
	if judged {
		consequence += policyJudgedNote
	}
	return lead + why + consequence + policyBrokenTail
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
	routeRep, routeWhy := routeReport(out.judgeRoute, r.Gateway)
	// What the POLICY half is allowed to assert about the route, read once.
	// ONLY an explicit good tuple on this pass licenses "accepted and SERVING".
	// The false set is wider than "broken or held" and the difference is worth
	// naming: a plain UNKNOWN reading is in it too, with no claim standing —
	// so a promoting pass, and every pass on a cluster whose agentgateway
	// controller is renamed (routeReport then matches nothing), hedges the
	// policy half's message as well. That is a second reach of the fail-open
	// this comparison opens, in the message rather than in what is raised, and
	// it is stated in §3.3.3 rather than left to be discovered (A81).
	routeOK := routeRep == reportHolding
	switch rep, why := routeRep, routeWhy; rep {
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
	switch rep, clause, why := policyReport(out.judgePolicy, r.Gateway); rep {
	case reportBroken:
		claims.policyUnattached = true
		// judged is true by construction: out.judgePolicy is set only where
		// §5's three guards passed.
		msg := policyBrokenMessage(clause, why, routeOK, true)
		raiseIncomplete(conds, ReasonAuthPolicyNotAttached, msg)
		appendGovernance(conds, ReasonAuthPolicyNotAttached, msg)
		withholdInOrder(out, ReasonAuthPolicyNotAttached, msg)
		out.served = true
	case reportHolding:
		claims.policyUnattached = false
	default:
		if stored.policyUnattached {
			// clauseUnknown, and not the clause that raised it: the claim
			// store is one boolean and does not carry which answer the
			// Gateway gave (A83).
			//
			// TWO different unknowns reach here and they are not the same
			// situation, which the held message must not blur: the Gateway has
			// gone quiet at this policy's generation, or §5's precondition
			// took the policy out of the judgement altogether — the policy
			// lost this Agent's UID, or this operator renders a digest
			// status.auth does not record, which an upgrade produces
			// PERMANENTLY. In the second the operator reads no policy status
			// at all, so no Gateway report will ever clear the claim, and a
			// message saying it waits on one is a promise nothing keeps
			// (A83's review, MAJOR 3).
			judged := out.judgePolicy != nil
			why := "this pass did not judge a policy at all: the one at this Agent's -auth name " +
				"is not this Agent's by the name-and-label rule, or it renders to a digest " +
				"status.auth.appliedDigest does not record, so §5's precondition excludes it and " +
				"NOTHING here will clear this claim until that changes — see GovernanceSkipped " +
				"and any ForeignTrafficPolicy report"
			// THE NOTE MOVES WITH THE WHY, and leaving it behind was the
			// defect: heldNote says "the Gateway has not reported since",
			// which on this path contradicts the why in the same message and
			// was never checked (A83's second review, MAJOR 1).
			note := unjudgedNote
			if judged {
				why = "the Gateway has not reported on it at its current generation since"
				note = heldNote
			}
			held := policyBrokenMessage(clauseUnknown, why, routeOK, judged)
			holdIncomplete(agent, conds, out, ReasonAuthPolicyNotAttached, held, note)
			holdGovernance(agent, conds, held, note)
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
		// judged is FALSE: this pass's -auth step errored, so it read no
		// policy and may assert nothing about one.
		held := policyBrokenMessage(clauseUnknown, "this pass could not re-read it", false, false)
		holdIncomplete(agent, conds, out, ReasonAuthPolicyNotAttached, held, note)
		holdGovernance(agent, conds, held, note)
	}
	// The claim store is NOT written back here, and the reason is worth stating
	// rather than leaving a line no test can pin (rule 5). `status` began as a
	// deep copy of stored status, so it already carries the flags; every path
	// that assigns status.auth WHOLESALE carries them across (refuseAdopt, the
	// J2 abandonment, enterLock over a refused Adopt); and the one that
	// deliberately drops them, recordServed, cannot be reached on a pass this
	// judgement runs on, because such a pass began with a transaction in the
	// slot and the gate excludes it. A write here would be a no-op, and a
	// no-op nothing can fail reads as load-bearing (A81).
}

// holdIncomplete re-asserts a standing PolicyApplyIncomplete claim this pass
// could not re-derive, and the withhold and served flag that make it withhold
// Ready.
//
// What is held is the reason's CLAIM, not the condition object, and that
// distinction is load-bearing: A80's two reasons sit at the END of
// incompleteOrder, so whenever another cause outranks one of them — and
// whenever the route half outranks the policy half, which is A80's own
// incident — the stored condition's REASON belongs to that other cause and a
// reason match finds nothing to restore. So the stored condition is put back WHOLE
// only when its reason is this one and nothing outranks it on this pass —
// keeping its ObservedGeneration, and its LastTransitionTime through merge,
// which is what makes design 10's duration rule meaningful when it lands.
// Otherwise the claim is re-appended to whatever reason this pass derived.
func holdIncomplete(agent *assaydv1alpha1.Agent, conds *conditionSet, out *gatewayOutcome,
	reason, fallback, note string) {
	// ONE EXCEPTION to "keeps its ObservedGeneration", stated rather than
	// quietly untrue: when this pass has already asserted the condition — both
	// halves held, or another cause standing — the claim goes through
	// raiseIncomplete below, whose conds.set stamps THIS pass's generation. The
	// LastTransitionTime still survives, through merge, which is what design
	// 10's duration rule reads; the generation is the weaker half of the claim
	// and is lost only in the composed state (A81).
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
// GovernanceSkipped whose reason IS AuthPolicyNotAttached is re-asserted from
// the stored condition, on seedStoredAbove's precedent; only a claim it carries
// as a fragment under another reason is re-appended.
//
// TWO things are at stake, and they are different sizes, which is worth
// separating because one of them is not reachable here:
//
//   - the REASON. A rule that appends to whatever the pass derived, keeping
//     that reason, leaves the Agent reading GovernanceSkipped=False, "auth
//     verified on one replica", while its -auth is attached to nothing. That
//     is fail-open and it is what case 19 (k)'s fourth half kills. It is NOT
//     what appendGovernance does below: with neither ForeignTrafficPolicy nor
//     GatewayAuthPolicy standing, appendGovernance makes AuthPolicyNotAttached
//     the reason, so the status and the reason come out the same either way.
//   - what is left is ObservedGeneration, which is the whole remaining point
//     of carrying rather than setting: conds.set stamps the generation of THIS
//     pass, and this pass observed nothing. carry keeps the generation that
//     did. Case 19 (k)'s fourth half asserts it, because without that
//     assertion deleting this branch leaves the suite green (A81).
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
