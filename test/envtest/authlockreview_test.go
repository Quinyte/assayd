// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package envtest

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/compiler"
	"github.com/Quinyte/assayd/internal/controller"
)

// The independent review of slice PR 5 found four Lock-side rules, and three
// more, that passed the suite with the rule removed. Each case here pins one.

// j2InProbingAfter is a served none Agent whose J2 Lock has written its
// policy, converged it, and waits in ProbingAfter with the stub held.
func j2InProbingAfter(t *testing.T, name string) (*assaydv1alpha1.Agent, *controller.AgentReconciler, *stubProber) {
	t.Helper()
	a, r, stub := servedNoneAgent(t, name, nil)
	recordCardFor(t, a, a.Status.ActiveRevision, a.Status.ActiveRevisionDigest)
	stub.hold(name, true)
	toMode(t, a, "apikey")
	lockPass(t, r, a)
	acceptPolicy(t, a.Namespace, name)
	lockPass(t, r, a)
	if tx := txOf(t, a); tx == nil || tx.Kind != "Lock" || tx.Stage != "ProbingAfter" || !tx.Written {
		t.Fatalf("want a J2 Lock waiting in ProbingAfter with its policy written: %+v", tx)
	}
	return a, r, stub
}

// failPolicyDelete refuses the first delete of an AgentgatewayPolicy: the
// operator dies between the abandonment's delete and its status update.
type failPolicyDelete struct {
	client.Client
	failed bool
}

func (c *failPolicyDelete) Delete(ctx context.Context, o client.Object, opts ...client.DeleteOption) error {
	if u, ok := o.(*unstructured.Unstructured); ok && u.GetKind() == compiler.PolicyKind && !c.failed {
		c.failed = true
		return errors.New("the operator stopped here")
	}
	return c.Client.Delete(ctx, o, opts...)
}

// Review MAJOR 1(a): for a Lock the policy is deleted BEFORE the abandoning
// status update (§3.3.3, A67). A delete that fails leaves the Lock in the slot
// with written set, so status never says open beside an operator-written
// policy, and the next pass finishes the abandonment.
func TestALocksPolicyIsDeletedBeforeItsAbandoningStatusUpdate(t *testing.T) {
	a, r, _ := j2InProbingAfter(t, "abandonorder")
	r.Client = &failPolicyDelete{Client: k8s}
	toMode(t, a, "none")
	if err := reconcileErr(t, r, a); err == nil {
		t.Fatal("the refused delete returned no error")
	}
	if tx := txOf(t, a); tx == nil || tx.Kind != "Lock" || !tx.Written {
		t.Fatalf("the abandoning status update ran before the delete succeeded: %+v. Status then says "+
			"the route is open beside a policy no rule deletes", authOf(t, a))
	}
	if policyExists(t, runNS(a.Namespace), policyNameOf(a)) == nil {
		t.Fatal("the crash was not simulated: the policy is gone")
	}
	reconcileOnce(t, r, a)
	if policyExists(t, runNS(a.Namespace), policyNameOf(a)) != nil {
		t.Error("the re-run abandonment did not delete the policy")
	}
	if auth := authOf(t, a); auth.Mode != "none" || auth.Transaction != nil {
		t.Errorf("the re-run abandonment did not return status to {mode: none}: %+v", auth)
	}
}

// Review MAJOR 1(b): a Lock evaluates no probe answer while a foreign traffic
// policy targets its route (§3.2), because either policy may have answered.
func TestALockTakesNoAnswerUnderAForeignPolicy(t *testing.T) {
	a, r, stub := j2InProbingAfter(t, "lockforeign")
	p := authPolicyFor(t, liveAgent(t, a))
	p.SetName("intruder")
	p.SetLabels(nil)
	if err := k8s.Create(context.Background(), p); err != nil {
		t.Fatalf("plant the foreign policy: %v", err)
	}
	stub.hold("lockforeign", false)
	lockPass(t, r, a)
	if auth := authOf(t, a); auth.Mode != "none" || auth.Transaction == nil {
		t.Fatalf("a Lock reached Served on a 401 a foreign policy may have produced: %+v", auth)
	}
	condIs(t, a, assaydv1alpha1.CondGovernanceSkipped, metav1.ConditionTrue, "ForeignTrafficPolicy")
	if err := k8s.Delete(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	lockPass(t, r, a)
	if auth := authOf(t, a); auth.Mode != "apikey" || auth.Transaction != nil {
		t.Errorf("the Lock did not reach Served once the foreign policy went: %+v", auth)
	}
}

// Review MAJOR 1(c): a genuine, fresh NACK returns a Lock to Converging and
// raises AuthLockUnverified at once, before the deadline, with Ready False.
func TestANackReturnsALockToConverging(t *testing.T) {
	ensureGatewayNamespace(t)
	a, r, stub := j2InProbingAfter(t, "locknacked")
	nackFor(t, a, "genuine-"+a.Namespace, controller.AgentgatewayControllerName, time.Now().Add(2*time.Second))
	lockPass(t, r, a)
	if tx := txOf(t, a); tx == nil || tx.Stage != "Converging" {
		t.Fatalf("a NACK did not return the Lock to Converging: %+v", tx)
	}
	condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "AuthLockUnverified")
	condIs(t, a, assaydv1alpha1.CondReady, metav1.ConditionFalse, "AuthLockUnverified")
	stub.hold("locknacked", false)
	lockPass(t, r, a)
	if auth := authOf(t, a); auth.Mode != "none" || auth.Transaction == nil {
		t.Errorf("a Lock reached Served while a genuine NACK of its policy stood: %+v", auth)
	}
}

// Review MAJOR 1(d): an abandonment whose policy delete a finalizer holds is
// not done. The Lock stays in the slot under AuthAbandonWaiting, and finishes
// once the object is gone.
func TestAnAbandonmentHeldByAFinalizerKeepsTheLock(t *testing.T) {
	a, r, _ := j2InProbingAfter(t, "abandonheld")
	p := policyExists(t, runNS(a.Namespace), policyNameOf(a))
	p.SetFinalizers([]string{"example.com/hold"})
	if err := k8s.Update(context.Background(), p); err != nil {
		t.Fatalf("hold the policy: %v", err)
	}
	toMode(t, a, "none")
	lockPass(t, r, a)
	if tx := txOf(t, a); tx == nil || tx.Kind != "Lock" {
		t.Fatalf("an abandonment whose policy is still there was taken as done: %+v", authOf(t, a))
	}
	condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "AuthAbandonWaiting")
	p = policyExists(t, runNS(a.Namespace), policyNameOf(a))
	p.SetFinalizers(nil)
	if err := k8s.Update(context.Background(), p); err != nil {
		t.Fatalf("release the policy: %v", err)
	}
	lockPass(t, r, a)
	if auth := authOf(t, a); auth.Mode != "none" || auth.Transaction != nil {
		t.Errorf("the abandonment did not finish once the policy went: %+v", auth)
	}
}

// writeOrder records the route strips and the gateway deletes the operator
// makes, in order.
type writeOrder struct {
	client.Client
	mu   sync.Mutex
	seen []string
}

func (c *writeOrder) record(s string) {
	c.mu.Lock()
	c.seen = append(c.seen, s)
	c.mu.Unlock()
}

func (c *writeOrder) Update(ctx context.Context, o client.Object, opts ...client.UpdateOption) error {
	if rt, ok := o.(*gatewayv1.HTTPRoute); ok && (len(rt.Spec.Rules) == 0 || len(rt.Spec.Rules[0].BackendRefs) == 0) {
		c.record("strip")
	}
	return c.Client.Update(ctx, o, opts...)
}

func (c *writeOrder) Delete(ctx context.Context, o client.Object, opts ...client.DeleteOption) error {
	if u, ok := o.(*unstructured.Unstructured); ok {
		c.record(u.GetKind())
	}
	return c.Client.Delete(ctx, o, opts...)
}

// Review MINOR 1: an abandoned Create whose route its Publishing attached
// loses the route's backendRefs before its policy goes, for a new mode that
// compiles too, and is then published under the successor.
func TestAnAbandonedPublishedCreateStripsItsRouteFirst(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "abandonstrip", nil)
	r, _ := createReconciler()
	promote(t, r, a)
	acceptRoute(t, ns, "abandonstrip")
	acceptPolicy(t, ns, "abandonstrip")
	reconcileOnce(t, r, a)
	if tx := txOf(t, a); tx.Stage != "Publishing" || !routePublished(t, ns, "abandonstrip") {
		t.Fatalf("want Publishing with the route published: %+v", tx)
	}
	order := &writeOrder{Client: k8s}
	r.Client = order
	toMode(t, a, "none")
	reconcileOnce(t, r, a)
	order.mu.Lock()
	seen := strings.Join(order.seen, ",")
	order.mu.Unlock()
	if !strings.HasPrefix(seen, "strip,"+compiler.PolicyKind) {
		t.Errorf("the writes were %q; the route must lose its backendRefs before the policy goes", seen)
	}
	if !routePublished(t, ns, "abandonstrip") || marker(t, a) != "none" {
		t.Errorf("the successor Create of none did not publish with its marker (%q)", marker(t, a))
	}
}

// Review MINOR 1: a pass that returns before the -auth step keeps a J2 Lock's
// AuthLockPending, and does not claim a missing policy.
func TestAnEarlyReturnKeepsAJ2LocksPending(t *testing.T) {
	a, r, _ := j2InProbingAfter(t, "earlyj2")
	mustEdit(t, a, func(x *assaydv1alpha1.Agent) {
		x.Spec.Runtime.EnvFrom = []corev1.EnvFromSource{{ConfigMapRef: &corev1.ConfigMapEnvSource{
			LocalObjectReference: corev1.LocalObjectReference{Name: "not-there"}}}}
	})
	reconcileOnce(t, r, a)
	if c := condition(liveAgent(t, a), assaydv1alpha1.CondEnvSourceUnresolved); c == nil {
		t.Fatal("the pass did not take the early return this test is about")
	}
	condIs(t, a, assaydv1alpha1.CondGovernanceSkipped, metav1.ConditionTrue, "AuthLockPending")
	if c := condition(liveAgent(t, a), assaydv1alpha1.CondPolicyApplyIncomplete); c != nil {
		t.Errorf("a J2 Lock before its deadline carries PolicyApplyIncomplete on an early return: %+v", c)
	}
}

// Review MINOR 1: a NACK is counted only when seen strictly after the
// policy's last write, so one in the second of the write is not.
func TestANackInTheSecondOfTheWriteIsNotCounted(t *testing.T) {
	ensureGatewayNamespace(t)
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "nacksame", nil)
	r, stub := createReconciler()
	stub.hold("nacksame", true)
	promote(t, r, a)
	acceptRoute(t, ns, "nacksame")
	acceptPolicy(t, ns, "nacksame")
	reconcileOnce(t, r, a)
	var written time.Time
	for _, mf := range policyExists(t, runNS(ns), policyNameOf(a)).GetManagedFields() {
		if mf.Subresource == "" && mf.Time != nil && mf.Time.After(written) {
			written = mf.Time.Time
		}
	}
	if written.IsZero() {
		t.Fatal("the policy carries no managedFields time")
	}
	nackFor(t, a, "same-"+ns, controller.AgentgatewayControllerName, written)
	reconcileOnce(t, r, a)
	if c := condition(liveAgent(t, a), assaydv1alpha1.CondPolicyApplyIncomplete); c != nil {
		t.Errorf("a NACK in the second of the write was counted: %+v", c)
	}
}
