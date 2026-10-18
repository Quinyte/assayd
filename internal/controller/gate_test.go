// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
)

const (
	gRev    = "abcde12345"
	gDigest = "sha256:candidate"
	gSuite  = "suitedigest"
)

var evalSuiteGR = schema.GroupResource{Group: "assayd.dev", Resource: "evalsuites"}

// everyCause is a gate input on which every reason decideGate can give
// holds at once. Each row of the precedence test clears the cause that won
// the row before, so a reorder of any two adjacent rules fails a row.
func everyCause(now time.Time) gateInput {
	deadline := metav1.NewTime(now.Add(-time.Minute))
	return gateInput{
		desired: gRev, desiredDigest: gDigest, namespace: "team-a", suiteName: "smoke",
		recordErr:   errors.New("injected"),
		missing:     []string{"blockedFor"},
		suiteErr:    apierrors.NewNotFound(evalSuiteGR, "smoke"),
		suite:       &assaydv1alpha1.EvalSuite{},
		suiteDigest: gSuite,
		now:         now,
		status: &assaydv1alpha1.AgentStatus{
			Eval: &assaydv1alpha1.EvalStatus{RevisionDigest: gDigest, SuiteDigest: gSuite,
				Verdict: assaydv1alpha1.EvalVerdictFailed, Score: "0/1",
				Cases:   []assaydv1alpha1.EvalCaseRecord{{ID: "arithmetic", Outcome: "fail", Reason: "Mismatch"}},
				Failed:  []assaydv1alpha1.EvalPair{{RevisionDigest: gDigest, SuiteDigest: gSuite}},
				Blocked: &assaydv1alpha1.EvalBlocked{Reason: ReasonEvalBindingUnsupported, Message: "no HTTP+JSON 1.0 interface"},
				BlockedFor: &assaydv1alpha1.EvalBlockedFor{RevisionDigest: gDigest, SuiteDigest: gSuite,
					LockDeadline: &deadline}},
			Auth: &assaydv1alpha1.AuthStatus{Mode: "none", Transaction: &assaydv1alpha1.AuthTransaction{
				Kind: TxLock, Stage: StageProbingAfter, Deadline: &deadline}},
		},
	}
}

func withCard(st *assaydv1alpha1.AgentStatus) {
	st.Cards = append(st.Cards, assaydv1alpha1.CardStatus{Revision: gRev, RevisionDigest: gDigest, Digest: "cafe"})
}

// Every GatesPassed reason of the gate branch, in its precedence (design 16
// §1.1; the order is decideGate's comment). Each row clears the previous
// row's cause, so a rule moved below its successor fails that row.
func TestTheGateBranchsReasonsTakeTheirPrecedence(t *testing.T) {
	now := time.Now()
	in := everyCause(now)
	step := func(name, want string, clear func(*gateInput)) {
		t.Helper()
		if clear != nil {
			clear(&in)
		}
		got := decideGate(in)
		if got.satisfied || got.reason != want {
			t.Fatalf("%s: decideGate gives satisfied=%v reason %q, want %q (message %q)",
				name, got.satisfied, got.reason, want, got.message)
		}
	}
	step("the CRD cannot be read", ReasonEvalRecordUnverifiable, nil)
	step("the CRD prunes the record", ReasonEvalRecordNotKept, func(in *gateInput) { in.recordErr = nil })
	step("the suite is missing", ReasonEvalSuiteNotFound, func(in *gateInput) { in.missing = nil })
	step("the suite is unreadable", ReasonEvalSuiteUnreadable, func(in *gateInput) {
		in.suiteErr = apierrors.NewForbidden(evalSuiteGR, "smoke", errors.New("no"))
	})
	step("a Failed verdict for the pair", ReasonEvalFailed, func(in *gateInput) { in.suiteErr = nil })
	step("the pair is in failed[]", ReasonEvalPairAlreadyFailed, func(in *gateInput) {
		in.status.Eval.Verdict = ""
	})
	step("no card for the candidate", ReasonEvalAwaitingCard, func(in *gateInput) { in.status.Eval.Failed = nil })
	// Until build step 6 a recorded transaction names itself under
	// GateControllerUnimplemented, never EvalWaitingForAuth: no eval
	// controller exists to wait on it (the coordinator's decision).
	step("a recorded Lock", ReasonGateControllerUnimplemented, func(in *gateInput) { withCard(in.status) })
	if d := decideGate(in); !strings.Contains(d.message, "An -auth Lock is also recorded") {
		t.Fatalf("a recorded Lock: GateControllerUnimplemented does not name it: %s", d.message)
	}
	step("blocked, matched", ReasonEvalBindingUnsupported, func(in *gateInput) {
		in.status.Auth = nil
		in.status.Eval.BlockedFor.LockDeadline = nil
	})
	step("nothing at all", ReasonGateControllerUnimplemented, func(in *gateInput) { in.status.Eval.Blocked = nil })
}

// A written verdict outranks every reason a run could be waiting for: a
// Passed verdict with its card promotes beside a recorded transaction and an
// unmatched blocked (design 16 §1.1, "Clause 5 stops new claims only"), and a
// Passed verdict whose card is gone holds as EvalAwaitingCard, not as the
// transaction.
func TestAPassedVerdictOutranksWhatARunWouldWaitFor(t *testing.T) {
	in := everyCause(time.Now())
	in.recordErr, in.missing, in.suiteErr = nil, nil, nil
	in.status.Eval.Verdict = assaydv1alpha1.EvalVerdictPassed
	if d := decideGate(in); d.satisfied || d.reason != ReasonEvalAwaitingCard {
		t.Fatalf("Passed with no card: satisfied=%v reason %q, want EvalAwaitingCard", d.satisfied, d.reason)
	} else if !strings.Contains(d.message, "passed") {
		t.Errorf("EvalAwaitingCard does not say the candidate passed: %s", d.message)
	}
	withCard(in.status)
	if d := decideGate(in); !d.satisfied {
		t.Fatalf("Passed with its card recorded did not satisfy the gate: %s %s", d.reason, d.message)
	}
}

// The four promotion conditions, each broken alone.
func TestEachPromotionConditionHoldsTheCandidateAlone(t *testing.T) {
	passed := func() gateInput {
		in := gateInput{desired: gRev, desiredDigest: gDigest, suiteName: "smoke", suiteDigest: gSuite,
			suite: &assaydv1alpha1.EvalSuite{}, now: time.Now(),
			status: &assaydv1alpha1.AgentStatus{Eval: &assaydv1alpha1.EvalStatus{
				RevisionDigest: gDigest, SuiteDigest: gSuite, Verdict: assaydv1alpha1.EvalVerdictPassed}}}
		withCard(in.status)
		return in
	}
	if d := decideGate(passed()); !d.satisfied {
		t.Fatalf("control: all four conditions hold and the gate is not satisfied: %s", d.reason)
	}
	for name, breakIt := range map[string]func(*gateInput){
		"verdict Running":         func(in *gateInput) { in.status.Eval.Verdict = assaydv1alpha1.EvalVerdictRunning },
		"another revision digest": func(in *gateInput) { in.status.Eval.RevisionDigest = "sha256:other" },
		"another suite digest":    func(in *gateInput) { in.suiteDigest = "edited" },
		"no card":                 func(in *gateInput) { in.status.Cards = nil },
		"a cleared card":          func(in *gateInput) { in.status.Cards[0].Digest = "" },
	} {
		in := passed()
		breakIt(&in)
		if d := decideGate(in); d.satisfied {
			t.Errorf("%s: the gate is satisfied", name)
		}
	}
}

// A refused Adopt is not a Create or a Lock, and does not hold the eval
// (design 16 §1.1's table). Until build step 6 a recorded Create is named
// under GateControllerUnimplemented, which claims no run, never as
// EvalWaitingForAuth.
func TestARefusedAdoptDoesNotHoldTheEval(t *testing.T) {
	in := gateInput{desired: gRev, desiredDigest: gDigest, suiteDigest: gSuite, now: time.Now(),
		status: &assaydv1alpha1.AgentStatus{Auth: &assaydv1alpha1.AuthStatus{Transaction: &assaydv1alpha1.AuthTransaction{
			Kind: TxAdopt, Stage: StageRefused}}}}
	withCard(in.status)
	if d := decideGate(in); d.reason != ReasonGateControllerUnimplemented {
		t.Errorf("beside a refused Adopt the reason is %q, want GateControllerUnimplemented", d.reason)
	} else if strings.Contains(d.message, "also recorded") {
		t.Errorf("a refused Adopt is named as a transaction that holds claims: %s", d.message)
	}
	in.status.Auth.Transaction = &assaydv1alpha1.AuthTransaction{Kind: TxCreate, Stage: StagePreparingRoute}
	if d := decideGate(in); d.reason != ReasonGateControllerUnimplemented {
		t.Errorf("beside a recorded Create the reason is %q, want GateControllerUnimplemented", d.reason)
	} else if !strings.Contains(d.message, "An -auth Create is also recorded on this Agent, in stage "+
		StagePreparingRoute) {
		t.Errorf("GateControllerUnimplemented does not name the Create and its stage: %s", d.message)
	} else if strings.Contains(d.message, "deadline") {
		t.Errorf("the message names a deadline the transaction does not have: %s", d.message)
	}
	if got := claimHoldingTransaction(in.status); got == nil || got.Kind != TxCreate {
		t.Errorf("claimHoldingTransaction misses a recorded Create: %+v", got)
	}
	in.status.Auth.Transaction = &assaydv1alpha1.AuthTransaction{Kind: TxLock, Stage: StageApplyingPolicies}
	if got := claimHoldingTransaction(in.status); got == nil || got.Kind != TxLock {
		t.Errorf("claimHoldingTransaction misses a recorded Lock: %+v", got)
	}
	in.status.Auth.Transaction = &assaydv1alpha1.AuthTransaction{Kind: TxAdopt, Stage: StageRefused}
	if got := claimHoldingTransaction(in.status); got != nil {
		t.Errorf("claimHoldingTransaction counts a refused Adopt: %+v", got)
	}
}

// Q12 (a): past its deadline, the transaction still holds the eval, and the
// message says when the deadline passed; before it, it says nothing of one.
func TestARecordedTransactionNamesAPassedDeadline(t *testing.T) {
	now := time.Now()
	past, future := metav1.NewTime(now.Add(-time.Minute)), metav1.NewTime(now.Add(time.Minute))
	in := gateInput{desired: gRev, desiredDigest: gDigest, suiteDigest: gSuite, now: now,
		status: &assaydv1alpha1.AgentStatus{Auth: &assaydv1alpha1.AuthStatus{Transaction: &assaydv1alpha1.AuthTransaction{
			Kind: TxLock, Stage: StageProbingAfter, Deadline: &past}}}}
	withCard(in.status)
	if d := decideGate(in); !strings.Contains(d.message, "deadline passed at "+past.UTC().Format(time.RFC3339)) {
		t.Errorf("a Lock past its deadline: %s", d.message)
	}
	in.status.Auth.Transaction.Deadline = &future
	if d := decideGate(in); strings.Contains(d.message, "deadline") {
		t.Errorf("a Lock inside its deadline says one passed: %s", d.message)
	}
}

// blocked applies only while blockedFor matches on all three parts (A12);
// absent or unmatched reads as not evaluated.
func TestBlockedAppliesOnlyWhileBlockedForMatches(t *testing.T) {
	deadline := metav1.NewTime(time.Now().Truncate(time.Second))
	other := metav1.NewTime(deadline.Add(time.Second))
	lock := &assaydv1alpha1.AuthStatus{Transaction: &assaydv1alpha1.AuthTransaction{Kind: TxLock, Deadline: &deadline}}
	for _, tc := range []struct {
		name string
		bf   *assaydv1alpha1.EvalBlockedFor
		auth *assaydv1alpha1.AuthStatus
		want bool
	}{
		{"matched, no Lock", &assaydv1alpha1.EvalBlockedFor{RevisionDigest: gDigest, SuiteDigest: gSuite}, nil, true},
		{"absent", nil, nil, false},
		{"another revision", &assaydv1alpha1.EvalBlockedFor{RevisionDigest: "x", SuiteDigest: gSuite}, nil, false},
		{"another suite", &assaydv1alpha1.EvalBlockedFor{RevisionDigest: gDigest, SuiteDigest: "x"}, nil, false},
		{"computed under a Lock that is gone",
			&assaydv1alpha1.EvalBlockedFor{RevisionDigest: gDigest, SuiteDigest: gSuite, LockDeadline: &deadline}, nil, false},
		{"computed with no Lock, one recorded now",
			&assaydv1alpha1.EvalBlockedFor{RevisionDigest: gDigest, SuiteDigest: gSuite}, lock, false},
		{"the same Lock",
			&assaydv1alpha1.EvalBlockedFor{RevisionDigest: gDigest, SuiteDigest: gSuite, LockDeadline: &deadline}, lock, true},
		{"another Lock",
			&assaydv1alpha1.EvalBlockedFor{RevisionDigest: gDigest, SuiteDigest: gSuite, LockDeadline: &other}, lock, false},
	} {
		st := &assaydv1alpha1.AgentStatus{Auth: tc.auth}
		if got := blockedForMatches(tc.bf, gDigest, gSuite, st); got != tc.want {
			t.Errorf("%s: blockedForMatches = %v, want %v", tc.name, got, tc.want)
		}
	}

	// And through decideGate: each of blocked's three reasons is taken, with
	// its message; an unknown one is not.
	for _, reason := range []string{ReasonEvalBindingUnsupported, ReasonEvalAwaitingCard, ReasonEvalWaitingForAuth, "Invented"} {
		in := gateInput{desired: gRev, desiredDigest: gDigest, suiteDigest: gSuite, now: time.Now(),
			status: &assaydv1alpha1.AgentStatus{Eval: &assaydv1alpha1.EvalStatus{
				Blocked:    &assaydv1alpha1.EvalBlocked{Reason: reason, Message: "the eval controller's words"},
				BlockedFor: &assaydv1alpha1.EvalBlockedFor{RevisionDigest: gDigest, SuiteDigest: gSuite}}}}
		withCard(in.status)
		d := decideGate(in)
		want := reason
		if reason == "Invented" {
			want = ReasonGateControllerUnimplemented
		}
		if d.reason != want {
			t.Errorf("blocked %s: reason %q, want %q", reason, d.reason, want)
		}
		if want == reason && !strings.Contains(d.message, "the eval controller's words") {
			t.Errorf("blocked %s: the message does not carry blocked.message: %s", reason, d.message)
		}
	}
}

// The EvalFailed message names each failed case and its reason, the score
// against the threshold, and both digests (design 16 §1.1's table).
func TestEvalFailedNamesTheCasesTheScoreAndThePair(t *testing.T) {
	in := gateInput{desired: gRev, desiredDigest: gDigest, suiteDigest: gSuite, now: time.Now(),
		suite: &assaydv1alpha1.EvalSuite{Spec: assaydv1alpha1.EvalSuiteSpec{
			Metrics: []assaydv1alpha1.EvalMetric{{Name: "task_completion", Threshold: "0.66"}}}},
		status: &assaydv1alpha1.AgentStatus{Eval: &assaydv1alpha1.EvalStatus{RevisionDigest: gDigest,
			SuiteDigest: gSuite, Verdict: assaydv1alpha1.EvalVerdictFailed, Score: "1/3",
			Cases: []assaydv1alpha1.EvalCaseRecord{{ID: "a", Outcome: "pass"},
				{ID: "b", Outcome: "fail", Reason: "Mismatch"}, {ID: "c", Outcome: "fail", Reason: "Unreachable"}}}}}
	d := decideGate(in)
	for _, want := range []string{"b (Mismatch)", "c (Unreachable)", "1/3", "0.66", gDigest, gSuite} {
		if !strings.Contains(d.message, want) {
			t.Errorf("EvalFailed's message does not name %q: %s", want, d.message)
		}
	}
	if strings.Contains(d.message, "a (") {
		t.Errorf("EvalFailed names a passing case: %s", d.message)
	}
	in.suite = &assaydv1alpha1.EvalSuite{}
	if d := decideGate(in); !strings.Contains(d.message, "threshold 1") {
		t.Errorf("a suite with no metric has threshold 1, and the message says otherwise: %s", d.message)
	}
}

func generatedAgentCRD(t *testing.T) *apiextensionsv1.CustomResourceDefinition {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "config", "crd", "assayd.dev_agents.yaml"))
	if err != nil {
		t.Fatalf("read the generated Agent CRD: %v", err)
	}
	var crd apiextensionsv1.CustomResourceDefinition
	if err := yaml.Unmarshal(b, &crd); err != nil {
		t.Fatalf("parse the generated Agent CRD: %v", err)
	}
	return &crd
}

// The schema check against the GENERATED Agent CRD: it finds nothing missing
// there, and names each of the eight fields, and cases' claim, when the CRD
// lacks it.
func TestTheSchemaCheckReadsTheGeneratedAgentCRD(t *testing.T) {
	if missing := evalRecordMissingFrom(generatedAgentCRD(t)); len(missing) != 0 {
		t.Fatalf("the generated Agent CRD reads as missing %v; it carries every eval-record field", missing)
	}
	evalOf := func(crd *apiextensionsv1.CustomResourceDefinition) apiextensionsv1.JSONSchemaProps {
		return crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["status"].Properties["eval"]
	}
	setEval := func(crd *apiextensionsv1.CustomResourceDefinition, ev apiextensionsv1.JSONSchemaProps) {
		root := crd.Spec.Versions[0].Schema.OpenAPIV3Schema
		st := root.Properties["status"]
		st.Properties["eval"] = ev
		root.Properties["status"] = st
	}
	for _, f := range []string{"suiteDigest", "endpointPath", "cardDigest", "blocked", "blockedFor",
		"verdict", "cases", "failed"} {
		crd := generatedAgentCRD(t)
		ev := evalOf(crd)
		delete(ev.Properties, f)
		setEval(crd, ev)
		if got := evalRecordMissingFrom(crd); len(got) != 1 || got[0] != f {
			t.Errorf("a CRD without status.eval.%s reads as missing %v", f, got)
		}
	}
	crd := generatedAgentCRD(t)
	ev := evalOf(crd)
	cases := ev.Properties["cases"]
	delete(cases.Items.Schema.Properties, "claim")
	ev.Properties["cases"] = cases
	setEval(crd, ev)
	if got := evalRecordMissingFrom(crd); len(got) != 1 || got[0] != "cases[].claim" {
		t.Errorf("a CRD whose cases carry no claim reads as missing %v", got)
	}
	crd = generatedAgentCRD(t)
	crd.Spec.Versions[0].Name = "v1beta1"
	if got := evalRecordMissingFrom(crd); len(got) != 8 {
		t.Errorf("a CRD with no v1alpha1 version reads as missing %v; want all eight", got)
	}
}

// flakyCRDReader serves the generated Agent CRD, or fails, and counts reads.
type flakyCRDReader struct {
	client.Reader
	crd   *apiextensionsv1.CustomResourceDefinition
	fail  bool
	reads int
}

func (f *flakyCRDReader) Get(_ context.Context, _ client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
	f.reads++
	if f.fail {
		return apierrors.NewInternalError(errors.New("injected"))
	}
	f.crd.DeepCopyInto(obj.(*apiextensionsv1.CustomResourceDefinition))
	return nil
}

// The schema check's cache: an answer is reused for its TTL; a failed read
// with nothing cached is the answer; a failed read with an answer cached
// leaves that answer standing, however old, as the EvalSuite detector does.
func TestTheSchemaCheckCachesItsAnswerAndKeepsItOnAFailedRead(t *testing.T) {
	ctx := context.Background()
	f := &flakyCRDReader{crd: generatedAgentCRD(t), fail: true}
	r := &AgentReconciler{Reader: f, EvalRecordCheckTTL: 50 * time.Millisecond}
	if _, err := r.evalRecordMissing(ctx); err == nil {
		t.Fatal("a failed read with nothing cached gave an answer")
	}
	f.fail = false
	if missing, err := r.evalRecordMissing(ctx); err != nil || len(missing) != 0 {
		t.Fatalf("a good read: missing %v, err %v", missing, err)
	}
	reads := f.reads
	if _, err := r.evalRecordMissing(ctx); err != nil || f.reads != reads {
		t.Fatalf("inside the TTL the CRD was read again (%d reads, was %d)", f.reads, reads)
	}
	time.Sleep(60 * time.Millisecond)
	f.fail = true
	if missing, err := r.evalRecordMissing(ctx); err != nil || len(missing) != 0 {
		t.Fatalf("a failed read after the TTL with an answer cached: missing %v, err %v; the last "+
			"answer must stand", missing, err)
	}
	if f.reads != reads+1 {
		t.Fatalf("after the TTL the CRD was not read again (%d reads)", f.reads)
	}
}

// Q9 (a): removing the gate names a Failed or Running verdict it overrides
// for the revision this pass converges, and nothing else.
func TestRemovingTheGateNamesTheVerdictItOverrides(t *testing.T) {
	for verdict, named := range map[assaydv1alpha1.EvalVerdict]bool{
		assaydv1alpha1.EvalVerdictFailed: true, assaydv1alpha1.EvalVerdictRunning: true,
		assaydv1alpha1.EvalVerdictPassed: false, "": false,
	} {
		e := &assaydv1alpha1.EvalStatus{Suite: "smoke", Revision: gRev, RevisionDigest: gDigest,
			SuiteDigest: gSuite, Verdict: verdict}
		note := removedGateNote(e, gRev, gDigest)
		if got := note != ""; got != named {
			t.Errorf("verdict %q: named = %v, want %v (%q)", verdict, got, named, note)
		}
		if named && (!strings.Contains(note, string(verdict)) || !strings.Contains(note, gDigest) ||
			!strings.Contains(note, gSuite)) {
			t.Errorf("verdict %q: the note does not name the verdict and its digests: %s", verdict, note)
		}
		if note := removedGateNote(e, gRev, "sha256:another"); note != "" {
			t.Errorf("verdict %q recorded for another revision is named: %s", verdict, note)
		}
	}
}
