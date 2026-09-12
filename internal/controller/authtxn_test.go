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
	"github.com/Quinyte/assayd/internal/compiler"
)

var testGateway = GatewayConfig{Enabled: true, Name: "assayd", Namespace: "assayd-gateway",
	HostnameSuffix: DefaultGatewayHostnameSuffix}

// H2 (design 03 §3.3.3): a pass is `Governed` only when exactly one gateway
// replica is declared; with more, or an unknown count, it is
// `AuthVerifiedOnOneReplica`. Both are False, so neither withholds Ready.
func TestH2NamesWhatOneProbeProves(t *testing.T) {
	one, three := int32(1), int32(3)
	for _, tc := range []struct {
		name string
		v    *assaydv1alpha1.AuthVerification
		want string
	}{
		{"no record", nil, "AuthVerifiedOnOneReplica"},
		{"count unknown", &assaydv1alpha1.AuthVerification{ReplicasProbed: 1}, "AuthVerifiedOnOneReplica"},
		{"three declared", &assaydv1alpha1.AuthVerification{ReplicasProbed: 1, ReplicasDeclared: &three},
			"AuthVerifiedOnOneReplica"},
		{"one declared, none probed", &assaydv1alpha1.AuthVerification{ReplicasDeclared: &one},
			"AuthVerifiedOnOneReplica"},
		{"one declared, one probed", &assaydv1alpha1.AuthVerification{ReplicasProbed: 1, ReplicasDeclared: &one},
			"Governed"},
	} {
		if got := governanceReason(tc.v); got != tc.want {
			t.Errorf("%s: GovernanceSkipped reason %q, want %q", tc.name, got, tc.want)
		}
	}
	st, reason, msg := governanceFor(&assaydv1alpha1.AuthStatus{Mode: "apikey",
		Verified: &assaydv1alpha1.AuthVerification{ReplicasProbed: 1}})
	if st != metav1.ConditionFalse || reason != "AuthVerifiedOnOneReplica" ||
		!strings.Contains(msg, "an unknown number of") {
		t.Errorf("a served apikey Agent with the count unknown reads %s/%s: %q", st, reason, msg)
	}
	if st, reason, _ := governanceFor(&assaydv1alpha1.AuthStatus{Mode: "none"}); st != metav1.ConditionTrue ||
		reason != "AuthOptedOut" {
		t.Errorf("a served auth: none Agent reads %s/%s, want True/AuthOptedOut (§3.1)", st, reason)
	}
}

func routeWithParent(gen int64, gwName, gwNS string, conds ...metav1.Condition) *gatewayv1.HTTPRoute {
	ns := gatewayv1.Namespace(gwNS)
	rt := &gatewayv1.HTTPRoute{}
	rt.Namespace, rt.Name, rt.Generation = "assayd-run-payments", "pricer-serving", gen
	rt.Status.Parents = []gatewayv1.RouteParentStatus{{
		ParentRef:      gatewayv1.ParentReference{Name: gatewayv1.ObjectName(gwName), Namespace: &ns},
		ControllerName: "agentgateway.dev/agentgateway",
		Conditions:     conds,
	}}
	return rt
}

func cond(t, s, reason string, gen int64) metav1.Condition {
	return metav1.Condition{Type: t, Status: metav1.ConditionStatus(s), Reason: reason, ObservedGeneration: gen}
}

// §3.3.2's HTTPRoute tuple: Accepted and ResolvedRefs, both True, from the
// assayd Gateway, at the route's current generation.
func TestTheRouteTupleHoldsOnlyWhenEveryPartDoes(t *testing.T) {
	good := []metav1.Condition{cond("Accepted", "True", "Accepted", 3), cond("ResolvedRefs", "True", "ResolvedRefs", 3)}
	for _, tc := range []struct {
		name string
		rt   *gatewayv1.HTTPRoute
		want bool
	}{
		{"converged", routeWithParent(3, "assayd", "assayd-gateway", good...), true},
		{"no status", &gatewayv1.HTTPRoute{}, false},
		{"another Gateway's status", routeWithParent(3, "other", "assayd-gateway", good...), false},
		{"the right name in another namespace", routeWithParent(3, "assayd", "elsewhere", good...), false},
		{"a generation behind", routeWithParent(4, "assayd", "assayd-gateway", good...), false},
		{"refs unresolved", routeWithParent(3, "assayd", "assayd-gateway",
			cond("Accepted", "True", "Accepted", 3), cond("ResolvedRefs", "False", "RefNotPermitted", 3)), false},
		{"not accepted", routeWithParent(3, "assayd", "assayd-gateway",
			cond("Accepted", "False", "NoMatchingParent", 3), cond("ResolvedRefs", "True", "ResolvedRefs", 3)), false},
		{"no ResolvedRefs at all", routeWithParent(3, "assayd", "assayd-gateway",
			cond("Accepted", "True", "Accepted", 3)), false},
	} {
		if got, why := routeConverged(tc.rt, testGateway); got != tc.want {
			t.Errorf("%s: converged=%v (%s), want %v", tc.name, got, why, tc.want)
		}
	}
}

func policyWith(gen int64, ancestors ...any) *unstructured.Unstructured {
	p := &unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"name": "pricer-auth", "namespace": "assayd-run-payments", "generation": gen},
	}}
	if ancestors != nil {
		p.Object["status"] = map[string]any{"ancestors": ancestors}
	}
	return p
}

func ancestor(group, kind, name, ns string, conds ...map[string]any) map[string]any {
	cs := make([]any, len(conds))
	for i := range conds {
		cs[i] = conds[i]
	}
	return map[string]any{
		"ancestorRef":    map[string]any{"group": group, "kind": kind, "name": name, "namespace": ns},
		"controllerName": "agentgateway.dev/agentgateway",
		"conditions":     cs,
	}
}

func ucond(t, s, reason string, gen int64) map[string]any {
	return map[string]any{"type": t, "status": s, "reason": reason, "observedGeneration": gen}
}

// §3.3.2's AgentgatewayPolicy tuple, and the three traps it closes: the
// success ancestor is the GATEWAY, not the target route; a StatusSummary
// ancestor is negative whatever else is there; and no ancestors at all is not
// success. `reason: Valid` is required, not merely Accepted=True.
func TestThePolicyTupleClosesTheTrapsTheResearchNamed(t *testing.T) {
	gw := func(conds ...map[string]any) map[string]any {
		return ancestor("gateway.networking.k8s.io", "Gateway", "assayd", "assayd-gateway", conds...)
	}
	accepted, attached := ucond("Accepted", "True", "Valid", 2), ucond("Attached", "True", "Attached", 2)
	for _, tc := range []struct {
		name string
		p    *unstructured.Unstructured
		want bool
	}{
		{"converged", policyWith(2, gw(accepted, attached)), true},
		{"no ancestors", policyWith(2), false},
		{"an empty ancestor list", policyWith(2, []any{}...), false},
		{"the target route as ancestor", policyWith(2,
			ancestor("gateway.networking.k8s.io", "HTTPRoute", "pricer-serving", "assayd-run-payments", accepted, attached)), false},
		{"StatusSummary beside a good Gateway ancestor", policyWith(2, gw(accepted, attached),
			ancestor("agentgateway.dev", "Gateway", "StatusSummary", "", ucond("Attached", "False", "Pending", 2))), false},
		{"PartiallyValid", policyWith(2, gw(ucond("Accepted", "True", "PartiallyValid", 2), attached)), false},
		{"not attached", policyWith(2, gw(accepted, ucond("Attached", "False", "Pending", 2))), false},
		{"a generation behind", policyWith(3, gw(accepted, attached)), false},
		{"another Gateway", policyWith(2,
			ancestor("gateway.networking.k8s.io", "Gateway", "other", "assayd-gateway", accepted, attached)), false},
	} {
		if got, why := policyConverged(tc.p, testGateway); got != tc.want {
			t.Errorf("%s: converged=%v (%s), want %v", tc.name, got, why, tc.want)
		}
	}
}

// The emitter's choice, from status.auth alone (§3.3.1's marker rule).
func TestTheEmitterChoosesThePublicationFromStatusAuth(t *testing.T) {
	create := func(mode, stage string) *assaydv1alpha1.AuthStatus {
		return &assaydv1alpha1.AuthStatus{Transaction: &assaydv1alpha1.AuthTransaction{
			Kind: TxCreate, TargetMode: mode, Stage: stage}}
	}
	for _, tc := range []struct {
		name string
		auth *assaydv1alpha1.AuthStatus
		want routePublication
	}{
		{"a Create preparing", create("apikey", StagePreparingRoute), routePublication{prepared: true}},
		{"a Create probing", create("apikey", StageProbingAfter), routePublication{prepared: true}},
		{"an apikey Create publishing", create("apikey", StagePublishing), routePublication{mode: "apikey"}},
		{"a none Create publishing", create("none", StagePublishing), routePublication{mode: "none"}},
		{"served apikey", &assaydv1alpha1.AuthStatus{Mode: "apikey"}, routePublication{mode: "apikey"}},
		{"served none", &assaydv1alpha1.AuthStatus{Mode: "none"}, routePublication{mode: "none"}},
		// A refused Adopt's route: the compiler did not publish it, so no marker.
		{"nothing recorded", nil, routePublication{}},
	} {
		if got := routePublicationFor(tc.auth); got != tc.want {
			t.Errorf("%s: %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

// PolicyCompileFailed's causes and their reasons (§3.3.1, §5).
func TestCompileFailuresNameTheCauseAndTheFix(t *testing.T) {
	oauth := desiredAuth(authTestAgent("oauth"), "assayd-run-payments")
	if f := compileFailures(oauth, "", ""); len(f) != 1 || f[0].reason != "AuthInputAbsent" ||
		!strings.Contains(f[0].message, "No route is published") {
		t.Errorf("a never-served oauth Agent: %+v", f)
	}
	if f := compileFailures(oauth, "apikey", ""); len(f) != 1 || f[0].reason != "AuthInputAbsent" ||
		!strings.Contains(f[0].message, "still enforced") {
		t.Errorf("a served oauth Agent (I1) must say its last good -auth is still enforced: %+v", f)
	}
	none := desiredAuth(authTestAgent("none"), "assayd-run-payments")
	if f := compileFailures(none, "apikey", ""); len(f) != 1 || f[0].reason != "AuthTransitionNotBuilt" {
		t.Errorf("apikey → none must be refused as AuthTransitionNotBuilt: %+v", f)
	}
	if f := compileFailures(none, "", ""); len(f) != 0 {
		t.Errorf("a never-served none Agent compiles: %+v", f)
	}
	// none → apikey on a served Agent is J2's Lock, not a refusal (§3.3.1).
	apikey := desiredAuth(authTestAgent("apikey"), "assayd-run-payments")
	if f := compileFailures(apikey, "none", ""); len(f) != 0 {
		t.Errorf("none → apikey is locked in place by J2, and compiles: %+v", f)
	}
	// A route re-create beside an uncompilable spec says the route is
	// unpublished until its probe passes (§3.3.3).
	if f := compileFailures(oauth, "apikey", "apikey"); len(f) != 1 || f[0].reason != "AuthInputAbsent" ||
		!strings.Contains(f[0].message, "UNPUBLISHED") {
		t.Errorf("an I1 Agent whose route is re-created must be told it is unpublished: %+v", f)
	}
	budgeted := authTestAgent("apikey")
	budgeted.Spec.Budget = &assaydv1alpha1.BudgetSpec{}
	d := desiredAuth(budgeted, "assayd-run-payments")
	if d.compiles() {
		t.Error("an Agent with a stored spec.budget compiles; §3.3.1 enters no transaction for it")
	}
	if f := compileFailures(d, "", ""); len(f) != 1 || f[0].reason != "ConcernNotBuilt" ||
		!strings.Contains(f[0].message, "spec.budget") {
		t.Errorf("a stored spec.budget must be ConcernNotBuilt, naming the field: %+v", f)
	}
	if got := compiler.KeySource("assayd-run-payments"); got != "assayd-run-payments/assayd.dev/api-keys=true" {
		t.Errorf("the recorded key source is %q", got)
	}
}
