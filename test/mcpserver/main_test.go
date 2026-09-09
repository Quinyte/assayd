// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The fixture's half of the contract, pinned here so the gateway e2e is testing
// agentgateway's allowlist rather than a server that never worked.

func post(t *testing.T, url, body string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusAccepted {
		return resp.StatusCode, nil
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp.StatusCode, out
}

func TestInitializeNegotiatesAVersionItActuallySupports(t *testing.T) {
	srv := httptest.NewServer(handler())
	defer srv.Close()

	_, out := post(t, srv.URL+"/mcp",
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`)
	res, _ := out["result"].(map[string]any)
	if res == nil {
		t.Fatalf("initialize returned no result: %+v", out)
	}
	if got := res["protocolVersion"]; got != "2025-06-18" {
		t.Errorf("protocolVersion %v; a supported version the client asked for should be "+
			"agreed to, not overridden", got)
	}
	// The mismatch case is the one that matters: agreeing to anything would make
	// the negotiation decorative.
	_, out = post(t, srv.URL+"/mcp",
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"1999-01-01"}}`)
	res, _ = out["result"].(map[string]any)
	if got := res["protocolVersion"]; got != preferred {
		t.Errorf("protocolVersion %v for an unsupported request; want this server's own %q",
			got, preferred)
	}
	caps, _ := res["capabilities"].(map[string]any)
	if _, ok := caps["tools"]; !ok {
		t.Error("no tools capability, so a client has no reason to call tools/list")
	}
	for _, unimplemented := range []string{"resources", "prompts", "logging", "tasks"} {
		if _, ok := caps[unimplemented]; ok {
			t.Errorf("declares %q and does not implement it", unimplemented)
		}
	}
}

func TestBothToolsAreListedAndCallable(t *testing.T) {
	srv := httptest.NewServer(handler())
	defer srv.Close()

	_, out := post(t, srv.URL+"/mcp", `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	res, _ := out["result"].(map[string]any)
	list, _ := res["tools"].([]any)
	names := map[string]bool{}
	for _, item := range list {
		m, _ := item.(map[string]any)
		name, _ := m["name"].(string)
		names[name] = true
		if _, ok := m["inputSchema"]; !ok {
			t.Errorf("tool %q has no inputSchema, which the spec requires", name)
		}
	}
	// BOTH, unfiltered. The gateway allowlist is what removes one, and if this
	// server only ever served one the e2e could not tell filtering from absence.
	for _, want := range []string{"echo_text", "delete_everything"} {
		if !names[want] {
			t.Errorf("tools/list omits %q; the allowlist test needs both to exist here", want)
		}
	}

	_, out = post(t, srv.URL+"/mcp",
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo_text","arguments":{"text":"hi"}}}`)
	if got := textOf(t, out); got != "echo: hi" {
		t.Errorf("echo_text returned %q", got)
	}

	// The refusable tool must SUCCEED here. A server that refused it on its own
	// would make the gateway's refusal unattributable.
	_, out = post(t, srv.URL+"/mcp",
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"delete_everything","arguments":{}}}`)
	res, _ = out["result"].(map[string]any)
	if isErr, _ := res["isError"].(bool); isErr {
		t.Error("delete_everything refuses itself, so a gateway allowlist could not be " +
			"distinguished from the server's own refusal")
	}
}

func TestAnUnknownToolIsAResultNotAProtocolError(t *testing.T) {
	srv := httptest.NewServer(handler())
	defer srv.Close()
	_, out := post(t, srv.URL+"/mcp",
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"nope"}}`)
	if _, isRPCErr := out["error"]; isRPCErr {
		t.Error("an unknown tool came back as a JSON-RPC error; the spec makes tool " +
			"failures a result with isError, and clients treat the two differently")
	}
	res, _ := out["result"].(map[string]any)
	if isErr, _ := res["isError"].(bool); !isErr {
		t.Error("an unknown tool did not set isError")
	}
}

// Stateless: no session to resume, so no stream to offer. FastMCP drops GET for
// the same reason under stateless_http=True.
func TestGetIsRefusedBecauseThereIsNoStream(t *testing.T) {
	srv := httptest.NewServer(handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/mcp")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET /mcp returned %d, want 405 — a stateless server with no "+
			"server-initiated stream must refuse rather than hang", resp.StatusCode)
	}
}

func TestANotificationGetsNoResponseBody(t *testing.T) {
	srv := httptest.NewServer(handler())
	defer srv.Close()
	code, out := post(t, srv.URL+"/mcp", `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	if code != http.StatusAccepted {
		t.Errorf("a notification got %d, want 202", code)
	}
	if out != nil {
		t.Errorf("a notification was answered with a body: %+v", out)
	}
}

func textOf(t *testing.T, out map[string]any) string {
	t.Helper()
	res, _ := out["result"].(map[string]any)
	content, _ := res["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("no content in %+v", out)
	}
	first, _ := content[0].(map[string]any)
	s, _ := first["text"].(string)
	return s
}
