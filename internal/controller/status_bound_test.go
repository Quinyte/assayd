// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
)

// A condition message over the API server's cap must not reach the API server.
//
// This has happened twice. A81 grew a held report by a few hundred bytes a pass
// until every status write for that Agent failed with `Too long`; A77's first
// implementation rendered an unbounded `spec.externalIPs` into a refusal
// message with the same result. The failure mode is the worst one this
// controller has — the WHOLE status frozen at its pre-incident value, so an
// Agent reports Ready=True with its route published while the incident the
// message describes goes unwritten, and nothing retries into anything but the
// same rejection.
//
// Every message should be bounded where it is composed, and each one that is
// has its own reason for the bound. This is the backstop that keeps the class
// non-fatal when the next composer is not, and it is pinned here rather than
// left as defensive code no test can reach (rule 5): the wiring is asserted
// through writeStatus, not only the function.
func TestWriteStatusBoundsAConditionMessageTheAPIServerWouldRefuse(t *testing.T) {
	agent := &assaydv1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{Name: "long", Namespace: "claims"},
	}
	r := &AgentReconciler{
		Scheme: testScheme(),
		Client: fake.NewClientBuilder().WithScheme(testScheme()).
			WithObjects(agent).WithStatusSubresource(agent).Build(),
	}

	status := agent.Status.DeepCopy()
	status.Conditions = []metav1.Condition{{
		Type:               string(assaydv1alpha1.CondDegraded),
		Status:             metav1.ConditionTrue,
		Reason:             CondReasonServiceHeadless,
		Message:            strings.Repeat("a", ConditionMessageMax+5000),
		LastTransitionTime: metav1.Now(),
	}}
	if err := r.writeStatus(context.Background(), agent, status); err != nil {
		t.Fatalf("writeStatus: %v", err)
	}

	// Counted in RUNES, which is how apiextensions validates `maxLength` — the
	// first version of this test counted bytes and failed on the truncation
	// marker's own multi-byte ellipsis, which is the same unit confusion the
	// bound itself had.
	got := agent.Status.Conditions[0].Message
	if n := utf8.RuneCountInString(got); n > ConditionMessageMax {
		t.Fatalf("writeStatus passed a %d-rune message to the API server, over the %d-rune cap: "+
			"the write fails and takes EVERY condition on the object with it",
			n, ConditionMessageMax)
	}
	if !utf8.ValidString(got) {
		t.Errorf("the shortened message is not valid UTF-8, so the bound cut a rune in half and " +
			"the encoder will replace it with U+FFFD")
	}
	if !strings.HasSuffix(got, conditionMessageTruncated) {
		t.Errorf("the shortened message does not say it was shortened, so a reader takes the "+
			"text as everything the operator had to say: it ends %q", got[max(0, len(got)-60):])
	}
}

// A message of multi-byte runes must not be cut through one of them.
func TestBoundingDoesNotSplitARune(t *testing.T) {
	conds := []metav1.Condition{{Message: strings.Repeat("\u00e9", ConditionMessageMax+100)}}
	boundConditionMessages(conds)
	if !utf8.ValidString(conds[0].Message) {
		t.Fatalf("the bound cut a multi-byte rune in half")
	}
	if n := utf8.RuneCountInString(conds[0].Message); n > ConditionMessageMax {
		t.Errorf("the bounded message is %d runes, over the %d-rune cap", n, ConditionMessageMax)
	}
}

// Truncation must be deterministic END TO END, or an Agent whose message is
// over the cap is rewritten on every pass — the churn equalStatus exists to
// prevent, and the refusal that produced the over-long message requeues every
// minute.
//
// Asserted through writeStatus and against the stored object, not by calling
// the bound twice: the bound short-circuits under the cap, so a second call on
// its own output is idempotent by construction and an assertion about it
// cannot fail. What CAN fail is the second write, and the review that found the
// first version of this test unfalsifiable is why it is written this way.
func TestASecondWriteOfAnOverLongMessageIsANoOp(t *testing.T) {
	agent := &assaydv1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{Name: "twice", Namespace: "claims"},
	}
	r := &AgentReconciler{
		Scheme: testScheme(),
		Client: fake.NewClientBuilder().WithScheme(testScheme()).
			WithObjects(agent).WithStatusSubresource(agent).Build(),
	}
	over := func() *assaydv1alpha1.AgentStatus {
		return &assaydv1alpha1.AgentStatus{Conditions: []metav1.Condition{{
			Type:               string(assaydv1alpha1.CondDegraded),
			Status:             metav1.ConditionTrue,
			Reason:             CondReasonServiceHeadless,
			Message:            strings.Repeat("c", ConditionMessageMax+5000),
			LastTransitionTime: metav1.NewTime(time.Unix(0, 0)),
		}}}
	}
	if err := r.writeStatus(context.Background(), agent, over()); err != nil {
		t.Fatalf("first write: %v", err)
	}
	first := agent.ResourceVersion
	msg := agent.Status.Conditions[0].Message

	// The same over-long status again, as the next refusing pass composes it.
	if err := r.writeStatus(context.Background(), agent, over()); err != nil {
		t.Fatalf("second write: %v", err)
	}
	if agent.ResourceVersion != first {
		t.Errorf("the second write of an unchanged over-long message was not a no-op "+
			"(resourceVersion %s → %s): a bound that is not deterministic rewrites the status "+
			"on every pass of a refusal that requeues every minute",
			first, agent.ResourceVersion)
	}
	if agent.Status.Conditions[0].Message != msg {
		t.Errorf("the stored message changed between two writes of the same input")
	}
}

// And a message inside the cap is left exactly as it is.
func TestBoundingLeavesAnOrdinaryMessageAlone(t *testing.T) {
	const msg = "revision 126eb2a71a is serving"
	conds := []metav1.Condition{{Message: msg}}
	boundConditionMessages(conds)
	if conds[0].Message != msg {
		t.Errorf("an ordinary message was rewritten to %q", conds[0].Message)
	}
}
