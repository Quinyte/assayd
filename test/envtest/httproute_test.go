// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package envtest

import (
	"context"
	"fmt"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/compiler"
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
	name, err := compiler.ServingRouteName(agentName)
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

// The route-shape tests in this file use `auth: none` Agents, whose route is
// published at once (design 03 §3.3.1): what they pin is the route, its drift
// correction and its sweep. An API-key Agent's route is prepared and published
// only through `Create`'s probe, and authcreate_test.go pins that.
func TestTheOperatorEmitsTheServingRouteWhenTheGatewayIsDeclared(t *testing.T) {
	ns := newNamespace(t)
	a := noneAgent(t, ns, "routed")
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
	// Converged at the Gateway, so the `Create` of `none` reaches Served and
	// the counting below measures the steady state.
	acceptRoute(t, ns, "routed")
	settle(t, r, a)
	if got := liveAgent(t, a).Status.Auth; got == nil || got.Mode != "none" || got.Transaction != nil {
		t.Fatalf("an auth: none Agent's Create did not reach Served: status.auth=%+v", got)
	}
	rt = servingRoute(t, ns, "routed")
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
	a := noneAgent(t, ns, "moves")
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
	a := noneAgent(t, ns, "sweeper")
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
	a := noneAgent(t, ns, "revoked")
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

// Drift on the emitted route is CORRECTED — one field at a time.
//
// `ensureServingRoute` assigns `spec.rules` wholesale, so the operator writes
// every field below on every pass; `equalRoute` decides whether that write
// happens, and a field it does not compare is a field the operator never
// notices and never corrects. A `RequestMirror` planted by anyone with
// `httproutes` update in a run namespace copies every request to an agent
// somewhere else, and it is invisible in `kubectl get agent`.
//
// ONE planted drift per case, and that is the point of the table. The first
// version planted a mirror AND a narrowed match in one update; because the
// writer replaces the rules wholesale, either comparison firing corrected both,
// so deleting either comparison left the test green. Each case below is killed
// by deleting exactly the comparison it names, and survives the deletion of any
// other.
//
// Design 03 §3.2 reserves route AUTHORSHIP to the operator through design 07
// A5.9's admission policy, but that policy is the chart's, matches only CREATE
// and UPDATE, and is not running here. Relying on it and not converging would
// be a control this reconciler leans on and does not itself perform — rule 5.
func TestTheOperatorCorrectsDriftOnEachFieldOfTheRouteItEmitted(t *testing.T) {
	mirror := func() []gatewayv1.HTTPRouteFilter {
		return []gatewayv1.HTTPRouteFilter{{
			Type: gatewayv1.HTTPRouteFilterRequestMirror,
			RequestMirror: &gatewayv1.HTTPRequestMirrorFilter{
				BackendRef: gatewayv1.BackendObjectReference{
					Name: "somewhere-else", Port: ptrTo(gatewayv1.PortNumber(8080)),
				},
			},
		}}
	}
	for i, tc := range []struct {
		field string
		harm  string
		plant func(*gatewayv1.HTTPRoute)
	}{
		{"rules[].filters", "every request to this agent is copied to somewhere-else",
			func(rt *gatewayv1.HTTPRoute) { rt.Spec.Rules[0].Filters = mirror() }},
		{"rules[].matches", "everything but /healthz 404s on this agent's hostname",
			func(rt *gatewayv1.HTTPRoute) {
				rt.Spec.Rules[0].Matches = []gatewayv1.HTTPRouteMatch{{Path: &gatewayv1.HTTPPathMatch{
					Type: ptrTo(gatewayv1.PathMatchExact), Value: ptrTo("/healthz"),
				}}}
			}},
		{"rules[].backendRefs[].filters", "every request to this agent is copied to somewhere-else, " +
			"one level below where the first review looked",
			func(rt *gatewayv1.HTTPRoute) { rt.Spec.Rules[0].BackendRefs[0].Filters = mirror() }},
		{"rules[].backendRefs[].namespace", "the ref needs a ReferenceGrant nobody wrote, resolves to " +
			"nothing, and the agent is off the air while reporting Ready",
			func(rt *gatewayv1.HTTPRoute) {
				rt.Spec.Rules[0].BackendRefs[0].Namespace = ptrTo(gatewayv1.Namespace("default"))
			}},
		{"parentRefs", "the route attaches to a different Gateway, or to none, and 404s",
			func(rt *gatewayv1.HTTPRoute) { rt.Spec.ParentRefs[0].Name = "some-other-gateway" }},
		{"parentRefs[].port", "the port must match the listener as well as the section name, so " +
			"the route attaches to no listener and the agent is off the air while reporting Ready",
			func(rt *gatewayv1.HTTPRoute) {
				rt.Spec.ParentRefs[0].Port = ptrTo(gatewayv1.PortNumber(9999))
			}},
		{"metadata.labels[agent-uid]", "the sweep and the finalizer find this Agent's route by " +
			"that label, so a route whose label was edited is skipped by both: it outlives the " +
			"Agent and keeps publishing a hostname to nothing",
			func(rt *gatewayv1.HTTPRoute) { rt.Labels[controller.LabelAgentUID] = "forged-uid" }},
		{"hostnames", "the agent answers on a host nobody calls, and its own host 404s",
			func(rt *gatewayv1.HTTPRoute) {
				rt.Spec.Hostnames = []gatewayv1.Hostname{"someone-else.example"}
			}},
	} {
		t.Run(tc.field, func(t *testing.T) {
			ns := newNamespace(t)
			agentName := fmt.Sprintf("drift%d", i)
			a := noneAgent(t, ns, agentName)
			r := newGatewayReconciler("assayd-gateway", "assayd")
			rev := revision.MustHash(a.Spec)
			reconcileOnce(t, r, a)
			reconcileOnce(t, r, a)
			markAvailable(t, ns, controller.WorkloadName(agentName, rev), 1)
			settle(t, r, a)

			rt := servingRoute(t, ns, agentName)
			if rt == nil {
				t.Fatal("no route to drift")
			}
			want := rt.Spec.DeepCopy()
			// The four provenance labels are owned too; the spec comparison
			// below would not see a label edit.
			owned := func(r *gatewayv1.HTTPRoute) map[string]string {
				return map[string]string{
					controller.LabelAgent:          r.Labels[controller.LabelAgent],
					controller.LabelRevision:       r.Labels[controller.LabelRevision],
					controller.LabelAgentUID:       r.Labels[controller.LabelAgentUID],
					controller.LabelAgentNamespace: r.Labels[controller.LabelAgentNamespace],
				}
			}
			wantLabels := owned(rt)
			tc.plant(rt)
			if err := k8s.Update(context.Background(), rt); err != nil {
				t.Fatalf("plant the drift: %v", err)
			}
			// The plant must have LANDED, or this case measures nothing: a
			// field the CRD strips or defaults back would make a passing
			// assertion below say nothing about equalRoute.
			if planted := servingRoute(t, ns, agentName); planted == nil ||
				(equality.Semantic.DeepEqual(planted.Spec, *want) &&
					equality.Semantic.DeepEqual(owned(planted), wantLabels)) {
				t.Fatalf("the drift on %s did not survive the API server, so this case cannot "+
					"observe whether the operator corrects it", tc.field)
			}

			reconcileOnce(t, r, a)

			back := servingRoute(t, ns, agentName)
			if back == nil {
				t.Fatal("the route disappeared")
			}
			if !equality.Semantic.DeepEqual(back.Spec, *want) ||
				!equality.Semantic.DeepEqual(owned(back), wantLabels) {
				t.Errorf("drift on %s survived a reconcile — %s, and nothing in the Agent's status "+
					"says so. equalRoute does not compare the field, so the operator writes it on "+
					"every pass and never notices it changed.\nwant %+v %v\ngot  %+v %v",
					tc.field, tc.harm, *want, wantLabels, back.Spec, owned(back))
			}
		})
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
//
// Two halves since design 03 §3.3.1's aggregation reached this path (A71): an
// Agent that was SERVING goes Degraded, and one whose `Create` never served
// goes Pending. PolicyApplyIncomplete and Ready=False are the same for both,
// and PolicyApplyIncomplete is what design 10 pages on.
func TestARouteThatCannotBeWrittenIsALoudDegradation(t *testing.T) {
	refuse := func(r *controller.AgentReconciler, name string) {
		r.Client = &refusingRouteClient{
			Client: k8s,
			err: apierrors.NewForbidden(
				schema.GroupResource{Group: "gateway.networking.k8s.io", Resource: "httproutes"},
				name+"-serving", fmt.Errorf("the ClusterRole grants no httproutes")),
		}
	}
	t.Run("served", func(t *testing.T) {
		ns := newNamespace(t)
		a := noneAgent(t, ns, "routefail")
		r := newGatewayReconciler("assayd-gateway", "assayd")
		rev := revision.MustHash(a.Spec)
		reconcileOnce(t, r, a)
		reconcileOnce(t, r, a)
		markAvailable(t, ns, controller.WorkloadName("routefail", rev), 1)
		settle(t, r, a)
		acceptRoute(t, ns, "routefail")
		settle(t, r, a)
		if auth := liveAgent(t, a).Status.Auth; auth == nil || auth.Mode != "none" {
			t.Fatalf("the Agent was not served before its route write was refused: %+v", auth)
		}
		// Drift, so the next pass must write the route.
		rt := servingRoute(t, ns, "routefail")
		rt.Spec.Hostnames = []gatewayv1.Hostname{"someone-else.example"}
		if err := k8s.Update(context.Background(), rt); err != nil {
			t.Fatal(err)
		}
		refuse(r, "routefail")
		// The error reaches the caller: a route the operator could not write
		// must retry, not be swallowed.
		if _, err := r.Reconcile(context.Background(),
			ctrl.Request{NamespacedName: client.ObjectKeyFromObject(a)}); err == nil {
			t.Fatal("a refused route write returned no error, so nothing requeues and the agent " +
				"stays unreachable through the Gateway forever")
		}
		live := liveAgent(t, a)
		if live.Status.Phase != assaydv1alpha1.PhaseDegraded {
			t.Errorf("phase is %q, want Degraded", live.Status.Phase)
		}
		if c := condition(live, assaydv1alpha1.CondDegraded); c == nil || c.Status != metav1.ConditionTrue {
			t.Errorf("Degraded is %v while the phase is Degraded. The condition is owned and "+
				"non-sticky, so a path that sets only the phase CLEARS it — and design 10 alerts on "+
				"the condition, so this agent would be unreachable through the Gateway with nobody "+
				"paged.", c)
		}
		assertRouteFailureReported(t, live)
	})
	t.Run("never served", func(t *testing.T) {
		ns := newNamespace(t)
		a := noneAgent(t, ns, "routefail")
		r := newGatewayReconciler("assayd-gateway", "assayd")
		reconcileOnce(t, r, a)
		reconcileOnce(t, r, a)
		markAvailable(t, ns, controller.WorkloadName("routefail", revision.MustHash(a.Spec)), 1)
		refuse(r, "routefail")
		if _, err := r.Reconcile(context.Background(),
			ctrl.Request{NamespacedName: client.ObjectKeyFromObject(a)}); err == nil {
			t.Fatal("a refused route write returned no error")
		}
		live := liveAgent(t, a)
		if live.Status.Phase != assaydv1alpha1.PhasePending {
			t.Errorf("phase is %q; an Agent whose Create never served is Pending (design 03 §3.3.1)",
				live.Status.Phase)
		}
		if c := condition(live, assaydv1alpha1.CondDegraded); c != nil {
			t.Errorf("Degraded is %v on an Agent that never served", c)
		}
		assertRouteFailureReported(t, live)
	})
}

func assertRouteFailureReported(t *testing.T, live *assaydv1alpha1.Agent) {
	t.Helper()
	if c := condition(live, assaydv1alpha1.CondPolicyApplyIncomplete); c == nil ||
		c.Status != metav1.ConditionTrue || c.Reason != "RouteApplyFailed" {
		t.Errorf("PolicyApplyIncomplete is %v, want True/RouteApplyFailed", c)
	}
	// One guard, stated the right way round. An earlier version nested this
	// inside `c.Status != ConditionTrue`, so Ready=True — the single outcome
	// the test exists to forbid — skipped the check entirely, and deleting the
	// reconciler's `Ready=False` for this path left the test green.
	if c := condition(live, assaydv1alpha1.CondReady); c == nil || c.Status != metav1.ConditionFalse {
		t.Errorf("Ready is %v, want False: on a gateway-enabled install an agent with no "+
			"route is not reachable through the Gateway", c)
	}
}

// A LOST RACE is not a degradation. An AlreadyExists from a stale informer
// cache resolves on the next pass, and marking the Agent Degraded for it would
// page the on-call for cache lag — the same treatment ensureService gives the
// same race.
//
// It is not a reason to claim Ready either. This Agent's Create has not
// published its route: the race was over that very write. So Ready is False
// and the phase is Pending, as on the Create's other passes, and nothing is
// Degraded. An earlier version of this test asserted Ready=True, on the
// premise that the agent was serving and nothing about it had changed. Under
// design 03's Create that premise is false (A71).
func TestALostRaceOnTheRouteDoesNotFlipReady(t *testing.T) {
	ns := newNamespace(t)
	a := noneAgent(t, ns, "routerace")
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
	if c := condition(&live, assaydv1alpha1.CondDegraded); c != nil {
		t.Errorf("a lost race raised Degraded on an Agent that never served: %+v", c)
	}
	if live.Status.Phase != assaydv1alpha1.PhasePending {
		t.Errorf("phase is %q after a lost race in a Create; want Pending", live.Status.Phase)
	}
	if c := condition(&live, assaydv1alpha1.CondReady); c == nil || c.Status != metav1.ConditionFalse {
		t.Errorf("Ready is %v after a lost race in a Create whose route is not published; it "+
			"must not claim the Agent is reachable", c)
	}
}

// A port edit mints a new revision, and until that revision is serving the
// route must keep the OLD one's port.
//
// The route names the serving revision's Service; the spec describes the
// desired one. An earlier emitter read the port off the spec, so a port edit
// whose revision never came up — a bad image, a crashloop — rewrote the healthy
// revision's route to `<agent>-R1:9090`, a port R1's Service does not publish.
// Every request through the gateway failed from that moment, indefinitely, and
// nothing reported it: the operator does not read route status. That is an edit
// that was NOT promoted taking down the revision that is serving, which is the
// failure the per-revision Service exists to prevent.
func TestAPortChangeThatNeverComesUpDoesNotMoveTheServingRoute(t *testing.T) {
	ns := newNamespace(t)
	a := noneAgent(t, ns, "ported")
	r := newGatewayReconciler("assayd-gateway", "assayd")
	first := revision.MustHash(a.Spec)
	reconcileOnce(t, r, a)
	reconcileOnce(t, r, a)
	markAvailable(t, ns, controller.WorkloadName("ported", first), 1)
	settle(t, r, a)

	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), a); err != nil {
		t.Fatalf("re-read agent: %v", err)
	}
	a.Spec.Runtime.Port = 9090
	if err := k8s.Update(context.Background(), a); err != nil {
		t.Fatalf("update agent: %v", err)
	}
	second := revision.MustHash(a.Spec)
	if second == first {
		t.Fatal("a port edit did not mint a new revision; this test is not exercising a rollout")
	}
	// The second revision never becomes available.
	live := settle(t, r, a)
	if live.Status.ActiveRevision != first {
		t.Fatalf("the unavailable revision was promoted, so this test cannot say anything about "+
			"the route of the one still serving: %+v", live.Status)
	}

	backendOf := func() (string, int32) {
		t.Helper()
		rt := servingRoute(t, ns, "ported")
		if rt == nil || len(rt.Spec.Rules) != 1 || len(rt.Spec.Rules[0].BackendRefs) != 1 ||
			rt.Spec.Rules[0].BackendRefs[0].Port == nil {
			t.Fatalf("no single-backend serving route: %+v", rt)
		}
		b := rt.Spec.Rules[0].BackendRefs[0]
		return string(b.Name), int32(*b.Port)
	}
	var svc corev1.Service
	if err := k8s.Get(context.Background(), types.NamespacedName{
		Namespace: runNS(ns), Name: controller.WorkloadName("ported", first),
	}, &svc); err != nil {
		t.Fatalf("read the serving revision's Service: %v", err)
	}
	published := svc.Spec.Ports[0].Port
	if name, p := backendOf(); name != controller.WorkloadName("ported", first) || p != published {
		t.Errorf("while the port edit's revision is not serving, the route names %s:%d; want "+
			"%s:%d, the port the serving revision's Service actually publishes. A route naming a "+
			"port its Service does not publish resolves to nothing, and the operator does not "+
			"read route status, so nothing would say so.",
			name, p, controller.WorkloadName("ported", first), published)
	}

	// And once the new revision IS serving, the route follows it — port and all.
	markAvailable(t, ns, controller.WorkloadName("ported", second), 1)
	if live = settle(t, r, a); live.Status.ActiveRevision != second {
		t.Fatalf("the second revision never promoted: %+v", live.Status)
	}
	if name, p := backendOf(); name != controller.WorkloadName("ported", second) || p != 9090 {
		t.Errorf("after promotion the route names %s:%d, want %s:9090",
			name, p, controller.WorkloadName("ported", second))
	}
}

// Design 03 §3.2: "a suffix collision is a compile error naming both inputs,
// never a silent reuse". A route at this Agent's name that ANOTHER Agent owns
// is refused and left exactly as it was.
//
// Before this check, update authority was looser than delete authority: the
// sweep required the UID label to match before deleting, while the converge
// path rewrote whatever it found by name.
func TestARouteAnotherAgentOwnsIsRefusedNotTakenOver(t *testing.T) {
	ns := newNamespace(t)
	a := noneAgent(t, ns, "claimant")
	r := newGatewayReconciler("assayd-gateway", "assayd")
	rev := revision.MustHash(a.Spec)
	reconcileOnce(t, r, a)
	reconcileOnce(t, r, a)

	name, err := compiler.ServingRouteName("claimant")
	if err != nil {
		t.Fatalf("route name: %v", err)
	}
	foreign := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: runNS(ns),
			Labels: map[string]string{
				controller.LabelAgent:          "incumbent",
				controller.LabelAgentUID:       "uid-of-another-agent",
				controller.LabelAgentNamespace: ns,
				controller.LabelRevision:       "0000000000",
			},
		},
		Spec: gatewayv1.HTTPRouteSpec{Hostnames: []gatewayv1.Hostname{"incumbent.invalid"}},
	}
	if err := k8s.Create(context.Background(), foreign); err != nil {
		t.Fatalf("create the incumbent's route: %v", err)
	}
	markAvailable(t, ns, controller.WorkloadName("claimant", rev), 1)

	_, err = r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(a)})
	if err == nil {
		t.Fatal("a route another Agent owns was converged without complaint")
	}
	if !strings.Contains(err.Error(), "incumbent") || !strings.Contains(err.Error(), "claimant") {
		t.Errorf("the refusal does not name both inputs, which §3.2 requires: %v", err)
	}

	var still gatewayv1.HTTPRoute
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(foreign), &still); err != nil {
		t.Fatalf("the incumbent's route is gone: %v", err)
	}
	if still.Labels[controller.LabelAgentUID] != "uid-of-another-agent" ||
		len(still.Spec.Hostnames) != 1 || still.Spec.Hostnames[0] != "incumbent.invalid" {
		t.Errorf("another Agent's route was rewritten: labels=%v hostnames=%v",
			still.Labels, still.Spec.Hostnames)
	}

	var live assaydv1alpha1.Agent
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &live); err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if c := condition(&live, assaydv1alpha1.CondDegraded); c == nil || c.Status != metav1.ConditionTrue ||
		c.Reason != "RouteApplyFailed" {
		t.Errorf("a collision is a degradation the Agent must report; Degraded is %v", c)
	}
}

// The other half of the rule: a route at this Agent's name whose identity
// labels are THIS Agent's name and namespace, under a different UID, is a
// predecessor's — an Agent deleted while the gateway was declared off sweeps
// nothing — and is adopted. Refusing it would wedge a recreated Agent forever
// behind a route nothing will ever remove.
func TestAPredecessorsRouteIsAdoptedNotRefused(t *testing.T) {
	ns := newNamespace(t)
	a := noneAgent(t, ns, "heir")
	r := newGatewayReconciler("assayd-gateway", "assayd")
	rev := revision.MustHash(a.Spec)
	reconcileOnce(t, r, a)
	reconcileOnce(t, r, a)

	name, err := compiler.ServingRouteName("heir")
	if err != nil {
		t.Fatalf("route name: %v", err)
	}
	old := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: runNS(ns),
			Labels: map[string]string{
				controller.LabelAgent:          "heir",
				controller.LabelAgentUID:       "uid-of-a-deleted-predecessor",
				controller.LabelAgentNamespace: ns,
				controller.LabelRevision:       "0000000000",
			},
		},
		Spec: gatewayv1.HTTPRouteSpec{Hostnames: []gatewayv1.Hostname{"stale.invalid"}},
	}
	if err := k8s.Create(context.Background(), old); err != nil {
		t.Fatalf("create the predecessor's route: %v", err)
	}
	markAvailable(t, ns, controller.WorkloadName("heir", rev), 1)
	settle(t, r, a)

	rt := servingRoute(t, ns, "heir")
	if rt == nil {
		t.Fatal("the route is gone")
	}
	if rt.UID != old.UID {
		t.Errorf("the predecessor's route was replaced rather than adopted")
	}
	if got := rt.Labels[controller.LabelAgentUID]; got != string(a.UID) {
		t.Errorf("the adopted route carries agent-uid %q, want this Agent's %q", got, a.UID)
	}
	wantHost := "heir." + ns + "." + controller.DefaultGatewayHostnameSuffix
	if len(rt.Spec.Hostnames) != 1 || string(rt.Spec.Hostnames[0]) != wantHost {
		t.Errorf("the adopted route still carries %v, want %s", rt.Spec.Hostnames, wantHost)
	}
}
