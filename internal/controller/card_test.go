package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	plumev1alpha1 "github.com/Quinyte/plume/api/v1alpha1"
)

// The validation logic, tested where it can be reached. The fetch itself needs
// a cluster (the URL names a Service), so these drive it through an injected
// client against a real HTTP server rather than asserting about a mock.
func cardFixture(t *testing.T, body string, status int) (*AgentReconciler, *plumev1alpha1.Agent) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	a := &plumev1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{Name: "pa-reviewer", Namespace: "team"},
		Spec:       plumev1alpha1.AgentSpec{Runtime: &plumev1alpha1.AgentRuntime{Port: 8080}},
	}
	// The fetch reads the revision's Service to get its ClusterIP, so the fake
	// client must hold one. redirectTo then sends the request to the test server
	// whatever address was built — the address CONSTRUCTION is exercised by the
	// e2e, where a wrong one simply fails to connect.
	r := &AgentReconciler{
		CardClient: redirectTo{srv.URL},
		Client: fake.NewClientBuilder().WithScheme(testScheme()).WithObjects(&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Namespace: "plume-run-team", Name: "pa-reviewer-abc123"},
			Spec:       corev1.ServiceSpec{ClusterIP: "10.43.0.9"},
		}).Build(),
	}
	return r, a
}

// redirectTo sends every request to the test server, whatever in-cluster URL
// the operator built. The URL construction is exercised separately by the e2e,
// where a wrong one simply fails to connect.
type redirectTo struct{ base string }

func (d redirectTo) Do(req *http.Request) (*http.Response, error) {
	u := d.base + req.URL.Path
	out, err := http.NewRequestWithContext(req.Context(), req.Method, u, nil)
	if err != nil {
		return nil, err
	}
	return http.DefaultClient.Do(out)
}

const goodCard = `{"name":"pa-reviewer","version":"0.1.0","protocolVersion":"1.0","skills":[{"id":"echo"}]}`

func TestAValidCardIsAccepted(t *testing.T) {
	r, a := cardFixture(t, goodCard, 200)
	got, err := r.fetchAndValidateCard(context.Background(), a, "plume-run-team", "abc123")
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
		{"name mismatch", `{"name":"someone-else","protocolVersion":"1.0"}`, "CardNameMismatch", 200},
		{"unsupported protocol", `{"name":"pa-reviewer","protocolVersion":"9.9"}`, "CardProtocolUnsupported", 200},
		{"not served", `nope`, "CardUnreachable", 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, a := cardFixture(t, tc.body, tc.status)
			_, err := r.fetchAndValidateCard(context.Background(), a, "plume-run-team", "abc123")
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
	spaced := `{"name":"pa-reviewer",  "version":"0.1.0","protocolVersion":"1.0","skills":[{"id":"echo"}]}`
	r1, a1 := cardFixture(t, goodCard, 200)
	r2, a2 := cardFixture(t, spaced, 200)
	c1, err1 := r1.fetchAndValidateCard(context.Background(), a1, "plume-run-team", "abc123")
	c2, err2 := r2.fetchAndValidateCard(context.Background(), a2, "plume-run-team", "abc123")
	if err1 != nil || err2 != nil {
		t.Fatalf("setup: %v %v", err1, err2)
	}
	if c1.Digest == c2.Digest {
		t.Error("two different byte sequences produced one digest, so a card edit that " +
			"changes only formatting would not read as drift")
	}
}

func TestUpsertReplacesOneRevisionAndKeepsOthers(t *testing.T) {
	cards := []plumev1alpha1.CardStatus{{Revision: "r1", RevisionDigest: "d1", Digest: "old"}}
	cards = upsertCard(cards, plumev1alpha1.CardStatus{Revision: "r2"}, "d2")
	cards = upsertCard(cards, plumev1alpha1.CardStatus{Revision: "r1", Digest: "new"}, "d1")
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
			ObjectMeta: metav1.ObjectMeta{Namespace: "plume-run-team", Name: "pa-reviewer-abc123"},
			Spec:       corev1.ServiceSpec{ClusterIP: "10.43.0.9"},
		}).Build(),
	}
	a := &plumev1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{Name: "pa-reviewer", Namespace: "team"},
		Spec: plumev1alpha1.AgentSpec{
			Runtime: &plumev1alpha1.AgentRuntime{Port: 8080},
			Card:    plumev1alpha1.CardSpec{Path: "/custom.json"},
		},
	}
	if _, err := r.fetchAndValidateCard(context.Background(), a, "plume-run-team", "abc123"); err != nil {
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
	_ = plumev1alpha1.AddToScheme(sc)
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
	withCard := func(age time.Duration) *plumev1alpha1.AgentStatus {
		at := metav1.NewTime(now.Add(-age))
		return &plumev1alpha1.AgentStatus{Cards: []plumev1alpha1.CardStatus{
			{RevisionDigest: rev, FetchedAt: &at}}}
	}
	withFailure := func(age time.Duration) *plumev1alpha1.AgentStatus {
		return &plumev1alpha1.AgentStatus{Conditions: []metav1.Condition{{
			Type:               string(plumev1alpha1.CondRegistered),
			Status:             metav1.ConditionFalse,
			Reason:             "CardUnreachable",
			LastTransitionTime: metav1.NewTime(now.Add(-age)),
		}}}
	}

	for _, tc := range []struct {
		name string
		st   *plumev1alpha1.AgentStatus
		want bool
		why  string
	}{
		{"never fetched", &plumev1alpha1.AgentStatus{}, true,
			"a revision with no card must be fetched or it never registers"},
		{"fetched just now", withCard(time.Second), false,
			"refetching a card already held is a network call per reconcile for nothing"},
		{"fetched a while ago", withCard(CardDriftInterval + time.Minute), true,
			"§3.4 detects drift by re-reading on an interval; never re-reading detects none"},
		{"failed just now", withFailure(time.Second), false,
			"an unreachable agent must not be dialled on every reconcile — the work queue is shared"},
		{"failed long ago", withFailure(CardRetryInterval + time.Second), true,
			"a failure must be retried or a card fetched a second too early stays unfetched forever"},
		{"another revision's card", &plumev1alpha1.AgentStatus{Cards: []plumev1alpha1.CardStatus{
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
