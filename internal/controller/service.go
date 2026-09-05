package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	plumev1alpha1 "github.com/Quinyte/plume/api/v1alpha1"
)

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
func (r *AgentReconciler) serviceFor(agent *plumev1alpha1.Agent, runNS, rev string) *corev1.Service {
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
				Name:     "a2a",
				Port:     port(agent.Spec.Runtime),
				Protocol: corev1.ProtocolTCP,
				// By NAME, not number. The container port is behaviour surface
				// (ADR-0031), so a port edit mints a new revision with its own
				// Deployment and its own Service — but naming the target means the
				// two halves of one revision can never disagree about it even if a
				// future change makes them render separately.
				TargetPort: intstr.FromString("a2a"),
			}},
		},
	}
}

// ensureService converges one revision's Service, under the same collision rule
// as its workload: a Service that exists under this name but was rendered from a
// DIFFERENT projection is a 40-bit name collision, and converging it would point
// the safe revision's route at the attacker's Pods. Stop instead.
func (r *AgentReconciler) ensureService(ctx context.Context, agent *plumev1alpha1.Agent, runNS, rev, digest string) error {
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

	// An absent annotation is adoption, not collision — a Service created before
	// this field existed carries none. Same rule as the workload's.
	if d, ok := existing.Annotations[RevisionDigestAnnotation]; ok && d != digest {
		// Typed, so reconcile reports RevisionHashCollision rather than returning a
		// bare error. A bare one retried forever and wrote no status — the silent
		// degraded path NFR-8 forbids, and the same defect ensureWorkload records.
		return &revisionCollisionError{name: desired.Name, existing: d, desired: digest, kind: "service"}
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
func (r *AgentReconciler) ownedServices(ctx context.Context, agent *plumev1alpha1.Agent, runNS string) ([]corev1.Service, error) {
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

func isRevisionService(agent *plumev1alpha1.Agent, s *corev1.Service) bool {
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
func (r *AgentReconciler) collectRevisionServices(ctx context.Context, agent *plumev1alpha1.Agent, runNS string, keep map[string]bool) error {
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
