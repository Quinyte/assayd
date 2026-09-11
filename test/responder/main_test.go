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

	resp := sendV1(t, srv.URL,
		`{"message":{"messageId":"m-1","contextId":"c-1","role":"ROLE_USER","parts":[{"text":"hello"}]}}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("SendMessage answered %d: %s", resp.StatusCode, b)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/a2a+json" {
		t.Errorf("SendMessage answered as %q; the HTTP+JSON binding answers as application/a2a+json", ct)
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

// sendV1 posts a SendMessageRequest the way an A2A 1.0 client must: with
// `A2A-Version: 1.0`, without which the spec reads the request as 0.3.
func sendV1(t *testing.T, base, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, base+"/message:send", strings.NewReader(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("A2A-Version", "1.0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("send message: %v", err)
	}
	return resp
}

// decodeTask reads the task out of a SendMessageResponse.
func decodeTask(t *testing.T, resp *http.Response) (id, text string) {
	t.Helper()
	defer resp.Body.Close()
	var out struct {
		Task *struct {
			ID        string
			Artifacts []struct{ Parts []struct{ Text string } }
		}
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || out.Task == nil ||
		len(out.Task.Artifacts) == 0 || len(out.Task.Artifacts[0].Parts) == 0 {
		t.Fatalf("not a SendMessageResponse carrying a task with an artifact: err=%v %+v", err, out)
	}
	return out.Task.ID, out.Task.Artifacts[0].Parts[0].Text
}

// A task id is minted per task. Two tasks sharing one would make a later
// GetTask — or any receipt keyed on the id — answer for the wrong one.
func TestEachTaskGetsItsOwnID(t *testing.T) {
	srv := httptest.NewServer(handler())
	defer srv.Close()
	body := `{"message":{"messageId":"m","role":"ROLE_USER","parts":[{"text":"x"}]}}`
	a, _ := decodeTask(t, sendV1(t, srv.URL, body))
	b, _ := decodeTask(t, sendV1(t, srv.URL, body))
	if a == "" || a == b {
		t.Errorf("two tasks got ids %q and %q; each task's id must be its own", a, b)
	}
}

// Every text part is answered, not only the first.
func TestEveryTextPartIsEchoed(t *testing.T) {
	srv := httptest.NewServer(handler())
	defer srv.Close()
	_, text := decodeTask(t, sendV1(t, srv.URL,
		`{"message":{"messageId":"m","role":"ROLE_USER","parts":[{"text":"one"},{"text":"two"}]}}`))
	if text != "echo: one two" {
		t.Errorf("a two-part message was answered %q, want %q", text, "echo: one two")
	}
}

// Version negotiation (spec §3.6): an agent reads an absent A2A-Version as 0.3
// and refuses a version it does not speak. This one speaks 1.0 only.
func TestItRefusesAnyVersionButOnePointZero(t *testing.T) {
	srv := httptest.NewServer(handler())
	defer srv.Close()
	body := `{"message":{"messageId":"m","role":"ROLE_USER","parts":[{"text":"x"}]}}`
	for _, v := range []string{"", "0.3", "9.9"} {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/message:send", strings.NewReader(body))
		if v != "" {
			req.Header.Set("A2A-Version", v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("post: %v", err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(b), "VersionNotSupportedError") {
			t.Errorf("A2A-Version %q was answered %d %s; want 400 VersionNotSupportedError", v, resp.StatusCode, b)
		}
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
			resp := sendV1(t, srv.URL, tc.body)
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

// fakeMCP stands in for the gateway's tools listener: it records the order of
// the JSON-RPC methods it receives and the Host each came with, and answers
// tools/call either with a result (as SSE, the way agentgateway answers a
// successful exchange) or with the JSON-RPC error agentgateway gives a tool its
// allowlist refuses.
func fakeMCP(t *testing.T, refuse bool) (*httptest.Server, *[]string) {
	t.Helper()
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var msg struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				Name      string `json:"name"`
				Arguments struct{ Text string }
			} `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&msg)
		seen = append(seen, r.URL.Path+" "+r.Host+" "+msg.Method)
		switch msg.Method {
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "initialize":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18"}}`))
		case "tools/call":
			if refuse {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":2,"error":{"code":-32602,"message":"Unknown tool: ` +
					msg.Params.Name + `"}}`))
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(`data: {"jsonrpc":"2.0","id":2,"result":{"content":[{"type":"text","text":"echo: ` +
				msg.Params.Arguments.Text + `"}],"isError":false}}` + "\n\n"))
		}
	}))
	return srv, &seen
}

type toolAnswer struct {
	Task *struct {
		Status struct {
			State   string
			Message *struct{ Parts []struct{ Text string } }
		}
		Artifacts []struct{ Parts []struct{ Text string } }
		Metadata  map[string]string
	}
}

func sendTool(t *testing.T, base, tool, text string) toolAnswer {
	t.Helper()
	resp := sendV1(t, base, `{"message":{"messageId":"m","role":"ROLE_USER","parts":[{"text":"`+text+
		`"}],"metadata":{"tool":"`+tool+`"}}}`)
	defer resp.Body.Close()
	var out toolAnswer
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || out.Task == nil {
		t.Fatalf("not a SendMessageResponse carrying a task: %v", err)
	}
	return out
}

// The agent completes the task BY calling the tool, through the gateway the
// operator injected, at the tool route's hostname — and returns the tool's
// answer as the task's artifact.
func TestItCompletesATaskByCallingATool(t *testing.T) {
	mcp, seen := fakeMCP(t, false)
	defer mcp.Close()
	t.Setenv("ASSAYD_GATEWAY_URL", mcp.URL)
	t.Setenv("MCP_TOOL_HOST", "mcp.assayd.test")
	srv := httptest.NewServer(handler())
	defer srv.Close()

	out := sendTool(t, srv.URL, "echo_text", "from the agent")
	if out.Task.Status.State != "TASK_STATE_COMPLETED" {
		t.Fatalf("task state %q, want TASK_STATE_COMPLETED: %+v", out.Task.Status.State, out.Task)
	}
	if len(out.Task.Artifacts) == 0 || out.Task.Artifacts[0].Parts[0].Text != "tool echo_text: echo: from the agent" {
		t.Errorf("the artifact is not the tool's answer: %+v", out.Task.Artifacts)
	}
	if out.Task.Metadata["tool"] != "echo_text" {
		t.Errorf("the task does not say which tool completed it: %v", out.Task.Metadata)
	}
	// The MCP lifecycle, in order, on /mcp, at the tool route's host.
	want := []string{
		"/mcp mcp.assayd.test initialize",
		"/mcp mcp.assayd.test notifications/initialized",
		"/mcp mcp.assayd.test tools/call",
	}
	if strings.Join(*seen, "|") != strings.Join(want, "|") {
		t.Errorf("the MCP exchange was %v, want %v", *seen, want)
	}
}

// A tool the gateway refuses fails the TASK — TASK_STATE_FAILED with the
// gateway's answer in the status message — rather than the request.
func TestARefusedToolFailsTheTaskAndSaysWhy(t *testing.T) {
	mcp, _ := fakeMCP(t, true)
	defer mcp.Close()
	t.Setenv("ASSAYD_GATEWAY_URL", mcp.URL)
	srv := httptest.NewServer(handler())
	defer srv.Close()

	out := sendTool(t, srv.URL, "delete_everything", "x")
	if out.Task.Status.State != "TASK_STATE_FAILED" {
		t.Fatalf("a refused tool left the task %q, want TASK_STATE_FAILED", out.Task.Status.State)
	}
	if m := out.Task.Status.Message; m == nil || len(m.Parts) == 0 ||
		!strings.Contains(m.Parts[0].Text, "Unknown tool: delete_everything") {
		t.Errorf("the failed task does not carry the gateway's refusal: %+v", out.Task.Status)
	}
	if len(out.Task.Artifacts) != 0 {
		t.Errorf("a failed task carries an artifact: %+v", out.Task.Artifacts)
	}
}

// With no gateway injected the agent calls nothing. There is no fallback
// address, so a success could only ever mean the gateway carried the call.
func TestWithNoGatewayTheToolCallFailsRatherThanGoingElsewhere(t *testing.T) {
	t.Setenv("ASSAYD_GATEWAY_URL", "")
	srv := httptest.NewServer(handler())
	defer srv.Close()
	out := sendTool(t, srv.URL, "echo_text", "x")
	if out.Task.Status.State != "TASK_STATE_FAILED" || out.Task.Status.Message == nil ||
		!strings.Contains(out.Task.Status.Message.Parts[0].Text, "ASSAYD_GATEWAY_URL") {
		t.Errorf("with no gateway the task is %+v; want FAILED naming ASSAYD_GATEWAY_URL", out.Task.Status)
	}
}
