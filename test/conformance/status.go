// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package conformance

// statusIsCurrent decides whether a published condition describes the
// generation the caller just wrote, or an earlier one.
//
// This is the whole reason the cluster suite can claim anything at all. A
// controller publishes status asynchronously, so a test that reads conditions
// immediately after a patch reads the PREVIOUS reconcile's verdict and asserts
// against it — which is how a NACK test once passed in 0.66 seconds without the
// controller having looked at the object.
//
// It lives in an untagged file, separate from the cluster suite it serves, so
// this decision is pinned by a test that runs in every `make test` rather than
// only when someone has a cluster. The rule it encodes:
//
//	observedGeneration missing  -> NOT current
//	observedGeneration != gen   -> NOT current
//	observedGeneration == gen   -> current
//
// The missing case is the one worth stating twice. `observedGeneration` is
// optional in the Gateway API's condition schema, so absence is common and
// means "this writer did not say which generation it saw" — which is the
// absence of evidence, not evidence of currency. Reading it as current is the
// same stale-status trap that a naive read-immediately test falls into, only
// harder to see, because the guard is present and merely inert.
func statusIsCurrent(conditions []map[string]any, gen int64) bool {
	if len(conditions) == 0 {
		// No conditions is not a current status; it is no status.
		return false
	}
	for _, c := range conditions {
		og, ok := c["observedGeneration"].(float64)
		if !ok || int64(og) != gen {
			return false
		}
	}
	return true
}
