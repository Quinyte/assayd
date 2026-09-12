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

// emittedRoute renders the serving route exactly as the emitter does: the
// publication is chosen by routePublicationFor from status.auth, and nothing
// here supplies a mode.
func emittedRoute(t *testing.T, agent *assaydv1alpha1.Agent, auth *assaydv1alpha1.AuthStatus) *gatewayv1.HTTPRoute {
	t.Helper()
	r := &AgentReconciler{Gateway: GatewayConfig{
		Enabled: true, Name: "assayd", Namespace: "assayd-gateway",
		HostnameSuffix: DefaultGatewayHostnameSuffix,
	}}
	name, err := compiler.ServingRouteName(agent.Name)
	if err != nil {
		t.Fatalf("name: %v", err)
	}
	return r.servingRouteFor(agent, RunNamespaceName(agent.Namespace), "abc1234567", name, 8080,
		routePublicationFor(auth))
}

// §8.1's unit golden of the EMITTED `auth: none` route (design 03 §3.3.1): it
// carries `assayd.dev/auth: "none"` and names its backend, and no policy
// exists for it. The mode comes from status.auth, where the `Create` recorded
// it, so this proves the emitter chooses the marker: at `Publishing`, before
// status.auth records the mode, and at `Served`, once it does. PR 2's golden
// of the same route had the test pass the mode in.
func TestGoldenTheEmittedAuthNoneRoute(t *testing.T) {
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
	for _, tc := range []struct {
		stage string
		auth  *assaydv1alpha1.AuthStatus
	}{
		{"Publishing", &assaydv1alpha1.AuthStatus{Transaction: &assaydv1alpha1.AuthTransaction{
			Kind: TxCreate, TargetMode: "none", Stage: StagePublishing}}},
		{"Served", &assaydv1alpha1.AuthStatus{Mode: "none"}},
	} {
		t.Run(tc.stage, func(t *testing.T) {
			route := emittedRoute(t, agent, tc.auth)
			if got := route.Labels["assayd.dev/auth"]; got != "none" {
				t.Errorf("the emitted route's assayd.dev/auth label is %q; §3.3.1's marker is exactly "+
					"\"none\" while the route is published unauthenticated at the owner's request", got)
			}
			assertRouteGolden(t, "pricer-serving-auth-none.golden.yaml", route)
		})
	}
}

// The route a `Create` prepares: parentRefs, no backendRefs, and no marker,
// because nothing is published yet (§3.3). Golden, so the shape the probe's
// `500` was measured on cannot drift silently.
func TestGoldenThePreparedRoute(t *testing.T) {
	route := emittedRoute(t, authTestAgent("apikey"), &assaydv1alpha1.AuthStatus{
		Transaction: &assaydv1alpha1.AuthTransaction{Kind: TxCreate, TargetMode: "apikey", Stage: StageProbingAfter}})
	if routePublished(route) {
		t.Errorf("a Create short of Publishing rendered a route with backendRefs: %+v", route.Spec.Rules)
	}
	if v, ok := route.Labels["assayd.dev/auth"]; ok {
		t.Errorf("the prepared route carries assayd.dev/auth=%q; nothing is published yet", v)
	}
	assertRouteGolden(t, "pricer-serving-prepared.golden.yaml", route)
}

// A marker planted on a route published under apikey is drift: equalRoute
// reports it, so the next reconcile writes the route and the marker goes.
// equalRoute used to check only that the desired labels were present, and a
// planted marker survived every reconcile in which nothing else drifted
// (design 03 A69).
func TestAPlantedMarkerIsDriftOnAnAPIKeyRoute(t *testing.T) {
	desired := emittedRoute(t, authTestAgent("apikey"), &assaydv1alpha1.AuthStatus{Mode: "apikey"})
	if v, ok := desired.Labels["assayd.dev/auth"]; ok {
		t.Fatalf("an apikey route was rendered with assayd.dev/auth=%q", v)
	}
	planted := desired.DeepCopy()
	planted.Labels["assayd.dev/auth"] = "none"
	if equalRoute(planted, desired) {
		t.Error("a route that should carry no marker, carrying `assayd.dev/auth: \"none\"`, compares " +
			"equal to what the emitter renders, so it is never rewritten: the route says it is " +
			"deliberately unauthenticated while it is authenticated")
	}
	if !equalRoute(desired.DeepCopy(), desired) {
		t.Error("the rendered route does not compare equal to itself, so every reconcile would write it")
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
