// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/compiler"
)

// The hostname is per AGENT and includes the Agent's own namespace, so two
// Agents of the same name in two namespaces do not answer for each other.
func TestTheServingHostnameSeparatesNamespaces(t *testing.T) {
	g := GatewayConfig{HostnameSuffix: DefaultGatewayHostnameSuffix}
	a := g.Hostname("pricer", "team-a")
	b := g.Hostname("pricer", "team-b")
	if a == b {
		t.Fatalf("two Agents in different namespaces share one hostname (%q); the Gateway matches "+
			"on the Host header, so one team's traffic would reach the other's agent", a)
	}
	if a != "pricer.team-a."+DefaultGatewayHostnameSuffix {
		t.Errorf("hostname is not <agent>.<agent-namespace>.<suffix>: %q", a)
	}
}

// What the route actually says. Asserted against the RENDERED object, not a
// fixture: every field here was measured through a real Gateway by design 07
// A6, and each one is load-bearing.
func TestTheServingRouteIsTheResourceTheE2EAuthoredByHand(t *testing.T) {
	r := &AgentReconciler{Gateway: GatewayConfig{
		Enabled: true, Name: "assayd", Namespace: "assayd-gateway",
		HostnameSuffix: DefaultGatewayHostnameSuffix,
	}}
	agent := &assaydv1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{Name: "pricer", Namespace: "team-a", UID: types.UID("uid-1")},
		Spec: assaydv1alpha1.AgentSpec{
			Runtime: &assaydv1alpha1.AgentRuntime{Image: "ghcr.io/acme/a@sha256:" + strings.Repeat("0", 64)},
		},
	}
	name, err := compiler.ServingRouteName(agent.Name)
	if err != nil {
		t.Fatalf("name: %v", err)
	}
	route := r.servingRouteFor(agent, RunNamespaceName("team-a"), "abc1234567", name, 8080, routePublication{})

	if route.Namespace != "assayd-run-team-a" {
		t.Errorf("the route is not in the run namespace (%q). A backendRef across namespaces needs "+
			"a ReferenceGrant and reports ResolvedRefs=False without one (design 03 §3.2).",
			route.Namespace)
	}
	if len(route.OwnerReferences) != 0 {
		t.Error("the route carries an ownerReference. The Agent is in another namespace, so a " +
			"cross-namespace owner is treated as ABSENT with an OwnerRefInvalidNamespace Event — " +
			"the route would leak silently while looking collected (design 03 §3.2).")
	}
	for k, want := range map[string]string{
		LabelAgentUID:       "uid-1",
		LabelRevision:       "abc1234567",
		LabelAgent:          "pricer",
		LabelAgentNamespace: "team-a",
	} {
		if got := route.Labels[k]; got != want {
			t.Errorf("label %s is %q, want %q", k, got, want)
		}
	}

	if len(route.Spec.ParentRefs) != 1 {
		t.Fatalf("want one parentRef, got %d", len(route.Spec.ParentRefs))
	}
	p := route.Spec.ParentRefs[0]
	if string(p.Name) != "assayd" || p.Namespace == nil || string(*p.Namespace) != "assayd-gateway" {
		t.Errorf("the parentRef does not name the configured Gateway: %+v. The namespace is not the "+
			"operator's, and conflating them made design 07 A5.9's reservation match nothing and "+
			"admit any author (A6.2).", p)
	}
	if p.SectionName == nil || string(*p.SectionName) != GatewayListenerName {
		t.Errorf("the parentRef names no listener; a route with no sectionName attaches to every "+
			"listener the Gateway has, including the MCP tools one: %+v", p)
	}
	if len(route.Spec.Hostnames) != 1 || string(route.Spec.Hostnames[0]) != "pricer.team-a."+DefaultGatewayHostnameSuffix {
		t.Errorf("hostnames are %v; a route with NO hostname matches every request on the listener, "+
			"so every Agent would answer for every other", route.Spec.Hostnames)
	}
	if len(route.Spec.Rules) != 1 || len(route.Spec.Rules[0].BackendRefs) != 1 {
		t.Fatalf("want one rule with one backendRef, got %+v", route.Spec.Rules)
	}
	b := route.Spec.Rules[0].BackendRefs[0]
	if string(b.Name) != WorkloadName("pricer", "abc1234567") {
		t.Errorf("the backendRef names %q, not the REVISION's Service. One Service per revision is "+
			"what makes a weight between revisions mean anything (design 02 §3.2).", b.Name)
	}
	if b.Port == nil || int32(*b.Port) != 8080 {
		t.Errorf("the backendRef names no port or the wrong one: %+v", b.Port)
	}
	if b.Weight == nil || *b.Weight != 100 {
		t.Errorf("the backendRef has weight %v, want 100", b.Weight)
	}
	// Spelled out rather than defaulted: a nil Kind/Group would differ from the
	// stored object on every read and Update on every reconcile forever.
	if b.Kind == nil || string(*b.Kind) != "Service" || b.Group == nil || string(*b.Group) != "" {
		t.Errorf("the backendRef leaves Kind/Group to the API server's defaulting, which makes the "+
			"desired object differ from the stored one on every read: kind=%v group=%v", b.Kind, b.Group)
	}
}

// The route's port is the one its SERVICE publishes, never the Agent's spec.
//
// This test used to assert the opposite — that the backendRef port equals
// `spec.runtime.port` — while rendering against an unrelated revision, on the
// ground that "the Service publishes the declared port". That is true only of
// the DESIRED revision, and the route names the SERVING one: a port edit whose
// revision never came up rewrote the healthy revision's route to a port its
// Service does not publish. The envtest
// TestAPortChangeThatNeverComesUpDoesNotMoveTheServingRoute measures that
// transition against a real API server; this pins only that the renderer
// cannot reach for the spec.
func TestTheServingRoutesPortIsTheServicesNotTheSpecs(t *testing.T) {
	r := &AgentReconciler{Gateway: GatewayConfig{
		Enabled: true, Name: "assayd", Namespace: "assayd-gateway",
		HostnameSuffix: DefaultGatewayHostnameSuffix,
	}}
	agent := &assaydv1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{Name: "pricer", Namespace: "team-a", UID: types.UID("uid-1")},
		Spec: assaydv1alpha1.AgentSpec{Runtime: &assaydv1alpha1.AgentRuntime{
			Image: "ghcr.io/acme/a@sha256:" + strings.Repeat("0", 64), Port: 9090,
		}},
	}
	route := r.servingRouteFor(agent, RunNamespaceName("team-a"), "abc1234567", "pricer-serving", 8080,
		routePublication{})
	if p := route.Spec.Rules[0].BackendRefs[0].Port; p == nil || int32(*p) != 8080 {
		t.Errorf("the route's backendRef port is %v, not the 8080 the serving revision's Service "+
			"publishes. The spec's 9090 belongs to a revision that is not serving.", p)
	}
}

// Design 03 §3.1's table, at the condition level, on the paths that can see
// only status.auth. `false` — what P1 ships — carries the reason the design
// writes down. `true` runs the compiler, so the value comes from status.auth:
// what a served -auth earned, and "vacuously False" where the compiler has
// published nothing. A refused `Adopt`'s value, which only a pass that read
// the route can know, is kept rather than overwritten.
func TestGovernanceSkippedNamesWhichUngovernedTierThisIs(t *testing.T) {
	off := newConditionSet(1)
	(&AgentReconciler{}).assessGovernance(off, &assaydv1alpha1.AgentStatus{})
	got := off.asserted[string(assaydv1alpha1.CondGovernanceSkipped)]
	// The LITERAL, not the constant. A reason is a user-facing string: design 03
	// §3.1's table writes `GovernanceSkipped=GatewayDisabled` and design 10 keys
	// its alerting on reasons rather than types, so comparing the constant to
	// itself would let a rename pass at every layer at once.
	if got.Status != metav1.ConditionTrue || got.Reason != "GatewayDisabled" {
		t.Errorf("with the gateway declared off, GovernanceSkipped is %s/%s; design 03 §3.1's table "+
			"says True/GatewayDisabled", got.Status, got.Reason)
	}

	on := &AgentReconciler{Gateway: GatewayConfig{Enabled: true, Name: "assayd", Namespace: "gw"}}
	for _, tc := range []struct {
		name   string
		status assaydv1alpha1.AgentStatus
		want   metav1.ConditionStatus
		reason string
	}{
		{"nothing published", assaydv1alpha1.AgentStatus{}, metav1.ConditionFalse, "Governed"},
		{"served apikey, replica count unknown", assaydv1alpha1.AgentStatus{Auth: &assaydv1alpha1.AuthStatus{
			Mode: "apikey", Verified: &assaydv1alpha1.AuthVerification{ReplicasProbed: 1}}},
			metav1.ConditionFalse, "AuthVerifiedOnOneReplica"},
		{"served none", assaydv1alpha1.AgentStatus{Auth: &assaydv1alpha1.AuthStatus{Mode: "none"}},
			metav1.ConditionTrue, "AuthOptedOut"},
	} {
		c := newConditionSet(1)
		st := tc.status
		on.assessGovernance(c, &st)
		got = c.asserted[string(assaydv1alpha1.CondGovernanceSkipped)]
		if got.Status != tc.want || got.Reason != tc.reason {
			t.Errorf("%s: GovernanceSkipped is %s/%s, want %s/%s", tc.name, got.Status, got.Reason,
				tc.want, tc.reason)
		}
		if tc.reason == "Governed" && !strings.Contains(got.Message, "vacuously") {
			t.Errorf("%s: a False with nothing published must say it is vacuous: %q", tc.name, got.Message)
		}
	}

	adopted := newConditionSet(1)
	on.assessGovernance(adopted, &assaydv1alpha1.AgentStatus{
		Auth: &assaydv1alpha1.AuthStatus{Transaction: &assaydv1alpha1.AuthTransaction{
			Kind: "Adopt", Stage: "Refused", RefusedMode: "apikey"}},
		Conditions: []metav1.Condition{{
			Type: string(assaydv1alpha1.CondGovernanceSkipped), Status: metav1.ConditionTrue,
			Reason: "CompilerUpgradeUnsupported"}}})
	if c, ok := adopted.asserted[string(assaydv1alpha1.CondGovernanceSkipped)]; ok {
		t.Errorf("a refused Adopt's CompilerUpgradeUnsupported was overwritten with %s/%s by a pass "+
			"that never read the route, which would call an unauthenticated route governed", c.Status, c.Reason)
	}
}

// The classification, asserted through merge() rather than by reading the maps.
//
// The first version of this test did `if !stickyTypes[CondGovernanceSkipped]`,
// and **deleting merge()'s entire sticky arm left it passing** — it compared a
// map to itself and observed no behaviour. That is the shape of the nine tests
// this repository has already had pass with their subject deleted, found by the
// independent review of design 07 A6.10.
//
// The two classifications are asserted together because design 03 §3.1's whole
// point is that they are different: a NORMAL-true condition must survive a pass
// that does not speak to it, because its absence is indistinguishable from
// "never evaluated"; an ABNORMAL-true one must be dropped, because its absence
// IS the signal.
func TestTheTierConditionSurvivesAPassThatDoesNotSpeakToIt(t *testing.T) {
	prior := []metav1.Condition{
		{
			Type: string(assaydv1alpha1.CondGovernanceSkipped), Status: metav1.ConditionTrue,
			Reason: "GatewayDisabled", Message: "the declared-ungoverned tier",
		},
		{
			Type: string(assaydv1alpha1.CondPolicyApplyIncomplete), Status: metav1.ConditionTrue,
			Reason: "RouteApplyFailed", Message: "a route that could not be written",
		},
	}
	// A pass that asserts NOTHING — the shape of every status path that builds
	// its own condition set and does not run every assessor.
	got := newConditionSet(2).merge(prior)

	var kept, dropped []string
	for _, c := range got {
		kept = append(kept, c.Type)
	}
	for _, want := range []string{string(assaydv1alpha1.CondGovernanceSkipped)} {
		found := false
		for _, k := range kept {
			if k == want {
				found = true
			}
		}
		if !found {
			t.Errorf("%s did not survive a pass that said nothing about it (kept %v). It is "+
				"NORMAL-true: design 03 §3.1 requires it to stay in the list and flip to False, "+
				"because dropping it erases the record of which tier this install chose.",
				want, kept)
		}
	}
	for _, mustGo := range []string{string(assaydv1alpha1.CondPolicyApplyIncomplete)} {
		for _, k := range kept {
			if k == mustGo {
				dropped = append(dropped, mustGo)
			}
		}
	}
	if len(dropped) != 0 {
		t.Errorf("%v survived a pass that said nothing about it. Those are ABNORMAL-true and "+
			"owned: their absence means 'not degraded', so carrying one forward reports a route "+
			"failure that was fixed passes ago.", dropped)
	}

	// And an UNOWNED type is another controller's and must be left exactly as
	// it was — clearing one would be a write race with its author.
	foreign := []metav1.Condition{{
		Type: string(assaydv1alpha1.CondBudgetExhausted), Status: metav1.ConditionTrue,
		Reason: "OverBudget", Message: "design 04's backstop wrote this",
	}}
	if out := newConditionSet(2).merge(foreign); len(out) != 1 ||
		out[0].Type != string(assaydv1alpha1.CondBudgetExhausted) {
		t.Errorf("another controller's condition did not survive untouched: %+v", out)
	}
}
