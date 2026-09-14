// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	authorizationv1 "k8s.io/api/authorization/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Design 07 A6.15, the human's T1, on a real cluster. hack/e2e.sh installs the
// chart with admission.toolRouteWriters naming one test identity, and the MCP
// tests author their tool route as it (attachMCPRoute). test/envtest measures
// every case of the rule against the rendered policy; this measures that the
// value an install sets reaches the policy the API server enforces, for an
// identity holding its own RBAC and nothing more.

// toolRouteWriter is the identity hack/e2e.sh put in admission.toolRouteWriters.
func toolRouteWriter(t *testing.T) string {
	t.Helper()
	w := os.Getenv("ASSAYD_E2E_TOOL_ROUTE_WRITER")
	if w == "" {
		t.Fatal("ASSAYD_E2E_TOOL_ROUTE_WRITER is unset; hack/e2e.sh names the identity it gives the chart " +
			"as admission.toolRouteWriters")
	}
	return w
}

// routeWriterClient impersonates user with every verb on HTTPRoutes in ns and
// nothing else. admission.toolRouteWriters grants no RBAC, so the grant is the
// harness's, as it would be an administrator's, and a refusal below is
// admission's.
func routeWriterClient(t *testing.T, ctx context.Context, user, ns string) client.Client {
	t.Helper()
	name := user + "-routes"
	role := &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Rules: []rbacv1.PolicyRule{{APIGroups: []string{"gateway.networking.k8s.io"},
			Resources: []string{"httproutes"}, Verbs: []string{"get", "list", "watch", "create", "update", "delete"}}}}
	rb := &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		RoleRef:  rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "Role", Name: name},
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
	// A grant takes effect when the authorizer has seen the binding, not when
	// the create returns. A write before then is refused by RBAC, which would
	// read as admission's refusal or fail the control, so wait for it.
	deadline := time.Now().Add(30 * time.Second)
	for {
		review := &authorizationv1.SelfSubjectAccessReview{Spec: authorizationv1.SelfSubjectAccessReviewSpec{
			ResourceAttributes: &authorizationv1.ResourceAttributes{Group: "gateway.networking.k8s.io",
				Resource: "httproutes", Verb: "create", Namespace: ns}}}
		if err := c.Create(ctx, review); err == nil && review.Status.Allowed {
			return c
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s was granted httproutes in %s and never allowed to create one", user, ns)
		}
		time.Sleep(time.Second)
	}
}

func TestTheInstalledRouteReservationKeepsToolRouteWritersOffTheServingListener(t *testing.T) {
	requireCluster(t)
	requireOperator(t)
	gwNS, gwName := requireGateway(t)
	toolsNS := os.Getenv("ASSAYD_E2E_TOOLS_NS")
	if toolsNS == "" {
		t.Fatal("ASSAYD_E2E_TOOLS_NS is unset; the harness creates the namespace the `tools` listener admits")
	}
	ctx := context.Background()
	asWriter := routeWriterClient(t, ctx, toolRouteWriter(t), toolsNS)
	asIntruder := routeWriterClient(t, ctx, "assayd-e2e-route-intruder", toolsNS)

	route := func(name, section, host string) *unstructured.Unstructured {
		ref := map[string]any{"name": gwName, "namespace": gwNS}
		if section != "" {
			ref["sectionName"] = section
		}
		return &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "gateway.networking.k8s.io/v1",
			"kind":       "HTTPRoute",
			"metadata":   map[string]any{"name": name, "namespace": toolsNS},
			"spec": map[string]any{
				"parentRefs": []any{ref},
				"hostnames":  []any{host},
				"rules": []any{map[string]any{
					"backendRefs": []any{map[string]any{"name": "mcpserver", "port": int64(8080)}},
				}},
			},
		}}
	}
	for _, c := range []struct {
		what string
		who  client.Client
		r    *unstructured.Unstructured
		want string
	}{
		{"the tool route writer on the serving listener `http`", asWriter,
			route("assayd-e2e-tr-http", "http", "probe.assayd.test"), "serving listener `http`"},
		{"the tool route writer with no sectionName, which attaches to every listener", asWriter,
			route("assayd-e2e-tr-nosection", "", "probe.assayd.test"), "serving listener `http`"},
		{"the tool route writer on `tools` with an Agent's serving host", asWriter,
			route("assayd-e2e-tr-host", "tools", "throughgw.assayd-e2e.assayd.internal"), "must list its hostnames"},
		{"an identity not in admission.toolRouteWriters on `tools`", asIntruder,
			route("assayd-e2e-tr-intruder", "tools", "probe.assayd.test"), "are authored by the assayd operator"},
	} {
		err := c.who.Create(ctx, c.r)
		if err == nil {
			_ = k8s.Delete(ctx, c.r)
			t.Errorf("%s was admitted", c.what)
		} else if !refusedByPolicy(err, "assayd-gateway-routes") || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s was refused, but not by assayd-gateway-routes' rule carrying %q, so this proves "+
				"nothing about that rule: %v", c.what, c.want, err)
		}
	}
	// CONTROL: the same identity, on `tools`, with a host that is not an
	// Agent's, is admitted, so the refusals above are about the listener, the
	// host and the identity, and not about the writer.
	ok := route("assayd-e2e-tr-tools", "tools", "probe.assayd.test")
	_ = k8s.Delete(ctx, ok)
	if err := asWriter.Create(ctx, ok); err != nil {
		t.Fatalf("the chart's admission.toolRouteWriters could not attach a route to `tools`, so the "+
			"value did not reach the installed policy: %v", err)
	}
	t.Cleanup(func() { _ = k8s.Delete(context.Background(), ok) })
}
