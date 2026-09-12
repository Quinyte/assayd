// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/compiler"
)

// Design 03 §6 and §3.4.4, on a real cluster with real RBAC: the two
// reservations the chart renders (templates/admission.yaml), and the grants
// design 03 §1.1's operator wiring needs. test/envtest measures the same
// policies' CEL against every identity; this measures them as installed, with
// the operator's real ServiceAccount and a principal that has RBAC to write
// and is refused by admission alone.

const operatorUser = "system:serviceaccount:assayd-system:assayd-agent-operator"

// keyAdminClient is the administrator design 03 §3.4.4 says writes the key
// set: a member of `system:masters`, the default of admission.apiKeyWriters.
// Impersonated under a username of its own, so what admits it is the GROUP.
func keyAdminClient(t *testing.T) client.Client {
	t.Helper()
	cfg := rest.CopyConfig(restCfg)
	cfg.Impersonate = rest.ImpersonationConfig{UserName: "assayd-e2e-key-admin", Groups: []string{"system:masters"}}
	c, err := client.New(cfg, client.Options{Scheme: k8s.Scheme()})
	if err != nil {
		t.Fatalf("impersonating the key administrator: %v", err)
	}
	return c
}

// intruderClient is a principal with every right RBAC can give it on
// ConfigMaps and AgentgatewayPolicies in ns, so that a refusal is admission's.
func intruderClient(t *testing.T, ctx context.Context, ns string) client.Client {
	t.Helper()
	const user = "assayd-e2e-intruder"
	role := &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: user, Namespace: ns},
		Rules: []rbacv1.PolicyRule{
			{APIGroups: []string{""}, Resources: []string{"configmaps"}, Verbs: []string{"*"}},
			{APIGroups: []string{"agentgateway.dev"}, Resources: []string{"agentgatewaypolicies"}, Verbs: []string{"*"}},
		}}
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
	cfg := rest.CopyConfig(restCfg)
	cfg.Impersonate = rest.ImpersonationConfig{UserName: user}
	c, err := client.New(cfg, client.Options{Scheme: k8s.Scheme()})
	if err != nil {
		t.Fatalf("impersonating %s: %v", user, err)
	}
	return c
}

// ensureRunNamespace makes sure assayd-e2e's run namespace exists, by giving
// the namespace an Agent, since only the operator may create one.
func ensureRunNamespace(t *testing.T, ctx context.Context) {
	t.Helper()
	ensureNamespace(t, ctx, "assayd-e2e")
	a := &assaydv1alpha1.Agent{ObjectMeta: metav1.ObjectMeta{Name: "reservation", Namespace: "assayd-e2e"},
		Spec: assaydv1alpha1.AgentSpec{Runtime: &assaydv1alpha1.AgentRuntime{Image: pauseImage}}}
	_ = k8s.Delete(ctx, a)
	if !waitGone(t, ctx, client.ObjectKeyFromObject(a), 2*time.Minute) {
		t.Fatal("a previous run's Agent is still being deleted")
	}
	if err := k8s.Create(ctx, a); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	t.Cleanup(func() { _ = k8s.Delete(context.Background(), a) })
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		var ns corev1.Namespace
		if err := k8s.Get(ctx, types.NamespacedName{Name: runNS}, &ns); err == nil && ns.DeletionTimestamp.IsZero() {
			return
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("the operator never created %s", runNS)
}

func refusedByPolicy(err error, policy string) bool {
	return err != nil && strings.Contains(err.Error(), "ValidatingAdmissionPolicy") &&
		strings.Contains(err.Error(), policy)
}

// §3.4.4: "anyone who can write a ConfigMap carrying the selected label in a
// run namespace can mint a principal." Not any more: a principal with full
// ConfigMap rights there is refused, and the administrator and the operator
// are not.
func TestKeySetsInRunNamespacesAreReservedToTheirWriters(t *testing.T) {
	requireCluster(t)
	requireOperator(t)
	ctx := context.Background()
	ensureRunNamespace(t, ctx)
	asIntruder := intruderClient(t, ctx, runNS)

	keySet := func(name string) *corev1.ConfigMap {
		return &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: runNS,
			Labels: map[string]string{compiler.APIKeySourceLabel: compiler.APIKeySourceValue}},
			Data: map[string]string{"k": `{"keyHash":"sha256:` + strings.Repeat("0", 64) +
				`","metadata":{"group":"assayd-e2e"}}`}}
	}
	minted := keySet("assayd-e2e-minted")
	if err := asIntruder.Create(ctx, minted); err == nil {
		_ = k8s.Delete(ctx, minted)
		t.Fatal("a principal with ConfigMap rights in a run namespace wrote a key set, which mints a " +
			"principal at the gateway with no compile and no revision (design 03 §3.4.4)")
	} else if !refusedByPolicy(err, "assayd-api-keys") {
		t.Fatalf("the key set was refused, but not by assayd-api-keys, so this proves nothing "+
			"about the reservation: %v", err)
	}
	// CONTROL: the same principal writes an ordinary ConfigMap there.
	ordinary := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "assayd-e2e-ordinary", Namespace: runNS}}
	_ = k8s.Delete(ctx, ordinary)
	if err := asIntruder.Create(ctx, ordinary); err != nil {
		t.Fatalf("an unlabelled ConfigMap was refused too, so the refusal above is not about the "+
			"label: %v", err)
	}
	t.Cleanup(func() { _ = k8s.Delete(context.Background(), ordinary) })

	for who, c := range map[string]client.Client{
		"the key administrator (system:masters)": keyAdminClient(t),
		// Its real ServiceAccount, with its real ClusterRole.
		"the operator": operatorClient(t),
	} {
		cm := keySet("assayd-e2e-issued-" + strings.Fields(who)[1])
		_ = k8s.Delete(ctx, cm)
		if err := c.Create(ctx, cm); err != nil {
			t.Errorf("%s could not write a key set: %v", who, err)
			continue
		}
		t.Cleanup(func() { _ = k8s.Delete(context.Background(), cm) })
	}
}

// §6: "refuses CREATE and UPDATE of agentgatewaypolicies in any namespace
// labelled assayd.dev/run-namespace: "true" from any identity but the
// operator's". A cluster administrator included: unlike a key set, a policy
// has no administrator author.
func TestPoliciesInRunNamespacesAreReservedToTheOperator(t *testing.T) {
	requireCluster(t)
	requireOperator(t)
	requireGateway(t)
	ctx := context.Background()
	ensureRunNamespace(t, ctx)
	asIntruder := intruderClient(t, ctx, runNS)

	policy := func() *unstructured.Unstructured {
		p, err := compiler.AuthPolicy(compiler.AuthInput{AgentName: "reserved", AgentNamespace: "assayd-e2e",
			AgentUID: "0f0e0d0c-0000-0000-0000-00000000e2e0", RunNamespace: runNS})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	_ = k8s.Delete(ctx, policy())
	for who, c := range map[string]client.Client{
		"a principal with every RBAC right on policies there": asIntruder,
		"the harness's cluster administrator":                 k8s,
	} {
		p := policy()
		if err := c.Create(ctx, p); err == nil {
			_ = k8s.Delete(ctx, p)
			t.Errorf("%s wrote an AgentgatewayPolicy in a run namespace. A traffic policy on an "+
				"Agent's route decides who may call it, and two on one target resolve at random "+
				"(design 03 §3.2)", who)
		} else if !refusedByPolicy(err, "assayd-gateway-policies") {
			t.Errorf("%s was refused, but not by assayd-gateway-policies: %v", who, err)
		}
	}
	// The operator is admitted: its real ServiceAccount, with its real
	// ClusterRole, which now grants `create` because the `Create` transaction
	// writes each API-key Agent's policy.
	p := policy()
	if err := operatorClient(t).Create(ctx, p); err != nil {
		t.Fatalf("the operator's identity could not write a policy in its run namespace: %v", err)
	}
	t.Cleanup(func() { _ = k8s.Delete(context.Background(), policy()) })
}

func operatorMay(t *testing.T, ctx context.Context, group, resource, verb, ns string) (bool, string) {
	t.Helper()
	sar := &authorizationv1.SubjectAccessReview{Spec: authorizationv1.SubjectAccessReviewSpec{
		User: operatorUser,
		ResourceAttributes: &authorizationv1.ResourceAttributes{
			Group: group, Resource: resource, Verb: verb, Namespace: ns}}}
	if err := k8s.Create(ctx, sar); err != nil {
		t.Fatalf("SubjectAccessReview: %v", err)
	}
	return sar.Status.Allowed, sar.Status.Reason
}

// The `Create` transaction writes each API-key Agent's -auth policy, the
// finalizer reads and deletes it, and the watch lists them, so exactly those
// verbs are granted. Never `patch`: the compiler writes over owned fields, not
// by server-side apply (design 03 §3.2). envtest runs as admin and cannot see
// either half.
func TestTheOperatorMayWriteAPolicyAndNotPatchOne(t *testing.T) {
	requireCluster(t)
	requireOperator(t)
	ctx := context.Background()
	for _, verb := range []string{"get", "list", "watch", "create", "update", "delete"} {
		if ok, why := operatorMay(t, ctx, "agentgateway.dev", "agentgatewaypolicies", verb, runNS); !ok {
			t.Errorf("the operator may not %s agentgatewaypolicies in %s: %s. The Create "+
				"transaction, teardown or the watch would be Forbidden on this cluster", verb, runNS, why)
		}
	}
	if ok, _ := operatorMay(t, ctx, "agentgateway.dev", "agentgatewaypolicies", "patch", runNS); ok {
		t.Error("the operator may patch agentgatewaypolicies, and nothing in it patches one")
	}
}

// The NACK watch lists and watches Events in the Gateway's namespace, and that
// is where the grant is: a Role, not the ClusterRole.
func TestTheOperatorReadsNackEventsOnlyWhereTheGatewayIs(t *testing.T) {
	requireCluster(t)
	requireOperator(t)
	gwNS, _ := requireGateway(t)
	ctx := context.Background()
	for _, verb := range []string{"list", "watch"} {
		if ok, why := operatorMay(t, ctx, "", "events", verb, gwNS); !ok {
			t.Errorf("the operator may not %s events in the Gateway's namespace %s: %s. The NACK "+
				"watch, design 03 §3.3's sixth input, is Forbidden", verb, gwNS, why)
		}
		if ok, _ := operatorMay(t, ctx, "", "events", verb, "assayd-e2e"); ok {
			t.Errorf("the operator may %s events in assayd-e2e, which is not the Gateway's namespace; "+
				"the grant is meant to be one namespace wide", verb)
		}
	}
}
