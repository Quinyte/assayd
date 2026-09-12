// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package envtest

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Quinyte/assayd/internal/compiler"
)

// Design 03 §6 and §3.4.4, measured against a real API server rather than
// read off a rendered file. envtest's kube-apiserver runs RBAC and the
// ValidatingAdmissionPolicy plugin, and its client may impersonate anyone, so
// what these tests see is what the admission chain decides for each identity.
//
// The policies are the chart's own, from `helm template`, with ONE change: the
// namespace selector gains a label only this test's namespaces carry, ANDed
// with the chart's `assayd.dev/run-namespace: "true"`. Without it the
// reservation would reach the run namespaces every other test in this package
// writes policies into as admin. The CEL, the resource rules and the
// operations are applied exactly as rendered. What the selector is, the chart
// test pins (test/chart, TestTheChartReservesGatewayPoliciesAndKeySetsInRunNamespaces).

const (
	renderedOperator = "system:serviceaccount:assayd-system:assayd-agent-operator"
	intruder         = "assayd-envtest-intruder"
	gcUser           = "system:serviceaccount:kube-system:generic-garbage-collector"
	scopeLabel       = "assayd-envtest/reservation"
)

func renderedAdmission(t *testing.T) map[string]*unstructured.Unstructured {
	t.Helper()
	out, err := exec.Command("helm", "template", "assayd", filepath.Join("..", "..", "charts", "assayd")).Output()
	if err != nil {
		t.Fatalf("helm template: %v", err)
	}
	docs := map[string]*unstructured.Unstructured{}
	dec := utilyaml.NewYAMLOrJSONDecoder(bytes.NewReader(out), 4096)
	for {
		var m map[string]any
		if err := dec.Decode(&m); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatalf("decode the rendered chart: %v", err)
		}
		if m == nil {
			continue
		}
		u := &unstructured.Unstructured{Object: m}
		if k := u.GetKind(); k == "ValidatingAdmissionPolicy" || k == "ValidatingAdmissionPolicyBinding" {
			docs[k+"/"+u.GetName()] = u
		}
	}
	return docs
}

// installReservation applies one rendered policy and its binding under a name
// of this test's, narrowed to namespaces carrying scope, and returns that name.
func installReservation(t *testing.T, name, scope string) string {
	t.Helper()
	ctx := context.Background()
	docs := renderedAdmission(t)
	pol, bind := docs["ValidatingAdmissionPolicy/"+name], docs["ValidatingAdmissionPolicyBinding/"+name]
	if pol == nil || bind == nil {
		t.Fatalf("the chart renders no policy and binding named %s", name)
	}
	unique := scope + "-" + strings.TrimPrefix(name, "assayd-")
	pol, bind = pol.DeepCopy(), bind.DeepCopy()
	sel, _, _ := unstructured.NestedStringMap(pol.Object, "spec", "matchConstraints", "namespaceSelector", "matchLabels")
	if sel["assayd.dev/run-namespace"] != "true" {
		t.Fatalf("the rendered %s no longer selects run namespaces: %v", name, sel)
	}
	sel[scopeLabel] = scope
	if err := unstructured.SetNestedStringMap(pol.Object, sel, "spec", "matchConstraints", "namespaceSelector", "matchLabels"); err != nil {
		t.Fatal(err)
	}
	pol.SetName(unique)
	bind.SetName(unique)
	if err := unstructured.SetNestedField(bind.Object, unique, "spec", "policyName"); err != nil {
		t.Fatal(err)
	}
	for _, o := range []*unstructured.Unstructured{pol, bind} {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatalf("install %s %s: %v", o.GetKind(), o.GetName(), err)
		}
		o := o
		t.Cleanup(func() { _ = k8s.Delete(context.Background(), o) })
	}
	return unique
}

func scopedNamespace(t *testing.T, name, scope string, run bool) string {
	t.Helper()
	labels := map[string]string{scopeLabel: scope}
	if run {
		labels["assayd.dev/run-namespace"] = "true"
	}
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels}}
	if err := k8s.Create(context.Background(), ns); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create namespace %s: %v", name, err)
	}
	return name
}

// grantWriters gives each user every verb on ConfigMaps and
// AgentgatewayPolicies in each namespace, so a refusal below is admission's,
// not RBAC's.
func grantWriters(t *testing.T, scope string, namespaces []string, users ...string) {
	t.Helper()
	ctx := context.Background()
	role := &rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: scope + "-writer"},
		Rules: []rbacv1.PolicyRule{
			{APIGroups: []string{""}, Resources: []string{"configmaps"}, Verbs: []string{"*"}},
			{APIGroups: []string{"agentgateway.dev"}, Resources: []string{"agentgatewaypolicies"}, Verbs: []string{"*"}},
		}}
	if err := k8s.Create(ctx, role); err != nil {
		t.Fatalf("create role: %v", err)
	}
	t.Cleanup(func() { _ = k8s.Delete(context.Background(), role) })
	var subjects []rbacv1.Subject
	for _, u := range users {
		subjects = append(subjects, rbacv1.Subject{Kind: "User", Name: u})
	}
	for _, ns := range namespaces {
		rb := &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "writers", Namespace: ns},
			RoleRef:  rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "ClusterRole", Name: role.Name},
			Subjects: subjects}
		if err := k8s.Create(ctx, rb); err != nil {
			t.Fatalf("bind writers in %s: %v", ns, err)
		}
	}
}

func as(t *testing.T, user string, groups ...string) client.Client {
	t.Helper()
	c := rest.CopyConfig(cfg)
	c.Impersonate = rest.ImpersonationConfig{UserName: user, Groups: groups}
	cl, err := client.New(c, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatalf("impersonate %s: %v", user, err)
	}
	return cl
}

// refusedBy reports whether err is the named policy's denial, and not RBAC's or
// anything else's: a refusal is evidence only about the rule that made it.
func refusedBy(err error, policy string) bool {
	return err != nil && apierrors.IsForbidden(err) && strings.Contains(err.Error(), policy)
}

// untilEnforced retries a write the policy must refuse until it does: a new
// policy takes effect when the API server has compiled it, not when the create
// returns.
func untilEnforced(t *testing.T, policy string, write func() (client.Object, error)) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	var last error
	for time.Now().Before(deadline) {
		obj, err := write()
		if refusedBy(err, policy) {
			return
		}
		if err == nil {
			_ = k8s.Delete(context.Background(), obj)
		}
		last = err
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("policy %s never took effect (last result: %v)", policy, last)
}

func keySet(ns, name, value string) *corev1.ConfigMap {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Labels: map[string]string{compiler.APIKeySourceLabel: value}},
		Data: map[string]string{"k": `{"keyHash":"sha256:` + strings.Repeat("0", 64) +
			`","metadata":{"group":"payments"}}`},
	}
	if name == "" {
		cm.GenerateName = "probe-"
	} else {
		cm.Name = name
	}
	return cm
}

func TestOnlyTheOperatorAndAnAdministratorWriteAKeySet(t *testing.T) {
	ctx := context.Background()
	scope := nsName(t.Name())
	run := scopedNamespace(t, nsName(t.Name()+"-run"), scope, true)
	plain := scopedNamespace(t, nsName(t.Name()+"-plain"), scope, false)
	policy := installReservation(t, "assayd-api-keys", scope)
	grantWriters(t, scope, []string{run, plain}, renderedOperator, intruder, gcUser)
	asIntruder := as(t, intruder)
	asGC := as(t, gcUser)
	asOperator := as(t, renderedOperator)
	// The chart's default administrator group. Impersonated under a name of
	// its own so that it is the GROUP that admits it, not a username.
	asAdmin := as(t, "assayd-envtest-key-admin", "system:masters")

	untilEnforced(t, policy, func() (client.Object, error) {
		cm := keySet(run, "", compiler.APIKeySourceValue)
		return cm, asIntruder.Create(ctx, cm)
	})

	// A principal with every ConfigMap right in the run namespace cannot mint a
	// key, under the product's label value or any other.
	for _, v := range []string{compiler.APIKeySourceValue, "e2e"} {
		if err := asIntruder.Create(ctx, keySet(run, "minted-"+v, v)); !refusedBy(err, policy) {
			t.Errorf("a key set labelled %s=%q was written by a non-writer: %v", compiler.APIKeySourceLabel, v, err)
		}
	}

	// CONTROL: the same identity writes an ordinary ConfigMap in the same
	// namespace, so the refusals are about the label.
	ordinary := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "ordinary", Namespace: run}}
	if err := asIntruder.Create(ctx, ordinary); err != nil {
		t.Fatalf("an unlabelled ConfigMap was refused too, so the refusals above say nothing about "+
			"the label: %v", err)
	}
	// Labelling an ordinary ConfigMap is writing a key set.
	ordinary.Labels = map[string]string{compiler.APIKeySourceLabel: compiler.APIKeySourceValue}
	if err := asIntruder.Update(ctx, ordinary); !refusedBy(err, policy) {
		t.Errorf("a non-writer turned an ordinary ConfigMap into a key set: %v", err)
	}

	// The administrator issues keys (§3.4.4: "written by an administrator").
	issued := keySet(run, "issued", compiler.APIKeySourceValue)
	if err := asAdmin.Create(ctx, issued); err != nil {
		t.Fatalf("an administrator in system:masters could not write a key set, so no key can ever "+
			"be issued: %v", err)
	}
	// And a non-writer may neither add to it nor take its label off to edit it.
	var got corev1.ConfigMap
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(issued), &got); err != nil {
		t.Fatal(err)
	}
	added := got.DeepCopy()
	added.Data["k2"] = added.Data["k"]
	if err := asIntruder.Update(ctx, added); !refusedBy(err, policy) {
		t.Errorf("a non-writer added a key to an issued key set: %v", err)
	}
	unlabelled := got.DeepCopy()
	unlabelled.Labels = nil
	if err := asIntruder.Update(ctx, unlabelled); !refusedBy(err, policy) {
		t.Errorf("a non-writer removed the key label from an issued key set: %v", err)
	}

	// Binary data is key material too.
	binary := got.DeepCopy()
	binary.BinaryData = map[string][]byte{"b": []byte("not a key")}
	if err := asIntruder.Update(ctx, binary); !refusedBy(err, policy) {
		t.Errorf("a non-writer added binary data to an issued key set: %v", err)
	}

	// The garbage collector finishes a foreground delete by removing the
	// `foregroundDeletion` finalizer: an UPDATE that changes nothing the
	// reservation protects. Refused, the key set would stay Terminating
	// forever. What it holds stays reserved while it waits.
	revoked := keySet(run, "revoked", compiler.APIKeySourceValue)
	if err := asAdmin.Create(ctx, revoked); err != nil {
		t.Fatalf("issue a key set: %v", err)
	}
	if err := asAdmin.Delete(ctx, revoked, client.PropagationPolicy(metav1.DeletePropagationForeground)); err != nil {
		t.Fatalf("foreground-delete the key set: %v", err)
	}
	var terminating corev1.ConfigMap
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(revoked), &terminating); err != nil ||
		terminating.DeletionTimestamp == nil {
		t.Fatalf("want the key set held by the foreground finalizer, got %v (%v)", terminating.Finalizers, err)
	}
	edited := terminating.DeepCopy()
	edited.Data["k2"] = edited.Data["k"]
	if err := asIntruder.Update(ctx, edited); !refusedBy(err, policy) {
		t.Errorf("a non-writer added a key to a key set that is being deleted: %v", err)
	}
	finalized := terminating.DeepCopy()
	finalized.Finalizers = nil
	if err := asGC.Update(ctx, finalized); err != nil {
		t.Fatalf("the garbage collector could not finish a foreground delete of a key set: %v", err)
	}
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(revoked), &terminating); !apierrors.IsNotFound(err) {
		t.Errorf("the key set outlived its foreground delete: %v", err)
	}

	// The operator is a permitted writer (§8.1), for Narrow's probe keys.
	if err := asOperator.Create(ctx, keySet(run, "probe-keys", compiler.APIKeySourceValue)); err != nil {
		t.Errorf("the operator could not write a key set: %v", err)
	}

	// Outside run namespaces the label is NOT reserved, and that is stated,
	// not hidden: whether a selector reaches another namespace is unmeasured
	// (§3.4.4).
	if err := asIntruder.Create(ctx, keySet(plain, "outside", compiler.APIKeySourceValue)); err != nil {
		t.Errorf("the reservation reached a namespace that is not a run namespace: %v", err)
	}
}

func TestOnlyTheOperatorAuthorsAPolicyInARunNamespace(t *testing.T) {
	ctx := context.Background()
	scope := nsName(t.Name())
	run := scopedNamespace(t, nsName(t.Name()+"-run"), scope, true)
	plain := scopedNamespace(t, nsName(t.Name()+"-plain"), scope, false)
	policy := installReservation(t, "assayd-gateway-policies", scope)
	grantWriters(t, scope, []string{run, plain}, renderedOperator, intruder, gcUser)
	asIntruder := as(t, intruder)
	asGC := as(t, gcUser)
	asOperator := as(t, renderedOperator)
	asAdmin := as(t, "assayd-envtest-key-admin", "system:masters")

	authPolicy := func(ns, agent string) *unstructured.Unstructured {
		p, err := compiler.AuthPolicy(compiler.AuthInput{AgentName: agent, AgentNamespace: "payments",
			AgentUID: "0f0e0d0c-0000-0000-0000-000000000001", RunNamespace: ns})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	n := 0
	untilEnforced(t, policy, func() (client.Object, error) {
		n++
		p := authPolicy(run, "probe"+strings.Repeat("x", n%3))
		return p, asIntruder.Create(ctx, p)
	})

	if err := asIntruder.Create(ctx, authPolicy(run, "forged")); !refusedBy(err, policy) {
		t.Errorf("a non-operator wrote an AgentgatewayPolicy in a run namespace: %v", err)
	}
	// Not an administrator either. §6 reserves policies to "any identity but
	// the operator's", unlike key sets, which an administrator writes.
	if err := asAdmin.Create(ctx, authPolicy(run, "admin")); !refusedBy(err, policy) {
		t.Errorf("an administrator wrote an AgentgatewayPolicy in a run namespace: %v", err)
	}

	own := authPolicy(run, "owned")
	if err := asOperator.Create(ctx, own); err != nil {
		t.Fatalf("the operator could not write its own -auth policy: %v", err)
	}
	fresh := func() *unstructured.Unstructured {
		p := authPolicy(run, "owned")
		if err := k8s.Get(ctx, client.ObjectKeyFromObject(own), p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	byOperator := fresh()
	byOperator.SetAnnotations(map[string]string{"assayd.dev/test": "rewritten"})
	if err := asOperator.Update(ctx, byOperator); err != nil {
		t.Errorf("the operator could not update its own policy: %v", err)
	}
	byIntruder := fresh()
	if err := unstructured.SetNestedField(byIntruder.Object, []any{`true`},
		"spec", "traffic", "authorization", "policy", "matchExpressions"); err != nil {
		t.Fatal(err)
	}
	if err := asIntruder.Update(ctx, byIntruder); !refusedBy(err, policy) {
		t.Errorf("a non-operator rewrote the operator's policy to admit everyone: %v", err)
	}

	// The labels are reserved with the spec: they are what makes the policy the
	// operator's (design 03 §3.2's name-and-label rule).
	relabelled := fresh()
	relabelled.SetLabels(nil)
	if err := asIntruder.Update(ctx, relabelled); !refusedBy(err, policy) {
		t.Errorf("a non-operator stripped the operator's labels from its policy: %v", err)
	}

	// The garbage collector finishes a foreground delete with an UPDATE that
	// changes nothing reserved. Refused, the policy stayed Terminating forever,
	// and so did the teardown of its Agent, which waits for it. A non-operator
	// still cannot change what a Terminating policy says.
	fg := authPolicy(run, "foreground")
	if err := asOperator.Create(ctx, fg); err != nil {
		t.Fatalf("the operator could not write a policy: %v", err)
	}
	if err := k8s.Delete(ctx, fg, client.PropagationPolicy(metav1.DeletePropagationForeground)); err != nil {
		t.Fatalf("foreground-delete the policy: %v", err)
	}
	held := authPolicy(run, "foreground")
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(fg), held); err != nil || held.GetDeletionTimestamp() == nil {
		t.Fatalf("want the policy held by the foreground finalizer, got %v (%v)", held.GetFinalizers(), err)
	}
	rewritten := held.DeepCopy()
	if err := unstructured.SetNestedField(rewritten.Object, []any{`true`},
		"spec", "traffic", "authorization", "policy", "matchExpressions"); err != nil {
		t.Fatal(err)
	}
	if err := asIntruder.Update(ctx, rewritten); !refusedBy(err, policy) {
		t.Errorf("a non-operator rewrote a policy that is being deleted: %v", err)
	}
	finalized := held.DeepCopy()
	finalized.SetFinalizers(nil)
	if err := asGC.Update(ctx, finalized); err != nil {
		t.Fatalf("the garbage collector could not finish a foreground delete of a policy, so the "+
			"policy and the teardown of the Agent it belongs to wait forever: %v", err)
	}
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(fg), authPolicy(run, "foreground")); !apierrors.IsNotFound(err) {
		t.Errorf("the policy outlived its foreground delete: %v", err)
	}

	// DELETE is deliberately not reserved: who may delete is RBAC's question,
	// as for routes, and reserving it would refuse the namespace controller's
	// teardown of a run namespace. A deleted -auth is §3.3.3's AuthPolicyMissing.
	if err := asIntruder.Delete(ctx, fresh()); err != nil {
		t.Errorf("a delete with RBAC to do it was refused; the reservation matches CREATE and "+
			"UPDATE only: %v", err)
	}

	// A policy outside run namespaces reaches no Agent's route: a policy's
	// targetRefs select same-namespace objects only (§3.2).
	if err := asIntruder.Create(ctx, authPolicy(plain, "outside")); err != nil {
		t.Errorf("the reservation reached a namespace that is not a run namespace: %v", err)
	}
}
