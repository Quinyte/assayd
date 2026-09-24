//go:build cluster

// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

// Design 03's owed `make conformance-cluster` measurements that A86, A87 and
// A82 left open, against agentgateway 1.5.0:
//
//   - A86's three key-source shapes, and §8.1 owed item 4's case: a key source
//     that holds no key (an empty labelled ConfigMap, and none at all), a key
//     source whose every entry agentgateway rejects, and a key held only under
//     `binaryData`. Each decides a sentence A86 derived, and the §9 D6 residue
//     "every entry rejected, if 1.5.0 reports that Valid".
//   - §8.1 case 19 (f)'s owed measurement of the listener rename: what 1.5.0
//     writes on a served `<agent>-auth` once the Gateway's serving listener is
//     renamed, against which the operator's `AuthPolicyNotAttached` reading is
//     judged. envtest writes that report itself (summarisePolicy); this is the
//     report agentgateway writes.
//   - §3.4.3's owed experiment: whether an authentication-rejected request
//     consumes a local rate limit's quota.
//
// No operator runs here, as in the rest of the suite. Every `<agent>-auth` is
// compiler.AuthPolicy's output. The measurements, and what they do and do not
// show, are recorded in docs/research/a86-key-source-conformance-2026-09.md.
package conformance

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"os/exec"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"

	"github.com/Quinyte/assayd/internal/compiler"
)

// The key source is namespace-wide: every `<agent>-auth` selects every
// ConfigMap in its own namespace that carries compiler.APIKeySourceLabel. So a
// case that empties it, or fills it with entries agentgateway rejects, cannot
// run in sliceNS, where every other slice case reads the shared key set. These
// cases get a run namespace of their own, with a Gateway that admits routes
// from it and a backend in it.
const (
	keySourceNS      = "conf-keysrc"
	keySourceGateway = "conf-keysrc"
	keySourceBackend = "conf-ks-rev1"
	// keySourceSet is the one key ConfigMap each key-source case rewrites in
	// place.
	keySourceSet = "conf-ks-keys"
	// holdFor is how many answers, one a second, a case takes after a
	// transition it has waited for, to show that the state HOLDS rather than
	// that it was passed through.
	holdFor = 5
)

// keySourceFixture establishes the key-source namespace, its Gateway and its
// backend, idempotently, on top of the slice fixture (the traffic Pod, the
// Gateway namespace, and the release check), and returns the Gateway's
// in-cluster address. It then deletes every labelled key ConfigMap in the
// namespace, so each case starts from a key source it wrote itself and a run
// on a kept cluster never inherits one a previous case left.
func keySourceFixture(t *testing.T) string {
	t.Helper()
	sliceFixture(t)
	if err := apply(t, fmt.Sprintf(`
apiVersion: v1
kind: Namespace
metadata: {name: %s}
---
%s
---
%s`, keySourceNS, gatewayYAMLFor(keySourceGateway, keySourceNS),
		backendIn(keySourceBackend, keySourceNS))); err != nil {
		t.Fatal(err)
	}
	if out, err := kubectl(t, "rollout", "status", "deploy/"+keySourceBackend, "-n", keySourceNS,
		"--timeout=300s"); err != nil {
		t.Fatalf("the key-source backend never rolled out: %s", out)
	}
	if out, err := kubectl(t, "delete", "configmap", "-n", keySourceNS, "-l",
		compiler.APIKeySourceLabel+"="+compiler.APIKeySourceValue, "--wait=true"); err != nil {
		t.Fatalf("clear the key-source namespace's key sets: %s", out)
	}
	return gatewayAddress(t, keySourceGateway)
}

// validEntry is one key as agentgateway reads a ConfigMap-sourced key: the
// hash, never the key, and the group the policy's rule reads.
func validEntry(key, group string) string {
	return fmt.Sprintf(`{"keyHash":"sha256:%s","metadata":{"group":%q}}`, sha256Hex(key), group)
}

// rejectedEntry is A82's row 10 shape: a raw `key` where a ConfigMap-sourced
// entry must carry `keyHash`.
func rejectedEntry(plain string) string {
	return fmt.Sprintf(`{"key":%q}`, plain)
}

// writeKeySource replaces keySourceSet's whole content: exactly `data` and
// exactly `binaryData` (raw bytes, base64-encoded here), and nothing else.
//
// It uses server-side apply under one field manager, which removes any field
// that manager wrote before and omits now. Client-side apply would too, but it
// copies the whole object into `last-applied-configuration`, and a key source
// whose entries live on in an annotation is not the shape being measured. The
// write is read back, because each case's claim is about a ConfigMap of an
// exact shape, and a write that left an old field behind would measure another.
func writeKeySource(t *testing.T, data, binaryData map[string]string) {
	t.Helper()
	obj := map[string]any{
		"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]any{
			"name": keySourceSet, "namespace": keySourceNS,
			"labels": map[string]any{compiler.APIKeySourceLabel: compiler.APIKeySourceValue},
		},
	}
	if len(data) > 0 {
		obj["data"] = data
	}
	if len(binaryData) > 0 {
		b := map[string]string{}
		for k, v := range binaryData {
			b[k] = base64.StdEncoding.EncodeToString([]byte(v))
		}
		obj["binaryData"] = b
	}
	body, err := json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	c := exec.Command("kubectl", "apply", "--server-side", "--force-conflicts",
		"--field-manager=conf-keysource", "-f", "-")
	c.Stdin = strings.NewReader(string(body))
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("write key source %s/%s: %s", keySourceNS, keySourceSet, out)
	}
	out, err := kubectl(t, "get", "configmap", keySourceSet, "-n", keySourceNS, "-o", "json")
	if err != nil {
		t.Fatalf("read key source back: %s", out)
	}
	var got struct {
		Data       map[string]string `json:"data"`
		BinaryData map[string]string `json:"binaryData"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Data) != len(data) || len(got.BinaryData) != len(binaryData) {
		t.Fatalf("key source %s/%s holds %d data and %d binaryData entries after the write; "+
			"wanted exactly %d and %d", keySourceNS, keySourceSet, len(got.Data), len(got.BinaryData),
			len(data), len(binaryData))
	}
}

// keySourceAgent publishes an Agent's serving route in the key-source
// namespace on its backend, waits for it to serve, writes the compiler's
// `<agent>-auth` for it, and waits for an anonymous request to be refused.
// Each case then waits for §3.3.2's whole tuple itself, once its key source
// admits a key. It returns the route's hostname, the route's name and the
// policy's name.
func keySourceAgent(t *testing.T, gw, agent string) (host, route, policy string) {
	t.Helper()
	route, err := compiler.ServingRouteName(agent)
	if err != nil {
		t.Fatal(err)
	}
	host = sliceHost(agent)
	if err := apply(t, routeOnIn(route, keySourceNS, keySourceGateway, host, keySourceBackend)); err != nil {
		t.Fatal(err)
	}
	deleteLater(t, "httproute", keySourceNS, route)
	awaitCode(t, gw, servingPort, host, cardPath, "", 200, []int{404}, 2*time.Minute, agent+": the open route")
	p, err := compiler.AuthPolicy(compiler.AuthInput{
		AgentName: agent, AgentNamespace: sliceTeam,
		AgentUID: types.UID("conf-uid-" + agent), RunNamespace: keySourceNS,
	})
	if err != nil {
		t.Fatal(err)
	}
	applyObject(t, p)
	deleteLater(t, "agentgatewaypolicy", keySourceNS, p.GetName())
	awaitCode(t, gw, servingPort, host, cardPath, "", 401, []int{200}, 2*time.Minute,
		agent+": <agent>-auth enforcing")
	return host, route, p.GetName()
}

// awaitKeySourcePolicy is awaitPolicyAncestorOn for the key-source Gateway.
func awaitKeySourcePolicy(t *testing.T, policy, why string, want func(ancestorReport) bool) ancestorReport {
	t.Helper()
	return awaitPolicyAncestorOn(t, keySourceNS, sliceGatewayNS, keySourceGateway, policy, 2*time.Minute, why, want)
}

// requireKeySourceRouteAccepted is requireRouteAccepted for the key-source
// Gateway: every key-source state below is measured on a route the Gateway
// accepts at its current generation, so a 401 is authentication and never a
// detached route.
func requireKeySourceRouteAccepted(t *testing.T, route, why string) {
	t.Helper()
	got := routeConditionsOn(t, keySourceNS, sliceGatewayNS, keySourceGateway, route, why)
	if got["Accepted"] != "True" || got["ResolvedRefs"] != "True" {
		t.Fatalf("%s: route %s/%s reads Accepted=%s ResolvedRefs=%s; a key-source measurement on a "+
			"route the Gateway does not accept measures the route, not the key source",
			why, keySourceNS, route, got["Accepted"], got["ResolvedRefs"])
	}
}

// holdCode requires that holdFor requests, one a second, all get `want`.
func holdCode(t *testing.T, gw, host, key string, want int, why string) {
	t.Helper()
	var got []int
	for i := 0; i < holdFor; i++ {
		got = append(got, send(t, gw, servingPort, host, cardPath, key).code)
		time.Sleep(time.Second)
	}
	if !all(got, want) {
		t.Errorf("%s: got %v, want %d every time", why, got, want)
	}
}

// ---- A86's first shape, and §8.1 owed item 4: a key source with no key ----

// TestSliceAKeySourceWithNoKeyRefusesEveryKeyAndReportsValid measures the
// state design 03 A86 reports as `ApiKeySourceEmpty`, in both of its forms —
// a labelled ConfigMap that holds no entry (A86's first owed shape), and no
// labelled ConfigMap at all (§8.1 owed item 4's case) — against agentgateway
// 1.5.0.
//
// Measured, and asserted: in both forms every key gets 401, the admitted one
// included, while the policy reports §3.3.2's whole tuple — the real Gateway
// ancestor, `Accepted=True`/`Valid`, `Attached=True` — and the route reads
// accepted. So the outage is silent at the gateway's control plane, and A86's
// "an empty key set takes every key to 401", which was derived, is measured.
// That silence is what makes A86's operator-side report the only one there
// is, and it is asserted as a converged tuple so that an agentgateway that
// starts reporting an empty key source fails here and sends the reader back to
// A86, whose K1 rests on nothing else reporting it.
//
// Each form is entered from a working key source, so the admitted key moving
// from 200 to 401 is the evidence that the data plane has the new key source,
// and each is left the same way, so a later form is not measured on a proxy
// that never reloaded.
func TestSliceAKeySourceWithNoKeyRefusesEveryKeyAndReportsValid(t *testing.T) {
	gw := keySourceFixture(t)
	working := map[string]string{keyTeam: validEntry(keyTeam, sliceTeam), keyRogue: validEntry(keyRogue, "rogue")}
	writeKeySource(t, working, nil)
	deleteLater(t, "configmap", keySourceNS, keySourceSet)
	host, route, policy := keySourceAgent(t, gw, agentName("ksnone"))
	awaitCode(t, gw, servingPort, host, cardPath, keyTeam, 200, []int{401}, 2*time.Minute,
		"the admitted key under a working key source: the control this case's 401s are measured against")
	expectCode(t, gw, servingPort, host, cardPath, keyRogue, 403, "another group's key under a working key source")
	awaitKeySourcePolicy(t, policy, "the policy under a working key source",
		func(a ancestorReport) bool { return a.converged() })

	for _, form := range []struct {
		name  string
		enter func()
	}{
		{"an EMPTY labelled ConfigMap (A86's first shape)", func() { writeKeySource(t, nil, nil) }},
		{"NO labelled ConfigMap (§8.1 owed item 4)", func() {
			if out, err := kubectl(t, "delete", "configmap", keySourceSet, "-n", keySourceNS,
				"--wait=true"); err != nil {
				t.Fatalf("delete the key source: %s", out)
			}
		}},
	} {
		// Timed from before the write, so the figure is an upper bound on
		// propagation that includes the write and its read-back, not a round
		// trip of the first probe.
		start := time.Now()
		form.enter()
		awaitCode(t, gw, servingPort, host, cardPath, keyTeam, 401, []int{200}, 2*time.Minute,
			form.name+": the admitted key once the key source holds no key")
		t.Logf("%s: the admitted key read 401 within %s of the write starting", form.name,
			time.Since(start).Round(time.Millisecond))
		holdCode(t, gw, host, keyTeam, 401, form.name+": the admitted key")
		holdCode(t, gw, host, keyRogue, 401, form.name+": another group's key, which was 403")
		holdCode(t, gw, host, "", 401, form.name+": anonymous")
		rep := awaitKeySourcePolicy(t, policy, form.name+": the policy's report",
			func(ancestorReport) bool { return true })
		t.Logf("%s: the policy as agentgateway reports it: %s", form.name, rep)
		if !rep.converged() {
			t.Errorf("%s: the policy reads %s. On 1.5.0 it read the whole converged tuple — Valid and "+
				"Attached on the real Gateway — so nothing on the gateway's side reported this outage, "+
				"and design 03 A86's ApiKeySourceEmpty is the only report of it. If agentgateway now "+
				"reports it, A86's reasoning for K1 has a second source to weigh: revisit it rather "+
				"than change this assertion", form.name, rep)
		}
		requireKeySourceRouteAccepted(t, route, form.name)

		// And back, so the next form is entered from a working source too, and
		// so the 401s above are shown to be the key source's.
		start = time.Now()
		writeKeySource(t, working, nil)
		awaitCode(t, gw, servingPort, host, cardPath, keyTeam, 200, []int{401}, 2*time.Minute,
			form.name+": the admitted key once keys are written again")
		t.Logf("%s: the admitted key read 200 again within %s of the keys being written", form.name,
			time.Since(start).Round(time.Millisecond))
	}
}

// ---- A86's second shape: every entry rejected -----------------------------

// TestSliceAKeySourceWhoseEveryEntryIsRejectedReportsPartiallyValid measures
// the shape design 03 §9 D6 lists as a silent residue "if 1.5.0 reports that
// Valid": a key source whose every entry agentgateway rejects. A82 measured
// ONE rejected entry beside valid ones; this is every entry.
//
// Measured, and asserted: 1.5.0 does NOT report it `Valid`. The policy reads
// `Accepted=True`, reason `PartiallyValid`, naming every rejected entry, with
// `Attached=True` on the real Gateway ancestor, at its current generation —
// the report A82 measured for one entry, and the one §5's policy half raises
// `AuthPolicyNotAttached` on, with A83's partly-valid lead. And every key gets
// 401: nothing is loaded, so this is a complete outage like the empty source,
// reported where the empty source is not. So the D6 residue "every entry
// rejected, if 1.5.0 reports that Valid" is not real on 1.5.0.
//
// The report changing is the evidence the controller read the new key source;
// the admitted key then moving from 200 to 401 is the evidence the data plane
// did. Restoring a working entry clears the report and admits the key again,
// so both are this key source's.
func TestSliceAKeySourceWhoseEveryEntryIsRejectedReportsPartiallyValid(t *testing.T) {
	gw := keySourceFixture(t)
	working := map[string]string{keyTeam: validEntry(keyTeam, sliceTeam)}
	writeKeySource(t, working, nil)
	deleteLater(t, "configmap", keySourceNS, keySourceSet)
	host, route, policy := keySourceAgent(t, gw, agentName("ksrejected"))
	awaitCode(t, gw, servingPort, host, cardPath, keyTeam, 200, []int{401}, 2*time.Minute,
		"the admitted key under a working key source")
	awaitKeySourcePolicy(t, policy, "the policy under a working key source",
		func(a ancestorReport) bool { return a.converged() })

	const rejectedA, rejectedB = "conf-rejected-a", "conf-rejected-b"
	const plainA, plainB = "conf-plaintext-a", "conf-plaintext-b"
	start := time.Now()
	writeKeySource(t, map[string]string{
		rejectedA: rejectedEntry(plainA),
		rejectedB: rejectedEntry(plainB),
	}, nil)
	rep := awaitKeySourcePolicy(t, policy, "a key source whose every entry is rejected",
		func(a ancestorReport) bool { return !a.converged() })
	t.Logf("every entry rejected: the policy as agentgateway reports it: %s", rep)
	if !rep.broken() || rep.Conds["Accepted"].Reason != "PartiallyValid" {
		t.Errorf("every entry rejected: the policy reads %s; 1.5.0 reported Accepted=True with reason "+
			"PartiallyValid, which is one of §5's four broken shapes. Design 03 A88 closed §9 D6's "+
			"\"every entry rejected, if 1.5.0 reports that Valid\" residue on that measurement: revisit it",
			rep)
	}
	if rep.Synthetic() || rep.Conds["Attached"].Status != "True" {
		t.Errorf("every entry rejected: the policy reads %s; 1.5.0 kept it Attached=True on the real "+
			"Gateway ancestor, which is the shape A83's partly-valid lead describes", rep)
	}
	for _, e := range []string{rejectedA, rejectedB} {
		if !strings.Contains(rep.Conds["Accepted"].Message, e) {
			t.Errorf("every entry rejected: the report does not name the rejected entry %s, so an "+
				"administrator cannot find it: %q", e, rep.Conds["Accepted"].Message)
		}
	}
	awaitCode(t, gw, servingPort, host, cardPath, keyTeam, 401, []int{200}, 2*time.Minute,
		"every entry rejected: the admitted key")
	t.Logf("every entry rejected: the admitted key read 401 within %s of the write starting",
		time.Since(start).Round(time.Millisecond))
	holdCode(t, gw, host, keyTeam, 401, "every entry rejected: the admitted key, whose entry is gone")
	holdCode(t, gw, host, "", 401, "every entry rejected: anonymous")
	// The rejected entries' OWN plaintext keys. The admitted key's 401 follows
	// from its entry being removed; these are what show the rejected entries
	// load nothing. A 403 would be an entry loaded with no group, and a 200 a
	// raw key accepted from a ConfigMap.
	holdCode(t, gw, host, plainA, 401, "every entry rejected: the plaintext key of a rejected entry")
	holdCode(t, gw, host, plainB, 401, "every entry rejected: the plaintext key of the other rejected entry")
	requireKeySourceRouteAccepted(t, route, "every entry rejected")

	writeKeySource(t, working, nil)
	awaitKeySourcePolicy(t, policy, "the report clearing once a working entry is back",
		func(a ancestorReport) bool { return a.converged() })
	awaitCode(t, gw, servingPort, host, cardPath, keyTeam, 200, []int{401}, 2*time.Minute,
		"the admitted key once its entry is back")
}

// ---- A86's third shape: a key held only under binaryData ------------------

// TestSliceAKeyHeldOnlyInBinaryDataIsNotRead measures whether agentgateway
// 1.5.0 reads a key source's `binaryData`, which design 03 A86 left open while
// choosing to count a `binaryData` entry as PRESENT ("the owed conformance row
// decides it").
//
// Measured, and asserted: it does NOT. A valid entry held only under
// `binaryData` authenticates nothing — its key gets 401 — while a canary in
// the SAME ConfigMap's `data` is authenticated (403, a valid key in a group
// the policy does not admit), so the ConfigMap was loaded and its
// `binaryData` ignored. The policy reads the whole converged tuple throughout:
// the ignored entry is not reported as rejected either. The same entry moved
// to `data` admits the key at once, so the entry is good and only where it is
// held differs.
//
// That makes the shape a silent outage the operator's count does not catch: a
// key source whose only entries are under `binaryData` reads PRESENT to
// `countKeySource`, so no `ApiKeySourceEmpty`, while every key is refused and
// the Gateway reports `Valid`. It contradicts nothing A86 asserted as fact —
// A86 recorded it as a residue "silent until measured" — but it decides A86's
// counting rule against the reading built, and design 03 A88 records it for a
// decision. No behaviour is changed here.
func TestSliceAKeyHeldOnlyInBinaryDataIsNotRead(t *testing.T) {
	gw := keySourceFixture(t)
	writeKeySource(t, map[string]string{keyCanary: validEntry(keyCanary, "conf-canary")},
		map[string]string{keyTeam: validEntry(keyTeam, sliceTeam)})
	deleteLater(t, "configmap", keySourceNS, keySourceSet)
	host, route, policy := keySourceAgent(t, gw, agentName("ksbinary"))

	// THE CONTROL. The canary is a valid key under `data`, in a group no
	// policy admits: 403 means the proxy authenticated it, which it can do only
	// from this ConfigMap, so the ConfigMap is loaded and the key under its
	// `binaryData` has had every chance to be read.
	awaitCode(t, gw, servingPort, host, cardPath, keyCanary, 403, []int{401}, 2*time.Minute,
		"the canary under data: until it answers 403 the ConfigMap is not loaded and no 401 below "+
			"says anything about binaryData")
	holdCode(t, gw, host, keyTeam, 401, "a valid key in the admitted group held only under binaryData, "+
		"beside a loaded canary under data: 200 would mean agentgateway reads binaryData")
	rep := awaitKeySourcePolicy(t, policy, "the policy with a key under binaryData",
		func(ancestorReport) bool { return true })
	t.Logf("canary under data, admitted key under binaryData: %s", rep)
	if !rep.converged() {
		t.Errorf("with a valid entry under binaryData the policy reads %s; on 1.5.0 it read the whole "+
			"converged tuple, so an ignored binaryData entry is not reported at all", rep)
	}
	requireKeySourceRouteAccepted(t, route, "binaryData beside data")

	// The pure shape: nothing under data. The canary leaving is the evidence
	// the data plane has the new ConfigMap.
	writeKeySource(t, nil, map[string]string{keyTeam: validEntry(keyTeam, sliceTeam)})
	awaitCode(t, gw, servingPort, host, cardPath, keyCanary, 401, []int{403}, 2*time.Minute,
		"the canary once data is empty: 401, so the proxy has the binaryData-only ConfigMap")
	holdCode(t, gw, host, keyTeam, 401, "the admitted key held only under binaryData, nothing under data")
	rep = awaitKeySourcePolicy(t, policy, "the policy with only binaryData",
		func(ancestorReport) bool { return true })
	t.Logf("binaryData only: %s", rep)
	if !rep.converged() {
		t.Errorf("with only binaryData entries the policy reads %s; on 1.5.0 it read the whole "+
			"converged tuple, so this outage is silent at the gateway", rep)
	}
	requireKeySourceRouteAccepted(t, route, "binaryData only")

	// The entry is good: the same bytes under data admit the key.
	start := time.Now()
	writeKeySource(t, map[string]string{keyTeam: validEntry(keyTeam, sliceTeam)}, nil)
	awaitCode(t, gw, servingPort, host, cardPath, keyTeam, 200, []int{401}, 2*time.Minute,
		"the same entry moved to data: the key is admitted, so only where it was held differed")
	t.Logf("the same entry under data: the admitted key read 200 within %s of the write starting",
		time.Since(start).Round(time.Millisecond))
}

// ---- §8.1 case 19 (f)'s owed measurement: the listener rename --------------

// policyAncestor is one entry of a policy's status.ancestors, whole.
type policyAncestor struct {
	ancestorReport
	ControllerName string
}

// policyAncestorsOn reads every ancestor a policy reports and the policy's
// generation, with nothing chosen or filtered, for a case whose finding is the
// whole list.
func policyAncestorsOn(t *testing.T, ns, name string) ([]policyAncestor, int64) {
	t.Helper()
	out, err := kubectl(t, "get", "agentgatewaypolicy", name, "-n", ns, "-o", "json")
	if err != nil {
		t.Fatalf("read policy %s/%s: %s", ns, name, out)
	}
	var obj struct {
		Metadata struct {
			Generation int64 `json:"generation"`
		} `json:"metadata"`
		Status struct {
			Ancestors []struct {
				AncestorRef struct {
					Group, Kind, Name, Namespace string
				} `json:"ancestorRef"`
				ControllerName string `json:"controllerName"`
				Conditions     []struct {
					Type, Status, Reason, Message string
					ObservedGeneration            int64 `json:"observedGeneration"`
				} `json:"conditions"`
			} `json:"ancestors"`
		} `json:"status"`
	}
	if err := json.Unmarshal([]byte(out), &obj); err != nil {
		t.Fatalf("parse policy %s/%s: %v", ns, name, err)
	}
	var list []policyAncestor
	for _, a := range obj.Status.Ancestors {
		r := a.AncestorRef
		p := policyAncestor{ControllerName: a.ControllerName, ancestorReport: ancestorReport{
			Group: r.Group, Kind: r.Kind, Name: r.Name, Namespace: r.Namespace,
			Conds: map[string]ancestorCondition{},
		}}
		for _, c := range a.Conditions {
			p.Conds[c.Type] = ancestorCondition{Status: c.Status, Reason: c.Reason, Message: c.Message,
				ObservedGeneration: c.ObservedGeneration}
		}
		list = append(list, p)
	}
	return list, obj.Metadata.Generation
}

// TestSliceARenamedListenerReportsThePolicyOnTheSyntheticAncestor records what
// agentgateway 1.5.0 writes on a served `<agent>-auth` when the Gateway's
// serving listener is renamed — the state §8.1 case 19 (f) and (q) WRITE in
// envtest and A81 judges, which until now was measured only by hand, in
// docs/research/a80-served-route-walkthrough-2026-09.md.
//
// Measured, and asserted, on a Gateway of the case's own:
//
//   - The policy's ancestor list is REPLACED by ONE entry: the synthetic
//     `{group: agentgateway.dev, kind: Gateway, name: StatusSummary}`, with no
//     namespace, from controller agentgateway.dev/agentgateway, carrying
//     `Accepted=True`/`Valid` and `Attached=False`, reason `Pending`, message
//     "Policy is not attached: HTTPRoute <ns>/<route> is not attached to any
//     Gateway", both at the policy's current generation — which the rename
//     does not move. The real Gateway ancestor is gone, not beside it.
//   - policyReport reads any ancestor with that group and name as broken,
//     clause unattached, on its synthetic short-circuit (transcribed and
//     pinned untagged in ancestor_test.go), so asserting the group and the
//     name here is what shows the operator's `AuthPolicyNotAttached` fires on
//     the real report and not only on envtest's.
//   - The route reads `Accepted=False`, reason `NoMatchingParent`, at its
//     current generation, with `ResolvedRefs=True`.
//   - Every request gets 404, anonymous and keyed alike: nothing reaches the
//     agent, and there is no unauthenticated path. That is what A81's
//     route-first order says of this pass.
//
// The same tuple is written by envtest's summarisePolicy and is a row of
// internal/controller's TestTheServedPolicyReportHasThreeAnswers, so the
// operator's policyReport is pinned against it there (design 03 A88 (4)).
func TestSliceARenamedListenerReportsThePolicyOnTheSyntheticAncestor(t *testing.T) {
	sliceFixture(t)
	const gwName = "conf-slice-rename"
	if err := apply(t, gatewayYAML(gwName)); err != nil {
		t.Fatal(err)
	}
	deleteAndWaitLater(t, "gateway", sliceGatewayNS, gwName)
	gw := gatewayAddress(t, gwName)
	agent := agentName("rename")
	route, _ := compiler.ServingRouteName(agent)
	host := sliceHost(agent)
	if err := apply(t, routeOn(route, gwName, host, "conf-rev1")); err != nil {
		t.Fatal(err)
	}
	deleteLater(t, "httproute", sliceNS, route)
	awaitCode(t, gw, servingPort, host, cardPath, "", 200, []int{404}, 2*time.Minute, "the open route")
	p := authPolicy(t, agent)
	applyObject(t, p)
	deleteLater(t, "agentgatewaypolicy", sliceNS, p.GetName())
	awaitCode(t, gw, servingPort, host, cardPath, "", 401, []int{200}, 2*time.Minute,
		"the served <agent>-auth enforcing before the rename")
	awaitPolicyAncestorOn(t, sliceNS, sliceGatewayNS, gwName, p.GetName(), 2*time.Minute,
		"the served <agent>-auth before the rename", func(a ancestorReport) bool { return a.converged() })
	before, genBefore := policyAncestorsOn(t, sliceNS, p.GetName())
	if len(before) != 1 {
		t.Fatalf("before the rename the policy reports %d ancestors; the case starts from one, the "+
			"real Gateway: %v", len(before), before)
	}

	if out, err := kubectl(t, "patch", "gateway", gwName, "-n", sliceGatewayNS, "--type=json", "-p",
		`[{"op":"replace","path":"/spec/listeners/0/name","value":"http-renamed"}]`); err != nil {
		t.Fatalf("rename the Gateway's serving listener: %s", out)
	}
	// The policy's report moving to the synthetic ancestor is what is waited
	// for; everything else is read once it has.
	awaitPolicyAncestorOn(t, sliceNS, sliceGatewayNS, gwName, p.GetName(), 2*time.Minute,
		"the served <agent>-auth once its route's listener is renamed",
		func(a ancestorReport) bool { return a.Synthetic() })
	list, gen := policyAncestorsOn(t, sliceNS, p.GetName())
	for _, a := range list {
		t.Logf("RENAMED: policy at generation %d reports %s, controller %s", gen, a.ancestorReport, a.ControllerName)
	}
	if gen != genBefore {
		t.Errorf("the rename moved the policy's generation from %d to %d; it edits the Gateway, not "+
			"the policy, and on 1.5.0 the generation stayed", genBefore, gen)
	}
	if len(list) != 1 {
		t.Fatalf("after the rename the policy reports %d ancestors; on 1.5.0 it reported ONE, the "+
			"synthetic StatusSummary, with the real Gateway's gone: %v", len(list), list)
	}
	s := list[0]
	if s.Group != "agentgateway.dev" || s.Kind != "Gateway" || s.Name != "StatusSummary" || s.Namespace != "" {
		t.Errorf("the synthetic ancestor reads {group %q kind %q name %q namespace %q}; 1.5.0 wrote "+
			"{agentgateway.dev Gateway StatusSummary, no namespace}, and policyReport keys the fail-open "+
			"signal on the group and the name", s.Group, s.Kind, s.Name, s.Namespace)
	}
	if s.ControllerName != "agentgateway.dev/agentgateway" {
		t.Errorf("the synthetic ancestor's controller is %q", s.ControllerName)
	}
	acc, att := s.Conds["Accepted"], s.Conds["Attached"]
	if acc.Status != "True" || acc.Reason != "Valid" || att.Status != "False" || att.Reason != "Pending" {
		t.Errorf("the synthetic ancestor reads %s; 1.5.0 wrote Accepted=True/Valid and Attached=False/Pending",
			s.ancestorReport)
	}
	for typ, c := range s.Conds {
		if c.ObservedGeneration != gen {
			t.Errorf("the synthetic ancestor's %s is at generation %d and the policy at %d; on 1.5.0 both "+
				"conditions were current, so policyReport's ungated short-circuit reads a current report "+
				"here", typ, c.ObservedGeneration, gen)
		}
	}
	if !strings.Contains(att.Message, route) || !strings.Contains(att.Message, "not attached to any Gateway") {
		t.Errorf("the unattached report does not say which route lost its Gateway: %q", att.Message)
	}

	// Awaited rather than read once: the route's report and the policy's are
	// two status writes, and the second may land after the first is seen.
	rc, rgen := awaitRouteReportOn(t, sliceNS, sliceGatewayNS, gwName, route, 2*time.Minute,
		"the route once its listener is renamed",
		func(c map[string]ancestorCondition) bool { return c["Accepted"].Reason == "NoMatchingParent" })
	t.Logf("RENAMED: route at generation %d reports %v", rgen, rc)
	if rc["Accepted"].Status != "False" || rc["Accepted"].Reason != "NoMatchingParent" ||
		rc["ResolvedRefs"].Status != "True" {
		t.Errorf("the route reads %v; 1.5.0 wrote Accepted=False/NoMatchingParent and ResolvedRefs=True, "+
			"which is what §8.1 case 19's refuseRoute writes", rc)
	}

	// Nothing reaches the agent: neither an unauthenticated 200 nor a 401.
	awaitCode(t, gw, servingPort, host, cardPath, "", 404, []int{401}, 2*time.Minute,
		"an anonymous request once the listener is renamed")
	holdCode(t, gw, host, "", 404, "an anonymous request on the renamed listener")
	holdCode(t, gw, host, keyTeam, 404, "the admitted key on the renamed listener")

	if out, err := kubectl(t, "patch", "gateway", gwName, "-n", sliceGatewayNS, "--type=json", "-p",
		`[{"op":"replace","path":"/spec/listeners/0/name","value":"http"}]`); err != nil {
		t.Fatalf("restore the listener: %s", out)
	}
	awaitPolicyAncestorOn(t, sliceNS, sliceGatewayNS, gwName, p.GetName(), 2*time.Minute,
		"the policy once the listener is restored", func(a ancestorReport) bool { return a.converged() })
	awaitCode(t, gw, servingPort, host, cardPath, "", 401, []int{404}, 2*time.Minute,
		"an anonymous request once the listener is restored")
	expectCode(t, gw, servingPort, host, cardPath, keyTeam, 200, "the admitted key once the listener is restored")
}

// ---- §3.4.3's owed experiment: authentication and the rate limiter ---------

// jwtFixture is an RSA key, the JWKS agentgateway verifies against, and one
// token it signed, for the JWT shape of the rate-limit experiment.
func jwtFixture(t *testing.T) (jwks, token string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	b64 := base64.RawURLEncoding.EncodeToString
	keys, err := json.Marshal(map[string]any{"keys": []any{map[string]any{
		"kty": "RSA", "alg": "RS256", "use": "sig", "kid": "conf",
		"n": b64(key.N.Bytes()), "e": b64(big.NewInt(int64(key.E)).Bytes()),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	header := b64([]byte(`{"alg":"RS256","typ":"JWT","kid":"conf"}`))
	claims := b64([]byte(fmt.Sprintf(`{"iss":"conf-issuer","sub":"conf","exp":%d}`,
		time.Now().Add(time.Hour).Unix())))
	digest := sha256.Sum256([]byte(header + "." + claims))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return string(keys), header + "." + claims + "." + b64(sig)
}

// TestSliceAnAuthRejectedRequestConsumesNoRateLimit runs design 03 §3.4.3's
// experiment, owed to this harness: one route carrying authentication AND
// `rateLimit.local: [{requests: 1, unit: Hours}]` in one `traffic` policy, and
// three unauthenticated requests. §3.4.3's table reads `401, 401, 401` as
// "auth precedes the limiter, and rejected requests do not consume quota", and
// `401, 429, 429` as the denial-of-wallet path.
//
// It adds the control the table lacks: three 401s alone cannot tell "the
// limiter did not count them" from "the limiter is not enforcing". So an
// authenticated request follows them and must get 200 — the one request of
// quota is still there — and a second must get 429, which shows the limiter
// IS enforcing. An anonymous request after that still gets 401, not 429, so
// authentication answers first even with the quota spent.
//
// Two authentication shapes: the slice's API keys (compiler.AuthPolicy's
// output plus the limit), and the JWT shape §3.4.3 names. Measured on 1.5.0:
// both read `401, 401, 401`, then 200, then 429, then 401.
func TestSliceAnAuthRejectedRequestConsumesNoRateLimit(t *testing.T) {
	gw := sliceFixture(t)
	jwks, token := jwtFixture(t)
	for _, shape := range []struct {
		name, base, credential string
		traffic                func(p *unstructured.Unstructured)
	}{
		{"API keys, the slice's shape", "rlkey", keyTeam, func(*unstructured.Unstructured) {}},
		{"JWT, §3.4.3's shape", "rljwt", token, func(p *unstructured.Unstructured) {
			unstructured.RemoveNestedField(p.Object, "spec", "traffic", "apiKeyAuthentication")
			unstructured.RemoveNestedField(p.Object, "spec", "traffic", "authorization")
			if err := unstructured.SetNestedField(p.Object, map[string]any{
				"mode": "Strict",
				"providers": []any{map[string]any{
					"issuer": "conf-issuer", "jwks": map[string]any{"inline": jwks}}},
			}, "spec", "traffic", "jwtAuthentication"); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(shape.name, func(t *testing.T) {
			agent := agentName(shape.base)
			route, _ := compiler.ServingRouteName(agent)
			host := sliceHost(agent)
			if err := apply(t, servingRoute(t, agent, "conf-rev1")); err != nil {
				t.Fatal(err)
			}
			deleteLater(t, "httproute", sliceNS, route)
			awaitCode(t, gw, servingPort, host, cardPath, "", 200, []int{404}, 2*time.Minute, "the open route")
			p := authPolicy(t, agent)
			shape.traffic(p)
			if err := unstructured.SetNestedSlice(p.Object, []any{map[string]any{
				"requests": int64(1), "unit": "Hours"}}, "spec", "traffic", "rateLimit", "local"); err != nil {
				t.Fatal(err)
			}
			applyObject(t, p)
			deleteLater(t, "agentgatewaypolicy", sliceNS, p.GetName())
			requireAttached(t, p.GetName())

			// The first answer that is not the open route's 200 is the policy
			// landing, and it is the first of the three. awaitCode is not used:
			// it takes three agreeing answers, which is the measurement itself.
			var anon []int
			for deadline := time.Now().Add(2 * time.Minute); len(anon) == 0; {
				if time.Now().After(deadline) {
					t.Fatal("the policy never took the route off 200")
				}
				if c := send(t, gw, servingPort, host, cardPath, "").code; c != 200 {
					anon = append(anon, c)
				} else {
					time.Sleep(time.Second)
				}
			}
			for len(anon) < 3 {
				anon = append(anon, send(t, gw, servingPort, host, cardPath, "").code)
			}
			first := send(t, gw, servingPort, host, cardPath, shape.credential).code
			second := send(t, gw, servingPort, host, cardPath, shape.credential).code
			after := send(t, gw, servingPort, host, cardPath, "").code
			t.Logf("%s: anonymous %v, then authenticated %d and %d, then anonymous %d",
				shape.name, anon, first, second, after)
			if !all(anon, 401) {
				t.Errorf("%s: three unauthenticated requests got %v; §3.4.3's table reads anything but "+
					"401, 401, 401 as the limiter running first or counting refused requests — the "+
					"denial-of-wallet path", shape.name, anon)
			}
			if first != 200 {
				t.Errorf("%s: the first authenticated request after them got %d; 200 is the one request "+
					"of quota still unspent, so a 429 here means the refused requests consumed it",
					shape.name, first)
			}
			if second != 429 {
				t.Errorf("%s: the second authenticated request got %d; 429 is the control that the "+
					"limiter enforces at all, without which the 401s above prove nothing about it",
					shape.name, second)
			}
			if after != 401 {
				t.Errorf("%s: an unauthenticated request after the quota was spent got %d; 401 means "+
					"authentication answers before the limiter even then", shape.name, after)
			}
		})
	}
}
