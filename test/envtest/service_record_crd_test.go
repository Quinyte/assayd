// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package envtest

import (
	"context"
	"strings"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
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

// dryRunStatusFails refuses every DRY-RUN status write of an Agent, and passes
// every real one through.
type dryRunStatusFails struct{ client.Client }

func (c *dryRunStatusFails) Status() client.SubResourceWriter {
	return &dryRunStatusFailsSW{SubResourceWriter: c.Client.Status()}
}

type dryRunStatusFailsSW struct{ client.SubResourceWriter }

func (w *dryRunStatusFailsSW) Update(ctx context.Context, obj client.Object,
	opts ...client.SubResourceUpdateOption) error {
	o := &client.SubResourceUpdateOptions{}
	o.ApplyOptions(opts)
	if _, ok := obj.(*assaydv1alpha1.Agent); ok && len(o.DryRun) > 0 {
		return apierrors.NewServiceUnavailable("injected: the dry-run was not answered")
	}
	return w.SubResourceWriter.Update(ctx, obj, opts...)
}

// A dry run that ERRORS establishes nothing about the CRD, and must not be
// reported as a stale one.
//
// The keepability check answers "cannot keep a record" only when the API
// server's dry-run answer shows the record pruned. Treating an error as that
// answer would send a reader on a CRD that is current to re-apply CRDs, for a
// timeout — rule 8. Round six's mutation V07 did exactly that and survived.
func TestADryRunThatErrorsIsNotReportedAsAStaleCRD(t *testing.T) {
	ns := newNamespace(t)
	a := noneAgent(t, ns, "dryerr")
	r := newGatewayReconciler("assayd-gateway", "assayd")
	rev := revision.MustHash(a.Spec)
	svcName := controller.WorkloadName("dryerr", rev)
	settle(t, r, a)
	markAvailable(t, ns, svcName, 1)
	settle(t, r, a)

	key := types.NamespacedName{Namespace: runNS(ns), Name: svcName}
	before := liveService(t, key)
	forgetServiceRecords(t, a)
	headlessByPatchAlone(t, key)

	failing := newGatewayReconciler("assayd-gateway", "assayd")
	failing.Client = &dryRunStatusFails{Client: k8s}
	failing.Reader = k8s
	for i := 0; i < 2; i++ {
		if _, err := failing.Reconcile(context.Background(),
			ctrl.Request{NamespacedName: client.ObjectKeyFromObject(a)}); err != nil {
			t.Logf("reconcile: %v", err)
		}
	}

	if after := liveService(t, key); after.UID != before.UID {
		t.Fatalf("an unrecorded headless Service was deleted (%s -> %s)", before.UID, after.UID)
	}
	ready := condition(liveAgentPtr(t, a), assaydv1alpha1.CondReady)
	if ready == nil || ready.Reason != controller.CondReasonServiceNotRecorded {
		t.Fatalf("a dry run that errored is reported as %+v, want %s: nothing established that "+
			"the CRD prunes the record", ready, controller.CondReasonServiceNotRecorded)
	}
	if strings.Contains(ready.Message, "crds/") {
		t.Errorf("the message sends the reader to re-apply CRDs on the strength of an error: %s",
			ready.Message)
	}
}
