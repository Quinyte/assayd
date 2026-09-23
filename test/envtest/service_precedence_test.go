// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package envtest

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/controller"
	"github.com/Quinyte/assayd/internal/revision"
)

// Which reason `Ready` carries when the active revision's Service has no
// ClusterIP AND design 03 A81's judgement finds the serving route refused on
// the same pass.
//
// The unaddressable report sets `Ready=False` before the gateway step, and
// withholdReady never overrides a `Ready` that is already False, so `Ready`
// reads this amendment's reason while `PolicyApplyIncomplete` keeps A81's.
// Round three of A77's review reasoned this from the code and did not measure
// it; this measures it, and pins it, so a change to the precedence has to come
// through design 02 §5's owner-edit row rather than arrive unrecorded.
func TestAnUnaddressableActiveServiceAndARefusedRouteOnOnePass(t *testing.T) {
	ns := newNamespace(t)
	a := noneAgent(t, ns, "prec")
	r := newGatewayReconciler("assayd-gateway", "assayd")
	rev := revision.MustHash(a.Spec)
	svcName := controller.WorkloadName("prec", rev)
	settle(t, r, a)
	markAvailable(t, ns, svcName, 1)
	settle(t, r, a)
	acceptRoute(t, ns, "prec")
	settle(t, r, a)

	key := types.NamespacedName{Namespace: runNS(ns), Name: svcName}
	headlessByPatchAlone(t, key)
	live := liveAgentPtr(t, a)
	live.Spec.Runtime.Image = "ghcr.io/acme/agent@sha256:" + strings.Repeat("c", 64)
	if err := k8s.Update(context.Background(), live); err != nil {
		t.Fatalf("edit the spec: %v", err)
	}
	for i := 0; i < 3; i++ {
		reconcileOnce(t, r, live)
	}
	refuseRoute(t, ns, "prec")
	reconcileOnce(t, r, live)

	got := liveAgentPtr(t, a)
	if got.Status.ActiveRevision != rev {
		t.Fatalf("setup: the edit promoted (%q), so the active revision is not the broken one",
			got.Status.ActiveRevision)
	}
	ready := condition(got, assaydv1alpha1.CondReady)
	inc := condition(got, assaydv1alpha1.CondPolicyApplyIncomplete)
	t.Logf("measured: Ready=%v/%v PolicyApplyIncomplete=%v Degraded=%v phase=%s",
		ready.Status, ready.Reason, inc, condition(got, assaydv1alpha1.CondDegraded), got.Status.Phase)
	if inc == nil || inc.Status != metav1.ConditionTrue || inc.Reason != controller.ReasonServingRouteNotAccepted {
		t.Fatalf("setup: A81's route judgement did not fire on this pass: %+v", inc)
	}
	if ready.Status != metav1.ConditionFalse || ready.Reason != controller.CondReasonActiveServiceUnaddressable {
		t.Errorf("Ready reads %v/%v. Design 02 §5 records that the unaddressable report, set "+
			"first, holds Ready over A81's ServingRouteNotAccepted; if that changed, change the "+
			"record", ready.Status, ready.Reason)
	}
}
