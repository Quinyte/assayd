// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package envtest

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/controller"
	"github.com/Quinyte/assayd/internal/evalsuite"
	"github.com/Quinyte/assayd/internal/revision"
)

// Design 16 §1.1 build step 5: the Agent reconciler's gate branch, answering
// from the four promotion conditions. No eval controller runs here or exists
// yet, so every verdict below is written into status.eval by the test, as the
// eval controller would write it — which T12 requires anyway, so that nothing
// can start a run of its own and overwrite the record under test.

// mustCreateSuite creates a one-case EvalSuite and returns the suite digest
// the operator computes for it, from the object the API server stored, so
// the CRD's defaults are in it.
func mustCreateSuite(t *testing.T, ns, name string) string {
	t.Helper()
	installGeneratedEvalSuiteCRD(t)
	s := suite(ns, name, evalCase("arithmetic", "What is 2 + 2?", "4", ""))
	if err := k8s.Create(context.Background(), s); err != nil {
		t.Fatalf("create suite %s: %v", name, err)
	}
	var stored assaydv1alpha1.EvalSuite
	if err := k8s.Get(context.Background(), client.ObjectKeyFromObject(s), &stored); err != nil {
		t.Fatalf("read suite %s: %v", name, err)
	}
	d, err := evalsuite.Digest(stored.Spec)
	if err != nil {
		t.Fatalf("digest suite %s: %v", name, err)
	}
	return d
}

// writeStatus applies edit to the Agent's live status, as a second writer of
// the status subresource would.
func writeAgentStatus(t *testing.T, a *assaydv1alpha1.Agent, edit func(*assaydv1alpha1.AgentStatus)) {
	t.Helper()
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		live := liveAgent(t, a)
		edit(&live.Status)
		return k8s.Status().Update(context.Background(), live)
	}); err != nil {
		t.Fatalf("write the Agent's status: %v", err)
	}
}

// putCard records a card digest for a revision, fetched at the given time, as
// the operator's own fetch would.
func putCard(st *assaydv1alpha1.AgentStatus, rev, digest string, at time.Time) {
	t := metav1.NewTime(at)
	for i := range st.Cards {
		if st.Cards[i].RevisionDigest == digest {
			st.Cards[i].Digest, st.Cards[i].FetchedAt, st.Cards[i].Revision = "cafe", &t, rev
			return
		}
	}
	st.Cards = append(st.Cards, assaydv1alpha1.CardStatus{Revision: rev, RevisionDigest: digest,
		Digest: "cafe", FetchedAt: &t})
}

func cardRecorded(st *assaydv1alpha1.AgentStatus, digest string) bool {
	for _, c := range st.Cards {
		if c.RevisionDigest == digest {
			return c.Digest != ""
		}
	}
	return false
}

// servedUngated serves a first revision with no gate declared, on a
// reconciler that sees the EvalSuite CRD, so that a later edit's candidate
// is gated while an earlier revision serves.
func servedUngated(t *testing.T, ns, name string) (*assaydv1alpha1.Agent, *controller.AgentReconciler) {
	t.Helper()
	a := mustCreateAgent(t, ns, name, nil)
	r := newReconciler(true)
	settle(t, r, a)
	markAvailable(t, ns, controller.WorkloadName(name, revision.MustHash(a.Spec)), 1)
	got := settle(t, r, a)
	if got.Status.ActiveRevision == "" {
		t.Fatal("setup: the ungated first revision never promoted")
	}
	return &got, r
}

// gatedCandidate edits a served Agent to a new image and the given suite,
// and brings the candidate up. It returns the candidate's name and digest.
func gatedCandidate(t *testing.T, r *controller.AgentReconciler, a *assaydv1alpha1.Agent,
	image, suiteName string) (string, string) {
	t.Helper()
	mustEdit(t, a, func(a *assaydv1alpha1.Agent) {
		a.Spec.Runtime.Image = image
		a.Spec.Gates = []assaydv1alpha1.GateRef{{EvalSuiteRef: suiteName}}
	})
	spec := liveAgent(t, a).Spec
	rev, digest := revision.MustHash(spec), revision.MustDigest(spec)
	reconcileOnce(t, r, a)
	reconcileOnce(t, r, a)
	markAvailable(t, a.Namespace, controller.WorkloadName(a.Name, rev), 1)
	reconcileOnce(t, r, a)
	if got := liveAgent(t, a).Status.CandidateRevisionDigest; got != digest {
		t.Fatalf("setup: the candidate is %q, want %q", got, digest)
	}
	return rev, digest
}

const imageB = "ghcr.io/acme/agent@sha256:b0b0000000000000000000000000000000000000000000000000000000000000"

func gatesPassed(t *testing.T, a *assaydv1alpha1.Agent) *metav1.Condition {
	t.Helper()
	c := condition(liveAgent(t, a), assaydv1alpha1.CondGatesPassed)
	if c == nil {
		t.Fatal("GatesPassed is not set")
	}
	return c
}

// T12: the collision pair. The desired revision is the malicious member M of
// internal/revision/collision_test.go's pinned pair; status.eval records
// Passed for the safe member S's digest under the shared name. S is neither
// active nor candidate, so the workload collision guard does not refuse
// first. The verdict was earned by S's digest, so M must not promote (A2).
func TestTheCollisionPairsVerdictDoesNotPromoteTheOtherMember(t *testing.T) {
	safe, evil := pinnedCollidingSpecs()
	ns := newNamespace(t)
	sd := mustCreateSuite(t, ns, "smoke")
	a, r := servedUngated(t, ns, "collidegate")
	active := a.Status.ActiveRevisionDigest

	mustEdit(t, a, func(a *assaydv1alpha1.Agent) {
		a.Spec = evil
		a.Spec.Gates = []assaydv1alpha1.GateRef{{EvalSuiteRef: "smoke"}}
	})
	shared := revision.MustHash(evil)
	mDigest, sDigest := revision.MustDigest(liveAgent(t, a).Spec), revision.MustDigest(safe)
	if revision.MustHash(safe) != shared || mDigest == sDigest {
		t.Fatal("setup: the pinned pair no longer collides; see internal/revision/collision_test.go")
	}
	reconcileOnce(t, r, a)
	reconcileOnce(t, r, a)
	markAvailable(t, ns, controller.WorkloadName("collidegate", shared), 1)
	reconcileOnce(t, r, a)
	if got := liveAgent(t, a).Status.CandidateRevisionDigest; got != mDigest {
		t.Fatalf("setup: M is not the candidate (candidate digest %q)", got)
	}

	// Every promotion condition holds but the second: the verdict is Passed,
	// for the current suite, M's card is recorded — and the verdict's digest
	// is S's.
	writeAgentStatus(t, a, func(st *assaydv1alpha1.AgentStatus) {
		st.Eval = &assaydv1alpha1.EvalStatus{Suite: "smoke", Revision: shared, RevisionDigest: sDigest,
			SuiteDigest: sd, Verdict: assaydv1alpha1.EvalVerdictPassed, Score: "1/1"}
		putCard(st, shared, mDigest, time.Now())
	})
	for i := 0; i < 3; i++ {
		reconcileOnce(t, r, a)
	}
	got := liveAgent(t, a)
	if got.Status.ActiveRevisionDigest != active {
		t.Fatalf("M (digest %s) was promoted on the verdict S (digest %s) earned under the shared "+
			"name %s: the gate compared the 40-bit name, not the digest", mDigest, sDigest, shared)
	}
	if c := gatesPassed(t, a); c.Status != metav1.ConditionFalse || c.Reason != "GateControllerUnimplemented" {
		t.Errorf("GatesPassed is %s/%s; want False/GateControllerUnimplemented — no verdict is "+
			"recorded for M's digest, and no eval controller exists to write one", c.Status, c.Reason)
	}

	// The control: the same record, keyed on M's own digest, promotes M. So
	// the hold above was the digest and nothing else.
	writeAgentStatus(t, a, func(st *assaydv1alpha1.AgentStatus) { st.Eval.RevisionDigest = mDigest })
	reconcileOnce(t, r, a)
	if got := liveAgent(t, a).Status.ActiveRevisionDigest; got != mDigest {
		t.Fatalf("control: a Passed verdict for M's own digest did not promote it (active %s)", got)
	}
	if c := gatesPassed(t, a); c.Status != metav1.ConditionTrue || c.Reason != "EvalPassed" {
		t.Errorf("control: GatesPassed is %s/%s, want True/EvalPassed", c.Status, c.Reason)
	}
}

// T14: the card cleared after the last case, as a failed drift re-read
// clears it. Every other promotion condition holds, so the fourth is what
// holds the candidate (Q10).
func TestACandidateWhoseCardIsClearedAfterItPassedDoesNotPromote(t *testing.T) {
	ns := newNamespace(t)
	sd := mustCreateSuite(t, ns, "smoke")
	a, r := servedUngated(t, ns, "cardcleared")
	active := a.Status.ActiveRevisionDigest
	rev, digest := gatedCandidate(t, r, a, imageB, "smoke")

	// The verdict is Passed and the card was recorded, but long enough ago
	// that the drift re-read is due. envtest has no cluster network, so that
	// re-read fails, and a failed re-read clears the digest
	// (recordCardAttempt).
	writeAgentStatus(t, a, func(st *assaydv1alpha1.AgentStatus) {
		st.Eval = &assaydv1alpha1.EvalStatus{Suite: "smoke", Revision: rev, RevisionDigest: digest,
			SuiteDigest: sd, Verdict: assaydv1alpha1.EvalVerdictPassed, Score: "1/1"}
		putCard(st, rev, digest, time.Now().Add(-controller.CardDriftInterval-time.Minute))
	})
	for i := 0; i < 3; i++ {
		reconcileOnce(t, r, a)
	}
	got := liveAgent(t, a)
	if cardRecorded(&got.Status, digest) {
		t.Fatal("setup: the drift re-read did not clear the candidate's card")
	}
	if got.Status.ActiveRevisionDigest != active {
		t.Fatal("a passed candidate whose card is no longer recorded was promoted; Q10's fourth " +
			"condition requires a recorded card at the moment of promotion")
	}
	c := gatesPassed(t, a)
	if c.Status != metav1.ConditionFalse || c.Reason != "EvalAwaitingCard" {
		t.Errorf("GatesPassed is %s/%s, want False/EvalAwaitingCard", c.Status, c.Reason)
	}
	if got.Status.Phase != assaydv1alpha1.PhaseHeld {
		t.Errorf("phase is %q, want Held", got.Status.Phase)
	}

	// The card records again, and the same verdict promotes, under
	// EvalPassed naming both digests and the score.
	writeAgentStatus(t, a, func(st *assaydv1alpha1.AgentStatus) { putCard(st, rev, digest, time.Now()) })
	reconcileOnce(t, r, a)
	if got := liveAgent(t, a).Status.ActiveRevisionDigest; got != digest {
		t.Fatalf("control: the recorded card did not let the passed candidate promote (active %s)", got)
	}
	c = gatesPassed(t, a)
	if c.Status != metav1.ConditionTrue || c.Reason != "EvalPassed" {
		t.Fatalf("GatesPassed is %s/%s, want True/EvalPassed", c.Status, c.Reason)
	}
	for _, want := range []string{digest, sd, "1/1"} {
		if !strings.Contains(c.Message, want) {
			t.Errorf("EvalPassed's message does not name %q: %s", want, c.Message)
		}
	}
}

// suiteUnreadable is an uncached reader that refuses every EvalSuite read
// with an API error that is not NotFound.
type suiteUnreadable struct{ client.Reader }

func (s suiteUnreadable) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if _, ok := obj.(*assaydv1alpha1.EvalSuite); ok {
		return apierrors.NewForbidden(schema.GroupResource{Group: "assayd.dev", Resource: "evalsuites"},
			key.Name, errors.New("injected by the test"))
	}
	return s.Reader.Get(ctx, key, obj, opts...)
}

// T25: EvalSuiteNotFound for a missing suite, and EvalSuiteUnreadable when the
// uncached reader returns an error that is not NotFound.
func TestASuiteThatIsMissingOrUnreadableHoldsTheCandidate(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		ns := newNamespace(t)
		a, r := servedUngated(t, ns, "nosuite")
		gatedCandidate(t, r, a, imageB, "absent")
		reconcileOnce(t, r, a)
		got := liveAgent(t, a)
		if got.Status.ActiveRevisionDigest == got.Status.CandidateRevisionDigest {
			t.Fatal("a candidate whose suite does not exist was promoted")
		}
		c := gatesPassed(t, a)
		if c.Status != metav1.ConditionFalse || c.Reason != "EvalSuiteNotFound" {
			t.Errorf("GatesPassed is %s/%s, want False/EvalSuiteNotFound", c.Status, c.Reason)
		}
		if !strings.Contains(c.Message, `"absent"`) {
			t.Errorf("EvalSuiteNotFound does not name the suite: %s", c.Message)
		}
	})
	t.Run("unreadable", func(t *testing.T) {
		ns := newNamespace(t)
		mustCreateSuite(t, ns, "smoke")
		a, r := servedUngated(t, ns, "badsuite")
		r.Reader = suiteUnreadable{k8s}
		gatedCandidate(t, r, a, imageB, "smoke")
		reconcileOnce(t, r, a)
		c := gatesPassed(t, a)
		if c.Status != metav1.ConditionFalse || c.Reason != "EvalSuiteUnreadable" {
			t.Errorf("GatesPassed is %s/%s, want False/EvalSuiteUnreadable: the suite exists, and "+
				"the read failed with Forbidden, not NotFound", c.Status, c.Reason)
		}
		if !strings.Contains(c.Message, "injected by the test") {
			t.Errorf("EvalSuiteUnreadable does not name the error: %s", c.Message)
		}
		// The control: the same Agent, read with a reader that works, is
		// held for the reason after the suite read, not by the suite.
		r.Reader = nil
		reconcileOnce(t, r, a)
		if c := gatesPassed(t, a); c.Reason != "EvalAwaitingCard" {
			t.Errorf("control: with the suite readable GatesPassed is %s, want EvalAwaitingCard "+
				"(no card is recorded for the candidate)", c.Reason)
		}
	})
}

// T29, with T63's second half. Removing spec.gates while a failed candidate
// is held promotes it (Q9 (a)); GatesSkipped's message names the Failed
// verdict and its digests, and GatesPassed flips to False/NoGatesDeclared
// rather than keeping EvalFailed beside a revision it no longer describes.
func TestRemovingTheGatePromotesAFailedCandidateAndSaysSo(t *testing.T) {
	ns := newNamespace(t)
	sd := mustCreateSuite(t, ns, "smoke")
	a, r := servedUngated(t, ns, "ungate")
	active := a.Status.ActiveRevisionDigest
	rev, digest := gatedCandidate(t, r, a, imageB, "smoke")
	writeAgentStatus(t, a, func(st *assaydv1alpha1.AgentStatus) {
		st.Eval = &assaydv1alpha1.EvalStatus{Suite: "smoke", Revision: rev, RevisionDigest: digest,
			SuiteDigest: sd, Verdict: assaydv1alpha1.EvalVerdictFailed, Score: "0/1",
			Cases:  []assaydv1alpha1.EvalCaseRecord{{ID: "arithmetic", Outcome: "fail", Reason: "Mismatch"}},
			Failed: []assaydv1alpha1.EvalPair{{RevisionDigest: digest, SuiteDigest: sd}}}
		putCard(st, rev, digest, time.Now())
	})
	for i := 0; i < 2; i++ {
		reconcileOnce(t, r, a)
	}
	if got := liveAgent(t, a).Status.ActiveRevisionDigest; got != active {
		t.Fatal("a candidate with a Failed verdict was promoted")
	}
	c := gatesPassed(t, a)
	if c.Status != metav1.ConditionFalse || c.Reason != "EvalFailed" {
		t.Fatalf("setup: GatesPassed is %s/%s, want False/EvalFailed", c.Status, c.Reason)
	}
	for _, want := range []string{"arithmetic", "Mismatch", "0/1", digest, sd} {
		if !strings.Contains(c.Message, want) {
			t.Errorf("EvalFailed's message does not name %q: %s", want, c.Message)
		}
	}

	mustEdit(t, a, func(a *assaydv1alpha1.Agent) { a.Spec.Gates = nil })
	for i := 0; i < 2; i++ {
		reconcileOnce(t, r, a)
	}
	got := liveAgent(t, a)
	if got.Status.ActiveRevisionDigest != digest {
		t.Fatalf("removing spec.gates did not promote the held candidate (active %s); Q9 (a) "+
			"releases it", got.Status.ActiveRevisionDigest)
	}
	skipped := condition(got, assaydv1alpha1.CondGatesSkipped)
	if skipped == nil || skipped.Status != metav1.ConditionTrue || skipped.Reason != "NoGatesDeclared" {
		t.Fatalf("GatesSkipped is %+v, want True/NoGatesDeclared", skipped)
	}
	for _, want := range []string{"Failed", digest, sd} {
		if !strings.Contains(skipped.Message, want) {
			t.Errorf("GatesSkipped's message does not name %q, so nothing says the removal "+
				"overrode a Failed verdict: %s", want, skipped.Message)
		}
	}
	if c := gatesPassed(t, a); c.Status != metav1.ConditionFalse || c.Reason != "NoGatesDeclared" {
		t.Errorf("GatesPassed is %s/%s after the gate was removed; want False/NoGatesDeclared, not "+
			"a sticky verdict about a gate that no longer exists", c.Status, c.Reason)
	} else if !strings.Contains(c.Message, rev) {
		t.Errorf("GatesPassed's removal message does not name the revision now serving: %s", c.Message)
	}
}

// T63's first half. A gate added over a serving revision mints nothing and
// does not hold it, and the revision serving was never evaluated: it reads
// ActiveRevisionNotEvaluated, never EvalPassed.
func TestAGateAddedOverAServingRevisionSaysItWasNotEvaluated(t *testing.T) {
	ns := newNamespace(t)
	mustCreateSuite(t, ns, "smoke")
	a, r := servedUngated(t, ns, "lategate")
	active := a.Status.ActiveRevisionDigest
	mustEdit(t, a, func(a *assaydv1alpha1.Agent) {
		a.Spec.Gates = []assaydv1alpha1.GateRef{{EvalSuiteRef: "smoke"}}
	})
	got := settle(t, r, a)
	if got.Status.ActiveRevisionDigest != active || got.Status.Phase != assaydv1alpha1.PhaseReady {
		t.Fatalf("adding a gate disturbed the serving revision: active %s, phase %s",
			got.Status.ActiveRevisionDigest, got.Status.Phase)
	}
	c := gatesPassed(t, a)
	if c.Status != metav1.ConditionFalse || c.Reason != "ActiveRevisionNotEvaluated" {
		t.Errorf("GatesPassed is %s/%s; want False/ActiveRevisionNotEvaluated — status.eval "+
			"records no Passed verdict for the revision serving", c.Status, c.Reason)
	}
	if !strings.Contains(c.Message, got.Status.ActiveRevision) {
		t.Errorf("ActiveRevisionNotEvaluated does not name the serving revision: %s", c.Message)
	}

	// The next revision is evaluated. While it is still coming up, the
	// not-ready branch that keeps the serving revision describes the
	// candidate by its own gate answer: GatesPassed is sticky, and left
	// alone it would go on describing the revision serving.
	mustEdit(t, a, func(a *assaydv1alpha1.Agent) { a.Spec.Runtime.Image = imageB })
	reconcileOnce(t, r, a)
	reconcileOnce(t, r, a)
	if got := liveAgent(t, a); got.Status.ActiveRevisionDigest != active ||
		got.Status.CandidateRevisionDigest == "" {
		t.Fatalf("setup: want the new revision coming up beside the serving one: %+v", got.Status)
	}
	if c := gatesPassed(t, a); c.Reason != "EvalAwaitingCard" {
		t.Errorf("GatesPassed is %s while the gated candidate comes up; want EvalAwaitingCard, the "+
			"candidate's own answer, not the serving revision's", c.Reason)
	}
}

// A gated candidate with its suite, its card and no verdict holds under
// GateControllerUnimplemented, never EvalRunning: until the eval controller
// lands (build step 6), nothing runs, and no pass may say a run is under way.
func TestAGatedCandidateWithNoVerdictHoldsWithNoRunClaimed(t *testing.T) {
	ns := newNamespace(t)
	sd := mustCreateSuite(t, ns, "smoke")
	a, r := servedUngated(t, ns, "noverdict")
	active := a.Status.ActiveRevisionDigest
	rev, digest := gatedCandidate(t, r, a, imageB, "smoke")
	writeAgentStatus(t, a, func(st *assaydv1alpha1.AgentStatus) { putCard(st, rev, digest, time.Now()) })
	reconcileOnce(t, r, a)
	c := gatesPassed(t, a)
	if c.Status != metav1.ConditionFalse || c.Reason != "GateControllerUnimplemented" {
		t.Errorf("GatesPassed is %s/%s, want False/GateControllerUnimplemented", c.Status, c.Reason)
	}
	// A Running record, which only an eval controller writes, changes nothing
	// either: there is no verdict to promote on.
	writeAgentStatus(t, a, func(st *assaydv1alpha1.AgentStatus) {
		st.Eval = &assaydv1alpha1.EvalStatus{Suite: "smoke", Revision: rev, RevisionDigest: digest,
			SuiteDigest: sd, Verdict: assaydv1alpha1.EvalVerdictRunning}
	})
	reconcileOnce(t, r, a)
	if got := liveAgent(t, a); got.Status.ActiveRevisionDigest != active {
		t.Fatal("a Running record promoted the candidate")
	}
	if c := gatesPassed(t, a); c.Reason != "GateControllerUnimplemented" {
		t.Errorf("GatesPassed is %s with a Running record, want GateControllerUnimplemented", c.Reason)
	}
}

// evalRecordKept asks the API server, by a dry-run status write, whether it
// would store status.eval's suiteDigest and blockedFor on this Agent.
func evalRecordKept(t *testing.T, a *assaydv1alpha1.Agent) bool {
	t.Helper()
	probe := liveAgent(t, a)
	probe.Status.Eval = &assaydv1alpha1.EvalStatus{SuiteDigest: "probe",
		BlockedFor: &assaydv1alpha1.EvalBlockedFor{RevisionDigest: "probe"}}
	if err := k8s.Status().Update(context.Background(), probe, client.DryRunAll); err != nil {
		return false
	}
	return probe.Status.Eval != nil && probe.Status.Eval.SuiteDigest == "probe" &&
		probe.Status.Eval.BlockedFor != nil
}

// installAgentCRDWithoutEvalFields replaces the live Agent CRD with this
// release's minus the named status.eval fields, and puts the release's back
// when the test ends.
func installAgentCRDWithoutEvalFields(t *testing.T, a *assaydv1alpha1.Agent, fields ...string) {
	t.Helper()
	ctx := context.Background()
	var installed apiextensionsv1.CustomResourceDefinition
	if err := k8s.Get(ctx, client.ObjectKey{Name: "agents.assayd.dev"}, &installed); err != nil {
		t.Fatalf("read the Agent CRD: %v", err)
	}
	current := installed.Spec.DeepCopy()
	stale := current.DeepCopy()
	for i := range stale.Versions {
		root := stale.Versions[i].Schema.OpenAPIV3Schema
		st := root.Properties["status"]
		ev := st.Properties["eval"]
		for _, f := range fields {
			delete(ev.Properties, f)
		}
		st.Properties["eval"] = ev
		root.Properties["status"] = st
	}
	write := func(spec *apiextensionsv1.CustomResourceDefinitionSpec) {
		if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			var c apiextensionsv1.CustomResourceDefinition
			if err := k8s.Get(ctx, client.ObjectKey{Name: "agents.assayd.dev"}, &c); err != nil {
				return err
			}
			c.Spec = *spec.DeepCopy()
			return k8s.Update(ctx, &c)
		}); err != nil {
			t.Fatalf("install the Agent CRD: %v", err)
		}
	}
	t.Cleanup(func() {
		write(current)
		eventually(t, "the release's Agent CRD to keep status.eval again", func() bool {
			return evalRecordKept(t, a)
		})
	})
	write(stale)
}

// crdUnreadable refuses every CustomResourceDefinition read.
type crdUnreadable struct{ client.Reader }

func (c crdUnreadable) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if _, ok := obj.(*apiextensionsv1.CustomResourceDefinition); ok {
		return apierrors.NewInternalError(errors.New("injected CRD read failure"))
	}
	return c.Reader.Get(ctx, key, obj, opts...)
}

// T55: the schema check, on its own. The new status.eval fields are fields of
// the Agent CRD, which helm upgrade never updates, and a stale one prunes
// them without an error — so the gate branch names that cause before it
// reads any of status.eval.
func TestAnAgentCRDThatWouldPruneTheEvalRecordIsNamed(t *testing.T) {
	allNew := []string{"suiteDigest", "endpointPath", "cardDigest", "blocked", "blockedFor", "verdict",
		"cases", "failed"}
	t.Run("every new field missing", func(t *testing.T) {
		ns := newNamespace(t)
		mustCreateSuite(t, ns, "smoke")
		a, r := servedUngated(t, ns, "stalecrd")
		installAgentCRDWithoutEvalFields(t, a, allNew...)
		gatedCandidate(t, r, a, imageB, "smoke")
		reconcileOnce(t, r, a)
		c := gatesPassed(t, a)
		if c.Status != metav1.ConditionFalse || c.Reason != "EvalRecordNotKept" {
			t.Fatalf("GatesPassed is %s/%s, want False/EvalRecordNotKept", c.Status, c.Reason)
		}
		for _, want := range []string{"kubectl", "charts/assayd/crds/", "helm upgrade"} {
			if !strings.Contains(c.Message, want) {
				t.Errorf("EvalRecordNotKept does not name the remedy (%q): %s", want, c.Message)
			}
		}
	})
	t.Run("blockedFor alone missing, beside a recorded Lock", func(t *testing.T) {
		ns := newNamespace(t)
		mustCreateSuite(t, ns, "smoke")
		a, r := servedUngated(t, ns, "noblockedfor")
		installAgentCRDWithoutEvalFields(t, a, "blockedFor")
		rev, digest := gatedCandidate(t, r, a, imageB, "smoke")
		deadline := metav1.NewTime(time.Now().Add(time.Minute).Truncate(time.Second))
		writeAgentStatus(t, a, func(st *assaydv1alpha1.AgentStatus) {
			putCard(st, rev, digest, time.Now())
			st.Auth = &assaydv1alpha1.AuthStatus{Mode: "none", Transaction: &assaydv1alpha1.AuthTransaction{
				Kind: "Lock", TargetMode: "apikey", TargetDigest: "abc", Stage: "ProbingAfter",
				Written: true, Deadline: &deadline}}
		})
		reconcileOnce(t, r, a)
		c := gatesPassed(t, a)
		if c.Status != metav1.ConditionFalse || c.Reason != "EvalRecordNotKept" {
			t.Fatalf("GatesPassed is %s/%s, want False/EvalRecordNotKept: without blockedFor every "+
				"read of it is absent, and the exempt state would read EvalWaitingForAuth for ever",
				c.Status, c.Reason)
		}
		if !strings.Contains(c.Message, "blockedFor") {
			t.Errorf("EvalRecordNotKept does not name the missing field: %s", c.Message)
		}
	})
	t.Run("the CRD cannot be read", func(t *testing.T) {
		ns := newNamespace(t)
		mustCreateSuite(t, ns, "smoke")
		a, r := servedUngated(t, ns, "crdunread")
		r.Reader = crdUnreadable{k8s}
		gatedCandidate(t, r, a, imageB, "smoke")
		reconcileOnce(t, r, a)
		c := gatesPassed(t, a)
		if c.Status != metav1.ConditionFalse || c.Reason != "EvalRecordUnverifiable" {
			t.Fatalf("GatesPassed is %s/%s, want False/EvalRecordUnverifiable: a CRD that cannot "+
				"be read is not a CRD that keeps the record", c.Status, c.Reason)
		}
		if !strings.Contains(c.Message, "injected CRD read failure") {
			t.Errorf("EvalRecordUnverifiable does not name the error: %s", c.Message)
		}
	})
}

// EvalHeldForAuth: a gated candidate under design 03's I1 hold is held by
// the I1 branch, which comes before the gate, and says so.
func TestAGatedCandidateUnderTheI1HoldSaysItIsHeldForAuth(t *testing.T) {
	a, r, _ := servedAPIKeyAgent(t, "heldforauth")
	ns := a.Namespace
	r.EvalSuiteInstalled = func() bool { return true }
	mustCreateSuite(t, ns, "smoke")
	first := a.Status.ActiveRevision
	mustEdit(t, a, func(a *assaydv1alpha1.Agent) {
		a.Spec.Expose = &assaydv1alpha1.ExposeSpec{A2A: &assaydv1alpha1.ExposeProtocol{Auth: "oauth"}}
		a.Spec.Gates = []assaydv1alpha1.GateRef{{EvalSuiteRef: "smoke"}}
	})
	second := revision.MustHash(liveAgent(t, a).Spec)
	reconcileOnce(t, r, a)
	markAvailable(t, ns, controller.WorkloadName("heldforauth", second), 1)
	reconcileOnce(t, r, a)
	reconcileOnce(t, r, a)
	if got := liveAgent(t, a).Status.ActiveRevision; got != first {
		t.Fatalf("the uncompilable edit's revision was promoted (%s)", got)
	}
	condIs(t, a, assaydv1alpha1.CondGatesPassed, metav1.ConditionFalse, "EvalHeldForAuth")
	condIs(t, a, assaydv1alpha1.CondProgressing, metav1.ConditionTrue, "AuthInputUncompilable")
}

// EvalWaitingForAuth: a route re-create's Create recorded on a served Agent
// holds its gated candidate's eval, and the message names the transaction's
// kind and stage.
func TestAGatedCandidateWaitsForARecordedCreate(t *testing.T) {
	a, r, _ := servedAPIKeyAgent(t, "waitsforauth")
	ns := a.Namespace
	r.EvalSuiteInstalled = func() bool { return true }
	mustCreateSuite(t, ns, "smoke")
	rev, digest := gatedCandidate(t, r, a, imageB, "smoke")
	writeAgentStatus(t, a, func(st *assaydv1alpha1.AgentStatus) { putCard(st, rev, digest, time.Now()) })
	deleteRoute(t, a)
	reconcileOnce(t, r, a)
	tx := txOf(t, a)
	if tx == nil || tx.Kind != "Create" {
		t.Fatalf("setup: deleting the route recorded no Create: %+v", tx)
	}
	reconcileOnce(t, r, a)
	tx = txOf(t, a)
	c := condIs(t, a, assaydv1alpha1.CondGatesPassed, metav1.ConditionFalse, "EvalWaitingForAuth")
	if c != nil && (!strings.Contains(c.Message, "Create") || !strings.Contains(c.Message, tx.Stage)) {
		t.Errorf("EvalWaitingForAuth does not name the transaction's kind and stage (%s): %s",
			tx.Stage, c.Message)
	}
}
