// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
)

// Design 16 §1.1 build step 7: the exemption for a J2 or K2 Lock that can
// finish only through a promotion, at the unit layer — each of clauses 1 to 4
// flipped alone, the gate branch's exempt state and its precedence, clause 5's
// verdict and its read errors, blockedFor's deadline, and the missing-policy
// wedge's exits.

// exemptLock is a J2 Lock that meets clauses 1 to 4: a recorded none, a
// target of apikey, at ProbingAfter, nothing observed before it, and no card
// recorded for status.activeRevision.
func exemptLock(deadline *metav1.Time) *assaydv1alpha1.AgentStatus {
	return &assaydv1alpha1.AgentStatus{ActiveRevision: "active0001", ActiveRevisionDigest: "sha256:active",
		Auth: &assaydv1alpha1.AuthStatus{Mode: "none", Transaction: &assaydv1alpha1.AuthTransaction{
			Kind: TxLock, TargetMode: "apikey", Stage: StageProbingAfter, Deadline: deadline}}}
}

// Each clause of 1 to 4, flipped alone from a Lock that meets all four, ends
// the exemption, and says which clause it was. K2's Lock, with no recorded
// mode, is exempt as J2's is.
func TestEachOfTheFirstFourClausesIsNeededAlone(t *testing.T) {
	if ok, why := exemptTransaction(exemptLock(nil)); !ok {
		t.Fatalf("control: a J2 Lock meeting clauses 1 to 4 is not exempt: %s", why)
	}
	k2 := exemptLock(nil)
	k2.Auth.Mode = ""
	if ok, why := exemptTransaction(k2); !ok {
		t.Fatalf("a K2 Lock, with no recorded mode, is not exempt: %s", why)
	}
	for _, tc := range []struct {
		clause string
		flip   func(*assaydv1alpha1.AgentStatus)
		says   string
	}{
		{"1: a re-creation (targetMode equals the recorded mode)", func(st *assaydv1alpha1.AgentStatus) {
			st.Auth.Mode = "apikey"
		}, "missing policy"},
		{"1: a Create", func(st *assaydv1alpha1.AgentStatus) { st.Auth.Transaction.Kind = TxCreate }, "Create"},
		{"2: before ProbingAfter", func(st *assaydv1alpha1.AgentStatus) {
			st.Auth.Transaction.Stage = StageConverging
		}, "before " + StageProbingAfter},
		{"2: at ProbingBefore", func(st *assaydv1alpha1.AgentStatus) {
			st.Auth.Transaction.Stage = StageProbingBefore
		}, "before " + StageProbingAfter},
		{"3: beforeObserved", func(st *assaydv1alpha1.AgentStatus) { st.Auth.Transaction.BeforeObserved = true },
			"observed the route open"},
		{"4: the active revision carded, by name", func(st *assaydv1alpha1.AgentStatus) {
			st.Cards = []assaydv1alpha1.CardStatus{{Revision: "active0001", RevisionDigest: "sha256:other",
				Digest: "cafe"}}
		}, "records a card digest for status.activeRevision active0001"},
	} {
		st := exemptLock(nil)
		tc.flip(st)
		ok, why := exemptTransaction(st)
		if ok {
			t.Errorf("clause %s: still exempt", tc.clause)
		} else if !strings.Contains(why, tc.says) {
			t.Errorf("clause %s: why does not say %q: %s", tc.clause, tc.says, why)
		}
	}

	// Clause 4 reads by NAME, not by digest (A19): an entry for the active
	// revision's digest under another name, or an entry for its name with an
	// empty digest, records no card for it, and the Lock stays exempt.
	for name, cards := range map[string][]assaydv1alpha1.CardStatus{
		"the active digest under another name": {{Revision: "elsewhere1", RevisionDigest: "sha256:active",
			Digest: "cafe"}},
		"the active name with an empty digest": {{Revision: "active0001", RevisionDigest: "sha256:active"}},
	} {
		st := exemptLock(nil)
		st.Cards = cards
		if ok, why := exemptTransaction(st); !ok {
			t.Errorf("%s: not exempt, so clause 4 read something other than a digest by name: %s", name, why)
		}
	}
	if ok, _ := exemptTransaction(&assaydv1alpha1.AgentStatus{Auth: &assaydv1alpha1.AuthStatus{
		Transaction: &assaydv1alpha1.AuthTransaction{Kind: TxAdopt, Stage: StageRefused}}}); ok {
		t.Error("a refused Adopt is exempt; it is not a transaction at all")
	}
}

// exemptInput is a gate input in the exempt state, with the candidate's card
// recorded and blockedFor matching this Lock, and nothing else set.
func exemptInput(now time.Time) gateInput {
	deadline := metav1.NewTime(now.Add(time.Minute).Truncate(time.Second))
	st := exemptLock(&deadline)
	withCard(st)
	st.Eval = &assaydv1alpha1.EvalStatus{BlockedFor: &assaydv1alpha1.EvalBlockedFor{RevisionDigest: gDigest,
		SuiteDigest: gSuite, LockDeadline: deadline.DeepCopy()}}
	return gateInput{desired: gRev, desiredDigest: gDigest, suiteName: "smoke", suiteDigest: gSuite,
		suite: twoCaseSuite(), now: now, available: true, status: st}
}

// The gate branch's order in the exempt state (design 16 §1.1, "The gate
// branch's order"): after a Passed verdict, (4) a Failed verdict for the
// pair, then the pair in failed[]; (5) no card recorded; (6) the candidate not
// available; (7) an unmatched blockedFor, as not yet evaluated; (8) blocked;
// and (9) EvalRunning. A failed pair is answered at (4), as outside the exempt
// state, ahead of every row a clause-5 hold could reach. Each row clears the
// previous row's cause.
//
// Mutation: guard the failed rows' call at (4) with !exempt, as the order
// before A24.1 did — every row with a failed pair fails on it.
func TestTheExemptStateTakesItsOwnPrecedence(t *testing.T) {
	in := exemptInput(time.Now())
	other := metav1.NewTime(in.status.Auth.Transaction.Deadline.Add(time.Hour))
	in.status.Cards = nil
	in.status.Eval.RevisionDigest, in.status.Eval.SuiteDigest = gDigest, gSuite
	in.status.Eval.Verdict, in.status.Eval.Score = assaydv1alpha1.EvalVerdictFailed, "0/2"
	in.status.Eval.Failed = []assaydv1alpha1.EvalPair{{RevisionDigest: gDigest, SuiteDigest: gSuite}}
	in.status.Eval.Blocked = &assaydv1alpha1.EvalBlocked{Reason: ReasonEvalWaitingForAuth,
		Message: "a hold" + holdClaimsTail}
	in.status.Eval.BlockedFor.LockDeadline = &other
	in.available = false
	step := func(name, want string, clear func(*gateInput)) gateDecision {
		t.Helper()
		if clear != nil {
			clear(&in)
		}
		d := decideGate(in)
		if d.satisfied || d.reason != want {
			t.Errorf("%s: decideGate gives satisfied=%v reason %q, want %q (message %q)",
				name, d.satisfied, d.reason, want, d.message)
		}
		return d
	}
	step("a failed pair with no card, not available, under an unmatched blockedFor", ReasonEvalFailed, nil)
	step("the pair in failed[] with no card", ReasonEvalPairAlreadyFailed, func(in *gateInput) {
		in.status.Eval.Verdict = ""
	})
	step("no card for the candidate", ReasonEvalAwaitingCard, func(in *gateInput) { in.status.Eval.Failed = nil })
	step("not available, beside a hold for another Lock", ReasonEvalPaused, func(in *gateInput) {
		withCard(in.status)
	})
	in.status.Eval.BlockedFor.LockDeadline = in.status.Auth.Transaction.Deadline.DeepCopy()
	step("not available, beside a matched hold", ReasonEvalPaused, nil)
	in.status.Eval.BlockedFor.LockDeadline = &other
	d := step("blockedFor computed for another Lock", ReasonEvalWaitingForAuth, func(in *gateInput) {
		in.available = true
	})
	if !strings.Contains(d.message, "has not yet evaluated clause 5 for this Lock") ||
		!strings.Contains(d.message, "may claim cases") {
		t.Errorf("an unmatched blockedFor does not say clause 5 is not yet evaluated: %s", d.message)
	}
	d = step("blocked, matched", ReasonEvalWaitingForAuth, func(in *gateInput) {
		in.status.Eval.BlockedFor.LockDeadline = in.status.Auth.Transaction.Deadline.DeepCopy()
	})
	if !strings.Contains(d.message, "a hold"+holdClaimsTail) {
		t.Errorf("a matched hold is not carried with its own message: %s", d.message)
	}
	d = step("blocked, matched, the address underivable", ReasonEvalCandidateUnaddressable, func(in *gateInput) {
		in.status.Eval.Blocked = &assaydv1alpha1.EvalBlocked{Reason: ReasonEvalCandidateUnaddressable,
			Message: "no address"}
	})
	if !strings.Contains(d.message, "that no case can be sent: no address") {
		t.Errorf("a matched fourth reason is not carried with its own message: %s", d.message)
	}
	step("no verdict, the candidate available", ReasonEvalRunning, func(in *gateInput) { in.status.Eval.Blocked = nil })

	// A failed pair is answered at (4) beside each row below it: an
	// unavailable candidate, an unmatched blockedFor, and a matched hold.
	for name, edit := range map[string]func(*gateInput){
		"not available":                  func(in *gateInput) { in.available = false },
		"beside an unmatched blockedFor": func(in *gateInput) { in.status.Eval.BlockedFor.LockDeadline = &other },
		"beside a matched hold":          func(in *gateInput) {},
		"beside a matched run-start refusal": func(in *gateInput) {
			in.status.Eval.Blocked = &assaydv1alpha1.EvalBlocked{Reason: ReasonEvalBindingUnsupported,
				Message: "no usable interface"}
		},
	} {
		for want, record := range map[string]func(*assaydv1alpha1.EvalStatus){
			ReasonEvalFailed: func(e *assaydv1alpha1.EvalStatus) {
				e.RevisionDigest, e.SuiteDigest, e.Verdict = gDigest, gSuite, assaydv1alpha1.EvalVerdictFailed
			},
			ReasonEvalPairAlreadyFailed: func(e *assaydv1alpha1.EvalStatus) {
				e.Failed = []assaydv1alpha1.EvalPair{{RevisionDigest: gDigest, SuiteDigest: gSuite}}
			},
		} {
			in := exemptInput(time.Now())
			in.status.Eval.Blocked = &assaydv1alpha1.EvalBlocked{Reason: ReasonEvalWaitingForAuth,
				Message: "a hold" + holdClaimsTail}
			record(in.status.Eval)
			edit(&in)
			if d := decideGate(in); d.satisfied || d.reason != want {
				t.Errorf("a failed pair %s reads %q, want %q: %s", name, d.reason, want, d.message)
			}
		}
	}

	// A Passed verdict outranks the exempt rows, an unmatched blockedFor and a
	// standing hold included: clause 5 stops new claims only.
	in.status.Eval.Verdict = assaydv1alpha1.EvalVerdictPassed
	in.status.Eval.Blocked = &assaydv1alpha1.EvalBlocked{Reason: ReasonEvalWaitingForAuth, Message: "a hold"}
	in.status.Eval.BlockedFor = nil
	if d := decideGate(in); !d.satisfied {
		t.Errorf("a Passed verdict beside an exempt Lock under a hold did not satisfy the gate: %s %s",
			d.reason, d.message)
	}
}

// Outside the exempt state, a recorded Lock answers before blocked; once the
// Lock is exempt, the same status reads blocked. And a refusal written while
// an exempt Lock stood stops applying the moment it ends.
func TestBlockedAppliesInTheExemptStateAndNotBeyondIt(t *testing.T) {
	in := exemptInput(time.Now())
	in.status.Eval.Blocked = &assaydv1alpha1.EvalBlocked{Reason: ReasonEvalBindingUnsupported,
		Message: "no usable interface"}
	if d := decideGate(in); d.reason != ReasonEvalBindingUnsupported {
		t.Fatalf("an exempt Lock's matched refusal reads %q, want EvalBindingUnsupported", d.reason)
	}
	in.status.Auth.Transaction.Stage = StageConverging
	if d := decideGate(in); d.reason != ReasonEvalWaitingForAuth ||
		!strings.Contains(d.message, "in stage "+StageConverging) {
		t.Errorf("a Lock that is not exempt does not answer before blocked: %s %s", d.reason, d.message)
	}
	in.status.Auth = nil
	if d := decideGate(in); d.reason != ReasonEvalRunning {
		t.Errorf("a refusal written under a Lock that has ended still applies: %s %s", d.reason, d.message)
	}
}

// blockedFor's third part is the recorded Lock's deadline truncated to the
// second, as the API server stores it, so a deadline held in memory at a finer
// grain reads as the same Lock and not as another one.
func TestBlockedForTruncatesTheLockDeadlineToTheSecond(t *testing.T) {
	stored := metav1.NewTime(time.Date(2026, 10, 19, 12, 0, 7, 0, time.UTC))
	fine := metav1.NewTime(stored.Add(400 * time.Millisecond))
	st := exemptLock(&fine)
	got := recordedLockDeadline(st)
	if got == nil || !got.Equal(&stored) {
		t.Fatalf("recordedLockDeadline = %v, want %v, truncated to the second", got, stored)
	}
	bf := &assaydv1alpha1.EvalBlockedFor{RevisionDigest: gDigest, SuiteDigest: gSuite, LockDeadline: &stored}
	if !blockedForMatches(bf, gDigest, gSuite, st) {
		t.Error("a blockedFor written at the second does not match the same Lock held at a finer grain")
	}
	next := metav1.NewTime(stored.Add(time.Second))
	st.Auth.Transaction.Deadline = &next
	if blockedForMatches(bf, gDigest, gSuite, st) {
		t.Error("a Lock entered a second later matches the blockedFor computed for the earlier one")
	}
}

// Clause 5's verdict: nil when nothing stands, and otherwise EvalWaitingForAuth
// naming every cause. The message is built from classes and names, never an
// API error's raw text, so two passes that fail differently-worded reads of
// the same class compute the same value.
func TestClauseFivesHoldNamesItsCausesAndIsStable(t *testing.T) {
	gw := GatewayConfig{Enabled: true, Name: "assayd", Namespace: "gw"}
	if h := clause5Hold(gw, "assayd-run-team", "gated-serving", gatewayAuth{}, nil, nil); h != nil {
		t.Fatalf("no cause, and a hold: %+v", h)
	}
	gr := schema.GroupResource{Group: "agentgateway.dev", Resource: "agentgatewaypolicies"}
	for name, tc := range map[string]struct {
		above   gatewayAuth
		foreign []string
		ferr    error
		says    []string
		not     []string
	}{
		"a Gateway-level policy": {above: gatewayAuth{policies: []string{"gw/allow-all"}},
			says: []string{"gw/allow-all", "Gateway gw/assayd on a listener that can take"},
			not:  []string{"could not be read"}},
		"a Gateway-level policy beside an unread Gateway": {above: gatewayAuth{policies: []string{"gw/allow-all"},
			unreadable: "x", gatewayErr: apierrors.NewForbidden(schema.GroupResource{Group: gatewayv1.GroupName,
				Resource: "gateways"}, "assayd", errors.New("no"))},
			says: []string{"gw/allow-all", "whose listeners could not be read", "(Forbidden)"},
			not:  []string{"on a listener that can take"}},
		"ListenerSets admitted": {above: gatewayAuth{listeners: "All"}, says: []string{"admits ListenerSets", "All"}},
		"an unreadable Gateway": {above: gatewayAuth{unreadable: "x", gatewayErr: apierrors.NewNotFound(
			schema.GroupResource{Group: gatewayv1.GroupName, Resource: "gateways"}, "assayd")},
			says: []string{"Gateway gw/assayd could not be read (NotFound)"}},
		"an unlisted Gateway namespace": {above: gatewayAuth{unreadable: "x", unlisted: true,
			listErr: apierrors.NewForbidden(gr, "", errors.New("no"))},
			says: []string{"AgentgatewayPolicies in gw could not be listed (Forbidden)"}},
		"a foreign traffic policy": {foreign: []string{"assayd-run-team/intruder"},
			says: []string{"assayd-run-team/intruder", "assayd-run-team/gated-serving"}},
		"a failed foreign list": {ferr: apierrors.NewInternalError(errors.New("etcd is sad")),
			says: []string{"could not be listed to find a foreign traffic policy", "(a server error)"}},
	} {
		h := clause5Hold(gw, "assayd-run-team", "gated-serving", tc.above, tc.foreign, tc.ferr)
		if h == nil || h.Reason != ReasonEvalWaitingForAuth {
			t.Errorf("%s: no EvalWaitingForAuth hold: %+v", name, h)
			continue
		}
		for _, want := range tc.says {
			if !strings.Contains(h.Message, want) {
				t.Errorf("%s: the hold does not name %q: %s", name, want, h.Message)
			}
		}
		for _, not := range tc.not {
			if strings.Contains(h.Message, not) {
				t.Errorf("%s: the hold says %q, which this read did not find: %s", name, not, h.Message)
			}
		}
	}
	a := clause5Hold(gw, "ns", "r", gatewayAuth{}, nil, fmt.Errorf("list: %w",
		apierrors.NewForbidden(gr, "", errors.New("user alice cannot list"))))
	b := clause5Hold(gw, "ns", "r", gatewayAuth{}, nil, fmt.Errorf("list: %w",
		apierrors.NewForbidden(gr, "", errors.New("user bob cannot list"))))
	if a == nil || b == nil || *a != *b {
		t.Errorf("two Forbidden lists worded differently compute different holds:\n%+v\n%+v", a, b)
	}
	if strings.Contains(a.Message, "alice") {
		t.Errorf("the hold quotes an API error's raw text: %s", a.Message)
	}
}

func TestReadErrorClassNamesEachClass(t *testing.T) {
	gr := schema.GroupResource{Group: "g", Resource: "r"}
	for want, err := range map[string]error{
		"NotFound":                  apierrors.NewNotFound(gr, "x"),
		"Forbidden":                 fmt.Errorf("wrapped: %w", apierrors.NewForbidden(gr, "x", errors.New("no"))),
		"Unauthorized":              apierrors.NewUnauthorized("no"),
		"the kind is not served":    &meta.NoKindMatchError{GroupKind: schema.GroupKind{Group: "g", Kind: "K"}},
		"a timeout":                 apierrors.NewTimeoutError("slow", 1),
		"throttled":                 apierrors.NewTooManyRequests("slow down", 1),
		"a server error":            apierrors.NewServiceUnavailable("down"),
		"an error of another class": errors.New("connection reset"),
	} {
		if got := readErrorClass(err); got != want {
			t.Errorf("readErrorClass(%v) = %q, want %q", err, got, want)
		}
	}
}

// clause5Fixture is newEvalFixture's Agent recorded under an exempt Lock, on a
// gateway-enabled eval controller whose Gateway and policy reads are answered
// by reads: the Gateway get, the Gateway namespace's list and the run
// namespace's list. The fake client serves neither kind, so every one of them
// is intercepted.
func clause5Fixture(t *testing.T, reads *clause5Reads, card string) *evalFixture {
	t.Helper()
	funcs := &interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object,
			opts ...client.GetOption) error {
			switch o := obj.(type) {
			case *gatewayv1.Gateway:
				reads.gatewayGets++
				if reads.gatewayErr != nil {
					return reads.gatewayErr
				}
				o.Name, o.Namespace = key.Name, key.Namespace
				o.Spec.Listeners = []gatewayv1.Listener{{Name: GatewayListenerName, Port: 8080}}
				if reads.listenerSets {
					from := gatewayv1.NamespacesFromAll
					o.Spec.AllowedListeners = &gatewayv1.AllowedListeners{
						Namespaces: &gatewayv1.ListenerNamespaces{From: &from}}
				}
				return nil
			case *gatewayv1.HTTPRoute:
				return apierrors.NewNotFound(schema.GroupResource{Group: gatewayv1.GroupName,
					Resource: "httproutes"}, key.Name)
			}
			return c.Get(ctx, key, obj, opts...)
		},
		List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			u, ok := list.(*unstructured.UnstructuredList)
			if !ok {
				return c.List(ctx, list, opts...)
			}
			reads.policyLists++
			lo := &client.ListOptions{}
			lo.ApplyOptions(opts)
			if lo.Namespace == "gw" {
				return reads.gatewayListErr
			}
			if reads.runListErr != nil {
				return reads.runListErr
			}
			u.Items = reads.runPolicies
			return nil
		},
	}
	f := newEvalFixture(t, funcs, echoCase("a", "one"), echoCase("b", "two"))
	f.r.Gateway = GatewayConfig{Enabled: true, Name: "assayd", Namespace: "gw",
		HostnameSuffix: DefaultGatewayHostnameSuffix}
	a := f.agent(t)
	deadline := metav1.NewTime(time.Now().Add(time.Minute).Truncate(time.Second))
	a.Status.Auth = exemptLock(&deadline).Auth
	if card != "" {
		f.stub.card = card
		a.Status.Cards[0].Digest = digestOfCard(card)
	}
	if err := f.c.Status().Update(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	return f
}

type clause5Reads struct {
	gatewayErr, gatewayListErr, runListErr error
	listenerSets                           bool
	runPolicies                            []unstructured.Unstructured
	// gatewayGets and policyLists count clause 5's reads.
	gatewayGets, policyLists int
}

// Beside an exempt Lock, a pass whose pair has a final verdict, Passed or
// Failed, or is in failed[], makes no clause-5 read: it writes blocked empty,
// with blockedFor for the pair and the Lock, clearing a stored hold, and
// returns CardDriftInterval, with no error even though every clause-5 read
// would fail (design 16 §1.1, "What wakes the eval controller while it
// waits").
//
// Mutations: read clause 5 before the verdict cases (the read counts and the
// error); drop the verdict cases' blocked write (the stored hold stands).
func TestAVerdictPassBesideAnExemptLockReadsNoClauseFive(t *testing.T) {
	for name, record := range map[string]func(*assaydv1alpha1.EvalStatus){
		"a Passed verdict": func(e *assaydv1alpha1.EvalStatus) { e.Verdict = assaydv1alpha1.EvalVerdictPassed },
		"a Failed verdict": func(e *assaydv1alpha1.EvalStatus) { e.Verdict = assaydv1alpha1.EvalVerdictFailed },
		"a pair in failed[]": func(e *assaydv1alpha1.EvalStatus) {
			e.Failed = []assaydv1alpha1.EvalPair{{RevisionDigest: e.RevisionDigest, SuiteDigest: e.SuiteDigest}}
			e.RevisionDigest, e.SuiteDigest = "", ""
		},
	} {
		t.Run(name, func(t *testing.T) {
			reads := &clause5Reads{listenerSets: true, gatewayErr: apierrors.NewForbidden(schema.GroupResource{
				Group: gatewayv1.GroupName, Resource: "gateways"}, "assayd", errors.New("no"))}
			f := clause5Fixture(t, reads, "")
			a := f.agent(t)
			deadline := a.Status.Auth.Transaction.Deadline
			// The stored hold was computed for another Lock, so the pass must
			// rewrite blockedFor as well as clear blocked.
			another := metav1.NewTime(deadline.Add(time.Hour))
			a.Status.Eval = &assaydv1alpha1.EvalStatus{RevisionDigest: "sha256:candidate", SuiteDigest: f.sd,
				Blocked: &assaydv1alpha1.EvalBlocked{Reason: ReasonEvalWaitingForAuth, Message: "a hold" + holdClaimsTail},
				BlockedFor: &assaydv1alpha1.EvalBlockedFor{RevisionDigest: "sha256:candidate", SuiteDigest: f.sd,
					LockDeadline: &another}}
			record(a.Status.Eval)
			if err := f.c.Status().Update(context.Background(), a); err != nil {
				t.Fatal(err)
			}
			res, err := f.pass(t)
			if err != nil || res.RequeueAfter != CardDriftInterval {
				t.Errorf("the pass returned %+v, %v; want CardDriftInterval and no error", res, err)
			}
			if reads.gatewayGets != 0 || reads.policyLists != 0 {
				t.Errorf("the pass read clause 5: %d Gateway gets, %d policy lists", reads.gatewayGets,
					reads.policyLists)
			}
			e := f.agent(t).Status.Eval
			if e.Blocked != nil {
				t.Errorf("the stored hold was not cleared: %+v", e.Blocked)
			}
			if bf := e.BlockedFor; bf == nil || bf.RevisionDigest != "sha256:candidate" || bf.SuiteDigest != f.sd ||
				bf.LockDeadline == nil || !bf.LockDeadline.Equal(deadline) {
				t.Errorf("blockedFor does not name the pair and the Lock: %+v", bf)
			}
			if f.stub.total() != 0 || f.stub.cardGets != 0 {
				t.Errorf("the pass sent %d cases and fetched the card %d times", f.stub.total(), f.stub.cardGets)
			}
		})
	}
}

// Through Reconcile: an exempt Lock with no hold is claimed beside, and the
// run-start record's blockedFor names the Lock; each hold, and each read that
// fails, claims nothing and records EvalWaitingForAuth. A NotFound from the
// run namespace's list reads as no foreign policies and holds nothing, as
// design 03 reads it; any other error there holds; and a NotFound of the
// Gateway holds (§1.1, "Clause 5's reads").
func TestClauseFiveHoldsAndReleasesThroughReconcile(t *testing.T) {
	notFound := apierrors.NewNotFound(schema.GroupResource{Group: "agentgateway.dev",
		Resource: "agentgatewaypolicies"}, "")
	t.Run("no hold, and a NotFound run-namespace list", func(t *testing.T) {
		f := clause5Fixture(t, &clause5Reads{runListErr: notFound}, "")
		res, err := f.pass(t)
		if err != nil {
			t.Fatal(err)
		}
		if f.stub.total() != 1 || res.RequeueAfter != evalSendBackstop {
			t.Fatalf("an exempt Lock with no hold: %d sends, %+v; want the first case claimed and sent",
				f.stub.total(), res)
		}
		a := f.agent(t)
		if bf := a.Status.Eval.BlockedFor; bf == nil || bf.LockDeadline == nil ||
			!bf.LockDeadline.Equal(a.Status.Auth.Transaction.Deadline) || a.Status.Eval.Blocked != nil {
			t.Errorf("the run start did not record blockedFor for this Lock with no hold: %+v", a.Status.Eval)
		}
	})
	for name, tc := range map[string]struct {
		reads   clause5Reads
		failed  bool
		message string
	}{
		"ListenerSets admitted": {reads: clause5Reads{listenerSets: true}, message: "admits ListenerSets"},
		"a Gateway that does not exist": {reads: clause5Reads{gatewayErr: apierrors.NewNotFound(
			schema.GroupResource{Group: gatewayv1.GroupName, Resource: "gateways"}, "assayd")},
			failed: true, message: "could not be read (NotFound)"},
		"a Gateway-namespace list that fails": {reads: clause5Reads{gatewayListErr: notFound}, failed: true,
			message: "AgentgatewayPolicies in gw could not be listed (NotFound)"},
		"a run-namespace list Forbidden": {reads: clause5Reads{runListErr: apierrors.NewForbidden(
			schema.GroupResource{Group: "agentgateway.dev", Resource: "agentgatewaypolicies"}, "",
			errors.New("no"))}, failed: true, message: "foreign traffic policy"},
		"a foreign traffic policy": {reads: clause5Reads{runPolicies: []unstructured.Unstructured{{Object: map[string]any{
			"apiVersion": "agentgateway.dev/v1alpha1", "kind": "AgentgatewayPolicy",
			"metadata": map[string]any{"name": "intruder", "namespace": RunNamespaceName("team")},
			"spec": map[string]any{"targetRefs": []any{map[string]any{"group": gatewayv1.GroupName,
				"kind": "HTTPRoute", "name": "gated-serving"}}, "traffic": map[string]any{}}}}}},
			message: "intruder"},
	} {
		t.Run(name, func(t *testing.T) {
			f := clause5Fixture(t, &tc.reads, "")
			res, err := f.pass(t)
			if (err != nil) != tc.failed {
				t.Fatalf("pass error %v, want one: %v", err, tc.failed)
			}
			if !tc.failed && res.RequeueAfter != AuthProbeInterval {
				t.Errorf("a pass under clause 5's hold requeues at %v, want AuthProbeInterval", res.RequeueAfter)
			}
			if f.stub.total() != 0 || f.stub.cardGets != 0 {
				t.Errorf("a pass under clause 5's hold sent %d cases and fetched the card %d times",
					f.stub.total(), f.stub.cardGets)
			}
			e := f.agent(t).Status.Eval
			if e == nil || e.Blocked == nil || e.Blocked.Reason != ReasonEvalWaitingForAuth ||
				!strings.Contains(e.Blocked.Message, tc.message) {
				t.Fatalf("blocked is %+v, want EvalWaitingForAuth naming %q", e, tc.message)
			}
			if e.BlockedFor == nil || e.BlockedFor.LockDeadline == nil {
				t.Errorf("blockedFor does not name the Lock the hold was evaluated for: %+v", e.BlockedFor)
			}
			// The same hold on the next pass writes nothing.
			rv := f.agent(t).ResourceVersion
			_, _ = f.pass(t)
			if got := f.agent(t).ResourceVersion; got != rv {
				t.Errorf("a standing hold was rewritten (resourceVersion %s → %s)", rv, got)
			}
		})
	}
}

// A run-start refusal outranks clause 5's hold only on a pass that would start
// a run, and a pass under a hold starts none: with a card that cannot be sent
// a case, the hold is recorded and the card is not fetched; once the hold
// clears, the refusal is, and it stands without a rewrite.
func TestARunStartRefusalAndClauseFiveShareOneSlot(t *testing.T) {
	bad := strings.Replace(evalCard, "HTTP+JSON", "JSONRPC", 1)
	f := clause5Fixture(t, &clause5Reads{listenerSets: true}, bad)
	if _, err := f.pass(t); err != nil {
		t.Fatal(err)
	}
	if e := f.agent(t).Status.Eval; e == nil || e.Blocked == nil || e.Blocked.Reason != ReasonEvalWaitingForAuth ||
		f.stub.cardGets != 0 {
		t.Fatalf("under a hold: blocked %+v after %d card fetches; want the hold, and no fetch", e, f.stub.cardGets)
	}
	g := clause5Fixture(t, &clause5Reads{}, bad)
	if _, err := g.pass(t); err != nil {
		t.Fatal(err)
	}
	e := g.agent(t).Status.Eval
	if e == nil || e.Blocked == nil || e.Blocked.Reason != ReasonEvalBindingUnsupported {
		t.Fatalf("with no hold, the refusal is not recorded: %+v", e)
	}
	rv := g.agent(t).ResourceVersion
	for i := 0; i < 3; i++ {
		if _, err := g.pass(t); err != nil {
			t.Fatal(err)
		}
	}
	if got := g.agent(t).ResourceVersion; got != rv {
		t.Errorf("a standing refusal beside an exempt Lock was rewritten (resourceVersion %s → %s)", rv, got)
	}
	if g.stub.total() != 0 {
		t.Errorf("a refused run sent %d cases", g.stub.total())
	}
}

// A Lock that is not exempt holds the pass before anything is read or
// written: no clause 5 read, no blocked write.
func TestALockThatIsNotExemptHoldsThePassBeforeAnyRead(t *testing.T) {
	f := clause5Fixture(t, &clause5Reads{listenerSets: true}, "")
	a := f.agent(t)
	a.Status.Auth.Transaction.BeforeObserved = true
	if err := f.c.Status().Update(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	rv := f.agent(t).ResourceVersion
	res, err := f.pass(t)
	if err != nil || res.RequeueAfter != AuthProbeInterval {
		t.Fatalf("a Lock that is not exempt: %+v %v, want AuthProbeInterval", res, err)
	}
	if got := f.agent(t).ResourceVersion; got != rv || f.stub.total() != 0 || f.stub.cardGets != 0 {
		t.Errorf("a pass beside a Lock that is not exempt wrote or sent something: rv %s → %s, %d sends, "+
			"%d card fetches", rv, got, f.stub.total(), f.stub.cardGets)
	}
}

// The missing-policy wedge's exits (design 16 §1.1, "When it names the
// exits"): named only when all three clauses hold, with R's name taken from
// the route's single backendRef; a route with no single backendRef names no
// revision and offers only the administrator's exit.
func TestTheWedgeNamesItsExitsOnlyWhenNothingCanAttribute(t *testing.T) {
	agent := &assaydv1alpha1.Agent{ObjectMeta: metav1.ObjectMeta{Name: "gated", Namespace: "team"}}
	route := func(backends ...string) *gatewayv1.HTTPRoute {
		rt := &gatewayv1.HTTPRoute{}
		rule := gatewayv1.HTTPRouteRule{}
		for _, b := range backends {
			rule.BackendRefs = append(rule.BackendRefs, gatewayv1.HTTPBackendRef{BackendRef: gatewayv1.BackendRef{
				BackendObjectReference: gatewayv1.BackendObjectReference{Name: gatewayv1.ObjectName(b)}}})
		}
		rt.Spec.Rules = []gatewayv1.HTTPRouteRule{rule}
		return rt
	}
	wedge := func() gateInput {
		past := metav1.NewTime(time.Now().Add(time.Minute))
		st := &assaydv1alpha1.AgentStatus{ActiveRevision: "rrrrrrrrrr", ActiveRevisionDigest: "sha256:r",
			CandidateRevision: gRev, CandidateRevisionDigest: gDigest,
			Auth: &assaydv1alpha1.AuthStatus{Mode: "apikey", Transaction: &assaydv1alpha1.AuthTransaction{
				Kind: TxLock, TargetMode: "apikey", Stage: StageProbingAfter, Written: true, Deadline: &past}}}
		withCard(st)
		return gateInput{desired: gRev, desiredDigest: gDigest, suiteName: "smoke", suiteDigest: gSuite,
			suite: twoCaseSuite(), now: time.Now(), available: true, status: st, agent: agent,
			runNS: "assayd-run-team", route: route("gated-rrrrrrrrrr")}
	}
	d := decideGate(wedge())
	if d.reason != ReasonEvalWaitingForAuth {
		t.Fatalf("the wedge reads %q", d.reason)
	}
	for _, want := range []string{"names revision rrrrrrrrrr", "TERMINAL", "removes assayd-run-team/gated-serving",
		"reverting the spec to it", "status.activeRevision's own card recording would end it",
		"administrator's of the run namespace"} {
		if !strings.Contains(d.message, want) {
			t.Errorf("the wedge's message does not carry %q: %s", want, d.message)
		}
	}

	in := wedge()
	in.route = route("gated-rrrrrrrrrr", "gated-ssssssssss")
	if d := decideGate(in); !strings.Contains(d.message, "does not carry exactly one backendRef") ||
		!strings.Contains(d.message, "only the first applies") {
		t.Errorf("a route with two backendRefs: %s", d.message)
	}
	for name, edit := range map[string]func(*gateInput){
		"the route's revision carded": func(in *gateInput) {
			in.status.Cards = append(in.status.Cards, assaydv1alpha1.CardStatus{Revision: "rrrrrrrrrr", Digest: "c"})
		},
		"status.activeRevision carded": func(in *gateInput) {
			in.status.ActiveRevision = "aaaaaaaaaa"
			in.status.Cards = append(in.status.Cards, assaydv1alpha1.CardStatus{Revision: "aaaaaaaaaa", Digest: "c"})
		},
		"a stage short of ProbingAfter": func(in *gateInput) { in.status.Auth.Transaction.Stage = StageConverging },
		"a J2 Lock, not a re-creation":  func(in *gateInput) { in.status.Auth.Mode = "none" },
		"no route read":                 func(in *gateInput) { in.route = nil },
	} {
		in := wedge()
		edit(&in)
		if d := decideGate(in); strings.Contains(d.message, "TERMINAL") || strings.Contains(d.message, "exits") {
			t.Errorf("%s: the exits are named: %s", name, d.message)
		}
	}
}

// EvalPaused's message beside a matched clause-5 hold recorded before the
// candidate went away: the run resumes once the candidate is available AND
// the hold has cleared, which the eval controller re-reads then. With no
// hold, it says only the first.
func TestEvalPausedSaysAHoldMustClearToo(t *testing.T) {
	in := exemptInput(time.Now())
	in.available = false
	in.status.Eval.RevisionDigest, in.status.Eval.SuiteDigest = gDigest, gSuite
	in.status.Eval.Verdict = assaydv1alpha1.EvalVerdictRunning
	in.status.Eval.Blocked = &assaydv1alpha1.EvalBlocked{Reason: ReasonEvalWaitingForAuth, Message: "a hold"}
	d := decideGate(in)
	if d.reason != ReasonEvalPaused || !strings.Contains(d.message, "once it is available again and the "+
		"clause-5 hold") {
		t.Errorf("beside a matched hold, EvalPaused does not say the hold must clear too: %s %s", d.reason, d.message)
	}
	in.status.Eval.Blocked = nil
	if d := decideGate(in); d.reason != ReasonEvalPaused || strings.Contains(d.message, "clause-5 hold") ||
		!strings.Contains(d.message, "once it is available again") {
		t.Errorf("with no hold, EvalPaused names one: %s", d.message)
	}
}

// EvalPaused beside a matched fourth reason, in both arms, says a run starts
// or resumes only once the candidate's address can be derived, and names the
// fault, as it names a matched hold or run-start refusal: otherwise "one
// starts once it is available again" is a promise the standing reason denies
// (design 16 A24.6).
//
// Mutation: drop the case from noVerdictRows's switch.
func TestEvalPausedNamesAnUnaddressableCandidate(t *testing.T) {
	for _, arm := range []string{"exempt", "outside the exempt state"} {
		for _, running := range []bool{false, true} {
			in := exemptInput(time.Now())
			if arm != "exempt" {
				in.status.Auth = nil
				in.status.Eval.BlockedFor.LockDeadline = nil
			}
			if running {
				in.status.Eval.RevisionDigest, in.status.Eval.SuiteDigest = gDigest, gSuite
				in.status.Eval.Verdict = assaydv1alpha1.EvalVerdictRunning
			}
			in.available = false
			in.status.Eval.Blocked = &assaydv1alpha1.EvalBlocked{Reason: ReasonEvalCandidateUnaddressable,
				Message: "the candidate's Service was not found"}
			d := decideGate(in)
			if d.reason != ReasonEvalPaused || !strings.Contains(d.message, "only once it is available again "+
				"and its address can be derived") || !strings.Contains(d.message, "the candidate's Service was not found") {
				t.Errorf("%s, running %v: EvalPaused beside the fourth reason: %s %s", arm, running, d.reason, d.message)
			}
		}
	}
}

// Clause 5's hold outranks the fourth reason (design 16 A24.17): a pass that
// clause 5 holds neither starts a run nor claims a case, so it derives no
// address, and a candidate whose Service is gone reads the hold, not
// EvalCandidateUnaddressable, until the hold clears.
//
// Mutation: derive the address on a held pass and write its fault in place
// of the hold.
func TestAHoldOutranksAnUnaddressableCandidate(t *testing.T) {
	reads := &clause5Reads{listenerSets: true}
	f := clause5Fixture(t, reads, "")
	breakCandidateService(t, f, "missing")
	if _, err := f.pass(t); err != nil {
		t.Fatalf("a held pass returned %v; it derives no address", err)
	}
	if e := f.agent(t).Status.Eval; e == nil || e.Blocked == nil || e.Blocked.Reason != ReasonEvalWaitingForAuth {
		t.Fatalf("blocked is %+v, want clause 5's hold", e)
	}
	reads.listenerSets = false
	if _, err := f.pass(t); err == nil {
		t.Error("once the hold cleared, a pass to a missing Service returned no error")
	}
	if e := f.agent(t).Status.Eval; e.Blocked == nil || e.Blocked.Reason != ReasonEvalCandidateUnaddressable {
		t.Errorf("once the hold cleared, blocked is %+v, want %s", e.Blocked, ReasonEvalCandidateUnaddressable)
	}
}

// A stale hold is replaced on a run-start pass that clause 5 found clear and
// that then stops before the card fetch because the candidate's address
// cannot be derived: that pass's one blocked write is the fourth reason, with
// blockedFor for the pair and the Lock, so the hold does not go on naming a
// cause that is gone (design 16 A24.4).
func TestAHoldFoundGoneIsReplacedWhenARunStartStopsShort(t *testing.T) {
	reads := &clause5Reads{listenerSets: true}
	f := clause5Fixture(t, reads, "")
	if _, err := f.pass(t); err != nil {
		t.Fatal(err)
	}
	if e := f.agent(t).Status.Eval; e == nil || e.Blocked == nil {
		t.Fatalf("setup: no hold recorded: %+v", e)
	}
	reads.listenerSets = false
	breakCandidateService(t, f, "headless")
	if _, err := f.pass(t); err == nil {
		t.Fatal("setup: a pass to an unusable address returned no error")
	}
	a := f.agent(t)
	e := a.Status.Eval
	if e.Blocked == nil || e.Blocked.Reason != ReasonEvalCandidateUnaddressable {
		t.Errorf("a hold clause 5 found gone was not replaced by the fourth reason: %+v", e.Blocked)
	}
	if e.BlockedFor == nil || e.BlockedFor.LockDeadline == nil ||
		!e.BlockedFor.LockDeadline.Equal(a.Status.Auth.Transaction.Deadline) {
		t.Errorf("blockedFor %+v does not name the Lock", e.BlockedFor)
	}
	if f.stub.total() != 0 || f.stub.cardGets != 0 {
		t.Errorf("a pass to an unusable address sent or fetched something")
	}
}

// A pass makes at most one blocked write (§1.1, "computes it once per
// pass"): a hold whose cause is gone, on a run-start pass whose card fetch is
// refused, is replaced by the refusal in one write; a run-start pass that
// stops at an unusable address replaces it with the fourth reason in one
// write; and so does a mid-run pass that would claim a case, with a
// non-empty blocked stored first, without which a write of empty before the
// fourth reason would store no change (design 16 A24.4).
//
// Mutations: write blocked empty before deriving the address mid-run (two
// writes, or the fourth reason never written); skip the fourth reason's
// write (the hold or the stale refusal stands).
func TestARunStartPassWritesBlockedOnce(t *testing.T) {
	for name, tc := range map[string]struct {
		stopShort func(*testing.T, *evalFixture)
		want      string
	}{
		"a refused run-start fetch": {func(_ *testing.T, f *evalFixture) { f.stub.refuse = true },
			ReasonEvalAwaitingCard},
		"an unusable address": {func(t *testing.T, f *evalFixture) { breakCandidateService(t, f, "headless") },
			ReasonEvalCandidateUnaddressable},
	} {
		t.Run(name, func(t *testing.T) {
			reads := &clause5Reads{listenerSets: true}
			f := clause5Fixture(t, reads, "")
			if _, err := f.pass(t); err != nil {
				t.Fatal(err)
			}
			if e := f.agent(t).Status.Eval; e == nil || e.Blocked == nil {
				t.Fatalf("setup: no hold recorded: %+v", e)
			}
			reads.listenerSets = false
			tc.stopShort(t, f)
			before, err := strconv.Atoi(f.agent(t).ResourceVersion)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = f.pass(t)
			after, _ := strconv.Atoi(f.agent(t).ResourceVersion)
			if after-before != 1 {
				t.Errorf("the pass moved the resourceVersion %d → %d; it makes one blocked write", before, after)
			}
			if e := f.agent(t).Status.Eval; e.Blocked == nil || e.Blocked.Reason != tc.want {
				t.Errorf("blocked is %+v, want %s", e.Blocked, tc.want)
			}
		})
	}
	t.Run("a mid-run pass that would claim a case", func(t *testing.T) {
		f := newEvalFixture(t, nil, echoCase("a", "one"), echoCase("b", "two"))
		if _, err := f.pass(t); err != nil {
			t.Fatal(err)
		}
		a := f.agent(t)
		if a.Status.Eval == nil || a.Status.Eval.Verdict != assaydv1alpha1.EvalVerdictRunning {
			t.Fatalf("setup: no run in progress: %+v", a.Status.Eval)
		}
		a.Status.Eval.Blocked = &assaydv1alpha1.EvalBlocked{Reason: ReasonEvalAwaitingCard, Message: "stale"}
		if err := f.c.Status().Update(context.Background(), a); err != nil {
			t.Fatal(err)
		}
		breakCandidateService(t, f, "headless")
		before, err := strconv.Atoi(f.agent(t).ResourceVersion)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = f.pass(t)
		after, _ := strconv.Atoi(f.agent(t).ResourceVersion)
		if after-before != 1 {
			t.Errorf("the pass moved the resourceVersion %d → %d; it makes one blocked write", before, after)
		}
		if e := f.agent(t).Status.Eval; e.Blocked == nil || e.Blocked.Reason != ReasonEvalCandidateUnaddressable {
			t.Errorf("blocked is %+v, want %s", e.Blocked, ReasonEvalCandidateUnaddressable)
		}
	})
}

// EvalPaused beside a matched run-start refusal, in both arms, does not
// promise a run once the candidate is back: the card's answer would be the
// same. It names the refusal.
func TestEvalPausedBesideARefusalPromisesNoRun(t *testing.T) {
	for _, arm := range []string{"exempt", "outside the exempt state"} {
		in := exemptInput(time.Now())
		if arm != "exempt" {
			in.status.Auth = nil
			in.status.Eval.BlockedFor.LockDeadline = nil
		}
		in.available = false
		in.status.Eval.Blocked = &assaydv1alpha1.EvalBlocked{Reason: ReasonEvalBindingUnsupported,
			Message: "no HTTP+JSON 1.0 interface"}
		d := decideGate(in)
		if d.reason != ReasonEvalPaused || !strings.Contains(d.message, "only once it is available again and the "+
			"run-start refusal") || !strings.Contains(d.message, "no HTTP+JSON 1.0 interface") {
			t.Errorf("%s: EvalPaused beside a refusal: %s %s", arm, d.reason, d.message)
		}
	}
}

// EvalPaused names a stored hold as recorded for this Lock only while
// blockedFor matches it: a hold computed under another Lock's deadline is not
// claimed.
func TestEvalPausedDoesNotClaimAHoldForAnotherLock(t *testing.T) {
	in := exemptInput(time.Now())
	in.available = false
	other := metav1.NewTime(in.status.Auth.Transaction.Deadline.Add(time.Hour))
	in.status.Eval.BlockedFor.LockDeadline = &other
	in.status.Eval.Blocked = &assaydv1alpha1.EvalBlocked{Reason: ReasonEvalWaitingForAuth, Message: "a hold"}
	if d := decideGate(in); d.reason != ReasonEvalPaused || strings.Contains(d.message, "clause-5 hold") {
		t.Errorf("a hold computed for another Lock is named as this Lock's: %s %s", d.reason, d.message)
	}
}

// EvalPaused names a stored hold or refusal only while blockedFor names the
// current pair: one computed for another revision — R1's, before an edit made
// R2 the candidate — or another suite is not described as this one's.
func TestEvalPausedDoesNotClaimBlockedForAnotherPair(t *testing.T) {
	for _, blocked := range []*assaydv1alpha1.EvalBlocked{
		{Reason: ReasonEvalWaitingForAuth, Message: "a hold"},
		{Reason: ReasonEvalBindingUnsupported, Message: "a refusal"},
	} {
		for part, edit := range map[string]func(*assaydv1alpha1.EvalBlockedFor){
			"revision": func(bf *assaydv1alpha1.EvalBlockedFor) { bf.RevisionDigest = "sha256:r1" },
			"suite":    func(bf *assaydv1alpha1.EvalBlockedFor) { bf.SuiteDigest = "another" },
		} {
			in := exemptInput(time.Now())
			in.available = false
			in.status.Eval.Blocked = blocked
			edit(in.status.Eval.BlockedFor)
			d := decideGate(in)
			if d.reason != ReasonEvalPaused || strings.Contains(d.message, "clause-5 hold") ||
				strings.Contains(d.message, "run-start refusal") {
				t.Errorf("%s computed for another %s is named as this pair's: %s %s", blocked.Reason, part,
					d.reason, d.message)
			}
		}
	}
}
