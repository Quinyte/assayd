// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package envtest

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/compiler"
	"github.com/Quinyte/assayd/internal/controller"
	"github.com/Quinyte/assayd/internal/revision"
)

// The `Create` transaction's edges: each case here was found by the
// independent review of slice PR 4, or by its first e2e run.

func reconcileErr(t *testing.T, r *controller.AgentReconciler, a *assaydv1alpha1.Agent) error {
	t.Helper()
	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(a)})
	return err
}

// A route that is gone while the `Create` waits in Publishing re-enters at
// PreparingRoute (§3.3). Re-created there, it would carry backends at once on
// an object no probe had seen refuse.
func TestARouteGoneInPublishingIsPreparedAgain(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "vanished", nil)
	r, stub := createReconciler()
	promote(t, r, a)
	acceptRoute(t, ns, "vanished")
	acceptPolicy(t, ns, "vanished")
	reconcileOnce(t, r, a)
	if tx := txOf(t, a); tx == nil || tx.Stage != "Publishing" || !routePublished(t, ns, "vanished") {
		t.Fatalf("want the Create published and waiting in Publishing: %+v", tx)
	}
	if err := k8s.Delete(context.Background(), servingRoute(t, ns, "vanished")); err != nil {
		t.Fatal(err)
	}
	stub.hold("vanished", true)
	reconcileOnce(t, r, a)
	rt := servingRoute(t, ns, "vanished")
	if rt == nil {
		t.Fatal("the route was not prepared again")
	}
	if routePublished(t, ns, "vanished") {
		t.Errorf("a route deleted during Publishing came back with backendRefs, before any probe "+
			"saw it refuse: %+v", rt.Spec.Rules)
	}
	if tx := txOf(t, a); tx == nil || tx.Stage == "Publishing" {
		t.Errorf("the Create did not re-enter before its write: %+v", tx)
	}
}

// staleAgent answers every Get of one Agent with an older copy, as an informer
// cache that has not yet seen the operator's own last status write does.
type staleAgent struct {
	client.Client
	stale *assaydv1alpha1.Agent
}

func (s *staleAgent) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if a, ok := obj.(*assaydv1alpha1.Agent); ok && key == client.ObjectKeyFromObject(s.stale) {
		s.stale.DeepCopyInto(a)
		return nil
	}
	return s.Client.Get(ctx, key, obj, opts...)
}

// A pass acts on status.auth only as the API server holds it. A cached Agent
// still at ProbingAfter, read after the Create reached Served, would strip the
// published route back to a prepared one.
func TestAStaleCachedAgentWritesNothingToTheGateway(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "stale", nil)
	r, stub := createReconciler()
	stub.hold("stale", true)
	promote(t, r, a)
	acceptRoute(t, ns, "stale")
	acceptPolicy(t, ns, "stale")
	reconcileOnce(t, r, a)
	old := liveAgent(t, a)
	if old.Status.Auth.Transaction.Stage != "ProbingAfter" {
		t.Fatalf("want a snapshot at ProbingAfter: %+v", old.Status.Auth)
	}
	driveToServed(t, r, stub, a)

	r.Client = &staleAgent{Client: k8s, stale: old}
	r.Reader = k8s
	_ = reconcileErr(t, r, a)
	if !routePublished(t, ns, "stale") {
		t.Error("a pass that read a stale Agent stripped the published route of its backendRefs")
	}
	if auth := authOf(t, a); auth == nil || auth.Mode != "apikey" {
		t.Errorf("a stale pass changed the served record: %+v", auth)
	}
}

// pruneAuth drops status.auth from every status write, as an Agent CRD from
// before slice PR 1 does: `helm upgrade` never updates a chart's crds/.
type pruneAuth struct{ client.Client }

func (p *pruneAuth) Status() client.SubResourceWriter { return &pruneAuthWriter{p.Client.Status()} }

type pruneAuthWriter struct{ client.SubResourceWriter }

func (w *pruneAuthWriter) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	if a, ok := obj.(*assaydv1alpha1.Agent); ok {
		a.Status.Auth = nil
	}
	return w.SubResourceWriter.Update(ctx, obj, opts...)
}

// Measured by slice PR 4's first e2e run on a reused cluster: a transaction
// whose record is pruned re-enters from nothing on every pass, and the route it
// finally publishes reads as Adopt's. A record that does not come back stops
// the pass before anything is written to the gateway.
func TestARecordTheCRDDropsStopsTheTransaction(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "pruned", nil)
	r, _ := createReconciler()
	r.Client = &pruneAuth{Client: k8s}
	reconcileOnce(t, r, a)
	reconcileOnce(t, r, a)
	markAvailable(t, ns, controller.WorkloadName("pruned", revision.MustHash(a.Spec)), 1)
	if err := reconcileErr(t, r, a); err == nil || !strings.Contains(err.Error(), "crds/") {
		t.Fatalf("a pruned status.auth did not stop the pass with the fix named: %v", err)
	}
	if rt := servingRoute(t, ns, "pruned"); rt != nil {
		t.Errorf("a route was written although its transaction's record was not kept: %+v", rt.Spec)
	}
	name, _ := compiler.AuthPolicyName("pruned")
	if policyExists(t, runNS(ns), name) != nil {
		t.Error("a policy was written although its transaction's record was not kept")
	}
	condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "AuthRecordNotKept")
}

// A status restored under a new Agent UID records a digest no policy the
// compiler renders can equal (A69's open question). The record wins: nothing
// is written, and the reason names the cause.
func TestAnUnrenderableTargetWritesNothing(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "restored", nil)
	r, _ := createReconciler()
	promote(t, r, a)
	name, _ := compiler.AuthPolicyName("restored")
	before := policyExists(t, runNS(ns), name)
	live := liveAgent(t, a)
	live.Status.Auth.Transaction.TargetDigest = "sha256:" + strings.Repeat("0", 64)
	if err := k8s.Status().Update(context.Background(), live); err != nil {
		t.Fatal(err)
	}
	if err := reconcileErr(t, r, a); err == nil {
		t.Fatal("a target this build cannot render was applied")
	}
	condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "AuthTargetUnrenderable")
	if after := policyExists(t, runNS(ns), name); after.GetResourceVersion() != before.GetResourceVersion() {
		t.Error("the policy was rewritten for a target the transaction did not record")
	}
}

// A policy at this Agent's -auth name that another Agent owns is refused and
// left as it is, by the route's collision rule (§3.2).
func TestAPolicyAnotherAgentOwnsIsNotTakenOver(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "claims", nil)
	r, _ := createReconciler()
	reconcileOnce(t, r, a)
	reconcileOnce(t, r, a)
	foreign := authPolicyFor(t, liveAgent(t, a))
	foreign.SetLabels(map[string]string{compiler.LabelAgent: "incumbent",
		compiler.LabelAgentNamespace: ns, compiler.LabelAgentUID: "uid-of-another-agent"})
	if err := unstructured.SetNestedStringSlice(foreign.Object, []string{`apiKey.group == "other"`},
		"spec", "traffic", "authorization", "policy", "matchExpressions"); err != nil {
		t.Fatal(err)
	}
	if err := k8s.Create(context.Background(), foreign); err != nil {
		t.Fatal(err)
	}
	markAvailable(t, ns, controller.WorkloadName("claims", revision.MustHash(a.Spec)), 1)
	err := reconcileErr(t, r, a)
	if err == nil || !strings.Contains(err.Error(), "incumbent") || !strings.Contains(err.Error(), "claims") {
		t.Fatalf("a policy another Agent owns was converged, or refused without naming both: %v", err)
	}
	got := policyExists(t, runNS(ns), foreign.GetName())
	if got.GetLabels()[compiler.LabelAgentUID] != "uid-of-another-agent" {
		t.Errorf("another Agent's policy was rewritten: %v", got.GetLabels())
	}
}

// swallowPolicyCreate reports every policy create as done and writes nothing.
type swallowPolicyCreate struct{ client.Client }

func (c *swallowPolicyCreate) Create(ctx context.Context, o client.Object, opts ...client.CreateOption) error {
	if u, ok := o.(*unstructured.Unstructured); ok && u.GetKind() == compiler.PolicyKind {
		return nil
	}
	return c.Client.Create(ctx, o, opts...)
}

// A write that does not keep sends the Create back and forth between the
// write and the check. One pass stops after a bounded number of steps, and
// says why, rather than spinning on the worker.
func TestAWriteThatDoesNotKeepCannotSpinThePass(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "spins", nil)
	r, _ := createReconciler()
	r.Client = &swallowPolicyCreate{Client: k8s}
	promote(t, r, a)
	c := condition(liveAgent(t, a), assaydv1alpha1.CondReady)
	if c == nil || !strings.Contains(c.Message, "did not keep") {
		t.Errorf("a pass whose policy write never kept did not say so: %+v", c)
	}
}

// A kind this operator does not run, `Narrow`, is left as found, loudly:
// nothing here knows how to finish one. So is a `Lock` with no target, which
// no code here enters: it is neither abandoned, which would publish a K2
// Lock's route, nor run, which would re-assert it (slice PR 5's review).
func TestATransactionKindThisOperatorDoesNotRunIsLeftAlone(t *testing.T) {
	for i, kind := range []string{"Narrow", "Lock"} {
		t.Run(kind, func(t *testing.T) {
			ns := newNamespace(t)
			name := []string{"foreignkind", "targetless"}[i]
			a := mustCreateAgent(t, ns, name, nil)
			r, _ := createReconciler()
			reconcileOnce(t, r, a)
			reconcileOnce(t, r, a)
			live := liveAgent(t, a)
			live.Status.Auth = &assaydv1alpha1.AuthStatus{Transaction: &assaydv1alpha1.AuthTransaction{
				Kind: kind, Stage: "Converging"}}
			if err := k8s.Status().Update(context.Background(), live); err != nil {
				t.Fatal(err)
			}
			markAvailable(t, ns, controller.WorkloadName(name, revision.MustHash(a.Spec)), 1)
			if err := reconcileErr(t, r, a); err == nil || !strings.Contains(err.Error(), kind) {
				t.Fatalf("a %s in status.auth was acted on, or refused without naming it: %v", kind, err)
			}
			if servingRoute(t, ns, name) != nil {
				t.Errorf("a route was written beside a %s this operator does not run", kind)
			}
		})
	}
}
