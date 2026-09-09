package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/controller"
	"github.com/Quinyte/assayd/internal/revision"
)

// responderImage is built and pushed by hack/e2e.sh and carries a REAL repo
// digest. Unset is a failure and not a skip — this suite's rule, stated at the
// top of hack/e2e.sh, is that a skipped e2e is an untested feature.
func responderImage(t *testing.T) string {
	t.Helper()
	// A skip here is a recorded coverage gap, not a shrug. hack/e2e.sh sets this
	// only for a distro whose cluster it did not create and therefore could not
	// wire a registry into — today that is kind, whose cluster CI's kind-action
	// makes before this script runs. Design 02 §5 carries the gap. Every other
	// absence is still a failure, because this suite's rule is that a skipped
	// e2e is an untested feature.
	if why := os.Getenv("ASSAYD_E2E_RESPONDER_SKIP"); why != "" {
		t.Skipf("responder tests not run: %s", why)
	}
	img := os.Getenv("ASSAYD_E2E_RESPONDER_IMAGE")
	if img == "" {
		t.Fatal("ASSAYD_E2E_RESPONDER_IMAGE is unset. Run `make e2e`, which builds the responder " +
			"and pushes it to the suite's registry so the kubelet can pull it by digest.")
	}
	return img
}

// The test this repository did not have. Every e2e ran registry.k8s.io/pause,
// which serves nothing, so nothing ever asked an agent a question — the finding
// that prompted ADR-0030's scope reset. This runs a real A2A responder through
// the operator and sends it a request.
func TestAnAgentAnswersARequestThroughItsRevisionService(t *testing.T) {
	requireCluster(t)
	requireOperator(t)
	img := responderImage(t)
	ctx := context.Background()
	ensureNamespace(t, ctx, "assayd-e2e")

	const name = "answerer"
	a := &assaydv1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "assayd-e2e"},
		Spec: assaydv1alpha1.AgentSpec{
			Runtime: &assaydv1alpha1.AgentRuntime{
				Image: img,
				Env:   []corev1.EnvVar{{Name: "AGENT_NAME", Value: name}},
			},
		},
	}
	_ = k8s.Delete(ctx, a)
	if err := k8s.Create(ctx, a); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	t.Cleanup(func() { _ = k8s.Delete(context.Background(), a) })

	rev := revision.MustHash(a.Spec)
	wl := controller.WorkloadName(name, rev)
	waitAvailable(t, ctx, wl, 4*time.Minute)

	// It answers through its REVISION Service — the object that did not exist
	// until ADR-0030 step 2, and the one design 03's backendRef will name.
	var s corev1.Service
	if err := k8s.Get(ctx, types.NamespacedName{Namespace: runNS, Name: wl}, &s); err != nil {
		t.Fatalf("the revision has no Service, so nothing can reach it: %v", err)
	}

	url := fmt.Sprintf("http://%s.%s.svc.cluster.local:8080/assayd-test/echo", wl, runNS)
	body := httpInCluster(t, ctx, "ask", url, `{"message":{"parts":[{"text":"ping"}]}}`)

	var out struct {
		Status    struct{ State string }
		Agent     string
		Artifacts []struct {
			Parts []struct{ Text string }
		}
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("the agent's answer is not JSON: %v\n%s", err, body)
	}
	if out.Status.State != "ok" {
		t.Errorf("task state %q; body %s", out.Status.State, body)
	}
	// Naming the responder is what proves WHICH revision answered — the property
	// per-revision Services exist for.
	if out.Agent != name {
		t.Errorf("the answer came from %q, not the agent under test; body %s", out.Agent, body)
	}
	if len(out.Artifacts) == 0 || !strings.Contains(out.Artifacts[0].Parts[0].Text, "ping") {
		t.Errorf("the agent did not echo the request: %s", body)
	}
}

// The card is served by the CONTAINER, which ADR-0019 makes the source of
// truth. The operator does not fetch it yet (design 02 §3.4, owed and in §5) —
// so this proves the container's half is real and gives that fetch something to
// be written against, rather than a fixture invented alongside it.
func TestTheAgentServesItsCardFromTheContainer(t *testing.T) {
	requireCluster(t)
	requireOperator(t)
	img := responderImage(t)
	ctx := context.Background()
	ensureNamespace(t, ctx, "assayd-e2e")

	const name = "carded"
	a := &assaydv1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "assayd-e2e"},
		Spec: assaydv1alpha1.AgentSpec{
			Runtime: &assaydv1alpha1.AgentRuntime{
				Image: img,
				Env:   []corev1.EnvVar{{Name: "AGENT_NAME", Value: name}},
			},
		},
	}
	_ = k8s.Delete(ctx, a)
	if err := k8s.Create(ctx, a); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	t.Cleanup(func() { _ = k8s.Delete(context.Background(), a) })

	rev := revision.MustHash(a.Spec)
	wl := controller.WorkloadName(name, rev)
	waitAvailable(t, ctx, wl, 4*time.Minute)

	url := fmt.Sprintf("http://%s.%s.svc.cluster.local:8080/.well-known/agent-card.json", wl, runNS)
	body := httpInCluster(t, ctx, "card", url, "")

	var c struct {
		Name                string
		SupportedInterfaces []struct{ ProtocolVersion, ProtocolBinding string }
		Skills              []struct{ ID string }
	}
	if err := json.Unmarshal([]byte(body), &c); err != nil {
		t.Fatalf("the card is not parseable, which is the first thing §3.4 will check: %v\n%s", err, body)
	}
	if c.Name != name {
		t.Errorf("the card names %q and the CR names %q; §3.4 fails registration on exactly "+
			"this mismatch", c.Name, name)
	}
	// A2A v1.0 puts the version inside supportedInterfaces[]; a card with a
	// top-level one is the pre-1.0 shape the operator used to parse (A71).
	if len(c.SupportedInterfaces) == 0 || c.SupportedInterfaces[0].ProtocolVersion == "" {
		t.Errorf("the card declares no supportedInterfaces, which A2A v1.0 requires: %s", body)
	}
	if len(c.Skills) == 0 {
		t.Errorf("the card declares no skills: %s", body)
	}
}

func waitAvailable(t *testing.T, ctx context.Context, workload string, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		var dep appsv1.Deployment
		if err := k8s.Get(ctx, types.NamespacedName{Namespace: runNS, Name: workload}, &dep); err == nil {
			if dep.Status.AvailableReplicas > 0 {
				return
			}
		}
		time.Sleep(3 * time.Second)
	}
	var dep appsv1.Deployment
	if err := k8s.Get(ctx, types.NamespacedName{Namespace: runNS, Name: workload}, &dep); err != nil {
		t.Fatalf("no workload %s in %s: %v", workload, runNS, err)
	}
	// Say what went wrong. A responder that will not start is usually an image
	// pull or a PodSecurity mismatch, and both are visible on the Pods.
	var pods corev1.PodList
	_ = k8s.List(ctx, &pods, client.InNamespace(runNS))
	for i := range pods.Items {
		for _, cs := range pods.Items[i].Status.ContainerStatuses {
			if cs.State.Waiting != nil {
				t.Logf("pod %s: %s: %s", pods.Items[i].Name, cs.State.Waiting.Reason, cs.State.Waiting.Message)
			}
		}
	}
	t.Fatalf("workload %s never became available: %d/%d, conditions %+v",
		workload, dep.Status.AvailableReplicas, dep.Status.Replicas, dep.Status.Conditions)
}

// httpInCluster issues one request from inside the cluster and returns the body.
//
// A Pod rather than an exec: the response is written to /dev/termination-log and
// read back off the Pod's status, so this needs only the typed client — no REST
// config, no SPDY, no logs API. It also means the request is made from a Pod
// subject to the same network rules a real caller would be, which an exec into
// the test process's own machine would not be.
func httpInCluster(t *testing.T, ctx context.Context, tag, url, postBody string) string {
	t.Helper()
	cmd := "curl -sS --max-time 20 -o /dev/termination-log " + url
	if postBody != "" {
		cmd = "curl -sS --max-time 20 -X POST -H 'Content-Type: application/json' " +
			"-d '" + postBody + "' -o /dev/termination-log " + url
	}
	name := "probe-" + tag
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "assayd-e2e"},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{{
				Name:    "curl",
				Image:   "curlimages/curl:8.11.1",
				Command: []string{"sh", "-c", cmd},
				// The body is read from here, so it must survive the container exit.
				TerminationMessagePath:   "/dev/termination-log",
				TerminationMessagePolicy: corev1.TerminationMessageReadFile,
			}},
		},
	}
	// A previous run's probe may still be terminating; the name is reused so the
	// pod must actually be gone before the create.
	_ = k8s.Delete(ctx, p)
	gone := time.Now().Add(time.Minute)
	for time.Now().Before(gone) {
		var scratch corev1.Pod
		if err := k8s.Get(ctx, types.NamespacedName{Namespace: "assayd-e2e", Name: name}, &scratch); err != nil {
			break
		}
		time.Sleep(time.Second)
	}
	if err := k8s.Create(ctx, p); err != nil {
		t.Fatalf("create probe pod: %v", err)
	}
	t.Cleanup(func() { _ = k8s.Delete(context.Background(), p) })

	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		var got corev1.Pod
		if err := k8s.Get(ctx, types.NamespacedName{Namespace: "assayd-e2e", Name: name}, &got); err == nil {
			for _, cs := range got.Status.ContainerStatuses {
				if cs.State.Terminated != nil {
					if cs.State.Terminated.ExitCode != 0 {
						t.Fatalf("the request failed inside the cluster (exit %d): %s",
							cs.State.Terminated.ExitCode, cs.State.Terminated.Message)
					}
					return cs.State.Terminated.Message
				}
			}
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("the probe pod never completed")
	return ""
}

// §3.4 end to end: the OPERATOR fetches the card the container serves, and says
// so on the object. The container's half was proven above; this is the half
// that was owed, and the two are separate tests on purpose — a single one would
// pass if either side were faked.
func TestTheOperatorRegistersTheCardItFetched(t *testing.T) {
	requireCluster(t)
	requireOperator(t)
	img := responderImage(t)
	ctx := context.Background()
	ensureNamespace(t, ctx, "assayd-e2e")

	const name = "registered"
	a := &assaydv1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "assayd-e2e"},
		Spec: assaydv1alpha1.AgentSpec{
			Runtime: &assaydv1alpha1.AgentRuntime{
				Image: img,
				Env:   []corev1.EnvVar{{Name: "AGENT_NAME", Value: name}},
			},
		},
	}
	_ = k8s.Delete(ctx, a)
	if err := k8s.Create(ctx, a); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	t.Cleanup(func() { _ = k8s.Delete(context.Background(), a) })

	rev := revision.MustHash(a.Spec)
	waitAvailable(t, ctx, controller.WorkloadName(name, rev), 4*time.Minute)

	// Registration happens after readiness, so poll for it rather than assuming
	// the reconcile that made it available also fetched.
	var live assaydv1alpha1.Agent
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		if err := k8s.Get(ctx, types.NamespacedName{Namespace: "assayd-e2e", Name: name}, &live); err == nil {
			// A REGISTERED card, not merely an entry. A failed attempt also writes
			// one, with an empty digest, so that the retry gate has something that
			// moves to measure from — breaking on any entry meant this test read the
			// first failed attempt and called it a registration.
			if len(live.Status.Cards) > 0 && live.Status.Cards[0].Digest != "" {
				break
			}
		}
		time.Sleep(3 * time.Second)
	}
	if len(live.Status.Cards) == 0 || live.Status.Cards[0].Digest == "" {
		t.Fatalf("the operator never registered a card; cards=%+v phase=%q conditions=%+v",
			live.Status.Cards, live.Status.Phase, live.Status.Conditions)
	}

	c := live.Status.Cards[0]
	if c.Name != name {
		t.Errorf("the recorded card names %q", c.Name)
	}
	if len(c.Digest) != 64 {
		t.Errorf("card digest %q is not a full SHA-256; drift detection compares it", c.Digest)
	}
	if c.RevisionDigest == "" {
		t.Error("the card is not bound to a revision digest, so a chosen 40-bit collision " +
			"could attach it to another projection")
	}
	if c.Signed {
		t.Error("the card is recorded as signed and nothing in this repository signs one")
	}

	var registered, unsigned *metav1.Condition
	for i := range live.Status.Conditions {
		switch live.Status.Conditions[i].Type {
		case string(assaydv1alpha1.CondRegistered):
			registered = &live.Status.Conditions[i]
		case string(assaydv1alpha1.CondCardUnsigned):
			unsigned = &live.Status.Conditions[i]
		}
	}
	if registered == nil || registered.Status != metav1.ConditionTrue {
		t.Errorf("Registered is %+v, want True after a valid card was fetched", registered)
	}
	// Loud rather than silent: nothing signs a card here, and clearing this would
	// claim a verification no code performs.
	if unsigned == nil || unsigned.Status != metav1.ConditionTrue {
		t.Errorf("CardUnsigned is %+v, want True — no card in this cluster is signed", unsigned)
	}
}
