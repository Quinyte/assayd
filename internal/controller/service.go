// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

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
			// Explicit, because the converge asserts it: `Local` is served only
			// by endpoints on the caller's node and DROPS the request when there
			// are none, so one patch black-holes the gateway's hop to the agent.
			// The API server defaults it to `Cluster` anyway; rendering it says
			// the operator has the opinion rather than inheriting it.
			InternalTrafficPolicy: ptr(corev1.ServiceInternalTrafficPolicyCluster),
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

	// READ LIVE, not from the manager's cache.
	//
	// This object's read authorises two things the cache must not be trusted
	// for: rewriting it, and — for the one unrepairable shape — DELETING it.
	// main.go uncaches ConfigMap, Secret and Namespace for A60/A61 in exactly
	// these words: "a comparison against a cached object that a recreate has
	// already replaced is no proof at all". A Service this operator destroys is
	// a strictly stronger case. The UID precondition on the Delete protects a
	// DIFFERENT object at the name, and does nothing for a stale read of the
	// SAME one: a human who repaired our headless Service in place keeps its
	// UID, and a cached copy from before the repair would authorise deleting
	// the repair. TestAStaleCachedReadDoesNotDeleteAnInPlaceRepair measures
	// exactly that, with a client that answers from a stale snapshot beside a
	// live Reader.
	var existing corev1.Service
	err := r.reader().Get(ctx, client.ObjectKeyFromObject(desired), &existing)
	switch {
	case apierrors.IsNotFound(err):
		if err := r.Create(ctx, desired); err != nil {
			if apierrors.IsAlreadyExists(err) {
				// Read-after-write against a stale informer cache. Return rather
				// than recurse, for the reason ensureWorkload records at length.
				return fmt.Errorf("service %s appeared between the read and the create; "+
					"requeueing to validate it: %w", desired.Name, err)
			}
			// Marked, so the caller's ServiceRejected message can say which
			// write was refused: "delete that Service" names nothing on this
			// path, because the object was never created.
			return fmt.Errorf("create service %s: %w: %w", desired.Name, errServiceCreate, err)
		}
		// The API server's answer to OUR create is the one proof of creation
		// there is: nobody chooses a UID. Persisted NOW, in a write of its own,
		// rather than by the pass's final status write — see
		// persistServiceRecord.
		recordServiceUID(status, rev, digest, desired.UID)
		r.persistServiceRecord(ctx, agent, rev, digest, desired.UID, nil)
		return nil
	case err != nil:
		// Not a Service fault, and not reported as one. The live read needs RBAC
		// `get` on services, where the informer needed only list and watch, so a
		// Forbidden here is this operator's own permissions — and the remedy the
		// ServiceRejected update arm names, "delete that Service", would change
		// nothing: the operator still could not read what it recreated.
		return &serviceReadError{ns: runNS, name: desired.Name, cause: err}
	}

	// A RECORDED UID DECIDES FIRST, and it decides ownership outright.
	//
	// The UID is assigned by the API server and cannot be chosen by whoever
	// creates the object, so an object whose UID equals the one this operator
	// recorded when it created it IS that object — whatever a patch has since
	// done to its labels and annotations. The three provenance grounds below
	// read only labels and annotations: every one is forgeable by a principal
	// who can `create` a Service here, and strippable by one who can `patch`
	// it. Round three of A77's review measured the second: stripping the digest
	// annotation on the way to headless — `services/patch` and nothing else —
	// left the operator's OWN Service, same UID, refused as Unstamped on every
	// pass, which is the per-Agent wedge the human's decision exists to remove.
	// Stripping the agent-uid label reached the same wedge through the first
	// ground. Neither survives a UID match.
	recorded := recordedServiceUID(status, rev, digest)
	ours := recorded != "" && existing.UID == recorded

	// PROVENANCE, for an object this operator has no record of creating. The
	// three grounds are ensureWorkload's, unchanged: the `assayd.dev/agent-uid`
	// label is not this Agent's, the digest is stamped and differs, or it is
	// unstamped and `status` vouches for no such revision.
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
	if !ours {
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
	}

	// Past that point the object is OURS — by record, or by provenance — and a
	// wrong shape on our own object is CONVERGED, not refused.
	//
	// The first cut of A77 refused it, and the human reversed that on the fifth
	// review's argument. Provenance is what decides: rewriting an object that
	// is ours is exactly what ensureWorkload already does to the Deployment,
	// and "converging would rewrite a stranger's object" — the whole case for
	// refusing — is not true of it. The wedge was the deeper problem: refusing
	// here handed anyone with `services/patch` in a run namespace a permanent
	// per-Agent outage, needing no ExternalName and no cleverness, which is a
	// worse failure than the one this amendment closes.
	//
	// ONE field cannot be repaired by an Update — `spec.clusterIP` is immutable
	// — so that object is DELETED and recreated instead. The human decided this
	// on 2026-09-22, after the first cut refused it, and decided on 2026-09-23
	// what authorises it: the recorded UID, and nothing else.
	//
	// `spec.clusterIP` is immutable EXCEPT across transitions to and from
	// `ExternalName`, so two strategic-merge patches reach headless with no
	// create, no delete and no forged label — the object keeps our UID the
	// whole way:
	//
	//	{"spec":{"type":"ExternalName","externalName":"elsewhere.example.com"}}
	//	{"spec":{"type":"ClusterIP","externalName":null,"clusterIP":"None","clusterIPs":["None"]}}
	//
	// That is `services/patch` alone, and before the record it left a served
	// Agent permanently Degraded with its route still naming the object.
	//
	// Only this fault takes this path. Every shape an Update CAN repair is
	// repaired in place, because a delete is strictly more destructive and the
	// replacement gets a new ClusterIP — design 02 §5 states the gap.
	if shape != nil && shape.what == faultHeadless {
		// The delete is authorised by the RECORD, not by the stamp. The stamp
		// was the gate until round three of A77's review, and it was the wrong
		// authority in both directions: forgeable with `create` (a plant
		// carrying a forged agent-uid label AND a forged digest was deleted and
		// replaced) and strippable with `patch` (the wedge above). An object
		// whose UID is not the recorded one is never deleted here, whatever it
		// carries — including an object this operator did create but has no
		// record of, which is the migration cost design 02 §5 states.
		if !ours {
			return &serviceShapeError{
				ns: existing.Namespace, name: existing.Name,
				what: faultHeadless, unrecorded: true, recorded: recorded, live: existing.UID,
			}
		}
		return r.replaceUnrepairableService(ctx, agent, &existing, desired, rev, digest, recorded, status)
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
	// publishNotReadyAddresses restores the readiness gate promotion rests on;
	// and internalTrafficPolicy back to Cluster restores the one setting whose
	// `Local` value DROPS traffic rather than steering it — a Service served
	// only by endpoints on the caller's node black-holes the gateway's hop to
	// the agent on any multi-node cluster, and nothing reported it.
	updated.Spec.Type = desired.Spec.Type
	updated.Spec.ExternalName = ""
	updated.Spec.ExternalIPs = nil
	updated.Spec.PublishNotReadyAddresses = false
	updated.Spec.InternalTrafficPolicy = desired.Spec.InternalTrafficPolicy
	// AND the field that only exists UNDER a type we are leaving. Kubernetes
	// couples several to the type: `dropTypeDependentFields` clears
	// externalTrafficPolicy, allocateLoadBalancerNodePorts, healthCheckNodePort,
	// loadBalancerClass and the ports' nodePorts on a type change, which is why
	// those are not here. It does NOT clear loadBalancerSourceRanges, and the
	// API server then refuses the repair with `may only be used when type is
	// 'LoadBalancer'`. Measured: one `services/patch` setting type=LoadBalancer
	// with a source range left the Agent permanently Degraded under
	// ServiceRejected with the exposure intact — the wedge this convergence
	// exists to avoid, restored by asserting a subset of a coupled field set.
	//
	// It does not clear `loadBalancerIP` either. An earlier version of this
	// comment said it did, and the measurement says otherwise: a repaired
	// Service keeps it. It is left alone rather than added here because nothing
	// on a ClusterIP Service reads it — no type-coupled validation refuses it
	// and no load-balancer controller acts on it — so it is an inert leftover
	// and §5 lists it with the other fields this operator does not read.
	updated.Spec.LoadBalancerSourceRanges = nil
	if equalService(&existing, updated) {
		adoptServiceRecord(status, rev, digest, &existing)
		return nil
	}
	// SAY what was reset. There is no durable record of a repair — no
	// EventRecorder is wired and a condition would announce every overwritten
	// hand-edit as an incident — so an attempted off-gateway exposure of a
	// governed Agent otherwise leaves nothing but a resourceVersion bump. A log
	// line is greppable, costs no machinery, and does not pretend to be more
	// than it is. Design 02 §5 records that this is the whole signal.
	if reset := shapeFieldsReset(&existing); len(reset) > 0 {
		log.FromContext(ctx).Info("repairing a revision Service whose shape had drifted",
			"service", runNS+"/"+desired.Name, "fields", reset)
	}
	if err := r.Update(ctx, updated); err != nil {
		return fmt.Errorf("converge service %s: %w", desired.Name, err)
	}
	adoptServiceRecord(status, rev, digest, &existing)
	return nil
}

// recordedServiceUID is the UID `status.revisionServices` records for this
// revision's Service at this digest, or "" when there is none. The digest is
// part of the key so that a 40-bit revision-name collision between two
// projections cannot borrow the other's record and skip the DigestMismatch
// refusal.
func recordedServiceUID(status *assaydv1alpha1.AgentStatus, rev, digest string) types.UID {
	for _, rec := range status.RevisionServices {
		if rec.Revision == rev && rec.RevisionDigest == digest {
			return rec.UID
		}
	}
	return ""
}

// recordServiceUID sets the entry for this revision AT THIS DIGEST, replacing
// only an entry with the same revision and digest. It is called with the UID
// the API server returned on this operator's own create — at the first create
// and at a replace — and by adoptServiceRecord. Keying the replacement by the
// digest as well is what makes "adoption never overwrites a record" true
// without qualification: an adoption at another projection of the same
// 40-bit name adds an entry beside the existing one and cannot replace it.
func recordServiceUID(status *assaydv1alpha1.AgentStatus, rev, digest string, uid types.UID) {
	for i := range status.RevisionServices {
		if status.RevisionServices[i].Revision == rev && status.RevisionServices[i].RevisionDigest == digest {
			status.RevisionServices[i] = assaydv1alpha1.RevisionServiceRecord{
				Revision: rev, RevisionDigest: digest, UID: uid}
			return
		}
	}
	status.RevisionServices = append(status.RevisionServices, assaydv1alpha1.RevisionServiceRecord{
		Revision: rev, RevisionDigest: digest, UID: uid})
}

// persistServiceRecord writes a record the moment the create that produced
// its UID returns, in a status write of its own, instead of leaving it to the
// pass's final status write.
//
// Left to the final write, the record of a REPLACE was lost by any error or
// Conflict between the replace and the end of the pass — the card fetch, the
// gateway step, a concurrent edit to the Agent — and the operator's own
// replacement then stayed unrecorded: the next time it was made headless it
// was refused as RevisionServiceNotRecorded until a human deleted it, with the
// route still published. Round four of A77's review measured that with one
// injected Conflict. A destructive act ordered ahead of its own record is the
// ordering collectGarbage's comment calls backwards; this puts the record as
// close behind the create as a second API call can.
//
// It writes ONLY the record, and on a replace the bound. The first attempt
// sends the Agent as this pass read it, so the API server's resourceVersion
// check guarantees it overwrites nothing written since. On success the pass's
// copy takes the new resourceVersion and the stored record, so its final
// write neither conflicts with this one nor sees a difference to write. On a
// Conflict it re-reads the Agent live and re-applies only these fields onto
// what it finds, and leaves the pass's copy stale: the final write then
// Conflicts, the pass is retried, and nothing written concurrently is
// overwritten by a pass that did not read it.
//
// What remains is the process dying between the create and this write, or
// every attempt failing. Either leaves the record to the final write, as
// before, and design 02 §5 states it.
func (r *AgentReconciler) persistServiceRecord(ctx context.Context, agent *assaydv1alpha1.Agent,
	rev, digest string, uid types.UID, replacedAt *metav1.Time) {
	apply := func(st *assaydv1alpha1.AgentStatus) {
		recordServiceUID(st, rev, digest, uid)
		if replacedAt != nil {
			st.ServiceReplacedRevision, st.ServiceReplacedAt = rev, replacedAt
		}
	}
	current := agent.DeepCopy()
	apply(&current.Status)
	err := r.Status().Update(ctx, current)
	if err == nil {
		agent.ResourceVersion = current.ResourceVersion
		apply(&agent.Status)
		return
	}
	for attempt := 0; attempt < 4 && apierrors.IsConflict(err); attempt++ {
		var live assaydv1alpha1.Agent
		if gerr := r.reader().Get(ctx, client.ObjectKeyFromObject(agent), &live); gerr != nil {
			err = gerr
			break
		}
		apply(&live.Status)
		err = r.Status().Update(ctx, &live)
	}
	if err != nil {
		log.FromContext(ctx).Info("could not persist the revision Service record ahead of the "+
			"pass's final status write; it rides that write instead",
			"service", rev, "uid", uid, "error", err.Error())
	}
}

// adoptServiceRecord records an existing Service's UID when this revision has
// NO record at this digest yet, and only once the object has been converged to
// the render.
//
// It runs on the first sight of ANY revision's Service without a record, which
// includes every new revision, not only an upgraded Agent. The migration is
// why it exists: an Agent created before `status.revisionServices` existed has
// no record, and neither has one whose record write was lost after the create.
// Refusing to record such an object would leave the operator's own Service
// permanently unrecorded, so the two-patch wedge this record exists to heal
// would stay open for every upgraded Agent.
//
// What adoption concedes is bounded by what is adopted, and it is the same on
// every revision. A revision's name is predictable from its spec, so anyone
// who can create a Service in the run namespace can put one at the next
// revision's name before the operator does, carrying this Agent's UID label
// and the new digest. It is called only past provenance and only on the
// converge path, never for a headless object: the object it records is one
// this pass has just made identical to the render — selector, ports, labels,
// stamp and shape — and which the route will name. Recording it concedes no
// traffic the convergence had not; what it adds is that a LATER headless state
// of that object is deleted and replaced with the operator's own. What gets
// deleted is then the planter's own object. An object headless on the pass it
// is first seen is never recorded, so it is never deleted.
//
// A record that exists at this revision and digest and names a DIFFERENT
// object is not overwritten. That object passed provenance, so it is
// converged, but it will never be deleted here.
func adoptServiceRecord(status *assaydv1alpha1.AgentStatus, rev, digest string, existing *corev1.Service) {
	if recordedServiceUID(status, rev, digest) != "" || existing.UID == "" {
		return
	}
	recordServiceUID(status, rev, digest, existing.UID)
}

// pruneServiceRecords drops the records of revisions that have left the
// retained set, exactly as pruneCards does for cards: the record leaves with
// the Service it names.
func pruneServiceRecords(status *assaydv1alpha1.AgentStatus, keep map[string]bool) bool {
	if len(status.RevisionServices) == 0 {
		return false
	}
	before := len(status.RevisionServices)
	kept := status.RevisionServices[:0]
	for _, rec := range status.RevisionServices {
		if keep[rec.Revision] {
			kept = append(kept, rec)
		}
	}
	status.RevisionServices = kept
	return len(kept) != before
}

// shapeFieldsReset names the shape fields the converge is about to put back, so
// the log line says which. It reads the SAME fields the converge asserts; a
// field added to one and not the other is a repair nothing reports.
func shapeFieldsReset(existing *corev1.Service) []string {
	var reset []string
	if existing.Spec.Type != corev1.ServiceTypeClusterIP {
		reset = append(reset, "spec.type="+string(existing.Spec.Type))
	}
	if existing.Spec.ExternalName != "" {
		reset = append(reset, "spec.externalName")
	}
	if len(existing.Spec.ExternalIPs) > 0 {
		reset = append(reset, "spec.externalIPs")
	}
	if existing.Spec.PublishNotReadyAddresses {
		reset = append(reset, "spec.publishNotReadyAddresses")
	}
	if p := existing.Spec.InternalTrafficPolicy; p != nil && *p != corev1.ServiceInternalTrafficPolicyCluster {
		reset = append(reset, "spec.internalTrafficPolicy="+string(*p))
	}
	if len(existing.Spec.LoadBalancerSourceRanges) > 0 {
		reset = append(reset, "spec.loadBalancerSourceRanges")
	}
	return reset
}

// ServiceReplacedAnnotation says on the object that this operator replaced it,
// and when. It is a MARKER FOR A HUMAN and nothing reads it: the bound lives in
// `status.serviceReplacedAt`, because `metadata.annotations` is writable by the
// principal the bound exists to stop.
const ServiceReplacedAnnotation = "assayd.dev/service-replaced-at"

// ServiceReplaceCooldown bounds the replace to at most one per revision per
// window, counted from `status.serviceReplacedAt`.
//
// Without a bound, a replacement that lands back in the unrepairable state —
// a mutating webhook that forces `clusterIP: None`, or a principal patching it
// back — is a delete-and-create against the API server on every pass, each one
// a fresh ClusterIP and a fresh traffic gap. With it, the second occurrence
// inside the window is REPORTED instead, under a reason that says the replace
// was held. An attacker who re-patches after the window gets one more replace
// and one more gap, which is their loop rather than the operator's, and each
// iteration leaves the Service repaired.
const ServiceReplaceCooldown = 10 * time.Minute

// replaceUnrepairableService deletes a revision Service this operator created
// and cannot repair by Update, and creates the rendered one in its place.
//
// CALLED ONLY ON AN OBJECT WHOSE UID IS THE ONE `status.revisionServices`
// RECORDS FOR THIS REVISION. That is the whole safety argument: the UID was
// returned by the API server on this operator's own create, and nobody can
// choose it. It is why this function takes the object the caller already
// validated rather than re-reading it, and why the Delete's precondition is
// the RECORDED UID: a second read could return a different object under the
// same name, and the precondition turns that into a refusal.
//
// Three things guard the window the delete opens, and each was a measured
// failure before it existed.
//
//   - The BOUND is read from `status`, not from the object. An annotation was
//     writable by the principal it exists to stop: a future-dated value held
//     the replace forever (the permanent wedge this amendment removes) and a
//     deleted value removed the bound, producing a delete and a traffic gap on
//     every pass.
//   - The Create is DRY-RUN before the Delete. The route's backendRef names
//     the Service by NAME, so a delete frees that name for one round trip. A
//     Create that would be refused — by an admission policy over `services`,
//     which is the likeliest case — must not be discovered after the serving
//     object is already gone.
//   - A lost race NEVER returns nil. Returning success let the pass go on to
//     readiness, promotion and the route, so the operator credited whatever had
//     taken the name with the Agent's traffic and reported Ready=True about it.
func (r *AgentReconciler) replaceUnrepairableService(
	ctx context.Context, agent *assaydv1alpha1.Agent, existing, desired *corev1.Service,
	rev, digest string, recorded types.UID, status *assaydv1alpha1.AgentStatus,
) error {
	if status.ServiceReplacedRevision == rev && status.ServiceReplacedAt != nil {
		if since := time.Since(status.ServiceReplacedAt.Time); since >= 0 && since < ServiceReplaceCooldown {
			return &serviceShapeError{
				ns: existing.Namespace, name: existing.Name,
				what: faultHeadless, heldSince: status.ServiceReplacedAt.Time,
			}
		}
	}

	// Would the replacement be admitted? Asked BEFORE anything is destroyed.
	probe := desired.DeepCopy()
	if err := r.Create(ctx, probe, client.DryRunAll); err != nil && !apierrors.IsAlreadyExists(err) {
		return &serviceReplaceError{
			ns: existing.Namespace, name: existing.Name, stage: "would not be admitted", cause: err,
			standing: true,
		}
	}

	if err := r.Delete(ctx, existing, client.Preconditions{UID: &recorded}); err != nil {
		if apierrors.IsNotFound(err) {
			// Already gone. The next pass reads what is there now; this one must
			// not go on to promote over an object it never established.
			return &serviceReplaceError{
				ns: existing.Namespace, name: existing.Name, stage: "vanished before it could be replaced", cause: err,
			}
		}
		// A lost precondition, or any other refusal of the delete. Either way
		// the object it names may still be standing, or may be somebody else's.
		return &serviceReplaceError{
			ns: existing.Namespace, name: existing.Name, stage: "could not be deleted", cause: err,
		}
	}
	desired.Annotations[ServiceReplacedAnnotation] = time.Now().UTC().Format(time.RFC3339)
	if err := r.Create(ctx, desired); err != nil {
		// The name was free for a round trip and is now taken, or the create was
		// refused. Either way the serving object is GONE and this must be
		// reported, not returned bare — the pass that says nothing here is the
		// one that leaves a route pointing at a stranger's Service.
		return &serviceReplaceError{
			ns: existing.Namespace, name: existing.Name, stage: "could not be recreated after it was deleted", cause: err,
		}
	}
	// Recorded only once the replacement exists, and in `status`, which only
	// this controller writes. The new UID and the replace time are one status
	// write: a record that still named the deleted object would refuse the next
	// replace of the one this operator just created.
	now := metav1.Now()
	status.ServiceReplacedRevision, status.ServiceReplacedAt = rev, &now
	recordServiceUID(status, rev, digest, desired.UID)
	r.persistServiceRecord(ctx, agent, rev, digest, desired.UID, &now)
	log.FromContext(ctx).Info("replaced a revision Service that could not be repaired in place",
		"service", existing.Namespace+"/"+existing.Name, "reason", "spec.clusterIP is immutable",
		"was", existing.UID, "now", desired.UID)
	// And CHECKED. The create's response is what the API server stored, so a
	// mutating webhook that forces `clusterIP: None` on create is visible here.
	// Returning nil over it let the pass promote and route with Ready=True over
	// a replacement as headless as the object it replaced, for one pass before
	// the next one held.
	if desired.Spec.ClusterIP == "" || desired.Spec.ClusterIP == corev1.ClusterIPNone {
		return &serviceReplaceError{
			ns: existing.Namespace, name: existing.Name,
			stage: "was recreated with no ClusterIP", standing: true,
			cause: fmt.Errorf("the API server stored spec.clusterIP %q", desired.Spec.ClusterIP),
		}
	}
	return nil
}

// serviceReplaceError is a replace that did not complete. It is reported rather
// than returned bare, because every stage of it leaves the Agent worse than it
// found it: before the delete, an object that cannot be repaired; after, no
// object at all under a name the serving route still points at.
type serviceReplaceError struct {
	ns, name string
	stage    string
	cause    error
	// standing is set on the stages that delete nothing — the dry-run refusal
	// and a replacement stored headless — so the message can say what is
	// actually at the name instead of implying it may be nothing.
	standing bool
}

func (e *serviceReplaceError) Error() string {
	at := "The serving route's backendRef names this object by name, so whatever holds that name " +
		"now is what traffic reaches."
	if e.standing {
		at = "The object this operator read is still at that name, and the serving route's " +
			"backendRef still names it."
	}
	return fmt.Sprintf("service %s/%s could not be repaired in place — spec.clusterIP is "+
		"immutable — and the replacement %s: %v. %s To recover: check what is at that name, "+
		"delete it if it is not this Agent's, and look for an admission policy or webhook over "+
		"services that refuses or rewrites this operator's writes.",
		e.ns, e.name, e.stage, e.cause, at)
}

func (e *serviceReplaceError) Unwrap() error { return e.cause }

// errServiceCreate marks an error from the CREATE of a revision Service, as
// opposed to the update of one that already exists. The two have different
// remedies and the message said the same thing for both.
var errServiceCreate = errors.New("the revision Service could not be created")

// serviceShapeError is a revision Service whose shape this operator never
// renders.
//
// Only the headless fault is returned AS an error, because it is the only one
// convergence cannot repair; the rest ride on a provenance refusal as detail,
// through detail(). Both situations it is returned for are re-read every
// RefusedServiceRecheck: a held replace runs again once the cooldown has
// passed, and an unrecorded object is replaced by the operator's own create
// once a human deletes it.
type serviceShapeError struct {
	ns, name     string
	what         shapeFault
	typ          corev1.ServiceType
	externalName string
	externalIPs  []string
	// heldSince is set when the replace was HELD by the cooldown rather than
	// performed: the object was replaced at this time and is unrepairable
	// again, so replacing it once more would be a loop.
	heldSince time.Time
	// unrecorded is set when the object was refused because its UID is not
	// the one `status.revisionServices` records for this revision, so this
	// operator cannot establish that it created it. It is a DIFFERENT
	// SITUATION from a held replace, and the type carried both: the message
	// described a replace that never happened, at a zero `heldSince`, and —
	// worse, because it is what an alert keys on — the caller set one reason
	// for either. A77 makes this exact argument one type over for
	// revisionCollisionError.
	unrecorded bool
	// recorded is the UID status records for this revision, "" when there is
	// none; live is the UID of the object that was refused. Both are rendered,
	// because "not the object this operator created" is checkable only if the
	// reader can see which object that was.
	recorded, live types.UID
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
			"operator cannot repair it in place — every other shape it renders is converged. " +
			"The card fetch, which addresses the Service by its ClusterIP, can never succeed " +
			"against it either."
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

// reason is what the caller reports. It lives on the error because the error
// is what knows which situation it is: guarding only the prose would leave
// `Ready.Reason` claiming a cooldown on an object that was never held.
//
// The unrecorded case has its own reason, and it is NOT `Unstamped`, which it
// reused until the record replaced the stamp as the delete's authority. The
// object refused here may carry this revision's stamp — a forged one, or this
// operator's own on a Service whose record was never written — so a reason
// saying it carries none would be false of the object in front of the reader.
func (e *serviceShapeError) reason() string {
	if e.unrecorded {
		return CondReasonServiceNotRecorded
	}
	return CondReasonServiceReplaceHeld
}

func (e *serviceShapeError) Error() string {
	if e.unrecorded {
		record := fmt.Sprintf("status.revisionServices records UID %s for this revision", e.recorded)
		if e.recorded == "" {
			record = "status.revisionServices records no Service for this revision"
		}
		return fmt.Sprintf("service %s/%s cannot be repaired in place. %s This operator will "+
			"not delete and recreate it either, because it cannot establish that it created it: "+
			"%s, and this object is UID %s. Its labels and annotations are not evidence either "+
			"way, since any principal who can create a Service here can forge them. To recover: "+
			"delete that Service yourself, and the operator creates its own in its place and "+
			"records it.",
			e.ns, e.name, e.detail(), record, e.live)
	}
	// Otherwise the replace was HELD: every other unrepairable Service this
	// operator has a record of is deleted and recreated rather than reported.
	return fmt.Sprintf("service %s/%s cannot be repaired in place. %s This operator already "+
		"deleted and recreated it at %s and it is unrepairable again, so it is being left alone "+
		"rather than replaced a second time inside %s — replacing it on every pass would be a "+
		"delete, a new ClusterIP and a fresh traffic gap each time. Something is putting it back: "+
		"look for an admission policy that forces spec.clusterIP, or a principal with "+
		"services/patch on this namespace. To recover: stop whatever is rewriting it, then delete "+
		"that Service and let the operator recreate it.",
		e.ns, e.name, e.detail(), e.heldSince.UTC().Format(time.RFC3339), ServiceReplaceCooldown)
}

// serviceReadError is a revision Service this operator could not READ, for a
// reason that is not "it does not exist". It is reported under its own reason
// because the fault is the operator's access, not the object: nothing was
// written, and deleting the Service would change nothing.
type serviceReadError struct {
	ns, name string
	cause    error
}

func (e *serviceReadError) Error() string {
	why := "Nothing was written to it."
	if apierrors.IsForbidden(e.cause) || apierrors.IsUnauthorized(e.cause) {
		why = "This operator's own credentials were refused: its ClusterRole must grant get on " +
			"services, which the chart's operator ClusterRole does (charts/assayd/files/" +
			"operator-rules.yaml) — check that the role and its binding are installed and " +
			"unmodified. Nothing was written to the Service, and deleting it would change nothing."
	}
	return fmt.Sprintf("could not read revision Service %s/%s: %v. %s", e.ns, e.name, e.cause, why)
}

func (e *serviceReadError) Unwrap() error { return e.cause }

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
// type and still not one this operator writes — and the one fault no Update can
// repair, so it is the only one whose caller deletes rather than converges.
//
// This function is a SHAPE CLASSIFIER and knows nothing about the cooldown or
// about ownership: the fields those decisions need are set by the callers that
// make them. It returns a zero `heldSince` and a false `unrecorded` because it
// has no business setting either.
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
	// Lengths, not contents, for the two slices: `b` is the render, which names
	// no address in either, so any non-empty value on `a` differs in length.
	// A content comparison was written here and deleted — its loop body could
	// never execute, and unreachable code that reads as load-bearing is what
	// rule 5 forbids. If the render ever names an address this must compare
	// contents, and §5 says so.
	if a.Spec.Type != b.Spec.Type || a.Spec.ExternalName != b.Spec.ExternalName ||
		a.Spec.PublishNotReadyAddresses != b.Spec.PublishNotReadyAddresses ||
		!equalInternalTrafficPolicy(a.Spec.InternalTrafficPolicy, b.Spec.InternalTrafficPolicy) ||
		len(a.Spec.ExternalIPs) != len(b.Spec.ExternalIPs) ||
		len(a.Spec.LoadBalancerSourceRanges) != len(b.Spec.LoadBalancerSourceRanges) {
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

// equalInternalTrafficPolicy compares the pointer field by VALUE. A stored
// Service always carries one, because the API server defaults it, and the
// render always sets it — but nil is compared rather than dereferenced so a
// Service stored before the field existed cannot panic the manager.
func equalInternalTrafficPolicy(a, b *corev1.ServiceInternalTrafficPolicy) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
