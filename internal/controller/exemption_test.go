// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"errors"
	"fmt"
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

// The gate branch's order in the exempt state (design 16 §1.1, "What the gate
// branch derives in the exempt state"): a Passed verdict and the card come
// first; then an unmatched blockedFor, as not yet evaluated; then blocked; and
// only then the table's rows, EvalFailed and EvalPairAlreadyFailed among them.
// Each row clears the previous row's cause.
func TestTheExemptStateTakesItsOwnPrecedence(t *testing.T) {
	in := exemptInput(time.Now())
	other := metav1.NewTime(in.status.Auth.Transaction.Deadline.Add(time.Hour))
	in.status.Cards = nil
	in.status.Eval.RevisionDigest, in.status.Eval.SuiteDigest = gDigest, gSuite
	in.status.Eval.Verdict, in.status.Eval.Score = assaydv1alpha1.EvalVerdictFailed, "0/2"
	in.status.Eval.Failed = []assaydv1alpha1.EvalPair{{RevisionDigest: gDigest, SuiteDigest: gSuite}}
	in.status.Eval.Blocked = &assaydv1alpha1.EvalBlocked{Reason: ReasonEvalWaitingForAuth, Message: "a hold"}
	in.status.Eval.BlockedFor.LockDeadline = &other
	in.available = false
	step := func(name, want string, clear func(*gateInput)) gateDecision {
		t.Helper()
		if clear != nil {
			clear(&in)
		}
		d := decideGate(in)
		if d.satisfied || d.reason != want {
			t.Fatalf("%s: decideGate gives satisfied=%v reason %q, want %q (message %q)",
				name, d.satisfied, d.reason, want, d.message)
		}
		return d
	}
	step("no card for the candidate", ReasonEvalAwaitingCard, nil)
	d := step("blockedFor computed for another Lock", ReasonEvalWaitingForAuth, func(in *gateInput) {
		withCard(in.status)
	})
	if !strings.Contains(d.message, "has not yet evaluated clause 5 for this Lock") {
		t.Errorf("an unmatched blockedFor in the exempt state does not say clause 5 is unevaluated: %s",
			d.message)
	}
	d = step("blocked, matched", ReasonEvalWaitingForAuth, func(in *gateInput) {
		in.status.Eval.BlockedFor.LockDeadline = in.status.Auth.Transaction.Deadline.DeepCopy()
	})
	if !strings.Contains(d.message, "a hold") {
		t.Errorf("blocked's message is not carried: %s", d.message)
	}
	step("a Failed verdict for the pair", ReasonEvalFailed, func(in *gateInput) { in.status.Eval.Blocked = nil })
	step("the pair in failed[]", ReasonEvalPairAlreadyFailed, func(in *gateInput) { in.status.Eval.Verdict = "" })
	step("no verdict, the candidate not available", ReasonEvalPaused, func(in *gateInput) {
		in.status.Eval.Failed = nil
	})
	step("no verdict, the candidate available", ReasonEvalRunning, func(in *gateInput) { in.available = true })

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
	}{
		"a Gateway-level policy": {above: gatewayAuth{policies: []string{"gw/allow-all"}},
			says: []string{"gw/allow-all", "Gateway gw/assayd"}},
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
func clause5Fixture(t *testing.T, reads clause5Reads, card string) *evalFixture {
	t.Helper()
	funcs := &interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object,
			opts ...client.GetOption) error {
			switch o := obj.(type) {
			case *gatewayv1.Gateway:
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
		f := clause5Fixture(t, clause5Reads{runListErr: notFound}, "")
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
			f := clause5Fixture(t, tc.reads, "")
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
	f := clause5Fixture(t, clause5Reads{listenerSets: true}, bad)
	if _, err := f.pass(t); err != nil {
		t.Fatal(err)
	}
	if e := f.agent(t).Status.Eval; e == nil || e.Blocked == nil || e.Blocked.Reason != ReasonEvalWaitingForAuth ||
		f.stub.cardGets != 0 {
		t.Fatalf("under a hold: blocked %+v after %d card fetches; want the hold, and no fetch", e, f.stub.cardGets)
	}
	g := clause5Fixture(t, clause5Reads{}, bad)
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
	f := clause5Fixture(t, clause5Reads{listenerSets: true}, "")
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
