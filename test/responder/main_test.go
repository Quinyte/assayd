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

// The thing no test in this repository could do before: send an A2A task to an
// agent and have it completed — SendMessage on the HTTP+JSON binding.
func TestItCompletesASendMessageTask(t *testing.T) {
	t.Setenv("AGENT_NAME", "answerer")
	t.Setenv("ASSAYD_GATEWAY_URL", "http://gw.assayd:8080")
	srv := httptest.NewServer(handler())
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/message:send", "application/json", strings.NewReader(
		`{"message":{"messageId":"m-1","contextId":"c-1","role":"ROLE_USER","parts":[{"text":"hello"}]}}`))
	if err != nil {
		t.Fatalf("send message: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("SendMessage answered %d: %s", resp.StatusCode, b)
	}

	var out struct {
		Task *struct {
			ID        string
			ContextID string
			Status    struct{ State string }
			Artifacts []struct {
				ArtifactID string
				Parts      []struct{ Text string }
			}
			Metadata map[string]string
		}
		Message json.RawMessage
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// SendMessageResponse is a ONEOF: exactly one of task and message.
	if out.Task == nil || out.Message != nil {
		t.Fatalf("the response is not a SendMessageResponse carrying a task: task=%v message=%s",
			out.Task, out.Message)
	}
	if out.Task.Status.State != "TASK_STATE_COMPLETED" {
		t.Errorf("task state %q; A2A v1.0 puts enums on the wire by proto name, so a completed "+
			"task is TASK_STATE_COMPLETED", out.Task.Status.State)
	}
	if out.Task.ID == "" {
		t.Error("task.id is REQUIRED and the agent mints it")
	}
	if out.Task.ContextID != "c-1" {
		t.Errorf("the caller's contextId was not kept: %q", out.Task.ContextID)
	}
	if len(out.Task.Artifacts) == 0 || out.Task.Artifacts[0].ArtifactID == "" ||
		len(out.Task.Artifacts[0].Parts) == 0 || out.Task.Artifacts[0].Parts[0].Text != "echo: hello" {
		t.Errorf("unexpected artifacts: %+v", out.Task.Artifacts)
	}
	// Naming the responder is what lets a rollout test prove WHICH revision
	// answered, which is the whole point of per-revision Services.
	if out.Task.Metadata["agent"] != "answerer" {
		t.Errorf("the task does not name the agent that completed it: %v", out.Task.Metadata)
	}
	// Reported, never dialled: an e2e can prove the operator's injected contract
	// reached the container without a gateway existing.
	if out.Task.Metadata["gateway"] != "http://gw.assayd:8080" {
		t.Errorf("ASSAYD_GATEWAY_URL was not reported back: %v", out.Task.Metadata)
	}
}

// What is not a SendMessageRequest is refused — including the body every e2e in
// this repository sent before the method was real. A fixture that accepted it
// would let a caller that is not speaking A2A pass every assertion made against
// it.
func TestItRefusesWhatIsNotASendMessageRequest(t *testing.T) {
	srv := httptest.NewServer(handler())
	defer srv.Close()
	for _, tc := range []struct{ name, body string }{
		{"the old echo body", `{"message":{"parts":[{"text":"hello"}]}}`},
		{"no messageId", `{"message":{"role":"ROLE_USER","parts":[{"text":"hello"}]}}`},
		{"no role", `{"message":{"messageId":"m","parts":[{"text":"hello"}]}}`},
		{"the agent's role", `{"message":{"messageId":"m","role":"ROLE_AGENT","parts":[{"text":"hello"}]}}`},
		{"no text part", `{"message":{"messageId":"m","role":"ROLE_USER","parts":[]}}`},
		{"no message", `{}`},
		{"not JSON", `hello`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := http.Post(srv.URL+"/message:send", "application/json", strings.NewReader(tc.body))
			if err != nil {
				t.Fatalf("post: %v", err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("%s was answered %d, want 400", tc.name, resp.StatusCode)
			}
		})
	}

	// And the endpoint it replaced is gone, rather than left beside it as a
	// second, non-A2A way to get an answer.
	resp, err := http.Post(srv.URL+"/assayd-test/echo", "application/json",
		strings.NewReader(`{"message":{"parts":[{"text":"hello"}]}}`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("the retired /assayd-test/echo answered %d, want 404", resp.StatusCode)
	}
	// SendMessage is a POST; anything else is not the method.
	g, err := http.Get(srv.URL + "/message:send")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	g.Body.Close()
	if g.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET /message:send answered %d, want 405", g.StatusCode)
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
