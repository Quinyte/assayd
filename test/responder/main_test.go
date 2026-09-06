package main

import (
	"encoding/json"
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
	if c.ProtocolVersion != "1.0" {
		t.Errorf("protocolVersion is %q, want an A2A version the operator supports", c.ProtocolVersion)
	}
	if len(c.Skills) == 0 {
		t.Error("no skills: §3.4 cross-checks advertised skills against the CR's grants, and a " +
			"card with none cannot exercise that check")
	}
	// Asserted false on purpose. Design 02 §3.2 raises TaskStateUnverified unless
	// the card claims shared task state, and this fixture keeps none — claiming
	// it would make the operator clear a condition that exists to catch this.
	if c.Capabilities.SharedTaskState {
		t.Error("the fixture claims shared task state and keeps none")
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
		b := make([]byte, 4096)
		n, _ := r.Body.Read(b)
		return string(b[:n])
	}
	if a, b := get(), get(); a != b {
		t.Errorf("the card changed between two fetches, which the operator reads as drift:\n%s\n%s", a, b)
	}
}

// The thing no test in this repository could do before: send a request to an
// agent and get an answer.
func TestItAnswersATask(t *testing.T) {
	t.Setenv("AGENT_NAME", "answerer")
	t.Setenv("PLUME_GATEWAY_URL", "http://gw.plume:8080")
	srv := httptest.NewServer(handler())
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/v1/tasks", "application/json",
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
	if out.Status.State != "completed" {
		t.Errorf("task state %q", out.Status.State)
	}
	// Naming the responder is what lets a rollout test prove WHICH revision
	// answered, which is the whole point of per-revision Services.
	if out.Agent != "answerer" {
		t.Errorf("the answer does not name the agent that produced it: %q", out.Agent)
	}
	// Reported, never dialled: an e2e can prove the operator's injected contract
	// reached the container without a gateway existing.
	if out.Gateway != "http://gw.plume:8080" {
		t.Errorf("PLUME_GATEWAY_URL was not reported back: %q", out.Gateway)
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
