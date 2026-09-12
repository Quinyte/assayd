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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/compiler"
	"github.com/Quinyte/assayd/internal/controller"
	"github.com/Quinyte/assayd/internal/revision"
)

// Design 03's J2 (§1.1, §3.3.3, ADR-0034 Amendment 2), against a real
// agentgateway: a served `auth: none` Agent edited to apikey is LOCKED IN
// PLACE. The operator writes `<agent>-auth` onto the route that is already
// serving, trusts it only once an anonymous request through the route gets a
// 401 it can attribute, and only then records `mode: apikey` and removes the
// no-auth marker.
//
// Measured here, through the Gateway's own Service: an anonymous request
// gets the agent's 200 before the edit and 401 after; a key in the Agent's
// namespace group is still answered by the agent; status.auth reaches Served.
// The edit mints a revision, and nothing holds its promotion, so on this
// cluster the Lock usually ends on the new revision's card digest.
func TestAServedNoneAgentIsLockedInPlace(t *testing.T) {
	requireCluster(t)
	requireOperator(t)
	img := responderImage(t)
	gwNS, gwName := requireGateway(t)
	ctx := context.Background()
	ensureNamespace(t, ctx, "assayd-e2e")

	const name = "locked"
	a := &assaydv1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "assayd-e2e"},
		Spec: assaydv1alpha1.AgentSpec{
			Runtime: &assaydv1alpha1.AgentRuntime{
				Image: img,
				Env:   []corev1.EnvVar{{Name: "AGENT_NAME", Value: name}},
			},
			Expose: &assaydv1alpha1.ExposeSpec{A2A: &assaydv1alpha1.ExposeProtocol{Auth: "none"}},
		},
	}
	_ = k8s.Delete(ctx, a)
	if err := k8s.Create(ctx, a); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	t.Cleanup(func() { _ = k8s.Delete(context.Background(), a) })

	waitAvailable(t, ctx, controller.WorkloadName(name, revision.MustHash(a.Spec)), 4*time.Minute)
	route := waitForPublishedRoute(t, ctx, name, 3*time.Minute)
	if got := route.GetLabels()[compiler.LabelAuth]; got != "none" {
		t.Fatalf("an auth: none route was published without its marker (%q)", got)
	}
	waitForAuth(t, ctx, name, 2*time.Minute, "Served under none", func(s *assaydv1alpha1.AuthStatus) bool {
		return s != nil && s.Mode == "none" && s.Transaction == nil
	})
	ensureAPIKeys(t, ctx)

	gwSvc := gatewayService(t, ctx, gwNS, gwName)
	url := fmt.Sprintf("http://%s.%s.svc.cluster.local:8080", gwSvc, gwNS) + a2aSendMessage
	host := emittedHostname(t, name, "assayd-e2e")
	body := sendMessage("before and after the lock")

	waitForCode(t, ctx, "lock-open", url, host, body, "200", 2*time.Minute,
		"an anonymous caller of an auth: none route must be answered by the agent")

	// The owner's edit: J2.
	for i := 0; ; i++ {
		var live assaydv1alpha1.Agent
		if err := k8s.Get(ctx, types.NamespacedName{Namespace: "assayd-e2e", Name: name}, &live); err != nil {
			t.Fatal(err)
		}
		live.Spec.Expose.A2A.Auth = "apikey"
		err := k8s.Update(ctx, &live)
		if err == nil {
			break
		}
		if i == 5 {
			t.Fatalf("edit auth to apikey: %v", err)
		}
		time.Sleep(time.Second)
	}

	auth := waitForAuth(t, ctx, name, 5*time.Minute, "Served under apikey", func(s *assaydv1alpha1.AuthStatus) bool {
		return s != nil && s.Mode == "apikey" && s.Transaction == nil
	})
	if auth.AppliedDigest == "" {
		t.Errorf("Served recorded no applied digest: %+v", auth)
	}
	assertServedUnderAPIKey(t, ctx, name)

	if rt, err := getEmittedRoute(ctx, t, name); err != nil {
		t.Fatalf("read the route: %v", err)
	} else if v, ok := rt.GetLabels()[compiler.LabelAuth]; ok {
		t.Errorf("the no-auth marker %q survived the lock", v)
	}

	waitForCode(t, ctx, "lock-closed", url, host, body, "401", 2*time.Minute,
		"an anonymous caller of a locked route must be refused")
	answer := askThroughGateway(t, ctx, "lock-key", url, host, body)
	if agent, _ := completedTask(t, answer); agent != name {
		t.Errorf("a key in the Agent's group got a 200 that is not the agent's answer: %s", answer)
	}
}

// waitForAuth polls the Agent's status.auth until ok holds, and fails with it
// and the conditions otherwise.
func waitForAuth(t *testing.T, ctx context.Context, name string, d time.Duration, what string,
	ok func(*assaydv1alpha1.AuthStatus) bool) *assaydv1alpha1.AuthStatus {
	t.Helper()
	deadline := time.Now().Add(d)
	var a assaydv1alpha1.Agent
	for time.Now().Before(deadline) {
		if err := k8s.Get(ctx, types.NamespacedName{Namespace: "assayd-e2e", Name: name}, &a); err == nil &&
			ok(a.Status.Auth) {
			return a.Status.Auth
		}
		time.Sleep(2 * time.Second)
	}
	var conds []string
	for _, c := range a.Status.Conditions {
		conds = append(conds, fmt.Sprintf("%s=%s/%s: %s", c.Type, c.Status, c.Reason, c.Message))
	}
	t.Fatalf("%s never reached %s within %s. status.auth=%+v conditions:\n%s", name, what, d,
		a.Status.Auth, strings.Join(conds, "\n"))
	return nil
}

// waitForCode polls an anonymous request until it gets want.
func waitForCode(t *testing.T, ctx context.Context, tag, url, host, body, want string, d time.Duration, why string) {
	t.Helper()
	deadline := time.Now().Add(d)
	var last string
	for i := 0; time.Now().Before(deadline); i++ {
		if last = probeCode(t, ctx, fmt.Sprintf("%s-%d", tag, i), url, host, "", body); last == want {
			return
		}
		time.Sleep(5 * time.Second)
	}
	t.Fatalf("%s: an anonymous request got %s, not %s, for %s", why, last, want, d)
}
