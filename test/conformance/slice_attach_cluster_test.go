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
// These two cases separate them. Both start from a SERVED Agent — route
// published on a backend, `<agent>-auth` attached, anonymous requests refused —
// and break the policy alone, leaving the route accepted at its current
// generation. They differ in which of §3.3.2's tuple breaks they produce, and
// they answer the question §8.1 asks of this case with two different numbers:
//
//   - the route's `<agent>-auth` reported NOT ATTACHED, on the synthetic
//     `StatusSummary` ancestor: the route answers 200 to an anonymous request.
//     A live authentication bypass, which is what A80's fail-open half fears.
//     Reaching it needs an EDIT to the policy's own spec, so the policy no
//     longer renders to `status.auth.appliedDigest` and A80's own precondition
//     excludes it.
//   - the policy reported `Accepted=True` with reason `PartiallyValid`, while
//     it stays attached: the route keeps answering 401. Reaching it needs no
//     edit to the policy at all — an administrator's key ConfigMap does it —
//     so this is the break A80's precondition admits, and it is fail-CLOSED.
//
// Measured on agentgateway 1.5.0; recorded, with what they do and do not show,
// in docs/research/a80-policy-half-conformance-2026-09.md.
package conformance

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/Quinyte/assayd/internal/compiler"
	"github.com/Quinyte/assayd/internal/controller"
)

// ancestorCondition is one condition on one policy ancestor, carrying the two
// fields §3.3.2's tuple reads beyond the status: the REASON, which separates
// `Valid` from every translation failure agentgateway still calls accepted,
// and the GENERATION it was observed at, which is what makes the report
// evidence about the object as it stands rather than a stale echo of it.
type ancestorCondition struct {
	Status             string
	Reason             string
	Message            string
	ObservedGeneration int64
}

// ancestorReport is a policy's first ancestor: the ref that discriminates the
// real Gateway from agentgateway's synthetic `StatusSummary` (§3.3.2), and its
// conditions.
type ancestorReport struct {
	Group, Kind, Name string
	Conds             map[string]ancestorCondition
}

// broken says whether this report is one of the four §5 calls a broken tuple:
// `Accepted=False`, `Accepted=True` with a reason other than `Valid`,
// `Attached=False`, or the synthetic `StatusSummary` ancestor.
func (a ancestorReport) broken() bool {
	acc := a.Conds["Accepted"]
	return acc.Status != "True" || acc.Reason != "Valid" ||
		a.Conds["Attached"].Status != "True" || a.Name == "StatusSummary"
}

func (a ancestorReport) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "ancestor %s/%s %s", a.Group, a.Kind, a.Name)
	for _, k := range []string{"Accepted", "Attached"} {
		if c, ok := a.Conds[k]; ok {
			fmt.Fprintf(&b, "; %s=%s reason=%s gen=%d (%s)", k, c.Status, c.Reason,
				c.ObservedGeneration, tail(c.Message, 1))
		}
	}
	return b.String()
}

// awaitPolicyAncestor polls a policy until its first ancestor's conditions were
// all observed at the policy's CURRENT generation and `want` accepts the
// report, then returns it.
//
// The generation gate is statusIsCurrent's rule, applied here for the same
// reason: a report read immediately after a patch is the previous reconcile's
// verdict. It is applied per READ rather than once, because a policy whose
// translation fails can leave one condition a generation behind the other —
// measured on 1.5.0 for an unparseable CEL expression, where `Accepted` moved
// to the new generation and `Attached` stayed on the old one — so a test that
// waited for a whole current ancestor there would wait forever. Both cases
// below reach reports whose conditions are current together; this helper says
// so rather than assuming it.
func awaitPolicyAncestor(t *testing.T, name string, timeout time.Duration,
	why string, want func(ancestorReport) bool) ancestorReport {
	t.Helper()
	var last string
	for deadline := time.Now().Add(timeout); time.Now().Before(deadline); {
		got, ok := readPolicyAncestor(t, name)
		if ok {
			last = got.String()
			if want(got) {
				return got
			}
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("%s: policy %s/%s never published the report this case is about in %s; "+
		"the last current one was %s", why, sliceNS, name, timeout, last)
	return ancestorReport{}
}

// readPolicyAncestor reads one policy's first ancestor. It reports ok=false
// while the object has no ancestor, or while any of that ancestor's conditions
// was observed at another generation.
func readPolicyAncestor(t *testing.T, name string) (ancestorReport, bool) {
	t.Helper()
	out, err := kubectl(t, "get", "agentgatewaypolicy", name, "-n", sliceNS, "-o", "json")
	if err != nil {
		return ancestorReport{}, false
	}
	var obj struct {
		Metadata struct {
			Generation int64 `json:"generation"`
		} `json:"metadata"`
		Status struct {
			Ancestors []struct {
				AncestorRef struct {
					Group, Kind, Name string
				} `json:"ancestorRef"`
				Conditions []struct {
					Type, Status, Reason, Message string
					ObservedGeneration            int64 `json:"observedGeneration"`
				} `json:"conditions"`
			} `json:"ancestors"`
		} `json:"status"`
	}
	if json.Unmarshal([]byte(out), &obj) != nil || len(obj.Status.Ancestors) == 0 {
		return ancestorReport{}, false
	}
	a := obj.Status.Ancestors[0]
	rep := ancestorReport{
		Group: a.AncestorRef.Group, Kind: a.AncestorRef.Kind, Name: a.AncestorRef.Name,
		Conds: map[string]ancestorCondition{},
	}
	if len(a.Conditions) == 0 {
		return ancestorReport{}, false
	}
	for _, c := range a.Conditions {
		if c.ObservedGeneration != obj.Metadata.Generation {
			return ancestorReport{}, false
		}
		rep.Conds[c.Type] = ancestorCondition{
			Status: c.Status, Reason: c.Reason, Message: c.Message,
			ObservedGeneration: c.ObservedGeneration,
		}
	}
	return rep, true
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
					Name, Namespace string
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
		if p.ControllerName != controller.AgentgatewayControllerName ||
			p.ParentRef.Name != sliceGateway || p.ParentRef.Namespace != sliceGatewayNS {
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
// renders to the digest the compiler gave for this Agent — A80's precondition,
// "renders to `status.auth.appliedDigest`", checked against the live object.
// It is what separates a state the operator's judgement would enter from one
// its own drift check would overwrite first.
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
// A80's precondition admits, the report is a reporting event and NOT the live
// authentication bypass its message announces. The bypass is case (B) below,
// which the same precondition excludes.
func TestSliceAPolicyBrokenByItsKeySetStaysAttachedAndKeepsRefusing(t *testing.T) {
	gw := sliceFixture(t)
	agent := agentName("brokenkeys")
	host := publishedWithAuth(t, gw, agent)
	route, _ := compiler.ServingRouteName(agent)
	policy, _ := compiler.AuthPolicyName(agent)

	// A second labelled key set, never the shared one: the shared one is every
	// other slice case's, and a json-patch that half-failed would leave it
	// broken for them.
	const badKeys = "conf-slice-rejected-keys"
	if err := apply(t, fmt.Sprintf(`
apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: %s
  labels: {%s: %q}
data:
  conf-rejected-entry: '{"key":"plaintext-not-a-hash"}'`,
		badKeys, sliceNS, compiler.APIKeySourceLabel, compiler.APIKeySourceValue)); err != nil {
		t.Fatal(err)
	}
	deleteAndWaitLater(t, "configmap", sliceNS, badKeys)

	// How long the controller takes to notice a ConfigMap it selects by label
	// is a re-list, not a watch on a named object, and on a cold cluster it has
	// been the slowest step here. The timeout is what it is because of this
	// number; log it so a later run that gets close says so before it flakes.
	reported := time.Now()
	rep := awaitPolicyAncestor(t, policy, 2*time.Minute,
		"a served <agent>-auth whose key source carries a rejected entry",
		func(a ancestorReport) bool { return a.Conds["Accepted"].Reason == "PartiallyValid" })
	t.Logf("the break as agentgateway reports it, %s after the ConfigMap was written: %s",
		time.Since(reported).Round(time.Millisecond), rep)
	if !rep.broken() {
		t.Fatalf("the report is not one §5 calls a broken tuple: %s", rep)
	}
	// The ancestor is the REAL Gateway, and the policy is still attached: this
	// is the reported-broken-but-attached shape, not case (B)'s.
	if rep.Name == "StatusSummary" || rep.Conds["Attached"].Status != "True" {
		t.Fatalf("this case is the ATTACHED break; agentgateway reported the unattached one "+
			"instead, which is case (B)'s state: %s", rep)
	}
	if !strings.Contains(rep.Conds["Accepted"].Message, badKeys) {
		t.Errorf("the reported reason does not name the ConfigMap that caused it, so an "+
			"administrator cannot find it: %q", rep.Conds["Accepted"].Message)
	}

	// The two facts that make this A80's POLICY half and not its route half.
	requireRouteAccepted(t, route, "the key-set break")
	requireDigestUnchanged(t, agent, "the key-set break")

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
	healed := awaitPolicyAncestor(t, policy, 2*time.Minute,
		"the report clearing once the rejected entry is gone",
		func(a ancestorReport) bool { return !a.broken() })
	if healed.Name == "StatusSummary" {
		t.Fatalf("the report cleared to the synthetic ancestor: %s", healed)
	}
	expectCode(t, gw, servingPort, host, cardPath, keyTeam, 200,
		"the admitted key after the rejected key set is gone")
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
//   - Reaching it took an EDIT to the policy's spec, which no other stimulus
//     tried avoided (docs/research/a80-policy-half-conformance-2026-09.md), so
//     the live policy no longer renders to `status.auth.appliedDigest` and
//     A80's own precondition would not judge it. The case asserts that too, as
//     the finding it is: the state exists, and the operator's judgement of it
//     is not reachable through this stimulus.
func TestSliceAnUnattachedAuthPolicyLeavesAnAcceptedRouteOpen(t *testing.T) {
	gw := sliceFixture(t)
	agent := agentName("unattached")
	host := publishedWithAuth(t, gw, agent)
	route, _ := compiler.ServingRouteName(agent)
	policy, _ := compiler.AuthPolicyName(agent)

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
	if rep.Name != "StatusSummary" {
		t.Errorf("§3.3.2 says a failure emits the synthetic StatusSummary ancestor, and the "+
			"unattached report names %q instead: %s", rep.Name, rep)
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
	body := expectCode(t, gw, servingPort, host, cardPath, "", 200,
		"the anonymous request again, for the body: the 200 must be the BACKEND answering and "+
			"not the gateway").body
	if strings.TrimSpace(body) == "" {
		t.Errorf("the anonymous 200 carried no body, so it is not evidence that the request " +
			"reached the agent's backend")
	}

	// A80's precondition, checked against the live object: this state is NOT
	// one the operator's policy half would judge, because reaching it changed
	// the policy's spec.
	want, err := compiler.Digest(authPolicy(t, agent))
	if err != nil {
		t.Fatal(err)
	}
	got, err := compiler.Digest(livePolicy(t, policy, "the edited policy"))
	if err != nil {
		t.Fatal(err)
	}
	if got == want {
		t.Fatalf("the edited policy still digests to the compiler's %s. If that ever becomes "+
			"true, A80's policy half is reachable through this stimulus and the finding in "+
			"docs/research/a80-policy-half-conformance-2026-09.md is stale", want)
	}

	// Removing the sectionName restores the refusal, which is what makes the
	// 200 above the policy's absence and not something else the case broke.
	if out, err := kubectl(t, "patch", "agentgatewaypolicy", policy, "-n", sliceNS,
		"--type=json", "-p", `[{"op":"remove","path":"/spec/targetRefs/0/sectionName"}]`); err != nil {
		t.Fatalf("restore <agent>-auth's target: %s", out)
	}
	healed := awaitPolicyAncestor(t, policy, 2*time.Minute,
		"the policy attaching again once its target resolves",
		func(a ancestorReport) bool { return !a.broken() })
	if healed.Name == "StatusSummary" {
		t.Fatalf("the policy healed onto the synthetic ancestor: %s", healed)
	}
	awaitCode(t, gw, servingPort, host, cardPath, "", 401, []int{200}, 2*time.Minute,
		"the anonymous request once <agent>-auth attaches again")
	requireDigestUnchanged(t, agent, "the restored policy")
}
