# Design 03 amendments A1–A6 / design 02 A15–A16 — critique record

Two independent Claude critiques, forked context, `effort: xhigh`. Persisted per
`docs/agent-protocol.md`: "Findings are files. A finding delivered only as a
message is lost the moment a session ends." Round 1 existed only as a message
until round 2 flagged its absence as blocking approval — recorded here as the
protocol requires, including the findings whose recommendations were **not**
adopted.

## Round 1 (2026-08-25) — REVISE: 4 BLOCKER, 8 MAJOR, 7 MINOR

Reviewed A1–A5 as first drafted.

| # | Sev | Finding | Disposition |
|---|---|---|---|
| 1 | BLOCKER | Card-discovery route's "zero mandatory concerns" cited ADR-0019, which says only that the card's source of truth is the container. An agent at `visibility: cluster`, or with no `expose` block, would disclose name/version/skill inventory unauthenticated | Fixed — inherits A2A auth (round 2 MAJOR 12 finds the fix incomplete for the no-`expose` default) |
| 2 | BLOCKER | §3.3.1 claimed completeness the six-row table did not have; the promised "route-class constants == table rows" property test is not mechanizable — markdown is not a machine-readable artifact | Fixed — emitter registry + golden diff (round 2 qualifies: the backstop claim is still wrong) |
| 3 | BLOCKER | A1+A3 made P1 uncompilable (identity unavailable → candidate route's mandatory `-auth` → compile error for every Agent); §5 stated two incompatible blast radii | Fixed — absent-input vs unbuilt-producer split; failure route-scoped |
| 4 | BLOCKER | New design 01 amendment numbered A5, which already existed; two live citations misresolved | Fixed — renumbered A8 |
| 5 | MAJOR | ADR-0027's "verified by mutation" false for the condition test; mutation renaming `CondPricingStale` SURVIVED | Corrected in ADR text (round 2 MAJOR 9: the replacement claim is also false) |
| 6 | MAJOR | A15's `Ready` withhold cites `GatesBypassed=DevProfile`, an opt-in condition, to justify blocking on an environmental one; `GatesSkipped` is the true analogue and does not withhold. A13 already adjudicated `Ready=False` as worse | **NOT ADOPTED** — user decided to keep the withhold. Analogy removed as wrong regardless; carve-out re-argued on its own merits. Round 2 BLOCKER 3 raises the consequence again |
| 7 | MAJOR | The `local` carve-out has no plumbing (`--profile` does not exist) and no sunset | Both stated as owed in A15 |
| 8 | MAJOR | A3 silently reverses review 03 R2-a's recompile requirement; "blast radius zero" falsified by its own §5 row | Fixed — reversal stated, claim rewritten |
| 9 | MAJOR | A4 under-specified: no bucket capacity, no overflow bound, CEL claimed in present tense but absent | Addressed (round 2 BLOCKER 1 and MAJOR 4 find both fixes wrong) |
| 10 | MAJOR | `llm.fallback` has no slot in `PolicyIntent` at all, though design 20 depends on it | Fixed — A6 |
| 11 | MAJOR | `PricingStale` permanently true on any install older than a month | Narrowed (round 2 MAJOR 11: narrowing 1 unenforceable) |
| 12 | MAJOR | `ModelDrifted` also missing from the vocabulary; count should be 25 | Fixed |
| — | MINOR ×7 | erratum pointers; ADR-0027 self-refuting sentence; `CRDsAbsent` unspecified CRD set; no-auth marker policy-vs-label; §8.1 over-claim; a restated field count; `-guard`/ADR-0014 profile dimension | All fixed |

## Round 2 (2026-08-25) — REVISE: 4 BLOCKER, 12 MAJOR, 6 MINOR

Reviewed the revised A1–A6 plus A15–A16. Verified by execution: wrote and ran
the leaf walker A16 mandates, mutated the projection to test whether that shape
can be satisfied without closing its own hole, re-derived the overflow bound in
Go, and checked the registry backstop against the repo's actual lint config.

**BLOCKER 1** — §3.5 derives the int64 threshold (~9,223,372) and then states a
grammar admitting 9,999,999. `usdPerDay: "9999999"` wraps to a negative
`tokensPerDay`, and the guard tests `rate == 0`, so a negative rate escapes it.
Rule 7 verbatim, in the sentence claiming to prevent it.

**BLOCKER 2** — A16's mandated test shape goes green without working.
`resource.Quantity`'s value is unexported; the only reflect-settable leaf under
`Divisor` is `Format`. Projecting `Format` alone turns the leaf walk green while
Codex M4 (`Divisor: 1m` vs `1`) still reproduces identically. The reviewer's
independent sweep reproduced A16's seven-leaf table exactly and found no eighth
— the table is right, the enforcement is not. Also undefined: map-kinded leaves
(`ResourceRequirements.Limits/Requests`).

**BLOCKER 3** — §5 gives opposite treatments to one state. "Producing design not
installed ⇒ no condition, because it would fire on every Agent in every
core-tier cluster" and "Gateway CRDs absent ⇒ condition + withhold `Ready`" are
the same P1 state, since design 07 puts agentgateway at core tier. With
`values.yaml` defaulting to `profile: prod`, A15's rule makes **no Agent ever
reach `Ready` on a stock install**, and design 10's new alert row pages on it.

**BLOCKER 4** — "the poll runs off the reconcile worker" (added in `ec0a8d5`,
attributed to no amendment) removes the per-object serialization that is §3.3's
only ordering guarantee, reopening review 03 finding 1: reconcile N's outstanding
poll can apply old routes after reconcile N+1 installed new isolation policies.

**MAJOR 1–12**, abbreviated: aggregate-vs-leaf classification rules contradict for
`tools[].requiresApproval`; `P = 0` panics (design 25 permits explicit zero
pricing); `<provider>/<model>` split undefined and `revision.go:66` already
documents the slash hazard; `capacity = rate × 3600` overshoots the daily budget
by 4.17%, breaking the "cuts early, never late" invariant the same section
invokes; `plume doctor reports the tier gap` has no mechanism in design 08 §7;
the registry cannot express a Backend restriction, so ADR-0014's BAA control is
outside its stated extension point; classifying `egressAllowlist` as behaviour
makes a BAA change a fleet-wide eval cycle with no exemption for the urgent
(narrowing) direction; nothing requires `llm.fallback`'s endpoint to be inside
`egressAllowlist`; ADR-0027's erratum replaced one false claim with another
(declares 25, test enforces 21); `PricingStale` narrowing 1 unenforceable from a
table-wide `updated` with two writers; the default Agent (no `expose` block) has
no A2A route to inherit card auth from, so ADR-0027's four-line Agent fails to
compile.

**MAJOR 10 concerns this project's own process** and is recorded in full because
it is about the record: the `ec0a8d5` commit message says Codex "independently"
corroborated A15's `Ready` withhold. Codex's scope note says it accounted for
current A15 text, and its sentence is "Current A15 explicitly requires…" — a
restatement of the proposition in dispute by a reviewer that had read it.
`agent-protocol.md` names this failure exactly. Codex's *fix* does support the
withhold on the merits; its agreement is not independent of the text. Codex also
never adjudicated `phase: Pending`.

## Standing disagreement, unresolved by design

Round 1 MAJOR 6 (do not withhold `Ready`) versus Codex MAJOR 1 (make `Ready`
depend on gateway readiness). Different model families, opposite recommendations.
Per `agent-protocol.md`, the disagreement is recorded rather than reconciled. The
user decided in favour of the withhold; round 2 BLOCKER 3 shows the decision has
a consequence neither reviewer priced — on a default install, nothing is `Ready`.
