// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package chart

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

// Design 03 §1.1's operator wiring, as the chart renders it: the grants the
// operator gets for the slice's `<agent>-auth` and for the gateway's NACK
// Events, the two reservations §6 and §3.4.4 owe to design 07, and the serving
// listener's address. What each of these DOES on a cluster is measured
// elsewhere: the reservations in test/envtest (admission_reservation_test.go)
// and in the e2e; the grants by SubjectAccessReview in the e2e. These pin what
// the chart says, so that a narrowing cannot quietly widen.

const operatorSA = "system:serviceaccount:assayd-system:assayd-agent-operator"

func byName(docs []map[string]any, kind string) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, d := range kindsOf(docs, kind) {
		out[nameOf(d)] = d
	}
	return out
}

func variable(t *testing.T, policy map[string]any, name string) string {
	t.Helper()
	for _, v := range toList(dig(policy, "spec")["variables"]) {
		vm, _ := v.(map[string]any)
		if vm["name"] == name {
			return toStr(vm["expression"])
		}
	}
	t.Fatalf("policy %s has no variable %q", nameOf(policy), name)
	return ""
}

func TestTheChartReservesGatewayPoliciesAndKeySetsInRunNamespaces(t *testing.T) {
	docs := render(t)
	policies := byName(docs, "ValidatingAdmissionPolicy")
	bindings := byName(docs, "ValidatingAdmissionPolicyBinding")

	for name, want := range map[string]struct{ group, resource string }{
		"assayd-gateway-policies": {"agentgateway.dev", "agentgatewaypolicies"},
		"assayd-api-keys":         {"", "configmaps"},
	} {
		p := policies[name]
		if p == nil {
			t.Errorf("the chart renders no ValidatingAdmissionPolicy %s (design 03 §6, §3.4.4)", name)
			continue
		}
		b := bindings[name]
		if b == nil || toStr(dig(b, "spec")["policyName"]) != name {
			t.Errorf("policy %s has no binding naming it, so it validates nothing", name)
		} else if got := toStrings(dig(b, "spec")["validationActions"]); !reflect.DeepEqual(got, []string{"Deny"}) {
			t.Errorf("binding %s has validationActions %v; a reservation that only warns reserves nothing", name, got)
		}
		spec := dig(p, "spec")
		if spec["failurePolicy"] != "Fail" {
			t.Errorf("policy %s has failurePolicy %v; an unavailable admission chain must deny, not admit",
				name, spec["failurePolicy"])
		}
		if _, has := spec["paramKind"]; has {
			t.Errorf("policy %s declares a paramKind; a params object that goes missing turns a "+
				"reservation into a denial of every write it matches (design 07 A5.9)", name)
		}
		// Run namespaces, by the label the namespace-label policy reserves to
		// the operator — not every namespace, and not a name prefix a tenant
		// could create.
		sel := dig(dig(spec, "matchConstraints"), "namespaceSelector")
		if got := dig(sel, "matchLabels"); !reflect.DeepEqual(got, map[string]any{"assayd.dev/run-namespace": "true"}) {
			t.Errorf("policy %s matches namespaces by %v, want exactly assayd.dev/run-namespace=true", name, got)
		}
		rules := toList(dig(spec, "matchConstraints")["resourceRules"])
		if len(rules) != 1 {
			t.Fatalf("policy %s has %d resource rules, want 1", name, len(rules))
		}
		rule, _ := rules[0].(map[string]any)
		if got := toStrings(rule["apiGroups"]); !reflect.DeepEqual(got, []string{want.group}) {
			t.Errorf("policy %s matches API groups %v, want [%q]", name, got, want.group)
		}
		// Exactly the resource: a policy's /status is written by the gateway's
		// controller, and matching it would refuse that controller.
		if got := toStrings(rule["resources"]); !reflect.DeepEqual(got, []string{want.resource}) {
			t.Errorf("policy %s matches %v, want exactly [%s]", name, got, want.resource)
		}
		ops := toStrings(rule["operations"])
		sort.Strings(ops)
		if !reflect.DeepEqual(ops, []string{"CREATE", "UPDATE"}) {
			t.Errorf("policy %s matches operations %v, want CREATE and UPDATE (design 03 §6). An UPDATE "+
				"left out lets anyone rewrite what the operator wrote; a DELETE added refuses the "+
				"namespace controller when a run namespace is torn down", name, ops)
		}
	}

	// An UPDATE is refused only when it changes what is reserved, so the
	// garbage collector can finish a foreground delete (measured in envtest).
	for _, name := range []string{"assayd-gateway-policies", "assayd-api-keys"} {
		if p := policies[name]; p != nil {
			touched := variable(t, p, "touched")
			if !strings.Contains(touched, "oldObject == null") {
				t.Errorf("%s's touched does not treat a CREATE as a write: %s", name, touched)
			}
		}
	}

	// Policies: the operator, and nobody else. Not an administrator either:
	// §6 says "from any identity but the operator's".
	if p := policies["assayd-gateway-policies"]; p != nil {
		want := `request.userInfo.username in ["` + operatorSA + `"]`
		if got := variable(t, p, "operator"); got != want {
			t.Errorf("assayd-gateway-policies permits %s, want exactly %s", got, want)
		}
	}
	// Key sets: the operator AND the administrators, because §3.4.4 says an
	// administrator writes the key set. The default administrator is
	// system:masters, the group this reservation could not exclude anyway.
	if p := policies["assayd-api-keys"]; p != nil {
		writer := variable(t, p, "writer")
		for _, want := range []string{`"` + operatorSA + `"`, `"system:masters"`} {
			if !strings.Contains(writer, want) {
				t.Errorf("assayd-api-keys does not permit %s: %s", want, writer)
			}
		}
		// The label is reserved on the old object as well as the new one, so
		// taking it off a key set is a write to a key set.
		if labelled := variable(t, p, "labelled"); !strings.Contains(labelled, "oldObject") {
			t.Errorf("assayd-api-keys looks only at the new object, so removing the label from a "+
				"key set, then rewriting it, slips past: %s", labelled)
		}
	}
}

// admission.apiKeyWriters and admission.extraOperators must REACH the rendered
// CEL. A value that renders nothing is a reservation an administrator believes
// they have configured.
func TestTheKeyWritersReachTheReservation(t *testing.T) {
	docs := render(t,
		"--set", "admission.apiKeyWriters.users={alice}",
		"--set", "admission.apiKeyWriters.groups={key-admins}",
		"--set", "admission.extraOperators={system:serviceaccount:tenancy:tenant-operator}")
	policies := byName(docs, "ValidatingAdmissionPolicy")
	writer := variable(t, policies["assayd-api-keys"], "writer")
	for _, want := range []string{`"alice"`, `"key-admins"`, `"` + operatorSA + `"`,
		`"system:serviceaccount:tenancy:tenant-operator"`} {
		if !strings.Contains(writer, want) {
			t.Errorf("the key reservation does not permit %s: %s", want, writer)
		}
	}
	if strings.Contains(writer, "system:masters") {
		t.Errorf("groups were set to [key-admins] and system:masters is still permitted: %s", writer)
	}
	// A user is not a group, and a group is not a user.
	if strings.Contains(writer, `g in ["alice"`) || strings.Contains(writer, `username in ["key-admins"`) {
		t.Errorf("users and groups are rendered into each other's lists: %s", writer)
	}
	// Key writers are not policy authors.
	if op := variable(t, policies["assayd-gateway-policies"], "operator"); strings.Contains(op, "alice") {
		t.Errorf("a key writer may author AgentgatewayPolicies: %s", op)
	}
}

// The grant is exactly the verbs the code calls (design 03 §3.2): the watch's
// `list` and `watch`, the transaction's and the finalizer's `get`, the
// `Create` transaction's `create` and `update`, and the finalizer's `delete`.
// Never `patch`, because the compiler writes over owned fields and never by
// server-side apply. No AgentgatewayBackend grant at all.
func TestTheOperatorMayWriteItsPolicyAndNotPatchOne(t *testing.T) {
	verbs := map[string][]string{}
	for _, role := range kindsOf(render(t), "ClusterRole") {
		for _, r := range toList(role["rules"]) {
			rule, _ := r.(map[string]any)
			for _, g := range toStrings(rule["apiGroups"]) {
				for _, res := range toStrings(rule["resources"]) {
					verbs[g+"/"+res] = append(verbs[g+"/"+res], toStrings(rule["verbs"])...)
				}
			}
		}
	}
	got := verbs["agentgateway.dev/agentgatewaypolicies"]
	sort.Strings(got)
	if want := []string{"create", "delete", "get", "list", "update", "watch"}; !reflect.DeepEqual(got, want) {
		t.Errorf("the ClusterRole grants %v on agentgatewaypolicies, want exactly %v", got, want)
	}
	if v, ok := verbs["agentgateway.dev/agentgatewaybackends"]; ok {
		t.Errorf("the ClusterRole grants %v on agentgatewaybackends, which nothing reads or writes", v)
	}
	// Events stay write-only cluster-wide. The NACK read is a Role in one
	// namespace, below; a `list` here would read every Event in the cluster.
	ev := verbs["/events"]
	sort.Strings(ev)
	if !reflect.DeepEqual(ev, []string{"create", "patch"}) {
		t.Errorf("the ClusterRole grants %v on events, want exactly [create patch]", ev)
	}
}

func TestTheNackReadIsARoleInTheGatewaysNamespace(t *testing.T) {
	const role = "assayd-agent-operator-gateway-events"
	if r := byName(render(t), "Role")[role]; r != nil {
		t.Errorf("gateway.enabled is false and the chart still grants a read of Events in %s; "+
			"no watch is registered then, so the grant has no consumer",
			toStr(dig(r, "metadata")["namespace"]))
	}

	for _, tc := range []struct {
		name string
		args []string
		ns   string
	}{
		{"the Gateway's own namespace", []string{"--set", "gateway.enabled=true", "--set", "gateway.namespace=gw-elsewhere"}, "gw-elsewhere"},
		{"unset falls back to the operator's, as admission.yaml does", []string{"--set", "gateway.enabled=true"}, "assayd-system"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			docs := render(t, tc.args...)
			r := byName(docs, "Role")[role]
			if r == nil {
				t.Fatalf("gateway.enabled is true and no Role %s is rendered, so the NACK watch is "+
					"Forbidden on a real cluster and design 03 §3.3's sixth input never arrives", role)
			}
			if got := toStr(dig(r, "metadata")["namespace"]); got != tc.ns {
				t.Errorf("the Role is in %q, want %q: the watch is in the namespace the operator is "+
					"told is the Gateway's", got, tc.ns)
			}
			want := []any{map[string]any{"apiGroups": []any{""}, "resources": []any{"events"},
				"verbs": []any{"list", "watch"}}}
			if !reflect.DeepEqual(r["rules"], want) {
				t.Errorf("the Role grants %v, want exactly list and watch on events", r["rules"])
			}
			b := byName(docs, "RoleBinding")[role]
			if b == nil {
				t.Fatalf("no RoleBinding %s, so the Role grants nobody anything", role)
			}
			if got := toStr(dig(b, "metadata")["namespace"]); got != tc.ns {
				t.Errorf("the RoleBinding is in %q, want %q", got, tc.ns)
			}
			if got := toStr(dig(b, "roleRef")["name"]); got != role {
				t.Errorf("the RoleBinding binds %q, want %q", got, role)
			}
			subjects := toList(b["subjects"])
			want0 := map[string]any{"kind": "ServiceAccount", "name": "assayd-agent-operator", "namespace": "assayd-system"}
			if len(subjects) != 1 || !reflect.DeepEqual(subjects[0], want0) {
				t.Errorf("the RoleBinding's subjects are %v, want exactly the operator's ServiceAccount", subjects)
			}
		})
	}
}

// gateway.servingUrl reaches the operator as --gateway-serving-url, and only
// when set: nothing requires it until the compiler runs (design 03 §3.3.3).
func TestTheServingURLReachesTheOperatorOnlyWhenSet(t *testing.T) {
	if args := operatorArgs(t); containsPrefix(args, "--gateway-serving-url") {
		t.Errorf("the default install passes %v; with no gateway.servingUrl nothing is passed", args)
	}
	args := operatorArgs(t, "--set", "gateway.servingUrl=http://gw.example:8080")
	if !contains(args, "--gateway-serving-url=http://gw.example:8080") {
		t.Errorf("gateway.servingUrl did not reach the operator: %v", args)
	}
}
