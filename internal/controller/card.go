// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
)

// Card fetch, design 02 §3.4. The card is served by the CONTAINER and the
// source of truth is the agent's code (ADR-0019), so the operator reads it from
// the running revision rather than from anything the user wrote in the CR.
//
// **What this implements, and what §5 still says it does not.** Implemented
// here: the in-cluster fetch after the revision is available, with bounded
// retries; parseability; the name matching the CR; a supported A2A protocol
// version; a per-revision digest so drift is detectable. Not implemented, and
// each would be a promise this repository cannot keep:
//
//   - **Signature verification.** Design 09 signs cards with Sigstore keyless
//     at build time and nothing in this repository signs anything, so every
//     card is unsigned. `CardUnsigned` is therefore set TRUE rather than left
//     silent — design 09's own rule is that an unsigned card registers with a
//     loud condition, and a fixture-built card is exactly the BYO case that
//     rule was written for.
//   - **The card-vs-CR skill cross-check.** §3.4 fails registration when a card
//     advertises a skill needing a tool or graph the CR does not grant. Tool
//     resolution is design 11's and does not exist, so there is nothing to
//     check a skill against; inventing a check here would report a guarantee
//     nothing enforces.
//   - **The 30-minute `registrationDeadline`.** §3.4 already records that
//     nothing counts it down and that it is neither a spec field nor an
//     operator constant.
//   - **The directory update and the drift event.** Design 05 owns the first
//     and no `EventRecorder` is wired for the second (§5).
//
// A failed fetch does NOT withhold traffic. §3.4 makes an unregistrable card a
// registration failure, not a serving one, and the deadline that would
// eventually fail the candidate is the unimplemented part above — so blocking
// here would invent an enforcement the design does not describe.
// ONE attempt per reconcile, with a short timeout, and retries across
// reconciles rather than inside one.
//
// §3.4 says "3 retries, backoff", and a first implementation read that as three
// in-line attempts with a growing sleep. An envtest exposed the cost
// immediately: an agent whose card cannot be reached made every reconcile block
// for roughly sixteen seconds, and the suite went from a minute to past ten. In
// a real cluster that is worse than slow — the operator's work queue is shared,
// so one unreachable agent would delay reconciles for every OTHER agent,
// turning a registration failure into a fleet-wide stall.
//
// The reconciler is level-triggered, so the retries belong between reconciles:
// one attempt here, and a requeue that brings us back. The design's intent —
// bounded retries with backoff before giving up — is preserved; where the sleep
// happens is not something §3.4 specifies.
const (
	// DefaultCardFetchTimeout must cover a COLD in-cluster fetch: a DNS lookup
	// against CoreDNS plus a TCP connect to a Service that may have just been
	// programmed. A first version used 2s to make envtest cheaper and the e2e
	// then failed with "context deadline exceeded" against a responder that was
	// demonstrably serving — tuning a production timeout for test speed, which is
	// the wrong trade in the obvious direction. The suite's cost is controlled by
	// cardFetchDue and by AgentReconciler.CardFetchTimeout instead.
	DefaultCardFetchTimeout = 5 * time.Second
	// CardRetryInterval is how soon a failed fetch is retried. Exported so the
	// reconcile's requeue and this file cannot disagree about it.
	CardRetryInterval = 15 * time.Second
)

// supportedA2AVersions is a closed set. An unrecognised version is a
// registration failure rather than a shrug: the operator cannot know what an
// unknown protocol's card means, and treating it as valid would register an
// agent whose contract nothing understood.
//
// **The value in it is unverified.** A review pointed out that this set was
// written to match the string the repository's own test fixture serves, and no
// document here states what a real A2A v1.0 card carries in `protocolVersion` —
// the two sides were written by one hand to agree, so nothing about the
// protocol has been checked. Design 09 describes A2A v1.0 as JSON-RPC + SSE and
// the fixture answers a bespoke HTTP JSON endpoint. `/research-latest` must pin
// the real card and task shapes before design 03's route assumes them; §5
// carries this.
var supportedA2AVersions = map[string]bool{"1.0": true}

// fetchedCard is the A2A **v1.0** Agent Card, as far as this operator reads it.
// Pinned to `a2aproject/A2A` @ `v1.0.1` `specification/a2a.proto`; see
// `docs/research/a2a-v1.0-card-and-transport-2026-09.md`.
//
// The previous version of this struct was a **v0.x** card — top-level `url` and
// `protocolVersion` — which v1.0 removed and folded into `supportedInterfaces`.
// Nothing caught it because the repository's own fixture served the same wrong
// shape: both sides were written by one hand to agree, and the supported-version
// check was written to match the fixture's string rather than any spec. The
// consequence was not cosmetic. A conformant v1.0 card has no top-level
// `protocolVersion`, so it decoded as "" and failed the check — **registration
// would have failed for every real A2A agent**, which is the one population this
// code exists to serve.
type fetchedCard struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     string `json:"version"`
	// SupportedInterfaces is REQUIRED and ordered. Each entry carries its own
	// protocol version, because one agent may expose several bindings.
	SupportedInterfaces []agentInterface  `json:"supportedInterfaces"`
	Capabilities        agentCapabilities `json:"capabilities"`
	Skills              []struct {
		ID string `json:"id"`
	} `json:"skills"`
}

type agentInterface struct {
	URL string `json:"url"`
	// ProtocolBinding is an open string; the spec names JSONRPC, GRPC and
	// HTTP+JSON as the officially supported ones.
	ProtocolBinding string `json:"protocolBinding"`
	// ProtocolVersion is MAJOR.MINOR — the proto's own examples are "0.3" and
	// "1.0", so the string this operator supports was right at the wrong key.
	ProtocolVersion string `json:"protocolVersion"`
}

// agentCapabilities is the whole of it: four fields, verified against the proto.
// `sharedTaskState` is NOT among them and appears nowhere in the specification —
// `grep -ci shared` over a2a.proto returns 0. Design 02 §3.2 keys
// TaskStateUnverified off that invented field, so the condition could never
// clear for a conformant agent; §5 and A71 record it.
type agentCapabilities struct {
	Streaming         bool `json:"streaming"`
	PushNotifications bool `json:"pushNotifications"`
	ExtendedAgentCard bool `json:"extendedAgentCard"`
	Extensions        []struct {
		URI      string `json:"uri"`
		Required bool   `json:"required"`
	} `json:"extensions"`
}

// cardError is a registration failure with a reason an operator can act on.
type cardError struct {
	reason string
	msg    string
}

func (e *cardError) Error() string { return e.msg }

// fetchAndValidateCard reads the card from the revision's Service and checks
// what can be checked. The URL is the revision Service, not the Pod: a Pod IP
// changes under the operator and the Service is the address design 03's route
// will name, so fetching from anywhere else would validate a card served by
// something other than what serves traffic.
func (r *AgentReconciler) fetchAndValidateCard(ctx context.Context, agent *assaydv1alpha1.Agent,
	runNS, rev string) (*assaydv1alpha1.CardStatus, error) {
	path := agent.Spec.Card.Path
	if path == "" {
		path = "/.well-known/agent-card.json"
	}
	// Addressed by the Service's ClusterIP, not by its DNS name.
	//
	// The operator created this Service and can read it, so resolving it through
	// the API is strictly more reliable than depending on cluster DNS from the
	// operator's own Pod — a dependency that bought nothing and was measured
	// failing: an e2e reached the identical URL from a curl Pod in the operator's
	// namespace and got the card, while the operator itself timed out on every
	// attempt. Whatever the resolver difference is, it is not one this fetch
	// needs to care about.
	//
	// Design 03's route will still name the Service; that is a different consumer
	// with a different resolver. This is the operator reading an object it owns.
	var svc corev1.Service
	svcName := WorkloadName(agent.Name, rev)
	if err := r.Get(ctx, types.NamespacedName{Namespace: runNS, Name: svcName}, &svc); err != nil {
		return nil, &cardError{reason: "CardUnreachable", msg: fmt.Sprintf(
			"the revision's Service %s/%s could not be read, so the card cannot be fetched: %v",
			runNS, svcName, err)}
	}
	if svc.Spec.ClusterIP == "" || svc.Spec.ClusterIP == "None" {
		return nil, &cardError{reason: "CardUnreachable", msg: fmt.Sprintf(
			"the revision's Service %s/%s has no ClusterIP", runNS, svcName)}
	}
	url := fmt.Sprintf("http://%s%s",
		net.JoinHostPort(svc.Spec.ClusterIP, fmt.Sprint(port(agent.Spec.Runtime))), path)

	body, err := r.fetchOnce(ctx, url)
	if err != nil {
		return nil, &cardError{reason: "CardUnreachable", msg: fmt.Sprintf(
			"could not fetch the agent card from %s: %v. Retrying every %s; this does not "+
				"withhold traffic", url, err, CardRetryInterval)}
	}

	var c fetchedCard
	if err := json.Unmarshal(body, &c); err != nil {
		return nil, &cardError{reason: "CardUnparseable", msg: fmt.Sprintf(
			"the agent card at %s is not valid JSON: %v", url, err)}
	}
	if c.Name != agent.Name {
		return nil, &cardError{reason: "CardNameMismatch", msg: fmt.Sprintf(
			"the agent card names %q and the Agent is %q. The card is the agent's own "+
				"description of itself, so a mismatch means this workload is not the agent "+
				"this CR describes", c.Name, agent.Name)}
	}
	// At least one interface must speak a version this operator supports. The
	// array is ordered by the agent's preference and an agent may expose several
	// bindings, so this is an ANY check, not a check of the first entry.
	if len(c.SupportedInterfaces) == 0 {
		return nil, &cardError{reason: "CardNoInterfaces", msg: fmt.Sprintf(
			"the agent card at %s declares no supportedInterfaces, which A2A v1.0 requires. "+
				"A v0.x card carrying a top-level url and protocolVersion decodes this way",
			url)}
	}
	var versions []string
	supported := false
	for _, iface := range c.SupportedInterfaces {
		versions = append(versions, iface.ProtocolVersion)
		if supportedA2AVersions[iface.ProtocolVersion] {
			supported = true
		}
	}
	if !supported {
		return nil, &cardError{reason: "CardProtocolUnsupported", msg: fmt.Sprintf(
			"the agent card declares A2A protocol versions %v and this operator supports none "+
				"of them", versions)}
	}

	// The digest is over the exact bytes served. Design 02 §3.4 detects drift by
	// comparing it without minting a revision, so it must not be computed from
	// the parsed struct — re-marshalling would normalise away a change the agent
	// actually made.
	sum := sha256.Sum256(body)
	now := metav1.NewTime(time.Now())
	return &assaydv1alpha1.CardStatus{
		Revision:  rev,
		Name:      c.Name,
		Version:   c.Version,
		Digest:    hex.EncodeToString(sum[:]),
		FetchedAt: &now,
		// Nothing in this repository signs a card, so this is false for every
		// agent today and CardUnsigned is raised alongside it.
		Signed: false,
	}, nil
}

// fetchOnce makes exactly one attempt. It was called getWithRetries when it
// looped; the loop moved between reconciles and the name did not follow it.
func (r *AgentReconciler) fetchOnce(ctx context.Context, url string) ([]byte, error) {
	client := r.CardClient
	if client == nil {
		client = &http.Client{}
	}
	{
		timeout := r.CardFetchTimeout
		if timeout <= 0 {
			timeout = DefaultCardFetchTimeout
		}
		fetchCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		req, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		// Bounded: a card is a small document and an agent that streams gigabytes
		// at this path should fail rather than exhaust the operator's memory.
		body, rerr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		if rerr != nil {
			return nil, rerr
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("status %d", resp.StatusCode)
		}
		return body, nil
	}
}

// upsertCard replaces this revision's entry, keeping other revisions'. Both the
// name and the full digest are written: design 16 requires {name, digest} at
// every gate step, and a card accepted under a chosen 40-bit collision would
// otherwise attach a verdict to the wrong projection.
func upsertCard(cards []assaydv1alpha1.CardStatus, c assaydv1alpha1.CardStatus,
	revDigest string) []assaydv1alpha1.CardStatus {
	c.RevisionDigest = revDigest
	for i := range cards {
		if cards[i].RevisionDigest == revDigest {
			cards[i] = c
			return cards
		}
	}
	return append(cards, c)
}

// CardDriftInterval is how often a card already fetched is read again. §3.4
// detects drift — the container's card changing without a spec change — by
// re-fetching and comparing the digest, and something has to say how often.
const CardDriftInterval = 5 * time.Minute

// cardFetchDue decides whether to go to the network at all, and when to come
// back.
//
// It keys on the per-revision entry in status.cards[], including one written
// for a FAILED attempt with an empty Digest. That entry is the attempt record,
// and it exists because a review measured the alternative failing: keying the
// retry gate on the Registered condition's LastTransitionTime meant the gate
// opened permanently after its first interval, because merge() correctly
// freezes that timestamp while the status stays False — five back-to-back
// reconciles produced five dials, each able to block the shared work queue for
// the fetch timeout.
//
// Fetching on every reconcile was the first implementation and was wrong twice
// over: TestReconcileIsIdempotent caught five reconciles of a converged agent
// issuing four status writes, and the envtest suite went from about a minute to
// seven. A card is not a per-reconcile input.
func cardFetchDue(status *assaydv1alpha1.AgentStatus, revDigest string, now time.Time) bool {
	c := cardEntry(status, revDigest)
	if c == nil {
		return true // never attempted
	}
	if c.FetchedAt == nil {
		return true
	}
	if c.Digest == "" {
		// An attempt that failed. Retry on the retry interval.
		return now.Sub(c.FetchedAt.Time) >= CardRetryInterval
	}
	// Registered. Re-read only to check for drift.
	return now.Sub(c.FetchedAt.Time) >= CardDriftInterval
}

// cardRequeueAfter says when to come back, and it is what makes CardDriftInterval
// mean anything.
//
// A review measured the previous behaviour: after a successful registration
// Reconcile returned RequeueAfter 0, no SyncPeriod is set, so the manager's
// ~10h default applied and a converged agent was never re-read. The constant's
// comment described a schedule that did not exist — a bound nothing enforced.
func cardRequeueAfter(status *assaydv1alpha1.AgentStatus, revDigest string) time.Duration {
	c := cardEntry(status, revDigest)
	if c == nil || c.Digest == "" {
		return CardRetryInterval
	}
	return CardDriftInterval
}

func cardEntry(status *assaydv1alpha1.AgentStatus, revDigest string) *assaydv1alpha1.CardStatus {
	for i := range status.Cards {
		if status.Cards[i].RevisionDigest == revDigest {
			return &status.Cards[i]
		}
	}
	return nil
}

func findCondition(conds []metav1.Condition, t string) *metav1.Condition {
	for i := range conds {
		if conds[i].Type == t {
			return &conds[i]
		}
	}
	return nil
}

// hasCardFor reports whether this revision is REGISTERED — an entry with an
// empty digest is a failed attempt, not a card.
func hasCardFor(status *assaydv1alpha1.AgentStatus, revDigest string) bool {
	c := cardEntry(status, revDigest)
	return c != nil && c.Digest != ""
}

// recordCardAttempt stamps a failed attempt so the retry gate has something to
// measure from that does not freeze the way a condition timestamp does.
func recordCardAttempt(status *assaydv1alpha1.AgentStatus, rev, revDigest string, at time.Time) {
	t := metav1.NewTime(at)
	if c := cardEntry(status, revDigest); c != nil {
		c.FetchedAt = &t
		c.Digest = ""
		c.Revision = rev
		return
	}
	// Revision is set even though this is not a card: pruneCards collects by
	// revision name, so an attempt record without one would be the one thing in
	// status that never gets collected.
	status.Cards = append(status.Cards, assaydv1alpha1.CardStatus{
		Revision: rev, RevisionDigest: revDigest, FetchedAt: &t,
	})
}

// pruneCards drops entries for revisions that have left the retained set.
// §3.4 says a card is "collected with revisions"; nothing collected them, so an
// Agent accumulated one entry per revision it had ever registered, forever.
func pruneCards(status *assaydv1alpha1.AgentStatus, keep map[string]bool) bool {
	if len(status.Cards) == 0 {
		return false
	}
	before := len(status.Cards)
	kept := status.Cards[:0]
	for _, c := range status.Cards {
		if keep[c.Revision] {
			kept = append(kept, c)
		}
	}
	status.Cards = kept
	return len(kept) != before
}
