// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
)

// Design 03 §8.1 case 19's unit table, beside the envtest rows and mirroring
// TestTheRouteTupleHoldsOnlyWhenEveryPartDoes: the served judgement's route
// predicate has THREE answers, not two — accepted, refused at the current
// generation, and NOT REPORTED at the current generation.
//
// The third is the whole of the human's (A1). routeConverged folds it into the
// second, which is right for a transaction's stage and would report a served
// Agent Degraded on every rollout, because every route write bumps the
// generation and the Gateway's report necessarily lags it.
//
// Mutation: collapse the third answer into the second. It compiles, and this
// table must fail.
func TestTheServedRouteReportHasThreeAnswers(t *testing.T) {
	gw := GatewayConfig{Name: "assayd", Namespace: "assayd-gateway"}
	route := func(gen int64, parents ...gatewayv1.RouteParentStatus) *gatewayv1.HTTPRoute {
		return &gatewayv1.HTTPRoute{
			ObjectMeta: metav1.ObjectMeta{Name: "a-serving", Namespace: "assayd-run-x", Generation: gen},
			Status:     gatewayv1.HTTPRouteStatus{RouteStatus: gatewayv1.RouteStatus{Parents: parents}},
		}
	}
	ours := func(conds ...metav1.Condition) gatewayv1.RouteParentStatus {
		ns := gatewayv1.Namespace("assayd-gateway")
		return gatewayv1.RouteParentStatus{
			ParentRef:      gatewayv1.ParentReference{Name: "assayd", Namespace: &ns},
			ControllerName: "agentgateway.dev/agentgateway", Conditions: conds}
	}
	cond := func(typ string, st metav1.ConditionStatus, gen int64) metav1.Condition {
		return metav1.Condition{Type: typ, Status: st, Reason: typ, Message: typ, ObservedGeneration: gen}
	}

	for _, tc := range []struct {
		name string
		rt   *gatewayv1.HTTPRoute
		want gatewayReport
	}{
		{"accepted at the current generation", route(3, ours(
			cond("Accepted", metav1.ConditionTrue, 3), cond("ResolvedRefs", metav1.ConditionTrue, 3))),
			reportHolding},
		{"Accepted=False at the current generation", route(3, ours(
			cond("Accepted", metav1.ConditionFalse, 3), cond("ResolvedRefs", metav1.ConditionTrue, 3))),
			reportBroken},
		{"ResolvedRefs=False at the current generation", route(3, ours(
			cond("Accepted", metav1.ConditionTrue, 3), cond("ResolvedRefs", metav1.ConditionFalse, 3))),
			reportBroken},
		{"a refusal reported a generation behind", route(4, ours(
			cond("Accepted", metav1.ConditionFalse, 3), cond("ResolvedRefs", metav1.ConditionTrue, 3))),
			reportUnknown},
		{"an acceptance reported a generation behind", route(4, ours(
			cond("Accepted", metav1.ConditionTrue, 3), cond("ResolvedRefs", metav1.ConditionTrue, 3))),
			reportUnknown},
		{"only one leg reported at the current generation", route(3, ours(
			cond("Accepted", metav1.ConditionTrue, 3))), reportUnknown},
		{"no entry for the assayd Gateway", route(3), reportUnknown},
		// Gateway API keys a RouteParentStatus by (parentRef, controllerName),
		// so a second controller can write its own entry for the same
		// parentRef. Here that entry would CLEAR a true refusal, which
		// routeConverged's ref-only match cannot do (A81, the review's MINOR
		// 4).
		{"another controller's Accepted=True for the same parentRef does not clear", route(3,
			func() gatewayv1.RouteParentStatus {
				p := ours(cond("Accepted", metav1.ConditionTrue, 3),
					cond("ResolvedRefs", metav1.ConditionTrue, 3))
				p.ControllerName = "example.com/some-other-controller"
				return p
			}()), reportUnknown},
		{"another controller's Accepted=False for the same parentRef does not raise", route(3,
			func() gatewayv1.RouteParentStatus {
				p := ours(cond("Accepted", metav1.ConditionFalse, 3),
					cond("ResolvedRefs", metav1.ConditionTrue, 3))
				p.ControllerName = "example.com/some-other-controller"
				return p
			}()), reportUnknown},
		{"another Gateway's entry only", route(3, func() gatewayv1.RouteParentStatus {
			p := ours(cond("Accepted", metav1.ConditionFalse, 3), cond("ResolvedRefs", metav1.ConditionFalse, 3))
			p.ParentRef.Name = "someone-else"
			return p
		}()), reportUnknown},
		{"no route at all", nil, reportUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, why := routeReport(tc.rt, gw)
			if got != tc.want {
				t.Errorf("routeReport = %v, want %v (%s)", got, tc.want, why)
			}
			if tc.want == reportBroken && why == "" {
				t.Error("a broken report names nothing; the condition, its reason and its message " +
					"are what a reader is sent to (design 03 §5)")
			}
		})
	}
}

// The policy predicate's three answers. An EMPTY ancestor list is UNKNOWN
// here, where policyConverged calls it a failure: §5 says this judgement
// deliberately does not, because raising on silence is how a report nobody can
// act on gets written.
func TestTheServedPolicyReportHasThreeAnswers(t *testing.T) {
	gw := GatewayConfig{Name: "assayd", Namespace: "assayd-gateway"}
	policy := func(gen int64, ancestors ...any) *unstructured.Unstructured {
		p := NewAgentgatewayPolicy()
		p.SetName("a-auth")
		p.SetNamespace("assayd-run-x")
		p.SetGeneration(gen)
		p.Object["status"] = map[string]any{"ancestors": ancestors}
		return p
	}
	cond := func(typ, status, reason string, gen int64) map[string]any {
		return map[string]any{"type": typ, "status": status, "reason": reason, "message": reason,
			"observedGeneration": gen}
	}
	ours := func(conds ...any) any {
		return map[string]any{"ancestorRef": map[string]any{"group": gatewayv1.GroupName,
			"kind": "Gateway", "name": "assayd", "namespace": "assayd-gateway"}, "conditions": conds}
	}
	summary := any(map[string]any{"ancestorRef": map[string]any{"group": "agentgateway.dev",
		"kind": "StatusSummary", "name": "StatusSummary"}, "conditions": []any{}})

	for _, tc := range []struct {
		name   string
		p      *unstructured.Unstructured
		want   gatewayReport
		clause policyClause
	}{
		{"accepted and attached at the current generation", policy(2, ours(
			cond("Accepted", "True", "Valid", 2), cond("Attached", "True", "Attached", 2))), reportHolding, clauseUnknown},
		{"Attached=False at the current generation", policy(2, ours(
			cond("Accepted", "True", "Valid", 2), cond("Attached", "False", "NotAttached", 2))), reportBroken, clauseUnattached},
		{"Accepted=False at the current generation", policy(2, ours(
			cond("Accepted", "False", "Invalid", 2), cond("Attached", "True", "Attached", 2))), reportBroken, clauseRejected},
		{"Accepted=True with a reason other than Valid", policy(2, ours(
			cond("Accepted", "True", "Translated", 2), cond("Attached", "True", "Attached", 2))), reportBroken, clausePartlyValid},
		{"the synthetic StatusSummary ancestor", policy(2, summary), reportBroken, clauseUnattached},
		// A83's ranking, which is the whole of what splitting the message
		// changes about the ANSWER: two clauses can fire on one policy, the
		// report is broken either way, and the message must take the STRONGER
		// claim. The loop reads Accepted first, so before A83 each of these
		// would have named the weaker one and stopped short of saying the
		// route may be answering with no credential required.
		{"Attached=False beside a non-Valid Accepted outranks it", policy(2, ours(
			cond("Accepted", "True", "PartiallyValid", 2), cond("Attached", "False", "Pending", 2))),
			reportBroken, clauseUnattached},
		{"Attached=False outranks Accepted=False", policy(2, ours(
			cond("Accepted", "False", "Invalid", 2), cond("Attached", "False", "Pending", 2))),
			reportBroken, clauseUnattached},
		// The synthetic ancestor still short-circuits WHEREVER it appears,
		// which is D5(c) and is deliberately NOT decided by A83: the human
		// took (B4) for the message, and the fail-open alternative A82
		// records — short-circuit only when no real ancestor reports
		// Attached=True at the current generation — is still open (§9 D5).
		{"the synthetic ancestor still outranks a real ancestor that holds", policy(2, summary, ours(
			cond("Accepted", "True", "Valid", 2), cond("Attached", "True", "Attached", 2))),
			reportBroken, clauseUnattached},
		// THE ONE EXCEPTION to "at the object's current generation", and it is
		// deliberate and stated in §3.3.3: the ancestor list is rewritten whole
		// on every status write, so there is no generation to compare the
		// synthetic ancestor's PRESENCE against, and §3.3.2 already calls that
		// presence the signal. Fail-safe on the fail-OPEN half (A81, the
		// review's MINOR 3).
		{"the StatusSummary ancestor is not generation-gated", policy(5, summary), reportBroken, clauseUnattached},
		{"a failure reported a generation behind", policy(3, ours(
			cond("Accepted", "True", "Valid", 2), cond("Attached", "False", "NotAttached", 2))), reportUnknown, clauseUnknown},
		{"an empty ancestor list", policy(2), reportUnknown, clauseUnknown},
		{"no ancestor is the assayd Gateway", policy(2, map[string]any{
			"ancestorRef": map[string]any{"group": gatewayv1.GroupName, "kind": "Gateway",
				"name": "other", "namespace": "elsewhere"}}), reportUnknown, clauseUnknown},
		{"no policy at all", nil, reportUnknown, clauseUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, clause, why := policyReport(tc.p, gw)
			if got != tc.want {
				t.Errorf("policyReport = %v, want %v (%s)", got, tc.want, why)
			}
			if clause != tc.clause {
				t.Errorf("policyReport named clause %v, want %v; the clause is what the message "+
					"branches on, and the wrong one names a cause that was never checked "+
					"(design 03 A83, %s)", clause, tc.clause, why)
			}
			if tc.want == reportBroken && why == "" {
				t.Error("a broken report names nothing")
			}
		})
	}
}

// What the policy half may say about the route, on each route reading and
// stored route claim (design 03 §8.1 item 11). Only a route reason standing on
// the pass — a refusal read now or held from status.auth — puts a route reading
// in PolicyApplyIncomplete for the hedge to point at; an unknown reading with
// no claim standing puts none there.
func TestThePolicyHalfSaysOnlyWhatWasReadOfTheRoute(t *testing.T) {
	for _, tc := range []struct {
		name    string
		rep     gatewayReport
		refused bool
		want    routeLead
	}{
		{"accepted", reportHolding, false, routeServing},
		{"accepted, with a stale claim the route half is about to clear", reportHolding, true, routeServing},
		{"refused on this pass", reportBroken, false, routeNamed},
		{"refused on this pass and before", reportBroken, true, routeNamed},
		{"unknown, a refusal held", reportUnknown, true, routeNamed},
		{"unknown, nothing standing", reportUnknown, false, routeUnread},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := servedRouteLead(tc.rep, servedClaims{routeRefused: tc.refused}); got != tc.want {
				t.Errorf("servedRouteLead = %d, want %d", got, tc.want)
			}
		})
	}
}

// The four leads A83 splits policyBrokenMessage into, each asserted by what it
// must NOT say as well as by what it must. The reason does not branch — (B1)
// is kept — so what a clause says is the only thing that separates it, and a
// test asserting only reasons would pass with A81's one lead for all four.
func TestThePolicyMessageNamesWhatTheGatewaySaid(t *testing.T) {
	for _, tc := range []struct {
		name          string
		clause        policyClause
		route         routeLead
		judged        bool
		says, saysNot []string
	}{
		{"unattached on an accepted route", clauseUnattached, routeServing, true,
			[]string{"does not attach", "no credential required", "announced, not closed"},
			[]string{"THIS PASS DID NOT READ IT AS ACCEPTED", "but not the whole of it"}},
		{"unattached beside a route reason that names the route", clauseUnattached, routeNamed, true,
			[]string{"does not attach", "THIS PASS DID NOT READ IT AS ACCEPTED",
				"PolicyApplyIncomplete also carries the route's own reading"},
			[]string{"route is accepted and SERVING", "reading at its current generation is unknown",
				"names the route's own reading first"}},
		// §8.1 item 11: on an UNKNOWN reading with no route claim standing,
		// nothing names a route reading, so the hedge must not send the reader
		// to one. It keeps "THIS PASS DID NOT READ IT AS ACCEPTED".
		{"unattached beside a route this pass read as unknown", clauseUnattached, routeUnread, true,
			[]string{"does not attach", "THIS PASS DID NOT READ IT AS ACCEPTED",
				"reading at its current generation is unknown", "announced, not closed",
				// The one controllerName assayd reads, which is what points an
				// operator on a renamed-controller cluster at the real cause.
				"controllerName agentgateway.dev/agentgateway, the only one assayd reads"},
			[]string{"route is accepted and SERVING", "names the route's own reading first"}},
		{"rejected outright on an accepted route", clauseRejected, routeServing, true,
			[]string{"REJECTED", "none of this Agent's authentication or authorization is in force",
				"announced, not closed"},
			[]string{"does not attach", "but not the whole of it"}},
		// The row A83's own review found missing, and it found it by DELETING
		// this lead's !routeOK block (since A85, `route != routeServing`) and
		// watching the whole suite stay green.
		// The arm is live — §5 keeps it for a release that starts producing
		// Accepted=False — and unpinned it reaches A81's MAJOR 2 verbatim:
		// "route is accepted and SERVING" on a pass that recorded the route
		// refused, with nothing able to fail on it (rule 5).
		{"rejected beside a route reason that names the route", clauseRejected, routeNamed, true,
			[]string{"REJECTED", "THIS PASS DID NOT READ IT AS ACCEPTED",
				"PolicyApplyIncomplete also carries the route's own reading"},
			[]string{"route is accepted and SERVING", "reading at its current generation is unknown",
				"names the route's own reading first"}},
		{"rejected beside a route this pass read as unknown", clauseRejected, routeUnread, true,
			[]string{"REJECTED", "THIS PASS DID NOT READ IT AS ACCEPTED",
				"reading at its current generation is unknown", "announced, not closed",
				// The one controllerName assayd reads, which is what points an
				// operator on a renamed-controller cluster at the real cause.
				"controllerName agentgateway.dev/agentgateway, the only one assayd reads"},
			[]string{"route is accepted and SERVING", "names the route's own reading first"}},
		// The shape A82 measured, and the one A83 exists for: the Gateway
		// says Attached=True and the route is measured refusing, so a lead
		// asserting non-attachment or an open route is rule 8. The ConfigMap
		// attribution is HEDGED, because 1.5.0 reports the same PartiallyValid
		// for three measured causes and only one of them is namespace-wide.
		{"accepted only in part", clausePartlyValid, routeServing, true,
			[]string{"but not the whole of it", "NOT reporting the policy unattached",
				"makes no request of its own", "IF it is the key ConfigMap",
				"shared by every Agent in this run namespace",
				"the other causes are this policy's alone"},
			[]string{"does not attach", "no credential required", "REJECTED",
				"announced, not closed", "the cause measured on agentgateway"}},
		// A held report has no clause to name, because the claim store is one
		// boolean. Restating the unattached lead here would re-enter the
		// defect one pass later for a claim that may have been partly-valid.
		//
		// THE LEAD says nothing about what the Gateway has done since or
		// about what would clear it. The NOTE the caller appends is a
		// separate string and is not scoped by this table: THREE held paths
		// reach this lead and two of them — the Gateway gone quiet at this
		// object's generation, and an errored step — do say it, correctly,
		// through heldNote and erroredNote. Only the third, where §5's
		// precondition excluded the policy, must not, and it gets
		// unjudgedNote. Saying "neither held path checked either" here was
		// wrong twice over: there are three, and the claim belongs to the
		// lead alone (A83's second review, MAJOR 1).
		{"held over a pass that still judged the policy", clauseUnknown, routeServing, true,
			[]string{"re-derived nothing", "restates the claim and cannot narrow it",
				"The policy is present"},
			[]string{"does not attach", "no credential required", "REJECTED",
				"but not the whole of it", "stands until the Gateway reports again",
				"has not reported at the policy's current generation since"}},
		// The path A83's review found asserting a precondition nothing on it
		// established: a claim held over a pass that read NO policy, which is
		// every pass after a foreign takeover of the -auth name and every pass
		// after an upgrade that renders a digest status.auth does not record —
		// permanently, in the second case.
		{"held over a pass that judged no policy", clauseUnknown, routeServing, false,
			[]string{"re-derived nothing", "restates the claim and cannot narrow it"},
			[]string{"The policy is present", "carries this Agent's UID",
				"stands until the Gateway reports again"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			msg := policyBrokenMessage(tc.clause, "WHY", tc.route, tc.judged)
			for _, s := range tc.says {
				if !strings.Contains(msg, s) {
					t.Errorf("the message does not say %q: %s", s, msg)
				}
			}
			for _, s := range tc.saysNot {
				if strings.Contains(msg, s) {
					t.Errorf("the message says %q, which this clause did not check: %s", s, msg)
				}
			}
			if !strings.Contains(msg, "WHY") {
				t.Errorf("the message drops the Gateway's own words: %s", msg)
			}
			if !strings.Contains(msg, "This is not AuthPolicyMissing") {
				t.Errorf("every clause tells the reader which condition this is not: %s", msg)
			}
		})
	}
}

// The Gateway's own MESSAGE reaches the condition on the partly-valid clause,
// and it is the only field that separates the three causes 1.5.0 has been
// measured reporting as `PartiallyValid`: a rejected key-ConfigMap entry, an
// authorization expression that does not parse, an extAuth Service that does
// not exist (`research/a80-policy-half-conformance-2026-09.md` rows 10, 1, 7b).
// A83's first cut formatted the REASON alone, so the condition withheld the
// one field the Gateway had written to name the cause while its own lead
// named a different one — rule 8, inside the amendment that exists to remove
// it, and found by A83's review.
//
// Mutation: drop c.Message from the reason-mismatch arm's format, and this
// must fail.
func TestThePartlyValidReportCarriesTheGatewaysMessage(t *testing.T) {
	gw := GatewayConfig{Name: "assayd", Namespace: "assayd-gateway"}
	p := NewAgentgatewayPolicy()
	p.SetName("a-auth")
	p.SetNamespace("assayd-run-x")
	p.SetGeneration(2)
	p.Object["status"] = map[string]any{"ancestors": []any{map[string]any{
		"ancestorRef": map[string]any{"group": gatewayv1.GroupName, "kind": "Gateway",
			"name": "assayd", "namespace": "assayd-gateway"},
		"conditions": []any{
			map[string]any{"type": "Accepted", "status": "True", "reason": "PartiallyValid",
				"message":            "authorization matchExpression is not a valid CEL expression",
				"observedGeneration": int64(2)},
			map[string]any{"type": "Attached", "status": "True", "reason": "Attached",
				"message": "Attached to all targets", "observedGeneration": int64(2)},
		},
	}}}
	rep, clause, why := policyReport(p, gw)
	if rep != reportBroken || clause != clausePartlyValid {
		t.Fatalf("policyReport = %v/%v, want broken/partly-valid (%s)", rep, clause, why)
	}
	if !strings.Contains(why, "authorization matchExpression is not a valid CEL expression") {
		t.Errorf("the report drops the Gateway's own message, which is the only field that says "+
			"WHICH translation failed; the reader is then sent to the key ConfigMap for a cause "+
			"that is not there: %s", why)
	}
}

// What seedStoredAbove's A80 block seeds and what the -auth step withdraws are
// the same set in the direction that fails silently: a reason seeded and not
// withdrawn survives the pass that re-derives it and NEVER CLEARS. The converse
// does not hold and a80Carried does not claim it — GatewayAuthPolicy and
// ForeignTrafficPolicy are withdrawn too, and are seeded by their own blocks
// (A81, the review's MINOR 8).
func TestSeedAndWithdrawAreTheSameSet(t *testing.T) {
	for _, r := range []string{ReasonServingRouteNotAccepted, ReasonAuthPolicyNotAttached,
		ReasonAPIKeySourceEmpty} {
		if !a80Carried(r) {
			t.Errorf("seedStoredAbove's A80 block does not seed %s, so a pass that returns before "+
				"the -auth step drops the report and resets design 10's clock", r)
		}
		if !carriedReason(r) {
			t.Errorf("%s is seeded and NOT withdrawn: it would survive the pass that re-derives it "+
				"and never clear, which is the fail-open direction", r)
		}
	}
	for _, r := range []string{ReasonGatewayAuthPolicy, ReasonForeignTrafficPolicy} {
		if a80Carried(r) {
			t.Errorf("A80's block seeds %s, which has a block of its own; it would be carried twice", r)
		}
		if !carriedReason(r) {
			t.Errorf("%s is no longer withdrawn by the -auth step, which A75 requires", r)
		}
	}
	for _, r := range []string{ReasonAuthPolicyMissing, ReasonAuthLockUnverified,
		ReasonAuthEnforcementUnverified, ReasonGoverned} {
		if carriedReason(r) || a80Carried(r) {
			t.Errorf("%s is carried across a pass that re-read nothing; only the reasons the -auth "+
				"step alone derives may be", r)
		}
	}
}

// The ORDER, asserted through raiseIncomplete — which is what a reader sees —
// rather than by comparing the slice to a copy of itself. A literal copy is a
// change-detector: it fails on any edit and proves nothing about a wrong one,
// because the "want" moves with the code (A81, the review's MINOR 9).
//
// The row that matters most is the last: in A80's own measured incident both
// halves fire on one pass, and the route half must LEAD, because an Agent
// nothing can reach is the cause to name first and the policy half's own text
// would otherwise announce a security incident the same pass refuted.
func TestPolicyApplyIncompleteNamesTheOutrankingCauseFirst(t *testing.T) {
	for _, tc := range []struct {
		name          string
		first, then   string
		wantReason    string
		wantInMessage string
	}{
		{"a missing policy outranks a foreign one", ReasonForeignTrafficPolicy,
			ReasonAuthPolicyMissing, ReasonAuthPolicyMissing, ReasonForeignTrafficPolicy},
		{"a foreign policy outranks a Gateway-level one", ReasonGatewayAuthPolicy,
			ReasonForeignTrafficPolicy, ReasonForeignTrafficPolicy, ReasonGatewayAuthPolicy},
		{"a Gateway-level policy outranks a refused route", ReasonServingRouteNotAccepted,
			ReasonGatewayAuthPolicy, ReasonGatewayAuthPolicy, ReasonServingRouteNotAccepted},
		{"a refused route outranks an unattached policy", ReasonAuthPolicyNotAttached,
			ReasonServingRouteNotAccepted, ReasonServingRouteNotAccepted, ReasonAuthPolicyNotAttached},
		{"an unattached policy does not outrank a refused route", ReasonServingRouteNotAccepted,
			ReasonAuthPolicyNotAttached, ReasonServingRouteNotAccepted, ReasonAuthPolicyNotAttached},
		// A86: the empty key source ranks LAST, below all four reasons that
		// can stand beside it, raised before or after it.
		{"a broken policy tuple outranks an empty key source", ReasonAPIKeySourceEmpty,
			ReasonAuthPolicyNotAttached, ReasonAuthPolicyNotAttached, ReasonAPIKeySourceEmpty},
		{"an empty key source does not outrank a refused route", ReasonServingRouteNotAccepted,
			ReasonAPIKeySourceEmpty, ReasonServingRouteNotAccepted, ReasonAPIKeySourceEmpty},
		{"an empty key source does not outrank a Gateway-level policy", ReasonGatewayAuthPolicy,
			ReasonAPIKeySourceEmpty, ReasonGatewayAuthPolicy, ReasonAPIKeySourceEmpty},
		{"a foreign policy outranks an empty key source", ReasonAPIKeySourceEmpty,
			ReasonForeignTrafficPolicy, ReasonForeignTrafficPolicy, ReasonAPIKeySourceEmpty},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conds := newConditionSet(1)
			raiseIncomplete(conds, tc.first, tc.first+" happened")
			raiseIncomplete(conds, tc.then, tc.then+" happened")
			c, ok := conds.get(assaydv1alpha1.CondPolicyApplyIncomplete)
			if !ok {
				t.Fatal("PolicyApplyIncomplete was not raised at all")
			}
			if c.Reason != tc.wantReason {
				t.Errorf("the condition reads %s, want %s: the outranking cause is what a reader is "+
					"sent to", c.Reason, tc.wantReason)
			}
			if !strings.Contains(c.Message, tc.wantInMessage) {
				t.Errorf("the message drops %s: every cause that stands must be named (%s)",
					tc.wantInMessage, c.Message)
			}
		})
	}
}
