// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package envtest

import (
	"context"
	"strings"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/controller"
	"github.com/Quinyte/assayd/internal/revision"
)

// recordKeptByTheCRD asks the API server, by a dry-run status write, whether it
// would store a Service record on this Agent.
func recordKeptByTheCRD(t *testing.T, a *assaydv1alpha1.Agent) bool {
	t.Helper()
	probe := liveAgentPtr(t, a)
	probe.Status.RevisionServices = []assaydv1alpha1.RevisionServiceRecord{{
		Revision: "probe", RevisionDigest: "sha256:probe", UID: "probe"}}
	if err := k8s.Status().Update(context.Background(), probe, client.DryRunAll); err != nil {
		return false
	}
	return len(probe.Status.RevisionServices) == 1
}

// installAgentCRDWithoutTheRecord replaces the live Agent CRD with this
// release's, minus `status.revisionServices` — the CRD every upgraded install
// has, because `helm upgrade` never updates a chart's crds/ — and puts the
// release's back when the test ends.
func installAgentCRDWithoutTheRecord(t *testing.T, a *assaydv1alpha1.Agent) {
	t.Helper()
	ctx := context.Background()
	var installed apiextensionsv1.CustomResourceDefinition
	if err := k8s.Get(ctx, client.ObjectKey{Name: "agents.assayd.dev"}, &installed); err != nil {
		t.Fatalf("read the Agent CRD: %v", err)
	}
	current := installed.Spec.DeepCopy()
	stale := current.DeepCopy()
	for i := range stale.Versions {
		root := stale.Versions[i].Schema.OpenAPIV3Schema
		st := root.Properties["status"]
		delete(st.Properties, "revisionServices")
		root.Properties["status"] = st
	}
	write := func(spec *apiextensionsv1.CustomResourceDefinitionSpec) {
		if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			var c apiextensionsv1.CustomResourceDefinition
			if err := k8s.Get(ctx, client.ObjectKey{Name: "agents.assayd.dev"}, &c); err != nil {
				return err
			}
			c.Spec = *spec.DeepCopy()
			return k8s.Update(ctx, &c)
		}); err != nil {
			t.Fatalf("install the Agent CRD: %v", err)
		}
	}
	t.Cleanup(func() {
		write(current)
		eventually(t, "the release's Agent CRD to keep the record again", func() bool {
			return recordKeptByTheCRD(t, a)
		})
	})
	write(stale)
	eventually(t, "the stale Agent CRD to prune the record", func() bool {
		return !recordKeptByTheCRD(t, a)
	})
}

// An Agent CRD that predates `status.revisionServices` prunes every record
// without an error. Measured first, by round five of design 02 A77's review
// and again here before the fix: both status writes succeed and store nothing,
// every headless Service is then refused as RevisionServiceNotRecorded, and
// that reason's remedy — "delete that Service yourself, and the operator
// creates its own in its place and records it" — is false, because it will
// not record that one either. That is every upgraded install.
//
// The safe behaviour is kept: nothing is deleted without a record. What
// changes is the report, which names the stale CRD and the fix.
func TestAStaleCRDThatPrunesTheRecordIsReportedAndNothingIsDeleted(t *testing.T) {
	ns := newNamespace(t)
	a := noneAgent(t, ns, "stalecrd")
	installAgentCRDWithoutTheRecord(t, a)

	r := newGatewayReconciler("assayd-gateway", "assayd")
	rev := revision.MustHash(a.Spec)
	svcName := controller.WorkloadName("stalecrd", rev)
	settle(t, r, a)
	markAvailable(t, ns, svcName, 1)
	settle(t, r, a)
	key := types.NamespacedName{Namespace: runNS(ns), Name: svcName}
	before := liveService(t, key)
	if rec := recordFor(liveAgentPtr(t, a), rev); rec != "" {
		t.Fatalf("setup: the stale CRD kept a record (%s), so this is not the upgraded install", rec)
	}

	headlessByPatchAlone(t, key)
	for i := 0; i < 3; i++ {
		reconcileOnce(t, r, a)
	}

	if after := liveService(t, key); after.UID != before.UID {
		t.Fatalf("a Service was deleted with no record to authorise it (%s -> %s)", before.UID, after.UID)
	}
	got := liveAgentPtr(t, a)
	ready := condition(got, assaydv1alpha1.CondReady)
	if ready == nil || ready.Status != metav1.ConditionFalse || ready.Reason != controller.CondReasonServiceRecordNotKept {
		t.Fatalf("a record the CRD prunes is reported as %+v, want %s: RevisionServiceNotRecorded's "+
			"remedy is false here, because the operator will not record its replacement either",
			ready, controller.CondReasonServiceRecordNotKept)
	}
	if d := condition(got, assaydv1alpha1.CondDegraded); d == nil || d.Reason != controller.CondReasonServiceRecordNotKept {
		t.Errorf("Degraded is %+v", d)
	}
	for _, want := range []string{"status.revisionServices", "crds/", "helm upgrade"} {
		if !strings.Contains(ready.Message, want) {
			t.Errorf("the message does not name %q: %s", want, ready.Message)
		}
	}
}
