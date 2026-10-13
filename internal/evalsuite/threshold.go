// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package evalsuite

import (
	"fmt"
	"regexp"

	assaydv1alpha1 "github.com/Quinyte/assayd/api/v1alpha1"
)

// thresholdForm is admission's format rule for spec.metrics[].threshold
// (api/v1alpha1/evalsuite_types.go): 0 or 1, with at most three decimal
// places, and nothing above 1. Admission's second rule, greater than 0, is
// checked on the parsed value.
var thresholdForm = regexp.MustCompile(`^(0([.][0-9]{1,3})?|1([.]0{1,3})?)$`)

// Threshold is the suite's task_completion threshold in integer thousandths:
// "0.66" is 660 and "1" is 1000. A suite with no metric has threshold "1",
// every case must pass (design 16 §1.1).
//
// The string is read digit by digit and never as a float, so no rounding can
// move a boundary. A threshold admission would refuse is an error, not a
// value: the operator meets one only on a suite stored before the rule, and
// reading it as anything would pass or fail revisions on a number nobody
// wrote.
func Threshold(spec assaydv1alpha1.EvalSuiteSpec) (int64, error) {
	if len(spec.Metrics) == 0 {
		return 1000, nil
	}
	s := spec.Metrics[0].Threshold
	if !thresholdForm.MatchString(s) {
		return 0, fmt.Errorf("spec.metrics[0].threshold %q is not a decimal from 0 to 1 with at most three decimal places", s)
	}
	var th int64
	// Integer part, then exactly three fractional digits, padded with zeros.
	th = int64(s[0]-'0') * 1000
	if len(s) > 2 {
		frac := s[2:] + "00"
		th += int64(frac[0]-'0')*100 + int64(frac[1]-'0')*10 + int64(frac[2]-'0')
	}
	if th == 0 {
		return 0, fmt.Errorf("spec.metrics[0].threshold %q is 0, which would pass every revision", s)
	}
	return th, nil
}

// Passes is the verdict rule: a run passes when passed × 1000 ≥ threshold ×
// total, with the threshold in thousandths. It is integer arithmetic, so no
// rounding decides a boundary: 2 of 3 passes "0.666" and fails "0.667"
// (design 16 §1.1, step 7). A run with no cases does not pass.
func Passes(passed, total int, threshold int64) bool {
	if total <= 0 {
		return false
	}
	return int64(passed)*1000 >= threshold*int64(total)
}
