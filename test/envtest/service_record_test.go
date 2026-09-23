// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package envtest

import (
	"context"
	"errors"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/controller"
	"github.com/Quinyte/assayd/internal/revision"
)

// Design 02 §3.2, A77: the delete of an unrepairable revision Service is
// authorised by the UID this operator RECORDED when it created that Service,
// and by nothing on the object.
//
// Round three of A77's review measured the stamp failing as that authority in
// both directions. Forgeable with `create`: a plant carrying a forged
// agent-uid label and a forged digest was deleted and replaced. Strippable with
// `patch`: two strategic-merge patches — strip the digest on the way to
// ExternalName, then back to ClusterIP headless — left the operator's OWN
// Service, same UID, refused as Unstamped on every pass. The human decided on
// 2026-09-23 that the record is the authority.

// recordFor returns the UID status records for rev, or "".
func recordFor(a *assaydv1alpha1.Agent, rev string) types.UID {
	for _, r := range a.Status.RevisionServices {
		if r.Revision == rev {
			return r.UID
		}
	}
	return ""
}

// forgetServiceRecords empties status.revisionServices through the status
// subresource: the state of an Agent created before the field existed, or one
// whose status write was lost after the create.
func forgetServiceRecords(t *testing.T, a *assaydv1alpha1.Agent) {
	t.Helper()
	live := liveAgentPtr(t, a)
	live.Status.RevisionServices = nil
	if err := k8s.Status().Update(context.Background(), live); err != nil {
		t.Fatalf("forget the service records: %v", err)
	}
}

// patchService applies strategic-merge patches in order, re-reading between
// them — `services/patch` and nothing else.
func patchService(t *testing.T, key types.NamespacedName, patches ...string) {
	t.Helper()
	for _, p := range patches {
		var svc corev1.Service
		if err := k8s.Get(context.Background(), key, &svc); err != nil {
			t.Fatalf("get for patch: %v", err)
		}
		if err := k8s.Patch(context.Background(), &svc,
			client.RawPatch(types.StrategicMergePatchType, []byte(p))); err != nil {
			t.Fatalf("patch %s: %v", p, err)
		}
	}
}

func liveService(t *testing.T, key types.NamespacedName) corev1.Service {
	t.Helper()
	var svc corev1.Service
	if err := k8s.Get(context.Background(), key, &svc); err != nil {
		t.Fatalf("get service %s: %v", key, err)
	}
	return svc
}

// The record is written by the create, with the UID the API server returned —
// on the pass that creates, and again when a Service this operator created is
// gone and it creates another. Adoption would eventually record a first
// Service anyway, so the second create is what pins the create path itself:
// adoption never overwrites a record, and a record left naming the first object
// would refuse any later replace of the second.
func TestTheOperatorRecordsTheServiceItCreates(t *testing.T) {
	ns := newNamespace(t)
	a := noneAgent(t, ns, "recorded")
	r := newGatewayReconciler("assayd-gateway", "assayd")
	rev := revision.MustHash(a.Spec)
	settle(t, r, a)

	key := types.NamespacedName{Namespace: runNS(ns), Name: controller.WorkloadName("recorded", rev)}
	first := liveService(t, key)
	got := liveAgentPtr(t, a)
	if rec := recordFor(got, rev); rec != first.UID {
		t.Fatalf("status.revisionServices records %q for %s, and the Service this operator created "+
			"is %s: nothing authorises replacing it", rec, rev, first.UID)
	}
	for _, e := range got.Status.RevisionServices {
		if e.Revision == rev && e.RevisionDigest != revision.MustDigest(a.Spec) {
			t.Errorf("the record carries digest %q, want the revision's %q: a 40-bit name "+
				"collision could otherwise borrow it", e.RevisionDigest, revision.MustDigest(a.Spec))
		}
	}

	if err := k8s.Delete(context.Background(), &first); err != nil {
		t.Fatalf("delete: %v", err)
	}
	reconcileOnce(t, r, a)
	second := liveService(t, key)
	if second.UID == first.UID {
		t.Fatal("setup: the Service was not recreated")
	}
	if rec := recordFor(liveAgentPtr(t, a), rev); rec != second.UID {
		t.Errorf("after recreating its Service the operator records %q; the object it created is %s",
			rec, second.UID)
	}
}

// A record written for ANOTHER projection of the same 40-bit revision name does
// not authorise a delete. The record is keyed by the digest as well, so a name
// collision cannot borrow it and skip the DigestMismatch refusal it exists for.
func TestARecordForAnotherDigestDoesNotAuthoriseTheDelete(t *testing.T) {
	ns := newNamespace(t)
	a := noneAgent(t, ns, "otherdig")
	r := newGatewayReconciler("assayd-gateway", "assayd")
	rev := revision.MustHash(a.Spec)
	svcName := controller.WorkloadName("otherdig", rev)
	settle(t, r, a)
	markAvailable(t, ns, svcName, 1)
	settle(t, r, a)

	key := types.NamespacedName{Namespace: runNS(ns), Name: svcName}
	before := liveService(t, key)
	live := liveAgentPtr(t, a)
	live.Status.RevisionServices = []assaydv1alpha1.RevisionServiceRecord{{
		Revision: rev, RevisionDigest: "sha256:" + strings.Repeat("0", 64), UID: before.UID,
	}}
	if err := k8s.Status().Update(context.Background(), live); err != nil {
		t.Fatalf("seed a record for another digest: %v", err)
	}
	headlessByPatchAlone(t, key)
	reconcileOnce(t, r, a)

	if after := liveService(t, key); after.UID != before.UID {
		t.Fatalf("a record for a DIFFERENT digest authorised the delete (%s -> %s)", before.UID, after.UID)
	}
}

// `services/patch` ALONE on the operator's own Service is repaired, whatever
// the patches did to its labels and annotations.
//
// The first two rows are round three's measurement and its label-side twin:
// the stamp and the agent-uid label are each strippable with the same verb
// that makes the object headless, and each used to refuse the object — as
// Unstamped, or as ForeignObject — on every pass.
func TestAPatchOnlyWedgeOnTheOperatorsOwnServiceIsReplaced(t *testing.T) {
	headless := `{"spec":{"type":"ClusterIP","externalName":null,"clusterIP":"None","clusterIPs":["None"]}}`
	for _, tc := range []struct {
		name  string
		first string
	}{
		{"the digest stripped on the way", `{"metadata":{"annotations":{"` + controller.RevisionDigestAnnotation +
			`":null}},"spec":{"type":"ExternalName","externalName":"elsewhere.example.com"}}`},
		{"the agent-uid label stripped on the way", `{"metadata":{"labels":{"` + controller.LabelAgentUID +
			`":null}},"spec":{"type":"ExternalName","externalName":"elsewhere.example.com"}}`},
		{"both stripped, a stranger's label added", `{"metadata":{"labels":{"` + controller.LabelAgentUID +
			`":"someone-else"},"annotations":{"` + controller.RevisionDigestAnnotation +
			`":null}},"spec":{"type":"ExternalName","externalName":"elsewhere.example.com"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ns := newNamespace(t)
			a := noneAgent(t, ns, "pwedge")
			r := newGatewayReconciler("assayd-gateway", "assayd")
			rev := revision.MustHash(a.Spec)
			svcName := controller.WorkloadName("pwedge", rev)
			settle(t, r, a)
			markAvailable(t, ns, svcName, 1)
			settle(t, r, a)

			key := types.NamespacedName{Namespace: runNS(ns), Name: svcName}
			before := liveService(t, key)
			patchService(t, key, tc.first, headless)
			broken := liveService(t, key)
			if broken.UID != before.UID || broken.Spec.ClusterIP != corev1.ClusterIPNone {
				t.Fatalf("setup: the patches did not leave the SAME object headless (uid %s -> %s, "+
					"clusterIP %q)", before.UID, broken.UID, broken.Spec.ClusterIP)
			}

			reconcileOnce(t, r, a)

			after := liveService(t, key)
			got := liveAgentPtr(t, a)
			ready := condition(got, assaydv1alpha1.CondReady)
			if after.UID == before.UID {
				t.Fatalf("the operator's own Service — the UID it recorded when it created it — "+
					"was not replaced after services/patch alone made it headless. That is the "+
					"per-Agent wedge the human's decision exists to remove. Ready=%+v", ready)
			}
			if after.Spec.ClusterIP == "" || after.Spec.ClusterIP == corev1.ClusterIPNone {
				t.Errorf("the replacement has no ClusterIP (%q)", after.Spec.ClusterIP)
			}
			if after.Labels[controller.LabelAgentUID] != string(a.UID) ||
				after.Annotations[controller.RevisionDigestAnnotation] != revision.MustDigest(a.Spec) {
				t.Errorf("the replacement does not carry the render's label and stamp: %v %v",
					after.Labels, after.Annotations)
			}
			// The record FOLLOWS the replace, in the same status write as the
			// bound. A record left naming the deleted object would refuse the
			// next replace of the object this operator just created.
			if rec := recordFor(got, rev); rec != after.UID {
				t.Errorf("after the replace status records %q, and the Service this operator just "+
					"created is %s", rec, after.UID)
			}
			if got.Status.ServiceReplacedRevision != rev || got.Status.ServiceReplacedAt == nil {
				t.Errorf("the replace is not bounded: %q %v", got.Status.ServiceReplacedRevision,
					got.Status.ServiceReplacedAt)
			}
			if ready != nil {
				switch ready.Reason {
				case "Unstamped", "ForeignObject", "RevisionHashCollision",
					controller.CondReasonServiceNotRecorded:
					t.Errorf("the operator's own Service is reported as not its own: %s: %s",
						ready.Reason, ready.Message)
				}
			}
			markAvailable(t, ns, svcName, 1)
			healed := settle(t, r, a)
			if c := condition(&healed, assaydv1alpha1.CondReady); c == nil || c.Status != metav1.ConditionTrue {
				t.Errorf("the Agent did not heal after its own Service was replaced: %+v", c)
			}
		})
	}
}

// A plant carrying a forged agent-uid label AND a forged digest, with a UID
// other than the recorded one, is refused and never deleted.
//
// Until round three this object WAS deleted and replaced, and design 02 §5 said
// so. The record reverses that: nothing on the object can make the operator
// destroy it, because nothing on the object can carry the UID of an object the
// operator created.
func TestAForgedStampOnAnotherUIDIsRefusedAndNeverDeleted(t *testing.T) {
	for _, tc := range []struct {
		name string
		// headlessFirst plants the object headless. Otherwise it is planted
		// addressable, converged — it passes provenance — and only then made
		// headless, which is what proves an existing record is not overwritten
		// by adoption.
		headlessFirst bool
	}{
		{"planted headless", true},
		{"planted addressable, converged, then made headless", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ns := newNamespace(t)
			a := noneAgent(t, ns, "forged")
			r := newGatewayReconciler("assayd-gateway", "assayd")
			rev := revision.MustHash(a.Spec)
			svcName := controller.WorkloadName("forged", rev)
			settle(t, r, a)
			markAvailable(t, ns, svcName, 1)
			settle(t, r, a)

			key := types.NamespacedName{Namespace: runNS(ns), Name: svcName}
			mine := liveService(t, key)
			recorded := recordFor(liveAgentPtr(t, a), rev)
			if recorded != mine.UID {
				t.Fatalf("setup: the operator's Service is not recorded (%q vs %s)", recorded, mine.UID)
			}
			if err := k8s.Delete(context.Background(), &mine); err != nil {
				t.Fatalf("delete: %v", err)
			}
			spec := corev1.ServiceSpec{
				Type:  corev1.ServiceTypeClusterIP,
				Ports: []corev1.ServicePort{{Name: "a2a", Port: 8080, Protocol: corev1.ProtocolTCP}},
			}
			if tc.headlessFirst {
				spec.ClusterIP = corev1.ClusterIPNone
			}
			planted := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: runNS(ns), Name: svcName,
					Labels:      map[string]string{controller.LabelAgentUID: string(a.UID)},
					Annotations: map[string]string{controller.RevisionDigestAnnotation: revision.MustDigest(a.Spec)},
				},
				Spec: spec,
			}
			if err := k8s.Create(context.Background(), planted); err != nil {
				t.Fatalf("plant: %v", err)
			}
			uid := planted.UID
			if !tc.headlessFirst {
				reconcileOnce(t, r, a)
				if rec := recordFor(liveAgentPtr(t, a), rev); rec != recorded {
					t.Fatalf("adopting the plant overwrote the record (%s -> %s): the operator "+
						"would now delete an object it did not create", recorded, rec)
				}
				headlessByPatchAlone(t, key)
			}
			for i := 0; i < 3; i++ {
				reconcileOnce(t, r, a)
			}

			after := liveService(t, key)
			if after.UID != uid {
				t.Fatalf("the forged plant was deleted and replaced (%s -> %s). Its labels and "+
					"annotations are forgeable with services/create; only the recorded UID %s "+
					"authorises a delete", uid, after.UID, recorded)
			}
			ready := condition(liveAgentPtr(t, a), assaydv1alpha1.CondReady)
			if ready == nil || ready.Reason != controller.CondReasonServiceNotRecorded {
				t.Fatalf("the refusal reads %+v, want %s", ready, controller.CondReasonServiceNotRecorded)
			}
			for _, want := range []string{string(recorded), string(uid), "cannot establish that it created it"} {
				if !strings.Contains(ready.Message, want) {
					t.Errorf("the message does not contain %q: %s", want, ready.Message)
				}
			}
		})
	}
}

// An Agent with NO record — created before the field existed — never has a
// headless Service deleted. It is refused, and says why.
func TestAServiceWithNoRecordIsNeverDeleted(t *testing.T) {
	ns := newNamespace(t)
	a := noneAgent(t, ns, "norecord")
	r := newGatewayReconciler("assayd-gateway", "assayd")
	rev := revision.MustHash(a.Spec)
	svcName := controller.WorkloadName("norecord", rev)
	settle(t, r, a)
	markAvailable(t, ns, svcName, 1)
	settle(t, r, a)

	key := types.NamespacedName{Namespace: runNS(ns), Name: svcName}
	before := liveService(t, key)
	forgetServiceRecords(t, a)
	headlessByPatchAlone(t, key)
	for i := 0; i < 3; i++ {
		reconcileOnce(t, r, a)
	}

	after := liveService(t, key)
	if after.UID != before.UID {
		t.Fatalf("a headless Service with no recorded UID was deleted (%s -> %s). Nothing "+
			"establishes that this operator created it", before.UID, after.UID)
	}
	got := liveAgentPtr(t, a)
	if rec := recordFor(got, rev); rec != "" {
		t.Errorf("a HEADLESS object was adopted into the record (%s), which would authorise its "+
			"delete on the next pass", rec)
	}
	ready := condition(got, assaydv1alpha1.CondReady)
	if ready == nil || ready.Reason != controller.CondReasonServiceNotRecorded {
		t.Fatalf("the refusal reads %+v, want %s", ready, controller.CondReasonServiceNotRecorded)
	}
	if !strings.Contains(ready.Message, "records no Service for this revision") {
		t.Errorf("the message does not say there is no record: %s", ready.Message)
	}

	// The refusal has TWO exits, not one. Deleting the object is the one the
	// message names; repairing it IN PLACE back to addressable — the two
	// patches in reverse — is the other: the next pass converges it and, since
	// there is no record, adopts it.
	patchService(t, key,
		`{"spec":{"type":"ExternalName","externalName":"elsewhere.example.com"}}`,
		`{"spec":{"type":"ClusterIP","externalName":null}}`)
	reconcileOnce(t, r, a)
	repaired := liveService(t, key)
	got = liveAgentPtr(t, a)
	if repaired.UID != before.UID {
		t.Fatalf("the in-place repair was replaced (%s -> %s)", before.UID, repaired.UID)
	}
	if c := condition(got, assaydv1alpha1.CondReady); c != nil && c.Reason == controller.CondReasonServiceNotRecorded {
		t.Errorf("an in-place repair back to addressable did not clear the refusal: %+v", c)
	}
	if rec := recordFor(got, rev); rec != repaired.UID {
		t.Errorf("the repaired object was not adopted into the empty record (%q, want %s)", rec, repaired.UID)
	}
}

// The MIGRATION: an addressable Service with no record, which passes
// provenance, is adopted into the record on the pass that converges it — so a
// later patch-only wedge on it heals like any other.
//
// Refusing to adopt would leave every Agent created before the record, and
// every Agent whose status write was lost after the create, permanently
// exposed to the wedge the record exists to heal.
func TestAnAddressableServiceWithNoRecordIsAdoptedIntoTheRecord(t *testing.T) {
	ns := newNamespace(t)
	a := noneAgent(t, ns, "adopted")
	r := newGatewayReconciler("assayd-gateway", "assayd")
	rev := revision.MustHash(a.Spec)
	svcName := controller.WorkloadName("adopted", rev)
	settle(t, r, a)
	markAvailable(t, ns, svcName, 1)
	settle(t, r, a)

	key := types.NamespacedName{Namespace: runNS(ns), Name: svcName}
	before := liveService(t, key)
	forgetServiceRecords(t, a)
	reconcileOnce(t, r, a)
	if rec := recordFor(liveAgentPtr(t, a), rev); rec != before.UID {
		t.Fatalf("the operator's own addressable Service was not adopted into the record (%q, "+
			"want %s): an Agent upgraded from before the record stays exposed to the wedge", rec, before.UID)
	}

	headlessByPatchAlone(t, key)
	reconcileOnce(t, r, a)
	if after := liveService(t, key); after.UID == before.UID {
		t.Fatalf("the adopted Service was not replaced once it went headless: %+v",
			condition(liveAgentPtr(t, a), assaydv1alpha1.CondReady))
	}
}

// conflictOnFinalStatusWrite answers Conflict to every Agent status write that
// changes anything BESIDES the Service record and the replace bound, for as
// long as it is armed — which is what the pass's final status write is, and
// what a concurrent edit to the Agent does to it.
type conflictOnFinalStatusWrite struct {
	client.Client
	refused int
}

func (c *conflictOnFinalStatusWrite) Status() client.SubResourceWriter {
	return &conflictOnFinalSW{SubResourceWriter: c.Client.Status(), c: c}
}

type conflictOnFinalSW struct {
	client.SubResourceWriter
	c *conflictOnFinalStatusWrite
}

func (w *conflictOnFinalSW) Update(ctx context.Context, obj client.Object,
	opts ...client.SubResourceUpdateOption) error {
	if a, ok := obj.(*assaydv1alpha1.Agent); ok {
		var live assaydv1alpha1.Agent
		if err := k8s.Get(ctx, client.ObjectKeyFromObject(a), &live); err == nil {
			strip := func(s assaydv1alpha1.AgentStatus) assaydv1alpha1.AgentStatus {
				s.RevisionServices, s.ServiceReplacedRevision, s.ServiceReplacedAt = nil, "", nil
				return s
			}
			if !equality.Semantic.DeepEqual(strip(a.Status), strip(live.Status)) {
				w.c.refused++
				return apierrors.NewConflict(schema.GroupResource{Group: "assayd.dev", Resource: "agents"},
					a.GetName(), errors.New("injected: the pass's final status write lost a race"))
			}
		}
	}
	return w.SubResourceWriter.Update(ctx, obj, opts...)
}

// The record survives a pass whose final status write fails.
//
// It was carried only by that write, so one Conflict after a replace left the
// operator's own replacement unrecorded, and the next headless state of it was
// refused as RevisionServiceNotRecorded until a human deleted it, with the
// route still published — round four of A77's review measured exactly that.
// The record is now written the moment the create returns, in a write of its
// own. Both creates are driven: the replace, and a plain create after the
// Service is gone.
func TestTheRecordSurvivesAConflictOnThePassesFinalStatusWrite(t *testing.T) {
	for _, tc := range []struct {
		name    string
		breakIt func(t *testing.T, key types.NamespacedName)
		replace bool
	}{
		{"the replace", headlessByPatchAlone, true},
		{"a create after the Service is gone", func(t *testing.T, key types.NamespacedName) {
			svc := liveService(t, key)
			if err := k8s.Delete(context.Background(), &svc); err != nil {
				t.Fatalf("delete: %v", err)
			}
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ns := newNamespace(t)
			a := noneAgent(t, ns, "conflicted")
			r := newGatewayReconciler("assayd-gateway", "assayd")
			rev := revision.MustHash(a.Spec)
			svcName := controller.WorkloadName("conflicted", rev)
			settle(t, r, a)
			markAvailable(t, ns, svcName, 1)
			settle(t, r, a)

			key := types.NamespacedName{Namespace: runNS(ns), Name: svcName}
			before := liveService(t, key)
			tc.breakIt(t, key)
			// Give the pass something besides the record to write, so its final
			// status write is not a no-op: the record now reaches the API server
			// before it, and a pass with nothing else to say would write nothing
			// for the Conflict to refuse.
			stale := liveAgentPtr(t, a)
			stale.Status.ObservedGeneration = 0
			if err := k8s.Status().Update(context.Background(), stale); err != nil {
				t.Fatalf("seed a stale observedGeneration: %v", err)
			}

			losing := &conflictOnFinalStatusWrite{Client: k8s}
			cr := newGatewayReconciler("assayd-gateway", "assayd")
			cr.Client = losing
			cr.Reader = k8s
			_, err := cr.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(a)})
			if losing.refused == 0 || err == nil {
				t.Fatalf("setup: the pass's final status write was not refused (refused=%d, err=%v)",
					losing.refused, err)
			}

			after := liveService(t, key)
			if after.UID == before.UID {
				t.Fatal("setup: the Service was not replaced or recreated")
			}
			got := liveAgentPtr(t, a)
			if rec := recordFor(got, rev); rec != after.UID {
				t.Fatalf("the pass's final status write failed and the record went with it: status "+
					"records %q, and the Service this operator just created is %s. The next headless "+
					"state of its own object would be refused as not its own", rec, after.UID)
			}
			if tc.replace && (got.Status.ServiceReplacedRevision != rev || got.Status.ServiceReplacedAt == nil) {
				t.Errorf("the replace bound was lost with the final write: %q %v",
					got.Status.ServiceReplacedRevision, got.Status.ServiceReplacedAt)
			}
		})
	}
}

// concurrentStatusWriter lets another writer update the Agent's status just
// before this operator's FIRST status write of the pass reaches the API server.
type concurrentStatusWriter struct {
	client.Client
	fired bool
}

func (c *concurrentStatusWriter) Status() client.SubResourceWriter {
	return &concurrentSW{SubResourceWriter: c.Client.Status(), c: c}
}

type concurrentSW struct {
	client.SubResourceWriter
	c *concurrentStatusWriter
}

func (w *concurrentSW) Update(ctx context.Context, obj client.Object,
	opts ...client.SubResourceUpdateOption) error {
	if a, ok := obj.(*assaydv1alpha1.Agent); ok && !w.c.fired {
		w.c.fired = true
		var live assaydv1alpha1.Agent
		if err := k8s.Get(ctx, client.ObjectKeyFromObject(a), &live); err != nil {
			return err
		}
		live.Status.Conditions = append(live.Status.Conditions, metav1.Condition{
			Type: "example.com/Concurrent", Status: metav1.ConditionTrue, Reason: "WrittenMeanwhile",
			Message: "written by another controller during the pass", LastTransitionTime: metav1.Now(),
		})
		if err := k8s.Status().Update(ctx, &live); err != nil {
			return err
		}
	}
	return w.SubResourceWriter.Update(ctx, obj, opts...)
}

// The early record write overwrites nothing written concurrently.
//
// Its first attempt carries the resourceVersion this pass read, so a write that
// landed in between makes it Conflict; the retry re-reads the Agent live and
// applies only the record onto it.
func TestTheEarlyRecordWriteDoesNotClobberAConcurrentStatusWrite(t *testing.T) {
	ns := newNamespace(t)
	a := noneAgent(t, ns, "concur")
	r := newGatewayReconciler("assayd-gateway", "assayd")
	rev := revision.MustHash(a.Spec)
	svcName := controller.WorkloadName("concur", rev)
	settle(t, r, a)
	markAvailable(t, ns, svcName, 1)
	settle(t, r, a)

	key := types.NamespacedName{Namespace: runNS(ns), Name: svcName}
	headlessByPatchAlone(t, key)
	racing := &concurrentStatusWriter{Client: k8s}
	cr := newGatewayReconciler("assayd-gateway", "assayd")
	cr.Client = racing
	cr.Reader = k8s
	if _, err := cr.Reconcile(context.Background(),
		ctrl.Request{NamespacedName: client.ObjectKeyFromObject(a)}); err != nil {
		t.Logf("reconcile: %v", err)
	}
	if !racing.fired {
		t.Fatal("setup: no status write was issued")
	}

	after := liveService(t, key)
	got := liveAgentPtr(t, a)
	if rec := recordFor(got, rev); rec != after.UID {
		t.Errorf("the record did not survive the race: %q, want %s", rec, after.UID)
	}
	// And the replace BOUND survives the retry with the record: without it the
	// cooldown is lost, and a second headless patch gets an immediate second
	// replace — a delete and a traffic gap per patch. Round five's mutation T4,
	// which dropped the bound from the retry, survived until this assertion.
	if got.Status.ServiceReplacedRevision != rev || got.Status.ServiceReplacedAt == nil {
		t.Errorf("the retry kept the record and lost the replace bound: %q %v",
			got.Status.ServiceReplacedRevision, got.Status.ServiceReplacedAt)
	}
	if condition(got, "example.com/Concurrent") == nil {
		t.Errorf("the early record write overwrote a status field another writer set during the "+
			"pass: %+v", got.Status.Conditions)
	}
}

// recreateAgentOnFirstStatusWrite deletes the Agent and creates another under
// its name just before this operator's first status write of the pass lands.
type recreateAgentOnFirstStatusWrite struct {
	client.Client
	t     *testing.T
	fired bool
	fresh *assaydv1alpha1.Agent
}

func (c *recreateAgentOnFirstStatusWrite) Status() client.SubResourceWriter {
	return &recreateSW{SubResourceWriter: c.Client.Status(), c: c}
}

type recreateSW struct {
	client.SubResourceWriter
	c *recreateAgentOnFirstStatusWrite
}

func (w *recreateSW) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	if a, ok := obj.(*assaydv1alpha1.Agent); ok && !w.c.fired {
		w.c.fired = true
		var live assaydv1alpha1.Agent
		if err := k8s.Get(ctx, client.ObjectKeyFromObject(a), &live); err != nil {
			w.c.t.Fatalf("recreate: get: %v", err)
		}
		live.Finalizers = nil
		if err := k8s.Update(ctx, &live); err != nil {
			w.c.t.Fatalf("recreate: drop finalizer: %v", err)
		}
		if err := k8s.Delete(ctx, &live); err != nil {
			w.c.t.Fatalf("recreate: delete: %v", err)
		}
		eventually(w.c.t, "the old Agent to go", func() bool {
			var gone assaydv1alpha1.Agent
			return apierrors.IsNotFound(k8s.Get(ctx, client.ObjectKeyFromObject(a), &gone))
		})
		fresh := &assaydv1alpha1.Agent{ObjectMeta: metav1.ObjectMeta{Namespace: a.Namespace, Name: a.Name},
			Spec: *live.Spec.DeepCopy()}
		if err := k8s.Create(ctx, fresh); err != nil {
			w.c.t.Fatalf("recreate: create: %v", err)
		}
		w.c.fresh = fresh
	}
	return w.SubResourceWriter.Update(ctx, obj, opts...)
}

// The early write's retry does not put a record on a DIFFERENT Agent that took
// the name while the pass ran. A record authorises a delete, and it belongs to
// the Agent whose Service it names.
func TestTheEarlyRecordWriteDoesNotRecordOntoARecreatedAgent(t *testing.T) {
	ns := newNamespace(t)
	a := noneAgent(t, ns, "recreated")
	r := newGatewayReconciler("assayd-gateway", "assayd")
	rev := revision.MustHash(a.Spec)
	svcName := controller.WorkloadName("recreated", rev)
	settle(t, r, a)
	markAvailable(t, ns, svcName, 1)
	settle(t, r, a)

	key := types.NamespacedName{Namespace: runNS(ns), Name: svcName}
	headlessByPatchAlone(t, key)
	racing := &recreateAgentOnFirstStatusWrite{Client: k8s, t: t}
	cr := newGatewayReconciler("assayd-gateway", "assayd")
	cr.Client = racing
	cr.Reader = k8s
	if _, err := cr.Reconcile(context.Background(),
		ctrl.Request{NamespacedName: client.ObjectKeyFromObject(a)}); err != nil {
		t.Logf("reconcile: %v", err)
	}
	if racing.fresh == nil {
		t.Fatal("setup: the Agent was not recreated during the pass")
	}
	got := liveAgentPtr(t, racing.fresh)
	if got.UID == a.UID {
		t.Fatal("setup: the Agent under the name is the same one")
	}
	if len(got.Status.RevisionServices) != 0 || got.Status.ServiceReplacedAt != nil {
		t.Errorf("the pass wrote the old Agent's Service record onto the Agent recreated under its "+
			"name: %+v, replacedAt %v", got.Status.RevisionServices, got.Status.ServiceReplacedAt)
	}
}
