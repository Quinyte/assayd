// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

// Command responder is the smallest agent that is actually an agent: it serves
// an A2A Agent Card and completes one A2A task, over the binding its card
// declares.
//
// It exists because of a finding, not a feature request. Every e2e test in this
// repository ran `registry.k8s.io/pause` — a container that does nothing — so
// no test anywhere sent a request to an agent, and the platform's central claim
// that governance becomes real at the gateway had never been exercised. A card
// fetch, a route, a policy and a budget all need something on the other end
// before any of them can be measured.
//
// **This is a test fixture, not design 09's SDK template.** ADR-0030 narrowed
// the commitment to "the parts of 09 that slice needs", and what the slice
// needs is a real responder to point at. It deliberately does not implement the
// skills directory, the JetStream task store, Sigstore card signing, OTel spans
// or the typed-pending retry — each is design 09's and each would be a promise
// this repository cannot keep yet. What it does implement, it implements for
// real: the card is served by the container, which ADR-0019 makes the source of
// truth, rather than by a fixture the operator was handed.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

// card is the A2A **v1.0** Agent Card, pinned to `a2aproject/A2A` @ `v1.0.1`
// (`specification/a2a.proto`) — see
// `docs/research/a2a-v1.0-card-and-transport-2026-09.md`.
//
// The first version of this fixture served a **v0.x** card: top-level `url` and
// `protocolVersion`, plus a `sharedTaskState` capability that has never existed
// in any version. It agreed with the operator because one hand wrote both, and
// the agreement was mistaken for verification. A conformant card puts the
// version inside `supportedInterfaces[]`, and `AgentCapabilities` has exactly
// four fields.
type card struct {
	Name                string           `json:"name"`
	Description         string           `json:"description"`
	Version             string           `json:"version"`
	SupportedInterfaces []agentInterface `json:"supportedInterfaces"`
	Capabilities        capsSpec         `json:"capabilities"`
	DefaultInputModes   []string         `json:"defaultInputModes"`
	DefaultOutputModes  []string         `json:"defaultOutputModes"`
	Skills              []skill          `json:"skills"`
}

// agentInterface is where the protocol version actually lives. protocolBinding
// is an open string; JSONRPC, GRPC and HTTP+JSON are the officially supported
// values. This fixture declares HTTP+JSON and implements ONE of its methods,
// SendMessage — see the task handler for everything it does not.
type agentInterface struct {
	URL             string `json:"url"`
	ProtocolBinding string `json:"protocolBinding"`
	ProtocolVersion string `json:"protocolVersion"`
}

type skill struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
}

// capsSpec is the whole of AgentCapabilities. `sharedTaskState` and
// `stateTransitionHistory` are NOT in it and never were in v1.0 — the first
// version of this fixture invented the first and carried the second from v0.x.
// Design 02 §3.2 keyed a condition off the invented one; A71 records that.
type capsSpec struct {
	Streaming         bool     `json:"streaming"`
	PushNotifications bool     `json:"pushNotifications"`
	ExtendedAgentCard bool     `json:"extendedAgentCard"`
	Extensions        []string `json:"extensions,omitempty"`
}

func main() {
	port := env("PORT", "8080")
	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("responder listening on :%s", port)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("serve: %v", err)
	}
}

// handler builds the mux. Separated from main so the contract can be tested
// without a container, a cluster or a port.
func handler() http.Handler {
	port := env("PORT", "8080")
	name := env("AGENT_NAME", "responder")
	cardPath := env("CARD_PATH", "/.well-known/agent-card.json")

	// ASSAYD_GATEWAY_URL is the operator's injected contract (design 02 §11,
	// A65). It is REPORTED, never dialled, so an e2e can prove injection reached
	// the container without this fixture needing a gateway to exist.
	gateway := os.Getenv("ASSAYD_GATEWAY_URL")

	self := card{
		Name:        name,
		Description: "minimal HTTP responder — assayd e2e fixture",
		Version:     "0.1.0",
		SupportedInterfaces: []agentInterface{{
			URL:             fmt.Sprintf("http://%s:%s/", name, port),
			ProtocolBinding: "HTTP+JSON",
			ProtocolVersion: "1.0",
		}},
		Capabilities:       capsSpec{Streaming: false},
		DefaultInputModes:  []string{"text/plain"},
		DefaultOutputModes: []string{"text/plain"},
		Skills: []skill{{
			ID: "echo", Name: "echo",
			Description: "returns the text it was sent",
			Tags:        []string{"test"},
		}},
	}

	mux := http.NewServeMux()

	mux.HandleFunc(cardPath, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Deterministic bytes: design 02 §3.4 digests the card and re-fetches on
		// drift, so a field whose value changed per request — a timestamp, a
		// counter — would look like drift on every reconcile.
		if err := json.NewEncoder(w).Encode(self); err != nil {
			log.Printf("encode card: %v", err)
		}
	})

	// SendMessage, on the HTTP+JSON binding the card declares: `POST
	// /message:send`, a `SendMessageRequest` in and a `SendMessageResponse` out,
	// pinned to `a2aproject/A2A` @ `v1.0.1` (`specification/a2a.proto`, and
	// `docs/research/a2a-v1.0-card-and-transport-2026-09.md` §2).
	//
	// It replaced `/assayd-test/echo`, and the comment that stood here argued
	// against doing exactly this. That argument was right about one thing and is
	// kept for it: this fixture must not become the only thing in the repository
	// claiming to speak A2A, and the A2A CLIENT is design 08's (`assayd invoke`,
	// 08:22). What overrode the rest is ADR-0030's stop criterion, which is
	// literal — the slice is carried "until one agent completes one A2A task …
	// through the gateway". A request to a path no A2A binding has, answered with
	// a state no A2A version defines, is not one; every e2e that claimed "the
	// agent answered through the gateway" was measuring a bespoke echo.
	//
	// So ONE method is real, and nothing else is claimed. What is NOT
	// implemented: GetTask, ListTasks, CancelTask, streaming (the card says
	// `streaming: false`), push notifications, the extended card, `tenant`,
	// `configuration`, and the binding's error mapping — a refusal here is a
	// 400 with a plain JSON body, which a test may rely on for the status and not
	// for the shape. Every task this returns is already TASK_STATE_COMPLETED,
	// because echoing is synchronous; a caller asking GetTask for it gets a 404.
	mux.HandleFunc("POST /message:send", func(w http.ResponseWriter, r *http.Request) {
		var req sendMessageRequest
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
			refuse(w, "the body is not a SendMessageRequest: "+err.Error())
			return
		}
		// The proto's REQUIRED fields, checked rather than defaulted. A fixture
		// that accepted the old echo body — `{"message":{"parts":[…]}}`, no
		// messageId and no role — would let a caller that is not speaking A2A
		// pass every assertion made against it.
		m := req.Message
		switch {
		case m == nil:
			refuse(w, "message is REQUIRED")
			return
		case m.MessageID == "":
			refuse(w, "message.messageId is REQUIRED")
			return
		case m.Role != "ROLE_USER":
			refuse(w, fmt.Sprintf("message.role is %q; a message a client sends is ROLE_USER", m.Role))
			return
		}
		var texts []string
		for _, p := range m.Parts {
			if p.Text != "" {
				texts = append(texts, p.Text)
			}
		}
		if len(texts) == 0 {
			refuse(w, "message.parts carries no text part, and text is the only mode this agent accepts")
			return
		}
		contextID := m.ContextID
		if contextID == "" {
			contextID = newID("ctx")
		}
		out := sendMessageResponse{Task: &task{
			ID:        newID("task"),
			ContextID: contextID,
			Status:    taskStatus{State: "TASK_STATE_COMPLETED"},
			Artifacts: []artifact{{
				ArtifactID: newID("artifact"),
				Name:       "echo",
				Parts:      []part{{Text: "echo: " + strings.Join(texts, " ")}},
			}},
			// Which agent answered, and what the operator injected, ride in the
			// task's own metadata — a `google.protobuf.Struct` the proto gives
			// every Task for exactly this — rather than in fields A2A does not
			// have. Naming the agent is what lets a rollout test prove WHICH
			// revision answered; the gateway URL is REPORTED, never dialled.
			Metadata: map[string]string{"agent": name, "gateway": gateway},
		}}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(out); err != nil {
			log.Printf("encode task: %v", err)
		}
	})

	// Distinct from the card path on purpose. A readiness probe that fetched the
	// card would make "the card is servable" and "the agent is up" the same
	// signal, and design 02 §3.4 needs them separate: a candidate becomes Ready
	// FIRST and the card is fetched after.
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "ok")
	})

	log.Printf("responder %q ready, card at %s, gateway=%q", name, cardPath, gateway)
	return mux
}

// The SendMessage request and response, as protojson renders a2a.proto: field
// names in lowerCamelCase, enums by their proto name. Only the fields this
// fixture reads or writes are declared; the rest of each message is optional.
type sendMessageRequest struct {
	Message *message `json:"message"`
}

type message struct {
	MessageID string `json:"messageId"`
	ContextID string `json:"contextId,omitempty"`
	Role      string `json:"role"`
	Parts     []part `json:"parts"`
}

// part is a oneof over text, raw, url and data; this agent reads and writes text.
type part struct {
	Text string `json:"text,omitempty"`
}

// sendMessageResponse is a ONEOF of task and message — not a bare task. This
// fixture always answers with a task, so `message` is never set.
type sendMessageResponse struct {
	Task *task `json:"task,omitempty"`
}

type task struct {
	ID        string            `json:"id"`
	ContextID string            `json:"contextId,omitempty"`
	Status    taskStatus        `json:"status"`
	Artifacts []artifact        `json:"artifacts,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

// taskStatus.state is SCREAMING_SNAKE on the wire. The echo endpoint this
// replaced answered "ok", which no A2A version defines.
type taskStatus struct {
	State string `json:"state"`
}

type artifact struct {
	ArtifactID string `json:"artifactId"`
	Name       string `json:"name,omitempty"`
	Parts      []part `json:"parts"`
}

func refuse(w http.ResponseWriter, why string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": why})
}

// newID is unique per call and carries no meaning. A task id is minted by the
// agent, never by the caller.
func newID(prefix string) string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		log.Fatalf("read random: %v", err)
	}
	return prefix + "-" + hex.EncodeToString(b)
}

func env(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}
