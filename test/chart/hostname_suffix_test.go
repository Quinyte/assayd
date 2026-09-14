// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package chart

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/Quinyte/assayd/internal/controller"
)

// gateway.hostnameSuffix ends every emitted route's hostname, and the HTTPRoute
// CRD refuses a hostname that is not a lowercase DNS name. The operator refuses
// such a suffix at startup when the gateway is enabled, so the chart refuses to
// render it: an install fails at `helm install` with the value named, and not
// later as a crash-looping operator. assayd-gateway-routes' hostname rule
// compares against the same value, so a render that let the two differ would
// also leave that rule guarding hosts no route carries.
func TestANonLowercaseHostnameSuffixDoesNotRender(t *testing.T) {
	if _, err := exec.LookPath("helm"); err != nil {
		t.Fatal("helm is required to test the chart")
	}
	// The refusal suggests a suffix that would be accepted: the value
	// lower-cased and trimmed when that is valid, and the default otherwise.
	// It never repeats the refused value.
	for _, c := range []struct{ suffix, hint string }{
		{"Agents.Example", "agents.example"},
		{"agents.example.", "agents.example"},
		{"-agents.example", "agents.example"},
		{" agents.example", "agents.example"},
		{"agents..example", controller.DefaultGatewayHostnameSuffix},
		{"Agents_Example", controller.DefaultGatewayHostnameSuffix},
	} {
		out, err := exec.Command("helm", "template", "assayd", chartPath,
			"--set", "gateway.hostnameSuffix="+c.suffix).CombinedOutput()
		if err == nil {
			t.Errorf("the chart rendered with gateway.hostnameSuffix=%q; every route's hostname would fail "+
				"the HTTPRoute CRD", c.suffix)
			continue
		}
		if !strings.Contains(string(out), "gateway.hostnameSuffix") {
			t.Errorf("the render of %q failed without naming gateway.hostnameSuffix: %s", c.suffix, out)
		}
		if want := `such as "` + c.hint + `"`; !strings.Contains(string(out), want) {
			t.Errorf("the refusal of %q does not suggest a valid suffix (%s): %s", c.suffix, want, out)
		}
	}
	// CONTROL: a lowercase suffix renders, so the refusals are about its form.
	if out, err := exec.Command("helm", "template", "assayd", chartPath,
		"--set", "gateway.hostnameSuffix=agents.example").CombinedOutput(); err != nil {
		t.Errorf("a lowercase gateway.hostnameSuffix no longer renders: %v\n%s", err, out)
	}
}

// `--set` types a bare number or boolean, so `--set gateway.hostnameSuffix=123`
// arrives as an int64. `123` is a valid DNS name, and it must reach the
// operator and the route reservation as the same string, not fail the render
// on a type.
func TestANumericHostnameSuffixRendersAsItsString(t *testing.T) {
	for _, v := range []string{"123", "true"} {
		hosts := variable(t, routePolicy(t, "--set", "gateway.hostnameSuffix="+v), "hostsClear")
		if n := strings.Count(hosts, `"`+v+`"`); n != 3 {
			t.Errorf("with gateway.hostnameSuffix=%s the reservation clears hostnames against %s, want %q in "+
				"all three clauses", v, hosts, v)
		}
		args := operatorArgs(t, "--set", "gateway.enabled=true", "--set", "gateway.servingUrl=http://gw.example:8080",
			"--set", "gateway.hostnameSuffix="+v)
		if !contains(args, "--gateway-hostname-suffix="+v) {
			t.Errorf("with gateway.hostnameSuffix=%s the operator is given %v", v, args)
		}
	}
}
