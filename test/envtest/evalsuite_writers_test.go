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

	authorizationv1 "k8s.io/api/authorization/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
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
// TestTheChartReservesEvalSuiteWritesToAgentEditors), and this refuses a
// rendered policy that narrows itself any other way.

const (
	evalSuiteWriters = "assayd-evalsuite-writers"
	// A fragment of the policy's message: the refusal names the fix.
	namesTheFix = "needs `update` on agents.assayd.dev in "
	// The garbage collector's identity, with bootstrap RBAC alone.
	gcServiceAccount = "system:serviceaccount:kube-system:generic-garbage-collector"
)

// installEvalSuiteWriters applies the chart's assayd-evalsuite-writers under a
// name of this test's, narrowed to namespaces carrying scope. It refuses a
// rendered policy or binding that narrows what the policy reaches, because the
// narrowing it adds here would hide it: a namespaceSelector, matchConditions
// or a binding's matchResources each exempt someone from the check.
func installEvalSuiteWriters(t *testing.T, scope string) string {
	t.Helper()
	ctx := context.Background()
	docs := renderedAdmission(t)
	pol, bind := docs["ValidatingAdmissionPolicy/"+evalSuiteWriters], docs["ValidatingAdmissionPolicyBinding/"+evalSuiteWriters]
	if pol == nil || bind == nil {
		t.Fatalf("the chart renders no policy and binding named %s", evalSuiteWriters)
	}
	pol, bind = pol.DeepCopy(), bind.DeepCopy()
	for _, f := range []struct {
		obj  *unstructured.Unstructured
		path []string
	}{
		{pol, []string{"spec", "matchConstraints", "namespaceSelector"}},
		{pol, []string{"spec", "matchConstraints", "objectSelector"}},
		{pol, []string{"spec", "matchConditions"}},
		{bind, []string{"spec", "matchResources"}},
		{bind, []string{"spec", "paramRef"}},
	} {
		if _, found, _ := unstructured.NestedFieldNoCopy(f.obj.Object, f.path...); found {
			t.Fatalf("the rendered %s %s now sets %s, which exempts whatever it does not select from the check",
				f.obj.GetKind(), evalSuiteWriters, strings.Join(f.path, "."))
		}
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

// renderedOperatorAgentRules is the chart's operator ClusterRole's rules over
// `agents`, as rendered: what the real operator brings to the policy's check.
func renderedOperatorAgentRules(t *testing.T) []rbacv1.PolicyRule {
	t.Helper()
	out, err := exec.Command("helm", "template", "assayd", filepath.Join("..", "..", "charts", "assayd")).Output()
	if err != nil {
		t.Fatalf("helm template: %v", err)
	}
	dec := utilyaml.NewYAMLOrJSONDecoder(bytes.NewReader(out), 4096)
	for {
		var role rbacv1.ClusterRole
		if err := dec.Decode(&role); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatalf("decode the rendered chart: %v", err)
		}
		if role.Kind != "ClusterRole" || role.Name != "assayd-agent-operator" {
			continue
		}
		var rules []rbacv1.PolicyRule
		for _, r := range role.Rules {
			for _, res := range r.Resources {
				if res == "agents" {
					rules = append(rules, r)
				}
			}
		}
		if len(rules) == 0 {
			t.Fatal("the rendered operator ClusterRole grants nothing on agents")
		}
		return rules
	}
	t.Fatal("the chart renders no ClusterRole assayd-agent-operator")
	return nil
}

// grant binds user to rules in ns by a Role of its own. With no rules, the
// binding names a Role that does not exist. RBAC grants nothing from that and
// mentions it in its reason, but returns no error, so the `authorizer`
// library's errored() stays false: no RBAC state found makes it true, and a
// policy that admitted on an authorizer error is caught only by the chart
// test's exact expression.
func grant(t *testing.T, ns, user string, rules ...rbacv1.PolicyRule) {
	t.Helper()
	ctx := context.Background()
	name := strings.NewReplacer(":", "-", ".", "-").Replace(user)
	if len(rules) > 0 {
		role := &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}, Rules: rules}
		if err := k8s.Create(ctx, role); err != nil {
			t.Fatalf("grant %s: %v", user, err)
		}
	}
	kind := "User"
	subject := rbacv1.Subject{Kind: kind, Name: user}
	if sa, ok := strings.CutPrefix(user, "system:serviceaccount:"); ok {
		saNS, saName, _ := strings.Cut(sa, ":")
		subject = rbacv1.Subject{Kind: "ServiceAccount", Namespace: saNS, Name: saName}
	}
	roleName := name
	if len(rules) == 0 {
		roleName = name + "-missing"
	}
	rb := &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{GenerateName: name + "-", Namespace: ns},
		RoleRef:  rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "Role", Name: roleName},
		Subjects: []rbacv1.Subject{subject}}
	if err := k8s.Create(ctx, rb); err != nil {
		t.Fatalf("bind %s: %v", user, err)
	}
}

// waitUntil polls the real authorizer until user may verb resource in ns: a
// Role binding takes effect when the authorizer's informer sees it, and a
// refusal read before then would be RBAC's, not the policy's.
func waitUntil(t *testing.T, user, ns, resource, verb string) {
	t.Helper()
	for deadline := time.Now().Add(30 * time.Second); !mayAccess(t, user, ns, resource, "", verb); time.Sleep(100 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("RBAC never granted %s %s on %s in %s", user, verb, resource, ns)
		}
	}
}

func mayAccess(t *testing.T, user, ns, resource, name, verb string) bool {
	t.Helper()
	sar := &authorizationv1.SubjectAccessReview{Spec: authorizationv1.SubjectAccessReviewSpec{User: user,
		ResourceAttributes: &authorizationv1.ResourceAttributes{Group: "assayd.dev", Resource: resource,
			Namespace: ns, Name: name, Verb: verb}}}
	if sa, ok := strings.CutPrefix(user, "system:serviceaccount:"); ok {
		sar.Spec.Groups = []string{"system:serviceaccounts", "system:serviceaccounts:" + strings.SplitN(sa, ":", 2)[0], "system:authenticated"}
	}
	if err := k8s.Create(context.Background(), sar); err != nil {
		t.Fatalf("SubjectAccessReview for %s: %v", user, err)
	}
	return sar.Status.Allowed
}

// T61: a user who may only write suites is denied; one who may `update` Agents
// only under `resourceNames` is denied; one who may `update` Agents
// namespace-wide is admitted. Beside those three, the rows that pin what the
// check asks: `patch` alone, an Agent editor of another namespace, and a
// binding to a missing Role are refused; a service account is not exempt; the
// operator, with its rendered ClusterRole, passes; and the garbage collector
// removes a finalizer. For CREATE and for UPDATE; DELETE stays RBAC's, and the
// policy reaches nothing but EvalSuites.
func TestOnlyAnAgentEditorWritesAnEvalSuite(t *testing.T) {
	ctx := context.Background()
	installGeneratedEvalSuiteCRD(t)
	scope := nsName(t, "")
	ns := scopedNamespace(t, nsName(t, "-suites"), scope, false)
	other := scopedNamespace(t, nsName(t, "-other"), scope, false)
	const agentName = "pricer"
	policy := installEvalSuiteWriters(t, scope)

	agents := func(verbs ...string) rbacv1.PolicyRule {
		return rbacv1.PolicyRule{APIGroups: []string{"assayd.dev"}, Resources: []string{"agents"}, Verbs: verbs}
	}
	suites := rbacv1.PolicyRule{APIGroups: []string{"assayd.dev"}, Resources: []string{"evalsuites"}, Verbs: []string{"*"}}
	// Every refused user may `get` Agents, so a check that asked for `get`
	// instead of `update` would admit them.
	read := agents("get", "list", "watch")
	named := agents("update", "patch")
	named.ResourceNames = []string{agentName}

	const (
		suiteOnly    = "assayd-envtest-suite-writer"
		namedEditor  = "assayd-envtest-named-agent-editor"
		patchOnly    = "assayd-envtest-patch-only-agent-editor"
		otherEditor  = "assayd-envtest-other-namespace-agent-editor"
		unresolvable = "assayd-envtest-unresolvable-agent-editor"
		editor       = "assayd-envtest-agent-editor"
		agentCreator = "assayd-envtest-agent-creator"
	)
	serviceAccount := "system:serviceaccount:" + ns + ":suite-writer"
	grant(t, ns, suiteOnly, suites, read)
	grant(t, ns, namedEditor, suites, read, named)
	grant(t, ns, patchOnly, suites, read, agents("patch"))
	grant(t, ns, otherEditor, suites, read)
	grant(t, other, otherEditor, agents("update", "patch"))
	grant(t, ns, unresolvable, suites, read)
	grant(t, ns, unresolvable) // a binding to an Agent editor Role that does not exist
	grant(t, ns, serviceAccount, suites, read)
	grant(t, ns, editor, suites, read, agents("update", "patch"))
	grant(t, ns, agentCreator, agents("create"))

	// The operator, with the agent rules its rendered ClusterRole grants,
	// cluster-wide, and a suite write here that the chart does NOT give it:
	// the chart grants the operator `get` alone on evalsuites, and that RBAC,
	// pinned by TestTheOperatorOnlyReadsEvalSuites (test/chart), is what keeps
	// it from writing one. The policy itself exempts no one, and the operator
	// passes its check, as every cluster-wide Agent editor does.
	opRole := &rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: scope + "-operator-agents"},
		Rules: renderedOperatorAgentRules(t)}
	opBinding := &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: scope + "-operator-agents"},
		RoleRef:  rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "ClusterRole", Name: opRole.Name},
		Subjects: []rbacv1.Subject{{Kind: "User", Name: renderedOperator}}}
	for _, o := range []client.Object{opRole, opBinding} {
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatalf("grant the operator its rendered agent rules: %v", err)
		}
		o := o
		t.Cleanup(func() { _ = k8s.Delete(context.Background(), o) })
	}
	grant(t, ns, renderedOperator, suites)

	for _, u := range []string{suiteOnly, namedEditor, patchOnly, otherEditor, unresolvable, serviceAccount, editor, renderedOperator} {
		waitUntil(t, u, ns, "evalsuites", "create")
	}
	waitUntil(t, otherEditor, other, "agents", "update")
	waitUntil(t, renderedOperator, ns, "agents", "update")
	waitUntil(t, agentCreator, ns, "agents", "create")

	// The grants are what the names say, so each refusal below is about the
	// grant's SHAPE.
	if !mayAccess(t, namedEditor, ns, "agents", agentName, "update") || mayAccess(t, namedEditor, ns, "agents", "", "update") {
		t.Fatalf("%s's grant is not update on %s alone", namedEditor, agentName)
	}
	if !mayAccess(t, patchOnly, ns, "agents", "", "patch") || mayAccess(t, patchOnly, ns, "agents", "", "update") {
		t.Fatalf("%s's grant is not patch alone", patchOnly)
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

	untilEnforced(t, policy, func() (client.Object, error) {
		s := newSuite()
		return s, as(t, suiteOnly).Create(ctx, s)
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
	refused := []struct {
		who string
		c   client.Client
	}{
		{"a user who may only write suites", as(t, suiteOnly)},
		{"a user who may update one Agent, by name", as(t, namedEditor)},
		{"a user who may patch, but not update, Agents", as(t, patchOnly)},
		{"a user who may update Agents only in another namespace", as(t, otherEditor)},
		{"a user whose Agent editor binding names a missing Role", as(t, unresolvable)},
		{"a service account that may only write suites", as(t, serviceAccount,
			"system:serviceaccounts", "system:serviceaccounts:"+ns, "system:authenticated")},
	}

	// CREATE.
	for _, u := range refused {
		s := newSuite()
		err := u.c.Create(ctx, s)
		if err == nil {
			_ = k8s.Delete(ctx, s)
		}
		expectRefused(u.who, "create", err)
	}
	if err := as(t, renderedOperator).Create(ctx, newSuite(), client.DryRunAll); err != nil {
		t.Errorf("the operator's identity, holding its rendered ClusterRole's update on every Agent, was refused "+
			"an EvalSuite create; the policy exempts no one and it passes the check: %v", err)
	}
	asEditor := as(t, editor)
	if err := asEditor.Create(ctx, newSuite()); err != nil {
		t.Fatalf("a user who may update every Agent in %s could not create an EvalSuite there: %v", ns, err)
	}

	// UPDATE, of the suite the Agent editor wrote.
	for _, u := range refused {
		live := get()
		live.Spec.Dataset.Cases[0].Message = "edited by " + u.who
		expectRefused(u.who, "update", u.c.Update(ctx, live))
	}
	live := get()
	live.Spec.Dataset.Cases[0].Message = "edited by the Agent editor"
	if err := asEditor.Update(ctx, live); err != nil {
		t.Errorf("a user who may update every Agent in %s could not update an EvalSuite there: %v", ns, err)
	}

	// The garbage collector, with bootstrap RBAC alone, finishes a foreground
	// delete by an UPDATE that removes its finalizer; every update is judged,
	// and it passes the check.
	live = get()
	live.Finalizers = append(live.Finalizers, metav1.FinalizerDeleteDependents)
	if err := k8s.Update(ctx, live); err != nil {
		t.Fatalf("add the foreground finalizer: %v", err)
	}
	live = get()
	live.Finalizers = nil
	if err := as(t, gcServiceAccount).Update(ctx, live); err != nil {
		t.Errorf("the garbage collector could not remove a suite's foreground finalizer: %v", err)
	}

	// The policy reaches EvalSuites alone: a user who may create, but not
	// update, Agents creates one.
	agent := &assaydv1alpha1.Agent{ObjectMeta: metav1.ObjectMeta{Name: agentName, Namespace: ns},
		Spec: assaydv1alpha1.AgentSpec{Runtime: &assaydv1alpha1.AgentRuntime{
			Image: "ghcr.io/acme/agent@sha256:6d5d9666a268df6f000000000000000000000000000000000000000000000000"}}}
	if err := as(t, agentCreator).Create(ctx, agent, client.DryRunAll); err != nil {
		t.Errorf("an Agent create by a user who may create Agents was refused; the policy reaches EvalSuites only: %v", err)
	}

	// DELETE is RBAC's: the suite-only writer, which RBAC lets delete, does.
	if err := as(t, suiteOnly).Delete(ctx, get()); err != nil {
		t.Errorf("a suite-only writer could not delete an EvalSuite, which only RBAC decides: %v", err)
	}
}
