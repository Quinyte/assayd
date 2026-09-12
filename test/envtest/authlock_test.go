// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package envtest

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/compiler"
	"github.com/Quinyte/assayd/internal/controller"
	"github.com/Quinyte/assayd/internal/revision"
)

// Design 03 slice PR 5's §8.1 cases: J2's Lock (6), abandonment (10), the
// custom card path and every attribution half (11), the Lock half of the
// deadline (12), Adopt keyed on the whole record (13), a re-created route's
// target (15), a prune's crash half (16), K2 (17), and the NACK source check
// and ForeignTrafficPolicy. The probe is §8.1's pinned oracle (stubProber).

// servedNoneAgent is an `auth: none` Agent taken through Create to Served.
func servedNoneAgent(t *testing.T, name string, mutate func(*assaydv1alpha1.Agent)) (
	*assaydv1alpha1.Agent, *controller.AgentReconciler, *stubProber) {
	t.Helper()
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, name, func(x *assaydv1alpha1.Agent) {
		x.Spec.Expose = &assaydv1alpha1.ExposeSpec{A2A: &assaydv1alpha1.ExposeProtocol{Auth: "none"}}
		if mutate != nil {
			mutate(x)
		}
	})
	r, stub := createReconciler()
	promote(t, r, a)
	acceptRoute(t, ns, name)
	reconcileOnce(t, r, a)
	if auth := authOf(t, a); auth == nil || auth.Mode != "none" || auth.Transaction != nil {
		t.Fatalf("the none Agent did not reach Served: %+v", auth)
	}
	return liveAgent(t, a), r, stub
}

func toMode(t *testing.T, a *assaydv1alpha1.Agent, mode string) {
	t.Helper()
	mustEdit(t, a, func(x *assaydv1alpha1.Agent) {
		card := x.Spec.Card
		x.Spec.Expose = &assaydv1alpha1.ExposeSpec{A2A: &assaydv1alpha1.ExposeProtocol{Auth: mode}}
		x.Spec.Card = card
	})
}

// recordCardFor writes the digest the operator's own card fetch would record
// for revision rev: the digest of the card the stub's backend for rev serves.
func recordCardFor(t *testing.T, a *assaydv1alpha1.Agent, rev, revDigest string) {
	t.Helper()
	live := liveAgent(t, a)
	now := metav1.Now()
	kept := []assaydv1alpha1.CardStatus{}
	for _, c := range live.Status.Cards {
		if c.Revision != rev {
			kept = append(kept, c)
		}
	}
	live.Status.Cards = append(kept, assaydv1alpha1.CardStatus{Revision: rev, RevisionDigest: revDigest,
		Name: a.Name, Digest: cardDigestOf(controller.WorkloadName(a.Name, rev)), FetchedAt: &now})
	if err := k8s.Status().Update(context.Background(), live); err != nil {
		t.Fatalf("record the card of %s: %v", rev, err)
	}
}

// forgetCards removes every card digest, as a fetch that never succeeded
// leaves status.cards.
func forgetCards(t *testing.T, a *assaydv1alpha1.Agent) {
	t.Helper()
	live := liveAgent(t, a)
	live.Status.Cards = nil
	if err := k8s.Status().Update(context.Background(), live); err != nil {
		t.Fatal(err)
	}
}

// lockPass is one pass of a J2 or K2 Lock, which must never leave the route
// without backendRefs (§3.3.3).
func lockPass(t *testing.T, r *controller.AgentReconciler, a *assaydv1alpha1.Agent) {
	t.Helper()
	reconcileOnce(t, r, a)
	if !routePublished(t, a.Namespace, a.Name) {
		t.Fatalf("the route lost its backendRefs during a Lock; it serves as before the edit until "+
			"Served, and a Lock never withdraws it (design 03 §3.3.3): tx=%+v", txOf(t, a))
	}
}

func marker(t *testing.T, a *assaydv1alpha1.Agent) string {
	t.Helper()
	rt := servingRoute(t, a.Namespace, a.Name)
	if rt == nil {
		return "<no route>"
	}
	return rt.Labels[controller.LabelAuth]
}

// §8.1 case 6: J2's Lock. A served auth: none Agent moved to apikey is never
// without backendRefs, its edit's weight shift is not held, gets <agent>-auth
// written, and reaches Served, with GovernanceSkipped cleared, only after the
// stub's answer turns from 200 to 401. The J2 edit's revision is promoted
// during the Lock, and its card is recorded, so the 401 is attributed on it.
func TestJ2LocksAServedNoneAgentInPlace(t *testing.T) {
	a, r, stub := servedNoneAgent(t, "jtwo", nil)
	r1, d1 := a.Status.ActiveRevision, a.Status.ActiveRevisionDigest
	recordCardFor(t, a, r1, d1)
	stub.hold("jtwo", true)
	toMode(t, a, "apikey")
	edited := liveAgent(t, a)
	r2, d2 := revision.MustHash(edited.Spec), revision.MustDigest(edited.Spec)

	lockPass(t, r, a)
	tx := txOf(t, a)
	if tx == nil || tx.Kind != "Lock" || tx.TargetMode != "apikey" || !tx.Written ||
		tx.Probe == nil || tx.Probe.Before == nil || *tx.Probe.Before != 200 ||
		!tx.BeforeObserved || tx.BeforeRevision != r1 {
		t.Fatalf("J2 did not enter a Lock that observed the open route and wrote its policy: %+v", tx)
	}
	if policyExists(t, runNS(a.Namespace), policyNameOf(a)) == nil {
		t.Fatal("the Lock wrote no <agent>-auth")
	}
	if m := marker(t, a); m != "none" {
		t.Errorf("the no-auth marker is %q before Served; only Served removes it", m)
	}
	g := condIs(t, a, assaydv1alpha1.CondGovernanceSkipped, metav1.ConditionTrue, "AuthLockPending")
	if g != nil && !strings.Contains(g.Message, "ONE-WAY") {
		t.Errorf("AuthLockPending does not say the lock is one-way: %s", g.Message)
	}
	condIs(t, a, assaydv1alpha1.CondReady, metav1.ConditionTrue, "Available")

	// The weight shift is not held: the J2 edit's revision is promoted mid-Lock,
	// the route follows it, and beforeObserved is cleared with it.
	markAvailable(t, a.Namespace, controller.WorkloadName("jtwo", r2), 1)
	lockPass(t, r, a)
	if got := liveAgent(t, a).Status.ActiveRevision; got != r2 {
		t.Fatalf("the J2 edit's revision was held (active %s, want %s); a Lock does not hold its "+
			"edit's weight shift (§1.1)", got, r2)
	}
	if rt := servingRoute(t, a.Namespace, "jtwo"); string(rt.Spec.Rules[0].BackendRefs[0].Name) !=
		controller.WorkloadName("jtwo", r2) {
		t.Errorf("the route did not follow the promotion: %+v", rt.Spec.Rules)
	}
	if tx := txOf(t, a); tx.BeforeObserved || tx.BeforeRevision != "" {
		t.Errorf("a promotion did not clear beforeObserved: %+v", tx)
	}
	recordCardFor(t, a, r2, d2)
	acceptRoute(t, a.Namespace, "jtwo")
	acceptPolicy(t, a.Namespace, "jtwo")
	for i := 0; i < 2; i++ {
		lockPass(t, r, a)
		if auth := authOf(t, a); auth.Mode != "none" || auth.Transaction == nil {
			t.Fatalf("pass %d recorded the lock while the stub still answered %d: %+v",
				i, stub.last["jtwo"], auth)
		}
	}
	if tx := txOf(t, a); tx.Stage != "ProbingAfter" || *tx.Probe.After != 200 {
		t.Fatalf("with the switch held the Lock waits in ProbingAfter on a 200: %+v", tx)
	}

	stub.hold("jtwo", false)
	lockPass(t, r, a)
	auth := authOf(t, a)
	if auth.Mode != "apikey" || auth.Transaction != nil || auth.AppliedDigest == "" {
		t.Fatalf("the Lock did not reach Served on the 401: %+v", auth)
	}
	if m := marker(t, a); m != "" {
		t.Errorf("the no-auth marker survived Served: %q", m)
	}
	g = condIs(t, a, assaydv1alpha1.CondGovernanceSkipped, metav1.ConditionFalse, "AuthVerifiedOnOneReplica")
	if g != nil && !strings.Contains(g.Message, "transition not observed") {
		t.Errorf("a 401 attributed by the promoted revision's digest must say the transition was "+
			"not observed: %s", g.Message)
	}
}

// §8.1 case 6's failure half and case 12's Lock half: with the switch held past
// the deadline, or with policy status the test never writes, the Agent reports
// PolicyApplyIncomplete, reason AuthLockUnverified, naming the stage, and
// status.auth.mode stays none.
func TestAJ2LockHasADeadlineOutcomeInEveryStage(t *testing.T) {
	t.Run("ProbingAfter", func(t *testing.T) {
		a, r, stub := servedNoneAgent(t, "jtwoheld", nil)
		recordCardFor(t, a, a.Status.ActiveRevision, a.Status.ActiveRevisionDigest)
		r.AuthDeadline = time.Millisecond
		stub.hold("jtwoheld", true)
		toMode(t, a, "apikey")
		lockPass(t, r, a)
		acceptPolicy(t, a.Namespace, "jtwoheld")
		lockPass(t, r, a)
		c := condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "AuthLockUnverified")
		if c != nil && (!strings.Contains(c.Message, "ProbingAfter") || !strings.Contains(c.Message, "200")) {
			t.Errorf("the deadline's message names neither the stage nor the last answer: %s", c.Message)
		}
		condIs(t, a, assaydv1alpha1.CondReady, metav1.ConditionFalse, "AuthLockUnverified")
		if auth := authOf(t, a); auth.Mode != "none" {
			t.Errorf("status.auth.mode is %q past the deadline; it stays none", auth.Mode)
		}
	})
	t.Run("Converging", func(t *testing.T) {
		a, r, _ := servedNoneAgent(t, "jtwoconv", nil)
		r.AuthDeadline = time.Millisecond
		toMode(t, a, "apikey")
		lockPass(t, r, a)
		lockPass(t, r, a)
		if tx := txOf(t, a); tx == nil || tx.Stage != "Converging" {
			t.Fatalf("want the Lock waiting in Converging: %+v", tx)
		}
		c := condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "AuthLockUnverified")
		if c != nil && !strings.Contains(c.Message, "Converging") {
			t.Errorf("the deadline's message does not name Converging: %s", c.Message)
		}
	})
}

// §8.1 case 11: a Lock on a custom card path, and a before-state it cannot
// observe. The stub serves the card only at /card.json.
func TestTheLockProbesTheCardPathAndAttributesEvery401(t *testing.T) {
	custom := func(x *assaydv1alpha1.Agent) { x.Spec.Card.Path = "/card.json" }

	t.Run("observed on the custom path", func(t *testing.T) {
		a, r, stub := servedNoneAgent(t, "cardpath", custom)
		recordCardFor(t, a, a.Status.ActiveRevision, a.Status.ActiveRevisionDigest)
		toMode(t, a, "apikey") // the revision it mints stays unready, so r1 serves
		stub.hold("cardpath", true)
		lockPass(t, r, a)
		if tx := txOf(t, a); !tx.BeforeObserved {
			t.Fatalf("the before-request on %s did not observe the open route: %+v", "/card.json", tx)
		}
		acceptPolicy(t, a.Namespace, "cardpath")
		stub.hold("cardpath", false)
		// The pass that reaches Served says how the 401 was attributed; a later
		// pass says what the served record earns (design 03 A72).
		lockPass(t, r, a)
		g := condIs(t, a, assaydv1alpha1.CondGovernanceSkipped, metav1.ConditionFalse, "AuthVerifiedOnOneReplica")
		if g != nil && !strings.Contains(g.Message, "transition was observed") {
			t.Errorf("Served after an observed 200 must say the transition was observed: %s", g.Message)
		}
	})

	t.Run("a 503 before is still written", func(t *testing.T) {
		a, r, stub := servedNoneAgent(t, "cardbusy", custom)
		recordCardFor(t, a, a.Status.ActiveRevision, a.Status.ActiveRevisionDigest)
		toMode(t, a, "apikey")
		stub.backendOverride("cardbusy", 503)
		stub.hold("cardbusy", true)
		lockPass(t, r, a)
		if policyExists(t, runNS(a.Namespace), policyNameOf(a)) == nil {
			t.Fatal("a 503 before-answer gated the write; ProbingBefore is an observation, never a gate")
		}
		if tx := txOf(t, a); tx.BeforeObserved || tx.Probe.Before == nil || *tx.Probe.Before != 503 {
			t.Errorf("the 503 was not recorded as an unobserved before-state: %+v", tx)
		}
		stub.backendOverride("cardbusy", 0)
		acceptPolicy(t, a.Namespace, "cardbusy")
		stub.hold("cardbusy", false)
		lockPass(t, r, a)
		g := condIs(t, a, assaydv1alpha1.CondGovernanceSkipped, metav1.ConditionFalse, "AuthVerifiedOnOneReplica")
		if g != nil && !strings.Contains(g.Message, "transition not observed") {
			t.Errorf("Served after an unobserved before-state must say so: %s", g.Message)
		}
	})

	t.Run("a backend's own 401 with no digest", func(t *testing.T) {
		a, r, stub := servedNoneAgent(t, "cardless", custom)
		forgetCards(t, a)
		r.AuthDeadline = time.Millisecond
		stub.backendOverride("cardless", 401)
		stub.hold("cardless", true)
		toMode(t, a, "apikey")
		lockPass(t, r, a)
		acceptPolicy(t, a.Namespace, "cardless")
		lockPass(t, r, a)
		lockPass(t, r, a)
		if auth := authOf(t, a); auth.Mode != "none" || auth.Transaction == nil {
			t.Fatalf("a backend's own 401, with no card digest recorded, ended the Lock: %+v", auth)
		}
		c := condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "AuthLockUnverified")
		if c != nil && !strings.Contains(c.Message, "cannot be attributed") {
			t.Errorf("the deadline does not say the 401 could not be attributed: %s", c.Message)
		}
	})

	t.Run("a promotion clears beforeObserved", func(t *testing.T) {
		a, r, stub := servedNoneAgent(t, "cardpromo", custom)
		recordCardFor(t, a, a.Status.ActiveRevision, a.Status.ActiveRevisionDigest)
		r.AuthDeadline = time.Millisecond
		stub.hold("cardpromo", true)
		toMode(t, a, "apikey")
		r2 := revision.MustHash(liveAgent(t, a).Spec)
		lockPass(t, r, a)
		if tx := txOf(t, a); !tx.BeforeObserved {
			t.Fatalf("r1's card was not observed: %+v", tx)
		}
		markAvailable(t, a.Namespace, controller.WorkloadName("cardpromo", r2), 1)
		stub.backendOverride("cardpromo", 401) // r2's backend refuses its own card path
		lockPass(t, r, a)
		acceptRoute(t, a.Namespace, "cardpromo")
		acceptPolicy(t, a.Namespace, "cardpromo")
		lockPass(t, r, a)
		lockPass(t, r, a)
		if auth := authOf(t, a); auth.Mode != "none" || auth.Transaction == nil {
			t.Fatalf("r2's own 401 passed for the lock on r1's observation: %+v", auth)
		}
		condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "AuthLockUnverified")
	})

	t.Run("another revision's card before", func(t *testing.T) {
		a, r, stub := servedNoneAgent(t, "cardstale", custom)
		r1 := a.Status.ActiveRevision
		recordCardFor(t, a, r1, a.Status.ActiveRevisionDigest)
		// r2 is promoted by a pass that does not reach the -auth step, so the
		// Lock's before-request comes right after it, while the gateway still
		// answers with r1's card.
		toMode(t, a, "apikey")
		r2 := revision.MustHash(liveAgent(t, a).Spec)
		off := newReconciler(false)
		reconcileOnce(t, off, a)
		markAvailable(t, a.Namespace, controller.WorkloadName("cardstale", r2), 1)
		reconcileOnce(t, off, a)
		if got := liveAgent(t, a).Status.ActiveRevision; got != r2 {
			t.Fatalf("r2 was not promoted: %s", got)
		}
		r.AuthDeadline = time.Millisecond
		stub.serveFrom("cardstale", controller.WorkloadName("cardstale", r1))
		stub.hold("cardstale", true)
		lockPass(t, r, a)
		if tx := txOf(t, a); tx == nil || tx.BeforeObserved {
			t.Fatalf("r1's card on a route naming r2 set beforeObserved: %+v", tx)
		}
		stub.serveFrom("cardstale", "")
		stub.backendOverride("cardstale", 401)
		acceptRoute(t, a.Namespace, "cardstale")
		acceptPolicy(t, a.Namespace, "cardstale")
		lockPass(t, r, a)
		lockPass(t, r, a)
		if auth := authOf(t, a); auth.Mode != "none" || auth.Transaction == nil {
			t.Fatalf("r2's own 401 passed for the lock: %+v", auth)
		}
		condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "AuthLockUnverified")
	})
}

// deleteOrder records the gateway objects the operator deletes, in order.
type deleteOrder struct {
	client.Client
	mu   sync.Mutex
	seen []string
}

func (c *deleteOrder) Delete(ctx context.Context, o client.Object, opts ...client.DeleteOption) error {
	kind := ""
	switch x := o.(type) {
	case *gatewayv1.HTTPRoute:
		kind = "HTTPRoute"
	case *unstructured.Unstructured:
		kind = x.GetKind()
	}
	if kind != "" {
		c.mu.Lock()
		c.seen = append(c.seen, kind)
		c.mu.Unlock()
	}
	return c.Client.Delete(ctx, o, opts...)
}

// §8.1 case 10: abandonment deletes only what it wrote.
func TestAbandonmentDeletesOnlyWhatItWrote(t *testing.T) {
	t.Run("(a) a J2 Lock reverted to none", func(t *testing.T) {
		a, r, stub := servedNoneAgent(t, "abandonj2", nil)
		recordCardFor(t, a, a.Status.ActiveRevision, a.Status.ActiveRevisionDigest)
		stub.hold("abandonj2", true)
		toMode(t, a, "apikey")
		lockPass(t, r, a)
		acceptPolicy(t, a.Namespace, "abandonj2")
		lockPass(t, r, a)
		if tx := txOf(t, a); tx == nil || tx.Stage != "ProbingAfter" || !tx.Written {
			t.Fatalf("want the Lock waiting in ProbingAfter with its policy written: %+v", tx)
		}
		toMode(t, a, "none")
		lockPass(t, r, a)
		if policyExists(t, runNS(a.Namespace), policyNameOf(a)) != nil {
			t.Error("the abandoned Lock's policy was kept: the route may be locked while status says open")
		}
		if auth := authOf(t, a); auth.Mode != "none" || auth.Transaction != nil {
			t.Errorf("status.auth is %+v, want {mode: none}", auth)
		}
		if m := marker(t, a); m != "none" {
			t.Errorf("the marker is %q after the abandonment; it never left", m)
		}
		condIs(t, a, assaydv1alpha1.CondGovernanceSkipped, metav1.ConditionTrue, "AuthOptedOut")
	})
	t.Run("(b) a Create reverted to none", func(t *testing.T) {
		ns := newNamespace(t)
		a := mustCreateAgent(t, ns, "abandoncreate", nil)
		r, stub := createReconciler()
		stub.hold("abandoncreate", true)
		promote(t, r, a)
		acceptRoute(t, ns, "abandoncreate")
		acceptPolicy(t, ns, "abandoncreate")
		reconcileOnce(t, r, a)
		if tx := txOf(t, a); tx.Stage != "ProbingAfter" {
			t.Fatalf("want ProbingAfter: %+v", tx)
		}
		toMode(t, a, "none")
		reconcileOnce(t, r, a)
		if policyExists(t, runNS(ns), policyNameOf(a)) != nil {
			t.Error("the abandoned Create's policy was kept")
		}
		if !routePublished(t, ns, "abandoncreate") || marker(t, a) != "none" {
			t.Errorf("the successor Create of none did not publish the route with its marker: %q", marker(t, a))
		}
	})
	t.Run("(c) a recorded policy is never deleted", func(t *testing.T) {
		a, r, _ := servedAPIKeyAgent(t, "abandonkept")
		before := policyExists(t, runNS(a.Namespace), policyNameOf(a))
		toMode(t, a, "none")
		reconcileOnce(t, r, a)
		toMode(t, a, "oauth")
		reconcileOnce(t, r, a)
		after := policyExists(t, runNS(a.Namespace), policyNameOf(a))
		if after == nil || after.GetResourceVersion() != before.GetResourceVersion() {
			t.Fatalf("a policy status.auth records as served was deleted or rewritten (%v)", after)
		}
	})
	t.Run("(d) the route goes before the policy", func(t *testing.T) {
		ns := newNamespace(t)
		a := mustCreateAgent(t, ns, "abandonpub", nil)
		r, _ := createReconciler()
		order := &deleteOrder{Client: k8s}
		r.Client = order
		promote(t, r, a)
		acceptRoute(t, ns, "abandonpub")
		acceptPolicy(t, ns, "abandonpub")
		reconcileOnce(t, r, a)
		if tx := txOf(t, a); tx.Stage != "Publishing" || !routePublished(t, ns, "abandonpub") {
			t.Fatalf("want Publishing with the route published: %+v", tx)
		}
		toMode(t, a, "oauth")
		reconcileOnce(t, r, a)
		if servingRoute(t, ns, "abandonpub") != nil || policyExists(t, runNS(ns), policyNameOf(a)) != nil {
			t.Fatal("an abandonment to oauth left the route or the policy")
		}
		order.mu.Lock()
		seen := strings.Join(order.seen, ",")
		order.mu.Unlock()
		if seen != "HTTPRoute,"+compiler.PolicyKind {
			t.Errorf("the deletes were %s; the route must lose its backendRefs before the policy goes, "+
				"or it carries backends with no policy", seen)
		}
	})
	t.Run("(e) a J2 Lock reverted to oauth", func(t *testing.T) {
		a, r, stub := servedNoneAgent(t, "abandonoauth", nil)
		stub.hold("abandonoauth", true)
		toMode(t, a, "apikey")
		lockPass(t, r, a)
		toMode(t, a, "oauth")
		lockPass(t, r, a)
		if policyExists(t, runNS(a.Namespace), policyNameOf(a)) != nil {
			t.Error("the abandoned Lock's policy was kept")
		}
		if auth := authOf(t, a); auth.Mode != "none" || auth.Transaction != nil {
			t.Errorf("status.auth is %+v, want {mode: none}", auth)
		}
		c := condIs(t, a, assaydv1alpha1.CondPolicyCompileFailed, metav1.ConditionTrue, "AuthInputAbsent")
		if c != nil && !strings.Contains(c.Message, "UNAUTHENTICATED again") {
			t.Errorf("PolicyCompileFailed does not say the route serves unauthenticated again: %s", c.Message)
		}
	})
}

// §8.1 case 13: Adopt keys on the whole record. A Create stopped after
// Publishing's route write and before Served, with <agent>-auth then deleted,
// re-enters Create at its write and never becomes Adopt.
func TestAdoptKeysOnTheWholeRecord(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "wholerecord", nil)
	r, _ := createReconciler()
	promote(t, r, a)
	acceptRoute(t, ns, "wholerecord")
	acceptPolicy(t, ns, "wholerecord")
	reconcileOnce(t, r, a)
	if tx := txOf(t, a); tx.Stage != "Publishing" || !routePublished(t, ns, "wholerecord") {
		t.Fatalf("want Publishing with the route published: %+v", tx)
	}
	deletePolicy(t, a)
	reconcileOnce(t, r, a)
	tx := txOf(t, a)
	if tx == nil || tx.Kind != "Create" {
		t.Fatalf("a published route beside a Create with no mode was read as %+v", tx)
	}
	if policyExists(t, runNS(ns), policyNameOf(a)) == nil {
		t.Error("the Create did not re-enter at its write")
	}
	if routePublished(t, ns, "wholerecord") {
		t.Error("the route kept its backendRefs while its policy was written again")
	}
}

// §8.1 case 15 (a) and (b): a re-created route takes its target from
// status.auth. (c) is TestANoneAgentIsPublishedAtOnceWithItsMarker.
func TestARecreatedRouteTakesItsTargetFromTheRecord(t *testing.T) {
	for _, mode := range []string{"none", "oauth"} {
		t.Run("spec "+mode, func(t *testing.T) {
			a, r, stub := servedAPIKeyAgent(t, "recreate"+mode)
			toMode(t, a, mode)
			reconcileOnce(t, r, a)
			stub.hold(a.Name, true)
			deleteRoute(t, a)
			reconcileOnce(t, r, a)
			assertNeverOpen(t, a)
			if routePublished(t, a.Namespace, a.Name) || marker(t, a) != "" {
				t.Fatalf("the route came back published or with a marker (%q) before any probe", marker(t, a))
			}
			acceptRoute(t, a.Namespace, a.Name)
			acceptPolicy(t, a.Namespace, a.Name)
			stub.hold(a.Name, false)
			for i := 0; i < 3; i++ {
				reconcileOnce(t, r, a)
				assertNeverOpen(t, a)
				acceptRoute(t, a.Namespace, a.Name)
			}
			if !routePublished(t, a.Namespace, a.Name) || marker(t, a) != "" {
				t.Errorf("the re-created route was not published under the recorded apikey (marker %q)", marker(t, a))
			}
			if auth := authOf(t, a); auth.Mode != "apikey" || auth.Transaction != nil {
				t.Errorf("the re-create did not reach Served under the record: %+v", auth)
			}
		})
	}
}

// §8.1 case 16's crash half: the operator stopped after it created the
// prepared route and before any status update, simulated by stripping the
// route beside the missing-policy Lock. The restarted operator records the
// Create and publishes after the 401.
func TestAnEmptyRouteBesideAMissingPolicyLockIsPublishedThroughCreate(t *testing.T) {
	a, r, stub := servedAPIKeyAgent(t, "crashhalf")
	recordCard(t, a)
	stub.hold("crashhalf", true)
	deletePolicy(t, a)
	reconcileOnce(t, r, a)
	if tx := txOf(t, a); tx == nil || tx.Kind != "Lock" {
		t.Fatalf("want the missing-policy Lock: %+v", tx)
	}
	rt := servingRoute(t, a.Namespace, "crashhalf")
	rt.Spec.Rules[0].BackendRefs = nil
	if err := k8s.Update(context.Background(), rt); err != nil {
		t.Fatalf("strip the route: %v", err)
	}
	reconcileOnce(t, r, a)
	if tx := txOf(t, a); tx == nil || tx.Kind != "Create" {
		t.Fatalf("an empty route beside a missing-policy Lock did not record the re-create: %+v", tx)
	}
	stub.hold("crashhalf", false)
	for i := 0; i < 3; i++ {
		acceptRoute(t, a.Namespace, "crashhalf")
		acceptPolicy(t, a.Namespace, "crashhalf")
		reconcileOnce(t, r, a)
	}
	if auth := authOf(t, a); !routePublished(t, a.Namespace, "crashhalf") || auth.Mode != "apikey" ||
		auth.Transaction != nil {
		t.Errorf("the route was not published through Create's probe: %+v", auth)
	}
}

// legacyAgent is an Agent whose route an operator without a compiler
// published, refused as Adopt by this one.
func legacyAgent(t *testing.T, name string, mutate func(*assaydv1alpha1.Agent)) (
	*assaydv1alpha1.Agent, *controller.AgentReconciler, *stubProber) {
	t.Helper()
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, name, mutate)
	off := newReconciler(false)
	rev := revision.MustHash(a.Spec)
	reconcileOnce(t, off, a)
	reconcileOnce(t, off, a)
	markAvailable(t, ns, controller.WorkloadName(name, rev), 1)
	settle(t, off, a)
	routeName, _ := compiler.ServingRouteName(name)
	group, kind := gatewayv1.Group(gatewayv1.GroupName), gatewayv1.Kind("Gateway")
	svcGroup, svcKind := gatewayv1.Group(""), gatewayv1.Kind("Service")
	gwNS, section := gatewayv1.Namespace("assayd-gateway"), gatewayv1.SectionName("http")
	port, weight := gatewayv1.PortNumber(8080), int32(100)
	legacy := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: routeName, Namespace: runNS(ns), Labels: map[string]string{
			controller.LabelAgent: name, controller.LabelAgentUID: string(a.UID),
			controller.LabelAgentNamespace: ns, controller.LabelRevision: rev}},
		Spec: gatewayv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{ParentRefs: []gatewayv1.ParentReference{{
				Group: &group, Kind: &kind, Name: "assayd", Namespace: &gwNS, SectionName: &section}}},
			Hostnames: []gatewayv1.Hostname{gatewayv1.Hostname(name + "." + ns + "." +
				controller.DefaultGatewayHostnameSuffix)},
			Rules: []gatewayv1.HTTPRouteRule{{BackendRefs: []gatewayv1.HTTPBackendRef{{
				BackendRef: gatewayv1.BackendRef{BackendObjectReference: gatewayv1.BackendObjectReference{
					Group: &svcGroup, Kind: &svcKind,
					Name: gatewayv1.ObjectName(controller.WorkloadName(name, rev)), Port: &port},
					Weight: &weight}}}}},
		},
	}
	if err := k8s.Create(context.Background(), legacy); err != nil {
		t.Fatalf("plant the legacy route: %v", err)
	}
	r, stub := createReconciler()
	reconcileOnce(t, r, a)
	reconcileOnce(t, r, a)
	if tx := txOf(t, a); tx == nil || tx.Kind != "Adopt" {
		t.Fatalf("the legacy route was not refused as Adopt: %+v", tx)
	}
	return liveAgent(t, a), r, stub
}

func noneSpec(x *assaydv1alpha1.Agent) {
	x.Spec.Expose = &assaydv1alpha1.ExposeSpec{A2A: &assaydv1alpha1.ExposeProtocol{Auth: "none"}}
}

// §8.1 case 17: K2. An owner's edit ends Adopt's refusal.
func TestK2AnOwnersEditEndsTheRefusal(t *testing.T) {
	t.Run("an edit to apikey locks in place", func(t *testing.T) {
		a, r, stub := legacyAgent(t, "ktwo", noneSpec)
		if tx := txOf(t, a); tx.RefusedMode != "none" {
			t.Fatalf("refusedMode is %q, want none", tx.RefusedMode)
		}
		recordCardFor(t, a, a.Status.ActiveRevision, a.Status.ActiveRevisionDigest)
		stub.hold("ktwo", true)
		toMode(t, a, "apikey")
		lockPass(t, r, a)
		tx := txOf(t, a)
		if tx == nil || tx.Kind != "Lock" || !tx.BeforeObserved {
			t.Fatalf("K2's edit did not enter a Lock that observed the open route: %+v", tx)
		}
		if auth := authOf(t, a); auth.Mode != "" {
			t.Errorf("K2's Lock recorded mode %q; no mode is recorded until Served", auth.Mode)
		}
		condIs(t, a, assaydv1alpha1.CondGovernanceSkipped, metav1.ConditionTrue, "AuthLockPending")
		acceptRoute(t, a.Namespace, "ktwo")
		acceptPolicy(t, a.Namespace, "ktwo")
		lockPass(t, r, a)
		if auth := authOf(t, a); auth.Mode != "" || auth.Transaction == nil {
			t.Fatalf("K2's Lock was recorded while the stub answered 200: %+v", auth)
		}
		stub.hold("ktwo", false)
		lockPass(t, r, a)
		if auth := authOf(t, a); auth.Mode != "apikey" || auth.Transaction != nil {
			t.Fatalf("K2's Lock did not reach Served on the 401: %+v", auth)
		}
		condIs(t, a, assaydv1alpha1.CondGovernanceSkipped, metav1.ConditionFalse, "AuthVerifiedOnOneReplica")
	})
	t.Run("apikey at the refusal is not consent", func(t *testing.T) {
		a, r, stub := legacyAgent(t, "ktwoasked", nil)
		if tx := txOf(t, a); tx.RefusedMode != "apikey" {
			t.Fatalf("refusedMode is %q, want apikey", tx.RefusedMode)
		}
		for i := 0; i < 3; i++ {
			reconcileOnce(t, r, a)
		}
		if tx := txOf(t, a); tx == nil || tx.Kind != "Adopt" {
			t.Fatalf("a spec that already said apikey was taken as consent: %+v", tx)
		}
		if policyExists(t, runNS(a.Namespace), policyNameOf(a)) != nil || stub.count("ktwoasked") != 0 {
			t.Error("a policy was written, or a probe sent, for an Agent still refused")
		}
		// Fourth half: an edit to none and back to apikey is consent.
		toMode(t, a, "none")
		reconcileOnce(t, r, a)
		if tx := txOf(t, a); tx.Kind != "Adopt" || tx.RefusedMode != "none" {
			t.Fatalf("refusedMode did not track the edit to none: %+v", tx)
		}
		toMode(t, a, "apikey")
		lockPass(t, r, a)
		if tx := txOf(t, a); tx == nil || tx.Kind != "Lock" {
			t.Fatalf("an edit to none and back to apikey did not enter Lock: %+v", tx)
		}
	})
	t.Run("reverted before Served", func(t *testing.T) {
		a, r, stub := legacyAgent(t, "ktworevert", noneSpec)
		stub.hold("ktworevert", true)
		toMode(t, a, "apikey")
		lockPass(t, r, a)
		if policyExists(t, runNS(a.Namespace), policyNameOf(a)) == nil {
			t.Fatal("K2's Lock wrote no policy")
		}
		toMode(t, a, "none")
		lockPass(t, r, a)
		if policyExists(t, runNS(a.Namespace), policyNameOf(a)) != nil {
			t.Error("the abandoned K2 Lock's policy was kept")
		}
		tx := txOf(t, a)
		if tx == nil || tx.Kind != "Adopt" || tx.Stage != "Refused" || tx.RefusedMode != "none" {
			t.Errorf("an abandoned K2 Lock did not return to the refused Adopt: %+v", tx)
		}
		if auth := authOf(t, a); auth.Mode != "" {
			t.Errorf("an abandoned K2 Lock recorded mode %q", auth.Mode)
		}
		condIs(t, a, assaydv1alpha1.CondGovernanceSkipped, metav1.ConditionTrue, "CompilerUpgradeUnsupported")
		toMode(t, a, "apikey")
		lockPass(t, r, a)
		if tx := txOf(t, a); tx == nil || tx.Kind != "Lock" {
			t.Errorf("a second edit to apikey did not enter Lock again: %+v", tx)
		}
	})
}

func ensureGatewayNamespace(t *testing.T) {
	t.Helper()
	err := k8s.Create(context.Background(), &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "assayd-gateway"}})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatal(err)
	}
}

// nackFor is a NACK naming a's policy, seen at `at`, from `component`.
func nackFor(t *testing.T, a *assaydv1alpha1.Agent, name, component string, at time.Time) {
	t.Helper()
	policy := policyNameOf(a)
	route, _ := compiler.ServingRouteName(a.Name)
	ev := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "assayd-gateway"},
		InvolvedObject: corev1.ObjectReference{APIVersion: "gateway.networking.k8s.io/v1", Kind: "Gateway",
			Name: "assayd", Namespace: "assayd-gateway"},
		Type: corev1.EventTypeWarning, Reason: controller.NackEventReason,
		Source:              corev1.EventSource{Component: component},
		ReportingController: component, ReportingInstance: "agentgateway-0",
		FirstTimestamp: metav1.NewTime(at), LastTimestamp: metav1.NewTime(at), Count: 1,
		Message: `[{"key":"policy/traffic/` + runNS(a.Namespace) + "/" + policy + ":auth:" +
			runNS(a.Namespace) + "/" + route + `","error":"rejected"}]`,
	}
	if err := k8s.Create(context.Background(), ev); err != nil {
		t.Fatalf("create the NACK: %v", err)
	}
	t.Cleanup(func() { _ = k8s.Delete(context.Background(), ev) })
}

// A70's owed check, with §3.3's NACK driving status: a genuine NACK naming
// the policy since its last write raises PolicyApplyIncomplete at once and
// holds the transaction in Converging; a forged one, or one older than the
// write, changes nothing.
func TestOnlyAGenuineFreshNackDrivesStatus(t *testing.T) {
	ensureGatewayNamespace(t)
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "nacked", nil)
	r, stub := createReconciler()
	stub.hold("nacked", true)
	promote(t, r, a)
	acceptRoute(t, ns, "nacked")
	acceptPolicy(t, ns, "nacked")
	reconcileOnce(t, r, a)
	if tx := txOf(t, a); tx.Stage != "ProbingAfter" {
		t.Fatalf("want ProbingAfter: %+v", tx)
	}
	later := time.Now().Add(2 * time.Second)

	nackFor(t, a, "forged-"+ns, "kubectl", later)
	nackFor(t, a, "stale-"+ns, controller.AgentgatewayControllerName, time.Now().Add(-time.Hour))
	reconcileOnce(t, r, a)
	if c := condition(liveAgent(t, a), assaydv1alpha1.CondPolicyApplyIncomplete); c != nil {
		t.Fatalf("a forged or stale NACK drove status: %+v", c)
	}
	if tx := txOf(t, a); tx.Stage != "ProbingAfter" {
		t.Fatalf("a forged or stale NACK moved the transaction: %+v", tx)
	}

	nackFor(t, a, "genuine-"+ns, controller.AgentgatewayControllerName, later)
	reconcileOnce(t, r, a)
	c := condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "AuthEnforcementUnverified")
	if c != nil && (!strings.Contains(c.Message, "rejected") || !strings.Contains(c.Message, "delete that Event")) {
		t.Errorf("the NACK's condition does not say the gateway rejected the policy, and how to "+
			"release the hold: %s", c.Message)
	}
	if tx := txOf(t, a); tx.Stage != "Converging" {
		t.Errorf("a genuine NACK did not return the transaction to Converging: %+v", tx)
	}
	stub.hold("nacked", false)
	reconcileOnce(t, r, a)
	if routePublished(t, ns, "nacked") {
		t.Error("a route was published while a genuine NACK of its policy stood")
	}
}

// §3.2's ForeignTrafficPolicy: a traffic policy on the Agent's route that the
// operator did not emit is reported, holds an unpublished route, withdraws no
// published one, and is never deleted.
func TestAForeignTrafficPolicyIsReportedAndNeverDeleted(t *testing.T) {
	plant := func(t *testing.T, a *assaydv1alpha1.Agent) *unstructured.Unstructured {
		t.Helper()
		p := authPolicyFor(t, liveAgent(t, a))
		p.SetName("intruder")
		p.SetLabels(nil)
		if err := k8s.Create(context.Background(), p); err != nil {
			t.Fatalf("plant the foreign policy: %v", err)
		}
		return p
	}
	t.Run("an unpublished route is not published", func(t *testing.T) {
		ns := newNamespace(t)
		a := mustCreateAgent(t, ns, "foreignnew", nil)
		r, _ := createReconciler()
		promote(t, r, a)
		p := plant(t, a)
		acceptRoute(t, ns, "foreignnew")
		acceptPolicy(t, ns, "foreignnew")
		for i := 0; i < 3; i++ {
			reconcileOnce(t, r, a)
		}
		if routePublished(t, ns, "foreignnew") {
			t.Fatal("a route was published under a foreign traffic policy")
		}
		c := condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "ForeignTrafficPolicy")
		if c != nil && !strings.Contains(c.Message, "intruder") {
			t.Errorf("the condition does not name the foreign policy: %s", c.Message)
		}
		condIs(t, a, assaydv1alpha1.CondGovernanceSkipped, metav1.ConditionTrue, "ForeignTrafficPolicy")
		if err := k8s.Delete(context.Background(), p); err != nil {
			t.Fatal(err)
		}
		reconcileOnce(t, r, a)
		if !routePublished(t, ns, "foreignnew") {
			t.Error("the route was not published once the foreign policy went")
		}
	})
	t.Run("a published route is not withdrawn", func(t *testing.T) {
		a, r, _ := servedAPIKeyAgent(t, "foreignserved")
		plant(t, a)
		reconcileOnce(t, r, a)
		if !routePublished(t, a.Namespace, "foreignserved") {
			t.Error("a foreign policy took a published route off the air")
		}
		condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "ForeignTrafficPolicy")
		condIs(t, a, assaydv1alpha1.CondGovernanceSkipped, metav1.ConditionTrue, "ForeignTrafficPolicy")
		condIs(t, a, assaydv1alpha1.CondReady, metav1.ConditionFalse, "ForeignTrafficPolicy")
		if policyExists(t, runNS(a.Namespace), "intruder") == nil {
			t.Error("the operator deleted a policy it did not emit")
		}
	})
}
