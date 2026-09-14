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
	"net/url"
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
	// URL is --gateway-serving-url's scheme and host with the Agent's card
	// path after them (probeURL).
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

// directTransport is http.DefaultTransport with NO proxy, for the two requests
// the operator sends to an address it derived itself: this probe, to the
// Gateway's serving listener, and the card fetch, to a revision's Service. A
// proxy taken from HTTP_PROXY in the operator's environment would answer in
// the target's place — a proxy that answers 401 would pass for the Agent's
// -auth, and a proxy's card would be registered as the revision's.
var directTransport = func() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = nil
	return t
}()

// refuseRedirects is the redirect policy of the same two requests. A 3xx comes
// back as the answer and is never followed, because following it sends the
// operator to an address the other side chose.
func refuseRedirects(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

// cardURL is how both of those requests build their URL: the scheme and host
// are the operator's, and the Agent's spec.card.path is only ever a PATH.
// Concatenated onto the host as a string, a path of `@elsewhere/...` turned the
// derived address into userinfo and named the host itself, which is the
// redirect with no redirect in it. url.URL puts a path that does not begin with
// "/" after one, so no path can reach the authority.
func cardURL(scheme, host, path string) string {
	return (&url.URL{Scheme: scheme, Host: host, Path: path}).String()
}

// probeURL is the probe's cardURL, on --gateway-serving-url's scheme and host.
// ValidateGatewayServingURL has already refused a path, query, fragment or
// userinfo on it, so those two are all it contributes.
func probeURL(servingURL, path string) (string, error) {
	u, err := url.Parse(servingURL)
	if err != nil {
		return "", fmt.Errorf("--gateway-serving-url %q: %w", servingURL, err)
	}
	return cardURL(u.Scheme, u.Host, path), nil
}

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
	c := &http.Client{Transport: directTransport, CheckRedirect: refuseRedirects}
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
