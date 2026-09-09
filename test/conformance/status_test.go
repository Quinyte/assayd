// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package conformance

import "testing"

// TestStatusIsCurrent pins the staleness rule the cluster suite depends on.
// Each case names the mistake it prevents rather than restating the code.
func TestStatusIsCurrent(t *testing.T) {
	cond := func(og any) map[string]any {
		c := map[string]any{"type": "Accepted", "status": "True"}
		if og != nil {
			c["observedGeneration"] = og
		}
		return c
	}

	cases := []struct {
		name string
		in   []map[string]any
		gen  int64
		want bool
		why  string
	}{
		{
			name: "matching generation is current",
			in:   []map[string]any{cond(float64(4))}, gen: 4, want: true,
			why: "the ordinary case; if this fails the suite can never make an assertion",
		},
		{
			name: "older generation is not current",
			in:   []map[string]any{cond(float64(3))}, gen: 4, want: false,
			why: "reading the previous reconcile's verdict is how a NACK test passed in 0.66s",
		},
		{
			name: "MISSING observedGeneration is not current",
			in:   []map[string]any{cond(nil)}, gen: 4, want: false,
			why: "absence of evidence is not evidence of currency; this is the r6 MAJOR 7 defect",
		},
		{
			name: "one stale condition among current ones is not current",
			in:   []map[string]any{cond(float64(4)), cond(float64(3))}, gen: 4, want: false,
			why: "a partially-updated status is a status mid-write, and asserting on it is racy",
		},
		{
			name: "one MISSING among current ones is not current",
			in:   []map[string]any{cond(float64(4)), cond(nil)}, gen: 4, want: false,
			why: "the inert-guard shape: the check runs, finds nothing to compare, and passes anyway",
		},
		{
			name: "no conditions at all is not current",
			in:   nil, gen: 4, want: false,
			why: "an empty list would vacuously satisfy a for-loop and report a status that does not exist",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := statusIsCurrent(tc.in, tc.gen); got != tc.want {
				t.Errorf("statusIsCurrent = %v, want %v — %s", got, tc.want, tc.why)
			}
		})
	}
}
