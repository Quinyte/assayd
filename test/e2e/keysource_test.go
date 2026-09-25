// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"fmt"
	"strings"
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

	// The same entries held under binaryData alone: agentgateway does not read
	// binaryData (A88 (3)), so the human's §9 D8 (R1) counts data only and
	// this is the same report (A89). The message names the entries it does
	// not count. writeAPIKeys deletes before it creates, so the absent report
	// may stand for a moment first: the row waits for the message, not only
	// the reason.
	writeAPIKeys(t, ctx, true)
	const binaryNote = "entries under binaryData, which agentgateway 1.5.0 does not read"
	last := ""
	for deadline := time.Now().Add(time.Minute); ; time.Sleep(2 * time.Second) {
		if err := k8s.Get(ctx, types.NamespacedName{Namespace: "assayd-e2e", Name: name}, &a); err == nil {
			for _, c := range a.Status.Conditions {
				if c.Type == string(assaydv1alpha1.CondPolicyApplyIncomplete) && c.Reason == "ApiKeySourceEmpty" {
					last = c.Message
				}
			}
		}
		if strings.Contains(last, binaryNote) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("a binaryData-only key set is not reported as one within a minute; the last "+
				"ApiKeySourceEmpty message: %q", last)
		}
	}
	waitForReason(t, ctx, name, assaydv1alpha1.CondReady, "ApiKeySourceEmpty", time.Minute)

	// The report is true of the agentgateway THIS run installed, not only of
	// the one make conformance-cluster pins: the permitted key, whose entry is
	// held under binaryData, is refused, and stays refused. The report stood
	// only after the binaryData write, so the gateway has had it for as long.
	// A release that read binaryData would answer 200 here, and the report
	// would be false (A89).
	gwNS, gwName := requireGateway(t)
	url := fmt.Sprintf("http://%s.%s.svc.cluster.local:8080", gatewayService(t, ctx, gwNS, gwName), gwNS) +
		a2aSendMessage
	host := emittedHostname(t, name, "assayd-e2e")
	body := sendMessage("keysource")
	for i := range 3 {
		if code := probeCode(t, ctx, fmt.Sprintf("keysrc-binary-%d", i), url, host, permittedKey, body); code != "401" {
			t.Fatalf("with its entry held only under binaryData, the permitted key got %s through the gateway, "+
				"not 401: this agentgateway reads binaryData, and the ApiKeySourceEmpty report is false on it",
				code)
		}
		time.Sleep(2 * time.Second)
	}

	// The same entries under data: the report clears, and the same key is
	// admitted, so only where the entries were held differed.
	ensureAPIKeys(t, ctx)
	waitForReason(t, ctx, name, assaydv1alpha1.CondReady, "Available", time.Minute)
	askThroughGateway(t, ctx, "keysrc-data", url, host, body)
}
