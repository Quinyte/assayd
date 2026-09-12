// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"errors"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/compiler"
)

// ADR-0030 step 3, SECOND half, and design 03's first slice on top of it: the
// serving route this file emits is now published through the `-auth`
// transaction in authtxn.go. A new Agent's route is PREPARED, with no
// backendRefs, and published only once an anonymous request through it gets
// `401` from the `<agent>-auth` policy the operator wrote (design 03 §3.3.3).
// An `auth: none` Agent's route is published at once, with the marker label
// `assayd.dev/auth: "none"` (§3.3.1).
//
// Only design 03's first slice is approved (2026-09-12), and this is part of
// it. Everything else design 03 describes is still absent: no
// AgentgatewayBackend, no budget, no rate limit, no tool allowlist.
const (
	// GatewayListenerName is the listener an agent's serving route attaches to.
	//
	// It is a constant rather than a value because nothing in this repository
	// renders a Gateway: design 07 A1 still owes the agentgateway subchart, so
	// the only Gateways that exist are the e2e harness's and whatever an
	// operator wrote by hand, and both name the agent-serving listener `http`
	// (design 07 A5.3). A Gateway whose listener is named otherwise will report
	// `NoMatchingParent` on the route rather than serving it — said here because
	// the operator does not read route status, so nothing else would say it.
	GatewayListenerName = "http"
)

// GatewayConfig is what the operator is told about the Gateway it authors
// routes against. DECLARED, never discovered — design 03 §3.1: "whether the
// compiler runs at all is declared, not discovered", so that `true` with the
// CRDs absent can only mean a broken install rather than an ambiguous one.
type GatewayConfig struct {
	// Enabled is `gateway.enabled`. False — what P1 ships — emits nothing and
	// puts GovernanceSkipped=GatewayDisabled on every Agent.
	Enabled bool
	// Name and Namespace are the Gateway a route's parentRef names. Namespace
	// is NOT the operator's: design 07 A6.2 measured the reservation failing
	// open when the two were conflated, and agentgateway's proxy cannot run in
	// the PodSecurity-`restricted` namespace the operator does (A6.3).
	Name      string
	Namespace string
	// HostnameSuffix is appended to `<agent>.<agent-namespace>` to make the
	// route's hostname. Configurable because **no design settles it**: design 03
	// §3.2 fixes where the route lives and what names the OBJECT and is silent
	// on the hostname the route matches, so this is assayd's choice and is
	// stated as one rather than presented as the design's.
	HostnameSuffix string
	// ServingURL is --gateway-serving-url: the base URL of the Gateway's
	// serving listener, where design 03 §3.3.3's anonymous probe goes. The
	// compiler runs whenever the gateway is enabled, so NewAgentReconciler
	// requires it then, as §3.3.3 says, and checks its form.
	ServingURL string
}

// DefaultGatewayHostnameSuffix is what an install gets when it says nothing.
//
// `.internal` is reserved by ICANN for private use, so this cannot collide with
// a name anyone can register. It is a ROUTING KEY, not an address: the Gateway
// matches it against the request's Host header and nothing resolves it, so
// reaching an agent by this name needs DNS an administrator provides. Nothing
// here creates that DNS, and this comment is the only place that says so.
const DefaultGatewayHostnameSuffix = "assayd.internal"

// Hostname is the host an agent answers on through the Gateway. Per AGENT, not
// per revision: the route is one object whose backendRef moves between
// revisions (see servingRouteFor), so a caller's address does not change when a
// revision is promoted.
func (g GatewayConfig) Hostname(agentName, agentNamespace string) string {
	return agentName + "." + agentNamespace + "." + g.HostnameSuffix
}

// servingRouteFor renders the route for one Agent, pointing at one revision.
//
// `backendPort` is the port that revision's SERVICE publishes, and it is a
// parameter precisely so that it cannot be read off `agent.Spec`. The spec is
// the DESIRED revision; the route names the SERVING one. They differ for the
// whole of every rollout, and a port edit is behaviour surface (ADR-0031), so it
// mints a new revision while the old one keeps answering on the old port. An
// earlier version read `port(agent.Spec.Runtime)` here: a port edit whose
// revision never came up rewrote the serving route to `<agent>-R1:<new port>`,
// a port R1's Service does not publish, and took down the revision that was
// still healthy — the exact failure the per-revision Service exists to prevent.
//
// **Ownership is a label AND the name is the deletion authority** (design 03
// §3.2). No ownerReference: the Agent is in another namespace, and a
// cross-namespace ownerReference is treated as ABSENT with an
// `OwnerRefInvalidNamespace` Event — so setting one would leak every emitted
// route silently while looking correct. `assayd.dev/agent-uid` and
// `assayd.dev/revision` are the design's provenance labels; the Agent's name
// and namespace are stamped too, because that is what the watch maps back to a
// reconcile request and what the list selector narrows on. None of the four is
// evidence: a label needs only `update` to forge, so ownedRoutes additionally
// requires the immutable NAME to match.
func (r *AgentReconciler) servingRouteFor(
	agent *assaydv1alpha1.Agent, runNS, rev, name string, servicePort int32, pub routePublication,
) *gatewayv1.HTTPRoute {
	group := gatewayv1.Group(gatewayv1.GroupName)
	gwKind := gatewayv1.Kind("Gateway")
	svcGroup := gatewayv1.Group("")
	svcKind := gatewayv1.Kind("Service")
	gwNS := gatewayv1.Namespace(r.Gateway.Namespace)
	section := gatewayv1.SectionName(GatewayListenerName)
	backendPort := gatewayv1.PortNumber(servicePort)
	weight := int32(100)

	route := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			// The RUN namespace, with the Service it names. A backendRef across
			// namespaces needs a ReferenceGrant and without one the route reports
			// `Accepted=True` beside `ResolvedRefs=False, RefNotPermitted` —
			// design 03 §3.2 records that leaving the route behind wedges every
			// Agent at every install. The parentRef to a Gateway in another
			// namespace needs no grant; only backendRefs and Secrets do.
			Namespace: runNS,
			// The marker is decided by the publication and nothing else:
			// present exactly while the route is published under `none`
			// (design 03 §3.3.1).
			Labels: compiler.RouteAuthLabels(pub.mode, map[string]string{
				LabelAgent:          agent.Name,
				LabelRevision:       rev,
				LabelAgentUID:       string(agent.UID),
				LabelAgentNamespace: agent.Namespace,
			}),
		},
		Spec: gatewayv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{
				ParentRefs: []gatewayv1.ParentReference{{
					Group:       &group,
					Kind:        &gwKind,
					Name:        gatewayv1.ObjectName(r.Gateway.Name),
					Namespace:   &gwNS,
					SectionName: &section,
				}},
			},
			Hostnames: []gatewayv1.Hostname{
				gatewayv1.Hostname(r.Gateway.Hostname(agent.Name, agent.Namespace)),
			},
			Rules: []gatewayv1.HTTPRouteRule{{
				// Spelled out, because the CRD DEFAULTS it. An earlier comment
				// here claimed an absent `matches` "comes back unchanged"; it
				// does not — `httproute_types.go` carries
				// `+kubebuilder:default={{path:{type:"PathPrefix",value:"/"}}}`,
				// so the stored object always has this rule and a desired one
				// that left it nil could only be compared by not looking. It is
				// rendered and compared instead, so a narrowed match written by
				// somebody else is drift this operator corrects rather than
				// drift it cannot see.
				Matches: []gatewayv1.HTTPRouteMatch{{
					Path: &gatewayv1.HTTPPathMatch{
						Type:  ptr(gatewayv1.PathMatchPathPrefix),
						Value: ptr("/"),
					},
				}},
				BackendRefs: []gatewayv1.HTTPBackendRef{{
					BackendRef: gatewayv1.BackendRef{
						// Group and Kind are spelled out rather than defaulted. The
						// API server defaults them to ""/Service, and a desired
						// object that left them nil would differ from the stored one
						// on every read — an update loop that converges on nothing.
						BackendObjectReference: gatewayv1.BackendObjectReference{
							Group: &svcGroup,
							Kind:  &svcKind,
							Name:  gatewayv1.ObjectName(WorkloadName(agent.Name, rev)),
							Port:  &backendPort,
						},
						// 100, not 1. The weight only means anything against a second
						// backendRef, and there is never one here — but writing the
						// figure design 02's rollout shifts BETWEEN keeps the emitted
						// object readable next to the weighted routes the e2e still
						// authors by hand.
						Weight: &weight,
					},
				}},
			}},
		},
	}
	if pub.prepared {
		// PREPARED: parentRefs and no backendRefs, so a policy can attach to
		// it before it carries anything (design 03 §3.3). Such a route is not
		// dark: it answers 500 on its hostname (spike §2.6), which replaces
		// nothing on a new Agent. routePublicationFor gives it no mode, so it
		// carries no marker: nothing is published.
		route.Spec.Rules[0].BackendRefs = nil
	}
	return route
}

// routePublication is how one write publishes the serving route. The zero
// value is published, with no marker. routePublicationFor chooses it from
// status.auth.
type routePublication struct {
	// prepared leaves the route with no backendRefs.
	prepared bool
	// mode is the -auth mode the route is published under. Only `none` adds
	// the marker.
	mode compiler.AuthMode
}

// LabelAuth is the marker's key (design 03 §3.3.1).
const LabelAuth = compiler.LabelAuth

// ensureServingRoute creates or converges the route, and returns it as stored
// and whether this call created it. Create/update rather than SSA, matching
// ensureService: one field manager writes these objects and nothing else has
// an opinion about them.
//
// A prepared route names no backend, so it reads no Service: `rev` is only
// its revision label then.
func (r *AgentReconciler) ensureServingRoute(
	ctx context.Context, agent *assaydv1alpha1.Agent, runNS, rev, digest, name string, pub routePublication,
) (*gatewayv1.HTTPRoute, bool, error) {
	var svcPort int32
	if !pub.prepared {
		p, err := r.servingBackendPort(ctx, agent, runNS, rev)
		if err != nil {
			return nil, false, err
		}
		svcPort = p
	}
	desired := r.servingRouteFor(agent, runNS, rev, name, svcPort, pub)
	// Corroboration only, exactly as on the workload and the Service: which
	// projection the revision this route points at was rendered from. Nothing
	// branches on it. The collision rule is on the Agent's IDENTITY instead —
	// see routeCollision — because the route's name carries no revision to
	// collide on.
	desired.Annotations = map[string]string{RevisionDigestAnnotation: digest}

	var existing gatewayv1.HTTPRoute
	err := r.Get(ctx, client.ObjectKeyFromObject(desired), &existing)
	switch {
	case apierrors.IsNotFound(err):
		if !pub.prepared && pub.mode == compiler.AuthModeAPIKey {
			// An API-key route is never CREATED published. It is published only
			// as an update of a route that exists, which a Create prepared and
			// probed. A route that vanished between a caller's check and this
			// read is the caller's to re-prepare (design 03 §3.3.3).
			return nil, false, errRouteGone
		}
		if cerr := r.Create(ctx, desired); cerr != nil {
			if apierrors.IsAlreadyExists(cerr) {
				// Return rather than recurse, for the reason ensureWorkload
				// records at length. Wrapped in errRouteRaceLost so the caller
				// knows this is a lost race and not a refusal.
				return nil, false, fmt.Errorf("route %s: %w: requeueing to converge it: %v",
					desired.Name, errRouteRaceLost, cerr)
			}
			return nil, false, fmt.Errorf("create route %s in %s: %w", desired.Name, runNS, cerr)
		}
		return desired, true, nil
	case err != nil:
		return nil, false, fmt.Errorf("get route %s in %s: %w", desired.Name, runNS, err)
	}
	if cerr := routeCollision(agent, &existing); cerr != nil {
		return nil, false, cerr
	}

	updated := existing.DeepCopy()
	updated.Labels = desired.Labels
	if updated.Annotations == nil {
		updated.Annotations = map[string]string{}
	}
	updated.Annotations[RevisionDigestAnnotation] = digest
	updated.Spec.ParentRefs = desired.Spec.ParentRefs
	updated.Spec.Hostnames = desired.Spec.Hostnames
	updated.Spec.Rules = desired.Spec.Rules
	if equalRoute(&existing, updated) {
		return &existing, false, nil
	}
	if err := r.Update(ctx, updated); err != nil {
		return nil, false, fmt.Errorf("converge route %s in %s: %w", desired.Name, runNS, err)
	}
	return updated, false, nil
}

// currentServingRoute reads the serving route, or nil when there is none. A
// route of that name that another Agent owns is a collision, not this Agent's
// route (design 03 §3.2).
func (r *AgentReconciler) currentServingRoute(
	ctx context.Context, agent *assaydv1alpha1.Agent, runNS, name string,
) (*gatewayv1.HTTPRoute, error) {
	var rt gatewayv1.HTTPRoute
	switch err := r.Get(ctx, client.ObjectKey{Namespace: runNS, Name: name}, &rt); {
	case apierrors.IsNotFound(err):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("get route %s in %s: %w", name, runNS, err)
	}
	if err := routeCollision(agent, &rt); err != nil {
		return nil, err
	}
	return &rt, nil
}

// routePublished reports whether a route carries traffic: any backendRef.
func routePublished(rt *gatewayv1.HTTPRoute) bool {
	for _, rule := range rt.Spec.Rules {
		if len(rule.BackendRefs) > 0 {
			return true
		}
	}
	return false
}

// equalRoute compares every field the renderer writes, and nothing else.
//
// A whole-spec DeepEqual is wrong here and would loop forever: the API server
// defaults fields on an HTTPRoute, so a desired object that left one unset
// would differ from the stored one on every single read and produce an Update
// on every reconcile of every Agent. Owning the fields and comparing exactly is
// rule 6 — and "owning" has to mean every field `ensureServingRoute` assigns,
// not only the interesting ones.
//
// `Filters` is the field that made that distinction load-bearing. The renderer
// emits a rule with none, `ensureServingRoute` assigns `Spec.Rules` wholesale,
// and an earlier version of this function did not compare them — so a request
// mirror or a URL rewrite planted on a converged route by anyone with
// `httproutes` update in a run namespace would have been invisible here
// forever. `assayd-gateway-routes` refuses an author who names the assayd
// Gateway, but it constrains CREATE and UPDATE on the object being written and
// is the chart's, not this reconciler's: a control the operator relies on and
// does not itself perform is exactly what rule 5 says not to leave implied.
func equalRoute(a, b *gatewayv1.HTTPRoute) bool {
	if a.Annotations[RevisionDigestAnnotation] != b.Annotations[RevisionDigestAnnotation] {
		return false
	}
	for k, v := range b.Labels {
		if a.Labels[k] != v {
			return false
		}
	}
	// The marker is compared both ways. The loop above checks only that the
	// desired labels are PRESENT, so a planted `assayd.dev/auth: "none"` on a
	// route that should carry none survived every reconcile in which nothing
	// else drifted (design 03 A69): a route saying "deliberately
	// unauthenticated" while it is not.
	if _, want := b.Labels[LabelAuth]; !want {
		if _, has := a.Labels[LabelAuth]; has {
			return false
		}
	}
	if len(a.Spec.Hostnames) != len(b.Spec.Hostnames) {
		return false
	}
	for i := range a.Spec.Hostnames {
		if a.Spec.Hostnames[i] != b.Spec.Hostnames[i] {
			return false
		}
	}
	if len(a.Spec.ParentRefs) != len(b.Spec.ParentRefs) {
		return false
	}
	for i := range a.Spec.ParentRefs {
		if !equalParentRef(a.Spec.ParentRefs[i], b.Spec.ParentRefs[i]) {
			return false
		}
	}
	if len(a.Spec.Rules) != len(b.Spec.Rules) {
		return false
	}
	for i := range a.Spec.Rules {
		ar, br := a.Spec.Rules[i], b.Spec.Rules[i]
		// A filter this operator did not write is drift, whatever it does: the
		// desired rule carries none, so any count but zero forces a rewrite.
		// Timeouts and the rule name are owned for the same reason — the
		// renderer leaves them empty and the writer assigns the whole rule.
		if len(ar.Filters) != len(br.Filters) ||
			!eqPtr(ar.Timeouts, br.Timeouts) || !eqPtr(ar.Name, br.Name) {
			return false
		}
		if !equalMatches(ar.Matches, br.Matches) {
			return false
		}
		x, y := ar.BackendRefs, br.BackendRefs
		if len(x) != len(y) {
			return false
		}
		for j := range x {
			// Filters and Namespace are owned for the reason Filters is owned
			// one level up, and a first version compared neither. A
			// RequestMirror on the backendRef copies every request elsewhere
			// exactly as one on the rule does; a namespace on the ref needs a
			// ReferenceGrant nobody wrote, so it resolves to nothing and the
			// agent is off the air while reporting Ready. Both survived every
			// reconcile until they were compared.
			if len(x[j].Filters) != len(y[j].Filters) ||
				!eqPtr(x[j].Namespace, y[j].Namespace) ||
				x[j].Name != y[j].Name ||
				!eqPtr(x[j].Port, y[j].Port) ||
				!eqPtr(x[j].Weight, y[j].Weight) ||
				!eqPtr(x[j].Kind, y[j].Kind) ||
				!eqPtr(x[j].Group, y[j].Group) {
				return false
			}
		}
	}
	return true
}

// equalMatches compares the path predicate, which is the whole of what the
// renderer sets. A match narrowed by somebody else — a path prefix that admits
// only `/healthz`, say — silently drops every other request on this hostname
// with a 404 and nothing else in the system would report it.
func equalMatches(a, b []gatewayv1.HTTPRouteMatch) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if len(a[i].Headers) != len(b[i].Headers) ||
			len(a[i].QueryParams) != len(b[i].QueryParams) ||
			!eqPtr(a[i].Method, b[i].Method) {
			return false
		}
		x, y := a[i].Path, b[i].Path
		if x == nil || y == nil {
			if x != y {
				return false
			}
			continue
		}
		if !eqPtr(x.Type, y.Type) || !eqPtr(x.Value, y.Value) {
			return false
		}
	}
	return true
}

// equalParentRef compares every field of a ParentReference, Port included. A
// first version compared five of the six: a `port` planted on the parentRef
// must match the listener as well as `sectionName`, so the route attaches to no
// listener, the agent is off the air, and it keeps reporting Ready because
// route status is not read. The third review found it.
func equalParentRef(a, b gatewayv1.ParentReference) bool {
	return a.Name == b.Name && eqPtr(a.Namespace, b.Namespace) &&
		eqPtr(a.SectionName, b.SectionName) && eqPtr(a.Kind, b.Kind) && eqPtr(a.Group, b.Group) &&
		eqPtr(a.Port, b.Port)
}

func eqPtr[T comparable](a, b *T) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// transientRouteWrite reports a route write that lost a race rather than one
// that was refused. It is the caller's guard against flipping Ready on cache
// lag; the error is still returned, so the work queue retries with backoff.
func transientRouteWrite(err error) bool {
	return apierrors.IsAlreadyExists(err) || apierrors.IsConflict(err) ||
		errors.Is(err, errRouteRaceLost) || errors.Is(err, errStaleAgent)
}

// errRouteGone is a published API-key route found missing at the write:
// ensureServingRoute will not create one, and the caller re-prepares it.
var errRouteGone = errors.New("the serving route is gone, and an API-key route is never created published")

// errRouteRaceLost marks the read-after-write race ensureServingRoute returns
// rather than recursing into, for the reason ensureWorkload records at length.
var errRouteRaceLost = errors.New("a route appeared between the read and the create")

// servingBackendPort is the port the SERVING revision's Service publishes,
// read off that Service by port name. See servingRouteFor for why it may not be
// read off the Agent's spec.
//
// A Service that is missing, or that publishes no `a2a` port, is an error and
// not a guess: a route whose port is invented sends traffic to nothing and
// reports nothing, while an error here reaches RouteApplyFailed and Degraded.
// It is not transient either. ensureService converges only the DESIRED
// revision's Service, so a serving revision's Service that somebody deleted
// is not coming back on its own.
func (r *AgentReconciler) servingBackendPort(
	ctx context.Context, agent *assaydv1alpha1.Agent, runNS, rev string,
) (int32, error) {
	var svc corev1.Service
	key := client.ObjectKey{Namespace: runNS, Name: WorkloadName(agent.Name, rev)}
	if err := r.Get(ctx, key, &svc); err != nil {
		return 0, fmt.Errorf("read the serving revision's Service %s, whose port the route "+
			"must use: %w", key, err)
	}
	for _, p := range svc.Spec.Ports {
		if p.Name == ServicePortName {
			return p.Port, nil
		}
	}
	return 0, fmt.Errorf("the serving revision's Service %s publishes no port named %q, so "+
		"there is no port a route could name", key, ServicePortName)
}

// routeCollisionError is design 03 §3.2's collision check for the one resource
// this operator emits: "a suffix collision is a compile error naming both
// inputs, never a silent reuse". It is not transient, so the caller reports it
// as RouteApplyFailed and Degraded rather than retrying quietly.
type routeCollisionError struct {
	name, ns                         string
	ownerNamespace, ownerAgent       string
	claimantNamespace, claimantAgent string
}

func (e *routeCollisionError) Error() string {
	return fmt.Sprintf("route %s/%s is already the serving route of Agent %s/%s, and Agent %s/%s "+
		"maps to the same name. Two Agents' emitted names collided (design 03 §3.2); the route "+
		"is left as it is rather than taken over", e.ns, e.name,
		e.ownerNamespace, e.ownerAgent, e.claimantNamespace, e.claimantAgent)
}

// routeCollision refuses to converge a route that another Agent owns.
//
// The collision §3.2 guards against is between AGENTS, not revisions: the name
// carries no revision, so the question is whether two different Agents map to
// one name. compiler.EmittedName makes that a 64-bit hash collision, and this is what
// turns one into a refusal naming both inputs rather than one Agent quietly
// rewriting another's route. Before it, update authority was looser than
// delete authority — the sweep required the UID label to match before
// deleting, while the converge path rewrote whatever it found by name.
//
// What counts as ANOTHER Agent is the name and namespace on the labels, not the
// UID alone, and that is deliberate. A route whose UID differs but whose name
// and namespace are this Agent's is a PREDECESSOR's: an Agent deleted while the
// gateway was declared off sweeps nothing (reconcileGateway), and one
// recreated under the same name must adopt that route rather than be wedged by
// it forever. A route with no `agent-uid` label is adopted for the same reason,
// whatever its other labels say. None of this is evidence against an attacker — a label needs only
// `update` to forge — and it does not claim to be: forging the labels buys a
// refusal of an Agent whose run namespace the forger can already write, and
// forging them to match buys an adoption that rewrites the forger's spec.
func routeCollision(agent *assaydv1alpha1.Agent, existing *gatewayv1.HTTPRoute) error {
	uid, ok := existing.Labels[LabelAgentUID]
	if !ok || uid == string(agent.UID) {
		return nil
	}
	ownerAgent, ownerNS := existing.Labels[LabelAgent], existing.Labels[LabelAgentNamespace]
	if ownerAgent == agent.Name && ownerNS == agent.Namespace {
		return nil
	}
	return &routeCollisionError{
		name: existing.Name, ns: existing.Namespace,
		ownerNamespace: ownerNS, ownerAgent: ownerAgent,
		claimantNamespace: agent.Namespace, claimantAgent: agent.Name,
	}
}

// ownedRoutes returns the routes this operator emitted for THIS Agent, by the
// same authority as ownedWorkloads and ownedServices: the immutable NAME, which
// a victim object cannot be renamed into, corroborated by the Agent's UID.
// Design 03 §3.2 states the rule in as many words — "the label is not the
// deletion authority … the sweep additionally requires the resource's NAME to
// match the deterministic shape". Nothing is deleted for wearing a label.
func (r *AgentReconciler) ownedRoutes(
	ctx context.Context, agent *assaydv1alpha1.Agent, runNS string,
) ([]gatewayv1.HTTPRoute, error) {
	return r.listOwnedRoutes(ctx, r.Client, agent, runNS)
}

// listOwnedRoutes is ownedRoutes through a reader of the caller's choosing.
// Teardown reads through the uncached one (revokeGateway).
func (r *AgentReconciler) listOwnedRoutes(
	ctx context.Context, from client.Reader, agent *assaydv1alpha1.Agent, runNS string,
) ([]gatewayv1.HTTPRoute, error) {
	name, err := compiler.ServingRouteName(agent.Name)
	if err != nil {
		return nil, err
	}
	var list gatewayv1.HTTPRouteList
	if err := from.List(ctx, &list,
		client.InNamespace(runNS),
		client.MatchingLabels{LabelAgent: agent.Name},
	); err != nil {
		// No NoMatch arm. An earlier version returned "no routes" on a
		// no-matching-kind error, for a finalizer that would otherwise never
		// release if Gateway API were uninstalled under a running operator. It
		// could not fire: this runs only when the gateway is enabled, which
		// means SetupWithManager already proved the CRD and started a
		// cache-backed informer, and a List against a started informer answers
		// from the cache rather than from the RESTMapper. Insurance against a
		// failure it does not catch reads as load-bearing and is not; what
		// uninstalling Gateway API under a running operator does is unmeasured
		// (design 07 A6.11).
		return nil, fmt.Errorf("list routes for %s/%s: %w", agent.Namespace, agent.Name, err)
	}
	owned := make([]gatewayv1.HTTPRoute, 0, len(list.Items))
	for i := range list.Items {
		rt := &list.Items[i]
		if rt.Labels[LabelAgentUID] != string(agent.UID) {
			continue
		}
		// Every name this operator can emit for this Agent. One concern today;
		// a second one adds a line here rather than a second sweep.
		if rt.Name != name {
			continue
		}
		owned = append(owned, *rt)
	}
	return owned, nil
}

// collectRoutes deletes every route this Agent owns except `keep`. An empty
// `keep` deletes all of them, which is what an Agent with no serving revision
// and what the finalizer both want: a route outliving the Service it names
// publishes a hostname that resolves to nothing, and Kubernetes will not
// collect it because it carries no ownerReference (design 03 §3.2).
func (r *AgentReconciler) collectRoutes(
	ctx context.Context, agent *assaydv1alpha1.Agent, runNS, keep string,
) error {
	owned, err := r.ownedRoutes(ctx, agent, runNS)
	if err != nil {
		return err
	}
	for i := range owned {
		if owned[i].Name == keep {
			continue
		}
		if err := r.Delete(ctx, &owned[i]); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("gc route %s in %s: %w", owned[i].Name, runNS, err)
		}
	}
	return nil
}

// authTransaction is status.auth.transaction, or nil.
func authTransaction(status *assaydv1alpha1.AgentStatus) *assaydv1alpha1.AuthTransaction {
	if status.Auth == nil {
		return nil
	}
	return status.Auth.Transaction
}

// ReasonGatewayDisabled is GovernanceSkipped's reason on design 03 §3.1's
// declared-ungoverned tier — the row P1 ships and `local` uses. The reasons
// for the enabled tier are in authtxn.go.
const ReasonGatewayDisabled = "GatewayDisabled"

// assessGovernance puts design 03 §3.1's tier condition on every Agent.
//
// It runs with the other assessors, BEFORE anything can return early, so that
// every status path carries it. With the gateway enabled the compiler runs,
// and the value comes from status.auth, which is all the paths that return
// early can see: the value a served -auth earned, and "vacuously False" for an
// Agent with no route the compiler published. reconcileGateway then refines it
// from the route itself. The one value only that refinement can know, a
// refused `Adopt`'s, is kept rather than overwritten here.
//
// Ready is never withheld by this. §3.1: only the incident row withholds Ready,
// so a stock `helm install` never produces a fleet that is permanently
// un-`Ready`.
func (r *AgentReconciler) assessGovernance(c *conditionSet, status *assaydv1alpha1.AgentStatus) {
	if !r.Gateway.Enabled {
		c.set(assaydv1alpha1.CondGovernanceSkipped, metav1.ConditionTrue, ReasonGatewayDisabled,
			"gateway.enabled is false, so this is the deliberately ungoverned tier (design 03 §3.1): "+
				"no route is emitted, no budget or rate limit is enforced, there is no gateway "+
				"authentication and no tool filtering, and no NetworkPolicy is materialized in any "+
				"namespace. Readiness is not withheld — this is a documented tier, not an incident")
		return
	}
	if tx := authTransaction(status); tx != nil && tx.Kind == TxLock {
		// Both conditions, so that a pass which returns before the -auth step
		// does not clear PolicyApplyIncomplete in the middle of the Lock: it is
		// raised until an attributed 401 (§3.3.3). The -auth step re-derives
		// both when it runs.
		const m = "a served Agent's -auth policy was missing and is being re-created; the route " +
			"serves with no key until an anonymous request through it gets an attributed 401 " +
			"(design 03 §3.3.3)"
		c.set(assaydv1alpha1.CondGovernanceSkipped, metav1.ConditionTrue, ReasonAuthPolicyMissing, m)
		c.set(assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, ReasonAuthPolicyMissing, m)
		return
	} else if tx != nil && tx.Kind == TxCreate && status.Auth.Mode != "" {
		st, reason, msg := vacuousGovernance("this Agent's route is being re-created as a prepared " +
			"route, and is not published")
		c.set(assaydv1alpha1.CondGovernanceSkipped, st, reason, msg)
		return
	}
	if status.Auth != nil && status.Auth.Mode != "" {
		st, reason, msg := governanceFor(status.Auth)
		c.set(assaydv1alpha1.CondGovernanceSkipped, st, reason, msg)
		return
	}
	if status.Auth != nil && status.Auth.Transaction != nil && status.Auth.Transaction.Kind == TxAdopt {
		// A refused Adopt is recorded; its message is refuseAdopt's, which
		// names the route, and not asserting keeps it (conditions.go: sticky).
		return
	}
	st, reason, msg := vacuousGovernance("this operator has published no route for this Agent " +
		"through its compiler")
	c.set(assaydv1alpha1.CondGovernanceSkipped, st, reason, msg)
}
