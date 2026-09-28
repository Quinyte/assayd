// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"fmt"
	"os"
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
// Its first half, the deleted key set, measures the OPERATOR, not
// agentgateway: that every key then gets 401 is A82's conformance row 3, and
// design 03 §8.1 keeps it there. Its second half, A89's, measures both: the
// same entries moved in place under binaryData must raise the report AND get
// the permitted key 401 through the gateway this run installed, having got
// 200 just before — because the report is true only while agentgateway does
// not read binaryData, and CI runs this suite, not make conformance-cluster.
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

	// A89: the same entries moved under binaryData. First the permitted key
	// is measured at 200 through the gateway, so a 401 after the move is the
	// move's. The move is an in-place UPDATE of the same ConfigMap, never a
	// delete, so a 401 cannot be a gateway that saw the key set vanish, and
	// the operator hears it through the key-set watch as an update.
	gwNS, gwName := requireGateway(t)
	url := fmt.Sprintf("http://%s.%s.svc.cluster.local:8080", gatewayService(t, ctx, gwNS, gwName), gwNS) +
		a2aSendMessage
	host := emittedHostname(t, name, "assayd-e2e")
	body := sendMessage("keysource")
	askThroughGateway(t, ctx, "keysrc-before", url, host, body)

	moveAPIKeys(t, ctx, true)
	agw := os.Getenv("ASSAYD_E2E_AGW_VERSION")
	if agw == "" {
		t.Fatal("ASSAYD_E2E_AGW_VERSION is unset: hack/e2e.sh exports the agentgateway release it installed, " +
			"and this row holds the operator's message to it")
	}
	// The message names the release measured not to read binaryData. On a bump
	// of hack/e2e.sh this fails until the message and the measurement are
	// brought to the new release (design 03 A89).
	binaryNote := "entries under binaryData, which agentgateway " + agw + " does not read"
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
			t.Fatalf("a key set whose entries were moved in place under binaryData is not reported as one "+
				"within a minute; the last ApiKeySourceEmpty message: %q", last)
		}
	}
	waitForReason(t, ctx, name, assaydv1alpha1.CondReady, "ApiKeySourceEmpty", time.Minute)

	// The gateway: poll to the first 401, since the proxy picks the update up
	// on its own clock, then hold it. Only a 200 means this agentgateway reads
	// binaryData; anything else is named for what it is.
	codeMeans := func(code string) string {
		switch {
		case code == "200":
			return "this agentgateway reads binaryData, and the ApiKeySourceEmpty report is false on it"
		case code == "000":
			return "no HTTP response at all: the probe's curl failed to connect or timed out"
		case strings.HasPrefix(code, "5"):
			return "a server error from the gateway or the agent, which says nothing about binaryData"
		default:
			return "neither the 401 an unread key set gives nor the 200 a read one would"
		}
	}
	got := ""
	for i, deadline := 0, time.Now().Add(time.Minute); ; i++ {
		if got = probeCode(t, ctx, fmt.Sprintf("keysrc-move-%d", i), url, host, permittedKey, body); got == "401" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("with its entry moved in place under binaryData, the permitted key still got %s through "+
				"the gateway after a minute: %s", got, codeMeans(got))
		}
		time.Sleep(3 * time.Second)
	}
	for i := range 3 {
		time.Sleep(2 * time.Second)
		if code := probeCode(t, ctx, fmt.Sprintf("keysrc-hold-%d", i), url, host, permittedKey, body); code != "401" {
			t.Fatalf("the permitted key, held only under binaryData, got 401 and then %s: %s", code,
				codeMeans(code))
		}
	}

	// Moved back in place under data: the report clears, and the same key is
	// admitted, so only where the entries were held differed.
	moveAPIKeys(t, ctx, false)
	waitForReason(t, ctx, name, assaydv1alpha1.CondReady, "Available", time.Minute)
	askThroughGateway(t, ctx, "keysrc-after", url, host, body)
}
