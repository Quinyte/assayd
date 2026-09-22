// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
)

// ServicePortName names the one port a revision's Service publishes. It is
// exported because the emitted route reads the port OFF this Service rather
// than off the Agent's spec (see servingBackendPort), and a port selected by
// index instead of by name would silently follow whatever a future second port
// happened to be ordered first.
const ServicePortName = "a2a"

// A Service is per REVISION, not per Agent, and that is the whole point.
//
// Design 02 §3.2 said "the Service", singular, and nothing created one at all —
// so a Pod could report Ready with no stable address and no route could reach
// it. Singular is also wrong, and the producing design says why: design 03's
// PolicyIntent carries `revision: {hash, weight, candidate}` and its Publishing
// stage shifts `backendRefs` weights BETWEEN revisions. R1 and R2 coexist by
// construction during a rollout. One Service whose selector matched both would
// make the weights meaningless — every backendRef would resolve to the same
// endpoint set — and would put production traffic on an ungated candidate the
// moment its Pods went Ready, which is precisely the gate this platform exists
// to enforce.
//
// So the Service shares the workload's name, `<agent>-<revision>`, and selects
// on {agent, revision} exactly as the Deployment's own pod selector does.
// Design 03 §3.6 places it in the run namespace with the route, because a
// backendRef across namespaces needs a ReferenceGrant in the target namespace
// and the user's namespace is not somewhere this operator may require one.
func (r *AgentReconciler) serviceFor(agent *assaydv1alpha1.Agent, runNS, rev string) *corev1.Service {
	// Identical to deploymentFor's, and deliberately so: this selects that
	// Deployment's Pods and nothing else. A revision's Pods carry the revision
	// label, so a Service for R1 cannot reach R2 even while both run.
	selector := map[string]string{
		LabelAgent:    agent.Name,
		LabelRevision: rev,
	}
	labels := map[string]string{
		LabelAgent:          agent.Name,
		LabelRevision:       rev,
		LabelAgentUID:       string(agent.UID),
		LabelAgentNamespace: agent.Namespace,
	}
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      WorkloadName(agent.Name, rev),
			Namespace: runNS,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Selector: selector,
			Type:     corev1.ServiceTypeClusterIP,
			Ports: []corev1.ServicePort{{
				Name: ServicePortName,
				// The port as of the revision that MINTED this Service, which is
				// this Agent's spec only while `rev` is the desired revision. A
				// Service is never re-rendered for a revision that has left
				// `desired`, so R1's Service keeps R1's port after a port edit
				// mints R2 — which is exactly why the emitted route may not read
				// the port off the spec (design 07 A6.11).
				Port:     port(agent.Spec.Runtime),
				Protocol: corev1.ProtocolTCP,
				// By NAME, not number. The container port is behaviour surface
				// (ADR-0031), so a port edit mints a new revision with its own
				// Deployment and its own Service — but naming the target means the
				// two halves of one revision can never disagree about it even if a
				// future change makes them render separately.
				TargetPort: intstr.FromString(ServicePortName),
			}},
		},
	}
}

// ensureService converges one revision's Service, under the same collision rule
// as its workload: a Service that exists under this name but was rendered from a
// DIFFERENT projection is a 40-bit name collision, and converging it would point
// the safe revision's route at the attacker's Pods. Stop instead.
//
// `status` is read, never written: it is the non-forgeable half of the
// provenance test below, because this controller is its only author and writes
// it through the status subresource.
func (r *AgentReconciler) ensureService(ctx context.Context, agent *assaydv1alpha1.Agent,
	runNS, rev, digest string, status *assaydv1alpha1.AgentStatus) error {
	desired := r.serviceFor(agent, runNS, rev)
	desired.Annotations = map[string]string{RevisionDigestAnnotation: digest}

	var existing corev1.Service
	err := r.Get(ctx, client.ObjectKeyFromObject(desired), &existing)
	switch {
	case apierrors.IsNotFound(err):
		if err := r.Create(ctx, desired); err != nil {
			if apierrors.IsAlreadyExists(err) {
				// Read-after-write against a stale informer cache. Return rather
				// than recurse, for the reason ensureWorkload records at length.
				return fmt.Errorf("service %s appeared between the read and the create; "+
					"requeueing to validate it: %w", desired.Name, err)
			}
			return fmt.Errorf("create service %s: %w", desired.Name, err)
		}
		return nil
	case err != nil:
		return fmt.Errorf("get service %s: %w", desired.Name, err)
	}

	// PROVENANCE decides, and it decides FIRST. The three grounds are
	// ensureWorkload's, unchanged: the `assayd.dev/agent-uid` label is not this
	// Agent's, the digest is stamped and differs, or it is unstamped and
	// `status` vouches for no such revision. `status` is the non-forgeable
	// side, written only by this controller through the status subresource.
	//
	// The Service path had only the middle one, so an unstamped Service at a
	// revision's name was adopted whatever else it said, and every field the
	// renderer does not own rode along.
	vouched := (status.ActiveRevision == rev && status.ActiveRevisionDigest == digest) ||
		(status.CandidateRevision == rev && status.CandidateRevisionDigest == digest)
	existingDigest, stamped := existing.Annotations[RevisionDigestAnnotation]

	// The shape is read here and used twice: as DETAIL on a refusal, where it
	// is what makes "someone else's object" actionable rather than merely
	// true, and below as the one thing convergence cannot repair.
	shape := serviceNotRendered(&existing)
	switch {
	case existing.Labels[LabelAgentUID] != string(agent.UID):
		return &revisionCollisionError{name: desired.Name, ns: runNS, existing: "(another agent's)",
			desired: digest, kind: "service", shape: shape}
	case stamped && existingDigest != digest:
		// Typed, so reconcile reports RevisionHashCollision rather than returning a
		// bare error. A bare one retried forever and wrote no status — the silent
		// degraded path NFR-8 forbids, and the same defect ensureWorkload records.
		return &revisionCollisionError{name: desired.Name, ns: runNS, existing: existingDigest,
			desired: digest, kind: "service", shape: shape}
	case !stamped && !vouched:
		return &revisionCollisionError{name: desired.Name, ns: runNS, existing: "(no stamp)",
			desired: digest, kind: "service", shape: shape}
	}

	// Past that switch the object is OURS, and a wrong shape on our own object
	// is CONVERGED, not refused.
	//
	// The first cut of A77 refused it, and the human reversed that on the fifth
	// review's argument. Provenance is what decides: an object that carries our
	// stamp is ours, rewriting it is exactly what ensureWorkload already does
	// to the Deployment, and "converging would rewrite a stranger's object" —
	// the whole case for refusing — is not true of it. The wedge was the deeper
	// problem: refusing here handed anyone with `services/patch` in a run
	// namespace a permanent per-Agent outage, needing no ExternalName and no
	// cleverness, which is a worse failure than the one this amendment closes.
	//
	// ONE field cannot be converged, and it is refused ON ITS OWN rather than
	// by widening the refusal to the rest: `spec.clusterIP` is immutable, so a
	// headless Service at this name can never be given an address. It is
	// reachable for an object this switch admits — a forged UID label needs
	// only `create`, and an unstamped object at a vouched revision is adopted —
	// so this is a live branch and not a guard against the impossible.
	if shape != nil && shape.what == faultHeadless {
		return shape
	}

	// ClusterIP is assigned by the API server and must survive the update, as
	// must the health-check node port and any field a future admission plugin
	// defaults. Converge only what this operator has an opinion about — which
	// now includes the shape fields, because an opinion it does not assert is
	// an opinion a patch can overwrite.
	updated := existing.DeepCopy()
	updated.Labels = desired.Labels
	if updated.Annotations == nil {
		updated.Annotations = map[string]string{}
	}
	updated.Annotations[RevisionDigestAnnotation] = digest
	updated.Spec.Selector = desired.Spec.Selector
	updated.Spec.Ports = desired.Spec.Ports
	// The shape, asserted rather than merely checked. Setting Type back to
	// ClusterIP on an ExternalName Service makes the API server allocate an
	// address; clearing externalIPs removes the node DNAT; clearing
	// publishNotReadyAddresses restores the readiness gate promotion rests on.
	updated.Spec.Type = desired.Spec.Type
	updated.Spec.ExternalName = ""
	updated.Spec.ExternalIPs = nil
	updated.Spec.PublishNotReadyAddresses = false
	// AND the fields that only exist UNDER a type we are leaving. Kubernetes
	// couples them: `dropTypeDependentFields` clears most on a type change —
	// externalTrafficPolicy, loadBalancerIP, allocateLoadBalancerNodePorts,
	// healthCheckNodePort, loadBalancerClass, the ports' nodePorts — but it
	// does NOT clear loadBalancerSourceRanges, and the API server then refuses
	// the repair with `may only be used when type is 'LoadBalancer'`. Measured:
	// one `services/patch` setting type=LoadBalancer with a source range left
	// the Agent permanently Degraded under ServiceRejected with the exposure
	// intact — the wedge this convergence exists to avoid, restored by
	// asserting a subset of a coupled field set. Design 02 §5 records that this
	// is an enumeration and a future coupled field would land in the same exit,
	// which is why that exit now requeues and names a remedy.
	updated.Spec.LoadBalancerSourceRanges = nil
	if equalService(&existing, updated) {
		return nil
	}
	if err := r.Update(ctx, updated); err != nil {
		return fmt.Errorf("converge service %s: %w", desired.Name, err)
	}
	return nil
}

// serviceShapeError is a revision Service whose shape this operator never
// renders.
//
// Only the headless fault is returned AS an error, because it is the only one
// convergence cannot repair; the rest ride on a provenance refusal as detail,
// through detail(). It is terminal by design, exactly as a revision collision
// is: the remedy is a human deleting the object.
type serviceShapeError struct {
	ns, name     string
	what         shapeFault
	typ          corev1.ServiceType
	externalName string
	externalIPs  []string
}

type shapeFault int

const (
	faultType shapeFault = iota
	faultHeadless
	faultExternalIPs
	faultNotReady
)

// detail is the clause that says what is wrong with the shape and why it
// matters, in a form that reads as a continuation of another sentence.
//
// It is what makes a refusal actionable: "a Service that is not this Agent's"
// is true and tells an administrator nothing about urgency, where "and it is
// of type ExternalName, resolving to elsewhere.example.com" tells them what
// the object was for.
func (e *serviceShapeError) detail() string {
	switch e.what {
	case faultHeadless:
		return "It is headless (spec.clusterIP: None), and spec.clusterIP is immutable, so this " +
			"operator cannot converge it back — every other shape it renders is repaired in " +
			"place. The card fetch, which addresses the Service by its ClusterIP, can never " +
			"succeed against it either."
	case faultExternalIPs:
		// BOUNDED, because the value is the writer's and `spec.externalIPs` has
		// no item cap in Kubernetes validation while metav1.Condition.Message is
		// capped at 32768 RUNES. Rendered whole, a Service carrying a few
		// thousand addresses made every status write for that Agent fail with
		// `Too long`, freezing its whole status at the pre-incident value —
		// Ready=True, route published — while the refusal it was reporting went
		// unwritten. That is A81's incident class through a different door.
		// Three addresses name the object well enough for an operator to find it.
		return fmt.Sprintf("It carries %d spec.externalIPs, beginning %v, which every node DNATs "+
			"to this revision's Pods — outside the gateway, which is the one way in this "+
			"platform claims to have on its governed tier (CVE-2020-8554 is this field).",
			len(e.externalIPs), e.externalIPs[:min(len(e.externalIPs), 3)])
	case faultNotReady:
		return "It sets spec.publishNotReadyAddresses, which serves Pods that have not passed " +
			"the readiness this operator promotes a revision on."
	case faultType:
		if e.typ == corev1.ServiceTypeExternalName {
			return fmt.Sprintf("It is of type ExternalName and resolves to %q, so cluster DNS "+
				"answers this revision's address with a CNAME to that host instead of the agent, "+
				"and a backendRef naming it would name a Service with no endpoints.",
				e.externalName)
		}
		return fmt.Sprintf("It is of type %s, which publishes this revision's Pods outside the "+
			"gateway, the one way in this platform claims to have on its governed tier.", e.typ)
	}
	// Unreachable: every shapeFault serviceNotRendered can return is above. A
	// bare panic here would take the manager down for a formatting bug.
	return "Its shape is not one this operator renders."
}

func (e *serviceShapeError) Error() string {
	// Reached only for faultHeadless, which is the only fault this operator
	// returns as an error rather than converging away.
	return fmt.Sprintf("service %s/%s cannot be converged to the shape this operator renders. %s "+
		"Refusing this one field rather than widening the refusal to shapes that CAN be "+
		"repaired. To recover: delete that Service and let the operator recreate it.",
		e.ns, e.name, e.detail())
}

// serviceNotRendered answers whether an existing revision Service has a shape
// serviceFor never produces.
//
// It is an ENUMERATION of the fields whose value is security-bearing here, not
// a whitelist of the whole spec, and design 02 §5 carries what it does not
// read. A whitelist is the stronger shape and is not what this is.
//
// `spec.type` is defaulted to ClusterIP by the API server, so a stored Service
// always carries one and an empty value is not a case this can see;
// `spec.clusterIP: None` is the headless shape, which is a ClusterIP Service by
// type and still not one this operator writes.
func serviceNotRendered(existing *corev1.Service) *serviceShapeError {
	e := &serviceShapeError{ns: existing.Namespace, name: existing.Name, typ: existing.Spec.Type}
	switch {
	case existing.Spec.Type != corev1.ServiceTypeClusterIP:
		e.what, e.externalName = faultType, existing.Spec.ExternalName
	case existing.Spec.ClusterIP == corev1.ClusterIPNone:
		e.what = faultHeadless
	case len(existing.Spec.ExternalIPs) > 0:
		e.what, e.externalIPs = faultExternalIPs, existing.Spec.ExternalIPs
	case existing.Spec.PublishNotReadyAddresses:
		e.what = faultNotReady
	default:
		return nil
	}
	return e
}

func equalService(a, b *corev1.Service) bool {
	if len(a.Spec.Ports) != len(b.Spec.Ports) || len(a.Spec.Selector) != len(b.Spec.Selector) {
		return false
	}
	for i := range a.Spec.Ports {
		if a.Spec.Ports[i] != b.Spec.Ports[i] {
			return false
		}
	}
	for k, v := range b.Spec.Selector {
		if a.Spec.Selector[k] != v {
			return false
		}
	}
	for k, v := range b.Labels {
		if a.Labels[k] != v {
			return false
		}
	}
	// The shape fields the converge now asserts. Left out, a patched Service
	// compares equal to its own repair and no Update is ever issued — the
	// convergence would be dead code, which a mutation shows and a reader does
	// not.
	if a.Spec.Type != b.Spec.Type || a.Spec.ExternalName != b.Spec.ExternalName ||
		a.Spec.PublishNotReadyAddresses != b.Spec.PublishNotReadyAddresses ||
		!equalStrings(a.Spec.ExternalIPs, b.Spec.ExternalIPs) ||
		!equalStrings(a.Spec.LoadBalancerSourceRanges, b.Spec.LoadBalancerSourceRanges) {
		return false
	}
	return a.Annotations[RevisionDigestAnnotation] == b.Annotations[RevisionDigestAnnotation]
}

// ownedServices returns the Services this Agent actually controls, by the same
// name authority as ownedWorkloads: the name shape `<agent>-<revision>`, which
// is immutable so a victim object cannot be renamed into it, corroborated by
// the Agent's UID. Nothing is deleted for wearing a label.
func (r *AgentReconciler) ownedServices(ctx context.Context, agent *assaydv1alpha1.Agent, runNS string) ([]corev1.Service, error) {
	var list corev1.ServiceList
	if err := r.List(ctx, &list,
		client.InNamespace(runNS),
		client.MatchingLabels{LabelAgent: agent.Name},
	); err != nil {
		return nil, fmt.Errorf("list services for %s/%s: %w", agent.Namespace, agent.Name, err)
	}
	owned := make([]corev1.Service, 0, len(list.Items))
	for _, s := range list.Items {
		if !isRevisionService(agent, &s) {
			continue
		}
		owned = append(owned, s)
	}
	return owned, nil
}

func isRevisionService(agent *assaydv1alpha1.Agent, s *corev1.Service) bool {
	rev := s.Labels[LabelRevision]
	if rev == "" || s.Labels[LabelAgentUID] != string(agent.UID) {
		return false
	}
	return s.Name == WorkloadName(agent.Name, rev)
}

// collectRevisionServices deletes the Services of revisions that have left the
// retained set. A Service carries no ownerReference for the same reason the
// workload does not — the Agent is in another namespace — so Kubernetes will
// not collect it, and a leaked one keeps a name that a later revision of the
// same Agent would legitimately want.
func (r *AgentReconciler) collectRevisionServices(ctx context.Context, agent *assaydv1alpha1.Agent, runNS string, keep map[string]bool) error {
	owned, err := r.ownedServices(ctx, agent, runNS)
	if err != nil {
		return err
	}
	for i := range owned {
		if keep[owned[i].Labels[LabelRevision]] {
			continue
		}
		if err := r.Delete(ctx, &owned[i]); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("gc service %s: %w", owned[i].Name, err)
		}
	}
	return nil
}

// equalStrings compares two string slices by CONTENT, not length. Length alone
// is sound only while the render has no opinion about the values — which is
// true of externalIPs today and is exactly the kind of assumption that stops
// being true without anything failing.
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
