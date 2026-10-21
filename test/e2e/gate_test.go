// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/controller"
	"github.com/Quinyte/assayd/internal/revision"
)

// Design 16 §1.1's two e2e rows, on k3d against a real agentgateway. Every
// other row of §1.1 runs on envtest, where no kubelet runs, no card is served
// by a container and no request crosses a gateway; these two are the only
// place the gate is measured with a real candidate answering a real A2A task
// and a real proxy deciding which revision a caller reaches.
//
// The eval's own requests do not cross the gateway: the operator sends each
// case to the candidate's revision Service (§1.1, "What the slice is"). What
// crosses the gateway here is the caller's traffic, which is what the gate
// protects.

// gateSuiteCase is the suite both rows gate on: one case that sends `hello`
// and expects `echo: hello`, which test/responder answers with its default
// ANSWER_PREFIX and no other prefix does.
var gateSuiteCase = assaydv1alpha1.EvalCase{ID: "hello", Message: "hello",
	Expect: assaydv1alpha1.EvalExpect{Contains: "echo: hello"}}

// T68. A gated API-key Agent's first revision is evaluated and passes before
// it is published; a revision whose answer prefix fails the suite holds with
// EvalFailed while the first one keeps answering every request through the
// gateway for 60 s; and a revision that restores the prefix and changes the
// metadata marker passes, is promoted, and answers 20 requests in a row once
// the route names it.
func TestARevisionIsEvaluatedBeforeItTakesTraffic(t *testing.T) {
	requireCluster(t)
	requireOperator(t)
	img := responderImage(t)
	gwNS, gwName := requireGateway(t)
	ctx := context.Background()
	ensureRunNamespace(t, ctx)
	ensureAPIKeys(t, ctx)

	const name = "gated"
	applyGateSuite(t, ctx, name+"-suite")
	a := gatedAgent(name, img, name+"-suite", corev1.EnvVar{Name: "ANSWER_MARKER", Value: "first"})
	createAgent(t, ctx, a)

	// The first revision: evaluated, passed, promoted, and only then published.
	r1 := a.Spec
	waitAvailable(t, ctx, controller.WorkloadName(name, revision.MustHash(r1)), 4*time.Minute)
	waitForVerdict(t, ctx, name, r1, assaydv1alpha1.EvalVerdictPassed, 4*time.Minute)
	waitForAgent(t, ctx, name, 3*time.Minute, "the first revision promoted with EvalPassed", func(a *assaydv1alpha1.Agent) bool {
		return a.Status.ActiveRevision == revision.MustHash(r1) && gatesPassed(a, metav1.ConditionTrue, "EvalPassed")
	})
	waitForPublishedRoute(t, ctx, name, 3*time.Minute)
	assertServedUnderAPIKey(t, ctx, name)
	assertRouteNames(t, ctx, name, r1)

	gwSvc := gatewayService(t, ctx, gwNS, gwName)
	url := fmt.Sprintf("http://%s.%s.svc.cluster.local:8080", gwSvc, gwNS) + a2aSendMessage
	host := emittedHostname(t, name, "assayd-e2e")
	waitForMarker(t, ctx, "gated-first", url, host, "first")

	// The second revision changes only the prefix, so its answer to the case
	// is `held: hello` and the suite fails it.
	r2 := editAgent(t, ctx, name, func(s *assaydv1alpha1.AgentSpec) {
		s.Runtime.Env = append(s.Runtime.Env, corev1.EnvVar{Name: "ANSWER_PREFIX", Value: "held: "})
	})
	waitAvailable(t, ctx, controller.WorkloadName(name, revision.MustHash(r2)), 4*time.Minute)
	held := waitForVerdict(t, ctx, name, r2, assaydv1alpha1.EvalVerdictFailed, 4*time.Minute)
	if c := caseOutcome(held.Status.Eval, gateSuiteCase.ID); c != assaydv1alpha1.EvalCaseFailed {
		t.Errorf("the failed run records case %s as %q, not Failed: %+v", gateSuiteCase.ID, c, held.Status.Eval.Cases)
	}
	held = waitForAgent(t, ctx, name, 2*time.Minute, "the failed candidate held with EvalFailed", func(a *assaydv1alpha1.Agent) bool {
		return gatesPassed(a, metav1.ConditionFalse, "EvalFailed")
	})
	if held.Status.ActiveRevision != revision.MustHash(r1) || held.Status.CandidateRevision != revision.MustHash(r2) ||
		held.Status.Phase != assaydv1alpha1.PhaseHeld {
		t.Errorf("a failed candidate must hold, phase Held, with the first revision active: active=%s candidate=%s "+
			"phase=%s, want active=%s candidate=%s phase=Held", held.Status.ActiveRevision,
			held.Status.CandidateRevision, held.Status.Phase, revision.MustHash(r1), revision.MustHash(r2))
	}

	// For 60 s, every answer through the gateway is the first revision's.
	for i, ans := range answersThroughGateway(t, ctx, "gated-hold", url, host, 31, 2*time.Second) {
		if ans.code != "200" || ans.state != "TASK_STATE_COMPLETED" || ans.text != "echo: probe" || ans.marker != "first" {
			t.Errorf("answer %d through the gateway while the failed candidate held is not the first "+
				"revision's (HTTP 200, a completed task, `echo: probe`, marker first): %+v", i, ans)
		}
	}
	assertRouteNames(t, ctx, name, r1)

	// The third revision restores the prefix and changes the marker: it passes,
	// is promoted, and takes the traffic.
	r3 := editAgent(t, ctx, name, func(s *assaydv1alpha1.AgentSpec) {
		s.Runtime.Env = []corev1.EnvVar{{Name: "AGENT_NAME", Value: name}, {Name: "ANSWER_MARKER", Value: "third"}}
	})
	waitAvailable(t, ctx, controller.WorkloadName(name, revision.MustHash(r3)), 4*time.Minute)
	waitForVerdict(t, ctx, name, r3, assaydv1alpha1.EvalVerdictPassed, 4*time.Minute)
	waitForAgent(t, ctx, name, 3*time.Minute, "the third revision promoted with EvalPassed", func(a *assaydv1alpha1.Agent) bool {
		return a.Status.ActiveRevision == revision.MustHash(r3) && gatesPassed(a, metav1.ConditionTrue, "EvalPassed")
	})
	waitForRouteNames(t, ctx, name, r3, 2*time.Minute)
	waitForMarker(t, ctx, "gated-third", url, host, "third")
	for i, ans := range answersThroughGateway(t, ctx, "gated-third-run", url, host, 20, 0) {
		if ans.code != "200" || ans.state != "TASK_STATE_COMPLETED" || ans.text != "echo: probe" || ans.marker != "third" {
			t.Errorf("answer %d of 20 after the route named the third revision is not the third "+
				"revision's (HTTP 200, a completed task, `echo: probe`, marker third): %+v", i, ans)
		}
	}
}

// T69. A Gateway-level API-key policy stands while a new gated Agent's first
// revision passes its suite. The promotion is the gate's and does not wait for
// the route: the revision becomes active with EvalPassed. The route is
// design 03's, and its `Create` holds, unpublished, under
// PolicyApplyIncomplete=GatewayAuthPolicy, because an anonymous 401 the
// Gateway-level policy could have answered is not credited (design 03 A75).
// Once the policy goes, the route is published on the promoted revision.
//
// Clause 5 of §1.1's exemption is not what holds here: a `Create` is never
// exempt, and a new gated Agent has no transaction until its first revision
// is promoted, so the eval ran with nothing recorded beside it.
func TestAGatewayLevelPolicyHoldsTheCreateAfterTheGateHasPassed(t *testing.T) {
	requireCluster(t)
	requireOperator(t)
	img := responderImage(t)
	gwNS, gwName := requireGateway(t)
	ctx := context.Background()
	ensureRunNamespace(t, ctx)
	ensureAPIKeys(t, ctx)
	gp := applyGatewayLevelAuthPolicy(t, ctx, gwNS, gwName, "assayd-e2e-gated-gateway-auth")

	const name = "gwgated"
	applyGateSuite(t, ctx, name+"-suite")
	a := gatedAgent(name, img, name+"-suite", corev1.EnvVar{Name: "ANSWER_MARKER", Value: "gwgated"})
	createAgent(t, ctx, a)

	r1 := a.Spec
	waitAvailable(t, ctx, controller.WorkloadName(name, revision.MustHash(r1)), 4*time.Minute)
	waitForVerdict(t, ctx, name, r1, assaydv1alpha1.EvalVerdictPassed, 4*time.Minute)
	waitForAgent(t, ctx, name, 3*time.Minute, "the revision promoted with EvalPassed", func(a *assaydv1alpha1.Agent) bool {
		return a.Status.ActiveRevision == revision.MustHash(r1) && gatesPassed(a, metav1.ConditionTrue, "EvalPassed")
	})

	held := waitForReason(t, ctx, name, assaydv1alpha1.CondPolicyApplyIncomplete, "GatewayAuthPolicy", 3*time.Minute)
	if !strings.Contains(held.Message, gwNS+"/"+gp.GetName()) {
		t.Errorf("GatewayAuthPolicy does not name the policy: %s", held.Message)
	}
	var live assaydv1alpha1.Agent
	if err := k8s.Get(ctx, types.NamespacedName{Namespace: "assayd-e2e", Name: name}, &live); err != nil {
		t.Fatal(err)
	}
	if tx := live.Status.Auth.Transaction; tx == nil || tx.Kind != "Create" || tx.Stage != "ProbingAfter" ||
		tx.Probe == nil || tx.Probe.After == nil || *tx.Probe.After != 401 {
		t.Errorf("the Create is not held at ProbingAfter on the Gateway's 401: %+v", live.Status.Auth)
	}
	if live.Status.ActiveRevision != revision.MustHash(r1) || !gatesPassed(&live, metav1.ConditionTrue, "EvalPassed") {
		t.Errorf("the held Create took the promotion back: active=%q conditions=%+v",
			live.Status.ActiveRevision, live.Status.Conditions)
	}
	assertRouteUnpublished(t, ctx, name)

	if err := k8s.Delete(ctx, gp.DeepCopy()); err != nil {
		t.Fatalf("remove the Gateway-level policy: %v", err)
	}
	waitForPublishedRoute(t, ctx, name, 3*time.Minute)
	assertServedUnderAPIKey(t, ctx, name)
	assertRouteNames(t, ctx, name, r1)

	gwSvc := gatewayService(t, ctx, gwNS, gwName)
	url := fmt.Sprintf("http://%s.%s.svc.cluster.local:8080", gwSvc, gwNS) + a2aSendMessage
	host := emittedHostname(t, name, "assayd-e2e")
	waitForCode(t, ctx, "gwgated-anon", url, host, sendMessage("anonymous"), "401", 2*time.Minute,
		"an anonymous caller of the published route must be refused by <agent>-auth")
	waitForMarker(t, ctx, "gwgated", url, host, "gwgated")
}

// applyGateSuite writes the one-case suite both rows gate on, as the
// cluster's administrator, who passes assayd-evalsuite-writers.
func applyGateSuite(t *testing.T, ctx context.Context, name string) {
	t.Helper()
	s := &assaydv1alpha1.EvalSuite{ObjectMeta: metav1.ObjectMeta{Namespace: "assayd-e2e", Name: name},
		Spec: assaydv1alpha1.EvalSuiteSpec{Dataset: assaydv1alpha1.EvalDataset{
			Cases: []assaydv1alpha1.EvalCase{gateSuiteCase}}}}
	_ = k8s.Delete(ctx, s.DeepCopy())
	if !waitGone(t, ctx, client.ObjectKeyFromObject(s), time.Minute) {
		t.Fatalf("a previous run's EvalSuite %s is still being deleted", name)
	}
	if err := k8s.Create(ctx, s.DeepCopy()); err != nil {
		t.Fatalf("create EvalSuite %s: %v", name, err)
	}
	t.Cleanup(func() { _ = k8s.Delete(context.Background(), s.DeepCopy()) })
}

// gatedAgent is an API-key Agent (no expose block) running the responder and
// gating on suite.
func gatedAgent(name, img, suite string, env ...corev1.EnvVar) *assaydv1alpha1.Agent {
	return &assaydv1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "assayd-e2e"},
		Spec: assaydv1alpha1.AgentSpec{
			Runtime: &assaydv1alpha1.AgentRuntime{Image: img,
				Env: append([]corev1.EnvVar{{Name: "AGENT_NAME", Value: name}}, env...)},
			Gates: []assaydv1alpha1.GateRef{{EvalSuiteRef: suite}},
		},
	}
}

// createAgent creates a after removing any previous run's, and deletes it on
// cleanup, waiting for it to go so the next run starts clean.
func createAgent(t *testing.T, ctx context.Context, a *assaydv1alpha1.Agent) {
	t.Helper()
	_ = k8s.Delete(ctx, a.DeepCopy())
	if !waitGone(t, ctx, client.ObjectKeyFromObject(a), 2*time.Minute) {
		t.Fatalf("a previous run's Agent %s is still being deleted", a.Name)
	}
	if err := k8s.Create(ctx, a); err != nil {
		t.Fatalf("create agent %s: %v", a.Name, err)
	}
	t.Cleanup(func() { _ = k8s.Delete(context.Background(), a.DeepCopy()) })
}

// editAgent applies edit to the live Agent's spec and returns the spec it
// wrote, from which the test derives the revision the edit mints.
func editAgent(t *testing.T, ctx context.Context, name string, edit func(*assaydv1alpha1.AgentSpec)) assaydv1alpha1.AgentSpec {
	t.Helper()
	var spec assaydv1alpha1.AgentSpec
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var live assaydv1alpha1.Agent
		if err := k8s.Get(ctx, types.NamespacedName{Namespace: "assayd-e2e", Name: name}, &live); err != nil {
			return err
		}
		edit(&live.Spec)
		if err := k8s.Update(ctx, &live); err != nil {
			return err
		}
		spec = live.Spec
		return nil
	}); err != nil {
		t.Fatalf("edit agent %s: %v", name, err)
	}
	return spec
}

// waitForAgent polls the Agent until ok holds, and fails with its status
// otherwise.
func waitForAgent(t *testing.T, ctx context.Context, name string, d time.Duration, what string,
	ok func(*assaydv1alpha1.Agent) bool) *assaydv1alpha1.Agent {
	t.Helper()
	deadline := time.Now().Add(d)
	var a assaydv1alpha1.Agent
	for time.Now().Before(deadline) {
		if err := k8s.Get(ctx, types.NamespacedName{Namespace: "assayd-e2e", Name: name}, &a); err == nil && ok(&a) {
			return &a
		}
		time.Sleep(2 * time.Second)
	}
	var conds []string
	for _, c := range a.Status.Conditions {
		conds = append(conds, fmt.Sprintf("%s=%s/%s: %s", c.Type, c.Status, c.Reason, c.Message))
	}
	t.Fatalf("%s: not reached within %s. phase=%s active=%s candidate=%s eval=%+v auth=%+v conditions:\n%s",
		what, d, a.Status.Phase, a.Status.ActiveRevision, a.Status.CandidateRevision, a.Status.Eval,
		a.Status.Auth, strings.Join(conds, "\n"))
	return nil
}

// waitForVerdict waits until status.eval records verdict for the revision
// spec mints, by its full digest and against the suite in force, with every
// case of the suite recorded. A verdict is the eval controller's alone, so this
// is where a run that sent no case shows.
func waitForVerdict(t *testing.T, ctx context.Context, name string, spec assaydv1alpha1.AgentSpec,
	verdict assaydv1alpha1.EvalVerdict, d time.Duration) *assaydv1alpha1.Agent {
	t.Helper()
	digest := revision.MustDigest(spec)
	return waitForAgent(t, ctx, name, d, fmt.Sprintf("status.eval records %s for revision %s",
		verdict, revision.MustHash(spec)), func(a *assaydv1alpha1.Agent) bool {
		e := a.Status.Eval
		return e != nil && e.RevisionDigest == digest && e.Verdict == verdict && e.SuiteDigest != "" &&
			caseOutcome(e, gateSuiteCase.ID) != ""
	})
}

// caseOutcome is the recorded outcome of case id, or empty.
func caseOutcome(e *assaydv1alpha1.EvalStatus, id string) assaydv1alpha1.EvalCaseOutcome {
	if e == nil {
		return ""
	}
	for _, c := range e.Cases {
		if c.ID == id && c.Outcome != assaydv1alpha1.EvalCaseSending {
			return c.Outcome
		}
	}
	return ""
}

func gatesPassed(a *assaydv1alpha1.Agent, status metav1.ConditionStatus, reason string) bool {
	for _, c := range a.Status.Conditions {
		if c.Type == string(assaydv1alpha1.CondGatesPassed) {
			return c.Status == status && c.Reason == reason
		}
	}
	return false
}

// routeBackends returns the names of the serving route's backendRefs.
func routeBackends(t *testing.T, ctx context.Context, name string) []string {
	t.Helper()
	rt, err := getEmittedRoute(ctx, t, name)
	if err != nil {
		t.Fatalf("read %s's route: %v", name, err)
	}
	var names []string
	rules, _, _ := unstructured.NestedSlice(rt.Object, "spec", "rules")
	for _, rule := range rules {
		refs, _, _ := unstructured.NestedSlice(rule.(map[string]any), "backendRefs")
		for _, ref := range refs {
			n, _, _ := unstructured.NestedString(ref.(map[string]any), "name")
			names = append(names, n)
		}
	}
	return names
}

// assertRouteNames requires the route's one backendRef to name the revision
// spec mints.
func assertRouteNames(t *testing.T, ctx context.Context, name string, spec assaydv1alpha1.AgentSpec) {
	t.Helper()
	want := controller.WorkloadName(name, revision.MustHash(spec))
	if got := routeBackends(t, ctx, name); len(got) != 1 || got[0] != want {
		t.Errorf("%s's route names %v, want exactly [%s]", name, got, want)
	}
}

func waitForRouteNames(t *testing.T, ctx context.Context, name string, spec assaydv1alpha1.AgentSpec, d time.Duration) {
	t.Helper()
	want := controller.WorkloadName(name, revision.MustHash(spec))
	deadline := time.Now().Add(d)
	var got []string
	for time.Now().Before(deadline) {
		if got = routeBackends(t, ctx, name); len(got) == 1 && got[0] == want {
			return
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("%s's route never named %s within %s; it names %v", name, want, d, got)
}

// assertRouteUnpublished requires the route to exist and carry no backendRefs.
func assertRouteUnpublished(t *testing.T, ctx context.Context, name string) {
	t.Helper()
	if got := routeBackends(t, ctx, name); len(got) > 0 {
		t.Fatalf("%s's route was published on a 401 the Gateway-level policy could have answered: %v", name, got)
	}
}

// gatewayAnswer is one answer through the gateway, reduced to what tells
// revisions apart.
type gatewayAnswer struct{ code, state, text, marker string }

var (
	answerCodeRe   = regexp.MustCompile(`HTTP([0-9]{3})`)
	answerStateRe  = regexp.MustCompile(`"state":"([A-Z_]*)"`)
	answerTextRe   = regexp.MustCompile(`"text":"([^"]*)"`)
	answerMarkerRe = regexp.MustCompile(`"marker":"([^"]*)"`)
)

// answersThroughGateway sends n SendMessage requests carrying `probe` through
// the gateway with the permitted key, from ONE pod, every interval, and
// returns each answer. Only the status, the task's state, the first text and
// the marker are kept, because the termination message is capped at 4 KiB.
func answersThroughGateway(t *testing.T, ctx context.Context, tag, url, host string, n int,
	every time.Duration) []gatewayAnswer {
	t.Helper()
	cmd := fmt.Sprintf(
		"for i in $(seq 1 %d); do "+
			"r=$(curl -sS --max-time 10 -X POST -H 'Host: %s' -H 'Authorization: Bearer %s' "+
			"-H 'Content-Type: application/json' -H 'A2A-Version: 1.0' "+
			"-d '{\"message\":{\"messageId\":\"g-'$i'\",\"role\":\"ROLE_USER\",\"parts\":[{\"text\":\"probe\"}]}}' "+
			"-w ' HTTP%%{http_code}' %s 2>&1); "+
			"echo \"$(echo \"$r\" | grep -o 'HTTP[0-9]*$') $(echo \"$r\" | grep -o '\"state\":\"[A-Z_]*\"') "+
			"$(echo \"$r\" | grep -o '\"text\":\"[^\"]*\"' | head -1) $(echo \"$r\" | grep -o '\"marker\":\"[^\"]*\"')\"; "+
			"sleep %d; done > /dev/termination-log; exit 0",
		n, host, permittedKey, url, int(every.Seconds()))
	body := runProbe(t, ctx, tag, cmd)
	var out []gatewayAnswer
	for _, line := range strings.Split(body, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var a gatewayAnswer
		if m := answerCodeRe.FindStringSubmatch(line); m != nil {
			a.code = m[1]
		}
		if m := answerStateRe.FindStringSubmatch(line); m != nil {
			a.state = m[1]
		}
		if m := answerTextRe.FindStringSubmatch(line); m != nil {
			a.text = m[1]
		}
		if m := answerMarkerRe.FindStringSubmatch(line); m != nil {
			a.marker = m[1]
		}
		out = append(out, a)
	}
	if len(out) != n {
		t.Fatalf("%s: %d of %d requests left an answer line; the probe returned %q", tag, len(out), n, body)
	}
	return out
}

// waitForMarker waits until an answer through the gateway with the permitted
// key carries marker. The proxy takes a moment to follow a route the operator
// has written, and the key set to be read.
func waitForMarker(t *testing.T, ctx context.Context, tag, url, host, marker string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Minute)
	var last []gatewayAnswer
	for i := 0; time.Now().Before(deadline); i++ {
		last = answersThroughGateway(t, ctx, fmt.Sprintf("%s-%d", tag, i), url, host, 1, 0)
		if a := last[0]; a.code == "200" && a.state == "TASK_STATE_COMPLETED" && a.marker == marker {
			return
		}
		time.Sleep(5 * time.Second)
	}
	t.Fatalf("no answer through the gateway carried marker %q within three minutes; the last was %+v", marker, last)
}
