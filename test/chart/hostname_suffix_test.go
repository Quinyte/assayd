// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package chart

import (
	"os/exec"
	"strings"
	"testing"
)

// gateway.hostnameSuffix ends every emitted route's hostname, and the HTTPRoute
// CRD refuses a hostname that is not a lowercase DNS name. The operator refuses
// such a suffix at startup, so the chart refuses to render it: an install fails
// at `helm install` with the value named, and not later as a crash-looping
// operator. assayd-gateway-routes' hostname rule compares against the same
// value, so a render that let the two differ would also leave that rule
// guarding hosts no route carries.
func TestANonLowercaseHostnameSuffixDoesNotRender(t *testing.T) {
	if _, err := exec.LookPath("helm"); err != nil {
		t.Fatal("helm is required to test the chart")
	}
	for _, suffix := range []string{"Agents.Example", "assayd.internal.", "-agents.example", "agents..example"} {
		out, err := exec.Command("helm", "template", "assayd", chartPath,
			"--set", "gateway.hostnameSuffix="+suffix).CombinedOutput()
		if err == nil {
			t.Errorf("the chart rendered with gateway.hostnameSuffix=%q; the operator refuses it at start, "+
				"and every route's hostname would fail the HTTPRoute CRD", suffix)
			continue
		}
		if !strings.Contains(string(out), "gateway.hostnameSuffix") {
			t.Errorf("the render of %q failed without naming gateway.hostnameSuffix: %s", suffix, out)
		}
	}
	// CONTROL: a lowercase suffix renders, so the refusals are about its form.
	if out, err := exec.Command("helm", "template", "assayd", chartPath,
		"--set", "gateway.hostnameSuffix=agents.example").CombinedOutput(); err != nil {
		t.Errorf("a lowercase gateway.hostnameSuffix no longer renders: %v\n%s", err, out)
	}
}
