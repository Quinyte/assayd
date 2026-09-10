// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
)

// ADR-0030 step 3, SECOND half: the mapping the e2e hand-authored is now
// encoded. This file emits ONE resource — the revision's serving HTTPRoute —
// and nothing else.
//
// **This is not design 03's compiler and must not be read as one.** Design 03
// is not approved and its own Status line says do not implement it. What is
// encoded here is exactly the resource design 07 A6 already authored by hand
// and executed against a real Gateway, plus design 03 §3.2's rules about where
// it lives, what names it, and what may delete it — because those were measured
// too. Everything else design 03 describes is absent: no AgentgatewayPolicy, no
// AgentgatewayBackend, no budget, no rate limit, no authentication, no tool
// allowlist. §3.3.1 would in fact REFUSE to emit this route, because its
// mandatory `-auth` concern has no producer until design 06 lands; that refusal
// is not implemented, so the route this operator emits is an UNAUTHENTICATED
// path to the agent, exactly as the hand-authored one was.
const (
	// RouteConcernServing is the `<concern>` of design 03 §3.2's naming grammar
	// for the A2A serving route.
	RouteConcernServing = "serving"

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

// EmittedName is design 03 §3.2's naming rule for every resource this operator
// emits at the gateway: `<name>-<concern>[-<rev>]`, and past 63 characters
// `<name>` is truncated and a 16-hex (64-bit) hash of the UNTRUNCATED whole
// name is appended.
//
// The suffix is 16 hex and not 8 for the reason design 03 §3.2 records: a
// chosen collision against 32 bits took 1.2 seconds when it was measured, and
// two crafted long names colliding in the suffix would map one Agent's
// resources onto another's. The hash covers the whole assembled name rather
// than only `<name>`, so two Agents cannot collide by differing only in a
// concern or a revision that the truncation kept.
//
// A name whose concern and revision alone leave no room to truncate is an
// ERROR, never a silent mangling — design 03 §3.2: "a suffix collision is a
// compile error naming both inputs, never a silent reuse", and a name truncated
// to nothing is the same failure one step earlier.
func EmittedName(name, concern, rev string) (string, error) {
	tail := "-" + concern
	if rev != "" {
		tail += "-" + rev
	}
	full := name + tail
	if len(full) <= 63 {
		return full, nil
	}
	const hashHex = 16
	keep := 63 - len(tail) - 1 - hashHex // 1 for the hash's separator
	if keep < 1 {
		return "", fmt.Errorf("cannot name an emitted resource for %q: the concern and revision "+
			"alone are %d characters, which leaves nothing of the name inside the 63-character "+
			"limit", name, len(tail))
	}
	sum := sha256.Sum256([]byte(full))
	return strings.TrimRight(name[:keep], "-") + tail + "-" + hex.EncodeToString(sum[:])[:hashHex], nil
}

// ServingRouteName is the object name of one Agent's serving route.
//
// It carries NO revision, and that is the design's shape rather than a
// simplification. Design 03 §3.2 lists "revision weights" as a property of one
// HTTPRoute and design 20's fallback row shifts `spec.rules[].backendRefs[]
// .weight` on "the serving HTTPRoute", singular — so a revision is a backendRef
// within one route, not a route of its own. A route per revision would put two
// routes on one hostname for the length of every rollout, and a listener merges
// them: the revision being rolled out would take a share of production traffic
// with no weight ever having been shifted, which is precisely the ungated
// window design 03 §3.3's ordering exists to close. Deleting the old route
// first trades that for a window with no route at all. One object whose
// backendRef is updated in place has neither.
func ServingRouteName(agentName string) (string, error) {
	return EmittedName(agentName, RouteConcernServing, "")
}

// servingRouteFor renders the route for one Agent, pointing at one revision.
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
func (r *AgentReconciler) servingRouteFor(agent *assaydv1alpha1.Agent, runNS, rev, name string) *gatewayv1.HTTPRoute {
	group := gatewayv1.Group(gatewayv1.GroupName)
	gwKind := gatewayv1.Kind("Gateway")
	svcGroup := gatewayv1.Group("")
	svcKind := gatewayv1.Kind("Service")
	gwNS := gatewayv1.Namespace(r.Gateway.Namespace)
	section := gatewayv1.SectionName(GatewayListenerName)
	backendPort := gatewayv1.PortNumber(port(agent.Spec.Runtime))
	weight := int32(100)

	return &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			// The RUN namespace, with the Service it names. A backendRef across
			// namespaces needs a ReferenceGrant and without one the route reports
			// `Accepted=True` beside `ResolvedRefs=False, RefNotPermitted` —
			// design 03 §3.2 records that leaving the route behind wedges every
			// Agent at every install. The parentRef to a Gateway in another
			// namespace needs no grant; only backendRefs and Secrets do.
			Namespace: runNS,
			Labels: map[string]string{
				LabelAgent:          agent.Name,
				LabelRevision:       rev,
				LabelAgentUID:       string(agent.UID),
				LabelAgentNamespace: agent.Namespace,
			},
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
}

// reconcileServingRoute converges the one route this operator emits, and sweeps
// any it owns that should no longer exist.
//
// `rev` is the revision that is SERVING — status.activeRevision — not the
// desired one. A candidate coming up must not take the hostname from the
// revision still answering on it; that is the whole reason design 02 gives a
// Service to every revision.
func (r *AgentReconciler) reconcileServingRoute(
	ctx context.Context, agent *assaydv1alpha1.Agent, runNS, rev, digest string,
) error {
	if !r.Gateway.Enabled {
		// Nothing is emitted, and nothing is SWEPT either. Design 03 §3.1 says a
		// `true → false` transition first runs §5's reverse-order teardown; that
		// teardown is NOT implemented, so a route left behind by an install that
		// was once enabled stays until the operator is re-enabled or a human
		// removes it. Said rather than implied: sweeping here would mean listing
		// a kind whose CRDs a disabled install has no reason to carry, and
		// pretending to a teardown that does not exist is worse than naming it.
		return nil
	}
	name, err := ServingRouteName(agent.Name)
	if err != nil {
		return err
	}
	keep := ""
	if rev != "" {
		if err := r.ensureServingRoute(ctx, agent, runNS, rev, digest, name); err != nil {
			return err
		}
		keep = name
	}
	return r.collectRoutes(ctx, agent, runNS, keep)
}

// ensureServingRoute creates or converges the route. Create/update rather than
// SSA, matching ensureService: one field manager writes these objects and
// nothing else has an opinion about them.
func (r *AgentReconciler) ensureServingRoute(
	ctx context.Context, agent *assaydv1alpha1.Agent, runNS, rev, digest, name string,
) error {
	desired := r.servingRouteFor(agent, runNS, rev, name)
	// Corroboration only, exactly as on the workload and the Service: which
	// projection the revision this route points at was rendered from. Nothing
	// branches on it — the route carries no collision rule, because its name
	// carries no revision to collide.
	desired.Annotations = map[string]string{RevisionDigestAnnotation: digest}

	var existing gatewayv1.HTTPRoute
	err := r.Get(ctx, client.ObjectKeyFromObject(desired), &existing)
	switch {
	case apierrors.IsNotFound(err):
		if cerr := r.Create(ctx, desired); cerr != nil {
			if apierrors.IsAlreadyExists(cerr) {
				// Return rather than recurse, for the reason ensureWorkload
				// records at length. Wrapped in errRouteRaceLost so the caller
				// knows this is a lost race and not a refusal.
				return fmt.Errorf("route %s: %w: requeueing to converge it: %v",
					desired.Name, errRouteRaceLost, cerr)
			}
			return fmt.Errorf("create route %s in %s: %w", desired.Name, runNS, cerr)
		}
		return nil
	case err != nil:
		return fmt.Errorf("get route %s in %s: %w", desired.Name, runNS, err)
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
		return nil
	}
	if err := r.Update(ctx, updated); err != nil {
		return fmt.Errorf("converge route %s in %s: %w", desired.Name, runNS, err)
	}
	return nil
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
			if x[j].Name != y[j].Name ||
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

func equalParentRef(a, b gatewayv1.ParentReference) bool {
	return a.Name == b.Name && eqPtr(a.Namespace, b.Namespace) &&
		eqPtr(a.SectionName, b.SectionName) && eqPtr(a.Kind, b.Kind) && eqPtr(a.Group, b.Group)
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
		errors.Is(err, errRouteRaceLost)
}

// errRouteRaceLost marks the read-after-write race ensureServingRoute returns
// rather than recursing into, for the reason ensureWorkload records at length.
var errRouteRaceLost = errors.New("a route appeared between the read and the create")

// ownedRoutes returns the routes this operator emitted for THIS Agent, by the
// same authority as ownedWorkloads and ownedServices: the immutable NAME, which
// a victim object cannot be renamed into, corroborated by the Agent's UID.
// Design 03 §3.2 states the rule in as many words — "the label is not the
// deletion authority … the sweep additionally requires the resource's NAME to
// match the deterministic shape". Nothing is deleted for wearing a label.
func (r *AgentReconciler) ownedRoutes(
	ctx context.Context, agent *assaydv1alpha1.Agent, runNS string,
) ([]gatewayv1.HTTPRoute, error) {
	name, err := ServingRouteName(agent.Name)
	if err != nil {
		return nil, err
	}
	var list gatewayv1.HTTPRouteList
	if err := r.List(ctx, &list,
		client.InNamespace(runNS),
		client.MatchingLabels{LabelAgent: agent.Name},
	); err != nil {
		if meta.IsNoMatchError(err) {
			// The Gateway API CRDs went away under a running, gateway-enabled
			// operator. There is no kind, so there are no routes — which is the
			// honest answer and, more importantly, not an error: the FINALIZER
			// sweeps through here, and a finalizer that returns an error never
			// releases, so every Agent in the cluster would become undeletable
			// the moment somebody uninstalled Gateway API.
			return nil, nil
		}
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

// Reasons on GovernanceSkipped (design 03 §3.1).
const (
	// ReasonGatewayDisabled is §3.1's own word for the declared-ungoverned tier
	// — the row P1 ships and `local` uses.
	ReasonGatewayDisabled = "GatewayDisabled"
	// ReasonPolicyCompilerAbsent is the row §3.1 does not have.
	//
	// Its table pairs `gateway.enabled: false` with GovernanceSkipped=True and
	// says nothing about the enabled row, which reads as though turning the
	// value on makes governance real. It does not: this operator emits a route
	// and no policy of any kind, so an enabled install is still ungoverned and
	// the condition still says so — with a different reason, so the two states
	// are distinguishable by a consumer and by a test. Reporting
	// GovernanceSkipped=False here would be the loud-and-wrong of rule 8: a
	// condition naming a plausible cause that was never checked, on the exact
	// claim the platform is built on.
	ReasonPolicyCompilerAbsent = "PolicyCompilerAbsent"
)

// assessGovernance puts design 03 §3.1's tier condition on every Agent.
//
// It runs with the other assessors, BEFORE anything can return early, for the
// reason the reconciler records there: GovernanceSkipped is owned, so a pass
// that exits without asserting it CLEARS it — and an Agent that is more
// degraded, not less, would lose the record of the tier it runs in.
//
// Ready is never withheld by this. §3.1: only the incident row withholds Ready,
// so a stock `helm install` never produces a fleet that is permanently
// un-`Ready`.
func (r *AgentReconciler) assessGovernance(c *conditionSet) {
	if !r.Gateway.Enabled {
		c.set(assaydv1alpha1.CondGovernanceSkipped, metav1.ConditionTrue, ReasonGatewayDisabled,
			"gateway.enabled is false, so this is the deliberately ungoverned tier (design 03 §3.1): "+
				"no route is emitted, no budget or rate limit is enforced, there is no gateway "+
				"authentication and no tool filtering, and no NetworkPolicy is materialized in any "+
				"namespace. Readiness is not withheld — this is a documented tier, not an incident")
		return
	}
	// A property of the INSTALL, not of this Agent, and the difference is not
	// pedantry. This runs with the other assessors, before the run namespace is
	// resolved and before it is known whether this Agent has a serving revision
	// at all — and it runs for EXTERNAL agents, which never get a route because
	// they never get a workload. An earlier version said "the serving HTTPRoute
	// is emitted … and attached to Gateway X/Y", which was false for every
	// external agent and for every agent in the window before its first
	// promotion: a condition naming a plausible state nobody checked, which is
	// rule 8 on the one claim this platform is built on.
	c.set(assaydv1alpha1.CondGovernanceSkipped, metav1.ConditionTrue, ReasonPolicyCompilerAbsent,
		fmt.Sprintf("gateway.enabled is true, so this operator emits ONE resource for an Agent that "+
			"has a serving revision — that revision's HTTPRoute, into the run namespace, attached "+
			"to Gateway %s/%s. That is ALL it emits: no AgentgatewayPolicy and no "+
			"AgentgatewayBackend, so no budget, rate limit, authentication or tool allowlist is "+
			"enforced anywhere, and traffic arriving through the gateway is unauthenticated. No "+
			"NetworkPolicy is materialized either, so an agent Pod stays directly reachable. The "+
			"policy compiler of design 03 does not exist; setting gateway.enabled publishes a "+
			"path, not a guarantee. Check the route itself for whether one exists for this Agent",
			r.Gateway.Namespace, r.Gateway.Name))
}
