//go:build cluster

// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

// Cluster conformance. Every assertion here was first observed by hand in a
// spike (docs/research/agentgateway-v1.4.1-spike.md) and is pinned so that an
// upstream behaviour change fails a test rather than silently invalidating a
// design sentence.
//
// These are contract tests against agentgateway, not tests of assayd. A failure
// means the world moved; the message names the design sentence that moved with
// it. Run with `make conformance-cluster`, which provisions and tears down.
package conformance

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func kubectl(t *testing.T, args ...string) (string, error) {
	t.Helper()
	out, err := exec.Command("kubectl", args...).CombinedOutput()
	return string(out), err
}

func apply(t *testing.T, manifest string) error {
	t.Helper()
	c := exec.Command("kubectl", "apply", "-f", "-")
	c.Stdin = strings.NewReader(manifest)
	out, err := c.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s", out)
	}
	return nil
}

// conds reads an ancestor-shaped policy status into {type: status} plus the
// ancestor's group/kind/name, which is the discriminator §3.3.2 depends on.
//
// It requires observedGeneration == metadata.generation. Without that it returns
// the PREVIOUS generation's status immediately after a patch — the exact
// stale-true trap §3.3.2 documents, which made a NACK test that should take a
// minute pass in 0.66s by reading a pre-patch converged status.
func conds(t *testing.T, kind, name string) (map[string]string, map[string]string) {
	t.Helper()
	var got map[string]any
	for i := 0; i < 90; i++ {
		out, err := kubectl(t, "get", kind, name, "-n", "default", "-o", "json")
		if err == nil {
			_ = json.Unmarshal([]byte(out), &got)
			gen := float64(-1)
			if md, ok := got["metadata"].(map[string]any); ok {
				if g, ok := md["generation"].(float64); ok {
					gen = g
				}
			}
			if st, ok := got["status"].(map[string]any); ok {
				if anc, ok := st["ancestors"].([]any); ok && len(anc) > 0 {
					a := anc[0].(map[string]any)
					ref, _ := a["ancestorRef"].(map[string]any)
					refs := map[string]string{}
					for k, v := range ref {
						if s, ok := v.(string); ok {
							refs[k] = s
						}
					}
					cs := map[string]string{}
					var raw []map[string]any
					for _, c := range a["conditions"].([]any) {
						cm := c.(map[string]any)
						cs[cm["type"].(string)] = cm["status"].(string)
						raw = append(raw, cm)
					}
					// statusIsCurrent (status.go) owns this decision and is unit-tested
					// there, so the staleness rule is pinned by `make test` rather than
					// only by a run that happens to have a cluster.
					if !statusIsCurrent(raw, int64(gen)) {
						time.Sleep(time.Second)
						continue
					}
					return cs, refs
				}
			}
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("%s/%s never published an ancestor status", kind, name)
	return nil, nil
}

// ensureGateway applies the shared Gateway and its backendRef-less route
// idempotently. Tests called it explicitly rather than depending on execution
// order, so `-run` on a single test still establishes its own preconditions.
func ensureGateway(t *testing.T) {
	t.Helper()
	if err := apply(t, `
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata: {name: conf-gw, namespace: default}
spec:
  gatewayClassName: agentgateway
  listeners: [{name: http, port: 8080, protocol: HTTP, allowedRoutes: {namespaces: {from: Same}}}]
---
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata: {name: conf-route, namespace: default}
spec:
  parentRefs: [{name: conf-gw}]
  rules: [{matches: [{path: {type: PathPrefix, value: /}}]}]`); err != nil {
		t.Fatal(err)
	}
}

// TestPolicyOnAbsentRouteIsAcceptedButNotAttached is the measurement that
// invalidated design 03's original apply barrier: Accepted=True is reported by a
// policy that emitted nothing, and the ordering guarantees that state.
func TestPolicyOnAbsentRouteIsAcceptedButNotAttached(t *testing.T) {
	if err := apply(t, `
apiVersion: agentgateway.dev/v1alpha1
kind: AgentgatewayPolicy
metadata: {name: conf-absent, namespace: default}
spec:
  targetRefs: [{kind: HTTPRoute, name: nope, group: gateway.networking.k8s.io}]
  traffic: {timeouts: {request: 5s}}`); err != nil {
		t.Fatal(err)
	}
	cs, ref := conds(t, "agentgatewaypolicy", "conf-absent")
	if cs["Accepted"] != "True" {
		t.Errorf("Accepted=%q; §3.3.2 exists because a policy with no target still reports True", cs["Accepted"])
	}
	if cs["Attached"] != "False" {
		t.Errorf("Attached=%q; §3.3.2's tuple requires Attached to distinguish this state", cs["Attached"])
	}
	if ref["name"] != "StatusSummary" || ref["group"] != "agentgateway.dev" {
		t.Errorf("failure ancestor is %v; §3.3.2 treats a synthetic StatusSummary ancestor as a negative signal", ref)
	}
}

// TestAttachedPolicyReportsTheRealGatewayAncestor pins the other half: success
// and failure differ in the ANCESTOR, not only the condition.
func TestAttachedPolicyReportsTheRealGatewayAncestor(t *testing.T) {
	ensureGateway(t)
	if err := apply(t, `
apiVersion: agentgateway.dev/v1alpha1
kind: AgentgatewayPolicy
metadata: {name: conf-attached, namespace: default}
spec:
  targetRefs: [{kind: HTTPRoute, name: conf-route, group: gateway.networking.k8s.io}]
  traffic: {timeouts: {request: 5s}}`); err != nil {
		t.Fatal(err)
	}
	cs, ref := conds(t, "agentgatewaypolicy", "conf-attached")
	if cs["Attached"] != "True" {
		t.Errorf("Attached=%q on an existing route; §3.3's PreparingRoute stage exists so this can be reached", cs["Attached"])
	}
	if ref["kind"] != "Gateway" || ref["group"] != "gateway.networking.k8s.io" {
		t.Errorf("success ancestor is %v; §3.3.2 requires a Gateway-shaped ancestorRef, never the targetRef", ref)
	}
}

// TestInertRouteIsAttachableAndAnswers500 pins the shape §3.3's PreparingRoute
// stage uses, and the availability cost §3.3.3 documents.
//
// An earlier version asserted only route STATUS and never sent a request, while
// its name claimed the 500. It would have passed against a gateway that served
// the inert route, which is the opposite of what §3.3.3 documents as the cost of
// a tightening. It owns its own route so filtered execution still establishes
// its preconditions.
func TestInertRouteIsAttachableAndAnswers500(t *testing.T) {
	ensureGateway(t)
	if err := apply(t, `
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata: {name: conf-inert, namespace: default}
spec:
  parentRefs: [{name: conf-gw}]
  rules: [{matches: [{path: {type: PathPrefix, value: /inert}}]}]`); err != nil {
		t.Fatal(err)
	}
	var out string
	for i := 0; i < 30; i++ {
		out, _ = kubectl(t, "get", "httproute", "conf-inert", "-n", "default",
			"-o", "jsonpath={range .status.parents[0].conditions[*]}{.type}={.status} {end}")
		if strings.Contains(out, "Accepted=True") {
			break
		}
		time.Sleep(time.Second)
	}
	for _, want := range []string{"Accepted=True", "ResolvedRefs=True"} {
		if !strings.Contains(out, want) {
			t.Errorf("backendRef-less route status %q lacks %s; §3.3 needs it attachable", out, want)
		}
	}
	// The half the name claimed and the test never checked.
	send := trafficFrom(t)
	if got := send("/inert"); got != 500 {
		t.Errorf("the inert route answered HTTP %d, expected 500; §3.3.3 documents that a "+
			"tightening takes the route DOWN for its convergence window rather than serving the old rule, "+
			"and that cost is only real if the inert shape errors", got)
	}
}

// TestExactlyOneOfIsEnforcedByAdmission is the SEMANTIC half of the hermetic
// ExactlyOneOf assertion. Reading the CEL expression proves the rule is written;
// only the API server proves it is enforced, and §3.5 relies on enforcement when
// it says one policy cannot carry both limits.
func TestExactlyOneOfIsEnforcedByAdmission(t *testing.T) {
	ensureGateway(t)
	err := apply(t, `
apiVersion: agentgateway.dev/v1alpha1
kind: AgentgatewayPolicy
metadata: {name: conf-bothlimits, namespace: default}
spec:
  targetRefs: [{kind: HTTPRoute, name: conf-route, group: gateway.networking.k8s.io}]
  traffic: {rateLimit: {local: [{requests: 10, tokens: 100, unit: Hours}]}}`)
	if err == nil {
		t.Fatal("a policy setting BOTH requests and tokens was admitted; §3.5 says one policy cannot " +
			"carry both, and emits a single -ratelimit at the minimum of the two derived rates on that basis")
	}
	// WHICH error matters. "apply failed" is satisfied by a typo, a missing
	// route, an unreachable API server or a webhook outage, so asserting only
	// that err != nil would keep passing after the constraint was removed.
	if !strings.Contains(err.Error(), "exactly one of the fields in [requests tokens]") {
		t.Errorf("the apply was rejected, but not by the ExactlyOneOf rule this test exists to "+
			"exercise — so it proves nothing about the constraint:\n%v", err)
	}
}

// TestNegativeBurstIsAcceptedEverywhere is why assayd validates burst >= 0
// itself: neither the API server nor the controller rejects it.
func TestNegativeBurstIsAcceptedEverywhere(t *testing.T) {
	ensureGateway(t)
	if err := apply(t, `
apiVersion: agentgateway.dev/v1alpha1
kind: AgentgatewayPolicy
metadata: {name: conf-negburst, namespace: default}
spec:
  targetRefs: [{kind: HTTPRoute, name: conf-route, group: gateway.networking.k8s.io}]
  traffic: {rateLimit: {local: [{tokens: 100, unit: Hours, burst: -1}]}}`); err != nil {
		t.Fatalf("a negative burst was rejected at admission; A10 says assayd must validate it because nothing else does: %v", err)
	}
	cs, _ := conds(t, "agentgatewaypolicy", "conf-negburst")
	if cs["Accepted"] != "True" {
		t.Errorf("controller now rejects burst: -1 (Accepted=%q); A10's assayd-side validation may be redundant", cs["Accepted"])
	}
}

// TestDailyUnitIsRejected is why §3.5 emits an hourly window.
func TestDailyUnitIsRejected(t *testing.T) {
	ensureGateway(t)
	err := apply(t, `
apiVersion: agentgateway.dev/v1alpha1
kind: AgentgatewayPolicy
metadata: {name: conf-days, namespace: default}
spec:
  targetRefs: [{kind: HTTPRoute, name: conf-route, group: gateway.networking.k8s.io}]
  traffic: {rateLimit: {local: [{tokens: 100, unit: Days}]}}`)
	if err == nil {
		t.Error("unit: Days is now accepted; §3.5 emits hourly because a daily window was not expressible")
	}
}

// trafficFrom returns a func that sends one request to the gateway from INSIDE
// the cluster and returns its HTTP code.
//
// An earlier version port-forwarded from the test process. That added three
// failure modes unrelated to what is being measured — a tunnel that is not up
// yet, a gateway Pod still ContainerCreating, and a local port bind — and each
// surfaced as a connection error that reads exactly like a policy result. The
// first version of this test read one as a policy result.
// curlImage is a REAL, PULLABLE digest — `docker manifest inspect
// curlimages/curl:8.11.1`. It is a constant because A21's digest migration
// rewrote every tagged image in the repository with a SYNTHETIC digest: valid
// to CEL, unpullable by a kubelet. This suite's traffic pod then never started,
// and the two tests that send requests timed out at 180s each —
// discovered only when the suite was next run, days later.
const curlImage = "curlimages/curl@sha256:9be9b39ac2a66b98bef6d1e1e615af8ff50ec2e746b9163bc12f2bdbf14a949d"

func trafficFrom(t *testing.T) func(path string) int {
	t.Helper()
	// The gateway's Service is created by the controller alongside its Deployment.
	var svc string
	for i := 0; i < 60; i++ {
		out, err := kubectl(t, "get", "svc", "-n", "default",
			"-l", "gateway.networking.k8s.io/gateway-name=conf-gw", "-o", "jsonpath={.items[0].metadata.name}")
		if err == nil && strings.TrimSpace(out) != "" {
			svc = strings.TrimSpace(out)
			break
		}
		time.Sleep(time.Second)
	}
	if svc == "" {
		t.Fatal("the Gateway never produced a Service; traffic assertions need one")
	}
	_ = apply(t, fmt.Sprintf(`
apiVersion: v1
kind: Pod
metadata: {name: conf-curl, namespace: default}
spec:
  restartPolicy: Never
  containers:
  - name: c
    image: %s
    command: ["sleep", "3600"]`, curlImage))
	if out, err := kubectl(t, "wait", "--for=condition=Ready", "pod/conf-curl", "-n", "default", "--timeout=180s"); err != nil {
		// Say WHY, or this reads as a dataplane problem. The last time this
		// happened the image digest was synthetic and unpullable, and the two
		// traffic tests each burned 180s before failing with nothing to act on.
		ev, _ := kubectl(t, "get", "events", "-n", "default", "--field-selector",
			"involvedObject.name=conf-curl")
		t.Fatalf("curl pod never became Ready, so no traffic assertion in this suite can run: %s\n"+
			"image=%s\nevents:\n%s", out, curlImage, ev)
	}

	url := "http://" + svc + ":8080"
	send := func(path string) int {
		out, err := kubectl(t, "exec", "conf-curl", "-n", "default", "--",
			"curl", "-s", "-o", "/dev/null", "-w", "%{http_code}", "--max-time", "10", url+path)
		if err != nil {
			return -1
		}
		code := 0
		fmt.Sscanf(strings.TrimSpace(out), "%d", &code)
		return code
	}
	// Wait until the data plane answers at all, so a not-yet-programmed gateway is
	// never mistaken for a policy decision.
	for i := 0; i < 60; i++ {
		if c := send("/"); c > 0 {
			return send
		}
		time.Sleep(time.Second)
	}
	t.Fatal("the gateway never answered from inside the cluster")
	return send
}

// TestNackRetainsTheOldConfigAndReportsConverged is the measurement A19 rests
// on, and it must observe TRAFFIC, not only an Event.
//
// STATUS (2026-08-29): observed green. NACK'd loosening -> 429 (still
// rate-limited by the old strict rule); the same loosening applied cleanly ->
// 503 (passed the limiter, no backend endpoints). The control is what makes the
// 429 evidence rather than a coincidence, because a spent bucket predicts 429
// under either hypothesis. If the control ever returns 429 too, this test SKIPS
// as INCONCLUSIVE rather than passing.
//
// An earlier version of this test was named RetainsOldConfig and never
// established old config nor sent a request — it asserted only that an Event
// appeared. It would have passed against a gateway that dropped the old rule
// entirely, which is the opposite of the behaviour A19 depends on.
//
// The sequence: install a STRICT limit and prove it enforces; then patch to a
// LOOSER limit carrying a dataplane-invalid value. If the NACK retains the old
// config the strict rule keeps rejecting; if it were dropped, the loosened rule
// would let traffic through.
func TestNackRetainsTheOldConfigAndReportsConverged(t *testing.T) {
	if err := apply(t, `
apiVersion: v1
kind: Service
metadata: {name: conf-svc, namespace: default}
spec: {ports: [{port: 80, targetPort: 80}], selector: {app: none}}
---
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata: {name: conf-served, namespace: default}
spec:
  parentRefs: [{name: conf-gw}]
  rules: [{matches: [{path: {type: PathPrefix, value: /nack}}], backendRefs: [{name: conf-svc, port: 80}]}]
---
apiVersion: agentgateway.dev/v1alpha1
kind: AgentgatewayPolicy
metadata: {name: conf-poison, namespace: default}
spec:
  targetRefs: [{kind: HTTPRoute, name: conf-served, group: gateway.networking.k8s.io}]
  traffic: {rateLimit: {local: [{requests: 1, unit: Hours}]}}`); err != nil {
		t.Fatal(err)
	}
	if cs, _ := conds(t, "agentgatewaypolicy", "conf-poison"); cs["Attached"] != "True" {
		t.Fatalf("strict policy did not attach: %v", cs)
	}
	send := trafficFrom(t)

	// Prove the STRICT limit actually enforces before relying on its retention.
	first, second := send("/nack"), send("/nack")
	if second != 429 {
		t.Fatalf("the strict 1/hour limit did not enforce (got %d then %d); "+
			"without an enforcing baseline this test cannot distinguish retention from a dropped rule", first, second)
	}

	// Now LOOSEN it, carrying a dataplane-invalid burst in the same edit.
	if _, err := kubectl(t, "patch", "agentgatewaypolicy", "conf-poison", "-n", "default", "--type=merge",
		"-p", `{"spec":{"traffic":{"rateLimit":{"local":[{"requests":1000,"unit":"Hours","burst":-1}]}}}}`); err != nil {
		t.Fatal(err)
	}
	cs, _ := conds(t, "agentgatewaypolicy", "conf-poison")
	if cs["Accepted"] != "True" || cs["Attached"] != "True" {
		t.Fatalf("poisoned policy reports %v; A19 rests on it reporting fully converged while the dataplane rejects it", cs)
	}

	var events string
	for i := 0; i < 60; i++ {
		events, _ = kubectl(t, "get", "events", "-n", "default", "--field-selector", "type=Warning", "-o", "json")
		if strings.Contains(events, "AgentGatewayNackError") {
			break
		}
		time.Sleep(time.Second)
	}
	if !strings.Contains(events, "AgentGatewayNackError") {
		t.Fatal("no AgentGatewayNackError Event; §3.3's NACK watch is the ONLY observable of a rejection and A19 rests on it")
	}
	if !strings.Contains(events, "default/conf-poison") {
		t.Error("the NACK Event no longer names the offending policy; §3.3.2 attributes a NACK by that key")
	}

	// The load-bearing assertion: the OLD strict rule is still rejecting.
	afterNack := send("/nack")

	// A 429 alone does NOT prove retention. It is equally predicted by "the
	// loosened policy applied but the token bucket was not reset", so without a
	// control this assertion cannot distinguish the two — and an earlier version
	// of this test asserted it anyway.
	//
	// The control: apply the same loosening WITHOUT the poison. If a
	// successfully-applied loosening changes observed behaviour, then the
	// poisoned run's 429 means the poisoned policy did not take effect. If the
	// control also stays 429, a policy update does not reset the bucket, the
	// discriminator does not exist at this layer, and the test says so rather
	// than claiming a result.
	if _, err := kubectl(t, "patch", "agentgatewaypolicy", "conf-poison", "-n", "default", "--type=merge",
		"-p", `{"spec":{"traffic":{"rateLimit":{"local":[{"requests":1000,"unit":"Hours"}]}}}}`); err != nil {
		t.Fatal(err)
	}
	if cs, _ := conds(t, "agentgatewaypolicy", "conf-poison"); cs["Attached"] != "True" {
		t.Fatalf("clean loosening did not attach: %v", cs)
	}
	var control int
	for i := 0; i < 30; i++ {
		if control = send("/nack"); control != 429 {
			break
		}
		time.Sleep(time.Second)
	}

	switch {
	case control == 429:
		// FAIL, not Skip. `go test` reports a skip as success, so the gate would
		// exit zero while the claim this test exists to protect went unverified —
		// which is how an inconclusive result becomes an implicit pass.
		t.Fatalf("INCONCLUSIVE: a cleanly-applied loosening still returns 429, so a policy update does "+
			"not reset the bucket and the 429 after the NACK (got %d) cannot distinguish retention from "+
			"a stale bucket. A19's retention claim then rests on the hand measurement in spike §2.8 "+
			"alone, and this gate must go RED so that is noticed rather than assumed.", afterNack)
	case afterNack != 429:
		t.Errorf("after the NACK the loosened limit was serving (HTTP %d) while the control shows a "+
			"clean loosening takes effect (HTTP %d); A19 and §3.3.2 rest on a NACK'd policy RETAINING "+
			"the previous configuration", afterNack, control)
	default:
		t.Logf("VERIFIED: NACK'd loosening kept rejecting (429) while a clean loosening served (HTTP %d)", control)
	}
}
