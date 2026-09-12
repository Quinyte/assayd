// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package chart

import (
	"os/exec"
	"strings"
	"testing"
)

// The compiler runs whenever the gateway is enabled, and the operator refuses
// to start without --gateway-serving-url (design 03 §3.3.3). The chart refuses
// to render that combination, so an install fails at `helm install` with the
// value named, and not later as a crash-looping operator.
func TestAnEnabledGatewayWithNoServingURLDoesNotRender(t *testing.T) {
	if _, err := exec.LookPath("helm"); err != nil {
		t.Fatal("helm is required to test the chart")
	}
	out, err := exec.Command("helm", "template", "assayd", chartPath,
		"--set", "gateway.enabled=true").CombinedOutput()
	if err == nil {
		t.Fatal("the chart rendered with gateway.enabled=true and no gateway.servingUrl; the " +
			"operator it installs refuses to start")
	}
	if !strings.Contains(string(out), "gateway.servingUrl") {
		t.Errorf("the render failed without naming gateway.servingUrl: %s", out)
	}
	// The default, gateway off, still renders with no URL.
	if out, err := exec.Command("helm", "template", "assayd", chartPath).CombinedOutput(); err != nil {
		t.Errorf("the default install no longer renders: %v\n%s", err, out)
	}
}
