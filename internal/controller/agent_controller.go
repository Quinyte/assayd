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
	"context"
	"fmt"
	"sort"

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

	plumev1alpha1 "github.com/ejs-5/plume/api/v1alpha1"
	"github.com/ejs-5/plume/internal/revision"
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

	desired := revision.Hash(agent.Spec)
	logger = logger.WithValues("revision", desired)

	owned, err := r.ownedWorkloads(ctx, &agent)
	if err != nil {
		return ctrl.Result{}, err
	}

	status := agent.Status.DeepCopy()

	// A new generation supersedes an in-flight candidate (§3.3). At most one
	// candidate is ever in flight; the abandoned one is recorded so the
	// transition is auditable rather than silent.
	if status.CandidateRevision != "" && status.CandidateRevision != desired {
		if !containsString(status.SupersededCandidates, status.CandidateRevision) {
			status.SupersededCandidates = append(status.SupersededCandidates, status.CandidateRevision)
			// Keep the newest within the CRD's cap; the durable audit trail is
			// events and receipts, not this list.
			if n := len(status.SupersededCandidates); n > maxSupersededCandidates {
				status.SupersededCandidates = status.SupersededCandidates[n-maxSupersededCandidates:]
			}
		}
		logger.Info("superseding in-flight candidate",
			"superseded", status.CandidateRevision, "candidate", desired)
		status.CandidateRevision = ""
	}

	if err := r.ensureWorkload(ctx, &agent, desired); err != nil {
		return ctrl.Result{}, err
	}

	ready, err := r.workloadAvailable(ctx, &agent, desired)
	if err != nil {
		return ctrl.Result{}, err
	}

	conds := newConditionSet(agent.Generation)
	r.assessGates(&agent, conds)
	r.assessSandbox(&agent, conds)
	r.assessTaskState(&agent, conds)

	switch {
	case !ready && status.ActiveRevision != "" && status.ActiveRevision != desired:
		// A genuine rollout: an earlier revision still holds all traffic and is
		// healthy while its successor comes up. Reporting Ready=False here would
		// trip every alert keyed on the canonical condition on any routine spec
		// edit; reporting Canary would claim a weighted shift that §3.3 defines
		// and that no gateway is performing yet (A13).
		status.CandidateRevision = desired
		status.Phase = plumev1alpha1.PhaseReady
		conds.set(plumev1alpha1.CondReady, metav1.ConditionTrue, "Available",
			fmt.Sprintf("revision %s is serving", status.ActiveRevision))
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
		status.CandidateRevision = desired
		conds.set(plumev1alpha1.CondProgressing, metav1.ConditionFalse, "NoRolloutInFlight",
			"no other revision is coming up")
		if status.ActiveRevision == desired {
			// It was serving and is not any more. Say so; do not silently keep the
			// promotion from a healthier moment.
			status.Phase = plumev1alpha1.PhaseDegraded
			status.CandidateRevision = ""
			conds.set(plumev1alpha1.CondReady, metav1.ConditionFalse, "WorkloadUnavailable",
				fmt.Sprintf("revision %s is the active revision but has no available replicas", desired))
			break
		}
		status.Phase = plumev1alpha1.PhasePending
		conds.set(plumev1alpha1.CondReady, metav1.ConditionFalse, "WorkloadNotAvailable",
			fmt.Sprintf("revision %s has no available replicas yet", desired))

	case !r.gatesSatisfied(&agent):
		// Gates are required and have not passed: hold the candidate at zero
		// traffic (§3.3). Whether the AGENT is ready is a separate question from
		// whether the CANDIDATE may promote — if an active revision is still
		// serving, saying Ready=False would page the on-call for adding gates to a
		// healthy agent, which is the same defect A13 fixed in the branch below.
		status.Phase = plumev1alpha1.PhaseHeld
		status.CandidateRevision = desired
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
		if status.ActiveRevision != desired {
			logger.Info("promoting revision", "from", status.ActiveRevision, "to", desired)
		}
		status.ActiveRevision = desired
		status.CandidateRevision = ""
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
func (r *AgentReconciler) ensureWorkload(ctx context.Context, agent *plumev1alpha1.Agent, rev string) error {
	desired := r.deploymentFor(agent, rev)
	if err := ctrl.SetControllerReference(agent, desired, r.Scheme); err != nil {
		return fmt.Errorf("set owner on workload %s: %w", desired.Name, err)
	}

	var existing appsv1.Deployment
	err := r.Get(ctx, client.ObjectKeyFromObject(desired), &existing)
	switch {
	case apierrors.IsNotFound(err):
		if err := r.Create(ctx, desired); err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("create workload %s: %w", desired.Name, err)
		}
		return nil
	case err != nil:
		return fmt.Errorf("get workload %s: %w", desired.Name, err)
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
	if !templateEquivalent(&existing, desired) {
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
	if !containersEquivalent(existing.Spec.Template.Spec.Containers, desired.Spec.Template.Spec.Containers) {
		return false
	}
	return equality.Semantic.DeepDerivative(desired.Spec.Template, existing.Spec.Template)
}

// containersEquivalent compares the container fields this operator sets, exactly.
// It deliberately does not compare the whole Container: the API server defaults
// terminationMessagePath, imagePullPolicy and protocol, none of which plume owns.
func containersEquivalent(existing, desired []corev1.Container) bool {
	if len(existing) != len(desired) {
		return false
	}
	for i := range desired {
		e, d := existing[i], desired[i]
		switch {
		case e.Name != d.Name,
			e.Image != d.Image,
			!equality.Semantic.DeepEqual(e.Resources, d.Resources),
			!equality.Semantic.DeepEqual(e.Env, d.Env),
			!equality.Semantic.DeepEqual(e.EnvFrom, d.EnvFrom),
			!equality.Semantic.DeepEqual(e.SecurityContext, d.SecurityContext),
			!portsEquivalent(e.Ports, d.Ports):
			return false
		}
	}
	return true
}

// portsEquivalent compares only name and number: the API server defaults
// protocol to TCP, which plume never sets.
func portsEquivalent(existing, desired []corev1.ContainerPort) bool {
	if len(existing) != len(desired) {
		return false
	}
	for i := range desired {
		if existing[i].Name != desired[i].Name || existing[i].ContainerPort != desired[i].ContainerPort {
			return false
		}
	}
	return true
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
					Containers: []corev1.Container{{
						Name:      "agent",
						Image:     rt.Image,
						Resources: rt.Resources,
						Env:       rt.Env,
						EnvFrom:   rt.EnvFrom,
						Ports:     []corev1.ContainerPort{{Name: "a2a", ContainerPort: port(rt)}},
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
func (r *AgentReconciler) workloadAvailable(ctx context.Context, agent *plumev1alpha1.Agent, rev string) (bool, error) {
	var d appsv1.Deployment
	key := types.NamespacedName{Namespace: agent.Namespace, Name: WorkloadName(agent.Name, rev)}
	if err := r.Get(ctx, key, &d); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("get workload %s: %w", key, err)
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
