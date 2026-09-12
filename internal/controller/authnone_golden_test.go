// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	"sigs.k8s.io/yaml"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/compiler"
)

// -update rewrites the golden files from what the renderer produces now.
//
//	go test ./internal/controller -run Golden -update
var update = flag.Bool("update", false, "rewrite the golden files under testdata/")

const goldenHeader = "# SPDX-FileCopyrightText: 2026 Quinyte\n" +
	"# SPDX-License-Identifier: Apache-2.0\n" +
	"#\n" +
	"# GOLDEN FILE, rendered by the code under test. Regenerate with\n" +
	"#   go test ./internal/controller -run Golden -update\n" +
	"# and read the diff.\n"

func authTestAgent(auth string) *assaydv1alpha1.Agent {
	return &assaydv1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{
			Name: "pricer", Namespace: "payments",
			UID: types.UID("0b5f4c2e-7d1a-4e59-9c3b-6a8d2f1e0c47"),
		},
		Spec: assaydv1alpha1.AgentSpec{
			Runtime: &assaydv1alpha1.AgentRuntime{Image: "ghcr.io/acme/a@sha256:" + strings.Repeat("0", 64)},
			Expose:  &assaydv1alpha1.ExposeSpec{A2A: &assaydv1alpha1.ExposeProtocol{Auth: auth}},
		},
	}
}

// renderRoute renders the serving route with the shipped builder,
// servingRouteFor, adds `extra` labels (a planted marker, for instance), and
// sets the auth marker for `published`, which THIS TEST SUPPLIES. Which mode a
// write publishes under is the unbuilt transaction's decision (see
// compiler.RouteAuthLabels), so nothing here can show that an emitter passes
// the published mode rather than the desired one. That is §8.1 case 10(a)'s to
// prove, against the emitter, when it exists.
//
// The marker step is the one design 07's emitter owes: servingRouteFor does not
// call RouteAuthLabels. Today's emitter renders no marker; `equalRoute` checks
// only that the desired labels are present, so a planted marker survives a
// reconcile in which nothing else drifted, and when something else does drift
// `ensureServingRoute` replaces the labels wholesale with servingRouteFor's.
func renderRoute(t *testing.T, agent *assaydv1alpha1.Agent, published compiler.AuthMode,
	extra map[string]string) *gatewayv1.HTTPRoute {
	t.Helper()
	r := &AgentReconciler{Gateway: GatewayConfig{
		Enabled: true, Name: "assayd", Namespace: "assayd-gateway",
		HostnameSuffix: DefaultGatewayHostnameSuffix,
	}}
	name, err := compiler.ServingRouteName(agent.Name)
	if err != nil {
		t.Fatalf("name: %v", err)
	}
	route := r.servingRouteFor(agent, RunNamespaceName(agent.Namespace), "abc1234567", name, 8080)
	for k, v := range extra {
		route.Labels[k] = v
	}
	route.Labels = compiler.RouteAuthLabels(published, route.Labels)
	return route
}

// An `auth: none` Agent compiles to no policy, and its serving route, published
// under `none`, carries the marker (§3.3.1).
//
// **This does not satisfy §8.1's golden of the emitted route.** The emitter
// does not apply the marker; renderRoute does, in this test, with a mode the
// test supplies. So this pins the marker's key, value and placement on the
// shipped route shape — not that any route in a cluster carries the label, and
// not which mode an emitter would pass. §8.1's golden is owed when the emitter
// renders the marker itself.
func TestGoldenTheAuthNoneMarkerOnTheRenderedServingRoute(t *testing.T) {
	agent := authTestAgent("none")
	target, err := compiler.CompileAuth(agent, RunNamespaceName(agent.Namespace))
	if err != nil {
		t.Fatalf("auth: none must always compile (§3.3.1): %v", err)
	}
	if target.Mode != compiler.AuthModeNone || target.Policy != nil || target.Digest != "" {
		t.Fatalf("auth: none compiled to %+v; want mode none, NO policy and no digest. The "+
			"opt-out is a label on the route, never an empty policy, so it cannot fail an "+
			"acceptance gate (§3.3.1)", target)
	}

	route := renderRoute(t, agent, compiler.AuthModeNone, nil)
	if got := route.Labels["assayd.dev/auth"]; got != "none" {
		t.Errorf("the route's assayd.dev/auth label is %q; §3.3.1's marker is exactly \"none\"", got)
	}
	for k, v := range map[string]string{LabelAgent: "pricer", LabelRevision: "abc1234567"} {
		if route.Labels[k] != v {
			t.Errorf("marking the route lost its label %s (%q, want %q)", k, route.Labels[k], v)
		}
	}
	assertRouteGolden(t, "pricer-serving-auth-none.golden.yaml", route)
}

// A marker planted on the rendered route is removed when the route is
// published under `apikey`. A route saying "deliberately unauthenticated" while
// it is authenticated is the reverse of the lie the marker exists to prevent.
// Like the golden above, the published mode is supplied by the test.
func TestRouteAuthLabelsRemovesAPlantedMarkerUnderAPIKey(t *testing.T) {
	route := renderRoute(t, authTestAgent("apikey"), compiler.AuthModeAPIKey,
		map[string]string{"assayd.dev/auth": "none"})
	if v, ok := route.Labels["assayd.dev/auth"]; ok {
		t.Errorf("a route published under apikey carries assayd.dev/auth=%q; the marker is "+
			"present only while the route is unauthenticated at the owner's request", v)
	}
	if route.Labels[LabelAgent] != "pricer" {
		t.Errorf("clearing the marker lost the route's labels: %v", route.Labels)
	}
}

// assertRouteGolden compares the rendered route with its golden file. TypeMeta
// is set and `status` dropped before marshalling: the builder sets neither, the
// client fills the first, and the operator never writes the second — so
// pinning either would move the golden on a Go-types upgrade that changed
// nothing the operator renders.
func assertRouteGolden(t *testing.T, file string, route *gatewayv1.HTTPRoute) {
	t.Helper()
	typed := route.DeepCopy()
	typed.APIVersion = gatewayv1.GroupVersion.String()
	typed.Kind = "HTTPRoute"
	obj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(typed)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	delete(obj, "status")
	unstructured.RemoveNestedField(obj, "metadata", "creationTimestamp")
	body, err := yaml.Marshal(obj)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := append([]byte(goldenHeader), body...)
	path := filepath.Join("testdata", file)
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (run with -update to create it)", path, err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("the rendered route differs from %s.\n--- rendered ---\n%s\n--- golden ---\n%s",
			path, got, want)
	}
}
