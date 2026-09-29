// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package envtest

import (
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/controller"
	"github.com/Quinyte/assayd/internal/revision"
)

// Design 02 A78, the human's decision of 2026-09-29: nothing in this install
// verifies an image signature, so every Agent says so, as
// ImageSignatureUnverified=True. It is an announcement and not an incident, so
// it must leave Ready, Degraded and the phase exactly where they would be
// without it.

// imageSignatureUnverified asserts the announcement and returns it.
func imageSignatureUnverified(t *testing.T, a *assaydv1alpha1.Agent, parts ...string) *metav1.Condition {
	t.Helper()
	c := condIs(t, a, assaydv1alpha1.CondImageSignatureUnverified, metav1.ConditionTrue,
		controller.ReasonSignatureVerificationNotBuilt)
	mustContain(t, c, "ImageSignatureUnverified", append([]string{"assayd verifies no image signature, and does not detect an admission verifier installed outside it"}, parts...)...)
	return c
}

func TestAFreshAgentAnnouncesThatAssaydVerifiesNoImageSignature(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "unsigned", nil)
	r := newReconciler(false)
	settle(t, r, a)
	// Before the workload is available: the announcement does not wait for it.
	imageSignatureUnverified(t, a, "pinned by a sha256 digest")

	markAvailable(t, ns, controller.WorkloadName("unsigned", revision.MustHash(a.Spec)), 1)
	got := settle(t, r, a)

	imageSignatureUnverified(t, a, "pinned by a sha256 digest")
	// Announce, not degrade: the human's option said so, and an Agent that is
	// serving must still read as serving.
	condIs(t, a, assaydv1alpha1.CondReady, metav1.ConditionTrue, "Available")
	if got.Status.Phase != assaydv1alpha1.PhaseReady {
		t.Errorf("phase is %q beside ImageSignatureUnverified, want Ready", got.Status.Phase)
	}
	if c := condition(&got, assaydv1alpha1.CondDegraded); c != nil && c.Status == metav1.ConditionTrue {
		t.Errorf("Degraded=True beside ImageSignatureUnverified: %+v", c)
	}
}

// The announcement is a constant, so it must cost nothing once written: no
// write on a converged Agent, and a LastTransitionTime that says when it was
// first set rather than when it was last observed — across passes that DO
// write status, too.
func TestTheImageSignatureAnnouncementDoesNotChurn(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "steady", nil)
	r := newReconciler(false)
	settle(t, r, a)
	markAvailable(t, ns, controller.WorkloadName("steady", revision.MustHash(a.Spec)), 1)
	settle(t, r, a)
	first := imageSignatureUnverified(t, a)

	counter := &countingClient{Client: k8s}
	counting := &controller.AgentReconciler{
		Client: counter, Scheme: scheme, EvalSuiteInstalled: func() bool { return false },
		OperatorNamespace: operatorNamespace, LabelAuthorityPresent: labelAuthorityPresent,
	}
	for i := 0; i < 5; i++ {
		reconcileOnce(t, counting, a)
	}
	if n := counter.count(); n != 0 {
		t.Errorf("five reconciles of a converged Agent issued %d writes; want 0", n)
	}

	// metav1.Time serialises to the second, so a refreshed timestamp is only
	// visible once a second has passed.
	time.Sleep(1100 * time.Millisecond)
	mustEdit(t, a, func(x *assaydv1alpha1.Agent) {
		x.Spec.Runtime.Env = []corev1.EnvVar{{Name: "EDITED", Value: "1"}}
	})
	reconcileOnce(t, r, a)
	after := imageSignatureUnverified(t, a)
	if !after.LastTransitionTime.Equal(&first.LastTransitionTime) {
		t.Errorf("a status-writing pass moved ImageSignatureUnverified's LastTransitionTime from %v "+
			"to %v; its status never changed", first.LastTransitionTime, after.LastTransitionTime)
	}
	if live := liveAgent(t, a); after.ObservedGeneration != live.Generation {
		t.Errorf("ImageSignatureUnverified observed generation %d, want %d: the pass re-derived it",
			after.ObservedGeneration, live.Generation)
	}
}

// Every Agent, not only the ones this cluster runs: an external Agent runs no
// image here, and the message must not claim a digest it does not have.
func TestAnExternalAgentAnnouncesThatAssaydVerifiesNoImageSignature(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "external", func(a *assaydv1alpha1.Agent) {
		a.Spec.Runtime = nil
		a.Spec.External = &assaydv1alpha1.ExternalAgent{Endpoint: "https://agent.example.com"}
	})
	settle(t, newReconciler(false), a)
	c := imageSignatureUnverified(t, a, "runs outside this cluster",
		"nothing designed would clear this condition for it")
	mustNotContain(t, c, "ImageSignatureUnverified", "pinned by a sha256 digest", "nothing clears it yet",
		"no image signature is checked")
	// This exit sets Ready BEFORE the announcement, so it is the one where an
	// announcement that touched Ready would show.
	condIs(t, a, assaydv1alpha1.CondReady, metav1.ConditionFalse, "ExternalRegistrationUnimplemented")
}

// The env-source refusal is its own exit with its own condition set. An
// owned condition it did not assert would be CLEARED there.
func TestAnUnresolvedEnvSourceKeepsTheImageSignatureAnnouncement(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "unresolved", func(a *assaydv1alpha1.Agent) {
		a.Spec.Runtime.EnvFrom = []corev1.EnvFromSource{{
			ConfigMapRef: &corev1.ConfigMapEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: "absent"}},
		}}
	})
	got := settle(t, newReconciler(false), a)
	if c := condition(&got, assaydv1alpha1.CondEnvSourceUnresolved); c == nil || c.Status != metav1.ConditionTrue {
		t.Fatalf("the fixture did not reach the env-source refusal: %+v", c)
	}
	imageSignatureUnverified(t, a, "pinned by a sha256 digest")
}

// Every early exit of the ordinary path merges the condition set the pass
// built at its top, so the announcement must be asserted there, before any of
// them. Two exits pin that. The run-namespace refusal is the EARLIEST: an
// assessor moved to anywhere after it clears the announcement there, which
// the release-pin case alone did not catch (the independent review of PR #76
// moved the call to just after it, and the whole suite stayed green). The
// unresolvable release pin is a later exit, reached on an Agent that is
// serving.
func TestTheEarliestExitOfTheOrdinaryPathKeepsTheImageSignatureAnnouncement(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "noauthority", nil)
	r := newReconciler(false)
	r.LabelAuthorityPresent = func(context.Context) (bool, error) { return false, nil }
	settle(t, r, a)
	condIs(t, a, assaydv1alpha1.CondRunNamespaceUnavailable, metav1.ConditionTrue,
		controller.ReasonLabelAuthorityAbsent)
	imageSignatureUnverified(t, a, "pinned by a sha256 digest")
}

func TestAnEarlyExitOfTheOrdinaryPathKeepsTheImageSignatureAnnouncement(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "earlyexit", nil)
	r := newReconciler(false)
	settle(t, r, a)
	imageSignatureUnverified(t, a)
	mustEdit(t, a, func(x *assaydv1alpha1.Agent) {
		x.Spec.Release = &assaydv1alpha1.ReleaseSpec{TargetRevisionDigest: strings.Repeat("c", 64)}
	})
	settle(t, r, a)
	condIs(t, a, assaydv1alpha1.CondDegraded, metav1.ConditionTrue, "ReleasePinUnresolvable")
	imageSignatureUnverified(t, a, "pinned by a sha256 digest")
}

// The gateway paths: a served API-key Agent stays Ready beside it, and a
// refused Adopt — whose pass assigns status.auth wholesale — keeps it.
func TestGatewayAgentsKeepTheImageSignatureAnnouncement(t *testing.T) {
	t.Run("served apikey", func(t *testing.T) {
		a, r, _ := healthyServedAPIKeyAgent(t, "sig-served")
		imageSignatureUnverified(t, a)
		reconcileOnce(t, r, a)
		imageSignatureUnverified(t, a)
		condIs(t, a, assaydv1alpha1.CondReady, metav1.ConditionTrue, "Available")
	})
	t.Run("refused adopt", func(t *testing.T) {
		a, r := refusedAdoptAgent(t, "sig-adopt")
		reconcileOnce(t, r, a)
		imageSignatureUnverified(t, a)
	})
}
