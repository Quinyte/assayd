// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package envtest

import (
	"context"
	"strings"
	"testing"

	admissionv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/controller"
	"github.com/Quinyte/assayd/internal/revision"
)

// Design 02 A42/A60: the operator-owned run namespace and the binding record
// that proves the operator created it. Each test below pins one row of §3.2's
// ordered check list or one step of its teardown protocol, and is wrong in
// exactly one way, so a mutation that deletes the predicate it covers cannot
// be masked by a neighbouring one.
//
// envtest has no namespace controller: a deleted namespace goes Terminating
// and never disappears. So every "the namespace is deleted" assertion here is
// against a deletion timestamp, and the steps past it are the e2e's.

func getBinding(t *testing.T, ns string) *corev1.ConfigMap {
	t.Helper()
	var cm corev1.ConfigMap
	key := types.NamespacedName{Namespace: operatorNamespace, Name: controller.BindingName(runNS(ns))}
	if err := k8s.Get(context.Background(), key, &cm); err != nil {
		t.Fatalf("get binding record %s: %v", key.Name, err)
	}
	return &cm
}

func getRunNamespace(t *testing.T, ns string) *corev1.Namespace {
	t.Helper()
	var run corev1.Namespace
	if err := k8s.Get(context.Background(), types.NamespacedName{Name: runNS(ns)}, &run); err != nil {
		t.Fatalf("get run namespace %s: %v", runNS(ns), err)
	}
	return &run
}

func runNamespaceCondition(t *testing.T, a *assaydv1alpha1.Agent, reason string) *metav1.Condition {
	t.Helper()
	var live assaydv1alpha1.Agent
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &live); err != nil {
		t.Fatalf("get agent: %v", err)
	}
	c := condition(&live, assaydv1alpha1.CondRunNamespaceUnavailable)
	if c == nil || c.Status != metav1.ConditionTrue {
		t.Fatalf("RunNamespaceUnavailable is not True; phase=%s conditions=%+v",
			live.Status.Phase, live.Status.Conditions)
	}
	if c.Reason != reason {
		t.Fatalf("RunNamespaceUnavailable reason is %q, want %q:\n%s", c.Reason, reason, c.Message)
	}
	return c
}

// nothingWritten asserts the operator wrote no material and no workload into a
// namespace it refused — the property every refusal row exists for.
func nothingWritten(t *testing.T, a *assaydv1alpha1.Agent, ns string) {
	t.Helper()
	var live assaydv1alpha1.Agent
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &live); err != nil {
		t.Fatalf("get agent: %v", err)
	}
	var ds appsv1.DeploymentList
	if err := k8s.List(context.Background(), &ds, client.InNamespace(ns)); err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(ds.Items) != 0 {
		t.Errorf("%d workloads were written into a namespace the operator refused", len(ds.Items))
	}
	var cms corev1.ConfigMapList
	if err := k8s.List(context.Background(), &cms, client.InNamespace(ns),
		client.MatchingLabels{controller.MaterialAgentUIDLabel: string(live.UID)}); err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(cms.Items) != 0 {
		t.Errorf("%d copies were written into a namespace the operator refused", len(cms.Items))
	}
	if live.Status.ActiveRevision != "" || live.Status.CandidateRevision != "" {
		t.Error("a revision was minted with nowhere to put its material")
	}
}

// The base case: workload and material land in the run namespace, nothing
// lands in the Agent's, and the binding is Bound to the namespace's UID.
func TestWorkloadAndMaterialLiveInTheRunNamespace(t *testing.T) {
	ns := newNamespace(t)
	a := agentWithPrompt(t, ns, "moved")
	r := newReconciler(false)
	settle(t, r, a)
	rev := revisionOf(t, ns, a.Spec)

	run := getRunNamespace(t, ns)
	for k, want := range map[string]string{
		controller.LabelRunNamespace:   "true",
		controller.LabelPodsBy:         controller.PodsByAgentOperator,
		controller.LabelAgentNamespace: ns,
	} {
		if run.Labels[k] != want {
			t.Errorf("run namespace label %s=%q, want %q", k, run.Labels[k], want)
		}
	}
	if run.Annotations[controller.AnnotationBindingNonce] == "" {
		t.Error("the run namespace carries no binding nonce")
	}
	b := getBinding(t, ns)
	if b.Data["state"] != "Bound" || b.Data["runNamespaceUID"] != string(run.UID) {
		t.Errorf("binding is %s bound to %q; want Bound to %s", b.Data["state"], b.Data["runNamespaceUID"], run.UID)
	}
	if b.Data["nonce"] != run.Annotations[controller.AnnotationBindingNonce] {
		t.Error("the binding's nonce is not the one stamped on the namespace")
	}

	var d appsv1.Deployment
	if err := k8s.Get(context.Background(),
		types.NamespacedName{Namespace: runNS(ns), Name: controller.WorkloadName("moved", rev)}, &d); err != nil {
		t.Fatalf("workload is not in the run namespace: %v", err)
	}
	var cm corev1.ConfigMap
	if err := k8s.Get(context.Background(),
		types.NamespacedName{Namespace: runNS(ns), Name: controller.MaterialName("moved", rev, 0)}, &cm); err != nil {
		t.Fatalf("material is not in the run namespace: %v", err)
	}
	// Nothing in the Agent's own namespace but the user's objects.
	var ds appsv1.DeploymentList
	if err := k8s.List(context.Background(), &ds, client.InNamespace(ns)); err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(ds.Items) != 0 {
		t.Errorf("%d workloads are still in the Agent's namespace, where a namespace editor can reach them", len(ds.Items))
	}
	var copies corev1.ConfigMapList
	if err := k8s.List(context.Background(), &copies, client.InNamespace(ns),
		client.MatchingLabels{controller.MaterialRevisionLabel: rev}); err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(copies.Items) != 0 {
		t.Errorf("%d copies are still in the Agent's namespace, where delete-and-recreate is one verb away", len(copies.Items))
	}
}

// Check 2: a run namespace with no binding is one this operator did not make.
func TestAPreCreatedRunNamespaceIsRefused(t *testing.T) {
	ns := newNamespace(t)
	planted := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: runNS(ns),
		Labels: map[string]string{controller.LabelRunNamespace: "true", controller.LabelPodsBy: controller.PodsByAgentOperator}}}
	if err := k8s.Create(context.Background(), planted); err != nil {
		t.Fatalf("pre-create: %v", err)
	}
	a := agentWithPrompt(t, ns, "precreated")
	got := settle(t, newReconciler(false), a)

	c := runNamespaceCondition(t, a, controller.ReasonNotCreatedByOperator)
	if got.Status.Phase != assaydv1alpha1.PhaseDegraded {
		t.Errorf("phase is %s, want Degraded: a refused namespace is terminal", got.Status.Phase)
	}
	if !strings.Contains(c.Message, runNS(ns)) || !strings.Contains(c.Message, "To recover") {
		t.Errorf("the message does not name the namespace and the recovery:\n%s", c.Message)
	}
	nothingWritten(t, a, runNS(ns))
	if _, err := getBindingMaybe(ns); err == nil {
		t.Error("a binding was written for a namespace the operator refused")
	}
}

func getBindingMaybe(ns string) (*corev1.ConfigMap, error) {
	var cm corev1.ConfigMap
	err := k8s.Get(context.Background(),
		types.NamespacedName{Namespace: operatorNamespace, Name: controller.BindingName(runNS(ns))}, &cm)
	return &cm, err
}

// Check 10: the binding is Creating and a namespace with that name appeared
// carrying no nonce, or someone else's — the pre-creation attack that raced the
// binding write.
func TestANamespaceWithTheWrongNonceIsRefusedWhileCreating(t *testing.T) {
	ns := newNamespace(t)
	a := agentWithPrompt(t, ns, "wrongnonce")
	r := newReconciler(false)
	// One pass adds the finalizer; the binding is written on the next. Stop the
	// operator between the binding write and the namespace create by planting
	// the namespace after the record exists and before the namespace does.
	reconcileOnce(t, r, a)
	// Write the record ourselves in Creating, as check 3 does.
	rec := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
		Name: controller.BindingName(runNS(ns)), Namespace: operatorNamespace,
		Labels: map[string]string{controller.LabelBinding: "true"}},
		Data: map[string]string{"schemaVersion": "1", "sourceNamespace": ns,
			"sourceNamespaceUID": string(getNamespace(t, ns).UID), "runNamespace": runNS(ns),
			"nonce": "0123456789abcdef0123456789abcdef", "state": "Creating"}}
	if err := k8s.Create(context.Background(), rec); err != nil {
		t.Fatalf("plant binding: %v", err)
	}
	planted := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: runNS(ns),
		Annotations: map[string]string{controller.AnnotationBindingNonce: "ffffffffffffffffffffffffffffffff"}}}
	if err := k8s.Create(context.Background(), planted); err != nil {
		t.Fatalf("plant namespace: %v", err)
	}
	settle(t, r, a)
	runNamespaceCondition(t, a, controller.ReasonNotCreatedByOperator)
	nothingWritten(t, a, runNS(ns))
	if b := getBinding(t, ns); b.Data["state"] != "Creating" {
		t.Errorf("the binding moved to %s on a namespace whose nonce it does not know", b.Data["state"])
	}
}

func getNamespace(t *testing.T, name string) *corev1.Namespace {
	t.Helper()
	var n corev1.Namespace
	if err := k8s.Get(context.Background(), types.NamespacedName{Name: name}, &n); err != nil {
		t.Fatalf("get namespace %s: %v", name, err)
	}
	return &n
}

// Check 9: the crash gap. The binding is Creating and a namespace carrying ITS
// nonce exists — a namespace this operator created and did not record, or a
// copy of it. It is never bound on the strength of being observed: it is
// deleted, the nonce rotates, and a fresh one is created.
func TestANamespaceObservedWhileCreatingIsDeletedNotAdopted(t *testing.T) {
	ns := newNamespace(t)
	a := agentWithPrompt(t, ns, "crashgap")
	r := newReconciler(false)
	reconcileOnce(t, r, a)
	nonce := "0123456789abcdef0123456789abcdef"
	rec := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
		Name: controller.BindingName(runNS(ns)), Namespace: operatorNamespace,
		Labels: map[string]string{controller.LabelBinding: "true"}},
		Data: map[string]string{"schemaVersion": "1", "sourceNamespace": ns,
			"sourceNamespaceUID": string(getNamespace(t, ns).UID), "runNamespace": runNS(ns),
			"nonce": nonce, "state": "Creating"}}
	if err := k8s.Create(context.Background(), rec); err != nil {
		t.Fatalf("plant binding: %v", err)
	}
	// The recreator who read the nonce inside the gap, with the RoleBinding
	// it will add a moment later.
	planted := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: runNS(ns),
		Annotations: map[string]string{controller.AnnotationBindingNonce: nonce}}}
	if err := k8s.Create(context.Background(), planted); err != nil {
		t.Fatalf("plant namespace: %v", err)
	}
	reconcileOnce(t, r, a)

	run := getRunNamespace(t, ns)
	if run.DeletionTimestamp.IsZero() {
		t.Fatal("a namespace observed while the binding was Creating was kept. Nothing proves who " +
			"created it, and a successor that binds it adopts whatever a recreator planted")
	}
	b := getBinding(t, ns)
	if b.Data["state"] != "Creating" || b.Data["nonce"] == nonce {
		t.Errorf("binding is %s with nonce %s; want Creating with a ROTATED nonce", b.Data["state"], b.Data["nonce"])
	}
	nothingWritten(t, a, runNS(ns))
}

// Check 13: the binding is Bound and the namespace's UID is not the one it
// recorded. The nonce was copied — it is public once stamped — and that must
// not matter: a recreator cannot reproduce a UID.
func TestADeletedAndRecreatedRunNamespaceIsRefusedEvenWithTheNonceCopied(t *testing.T) {
	ns := newNamespace(t)
	a := agentWithPrompt(t, ns, "recreated")
	r := newReconciler(false)
	settle(t, r, a)
	run := getRunNamespace(t, ns)
	b := getBinding(t, ns)

	// Simulate the recreate: envtest cannot finish a namespace deletion, so the
	// binding is re-pointed at a namespace that is not the one it recorded —
	// which is exactly what a recreated namespace looks like to the operator.
	// Everything else, labels and nonce included, is identical.
	b.Data["runNamespaceUID"] = "00000000-0000-0000-0000-000000000000"
	if err := k8s.Update(context.Background(), b); err != nil {
		t.Fatalf("repoint binding: %v", err)
	}
	if run.Annotations[controller.AnnotationBindingNonce] != b.Data["nonce"] {
		t.Fatal("test premise: the nonce must match, so only the UID can refuse")
	}
	settle(t, r, a)
	c := runNamespaceCondition(t, a, controller.ReasonNotCreatedByOperator)
	if !strings.Contains(c.Message, "UID") {
		t.Errorf("refused for a reason other than the UID:\n%s", c.Message)
	}
}

// Check 4: two source namespaces that map to one run name. The first tenant
// holds it; the second is refused and nothing of the first is touched.
func TestANameCollisionIsTerminalForTheSecondTenantOnly(t *testing.T) {
	ns := newNamespace(t)
	a := agentWithPrompt(t, ns, "second")
	r := newReconciler(false)
	reconcileOnce(t, r, a)
	// A binding for this run name that belongs to ANOTHER source namespace.
	rec := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
		Name: controller.BindingName(runNS(ns)), Namespace: operatorNamespace,
		Labels: map[string]string{controller.LabelBinding: "true"}},
		Data: map[string]string{"schemaVersion": "1", "sourceNamespace": "some-other-tenant",
			"sourceNamespaceUID": "11111111-1111-1111-1111-111111111111", "runNamespace": runNS(ns),
			"runNamespaceUID": "22222222-2222-2222-2222-222222222222",
			"nonce":           "0123456789abcdef0123456789abcdef", "state": "Bound"}}
	if err := k8s.Create(context.Background(), rec); err != nil {
		t.Fatalf("plant binding: %v", err)
	}
	settle(t, r, a)
	c := runNamespaceCondition(t, a, controller.ReasonNameCollision)
	if !strings.Contains(c.Message, "some-other-tenant") {
		t.Errorf("the message does not name both tenants:\n%s", c.Message)
	}
	b := getBinding(t, ns)
	if b.Data["sourceNamespace"] != "some-other-tenant" || b.Data["state"] != "Bound" {
		t.Error("the first tenant's binding was touched")
	}
}

// Check 5: the source namespace was deleted and recreated, so its old run
// namespace belongs to a tenant that no longer exists. It is torn down and
// the Agent waits — and the dead tenant's leftover workloads, which are why
// this check is reachable at all, do not keep it alive.
func TestARecreatedSourceNamespaceTearsDownTheOldRunNamespace(t *testing.T) {
	ns := newNamespace(t)
	a := agentWithPrompt(t, ns, "tenantv2")
	r := newReconciler(false)
	settle(t, r, a)
	b := getBinding(t, ns)
	// The record says the source namespace had a different UID: it was
	// recreated. Its leftovers — this Agent's workload under the OLD tenant's
	// UID — are in the run namespace.
	b.Data["sourceNamespaceUID"] = "33333333-3333-3333-3333-333333333333"
	if err := k8s.Update(context.Background(), b); err != nil {
		t.Fatalf("repoint binding: %v", err)
	}
	got := settle(t, r, a)
	runNamespaceCondition(t, a, controller.ReasonTerminating)
	if got.Status.Phase != assaydv1alpha1.PhasePending {
		t.Errorf("phase %s, want Pending: Terminating clears by itself", got.Status.Phase)
	}
	if run := getRunNamespace(t, ns); run.DeletionTimestamp.IsZero() {
		t.Fatal("the old tenant's run namespace was kept: its leftover workloads counted as " +
			"evidence to keep it, though they belong to no Agent under the current source UID")
	}
	if b := getBinding(t, ns); b.Data["state"] != "Deleting" {
		t.Errorf("binding is %s, want Deleting", b.Data["state"])
	}
}

// Teardown step 1–3: the LAST Agent's finalizer swaps the binding to
// Terminating before the confirming list, and the handler deletes the
// namespace. A second live Agent keeps it.
func TestTheLastAgentTearsDownTheRunNamespaceAndAnotherKeepsIt(t *testing.T) {
	ns := newNamespace(t)
	a := agentWithPrompt(t, ns, "first")
	b2 := mustCreateAgent(t, ns, "second", nil)
	r := newReconciler(false)
	settle(t, r, a)
	settle(t, r, b2)

	if err := k8s.Delete(context.Background(), a); err != nil {
		t.Fatalf("delete: %v", err)
	}
	var live assaydv1alpha1.Agent
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &live); err == nil {
		reconcileOnce(t, r, &live)
	}
	if !getRunNamespace(t, ns).DeletionTimestamp.IsZero() {
		t.Fatal("the run namespace was deleted while another Agent still lives in the source namespace")
	}
	if st := getBinding(t, ns).Data["state"]; st != "Bound" {
		t.Fatalf("binding is %s after a non-last teardown; want Bound", st)
	}
	// The first Agent's own material and workload are gone.
	rev := revisionOf(t, ns, a.Spec)
	var cm corev1.ConfigMap
	if err := k8s.Get(context.Background(),
		types.NamespacedName{Namespace: runNS(ns), Name: controller.MaterialName("first", rev, 0)}, &cm); err == nil {
		t.Error("the deleted Agent's material outlived it")
	}
	var d appsv1.Deployment
	if err := k8s.Get(context.Background(),
		types.NamespacedName{Namespace: runNS(ns), Name: controller.WorkloadName("first", rev)}, &d); err == nil {
		t.Error("the deleted Agent's workload outlived it: nothing but the finalizer collects it")
	}

	if err := k8s.Delete(context.Background(), b2); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(b2), &live); err == nil {
		reconcileOnce(t, r, &live)
	}
	if getRunNamespace(t, ns).DeletionTimestamp.IsZero() {
		t.Fatal("the last Agent released its finalizer and left the run namespace behind")
	}
	if st := getBinding(t, ns).Data["state"]; st != "Deleting" {
		t.Errorf("binding is %s after the last teardown; want Deleting", st)
	}
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(b2), &live); err == nil {
		t.Error("the finalizer was not released")
	}
}

// Handler step 3: a Terminating binding whose confirming list finds a live
// Agent flips back to Bound. This is the crash between teardown steps 2 and 3
// with a new Agent created in the gap.
func TestATerminatingBindingFlipsBackWhenAnAgentArrives(t *testing.T) {
	ns := newNamespace(t)
	a := agentWithPrompt(t, ns, "arrived")
	r := newReconciler(false)
	settle(t, r, a)
	b := getBinding(t, ns)
	b.Data["state"] = "Terminating"
	if err := k8s.Update(context.Background(), b); err != nil {
		t.Fatalf("swap: %v", err)
	}
	got := settle(t, r, a)
	if st := getBinding(t, ns).Data["state"]; st != "Bound" {
		t.Fatalf("binding is %s; the handler found a live Agent and must restore Bound", st)
	}
	if !getRunNamespace(t, ns).DeletionTimestamp.IsZero() {
		t.Fatal("the namespace was deleted under a live Agent")
	}
	// And the condition CLEARS. It is owned: an Agent that once waited must not
	// carry RunNamespaceUnavailable=True beside Ready=True forever.
	if c := condition(&got, assaydv1alpha1.CondRunNamespaceUnavailable); c != nil && c.Status == metav1.ConditionTrue {
		t.Errorf("RunNamespaceUnavailable stayed True after the namespace was restored:\n%s", c.Message)
	}
}

// Handler step 1, UID first: a Terminating binding whose namespace now has a
// different UID must never flip back to Bound on a live Agent's say-so — the
// namespace it named is gone; whatever holds the name is foreign.
func TestTheHandlerComparesTheUIDBeforeRestoringBound(t *testing.T) {
	ns := newNamespace(t)
	a := agentWithPrompt(t, ns, "foreign")
	r := newReconciler(false)
	settle(t, r, a)
	b := getBinding(t, ns)
	b.Data["state"] = "Terminating"
	b.Data["runNamespaceUID"] = "44444444-4444-4444-4444-444444444444"
	if err := k8s.Update(context.Background(), b); err != nil {
		t.Fatalf("swap: %v", err)
	}
	settle(t, r, a)
	// The binding is gone (step 1) and the next pass met check 2: a namespace
	// with no binding.
	if _, err := getBindingMaybe(ns); err == nil {
		t.Fatal("the handler kept a binding whose namespace no longer exists")
	}
	runNamespaceCondition(t, a, controller.ReasonNotCreatedByOperator)
}

// Check 12: someone else deleted the run namespace; the operator waits rather
// than creating into a Terminating namespace, and says so.
func TestARunNamespaceDeletedBySomeoneElseIsWaitedFor(t *testing.T) {
	ns := newNamespace(t)
	a := agentWithPrompt(t, ns, "waited")
	r := newReconciler(false)
	settle(t, r, a)
	if err := k8s.Delete(context.Background(), getRunNamespace(t, ns)); err != nil {
		t.Fatalf("delete: %v", err)
	}
	got := settle(t, r, a)
	runNamespaceCondition(t, a, controller.ReasonTerminating)
	if got.Status.Phase != assaydv1alpha1.PhasePending {
		t.Errorf("phase %s, want Pending", got.Status.Phase)
	}
	if st := getBinding(t, ns).Data["state"]; st != "Terminating" {
		t.Errorf("binding is %s, want Terminating", st)
	}
}

// The strict decode: a record with an unknown state is BindingRecordInvalid,
// never defaulted to something a decision arm would act on.
func TestAnInvalidBindingRecordIsTerminalAndNamesTheField(t *testing.T) {
	ns := newNamespace(t)
	a := agentWithPrompt(t, ns, "invalid")
	r := newReconciler(false)
	settle(t, r, a)
	b := getBinding(t, ns)
	b.Data["state"] = "Bouncing"
	if err := k8s.Update(context.Background(), b); err != nil {
		t.Fatalf("corrupt: %v", err)
	}
	got := settle(t, r, a)
	c := runNamespaceCondition(t, a, controller.ReasonBindingRecordInvalid)
	if !strings.Contains(c.Message, "Bouncing") {
		t.Errorf("the message does not name the bad value:\n%s", c.Message)
	}
	if got.Status.Phase != assaydv1alpha1.PhaseDegraded {
		t.Errorf("phase %s, want Degraded", got.Status.Phase)
	}
	b = getBinding(t, ns)
	delete(b.Data, "sourceNamespaceUID")
	b.Data["state"] = "Bound"
	if err := k8s.Update(context.Background(), b); err != nil {
		t.Fatalf("corrupt: %v", err)
	}
	settle(t, r, a)
	c = runNamespaceCondition(t, a, controller.ReasonBindingRecordInvalid)
	if !strings.Contains(c.Message, "sourceNamespaceUID") {
		t.Errorf("the message does not name the missing field:\n%s", c.Message)
	}
}

// Without the admission policies that reserve the assayd.dev namespace labels,
// the operator fail-closes: no run namespace, a condition that says why.
func TestRunNamespaceRefusesWithoutLabelAuthority(t *testing.T) {
	ns := newNamespace(t)
	a := agentWithPrompt(t, ns, "noauthority")
	r := newReconciler(false)
	// The REAL adapter, against a control plane where the chart is not
	// installed and the policies do not exist.
	r.LabelAuthorityPresent = controller.LabelAuthorityPresent(k8s)
	got := settle(t, r, a)
	c := runNamespaceCondition(t, a, controller.ReasonLabelAuthorityAbsent)
	if !strings.Contains(c.Message, controller.NamespaceLabelPolicyName) {
		t.Errorf("the message does not name the missing policy:\n%s", c.Message)
	}
	if got.Status.Phase != assaydv1alpha1.PhaseDegraded {
		t.Errorf("phase %s, want Degraded", got.Status.Phase)
	}
	if _, err := getBindingMaybe(ns); err == nil {
		t.Error("a binding was written without the label authority")
	}
	var run corev1.Namespace
	if err := k8s.Get(context.Background(), types.NamespacedName{Name: runNS(ns)}, &run); err == nil {
		t.Error("a run namespace was created without the label authority")
	}

	// Install both policies with their bindings, and the adapter says so. The
	// policies validate nothing here — no params ConfigMap, no matching
	// requests from this test — so they are presence, which is all the adapter
	// checks; the enforcement itself is the e2e's.
	for _, name := range []string{controller.NamespaceLabelPolicyName, controller.GatewayRoutePolicyName} {
		p := &admissionv1.ValidatingAdmissionPolicy{ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: admissionv1.ValidatingAdmissionPolicySpec{
				MatchConstraints: &admissionv1.MatchResources{ResourceRules: []admissionv1.NamedRuleWithOperations{{
					RuleWithOperations: admissionv1.RuleWithOperations{
						Operations: []admissionv1.OperationType{admissionv1.Create},
						Rule: admissionv1.Rule{APIGroups: []string{"assayd.dev"}, APIVersions: []string{"v1alpha1"},
							Resources: []string{"nothing-matches-this"}}}}}},
				Validations: []admissionv1.Validation{{Expression: "true"}}}}
		if err := k8s.Create(context.Background(), p); err != nil {
			t.Fatalf("create policy %s: %v", name, err)
		}
		t.Cleanup(func() { _ = k8s.Delete(context.Background(), p) })
		bnd := &admissionv1.ValidatingAdmissionPolicyBinding{ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: admissionv1.ValidatingAdmissionPolicyBindingSpec{PolicyName: name,
				ValidationActions: []admissionv1.ValidationAction{admissionv1.Deny}}}
		if err := k8s.Create(context.Background(), bnd); err != nil {
			t.Fatalf("create binding %s: %v", name, err)
		}
		t.Cleanup(func() { _ = k8s.Delete(context.Background(), bnd) })
	}
	ok, err := controller.LabelAuthorityPresent(k8s)(context.Background())
	if err != nil || !ok {
		t.Fatalf("the adapter reports %v, %v with both policies and bindings present", ok, err)
	}
	got = settle(t, r, a)
	if _, err := getBindingMaybe(ns); err != nil {
		t.Error("the Agent did not recover once the label authority appeared")
	}
	if c := condition(&got, assaydv1alpha1.CondRunNamespaceUnavailable); c != nil && c.Status == metav1.ConditionTrue {
		t.Errorf("LabelAuthorityAbsent stayed True after the policies appeared:\n%s", c.Message)
	}
	// Presence means policy AND binding: a policy without its binding validates
	// nothing, and the adapter must not count it.
	_ = k8s.Delete(context.Background(), &admissionv1.ValidatingAdmissionPolicyBinding{
		ObjectMeta: metav1.ObjectMeta{Name: controller.GatewayRoutePolicyName}})
	if ok, _ := controller.LabelAuthorityPresent(k8s)(context.Background()); ok {
		t.Error("the adapter reports the label authority present with one binding missing")
	}
}

// Pod Security labels follow the source namespace, in both directions.
func TestPodSecurityLabelsAreMirroredAndReconciled(t *testing.T) {
	ns := newNamespace(t)
	src := getNamespace(t, ns)
	src.Labels = map[string]string{"pod-security.kubernetes.io/enforce": "restricted",
		"pod-security.kubernetes.io/warn": "baseline"}
	if err := k8s.Update(context.Background(), src); err != nil {
		t.Fatalf("label source: %v", err)
	}
	a := mustCreateAgent(t, ns, "pss", nil)
	r := newReconciler(false)
	settle(t, r, a)
	run := getRunNamespace(t, ns)
	if run.Labels["pod-security.kubernetes.io/enforce"] != "restricted" ||
		run.Labels["pod-security.kubernetes.io/warn"] != "baseline" {
		t.Fatalf("PSS labels not mirrored: %v", run.Labels)
	}
	src = getNamespace(t, ns)
	delete(src.Labels, "pod-security.kubernetes.io/warn")
	src.Labels["pod-security.kubernetes.io/enforce"] = "baseline"
	if err := k8s.Update(context.Background(), src); err != nil {
		t.Fatalf("relabel source: %v", err)
	}
	reconcileOnce(t, r, a)
	run = getRunNamespace(t, ns)
	if run.Labels["pod-security.kubernetes.io/enforce"] != "baseline" {
		t.Errorf("a changed level was not reconciled: %v", run.Labels)
	}
	if _, has := run.Labels["pod-security.kubernetes.io/warn"]; has {
		t.Errorf("a removed level survived on the run namespace: %v", run.Labels)
	}
}

// ResourceQuota and LimitRange are mirrored, updated, and removed with their
// source (A60).
func TestQuotaAndLimitRangeAreMirrored(t *testing.T) {
	ns := newNamespace(t)
	q := &corev1.ResourceQuota{ObjectMeta: metav1.ObjectMeta{Name: "compute", Namespace: ns},
		Spec: corev1.ResourceQuotaSpec{Hard: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("4")}}}
	lr := &corev1.LimitRange{ObjectMeta: metav1.ObjectMeta{Name: "defaults", Namespace: ns},
		Spec: corev1.LimitRangeSpec{Limits: []corev1.LimitRangeItem{{Type: corev1.LimitTypeContainer,
			DefaultRequest: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m")}}}}}
	for _, o := range []client.Object{q, lr} {
		if err := k8s.Create(context.Background(), o); err != nil {
			t.Fatalf("create %s: %v", o.GetName(), err)
		}
	}
	a := mustCreateAgent(t, ns, "quota", nil)
	r := newReconciler(false)
	settle(t, r, a)

	var mq corev1.ResourceQuota
	if err := k8s.Get(context.Background(),
		types.NamespacedName{Namespace: runNS(ns), Name: controller.MirrorName("compute")}, &mq); err != nil {
		t.Fatalf("quota not mirrored: %v", err)
	}
	if mq.Spec.Hard.Cpu().Cmp(resource.MustParse("4")) != 0 {
		t.Errorf("mirrored quota carries %v, want cpu 4", mq.Spec.Hard)
	}
	var ml corev1.LimitRange
	if err := k8s.Get(context.Background(),
		types.NamespacedName{Namespace: runNS(ns), Name: controller.MirrorName("defaults")}, &ml); err != nil {
		t.Fatalf("limit range not mirrored: %v", err)
	}

	q.Spec.Hard[corev1.ResourceCPU] = resource.MustParse("8")
	if err := k8s.Update(context.Background(), q); err != nil {
		t.Fatalf("update quota: %v", err)
	}
	if err := k8s.Delete(context.Background(), lr); err != nil {
		t.Fatalf("delete limit range: %v", err)
	}
	reconcileOnce(t, r, a)
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(&mq), &mq); err != nil {
		t.Fatalf("mirrored quota: %v", err)
	}
	if mq.Spec.Hard.Cpu().Cmp(resource.MustParse("8")) != 0 {
		t.Errorf("a changed quota was not reconciled: %v", mq.Spec.Hard)
	}
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(&ml), &ml); err == nil {
		t.Error("a deleted LimitRange's mirror survived")
	}
}

// A squatting Deployment under the expected name that does not carry this
// Agent's UID is refused whole — AlreadyExists is never provenance (A60).
func TestAWorkloadWithoutTheAgentsUIDIsRefused(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "notmineuid", nil)
	rns := provisionRunNamespace(t, ns)
	name := controller.WorkloadName("notmineuid", revision.MustHash(a.Spec))
	squatter := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: rns,
			Labels:      map[string]string{controller.LabelAgent: "notmineuid", controller.LabelRevision: revision.MustHash(a.Spec)},
			Annotations: map[string]string{controller.RevisionDigestAnnotation: revision.MustDigest(a.Spec)}},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"squat": "yes"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"squat": "yes"}},
				Spec: corev1.PodSpec{Containers: []corev1.Container{
					{Name: "agent", Image: "ghcr.io/attacker/backdoor@sha256:6d5d9666a268df6f000000000000000000000000000000000000000000000000"}}},
			},
		},
	}
	if err := k8s.Create(context.Background(), squatter); err != nil {
		t.Fatalf("plant: %v", err)
	}
	got := settle(t, newReconciler(false), a)
	c := condition(&got, assaydv1alpha1.CondRevisionHashCollision)
	if c == nil || c.Status != metav1.ConditionTrue || !strings.Contains(c.Message, "UID") {
		t.Fatalf("a workload carrying the right name, labels and digest but another owner's UID "+
			"was accepted; conditions=%+v", got.Status.Conditions)
	}
	var d appsv1.Deployment
	if err := k8s.Get(context.Background(), types.NamespacedName{Namespace: rns, Name: name}, &d); err != nil {
		t.Fatal(err)
	}
	if d.Spec.Template.Spec.Containers[0].Image != squatter.Spec.Template.Spec.Containers[0].Image {
		t.Error("the squatter was converged rather than refused")
	}
}

// The reconciler loop must not exit early past a namespace refusal without
// asserting Ready=False: the refusal is terminal and the Agent must say so.
func TestARefusedRunNamespaceIsNotReady(t *testing.T) {
	ns := newNamespace(t)
	planted := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: runNS(ns)}}
	if err := k8s.Create(context.Background(), planted); err != nil {
		t.Fatalf("pre-create: %v", err)
	}
	a := mustCreateAgent(t, ns, "notready", nil)
	got := settle(t, newReconciler(false), a)
	c := condition(&got, assaydv1alpha1.CondReady)
	if c == nil || c.Status != metav1.ConditionFalse || c.Reason != "RunNamespaceUnavailable" {
		t.Errorf("Ready is %+v; want False with reason RunNamespaceUnavailable", c)
	}
	// A terminal refusal returns no error: an error would be retried forever.
	_, err := newReconciler(false).Reconcile(context.Background(),
		ctrl.Request{NamespacedName: client.ObjectKeyFromObject(a)})
	if err != nil {
		t.Errorf("a terminal refusal returned an error and would be retried forever: %v", err)
	}
}

// Teardown step 1's second predicate: a DELETING Agent is not a reason to keep
// the namespace, but its workload still draining is. Here B is deleted and its
// finalizer has not run — its workload is still in the run namespace — when A,
// the other Agent, is torn down. A must not delete the namespace under B.
func TestADrainingWorkloadKeepsTheRunNamespace(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "alpha", nil)
	b := mustCreateAgent(t, ns, "beta", nil)
	r := newReconciler(false)
	settle(t, r, a)
	settle(t, r, b)

	// B is deleting; nobody has reconciled it, so its workload remains.
	if err := k8s.Delete(context.Background(), b); err != nil {
		t.Fatalf("delete beta: %v", err)
	}
	if err := k8s.Delete(context.Background(), a); err != nil {
		t.Fatalf("delete alpha: %v", err)
	}
	var live assaydv1alpha1.Agent
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &live); err == nil {
		reconcileOnce(t, r, &live)
	}
	if !getRunNamespace(t, ns).DeletionTimestamp.IsZero() {
		t.Fatal("alpha's finalizer deleted the run namespace while beta's workload was still draining " +
			"in it — nothing is force-killed silently, and this was")
	}
	if st := getBinding(t, ns).Data["state"]; st != "Bound" {
		t.Fatalf("binding is %s; a draining workload must keep it Bound", st)
	}
	// Now beta's finalizer runs: it is last, and the namespace goes.
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(b), &live); err == nil {
		reconcileOnce(t, r, &live)
	}
	if getRunNamespace(t, ns).DeletionTimestamp.IsZero() {
		t.Fatal("the last Agent's finalizer left the run namespace behind")
	}
}

// Handler step 4's fence: nothing flips back from Deleting. A live Agent that
// reads Deleting waits; it does not restore Bound and write into a namespace
// another worker is deleting.
func TestADeletingBindingDoesNotFlipBack(t *testing.T) {
	ns := newNamespace(t)
	a := agentWithPrompt(t, ns, "fenced")
	r := newReconciler(false)
	settle(t, r, a)
	b := getBinding(t, ns)
	b.Data["state"] = "Deleting"
	if err := k8s.Update(context.Background(), b); err != nil {
		t.Fatalf("swap: %v", err)
	}
	got := settle(t, r, a)
	runNamespaceCondition(t, a, controller.ReasonTerminating)
	if got.Status.Phase != assaydv1alpha1.PhasePending {
		t.Errorf("phase %s, want Pending", got.Status.Phase)
	}
	if st := getBinding(t, ns).Data["state"]; st != "Deleting" {
		t.Fatalf("binding moved from Deleting to %s: the fence the delete waits on was reopened", st)
	}
	if getRunNamespace(t, ns).DeletionTimestamp.IsZero() {
		t.Error("the committed delete was not issued")
	}
}

// Check 9's recovery must not wedge on its own Terminating namespace: the pass
// after the delete sees a namespace with the OLD nonce and a deletion
// timestamp, and that is a wait, not a refusal (found by the code review).
func TestTheCrashGapRecoveryWaitsRatherThanWedging(t *testing.T) {
	ns := newNamespace(t)
	a := agentWithPrompt(t, ns, "crashwait")
	r := newReconciler(false)
	reconcileOnce(t, r, a)
	nonce := "0123456789abcdef0123456789abcdef"
	rec := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
		Name: controller.BindingName(runNS(ns)), Namespace: operatorNamespace,
		Labels: map[string]string{controller.LabelBinding: "true"}},
		Data: map[string]string{"schemaVersion": "1", "sourceNamespace": ns,
			"sourceNamespaceUID": string(getNamespace(t, ns).UID), "runNamespace": runNS(ns),
			"nonce": nonce, "state": "Creating"}}
	if err := k8s.Create(context.Background(), rec); err != nil {
		t.Fatalf("plant binding: %v", err)
	}
	planted := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: runNS(ns),
		Annotations: map[string]string{controller.AnnotationBindingNonce: nonce}}}
	if err := k8s.Create(context.Background(), planted); err != nil {
		t.Fatalf("plant namespace: %v", err)
	}
	got := settle(t, r, a) // check 9, then the passes after it
	c := runNamespaceCondition(t, a, controller.ReasonTerminating)
	if got.Status.Phase != assaydv1alpha1.PhasePending {
		t.Errorf("phase %s with %q; the recovery wedged terminally on the operator's own namespace",
			got.Status.Phase, c.Reason)
	}
	if st := getBinding(t, ns).Data["state"]; st != "Creating" {
		t.Errorf("binding is %s, want Creating with a rotated nonce", st)
	}
}

// Check 5 from Creating: nothing was bound, so there is nothing to terminate and
// a Terminating record without a UID is one the decoder refuses. The record is
// discarded, the crash-gap namespace goes with it, and the Agent waits.
func TestARecreatedSourceNamespaceWhileCreatingDiscardsTheRecord(t *testing.T) {
	ns := newNamespace(t)
	a := agentWithPrompt(t, ns, "v2creating")
	r := newReconciler(false)
	reconcileOnce(t, r, a)
	nonce := "0123456789abcdef0123456789abcdef"
	rec := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
		Name: controller.BindingName(runNS(ns)), Namespace: operatorNamespace,
		Labels: map[string]string{controller.LabelBinding: "true"}},
		Data: map[string]string{"schemaVersion": "1", "sourceNamespace": ns,
			"sourceNamespaceUID": "55555555-5555-5555-5555-555555555555", "runNamespace": runNS(ns),
			"nonce": nonce, "state": "Creating"}}
	if err := k8s.Create(context.Background(), rec); err != nil {
		t.Fatalf("plant binding: %v", err)
	}
	planted := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: runNS(ns),
		Annotations: map[string]string{controller.AnnotationBindingNonce: nonce}}}
	if err := k8s.Create(context.Background(), planted); err != nil {
		t.Fatalf("plant namespace: %v", err)
	}
	got := settle(t, r, a)
	runNamespaceCondition(t, a, controller.ReasonTerminating)
	if got.Status.Phase == assaydv1alpha1.PhaseDegraded {
		t.Fatal("the operator went terminal on a record it wrote itself")
	}
	if getRunNamespace(t, ns).DeletionTimestamp.IsZero() {
		t.Error("the crash-gap namespace of a dead tenant was kept")
	}
	if b, err := getBindingMaybe(ns); err == nil && b.Data["state"] == "Terminating" && b.Data["runNamespaceUID"] == "" {
		t.Error("a Terminating record with no UID was written — the decoder refuses it on the next pass")
	}
}

// Check 11: a Bound record whose namespace no longer exists returns to Creating
// with a fresh nonce, and the next pass creates and binds anew.
func TestABoundRecordWhoseNamespaceIsGoneIsRecreated(t *testing.T) {
	ns := newNamespace(t)
	a := agentWithPrompt(t, ns, "gone")
	r := newReconciler(false)
	reconcileOnce(t, r, a)
	rec := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
		Name: controller.BindingName(runNS(ns)), Namespace: operatorNamespace,
		Labels: map[string]string{controller.LabelBinding: "true"}},
		Data: map[string]string{"schemaVersion": "1", "sourceNamespace": ns,
			"sourceNamespaceUID": string(getNamespace(t, ns).UID), "runNamespace": runNS(ns),
			"runNamespaceUID": "66666666-6666-6666-6666-666666666666",
			"nonce":           "0123456789abcdef0123456789abcdef", "state": "Bound"}}
	if err := k8s.Create(context.Background(), rec); err != nil {
		t.Fatalf("plant binding: %v", err)
	}
	reconcileOnce(t, r, a)
	b := getBinding(t, ns)
	if b.Data["state"] != "Creating" || b.Data["nonce"] == "0123456789abcdef0123456789abcdef" {
		t.Fatalf("binding is %s with the old nonce; check 11 must return to Creating with a rotated nonce", b.Data["state"])
	}
	settle(t, r, a)
	run := getRunNamespace(t, ns)
	b = getBinding(t, ns)
	if b.Data["state"] != "Bound" || b.Data["runNamespaceUID"] != string(run.UID) {
		t.Errorf("after recreation the binding is %s to %q, want Bound to %s", b.Data["state"], b.Data["runNamespaceUID"], run.UID)
	}
}

// The Namespace-keyed reconciler runs the handler with no Agent to drive it.
func TestTheNamespaceReconcilerRunsTheHandler(t *testing.T) {
	ns := newNamespace(t)
	a := agentWithPrompt(t, ns, "nskeyed")
	r := newReconciler(false)
	settle(t, r, a)
	b := getBinding(t, ns)
	b.Data["state"] = "Terminating"
	if err := k8s.Update(context.Background(), b); err != nil {
		t.Fatalf("swap: %v", err)
	}
	if _, err := (&controller.RunNamespaceReconciler{}).Reconcile(context.Background(),
		ctrl.Request{NamespacedName: types.NamespacedName{Name: runNS(ns)}}); err == nil {
		// A zero reconciler must not silently succeed; the constructor path
		// below is the real one.
	}
	n := controller.NewRunNamespaceReconciler(r)
	if _, err := n.Reconcile(context.Background(),
		ctrl.Request{NamespacedName: types.NamespacedName{Name: runNS(ns)}}); err != nil {
		t.Fatalf("namespace reconciler: %v", err)
	}
	if st := getBinding(t, ns).Data["state"]; st != "Bound" {
		t.Fatalf("binding is %s; the Namespace-keyed reconciler found a live Agent and must restore Bound", st)
	}
}

// The deletion authority for workloads is the NAME (A57/A60). Three impostors
// carrying every label the operator writes — agent, revision, agent UID — but
// the wrong name are beyond the retention window and must survive.
func TestWorkloadDeletionAuthorityIsTheName(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "nameauth", nil)
	r := newReconciler(false)
	settle(t, r, a)
	var live assaydv1alpha1.Agent
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &live); err != nil {
		t.Fatal(err)
	}
	var names []string
	for i := 0; i < 3; i++ {
		name := "labelled-impostor-" + string(rune('a'+i))
		names = append(names, name)
		d := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: runNS(ns), Labels: map[string]string{
				controller.LabelAgent: "nameauth", controller.LabelRevision: "deadbeef0" + string(rune('0'+i)),
				controller.LabelAgentUID: string(live.UID)}},
			Spec: appsv1.DeploymentSpec{
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": name}},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": name}},
					Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "c", Image: "busybox"}}}}}}
		if err := k8s.Create(context.Background(), d); err != nil {
			t.Fatalf("create impostor: %v", err)
		}
	}
	settle(t, r, a)
	for _, name := range names {
		var d appsv1.Deployment
		if err := k8s.Get(context.Background(), types.NamespacedName{Namespace: runNS(ns), Name: name}, &d); err != nil {
			t.Errorf("%s was deleted: it carries every label the operator writes and not the name, so a "+
				"principal with update turned labels into a delete: %v", name, err)
		}
	}
}

// A squatter on the ACTIVE revision's name, stamped with the right digest but
// without this Agent's UID, must not make the active revision read as serving.
// The desired revision's own path refuses such an object outright (the
// collision check), so this is staged through the one path that asks about
// the ACTIVE revision without it: a rollout, where the successor is not yet
// available and the operator asks whether the incumbent still serves.
func TestAnActiveWorkloadWithoutTheAgentsUIDIsNotServing(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "activeuid", nil)
	r := newReconciler(false)
	settle(t, r, a)
	rev := revision.MustHash(a.Spec)
	markAvailable(t, ns, controller.WorkloadName("activeuid", rev), 1)
	got := settle(t, r, a)
	if got.Status.ActiveRevision != rev {
		t.Fatalf("setup: active is %q", got.Status.ActiveRevision)
	}
	// Replace the ACTIVE workload with one carrying the digest but not the UID.
	key := types.NamespacedName{Namespace: runNS(ns), Name: controller.WorkloadName("activeuid", rev)}
	var d appsv1.Deployment
	if err := k8s.Get(context.Background(), key, &d); err != nil {
		t.Fatal(err)
	}
	if err := k8s.Delete(context.Background(), &d); err != nil {
		t.Fatal(err)
	}
	squatter := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace,
			Labels:      map[string]string{controller.LabelAgent: "activeuid", controller.LabelRevision: rev},
			Annotations: map[string]string{controller.RevisionDigestAnnotation: revision.MustDigest(a.Spec)}},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"squat": "yes"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"squat": "yes"}},
				Spec: corev1.PodSpec{Containers: []corev1.Container{
					{Name: "agent", Image: "ghcr.io/attacker/backdoor@sha256:6d5d9666a268df6f000000000000000000000000000000000000000000000000"}}}}}}
	if err := k8s.Create(context.Background(), squatter); err != nil {
		t.Fatal(err)
	}
	markAvailable(t, ns, key.Name, 1)
	// A rollout: the desired revision moves and is not yet available, so the
	// operator asks whether the ACTIVE one still serves.
	var live assaydv1alpha1.Agent
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &live); err != nil {
		t.Fatal(err)
	}
	live.Spec.Runtime.Image = "ghcr.io/acme/agent@sha256:7777777777777777777777777777777777777777777777777777777777777777"
	if err := k8s.Update(context.Background(), &live); err != nil {
		t.Fatal(err)
	}
	got = settle(t, r, &live)
	if c := condition(&got, assaydv1alpha1.CondReady); c != nil && c.Status == metav1.ConditionTrue {
		t.Fatalf("an available squatter under the active name, with the right digest and another "+
			"owner's UID, was reported as still serving during a rollout: %s", c.Message)
	}
}

// A stray object in a run namespace wearing the mirror name shape but no
// source annotation is not a mirror, and the sweep leaves it alone.
func TestTheMirrorSweepDecidesByNameAndAnnotation(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "mirrorsweep", nil)
	r := newReconciler(false)
	settle(t, r, a)
	stray := &corev1.ResourceQuota{ObjectMeta: metav1.ObjectMeta{Name: controller.MirrorPrefix + "stray", Namespace: runNS(ns)},
		Spec: corev1.ResourceQuotaSpec{Hard: corev1.ResourceList{corev1.ResourcePods: resource.MustParse("3")}}}
	if err := k8s.Create(context.Background(), stray); err != nil {
		t.Fatal(err)
	}
	reconcileOnce(t, r, a)
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(stray), stray); err != nil {
		t.Error("a quota that only wears the mirror name shape was deleted by the sweep")
	}
}

// Check 7 mirrors BEFORE binding: if the mirror cannot be written, the record
// stays Creating rather than binding a namespace whose quota never landed.
func TestMirrorsLandBeforeTheBindingIsBound(t *testing.T) {
	ns := newNamespace(t)
	q := &corev1.ResourceQuota{ObjectMeta: metav1.ObjectMeta{Name: "compute", Namespace: ns},
		Spec: corev1.ResourceQuotaSpec{Hard: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2")}}}
	if err := k8s.Create(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	a := mustCreateAgent(t, ns, "mirrorfirst", nil)
	r := newReconciler(false)
	r.Client = &refuseQuotaCreate{Client: k8s}
	for i := 0; i < 4; i++ {
		_, _ = r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(a)})
	}
	if b, err := getBindingMaybe(ns); err == nil && b.Data["state"] == "Bound" {
		t.Fatal("the binding was Bound while the quota mirror could not be written; the first Pod would " +
			"precede its quota")
	}
}

// refuseQuotaCreate is the client that cannot write a ResourceQuota.
type refuseQuotaCreate struct{ client.Client }

func (c *refuseQuotaCreate) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	if _, ok := obj.(*corev1.ResourceQuota); ok {
		return apierrors.NewInternalError(context.DeadlineExceeded)
	}
	return c.Client.Create(ctx, obj, opts...)
}

// Upgrade from an operator built before A42: its workload and copies sit in the
// Agent's own namespace, owned by controller reference. They are collected,
// or an upgrade runs two workloads per Agent and leaks the old Secret copies
// where a namespace editor can read them.
func TestPreA42LeftoversAreCollected(t *testing.T) {
	ns := newNamespace(t)
	a := agentWithPrompt(t, ns, "legacy")
	var live assaydv1alpha1.Agent
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &live); err != nil {
		t.Fatal(err)
	}
	old := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "legacy-0123456789", Namespace: ns,
			Labels: map[string]string{controller.LabelAgent: "legacy", controller.LabelRevision: "0123456789"}},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "legacy"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "legacy"}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "c", Image: "busybox"}}}}}}
	if err := ctrl.SetControllerReference(&live, old, scheme); err != nil {
		t.Fatal(err)
	}
	if err := k8s.Create(context.Background(), old); err != nil {
		t.Fatal(err)
	}
	yes := true
	oldCopy := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
		Name: controller.MaterialName("legacy", "0123456789", 0), Namespace: ns,
		Labels:      map[string]string{controller.MaterialAgentUIDLabel: string(live.UID), controller.MaterialRevisionLabel: "0123456789"},
		Annotations: map[string]string{controller.MaterialDigestAnnotation: "x", controller.MaterialSourceAnnotation: "ConfigMap.prompt"}},
		Immutable: &yes, Data: map[string]string{"SYSTEM_PROMPT": "old"}}
	if err := k8s.Create(context.Background(), oldCopy); err != nil {
		t.Fatal(err)
	}
	settle(t, newReconciler(false), a)
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(old), old); err == nil {
		t.Error("the pre-A42 workload in the Agent's namespace survived; two workloads serve one Agent")
	}
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(oldCopy), oldCopy); err == nil {
		t.Error("the pre-A42 copy in the Agent's namespace survived; a namespace editor can read it")
	}
}

// An external Agent never uses the run namespace and does not keep it alive.
func TestAnExternalAgentDoesNotKeepTheRunNamespace(t *testing.T) {
	ns := newNamespace(t)
	a := agentWithPrompt(t, ns, "runtime")
	mustCreateAgent(t, ns, "external", func(x *assaydv1alpha1.Agent) {
		x.Spec.Runtime = nil
		x.Spec.External = &assaydv1alpha1.ExternalAgent{Endpoint: "https://agent.example.com"}
	})
	r := newReconciler(false)
	settle(t, r, a)
	if err := k8s.Delete(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	var live assaydv1alpha1.Agent
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), &live); err == nil {
		reconcileOnce(t, r, &live)
	}
	if getRunNamespace(t, ns).DeletionTimestamp.IsZero() {
		t.Fatal("an external Agent, which runs elsewhere, kept the run namespace alive")
	}
}
