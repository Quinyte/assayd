// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package envtest

import (
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
	"github.com/Quinyte/assayd/internal/controller"
	"github.com/Quinyte/assayd/internal/revision"
)

// Design 16 A27.1 (#137): a removed gate's note ends by saying whether the
// revision it names serves, from status and the spec's pin, and reads
// nothing to say it.

const (
	noteActive  = "and it is the active revision (design 16 Q9)"
	notePinned  = "but it takes no traffic while spec.release.targetRevisionDigest names another revision"
	notePending = "takes traffic once it is available"
	imageR1     = "ghcr.io/acme/agent@sha256:a1a1000000000000000000000000000000000000000000000000000000000000"
)

// removedGateMessages returns GatesSkipped's and GatesPassed's messages,
// failing unless both read NoGatesDeclared.
func removedGateMessages(t *testing.T, a *assaydv1alpha1.Agent) (string, string) {
	t.Helper()
	got := liveAgent(t, a)
	skipped := condition(got, assaydv1alpha1.CondGatesSkipped)
	if skipped == nil || skipped.Status != metav1.ConditionTrue || skipped.Reason != "NoGatesDeclared" {
		t.Fatalf("GatesSkipped is %+v, want True/NoGatesDeclared", skipped)
	}
	passed := gatesPassed(t, a)
	if passed.Status != metav1.ConditionFalse || passed.Reason != "NoGatesDeclared" {
		t.Fatalf("GatesPassed is %s/%s, want False/NoGatesDeclared", passed.Status, passed.Reason)
	}
	return skipped.Message, passed.Message
}

// unpassedCandidateHeld writes a Failed or Running verdict for the gated
// candidate the Agent converges and runs the passes that hold it.
func unpassedCandidateHeld(t *testing.T, r *controller.AgentReconciler, a *assaydv1alpha1.Agent,
	rev, digest, sd string, verdict assaydv1alpha1.EvalVerdict) {
	t.Helper()
	writeAgentStatus(t, a, func(st *assaydv1alpha1.AgentStatus) {
		st.Eval = &assaydv1alpha1.EvalStatus{Suite: "smoke", Revision: rev, RevisionDigest: digest,
			SuiteDigest: sd, Verdict: verdict, Score: "0/1"}
		if verdict == assaydv1alpha1.EvalVerdictFailed {
			st.Eval.Failed = []assaydv1alpha1.EvalPair{{RevisionDigest: digest, SuiteDigest: sd}}
		}
		putCard(st, rev, digest, time.Now())
	})
	reconcileOnce(t, r, a)
	reconcileOnce(t, r, a)
	if got := liveAgent(t, a).Status.ActiveRevisionDigest; got == digest {
		t.Fatalf("setup: the candidate with a %s verdict was promoted", verdict)
	}
	if c := gatesPassed(t, a); c.Status != metav1.ConditionFalse {
		t.Fatalf("setup: GatesPassed is %s/%s, want False", c.Status, c.Reason)
	}
}

// R1. An ungated Agent whose status.eval records Failed for the spec's
// revision D, pinned to a retained revision A: the note says D takes no
// traffic while the pin names another revision, never that it takes traffic
// once it is available. On a steady pass with A serving, GatesPassed is built
// for A, which status.eval does not record, and carries no note.
func TestARemovedGateWithAPinSaysTheRevisionTakesNoTraffic(t *testing.T) {
	ns := newNamespace(t)
	sd := mustCreateSuite(t, ns, "smoke")
	a, r := servedUngated(t, ns, "notepin")
	revA, dA := a.Status.ActiveRevision, a.Status.ActiveRevisionDigest
	// A second ungated revision B takes over, so A is retained, not active.
	mustEdit(t, a, func(a *assaydv1alpha1.Agent) { a.Spec.Runtime.Image = imageR1 })
	revB := revision.MustHash(liveAgent(t, a).Spec)
	settle(t, r, a)
	markAvailable(t, ns, controller.WorkloadName("notepin", revB), 1)
	if got := settle(t, r, a); got.Status.ActiveRevision != revB {
		t.Fatalf("setup: revision B did not promote; active %s", got.Status.ActiveRevision)
	}
	revD, dD := gatedCandidate(t, r, a, imageB, "smoke")
	unpassedCandidateHeld(t, r, a, revD, dD, sd, assaydv1alpha1.EvalVerdictFailed)

	// A is not yet available, so the pass that sees the pin stops before
	// the promotion branch.
	markAvailable(t, ns, controller.WorkloadName("notepin", revA), 0)
	mustEdit(t, a, func(a *assaydv1alpha1.Agent) {
		a.Spec.Gates = nil
		a.Spec.Release = &assaydv1alpha1.ReleaseSpec{TargetRevisionDigest: dA}
	})
	reconcileOnce(t, r, a)
	if got := liveAgent(t, a).Status.ActiveRevision; got != revB {
		t.Fatalf("setup: the pass with A unavailable changed the active revision to %s", got)
	}
	skipped, passed := removedGateMessages(t, a)
	for name, msg := range map[string]string{"GatesSkipped": skipped, "GatesPassed": passed} {
		if !strings.Contains(msg, notePinned) || strings.Contains(msg, notePending) {
			t.Errorf("%s does not say D takes no traffic while the pin names A: %s", name, msg)
		}
		if !strings.Contains(msg, "verdict Failed for revision "+revD+" (digest "+dD+")") {
			t.Errorf("%s does not name D's verdict and digests: %s", name, msg)
		}
	}

	markAvailable(t, ns, controller.WorkloadName("notepin", revA), 1)
	reconcileOnce(t, r, a)
	reconcileOnce(t, r, a)
	if got := liveAgent(t, a).Status.ActiveRevisionDigest; got != dA {
		t.Fatalf("setup: the pin to A did not take effect; active %s", got)
	}
	skipped, passed = removedGateMessages(t, a)
	if !strings.Contains(skipped, notePinned) || strings.Contains(skipped, notePending) {
		t.Errorf("GatesSkipped does not say D takes no traffic while the pin names A: %s", skipped)
	}
	// The promotion branch builds GatesPassed for A, and status.eval records
	// D, so in steady state GatesPassed names no verdict (A27.1 keeps that
	// asymmetry).
	want := "spec.gates was removed, so no revision of this Agent is eval-gated; revision " + revA +
		" (digest " + dA + ") is the active revision"
	if passed != want {
		t.Errorf("GatesPassed on a steady pass with A serving carries a note:\n got  %s\n want %s", passed, want)
	}
}

// R2. The gate removed while D's run is Running, and D promoted: on the pass
// after the promotion both messages say D is the active revision.
func TestARemovedGateOverARunningPromotedCandidateSaysItIsActive(t *testing.T) {
	ns := newNamespace(t)
	sd := mustCreateSuite(t, ns, "smoke")
	a, r := servedUngated(t, ns, "noteactive")
	revD, dD := gatedCandidate(t, r, a, imageB, "smoke")
	unpassedCandidateHeld(t, r, a, revD, dD, sd, assaydv1alpha1.EvalVerdictRunning)

	mustEdit(t, a, func(a *assaydv1alpha1.Agent) { a.Spec.Gates = nil })
	reconcileOnce(t, r, a)
	if got := liveAgent(t, a).Status.ActiveRevisionDigest; got != dD {
		t.Fatalf("setup: removing the gate did not promote D; active %s", got)
	}
	reconcileOnce(t, r, a)
	skipped, passed := removedGateMessages(t, a)
	for name, msg := range map[string]string{"GatesSkipped": skipped, "GatesPassed": passed} {
		if !strings.Contains(msg, noteActive) || strings.Contains(msg, notePending) {
			t.Errorf("%s does not say D is the active revision: %s", name, msg)
		}
		if !strings.Contains(msg, "verdict Running for revision "+revD+" (digest "+dD+")") {
			t.Errorf("%s does not name D's verdict and digests: %s", name, msg)
		}
	}
}

// R3. Unpinned, with D not available: D takes traffic once it is available.
func TestARemovedGateOverAnUnavailableCandidateSaysItTakesTrafficOnceAvailable(t *testing.T) {
	ns := newNamespace(t)
	sd := mustCreateSuite(t, ns, "smoke")
	a, r := servedUngated(t, ns, "notepending")
	active := a.Status.ActiveRevisionDigest
	revD, dD := gatedCandidate(t, r, a, imageB, "smoke")
	unpassedCandidateHeld(t, r, a, revD, dD, sd, assaydv1alpha1.EvalVerdictFailed)

	markAvailable(t, ns, controller.WorkloadName("notepending", revD), 0)
	mustEdit(t, a, func(a *assaydv1alpha1.Agent) { a.Spec.Gates = nil })
	reconcileOnce(t, r, a)
	if got := liveAgent(t, a).Status.ActiveRevisionDigest; got != active {
		t.Fatalf("setup: D promoted with no replicas; active %s", got)
	}
	skipped, passed := removedGateMessages(t, a)
	for name, msg := range map[string]string{"GatesSkipped": skipped, "GatesPassed": passed} {
		if !strings.Contains(msg, "and it "+notePending+" (design 16 Q9)") {
			t.Errorf("%s does not say D takes traffic once it is available: %s", name, msg)
		}
		if !strings.Contains(msg, "verdict Failed for revision "+revD+" (digest "+dD+")") {
			t.Errorf("%s does not name D's verdict and digests: %s", name, msg)
		}
	}
}
