// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package envtest

import (
	"context"

	appsv1 "k8s.io/api/apps/v1"
	"sync"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlconfig "sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/controller"
	"github.com/Quinyte/assayd/internal/revision"
)

// The eval controller's rows that need what only a manager gives: its
// requeues honoured, its two workers, its own status writes driving the next
// pass, and a shutdown.

// newTestManager is a manager scoped to this test's namespaces, with no
// listeners and no leader election.
func newTestManager(t *testing.T) manager.Manager {
	t.Helper()
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme, Metrics: metricsserver.Options{BindAddress: "0"}, HealthProbeBindAddress: "0",
		Controller: ctrlconfig.Controller{SkipNameValidation: ptrTo(true)},
		Cache:      testCache(t),
	})
	if err != nil {
		t.Fatalf("build manager: %v", err)
	}
	return mgr
}

// addEvalController registers an eval controller built as the binary builds
// it, from the manager's client and uncached reader.
func addEvalController(t *testing.T, mgr manager.Manager, stub *a2aStub, installed func() bool,
	edit func(*controller.EvalReconciler)) *controller.EvalReconciler {
	t.Helper()
	e, err := controller.NewEvalReconciler(mgr.GetClient(), mgr.GetAPIReader(), installed, controller.GatewayConfig{})
	if err != nil {
		t.Fatal(err)
	}
	e.A2A.HTTP = stub
	e.CardFetchTimeout = time.Second
	if edit != nil {
		edit(e)
	}
	if err := e.SetupWithManager(mgr); err != nil {
		t.Fatalf("register the eval controller: %v", err)
	}
	return e
}

// startStoppable starts mgr and returns a stop that cancels it and waits for
// Start to return, as a graceful shutdown does.
func startStoppable(t *testing.T, mgr manager.Manager) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- mgr.Start(ctx) }()
	var once sync.Once
	stop = func() {
		once.Do(func() {
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Errorf("manager exited with error: %v", err)
				}
			case <-time.After(60 * time.Second):
				t.Error("manager did not shut down within 60s")
			}
		})
	}
	t.Cleanup(stop)
	if !mgr.GetCache().WaitForCacheSync(ctx) {
		t.Fatal("cache never synced")
	}
	return stop
}

// T26: a suite edited under a held candidate with a final verdict starts a
// new run within one requeue of the eval controller's field, with no Agent
// event: nothing watches suites, and the requeue on a final verdict is the
// only thing that notices the edit.
func TestASuiteEditUnderAHeldCandidateIsNoticedOnTheRequeue(t *testing.T) {
	g := newEvalRig(t, "suiteedit", evalCase("a", "one", "nope", ""))
	g.evalPass(t)
	if e := g.eval(t); e.Verdict != assaydv1alpha1.EvalVerdictFailed {
		t.Fatalf("setup: %+v", e)
	}
	mgr := newTestManager(t)
	addEvalController(t, mgr, g.stub, func() bool { return true },
		func(e *controller.EvalReconciler) { e.RequeueAfter = time.Second })
	startStoppable(t, mgr)
	time.Sleep(2 * time.Second) // the manager's first pass finds the verdict final
	sd2 := putSuite(t, g.ns, "smoke", echo("a", "one"))
	eventually(t, "a run for the edited suite", func() bool {
		e := g.eval(t)
		return e.SuiteDigest == sd2 && e.Verdict == assaydv1alpha1.EvalVerdictPassed
	})
}

// T54: the read-back, on its own. The eval controller runs alone, against an
// Agent CRD that prunes the new status.eval fields from every write, as a
// stale install does. It sends no case; it fetches the card once per requeue,
// set here to 2 s — at least 3 and at most 6 times in 10 s — and after its
// first run-start write the Agent's resourceVersion does not change.
func TestTheReadBackStopsARunOnACRDThatPrunesIt(t *testing.T) {
	g := newEvalRig(t, "readback", echo("a", "one"))
	installAgentCRDWithoutEvalFields(t, g.a, "suiteDigest", "endpointPath", "cardDigest", "blocked",
		"blockedFor", "verdict", "cases", "failed")
	eventually(t, "the pruning CRD to be served", func() bool { return !evalRecordKept(t, g.a) })
	before := g.stub.cardFetches(g.name)

	mgr := newTestManager(t)
	addEvalController(t, mgr, g.stub, func() bool { return true },
		func(e *controller.EvalReconciler) { e.RequeueAfter = 2 * time.Second })
	started := time.Now()
	startStoppable(t, mgr)
	eventually(t, "the first run-start write", func() bool {
		e := liveAgent(t, g.a).Status.Eval
		return e != nil && e.RevisionDigest == g.dgst && g.stub.cardFetches(g.name) > before
	})
	rv := liveAgent(t, g.a).ResourceVersion
	// Counted from the manager's start, so the rate-limited retries of
	// Requeue: true — 5 ms, then doubling — fall inside the window.
	time.Sleep(time.Until(started.Add(10 * time.Second)))
	n := g.stub.cardFetches(g.name) - before
	if n < 3 || n > 6 {
		t.Errorf("the eval controller fetched the card %d times in 10 s with a 2 s requeue; want 3 to 6", n)
	}
	if got := g.stub.sentTo(g.name, g.rev); got != 0 {
		t.Errorf("a run whose record the CRD prunes sent %d cases", got)
	}
	if got := liveAgent(t, g.a).ResourceVersion; got != rv {
		t.Errorf("the Agent's resourceVersion moved from %s to %s: a repeated run-start write changed "+
			"something", rv, got)
	}
}

// statusSpy records every status Update the Agent reconciler makes while
// status.eval records a run with at least one outcome.
type statusSpy struct {
	client.Client
	mu      sync.Mutex
	running int
}

type statusSpyWriter struct {
	client.SubResourceWriter
	spy *statusSpy
}

func (s *statusSpy) Status() client.SubResourceWriter {
	return &statusSpyWriter{s.Client.Status(), s}
}

func (w *statusSpyWriter) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	if a, ok := obj.(*assaydv1alpha1.Agent); ok && a.Status.Eval != nil &&
		a.Status.Eval.Verdict == assaydv1alpha1.EvalVerdictRunning && len(a.Status.Eval.Cases) > 0 {
		w.spy.mu.Lock()
		w.spy.running++
		w.spy.mu.Unlock()
	}
	return w.SubResourceWriter.Update(ctx, obj, opts...)
}

func (s *statusSpy) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

// T31 and T33: two writers, and the hit count. With the Agent reconciler
// running beside the eval controller in one manager, every case of a 5-case
// run is sent exactly once and recorded, though each eval patch races an
// Agent reconciler pass; and the Agent reconciler writes no status while the
// run records its cases, because EvalRunning does not change as they do.
func TestEveryCaseIsSentOnceBesideTheAgentReconciler(t *testing.T) {
	ns := newNamespace(t)
	cases := []assaydv1alpha1.EvalCase{echo("a", "one"), echo("b", "two"), echo("c", "three"),
		echo("d", "four"), echo("e", "five")}
	putSuite(t, ns, "smoke", cases...)
	stub := newA2AStub()
	// Each answer takes 300 ms, so the Agent reconciler's passes, driven by
	// the eval controller's own patches, land between and during cases
	// rather than after a run that finished first.
	stub.delay = 300 * time.Millisecond
	mgr := newTestManager(t)
	spy := &statusSpy{Client: mgr.GetClient()}
	r, err := controller.NewAgentReconciler(spy, mgr.GetAPIReader(), mgr.GetScheme(), operatorNamespace,
		func() bool { return true }, labelAuthorityPresent, controller.InjectedEnvConfig{}, controller.GatewayConfig{})
	if err != nil {
		t.Fatal(err)
	}
	r.CardClient = stub
	r.CardFetchTimeout = time.Second
	if err := r.SetupWithManager(mgr); err != nil {
		t.Fatal(err)
	}
	addEvalController(t, mgr, stub, func() bool { return true }, nil)
	startStoppable(t, mgr)

	a := mustCreateAgent(t, ns, "hitcount", nil)
	first := revision.MustHash(a.Spec)
	eventually(t, "the first workload", func() bool { return deploymentExists(t, ns, controller.WorkloadName("hitcount", first)) })
	markAvailable(t, ns, controller.WorkloadName("hitcount", first), 1)
	eventually(t, "the first revision to serve", func() bool { return liveAgent(t, a).Status.ActiveRevision == first })

	mustEdit(t, a, func(a *assaydv1alpha1.Agent) {
		a.Spec.Runtime.Image = imageB
		a.Spec.Gates = []assaydv1alpha1.GateRef{{EvalSuiteRef: "smoke"}}
	})
	second := revision.MustHash(liveAgent(t, a).Spec)
	eventually(t, "the candidate's workload", func() bool { return deploymentExists(t, ns, controller.WorkloadName("hitcount", second)) })
	markAvailable(t, ns, controller.WorkloadName("hitcount", second), 1)
	eventually(t, "the candidate to be evaluated and promoted", func() bool {
		return liveAgent(t, a).Status.ActiveRevision == second
	})
	for _, c := range cases {
		if n := stub.sent("hitcount", second, c.Message); n != 1 {
			t.Errorf("case %q was sent %d times", c.Message, n)
		}
	}
	e := liveAgent(t, a).Status.Eval
	if e.Verdict != assaydv1alpha1.EvalVerdictPassed || e.Score != "5/5" || len(e.Cases) != 5 {
		t.Errorf("status.eval is %+v; want Passed, 5/5, five recorded cases", e)
	}
	if n := spy.count(); n != 0 {
		t.Errorf("the Agent reconciler wrote status %d times while the run recorded its cases; each "+
			"eval patch that only records a case must leave its write a no-op", n)
	}
}

func deploymentExists(t *testing.T, ns, name string) bool {
	t.Helper()
	var d appsv1.Deployment
	return k8s.Get(context.Background(), types.NamespacedName{Namespace: runNS(ns), Name: name}, &d) == nil
}

// T35: graceful shutdown. While the stub holds the first Agent's case, a
// hook holds the second Agent's pass after its live read and before its
// ctx.Err() check. The manager is stopped, then the hook released. The first
// case records the stub's answer; the second Agent writes no claim; and the
// next manager claims the next case and never re-sends the first.
func TestAGracefulStopFinishesTheCaseInFlightAndClaimsNoMore(t *testing.T) {
	ns := newNamespace(t)
	putSuite(t, ns, "smoke", echo("a", "one"), echo("b", "two"))
	stub := newA2AStub()
	rigs := map[string]*evalRig{}
	for _, name := range []string{"inflight", "heldpass"} {
		a, r := servedUngated(t, ns, name)
		r.CardClient = stub
		rig := &evalRig{ns: ns, name: name, a: a, r: r, stub: stub}
		rig.rev, rig.dgst = gatedCandidate(t, r, a, imageB, "smoke")
		rigs[name] = rig
	}
	inflight, heldpass := rigs["inflight"], rigs["heldpass"]

	arrived, releaseCase := stub.hold("one")
	reached, releaseHook := make(chan struct{}), make(chan struct{})
	var reachOnce sync.Once
	heldKey := client.ObjectKeyFromObject(heldpass.a)
	mgr := newTestManager(t)
	addEvalController(t, mgr, stub, func() bool { return true }, func(e *controller.EvalReconciler) {
		e.BeforeClaimCheck = func(key types.NamespacedName) {
			if key == heldKey {
				reachOnce.Do(func() {
					close(reached)
					<-releaseHook
				})
			}
		}
	})
	stop := startStoppable(t, mgr)
	waitFor(t, arrived, "the first Agent's case")
	waitFor(t, reached, "the second Agent's pass at the hook")

	stopped := make(chan struct{})
	go func() { defer close(stopped); stop() }()
	time.Sleep(500 * time.Millisecond) // the cancel lands
	close(releaseHook)
	time.Sleep(2 * time.Second)
	releaseCase()
	<-stopped

	a := caseOf(liveAgent(t, inflight.a).Status.Eval, "a")
	if a == nil || a.Outcome != assaydv1alpha1.EvalCasePassed {
		t.Fatalf("the case in flight at shutdown recorded %+v; want the stub's answer, Passed", a)
	}
	if e := liveAgent(t, heldpass.a).Status.Eval; e != nil && len(e.Cases) != 0 {
		t.Errorf("a pass held before its ctx.Err() check claimed after shutdown began: %+v", e.Cases)
	}

	mgr2 := newTestManager(t)
	addEvalController(t, mgr2, stub, func() bool { return true }, nil)
	startStoppable(t, mgr2)
	eventually(t, "the next manager to send the first Agent's second case", func() bool {
		return stub.sent("inflight", inflight.rev, "two") == 1
	})
	if n := stub.sent("inflight", inflight.rev, "one"); n != 1 {
		t.Errorf("the first case was sent %d times across the shutdown", n)
	}
}

// T27: the EvalSuite CRD installed after the manager starts. The manager
// starts without it, so nothing in the eval controller watches suites; an
// Agent's gate is skipped while the CRD is absent and holds once discovery
// sees it, with no restart. A static watch on EvalSuites does not fail the
// manager at start on this controller-runtime: its source waits for the kind
// until the controller's CacheSyncTimeout, and only then fails the manager.
// So the test shortens that timeout to 2 s and keeps the CRD absent for
// longer, and the manager must still be serving afterwards.
func TestTheEvalSuiteCRDInstalledAfterStartGatesTheNextRevision(t *testing.T) {
	ensureEvalSuiteAbsent(t)
	t.Cleanup(func() { restoreGeneratedEvalSuiteCRD(t) })
	detector, err := controller.NewEvalSuiteDetector(cfg, 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	installed := func() bool { return detector.Installed() }
	ns := newNamespace(t)
	stub := newA2AStub()
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme, Metrics: metricsserver.Options{BindAddress: "0"}, HealthProbeBindAddress: "0",
		Controller: ctrlconfig.Controller{SkipNameValidation: ptrTo(true), CacheSyncTimeout: 2 * time.Second},
		Cache:      testCache(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	r, err := controller.NewAgentReconciler(mgr.GetClient(), mgr.GetAPIReader(), mgr.GetScheme(), operatorNamespace,
		installed, labelAuthorityPresent, controller.InjectedEnvConfig{}, controller.GatewayConfig{})
	if err != nil {
		t.Fatal(err)
	}
	r.CardClient = stub
	if err := r.SetupWithManager(mgr); err != nil {
		t.Fatal(err)
	}
	addEvalController(t, mgr, stub, installed, nil)
	startStoppable(t, mgr)

	a := mustCreateAgent(t, ns, "latecrd", func(a *assaydv1alpha1.Agent) {
		a.Spec.Gates = []assaydv1alpha1.GateRef{{EvalSuiteRef: "smoke"}}
	})
	first := revision.MustHash(a.Spec)
	eventually(t, "the first workload", func() bool { return deploymentExists(t, ns, controller.WorkloadName("latecrd", first)) })
	markAvailable(t, ns, controller.WorkloadName("latecrd", first), 1)
	eventually(t, "the ungated promotion while the CRD is absent", func() bool {
		return liveAgent(t, a).Status.ActiveRevision == first
	})
	condIs(t, a, assaydv1alpha1.CondGatesSkipped, metav1.ConditionTrue, "EvalSuiteCRDAbsent")
	time.Sleep(4 * time.Second) // past the controllers' CacheSyncTimeout, with the CRD still absent

	installGeneratedEvalSuiteCRD(t)
	putSuite(t, ns, "smoke", evalCase("a", "one", "nope", ""))
	mustEdit(t, a, func(a *assaydv1alpha1.Agent) { a.Spec.Runtime.Image = imageB })
	second := revision.MustHash(liveAgent(t, a).Spec)
	eventually(t, "the candidate's workload", func() bool { return deploymentExists(t, ns, controller.WorkloadName("latecrd", second)) })
	markAvailable(t, ns, controller.WorkloadName("latecrd", second), 1)
	eventually(t, "the candidate to be evaluated and held", func() bool {
		c := condition(liveAgent(t, a), assaydv1alpha1.CondGatesPassed)
		return c != nil && c.Reason == controller.ReasonEvalFailed
	})
	if got := liveAgent(t, a).Status.ActiveRevision; got != first {
		t.Errorf("a gated candidate that failed was promoted once the CRD appeared (%s)", got)
	}
}
