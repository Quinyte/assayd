// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package envtest

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/compiler"
	"github.com/Quinyte/assayd/internal/controller"
	"github.com/Quinyte/assayd/internal/revision"
)

// The second independent review of slice PR 5 (PR #31): its BLOCKER's two
// reproductions, and MINORs 2, 3 and 6.

// publishingCreate is a new API-key Agent whose Create has published its route
// and waits in Publishing for the published route to converge.
func publishingCreate(t *testing.T, name string) (*assaydv1alpha1.Agent, *controller.AgentReconciler) {
	t.Helper()
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, name, nil)
	r, _ := createReconciler()
	promote(t, r, a)
	acceptRoute(t, ns, name)
	acceptPolicy(t, ns, name)
	reconcileOnce(t, r, a)
	if tx := txOf(t, a); tx == nil || tx.Stage != "Publishing" || !routePublished(t, ns, name) {
		t.Fatalf("want Publishing with the route published: %+v", tx)
	}
	return a, r
}

// BLOCKER, reproduction (1): a finalizer holds the route's delete when the
// owner's edit to oauth abandons the Create. The route loses its backendRefs
// first; the policy stays and status.auth keeps the Create until a live read
// finds the route gone; and once it is gone, nothing comes back as Adopt's.
func TestAnAbandonmentToOAuthWaitsForItsRouteToGo(t *testing.T) {
	a, r := publishingCreate(t, "heldroute")
	ns := a.Namespace
	rt := servingRoute(t, ns, "heldroute")
	rt.Finalizers = append(rt.Finalizers, "example.com/hold")
	if err := k8s.Update(context.Background(), rt); err != nil {
		t.Fatalf("hold the route: %v", err)
	}
	toMode(t, a, "oauth")
	reconcileOnce(t, r, a)

	rt = servingRoute(t, ns, "heldroute")
	if rt == nil {
		t.Fatal("the route is gone despite its finalizer; the experiment did not run")
	}
	if rt.DeletionTimestamp == nil {
		t.Error("the abandonment to oauth did not delete the route")
	}
	if routePublished(t, ns, "heldroute") {
		t.Error("the route whose delete is held still carries backendRefs: it was not stripped first")
	}
	if policyExists(t, runNS(ns), policyNameOf(a)) == nil {
		t.Error("the policy was deleted while the route is still in the API")
	}
	if tx := txOf(t, a); tx == nil || tx.Kind != "Create" {
		t.Fatalf("status.auth let go of the Create while its route is still in the API: %+v", authOf(t, a))
	}
	condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "AuthAbandonWaiting")
	reconcileOnce(t, r, a)
	if tx := txOf(t, a); tx == nil || tx.Kind != "Create" {
		t.Fatalf("a second pass beside the held route recorded %+v", authOf(t, a))
	}

	rt = servingRoute(t, ns, "heldroute")
	rt.Finalizers = nil
	if err := k8s.Update(context.Background(), rt); err != nil {
		t.Fatalf("release the route: %v", err)
	}
	for i := 0; i < 3; i++ {
		reconcileOnce(t, r, a)
	}
	if servingRoute(t, ns, "heldroute") != nil {
		t.Errorf("a route came back after the abandonment: published=%v, marker=%q",
			routePublished(t, ns, "heldroute"), marker(t, a))
	}
	if policyExists(t, runNS(ns), policyNameOf(a)) != nil {
		t.Error("the abandoned Create's policy was kept")
	}
	if auth := authOf(t, a); auth != nil {
		t.Errorf("status.auth is %+v; an abandoned never-served Create to oauth keeps nothing, and "+
			"is never Adopt's", auth)
	}
}

// staleRouteOnce answers the first read of one route with a copy saved
// earlier, as an informer cache that has not yet seen its delete does, and
// every later read from the API.
type staleRouteOnce struct {
	client.Client
	mu    sync.Mutex
	saved *gatewayv1.HTTPRoute
}

func (c *staleRouteOnce) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	c.mu.Lock()
	s := c.saved
	if rt, ok := obj.(*gatewayv1.HTTPRoute); ok && s != nil && key.Name == s.Name && key.Namespace == s.Namespace {
		c.saved = nil
		c.mu.Unlock()
		s.DeepCopyInto(rt)
		return nil
	}
	c.mu.Unlock()
	return c.Client.Get(ctx, key, obj, opts...)
}

// BLOCKER, reproduction (2): one stale route read after the abandonment. A
// trigger reading the cache sees the deleted route published beside an empty
// record, records Adopt, and re-creates the route published with no policy.
// Adopt's trigger reads live.
func TestAStaleRouteReadAfterAnAbandonmentIsNotAdopt(t *testing.T) {
	a, r := publishingCreate(t, "staleroute")
	ns := a.Namespace
	saved := servingRoute(t, ns, "staleroute").DeepCopy()
	toMode(t, a, "oauth")
	reconcileOnce(t, r, a)
	if servingRoute(t, ns, "staleroute") != nil || authOf(t, a) != nil {
		t.Fatalf("the abandonment did not finish: auth=%+v", authOf(t, a))
	}

	r.Reader = k8s
	r.Client = &staleRouteOnce{Client: k8s, saved: saved}
	reconcileOnce(t, r, a)
	r.Client = k8s
	reconcileOnce(t, r, a)
	if tx := txOf(t, a); tx != nil {
		t.Errorf("a stale route read after the abandonment recorded %+v", tx)
	}
	if servingRoute(t, ns, "staleroute") != nil {
		t.Errorf("a route came back after the abandonment: published=%v", routePublished(t, ns, "staleroute"))
	}
}

// swapAgentCRD writes the Agent CRD's spec and waits until the API server
// serves it and applies the budget rule as asked, as budget_ratchet_test.go
// does.
func swapAgentCRD(t *testing.T, ns string, spec *apiextensionsv1.CustomResourceDefinitionSpec, budgetAdmitted bool) {
	t.Helper()
	ctx := context.Background()
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
	eventually(t, "the swapped Agent CRD to serve", func() bool {
		err := k8s.Create(ctx, budgetedAgent(ns, "probe"), client.DryRunAll)
		if budgetAdmitted {
			return err == nil
		}
		return err != nil && strings.Contains(err.Error(), budgetRefusedMessage)
	})
}

// MINOR 2: a desired apikey never overwrites refusedMode. When K2 is blocked,
// here by a stored spec.budget, the owner's consent survives the block, and
// removing the budget enters the Lock.
func TestAK2BlockedByABudgetKeepsTheOwnersConsent(t *testing.T) {
	a, r, _ := legacyAgent(t, "ktwobudget", noneSpec)
	if tx := txOf(t, a); tx.RefusedMode != "none" {
		t.Fatalf("refusedMode is %q, want none", tx.RefusedMode)
	}

	var installed apiextensionsv1.CustomResourceDefinition
	if err := k8s.Get(context.Background(), client.ObjectKey{Name: "agents.assayd.dev"}, &installed); err != nil {
		t.Fatal(err)
	}
	current := installed.Spec.DeepCopy()
	t.Cleanup(func() { swapAgentCRD(t, a.Namespace, current, false) })
	before := current.DeepCopy()
	for i := range before.Versions {
		root := before.Versions[i].Schema.OpenAPIV3Schema
		spec := root.Properties["spec"]
		kept := spec.XValidations[:0]
		for _, v := range spec.XValidations {
			if v.Rule != "!has(self.budget)" {
				kept = append(kept, v)
			}
		}
		spec.XValidations = kept
		root.Properties["spec"] = spec
	}
	// The edit to apikey lands beside a budget, stored under the CRD from
	// before the refusal, which is then put back.
	swapAgentCRD(t, a.Namespace, before, true)
	mustEdit(t, a, func(x *assaydv1alpha1.Agent) {
		x.Spec.Expose = &assaydv1alpha1.ExposeSpec{A2A: &assaydv1alpha1.ExposeProtocol{Auth: "apikey"}}
		tokens := int64(1000)
		x.Spec.Budget = &assaydv1alpha1.BudgetSpec{TokensPerDay: &tokens}
	})
	swapAgentCRD(t, a.Namespace, current, false)

	reconcileOnce(t, r, a)
	tx := txOf(t, a)
	if tx == nil || tx.Kind != "Adopt" || tx.RefusedMode != "none" {
		t.Fatalf("a desired apikey blocked by a stored spec.budget changed the refusal to %+v; the "+
			"owner's consent is lost with it", tx)
	}
	g := condIs(t, a, assaydv1alpha1.CondGovernanceSkipped, metav1.ConditionTrue, "CompilerUpgradeUnsupported")
	if g != nil && !strings.Contains(g.Message, "spec.budget") {
		t.Errorf("the refusal does not say to remove spec.budget first: %s", g.Message)
	}

	mustEdit(t, a, func(x *assaydv1alpha1.Agent) { x.Spec.Budget = nil })
	lockPass(t, r, a)
	if tx := txOf(t, a); tx == nil || tx.Kind != "Lock" {
		t.Errorf("removing the budget did not enter K2's Lock: %+v", tx)
	}
}

// MINOR 3: one ownership rule for every mode. A policy at <agent>-auth that
// does not carry this Agent's UID, here a deleted predecessor's, is
// ForeignTrafficPolicy, and is neither taken over nor deleted.
func TestALeftoverPolicyOfAPredecessorIsForeign(t *testing.T) {
	const oldUID = "uid-of-a-deleted-predecessor"
	leftover := func(t *testing.T, a *assaydv1alpha1.Agent) {
		t.Helper()
		p := authPolicyFor(t, liveAgent(t, a))
		l := p.GetLabels()
		l[compiler.LabelAgentUID] = oldUID
		p.SetLabels(l)
		if err := k8s.Create(context.Background(), p); err != nil {
			t.Fatalf("plant the predecessor's policy: %v", err)
		}
	}
	untouched := func(t *testing.T, a *assaydv1alpha1.Agent) {
		t.Helper()
		p := policyExists(t, runNS(a.Namespace), policyNameOf(a))
		if p == nil || p.GetLabels()[compiler.LabelAgentUID] != oldUID {
			t.Errorf("the predecessor's policy was deleted or taken over: %v", p)
		}
		condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "ForeignTrafficPolicy")
		condIs(t, a, assaydv1alpha1.CondGovernanceSkipped, metav1.ConditionTrue, "ForeignTrafficPolicy")
	}
	t.Run("an auth: none Agent", func(t *testing.T) {
		a, r, _ := servedNoneAgent(t, "leftovernone", nil)
		leftover(t, a)
		reconcileOnce(t, r, a)
		untouched(t, a)
	})
	t.Run("an apikey Agent", func(t *testing.T) {
		ns := newNamespace(t)
		a := mustCreateAgent(t, ns, "leftoverkey", nil)
		r, _ := createReconciler()
		reconcileOnce(t, r, a)
		reconcileOnce(t, r, a)
		leftover(t, a)
		markAvailable(t, ns, controller.WorkloadName("leftoverkey", revision.MustHash(a.Spec)), 1)
		reconcileOnce(t, r, a)
		reconcileOnce(t, r, a)
		untouched(t, a)
		if routePublished(t, ns, "leftoverkey") {
			t.Error("a route was published beside a predecessor's policy at its -auth name")
		}
	})
}

// MINOR 6: a Create in Publishing whose route it already published, which then
// meets a foreign policy, says the route is published and not withdrawn,
// never that it stays unpublished.
func TestAForeignPolicyAtAPublishedCreateSaysItIsPublished(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "foreignpublished", nil)
	r, _ := createReconciler()
	r.AuthDeadline = time.Millisecond
	promote(t, r, a)
	acceptRoute(t, ns, "foreignpublished")
	acceptPolicy(t, ns, "foreignpublished")
	reconcileOnce(t, r, a)
	if tx := txOf(t, a); tx.Stage != "Publishing" || !routePublished(t, ns, "foreignpublished") {
		t.Fatalf("want Publishing with the route published: %+v", tx)
	}
	p := authPolicyFor(t, liveAgent(t, a))
	p.SetName("intruder")
	p.SetLabels(nil)
	if err := k8s.Create(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	reconcileOnce(t, r, a)
	if !routePublished(t, ns, "foreignpublished") {
		t.Fatal("a foreign policy withdrew a published route")
	}
	c := condition(liveAgent(t, a), assaydv1alpha1.CondPolicyApplyIncomplete)
	if c == nil || strings.Contains(c.Message, "stays unpublished") ||
		strings.Contains(c.Message, "so it is not published") ||
		!strings.Contains(c.Message, "is published and is not withdrawn") {
		t.Errorf("the message is not true of a published route: %+v", c)
	}
}
