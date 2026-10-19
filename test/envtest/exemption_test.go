// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package envtest

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/controller"
	"github.com/Quinyte/assayd/internal/revision"
)

// Design 16 §1.1 build step 7: the exemption for a J2 or K2 Lock that can
// finish only through a promotion, clause 5's hold in status.eval.blocked,
// the missing-policy wedge's message, and the refused Adopt. Each row builds
// a design 03 transaction with the pinned probe oracle (stubProber) and the
// A2A stub answering in every revision's place (a2aStub): its card for the
// Agent reconciler's fetch and the eval controller's run-start fetch, and the
// cases. There is no gateway here, so the test writes route and policy status
// as agentgateway would, and what is under test is the two controllers'
// reaction to them.

// authEvalRig is a served Agent on a gateway-enabled Agent reconciler whose
// probe is the oracle and whose card fetches reach the A2A stub, beside an
// eval controller reading the same Gateway, and its suite "smoke".
type authEvalRig struct {
	ns, name string
	a        *assaydv1alpha1.Agent
	r        *controller.AgentReconciler
	prober   *stubProber
	e        *controller.EvalReconciler
	stub     *a2aStub
	// r1 is the revision first served.
	r1, d1 string
}

// newAuthEvalRig serves a new Agent — under auth: none when none is set, and
// under apikey otherwise — with its first revision's card recorded by the
// operator's own fetch, and no gate yet.
func newAuthEvalRig(t *testing.T, name string, none bool, cases ...assaydv1alpha1.EvalCase) *authEvalRig {
	t.Helper()
	ns := newNamespace(t)
	putSuite(t, ns, "smoke", cases...)
	a := mustCreateAgent(t, ns, name, func(x *assaydv1alpha1.Agent) {
		if none {
			noneSpec(x)
		}
	})
	r, prober := createReconciler()
	r.EvalSuiteInstalled = func() bool { return true }
	stub := newA2AStub()
	r.CardClient = stub
	r.CardFetchTimeout = time.Second
	e := newEvalController(t, stub)
	e.Gateway = r.Gateway
	promote(t, r, a)
	if none {
		acceptRoute(t, ns, name)
		reconcileOnce(t, r, a)
	} else {
		driveToServed(t, r, prober, a)
	}
	live := liveAgent(t, a)
	g := &authEvalRig{ns: ns, name: name, a: live, r: r, prober: prober, e: e, stub: stub,
		r1: live.Status.ActiveRevision, d1: live.Status.ActiveRevisionDigest}
	if !cardRecorded(&live.Status, g.d1) {
		t.Fatalf("setup: the first revision's card was not recorded: %+v", live.Status.Cards)
	}
	return g
}

func (g *authEvalRig) agentPass(t *testing.T) { t.Helper(); reconcileOnce(t, g.r, g.a) }
func (g *authEvalRig) evalPass(t *testing.T) ctrl.Result {
	t.Helper()
	return evalOnce(t, g.e, g.a)
}

// evalPassErr is an eval pass that may return an error: clause 5's failed
// reads do, after recording the hold.
func (g *authEvalRig) evalPassErr(t *testing.T) error {
	t.Helper()
	_, err := g.e.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(g.a)})
	return err
}

// gate declares the suite as the Agent's gate. Gates are policy surface, so
// this mints no revision (design 02 A12).
func (g *authEvalRig) gate(t *testing.T) {
	t.Helper()
	mustEdit(t, g.a, func(x *assaydv1alpha1.Agent) {
		x.Spec.Gates = []assaydv1alpha1.GateRef{{EvalSuiteRef: "smoke"}}
	})
	g.agentPass(t)
}

// clearCard clears the card of the revision serving now, while it is still
// the desired one, by a failing re-read: the re-read is made due by
// back-dating fetchedAt past CardDriftInterval, as card_test.go does.
func (g *authEvalRig) clearCard(t *testing.T) {
	t.Helper()
	rev := liveAgent(t, g.a).Status.ActiveRevisionDigest
	old := metav1.NewTime(time.Now().Add(-controller.CardDriftInterval - time.Minute))
	writeAgentStatus(t, g.a, func(st *assaydv1alpha1.AgentStatus) {
		for i := range st.Cards {
			st.Cards[i].FetchedAt = &old
		}
	})
	g.stub.refuseCard(g.name, true)
	g.agentPass(t)
	g.stub.refuseCard(g.name, false)
	if cardRecorded(&liveAgent(t, g.a).Status, rev) {
		t.Fatal("setup: the failing re-read did not clear the serving revision's card")
	}
}

// candidate edits the Agent with edit, brings the revision it mints up and
// has its card recorded, and returns its name and digest.
func (g *authEvalRig) candidate(t *testing.T, edit func(*assaydv1alpha1.Agent)) (string, string) {
	t.Helper()
	mustEdit(t, g.a, edit)
	spec := liveAgent(t, g.a).Spec
	rev, digest := revision.MustHash(spec), revision.MustDigest(spec)
	g.agentPass(t)
	markAvailable(t, g.ns, controller.WorkloadName(g.name, rev), 1)
	g.agentPass(t)
	if !cardRecorded(&liveAgent(t, g.a).Status, digest) {
		t.Fatalf("setup: the candidate %s's card was not recorded", rev)
	}
	return rev, digest
}

func (g *authEvalRig) hits(rev string) int { return g.stub.sentTo(g.name, rev) }

// noHits runs n eval and Agent passes and fails on any case sent to rev.
func (g *authEvalRig) noHits(t *testing.T, rev, when string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		_ = g.evalPassErr(t)
		g.agentPass(t)
	}
	if h := g.hits(rev); h != 0 {
		t.Fatalf("%s: %d cases were sent to %s", when, h, rev)
	}
}

// runToVerdict runs eval passes, each followed by an Agent pass, until the
// candidate is no longer pending.
func (g *authEvalRig) runToVerdict(t *testing.T, rev string) {
	t.Helper()
	for i := 0; i < 12; i++ {
		g.evalPass(t)
		g.agentPass(t)
		if liveAgent(t, g.a).Status.ActiveRevision == rev {
			return
		}
	}
	t.Fatalf("the candidate %s was not evaluated and promoted: eval %+v, GatesPassed %+v", rev,
		liveAgent(t, g.a).Status.Eval, condition(liveAgent(t, g.a), assaydv1alpha1.CondGatesPassed))
}

func (g *authEvalRig) waitingForAuth(t *testing.T, when string) *metav1.Condition {
	t.Helper()
	c := condition(liveAgent(t, g.a), assaydv1alpha1.CondGatesPassed)
	if c == nil || c.Status != metav1.ConditionFalse || c.Reason != controller.ReasonEvalWaitingForAuth {
		t.Fatalf("%s: GatesPassed is %+v, want False/EvalWaitingForAuth", when, c)
	}
	return c
}

// exemptJ2 builds the J2 row's state (T42): a gated Agent served under
// auth: none, whose serving revision R's card is cleared by a failing re-read
// while R is still desired, edited to apikey. After the edit R is not desired,
// so its card can be cleared only before it. ProbingBefore finds no recorded
// card to match, so beforeObserved stays false, and the Lock reaches
// ProbingAfter with a 401 it cannot attribute. plant, when set, runs once the
// policy is written and before it converges. It returns the edit's revision C.
func exemptJ2(t *testing.T, name string, deadline time.Duration,
	plant func(*authEvalRig)) (*authEvalRig, string, string) {
	t.Helper()
	return exemptJ2With(t, name, deadline, plant, echo("a", "one"), echo("b", "two"))
}

// exemptJ2With is exemptJ2 with the suite's cases given.
func exemptJ2With(t *testing.T, name string, deadline time.Duration, plant func(*authEvalRig),
	cases ...assaydv1alpha1.EvalCase) (*authEvalRig, string, string) {
	t.Helper()
	g := newAuthEvalRig(t, name, true, cases...)
	g.gate(t)
	g.clearCard(t)
	g.r.AuthDeadline = deadline
	mustEdit(t, g.a, func(x *assaydv1alpha1.Agent) {
		x.Spec.Expose = &assaydv1alpha1.ExposeSpec{A2A: &assaydv1alpha1.ExposeProtocol{Auth: "apikey"}}
	})
	spec := liveAgent(t, g.a).Spec
	c, cd := revision.MustHash(spec), revision.MustDigest(spec)
	lockPass(t, g.r, g.a)
	tx := txOf(t, g.a)
	if tx == nil || tx.Kind != "Lock" || tx.BeforeObserved {
		t.Fatalf("setup: the edit did not enter a J2 Lock that observed nothing: %+v", tx)
	}
	if plant != nil {
		plant(g)
	}
	markAvailable(t, g.ns, controller.WorkloadName(name, c), 1)
	acceptRoute(t, g.ns, name)
	acceptPolicy(t, g.ns, name)
	lockPass(t, g.r, g.a)
	lockPass(t, g.r, g.a)
	live := liveAgent(t, g.a)
	if tx := live.Status.Auth.Transaction; tx == nil || tx.Stage != "ProbingAfter" || tx.BeforeObserved {
		t.Fatalf("setup: the J2 Lock is not at ProbingAfter with nothing observed: %+v", tx)
	}
	if live.Status.CandidateRevision != c || !cardRecorded(&live.Status, cd) || cardRecorded(&live.Status, g.d1) {
		t.Fatalf("setup: want candidate %s carded beside an uncarded R %s: %+v", c, g.r1, live.Status)
	}
	return g, c, cd
}

// T42: a J2 Lock that can finish only through a promotion. The eval claims
// beside it, the J2 edit's revision C is evaluated and promoted, the route's
// backendRef then names C, and the Lock credits its 401 on C's recorded card
// and reaches Served.
//
// Mutations: wait on every Lock, which leaves the Lock at AuthLockUnverified
// and C at EvalWaitingForAuth (the hit count and the promotion); or skip
// runLock's re-assertion of the route, which the backendRef assertion catches.
func TestAJ2LockThatCanFinishOnlyThroughAPromotionIsEvaluatedBeside(t *testing.T) {
	g, c, _ := exemptJ2(t, "jtwoexempt", 0, nil)
	g.agentPass(t)
	g.waitingForAuth(t, "before the eval controller's first clause-5 pass")
	g.runToVerdict(t, c)
	if g.hits(c) != 2 {
		t.Errorf("C was sent %d cases, want its two", g.hits(c))
	}
	if got := backendOf(t, g.a); got != c {
		t.Fatalf("the route names %s after C's promotion; the J2 Lock re-asserts it on "+
			"status.activeRevision %s (design 03 §3.3.3)", got, c)
	}
	if auth := authOf(t, g.a); auth.Transaction == nil {
		t.Fatalf("the Lock credited a 401 in the pass that moved its route: %+v", auth)
	}
	acceptRoute(t, g.ns, g.name)
	g.agentPass(t)
	if auth := authOf(t, g.a); auth.Mode != "apikey" || auth.Transaction != nil {
		t.Fatalf("the Lock did not reach Served on C's recorded card: %+v", auth)
	}
	condIs(t, g.a, assaydv1alpha1.CondGovernanceSkipped, metav1.ConditionFalse, "AuthVerifiedOnOneReplica")
}

// T46: the exemption's edges. In the J2 row's state no case is claimed while
// the Lock is at ApplyingPolicies or Converging, and a J2 Lock at
// ProbingAfter with beforeObserved set and the serving card cleared is not
// exempt. The stage and beforeObserved are written to status by the test: the
// eval controller reads clauses 1 to 4 from status alone.
//
// Mutations: exempt the Lock at any stage; or ignore beforeObserved.
func TestTheExemptionsEdges(t *testing.T) {
	g, c, _ := exemptJ2(t, "jtwoedges", 0, nil)
	for _, edge := range []struct {
		what string
		edit func(*assaydv1alpha1.AuthTransaction)
	}{
		{"at ApplyingPolicies", func(tx *assaydv1alpha1.AuthTransaction) { tx.Stage = "ApplyingPolicies" }},
		{"at Converging", func(tx *assaydv1alpha1.AuthTransaction) { tx.Stage = "Converging" }},
		{"at ProbingAfter with beforeObserved set", func(tx *assaydv1alpha1.AuthTransaction) {
			tx.Stage, tx.BeforeObserved, tx.BeforeRevision = "ProbingAfter", true, g.r1
		}},
	} {
		writeAgentStatus(t, g.a, func(st *assaydv1alpha1.AgentStatus) { edge.edit(st.Auth.Transaction) })
		for i := 0; i < 3; i++ {
			g.evalPass(t)
		}
		if h := g.hits(c); h != 0 {
			t.Fatalf("%s: %d cases were claimed beside a Lock that is not exempt", edge.what, h)
		}
		if e := liveAgent(t, g.a).Status.Eval; e != nil {
			t.Fatalf("%s: the eval controller wrote status.eval beside a Lock that is not exempt: %+v",
				edge.what, e)
		}
	}
	writeAgentStatus(t, g.a, func(st *assaydv1alpha1.AgentStatus) {
		st.Auth.Transaction.BeforeObserved, st.Auth.Transaction.BeforeRevision = false, ""
	})
	g.evalPass(t)
	if g.hits(c) != 1 {
		t.Fatalf("control: the exempt state, restored, claimed %d cases, want 1", g.hits(c))
	}
}

// T47: a J2 Lock in the J2 row's state held at ProbingAfter by a
// Gateway-level policy, with the Lock's deadline shortened and allowed to
// pass. No case is sent while the policy stands, before the deadline and
// after it, when PolicyApplyIncomplete's reason has become
// AuthLockUnverified; across the interval GatesPassed reads
// False/EvalWaitingForAuth — before the eval controller's first clause-5
// pass because blockedFor does not match, and after it from blocked, whose
// message names the policy. Once the policy is removed, the run starts,
// passes and promotes, and the Lock reaches Served.
//
// Mutations: drop the fifth clause (the zero-hit interval); key it on
// PolicyApplyIncomplete's reason, as A9 did (the interval after the
// deadline); a gate branch that checks only clauses 1 to 4 and shows
// EvalRunning while nothing is claimed (the GatesPassed assertion); or read an
// unmatched blockedFor as no hold (the interval's first moments).
func TestAGatewayLevelPolicyHoldsTheExemptLockAcrossItsDeadline(t *testing.T) {
	var key client.ObjectKey
	g, c, _ := exemptJ2(t, "jtwoabove", 5*time.Second, func(g *authEvalRig) {
		key = gatewayAuthPolicy(t, g.a, g.prober)
	})
	if !time.Now().Before(txOf(t, g.a).Deadline.Time) {
		t.Fatal("setup: the Lock's deadline passed before the interval before it could be measured")
	}
	g.agentPass(t)
	first := g.waitingForAuth(t, "before the eval controller's first clause-5 pass")
	if !strings.Contains(first.Message, "has not yet evaluated clause 5 for this Lock") {
		t.Errorf("before clause 5 is evaluated, the message does not say so: %s", first.Message)
	}
	g.noHits(t, c, "while the Gateway-level policy stands", 3)
	held := g.waitingForAuth(t, "after the eval controller's first clause-5 pass")
	beforeDeadline(t, g, "the interval before the deadline")
	if !strings.Contains(held.Message, key.Namespace+"/"+key.Name) {
		t.Errorf("the hold does not name the Gateway-level policy %s: %s", key, held.Message)
	}
	if e := liveAgent(t, g.a).Status.Eval; e == nil || e.Blocked == nil ||
		e.Blocked.Reason != controller.ReasonEvalWaitingForAuth {
		t.Fatalf("status.eval.blocked does not record clause 5's hold: %+v", e)
	}

	time.Sleep(time.Until(txOf(t, g.a).Deadline.Time) + 100*time.Millisecond)
	g.agentPass(t)
	condIs(t, g.a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "AuthLockUnverified")
	g.noHits(t, c, "past the Lock's deadline, while the policy stands", 3)
	after := g.waitingForAuth(t, "past the deadline")
	if !strings.Contains(after.Message, key.Name) {
		t.Errorf("past the deadline the hold no longer names the policy: %s", after.Message)
	}

	removePolicy(t, key)
	g.runToVerdict(t, c)
	acceptRoute(t, g.ns, g.name)
	g.agentPass(t)
	if auth := authOf(t, g.a); auth.Mode != "apikey" || auth.Transaction != nil {
		t.Fatalf("the Lock did not reach Served once the hold cleared and C promoted: %+v", auth)
	}
}

// T47's variant: the Gateway's get refused through the eval controller's
// injected reader. The pass records the hold, returns the error, and sends
// nothing.
func TestAnUnreadableGatewayHoldsTheExemptLock(t *testing.T) {
	g, c, _ := exemptJ2(t, "jtwounread", 0, nil)
	gr := schema.GroupResource{Group: gatewayv1.GroupName, Resource: "gateways"}
	e, err := controller.NewEvalReconciler(k8s, gatewayUnreadable{Reader: k8s,
		err: apierrors.NewForbidden(gr, suiteGatewayName, errors.New("no grant"))},
		func() bool { return true }, g.r.Gateway)
	if err != nil {
		t.Fatal(err)
	}
	e.A2A.HTTP, e.CardFetchTimeout = g.stub, time.Second
	g.e = e
	for i := 0; i < 3; i++ {
		if err := g.evalPassErr(t); err == nil {
			t.Fatal("a pass whose Gateway read failed returned no error")
		}
		g.agentPass(t)
	}
	if h := g.hits(c); h != 0 {
		t.Fatalf("%d cases were sent while the Gateway could not be read", h)
	}
	held := g.waitingForAuth(t, "with the Gateway unreadable")
	if !strings.Contains(held.Message, "could not be read (Forbidden)") {
		t.Errorf("the hold does not name the unreadable Gateway's class: %s", held.Message)
	}
}

// T48: a J2 Lock in the J2 row's state beside a foreign traffic policy that
// targets `<agent>-serving`, with the deadline shortened and passed. No case
// is sent while the foreign policy stands, before the deadline and after it,
// and GatesPassed reads False/EvalWaitingForAuth throughout. Once it is
// removed, the run starts.
//
// Mutations: drop the foreign-policy half of the fifth clause (the zero-hit
// interval); or a gate branch that checks only clauses 1 to 4 (GatesPassed).
func TestAForeignTrafficPolicyHoldsTheExemptLockAcrossItsDeadline(t *testing.T) {
	var foreign *unstructured.Unstructured
	g, c, _ := exemptJ2(t, "jtwoforeign", 5*time.Second, func(g *authEvalRig) {
		foreign = plantForeign(t, g.a)
		g.prober.foreignAnswers(g.name, true)
	})
	if !time.Now().Before(txOf(t, g.a).Deadline.Time) {
		t.Fatal("setup: the Lock's deadline passed before the interval before it could be measured")
	}
	g.noHits(t, c, "while the foreign policy stands", 3)
	held := g.waitingForAuth(t, "beside the foreign policy")
	beforeDeadline(t, g, "the interval before the deadline")
	if !strings.Contains(held.Message, "intruder") {
		t.Errorf("the hold does not name the foreign policy: %s", held.Message)
	}
	time.Sleep(time.Until(txOf(t, g.a).Deadline.Time) + 100*time.Millisecond)
	g.noHits(t, c, "past the deadline, while the foreign policy stands", 3)
	g.waitingForAuth(t, "past the deadline")

	if err := k8s.Delete(context.Background(), foreign); err != nil {
		t.Fatal(err)
	}
	g.prober.foreignAnswers(g.name, false)
	g.evalPass(t)
	if g.hits(c) != 1 {
		t.Fatalf("once the foreign policy was removed the run did not start: %d cases sent", g.hits(c))
	}
}

// runListFails fails the run namespace's AgentgatewayPolicy list with err,
// and lets every other read through, the Gateway namespace's list included.
type runListFails struct {
	client.Reader
	ns  string
	err error
}

func (p runListFails) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	lo := &client.ListOptions{}
	lo.ApplyOptions(opts)
	if u, ok := list.(*unstructured.UnstructuredList); ok && lo.Namespace == p.ns &&
		strings.HasPrefix(u.GetKind(), "AgentgatewayPolicy") {
		return p.err
	}
	return p.Reader.List(ctx, list, opts...)
}

// T48's variant: once the Lock is recorded at ProbingAfter, the eval
// controller's reader alone fails the run namespace's policy list with a
// Forbidden, and then a 5xx, never a NotFound. Design 03's reader is left
// alone: gatewayStep returns on a failed foreign list before authStep, which
// would stall the Lock short of ProbingAfter, where clause 2 already keeps the
// eval off. Each pass records the hold and sends nothing.
//
// Mutation: read a failed foreign list as no hold.
func TestAFailedForeignListHoldsTheExemptLock(t *testing.T) {
	g, c, _ := exemptJ2(t, "jtwoflist", 0, nil)
	gr := schema.GroupResource{Group: "agentgateway.dev", Resource: "agentgatewaypolicies"}
	for _, fail := range []error{apierrors.NewForbidden(gr, "", errors.New("no grant")),
		apierrors.NewInternalError(errors.New("etcd is unavailable"))} {
		e, err := controller.NewEvalReconciler(k8s, runListFails{Reader: k8s, ns: runNS(g.ns), err: fail},
			func() bool { return true }, g.r.Gateway)
		if err != nil {
			t.Fatal(err)
		}
		e.A2A.HTTP, e.CardFetchTimeout = g.stub, time.Second
		g.e = e
		for i := 0; i < 2; i++ {
			if err := g.evalPassErr(t); err == nil {
				t.Fatalf("%v: a pass whose foreign list failed returned no error", fail)
			}
			g.agentPass(t)
		}
		if h := g.hits(c); h != 0 {
			t.Fatalf("%v: %d cases were sent while the foreign list failed", fail, h)
		}
		held := g.waitingForAuth(t, "with the foreign list failing")
		if !strings.Contains(held.Message, "foreign traffic policy") {
			t.Errorf("%v: the hold does not name the failed list: %s", fail, held.Message)
		}
	}
}

// T57: a J2 Lock in the exempt state, with no hold standing, whose
// candidate's card is valid but serves no usable HTTP+JSON 1.0 interface —
// only a JSON-RPC one. GatesPassed reads False/EvalBindingUnsupported, and
// over 10 s of eval and Agent passes the Agent's resourceVersion does not
// change.
//
// Mutations: clear blocked whenever clause 5 finds no hold (the
// resourceVersion bound); or show EvalRunning when blocked is not the hold
// (the reason).
func TestAnExemptLocksUnusableBindingWritesNoLoop(t *testing.T) {
	g, c, cd := exemptJ2(t, "jtwobind", 0, nil)
	jsonrpc := strings.Replace(stubCard(g.name), "HTTP+JSON", "JSONRPC", 1)
	g.stub.setCard(g.name, jsonrpc)
	writeAgentStatus(t, g.a, func(st *assaydv1alpha1.AgentStatus) {
		for i := range st.Cards {
			if st.Cards[i].RevisionDigest == cd {
				st.Cards[i].Digest = sha256Hex(jsonrpc)
			}
		}
	})
	g.evalPass(t)
	g.agentPass(t)
	condIs(t, g.a, assaydv1alpha1.CondGatesPassed, metav1.ConditionFalse, controller.ReasonEvalBindingUnsupported)
	rv := liveAgent(t, g.a).ResourceVersion
	end := time.Now().Add(10 * time.Second)
	for time.Now().Before(end) {
		g.evalPass(t)
		g.agentPass(t)
		time.Sleep(500 * time.Millisecond)
	}
	if got := liveAgent(t, g.a).ResourceVersion; got != rv {
		t.Errorf("the Agent's resourceVersion moved from %s to %s over 10 s: a write loop", rv, got)
	}
	if g.hits(c) != 0 {
		t.Errorf("a card with no usable interface was sent %d cases", g.hits(c))
	}
	condIs(t, g.a, assaydv1alpha1.CondGatesPassed, metav1.ConditionFalse, controller.ReasonEvalBindingUnsupported)
}

// T58: a stale hold. A J2 Lock in the Gateway-level row's state has blocked
// recording EvalWaitingForAuth. A spec edit then abandons the Lock and mints
// a new candidate, and once the new candidate's run starts, blocked is empty
// and blockedFor names the new pair.
//
// Mutation: clear EvalWaitingForAuth only when clause 5 is next evaluated,
// which leaves it standing here.
func TestAStaleHoldClearsWhenTheLockIsAbandoned(t *testing.T) {
	g, _, _ := exemptJ2(t, "jtwostale", 0, func(g *authEvalRig) { gatewayAuthPolicy(t, g.a, g.prober) })
	g.evalPass(t)
	if e := liveAgent(t, g.a).Status.Eval; e == nil || e.Blocked == nil ||
		e.Blocked.Reason != controller.ReasonEvalWaitingForAuth {
		t.Fatalf("setup: blocked does not record the hold: %+v", e)
	}
	// Back to none, on a new image: the Lock is abandoned, and the edit mints
	// a new candidate D.
	d, dd := g.candidate(t, func(x *assaydv1alpha1.Agent) {
		noneSpec(x)
		x.Spec.Runtime.Image = imageB
	})
	if tx := txOf(t, g.a); tx != nil {
		t.Fatalf("setup: the edit did not abandon the Lock: %+v", tx)
	}
	g.evalPass(t)
	e := liveAgent(t, g.a).Status.Eval
	if e == nil || e.RevisionDigest != dd || e.Verdict != assaydv1alpha1.EvalVerdictRunning {
		t.Fatalf("the new candidate %s's run did not start: %+v", d, e)
	}
	if e.Blocked != nil {
		t.Errorf("a hold computed for the abandoned Lock still stands: %+v", e.Blocked)
	}
	if bf := e.BlockedFor; bf == nil || bf.RevisionDigest != dd || bf.LockDeadline != nil {
		t.Errorf("blockedFor does not name the new pair with no Lock: %+v", bf)
	}
}

// imageN is a distinct image digest for the nth revision a row mints.
func imageN(n int) string {
	return "ghcr.io/acme/agent@sha256:" + strings.Repeat(string("098765432"[n]), 64)
}

// missingPolicyAtProbingAfter deletes `<agent>-auth` and takes the Lock of
// the missing policy it enters to ProbingAfter, accepting the policy it
// re-creates as the Gateway would. With the oracle's switch released, the
// probe there answers 401, which only a recorded card for the route's
// revision can attribute.
func (g *authEvalRig) missingPolicyAtProbingAfter(t *testing.T) {
	t.Helper()
	deletePolicy(t, g.a)
	g.agentPass(t)
	acceptPolicy(t, g.ns, g.name)
	g.agentPass(t)
	tx := txOf(t, g.a)
	if tx == nil || tx.Kind != "Lock" || tx.Stage != "ProbingAfter" || authOf(t, g.a).Mode != "apikey" {
		t.Fatalf("setup: the missing-policy Lock is not at ProbingAfter: %+v", authOf(t, g.a))
	}
}

// wedge builds T43's state, in its order. (1) With R served under apikey and
// still desired, R's card is cleared by a failing re-read. (2) `<agent>-auth`
// is deleted, and the Lock reaches ProbingAfter with a 401 it cannot
// attribute. (3) Only then is the candidate C minted, available and carded.
// The route names R, status.activeRevision is R, and neither is carded: the
// residue design 03 A77 leaves, which the re-point does not end.
func wedge(t *testing.T, name string) (*authEvalRig, string) {
	t.Helper()
	g := newAuthEvalRig(t, name, false, echo("a", "one"))
	g.gate(t)
	g.clearCard(t)
	g.missingPolicyAtProbingAfter(t)
	c, _ := g.candidate(t, func(x *assaydv1alpha1.Agent) { x.Spec.Runtime.Image = imageN(1) })
	g.noHits(t, c, "in the wedge", 3)
	if got := backendOf(t, g.a); got != g.r1 {
		t.Fatalf("setup: the route names %s, want R %s", got, g.r1)
	}
	return g, c
}

// T43: the missing-policy wedge, its message and the working exit. The stub
// sees no case. The route names R, `<agent>-auth` is present, and the Agent
// carries PolicyApplyIncomplete=True/AuthPolicyMissing, Ready=False and phase
// Degraded. EvalWaitingForAuth names R, both exits and which of them this
// state admits — the route names status.activeRevision and C is pending, so
// the revert — and that status.activeRevision's own card recording would also
// end it, with no fetch of that card running while C is desired. Deleting
// `<agent>-serving` then displaces the Lock with a route re-create Create,
// which publishes on status.activeRevision, and once it is Served the run
// starts.
//
// Mutations: exempt the missing-policy Lock by dropping the J2-or-K2 clause
// (the zero-hit assertion); the generic message; or name the administrator
// as the only way out, which design 03 A78 measured false (the card-recording
// assertion).
func TestTheMissingPolicyWedgeNamesItsExitsAndTheWorkingExitEndsIt(t *testing.T) {
	g, c := wedge(t, "wedgeexits")
	if policyExists(t, runNS(g.ns), policyNameOf(g.a)) == nil {
		t.Fatal("the missing-policy Lock did not re-create <agent>-auth")
	}
	condIs(t, g.a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "AuthPolicyMissing")
	condIs(t, g.a, assaydv1alpha1.CondReady, metav1.ConditionFalse, "AuthPolicyMissing")
	if p := liveAgent(t, g.a).Status.Phase; p != assaydv1alpha1.PhaseDegraded {
		t.Errorf("phase is %s, want Degraded", p)
	}
	msg := g.waitingForAuth(t, "in the wedge").Message
	for _, want := range []string{
		"names revision " + g.r1,
		"TERMINAL",
		"removes " + runNS(g.ns) + "/" + g.name + "-serving",
		"The exit that works then is the second",
		"status.activeRevision's own card recording would end it too, though no fetch of that card runs",
		"administrator's of the run namespace",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("EvalWaitingForAuth does not carry %q: %s", want, msg)
		}
	}
	if e := liveAgent(t, g.a).Status.Eval; e != nil && e.Blocked != nil {
		t.Errorf("a re-creation fails clause 1, so clause 5 is never evaluated and blocked stays "+
			"empty: %+v", e.Blocked)
	}

	// The working exit: an administrator deletes the route.
	deleteRoute(t, g.a)
	g.agentPass(t)
	if tx := txOf(t, g.a); tx == nil || tx.Kind != "Create" {
		t.Fatalf("deleting the route did not displace the Lock with a route re-create: %+v", tx)
	}
	g.driveCreateToServed(t)
	if got := backendOf(t, g.a); got != g.r1 {
		t.Errorf("the re-create published on %s, want status.activeRevision %s", got, g.r1)
	}
	g.evalPass(t)
	if g.hits(c) != 1 {
		t.Fatalf("once the re-create was Served the run did not start: %d cases sent", g.hits(c))
	}
}

// driveCreateToServed accepts the route and the policy as the Gateway would
// and runs passes until the recorded Create is Served.
func (g *authEvalRig) driveCreateToServed(t *testing.T) {
	t.Helper()
	g.prober.hold(g.name, false)
	for i := 0; i < 8; i++ {
		if servingRoute(t, g.ns, g.name) != nil {
			acceptRoute(t, g.ns, g.name)
		}
		if policyExists(t, runNS(g.ns), policyNameOf(g.a)) != nil {
			acceptPolicy(t, g.ns, g.name)
		}
		g.agentPass(t)
		if auth := authOf(t, g.a); auth.Mode == "apikey" && auth.Transaction == nil {
			return
		}
	}
	t.Fatalf("the Create did not reach Served: %+v", authOf(t, g.a))
}

// T50: R's name in the wedge message, in the wedge state with C a held
// candidate, so status.activeRevision is still R and no re-point moves the
// route. Once C is desired, R's Service is deleted; it is not re-created,
// because ensureService runs only for the desired revision. EvalWaitingForAuth
// still names R, taken from the route's backendRef.
//
// Mutation: take R's name from the Service.
func TestTheWedgeNamesRAfterItsServiceIsDeleted(t *testing.T) {
	g, _ := wedge(t, "wedgenosvc")
	svc := controller.WorkloadName(g.name, g.r1)
	var s corev1.Service
	if err := k8s.Get(context.Background(), client.ObjectKey{Namespace: runNS(g.ns), Name: svc}, &s); err != nil {
		t.Fatalf("setup: R's Service: %v", err)
	}
	if err := k8s.Delete(context.Background(), &s); err != nil {
		t.Fatal(err)
	}
	g.agentPass(t)
	g.agentPass(t)
	if err := k8s.Get(context.Background(), client.ObjectKey{Namespace: runNS(g.ns), Name: svc}, &s); !apierrors.IsNotFound(err) {
		t.Fatalf("setup: R's Service was re-created (%v); the row needs it gone", err)
	}
	msg := g.waitingForAuth(t, "with R's Service gone").Message
	if !strings.Contains(msg, "names revision "+g.r1) || !strings.Contains(msg, "TERMINAL") {
		t.Errorf("with R's Service gone, EvalWaitingForAuth does not name R from the backendRef: %s", msg)
	}
}

// T51: a Lock past its deadline, shortened here, keeps the eval waiting, and
// EvalWaitingForAuth names when the deadline passed (Q12 (a)). The Lock is
// the missing policy's, whose route's revision R is carded, held short of
// Served by the oracle's switch.
//
// Mutation: claim cases once the deadline has passed.
func TestALockPastItsDeadlineKeepsTheEvalWaiting(t *testing.T) {
	g := newAuthEvalRig(t, "pastdeadline", false, echo("a", "one"))
	g.gate(t)
	g.r.AuthDeadline = time.Millisecond
	g.prober.hold(g.name, true)
	g.missingPolicyAtProbingAfter(t)
	c, _ := g.candidate(t, func(x *assaydv1alpha1.Agent) { x.Spec.Runtime.Image = imageN(1) })
	deadline := txOf(t, g.a).Deadline
	g.noHits(t, c, "past the Lock's deadline", 3)
	msg := g.waitingForAuth(t, "past the deadline").Message
	if !strings.Contains(msg, "deadline passed at "+deadline.UTC().Format(time.RFC3339)) {
		t.Errorf("EvalWaitingForAuth does not name when the deadline passed: %s", msg)
	}
}

// T49: an orphaned claim of the last case while a Lock is recorded, built as
// the handover row is: the test writes the last case's claim with no outcome.
// The Lock is a missing-policy Lock, which is never exempt, on a route whose
// revision R has its card recorded, so that it can reach Served; the oracle's
// switch keeps it short of Served. Over an observation interval no
// Interrupted is recorded and no verdict is written; the oracle then answers,
// the Lock reaches Served, and Interrupted is recorded.
//
// Mutation: record Interrupted without the no-transaction predicate.
func TestAnOrphanedLastClaimWaitsOnARecordedLock(t *testing.T) {
	g := newAuthEvalRig(t, "orphanlock", false, echo("a", "one"), echo("b", "two"))
	g.gate(t)
	c, _ := g.candidate(t, func(x *assaydv1alpha1.Agent) { x.Spec.Runtime.Image = imageN(1) })
	g.evalPass(t) // the run start and case a
	writeAgentStatus(t, g.a, func(st *assaydv1alpha1.AgentStatus) {
		st.Eval.Cases = append(st.Eval.Cases, assaydv1alpha1.EvalCaseRecord{ID: "b",
			Outcome: assaydv1alpha1.EvalCaseSending, Claim: "a-stopped-controller"})
	})
	g.prober.hold(g.name, true)
	g.missingPolicyAtProbingAfter(t)
	for i := 0; i < 4; i++ {
		g.evalPass(t)
		g.agentPass(t)
		e := liveAgent(t, g.a).Status.Eval
		if b := caseOf(e, "b"); b == nil || b.Outcome != assaydv1alpha1.EvalCaseSending || e.Verdict != assaydv1alpha1.EvalVerdictRunning {
			t.Fatalf("an orphaned claim was recorded, or a verdict written, while the Lock was recorded: %+v", e)
		}
	}
	g.prober.hold(g.name, false)
	g.agentPass(t)
	if auth := authOf(t, g.a); auth.Transaction != nil {
		t.Fatalf("setup: the Lock did not reach Served on R's card: %+v", auth)
	}
	g.evalPass(t)
	e := liveAgent(t, g.a).Status.Eval
	if b := caseOf(e, "b"); b == nil || b.Reason != "Interrupted" || e.Verdict != assaydv1alpha1.EvalVerdictFailed {
		t.Fatalf("once the Lock was Served the orphaned claim was not recorded Interrupted: %+v", e)
	}
	if g.stub.sent(g.name, c, "two") != 0 {
		t.Error("the orphaned case was sent")
	}
}

// heldLastCase starts the candidate's run and sends its first case, then
// sends the last in a goroutine, where the stub holds it. It returns once the
// case has arrived, with release and the channel that closes when that pass
// has returned.
func (g *authEvalRig) heldLastCase(t *testing.T, last string) (release func(), done <-chan struct{}) {
	t.Helper()
	g.evalPass(t)
	arrived, release := g.stub.hold(last)
	ch := make(chan struct{})
	go func() {
		defer close(ch)
		_, _ = g.e.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(g.a)})
	}()
	t.Cleanup(release)
	waitFor(t, arrived, "the candidate's last case")
	return release, ch
}

// T44: the re-point ends the wedge with nobody acting, from the gate's side
// (design 03 §8.1 case 18 (a)). R's card is cleared while R is desired; then
// C is minted and runs. The stub holds C's last case while `<agent>-auth` is
// deleted, so the Lock is recorded before the verdict. The stub answers, and
// that case's record writes Passed. C is promoted with its card recorded, and
// with the Lock at ProbingAfter the same pass re-points the route at C; the
// route gate defers the request, so that pass takes no answer. Once the
// Gateway reports at the new generation, the 401 is attributed on C's card
// and the Lock reaches Served. The next candidate D then runs and never reads
// EvalWaitingForAuth.
//
// Mutations: never re-point (the Lock stays, and D reads EvalWaitingForAuth);
// hold the eval after the Lock is gone (D's run); or credit in the pass that
// moved the route (the no-answer assertion).
func TestTheRepointEndsTheWedgeWithNobodyActingFromTheGatesSide(t *testing.T) {
	g := newAuthEvalRig(t, "wedgegate", false, echo("a", "one"), echo("b", "two"))
	g.gate(t)
	g.clearCard(t)
	c, _ := g.candidate(t, func(x *assaydv1alpha1.Agent) { x.Spec.Runtime.Image = imageN(1) })
	release, done := g.heldLastCase(t, "two")
	g.missingPolicyAtProbingAfter(t)
	release()
	<-done
	if e := liveAgent(t, g.a).Status.Eval; e.Verdict != assaydv1alpha1.EvalVerdictPassed {
		t.Fatalf("setup: the case in flight did not write the verdict: %+v", e)
	}
	// The eval controller sees the recorded Lock, and waits on it, before it
	// ends: what must not outlive the Lock is that wait.
	if res := g.evalPass(t); res.RequeueAfter != controller.AuthProbeInterval {
		t.Fatalf("an eval pass beside the recorded Lock did not wait on it: %+v", res)
	}
	probes := g.prober.count(g.name)
	g.agentPass(t)
	live := liveAgent(t, g.a)
	if live.Status.ActiveRevision != c {
		t.Fatalf("a Passed C was not promoted beside the recorded Lock (active %s)", live.Status.ActiveRevision)
	}
	if got := backendOf(t, g.a); got != c {
		t.Fatalf("the promoting pass did not re-point the route at C: it names %s", got)
	}
	if n := g.prober.count(g.name); n != probes || live.Status.Auth.Transaction == nil {
		t.Fatalf("the pass that moved the route took an answer (%d probes → %d): %+v", probes, n,
			live.Status.Auth)
	}
	acceptRoute(t, g.ns, g.name)
	g.agentPass(t)
	if auth := authOf(t, g.a); auth.Mode != "apikey" || auth.Transaction != nil {
		t.Fatalf("the Lock did not reach Served on C's card once the Gateway reported: %+v", auth)
	}
	d, _ := g.candidate(t, func(x *assaydv1alpha1.Agent) { x.Spec.Runtime.Image = imageN(2) })
	for i := 0; i < 6 && liveAgent(t, g.a).Status.ActiveRevision != d; i++ {
		g.evalPass(t)
		g.agentPass(t)
		if gp := condition(liveAgent(t, g.a), assaydv1alpha1.CondGatesPassed); gp != nil &&
			gp.Reason == controller.ReasonEvalWaitingForAuth {
			t.Fatalf("D reads EvalWaitingForAuth after the Lock ended: %s", gp.Message)
		}
	}
	if got := liveAgent(t, g.a).Status.ActiveRevision; got != d {
		t.Fatalf("D was not evaluated and promoted (active %s)", got)
	}
}

// T67: a missing-policy Lock recorded when a Passed candidate is promoted,
// with the card of the route's revision R recorded. The stub holds C's last
// case while `<agent>-auth` is deleted; the Lock is recorded; the stub answers,
// and that case's record writes Passed. The oracle withholds the 401 until the
// test has seen status.activeRevision == C with the Lock still recorded. The
// re-point reads status.activeRevision, so with C carded and the Lock at
// ProbingAfter the promoting pass itself moves the backendRef to C while the
// Lock runs; the next pass credits the 401 on C's card, and the Lock reaches
// Served with the backendRef never back on R. R's card, recorded at an
// earlier fetchedAt, is read by nothing: the served GovernanceSkipped message
// names C's.
//
// Mutations: re-point only after Served (the mid-Lock backendRef assertion);
// cardAttributes returning the first entry with a non-empty digest whatever
// backend the route names (the fetchedAt the message names); or hold
// promotion while a Lock is recorded (the promotion assertion).
func TestAMissingPolicyLockRecordedWhenAPassedCandidatePromotes(t *testing.T) {
	g := newAuthEvalRig(t, "lockpromote", false, echo("a", "one"), echo("b", "two"))
	g.gate(t)
	c, cd := g.candidate(t, func(x *assaydv1alpha1.Agent) { x.Spec.Runtime.Image = imageN(1) })
	earlier := metav1.NewTime(time.Now().Add(-2 * time.Minute).Truncate(time.Second))
	writeAgentStatus(t, g.a, func(st *assaydv1alpha1.AgentStatus) {
		for i := range st.Cards {
			if st.Cards[i].RevisionDigest == g.d1 {
				st.Cards[i].FetchedAt = &earlier
			}
		}
	})
	release, done := g.heldLastCase(t, "two")
	g.prober.hold(g.name, true)
	g.missingPolicyAtProbingAfter(t)
	release()
	<-done
	g.agentPass(t)
	live := liveAgent(t, g.a)
	if live.Status.ActiveRevision != c || live.Status.Auth.Transaction == nil {
		t.Fatalf("want C %s promoted with the Lock still recorded: active %s, auth %+v", c,
			live.Status.ActiveRevision, live.Status.Auth)
	}
	if got := backendOf(t, g.a); got != c {
		t.Fatalf("the promoting pass did not move the backendRef to C while the Lock runs: %s", got)
	}
	var cAt string
	for _, card := range live.Status.Cards {
		if card.RevisionDigest == cd && card.FetchedAt != nil {
			cAt = card.FetchedAt.UTC().Format(time.RFC3339)
		}
	}
	g.prober.hold(g.name, false)
	acceptRoute(t, g.ns, g.name)
	g.agentPass(t)
	if auth := authOf(t, g.a); auth.Mode != "apikey" || auth.Transaction != nil {
		t.Fatalf("the Lock did not reach Served: %+v", auth)
	}
	if got := backendOf(t, g.a); got != c {
		t.Errorf("the backendRef went back to R: %s", got)
	}
	gs := condition(liveAgent(t, g.a), assaydv1alpha1.CondGovernanceSkipped)
	if gs == nil || !strings.Contains(gs.Message, "recorded at "+cAt) ||
		strings.Contains(gs.Message, earlier.UTC().Format(time.RFC3339)) {
		t.Errorf("the credit is not on C's card (fetched %s; R's %s): %+v", cAt,
			earlier.UTC().Format(time.RFC3339), gs)
	}
}

// T21: a served API-key Agent whose re-created route is in Create when its
// gated candidate passes. The Create reaches Served and publishes onto the
// new active revision.
//
// Mutation: publish onto the revision recorded when the Create started.
func TestARouteRecreateInFlightPublishesOntoThePromotedRevision(t *testing.T) {
	g := newAuthEvalRig(t, "recreatepass", false, echo("a", "one"), echo("b", "two"))
	g.gate(t)
	c, _ := g.candidate(t, func(x *assaydv1alpha1.Agent) { x.Spec.Runtime.Image = imageN(1) })
	release, done := g.heldLastCase(t, "two")
	deleteRoute(t, g.a)
	g.prober.hold(g.name, true)
	for i := 0; i < 3; i++ {
		g.agentPass(t)
		if servingRoute(t, g.ns, g.name) != nil {
			acceptRoute(t, g.ns, g.name)
		}
		acceptPolicy(t, g.ns, g.name)
	}
	g.agentPass(t)
	if tx := txOf(t, g.a); tx == nil || tx.Kind != "Create" || tx.Stage != "ProbingAfter" {
		t.Fatalf("setup: the route re-create is not held at ProbingAfter: %+v", tx)
	}
	release()
	<-done
	if e := liveAgent(t, g.a).Status.Eval; e.Verdict != assaydv1alpha1.EvalVerdictPassed {
		t.Fatalf("setup: the case in flight did not write the verdict: %+v", e)
	}
	g.prober.hold(g.name, false)
	g.agentPass(t)
	if got := liveAgent(t, g.a).Status.ActiveRevision; got != c {
		t.Fatalf("the Passed candidate was not promoted beside the Create (active %s)", got)
	}
	if got := backendOf(t, g.a); got != c {
		t.Fatalf("the Create published onto %s, not the new active revision %s", got, c)
	}
	g.driveCreateToServed(t)
	if got := backendOf(t, g.a); got != c {
		t.Errorf("the Served route names %s, want %s", got, c)
	}
}

// T45: the wedge with the route and the active revision apart, which design
// 03 A77 narrows and does not remove, built in this order because the order
// is the whole row. (1) R served under apikey, its card cleared while R is
// desired. (2) C minted, available and carded; its run starts and the stub
// holds C's last case. (3) `<agent>-auth` deleted, and the missing-policy Lock
// reaches ProbingAfter. (4) A foreign traffic policy stored on
// `<agent>-serving` under a name of its own, so the stage stays at
// ProbingAfter and every pass breaks at the foreign check, one rung above the
// re-point. (5) The stub answers, and that case's record writes Passed. (6) C
// is promoted, which a hold does not stop once the verdict is written. The
// state is then apart with C carded: status.activeRevision is C, the route
// still names R, and nothing moves. The message names no exits, because
// status.cards[] records a digest for status.activeRevision: the next
// candidate D reads the generic EvalWaitingForAuth, naming neither R nor a
// revert, and blocked is empty, because a re-creation fails clause 1. Three
// more revisions are then minted, and R is collected with its Service under a
// route that still names it.
//
// Mutations: name the exits whenever the route's revision is uncarded,
// dropping the status.activeRevision half of the condition (the no-exits
// assertion); or move the re-point above the foreign check (the "route still
// names R" assertion). This row is the only pin on that ordering.
func TestTheWedgeApartWithACardedActiveRevisionNamesNoExits(t *testing.T) {
	g := newAuthEvalRig(t, "wedgeapart", false, echo("a", "one"), echo("b", "two"))
	g.gate(t)
	g.clearCard(t)
	c, _ := g.candidate(t, func(x *assaydv1alpha1.Agent) { x.Spec.Runtime.Image = imageN(1) })
	release, done := g.heldLastCase(t, "two")
	g.missingPolicyAtProbingAfter(t)
	plantForeign(t, g.a)
	g.agentPass(t)
	if tx := txOf(t, g.a); tx == nil || tx.Stage != "ProbingAfter" {
		t.Fatalf("setup: the foreign policy moved the Lock off ProbingAfter: %+v", tx)
	}
	release()
	<-done
	for i := 0; i < 4; i++ {
		g.agentPass(t)
	}
	live := liveAgent(t, g.a)
	if live.Status.ActiveRevision != c || live.Status.Auth.Transaction == nil {
		t.Fatalf("setup: want C %s promoted beside the recorded Lock: active %s, auth %+v", c,
			live.Status.ActiveRevision, live.Status.Auth)
	}
	if got := backendOf(t, g.a); got != g.r1 {
		t.Fatalf("the route left R for %s while a foreign traffic policy breaks every pass above the "+
			"re-point (design 03 §3.3.3)", got)
	}

	d, _ := g.candidate(t, func(x *assaydv1alpha1.Agent) { x.Spec.Runtime.Image = imageN(2) })
	g.noHits(t, d, "beside the apart Lock", 2)
	msg := g.waitingForAuth(t, "D, apart with C carded").Message
	if !strings.Contains(msg, "an -auth Lock is recorded on this Agent, in stage ProbingAfter") {
		t.Errorf("D's message does not name the Lock and its stage: %s", msg)
	}
	for _, not := range []string{"TERMINAL", g.r1, "revert"} {
		if strings.Contains(msg, not) {
			t.Errorf("D's message names %q, and with status.activeRevision carded it names no exits: %s",
				not, msg)
		}
	}
	if e := liveAgent(t, g.a).Status.Eval; e.Blocked != nil {
		t.Errorf("clause 5 was evaluated for a re-creation: blocked %+v", e.Blocked)
	}

	// Three more revisions: D superseded by E, then F. R is then collected
	// with its Service, under a route that still names it.
	for n := 3; n <= 4; n++ {
		g.candidate(t, func(x *assaydv1alpha1.Agent) { x.Spec.Runtime.Image = imageN(n) })
	}
	g.agentPass(t)
	var s corev1.Service
	err := k8s.Get(context.Background(), client.ObjectKey{Namespace: runNS(g.ns),
		Name: controller.WorkloadName(g.name, g.r1)}, &s)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("R's Service was not collected after three more revisions (%v)", err)
	}
	if got := backendOf(t, g.a); got != g.r1 {
		t.Errorf("the route no longer names the collected R: %s", got)
	}
}

// T45's variant: the apart state with an uncarded C, where the exits are
// named. No foreign policy; the Lock is left at Converging, where its fresh
// `<agent>-auth` reports no ancestors yet; C is promoted in that window; and
// a failing card stub clears C's digest by a drift re-read before the policy
// is accepted. The Lock then reaches ProbingAfter with C uncarded, the route
// is left as found, and D's EvalWaitingForAuth names R and offers the revert
// to R, not to C.
//
// Mutations: re-point unconditionally (the route still names R); compute the
// message from status.activeRevision in place of the route's single
// backendRef (it names R); or name status.activeRevision in the revert.
func TestTheWedgeApartWithAnUncardedActiveRevisionNamesR(t *testing.T) {
	g := newAuthEvalRig(t, "wedgeapartc", false, echo("a", "one"), echo("b", "two"))
	g.gate(t)
	g.clearCard(t)
	c, _ := g.candidate(t, func(x *assaydv1alpha1.Agent) { x.Spec.Runtime.Image = imageN(1) })
	release, done := g.heldLastCase(t, "two")
	deletePolicy(t, g.a)
	g.agentPass(t)
	if tx := txOf(t, g.a); tx == nil || tx.Stage != "Converging" {
		t.Fatalf("setup: the missing-policy Lock is not at Converging: %+v", tx)
	}
	release()
	<-done
	g.agentPass(t)
	if live := liveAgent(t, g.a); live.Status.ActiveRevision != c || txOf(t, g.a).Stage != "Converging" {
		t.Fatalf("setup: want C promoted with the Lock at Converging: active %s, tx %+v",
			live.Status.ActiveRevision, txOf(t, g.a))
	}
	g.clearCard(t)
	acceptPolicy(t, g.ns, g.name)
	g.agentPass(t)
	g.agentPass(t)
	if tx := txOf(t, g.a); tx == nil || tx.Stage != "ProbingAfter" {
		t.Fatalf("setup: the Lock did not reach ProbingAfter: %+v", tx)
	}
	if got := backendOf(t, g.a); got != g.r1 {
		t.Fatalf("the route was re-pointed onto uncarded C: it names %s, want R %s", got, g.r1)
	}
	d, _ := g.candidate(t, func(x *assaydv1alpha1.Agent) { x.Spec.Runtime.Image = imageN(2) })
	g.noHits(t, d, "beside the apart Lock", 2)
	msg := g.waitingForAuth(t, "D, apart with C uncarded").Message
	for _, want := range []string{"names revision " + g.r1, "TERMINAL",
		"reverting the spec to the revision the route names"} {
		if !strings.Contains(msg, want) {
			t.Errorf("D's message does not carry %q: %s", want, msg)
		}
	}
	if strings.Contains(msg, "names revision "+c) || strings.Contains(msg, "reverting the spec to it") {
		t.Errorf("D's message names C where the route names R: %s", msg)
	}
}

// T41: a refused Adopt. A gated Agent whose status.auth holds {kind: Adopt,
// stage: Refused} evaluates and promotes its candidate, and the route's one
// backendRef then names the promoted revision's Service.
//
// Mutations: key the predicate on status.auth.transaction != nil (no case is
// sent); or skip ensureServingRoute in refuseAdopt (the backendRef).
func TestARefusedAdoptEvaluatesAndPromotes(t *testing.T) {
	a, r, _ := legacyAgent(t, "adopted", nil)
	ns := a.Namespace
	putSuite(t, ns, "smoke", echo("a", "one"))
	r.EvalSuiteInstalled = func() bool { return true }
	stub := newA2AStub()
	r.CardClient, r.CardFetchTimeout = stub, time.Second
	e := newEvalController(t, stub)
	e.Gateway = r.Gateway
	g := &authEvalRig{ns: ns, name: a.Name, a: a, r: r, e: e, stub: stub, r1: a.Status.ActiveRevision}
	c, _ := g.candidate(t, func(x *assaydv1alpha1.Agent) {
		x.Spec.Runtime.Image = imageN(1)
		x.Spec.Gates = []assaydv1alpha1.GateRef{{EvalSuiteRef: "smoke"}}
	})
	if tx := txOf(t, a); tx == nil || tx.Kind != "Adopt" || tx.Stage != "Refused" {
		t.Fatalf("setup: the refused Adopt is not recorded: %+v", tx)
	}
	g.runToVerdict(t, c)
	if g.hits(c) != 1 {
		t.Errorf("C was sent %d cases beside the refused Adopt, want 1", g.hits(c))
	}
	g.agentPass(t)
	if got := backendOf(t, a); got != c {
		t.Errorf("the route's backendRef names %s after the promotion, want C %s", got, c)
	}
	if tx := txOf(t, a); tx == nil || tx.Kind != "Adopt" {
		t.Errorf("the refused Adopt did not stand through the promotion: %+v", tx)
	}
}

// addGatewayAgentController registers an Agent reconciler built as the binary
// builds it, gateway-enabled on the suite's Gateway, with the oracle as its
// probe and the A2A stub as every card.
func addGatewayAgentController(t *testing.T, mgr manager.Manager, gw controller.GatewayConfig, prober *stubProber,
	stub *a2aStub, deadline time.Duration) {
	t.Helper()
	r, err := controller.NewAgentReconciler(mgr.GetClient(), mgr.GetAPIReader(), mgr.GetScheme(), operatorNamespace,
		func() bool { return true }, labelAuthorityPresent, controller.InjectedEnvConfig{}, gw)
	if err != nil {
		t.Fatal(err)
	}
	r.AuthProbe, r.CardClient, r.CardFetchTimeout, r.AuthDeadline = prober, stub, time.Second, deadline
	if err := r.SetupWithManager(mgr); err != nil {
		t.Fatalf("register the Agent reconciler: %v", err)
	}
}

// T66: J2 on a gated Agent, against the real manager with both controllers,
// with a stub that answers each case after 1 s. The oracle withholds the 401,
// which holds the Lock at ProbingAfter, until the candidate is available and
// its card recorded; the serving revision R's card is recorded, so the Lock
// is not exempt. The stub sees no case across that interval. The oracle then
// answers, the Lock reaches Served inside its deadline, and the run starts,
// passes and promotes.
//
// Mutation: drop the Create-or-Lock predicate (the zero-hit interval).
func TestAJ2LockOnAGatedAgentHoldsTheEvalUntilServed(t *testing.T) {
	g := newAuthEvalRig(t, "jtwogated", true, echo("a", "one"), echo("b", "two"))
	g.gate(t)
	g.stub.delay = time.Second
	g.r.AuthDeadline = time.Minute
	g.prober.hold(g.name, true)
	mustEdit(t, g.a, func(x *assaydv1alpha1.Agent) {
		x.Spec.Expose = &assaydv1alpha1.ExposeSpec{A2A: &assaydv1alpha1.ExposeProtocol{Auth: "apikey"}}
	})
	spec := liveAgent(t, g.a).Spec
	c, cd := revision.MustHash(spec), revision.MustDigest(spec)
	lockPass(t, g.r, g.a)
	markAvailable(t, g.ns, controller.WorkloadName(g.name, c), 1)
	acceptRoute(t, g.ns, g.name)
	acceptPolicy(t, g.ns, g.name)
	lockPass(t, g.r, g.a)
	lockPass(t, g.r, g.a)
	live := liveAgent(t, g.a)
	tx := live.Status.Auth.Transaction
	if tx == nil || tx.Stage != "ProbingAfter" || !cardRecorded(&live.Status, cd) || !cardRecorded(&live.Status, g.d1) {
		t.Fatalf("setup: want the J2 Lock held at ProbingAfter beside a carded R and a carded, available "+
			"candidate: %+v", live.Status)
	}
	deadline := tx.Deadline.Time

	mgr := newTestManager(t)
	addGatewayAgentController(t, mgr, g.r.Gateway, g.prober, g.stub, time.Minute)
	addEvalController(t, mgr, g.stub, func() bool { return true },
		func(e *controller.EvalReconciler) { e.Gateway = g.r.Gateway })
	probes := g.prober.count(g.name)
	startStoppable(t, mgr)
	// The interval: the manager's first passes of both controllers, which
	// start on the Agent's initial list event, the Agent reconciler's
	// probing the held Lock, and one more pass of each, driven by an event.
	eventually(t, "the manager's first held Lock pass", func() bool { return g.prober.count(g.name) > probes })
	probes = g.prober.count(g.name)
	touch(t, g.a)
	eventually(t, "a second held Lock pass", func() bool { return g.prober.count(g.name) > probes })
	time.Sleep(time.Second)
	if h := g.hits(c); h != 0 {
		t.Fatalf("%d cases were sent while the J2 Lock was recorded", h)
	}
	if auth := authOf(t, g.a); auth.Transaction == nil {
		t.Fatalf("setup: the Lock ended while the oracle withheld the 401: %+v", auth)
	}
	g.prober.hold(g.name, false)
	touch(t, g.a) // the next Lock pass now, not at the next AuthProbeInterval
	eventually(t, "the J2 Lock to reach Served", func() bool {
		auth := authOf(t, g.a)
		return auth.Mode == "apikey" && auth.Transaction == nil
	})
	if now := time.Now(); !now.Before(deadline) {
		t.Errorf("the Lock reached Served at %s, after its deadline %s", now, deadline)
	}
	eventually(t, "the candidate to be evaluated and promoted", func() bool {
		return liveAgent(t, g.a).Status.ActiveRevision == c
	})
	if h := g.hits(c); h != 2 {
		t.Errorf("C was sent %d cases, want its two", h)
	}
}

// T30: starvation. Against the real manager with both controllers, a J2 Lock
// on one Agent reaches Served inside its deadline while two other Agents'
// stubs each sleep for caseTimeoutSeconds on every case of a twenty-case
// suite. The eval controller's two workers are the only ones the slow
// tenants occupy; the Agent reconciler's one worker, which runs the Lock's
// passes, is not among them (design 16 Q1).
//
// Mutation: run the cases in the Agent reconciler, which this measures as one
// lock serialising the eval controller's passes with the Agent reconciler's.
func TestAJ2LockFinishesBesideTwoSleepingTenants(t *testing.T) {
	ns := newNamespace(t)
	stub := newA2AStub()
	stub.delay = 3 * time.Second
	var cases []assaydv1alpha1.EvalCase
	for i := 0; i < 20; i++ {
		cases = append(cases, echo(string(rune('a'+i)), "slow-"+string(rune('a'+i))))
	}
	slow := suite(ns, "slow", cases...)
	slow.Spec.CaseTimeoutSeconds = 3
	installGeneratedEvalSuiteCRD(t)
	if err := k8s.Create(context.Background(), slow); err != nil {
		t.Fatal(err)
	}
	var tenants []*evalRig
	for _, name := range []string{"sleepera", "sleeperb"} {
		a, r := servedUngated(t, ns, name)
		r.CardClient = stub
		rig := &evalRig{ns: ns, name: name, a: a, r: r, stub: stub}
		rig.rev, rig.dgst = gatedCandidate(t, r, a, imageB, "slow")
		tenants = append(tenants, rig)
	}

	j := mustCreateAgent(t, ns, "jtwofast", noneSpec)
	jr, prober := createReconciler()
	jr.CardClient, jr.CardFetchTimeout = stub, time.Second
	promote(t, jr, j)
	acceptRoute(t, ns, j.Name)
	reconcileOnce(t, jr, j)
	if auth := authOf(t, j); auth == nil || auth.Mode != "none" || !cardRecorded(&liveAgent(t, j).Status,
		liveAgent(t, j).Status.ActiveRevisionDigest) {
		t.Fatalf("setup: the J2 Agent is not served under none with its card recorded: %+v", liveAgent(t, j).Status)
	}

	// Unserialised the Lock was measured reaching Served about 1 s after the
	// edit; with the two controllers serialised, 7 to 10 s. The Lock's first
	// pass, which records its deadline, is one of the Agent reconciler's too, so
	// the bound is also held from the edit.
	const lockDeadline = 4 * time.Second
	mgr := newTestManager(t)
	addGatewayAgentController(t, mgr, jr.Gateway, prober, stub, lockDeadline)
	addEvalController(t, mgr, stub, func() bool { return true },
		func(e *controller.EvalReconciler) { e.Gateway = jr.Gateway })
	startStoppable(t, mgr)
	eventually(t, "both tenants to have a case in flight", func() bool {
		return stub.sentTo("sleepera", tenants[0].rev) > 0 && stub.sentTo("sleeperb", tenants[1].rev) > 0
	})

	edited := time.Now()
	toMode(t, j, "apikey")
	eventually(t, "the J2 Lock to write its policy", func() bool {
		tx := txOf(t, j)
		return tx != nil && tx.Stage == "Converging"
	})
	deadline := txOf(t, j).Deadline.Time
	acceptRoute(t, ns, j.Name)
	acceptPolicy(t, ns, j.Name)
	served := time.Time{}
	for time.Now().Before(deadline.Add(5 * time.Second)) {
		if auth := authOf(t, j); auth.Mode == "apikey" && auth.Transaction == nil {
			served = time.Now()
			break
		}
		if tx := txOf(t, j); tx != nil && tx.Stage == "Converging" {
			// A route re-asserted since the acceptance moves its generation.
			acceptRoute(t, ns, j.Name)
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Logf("the J2 Lock reached Served %v after the edit", served.Sub(edited))
	if served.IsZero() || !served.Before(deadline) || served.Sub(edited) >= lockDeadline {
		t.Fatalf("the J2 Lock did not reach Served inside its deadline %s beside two sleeping tenants "+
			"(served at %v): %+v", deadline, served, authOf(t, j))
	}
	for _, rig := range tenants {
		if n := stub.sentTo(rig.name, rig.rev); n >= len(cases) {
			t.Errorf("tenant %s's run finished (%d cases) before the Lock did: the row measured nothing", rig.name, n)
		}
	}
}

// touch annotates the Agent, which enqueues it on both controllers' watches
// at once, so a test need not wait out a requeue interval.
func touch(t *testing.T, a *assaydv1alpha1.Agent) {
	t.Helper()
	live := liveAgent(t, a)
	if live.Annotations == nil {
		live.Annotations = map[string]string{}
	}
	live.Annotations["test.assayd.dev/touch"] = time.Now().Format(time.RFC3339Nano)
	if err := k8s.Update(context.Background(), live); err != nil {
		t.Fatalf("touch the Agent: %v", err)
	}
}

// beforeDeadline fails a row whose interval before the Lock's deadline ended
// after it: on a slow runner that interval would measure nothing.
func beforeDeadline(t *testing.T, g *authEvalRig, what string) {
	t.Helper()
	if tx := txOf(t, g.a); tx == nil || !time.Now().Before(tx.Deadline.Time) {
		t.Fatalf("%s ended after the Lock's deadline, so it measured nothing before it", what)
	}
}

// A clause-5 hold is not applied to a candidate that is not available. The
// eval controller reads clause 5 only on a pass on which the candidate is
// available, so a hold recorded before it went away is not re-read while it is
// away: here the hold names a foreign traffic policy, the policy is removed and
// the candidate made unavailable, and GatesPassed reads EvalPaused, not the
// hold naming a policy that no longer exists (rule 8). Once the candidate is
// available again, the next clause-5 pass clears the stale hold and the run
// goes on.
//
// Mutations: blocked before availability in the exempt arm (EvalPaused); or a
// blocked write skipped while blockedFor is unchanged (the hold cleared on the
// candidate's return).
func TestAHoldIsNotAppliedToAnUnavailableCandidate(t *testing.T) {
	g, c, _ := exemptJ2(t, "jtwopaused", 0, nil)
	g.evalPass(t) // the run start, and case a
	foreign := plantForeign(t, g.a)
	g.evalPass(t)
	if e := liveAgent(t, g.a).Status.Eval; e.Blocked == nil || !strings.Contains(e.Blocked.Message, "intruder") {
		t.Fatalf("setup: the hold is not recorded: %+v", e.Blocked)
	}
	if err := k8s.Delete(context.Background(), foreign); err != nil {
		t.Fatal(err)
	}
	markAvailable(t, g.ns, controller.WorkloadName(g.name, c), 0)
	for i := 0; i < 2; i++ {
		g.evalPass(t)
		g.agentPass(t)
	}
	condIs(t, g.a, assaydv1alpha1.CondGatesPassed, metav1.ConditionFalse, controller.ReasonEvalPaused)
	if gp := condition(liveAgent(t, g.a), assaydv1alpha1.CondGatesPassed); gp != nil &&
		strings.Contains(gp.Message, "intruder") {
		t.Errorf("GatesPassed names a policy that no longer exists: %s", gp.Message)
	}

	markAvailable(t, g.ns, controller.WorkloadName(g.name, c), 1)
	g.agentPass(t)
	g.evalPass(t)
	if e := liveAgent(t, g.a).Status.Eval; e.Blocked != nil {
		t.Errorf("the next clause-5 pass did not clear the stale hold: %+v", e.Blocked)
	}
	if g.hits(c) != 2 {
		t.Errorf("the run did not go on once the candidate came back: %d cases sent, want 2", g.hits(c))
	}
}

// The abandonWaiting row of §1.1's table: a J2 Lock being abandoned while it
// waits on a finalizer, whose record meets the five clauses, is claimed
// beside. The abandoning edit (back to none, on a new image) mints the
// candidate D, and a finalizer on `<agent>-auth` holds the delete, so the Lock
// stays recorded at ProbingAfter with PolicyApplyIncomplete=AuthAbandonWaiting.
//
// Mutation: exclude a Lock waiting on its abandonment from the exemption.
func TestALockAbandonedBehindAFinalizerIsClaimedBeside(t *testing.T) {
	g, _, _ := exemptJ2(t, "jtwoabandon", 0, nil)
	p := policyExists(t, runNS(g.ns), policyNameOf(g.a))
	p.SetFinalizers([]string{"test.assayd.dev/hold"})
	if err := k8s.Update(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if q := policyExists(t, runNS(g.ns), policyNameOf(g.a)); q != nil {
			q.SetFinalizers(nil)
			_ = k8s.Update(context.Background(), q)
		}
	})
	d, _ := g.candidate(t, func(x *assaydv1alpha1.Agent) {
		noneSpec(x)
		x.Spec.Runtime.Image = imageN(5)
	})
	tx := txOf(t, g.a)
	if tx == nil || tx.Kind != "Lock" || tx.Stage != "ProbingAfter" {
		t.Fatalf("setup: the Lock is not recorded while its abandonment waits: %+v", tx)
	}
	condIs(t, g.a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, controller.ReasonAuthAbandonWaiting)
	g.evalPass(t)
	if g.hits(d) != 1 {
		t.Fatalf("a Lock waiting on its abandonment, whose record meets the five clauses, held the eval: "+
			"%d cases sent to D", g.hits(d))
	}
}

// A clause-5 hold on a final-verdict pass in the exempt state. §1.1's table
// rows come after blocked in the exempt state, EvalFailed among them, and the
// eval controller evaluates clause 5 on a held candidate's passes too, so a
// hold that appears after a Failed verdict reads EvalWaitingForAuth, naming
// the policy, ahead of EvalFailed; once it clears, EvalFailed again.
//
// Mutation: write no hold on a final-verdict pass.
func TestAHoldAfterAFailedVerdictComesBeforeIt(t *testing.T) {
	g, c, _ := exemptJ2With(t, "jtwofailed", 0, nil, evalCase("a", "one", "nope", ""))
	g.evalPass(t)
	g.agentPass(t)
	condIs(t, g.a, assaydv1alpha1.CondGatesPassed, metav1.ConditionFalse, controller.ReasonEvalFailed)
	key := gatewayAuthPolicy(t, g.a, g.prober)
	g.evalPass(t)
	g.agentPass(t)
	held := g.waitingForAuth(t, "a hold after the Failed verdict")
	if !strings.Contains(held.Message, key.Name) ||
		!strings.Contains(held.Message, "no case of it is claimed whether or not the hold clears") ||
		strings.Contains(held.Message, "until it clears") {
		t.Errorf("the hold does not name the policy, or says cases may be claimed for a failed pair: %s",
			held.Message)
	}
	removePolicy(t, key)
	g.evalPass(t)
	g.agentPass(t)
	condIs(t, g.a, assaydv1alpha1.CondGatesPassed, metav1.ConditionFalse, controller.ReasonEvalFailed)
	if g.hits(c) != 1 {
		t.Errorf("C was sent %d cases, want its one", g.hits(c))
	}
}

// The failed-pair branch's hold, as the Failed-verdict row's: the pair is in
// status.eval.failed[] with no verdict recorded for it (#111's clear leaves
// that), and a hold that appears reads EvalWaitingForAuth, worded for a pair
// that has failed, ahead of EvalPairAlreadyFailed; once it clears,
// EvalPairAlreadyFailed again.
//
// Mutation: write no hold on the failed-pair branch's pass.
func TestAHoldBesideAFailedPairComesBeforeIt(t *testing.T) {
	g, c, _ := exemptJ2With(t, "jtwofailedpair", 0, nil, evalCase("a", "one", "nope", ""))
	g.evalPass(t)
	writeAgentStatus(t, g.a, func(st *assaydv1alpha1.AgentStatus) {
		st.Eval.Verdict, st.Eval.Cases, st.Eval.Score, st.Eval.At = "", nil, "", nil
	})
	g.agentPass(t)
	condIs(t, g.a, assaydv1alpha1.CondGatesPassed, metav1.ConditionFalse, controller.ReasonEvalPairAlreadyFailed)
	key := gatewayAuthPolicy(t, g.a, g.prober)
	g.evalPass(t)
	g.agentPass(t)
	held := g.waitingForAuth(t, "a hold beside the failed pair")
	if !strings.Contains(held.Message, key.Name) ||
		!strings.Contains(held.Message, "no case of it is claimed whether or not the hold clears") {
		t.Errorf("the hold does not name the policy and the failed pair: %s", held.Message)
	}
	removePolicy(t, key)
	g.evalPass(t)
	g.agentPass(t)
	condIs(t, g.a, assaydv1alpha1.CondGatesPassed, metav1.ConditionFalse, controller.ReasonEvalPairAlreadyFailed)
	if g.hits(c) != 1 {
		t.Errorf("C was sent %d cases, want its one", g.hits(c))
	}
}
