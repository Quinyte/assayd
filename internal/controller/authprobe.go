// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// AuthProber sends design 03 §3.3.3's anonymous probe: one `GET` of an Agent's
// card path, to the Gateway's serving listener, with the Agent's `Host` header
// and no credential.
//
// It is an interface so that envtest can answer with §8.1's pinned oracle,
// because there is no gateway there to answer. The production implementation
// is httpAuthProber.
type AuthProber interface {
	Probe(ctx context.Context, req AuthProbeRequest) (AuthProbeAnswer, error)
}

// AuthProbeRequest is one anonymous request. It carries no key: `Create`'s
// probe never mints one (§3.3.3).
type AuthProbeRequest struct {
	// URL is --gateway-serving-url followed by the Agent's card path.
	URL string
	// Host is the Agent's hostname, which is what the serving route matches.
	Host string
}

// AuthProbeAnswer is what came back. Only the status code is read today:
// `Create` publishes on a `401` and on nothing else.
type AuthProbeAnswer struct {
	Code int
}

// probeTransport is http.DefaultTransport with NO proxy. The probe must reach
// the Gateway's serving listener itself: a proxy taken from HTTP_PROXY in the
// operator's environment would answer in its place, and a proxy that answers
// 401 would pass for the Agent's -auth.
var probeTransport = func() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = nil
	return t
}()

// httpAuthProber is the probe on a real cluster.
//
// It follows no redirect. A `3xx` is an answer to record, and following it
// would put a different host's reply in the transaction's `probe.after`.
type httpAuthProber struct {
	timeout time.Duration
}

func (p httpAuthProber) Probe(ctx context.Context, req AuthProbeRequest) (AuthProbeAnswer, error) {
	timeout := p.timeout
	if timeout <= 0 {
		timeout = DefaultCardFetchTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, req.URL, nil)
	if err != nil {
		return AuthProbeAnswer{}, fmt.Errorf("build the probe of %s: %w", req.URL, err)
	}
	r.Host = req.Host
	c := &http.Client{Transport: probeTransport, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := c.Do(r)
	if err != nil {
		return AuthProbeAnswer{}, err
	}
	// Drained, bounded, and never kept: the body of a 200 would be the card, and
	// nothing here needs it yet.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	_ = resp.Body.Close()
	return AuthProbeAnswer{Code: resp.StatusCode}, nil
}
