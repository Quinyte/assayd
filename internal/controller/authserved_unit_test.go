// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
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
		name string
		p    *unstructured.Unstructured
		want gatewayReport
	}{
		{"accepted and attached at the current generation", policy(2, ours(
			cond("Accepted", "True", "Valid", 2), cond("Attached", "True", "Attached", 2))), reportHolding},
		{"Attached=False at the current generation", policy(2, ours(
			cond("Accepted", "True", "Valid", 2), cond("Attached", "False", "NotAttached", 2))), reportBroken},
		{"Accepted=False at the current generation", policy(2, ours(
			cond("Accepted", "False", "Invalid", 2), cond("Attached", "True", "Attached", 2))), reportBroken},
		{"Accepted=True with a reason other than Valid", policy(2, ours(
			cond("Accepted", "True", "Translated", 2), cond("Attached", "True", "Attached", 2))), reportBroken},
		{"the synthetic StatusSummary ancestor", policy(2, summary), reportBroken},
		// THE ONE EXCEPTION to "at the object's current generation", and it is
		// deliberate and stated in §3.3.3: the ancestor list is rewritten whole
		// on every status write, so there is no generation to compare the
		// synthetic ancestor's PRESENCE against, and §3.3.2 already calls that
		// presence the signal. Fail-safe on the fail-OPEN half (A81, the
		// review's MINOR 3).
		{"the StatusSummary ancestor is not generation-gated", policy(5, summary), reportBroken},
		{"a failure reported a generation behind", policy(3, ours(
			cond("Accepted", "True", "Valid", 2), cond("Attached", "False", "NotAttached", 2))), reportUnknown},
		{"an empty ancestor list", policy(2), reportUnknown},
		{"no ancestor is the assayd Gateway", policy(2, map[string]any{
			"ancestorRef": map[string]any{"group": gatewayv1.GroupName, "kind": "Gateway",
				"name": "other", "namespace": "elsewhere"}}), reportUnknown},
		{"no policy at all", nil, reportUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, why := policyReport(tc.p, gw)
			if got != tc.want {
				t.Errorf("policyReport = %v, want %v (%s)", got, tc.want, why)
			}
			if tc.want == reportBroken && why == "" {
				t.Error("a broken report names nothing")
			}
		})
	}
}

// What seedStoredAbove's A80 block seeds and what the -auth step withdraws are
// the same set in the direction that fails silently: a reason seeded and not
// withdrawn survives the pass that re-derives it and NEVER CLEARS. The converse
// does not hold and a80Carried does not claim it — GatewayAuthPolicy and
// ForeignTrafficPolicy are withdrawn too, and are seeded by their own blocks
// (A81, the review's MINOR 8).
func TestSeedAndWithdrawAreTheSameSet(t *testing.T) {
	for _, r := range []string{ReasonServingRouteNotAccepted, ReasonAuthPolicyNotAttached} {
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

// A80's two reasons sit at the END of incompleteOrder, after GatewayAuthPolicy.
// Neither can stand beside a transaction's reason, and AuthPolicyNotAttached
// cannot stand beside AuthPolicyMissing, which needs the policy to be absent —
// so the only states in which these ranks are exercised are beside
// ForeignTrafficPolicy and W1's GatewayAuthPolicy, which both keep the reason.
func TestA80SReasonsRankLast(t *testing.T) {
	want := []string{ReasonAuthPolicyMissing, ReasonForeignTrafficPolicy, ReasonGatewayAuthPolicy,
		ReasonAuthPolicyNotAttached, ReasonServingRouteNotAccepted}
	if len(incompleteOrder) != len(want) {
		t.Fatalf("incompleteOrder is %v, want %v", incompleteOrder, want)
	}
	for i := range want {
		if incompleteOrder[i] != want[i] {
			t.Fatalf("incompleteOrder is %v, want %v", incompleteOrder, want)
		}
	}
}
