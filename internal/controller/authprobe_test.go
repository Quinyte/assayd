// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// The probe, as it is sent, never goes through a proxy from the operator's
// environment. A proxy that answered 401 would pass for the Agent's -auth.
//
// Run in a child process, because net/http reads HTTP_PROXY once per process
// and skips any proxy for a loopback host. The child probes a hostname that is
// not loopback and does not resolve, with HTTP_PROXY set to a proxy that
// answers 401: sent directly, the probe gets no answer; through the proxy, it
// gets 401. A control request through the default client, in the same child,
// must get the 401, or the test would prove nothing.
func TestTheProbeNeverTakesAProxyFromTheEnvironment(t *testing.T) {
	const target = "http://gateway.probe.invalid:8080/.well-known/agent-card.json"
	if os.Getenv("ASSAYD_PROBE_PROXY_CHILD") == "1" {
		a, err := httpAuthProber{timeout: 2 * time.Second}.Probe(context.Background(),
			AuthProbeRequest{URL: target, Host: "pricer.payments.assayd.internal"})
		fmt.Printf("PROBE code=%d failed=%v\n", a.Code, err != nil)
		control := 0
		if resp, err := http.Get(target); err == nil {
			control = resp.StatusCode
			_ = resp.Body.Close()
		}
		fmt.Printf("CONTROL code=%d\n", control)
		return
	}
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer proxy.Close()
	cmd := exec.Command(os.Args[0], "-test.run=^TestTheProbeNeverTakesAProxyFromTheEnvironment$", "-test.count=1")
	cmd.Env = append(os.Environ(), "ASSAYD_PROBE_PROXY_CHILD=1",
		"HTTP_PROXY="+proxy.URL, "http_proxy="+proxy.URL, "NO_PROXY=", "no_proxy=")
	out, _ := cmd.CombinedOutput()
	if !strings.Contains(string(out), "CONTROL code=401") {
		t.Fatalf("the control request did not go through the environment's proxy, so this test "+
			"cannot show the probe avoids it:\n%s", out)
	}
	if !strings.Contains(string(out), "PROBE code=0 failed=true") {
		t.Errorf("the probe went through HTTP_PROXY, or answered otherwise than a direct request "+
			"to a host that does not resolve:\n%s", out)
	}
}

// The production probe: one anonymous GET to the serving listener, carrying
// the Agent's Host header, bounded by its timeout, following no redirect, and
// never through a proxy from the operator's environment.
func TestTheHTTPProbeAsksTheListenerItselfAndNothingElse(t *testing.T) {
	var gotHost, gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/slow":
			time.Sleep(2 * time.Second)
		case "/moved":
			http.Redirect(w, r, "/elsewhere", http.StatusFound)
			return
		case "/elsewhere":
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		gotHost, gotAuth, gotPath = r.Host, r.Header.Get("Authorization"), r.URL.Path
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	p := httpAuthProber{timeout: 300 * time.Millisecond}
	ctx := context.Background()

	a, err := p.Probe(ctx, AuthProbeRequest{URL: srv.URL + "/.well-known/agent-card.json",
		Host: "pricer.payments.assayd.internal"})
	if err != nil || a.Code != http.StatusUnauthorized {
		t.Fatalf("probe = %+v, %v; want 401", a, err)
	}
	if gotHost != "pricer.payments.assayd.internal" {
		t.Errorf("the probe sent Host %q; the serving route matches on the Agent's hostname", gotHost)
	}
	if gotAuth != "" || gotPath != "/.well-known/agent-card.json" {
		t.Errorf("the probe is not anonymous on the card path: Authorization=%q path=%q", gotAuth, gotPath)
	}

	// A redirect is an answer to record, not one to follow: following it would
	// credit another host's 401 to this route.
	if a, err := p.Probe(ctx, AuthProbeRequest{URL: srv.URL + "/moved", Host: "x"}); err != nil ||
		a.Code != http.StatusFound {
		t.Errorf("a redirect was followed or failed: %+v, %v; want the 302 itself", a, err)
	}

	start := time.Now()
	if _, err := p.Probe(ctx, AuthProbeRequest{URL: srv.URL + "/slow", Host: "x"}); err == nil {
		t.Error("a listener slower than the timeout answered the probe")
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("the probe waited %s, past its %s timeout, on the reconcile worker", d, p.timeout)
	}

	// No proxy: HTTP_PROXY in the operator's environment must not put another
	// hop between the probe and the Gateway, whose 401 would pass for the
	// Agent's -auth.
	if probeTransport.Proxy != nil {
		t.Error("the probe's transport takes a proxy; it must reach the serving listener directly")
	}
}
