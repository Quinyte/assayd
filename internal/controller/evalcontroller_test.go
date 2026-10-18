// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/evalclient"
	"github.com/Quinyte/assayd/internal/evalsuite"
)

// candidateStub answers in the candidate's place: its card at the card path,
// and each SendMessage with "echo: <message>", completed. It counts the
// messages it was sent, by text.
type candidateStub struct {
	mu       sync.Mutex
	card     string
	hits     map[string]int
	sends    int
	cardGets int
	// refuse fails the card fetch at the connection.
	refuse bool
}

func newCandidateStub(card string) *candidateStub {
	return &candidateStub{card: card, hits: map[string]int{}}
}

func (s *candidateStub) Do(req *http.Request) (*http.Response, error) {
	answer := func(code int, body string) (*http.Response, error) {
		return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)),
			Header: http.Header{}, Request: req}, nil
	}
	if req.Method == http.MethodGet {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.cardGets++
		if s.refuse {
			return nil, errors.New("connection refused")
		}
		return answer(http.StatusOK, s.card)
	}
	var m struct {
		Message struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"message"`
	}
	b, _ := io.ReadAll(req.Body)
	if err := json.Unmarshal(b, &m); err != nil || len(m.Message.Parts) != 1 {
		return answer(http.StatusBadRequest, "")
	}
	text := m.Message.Parts[0].Text
	s.mu.Lock()
	s.hits[text]++
	s.sends++
	s.mu.Unlock()
	out, _ := json.Marshal(map[string]any{"task": map[string]any{"id": "t",
		"status":    map[string]any{"state": "TASK_STATE_COMPLETED"},
		"artifacts": []any{map[string]any{"artifactId": "a", "parts": []any{map[string]any{"text": "echo: " + text}}}}}})
	return answer(http.StatusOK, string(out))
}

func (s *candidateStub) count(text string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hits[text]
}

func (s *candidateStub) total() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sends
}

const evalCard = `{"name":"gated","supportedInterfaces":[{"url":"http://agent.example/a2a/",` +
	`"protocolBinding":"HTTP+JSON","protocolVersion":"1.0"}]}`

func digestOfCard(card string) string {
	sum := sha256.Sum256([]byte(card))
	return hex.EncodeToString(sum[:])
}

// evalFixture is a gated Agent whose candidate is available, carded, and
// observed at its generation, beside its suite, in a fake API server.
type evalFixture struct {
	r     *EvalReconciler
	c     client.Client
	stub  *candidateStub
	key   types.NamespacedName
	suite *assaydv1alpha1.EvalSuite
	sd    string
}

func newEvalFixture(t *testing.T, funcs *interceptor.Funcs, cases ...assaydv1alpha1.EvalCase) *evalFixture {
	t.Helper()
	const ns, name, rev, digest = "team", "gated", "cand000001", "sha256:candidate"
	suite := &assaydv1alpha1.EvalSuite{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "smoke"},
		Spec: assaydv1alpha1.EvalSuiteSpec{Dataset: assaydv1alpha1.EvalDataset{Cases: cases},
			CaseTimeoutSeconds: 10}}
	sd, err := evalsuite.Digest(suite.Spec)
	if err != nil {
		t.Fatal(err)
	}
	agent := &assaydv1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name, UID: "agent-uid", Generation: 2},
		Spec: assaydv1alpha1.AgentSpec{Runtime: &assaydv1alpha1.AgentRuntime{Port: 8080},
			Gates: []assaydv1alpha1.GateRef{{EvalSuiteRef: "smoke"}}},
		Status: assaydv1alpha1.AgentStatus{ObservedGeneration: 2, ActiveRevision: "active0001",
			ActiveRevisionDigest: "sha256:active", CandidateRevision: rev, CandidateRevisionDigest: digest,
			Cards: []assaydv1alpha1.CardStatus{{Revision: rev, RevisionDigest: digest, Digest: digestOfCard(evalCard)}}},
	}
	runNS := RunNamespaceName(ns)
	deploy := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: runNS, Name: WorkloadName(name, rev),
		Annotations: map[string]string{RevisionDigestAnnotation: digest},
		Labels:      map[string]string{LabelAgentUID: "agent-uid"}},
		Status: appsv1.DeploymentStatus{AvailableReplicas: 1}}
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: runNS, Name: WorkloadName(name, rev)},
		Spec: corev1.ServiceSpec{ClusterIP: "10.43.0.9"}}
	b := fake.NewClientBuilder().WithScheme(testScheme()).WithObjects(agent, deploy, svc, suite).
		WithStatusSubresource(&assaydv1alpha1.Agent{})
	if funcs != nil {
		b = b.WithInterceptorFuncs(*funcs)
	}
	c := b.Build()
	r, err := NewEvalReconciler(c, c, func() bool { return true }, GatewayConfig{})
	if err != nil {
		t.Fatal(err)
	}
	stub := newCandidateStub(evalCard)
	r.A2A.HTTP = stub
	return &evalFixture{r: r, c: c, stub: stub, key: types.NamespacedName{Namespace: ns, Name: name},
		suite: suite, sd: sd}
}

func (f *evalFixture) pass(t *testing.T) (ctrl.Result, error) {
	t.Helper()
	return f.r.Reconcile(context.Background(), ctrl.Request{NamespacedName: f.key})
}

func (f *evalFixture) agent(t *testing.T) *assaydv1alpha1.Agent {
	t.Helper()
	var a assaydv1alpha1.Agent
	if err := f.c.Get(context.Background(), f.key, &a); err != nil {
		t.Fatal(err)
	}
	return &a
}

func echoCase(id, msg string) assaydv1alpha1.EvalCase {
	return assaydv1alpha1.EvalCase{ID: id, Message: msg, Expect: assaydv1alpha1.EvalExpect{Contains: "echo: " + msg}}
}

// The run, end to end against the fake API server: a run start, one case per
// pass in id order, and the verdict in the last case's record.
func TestTheEvalControllerRunsTheSuiteToAVerdict(t *testing.T) {
	f := newEvalFixture(t, nil, echoCase("b-second", "two"), echoCase("a-first", "one"))
	res, err := f.pass(t)
	if err != nil {
		t.Fatal(err)
	}
	if res.RequeueAfter != evalSendBackstop || res.Requeue { //nolint:staticcheck // Requeue is what must not be set
		t.Errorf("a pass that sent a case returned %+v, want RequeueAfter %v", res, evalSendBackstop)
	}
	e := f.agent(t).Status.Eval
	if e == nil || e.Verdict != assaydv1alpha1.EvalVerdictRunning || e.EndpointPath != "/a2a/message:send" ||
		e.SuiteDigest != f.sd || e.CardDigest != digestOfCard(evalCard) {
		t.Fatalf("after the first pass status.eval is %+v", e)
	}
	if f.stub.count("one") != 1 || f.stub.count("two") != 0 {
		t.Fatalf("the first pass sent %v; want the first case by id, a-first, alone", f.stub.hits)
	}
	if _, err := f.pass(t); err != nil {
		t.Fatal(err)
	}
	e = f.agent(t).Status.Eval
	if e.Verdict != assaydv1alpha1.EvalVerdictPassed || e.Score != "2/2" || e.At == nil {
		t.Fatalf("after the last case status.eval is %+v", e)
	}
	if len(e.Cases) != 2 || e.Cases[0].ID != "a-first" || e.Cases[1].ID != "b-second" ||
		e.Cases[0].Outcome != assaydv1alpha1.EvalCasePassed {
		t.Errorf("cases[] is %+v; want both, sorted by id, Passed", e.Cases)
	}
	if _, err := f.pass(t); err != nil {
		t.Fatal(err)
	}
	if f.stub.total() != 2 {
		t.Errorf("a pass after the verdict sent a case: %v", f.stub.hits)
	}
}

// T40: the requeue field. At its zero value each branch resolves to the
// constant §1.1 gives it; set, it replaces all of them.
func TestTheRequeueFieldResolvesEachBranch(t *testing.T) {
	r := &EvalReconciler{}
	for w, want := range map[evalWait]time.Duration{
		waitTransaction: AuthProbeInterval,
		waitFetchFailed: CardRetryInterval,
		waitPaused:      CardDriftInterval,
		waitRefused:     CardDriftInterval,
		waitHeld:        CardDriftInterval,
		waitReadBack:    CardDriftInterval,
		waitSent:        5 * time.Second,
	} {
		if got := r.requeueFor(w); got != want {
			t.Errorf("branch %d resolves to %v at the zero value, want %v", w, got, want)
		}
	}
	r.RequeueAfter = 2 * time.Second
	for _, w := range []evalWait{waitTransaction, waitFetchFailed, waitPaused, waitRefused, waitHeld,
		waitReadBack, waitSent} {
		if got := r.requeueFor(w); got != 2*time.Second {
			t.Errorf("branch %d ignores the field: %v", w, got)
		}
	}
}

// T40 through Reconcile: the branch a pass takes is the interval it returns.
func TestEachWaitingPassReturnsItsInterval(t *testing.T) {
	deadline := metav1.NewTime(time.Now().Add(time.Minute))
	for name, tc := range map[string]struct {
		edit func(*assaydv1alpha1.Agent, *evalFixture)
		card string
		want time.Duration
	}{
		"a recorded Create": {edit: func(a *assaydv1alpha1.Agent, _ *evalFixture) {
			a.Status.Auth = &assaydv1alpha1.AuthStatus{Transaction: &assaydv1alpha1.AuthTransaction{
				Kind: TxCreate, Stage: StagePreparingRoute, Deadline: &deadline}}
		}, want: AuthProbeInterval},
		"a failed run-start fetch": {edit: func(_ *assaydv1alpha1.Agent, f *evalFixture) {
			f.stub.refuse = true
		}, want: CardRetryInterval},
		// The recorded digest is this body's, so only a validity check made
		// before the digest compare tells it from a card that decodes badly.
		"a body that does not parse": {card: "not json", want: CardRetryInterval},
		"a refusal that read a card": {card: strings.Replace(evalCard, "HTTP+JSON", "JSONRPC", 1),
			want: CardDriftInterval},
		"a final verdict": {edit: func(a *assaydv1alpha1.Agent, f *evalFixture) {
			a.Status.Eval = &assaydv1alpha1.EvalStatus{RevisionDigest: a.Status.CandidateRevisionDigest,
				SuiteDigest: f.sd, Verdict: assaydv1alpha1.EvalVerdictFailed}
		}, want: CardDriftInterval},
	} {
		t.Run(name, func(t *testing.T) {
			f := newEvalFixture(t, nil, echoCase("a", "one"))
			if tc.card != "" {
				f.stub.card = tc.card
				// The recorded digest is the served card's, so a refusal is the
				// card's content, not a mismatch.
				a := f.agent(t)
				a.Status.Cards[0].Digest = digestOfCard(tc.card)
				if err := f.c.Status().Update(context.Background(), a); err != nil {
					t.Fatal(err)
				}
			}
			if tc.edit != nil {
				a := f.agent(t)
				tc.edit(a, f)
				if err := f.c.Status().Update(context.Background(), a); err != nil {
					t.Fatal(err)
				}
			}
			res, err := f.pass(t)
			if err != nil {
				t.Fatal(err)
			}
			if res.RequeueAfter != tc.want {
				t.Errorf("RequeueAfter %v, want %v", res.RequeueAfter, tc.want)
			}
			if f.stub.total() != 0 {
				t.Errorf("a waiting pass sent a case")
			}
		})
	}
	t.Run("a paused candidate", func(t *testing.T) {
		f := newEvalFixture(t, nil, echoCase("a", "one"))
		var d appsv1.Deployment
		key := types.NamespacedName{Namespace: RunNamespaceName("team"), Name: WorkloadName("gated", "cand000001")}
		if err := f.c.Get(context.Background(), key, &d); err != nil {
			t.Fatal(err)
		}
		d.Status.AvailableReplicas = 0
		if err := f.c.Status().Update(context.Background(), &d); err != nil {
			t.Fatal(err)
		}
		res, err := f.pass(t)
		if err != nil || res.RequeueAfter != CardDriftInterval || f.stub.total() != 0 {
			t.Errorf("a paused candidate: %+v %v, %d sends; want CardDriftInterval and none", res, err, f.stub.total())
		}
	})
}

// The constructor refuses a nil uncached reader, and the receiver it builds
// per pass for clause 5 carries that reader, not the cached client.
func TestTheEvalControllerRequiresAnUncachedReader(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(testScheme()).Build()
	if _, err := NewEvalReconciler(c, nil, func() bool { return true }, GatewayConfig{}); err == nil {
		t.Fatal("an eval controller was built with no uncached reader")
	}
	uncached := fake.NewClientBuilder().WithScheme(testScheme()).Build()
	r, err := NewEvalReconciler(c, uncached, func() bool { return true }, GatewayConfig{Name: "assayd"})
	if err != nil {
		t.Fatal(err)
	}
	if got := r.policyReads().reader(); got != uncached {
		t.Errorf("policyReads reads through %v, not the uncached reader", got)
	}
	if r.policyReads().Gateway.Name != "assayd" {
		t.Error("policyReads does not carry the Gateway")
	}
	if r.A2A == nil || r.A2A.HTTP == nil {
		t.Error("the constructor built no eval client")
	}
}

// A Service with no usable ClusterIP is the operator's fault: the pass
// returns an error and claims, sends and records nothing.
func TestAnUnusableAddressClaimsNothing(t *testing.T) {
	f := newEvalFixture(t, nil, echoCase("a", "one"))
	var svc corev1.Service
	key := types.NamespacedName{Namespace: RunNamespaceName("team"), Name: WorkloadName("gated", "cand000001")}
	if err := f.c.Get(context.Background(), key, &svc); err != nil {
		t.Fatal(err)
	}
	svc.Spec.ClusterIP = "None"
	if err := f.c.Update(context.Background(), &svc); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pass(t); err == nil {
		t.Error("a headless candidate Service gave no error")
	}
	if e := f.agent(t).Status.Eval; e != nil || f.stub.total() != 0 || f.stub.cardGets != 0 {
		t.Errorf("an unusable address wrote %+v, fetched the card %d times and sent %d", e,
			f.stub.cardGets, f.stub.total())
	}
}

// The same, mid-run: the address is checked before every claim, not only at
// run start. A Service that loses its ClusterIP after the first case gets no
// claim and no send for the second.
func TestAnAddressThatBreaksMidRunClaimsNothing(t *testing.T) {
	f := newEvalFixture(t, nil, echoCase("a", "one"), echoCase("b", "two"))
	if _, err := f.pass(t); err != nil {
		t.Fatal(err)
	}
	var svc corev1.Service
	key := types.NamespacedName{Namespace: RunNamespaceName("team"), Name: WorkloadName("gated", "cand000001")}
	if err := f.c.Get(context.Background(), key, &svc); err != nil {
		t.Fatal(err)
	}
	svc.Spec.ClusterIP = "None"
	if err := f.c.Update(context.Background(), &svc); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pass(t); err == nil {
		t.Error("a candidate Service that lost its address gave no error")
	}
	if c := caseRecord(f.agent(t).Status.Eval.Cases, "b"); c != nil || f.stub.count("two") != 0 {
		t.Errorf("a case was claimed (%+v) or sent (%d) to an unusable address", c, f.stub.count("two"))
	}
}

// #105 item 9: the path the eval would record and send to is refused at run
// start, under EvalBindingUnsupported, when it is too long, has no leading
// "/", or carries a control character.
func TestTheEndpointPathIsBounded(t *testing.T) {
	ok := "/" + strings.Repeat("a", MaxEndpointPathLength-1)
	if err := checkEndpointPath(ok); err != nil {
		t.Errorf("a path of exactly %d characters is refused: %v", MaxEndpointPathLength, err)
	}
	// Counted in characters, as the CRD's MaxLength counts them, not bytes.
	if err := checkEndpointPath("/" + strings.Repeat("é", MaxEndpointPathLength-1)); err != nil {
		t.Errorf("a path of %d two-byte characters is refused: %v", MaxEndpointPathLength, err)
	}
	for name, path := range map[string]string{
		"too long":         ok + "a",
		"no leading slash": "a2a/message:send",
	} {
		if err := checkEndpointPath(path); err == nil {
			t.Errorf("%s: %q is admitted", name, path)
		}
	}
}

// The run-start refusals through Reconcile: a tenanted interface (#108), and
// a relative card URL whose path has no leading "/" (#105 item 9), each write
// EvalBindingUnsupported to blocked and start no run.
func TestARunStartRefusalIsWrittenToBlocked(t *testing.T) {
	for name, card := range map[string]string{
		"a tenanted interface": strings.Replace(evalCard, `"protocolVersion":"1.0"`,
			`"protocolVersion":"1.0","tenant":"t1"`, 1),
		"a relative url": strings.Replace(evalCard, "http://agent.example/a2a/", "a2a", 1),
		"a long path": strings.Replace(evalCard, "http://agent.example/a2a/",
			"http://agent.example/"+strings.Repeat("p", MaxEndpointPathLength), 1),
		// %FF decodes to a byte that is not UTF-8, which status, being JSON,
		// would store as U+FFFD: a path the card never declared.
		"a path that is not UTF-8": strings.Replace(evalCard, "http://agent.example/a2a/",
			"http://agent.example/%FF/", 1),
		// A tenant that is a number: the card fetch's own type does not read
		// it, so the card is recorded, and it is a property of the card, not
		// a fetch to retry.
		"a wrongly typed interface field": strings.Replace(evalCard, `"protocolVersion":"1.0"`,
			`"protocolVersion":"1.0","tenant":5`, 1),
		// An escaped slash: Path decodes it, so the case would go to /a/b/,
		// a path the card never declared.
		"an escaped slash": strings.Replace(evalCard, "http://agent.example/a2a/",
			"http://agent.example/a%2Fb/", 1),
	} {
		t.Run(name, func(t *testing.T) {
			f := newEvalFixture(t, nil, echoCase("a", "one"))
			f.stub.card = card
			a := f.agent(t)
			a.Status.Cards[0].Digest = digestOfCard(card)
			if err := f.c.Status().Update(context.Background(), a); err != nil {
				t.Fatal(err)
			}
			if _, err := f.pass(t); err != nil {
				t.Fatal(err)
			}
			e := f.agent(t).Status.Eval
			if e == nil || e.Blocked == nil || e.Blocked.Reason != ReasonEvalBindingUnsupported {
				t.Fatalf("status.eval is %+v; want blocked EvalBindingUnsupported", e)
			}
			if e.Verdict != "" || e.EndpointPath != "" || f.stub.total() != 0 {
				t.Errorf("a refused card started a run: %+v, %d sends", e, f.stub.total())
			}
			if e.BlockedFor == nil || e.BlockedFor.RevisionDigest != "sha256:candidate" || e.BlockedFor.SuiteDigest != f.sd {
				t.Errorf("blockedFor %+v does not name the pair blocked was computed for", e.BlockedFor)
			}
		})
	}
}

// T32: a stale reader. The Agent reader returns a status from before case
// k's outcome was recorded; the claim made from it carries the old
// resourceVersion and is refused, so the candidate is sent case k once.
func TestAStaleReadNeverResendsARecordedCase(t *testing.T) {
	f := newEvalFixture(t, nil, echoCase("a", "one"), echoCase("b", "two"))
	stale := f.agent(t) // before any run
	if _, err := f.pass(t); err != nil {
		t.Fatal(err)
	}
	if f.stub.count("one") != 1 {
		t.Fatal("setup: case a was not sent")
	}
	// The run start and a's record: a's outcome is recorded now. A read that
	// returns the status from after the run start and before a's record:
	after := f.agent(t)
	afterStart := after.DeepCopy()
	afterStart.Status.Eval.Cases = nil
	afterStart.Status.Eval.Verdict = assaydv1alpha1.EvalVerdictRunning
	afterStart.ResourceVersion = stale.ResourceVersion
	f.r.uncached = &staleOnce{Client: f.c, agent: afterStart}
	if _, err := f.pass(t); err != nil {
		t.Fatal(err)
	}
	if n := f.stub.count("one"); n != 1 {
		t.Fatalf("case a, already recorded, was sent %d times after a stale read", n)
	}
	// The next pass reads live and goes on to b.
	if _, err := f.pass(t); err != nil {
		t.Fatal(err)
	}
	if f.stub.count("two") != 1 || f.stub.count("one") != 1 {
		t.Errorf("after the stale pass the run sent %v", f.stub.hits)
	}
}

// staleOnce returns one stored Agent on its first Agent read, and reads
// through after that.
type staleOnce struct {
	client.Client
	agent *assaydv1alpha1.Agent
}

func (s *staleOnce) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if a, ok := obj.(*assaydv1alpha1.Agent); ok && s.agent != nil {
		s.agent.DeepCopyInto(a)
		s.agent = nil
		return nil
	}
	return s.Client.Get(ctx, key, obj, opts...)
}

// T37: an ambiguous claim. The claim patch is committed and its response is
// a 5xx: that pass sends nothing, and the next records Interrupted, never
// sending the case at all.
func TestAnAmbiguousClaimSendsNothing(t *testing.T) {
	// The first patch that writes a claim commits, and its response is lost.
	lost := false
	funcs := &interceptor.Funcs{SubResourcePatch: func(ctx context.Context, c client.Client, sub string,
		obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
		err := c.SubResource(sub).Patch(ctx, obj, patch, opts...)
		if err != nil || lost {
			return err
		}
		if a, ok := obj.(*assaydv1alpha1.Agent); ok && a.Status.Eval != nil && len(a.Status.Eval.Cases) == 1 &&
			a.Status.Eval.Cases[0].Outcome == assaydv1alpha1.EvalCaseSending {
			lost = true
			return apierrors.NewInternalError(errors.New("the response was lost"))
		}
		return nil
	}}
	f := newEvalFixture(t, funcs, echoCase("a", "one"), echoCase("b", "two"))
	if _, err := f.pass(t); err == nil {
		t.Fatal("a claim whose response was lost gave no error")
	}
	if f.stub.total() != 0 {
		t.Fatalf("a claim whose response was lost was followed by a send: %v", f.stub.hits)
	}
	c := caseRecord(f.agent(t).Status.Eval.Cases, "a")
	if c == nil || c.Outcome != assaydv1alpha1.EvalCaseSending {
		t.Fatalf("setup: the claim was not committed: %+v", c)
	}
	if _, err := f.pass(t); err != nil {
		t.Fatal(err)
	}
	c = caseRecord(f.agent(t).Status.Eval.Cases, "a")
	if c == nil || c.Outcome != assaydv1alpha1.EvalCaseFailed || c.Reason != string(evalclient.ReasonInterrupted) {
		t.Fatalf("the next pass recorded %+v, want Failed/Interrupted", c)
	}
	for i := 0; i < 3; i++ {
		if _, err := f.pass(t); err != nil {
			t.Fatal(err)
		}
	}
	if f.stub.count("one") != 0 || f.stub.count("two") != 1 {
		t.Errorf("after the Interrupted record the run sent %v; want b once and a never", f.stub.hits)
	}
	if e := f.agent(t).Status.Eval; e.Verdict != assaydv1alpha1.EvalVerdictFailed || e.Score != "1/2" {
		t.Errorf("the verdict is %s %s; an Interrupted case counts against the threshold", e.Verdict, e.Score)
	}
}

// failed[] holds at most ten pairs, and evicts the oldest.
func TestFailedPairsEvictTheOldest(t *testing.T) {
	var failed []assaydv1alpha1.EvalPair
	for i := 0; i < 11; i++ {
		failed = appendFailed(failed, assaydv1alpha1.EvalPair{RevisionDigest: string(rune('a' + i)), SuiteDigest: "s"})
	}
	if len(failed) != 10 || failed[0].RevisionDigest != "b" || failed[9].RevisionDigest != "k" {
		t.Errorf("failed[] is %+v; want the ten newest, b to k", failed)
	}
}

// EvalRunning names the pair and N and never k, so an eval patch that only
// records a case leaves the Agent reconciler's status write a no-op (A5).
// EvalPaused names k of N.
func TestRunningNamesNAndPausedNamesKOfN(t *testing.T) {
	in := gateInput{desired: gRev, desiredDigest: gDigest, suiteDigest: gSuite, suite: twoCaseSuite(),
		available: true, now: time.Now(), status: &assaydv1alpha1.AgentStatus{Eval: &assaydv1alpha1.EvalStatus{
			RevisionDigest: gDigest, SuiteDigest: gSuite, Verdict: assaydv1alpha1.EvalVerdictRunning}}}
	withCard(in.status)
	before := decideGate(in)
	in.status.Eval.Cases = []assaydv1alpha1.EvalCaseRecord{{ID: "arithmetic", Outcome: assaydv1alpha1.EvalCasePassed}}
	after := decideGate(in)
	if before.reason != ReasonEvalRunning || after != before {
		t.Errorf("EvalRunning changed as a case recorded:\n%+v\n%+v", before, after)
	}
	if !strings.Contains(before.message, "2 cases") || !strings.Contains(before.message, gDigest) ||
		!strings.Contains(before.message, "status.eval.cases[]") {
		t.Errorf("EvalRunning does not name the pair, N and where progress is: %s", before.message)
	}
	// It claims nothing about sending: the eval controller may be unable to
	// reach the candidate at all, and nothing tells this branch (#113).
	for _, claim := range []string{"is sent", "evaluating", "sending"} {
		if strings.Contains(before.message, claim) {
			t.Errorf("EvalRunning asserts progress (%q): %s", claim, before.message)
		}
	}
	in.available = false
	d := decideGate(in)
	if d.reason != ReasonEvalPaused || !strings.Contains(d.message, "1 of its 2 cases have an outcome") {
		t.Errorf("a paused run: %s %s", d.reason, d.message)
	}
	// A claim in flight is not an outcome.
	in.status.Eval.Cases = append(in.status.Eval.Cases,
		assaydv1alpha1.EvalCaseRecord{ID: "greeting", Outcome: assaydv1alpha1.EvalCaseSending})
	if d := decideGate(in); !strings.Contains(d.message, "1 of its 2 cases") {
		t.Errorf("a Sending entry counted as an outcome: %s", d.message)
	}
	in.status.Eval = nil
	if d := decideGate(in); d.reason != ReasonEvalPaused || !strings.Contains(d.message, "no run has started") {
		t.Errorf("an unavailable candidate with no run: %s %s", d.reason, d.message)
	}
}

// The verdict is the suite's threshold rule (step 7): 1 of 2 passes "0.5"
// and fails with no metric, whose threshold is 1.
func TestTheVerdictIsTheSuitesThresholdRule(t *testing.T) {
	for _, tc := range []struct {
		metrics []assaydv1alpha1.EvalMetric
		want    assaydv1alpha1.EvalVerdict
	}{
		{[]assaydv1alpha1.EvalMetric{{Name: "task_completion", Threshold: "0.5"}}, assaydv1alpha1.EvalVerdictPassed},
		{[]assaydv1alpha1.EvalMetric{{Name: "task_completion", Threshold: "0.501"}}, assaydv1alpha1.EvalVerdictFailed},
		{nil, assaydv1alpha1.EvalVerdictFailed},
	} {
		suite := &assaydv1alpha1.EvalSuite{Spec: assaydv1alpha1.EvalSuiteSpec{Metrics: tc.metrics,
			Dataset: assaydv1alpha1.EvalDataset{Cases: []assaydv1alpha1.EvalCase{{ID: "a"}, {ID: "b"}}}}}
		th, err := evalsuite.Threshold(suite.Spec)
		if err != nil {
			t.Fatal(err)
		}
		run := &evalRun{suite: suite, threshold: th, revisionDigest: "r", suiteDigest: "s"}
		e := run.withOutcome(&assaydv1alpha1.EvalStatus{}, "a", "n1", assaydv1alpha1.EvalCasePassed, "")
		if e.Verdict != "" {
			t.Fatalf("a verdict was written with one of two cases recorded: %+v", e)
		}
		e = run.withOutcome(e, "b", "n2", assaydv1alpha1.EvalCaseFailed, "Mismatch")
		if e.Verdict != tc.want || e.Score != "1/2" || e.At == nil {
			t.Errorf("threshold %v: verdict %s score %s, want %s 1/2", tc.metrics, e.Verdict, e.Score, tc.want)
		}
		if (tc.want == assaydv1alpha1.EvalVerdictFailed) != (len(e.Failed) == 1) {
			t.Errorf("threshold %v: failed[] is %+v", tc.metrics, e.Failed)
		}
	}
}

// cases[] is kept sorted by id, whatever order its entries are written in
// (A12).
func TestCasesStaySortedByID(t *testing.T) {
	cases := withCase(nil, assaydv1alpha1.EvalCaseRecord{ID: "b"})
	cases = withCase(cases, assaydv1alpha1.EvalCaseRecord{ID: "a"})
	if len(cases) != 2 || cases[0].ID != "a" {
		t.Fatalf("a claim of a, after b, left cases[] as %+v", cases)
	}
	cases = withCase(cases, assaydv1alpha1.EvalCaseRecord{ID: "b", Outcome: assaydv1alpha1.EvalCasePassed})
	if len(cases) != 2 || cases[0].ID != "a" || cases[1].ID != "b" || cases[1].Outcome != assaydv1alpha1.EvalCasePassed {
		t.Errorf("cases[] is %+v; want a then b, b replaced in place", cases)
	}
}

// A card body that does not parse is a run-start fetch that did not succeed,
// "where no digest was compared" (§1.1 step 2): EvalAwaitingCard naming the
// failure and no digest, retried at CardRetryInterval. A card that parses,
// matches and decodes badly is the card's own fault instead
// (EvalBindingUnsupported, in TestARunStartRefusalIsWrittenToBlocked). The
// two are told apart whatever the recorded digest says.
func TestABodyThatIsNotJSONIsAFailedFetch(t *testing.T) {
	for name, recorded := range map[string]bool{"recorded digest is this body's": true,
		"recorded digest is another card's": false} {
		t.Run(name, func(t *testing.T) {
			f := newEvalFixture(t, nil, echoCase("a", "one"))
			f.stub.card = "not json"
			if recorded {
				a := f.agent(t)
				a.Status.Cards[0].Digest = digestOfCard("not json")
				if err := f.c.Status().Update(context.Background(), a); err != nil {
					t.Fatal(err)
				}
			}
			res, err := f.pass(t)
			if err != nil {
				t.Fatal(err)
			}
			e := f.agent(t).Status.Eval
			if e == nil || e.Blocked == nil || e.Blocked.Reason != ReasonEvalAwaitingCard ||
				!strings.Contains(e.Blocked.Message, "did not succeed") ||
				strings.Contains(e.Blocked.Message, "has digest") || res.RequeueAfter != CardRetryInterval {
				t.Errorf("a body that is not JSON: blocked %+v, requeue %v; want EvalAwaitingCard, a "+
					"failed fetch naming no digest, retried at CardRetryInterval", e, res.RequeueAfter)
			}
		})
	}
}

// T31's other half: a record whose first patch meets a Conflict, while the
// claim still stands and its outcome is still Sending, is re-read and
// re-applied, and the outcome the candidate gave is recorded, not left for
// the next pass to record Interrupted.
func TestARecordThatConflictsIsReapplied(t *testing.T) {
	conflicted := false
	funcs := &interceptor.Funcs{SubResourcePatch: func(ctx context.Context, c client.Client, sub string,
		obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
		raw, _ := patch.Data(obj)
		if !conflicted && strings.Contains(string(raw), `"Passed"`) && strings.Contains(string(raw), `"cases"`) {
			conflicted = true
			// A real write by another writer first, so the re-read finds a
			// newer object with the claim still standing.
			var live assaydv1alpha1.Agent
			if err := c.Get(ctx, client.ObjectKeyFromObject(obj), &live); err != nil {
				return err
			}
			live.Status.Phase = assaydv1alpha1.PhaseHeld
			if err := c.Status().Update(ctx, &live); err != nil {
				return err
			}
			return apierrors.NewConflict(evalAgentGR, obj.GetName(), errors.New("injected"))
		}
		return c.SubResource(sub).Patch(ctx, obj, patch, opts...)
	}}
	f := newEvalFixture(t, funcs, echoCase("a", "one"), echoCase("b", "two"))
	if _, err := f.pass(t); err != nil {
		t.Fatal(err)
	}
	if !conflicted {
		t.Fatal("setup: the record never met the injected Conflict")
	}
	c := caseRecord(f.agent(t).Status.Eval.Cases, "a")
	if c == nil || c.Outcome != assaydv1alpha1.EvalCasePassed {
		t.Fatalf("after a Conflict on its record, case a is %+v; want Passed, re-applied", c)
	}
	if _, err := f.pass(t); err != nil {
		t.Fatal(err)
	}
	if c := caseRecord(f.agent(t).Status.Eval.Cases, "a"); c.Reason == string(evalclient.ReasonInterrupted) {
		t.Errorf("case a was recorded Interrupted: %+v", c)
	}
	if f.stub.count("one") != 1 {
		t.Errorf("case a was sent %d times", f.stub.count("one"))
	}
}

var evalAgentGR = schema.GroupResource{Group: "assayd.dev", Resource: "agents"}

// staleAgents answers every Agent read with a snapshot, and every other
// read through the client: a cache that lags the API server.
type staleAgents struct {
	client.Client
	snapshot *assaydv1alpha1.Agent
}

func (s staleAgents) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if a, ok := obj.(*assaydv1alpha1.Agent); ok {
		s.snapshot.DeepCopyInto(a)
		return nil
	}
	return s.Client.Get(ctx, key, obj, opts...)
}

// The Agent is read through the uncached reader before every write, never
// through the cached client (design 16 §1.1, "The two controllers"). With a
// cache that lags — here, a snapshot from before the run — a pass still
// reads the live record and goes on to the next case; one that read the
// cache would build its writes on a stale object and lose every pass to the
// optimistic lock.
func TestTheAgentIsReadLiveNotFromTheCache(t *testing.T) {
	f := newEvalFixture(t, nil, echoCase("a", "one"), echoCase("b", "two"))
	before := f.agent(t)
	if _, err := f.pass(t); err != nil {
		t.Fatal(err)
	}
	f.r.Client = staleAgents{Client: f.c, snapshot: before}
	if _, err := f.pass(t); err != nil {
		t.Fatal(err)
	}
	if f.stub.count("two") != 1 || f.stub.count("one") != 1 {
		t.Errorf("with a lagging cache the run sent %v; want b next, read live", f.stub.hits)
	}
}

// The record's claim-stands check reads the nonce: a live record whose entry
// carries another claim — a restart or a second claimant re-claimed the case
// — is not overwritten by this send's outcome, even with the pair the same
// and the outcome still Sending.
func TestARecordIsDroppedWhenAnotherClaimHoldsTheCase(t *testing.T) {
	swapped := false
	funcs := &interceptor.Funcs{SubResourcePatch: func(ctx context.Context, c client.Client, sub string,
		obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
		raw, _ := patch.Data(obj)
		if !swapped && strings.Contains(string(raw), `"Passed"`) && strings.Contains(string(raw), `"cases"`) {
			swapped = true
			var live assaydv1alpha1.Agent
			if err := c.Get(ctx, client.ObjectKeyFromObject(obj), &live); err != nil {
				return err
			}
			for i := range live.Status.Eval.Cases {
				if live.Status.Eval.Cases[i].ID == "a" {
					live.Status.Eval.Cases[i].Claim = "another-claimant"
				}
			}
			if err := c.Status().Update(ctx, &live); err != nil {
				return err
			}
			return apierrors.NewConflict(evalAgentGR, obj.GetName(), errors.New("injected"))
		}
		return c.SubResource(sub).Patch(ctx, obj, patch, opts...)
	}}
	f := newEvalFixture(t, funcs, echoCase("a", "one"), echoCase("b", "two"))
	if _, err := f.pass(t); err != nil {
		t.Fatal(err)
	}
	if !swapped {
		t.Fatal("setup: the record never met the re-claimed case")
	}
	c := caseRecord(f.agent(t).Status.Eval.Cases, "a")
	if c == nil || c.Claim != "another-claimant" || c.Outcome != assaydv1alpha1.EvalCaseSending {
		t.Errorf("a record overwrote a case another claim holds: %+v", c)
	}
}

// EvalPaused counts the outcomes of the current pair's run only: a record of
// another pair, Running with outcomes, is no run of this one.
func TestPausedCountsOnlyTheCurrentPairsRun(t *testing.T) {
	in := gateInput{desired: gRev, desiredDigest: gDigest, suiteDigest: gSuite, suite: twoCaseSuite(),
		now: time.Now(), status: &assaydv1alpha1.AgentStatus{Eval: &assaydv1alpha1.EvalStatus{
			RevisionDigest: "sha256:another", SuiteDigest: gSuite, Verdict: assaydv1alpha1.EvalVerdictRunning,
			Cases: []assaydv1alpha1.EvalCaseRecord{{ID: "arithmetic", Outcome: assaydv1alpha1.EvalCasePassed}}}}}
	withCard(in.status)
	d := decideGate(in)
	if d.reason != ReasonEvalPaused || !strings.Contains(d.message, "no run has started") ||
		strings.Contains(d.message, "1 of its 2") {
		t.Errorf("a paused candidate beside another revision's run: %s %s", d.reason, d.message)
	}
	// The same revision against a suite edited while the candidate was paused
	// mid-run: no run of the edited suite has started.
	in.status.Eval.RevisionDigest, in.status.Eval.SuiteDigest = gDigest, "sha256:the-suite-before-its-edit"
	d = decideGate(in)
	if d.reason != ReasonEvalPaused || !strings.Contains(d.message, "no run has started") ||
		strings.Contains(d.message, "1 of its 2") {
		t.Errorf("a paused candidate beside the run of its suite before an edit: %s %s", d.reason, d.message)
	}
}

// A run-start fetch that failed at the connection names that class, not
// "the body is not JSON": the validity check reads a body only when there is
// one.
func TestAFailedFetchNamesItsOwnClass(t *testing.T) {
	f := newEvalFixture(t, nil, echoCase("a", "one"))
	f.stub.refuse = true
	if _, err := f.pass(t); err != nil {
		t.Fatal(err)
	}
	e := f.agent(t).Status.Eval
	if e == nil || e.Blocked == nil || !strings.Contains(e.Blocked.Message, "refused or failed") ||
		strings.Contains(e.Blocked.Message, "not JSON") {
		t.Errorf("a refused run-start fetch: blocked %+v; want it to name the refused connection", e)
	}
}
