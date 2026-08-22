// Package envtest runs the platform's types and controllers against a real
// Kubernetes API server (etcd + kube-apiserver, no kubelet). It is the layer
// where defaulting, CEL validation, status subresources, and controller
// behaviour are actually true rather than merely declared in a marker.
//
// Everything here shares one control plane, so tests are namespace-scoped and
// must not mutate cluster-scoped state a sibling test reads.
package envtest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	plumev1alpha1 "github.com/ejs-5/plume/api/v1alpha1"
)

var (
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
	must(plumev1alpha1.AddToScheme(scheme), "register plume scheme")

	env := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd")},
		ErrorIfCRDPathMissing: true,
	}

	var err error
	cfg, err = env.Start()
	must(err, "start control plane")

	k8s, err = client.New(cfg, client.Options{Scheme: scheme})
	must(err, "build client")

	code := m.Run()

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
