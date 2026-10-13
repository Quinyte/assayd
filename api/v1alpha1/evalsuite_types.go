// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EvalSuiteSpec is design 16 §1.1's slice of the EvalSuite: a list of cases,
// at most one metric, and a per-case timeout. It is a strict subset of design
// 16 §3's schema (Q8 (a)), so the full schema can add fields without migrating
// a stored suite. Every other §3 field — runner, target, dataset.goldenSetRef,
// dataset.fromSessions, gate, budget — is absent, and the API server prunes or
// refuses it as it does any unknown field.
//
// An Agent binds a suite through spec.gates[].evalSuiteRef, in the Agent's own
// namespace; the suite names no Agent.
//
// Nothing in the operator runs a suite yet. Declaring a gate holds the
// candidate with GatesPassed=False/GateControllerUnimplemented until the eval
// controller ships.
type EvalSuiteSpec struct {
	// Dataset holds the cases each candidate revision is sent.
	Dataset EvalDataset `json:"dataset"`

	// Metrics holds at most one metric, task_completion: the share of cases
	// that must pass. With no metric, every case must pass (threshold "1").
	// +kubebuilder:validation:MaxItems=1
	// +optional
	Metrics []EvalMetric `json:"metrics,omitempty"`

	// CaseTimeoutSeconds bounds each case's request to the candidate. A case
	// with no answer within it fails, reason Unreachable.
	// +kubebuilder:default=10
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=10
	// +optional
	CaseTimeoutSeconds int32 `json:"caseTimeoutSeconds,omitempty"`
}

// The upper bounds on cases and on metrics are schema MaxItems and not CEL
// because the API server needs them to estimate the cost of the rules on each
// item: without them the lists are unbounded and the CRD is refused as over the
// CEL cost budget. Measured on envtest's API server 1.36.2, with the
// threshold's MaxLength=16 and metrics left unbounded, the threshold's format
// rule exceeds it by 6.7x and its greater-than-zero rule by 3.1x. A CEL rule repeating a MaxItems bound could never be
// the one that refuses, so there is none. The lower bound on cases is CEL so
// that its refusal can say why.

// EvalDataset is the suite's cases, written inline.
type EvalDataset struct {
	// Cases are sent one A2A task each: 1 to 20, with unique ids.
	// +kubebuilder:validation:MaxItems=20
	// +kubebuilder:validation:XValidation:rule="self.size() >= 1",message="spec.dataset.cases is empty, and an eval with no cases would pass every revision. Add at least one case: {id, message, expect: {contains or equals}}."
	// +kubebuilder:validation:XValidation:rule="self.all(c, self.exists_one(d, d.id == c.id))",message="spec.dataset.cases[].id must be unique within the suite: each case's outcome is recorded by its id. Rename the duplicate."
	Cases []EvalCase `json:"cases"`
}

// EvalCase is one task sent to the candidate, and what its answer must hold.
//
// +kubebuilder:validation:XValidation:rule="(!has(self.expect.contains) || !self.message.contains(self.expect.contains)) && (!has(self.expect.equals) || self.message != self.expect.equals)",message="this case's message already satisfies its own expectation, so an agent that echoes its input would pass it. Change the expectation to something only a correct answer contains."
type EvalCase struct {
	// ID names the case in status.eval.cases[]. A DNS label.
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:XValidation:rule="self.matches('^[a-z0-9]([-a-z0-9]*[a-z0-9])?$')",message="spec.dataset.cases[].id must be a DNS label: lowercase letters, digits and '-', starting and ending with a letter or digit, at most 63 characters."
	ID string `json:"id"`

	// Message is sent as the task's one text part.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=4096
	Message string `json:"message"`

	// Expect is what the answer text must satisfy.
	Expect EvalExpect `json:"expect"`
}

// EvalExpect compares the answer text, case-sensitively and with no
// normalization. Exactly one of Contains or Equals is set.
//
// Each is capped at 512 characters, not 4096: the API server estimates a CEL
// `contains` at the product of its two strings' sizes, and at 4096 against a
// 4096-character message over 20 cases it refuses the CRD itself as over the
// cost budget (design 16 §1.1, A5, measured on envtest v1.36.2).
//
// +kubebuilder:validation:XValidation:rule="has(self.contains) != has(self.equals)",message="spec.dataset.cases[].expect must set exactly one of contains or equals."
type EvalExpect struct {
	// Contains passes the case when the answer text contains it.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=512
	// +optional
	Contains string `json:"contains,omitempty"`

	// Equals passes the case when the answer text is exactly it.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=512
	// +optional
	Equals string `json:"equals,omitempty"`
}

// EvalMetric is the slice's one metric.
type EvalMetric struct {
	// Name must be task_completion.
	// +kubebuilder:validation:XValidation:rule="self == 'task_completion'",message="spec.metrics[].name must be task_completion: it is the only metric in this release (design 16 §1.1)."
	Name string `json:"name"`

	// Threshold is the share of cases that must pass: a decimal string greater
	// than 0 and at most 1, with at most three decimal places. It is a string
	// because a float field cannot be compared exactly; the operator compares
	// in integer thousandths.
	//
	// MaxLength is a cost bound for the CEL rules, set above any plausible
	// over-precise input so that "0.6666" reaches the format rule's message
	// rather than a bare "too long": the API server skips CEL once a length
	// bound fails.
	// +kubebuilder:validation:MaxLength=16
	// +kubebuilder:validation:XValidation:rule="self.matches('^(0([.][0-9]{1,3})?|1([.]0{1,3})?)$')",message="spec.metrics[].threshold must be a decimal string from 0 to 1 with at most three decimal places, such as 0.66 or 1."
	// +kubebuilder:validation:XValidation:rule="!self.matches('^0([.]0{1,3})?$')",message="spec.metrics[].threshold must be greater than 0: a threshold of 0 would pass every revision."
	Threshold string `json:"threshold"`
}

// +kubebuilder:object:root=true

// EvalSuite is the set of cases a gated Agent's candidate revision must pass
// before it takes traffic (design 16 §1.1).
type EvalSuite struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec EvalSuiteSpec `json:"spec"`
}

// +kubebuilder:object:root=true

type EvalSuiteList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []EvalSuite `json:"items"`
}

func init() {
	SchemeBuilder.Register(&EvalSuite{}, &EvalSuiteList{})
}
