// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package envtest

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	ctrlconfig "sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/compiler"
	"github.com/Quinyte/assayd/internal/controller"
	"github.com/Quinyte/assayd/internal/revision"
)

// Design 03 §1.1's operator wiring: the startup refusal, the finalizer that
// revokes a route before its `-auth`, and the two watches. Nothing here emits
// a policy, because nothing in the operator does yet. The policies these tests
// plant are rendered by internal/compiler, so each is the object the slice
// will emit rather than a shape invented here.

func authPolicyFor(t *testing.T, a *assaydv1alpha1.Agent) *unstructured.Unstructured {
	t.Helper()
	p, err := compiler.AuthPolicy(compiler.AuthInput{AgentName: a.Name, AgentNamespace: a.Namespace,
		AgentUID: a.UID, RunNamespace: runNS(a.Namespace)})
	if err != nil {
		t.Fatalf("render the -auth policy: %v", err)
	}
	return p
}

func liveAgent(t *testing.T, a *assaydv1alpha1.Agent) *assaydv1alpha1.Agent {
	t.Helper()
	var got assaydv1alpha1.Agent
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &got); err != nil {
		t.Fatalf("read agent: %v", err)
	}
	return &got
}

func policyExists(t *testing.T, ns, name string) *unstructured.Unstructured {
	t.Helper()
	p := controller.NewAgentgatewayPolicy()
	switch err := k8s.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: name}, p); {
	case apierrors.IsNotFound(err):
		return nil
	case err != nil:
		t.Fatalf("read policy %s/%s: %v", ns, name, err)
	}
	return p
}

// deleteLog records, in order, every delete the reconciler issues. The order is
// the claim, and the API server keeps no record of it.
type deleteLog struct {
	client.Client
	mu      sync.Mutex
	deleted []string
}

func (d *deleteLog) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	gvk, err := apiutil.GVKForObject(obj, scheme)
	kind := gvk.Kind
	if err != nil {
		kind = obj.GetObjectKind().GroupVersionKind().Kind
	}
	d.mu.Lock()
	d.deleted = append(d.deleted, kind+"/"+obj.GetName())
	d.mu.Unlock()
	return d.Client.Delete(ctx, obj, opts...)
}

func (d *deleteLog) index(entry string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	for i, e := range d.deleted {
		if e == entry {
			return i
		}
	}
	return -1
}

// servedGatewayAgent creates an Agent, promotes its revision and returns it
// with its serving route in place, under a reconciler whose deletes are logged.
func servedGatewayAgent(t *testing.T, name string) (*assaydv1alpha1.Agent, *controller.AgentReconciler, *deleteLog, string) {
	t.Helper()
	ns := newNamespace(t)
	// `auth: none`, whose route is published at once and which writes no
	// policy of its own, so the tests below can plant the policy they need.
	a := noneAgent(t, ns, name)
	r := newGatewayReconciler("assayd-gateway", "assayd")
	log := &deleteLog{Client: k8s}
	r.Client = log
	rev := revision.MustHash(a.Spec)
	reconcileOnce(t, r, a)
	reconcileOnce(t, r, a)
	markAvailable(t, ns, controller.WorkloadName(name, rev), 1)
	settle(t, r, a)
	route, err := compiler.ServingRouteName(name)
	if err != nil {
		t.Fatal(err)
	}
	if servingRoute(t, ns, name) == nil {
		t.Fatal("no serving route to revoke")
	}
	return liveAgent(t, a), r, log, route
}

func agentGone(t *testing.T, a *assaydv1alpha1.Agent) bool {
	t.Helper()
	var got assaydv1alpha1.Agent
	err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &got)
	if err != nil && !apierrors.IsNotFound(err) {
		t.Fatalf("read agent: %v", err)
	}
	return apierrors.IsNotFound(err)
}

// Design 03 §3.3, step 5: routes detach before their policies are deleted,
// because the reverse order leaves the route published and unauthenticated for
// the gap.
func TestTeardownRevokesTheRouteBeforeItsAuthPolicy(t *testing.T) {
	ctx := context.Background()
	a, r, log, route := servedGatewayAgent(t, "ordered")
	policy := authPolicyFor(t, a)
	if err := k8s.Create(ctx, policy); err != nil {
		t.Fatalf("plant the -auth policy: %v", err)
	}

	if err := k8s.Delete(ctx, a); err != nil {
		t.Fatalf("delete agent: %v", err)
	}
	reconcileOnce(t, r, a)

	if policyExists(t, policy.GetNamespace(), policy.GetName()) != nil {
		t.Errorf("the Agent's own -auth policy survived its teardown. It carries no ownerReference, " +
			"so the finalizer is the only thing that collects it (design 03 §3.2)")
	}
	if !agentGone(t, a) {
		t.Error("the finalizer did not release")
	}
	ri, pi := log.index("HTTPRoute/"+route), log.index("AgentgatewayPolicy/"+policy.GetName())
	if ri < 0 || pi < 0 {
		t.Fatalf("want both deletes logged, got %v", log.deleted)
	}
	if ri > pi {
		t.Errorf("the policy was deleted before the route (order: %v). For the gap between them the "+
			"route is published with no -auth: design 03 §3.3 step 5 revokes routes first", log.deleted)
	}
}

// The name is not the authority to delete, and neither is a label alone
// (design 03 §3.2). A policy that has this Agent's `-auth` name and does not
// carry its UID is someone else's, and teardown leaves it.
func TestTeardownLeavesAnAuthPolicyThatIsNotThisAgents(t *testing.T) {
	for _, tc := range []struct {
		name   string
		labels func(a *assaydv1alpha1.Agent) map[string]string
	}{
		{"no labels at all", func(*assaydv1alpha1.Agent) map[string]string { return nil }},
		// The Agent's name and namespace, which anyone can write, and another
		// UID: a predecessor's, or a forgery.
		{"this Agent's name, another UID", func(a *assaydv1alpha1.Agent) map[string]string {
			return map[string]string{compiler.LabelAgent: a.Name, compiler.LabelAgentNamespace: a.Namespace,
				compiler.LabelAgentUID: "0f0e0d0c-0000-0000-0000-000000000000"}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			a, r, log, route := servedGatewayAgent(t, "foreign")
			foreign := authPolicyFor(t, a)
			foreign.SetLabels(tc.labels(a))
			if err := k8s.Create(ctx, foreign); err != nil {
				t.Fatalf("plant the foreign policy: %v", err)
			}

			if err := k8s.Delete(ctx, a); err != nil {
				t.Fatalf("delete agent: %v", err)
			}
			reconcileOnce(t, r, a)

			if policyExists(t, foreign.GetNamespace(), foreign.GetName()) == nil {
				t.Errorf("teardown deleted %s, which does not carry this Agent's UID. A name is no "+
					"authority to delete, and a label is writable by anyone with update (design 03 §3.2)",
					foreign.GetName())
			}
			if log.index("AgentgatewayPolicy/"+foreign.GetName()) >= 0 {
				t.Errorf("teardown issued a delete for the foreign policy: %v", log.deleted)
			}
			if servingRoute(t, a.Namespace, a.Name) != nil || log.index("HTTPRoute/"+route) < 0 {
				t.Errorf("the Agent's own route was not revoked: %v", log.deleted)
			}
			if !agentGone(t, a) {
				t.Error("a foreign policy held the Agent's finalizer; it is not the Agent's to wait for")
			}
		})
	}
}

func setObjectFinalizers(t *testing.T, obj client.Object, finalizers []string) {
	t.Helper()
	ctx := context.Background()
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
		t.Fatalf("read %s: %v", obj.GetName(), err)
	}
	obj.SetFinalizers(finalizers)
	if err := k8s.Update(ctx, obj); err != nil {
		t.Fatalf("set finalizers on %s: %v", obj.GetName(), err)
	}
}

const holdFinalizer = "example.com/hold"

// A delete call that returned is not a deleted object. A finalizer of anyone's
// on the route or on the policy keeps it, and teardown must not move past a
// delete that has not happened: the policy is not deleted while the route
// still exists, and the Agent is not released while either does.
func TestTeardownDoesNotMovePastADeleteThatHasNotHappened(t *testing.T) {
	t.Run("the route is held", func(t *testing.T) {
		ctx := context.Background()
		a, r, log, route := servedGatewayAgent(t, "heldroute")
		policy := authPolicyFor(t, a)
		if err := k8s.Create(ctx, policy); err != nil {
			t.Fatalf("plant the -auth policy: %v", err)
		}
		rt := servingRoute(t, a.Namespace, a.Name)
		setObjectFinalizers(t, rt, []string{holdFinalizer})
		t.Cleanup(func() {
			_ = client.IgnoreNotFound(k8s.Patch(context.Background(), rt, client.RawPatch(types.MergePatchType, []byte(`{"metadata":{"finalizers":null}}`))))
		})

		if err := k8s.Delete(ctx, a); err != nil {
			t.Fatalf("delete agent: %v", err)
		}
		reconcileOnce(t, r, a)

		if log.index("HTTPRoute/"+route) < 0 {
			t.Fatalf("the route's delete was never issued: %v", log.deleted)
		}
		if policyExists(t, policy.GetNamespace(), policy.GetName()) == nil ||
			log.index("AgentgatewayPolicy/"+policy.GetName()) >= 0 {
			t.Errorf("the policy was deleted while its route was still there, held by a finalizer. "+
				"That is the gap step 5 exists to close: a published route with no -auth (%v)", log.deleted)
		}
		if agentGone(t, a) {
			t.Fatal("the Agent was released with its route still published")
		}
		assertTeardownWaiting(t, a, route, holdFinalizer)

		setObjectFinalizers(t, rt, nil)
		reconcileOnce(t, r, a)
		if policyExists(t, policy.GetNamespace(), policy.GetName()) != nil {
			t.Error("the route went and the policy was not deleted after it")
		}
		if !agentGone(t, a) {
			t.Error("both are gone and the Agent was not released")
		}
	})

	t.Run("the policy is held", func(t *testing.T) {
		ctx := context.Background()
		a, r, _, _ := servedGatewayAgent(t, "heldpolicy")
		policy := authPolicyFor(t, a)
		policy.SetFinalizers([]string{holdFinalizer})
		if err := k8s.Create(ctx, policy); err != nil {
			t.Fatalf("plant the -auth policy: %v", err)
		}
		t.Cleanup(func() {
			_ = client.IgnoreNotFound(k8s.Patch(context.Background(), policy,
				client.RawPatch(types.MergePatchType, []byte(`{"metadata":{"finalizers":null}}`))))
		})

		if err := k8s.Delete(ctx, a); err != nil {
			t.Fatalf("delete agent: %v", err)
		}
		reconcileOnce(t, r, a)

		held := policyExists(t, policy.GetNamespace(), policy.GetName())
		if held == nil || held.GetDeletionTimestamp() == nil {
			t.Fatalf("want the policy marked for deletion and still held, got %v", held)
		}
		if agentGone(t, a) {
			t.Fatal("the Agent was released while its -auth policy's delete was still pending")
		}
		assertTeardownWaiting(t, a, policy.GetName(), holdFinalizer)

		held.SetFinalizers(nil)
		if err := k8s.Update(ctx, held); err != nil {
			t.Fatalf("release the hold on the policy: %v", err)
		}
		reconcileOnce(t, r, a)
		if policyExists(t, policy.GetNamespace(), policy.GetName()) != nil {
			t.Error("the hold was released and the policy is still there")
		}
		if !agentGone(t, a) {
			t.Error("the policy went and the Agent was not released")
		}
	})
}

// assertTeardownWaiting: a teardown that waits says so on the Agent, naming the
// object it waits for and the finalizer holding it (NFR-8). Without it the
// Agent sits Terminating and nothing says why.
func assertTeardownWaiting(t *testing.T, a *assaydv1alpha1.Agent, object, finalizer string) {
	t.Helper()
	c := condition(liveAgent(t, a), assaydv1alpha1.CondReady)
	if c == nil || c.Status != metav1.ConditionFalse || c.Reason != controller.ReasonTeardownWaiting {
		t.Fatalf("teardown is waiting on %s and the Agent does not say so: Ready=%+v", object, c)
	}
	for _, want := range []string{object, finalizer} {
		if !strings.Contains(c.Message, want) {
			t.Errorf("the TeardownWaiting message does not name %q: %s", want, c.Message)
		}
	}
}

// staleRoutes answers every HTTPRoute list as a cache that has not yet seen a
// route would: empty.
type staleRoutes struct{ client.Client }

func (s *staleRoutes) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	if l, ok := list.(*gatewayv1.HTTPRouteList); ok {
		l.Items = nil
		return nil
	}
	return s.Client.List(ctx, list, opts...)
}

// Teardown reads the routes through the uncached reader. A cache that has not
// seen a route created moments earlier says there is none: the policy goes, the
// Agent is released, and the route outlives both, with nothing left to sweep
// it, because every later path keys on a UID that no longer exists.
func TestTeardownFindsARouteTheCacheHasNotSeen(t *testing.T) {
	ctx := context.Background()
	a, r, log, route := servedGatewayAgent(t, "unseen")
	policy := authPolicyFor(t, a)
	if err := k8s.Create(ctx, policy); err != nil {
		t.Fatalf("plant the -auth policy: %v", err)
	}
	r.Reader = k8s
	r.Client = &staleRoutes{Client: log}

	if err := k8s.Delete(ctx, a); err != nil {
		t.Fatalf("delete agent: %v", err)
	}
	reconcileOnce(t, r, a)

	if servingRoute(t, a.Namespace, a.Name) != nil {
		t.Fatalf("the route survived its Agent's teardown: the cache had not seen it, and teardown "+
			"believed the cache (%v)", log.deleted)
	}
	ri, pi := log.index("HTTPRoute/"+route), log.index("AgentgatewayPolicy/"+policy.GetName())
	if ri < 0 || pi < 0 || ri > pi {
		t.Errorf("want the route deleted, then the policy; got %v", log.deleted)
	}
	if !agentGone(t, a) {
		t.Error("the finalizer did not release")
	}
}

// swapOnDelete replaces the policy between teardown's read of it and its
// delete: the real one goes, and a policy of the same name that is not the
// Agent's takes its place.
type swapOnDelete struct {
	client.Client
	swap  func(ctx context.Context) error
	fired bool
}

func (s *swapOnDelete) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	if u, ok := obj.(*unstructured.Unstructured); ok && u.GetKind() == compiler.PolicyKind && !s.fired {
		s.fired = true
		if err := s.swap(ctx); err != nil {
			return fmt.Errorf("the test's swap failed: %w", err)
		}
	}
	return s.Client.Delete(ctx, obj, opts...)
}

// The label is checked on one read and the delete is a second call. What is
// deleted must be the object that was checked: a same-named policy that took
// its place in between is someone else's.
func TestTeardownDeletesOnlyThePolicyItInspected(t *testing.T) {
	ctx := context.Background()
	a, r, log, _ := servedGatewayAgent(t, "swapped")
	own := authPolicyFor(t, a)
	if err := k8s.Create(ctx, own); err != nil {
		t.Fatalf("plant the -auth policy: %v", err)
	}
	foreign := authPolicyFor(t, a)
	foreign.SetLabels(nil)
	r.Client = &swapOnDelete{Client: log, swap: func(ctx context.Context) error {
		if err := k8s.Delete(ctx, own.DeepCopy()); err != nil {
			return err
		}
		return k8s.Create(ctx, foreign.DeepCopy())
	}}

	if err := k8s.Delete(ctx, a); err != nil {
		t.Fatalf("delete agent: %v", err)
	}
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(a)}); err == nil {
		t.Error("teardown deleted a policy it had not inspected, or reported success without deleting")
	}
	got := policyExists(t, own.GetNamespace(), own.GetName())
	if got == nil || got.GetLabels()[compiler.LabelAgentUID] != "" {
		t.Fatalf("the same-named policy that replaced the Agent's was deleted (now: %v). The name alone "+
			"is no authority to delete (design 03 §3.2)", got)
	}

	r.Client = log
	reconcileOnce(t, r, a)
	if policyExists(t, own.GetNamespace(), own.GetName()) == nil {
		t.Error("the foreign policy was deleted on the next pass")
	}
	if !agentGone(t, a) {
		t.Error("the finalizer did not release")
	}
}

// --- the startup refusal ---------------------------------------------------

// Design 03 §3.1: "before any AgentgatewayPolicy is emitted it must also read
// AgentgatewayPolicy". A second control plane that serves the Gateway API and
// NOT agentgateway: the operator declared ON must refuse to start there, and
// name the kind. The main control plane serves both, and there it starts
// (TestThePolicyWatchRequeuesItsAgent builds its manager there).
func TestTheOperatorRefusesToStartWithoutAgentgatewayPolicy(t *testing.T) {
	env := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd"), gatewayCRDs},
		ErrorIfCRDPathMissing: true,
	}
	restCfg, err := env.Start()
	if err != nil {
		t.Fatalf("start a control plane without agentgateway: %v", err)
	}
	t.Cleanup(func() { _ = env.Stop() })

	mgr, err := ctrl.NewManager(restCfg, ctrl.Options{
		Scheme: scheme, Metrics: metricsserver.Options{BindAddress: "0"}, HealthProbeBindAddress: "0",
		Controller: ctrlconfig.Controller{SkipNameValidation: ptrTo(true)},
	})
	if err != nil {
		t.Fatalf("build manager: %v", err)
	}
	r, err := controller.NewAgentReconciler(mgr.GetClient(), mgr.GetAPIReader(), mgr.GetScheme(),
		operatorNamespace, func() bool { return false }, labelAuthorityPresent, controller.InjectedEnvConfig{},
		controller.GatewayConfig{Enabled: true, Name: "assayd", Namespace: "assayd-gateway",
			HostnameSuffix: controller.DefaultGatewayHostnameSuffix, ServingURL: testServingURL})
	if err != nil {
		t.Fatalf("build reconciler: %v", err)
	}
	err = r.SetupWithManager(mgr)
	if err == nil {
		t.Fatal("the operator set up with the gateway declared on a cluster that serves no " +
			"AgentgatewayPolicy. Its teardown reads each Agent's -auth policy, so no Agent there could " +
			"be deleted, and nothing would say why (design 03 §3.1)")
	}
	for _, want := range []string{"AgentgatewayPolicy", compiler.PolicyAPIVersion, "design 03 §3.1", "gateway.enabled"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}

	// The same cluster, declared OFF: nothing there reads the kind, so nothing
	// may refuse for want of it. The declared-ungoverned tier (§3.1) is what P1
	// ships, and it has no reason to carry agentgateway's CRDs.
	off, err := ctrl.NewManager(restCfg, ctrl.Options{
		Scheme: scheme, Metrics: metricsserver.Options{BindAddress: "0"}, HealthProbeBindAddress: "0",
		Controller: ctrlconfig.Controller{SkipNameValidation: ptrTo(true)},
	})
	if err != nil {
		t.Fatalf("build manager: %v", err)
	}
	disabled, err := controller.NewAgentReconciler(off.GetClient(), off.GetAPIReader(), off.GetScheme(),
		operatorNamespace, func() bool { return false }, labelAuthorityPresent, controller.InjectedEnvConfig{},
		controller.GatewayConfig{})
	if err != nil {
		t.Fatalf("build reconciler: %v", err)
	}
	if err := disabled.SetupWithManager(off); err != nil {
		t.Errorf("with gateway.enabled false the operator refused to set up on a cluster without "+
			"agentgateway: %v. The refusal belongs to the declared-on tier only", err)
	}
}

// The policy watch reads through a cache that holds only policies carrying
// assayd.dev/agent, the label it maps by. The manager's cache would hold every
// AgentgatewayPolicy in the cluster.
func TestTheGatewayWatchCacheHoldsOnlyPoliciesItCanMap(t *testing.T) {
	ctx := context.Background()
	ns := newNamespace(t)
	build := func(agent string, labelled bool) *unstructured.Unstructured {
		p, err := compiler.AuthPolicy(compiler.AuthInput{AgentName: agent, AgentNamespace: "payments",
			AgentUID: "0f0e0d0c-0000-0000-0000-00000000cafe", RunNamespace: ns})
		if err != nil {
			t.Fatal(err)
		}
		if !labelled {
			p.SetLabels(map[string]string{compiler.LabelAgentNamespace: "payments"})
		}
		if err := k8s.Create(ctx, p); err != nil {
			t.Fatalf("create %s: %v", p.GetName(), err)
		}
		return p
	}
	mapped, foreign := build("mapped", true), build("foreign", false)

	opts, err := controller.GatewayWatchCacheOptions(cache.Options{Scheme: scheme}, ns)
	if err != nil {
		t.Fatal(err)
	}
	c, err := cache.New(cfg, opts)
	if err != nil {
		t.Fatalf("build cache: %v", err)
	}
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() { _ = c.Start(cctx) }()

	var names []string
	eventually(t, "the cache to hold the policy that carries assayd.dev/agent", func() bool {
		list := &unstructured.UnstructuredList{}
		list.SetGroupVersionKind(controller.AgentgatewayPolicyGVK.GroupVersion().WithKind(compiler.PolicyKind + "List"))
		if err := c.List(ctx, list, client.InNamespace(ns)); err != nil {
			return false
		}
		names = names[:0]
		for _, it := range list.Items {
			names = append(names, it.GetName())
		}
		return contains(names, mapped.GetName())
	})
	if contains(names, foreign.GetName()) {
		t.Errorf("the gateway watch cache holds %s, which carries no assayd.dev/agent and maps to no "+
			"Agent: %v", foreign.GetName(), names)
	}
}

// --- the watches -----------------------------------------------------------

// agentReads counts the reconciler's reads of each Agent. Every reconcile
// starts with one, so a count that rises is a reconcile that happened.
type agentReads struct {
	client.Client
	mu    sync.Mutex
	reads map[types.NamespacedName]int
}

func (c *agentReads) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if _, ok := obj.(*assaydv1alpha1.Agent); ok {
		c.mu.Lock()
		c.reads[key]++
		c.mu.Unlock()
	}
	return c.Client.Get(ctx, key, obj, opts...)
}

func (c *agentReads) count(key types.NamespacedName) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reads[key]
}

// startGatewayManager runs a manager with the gateway declared ON, against the
// main control plane, which serves both kinds. Setting it up there is the
// other half of the startup refusal: the refusal is for a cluster missing the
// kind, not for every cluster.
//
// Its cache is scoped to this test's Agent namespace, its run namespace and
// the operator's. The control plane is shared, and every earlier test leaves
// Agents behind: a manager watching all of them reconciles each one with the
// gateway on, which since slice PR 4 means running its `Create`, writes and
// probes included, on the one worker. This test's Agent then waited behind
// them past its window, and the watch under test was never what failed.
// Scoping takes nothing from the claim, which is about the watches, and the
// gateway watch cache is built from its own options and is not narrowed.
func startGatewayManager(t *testing.T, gwNS string) *agentReads {
	t.Helper()
	agentNS := nsName(t.Name())
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme, Metrics: metricsserver.Options{BindAddress: "0"}, HealthProbeBindAddress: "0",
		Controller: ctrlconfig.Controller{SkipNameValidation: ptrTo(true)},
		Cache: cache.Options{DefaultNamespaces: map[string]cache.Config{
			agentNS: {}, runNS(agentNS): {}, operatorNamespace: {},
		}},
	})
	if err != nil {
		t.Fatalf("build manager: %v", err)
	}
	reads := &agentReads{Client: mgr.GetClient(), reads: map[types.NamespacedName]int{}}
	r, err := controller.NewAgentReconciler(reads, mgr.GetAPIReader(), mgr.GetScheme(),
		operatorNamespace, func() bool { return false }, labelAuthorityPresent, controller.InjectedEnvConfig{},
		controller.GatewayConfig{Enabled: true, Name: "assayd", Namespace: gwNS,
			HostnameSuffix: controller.DefaultGatewayHostnameSuffix, ServingURL: testServingURL})
	if err != nil {
		t.Fatalf("build reconciler: %v", err)
	}
	r.CardFetchTimeout = 100 * time.Millisecond
	if err := r.SetupWithManager(mgr); err != nil {
		t.Fatalf("the operator refused to set up on a cluster that serves HTTPRoute AND "+
			"AgentgatewayPolicy: %v", err)
	}
	runManager(t, mgr)
	return reads
}

func runManager(t *testing.T, mgr manager.Manager) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- mgr.Start(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("manager exited with error: %v", err)
			}
		case <-time.After(30 * time.Second):
			t.Error("manager did not shut down within 30s")
		}
	})
	if !mgr.GetCache().WaitForCacheSync(ctx) {
		t.Fatal("cache never synced")
	}
}

// waitQuiet waits until the Agent has been reconciled and then left alone for
// a while, and returns the count at that point.
func waitQuiet(t *testing.T, reads *agentReads, key types.NamespacedName) int {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	last, since := reads.count(key), time.Now()
	for time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
		n := reads.count(key)
		if n != last {
			last, since = n, time.Now()
			continue
		}
		if n > 0 && time.Since(since) >= 1500*time.Millisecond {
			return n
		}
	}
	t.Fatalf("the Agent %s never settled (reads: %d)", key, last)
	return 0
}

// assertNoReconcile holds for a window in which nothing may requeue the Agent.
func assertNoReconcile(t *testing.T, reads *agentReads, key types.NamespacedName, before int, what string) {
	t.Helper()
	time.Sleep(2 * time.Second)
	if n := reads.count(key); n != before {
		t.Errorf("%s requeued the Agent (reads %d → %d)", what, before, n)
	}
}

// externalAgent is an Agent whose reconcile writes status once and then
// returns without a requeue, so any later reconcile is one a watch caused.
func externalAgent(t *testing.T, ns, name string) *assaydv1alpha1.Agent {
	t.Helper()
	return mustCreateAgent(t, ns, name, func(a *assaydv1alpha1.Agent) {
		a.Spec.Runtime = nil
		a.Spec.External = &assaydv1alpha1.ExternalAgent{Endpoint: "https://agent.example.com"}
	})
}

// §3.2: "an AgentgatewayPolicy watch", mapped "by the assayd.dev/agent and
// assayd.dev/agent-namespace labels", as the route watch is.
func TestThePolicyWatchRequeuesItsAgent(t *testing.T) {
	ns := newNamespace(t)
	reads := startGatewayManager(t, ns)
	a := externalAgent(t, ns, "watched")
	provisionRunNamespace(t, ns)
	key := client.ObjectKeyFromObject(a)
	before := waitQuiet(t, reads, key)

	// CONTROL: a policy that names this Agent's namespace but carries no
	// assayd.dev/agent maps to nothing. It cannot, whether or not the cache
	// holds it; TestTheGatewayWatchCacheHoldsOnlyPoliciesItCanMap pins that
	// it does not.
	unmapped := authPolicyFor(t, liveAgent(t, a))
	unmapped.SetName("unmapped-auth")
	unmapped.SetLabels(map[string]string{compiler.LabelAgentNamespace: a.Namespace})
	if err := k8s.Create(context.Background(), unmapped); err != nil {
		t.Fatalf("create the unlabelled policy: %v", err)
	}
	assertNoReconcile(t, reads, key, before, "a policy without assayd.dev/agent")

	policy := authPolicyFor(t, liveAgent(t, a))
	if err := k8s.Create(context.Background(), policy); err != nil {
		t.Fatalf("create the -auth policy: %v", err)
	}
	eventually(t, "the policy's creation to requeue its Agent", func() bool { return reads.count(key) > before })
}

func nackEvent(ns, name, reason, policyNS, policy, route string) *corev1.Event {
	return &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		InvolvedObject: corev1.ObjectReference{APIVersion: "gateway.networking.k8s.io/v1", Kind: "Gateway",
			Name: "assayd", Namespace: ns},
		Type:   corev1.EventTypeWarning,
		Reason: reason,
		Source: corev1.EventSource{Component: "agentgateway"},
		// The shape research/agentgateway-v1.4.1-spike.md §2.8 measured.
		Message: fmt.Sprintf(`[{"key":"policy/traffic/%s/%s:auth:%s/%s","error":"rejected"}]`,
			policyNS, policy, policyNS, route),
	}
}

// §3.3: "The operator watches Events on the Gateways it emits to, parses the
// key … and maps it to the owning Agent." Only a NACK, only in the Gateway's
// namespace: the same Event anywhere else, or another Warning there, is not one.
func TestANackInTheGatewaysNamespaceRequeuesTheAgentWhosePolicyItNames(t *testing.T) {
	ctx := context.Background()
	gwNS := nsName(t.Name() + "-gateway")
	elsewhere := nsName(t.Name() + "-elsewhere")
	for _, n := range []string{gwNS, elsewhere} {
		if err := k8s.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: n}}); err != nil &&
			!apierrors.IsAlreadyExists(err) {
			t.Fatalf("create namespace %s: %v", n, err)
		}
	}
	reads := startGatewayManager(t, gwNS)
	ns := newNamespace(t)
	a := externalAgent(t, ns, "nacked")
	run := provisionRunNamespace(t, ns)
	policy := authPolicyFor(t, liveAgent(t, a))
	if err := k8s.Create(ctx, policy); err != nil {
		t.Fatalf("create the -auth policy: %v", err)
	}
	route, _ := compiler.ServingRouteName(a.Name)
	key := client.ObjectKeyFromObject(a)
	before := waitQuiet(t, reads, key)

	// Controls first: each must requeue nothing.
	if err := k8s.Create(ctx, nackEvent(elsewhere, "nack-elsewhere", controller.NackEventReason,
		run, policy.GetName(), route)); err != nil {
		t.Fatalf("create event: %v", err)
	}
	assertNoReconcile(t, reads, key, before, "a NACK in a namespace that is not the Gateway's")
	if err := k8s.Create(ctx, nackEvent(gwNS, "not-a-nack", "SomeOtherWarning",
		run, policy.GetName(), route)); err != nil {
		t.Fatalf("create event: %v", err)
	}
	assertNoReconcile(t, reads, key, before, "a Warning in the Gateway's namespace that is not a NACK")

	if err := k8s.Create(ctx, nackEvent(gwNS, "nack", controller.NackEventReason,
		run, policy.GetName(), route)); err != nil {
		t.Fatalf("create event: %v", err)
	}
	eventually(t, "the NACK to requeue the Agent whose policy it names", func() bool {
		return reads.count(key) > before
	})
}

// --- the serving listener's address ------------------------------------------

// Design 03 §3.3.3 makes --gateway-serving-url required "whenever the compiler
// runs", and the compiler runs whenever the gateway is enabled: every new
// Agent's route is published only after a probe sent there gets 401. Set, it
// must address a listener. With the gateway off it is not required.
func TestAServingURLIsRequiredWithTheGatewayAndChecked(t *testing.T) {
	gw := controller.GatewayConfig{Enabled: true, Name: "assayd", Namespace: "gw",
		HostnameSuffix: controller.DefaultGatewayHostnameSuffix}
	build := func(g controller.GatewayConfig) error {
		_, err := controller.NewAgentReconciler(k8s, k8s, scheme, operatorNamespace,
			func() bool { return false }, labelAuthorityPresent, controller.InjectedEnvConfig{}, g)
		return err
	}
	if err := build(gw); err == nil || !strings.Contains(err.Error(), "--gateway-serving-url") {
		t.Errorf("the gateway was enabled with no --gateway-serving-url and the reconciler was built, "+
			"or refused without naming the flag (%v). No new Agent could ever be published", err)
	}
	if err := build(controller.GatewayConfig{}); err != nil {
		t.Errorf("with the gateway off, an unset --gateway-serving-url was refused: %v", err)
	}
	gw.ServingURL = "http://gw.example:8080"
	if err := build(gw); err != nil {
		t.Errorf("a listener's URL was refused: %v", err)
	}
	gw.ServingURL = "http://gw.example:8080/agents"
	err := build(gw)
	if err == nil || !strings.Contains(err.Error(), "--gateway-serving-url") {
		t.Errorf("a serving URL with a path was accepted, or refused without naming the flag: %v", err)
	}
}
