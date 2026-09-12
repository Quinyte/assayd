// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/compiler"
)

// Design 03's first slice, PR 4: the `Create` transaction of `<agent>-auth`
// (§3.3, §3.3.1, §3.3.3). A new Agent's serving route is prepared with no
// backendRefs, its policy is written and converged, and the route is published
// only after an anonymous request through it gets `401`.
//
// Also here: a served API-key Agent's deleted route re-created prepared and
// published only through `Create`'s probe, its missing policy re-created by the
// `Lock` of a missing policy, and `Adopt`'s refusal with its record.
//
// Slice PR 5 adds the rest of §1.1's slice: J2's `Lock` of a served
// `auth: none` route edited to apikey, K2's edit that ends a refused `Adopt`,
// the abandonment of an unfinished `Create` or J2/K2 `Lock` whose target no
// longer matches the desired mode, a NACK that returns a transaction to
// `Converging` (authnack.go), and `ForeignTrafficPolicy` (authforeign.go).

const (
	// AuthTransactionDeadline is design 03 §3.3.3's deadline for `Create`:
	// set on first entering the first stage, kept on every re-entry, and a
	// condition when it passes, never a timeout. A constant, not a flag, as the
	// design says; the reconciler's AuthDeadline field exists for tests.
	AuthTransactionDeadline = 4 * time.Minute
	// AuthProbeInterval is how often `ProbingAfter` probes before the
	// deadline: the 5 s the e2e polls at. After it, the probe backs off to
	// CardRetryInterval (§3.3.3).
	AuthProbeInterval = 5 * time.Second
)

// The kinds and the stages this operator runs (status.auth.transaction). The
// other kinds the CRD admits, `Narrow` and `Loosen`, are written by no code
// here.
const (
	TxCreate              = "Create"
	TxAdopt               = "Adopt"
	TxLock                = "Lock"
	StageRefused          = "Refused"
	StageProbingBefore    = "ProbingBefore"
	StagePreparingRoute   = "PreparingRoute"
	StageApplyingPolicies = "ApplyingPolicies"
	StageConverging       = "Converging"
	StageProbingAfter     = "ProbingAfter"
	StagePublishing       = "Publishing"
)

// Reasons the -auth step writes. The design's names, except
// ReasonAuthEnforcementPending, which is Ready's while a `Create` is short of
// `Served` (§3.1: "Ready carries why").
const (
	// PolicyCompileFailed (§3.3.1).
	ReasonAuthInputAbsent        = "AuthInputAbsent"
	ReasonConcernNotBuilt        = "ConcernNotBuilt"
	ReasonAuthTransitionNotBuilt = "AuthTransitionNotBuilt"
	// PolicyApplyIncomplete (§3.3.3, §5).
	ReasonAuthEnforcementUnverified = "AuthEnforcementUnverified"
	ReasonAuthLockUnverified        = "AuthLockUnverified"
	ReasonAuthPolicyMissing         = "AuthPolicyMissing"
	ReasonForeignTrafficPolicy      = "ForeignTrafficPolicy"
	ReasonPolicyWriteFailed         = "PolicyWriteFailed"
	ReasonAuthTargetUnrenderable    = "AuthTargetUnrenderable"
	ReasonAuthRecordNotKept         = "AuthRecordNotKept"
	// Ready, while a `Create` is short of `Served` and before its deadline.
	ReasonAuthEnforcementPending = "AuthEnforcementPending"
	// GovernanceSkipped (§3.1).
	ReasonGoverned                   = "Governed"
	ReasonAuthVerifiedOnOneReplica   = "AuthVerifiedOnOneReplica"
	ReasonAuthOptedOut               = "AuthOptedOut"
	ReasonCompilerUpgradeUnsupported = "CompilerUpgradeUnsupported"
	ReasonAuthLockPending            = "AuthLockPending"
)

// failure is one cause the -auth step withholds Ready for.
type failure struct{ reason, message string }

// gatewayError is a failed write the step could not finish, with the reason
// PolicyApplyIncomplete reports it under.
type gatewayError struct {
	reason string
	err    error
}

func (e *gatewayError) Error() string { return e.err.Error() }
func (e *gatewayError) Unwrap() error { return e.err }

// gatewayErrorReason is the reason a failed gateway write is reported under.
func gatewayErrorReason(err error) string {
	if g := (*gatewayError)(nil); errors.As(err, &g) {
		return g.reason
	}
	return "RouteApplyFailed"
}

// gatewayOutcome is what the -auth step leaves for the rest of the pass.
type gatewayOutcome struct {
	requeue time.Duration
	// withhold makes Ready False, unless something else already has. served
	// says whether the Agent was serving, which decides Degraded over Pending
	// (§3.3.1's aggregation).
	withhold *failure
	served   bool
	// foreign names the `traffic` policies on this Agent's route that the
	// operator did not emit (§3.2). While any is set no probe answer is
	// evaluated, because two same-level policies resolve at random.
	foreign []string
	// nack is a genuine NACK naming this Agent's `<agent>-auth` since its last
	// write, as its message says (§3.3.2), or "".
	nack string
}

// authDesire is what the current spec's -auth compiles to, and whether any
// other mandatory concern of the serving route fails to compile.
type authDesire struct {
	target  compiler.AuthTarget
	authErr error
	// budget is a stored spec.budget: admission refuses it now, and nothing in
	// this build enforces one (§3.1, `ConcernNotBuilt`).
	budget bool
}

func desiredAuth(agent *assaydv1alpha1.Agent, runNS string) authDesire {
	target, err := compiler.CompileAuth(agent, runNS)
	return authDesire{target: target, authErr: err, budget: agent.Spec.Budget != nil}
}

// compiles is §3.3.1's entry condition for a transaction: the desired -auth
// compiles, and no other mandatory concern of the serving route is
// PolicyCompileFailed.
func (d authDesire) compiles() bool { return d.authErr == nil && !d.budget }

// compileFailures is PolicyCompileFailed's causes, in a stable order, so that
// one condition carries every one of them (§3.3.1).
//
// `inFlight` is the target mode of a route re-create's `Create` (a `Create`
// beside a recorded mode), or "". A spec-driven `Create` whose target no
// longer matches the desired mode is abandoned before this is judged (§3.3.3),
// so it never reaches here.
func compileFailures(d authDesire, recorded, inFlight string) []failure {
	var out []failure
	switch {
	case d.authErr != nil:
		msg := d.authErr.Error() + ". "
		if recorded != "" && inFlight != "" {
			// A Create beside a recorded mode is a re-create of a deleted route:
			// the route is unpublished until its probe passes (§3.3.3).
			msg += fmt.Sprintf("The -auth this Agent was last served with, %s, is still the one "+
				"applied, and its route is being re-created: it is UNPUBLISHED until an anonymous "+
				"request through it gets 401, and is then published under %s. The revision this "+
				"spec minted gains no weight while the input does not compile (design 03 §3.3.1, "+
				"I1; §3.3.3)", recorded, recorded)
		} else if recorded != "" {
			msg += fmt.Sprintf("The -auth this Agent was last served with, %s, is still enforced, "+
				"its route keeps serving under it, and the revision this spec minted gains no "+
				"weight while the input does not compile (design 03 §3.3.1, I1)", recorded)
		} else {
			msg += "No route is published for this Agent, and no transaction is entered, until " +
				"it compiles (design 03 §3.3.1)"
		}
		out = append(out, failure{ReasonAuthInputAbsent, msg})
	case recorded == string(compiler.AuthModeAPIKey) && d.target.Mode == compiler.AuthModeNone:
		out = append(out, failure{ReasonAuthTransitionNotBuilt, "spec.expose.a2a.auth moved from " +
			"apikey to none. That would open a route this operator has locked, and the transaction " +
			"that would do it is not built, so it is refused: the served -auth policy stays, and " +
			"the route keeps it. To serve this Agent unauthenticated, delete it and create it " +
			"again with auth: none (design 03 §3.3.1)."})
	}
	if d.budget {
		out = append(out, failure{ReasonConcernNotBuilt, "spec.budget is stored on this Agent " +
			"from before admission refused it, and nothing in this build enforces a budget. " +
			"Remove spec.budget. Until then no transaction is entered for this Agent's route, " +
			"and a route already served keeps the -auth it was served with (design 03 §3.1, " +
			"§3.3.1)"})
	}
	return out
}

func joinFailures(fs []failure) string {
	msgs := make([]string, len(fs))
	for i, f := range fs {
		msgs[i] = f.reason + ": " + f.message
	}
	return strings.Join(msgs, " | ")
}

// governanceReason is H2 (design 03 §3.3.3, ADR-0034 Amendment 1): a passing
// probe is a proof when exactly one gateway replica is declared, and evidence
// about the replica that answered otherwise, including when the count is
// unknown. Both are False, so neither withholds Ready.
//
// No flag declares the count yet (`--gateway-replicas` is owed, §8.1), so on
// every install this operator runs today the count is unknown and the reason
// is AuthVerifiedOnOneReplica.
func governanceReason(v *assaydv1alpha1.AuthVerification) string {
	if v != nil && v.ReplicasDeclared != nil && *v.ReplicasDeclared == 1 && v.ReplicasProbed >= 1 {
		return ReasonGoverned
	}
	return ReasonAuthVerifiedOnOneReplica
}

// governanceFor is GovernanceSkipped for an -auth recorded as served (§3.1).
func governanceFor(auth *assaydv1alpha1.AuthStatus) (metav1.ConditionStatus, string, string) {
	if auth.Mode == string(compiler.AuthModeNone) {
		return metav1.ConditionTrue, ReasonAuthOptedOut, "spec.expose.a2a.auth is none, so this " +
			"Agent's route is published unauthenticated at the owner's request, and carries the " +
			"label assayd.dev/auth: \"none\" to say so. Any caller that reaches the gateway reaches " +
			"the agent with no credential. It is an opt-out, not a hole (design 03 §3.1, §3.3.1)"
	}
	probed, declared := int32(0), "an unknown number of"
	if v := auth.Verified; v != nil {
		probed = v.ReplicasProbed
		if v.ReplicasDeclared != nil {
			declared = fmt.Sprint(*v.ReplicasDeclared)
		}
	}
	return metav1.ConditionFalse, governanceReason(auth.Verified), fmt.Sprintf("this Agent's "+
		"route carries the operator's -auth policy, converged at the control plane, and it was "+
		"published only after an anonymous request through it got 401 on %d of %s declared "+
		"gateway replicas. That claims control-plane convergence and enforcement on the replica "+
		"that answered: not that every replica took the policy, not that the group rule refuses a "+
		"key in the wrong group (the probe carries no key), nothing about reaching the Pod "+
		"directly (no NetworkPolicy is materialized), and nothing about where the agent's egress "+
		"goes (design 03 §3.1, §3.3.3 H2)", probed, declared)
}

// vacuousGovernance is GovernanceSkipped for an Agent this operator has
// published no route for through its compiler (§3.1: "vacuously False").
func vacuousGovernance(why string) (metav1.ConditionStatus, string, string) {
	return metav1.ConditionFalse, ReasonGoverned, "vacuously: " + why + ", so nothing reaches " +
		"this Agent through the gateway. Ready and the -auth conditions say why (design 03 §3.1)"
}

// reconcileGateway converges the one route and the one policy this operator
// emits for an Agent (design 03 §3.3, §3.3.1, §3.3.3), and sweeps any route it
// owns that should no longer exist. It reads and writes status.auth, and
// writes GovernanceSkipped, PolicyCompileFailed and PolicyApplyIncomplete.
//
// Which case applies is decided from status.auth first, and from the spec
// only where status.auth records nothing, because a transaction is keyed on
// its target and not on the generation (§3.3).
func (r *AgentReconciler) reconcileGateway(ctx context.Context, agent *assaydv1alpha1.Agent,
	runNS string, status *assaydv1alpha1.AgentStatus, conds *conditionSet, desire authDesire,
) (gatewayOutcome, error) {
	// assessGovernance raised PolicyApplyIncomplete for a Lock in flight, for
	// the paths that never reach this step, and this step re-derives it. But a
	// pass this step leaves with an error re-derives nothing, and dropping the
	// condition there would clear it mid-Lock, one pass before a 401 anybody
	// attributed. So it is withdrawn on the way in and put back on the way out
	// of an error that did not re-derive it, and only while a Lock is still in
	// the slot: a route re-create that replaced the Lock in this pass clears
	// its AuthPolicyMissing in the update that replaced it (A62, A63).
	pre, had := conds.get(assaydv1alpha1.CondPolicyApplyIncomplete)
	had = had && (pre.Reason == ReasonAuthPolicyMissing || pre.Reason == ReasonAuthLockUnverified)
	if had {
		conds.unset(assaydv1alpha1.CondPolicyApplyIncomplete)
	}
	out, err := r.gatewayStep(ctx, agent, runNS, status, conds, desire)
	stillLock := func() bool { tx := authTransaction(status); return tx != nil && tx.Kind == TxLock }
	if _, set := conds.get(assaydv1alpha1.CondPolicyApplyIncomplete); err != nil && had && !set && stillLock() {
		conds.set(assaydv1alpha1.CondPolicyApplyIncomplete, pre.Status, pre.Reason, pre.Message)
	}
	if err == nil {
		reportForeign(status, conds, &out)
	}
	return out, err
}

// reportForeign is §3.2's report of a `traffic` policy on this Agent's route
// that the operator did not emit: PolicyApplyIncomplete and GovernanceSkipped,
// both reason ForeignTrafficPolicy, naming each. It withholds Ready. It
// withdraws nothing and deletes nothing: a route not yet published is held by
// the transaction, which evaluates no probe while one stands, and a published
// route keeps serving (§3.2).
//
// A refused Adopt keeps GovernanceSkipped=CompilerUpgradeUnsupported, whose
// message is the only one that names that Agent's way out; the foreign policy
// is reported on PolicyApplyIncomplete there.
func reportForeign(status *assaydv1alpha1.AgentStatus, conds *conditionSet, out *gatewayOutcome) {
	if len(out.foreign) == 0 {
		return
	}
	msg := fmt.Sprintf("%s: a traffic policy the operator did not emit targets this Agent's route. "+
		"Two same-level traffic policies on one target resolve at random, per gateway replica and "+
		"per restart, so this Agent's -auth cannot be claimed while it stands, and no probe answer "+
		"is taken. A route not yet published is not published, a published route is not "+
		"withdrawn, and the operator never deletes it, because it is not this Agent's <agent>-auth "+
		"by name and label. Remove it (design 03 §3.2)", strings.Join(out.foreign, ", "))
	if c, ok := conds.get(assaydv1alpha1.CondPolicyApplyIncomplete); ok {
		// One condition carries every failing path (§3.3.1).
		conds.set(assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, c.Reason,
			c.Message+" | "+ReasonForeignTrafficPolicy+": "+msg)
	} else {
		conds.set(assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, ReasonForeignTrafficPolicy, msg)
	}
	if tx := authTransaction(status); tx == nil || tx.Kind != TxAdopt {
		conds.set(assaydv1alpha1.CondGovernanceSkipped, metav1.ConditionTrue, ReasonForeignTrafficPolicy, msg)
	}
	if out.withhold == nil {
		out.withhold = &failure{ReasonForeignTrafficPolicy, msg}
	}
	if auth := status.Auth; auth != nil && (auth.Mode != "" ||
		(auth.Transaction != nil && (auth.Transaction.Kind == TxAdopt || auth.Transaction.Kind == TxLock))) {
		out.served = true
	}
}

// gatewayStep is reconcileGateway's body: what every pass reads before the
// -auth step, and the step.
func (r *AgentReconciler) gatewayStep(ctx context.Context, agent *assaydv1alpha1.Agent,
	runNS string, status *assaydv1alpha1.AgentStatus, conds *conditionSet, desire authDesire,
) (gatewayOutcome, error) {
	var out gatewayOutcome
	if !r.Gateway.Enabled {
		// Nothing is emitted, and nothing is SWEPT either. Design 03 §3.1 says a
		// `true → false` transition first runs §5's reverse-order teardown; that
		// teardown is NOT implemented, so a route left behind by an install that
		// was once enabled stays until the operator is re-enabled or a human
		// removes it.
		return out, nil
	}
	name, err := compiler.ServingRouteName(agent.Name)
	if err != nil {
		return out, err
	}

	// A pass acts on status.auth, so it must be the live one. The Agent was read
	// through the cache, and a cache that has not yet seen this operator's own
	// last status write would send this pass back a stage: a route just
	// published would be stripped again, or a refused Adopt read as absent.
	// Nothing is written on a stale read; the queue brings the pass back.
	if err := r.requireFreshAgent(ctx, agent); err != nil {
		return out, err
	}
	// Listed live, every pass: the watch cache holds only policies carrying
	// assayd.dev/agent, and a foreign policy is exactly one it may not hold.
	foreign, err := r.foreignTrafficPolicies(ctx, agent, runNS, name)
	if err != nil {
		return out, err
	}
	out.foreign = foreign
	res, err := r.authStep(ctx, agent, runNS, name, status, conds, desire, out)
	res.foreign = foreign
	return res, err
}

// authStep decides which -auth case applies and runs it.
func (r *AgentReconciler) authStep(ctx context.Context, agent *assaydv1alpha1.Agent, runNS, name string,
	status *assaydv1alpha1.AgentStatus, conds *conditionSet, desire authDesire, out gatewayOutcome,
) (gatewayOutcome, error) {
	recorded, tx := recordOf(status)

	if tx != nil && tx.Kind == TxAdopt {
		// K2 (§3.3.3, ADR-0034 Amendment 4): a desired apikey beside a
		// refusedMode that is not apikey is an owner's edit observed since the
		// refusal, which is the consent the refusal lacked. It becomes a J2
		// Lock, entered only for a target that compiles (§3.3.1).
		if k2Consent(tx, agent) && desire.compiles() && status.ActiveRevision != "" {
			return r.enterLock(ctx, agent, runNS, name, status, conds, out, desire)
		}
		return r.refuseAdopt(ctx, agent, runNS, name, status, conds, desire)
	}
	if tx == nil && recorded == "" {
		// Adopt's trigger (§3.3.3): a PUBLISHED serving route and nothing in
		// status.auth. A route an operator without a compiler published, or one
		// whose status was lost. It is refused: no policy, and the route keeps
		// following promotion as the shipped emitter kept it. Checked before the
		// compile failure, because `Adopt` compiles nothing and outranks it
		// (§3.4.4). The refusal is recorded, so that deleting the route is not
		// an exit from it.
		existing, err := r.currentServingRoute(ctx, agent, runNS, name)
		if err != nil {
			return out, err
		}
		if existing != nil && routePublished(existing) {
			return r.refuseAdopt(ctx, agent, runNS, name, status, conds, desire)
		}
	}

	// Abandonment (§3.3.3): an unfinished spec-driven `Create`, or a J2 or K2
	// `Lock`, whose target mode no longer equals the desired mode. Never a
	// re-creation, whose target is status.auth's.
	abandonedLock := false
	if tx != nil && abandons(status.Auth, desiredModeName(agent)) {
		wasLock := tx.Kind == TxLock
		res, cont, err := r.abandon(ctx, agent, runNS, name, status, conds, out, desire)
		if err != nil || !cont {
			return res, err
		}
		abandonedLock = wasLock
		recorded, tx = recordOf(status)
	}

	inFlight := ""
	if tx != nil && tx.Kind == TxCreate {
		inFlight = tx.TargetMode
	}
	if fails := compileFailures(desire, recorded, inFlight); len(fails) > 0 {
		if abandonedLock && fails[0].reason == ReasonAuthInputAbsent {
			policy, _ := compiler.AuthPolicyName(agent.Name)
			fails[0].message += fmt.Sprintf(". The Lock to apikey was abandoned before it was proved, "+
				"and %s was deleted, so this route serves UNAUTHENTICATED again, whether or not the "+
				"gateway had taken the policy (design 03 §3.3.3)", policy)
		}
		conds.set(assaydv1alpha1.CondPolicyCompileFailed, metav1.ConditionTrue, fails[0].reason,
			joinFailures(fails))
		out.withhold = &failure{fails[0].reason, joinFailures(fails)}
		out.served = recorded != ""
	}

	switch {
	case tx != nil && tx.Kind == TxCreate:
		return r.runCreate(ctx, agent, runNS, name, status, conds, out)
	case tx != nil && tx.Kind == TxLock:
		// A Lock that re-creates a missing policy, or J2's or K2's.
		return r.runLock(ctx, agent, runNS, name, status, conds, out)
	case tx != nil:
		// Nothing here writes another kind, and nothing here knows how to finish
		// one. Leaving everything as found, loudly, is the one safe answer.
		return out, fmt.Errorf("status.auth.transaction is a %s, which this operator does not run; "+
			"the route and the policy are left as they are", tx.Kind)
	case recorded == string(compiler.AuthModeNone) && desire.compiles() &&
		desire.target.Mode == compiler.AuthModeAPIKey && status.ActiveRevision != "":
		// J2 (§3.3.1, ADR-0034 Amendment 2): a served `auth: none` Agent edited
		// to apikey is locked in place.
		return r.enterLock(ctx, agent, runNS, name, status, conds, out, desire)
	case recorded != "":
		res, err := r.reconcileServed(ctx, agent, runNS, name, status, conds, out)
		if tx := authTransaction(status); err == nil && tx != nil && tx.Kind == TxCreate {
			// A route re-create began in this pass, after the compile failure
			// was judged: say what is true now, that the route is unpublished
			// (§3.3.3), and not that it keeps serving.
			if fails := compileFailures(desire, recorded, tx.TargetMode); len(fails) > 0 {
				conds.set(assaydv1alpha1.CondPolicyCompileFailed, metav1.ConditionTrue, fails[0].reason,
					joinFailures(fails))
				res.withhold = &failure{fails[0].reason, joinFailures(fails)}
			}
		}
		return res, err
	}

	// Never served. A transaction is entered only for a target that compiles
	// (§3.3.1), and only once a revision serves, because `Publishing` needs one
	// and a deadline that ran on a slow image pull would name the wrong cause.
	if !desire.compiles() || status.ActiveRevision == "" {
		if err := r.collectRoutes(ctx, agent, runNS, ""); err != nil {
			return out, err
		}
		why := "no revision of this Agent is serving yet"
		if !desire.compiles() {
			why = "this Agent's serving route does not compile, so no route is prepared for it"
		}
		st, reason, msg := vacuousGovernance(why)
		conds.set(assaydv1alpha1.CondGovernanceSkipped, st, reason, msg)
		return out, nil
	}

	// Truncated to the second, which is what metav1.Time keeps, so the
	// recorded deadline and the one in memory are the same instant.
	deadline := metav1.NewTime(time.Now().Add(r.authDeadline()).Truncate(time.Second))
	tx = &assaydv1alpha1.AuthTransaction{
		Kind:         TxCreate,
		TargetMode:   string(desire.target.Mode),
		TargetDigest: desire.target.Digest,
		Stage:        StagePreparingRoute,
		Deadline:     &deadline,
	}
	if desire.target.Mode == compiler.AuthModeNone {
		// `none` has no policy to prepare for: it is published at once, with
		// its marker (§3.3.1). It is still a recorded `Create`, so that a crash
		// after the route write re-enters it rather than reading as `Adopt`.
		tx.Stage = StagePublishing
	}
	if status.Auth == nil {
		status.Auth = &assaydv1alpha1.AuthStatus{}
	}
	status.Auth.Transaction = tx
	// Recorded BEFORE the route is written, so a crash between the two leaves a
	// `Create` with no route, which re-enters at its first stage (§3.3.3). An
	// abandonment earlier in this pass is replaced by this Create in this same
	// update (§3.3.3, "What runs next").
	if err := r.persistStatus(ctx, agent, status); err != nil {
		return out, err
	}
	return r.runCreate(ctx, agent, runNS, name, status, conds, out)
}

// recordOf is status.auth's recorded mode and its transaction.
func recordOf(status *assaydv1alpha1.AgentStatus) (string, *assaydv1alpha1.AuthTransaction) {
	if status.Auth == nil {
		return "", nil
	}
	return status.Auth.Mode, status.Auth.Transaction
}

// isReCreation is §3.3's classification, read from stored fields only: a
// `Create` while status.auth records a mode, or a `Lock` whose targetMode
// equals the recorded mode. A J2 Lock targets apikey over a recorded none, and
// a K2 Lock has no recorded mode, so neither is one.
func isReCreation(auth *assaydv1alpha1.AuthStatus) bool {
	if auth == nil || auth.Transaction == nil || auth.Mode == "" {
		return false
	}
	tx := auth.Transaction
	return tx.Kind == TxCreate || (tx.Kind == TxLock && tx.TargetMode == auth.Mode)
}

// abandons is §3.3.3's "When": an unfinished `Create` or J2/K2 `Lock` whose
// target mode no longer equals the desired mode. Re-creations never are.
func abandons(auth *assaydv1alpha1.AuthStatus, desiredMode string) bool {
	if auth == nil || auth.Transaction == nil || isReCreation(auth) {
		return false
	}
	tx := auth.Transaction
	// A record with no target is not a transaction this operator entered: it
	// is left as found, loudly, and nothing is deleted or published for it.
	if (tx.Kind != TxCreate && tx.Kind != TxLock) || tx.TargetMode == "" {
		return false
	}
	return tx.TargetMode != desiredMode
}

// k2Consent is K2's test (§3.3.3): the desired mode is apikey and the refused
// Adopt's refusedMode is not, so a reconcile has observed the Agent under
// another mode since the refusal and its owner has moved it to apikey.
func k2Consent(tx *assaydv1alpha1.AuthTransaction, agent *assaydv1alpha1.Agent) bool {
	return desiredModeName(agent) == string(compiler.AuthModeAPIKey) &&
		tx.RefusedMode != "" && tx.RefusedMode != string(compiler.AuthModeAPIKey)
}

// ReasonAuthAbandonWaiting is PolicyApplyIncomplete's reason while an
// abandoned transaction's policy delete has returned and the object is still
// held by a finalizer: status keeps the transaction until it is gone.
const ReasonAuthAbandonWaiting = "AuthAbandonWaiting"

// abandon is §3.3.3's abandonment. It returns cont=true when the pass should
// go on to whatever the desired spec now calls for, with status.auth already
// returned to the recorded state; the successor is entered in the same status
// update where it writes one (a `Create`), or the recorded state was persisted
// here where it does not (J2's `none`, a never-served Agent now uncompilable).
//
// Order, each step before the next:
//   - a `Create`'s route loses its backendRefs, and is deleted when the new
//     desired serving route does not compile, because no prepared route exists
//     while it does not (§3.3.1);
//   - the policy the transaction wrote (`written`) is deleted, by §3.2's
//     name-and-label rule. `status.auth` never recorded it as served, so it is
//     the transaction's own. Nothing else is deleted;
//   - the status update. For a `Lock` it comes AFTER the delete, so a crash
//     between them re-runs the abandonment with the Lock still in the slot,
//     and status never says open while an operator-written policy may still
//     lock the route (A67).
func (r *AgentReconciler) abandon(ctx context.Context, agent *assaydv1alpha1.Agent, runNS, name string,
	status *assaydv1alpha1.AgentStatus, conds *conditionSet, out gatewayOutcome, desire authDesire,
) (gatewayOutcome, bool, error) {
	tx := status.Auth.Transaction
	log.FromContext(ctx).Info("abandoning an unfinished -auth transaction: the desired mode changed "+
		"(design 03 §3.3.3)", "kind", tx.Kind, "stage", tx.Stage, "target", tx.TargetMode,
		"desired", desiredModeName(agent), "written", tx.Written)
	if tx.Kind == TxCreate {
		rt, err := r.currentServingRoute(ctx, agent, runNS, name)
		if err != nil {
			return out, false, err
		}
		switch {
		case rt == nil:
		case !desire.compiles():
			if err := r.collectRoutes(ctx, agent, runNS, ""); err != nil {
				return out, false, err
			}
		case routePublished(rt):
			if _, _, err := r.ensureServingRoute(ctx, agent, runNS, status.ActiveRevision,
				status.ActiveRevisionDigest, name, routePublication{prepared: true}); err != nil {
				return out, false, err
			}
		}
	}
	if tx.Written {
		waiting, err := r.deleteAuthPolicy(ctx, agent, runNS)
		if err != nil {
			return out, false, &gatewayError{reason: ReasonPolicyWriteFailed, err: err}
		}
		if waiting != "" {
			msg := fmt.Sprintf("the -auth %s to %s is abandoned, because the desired mode is now %s, "+
				"and the policy it wrote is not gone yet: %s. status.auth keeps the transaction until "+
				"it is (design 03 §3.3.3)", tx.Kind, tx.TargetMode, desiredModeName(agent), waiting)
			conds.set(assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, ReasonAuthAbandonWaiting, msg)
			if out.withhold == nil {
				out.withhold = &failure{ReasonAuthAbandonWaiting, msg}
			}
			out.served = status.Auth.Mode != "" || tx.Kind == TxLock
			out.requeue = 30 * time.Second
			return out, false, nil
		}
	}
	switch {
	case tx.Kind == TxLock && status.Auth.Mode == "":
		// K2's: the refused Adopt, with refusedMode the new desired mode, in the
		// update that replaces the Lock (A66).
		res, err := r.refuseAdopt(ctx, agent, runNS, name, status, conds, desire)
		return res, false, err
	case tx.Kind == TxLock:
		// J2's: the recorded `none`. The route kept its backendRefs and its
		// marker throughout, because only Served removes the marker.
		status.Auth = &assaydv1alpha1.AuthStatus{Mode: status.Auth.Mode}
		if err := r.persistStatus(ctx, agent, status); err != nil {
			return out, false, err
		}
		return out, true, nil
	default:
		// A never-served Create's recorded state is an unpublished route and
		// nothing in status.auth. Its successor is entered by the caller, in the
		// update that writes it. With none to enter, the empty record is
		// written here: the route was deleted above, so Adopt's trigger, a
		// published route beside an empty record, cannot hold.
		status.Auth = nil
		if !desire.compiles() {
			if err := r.persistStatus(ctx, agent, status); err != nil {
				return out, false, err
			}
		}
		return out, true, nil
	}
}

// enterLock records J2's `Lock`, or K2's, which replaces a refused Adopt's
// record: `{kind: Lock, targetMode: apikey, targetDigest}` from the spec, at
// `ProbingBefore`, with its deadline. K2 records no mode (§3.3.3).
func (r *AgentReconciler) enterLock(ctx context.Context, agent *assaydv1alpha1.Agent, runNS, name string,
	status *assaydv1alpha1.AgentStatus, conds *conditionSet, out gatewayOutcome, desire authDesire,
) (gatewayOutcome, error) {
	deadline := metav1.NewTime(time.Now().Add(r.authDeadline()).Truncate(time.Second))
	tx := &assaydv1alpha1.AuthTransaction{Kind: TxLock, TargetMode: string(compiler.AuthModeAPIKey),
		TargetDigest: desire.target.Digest, Stage: StageProbingBefore, Deadline: &deadline}
	if status.Auth == nil || status.Auth.Mode == "" {
		status.Auth = &assaydv1alpha1.AuthStatus{Transaction: tx}
	} else {
		status.Auth.Transaction = tx
	}
	if err := r.persistStatus(ctx, agent, status); err != nil {
		return out, err
	}
	return r.runLock(ctx, agent, runNS, name, status, conds, out)
}

func (r *AgentReconciler) authDeadline() time.Duration {
	if r.AuthDeadline > 0 {
		return r.AuthDeadline
	}
	return AuthTransactionDeadline
}

// persistStatus writes status.auth NOW, ahead of the write it authorizes.
//
// Only status.auth: the rest of the pass's status is written at its end, with
// the conditions that go with it. Writing it here would publish, for
// instance, a phase of Ready beside the previous pass's conditions, for an
// Agent whose route is not published yet.
//
// And the record must be KEPT. The chart ships the CRD under crds/, which
// `helm upgrade` never updates, and an Agent CRD from before slice PR 1 prunes
// status.auth without an error. A transaction that cannot keep its record
// re-enters from nothing on every pass, and a route it published reads as
// Adopt's on the next one. So a record that did not come back stops the pass
// before the write it was meant to authorize.
func (r *AgentReconciler) persistStatus(ctx context.Context, agent *assaydv1alpha1.Agent,
	status *assaydv1alpha1.AgentStatus) error {
	next := agent.Status.DeepCopy()
	next.Auth = status.Auth.DeepCopy()
	if err := r.writeStatus(ctx, agent, next); err != nil {
		return err
	}
	if !authKept(status.Auth, agent.Status.Auth) {
		return &gatewayError{reason: ReasonAuthRecordNotKept, err: fmt.Errorf("status.auth was " +
			"written and did not come back: the Agent CRD installed on this cluster does not carry " +
			"it, and prunes it without an error. `helm upgrade` never updates a chart's crds/, so " +
			"apply this release's charts/assayd/crds/ with kubectl. Nothing is written to the " +
			"gateway until the record is kept")}
	}
	return nil
}

// authKept reports whether the fields that decide the next pass came back.
func authKept(want, got *assaydv1alpha1.AuthStatus) bool {
	if want == nil {
		return got == nil
	}
	if got == nil || got.Mode != want.Mode || (want.Transaction == nil) != (got.Transaction == nil) {
		return false
	}
	if w, g := want.Transaction, got.Transaction; w != nil &&
		(g.Kind != w.Kind || g.Stage != w.Stage || g.Written != w.Written || g.TargetMode != w.TargetMode) {
		return false
	}
	return true
}

// requireFreshAgent reads the Agent through the uncached reader and refuses a
// pass whose cached copy is older.
func (r *AgentReconciler) requireFreshAgent(ctx context.Context, agent *assaydv1alpha1.Agent) error {
	var live assaydv1alpha1.Agent
	if err := r.reader().Get(ctx, client.ObjectKeyFromObject(agent), &live); err != nil {
		return fmt.Errorf("read agent %s/%s live: %w", agent.Namespace, agent.Name, err)
	}
	if live.ResourceVersion != agent.ResourceVersion {
		return fmt.Errorf("agent %s/%s: %w (cached %s, live %s)", agent.Namespace, agent.Name,
			errStaleAgent, agent.ResourceVersion, live.ResourceVersion)
	}
	return nil
}

// errStaleAgent is a pass that read an older Agent than the API server holds.
// It changes no condition: the next pass reads the current one.
var errStaleAgent = errors.New("the cached Agent is older than the live one; retrying")

// runCreate advances a `Create` as far as it can in one pass, and returns
// when a stage waits. Every stage's status update precedes the write it
// authorizes. The loop is bounded, so a write that does not keep can only
// requeue, never spin.
func (r *AgentReconciler) runCreate(ctx context.Context, agent *assaydv1alpha1.Agent, runNS, name string,
	status *assaydv1alpha1.AgentStatus, conds *conditionSet, out gatewayOutcome,
) (gatewayOutcome, error) {
	tx := status.Auth.Transaction
	target, err := createTarget(agent, runNS, tx)
	if err != nil {
		return out, err
	}
	rev, revDigest := status.ActiveRevision, status.ActiveRevisionDigest
	now := time.Now()
	deadlinePassed := tx.Deadline != nil && !now.Before(tx.Deadline.Time)
	var unmet string
	published := false
	requeue := time.Duration(0)

	for step := 0; ; step++ {
		if step == 12 {
			unmet = "the transaction moved back and forth without settling in one pass; a write " +
				"it made did not keep"
			requeue = AuthProbeInterval
			break
		}
		stage := tx.Stage
		if stage == StagePreparingRoute {
			if _, _, err := r.ensureServingRoute(ctx, agent, runNS, rev, revDigest, name,
				routePublicationFor(status.Auth)); err != nil {
				return out, err
			}
			if err := r.collectRoutes(ctx, agent, runNS, name); err != nil {
				return out, err
			}
			// `written` is set in the update that ENTERS ApplyingPolicies, before
			// the write, so a crash between the two reads as written (§3.3).
			tx.Stage, tx.Written = StageApplyingPolicies, true
			if err := r.persistStatus(ctx, agent, status); err != nil {
				return out, err
			}
			continue
		}
		if stage == StageApplyingPolicies {
			// Every entry repeats the write, a re-entry included: it is
			// create/update over owned fields, so a write that already landed is
			// a no-op, and a crash before it landed cannot skip it (§3.3.3).
			if _, err := r.ensureAuthPolicy(ctx, agent, target.Policy); err != nil {
				return out, &gatewayError{reason: ReasonPolicyWriteFailed, err: err}
			}
			tx.Stage = StageConverging
			if err := r.persistStatus(ctx, agent, status); err != nil {
				return out, err
			}
			continue
		}
		if stage == StageConverging || stage == StageProbingAfter {
			// The prepared route and the written policy must still be what
			// this transaction wrote. A route that is gone re-enters at
			// PreparingRoute, and a policy that is gone or changed re-enters
			// at the write: nothing past ApplyingPolicies writes a policy.
			rt, created, err := r.ensureServingRoute(ctx, agent, runNS, rev, revDigest, name,
				routePublicationFor(status.Auth))
			if err != nil {
				return out, err
			}
			policy, intact, err := r.authPolicyIntact(ctx, agent, runNS, target)
			if err != nil {
				return out, err
			}
			if created || !intact {
				tx.Stage = StageApplyingPolicies
				if err := r.persistStatus(ctx, agent, status); err != nil {
					return out, err
				}
				continue
			}
			if n := r.freshNack(ctx, agent, runNS, policy); n != "" {
				// A NACK returns the machine to Converging and advances
				// nothing (§3.3).
				out.nack, unmet = n, n
				if stage != StageConverging {
					tx.Stage = StageConverging
					if err := r.persistStatus(ctx, agent, status); err != nil {
						return out, err
					}
				}
				requeue = AuthProbeInterval
				break
			}
			if stage == StageConverging {
				ok, why := routeConverged(rt, r.Gateway)
				if ok {
					ok, why = policyConverged(policy, r.Gateway)
				}
				if !ok {
					unmet = why
					break
				}
				tx.Stage = StageProbingAfter
				if err := r.persistStatus(ctx, agent, status); err != nil {
					return out, err
				}
				continue
			}
			if len(out.foreign) > 0 {
				// A foreign traffic policy could answer the 401 as well as
				// this Agent's, at random: no answer is taken, and the route
				// is not published while it stands (§3.2).
				unmet = "a traffic policy the operator did not emit targets the route, so no probe " +
					"answer is taken: " + strings.Join(out.foreign, ", ")
				requeue = AuthProbeInterval
				break
			}
			answer, perr := r.probeAgent(ctx, agent)
			tx.Probe = &assaydv1alpha1.AuthProbe{}
			if perr == nil {
				code := int32(answer.Code)
				tx.Probe.After = &code
			}
			// A route with no backend answers 500, never 401, so a 401 here is
			// the route's -auth, on the replica that answered (§3.3.3).
			if perr == nil && answer.Code == 401 {
				tx.Stage = StagePublishing
				if err := r.persistStatus(ctx, agent, status); err != nil {
					return out, err
				}
				continue
			}
			if err := r.persistStatus(ctx, agent, status); err != nil {
				return out, err
			}
			unmet = describeProbe(agent, answer, perr)
			requeue = AuthProbeInterval
			if deadlinePassed {
				requeue = CardRetryInterval
			}
			break
		}
		if stage == StagePublishing {
			if target.Mode == compiler.AuthModeAPIKey {
				_, intact, err := r.authPolicyIntact(ctx, agent, runNS, target)
				if err != nil {
					return out, err
				}
				if !intact {
					// Back to the start: the route loses its backendRefs before the
					// policy is written again, so it never carries traffic under a
					// policy the probe did not see.
					tx.Stage = StagePreparingRoute
					if err := r.persistStatus(ctx, agent, status); err != nil {
						return out, err
					}
					continue
				}
			}
			if rev == "" {
				unmet = "no revision of this Agent is serving, so there is no backend to publish"
				break
			}
			if len(out.foreign) > 0 {
				// §3.2: a route not yet published is not published while a foreign
				// traffic policy stands, whichever stage saw it: a 401 taken
				// before it landed, or a none route, which runs no probe. One
				// already published is not withdrawn (slice PR 5's review).
				unmet = "a traffic policy the operator did not emit targets the route, so it is not " +
					"published: " + strings.Join(out.foreign, ", ")
				requeue = AuthProbeInterval
				break
			}
			// An API-key route that is gone re-enters at PreparingRoute.
			// Re-created here it would carry backends at once, on an object no
			// probe has seen refuse (§3.3: "at PreparingRoute when it does not"
			// exist). A `none` route has no policy and no probe, and is
			// published at once, re-created or not (§3.3.1).
			current, err := r.currentServingRoute(ctx, agent, runNS, name)
			if err != nil {
				return out, err
			}
			if current == nil && target.Mode == compiler.AuthModeAPIKey {
				tx.Stage = StagePreparingRoute
				if err := r.persistStatus(ctx, agent, status); err != nil {
					return out, err
				}
				continue
			}
			rt, _, err := r.ensureServingRoute(ctx, agent, runNS, rev, revDigest, name,
				routePublicationFor(status.Auth))
			if errors.Is(err, errRouteGone) {
				// Gone between the check above and the write: re-prepared, never
				// created published.
				tx.Stage = StagePreparingRoute
				if err := r.persistStatus(ctx, agent, status); err != nil {
					return out, err
				}
				continue
			}
			if err != nil {
				return out, err
			}
			if err := r.collectRoutes(ctx, agent, runNS, name); err != nil {
				return out, err
			}
			published = true
			if ok, why := routeConverged(rt, r.Gateway); !ok {
				unmet = "the published route has not converged: " + why
				break
			}
			return r.recordServed(ctx, agent, runNS, target, status, conds, out)
		}
		return out, fmt.Errorf("status.auth.transaction is a Create in stage %q, which this "+
			"operator does not know", stage)
	}

	// Waiting.
	if requeue == 0 {
		requeue = CardRetryInterval
		if !deadlinePassed {
			// Positive: `now` is before the deadline, by the same reading.
			requeue = tx.Deadline.Sub(now) + time.Second
		}
	}
	out.requeue = requeue
	if status.Auth.Mode != "" {
		// A re-create: this Agent was serving (§3.3.1's aggregation).
		out.served = true
	}
	msg := fmt.Sprintf("the Create of this Agent's -auth (target %s) is in stage %s: %s",
		tx.TargetMode, tx.Stage, unmet)
	if deadlinePassed || out.nack != "" {
		// A NACK raises the condition at once, not at the deadline (§3.3).
		full := msg + ". The route stays unpublished, and the transaction keeps working (design 03 §3.3.3)"
		if deadlinePassed {
			full = fmt.Sprintf("%s. Its deadline, %s, has passed. The route stays unpublished, and "+
				"the transaction keeps working. Read the route's and the policy's status, and the "+
				"Gateway's Warning Events (design 03 §3.3.3)", msg, tx.Deadline.UTC().Format(time.RFC3339))
		}
		if published {
			full = fmt.Sprintf("%s. The transaction keeps working (design 03 §3.3.3)", msg)
			if deadlinePassed {
				full = fmt.Sprintf("%s. Its deadline, %s, has passed. The transaction keeps working "+
					"(design 03 §3.3.3)", msg, tx.Deadline.UTC().Format(time.RFC3339))
			}
		}
		conds.set(assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue,
			ReasonAuthEnforcementUnverified, full)
		if out.withhold == nil {
			out.withhold = &failure{ReasonAuthEnforcementUnverified, full}
		}
	} else if !published && out.withhold == nil {
		out.withhold = &failure{ReasonAuthEnforcementPending, msg + ". The route is not " +
			"published until an anonymous request through it gets 401 (design 03 §3.3.3)"}
	}
	var st metav1.ConditionStatus
	var reason, gmsg string
	switch {
	case published && target.Mode == compiler.AuthModeNone:
		st, reason, gmsg = governanceFor(servedRecord(agent, runNS, target))
	case published:
		st, reason, gmsg = metav1.ConditionFalse, ReasonAuthVerifiedOnOneReplica, "this Agent's route "+
			"is published under the operator's -auth policy, after an anonymous request through it "+
			"got 401 on the gateway replica that answered, and the published route has not "+
			"converged at the control plane yet (design 03 §3.3.3, H2)"
	default:
		st, reason, gmsg = vacuousGovernance("this Agent's route is prepared and not yet published, " +
			"in the Create's stage " + tx.Stage)
	}
	conds.set(assaydv1alpha1.CondGovernanceSkipped, st, reason, gmsg)
	return out, nil
}

// createTarget re-renders the transaction's recorded target. It is the
// recorded one, never the spec's: a transaction is keyed on its target
// (§3.3), so an edit that changes the desired mode does not change what an
// unfinished `Create` applies. Abandoning it is the next change's (§3.3.3).
func createTarget(agent *assaydv1alpha1.Agent, runNS string, tx *assaydv1alpha1.AuthTransaction) (compiler.AuthTarget, error) {
	switch compiler.AuthMode(tx.TargetMode) {
	case compiler.AuthModeNone:
		return compiler.AuthTarget{Mode: compiler.AuthModeNone}, nil
	case compiler.AuthModeAPIKey:
		policy, err := compiler.AuthPolicy(compiler.AuthInput{AgentName: agent.Name,
			AgentNamespace: agent.Namespace, AgentUID: agent.UID, RunNamespace: runNS})
		if err != nil {
			return compiler.AuthTarget{}, err
		}
		digest, err := compiler.Digest(policy)
		if err != nil {
			return compiler.AuthTarget{}, err
		}
		if digest != tx.TargetDigest {
			// Design 03 A69 left this comparison to PR 4: a status restored
			// under a new Agent UID records a digest no policy the compiler
			// renders can equal. The recorded target wins, so nothing is written.
			return compiler.AuthTarget{}, &gatewayError{reason: ReasonAuthTargetUnrenderable,
				err: fmt.Errorf("the Create in status.auth.transaction targets policy digest %s, and "+
					"this operator renders %s for this Agent, for example because its status was "+
					"restored under another UID, so it cannot apply the recorded target. Nothing is "+
					"written. Delete the Agent and create it again", tx.TargetDigest, digest)}
		}
		return compiler.AuthTarget{Mode: compiler.AuthModeAPIKey, Policy: policy, Digest: digest}, nil
	}
	return compiler.AuthTarget{}, fmt.Errorf("status.auth.transaction targets mode %q, which does "+
		"not compile", tx.TargetMode)
}

// servedRecord is what `Served` records (§3.3): the mode, and for apikey the
// key source, the admitted group, the digest and how far the probe reached.
func servedRecord(agent *assaydv1alpha1.Agent, runNS string, target compiler.AuthTarget) *assaydv1alpha1.AuthStatus {
	if target.Mode == compiler.AuthModeNone {
		return &assaydv1alpha1.AuthStatus{Mode: string(compiler.AuthModeNone)}
	}
	return &assaydv1alpha1.AuthStatus{
		Mode:           string(compiler.AuthModeAPIKey),
		KeySource:      compiler.KeySource(runNS),
		AdmittedGroups: []string{agent.Namespace},
		AppliedDigest:  target.Digest,
		// One probe reaches one replica, and no flag declares how many there
		// are, so the count is unknown: absent (H2).
		Verified: &assaydv1alpha1.AuthVerification{ReplicasProbed: 1},
	}
}

// recordServed is `Served`: only now does status.auth record the mode (§3.3).
func (r *AgentReconciler) recordServed(ctx context.Context, agent *assaydv1alpha1.Agent, runNS string,
	target compiler.AuthTarget, status *assaydv1alpha1.AgentStatus, conds *conditionSet, out gatewayOutcome,
) (gatewayOutcome, error) {
	status.Auth = servedRecord(agent, runNS, target)
	if err := r.persistStatus(ctx, agent, status); err != nil {
		return out, err
	}
	st, reason, msg := governanceFor(status.Auth)
	conds.set(assaydv1alpha1.CondGovernanceSkipped, st, reason, msg)
	return out, nil
}

// reconcileServed keeps a served Agent's route published under the mode
// status.auth records, and its `<agent>-auth` as status.auth records it.
//
// **For an API-key Agent, a route with backendRefs is never CREATED.**
// ensureServingRoute refuses to (errRouteGone), so backendRefs reach the route
// only as an update of a route that exists, one a Create prepared and published
// after a 401. And that update happens only after `<agent>-auth` was read
// present and intact in the same pass. One window remains, and it is named
// rather than claimed away: a policy deleted after that read and before the
// route update leaves the route published without it until the next pass,
// which the policy watch starts. What that pass does depends on where it
// finds the transaction. A Create still short of Served finds the policy
// missing or changed and re-enters at PreparingRoute, which strips the route
// before the policy is written again. A transaction already Served, including
// one whose Publishing found its route published and converged and so wrote
// nothing and recorded Served in the pass of the window, finds the policy
// missing here and enters the Lock below, with the route left open. So both objects are checked BEFORE the route is touched:
//
//   - a route that is absent, or has lost its backendRefs, is re-created
//     PREPARED, by a `Create` whose target is status.auth's and never the
//     spec's, and published only through that Create's probe (§3.3.3). A
//     prune that takes the route and the policy together ends here, whichever
//     delete is seen first;
//   - a policy that is missing beside a published route is re-created from
//     status.auth by a `Lock` from ApplyingPolicies, and trusted only once an
//     anonymous request gets a 401 that can be attributed (§3.3.3). The route
//     is not withdrawn: it is already open, and withdrawing it would hand
//     anyone who can delete a policy a switch that takes the Agent off the air.
//
// Nothing here deletes the policy. A compile failure means "the desired
// policy is unknown", never "no policy is desired" (§3.3.1, I1).
func (r *AgentReconciler) reconcileServed(ctx context.Context, agent *assaydv1alpha1.Agent, runNS, name string,
	status *assaydv1alpha1.AgentStatus, conds *conditionSet, out gatewayOutcome,
) (gatewayOutcome, error) {
	apikey := status.Auth.Mode == string(compiler.AuthModeAPIKey)
	if apikey && status.ActiveRevision != "" {
		rt, err := r.currentServingRoute(ctx, agent, runNS, name)
		if err != nil {
			return out, err
		}
		if rt == nil || !routePublished(rt) {
			return r.recreateRoute(ctx, agent, runNS, name, status, conds, out)
		}
		present, err := r.authPolicyPresent(ctx, agent, runNS)
		if err != nil {
			return out, err
		}
		if !present {
			return r.lockMissingPolicy(ctx, agent, runNS, name, status, conds, out)
		}
		// Re-asserted before the route, so that a promotion never moves the
		// route's backendRef while the policy has been changed out of band.
		if err := r.reassertServedPolicy(ctx, agent, runNS, status.Auth); err != nil {
			return out, &gatewayError{reason: ReasonPolicyWriteFailed, err: err}
		}
	}
	keep := ""
	if status.ActiveRevision != "" {
		_, _, err := r.ensureServingRoute(ctx, agent, runNS, status.ActiveRevision,
			status.ActiveRevisionDigest, name, routePublicationFor(status.Auth))
		if errors.Is(err, errRouteGone) {
			// Gone between the check above and the write.
			return r.recreateRoute(ctx, agent, runNS, name, status, conds, out)
		}
		if err != nil {
			return out, err
		}
		keep = name
	}
	if err := r.collectRoutes(ctx, agent, runNS, keep); err != nil {
		return out, err
	}
	// The value the served policy earned, compile failure or not (§3.3.1).
	st, reason, msg := governanceFor(status.Auth)
	conds.set(assaydv1alpha1.CondGovernanceSkipped, st, reason, msg)
	return out, nil
}

// recreateRoute is §3.3.3's re-create of a served Agent's deleted or stripped
// `<agent>-serving`: `{kind: Create, targetMode, targetDigest}` taken from
// status.auth, recorded before the prepared route is written. It displaces a
// missing-policy Lock, whose policy the Create writes at ApplyingPolicies, and
// which has no Publishing of its own (A62). It is never abandoned.
func (r *AgentReconciler) recreateRoute(ctx context.Context, agent *assaydv1alpha1.Agent, runNS, name string,
	status *assaydv1alpha1.AgentStatus, conds *conditionSet, out gatewayOutcome,
) (gatewayOutcome, error) {
	deadline := metav1.NewTime(time.Now().Add(r.authDeadline()).Truncate(time.Second))
	status.Auth.Transaction = &assaydv1alpha1.AuthTransaction{Kind: TxCreate, TargetMode: status.Auth.Mode,
		TargetDigest: status.Auth.AppliedDigest, Stage: StagePreparingRoute, Deadline: &deadline}
	if err := r.persistStatus(ctx, agent, status); err != nil {
		return out, err
	}
	return r.runCreate(ctx, agent, runNS, name, status, conds, out)
}

// lockMissingPolicy records §3.3.3's Lock of a missing policy, from
// ApplyingPolicies, with its target taken from status.auth.
func (r *AgentReconciler) lockMissingPolicy(ctx context.Context, agent *assaydv1alpha1.Agent, runNS, name string,
	status *assaydv1alpha1.AgentStatus, conds *conditionSet, out gatewayOutcome,
) (gatewayOutcome, error) {
	deadline := metav1.NewTime(time.Now().Add(r.authDeadline()).Truncate(time.Second))
	status.Auth.Transaction = &assaydv1alpha1.AuthTransaction{Kind: TxLock, TargetMode: status.Auth.Mode,
		TargetDigest: status.Auth.AppliedDigest, Stage: StageApplyingPolicies, Written: true, Deadline: &deadline}
	if err := r.persistStatus(ctx, agent, status); err != nil {
		return out, err
	}
	return r.runLock(ctx, agent, runNS, name, status, conds, out)
}

// runLock runs a `Lock` (§3.3.3) on a route that stays published throughout:
//
//   - a Lock that re-creates a missing policy (its targetMode equals the
//     recorded mode): ApplyingPolicies → Converging → ProbingAfter → Served. It
//     writes nothing to the route, and a route found absent or stripped
//     displaces it with the route re-create (A62);
//   - J2's, over a recorded `none`, and K2's, over a refused Adopt with no
//     recorded mode: ProbingBefore → ApplyingPolicies → Converging →
//     ProbingAfter → Served. The route is re-asserted published at once under
//     the recorded state (J2: with its no-auth marker; K2: as the shipped
//     emitter made it), so it is never without backendRefs, and it follows
//     promotion: the Lock does not hold its edit's weight shift (§1.1). The
//     marker is removed only at Served.
func (r *AgentReconciler) runLock(ctx context.Context, agent *assaydv1alpha1.Agent, runNS, name string,
	status *assaydv1alpha1.AgentStatus, conds *conditionSet, out gatewayOutcome,
) (gatewayOutcome, error) {
	tx := status.Auth.Transaction
	reCreate := isReCreation(status.Auth)
	k2 := status.Auth.Mode == ""
	// The target first: a Lock whose recorded target does not render touches
	// nothing, the route included.
	target, err := createTarget(agent, runNS, tx)
	if err != nil {
		return out, fmt.Errorf("the Lock in status.auth.transaction: %w", err)
	}
	var rt *gatewayv1.HTTPRoute
	if reCreate {
		cur, err := r.currentServingRoute(ctx, agent, runNS, name)
		if err != nil {
			return out, err
		}
		if (cur == nil || !routePublished(cur)) && status.ActiveRevision != "" {
			return r.recreateRoute(ctx, agent, runNS, name, status, conds, out)
		}
		if cur == nil {
			return out, fmt.Errorf("a Lock of %s's missing policy has no route and no serving revision", agent.Name)
		}
		rt = cur
	} else {
		if status.ActiveRevision == "" {
			return out, fmt.Errorf("a Lock of %s has no serving revision to keep published", agent.Name)
		}
		cur, _, err := r.ensureServingRoute(ctx, agent, runNS, status.ActiveRevision,
			status.ActiveRevisionDigest, name, routePublicationFor(status.Auth))
		if err != nil {
			return out, err
		}
		if err := r.collectRoutes(ctx, agent, runNS, name); err != nil {
			return out, err
		}
		rt = cur
		// A promotion puts a different backend behind the probe, so it clears
		// beforeObserved and beforeRevision in the status update that sees it,
		// and a 401 must then be attributed on the new revision's own digest
		// (§3.3.3, A61).
		if tx.BeforeRevision != "" && singleBackend(rt) != WorkloadName(agent.Name, tx.BeforeRevision) {
			tx.BeforeObserved, tx.BeforeRevision = false, ""
			if err := r.persistStatus(ctx, agent, status); err != nil {
				return out, err
			}
		}
	}
	now := time.Now()
	deadlinePassed := tx.Deadline != nil && !now.Before(tx.Deadline.Time)
	var unmet string
	requeue := time.Duration(0)
steps:
	for step := 0; ; step++ {
		if step == 12 {
			unmet = "the transaction moved back and forth without settling in one pass; a write " +
				"it made did not keep"
			requeue = AuthProbeInterval
			break
		}
		switch tx.Stage {
		case StageProbingBefore:
			// J2 and K2 only, and an observation, never a gate: ONE anonymous
			// request, recorded whatever it got, and then the write (§3.3.3,
			// A58). It sets beforeObserved only on the card the route's one
			// backendRef names, by the digest status.cards records for that
			// revision (A62).
			answer, perr := r.probeAgent(ctx, agent)
			tx.Probe = &assaydv1alpha1.AuthProbe{}
			if perr == nil {
				code := int32(answer.Code)
				tx.Probe.Before = &code
				if rev := beforeRevisionFor(agent, status, rt, answer); rev != "" {
					tx.BeforeObserved, tx.BeforeRevision = true, rev
				}
			}
			tx.Stage, tx.Written = StageApplyingPolicies, true
			if err := r.persistStatus(ctx, agent, status); err != nil {
				return out, err
			}
		case StageApplyingPolicies:
			if _, err := r.ensureAuthPolicy(ctx, agent, target.Policy); err != nil {
				return out, &gatewayError{reason: ReasonPolicyWriteFailed, err: err}
			}
			tx.Stage = StageConverging
			if err := r.persistStatus(ctx, agent, status); err != nil {
				return out, err
			}
		case StageConverging, StageProbingAfter:
			policy, intact, err := r.authPolicyIntact(ctx, agent, runNS, target)
			if err != nil {
				return out, err
			}
			if !intact {
				tx.Stage = StageApplyingPolicies
				if err := r.persistStatus(ctx, agent, status); err != nil {
					return out, err
				}
				continue
			}
			if n := r.freshNack(ctx, agent, runNS, policy); n != "" {
				out.nack, unmet = n, n
				if tx.Stage != StageConverging {
					tx.Stage = StageConverging
					if err := r.persistStatus(ctx, agent, status); err != nil {
						return out, err
					}
				}
				requeue = AuthProbeInterval
				break steps
			}
			if tx.Stage == StageConverging {
				ok, why := routeConverged(rt, r.Gateway)
				if ok {
					ok, why = policyConverged(policy, r.Gateway)
				}
				if !ok {
					unmet = why
					break steps
				}
				tx.Stage = StageProbingAfter
				if err := r.persistStatus(ctx, agent, status); err != nil {
					return out, err
				}
				continue
			}
			if len(out.foreign) > 0 {
				// Two same-level traffic policies resolve at random, so a 401
				// says nothing about which one answered (§3.2).
				unmet = "a traffic policy the operator did not emit targets the route, so no probe " +
					"answer is taken: " + strings.Join(out.foreign, ", ")
				requeue = AuthProbeInterval
				break steps
			}
			answer, perr := r.probeAgent(ctx, agent)
			if tx.Probe == nil {
				tx.Probe = &assaydv1alpha1.AuthProbe{}
			}
			tx.Probe.After = nil
			if perr == nil {
				code := int32(answer.Code)
				tx.Probe.After = &code
			}
			// A 401 on a published route counts only when it can be attributed:
			// after an observed 200 on the revision the route still names, or
			// while status.cards records a digest for a revision it serves.
			attributed, fetchedAt, why := cardAttributes(agent, status, rt)
			observed, beforeRev := tx.BeforeObserved, tx.BeforeRevision
			if perr == nil && answer.Code == 401 && (observed || attributed) {
				return r.lockServed(ctx, agent, runNS, name, target, status, conds, out, reCreate,
					observed, beforeRev, fetchedAt)
			}
			if err := r.persistStatus(ctx, agent, status); err != nil {
				return out, err
			}
			unmet = describeProbe(agent, answer, perr)
			if perr == nil && answer.Code == 401 {
				unmet = "the last anonymous probe got a 401 that cannot be attributed, so the " +
					"transition is not observed: " + why
			}
			requeue = AuthProbeInterval
			if deadlinePassed {
				requeue = CardRetryInterval
			}
			break steps
		default:
			return out, fmt.Errorf("status.auth.transaction is a Lock in stage %q, which this "+
				"operator does not know", tx.Stage)
		}
	}
	if requeue == 0 {
		requeue = CardRetryInterval
		if !deadlinePassed {
			requeue = tx.Deadline.Sub(now) + time.Second
		}
	}
	out.requeue = requeue
	out.served = true
	deadlineNote := ""
	if deadlinePassed {
		deadlineNote = fmt.Sprintf(". Its deadline, %s, has passed, and the Lock keeps working",
			tx.Deadline.UTC().Format(time.RFC3339))
	}
	policyName, _ := compiler.AuthPolicyName(agent.Name)
	if reCreate {
		msg := fmt.Sprintf("status.auth records this Agent as served under apikey, and its policy %s/%s "+
			"was missing, so its route served with no key. It has been re-created from status.auth, "+
			"and is trusted only once an anonymous request through the route gets a 401 that can be "+
			"attributed (design 03 §3.3.3, the Lock of a missing policy). The route is not withdrawn. "+
			"The Lock is in stage %s: %s%s", runNS, policyName, tx.Stage, unmet, deadlineNote)
		conds.set(assaydv1alpha1.CondGovernanceSkipped, metav1.ConditionTrue, ReasonAuthPolicyMissing, msg)
		conds.set(assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, ReasonAuthPolicyMissing, msg)
		if out.withhold == nil {
			out.withhold = &failure{ReasonAuthPolicyMissing, msg}
		}
		return out, nil
	}
	msg := lockPendingMessage(k2, policyName, tx, unmet) + deadlineNote
	conds.set(assaydv1alpha1.CondGovernanceSkipped, metav1.ConditionTrue, ReasonAuthLockPending, msg)
	if deadlinePassed || out.nack != "" {
		// §3.3.3's deadline table, J2's row, K2's included: PolicyApplyIncomplete,
		// AuthLockUnverified, and Ready=False. The route keeps serving, open, as
		// before the edit. A NACK raises it at once (§3.3).
		conds.set(assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, ReasonAuthLockUnverified, msg)
		if out.withhold == nil {
			out.withhold = &failure{ReasonAuthLockUnverified, msg}
		}
	}
	return out, nil
}

// lockPendingMessage is GovernanceSkipped=AuthLockPending's message, and
// AuthLockUnverified's at the deadline (§3.3.3): the stage, what it waits on
// (the last answer, and whether a 401 could be attributed), whether the
// before-state was observed, and that the lock is one-way once it lands.
func lockPendingMessage(k2 bool, policy string, tx *assaydv1alpha1.AuthTransaction, unmet string) string {
	what := "spec.expose.a2a.auth moved from none to apikey, so this Agent's serving route is being " +
		"locked in place by " + policy
	if k2 {
		what = "this route was published before the compiler (Adopt), and its owner edited " +
			"spec.expose.a2a.auth to apikey, which is the consent that ends the refusal (K2), so the " +
			"route is being locked in place by " + policy
	}
	before := "the before-state was not observed"
	if tx.BeforeObserved {
		before = fmt.Sprintf("the before-state was observed: an anonymous request was served "+
			"revision %s's own card", tx.BeforeRevision)
	}
	return fmt.Sprintf("%s (design 03 §3.3.3, Lock). The route keeps serving UNAUTHENTICATED until an "+
		"anonymous request through it gets a 401 that can be attributed. The Lock is in stage %s: %s; "+
		"%s. Once the lock lands it is ONE-WAY in this operator: apikey → none is refused, so only "+
		"deleting the Agent and creating it again undoes it. Editing auth away from apikey before "+
		"then abandons the Lock and deletes the policy it wrote (design 03 §1.1)", what, tx.Stage,
		unmet, before)
}

// lockServed is a Lock's `Served`: only now does status.auth record
// `mode: apikey` (§3.3.3). For J2 and K2 the route's no-auth marker is then
// removed, because until the probe saw 401 its claim may still have been true
// (§3.3.1). The message says "transition observed" when beforeObserved was
// set, and "transition not observed", with the card digest's fetchedAt,
// otherwise.
func (r *AgentReconciler) lockServed(ctx context.Context, agent *assaydv1alpha1.Agent, runNS, name string,
	target compiler.AuthTarget, status *assaydv1alpha1.AgentStatus, conds *conditionSet, out gatewayOutcome,
	reCreate, observed bool, beforeRev, fetchedAt string,
) (gatewayOutcome, error) {
	served, err := r.recordServed(ctx, agent, runNS, target, status, conds, out)
	if err != nil {
		return served, err
	}
	if !reCreate {
		if _, _, err := r.ensureServingRoute(ctx, agent, runNS, status.ActiveRevision,
			status.ActiveRevisionDigest, name, routePublicationFor(status.Auth)); err != nil {
			return served, err
		}
	}
	st, reason, msg := governanceFor(status.Auth)
	switch {
	case reCreate:
		msg += ". Its policy was missing and was re-created, and the 401 that proved it is attributed " +
			"by the card digest the operator's own fetch recorded at " + fetchedAt + ": transition " +
			"not observed (design 03 §3.3.3)"
	case observed:
		msg += fmt.Sprintf(". It was locked in place: an anonymous request that was served revision "+
			"%s's own card now gets 401, so the transition was observed (design 03 §3.3.3)", beforeRev)
	default:
		msg += ". It was locked in place, and the 401 that proved it is attributed by the card digest " +
			"the operator's own fetch recorded at " + fetchedAt + " for a revision the route serves: " +
			"transition not observed (design 03 §3.3.3)"
	}
	conds.set(assaydv1alpha1.CondGovernanceSkipped, st, reason, msg)
	return served, nil
}

// singleBackend is the name of the route's one backendRef, or "" when it has
// none or more than one.
func singleBackend(rt *gatewayv1.HTTPRoute) string {
	if rt == nil || len(rt.Spec.Rules) != 1 || len(rt.Spec.Rules[0].BackendRefs) != 1 {
		return ""
	}
	return string(rt.Spec.Rules[0].BackendRefs[0].Name)
}

// beforeRevisionFor is ProbingBefore's attribution (§3.3.3, A62): the
// revision the route's one backendRef names, when the answer was a 200 whose
// body digests to what status.cards records for THAT revision. A card that
// matches only another revision's digest proves nothing about the one the
// route now names.
func beforeRevisionFor(agent *assaydv1alpha1.Agent, status *assaydv1alpha1.AgentStatus,
	rt *gatewayv1.HTTPRoute, a AuthProbeAnswer) string {
	backend := singleBackend(rt)
	if a.Code != 200 || a.CardDigest == "" || backend == "" {
		return ""
	}
	for _, c := range status.Cards {
		if c.Digest != "" && WorkloadName(agent.Name, c.Revision) == backend {
			if c.Digest == a.CardDigest {
				return c.Revision
			}
			return ""
		}
	}
	return ""
}

// cardAttributes is §3.3.3's attribution for a 401 on a published route: it
// counts only while status.cards records a digest for a revision the route's
// backendRefs serve. The operator's own card fetch records one only from an
// anonymous 200 with a valid card on the same path, so that backend answered
// the path anonymously at its fetchedAt, and a 401 there now comes from
// something in front of it. With no digest, a backend's own 401 would pass
// for the lock whether or not the policy took.
//
// It returns the digest's fetchedAt, which the message names: how old the
// anonymous answer it rests on is (§3.3.3).
func cardAttributes(agent *assaydv1alpha1.Agent, status *assaydv1alpha1.AgentStatus,
	rt *gatewayv1.HTTPRoute) (bool, string, string) {
	for _, rule := range rt.Spec.Rules {
		for _, ref := range rule.BackendRefs {
			for _, c := range status.Cards {
				if c.Digest != "" && WorkloadName(agent.Name, c.Revision) == string(ref.Name) {
					at := "an unrecorded time"
					if c.FetchedAt != nil {
						at = c.FetchedAt.UTC().Format(time.RFC3339)
					}
					return true, at, ""
				}
			}
		}
	}
	return false, "", "no card digest is recorded for a revision the route serves, so the " +
		"backend's anonymous answer on the card path was never observed, and the 401 could be the " +
		"backend's own"
}

// authPolicyPresent reports whether `<agent>-auth` exists, read live.
func (r *AgentReconciler) authPolicyPresent(ctx context.Context, agent *assaydv1alpha1.Agent, runNS string) (bool, error) {
	name, err := compiler.AuthPolicyName(agent.Name)
	if err != nil {
		return false, err
	}
	switch err := r.Get(ctx, types.NamespacedName{Namespace: runNS, Name: name}, NewAgentgatewayPolicy()); {
	case apierrors.IsNotFound(err):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("read policy %s in %s: %w", name, runNS, err)
	}
	return true, nil
}

// reassertServedPolicy re-asserts a served `<agent>-auth` to the policy
// status.auth records (§3.3.1): a pure function of the Agent, checked against
// appliedDigest. A policy that is missing is the Lock's, not this.
func (r *AgentReconciler) reassertServedPolicy(ctx context.Context, agent *assaydv1alpha1.Agent,
	runNS string, auth *assaydv1alpha1.AuthStatus) error {
	recorded, err := compiler.AuthPolicy(compiler.AuthInput{AgentName: agent.Name,
		AgentNamespace: agent.Namespace, AgentUID: agent.UID, RunNamespace: runNS})
	if err != nil {
		return err
	}
	digest, err := compiler.Digest(recorded)
	if err != nil {
		return err
	}
	existing := NewAgentgatewayPolicy()
	switch err := r.Get(ctx, types.NamespacedName{Namespace: runNS, Name: recorded.GetName()}, existing); {
	case apierrors.IsNotFound(err):
		return nil
	case err != nil:
		return fmt.Errorf("read policy %s in %s: %w", recorded.GetName(), runNS, err)
	}
	if digest != auth.AppliedDigest {
		// What this build renders is not what was served, so the served policy
		// cannot be reproduced here. It is left as found (§3.3.1).
		log.FromContext(ctx).Info("not re-asserting the served -auth policy: this operator renders a "+
			"different digest than status.auth records", "policy", recorded.GetName(),
			"recorded", auth.AppliedDigest, "rendered", digest)
		return nil
	}
	_, err = r.writeAuthPolicy(ctx, agent, existing, recorded, digest)
	return err
}

// refuseAdopt is `Adopt`, refused (§3.3.3): no policy is written, and the
// route keeps following promotion, as the shipped emitter kept it, with no
// marker (§3.3.1). A route found deleted is re-created that way too.
// GovernanceSkipped says the route is unauthenticated.
func (r *AgentReconciler) refuseAdopt(ctx context.Context, agent *assaydv1alpha1.Agent, runNS, name string,
	status *assaydv1alpha1.AgentStatus, conds *conditionSet, desire authDesire,
) (gatewayOutcome, error) {
	if status.ActiveRevision != "" {
		if _, _, err := r.ensureServingRoute(ctx, agent, runNS, status.ActiveRevision,
			status.ActiveRevisionDigest, name, routePublicationFor(status.Auth)); err != nil {
			return gatewayOutcome{}, err
		}
	}
	if err := r.collectRoutes(ctx, agent, runNS, name); err != nil {
		return gatewayOutcome{}, err
	}
	// §3.3.3's record: `{kind: Adopt, stage: Refused, refusedMode}`, so that a
	// deleted route is re-created as the shipped emitter did and is not an
	// exit into `Create` (ADR-0034 Amendment 3). refusedMode tracks the desired
	// mode while it is not apikey, and a desired apikey never overwrites it:
	// K2's consent is a desired apikey beside a refusedMode that is not
	// (authStep). An abandoned K2 Lock arrives here with a Lock in the slot,
	// and records the new desired mode afresh (A66).
	refused := desiredModeName(agent)
	if prev := status.Auth; prev != nil && prev.Transaction != nil && prev.Transaction.Kind == TxAdopt &&
		refused == string(compiler.AuthModeAPIKey) && prev.Transaction.RefusedMode != "" {
		refused = prev.Transaction.RefusedMode
	}
	status.Auth = &assaydv1alpha1.AuthStatus{Transaction: &assaydv1alpha1.AuthTransaction{
		Kind: TxAdopt, Stage: StageRefused, RefusedMode: refused}}
	if err := r.persistStatus(ctx, agent, status); err != nil {
		return gatewayOutcome{}, err
	}
	// How K2 ends the refusal, in the branch that is true for this record
	// (§3.3.3, A66).
	way := "While status.auth.transaction.refusedMode is not apikey, editing spec.expose.a2a.auth to " +
		"apikey locks this route in place instead (K2), and an anonymous caller then gets 401"
	if refused == string(compiler.AuthModeAPIKey) {
		way = "The spec already asked for apikey when this route was refused, and that is not taken as " +
			"consent to lock it: edit spec.expose.a2a.auth to none, wait until " +
			"status.auth.transaction.refusedMode reads none, then edit it to apikey, which locks the " +
			"route in place (K2). An edit made before a reconcile observes none is coalesced and does " +
			"not count"
	}
	if !desire.compiles() && desire.authErr == nil {
		way += ". Another mandatory concern of this route does not compile, spec.budget: remove it " +
			"first, because no Lock is entered until it does (design 03 §3.3.1)"
	}
	conds.set(assaydv1alpha1.CondGovernanceSkipped, metav1.ConditionTrue, ReasonCompilerUpgradeUnsupported,
		fmt.Sprintf("route %s/%s is published and UNAUTHENTICATED, and this operator has no record of "+
			"publishing it: it was published before this operator carried a compiler, or its status "+
			"was lost, for example by a backup restore. A first policy on a route that is already "+
			"serving is refused (design 03 §3.3.3, Adopt), so none is written, and new revisions "+
			"keep going live on it unauthenticated. Delete the Agent and create it again: it then "+
			"has no route, and is published only after its -auth is enforced. %s", runNS, name, way))
	return gatewayOutcome{}, nil
}

// desiredModeName is the desired -auth mode as `refusedMode` records it: the
// derived apikey for an Agent with no expose.a2a, and the spec's value
// otherwise, which the CRD admits only as none, oauth or apikey.
func desiredModeName(agent *assaydv1alpha1.Agent) string {
	if agent.Spec.Expose == nil || agent.Spec.Expose.A2A == nil {
		return string(compiler.AuthModeAPIKey)
	}
	return agent.Spec.Expose.A2A.Auth
}

// routePublicationFor is how the emitter publishes the serving route, chosen
// from status.auth and never from the spec (§3.3.1's marker rule):
//
//   - a `Create` short of `Publishing`: prepared, with no backendRefs;
//   - a `Create` at `Publishing`: published under its TARGET mode, so a `none`
//     route carries its marker from its first published write, before
//     status.auth records the mode at `Served`;
//   - a recorded mode: published under it;
//   - nothing recorded: published with no marker. That is only the refused
//     `Adopt`, whose route the compiler did not publish (§3.3.1, A69).
func routePublicationFor(auth *assaydv1alpha1.AuthStatus) routePublication {
	if auth != nil && auth.Transaction != nil && auth.Transaction.Kind == TxCreate {
		if auth.Transaction.Stage == StagePublishing {
			return routePublication{mode: compiler.AuthMode(auth.Transaction.TargetMode)}
		}
		return routePublication{prepared: true}
	}
	if auth != nil && auth.Mode != "" {
		return routePublication{mode: compiler.AuthMode(auth.Mode)}
	}
	return routePublication{}
}

// probeAgent sends `Create`'s anonymous probe to the Agent's card path through
// the Gateway's serving listener (§3.3.3).
func (r *AgentReconciler) probeAgent(ctx context.Context, agent *assaydv1alpha1.Agent) (AuthProbeAnswer, error) {
	prober := r.AuthProbe
	if prober == nil {
		if r.Gateway.ServingURL == "" {
			return AuthProbeAnswer{}, fmt.Errorf("--gateway-serving-url is unset, so there is no " +
				"address to probe")
		}
		prober = httpAuthProber{timeout: DefaultCardFetchTimeout}
	}
	return prober.Probe(ctx, AuthProbeRequest{
		URL:  r.Gateway.ServingURL + cardPath(agent),
		Host: r.Gateway.Hostname(agent.Name, agent.Namespace),
	})
}

// cardPath is the path the operator's own card fetch requests, and the one
// every probe requests (§3.3.3).
func cardPath(agent *assaydv1alpha1.Agent) string {
	if agent.Spec.Card.Path != "" {
		return agent.Spec.Card.Path
	}
	return "/.well-known/agent-card.json"
}

func describeProbe(agent *assaydv1alpha1.Agent, a AuthProbeAnswer, err error) string {
	path := cardPath(agent)
	switch {
	case err != nil:
		return fmt.Sprintf("the last anonymous probe of %s got no answer: %v", path, err)
	case a.Code == 500:
		return fmt.Sprintf("the last anonymous probe of %s got 500: the route has no backend, and "+
			"the gateway replica that answered has not taken the policy yet", path)
	case a.Code == 404:
		return fmt.Sprintf("the last anonymous probe of %s got 404: the route is not attached to "+
			"the serving listener yet", path)
	}
	return fmt.Sprintf("the last anonymous probe of %s got %d, not the 401 that shows the route's "+
		"-auth refusing it", path, a.Code)
}

// ensureAuthPolicy is ApplyingPolicies' write: create, or update over owned
// fields, never server-side apply (§3.2).
func (r *AgentReconciler) ensureAuthPolicy(ctx context.Context, agent *assaydv1alpha1.Agent,
	desired *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	digest, err := compiler.Digest(desired)
	if err != nil {
		return nil, err
	}
	existing := NewAgentgatewayPolicy()
	switch err := r.Get(ctx, types.NamespacedName{Namespace: desired.GetNamespace(),
		Name: desired.GetName()}, existing); {
	case apierrors.IsNotFound(err):
		obj := desired.DeepCopy()
		if err := r.Create(ctx, obj); err != nil {
			return nil, fmt.Errorf("create policy %s in %s: %w", obj.GetName(), obj.GetNamespace(), err)
		}
		return obj, nil
	case err != nil:
		return nil, fmt.Errorf("read policy %s in %s: %w", desired.GetName(), desired.GetNamespace(), err)
	}
	return r.writeAuthPolicy(ctx, agent, existing, desired, digest)
}

// writeAuthPolicy converges an existing policy to desired over the fields the
// compiler owns: the three provenance labels and all of `spec` (§3.2). The
// comparison is the digest, which covers exactly those fields, so another
// writer's label or annotation is not drift and is kept.
func (r *AgentReconciler) writeAuthPolicy(ctx context.Context, agent *assaydv1alpha1.Agent,
	existing, desired *unstructured.Unstructured, digest string) (*unstructured.Unstructured, error) {
	if err := policyCollision(agent, existing); err != nil {
		return nil, err
	}
	if have, err := compiler.Digest(existing); err == nil && have == digest {
		return existing, nil
	}
	updated := existing.DeepCopy()
	labels := updated.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	for k, v := range desired.GetLabels() {
		labels[k] = v
	}
	updated.SetLabels(labels)
	updated.Object["spec"] = runtimeDeepCopy(desired.Object["spec"])
	if err := r.Update(ctx, updated); err != nil {
		return nil, fmt.Errorf("converge policy %s in %s: %w", updated.GetName(), updated.GetNamespace(), err)
	}
	return updated, nil
}

func runtimeDeepCopy(v any) any {
	u := &unstructured.Unstructured{Object: map[string]any{"v": v}}
	return u.DeepCopy().Object["v"]
}

// policyCollision is routeCollision's rule for the policy (§3.2): a policy at
// this Agent's -auth name that ANOTHER Agent owns is refused, never taken
// over; a predecessor's, or one carrying no UID, is converged.
func policyCollision(agent *assaydv1alpha1.Agent, existing *unstructured.Unstructured) error {
	l := existing.GetLabels()
	uid, ok := l[LabelAgentUID]
	if !ok || uid == string(agent.UID) {
		return nil
	}
	if l[LabelAgent] == agent.Name && l[LabelAgentNamespace] == agent.Namespace {
		return nil
	}
	return fmt.Errorf("policy %s/%s is already Agent %s/%s's -auth, and Agent %s/%s maps to the same "+
		"name. Two Agents' emitted names collided (design 03 §3.2); the policy is left as it is "+
		"rather than taken over", existing.GetNamespace(), existing.GetName(),
		l[LabelAgentNamespace], l[LabelAgent], agent.Namespace, agent.Name)
}

// authPolicyIntact reads the stored `<agent>-auth` and reports whether it is
// still exactly the target. Absent, or changed in an owned field, is not.
func (r *AgentReconciler) authPolicyIntact(ctx context.Context, agent *assaydv1alpha1.Agent, runNS string,
	target compiler.AuthTarget) (*unstructured.Unstructured, bool, error) {
	if target.Policy == nil {
		return nil, true, nil
	}
	p := NewAgentgatewayPolicy()
	switch err := r.Get(ctx, types.NamespacedName{Namespace: runNS, Name: target.Policy.GetName()}, p); {
	case apierrors.IsNotFound(err):
		return nil, false, nil
	case err != nil:
		return nil, false, fmt.Errorf("read policy %s in %s: %w", target.Policy.GetName(), runNS, err)
	}
	d, err := compiler.Digest(p)
	if err != nil {
		return nil, false, err
	}
	return p, d == target.Digest, nil
}

// routeConverged is §3.3.2's tuple for an HTTPRoute: `Accepted=True` and
// `ResolvedRefs=True` from the assayd Gateway, both at the route's current
// generation. There is no `Programmed` condition to wait for.
func routeConverged(rt *gatewayv1.HTTPRoute, gw GatewayConfig) (bool, string) {
	where := fmt.Sprintf("route %s/%s at generation %d", rt.Namespace, rt.Name, rt.Generation)
	for _, p := range rt.Status.Parents {
		ns := rt.Namespace
		if p.ParentRef.Namespace != nil {
			ns = string(*p.ParentRef.Namespace)
		}
		if string(p.ParentRef.Name) != gw.Name || ns != gw.Namespace {
			continue
		}
		for _, t := range []string{"Accepted", "ResolvedRefs"} {
			c := meta.FindStatusCondition(p.Conditions, t)
			switch {
			case c == nil:
				return false, fmt.Sprintf("%s: Gateway %s/%s reports no %s condition", where, gw.Namespace, gw.Name, t)
			case c.ObservedGeneration != rt.Generation:
				return false, fmt.Sprintf("%s: %s is reported for generation %d", where, t, c.ObservedGeneration)
			case c.Status != metav1.ConditionTrue:
				return false, fmt.Sprintf("%s: %s is %s, reason %s: %s", where, t, c.Status, c.Reason, c.Message)
			}
		}
		return true, ""
	}
	return false, fmt.Sprintf("%s: Gateway %s/%s has written no status for it", where, gw.Namespace, gw.Name)
}

// policyConverged is §3.3.2's tuple for an AgentgatewayPolicy: an ancestor
// that is the assayd GATEWAY, not the target route, with `Accepted=True`,
// reason `Valid`, and `Attached=True`, both at the policy's current
// generation; and no synthetic `StatusSummary` ancestor, whose presence is a
// negative signal. An empty ancestor list is a failure state, not a pending
// one: an unsupported target yields no ancestors and no conditions.
//
// `reason == Valid` is the controller's translation, not a proxy's
// acknowledgement (§3.3.2). This proves control-plane convergence only, which
// is why `Create` then probes.
func policyConverged(p *unstructured.Unstructured, gw GatewayConfig) (bool, string) {
	where := fmt.Sprintf("policy %s/%s at generation %d", p.GetNamespace(), p.GetName(), p.GetGeneration())
	ancestors, _, _ := unstructured.NestedSlice(p.Object, "status", "ancestors")
	if len(ancestors) == 0 {
		return false, where + ": it reports no ancestors. A policy whose target is not supported " +
			"reports none at all, so this is a failure state if it persists, not only a pending one"
	}
	var ours map[string]any
	for _, a := range ancestors {
		m, _ := a.(map[string]any)
		ref, _ := m["ancestorRef"].(map[string]any)
		group, _ := ref["group"].(string)
		kind, _ := ref["kind"].(string)
		name, _ := ref["name"].(string)
		ns, _ := ref["namespace"].(string)
		if group == "agentgateway.dev" && name == "StatusSummary" {
			return false, where + ": it carries agentgateway's synthetic StatusSummary ancestor, " +
				"which is written when the policy attached to nothing (" + describeConditions(m) + ")"
		}
		if group == gatewayv1.GroupName && kind == "Gateway" && name == gw.Name && ns == gw.Namespace {
			ours = m
		}
	}
	if ours == nil {
		return false, fmt.Sprintf("%s: no ancestor is Gateway %s/%s", where, gw.Namespace, gw.Name)
	}
	for _, want := range []struct{ typ, reason string }{{"Accepted", "Valid"}, {"Attached", ""}} {
		c, ok := findUnstructuredCondition(ours, want.typ)
		switch {
		case !ok:
			return false, fmt.Sprintf("%s: Gateway %s/%s reports no %s condition", where, gw.Namespace, gw.Name, want.typ)
		case c.ObservedGeneration != p.GetGeneration():
			return false, fmt.Sprintf("%s: %s is reported for generation %d", where, want.typ, c.ObservedGeneration)
		case c.Status != metav1.ConditionTrue:
			return false, fmt.Sprintf("%s: %s is %s, reason %s: %s", where, want.typ, c.Status, c.Reason, c.Message)
		case want.reason != "" && c.Reason != want.reason:
			return false, fmt.Sprintf("%s: %s is True with reason %s, not %s", where, want.typ, c.Reason, want.reason)
		}
	}
	return true, ""
}

func findUnstructuredCondition(ancestor map[string]any, typ string) (metav1.Condition, bool) {
	conds, _ := ancestor["conditions"].([]any)
	for _, c := range conds {
		m, _ := c.(map[string]any)
		if t, _ := m["type"].(string); t != typ {
			continue
		}
		out := metav1.Condition{Type: typ}
		s, _ := m["status"].(string)
		out.Status = metav1.ConditionStatus(s)
		out.Reason, _ = m["reason"].(string)
		out.Message, _ = m["message"].(string)
		switch g := m["observedGeneration"].(type) {
		case int64:
			out.ObservedGeneration = g
		case float64:
			out.ObservedGeneration = int64(g)
		}
		return out, true
	}
	return metav1.Condition{}, false
}

func describeConditions(ancestor map[string]any) string {
	conds, _ := ancestor["conditions"].([]any)
	parts := make([]string, 0, len(conds))
	for _, c := range conds {
		m, _ := c.(map[string]any)
		parts = append(parts, fmt.Sprintf("%v=%v/%v", m["type"], m["status"], m["reason"]))
	}
	return strings.Join(parts, ", ")
}
