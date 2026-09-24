// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/compiler"
)

// Design 03 A86 with the human's K1 (ADR-0034 Amendment 7), on a real cluster
// running the operator just built: deleting the run namespace's API-key set
// takes a served API-key Agent to PolicyApplyIncomplete=True and Ready=False,
// both ApiKeySourceEmpty, phase Degraded — and writing a key set back heals it.
// Neither transition waits on a requeue: the Agent is Ready, so its only
// timed requeue is CardDriftInterval, five minutes, and the deadlines here are
// one minute each, so a transition inside them is the key-set watch's.
//
// It measures the OPERATOR, not agentgateway: that every key then gets 401 is
// A82's conformance row 3, and design 03 §8.1 keeps it there.
func TestAnEmptiedKeySetDegradesAServedAgentUntilKeysReturn(t *testing.T) {
	requireCluster(t)
	requireOperator(t)
	img := responderImage(t)
	requireGateway(t)
	ctx := context.Background()
	ensureNamespace(t, ctx, "assayd-e2e")

	const name = "keysource"
	ensureAPIKeys(t, ctx)
	deployResponder(t, ctx, name, img)
	waitForPublishedRoute(t, ctx, name, 3*time.Minute)
	assertServedUnderAPIKey(t, ctx, name)
	waitForReason(t, ctx, name, assaydv1alpha1.CondReady, "Available", 2*time.Minute)

	// Deleted as the suite's own identity, which is cluster-admin: deletes of
	// a key set are RBAC's, and assayd-api-keys reserves only CREATE and
	// UPDATE (design 03 A86, "Why admission cannot prevent it").
	if err := k8s.DeleteAllOf(ctx, &corev1.ConfigMap{}, client.InNamespace(runNS),
		client.MatchingLabels{compiler.APIKeySourceLabel: compiler.APIKeySourceValue}); err != nil {
		t.Fatalf("delete the key set: %v", err)
	}
	waitForReason(t, ctx, name, assaydv1alpha1.CondPolicyApplyIncomplete, "ApiKeySourceEmpty", time.Minute)
	waitForReason(t, ctx, name, assaydv1alpha1.CondReady, "ApiKeySourceEmpty", time.Minute)
	var a assaydv1alpha1.Agent
	if err := k8s.Get(ctx, types.NamespacedName{Namespace: "assayd-e2e", Name: name}, &a); err != nil {
		t.Fatal(err)
	}
	if a.Status.Phase != assaydv1alpha1.PhaseDegraded {
		t.Errorf("a served Agent nobody can call is %s, want Degraded (K1)", a.Status.Phase)
	}
	if a.Status.Auth == nil || !a.Status.Auth.KeySourceEmpty {
		t.Errorf("status.auth.keySourceEmpty was not recorded on the installed CRD: %+v", a.Status.Auth)
	}
	if rt, err := getEmittedRoute(ctx, t, name); err != nil {
		t.Errorf("the route is gone; nothing is withdrawn for an empty key source: %v", err)
	} else {
		assertRouteAccepted(t, ctx, rt, "")
	}

	ensureAPIKeys(t, ctx)
	waitForReason(t, ctx, name, assaydv1alpha1.CondReady, "Available", time.Minute)
}
