// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package envtest

import (
	"context"
	"strings"
	"testing"
	"time"

	authorizationv1 "k8s.io/api/authorization/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
)

// Design 16 §1.1, Q6 (c): an EvalSuite create or update is admitted only from
// a principal the authorizer says may `update` Agents in the suite's namespace,
// namespace-wide. Measured against a real API server, which runs RBAC and the
// ValidatingAdmissionPolicy plugin, with the chart's own rendered
// assayd-evalsuite-writers.
//
// The rendered policy is cluster-wide, because a suite lives in its Agent's
// namespace and those carry no label of assayd's. It gains a namespaceSelector
// here, matching a label only this test's namespace carries, so that it does
// not reach the suites the rest of this package writes as admin. Its CEL,
// resource rules and operations are applied exactly as rendered. The chart
// test pins that it is cluster-wide (test/chart,
// TestTheChartReservesEvalSuiteWritesToAgentEditors).

const (
	// May write suites and read Agents, and may not update any Agent.
	suiteOnlyWriter = "assayd-envtest-suite-writer"
	// May write suites, read Agents, and update ONE Agent, by resourceNames.
	namedAgentEditor = "assayd-envtest-named-agent-editor"
	// May write suites and update every Agent in the namespace.
	agentEditor = "assayd-envtest-agent-editor"

	evalSuiteWriters = "assayd-evalsuite-writers"
	// A fragment of the policy's message: the refusal names the fix.
	namesTheFix = "needs `update` on agents.assayd.dev in "
)

// installEvalSuiteWriters applies the chart's assayd-evalsuite-writers under a
// name of this test's, narrowed to namespaces carrying scope.
func installEvalSuiteWriters(t *testing.T, scope string) string {
	t.Helper()
	ctx := context.Background()
	docs := renderedAdmission(t)
	pol, bind := docs["ValidatingAdmissionPolicy/"+evalSuiteWriters], docs["ValidatingAdmissionPolicyBinding/"+evalSuiteWriters]
	if pol == nil || bind == nil {
		t.Fatalf("the chart renders no policy and binding named %s", evalSuiteWriters)
	}
	pol, bind = pol.DeepCopy(), bind.DeepCopy()
	if _, found, _ := unstructured.NestedMap(pol.Object, "spec", "matchConstraints", "namespaceSelector"); found {
		t.Fatalf("the rendered %s now has a namespaceSelector; a suite in an unselected namespace is unguarded", evalSuiteWriters)
	}
	if err := unstructured.SetNestedStringMap(pol.Object, map[string]string{scopeLabel: scope},
		"spec", "matchConstraints", "namespaceSelector", "matchLabels"); err != nil {
		t.Fatal(err)
	}
	unique := scope + "-evalsuite-writers"
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

// grantSuiteWriters gives each user below every verb on EvalSuites in ns, so a
// refusal is admission's and not RBAC's, and the Agent rights its name says.
// Every one of them may `get` Agents, so a check that asked for `get` instead
// of `update` would admit them all.
func grantSuiteWriters(t *testing.T, ns, namedAgent string) {
	t.Helper()
	ctx := context.Background()
	suites := rbacv1.PolicyRule{APIGroups: []string{"assayd.dev"}, Resources: []string{"evalsuites"}, Verbs: []string{"*"}}
	readAgents := rbacv1.PolicyRule{APIGroups: []string{"assayd.dev"}, Resources: []string{"agents"}, Verbs: []string{"get", "list", "watch"}}
	for user, rules := range map[string][]rbacv1.PolicyRule{
		suiteOnlyWriter: {suites, readAgents},
		// The operator's identity, as the chart renders it, with a suite-only
		// writer's rights: Q6 (c) exempts no one, so it is refused as one.
		renderedOperator: {suites, readAgents},
		namedAgentEditor: {suites, readAgents, {APIGroups: []string{"assayd.dev"}, Resources: []string{"agents"},
			ResourceNames: []string{namedAgent}, Verbs: []string{"update", "patch"}}},
		agentEditor: {suites, readAgents, {APIGroups: []string{"assayd.dev"}, Resources: []string{"agents"},
			Verbs: []string{"update", "patch"}}},
	} {
		name := strings.ReplaceAll(user, ":", "-")
		role := &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}, Rules: rules}
		rb := &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
			RoleRef:  rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "Role", Name: name},
			Subjects: []rbacv1.Subject{{Kind: "User", Name: user}}}
		for _, o := range []client.Object{role, rb} {
			if err := k8s.Create(ctx, o); err != nil {
				t.Fatalf("grant %s: %v", user, err)
			}
		}
	}
	// A Role binding takes effect when the authorizer's informer sees it, so
	// wait for RBAC to grant each write before a refusal is read as the
	// policy's.
	for _, user := range []string{suiteOnlyWriter, renderedOperator, namedAgentEditor, agentEditor} {
		for deadline := time.Now().Add(30 * time.Second); !mayAccess(t, user, ns, "evalsuites", "", "create"); time.Sleep(100 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("RBAC never granted %s the EvalSuite writes its Role names", user)
			}
		}
	}
}

// mayUpdateAgent asks the real authorizer whether user may update the Agent
// named name in ns; "" asks namespace-wide, as the policy does.
func mayUpdateAgent(t *testing.T, user, ns, name string) bool {
	t.Helper()
	return mayAccess(t, user, ns, "agents", name, "update")
}

func mayAccess(t *testing.T, user, ns, resource, name, verb string) bool {
	t.Helper()
	sar := &authorizationv1.SubjectAccessReview{Spec: authorizationv1.SubjectAccessReviewSpec{User: user,
		ResourceAttributes: &authorizationv1.ResourceAttributes{Group: "assayd.dev", Resource: resource,
			Namespace: ns, Name: name, Verb: verb}}}
	if err := k8s.Create(context.Background(), sar); err != nil {
		t.Fatalf("SubjectAccessReview for %s: %v", user, err)
	}
	return sar.Status.Allowed
}

// T61: a user who may only write suites is denied; one who may `update` Agents
// only under `resourceNames` is denied; one who may `update` Agents
// namespace-wide is admitted. So is the operator's own identity refused, given
// only a suite writer's rights. For CREATE and for UPDATE; DELETE stays RBAC's.
func TestOnlyAnAgentEditorWritesAnEvalSuite(t *testing.T) {
	ctx := context.Background()
	installGeneratedEvalSuiteCRD(t)
	scope := nsName(t, "")
	ns := scopedNamespace(t, nsName(t, "-suites"), scope, false)
	const agentName = "pricer"
	policy := installEvalSuiteWriters(t, scope)
	grantSuiteWriters(t, ns, agentName)

	// The grants are what the names say, so each refusal below is about the
	// grant's SHAPE: the named editor really may update its Agent, and only it.
	if !mayUpdateAgent(t, namedAgentEditor, ns, agentName) || mayUpdateAgent(t, namedAgentEditor, ns, "") {
		t.Fatalf("%s's grant is not update on %s alone", namedAgentEditor, agentName)
	}
	if mayUpdateAgent(t, suiteOnlyWriter, ns, agentName) {
		t.Fatalf("%s may update an Agent, so it is not a suite-only writer", suiteOnlyWriter)
	}

	// The suite is named for the Agent that gates on it, as a person names
	// one, so a check that asked for the named object would admit the editor
	// scoped to that Agent.
	newSuite := func() *assaydv1alpha1.EvalSuite { return suite(ns, agentName, evalCase("a", "ping", "pong", "")) }
	get := func() *assaydv1alpha1.EvalSuite {
		t.Helper()
		var live assaydv1alpha1.EvalSuite
		if err := k8s.Get(ctx, client.ObjectKey{Namespace: ns, Name: agentName}, &live); err != nil {
			t.Fatalf("read the suite: %v", err)
		}
		return &live
	}

	asWriter, asNamed, asEditor := as(t, suiteOnlyWriter), as(t, namedAgentEditor), as(t, agentEditor)
	asOperator := as(t, renderedOperator)
	untilEnforced(t, policy, func() (client.Object, error) {
		s := newSuite()
		return s, asWriter.Create(ctx, s)
	})

	expectRefused := func(who, op string, err error) {
		t.Helper()
		switch {
		case err == nil:
			t.Errorf("the %s of an EvalSuite by %s was admitted, though it may not update every Agent in %s: "+
				"a suite write sends that namespace's held candidates messages of its writer's choosing (design 16 §1.1, Q6)",
				op, who, ns)
		case !refusedBy(err, policy):
			t.Errorf("the %s by %s was refused, but not by %s, so this proves nothing about the policy: %v", op, who, policy, err)
		case !strings.Contains(err.Error(), namesTheFix+ns):
			t.Errorf("the %s by %s was refused without naming the fix (%q): %v", op, who, namesTheFix+ns, err)
		}
	}

	// CREATE.
	for who, c := range map[string]client.Client{
		"a user who may only write suites":         asWriter,
		"a user who may update one Agent, by name": asNamed,
		"the operator's identity":                  asOperator,
	} {
		s := newSuite()
		err := c.Create(ctx, s)
		if err == nil {
			_ = k8s.Delete(ctx, s)
		}
		expectRefused(who, "create", err)
	}
	if err := asEditor.Create(ctx, newSuite()); err != nil {
		t.Fatalf("a user who may update every Agent in %s could not create an EvalSuite there: %v", ns, err)
	}

	// UPDATE, of the suite the Agent editor wrote.
	for who, c := range map[string]client.Client{
		"a user who may only write suites":         asWriter,
		"a user who may update one Agent, by name": asNamed,
		"the operator's identity":                  asOperator,
	} {
		live := get()
		live.Spec.Dataset.Cases[0].Message = "edited by " + who
		expectRefused(who, "update", c.Update(ctx, live))
	}
	live := get()
	live.Spec.Dataset.Cases[0].Message = "edited by the Agent editor"
	if err := asEditor.Update(ctx, live); err != nil {
		t.Errorf("a user who may update every Agent in %s could not update an EvalSuite there: %v", ns, err)
	}

	// DELETE is RBAC's: the suite-only writer, which RBAC lets delete, does.
	if err := asWriter.Delete(ctx, get()); err != nil {
		t.Errorf("a suite-only writer could not delete an EvalSuite, which only RBAC decides: %v", err)
	}
}
