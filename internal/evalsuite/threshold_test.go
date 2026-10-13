// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package evalsuite

import (
	"math/big"
	"testing"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
)

func withThreshold(th string) assaydv1alpha1.EvalSuiteSpec {
	return assaydv1alpha1.EvalSuiteSpec{Metrics: []assaydv1alpha1.EvalMetric{
		{Name: "task_completion", Threshold: th},
	}}
}

func thousandths(t *testing.T, s assaydv1alpha1.EvalSuiteSpec) int64 {
	t.Helper()
	th, err := Threshold(s)
	if err != nil {
		t.Fatalf("threshold: %v", err)
	}
	return th
}

// T2: the boundary design 16 §1.1 names. Two of three is 0.6666…, so it
// passes "0.666" and fails "0.667". Changing ≥ to > does not move this pair,
// which is why TestThresholdRuleIsExact exists beside it.
func TestThresholdRuleAtItsBoundary(t *testing.T) {
	if !Passes(2, 3, thousandths(t, withThreshold("0.666"))) {
		t.Error(`2 of 3 failed threshold "0.666"; 2/3 is above 0.666, so it must pass`)
	}
	if Passes(2, 3, thousandths(t, withThreshold("0.667"))) {
		t.Error(`2 of 3 passed threshold "0.667"; 2/3 is below 0.667, so it must fail`)
	}
}

// Every (passed, total, threshold) a suite can produce — at most 20 cases, a
// threshold in thousandths from 0.001 to 1 — is checked against exact rational
// arithmetic, an oracle independent of the rule under test. Equality cases
// such as 2 of 4 at "0.5" catch ≥ changed to >, which the boundary pair above
// does not.
//
// T2's other named mutation, a float comparison in place of the integer one,
// survives this test, and no test on an admitted suite can kill it: measured
// over this whole domain, float64 forms of the rule (passed/total ≥ t,
// passed ≥ t × total, and the threshold read with strconv.ParseFloat) agree
// with the exact answer at every point. The integer rule is kept because it
// is correct by construction rather than by a property of float64 at these
// sizes, which a wider domain would not keep.
func TestThresholdRuleIsExact(t *testing.T) {
	for total := 1; total <= 20; total++ {
		for passed := 0; passed <= total; passed++ {
			score := big.NewRat(int64(passed), int64(total))
			for th := int64(1); th <= 1000; th++ {
				want := score.Cmp(big.NewRat(th, 1000)) >= 0
				if got := Passes(passed, total, th); got != want {
					t.Fatalf("Passes(%d, %d, %d thousandths) = %v; exactly, %d/%d ≥ %d/1000 is %v",
						passed, total, th, got, passed, total, th, want)
				}
			}
		}
	}
}

// The threshold is read in integer thousandths from its decimal string, with
// no float parse, and with no metric it is "1": every case must pass.
func TestThresholdParsesTheAdmittedSpellings(t *testing.T) {
	for in, want := range map[string]int64{
		"1": 1000, "1.0": 1000, "1.00": 1000, "1.000": 1000,
		"0.5": 500, "0.66": 660, "0.666": 666, "0.05": 50, "0.005": 5, "0.001": 1, "0.1": 100,
	} {
		if got := thousandths(t, withThreshold(in)); got != want {
			t.Errorf("threshold %q = %d thousandths, want %d", in, got, want)
		}
	}
	if got := thousandths(t, assaydv1alpha1.EvalSuiteSpec{}); got != 1000 {
		t.Errorf("a suite with no metric has threshold %d thousandths; design 16 §1.1 says absent means \"1\", 1000", got)
	}
}

// Admission refuses every one of these, so the operator only meets them on a
// suite stored before a rule existed or written around it. Each is refused
// rather than read as something, because a threshold misread low passes
// revisions the suite's author meant to fail.
func TestThresholdRefusesWhatAdmissionRefuses(t *testing.T) {
	for _, in := range []string{
		"", "0", "0.0", "0.000", "1.001", "1.5", "2", "0.6666", ".5", "0.", "-0.5", "+0.5",
		" 0.5", "0.5 ", "0x1", "1e0", "0,5", "１",
	} {
		if th, err := Threshold(withThreshold(in)); err == nil {
			t.Errorf("threshold %q was read as %d thousandths; it is not an admitted threshold and must be refused", in, th)
		}
	}
}

// A run with no cases cannot pass. Admission refuses an empty suite, so this
// is the rule failing closed rather than a reachable state.
func TestThresholdRuleFailsAnEmptyRun(t *testing.T) {
	if Passes(0, 0, 1) {
		t.Error("0 of 0 passed; a run with no cases would pass every revision")
	}
}
