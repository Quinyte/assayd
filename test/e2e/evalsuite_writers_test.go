// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	authorizationv1 "k8s.io/api/authorization/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
)

// Design 16 §1.1, Q6 (c), on the e2e's k3s with the chart as installed: an
// EvalSuite create or update is admitted only from a principal the authorizer
// says may `update` Agents in the suite's namespace, namespace-wide. Q6
// measured the `authorizer` library on envtest v1.36.2 and recorded it
// unmeasured on this k3s; test/envtest measures the same three users against
// the rendered policy, and this measures them against the installed one.

const suitesNS = "assayd-e2e-suites"

// suiteWriterClient grants user every verb on EvalSuites in ns, `get` on its
// Agents, and the Agent rule given, then impersonates it. Every user may
// `get` Agents, so a check that asked for `get` would admit all three.
func suiteWriterClient(t *testing.T, ctx context.Context, ns, user string, agents *rbacv1.PolicyRule) client.Client {
	t.Helper()
	rules := []rbacv1.PolicyRule{
		{APIGroups: []string{"assayd.dev"}, Resources: []string{"evalsuites"}, Verbs: []string{"*"}},
		{APIGroups: []string{"assayd.dev"}, Resources: []string{"agents"}, Verbs: []string{"get", "list", "watch"}},
	}
	if agents != nil {
		rules = append(rules, *agents)
	}
	role := &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: user, Namespace: ns}, Rules: rules}
	rb := &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: user, Namespace: ns},
		RoleRef:  rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "Role", Name: user},
		Subjects: []rbacv1.Subject{{Kind: "User", Name: user}}}
	for _, o := range []client.Object{role, rb} {
		_ = k8s.Delete(ctx, o)
		if err := k8s.Create(ctx, o); err != nil {
			t.Fatalf("create %s: %v", o.GetName(), err)
		}
		o := o
		t.Cleanup(func() { _ = k8s.Delete(context.Background(), o) })
	}
	// A Role binding takes effect when the authorizer's informer sees it, so
	// wait for RBAC to grant the write before any refusal is read as the
	// policy's.
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(500 * time.Millisecond) {
		sar := &authorizationv1.SubjectAccessReview{Spec: authorizationv1.SubjectAccessReviewSpec{User: user,
			ResourceAttributes: &authorizationv1.ResourceAttributes{Group: "assayd.dev", Resource: "evalsuites",
				Namespace: ns, Verb: "create"}}}
		if err := k8s.Create(ctx, sar); err != nil {
			t.Fatalf("SubjectAccessReview for %s: %v", user, err)
		}
		if sar.Status.Allowed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("RBAC never granted %s the EvalSuite writes its Role names", user)
		}
	}
	cfg := rest.CopyConfig(restCfg)
	cfg.Impersonate = rest.ImpersonationConfig{UserName: user}
	c, err := client.New(cfg, client.Options{Scheme: k8s.Scheme()})
	if err != nil {
		t.Fatalf("impersonating %s: %v", user, err)
	}
	return c
}

// T62: the same three users as T61, with the same results.
func TestOnlyAnAgentEditorWritesAnEvalSuiteOnTheE2ECluster(t *testing.T) {
	requireCluster(t)
	requireOperator(t)
	ctx := context.Background()
	ensureNamespace(t, ctx, suitesNS)
	const agentName = "pricer"

	updateAgents := rbacv1.PolicyRule{APIGroups: []string{"assayd.dev"}, Resources: []string{"agents"},
		Verbs: []string{"update", "patch"}}
	updateOneAgent := updateAgents
	updateOneAgent.ResourceNames = []string{agentName}
	asWriter := suiteWriterClient(t, ctx, suitesNS, "assayd-e2e-suite-writer", nil)
	asNamed := suiteWriterClient(t, ctx, suitesNS, "assayd-e2e-named-agent-editor", &updateOneAgent)
	asEditor := suiteWriterClient(t, ctx, suitesNS, "assayd-e2e-agent-editor", &updateAgents)

	// Named for the Agent that gates on it, as a person names one.
	newSuite := func() *assaydv1alpha1.EvalSuite {
		return &assaydv1alpha1.EvalSuite{ObjectMeta: metav1.ObjectMeta{Namespace: suitesNS, Name: agentName},
			Spec: assaydv1alpha1.EvalSuiteSpec{Dataset: assaydv1alpha1.EvalDataset{Cases: []assaydv1alpha1.EvalCase{
				{ID: "a", Message: "ping", Expect: assaydv1alpha1.EvalExpect{Contains: "pong"}}}}}}
	}
	_ = k8s.Delete(ctx, newSuite())
	if !waitGone(t, ctx, client.ObjectKeyFromObject(newSuite()), time.Minute) {
		t.Fatal("a previous run's EvalSuite is still being deleted")
	}
	t.Cleanup(func() { _ = k8s.Delete(context.Background(), newSuite()) })

	const fix = "needs `update` on agents.assayd.dev in " + suitesNS
	expectRefused := func(who, op string, err error) {
		t.Helper()
		switch {
		case err == nil:
			t.Errorf("the %s of an EvalSuite by %s was admitted, though it may not update every Agent in %s: "+
				"a suite write sends that namespace's held candidates messages of its writer's choosing "+
				"(design 16 §1.1, Q6)", op, who, suitesNS)
		case !refusedByPolicy(err, "assayd-evalsuite-writers"):
			t.Errorf("the %s by %s was refused, but not by assayd-evalsuite-writers, so this proves nothing "+
				"about the policy: %v", op, who, err)
		case !strings.Contains(err.Error(), fix):
			t.Errorf("the %s by %s was refused without naming the fix (%q): %v", op, who, fix, err)
		}
	}
	users := []struct {
		who string
		c   client.Client
	}{
		{"a user who may only write suites", asWriter},
		{"a user who may update one Agent, by name", asNamed},
	}

	// CREATE.
	for _, u := range users {
		s := newSuite()
		err := u.c.Create(ctx, s)
		if err == nil {
			_ = k8s.Delete(ctx, s)
		}
		expectRefused(u.who, "create", err)
	}
	if err := asEditor.Create(ctx, newSuite()); err != nil {
		t.Fatalf("a user who may update every Agent in %s could not create an EvalSuite there: %v", suitesNS, err)
	}

	// UPDATE, of the suite the Agent editor wrote.
	get := func() *assaydv1alpha1.EvalSuite {
		t.Helper()
		var live assaydv1alpha1.EvalSuite
		if err := k8s.Get(ctx, client.ObjectKeyFromObject(newSuite()), &live); err != nil {
			t.Fatalf("read the suite: %v", err)
		}
		return &live
	}
	for _, u := range users {
		live := get()
		live.Spec.Dataset.Cases[0].Message = "edited by " + u.who
		expectRefused(u.who, "update", u.c.Update(ctx, live))
	}
	live := get()
	live.Spec.Dataset.Cases[0].Message = "edited by the Agent editor"
	if err := asEditor.Update(ctx, live); err != nil {
		t.Errorf("a user who may update every Agent in %s could not update an EvalSuite there: %v", suitesNS, err)
	}

	// DELETE is RBAC's: the suite-only writer, which RBAC lets delete, does.
	if err := asWriter.Delete(ctx, get()); err != nil {
		t.Errorf("a suite-only writer could not delete an EvalSuite, which only RBAC decides: %v", err)
	}
}
