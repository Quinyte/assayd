// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"strings"
	"testing"

	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
)

// Design 03 A77's two pure halves: the narrowed attribution, and the message
// §3.3.3's deadline table requires of a missing policy's Lock. The transaction
// behaviour they sit inside is §8.1 case 18's, in test/envtest/authgate_test.go;
// these pin the two functions against the states that suite cannot reach,
// because the slice's own emitter never writes a route with two backendRefs.

// routeNaming is a serving route whose one rule carries one backendRef per
// revision named — none, one, or two.
func routeNaming(agent string, revs ...string) *gatewayv1.HTTPRoute {
	rule := gatewayv1.HTTPRouteRule{}
	for _, rev := range revs {
		rule.BackendRefs = append(rule.BackendRefs, gatewayv1.HTTPBackendRef{
			BackendRef: gatewayv1.BackendRef{BackendObjectReference: gatewayv1.BackendObjectReference{
				Name: gatewayv1.ObjectName(WorkloadName(agent, rev))}}})
	}
	return &gatewayv1.HTTPRoute{Spec: gatewayv1.HTTPRouteSpec{
		Rules: []gatewayv1.HTTPRouteRule{rule}}}
}

// A77's narrowing of §3.3.3's attribution: a 401 is attributable only on the
// revision the route's SINGLE backendRef names. A route not carrying exactly
// one is unattributable whatever status.cards holds. Before A77 the function
// read every rule and every ref and returned on the first recorded digest, so
// a route momentarily carrying two backends was attributed on the carded one
// while the other answered.
func TestOnlyASingleBackendRefAttributesA401(t *testing.T) {
	agent := authTestAgent("apikey")
	status := &assaydv1alpha1.AgentStatus{Cards: []assaydv1alpha1.CardStatus{
		{Revision: "r1", Digest: "d1"}, {Revision: "r3", Digest: ""}}}

	if ok, _, why := cardAttributes(agent, status, routeNaming(agent.Name, "r1")); !ok {
		t.Errorf("a route naming one carded revision was refused: %s", why)
	}
	if ok, _, _ := cardAttributes(agent, status, routeNaming(agent.Name, "r2")); ok {
		t.Error("a route naming one UNcarded revision was attributed")
	}
	if ok, _, _ := cardAttributes(agent, status, routeNaming(agent.Name, "r3")); ok {
		t.Error("an entry with an empty digest — a failed fetch, not a card — attributed")
	}
	for _, tc := range []struct {
		name string
		rt   *gatewayv1.HTTPRoute
	}{
		{"two backendRefs, the first carded", routeNaming(agent.Name, "r1", "r2")},
		{"two backendRefs, the second carded", routeNaming(agent.Name, "r2", "r1")},
		{"no backendRef at all", routeNaming(agent.Name)},
	} {
		ok, _, why := cardAttributes(agent, status, tc.rt)
		if ok {
			t.Errorf("%s: attributed. A route not carrying exactly one backendRef is unattributable "+
				"whatever status.cards holds (design 03 §3.3.3, A77)", tc.name)
		}
		if !strings.Contains(why, "exactly one backendRef") {
			t.Errorf("%s: the reason does not say the route carries no single backendRef: %s",
				tc.name, why)
		}
	}
}

// §3.3.3's deadline table for a missing policy's Lock: the message names the
// revision the route's single backendRef names, says whether the route was
// re-pointed onto status.activeRevision or left as found, and, where neither
// that revision nor status.activeRevision has a recorded card, says the state
// is terminal, names both exits, and says which of them the state it reports
// admits (A77).
func TestTheMissingPolicyLockMessageNamesTheRouteAndItsExits(t *testing.T) {
	agent := authTestAgent("apikey")
	const runNS = "assayd-run-payments"
	status := func(carded ...string) *assaydv1alpha1.AgentStatus {
		st := &assaydv1alpha1.AgentStatus{ActiveRevision: "r2"}
		for _, rev := range carded {
			st.Cards = append(st.Cards, assaydv1alpha1.CardStatus{Revision: rev, Digest: "d"})
		}
		return st
	}
	// pending is the same status with a candidate held at weight 0. It is the
	// DESIRED revision then, so nothing is fetching the active revision's card.
	pending := func() *assaydv1alpha1.AgentStatus {
		st := status()
		st.CandidateRevision = "r3"
		return st
	}
	for _, tc := range []struct {
		name          string
		status        *assaydv1alpha1.AgentStatus
		rt            *gatewayv1.HTTPRoute
		repointed     bool
		held          bool
		foreign       []string
		want, refused []string
	}{
		{
			name: "re-pointed onto a carded active revision", status: status("r2"),
			rt: routeNaming(agent.Name, "r2"), repointed: true,
			want:    []string{"names revision r2", "re-pointed at status.activeRevision r2"},
			refused: []string{"TERMINAL", "left as found"},
		},
		{
			// A78: the ordinary way out of this state is the operator's own
			// card retry, not an administrator. A77 said "only an administrator
			// ends it", and a measurement of exactly this state disproved it.
			name:   "left as found on an uncarded revision beside an uncarded active one",
			status: status(), rt: routeNaming(agent.Name, "r1"),
			want: []string{"names revision r1", "left as found", "TERMINAL",
				"or for status.activeRevision",
				"ends without an administrator when status.activeRevision's own card records",
				"An administrator is needed where that card never validates",
				"Either exit applies",
				"removes " + runNS + "/pricer-serving", "the spec is reverted to the revision the route names"},
			refused: []string{"re-pointed at status.activeRevision", "only by an administrator",
				"Neither exit is needed here"},
		},
		{
			// The precondition is in the name because it is now in the CODE:
			// the arm is gated on there being no candidate, since a card is
			// fetched from a ready DESIRED revision alone (A60). A78's first
			// version named the retry in the message and checked nothing.
			name:   "the route names the active revision, with no candidate pending",
			status: status(), rt: routeNaming(agent.Name, "r2"),
			want: []string{"names revision r2", "TERMINAL", "which is also status.activeRevision",
				"ends without an administrator when status.activeRevision's own card records",
				"only while that revision has a replica available",
				"Neither exit is needed here"},
			refused: []string{"or for status.activeRevision", "Either exit applies",
				"only the first applies", "is the desired revision now"},
		},
		{
			// The candidate case, twice corrected. A78's first code said
			// "neither exit is needed" here, reading a retry that is not
			// running; its second said reverting the spec is "the one that
			// restarts the fetch", which was measured HARMFUL — the candidate
			// became available, its card recorded, it promoted, the promotion
			// fired the re-point and the Agent reached Served with nobody
			// acting, so a revert would have aborted the rollout that ended it.
			// The message names the promotion and warns against the revert.
			name:   "the route names the active revision while a candidate is pending",
			status: pending(), rt: routeNaming(agent.Name, "r2"),
			want: []string{"names revision r2", "TERMINAL", "which is also status.activeRevision",
				"Nothing is fetching status.activeRevision's card",
				"candidate revision r3 is the desired revision now",
				"This still ends with nobody acting if that candidate becomes available AND promotes",
				"its own card records, and the promotion fires the re-point",
				"Act only where that candidate can never promote",
				"reverting the spec aborts a rollout that would end this on its own",
				"The exit that works then is the second"},
			refused: []string{"ends without an administrator when", "Neither exit is needed here",
				"the operator is fetching ITS card", "is the one that restarts the fetch"},
		},
		{
			// Code MAJOR 2 of the combined pass: the candidate text must not
			// name an exit, because this arm has only one.
			name:   "two backendRefs while a candidate is pending",
			status: pending(), rt: routeNaming(agent.Name, "r1", "r2"),
			want: []string{"does not carry exactly one backendRef",
				"candidate revision r3 is the desired revision now",
				"Act only where that candidate can never promote",
				"only the first applies"},
			// Where the route names no single revision the revert exit does
			// not exist, so NOTHING in the message may recommend it — this
			// lists every arm's way of doing so, not just this arm's.
			refused: []string{"The exit that works then is the second", "the second exit is",
				"the second is the cheaper one", "Either exit applies",
				"Neither exit is needed here"},
		},
		{
			name:   "a foreign traffic policy stopped the pass above the re-point",
			status: status(), rt: routeNaming(agent.Name, "r2"), foreign: []string{"ns/rogue"},
			want: []string{"TERMINAL",
				"None of that ends it while the foreign traffic policy this pass found stands",
				"stops before the route is re-pointed and before any answer is taken"},
			refused: []string{"Gateway-level policy this pass found stands"},
		},
		{
			name:   "a Gateway-level policy holds the credit",
			status: status(), rt: routeNaming(agent.Name, "r2"), held: true,
			want: []string{"TERMINAL",
				"None of that ends it while the Gateway-level policy this pass found stands",
				"even once a card records and the re-point moves the route, no 401 through it is credited"},
			refused: []string{"foreign traffic policy this pass found stands",
				// In THIS state cardedRevision is false, so the emitting pass
				// made no re-point: the note must not say one ran.
				"the re-point still runs"},
		},
		{
			// The switch is ordered, and the order is the claim: a foreign
			// policy stops the pass higher up than A75's hold does.
			name:   "both a foreign policy and a Gateway-level hold",
			status: status(), rt: routeNaming(agent.Name, "r2"), held: true,
			foreign: []string{"ns/rogue"},
			want:    []string{"None of that ends it while the foreign traffic policy this pass found stands"},
			refused: []string{"Gateway-level policy this pass found stands"},
		},
		{
			name:   "two backendRefs beside an uncarded active revision",
			status: status(), rt: routeNaming(agent.Name, "r1", "r2"),
			want: []string{"does not carry exactly one backendRef", "names no revision", "TERMINAL",
				"names no single revision to attribute on instead",
				"ends without an administrator when status.activeRevision's own card records",
				"only the first applies"},
			refused: []string{"names revision r", "for that revision", "Neither exit is needed here",
				"The exit that works then is the second", "the second exit is",
				"the second is the cheaper one"},
		},
	} {
		got := missingPolicyRouteNote(agent, tc.status, tc.rt, runNS, tc.repointed, tc.held, tc.foreign)
		for _, w := range tc.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: the message does not carry %q: %s", tc.name, w, got)
			}
		}
		// Refusals are case-INSENSITIVE. A refusal is a claim about what the
		// message may not say, and the same sentence capitalised at the start
		// of one is the same claim: a lowercase entry compared with
		// strings.Contains never saw it, and a paraphrase that only moved the
		// recommendation to a sentence boundary survived this whole list.
		// Same shape as the "re-pointed onto" grep that could never fire.
		lower := strings.ToLower(got)
		for _, r := range tc.refused {
			if strings.Contains(lower, strings.ToLower(r)) {
				t.Errorf("%s: the message carries %q, which is false here: %s", tc.name, r, got)
			}
		}
		if !strings.Contains(got, runNS+"/pricer-serving") {
			t.Errorf("%s: the message does not name the route: %s", tc.name, got)
		}
	}
}
