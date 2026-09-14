# Critique: design 16 A11 — the first slice (§1.1), consolidated

- **Date**: 2026-09-14
- **Target**: `docs/designs/16-evalsuite.md` §1.1 and amendment A11, at `ee3eb1c` (branch `design-16-first-slice`), read whole and as `git diff 729c920 ee3eb1c`. A11 closes the six minors of `reviews/16-a10-critique.md` (PASS: 0 BLOCKER, 0 MAJOR, 6 MINOR). The earlier records are `reviews/16-a3-critique.md` to `reviews/16-a10-critique.md`.
- **Independence**: an independent session. It wrote none of A4 to A11 and none of the eight earlier critiques. A forked first pass ran with the `critique-design` skill. Every claim it made was traced again by hand before it was counted. Where the two passes differ, this record says so and gives the reason.
- **Read against**:
  - `internal/controller/authforeign.go`: `foreignTrafficPolicies` (`:37-69`), its `listPolicies(..., true)` call (`:43`), the early return on an empty list before `servingRouteLabels` (`:47-50`), `listPolicies`' NotFound branch (`:92-94`), `servingRouteLabels` through the cached `r.Get` (`:107-121`).
  - `internal/controller/authabove.go`: `stands()` (`:101-103`), `gatewayAuthPolicies` (`:199-247`), and its `listPolicies(..., false)` call (`:221`).
  - `internal/controller/authtxn.go`:
    - `gatewayStep` (`:584-618`): `requireFreshAgent` (`:606`), and the foreign list, which returns on any error before `authStep` (`:611-615`);
    - `persistStatus` (`:1017-1032`), which goes through `writeStatus`;
    - `runLock`: the re-creation branch (`:1603-1613`), and the `ProbingAfter` step (`:1723-1795`), which reads the Gateway and probes on every pass with no time gate.
  - `internal/controller/httproute.go`: `currentServingRoute` (`:297-311`), `routePublished` (`:334-341`).
  - `internal/controller/agent_controller.go`: the one `ensureService` call, for the desired revision under `pin == nil` (`:574-575`); `SetupWithManager`'s `For(&Agent{})`, with no predicate (`:1825`).
  - `internal/controller/card.go`: card validation, which needs any interface at a supported version (`:216-235`); `cardFetchDue` (`:327-341`); `recordCardAttempt` (`:385`).
  - `test/envtest/authabove_test.go`: `policiesUnlistable` (`:491-503`).
- **Method**:
  - A sentence-by-sentence map of A10's "Who runs the eval" (A10 `:88-146`) onto A11's (`:88-188`).
  - A mechanical diff of the test table: 63 rows in each, with only `:504` to `:507` changed.
  - Every code claim A11 adds, traced to the source.
  - Nothing was run or mutated, because nothing in the slice is implemented.

**Verdict: FAIL — 0 BLOCKER, 2 MAJOR, 6 MINOR.**

**MAJOR 1 comes from A11's one intended rule change.**
- The clause-5 result now lives in `status.eval.blocked`, a slot two run-start reasons already share.
- The new text is written as if clause 5 owned the slot.
- In the same place, the consolidation dropped the one explicit bound that the change breaks.

**MAJOR 2 dates from A4 and A5, and all eight earlier critiques passed over it.**

The rest of A11 is right:
- m1, m3 and m4 are closed exactly.
- The consolidation keeps every rule, exit, non-exit and pinned row, apart from the cost bound in MAJOR 1.
- m6's reason for dropping row `:507`'s first half is true for the case the row named.

## Closure of the eighth critique

| Finding | A11's answer | Closed? |
|---|---|---|
| m1: clause 5 governs new claims only | `:114` scoped to new claims and Q12 (a); `:140`; `:177` | **Closed.** Q12 (b)'s own text (`:445`) releases the eval past the deadline "whether or not a hold stands", so the scoping is exact |
| m2: which controller evaluates clause 5 | option (a): the eval controller writes `blocked`, and the gate branch reads it (`:132`, `:185`, `:263`, `:270`, `:280`); rows `:504`, `:505` | **Closed as a choice.** Its text opens MAJOR 1 and m2 to m3 below. "Only on an eval pass" (`:133`) is now true |
| m3: NotFound on the foreign list | `:131`; row `:505`'s variant: Forbidden or 5xx, run namespace only, the Gateway namespace's list succeeding | **Closed.** `listPolicies(..., true)` reads NotFound as empty (`authforeign.go:43`, `:92-94`); the Gateway half lists with `false` (`authabove.go:221`). `policiesUnlistable` returns NotFound and is scoped to the Gateway's namespace (`authabove_test.go:491-503`), exactly as the row says. The injection timing is m5 |
| m4: the reason covers served-Agent incidents | `:96-100`; Q12 (`:446`) | **Closed.** The rule is for recorded transactions; W1 and a foreign policy on a served Agent are named as evaluated and promoted |
| m5: readability | three tables and subheadings; history moved to the A11 log (`:712-717`) | **Closed, with one cost lost.** See the audit below and MAJOR 1 |
| m6: rows `:471`, `:472` | row `:506` on a written claim and a held missing-policy `Lock`; row `:507` keeps the deleted-Service half; fallback scoped (`:167`) | **Closed for the rows.** `ensureService` has one call, for the desired revision (`agent_controller.go:575`). The no-`backendRef` route is swapped to `recreateRoute` in the same pass (`authtxn.go:1607-1608`, `routePublished`), so the dropped half's reason is true. `:167` overreaches for a read error (m4) |
| Noted: what wakes the eval controller | `:115` | **Partly.** It cites a backstop requeue that no waiting pass returns (m1) |

## The consolidation audit (m5)

Each A10 sentence in "Who runs the eval" was mapped onto A11's text.

| A10 content | Where it is in A11 | Survives? |
|---|---|---|
| Serialisation: the resourceVersion bump, `writeStatus` and `persistStatus` with full Updates, a lost pass or probe observation | `:95` | yes |
| No claim while a `Create` or `Lock` is recorded, at every stage named; abandonment counts; `EvalWaitingForAuth` meanwhile | `:92`, `:94`, table `:106-110` | yes |
| Refused `Adopt` does not hold; standing record; `writeStatus` early return; the route stays unauthenticated, as design 03's `Adopt` leaves it | table `:112` | yes. "It stays there until the Agent is recreated, or until K2" is dropped. It is a fact of design 03, not a rule here (m6) |
| A transaction held indefinitely holds the eval; the two cases; paged; Q12's message | `:114` | yes, now scoped to new claims and Q12 (a) (m1's fix). "The candidate holds meanwhile, which is fail-closed" is dropped as a phrase, and its meaning stands |
| A held J2/K2 `Lock` is not exempt before or after its deadline | table `:110`, clause 5, `:129`, `:139`; Q12 `:446` | yes |
| The exemption's premise and its five clauses | `:119`, table `:121-127` | yes |
| Clause 5's reads, `stands()`'s three cases, fail-closed inputs | `:131`, the table's "Fails closed on" | yes, corrected for NotFound (m3) |
| "A `Lock` held this way is an open incident…" (clause 5's reason) | removed | removed on purpose, by m4's fix. Clause 5 is left with "a hold stands on it" (`:110`) as its reason. Noted below |
| "Why not a condition reason" | `:139`; the history went to the log | yes |
| The cost of clause 5: only on an eval pass that met 1–4; the three reads; uncached; RBAC; the owed move | `:133-138` | yes, made more exact: the route read happens only on a non-empty list (`authforeign.go:47-50`), through the cached client; "bodies unchanged" |
| While all five hold, claims as if no transaction; Q12 does not govern; `EvalRunning` | `:129` | yes |
| Abandonment superset: exempt, no deadline, safe to re-run, delayed by at most one 30 s re-check; overrides "abandonment counts" | table `:111` | yes. "It does finish … once the finalizer lets go" is dropped. It supports the rule, and the rule's reason stands without it (m6) |
| N4's contention: two patches per case, one per run start, the verdict adds none, **"So an eval costs the `Lock` at most two probe passes per case, and one per run start"** | `:142` | **The patch count survives, and the explicit probe-pass bound is gone.** A11 also adds "A change to `blocked` adds one", which ties the bound to how often `blocked` changes. MAJOR 1 shows that is unbounded as `:132` is written |
| What it buys; a pin or gate removal also ends the wait | `:143` | yes ("wedge" becomes "wait", which is more accurate for the exempt state) |
| The missing-policy wedge: not exempt; which revision (R); the state; the conditions; the candidate's message; the stale pass; exits naming; R's name; fallback | `:145-167` | yes. The fallback is newly scoped (m4) |
| The working exit; who can take it; its cost; unbounded under a hold or foreign policy; a `Create` meanwhile keeps the eval waiting | table `:156-157`, `:162` | yes |
| Leaving it standing; the garbage-collection outage after D, E, F; the same on an ungated Agent; design 02's | table `:160` | yes |
| Not exits: removing the gate; a pin | table `:159` | yes |
| The transient exit: revert to R; `CardRetryInterval`; the promoting-pass case | table `:158` | yes |
| The wedge is design 03's; the amendment that would remove it; Q13 | `:168` | yes |
| The case in flight: new claims only; one pass once; the verdict in the in-flight record; `Interrupted` waits | `:170-176` | yes. "…and it is not a second outcome beside the one in flight" is dropped, and "at most one outcome" (`:174`) carries it |
| The shared predicates, the generation clause the eval controller's alone | `:185` | yes, plus clause 5 is now the eval controller's alone (m2) |

**The moved history is accurate in the A11 log (`:712-717`), with one nit.** A10's body said A9's citation was wrong because design 02's row "is about run-namespace logs". The log now says A9 "cited design 02's read-access row for run-namespace objects". That repeats A9's own wording and drops the reason it was wrong (m6).

Two other history sentences were dropped from the body without being moved:
- A8's abandonment reason, corrected by A9;
- A7's "one pass per outcome" and its card-re-read exit.

Both are already recorded in the A8 and A9 log entries, so nothing is lost.

## MAJOR 1: `blocked` is one slot for three reasons, and A11's clause-5 text treats it as clause 5's own

`16-evalsuite.md:132`, against `:129`, `:142`, `:193`, `:263`, `:280`, `:286`.

**What the text says.**
- `:263`: `blocked` is one `{reason, message}`, written for three reasons: `EvalAwaitingCard`, `EvalBindingUnsupported`, and now `EvalWaitingForAuth`. "Each reason is cleared when it stops holding, and the first two also when a run starts or resumes."
- `:132`: the eval controller "clears `blocked` when none stands, and writes only when the value changes". In the exempt state, the gate branch "sets `EvalWaitingForAuth` when `blocked` records the hold, and `EvalRunning` otherwise".

**The state is reachable.**
- In the exempt state cases are claimed "as if no transaction were recorded" (`:129`), so a run starts.
- At run start, step 2 can refuse the card and write `EvalBindingUnsupported` or `EvalAwaitingCard`, and "the next pass tries again" (`:193`).
- Card validation accepts a card if any interface declares a supported version (`card.go:216-235`). So a card whose only 1.0 interface is JSON-RPC, or whose HTTP+JSON URL carries a query, is recorded and meets the shared predicate. Run start then refuses it.

**Read literally, it loops without bound.**
- Each eval pass in that state meets clauses 1 to 4, finds no hold, and clears `blocked`. That is a real change from `EvalBindingUnsupported`, so it is one patch.
- The run start then writes `EvalBindingUnsupported` again, a second real change.
- The next pass starts from `EvalBindingUnsupported` and repeats.
- Both controllers watch `For(&Agent{})` with no predicate (`agent_controller.go:1825`; step 9 for the eval controller), so every patch re-enqueues both.
- Each Agent reconciler pass at `ProbingAfter` makes these calls, none of them gated by time (`authtxn.go:606`, `:611`, `:1723-1747`):
  - `requireFreshAgent`;
  - the uncached foreign list;
  - an uncached Gateway `get` and list;
  - an anonymous probe through the gateway.
- So the loop becomes a continuous probe stream on the Agent reconciler's single worker. That is the cross-tenant cost Q1 exists to remove (`:90`).
- It is also the bound the consolidation dropped: A10's "an eval costs the `Lock` at most two probe passes per case, and one per run start" (A10 `:112`).

**Read the other way, it is loud and wrong.**
- An implementation that computes one `blocked` per pass and writes once does not loop.
- But `:132` then tells its gate branch to show `EvalRunning` in the exempt state whenever `blocked` is not the hold, and that includes `EvalBindingUnsupported`.
- Nothing is claimed, and the condition names a run. That is the rule-8 outcome `:132` itself names.
- The table gives the right answer (`:280` does not apply, `:286` does), so `:132` contradicts `:286`.
- `:129`'s "The Agent reads `EvalRunning`" dates from A7, and it had the same gap. A11 turned it into the gate branch's instruction.

**A stale `EvalWaitingForAuth` has no clearer.**
- Clause 5 is evaluated only on passes that meet clauses 1 to 4 (`:132`).
- Run start clears only "the first two" reasons (`:263`).
- So nothing clears the hold record when the exempt state ends another way:
  - a spec edit abandons the `Lock`, and the new generation's run starts;
  - `recreateRoute` displaces the `Lock`;
  - the gate is removed.
- `status.eval.blocked`, whose meaning is "no case can be sent" (`:263`), then names a hold beside a run that is sending cases.
- The condition stays right, because `:280` applies the field only to a `Lock` that meets clauses 1 to 4.
- The next J2 or K2 `Lock` to reach `ProbingAfter` then shows the old hold's text as its cause until the eval controller's first clause-5 pass. That is loud and wrong, briefly.

**No row sees any of this.**
- Row `:513` builds the run-start reasons with no `Lock` recorded, so clause 5 never runs.
- Rows `:500`, `:504` and `:505` use a candidate whose card has a usable HTTP+JSON interface.

**Fix.**
1. Clause 5 writes and clears only its own reason: it clears `blocked` only when `blocked.reason` is `EvalWaitingForAuth`. Alternatively, state that the eval controller computes one `blocked` per pass, with clause 5's hold taking precedence, and writes it once.
2. The eval controller clears a standing `EvalWaitingForAuth` on any pass that does not meet clauses 1 to 4.
3. At `:132` and `:129`, replace "`EvalRunning` otherwise" with "otherwise the table's rows apply, from `blocked` and the run".
4. Restore the bound at `:142`: at most two probe passes per case, one per run start, and one per change of clause 5's result.
5. Add a row: a J2 `Lock` in the exempt state whose candidate's card is valid but has no usable HTTP+JSON interface. It reads `GatesPassed=False/EvalBindingUnsupported`, and the Agent's resourceVersion does not change over 10 s.
   - Mutation: clear `blocked` unconditionally in clause 5. The resourceVersion bound catches it.
   - Mutation: "`EvalRunning` otherwise". The reason catches it.
   - Mutation: leave a stale `EvalWaitingForAuth` after the `Lock` is abandoned. Assert that `blocked` is empty once the next run starts.

*The fork graded this MAJOR as a loop "built to `:132`". This record keeps MAJOR on two grounds: the loop follows from the literal sentence, and the other reading still contradicts the table. The dropped bound is folded in here because it is the consolidation's one vanished cost, and this change is what breaks it.*

## MAJOR 2: `cases[]` is kept "in suite order", but the suite digest ignores order, so a reorder mid-run can re-send one case and never send another

`:86`, `:209`, `:210`, `:214`, `:216`, `:265`. The text is unchanged since A5, so this finding is not new to A11.

**The design makes a reorder a no-op for the digest.**
- The digest sorts cases by `id`, and "Reordering cases does not" change it (`:86`).
- So a reordered suite is the same pair, and the run continues.

**But the record is positional.**
- `cases[]` is kept "in suite order" (`:265`).
- A claim writes `cases[k]` (`:214`), and the record checks the claim "at `cases[k]`" (`:216`).
- A paused run "resumes at the next unrecorded case" (`:210`), and the verdict comes "after the last case" (`:209`).
- Nothing says that the next case is chosen by `id`, or that the verdict needs one entry per suite id.
- The only id-keyed wording is row `:477`'s mutation, and that concerns spec edits across revisions.

**The scenario.**
1. The suite is `[a, b, c]`. `cases[]` records a pass for `a` and a pass for `b`.
2. The owner reorders the suite to `[c, b, a]`, for example by sorting the file.
3. A position-keyed build sends `suite[2]`, which is now `a`, a second time.
4. Its record is the last case's, so it carries the verdict: 3 of 3, `Passed`, under a digest that includes `c`. `c`, perhaps the one failing case, never ran.

**What it breaks.**
- Step 10's at-most-once, which exists because a re-sent case is "two tasks, with two sets of tool calls and spend" (`:220`).
- Row `:471`'s "never re-sent after its outcome is recorded".
- What the verdict proves (`:313`).

**The window is not small.**
- A paused run has no deadline (`:210`), and a run slowed by k slow tenants takes O(k) longer (`:90`).
- This is not a new attack: a suite writer can already delete `c` (Q6). The harm falls on an honest owner, and on the record the gate keeps.

**No row sees it.** Rows `:478` and `:482` resume mid-run, and row `:492` counts hits, but none of them reorders the suite.

**Fix.**
- Keep `cases[]` in the digest's canonical order, sorted by `id`.
- The next case is the first id in that order with no entry.
- The verdict requires exactly one entry per suite id.
- Add a row: reorder the suite after the first of three cases. The stub sees each case exactly once, and the verdict counts all three. Mutation: pick the next case by position.

## MINOR

### m1 — MINOR: the wake sentence cites a requeue no waiting pass returns

`:115`, against `:186`, `:211`.

- `:115` says the `For(&Agent{})` watch wakes the controller, "and the controller's backstop requeue (step 9) covers the rest".
- Step 9 returns `RequeueAfter: 5s` only after a case (`:211`). `:186` adds a `CardDriftInterval` requeue only while a verdict is final.
- No requeue is stated for a pass that waits on a transaction or on clause 5.
- **The gap.**
  1. Clause 5's own uncached read fails transiently while design 03's reads succeed. For example, a 5xx reaches the eval controller's list, and the `Lock`'s list gets through.
  2. The eval controller writes the hold to `blocked`, which wakes it once. The retry gets the same error text, so nothing is written and nothing wakes it.
  3. Design 03 saw no hold, so its status does not change.
- **What actually wakes it:** the candidate's card drift re-read, which stamps `fetchedAt` every `CardDriftInterval`, 5 minutes (`card.go:327-341`). That is incidental.
- **Fix.**
  - State a requeue for every waiting pass, for example `AuthProbeInterval`, and point `:115` at it.
  - Cost clause 5's reads at that rate in `:133`.

### m2 — MINOR: the lag is stated but not bounded, and in that window the condition shows the rule-8 outcome `:132` forbids

`:132`, rows `:504`, `:505`.

- **An empty `blocked` means two things:** "clause 5 found no hold", and "clause 5 has not run yet for this `Lock`".
- **The window.**
  - The J2 `Lock` reaches `ProbingAfter` with a hold standing.
  - Until the eval controller's first clause-5 pass, the gate branch shows `EvalRunning` while nothing is claimed.
- **Sends stay fail-closed.** Clause 5 is read afresh before each claim (`:140`), and after an operator restart `blocked` persists in status. So only the condition is wrong.
- **"By one write" is not a time bound.** The eval controller has two workers, and a slow tenant holds one for up to `caseTimeoutSeconds` (`:90`). So the window can reach about k/2 × 10 s.
- **The rows can fail a correct implementation.** Rows `:504` and `:505` assert `EvalWaitingForAuth` "across that interval". Built the natural way, with the policy in place before the `Lock` reaches `ProbingAfter`, a correct implementation can show `EvalRunning` at the start of that interval.
- **Fix.** Either:
  - (a) keep the gate branch at `EvalWaitingForAuth` in the exempt state until `status.eval` records that clause 5 was evaluated for this transaction, for example a `blocked`-adjacent field carrying the transaction's start time; or
  - (b) state the window as an accepted rule-8 lag, bound it, and start the rows' assertion once `blocked` first records the hold.

  (a) also kills the stale-`EvalWaitingForAuth` case in MAJOR 1.

### m3 — MINOR: the `EvalWaitingForAuth` message gives no source for the hold's cause

`:280`, against `:132`.

- The row says the message names "the transaction's kind and stage, and when its deadline passed".
- For the clause-5 source, the cause is the policy or read that holds, and only `blocked.message` carries it. The gate branch makes no Gateway reads.
- A gate branch built to `:280` would name a `Lock` at `ProbingAfter`, and not what to remove. Rule 8 requires the real cause.
- **Fix.** For that source, the condition's message carries `blocked.message` beside the transaction's kind and stage.

### m4 — MINOR: the fallback is reachable beyond the one stale pass when the route read fails

`:167`, row `:507`.

- `:167` says a route "that cannot be read, or has no `backendRef`" is "reachable only in the one stale pass", because afterwards the transaction is the `Create`.
- **True in two cases.** For no `backendRef`, and for NotFound, `runLock` swaps the `Lock` for `recreateRoute` (`authtxn.go:1607-1608`: `cur == nil || !routePublished(cur)`).
- **False for any other read error.** `currentServingRoute` returns that error (`httproute.go:301-305`), and `runLock` returns it (`authtxn.go:1604-1606`). The `Lock` stays recorded, and the fallback shows for as long as the error lasts.
- Row `:507`'s dropped half was the no-`backendRef` case, for which the reason holds. So the row is right, and the sentence is too broad.
- **Fix.** Scope `:167` to "has no `backendRef`, or is not found". State that a read that fails otherwise keeps the `Lock` and the generic text.

### m5 — MINOR: row `:505`'s variant does not say whose reader fails, or when

- `gatewayStep` lists foreign policies before `authStep` on every pass, and returns on an error (`authtxn.go:611-615`). Suppose the injected reader is the one both controllers share, as the owed move provides.
- **Injected before the J2 edit.** No `Lock` is ever recorded, a correct implementation claims cases, and the zero-hit assertion fails it.
- **Injected after entry but before `ProbingAfter`.** The `Lock` stalls before `ProbingAfter`, and clause 2 keeps the eval off. The mutation "read a failed foreign list as no hold" then survives.
- **Fix.** Either inject into the eval controller's reader only, or inject once the `Lock` is recorded at `ProbingAfter`. Say which.

### m6 — MINOR: readability and small accuracy losses in the status text and the log

- **The Status line (`:3`) and `:14` still open with about 250 words of critique counts.** The same counts are in the README and in §14. A11 moved history out of the body, but not out of the first paragraph a newcomer reads.
- **The heading's "critique PASS" (`:12`) now sits over a rule change no critique has passed.** It describes A10. It should read "critique PASS at A10", or be updated with this record.
- **The A11 log (`:717`) drops the reason A9's citation was wrong.** The row was about run-namespace logs.
- **The consolidation dropped two facts that are not rules:**
  - a refused `Adopt` "stays there until the Agent is recreated, or until its owner edits it to `apikey` (K2)";
  - an abandonment "completes once the finalizer lets go".

  Both help a reader judge the rows `:111` and `:112` without opening design 03.
- **Fix.**
  - Status: "not approved; §1.1 critique PASS at A10; A11's changes under critique; awaits Q1–Q13; approval must amend ADR-0024". Move the counts to §14.
  - Restore the two facts, as one clause each, in their table cells.
  - Restore "about logs" in the A11 log.

## What was verified

- **The clause-5 reads match design 03's.** `stands()` is true in exactly the three cases `:131` lists (`authabove.go:101-103`). The NotFound split at `:131` is exact (`authforeign.go:43`, `:92-94`; `authabove.go:221`).
- **The cost precision at `:136` is right.** The route read happens only after a non-empty run-namespace list (`authforeign.go:47-50`), through the cached `r.Get` (`:110`).
- **"No field of `status.auth.transaction` records the hold" (`:139`) still holds.** `runLock` keeps the hold only in `unmet` and in the conditions (`authtxn.go:1726-1795`).
- **The `blocked` write does not conflict with the claim and nonce rules.**
  - It is a write to `status.eval` by its sole writer, under the same optimistic lock.
  - It is made before a claim, never between a claim and its record.
  - The record step's re-apply condition checks only `cases[k]`, so it is unaffected.
  - The one thing it adds to contention is the extra patch that `:142` counts.
- **Rows `:504` and `:505` kill the named mutation.** A gate branch that checks four clauses and shows `EvalRunning` while nothing is claimed fails the `GatesPassed` assertion, with the caveat on where the assertion starts (m2).
- **Row `:506` can be built.**
  - A missing-policy `Lock` is never exempt: clause 1 negates `isReCreation`.
  - The oracle can hold it at `ProbingAfter`.
  - It reaches `Served` once the oracle answers, provided R's card is recorded. The default build records it, and the row does not say so (noted below).
- **Row `:507` can be built.** `ensureService` has exactly one call, for the desired revision, when no pin is set (`agent_controller.go:574-575`). So R's deleted Service is not re-created once C is desired. Its message takes R from the `backendRef` (A10's m2, unchanged).
- **The Status line, the heading and the README row** each say that the eighth critique passed and the slice is not approved, and that it awaits Q1–Q13. That is accurate at `ee3eb1c`. The human's decisions are recorded in a separate commit, outside this critique.
- **Rule 7.** No new sentence makes a present-tense claim that something is enforced. Every new enforcement is stated as owed with the slice.
- **Rule 8.** The new condition text is the subject of MAJOR 1, m2 and m3.

## Noted, not counted

- **Clause 5's reason is now thin.** m4's fix removed "an open incident" as its reason, and the table gives only "a hold stands on it" (`:110`). The real reason is at `:143`: a hold still holds the `Lock` after a promotion, so exempting it earns nothing. One cross-reference would make that plain.
- **The gate branch's reasons still have no stated precedence** when several states hold at once, for example `EvalRecordNotKept` with a transaction recorded. A11 adds a source to that set.
- **Row `:506`'s `Served` assertion needs R's card recorded.** The default build records it, and the row does not say so.
- **"Writes only when the value changes" compares a message that embeds an API error's text** (`authabove.go:206`, `:223`; `authforeign.go:95`). An error whose text varies from call to call would write on every pass. m1's requeue would bound that rate.
- **Leaving designs 02 and 03 unchanged is deliberate** and is not counted.

## The smallest set of changes

1. **MAJOR 1:**
   - clause 5 owns only its own reason, or `blocked` is computed once per pass;
   - a stale `EvalWaitingForAuth` is cleared outside the exempt state;
   - "`EvalRunning` otherwise" is replaced at `:132` and `:129`;
   - the probe-pass bound is restored at `:142`;
   - the no-usable-interface exempt-state row is added.
2. **MAJOR 2:**
   - `cases[]` is kept in canonical id order;
   - the next case is chosen by id;
   - the verdict needs one entry per suite id;
   - the reorder row is added.
3. **m1 to m6**, as stated above.

**design 16 §1.1 (A11): FAIL — 0 BLOCKER, 2 MAJOR, 6 MINOR**
