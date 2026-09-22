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

	// TWO refusals, in this order, and each catches a case the other cannot.
	//
	// SHAPE first, because it is the more specific evidence and names the more
	// specific cause. serviceFor renders a ClusterIP Service with an allocated
	// address and no off-gateway publication, ever, so a revision Service that
	// is not that shape was not written by this operator's renderer — and this
	// is the ONLY check that catches the operator's OWN Service patched in
	// place, which keeps its UID label and its digest stamp and therefore
	// passes the provenance test below untouched.
	//
	// Refused rather than converged. Converging would rewrite a stranger's
	// object and destroy the only evidence it was ever planted, and it cannot
	// cover the shape that most needs covering: `spec.clusterIP` is immutable,
	// so a headless Service can never be converged back and would need a
	// refusal anyway. One rule beats one rule with an exception.
	//
	// What an ExternalName backendRef does at the GATEWAY is agentgateway's
	// choice, not a property this refusal may assume. Gateway API v1.6.0 says
	// implementations SHOULD NOT support one (`BackendObjectReference.Kind`,
	// CVE-2021-25740) and leaves it implementation-specific; agentgateway 1.5.0
	// resolves Service backends from EndpointSlices, of which an ExternalName
	// Service has none, so the route is reported Accepted and ResolvedRefs and
	// then serves nothing. The refusal rests on neither behaviour. It rests on
	// cluster DNS, which is the same everywhere: the revision's own address
	// answers with a CNAME to the planter's host.
	if terr := serviceNotRendered(&existing); terr != nil {
		return terr
	}

	// PROVENANCE second, the same three ways ensureWorkload establishes it. The
	// Service path had only the middle one — a stamped-and-mismatched digest —
	// so an unstamped Service at a revision's name was adopted whatever else it
	// said, and every field the renderer does not own rode along: a plain
	// ClusterIP Service carrying `publishNotReadyAddresses: true` was adopted,
	// stamped, given this Agent's UID label and named by the emitted route.
	// `status` is the non-forgeable side, as it is for the workload.
	vouched := (status.ActiveRevision == rev && status.ActiveRevisionDigest == digest) ||
		(status.CandidateRevision == rev && status.CandidateRevisionDigest == digest)
	existingDigest, stamped := existing.Annotations[RevisionDigestAnnotation]
	switch {
	case existing.Labels[LabelAgentUID] != string(agent.UID):
		return &revisionCollisionError{name: desired.Name, ns: runNS, existing: "(another agent's)",
			desired: digest, kind: "service"}
	case stamped && existingDigest != digest:
		// Typed, so reconcile reports RevisionHashCollision rather than returning a
		// bare error. A bare one retried forever and wrote no status — the silent
		// degraded path NFR-8 forbids, and the same defect ensureWorkload records.
		return &revisionCollisionError{name: desired.Name, ns: runNS, existing: existingDigest,
			desired: digest, kind: "service"}
	case !stamped && !vouched:
		return &revisionCollisionError{name: desired.Name, ns: runNS, existing: "(no stamp)",
			desired: digest, kind: "service"}
	}

	// ClusterIP is assigned by the API server and must survive the update, as
	// must the health-check node port and any field a future admission plugin
	// defaults. Converge only what this operator has an opinion about.
	updated := existing.DeepCopy()
	updated.Labels = desired.Labels
	if updated.Annotations == nil {
		updated.Annotations = map[string]string{}
	}
	updated.Annotations[RevisionDigestAnnotation] = digest
	updated.Spec.Selector = desired.Spec.Selector
	updated.Spec.Ports = desired.Spec.Ports
	if equalService(&existing, updated) {
		return nil
	}
	if err := r.Update(ctx, updated); err != nil {
		return fmt.Errorf("converge service %s: %w", desired.Name, err)
	}
	return nil
}

// serviceShapeError is a revision Service whose shape this operator never
// renders. It is terminal by design, exactly as a revision collision is: the
// remedy is a human deleting the object, and retrying quietly would leave the
// Agent saying nothing while the route it already has keeps its backendRef.
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

func (e *serviceShapeError) Error() string {
	const render = "The operator renders a revision's Service as a ClusterIP Service with an " +
		"allocated address, selecting that revision's Pods, publishing nothing outside the " +
		"gateway and no address of its own, so this object is not one it created."
	const remedy = "To recover: delete that Service and let the operator recreate it."
	switch e.what {
	case faultHeadless:
		return fmt.Sprintf("service %s/%s is headless (spec.clusterIP: None). %s spec.clusterIP is "+
			"immutable, so it cannot be repaired in place either, and the card fetch — which "+
			"addresses the Service by its ClusterIP — can never succeed against it. Refusing. %s",
			e.ns, e.name, render, remedy)
	case faultExternalIPs:
		// BOUNDED, because the value is the planter's and `spec.externalIPs` has
		// no item cap in Kubernetes validation while metav1.Condition.Message is
		// capped at 32768 RUNES. Rendered whole, a Service carrying a few
		// thousand addresses made every status write for that Agent fail with
		// `Too long`, freezing its whole status at the pre-incident value —
		// Ready=True, route published — while the refusal it was reporting went
		// unwritten. That is A81's incident class through a different door, and
		// it made this Agent QUIETER than before the refusal existed. Three
		// addresses name the object well enough for an operator to find it.
		return fmt.Sprintf("service %s/%s carries %d spec.externalIPs, beginning %v. %s Every node "+
			"DNATs those addresses to this revision's Pods, so the agent answers outside the "+
			"gateway, which is the one way in this platform claims to have on its governed tier "+
			"(CVE-2020-8554 is this "+
			"field). Refusing — but refusing is not closing: this operator declines to adopt the "+
			"object, and the Pods it selects go on answering those addresses until it is "+
			"deleted. %s",
			e.ns, e.name, len(e.externalIPs), e.externalIPs[:min(len(e.externalIPs), 3)], render, remedy)
	case faultNotReady:
		return fmt.Sprintf("service %s/%s sets spec.publishNotReadyAddresses. %s It sends traffic "+
			"to Pods that have not passed readiness, and readiness is what this operator promotes "+
			"a revision on, so the gate the rollout rests on would be answered by Pods that never "+
			"passed it. Refusing. %s", e.ns, e.name, render, remedy)
	case faultType:
		if e.typ == corev1.ServiceTypeExternalName {
			return fmt.Sprintf("service %s/%s is of type ExternalName and resolves to %q. %s "+
				"Adopting it as it stands would leave the serving route's backendRef — which this "+
				"operator writes, onto the assayd Gateway — naming a Service that has no endpoints "+
				"and can serve nothing, while every in-cluster DNS lookup of this revision's "+
				"address is answered with a CNAME to that host instead of the agent. Refusing. %s",
				e.ns, e.name, e.externalName, render, remedy)
		}
		// "Refusing" is declining to ADOPT, and a reader must not take it for
		// closing. ensureWorkload runs before ensureService, so the Deployment
		// exists by the time this fires, and a Service carrying the revision's
		// selector reaches those Pods whether this operator adopts it or not —
		// which is true of any name, not just this one, for anyone who can
		// create a Service here. Say so rather than let "Refusing." imply
		// otherwise.
		return fmt.Sprintf("service %s/%s is of type %s. %s A %s Service publishes this "+
			"revision's Pods outside the gateway, which is the one way in this platform claims "+
			"to have on its governed tier. Refusing — but refusing is not closing: this operator "+
			"declines to adopt "+
			"the object, and the Pods it selects go on running and go on answering it. Only "+
			"deleting it stops that. %s", e.ns, e.name, e.typ, render, e.typ, remedy)
	}
	// Unreachable: every shapeFault serviceNotRendered can return is above. A
	// bare panic here would take the manager down for a formatting bug.
	return fmt.Sprintf("service %s/%s is not a shape this operator renders. Refusing. %s",
		e.ns, e.name, remedy)
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
