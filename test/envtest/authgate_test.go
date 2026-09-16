// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package envtest

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/compiler"
	"github.com/Quinyte/assayd/internal/controller"
	"github.com/Quinyte/assayd/internal/revision"
)

// Design 03 §8.1 case 18 (A77): the route gate, in every Lock, and the Lock of
// a missing policy following status.activeRevision.
//
// (f) and (g) cover behaviour that shipped: a J2 or K2 Lock whose route moves
// in the crediting pass credited the previous revision's own 401 against the
// new revision's card digest (research/auth-lock-false-credit-2026-09.md).
// (a)–(e) and (h) cover what A77 adds. Every sub-case builds its state in the
// order given, because the order is load-bearing.

// secondImage is the digest a sub-case edits an Agent's runtime to, to mint a
// second revision without touching its auth mode.
const secondImage = "ghcr.io/acme/agent@sha256:1111111111111111111111111111111111111111111111111111111111111111"

// missingPolicyLock builds case 18's shared state: a served API-key Agent on
// r1 whose card status.cards does not record, whose <agent>-auth was then
// deleted, and whose missing-policy Lock has reached ProbingAfter. The oracle's
// switch is left HELD, so the probe is answered as a route with no policy taken
// answers it, and a sub-case releases it at the point where the refusal is the
// thing being measured. deadline is the Lock's, so a sub-case that reads the
// message at the deadline does not wait four minutes for it.
func missingPolicyLock(t *testing.T, name string, deadline time.Duration) (
	*assaydv1alpha1.Agent, *controller.AgentReconciler, *stubProber) {
	t.Helper()
	a, r, stub := servedAPIKeyAgent(t, name)
	// r1 serves, and nothing recorded a card for it: in this suite every fetch
	// fails, so this is the state the operator's own retry leaves.
	forgetCards(t, a)
	r.AuthDeadline = deadline
	stub.hold(name, true)
	deletePolicy(t, a)
	reconcileOnce(t, r, a) // enters the Lock at ApplyingPolicies and writes the policy
	acceptPolicy(t, a.Namespace, name)
	reconcileOnce(t, r, a) // Converging -> ProbingAfter, and one probe that is not a refusal
	tx := txOf(t, a)
	if tx == nil || tx.Kind != "Lock" || tx.Stage != "ProbingAfter" {
		t.Fatalf("the missing-policy Lock is not at ProbingAfter: %+v", tx)
	}
	if auth := authOf(t, a); auth.Transaction == nil {
		t.Fatalf("the Lock ended before any refusal was observed: %+v", auth)
	}
	return liveAgent(t, a), r, stub
}

// mintRevision edits the Agent's image to mint a second revision and brings it
// up, stopping short of the pass that promotes it so a sub-case can assert on
// that pass alone. With carded, the digest the operator's own fetch would have
// recorded is written first, as a fetch that had just succeeded leaves it —
// envtest has no cluster network, and cardFetchDue then makes the promoting
// pass skip the fetch.
func mintRevision(t *testing.T, r *controller.AgentReconciler, a *assaydv1alpha1.Agent,
	carded bool) (string, string) {
	t.Helper()
	mustEdit(t, a, func(x *assaydv1alpha1.Agent) { x.Spec.Runtime.Image = secondImage })
	edited := liveAgent(t, a)
	rev, digest := revision.MustHash(edited.Spec), revision.MustDigest(edited.Spec)
	reconcileOnce(t, r, a)
	markAvailable(t, a.Namespace, controller.WorkloadName(a.Name, rev), 1)
	if carded {
		recordCardFor(t, a, rev, digest)
	}
	return rev, digest
}

// backendOf is the revision the serving route's single backendRef names, or
// "" when it does not carry exactly one.
func backendOf(t *testing.T, a *assaydv1alpha1.Agent) string {
	t.Helper()
	rt := servingRoute(t, a.Namespace, a.Name)
	if rt == nil || len(rt.Spec.Rules) != 1 || len(rt.Spec.Rules[0].BackendRefs) != 1 {
		return ""
	}
	return strings.TrimPrefix(string(rt.Spec.Rules[0].BackendRefs[0].Name), a.Name+"-")
}

// acceptedGen is the route generation the assayd Gateway last reported
// Accepted for, or -1. A sub-case holds the Gateway's reported generation at
// its pre-move value simply by not accepting the route again.
func acceptedGen(t *testing.T, a *assaydv1alpha1.Agent) int64 {
	t.Helper()
	rt := servingRoute(t, a.Namespace, a.Name)
	if rt == nil {
		return -1
	}
	for _, p := range rt.Status.Parents {
		for _, c := range p.Conditions {
			if c.Type == "Accepted" && c.Status == metav1.ConditionTrue {
				return c.ObservedGeneration
			}
		}
	}
	return -1
}

// (a) The wedge ends when the active revision is carded. The missing-policy
// Lock re-points its route at status.activeRevision, and once the Gateway
// reports on the route's new generation it reaches Served on the 401.
//
// Mutation: take the route as found and write nothing, which is the shipped
// code; the Agent must then never reach Served and must hold
// PolicyApplyIncomplete, reason AuthPolicyMissing, past its deadline.
func TestTheMissingPolicyLockEndsWhenTheActiveRevisionIsCarded(t *testing.T) {
	a, r, stub := missingPolicyLock(t, "wedgeends", time.Millisecond)
	r1 := liveAgent(t, a).Status.ActiveRevision
	r2, _ := mintRevision(t, r, a, true)
	stub.hold(a.Name, false) // from here the re-created policy refuses

	reconcileOnce(t, r, a) // promotes r2, and re-points the route onto it
	live := liveAgent(t, a)
	if live.Status.ActiveRevision != r2 {
		t.Fatalf("r2 was not promoted in this pass (active %s); the case needs the route to move",
			live.Status.ActiveRevision)
	}
	if got := backendOf(t, a); got != r2 {
		t.Fatalf("the missing-policy Lock left its route on %s; it must re-point at "+
			"status.activeRevision %s (design 03 §3.3.3, A77)", got, r2)
	}
	if tx := txOf(t, a); tx == nil || tx.Stage != "ProbingAfter" {
		t.Fatalf("the re-pointing pass left the Lock somewhere other than ProbingAfter: %+v", tx)
	}

	acceptRoute(t, a.Namespace, a.Name)
	reconcileOnce(t, r, a)
	auth := authOf(t, a)
	if auth == nil || auth.Mode != "apikey" || auth.Transaction != nil {
		t.Fatalf("the Lock did not reach Served after the Gateway reported on the re-pointed route "+
			"(r1 %s, r2 %s): %+v", r1, r2, auth)
	}
	condIs(t, a, assaydv1alpha1.CondGovernanceSkipped, metav1.ConditionFalse, "AuthVerifiedOnOneReplica")
	if c := condition(liveAgent(t, a), assaydv1alpha1.CondPolicyApplyIncomplete); c != nil {
		t.Errorf("AuthPolicyMissing outlived the Lock: %+v", c)
	}
}

// (b) The re-point is skipped when the active revision is uncarded, and the
// message says so. (a)'s build with r2 uncarded as well: the route is left on
// r1, the Lock never reaches Served, and past the deadline the message names
// r1, says the route was left as found, says the state is terminal, and names
// both exits.
//
// Two mutations, and both are killed here. Re-point unconditionally, dropping
// the recorded-digest condition: the route moves to r2, so the message names
// r2 and says re-pointed. And make cardAttributes return true whenever the
// route carries any backendRef: the Lock then credits r1's 401 and reaches
// Served, so PolicyApplyIncomplete is gone. (a) survives both, so the
// condition is fixed from both sides across the pair.
func TestTheRepointIsSkippedWhenTheActiveRevisionIsUncarded(t *testing.T) {
	a, r, stub := missingPolicyLock(t, "wedgeholds", time.Millisecond)
	r1 := liveAgent(t, a).Status.ActiveRevision
	r2, _ := mintRevision(t, r, a, false)
	stub.hold(a.Name, false) // from here the re-created policy refuses

	reconcileOnce(t, r, a)
	if got := liveAgent(t, a).Status.ActiveRevision; got != r2 {
		t.Fatalf("r2 was not promoted (active %s)", got)
	}
	acceptRoute(t, a.Namespace, a.Name)
	reconcileOnce(t, r, a)

	if got := backendOf(t, a); got != r1 {
		t.Fatalf("the route was re-pointed onto %s while status.cards records no digest for "+
			"status.activeRevision %s; it must be taken as found on %s", got, r2, r1)
	}
	if auth := authOf(t, a); auth.Transaction == nil {
		t.Fatalf("the Lock reached Served on a 401 nothing could attribute: %+v", auth)
	}
	c := condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "AuthPolicyMissing")
	if c == nil {
		return
	}
	for _, want := range []string{
		"names revision " + r1,
		"left as found",
		"TERMINAL",
		"removes " + runNS(a.Namespace),
		"reverted to the revision the route names",
	} {
		if !strings.Contains(c.Message, want) {
			t.Errorf("the message does not carry %q: %s", want, c.Message)
		}
	}
	if strings.Contains(c.Message, "re-pointed onto status.activeRevision") {
		t.Errorf("the message says the route was re-pointed, and it was not: %s", c.Message)
	}
}

// (c) No backend moves while the policy is not this Agent's. The build order
// is required: authPolicyPresent matches on name alone, so a foreign policy
// present when reconcileServed runs sends the pass to reassertServedPolicy and
// no Lock is entered at all.
//
// Mutation: re-point before the stage loop, as J2's write is placed.
func TestNoBackendMovesWhileThePolicyIsNotThisAgents(t *testing.T) {
	a, r, _ := missingPolicyLock(t, "foreignown", time.Millisecond)
	r1 := liveAgent(t, a).Status.ActiveRevision
	r2, _ := mintRevision(t, r, a, true)

	// A policy at <agent>-auth carrying another Agent's UID, stored only now
	// that the Lock is in the slot at ProbingAfter.
	other := mustCreateAgent(t, newNamespace(t), "foreignownother", nil)
	stored := policyExists(t, runNS(a.Namespace), policyNameOf(a))
	if stored == nil {
		t.Fatal("the Lock wrote no policy to take over")
	}
	labels := stored.GetLabels()
	labels[compiler.LabelAgentUID] = string(other.UID)
	stored.SetLabels(labels)
	if err := k8s.Update(context.Background(), stored); err != nil {
		t.Fatalf("store a policy at <agent>-auth carrying another Agent's UID: %v", err)
	}

	reconcileOnce(t, r, a)
	if got := liveAgent(t, a).Status.ActiveRevision; got != r2 {
		t.Fatalf("r2 was not promoted (active %s)", got)
	}
	tx := txOf(t, a)
	if tx == nil || tx.Kind != "Lock" || tx.Stage != "ApplyingPolicies" {
		t.Fatalf("the Lock did not hold at ApplyingPolicies beside a foreign policy at its own "+
			"name: %+v", tx)
	}
	if got := backendOf(t, a); got != r1 {
		t.Fatalf("the route moved to %s while <agent>-auth is not this Agent's; the policy is "+
			"written before the backendRef moves (design 03 §3.3.1)", got)
	}
	if c := condition(liveAgent(t, a), assaydv1alpha1.CondPolicyApplyIncomplete); c != nil &&
		!strings.Contains(c.Message, "names revision "+r1) {
		t.Errorf("the message does not name the revision the route's single backendRef names "+
			"(%s): %s", r1, c.Message)
	}
}

// (d) The route gate stops the pass that moved the route. (a)'s build, with
// the Gateway's reported generation for <agent>-serving held at the pre-
// re-point value: the pass that re-points takes no probe answer, the stub
// records zero hits after the write, and the stage stays ProbingAfter. The
// test then writes the Gateway's status at the new generation, and only then
// does the probe run and Served follow.
//
// Mutation: read routeConverged only under StageConverging, which is the
// shipped shape; the probe then runs in the re-pointing pass.
func TestTheRouteGateStopsThePassThatMovedTheRoute(t *testing.T) {
	a, r, stub := missingPolicyLock(t, "gatemoves", time.Millisecond)
	r2, _ := mintRevision(t, r, a, true)
	stub.hold(a.Name, false) // from here the re-created policy refuses
	before := stub.count(a.Name)
	moved := acceptedGen(t, a)

	reconcileOnce(t, r, a)
	if got := backendOf(t, a); got != r2 {
		t.Fatalf("the route was not re-pointed onto r2 in this pass: %s", got)
	}
	if got := servingRoute(t, a.Namespace, a.Name).Generation; got == moved {
		t.Fatalf("the re-point did not bump the route's generation (%d); the gate has nothing "+
			"to hold on", got)
	}
	if n := stub.count(a.Name); n != before {
		t.Errorf("the pass that moved the route sent %d probe(s); no request is sent and no "+
			"answer is taken until the Gateway reports at the route's current generation "+
			"(design 03 §3.3.3, A77)", n-before)
	}
	if tx := txOf(t, a); tx == nil || tx.Stage != "ProbingAfter" {
		t.Fatalf("the gated pass did not leave the transaction at ProbingAfter: %+v", tx)
	}
	if auth := authOf(t, a); auth.Transaction == nil {
		t.Fatalf("the gated pass credited a 401 it never took: %+v", auth)
	}
	// The unmet condition the stage reports is the Gateway's silence on that
	// generation, and it names both numbers (§3.3.3).
	if c := condition(liveAgent(t, a), assaydv1alpha1.CondPolicyApplyIncomplete); c == nil ||
		!strings.Contains(c.Message, "until the assayd Gateway reports Accepted and ResolvedRefs on "+
			"the serving route at its current generation") ||
		!strings.Contains(c.Message, fmt.Sprintf("at generation %d",
			servingRoute(t, a.Namespace, a.Name).Generation)) ||
		!strings.Contains(c.Message, fmt.Sprintf("is reported for generation %d", moved)) {
		t.Errorf("the gated pass does not name the Gateway's silence at the route's current "+
			"generation as its unmet condition: %+v", c)
	}

	acceptRoute(t, a.Namespace, a.Name)
	reconcileOnce(t, r, a)
	if n := stub.count(a.Name); n == before {
		t.Fatal("no probe was sent after the Gateway reported at the route's new generation")
	}
	if auth := authOf(t, a); auth.Mode != "apikey" || auth.Transaction != nil {
		t.Fatalf("the Lock did not reach Served once the gate opened: %+v", auth)
	}
}

// deleteRouteOnPolicyRead deletes the serving route inside the window A77
// names for the re-point: after runLock's own read of the route at the top of
// the pass and before the re-point's write. <agent>-auth is read exactly once
// in a ProbingAfter pass, by authPolicyIntact, which runs between the two —
// and must, because the policy is read right before the backendRef moves
// (§3.3.1).
type deleteRouteOnPolicyRead struct {
	client.Client
	policy client.ObjectKey
	route  client.ObjectKey
	armed  bool
	fired  bool
}

func (c *deleteRouteOnPolicyRead) Get(ctx context.Context, key client.ObjectKey, obj client.Object,
	opts ...client.GetOption) error {
	if u, ok := obj.(*unstructured.Unstructured); ok && c.armed && key == c.policy &&
		u.GetKind() == "AgentgatewayPolicy" {
		c.armed, c.fired = false, true
		rt := &gatewayv1.HTTPRoute{}
		rt.Namespace, rt.Name = c.route.Namespace, c.route.Name
		if err := c.Client.Delete(ctx, rt); err != nil {
			return err
		}
	}
	return c.Client.Get(ctx, key, obj, opts...)
}

// (e) A route that goes in the window is the route re-create's. An API-key
// route is never created published (§3.3.1), so the re-point refuses to
// re-create it and hands the pass to the Create that prepares it.
//
// Mutation: treat the missing route as an error and requeue, which leaves a
// Lock in the slot.
func TestARouteThatGoesInTheRepointWindowIsTheRouteRecreates(t *testing.T) {
	a, r, _ := missingPolicyLock(t, "windowgone", time.Millisecond)
	mintRevision(t, r, a, true)
	routeName, err := compiler.ServingRouteName(a.Name)
	if err != nil {
		t.Fatal(err)
	}
	hook := &deleteRouteOnPolicyRead{
		Client: k8s,
		policy: client.ObjectKey{Namespace: runNS(a.Namespace), Name: policyNameOf(a)},
		route:  client.ObjectKey{Namespace: runNS(a.Namespace), Name: routeName},
		armed:  true,
	}
	r.Client = hook

	reconcileOnce(t, r, a)
	if !hook.fired {
		t.Fatal("the route was never deleted in the window; the case measured nothing")
	}
	tx := txOf(t, a)
	if tx == nil || tx.Kind != "Create" {
		t.Fatalf("a route gone in the re-point's window did not become the route re-create's "+
			"Create: %+v", tx)
	}
	if auth := authOf(t, a); tx.TargetMode != "apikey" || tx.TargetDigest != auth.AppliedDigest {
		t.Errorf("the re-create's target is not status.auth's: %+v", tx)
	}
	if servingRoute(t, a.Namespace, a.Name) == nil {
		t.Fatal("the route was not re-created")
	}
	if routePublished(t, a.Namespace, a.Name) {
		t.Errorf("the re-created route carries backendRefs; an API-key route is never created " +
			"published (design 03 §3.3.1)")
	}
}

// (f) The shipped hole, J2. A J2 Lock at ProbingAfter whose route moves r1 →
// r2 in the crediting pass, with r2 carded and the stub answering 401 and the
// Gateway's reported generation held at the pre-promotion value. Before A77
// the Lock recorded mode: apikey in that pass, on the previous revision's own
// 401 (research/auth-lock-false-credit-2026-09.md); under A77 it takes no
// answer, stays at ProbingAfter, and reaches Served only after the Gateway
// reports at the new generation.
//
// Mutation: gate the route check on isReCreation, so the guard runs for a
// missing policy's Lock alone. It compiles, and (a)–(e) all survive it.
func TestTheRouteGateHoldsAJ2LockWhoseRouteMoves(t *testing.T) {
	a, r, stub := servedNoneAgent(t, "j2gate", nil)
	// r1 has no recorded card, so no 401 through it is attributable.
	forgetCards(t, a)
	toMode(t, a, "apikey")
	edited := liveAgent(t, a)
	r2, d2 := revision.MustHash(edited.Spec), revision.MustDigest(edited.Spec)

	lockPass(t, r, a) // ProbingBefore -> ApplyingPolicies -> Converging
	if tx := txOf(t, a); tx == nil || tx.Kind != "Lock" || tx.BeforeObserved {
		t.Fatalf("want a J2 Lock that could not observe the before-state: %+v", tx)
	}
	acceptRoute(t, a.Namespace, a.Name)
	acceptPolicy(t, a.Namespace, a.Name)
	stub.hold(a.Name, false)
	lockPass(t, r, a) // Converging -> ProbingAfter, one unattributable 401
	if tx := txOf(t, a); tx == nil || tx.Stage != "ProbingAfter" {
		t.Fatalf("want the J2 Lock at ProbingAfter: %+v", tx)
	}
	if auth := authOf(t, a); auth.Mode != "none" {
		t.Fatalf("r1's unattributable 401 was credited before any promotion: %+v", auth)
	}

	recordCardFor(t, a, r2, d2)
	markAvailable(t, a.Namespace, controller.WorkloadName(a.Name, r2), 1)
	before := stub.count(a.Name)
	lockPass(t, r, a) // promotes r2, moves the route onto it, and must not probe

	if got := backendOf(t, a); got != r2 {
		t.Fatalf("the J2 Lock's route did not follow the promotion to %s: %s", r2, got)
	}
	if n := stub.count(a.Name); n != before {
		t.Errorf("the pass that moved the route sent %d probe(s) and credited what it got; no "+
			"Lock takes an answer until the Gateway reports at the route's current generation "+
			"(design 03 §3.3.3, A77)", n-before)
	}
	if auth := authOf(t, a); auth.Mode != "apikey" {
		if tx := txOf(t, a); tx == nil || tx.Stage != "ProbingAfter" {
			t.Fatalf("the gated pass did not leave the J2 Lock at ProbingAfter: %+v", tx)
		}
	} else {
		t.Fatalf("the J2 Lock recorded apikey in the pass that moved its route: %+v", authOf(t, a))
	}

	acceptRoute(t, a.Namespace, a.Name)
	lockPass(t, r, a)
	if auth := authOf(t, a); auth.Mode != "apikey" || auth.Transaction != nil {
		t.Fatalf("the J2 Lock did not reach Served once the Gateway reported at the route's new "+
			"generation: %+v", auth)
	}
}

// (g) The shipped hole, K2. (f) on a K2 Lock — a refused Adopt whose owner
// edits to apikey — which takes the same branch of runLock and differs only in
// its message and in having no recorded mode when it starts. It records
// mode: apikey at Served like every other credited Lock.
//
// Mutation: apply the route gate to J2 alone, by keying it on
// status.auth.mode != "".
func TestTheRouteGateHoldsAK2LockWhoseRouteMoves(t *testing.T) {
	a, r, stub := legacyAgent(t, "k2gate", noneSpec)
	forgetCards(t, a)
	toMode(t, a, "apikey")
	edited := liveAgent(t, a)
	r2, d2 := revision.MustHash(edited.Spec), revision.MustDigest(edited.Spec)

	lockPass(t, r, a)
	if tx := txOf(t, a); tx == nil || tx.Kind != "Lock" || tx.BeforeObserved {
		t.Fatalf("want a K2 Lock that could not observe the before-state: %+v", tx)
	}
	acceptRoute(t, a.Namespace, a.Name)
	acceptPolicy(t, a.Namespace, a.Name)
	stub.hold(a.Name, false)
	lockPass(t, r, a)
	if tx := txOf(t, a); tx == nil || tx.Stage != "ProbingAfter" {
		t.Fatalf("want the K2 Lock at ProbingAfter: %+v", tx)
	}
	if auth := authOf(t, a); auth.Mode != "" {
		t.Fatalf("K2 recorded a mode before Served: %+v", auth)
	}

	recordCardFor(t, a, r2, d2)
	markAvailable(t, a.Namespace, controller.WorkloadName(a.Name, r2), 1)
	before := stub.count(a.Name)
	lockPass(t, r, a)

	if got := backendOf(t, a); got != r2 {
		t.Fatalf("the K2 Lock's route did not follow the promotion to %s: %s", r2, got)
	}
	if n := stub.count(a.Name); n != before {
		t.Errorf("the pass that moved the K2 Lock's route sent %d probe(s): the gate is not "+
			"keyed on the recorded mode, which K2 does not have (design 03 §3.3.3, A77)", n-before)
	}
	if auth := authOf(t, a); auth.Mode == "apikey" {
		t.Fatalf("the K2 Lock recorded apikey in the pass that moved its route: %+v", auth)
	}
	if tx := txOf(t, a); tx == nil || tx.Stage != "ProbingAfter" {
		t.Fatalf("the gated pass did not leave the K2 Lock at ProbingAfter: %+v", tx)
	}

	acceptRoute(t, a.Namespace, a.Name)
	lockPass(t, r, a)
	if auth := authOf(t, a); auth.Mode != "apikey" || auth.Transaction != nil {
		t.Fatalf("the K2 Lock did not reach Served once the Gateway reported: %+v", auth)
	}
}

// (h) The gate does not cost A75 its hold, on the gated pass or after it. A J2
// Lock at ProbingAfter under a standing Gateway-level counting policy, whose
// route then moves on a promotion, with the Gateway's reported generation held
// at the pre-promotion value — without that clause a harness that accepts the
// route between the two passes never gates.
//
// Two mutations, both killed here. Mutation 1: move the gate above A75's
// Gateway read, so the gated pass breaks before gatewayAuthPolicies and the
// hold is not raised on it. Mutation 2: rewind the transaction to Converging
// on a gated pass, so storedHold carries nothing, the Converging arm breaks on
// the tuple before A75's read, and the hold is gone on the pass after.
// Mutation 1 is survived by (a)–(g); mutation 2 is killed here and also fails
// (d), (f) and (g), which assert ProbingAfter too.
func TestTheRouteGateKeepsA75sHoldOnTheGatedPassAndAfterIt(t *testing.T) {
	a, r, stub := servedNoneAgent(t, "gatehold", nil)
	forgetCards(t, a)
	toMode(t, a, "apikey")
	edited := liveAgent(t, a)
	r2, d2 := revision.MustHash(edited.Spec), revision.MustDigest(edited.Spec)

	lockPass(t, r, a)
	acceptRoute(t, a.Namespace, a.Name)
	acceptPolicy(t, a.Namespace, a.Name)
	stub.hold(a.Name, false)
	gatewayAuthPolicy(t, liveAgent(t, a), stub)
	lockPass(t, r, a)
	if tx := txOf(t, a); tx == nil || tx.Stage != "ProbingAfter" {
		t.Fatalf("want the J2 Lock at ProbingAfter under the Gateway-level policy: %+v", tx)
	}
	condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "GatewayAuthPolicy")

	recordCardFor(t, a, r2, d2)
	markAvailable(t, a.Namespace, controller.WorkloadName(a.Name, r2), 1)
	lockPass(t, r, a) // the gated pass: the route moves and the Gateway has not reported
	if got := backendOf(t, a); got != r2 {
		t.Fatalf("the route did not move in this pass, so nothing was gated: %s", got)
	}
	assertHeldAtProbingAfter(t, a, "the gated pass")

	lockPass(t, r, a) // the pass after it
	assertHeldAtProbingAfter(t, a, "the pass after the gated one")
}

// assertHeldAtProbingAfter is (h)'s reading: A75's hold raised, Ready
// withheld, Degraded, and the transaction still at ProbingAfter.
func assertHeldAtProbingAfter(t *testing.T, a *assaydv1alpha1.Agent, when string) {
	t.Helper()
	if c := condition(liveAgent(t, a), assaydv1alpha1.CondPolicyApplyIncomplete); c == nil ||
		c.Status != metav1.ConditionTrue || c.Reason != "GatewayAuthPolicy" {
		t.Errorf("%s dropped A75's hold: PolicyApplyIncomplete is %+v", when, c)
	}
	if c := condition(liveAgent(t, a), assaydv1alpha1.CondReady); c == nil ||
		c.Status != metav1.ConditionFalse || c.Reason != "GatewayAuthPolicy" {
		t.Errorf("%s did not withhold Ready under GatewayAuthPolicy: %+v", when, c)
	}
	if got := phaseOf(t, a); got != assaydv1alpha1.PhaseDegraded {
		t.Errorf("%s left the phase %s, want Degraded", when, got)
	}
	if tx := txOf(t, a); tx == nil || tx.Stage != "ProbingAfter" {
		t.Errorf("%s left the transaction at %+v; it stays at ProbingAfter so the hold keeps "+
			"being raised (design 03 §3.3.3, A77)", when, tx)
	}
}
