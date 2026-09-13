//go:build cluster

// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package conformance

import (
	"fmt"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/Quinyte/assayd/internal/compiler"
	"github.com/Quinyte/assayd/internal/controller"
)

// publishedWithAuth writes an Agent's serving route, published on conf-rev1,
// waits for it to serve, then writes the compiler's `<agent>-auth` and waits
// for it to refuse an anonymous request. It returns the route's hostname.
func publishedWithAuth(t *testing.T, gw, agent string) string {
	t.Helper()
	route, _ := compiler.ServingRouteName(agent)
	host := sliceHost(agent)
	if err := apply(t, servingRoute(t, agent, "conf-rev1")); err != nil {
		t.Fatal(err)
	}
	deleteLater(t, "httproute", sliceNS, route)
	awaitCode(t, gw, servingPort, host, cardPath, "", 200, []int{404}, 2*time.Minute, agent+": the open route")
	p := authPolicy(t, agent)
	applyObject(t, p)
	deleteLater(t, "agentgatewaypolicy", sliceNS, p.GetName())
	requireAttached(t, p.GetName())
	awaitCode(t, gw, servingPort, host, cardPath, "", 401, []int{200}, controller.AuthTransactionDeadline,
		agent+": <agent>-auth enforcing")
	return host
}

func codes(t *testing.T, n int, gw string, port int, host, key string) []int {
	t.Helper()
	var out []int
	for i := 0; i < n; i++ {
		out = append(out, send(t, gw, port, host, cardPath, key).code)
	}
	return out
}

func all(cs []int, want int) bool {
	for _, c := range cs {
		if c != want {
			return false
		}
	}
	return len(cs) > 0
}

// settle waits until n answers in a row with one key are the same code, and
// returns them. A key set or a policy change reaches the proxy asynchronously,
// and a case must not measure the moment before it lands.
func settle(t *testing.T, gw string, port int, host, key string, n int, timeout time.Duration) []int {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var got []int
	for time.Now().Before(deadline) {
		got = codes(t, n, gw, port, host, key)
		if all(got, got[0]) {
			return got
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("the answers to key %q never settled in %s: last %v", key, timeout, got)
	return nil
}

// ---- §8.1 cluster case 3: a key set in another namespace ------------------

// TestSliceAKeySetInAnotherNamespaceDoesNotAdmit measures the reach of
// `configMapSelector`, which the CRD does not state (§3.4.4: "That is
// unmeasured"). A ConfigMap carrying the product's key-source label, in a
// namespace other than the policy's, holds a key in the one group the policy
// admits. That key gets 401, as an unknown key does: the selector reached
// only the policy's own namespace. A key in the policy's namespace, in the
// same group, is admitted in the same round, so the 401 is not a policy that
// refuses everything.
//
// It is what §3.4.4's tenancy sentence waited on: a labelled ConfigMap in
// another namespace mints no principal for this policy. It measures one
// other namespace on one release; it does not prove that no selector ever
// reaches further.
func TestSliceAKeySetInAnotherNamespaceDoesNotAdmit(t *testing.T) {
	gw := sliceFixture(t)
	const elsewhere = "conf-elsewhere"
	const keyElsewhere = "conf-elsewhere-key"
	// Written before the policy, so a selector that did reach it would have it
	// from the start.
	if err := apply(t, fmt.Sprintf(`
apiVersion: v1
kind: Namespace
metadata: {name: %s}
---
%s`, elsewhere, keySet(elsewhere, "conf-elsewhere-keys", map[string]string{keyElsewhere: sliceTeam}))); err != nil {
		t.Fatal(err)
	}
	host := publishedWithAuth(t, gw, agentName("xns"))
	if got := settle(t, gw, servingPort, host, keyTeam, 3, time.Minute); got[0] != 200 {
		t.Fatalf("a key in the policy's own namespace, in its group, got %v; the control must be "+
			"admitted, or this case measures a policy that refuses everyone", got)
	}
	// A canary in the policy's own namespace, written AFTER the other
	// namespace's key set. Once the gateway admits it, the key sets have been
	// read at least up to that write, so a selector that reached the other
	// namespace would have its key by now. Without it, five quick 401s could be
	// a key set not yet read. That rests on the controller reading ConfigMap
	// events in order, which one cluster-wide watch delivers; it is not
	// measured separately.
	canary := "conf-xns-canary-" + runID
	if err := apply(t, keySet(sliceNS, "conf-xns-canary", map[string]string{canary: sliceTeam})); err != nil {
		t.Fatal(err)
	}
	deleteLater(t, "configmap", sliceNS, "conf-xns-canary")
	if got := settle(t, gw, servingPort, host, canary, 3, 2*time.Minute); got[0] != 200 {
		t.Fatalf("the canary written after the other namespace's key set got %v, so the key sets "+
			"were never read and nothing below measures the other namespace's", got)
	}
	if got := codes(t, 5, gw, servingPort, host, keyElsewhere); !all(got, 401) {
		t.Errorf("a key in the admitted group, stored only in namespace %s, got %v; want 401 every "+
			"time. Anything else means configMapSelector reaches another namespace, and §3.4.4's "+
			"group rule is no tenancy boundary: a labelled ConfigMap anywhere mints a principal",
			elsewhere, got)
	}
}

// ---- §8.1 cluster case 4: a duplicate keyHash -----------------------------

// dupWrites is how many times the duplicate is rewritten. Each write swaps
// which ConfigMap gives the key which group, and every second pair of writes
// reverses the order the two ConfigMaps are written in, so a winner chosen by
// name and one chosen by write order give different answers.
const dupWrites = 8

// dupAnswers is how many answers are taken once a write has landed. All must
// agree.
const dupAnswers = 30

// TestSliceADuplicateKeyHashIsNotRefused measures the CRD's "the behavior is
// undefined" for one key hash stored in two selected ConfigMaps under two
// groups (§3.4.4: "writing a known key's hash under another group re-groups
// that credential rather than adding one").
//
// Each write carries a CANARY in each ConfigMap: a key of its own, new with
// every write, in the admitted group. The write is taken as landed only once
// both canaries are admitted. The first version of this case slept ten
// seconds instead, and its answers could come from a write that had not
// landed. An independent review of this change found it.
//
// Asserted: the duplicate is not refused, and the key never gets 401; once a
// write has landed, every one of dupAnswers answers is the same, admitted as
// one group or refused as the other; and across the writes the key is
// admitted at least once and refused at least once, so a duplicate does move a
// credential into and out of the admitted group. WHICH ConfigMap wins is
// logged, by name and by write order, and not asserted: the CRD calls it
// undefined, so it may change without notice.
func TestSliceADuplicateKeyHashIsNotRefused(t *testing.T) {
	gw := sliceFixture(t)
	const keyDup = "conf-dup-key"
	deleteLater(t, "configmap", sliceNS, "conf-dup-a")
	deleteLater(t, "configmap", sliceNS, "conf-dup-b")
	canary := func(cm string, i int) string { return fmt.Sprintf("conf-canary-%s-%s-%d", cm, runID, i) }
	writeOne := func(cm, group string, i int) {
		t.Helper()
		if err := apply(t, keySet(sliceNS, "conf-dup-"+cm,
			map[string]string{keyDup: group, canary(cm, i): sliceTeam})); err != nil {
			t.Fatalf("the API server refused a duplicate key hash in ConfigMap conf-dup-%s: %v", cm, err)
		}
	}

	var host string
	var admitted, refused int
	byName := map[string]int{}
	byOrder := map[string]int{}
	for i := 0; i < dupWrites; i++ {
		groups := map[string]string{"a": sliceTeam, "b": "rogue"}
		if i%2 == 1 {
			groups = map[string]string{"a": "rogue", "b": sliceTeam}
		}
		order := []string{"a", "b"}
		if (i/2)%2 == 1 {
			order = []string{"b", "a"}
		}
		for _, cm := range order {
			writeOne(cm, groups[cm], i)
		}
		if host == "" {
			host = publishedWithAuth(t, gw, agentName("dup"))
		}
		for _, cm := range order {
			if got := settle(t, gw, servingPort, host, canary(cm, i), 3, 2*time.Minute); got[0] != 200 {
				t.Fatalf("write %d: the canary in conf-dup-%s got %v, so the write never landed and "+
					"nothing after it measures that write", i+1, cm, got)
			}
		}
		got := codes(t, dupAnswers, gw, servingPort, host, keyDup)
		if !all(got, got[0]) {
			t.Fatalf("write %d: once the write had landed, the duplicated key's answers were %v. "+
				"They are not stable, so the gateway chooses per request, and §3.4.4's \"one of "+
				"the two groups\" is not what it does", i+1, got)
		}
		var winner string
		switch got[0] {
		case 200:
			admitted++
			winner = map[bool]string{true: "a", false: "b"}[groups["a"] == sliceTeam]
		case 403:
			refused++
			winner = map[bool]string{true: "a", false: "b"}[groups["a"] == "rogue"]
		default:
			t.Fatalf("write %d: the duplicated key got %v. A 401 means the gateway refused the "+
				"duplicate or dropped the key, which would make §3.4.4's re-grouping sentence "+
				"false; any other code is not an answer the group rule gives", i+1, got)
		}
		byName["conf-dup-"+winner]++
		if winner == order[1] {
			byOrder["written last"]++
		} else {
			byOrder["written first"]++
		}
		t.Logf("DUPLICATE-KEY write %d: a=%s b=%s, written %s then %s: HTTP %d, so conf-dup-%s won",
			i+1, groups["a"], groups["b"], order[0], order[1], got[0], winner)
	}
	t.Logf("DUPLICATE-KEY winners by name %v, by write order %v", byName, byOrder)
	if admitted == 0 || refused == 0 {
		t.Errorf("across %d writes the duplicated key was admitted %d times and refused %d times. "+
			"Its two entries name different groups, and measured on 1.5.0 the credential takes "+
			"each of them at some write, whichever way the groups are laid out; one answer only "+
			"means the gateway does something §3.4.4 does not say, such as taking the union of "+
			"the groups", dupWrites, admitted, refused)
	}
}

// ---- §8.1 cluster case 7: a Gateway-level policy beside `-auth` ------------

// TestSliceARoutePolicyOverridesAGatewayLevelOne measures what §3.2's
// route-level detection leaves out: an `AgentgatewayPolicy` with `traffic`
// authentication and authorization targeting the GATEWAY, beside the route's
// `<agent>-auth`. The Gateway's policy admits group `gwgroup` from a key set
// in the Gateway's namespace; the route's admits the Agent's namespace group.
//
// Two results, both measured on 1.5.0:
//
//   - Once `<agent>-auth` is on the route, the route's policy decides alone:
//     the Gateway's key gets 401 and the route's key 200. They do not merge,
//     so a Gateway-level policy neither adds to nor subtracts from what
//     `-auth` enforces, on a route `-auth` has reached.
//   - Before `<agent>-auth` exists, the Gateway's policy answers an
//     anonymous request on a PREPARED route with 401 — the answer `Create`'s
//     probe waits for (§3.3.3). So on such a Gateway the probe can pass with
//     no `-auth` enforcing, and the route is then published under the
//     Gateway's policy, admitting the Gateway's keys. That is §3.3.3's open
//     MINOR 5, measured.
func TestSliceARoutePolicyOverridesAGatewayLevelOne(t *testing.T) {
	sliceFixture(t)
	const gwName = "conf-slice-gwpol"
	const keyGw = "conf-gw-key"
	if err := apply(t, gatewayYAML(gwName)+"\n---\n"+
		keySet(sliceGatewayNS, "conf-gw-keys", map[string]string{keyGw: "gwgroup"})); err != nil {
		t.Fatal(err)
	}
	gw := gatewayAddress(t, gwName)

	// The Gateway-level policy: the compiler's shape, moved to the Gateway's
	// namespace (a targetRef selects same-namespace objects only, §3.2),
	// targeting the Gateway, admitting gwgroup.
	gp := authPolicy(t, agentName("gwlevel"))
	gp.SetNamespace(sliceGatewayNS)
	gp.SetName("conf-gateway-auth-" + runID)
	if err := unstructured.SetNestedSlice(gp.Object, []any{map[string]any{
		"group": "gateway.networking.k8s.io", "kind": "Gateway", "name": gwName}}, "spec", "targetRefs"); err != nil {
		t.Fatal(err)
	}
	if err := unstructured.SetNestedStringSlice(gp.Object, []string{`apiKey.group == "gwgroup"`},
		"spec", "traffic", "authorization", "policy", "matchExpressions"); err != nil {
		t.Fatal(err)
	}
	applyObject(t, gp)
	deleteLater(t, "agentgatewaypolicy", sliceGatewayNS, gp.GetName())
	if cs, _ := condsIn(t, sliceGatewayNS, "agentgatewaypolicy", gp.GetName()); cs["Attached"] != "True" {
		t.Fatalf("the Gateway-level policy did not attach: %v", cs)
	}

	agent := agentName("gwpol")
	route, _ := compiler.ServingRouteName(agent)
	host := sliceHost(agent)
	if err := apply(t, routeOn(route, gwName, host, "")); err != nil {
		t.Fatal(err)
	}
	deleteLater(t, "httproute", sliceNS, route)
	// The Gateway's own key reaching the missing backend (500) is the sign the
	// route is programmed under the Gateway's policy. Two answers mean only
	// "not yet", and the wait goes on past them: a 404 is a route not yet
	// attached, and no answer at all (code 0) is a new Gateway's proxy not yet
	// listening on the port. An earlier version waited past the 404 alone,
	// and failed on a fresh cluster whose proxy took a moment longer.
	for deadline := time.Now().Add(2 * time.Minute); ; {
		got := settle(t, gw, servingPort, host, keyGw, 3, 2*time.Minute)
		if got[0] == 500 {
			break
		}
		if (got[0] != 404 && got[0] != 0) || time.Now().After(deadline) {
			t.Fatalf("the Gateway's own key on the prepared route got %v; want 500, authorised by "+
				"the Gateway's policy and then no backend. Without it nothing below says the route "+
				"is under that policy", got)
		}
		time.Sleep(time.Second)
	}
	if got := codes(t, 3, gw, servingPort, host, ""); !all(got, 401) {
		t.Errorf("an anonymous request on a prepared route, with only a Gateway-level auth policy, "+
			"got %v. 401 is what makes §3.3.3's MINOR 5 real: `Create`'s probe would take it as "+
			"<agent>-auth enforcing. If this is 500 now, the Gateway's policy no longer reaches a "+
			"route with no backend, and the probe is not fooled by it", got)
	}

	if err := apply(t, routeOn(route, gwName, host, "conf-rev1")); err != nil {
		t.Fatal(err)
	}
	if got := settle(t, gw, servingPort, host, keyGw, 3, 2*time.Minute); got[0] != 200 {
		t.Fatalf("with only the Gateway-level policy, the Gateway's key on the published route got "+
			"%v; the control needs it admitted, or the Gateway's policy is not enforcing", got)
	}
	// The Gateway's policy lives in the Gateway's namespace, and the route's
	// key is stored only in the route's. §3.4.4 reads this as a second
	// namespace the selector did not reach.
	if got := codes(t, 3, gw, servingPort, host, keyTeam); !all(got, 401) {
		t.Errorf("with only the Gateway-level policy, a key stored only in the route's namespace got "+
			"%v; want 401. Anything else means the Gateway's selector read a key set outside its "+
			"own namespace, and §3.4.4's measured scope is wrong", got)
	}

	p := authPolicy(t, agent)
	applyObject(t, p)
	deleteLater(t, "agentgatewaypolicy", sliceNS, p.GetName())
	requireAttached(t, p.GetName())
	if got := settle(t, gw, servingPort, host, keyGw, 3, 2*time.Minute); got[0] != 401 {
		t.Errorf("with <agent>-auth on the route, the Gateway-level policy's key got %v; want 401. "+
			"200 means the Gateway's rule still admits beside the route's, and §3.2's route-level "+
			"detection is not enough", got)
	}
	for _, c := range []struct {
		key  string
		want int
		why  string
	}{
		{"", 401, "anonymous"},
		{keyTeam, 200, "a key in the route policy's group: the Gateway's rule, which does not admit it, is not applied too"},
		{keyRogue, 403, "a valid key in another group, refused by the route's rule"},
	} {
		if got := codes(t, 3, gw, servingPort, host, c.key); !all(got, c.want) {
			t.Errorf("%s got %v; want %d", c.why, got, c.want)
		}
	}
}
