// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package envtest

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
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
