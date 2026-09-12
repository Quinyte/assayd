// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
)

// §3.3's re-creation test and §3.3.3's abandonment trigger, read from stored
// fields only, so that a restarted operator classifies every transaction the
// same way.
func TestAbandonmentIsKeyedOnTheTargetAndNeverTouchesARecreation(t *testing.T) {
	lock := func(target string) *assaydv1alpha1.AuthTransaction {
		return &assaydv1alpha1.AuthTransaction{Kind: TxLock, TargetMode: target}
	}
	create := func(target string) *assaydv1alpha1.AuthTransaction {
		return &assaydv1alpha1.AuthTransaction{Kind: TxCreate, TargetMode: target}
	}
	for _, tc := range []struct {
		name      string
		auth      *assaydv1alpha1.AuthStatus
		desired   string
		reCreate  bool
		abandoned bool
	}{
		{"J2 Lock, reverted to none", &assaydv1alpha1.AuthStatus{Mode: "none", Transaction: lock("apikey")}, "none", false, true},
		{"J2 Lock, moved to oauth", &assaydv1alpha1.AuthStatus{Mode: "none", Transaction: lock("apikey")}, "oauth", false, true},
		{"J2 Lock, still apikey", &assaydv1alpha1.AuthStatus{Mode: "none", Transaction: lock("apikey")}, "apikey", false, false},
		{"K2 Lock, reverted", &assaydv1alpha1.AuthStatus{Transaction: lock("apikey")}, "none", false, true},
		{"missing-policy Lock, spec none", &assaydv1alpha1.AuthStatus{Mode: "apikey", Transaction: lock("apikey")}, "none", true, false},
		{"route re-create, spec oauth", &assaydv1alpha1.AuthStatus{Mode: "apikey", Transaction: create("apikey")}, "oauth", true, false},
		{"never-served Create, reverted", &assaydv1alpha1.AuthStatus{Transaction: create("apikey")}, "none", false, true},
		{"never-served Create, unchanged", &assaydv1alpha1.AuthStatus{Transaction: create("none")}, "none", false, false},
		{"refused Adopt", &assaydv1alpha1.AuthStatus{Transaction: &assaydv1alpha1.AuthTransaction{Kind: TxAdopt, RefusedMode: "none"}}, "apikey", false, false},
		{"a Lock with no target", &assaydv1alpha1.AuthStatus{Transaction: lock("")}, "apikey", false, false},
	} {
		if got := isReCreation(tc.auth); got != tc.reCreate {
			t.Errorf("%s: isReCreation = %v, want %v", tc.name, got, tc.reCreate)
		}
		if got := abandons(tc.auth, tc.desired); got != tc.abandoned {
			t.Errorf("%s: abandons = %v, want %v", tc.name, got, tc.abandoned)
		}
	}
}

// K2 (§3.3.3, A66): consent is a desired apikey beside a refusedMode that is
// not apikey. A spec that already said apikey at the refusal is not consent.
func TestK2ConsentIsAnEditObservedSinceTheRefusal(t *testing.T) {
	adopt := func(refused string) *assaydv1alpha1.AuthTransaction {
		return &assaydv1alpha1.AuthTransaction{Kind: TxAdopt, Stage: StageRefused, RefusedMode: refused}
	}
	if !k2Consent(adopt("none"), authTestAgent("apikey")) {
		t.Error("a desired apikey after a refusal under none is K2's consent")
	}
	if !k2Consent(adopt("oauth"), authTestAgent("apikey")) {
		t.Error("a move from oauth counts, by the author's reading (§1.1)")
	}
	if k2Consent(adopt("apikey"), authTestAgent("apikey")) {
		t.Error("a spec that already said apikey at the refusal was taken as consent")
	}
	if k2Consent(adopt("none"), authTestAgent("none")) {
		t.Error("a desired none was taken as consent")
	}
}

// ProbingBefore's attribution (§3.3.3, A62): beforeObserved is set only when
// the 200's body digests to what status.cards records for the revision the
// route's one backendRef names, never another revision's.
func TestTheBeforeRevisionIsTheOneTheRouteNames(t *testing.T) {
	agent := authTestAgent("none")
	route := func(rev string) *gatewayv1.HTTPRoute {
		return &gatewayv1.HTTPRoute{Spec: gatewayv1.HTTPRouteSpec{Rules: []gatewayv1.HTTPRouteRule{{
			BackendRefs: []gatewayv1.HTTPBackendRef{{BackendRef: gatewayv1.BackendRef{
				BackendObjectReference: gatewayv1.BackendObjectReference{
					Name: gatewayv1.ObjectName(WorkloadName(agent.Name, rev))}}}}}}}}
	}
	status := &assaydv1alpha1.AgentStatus{Cards: []assaydv1alpha1.CardStatus{
		{Revision: "r1", Digest: "d1"}, {Revision: "r2", Digest: "d2"}}}
	if got := beforeRevisionFor(agent, status, route("r1"), AuthProbeAnswer{Code: 200, CardDigest: "d1"}); got != "r1" {
		t.Errorf("r1's card on a route naming r1: %q", got)
	}
	if got := beforeRevisionFor(agent, status, route("r2"), AuthProbeAnswer{Code: 200, CardDigest: "d1"}); got != "" {
		t.Errorf("r1's card on a route naming r2 set beforeRevision %q", got)
	}
	if got := beforeRevisionFor(agent, status, route("r1"), AuthProbeAnswer{Code: 503}); got != "" {
		t.Errorf("a 503 set beforeRevision %q", got)
	}
}

// A70's owed check: a NACK drives status only when it has the shape
// agentgateway gives one, and every field of that shape is load-bearing.
func TestOnlyAGenuineNackIsTakenAsOne(t *testing.T) {
	gw := GatewayConfig{Enabled: true, Name: "assayd", Namespace: "assayd-gateway"}
	genuine := func() *corev1.Event {
		return &corev1.Event{
			ObjectMeta: metav1.ObjectMeta{Name: "gw.1", Namespace: "assayd-gateway"},
			InvolvedObject: corev1.ObjectReference{APIVersion: "gateway.networking.k8s.io/v1",
				Kind: "Gateway", Name: "assayd", Namespace: "assayd-gateway"},
			Type: corev1.EventTypeWarning, Reason: NackEventReason,
			Source:              corev1.EventSource{Component: AgentgatewayControllerName},
			ReportingController: AgentgatewayControllerName,
		}
	}
	if !GenuineNack(genuine(), gw) {
		t.Fatal("the shape agentgateway 1.5.0 emits was refused")
	}
	for name, forge := range map[string]func(*corev1.Event){
		"another component":           func(e *corev1.Event) { e.Source.Component = "kubectl" },
		"another reportingController": func(e *corev1.Event) { e.ReportingController = "" },
		"another Gateway":             func(e *corev1.Event) { e.InvolvedObject.Name = "other" },
		"a Pod":                       func(e *corev1.Event) { e.InvolvedObject.Kind = "Pod" },
		"another group":               func(e *corev1.Event) { e.InvolvedObject.APIVersion = "example.com/v1" },
		"another namespace":           func(e *corev1.Event) { e.Namespace = "elsewhere" },
		"a Normal event":              func(e *corev1.Event) { e.Type = corev1.EventTypeNormal },
	} {
		ev := genuine()
		forge(ev)
		if GenuineNack(ev, gw) {
			t.Errorf("an Event with %s was taken as a NACK", name)
		}
	}
}

// A NACK is tied to a write only by time, and only when strictly after it.
func TestAPolicyIsWrittenWhenItsLastNonStatusEntrySays(t *testing.T) {
	early, late := metav1.NewTime(time.Unix(100, 0)), metav1.NewTime(time.Unix(200, 0))
	p := &unstructured.Unstructured{}
	p.SetManagedFields([]metav1.ManagedFieldsEntry{
		{Manager: "operator", Time: &early},
		{Manager: "agentgateway", Subresource: "status", Time: &late},
	})
	if got := policyWrittenAt(p); !got.Equal(early.Time) {
		t.Errorf("a status write moved the policy's write time to %v", got)
	}
}

// §3.2's route-level target test: a targetRef naming the route, or a selector
// whose labels the route carries, of kind HTTPRoute.
func TestAForeignPolicyTargetsTheRouteByRefOrSelector(t *testing.T) {
	labels := map[string]string{LabelAgent: "a", LabelAgentNamespace: "ns"}
	mk := func(spec map[string]any) *unstructured.Unstructured {
		return &unstructured.Unstructured{Object: map[string]any{"spec": spec}}
	}
	ref := func(kind, name string) map[string]any {
		return map[string]any{"targetRefs": []any{map[string]any{
			"group": gatewayv1.GroupName, "kind": kind, "name": name}}}
	}
	sel := func(ml map[string]any) map[string]any {
		return map[string]any{"targetSelectors": []any{map[string]any{
			"group": gatewayv1.GroupName, "kind": "HTTPRoute", "matchLabels": ml}}}
	}
	for name, tc := range map[string]struct {
		spec map[string]any
		want bool
	}{
		"a ref to the route":        {ref("HTTPRoute", "a-serving"), true},
		"a ref to another route":    {ref("HTTPRoute", "b-serving"), false},
		"a ref to the Gateway":      {ref("Gateway", "a-serving"), false},
		"a selector it matches":     {sel(map[string]any{LabelAgent: "a"}), true},
		"a selector of every route": {sel(map[string]any{}), true},
		"a selector it misses":      {sel(map[string]any{LabelAgent: "b"}), false},
	} {
		if got := policyTargetsRoute(mk(tc.spec), "a-serving", labels); got != tc.want {
			t.Errorf("%s: %v, want %v", name, got, tc.want)
		}
	}
}

// client-go's recorder aggregates similar Events and prefixes the message it
// keeps, which is not JSON: an aggregated NACK must still name its policy
// (slice PR 5's review).
func TestAnAggregatedNackIsStillRead(t *testing.T) {
	msg := "(combined from similar events): " + `[{"key":"policy/traffic/ns/p:auth:ns/r","error":"x"}]`
	got, _ := NackedPolicies(msg)
	if len(got) != 1 || got[0].Namespace != "ns" || got[0].Name != "p" {
		t.Errorf("an aggregated NACK was read as %v", got)
	}
}
