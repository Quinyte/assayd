// Command responder is the smallest agent that is actually an agent: it serves
// an A2A Agent Card and answers one task.
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
	"encoding/json"
	"fmt"
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
// values. This fixture declares HTTP+JSON and does NOT implement the JSON-RPC
// method set — see the task handler.
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

	// NOT an A2A method. A2A v1.0 defines three bindings and a PascalCase method
	// set — SendMessage, SendStreamingMessage, GetTask, ListTasks, CancelTask,
	// SubscribeToTask — and no binding has a `POST /v1/tasks`. This endpoint is
	// this repository's own, and it is named honestly rather than dressed up: it
	// exists so a test can prove a request reached THIS revision's Service and
	// got an answer naming the revision, which is what ADR-0030 step 2 needed.
	//
	// Implementing the real method set belongs with the client that will call it
	// — design 03's route — not with a fixture that would then be the only thing
	// in the repository claiming to speak A2A.
	mux.HandleFunc("/assayd-test/echo", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		var in struct {
			Message struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"message"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		var text string
		if len(in.Message.Parts) > 0 {
			text = in.Message.Parts[0].Text
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			// Not an A2A TaskState either. protojson serialises enums by proto
			// name, so a real one is "TASK_STATE_COMPLETED"; the lowercase
			// lifecycle in docs/research/a2a-2026-08.md is wrong and superseded.
			"status":  map[string]string{"state": "ok"},
			"agent":   name,
			"gateway": gateway,
			"artifacts": []map[string]any{{
				"parts": []map[string]string{{"text": "echo: " + text}},
			}},
		})
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

func env(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}
