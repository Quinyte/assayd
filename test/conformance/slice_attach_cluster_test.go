//go:build cluster

// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

// Design 03 §8.1's OWED `make conformance-cluster` case: A80's policy half,
// measured rather than inferred.
//
// §5's policy row describes a served `<agent>-auth` the assayd Gateway reports
// it does not attach, on a route it DOES accept, and calls the state
// "inferred from §3.3.2's reporting shape and not measured". §8.1 case 19 (f)
// writes that report into a fake Gateway status and measures the operator's
// reaction to it, which is not the same as measuring that agentgateway writes
// it — and the listener-rename walkthrough
// (docs/research/a80-served-route-walkthrough-2026-09.md) cannot separate the
// halves, because renaming the listener detaches the route AND the policy in
// one breath.
//
// These cases separate them. The first two start from a SERVED Agent — route
// published on a backend, `<agent>-auth` attached, anonymous requests refused —
// and break the policy alone, leaving the route accepted at its current
// generation. They differ in which of §3.3.2's tuple breaks they produce, and
// they answer the question §8.1 asks of this case with two different numbers:
//
//   - the route's `<agent>-auth` reported NOT ATTACHED, on the synthetic
//     `StatusSummary` ancestor: the route answers 200 to an anonymous request.
//     A live authentication bypass, which is what A80's fail-open half fears.
//     Reaching it needs an EDIT to the policy's own spec — which does NOT put
//     it outside §5's precondition, because that comparison is render against
//     render (`reassertServedPolicy` digests the compiler's output, never the
//     stored object). The operator judges the state AND repairs it on the same
//     pass, so this 200 is a window rather than a standing hole; §8.1 case 19
//     (f) pins that sequence, where the operator runs and this suite's does not.
//   - the policy reported `Accepted=True` with reason `PartiallyValid`, while
//     it stays attached: the route keeps answering 401. Reaching it needs no
//     edit to the policy at all — an administrator's key ConfigMap does it —
//     so this is the break A80's precondition admits, and it is fail-CLOSED.
//
// Measured on agentgateway 1.5.0; recorded, with what they do and do not show,
// in docs/research/a80-policy-half-conformance-2026-09.md.
package conformance

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/Quinyte/assayd/internal/compiler"
	"github.com/Quinyte/assayd/internal/controller"
)

// awaitPolicyAncestor polls a policy until the ancestor policyReport would read
// carries only conditions observed at the policy's CURRENT generation and
// `want` accepts the report, then returns it.
//
// The generation gate here is statusIsCurrent's, and it is STRICTER than
// `policyReport`'s on purpose: this reader rejects the whole read while ANY
// condition on the chosen ancestor is at another generation, where
// `policyReport` skips the stale condition, counts the rest, and does not gate
// the synthetic ancestor at all. That is a test's choice, not a transcription
// of the code — a case that measured a shape half of whose report was a
// generation old would be measuring two moments — and it is why the rule lives
// here rather than in ancestor.go beside the ones that ARE transcribed.
//
// It matters because a policy whose translation fails can leave one condition
// a generation behind the other: measured on 1.5.0 for an unparseable CEL
// expression, where `Accepted` moved to the new generation and `Attached`
// stayed on the old one. That is also why the timeout prints the last RAW
// read, stale conditions and all — a case that hit that hazard would otherwise
// time out reporting nothing at all.
func awaitPolicyAncestor(t *testing.T, name string, timeout time.Duration,
	why string, want func(ancestorReport) bool) ancestorReport {
	t.Helper()
	last := "nothing was read at all"
	for deadline := time.Now().Add(timeout); time.Now().Before(deadline); {
		got, raw, current := readPolicyAncestor(t, name)
		last = raw
		if current && want(got) {
			return got
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("%s: policy %s/%s never published the report this case is about in %s; "+
		"the last read was %s", why, sliceNS, name, timeout, last)
	return ancestorReport{}
}

// readPolicyAncestor reads the ancestor policyReport would read: the synthetic
// one if it is present anywhere in the list, otherwise the assayd Gateway's
// own. It scans the whole list rather than indexing [0] because policyReport
// does, and a list whose order changed would otherwise take the two apart.
//
// It returns the report, a raw description for a diagnostic, and whether every
// condition on that ancestor was observed at the policy's current generation.
func readPolicyAncestor(t *testing.T, name string) (rep ancestorReport, raw string, current bool) {
	t.Helper()
	out, err := kubectl(t, "get", "agentgatewaypolicy", name, "-n", sliceNS, "-o", "json")
	if err != nil {
		return ancestorReport{}, fmt.Sprintf("kubectl get failed: %s", tail(out, 2)), false
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
				Conditions []struct {
					Type, Status, Reason, Message string
					ObservedGeneration            int64 `json:"observedGeneration"`
				} `json:"conditions"`
			} `json:"ancestors"`
		} `json:"status"`
	}
	if err := json.Unmarshal([]byte(out), &obj); err != nil {
		return ancestorReport{}, fmt.Sprintf("the policy's JSON did not parse: %v", err), false
	}
	if len(obj.Status.Ancestors) == 0 {
		return ancestorReport{}, "the policy carries no ancestor at all", false
	}
	// policyReport's own scan, twice over: the synthetic ancestor wins wherever
	// it appears and short-circuits, and among the real Gateway's entries the
	// LAST one wins, because `ours = m` is re-assigned rather than guarded.
	// Both details matter and one of them is measured: on a policy with two
	// targetRefs of which one does not resolve, 1.5.0 writes BOTH ancestors at
	// the current generation — the synthetic for the ref that did not resolve
	// and the real Gateway, `Attached=True`, for the one that did
	// (`research/a80-policy-half-conformance-2026-09.md`, row 15).
	chosen := -1
	for i, a := range obj.Status.Ancestors {
		r := a.AncestorRef
		if r.Group == "agentgateway.dev" && r.Name == "StatusSummary" {
			chosen = i
			break
		}
		if r.Group == "gateway.networking.k8s.io" && r.Kind == "Gateway" &&
			r.Name == sliceGateway && r.Namespace == sliceGatewayNS {
			chosen = i
		}
	}
	if chosen < 0 {
		return ancestorReport{}, fmt.Sprintf(
			"none of the policy's %d ancestors is the synthetic one or Gateway %s/%s — which is "+
				"what policyReport calls unknown, and the state in which A80's policy half raises "+
				"nothing at all", len(obj.Status.Ancestors), sliceGatewayNS, sliceGateway), false
	}
	a := obj.Status.Ancestors[chosen]
	rep = ancestorReport{
		Group: a.AncestorRef.Group, Kind: a.AncestorRef.Kind,
		Name: a.AncestorRef.Name, Namespace: a.AncestorRef.Namespace,
		Conds: map[string]ancestorCondition{},
	}
	stale := []string{}
	for _, c := range a.Conditions {
		rep.Conds[c.Type] = ancestorCondition{
			Status: c.Status, Reason: c.Reason, Message: c.Message,
			ObservedGeneration: c.ObservedGeneration,
		}
		if c.ObservedGeneration != obj.Metadata.Generation {
			stale = append(stale, fmt.Sprintf("%s at gen %d", c.Type, c.ObservedGeneration))
		}
	}
	raw = fmt.Sprintf("policy at gen %d: %s", obj.Metadata.Generation, rep)
	if len(stale) > 0 {
		return rep, raw + fmt.Sprintf("; REJECTED as not current: %s while the policy is at %d",
			strings.Join(stale, ", "), obj.Metadata.Generation), false
	}
	return rep, raw, true
}

// requireRouteAccepted is the half of each case that makes it about the POLICY.
// It requires the assayd Gateway's OWN entry in the route's `status.parents` —
// matched on `parentRef` and `controllerName`, as internal/controller's
// routeReport matches it — to read `Accepted=True` and `ResolvedRefs=True` at
// the route's current generation. Without it a case could be measuring the
// route half, which is what the listener-rename walkthrough could not rule out.
func requireRouteAccepted(t *testing.T, route, why string) {
	t.Helper()
	out, err := kubectl(t, "get", "httproute", route, "-n", sliceNS, "-o", "json")
	if err != nil {
		t.Fatalf("%s: read route %s/%s: %s", why, sliceNS, route, out)
	}
	var obj struct {
		Metadata struct {
			Generation int64 `json:"generation"`
		} `json:"metadata"`
		Status struct {
			Parents []struct {
				ControllerName string `json:"controllerName"`
				ParentRef      struct {
					Name      string  `json:"name"`
					Namespace *string `json:"namespace"`
				} `json:"parentRef"`
				Conditions []struct {
					Type, Status, Reason string
					ObservedGeneration   int64 `json:"observedGeneration"`
				} `json:"conditions"`
			} `json:"parents"`
		} `json:"status"`
	}
	if err := json.Unmarshal([]byte(out), &obj); err != nil {
		t.Fatalf("%s: parse route %s/%s: %v", why, sliceNS, route, err)
	}
	for _, p := range obj.Status.Parents {
		// An absent parentRef namespace means the ROUTE's namespace, as
		// routeReport defaults it. The fixture always sets it — the Gateway is
		// in another namespace — but matching the way the code matches is what
		// keeps the comment above true.
		ns := sliceNS
		if p.ParentRef.Namespace != nil {
			ns = *p.ParentRef.Namespace
		}
		if p.ControllerName != controller.AgentgatewayControllerName ||
			p.ParentRef.Name != sliceGateway || ns != sliceGatewayNS {
			continue
		}
		got := map[string]string{}
		for _, c := range p.Conditions {
			if c.ObservedGeneration != obj.Metadata.Generation {
				t.Fatalf("%s: the Gateway's entry on route %s/%s reports %s at generation %d, "+
					"and the route is at %d; this case may not rest on a stale route report",
					why, sliceNS, route, c.Type, c.ObservedGeneration, obj.Metadata.Generation)
			}
			got[c.Type] = c.Status
		}
		if got["Accepted"] != "True" || got["ResolvedRefs"] != "True" {
			t.Fatalf("%s: route %s/%s reads Accepted=%s ResolvedRefs=%s from the assayd Gateway; "+
				"this case measures the POLICY half and needs an ACCEPTED route, or it cannot be "+
				"told from the route half (§5, A81)",
				why, sliceNS, route, got["Accepted"], got["ResolvedRefs"])
		}
		return
	}
	t.Fatalf("%s: route %s/%s carries no status.parents entry from %s for Gateway %s/%s",
		why, sliceNS, route, controller.AgentgatewayControllerName, sliceGatewayNS, sliceGateway)
}

// keyCanary is the freshness control for the key-set case: a VALID key, in a
// group no `<agent>-auth` admits, written in the same ConfigMap as the entry
// the controller rejects. Its 403 is what makes that ConfigMap's presence in
// the data plane observable from a request.
const keyCanary = "conf-canary-key"

// sha256Hex is the hash form agentgateway reads a ConfigMap-sourced key in.
func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// livePolicy reads one policy back as the unstructured object compiler.Digest
// takes, so a digest can be computed over what the CLUSTER holds rather than
// over what the test believes it wrote.
func livePolicy(t *testing.T, name, why string) *unstructured.Unstructured {
	t.Helper()
	out, err := kubectl(t, "get", "agentgatewaypolicy", name, "-n", sliceNS, "-o", "json")
	if err != nil {
		t.Fatalf("%s: read policy %s/%s: %s", why, sliceNS, name, out)
	}
	obj := &unstructured.Unstructured{}
	if err := json.Unmarshal([]byte(out), &obj.Object); err != nil {
		t.Fatalf("%s: parse policy %s/%s: %v", why, sliceNS, name, err)
	}
	return obj
}

// requireDigestUnchanged requires that the policy AS THE CLUSTER HOLDS IT still
// renders to the digest the compiler gave for this Agent.
//
// It is NOT §5's precondition, and saying so is the correction A82's critique
// forced: that precondition compares the compiler's render against
// `appliedDigest`, which is a render digest too, so it is invariant under an
// out-of-band edit and never excludes one. This checks the STORED object, which
// is a different and narrower question — did the stimulus leave the policy
// byte-identical to what the operator would write? — and it is what tells case
// (A)'s administrator-caused break apart from case (B)'s hand edit.
func requireDigestUnchanged(t *testing.T, agent, why string) {
	t.Helper()
	want, err := compiler.Digest(authPolicy(t, agent))
	if err != nil {
		t.Fatal(err)
	}
	name, err := compiler.AuthPolicyName(agent)
	if err != nil {
		t.Fatal(err)
	}
	got, err := compiler.Digest(livePolicy(t, name, why))
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("%s: the live policy %s/%s digests to %s and the compiler's to %s, so it no "+
			"longer renders to status.auth.appliedDigest and A80's policy half would not judge it",
			why, sliceNS, name, got, want)
	}
}

// ---- (A) the break that needs no edit to the policy, and refuses ----------

// TestSliceAPolicyBrokenByItsKeySetStaysAttachedAndKeepsRefusing measures the
// one shape of A80's policy half that a served Agent can reach with its
// `<agent>-auth` BYTE-UNCHANGED, and so the one shape the operator's own
// precondition admits (§5: "exists, carries this Agent's UID label, and renders
// to `status.auth.appliedDigest`").
//
// The stimulus is outside the policy entirely: a second ConfigMap carrying the
// product's key-source label, holding an entry agentgateway rejects — a raw
// `key` where a ConfigMap-sourced entry must carry `keyHash`. The policy
// selects it by label, cannot translate it, and reports `Accepted=True` with
// reason `PartiallyValid` at its own current generation, which is §5's second
// listed tuple break. Nobody edited the policy; the key ConfigMap is written by
// an administrator (§3.4.4, "the key set is outside every gate").
//
// The number the case exists for: the route still answers **401** to an
// anonymous request, and still answers 200 to the admitted key. So in the state
// an administrator reaches without touching anything assayd wrote, the report is
// a reporting event and NOT the live authentication bypass its message
// announces. Case (B) below is the shape that IS a bypass; the precondition does
// not exclude it either — it is judged, and repaired — and what separates the two
// is who can cause them and what the route then does, not the digest.
func TestSliceAPolicyBrokenByItsKeySetStaysAttachedAndKeepsRefusing(t *testing.T) {
	gw := sliceFixture(t)
	agent := agentName("brokenkeys")
	host := publishedWithAuth(t, gw, agent)
	route, _ := compiler.ServingRouteName(agent)
	policy, _ := compiler.AuthPolicyName(agent)

	// §3.3.2's whole tuple holds BEFORE the stimulus. Without this the case
	// would pass having caused nothing on a KEEP=1 cluster where an earlier run
	// left a rejected key set behind: the first read would already be
	// `PartiallyValid` and every check below would hold. `converged` covers the
	// ancestor as well as the two conditions, so there is nothing left to
	// assert beside it.
	awaitPolicyAncestor(t, policy, 2*time.Minute,
		"the served <agent>-auth before anything is broken",
		func(a ancestorReport) bool { return a.converged() })

	// A second labelled key set, never the shared one: the shared one is every
	// other slice case's, and a json-patch that half-failed would leave it
	// broken for them. runID, as everything else here is named, so a run on a
	// kept cluster never meets the ConfigMap a previous one left.
	//
	// THE BLAST RADIUS IS THE WHOLE NAMESPACE while this exists. Every
	// `<agent>-auth` in sliceNS selects key sets by the same constant label
	// (compiler.APIKeySourceLabel), so one rejected entry puts every policy
	// here at PartiallyValid until the Cleanup deletes it. Nothing enforces
	// that no case here runs in parallel — Go runs them sequentially only
	// because none calls t.Parallel(), and no gate would catch one that did —
	// so this is a standing hazard for the next editor and not a guarantee.
	// The same fan-out is the production consequence A82 records in §5.
	badKeys := "conf-slice-rejected-keys-" + runID
	if err := apply(t, fmt.Sprintf(`
apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: %s
  labels: {%s: %q}
data:
  conf-rejected-entry: '{"key":"plaintext-not-a-hash"}'
  %s: '{"keyHash":"sha256:%s","metadata":{"group":"conf-canary"}}'`,
		badKeys, sliceNS, compiler.APIKeySourceLabel, compiler.APIKeySourceValue,
		keyCanary, sha256Hex(keyCanary))); err != nil {
		t.Fatal(err)
	}
	deleteAndWaitLater(t, "configmap", sliceNS, badKeys)

	// How long the controller takes to notice a ConfigMap it selects by label
	// is a re-list, not a watch on a named object, and on a cold cluster it has
	// been the slowest step here. The timeout is what it is because of this
	// number; log it so a later run that gets close says so before it flakes.
	reported := time.Now()
	// `broken` is in the predicate rather than asserted after it, so the thing
	// waited for is the thing claimed: §5's second tuple break, on a report
	// that is one of the four and not merely a reason string.
	rep := awaitPolicyAncestor(t, policy, 2*time.Minute,
		"a served <agent>-auth whose key source carries a rejected entry",
		func(a ancestorReport) bool {
			return a.broken() && a.Conds["Accepted"].Reason == "PartiallyValid"
		})
	t.Logf("the break as agentgateway reports it, %s after the ConfigMap was written: %s",
		time.Since(reported).Round(time.Millisecond), rep)
	// The ancestor is the REAL Gateway, named the way policyReport names it,
	// and the policy is still attached: this is the reported-broken-but-
	// attached shape, not case (B)'s. All four ref fields, because
	// policyReport compares all four and an upstream rename of any of them
	// takes A80's policy half silent.
	if rep.Synthetic() || rep.Conds["Attached"].Status != "True" {
		t.Fatalf("this case is the ATTACHED break; agentgateway reported the unattached one "+
			"instead, which is case (B)'s state: %s", rep)
	}
	if rep.Group != "gateway.networking.k8s.io" || rep.Kind != "Gateway" ||
		rep.Name != sliceGateway || rep.Namespace != sliceGatewayNS {
		t.Errorf("policyReport finds this Agent's report by {group: gateway.networking.k8s.io, "+
			"kind: Gateway, name: %s, namespace: %s} and agentgateway wrote %s; on an install "+
			"where that is what it writes, the policy half reads reportUnknown and raises nothing",
			sliceGateway, sliceGatewayNS, rep)
	}
	if !strings.Contains(rep.Conds["Accepted"].Message, badKeys) {
		t.Errorf("the reported reason does not name the ConfigMap that caused it, so an "+
			"administrator cannot find it: %q", rep.Conds["Accepted"].Message)
	}

	// The two facts that make this A80's POLICY half and not its route half.
	requireRouteAccepted(t, route, "the key-set break")
	requireDigestUnchanged(t, agent, "the key-set break")

	// THE CONTROL, and the case says nothing without it. The three 401s below
	// are measured through the data plane while the break is read from the
	// CONTROL plane, so a 401 served from configuration the proxy loaded before
	// the ConfigMap existed would be indistinguishable from a 401 served under
	// it. `keyCanary` is a VALID entry in a group the policy does not admit,
	// written in the SAME ConfigMap as the rejected one: 403 means the proxy
	// authenticated it, which it can only do from that ConfigMap, so the
	// ConfigMap is live and the 401s that follow are measured under it. 401
	// here would mean the key is unknown — the ConfigMap not loaded — and the
	// case would be measuring stale config.
	awaitCode(t, gw, servingPort, host, cardPath, keyCanary, 403, []int{401},
		2*time.Minute,
		"a valid key in another group, written in the REJECTED ConfigMap: until this answers 403 "+
			"the proxy has not loaded that ConfigMap and no 401 below is evidence about it")

	// The number §8.1 asks this case to record.
	for i := 0; i < 3; i++ {
		expectCode(t, gw, servingPort, host, cardPath, "", 401,
			"an anonymous request while the Gateway reports <agent>-auth PartiallyValid on an "+
				"accepted route: §5's policy row announces a possible bypass, and this shape is "+
				"fail-CLOSED")
	}
	expectCode(t, gw, servingPort, host, cardPath, keyTeam, 200,
		"the admitted key while the policy reports PartiallyValid: the good entries still load, "+
			"which is what PARTIALLY valid means here")
	expectCode(t, gw, servingPort, host, cardPath, keyRogue, 403,
		"a valid key in another group while the policy reports PartiallyValid: the authorization "+
			"rule is still enforced")

	// It heals with the cause, so the report is of THIS ConfigMap and not of
	// anything the case did to reach it.
	if out, err := kubectl(t, "delete", "configmap", badKeys, "-n", sliceNS, "--wait=true"); err != nil {
		t.Fatalf("remove the rejected key set: %s", out)
	}
	awaitPolicyAncestor(t, policy, 2*time.Minute,
		"the report clearing once the rejected entry is gone",
		func(a ancestorReport) bool { return a.converged() })
	// Both numbers again, not only the admitted key: the case's claim is that
	// the break changed what the Gateway REPORTS and not what the route DOES,
	// and that is only shown by measuring the route on both sides of it.
	expectCode(t, gw, servingPort, host, cardPath, "", 401,
		"the anonymous request after the rejected key set is gone: unchanged, which is the point")
	expectCode(t, gw, servingPort, host, cardPath, keyTeam, 200,
		"the admitted key after the rejected key set is gone")
	expectCode(t, gw, servingPort, host, cardPath, keyCanary, 401,
		"the canary after its ConfigMap is gone: unknown again, so the control was measuring that "+
			"ConfigMap and not something else")
}

// ---- (B) the break that is a bypass, and needs an edit to reach ------------

// TestSliceAnUnattachedAuthPolicyLeavesAnAcceptedRouteOpen measures the state
// A80's policy half describes in terms: the Gateway attaches `<agent>-auth` to
// nothing while it goes on accepting the route the policy names, and the route
// keeps carrying traffic.
//
// The stimulus is a `sectionName` on the policy's own `targetRefs` naming a
// rule the route does not have. agentgateway reports it on the synthetic
// `StatusSummary` ancestor — §3.3.2's negative signal — with `Attached=False`,
// reason `Pending`, at the policy's current generation, while the route reads
// `Accepted=True` and `ResolvedRefs=True` at its own.
//
// The number: an anonymous request gets **200**, with the backend's own answer.
// A80's fail-open half is real at the gateway. Two things bound the claim.
//
//   - The route is what serves it: the same request got 401 a moment earlier
//     and gets 401 again when the `sectionName` is removed, so the 200 is the
//     policy's absence and not a route the case knocked over.
//   - Reaching it took an EDIT to the policy's spec. That does NOT put the
//     state beyond the operator: §5's precondition compares the compiler's
//     RENDER against `appliedDigest`, itself a render digest, so an
//     out-of-band edit is invisible to it. The operator judges this state and
//     `writeAuthPolicy` repairs the spec on the same pass — entered, reported,
//     repaired — which is why the case asserts the live digest has CHANGED and
//     then, after the restore, that it is back. What the digest assertion pins
//     here is that the stimulus really did drift the stored object, so the
//     envtest row that measures the repair is measuring the same thing this
//     case measured at the gateway.
//   - Entering it needs an identity that can write a policy in a run
//     namespace, which a chart install reserves to the operator and
//     `admission.extraOperators`. This cluster installs no chart, so the case
//     writes it as cluster-admin.
func TestSliceAnUnattachedAuthPolicyLeavesAnAcceptedRouteOpen(t *testing.T) {
	gw := sliceFixture(t)
	agent := agentName("unattached")
	host := publishedWithAuth(t, gw, agent)
	route, _ := compiler.ServingRouteName(agent)
	policy, _ := compiler.AuthPolicyName(agent)

	// Healthy first, on §3.3.2's whole tuple: publishedWithAuth's
	// requireAttached takes any `Accepted` reason, so without this the case
	// could start from a policy that was already reported broken.
	awaitPolicyAncestor(t, policy, 2*time.Minute,
		"the served <agent>-auth before its target is broken",
		func(a ancestorReport) bool { return a.converged() })

	patch := fmt.Sprintf(
		`{"spec":{"targetRefs":[{"group":"gateway.networking.k8s.io","kind":"HTTPRoute",`+
			`"name":%q,"sectionName":"conf-no-such-rule"}]}}`, route)
	if out, err := kubectl(t, "patch", "agentgatewaypolicy", policy, "-n", sliceNS,
		"--type=merge", "-p", patch); err != nil {
		t.Fatalf("point <agent>-auth at a rule the route does not have: %s", out)
	}

	rep := awaitPolicyAncestor(t, policy, 2*time.Minute,
		"a served <agent>-auth the Gateway attaches to nothing",
		func(a ancestorReport) bool { return a.Conds["Attached"].Status == "False" })
	// Both fields, spelled as policyReport spells them
	// (`internal/controller/authserved.go`): it keys the fail-open signal on
	// `group == "agentgateway.dev"` AND `name == "StatusSummary"`, so a rename
	// of either takes A80's policy half silent while the bypass below stays
	// real, and asserting the name alone would not see it.
	if rep.Group != "agentgateway.dev" || rep.Name != "StatusSummary" {
		t.Errorf("§3.3.2 says a failure emits the synthetic ancestor {group: agentgateway.dev, "+
			"name: StatusSummary}, which is what policyReport matches on, and agentgateway wrote "+
			"%s; on an install where that is what it writes, the policy half raises nothing while "+
			"the route below still answers 200", rep)
	}
	if got := rep.Conds["Attached"]; got.Reason != "Pending" {
		t.Errorf("the unattached report reads reason %q; 1.5.0 reported Pending, and the "+
			"operator's judgement keys on the status and the ancestor rather than this "+
			"string: %s", got.Reason, rep)
	}
	if !strings.Contains(rep.Conds["Attached"].Message, "conf-no-such-rule") {
		t.Errorf("the unattached report does not name what it could not resolve: %q",
			rep.Conds["Attached"].Message)
	}
	// The route is ACCEPTED on the same pass. This is the assertion that makes
	// the case A80's policy half rather than the listener-rename incident,
	// where both halves fire and the route's reason leads (A81, case 19 (q)).
	requireRouteAccepted(t, route, "the unattached policy")

	// The number §8.1 asks this case to record. 401 is allowed on the way: the
	// proxy lags the controller's report, which is exactly the window §3.3.3
	// says the control-plane gate narrows and does not close.
	awaitCode(t, gw, servingPort, host, cardPath, "", 200, []int{401}, 2*time.Minute,
		"an anonymous request while the Gateway reports <agent>-auth attached to nothing on an "+
			"ACCEPTED route: §5's policy row says the route may be answering with no credential "+
			"required, and it is")
	// Again AFTER the 200, not only before it. The wait above can take up to
	// two minutes, and "the route is accepted on the same pass" is a claim
	// about the moment the 200 was served, not about the moment the case
	// started waiting for it.
	requireRouteAccepted(t, route, "the unattached policy, beside the anonymous 200")
	body := expectCode(t, gw, servingPort, host, cardPath, "", 200,
		"the anonymous request again, for the body: the 200 must be the BACKEND answering and "+
			"not the gateway").body
	if strings.TrimSpace(body) == "" {
		t.Errorf("the anonymous 200 carried no body, so it is not evidence that the request " +
			"reached the agent's backend")
	}

	// The stored object really drifted — which is what makes this the state the
	// envtest repair row measures the operator against, and NOT a reason the
	// operator would skip it. §5's precondition compares the compiler's RENDER to
	// `appliedDigest`, itself a render digest, so this drift is invisible to it:
	// the policy half judges this state and `writeAuthPolicy` repairs the spec.
	want, err := compiler.Digest(authPolicy(t, agent))
	if err != nil {
		t.Fatal(err)
	}
	got, err := compiler.Digest(livePolicy(t, policy, "the edited policy"))
	if err != nil {
		t.Fatal(err)
	}
	if got == want {
		t.Fatalf("the edited policy still digests to the compiler's %s, so the stimulus did not "+
			"drift the stored object and this case is not the state §8.1 case 19 (f)'s repair row "+
			"measures the operator against", want)
	}

	// Removing the sectionName restores the refusal, which is what makes the
	// 200 above the policy's absence and not something else the case broke.
	if out, err := kubectl(t, "patch", "agentgatewaypolicy", policy, "-n", sliceNS,
		"--type=json", "-p", `[{"op":"remove","path":"/spec/targetRefs/0/sectionName"}]`); err != nil {
		t.Fatalf("restore <agent>-auth's target: %s", out)
	}
	awaitPolicyAncestor(t, policy, 2*time.Minute,
		"the policy attaching again once its target resolves",
		func(a ancestorReport) bool { return a.converged() })
	awaitCode(t, gw, servingPort, host, cardPath, "", 401, []int{200}, 2*time.Minute,
		"the anonymous request once <agent>-auth attaches again")
	requireDigestUnchanged(t, agent, "the restored policy")
}

// ---- (C) both ancestors at once: the shape that breaks policyReport ---------

// TestSliceAPartlyResolvedPolicyReportsBothAncestors measures a shape nobody
// had recorded and that A82's critique found by asking the right question: what
// does 1.5.0 write when a policy has SEVERAL targets and only some resolve?
//
// It writes BOTH ancestors, at the policy's current generation — the synthetic
// `StatusSummary` for the ref that did not resolve, and the real Gateway's,
// `Accepted=True`/`Valid` and **`Attached=True`**, for the one that did — and
// the route the resolved ref names goes on refusing anonymous requests `401`.
// So the synthetic ancestor does not mean "this policy attached to nothing"; it
// means "at least one of its targets did not resolve".
//
// Two things follow, and the case exists for both.
//
//   - **The mechanism behind A82's negative result.** The compiler emits ONE
//     `targetRef`, so for an `<agent>-auth` the synthetic ancestor and
//     non-attachment do coincide — and the only way to make that one ref fail
//     to resolve is to change the ref or remove the route, which is why every
//     bypass stimulus had to edit the policy's spec. That is a reason, not a
//     stimulus count. Removing the route does NOT fire the route half:
//     recreateRoute puts a Create in the slot, and the served judgement runs
//     only on an empty slot or a refused Adopt, so the Create path is the way
//     out. And the bound, because a reader would otherwise act on this: an
//     identity that can write the policy can strip assayd.dev/agent-uid and
//     repoint targetRefs in ONE patch, after which §3.2's name-and-label rule
//     means the operator neither judges nor repairs it and the route serves
//     unauthenticated STANDING, not for a window.
//   - **A second instance of the rule-8 defect, worse than the first.**
//     `policyReport` short-circuits on the synthetic ancestor wherever it
//     appears, so on this shape the operator would report
//     `AuthPolicyNotAttached` against a contemporaneous `Attached=True` at the
//     same generation, while the route is measured enforcing. The comment in
//     `internal/controller/authserved.go` justifying the short-circuit — "the
//     last thing the Gateway said is that this policy attached to nothing" — is
//     measured FALSE here. Recorded in §5 and A82 as owed; not fixed in a
//     measurement.
//
// It is out of the operator's reach today for the same reason as case (B): the
// compiler emits one ref and repairs a second away. It is measured because the
// design's reading of the synthetic ancestor rests on what it means, and it
// means less than the code assumes.
func TestSliceAPartlyResolvedPolicyReportsBothAncestors(t *testing.T) {
	gw := sliceFixture(t)
	agent := agentName("partlyresolved")
	host := publishedWithAuth(t, gw, agent)
	route, _ := compiler.ServingRouteName(agent)
	policy, _ := compiler.AuthPolicyName(agent)

	awaitPolicyAncestor(t, policy, 2*time.Minute,
		"the served <agent>-auth before a second target is added",
		func(a ancestorReport) bool { return a.converged() })

	absent := route + "-absent"
	patch := fmt.Sprintf(
		`{"spec":{"targetRefs":[`+
			`{"group":"gateway.networking.k8s.io","kind":"HTTPRoute","name":%q},`+
			`{"group":"gateway.networking.k8s.io","kind":"HTTPRoute","name":%q}]}}`, route, absent)
	if out, err := kubectl(t, "patch", "agentgatewaypolicy", policy, "-n", sliceNS,
		"--type=merge", "-p", patch); err != nil {
		t.Fatalf("give <agent>-auth a second target that does not resolve: %s", out)
	}

	// The reader takes the ancestor policyReport would take, which is the
	// synthetic one; the raw list is read beside it, because the whole point is
	// the OTHER entry the operator never looks at.
	rep := awaitPolicyAncestor(t, policy, 2*time.Minute,
		"a partly resolved <agent>-auth",
		func(a ancestorReport) bool { return a.Synthetic() })
	if got := rep.Conds["Attached"]; got.Status != "False" {
		t.Errorf("the synthetic ancestor reads Attached=%s; 1.5.0 wrote False for the ref that "+
			"did not resolve: %s", got.Status, rep)
	}
	if !strings.Contains(rep.Conds["Attached"].Message, absent) {
		t.Errorf("the synthetic ancestor does not name the ref that did not resolve: %q",
			rep.Conds["Attached"].Message)
	}

	real := requireRealGatewayAncestor(t, policy)
	if real.Conds["Attached"].Status != "True" || real.Conds["Accepted"].Reason != "Valid" {
		t.Fatalf("the real Gateway's ancestor reads %s; this case is about it saying the policy IS "+
			"attached while the synthetic one says it is not", real)
	}
	if a, b := real.Conds["Attached"].ObservedGeneration,
		rep.Conds["Attached"].ObservedGeneration; a != b {
		t.Errorf("the two ancestors were observed at generations %d and %d; the finding is that they "+
			"are CONTEMPORANEOUS, and at different generations it would be an ordinary lag", a, b)
	}

	requireRouteAccepted(t, route, "the partly resolved policy")
	// And the route enforces. This is the number that makes the short-circuit
	// wrong rather than merely imprecise.
	for i := 0; i < 3; i++ {
		expectCode(t, gw, servingPort, host, cardPath, "", 401,
			"an anonymous request while the Gateway reports BOTH a synthetic Attached=False and a "+
				"real Attached=True at the same generation: policyReport would call this "+
				"AuthPolicyNotAttached, and the route is enforcing")
	}
	expectCode(t, gw, servingPort, host, cardPath, keyTeam, 200,
		"the admitted key on a partly resolved policy")

	if out, err := kubectl(t, "patch", "agentgatewaypolicy", policy, "-n", sliceNS,
		"--type=json", "-p", `[{"op":"remove","path":"/spec/targetRefs/1"}]`); err != nil {
		t.Fatalf("remove the second target: %s", out)
	}
	awaitPolicyAncestor(t, policy, 2*time.Minute,
		"the synthetic ancestor going once the second target does",
		func(a ancestorReport) bool { return a.converged() })
	requireDigestUnchanged(t, agent, "the restored policy")
}

// requireRealGatewayAncestor reads the assayd Gateway's OWN ancestor entry,
// which readPolicyAncestor deliberately does not return when the synthetic one
// is present — because policyReport does not either. Only case (C) needs it,
// and it needs it precisely to show what the operator cannot see.
func requireRealGatewayAncestor(t *testing.T, name string) ancestorReport {
	t.Helper()
	out, err := kubectl(t, "get", "agentgatewaypolicy", name, "-n", sliceNS, "-o", "json")
	if err != nil {
		t.Fatalf("read policy %s/%s: %s", sliceNS, name, out)
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
				Conditions []struct {
					Type, Status, Reason, Message string
					ObservedGeneration            int64 `json:"observedGeneration"`
				} `json:"conditions"`
			} `json:"ancestors"`
		} `json:"status"`
	}
	if err := json.Unmarshal([]byte(out), &obj); err != nil {
		t.Fatalf("parse policy %s/%s: %v", sliceNS, name, err)
	}
	for _, a := range obj.Status.Ancestors {
		r := a.AncestorRef
		if r.Group != "gateway.networking.k8s.io" || r.Kind != "Gateway" ||
			r.Name != sliceGateway || r.Namespace != sliceGatewayNS {
			continue
		}
		rep := ancestorReport{Group: r.Group, Kind: r.Kind, Name: r.Name, Namespace: r.Namespace,
			Conds: map[string]ancestorCondition{}}
		for _, c := range a.Conditions {
			rep.Conds[c.Type] = ancestorCondition{Status: c.Status, Reason: c.Reason,
				Message: c.Message, ObservedGeneration: c.ObservedGeneration}
		}
		return rep
	}
	t.Fatalf("policy %s/%s carries no ancestor for Gateway %s/%s beside the synthetic one, so this "+
		"case has nothing to compare", sliceNS, name, sliceGatewayNS, sliceGateway)
	return ancestorReport{}
}
