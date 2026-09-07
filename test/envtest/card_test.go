package envtest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"k8s.io/apimachinery/pkg/api/meta"

	plumev1alpha1 "github.com/Quinyte/plume/api/v1alpha1"
	"github.com/Quinyte/plume/internal/controller"
	"github.com/Quinyte/plume/internal/revision"
)

// The WIRING, pinned where it is cheap to check. §3.4 fetches the card once the
// revision is available; envtest has no kubelet and no cluster network, so the
// fetch cannot succeed — and that is the point. A reachable failure is still
// evidence the fetch was ATTEMPTED, which is what distinguishes "wired" from
// "never called". The successful path is proven in e2e against a real
// responder; this is the guard that the call site survives.
func TestTheOperatorAttemptsACardFetchOnceTheRevisionIsAvailable(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "cardwire", nil)
	r := newReconciler(false)
	rev := revision.MustHash(a.Spec)
	settle(t, r, a)

	// Before availability there is nothing to fetch FROM, so nothing should be
	// claimed about registration.
	got := settle(t, r, a)
	if c := meta.FindStatusCondition(got.Status.Conditions, string(plumev1alpha1.CondRegistered)); c != nil {
		t.Errorf("Registered was set before the revision was available: %+v — there is no "+
			"container serving a card yet, so any verdict here is invented", c)
	}

	markAvailable(t, ns, controller.WorkloadName("cardwire", rev), 1)
	got = settle(t, r, a)

	c := meta.FindStatusCondition(got.Status.Conditions, string(plumev1alpha1.CondRegistered))
	if c == nil {
		t.Fatalf("no Registered condition after the revision became available: the card fetch "+
			"was never attempted; conditions %+v", got.Status.Conditions)
	}
	// It must FAIL here, and name why. envtest has no cluster DNS, so a success
	// would mean the operator registered a card it could not have read.
	if c.Status != "False" || c.Reason != "CardUnreachable" {
		t.Errorf("Registered is %s/%s; envtest has no cluster network, so the only honest "+
			"outcome is CardUnreachable", c.Status, c.Reason)
	}
	// A failed attempt DOES leave an entry — with an empty Digest, which is the
	// attempt record the retry gate measures from. What must not appear is a
	// registered card: an entry with a digest would mean the operator recorded a
	// card it could not read.
	for _, c := range got.Status.Cards {
		if c.Digest != "" {
			t.Errorf("a card was recorded despite the fetch failing: %+v", c)
		}
	}
	if len(got.Status.Cards) != 1 || got.Status.Cards[0].Revision != rev {
		t.Errorf("no attempt record for revision %s, so the retry gate has nothing to "+
			"measure from and reopens on every reconcile: %+v", rev, got.Status.Cards)
	}
}

// A failed registration must not withhold traffic. §3.4's remedy for an
// unregistrable card is the 30-minute deadline, which §5 records as uncounted —
// so failing the agent here would invent an enforcement the design does not
// describe, and would take every agent down the moment card fetch shipped.
func TestAFailedCardFetchDoesNotWithholdTraffic(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "cardserve", nil)
	r := newReconciler(false)
	rev := revision.MustHash(a.Spec)
	settle(t, r, a)
	markAvailable(t, ns, controller.WorkloadName("cardserve", rev), 1)
	got := settle(t, r, a)

	if got.Status.ActiveRevision != rev {
		t.Errorf("the revision did not become active despite being available; a card fetch "+
			"failure blocked promotion. active=%q phase=%q", got.Status.ActiveRevision, got.Status.Phase)
	}
	if !meta.IsStatusConditionTrue(got.Status.Conditions, string(plumev1alpha1.CondReady)) {
		t.Errorf("Ready is not True after an unreachable card; conditions %+v", got.Status.Conditions)
	}
}

// The requeue, pinned. Setting it only on a FAILED attempt was a real bug: the
// status write a failure performs triggers an immediate reconcile, that one
// skips because the retry interval has not elapsed and returns RequeueAfter=0,
// and the queue's pending delayed add is consumed by the early run. Nothing
// ever came back, so a card fetched a second too early stayed unfetched
// forever — which is exactly what the e2e saw. The rule is therefore about
// STATE, not about what just happened: while a ready revision has no card,
// something must bring us back.
func TestAnUnregisteredReadyRevisionKeepsBeingRequeued(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "requeued", nil)
	r := newReconciler(false)
	rev := revision.MustHash(a.Spec)
	settle(t, r, a)
	markAvailable(t, ns, controller.WorkloadName("requeued", rev), 1)
	settle(t, r, a)

	// envtest has no cluster network, so this revision can never register — which
	// makes it the exact state the requeue exists for. Reconcile twice: the second
	// pass is the one that used to return zero and strand the agent.
	for i := 0; i < 2; i++ {
		res, err := r.Reconcile(context.Background(),
			ctrl.Request{NamespacedName: client.ObjectKeyFromObject(a)})
		if err != nil {
			t.Fatalf("reconcile %d: %v", i, err)
		}
		if res.RequeueAfter == 0 {
			t.Fatalf("reconcile %d returned no requeue for a ready revision with no card. "+
				"Nothing else will bring the operator back, so the card is never fetched "+
				"again and the agent stays unregistered forever.", i)
		}
	}
}

var _ = context.Background

// The SUCCESS path, in envtest. It was reachable all along and was covered only
// by a three-minute k3d run, which is why two defects lived here: the drift
// re-read was never scheduled, and a drifted card replaced its digest in
// silence. One case shows both (reviews/02-step2-fable-review.md MINOR 6).
func TestARegisteredCardIsRereadAndDriftIsSignalled(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "drifty", nil)

	v1card := func(version, extraSkill string) string {
		return `{"name":"drifty","description":"d","version":"` + version + `",` +
			`"supportedInterfaces":[{"url":"http://x/","protocolBinding":"HTTP+JSON","protocolVersion":"1.0"}],` +
			`"capabilities":{"streaming":false},"defaultInputModes":["text/plain"],` +
			`"defaultOutputModes":["text/plain"],"skills":[{"id":"echo"}` + extraSkill + `]}`
	}
	served := v1card("1", "")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(served))
	}))
	defer srv.Close()

	r := newReconciler(false)
	r.CardClient = redirect{srv.URL}
	rev := revision.MustHash(a.Spec)
	settle(t, r, a)
	markAvailable(t, ns, controller.WorkloadName("drifty", rev), 1)
	got := settle(t, r, a)

	c := meta.FindStatusCondition(got.Status.Conditions, string(plumev1alpha1.CondRegistered))
	if c == nil || c.Status != metav1.ConditionTrue || c.Reason != "CardValidated" {
		t.Fatalf("the card was not registered: %+v", c)
	}
	if len(got.Status.Cards) != 1 || got.Status.Cards[0].Digest == "" {
		t.Fatalf("no card recorded: %+v", got.Status.Cards)
	}
	first := got.Status.Cards[0].Digest
	// SharedTaskState is deliberately not asserted here: A2A v1.0 has no such
	// capability, so a fixture claiming it would be inventing protocol (A71).
	if got.Status.Cards[0].SharedTaskState {
		t.Error("status recorded a sharedTaskState assertion, which no A2A card can make")
	}

	// The re-read must be SCHEDULED. Returning zero here is what made
	// CardDriftInterval a bound nothing enforced: no SyncPeriod is set, so a
	// converged agent would not be re-read for the manager's ~10h default.
	res, err := r.Reconcile(context.Background(),
		ctrl.Request{NamespacedName: client.ObjectKeyFromObject(a)})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if res.RequeueAfter != controller.CardDriftInterval {
		t.Errorf("a registered revision requeues after %v, want the drift interval %v — "+
			"otherwise the card is never re-read and drift is undetectable in practice",
			res.RequeueAfter, controller.CardDriftInterval)
	}

	// Now drift: same revision, different bytes.
	served = v1card("2", `,{"id":"delete-everything"}`)
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(a), a); err != nil {
		t.Fatalf("get: %v", err)
	}
	// Age the fetch past the drift interval so the re-read is due.
	live := a.DeepCopy()
	old := metav1.NewTime(time.Now().Add(-controller.CardDriftInterval - time.Minute))
	live.Status.Cards[0].FetchedAt = &old
	if err := k8s.Status().Update(context.Background(), live); err != nil {
		t.Fatalf("age the card: %v", err)
	}
	got = settle(t, r, a)

	c = meta.FindStatusCondition(got.Status.Conditions, string(plumev1alpha1.CondRegistered))
	if c == nil || c.Reason != "CardDrifted" {
		t.Errorf("the agent replaced its card — a new skill and a new version — and the only "+
			"trace was the digest changing in place. Registered reason is %q, want CardDrifted",
			cond(c))
	}
	if got.Status.Cards[0].Digest == first {
		t.Error("the digest did not change, so the drift was not observed at all")
	}
}

func cond(c *metav1.Condition) string {
	if c == nil {
		return "<absent>"
	}
	return c.Reason
}

// redirect sends the operator's request to the test server whatever in-cluster
// address it built. The address construction is exercised by the e2e.
type redirect struct{ base string }

func (d redirect) Do(req *http.Request) (*http.Response, error) {
	out, err := http.NewRequestWithContext(req.Context(), req.Method, d.base+req.URL.Path, nil)
	if err != nil {
		return nil, err
	}
	return http.DefaultClient.Do(out)
}
