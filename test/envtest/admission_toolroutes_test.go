// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package envtest

import (
	"context"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Design 07 A6.15, the human's T1: `admission.toolRouteWriters` may attach an
// HTTPRoute to the assayd Gateway on any listener but the serving one, `http`,
// and the serving listener stays the operator's. Measured against a real API
// server, with the chart's own rendered `assayd-gateway-routes`, for each
// identity the rule names.
//
// The rendered policy is cluster-wide: a route in any namespace can name the
// Gateway, so it has no namespaceSelector. It gains one here, matching a label
// only this test's namespaces carry, so that it does not reach the routes the
// rest of this package writes as admin. Its CEL, resource rules and operations
// are applied exactly as rendered. The chart test pins that it is cluster-wide
// (test/chart, TestTheToolRouteWritersReachTheRouteReservation).

const (
	toolAuthor      = "assayd-envtest-tool-author"
	toolGroupMember = "assayd-envtest-tool-group-member"
	toolGroup       = "assayd-envtest:tool-authors"
	servingHost     = "pricer.payments.assayd.internal"
	toolHost        = "mcp.assayd.test"

	// Fragments of the three validations' messages, so that each refusal
	// below is evidence about the rule that made it and not only the policy.
	notAWriter   = "are authored by the assayd operator"
	servingOnly  = "serving listener `http` is the assayd operator's alone"
	hostsCollide = "must list its hostnames"
)

// installRouteReservation applies the chart's assayd-gateway-routes, rendered
// with helmArgs, under a name of this test's, narrowed to namespaces carrying
// scope.
func installRouteReservation(t *testing.T, scope string, helmArgs ...string) string {
	t.Helper()
	ctx := context.Background()
	const name = "assayd-gateway-routes"
	docs := renderedAdmission(t, helmArgs...)
	pol, bind := docs["ValidatingAdmissionPolicy/"+name], docs["ValidatingAdmissionPolicyBinding/"+name]
	if pol == nil || bind == nil {
		t.Fatalf("the chart renders no policy and binding named %s", name)
	}
	pol, bind = pol.DeepCopy(), bind.DeepCopy()
	if _, found, _ := unstructured.NestedMap(pol.Object, "spec", "matchConstraints", "namespaceSelector"); found {
		t.Fatalf("the rendered %s now has a namespaceSelector; a route in any namespace can name the Gateway", name)
	}
	if err := unstructured.SetNestedStringMap(pol.Object, map[string]string{scopeLabel: scope},
		"spec", "matchConstraints", "namespaceSelector", "matchLabels"); err != nil {
		t.Fatal(err)
	}
	unique := scope + "-gateway-routes"
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

// parent is a parentRef to Gateway name in gwNS. An empty section names no
// listener; a zero port names none.
func parent(gwNS, name, section string, port int64) map[string]any {
	p := map[string]any{"name": name}
	if gwNS != "" {
		p["namespace"] = gwNS
	}
	if section != "" {
		p["sectionName"] = section
	}
	if port != 0 {
		p["port"] = port
	}
	return p
}

func httpRoute(ns, name string, hosts []string, parents ...map[string]any) *unstructured.Unstructured {
	refs := make([]any, len(parents))
	for i, p := range parents {
		refs[i] = p
	}
	spec := map[string]any{
		"parentRefs": refs,
		"rules": []any{map[string]any{
			"backendRefs": []any{map[string]any{"name": "mcpserver", "port": int64(8080)}},
		}},
	}
	if hosts != nil {
		hs := make([]any, len(hosts))
		for i, h := range hosts {
			hs[i] = h
		}
		spec["hostnames"] = hs
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gateway.networking.k8s.io/v1",
		"kind":       "HTTPRoute",
		"metadata":   map[string]any{"name": name, "namespace": ns},
		"spec":       spec,
	}}
}

// grpcRoute is httpRoute's route as a GRPCRoute: the same parentRefs and
// hostnames, and a backendRef a GRPCRoute also accepts.
func grpcRoute(ns, name string, hosts []string, parents ...map[string]any) *unstructured.Unstructured {
	r := httpRoute(ns, name, hosts, parents...)
	r.SetKind("GRPCRoute")
	return r
}

func getRoute(t *testing.T, ns, name string) *unstructured.Unstructured {
	t.Helper()
	r := httpRoute(ns, name, nil)
	if err := k8s.Get(context.Background(), client.ObjectKey{Namespace: ns, Name: name}, r); err != nil {
		t.Fatalf("read route %s/%s: %v", ns, name, err)
	}
	return r
}

// refusedWith reports whether err is the named policy's denial by the
// validation whose message carries fragment.
func refusedWith(err error, policy, fragment string) bool {
	return refusedBy(err, policy) && strings.Contains(err.Error(), fragment)
}

// expect asserts one write's outcome: admitted when want is empty, otherwise
// refused by the validation whose message carries want.
func expect(t *testing.T, policy, what, want string, err error) {
	t.Helper()
	switch {
	case want == "" && err != nil:
		t.Errorf("%s was refused: %v", what, err)
	case want != "" && !refusedWith(err, policy, want):
		t.Errorf("%s: want a refusal by %s carrying %q, got %v", what, policy, want, err)
	}
}

func TestAToolRouteWriterAttachesOnlyOffTheServingListener(t *testing.T) {
	ctx := context.Background()
	scope := nsName(t, "")
	tools := scopedNamespace(t, nsName(t, "-tools"), scope, false)
	gw := scopedNamespace(t, nsName(t, "-gw"), scope, false)
	served := scopedNamespace(t, nsName(t, "-served"), scope, false)
	policy := installRouteReservation(t, scope,
		"--set", "gateway.namespace="+gw,
		"--set", "admission.toolRouteWriters.users={"+toolAuthor+"}",
		"--set", "admission.toolRouteWriters.groups={"+toolGroup+"}")
	grantWriters(t, scope, []string{tools, gw, served},
		renderedOperator, toolAuthor, toolGroupMember, intruder, gcUser)
	asAuthor := as(t, toolAuthor)
	asMember := as(t, toolGroupMember, toolGroup)
	asIntruder := as(t, intruder)
	asOperator := as(t, renderedOperator)
	asGC := as(t, gcUser)

	n := 0
	untilEnforced(t, policy, func() (client.Object, error) {
		n++
		r := httpRoute(tools, "probe-"+strings.Repeat("x", n%3+1), []string{toolHost}, parent(gw, "assayd", "tools", 0))
		return r, asIntruder.Create(ctx, r)
	})

	onTools := parent(gw, "assayd", "tools", 0)
	onHTTP := parent(gw, "assayd", "http", 0)
	for i, c := range []struct {
		what  string
		who   client.Client
		ns    string
		hosts []string
		refs  []map[string]any
		want  string
	}{
		{"a tool route writer on `tools`", asAuthor, tools, []string{toolHost}, []map[string]any{onTools}, ""},
		{"a member of a tool route writer group on `tools`", asMember, tools, []string{toolHost},
			[]map[string]any{onTools}, ""},
		{"a tool route writer on `tools` at its port", asAuthor, tools, []string{toolHost},
			[]map[string]any{parent(gw, "assayd", "tools", 8081)}, ""},
		{"a tool route writer on `tools` and on another Gateway's `http`", asAuthor, tools, []string{toolHost},
			[]map[string]any{onTools, parent(gw, "elsewhere", "http", 0)}, ""},
		{"a tool route writer naming the Gateway by bare name from its namespace, on `tools`", asAuthor, gw,
			[]string{toolHost}, []map[string]any{parent("", "assayd", "tools", 0)}, ""},

		{"a tool route writer on `http`", asAuthor, tools, []string{toolHost}, []map[string]any{onHTTP}, servingOnly},
		{"a tool route writer naming no sectionName, which attaches to every listener", asAuthor, tools,
			[]string{toolHost}, []map[string]any{parent(gw, "assayd", "", 0)}, servingOnly},
		{"a tool route writer naming a port and no sectionName", asAuthor, tools, []string{toolHost},
			[]map[string]any{parent(gw, "assayd", "", 8080)}, servingOnly},
		{"a tool route writer on `tools` and on `http`", asAuthor, tools, []string{toolHost},
			[]map[string]any{onTools, onHTTP}, servingOnly},
		{"a tool route writer on another Gateway and on this one with no sectionName", asAuthor, tools,
			[]string{toolHost}, []map[string]any{parent(gw, "elsewhere", "", 0), parent(gw, "assayd", "", 0)},
			servingOnly},
		{"a tool route writer naming the Gateway by bare name from its namespace, on `http`", asAuthor, gw,
			[]string{toolHost}, []map[string]any{parent("", "assayd", "http", 0)}, servingOnly},

		{"a tool route writer on `tools` matching an Agent's serving host", asAuthor, tools,
			[]string{servingHost}, []map[string]any{onTools}, hostsCollide},
		{"a tool route writer on `tools` matching the serving suffix itself", asAuthor, tools,
			[]string{"assayd.internal"}, []map[string]any{onTools}, hostsCollide},
		{"a tool route writer on `tools` with a wildcard under the serving suffix", asAuthor, tools,
			[]string{"*.payments.assayd.internal"}, []map[string]any{onTools}, hostsCollide},
		{"a tool route writer on `tools` with a wildcard above the serving suffix", asAuthor, tools,
			[]string{"*.internal"}, []map[string]any{onTools}, hostsCollide},
		{"a tool route writer on `tools` with no hostnames, which matches every host", asAuthor, tools,
			nil, []map[string]any{onTools}, hostsCollide},
		{"a tool route writer on `tools` with one clear host and one serving host", asAuthor, tools,
			[]string{toolHost, servingHost}, []map[string]any{onTools}, hostsCollide},

		{"an identity not in admission.toolRouteWriters on `tools`", asIntruder, tools, []string{toolHost},
			[]map[string]any{onTools}, notAWriter},
		{"a cluster administrator (system:masters) not in admission.toolRouteWriters on `tools`",
			as(t, "assayd-envtest-masters", "system:masters"), tools, []string{toolHost},
			[]map[string]any{onTools}, notAWriter},
		{"an identity not in admission.toolRouteWriters on a route to another Gateway", asIntruder, tools,
			[]string{servingHost}, []map[string]any{parent(gw, "elsewhere", "http", 0)}, ""},

		{"the operator on `http` with an Agent's serving host", asOperator, served, []string{servingHost},
			[]map[string]any{onHTTP}, ""},
		{"the operator naming no sectionName and no hostnames", asOperator, served, nil,
			[]map[string]any{parent(gw, "assayd", "", 0)}, ""},
		{"the operator on `tools` with an Agent's serving host", asOperator, served, []string{servingHost},
			[]map[string]any{onTools}, ""},
	} {
		r := httpRoute(c.ns, "case-"+string(rune('a'+i)), c.hosts, c.refs...)
		expect(t, policy, c.what, c.want, c.who.Create(ctx, r))
	}

	// A GRPCRoute attaches to an HTTP listener as an HTTPRoute does, with the
	// same parentRefs and hostnames, so the serving listener is kept from it too.
	for i, c := range []struct {
		what  string
		who   client.Client
		hosts []string
		ref   map[string]any
		want  string
	}{
		{"a tool route writer's GRPCRoute on `tools`", asAuthor, []string{toolHost}, onTools, ""},
		{"a tool route writer's GRPCRoute on `http`", asAuthor, []string{toolHost}, onHTTP, servingOnly},
		{"a tool route writer's GRPCRoute on `tools` with an Agent's serving host", asAuthor,
			[]string{servingHost}, onTools, hostsCollide},
		{"an unlisted identity's GRPCRoute on `tools`", asIntruder, []string{toolHost}, onTools, notAWriter},
	} {
		expect(t, policy, c.what, c.want,
			c.who.Create(ctx, grpcRoute(tools, "grpc-"+string(rune('a'+i)), c.hosts, c.ref)))
	}

	// UPDATEs of a tool route its writer created. The new object is judged,
	// and the old one too: a route that names the Gateway before the update is
	// one whose writer must be permitted.
	tool := httpRoute(tools, "tool", []string{toolHost}, onTools)
	if err := asAuthor.Create(ctx, tool); err != nil {
		t.Fatalf("a tool route writer could not create its route: %v", err)
	}
	edit := func(who client.Client, f func(r *unstructured.Unstructured)) error {
		r := getRoute(t, tools, "tool")
		f(r)
		return who.Update(ctx, r)
	}
	setRefs := func(refs ...map[string]any) func(*unstructured.Unstructured) {
		return func(r *unstructured.Unstructured) {
			l := make([]any, len(refs))
			for i, p := range refs {
				l[i] = p
			}
			_ = unstructured.SetNestedSlice(r.Object, l, "spec", "parentRefs")
		}
	}
	setHosts := func(hosts ...string) func(*unstructured.Unstructured) {
		return func(r *unstructured.Unstructured) {
			_ = unstructured.SetNestedStringSlice(r.Object, hosts, "spec", "hostnames")
		}
	}
	for _, c := range []struct {
		what string
		who  client.Client
		f    func(*unstructured.Unstructured)
		want string
	}{
		{"a tool route writer moving its route from `tools` to `http`", asAuthor, setRefs(onHTTP), servingOnly},
		{"a tool route writer removing its route's sectionName", asAuthor,
			setRefs(parent(gw, "assayd", "", 0)), servingOnly},
		{"a tool route writer giving its route an Agent's serving host", asAuthor, setHosts(toolHost, servingHost),
			hostsCollide},
		{"a non-writer rewriting a tool route's hostnames", asIntruder, setHosts("other.assayd.test"), notAWriter},
		{"a non-writer relabelling a tool route", asIntruder, func(r *unstructured.Unstructured) {
			r.SetLabels(map[string]string{"assayd.dev/agent": "pricer"})
		}, notAWriter},
		{"a non-writer annotating a tool route", asIntruder, func(r *unstructured.Unstructured) {
			r.SetAnnotations(map[string]string{"example.com/note": "x"})
		}, notAWriter},
		{"a non-writer adding a finalizer to a tool route", asIntruder, func(r *unstructured.Unstructured) {
			r.SetFinalizers(append(r.GetFinalizers(), "example.com/stall"))
		}, notAWriter},
		{"a non-writer adding an ownerReference to a tool route", asIntruder, func(r *unstructured.Unstructured) {
			r.SetOwnerReferences([]metav1.OwnerReference{{APIVersion: "v1", Kind: "ConfigMap",
				Name: "gone", UID: "0f0e0d0c-0000-0000-0000-00000000dead"}})
		}, notAWriter},
		{"a non-writer moving a tool route off the Gateway", asIntruder,
			setRefs(parent(gw, "elsewhere", "http", 0)), notAWriter},
		{"a tool route writer changing its route's hostnames", asAuthor, setHosts("other.assayd.test"), ""},
	} {
		expect(t, policy, c.what, c.want, edit(c.who, c.f))
	}

	// The operator's serving route. A tool route writer may not take it off
	// the serving listener, to another listener or off the Gateway: the old
	// object names `http`, so the update is a write on the serving listener.
	serving := httpRoute(served, "pricer-serving", []string{servingHost}, onHTTP)
	if err := asOperator.Create(ctx, serving); err != nil {
		t.Fatalf("the operator could not create a serving route: %v", err)
	}
	editServing := func(who client.Client, f func(r *unstructured.Unstructured)) error {
		r := getRoute(t, served, "pricer-serving")
		f(r)
		return who.Update(ctx, r)
	}
	expect(t, policy, "a tool route writer moving the operator's serving route to `tools`", servingOnly,
		editServing(asAuthor, setRefs(onTools)))
	expect(t, policy, "a tool route writer moving the operator's serving route off the Gateway", servingOnly,
		editServing(asAuthor, setRefs(parent(gw, "elsewhere", "http", 0))))
	// A route naming the Gateway with no sectionName is on every listener, the
	// serving one included, so narrowing it onto `tools` is a write on the
	// serving listener: the old object is judged as well as the new one.
	if err := asOperator.Create(ctx, httpRoute(served, "everywhere", []string{servingHost},
		parent(gw, "assayd", "", 0))); err != nil {
		t.Fatalf("the operator could not create a route with no sectionName: %v", err)
	}
	narrowed := getRoute(t, served, "everywhere")
	setRefs(onTools)(narrowed)
	setHosts(toolHost)(narrowed)
	expect(t, policy, "a tool route writer narrowing a route with no sectionName onto `tools`", servingOnly,
		asAuthor.Update(ctx, narrowed))
	expect(t, policy, "the operator updating its serving route", "",
		editServing(asOperator, func(r *unstructured.Unstructured) {
			r.SetAnnotations(map[string]string{"assayd.dev/test": "rewritten"})
		}))

	// The garbage collector finishes a foreground delete by removing the
	// `foregroundDeletion` finalizer: an UPDATE that changes nothing the
	// reservation protects. Refused, the route stays Terminating forever, and
	// with it the teardown of the Agent that waits for it (design 03 A70). What
	// the route says stays reserved while it waits.
	if err := k8s.Delete(ctx, getRoute(t, served, "pricer-serving"),
		client.PropagationPolicy(metav1.DeletePropagationForeground)); err != nil {
		t.Fatalf("foreground-delete the serving route: %v", err)
	}
	held := getRoute(t, served, "pricer-serving")
	if held.GetDeletionTimestamp() == nil {
		t.Fatalf("want the route held by the foreground finalizer, got %v", held.GetFinalizers())
	}
	expect(t, policy, "a non-writer rewriting a serving route that is being deleted", notAWriter,
		editServing(asIntruder, setHosts("elsewhere.assayd.test")))
	if err := editServing(asGC, func(r *unstructured.Unstructured) { r.SetFinalizers(nil) }); err != nil {
		t.Fatalf("the garbage collector could not finish a foreground delete of a route attached to the "+
			"assayd Gateway, so the route and the teardown of its Agent wait forever: %v", err)
	}
	if err := k8s.Get(ctx, client.ObjectKey{Namespace: served, Name: "pricer-serving"},
		httpRoute(served, "pricer-serving", nil)); !apierrors.IsNotFound(err) {
		t.Errorf("the route outlived its foreground delete: %v", err)
	}
}

// With admission.toolRouteWriters empty, the default, the serving listener and
// every other listener are the operator's: what the chart rendered before T1.
func TestWithNoToolRouteWritersOnlyTheOperatorAttachesARoute(t *testing.T) {
	ctx := context.Background()
	scope := nsName(t, "")
	tools := scopedNamespace(t, nsName(t, "-tools"), scope, false)
	gw := scopedNamespace(t, nsName(t, "-gw"), scope, false)
	policy := installRouteReservation(t, scope, "--set", "gateway.namespace="+gw)
	grantWriters(t, scope, []string{tools}, renderedOperator, toolAuthor, toolGroupMember)
	asAuthor := as(t, toolAuthor)

	untilEnforced(t, policy, func() (client.Object, error) {
		r := httpRoute(tools, "probe", []string{toolHost}, parent(gw, "assayd", "tools", 0))
		return r, asAuthor.Create(ctx, r)
	})
	expect(t, policy, "an unlisted identity on `tools`", notAWriter,
		asAuthor.Create(ctx, httpRoute(tools, "author", []string{toolHost}, parent(gw, "assayd", "tools", 0))))
	expect(t, policy, "an unlisted group member on `tools`", notAWriter,
		as(t, toolGroupMember, toolGroup).Create(ctx,
			httpRoute(tools, "member", []string{toolHost}, parent(gw, "assayd", "tools", 0))))
	expect(t, policy, "the operator on `tools`", "",
		as(t, renderedOperator).Create(ctx, httpRoute(tools, "operator", []string{toolHost},
			parent(gw, "assayd", "tools", 0))))
}
