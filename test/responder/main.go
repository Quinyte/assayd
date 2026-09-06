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

// card is the A2A Agent Card subset design 02 §3.4 validates: it must be
// parseable, its name must match the CR, and its version must be a supported
// A2A one. Everything the operator cross-checks lives here; nothing else does,
// so a field appearing in this struct means some plume code reads it.
type card struct {
	Name            string   `json:"name"`
	Description     string   `json:"description"`
	Version         string   `json:"version"`
	ProtocolVersion string   `json:"protocolVersion"`
	URL             string   `json:"url"`
	Skills          []skill  `json:"skills"`
	Capabilities    capsSpec `json:"capabilities"`
}

type skill struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
}

// capsSpec carries the one capability design 02 §3.2 keys a condition off:
// TaskStateUnverified is raised unless the card asserts shared task state.
// This responder asserts FALSE, and that is deliberate rather than an omission
// — it keeps no shared state, and a fixture that claimed otherwise would make
// the operator clear a condition that exists to catch exactly this.
type capsSpec struct {
	Streaming              bool `json:"streaming"`
	StateTransitionHistory bool `json:"stateTransitionHistory"`
	SharedTaskState        bool `json:"sharedTaskState"`
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

	// PLUME_GATEWAY_URL is the operator's injected contract (design 02 §11,
	// A65). It is REPORTED, never dialled, so an e2e can prove injection reached
	// the container without this fixture needing a gateway to exist.
	gateway := os.Getenv("PLUME_GATEWAY_URL")

	self := card{
		Name:            name,
		Description:     "minimal A2A responder — plume e2e fixture",
		Version:         "0.1.0",
		ProtocolVersion: "1.0",
		URL:             fmt.Sprintf("http://%s:%s/", name, port),
		Skills: []skill{{
			ID: "echo", Name: "echo",
			Description: "returns the text it was sent",
			Tags:        []string{"test"},
		}},
		Capabilities: capsSpec{SharedTaskState: false},
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

	// The A2A task endpoint, reduced to what a first slice measures: a request
	// arrives, a task completes, and the response says which agent answered.
	// Naming the responder is the point — it is how a test proves traffic
	// reached THIS revision and not another one during a rollout.
	mux.HandleFunc("/v1/tasks", func(w http.ResponseWriter, r *http.Request) {
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
			"status":  map[string]string{"state": "completed"},
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
