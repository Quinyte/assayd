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

**Dispositions**: all 22 addressed in `5e3b043` (design 03 A7, design 02 A17). Round 3 then found that three of the fixes were applied at the point of edit and **not propagated** to the document the amended text points at — B1 to design 02 §3.1's schema comment, B3 to design 03 §3.4's rate row, and the alert/CLI consumers of the condition split. Two more (the asymmetric gate, the perturber registry) were mechanism-shaped rules that did not survive being built. See round 3.

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

## Round 3 (2026-08-26) — REVISE: 5 BLOCKER, 10 MAJOR, 10 MINOR

Verified by execution: the arithmetic re-derived in Go across 8 budgets × 8 replica counts, the `AgentSpec` type graph walked and enumerated, the condition test mutation-checked, every cross-design citation grepped. Two parallel verification agents corroborated independently.

**The diagnosis, in the reviewer's words: "The reasoning is converging. What is not converging is reach."** Three blockers were one-line edits in files the round-2 fix commit never opened — the amended paragraph was fixed and the document it points at was not. Two were rules whose shape did not survive being built.

| # | Sev | Finding | Disposition |
|---|---|---|---|
| 1 | BLOCKER | Six-digit grammar fixed in design 03; design 02 §3.1 — which design 03 calls the authoritative schema — still said `{1,7}` | Fixed |
| 2 | BLOCKER | Design 10's alert keys on the condition **type** and on profile, so every Agent pages on a stock P1 install; design 08 makes the same type terminal for `deploy` | Fixed — tier-absence split into its own condition type, `GovernanceSkipped` |
| 3 | BLOCKER | The 4.17% capacity overshoot fixed in §3.5, still live in §3.4's rate row | Fixed |
| 4 | BLOCKER | The asymmetric `egressAllowlist` gate is not expressible in `revisionHash(spec)`, and `{A}→{A,B}→{B}` launders an ungated widening onto the serving revision | **Reverted** — A18. The premise was also false: design 27 §5 ships the allowlist as a pack filter, not by editing every Agent |
| 5 | BLOCKER | `gateway.enabled` "default `true` at core tier" reproduces the never-`Ready` fleet on the P1 chart | Fixed — bound to the subchart's `dependencies[].condition`; P1 ships `false` explicitly |
| 6–15 | MAJOR | Backend acceptance never verified; `-ratelimit` keyed on a budget field falsifies design 25's always-compiles guarantee; `internal/*` fallback fails its own reachability check; the perturber registry named three types that need no perturber and missed the one drift that occurred; the no-stall claim false against `MaxConcurrentReconciles: 1`; `GovernanceSkipped` vs the per-class objection; no teardown on `true → false`; `CRDsAbsent` scoped to cold start; §8 still promised the impossible property test; round 2 had no dispositions | All fixed |
| — | MINOR ×9 | four wrong-section citations, stale A2 and A4 provenance text, `floor` over-enforcement unsignalled, map-graph ambiguity, list indentation | All fixed. **`NOTES.txt` was recorded fixed here and was not** — it did not exist and no design owed it; round 4 caught the contradiction between this row and `a8be2ef`'s own NOT DONE list, and design 07 §3 now owes it |

**What the verification agents added beyond the review**: only **one** type in `AgentSpec`'s graph carries unexported state (`resource.Quantity`); `metav1.Time` and `intstr.IntOrString` are not in the graph at all. Measured `k8s.io/api` drift v0.28→v0.36 is +1 type / +5 leaves, and the one arrival (`FileKeySelector`) has all-exported fields — so a registry keyed on unexported state would have stayed silent on exactly the regression A12 rule 2 records as having shipped. The **path** walk is what catches drift. Separately, adding `SystemPrompt` to `LLMSpec` left the entire unit suite green, confirming `TestEveryFieldIsClassified` walks only nine top-level fields.

## Round 4 (2026-08-26) — REVISE: **0 BLOCKER**, 5 MAJOR, 6 MINOR

Scoped to whether `a8be2ef` landed everywhere and introduced anything new. Verified by an independent reflection walk of `AgentSpec`'s type graph and by grepping every restatement of each retracted claim corpus-wide, commit messages included.

**"The substance has converged."** All five round-3 blockers landed; no surviving restatement of four of the five retracted claims anywhere; the condition split reaches every consumer; the arithmetic is right; and four of five type-graph measurements reproduce exactly under an independent walk — including "exactly one unexported-state type", the claim the registry design rests on. No new design defect.

What had not converged was still **reach**, now narrower: five of the eleven findings were the amended paragraph fixed and the sentence beside it, the ADR that restates it, or the index row not opened.

| # | Sev | Finding | Disposition |
|---|---|---|---|
| 1 | MAJOR | A17's bullet still stated the retracted asymmetric gate in the present tense, unmarked, asserting the false design-27 premise as fact | Fixed — struck and marked RETRACTED BY A18 |
| 2 | MAJOR | "design 27 §4" is wrong (§5 pack contents, §3 admission) — a **new** wrong-section citation, minted by the commit whose message says it corrected four | Fixed at all sites |
| 3 | MAJOR | §3.3 step 2's scope sentence said "Backends included" while its three normative clauses still said "policy" — the round-3 pattern reproduced *inside the paragraph the fix opened* | Fixed; also flagged that Backend `Accepted` status has no research citation |
| 4 | MAJOR | ADR-0027 and design 02 A15 both said §3.1 declares 25; it declares 26 — the **third** consecutive wrong version of the sentence whose only job is to state what is enforced | Fixed by deleting the literal from both; §3.1's list is now the sole statement |
| 5 | MAJOR | `NOTES.txt` and `gateway.enabled` asserted as existing chart mechanisms; neither exists and no design owned either. The review file recorded `NOTES.txt` "fixed" while the same commit's NOT DONE said it does not exist | Fixed — design 07 §3 now owes both; this file's row corrected |
| — | MINOR ×6 | owed-list short by one condition; `GovernanceSkipped` unclassified normal/abnormal-true; the 57-leaf figure does not reproduce; the `(enabled × CRDs)` table called total but silent on transitions; README's design 02 row unopened; a miscount in this file | All fixed |

## Codex cross-family review (2026-08-27) — REVISE: 7 BLOCKER, 7 MAJOR

`reviews/03-codex-review.md`, commit `dd6f10c`. Reviewed at snapshot `bcc7a98`; design 02 A20–A21 landed while it ran and were deliberately excluded rather than allowed to move the target.

**It found seven blockers where four same-family rounds had converged to zero, and the reason is structural, not diligence.** The Claude rounds checked the amendments against themselves — internal consistency, propagation, claims-vs-diff. Codex checked them against **pinned upstream agentgateway source and the published 2.2 API**. Round 4's "the substance has converged" was true about internal coherence and silent about whether the external contract holds. This is the cross-family independence `agent-protocol.md` exists to buy, and it paid for itself in one pass.

### Independently verified from primary sources by the implementer

Not accepted on the reviewer's word. Each checked against the pinned commit `53f3952` or the published docs:

| Claim | Verdict | Primary evidence |
|---|---|---|
| A policy targeting an absent route reports `Accepted=True` | **CONFIRMED** | Fixture `http-route-missing-not-attached.yaml` returns `output: []` — **no policy emitted at all** — with `Accepted: True / reason: Valid / "Policy accepted"` beside `Attached: False / reason: Pending / "Policy is not attached: HTTPRoute default/missing not found"` |
| The token limiter cannot stop the crossing request | **CONFIRMED, in the CRD's own doc comment** | `LocalRateLimit.Tokens`: *"token counts are not known until the request completes. As a result, token-based rate limits will apply to future requests only."* The standalone docs restate it: *"the response is still returned to the user. Only subsequent requests are rate limited."* |
| `tokens`/`burst` are `int32`, and `burst` is additive above the base | **CONFIRMED** | `Requests`, `Tokens`, `Burst` are all `*int32`. `Burst` is *"Allowance of requests above the request-per-unit"* |
| Design-admissible inputs overflow the emitted field | **CONFIRMED BY EXECUTION** | Max six-digit budget at the cheapest admissible price → `rate = 11,111,111,111,100` and `burst = 39,999,999,999,960,000`; int32 max is 2,147,483,647. The largest representable `tokensPerDay` is ~1.93×10¹⁴ |

**BLOCKER 1 is the deepest and is structural.** §3.3 waits for `Accepted=True`, and the design *deliberately creates routes last* — so the policy's target is guaranteed absent when the barrier runs, and the fixture shows exactly that state satisfying the barrier while emitting nothing. The fail-closed apply protocol proves a policy **parsed**, never that it **attached**. `Accepted=True/programmed` in the design text: the slash concealed a missing contract.

### One constraint the review did not name

`LocalRateLimit` carries `+kubebuilder:validation:ExactlyOneOf=requests;tokens`, and `Unit` is `+required` (`Seconds|Minutes|Hours`). A single policy cannot express a request limit and a token limit together, which §3.5's "one `-ratelimit` policy at the minimum of the two derived rates" happens to survive (both are token rates) but which the emitted-shape mapping must state.

### Consequences beyond a fix pass

- **ADR-0020 decision (4) is false as written.** Two-tier budgets rest on the gateway being a conservative tier that "cuts early, never late"; the pinned API says it cannot. AGENTS.md requires a **superseding ADR**, not an amendment. A path exists — the gateway's `tokenize: true` estimates tokens pre-dispatch and admits only if the estimate fits — but that is a different mechanism with a different guarantee, and it must be verified before it is promised.
- **BLOCKER 5 crosses into design 04.** The "exact" tier prices from the same ConfigMap as the approximation, and design 04 already names the field `usd_est`. A common mutable estimate cannot be the backstop for itself.
- **Design 03 should not be implemented from A1–A9.** The reviewer's instruction, and it is right.

### Process finding, recorded because it is the reusable one

`docs/research/agentgateway-2.2-2026-08.md` opens with **"re-verify before implementation"**. The implementer flagged that in the first message of the session and never ran `/research-latest`. Four internal rounds followed, each finding real defects, none able to find these — because none looked outside the corpus. At least three of the seven blockers turn on facts about a dependency that the corpus asserted from a note explicitly telling the reader to re-check it.

## Standing disagreement, unresolved by design

Round 1 MAJOR 6 (do not withhold `Ready`) versus Codex MAJOR 1 (make `Ready`
depend on gateway readiness). Different model families, opposite recommendations.
Per `agent-protocol.md`, the disagreement is recorded rather than reconciled. The
user decided in favour of the withhold; round 2 BLOCKER 3 shows the decision has
a consequence neither reviewer priced — on a default install, nothing is `Ready`.
