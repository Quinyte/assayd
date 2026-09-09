// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// The card must satisfy what design 02 §3.4 validates, and the validation does
// not exist yet — so this pins the fixture's half of the contract now, and the
// operator's half can be written against something real rather than a guess.
func TestTheCardCarriesWhatTheOperatorValidates(t *testing.T) {
	t.Setenv("AGENT_NAME", "pa-reviewer")
	srv := httptest.NewServer(handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/.well-known/agent-card.json")
	if err != nil {
		t.Fatalf("fetch card: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("card status %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("card content-type %q", ct)
	}

	var c card
	if err := json.NewDecoder(resp.Body).Decode(&c); err != nil {
		t.Fatalf("the card is not parseable, which is the first thing §3.4 checks: %v", err)
	}
	if c.Name != "pa-reviewer" {
		t.Errorf("card name is %q; §3.4 fails registration when it does not match the CR, so "+
			"the fixture must take it from the environment the operator sets", c.Name)
	}
	// The version lives inside supportedInterfaces[], not at the top level. This
	// fixture served the v0.x shape and the operator parsed the same wrong shape,
	// so both agreed and neither was right (A71).
	if len(c.SupportedInterfaces) == 0 {
		t.Fatal("no supportedInterfaces: A2A v1.0 requires it, and a card without one is the " +
			"pre-1.0 shape this fixture used to serve")
	}
	if got := c.SupportedInterfaces[0].ProtocolVersion; got != "1.0" {
		t.Errorf("protocolVersion is %q, want 1.0", got)
	}
	if got := c.SupportedInterfaces[0].ProtocolBinding; got == "" {
		t.Error("protocolBinding is required")
	}
	for _, f := range []struct {
		name string
		ok   bool
	}{
		{"description", c.Description != ""},
		{"version", c.Version != ""},
		{"defaultInputModes", len(c.DefaultInputModes) > 0},
		{"defaultOutputModes", len(c.DefaultOutputModes) > 0},
	} {
		if !f.ok {
			t.Errorf("%s is required by A2A v1.0 and is empty", f.name)
		}
	}
	if len(c.Skills) == 0 {
		t.Error("no skills: §3.4 cross-checks advertised skills against the CR's grants, and a " +
			"card with none cannot exercise that check")
	}
	// There is no sharedTaskState capability in A2A — AgentCapabilities is four
	// fields and none of them is it (A71). The struct no longer has one, so this
	// asserts the shape rather than a value.
	if c.Capabilities.Streaming {
		t.Error("the fixture claims streaming and does not stream")
	}
}

// §3.4 digests the card and re-fetches on drift. A card whose bytes changed per
// request would read as drift on every reconcile.
func TestTheCardIsByteStableAcrossFetches(t *testing.T) {
	srv := httptest.NewServer(handler())
	defer srv.Close()
	get := func() string {
		r, err := http.Get(srv.URL + "/.well-known/agent-card.json")
		if err != nil {
			t.Fatalf("fetch: %v", err)
		}
		defer r.Body.Close()
		// io.ReadAll, not one Read: a single Read is correct for this card and
		// silently truncates a longer one, which would make two different cards
		// compare equal.
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		return string(b)
	}
	if a, b := get(), get(); a != b {
		t.Errorf("the card changed between two fetches, which the operator reads as drift:\n%s\n%s", a, b)
	}
}

// The thing no test in this repository could do before: send a request to an
// agent and get an answer.
func TestItAnswersATask(t *testing.T) {
	t.Setenv("AGENT_NAME", "answerer")
	t.Setenv("ASSAYD_GATEWAY_URL", "http://gw.assayd:8080")
	srv := httptest.NewServer(handler())
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/assayd-test/echo", "application/json",
		strings.NewReader(`{"message":{"parts":[{"text":"hello"}]}}`))
	if err != nil {
		t.Fatalf("post task: %v", err)
	}
	defer resp.Body.Close()

	var out struct {
		Status    struct{ State string }
		Agent     string
		Gateway   string
		Artifacts []struct {
			Parts []struct{ Text string }
		}
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Status.State != "ok" {
		t.Errorf("task state %q", out.Status.State)
	}
	// Naming the responder is what lets a rollout test prove WHICH revision
	// answered, which is the whole point of per-revision Services.
	if out.Agent != "answerer" {
		t.Errorf("the answer does not name the agent that produced it: %q", out.Agent)
	}
	// Reported, never dialled: an e2e can prove the operator's injected contract
	// reached the container without a gateway existing.
	if out.Gateway != "http://gw.assayd:8080" {
		t.Errorf("ASSAYD_GATEWAY_URL was not reported back: %q", out.Gateway)
	}
	if len(out.Artifacts) == 0 || out.Artifacts[0].Parts[0].Text != "echo: hello" {
		t.Errorf("unexpected artifacts: %+v", out.Artifacts)
	}
}

func TestTheCardPathIsConfigurable(t *testing.T) {
	// spec.card.path is a real CRD field, so a fixture that hardcoded the default
	// could not exercise an Agent that moved it.
	t.Setenv("CARD_PATH", "/custom-card.json")
	srv := httptest.NewServer(handler())
	defer srv.Close()
	r, err := http.Get(srv.URL + "/custom-card.json")
	if err != nil || r.StatusCode != http.StatusOK {
		t.Fatalf("card not served at the configured path: err=%v status=%v", err, r)
	}
	r.Body.Close()
}

func TestMain(m *testing.M) { os.Exit(m.Run()) }
