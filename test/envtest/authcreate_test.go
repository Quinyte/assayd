// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package envtest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/compiler"
	"github.com/Quinyte/assayd/internal/controller"
	"github.com/Quinyte/assayd/internal/revision"
)

// Design 03 §8.1's first-slice envtest cases for `Create` (cases 1, 2, 3, 4,
// 5, 9, 12 and 14), with the probe answered by the pinned oracle §8.1 states.
// There is no gateway here, so the test writes route and policy status, and
// what is under test is the operator's reaction to them.

// testServingURL is --gateway-serving-url in envtest. Nothing listens there:
// the probe goes to stubProber, which reads the path off it.
const testServingURL = "http://gateway.test:8080"

// stubProber is §8.1's pinned oracle. It answers `401`, at any path, ONLY
// while the stored `<agent>-auth` equals that Agent's golden policy AND the
// policy is "taken". Otherwise it answers as the route would with no policy:
// `500` on a route with no backendRefs, and on a route with them `200` at the
// Agent's card path and `404` anywhere else; `404` with no route at all.
// "Taken" is a switch the test holds: released, a stored policy is taken at
// once. Without the switch a stub that answered 401 whenever a policy existed
// could not express a gateway that has not read the policy yet, and a stub
// that always answered 401 would let a case reach Served with no policy
// written (reviews/03-a56-critique.md MINOR 1).
//
// On a route with backendRefs the card path answers `200` with the card of
// the backend the route's one backendRef names, whose digest is
// cardDigestOf(that backend). Two switches the test holds change that
// answer, as §8.1 says: a backend override replaces the code (case 11's `503`
// and a backend's own `401`), and servedBy makes another backend serve the
// card, which is how a gateway looks just after a promotion.
type stubProber struct {
	mu       sync.Mutex
	held     map[string]bool
	probes   map[string]int
	last     map[string]int
	override map[string]int
	servedBy map[string]string
	// foreign makes a policy the operator did not emit answer 401 at any
	// path, as a foreign traffic policy on the route would (§3.2).
	foreign map[string]bool
}

func newStubProber() *stubProber {
	return &stubProber{held: map[string]bool{}, probes: map[string]int{}, last: map[string]int{},
		override: map[string]int{}, servedBy: map[string]string{}, foreign: map[string]bool{}}
}

// foreignAnswers makes a foreign policy answer every probe of agent with 401.
func (s *stubProber) foreignAnswers(agent string, on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.foreign[agent] = on
}

// backendOverride makes the card path on a published route answer code, or,
// with 0, the card again.
func (s *stubProber) backendOverride(agent string, code int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.override[agent] = code
}

// serveFrom makes the card path answer with backend's card whatever the route
// names, or, with "", with the named backend's again.
func (s *stubProber) serveFrom(agent, backend string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.servedBy[agent] = backend
}

// cardDigestOf is the digest of the card a backend serves in this stub, and
// the one a test records in status.cards for the revision it names.
func cardDigestOf(backend string) string {
	sum := sha256.Sum256([]byte("the card " + backend + " serves"))
	return hex.EncodeToString(sum[:])
}

func (s *stubProber) hold(agent string, held bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.held[agent] = held
}

func (s *stubProber) count(agent string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.probes[agent]
}

func (s *stubProber) Probe(ctx context.Context, req controller.AuthProbeRequest) (controller.AuthProbeAnswer, error) {
	parts := strings.SplitN(req.Host, ".", 3)
	if len(parts) != 3 {
		return controller.AuthProbeAnswer{}, errors.New("the stub cannot read an Agent off host " + req.Host)
	}
	name, ns := parts[0], parts[1]
	u, err := url.Parse(req.URL)
	if err != nil {
		return controller.AuthProbeAnswer{}, err
	}
	code, digest, err := s.answer(ctx, name, ns, u.Path)
	s.mu.Lock()
	s.probes[name]++
	s.last[name] = code
	s.mu.Unlock()
	return controller.AuthProbeAnswer{Code: code, CardDigest: digest}, err
}

func (s *stubProber) answer(ctx context.Context, name, ns, path string) (int, string, error) {
	var a assaydv1alpha1.Agent
	if err := k8s.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, &a); err != nil {
		return 0, "", err
	}
	golden, err := compiler.AuthPolicy(compiler.AuthInput{AgentName: a.Name, AgentNamespace: a.Namespace,
		AgentUID: a.UID, RunNamespace: runNS(ns)})
	if err != nil {
		return 0, "", err
	}
	want, err := compiler.Digest(golden)
	if err != nil {
		return 0, "", err
	}
	s.mu.Lock()
	held, override, servedBy, foreign := s.held[name], s.override[name], s.servedBy[name], s.foreign[name]
	s.mu.Unlock()
	if foreign {
		return 401, "", nil
	}
	stored := controller.NewAgentgatewayPolicy()
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(golden), stored); err == nil {
		if d, err := compiler.Digest(stored); err == nil && d == want && !held {
			return 401, "", nil
		}
	}
	routeName, _ := compiler.ServingRouteName(name)
	var rt gatewayv1.HTTPRoute
	if err := k8s.Get(ctx, types.NamespacedName{Namespace: runNS(ns), Name: routeName}, &rt); err != nil {
		return 404, "", nil
	}
	if len(rt.Spec.Rules) == 0 || len(rt.Spec.Rules[0].BackendRefs) == 0 {
		return 500, "", nil
	}
	cardPath := a.Spec.Card.Path
	if cardPath == "" {
		cardPath = "/.well-known/agent-card.json"
	}
	if path != cardPath {
		return 404, "", nil
	}
	if override != 0 {
		return override, "", nil
	}
	backend := string(rt.Spec.Rules[0].BackendRefs[0].Name)
	if servedBy != "" {
		backend = servedBy
	}
	return 200, cardDigestOf(backend), nil
}

// createReconciler is a gateway-enabled reconciler whose probe is the oracle.
func createReconciler() (*controller.AgentReconciler, *stubProber) {
	r := newGatewayReconciler("assayd-gateway", "assayd")
	stub := newStubProber()
	r.AuthProbe = stub
	return r, stub
}

// noneAgent is an Agent whose route is published unauthenticated at its
// owner's request.
func noneAgent(t *testing.T, ns, name string) *assaydv1alpha1.Agent {
	t.Helper()
	return mustCreateAgent(t, ns, name, func(a *assaydv1alpha1.Agent) {
		a.Spec.Expose = &assaydv1alpha1.ExposeSpec{A2A: &assaydv1alpha1.ExposeProtocol{Auth: "none"}}
	})
}

// acceptRoute writes what agentgateway writes for a route the assayd Gateway
// accepted, at the route's current generation (§3.3.2).
func acceptRoute(t *testing.T, ns, agentName string) {
	t.Helper()
	rt := servingRoute(t, ns, agentName)
	if rt == nil {
		t.Fatalf("no route to accept for %s", agentName)
	}
	group, kind := gatewayv1.Group(gatewayv1.GroupName), gatewayv1.Kind("Gateway")
	gwNS, section := gatewayv1.Namespace("assayd-gateway"), gatewayv1.SectionName("http")
	now := metav1.Now()
	rt.Status.Parents = []gatewayv1.RouteParentStatus{{
		ParentRef: gatewayv1.ParentReference{Group: &group, Kind: &kind, Name: "assayd",
			Namespace: &gwNS, SectionName: &section},
		ControllerName: "agentgateway.dev/agentgateway",
		Conditions: []metav1.Condition{
			{Type: "Accepted", Status: metav1.ConditionTrue, Reason: "Accepted", Message: "accepted",
				ObservedGeneration: rt.Generation, LastTransitionTime: now},
			{Type: "ResolvedRefs", Status: metav1.ConditionTrue, Reason: "ResolvedRefs", Message: "resolved",
				ObservedGeneration: rt.Generation, LastTransitionTime: now},
		},
	}}
	if err := k8s.Status().Update(context.Background(), rt); err != nil {
		t.Fatalf("write the route's status: %v", err)
	}
}

// acceptPolicy writes what agentgateway writes for a policy that attached
// through the assayd Gateway: `Accepted=True` reason `Valid`, and
// `Attached=True`, on the GATEWAY's ancestor, at the current generation.
func acceptPolicy(t *testing.T, ns, agentName string) {
	t.Helper()
	name, _ := compiler.AuthPolicyName(agentName)
	p := policyExists(t, runNS(ns), name)
	if p == nil {
		t.Fatalf("no policy %s to accept", name)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	cond := func(typ, reason string) map[string]any {
		return map[string]any{"type": typ, "status": "True", "reason": reason, "message": reason,
			"lastTransitionTime": now, "observedGeneration": p.GetGeneration()}
	}
	p.Object["status"] = map[string]any{"ancestors": []any{map[string]any{
		"ancestorRef": map[string]any{"group": gatewayv1.GroupName, "kind": "Gateway",
			"name": "assayd", "namespace": "assayd-gateway"},
		"controllerName": "agentgateway.dev/agentgateway",
		"conditions":     []any{cond("Accepted", "Valid"), cond("Attached", "Attached")},
	}}}
	if err := k8s.Status().Update(context.Background(), p); err != nil {
		t.Fatalf("write the policy's status: %v", err)
	}
}

func authOf(t *testing.T, a *assaydv1alpha1.Agent) *assaydv1alpha1.AuthStatus {
	t.Helper()
	return liveAgent(t, a).Status.Auth
}

func txOf(t *testing.T, a *assaydv1alpha1.Agent) *assaydv1alpha1.AuthTransaction {
	t.Helper()
	if auth := authOf(t, a); auth != nil {
		return auth.Transaction
	}
	return nil
}

func routePublished(t *testing.T, ns, agentName string) bool {
	t.Helper()
	rt := servingRoute(t, ns, agentName)
	return rt != nil && len(rt.Spec.Rules) == 1 && len(rt.Spec.Rules[0].BackendRefs) > 0
}

// goldenPolicy is the policy the compiler renders for this Agent: the one the
// oracle answers 401 for, and the one `Served` must leave stored.
func goldenPolicy(t *testing.T, a *assaydv1alpha1.Agent) (*unstructured.Unstructured, string) {
	t.Helper()
	p := authPolicyFor(t, liveAgent(t, a))
	d, err := compiler.Digest(p)
	if err != nil {
		t.Fatal(err)
	}
	return p, d
}

// promote brings a new Agent's first revision up and promotes it: the pass
// that promotes it enters `Create`.
func promote(t *testing.T, r *controller.AgentReconciler, a *assaydv1alpha1.Agent) {
	t.Helper()
	reconcileOnce(t, r, a)
	reconcileOnce(t, r, a)
	markAvailable(t, a.Namespace, controller.WorkloadName(a.Name, revision.MustHash(a.Spec)), 1)
	reconcileOnce(t, r, a)
	if got := liveAgent(t, a).Status.ActiveRevision; got == "" {
		t.Fatal("the revision never promoted")
	}
}

// driveToServed takes a promoted API-key Agent's `Create` to `Served` as a
// gateway would: it accepts the prepared route and the policy, lets the
// oracle take the policy, and accepts the published route.
func driveToServed(t *testing.T, r *controller.AgentReconciler, stub *stubProber, a *assaydv1alpha1.Agent) {
	t.Helper()
	stub.hold(a.Name, false)
	acceptRoute(t, a.Namespace, a.Name)
	acceptPolicy(t, a.Namespace, a.Name)
	reconcileOnce(t, r, a)
	if !routePublished(t, a.Namespace, a.Name) {
		t.Fatalf("the route was not published after the oracle answered 401: tx=%+v", txOf(t, a))
	}
	acceptRoute(t, a.Namespace, a.Name)
	reconcileOnce(t, r, a)
	if auth := authOf(t, a); auth == nil || auth.Mode != "apikey" || auth.Transaction != nil {
		t.Fatalf("the Create did not reach Served: status.auth=%+v", auth)
	}
}

// servedAPIKeyAgent is a new Agent with no expose block, which compiles to the
// API-key policy (§3.4.4), taken through `Create` to `Served`.
func servedAPIKeyAgent(t *testing.T, name string) (*assaydv1alpha1.Agent, *controller.AgentReconciler, *stubProber) {
	t.Helper()
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, name, nil)
	r, stub := createReconciler()
	promote(t, r, a)
	driveToServed(t, r, stub, a)
	return liveAgent(t, a), r, stub
}

func mustEdit(t *testing.T, a *assaydv1alpha1.Agent, edit func(*assaydv1alpha1.Agent)) {
	t.Helper()
	live := liveAgent(t, a)
	edit(live)
	if err := k8s.Update(context.Background(), live); err != nil {
		t.Fatalf("edit agent: %v", err)
	}
}

func condIs(t *testing.T, a *assaydv1alpha1.Agent, typ assaydv1alpha1.ConditionType,
	status metav1.ConditionStatus, reason string) *metav1.Condition {
	t.Helper()
	c := condition(liveAgent(t, a), typ)
	if c == nil || c.Status != status || c.Reason != reason {
		t.Errorf("%s is %+v, want %s/%s", typ, c, status, reason)
	}
	return c
}

// §8.1 case 2: `Create` publishes only on `401`. With the oracle's switch
// held, the policy is written and converged and the probe gets 500, and the
// route stays prepared, with no backendRefs, pass after pass. Released, the
// next probe gets 401, and only then are backendRefs attached.
func TestCreatePublishesOnlyOnA401(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "gated", nil)
	r, stub := createReconciler()
	stub.hold("gated", true)
	promote(t, r, a)

	tx := txOf(t, a)
	if tx == nil || tx.Kind != "Create" || tx.TargetMode != "apikey" || tx.Stage != "Converging" || !tx.Written {
		t.Fatalf("a new API-key Agent did not enter Create and stop at Converging: %+v", tx)
	}
	golden, digest := goldenPolicy(t, a)
	if tx.TargetDigest != digest {
		t.Errorf("the Create targets digest %s; the compiler renders %s", tx.TargetDigest, digest)
	}
	if stored := policyExists(t, runNS(ns), golden.GetName()); stored == nil {
		t.Fatal("the policy was not written at ApplyingPolicies")
	}
	if routePublished(t, ns, "gated") || servingRoute(t, ns, "gated") == nil {
		t.Fatal("the route must exist, prepared, with no backendRefs, before anything converges")
	}

	acceptRoute(t, ns, "gated")
	acceptPolicy(t, ns, "gated")
	for i := 0; i < 3; i++ {
		reconcileOnce(t, r, a)
		if routePublished(t, ns, "gated") {
			t.Fatalf("pass %d published the route while the gateway had not taken the policy "+
				"(the oracle answered %d). A route published on conditions alone is the fail-open "+
				"§3.3.2 measured (a NACK'd policy reports converged while the old config serves)",
				i, stub.last["gated"])
		}
	}
	if tx = txOf(t, a); tx.Stage != "ProbingAfter" || tx.Probe == nil || tx.Probe.After == nil || *tx.Probe.After != 500 {
		t.Fatalf("with the switch held the Create should wait in ProbingAfter with the last answer 500: %+v", tx)
	}
	if stub.count("gated") == 0 {
		t.Fatal("nothing was probed")
	}
	c := condIs(t, a, assaydv1alpha1.CondReady, metav1.ConditionFalse, "AuthEnforcementPending")
	if c != nil && !strings.Contains(c.Message, "ProbingAfter") {
		t.Errorf("Ready does not name the stage it waits in: %s", c.Message)
	}

	stub.hold("gated", false)
	reconcileOnce(t, r, a)
	if !routePublished(t, ns, "gated") {
		t.Fatalf("the oracle answered 401 and the route was not published: %+v", txOf(t, a))
	}
	if tx = txOf(t, a); tx.Stage != "Publishing" || *tx.Probe.After != 401 {
		t.Errorf("after the 401 the Create is %+v, want Publishing with the 401 recorded", tx)
	}
	acceptRoute(t, ns, "gated")
	reconcileOnce(t, r, a)
	auth := authOf(t, a)
	if auth == nil || auth.Mode != "apikey" || auth.Transaction != nil || auth.AppliedDigest != digest ||
		len(auth.AdmittedGroups) != 1 || auth.AdmittedGroups[0] != ns ||
		auth.KeySource != runNS(ns)+"/assayd.dev/api-keys=true" {
		t.Errorf("Served recorded %+v", auth)
	}
}

// failPolicyCreate refuses the first Create of an AgentgatewayPolicy: the
// operator dies between the status update that enters ApplyingPolicies and
// the write it authorizes.
type failPolicyCreate struct {
	client.Client
	failed bool
}

func (c *failPolicyCreate) Create(ctx context.Context, o client.Object, opts ...client.CreateOption) error {
	if u, ok := o.(*unstructured.Unstructured); ok && u.GetKind() == compiler.PolicyKind && !c.failed {
		c.failed = true
		return errors.New("the operator stopped here")
	}
	return c.Client.Create(ctx, o, opts...)
}

// §8.1 case 3: re-entry at the write. `written` is set in the status update
// that enters ApplyingPolicies, before the write, so a crash between the two
// reads as written, and the re-entry repeats the write. The case ends at
// Served with the stored policy equal to the golden one, so it does not rest
// on the oracle alone.
func TestCreateReentersAtTheWriteAfterACrash(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "crashed", nil)
	r, stub := createReconciler()
	crash := &failPolicyCreate{Client: k8s}
	r.Client = crash
	reconcileOnce(t, r, a)
	reconcileOnce(t, r, a)
	markAvailable(t, ns, controller.WorkloadName("crashed", revision.MustHash(a.Spec)), 1)
	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(a)}); err == nil {
		t.Fatal("the refused policy write returned no error")
	}
	golden, digest := goldenPolicy(t, a)
	tx := txOf(t, a)
	if tx == nil || tx.Stage != "ApplyingPolicies" || !tx.Written {
		t.Fatalf("the status update entering ApplyingPolicies, with written set, was not recorded "+
			"before the write: %+v", tx)
	}
	if policyExists(t, runNS(ns), golden.GetName()) != nil {
		t.Fatal("the crash was not simulated: the policy was written")
	}

	reconcileOnce(t, r, a)
	if policyExists(t, runNS(ns), golden.GetName()) == nil {
		t.Fatalf("the re-entry after the crash did not write the policy: %+v", txOf(t, a))
	}
	driveToServed(t, r, stub, a)
	stored := policyExists(t, runNS(ns), golden.GetName())
	if d, err := compiler.Digest(stored); err != nil || d != digest {
		t.Errorf("at Served the stored <agent>-auth digests to %s, not the golden %s (%v)", d, digest, err)
	}
	if !equalJSON(stored.Object["spec"], golden.Object["spec"]) {
		t.Errorf("at Served the stored policy's spec is not the golden spec:\n%v\n%v",
			stored.Object["spec"], golden.Object["spec"])
	}
}

func equalJSON(a, b any) bool {
	x := &unstructured.Unstructured{Object: map[string]any{"v": a}}
	y := &unstructured.Unstructured{Object: map[string]any{"v": b}}
	dx, _ := compiler.Digest(x)
	dy, _ := compiler.Digest(y)
	return dx == dy && strings.TrimSpace(dx) != ""
}

// §8.1 case 4, H2. A passing probe with the replica count unknown leaves
// Ready=True and GovernanceSkipped=False, reason AuthVerifiedOnOneReplica.
// With the switch held past the deadline, the route stays unpublished, with
// PolicyApplyIncomplete, reason AuthEnforcementUnverified, and Ready=False.
func TestAPassOnOneReplicaIsReadyAndAHeldSwitchIsNot(t *testing.T) {
	t.Run("a pass", func(t *testing.T) {
		a, _, _ := servedAPIKeyAgent(t, "onereplica")
		condIs(t, a, assaydv1alpha1.CondReady, metav1.ConditionTrue, "Available")
		condIs(t, a, assaydv1alpha1.CondGovernanceSkipped, metav1.ConditionFalse, "AuthVerifiedOnOneReplica")
		if c := condition(liveAgent(t, a), assaydv1alpha1.CondPolicyApplyIncomplete); c != nil {
			t.Errorf("PolicyApplyIncomplete survived a pass: %+v", c)
		}
		if v := authOf(t, a).Verified; v == nil || v.ReplicasProbed != 1 || v.ReplicasDeclared != nil {
			t.Errorf("status.auth.verified is %+v, want one replica probed and the count unknown", v)
		}
		if p := liveAgent(t, a).Status.Phase; p != assaydv1alpha1.PhaseReady {
			t.Errorf("phase is %s after a pass", p)
		}
	})
	t.Run("the switch held past the deadline", func(t *testing.T) {
		ns := newNamespace(t)
		a := mustCreateAgent(t, ns, "stalled", nil)
		r, stub := createReconciler()
		r.AuthDeadline = time.Millisecond
		stub.hold("stalled", true)
		promote(t, r, a)
		acceptRoute(t, ns, "stalled")
		acceptPolicy(t, ns, "stalled")
		reconcileOnce(t, r, a)
		reconcileOnce(t, r, a)
		if routePublished(t, ns, "stalled") {
			t.Fatal("a route was published while the oracle refused nothing")
		}
		c := condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "AuthEnforcementUnverified")
		if c != nil && (!strings.Contains(c.Message, "ProbingAfter") || !strings.Contains(c.Message, "500")) {
			t.Errorf("the deadline's message does not name the stage and the last answer: %s", c.Message)
		}
		condIs(t, a, assaydv1alpha1.CondReady, metav1.ConditionFalse, "AuthEnforcementUnverified")
		if auth := authOf(t, a); auth.Mode != "" {
			t.Errorf("status.auth records mode %q before Served", auth.Mode)
		}
	})
}

// §8.1 case 12, the `Create` half: a deadline in every stage. A `Create` whose
// policy status the test never writes stays in Converging, and at the
// deadline it reports PolicyApplyIncomplete, reason AuthEnforcementUnverified,
// naming Converging. The same for one stopped in Publishing, whose published
// route the Gateway never accepts.
func TestACreateHasADeadlineOutcomeInEveryStage(t *testing.T) {
	t.Run("Converging", func(t *testing.T) {
		ns := newNamespace(t)
		a := mustCreateAgent(t, ns, "unconverged", nil)
		r, _ := createReconciler()
		r.AuthDeadline = time.Millisecond
		promote(t, r, a)
		reconcileOnce(t, r, a)
		if tx := txOf(t, a); tx == nil || tx.Stage != "Converging" {
			t.Fatalf("want the Create waiting in Converging: %+v", tx)
		}
		c := condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "AuthEnforcementUnverified")
		if c != nil && !strings.Contains(c.Message, "Converging") {
			t.Errorf("the deadline's message does not name Converging: %s", c.Message)
		}
		condIs(t, a, assaydv1alpha1.CondReady, metav1.ConditionFalse, "AuthEnforcementUnverified")
		if routePublished(t, ns, "unconverged") {
			t.Error("the route was published")
		}
	})
	t.Run("Publishing", func(t *testing.T) {
		ns := newNamespace(t)
		a := mustCreateAgent(t, ns, "unaccepted", nil)
		r, _ := createReconciler()
		r.AuthDeadline = time.Millisecond
		promote(t, r, a)
		acceptRoute(t, ns, "unaccepted")
		acceptPolicy(t, ns, "unaccepted")
		reconcileOnce(t, r, a)
		reconcileOnce(t, r, a)
		if tx := txOf(t, a); tx == nil || tx.Stage != "Publishing" {
			t.Fatalf("want the Create waiting in Publishing: %+v", tx)
		}
		c := condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "AuthEnforcementUnverified")
		if c != nil && !strings.Contains(c.Message, "Publishing") {
			t.Errorf("the deadline's message does not name Publishing: %s", c.Message)
		}
	})
}

// The deadline is set on first entering the first stage and kept on every
// re-entry (§3.3.3), so a Create that keeps re-entering cannot outrun it.
func TestTheDeadlineIsKeptOnReentry(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "keptdeadline", nil)
	r, _ := createReconciler()
	promote(t, r, a)
	first := txOf(t, a).Deadline
	if first == nil || time.Until(first.Time) < 3*time.Minute || time.Until(first.Time) > 4*time.Minute+time.Second {
		t.Fatalf("the deadline is %v; want about 4 minutes after the Create was entered", first)
	}
	name, _ := compiler.AuthPolicyName("keptdeadline")
	if err := k8s.Delete(context.Background(), policyExists(t, runNS(ns), name)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)
	reconcileOnce(t, r, a)
	if policyExists(t, runNS(ns), name) == nil {
		t.Fatal("a deleted policy beside a Create was not written again at the re-entry")
	}
	if got := txOf(t, a).Deadline; got == nil || !got.Equal(first) {
		t.Errorf("the re-entry moved the deadline from %v to %v", first, got)
	}
}

// §8.1 case 1, I1. A served Agent is edited to `auth: oauth`, which the slice
// cannot compile. Its `<agent>-auth` is neither deleted nor rewritten, the
// route keeps its backendRefs, the revision the edit minted gains no weight,
// and the Agent reports AuthInputAbsent.
func TestAServedAgentEditedToOAuthKeepsItsLastGoodAuth(t *testing.T) {
	a, r, _ := servedAPIKeyAgent(t, "keptauth")
	ns := a.Namespace
	name, _ := compiler.AuthPolicyName("keptauth")
	before := policyExists(t, runNS(ns), name)
	first := a.Status.ActiveRevision

	mustEdit(t, a, func(a *assaydv1alpha1.Agent) {
		a.Spec.Expose = &assaydv1alpha1.ExposeSpec{A2A: &assaydv1alpha1.ExposeProtocol{Auth: "oauth"}}
	})
	second := revision.MustHash(liveAgent(t, a).Spec)
	if second == first {
		t.Fatal("the expose edit did not mint a revision; the hold is not exercised")
	}
	reconcileOnce(t, r, a)
	markAvailable(t, ns, controller.WorkloadName("keptauth", second), 1)
	for i := 0; i < 3; i++ {
		reconcileOnce(t, r, a)
	}

	after := policyExists(t, runNS(ns), name)
	if after == nil {
		t.Fatal("the served <agent>-auth was deleted on a compile failure. A compile failure means " +
			"the desired policy is unknown, never that no policy is desired, and the route it " +
			"leaves serves with no key (design 03 §3.3.1, I1)")
	}
	if after.GetResourceVersion() != before.GetResourceVersion() {
		t.Errorf("the served <agent>-auth was rewritten (resourceVersion %s → %s)",
			before.GetResourceVersion(), after.GetResourceVersion())
	}
	live := liveAgent(t, a)
	if live.Status.ActiveRevision != first {
		t.Errorf("the revision the uncompilable edit minted was promoted (%s → %s); it must gain no "+
			"weight while its -auth input does not compile", first, live.Status.ActiveRevision)
	}
	rt := servingRoute(t, ns, "keptauth")
	if !routePublished(t, ns, "keptauth") ||
		string(rt.Spec.Rules[0].BackendRefs[0].Name) != controller.WorkloadName("keptauth", first) {
		t.Errorf("the route no longer serves the revision it served: %+v", rt.Spec.Rules)
	}
	condIs(t, a, assaydv1alpha1.CondPolicyCompileFailed, metav1.ConditionTrue, "AuthInputAbsent")
	condIs(t, a, assaydv1alpha1.CondReady, metav1.ConditionFalse, "AuthInputAbsent")
	condIs(t, a, assaydv1alpha1.CondGovernanceSkipped, metav1.ConditionFalse, "AuthVerifiedOnOneReplica")
	if live.Status.Auth.Mode != "apikey" {
		t.Errorf("status.auth moved off its served record: %+v", live.Status.Auth)
	}
}

// §8.1 case 9: PolicyCompileFailed is owned, abnormal-true and not sticky, so
// it clears when its cause goes. An I1 owner reverts the edit.
func TestPolicyCompileFailedClearsWhenItsCauseGoes(t *testing.T) {
	a, r, _ := servedAPIKeyAgent(t, "reverted")
	mustEdit(t, a, func(a *assaydv1alpha1.Agent) {
		a.Spec.Expose = &assaydv1alpha1.ExposeSpec{A2A: &assaydv1alpha1.ExposeProtocol{Auth: "oauth"}}
	})
	reconcileOnce(t, r, a)
	condIs(t, a, assaydv1alpha1.CondPolicyCompileFailed, metav1.ConditionTrue, "AuthInputAbsent")

	mustEdit(t, a, func(a *assaydv1alpha1.Agent) { a.Spec.Expose = nil })
	settle(t, r, a)
	if c := condition(liveAgent(t, a), assaydv1alpha1.CondPolicyCompileFailed); c != nil {
		t.Errorf("PolicyCompileFailed survived the revert that removed its cause: %+v", c)
	}
	condIs(t, a, assaydv1alpha1.CondReady, metav1.ConditionTrue, "Available")
}

// §8.1 case 5: `apikey` → `none` is refused. A served Agent moved to
// `auth: none` keeps its policy, and its route does not take the marker.
func TestAPIKeyToNoneIsRefused(t *testing.T) {
	a, r, _ := servedAPIKeyAgent(t, "stayslocked")
	ns := a.Namespace
	name, _ := compiler.AuthPolicyName("stayslocked")
	before := policyExists(t, runNS(ns), name)

	mustEdit(t, a, func(a *assaydv1alpha1.Agent) {
		a.Spec.Expose = &assaydv1alpha1.ExposeSpec{A2A: &assaydv1alpha1.ExposeProtocol{Auth: "none"}}
	})
	second := revision.MustHash(liveAgent(t, a).Spec)
	reconcileOnce(t, r, a)
	markAvailable(t, ns, controller.WorkloadName("stayslocked", second), 1)
	settle(t, r, a)

	after := policyExists(t, runNS(ns), name)
	if after == nil || after.GetResourceVersion() != before.GetResourceVersion() {
		t.Fatalf("the served policy was deleted or rewritten on a refused apikey → none (%v)", after)
	}
	condIs(t, a, assaydv1alpha1.CondPolicyCompileFailed, metav1.ConditionTrue, "AuthTransitionNotBuilt")
	if auth := authOf(t, a); auth.Mode != "apikey" {
		t.Errorf("status.auth records mode %q; the refused change must not be recorded", auth.Mode)
	}
	if rt := servingRoute(t, ns, "stayslocked"); rt == nil || rt.Labels["assayd.dev/auth"] != "" {
		t.Errorf("the route of a refused apikey → none carries the no-auth marker: %v", rt)
	}
	condIs(t, a, assaydv1alpha1.CondGovernanceSkipped, metav1.ConditionFalse, "AuthVerifiedOnOneReplica")
}

// §8.1 case 14: no transaction for a target that does not compile. A
// never-served Agent stored with `auth: oauth` is held past the deadline. It
// carries PolicyCompileFailed, reason AuthInputAbsent, and no
// PolicyApplyIncomplete; no route exists, and status.auth has no transaction.
func TestNoTransactionForATargetThatDoesNotCompile(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "uncompiled", func(a *assaydv1alpha1.Agent) {
		a.Spec.Expose = &assaydv1alpha1.ExposeSpec{A2A: &assaydv1alpha1.ExposeProtocol{Auth: "oauth"}}
	})
	r, stub := createReconciler()
	r.AuthDeadline = time.Millisecond
	promote(t, r, a)
	time.Sleep(10 * time.Millisecond)
	for i := 0; i < 3; i++ {
		reconcileOnce(t, r, a)
	}
	condIs(t, a, assaydv1alpha1.CondPolicyCompileFailed, metav1.ConditionTrue, "AuthInputAbsent")
	if c := condition(liveAgent(t, a), assaydv1alpha1.CondPolicyApplyIncomplete); c != nil {
		t.Errorf("an Agent that never entered a transaction carries PolicyApplyIncomplete: %+v. It "+
			"pages, and the cause is the spec, which PolicyCompileFailed already names", c)
	}
	if rt := servingRoute(t, ns, "uncompiled"); rt != nil {
		t.Errorf("a route was prepared for an Agent whose serving route does not compile: %+v", rt.Spec)
	}
	if tx := txOf(t, a); tx != nil {
		t.Errorf("status.auth carries a transaction for a target that does not compile: %+v", tx)
	}
	name, _ := compiler.AuthPolicyName("uncompiled")
	if policyExists(t, runNS(ns), name) != nil {
		t.Error("a policy was written for an Agent whose -auth does not compile")
	}
	if stub.count("uncompiled") != 0 {
		t.Error("an Agent with no transaction was probed")
	}
	condIs(t, a, assaydv1alpha1.CondReady, metav1.ConditionFalse, "AuthInputAbsent")
}

// `auth: none` is published at once with its marker, and no policy (§3.3.1).
func TestANoneAgentIsPublishedAtOnceWithItsMarker(t *testing.T) {
	ns := newNamespace(t)
	a := noneAgent(t, ns, "optedout")
	r, stub := createReconciler()
	promote(t, r, a)
	if !routePublished(t, ns, "optedout") {
		t.Fatalf("an auth: none Agent's route was not published at once: %+v", txOf(t, a))
	}
	if rt := servingRoute(t, ns, "optedout"); rt.Labels["assayd.dev/auth"] != "none" {
		t.Errorf("the published none route carries labels %v, without the marker", rt.Labels)
	}
	acceptRoute(t, ns, "optedout")
	reconcileOnce(t, r, a)
	if auth := authOf(t, a); auth == nil || auth.Mode != "none" || auth.Transaction != nil ||
		auth.AppliedDigest != "" || auth.KeySource != "" || auth.Verified != nil {
		t.Errorf("Served for none must record {mode: none} and nothing beside it: %+v", auth)
	}
	condIs(t, a, assaydv1alpha1.CondGovernanceSkipped, metav1.ConditionTrue, "AuthOptedOut")
	name, _ := compiler.AuthPolicyName("optedout")
	if policyExists(t, runNS(ns), name) != nil || stub.count("optedout") != 0 {
		t.Error("auth: none wrote a policy or ran a probe")
	}
}

// A served policy changed out of band is re-asserted to the one status.auth
// records (§3.3.1): widening its group rule is exactly the change that must
// not stick.
func TestAServedPolicyChangedOutOfBandIsReasserted(t *testing.T) {
	a, r, _ := servedAPIKeyAgent(t, "reasserted")
	ns := a.Namespace
	name, _ := compiler.AuthPolicyName("reasserted")
	p := policyExists(t, runNS(ns), name)
	if err := unstructured.SetNestedStringSlice(p.Object, []string{"true"},
		"spec", "traffic", "authorization", "policy", "matchExpressions"); err != nil {
		t.Fatal(err)
	}
	if err := k8s.Update(context.Background(), p); err != nil {
		t.Fatalf("widen the policy: %v", err)
	}
	reconcileOnce(t, r, a)
	_, want := goldenPolicy(t, a)
	if d, _ := compiler.Digest(policyExists(t, runNS(ns), name)); d != want {
		t.Errorf("a served policy widened to admit everyone was not re-asserted: digest %s, want %s", d, want)
	}
}

// A route published before this operator carried a compiler, with nothing in
// status.auth, is `Adopt`'s trigger: refused. No policy is written, the route
// keeps serving, and GovernanceSkipped says it is unauthenticated (§3.3.3).
// Adopt's record, `refusedMode`, and K2 are the next change's.
func TestARoutePublishedBeforeTheCompilerIsNotLocked(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "legacy", nil)
	off := newReconciler(false)
	rev := revision.MustHash(a.Spec)
	reconcileOnce(t, off, a)
	reconcileOnce(t, off, a)
	markAvailable(t, ns, controller.WorkloadName("legacy", rev), 1)
	settle(t, off, a)

	// What an operator without a compiler left: the same route, published at
	// once, with no marker and no record in status.auth.
	name, _ := compiler.ServingRouteName("legacy")
	group, kind := gatewayv1.Group(gatewayv1.GroupName), gatewayv1.Kind("Gateway")
	svcGroup, svcKind := gatewayv1.Group(""), gatewayv1.Kind("Service")
	gwNS, section := gatewayv1.Namespace("assayd-gateway"), gatewayv1.SectionName("http")
	port, weight := gatewayv1.PortNumber(8080), int32(100)
	legacy := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: runNS(ns), Labels: map[string]string{
			controller.LabelAgent: "legacy", controller.LabelAgentUID: string(a.UID),
			controller.LabelAgentNamespace: ns, controller.LabelRevision: rev}},
		Spec: gatewayv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{ParentRefs: []gatewayv1.ParentReference{{
				Group: &group, Kind: &kind, Name: "assayd", Namespace: &gwNS, SectionName: &section}}},
			Hostnames: []gatewayv1.Hostname{gatewayv1.Hostname("legacy." + ns + "." +
				controller.DefaultGatewayHostnameSuffix)},
			Rules: []gatewayv1.HTTPRouteRule{{BackendRefs: []gatewayv1.HTTPBackendRef{{
				BackendRef: gatewayv1.BackendRef{BackendObjectReference: gatewayv1.BackendObjectReference{
					Group: &svcGroup, Kind: &svcKind,
					Name: gatewayv1.ObjectName(controller.WorkloadName("legacy", rev)), Port: &port},
					Weight: &weight}}}}},
		},
	}
	if err := k8s.Create(context.Background(), legacy); err != nil {
		t.Fatalf("plant the legacy route: %v", err)
	}

	r, stub := createReconciler()
	reconcileOnce(t, r, a)
	reconcileOnce(t, r, a)
	condIs(t, a, assaydv1alpha1.CondGovernanceSkipped, metav1.ConditionTrue, "CompilerUpgradeUnsupported")
	if !routePublished(t, ns, "legacy") {
		t.Error("the refused Adopt took the route off the air")
	}
	if v, ok := servingRoute(t, ns, "legacy").Labels["assayd.dev/auth"]; ok {
		t.Errorf("the refused Adopt's route carries the marker %q; the compiler did not publish it", v)
	}
	policy, _ := compiler.AuthPolicyName("legacy")
	if policyExists(t, runNS(ns), policy) != nil || stub.count("legacy") != 0 {
		t.Error("a first policy was written onto a route that was already serving, or it was probed")
	}
	tx := txOf(t, a)
	if tx == nil || tx.Kind != "Adopt" || tx.Stage != "Refused" || tx.RefusedMode != "apikey" {
		t.Fatalf("the refusal is not recorded as {Adopt, Refused, refusedMode: apikey}: %+v", tx)
	}

	// Deleting the route is not an exit from Adopt (§3.3.3, ADR-0034
	// Amendment 3): it comes back as the shipped emitter made it, and nothing
	// locks it, which with no record would have been a Create by the back door.
	if err := k8s.Delete(context.Background(), servingRoute(t, ns, "legacy")); err != nil {
		t.Fatal(err)
	}
	reconcileOnce(t, r, a)
	reconcileOnce(t, r, a)
	if !routePublished(t, ns, "legacy") {
		t.Error("the refused Adopt's deleted route did not come back published")
	}
	if policyExists(t, runNS(ns), policy) != nil {
		t.Error("deleting a refused Adopt's route locked it through Create")
	}
	if tx := txOf(t, a); tx == nil || tx.Kind != "Adopt" {
		t.Errorf("the refusal did not survive its route's delete: %+v", tx)
	}
	condIs(t, a, assaydv1alpha1.CondGovernanceSkipped, metav1.ConditionTrue, "CompilerUpgradeUnsupported")

	// refusedMode tracks the desired mode while it is not apikey (§3.3.3). The
	// edit back to apikey is then K2's consent, which TestK2* pins.
	mustEdit(t, a, func(x *assaydv1alpha1.Agent) {
		x.Spec.Expose = &assaydv1alpha1.ExposeSpec{A2A: &assaydv1alpha1.ExposeProtocol{Auth: "none"}}
	})
	reconcileOnce(t, r, a)
	if tx := txOf(t, a); tx == nil || tx.Kind != "Adopt" || tx.RefusedMode != "none" {
		t.Errorf("refusedMode did not track an edit to none: %+v", tx)
	}
}
