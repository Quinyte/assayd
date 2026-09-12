// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package envtest

import (
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/compiler"
	"github.com/Quinyte/assayd/internal/controller"
	"github.com/Quinyte/assayd/internal/revision"
)

// The third independent review of slice PR 5 (the re-review of PR #31): its
// MINORs 1, 2, 3, 4 and 5.

// cacheMissesRoute answers every read of one route NotFound, as an informer
// cache that does not hold it yet does. The uncached reader is not wrapped.
type cacheMissesRoute struct {
	client.Client
	name string
}

func (c *cacheMissesRoute) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if _, ok := obj.(*gatewayv1.HTTPRoute); ok && key.Name == c.name {
		return apierrors.NewNotFound(schema.GroupResource{Group: gatewayv1.GroupName, Resource: "httproutes"}, key.Name)
	}
	return c.Client.Get(ctx, key, obj, opts...)
}

// MINOR 1: the abandonment's own reads are live. With a cache that reports the
// route gone while the API server holds it published, and a finalizer that
// holds its delete, no pass leaves a published route without a policy, and
// no pass records Adopt.
func TestAnAbandonmentWhoseCacheMissesTheRouteStillStripsAndWaits(t *testing.T) {
	a, r := publishingCreate(t, "cachemiss")
	ns := a.Namespace
	routeName, _ := compiler.ServingRouteName("cachemiss")
	rt := servingRoute(t, ns, "cachemiss")
	rt.Finalizers = append(rt.Finalizers, "example.com/hold")
	if err := k8s.Update(context.Background(), rt); err != nil {
		t.Fatalf("hold the route: %v", err)
	}
	r.Reader = k8s
	r.Client = &cacheMissesRoute{Client: k8s, name: routeName}
	toMode(t, a, "oauth")

	check := func(pass int) {
		t.Helper()
		if routePublished(t, ns, "cachemiss") && policyExists(t, runNS(ns), policyNameOf(a)) == nil {
			t.Fatalf("pass %d left the route published with no policy", pass)
		}
		if tx := txOf(t, a); tx != nil && tx.Kind == "Adopt" {
			t.Fatalf("pass %d recorded Adopt: %+v", pass, tx)
		}
	}
	for i := 0; i < 3; i++ {
		if err := reconcileErr(t, r, a); err != nil {
			t.Fatalf("pass %d: %v", i, err)
		}
		check(i)
		if i == 0 {
			// The read that confirms the route is gone is live too: the stripped
			// route is still in the API, so nothing past it happens yet (the
			// fourth review of slice PR 5).
			if tx := txOf(t, a); tx == nil || tx.Kind != "Create" {
				t.Fatalf("pass 0 let go of the Create while the route is still in the API: %+v", authOf(t, a))
			}
			condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "AuthAbandonWaiting")
			if policyExists(t, runNS(ns), policyNameOf(a)) == nil {
				t.Fatal("pass 0 deleted the policy while the route is still in the API")
			}
		}
	}
	if routePublished(t, ns, "cachemiss") {
		t.Error("the route whose delete is held was not stripped")
	}
	rt = servingRoute(t, ns, "cachemiss")
	rt.Finalizers = nil
	if err := k8s.Update(context.Background(), rt); err != nil {
		t.Fatalf("release the route: %v", err)
	}
	for i := 3; i < 6; i++ {
		if err := reconcileErr(t, r, a); err != nil {
			t.Fatalf("pass %d: %v", i, err)
		}
		check(i)
	}
	if servingRoute(t, ns, "cachemiss") != nil {
		t.Error("a route came back after the abandonment")
	}
	if auth := authOf(t, a); auth != nil {
		t.Errorf("status.auth is %+v after the abandonment finished", auth)
	}
}

// plantAtOwnName writes a policy at a's -auth name carrying uid, targeting
// route.
func plantAtOwnName(t *testing.T, a *assaydv1alpha1.Agent, uid, route string) {
	t.Helper()
	p := authPolicyFor(t, liveAgent(t, a))
	l := p.GetLabels()
	l[compiler.LabelAgentUID] = uid
	p.SetLabels(l)
	if err := unstructured.SetNestedSlice(p.Object, []any{map[string]any{
		"group": gatewayv1.GroupName, "kind": "HTTPRoute", "name": route}}, "spec", "targetRefs"); err != nil {
		t.Fatal(err)
	}
	if err := k8s.Create(context.Background(), p); err != nil {
		t.Fatalf("plant the policy at the -auth name: %v", err)
	}
}

// MINOR 3(a) and MINOR 2: a J2 Lock beside a leftover policy at its own name
// holds at ApplyingPolicies under ForeignTrafficPolicy, with no write failure,
// and an edit away from apikey abandons it without claiming the leftover was
// deleted: the message names the policy that remains.
func TestAJ2LockBesideALeftoverAtItsNameHoldsAndNamesWhatRemains(t *testing.T) {
	const oldUID = "uid-of-a-deleted-predecessor"
	a, r, _ := servedNoneAgent(t, "leftoverj2", nil)
	route, _ := compiler.ServingRouteName("leftoverj2")
	plantAtOwnName(t, a, oldUID, route)
	toMode(t, a, "apikey")
	lockPass(t, r, a)
	if tx := txOf(t, a); tx == nil || tx.Kind != "Lock" || tx.Stage != "ApplyingPolicies" {
		t.Fatalf("want the J2 Lock held at ApplyingPolicies: %+v", tx)
	}
	condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "ForeignTrafficPolicy")
	if p := policyExists(t, runNS(a.Namespace), policyNameOf(a)); p.GetLabels()[compiler.LabelAgentUID] != oldUID {
		t.Fatalf("the Lock took the leftover over: %v", p.GetLabels())
	}

	toMode(t, a, "oauth")
	lockPass(t, r, a)
	if auth := authOf(t, a); auth.Mode != "none" || auth.Transaction != nil {
		t.Fatalf("the Lock was not abandoned: %+v", auth)
	}
	c := condIs(t, a, assaydv1alpha1.CondPolicyCompileFailed, metav1.ConditionTrue, "AuthInputAbsent")
	if c != nil && (strings.Contains(c.Message, "was deleted") || !strings.Contains(c.Message, policyNameOf(a)) ||
		!strings.Contains(c.Message, "is still there")) {
		t.Errorf("the message claims a deletion that did not happen, or does not name what remains: %s", c.Message)
	}
}

// MINOR 3(b) and MINOR 4: a served apikey Agent whose policy lost its UID
// label, and was weakened, holds under ForeignTrafficPolicy with no write
// failure, is not re-asserted, and is told how to recover.
func TestAServedPolicyWithoutItsUIDIsReportedAndNotReasserted(t *testing.T) {
	a, r, _ := servedAPIKeyAgent(t, "uidstripped")
	p := policyExists(t, runNS(a.Namespace), policyNameOf(a))
	l := p.GetLabels()
	delete(l, compiler.LabelAgentUID)
	p.SetLabels(l)
	if err := unstructured.SetNestedStringSlice(p.Object, []string{"true"},
		"spec", "traffic", "authorization", "policy", "matchExpressions"); err != nil {
		t.Fatal(err)
	}
	if err := k8s.Update(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	reconcileOnce(t, r, a)
	c := condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "ForeignTrafficPolicy")
	if c != nil && (!strings.Contains(c.Message, "own -auth name") || !strings.Contains(c.Message, "delete it")) {
		t.Errorf("the report does not say the policy sits at the Agent's own name, or how to recover: %s", c.Message)
	}
	got := policyExists(t, runNS(a.Namespace), policyNameOf(a))
	if _, ok := got.GetLabels()[compiler.LabelAgentUID]; ok {
		t.Error("a policy without this Agent's UID was re-asserted as its own")
	}
	if !routePublished(t, a.Namespace, "uidstripped") {
		t.Error("the published route was withdrawn")
	}
}

// MINOR 3(c): a predecessor's policy at the -auth name that targets another
// route is not detected as a foreign traffic policy on this route, and
// policyCollision still refuses to take it over.
func TestAPredecessorAtTheNameThatTargetsElsewhereIsNotTakenOver(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "elsewhere", nil)
	r, _ := createReconciler()
	reconcileOnce(t, r, a)
	reconcileOnce(t, r, a)
	plantAtOwnName(t, a, "uid-of-a-deleted-predecessor", "someone-elses-serving")
	markAvailable(t, ns, controller.WorkloadName("elsewhere", revision.MustHash(a.Spec)), 1)
	err := reconcileErr(t, r, a)
	if err == nil || !strings.Contains(err.Error(), "does not carry its UID") {
		t.Fatalf("a predecessor's policy at the -auth name was taken over, or refused without saying why: %v", err)
	}
	p := policyExists(t, runNS(ns), policyNameOf(a))
	if p.GetLabels()[compiler.LabelAgentUID] != "uid-of-a-deleted-predecessor" {
		t.Errorf("the predecessor's policy was rewritten: %v", p.GetLabels())
	}
}

// rawNack is a genuine NACK named name, naming policy key, seen at at.
func rawNack(t *testing.T, name, key string, at time.Time) {
	t.Helper()
	ev := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "assayd-gateway"},
		InvolvedObject: corev1.ObjectReference{APIVersion: "gateway.networking.k8s.io/v1", Kind: "Gateway",
			Name: "assayd", Namespace: "assayd-gateway"},
		Type: corev1.EventTypeWarning, Reason: controller.NackEventReason,
		Source:              corev1.EventSource{Component: controller.AgentgatewayControllerName},
		ReportingController: controller.AgentgatewayControllerName, ReportingInstance: "agentgateway-0",
		FirstTimestamp: metav1.NewTime(at), LastTimestamp: metav1.NewTime(at), Count: 1,
		Message: `[{"key":"policy/traffic/` + key + `:auth:x/y","error":"rejected"}]`,
	}
	if err := k8s.Create(context.Background(), ev); err != nil {
		t.Fatalf("create the NACK: %v", err)
	}
	t.Cleanup(func() { _ = k8s.Delete(context.Background(), ev) })
}

// MINOR 5: NACK Events past the page cap are noted on the transaction's own
// condition, and the page loop reads every page up to the cap. With one Event
// a page, a cap of one leaves the genuine NACK on page two unseen and says
// so; a cap of two reads it.
func TestTheNackPageCapIsSurfacedAndThePagesAreRead(t *testing.T) {
	ensureGatewayNamespace(t)
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "nackcap", nil)
	r, stub := createReconciler()
	stub.hold("nackcap", true)
	r.NackPageSize, r.NackMaxPages = 1, 1
	promote(t, r, a)
	acceptRoute(t, ns, "nackcap")
	acceptPolicy(t, ns, "nackcap")
	reconcileOnce(t, r, a)
	if tx := txOf(t, a); tx.Stage != "ProbingAfter" {
		t.Fatalf("want ProbingAfter: %+v", tx)
	}
	later := time.Now().Add(2 * time.Second)
	rawNack(t, "aaa-"+ns, runNS(ns)+"/someone-else-auth", later)
	rawNack(t, "zzz-"+ns, runNS(ns)+"/"+policyNameOf(a), later)

	reconcileOnce(t, r, a)
	if tx := txOf(t, a); tx.Stage != "ProbingAfter" {
		t.Errorf("a NACK past the cap moved the transaction: %+v", tx)
	}
	c := condition(liveAgent(t, a), assaydv1alpha1.CondReady)
	if c == nil || !strings.Contains(c.Message, "were not inspected") {
		t.Errorf("the transaction's condition does not say NACKs past the cap were not inspected: %+v", c)
	}

	r.NackMaxPages = 2
	reconcileOnce(t, r, a)
	condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "AuthEnforcementUnverified")
	if tx := txOf(t, a); tx.Stage != "Converging" {
		t.Errorf("the genuine NACK on page two was not read: %+v", tx)
	}
}
