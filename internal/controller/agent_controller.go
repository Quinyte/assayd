// Package controller holds the agent-operator's reconcilers.
//
// Design 02 §4 states the loop; this file implements the slice that needs no
// other plume component: observe → compute the desired revision → materialize
// its workload → track rollout state → report conditions and phase.
//
// Deliberately absent, because the components do not exist yet: card fetch and
// directory registration (§3.4), identity wiring (§3.5), the gateway handoff
// (§3.6), and gate-controller signals (§3.3 step 3). Each is a named seam here
// rather than a silent gap — see the Held phase and GatesSkipped below.
package controller

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	plumev1alpha1 "github.com/Quinyte/plume/api/v1alpha1"
	"github.com/Quinyte/plume/internal/revision"
)

const (
	// LabelAgent and LabelRevision let the operator find the workloads it owns
	// without trusting names, and let a human find them with kubectl.
	LabelAgent    = "plume.dev/agent"
	LabelRevision = "plume.dev/revision"

	// Finalizer gates the ordered teardown of §3.7. The teardown steps that need
	// the gateway and directory are not implemented; the finalizer is installed
	// now so that agents created today do not need a migration to acquire it.
	Finalizer = "plume.dev/agent-teardown"

	// DefaultRevisionHistoryLimit is the number of revisions retained IN ADDITION
	// TO the active one and any in-flight candidate (design 02 §3.3). Counting
	// them inside the limit would let a rollout garbage-collect its own rollback
	// target, destroying the instant-rollback property ADR-0019 exists for.
	DefaultRevisionHistoryLimit = 2

	// maxSupersededCandidates matches the CRD's MaxItems on the same field. The
	// API server would reject a longer list, so the operator must trim rather than
	// discover the cap at write time.
	maxSupersededCandidates = 10
)

// AgentReconciler reconciles an Agent toward its desired revision.
type AgentReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// EvalSuiteInstalled reports whether the EvalSuite CRD is present. Design 02
	// §3.3 makes gating conditional on it: on a core-only install rollouts
	// proceed with a loud GatesSkipped rather than blocking forever on a
	// controller that was never installed.
	//
	// UNSET MEANS "ASSUME INSTALLED", which holds. The previous default returned
	// false, so an unwired reconciler promoted everything through declared gates
	// while asserting a CRD check it never performed. A default on a safety hook
	// must fail in the recoverable direction: holding is recoverable, ungated
	// promotion is not. Prefer NewAgentReconciler, which refuses to construct one
	// without the hook.
	EvalSuiteInstalled func() bool
}

// NewAgentReconciler builds a reconciler with its dependencies stated, so that
// wiring a manager cannot silently decide ADR-0006's fate by omission.
func NewAgentReconciler(c client.Client, scheme *runtime.Scheme, evalSuiteInstalled func() bool) (*AgentReconciler, error) {
	switch {
	case c == nil:
		return nil, fmt.Errorf("agent reconciler: client is required")
	case scheme == nil:
		return nil, fmt.Errorf("agent reconciler: scheme is required")
	case evalSuiteInstalled == nil:
		return nil, fmt.Errorf("agent reconciler: evalSuiteInstalled is required — " +
			"whether the EvalSuite CRD is present decides whether rollouts are eval-gated " +
			"(ADR-0006), and it must be an explicit decision rather than a zero value")
	}
	return &AgentReconciler{Client: c, Scheme: scheme, EvalSuiteInstalled: evalSuiteInstalled}, nil
}

// +kubebuilder:rbac:groups=plume.dev,resources=agents,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=plume.dev,resources=agents/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=plume.dev,resources=agents/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// Leader election. Without these the elector retries a forbidden lease forever:
// the pod runs, reports Ready, and reconciles nothing — the silence NFR-8 forbids.
// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=get;list;watch;create;update;patch
// Events are the durable record design 02 §3.3 promises for a superseded
// candidate; status is the convenience and is capped.
// A20: the revision identity covers the resolved CONTENT of every referenced
// env source, so the operator must read them. `get` only — it never writes a
// user's ConfigMap or Secret, and the copies A35 will create live under names
// this operator owns.
// +kubebuilder:rbac:groups="",resources=configmaps;secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// Discovery of whether the EvalSuite CRD is installed decides whether rollouts
// are eval-gated (ADR-0006), so the operator must be able to see CRDs.
// +kubebuilder:rbac:groups=apiextensions.k8s.io,resources=customresourcedefinitions,verbs=get;list;watch

func (r *AgentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var agent plumev1alpha1.Agent
	if err := r.Get(ctx, req.NamespacedName, &agent); err != nil {
		// A deleted Agent is not an error; its workloads go with it via ownerRefs.
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !agent.DeletionTimestamp.IsZero() {
		return r.finalize(ctx, &agent)
	}

	if !containsString(agent.Finalizers, Finalizer) {
		agent.Finalizers = append(agent.Finalizers, Finalizer)
		if err := r.Update(ctx, &agent); err != nil {
			return ctrl.Result{}, fmt.Errorf("add finalizer to %s: %w", req.NamespacedName, err)
		}
		// Requeue explicitly rather than relying on our own write producing a watch
		// event: finalizers are metadata and do not bump metadata.generation, so
		// adding a GenerationChangedPredicate later would strand every new Agent here.
		return ctrl.Result{Requeue: true}, nil
	}

	// An external agent runs elsewhere: there is no workload to materialize. The
	// rest of its lifecycle (OAuth client, card fetch, gateway route) belongs to
	// components that do not exist yet, so it is held rather than pretended Ready.
	if agent.Spec.External != nil {
		return r.reconcileExternal(ctx, &agent)
	}

	if agent.Spec.Runtime == nil {
		// Admission requires exactly one of runtime/external, and spec is now
		// required — but an operator must not panic on an object it was handed,
		// whatever admission did or did not run when it was created.
		return ctrl.Result{}, r.reportUnreconcilable(ctx, &agent)
	}

	// A20: resolve every referenced env source BEFORE computing an identity. The
	// content is part of the identity, so a spec alone cannot produce one — which
	// is the point: an editor changing a referenced ConfigMap must mint a
	// candidate, not serve new behaviour under the old revision's gate result.
	resolved, unresolved, err := r.resolveEnvSources(ctx, &agent)
	if err != nil {
		return ctrl.Result{}, err
	}

	// `desired` NAMES the revision; `desiredDigest` IDENTIFIES it. Every equality
	// test below is on the digest, because the name is 40 bits and a chosen
	// collision against it takes about a second (A37).
	desired, herr := revision.Hash(agent.Spec, resolved)
	desiredDigest, derr := revision.Digest(agent.Spec, resolved)
	if herr != nil || derr != nil {
		// No revision is minted and no workload is created. A missing referent is
		// UNRESOLVED, never a zero digest: a zero would let deleting an object
		// mint the same hash as never having referenced it.
		return ctrl.Result{}, r.reportUnresolvedSources(ctx, &agent, unresolved, cmp.Or(herr, derr))
	}
	logger = logger.WithValues("revision", desired)

	owned, err := r.ownedWorkloads(ctx, &agent)
	if err != nil {
		return ctrl.Result{}, err
	}

	status := agent.Status.DeepCopy()
	conds := newConditionSet(agent.Generation)

	// The assessors run BEFORE anything can return early. A pass that exits
	// without them merges a condition set that never saw them, and because owned
	// conditions are non-sticky, merge() then CLEARS every one — so entering the
	// terminal collision state below used to retract SandboxDowngraded,
	// GatesSkipped and EnvSourceProtectionUnavailable on an agent that is more
	// degraded, not less.
	r.assessGates(&agent, conds)
	r.assessSandbox(&agent, conds)
	r.assessTaskState(&agent, conds)
	r.assessEnvSourceProtection(&agent, conds)

	// STATUS is the authority on which revision a name belongs to, and it is
	// checked before the workload is touched.
	//
	// The first version of this guard read a plume.dev/revision-digest annotation
	// off the Deployment. That closed the collision against an agents/update
	// principal and left it wide open to a WEAKER one: anyone with
	// deployments/patch could set the annotation to the digest of the spec they
	// were about to write — a pure function of that spec, no secret — or simply
	// delete it, which the adoption rule blessed. Either way the operator itself
	// installed the attacker's image on the gated revision, and unlike a raw
	// image patch it survived drift correction. The guard was defeatable by the
	// same principal the drift-correction block exists to defeat.
	//
	// status.activeRevisionDigest is on the status subresource, under separate
	// RBAC, and is written only here. The annotation is kept as corroboration.
	if c := collisionAgainstStatus(status, desired, desiredDigest); c != nil {
		return ctrl.Result{}, r.reportCollision(ctx, &agent, status, conds, c)
	}

	// A new generation supersedes an in-flight candidate (§3.3). At most one
	// candidate is ever in flight; the abandoned one is recorded so the
	// transition is auditable rather than silent.
	if status.CandidateRevision != "" && status.CandidateRevisionDigest != desiredDigest {
		status.SupersededCandidates = appendSuperseded(status.SupersededCandidates,
			status.CandidateRevision+"@"+status.CandidateRevisionDigest)
		logger.Info("superseding in-flight candidate",
			"superseded", status.CandidateRevision, "candidate", desired)
		status.CandidateRevision, status.CandidateRevisionDigest = "", ""
	}

	if err := r.ensureWorkload(ctx, &agent, desired, desiredDigest, status); err != nil {
		if collision := (*revisionCollisionError)(nil); errors.As(err, &collision) {
			return ctrl.Result{}, r.reportCollision(ctx, &agent, status, conds, collision)
		}
		if apierrors.IsInvalid(err) {
			// The API server rejected the rendered workload. Returning a bare error
			// here retried forever and wrote NO status, so the Agent sat at an empty
			// phase with nothing said — the silent degraded path NFR-8 forbids, and
			// the one an operator is least able to diagnose because the reason lives
			// only in operator logs. A rejected render is a spec the user can fix.
			conds.set(plumev1alpha1.CondReady, metav1.ConditionFalse, "WorkloadRejected", err.Error())
			conds.set(plumev1alpha1.CondDegraded, metav1.ConditionTrue, "WorkloadRejected", err.Error())
			status.Phase = plumev1alpha1.PhaseDegraded
			status.Conditions = conds.merge(agent.Status.Conditions)
			status.ObservedGeneration = agent.Generation
			return ctrl.Result{}, r.writeStatus(ctx, &agent, status)
		}
		return ctrl.Result{}, err
	}

	ready, err := r.workloadAvailable(ctx, &agent, desired, desiredDigest)
	if err != nil {
		return ctrl.Result{}, err
	}

	switch {
	case !ready && status.ActiveRevision != "" && status.ActiveRevisionDigest != desiredDigest &&
		r.revisionAvailable(ctx, &agent, status.ActiveRevision, status.ActiveRevisionDigest):
		// A genuine rollout: an earlier revision still holds all traffic and is
		// healthy while its successor comes up. Reporting Ready=False here would
		// trip every alert keyed on the canonical condition on any routine spec
		// edit; reporting Canary would claim a weighted shift that §3.3 defines
		// and that no gateway is performing yet (A13).
		status.CandidateRevision, status.CandidateRevisionDigest = desired, desiredDigest
		status.Phase = plumev1alpha1.PhaseReady
		conds.set(plumev1alpha1.CondReady, metav1.ConditionTrue, "Available",
			fmt.Sprintf("revision %s is serving", status.ActiveRevision))
		// The guard on this case now CHECKS that. `ready` is computed for the
		// DESIRED revision only, so this branch used to report "revision X is
		// serving" from the mere presence of a name in status — true of an agent
		// whose active workload had been deleted, which then read as a healthy
		// rollout instead of an outage.
		conds.set(plumev1alpha1.CondProgressing, metav1.ConditionTrue, "CandidateNotAvailable",
			fmt.Sprintf("revision %s is rolling out; %s continues to serve",
				desired, status.ActiveRevision))

	case !ready:
		// Either nothing has ever served, or — the case the guard above exists for
		// — the ACTIVE revision itself has no available replicas. `ready` is
		// computed for the DESIRED revision, so without the active != desired test
		// an agent whose pods died would report "revision X is serving" about a
		// workload serving nothing, and Progressing about a revision rolling out
		// over itself.
		status.CandidateRevision, status.CandidateRevisionDigest = desired, desiredDigest
		conds.set(plumev1alpha1.CondProgressing, metav1.ConditionFalse, "NoRolloutInFlight",
			"no other revision is coming up")
		// EQUIVALENT to comparing names here, and the argument is worth writing
		// down because a mutation shows this line is unpinned. This branch is
		// reached only when the case above was false, which means either
		// ActiveRevision is empty or the digests already match — and in both
		// states the name test and the digest test agree. It is written on the
		// digest anyway, so that every revision comparison in this function reads
		// the same way and none has to be re-derived as safe. The premise is that
		// the name and the digest are always written together, which
		// TestNameAndDigestAreAlwaysWrittenTogether checks.
		if status.ActiveRevisionDigest == desiredDigest {
			// It was serving and is not any more. Say so; do not silently keep the
			// promotion from a healthier moment.
			status.Phase = plumev1alpha1.PhaseDegraded
			status.CandidateRevision, status.CandidateRevisionDigest = "", ""
			// Assert Degraded, do not merely set the phase. CondDegraded is owned and
			// non-sticky, so a path that sets the phase without the condition actively
			// CLEARS it — and an alert keyed on the condition would miss the worst
			// case in this machine.
			conds.set(plumev1alpha1.CondDegraded, metav1.ConditionTrue, "WorkloadUnavailable",
				fmt.Sprintf("revision %s is the active revision but has no available replicas", desired))
			conds.set(plumev1alpha1.CondReady, metav1.ConditionFalse, "WorkloadUnavailable",
				fmt.Sprintf("revision %s is the active revision but has no available replicas", desired))
			break
		}
		status.Phase = plumev1alpha1.PhasePending
		conds.set(plumev1alpha1.CondReady, metav1.ConditionFalse, "WorkloadNotAvailable",
			fmt.Sprintf("revision %s has no available replicas yet", desired))

	case !r.gatesSatisfied(&agent) && status.ActiveRevisionDigest != desiredDigest:
		// Gates are required and have not passed: hold the candidate at zero
		// traffic (§3.3).
		//
		// The `ActiveRevision != desired` guard is load-bearing. `gates` is
		// policy-surface (A12), so adding one does not mint a revision — and
		// without the guard, adding a gate to a running agent held the revision
		// already serving 100% of traffic, listing it as its own candidate. A12
		// put gates on the policy surface with the reason that "a gate that
		// re-gated itself on edit could not converge"; this is that case. Whether the AGENT is ready is a separate question from
		// whether the CANDIDATE may promote — if an active revision is still
		// serving, saying Ready=False would page the on-call for adding gates to a
		// healthy agent, which is the same defect A13 fixed in the branch below.
		status.Phase = plumev1alpha1.PhaseHeld
		status.CandidateRevision, status.CandidateRevisionDigest = desired, desiredDigest
		// Progressing must be asserted here too: it is sticky, so a branch that
		// stays silent leaves the previous reason in place — and "CandidateNotAvailable"
		// is false once the candidate is available and merely held.
		conds.set(plumev1alpha1.CondProgressing, metav1.ConditionTrue, "AwaitingGates",
			fmt.Sprintf("revision %s is available and held at zero traffic pending its eval gates", desired))
		if status.ActiveRevision != "" {
			conds.set(plumev1alpha1.CondReady, metav1.ConditionTrue, "Available",
				fmt.Sprintf("revision %s is serving", status.ActiveRevision))
		} else {
			conds.set(plumev1alpha1.CondReady, metav1.ConditionFalse, "AwaitingGates",
				fmt.Sprintf("revision %s is held at zero traffic pending its eval gates, "+
					"and no earlier revision is serving", desired))
		}

	default:
		// Promote. Without the gateway there are no weights to shift, so this
		// flips activeRevision only — the weight shift belongs to design 03.
		// LOG ONLY — the assignment below is unconditional, so this comparison
		// decides nothing and no test pins it. Said explicitly because every other
		// revision comparison in this function IS load-bearing and is pinned by a
		// collision test; a reader should not have to work out which this is.
		if status.ActiveRevisionDigest != desiredDigest {
			logger.Info("promoting revision", "from", status.ActiveRevision, "to", desired)
		}
		status.ActiveRevision, status.ActiveRevisionDigest = desired, desiredDigest
		status.CandidateRevision, status.CandidateRevisionDigest = "", ""
		// Rolling A -> B -> A leaves A in the superseded list, where a revision
		// that is currently serving reads as one that was abandoned.
		status.SupersededCandidates = removeString(status.SupersededCandidates, desired+"@"+desiredDigest)
		status.Phase = plumev1alpha1.PhaseReady
		conds.set(plumev1alpha1.CondProgressing, metav1.ConditionFalse, "RolloutComplete",
			fmt.Sprintf("revision %s is the active revision", desired))
		conds.set(plumev1alpha1.CondReady, metav1.ConditionTrue, "Available",
			fmt.Sprintf("revision %s is serving", desired))
	}

	status.Conditions = conds.merge(agent.Status.Conditions)
	status.ObservedGeneration = agent.Generation
	if err := r.writeStatus(ctx, &agent, status); err != nil {
		return ctrl.Result{}, err
	}
	// Collect AFTER the status that authorizes it is durable: a destructive act
	// ordered ahead of its own record is backwards, even when it converges.
	return ctrl.Result{}, r.collectGarbage(ctx, &agent, owned, status)
}

// reportUnreconcilable records why an Agent cannot be acted on, rather than
// error-looping with an empty status that says nothing.
func (r *AgentReconciler) reportUnreconcilable(ctx context.Context, agent *plumev1alpha1.Agent) error {
	status := agent.Status.DeepCopy()
	status.Phase = plumev1alpha1.PhaseDegraded
	conds := newConditionSet(agent.Generation)
	conds.set(plumev1alpha1.CondDegraded, metav1.ConditionTrue, "NoWorkloadSpecified",
		"neither spec.runtime nor spec.external is set, so there is nothing to reconcile; "+
			"set exactly one of them")
	conds.set(plumev1alpha1.CondReady, metav1.ConditionFalse, "NoWorkloadSpecified",
		"the agent specifies no workload")
	status.Conditions = conds.merge(agent.Status.Conditions)
	status.ObservedGeneration = agent.Generation
	return r.writeStatus(ctx, agent, status)
}

// reconcileExternal handles an agent that runs outside this cluster.
func (r *AgentReconciler) reconcileExternal(ctx context.Context, agent *plumev1alpha1.Agent) (ctrl.Result, error) {
	status := agent.Status.DeepCopy()
	status.Phase = plumev1alpha1.PhasePending
	conds := newConditionSet(agent.Generation)
	conds.set(plumev1alpha1.CondReady, metav1.ConditionFalse, "ExternalRegistrationUnimplemented",
		"external agents need OAuth client provisioning (design 06) and card fetch (§3.4), "+
			"neither of which is implemented; the agent is held rather than reported ready")
	status.Conditions = conds.merge(agent.Status.Conditions)
	status.ObservedGeneration = agent.Generation
	return ctrl.Result{}, r.writeStatus(ctx, agent, status)
}

// ensureWorkload creates or updates the Deployment for one revision.
// RevisionDigestAnnotation records which projection a workload was rendered
// from. The Deployment's NAME carries only 40 bits, so the name alone cannot
// answer "is this the same revision?" — this can.
const RevisionDigestAnnotation = "plume.dev/revision-digest"

// revisionCollisionError reports two different projections claiming one
// workload name. It is terminal by design: see ensureWorkload.
type revisionCollisionError struct {
	name, existing, desired string
	// role is set when the collision was found in STATUS rather than on a
	// workload, so the message can say "the active revision …" instead of
	// rendering a role name into a "workload %s" slot.
	role string
}

func (e *revisionCollisionError) Error() string {
	if e.existing == "(no stamp)" {
		return fmt.Sprintf("workload %s carries no plume.dev/revision-digest and nothing in status "+
			"vouches for it, so this operator cannot establish that it created it. Refusing to "+
			"converge. To recover: delete that Deployment and let the operator recreate it.", e.name)
	}
	if e.role != "" {
		return fmt.Sprintf("the %s revision %s was gated with digest %s and this spec projects to %s: "+
			"two different projections share one 40-bit revision name. Refusing, because promoting "+
			"this spec would serve it under the gate result the other one earned. To recover: revert "+
			"the spec, or change it so it no longer projects to the same name.",
			e.role, e.name, e.existing, e.desired)
	}
	return fmt.Sprintf("workload %s was rendered from revision digest %s and this spec projects to %s: "+
		"two different projections share one 40-bit name. Refusing to converge, because rewriting it "+
		"would serve this spec under the gate result the other revision earned. To recover: delete "+
		"that Deployment and let the operator recreate it.", e.name, e.existing, e.desired)
}

// collisionAgainstStatus reports a desired revision whose NAME matches a
// recorded one while its identity does not. Status is written only by this
// controller through the status subresource, so it is the non-forgeable side of
// the comparison.
func collisionAgainstStatus(st *plumev1alpha1.AgentStatus, rev, digest string) *revisionCollisionError {
	for _, r := range []struct{ role, name, dig string }{
		{"active", st.ActiveRevision, st.ActiveRevisionDigest},
		{"candidate", st.CandidateRevision, st.CandidateRevisionDigest},
	} {
		if r.name == rev && r.dig != "" && r.dig != digest {
			return &revisionCollisionError{role: r.role, name: r.name, existing: r.dig, desired: digest}
		}
	}
	return nil
}

// reportCollision is the single exit for every collision path, so none of them
// can drift into asserting a different set of conditions than the others.
func (r *AgentReconciler) reportCollision(ctx context.Context, agent *plumev1alpha1.Agent,
	status *plumev1alpha1.AgentStatus, conds *conditionSet, c *revisionCollisionError) error {
	conds.set(plumev1alpha1.CondRevisionHashCollision, metav1.ConditionTrue, "DigestMismatch", c.Error())
	conds.set(plumev1alpha1.CondReady, metav1.ConditionFalse, "RevisionHashCollision", c.Error())
	// Degraded is asserted, not merely implied by the phase. CondDegraded is
	// owned and non-sticky, so a path that sets the phase and stays silent
	// actively CLEARS the condition — and a suspected chosen collision is the
	// worst state this machine has, which is exactly when an alert keyed on the
	// condition must not go quiet.
	conds.set(plumev1alpha1.CondDegraded, metav1.ConditionTrue, "RevisionHashCollision", c.Error())
	status.Phase = plumev1alpha1.PhaseDegraded
	status.Conditions = conds.merge(agent.Status.Conditions)
	status.ObservedGeneration = agent.Generation
	return r.writeStatus(ctx, agent, status)
}

// revisionAvailable answers whether a NAMED revision has available replicas,
// as opposed to workloadAvailable, which only ever answers for the desired one.
// An error is reported as unavailable: claiming a revision is serving because
// the API server did not answer is the loud-and-wrong of rule 8.
func (r *AgentReconciler) revisionAvailable(ctx context.Context, agent *plumev1alpha1.Agent, rev, digest string) bool {
	ok, err := r.workloadAvailable(ctx, agent, rev, digest)
	return err == nil && ok
}

// resolveEnvSources reads every ConfigMap and Secret the runtime references and
// returns their content digests. A source that does not exist, or that the
// operator may not read, is returned as unresolved rather than as an error: the
// two are the same fact to an Agent — its behaviour is not knowable — and both
// must stop the revision rather than produce a partial identity.
func (r *AgentReconciler) resolveEnvSources(ctx context.Context, agent *plumev1alpha1.Agent) (
	revision.Resolved, []revision.SourceRef, error) {
	refs := revision.EnvSources(agent.Spec)
	if len(refs) == 0 {
		return nil, nil, nil
	}
	out := revision.Resolved{}
	var unresolved []revision.SourceRef
	for _, ref := range refs {
		key := types.NamespacedName{Namespace: agent.Namespace, Name: ref.Name}
		switch ref.Kind {
		case "ConfigMap":
			var cm corev1.ConfigMap
			if err := r.Get(ctx, key, &cm); err != nil {
				if apierrors.IsNotFound(err) || apierrors.IsForbidden(err) {
					unresolved = append(unresolved, ref)
					continue
				}
				return nil, nil, fmt.Errorf("read %s for %s: %w", ref, agent.Name, err)
			}
			out[ref] = revision.ContentDigest(cm.Data, cm.BinaryData)
		case "Secret":
			var sec corev1.Secret
			if err := r.Get(ctx, key, &sec); err != nil {
				if apierrors.IsNotFound(err) || apierrors.IsForbidden(err) {
					unresolved = append(unresolved, ref)
					continue
				}
				return nil, nil, fmt.Errorf("read %s for %s: %w", ref, agent.Name, err)
			}
			// StringData is a write-only convenience the API server folds into
			// Data, so reading Data alone is complete.
			out[ref] = revision.ContentDigest(nil, sec.Data)
		}
	}
	return out, unresolved, nil
}

// reportUnresolvedSources is the A20 failure path: no revision, no workload, and
// a condition naming what could not be read.
func (r *AgentReconciler) reportUnresolvedSources(ctx context.Context, agent *plumev1alpha1.Agent,
	unresolved []revision.SourceRef, cause error) error {
	status := agent.Status.DeepCopy()
	conds := newConditionSet(agent.Generation)
	r.assessGates(agent, conds)
	r.assessSandbox(agent, conds)
	r.assessTaskState(agent, conds)

	names := make([]string, 0, len(unresolved))
	for _, u := range unresolved {
		names = append(names, u.String())
	}
	msg := cause.Error()
	if len(names) > 0 {
		msg = fmt.Sprintf("%s: %s. Create them, or remove the reference from spec.runtime",
			strings.Join(names, ", "), cause)
	}
	conds.set(plumev1alpha1.CondEnvSourceUnresolved, metav1.ConditionTrue, "Unresolved", msg)
	conds.set(plumev1alpha1.CondReady, metav1.ConditionFalse, "EnvSourceUnresolved", msg)
	status.Phase = plumev1alpha1.PhasePending
	status.Conditions = conds.merge(agent.Status.Conditions)
	status.ObservedGeneration = agent.Generation
	return r.writeStatus(ctx, agent, status)
}

func (r *AgentReconciler) ensureWorkload(ctx context.Context, agent *plumev1alpha1.Agent, rev, digest string,
	status *plumev1alpha1.AgentStatus) error {
	desired := r.deploymentFor(agent, rev)
	if desired.Annotations == nil {
		desired.Annotations = map[string]string{}
	}
	desired.Annotations[RevisionDigestAnnotation] = digest
	if err := ctrl.SetControllerReference(agent, desired, r.Scheme); err != nil {
		return fmt.Errorf("set owner on workload %s: %w", desired.Name, err)
	}

	var existing appsv1.Deployment
	err := r.Get(ctx, client.ObjectKeyFromObject(desired), &existing)
	switch {
	case apierrors.IsNotFound(err):
		err := r.Create(ctx, desired)
		if apierrors.IsAlreadyExists(err) {
			// Return, do NOT recurse. The client reads Deployments from an informer
			// cache that does not reflect this Create for some milliseconds, so
			// Get→NotFound, Create→AlreadyExists is the routine read-after-write
			// case — and recursing there grew stack depth and API Creates 1:1 with
			// cache lag, measured at 1500 frames for 1500 stale Gets. A stalled
			// watch would turn that into a fatal stack overflow of the only control
			// plane, plus a Create storm on the way down. The reconciler is
			// level-triggered; controller-runtime requeues with backoff.
			return fmt.Errorf("workload %s appeared between the read and the create; "+
				"requeueing to validate it: %w", desired.Name, err)
		}

		if err != nil {
			return fmt.Errorf("create workload %s: %w", desired.Name, err)
		}
		return nil
	case err != nil:
		return fmt.Errorf("get workload %s: %w", desired.Name, err)
	}

	// A workload that exists under this name but was rendered from a DIFFERENT
	// projection is a hash collision, not a drifted revision. Converging it is
	// the whole payload of a chosen 40-bit collision: the safe spec passes its
	// gate as revision H, the malicious spec computes the same H, and the block
	// below — which exists to correct out-of-band drift — rewrites H's pod
	// template to the malicious image while status still names the revision that
	// passed. Stop before that, and say why.
	//
	// An ABSENT annotation is not a mismatch: a workload created before this
	// field existed carries no digest, and treating unknown as collision would
	// wedge every upgraded cluster. It is adopted and stamped on the next
	// converge.
	// Corroboration — status was checked first, above. A workload carrying no
	// stamp is REFUSED, not adopted.
	//
	// An earlier version adopted it and stamped it from the current spec, on the
	// reasoning that a workload predating the field would otherwise wedge an
	// upgraded cluster. There is no such workload: plume is unreleased, so the
	// migration had nothing to migrate and was purely an attack surface —
	// stripping the annotation made the operator rewrite the pod template from
	// whatever spec was current and stamp the result as legitimate. Legacy
	// identity must never be reconstructed from current spec; that is the
	// collision payload with an extra step.
	// Refuse when nothing NON-FORGEABLE vouches for this name/digest pair, and
	// not merely when the annotation is missing.
	//
	// The wider rule was a regression: a principal with only deployments/patch
	// could strip the stamp AND rewrite the image, and the workload then stayed
	// terminally refused with the attacker's pods running — where before it
	// self-healed, because the template rewrite below corrects out-of-band drift.
	// The sequence r8 BLOCKER 3 describes additionally needs agents/update to
	// write a colliding spec, and collisionAgainstStatus above already refuses
	// that before this code runs. So the wider rule protected nothing and traded
	// a self-correcting drift for a permanent one.
	//
	// status is written only by this controller through the status subresource,
	// so consulting it is not "reconstructing identity from current spec".
	// The digest comparison here is EQUIVALENT to comparing names alone, and that
	// is worth stating because a mutation shows it is unpinned. A name that
	// matches with a DIFFERENT digest was already refused by
	// collisionAgainstStatus at the top of Reconcile, so by the time this runs a
	// matching name implies a matching digest — unless status held a name with no
	// digest, which TestNameAndDigestAreAlwaysWrittenTogether rules out. It is
	// written on the digest anyway so that every provenance test in this file
	// reads the same way and none has to be re-derived as safe.
	vouched := (status.ActiveRevision == rev && status.ActiveRevisionDigest == digest) ||
		(status.CandidateRevision == rev && status.CandidateRevisionDigest == digest)
	existingDigest, stamped := existing.Annotations[RevisionDigestAnnotation]
	switch {
	case stamped && existingDigest != digest:
		return &revisionCollisionError{name: existing.Name, existing: existingDigest, desired: digest}
	case !stamped && !vouched:
		return &revisionCollisionError{name: existing.Name, existing: "(no stamp)", desired: digest}
	}

	// Reconcile the WHOLE template, not just replicas. Two reasons, and the
	// second is the important one:
	//
	//  1. A12's policy-surface fields (resources, port) are "applied in place",
	//     and they reach the pod template — syncing only replicas made raising
	//     memory to stop OOM kills a silent no-op on a green agent.
	//  2. Out-of-band drift must be corrected. Without this, anyone with
	//     deployments/update could rewrite the image of a gated revision: the gate
	//     passed on X, the pods run Y, and the CR asserts X. That bypasses
	//     ADR-0006 entirely and makes the hardening below a create-time decoration
	//     rather than an invariant.
	if !templateEquivalent(&existing, desired) || existing.Annotations[RevisionDigestAnnotation] != digest {
		if existing.Annotations == nil {
			existing.Annotations = map[string]string{}
		}
		existing.Annotations[RevisionDigestAnnotation] = digest
		existing.Spec.Replicas = desired.Spec.Replicas
		existing.Spec.Template = desired.Spec.Template
		if err := r.Update(ctx, &existing); err != nil {
			return fmt.Errorf("converge workload %s: %w", existing.Name, err)
		}
	}
	return nil
}

// templateEquivalent reports whether a workload already matches what this
// revision and its current policy fields ask for.
//
// Two comparisons, because neither alone is correct. The API server defaults
// many PodSpec fields on write (terminationGracePeriodSeconds, dnsPolicy,
// scheduler name…), so an exact comparison of the whole template would never
// converge and the operator would rewrite the Deployment forever. But a
// derivative comparison IGNORES fields that are empty in `desired`, so clearing
// a value is invisible to it — dropping one key from a resource map leaves the
// stale entry, because every key that remains still matches, and an operator
// removing a request would get a green agent and no change.
//
// So: the container is fully owned by this operator and is compared EXACTLY,
// which sees removals; the surrounding PodSpec is compared derivatively, which
// tolerates server defaulting.
func templateEquivalent(existing, desired *appsv1.Deployment) bool {
	if existing.Spec.Replicas == nil || desired.Spec.Replicas == nil ||
		*existing.Spec.Replicas != *desired.Spec.Replicas {
		return false
	}
	return podSpecEquivalent(existing.Spec.Template.Spec, desired.Spec.Template.Spec)
}

// podSpecEquivalent compares the WHOLE pod spec exactly.
//
// It used to compare containers exactly and the surrounding PodSpec
// derivatively — which skipped every field unset in desired, and deploymentFor
// set none of them. An injected initContainer with an escalated
// serviceAccountName therefore survived reconciliation forever: arbitrary code
// before the agent starts, holding a token the same edit could grant. That is a
// more complete bypass than the image swap this drift correction was built for,
// and it hid behind container-level tests that all passed.
//
// The lesson generalizes past this function: apimachinery's derivative
// comparison skips zero values for Slice, String, Map and Ptr kinds, so ANY
// object compared that way has a hole shaped like the fields its renderer does
// not set. The fix is always the same — own the fields, then compare exactly.
func podSpecEquivalent(existing, desired corev1.PodSpec) bool {
	e, d := *existing.DeepCopy(), *desired.DeepCopy()
	// DeprecatedServiceAccount is mirrored from ServiceAccountName by admission
	// and cannot be set independently, so comparing it would never converge.
	e.DeprecatedServiceAccount, d.DeprecatedServiceAccount = "", ""
	return equality.Semantic.DeepEqual(e, d)
}

// deploymentFor renders one revision's workload. Design 02 §6: non-root,
// read-only rootfs, seccomp — applied to every agent, not only sandboxed ones,
// since the sandbox fallback path must be no weaker than the default path.
func (r *AgentReconciler) deploymentFor(agent *plumev1alpha1.Agent, rev string) *appsv1.Deployment {
	rt := agent.Spec.Runtime
	labels := map[string]string{
		LabelAgent:    agent.Name,
		LabelRevision: rev,
	}
	replicas := rt.Replicas
	if replicas == 0 {
		replicas = 1
	}
	// A sandboxed agent is a stateful singleton; admission rejects replicas>1 with
	// a sandbox, and this is the belt to that braces.
	if rt.Sandbox != nil {
		replicas = 1
	}

	yes, no := ptr(true), ptr(false)
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      WorkloadName(agent.Name, rev),
			Namespace: agent.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot:   yes,
						SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
					},
					// An agent has no reason to hold an API token, and turning this
					// off closes the exfil path an injected initContainer would use.
					AutomountServiceAccountToken: no,
					// Named explicitly so a tampered value is a difference rather than
					// a field the operator never had an opinion about. A real cluster's
					// ServiceAccount admission plugin defaults this to "default";
					// envtest does not, so leaving it unset would make the two
					// environments disagree about what "unchanged" means.
					ServiceAccountName: "default",
					// The four fields the API server defaults on a pod template. Set
					// here so read-back matches and the PodSpec can be compared
					// exactly; unset, each was a hole rather than a default.
					RestartPolicy:                 corev1.RestartPolicyAlways,
					TerminationGracePeriodSeconds: ptr(int64(30)),
					DNSPolicy:                     corev1.DNSClusterFirst,
					SchedulerName:                 corev1.DefaultSchedulerName,
					Containers: []corev1.Container{{
						Name:      "agent",
						Image:     rt.Image,
						Resources: rt.Resources,
						Env:       rt.Env,
						EnvFrom:   rt.EnvFrom,
						Ports: []corev1.ContainerPort{{
							Name:          "a2a",
							ContainerPort: port(rt),
							// Set explicitly rather than left to defaulting, so drift is visible.
							Protocol: corev1.ProtocolTCP,
						}},
						// These three were previously left unset and therefore exempted from
						// comparison, which made them unreverted drift rather than defaults.
						// The sharp one is the pull policy: `Never` tells the kubelet to use
						// whatever local image already carries this tag, so reverting the tag
						// corrects nothing, and nothing pulls so nothing is signature-checked
						// (ADR-0019). Always is deliberate — plume permits tags, and a tag
						// does not identify content.
						ImagePullPolicy: corev1.PullAlways,
						// The kubelet reads up to 4KB from this path into pod status, which
						// anyone with pod-get can read.
						TerminationMessagePath:   corev1.TerminationMessagePathDefault,
						TerminationMessagePolicy: corev1.TerminationMessageReadFile,
						SecurityContext: &corev1.SecurityContext{
							AllowPrivilegeEscalation: no,
							ReadOnlyRootFilesystem:   yes,
							RunAsNonRoot:             yes,
							Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
						},
					}},
				},
			},
		},
	}
}

// workloadAvailable reports whether a revision has at least one available
// replica. Availability, not readiness of a single pod: a Deployment reporting
// availableReplicas is the closest signal the operator has to "this revision can
// serve" before the card fetch of §3.4 exists.
func (r *AgentReconciler) workloadAvailable(ctx context.Context, agent *plumev1alpha1.Agent, rev, digest string) (bool, error) {
	var d appsv1.Deployment
	key := types.NamespacedName{Namespace: agent.Namespace, Name: WorkloadName(agent.Name, rev)}
	if err := r.Get(ctx, key, &d); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("get workload %s: %w", key, err)
	}
	// Availability is necessary and not sufficient. This result authorizes
	// PROMOTION, so it must also answer "available workload of WHICH revision?" —
	// a same-named object created by anyone with deployments/create is Available
	// too, and promoting on replicas alone made its status the operator's
	// evidence. The digest stamp is the operator's own mark.
	if d.Annotations[RevisionDigestAnnotation] != digest {
		return false, nil
	}
	return d.Status.AvailableReplicas > 0, nil
}

// gatesSatisfied reports whether this agent may take traffic. With no EvalSuite
// CRD installed the gate requirement does not apply (§3.3 core tier); with it
// installed, the gate controller does not exist yet, so nothing can pass and the
// agent holds — which is the fail-closed direction.
// gatesSatisfied reports whether this agent may take traffic.
//
// There is deliberately no separate branch for the unwired case: evalSuiteInstalled
// reports true when the hook is unset, which lands here as "assume the CRD is
// present", and an agent with declared gates then holds. A second nil-check
// would read as load-bearing while never changing an outcome — and defensive
// code no test can pin is worse than none, because it invites the next reader to
// trust it.
func (r *AgentReconciler) gatesSatisfied(agent *plumev1alpha1.Agent) bool {
	if !r.evalSuiteInstalled() {
		return true
	}
	return len(agent.Spec.Gates) == 0
}

// evalSuiteInstalled defaults to TRUE when unset, so an unwired reconciler holds
// rather than promoting ungated. See the field comment.
func (r *AgentReconciler) evalSuiteInstalled() bool {
	if r.EvalSuiteInstalled == nil {
		return true
	}
	return r.EvalSuiteInstalled()
}

func (r *AgentReconciler) assessGates(agent *plumev1alpha1.Agent, c *conditionSet) {
	switch {
	// The no-gates case comes FIRST, and deliberately. With nothing declared to
	// gate, detection cannot change the outcome — the agent promotes either way —
	// so reporting "holding rather than promoting ungated" on an agent that just
	// promoted would be loud and wrong, which is exactly what NFR-8 forbids.
	case len(agent.Spec.Gates) == 0:
		c.set(plumev1alpha1.CondGatesSkipped, metav1.ConditionTrue, "NoGatesDeclared",
			"no spec.gates are declared, so this rollout is not eval-gated")
	case r.EvalSuiteInstalled == nil:
		// Gates ARE declared and we cannot tell whether the CRD is present. Hold,
		// and say exactly that.
		c.set(plumev1alpha1.CondGatesPassed, metav1.ConditionFalse, "GateDetectionUnwired",
			"the operator was built without EvalSuite detection, so it cannot tell whether "+
				"this rollout should be eval-gated; holding rather than promoting ungated")
	case !r.evalSuiteInstalled():
		c.set(plumev1alpha1.CondGatesSkipped, metav1.ConditionTrue, "EvalSuiteCRDAbsent",
			"the EvalSuite CRD is not installed, so this rollout is NOT eval-gated (design 02 §3.3, core tier)")
	default:
		c.set(plumev1alpha1.CondGatesPassed, metav1.ConditionFalse, "GateControllerUnimplemented",
			"spec.gates are declared but the gate controller (design 16) is not implemented; "+
				"the revision holds at zero traffic rather than promoting ungated")
	}
}

// assessSandbox reports the §3.2 downgrade. The agent-sandbox CRD is not bound
// yet, so every sandboxed agent currently downgrades — stated loudly, per NFR-8,
// rather than silently running an unsandboxed pod.
// assessEnvSourceProtection is NFR-8 applied to a degradation this operator
// currently HAS, rather than to one it might have.
//
// Design 02 §3.3 says the revision identity covers the resolved CONTENT of every
// env source (A20), and A35/A42 say a revision reads its own immutable copy in
// an operator-owned namespace. None of that is built: internal/revision hashes
// the REFERENT, and workloads are created in the Agent's own namespace. So an
// editor changes a referenced ConfigMap from a safe system prompt to an injected
// one, a Pod is replaced, and the new content serves under the old revision's
// gate result — with no permission to touch the Agent.
//
// A43 wrote that down in the design. A design paragraph is not what NFR-8 asks
// for: "any downgraded guarantee surfaces as a CR condition, never silently."
// Until A20+A35+A42 land, an Agent that actually references an env source says
// so on the object.
func (r *AgentReconciler) assessEnvSourceProtection(agent *plumev1alpha1.Agent, c *conditionSet) {
	rt := agent.Spec.Runtime
	if rt == nil {
		return
	}
	// Every arm is reported, and an arm this switch does not NAME still counts.
	// A named-arm enumeration that silently skips the rest is the exact defect
	// internal/revision just replaced with a whole-selector marshal: corev1 gains
	// arms between releases, and the one it gained last (fileKeyRef) is already
	// classified as behaviour by the projection while being invisible here.
	var refs []string
	for _, f := range rt.EnvFrom {
		switch {
		case f.ConfigMapRef != nil:
			refs = append(refs, "envFrom configMapRef/"+f.ConfigMapRef.Name)
		case f.SecretRef != nil:
			refs = append(refs, "envFrom secretRef/"+f.SecretRef.Name)
		default:
			refs = append(refs, "envFrom (unrecognised source)")
		}
	}
	for _, e := range rt.Env {
		if e.ValueFrom == nil {
			continue
		}
		switch v := e.ValueFrom; {
		case v.ConfigMapKeyRef != nil:
			refs = append(refs, "env."+e.Name+" configMapKeyRef/"+v.ConfigMapKeyRef.Name)
		case v.SecretKeyRef != nil:
			refs = append(refs, "env."+e.Name+" secretKeyRef/"+v.SecretKeyRef.Name)
		case v.FieldRef != nil, v.ResourceFieldRef != nil:
			// Downward API: the value comes from the Pod, which the operator owns.
			// Not an ungated external input, so not reported.
		default:
			refs = append(refs, "env."+e.Name+" (unrecognised source)")
		}
	}
	if len(refs) == 0 {
		// No env source, no exposure. Saying nothing here is correct: an abnormal-
		// true condition on an Agent that cannot be affected is the noise operators
		// learn to filter, and then miss the one that matters.
		return
	}
	// The message is BUDGETED. Kubernetes caps a condition message at 32768
	// bytes and rejects the whole status write past it — so an Agent with enough
	// env sources got no status at all: no Ready, no Degraded, nothing, in an
	// error loop. The condition added to satisfy NFR-8 would have been the thing
	// that silenced the object. 150 sources with long names was enough.
	//
	// refs keeps SPEC ORDER, which is the order the operator wrote and the order
	// they will look for. It was sorted for a "stable message" that spec order
	// already provided.
	const budget = 12
	listed, more := refs, 0
	if len(listed) > budget {
		listed, more = listed[:budget], len(refs)-budget
	}
	shown := strings.Join(listed, ", ")
	if more > 0 {
		shown = fmt.Sprintf("%s, and %d more", shown, more)
	}
	// A20 is implemented, so a source edit now MINTS a candidate and is gated
	// before promotion. What remains is A35 and A42: the retained workload still
	// resolves the user's object by name, so replacing a Pod can serve changed
	// bytes under a revision that already passed. Narrower than the gap this
	// condition first announced, and still a real one (design 02 A46).
	c.set(plumev1alpha1.CondEnvSourceProtectionUnavailable, metav1.ConditionTrue, "SourcesNotIsolated",
		fmt.Sprintf("%d env source(s) are hashed by content and gated on change (A20), but the "+
			"running workload still reads them BY NAME: %s. Replacing a Pod after an edit serves the "+
			"new content under this revision's existing gate result. Restrict update on those objects "+
			"until A35 (revision-scoped copies) and A42 (the run namespace) are implemented",
			len(refs), shown))
}

func (r *AgentReconciler) assessSandbox(agent *plumev1alpha1.Agent, c *conditionSet) {
	if agent.Spec.Runtime == nil || agent.Spec.Runtime.Sandbox == nil {
		return
	}
	c.set(plumev1alpha1.CondSandboxDowngraded, metav1.ConditionTrue, "SandboxRuntimeUnavailable",
		fmt.Sprintf("spec.runtime.sandbox.profile=%q was requested, but no sandbox runtime is bound; "+
			"running a hardened Deployment instead (non-root, read-only rootfs, seccomp, no capabilities)",
			agent.Spec.Runtime.Sandbox.Profile))
}

// assessTaskState implements §3.2's detected-not-assumed rule. The A2A card is
// the declaration point, and card fetch is unimplemented, so replicas>1 cannot
// currently be verified and the condition names that rather than assuming.
func (r *AgentReconciler) assessTaskState(agent *plumev1alpha1.Agent, c *conditionSet) {
	if agent.Spec.Runtime == nil || agent.Spec.Runtime.Replicas <= 1 {
		return
	}
	c.set(plumev1alpha1.CondTaskStateUnverified, metav1.ConditionTrue, "CardNotFetched",
		fmt.Sprintf("replicas=%d requires the A2A card to assert shared task state, but card fetch "+
			"(§3.4) is not implemented; concurrent replicas may lose task state",
			agent.Spec.Runtime.Replicas))
}

// collectGarbage deletes revisions beyond the retention window. The window is N
// revisions IN ADDITION TO the active one and any in-flight candidate, so a
// rollout can never GC its own rollback target.
func (r *AgentReconciler) collectGarbage(
	ctx context.Context, agent *plumev1alpha1.Agent,
	owned []appsv1.Deployment, status *plumev1alpha1.AgentStatus,
) error {
	protected := map[string]bool{}
	if status.ActiveRevision != "" {
		protected[status.ActiveRevision] = true
	}
	if status.CandidateRevision != "" {
		protected[status.CandidateRevision] = true
	}

	var retainable []appsv1.Deployment
	for _, d := range owned {
		if !protected[d.Labels[LabelRevision]] {
			retainable = append(retainable, d)
		}
	}
	// Newest first, so the oldest fall outside the window.
	sort.Slice(retainable, func(i, j int) bool {
		// Newest first. Kubernetes timestamps have one-second granularity, so
		// revisions created in the same second need a deterministic tie-break —
		// otherwise which revision survives GC depends on list order.
		if !retainable[i].CreationTimestamp.Equal(&retainable[j].CreationTimestamp) {
			return retainable[j].CreationTimestamp.Before(&retainable[i].CreationTimestamp)
		}
		return retainable[i].Name > retainable[j].Name
	})

	limit := DefaultRevisionHistoryLimit
	for i := limit; i < len(retainable); i++ {
		d := retainable[i]
		if err := r.Delete(ctx, &d); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("gc workload %s: %w", d.Name, err)
		}
	}
	return nil
}

// ownedWorkloads returns the Deployments this Agent actually controls.
//
// Selecting on the label alone was a defect with teeth: a stray plume.dev/agent
// label — copied from an example, applied by a Kustomize commonLabels, or set by
// anyone with deployment-create in the namespace — made this operator a deleter
// of other people's workloads. Ownership is decided by the controller reference
// and its UID, which nobody can forge by labelling.
func (r *AgentReconciler) ownedWorkloads(ctx context.Context, agent *plumev1alpha1.Agent) ([]appsv1.Deployment, error) {
	var list appsv1.DeploymentList
	if err := r.List(ctx, &list,
		client.InNamespace(agent.Namespace),
		client.MatchingLabels{LabelAgent: agent.Name},
	); err != nil {
		return nil, fmt.Errorf("list workloads for %s/%s: %w", agent.Namespace, agent.Name, err)
	}

	owned := make([]appsv1.Deployment, 0, len(list.Items))
	for _, d := range list.Items {
		ref := metav1.GetControllerOf(&d)
		if ref == nil || ref.UID != agent.UID {
			continue
		}
		// A workload with no revision label cannot be placed in the retention
		// window, so it must never be a GC candidate.
		if d.Labels[LabelRevision] == "" {
			continue
		}
		owned = append(owned, d)
	}
	return owned, nil
}

// finalize runs §3.7's ordered teardown. Only the steps whose components exist
// are implemented; the rest are named here so that adding them is an edit to a
// stated sequence rather than a rediscovery of it.
func (r *AgentReconciler) finalize(ctx context.Context, agent *plumev1alpha1.Agent) (ctrl.Result, error) {
	// TODO(design 03/06/05): drain traffic to weight 0 respecting taskTimeout,
	// revoke gateway routes in reverse apply order, deactivate (never delete) the
	// agent-actor OAuth client, GC directory entries. Workloads go with the
	// ownerRef; nothing else is wired yet, so there is nothing to unwind.
	if !containsString(agent.Finalizers, Finalizer) {
		return ctrl.Result{}, nil
	}
	agent.Finalizers = removeString(agent.Finalizers, Finalizer)
	if err := r.Update(ctx, agent); err != nil {
		return ctrl.Result{}, fmt.Errorf("release finalizer on %s/%s: %w", agent.Namespace, agent.Name, err)
	}
	return ctrl.Result{}, nil
}

func (r *AgentReconciler) writeStatus(ctx context.Context, agent *plumev1alpha1.Agent, status *plumev1alpha1.AgentStatus) error {
	if equalStatus(&agent.Status, status) {
		return nil // no-op writes churn the API server and fight other controllers
	}
	agent.Status = *status
	if err := r.Status().Update(ctx, agent); err != nil {
		return fmt.Errorf("update status of %s/%s: %w", agent.Namespace, agent.Name, err)
	}
	return nil
}

// WorkloadName is the Deployment name for one revision of one agent. Agent names
// are capped at 52 characters by CEL so that this always fits a DNS-1123 label.
func WorkloadName(agent, rev string) string { return agent + "-" + rev }

func (r *AgentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&plumev1alpha1.Agent{}).
		Owns(&appsv1.Deployment{}).
		Complete(r)
}

func ptr[T any](v T) *T { return &v }

func port(rt *plumev1alpha1.AgentRuntime) int32 {
	if rt.Port == 0 {
		return 8080
	}
	return rt.Port
}

func containsString(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func removeString(xs []string, s string) []string {
	out := xs[:0]
	for _, x := range xs {
		if x != s {
			out = append(out, x)
		}
	}
	return out
}

// appendSuperseded records an abandoned candidate, deduplicated and trimmed to
// the CRD's MaxItems.
//
// The cap is not free. Design 02 §3.3 promises the abandoned revision is
// "appended to status.supersededCandidates AND emitted as an event" — status
// being the convenience and the event the durable record. No EventRecorder is
// wired yet, so past the cap the record is GONE rather than relocated. Stated
// plainly rather than justified by a mechanism that does not exist; wiring the
// recorder is what makes the cap harmless.
func appendSuperseded(list []string, rev string) []string {
	if rev == "" || containsString(list, rev) {
		return list
	}
	list = append(list, rev)
	if n := len(list); n > maxSupersededCandidates {
		list = list[n-maxSupersededCandidates:]
	}
	return list
}
