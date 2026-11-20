// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package envtest

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/controller"
)

// Direct reconcile calls are synchronous. Record the relevant uncached Gets
// while letting the real API server answer (except the injected schema error).
type a20SuiteReads struct {
	client.Reader
	gets        []string
	schemaErr   error
	missingEval bool
}

func (r *a20SuiteReads) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	switch typed := obj.(type) {
	case *apiextensionsv1.CustomResourceDefinition:
		if key.Name == "agents.assayd.dev" {
			r.gets = append(r.gets, "Agent CRD")
			if r.schemaErr != nil {
				return r.schemaErr
			}
			if r.missingEval {
				if err := r.Reader.Get(ctx, key, obj, opts...); err != nil {
					return err
				}
				copy := typed.DeepCopy()
				for i := range copy.Spec.Versions {
					v := &copy.Spec.Versions[i]
					if v.Name == assaydv1alpha1.GroupVersion.Version && v.Schema != nil && v.Schema.OpenAPIV3Schema != nil {
						delete(v.Schema.OpenAPIV3Schema.Properties["status"].Properties, "eval")
					}
				}
				*typed = *copy
				return nil
			}
		}
	case *assaydv1alpha1.EvalSuite:
		r.gets = append(r.gets, "EvalSuite "+key.String())
	}
	return r.Reader.Get(ctx, key, obj, opts...)
}

func (r *a20SuiteReads) assert(t *testing.T, want ...string) {
	t.Helper()
	if !slices.Equal(r.gets, want) {
		t.Errorf("uncached gate reads = %v, want %v", r.gets, want)
	}
	r.gets = nil
}

func a20EvalReader(t *testing.T, stub *a2aStub, gateway controller.GatewayConfig, reader client.Reader) *controller.EvalReconciler {
	t.Helper()
	e, err := controller.NewEvalReconciler(k8s, reader, func() bool { return true }, gateway)
	if err != nil {
		t.Fatal(err)
	}
	e.A2A.HTTP = stub
	e.CardFetchTimeout = time.Second
	return e
}

// A20.27: the ordinary promotion gate reads neither object while the desired
// revision is already serving. A candidate reads its suite only after the
// Agent schema check succeeds. Use fresh reconcilers so a cached schema
// answer cannot hide the read on the serving or schema-failure pass.
func TestAgentPromotionGateReadsSchemaBeforeSuiteOnlyForCandidate(t *testing.T) {
	ns := newNamespace(t)
	mustCreateSuite(t, ns, "smoke")
	a, _ := servedUngated(t, ns, "a20gate-reads")
	active := liveAgent(t, a).Status.ActiveRevisionDigest
	r := newReconciler(true)
	reads := &a20SuiteReads{Reader: k8s}
	r.Reader = reads
	mustEdit(t, a, func(a *assaydv1alpha1.Agent) {
		a.Spec.Gates = []assaydv1alpha1.GateRef{{EvalSuiteRef: "smoke"}}
	})
	reconcileOnce(t, r, a)
	reads.assert(t)
	condIs(t, a, assaydv1alpha1.CondGatesPassed, metav1.ConditionFalse, controller.ReasonActiveRevisionNotEvaluated)
	if got := liveAgent(t, a).Status.ActiveRevisionDigest; got != active {
		t.Fatalf("setup: serving digest changed from %s to %s", active, got)
	}

	// Build the candidate with the same base fixture, but use a cold schema
	// checker for each discriminating read so the call order is observable.
	gatedCandidate(t, r, a, imageB, "smoke")
	reads.gets = nil
	r = newReconciler(true)
	r.Reader = reads
	reads.schemaErr = errors.New("test: Agent CRD read unavailable")
	reconcileOnce(t, r, a)
	reads.assert(t, "Agent CRD")
	condIs(t, a, assaydv1alpha1.CondGatesPassed, metav1.ConditionFalse, controller.ReasonEvalRecordUnverifiable)

	reads.schemaErr = nil
	reads.missingEval = true
	reconcileOnce(t, r, a)
	reads.assert(t, "Agent CRD")
	condIs(t, a, assaydv1alpha1.CondGatesPassed, metav1.ConditionFalse, controller.ReasonEvalRecordNotKept)

	// An incompatible answer is cached, unlike the read error above.
	r = newReconciler(true)
	r.Reader = reads
	reads.missingEval = false
	reconcileOnce(t, r, a)
	reads.assert(t, "Agent CRD", "EvalSuite "+ns+"/smoke")
	condIs(t, a, assaydv1alpha1.CondGatesPassed, metav1.ConditionFalse, controller.ReasonEvalAwaitingCard)
}
