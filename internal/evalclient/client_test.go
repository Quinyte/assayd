// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package evalclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
)

func contains(s string) assaydv1alpha1.EvalExpect { return assaydv1alpha1.EvalExpect{Contains: s} }
func equals(s string) assaydv1alpha1.EvalExpect   { return assaydv1alpha1.EvalExpect{Equals: s} }

func writeTask(w http.ResponseWriter, state, text string) {
	w.Header().Set("Content-Type", "application/a2a+json")
	fmt.Fprintf(w, `{"task":{"id":"t","status":{"state":%q},"artifacts":[{"artifactId":"a","parts":[{"text":%q}]}]}}`,
		state, text)
}

// stub answers every request with a fixed status and body.
func stub(t *testing.T, code int, body string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(code)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return hostOf(t, srv.URL)
}

func send(t *testing.T, addr string, expect assaydv1alpha1.EvalExpect) Outcome {
	t.Helper()
	return New().Send(context.Background(), Case{
		Address: addr, Path: "/message:send", Message: "q", Expect: expect, TimeoutSeconds: 2,
	})
}

var pass = Outcome{Passed: true}

func fail(r Reason) Outcome { return Outcome{Reason: r} }

// T1: design 16 §1.1 step 6's outcome table, one case per row (and per
// distinct shape within a row), each against a recorded response.
func TestOutcomeTable(t *testing.T) {
	const completed = `{"task":{"id":"t","status":{"state":"TASK_STATE_COMPLETED"},"artifacts":[` +
		`{"artifactId":"a","parts":[{"text":"the deductible"},{"data":{"x":1}},{"text":"is 500"}]},` +
		`{"artifactId":"b","parts":[{"text":"USD"}]}]}}`
	for _, tc := range []struct {
		name   string
		code   int
		body   string
		expect assaydv1alpha1.EvalExpect
		want   Outcome
	}{
		// Row 1: 200, completed or a message, and the answer satisfies expect.
		{"completed task, contains", 200, completed, contains("500"), pass},
		// The answer text is every text part of every artifact, in order,
		// joined by one space; a non-text part contributes nothing.
		{"completed task, equals the joined text", 200, completed, equals("the deductible is 500 USD"), pass},
		{"message response", 200, `{"message":{"messageId":"m","role":"ROLE_AGENT","parts":[{"text":"o"},{"text":"k"}]}}`,
			equals("o k"), pass},
		// Row 2: the same, and the answer does not satisfy expect.
		{"completed task, mismatch", 200, completed, contains("600"), fail(ReasonMismatch)},
		{"equals is case-sensitive", 200, `{"message":{"parts":[{"text":"OK"}]}}`, equals("ok"), fail(ReasonMismatch)},
		{"equals does not normalize", 200, `{"message":{"parts":[{"text":"ok "}]}}`, equals("ok"), fail(ReasonMismatch)},
		// status.message is not an artifact, so its text is not the answer.
		{"status message is not the answer", 200,
			`{"task":{"status":{"state":"TASK_STATE_COMPLETED","message":{"parts":[{"text":"500"}]}}}}`,
			contains("500"), fail(ReasonMismatch)},
		// Row 3: 200, and the task is in any other state — even with an
		// answer that would satisfy expect.
		{"failed task", 200, strings.Replace(completed, "COMPLETED", "FAILED", 1), contains("500"), fail(ReasonTaskNotCompleted)},
		{"input required", 200, strings.Replace(completed, "COMPLETED", "INPUT_REQUIRED", 1), contains("500"), fail(ReasonTaskNotCompleted)},
		{"working", 200, strings.Replace(completed, "COMPLETED", "WORKING", 1), contains("500"), fail(ReasonTaskNotCompleted)},
		{"lowercase completed is not a v1.0 state", 200, strings.Replace(completed, "TASK_STATE_COMPLETED", "completed", 1),
			contains("500"), fail(ReasonTaskNotCompleted)},
		{"no state", 200, `{"task":{"artifacts":[{"parts":[{"text":"500"}]}]}}`, contains("500"), fail(ReasonTaskNotCompleted)},
		// Row 4: any other status, or a body that is not a SendMessageResponse.
		// 3xx is TestARedirectIsNotFollowed and the size cap TestTheBodyCap.
		{"500", 500, completed, contains("500"), fail(ReasonProtocolError)},
		{"201", 201, completed, contains("500"), fail(ReasonProtocolError)},
		{"400", 400, `{"error":"VersionNotSupportedError"}`, contains("500"), fail(ReasonProtocolError)},
		{"not JSON", 200, `the deductible is 500`, contains("500"), fail(ReasonProtocolError)},
		{"a bare task, not the oneof", 200, `{"id":"t","status":{"state":"TASK_STATE_COMPLETED"}}`, contains("x"),
			fail(ReasonProtocolError)},
		{"both task and message", 200,
			`{"task":{"status":{"state":"TASK_STATE_COMPLETED"}},"message":{"parts":[{"text":"500"}]}}`,
			contains("500"), fail(ReasonProtocolError)},
		{"null task", 200, `{"task":null}`, contains("500"), fail(ReasonProtocolError)},
		{"an array", 200, `[` + completed + `]`, contains("500"), fail(ReasonProtocolError)},
		{"trailing data", 200, completed + `{}`, contains("500"), fail(ReasonProtocolError)},
		{"empty body", 200, ``, contains("500"), fail(ReasonProtocolError)},
		// encoding/json's struct decoding would read each of these as a
		// SendMessageResponse; protojson writes none of them.
		{"keys in another case", 200, strings.ReplaceAll(completed, `"task"`, `"Task"`), contains("500"),
			fail(ReasonProtocolError)},
		{"a duplicate task key", 200,
			`{"task":{"status":{"state":"TASK_STATE_FAILED"},"artifacts":[{"parts":[{"text":"500"}]}]},` +
				`"task":{"status":{"state":"TASK_STATE_COMPLETED"}}}`, contains("500"), fail(ReasonProtocolError)},
		{"a duplicate key deeper down", 200,
			`{"message":{"parts":[{"text":"500","text":"600"}]}}`, contains("600"), fail(ReasonProtocolError)},
		{"a part that is text and data", 200, `{"message":{"parts":[{"text":"500","data":{}}]}}`, contains("500"),
			fail(ReasonProtocolError)},
		{"a part that is not an object", 200, `{"message":{"parts":["500"]}}`, contains("500"), fail(ReasonProtocolError)},
		{"text that is not a string", 200, `{"message":{"parts":[{"text":500}]}}`, contains("500"), fail(ReasonProtocolError)},
		// 3 is TASK_STATE_COMPLETED by number, which a protojson parser
		// accepts; A2A 1.0 §5.5 requires an enum to be written by its name.
		{"a state that is not a string", 200, `{"task":{"status":{"state":3}}}`, contains("x"), fail(ReasonProtocolError)},
		// Each member on the answer path, of the wrong JSON type.
		{"a task that is not an object", 200, `{"task":1}`, contains("x"), fail(ReasonProtocolError)},
		{"a message that is not an object", 200, `{"message":"500"}`, contains("500"), fail(ReasonProtocolError)},
		{"a status that is not an object", 200, `{"task":{"status":"TASK_STATE_COMPLETED"}}`, contains("x"),
			fail(ReasonProtocolError)},
		{"artifacts that are not a list", 200, `{"task":{"status":{"state":"TASK_STATE_COMPLETED"},"artifacts":{}}}`,
			contains("x"), fail(ReasonProtocolError)},
		{"an artifact that is not an object", 200,
			`{"task":{"status":{"state":"TASK_STATE_COMPLETED"},"artifacts":[1,{"parts":[{"text":"500"}]}]}}`,
			contains("500"), fail(ReasonProtocolError)},
		{"a null artifact", 200,
			`{"task":{"status":{"state":"TASK_STATE_COMPLETED"},"artifacts":[null,{"parts":[{"text":"500"}]}]}}`,
			contains("500"), fail(ReasonProtocolError)},
		{"task parts that are not a list", 200,
			`{"task":{"status":{"state":"TASK_STATE_COMPLETED"},"artifacts":[{"parts":{"text":"500"}}]}}`,
			contains("500"), fail(ReasonProtocolError)},
		{"message parts that are not a list", 200, `{"message":{"parts":{"text":"500"}}}`, contains("500"),
			fail(ReasonProtocolError)},
		// A null member is unset, as protojson reads it.
		{"a null message beside a task", 200,
			`{"task":{"status":{"state":"TASK_STATE_COMPLETED"},"artifacts":[{"parts":[{"text":"500"}]}]},"message":null}`,
			contains("500"), pass},
		{"null raw and url beside text", 200, `{"message":{"parts":[{"text":"500","raw":null,"url":null}]}}`,
			contains("500"), pass},
		{"a null text is no text part", 200, `{"message":{"parts":[{"text":null,"data":{}},{"text":"ok"}]}}`,
			equals("ok"), pass},
		// The oneof: text beside raw, url, or data — a data of null included,
		// because data is a google.protobuf.Value and its null is a value.
		{"a part that is text and raw", 200, `{"message":{"parts":[{"text":"500","raw":"AA=="}]}}`, contains("500"),
			fail(ReasonProtocolError)},
		{"a part that is text and url", 200, `{"message":{"parts":[{"text":"500","url":"u"}]}}`, contains("500"),
			fail(ReasonProtocolError)},
		{"a part that is text and a null data", 200, `{"message":{"parts":[{"text":"500","data":null}]}}`,
			contains("500"), fail(ReasonProtocolError)},
		// A field the client does not read is ignored, as a newer minor
		// version's reader must.
		// The duplicate-key check tracks which strings are keys: a value that
		// spells a sibling key, and a string repeated in an array, are not
		// duplicate keys.
		{"a value that spells a key", 200,
			`{"task":{"id":"status","status":{"state":"TASK_STATE_COMPLETED"},"artifacts":[{"parts":[{"text":"text"}]}]}}`,
			equals("text"), pass},
		{"repeated strings in an array", 200,
			`{"message":{"extensions":["a","b","a"],"parts":[{"text":"500"}]}}`, contains("500"), pass},
		{"a body that is not UTF-8", 200, "{\"message\":{\"parts\":[{\"text\":\"500\xff\"}]}}", contains("500"),
			fail(ReasonProtocolError)},
		{"a number too large for a float, in a field never read", 200,
			`{"message":{"parts":[{"text":"500"}]},"extra":1e400}`, contains("500"), pass},
		{"unknown fields", 200, `{"task":{"id":"t","kind":"task","status":{"state":"TASK_STATE_COMPLETED"},` +
			`"artifacts":[{"artifactId":"a","extensions":[],"parts":[{"text":"500","metadata":{"k":"v"}}]}]},"extra":1}`,
			contains("500"), pass},
		// Admission sets exactly one of contains and equals; an expectation
		// with neither is satisfied by nothing.
		{"an empty expectation", 200, completed, assaydv1alpha1.EvalExpect{}, fail(ReasonMismatch)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := send(t, stub(t, tc.code, tc.body), tc.expect); got != tc.want {
				t.Errorf("outcome %+v, want %+v", got, tc.want)
			}
		})
	}
}

// Row 5, first half: a refused connection is Unreachable.
func TestARefusedConnectionIsUnreachable(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	if got := send(t, addr, contains("x")); got != fail(ReasonUnreachable) {
		t.Errorf("outcome %+v, want Unreachable", got)
	}
}

// An address no request can be built for reaches nothing, so it is
// Unreachable rather than a protocol fault of the candidate's.
func TestAnUnusableAddressIsUnreachable(t *testing.T) {
	if got := send(t, "bad host:1", contains("x")); got != fail(ReasonUnreachable) {
		t.Errorf("outcome %+v, want Unreachable", got)
	}
}

// blocker is a candidate that never answers until the test ends.
func blocker(t *testing.T, writeHeader bool) string {
	t.Helper()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if writeHeader {
			w.WriteHeader(200)
			_, _ = io.WriteString(w, `{"task":`)
			w.(http.Flusher).Flush()
		}
		select {
		case <-release:
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	t.Cleanup(func() { close(release); srv.Close() })
	return hostOf(t, srv.URL)
}

// T7: a candidate that sleeps past caseTimeoutSeconds is Unreachable within
// caseTimeoutSeconds plus 1 s — before its answer, and while its body is still
// arriving.
func TestATimeoutIsUnreachableWithinTheBound(t *testing.T) {
	for name, header := range map[string]bool{"no answer": false, "a body that stalls": true} {
		t.Run(name, func(t *testing.T) {
			addr := blocker(t, header)
			start := time.Now()
			got := New().Send(context.Background(), Case{
				Address: addr, Path: "/message:send", Message: "q", Expect: contains("x"), TimeoutSeconds: 1,
			})
			if el := time.Since(start); el > 2*time.Second {
				t.Errorf("the case took %s; caseTimeoutSeconds is 1, so it must end within 2s", el)
			}
			if got != fail(ReasonUnreachable) {
				t.Errorf("outcome %+v, want Unreachable", got)
			}
		})
	}
}

// Row 6, second half: a send cut short by cancellation is Interrupted, never
// Unreachable — Unreachable names a refusal or a timeout that was observed.
func TestACancelledSendIsInterrupted(t *testing.T) {
	for name, header := range map[string]bool{"no answer": false, "a body that stalls": true} {
		t.Run(name, func(t *testing.T) {
			addr := blocker(t, header)
			ctx, cancel := context.WithCancel(context.Background())
			time.AfterFunc(200*time.Millisecond, cancel)
			got := New().Send(ctx, Case{
				Address: addr, Path: "/message:send", Message: "q", Expect: contains("x"), TimeoutSeconds: 5,
			})
			if got != fail(ReasonInterrupted) {
				t.Errorf("outcome %+v, want Interrupted", got)
			}
		})
	}
}

// The request's deadline is caseTimeoutSeconds — not shorter, which would fail
// every slow but honest candidate, and not longer — and a zero, a suite read
// without the API server's default, is the CRD's default, 10 s.
func TestTheDeadlineIsCaseTimeoutSeconds(t *testing.T) {
	for in, want := range map[int32]time.Duration{0: 10 * time.Second, 1: time.Second, 3: 3 * time.Second,
		10: 10 * time.Second} {
		var left time.Duration
		c := &Client{HTTP: doerFunc(func(r *http.Request) (*http.Response, error) {
			d, ok := r.Context().Deadline()
			if !ok {
				return nil, fmt.Errorf("no deadline")
			}
			left = time.Until(d)
			return nil, fmt.Errorf("stop")
		})}
		c.Send(context.Background(), Case{Address: "127.0.0.1:1", Path: "/message:send", Message: "q",
			Expect: contains("x"), TimeoutSeconds: in})
		if left > want || left < want-100*time.Millisecond {
			t.Errorf("caseTimeoutSeconds %d: the request's deadline was %s away, want %s", in, left, want)
		}
	}
}

// A deadline on the caller's context that passes first is a timeout, not an
// interruption: the eval controller may bound the send by caseTimeoutSeconds
// itself, and Interrupted would send its reader looking for an operator
// restart that never happened.
func TestACallerDeadlineIsUnreachable(t *testing.T) {
	for name, header := range map[string]bool{"no answer": false, "a body that stalls": true} {
		t.Run(name, func(t *testing.T) {
			addr := blocker(t, header)
			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			defer cancel()
			got := New().Send(ctx, Case{Address: addr, Path: "/message:send", Message: "q", Expect: contains("x"),
				TimeoutSeconds: 5})
			if got != fail(ReasonUnreachable) {
				t.Errorf("outcome %+v, want Unreachable", got)
			}
		})
	}
}

type doerFunc func(*http.Request) (*http.Response, error)

func (f doerFunc) Do(r *http.Request) (*http.Response, error) { return f(r) }

// T4: the request — POST to the path, A2A-Version: 1.0, a SendMessageRequest
// whose only field is message, and a message of exactly messageId, role
// ROLE_USER and one text part: no contextId, so every case opens a fresh
// context, and no configuration, so the call blocks until the task is
// terminal. No credential is sent. Each case gets a fresh messageId.
func TestTheRequest(t *testing.T) {
	var mu sync.Mutex
	var reqs []*http.Request
	var bodies [][]byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		reqs, bodies = append(reqs, r), append(bodies, b)
		mu.Unlock()
		writeTask(w, "TASK_STATE_COMPLETED", "4")
	}))
	defer srv.Close()

	c := New()
	for range 2 {
		c.Send(context.Background(), Case{
			Address: hostOf(t, srv.URL), Path: "/a2a/message:send", Message: "What is 2 + 2?",
			Expect: contains("4"), TimeoutSeconds: 2,
		})
	}
	if len(reqs) != 2 {
		t.Fatalf("the stub saw %d requests, want 2", len(reqs))
	}
	var ids []string
	for i, r := range reqs {
		if r.Method != http.MethodPost || r.URL.Path != "/a2a/message:send" || r.URL.RawQuery != "" {
			t.Errorf("request %d is %s %s, want POST /a2a/message:send", i, r.Method, r.URL)
		}
		if got := r.Header.Values("A2A-Version"); len(got) != 1 || got[0] != "1.0" {
			t.Errorf("request %d carries A2A-Version %q, want exactly 1.0", i, got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/a2a+json" {
			t.Errorf("request %d has Content-Type %q, want application/a2a+json", i, got)
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("request %d carries a credential", i)
		}
		var top map[string]json.RawMessage
		if err := json.Unmarshal(bodies[i], &top); err != nil {
			t.Fatalf("request %d body is not a JSON object: %v", i, err)
		}
		if k := keys(top); !equal(k, []string{"message"}) {
			t.Errorf("request %d's SendMessageRequest has fields %v, want only message (no configuration, no tenant)", i, k)
		}
		var msg map[string]json.RawMessage
		if err := json.Unmarshal(top["message"], &msg); err != nil {
			t.Fatalf("request %d message: %v", i, err)
		}
		if k := keys(msg); !equal(k, []string{"messageId", "parts", "role"}) {
			t.Errorf("request %d's message has fields %v, want exactly messageId, parts and role (no contextId)", i, k)
		}
		if string(msg["role"]) != `"ROLE_USER"` {
			t.Errorf("request %d role %s, want ROLE_USER", i, msg["role"])
		}
		if string(msg["parts"]) != `[{"text":"What is 2 + 2?"}]` {
			t.Errorf("request %d parts %s, want one text part carrying the case's message", i, msg["parts"])
		}
		var id string
		if err := json.Unmarshal(msg["messageId"], &id); err != nil || id == "" {
			t.Errorf("request %d messageId %s is not a non-empty string", i, msg["messageId"])
		}
		ids = append(ids, id)
	}
	if ids[0] == ids[1] {
		t.Errorf("two cases were sent with the same messageId %q; each send needs a fresh one", ids[0])
	}
}

func keys(m map[string]json.RawMessage) []string {
	var k []string
	for s := range m {
		k = append(k, s)
	}
	sort.Strings(k)
	return k
}

func equal(a, b []string) bool { return strings.Join(a, "\x00") == strings.Join(b, "\x00") }

// T6: a 307 to a second stub that counts hits. The second stub sees zero, and
// the outcome is ProtocolError. 307 and 308 are the codes on which Go's
// default client re-sends a POST body, so a followed redirect would re-post the
// suite's message from the operator's network position.
func TestARedirectIsNotFollowed(t *testing.T) {
	for _, code := range []int{301, 302, 303, 307, 308} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			var hits atomic.Int32
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				hits.Add(1)
				writeTask(w, "TASK_STATE_COMPLETED", "4")
			}))
			defer target.Close()
			redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, target.URL+"/message:send", code)
			}))
			defer redir.Close()

			if got := send(t, hostOf(t, redir.URL), contains("4")); got != fail(ReasonProtocolError) {
				t.Errorf("outcome %+v, want ProtocolError", got)
			}
			if n := hits.Load(); n != 0 {
				t.Errorf("the redirect target was requested %d times; a redirect must not be followed", n)
			}
		})
	}
}

// The production client takes no proxy from the environment: a proxy would
// answer in the candidate's place.
func TestTheClientUsesNoProxy(t *testing.T) {
	hc, ok := New().HTTP.(*http.Client)
	if !ok {
		t.Fatalf("New().HTTP is %T, want *http.Client", New().HTTP)
	}
	tr, ok := hc.Transport.(*http.Transport)
	if !ok || tr.Proxy != nil {
		t.Errorf("the client's transport is %T with a proxy func; it must use no proxy", hc.Transport)
	}
}

const mib = 1 << 20

// paddedTask is a completed task whose JSON is exactly n bytes, padded with
// trailing whitespace, which JSON permits.
func paddedTask(t *testing.T, n int) string {
	t.Helper()
	b := `{"task":{"status":{"state":"TASK_STATE_COMPLETED"},"artifacts":[{"parts":[{"text":"4"}]}]}}`
	if len(b) > n {
		t.Fatalf("cannot pad to %d", n)
	}
	return b + strings.Repeat(" ", n-len(b))
}

// T8: the 1 MiB cap. A body of 1 MiB + 1 byte is ProtocolError, and at most
// 1 MiB + 1 byte of it is read; a body of exactly 1 MiB is read whole.
func TestTheBodyCap(t *testing.T) {
	if got := send(t, stub(t, 200, paddedTask(t, mib)), contains("4")); got != pass {
		t.Errorf("a 1 MiB answer: outcome %+v, want a pass — the cap is 1 MiB, not less", got)
	}
	if got := send(t, stub(t, 200, paddedTask(t, mib+1)), contains("4")); got != fail(ReasonProtocolError) {
		t.Errorf("a 1 MiB + 1 byte answer: outcome %+v, want ProtocolError", got)
	}

	// How much is read, through the injection seam: a body four times the cap.
	body := &countingReader{r: strings.NewReader(paddedTask(t, 4*mib))}
	c := &Client{HTTP: doerFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(body), Header: http.Header{}, Request: r}, nil
	})}
	got := c.Send(context.Background(), Case{Address: "127.0.0.1:1", Path: "/message:send", Message: "q",
		Expect: contains("4"), TimeoutSeconds: 2})
	if got != fail(ReasonProtocolError) {
		t.Errorf("a 4 MiB answer: outcome %+v, want ProtocolError", got)
	}
	if body.n > mib+1 {
		t.Errorf("%d bytes of the answer were read; at most 1 MiB + 1 (%d) may be", body.n, mib+1)
	}
}

type countingReader struct {
	r io.Reader
	n int
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += n
	return n, err
}

// An answer whose body breaks off before the timeout — a reset, say — was
// answered: the candidate sent a status, so the case is ProtocolError, not
// Unreachable.
func TestABodyThatBreaksOffIsAProtocolError(t *testing.T) {
	c := &Client{HTTP: doerFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Request: r,
			Body: io.NopCloser(io.MultiReader(strings.NewReader(`{"task":`), errReader{}))}, nil
	})}
	got := c.Send(context.Background(), Case{Address: "127.0.0.1:1", Path: "/message:send", Message: "q",
		Expect: contains("4"), TimeoutSeconds: 2})
	if got != fail(ReasonProtocolError) {
		t.Errorf("outcome %+v, want ProtocolError", got)
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, fmt.Errorf("connection reset by peer") }
