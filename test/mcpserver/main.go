// Command mcpserver is the smallest thing that is actually an MCP server: it
// speaks Streamable HTTP, answers `initialize`, lists two tools and calls them.
//
// It exists for the last clause of ADR-0030's slice — "one agent completes one
// A2A task and **one MCP tool call** through the gateway" — which no test in
// this repository could exercise, because there was no MCP server anywhere in
// it to call. agentgateway's `AgentgatewayBackend` of type `mcp` selects
// Services and speaks `StreamableHTTP` on `/mcp` by default; this serves that.
//
// **It is a fixture, not design 11's MCP integration.** It has no resources, no
// prompts, no sampling, no SSE stream, no session store, no pagination and no
// task support. Two tools are enough to prove the one thing that needs proving:
// that a tool allowlist at the gateway admits one and refuses the other, and
// that the refusal happens to a real call rather than to a mock.
//
// The wire shapes are the 2025-11-25 specification's
// (modelcontextprotocol.io/specification/2025-11-25), read at the time of
// writing rather than recalled.
//
// **Stateless on purpose, and the choice is assayd's, not a shortcut.** This
// server assigns no `Mcp-Session-Id` and answers GET with 405, which is what a
// server with no session tracking and no server-initiated stream should do —
// FastMCP does the same under `stateless_http=True`, dropping GET from its
// allowed methods for that reason, and FastMCP 4's headline feature is the MCP
// sessionless protocol. It matters here beyond the fixture: design 03 shifts
// traffic between revisions by moving Gateway API `backendRefs` weights, and a
// STATEFUL MCP session is in tension with that — pinned to a revision, it either
// breaks when the weight moves or holds traffic on the old revision and defeats
// the shift. agentgateway exposes the choice as `sessionRouting` on the MCP
// backend and defaults it to `Stateful`. Nothing in assayd decides this yet;
// design 03 owes the decision, and this fixture does not pre-empt it.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

// supported lists the protocol versions this fixture will agree to. A server
// answers `initialize` with a version it supports; echoing back whatever the
// client asked would make every version "supported" and would hide exactly the
// mismatch this list exists to surface.
var supported = map[string]bool{
	"2025-11-25": true,
	"2025-06-18": true,
	"2025-03-26": true,
}

const preferred = "2025-11-25"

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type tool struct {
	Name        string         `json:"name"`
	Title       string         `json:"title,omitempty"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// The two tools. Their names are the whole point: an allowlist at the gateway
// admits `echo_text` and refuses `delete_everything`, and a test that could not
// tell which one it called would prove nothing.
func tools() []tool {
	str := func(desc string) map[string]any {
		return map[string]any{
			"type": "object",
			"properties": map[string]any{
				"text": map[string]any{"type": "string", "description": desc},
			},
			"required": []string{"text"},
		}
	}
	return []tool{
		{
			Name: "echo_text", Title: "Echo",
			Description: "returns the text it was given",
			InputSchema: str("the text to echo"),
		},
		{
			Name: "delete_everything", Title: "Delete everything",
			Description: "the tool an allowlist is supposed to keep an agent away from; " +
				"it deletes nothing and exists to be refused",
			InputSchema: str("ignored"),
		},
	}
}

func main() {
	port := env("PORT", "8080")
	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("mcpserver listening on :%s", port)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("serve: %v", err)
	}
}

func handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", serveMCP)
	// Separate from /mcp on purpose: a readiness probe that spoke JSON-RPC would
	// make "the transport works" and "the process is up" the same signal.
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "ok")
	})
	return mux
}

func serveMCP(w http.ResponseWriter, r *http.Request) {
	// A server that offers no server-initiated stream must say so rather than
	// hang: the spec allows 405 for a GET the server does not support.
	if r.Method == http.MethodGet {
		http.Error(w, "this server offers no SSE stream", http.StatusMethodNotAllowed)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req rpcRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, nil, -32700, "parse error: "+err.Error())
		return
	}

	// A notification has no id and takes no response — `notifications/initialized`
	// is the one this fixture actually receives. Answering it with a result would
	// be a response to a message that never asked for one.
	if len(req.ID) == 0 || string(req.ID) == "null" {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		version := preferred
		if supported[p.ProtocolVersion] {
			version = p.ProtocolVersion
		}
		writeResult(w, req.ID, map[string]any{
			"protocolVersion": version,
			// Only tools. Declaring a capability this fixture does not implement
			// would make a client ask for something that does not answer.
			"capabilities": map[string]any{
				"tools": map[string]any{"listChanged": false},
			},
			"serverInfo": map[string]any{
				"name": "assayd-e2e-mcpserver", "version": "0.1.0",
			},
		})

	case "tools/list":
		writeResult(w, req.ID, map[string]any{"tools": tools()})

	case "tools/call":
		var p struct {
			Name      string `json:"name"`
			Arguments struct {
				Text string `json:"text"`
			} `json:"arguments"`
		}
		_ = json.Unmarshal(req.Params, &p)
		switch p.Name {
		case "echo_text":
			writeResult(w, req.ID, content("echo: "+p.Arguments.Text, false))
		case "delete_everything":
			// Answers normally. If this refused by itself, a test could not tell a
			// gateway allowlist from the server's own refusal — and the allowlist is
			// the thing under test.
			writeResult(w, req.ID, content("deleted nothing, as promised", false))
		default:
			// A tool error is a RESULT with isError, not a JSON-RPC error: protocol
			// errors and tool failures are different things and clients treat them
			// differently.
			writeResult(w, req.ID, content("no such tool: "+p.Name, true))
		}

	default:
		writeErr(w, req.ID, -32601, "method not found: "+req.Method)
	}
}

func content(text string, isErr bool) map[string]any {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
		"isError": isErr,
	}
}

func writeResult(w http.ResponseWriter, id json.RawMessage, result any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"jsonrpc": "2.0", "id": id, "result": result,
	}); err != nil {
		log.Printf("encode result: %v", err)
	}
}

func writeErr(w http.ResponseWriter, id json.RawMessage, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	body := map[string]any{"jsonrpc": "2.0", "error": rpcError{Code: code, Message: msg}}
	if len(id) > 0 {
		body["id"] = id
	} else {
		body["id"] = nil
	}
	if err := json.NewEncoder(w).Encode(body); err != nil {
		log.Printf("encode error: %v", err)
	}
}

func env(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}
