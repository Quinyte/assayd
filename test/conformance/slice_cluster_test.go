//go:build cluster

// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

// The first slice's cluster cases: design 03 §8.1's "`make conformance-cluster`"
// bullet, measured against agentgateway 1.5.0, the release the operator's e2e
// runs and the one the prepared-route spike measured
// (docs/research/agentgateway-prepared-route-401-spike-2026-09.md).
//
// Every `-auth` policy here is the COMPILER's output, compiler.AuthPolicy, so a
// case pins what the operator would write and not a hand copy of it. Where a
// case needs a policy the slice cannot compile, it starts from that output and
// changes the one field the case is about, and says so. The serving route is
// written here in the emitter's shape (internal/controller/httproute.go,
// servingRouteFor, which is a reconciler method this package cannot call): the
// same name, parentRef section, hostname and `PathPrefix: /` rule.
//
// No operator runs in this suite. What it measures is the gateway the
// operator's transactions rely on, so a failure means agentgateway moved, and
// the message names the design sentence that moved with it.
package conformance

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"

	"github.com/Quinyte/assayd/internal/compiler"
	"github.com/Quinyte/assayd/internal/controller"
)

const (
	// sliceNS plays the run namespace: routes, policies, key sets, backends
	// and the traffic Pod all live here, as they do beside a real Agent.
	sliceNS = "conf-slice"
	// The Gateway gets its own namespace, as in hack/e2e.sh, and admits routes
	// from sliceNS on both listeners.
	sliceGatewayNS = "conf-slice-gw"
	sliceGateway   = "conf-slice"
	// sliceTeam is the Agent's namespace, and so the one group its `-auth`
	// admits (§3.4.4). No namespace of that name needs to exist.
	sliceTeam = "conf-team"
	// servingPort is the port of the listener the serving route attaches to,
	// controller.GatewayListenerName. otherPort is a second listener, `tools`,
	// as the e2e's Gateway has, which the serving route does not attach to.
	servingPort   = 8080
	otherListener = "tools"
	otherPort     = 8081
	// cardPath is the card path every probe requests: the CRD's default for
	// spec.card.path (api/v1alpha1/agent_types.go), which the operator's
	// probe falls back to (§3.3.3).
	cardPath = "/.well-known/agent-card.json"
	// agnhostImage is the backend: `agnhost netexec` answers 200 at any path
	// and its Pod's name at /hostname, so a response says which revision
	// served it. A real, pullable digest (`docker buildx imagetools inspect
	// registry.k8s.io/e2e-test-images/agnhost:2.53`), for curlImage's reason.
	agnhostImage = "registry.k8s.io/e2e-test-images/agnhost@sha256:99c6b4bb4a1e1df3f0b3752168c89358794d02258ebebc26bf21c29399011a85"
	// sliceAGWImage is agentgateway's controller image, whose tag names the
	// release a cluster runs (requireSliceAgentgateway).
	sliceAGWImage = "cr.agentgateway.dev/controller"
	sliceCurlPod  = "conf-slice-curl"
)

// The keys in the slice's key set. Raw keys are literals in a test file, not
// secrets; the ConfigMap holds only their hashes, as agentgateway requires.
const (
	keyTeam    = "conf-team-key"  // group sliceTeam: what `-auth` admits
	keyRogue   = "conf-rogue-key" // a valid key in another group
	keyA       = "conf-a-key"
	keyB       = "conf-b-key"
	keyC       = "conf-c-key"
	keyUnknown = "conf-never-written" // no hash of it is stored anywhere
)

var sliceKeyGroups = map[string]string{
	keyTeam: sliceTeam, keyRogue: "rogue", keyA: "a", keyB: "b", keyC: "c",
}

// runID keeps each run's objects apart, so a re-run against a kept cluster
// (KEEP=1) never meets a route, a policy or a hostname a previous run left.
var runID = strconv.FormatInt(time.Now().Unix()%100000, 36)

func agentName(base string) string { return base + "-" + runID }

func sliceHost(agent string) string {
	return controller.GatewayConfig{HostnameSuffix: controller.DefaultGatewayHostnameSuffix}.
		Hostname(agent, sliceTeam)
}

// keyEntries renders key set entries as agentgateway reads them:
// `{"keyHash": "sha256:<hex>", "metadata": {"group": …}}`, one per key.
func keyEntries(keys map[string]string) string {
	var b strings.Builder
	names := make([]string, 0, len(keys))
	for k := range keys {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		sum := sha256.Sum256([]byte(k))
		fmt.Fprintf(&b, "  %s: '{\"keyHash\":\"sha256:%s\",\"metadata\":{\"group\":%q}}'\n",
			k, hex.EncodeToString(sum[:]), keys[k])
	}
	return b.String()
}

// keySet renders a labelled key ConfigMap: the product's constant selector,
// compiler.APIKeySourceLabel=compiler.APIKeySourceValue, which every emitted
// policy selects.
func keySet(ns, name string, keys map[string]string) string {
	return fmt.Sprintf(`
apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: %s
  labels: {%s: %q}
data:
%s`, name, ns, compiler.APIKeySourceLabel, compiler.APIKeySourceValue, keyEntries(keys))
}

func backend(name string) string {
	return fmt.Sprintf(`
apiVersion: apps/v1
kind: Deployment
metadata: {name: %[1]s, namespace: %[2]s}
spec:
  replicas: 1
  selector: {matchLabels: {app: %[1]s}}
  template:
    metadata: {labels: {app: %[1]s}}
    spec:
      containers:
      - name: netexec
        image: %[3]s
        args: ["netexec", "--http-port=8080"]
        readinessProbe: {httpGet: {path: /, port: 8080}, periodSeconds: 1}
---
apiVersion: v1
kind: Service
metadata: {name: %[1]s, namespace: %[2]s}
spec:
  selector: {app: %[1]s}
  ports: [{port: 80, targetPort: 8080}]`, name, sliceNS, agnhostImage)
}

// gatewayYAML is a Gateway shaped like hack/e2e.sh's: the serving listener,
// named as the operator's routes name it, and a second listener, `tools`.
func gatewayYAML(name string) string {
	return fmt.Sprintf(`
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata: {name: %s, namespace: %s}
spec:
  gatewayClassName: agentgateway
  listeners:
  - name: %s
    port: %d
    protocol: HTTP
    allowedRoutes: {namespaces: {from: Selector, selector: {matchLabels: {kubernetes.io/metadata.name: %s}}}}
  - name: %s
    port: %d
    protocol: HTTP
    allowedRoutes: {namespaces: {from: Selector, selector: {matchLabels: {kubernetes.io/metadata.name: %s}}}}`,
		name, sliceGatewayNS, controller.GatewayListenerName, servingPort, sliceNS,
		otherListener, otherPort, sliceNS)
}

// sliceFixture establishes, idempotently, everything the slice cases share,
// and returns the in-cluster address of the Gateway's Service. Each test calls
// it, so `-run` on one test still sets up its own preconditions.
func sliceFixture(t *testing.T) string {
	t.Helper()
	requireSliceAgentgateway(t)
	if err := apply(t, fmt.Sprintf(`
apiVersion: v1
kind: Namespace
metadata: {name: %s}
---
apiVersion: v1
kind: Namespace
metadata: {name: %s}
---
%s
---
%s
---
%s
---
%s
---
apiVersion: v1
kind: Pod
metadata: {name: %s, namespace: %s}
spec:
  restartPolicy: Never
  containers:
  - name: c
    image: %s
    command: ["sleep", "7200"]`,
		sliceNS, sliceGatewayNS, gatewayYAML(sliceGateway),
		keySet(sliceNS, "conf-slice-keys", sliceKeyGroups),
		backend("conf-rev1"), backend("conf-rev2"),
		sliceCurlPod, sliceNS, curlImage)); err != nil {
		t.Fatal(err)
	}
	return gatewayAddress(t, sliceGateway)
}

// gatewayAddress waits for a Gateway in sliceGatewayNS to be Programmed with
// a running data plane, and for the shared backends and the traffic Pod, then
// returns the Gateway Service's in-cluster name.
func gatewayAddress(t *testing.T, gw string) string {
	t.Helper()
	for _, args := range [][]string{
		{"wait", "--for=condition=Programmed", "gateway/" + gw, "-n", sliceGatewayNS, "--timeout=300s"},
		// Programmed is not a running data plane (hack/e2e.sh). The proxy's
		// Deployment carries the Gateway's name.
		{"rollout", "status", "deploy/" + gw, "-n", sliceGatewayNS, "--timeout=600s"},
		{"rollout", "status", "deploy/conf-rev1", "-n", sliceNS, "--timeout=300s"},
		{"rollout", "status", "deploy/conf-rev2", "-n", sliceNS, "--timeout=300s"},
		{"wait", "--for=condition=Ready", "pod/" + sliceCurlPod, "-n", sliceNS, "--timeout=300s"},
	} {
		if out, err := kubectl(t, args...); err != nil {
			ev, _ := kubectl(t, "get", "events", "-A", "--sort-by=.lastTimestamp")
			t.Fatalf("the slice fixture never became ready at `kubectl %s`: %s\nrecent events:\n%s",
				strings.Join(args, " "), out, tail(ev, 20))
		}
	}
	out, err := kubectl(t, "get", "svc", "-n", sliceGatewayNS,
		"-l", "gateway.networking.k8s.io/gateway-name="+gw, "-o", "jsonpath={.items[0].metadata.name}")
	if err != nil || strings.TrimSpace(out) == "" {
		t.Fatalf("Gateway %s/%s has no Service: %s", sliceGatewayNS, gw, out)
	}
	return strings.TrimSpace(out) + "." + sliceGatewayNS + ".svc.cluster.local"
}

func tail(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// requireSliceAgentgateway fails, never skips, unless the cluster's
// agentgateway controller is the release hack/conformance-cluster.sh installed
// for these cases, CONF_SLICE_AGW_VERSION (1.5.0 today). Run against 1.4.1,
// which the rest of this suite pins, they would measure a release the
// operator's e2e does not run, and a pass would say nothing about the slice.
// Bumping the variable is how an upgrade re-measures them.
func requireSliceAgentgateway(t *testing.T) {
	t.Helper()
	version := os.Getenv("CONF_SLICE_AGW_VERSION")
	if version == "" {
		t.Fatal("CONF_SLICE_AGW_VERSION is not set. The slice cases run only against the " +
			"agentgateway release hack/conformance-cluster.sh installs for them; run them " +
			"through `make conformance-cluster`")
	}
	want := sliceAGWImage + ":v" + version
	out, err := kubectl(t, "get", "deploy", "-n", "agentgateway-system", "-o", "jsonpath={..image}")
	if err != nil || !strings.Contains(out, want) {
		t.Fatalf("the slice cases run against %s, and the cluster's agentgateway controller is %q "+
			"(err %v)", want, out, err)
	}
}

// servingRoute renders an Agent's serving route in the emitter's shape. An
// empty backend renders it PREPARED: parentRefs and no backendRefs (§3.3).
func servingRoute(t *testing.T, agent, backend string) string {
	t.Helper()
	name, err := compiler.ServingRouteName(agent)
	if err != nil {
		t.Fatal(err)
	}
	return routeOn(name, sliceGateway, sliceHost(agent), backend)
}

func routeOn(name, gateway, host, backend string) string {
	refs := ""
	if backend != "" {
		refs = fmt.Sprintf(`
    backendRefs: [{group: "", kind: Service, name: %s, port: 80, weight: 100}]`, backend)
	}
	return fmt.Sprintf(`
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: %s
  namespace: %s
spec:
  parentRefs:
  - {group: gateway.networking.k8s.io, kind: Gateway, name: %s, namespace: %s, sectionName: %s}
  hostnames: [%q]
  rules:
  - matches: [{path: {type: PathPrefix, value: /}}]%s`,
		name, sliceNS, gateway, sliceGatewayNS, controller.GatewayListenerName, host, refs)
}

// authPolicy is the compiler's `<agent>-auth` for an Agent in sliceTeam,
// placed in sliceNS: exactly what the operator would write (§3.4.4).
func authPolicy(t *testing.T, agent string) *unstructured.Unstructured {
	t.Helper()
	p, err := compiler.AuthPolicy(compiler.AuthInput{
		AgentName: agent, AgentNamespace: sliceTeam,
		AgentUID: types.UID("conf-uid-" + agent), RunNamespace: sliceNS,
	})
	if err != nil {
		t.Fatalf("compile the -auth policy for %s: %v", agent, err)
	}
	return p
}

func applyObject(t *testing.T, obj *unstructured.Unstructured) {
	t.Helper()
	body, err := json.Marshal(obj.Object)
	if err != nil {
		t.Fatal(err)
	}
	if err := apply(t, string(body)); err != nil {
		t.Fatalf("apply %s %s/%s: %v", obj.GetKind(), obj.GetNamespace(), obj.GetName(), err)
	}
}

// deleteLater removes an object when the test ends, so one run's routes and
// policies do not accumulate in the gateway's configuration.
func deleteLater(t *testing.T, kind, ns, name string) {
	t.Cleanup(func() {
		_, _ = kubectl(t, "delete", kind, name, "-n", ns, "--ignore-not-found", "--wait=false")
	})
}

// requireAttached waits for the policy's current-generation status and
// requires §3.3.2's Accepted and Attached, so an answer measured next is not
// mistaken for one from a policy that never attached.
func requireAttached(t *testing.T, name string) {
	t.Helper()
	cs, _ := condsIn(t, sliceNS, "agentgatewaypolicy", name)
	if cs["Accepted"] != "True" || cs["Attached"] != "True" {
		t.Fatalf("policy %s/%s reports %v; a case measured on a policy that did not attach "+
			"measures nothing", sliceNS, name, cs)
	}
}

// answer is one request's result.
type answer struct {
	code int
	body string
}

// send sends one GET through the Gateway from inside the cluster, with the
// route's Host header and, when key is not empty, `Authorization: Bearer
// <key>`, where §3.4.4's policy reads a key. A connection that fails is code
// 0, so it can never read as a policy decision.
func send(t *testing.T, gw string, port int, host, path, key string) answer {
	t.Helper()
	args := []string{"exec", sliceCurlPod, "-n", sliceNS, "--", "curl", "-s", "--max-time", "5",
		"-H", "Host: " + host, "-w", "\n%{http_code}"}
	if key != "" {
		args = append(args, "-H", "Authorization: Bearer "+key)
	}
	args = append(args, fmt.Sprintf("http://%s:%d%s", gw, port, path))
	out, _ := kubectl(t, args...)
	i := strings.LastIndex(out, "\n")
	if i < 0 {
		return answer{body: out}
	}
	code, _ := strconv.Atoi(strings.TrimSpace(out[i+1:]))
	return answer{code: code, body: out[:i]}
}

// awaitCode sends the same request once a second until it gets `want`, and
// fails at once on any code outside want and `allowed`: a route that passes
// through a state the design says it cannot must not be waited past. It then
// requires `want` twice more in a row, so a single answer is never the
// evidence. It returns how long `want` took to first appear.
func awaitCode(t *testing.T, gw string, port int, host, path, key string, want int,
	allowed []int, timeout time.Duration, why string) time.Duration {
	t.Helper()
	start := time.Now()
	var seen []int
	for time.Since(start) < timeout {
		got := send(t, gw, port, host, path, key).code
		seen = append(seen, got)
		if got == want {
			took := time.Since(start)
			for i := 0; i < 2; i++ {
				if again := send(t, gw, port, host, path, key).code; again != want {
					t.Fatalf("%s: got %d, then %d on the next request; want %d to hold (answers %v)",
						why, want, again, want, seen)
				}
			}
			return took
		}
		ok := false
		for _, a := range allowed {
			ok = ok || got == a
		}
		if !ok {
			t.Fatalf("%s: got HTTP %d while waiting for %d, which the design does not allow on "+
				"the way (answers %v)", why, got, want, seen)
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("%s: never got %d in %s (answers %v)", why, want, timeout, seen)
	return 0
}

// expectCode sends one request and requires its code.
func expectCode(t *testing.T, gw string, port int, host, path, key string, want int, why string) answer {
	t.Helper()
	a := send(t, gw, port, host, path, key)
	if a.code != want {
		t.Errorf("%s: got HTTP %d, want %d (body %q)", why, a.code, want, tail(a.body, 3))
	}
	return a
}

// ---- (1) the prepared route: 500, then 401 --------------------------------

// TestSliceAPreparedRouteAnswers500Then401OnTheServingListener pins
// `Create`'s probe (§3.3.3) on the listener the operator's route attaches to.
// The spike measured it by hand on the `tools` listener only
// (research/agentgateway-prepared-route-401-spike-2026-09.md). On a route with
// parentRefs and no backendRefs, an anonymous request must get 500 before
// `<agent>-auth` attaches and 401 after it, and nothing else on the way. If an
// upgrade put routing before authentication, the probe would see 500 forever
// and every new Agent would stay unpublished; if it answered 401 before the
// policy, the probe could pass early.
func TestSliceAPreparedRouteAnswers500Then401OnTheServingListener(t *testing.T) {
	gw := sliceFixture(t)
	agent := agentName("prep")
	route, _ := compiler.ServingRouteName(agent)
	host := sliceHost(agent)
	if err := apply(t, servingRoute(t, agent, "")); err != nil {
		t.Fatal(err)
	}
	deleteLater(t, "httproute", sliceNS, route)

	// 404 only until the route is programmed.
	awaitCode(t, gw, servingPort, host, cardPath, "", 500, []int{404}, 2*time.Minute,
		"a prepared route with no policy (§3.3, spike §2.6)")

	p := authPolicy(t, agent)
	applyObject(t, p)
	deleteLater(t, "agentgatewaypolicy", sliceNS, p.GetName())
	requireAttached(t, p.GetName())
	took := awaitCode(t, gw, servingPort, host, cardPath, "", 401, []int{500},
		controller.AuthTransactionDeadline,
		"`Create`'s anonymous probe on the prepared route once <agent>-auth is written (§3.3.3)")
	// Not a latency. awaitCode probes once a second through `kubectl exec`,
	// so `took` is at most one probe's round trip when the first probe after
	// Attached is already refused. It bounds the wait from above and no more.
	t.Logf("prepared route: refused within %s of the policy reporting Attached (an upper bound: "+
		"one probe round trip at 1 s cadence)", took.Round(time.Millisecond))

	expectCode(t, gw, servingPort, host, cardPath, keyUnknown, 401,
		"an unknown key on the prepared route (the spike's second row)")
	expectCode(t, gw, servingPort, host, cardPath, keyTeam, 500,
		"a valid key in the admitted group on the prepared route: authenticated, authorised, then "+
			"no backend. The research note measured 500, which is why the probe proves "+
			"authentication and not the group rule (§3.3.3)")
}

// ---- (2) the published route: 200, then 401, and how long it takes ---------

// codeStream is a tight loop of anonymous requests run inside the traffic Pod,
// each answer's code stamped when it reaches the test.
type codeStream struct {
	codes  chan stamped
	stop   func()
	stopMu sync.Once

	mu          sync.Mutex
	n           int
	first, last time.Time
}

// interval is the mean time between answers so far: the loop's resolution.
func (s *codeStream) interval() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.n < 2 {
		return 0
	}
	return s.last.Sub(s.first) / time.Duration(s.n-1)
}

type stamped struct {
	code int
	at   time.Time
}

// streamBatch is how many requests one `curl` process sends before the loop
// checks its stop file.
const streamBatch = 500

// startStream sends anonymous requests back to back from the traffic Pod to
// one host. The loop ends when a stop file appears or after `bound`, so a
// killed test never leaves it running.
//
// One `curl` process sends a batch of requests through a URL glob, each on a
// NEW connection (`Connection: close`), so a policy the proxy applies per
// connection still shows on the next request. An earlier version started one
// `curl` per request, and its ~65 ms of process start was the resolution of
// every latency this measured (the second review of A74).
func startStream(t *testing.T, gw, host, tag string, bound time.Duration) *codeStream {
	t.Helper()
	stopFile := "/tmp/stop-" + tag
	script := fmt.Sprintf(`rm -f %[1]s; end=$(( $(date +%%s) + %[2]d )); `+
		`while [ ! -f %[1]s ] && [ "$(date +%%s)" -lt "$end" ]; do `+
		`curl -s -H 'Connection: close' -H 'Host: %[3]s' -o /dev/null -w '%%{http_code}\n' --max-time 2 `+
		`'http://%[4]s:%[5]d%[6]s?n=[1-%[7]d]'; done`,
		stopFile, int(bound.Seconds()), host, gw, servingPort, cardPath, streamBatch)
	cmd := exec.Command("kubectl", "exec", sliceCurlPod, "-n", sliceNS, "--", "sh", "-c", script)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start the request loop: %v", err)
	}
	s := &codeStream{codes: make(chan stamped, 1<<16)}
	go func() {
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			code, _ := strconv.Atoi(strings.TrimSpace(sc.Text()))
			now := time.Now()
			s.mu.Lock()
			if s.n == 0 {
				s.first = now
			}
			s.n, s.last = s.n+1, now
			s.mu.Unlock()
			s.codes <- stamped{code: code, at: now}
		}
		close(s.codes)
	}()
	s.stop = func() {
		s.stopMu.Do(func() {
			_, _ = kubectl(t, "exec", sliceCurlPod, "-n", sliceNS, "--", "touch", stopFile)
			done := make(chan struct{})
			go func() { _ = cmd.Wait(); close(done) }()
			select {
			case <-done:
			case <-time.After(15 * time.Second):
				_ = cmd.Process.Kill()
			}
		})
	}
	t.Cleanup(s.stop)
	return s
}

// next returns the next answer, or false when none arrives within timeout.
func (s *codeStream) next(timeout time.Duration) (stamped, bool) {
	select {
	case a, ok := <-s.codes:
		return a, ok
	case <-time.After(timeout):
		return stamped{}, false
	}
}

// drain discards answers already received, so what is read next was answered
// after this call.
func (s *codeStream) drain() {
	for {
		select {
		case <-s.codes:
		default:
			return
		}
	}
}

// lockTrials is how many published routes the latency case locks. Each is a
// new Agent, route and hostname, so no trial inherits another's state.
const lockTrials = 10

// TestSliceAPublishedRouteLocksFrom200To401 pins what `Lock` rests on (J2,
// §3.3.3): on a route that is already SERVING, an anonymous request goes from
// the backend's 200 to 401 once `<agent>-auth` is written onto it, and passes
// through nothing else — no 5xx, no 404 — because the route keeps its
// backendRefs throughout. It also measures how long that takes, which nothing
// had measured: §3.3.3's 4-minute deadline for `Lock` rested on a
// prepared-route measurement and a test tolerance. The numbers are recorded
// in docs/research/agentgateway-published-route-lock-latency-2026-09.md.
//
// **What the figure is.** The time from the START of the policy write to the
// arrival of the first 401. The API server commits somewhere inside the
// write, so this is an upper bound on commit-to-enforcement, loose by the
// write's own duration (also logged), the loop's interval, and the streaming
// delay. The time from the write's RETURN is logged too, and can be negative:
// the proxy may enforce before the response reaches the test.
//
// An earlier version wrote with `kubectl apply` straight after an answer
// arrived, from a loop that started one `curl` per request. Its figures were
// that loop's rhythm, and a gateway faster than `kubectl` would have failed
// the trial (the second review of A74). Now the write is one client-go
// request after a random pause, the loop sends a request every few
// milliseconds, and a 401 is accepted whenever it arrives.
func TestSliceAPublishedRouteLocksFrom200To401(t *testing.T) {
	gw := sliceFixture(t)
	pc := policyClient(t)
	var fromStart, fromReturn, writes, intervals []time.Duration
	for i := 1; i <= lockTrials; i++ {
		agent := agentName(fmt.Sprintf("lock%d", i))
		route, _ := compiler.ServingRouteName(agent)
		host := sliceHost(agent)
		if err := apply(t, servingRoute(t, agent, "conf-rev1")); err != nil {
			t.Fatal(err)
		}
		deleteLater(t, "httproute", sliceNS, route)
		awaitCode(t, gw, servingPort, host, cardPath, "", 200, []int{404}, 2*time.Minute,
			"the published route before any policy")

		s := startStream(t, gw, host, fmt.Sprintf("lock-%s-%d", runID, i),
			controller.AuthTransactionDeadline+time.Minute)
		// The loop is running and the route is open: three 200s in a row.
		for run := 0; run < 3; {
			a, ok := s.next(30 * time.Second)
			if !ok {
				t.Fatalf("trial %d: the request loop produced nothing", i)
			}
			if a.code == 200 {
				run++
			} else {
				run = 0
			}
		}
		p := authPolicy(t, agent)
		warm(t, pc)
		// A random pause, so the write does not start at a fixed phase of the
		// loop: straight after an answer arrived, the next one was always one
		// interval away, and that interval was what an earlier version measured.
		randomPause(250 * time.Millisecond)
		s.drain()
		begin := time.Now()
		if _, err := pc.Create(context.Background(), p, metav1.CreateOptions{}); err != nil {
			t.Fatalf("trial %d: write the policy: %v", i, err)
		}
		written := time.Now()
		deleteLater(t, "agentgatewaypolicy", sliceNS, p.GetName())

		var after int
		var first time.Time
		for first.IsZero() {
			a, ok := s.next(controller.AuthTransactionDeadline - time.Since(begin))
			if !ok {
				t.Fatalf("trial %d: no anonymous 401 within §3.3.3's %s deadline after the policy "+
					"write (%d answers after it, all 200): `Lock` would report AuthLockUnverified "+
					"here", i, controller.AuthTransactionDeadline, after)
			}
			switch a.code {
			case 401:
				// Whenever it arrives. No policy existed before the write
				// began, so a 401 is the write's, even one that beats the
				// write's response to the test.
				first = a.at
			case 200:
				if a.at.After(written) {
					after++
				}
			default:
				t.Fatalf("trial %d: an anonymous request got HTTP %d while the policy was being "+
					"applied to a serving route. `Lock` keeps the route's backendRefs "+
					"throughout (§3.3.3), so only the backend's 200 or the policy's 401 may "+
					"appear", i, a.code)
			}
		}
		// Once refused, refused: the next twenty answers are all 401.
		for n := 0; n < 20; n++ {
			a, ok := s.next(10 * time.Second)
			if !ok {
				t.Fatalf("trial %d: the request loop stopped after the first 401", i)
			}
			if a.code != 401 {
				t.Fatalf("trial %d: after the first 401, answer %d was HTTP %d; a lock that "+
					"flaps would let the probe pass while callers still get through", i, n+1, a.code)
			}
		}
		s.stop()
		expectCode(t, gw, servingPort, host, cardPath, keyTeam, 200,
			fmt.Sprintf("trial %d: a key in the Agent's namespace group after the lock", i))

		fromStart = append(fromStart, first.Sub(begin))
		fromReturn = append(fromReturn, first.Sub(written))
		writes = append(writes, written.Sub(begin))
		intervals = append(intervals, s.interval())
		t.Logf("LOCK-LATENCY trial=%d from_write_start_ms=%.1f from_write_return_ms=%.1f "+
			"write_ms=%.1f probe_interval_ms=%.2f anonymous_200s_after_write=%d",
			i, ms(first.Sub(begin)), ms(first.Sub(written)), ms(written.Sub(begin)), ms(s.interval()), after)
	}

	for _, d := range [][]time.Duration{fromStart, fromReturn, writes, intervals} {
		sortDurations(d)
	}
	last := len(fromStart) - 1
	t.Logf("LOCK-LATENCY n=%d from_write_start: min_ms=%.1f median_ms=%.1f max_ms=%.1f; "+
		"from_write_return: min_ms=%.1f median_ms=%.1f max_ms=%.1f; write: median_ms=%.1f; "+
		"probe_interval: median_ms=%.2f; deadline=%s",
		len(fromStart), ms(fromStart[0]), ms(median(fromStart)), ms(fromStart[last]),
		ms(fromReturn[0]), ms(median(fromReturn)), ms(fromReturn[last]), ms(median(writes)),
		ms(median(intervals)), controller.AuthTransactionDeadline)
}

func sortDurations(d []time.Duration) { sort.Slice(d, func(a, b int) bool { return d[a] < d[b] }) }

// median of a sorted, non-empty slice.
func median(d []time.Duration) time.Duration { return (d[(len(d)-1)/2] + d[len(d)/2]) / 2 }

// ---- §8.1 cluster case 2: two groups --------------------------------------

// TestSliceATwoGroupExpressionAdmitsBothGroups measures the compiler's own
// multi-group rule at the gateway (§3.4.4: "standard CEL but unmeasured at this
// gateway"). The policy is compiler.AuthPolicy with its one match expression
// replaced by compiler.AdmitGroupsExpression's for two groups — the slice has
// no `allowedGroups`, so no Agent compiles to two groups yet. Keys in both
// groups are admitted; a key in a third is refused with 403, not 401, because
// it authenticates.
func TestSliceATwoGroupExpressionAdmitsBothGroups(t *testing.T) {
	gw := sliceFixture(t)
	expr, err := compiler.AdmitGroupsExpression([]string{"b", "a"})
	if err != nil {
		t.Fatal(err)
	}
	if want := `apiKey.group == "a" || apiKey.group == "b"`; expr != want {
		t.Fatalf("the compiler rendered %q for groups b and a; §3.4.4 says %q", expr, want)
	}
	agent := agentName("groups")
	route, _ := compiler.ServingRouteName(agent)
	host := sliceHost(agent)
	if err := apply(t, servingRoute(t, agent, "conf-rev1")); err != nil {
		t.Fatal(err)
	}
	deleteLater(t, "httproute", sliceNS, route)
	awaitCode(t, gw, servingPort, host, cardPath, "", 200, []int{404}, 2*time.Minute, "the open route")

	p := authPolicy(t, agent)
	if err := unstructured.SetNestedStringSlice(p.Object, []string{expr},
		"spec", "traffic", "authorization", "policy", "matchExpressions"); err != nil {
		t.Fatal(err)
	}
	applyObject(t, p)
	deleteLater(t, "agentgatewaypolicy", sliceNS, p.GetName())
	requireAttached(t, p.GetName())
	awaitCode(t, gw, servingPort, host, cardPath, "", 401, []int{200}, controller.AuthTransactionDeadline,
		"the two-group policy enforcing")

	expectCode(t, gw, servingPort, host, cardPath, keyA, 200, "a key in group a, the first of two")
	expectCode(t, gw, servingPort, host, cardPath, keyB, 200,
		"a key in group b, the second of two: the `||` must admit both, not only the first term")
	expectCode(t, gw, servingPort, host, cardPath, keyC, 403, "a valid key in group c, admitted by neither term")
	expectCode(t, gw, servingPort, host, cardPath, keyTeam, 403,
		"a valid key in the Agent's namespace group, which this rule does not name")
}

// ---- §8.1 cluster case 1: one per-Agent policy across a promotion ---------

// TestSliceOnePerAgentPolicyCoversAPromotion measures the gateway half of
// §8.1's cluster case 1: the per-Agent `-auth` targets the ROUTE, so a
// promotion — the route's one backendRef moving, in place, from one
// revision's Service to the next — leaves that policy enforcing on the new
// revision. Anonymous requests sent throughout the promotion never reach
// either revision.
//
// What it does not exercise: the route is locked first and promoted after,
// with a single backendRef. A `Lock` racing a promotion, which §1.1 argues
// needs no ordering, and a weighted two-backend shift, are not measured here.
//
// The rest of case 1 is the operator's: that it emits exactly one traffic
// policy, and that a second is reported as ForeignTrafficPolicy
// (internal/controller/authforeign.go). No operator runs here, and this test
// writes the only policy, so asserting either would test the fixture. envtest
// pins them against the real CRD (A72).
func TestSliceOnePerAgentPolicyCoversAPromotion(t *testing.T) {
	gw := sliceFixture(t)
	agent := agentName("promo")
	route, _ := compiler.ServingRouteName(agent)
	host := sliceHost(agent)
	if err := apply(t, servingRoute(t, agent, "conf-rev1")); err != nil {
		t.Fatal(err)
	}
	deleteLater(t, "httproute", sliceNS, route)
	awaitCode(t, gw, servingPort, host, cardPath, "", 200, []int{404}, 2*time.Minute, "the open route")
	p := authPolicy(t, agent)
	applyObject(t, p)
	deleteLater(t, "agentgatewaypolicy", sliceNS, p.GetName())
	requireAttached(t, p.GetName())
	awaitCode(t, gw, servingPort, host, cardPath, "", 401, []int{200}, controller.AuthTransactionDeadline,
		"the per-Agent policy enforcing before the promotion")
	if b := expectCode(t, gw, servingPort, host, "/hostname", keyTeam, 200, "a keyed request before the promotion").body; !strings.HasPrefix(b, "conf-rev1-") {
		t.Fatalf("before the promotion a keyed request was served by %q, not revision conf-rev1", b)
	}

	s := startStream(t, gw, host, "promo-"+runID, 5*time.Minute)
	if a, ok := s.next(30 * time.Second); !ok {
		t.Fatal("the request loop produced nothing")
	} else if a.code != 401 {
		t.Fatalf("before the promotion an anonymous request got HTTP %d; the route must be locked "+
			"before anything below measures it", a.code)
	}
	promote(t, agent, route)

	deadline := time.Now().Add(2 * time.Minute)
	served := ""
	for time.Now().Before(deadline) {
		served = send(t, gw, servingPort, host, "/hostname", keyTeam).body
		if strings.HasPrefix(served, "conf-rev2-") {
			break
		}
		time.Sleep(time.Second)
	}
	if !strings.HasPrefix(served, "conf-rev2-") {
		t.Fatalf("a keyed request was still served by %q two minutes after the promotion", served)
	}
	// Keep the anonymous loop running past the moment the new revision was
	// first seen, so the window covers the proxy's switch on both sides.
	time.Sleep(5 * time.Second)
	s.stop()
	var anon int
	for a := range s.codes {
		anon++
		if a.code != 401 {
			t.Fatalf("anonymous request %d during the promotion got HTTP %d: a revision was "+
				"reachable without a key while the route moved to it", anon, a.code)
		}
	}
	// Enough answers that "all refused" means something: at the loop's rate,
	// several seconds of requests either side of the switch.
	if anon < 30 {
		t.Fatalf("only %d anonymous requests were answered across the promotion; too few to "+
			"say the route was never open", anon)
	}
	t.Logf("promotion: %d anonymous requests across it, every one refused with 401", anon)
	expectCode(t, gw, servingPort, host, cardPath, "", 401, "an anonymous request after the promotion")
}

// promote is the promotion: the route's one backendRef moves to the next
// revision's Service, in place, as design 20 shifts it on "the serving
// HTTPRoute", singular (internal/compiler/naming.go, ServingRouteName).
func promote(t *testing.T, agent, route string) {
	t.Helper()
	if out, err := kubectl(t, "patch", "httproute", route, "-n", sliceNS, "--type=json", "-p",
		`[{"op":"replace","path":"/spec/rules/0/backendRefs/0/name","value":"conf-rev2"}]`); err != nil {
		t.Fatalf("promote %s: %s", agent, out)
	}
}

// ---- §8.1 cluster case 5: an explicit Strict ------------------------------

// TestSliceAnExplicitStrictBehavesAsTheDefault measures the one field the
// compiler emits that the e2e's hand-authored policy never did: `mode:
// Strict` (§3.4.4: "the explicit form is unmeasured at the gateway"). The
// compiler's policy and the same policy with `mode` removed must give the same
// four answers, and the API server must store the defaulted one as Strict.
func TestSliceAnExplicitStrictBehavesAsTheDefault(t *testing.T) {
	gw := sliceFixture(t)
	results := map[string][4]int{}
	for _, variant := range []string{"explicit", "defaulted"} {
		agent := agentName("strict-" + variant)
		route, _ := compiler.ServingRouteName(agent)
		host := sliceHost(agent)
		if err := apply(t, servingRoute(t, agent, "conf-rev1")); err != nil {
			t.Fatal(err)
		}
		deleteLater(t, "httproute", sliceNS, route)
		awaitCode(t, gw, servingPort, host, cardPath, "", 200, []int{404}, 2*time.Minute, variant+": the open route")
		p := authPolicy(t, agent)
		if mode, _, _ := unstructured.NestedString(p.Object, "spec", "traffic", "apiKeyAuthentication", "mode"); variant == "explicit" && mode != "Strict" {
			t.Fatalf("the compiler emitted mode %q; §3.4.4 emits Strict", mode)
		}
		if variant == "defaulted" {
			unstructured.RemoveNestedField(p.Object, "spec", "traffic", "apiKeyAuthentication", "mode")
		}
		applyObject(t, p)
		deleteLater(t, "agentgatewaypolicy", sliceNS, p.GetName())
		requireAttached(t, p.GetName())
		awaitCode(t, gw, servingPort, host, cardPath, "", 401, []int{200}, controller.AuthTransactionDeadline,
			variant+": the policy enforcing")
		results[variant] = [4]int{
			send(t, gw, servingPort, host, cardPath, "").code,
			send(t, gw, servingPort, host, cardPath, keyUnknown).code,
			send(t, gw, servingPort, host, cardPath, keyTeam).code,
			send(t, gw, servingPort, host, cardPath, keyRogue).code,
		}
		if variant == "defaulted" {
			stored, _ := kubectl(t, "get", "agentgatewaypolicy", p.GetName(), "-n", sliceNS,
				"-o", "jsonpath={.spec.traffic.apiKeyAuthentication.mode}")
			if stored != "Strict" {
				t.Errorf("a policy written with no mode is stored with mode %q; §3.4.4 says the "+
					"CRD defaults it to Strict", stored)
			}
		}
	}
	want := [4]int{401, 401, 200, 403}
	for variant, got := range results {
		if got != want {
			t.Errorf("%s mode: anonymous, unknown key, admitted key, wrong-group key got %v; want %v",
				variant, got, want)
		}
	}
}
