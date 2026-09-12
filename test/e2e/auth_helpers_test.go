// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/compiler"
)

// Every e2e Agent on the gateway lane has no expose block, so it compiles to
// design 03 §3.4.4's derived default: an `<agent>-auth` policy that admits
// API keys in the group named for its namespace. The operator publishes its
// route only after an anonymous request through it gets 401, so a request
// through the gateway now needs a key in that group.

const (
	// admittedGroup is the group the emitted policy admits: assayd-e2e's own.
	admittedGroup = "assayd-e2e"
	// permittedKey is a key in that group. refusedKey is a valid key in
	// another group, which the policy authenticates and then refuses.
	permittedKey = "assayd-e2e-permitted"
	refusedKey   = "assayd-e2e-refused"
)

// ensureAPIKeys writes the key set as agentgateway reads it, carrying the
// slice's constant key-source label, compiler.APIKeySourceLabel=
// compiler.APIKeySourceValue, which every emitted policy selects. The label is
// the product's selector, not a test's (design 03 §8.1).
//
// Each entry is `{"keyHash": "sha256:<hex>", "metadata": {"group": …}}`. The
// API refuses a raw key in a ConfigMap, so only the hash is written, and the
// keys are literals in a test file, not secrets. It is written as the
// administrator design 03 §3.4.4 says writes key sets: assayd-api-keys
// refuses anyone else in a run namespace.
func ensureAPIKeys(t *testing.T, ctx context.Context) {
	t.Helper()
	data := map[string]string{}
	for key, group := range map[string]string{permittedKey: admittedGroup, refusedKey: "rogue"} {
		sum := sha256.Sum256([]byte(key))
		data[key] = fmt.Sprintf(`{"keyHash":"sha256:%s","metadata":{"group":%q}}`,
			hex.EncodeToString(sum[:]), group)
	}
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "assayd-e2e-api-keys",
			Namespace: runNS,
			Labels:    map[string]string{compiler.APIKeySourceLabel: compiler.APIKeySourceValue},
		},
		Data: data,
	}
	_ = k8s.Delete(ctx, cm)
	if err := keyAdminClient(t).Create(ctx, cm); err != nil {
		t.Fatalf("write the API key set: %v", err)
	}
	t.Cleanup(func() { _ = k8s.Delete(context.Background(), cm) })
}

// waitForPublishedRoute waits until the operator's route carries backendRefs,
// which it does only once its `Create` saw the anonymous 401 (design 03
// §3.3.3). The route exists before that, prepared, answering 500. On timeout
// it reports the Agent's status.auth and conditions, which say which stage the
// transaction stopped in and why.
func waitForPublishedRoute(t *testing.T, ctx context.Context, agentName string, d time.Duration) *unstructured.Unstructured {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if rt, err := getEmittedRoute(ctx, t, agentName); err == nil {
			rules, _, _ := unstructured.NestedSlice(rt.Object, "spec", "rules")
			if len(rules) == 1 {
				if refs, _, _ := unstructured.NestedSlice(rules[0].(map[string]any), "backendRefs"); len(refs) > 0 {
					return rt
				}
			}
		}
		time.Sleep(2 * time.Second)
	}
	var a assaydv1alpha1.Agent
	_ = k8s.Get(ctx, types.NamespacedName{Namespace: "assayd-e2e", Name: agentName}, &a)
	t.Fatalf("the operator did not publish %s's route within %s. status.auth=%+v conditions=%+v",
		agentName, d, a.Status.Auth, a.Status.Conditions)
	return nil
}

// askThroughGateway sends an A2A request through the gateway with the
// permitted key and returns the body, once the gateway has read the key set:
// agentgateway reads it live, so a key written a moment ago can still be
// unknown, and a 401 body would read as a broken agent.
func askThroughGateway(t *testing.T, ctx context.Context, tag, url, host, body string) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	var last string
	for i := 0; time.Now().Before(deadline); i++ {
		if last = probeCode(t, ctx, fmt.Sprintf("%s-key-%d", tag, i), url, host, permittedKey, body); last == "200" {
			return httpInClusterHostKey(t, ctx, tag, url, host, permittedKey, body)
		}
		time.Sleep(5 * time.Second)
	}
	t.Fatalf("a key in group %s was answered %s through the gateway for two minutes", admittedGroup, last)
	return ""
}

// assertServedUnderAPIKey requires the Agent's `Create` to have reached
// `Served` under the API-key policy, and to say what one probe proved (H2).
func assertServedUnderAPIKey(t *testing.T, ctx context.Context, agentName string) {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	var a assaydv1alpha1.Agent
	for time.Now().Before(deadline) {
		if err := k8s.Get(ctx, types.NamespacedName{Namespace: "assayd-e2e", Name: agentName}, &a); err == nil &&
			a.Status.Auth != nil && a.Status.Auth.Mode == "apikey" && a.Status.Auth.Transaction == nil {
			for _, c := range a.Status.Conditions {
				if c.Type == string(assaydv1alpha1.CondGovernanceSkipped) {
					if c.Status != metav1.ConditionFalse || c.Reason != "AuthVerifiedOnOneReplica" {
						t.Errorf("GovernanceSkipped is %s/%s on a served API-key Agent; with no replica "+
							"count declared, H2 says False/AuthVerifiedOnOneReplica", c.Status, c.Reason)
					}
					return
				}
			}
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("%s never reached Served under apikey: status.auth=%+v", agentName, a.Status.Auth)
}
