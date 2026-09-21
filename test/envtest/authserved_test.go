// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package envtest

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/controller"
)

// Design 03 §8.1 case 19 (A80): a served Agent's route and its `<agent>-auth`
// are judged again after `Served`.
//
// Every sub-case but (h) drives an Agent to `Served` under `apikey` with the
// existing harness, leaves the transaction slot empty, and then writes route
// or policy status itself — which is what §8.1 says this layer uniquely
// proves, since no controller writes that status here.
//
// The three markers a held report can carry are distinct on purpose, and the
// rows read them: carriedNote for a pass that returned before the -auth step,
// heldMark for a pass that reached the step and read no report at the object's
// current generation, and erroredMark for a pass whose step could not
// complete.
const (
	carriedMark = "carried as the last pass that read the Gateway stored it"
	heldMark    = "carried from the last pass that got a report at this object's generation"
	erroredMark = "carried across a pass whose -auth step could not complete"
)

// reportRoute writes the assayd Gateway's status.parents entry for the serving
// route at the route's CURRENT generation, with the two legs of §3.3.2's tuple
// set as given. It is acceptRoute's general form.
func reportRoute(t *testing.T, ns, agentName string, accepted, resolved metav1.ConditionStatus,
	acceptedReason, resolvedReason string) {
	t.Helper()
	rt := servingRoute(t, ns, agentName)
	if rt == nil {
		t.Fatalf("no route to report on for %s", agentName)
	}
	group, kind := gatewayv1.Group(gatewayv1.GroupName), gatewayv1.Kind("Gateway")
	gwNS, section := gatewayv1.Namespace("assayd-gateway"), gatewayv1.SectionName("http")
	now := metav1.Now()
	rt.Status.Parents = []gatewayv1.RouteParentStatus{{
		ParentRef: gatewayv1.ParentReference{Group: &group, Kind: &kind, Name: "assayd",
			Namespace: &gwNS, SectionName: &section},
		ControllerName: "agentgateway.dev/agentgateway",
		Conditions: []metav1.Condition{
			{Type: "Accepted", Status: accepted, Reason: acceptedReason, Message: acceptedReason,
				ObservedGeneration: rt.Generation, LastTransitionTime: now},
			{Type: "ResolvedRefs", Status: resolved, Reason: resolvedReason, Message: resolvedReason,
				ObservedGeneration: rt.Generation, LastTransitionTime: now},
		},
	}}
	if err := k8s.Status().Update(context.Background(), rt); err != nil {
		t.Fatalf("write the route's status: %v", err)
	}
}

// refuseRoute is the defect's measured trigger: Accepted=False, reason
// NoMatchingParent, at the route's current generation, which is what renaming
// the assayd Gateway's listener produced on k3d on 2026-09-14.
func refuseRoute(t *testing.T, ns, agentName string) {
	t.Helper()
	reportRoute(t, ns, agentName, metav1.ConditionFalse, metav1.ConditionTrue,
		"NoMatchingParent", "ResolvedRefs")
}

// dropRouteReport removes the assayd Gateway's entry from status.parents.
func dropRouteReport(t *testing.T, ns, agentName string) {
	t.Helper()
	rt := servingRoute(t, ns, agentName)
	rt.Status.Parents = []gatewayv1.RouteParentStatus{}
	if err := k8s.Status().Update(context.Background(), rt); err != nil {
		t.Fatalf("drop the route's status: %v", err)
	}
}

// reportPolicyAncestors writes the policy's whole status.ancestors list.
func reportPolicyAncestors(t *testing.T, a *assaydv1alpha1.Agent, ancestors []any) {
	t.Helper()
	p := policyExists(t, runNS(a.Namespace), policyNameOf(a))
	if p == nil {
		t.Fatalf("no policy to report on for %s", a.Name)
	}
	p.Object["status"] = map[string]any{"ancestors": ancestors}
	if err := k8s.Status().Update(context.Background(), p); err != nil {
		t.Fatalf("write the policy's status: %v", err)
	}
}

func policyCondition(typ, status, reason string, gen int64) map[string]any {
	return map[string]any{"type": typ, "status": status, "reason": reason, "message": reason,
		"lastTransitionTime": time.Now().UTC().Format(time.RFC3339), "observedGeneration": gen}
}

// unattachPolicy makes the assayd Gateway report that it accepted this Agent's
// `<agent>-auth` and attached it to NOTHING, at the policy's current
// generation: the fail-OPEN half.
func unattachPolicy(t *testing.T, a *assaydv1alpha1.Agent) {
	t.Helper()
	gen := policyExists(t, runNS(a.Namespace), policyNameOf(a)).GetGeneration()
	reportPolicyAncestors(t, a, []any{map[string]any{
		"ancestorRef": map[string]any{"group": gatewayv1.GroupName, "kind": "Gateway",
			"name": "assayd", "namespace": "assayd-gateway"},
		"controllerName": "agentgateway.dev/agentgateway",
		"conditions": []any{policyCondition("Accepted", "True", "Valid", gen),
			policyCondition("Attached", "False", "NotAttached", gen)},
	}})
}

// summarisePolicy replaces the ancestor with agentgateway's synthetic
// StatusSummary, which it writes when a policy attached to nothing (§3.3.2).
func summarisePolicy(t *testing.T, a *assaydv1alpha1.Agent) {
	t.Helper()
	gen := policyExists(t, runNS(a.Namespace), policyNameOf(a)).GetGeneration()
	reportPolicyAncestors(t, a, []any{map[string]any{
		"ancestorRef":    map[string]any{"group": "agentgateway.dev", "kind": "StatusSummary", "name": "StatusSummary"},
		"controllerName": "agentgateway.dev/agentgateway",
		"conditions": []any{policyCondition("Accepted", "True", "Valid", gen),
			policyCondition("Attached", "False", "NotAttached", gen)},
	}})
}

// driftPolicySpec moves the policy's metadata.generation by a SPEC edit. A CRD
// with the status subresource does not increment generation for a change
// confined to metadata, so an unowned label would leave the reading known and
// the half would assert nothing. reassertServedPolicy reverts this on the next
// pass, which moves the generation again — past the Gateway's last report
// either way.
func driftPolicySpec(t *testing.T, a *assaydv1alpha1.Agent) {
	t.Helper()
	p := policyExists(t, runNS(a.Namespace), policyNameOf(a))
	if err := unstructured.SetNestedStringSlice(p.Object, []string{"true"},
		"spec", "traffic", "authorization", "policy", "matchExpressions"); err != nil {
		t.Fatal(err)
	}
	if err := k8s.Update(context.Background(), p); err != nil {
		t.Fatalf("drift the policy's spec: %v", err)
	}
}

func transitionedAt(t *testing.T, a *assaydv1alpha1.Agent, typ assaydv1alpha1.ConditionType) metav1.Time {
	t.Helper()
	c := condition(liveAgent(t, a), typ)
	if c == nil {
		t.Fatalf("%s is not set, so there is no transition time to record", typ)
	}
	return c.LastTransitionTime
}

func sameTransition(t *testing.T, a *assaydv1alpha1.Agent, typ assaydv1alpha1.ConditionType, want metav1.Time) {
	t.Helper()
	if got := transitionedAt(t, a, typ); !got.Equal(&want) {
		t.Errorf("%s's LastTransitionTime moved from %s to %s; a condition whose clock resets never "+
			"reaches design 10's five-minute rule", typ, want, got)
	}
}

// noReason fails when either of A80's reasons appears anywhere in the Agent's
// conditions — as a reason OR inside a message. It is an ABSENCE assertion,
// because the rule is that neither is produced at all in the state it guards.
func noA80Reason(t *testing.T, a *assaydv1alpha1.Agent, why string) {
	t.Helper()
	live := liveAgent(t, a)
	for _, c := range live.Status.Conditions {
		for _, r := range []string{"ServingRouteNotAccepted", "AuthPolicyNotAttached"} {
			if c.Reason == r || strings.Contains(c.Message, r) {
				t.Errorf("%s: %s carries %s (%s/%s: %s)", why, c.Type, r, c.Status, c.Reason, c.Message)
			}
		}
	}
}

func policyResourceVersion(t *testing.T, a *assaydv1alpha1.Agent) string {
	t.Helper()
	p := policyExists(t, runNS(a.Namespace), policyNameOf(a))
	if p == nil {
		t.Fatalf("the served policy of %s is gone", a.Name)
	}
	return p.GetResourceVersion()
}

// (a) A route the Gateway refuses. The measured defect: before A80 this Agent
// reported Ready=True, phase Ready and GovernanceSkipped=False for as long as
// it existed, and the state did not heal.
//
// Mutations, each a single edit: make the served-route judgement report
// "accepted" unconditionally, which the first half must fail; and leave
// gatewayOutcome.served unset in the judgement, which compiles and must fail
// the phase and Degraded assertions, because withholdReady then reports phase
// Pending and CLEARS Degraded.
func TestAServedRouteTheGatewayRefusesIsDegraded(t *testing.T) {
	a, r, _ := servedAPIKeyAgent(t, "a80route")
	refuseRoute(t, a.Namespace, a.Name)
	reconcileOnce(t, r, a)

	c := condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "ServingRouteNotAccepted")
	mustContain(t, c, "PolicyApplyIncomplete", "Accepted=False", "NoMatchingParent", "NOT withdrawn")
	condIs(t, a, assaydv1alpha1.CondReady, metav1.ConditionFalse, "ServingRouteNotAccepted")
	condIs(t, a, assaydv1alpha1.CondDegraded, metav1.ConditionTrue, "ServingRouteNotAccepted")
	if got := phaseOf(t, a); got != assaydv1alpha1.PhaseDegraded {
		t.Errorf("a served Agent whose route the Gateway refuses is %s, want Degraded", got)
	}
	// GovernanceSkipped is not moved at all: it keeps the value the served
	// policy earned, and gains a note.
	g := condIs(t, a, assaydv1alpha1.CondGovernanceSkipped, metav1.ConditionFalse, "AuthVerifiedOnOneReplica")
	mustContain(t, g, "GovernanceSkipped", "not accepting")
	if !routePublished(t, a.Namespace, a.Name) {
		t.Error("the route was withdrawn; the Gateway is already not carrying it, and stripping it " +
			"would enter a Create whose Publishing waits on a 401 through a route it will not accept")
	}
	if policyExists(t, runNS(a.Namespace), policyNameOf(a)) == nil {
		t.Error("the served <agent>-auth was deleted")
	}

	// It clears when the Gateway reports the route accepted again.
	acceptRoute(t, a.Namespace, a.Name)
	reconcileOnce(t, r, a)
	if c := condition(liveAgent(t, a), assaydv1alpha1.CondPolicyApplyIncomplete); c != nil {
		t.Errorf("ServingRouteNotAccepted survived an accepted route: %+v", c)
	}
	condIs(t, a, assaydv1alpha1.CondReady, metav1.ConditionTrue, "Available")
}

// (b) ResolvedRefs is the other leg.
//
// Mutation: judge Accepted alone and ignore ResolvedRefs. It compiles, and
// this must fail.
func TestAServedRouteWhoseRefsDoNotResolveIsDegraded(t *testing.T) {
	a, r, _ := servedAPIKeyAgent(t, "a80refs")
	reportRoute(t, a.Namespace, a.Name, metav1.ConditionTrue, metav1.ConditionFalse,
		"Accepted", "BackendNotFound")
	reconcileOnce(t, r, a)

	c := condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "ServingRouteNotAccepted")
	mustContain(t, c, "PolicyApplyIncomplete", "ResolvedRefs=False", "BackendNotFound")
	condIs(t, a, assaydv1alpha1.CondReady, metav1.ConditionFalse, "ServingRouteNotAccepted")
	if got := phaseOf(t, a); got != assaydv1alpha1.PhaseDegraded {
		t.Errorf("a served Agent whose route's refs do not resolve is %s, want Degraded", got)
	}
}

// (c) A report a generation behind is UNKNOWN, and must not page. This is the
// sub-case that pins the human's (A1) against the obvious call.
//
// Mutation: judge with routeConverged — the full §3.3.2 tuple — instead of
// "not reported broken at the current generation". It compiles, it is exactly
// option (A3), and this must fail.
func TestAStaleRouteReportRaisesNothing(t *testing.T) {
	a, r, stub := servedAPIKeyAgent(t, "a80stale")
	acceptRoute(t, a.Namespace, a.Name)
	reconcileOnce(t, r, a)
	before := acceptedGen(t, a)

	// Force a route write: promote a second revision, so the route's
	// backendRef moves and its generation becomes n+1 — and write no new
	// status, which is what acceptedGen is for.
	r2, _ := mintRevision(t, r, a, true)
	stub.hold(a.Name, false)
	reconcileOnce(t, r, a)
	if live := liveAgent(t, a); live.Status.ActiveRevision != r2 {
		t.Fatalf("r2 was not promoted (active %s); the case needs the route to move", live.Status.ActiveRevision)
	}
	if got := acceptedGen(t, a); got != before {
		t.Fatalf("the Gateway's reported generation moved to %d from %d; the case needs it held", got, before)
	}
	if gen := servingRoute(t, a.Namespace, a.Name).Generation; gen == before {
		t.Fatalf("the promotion did not move the route's generation past %d", before)
	}

	for i := 0; i < 2; i++ {
		reconcileOnce(t, r, a)
		if c := condition(liveAgent(t, a), assaydv1alpha1.CondPolicyApplyIncomplete); c != nil {
			t.Fatalf("a report one generation behind was treated as a failure on reconcile %d: %+v", i, c)
		}
		condIs(t, a, assaydv1alpha1.CondReady, metav1.ConditionTrue, "Available")
	}
}

// (d) A missing report is unknown too.
//
// What this row deliberately does NOT claim: that a Gateway which is readable
// and has stopped reporting is detected. A80 says it is not, for any mode, and
// this row is the pin on that admission — (A4-spec), the one option that would
// have seen the measured defect with the gateway controller dead, was offered
// and not taken.
//
// Mutation: treat an absent entry as a refusal. It compiles, and this must fail.
func TestAMissingRouteReportRaisesNothing(t *testing.T) {
	a, r, _ := servedAPIKeyAgent(t, "a80noreport")
	dropRouteReport(t, a.Namespace, a.Name)
	reconcileOnce(t, r, a)

	if c := condition(liveAgent(t, a), assaydv1alpha1.CondPolicyApplyIncomplete); c != nil {
		t.Errorf("an absent Gateway entry was treated as a refusal: %+v", c)
	}
	condIs(t, a, assaydv1alpha1.CondReady, metav1.ConditionTrue, "Available")
}

// (e) It is not gated on apikey. A served `auth: none` Agent's reconcileServed
// path never reads the route through currentServingRoute, so the object the
// judgement gets is ensureServingRoute's return.
//
// Mutation: gate the re-derivation on status.auth.mode == apikey. It compiles,
// and this must fail.
func TestAServedNoneAgentIsDegradedOnARefusedRoute(t *testing.T) {
	a, r, _ := servedNoneAgent(t, "a80none", nil)
	refuseRoute(t, a.Namespace, a.Name)
	reconcileOnce(t, r, a)

	condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "ServingRouteNotAccepted")
	condIs(t, a, assaydv1alpha1.CondReady, metav1.ConditionFalse, "ServingRouteNotAccepted")
	if got := phaseOf(t, a); got != assaydv1alpha1.PhaseDegraded {
		t.Errorf("a served auth: none Agent whose route the Gateway refuses is %s, want Degraded", got)
	}
	g := condIs(t, a, assaydv1alpha1.CondGovernanceSkipped, metav1.ConditionTrue, "AuthOptedOut")
	mustContain(t, g, "GovernanceSkipped", "not accepting")
}

// (f) The policy half, and it does not withdraw. The fail-OPEN half: the route
// is accepted and serving while its authentication is attached to nothing.
//
// Mutations, each a single edit: make the served-policy judgement report
// "attached" unconditionally, which the first two halves must fail; strip the
// route back to prepared on a policy the Gateway does not attach, which the
// backendRefs assertion must fail; reuse AuthPolicyMissing as the reason,
// which the reason assertion must fail; and set GovernanceSkipped
// unconditionally rather than composing with a standing reason, which the
// third half must fail.
func TestAServedPolicyTheGatewayDoesNotAttachIsReported(t *testing.T) {
	unattached := func(t *testing.T, a *assaydv1alpha1.Agent, r *controller.AgentReconciler, says string) {
		t.Helper()
		// Writing the policy's STATUS perturbs no digest — compiler.Digest
		// projects spec and three labels and never status — so the pass makes
		// no write of its own. Status().Update bumps resourceVersion once by
		// itself, which is the baseline.
		rv := policyResourceVersion(t, a)
		reconcileOnce(t, r, a)
		c := condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "AuthPolicyNotAttached")
		mustContain(t, c, "PolicyApplyIncomplete", says, "no credential required", "announced, not closed")
		condIs(t, a, assaydv1alpha1.CondGovernanceSkipped, metav1.ConditionTrue, "AuthPolicyNotAttached")
		condIs(t, a, assaydv1alpha1.CondReady, metav1.ConditionFalse, "AuthPolicyNotAttached")
		if got := phaseOf(t, a); got != assaydv1alpha1.PhaseDegraded {
			t.Errorf("a served Agent whose -auth attaches to nothing is %s, want Degraded", got)
		}
		if !routePublished(t, a.Namespace, a.Name) {
			t.Error("the route was stripped back to prepared; A80 withdraws nothing, because doing so " +
				"hands anyone who can edit the Gateway a switch that takes the Agent off the air")
		}
		reconcileOnce(t, r, a)
		if got := policyResourceVersion(t, a); got != rv {
			t.Errorf("the policy was rewritten (resourceVersion %s, want %s); it is neither deleted "+
				"nor rewritten", got, rv)
		}
	}
	t.Run("Attached=False", func(t *testing.T) {
		a, r, _ := servedAPIKeyAgent(t, "a80unattached")
		acceptRoute(t, a.Namespace, a.Name)
		unattachPolicy(t, a)
		unattached(t, a, r, "Attached=False")
	})
	t.Run("the synthetic StatusSummary ancestor", func(t *testing.T) {
		a, r, _ := servedAPIKeyAgent(t, "a80summary")
		acceptRoute(t, a.Namespace, a.Name)
		summarisePolicy(t, a)
		unattached(t, a, r, "StatusSummary")
	})
	t.Run("a standing ForeignTrafficPolicy keeps the reason", func(t *testing.T) {
		a, r, _ := servedAPIKeyAgent(t, "a80compose")
		acceptRoute(t, a.Namespace, a.Name)
		plantForeign(t, a)
		unattachPolicy(t, a)
		reconcileOnce(t, r, a)
		g := condIs(t, a, assaydv1alpha1.CondGovernanceSkipped, metav1.ConditionTrue, "ForeignTrafficPolicy")
		mustContain(t, g, "GovernanceSkipped", "AuthPolicyNotAttached")
		// PolicyApplyIncomplete names both, foreign first: incompleteOrder.
		c := condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "ForeignTrafficPolicy")
		mustContain(t, c, "PolicyApplyIncomplete", "AuthPolicyNotAttached")
	})
}

// (g) A transaction that runs stages is left alone. The assertion is an
// ABSENCE, because the rule is that neither reason is produced at all while
// such a transaction is in the slot.
//
// Mutation, one edit: run the re-derivation whatever the slot holds, which
// makes one of the two appear and must fail the row.
func TestATransactionInTheSlotIsNotSecondGuessed(t *testing.T) {
	a, r, _ := missingPolicyLock(t, "a80inflight", 4*time.Minute)
	refuseRoute(t, a.Namespace, a.Name)
	reconcileOnce(t, r, a)

	condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "AuthPolicyMissing")
	noA80Reason(t, a, "a missing-policy Lock at ProbingAfter was second-guessed by A80's judgement")
}

// (h) A refused `Adopt` is judged, and it is the state with the LEAST other
// coverage: it runs no stage, calls no routeConverged, sets no withhold and
// returns an empty outcome, and A75's read skips it too. Only the route half
// applies to it: it has no `<agent>-auth`.
//
// Mutation, one edit: gate the re-derivation on an empty transaction slot. It
// compiles, and this must fail.
func TestARefusedAdoptIsDegradedOnARefusedRoute(t *testing.T) {
	a, r := refusedAdoptAgent(t, "a80adopt")
	refuseRoute(t, a.Namespace, a.Name)
	reconcileOnce(t, r, a)

	condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "ServingRouteNotAccepted")
	condIs(t, a, assaydv1alpha1.CondReady, metav1.ConditionFalse, "ServingRouteNotAccepted")
	if got := phaseOf(t, a); got != assaydv1alpha1.PhaseDegraded {
		t.Errorf("a refused Adopt whose route the Gateway refuses is %s, want Degraded", got)
	}
	// Its GovernanceSkipped message is the only one naming that Agent's way
	// out, and an availability failure must not erase it.
	g := condIs(t, a, assaydv1alpha1.CondGovernanceSkipped, metav1.ConditionTrue, "CompilerUpgradeUnsupported")
	mustContain(t, g, "GovernanceSkipped", "Delete the Agent and create it again", "not accepting")
}

// (i) The report survives a pass that returns before the -auth step, which is
// what makes it a page: design 10 alerts on PolicyApplyIncomplete open for
// more than five minutes, and a condition whose clock resets never gets there.
//
// Mutations, each one edit: leave the two reasons out of carriedReason, which
// the first two halves must fail; and carry them in seedStoredAbove's W1 block
// rather than a block of their own, which the third must fail.
func TestAServedReportSurvivesAnEarlyReturn(t *testing.T) {
	earlyReturn := func(t *testing.T, r *controller.AgentReconciler, a *assaydv1alpha1.Agent) {
		t.Helper()
		orig := r.LabelAuthorityPresent
		r.LabelAuthorityPresent = func(context.Context) (bool, error) { return false, nil }
		reconcileOnce(t, r, a)
		r.LabelAuthorityPresent = orig
	}
	t.Run("the route half", func(t *testing.T) {
		a, r, _ := servedAPIKeyAgent(t, "a80earlyroute")
		refuseRoute(t, a.Namespace, a.Name)
		reconcileOnce(t, r, a)
		at := transitionedAt(t, a, assaydv1alpha1.CondPolicyApplyIncomplete)
		earlyReturn(t, r, a)
		c := condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "ServingRouteNotAccepted")
		mustContain(t, c, "PolicyApplyIncomplete", carriedMark)
		sameTransition(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, at)
	})
	t.Run("the policy half", func(t *testing.T) {
		a, r, _ := servedAPIKeyAgent(t, "a80earlypolicy")
		acceptRoute(t, a.Namespace, a.Name)
		unattachPolicy(t, a)
		reconcileOnce(t, r, a)
		at := transitionedAt(t, a, assaydv1alpha1.CondPolicyApplyIncomplete)
		earlyReturn(t, r, a)
		c := condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "AuthPolicyNotAttached")
		mustContain(t, c, "PolicyApplyIncomplete", carriedMark)
		g := condIs(t, a, assaydv1alpha1.CondGovernanceSkipped, metav1.ConditionTrue, "AuthPolicyNotAttached")
		mustContain(t, g, "GovernanceSkipped", carriedMark)
		sameTransition(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, at)
	})
	// The third half: the two classes seedStoredAbove's W1 block would
	// exclude, because it returns unless the mode is apikey.
	t.Run("a served auth: none Agent", func(t *testing.T) {
		a, r, _ := servedNoneAgent(t, "a80earlynone", nil)
		refuseRoute(t, a.Namespace, a.Name)
		reconcileOnce(t, r, a)
		at := transitionedAt(t, a, assaydv1alpha1.CondPolicyApplyIncomplete)
		earlyReturn(t, r, a)
		c := condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "ServingRouteNotAccepted")
		mustContain(t, c, "PolicyApplyIncomplete", carriedMark)
		sameTransition(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, at)
	})
	t.Run("a refused Adopt", func(t *testing.T) {
		a, r := refusedAdoptAgent(t, "a80earlyadopt")
		refuseRoute(t, a.Namespace, a.Name)
		reconcileOnce(t, r, a)
		at := transitionedAt(t, a, assaydv1alpha1.CondPolicyApplyIncomplete)
		earlyReturn(t, r, a)
		c := condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "ServingRouteNotAccepted")
		mustContain(t, c, "PolicyApplyIncomplete", carriedMark)
		sameTransition(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, at)
	})
}

// (j) A policy at the `-auth` name that is not this Agent's is never judged —
// which is the whole point of putting §3.2's UID clause in the precondition
// rather than relying on "the object the pass already holds".
//
// Mutation, one edit: judge the object reassertServedPolicy holds BEFORE its
// UID guard returns. It compiles, and this must fail.
func TestAPolicyAtTheAuthNameThatIsNotThisAgentsIsNotJudged(t *testing.T) {
	a, r, _ := servedAPIKeyAgent(t, "a80foreignname")
	acceptRoute(t, a.Namespace, a.Name)
	// Replace the served <agent>-auth with one carrying another Agent's UID,
	// still targeting <agent>-serving, so foreignTrafficPolicies reports it.
	p := authPolicyFor(t, liveAgent(t, a))
	deletePolicy(t, a)
	p.SetLabels(map[string]string{controller.LabelAgentUID: "11111111-2222-3333-4444-555555555555"})
	if err := k8s.Create(context.Background(), p); err != nil {
		t.Fatalf("plant the policy at this Agent's -auth name: %v", err)
	}
	unattachPolicy(t, a)
	rv := policyResourceVersion(t, a)
	reconcileOnce(t, r, a)

	condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "ForeignTrafficPolicy")
	condIs(t, a, assaydv1alpha1.CondGovernanceSkipped, metav1.ConditionTrue, "ForeignTrafficPolicy")
	noA80Reason(t, a, "a policy at this Agent's -auth name that does not carry its UID was judged")
	if got := policyResourceVersion(t, a); got != rv {
		t.Errorf("a policy that is not this Agent's was written to (resourceVersion %s, want %s)", got, rv)
	}
}

// (k) An unknown reading HOLDS a standing report and does not retract it. This
// is the row that keeps the fix from re-entering the defect: joining
// carriedReason also makes the -auth step WITHDRAW both reasons on the way
// into every pass, so a pass that read unknown and simply said nothing would
// retract a report that is still true — and a promotion is enough to trigger
// it.
//
// Mutation for the first two halves, one edit: take the retract branch — let
// an unknown reading assert nothing. It compiles, and the first half must
// fail, because the promoting pass clears the condition and Ready goes back to
// True on a route the Gateway is still refusing.
// Mutation for the third half, one conditional: hold by REASON match alone,
// which finds nothing stored under either new reason and must fail it.
// Mutation for the fourth half, one edit: re-append the claim to whatever the
// pass derived instead of re-asserting the stored condition whole — the Agent
// then claims verified authentication on a route whose policy is attached to
// nothing.
func TestAnUnknownReadingHoldsAStandingReport(t *testing.T) {
	t.Run("the route half", func(t *testing.T) {
		a, r, stub := servedAPIKeyAgent(t, "a80holdroute")
		refuseRoute(t, a.Namespace, a.Name)
		reconcileOnce(t, r, a)
		at := transitionedAt(t, a, assaydv1alpha1.CondPolicyApplyIncomplete)
		refused := acceptedGen(t, a)

		// Move the route's generation, and write NO new route status.
		mintRevision(t, r, a, true)
		stub.hold(a.Name, false)
		for i := 0; i < 3; i++ {
			reconcileOnce(t, r, a)
			condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "ServingRouteNotAccepted")
			condIs(t, a, assaydv1alpha1.CondReady, metav1.ConditionFalse, "ServingRouteNotAccepted")
			if got := phaseOf(t, a); got != assaydv1alpha1.PhaseDegraded {
				t.Fatalf("reconcile %d left the Agent %s on a route the Gateway is still refusing", i, got)
			}
			sameTransition(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, at)
		}
		if gen := servingRoute(t, a.Namespace, a.Name).Generation; gen == refused {
			t.Fatalf("the promotion did not move the route's generation past %d", refused)
		}
		c := condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "ServingRouteNotAccepted")
		mustContain(t, c, "PolicyApplyIncomplete", heldMark)

		// Only a report at the NEW generation clears it.
		acceptRoute(t, a.Namespace, a.Name)
		reconcileOnce(t, r, a)
		if c := condition(liveAgent(t, a), assaydv1alpha1.CondPolicyApplyIncomplete); c != nil {
			t.Errorf("a good tuple at the current generation did not clear the report: %+v", c)
		}
		condIs(t, a, assaydv1alpha1.CondReady, metav1.ConditionTrue, "Available")
	})
	t.Run("the policy half", func(t *testing.T) {
		a, r, _ := servedAPIKeyAgent(t, "a80holdpolicy")
		acceptRoute(t, a.Namespace, a.Name)
		unattachPolicy(t, a)
		reconcileOnce(t, r, a)
		at := transitionedAt(t, a, assaydv1alpha1.CondPolicyApplyIncomplete)

		driftPolicySpec(t, a)
		for i := 0; i < 2; i++ {
			reconcileOnce(t, r, a)
			condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "AuthPolicyNotAttached")
			condIs(t, a, assaydv1alpha1.CondReady, metav1.ConditionFalse, "AuthPolicyNotAttached")
			sameTransition(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, at)
		}
		c := condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "AuthPolicyNotAttached")
		mustContain(t, c, "PolicyApplyIncomplete", heldMark)

		// Accepted and attached at the policy's current generation clears it.
		acceptPolicy(t, a.Namespace, a.Name)
		reconcileOnce(t, r, a)
		if c := condition(liveAgent(t, a), assaydv1alpha1.CondPolicyApplyIncomplete); c != nil {
			t.Errorf("a good policy tuple at the current generation did not clear the report: %+v", c)
		}
		condIs(t, a, assaydv1alpha1.CondReady, metav1.ConditionTrue, "Available")
	})
	// The third half: the CLAIM rather than the condition. In the composed
	// state the stored condition's reason belongs to another cause, so a
	// reason match finds nothing to restore — which is why the claim is a
	// store on status.auth and not a message fragment.
	t.Run("the claim, not the condition", func(t *testing.T) {
		a, r, _ := servedAPIKeyAgent(t, "a80holdclaim")
		key := gatewayPolicy(t, "gw-"+a.Name, map[string]any{
			"targetRefs": onGateway(suiteGatewayName, ""), "traffic": apiKeyTraffic(t, a)})
		refuseRoute(t, a.Namespace, a.Name)
		reconcileOnce(t, r, a)
		c := condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "GatewayAuthPolicy")
		mustContain(t, c, "PolicyApplyIncomplete", "ServingRouteNotAccepted")
		at := transitionedAt(t, a, assaydv1alpha1.CondPolicyApplyIncomplete)

		// Remove the outranking cause and move the route's generation in ONE
		// step, so no pass sees the refusal beside a readable report.
		removePolicy(t, key)
		rt := servingRoute(t, a.Namespace, a.Name)
		rt.Spec.Hostnames = append(rt.Spec.Hostnames, "drifted.example.com")
		if err := k8s.Update(context.Background(), rt); err != nil {
			t.Fatal(err)
		}
		reconcileOnce(t, r, a)

		c = condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "ServingRouteNotAccepted")
		mustContain(t, c, "PolicyApplyIncomplete", heldMark)
		sameTransition(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, at)
		condIs(t, a, assaydv1alpha1.CondReady, metav1.ConditionFalse, "ServingRouteNotAccepted")
	})
	// The fourth half: GovernanceSkipped in the ordinary policy half, where
	// the stored reason IS AuthPolicyNotAttached and assessGovernance has
	// already derived False/AuthVerifiedOnOneReplica fresh on this pass.
	t.Run("GovernanceSkipped is re-asserted whole", func(t *testing.T) {
		a, r, _ := servedAPIKeyAgent(t, "a80holdgov")
		acceptRoute(t, a.Namespace, a.Name)
		unattachPolicy(t, a)
		reconcileOnce(t, r, a)
		condIs(t, a, assaydv1alpha1.CondGovernanceSkipped, metav1.ConditionTrue, "AuthPolicyNotAttached")
		at := transitionedAt(t, a, assaydv1alpha1.CondGovernanceSkipped)

		driftPolicySpec(t, a)
		reconcileOnce(t, r, a)
		g := condIs(t, a, assaydv1alpha1.CondGovernanceSkipped, metav1.ConditionTrue, "AuthPolicyNotAttached")
		mustContain(t, g, "GovernanceSkipped", heldMark)
		sameTransition(t, a, assaydv1alpha1.CondGovernanceSkipped, at)
	})
}

// (l) A pass whose -auth step ERRORED holds the report too, and on the
// transient arm that is the same defect one path over: the caller takes none
// of its three arms for a served Agent with an empty slot, so the condition is
// dropped as owned-and-not-sticky and Ready and the phase keep the True the
// rollout switch set.
//
// Mutations, each a single edit. Leave the error path exactly as origin/main
// has it — one deletion of the restoration AND its withhold — which drops the
// condition on the transient arm and replaces it on the other; both halves
// must fail. And leave the caller's third arm gated on GatewayAuthPolicy, so
// the restoration populates a withhold nothing applies: the condition comes
// back and Ready stays True beside it, which the first half must fail.
func TestAnErroredPassHoldsAServedReport(t *testing.T) {
	t.Run("the transient arm", func(t *testing.T) {
		a, r, _ := servedAPIKeyAgent(t, "a80errtransient")
		refuseRoute(t, a.Namespace, a.Name)
		reconcileOnce(t, r, a)
		at := transitionedAt(t, a, assaydv1alpha1.CondPolicyApplyIncomplete)

		// errStaleAgent, which requireFreshAgent evaluates on every gateway
		// pass, is transient by transientRouteWrite.
		base := r.Reader
		if base == nil {
			base = k8s
		}
		r.Reader = staleAgentReader{Reader: base}
		if err := reconcileErr(t, r, a); err == nil || !strings.Contains(err.Error(), "older than the live one") {
			t.Fatalf("the injected stale read did not reach the caller: %v", err)
		}
		r.Reader = base

		c := condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "ServingRouteNotAccepted")
		mustContain(t, c, "PolicyApplyIncomplete", erroredMark)
		condIs(t, a, assaydv1alpha1.CondReady, metav1.ConditionFalse, "ServingRouteNotAccepted")
		if got := phaseOf(t, a); got != assaydv1alpha1.PhaseDegraded {
			t.Errorf("a pass whose -auth step errored left the Agent %s on a route the Gateway is "+
				"still refusing, want Degraded", got)
		}
		sameTransition(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, at)
	})
	t.Run("the non-transient arm composes", func(t *testing.T) {
		a, r, _ := servedAPIKeyAgent(t, "a80errhard")
		refuseRoute(t, a.Namespace, a.Name)
		reconcileOnce(t, r, a)
		at := transitionedAt(t, a, assaydv1alpha1.CondPolicyApplyIncomplete)

		// On (a)'s state writeAuthPolicy short-circuits on the digest match and
		// ensureServingRoute converges to a no-op, so an injecting client would
		// see no Update at all: perturb the policy's spec first and fail that
		// write.
		driftPolicySpec(t, a)
		r.Client = &policyUpdateForbidden{Client: k8s}
		if err := reconcileErr(t, r, a); err == nil {
			t.Fatal("the injected non-transient write error did not reach the caller")
		}
		r.Client = k8s

		c := condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "ServingRouteNotAccepted")
		mustContain(t, c, "PolicyApplyIncomplete", "PolicyWriteFailed")
		sameTransition(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, at)
	})
}

// policyUpdateForbidden fails every write of an AgentgatewayPolicy with a
// Forbidden, which transientRouteWrite does NOT call transient: the
// non-transient arm of the caller's error tail.
type policyUpdateForbidden struct{ client.Client }

func (c *policyUpdateForbidden) Update(ctx context.Context, obj client.Object,
	opts ...client.UpdateOption) error {
	if _, ok := obj.(*unstructured.Unstructured); ok {
		gr := schema.GroupResource{Group: "gateway.agentgateway.dev", Resource: "agentgatewaypolicies"}
		return apierrors.NewForbidden(gr, obj.GetName(), errors.New("no grant"))
	}
	return c.Client.Update(ctx, obj, opts...)
}

// unit, beside them: the new route and policy predicates' THREE answers, in
// internal/controller (authserved_unit_test.go).
