// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package envtest

import (
	"context"
	"fmt"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/controller"
	"github.com/Quinyte/assayd/internal/revision"
)

// ADR-0030 step 3, second half. Design 07 A6 authored this route by hand and
// executed it through a real Gateway; here the OPERATOR authors it, against a
// real API server with the pinned Gateway API CRDs installed.
//
// What this layer can claim and what it cannot: envtest runs no kubelet and no
// gateway, so nothing here says the route CARRIES traffic — that is the e2e's
// claim, and `TestAnAgentAnswersThroughTheGateway` makes it. What is true here
// is the shape of the object the operator writes, which nothing else pins
// against a schema that will reject a wrong one.

func servingRoute(t *testing.T, ns, agentName string) *gatewayv1.HTTPRoute {
	t.Helper()
	name, err := controller.ServingRouteName(agentName)
	if err != nil {
		t.Fatalf("route name: %v", err)
	}
	var rt gatewayv1.HTTPRoute
	if err := k8s.Get(context.Background(),
		types.NamespacedName{Namespace: runNS(ns), Name: name}, &rt); err != nil {
		return nil
	}
	return &rt
}

func TestTheOperatorEmitsTheServingRouteWhenTheGatewayIsDeclared(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "routed", nil)
	r := newGatewayReconciler("assayd-gateway", "assayd")
	rev := revision.MustHash(a.Spec)

	// Nothing serves until a replica is available, and the route follows the
	// SERVING revision — so before that there is no route at all. Twice: the
	// first pass only installs the finalizer and requeues.
	reconcileOnce(t, r, a)
	reconcileOnce(t, r, a)
	if rt := servingRoute(t, ns, "routed"); rt != nil {
		t.Fatalf("a route was emitted for an Agent with no available replica (%s). The route "+
			"follows status.activeRevision; publishing a hostname to a workload that has never "+
			"been ready sends traffic nowhere.", rt.Name)
	}

	markAvailable(t, ns, controller.WorkloadName("routed", rev), 1)
	live := settle(t, r, a)
	if live.Status.ActiveRevision != rev {
		t.Fatalf("the revision never promoted: %+v", live.Status)
	}

	rt := servingRoute(t, ns, "routed")
	if rt == nil {
		t.Fatal("the operator emitted no serving route for a promoted revision. ADR-0030 step 3's " +
			"second half is exactly this object; without it the e2e's hand-authored route is still " +
			"the only one that has ever existed.")
	}
	if got := rt.Labels[controller.LabelRevision]; got != rev {
		t.Errorf("the route's revision label is %q, want the active %q", got, rev)
	}
	if got := rt.Labels[controller.LabelAgentUID]; got != string(a.UID) {
		t.Errorf("the route carries agent-uid %q, want %q — the sweep corroborates on it", got, a.UID)
	}
	if len(rt.Spec.Rules) != 1 || len(rt.Spec.Rules[0].BackendRefs) != 1 ||
		string(rt.Spec.Rules[0].BackendRefs[0].Name) != controller.WorkloadName("routed", rev) {
		t.Fatalf("the route does not name the active revision's Service: %+v", rt.Spec.Rules)
	}

	// Converged: further passes must issue no writes at all.
	//
	// Counted, not compared by resourceVersion — countingClient's own comment
	// says why, and this test learned it the hard way. The API server does not
	// bump the version on an update that changes nothing, so a renderer that
	// leaves `kind` and `group` on the backendRef to the API server's defaulting
	// differs from the stored object on every read, issues an Update on every
	// reconcile of every Agent forever, and a resourceVersion assertion sees
	// none of it.
	counter := &countingClient{Client: k8s}
	counting := newGatewayReconciler("assayd-gateway", "assayd")
	counting.Client = counter
	for i := 0; i < 5; i++ {
		reconcileOnce(t, counting, a)
	}
	if n := counter.count(); n != 0 {
		t.Errorf("five reconciles of a converged, gateway-enabled agent issued %d writes; want 0. "+
			"A route the operator rewrites every pass churns the API server and fights every "+
			"other writer of the object.", n)
	}
}

// The route MOVES with the serving revision rather than multiplying. Two routes
// on one hostname are merged by a listener, so a candidate coming up would take
// a share of production traffic without a weight ever having been shifted.
func TestTheServingRouteFollowsThePromotedRevisionAndDoesNotMultiply(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "moves", nil)
	r := newGatewayReconciler("assayd-gateway", "assayd")

	first := revision.MustHash(a.Spec)
	reconcileOnce(t, r, a)
	reconcileOnce(t, r, a)
	markAvailable(t, ns, controller.WorkloadName("moves", first), 1)
	settle(t, r, a)
	was := servingRoute(t, ns, "moves")
	if was == nil {
		t.Fatal("no route for the first revision")
	}

	// A behaviour-surface edit mints a second revision.
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), a); err != nil {
		t.Fatalf("re-read agent: %v", err)
	}
	a.Spec.Runtime.Env = []corev1.EnvVar{{Name: "TIER", Value: "two"}}
	if err := k8s.Update(context.Background(), a); err != nil {
		t.Fatalf("update agent: %v", err)
	}
	second := revision.MustHash(a.Spec)
	if second == first {
		t.Fatal("the edit did not mint a new revision; the test is not exercising a rollout")
	}
	reconcileOnce(t, r, a)
	markAvailable(t, ns, controller.WorkloadName("moves", second), 1)
	settle(t, r, a)

	var routes gatewayv1.HTTPRouteList
	if err := k8s.List(context.Background(), &routes, client.InNamespace(runNS(ns))); err != nil {
		t.Fatalf("list routes: %v", err)
	}
	if len(routes.Items) != 1 {
		names := make([]string, 0, len(routes.Items))
		for i := range routes.Items {
			names = append(names, routes.Items[i].Name)
		}
		t.Fatalf("a rollout left %d routes in the run namespace (%v). A listener MERGES routes "+
			"sharing a hostname, so the second one hands the new revision a share of production "+
			"traffic with no weight shifted — the ungated window design 03 §3.3's ordering closes.",
			len(routes.Items), names)
	}
	rt := &routes.Items[0]
	// The SAME object, not a replacement. The name carries no revision, so a
	// promotion is an in-place backendRef update: there is never a moment with
	// two routes on one hostname, and never a moment with none. A name per
	// revision has to pick which of those two windows to open.
	if rt.UID != was.UID {
		t.Errorf("the promotion replaced the route object (%s → %s) instead of updating it in "+
			"place. Create-then-delete puts two routes on one hostname and a listener merges "+
			"them; delete-then-create leaves the hostname unrouted. Neither window exists when "+
			"the name is stable across revisions.", was.UID, rt.UID)
	}
	if got := string(rt.Spec.Rules[0].BackendRefs[0].Name); got != controller.WorkloadName("moves", second) {
		t.Errorf("after promotion the route still names %q; it must follow the serving revision", got)
	}
	if got := rt.Labels[controller.LabelRevision]; got != second {
		t.Errorf("the route's revision label is %q, want %q", got, second)
	}
}

// Design 03 §3.1's `false` row: emit nothing, say so on every Agent, and do NOT
// withhold Ready. A stock `helm install` must not produce a permanently
// un-Ready fleet.
func TestTheGatewayDeclaredOffEmitsNothingAndSaysSo(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "ungoverned", nil)
	r := newReconciler(false) // gateway.enabled: false — what P1 ships
	rev := revision.MustHash(a.Spec)
	reconcileOnce(t, r, a)
	reconcileOnce(t, r, a)
	markAvailable(t, ns, controller.WorkloadName("ungoverned", rev), 1)
	live := settle(t, r, a)

	var routes gatewayv1.HTTPRouteList
	if err := k8s.List(context.Background(), &routes, client.InNamespace(runNS(ns))); err != nil {
		t.Fatalf("list routes: %v", err)
	}
	if len(routes.Items) != 0 {
		t.Errorf("the declared-ungoverned tier emitted %d route(s). `gateway.enabled: false` means "+
			"the compiler does not run at all (design 03 §3.1).", len(routes.Items))
	}

	c := condition(&live, assaydv1alpha1.CondGovernanceSkipped)
	if c == nil {
		t.Fatal("no GovernanceSkipped condition. Design 03 §3.1: every Agent carries it on the " +
			"declared-ungoverned tier, and NFR-8 forbids a degraded path being silent.")
	}
	if c.Status != metav1.ConditionTrue || c.Reason != "GatewayDisabled" {
		t.Errorf("GovernanceSkipped is %s/%s, want True/GatewayDisabled", c.Status, c.Reason)
	}
	if ready := condition(&live, assaydv1alpha1.CondReady); ready == nil || ready.Status != metav1.ConditionTrue {
		t.Errorf("Ready is %v on the declared-ungoverned tier. §3.1: only the INCIDENT row withholds "+
			"Ready, so a stock install must not produce a permanently un-Ready fleet.", ready)
	}
	if live.Status.Phase != assaydv1alpha1.PhaseReady {
		t.Errorf("phase is %q, want Ready", live.Status.Phase)
	}
}

// The name is the deletion authority (design 03 §3.2). A route wearing every
// label but carrying a name this operator would never emit is left alone and
// NOT swept — a label needs only `update` to forge, so sweeping on one would
// let anyone with route-update rights in a run namespace have this operator
// delete a route it does not own.
func TestTheSweepDeletesOnTheNameAndNotOnTheLabel(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "sweeper", nil)
	r := newGatewayReconciler("assayd-gateway", "assayd")
	rev := revision.MustHash(a.Spec)
	reconcileOnce(t, r, a)
	reconcileOnce(t, r, a)
	markAvailable(t, ns, controller.WorkloadName("sweeper", rev), 1)
	settle(t, r, a)

	// An impostor: this Agent's labels exactly, a name the operator cannot emit.
	impostor := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name: "not-a-name-this-operator-emits", Namespace: runNS(ns),
			Labels: map[string]string{
				controller.LabelAgent:          "sweeper",
				controller.LabelAgentUID:       string(a.UID),
				controller.LabelRevision:       rev,
				controller.LabelAgentNamespace: ns,
			},
		},
		Spec: gatewayv1.HTTPRouteSpec{Hostnames: []gatewayv1.Hostname{"impostor.invalid"}},
	}
	if err := k8s.Create(context.Background(), impostor); err != nil {
		t.Fatalf("create the impostor route: %v", err)
	}
	reconcileOnce(t, r, a)

	var still gatewayv1.HTTPRoute
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(impostor), &still); err != nil {
		t.Fatalf("the sweep deleted a route it does not own, on the strength of labels alone. "+
			"Design 03 §3.2: the label is NOT the deletion authority, because a label needs only "+
			"`update` to forge; the immutable name is. err=%v", err)
	}
	if servingRoute(t, ns, "sweeper") == nil {
		t.Error("the operator's own route disappeared")
	}
}

// A deleted Agent takes its route with it, and takes it FIRST. Nothing else
// collects it: the Agent is in another namespace, so the route carries no
// ownerReference and Kubernetes will never garbage-collect it. A route
// outliving its Service publishes a hostname that resolves to nothing.
func TestDeletingAnAgentRevokesItsRoute(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "revoked", nil)
	r := newGatewayReconciler("assayd-gateway", "assayd")
	rev := revision.MustHash(a.Spec)
	reconcileOnce(t, r, a)
	reconcileOnce(t, r, a)
	markAvailable(t, ns, controller.WorkloadName("revoked", rev), 1)
	settle(t, r, a)
	if servingRoute(t, ns, "revoked") == nil {
		t.Fatal("no route to revoke")
	}

	if err := k8s.Delete(context.Background(), a); err != nil {
		t.Fatalf("delete agent: %v", err)
	}
	reconcileOnce(t, r, a)

	if rt := servingRoute(t, ns, "revoked"); rt != nil {
		t.Errorf("route %s survived the Agent. It carries no ownerReference — the Agent is in "+
			"another namespace and a cross-namespace owner is treated as absent — so the finalizer "+
			"is the only thing that collects it (design 02 §3.7, design 03 §3.2).", rt.Name)
	}
	var gone assaydv1alpha1.Agent
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &gone); !apierrors.IsNotFound(err) {
		t.Errorf("the finalizer did not release: %v", err)
	}
}

// A reconciler told the gateway is on but given no Gateway must refuse to
// build. A route whose parentRef names "" attaches to nothing, and design 07
// A5.9's reservation compares against exactly the namespace that would be
// missing — the failure it exists to prevent is silent and permissive.
func TestAnEnabledGatewayWithNoGatewayNamedIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  controller.GatewayConfig
		want string
	}{
		{"no name", controller.GatewayConfig{Enabled: true, Namespace: "gw", HostnameSuffix: "x"}, "--gateway-name"},
		{"no namespace", controller.GatewayConfig{Enabled: true, Name: "assayd", HostnameSuffix: "x"}, "--gateway-namespace"},
		{"no hostname suffix", controller.GatewayConfig{Enabled: true, Name: "assayd", Namespace: "gw"}, "--gateway-hostname-suffix"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := controller.NewAgentReconciler(k8s, k8s, scheme, operatorNamespace,
				func() bool { return false }, labelAuthorityPresent,
				controller.InjectedEnvConfig{}, tc.cfg)
			if err == nil {
				t.Fatalf("a reconciler was built with %s unset", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal does not name the missing flag: %v", err)
			}
		})
	}
}

// Design 03 §3.1 says EVERY Agent carries the tier condition, and the status
// paths that build their OWN condition set are where "every" quietly stops
// being true: GovernanceSkipped is owned, so a pass that does not assert it
// CLEARS it. An Agent that goes degraded must not lose the record of which tier
// it runs in — the same defect the reconciler's own comment records for
// SandboxDowngraded and GatesSkipped, one condition later.
func TestEveryAgentCarriesTheTierConditionOnEveryStatusPath(t *testing.T) {
	ns := newNamespace(t)
	r := newReconciler(false)

	// The unresolved-env-source path: a reference to a ConfigMap that is not
	// there, so no revision is minted and reportUnresolvedSources answers.
	bad := mustCreateAgent(t, ns, "notiersrc", func(a *assaydv1alpha1.Agent) {
		a.Spec.Runtime.EnvFrom = []corev1.EnvFromSource{{
			ConfigMapRef: &corev1.ConfigMapEnvSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: "nothing-here"},
			},
		}}
	})
	reconcileOnce(t, r, bad)
	reconcileOnce(t, r, bad)
	var live assaydv1alpha1.Agent
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(bad), &live); err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if c := condition(&live, assaydv1alpha1.CondEnvSourceUnresolved); c == nil || c.Status != metav1.ConditionTrue {
		t.Fatalf("the agent never reached the unresolved-sources path, so this test is measuring "+
			"nothing: %+v", live.Status.Conditions)
	}
	if c := condition(&live, assaydv1alpha1.CondGovernanceSkipped); c == nil ||
		c.Status != metav1.ConditionTrue || c.Reason != "GatewayDisabled" {
		t.Errorf("an Agent on the unresolved-sources path carries %v for GovernanceSkipped. That "+
			"path builds its own condition set and the condition is owned, so failing to assert "+
			"it CLEARS it — and the tier a degraded Agent runs in disappears exactly when "+
			"somebody is looking at it.", c)
	}

	// An EXTERNAL agent, whose whole lifecycle is elsewhere. It is the case
	// where the tier matters most: nothing about it is in this cluster but the
	// record.
	ext := mustCreateAgent(t, ns, "notierext", func(a *assaydv1alpha1.Agent) {
		a.Spec.Runtime = nil
		a.Spec.External = &assaydv1alpha1.ExternalAgent{Endpoint: "https://elsewhere.example/a2a"}
	})
	reconcileOnce(t, r, ext)
	reconcileOnce(t, r, ext)
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(ext), &live); err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if c := condition(&live, assaydv1alpha1.CondGovernanceSkipped); c == nil ||
		c.Status != metav1.ConditionTrue || c.Reason != "GatewayDisabled" {
		t.Errorf("an external agent carries %v for GovernanceSkipped", c)
	}
}

// Drift on the emitted route is CORRECTED, and the fields that matter most are
// the ones the renderer leaves empty.
//
// `ensureServingRoute` assigns `spec.rules` wholesale, so the operator writes
// `filters`, `matches` and `timeouts` on every pass — but `equalRoute` decides
// whether that write happens, and a field it does not compare is a field the
// operator will never notice and never correct. A `RequestMirror` filter planted
// by anyone with `httproutes` update in a run namespace copies every request to
// an agent somewhere else, and it is invisible in `kubectl get agent`.
//
// Design 03 §3.2 reserves route AUTHORSHIP to the operator through design 07
// A5.9's admission policy, but that policy is the chart's and matches CREATE and
// UPDATE on routes naming the assayd Gateway. Relying on it and not converging
// is a control this reconciler leans on and does not itself perform — rule 5.
func TestTheOperatorCorrectsDriftOnTheRouteItEmitted(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "drifted", nil)
	r := newGatewayReconciler("assayd-gateway", "assayd")
	rev := revision.MustHash(a.Spec)
	reconcileOnce(t, r, a)
	reconcileOnce(t, r, a)
	markAvailable(t, ns, controller.WorkloadName("drifted", rev), 1)
	settle(t, r, a)

	rt := servingRoute(t, ns, "drifted")
	if rt == nil {
		t.Fatal("no route to drift")
	}
	mirror := gatewayv1.ObjectName("somewhere-else")
	rt.Spec.Rules[0].Filters = []gatewayv1.HTTPRouteFilter{{
		Type: gatewayv1.HTTPRouteFilterRequestMirror,
		RequestMirror: &gatewayv1.HTTPRequestMirrorFilter{
			BackendRef: gatewayv1.BackendObjectReference{
				Name: mirror, Port: ptrTo(gatewayv1.PortNumber(8080)),
			},
		},
	}}
	// And a narrowed path, which drops every other request on this hostname
	// with a 404 and reports nothing anywhere.
	rt.Spec.Rules[0].Matches = []gatewayv1.HTTPRouteMatch{{
		Path: &gatewayv1.HTTPPathMatch{
			Type:  ptrTo(gatewayv1.PathMatchExact),
			Value: ptrTo("/healthz"),
		},
	}}
	if err := k8s.Update(context.Background(), rt); err != nil {
		t.Fatalf("plant the drift: %v", err)
	}

	reconcileOnce(t, r, a)

	back := servingRoute(t, ns, "drifted")
	if back == nil {
		t.Fatal("the route disappeared")
	}
	if len(back.Spec.Rules[0].Filters) != 0 {
		t.Errorf("a RequestMirror filter planted on the emitted route survived a reconcile: %+v. "+
			"equalRoute does not compare the field, so the operator writes it on every pass and "+
			"never notices it changed — every request to this agent is copied to %s and nothing "+
			"in the Agent's status says so.", back.Spec.Rules[0].Filters, mirror)
	}
	m := back.Spec.Rules[0].Matches
	if len(m) != 1 || m[0].Path == nil || *m[0].Path.Type != gatewayv1.PathMatchPathPrefix ||
		*m[0].Path.Value != "/" {
		t.Errorf("a narrowed path match survived a reconcile: %+v. Everything but that path now "+
			"404s on this agent's hostname, and no condition reports it.", m)
	}
}

// refusingRouteClient refuses every HTTPRoute write and passes everything else
// through, so a test can reach the route-apply failure path without breaking
// the run namespace, the workload or the Service on the way to it.
type refusingRouteClient struct {
	client.Client
	err error
}

func (c *refusingRouteClient) Create(ctx context.Context, o client.Object, opts ...client.CreateOption) error {
	if _, ok := o.(*gatewayv1.HTTPRoute); ok {
		return c.err
	}
	return c.Client.Create(ctx, o, opts...)
}

func (c *refusingRouteClient) Update(ctx context.Context, o client.Object, opts ...client.UpdateOption) error {
	if _, ok := o.(*gatewayv1.HTTPRoute); ok {
		return c.err
	}
	return c.Client.Update(ctx, o, opts...)
}

// A route that could not be written is a degradation and must SAY Degraded, not
// merely set the phase.
//
// `CondDegraded` is owned and non-sticky, so a path that sets `phase: Degraded`
// without asserting the condition actively CLEARS a previous `Degraded=True` —
// and design 10 keys its alerting on the condition. The Agent would be
// unreachable through the Gateway while nothing paged. The identical defect is
// recorded in the reconciler on the WorkloadUnavailable branch; this path
// reintroduced it and the independent review of A6.10 caught it.
func TestARouteThatCannotBeWrittenIsALoudDegradation(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "routefail", nil)
	r := newGatewayReconciler("assayd-gateway", "assayd")
	rev := revision.MustHash(a.Spec)
	reconcileOnce(t, r, a)
	reconcileOnce(t, r, a)
	markAvailable(t, ns, controller.WorkloadName("routefail", rev), 1)

	r.Client = &refusingRouteClient{
		Client: k8s,
		err: apierrors.NewForbidden(
			schema.GroupResource{Group: "gateway.networking.k8s.io", Resource: "httproutes"},
			"routefail-serving", fmt.Errorf("the ClusterRole grants no httproutes")),
	}
	// The error reaches the caller: a route the operator could not write must
	// retry, not be swallowed.
	if _, err := r.Reconcile(context.Background(),
		ctrl.Request{NamespacedName: client.ObjectKeyFromObject(a)}); err == nil {
		t.Fatal("a refused route write returned no error, so nothing requeues and the agent " +
			"stays unreachable through the Gateway forever")
	}

	var live assaydv1alpha1.Agent
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &live); err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if live.Status.Phase != assaydv1alpha1.PhaseDegraded {
		t.Errorf("phase is %q, want Degraded", live.Status.Phase)
	}
	if c := condition(&live, assaydv1alpha1.CondDegraded); c == nil || c.Status != metav1.ConditionTrue {
		t.Errorf("Degraded is %v while the phase is Degraded. The condition is owned and "+
			"non-sticky, so a path that sets only the phase CLEARS it — and design 10 alerts on "+
			"the condition, so this agent would be unreachable through the Gateway with nobody "+
			"paged.", c)
	}
	if c := condition(&live, assaydv1alpha1.CondPolicyApplyIncomplete); c == nil ||
		c.Status != metav1.ConditionTrue || c.Reason != "RouteApplyFailed" {
		t.Errorf("PolicyApplyIncomplete is %v, want True/RouteApplyFailed", c)
	}
	if c := condition(&live, assaydv1alpha1.CondReady); c == nil || c.Status != metav1.ConditionTrue {
		// Ready must be FALSE here; this branch asserts the opposite deliberately
		// so the message reads correctly when it fires.
		if c == nil || c.Status != metav1.ConditionFalse {
			t.Errorf("Ready is %v, want False: on a gateway-enabled install an agent with no "+
				"route is not reachable through the Gateway", c)
		}
	}
}

// A LOST RACE is not a degradation. An AlreadyExists from a stale informer
// cache resolves on the next pass, and flipping Ready for it would page the
// on-call for cache lag — the same treatment ensureService gives the same race.
func TestALostRaceOnTheRouteDoesNotFlipReady(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "routerace", nil)
	r := newGatewayReconciler("assayd-gateway", "assayd")
	rev := revision.MustHash(a.Spec)
	reconcileOnce(t, r, a)
	reconcileOnce(t, r, a)
	markAvailable(t, ns, controller.WorkloadName("routerace", rev), 1)

	r.Client = &refusingRouteClient{
		Client: k8s,
		err: apierrors.NewAlreadyExists(
			schema.GroupResource{Group: "gateway.networking.k8s.io", Resource: "httproutes"},
			"routerace-serving"),
	}
	if _, err := r.Reconcile(context.Background(),
		ctrl.Request{NamespacedName: client.ObjectKeyFromObject(a)}); err == nil {
		t.Fatal("a lost race returned no error, so nothing requeues to converge the route")
	}

	var live assaydv1alpha1.Agent
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &live); err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if live.Status.Phase == assaydv1alpha1.PhaseDegraded {
		t.Errorf("a stale-cache AlreadyExists made the agent Degraded. It resolves on the next "+
			"pass; reporting it degrades pages the on-call for informer lag: %+v", live.Status)
	}
	if c := condition(&live, assaydv1alpha1.CondReady); c == nil || c.Status != metav1.ConditionTrue {
		t.Errorf("Ready is %v after a lost race; the agent is serving and nothing about it "+
			"changed", c)
	}
}
