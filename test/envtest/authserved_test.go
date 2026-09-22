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
	"github.com/Quinyte/assayd/internal/compiler"
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

// partiallyValidPolicy makes the assayd Gateway report the shape A82 measured
// on agentgateway 1.5.0 and the ONE shape of this half known to be reachable
// with a policy the operator still recognises as its own: `Accepted=True` with
// a reason other than `Valid`, beside `Attached=True` on the real Gateway
// ancestor. 1.5.0 writes it whenever the policy's translation partly fails —
// a rejected entry in a key `ConfigMap` an administrator wrote does it, with
// nothing in assayd edited (`research/a80-policy-half-conformance-2026-09.md`).
//
// It is the row that was missing: `unattachPolicy` writes `Attached=False` on
// the real ancestor, which 1.5.0 was measured NOT to produce, and
// `summarisePolicy` writes the synthetic one. This one is neither, and it is
// the shape the measurement says an operator will actually meet.
func partiallyValidPolicy(t *testing.T, a *assaydv1alpha1.Agent) {
	t.Helper()
	gen := policyExists(t, runNS(a.Namespace), policyNameOf(a)).GetGeneration()
	reportPolicyAncestors(t, a, []any{map[string]any{
		"ancestorRef": map[string]any{"group": gatewayv1.GroupName, "kind": "Gateway",
			"name": "assayd", "namespace": "assayd-gateway"},
		"controllerName": "agentgateway.dev/agentgateway",
		"conditions": []any{policyCondition("Accepted", "True", "PartiallyValid", gen),
			policyCondition("Attached", "True", "Attached", gen)},
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
	// The shape A82 measured reachable on agentgateway 1.5.0 with a
	// byte-unchanged policy. Until A82 this half was driven only by shapes
	// 1.5.0 was measured not to produce, so the one an operator will actually
	// meet was composed by nothing.
	t.Run("Accepted=True with a reason other than Valid", func(t *testing.T) {
		a, r, _ := servedAPIKeyAgent(t, "a80partial")
		acceptRoute(t, a.Namespace, a.Name)
		partiallyValidPolicy(t, a)
		unattached(t, a, r, "PartiallyValid")
	})
	// ENTERED, REPORTED, REPAIRED — the sequence A82's first draft got
	// backwards, and the reason the measured bypass is not a standing hole.
	//
	// The precondition §5 states is render against render: reassertServedPolicy
	// digests compiler.AuthPolicy's output, never the stored object, and
	// compares that to status.auth.appliedDigest, which is itself a render
	// digest (§3.3.3, "not a digest of the stored object"). An out-of-band edit
	// to the policy's spec therefore does NOT take it out of the judgement. It
	// does the opposite: the guards pass, writeAuthPolicy overwrites the spec
	// wholesale, and the repaired object is what judgeServed then judges — with
	// a status whose last word is still the Gateway's, which policyReport
	// deliberately does not generation-gate for the synthetic ancestor.
	//
	// So the one pass both RAISES AuthPolicyNotAttached and CLOSES the hole it
	// reports. Mutations: make reassertServedPolicy digest `existing` instead of
	// `recorded`, and the repair assertion fails (the guard rejects the edited
	// policy and nothing is written); make writeAuthPolicy return early on a
	// spec mismatch, and it fails the same way.
	t.Run("an out-of-band spec edit is judged AND repaired on the same pass", func(t *testing.T) {
		a, r, _ := servedAPIKeyAgent(t, "a80repair")
		acceptRoute(t, a.Namespace, a.Name)
		want, err := compiler.AuthPolicy(compiler.AuthInput{AgentName: a.Name,
			AgentNamespace: a.Namespace, AgentUID: a.UID, RunNamespace: runNS(a.Namespace)})
		if err != nil {
			t.Fatal(err)
		}
		wantDigest, err := compiler.Digest(want)
		if err != nil {
			t.Fatal(err)
		}
		// The edit that produced the measured bypass on a cluster: a
		// sectionName the serving route has no rule for. The Gateway answers
		// it with the synthetic ancestor, and the route stays accepted.
		p := policyExists(t, runNS(a.Namespace), policyNameOf(a))
		refs, _, err := unstructured.NestedSlice(p.Object, "spec", "targetRefs")
		if err != nil || len(refs) != 1 {
			t.Fatalf("read the policy's targetRefs: %v (%d refs)", err, len(refs))
		}
		refs[0].(map[string]any)["sectionName"] = "no-such-rule"
		if err := unstructured.SetNestedSlice(p.Object, refs, "spec", "targetRefs"); err != nil {
			t.Fatal(err)
		}
		if err := k8s.Update(context.Background(), p); err != nil {
			t.Fatalf("point the policy at a rule the route does not have: %v", err)
		}
		if edited, err := compiler.Digest(policyExists(t, runNS(a.Namespace), policyNameOf(a))); err != nil {
			t.Fatal(err)
		} else if edited == wantDigest {
			t.Fatalf("the edit did not change the stored policy's digest, so this row measures nothing")
		}
		summarisePolicy(t, a)

		reconcileOnce(t, r, a)
		// REPORTED: the guards did not exclude it.
		c := condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue,
			"AuthPolicyNotAttached")
		mustContain(t, c, "PolicyApplyIncomplete", "no credential required")
		condIs(t, a, assaydv1alpha1.CondGovernanceSkipped, metav1.ConditionTrue, "AuthPolicyNotAttached")
		// REPAIRED: the same pass put the spec back, so the bypass measured on
		// a cluster closes itself rather than standing.
		got, err := compiler.Digest(policyExists(t, runNS(a.Namespace), policyNameOf(a)))
		if err != nil {
			t.Fatal(err)
		}
		if got != wantDigest {
			t.Errorf("the pass left the edited policy in place (digest %s, want the compiler's %s); "+
				"A82 rests on this pass repairing the spec it judges, which is what makes the "+
				"measured 200 a window and not a standing hole", got, wantDigest)
		}
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

// Both halves on ONE pass, which is A80's own measured incident and which no
// row covered: every policy-half row above calls acceptRoute first. Renaming
// the assayd Gateway's listener detaches the route AND leaves <agent>-auth
// attached to nothing, so the Gateway reports Accepted=False and writes the
// synthetic StatusSummary ancestor in the same breath.
//
// The ROUTE half leads. An Agent nothing can reach is the cause to name first,
// and the policy half's own text must not open by announcing that the route
// "may be answering with no credential required" when this pass recorded
// Accepted=False — which is what it did until A81 (the review's MAJOR 2).
//
// Mutations, each a single edit: put AuthPolicyNotAttached back before
// ServingRouteNotAccepted in incompleteOrder, which the reason and phase
// assertions must fail; and make policyUnattachedMessage ignore the route
// reading, which the message assertions must fail.
func TestBothHalvesOnOnePassNameTheRouteFirst(t *testing.T) {
	a, r, _ := servedAPIKeyAgent(t, "a80bothhalves")
	refuseRoute(t, a.Namespace, a.Name)
	summarisePolicy(t, a)
	reconcileOnce(t, r, a)

	c := condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue,
		"ServingRouteNotAccepted")
	mustContain(t, c, "PolicyApplyIncomplete", "NoMatchingParent", "AuthPolicyNotAttached")
	// NOT HasPrefix: under the corrected order the message leads with the route
	// half, so a prefix check passes whatever the policy half says and proves
	// nothing. What must not appear ANYWHERE is the claim itself.
	if strings.Contains(c.Message, "route is accepted and SERVING") {
		t.Errorf("PolicyApplyIncomplete claims the route is accepted and serving, on a pass that "+
			"recorded Accepted=False: %s", c.Message)
	}
	// Ready and Degraded follow the same order, because withholdInOrder reads
	// incompleteOrder too: the operator must not page an administrator with a
	// security incident when the real one is that nothing reaches the agent.
	condIs(t, a, assaydv1alpha1.CondReady, metav1.ConditionFalse, "ServingRouteNotAccepted")
	condIs(t, a, assaydv1alpha1.CondDegraded, metav1.ConditionTrue, "ServingRouteNotAccepted")
	if got := phaseOf(t, a); got != assaydv1alpha1.PhaseDegraded {
		t.Errorf("an Agent with both halves standing is %s, want Degraded", got)
	}
	// GovernanceSkipped still reads the policy half, which is the condition
	// that records the tier, and its message must not assert the route either.
	g := condIs(t, a, assaydv1alpha1.CondGovernanceSkipped, metav1.ConditionTrue, "AuthPolicyNotAttached")
	if strings.Contains(g.Message, "route is accepted and SERVING") {
		t.Errorf("GovernanceSkipped claims the route is accepted and serving, on a pass that "+
			"recorded Accepted=False: %s", g.Message)
	}

	// The licence is an explicit GOOD tuple on this pass, not merely "not
	// broken": an UNKNOWN reading must hedge too, or a promoting pass — and
	// every pass on a cluster whose agentgateway controller is renamed — would
	// assert a route it did not read. Without this half,
	// routeOK := routeRep != reportBroken survives the whole suite.
	//
	// Mutation, one edit: make routeOK `routeRep != reportBroken`.
	t.Run("an unknown route reading hedges too", func(t *testing.T) {
		b, rb, _ := servedAPIKeyAgent(t, "a80unknownhedge")
		acceptRoute(t, b.Namespace, b.Name)
		unattachPolicy(t, b)
		reconcileOnce(t, rb, b)
		condIs(t, b, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "AuthPolicyNotAttached")
		// Move the route's generation and report nothing new: the reading goes
		// unknown, with no routeRefused claim standing.
		rt := servingRoute(t, b.Namespace, b.Name)
		rt.Spec.Hostnames = append(rt.Spec.Hostnames, "drifted.example.com")
		if err := k8s.Update(context.Background(), rt); err != nil {
			t.Fatal(err)
		}
		reconcileOnce(t, rb, b)
		if auth := authOf(t, b); auth == nil || auth.RouteRefused {
			t.Fatalf("this half needs an UNKNOWN reading with no claim standing: %+v", auth)
		}
		c := condIs(t, b, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue,
			"AuthPolicyNotAttached")
		if strings.Contains(c.Message, "route is accepted and SERVING") {
			t.Errorf("the policy half asserts the route is accepted on a pass that did not read "+
				"it at its current generation: %s", c.Message)
		}
	})
	// Both claims are stored, which is what lets a held pass re-raise each.
	if auth := authOf(t, a); auth == nil || !auth.RouteRefused || !auth.PolicyUnattached {
		t.Errorf("both halves fired and the claim store records %+v", auth)
	}
}

// A81's MINOR-2 carries, at the one that is not benign: K2's Lock over a
// refused `Adopt` assigns status.auth wholesale, and nothing in entering a
// transaction read the route, so there is no explicit not-broken tuple to
// clear a standing claim with. The claim must survive the entry.
//
// Mutation, one edit: drop the carry from enterLock's empty-mode arm. It
// compiles, and this must fail.
func TestEnteringK2SLockKeepsAStandingClaim(t *testing.T) {
	a, r := refusedAdoptAgent(t, "a80k2claim")
	refuseRoute(t, a.Namespace, a.Name)
	reconcileOnce(t, r, a)
	if auth := authOf(t, a); auth == nil || !auth.RouteRefused {
		t.Fatalf("the claim was not stored: %+v", auth)
	}
	// refusedMode must read something other than apikey before an edit to
	// apikey counts as K2's consent (§3.3.3).
	toMode(t, a, "none")
	reconcileOnce(t, r, a)
	if tx := txOf(t, a); tx == nil || tx.RefusedMode != "none" {
		t.Fatalf("refusedMode did not track the edit: %+v", tx)
	}
	recordCardFor(t, a, liveAgent(t, a).Status.ActiveRevision, liveAgent(t, a).Status.ActiveRevisionDigest)
	toMode(t, a, "apikey")
	reconcileOnce(t, r, a)

	auth := authOf(t, a)
	if auth == nil || auth.Transaction == nil || auth.Transaction.Kind != "Lock" {
		t.Fatalf("K2's Lock was not entered: %+v", auth)
	}
	if !auth.RouteRefused {
		t.Error("entering K2's Lock dropped a standing routeRefused claim; nothing in the entry " +
			"read the route, so there was no not-broken tuple to clear it with")
	}
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

	// A81's correction 1. refuseAdopt rebuilds status.auth on EVERY pass, so
	// unless it carries the claim store its write clears the flag and the
	// judgement sets it again — two status writes a pass, each of which
	// re-enqueues the Agent through For(&Agent{}), which carries no generation
	// predicate. A refused `Adopt` on a refused route would then reconcile for
	// ever. The pass must converge: same resourceVersion, claim still standing.
	reconcileOnce(t, r, a)
	rv := liveAgent(t, a).ResourceVersion
	reconcileOnce(t, r, a)
	live := liveAgent(t, a)
	if live.ResourceVersion != rv {
		t.Errorf("a refused Adopt on a refused route wrote its status again (resourceVersion %s, "+
			"then %s): the claim store and refuseAdopt's wholesale assignment are fighting, and "+
			"each write re-enqueues the Agent", rv, live.ResourceVersion)
	}
	if live.Status.Auth == nil || !live.Status.Auth.RouteRefused {
		t.Errorf("the claim store did not survive refuseAdopt's assignment: %+v", live.Status.Auth)
	}
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
	t.Run("GovernanceSkipped is re-asserted from the stored condition", func(t *testing.T) {
		a, r, _ := servedAPIKeyAgent(t, "a80holdgov")
		acceptRoute(t, a.Namespace, a.Name)
		unattachPolicy(t, a)
		reconcileOnce(t, r, a)
		g := condIs(t, a, assaydv1alpha1.CondGovernanceSkipped, metav1.ConditionTrue, "AuthPolicyNotAttached")
		at := transitionedAt(t, a, assaydv1alpha1.CondGovernanceSkipped)
		observed := g.ObservedGeneration

		// The Agent's generation and the policy's both move, in one step and
		// with no new policy report. The Agent's is what makes the hold
		// OBSERVABLE: conds.set would stamp this pass's generation, and this
		// pass observed nothing. Without that, appendGovernance produces the
		// same status, reason and message and deleting the whole-restore branch
		// leaves the suite green (A81, the review's MINOR 1).
		mustEdit(t, a, func(x *assaydv1alpha1.Agent) { x.Spec.Runtime.Image = secondImage })
		driftPolicySpec(t, a)
		reconcileOnce(t, r, a)

		g = condIs(t, a, assaydv1alpha1.CondGovernanceSkipped, metav1.ConditionTrue, "AuthPolicyNotAttached")
		mustContain(t, g, "GovernanceSkipped", heldMark)
		sameTransition(t, a, assaydv1alpha1.CondGovernanceSkipped, at)
		if live := liveAgent(t, a); g.ObservedGeneration != observed {
			t.Errorf("a held GovernanceSkipped was stamped with generation %d; it must keep the %d "+
				"that observed it, because this pass read no report at the policy's generation "+
				"(the Agent is at %d)", g.ObservedGeneration, observed, live.Generation)
		}
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
		// THREE consecutive errored passes, because one cannot see
		// accumulation. A held message is REBUILT from the fallback and cut at
		// its marker; appending, which is what §3.3.3's restoration reads like,
		// grew it 240 bytes a pass here and 475 on the arm below, and at pass
		// 133 the API server refused every status write for the Agent — phase,
		// Ready, Degraded, WorkloadUnavailable and the card conditions all
		// froze, during the incident this judgement exists to report, and it
		// did not heal.
		var lens []int
		for i := 1; i <= 3; i++ {
			r.Reader = staleAgentReader{Reader: base}
			err := reconcileErr(t, r, a)
			r.Reader = base
			if err == nil || !strings.Contains(err.Error(), "older than the live one") {
				t.Fatalf("pass %d: the injected stale read did not reach the caller: %v", i, err)
			}
			c := condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue,
				"ServingRouteNotAccepted")
			mustContain(t, c, "PolicyApplyIncomplete", erroredMark)
			if n := strings.Count(c.Message, erroredMark); n != 1 {
				t.Fatalf("pass %d: the errored marker appears %d times, want exactly 1: %s",
					i, n, c.Message)
			}
			if n := strings.Count(c.Message, heldMark); n != 0 {
				t.Errorf("pass %d: a held message carries %d unknown-reading markers beside its "+
					"errored one", i, n)
			}
			condIs(t, a, assaydv1alpha1.CondReady, metav1.ConditionFalse, "ServingRouteNotAccepted")
			if got := phaseOf(t, a); got != assaydv1alpha1.PhaseDegraded {
				t.Errorf("pass %d: a pass whose -auth step errored left the Agent %s on a route the "+
					"Gateway is still refusing, want Degraded", i, got)
			}
			sameTransition(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, at)
			lens = append(lens, len(withoutDigits(c.Message)))
		}
		// Passes 2 and 3 are both holds of the same claim under the same note,
		// so the message is the same length. Digits are stripped because the
		// error names the Agent's resourceVersions, which this pass's own write
		// moves — the only part of the text that legitimately varies.
		if lens[1] != lens[2] {
			t.Errorf("the held message grew from %d to %d bytes between two errored passes; at "+
				"~240 bytes a pass it reaches the API server's 32768-byte limit and every status "+
				"write for this Agent then fails", lens[1], lens[2])
		}
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
		var lens []int
		for i := 1; i <= 3; i++ {
			if err := reconcileErr(t, r, a); err == nil {
				t.Fatalf("pass %d: the injected non-transient write error did not reach the caller", i)
			}
			c := condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue,
				"ServingRouteNotAccepted")
			mustContain(t, c, "PolicyApplyIncomplete", "PolicyWriteFailed")
			// The write error composes BESIDE the standing reason rather than
			// replacing it, and it composes ONCE: the caller's raiseIncomplete
			// appends it after the note, which is why a held message has to be
			// rebuilt rather than appended to.
			if n := strings.Count(c.Message, "PolicyWriteFailed"); n != 1 {
				t.Fatalf("pass %d: the write error appears %d times, want exactly 1", i, n)
			}
			sameTransition(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, at)
			lens = append(lens, len(withoutDigits(c.Message)))
		}
		r.Client = k8s
		if lens[1] != lens[2] {
			t.Errorf("the held message grew from %d to %d bytes between two failed writes; at "+
				"~475 bytes a pass it reaches the API server's 32768-byte limit", lens[1], lens[2])
		}
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

// withoutDigits is a condition message with its digits removed, so two holds of
// the same claim compare equal even though the error names the Agent's
// resourceVersions, which the errored pass's own write moves.
func withoutDigits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < '0' || r > '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// claimPruningClient is an Agent CRD that predates A80's two status.auth
// fields: the write is accepted and the two properties come back missing,
// which is what `helm upgrade` leaves behind, since it never updates a chart's
// crds/.
type claimPruningClient struct{ client.Client }

func (c *claimPruningClient) Status() client.SubResourceWriter {
	return &claimPruningWriter{SubResourceWriter: c.Client.Status(), inner: c.Client}
}

type claimPruningWriter struct {
	client.SubResourceWriter
	inner client.Client
}

func (w *claimPruningWriter) Update(ctx context.Context, obj client.Object,
	opts ...client.SubResourceUpdateOption) error {
	if err := w.SubResourceWriter.Update(ctx, obj, opts...); err != nil {
		return err
	}
	if a, ok := obj.(*assaydv1alpha1.Agent); ok && a.Status.Auth != nil {
		a.Status.Auth.RouteRefused = false
		a.Status.Auth.PolicyUnattached = false
	}
	return nil
}

// A81's correction 2, at the reach it actually has: `authKept` compares the
// claim store, so an install whose Agent CRD predates the two fields is
// refused as AuthRecordNotKept rather than going on writing to the gateway
// with an inert hold. It is consulted only from persistStatus, so the class it
// reaches is a refused `Adopt` — which calls it on every pass — a route
// re-create, a missing-policy Lock, an abandonment and every transaction
// stage, and NOT a served Agent with an empty slot, which calls it on no pass
// at all (§3.3.3).
//
// Mutation, one edit: drop the two comparisons from authKept. It compiles, and
// this must fail.
func TestAPruningCRDIsRefusedRatherThanLosingTheClaim(t *testing.T) {
	a, r := refusedAdoptAgent(t, "a80prune")
	refuseRoute(t, a.Namespace, a.Name)
	reconcileOnce(t, r, a)
	if auth := authOf(t, a); auth == nil || !auth.RouteRefused {
		t.Fatalf("the claim was not stored, so the pruning has nothing to lose: %+v", auth)
	}

	// persistStatus no-ops when the write would change nothing, and A81's carry
	// makes a refused Adopt's pass converge — so the pruning is only observable
	// on a pass that actually writes status.auth. An edit to the desired mode
	// moves refusedMode, which is such a pass and which keeps the refusal.
	mustEdit(t, a, func(x *assaydv1alpha1.Agent) {
		x.Spec.Expose = &assaydv1alpha1.ExposeSpec{A2A: &assaydv1alpha1.ExposeProtocol{Auth: "none"}}
	})
	r.Client = &claimPruningClient{Client: k8s}
	err := reconcileErr(t, r, a)
	r.Client = k8s
	if err == nil || !strings.Contains(err.Error(), "did not come back") {
		t.Fatalf("a CRD that prunes the claim store was not refused: %v", err)
	}
	condIs(t, a, assaydv1alpha1.CondReady, metav1.ConditionFalse, "AuthRecordNotKept")
	c := condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue,
		"ServingRouteNotAccepted")
	mustContain(t, c, "PolicyApplyIncomplete", "AuthRecordNotKept", "charts/assayd/crds/")
}

// The gate is evaluated TWICE — the slot as the pass found it and as it leaves
// it — and this row pins the half the post-step check cannot: a pass that
// STARTS with a transaction and ENDS without one. A transaction reaching
// `Served` got §3.3.2's full tuple at the object's current generation, which is
// the explicit not-broken reading that clears a standing claim, so nothing of
// the old claim may be re-raised on that pass.
//
// Mutation, one edit: make the gate the post-step slot alone. It compiles, and
// this must fail, because the judgement then runs on the Served pass and holds
// a claim the transaction has just disproved.
func TestThePassThatReachesServedIsNotJudged(t *testing.T) {
	a, r, stub := servedAPIKeyAgent(t, "a80servedpass")
	acceptRoute(t, a.Namespace, a.Name)
	unattachPolicy(t, a)
	reconcileOnce(t, r, a)
	if auth := authOf(t, a); auth == nil || !auth.PolicyUnattached {
		t.Fatalf("the claim was not stored: %+v", auth)
	}
	condIs(t, a, assaydv1alpha1.CondGovernanceSkipped, metav1.ConditionTrue, "AuthPolicyNotAttached")

	// The policy is deleted out of band, and the missing-policy Lock that
	// re-creates it runs to Served.
	recordCardFor(t, a, a.Status.ActiveRevision, a.Status.ActiveRevisionDigest)
	r.AuthDeadline = 4 * time.Minute
	stub.hold(a.Name, true)
	deletePolicy(t, a)
	reconcileOnce(t, r, a)
	acceptPolicy(t, a.Namespace, a.Name)
	acceptRoute(t, a.Namespace, a.Name)
	stub.hold(a.Name, false)
	for i := 0; i < 6 && txOf(t, a) != nil; i++ {
		reconcileOnce(t, r, a)
		acceptRoute(t, a.Namespace, a.Name)
		acceptPolicy(t, a.Namespace, a.Name)
	}
	auth := authOf(t, a)
	if auth == nil || auth.Transaction != nil {
		t.Fatalf("the missing-policy Lock did not reach Served: %+v", auth)
	}
	if auth.PolicyUnattached {
		t.Errorf("a claim the Lock's own policyConverged disproved survived its Served record: %+v", auth)
	}
	noA80Reason(t, a, "the pass that reached Served was judged, and held a claim the transaction "+
		"had just disproved")
}

// unit, beside them: the new route and policy predicates' THREE answers, in
// internal/controller (authserved_unit_test.go).
