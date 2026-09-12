// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

package compiler

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// The `<concern>` of design 03 §3.2's naming grammar, one per resource this
// package can name.
//
// The naming rule lives here and not in the controller, because it is the
// compiler's rule (§3.2) and because the `-auth` policy's targetRef has to name
// the route the emitter writes by calling the SAME function the emitter calls.
// A second copy of the rule is what would let the two drift, and a policy whose
// targetRef names a route that does not exist is valid, `Accepted`, and
// enforces nothing.
const (
	// ConcernServing is the concern of the per-Agent A2A serving route.
	ConcernServing = "serving"
	// ConcernAuth is the concern of the per-Agent `-auth` policy (§3.2, §3.4.4).
	ConcernAuth = "auth"
)

// EmittedName is design 03 §3.2's naming rule for every resource this operator
// emits at the gateway: `<name>-<concern>[-<rev>]`, and past 63 characters
// `<name>` is truncated and a 16-hex (64-bit) hash of the UNTRUNCATED whole
// name is appended.
//
// The suffix is 16 hex and not 8 for the reason design 03 §3.2 records: a
// chosen collision against 32 bits took 1.2 seconds when it was measured, and
// two crafted long names colliding in the suffix would map one Agent's
// resources onto another's. The hash covers the whole assembled name rather
// than only `<name>`, so two Agents cannot collide by differing only in a
// concern or a revision that the truncation kept.
//
// A name whose concern and revision alone leave no room to truncate is an
// ERROR, never a silent mangling — design 03 §3.2: "a suffix collision is a
// compile error naming both inputs, never a silent reuse", and a name truncated
// to nothing is the same failure one step earlier.
func EmittedName(name, concern, rev string) (string, error) {
	tail := "-" + concern
	if rev != "" {
		tail += "-" + rev
	}
	full := name + tail
	if len(full) <= 63 {
		return full, nil
	}
	const hashHex = 16
	keep := 63 - len(tail) - 1 - hashHex // 1 for the hash's separator
	if keep < 1 {
		return "", fmt.Errorf("cannot name an emitted resource for %q: the concern and revision "+
			"alone are %d characters, which leaves nothing of the name inside the 63-character "+
			"limit", name, len(tail))
	}
	sum := sha256.Sum256([]byte(full))
	return strings.TrimRight(name[:keep], "-") + tail + "-" + hex.EncodeToString(sum[:])[:hashHex], nil
}

// ServingRouteName is the object name of one Agent's serving route.
//
// It carries NO revision, and that is the design's shape rather than a
// simplification. Design 03 §3.2 lists "revision weights" as a property of one
// HTTPRoute and design 20's fallback row shifts `spec.rules[].backendRefs[]
// .weight` on "the serving HTTPRoute", singular — so a revision is a backendRef
// within one route, not a route of its own. A route per revision would put two
// routes on one hostname for the length of every rollout, and a listener merges
// them: the revision being rolled out would take a share of production traffic
// with no weight ever having been shifted, which is precisely the ungated
// window design 03 §3.3's ordering exists to close. Deleting the old route
// first trades that for a window with no route at all. One object whose
// backendRef is updated in place has neither.
func ServingRouteName(agentName string) (string, error) {
	return EmittedName(agentName, ConcernServing, "")
}

// AuthPolicyName is the object name of one Agent's `-auth` policy.
//
// Per Agent, so no `-<rev>` (§3.2): the policy follows the Agent's current
// spec and belongs to no revision (ADR-0034, F2). A candidate route's `-auth`,
// which is per revision and out of the slice, would be `<agent>-auth-<rev>`,
// and cannot collide with this one because this one carries no `-<rev>`.
func AuthPolicyName(agentName string) (string, error) {
	return EmittedName(agentName, ConcernAuth, "")
}
