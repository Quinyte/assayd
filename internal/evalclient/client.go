// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

// Package evalclient is design 16 §1.1's A2A eval client: one case, sent as
// one A2A 1.0 SendMessage task on the HTTP+JSON binding to a candidate
// revision's Service, and scored by the outcome table of step 6.
//
// It is the client and nothing else. Who calls it, when, and what is recorded
// is the eval controller's, which does not exist yet: nothing in the operator
// calls this package in this release.
package evalclient

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/evalsuite"
)

// MaxBodyBytes is the most of an answer that is read: 1 MiB. An answer longer
// than that is ProtocolError, and at most one byte past it is read to tell.
const MaxBodyBytes = 1 << 20

// Reason is why a case failed. The spellings are design 16 §1.1 step 6's.
type Reason string

const (
	// ReasonMismatch: the task completed, or the answer was a message, and
	// its text does not satisfy the case's expectation.
	ReasonMismatch Reason = "Mismatch"
	// ReasonTaskNotCompleted: 200, and the task is in a state other than
	// TASK_STATE_COMPLETED.
	ReasonTaskNotCompleted Reason = "TaskNotCompleted"
	// ReasonProtocolError: any status but 200 — a 3xx included, since no
	// redirect is followed — a body over MaxBodyBytes, or a body that is not
	// a SendMessageResponse.
	ReasonProtocolError Reason = "ProtocolError"
	// ReasonUnreachable: no answer, because the request could not be made or
	// the case's timeout passed first.
	ReasonUnreachable Reason = "Unreachable"
	// ReasonInterrupted: the send was cut short by cancellation of the
	// caller's context. The eval controller also records it for a claim found
	// with no outcome, which this package never sees.
	ReasonInterrupted Reason = "Interrupted"
)

// Outcome is a case's result. A pass has no reason; a failure always has one.
// It carries nothing of the answer: design 16 records {id, outcome, reason}
// and never the answer text.
//
// How the outcome is spelled in status.eval.cases[].outcome is the eval
// controller's to decide; §1.1 writes it as pass and fail but does not fix a
// stored spelling, and this package does not either.
type Outcome struct {
	Passed bool
	Reason Reason
}

// Case is one case to send.
type Case struct {
	// Address is the candidate revision's Service, as ClusterIP:port — the
	// address the operator derived, never one the card names (step 3).
	Address string
	// Path is status.eval.endpointPath, from EndpointPath.
	Path string
	// Message is the case's message, sent as the task's one text part.
	Message string
	// Expect is what the answer text must satisfy.
	Expect assaydv1alpha1.EvalExpect
	// TimeoutSeconds is the suite's caseTimeoutSeconds. Zero is the CRD's
	// default, evalsuite.DefaultCaseTimeoutSeconds.
	TimeoutSeconds int32
}

// Doer is the injection seam: *http.Client satisfies it, and a test can
// answer in the candidate's place.
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// Client sends cases. HTTP is the seam; New sets it to the production client.
type Client struct {
	HTTP Doer
}

// New is the eval client on a real cluster.
//
// It follows no redirect: a 3xx comes back as the answer and is
// ProtocolError. Go's default client follows up to ten, and on 307 and 308
// re-sends a POST body, so a candidate answering 307 Location: <anywhere>
// would make the operator re-post the suite's message from its own network
// position (design 16 §1.1 step 4). And it takes no proxy from the
// environment, because a proxy would answer in the candidate's place. Both are
// the card fetch's and the auth probe's policy (internal/controller,
// directTransport and refuseRedirects), mirrored here rather than shared,
// because the controller will import this package.
//
// Every Client New returns shares one transport, and so one pool of
// keep-alive connections, as the controller's card fetch and probe share
// theirs. A Client is meant to be built once and reused; building one per
// send costs nothing more than the struct.
func New() *Client {
	return &Client{HTTP: &http.Client{
		Transport: directTransport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}}
}

// directTransport is http.DefaultTransport with no proxy, shared by every
// Client New returns.
var directTransport = func() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = nil
	return t
}()

// Send sends one case and scores the answer. It makes exactly one request and
// never retries: a case is sent at most once (design 16 §1.1 step 10), and the
// claim that guarantees it is the caller's.
//
// It returns an error, and sends nothing, only when no request can be built
// from cs — an Address or Path that does not make a URL. That is the caller's
// fault and not the candidate's, so it is no outcome: recording one would
// fail the candidate, and step 7's failed[] would make that final. Check
// validates the same thing without sending, so the caller can refuse such a
// case before it claims it.
//
// The request is bounded by the case's timeout, derived from ctx. If ctx is
// cancelled first, the outcome is Interrupted, never Unreachable. A deadline
// on ctx that passes first is a timeout like the case's own, Unreachable: the
// eval controller may bound the send by caseTimeoutSeconds itself. It must not
// bound it tighter, because Unreachable says the candidate gave no answer
// within caseTimeoutSeconds, and a shorter wait did not establish that (#105).
//
// Unreachable is every failure before a status line was read — a refused or
// reset connection, an address that does not resolve, an answer that is not
// HTTP — and the timeout, whether it passes before the status line or while a
// 200's body is still arriving. Design 16 §1.1 step 6 names a refused connection and
// a timeout; the other transport failures are not in its table, and are read
// as the candidate not being reached rather than as its protocol fault.
func (c *Client) Send(ctx context.Context, cs Case) (Outcome, error) {
	timeout := time.Duration(cs.TimeoutSeconds) * time.Second
	if cs.TimeoutSeconds <= 0 {
		timeout = evalsuite.DefaultCaseTimeoutSeconds * time.Second
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// A struct of strings always marshals.
	body, _ := json.Marshal(sendMessageRequest{Message: requestMessage{
		MessageID: newMessageID(),
		Role:      "ROLE_USER",
		Parts:     []requestPart{{Text: cs.Message}},
	}})
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, cs.target(), bytes.NewReader(body))
	if err != nil {
		return Outcome{}, fmt.Errorf("build the request for this case: %w", err)
	}
	// A2A 1.0 §3.6: a client MUST send A2A-Version, and an agent reads its
	// absence as 0.3. The content type is the one the specification's §11.1
	// says the HTTP+JSON binding SHOULD use, read from its main branch and not
	// checked against the v1.0.1 tag (#105).
	req.Header.Set("A2A-Version", "1.0")
	req.Header.Set("Content-Type", "application/a2a+json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return failed(ctx, ReasonUnreachable), nil
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return Outcome{Reason: ReasonProtocolError}, nil
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, MaxBodyBytes+1))
	if err != nil {
		if reqCtx.Err() != nil {
			return failed(ctx, ReasonUnreachable), nil
		}
		return Outcome{Reason: ReasonProtocolError}, nil
	}
	if len(raw) > MaxBodyBytes {
		return Outcome{Reason: ReasonProtocolError}, nil
	}
	return score(raw, cs.Expect), nil
}

// Check reports whether a request can be built for cs, without sending one.
// Send fails on exactly the cases Check refuses.
func Check(cs Case) error {
	if _, err := http.NewRequest(http.MethodPost, cs.target(), nil); err != nil {
		return fmt.Errorf("build the request for this case: %w", err)
	}
	return nil
}

// target is the case's URL: the operator's address, the card's path.
func (cs Case) target() string {
	return (&url.URL{Scheme: "http", Host: cs.Address, Path: cs.Path}).String()
}

// failed is the outcome of a request that ended without a full answer:
// Interrupted when the caller cancelled, otherwise the observed reason.
func failed(ctx context.Context, observed Reason) Outcome {
	if errors.Is(ctx.Err(), context.Canceled) {
		return Outcome{Reason: ReasonInterrupted}
	}
	return Outcome{Reason: observed}
}

// satisfies compares case-sensitively, with no normalization. Admission
// guarantees exactly one of the two is set.
func satisfies(answer string, expect assaydv1alpha1.EvalExpect) bool {
	if expect.Equals != "" {
		return answer == expect.Equals
	}
	return expect.Contains != "" && strings.Contains(answer, expect.Contains)
}

// newMessageID is a fresh messageId for every send. A2A lets an agent
// de-duplicate by it, and the design relies on no agent doing so: a case sent
// twice is two tasks, which is why the caller claims before it sends.
func newMessageID() string {
	var b [16]byte
	_, _ = rand.Read(b[:]) // crypto/rand.Read never returns an error
	return "eval-" + hex.EncodeToString(b[:])
}

// The SendMessage request, as protojson renders a2a.proto at v1.0.1
// (docs/research/a2a-v1.0-card-and-transport-2026-09.md §2). It declares only
// what is sent: no contextId, so every case opens a
// fresh context, and no configuration, so the call blocks until the task is
// terminal or interrupted — the protocol's default, which step 6's scoring
// depends on.
type sendMessageRequest struct {
	Message requestMessage `json:"message"`
}

type requestMessage struct {
	MessageID string        `json:"messageId"`
	Role      string        `json:"role"`
	Parts     []requestPart `json:"parts"`
}

type requestPart struct {
	Text string `json:"text"`
}
