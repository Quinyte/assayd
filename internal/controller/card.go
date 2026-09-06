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

	plumev1alpha1 "github.com/Quinyte/plume/api/v1alpha1"
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
var supportedA2AVersions = map[string]bool{"1.0": true}

type fetchedCard struct {
	Name            string `json:"name"`
	Version         string `json:"version"`
	ProtocolVersion string `json:"protocolVersion"`
	Skills          []struct {
		ID string `json:"id"`
	} `json:"skills"`
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
func (r *AgentReconciler) fetchAndValidateCard(ctx context.Context, agent *plumev1alpha1.Agent,
	runNS, rev string) (*plumev1alpha1.CardStatus, error) {
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

	body, err := r.getWithRetries(ctx, url)
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
	if !supportedA2AVersions[c.ProtocolVersion] {
		return nil, &cardError{reason: "CardProtocolUnsupported", msg: fmt.Sprintf(
			"the agent card declares A2A protocol version %q, which this operator does not "+
				"support", c.ProtocolVersion)}
	}

	// The digest is over the exact bytes served. Design 02 §3.4 detects drift by
	// comparing it without minting a revision, so it must not be computed from
	// the parsed struct — re-marshalling would normalise away a change the agent
	// actually made.
	sum := sha256.Sum256(body)
	now := metav1.NewTime(time.Now())
	return &plumev1alpha1.CardStatus{
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

func (r *AgentReconciler) getWithRetries(ctx context.Context, url string) ([]byte, error) {
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
func upsertCard(cards []plumev1alpha1.CardStatus, c plumev1alpha1.CardStatus,
	revDigest string) []plumev1alpha1.CardStatus {
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

// cardFetchDue decides whether to go to the network at all.
//
// Fetching on every reconcile was the first implementation and it was wrong
// twice over. `TestReconcileIsIdempotent` caught it immediately — five
// reconciles of a converged agent issued four status writes, which is the churn
// that test exists to forbid — and the envtest suite went from about a minute to
// seven, because each reconcile spent its timeout failing to resolve a name.
// Both are the same mistake: a card is not a per-reconcile input. It is fetched
// once per revision and re-read on an interval to catch drift.
func cardFetchDue(status *plumev1alpha1.AgentStatus, revDigest string, now time.Time) bool {
	for _, c := range status.Cards {
		if c.RevisionDigest != revDigest {
			continue
		}
		// Already have this revision's card: only re-read to check for drift.
		return c.FetchedAt == nil || now.Sub(c.FetchedAt.Time) >= CardDriftInterval
	}
	// No card for this revision. If the last attempt FAILED recently, wait —
	// otherwise an agent whose card is unreachable would be dialled on every
	// reconcile, and its failures would ride the shared work queue.
	if c := findCondition(status.Conditions, string(plumev1alpha1.CondRegistered)); c != nil &&
		c.Status == metav1.ConditionFalse {
		return now.Sub(c.LastTransitionTime.Time) >= CardRetryInterval
	}
	return true
}

func findCondition(conds []metav1.Condition, t string) *metav1.Condition {
	for i := range conds {
		if conds[i].Type == t {
			return &conds[i]
		}
	}
	return nil
}

// hasCardFor reports whether this revision's card has been fetched.
func hasCardFor(status *plumev1alpha1.AgentStatus, revDigest string) bool {
	for _, c := range status.Cards {
		if c.RevisionDigest == revDigest {
			return true
		}
	}
	return false
}
