package envtest

import (
	"context"
	"testing"
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/ejs-5/plume/internal/controller"
)

// Whether the EvalSuite CRD is installed decides whether rollouts are eval-gated
// (ADR-0006). B1 was that this arrived as a struct field an operator author had
// to remember; it must be DISCOVERED from the cluster instead.
//
// The hard part is staleness. A cached "absent" that survives the CRD being
// installed means agents keep promoting ungated after an operator installed the
// thing whose whole purpose is to gate them — the fail-open direction, and
// exactly the class B1 was.

func TestEvalSuiteDetectorSeesTheCRDAppear(t *testing.T) {
	ctx := context.Background()
	d, err := controller.NewEvalSuiteDetector(cfg, 100*time.Millisecond)
	if err != nil {
		t.Fatalf("build detector: %v", err)
	}

	// A CRD is cluster-scoped, so this test must establish its own precondition
	// rather than assume one. Asserting "not installed" without ensuring it made
	// the test order-dependent and non-repeatable: `go test -count=2` failed.
	ensureEvalSuiteAbsent(t)
	if d.Installed() {
		t.Fatal("the detector reports installed after the CRD was removed")
	}

	crd := evalSuiteCRD("v1alpha1")
	if err := k8s.Create(ctx, crd); err != nil {
		t.Fatalf("install CRD: %v", err)
	}
	t.Cleanup(func() { ensureEvalSuiteAbsent(t) })

	// The detector must notice within its refresh window. A cached absent that
	// never expires would keep every agent promoting ungated after the gate
	// controller was installed.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if d.Installed() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Error("the detector never saw the EvalSuite CRD appear: a stale 'absent' keeps " +
		"agents promoting ungated after the gate controller is installed")
}

// The safe direction on error is "installed", which holds. An operator that
// cannot reach discovery must not conclude that nothing needs gating.
func TestEvalSuiteDetectorHoldsWhenDiscoveryFails(t *testing.T) {
	broken := *cfg
	broken.Host = "https://127.0.0.1:1" // nothing listening
	broken.Timeout = time.Second

	d, err := controller.NewEvalSuiteDetector(&broken, time.Millisecond)
	if err != nil {
		t.Fatalf("build detector: %v", err)
	}
	if !d.Installed() {
		t.Error("an unreachable API server was read as 'no EvalSuite CRD', which promotes " +
			"every agent ungated. Holding is recoverable; ungated promotion is not.")
	}
}

func evalSuiteCRD(version string) *apiextensionsv1.CustomResourceDefinition {
	preserve := true
	return &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: "evalsuites.plume.dev"},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: "plume.dev",
			Names: apiextensionsv1.CustomResourceDefinitionNames{
				Plural: "evalsuites", Singular: "evalsuite", Kind: "EvalSuite", ListKind: "EvalSuiteList",
			},
			Scope: apiextensionsv1.NamespaceScoped,
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{{
				Name: version, Served: true, Storage: true,
				Schema: &apiextensionsv1.CustomResourceValidation{
					OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
						Type:                   "object",
						XPreserveUnknownFields: &preserve,
					},
				},
			}},
		},
	}
}

// B3 — detection asked for a GROUP-VERSION, so it was pinned to v1alpha1. When
// EvalSuite ships at v1beta1 the call still succeeds (Agent lives in v1alpha1),
// EvalSuite is simply absent from that version's list, and the detector returns
// a confident, cached "not installed". Every agent with declared gates then
// promotes ungated while the condition asserts EvalSuiteCRDAbsent.
//
// v1alpha1 -> v1beta1 is not hypothetical for an API in this repo; design 16
// pins no version.
func TestEvalSuiteDetectorFindsTheCRDAtAnyVersion(t *testing.T) {
	ctx := context.Background()
	ensureEvalSuiteAbsent(t)
	// Served at v1beta1 only — the shape this API will actually take.
	crd := evalSuiteCRD("v1beta1")
	if err := k8s.Create(ctx, crd); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("install CRD: %v", err)
	}
	t.Cleanup(func() { ensureEvalSuiteAbsent(t) })

	d, err := controller.NewEvalSuiteDetector(cfg, 50*time.Millisecond)
	if err != nil {
		t.Fatalf("build detector: %v", err)
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if d.Installed() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Error("the detector did not find EvalSuite served at v1beta1. Version-pinned " +
		"detection fails OPEN on an API bump: every gated agent promotes ungated while " +
		"the condition claims the CRD is absent.")
}

// ensureEvalSuiteAbsent deletes the EvalSuite CRD and waits for discovery to
// stop reporting it. Deletion is asynchronous and discovery is cached by the
// API server, so returning as soon as Delete succeeds would leave the next test
// racing a CRD that is still being torn down.
func ensureEvalSuiteAbsent(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	crd := &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: "evalsuites.plume.dev"},
	}
	if err := k8s.Delete(ctx, crd); err != nil && !apierrors.IsNotFound(err) {
		t.Fatalf("delete EvalSuite CRD: %v", err)
	}
	probe, err := controller.NewEvalSuiteDetector(cfg, time.Nanosecond)
	if err != nil {
		t.Fatalf("build probe detector: %v", err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if !probe.Installed() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("the EvalSuite CRD was still discoverable 30s after deletion")
}
