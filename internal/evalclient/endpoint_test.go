// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package evalclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func httpJSON(u string) Interface {
	return Interface{URL: u, ProtocolBinding: "HTTP+JSON", ProtocolVersion: "1.0"}
}

// T5: the endpoint join of design 16 §1.1 step 2 — the URL's path alone, one
// trailing "/" trimmed, "/message:send" appended.
func TestEndpointJoin(t *testing.T) {
	for in, want := range map[string]string{
		"http://card.example":       "/message:send",
		"http://card.example/":      "/message:send",
		"http://card.example/a2a":   "/a2a/message:send",
		"http://card.example/a2a/":  "/a2a/message:send",
		"http://card.example/a2a//": "/a2a//message:send",
		"https://card.example:8443": "/message:send",
	} {
		got, err := EndpointPath([]Interface{httpJSON(in)})
		if err != nil {
			t.Errorf("EndpointPath(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("EndpointPath(%q) = %q, want %q", in, got, want)
		}
	}
}

// The first HTTP+JSON 1.0 entry is taken, in the card's order; entries on
// another binding or version are passed over.
func TestEndpointTakesTheFirstHTTPJSON10Entry(t *testing.T) {
	got, err := EndpointPath([]Interface{
		{URL: "http://x/rpc", ProtocolBinding: "JSONRPC", ProtocolVersion: "1.0"},
		{URL: "http://x/old", ProtocolBinding: "HTTP+JSON", ProtocolVersion: "0.3"},
		httpJSON("http://x/first"),
		httpJSON("http://x/second"),
	})
	if err != nil {
		t.Fatalf("EndpointPath: %v", err)
	}
	if got != "/first/message:send" {
		t.Errorf("EndpointPath = %q, want the first HTTP+JSON 1.0 entry's, /first/message:send", got)
	}
}

// A query, a fragment, an unparseable URL, and a card with no HTTP+JSON 1.0
// entry are each EvalBindingUnsupported. The first matching entry is the one
// judged: a refused entry is not skipped for a later one, because the card
// names its preferred interface first and a later entry is not what the agent
// asked to be called on.
func TestEndpointRefusals(t *testing.T) {
	// Every URL names the marker host, so a message that quoted any part of a
	// card's interfaces would carry it.
	const m = "marker.invalid"
	for name, ifaces := range map[string][]Interface{
		"query":                {httpJSON("http://" + m + "/a2a?tenant=t")},
		"empty query":          {httpJSON("http://" + m + "/a2a?")},
		"fragment":             {httpJSON("http://" + m + "/a2a#frag")},
		"empty fragment":       {httpJSON("http://" + m + "/a2a#")},
		"unparseable":          {httpJSON("http://" + m + "/%zz")},
		"no entries":           nil,
		"no HTTP+JSON 1.0":     {{URL: "http://" + m + "/", ProtocolBinding: "JSONRPC", ProtocolVersion: "1.0"}},
		"refused, not skipped": {httpJSON("http://" + m + "/a?q=1"), httpJSON("http://" + m + "/b")},
	} {
		got, err := EndpointPath(ifaces)
		var be *BindingError
		if !errors.As(err, &be) {
			t.Errorf("%s: EndpointPath = %q, %v; want a *BindingError", name, got, err)
			continue
		}
		if be.Reason() != ReasonBindingUnsupported || ReasonBindingUnsupported != "EvalBindingUnsupported" {
			t.Errorf("%s: reason %q, want EvalBindingUnsupported", name, be.Reason())
		}
		if strings.Contains(be.Error(), "marker") {
			t.Errorf("%s: the message quotes the card's URL, an address the agent chose: %q", name, be.Error())
		}
	}
}

// T5's second half: the card URL's host is never dialled. The card names a
// stub that counts every request; the case goes to the address the operator
// derived — the Service — at the path the join took from the card.
func TestTheCardURLHostIsNeverDialled(t *testing.T) {
	var cardHost atomic.Int32
	card := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		cardHost.Add(1)
	}))
	defer card.Close()

	var gotPath atomic.Value
	svc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath.Store(r.URL.Path)
		writeTask(w, "TASK_STATE_COMPLETED", "4")
	}))
	defer svc.Close()

	path, err := EndpointPath([]Interface{httpJSON(card.URL + "/a2a/")})
	if err != nil {
		t.Fatalf("EndpointPath: %v", err)
	}
	out := New().Send(context.Background(), Case{
		Address: hostOf(t, svc.URL), Path: path, Message: "2 + 2?", Expect: contains("4"), TimeoutSeconds: 2,
	})
	if out != (Outcome{Passed: true}) {
		t.Errorf("outcome %+v, want a pass", out)
	}
	if n := cardHost.Load(); n != 0 {
		t.Errorf("the card URL's host was dialled %d times; the operator sends nothing to an address the agent chose", n)
	}
	if p, _ := gotPath.Load().(string); p != "/a2a/message:send" {
		t.Errorf("the Service saw path %q, want /a2a/message:send", p)
	}
}

func hostOf(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return u.Host
}
