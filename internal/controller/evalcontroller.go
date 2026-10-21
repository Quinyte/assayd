// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlcontroller "sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/log"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/evalclient"
	"github.com/Quinyte/assayd/internal/evalsuite"
)

// The eval controller, design 16 §1.1 ("Who runs the eval", "The two
// controllers", "How a run works"). It runs a gated Agent's EvalSuite against
// the candidate revision, one A2A SendMessage per case, and writes
// status.eval and nothing else. The Agent reconciler reads the verdict and
// promotes on it; this controller never promotes.
//
// It waits on every Create or Lock recorded in status.auth.transaction but
// one: a J2 or K2 Lock that can finish only through a promotion, which meets
// clauses 1 to 4 of the exemption from status (exemptTransaction) and clause
// 5 from the Gateway and two policy lists (clause5), evaluated last. A refused
// Adopt never holds it (design 16 §1.1, "What the rule does with each record").

const (
	// evalWriteBound bounds a claim, and a record including its retries
	// (design 16 §1.1 step 10).
	evalWriteBound = 5 * time.Second
	// evalSendBackstop is the requeue a pass that sent a case returns. The
	// next pass is driven by the record's own status write; this is the
	// backstop for one that is lost, and it is never Requeue: true, whose
	// rate-limited backoff grows and is not reset while a run goes on (step 9).
	evalSendBackstop = 5 * time.Second
	// MaxEndpointPathLength bounds status.eval.endpointPath, in characters, as
	// the CRD's MaxLength counts them. A card is written
	// by the agent, and the path is taken from it verbatim: unbounded, it would
	// be bounded only by the card's own size, a megabyte, and land in status
	// on every run start. 1024 is the CRD's MaxLength for the field: far above
	// any route an A2A agent serves at (the spec's examples are a few path
	// segments), and small enough that a run-start record cannot approach the
	// object's size limit. A longer path is refused at run start, under
	// EvalBindingUnsupported, before anything is written.
	MaxEndpointPathLength = 1024
)

// evalWait names why a pass returns without sending, so that every requeue
// goes through one field (EvalReconciler.RequeueAfter) and the zero value of
// that field resolves to the constant §1.1 gives each branch.
type evalWait int

const (
	// waitTransaction: a Create or Lock is recorded that is not exempt, or
	// clause 5 holds an exempt one. AuthProbeInterval, the rate design 03
	// probes at.
	waitTransaction evalWait = iota
	// waitFetchFailed: the run-start card fetch did not succeed. Nothing in
	// Agent status changes when it fails, so no event would wake the pass:
	// CardRetryInterval, design 02's interval for a failed card fetch (A15).
	waitFetchFailed
	// waitPaused: the candidate is not available. Its availability reaches
	// this controller as an Agent event anyway: CardDriftInterval.
	waitPaused
	// waitRefused: a run-start refusal that read a card (a digest mismatch, or
	// no usable interface). CardDriftInterval: a card is not a per-reconcile
	// input (card.go).
	waitRefused
	// waitHeld: a candidate held with a final verdict, or by anything else
	// this controller does not wait on actively — an I1 hold, no card
	// recorded, a suite that does not exist, a pin naming the candidate. CardDriftInterval: the bound on
	// noticing a suite edit, which nothing watches (A4).
	waitHeld
	// waitReadBack: a write did not come back. CardDriftInterval, and nothing
	// is sent (step 11).
	waitReadBack
	// waitSent: a pass that sent a case. The 5 s backstop (step 9).
	waitSent
)

// EvalReconciler is the eval controller. Build it with NewEvalReconciler.
type EvalReconciler struct {
	// Client is the manager's cached client: the candidate's Service and
	// Deployment, as the Agent reconciler reads them.
	client.Client
	// uncached reads the Agent before every write, because the manager's
	// cache can lag this controller's own last patch, and the EvalSuite,
	// which no informer watches. Required. What keeps a recorded case from
	// being sent again is the optimistic lock on the claim: a claim built
	// from a stale read is refused with Conflict. The live read is what keeps
	// that from happening on every pass after a write.
	uncached client.Reader
	// EvalSuiteInstalled is the EvalSuite detector the Agent reconciler holds.
	EvalSuiteInstalled func() bool
	// Gateway is the assayd Gateway: the I1 hold reads whether it is enabled,
	// and clause 5 reads it, and the Agent's hostname on it, through
	// policyReads.
	Gateway GatewayConfig
	// A2A is the eval client, built once and reused, so every send shares one
	// transport. Its HTTP is the test seam, as AgentReconciler.CardClient is.
	A2A *evalclient.Client
	// RequeueAfter is every requeue this controller returns. Zero — what the
	// operator runs with — resolves to the constant each branch names
	// (requeueFor); a test sets it to shorten them all, as AuthDeadline does
	// for the -auth transaction's deadline.
	RequeueAfter time.Duration
	// CardFetchTimeout bounds the run-start card fetch. Zero means
	// DefaultCardFetchTimeout.
	CardFetchTimeout time.Duration
	// BeforeClaimCheck, when set, runs after a pass's live read and before its
	// ctx.Err() check, the last thing before a claim. Only tests set it: it is
	// how a test holds a pass where a shutdown must stop it (design 16 T35).
	BeforeClaimCheck func(types.NamespacedName)
}

// NewEvalReconciler builds the eval controller. The uncached reader is
// required, unlike the Agent reconciler's: every write here is preceded by a
// live read of the Agent (design 16 §1.1, "The two controllers"). A cached
// read can lag this controller's own last patch, and every claim built from
// it would then be refused by the optimistic lock, losing the pass.
func NewEvalReconciler(c client.Client, uncached client.Reader, evalSuiteInstalled func() bool,
	gateway GatewayConfig) (*EvalReconciler, error) {
	switch {
	case c == nil:
		return nil, fmt.Errorf("eval controller: client is required")
	case uncached == nil:
		return nil, fmt.Errorf("eval controller: an uncached reader is required — it reads the Agent " +
			"before every write, because a cached read can lag its own last patch and a case already " +
			"recorded would read as unsent (design 16 §1.1 step 10)")
	case evalSuiteInstalled == nil:
		return nil, fmt.Errorf("eval controller: evalSuiteInstalled is required — whether the " +
			"EvalSuite CRD is present decides whether rollouts are eval-gated (ADR-0006)")
	}
	return &EvalReconciler{Client: c, uncached: uncached, EvalSuiteInstalled: evalSuiteInstalled,
		Gateway: gateway, A2A: evalclient.New()}, nil
}

// policyReads is this controller's gatewayPolicyReads: its client, its
// uncached reader and its Gateway, built per pass. Clause 5 reads the Gateway
// and the two policy lists through it, uncached, and `<agent>-serving`'s
// labels through the cached client (servingRouteLabels).
func (r *EvalReconciler) policyReads() gatewayPolicyReads {
	return gatewayPolicyReads{Client: r.Client, uncached: r.uncached, Gateway: r.Gateway}
}

// requeueFor resolves RequeueAfter for one branch.
func (r *EvalReconciler) requeueFor(w evalWait) time.Duration {
	if r.RequeueAfter > 0 {
		return r.RequeueAfter
	}
	switch w {
	case waitTransaction:
		return AuthProbeInterval
	case waitFetchFailed:
		return CardRetryInterval
	case waitSent:
		return evalSendBackstop
	default:
		return CardDriftInterval
	}
}

func (r *EvalReconciler) wait(w evalWait) (ctrl.Result, error) {
	return ctrl.Result{RequeueAfter: r.requeueFor(w)}, nil
}

// SetupWithManager registers the controller: For Agent with no generation
// predicate, so its own status patches enqueue the next pass (step 9); two
// workers, so at most two cases are in flight across the cluster (Q1); and
// leader-gated, controller-runtime's default, which nothing here overrides.
// No EvalSuite watch: suites are read uncached by get, so no informer starts
// and an absent CRD cannot fail manager start.
func (r *EvalReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("agent-eval").
		For(&assaydv1alpha1.Agent{}).
		WithOptions(ctrlcontroller.Options{MaxConcurrentReconciles: 2}).
		Complete(r)
}

// +kubebuilder:rbac:groups=assayd.dev,resources=agents,verbs=get;list;watch
// +kubebuilder:rbac:groups=assayd.dev,resources=agents/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=assayd.dev,resources=evalsuites,verbs=get

// Reconcile is one pass: read live, check the shared predicates, then either
// clear a superseded verdict, start a run, record an orphaned claim, or claim,
// send and record one case.
func (r *EvalReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	agent, err := r.live(ctx, req.NamespacedName)
	if err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !agent.DeletionTimestamp.IsZero() || agent.Spec.Runtime == nil || len(agent.Spec.Gates) == 0 ||
		!r.EvalSuiteInstalled() {
		return ctrl.Result{}, nil
	}
	// The Agent reconciler has not recorded what the current spec mints; its
	// status write will enqueue this Agent again.
	if !evalGenerationCurrent(agent) {
		return ctrl.Result{}, nil
	}
	st := &agent.Status
	// Every eval write waits on the transaction predicate, the clear below
	// and an Interrupted record included (design 16 §1.1, "The case in flight
	// when a transaction begins"). A Lock exempt by clauses 1 to 4 lets the
	// pass go on to clause 5, read last, below; every other recorded Create
	// or Lock holds the pass here, before anything is read or written.
	exempt := false
	if tx := recordedTransaction(st); tx != nil {
		var why string
		if exempt, why = exemptTransaction(st); !exempt {
			logger.V(1).Info("an -auth transaction is recorded and is not exempt; no case is claimed "+
				"while it is", "kind", tx.Kind, "stage", tx.Stage, "why", why)
			return r.wait(waitTransaction)
		}
	}
	// The clear of a superseded run waits for the exempt Lock to end too: it
	// is an eval write that no claim needs, and clause 5 is read only on a
	// pass that has a pair to record it for.
	if !exempt {
		if done, err := r.clearSuperseded(ctx, agent); done || err != nil {
			return ctrl.Result{}, err
		}
	}
	if !candidatePending(st) {
		return ctrl.Result{}, nil
	}
	cand, candDigest := st.CandidateRevision, st.CandidateRevisionDigest
	runNS := RunNamespaceName(agent.Namespace)
	available, err := workloadAvailableIn(ctx, r.Client, agent, runNS, cand, candDigest)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !available {
		return r.wait(waitPaused)
	}
	pinned := agent.Spec.Release != nil && agent.Spec.Release.TargetRevisionDigest != ""
	if i1Hold(r.Gateway, st, desiredAuth(agent, runNS), pinned) != "" || !hasCardFor(st, candDigest) {
		return r.wait(waitHeld)
	}

	var suite assaydv1alpha1.EvalSuite
	if err := r.uncached.Get(ctx, types.NamespacedName{Namespace: agent.Namespace,
		Name: agent.Spec.Gates[0].EvalSuiteRef}, &suite); err != nil {
		if apierrors.IsNotFound(err) {
			// The Agent reconciler reports EvalSuiteNotFound; a suite created
			// later is noticed on this requeue, since nothing watches suites.
			return r.wait(waitHeld)
		}
		return ctrl.Result{}, fmt.Errorf("read EvalSuite %s/%s: %w", agent.Namespace,
			agent.Spec.Gates[0].EvalSuiteRef, err)
	}
	sd, err := evalsuite.Digest(suite.Spec)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("digest EvalSuite %s/%s: %w", suite.Namespace, suite.Name, err)
	}
	// A pinned rollback is promoted, not evaluated (Q5 (a)). The Agent
	// reconciler's status write when it promotes, or when the pin goes,
	// brings the next pass.
	if pinned, err := pinnedRollbackCandidate(ctx, r.Client, agent); err != nil {
		return ctrl.Result{}, fmt.Errorf("decide whether the pin on %s is admitted: %w", req.NamespacedName, err)
	} else if pinned {
		return r.wait(waitHeld)
	}
	// Admission refuses a threshold this cannot read, so it can fail only on
	// a suite stored before the rule; nothing is written for one.
	th, err := evalsuite.Threshold(suite.Spec)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("EvalSuite %s/%s: %w", suite.Namespace, suite.Name, err)
	}
	run := evalRun{agent: agent, suite: &suite, suiteDigest: sd, threshold: th, runNS: runNS,
		revision: cand, revisionDigest: candDigest}

	// Clause 5, last, on a pass that met clauses 1 to 4 and every other send
	// predicate. Its hold is the second reason blocked can carry; a run-start
	// refusal outranks it, but only on a pass that would start a run, and a
	// pass under a hold starts none, so the card is not fetched while one
	// stands (§1.1, "`blocked`: one slot, one computation").
	e := st.Eval
	recorded := e != nil && e.RevisionDigest == candDigest && e.SuiteDigest == sd
	var hold *assaydv1alpha1.EvalBlocked
	if exempt {
		var readErr error
		if hold, readErr = r.clause5(ctx, agent, runNS); readErr != nil {
			if res, done, err := r.syncBlocked(ctx, &run, hold); done || err != nil {
				return res, err
			}
			logger.Info("clause 5 could not be read; the hold is recorded and no case is claimed",
				"error", readErr.Error())
			return ctrl.Result{}, readErr
		}
	}

	switch {
	case recorded && (e.Verdict == assaydv1alpha1.EvalVerdictPassed || e.Verdict == assaydv1alpha1.EvalVerdictFailed):
		// A final verdict for this pair. The Agent reconciler promotes on a
		// Passed one, whatever blocked says; a Failed one holds the candidate,
		// and a suite edit, noticed on this requeue, gives a new pair (A4).
		if res, done, err := r.syncBlocked(ctx, &run, hold); done || err != nil {
			return res, err
		}
		return r.wait(waitHeld)
	case pairFailed(failedOf(e), candDigest, sd):
		// No run starts for a failed pair again (step 7, Q3 (a)).
		if res, done, err := r.syncBlocked(ctx, &run, hold); done || err != nil {
			return res, err
		}
		return r.wait(waitHeld)
	case hold != nil:
		// Nothing is claimed, and no run starts, while the hold stands. The
		// pass comes round at the rate design 03 probes at.
		if res, done, err := r.syncBlocked(ctx, &run, hold); done || err != nil {
			return res, err
		}
		logger.V(1).Info("clause 5 holds the exempt Lock; no case is claimed", "hold", hold.Message)
		return r.wait(waitTransaction)
	case !recorded || e.Verdict != assaydv1alpha1.EvalVerdictRunning || e.EndpointPath == "" || e.CardDigest == "":
		// A Running record with no endpoint or card digest is one a CRD
		// pruned in part: the run-start read-back refused it, and nothing
		// may be sent on it. The run starts again, and its read-back fails
		// again, until the CRD keeps the whole record (step 11).
		if res, done, err := r.startRun(ctx, &run); done || err != nil {
			return res, err
		}
	default:
		if res, done, err := r.syncBlocked(ctx, &run, nil); done || err != nil {
			return res, err
		}
	}
	return r.nextCase(ctx, &run)
}

// evalRun is one pass's view of the run it acts on.
type evalRun struct {
	agent                    *assaydv1alpha1.Agent
	suite                    *assaydv1alpha1.EvalSuite
	suiteDigest              string
	threshold                int64
	runNS                    string
	revision, revisionDigest string
}

func (r *EvalReconciler) live(ctx context.Context, key types.NamespacedName) (*assaydv1alpha1.Agent, error) {
	var a assaydv1alpha1.Agent
	if err := r.uncached.Get(ctx, key, &a); err != nil {
		return nil, err
	}
	return &a, nil
}

// patchEval writes agent's status.eval, as edit leaves it, by a JSON merge
// patch under the optimistic lock, and leaves agent as the API server returned
// it. Only status.eval differs between the base and the patched object, so
// that is all the patch carries.
func (r *EvalReconciler) patchEval(ctx context.Context, agent *assaydv1alpha1.Agent,
	edit func(*assaydv1alpha1.EvalStatus) *assaydv1alpha1.EvalStatus) error {
	base := agent.DeepCopy()
	var cur *assaydv1alpha1.EvalStatus
	if agent.Status.Eval != nil {
		cur = agent.Status.Eval.DeepCopy()
	}
	agent.Status.Eval = edit(cur)
	return r.Status().Patch(ctx, agent, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}))
}

// clearSuperseded is the human's decision of 2026-10-18 (#111): "When the
// spec moves away from the failed revision, the eval controller clears the
// verdict and cases[] of the superseded run and keeps failed[]." Re-applying
// the failed revision then reads EvalPairAlreadyFailed rather than EvalFailed.
// score and at go with the verdict, so a stale score is not shown beside
// another revision.
//
// The trigger is exactly: a Failed verdict is recorded, and the recorded
// revision digest differs from the revision the Agent reconciler has recorded
// as desired — the candidate while one is pending, otherwise the active
// revision — with observedGeneration current (checked by the caller).
//
// A Passed verdict is never cleared: #111 covers the failed revision only.
// Clearing a Passed one would erase the evidence for a revision that may
// still serve, or still promote on it — re-applied, it would be sent its
// cases again — and the serving revision would read
// ActiveRevisionNotEvaluated for a revision that passed. A Running record is
// not touched either: the next run start replaces it. The clear is an eval
// write, so it waits on the no-transaction predicate like every other, and
// the Agent reconciler, which never writes status.eval, cannot fight it.
func (r *EvalReconciler) clearSuperseded(ctx context.Context, agent *assaydv1alpha1.Agent) (bool, error) {
	e := agent.Status.Eval
	if e == nil || e.Verdict != assaydv1alpha1.EvalVerdictFailed {
		return false, nil
	}
	desired := agent.Status.ActiveRevisionDigest
	if candidatePending(&agent.Status) {
		desired = agent.Status.CandidateRevisionDigest
	}
	if e.RevisionDigest == desired {
		return false, nil
	}
	err := r.patchEval(ctx, agent, func(e *assaydv1alpha1.EvalStatus) *assaydv1alpha1.EvalStatus {
		e.Verdict, e.Cases, e.Score, e.At = "", nil, "", nil
		return e
	})
	if apierrors.IsConflict(err) {
		return true, nil // the other writer's event brings the next pass
	}
	if err != nil {
		return true, fmt.Errorf("clear the superseded run of %s: %w", e.RevisionDigest, err)
	}
	log.FromContext(ctx).Info("the spec moved away from a revision whose run failed; its run's "+
		"verdict and cases are cleared, and failed[] is kept", "revisionDigest", e.RevisionDigest)
	return true, nil
}

// syncBlocked writes status.eval.blocked and blockedFor when the value this
// pass computed differs from the stored one, and reads both back (step 11).
// blocked is the pass's one computation, in §1.1's precedence: a run-start
// refusal on a pass that would start a run (startRun), otherwise clause 5's
// hold on an exempt Lock, otherwise empty. It is called only on a pass on
// which every shared send predicate holds, the transaction predicate with its
// exemption included, so the stored value is replaced only by one evaluated
// for the current pair. A pass that waits on a transaction that is not exempt
// writes nothing at all, blocked included: that is what the wait is for, and
// the gate branch answers such a transaction before it reads blocked. done is
// true when the pass must return res.
func (r *EvalReconciler) syncBlocked(ctx context.Context, run *evalRun, blocked *assaydv1alpha1.EvalBlocked) (
	ctrl.Result, bool, error) {
	want := run.blockedFor()
	e := run.agent.Status.Eval
	if e != nil && equalBlocked(e.Blocked, blocked) && equalBlockedFor(e.BlockedFor, want) {
		return ctrl.Result{}, false, nil
	}
	err := r.patchEval(ctx, run.agent, func(e *assaydv1alpha1.EvalStatus) *assaydv1alpha1.EvalStatus {
		if e == nil {
			e = &assaydv1alpha1.EvalStatus{}
		}
		e.Blocked, e.BlockedFor = blocked.DeepCopy(), want
		return e
	})
	if apierrors.IsConflict(err) {
		return ctrl.Result{}, true, nil
	}
	if err != nil {
		return ctrl.Result{}, true, fmt.Errorf("write status.eval.blocked: %w", err)
	}
	if got := run.agent.Status.Eval; got == nil || !equalBlocked(got.Blocked, blocked) ||
		!equalBlockedFor(got.BlockedFor, want) {
		return r.readBackFailed(ctx, "blocked and blockedFor")
	}
	return ctrl.Result{}, false, nil
}

// blockedFor is what this pass evaluated blocked for: the pair, and the
// deadline of the Lock recorded now, read as the gate branch reads it.
func (run *evalRun) blockedFor() *assaydv1alpha1.EvalBlockedFor {
	return &assaydv1alpha1.EvalBlockedFor{RevisionDigest: run.revisionDigest, SuiteDigest: run.suiteDigest,
		LockDeadline: recordedLockDeadline(&run.agent.Status)}
}

func equalBlocked(a, b *assaydv1alpha1.EvalBlocked) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func equalBlockedFor(a, b *assaydv1alpha1.EvalBlockedFor) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if a.RevisionDigest != b.RevisionDigest || a.SuiteDigest != b.SuiteDigest {
		return false
	}
	if a.LockDeadline == nil || b.LockDeadline == nil {
		return a.LockDeadline == nil && b.LockDeadline == nil
	}
	return a.LockDeadline.Equal(b.LockDeadline)
}

// readBackFailed is a write whose fields did not come back: the installed
// Agent CRD predates them and prunes them without an error. Nothing more is
// written or sent on this pass, and the Agent reconciler's schema check names
// the cause (EvalRecordNotKept).
func (r *EvalReconciler) readBackFailed(ctx context.Context, what string) (ctrl.Result, bool, error) {
	log.FromContext(ctx).Info("status.eval was written and did not come back; the installed Agent CRD "+
		"prunes it, so nothing is sent. Apply this release's charts/assayd/crds/ with kubectl; helm "+
		"upgrade never updates a chart's crds/", "fields", what)
	res, err := r.wait(waitReadBack)
	return res, true, err
}

// startRun is step 1 and step 2: the run-start card fetch, and the patch that
// replaces the previous pair's record. done is true when the pass must return
// res; otherwise the run has started and the pass goes on to its first case.
func (r *EvalReconciler) startRun(ctx context.Context, run *evalRun) (ctrl.Result, bool, error) {
	addr, err := r.candidateAddress(ctx, run, cardPath(run.agent))
	if err != nil {
		// Every other way out of a run start writes blocked once, with the
		// pass's own value. This one would write nothing, and a clause-5 hold
		// this pass found gone would go on naming a cause that is gone for as
		// long as the address cannot be used; so it is cleared here, the
		// pass's one blocked write.
		if e := run.agent.Status.Eval; e != nil && e.Blocked != nil && e.Blocked.Reason == ReasonEvalWaitingForAuth {
			if res, done, serr := r.syncBlocked(ctx, run, nil); done || serr != nil {
				if serr == nil {
					serr = err
				}
				return res, true, serr
			}
		}
		return ctrl.Result{}, true, err
	}
	path, cardDigest, refusal, wait := r.runStartCard(ctx, run, addr)
	if refusal != nil {
		if res, done, err := r.syncBlocked(ctx, run, refusal); done || err != nil {
			return res, true, err
		}
		res, err := r.wait(wait)
		return res, true, err
	}
	want := &assaydv1alpha1.EvalStatus{
		Suite: run.suite.Name, Revision: run.revision, RevisionDigest: run.revisionDigest,
		SuiteDigest: run.suiteDigest, EndpointPath: path, CardDigest: cardDigest,
		Verdict: assaydv1alpha1.EvalVerdictRunning, BlockedFor: run.blockedFor(),
	}
	// One patch accounts for every field: the new pair's record is set;
	// score, at and cases[], which the previous pair's run produced, are
	// cleared; failed[] is kept; blocked is computed empty for a run that
	// starts, and blockedFor names what it was computed for. Nothing in it
	// changes from pass to pass, so on a CRD that prunes it a repeated write
	// stores no change and enqueues nothing (step 11).
	err = r.patchEval(ctx, run.agent, func(e *assaydv1alpha1.EvalStatus) *assaydv1alpha1.EvalStatus {
		next := want.DeepCopy()
		if e != nil {
			next.Failed = e.Failed
		}
		return next
	})
	if apierrors.IsConflict(err) {
		return ctrl.Result{}, true, nil
	}
	if err != nil {
		return ctrl.Result{}, true, fmt.Errorf("start the run of %s against suite %s: %w",
			run.revisionDigest, run.suiteDigest, err)
	}
	got := run.agent.Status.Eval
	if got == nil || got.Verdict != want.Verdict || got.Suite != want.Suite || got.Revision != want.Revision ||
		got.RevisionDigest != want.RevisionDigest || got.SuiteDigest != want.SuiteDigest ||
		got.EndpointPath != want.EndpointPath || got.CardDigest != want.CardDigest ||
		got.Blocked != nil || !equalBlockedFor(got.BlockedFor, want.BlockedFor) {
		return r.readBackFailed(ctx, "the run-start record")
	}
	log.FromContext(ctx).Info("eval run started", "revision", run.revision, "suite", run.suite.Name,
		"suiteDigest", run.suiteDigest, "endpointPath", path)
	return ctrl.Result{}, false, nil
}

// candidateAddress is where every request of this pass goes: the candidate
// revision's Service, by its ClusterIP and spec.runtime.port, as the card
// fetch derives it (step 3). evalclient.Check, the one validator of it, must
// admit it with path before anything is fetched or claimed (#105 item 12): an
// address that is not an IP and a port is the operator's fault, not the
// candidate's, so it records no outcome — failed[] would make one final — and
// the pass returns the error for controller-runtime's backoff. No condition
// says so; that is owed to design 16's amendment (#113).
func (r *EvalReconciler) candidateAddress(ctx context.Context, run *evalRun, path string) (string, error) {
	var svc corev1.Service
	key := types.NamespacedName{Namespace: run.runNS, Name: WorkloadName(run.agent.Name, run.revision)}
	if err := r.Get(ctx, key, &svc); err != nil {
		log.FromContext(ctx).Error(err, "the candidate's Service cannot be read; nothing is fetched, "+
			"claimed or sent", "service", key)
		return "", fmt.Errorf("read the candidate's Service %s: %w", key, err)
	}
	addr := net.JoinHostPort(svc.Spec.ClusterIP, strconv.Itoa(int(port(run.agent.Spec.Runtime))))
	if err := evalclient.Check(evalclient.Case{Address: addr, Path: path}); err != nil {
		log.FromContext(ctx).Error(err, "the candidate's address is not one a case can be sent to; "+
			"nothing is fetched, claimed or sent", "service", key)
		return "", err
	}
	return addr, nil
}

// runStartCard fetches the candidate's card through the eval client and
// takes the endpoint path from it (step 2). It returns the path and the card
// digest, or the refusal to write to blocked with the wait that goes with it.
func (r *EvalReconciler) runStartCard(ctx context.Context, run *evalRun, addr string) (
	string, string, *assaydv1alpha1.EvalBlocked, evalWait) {
	// The messages name no pair: blockedFor records it, and the gate branch
	// names it beside the message.
	body, class := r.fetchCard(ctx, addr, cardPath(run.agent))
	var card struct {
		// Decoded into evalclient.Interface, which carries the tenant, and
		// not into the card fetch's agentInterface, which does not: a tenant
		// refusal built from that type could never fire (#108).
		SupportedInterfaces []evalclient.Interface `json:"supportedInterfaces"`
	}
	// A body that does not parse is a fetch that did not succeed, as §1.1
	// step 2 lists it, "where no digest was compared": retried at
	// CardRetryInterval, because nothing in Agent status changes to wake it.
	if class == "" && !json.Valid(body) {
		class = "the body is not JSON"
	}
	if class != "" {
		// Nothing was compared, so the message names no mismatch (A15).
		return "", "", &assaydv1alpha1.EvalBlocked{Reason: ReasonEvalAwaitingCard, Message: fmt.Sprintf(
			"the run-start card fetch did not succeed: %s. Nothing was compared with the card "+
				"status.cards[] records, and no run starts until a fetch succeeds; it is retried every %s",
			class, CardRetryInterval)}, waitFetchFailed
	}
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])
	if c := cardEntry(&run.agent.Status, run.revisionDigest); c == nil || c.Digest != digest {
		recorded := ""
		if c != nil {
			recorded = c.Digest
		}
		return "", "", &assaydv1alpha1.EvalBlocked{Reason: ReasonEvalAwaitingCard, Message: fmt.Sprintf(
			"the card the candidate served at run start has digest %s, and status.cards[] records %q "+
				"for it. The eval takes its endpoint only from the card the operator recorded, so no run "+
				"starts until the two agree", shortDigest(digest), shortDigest(recorded))}, waitRefused
	}
	// Decoded only once its digest matches the card the operator recorded,
	// which the Agent reconciler has parsed as JSON. A decode failure here is
	// an interface entry of the wrong JSON type — a number for a tenant, say
	// — which the card fetch's own type does not read: a property of the card,
	// as stable as it, and not a fetch to retry.
	if err := json.Unmarshal(body, &card); err != nil {
		return "", "", &assaydv1alpha1.EvalBlocked{Reason: ReasonEvalBindingUnsupported, Message: bindingRefusal(
			"its supportedInterfaces[] do not decode as A2A 1.0 interfaces: an entry carries a field of " +
				"the wrong JSON type. Serve a card whose interfaces are well-typed").Error()}, waitRefused
	}
	path, err := evalclient.EndpointPath(card.SupportedInterfaces)
	if err == nil {
		err = checkEndpointPath(path)
	}
	if err != nil {
		return "", "", &assaydv1alpha1.EvalBlocked{Reason: ReasonEvalBindingUnsupported,
			Message: err.Error()}, waitRefused
	}
	return path, digest, nil, 0
}

// checkEndpointPath refuses, under EvalBindingUnsupported, an endpoint path
// the eval would record and send to but should not (#105 item 9): longer than
// MaxEndpointPathLength, or with no leading "/", which a relative card URL
// gives. The bound is on the endpoint, the trimmed path with "/message:send"
// appended. A path written with a percent-escape or a byte outside
// evalclient's allowlist is refused before this, by evalclient.EndpointPath,
// which has the URL as the card wrote it.
func checkEndpointPath(path string) error {
	switch {
	case utf8.RuneCountInString(path) > MaxEndpointPathLength:
		return bindingRefusal(fmt.Sprintf("the endpoint path its first HTTP+JSON 1.0 interface names is "+
			"%d characters long, and the eval records and sends to one of at most %d. Serve the "+
			"interface at a shorter path", utf8.RuneCountInString(path), MaxEndpointPathLength))
	case !strings.HasPrefix(path, "/"):
		return bindingRefusal("its first HTTP+JSON 1.0 interface's url is not an absolute URL with a " +
			"path, so the endpoint the eval would take from it does not begin with \"/\". Serve the " +
			"interface at an absolute URL")
	}
	return nil
}

// bindingRefusal is an EvalBindingUnsupported refusal of the operator's own,
// beside evalclient.EndpointPath's. Like those, it never quotes the card's
// URL: that is an address the agent chose.
func bindingRefusal(msg string) error {
	return errors.New("the candidate's card cannot be sent a case: " + msg)
}

func shortDigest(d string) string {
	if len(d) > 12 {
		return d[:12]
	}
	return d
}

// fetchCard is the run-start card fetch, through the eval client: no
// redirect is followed and no environment proxy is used (step 2). It returns
// the body, or the class of the failure, which is what the blocked message
// names: a class is the same on every pass, where an error's raw text varies
// and would rewrite blocked on each one.
func (r *EvalReconciler) fetchCard(ctx context.Context, addr, path string) ([]byte, string) {
	timeout := r.CardFetchTimeout
	if timeout <= 0 {
		timeout = DefaultCardFetchTimeout
	}
	fctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(fctx, http.MethodGet, cardURL("http", addr, path), nil)
	if err != nil {
		return nil, "no request could be built for the card path"
	}
	resp, err := r.A2A.HTTP.Do(req)
	if err != nil {
		if fctx.Err() != nil {
			return nil, fmt.Sprintf("no answer within %s", timeout)
		}
		return nil, "the connection was refused or failed"
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Sprintf("the card path answered status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, evalclient.MaxBodyBytes+1))
	switch {
	case err != nil:
		return nil, "the body was cut short"
	case len(body) > evalclient.MaxBodyBytes:
		return nil, "the body is over 1 MiB"
	}
	return body, ""
}

// nextCase is step 10 from the claim on: record Interrupted for a claim found
// with no outcome, or claim the next case by id, send it, and record its
// outcome, with the verdict in the last case's record (step 7).
func (r *EvalReconciler) nextCase(ctx context.Context, run *evalRun) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	key := client.ObjectKeyFromObject(run.agent)
	ids := suiteIDs(run.suite)
	cases := run.agent.Status.Eval.Cases

	// A claim found with no outcome: the eval controller stopped between
	// claiming and recording without a graceful stop, or the claim's response
	// was lost. Nobody knows whether the candidate received it, so it is never
	// sent again (Q11 (a)).
	for _, id := range ids {
		if c := caseRecord(cases, id); c != nil && c.Outcome == assaydv1alpha1.EvalCaseSending {
			err := r.patchEval(ctx, run.agent, func(e *assaydv1alpha1.EvalStatus) *assaydv1alpha1.EvalStatus {
				return run.withOutcome(e, id, c.Claim, assaydv1alpha1.EvalCaseFailed, string(evalclient.ReasonInterrupted))
			})
			if apierrors.IsConflict(err) {
				return ctrl.Result{}, nil
			}
			if err != nil {
				return ctrl.Result{}, fmt.Errorf("record case %s as Interrupted: %w", id, err)
			}
			logger.Info("a claimed case had no outcome; it is recorded Interrupted and not sent again",
				"case", id)
			return r.wait(waitSent)
		}
	}

	var next *assaydv1alpha1.EvalCase
	for _, id := range ids {
		if caseRecord(cases, id) == nil {
			next = suiteCase(run.suite, id)
			break
		}
	}
	if next == nil {
		// Every case has an outcome, and the record that wrote the last one
		// wrote the verdict with it; nothing is left to send.
		return ctrl.Result{}, nil
	}
	// Check before the claim, inside candidateAddress: a claim followed by
	// Send's error would be left with no outcome and recorded Interrupted,
	// failing the candidate for the operator's fault.
	addr, err := r.candidateAddress(ctx, run, run.agent.Status.Eval.EndpointPath)
	if err != nil {
		return ctrl.Result{}, err
	}
	cs := evalclient.Case{Address: addr, Path: run.agent.Status.Eval.EndpointPath, Message: next.Message,
		Expect: next.Expect, TimeoutSeconds: run.suite.Spec.CaseTimeoutSeconds}

	if r.BeforeClaimCheck != nil {
		r.BeforeClaimCheck(key)
	}
	// Once shutdown has begun, nothing is claimed. A claim that starts always
	// completes, because it and everything after it run on a context shutdown
	// does not cancel.
	if ctx.Err() != nil {
		return ctrl.Result{}, nil
	}
	bg := context.WithoutCancel(ctx)

	nonce := newClaimNonce()
	claimCtx, cancel := context.WithTimeout(bg, evalWriteBound)
	err = r.patchEval(claimCtx, run.agent, func(e *assaydv1alpha1.EvalStatus) *assaydv1alpha1.EvalStatus {
		e.Cases = withCase(e.Cases, assaydv1alpha1.EvalCaseRecord{ID: next.ID,
			Outcome: assaydv1alpha1.EvalCaseSending, Claim: nonce})
		return e
	})
	cancel()
	if apierrors.IsConflict(err) {
		// A lost race costs a pass, never a second request.
		return ctrl.Result{}, nil
	}
	if err != nil {
		// Possibly committed with its response lost: send nothing. The next
		// pass finds the claim with no outcome and records Interrupted.
		return ctrl.Result{}, fmt.Errorf("claim case %s: %w", next.ID, err)
	}
	if c := caseRecord(run.agent.Status.Eval.Cases, next.ID); c == nil ||
		c.Outcome != assaydv1alpha1.EvalCaseSending || c.Claim != nonce {
		res, _, err := r.readBackFailed(ctx, "the claim of case "+next.ID)
		return res, err
	}

	// The send is synchronous, inside the pass: controller-runtime's Start
	// waits for its workers and would not wait for a goroutine. Send bounds it
	// by caseTimeoutSeconds.
	out, err := r.A2A.Send(bg, cs)
	if err != nil {
		// Send returns an error exactly when evalclient.Check refuses the
		// case (Send's doc, pinned by evalclient's
		// TestCheckRefusesAnAddressThatIsNotAnIPAndAPort), and candidateAddress
		// had Check admit this address and path on this pass, so no error can
		// come back here. The signature returns one, so it is handled: no
		// outcome is recorded, and the claim reads Interrupted next.
		logger.Error(err, "the case could not be sent; no outcome is recorded", "case", next.ID)
		return ctrl.Result{}, err
	}
	outcome := assaydv1alpha1.EvalCaseFailed
	if out.Passed {
		outcome = assaydv1alpha1.EvalCasePassed
	}
	if err := r.record(bg, run, next.ID, nonce, outcome, string(out.Reason)); err != nil {
		return ctrl.Result{}, err
	}
	return r.wait(waitSent)
}

// record writes a sent case's outcome in place of its claim, with the verdict
// when it is the last. On Conflict it re-reads live and re-applies, but only
// while the claim stands — the same pair, this nonce at this id — and its
// outcome is still Sending, so an old leader finishing its send during a
// handover never overwrites an Interrupted a new leader wrote (A6). The whole
// of it, retries included, is bounded by evalWriteBound.
func (r *EvalReconciler) record(bg context.Context, run *evalRun, id, nonce string,
	outcome assaydv1alpha1.EvalCaseOutcome, reason string) error {
	ctx, cancel := context.WithTimeout(bg, evalWriteBound)
	defer cancel()
	logger := log.FromContext(ctx)
	agent := run.agent
	for {
		err := r.patchEval(ctx, agent, func(e *assaydv1alpha1.EvalStatus) *assaydv1alpha1.EvalStatus {
			return run.withOutcome(e, id, nonce, outcome, reason)
		})
		if err == nil {
			return nil
		}
		if !apierrors.IsConflict(err) {
			return fmt.Errorf("record case %s: %w", id, err)
		}
		live, err := r.live(ctx, client.ObjectKeyFromObject(agent))
		if err != nil {
			return fmt.Errorf("re-read the Agent to record case %s: %w", id, err)
		}
		e := live.Status.Eval
		c := (*assaydv1alpha1.EvalCaseRecord)(nil)
		if e != nil && e.RevisionDigest == run.revisionDigest && e.SuiteDigest == run.suiteDigest {
			c = caseRecord(e.Cases, id)
		}
		if c == nil || c.Claim != nonce || c.Outcome != assaydv1alpha1.EvalCaseSending {
			logger.Info("the claim no longer stands; the outcome is dropped", "case", id)
			return nil
		}
		agent = live
	}
}

// withOutcome is e with case id's entry recorded, and, when every suite id
// then has an outcome, the verdict, score and time written in the same patch
// and a Failed pair appended to failed[] (step 7).
func (run *evalRun) withOutcome(e *assaydv1alpha1.EvalStatus, id, claim string,
	outcome assaydv1alpha1.EvalCaseOutcome, reason string) *assaydv1alpha1.EvalStatus {
	e.Cases = withCase(e.Cases, assaydv1alpha1.EvalCaseRecord{ID: id, Outcome: outcome, Reason: reason,
		Claim: claim})
	passed, done := 0, 0
	for _, sid := range suiteIDs(run.suite) {
		if c := caseRecord(e.Cases, sid); c != nil && outcomeRecorded(c.Outcome) {
			done++
			if c.Outcome == assaydv1alpha1.EvalCasePassed {
				passed++
			}
		}
	}
	total := len(run.suite.Spec.Dataset.Cases)
	if done < total {
		return e
	}
	now := metav1.Now()
	e.Score, e.At = fmt.Sprintf("%d/%d", passed, total), &now
	e.Verdict = assaydv1alpha1.EvalVerdictPassed
	if !evalsuite.Passes(passed, total, run.threshold) {
		e.Verdict = assaydv1alpha1.EvalVerdictFailed
		e.Failed = appendFailed(e.Failed, assaydv1alpha1.EvalPair{RevisionDigest: run.revisionDigest,
			SuiteDigest: run.suiteDigest})
	}
	return e
}

// maxFailedPairs is failed[]'s cap, the CRD's MaxItems: the oldest is evicted,
// the same cap as status.supersededCandidates (step 7).
const maxFailedPairs = 10

func appendFailed(failed []assaydv1alpha1.EvalPair, p assaydv1alpha1.EvalPair) []assaydv1alpha1.EvalPair {
	out := append(append([]assaydv1alpha1.EvalPair(nil), failed...), p)
	if len(out) > maxFailedPairs {
		out = out[len(out)-maxFailedPairs:]
	}
	return out
}

// withCase replaces or adds the entry for c.ID, and keeps cases[] sorted by
// id: the digest's canonical order, never the suite's written order (A12).
func withCase(cases []assaydv1alpha1.EvalCaseRecord, c assaydv1alpha1.EvalCaseRecord) []assaydv1alpha1.EvalCaseRecord {
	out := make([]assaydv1alpha1.EvalCaseRecord, 0, len(cases)+1)
	for _, x := range cases {
		if x.ID != c.ID {
			out = append(out, x)
		}
	}
	out = append(out, c)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// suiteIDs is the suite's case ids in id order: the next case is the first of
// them with no entry, whatever order the suite is written in (A12).
func suiteIDs(s *assaydv1alpha1.EvalSuite) []string {
	ids := make([]string, 0, len(s.Spec.Dataset.Cases))
	for _, c := range s.Spec.Dataset.Cases {
		ids = append(ids, c.ID)
	}
	sort.Strings(ids)
	return ids
}

func suiteCase(s *assaydv1alpha1.EvalSuite, id string) *assaydv1alpha1.EvalCase {
	for i := range s.Spec.Dataset.Cases {
		if s.Spec.Dataset.Cases[i].ID == id {
			return &s.Spec.Dataset.Cases[i]
		}
	}
	return nil
}

func caseRecord(cases []assaydv1alpha1.EvalCaseRecord, id string) *assaydv1alpha1.EvalCaseRecord {
	for i := range cases {
		if cases[i].ID == id {
			return &cases[i]
		}
	}
	return nil
}

// outcomeRecorded is whether an entry carries an outcome, which a claim in
// flight does not.
func outcomeRecorded(o assaydv1alpha1.EvalCaseOutcome) bool {
	return o == assaydv1alpha1.EvalCasePassed || o == assaydv1alpha1.EvalCaseFailed
}

func failedOf(e *assaydv1alpha1.EvalStatus) []assaydv1alpha1.EvalPair {
	if e == nil {
		return nil
	}
	return e.Failed
}

// newClaimNonce is fresh for every send, so a claim's record finds its own
// claim and no other.
func newClaimNonce() string {
	var b [8]byte
	_, _ = rand.Read(b[:]) // crypto/rand.Read never returns an error
	return hex.EncodeToString(b[:])
}
