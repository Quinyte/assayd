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
	for _, tc := range []struct {
		name          string
		status        *assaydv1alpha1.AgentStatus
		rt            *gatewayv1.HTTPRoute
		repointed     bool
		want, refused []string
	}{
		{
			name: "re-pointed onto a carded active revision", status: status("r2"),
			rt: routeNaming(agent.Name, "r2"), repointed: true,
			want:    []string{"names revision r2", "re-pointed at status.activeRevision r2"},
			refused: []string{"TERMINAL", "left as found"},
		},
		{
			name:   "left as found on an uncarded revision beside an uncarded active one",
			status: status(), rt: routeNaming(agent.Name, "r1"),
			want: []string{"names revision r1", "left as found", "TERMINAL",
				"or for status.activeRevision", "ends only by an administrator acting",
				"removes " + runNS + "/pricer-serving", "the spec is reverted to the revision the route names"},
			refused: []string{"re-pointed at status.activeRevision"},
		},
		{
			name:   "the route names the active revision, whose card is still being retried",
			status: status(), rt: routeNaming(agent.Name, "r2"),
			want: []string{"names revision r2", "TERMINAL", "which is also status.activeRevision",
				"needs neither exit"},
			refused: []string{"or for status.activeRevision", "ends only by an administrator acting"},
		},
		{
			name:   "two backendRefs beside an uncarded active revision",
			status: status(), rt: routeNaming(agent.Name, "r1", "r2"),
			want: []string{"does not carry exactly one backendRef", "names no revision", "TERMINAL",
				"names no single revision to attribute on instead", "admits only the first exit"},
			refused: []string{"names revision r", "for that revision"},
		},
	} {
		got := missingPolicyRouteNote(agent, tc.status, tc.rt, runNS, tc.repointed)
		for _, w := range tc.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: the message does not carry %q: %s", tc.name, w, got)
			}
		}
		for _, r := range tc.refused {
			if strings.Contains(got, r) {
				t.Errorf("%s: the message carries %q, which is false here: %s", tc.name, r, got)
			}
		}
		if !strings.Contains(got, runNS+"/pricer-serving") {
			t.Errorf("%s: the message does not name the route: %s", tc.name, got)
		}
	}
}
