// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package envtest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/controller"
	"github.com/Quinyte/assayd/internal/evalsuite"
	"github.com/Quinyte/assayd/internal/revision"
)

// Design 16 §1.1 build step 6: the eval controller. envtest has no kubelet
// and no cluster network, so a2aStub answers in every candidate's place: the
// Agent reconciler's card fetch and the eval controller's run-start fetch and
// cases all reach it, addressed to a revision Service's ClusterIP, and it
// resolves that address to the Service, and so to the Agent and revision it
// belongs to, through the API server. What it sees is what the operator sent.

// a2aStub is every candidate.
type a2aStub struct {
	mu sync.Mutex
	// byIP resolves a ClusterIP to its revision Service's labels.
	byIP map[string]map[string]string
	// cards overrides the card an Agent serves, by Agent name.
	cards map[string]string
	// cardRefused refuses the card fetch of an Agent at the connection.
	cardRefused map[string]bool
	// cardGets counts card fetches, by Agent name.
	cardGets map[string]int
	// hits counts the messages a revision was sent: "<agent>/<revision>/<text>".
	hits map[string]int
	// holds parks a message with this text until it is released.
	holds map[string]*held
	// posts counts every POST, to any path, by Agent name.
	posts map[string]int
	// delay is how long each answer to a case takes.
	delay time.Duration
}

type held struct {
	arrived chan struct{}
	release chan struct{}
	once    sync.Once
}

func newA2AStub() *a2aStub {
	return &a2aStub{byIP: map[string]map[string]string{}, cards: map[string]string{},
		cardRefused: map[string]bool{}, cardGets: map[string]int{}, hits: map[string]int{},
		holds: map[string]*held{}, posts: map[string]int{}}
}

// stubCard is a valid A2A 1.0 card naming the Agent, with one HTTP+JSON 1.0
// interface at /a2a/. Its URL's host is never dialled.
func stubCard(agent string) string {
	return `{"name":"` + agent + `","description":"d","version":"1",` +
		`"supportedInterfaces":[{"url":"http://agent.example/a2a/","protocolBinding":"HTTP+JSON",` +
		`"protocolVersion":"1.0"}],"capabilities":{},"skills":[{"id":"echo"}]}`
}

func (s *a2aStub) labelsOf(ip string) map[string]string {
	s.mu.Lock()
	if l, ok := s.byIP[ip]; ok {
		s.mu.Unlock()
		return l
	}
	s.mu.Unlock()
	var svcs corev1.ServiceList
	if err := k8s.List(context.Background(), &svcs); err != nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, svc := range svcs.Items {
		if svc.Spec.ClusterIP != "" {
			s.byIP[svc.Spec.ClusterIP] = svc.Labels
		}
	}
	return s.byIP[ip]
}

func (s *a2aStub) Do(req *http.Request) (*http.Response, error) {
	host, _, err := net.SplitHostPort(req.URL.Host)
	if err != nil {
		return nil, err
	}
	l := s.labelsOf(host)
	if l == nil {
		return nil, fmt.Errorf("dial %s: no revision Service has this address", req.URL.Host)
	}
	agent, rev := l[controller.LabelAgent], l[controller.LabelRevision]
	answer := func(code int, body string) (*http.Response, error) {
		return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)),
			Header: http.Header{}, Request: req}, nil
	}
	if req.Method == http.MethodGet {
		s.mu.Lock()
		s.cardGets[agent]++
		refused, card := s.cardRefused[agent], s.cards[agent]
		s.mu.Unlock()
		if refused {
			return nil, errors.New("connection refused")
		}
		if card == "" {
			card = stubCard(agent)
		}
		return answer(http.StatusOK, card)
	}
	s.mu.Lock()
	s.posts[agent]++
	s.mu.Unlock()
	if req.URL.Path != "/a2a/message:send" {
		return answer(http.StatusNotFound, "")
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
	s.hits[agent+"/"+rev+"/"+text]++
	h := s.holds[text]
	s.mu.Unlock()
	if h != nil {
		h.once.Do(func() { close(h.arrived) })
		// A candidate that is still answering when the caller gives up: the
		// request's context ends the wait, as a real connection would.
		select {
		case <-h.release:
		case <-req.Context().Done():
			return nil, req.Context().Err()
		}
	}
	if s.delay > 0 {
		time.Sleep(s.delay)
	}
	if text == "unreachable" {
		return nil, errors.New("connection refused")
	}
	out, _ := json.Marshal(map[string]any{"task": map[string]any{"id": "t",
		"status": map[string]any{"state": "TASK_STATE_COMPLETED"},
		"artifacts": []any{map[string]any{"artifactId": "a",
			"parts": []any{map[string]any{"text": "echo: " + text}}}}}})
	return answer(http.StatusOK, string(out))
}

// hold parks the next message with this text until release is called.
func (s *a2aStub) hold(text string) (arrived <-chan struct{}, release func()) {
	h := &held{arrived: make(chan struct{}), release: make(chan struct{})}
	s.mu.Lock()
	s.holds[text] = h
	s.mu.Unlock()
	var once sync.Once
	return h.arrived, func() { once.Do(func() { close(h.release) }) }
}

func (s *a2aStub) sent(agent, rev, text string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hits[agent+"/"+rev+"/"+text]
}

// sentTo is how many messages of any text a revision was sent.
func (s *a2aStub) sentTo(agent, rev string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for k, v := range s.hits {
		if strings.HasPrefix(k, agent+"/"+rev+"/") {
			n += v
		}
	}
	return n
}

func (s *a2aStub) postsTo(agent string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.posts[agent]
}

func (s *a2aStub) cardFetches(agent string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cardGets[agent]
}

func (s *a2aStub) setCard(agent, card string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cards[agent] = card
}

func (s *a2aStub) refuseCard(agent string, on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cardRefused[agent] = on
}

// newEvalController is the eval controller as the binary builds it, reading
// through the suite's client, with the stub as every candidate.
func newEvalController(t *testing.T, stub *a2aStub) *controller.EvalReconciler {
	t.Helper()
	e, err := controller.NewEvalReconciler(k8s, k8s, func() bool { return true }, controller.GatewayConfig{})
	if err != nil {
		t.Fatal(err)
	}
	e.A2A.HTTP = stub
	e.CardFetchTimeout = time.Second
	return e
}

func evalOnce(t *testing.T, e *controller.EvalReconciler, a *assaydv1alpha1.Agent) ctrl.Result {
	t.Helper()
	res, err := e.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(a)})
	if err != nil {
		t.Fatalf("eval pass: %v", err)
	}
	return res
}

func echo(id, msg string) assaydv1alpha1.EvalCase { return evalCase(id, msg, "echo: "+msg, "") }

// putSuite creates or replaces a suite's cases and returns the digest the
// operator computes from what the API server stored.
func putSuite(t *testing.T, ns, name string, cases ...assaydv1alpha1.EvalCase) string {
	t.Helper()
	ctx := context.Background()
	installGeneratedEvalSuiteCRD(t)
	key := types.NamespacedName{Namespace: ns, Name: name}
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var s assaydv1alpha1.EvalSuite
		err := k8s.Get(ctx, key, &s)
		if err != nil {
			return k8s.Create(ctx, suite(ns, name, cases...))
		}
		s.Spec.Dataset.Cases = cases
		return k8s.Update(ctx, &s)
	}); err != nil {
		t.Fatalf("write suite %s: %v", name, err)
	}
	var stored assaydv1alpha1.EvalSuite
	if err := k8s.Get(ctx, key, &stored); err != nil {
		t.Fatal(err)
	}
	d, err := evalsuite.Digest(stored.Spec)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// evalRig is a served Agent whose gated candidate is available and carded,
// beside its suite: the state in which a run starts.
type evalRig struct {
	ns, name   string
	a          *assaydv1alpha1.Agent
	r          *controller.AgentReconciler
	e          *controller.EvalReconciler
	stub       *a2aStub
	active     string
	rev, dgst  string
	suiteDgst  string
	activeName string
}

func newEvalRig(t *testing.T, name string, cases ...assaydv1alpha1.EvalCase) *evalRig {
	t.Helper()
	ns := newNamespace(t)
	sd := putSuite(t, ns, "smoke", cases...)
	a, r := servedUngated(t, ns, name)
	stub := newA2AStub()
	r.CardClient = stub
	rig := &evalRig{ns: ns, name: name, a: a, r: r, e: newEvalController(t, stub), stub: stub,
		active: a.Status.ActiveRevisionDigest, activeName: a.Status.ActiveRevision, suiteDgst: sd}
	rig.rev, rig.dgst = gatedCandidate(t, r, a, imageB, "smoke")
	if !cardRecorded(&liveAgent(t, a).Status, rig.dgst) {
		t.Fatal("setup: the candidate's card was not recorded")
	}
	return rig
}

func (g *evalRig) agentPass(t *testing.T) { t.Helper(); reconcileOnce(t, g.r, g.a) }
func (g *evalRig) evalPass(t *testing.T) ctrl.Result {
	t.Helper()
	return evalOnce(t, g.e, g.a)
}

// both runs one eval pass and then one Agent reconciler pass, as the eval
// patch's event would.
func (g *evalRig) both(t *testing.T, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		g.evalPass(t)
		g.agentPass(t)
	}
}

func (g *evalRig) eval(t *testing.T) *assaydv1alpha1.EvalStatus {
	t.Helper()
	return liveAgent(t, g.a).Status.Eval
}

func caseOf(e *assaydv1alpha1.EvalStatus, id string) *assaydv1alpha1.EvalCaseRecord {
	if e == nil {
		return nil
	}
	for i := range e.Cases {
		if e.Cases[i].ID == id {
			return &e.Cases[i]
		}
	}
	return nil
}

// T10: a gated Agent's second revision holds, EvalRunning, while the first
// serves; it promotes only after every case has an outcome and the verdict is
// Passed. A Passed record then survives further eval passes: the revision it
// names is the one serving, so the clear on revert (#111) does not fire.
func TestAGatedCandidatePromotesOnlyOnAPassedVerdict(t *testing.T) {
	g := newEvalRig(t, "promotes", echo("a", "one"), echo("b", "two"), echo("c", "three"))
	g.agentPass(t)
	condIs(t, g.a, assaydv1alpha1.CondGatesPassed, metav1.ConditionFalse, controller.ReasonEvalRunning)
	for i, text := range []string{"one", "two"} {
		g.both(t, 1)
		if got := liveAgent(t, g.a); got.Status.ActiveRevisionDigest != g.active {
			t.Fatalf("the candidate was promoted after %d of 3 cases", i+1)
		}
		condIs(t, g.a, assaydv1alpha1.CondGatesPassed, metav1.ConditionFalse, controller.ReasonEvalRunning)
		condIs(t, g.a, assaydv1alpha1.CondReady, metav1.ConditionTrue, "Available")
		if g.stub.sent(g.name, g.rev, text) != 1 {
			t.Fatalf("case %q was sent %d times", text, g.stub.sent(g.name, g.rev, text))
		}
	}
	g.both(t, 1)
	got := liveAgent(t, g.a)
	if got.Status.ActiveRevisionDigest != g.dgst {
		t.Fatalf("the candidate did not promote on a Passed verdict: %+v", got.Status.Eval)
	}
	c := condIs(t, g.a, assaydv1alpha1.CondGatesPassed, metav1.ConditionTrue, controller.ReasonEvalPassed)
	if c != nil && !strings.Contains(c.Message, "3/3") {
		t.Errorf("EvalPassed does not name the score: %s", c.Message)
	}
	for i := 0; i < 2; i++ {
		g.evalPass(t)
	}
	if e := g.eval(t); e.Verdict != assaydv1alpha1.EvalVerdictPassed || e.Score != "3/3" {
		t.Errorf("the serving revision's Passed record was cleared: %+v", e)
	}
	if g.stub.sentTo(g.name, g.rev) != 3 {
		t.Errorf("the run sent %d messages, want 3", g.stub.sentTo(g.name, g.rev))
	}
}

// T11: a failing case holds the candidate with EvalFailed naming it; the
// active revision is unchanged over later passes; a case is never re-sent
// after its outcome is recorded.
func TestAFailingCaseHoldsTheCandidate(t *testing.T) {
	g := newEvalRig(t, "fails", echo("a", "one"), evalCase("b", "two", "nope", ""), echo("c", "unreachable"))
	g.both(t, 3)
	e := g.eval(t)
	if e.Verdict != assaydv1alpha1.EvalVerdictFailed || e.Score != "1/3" {
		t.Fatalf("status.eval is %+v; want Failed, 1/3", e)
	}
	if b := caseOf(e, "b"); b == nil || b.Outcome != assaydv1alpha1.EvalCaseFailed || b.Reason != "Mismatch" {
		t.Errorf("case b is %+v, want Failed/Mismatch", b)
	}
	if c := caseOf(e, "c"); c == nil || c.Outcome != assaydv1alpha1.EvalCaseFailed || c.Reason != "Unreachable" {
		t.Errorf("case c is %+v, want Failed/Unreachable", c)
	}
	if len(e.Failed) != 1 || e.Failed[0].RevisionDigest != g.dgst || e.Failed[0].SuiteDigest != g.suiteDgst {
		t.Errorf("failed[] is %+v, want the pair", e.Failed)
	}
	c := condIs(t, g.a, assaydv1alpha1.CondGatesPassed, metav1.ConditionFalse, controller.ReasonEvalFailed)
	for _, want := range []string{"b (Mismatch)", "c (Unreachable)", "1/3", "threshold 1.000", g.dgst, g.suiteDgst} {
		if c != nil && !strings.Contains(c.Message, want) {
			t.Errorf("EvalFailed does not name %q: %s", want, c.Message)
		}
	}
	g.both(t, 3)
	if got := liveAgent(t, g.a); got.Status.ActiveRevisionDigest != g.active || got.Status.Phase != assaydv1alpha1.PhaseHeld {
		t.Errorf("after the failure the active revision is %s, phase %s", got.Status.ActiveRevisionDigest, got.Status.Phase)
	}
	for _, text := range []string{"one", "two", "unreachable"} {
		if n := g.stub.sent(g.name, g.rev, text); n != 1 {
			t.Errorf("case %q was sent %d times", text, n)
		}
	}
}

// T13: a verdict for a suite digest that no longer matches the suite
// promotes nothing, and a new run starts for the new pair.
func TestAVerdictForAnEditedSuitePromotesNothing(t *testing.T) {
	g := newEvalRig(t, "staledigest", echo("a", "one"))
	g.evalPass(t)
	if e := g.eval(t); e.Verdict != assaydv1alpha1.EvalVerdictPassed {
		t.Fatalf("setup: %+v", e)
	}
	sd2 := putSuite(t, g.ns, "smoke", echo("a", "one"), echo("b", "two"))
	g.agentPass(t)
	if got := liveAgent(t, g.a); got.Status.ActiveRevisionDigest != g.active {
		t.Fatal("a Passed verdict for the suite before its edit promoted the candidate")
	}
	condIs(t, g.a, assaydv1alpha1.CondGatesPassed, metav1.ConditionFalse, controller.ReasonEvalRunning)
	g.evalPass(t)
	e := g.eval(t)
	if e.SuiteDigest != sd2 || e.Verdict != assaydv1alpha1.EvalVerdictRunning {
		t.Fatalf("no run started for the edited suite: %+v", e)
	}
	if g.stub.sent(g.name, g.rev, "one") != 2 {
		t.Errorf("the new run did not send its first case: %d", g.stub.sent(g.name, g.rev, "one"))
	}
}

// T15, with the human's decision of 2026-10-18 (#111): revision A active; B
// fails; the spec reverts to A, which clears B's run record and keeps
// failed[]; B re-applied is sent nothing and reads EvalPairAlreadyFailed. An
// edit of the suite then starts a run, from that run's outcomes only.
func TestAFailedPairIsFinalAndASuiteEditReopensIt(t *testing.T) {
	g := newEvalRig(t, "finality", evalCase("a", "one", "nope", ""))
	g.both(t, 1)
	if e := g.eval(t); e.Verdict != assaydv1alpha1.EvalVerdictFailed {
		t.Fatalf("setup: B did not fail: %+v", e)
	}
	imageA := "ghcr.io/acme/agent@sha256:6d5d9666a268df6f000000000000000000000000000000000000000000000000"
	mustEdit(t, g.a, func(a *assaydv1alpha1.Agent) { a.Spec.Runtime.Image = imageA })
	g.agentPass(t)
	if got := liveAgent(t, g.a); got.Status.ActiveRevisionDigest != g.active || got.Status.CandidateRevisionDigest != "" {
		t.Fatalf("setup: the revert did not settle on A: active %s candidate %s", got.Status.ActiveRevisionDigest,
			got.Status.CandidateRevisionDigest)
	}
	g.evalPass(t)
	e := g.eval(t)
	if e.Verdict != "" || len(e.Cases) != 0 || e.Score != "" || e.At != nil {
		t.Fatalf("the spec moved away from B and its run's record survives: %+v", e)
	}
	if len(e.Failed) != 1 || e.Failed[0].RevisionDigest != g.dgst {
		t.Fatalf("the clear dropped failed[]: %+v", e.Failed)
	}

	mustEdit(t, g.a, func(a *assaydv1alpha1.Agent) { a.Spec.Runtime.Image = imageB })
	for i := 0; i < 3; i++ {
		g.agentPass(t)
		g.evalPass(t)
	}
	if got := liveAgent(t, g.a); got.Status.CandidateRevisionDigest != g.dgst {
		t.Fatalf("setup: B is not the candidate again: %s", got.Status.CandidateRevisionDigest)
	}
	if n := g.stub.sent(g.name, g.rev, "one"); n != 1 {
		t.Fatalf("the failed pair was sent its case again: %d", n)
	}
	condIs(t, g.a, assaydv1alpha1.CondGatesPassed, metav1.ConditionFalse, controller.ReasonEvalPairAlreadyFailed)

	// The suite edit: three cases, one of them a's. The first is held while
	// the record is read.
	sd2 := putSuite(t, g.ns, "smoke", echo("a", "one"), echo("b", "two"), echo("c", "three"))
	arrived, release := g.stub.hold("one")
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = g.e.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(g.a)})
	}()
	waitFor(t, arrived, "the new run's first case")
	e = g.eval(t)
	if e.Verdict != assaydv1alpha1.EvalVerdictRunning || e.SuiteDigest != sd2 || e.Score != "" || e.At != nil {
		t.Errorf("while the new run's first case is in flight status.eval is %+v; want Running for the "+
			"new pair with no score and no at", e)
	}
	release()
	<-done
	g.both(t, 3)
	e = g.eval(t)
	if e.Verdict != assaydv1alpha1.EvalVerdictPassed || e.Score != "3/3" {
		t.Errorf("the new run's verdict is %s %s; want Passed, 3/3, from its own outcomes", e.Verdict, e.Score)
	}
	for _, text := range []string{"two", "three"} {
		if n := g.stub.sent(g.name, g.rev, text); n != 1 {
			t.Errorf("case %q was sent %d times", text, n)
		}
	}
	if n := g.stub.sent(g.name, g.rev, "one"); n != 2 {
		t.Errorf("case a was sent %d times; want once per run", n)
	}
}

// T15's held read, on its own: a run start for a new pair clears the score
// and at the previous pair's verdict wrote, as well as its cases.
func TestARunStartClearsThePreviousPairsScore(t *testing.T) {
	g := newEvalRig(t, "clearscore", evalCase("a", "one", "nope", ""))
	g.evalPass(t)
	if e := g.eval(t); e.Score != "0/1" || e.At == nil {
		t.Fatalf("setup: %+v", e)
	}
	putSuite(t, g.ns, "smoke", echo("a", "one"))
	arrived, release := g.stub.hold("one")
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = g.e.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(g.a)})
	}()
	waitFor(t, arrived, "the new run's case")
	if e := g.eval(t); e.Score != "" || e.At != nil || e.Verdict != assaydv1alpha1.EvalVerdictRunning {
		t.Errorf("the run start left the previous pair's score: %+v", e)
	}
	release()
	<-done
}

func waitFor(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(20 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

// T16: failed[] holds ten pairs; an eleventh failed pair evicts the oldest,
// and the evicted pair can run again while the others cannot.
func TestTheFailedRecordEvictsTheOldestPair(t *testing.T) {
	g := newEvalRig(t, "failcap", evalCase("a", "m0", "nope", ""))
	g.evalPass(t)
	for i := 1; i <= 10; i++ {
		putSuite(t, g.ns, "smoke", evalCase("a", fmt.Sprintf("m%d", i), "nope", ""))
		g.evalPass(t)
	}
	e := g.eval(t)
	if len(e.Failed) != 10 {
		t.Fatalf("failed[] holds %d pairs after eleven failures, want 10", len(e.Failed))
	}
	// m1's suite is still recorded: no run.
	putSuite(t, g.ns, "smoke", evalCase("a", "m1", "nope", ""))
	g.evalPass(t)
	if n := g.stub.sent(g.name, g.rev, "m1"); n != 1 {
		t.Errorf("a recorded failed pair ran again (%d sends)", n)
	}
	// m0's, the oldest, was evicted: it runs again.
	putSuite(t, g.ns, "smoke", evalCase("a", "m0", "nope", ""))
	g.evalPass(t)
	if n := g.stub.sent(g.name, g.rev, "m0"); n != 2 {
		t.Errorf("the evicted pair did not run again (%d sends)", n)
	}
}

// T17: a spec edit after the first of three cases abandons the run, and the
// new candidate starts at its first case.
func TestASpecEditAbandonsTheRun(t *testing.T) {
	g := newEvalRig(t, "abandon", echo("a", "one"), echo("b", "two"), echo("c", "three"))
	g.both(t, 1)
	imageC := "ghcr.io/acme/agent@sha256:c0c0000000000000000000000000000000000000000000000000000000000000"
	revC, digestC := gatedCandidate(t, g.r, g.a, imageC, "smoke")
	if digestC == g.dgst {
		t.Fatal("setup: no new candidate")
	}
	g.both(t, 3)
	for _, text := range []string{"one", "two", "three"} {
		if n := g.stub.sent(g.name, revC, text); n != 1 {
			t.Errorf("the new candidate was sent %q %d times, want once", text, n)
		}
	}
	if g.stub.sent(g.name, g.rev, "two") != 0 {
		t.Error("the abandoned candidate was sent its second case")
	}
	if got := liveAgent(t, g.a); got.Status.ActiveRevisionDigest != digestC {
		t.Errorf("the new candidate's run did not promote it: %+v", got.Status.Eval)
	}
}

// T18: a candidate made unavailable after one of three cases reads
// EvalPaused, naming k of N, and is sent nothing; made available again, it is
// sent the second case and not the first.
func TestAnUnavailableCandidatePausesTheRun(t *testing.T) {
	g := newEvalRig(t, "paused", echo("a", "one"), echo("b", "two"), echo("c", "three"))
	g.both(t, 1)
	markAvailable(t, g.ns, controller.WorkloadName(g.name, g.rev), 0)
	g.agentPass(t)
	c := condIs(t, g.a, assaydv1alpha1.CondGatesPassed, metav1.ConditionFalse, controller.ReasonEvalPaused)
	if c != nil && !strings.Contains(c.Message, "1 of its 3 cases") {
		t.Errorf("EvalPaused does not name k of N: %s", c.Message)
	}
	res := g.evalPass(t)
	g.evalPass(t)
	if g.stub.sentTo(g.name, g.rev) != 1 {
		t.Fatalf("a paused candidate was sent a case: %d sends", g.stub.sentTo(g.name, g.rev))
	}
	if res.RequeueAfter != controller.CardDriftInterval {
		t.Errorf("a paused pass requeued after %v, want CardDriftInterval", res.RequeueAfter)
	}
	markAvailable(t, g.ns, controller.WorkloadName(g.name, g.rev), 1)
	g.both(t, 1)
	if g.stub.sent(g.name, g.rev, "two") != 1 || g.stub.sent(g.name, g.rev, "one") != 1 {
		t.Errorf("after the pause the run sent one=%d two=%d; want the second case, and the first not again",
			g.stub.sent(g.name, g.rev, "one"), g.stub.sent(g.name, g.rev, "two"))
	}
}

// T22: an operator restarted after two of three cases sends only the third.
// The outcomes are in status, not in the process.
func TestARestartedControllerSendsOnlyTheRest(t *testing.T) {
	g := newEvalRig(t, "restart", echo("a", "one"), echo("b", "two"), echo("c", "three"))
	g.both(t, 2)
	g.e = newEvalController(t, g.stub)
	g.both(t, 2)
	for _, text := range []string{"one", "two", "three"} {
		if n := g.stub.sent(g.name, g.rev, text); n != 1 {
			t.Errorf("case %q was sent %d times across the restart", text, n)
		}
	}
	if e := g.eval(t); e.Verdict != assaydv1alpha1.EvalVerdictPassed {
		t.Errorf("the run did not finish: %+v", e)
	}
}

// T23: no case is sent before the candidate's card is recorded; a card
// fetched at run start whose digest differs from the recorded one starts no
// run; a card with no usable interface reads EvalBindingUnsupported.
func TestTheCardPreconditionHoldsTheRun(t *testing.T) {
	g := newEvalRig(t, "cardpre", echo("a", "one"))
	writeAgentStatus(t, g.a, func(st *assaydv1alpha1.AgentStatus) {
		for i := range st.Cards {
			if st.Cards[i].RevisionDigest == g.dgst {
				st.Cards[i].Digest = ""
			}
		}
	})
	g.evalPass(t)
	if g.stub.sentTo(g.name, g.rev) != 0 || g.eval(t) != nil {
		t.Fatalf("a candidate with no recorded card was evaluated: %+v", g.eval(t))
	}
	writeAgentStatus(t, g.a, func(st *assaydv1alpha1.AgentStatus) { putCard(st, g.rev, g.dgst, time.Now()) })
	g.evalPass(t)
	e := g.eval(t)
	if g.stub.sentTo(g.name, g.rev) != 0 || e.Verdict != "" {
		t.Fatalf("a card whose digest differs from the recorded one started a run: %+v", e)
	}
	if e.Blocked == nil || e.Blocked.Reason != controller.ReasonEvalAwaitingCard {
		t.Errorf("blocked is %+v, want EvalAwaitingCard", e.Blocked)
	}
	jsonrpc := strings.Replace(stubCard(g.name), "HTTP+JSON", "JSONRPC", 1)
	g.stub.setCard(g.name, jsonrpc)
	recordServedCard(t, g, jsonrpc)
	g.evalPass(t)
	g.agentPass(t)
	if g.stub.sentTo(g.name, g.rev) != 0 {
		t.Fatal("a card with no HTTP+JSON 1.0 interface was sent a case")
	}
	condIs(t, g.a, assaydv1alpha1.CondGatesPassed, metav1.ConditionFalse, controller.ReasonEvalBindingUnsupported)
}

// recordServedCard records the digest of the card the stub now serves, as
// the Agent reconciler's next fetch would.
func recordServedCard(t *testing.T, g *evalRig, card string) {
	t.Helper()
	writeAgentStatus(t, g.a, func(st *assaydv1alpha1.AgentStatus) {
		putCard(st, g.rev, g.dgst, time.Now())
		for i := range st.Cards {
			if st.Cards[i].RevisionDigest == g.dgst {
				st.Cards[i].Digest = sha256Hex(card)
			}
		}
	})
}

// T24 and T56: the two causes of EvalAwaitingCard, told apart by their
// messages, and the binding refusal, each set by the Agent reconciler from
// status.eval.blocked. A fetch refused at the connection names the error's
// class and no digest; a card that parses and whose digest differs names the
// mismatch. Neither starts a run.
func TestTheCausesOfARunStartRefusalAreToldApart(t *testing.T) {
	g := newEvalRig(t, "twocauses", echo("a", "one"))
	g.stub.refuseCard(g.name, true)
	res := g.evalPass(t)
	g.agentPass(t)
	c := condIs(t, g.a, assaydv1alpha1.CondGatesPassed, metav1.ConditionFalse, controller.ReasonEvalAwaitingCard)
	if c != nil && (!strings.Contains(c.Message, "refused or failed") || strings.Contains(c.Message, "has digest")) {
		t.Errorf("a refused fetch's message: %s", c.Message)
	}
	if res.RequeueAfter != controller.CardRetryInterval {
		t.Errorf("a failed run-start fetch requeued after %v, want CardRetryInterval", res.RequeueAfter)
	}
	g.stub.refuseCard(g.name, false)
	g.stub.setCard(g.name, stubCard(g.name)+" ")
	res = g.evalPass(t)
	g.agentPass(t)
	c = condIs(t, g.a, assaydv1alpha1.CondGatesPassed, metav1.ConditionFalse, controller.ReasonEvalAwaitingCard)
	if c != nil && (!strings.Contains(c.Message, "has digest") || !strings.Contains(c.Message, "records")) {
		t.Errorf("a mismatched card's message does not name the mismatch: %s", c.Message)
	}
	if res.RequeueAfter != controller.CardDriftInterval {
		t.Errorf("a refusal that read a card requeued after %v, want CardDriftInterval", res.RequeueAfter)
	}
	query := strings.Replace(stubCard(g.name), "/a2a/", "/a2a/?tenant=x", 1)
	g.stub.setCard(g.name, query)
	recordServedCard(t, g, query)
	g.evalPass(t)
	g.agentPass(t)
	c = condIs(t, g.a, assaydv1alpha1.CondGatesPassed, metav1.ConditionFalse, controller.ReasonEvalBindingUnsupported)
	if c != nil && !strings.Contains(c.Message, "query or a fragment") {
		t.Errorf("EvalBindingUnsupported does not carry blocked.message: %s", c.Message)
	}
	if g.stub.sentTo(g.name, g.rev) != 0 || g.eval(t).Verdict != "" {
		t.Error("a refused card started a run")
	}
}

// #108: a card whose HTTP+JSON 1.0 interface names a tenant is decoded with
// the tenant, so the refusal is live: EvalBindingUnsupported, and no case.
func TestATenantedInterfaceIsRefused(t *testing.T) {
	g := newEvalRig(t, "tenanted", echo("a", "one"))
	tenanted := strings.Replace(stubCard(g.name), `"protocolVersion":"1.0"`, `"protocolVersion":"1.0","tenant":"t1"`, 1)
	g.stub.setCard(g.name, tenanted)
	recordServedCard(t, g, tenanted)
	g.evalPass(t)
	g.agentPass(t)
	c := condIs(t, g.a, assaydv1alpha1.CondGatesPassed, metav1.ConditionFalse, controller.ReasonEvalBindingUnsupported)
	if c != nil && !strings.Contains(c.Message, "tenant") {
		t.Errorf("the refusal does not name the tenant: %s", c.Message)
	}
	if g.stub.sentTo(g.name, g.rev) != 0 {
		t.Error("a tenanted interface was sent a case")
	}
}

// T52: once a run starts, status.eval.cardDigest is the digest status.cards[]
// recorded for the candidate when the run-start card was accepted, and the
// endpoint is the card's path joined with /message:send.
func TestTheRunRecordsTheCardItTookItsPathFrom(t *testing.T) {
	g := newEvalRig(t, "carddigest", echo("a", "one"), echo("b", "two"))
	var recorded string
	for _, c := range liveAgent(t, g.a).Status.Cards {
		if c.RevisionDigest == g.dgst {
			recorded = c.Digest
		}
	}
	g.evalPass(t)
	e := g.eval(t)
	if recorded == "" || e.CardDigest != recorded || e.CardDigest != sha256Hex(stubCard(g.name)) {
		t.Errorf("cardDigest is %q; status.cards[] recorded %q", e.CardDigest, recorded)
	}
	if e.EndpointPath != "/a2a/message:send" {
		t.Errorf("endpointPath is %q", e.EndpointPath)
	}
	// A later card changes nothing the run recorded.
	g.stub.setCard(g.name, stubCard(g.name)+"  ")
	g.evalPass(t)
	if got := g.eval(t); got.CardDigest != recorded || got.Verdict != assaydv1alpha1.EvalVerdictPassed {
		t.Errorf("a card served after the run start moved its record: %+v", got)
	}
}

// T34: a claim with no outcome, left by a stopped eval controller, is
// recorded Interrupted and never sent (Q11 (a)).
func TestAnOrphanedClaimIsInterrupted(t *testing.T) {
	g := newEvalRig(t, "orphan", echo("a", "one"), echo("b", "two"))
	g.evalPass(t) // the run start and a
	writeAgentStatus(t, g.a, func(st *assaydv1alpha1.AgentStatus) {
		st.Eval.Cases = append(st.Eval.Cases, assaydv1alpha1.EvalCaseRecord{ID: "b",
			Outcome: assaydv1alpha1.EvalCaseSending, Claim: "a-stopped-controller"})
	})
	g.both(t, 3)
	e := g.eval(t)
	b := caseOf(e, "b")
	if b == nil || b.Outcome != assaydv1alpha1.EvalCaseFailed || b.Reason != "Interrupted" || b.Claim != "a-stopped-controller" {
		t.Fatalf("the orphaned claim is %+v; want Failed/Interrupted under its own claim", b)
	}
	if n := g.stub.sent(g.name, g.rev, "two"); n != 0 {
		t.Errorf("an orphaned claim was sent %d times", n)
	}
	if e.Verdict != assaydv1alpha1.EvalVerdictFailed || e.Score != "1/2" {
		t.Errorf("the verdict is %s %s; an Interrupted case counts against the threshold", e.Verdict, e.Score)
	}
}

// T36: a handover. While the candidate holds the case, the test writes
// Interrupted at that case's entry, keeping the nonce, as a new leader would.
// The candidate then answers; the record's first attempt conflicts, its
// re-read finds Interrupted, and it drops.
func TestARecordDropsWhenAHandoverInterruptedTheClaim(t *testing.T) {
	g := newEvalRig(t, "handover", echo("a", "one"), echo("b", "two"))
	arrived, release := g.stub.hold("one")
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = g.e.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(g.a)})
	}()
	waitFor(t, arrived, "the case")
	writeAgentStatus(t, g.a, func(st *assaydv1alpha1.AgentStatus) {
		for i := range st.Eval.Cases {
			if st.Eval.Cases[i].ID == "a" {
				st.Eval.Cases[i].Outcome, st.Eval.Cases[i].Reason = assaydv1alpha1.EvalCaseFailed, "Interrupted"
			}
		}
	})
	release()
	<-done
	a := caseOf(g.eval(t), "a")
	if a == nil || a.Outcome != assaydv1alpha1.EvalCaseFailed || a.Reason != "Interrupted" {
		t.Errorf("the old leader's record overwrote the new leader's Interrupted: %+v", a)
	}
}

// T59: a suite reordered mid-run keeps its digest and its pair, and the next
// case is picked by id: each case is sent exactly once, and the verdict counts
// all three.
func TestASuiteReorderedMidRunSendsEachCaseOnce(t *testing.T) {
	g := newEvalRig(t, "reorder", echo("a", "one"), echo("b", "two"), echo("c", "three"))
	g.evalPass(t)
	if sd := putSuite(t, g.ns, "smoke", echo("c", "three"), echo("b", "two"), echo("a", "one")); sd != g.suiteDgst {
		t.Fatal("setup: reordering the cases changed the digest")
	}
	g.both(t, 3)
	for _, text := range []string{"one", "two", "three"} {
		if n := g.stub.sent(g.name, g.rev, text); n != 1 {
			t.Errorf("case %q was sent %d times", text, n)
		}
	}
	if e := g.eval(t); e.Verdict != assaydv1alpha1.EvalVerdictPassed || e.Score != "3/3" {
		t.Errorf("the verdict is %s %s; want Passed, 3/3", e.Verdict, e.Score)
	}
}

// T64: generation lag. With no Agent reconciler pass after a spec edit, the
// old candidate stays recorded, and the eval controller sends it nothing.
func TestTheEvalWaitsForTheNewGenerationToBeObserved(t *testing.T) {
	g := newEvalRig(t, "genlag", echo("a", "one"))
	mustEdit(t, g.a, func(a *assaydv1alpha1.Agent) {
		a.Spec.Runtime.Image = "ghcr.io/acme/agent@sha256:d0d0000000000000000000000000000000000000000000000000000000000000"
	})
	if got := liveAgent(t, g.a); got.Status.CandidateRevisionDigest != g.dgst || got.Status.ObservedGeneration == got.Generation {
		t.Fatal("setup: the edit is already observed")
	}
	for i := 0; i < 3; i++ {
		g.evalPass(t)
	}
	if n := g.stub.sentTo(g.name, g.rev); n != 0 {
		t.Errorf("a candidate the spec has superseded was sent %d cases", n)
	}
}

// T19: a candidate under an I1 hold is sent no case, and reads
// EvalHeldForAuth. Its card is recorded from the candidate itself, so the
// hold is the only thing stopping the run.
func TestACandidateUnderTheI1HoldIsSentNothing(t *testing.T) {
	a, r, _ := servedAPIKeyAgent(t, "i1eval")
	ns := a.Namespace
	r.EvalSuiteInstalled = func() bool { return true }
	stub := newA2AStub()
	r.CardClient = stub
	putSuite(t, ns, "smoke", echo("a", "one"))
	mustEdit(t, a, func(a *assaydv1alpha1.Agent) {
		a.Spec.Expose = &assaydv1alpha1.ExposeSpec{A2A: &assaydv1alpha1.ExposeProtocol{Auth: "oauth"}}
		a.Spec.Gates = []assaydv1alpha1.GateRef{{EvalSuiteRef: "smoke"}}
	})
	spec := liveAgent(t, a).Spec
	second, digest := revision.MustHash(spec), revision.MustDigest(spec)
	reconcileOnce(t, r, a)
	markAvailable(t, ns, controller.WorkloadName("i1eval", second), 1)
	reconcileOnce(t, r, a)
	reconcileOnce(t, r, a)
	if !cardRecorded(&liveAgent(t, a).Status, digest) {
		t.Fatal("setup: the held candidate's card was not recorded")
	}
	e, err := controller.NewEvalReconciler(k8s, k8s, func() bool { return true }, r.Gateway)
	if err != nil {
		t.Fatal(err)
	}
	e.A2A.HTTP = stub
	for i := 0; i < 3; i++ {
		evalOnce(t, e, a)
		reconcileOnce(t, r, a)
	}
	if n := stub.sentTo("i1eval", second); n != 0 {
		t.Errorf("a candidate under the I1 hold was sent %d cases", n)
	}
	condIs(t, a, assaydv1alpha1.CondGatesPassed, metav1.ConditionFalse, controller.ReasonEvalHeldForAuth)
}

// sha256Hex is the card digest the operator records: hex SHA-256 of the
// bytes served.
func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// T20: a new gated Agent has no HTTPRoute while its first revision runs or
// fails: design 03 enters no -auth transaction and collects the Agent's routes
// while no revision serves. One whose first revision passes is promoted on a
// pass, and only then gets Create's prepared route, with no backendRefs.
func TestANewGatedAgentHasNoRouteUntilItsFirstRevisionPromotes(t *testing.T) {
	ns := newNamespace(t)
	putSuite(t, ns, "failing", evalCase("a", "one", "nope", ""))
	putSuite(t, ns, "passing", echo("a", "one"))
	stub := newA2AStub()
	r, _ := createReconciler()
	r.EvalSuiteInstalled = func() bool { return true }
	r.CardClient = stub
	e := newEvalController(t, stub)
	firstRun := func(name, suiteName string) (*assaydv1alpha1.Agent, string) {
		a := mustCreateAgent(t, ns, name, func(a *assaydv1alpha1.Agent) {
			a.Spec.Gates = []assaydv1alpha1.GateRef{{EvalSuiteRef: suiteName}}
		})
		rev := revision.MustHash(a.Spec)
		reconcileOnce(t, r, a)
		reconcileOnce(t, r, a)
		markAvailable(t, ns, controller.WorkloadName(name, rev), 1)
		reconcileOnce(t, r, a)
		return a, rev
	}

	failing, rev := firstRun("firstfails", "failing")
	arrived, release := stub.hold("one")
	done := make(chan struct{})
	go func() { defer close(done); evalOnce(t, e, failing) }()
	waitFor(t, arrived, "the first revision's case")
	if servingRoute(t, ns, "firstfails") != nil {
		t.Error("a new gated Agent has a route while its first revision runs")
	}
	release()
	<-done
	for i := 0; i < 3; i++ {
		reconcileOnce(t, r, failing)
	}
	if got := liveAgent(t, failing); got.Status.ActiveRevision != "" || stub.sentTo("firstfails", rev) != 1 {
		t.Fatalf("setup: the failing first revision: active %q, %d sends", got.Status.ActiveRevision,
			stub.sentTo("firstfails", rev))
	}
	if servingRoute(t, ns, "firstfails") != nil {
		t.Error("a new gated Agent whose first revision failed has a route")
	}

	passing, rev := firstRun("firstpasses", "passing")
	if servingRoute(t, ns, "firstpasses") != nil {
		t.Error("a new gated Agent has a route before its first revision is evaluated")
	}
	evalOnce(t, e, passing)
	reconcileOnce(t, r, passing)
	if got := liveAgent(t, passing).Status.ActiveRevision; got != rev {
		t.Fatalf("the passing first revision was not promoted (%q)", got)
	}
	reconcileOnce(t, r, passing)
	rt := servingRoute(t, ns, "firstpasses")
	if rt == nil {
		t.Fatal("the promoted first revision got no route from Create")
	}
	for _, rule := range rt.Spec.Rules {
		if len(rule.BackendRefs) != 0 {
			t.Errorf("Create's route carries backendRefs before its 401: %+v", rule.BackendRefs)
		}
	}
}

// pruneClaimFromAgentCRD replaces the live Agent CRD with this release's
// minus status.eval.cases[].claim, and puts the release's back when the test
// ends: an install whose CRD keeps every field but the claim's nonce.
func pruneClaimFromAgentCRD(t *testing.T, a *assaydv1alpha1.Agent) {
	t.Helper()
	ctx := context.Background()
	var installed apiextensionsv1.CustomResourceDefinition
	if err := k8s.Get(ctx, client.ObjectKey{Name: "agents.assayd.dev"}, &installed); err != nil {
		t.Fatal(err)
	}
	current := installed.Spec.DeepCopy()
	stale := current.DeepCopy()
	for i := range stale.Versions {
		root := stale.Versions[i].Schema.OpenAPIV3Schema
		st := root.Properties["status"]
		ev := st.Properties["eval"]
		cases := ev.Properties["cases"]
		item := *cases.Items.Schema
		delete(item.Properties, "claim")
		cases.Items.Schema = &item
		ev.Properties["cases"] = cases
		st.Properties["eval"] = ev
		root.Properties["status"] = st
	}
	write := func(spec *apiextensionsv1.CustomResourceDefinitionSpec) {
		if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			var c apiextensionsv1.CustomResourceDefinition
			if err := k8s.Get(ctx, client.ObjectKey{Name: "agents.assayd.dev"}, &c); err != nil {
				return err
			}
			c.Spec = *spec.DeepCopy()
			return k8s.Update(ctx, &c)
		}); err != nil {
			t.Fatalf("install the Agent CRD: %v", err)
		}
	}
	claimKept := func() bool {
		probe := liveAgent(t, a)
		probe.Status.Eval = &assaydv1alpha1.EvalStatus{Cases: []assaydv1alpha1.EvalCaseRecord{{ID: "x", Claim: "probe"}}}
		if err := k8s.Status().Update(ctx, probe, client.DryRunAll); err != nil {
			return false
		}
		return len(probe.Status.Eval.Cases) == 1 && probe.Status.Eval.Cases[0].Claim == "probe"
	}
	t.Cleanup(func() {
		write(current)
		eventually(t, "the release's Agent CRD to keep the claim again", claimKept)
	})
	write(stale)
	eventually(t, "the CRD that prunes the claim to be served", func() bool { return !claimKept() })
}

// Step 11: a claim is read back from the object its patch returns. On a CRD
// that keeps the run's record but prunes the claim's nonce, the claim does not
// come back, and nothing is sent: a request follows only a claim that came
// back. The next pass finds an entry with no outcome and records it
// Interrupted, which fails closed and never sends it.
func TestAClaimThatDoesNotComeBackIsNotSent(t *testing.T) {
	g := newEvalRig(t, "claimpruned", echo("a", "one"))
	pruneClaimFromAgentCRD(t, g.a)
	res := g.evalPass(t)
	if n := g.stub.sentTo(g.name, g.rev); n != 0 {
		t.Fatalf("a claim the CRD pruned was followed by %d sends", n)
	}
	if res.RequeueAfter != controller.CardDriftInterval {
		t.Errorf("a failed claim read-back requeued after %v, want CardDriftInterval", res.RequeueAfter)
	}
	g.evalPass(t)
	g.evalPass(t)
	if n := g.stub.sentTo(g.name, g.rev); n != 0 {
		t.Errorf("a claim whose nonce was pruned was sent %d times on later passes", n)
	}
}

// Step 11: a blocked write is read back. On a CRD that prunes blocked alone,
// a run-start refusal does not come back, and the pass returns the read-back's
// CardDriftInterval rather than the failed fetch's CardRetryInterval.
func TestABlockedWriteThatDoesNotComeBackIsReadBack(t *testing.T) {
	g := newEvalRig(t, "blockedpruned", echo("a", "one"))
	installAgentCRDWithoutEvalFields(t, g.a, "blocked")
	eventually(t, "the CRD that prunes blocked to be served", func() bool {
		probe := liveAgent(t, g.a)
		probe.Status.Eval = &assaydv1alpha1.EvalStatus{Blocked: &assaydv1alpha1.EvalBlocked{Reason: "probe"}}
		if err := k8s.Status().Update(context.Background(), probe, client.DryRunAll); err != nil {
			return false
		}
		return probe.Status.Eval == nil || probe.Status.Eval.Blocked == nil
	})
	g.stub.refuseCard(g.name, true)
	res := g.evalPass(t)
	if res.RequeueAfter != controller.CardDriftInterval {
		t.Errorf("a refusal whose blocked was pruned requeued after %v, want the read-back's "+
			"CardDriftInterval", res.RequeueAfter)
	}
	if g.stub.sentTo(g.name, g.rev) != 0 {
		t.Error("a pass whose blocked write did not come back sent a case")
	}
}

// Step 11: the run-start record is read back. On a CRD that keeps every
// field of the run but endpointPath, the run-start write does not come back
// whole, and nothing is sent at all: without the read-back, the pass would
// claim (cases[] is kept) and send to a path the CRD pruned.
func TestARunStartRecordThatDoesNotComeBackSendsNothing(t *testing.T) {
	g := newEvalRig(t, "startpruned", echo("a", "one"))
	installAgentCRDWithoutEvalFields(t, g.a, "endpointPath")
	eventually(t, "the CRD that prunes endpointPath to be served", func() bool {
		probe := liveAgent(t, g.a)
		probe.Status.Eval = &assaydv1alpha1.EvalStatus{EndpointPath: "/probe"}
		if err := k8s.Status().Update(context.Background(), probe, client.DryRunAll); err != nil {
			return false
		}
		return probe.Status.Eval == nil || probe.Status.Eval.EndpointPath == ""
	})
	res := g.evalPass(t)
	g.evalPass(t)
	if n := g.stub.postsTo(g.name); n != 0 {
		t.Errorf("a run whose record did not come back sent %d requests", n)
	}
	if res.RequeueAfter != controller.CardDriftInterval {
		t.Errorf("a failed read-back requeued after %v, want CardDriftInterval", res.RequeueAfter)
	}
}

// #111 clears a failed revision's record only. A Passed record is kept
// across a candidate that is applied and abandoned: (i) the revision serving
// on it still reads EvalPassed once the spec returns to it, never
// ActiveRevisionNotEvaluated about a revision that passed.
func TestAServingRevisionKeepsItsPassedRecordAcrossAnAbandonedCandidate(t *testing.T) {
	g := newEvalRig(t, "keeppassed", echo("a", "one"))
	g.both(t, 1)
	if got := liveAgent(t, g.a); got.Status.ActiveRevisionDigest != g.dgst {
		t.Fatalf("setup: B was not promoted: %+v", got.Status.Eval)
	}
	condIs(t, g.a, assaydv1alpha1.CondGatesPassed, metav1.ConditionTrue, controller.ReasonEvalPassed)

	imageC := "ghcr.io/acme/agent@sha256:c1c1000000000000000000000000000000000000000000000000000000000000"
	mustEdit(t, g.a, func(a *assaydv1alpha1.Agent) { a.Spec.Runtime.Image = imageC })
	g.agentPass(t)
	g.agentPass(t)
	if got := liveAgent(t, g.a); got.Status.CandidateRevisionDigest == "" ||
		got.Status.CandidateRevisionDigest == g.dgst {
		t.Fatalf("setup: no candidate C: %+v", got.Status)
	}
	g.evalPass(t)
	g.evalPass(t)

	mustEdit(t, g.a, func(a *assaydv1alpha1.Agent) { a.Spec.Runtime.Image = imageB })
	g.agentPass(t)
	g.evalPass(t)
	g.agentPass(t)
	if e := g.eval(t); e.Verdict != assaydv1alpha1.EvalVerdictPassed || e.RevisionDigest != g.dgst {
		t.Errorf("B's Passed record did not survive an abandoned candidate: %+v", e)
	}
	condIs(t, g.a, assaydv1alpha1.CondGatesPassed, metav1.ConditionTrue, controller.ReasonEvalPassed)
}

// (ii) A candidate that passed and is not yet promoted keeps its record
// across a spec edit away from it and back: it is not sent its cases again,
// and it promotes on the verdict it earned.
func TestAPassedCandidateIsNotSentItsCasesAgainAfterAnEditAndBack(t *testing.T) {
	g := newEvalRig(t, "passedback", echo("a", "one"))
	g.evalPass(t)
	if e := g.eval(t); e.Verdict != assaydv1alpha1.EvalVerdictPassed {
		t.Fatalf("setup: %+v", e)
	}
	imageC := "ghcr.io/acme/agent@sha256:c2c2000000000000000000000000000000000000000000000000000000000000"
	mustEdit(t, g.a, func(a *assaydv1alpha1.Agent) { a.Spec.Runtime.Image = imageC })
	g.agentPass(t)
	g.agentPass(t)
	g.evalPass(t)
	mustEdit(t, g.a, func(a *assaydv1alpha1.Agent) { a.Spec.Runtime.Image = imageB })
	for i := 0; i < 2; i++ {
		g.agentPass(t)
		g.evalPass(t)
	}
	if n := g.stub.sent(g.name, g.rev, "one"); n != 1 {
		t.Errorf("a candidate that had passed was sent its case %d times", n)
	}
	if got := liveAgent(t, g.a); got.Status.ActiveRevisionDigest != g.dgst {
		t.Errorf("B did not promote on the verdict it earned: active %s, eval %+v",
			got.Status.ActiveRevisionDigest, got.Status.Eval)
	}
}
