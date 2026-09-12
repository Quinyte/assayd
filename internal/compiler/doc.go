// SPDX-FileCopyrightText: 2026 Quinyte
// SPDX-License-Identifier: Apache-2.0

// Package compiler is the start of design 03's policy compiler: the pure,
// cluster-free half of the first slice (§1.1).
//
// What it holds today:
//
//   - §3.2's naming rule, which the serving-route emitter in internal/controller
//     already uses;
//   - the `<agent>-auth` AgentgatewayPolicy of §3.4.4, rendered from an Agent,
//     and its digest (§3.3's `targetDigest` and `appliedDigest`);
//   - the `auth: none` route marker of §3.3.1.
//
// **The agent-operator emits what this renders**, through the `Create`
// transaction in internal/controller/authtxn.go: a new Agent's route is
// published only after its `<agent>-auth` enforces (§3.3.3). `Lock`, the
// abandonment of an unfinished transaction, the prepared re-create of a
// deleted route, and `Adopt`'s record are still owed (§1.1), so a route this
// operator did not publish through a `Create` is not governed by it.
//
// **One dependency is load-bearing here: cel.dev/cel-go** (Apache-2.0; the
// module was github.com/google/cel-go until v0.32.0 moved it). §3.4.2 requires
// a real CEL string encoder, and the CEL rule's group literal is printed by
// cel-go's unparser, so the rule's correctness rests on that unparser. It
// encodes a string literal with Go's strconv.Quote, which agrees with CEL's
// escapes only on valid UTF-8; admitGroupExpression refuses anything else, and
// TestTheGroupLiteralIsCELEncodedNotConcatenated proves the output with
// cel-go's own parser. An upgrade that changes the unparser must pass that
// test; it is the check that the encoder still round-trips.
package compiler
