// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package envtest

import (
	"testing"
)

// TestACreateWaitsInConvergingUntilThePolicyConverges pins the policy half of
// a Create's Converging stage (design 03 §3.3.2's tuple): with the route
// accepted and the <agent>-auth policy NOT reported, the Create stays in
// Converging, sends no request, and publishes nothing — and it moves on once
// the policy is reported.
//
// Written because nothing pinned it. An independent review of PR #67 deleted
// the policyConverged check from runCreate's Converging stage and every
// behavioural test still passed: the existing cases accept the route and the
// policy together, or neither.
func TestACreateWaitsInConvergingUntilThePolicyConverges(t *testing.T) {
	ns := newNamespace(t)
	a := mustCreateAgent(t, ns, "policywait", nil)
	r, stub := createReconciler()
	promote(t, r, a)

	acceptRoute(t, ns, "policywait")
	for i := 0; i < 3; i++ {
		reconcileOnce(t, r, a)
		if tx := txOf(t, a); tx == nil || tx.Stage != "Converging" {
			t.Fatalf("pass %d: with the route accepted and the policy not reported, the Create "+
				"left Converging: %+v", i, tx)
		}
	}
	if n := stub.count("policywait"); n != 0 {
		t.Errorf("the Create sent %d anonymous request(s) before the policy converged", n)
	}
	if routePublished(t, ns, "policywait") {
		t.Fatal("the route was published before the policy converged")
	}

	acceptPolicy(t, ns, "policywait")
	reconcileOnce(t, r, a)
	if tx := txOf(t, a); tx == nil || tx.Stage == "Converging" {
		t.Fatalf("the policy converged and the Create did not move on: %+v", tx)
	}
}
