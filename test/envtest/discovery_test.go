package envtest

import (
	"context"
	"testing"
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

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

	if d.Installed(ctx) {
		t.Fatal("fixture: the EvalSuite CRD should not be installed yet")
	}

	crd := evalSuiteCRD()
	if err := k8s.Create(ctx, crd); err != nil {
		t.Fatalf("install CRD: %v", err)
	}
	t.Cleanup(func() { _ = k8s.Delete(context.Background(), crd) })

	// The detector must notice within its refresh window. A cached absent that
	// never expires would keep every agent promoting ungated after the gate
	// controller was installed.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if d.Installed(ctx) {
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
	if !d.Installed(context.Background()) {
		t.Error("an unreachable API server was read as 'no EvalSuite CRD', which promotes " +
			"every agent ungated. Holding is recoverable; ungated promotion is not.")
	}
}

func evalSuiteCRD() *apiextensionsv1.CustomResourceDefinition {
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
				Name: "v1alpha1", Served: true, Storage: true,
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

// A sanity check that the fixture above really is absent to start with, so the
// first test's precondition is meaningful rather than accidental.
func TestEvalSuiteCRDIsAbsentByDefault(t *testing.T) {
	var got apiextensionsv1.CustomResourceDefinition
	err := k8s.Get(context.Background(), types.NamespacedName{Name: "evalsuites.plume.dev"}, &got)
	if err == nil {
		t.Skip("another test installed it; ordering makes this check moot")
	}
	if !apierrors.IsNotFound(err) {
		t.Fatalf("unexpected error checking for the CRD: %v", err)
	}
}
