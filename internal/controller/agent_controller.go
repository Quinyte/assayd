// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

// Package controller holds the agent-operator's reconcilers.
//
// Design 02 §4 states the loop; this file implements the slice that needs no
// other assayd component: observe → compute the desired revision → materialize
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
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/compiler"
	"github.com/Quinyte/assayd/internal/revision"
)

const (
	// LabelAgent and LabelRevision let the operator find the workloads it owns
	// without trusting names, and let a human find them with kubectl.
	LabelAgent    = compiler.LabelAgent
	LabelRevision = "assayd.dev/revision"

	// Finalizer gates the ordered teardown of §3.7. The teardown steps that need
	// the gateway and directory are not implemented; the finalizer is installed
	// now so that agents created today do not need a migration to acquire it.
	Finalizer = "assayd.dev/agent-teardown"

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

	// OperatorNamespace is where the binding records live (design 02 A60) and
	// whose UID is the install identity stamped on run namespaces. Required.
	OperatorNamespace string
	// Reader is the UNCACHED reader the run-namespace protocol's live lists
	// use. In envtest the client itself is uncached and this may be nil.
	Reader client.Reader
	// LabelAuthorityPresent reports whether design 07 A5.9's admission
	// policies exist. The operator fail-closes without them. Required; see
	// LabelAuthorityPresent for the production adapter.
	LabelAuthorityPresent func(context.Context) (bool, error)
	// CardFetchTimeout bounds one card fetch. Zero means
	// DefaultCardFetchTimeout, which is sized for a cold in-cluster lookup;
	// envtest sets it short because there is no cluster network there to wait for.
	CardFetchTimeout time.Duration
	// CardClient fetches the agent card (§3.4). Injected so a test can supply a
	// transport; nil means a plain client.
	CardClient interface {
		Do(*http.Request) (*http.Response, error)
	}
	// InjectedEnv is what the operator knows and an Agent does not: the cluster's
	// gateway address, and in time the KG and task-store coordinates. Operator
	// configuration, so one cluster has one answer (A65).
	InjectedEnv InjectedEnvConfig
	// Gateway is `gateway.enabled` and the Gateway a serving route attaches to
	// (design 03 §3.1: declared, never discovered). Zero value — disabled — is
	// what P1 ships, and NewAgentReconciler refuses an enabled one that names no
	// Gateway.
	Gateway GatewayConfig
	// AuthProbe sends `Create`'s anonymous probe (design 03 §3.3.3). Nil means
	// the HTTP probe to Gateway.ServingURL; envtest injects §8.1's oracle.
	AuthProbe AuthProber
	// AuthDeadline is the `-auth` transaction's deadline. Zero means
	// AuthTransactionDeadline, the design's constant; only tests set it.
	AuthDeadline time.Duration

	installMu  sync.Mutex
	installUID string
	// runNamespaceLocks serializes the writers of one binding record within
	// this process; see lockRunNamespace.
	runNamespaceLocks sync.Map
}

// NewAgentReconciler builds a reconciler with its dependencies stated, so that
// wiring a manager cannot silently decide ADR-0006's fate by omission.
func NewAgentReconciler(c client.Client, reader client.Reader, scheme *runtime.Scheme,
	operatorNamespace string, evalSuiteInstalled func() bool,
	labelAuthority func(context.Context) (bool, error), injected InjectedEnvConfig,
	gateway GatewayConfig) (*AgentReconciler, error) {
	switch {
	case c == nil:
		return nil, fmt.Errorf("agent reconciler: client is required")
	case reader == nil:
		return nil, fmt.Errorf("agent reconciler: an uncached reader is required — the run-namespace " +
			"teardown protocol (design 02 A60) lists live, and a cached list is what it exists to avoid")
	case scheme == nil:
		return nil, fmt.Errorf("agent reconciler: scheme is required")
	case operatorNamespace == "":
		return nil, fmt.Errorf("agent reconciler: operatorNamespace is required — the run-namespace " +
			"binding records live there (design 02 A60) and there is no safe default")
	case evalSuiteInstalled == nil:
		return nil, fmt.Errorf("agent reconciler: evalSuiteInstalled is required — " +
			"whether the EvalSuite CRD is present decides whether rollouts are eval-gated " +
			"(ADR-0006), and it must be an explicit decision rather than a zero value")
	case labelAuthority == nil:
		return nil, fmt.Errorf("agent reconciler: labelAuthority is required — without design 07 A5.9's " +
			"admission policies the assayd.dev namespace labels are forgeable, and whether they are " +
			"installed must be checked rather than assumed")
	case gateway.Enabled && gateway.Name == "":
		return nil, fmt.Errorf("agent reconciler: --gateway-enabled is set and --gateway-name is empty; " +
			"a route's parentRef must name the Gateway it attaches to, and design 07 A5.9's admission " +
			"policy reserves authorship by comparing that very name")
	case gateway.Enabled && gateway.Namespace == "":
		return nil, fmt.Errorf("agent reconciler: --gateway-enabled is set and --gateway-namespace is " +
			"empty. It is NOT the operator's namespace: design 07 A6.2 measured the route reservation " +
			"failing open — silently, in the permissive direction — when the two were conflated, so " +
			"there is no safe default to infer here")
	case gateway.Enabled && gateway.HostnameSuffix == "":
		return nil, fmt.Errorf("agent reconciler: --gateway-enabled is set and --gateway-hostname-suffix " +
			"is empty; a route with no hostname matches every request on the listener, so every Agent " +
			"would answer for every other")
	}
	// Required whenever the compiler runs (design 03 §3.3.3), and it runs
	// whenever the gateway is enabled: every new Agent's route is published only
	// after a probe sent here gets 401, so an unset flag would hold every new
	// Agent unpublished. The form is checked whenever it is set.
	if gateway.Enabled && gateway.ServingURL == "" {
		return nil, fmt.Errorf("agent reconciler: --gateway-enabled is set and --gateway-serving-url " +
			"is empty. The policy compiler runs whenever the gateway is enabled, and it publishes a " +
			"new Agent's route only after an anonymous request to the Gateway's serving listener " +
			"gets 401 (design 03 §3.3.3), so without the listener's address no new Agent could " +
			"ever be published")
	}
	if gateway.ServingURL != "" {
		if err := ValidateGatewayServingURL(gateway.ServingURL); err != nil {
			return nil, fmt.Errorf("agent reconciler: --gateway-serving-url: %w", err)
		}
	}
	return &AgentReconciler{Client: c, Reader: reader, Scheme: scheme, OperatorNamespace: operatorNamespace,
		EvalSuiteInstalled: evalSuiteInstalled, LabelAuthorityPresent: labelAuthority,
		InjectedEnv: injected, Gateway: gateway}, nil
}

// installIdentity is the operator namespace's UID, stamped on run namespaces
// as assayd.dev/owned-by. Observability only, never evidence.
func (r *AgentReconciler) installIdentity(ctx context.Context) (string, error) {
	r.installMu.Lock()
	defer r.installMu.Unlock()
	if r.installUID != "" {
		return r.installUID, nil
	}
	var ns corev1.Namespace
	if err := r.Get(ctx, types.NamespacedName{Name: r.OperatorNamespace}, &ns); err != nil {
		return "", fmt.Errorf("read the operator's namespace %s: %w", r.OperatorNamespace, err)
	}
	r.installUID = string(ns.UID)
	return r.installUID, nil
}

// +kubebuilder:rbac:groups=assayd.dev,resources=agents,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=assayd.dev,resources=agents/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=assayd.dev,resources=agents/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// HTTPRoutes, in the run namespace, attaching to the assayd Gateway.
//
// Design 07 A5.7's RBAC table lists none of the gateway kinds and says so —
// "the ClusterRole today grants none of these. They land with the A42
// implementation" — and A42 has landed. Measured on a live cluster before this
// marker existed: the admission policy correctly PERMITTED the operator to
// author a route (design 07 A5.9 names its ServiceAccount) and the API server
// then refused it for want of RBAC. The two halves of the design had never both
// been implemented, so neither half was wrong and the path did not work.
//
// Only `httproutes`, and only the verbs the emitter calls: get, list and watch
// through the cache, create, update, delete. A verb granted ahead of the code
// that calls it is standing privilege with no consumer, and the same rule
// removed `patch` and `httproutes/status: get` here (design 07 A6.11), which
// were granted with A6 and never called: route status is not read.
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=httproutes,verbs=get;list;watch;create;update;delete
// The per-Agent `<agent>-auth` AgentgatewayPolicy (design 03 §1.1), by the same
// rule: listed and watched through the cache, read live by the transaction,
// the finalizer and the NACK mapping (`get`: the client does not cache
// unstructured objects), written by the `Create` transaction (`create`,
// `update`), and deleted by the finalizer after the route. Never `patch`: the
// compiler writes by create/update over owned fields, not server-side apply
// (design 03 §3.2). No AgentgatewayBackend grant at all: nothing reads or
// writes one.
//
// The NACK Events' `list` and `watch` are NOT a marker. A marker grants them in
// every namespace, and the watch is in the Gateway's alone, so the chart
// renders them as a Role there (charts/assayd/templates/rbac.yaml).
// +kubebuilder:rbac:groups=agentgateway.dev,resources=agentgatewaypolicies,verbs=get;list;watch;create;update;delete

// Services are per revision and share the workload's name shape; the operator
// creates one with each revision and collects it when that revision leaves the
// retained set, so it needs delete as well as create.
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// Leader election. Without these the elector retries a forbidden lease forever:
// the pod runs, reports Ready, and reconciles nothing — the silence NFR-8 forbids.
// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=get;list;watch;create;update;patch
// Events are the durable record design 02 §3.3 promises for a superseded
// candidate; status is the convenience and is capped.
// A20: the revision identity covers the resolved CONTENT of every referenced
// env source, so the operator must read them. `get` only — it never writes a
// user's ConfigMap or Secret, and the copies A35 will create live under names
// this operator owns.
// `create` and `delete` are for A35's revision-scoped copies and A56's sweep,
// NOT for the user's objects — the operator never writes a source. The marker
// cannot express that distinction, so §6 states it and the material code is the
// only caller: everything it creates or deletes is named
// `<agent>-<revision>-env-<n>` and verified before deletion (A57).
// `update` is for the binding record's compare-and-swap (A60) and for A57's
// metadata repair, which called Update without the grant — Forbidden on a real
// cluster, green in envtest, which runs as admin (design 07 A5.7).
// +kubebuilder:rbac:groups="",resources=configmaps;secrets,verbs=get;list;watch;create;update;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// A42/A60: the operator creates, labels and — when the last Agent of a source
// namespace goes — deletes run namespaces. RBAC cannot bound a dynamic name, so
// every write verb here is cluster-wide; the code writes only to a namespace
// whose name has the assayd-run- shape AND whose UID the binding record
// vouches for (design 07 A5.7). A real escalation, named rather than buried.
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch;create;update;delete
// ResourceQuota and LimitRange are mirrored from the source namespace into the
// run namespace (A60); mirrors are named assayd-mirror-<name> and only those are
// ever written or deleted.
// +kubebuilder:rbac:groups="",resources=resourcequotas;limitranges,verbs=get;list;watch;create;update;delete
// The operator checks that design 07 A5.9's label-reserving policies exist
// before creating a run namespace, and fail-closes if they do not.
// +kubebuilder:rbac:groups=admissionregistration.k8s.io,resources=validatingadmissionpolicies;validatingadmissionpolicybindings,verbs=get
// Discovery of whether the EvalSuite CRD is installed decides whether rollouts
// are eval-gated (ADR-0006), so the operator must be able to see CRDs.
// +kubebuilder:rbac:groups=apiextensions.k8s.io,resources=customresourcedefinitions,verbs=get;list;watch

func (r *AgentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var agent assaydv1alpha1.Agent
	if err := r.Get(ctx, req.NamespacedName, &agent); err != nil {
		// A deleted Agent is not an error. Its workloads and material do NOT go
		// with it by ownerReference — they live in the run namespace (A42) and a
		// cross-namespace owner is treated as absent — so the finalizer below is
		// the only thing that collects them, and it has run by the time the
		// object is gone.
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !agent.DeletionTimestamp.IsZero() {
		return r.finalize(ctx, &agent)
	}

	if !containsString(agent.Finalizers, Finalizer) {
		// A merge patch on metadata.finalizers, never a typed Update. A typed
		// round-trip adds `card: {}` and `runtime.resources: {}` to a spec written
		// without them, so spec changes, CRD validation ratcheting stops exempting
		// it, and a rule the stored object predates refuses the write: an Agent
		// stored with spec.budget before ADR-0034 B2 could never get its
		// finalizer (design 03 §3.1). The optimistic lock keeps the conflict
		// check Update had, so replacing the list cannot drop another writer's
		// finalizer.
		base := agent.DeepCopy()
		agent.Finalizers = append(agent.Finalizers, Finalizer)
		if err := r.Patch(ctx, &agent, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})); err != nil {
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
	resolved, buffers, unresolved, err := r.resolveEnvSources(ctx, &agent)
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

	status := agent.Status.DeepCopy()
	conds := newConditionSet(agent.Generation)

	// The assessors run BEFORE anything can return early. A pass that exits
	// without them merges a condition set that never saw them, and because owned
	// conditions are non-sticky, merge() then CLEARS every one — so entering the
	// terminal collision state below used to retract SandboxDowngraded,
	// GatesSkipped and SandboxDowngraded on an agent that is more
	// degraded, not less.
	r.assessGates(&agent, conds)
	r.assessSandbox(&agent, conds)
	r.assessTaskState(&agent, status, conds)
	r.assessGovernance(conds, status)

	// A42/A60: everything below goes into the operator-owned run namespace, and
	// the operator must be able to prove it created that namespace before it
	// writes a single copy into it.
	runNS, err := r.ensureRunNamespace(ctx, &agent)
	if err != nil {
		rerr := (*runNamespaceError)(nil)
		if !errors.As(err, &rerr) {
			return ctrl.Result{}, err
		}
		conds.set(assaydv1alpha1.CondRunNamespaceUnavailable, metav1.ConditionTrue, rerr.reason, rerr.message)
		conds.set(assaydv1alpha1.CondReady, metav1.ConditionFalse, "RunNamespaceUnavailable", rerr.message)
		if rerr.terminal {
			conds.set(assaydv1alpha1.CondDegraded, metav1.ConditionTrue, "RunNamespaceUnavailable", rerr.message)
			status.Phase = assaydv1alpha1.PhaseDegraded
		} else {
			status.Phase = assaydv1alpha1.PhasePending
		}
		status.Conditions = conds.merge(agent.Status.Conditions)
		status.ObservedGeneration = agent.Generation
		if err := r.writeStatus(ctx, &agent, status); err != nil {
			return ctrl.Result{}, err
		}
		if rerr.terminal {
			return ctrl.Result{}, nil
		}
		// Terminating clears by itself; nothing watches the binding record, so
		// poll rather than wait for an event that never comes.
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	owned, err := r.ownedWorkloads(ctx, &agent, runNS)
	if err != nil {
		return ctrl.Result{}, err
	}

	// A rollback request overrides which revision serves, and it does so by
	// SELECTING a retained one rather than recomputing from spec (ADR-0031).
	// Placed after the run namespace exists — the retained workloads live there —
	// and before the collision guard, so the pinned revision is what everything
	// downstream is checked against.
	pin, perr := r.resolveReleasePin(ctx, &agent, runNS)
	if perr != nil {
		if u := (*unresolvablePinError)(nil); errors.As(perr, &u) {
			return ctrl.Result{}, r.reportUnresolvablePin(ctx, &agent, status, conds, u)
		}
		return ctrl.Result{}, perr
	}
	if pin != nil {
		// The pinned revision's material was written when it was minted and is
		// immutable, so nothing is re-resolved here. That is the property under
		// source drift: re-resolving would read the CHANGED ConfigMap and either
		// collide with the immutable copy or serve bytes the revision was never
		// evaluated with.
		desired, desiredDigest = pin.Revision, pin.Digest
		logger = logger.WithValues("revision", desired, "pinned", true)
	}

	// STATUS is the authority on which revision a name belongs to, and it is
	// checked before the workload is touched.
	//
	// The first version of this guard read a assayd.dev/revision-digest annotation
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

	// A35: create the revision's own immutable copies BEFORE the workload, and
	// point the workload at them. Order matters — publishing a workload that
	// reads the user's object, even briefly, is the window this closes.
	// A pinned revision's material is RESOLVED, never rewritten: ensureRevision-
	// Material writes the buffers read from the user's current objects, which
	// after a drift are not the bytes this revision was evaluated with, and the
	// immutable copy correctly refuses them (ADR-0031).
	var material map[revision.SourceRef]string
	var merr error
	if pin != nil {
		// Verified, not rewritten and not re-pointed: the pinned Deployment already
		// references its own copies and is not re-rendered, so this only refuses a
		// revision whose material is gone or whose spec has changed shape.
		merr = r.verifyRetainedMaterial(ctx, &agent, runNS, desired, desiredDigest)
		if u := (*unresolvablePinError)(nil); merr != nil && errors.As(merr, &u) {
			return ctrl.Result{}, r.reportUnresolvablePin(ctx, &agent, status, conds, u)
		}
	} else {
		material, merr = r.ensureRevisionMaterial(ctx, &agent, runNS, desired, desiredDigest, buffers)
	}
	if merr != nil {
		// EVERY error here reaches the object, not only the typed one. The
		// earlier version returned a bare error for a Forbidden create, an
		// unavailable API server or ordinary cache lag — so an operator missing
		// RBAC to create material produced an Agent with no phase, no conditions
		// and a silent backoff loop. That is the NFR-8 failure this same file
		// fixes twenty lines below for IsInvalid, and it is what made a
		// completely broken feature undiagnosable.
		if isNamespaceTerminating(merr) {
			// The run namespace went Terminating between ensureRunNamespace and
			// this create. That is a wait, not an error to retry against: the next
			// pass meets check 12 and the handler takes over.
			msg := fmt.Sprintf("run namespace %s is being deleted; waiting for it to be gone: %v", runNS, merr)
			conds.set(assaydv1alpha1.CondRunNamespaceUnavailable, metav1.ConditionTrue, ReasonTerminating, msg)
			conds.set(assaydv1alpha1.CondReady, metav1.ConditionFalse, "RunNamespaceUnavailable", msg)
			status.Phase = assaydv1alpha1.PhasePending
			status.Conditions = conds.merge(agent.Status.Conditions)
			status.ObservedGeneration = agent.Generation
			if err := r.writeStatus(ctx, &agent, status); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}
		reason := "MaterialUnavailable"
		if me := (*materialError)(nil); errors.As(merr, &me) {
			reason = "MaterialInvalid"
		}
		conds.set(assaydv1alpha1.CondRevisionMaterialUnavailable, metav1.ConditionTrue,
			reason, merr.Error())
		conds.set(assaydv1alpha1.CondReady, metav1.ConditionFalse,
			"RevisionMaterialUnavailable", merr.Error())
		status.Phase = assaydv1alpha1.PhaseDegraded
		status.Conditions = conds.merge(agent.Status.Conditions)
		status.ObservedGeneration = agent.Generation
		if err := r.writeStatus(ctx, &agent, status); err != nil {
			return ctrl.Result{}, err
		}
		// A typed materialError is terminal until a human acts; anything else is
		// transient and must retry with backoff.
		if me := (*materialError)(nil); errors.As(merr, &me) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, merr
	}

	// A PINNED revision's workload and Service are not re-rendered. This is the
	// difference between selecting a release and recomputing one, and getting it
	// wrong was a BLOCKER (reviews/02-step2-fable-review.md T7): every renderer
	// below reads `agent.Spec`, so a pin only redirected which NAME was written
	// while the pod template still came from the current spec. Measured: pin R1
	// after an image change and R1's Deployment ran R2's image, stamped with R1's
	// digest — the annotation that exists to prove which projection a workload
	// came from certifying the opposite, and the collision guard defending it on
	// the next pass.
	//
	// The retained objects already hold that revision's rendering, and the pin
	// resolved against that very Deployment, so they exist by construction.
	// Leaving them alone IS the rollback.
	//
	// The cost is stated rather than hidden, and is in §5: while pinned, this
	// revision's workload is NOT drift-corrected. The operator cannot converge
	// what it has no record of — rebuilding R1's pod template needs R1's spec,
	// and `status.revisions[]` is not on the CRD. Correcting it from the current
	// spec is exactly the defect above.
	if pin == nil {
		if err := r.ensureWorkload(ctx, &agent, runNS, desired, desiredDigest, status, material); err != nil {
			if collision := (*revisionCollisionError)(nil); errors.As(err, &collision) {
				return ctrl.Result{}, r.reportCollision(ctx, &agent, status, conds, collision)
			}
			if apierrors.IsInvalid(err) {
				// The API server rejected the rendered workload. Returning a bare error
				// here retried forever and wrote NO status, so the Agent sat at an empty
				// phase with nothing said — the silent degraded path NFR-8 forbids, and
				// the one an operator is least able to diagnose because the reason lives
				// only in operator logs. A rejected render is a spec the user can fix.
				conds.set(assaydv1alpha1.CondReady, metav1.ConditionFalse, "WorkloadRejected", err.Error())
				conds.set(assaydv1alpha1.CondDegraded, metav1.ConditionTrue, "WorkloadRejected", err.Error())
				status.Phase = assaydv1alpha1.PhaseDegraded
				status.Conditions = conds.merge(agent.Status.Conditions)
				status.ObservedGeneration = agent.Generation
				return ctrl.Result{}, r.writeStatus(ctx, &agent, status)
			}
			return ctrl.Result{}, err
		}
	}

	// The Service is part of materializing a revision, not a later step: a Pod
	// that reports Ready with no Service has no address, and design 03's route
	// backendRef names this object. It is created with the workload and before
	// readiness is judged, so a revision is never "available" without one.
	if pin == nil {
		if err := r.ensureService(ctx, &agent, runNS, desired, desiredDigest); err != nil {
			if collision := (*revisionCollisionError)(nil); errors.As(err, &collision) {
				return ctrl.Result{}, r.reportCollision(ctx, &agent, status, conds, collision)
			}
			if apierrors.IsInvalid(err) {
				conds.set(assaydv1alpha1.CondReady, metav1.ConditionFalse, "ServiceRejected", err.Error())
				conds.set(assaydv1alpha1.CondDegraded, metav1.ConditionTrue, "ServiceRejected", err.Error())
				status.Phase = assaydv1alpha1.PhaseDegraded
				status.Conditions = conds.merge(agent.Status.Conditions)
				status.ObservedGeneration = agent.Generation
				return ctrl.Result{}, r.writeStatus(ctx, &agent, status)
			}
			return ctrl.Result{}, err
		}
	}

	ready, err := r.workloadAvailable(ctx, &agent, runNS, desired, desiredDigest)
	if err != nil {
		return ctrl.Result{}, err
	}

	// What the current spec's -auth compiles to (design 03 §3.3.1). It decides
	// the promotion hold below and the -auth step after the switch.
	desire := desiredAuth(&agent, runNS)
	authHold := r.authHoldsPromotion(status, desire, pin)

	// §3.4: the card is fetched AFTER the revision is available, because the
	// container is the source of truth (ADR-0019) and there is nothing to read
	// until it is running. A failure here is a REGISTRATION failure, never a
	// serving one — the design's remedy for an unregistrable card is the
	// 30-minute deadline, which §5 records as uncounted, so withholding traffic
	// here would invent an enforcement the design does not describe.
	var cardRequeue time.Duration
	if ready && cardFetchDue(status, desiredDigest, time.Now()) {
		card, cerr := r.fetchAndValidateCard(ctx, &agent, runNS, desired)
		switch {
		case cerr != nil:
			ce := (*cardError)(nil)
			if !errors.As(cerr, &ce) {
				return ctrl.Result{}, cerr
			}
			conds.set(assaydv1alpha1.CondRegistered, metav1.ConditionFalse, ce.reason, ce.Error())
			// Stamp the ATTEMPT, so the retry gate measures from something that
			// moves. Keying it on the condition's LastTransitionTime meant the gate
			// opened permanently after its first interval.
			recordCardAttempt(status, desired, desiredDigest, time.Now())
			// Retries live BETWEEN reconciles, not inside one: the work queue is
			// shared, so blocking here would let one unreachable agent delay every
			// other agent's reconcile.
		default:
			// Drift, §3.4: the same revision now serves different bytes. Say so
			// rather than replacing the digest in silence — "detectable" previously
			// meant a human diffing two status dumps, which is not a signal.
			prev := cardEntry(status, desiredDigest)
			drifted := prev != nil && prev.Digest != "" && prev.Digest != card.Digest
			status.Cards = upsertCard(status.Cards, *card, desiredDigest)
			switch {
			case drifted:
				logger.Info("agent card drifted", "revision", desired,
					"from", prev.Digest[:12], "to", card.Digest[:12])
				conds.set(assaydv1alpha1.CondRegistered, metav1.ConditionTrue, "CardDrifted",
					fmt.Sprintf("revision %s now serves a different card: digest %s, was %s. "+
						"The new card is valid and the revision keeps serving; a card change "+
						"without a spec change is the agent redescribing itself, which nothing "+
						"gates. No event is emitted — no EventRecorder is wired (§5)",
						desired, card.Digest[:12], prev.Digest[:12]))
			default:
				conds.set(assaydv1alpha1.CondRegistered, metav1.ConditionTrue, "CardValidated",
					fmt.Sprintf("revision %s serves a valid A2A card (digest %s)", desired, card.Digest[:12]))
			}
			// Loud rather than silent, per design 09: nothing in this repository
			// signs a card, so every agent is the unsigned BYO case that rule was
			// written for. Clearing this would claim a verification that no code
			// performs.
			conds.set(assaydv1alpha1.CondCardUnsigned, metav1.ConditionTrue, "NoSigningConfigured",
				"the card is not signature-verified: design 09's Sigstore card signing is not "+
					"implemented, so no card in this cluster is signed or checked")
		}
	}

	// Keep coming back until this revision HAS a card, whether or not an attempt
	// was made just now.
	//
	// Setting the requeue only on a failed attempt was a real bug, found by
	// running it: reconcile 1 fails and schedules +15s, the status write it
	// performs immediately triggers reconcile 2, reconcile 2 skips because the
	// retry interval has not elapsed and returns RequeueAfter=0 — and the queue's
	// pending delayed add is consumed by that early run. Nothing ever came back,
	// so a card that was merely fetched a second too early stayed unfetched
	// forever. Forcing one reconcile by hand registered it instantly, which is
	// what showed the fetch had never been the problem.
	// Always come back while this revision is ready: on the retry interval until
	// it registers, and on the DRIFT interval once it has. Returning zero after a
	// successful registration is what made CardDriftInterval a bound nothing
	// enforced — no SyncPeriod is set, so a converged agent was not re-read for
	// the manager's ~10h default.
	if ready {
		cardRequeue = cardRequeueAfter(status, desiredDigest)
	}

	switch {
	case !ready && status.ActiveRevision != "" && status.ActiveRevisionDigest != desiredDigest &&
		r.revisionAvailable(ctx, &agent, runNS, status.ActiveRevision, status.ActiveRevisionDigest):
		// A genuine rollout: an earlier revision still holds all traffic and is
		// healthy while its successor comes up. Reporting Ready=False here would
		// trip every alert keyed on the canonical condition on any routine spec
		// edit; reporting Canary would claim a weighted shift that §3.3 defines
		// and that no gateway is performing yet (A13).
		status.CandidateRevision, status.CandidateRevisionDigest = desired, desiredDigest
		status.Phase = assaydv1alpha1.PhaseReady
		conds.set(assaydv1alpha1.CondReady, metav1.ConditionTrue, "Available",
			fmt.Sprintf("revision %s is serving", status.ActiveRevision))
		// The guard on this case now CHECKS that. `ready` is computed for the
		// DESIRED revision only, so this branch used to report "revision X is
		// serving" from the mere presence of a name in status — true of an agent
		// whose active workload had been deleted, which then read as a healthy
		// rollout instead of an outage.
		conds.set(assaydv1alpha1.CondProgressing, metav1.ConditionTrue, "CandidateNotAvailable",
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
		conds.set(assaydv1alpha1.CondProgressing, metav1.ConditionFalse, "NoRolloutInFlight",
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
			status.Phase = assaydv1alpha1.PhaseDegraded
			status.CandidateRevision, status.CandidateRevisionDigest = "", ""
			// Assert Degraded, do not merely set the phase. CondDegraded is owned and
			// non-sticky, so a path that sets the phase without the condition actively
			// CLEARS it — and an alert keyed on the condition would miss the worst
			// case in this machine.
			conds.set(assaydv1alpha1.CondDegraded, metav1.ConditionTrue, "WorkloadUnavailable",
				fmt.Sprintf("revision %s is the active revision but has no available replicas", desired))
			conds.set(assaydv1alpha1.CondReady, metav1.ConditionFalse, "WorkloadUnavailable",
				fmt.Sprintf("revision %s is the active revision but has no available replicas", desired))
			break
		}
		status.Phase = assaydv1alpha1.PhasePending
		conds.set(assaydv1alpha1.CondReady, metav1.ConditionFalse, "WorkloadNotAvailable",
			fmt.Sprintf("revision %s has no available replicas yet", desired))

	case authHold != "" && status.ActiveRevision != "" && status.ActiveRevisionDigest != desiredDigest:
		// Design 03 §3.3.1, I1: "a revision whose projection carries an -auth
		// input this build cannot compile gains no weight while that is so".
		// The served route keeps its last good -auth and keeps naming the
		// revision it names; promoting would move the route onto a revision
		// minted by the very edit that does not compile.
		status.Phase = assaydv1alpha1.PhaseHeld
		status.CandidateRevision, status.CandidateRevisionDigest = desired, desiredDigest
		conds.set(assaydv1alpha1.CondProgressing, metav1.ConditionTrue, "AuthInputUncompilable",
			fmt.Sprintf("revision %s is available and held at zero traffic: %s", desired, authHold))
		conds.set(assaydv1alpha1.CondReady, metav1.ConditionTrue, "Available",
			fmt.Sprintf("revision %s is serving", status.ActiveRevision))

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
		status.Phase = assaydv1alpha1.PhaseHeld
		status.CandidateRevision, status.CandidateRevisionDigest = desired, desiredDigest
		// Progressing must be asserted here too: it is sticky, so a branch that
		// stays silent leaves the previous reason in place — and "CandidateNotAvailable"
		// is false once the candidate is available and merely held.
		conds.set(assaydv1alpha1.CondProgressing, metav1.ConditionTrue, "AwaitingGates",
			fmt.Sprintf("revision %s is available and held at zero traffic pending its eval gates", desired))
		if status.ActiveRevision != "" {
			conds.set(assaydv1alpha1.CondReady, metav1.ConditionTrue, "Available",
				fmt.Sprintf("revision %s is serving", status.ActiveRevision))
		} else {
			conds.set(assaydv1alpha1.CondReady, metav1.ConditionFalse, "AwaitingGates",
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
		status.Phase = assaydv1alpha1.PhaseReady
		conds.set(assaydv1alpha1.CondProgressing, metav1.ConditionFalse, "RolloutComplete",
			fmt.Sprintf("revision %s is the active revision", desired))
		conds.set(assaydv1alpha1.CondReady, metav1.ConditionTrue, "Available",
			fmt.Sprintf("revision %s is serving", desired))
	}

	// The route and its -auth are converged AFTER the switch above, from
	// status.activeRevision — the revision that is SERVING, never the desired
	// one. A candidate coming up must not take the hostname from the revision
	// still answering on it, which is the whole reason design 02 gives a
	// Service to every revision. With the gateway declared off this does
	// nothing at all (design 03 §3.1's table).
	gw, rerr := r.reconcileGateway(ctx, &agent, runNS, status, conds, desire)
	if rerr != nil {
		// A route or a policy that could not be written means this agent is not
		// reachable through the Gateway as it should be, and on a
		// gateway-enabled install that is not Ready.
		//
		// A LOST RACE IS NOT A DEGRADATION. An AlreadyExists from a stale
		// informer cache, or a Conflict from a concurrent write, resolves on the
		// next pass; flipping Ready for it would page the on-call for cache lag.
		// Those return the error so the queue retries with backoff and change no
		// condition, which is the same treatment ensureService gives the same
		// races.
		if !transientRouteWrite(rerr) {
			msg := rerr.Error()
			reason := gatewayErrorReason(rerr)
			conds.set(assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, reason, msg)
			conds.set(assaydv1alpha1.CondReady, metav1.ConditionFalse, reason, msg)
			// Assert Degraded, do not merely set the phase. CondDegraded is owned
			// and non-sticky, so a path that sets the phase without the condition
			// actively CLEARS it — and design 10 alerts on the condition, so this
			// would page nobody while the agent was unreachable. The identical
			// defect is recorded two hundred lines above, on the
			// WorkloadUnavailable branch; it was reintroduced here and caught by
			// the independent review of A6.10.
			// An Agent a Create is still bringing up was never serving, so the
			// aggregation says Pending, not Degraded (design 03 §3.3.1).
			if status.Auth != nil && status.Auth.Mode == "" && status.Auth.Transaction != nil &&
				status.Auth.Transaction.Kind == TxCreate {
				status.Phase = assaydv1alpha1.PhasePending
			} else {
				conds.set(assaydv1alpha1.CondDegraded, metav1.ConditionTrue, reason, msg)
				status.Phase = assaydv1alpha1.PhaseDegraded
			}
		}
		status.Conditions = conds.merge(agent.Status.Conditions)
		status.ObservedGeneration = agent.Generation
		if err := r.writeStatus(ctx, &agent, status); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, rerr
	}
	withholdReady(status, conds, gw)

	status.Conditions = conds.merge(agent.Status.Conditions)
	status.ObservedGeneration = agent.Generation
	if err := r.writeStatus(ctx, &agent, status); err != nil {
		return ctrl.Result{}, err
	}
	// Collect AFTER the status that authorizes it is durable: a destructive act
	// ordered ahead of its own record is backwards, even when it converges.
	if err := r.collectGarbage(ctx, &agent, runNS, owned, status); err != nil {
		return ctrl.Result{}, err
	}
	// A card that could not be fetched is retried here rather than in a sleep
	// inside the fetch, so one unreachable agent cannot delay another's reconcile.
	return ctrl.Result{RequeueAfter: earliest(cardRequeue, gw.requeue)}, nil
}

// earliest is the sooner of two requeues, where zero means none.
func earliest(a, b time.Duration) time.Duration {
	if a == 0 || (b != 0 && b < a) {
		return b
	}
	return a
}

// authHoldsPromotion says why a new revision may not be promoted, or "": a
// served Agent whose current -auth input does not compile keeps its route on
// the revision it names (design 03 §3.3.1, I1). A pinned rollback is not held:
// it selects a revision that already served, and -auth follows the current
// spec, never a revision (ADR-0034, F2).
func (r *AgentReconciler) authHoldsPromotion(status *assaydv1alpha1.AgentStatus, d authDesire,
	pin *releasePin) string {
	if !r.Gateway.Enabled || pin != nil || status.Auth == nil || d.compiles() {
		return ""
	}
	// A served Agent, and one whose Create is in flight: that Create finishes
	// to its recorded target and publishes the revision the route names, so a
	// revision an uncompilable edit minted must not become that revision.
	inFlight := status.Auth.Transaction != nil && status.Auth.Transaction.Kind == TxCreate
	if status.Auth.Mode == "" && !inFlight {
		return ""
	}
	return "its -auth input does not compile, and this Agent's route keeps the -auth it has or is " +
		"being given (design 03 §3.3.1, I1). PolicyCompileFailed says what to change"
}

// withholdReady applies design 03 §3.3.1's aggregation for the -auth step: a
// failing serving route makes Ready False, reason by cause, and the phase
// Degraded for an Agent that was serving or Pending for one being created. It
// never overrides a Ready that is already False: that cause is as real, and
// the -auth conditions are set either way.
func withholdReady(status *assaydv1alpha1.AgentStatus, conds *conditionSet, gw gatewayOutcome) {
	w := gw.withhold
	if w == nil {
		return
	}
	if ready, ok := conds.get(assaydv1alpha1.CondReady); ok && ready.Status != metav1.ConditionTrue {
		return
	}
	conds.set(assaydv1alpha1.CondReady, metav1.ConditionFalse, w.reason, w.message)
	if gw.served {
		conds.set(assaydv1alpha1.CondDegraded, metav1.ConditionTrue, w.reason, w.message)
		status.Phase = assaydv1alpha1.PhaseDegraded
		return
	}
	status.Phase = assaydv1alpha1.PhasePending
}

// reportUnreconcilable records why an Agent cannot be acted on, rather than
// error-looping with an empty status that says nothing.
func (r *AgentReconciler) reportUnreconcilable(ctx context.Context, agent *assaydv1alpha1.Agent) error {
	status := agent.Status.DeepCopy()
	status.Phase = assaydv1alpha1.PhaseDegraded
	conds := newConditionSet(agent.Generation)
	// Design 03 §3.1 puts the tier condition on EVERY Agent, and an Agent that
	// cannot be reconciled is still one. Owned and asserted nowhere else on this
	// path, it would otherwise be CLEARED here — so an Agent that went degraded
	// would silently lose the record of which tier it runs in.
	r.assessGovernance(conds, status)
	conds.set(assaydv1alpha1.CondDegraded, metav1.ConditionTrue, "NoWorkloadSpecified",
		"neither spec.runtime nor spec.external is set, so there is nothing to reconcile; "+
			"set exactly one of them")
	conds.set(assaydv1alpha1.CondReady, metav1.ConditionFalse, "NoWorkloadSpecified",
		"the agent specifies no workload")
	status.Conditions = conds.merge(agent.Status.Conditions)
	status.ObservedGeneration = agent.Generation
	return r.writeStatus(ctx, agent, status)
}

// reconcileExternal handles an agent that runs outside this cluster.
func (r *AgentReconciler) reconcileExternal(ctx context.Context, agent *assaydv1alpha1.Agent) (ctrl.Result, error) {
	status := agent.Status.DeepCopy()
	status.Phase = assaydv1alpha1.PhasePending
	conds := newConditionSet(agent.Generation)
	conds.set(assaydv1alpha1.CondReady, metav1.ConditionFalse, "ExternalRegistrationUnimplemented",
		"external agents need OAuth client provisioning (design 06) and card fetch (§3.4), "+
			"neither of which is implemented; the agent is held rather than reported ready")
	// Design 03 §3.1 puts GovernanceSkipped on EVERY Agent, not on the ones that
	// happen to run here. An external agent is the case where an ungoverned tier
	// matters most: nothing about it is in this cluster except the record.
	r.assessGovernance(conds, status)
	status.Conditions = conds.merge(agent.Status.Conditions)
	status.ObservedGeneration = agent.Generation
	return ctrl.Result{}, r.writeStatus(ctx, agent, status)
}

// ensureWorkload creates or updates the Deployment for one revision.
// RevisionDigestAnnotation records which projection a workload was rendered
// from. The Deployment's NAME carries only 40 bits, so the name alone cannot
// answer "is this the same revision?" — this can.
const RevisionDigestAnnotation = "assayd.dev/revision-digest"

// revisionCollisionError reports two different projections claiming one
// workload name. It is terminal by design: see ensureWorkload.
type revisionCollisionError struct {
	name, existing, desired string
	// kind names the object when it is not a Deployment, so a Service collision
	// does not report itself as a workload one and send an operator to the wrong
	// object. Empty means workload.
	kind string
	// role is set when the collision was found in STATUS rather than on a
	// workload, so the message can say "the active revision …" instead of
	// rendering a role name into a "workload %s" slot.
	role string
}

func (e *revisionCollisionError) Error() string {
	if e.kind == "service" {
		return fmt.Sprintf("service %s carries revision digest %s and this revision is %s. Two "+
			"different projections claim one revision name, so converging it would point this "+
			"revision's route at another projection's Pods. Refusing. To recover: delete that "+
			"Service and let the operator recreate it.", e.name, e.existing, e.desired)
	}
	if e.existing == "(another agent's)" {
		return fmt.Sprintf("workload %s exists in the run namespace and does not carry this Agent's UID, "+
			"so this operator did not create it for this Agent. Refusing to converge. To recover: delete "+
			"that Deployment and let the operator recreate it.", e.name)
	}
	if e.existing == "(no stamp)" {
		return fmt.Sprintf("workload %s carries no assayd.dev/revision-digest and nothing in status "+
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
func collisionAgainstStatus(st *assaydv1alpha1.AgentStatus, rev, digest string) *revisionCollisionError {
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
func (r *AgentReconciler) reportCollision(ctx context.Context, agent *assaydv1alpha1.Agent,
	status *assaydv1alpha1.AgentStatus, conds *conditionSet, c *revisionCollisionError) error {
	conds.set(assaydv1alpha1.CondRevisionHashCollision, metav1.ConditionTrue, "DigestMismatch", c.Error())
	conds.set(assaydv1alpha1.CondReady, metav1.ConditionFalse, "RevisionHashCollision", c.Error())
	// Degraded is asserted, not merely implied by the phase. CondDegraded is
	// owned and non-sticky, so a path that sets the phase and stays silent
	// actively CLEARS the condition — and a suspected chosen collision is the
	// worst state this machine has, which is exactly when an alert keyed on the
	// condition must not go quiet.
	conds.set(assaydv1alpha1.CondDegraded, metav1.ConditionTrue, "RevisionHashCollision", c.Error())
	status.Phase = assaydv1alpha1.PhaseDegraded
	status.Conditions = conds.merge(agent.Status.Conditions)
	status.ObservedGeneration = agent.Generation
	return r.writeStatus(ctx, agent, status)
}

// revisionAvailable answers whether a NAMED revision has available replicas,
// as opposed to workloadAvailable, which only ever answers for the desired one.
// An error is reported as unavailable: claiming a revision is serving because
// the API server did not answer is the loud-and-wrong of rule 8.
func (r *AgentReconciler) revisionAvailable(ctx context.Context, agent *assaydv1alpha1.Agent, runNS, rev, digest string) bool {
	ok, err := r.workloadAvailable(ctx, agent, runNS, rev, digest)
	return err == nil && ok
}

// resolveEnvSources reads every ConfigMap and Secret the runtime references and
// returns their content digests. A source that does not exist, or that the
// operator may not read, is returned as unresolved rather than as an error: the
// two are the same fact to an Agent — its behaviour is not knowable — and both
// must stop the revision rather than produce a partial identity.
func (r *AgentReconciler) resolveEnvSources(ctx context.Context, agent *assaydv1alpha1.Agent) (
	revision.Resolved, map[revision.SourceRef]*sourceBuffer, []revision.SourceRef, error) {
	refs := revision.EnvSources(agent.Spec)
	if len(refs) == 0 {
		return nil, nil, nil, nil
	}
	out := revision.Resolved{}
	// ONE read fills both. A35's copies are created from these exact bytes, never
	// from a second read (A39).
	buffers := map[revision.SourceRef]*sourceBuffer{}
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
				return nil, nil, nil, fmt.Errorf("read %s for %s: %w", ref, agent.Name, err)
			}
			d := revision.ContentDigest(cm.Data, cm.BinaryData)
			out[ref] = d
			buffers[ref] = &sourceBuffer{kind: ref.Kind, data: cm.Data, binary: cm.BinaryData, digest: d}
		case "Secret":
			var sec corev1.Secret
			if err := r.Get(ctx, key, &sec); err != nil {
				if apierrors.IsNotFound(err) || apierrors.IsForbidden(err) {
					unresolved = append(unresolved, ref)
					continue
				}
				return nil, nil, nil, fmt.Errorf("read %s for %s: %w", ref, agent.Name, err)
			}
			// StringData is a write-only convenience the API server folds into
			// Data, so reading Data alone is complete.
			d := revision.ContentDigest(nil, sec.Data)
			out[ref] = d
			buffers[ref] = &sourceBuffer{kind: ref.Kind, binary: sec.Data, digest: d}
		}
	}
	return out, buffers, unresolved, nil
}

// reportUnresolvedSources is the A20 failure path: no revision, no workload, and
// a condition naming what could not be read.
func (r *AgentReconciler) reportUnresolvedSources(ctx context.Context, agent *assaydv1alpha1.Agent,
	unresolved []revision.SourceRef, cause error) error {
	status := agent.Status.DeepCopy()
	conds := newConditionSet(agent.Generation)
	r.assessGates(agent, conds)
	r.assessSandbox(agent, conds)
	r.assessTaskState(agent, status, conds)
	r.assessGovernance(conds, status)

	names := make([]string, 0, len(unresolved))
	for _, u := range unresolved {
		names = append(names, u.String())
	}
	msg := cause.Error()
	if len(names) > 0 {
		msg = fmt.Sprintf("%s: %s. Create them, or remove the reference from spec.runtime",
			strings.Join(names, ", "), cause)
	}
	conds.set(assaydv1alpha1.CondEnvSourceUnresolved, metav1.ConditionTrue, "Unresolved", msg)
	conds.set(assaydv1alpha1.CondReady, metav1.ConditionFalse, "EnvSourceUnresolved", msg)
	status.Phase = assaydv1alpha1.PhasePending
	status.Conditions = conds.merge(agent.Status.Conditions)
	status.ObservedGeneration = agent.Generation
	return r.writeStatus(ctx, agent, status)
}

func (r *AgentReconciler) ensureWorkload(ctx context.Context, agent *assaydv1alpha1.Agent, runNS, rev, digest string,
	status *assaydv1alpha1.AgentStatus, material map[revision.SourceRef]string) error {
	desired := r.deploymentFor(agent, runNS, rev, material)
	if desired.Annotations == nil {
		desired.Annotations = map[string]string{}
	}
	desired.Annotations[RevisionDigestAnnotation] = digest
	// No ownerReference: the Deployment is in the run namespace and the Agent is
	// not, and a cross-namespace owner reference is treated as absent (A44/A60).
	// Provenance is the name, the assayd.dev/agent-uid label and the
	// status-vouched digest; the finalizer collects it.

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
	// upgraded cluster. There is no such workload: assayd is unreleased, so the
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
	case existing.Labels[LabelAgentUID] != string(agent.UID):
		// A60: AlreadyExists is never provenance. An object under this name that
		// does not carry this Agent's UID is someone else's, and is refused whole
		// rather than converged.
		return &revisionCollisionError{name: existing.Name, existing: "(another agent's)", desired: digest}
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
func (r *AgentReconciler) deploymentFor(agent *assaydv1alpha1.Agent, runNS, rev string,
	material map[revision.SourceRef]string) *appsv1.Deployment {
	rt := agent.Spec.Runtime
	// The selector is {agent, revision}; the object and its Pods additionally
	// carry the Agent's UID (provenance, A60) and the Agent's OWN namespace —
	// the SVID path segment reads that label, so the move to the run namespace
	// is invisible to authorization (A59).
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
			Namespace: runNS,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: selector},
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
						Env:       withInjectedEnv(rewriteEnv(rt.Env, material), r.InjectedEnv),
						EnvFrom:   rewriteEnvFrom(rt.EnvFrom, material),
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
						// (ADR-0019). Always is deliberate — assayd permits tags, and a tag
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
func (r *AgentReconciler) workloadAvailable(ctx context.Context, agent *assaydv1alpha1.Agent, runNS, rev, digest string) (bool, error) {
	var d appsv1.Deployment
	key := types.NamespacedName{Namespace: runNS, Name: WorkloadName(agent.Name, rev)}
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
	if d.Annotations[RevisionDigestAnnotation] != digest || d.Labels[LabelAgentUID] != string(agent.UID) {
		return false, nil
	}
	return d.Status.AvailableReplicas > 0, nil
}

// gatesSatisfied reports whether this agent may take traffic.
//
// There is deliberately no separate branch for the unwired case: evalSuiteInstalled
// reports true when the hook is unset, which lands here as "assume the CRD is
// present", and an agent with declared gates then holds. A second nil-check
// would read as load-bearing while never changing an outcome — and defensive
// code no test can pin is worse than none, because it invites the next reader to
// trust it.
func (r *AgentReconciler) gatesSatisfied(agent *assaydv1alpha1.Agent) bool {
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

func (r *AgentReconciler) assessGates(agent *assaydv1alpha1.Agent, c *conditionSet) {
	switch {
	// The no-gates case comes FIRST, and deliberately. With nothing declared to
	// gate, detection cannot change the outcome — the agent promotes either way —
	// so reporting "holding rather than promoting ungated" on an agent that just
	// promoted would be loud and wrong, which is exactly what NFR-8 forbids.
	case len(agent.Spec.Gates) == 0:
		c.set(assaydv1alpha1.CondGatesSkipped, metav1.ConditionTrue, "NoGatesDeclared",
			"no spec.gates are declared, so this rollout is not eval-gated")
	case r.EvalSuiteInstalled == nil:
		// Gates ARE declared and we cannot tell whether the CRD is present. Hold,
		// and say exactly that.
		c.set(assaydv1alpha1.CondGatesPassed, metav1.ConditionFalse, "GateDetectionUnwired",
			"the operator was built without EvalSuite detection, so it cannot tell whether "+
				"this rollout should be eval-gated; holding rather than promoting ungated")
	case !r.evalSuiteInstalled():
		c.set(assaydv1alpha1.CondGatesSkipped, metav1.ConditionTrue, "EvalSuiteCRDAbsent",
			"the EvalSuite CRD is not installed, so this rollout is NOT eval-gated (design 02 §3.3, core tier)")
	default:
		c.set(assaydv1alpha1.CondGatesPassed, metav1.ConditionFalse, "GateControllerUnimplemented",
			"spec.gates are declared but the gate controller (design 16) is not implemented; "+
				"the revision holds at zero traffic rather than promoting ungated")
	}
}

// assessSandbox reports the §3.2 downgrade. The agent-sandbox CRD is not bound
// yet, so every sandboxed agent currently downgrades — stated loudly, per NFR-8,
// rather than silently running an unsandboxed pod.
//
// EnvSourceProtectionUnavailable is no longer raised. It announced the
// env-source bypass while A20, A35 and A42 were design; the last of the three
// landed with the run namespace (runnamespace.go), and a real-cluster test
// proves a principal with create/delete on ConfigMaps in the Agent's namespace
// can no longer replace a published revision's copy. The condition type stays
// in the closed vocabulary and in the OWNED set so a stale True written by an
// operator before A42 is CLEARED on the first reconcile after upgrade — an
// abnormal-true condition that no longer applies is what teaches operators to
// ignore conditions.
func (r *AgentReconciler) assessSandbox(agent *assaydv1alpha1.Agent, c *conditionSet) {
	if agent.Spec.Runtime == nil || agent.Spec.Runtime.Sandbox == nil {
		return
	}
	c.set(assaydv1alpha1.CondSandboxDowngraded, metav1.ConditionTrue, "SandboxRuntimeUnavailable",
		fmt.Sprintf("spec.runtime.sandbox.profile=%q was requested, but no sandbox runtime is bound; "+
			"running a hardened Deployment instead (non-root, read-only rootfs, seccomp, no capabilities)",
			agent.Spec.Runtime.Sandbox.Profile))
}

// assessTaskState implements §3.2's detected-not-assumed rule. The A2A card is
// the declaration point, and card fetch is unimplemented, so replicas>1 cannot
// currently be verified and the condition names that rather than assuming.
func (r *AgentReconciler) assessTaskState(agent *assaydv1alpha1.Agent, status *assaydv1alpha1.AgentStatus, c *conditionSet) {
	if agent.Spec.Runtime == nil || agent.Spec.Runtime.Replicas <= 1 {
		return
	}
	// §3.2 says "the A2A card is the declaration point" for shared task state,
	// and A2A v1.0 has no such declaration. `AgentCapabilities` is exactly four
	// fields — streaming, pushNotifications, extensions, extendedAgentCard — and
	// `grep -ci shared` over a2a.proto returns 0 (A71). So no conformant agent
	// can ever clear this, and saying so is the honest reading.
	//
	// It is deliberately NOT keyed off the registered card any more. Doing that
	// meant keying off a field that could only be absent — a check that looks
	// like one and is a constant, which is worse than no check because it reads
	// as verified. The sanctioned vehicle would be capabilities.extensions[]
	// under a assayd-owned URI; that is a design decision, not one to take inside
	// an assessor.
	c.set(assaydv1alpha1.CondTaskStateUnverified, metav1.ConditionTrue,
		"ProtocolHasNoDeclaration",
		fmt.Sprintf("replicas=%d, and A2A v1.0 gives a card no way to assert shared task "+
			"state: AgentCapabilities carries streaming, pushNotifications, extensions and "+
			"extendedAgentCard, and nothing else. Concurrent replicas may lose task state "+
			"and this operator cannot tell (design 02 A71)", agent.Spec.Runtime.Replicas))
}

// collectGarbage deletes revisions beyond the retention window. The window is N
// revisions IN ADDITION TO the active one and any in-flight candidate, so a
// rollout can never GC its own rollback target.
func (r *AgentReconciler) collectGarbage(
	ctx context.Context, agent *assaydv1alpha1.Agent, runNS string,
	owned []appsv1.Deployment, status *assaydv1alpha1.AgentStatus,
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
	keep := map[string]bool{}
	for k := range protected {
		keep[k] = true
	}
	for i, d := range retainable {
		if i < limit {
			keep[d.Labels[LabelRevision]] = true
			continue
		}
		if err := r.Delete(ctx, &retainable[i]); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("gc workload %s: %w", d.Name, err)
		}
	}
	// The material goes with the revision. It carries no ownerReference by A44,
	// so Kubernetes will not collect it and a leaked copy is a leaked snapshot of
	// a Secret's bytes.
	if err := r.collectRevisionMaterial(ctx, agent, runNS, keep); err != nil {
		return err
	}
	// The Service goes with its revision for the same reason: no ownerReference,
	// so nothing else will collect it.
	if err := r.collectRevisionServices(ctx, agent, runNS, keep); err != nil {
		return err
	}
	// And the card entry, which §3.4 says is "collected with revisions" and which
	// nothing collected — one entry per revision the agent had ever registered.
	//
	// This runs AFTER the reconcile's status write, so a prune that changes
	// something must write again or it is simply lost. It is rare — only when a
	// revision leaves the retained set — and the alternative, dropping the write,
	// would make the pruning code look like it worked while nothing persisted.
	if pruneCards(status, keep) {
		if err := r.writeStatus(ctx, agent, status); err != nil {
			return fmt.Errorf("persist pruned cards: %w", err)
		}
	}
	return r.collectPreA42Leftovers(ctx, agent)
}

// collectPreA42Leftovers removes what an operator built before A42 left in the
// Agent's OWN namespace: workloads it owned by controller reference, and
// copies of the material name shape. Without this an upgrade ran two
// workloads per Agent and leaked the old Secret copies where a namespace
// editor could read them (found by the code review of A61). Ownership here is
// the pre-A42 kind — a same-namespace controller reference, which nobody can
// forge by labelling — and the copies are decided by isRevisionMaterial's name
// authority as everywhere else.
func (r *AgentReconciler) collectPreA42Leftovers(ctx context.Context, agent *assaydv1alpha1.Agent) error {
	var list appsv1.DeploymentList
	if err := r.List(ctx, &list, client.InNamespace(agent.Namespace),
		client.MatchingLabels{LabelAgent: agent.Name}); err != nil {
		return fmt.Errorf("list pre-A42 workloads for %s/%s: %w", agent.Namespace, agent.Name, err)
	}
	for i := range list.Items {
		d := &list.Items[i]
		if ref := metav1.GetControllerOf(d); ref == nil || ref.UID != agent.UID {
			continue
		}
		if err := r.Delete(ctx, d); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("delete pre-A42 workload %s: %w", d.Name, err)
		}
	}
	return r.deleteMaterial(ctx, agent, agent.Namespace,
		client.MatchingLabels{MaterialAgentUIDLabel: string(agent.UID)})
}

// ownedWorkloads returns the Deployments this Agent actually controls.
//
// Selecting on the label alone was a defect with teeth: a stray assayd.dev/agent
// label — copied from an example, applied by a Kustomize commonLabels, or set by
// anyone with deployment-create in the namespace — made this operator a deleter
// of other people's workloads. Ownership used to be the controller reference;
// under A42 the Agent is in another namespace and that reference is treated as
// absent (A44/A60). So ownership is the NAME — `<agent>-<revision>`, immutable,
// so a victim object cannot be renamed into the shape (A57) — corroborated by
// the assayd.dev/agent-uid label. Nothing here is deleted for wearing a label.
func (r *AgentReconciler) ownedWorkloads(ctx context.Context, agent *assaydv1alpha1.Agent, runNS string) ([]appsv1.Deployment, error) {
	var list appsv1.DeploymentList
	if err := r.List(ctx, &list,
		client.InNamespace(runNS),
		client.MatchingLabels{LabelAgent: agent.Name},
	); err != nil {
		return nil, fmt.Errorf("list workloads for %s/%s: %w", agent.Namespace, agent.Name, err)
	}

	owned := make([]appsv1.Deployment, 0, len(list.Items))
	for _, d := range list.Items {
		if !isRevisionWorkload(agent, &d) {
			continue
		}
		owned = append(owned, d)
	}
	return owned, nil
}

// isRevisionWorkload decides whether a Deployment is one THIS operator created
// for THIS Agent: name shape, revision label, and the Agent's UID.
func isRevisionWorkload(agent *assaydv1alpha1.Agent, d *appsv1.Deployment) bool {
	rev := d.Labels[LabelRevision]
	// A workload with no revision label cannot be placed in the retention
	// window, so it must never be a GC candidate.
	if rev == "" || d.Labels[LabelAgentUID] != string(agent.UID) {
		return false
	}
	return d.Name == WorkloadName(agent.Name, rev)
}

// finalize runs §3.7's ordered teardown. Only the steps whose components exist
// are implemented; the rest are named here so that adding them is an edit to a
// stated sequence rather than a rediscovery of it.
func (r *AgentReconciler) finalize(ctx context.Context, agent *assaydv1alpha1.Agent) (ctrl.Result, error) {
	// TODO(design 03/06/05): drain traffic to weight 0 respecting taskTimeout,
	// revoke the gateway resources nothing emits yet (Backends, the other policy
	// concerns) in reverse apply order, deactivate (never delete) the
	// agent-actor OAuth client, GC directory entries.
	if !containsString(agent.Finalizers, Finalizer) {
		return ctrl.Result{}, nil
	}
	// Nothing here goes with an ownerReference: workloads and material live in
	// the run namespace (A42) and a cross-namespace owner is treated as absent
	// (A44/A60), so this step is the only thing that collects them. A copy left
	// behind is a snapshot of a Secret's bytes outliving the Agent that
	// justified reading them.
	runNS := RunNamespaceName(agent.Namespace)
	var run corev1.Namespace
	switch err := r.Get(ctx, types.NamespacedName{Name: runNS}, &run); {
	case apierrors.IsNotFound(err):
		// Nothing to collect; the binding, if any, is the handler's.
	case err != nil:
		return ctrl.Result{}, fmt.Errorf("read run namespace %s: %w", runNS, err)
	case run.DeletionTimestamp.IsZero():
		// The route goes FIRST, and the order is the point: §3.7 revokes gateway
		// routes in reverse apply order, so traffic stops arriving before the
		// workload it was arriving at disappears. It is not a drain — draining
		// to weight 0 while honouring taskTimeout is still owed, and the TODO
		// above still names it.
		//
		// Guarded on the declared gateway, and not for symmetry: a disabled
		// install has no reason to carry the Gateway API CRDs, the List would
		// fail with a no-matching-kind error, and a finalizer that returns an
		// error never releases — every Agent in the cluster would become
		// undeletable the moment someone tried.
		//
		// The route's `-auth` policy goes after it, and only once the route is
		// gone (design 03 §3.3, step 5). Nothing else is torn down while either
		// delete is pending, so, while the run namespace is live, the route
		// never outlives its policy and the workload never outlives its route.
		// A run namespace that is itself being deleted does not reach this
		// branch, and the namespace controller removes both in no order this
		// sets.
		if r.Gateway.Enabled {
			waiting, err := r.revokeGateway(ctx, agent, runNS)
			if err != nil {
				return ctrl.Result{}, err
			}
			if waiting != "" {
				return ctrl.Result{RequeueAfter: teardownPendingRequeue},
					r.reportTeardownWaiting(ctx, agent, waiting)
			}
		}
		owned, err := r.ownedWorkloads(ctx, agent, runNS)
		if err != nil {
			return ctrl.Result{}, err
		}
		for i := range owned {
			if err := r.Delete(ctx, &owned[i]); err != nil && !apierrors.IsNotFound(err) {
				return ctrl.Result{}, fmt.Errorf("delete workload %s: %w", owned[i].Name, err)
			}
		}
		ownedSvc, err := r.ownedServices(ctx, agent, runNS)
		if err != nil {
			return ctrl.Result{}, err
		}
		for i := range ownedSvc {
			if err := r.Delete(ctx, &ownedSvc[i]); err != nil && !apierrors.IsNotFound(err) {
				return ctrl.Result{}, fmt.Errorf("delete service %s: %w", ownedSvc[i].Name, err)
			}
		}
		if err := r.deleteMaterial(ctx, agent, runNS,
			client.MatchingLabels{MaterialAgentUIDLabel: string(agent.UID)}); err != nil {
			return ctrl.Result{}, err
		}
	}
	if err := r.collectPreA42Leftovers(ctx, agent); err != nil {
		return ctrl.Result{}, err
	}
	if err := r.releaseRunNamespaceIfLast(ctx, agent); err != nil {
		return ctrl.Result{}, err
	}
	// A metadata patch, for the reason the add gives: a typed Update of an
	// Agent stored with spec.budget is refused, the release fails forever, and
	// `kubectl delete` leaves the Agent Terminating after its workloads are gone.
	base := agent.DeepCopy()
	agent.Finalizers = removeString(agent.Finalizers, Finalizer)
	if err := r.Patch(ctx, agent, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})); err != nil {
		return ctrl.Result{}, fmt.Errorf("release finalizer on %s/%s: %w", agent.Namespace, agent.Name, err)
	}
	return ctrl.Result{}, nil
}

func (r *AgentReconciler) writeStatus(ctx context.Context, agent *assaydv1alpha1.Agent, status *assaydv1alpha1.AgentStatus) error {
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
	// Workloads carry no ownerReference (A42/A60), so Owns() would never fire.
	// The watch maps by the labels the operator stamps: the Agent's own
	// namespace and name. Anyone who labels a Deployment that way only triggers
	// a reconcile, which is idempotent.
	byAgentLabels := handler.EnqueueRequestsFromMapFunc(func(_ context.Context, o client.Object) []reconcile.Request {
		l := o.GetLabels()
		if l[LabelAgentNamespace] == "" || l[LabelAgent] == "" {
			return nil
		}
		return []reconcile.Request{{NamespacedName: types.NamespacedName{
			Namespace: l[LabelAgentNamespace], Name: l[LabelAgent]}}}
	})
	// A ResourceQuota or LimitRange change in a source namespace must reach its
	// run namespace (A60): every Agent in that namespace is enqueued, and the
	// first to reconcile mirrors it.
	byNamespace := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, o client.Object) []reconcile.Request {
		var agents assaydv1alpha1.AgentList
		if err := mgr.GetClient().List(ctx, &agents, client.InNamespace(o.GetNamespace())); err != nil {
			return nil
		}
		out := make([]reconcile.Request, 0, len(agents.Items))
		for i := range agents.Items {
			out = append(out, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&agents.Items[i])})
		}
		return out
	})
	agents := ctrl.NewControllerManagedBy(mgr).
		For(&assaydv1alpha1.Agent{}).
		Watches(&appsv1.Deployment{}, byAgentLabels).
		Watches(&corev1.ResourceQuota{}, byNamespace).
		Watches(&corev1.LimitRange{}, byNamespace)
	// Routes are watched only where they exist. Registering the watch
	// unconditionally would start a cluster-wide informer on a kind a P1
	// install has no CRD for, and the manager would fail to start — so the
	// declared-off tier would break the operator outright.
	//
	// Declared ON with the CRDs absent is design 03 §3.1's broken-install row,
	// and this refuses to start rather than reporting it per-Agent. The
	// GatewayIncompatible=CRDsAbsent condition that row calls for is NOT
	// implemented; what is implemented is fail-closed and loud, which is the
	// half of that row that matters, and this comment is where the other half
	// is owed from.
	if r.Gateway.Enabled {
		if _, err := mgr.GetRESTMapper().RESTMapping(
			schema.GroupKind{Group: gatewayv1.GroupName, Kind: "HTTPRoute"}, "v1"); err != nil {
			return fmt.Errorf("--gateway-enabled is set and this cluster serves no "+
				"gateway.networking.k8s.io/v1 HTTPRoute: %w. Install the Gateway API CRDs, or "+
				"unset gateway.enabled to run the declared-ungoverned tier (design 03 §3.1). "+
				"Starting without them would emit no route while every Agent reported nothing "+
				"wrong, which is the fail-open this refuses", err)
		}
		// And the kind of the slice's `<agent>-auth`: design 03 §3.1 says that
		// "before any AgentgatewayPolicy is emitted it must also read
		// AgentgatewayPolicy". Refused for the same reason, and before the
		// emitter needs it, because the finalizer already reads the kind:
		// against a cluster that serves none, every Agent's teardown would fail
		// its read of `<agent>-auth`, so no Agent could be deleted.
		if _, err := mgr.GetRESTMapper().RESTMapping(
			AgentgatewayPolicyGVK.GroupKind(), AgentgatewayPolicyGVK.Version); err != nil {
			return fmt.Errorf("--gateway-enabled is set and this cluster serves no "+
				"%s AgentgatewayPolicy: %w. Install the agentgateway CRDs, or unset "+
				"gateway.enabled to run the declared-ungoverned tier (design 03 §3.1). "+
				"Starting without them would leave no Agent deletable, because teardown "+
				"reads each Agent's -auth policy, which is the failure this refuses",
				compiler.PolicyAPIVersion, err)
		}
		// Policies map to their Agent by the labels routes do (§3.2), and a
		// NACK Event by the labels of the policy it names (§3.3). Both read
		// through a cache scoped to what they can map (gatewaySources), not
		// the manager's, which would hold every policy and Event cluster-wide.
		sources, err := r.gatewaySources(mgr, byAgentLabels)
		if err != nil {
			return err
		}
		agents = agents.Watches(&gatewayv1.HTTPRoute{}, byAgentLabels)
		for _, src := range sources {
			agents = agents.WatchesRawSource(src)
		}
	}
	if err := agents.Complete(r); err != nil {
		return err
	}
	// The Namespace-keyed reconciler exists so the Terminating/Deleting handler
	// runs when the source namespace holds no Agent at all (A60). It keys on the
	// pods-by label and the name shape — not on assayd.dev/run-namespace, which a
	// vCluster's host sync namespace also carries (design 26 A1).
	isRun := predicate.NewPredicateFuncs(func(o client.Object) bool {
		return strings.HasPrefix(o.GetName(), RunNamespacePrefix) &&
			o.GetLabels()[LabelPodsBy] == PodsByAgentOperator
	})
	// isRun applies to delete events too, and a namespace whose deletion
	// completed is exactly when the binding must be removed.
	return ctrl.NewControllerManagedBy(mgr).
		Named("run-namespace").
		For(&corev1.Namespace{}, builder.WithPredicates(isRun)).
		Complete(NewRunNamespaceReconciler(r))
}

// RunNamespaceReconciler runs the binding handler for a run namespace with no
// Agent to reconcile it — a namespace whose last Agent is gone, or whose
// deletion completed after the finalizer that issued it released.
type RunNamespaceReconciler struct{ agents *AgentReconciler }

// NewRunNamespaceReconciler wraps an AgentReconciler; SetupWithManager
// registers one, and tests drive it directly.
func NewRunNamespaceReconciler(agents *AgentReconciler) *RunNamespaceReconciler {
	return &RunNamespaceReconciler{agents: agents}
}

func (n *RunNamespaceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if n.agents == nil {
		return ctrl.Result{}, fmt.Errorf("run-namespace reconciler built without an AgentReconciler; " +
			"use NewRunNamespaceReconciler")
	}
	defer n.agents.lockRunNamespace(req.Name)()
	b, err := n.agents.readBinding(ctx, req.Name)
	if err != nil {
		if rerr := (*runNamespaceError)(nil); errors.As(err, &rerr) {
			// An invalid record is reported on the Agents; nothing to do here.
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	if b == nil {
		return ctrl.Result{}, nil
	}
	if err := n.agents.runNamespaceHandler(ctx, b, nil); err != nil {
		return ctrl.Result{}, err
	}
	if b.state == bindingTerminating || b.state == bindingDeleting {
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}
	return ctrl.Result{}, nil
}

func ptr[T any](v T) *T { return &v }

func port(rt *assaydv1alpha1.AgentRuntime) int32 {
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
