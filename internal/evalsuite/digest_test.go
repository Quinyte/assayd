// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package evalsuite

import (
	"testing"

	"sigs.k8s.io/yaml"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
)

func spec(t *testing.T, doc string) assaydv1alpha1.EvalSuiteSpec {
	t.Helper()
	var s assaydv1alpha1.EvalSuiteSpec
	if err := yaml.UnmarshalStrict([]byte(doc), &s); err != nil {
		t.Fatalf("parse suite spec: %v", err)
	}
	return s
}

func digest(t *testing.T, s assaydv1alpha1.EvalSuiteSpec) string {
	t.Helper()
	d, err := Digest(s)
	if err != nil {
		t.Fatalf("digest: %v", err)
	}
	return d
}

// The realistic suite of design 16 §1.1, written in the design's order.
const base = `
dataset:
  cases:
    - id: deductible-lookup
      message: "What is the deductible on policy P-1042?"
      expect: {contains: "500"}
    - id: refuses-travel
      message: "Book me a flight to Lisbon."
      expect: {contains: "cannot help with travel"}
    - id: health
      message: "status"
      expect: {equals: "ok"}
metrics:
  - {name: task_completion, threshold: "0.66"}
caseTimeoutSeconds: 10
`

// T3: the suite digest is stable under key order and case order, and changed
// by each of a case's message, its expectation, the threshold and the timeout.
// A reorder must not change it, because a run's record is kept by id and the
// pair (revision digest, suite digest) is what a verdict is bound to.
//
// Key order is not exercised here: a spec decoded into the Go type has no key
// order left to vary. TestSuiteDigestIsPinned holds it, because a digest whose
// keys were not sorted would not match the independently computed golden.
func TestSuiteDigestIsStableAndSensitive(t *testing.T) {
	want := digest(t, spec(t, base))

	stable := map[string]string{
		"cases in another order": `
dataset:
  cases:
    - id: health
      message: "status"
      expect: {equals: "ok"}
    - id: refuses-travel
      message: "Book me a flight to Lisbon."
      expect: {contains: "cannot help with travel"}
    - id: deductible-lookup
      message: "What is the deductible on policy P-1042?"
      expect: {contains: "500"}
metrics:
  - {name: task_completion, threshold: "0.66"}
caseTimeoutSeconds: 10
`,
		// The API server writes the default; a suite read before it did, or
		// built in Go, must digest the same as the stored one.
		"the timeout left to its default": `
dataset:
  cases:
    - id: deductible-lookup
      message: "What is the deductible on policy P-1042?"
      expect: {contains: "500"}
    - id: refuses-travel
      message: "Book me a flight to Lisbon."
      expect: {contains: "cannot help with travel"}
    - id: health
      message: "status"
      expect: {equals: "ok"}
metrics:
  - {name: task_completion, threshold: "0.66"}
`,
	}
	for name, doc := range stable {
		if got := digest(t, spec(t, doc)); got != want {
			t.Errorf("%s changed the digest: %s, want %s", name, got, want)
		}
	}

	changed := map[string]func(*assaydv1alpha1.EvalSuiteSpec){
		"a case's message":  func(s *assaydv1alpha1.EvalSuiteSpec) { s.Dataset.Cases[0].Message += "?" },
		"a case's contains": func(s *assaydv1alpha1.EvalSuiteSpec) { s.Dataset.Cases[0].Expect.Contains = "250" },
		"a case's equals":   func(s *assaydv1alpha1.EvalSuiteSpec) { s.Dataset.Cases[2].Expect.Equals = "OK" },
		"contains to equals": func(s *assaydv1alpha1.EvalSuiteSpec) {
			s.Dataset.Cases[0].Expect = assaydv1alpha1.EvalExpect{Equals: "500"}
		},
		"the threshold": func(s *assaydv1alpha1.EvalSuiteSpec) { s.Metrics[0].Threshold = "0.67" },
		"the timeout":   func(s *assaydv1alpha1.EvalSuiteSpec) { s.CaseTimeoutSeconds = 9 },
		"a case's id":   func(s *assaydv1alpha1.EvalSuiteSpec) { s.Dataset.Cases[1].ID = "refuses-trips" },
	}
	for name, mutate := range changed {
		s := spec(t, base)
		mutate(&s)
		if got := digest(t, s); got == want {
			t.Errorf("changing %s left the digest unchanged: a verdict earned on the old suite "+
				"would promote under the new one", name)
		}
	}
}

// The digest must not move between releases for an unchanged suite: a pair in
// status.eval.failed[] is keyed on it, and a moved digest would re-open every
// failed pair on upgrade. If a change to canonicalization is ever deliberate,
// this value changes with it, in a change that says so.
func TestSuiteDigestIsPinned(t *testing.T) {
	// Computed independently of this package: Python's json.dumps of the same
	// value with sort_keys=True, separators=(",", ":"), ensure_ascii=False and
	// the cases sorted by id, then hashlib.sha256.
	const golden = "3f304ca25896938de0b4f50589b0e60c7fb538b9744a81040c2c163eda769fd9"
	if got := digest(t, spec(t, base)); got != golden {
		t.Errorf("the suite digest of design 16 §1.1's realistic suite is %s, pinned %s", got, golden)
	}
}

// The canonical form is pinned for characters encoders disagree on, too: HTML
// characters are written raw, non-ASCII is written as UTF-8, a control
// character as \u00XX, and U+2028 and U+2029 escaped. Python's json.dumps does
// not escape U+2028 by itself, so the independent computation escapes it
// explicitly — the point being that the rule is stated and held, so a change
// to it cannot reopen every failed pair silently.
func TestSuiteDigestPinsItsEscaping(t *testing.T) {
	s := spec(t, base)
	s.Dataset.Cases[0].Message = "caf\u00e9 <b>&</b> one\u2028two\u2029three\u0001\tend"
	s.Dataset.Cases[0].Expect.Contains = "\u00fcber & <ok>"
	// Computed as TestSuiteDigestIsPinned's golden, with
	// .replace("\u2028", "\\u2028").replace("\u2029", "\\u2029") applied to
	// the encoded text before hashing.
	const golden = "af3f31e6ca73f2ce064aacd7e6bda34caff445b2366055797aea84f403d8dfae"
	if got := digest(t, s); got != golden {
		t.Errorf("the suite digest of a suite with HTML, non-ASCII, line-separator and control "+
			"characters is %s, pinned %s", got, golden)
	}
}

// Digesting must not reorder the caller's suite: the eval controller reads
// cases in the order it was given and keys them by id itself.
func TestSuiteDigestDoesNotMutateItsInput(t *testing.T) {
	s := spec(t, base)
	_ = digest(t, s)
	// Written order is deductible-lookup, refuses-travel, health; id order
	// would put health second.
	if s.Dataset.Cases[1].ID != "refuses-travel" || s.CaseTimeoutSeconds != 10 {
		t.Error("Digest reordered or defaulted the caller's spec")
	}
	bare := spec(t, base)
	bare.CaseTimeoutSeconds = 0
	_ = digest(t, bare)
	if bare.CaseTimeoutSeconds != 0 {
		t.Error("Digest wrote the default into the caller's spec")
	}
}
