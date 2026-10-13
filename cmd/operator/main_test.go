// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"testing"
	"time"
)

// T39 (design 16 §1.1, A7). The eval controller sends a case and records its
// answer on a context shutdown does not cancel, bounded at 5 s + 10 s + 5 s =
// 20 s, and a graceful stop lets that finish only because the manager waits for
// reconciles in flight. How long it waits is GracefulShutdownTimeout. Left
// unset, it is controller-runtime's default, and a bound that rests on a
// library's unset default is rule 7's case: nothing here would notice the
// default moving. So the option must be SET, and at 25 s or more.
func TestManagerWaitsForReconcilesInFlightOnShutdown(t *testing.T) {
	opts := managerOptions(":8080", ":8081", true)
	if opts.GracefulShutdownTimeout == nil {
		t.Fatal("the manager options leave GracefulShutdownTimeout unset, so the shutdown bound " +
			"is controller-runtime's default and not this operator's (design 16 A7)")
	}
	if got, floor := *opts.GracefulShutdownTimeout, 25*time.Second; got < floor {
		t.Errorf("GracefulShutdownTimeout is %v; it must be at least %v so a case in flight at "+
			"shutdown, bounded at 20 s, can finish and record what the candidate answered "+
			"(design 16 A7)", got, floor)
	}
}
