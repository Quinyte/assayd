// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package envtest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	admissionv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/controller"
	"github.com/Quinyte/assayd/internal/revision"
)

// How the revision Service is READ, and what the operator says when a write to
// it is refused (design 02 §3.2 and §5, A77, answering round three of its
// review).

// staleServiceClient answers a Get of one Service from a snapshot, the way a
// lagging informer cache would, and passes everything else through.
type staleServiceClient struct {
	client.Client
	at    types.NamespacedName
	stale *corev1.Service
}

func (c *staleServiceClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object,
	opts ...client.GetOption) error {
	if svc, ok := obj.(*corev1.Service); ok && key == c.at {
		c.stale.DeepCopyInto(svc)
		return nil
	}
	return c.Client.Get(ctx, key, obj, opts...)
}

// The delete is decided on a LIVE read, and a stale cached one would destroy a
// human's in-place repair.
//
// The UID precondition does not help here: it protects a different object at
// the name, and a repair in place keeps the UID. An earlier comment in
// service.go said no test here could see the difference, because envtest's
// client is uncached; the reconciler's Reader is independent of its Client, so
// a test can give it a stale Client and a live Reader.
func TestAStaleCachedReadDoesNotDeleteAnInPlaceRepair(t *testing.T) {
	ns := newNamespace(t)
	a := noneAgent(t, ns, "stale")
	r := newGatewayReconciler("assayd-gateway", "assayd")
	rev := revision.MustHash(a.Spec)
	svcName := controller.WorkloadName("stale", rev)
	settle(t, r, a)
	markAvailable(t, ns, svcName, 1)
	settle(t, r, a)

	key := types.NamespacedName{Namespace: runNS(ns), Name: svcName}
	headlessByPatchAlone(t, key)
	snapshot := liveService(t, key)
	// A human repairs it in place by the same route the break took: through
	// ExternalName, back to ClusterIP with an allocated address. Same UID.
	patchService(t, key,
		`{"spec":{"type":"ExternalName","externalName":"elsewhere.example.com"}}`,
		`{"spec":{"type":"ClusterIP","externalName":null}}`)
	repaired := liveService(t, key)
	if repaired.UID != snapshot.UID || repaired.Spec.ClusterIP == "" ||
		repaired.Spec.ClusterIP == corev1.ClusterIPNone {
		t.Fatalf("setup: the repair did not leave the same object addressable (%s/%s, %q)",
			snapshot.UID, repaired.UID, repaired.Spec.ClusterIP)
	}

	cached := newGatewayReconciler("assayd-gateway", "assayd")
	cached.Client = &staleServiceClient{Client: k8s, at: key, stale: &snapshot}
	cached.Reader = k8s
	if _, err := cached.Reconcile(context.Background(),
		ctrl.Request{NamespacedName: client.ObjectKeyFromObject(a)}); err != nil {
		t.Logf("reconcile: %v", err)
	}

	if after := liveService(t, key); after.UID != repaired.UID {
		t.Fatalf("a STALE cached read of the headless object deleted the human's in-place repair "+
			"(%s -> %s): the delete must be decided on the live object", repaired.UID, after.UID)
	}
}

// forbidServiceGet refuses the Reader's Get of one Service, as the API server
// does when the operator's RBAC does not grant `get` on services.
type forbidServiceGet struct {
	client.Reader
	at  types.NamespacedName
	err error
}

func (f *forbidServiceGet) Get(ctx context.Context, key client.ObjectKey, obj client.Object,
	opts ...client.GetOption) error {
	if _, ok := obj.(*corev1.Service); ok && key == f.at {
		return f.err
	}
	return f.Reader.Get(ctx, key, obj, opts...)
}

// A refused READ of the revision Service is the operator's own permissions,
// not the Service's fault, and is reported as that.
//
// Round three measured it reported as ServiceRejected with the update arm's
// remedy, "delete that Service" — which would change nothing, because the
// operator still could not read what it recreated. The live read made it newly
// reachable: it needs RBAC `get`, where the informer needed list and watch.
func TestARefusedServiceReadIsReportedAsTheOperatorsOwnAccess(t *testing.T) {
	ns := newNamespace(t)
	a := noneAgent(t, ns, "fget")
	r := newGatewayReconciler("assayd-gateway", "assayd")
	rev := revision.MustHash(a.Spec)
	key := types.NamespacedName{Namespace: runNS(ns), Name: controller.WorkloadName("fget", rev)}
	settle(t, r, a)
	before := liveService(t, key)
	seedGatewayReport(t, a)

	r.Reader = &forbidServiceGet{Reader: k8s, at: key,
		err: apierrors.NewForbidden(schema.GroupResource{Resource: "services"}, key.Name,
			errors.New("the operator's service account may not get services"))}
	res, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(a)})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	got := liveAgentPtr(t, a)
	ready := condition(got, assaydv1alpha1.CondReady)
	if ready == nil || ready.Reason != controller.CondReasonServiceUnreadable {
		t.Fatalf("a refused read is reported as %+v, want %s", ready, controller.CondReasonServiceUnreadable)
	}
	if d := condition(got, assaydv1alpha1.CondDegraded); d == nil || d.Status != metav1.ConditionTrue ||
		d.Reason != controller.CondReasonServiceUnreadable {
		t.Errorf("Degraded is %+v", d)
	}
	if !strings.Contains(ready.Message, "get on services") {
		t.Errorf("the message does not name the missing permission: %s", ready.Message)
	}
	for _, wrong := range []string{"To recover: delete", "repair its own Service in place",
		"no such object to delete"} {
		if strings.Contains(ready.Message, wrong) {
			t.Errorf("the message carries ServiceRejected's remedy %q, about an object that is not "+
				"at fault: %s", wrong, ready.Message)
		}
	}
	if res.RequeueAfter != controller.RefusedServiceRecheck {
		t.Errorf("requeue %v, want %v: a restored permission is an event nothing here watches",
			res.RequeueAfter, controller.RefusedServiceRecheck)
	}
	for _, want := range []struct{ typ, reason string }{
		{string(assaydv1alpha1.CondPolicyApplyIncomplete), "AuthPolicyNotAttached"},
		{string(assaydv1alpha1.CondPolicyCompileFailed), "Budget"},
	} {
		held := condition(got, assaydv1alpha1.ConditionType(want.typ))
		if held == nil || held.Status != metav1.ConditionTrue || held.Reason != want.reason {
			t.Errorf("the unreadable exit retracted %s: %+v", want.typ, held)
		}
	}
	if after := liveService(t, key); after.UID != before.UID || after.ResourceVersion != before.ResourceVersion {
		t.Errorf("the operator wrote to a Service it could not read")
	}

	// A TRANSIENT refusal is left to the queue's backoff and raises nothing.
	r2 := newGatewayReconciler("assayd-gateway", "assayd")
	b := noneAgent(t, ns, "fgett")
	settle(t, newGatewayReconciler("assayd-gateway", "assayd"), b)
	r2.Reader = &forbidServiceGet{Reader: k8s,
		at:  types.NamespacedName{Namespace: runNS(ns), Name: controller.WorkloadName("fgett", rev)},
		err: apierrors.NewServiceUnavailable("the API server is restarting")}
	if _, err := r2.Reconcile(context.Background(),
		ctrl.Request{NamespacedName: client.ObjectKeyFromObject(b)}); err == nil {
		t.Errorf("a transient read error was swallowed rather than retried")
	}
	if c := condition(liveAgentPtr(t, b), assaydv1alpha1.CondReady); c != nil &&
		c.Reason == controller.CondReasonServiceUnreadable {
		t.Errorf("a transient read error was reported as a standing fault: %+v", c)
	}
}

// headlessOnCreate stores every non-dry-run create of one Service headless, as
// a mutating webhook that forces `clusterIP: None` would.
type headlessOnCreate struct {
	client.Client
	at types.NamespacedName
}

func (c *headlessOnCreate) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	if svc, ok := obj.(*corev1.Service); ok && svc.Name == c.at.Name && svc.Namespace == c.at.Namespace {
		co := &client.CreateOptions{}
		co.ApplyOptions(opts)
		if len(co.DryRun) == 0 {
			svc.Spec.ClusterIP = corev1.ClusterIPNone
			svc.Spec.ClusterIPs = []string{corev1.ClusterIPNone}
		}
	}
	return c.Client.Create(ctx, obj, opts...)
}

// A replace whose replacement is stored headless too is not a success.
//
// Returning nil over it let the pass promote and route with Ready=True over a
// replacement as unaddressable as the object it replaced — for one pass, until
// the next one found it headless inside the cooldown and held.
func TestAReplacementStoredHeadlessIsNotASuccess(t *testing.T) {
	ns := newNamespace(t)
	a := noneAgent(t, ns, "rehead")
	r := newGatewayReconciler("assayd-gateway", "assayd")
	rev := revision.MustHash(a.Spec)
	svcName := controller.WorkloadName("rehead", rev)
	settle(t, r, a)
	markAvailable(t, ns, svcName, 1)
	settle(t, r, a)

	key := types.NamespacedName{Namespace: runNS(ns), Name: svcName}
	before := liveService(t, key)
	headlessByPatchAlone(t, key)
	forcing := newGatewayReconciler("assayd-gateway", "assayd")
	forcing.Client = &headlessOnCreate{Client: k8s, at: key}
	if _, err := forcing.Reconcile(context.Background(),
		ctrl.Request{NamespacedName: client.ObjectKeyFromObject(a)}); err != nil {
		t.Logf("reconcile: %v", err)
	}

	after := liveService(t, key)
	if after.UID == before.UID {
		t.Fatal("setup: the replace did not run")
	}
	if after.Spec.ClusterIP != corev1.ClusterIPNone {
		t.Fatalf("setup: the replacement was not stored headless (%q)", after.Spec.ClusterIP)
	}
	ready := condition(liveAgentPtr(t, a), assaydv1alpha1.CondReady)
	if ready == nil || ready.Status != metav1.ConditionFalse ||
		ready.Reason != controller.CondReasonServiceReplaceFailed {
		t.Fatalf("a replacement stored with no ClusterIP reads %+v", ready)
	}
	if !strings.Contains(ready.Message, "was recreated with no ClusterIP") {
		t.Errorf("the message does not say what happened: %s", ready.Message)
	}
}

// refuseServicesByPolicy installs a REAL ValidatingAdmissionPolicy over
// Services in one namespace — "every Service must carry a cost-centre label",
// with Deny — and waits until the API server enforces it.
func refuseServicesByPolicy(t *testing.T, name, namespace string) {
	t.Helper()
	ctx := context.Background()
	p := &admissionv1.ValidatingAdmissionPolicy{ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: admissionv1.ValidatingAdmissionPolicySpec{
			MatchConstraints: &admissionv1.MatchResources{
				NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{
					"kubernetes.io/metadata.name": namespace}},
				ResourceRules: []admissionv1.NamedRuleWithOperations{{
					RuleWithOperations: admissionv1.RuleWithOperations{
						Operations: []admissionv1.OperationType{admissionv1.Create, admissionv1.Update},
						Rule: admissionv1.Rule{APIGroups: []string{""}, APIVersions: []string{"v1"},
							Resources: []string{"services"}},
					},
				}},
			},
			Validations: []admissionv1.Validation{{
				Expression: "has(object.metadata.labels) && 'cost-centre' in object.metadata.labels",
				Message:    "every Service must carry a cost-centre label",
			}},
		}}
	if err := k8s.Create(ctx, p); err != nil {
		t.Fatalf("policy: %v", err)
	}
	t.Cleanup(func() { _ = k8s.Delete(context.Background(), p) })
	b := &admissionv1.ValidatingAdmissionPolicyBinding{ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: admissionv1.ValidatingAdmissionPolicyBindingSpec{PolicyName: name,
			ValidationActions: []admissionv1.ValidationAction{admissionv1.Deny}}}
	if err := k8s.Create(ctx, b); err != nil {
		t.Fatalf("binding: %v", err)
	}
	t.Cleanup(func() { _ = k8s.Delete(context.Background(), b) })
	for i := 0; i < 100; i++ {
		probe := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: "policy-probe"},
			Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{{Port: 80}}}}
		if err := k8s.Create(ctx, probe, client.DryRunAll); err != nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("the admission policy never became active")
}

// seedGatewayReport writes both of design 03's owned conditions as an earlier
// -auth step would, so a test can see whether an exit carries them.
func seedGatewayReport(t *testing.T, a *assaydv1alpha1.Agent) {
	t.Helper()
	live := liveAgentPtr(t, a)
	for _, c := range []metav1.Condition{{
		Type: string(assaydv1alpha1.CondPolicyApplyIncomplete), Status: metav1.ConditionTrue,
		Reason: "AuthPolicyNotAttached", Message: "the Gateway reports the policy attached to nothing",
		ObservedGeneration: live.Generation,
	}, {
		Type: string(assaydv1alpha1.CondPolicyCompileFailed), Status: metav1.ConditionTrue,
		Reason: "Budget", Message: "spec.budget does not compile",
		ObservedGeneration: live.Generation,
	}} {
		meta.SetStatusCondition(&live.Status.Conditions, c)
	}
	if err := k8s.Status().Update(context.Background(), live); err != nil {
		t.Fatalf("seed the report: %v", err)
	}
}

// ServiceRejected, through a REAL admission policy, on both of its arms.
//
// Design 02 §5 called both arms measured when only the create arm was pinned,
// through a fake client. Round three drove both with a real policy, and four
// mutations survived the suite: the update arm given the create remedy, the
// requeue dropped, and either half of the carry dropped. Each row asserts all
// of it.
func TestServiceRejectedByAnAdmissionPolicyOnBothArms(t *testing.T) {
	for _, tc := range []struct {
		name string
		// breakIt runs after the policy is active and puts the Agent in front
		// of the write the policy refuses.
		breakIt       func(t *testing.T, key types.NamespacedName)
		says, notSays string
	}{{
		name: "the update arm",
		// The policy is installed AFTER the drift, so the patch itself is
		// admitted and only the operator's repair is refused.
		breakIt: nil,
		says:    "could not repair its own Service in place",
		notSays: "no such object to delete",
	}, {
		name: "the create arm",
		breakIt: func(t *testing.T, key types.NamespacedName) {
			svc := liveService(t, key)
			if err := k8s.Delete(context.Background(), &svc); err != nil {
				t.Fatalf("delete: %v", err)
			}
		},
		says:    "no such object to delete",
		notSays: "could not repair its own Service in place",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			ns := newNamespace(t)
			name := "vapu"
			if tc.breakIt != nil {
				name = "vapc"
			}
			a := noneAgent(t, ns, name)
			r := newGatewayReconciler("assayd-gateway", "assayd")
			rev := revision.MustHash(a.Spec)
			svcName := controller.WorkloadName(name, rev)
			settle(t, r, a)
			markAvailable(t, ns, svcName, 1)
			settle(t, r, a)
			seedGatewayReport(t, a)

			key := types.NamespacedName{Namespace: runNS(ns), Name: svcName}
			if tc.breakIt == nil {
				patchService(t, key, `{"spec":{"externalIPs":["203.0.113.9"]}}`)
			}
			refuseServicesByPolicy(t, fmt.Sprintf("refuse-services-%s", name), runNS(ns))
			if tc.breakIt != nil {
				tc.breakIt(t, key)
			}

			res, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(a)})
			if err != nil {
				t.Fatalf("reconcile: %v", err)
			}
			got := liveAgentPtr(t, a)
			ready := condition(got, assaydv1alpha1.CondReady)
			if ready == nil || ready.Reason != "ServiceRejected" {
				t.Fatalf("the refused write is reported as %+v, want ServiceRejected", ready)
			}
			if !strings.Contains(ready.Message, "every Service must carry a cost-centre label") {
				t.Errorf("the message does not carry the policy's own words: %s", ready.Message)
			}
			if !strings.Contains(ready.Message, tc.says) || strings.Contains(ready.Message, tc.notSays) {
				t.Errorf("the %s carries the wrong remedy (want %q, not %q): %s",
					tc.name, tc.says, tc.notSays, ready.Message)
			}
			if d := condition(got, assaydv1alpha1.CondDegraded); d == nil || d.Status != metav1.ConditionTrue {
				t.Errorf("Degraded is %+v", d)
			}
			if res.RequeueAfter != controller.RefusedServiceRecheck {
				t.Errorf("requeue %v, want %v: a fixed or exempted policy is an event nothing here "+
					"watches", res.RequeueAfter, controller.RefusedServiceRecheck)
			}
			for _, want := range []struct{ typ, reason string }{
				{string(assaydv1alpha1.CondPolicyApplyIncomplete), "AuthPolicyNotAttached"},
				{string(assaydv1alpha1.CondPolicyCompileFailed), "Budget"},
			} {
				held := condition(got, assaydv1alpha1.ConditionType(want.typ))
				if held == nil || held.Status != metav1.ConditionTrue || held.Reason != want.reason {
					t.Errorf("ServiceRejected retracted %s: %+v", want.typ, held)
				}
			}
			if tc.breakIt == nil {
				if svc := liveService(t, key); len(svc.Spec.ExternalIPs) == 0 {
					t.Errorf("setup: the policy did not refuse the repair")
				}
			}
		})
	}
}
