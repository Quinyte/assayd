// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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

// AuthProbeAnswer is what came back: the status code, and for a `200` the
// SHA-256 of the body, which `Lock`'s one before-request compares with the
// card digest status.cards records for the revision the route names (§3.3.3).
// The body itself is never kept or logged.
type AuthProbeAnswer struct {
	Code int
	// CardDigest is the hex SHA-256 of a 200's body, computed as the card
	// fetch computes it (fetchAndValidateCard), or "".
	CardDigest string
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
	// Bounded, as the card fetch is, and never kept: only its digest is, and
	// only for a 200, which is the card while the route is open.
	h := sha256.New()
	_, cerr := io.Copy(h, io.LimitReader(resp.Body, 1<<20))
	_ = resp.Body.Close()
	answer := AuthProbeAnswer{Code: resp.StatusCode}
	if resp.StatusCode == http.StatusOK && cerr == nil {
		answer.CardDigest = hex.EncodeToString(h.Sum(nil))
	}
	return answer, nil
}
