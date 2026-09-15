// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
)

// The validation logic, tested where it can be reached. The fetch itself needs
// a cluster (the URL names a Service), so these drive it through an injected
// client against a real HTTP server rather than asserting about a mock.
func cardFixture(t *testing.T, body string, status int) (*AgentReconciler, *assaydv1alpha1.Agent) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	a := &assaydv1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{Name: "pa-reviewer", Namespace: "team"},
		Spec:       assaydv1alpha1.AgentSpec{Runtime: &assaydv1alpha1.AgentRuntime{Port: 8080}},
	}
	// The fetch reads the revision's Service to get its ClusterIP, so the fake
	// client must hold one. redirectTo then sends the request to the test server
	// whatever address was built; the address CONSTRUCTION is exercised by
	// directFixture's tests below and by the e2e.
	r := &AgentReconciler{
		CardClient: redirectTo{srv.URL},
		Client: fake.NewClientBuilder().WithScheme(testScheme()).WithObjects(&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Namespace: "assayd-run-team", Name: "pa-reviewer-abc123"},
			Spec:       corev1.ServiceSpec{ClusterIP: "10.43.0.9"},
		}).Build(),
	}
	return r, a
}

// redirectTo sends every request to the test server, whatever in-cluster URL
// the operator built, through the production client's policy: no redirect
// followed, no proxy taken. The URL construction is exercised by directFixture
// below and by the e2e.
type redirectTo struct{ base string }

func (d redirectTo) Do(req *http.Request) (*http.Response, error) {
	u := d.base + req.URL.Path
	out, err := http.NewRequestWithContext(req.Context(), req.Method, u, nil)
	if err != nil {
		return nil, err
	}
	return cardHTTPClient.Do(out)
}

// A real A2A v1.0 card. protocolVersion lives inside supportedInterfaces[];
// there is no top-level one, which is the defect this fixture used to encode.
const goodCard = `{"name":"pa-reviewer","description":"d","version":"0.1.0",` +
	`"supportedInterfaces":[{"url":"http://x/","protocolBinding":"JSONRPC","protocolVersion":"1.0"}],` +
	`"capabilities":{"streaming":false},"defaultInputModes":["text/plain"],` +
	`"defaultOutputModes":["text/plain"],"skills":[{"id":"echo"}]}`

// The pre-1.0 shape, kept as a fixture on purpose. assayd parsed exactly this and
// called it v1.0; a conformant card has no top-level protocolVersion, so the
// operator would have refused every real agent while accepting this one.
const v0Card = `{"name":"pa-reviewer","version":"0.1.0","protocolVersion":"1.0",` +
	`"url":"http://x/","skills":[{"id":"echo"}]}`

func TestAValidCardIsAccepted(t *testing.T) {
	r, a := cardFixture(t, goodCard, 200)
	got, err := r.fetchAndValidateCard(context.Background(), a, "assayd-run-team", "abc123")
	if err != nil {
		t.Fatalf("a valid card was rejected: %v", err)
	}
	if got.Name != "pa-reviewer" || got.Version != "0.1.0" {
		t.Errorf("card fields not carried through: %+v", got)
	}
	if len(got.Digest) != 64 {
		t.Errorf("digest %q is not a full SHA-256; drift detection compares it", got.Digest)
	}
	if got.Signed {
		t.Error("Signed is true and nothing in this repository verifies a signature")
	}
	if got.FetchedAt == nil {
		t.Error("FetchedAt is nil")
	}
}

// Each rejection must name WHICH rule refused. "It was refused" is not evidence
// about which one, and this project has shipped vacuous tests on exactly that.
func TestEachCardDefectIsRefusedByItsOwnRule(t *testing.T) {
	for _, tc := range []struct {
		name, body, wantReason string
		status                 int
	}{
		{"unparseable", `{not json`, "CardUnparseable", 200},
		{"name mismatch", `{"name":"someone-else","supportedInterfaces":[{"protocolVersion":"1.0"}]}`,
			"CardNameMismatch", 200},
		{"unsupported protocol", `{"name":"pa-reviewer","supportedInterfaces":[{"protocolVersion":"9.9"}]}`,
			"CardProtocolUnsupported", 200},
		// The regression guard for A71. A v0.x card decodes with no interfaces.
		{"a pre-1.0 card", v0Card, "CardNoInterfaces", 200},
		{"not served", `nope`, "CardUnreachable", 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, a := cardFixture(t, tc.body, tc.status)
			_, err := r.fetchAndValidateCard(context.Background(), a, "assayd-run-team", "abc123")
			if err == nil {
				t.Fatal("accepted")
			}
			ce, ok := err.(*cardError)
			if !ok {
				t.Fatalf("not a cardError: %v", err)
			}
			if ce.reason != tc.wantReason {
				t.Errorf("refused as %q, want %q — the condition reason is what an operator "+
					"acts on, so the wrong one sends them to the wrong problem", ce.reason, tc.wantReason)
			}
		})
	}
}

// The digest is over the bytes SERVED, not over a re-marshalled struct: §3.4
// detects drift by comparing it, and normalising would hide a change the agent
// actually made.
func TestTheDigestIsOverTheServedBytes(t *testing.T) {
	spaced := `{"name":"pa-reviewer",  "description":"d","version":"0.1.0",` +
		`"supportedInterfaces":[{"url":"http://x/","protocolBinding":"JSONRPC","protocolVersion":"1.0"}],` +
		`"capabilities":{"streaming":false},"defaultInputModes":["text/plain"],` +
		`"defaultOutputModes":["text/plain"],"skills":[{"id":"echo"}]}`
	r1, a1 := cardFixture(t, goodCard, 200)
	r2, a2 := cardFixture(t, spaced, 200)
	c1, err1 := r1.fetchAndValidateCard(context.Background(), a1, "assayd-run-team", "abc123")
	c2, err2 := r2.fetchAndValidateCard(context.Background(), a2, "assayd-run-team", "abc123")
	if err1 != nil || err2 != nil {
		t.Fatalf("setup: %v %v", err1, err2)
	}
	if c1.Digest == c2.Digest {
		t.Error("two different byte sequences produced one digest, so a card edit that " +
			"changes only formatting would not read as drift")
	}
}

func TestUpsertReplacesOneRevisionAndKeepsOthers(t *testing.T) {
	cards := []assaydv1alpha1.CardStatus{{Revision: "r1", RevisionDigest: "d1", Digest: "old"}}
	cards = upsertCard(cards, assaydv1alpha1.CardStatus{Revision: "r2"}, "d2")
	cards = upsertCard(cards, assaydv1alpha1.CardStatus{Revision: "r1", Digest: "new"}, "d1")
	if len(cards) != 2 {
		t.Fatalf("want 2 entries, got %d: %+v", len(cards), cards)
	}
	for _, c := range cards {
		if c.RevisionDigest == "d1" && c.Digest != "new" {
			t.Error("the revision's own entry was not replaced")
		}
		if c.RevisionDigest == "d2" && c.Revision != "r2" {
			t.Error("another revision's entry was disturbed")
		}
	}
}

func TestTheCardPathFromTheSpecIsUsed(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		got = req.URL.Path
		_, _ = w.Write([]byte(goodCard))
	}))
	defer srv.Close()
	r := &AgentReconciler{
		CardClient: redirectTo{srv.URL},
		Client: fake.NewClientBuilder().WithScheme(testScheme()).WithObjects(&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Namespace: "assayd-run-team", Name: "pa-reviewer-abc123"},
			Spec:       corev1.ServiceSpec{ClusterIP: "10.43.0.9"},
		}).Build(),
	}
	a := &assaydv1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{Name: "pa-reviewer", Namespace: "team"},
		Spec: assaydv1alpha1.AgentSpec{
			Runtime: &assaydv1alpha1.AgentRuntime{Port: 8080},
			Card:    assaydv1alpha1.CardSpec{Path: "/custom.json"},
		},
	}
	if _, err := r.fetchAndValidateCard(context.Background(), a, "assayd-run-team", "abc123"); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if got != "/custom.json" {
		t.Errorf("fetched %q; spec.card.path is a real CRD field and must be honoured", got)
	}
	if strings.Contains(got, "well-known") {
		t.Error("fell back to the default despite an explicit path")
	}
}

func testScheme() *runtime.Scheme {
	sc := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(sc)
	_ = assaydv1alpha1.AddToScheme(sc)
	return sc
}

// cardFetchDue decides whether to go to the network at all, and it is the only
// thing standing between this operator and a card fetch on every reconcile of
// every agent. It was pinned indirectly by TestReconcileIsIdempotent until the
// fetch started addressing the Service by ClusterIP, which made the failure
// message constant and the churn disappear — so a mutation that made the guard
// always say yes SURVIVED. Pinned directly now, because a performance property
// no test measures is a property that will regress.
func TestCardFetchDueDecidesWhenToGoToTheNetwork(t *testing.T) {
	now := time.Now()
	rev := "d1"
	withCard := func(age time.Duration) *assaydv1alpha1.AgentStatus {
		at := metav1.NewTime(now.Add(-age))
		return &assaydv1alpha1.AgentStatus{Cards: []assaydv1alpha1.CardStatus{
			{RevisionDigest: rev, FetchedAt: &at}}}
	}
	// A failed ATTEMPT, which is an entry with an empty digest. It is not a
	// condition: keying the gate on Registered.LastTransitionTime meant the gate
	// opened permanently after its first interval, because merge() freezes that
	// timestamp while the status stays False — five reconciles, five dials, each
	// able to block the shared work queue for the fetch timeout.
	withFailure := func(age time.Duration) *assaydv1alpha1.AgentStatus {
		at := metav1.NewTime(now.Add(-age))
		return &assaydv1alpha1.AgentStatus{Cards: []assaydv1alpha1.CardStatus{
			{RevisionDigest: rev, FetchedAt: &at}}}
	}

	for _, tc := range []struct {
		name string
		st   *assaydv1alpha1.AgentStatus
		want bool
		why  string
	}{
		{"never fetched", &assaydv1alpha1.AgentStatus{}, true,
			"a revision with no card must be fetched or it never registers"},
		{"fetched just now", withCard(time.Second), false,
			"refetching a card already held is a network call per reconcile for nothing"},
		{"fetched a while ago", withCard(CardDriftInterval + time.Minute), true,
			"§3.4 detects drift by re-reading on an interval; never re-reading detects none"},
		{"failed just now", withFailure(time.Second), false,
			"an unreachable agent must not be dialled on every reconcile — the work queue is shared"},
		{"failed long ago", withFailure(CardRetryInterval + time.Second), true,
			"a failure must be retried or a card fetched a second too early stays unfetched forever"},
		{"failed long ago, then retried just now", withFailure(time.Second), false,
			"the gate must CLOSE again after each retry. Keyed on a condition timestamp it did " +
				"not: once open it stayed open, and every reconcile dialled"},
		{"another revision's card", &assaydv1alpha1.AgentStatus{Cards: []assaydv1alpha1.CardStatus{
			{RevisionDigest: "other", FetchedAt: ptrTime(now)}}}, true,
			"this revision has no card of its own"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := cardFetchDue(tc.st, rev, now); got != tc.want {
				t.Errorf("cardFetchDue = %v, want %v: %s", got, tc.want, tc.why)
			}
		})
	}
}

func ptrTime(t time.Time) *metav1.Time { m := metav1.NewTime(t); return &m }

// supportedInterfaces[] is ORDERED BY THE AGENT'S PREFERENCE and an agent may
// expose several bindings, so a supported version anywhere in the list is
// enough. Checking only the first entry survived as a mutation: no fixture had
// more than one interface, so nothing noticed that an agent preferring a
// binding assayd does not speak would be refused outright.
func TestAnySupportedInterfaceIsEnoughNotOnlyTheFirst(t *testing.T) {
	multi := `{"name":"pa-reviewer","description":"d","version":"0.1.0","supportedInterfaces":[` +
		`{"url":"grpc://x/","protocolBinding":"GRPC","protocolVersion":"0.3"},` +
		`{"url":"http://x/","protocolBinding":"JSONRPC","protocolVersion":"1.0"}],` +
		`"capabilities":{"streaming":false},"defaultInputModes":["text/plain"],` +
		`"defaultOutputModes":["text/plain"],"skills":[{"id":"echo"}]}`
	r, a := cardFixture(t, multi, 200)
	if _, err := r.fetchAndValidateCard(context.Background(), a, "assayd-run-team", "abc123"); err != nil {
		t.Errorf("an agent offering an unsupported binding FIRST and a supported one second "+
			"was refused: %v.\nThe array is the agent's preference order, not a single "+
			"declaration, so refusing on the first entry rejects conformant agents.", err)
	}
}

// And the inverse, so the check is not simply "there is an interface".
func TestACardWhoseInterfacesAreAllUnsupportedIsRefused(t *testing.T) {
	old := `{"name":"pa-reviewer","description":"d","version":"0.1.0","supportedInterfaces":[` +
		`{"url":"grpc://x/","protocolBinding":"GRPC","protocolVersion":"0.3"},` +
		`{"url":"http://x/","protocolBinding":"JSONRPC","protocolVersion":"0.2"}],` +
		`"capabilities":{"streaming":false},"defaultInputModes":["text/plain"],` +
		`"defaultOutputModes":["text/plain"],"skills":[{"id":"echo"}]}`
	r, a := cardFixture(t, old, 200)
	_, err := r.fetchAndValidateCard(context.Background(), a, "assayd-run-team", "abc123")
	ce, ok := err.(*cardError)
	if !ok || ce.reason != "CardProtocolUnsupported" {
		t.Errorf("a card offering only 0.3 and 0.2 was not refused as unsupported: %v", err)
	}
}

// directFixture points the PRODUCTION card client at srv: the revision's
// Service carries srv's loopback address as its ClusterIP and the Agent's port
// is srv's, so nothing is injected and the URL fetchAndValidateCard builds is
// the one it dials. The other fixtures inject redirectTo, which replaces the
// URL the operator built, so they prove nothing about its construction.
func directFixture(t *testing.T, srv *httptest.Server) (*AgentReconciler, *assaydv1alpha1.Agent) {
	t.Helper()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	p, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	return directFixtureAt(u.Hostname()), &assaydv1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{Name: "pa-reviewer", Namespace: "team"},
		Spec:       assaydv1alpha1.AgentSpec{Runtime: &assaydv1alpha1.AgentRuntime{Port: int32(p)}},
	}
}

// directFixtureAt is a reconciler with no injected CardClient, whose revision
// Service has the given ClusterIP.
func directFixtureAt(clusterIP string) *AgentReconciler {
	return &AgentReconciler{
		CardFetchTimeout: time.Second,
		Client: fake.NewClientBuilder().WithScheme(testScheme()).WithObjects(&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Namespace: "assayd-run-team", Name: "pa-reviewer-abc123"},
			Spec:       corev1.ServiceSpec{ClusterIP: clusterIP},
		}).Build(),
	}
}

// The operator dials the address it derived from its own Service, and nothing
// else. A candidate that answers the card path with a redirect is choosing a
// second address for the operator to GET — another in-cluster Service, a cloud
// metadata endpoint, anything the operator's Pod can reach — which is
// server-side request forgery from the operator's network position. So the
// redirect is not followed, whatever its code, and it is a registration
// failure that names itself rather than a card from somewhere else.
func TestACardPathThatRedirectsIsNotFollowed(t *testing.T) {
	var hits atomic.Int32
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(goodCard))
	}))
	defer elsewhere.Close()
	for _, code := range []int{http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther,
		http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			hits.Store(0)
			agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, elsewhere.URL+"/.well-known/agent-card.json", code)
			}))
			defer agent.Close()
			r, a := directFixture(t, agent)
			card, err := r.fetchAndValidateCard(context.Background(), a, "assayd-run-team", "abc123")
			if n := hits.Load(); n != 0 {
				t.Errorf("the operator followed the agent's %d to an address the agent chose "+
					"(%d request(s) reached it)", code, n)
			}
			if card != nil {
				t.Errorf("a card served from the redirect's target was accepted as this revision's: %+v", card)
			}
			ce, ok := err.(*cardError)
			if !ok {
				t.Fatalf("a %d was not refused as a card error: %v", code, err)
			}
			if ce.reason != "CardRedirected" {
				t.Errorf("refused as %q, want CardRedirected: a redirect is an answer, not an "+
					"unreachable agent, and the owner has to know which to fix", ce.reason)
			}
			for _, want := range []string{"redirect", "not followed", strconv.Itoa(code)} {
				if !strings.Contains(ce.msg, want) {
					t.Errorf("the message does not say %q: %s", want, ce.msg)
				}
			}
			// Location is the agent's choice of address, and status is no place
			// to echo it.
			if strings.Contains(ce.msg, strings.TrimPrefix(elsewhere.URL, "http://")) {
				t.Errorf("the message quotes the redirect's target: %s", ce.msg)
			}
		})
	}
}

// The card fetch, as it is sent, never goes through a proxy from the
// operator's environment: a proxy answers in the revision's place, and the card
// it returns would be registered as the one the container serves.
//
// Run in a child process for the reason TestTheProbeNeverTakesAProxyFromTheEnvironment
// gives: net/http reads HTTP_PROXY once per process and never proxies a
// loopback host. The child fetches from a ClusterIP in TEST-NET-1 (RFC 5737),
// which is not loopback and routes nowhere, with HTTP_PROXY set to a proxy
// that serves a valid card: sent directly the fetch fails, through the proxy
// it registers. A control request through the default client, in the same
// child, must get the proxy's 200, or the test would prove nothing.
func TestTheCardFetchNeverTakesAProxyFromTheEnvironment(t *testing.T) {
	const clusterIP = "192.0.2.1"
	if os.Getenv("ASSAYD_CARD_PROXY_CHILD") == "1" {
		a := &assaydv1alpha1.Agent{
			ObjectMeta: metav1.ObjectMeta{Name: "pa-reviewer", Namespace: "team"},
			Spec:       assaydv1alpha1.AgentSpec{Runtime: &assaydv1alpha1.AgentRuntime{Port: 8080}},
		}
		card, err := directFixtureAt(clusterIP).fetchAndValidateCard(context.Background(), a,
			"assayd-run-team", "abc123")
		fmt.Printf("FETCH registered=%v\n", card != nil && err == nil)
		if ce, ok := err.(*cardError); ok {
			fmt.Printf("FETCH reason=%s msg=%s\n", ce.reason, ce.msg)
		}
		control := 0
		if resp, err := http.Get("http://" + clusterIP + ":8080/.well-known/agent-card.json"); err == nil {
			control = resp.StatusCode
			_ = resp.Body.Close()
		}
		fmt.Printf("CONTROL code=%d\n", control)
		return
	}
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(goodCard))
	}))
	defer proxy.Close()
	cmd := exec.Command(os.Args[0], "-test.run=^TestTheCardFetchNeverTakesAProxyFromTheEnvironment$", "-test.count=1")
	cmd.Env = append(os.Environ(), "ASSAYD_CARD_PROXY_CHILD=1",
		"HTTP_PROXY="+proxy.URL, "http_proxy="+proxy.URL, "NO_PROXY=", "no_proxy=")
	out, _ := cmd.CombinedOutput()
	if !strings.Contains(string(out), "CONTROL code=200") {
		t.Fatalf("the control request did not go through the environment's proxy, so this test "+
			"cannot show the card fetch avoids it:\n%s", out)
	}
	if !strings.Contains(string(out), "FETCH registered=false") {
		t.Errorf("the card fetch went through HTTP_PROXY and registered the proxy's card as the "+
			"revision's:\n%s", out)
	}
	// And it failed for the one reason a direct request can: dialling the
	// derived address. "Not registered" alone passed with the proxy restored,
	// once the child's fetch broke for anything else first — a Service lookup
	// in the wrong namespace, say.
	want := "FETCH reason=CardUnreachable msg=could not fetch the agent card from http://" + clusterIP + ":8080/"
	if !strings.Contains(string(out), want) {
		t.Errorf("the card fetch did not fail at the dial to %s, so this run shows nothing about "+
			"the proxy:\n%s", clusterIP, out)
	}
}

// A 3xx that is not one of the five codes net/http follows names nowhere to
// go, and the message must not call it a redirect.
func TestA3xxThatIsNotARedirectIsNotCalledOne(t *testing.T) {
	for _, code := range []int{http.StatusMultipleChoices, http.StatusNotModified} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(code)
			}))
			defer srv.Close()
			r, a := directFixture(t, srv)
			_, err := r.fetchAndValidateCard(context.Background(), a, "assayd-run-team", "abc123")
			ce, ok := err.(*cardError)
			if !ok || ce.reason != "CardUnreachable" || strings.Contains(ce.msg, "redirect") {
				t.Errorf("a %d was reported as %v; it is a plain failure, not a redirect", code, err)
			}
		})
	}
}

// hostNamingPaths are the two shapes of spec.card.path that read as an
// authority, and the path each must arrive as at the address the OPERATOR
// derived. The guard is `cardURL`, not the CRD schema: it writes the derived
// authority first and url.URL keeps a path a path, so neither shape moves the
// dial host. Design 02 A76 made the schema refuse `@host`, and the schema
// admits `//host` — so this list must carry `//host`, or it would be guarding
// only a shape no cluster can produce.
func hostNamingPaths(elsewhereURL string) []struct{ name, path, wantPath string } {
	host := strings.TrimPrefix(elsewhereURL, "http://")
	return []struct{ name, path, wantPath string }{
		// The reported hole: appended to the derived host as a string,
		// `@elsewhere/...` turned the ClusterIP and port into userinfo and
		// named the host itself. Refused at admission since A76; kept here
		// because the code, not the schema, is what makes it harmless.
		{"userinfo", "@" + host + "/.well-known/agent-card.json", "/@" + host + "/.well-known/agent-card.json"},
		// A network-path reference. The schema ADMITS this one, so the code is
		// the only thing standing between it and a fetch at another host.
		{"network-path reference", "//" + host + "/.well-known/agent-card.json", "//" + host + "/.well-known/agent-card.json"},
	}
}

// spec.card.path is the Agent author's, and it is a PATH.
func TestTheCardPathCannotNameTheHost(t *testing.T) {
	var hits atomic.Int32
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(goodCard))
	}))
	defer elsewhere.Close()

	for _, tc := range hostNamingPaths(elsewhere.URL) {
		t.Run(tc.name, func(t *testing.T) {
			before := hits.Load()
			var gotPath atomic.Value
			agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath.Store(r.URL.Path)
				w.WriteHeader(http.StatusNotFound)
			}))
			defer agent.Close()
			r, a := directFixture(t, agent)
			a.Spec.Card.Path = tc.path

			card, err := r.fetchAndValidateCard(context.Background(), a, "assayd-run-team", "abc123")
			if n := hits.Load() - before; n != 0 {
				t.Errorf("a card path of %q sent the operator to %s (%d request(s))", tc.path, elsewhere.URL, n)
			}
			if card != nil || err == nil {
				t.Errorf("a card from an address the path named was registered: %+v", card)
			}
			if got := gotPath.Load(); got != tc.wantPath {
				t.Errorf("the revision's Service was asked for %v, want the path itself, %q", got, tc.wantPath)
			}
		})
	}
}

// The same shapes in the auth probe, where they are worse: the host a card path
// names answers the anonymous request whose 401 publishes a route, so an
// author could forge the evidence that their route is authenticated.
func TestTheProbesCardPathCannotNameTheHost(t *testing.T) {
	var hits atomic.Int32
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer elsewhere.Close()

	for _, tc := range hostNamingPaths(elsewhere.URL) {
		t.Run(tc.name, func(t *testing.T) {
			before := hits.Load()
			var gotPath atomic.Value
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath.Store(r.URL.Path)
				w.WriteHeader(http.StatusOK)
			}))
			defer gateway.Close()
			r := &AgentReconciler{Gateway: GatewayConfig{ServingURL: gateway.URL, HostnameSuffix: "assayd.internal"}}
			a := &assaydv1alpha1.Agent{
				ObjectMeta: metav1.ObjectMeta{Name: "pricer", Namespace: "payments"},
				Spec:       assaydv1alpha1.AgentSpec{Card: assaydv1alpha1.CardSpec{Path: tc.path}},
			}

			ans, err := r.probeAgent(context.Background(), a)
			if n := hits.Load() - before; n != 0 {
				t.Errorf("a card path of %q sent the probe to %s, whose 401 would publish the route "+
					"(%d request(s))", tc.path, elsewhere.URL, n)
			}
			if err != nil || ans.Code != http.StatusOK {
				t.Errorf("the probe did not get the serving listener's answer: %+v, %v", ans, err)
			}
			if got := gotPath.Load(); got != tc.wantPath {
				t.Errorf("the serving listener was asked for %v, want the path itself, %q", got, tc.wantPath)
			}
		})
	}
}

// probeURL's one failure. ValidateGatewayServingURL runs first in production,
// so this is reached only by a reconciler built around NewAgentReconciler.
func TestProbeURLRefusesAServingURLItCannotParse(t *testing.T) {
	if got, err := probeURL("http://[::1", "/.well-known/agent-card.json"); err == nil {
		t.Errorf("an unparseable --gateway-serving-url produced %q", got)
	}
}
