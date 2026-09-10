// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"regexp"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
)

// dns1123Label is the shape every object name here must have. A name that is
// not one is rejected by the API server at create time, which is a failure the
// operator discovers per Agent rather than in a test.
var dns1123Label = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// Design 03 §3.2's naming rule, at and past the limit.
//
// The rule matters for the same reason design 02 A42's does: it names an object
// that is a boundary. Two Agents whose emitted names collided would have one
// Agent's serving route deleted by the other's sweep — the sweep's authority is
// the NAME, so a name that is not injective hands one Agent authority over
// another's traffic.
func TestEmittedNameIsADNSLabelAndInjectiveAtTheLimit(t *testing.T) {
	short, err := EmittedName("agent", RouteConcernServing, "")
	if err != nil {
		t.Fatalf("short name: %v", err)
	}
	if short != "agent-serving" {
		t.Errorf("a name that fits is not hashed: got %q, want %q", short, "agent-serving")
	}
	withRev, err := EmittedName("agent", RouteConcernServing, "0123456789")
	if err != nil {
		t.Fatalf("name with revision: %v", err)
	}
	if withRev != "agent-serving-0123456789" {
		t.Errorf("the optional <rev> is not appended as `<name>-<concern>-<rev>`: %q", withRev)
	}

	// Two long names that share every character the truncation keeps. Under an
	// unhashed truncation these are one name.
	prefix := strings.Repeat("a", 60)
	one, err := EmittedName(prefix+"-one", RouteConcernServing, "")
	if err != nil {
		t.Fatalf("long name: %v", err)
	}
	two, err := EmittedName(prefix+"-two", RouteConcernServing, "")
	if err != nil {
		t.Fatalf("long name: %v", err)
	}
	for _, n := range []string{short, withRev, one, two} {
		if len(n) > 63 {
			t.Errorf("emitted name %q is %d characters; a DNS label is at most 63", n, len(n))
		}
		if !dns1123Label.MatchString(n) {
			t.Errorf("emitted name %q is not a DNS-1123 label", n)
		}
	}
	if one == two {
		t.Errorf("two different names truncate to one emitted name (%q). The sweep's authority is "+
			"the name, so a collision hands one Agent the power to delete another's serving route.",
			one)
	}
	// The concern must survive truncation, or two concerns of one Agent collide
	// with each other — which is the same failure one level down.
	if !strings.Contains(one, "-"+RouteConcernServing+"-") {
		t.Errorf("the concern did not survive truncation: %q", one)
	}

	// A concern and revision that leave no room is an ERROR, never a mangled
	// name. Design 03 §3.2: never a silent reuse.
	if _, err := EmittedName("a", strings.Repeat("c", 70), ""); err == nil {
		t.Error("a concern longer than the whole limit produced a name instead of an error")
	}
}

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
	name, err := ServingRouteName(agent.Name)
	if err != nil {
		t.Fatalf("name: %v", err)
	}
	route := r.servingRouteFor(agent, RunNamespaceName("team-a"), "abc1234567", name)

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

// A route's port follows the Agent's declared container port. A hard-coded 8080
// would send traffic to a port nothing listens on the moment an Agent sets one.
func TestTheServingRouteFollowsTheAgentsPort(t *testing.T) {
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
	route := r.servingRouteFor(agent, RunNamespaceName("team-a"), "abc1234567", "pricer-serving")
	if p := route.Spec.Rules[0].BackendRefs[0].Port; p == nil || int32(*p) != 9090 {
		t.Errorf("the route's backendRef port is %v, not the Agent's declared 9090. The Service "+
			"publishes the declared port, so a fixed 8080 routes to nothing.", p)
	}
}

// Design 03 §3.1's table, at the condition level. `false` — what P1 ships —
// carries the reason the design writes down; `true` carries a DIFFERENT reason,
// because the compiler still does not exist and a False here would claim a
// governance nothing performs.
func TestGovernanceSkippedNamesWhichUngovernedTierThisIs(t *testing.T) {
	off := newConditionSet(1)
	(&AgentReconciler{}).assessGovernance(off)
	got := off.asserted[string(assaydv1alpha1.CondGovernanceSkipped)]
	// The LITERAL, not the constant. A reason is a user-facing string: design 03
	// §3.1's table writes `GovernanceSkipped=GatewayDisabled` and design 10 keys
	// its alerting on reasons rather than types, so comparing the constant to
	// itself would let a rename pass at every layer at once.
	if got.Status != metav1.ConditionTrue || got.Reason != "GatewayDisabled" {
		t.Errorf("with the gateway declared off, GovernanceSkipped is %s/%s; design 03 §3.1's table "+
			"says True/GatewayDisabled", got.Status, got.Reason)
	}

	on := newConditionSet(1)
	(&AgentReconciler{Gateway: GatewayConfig{Enabled: true, Name: "assayd", Namespace: "gw"}}).
		assessGovernance(on)
	got = on.asserted[string(assaydv1alpha1.CondGovernanceSkipped)]
	if got.Status != metav1.ConditionTrue || got.Reason != "PolicyCompilerAbsent" {
		t.Errorf("with the gateway declared on, GovernanceSkipped is %s/%s; the compiler does not "+
			"exist, so this install is still ungoverned and must say so under its own reason",
			got.Status, got.Reason)
	}
	if !strings.Contains(got.Message, "unauthenticated") {
		t.Errorf("the enabled message does not say the published path is unauthenticated: %q", got.Message)
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
