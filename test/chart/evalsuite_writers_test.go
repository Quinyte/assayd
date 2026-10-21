// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package chart

import (
	"reflect"
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
		bspec := dig(b, "spec")
		if bspec["policyName"] != "assayd-evalsuite-writers" || !reflect.DeepEqual(toStrings(bspec["validationActions"]), []string{"Deny"}) {
			t.Errorf("%v: the binding does not deny under assayd-evalsuite-writers: %v", args, bspec)
		}

		spec := dig(p, "spec")
		if spec["failurePolicy"] != "Fail" {
			t.Errorf("%v: failurePolicy is %v; an authorizer that cannot answer must refuse the suite, not admit it",
				args, spec["failurePolicy"])
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
