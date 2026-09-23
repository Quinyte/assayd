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
	// a strictly stronger case, and a stale read could authorise the delete of
	// an object that no longer looks the way the decision was made on. The UID
	// precondition on the Delete then narrows the consequence to a refusal
	// rather than a wrong deletion, but the decision itself must be live.
	//
	// No test here can see the difference: envtest's client is uncached, so
	// r.reader() is r.Client and both paths read the same object.
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
	// ONE field cannot be repaired by an Update — `spec.clusterIP` is immutable
	// — so that object is DELETED and recreated instead. The human decided this
	// on 2026-09-22, after the first cut refused it.
	//
	// The warrant is the line above: provenance has already admitted the object
	// as ours, and an object carrying our stamp for this revision is ours to
	// replace as much as it is ours to rewrite. **The ordering is the safety
	// property**, not an accident of where the code sits: a foreign or
	// unstamped object never reaches this line, because every ground above
	// returns.
	//
	// What that buys is stated exactly, because the obvious sentence —
	// "nothing this operator did not create is ever deleted here" — was
	// measured FALSE: every element of the gate is forgeable by a principal who
	// can create a Service in this namespace, and a plant carrying a forged UID
	// label AND a forged digest is deleted and replaced. What the gate buys is
	// that an object which merely SITS at the name, unstamped or stamped for
	// another revision, is never destroyed, and that an adversary who forges
	// the whole set gets their own plant repaired — strictly worse for them
	// than leaving it unstamped, which buys a wedge. Design 02 §5 says it in
	// those terms.
	//
	// Refusing instead was measured to be a wedge, and the premise that made it
	// look narrow was false. `spec.clusterIP` is immutable EXCEPT across
	// transitions to and from `ExternalName`, so two strategic-merge patches
	// reach it with no create, no delete and no forged label — the object keeps
	// our UID and our stamp the whole way:
	//
	//	{"spec":{"type":"ExternalName","externalName":"elsewhere.example.com"}}
	//	{"spec":{"type":"ClusterIP","externalName":null,"clusterIP":"None","clusterIPs":["None"]}}
	//
	// That is `services/patch` alone, and it left a served Agent permanently
	// Degraded with its route still naming the object.
	//
	// Only this fault takes this path. Every shape an Update CAN repair is
	// repaired in place, because a delete is strictly more destructive and the
	// replacement gets a new ClusterIP — design 02 §5 states the gap.
	if shape != nil && shape.what == faultHeadless {
		// STAMPED, and with THIS revision's digest — the strongest evidence
		// available, and NOT a proof of creation.
		//
		// Every element of it is forgeable by a principal who can `create` a
		// Service here: the name is derived from the Agent's name and a public
		// revision hash, the `agent-uid` label is readable off any object in the
		// namespace, and the digest is a pure function of the spec that is also
		// stamped on the Deployment. routeCollision says the same of its own
		// labels and does not pretend otherwise. What this gate buys is that an
		// object which merely SITS at the name — unstamped, or stamped for a
		// different revision — is never destroyed, and that an adversary who
		// forges the whole set gets their own plant repaired. Design 02 §5
		// states it in those terms rather than as "nothing this operator did not
		// create is ever deleted", which was measured false.
		if !stamped || existingDigest != digest {
			return &serviceShapeError{
				ns: existing.Namespace, name: existing.Name,
				what: faultHeadless, unowned: true,
			}
		}
		return r.replaceUnrepairableService(ctx, &existing, desired, rev, status)
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
	return nil
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

// replaceUnrepairableService deletes a revision Service this operator owns and
// cannot repair by Update, and creates the rendered one in its place.
//
// CALLED ONLY ON AN OBJECT CARRYING THIS OPERATOR'S STAMP FOR THIS REVISION.
// That is the whole safety argument, and it is why this function takes the
// object the caller already validated rather than re-reading it: a second read
// could return a different object under the same name.
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
	ctx context.Context, existing, desired *corev1.Service, rev string,
	status *assaydv1alpha1.AgentStatus,
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
		}
	}

	if err := r.Delete(ctx, existing, client.Preconditions{UID: &existing.UID}); err != nil {
		if apierrors.IsNotFound(err) {
			// Already gone. The next pass reads what is there now; this one must
			// not go on to promote over an object it never established.
			return &serviceReplaceError{
				ns: existing.Namespace, name: existing.Name, stage: "vanished before it could be replaced", cause: err,
			}
		}
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
	// this controller writes.
	now := metav1.Now()
	status.ServiceReplacedRevision, status.ServiceReplacedAt = rev, &now
	log.FromContext(ctx).Info("replaced a revision Service that could not be repaired in place",
		"service", existing.Namespace+"/"+existing.Name, "reason", "spec.clusterIP is immutable",
		"was", existing.UID, "now", desired.UID)
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
}

func (e *serviceReplaceError) Error() string {
	return fmt.Sprintf("service %s/%s could not be repaired in place — spec.clusterIP is "+
		"immutable — and the replacement %s: %v. The serving route's backendRef names this "+
		"object by name, so whatever holds that name now is what traffic reaches. To recover: "+
		"check what is at that name, delete it if it is not this Agent's, and look for an "+
		"admission policy over services that refuses this operator's writes.",
		e.ns, e.name, e.stage, e.cause)
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
// through detail(). It is terminal by design, exactly as a revision collision
// is: the remedy is a human deleting the object.
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
	// unowned is set when the object was refused because this operator cannot
	// establish it created it. It is a DIFFERENT SITUATION from a held replace,
	// and the type carried both: the message described a replace that never
	// happened, at a zero `heldSince`, and — worse, because it is what an alert
	// keys on — the caller set one reason for either. A77 makes this exact
	// argument one type over for revisionCollisionError, where "an operator
	// whose object was refused for carrying no stamp was told two projections
	// had collided"; shipping it here would be that defect with its own fix as
	// the indictment.
	unowned bool
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
// `Ready.Reason` claiming a cooldown on an object that was never held, and no
// cooldown will ever run on it — the operator has committed to never replacing
// it.
//
// `Unstamped` is reused rather than minted. It is already this repo's word for
// "provenance could not be established", which is exactly why the operator will
// not act, and a new string is a vocabulary change with a cost (PR #59
// enumerates 64 reasons across 83 call sites). The state it names on the
// collision path — an object at a revision's name that nothing vouches for —
// is the same state, reached one branch over.
func (e *serviceShapeError) reason() string {
	if e.unowned {
		return "Unstamped"
	}
	return CondReasonServiceReplaceHeld
}

func (e *serviceShapeError) Error() string {
	if e.unowned {
		return fmt.Sprintf("service %s/%s cannot be repaired in place. %s This operator will "+
			"not delete and recreate it either, because it cannot establish that it created "+
			"it: the object carries no assayd.dev/revision-digest for this revision. An object "+
			"it merely finds at this name is rewritten, never destroyed. To recover: delete "+
			"that Service yourself and let the operator recreate it.",
			e.ns, e.name, e.detail())
	}
	// Otherwise the replace was HELD: every other unrepairable Service this
	// operator owns is deleted and recreated rather than reported.
	return fmt.Sprintf("service %s/%s cannot be repaired in place. %s This operator already "+
		"deleted and recreated it at %s and it is unrepairable again, so it is being left alone "+
		"rather than replaced a second time inside %s — replacing it on every pass would be a "+
		"delete, a new ClusterIP and a fresh traffic gap each time. Something is putting it back: "+
		"look for an admission policy that forces spec.clusterIP, or a principal with "+
		"services/patch on this namespace. To recover: stop whatever is rewriting it, then delete "+
		"that Service and let the operator recreate it.",
		e.ns, e.name, e.detail(), e.heldSince.UTC().Format(time.RFC3339), ServiceReplaceCooldown)
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
// type and still not one this operator writes — and the one fault no Update can
// repair, so it is the only one whose caller deletes rather than converges.
//
// This function is a SHAPE CLASSIFIER and knows nothing about the cooldown or
// about ownership: the fields those decisions need are set by the callers that
// make them. It returns a zero `heldSince` and a false `unowned` because it
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
