// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package chart

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

// Design 16 §1.1, Q6 (c), as the chart renders it: assayd-evalsuite-writers
// admits an EvalSuite create or update only from a principal the authorizer
// says may `update` Agents in the suite's namespace. What the rule DOES for
// each identity is measured against a real API server in test/envtest
// (evalsuite_writers_test.go) and on the e2e's k3s (test/e2e); this pins what
// the chart says, so that the measured rule is the shipped one.

// The check, exactly: namespace-wide `update` on agents, asked with no name.
const evalSuiteWriterCheck = "authorizer.group('assayd.dev').resource('agents')" +
	".namespace(request.namespace).check('update').allowed()"

func TestTheChartReservesEvalSuiteWritesToAgentEditors(t *testing.T) {
	// extraOperators set, so a render that exempted them would show it: Q6 (c)
	// exempts no one.
	for _, args := range [][]string{nil, {"--set", "admission.extraOperators={tenant-operator}"}} {
		docs := render(t, args...)
		p := byName(docs, "ValidatingAdmissionPolicy")["assayd-evalsuite-writers"]
		if p == nil {
			t.Fatalf("%v: the chart renders no ValidatingAdmissionPolicy assayd-evalsuite-writers, so anyone "+
				"who may write an EvalSuite invokes the namespace's held candidates (design 16 §1.1, Q6)", args)
		}
		b := byName(docs, "ValidatingAdmissionPolicyBinding")["assayd-evalsuite-writers"]
		if b == nil {
			t.Fatalf("%v: assayd-evalsuite-writers has no binding, so it validates nothing", args)
		}
		// Exact key sets: anything else a policy or binding can carry --
		// matchConditions, an objectSelector, a namespaceSelector, a binding's
		// matchResources or paramRef -- narrows what the check reaches, which
		// exempts whoever it leaves out.
		keys(t, args, "the binding's spec", dig(b, "spec"), "policyName", "validationActions")
		keys(t, args, "the policy's spec", dig(p, "spec"), "failurePolicy", "matchConstraints", "validations")
		keys(t, args, "the policy's matchConstraints", dig(p, "spec", "matchConstraints"), "resourceRules")
		bspec := dig(b, "spec")
		if bspec["policyName"] != "assayd-evalsuite-writers" || !reflect.DeepEqual(toStrings(bspec["validationActions"]), []string{"Deny"}) {
			t.Errorf("%v: the binding does not deny under assayd-evalsuite-writers: %v", args, bspec)
		}

		spec := dig(p, "spec")
		if spec["failurePolicy"] != "Fail" {
			// An authorizer error is already a deny: the `authorizer` library
			// turns it into a decision whose allowed() is false. failurePolicy
			// governs the rest -- a CEL runtime error, a policy the API server
			// cannot compile or bind -- and Ignore would admit every suite then.
			t.Errorf("%v: failurePolicy is %v; a policy that errors or is misconfigured must refuse the suite, "+
				"not admit it", args, spec["failurePolicy"])
		}
		if _, has := spec["paramKind"]; has {
			t.Errorf("%v: the policy declares a paramKind; a params object that goes missing changes who may write", args)
		}
		mc := dig(spec, "matchConstraints")
		if _, has := mc["namespaceSelector"]; has {
			t.Errorf("%v: the policy has a namespaceSelector, but a suite lives in its Agent's namespace, which "+
				"carries no label of assayd's: a suite outside the selector is unguarded", args)
		}
		rules := toList(mc["resourceRules"])
		if len(rules) != 1 {
			t.Fatalf("%v: want one resource rule, got %v", args, rules)
		}
		r, _ := rules[0].(map[string]any)
		if !reflect.DeepEqual(toStrings(r["apiGroups"]), []string{"assayd.dev"}) ||
			!reflect.DeepEqual(toStrings(r["resources"]), []string{"evalsuites"}) {
			t.Errorf("%v: the policy matches %v, want evalsuites.assayd.dev", args, r)
		}
		// Exactly these keys: resourceNames or scope would narrow the suites
		// judged, and apiVersions anything but "*" would miss a served version.
		keys(t, args, "the policy's resource rule", r, "apiGroups", "apiVersions", "operations", "resources")
		if got := toStrings(r["apiVersions"]); !reflect.DeepEqual(got, []string{"*"}) {
			t.Errorf("%v: the policy matches apiVersions %v, want [*]", args, got)
		}
		// CREATE and UPDATE; DELETE is RBAC's, as for every other reservation.
		if got := toStrings(r["operations"]); !reflect.DeepEqual(got, []string{"CREATE", "UPDATE"}) {
			t.Errorf("%v: the policy judges %v, want exactly CREATE and UPDATE", args, got)
		}
		// One validation, the check exactly: no identity is exempt from it.
		if vs := toList(spec["variables"]); len(vs) != 0 {
			t.Errorf("%v: the policy declares variables %v; the check is the whole rule", args, vs)
		}
		vals := toList(spec["validations"])
		if len(vals) != 1 {
			t.Fatalf("%v: want one validation, got %v", args, vals)
		}
		v, _ := vals[0].(map[string]any)
		if v["expression"] != evalSuiteWriterCheck {
			t.Errorf("%v: the validation is %v, want exactly %s", args, v["expression"], evalSuiteWriterCheck)
		}
		for _, field := range []string{"message", "messageExpression"} {
			if m := toStr(v[field]); !strings.Contains(m, "`update` on agents.assayd.dev") ||
				!strings.Contains(m, "ask an Agent editor") {
				t.Errorf("%v: the refusal's %s does not name the fix: %q", args, field, m)
			}
		}
	}
}

// keys fails unless m holds exactly the keys want.
func keys(t *testing.T, args []string, what string, m map[string]any, want ...string) {
	t.Helper()
	var got []string
	for k := range m {
		got = append(got, k)
	}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%v: %s has keys %v, want exactly %v", args, what, got, want)
	}
}

// The policy exempts no one, and the operator passes its check: the rendered
// ClusterRole lets it update every Agent. What keeps the operator from writing
// an EvalSuite is this RBAC, `get` and nothing else on evalsuites (design 16
// §1.1, "What the slice depends on"), so it is pinned here.
func TestTheOperatorOnlyReadsEvalSuites(t *testing.T) {
	var verbs []string
	for _, role := range kindsOf(render(t), "ClusterRole") {
		for _, r := range toList(role["rules"]) {
			rule, _ := r.(map[string]any)
			for _, g := range toStrings(rule["apiGroups"]) {
				for _, res := range toStrings(rule["resources"]) {
					if (g == "assayd.dev" || g == "*") && (res == "evalsuites" || res == "*") {
						verbs = append(verbs, toStrings(rule["verbs"])...)
					}
				}
			}
		}
	}
	if !reflect.DeepEqual(verbs, []string{"get"}) {
		t.Errorf("the chart's ClusterRoles grant %v on evalsuites, want exactly [get]: the operator passes "+
			"assayd-evalsuite-writers' check, so RBAC alone keeps it from writing a suite", verbs)
	}
}
