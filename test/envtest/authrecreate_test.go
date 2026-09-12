// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package envtest

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/compiler"
	"github.com/Quinyte/assayd/internal/controller"
	"github.com/Quinyte/assayd/internal/revision"
)

// Design 03 §3.3.3's re-creations for a served API-key Agent, and the
// invariant they keep: backendRefs are never written onto its route unless
// `<agent>-auth` is present, intact, converged, and a 401 was attributed.

func deleteRoute(t *testing.T, a *assaydv1alpha1.Agent) {
	t.Helper()
	if err := k8s.Delete(context.Background(), servingRoute(t, a.Namespace, a.Name)); err != nil {
		t.Fatalf("delete the route: %v", err)
	}
}

func deletePolicy(t *testing.T, a *assaydv1alpha1.Agent) {
	t.Helper()
	name, _ := compiler.AuthPolicyName(a.Name)
	if err := k8s.Delete(context.Background(), policyExists(t, runNS(a.Namespace), name)); err != nil {
		t.Fatalf("delete the policy: %v", err)
	}
}

// assertNeverOpen is the invariant, read after a pass.
func assertNeverOpen(t *testing.T, a *assaydv1alpha1.Agent) {
	t.Helper()
	name, _ := compiler.AuthPolicyName(a.Name)
	if routePublished(t, a.Namespace, a.Name) && policyExists(t, runNS(a.Namespace), name) == nil {
		t.Fatal("the route carries backendRefs and <agent>-auth does not exist: an API-key Agent's " +
			"route published with no policy")
	}
}

// recordCard writes the card digest the operator's own fetch would record for
// the serving revision, which is what lets a 401 on a PUBLISHED route be
// attributed to something in front of the backend (§3.3.3).
func recordCard(t *testing.T, a *assaydv1alpha1.Agent) {
	t.Helper()
	live := liveAgent(t, a)
	now := metav1.Now()
	live.Status.Cards = []assaydv1alpha1.CardStatus{{Revision: live.Status.ActiveRevision,
		RevisionDigest: live.Status.ActiveRevisionDigest, Name: a.Name, Digest: "cafe", FetchedAt: &now}}
	if err := k8s.Status().Update(context.Background(), live); err != nil {
		t.Fatalf("record the card: %v", err)
	}
}

// §8.1 case 8's core: a served Agent's deleted route comes back PREPARED, with
// the switch held, and is published only after the probe's 401.
func TestADeletedRouteIsPreparedAgainAndPublishedOnlyOnA401(t *testing.T) {
	a, r, stub := servedAPIKeyAgent(t, "reroute")
	stub.hold("reroute", true)
	deleteRoute(t, a)
	reconcileOnce(t, r, a)
	assertNeverOpen(t, a)
	rt := servingRoute(t, a.Namespace, "reroute")
	if rt == nil {
		t.Fatal("the deleted route was not re-created")
	}
	if routePublished(t, a.Namespace, "reroute") {
		t.Fatalf("the first version of the re-created route carries backendRefs: %+v", rt.Spec.Rules)
	}
	tx := txOf(t, a)
	if tx == nil || tx.Kind != "Create" || tx.TargetMode != "apikey" || tx.TargetDigest != authOf(t, a).AppliedDigest {
		t.Fatalf("the re-create is not a Create whose target is status.auth's: %+v", tx)
	}
	acceptRoute(t, a.Namespace, "reroute")
	reconcileOnce(t, r, a)
	if routePublished(t, a.Namespace, "reroute") {
		t.Fatal("the re-created route was published while the oracle refused nothing")
	}
	stub.hold("reroute", false)
	reconcileOnce(t, r, a)
	if !routePublished(t, a.Namespace, "reroute") {
		t.Fatalf("the oracle answered 401 and the re-created route was not published: %+v", txOf(t, a))
	}
	acceptRoute(t, a.Namespace, "reroute")
	reconcileOnce(t, r, a)
	if auth := authOf(t, a); auth.Mode != "apikey" || auth.Transaction != nil {
		t.Errorf("the re-create did not reach Served: %+v", auth)
	}
}

// A served policy deleted out of band is re-created from status.auth by a
// Lock, and trusted only once an anonymous request gets an attributed 401. The
// route is not withdrawn meanwhile, and AuthPolicyMissing stays raised.
func TestADeletedPolicyIsRecreatedAndTrustedOnlyOnAnAttributed401(t *testing.T) {
	a, r, stub := servedAPIKeyAgent(t, "repolicy")
	recordCard(t, a)
	stub.hold("repolicy", true)
	deletePolicy(t, a)
	reconcileOnce(t, r, a)

	_, want := goldenPolicy(t, a)
	name, _ := compiler.AuthPolicyName("repolicy")
	p := policyExists(t, runNS(a.Namespace), name)
	if p == nil {
		t.Fatal("the deleted policy was not re-created")
	}
	if d, _ := compiler.Digest(p); d != want {
		t.Errorf("the policy was re-created as %s, not as status.auth records (%s)", d, want)
	}
	if !routePublished(t, a.Namespace, "repolicy") {
		t.Error("the route was withdrawn while its policy was re-created; it was already open")
	}
	condIs(t, a, assaydv1alpha1.CondGovernanceSkipped, metav1.ConditionTrue, "AuthPolicyMissing")
	condIs(t, a, assaydv1alpha1.CondPolicyApplyIncomplete, metav1.ConditionTrue, "AuthPolicyMissing")
	if tx := txOf(t, a); tx == nil || tx.Kind != "Lock" || !tx.Written {
		t.Fatalf("want a Lock of the missing policy, written: %+v", tx)
	}

	acceptPolicy(t, a.Namespace, "repolicy")
	reconcileOnce(t, r, a)
	condIs(t, a, assaydv1alpha1.CondGovernanceSkipped, metav1.ConditionTrue, "AuthPolicyMissing")
	stub.hold("repolicy", false)
	reconcileOnce(t, r, a)
	if auth := authOf(t, a); auth.Transaction != nil || auth.Mode != "apikey" {
		t.Fatalf("the Lock did not reach Served on an attributed 401: %+v", auth)
	}
	condIs(t, a, assaydv1alpha1.CondGovernanceSkipped, metav1.ConditionFalse, "AuthVerifiedOnOneReplica")
	if c := condition(liveAgent(t, a), assaydv1alpha1.CondPolicyApplyIncomplete); c != nil {
		t.Errorf("AuthPolicyMissing outlived the probe that passed: %+v", c)
	}
}

// A 401 on a published route with no card digest recorded for the revision
// it serves cannot be attributed: the backend may have produced it. The Lock
// does not reach Served on it.
func TestAnUnattributable401DoesNotEndTheLock(t *testing.T) {
	a, r, stub := servedAPIKeyAgent(t, "unattributed")
	live := liveAgent(t, a)
	live.Status.Cards = nil
	if err := k8s.Status().Update(context.Background(), live); err != nil {
		t.Fatal(err)
	}
	deletePolicy(t, a)
	reconcileOnce(t, r, a)
	acceptPolicy(t, a.Namespace, "unattributed")
	stub.hold("unattributed", false)
	reconcileOnce(t, r, a)
	reconcileOnce(t, r, a)
	if tx := txOf(t, a); tx == nil || tx.Kind != "Lock" {
		t.Fatalf("an unattributable 401 ended the Lock: %+v", authOf(t, a))
	}
	c := condIs(t, a, assaydv1alpha1.CondGovernanceSkipped, metav1.ConditionTrue, "AuthPolicyMissing")
	if c != nil && !strings.Contains(c.Message, "cannot be attributed") {
		t.Errorf("the message does not say the 401 could not be attributed: %s", c.Message)
	}
}

// A prune takes both objects (§8.1 case 16's core). Route delete seen first,
// or both in one pass: the route re-create prepares the route and writes the
// policy from status.auth, and publishes only on the probe's 401.
func TestAPruneOfBothRouteFirstIsPreparedAgain(t *testing.T) {
	a, r, stub := servedAPIKeyAgent(t, "prunedrf")
	stub.hold("prunedrf", true)
	deleteRoute(t, a)
	deletePolicy(t, a)
	reconcileOnce(t, r, a)
	assertNeverOpen(t, a)
	if routePublished(t, a.Namespace, "prunedrf") {
		t.Fatal("the re-created route carries backendRefs with the switch held")
	}
	name, _ := compiler.AuthPolicyName("prunedrf")
	if policyExists(t, runNS(a.Namespace), name) == nil {
		t.Fatal("the re-create did not write the policy from status.auth")
	}
	if tx := txOf(t, a); tx == nil || tx.Kind != "Create" {
		t.Fatalf("want the route re-create's Create: %+v", tx)
	}
	driveToServed(t, r, stub, a)
}

// Policy delete seen first: a Lock takes the slot. The route delete seen next
// displaces it with the route re-create, whose Create has the Publishing the
// Lock lacks, and AuthPolicyMissing clears in that update (§3.3.3, A62, A63).
func TestAPruneOfBothPolicyFirstDisplacesTheLock(t *testing.T) {
	a, r, stub := servedAPIKeyAgent(t, "prunedpf")
	recordCard(t, a)
	stub.hold("prunedpf", true)
	deletePolicy(t, a)
	reconcileOnce(t, r, a)
	if tx := txOf(t, a); tx == nil || tx.Kind != "Lock" {
		t.Fatalf("want a Lock of the missing policy first: %+v", tx)
	}
	deleteRoute(t, a)
	reconcileOnce(t, r, a)
	assertNeverOpen(t, a)
	rt := servingRoute(t, a.Namespace, "prunedpf")
	if rt == nil || routePublished(t, a.Namespace, "prunedpf") {
		t.Fatalf("the route delete did not re-create a PREPARED route: %v", rt)
	}
	if tx := txOf(t, a); tx == nil || tx.Kind != "Create" {
		t.Fatalf("the route re-create did not displace the missing-policy Lock: %+v", tx)
	}
	if c := condition(liveAgent(t, a), assaydv1alpha1.CondPolicyApplyIncomplete); c != nil && c.Reason == "AuthPolicyMissing" {
		t.Errorf("the displaced Lock's AuthPolicyMissing outlived it: %+v", c)
	}
	driveToServed(t, r, stub, a)
}

// Review MAJOR 1: during an unfinished Create, a revision minted by an edit
// that does not compile gains no weight, as the message says. The Create
// finishes on the revision it had.
func TestAnUncompilableEditDuringACreateGainsNoWeight(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "midcreate", nil)
	r, stub := createReconciler()
	stub.hold("midcreate", true)
	promote(t, r, a)
	first := liveAgent(t, a).Status.ActiveRevision
	mustEdit(t, a, func(x *assaydv1alpha1.Agent) {
		x.Spec.Expose = &assaydv1alpha1.ExposeSpec{A2A: &assaydv1alpha1.ExposeProtocol{Auth: "oauth"}}
	})
	second := revision.MustHash(liveAgent(t, a).Spec)
	reconcileOnce(t, r, a)
	markAvailable(t, ns, controller.WorkloadName("midcreate", second), 1)
	reconcileOnce(t, r, a)
	reconcileOnce(t, r, a)
	if got := liveAgent(t, a).Status.ActiveRevision; got != first {
		t.Fatalf("an oauth revision minted during a Create was promoted (%s → %s); the "+
			"PolicyCompileFailed message says it gains no weight", first, got)
	}
	condIs(t, a, assaydv1alpha1.CondPolicyCompileFailed, metav1.ConditionTrue, "AuthInputAbsent")
}

func widen(t *testing.T, a *assaydv1alpha1.Agent) {
	t.Helper()
	name, _ := compiler.AuthPolicyName(a.Name)
	p := policyExists(t, runNS(a.Namespace), name)
	if err := unstructured.SetNestedStringSlice(p.Object, []string{"true"},
		"spec", "traffic", "authorization", "policy", "matchExpressions"); err != nil {
		t.Fatal(err)
	}
	if err := k8s.Update(context.Background(), p); err != nil {
		t.Fatalf("widen the policy: %v", err)
	}
}

func assertGolden(t *testing.T, a *assaydv1alpha1.Agent) {
	t.Helper()
	_, want := goldenPolicy(t, a)
	name, _ := compiler.AuthPolicyName(a.Name)
	if d, _ := compiler.Digest(policyExists(t, runNS(a.Namespace), name)); d != want {
		t.Errorf("the policy was not rewritten to the compiled one: digest %s, want %s", d, want)
	}
}

// Review MAJOR 2: a policy widened mid-Create still answers an anonymous
// request with 401, so the probe alone cannot catch it, and the stub oracle
// would mask it. The digest check must. At ProbingAfter it is rewritten
// before anything is published.
func TestAPolicyEditedDuringTheProbeIsRewrittenBeforePublication(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "widenedprobe", nil)
	r, stub := createReconciler()
	stub.hold("widenedprobe", true)
	promote(t, r, a)
	acceptRoute(t, ns, "widenedprobe")
	acceptPolicy(t, ns, "widenedprobe")
	reconcileOnce(t, r, a)
	if tx := txOf(t, a); tx.Stage != "ProbingAfter" {
		t.Fatalf("want ProbingAfter: %+v", tx)
	}
	widen(t, a)
	reconcileOnce(t, r, a)
	assertGolden(t, a)
	if routePublished(t, ns, "widenedprobe") {
		t.Error("the route was published over a widened policy")
	}
}

// ... and at Publishing, the route loses its backendRefs before the policy is
// written again.
func TestAPolicyEditedDuringPublishingStripsTheRoute(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "widenedpub", nil)
	r, stub := createReconciler()
	promote(t, r, a)
	acceptRoute(t, ns, "widenedpub")
	acceptPolicy(t, ns, "widenedpub")
	reconcileOnce(t, r, a)
	if tx := txOf(t, a); tx.Stage != "Publishing" || !routePublished(t, ns, "widenedpub") {
		t.Fatalf("want Publishing with the route published: %+v", tx)
	}
	widen(t, a)
	stub.hold("widenedpub", true)
	reconcileOnce(t, r, a)
	assertGolden(t, a)
	if routePublished(t, ns, "widenedpub") {
		t.Error("a policy widened during Publishing left the route carrying backendRefs")
	}
}
