package envtest

import (
	"context"
	"testing"

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
	if len(got.Status.Cards) != 0 {
		t.Errorf("a card was recorded despite the fetch failing: %+v", got.Status.Cards)
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
