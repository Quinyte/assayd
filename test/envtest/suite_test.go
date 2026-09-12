// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

// Package envtest runs the platform's types and controllers against a real
// Kubernetes API server (etcd + kube-apiserver, no kubelet). It is the layer
// where defaulting, CEL validation, status subresources, and controller
// behaviour are actually true rather than merely declared in a marker.
//
// Everything here shares one control plane, so tests are namespace-scoped and
// must not mutate cluster-scoped state a sibling test reads.
package envtest

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/controller"
)

// gatewayAPICRDDir returns the standard Gateway API CRD directory inside the
// module cache, for the version go.mod pins. `go list` is asked rather than the
// module cache path being assembled by hand, so the answer cannot disagree with
// the version the binary is built from.
func gatewayAPICRDDir() (string, error) {
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "sigs.k8s.io/gateway-api").Output()
	if err != nil {
		return "", fmt.Errorf("go list sigs.k8s.io/gateway-api: %w", err)
	}
	dir := filepath.Join(strings.TrimSpace(string(out)), "config", "crd", "standard")
	if _, err := os.Stat(dir); err != nil {
		return "", fmt.Errorf("the pinned gateway-api module has no %s: %w", dir, err)
	}
	return dir, nil
}

// agentgatewayCRDDir extracts the AgentgatewayPolicy CRD from the agentgateway
// chart test/conformance vendors and pins by digest, into a directory envtest
// installs from. The same artifact rather than a second copy here, which could
// drift from the one whose contract the conformance suite reads.
//
// Only that CRD: it is the one agentgateway kind the operator touches (the
// slice's `<agent>-auth`), and with the gateway enabled the finalizer reads
// it, so a control plane without it could delete no Agent.
func agentgatewayCRDDir() (string, error) {
	raw, err := os.ReadFile(filepath.Join("..", "conformance", "testdata", "agentgateway-crds-1.4.1.tgz"))
	if err != nil {
		return "", fmt.Errorf("read the pinned agentgateway chart: %w", err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return "", fmt.Errorf("open the pinned agentgateway chart: %w", err)
	}
	const want = "agentgateway.dev_agentgatewaypolicies.yaml"
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return "", fmt.Errorf("the pinned agentgateway chart carries no %s", want)
		}
		if err != nil {
			return "", fmt.Errorf("read the pinned agentgateway chart: %w", err)
		}
		if path.Base(h.Name) != want {
			continue
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", want, err)
		}
		dir, err := os.MkdirTemp("", "assayd-envtest-agentgateway-")
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(dir, want), b, 0o600); err != nil {
			return "", err
		}
		return dir, nil
	}
}

var (
	// gatewayCRDs and agentgatewayCRDs are the directories the control plane
	// installed from, kept so that a test can start a second control plane
	// with one set and not the other.
	gatewayCRDs      string
	agentgatewayCRDs string

	cfg    *rest.Config
	k8s    client.Client
	scheme = runtime.NewScheme()
)

func TestMain(m *testing.M) {
	// A skipped envtest is an untested feature. Fail loudly rather than pass
	// vacuously when the control-plane binaries are absent.
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		fmt.Fprintln(os.Stderr,
			"envtest: KUBEBUILDER_ASSETS is unset — run `make envtest`, not `go test` directly.")
		os.Exit(1)
	}

	must(clientgoscheme.AddToScheme(scheme), "register core scheme")
	must(apiextensionsv1.AddToScheme(scheme), "register apiextensions scheme")
	must(assaydv1alpha1.AddToScheme(scheme), "register assayd scheme")
	must(gatewayv1.Install(scheme), "register the Gateway API scheme")

	// The Gateway API CRDs come from the PINNED module in go.mod, not from a
	// copy in this repository. A vendored copy would be a fixture: it could
	// drift from the version the operator compiles against and from the version
	// hack/e2e.sh installs, and a route this suite accepted would then be one a
	// real cluster rejects. ErrorIfCRDPathMissing below makes a bad path a
	// failure rather than a silently gateway-less control plane.
	gwCRDs, err := gatewayAPICRDDir()
	must(err, "locate the Gateway API CRDs")
	gatewayCRDs = gwCRDs
	agentgatewayCRDs, err = agentgatewayCRDDir()
	must(err, "extract the AgentgatewayPolicy CRD")

	env := &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "..", "config", "crd"),
			gwCRDs,
			agentgatewayCRDs,
		},
		ErrorIfCRDPathMissing: true,
	}

	cfg, err = env.Start()
	must(err, "start control plane")

	k8s, err = client.New(cfg, client.Options{Scheme: scheme})
	must(err, "build client")

	// The operator's own namespace: where run-namespace binding records live
	// (design 02 A60). One per control plane, shared by every test.
	must(client.IgnoreAlreadyExists(k8s.Create(context.Background(),
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: operatorNamespace}})),
		"create the operator namespace")

	code := m.Run()
	_ = os.RemoveAll(agentgatewayCRDs)

	if err := env.Stop(); err != nil {
		fmt.Fprintf(os.Stderr, "envtest: stop control plane: %v\n", err)
	}
	os.Exit(code)
}

func must(err error, what string) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "envtest: %s: %v\n", what, err)
		os.Exit(1)
	}
}

// operatorNamespace is the envtest stand-in for assayd-system.
const operatorNamespace = "assayd-system"

// runNS is the run namespace an Agent in ns gets its workload and material in
// (design 02 A42). Tests that look for either look there.
func runNS(ns string) string { return controller.RunNamespaceName(ns) }

// labelAuthorityPresent is the envtest stand-in for the admission-policy check:
// the chart is not installed here, so the reconciler is told the policies
// exist. TestRunNamespaceRefusesWithoutLabelAuthority uses the real adapter.
func labelAuthorityPresent(context.Context) (bool, error) { return true, nil }

// provisionRunNamespace creates the run namespace for ns exactly as the operator
// would — binding record first, then the namespace carrying the nonce, then the
// record bound to the namespace's UID (design 02 §3.2, checks 3 and 7) — so a test
// can plant objects in it BEFORE the reconciler's first pass. Planting into a
// namespace the operator has no record of would be the pre-creation attack, and
// the operator refuses it; this is the operator's own state, reproduced.
// Idempotent: a run namespace the operator already made is returned as is.
func provisionRunNamespace(t *testing.T, ns string) string {
	t.Helper()
	ctx := context.Background()
	name := runNS(ns)
	var existing corev1.ConfigMap
	if err := k8s.Get(ctx, types.NamespacedName{Namespace: operatorNamespace, Name: controller.BindingName(name)}, &existing); err == nil {
		return name
	}
	var source corev1.Namespace
	if err := k8s.Get(ctx, types.NamespacedName{Name: ns}, &source); err != nil {
		t.Fatalf("get namespace %s: %v", ns, err)
	}
	var operator corev1.Namespace
	if err := k8s.Get(ctx, types.NamespacedName{Name: operatorNamespace}, &operator); err != nil {
		t.Fatalf("get operator namespace: %v", err)
	}
	nonce := hex.EncodeToString(sha256.New().Sum([]byte(t.Name()))[:16])
	rec := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: controller.BindingName(name), Namespace: operatorNamespace,
			Labels: map[string]string{controller.LabelBinding: "true"}},
		Data: map[string]string{
			"schemaVersion": "1", "sourceNamespace": ns, "sourceNamespaceUID": string(source.UID),
			"runNamespace": name, "nonce": nonce, "state": "Creating",
		},
	}
	if err := k8s.Create(ctx, rec); err != nil {
		t.Fatalf("create binding record: %v", err)
	}
	run := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name,
		Labels: map[string]string{
			controller.LabelRunNamespace: "true", controller.LabelPodsBy: controller.PodsByAgentOperator,
			controller.LabelAgentNamespace: ns, controller.LabelOwnedBy: string(operator.UID)},
		Annotations: map[string]string{controller.AnnotationBindingNonce: nonce}}}
	if err := k8s.Create(ctx, run); err != nil {
		t.Fatalf("create run namespace: %v", err)
	}
	rec.Data["runNamespaceUID"] = string(run.UID)
	rec.Data["state"] = "Bound"
	if err := k8s.Update(ctx, rec); err != nil {
		t.Fatalf("bind: %v", err)
	}
	return name
}

// newNamespace gives each test its own namespace so the shared control plane
// never leaks state between tests.
func newNamespace(t *testing.T) string {
	t.Helper()
	name := nsName(t.Name())
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
	if err := k8s.Create(context.Background(), ns); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create namespace %s: %v", name, err)
	}
	return name
}

// nsName turns a Go test name into a DNS-1123 label.
//
// A hash suffix, not plain truncation: subtest names share long prefixes, and
// two that differ only past the cut produced the same namespace — which showed
// up as "already exists" from an unrelated test rather than as a collision.
func nsName(testName string) string {
	s := strings.ToLower(testName)
	s = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			return r
		default:
			return '-'
		}
	}, s)
	s = strings.Trim(s, "-")
	sum := sha256.Sum256([]byte(testName))
	suffix := hex.EncodeToString(sum[:])[:6]
	if len(s) > 48 {
		s = strings.Trim(s[:48], "-")
	}
	return "t-" + s + "-" + suffix
}
