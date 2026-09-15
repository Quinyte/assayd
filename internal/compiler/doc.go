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
// **The agent-operator emits what this renders**, through the `-auth`
// transactions in internal/controller/authtxn.go (§3.3.3). A new API-key
// Agent's route is published only after its `<agent>-auth` enforces, and a new
// `auth: none` Agent's at once, with its marker (`Create`). A served
// `auth: none` Agent edited to `apikey` is locked in place by J2's `Lock`, and
// an `Adopt`ed Agent whose owner edits it to `apikey` by K2's (`enterLock`,
// `runLock`). A served API-key Agent's deleted policy is re-created by a
// `Lock` while its route stays published (`lockMissingPolicy`), and by the
// `Create` that re-creates a deleted or stripped route when it does not
// (`recreateRoute`). An unfinished spec-driven `Create`, or J2/K2 `Lock`,
// whose target mode no longer equals the desired mode is abandoned, and the
// only policy it deletes is the one it wrote (`abandon`). A route this
// operator did not publish is refused as `Adopt`, not governed, until that K2
// edit. Everything outside the first slice is specified, not approved, and
// absent: `Narrow`, `Loosen`, `Withdraw`, and every concern but `-auth`, so
// nothing here renders a rate limit, a tool filter or an AgentgatewayBackend
// (§1.1).
//
// **One dependency is load-bearing here: cel.dev/cel-go** (Apache-2.0; the
// module was github.com/google/cel-go until v0.32.0 moved it). §3.4.2 requires
// a real CEL string encoder, and the CEL rule's group literal is printed by
// cel-go's unparser, so the rule's correctness rests on that unparser. It
// encodes a string literal with Go's strconv.Quote, which agrees with CEL's
// escapes only on valid UTF-8; AdmitGroupsExpression refuses anything else, and
// TestTheGroupLiteralIsCELEncodedNotConcatenated proves the output with
// cel-go's own parser. An upgrade that changes the unparser must pass that
// test; it is the check that the encoder still round-trips.
package compiler
