# Critique: design 16 A12 — the first slice (§1.1), the tenth pass

- **Date**: 2026-09-16
- **Target**: `docs/designs/16-evalsuite.md` §1.1 and amendment A12, at `f420db4` (branch `design-16-first-slice`), read whole and as `git diff ee3eb1c f420db4`. A12 answers `reviews/16-a11-critique.md` (FAIL: 0 BLOCKER, 2 MAJOR, 6 MINOR). The earlier records are `reviews/16-a3-critique.md` to `reviews/16-a11-critique.md`.
- **Independence**: an independent session. It wrote none of A4 to A12 and none of the nine earlier critiques. Every code citation below was traced by hand in the tree at `f420db4`; nothing was taken from an earlier record without re-reading the source.
- **Scope**: the human's decisions on Q1–Q13 sit in a separate commit and are out of scope. Leaving designs 02 and 03 unchanged is deliberate and is not counted.
- **Read against**:
  - `internal/controller/authtxn.go`: `AuthProbeInterval = 5 * time.Second` (`:47-50`); `gatewayStep`, whose foreign list returns before `authStep` (`:609-615`); `enterLock`'s one-time deadline (`:981-985`); `recreateRoute`'s (`:1553-1555`) and `lockMissingPolicy`'s (`:1567-1569`); `persistStatus` (`:1017-1032`); `runLock`'s re-creation branch (`:1603-1613`) and its `else` branch, which calls `ensureServingRoute` and never `recreateRoute` (`:1615-1621`); the `ProbingAfter` step (`:1723-1795`); `reconcileServed`'s three `recreateRoute`/`lockMissingPolicy` call sites (`:1498-1533`); `abandons` (`:799-812`).
  - `internal/controller/agent_controller.go`: `writeStatus`'s early return on an equal status (`:1782-1785`); the one `ensureService` call, for the desired revision (`:575`); `authHoldsPromotion` (`:906-924`); `For(&Agent{})` with no predicate (`:1824-1828`).
  - `internal/controller/authabove.go`: `stands()` (`:101-103`); `gatewayAuthPolicies`' `listPolicies(..., false)` (`:221`).
  - `internal/controller/authforeign.go`: `foreignTrafficPolicies` (`:37-69`), its `listPolicies(..., true)` (`:43`), the early return on an empty list (`:47-50`), the NotFound branch (`:92-94`).
  - `internal/controller/httproute.go`: `currentServingRoute`, through the cached `r.Get` (`:297-311`); `routePublished`, any `backendRef` (`:333-341`).
  - `internal/controller/card.go`: validation, an ANY check over `supportedInterfaces[]` versions with no binding requirement (`:213-233`); `cardFetchDue` and its comment that "a card is not a per-reconcile input" (`:316-341`); `CardRetryInterval` 15 s (`:81`), `CardDriftInterval` 5 minutes (`:309`).
  - `internal/controller/conditions.go`: `GatesPassed` owned and sticky (`:71`, `:115-122`).
- **Method**:
  - Every sentence A12 added or replaced, traced to its source or to the rule it changes.
  - The `blocked` precedence walked against each reachable state, looking for a hold it hides.
  - The `cases[]` rewrite walked against add, remove, reorder and edit mid-run, and against a claim orphaned by a hard stop.
  - The three places the design enumerates `status.eval`'s new fields, compared with the schema table.
  - Each new or changed row built in the head, and each named mutation run against the rule it is meant to kill.
  - Nothing was run, because nothing in the slice is implemented.

**Verdict: FAIL — 0 BLOCKER, 2 MAJOR, 6 MINOR.**

Both of the ninth critique's MAJORs are closed on their own terms, and every minor is closed. The two MAJORs below are the seams the answers opened:

- A12 adds `status.eval.blockedFor` and makes the whole fail-closed reading rest on it — and then leaves it out of the list of what design 02 is owed, out of the CRD check that exists to catch a missing field, and out of the field count.
- A12 replaces two event-triggered rules ("after the last case", "the next unrecorded case") with two state predicates over `cases[]` — and nothing in the design clears `cases[]` when the suite digest changes, which is the one re-run path Q3 leaves open.

## Closure of the ninth critique

| Finding | A12's answer | Closed? |
|---|---|---|
| MAJOR 1: `blocked` is one slot for three reasons | `:133-142`: one computation per pass, precedence run-start refusal → clause 5 → empty, written only on a change; `blockedFor` (`:139`); the gate branch at `:140-141`; the bound restored at `:152`; rows `:526`, `:527` | **Closed.** See the audit below. The precedence hides no hold, there is no write loop, and both new rows kill their mutations |
| MAJOR 2: `cases[]` kept "in suite order" against an order-insensitive digest | `:86`, `:219`, `:220`, `:224`, `:227`, `:277`; row `:528` | **Closed as a rule.** A reorder is the only edit that leaves the pair unchanged, so keying on `id` removes the whole class. The residue is MAJOR 2 below, which is about what `cases[]` holds across pairs, not across positions |
| m1: the wake sentence cites a requeue no waiting pass returns | `:115`: every waiting pass returns `RequeueAfter: AuthProbeInterval`, 5 s; a failed clause-5 read returns the error to backoff. Costed at `:143` | **Closed.** The constant is `AuthProbeInterval = 5 * time.Second` (`authtxn.go:47-50`). Its unstated cost is m4 below |
| m2: the lag is stated but not bounded | `:139-141`, `blockedFor`, read fail-closed; rows `:516`, `:517` assert from the moment the `Lock` reaches `ProbingAfter` | **Closed for the exempt state**, which is what the rows assert. The outside-the-exempt-state half is m3 below |
| m3: the message gives no source for the hold's cause | `:292`: "the message also carries `blocked.message`, which names the policy or the failed read that holds" | **Closed** |
| m4: the fallback is reachable beyond the one stale pass | `:177` scoped to "has no `backendRef`, or is not found", with the other-error case stated | **Closed, and exact.** `currentServingRoute` returns NotFound as `(nil, nil)` and any other error as an error (`httproute.go:301-305`); `runLock` swaps to `recreateRoute` on `cur == nil \|\| !routePublished(cur)` and returns any other error (`authtxn.go:1603-1608`). The gate branch's own read is the same cached `r.Get`, so the two agree |
| m5: row `:505`'s variant does not say whose reader fails, or when | `:517`: the eval controller's reader only, once the `Lock` is at `ProbingAfter`, Forbidden or 5xx on the run namespace only, with the reason design 03's reader is left alone | **Closed, and the reason is right.** `gatewayStep` lists foreign policies and returns on any error before `authStep` (`authtxn.go:609-615`), so injecting there would stall the `Lock` short of `ProbingAfter`, where clause 2 already keeps the eval off |
| m6: the Status line, the heading, the A9 citation, the two dropped facts | `:3` (counts moved to the §14 table at `:748-758`); `:12`, `:14` carry no counts; `:732`; `:111`, `:112`; `:110`'s cross-reference; `:518`'s R-card note | **Closed.** One residue: the log's own row references, m6 below |

## The MAJOR 1 audit

Each branch of `:133-142` walked against the states it must cover.

| Question | Answer, traced |
|---|---|
| Does the precedence hide a hold? | No. Precedence 1 is guarded by "when this pass **would start a run**" (`:134`). A run starts only when the shared predicates hold (`:195`), which include the no-transaction predicate, from which an exempt `Lock` is excluded only if all five clauses hold, clause 5 included. So with a hold standing no run would start, precedence 1 is false, and precedence 2 fires. The ordering is safe because the guard, not the position, decides |
| Is there a write loop? | No. "writes it only when the computed value differs from the stored one" (`:133`), and the message is built "from the hold's stable causes, the policies' names and the class of a read error, and not from an API error's raw text" (`:138`), which also closes A11's noted item about `authabove.go:206`/`:223`'s embedded error text. `blockedFor` is written once per pair-or-`Lock` change (`:139`) |
| Is a stale hold cleared? | Yes, by the precedence itself: any pass on which clauses 1 to 4 fail computes empty and clears. That covers every exit, not only the three `:138` lists — the stage advancing past `ProbingAfter`, a card recorded for `status.activeRevisionDigest` (clause 4), and the hold simply clearing. One of the three it does list is impossible: m1 below |
| Is the restored bound true? | Yes as stated. A case is two patches (claim, record); a run start is one; the verdict rides in the last record (`:219`); `blockedFor` changes once per `Lock` in the exempt state. A flapping clause-5 read would patch per flap, which the bound admits as "one per change", and `:152`'s own "Each pass lost to these loses nothing that could have finished the `Lock`" makes that harmless |
| Row `:526` — buildable? | Yes. Card validation is an ANY check over `supportedInterfaces[]` protocol versions with no binding requirement (`card.go:213-233`), so a JSON-RPC-only 1.0 card validates and records a digest, and run start then refuses it for want of an `HTTP+JSON` entry (`:203`). The resourceVersion bound holds with the Agent reconciler running: `writeStatus` returns early on an equal status (`agent_controller.go:1782-1785`), and a `Lock` at `ProbingAfter` re-probes every 5 s writing the same `tx.Probe.After` (`authtxn.go:1741-1748`) |
| Row `:526` — mutations killed? | Both. "Clear `blocked` whenever clause 5 finds no hold" makes each pass clear and each run start rewrite, two real changes per pass, which the resourceVersion bound catches. "Show `EvalRunning` when `blocked` is not the hold" fails the `False/EvalBindingUnsupported` assertion |
| Row `:527` — buildable? | Yes. In the J2 row's state the recorded mode is still `none`, so editing `expose.a2a.auth` back to `none` makes `abandons` true (`authtxn.go:801-811`: `targetMode` `apikey` ≠ desired `none`, and the record is not a re-creation), and `none` compiles, so `authHoldsPromotion` raises no I1 hold (`agent_controller.go:911-914`). The edit is behaviour surface, so it mints the new candidate the row needs, and with no transaction recorded the standing Gateway-level hold does not stop the run (`:96-100`) |
| Row `:527` — mutation killed? | Yes. "Clear `EvalWaitingForAuth` only when clause 5 is next evaluated" leaves the hold standing, because with no `Lock` recorded clauses 1 to 4 never hold again, and the row asserts `blocked` is empty once the new run starts |
| Rows `:516`/`:517`'s new mutation | "Read an unmatched `blockedFor` as no hold" is killed. In the first moments `blockedFor` does not match, the mutated branch falls to the table's rows, and `:140` sends "no suite `id` has an entry" to `EvalRunning`, against an assertion of `EvalWaitingForAuth` |

## The `blockedFor` audit (finding MAJOR 1)

Where the design enumerates the new `status.eval` fields:

| Place | Lists `blockedFor`? |
|---|---|
| `:265` "gains **eight** fields", and the row at `:275` | yes |
| `:234` step 11, the Agent CRD schema check: "each of `suiteDigest`, `endpointPath`, `cardDigest`, `blocked`, `verdict`, `cases` (with `claim` in its items) and `failed`" | **no** |
| `:346` "In the slice": "`status.eval`'s **seven** new fields" | **no** |
| `:370` "What the slice depends on … Design 02": `suiteDigest`, `endpointPath`, `cardDigest`, `blocked`, `verdict`, `cases[]`, `failed[]` | **no** |
| `:233` the eval controller's read-back: `verdict`, `suiteDigest`, `endpointPath`, `cardDigest`, and every claim | no, and `blocked` is not there either — see m2 |

## MAJOR 1: `blockedFor` is the field the fail-closed reading rests on, and it is in neither the owed list nor the guard that would catch its absence

`16-evalsuite.md:234`, `:346`, `:370`, against `:265`, `:275`, `:139`.

**What A12 makes the field carry.** `:139` is the new fail-closed rule: "`blocked` applies only while `blockedFor` matches the current candidate's digest, the current suite's digest and the current `Lock`'s deadline. A `blockedFor` that is absent, or does not match, means that nothing has been evaluated for this state." `:140` turns "absent" into a condition: in the exempt state the gate branch sets `GatesPassed=False/EvalWaitingForAuth`, "whose message says the eval controller has not yet evaluated clause 5 for this `Lock`". `:292` repeats it in the table. So `blockedFor`'s absence is not a degraded mode — it is the permanent answer.

**What the three lists are for.** `:370` opens "What the slice depends on. **Every item is owed.**" It is the list from which design 02's `EvalStatus` is amended. `:234` is the enumeration the Agent reconciler's schema check is written from, and `:238` says of it "Either way the candidate holds, which is fail-closed", which is only true if the enumeration is complete. `:346` is the in-slice count.

**The failure.** Build design 02's amendment from `:370`. `blockedFor` is not added to the Agent CRD. Every eval-controller patch that carries it is pruned without an error — the same mechanism `:232` describes for the other fields. `blockedFor` then reads as absent on every pass, for ever. In the exempt state the gate branch takes `:140`'s first clause and reports `EvalWaitingForAuth`, "clause 5 has not yet been evaluated for this `Lock`" — for ever, on a `Lock` for which clause 5 is being evaluated every 5 s and finds nothing. The candidate never promotes, the `Lock` never finishes, and the Agent pages under a reason that names the wrong thing (rule 8). The guard that exists to name the right thing, `EvalRecordNotKept`, is written from `:234`, which does not check `blockedFor`, so it never fires.

**Why the schema table is not enough.** `:265`/`:275` describe the field; `:370` is what gets built, and `:234` is what gets checked. The project's own rule is that code diverging from an approved design is drift — so the list that omits the field is the one an implementer is held to. The same class of omission is what step 11 exists to survive: `:232` says an Agent CRD that predates the fields "prunes them without an error", and A12 has added a field to which that sentence now applies and no guard.

**Counter-argument, and why it does not hold.** One could say the slice ships one CRD version, so partial pruning cannot happen. That is exactly the argument step 11 refuses — `helm upgrade` never updates `crds/` (`:232`, `:385`), so the installed CRD and the running operator are routinely different releases, and a field added in one release against an operator-side rule added in the same release is the case the guard was written for.

**Fix.**
1. `:234`: add `blockedFor` to the checked fields.
2. `:370`: add `blockedFor` to design 02's owed list.
3. `:346`: "seven" → "eight".
4. Row `:524` already installs "an Agent CRD without the new `status.eval` fields"; say that the set includes `blockedFor`, so the mutation "omit `blockedFor` from the checked set" has a row that kills it.

## MAJOR 2: nothing clears `cases[]` when the suite digest changes, and A12 turned the verdict and the next-case pick into state predicates over it

`:219`, `:224`, against `:202`, `:233`, `:319`, `:277`.

**What A12 changed.** Two event-triggered rules became state predicates over `cases[]`:
- `:219`: "After the last case, `score` is passed ÷ total" became "**Once every suite `id` has exactly one entry in `cases[]`**, `score` is passed ÷ total (A12)".
- `:224`: "resumes at the next unrecorded case" became "the next case is the first suite `id`, in `id` order, that **has no entry in `cases[]`**".

**What clears `cases[]`.** Only a new generation. `:319`: "A new generation abandons the run. Outcomes recorded for the old digest are dropped with it, and the new candidate starts at its first case." `:227` repeats it for a claim: "A claim that no longer stands, because a **new generation** superseded the run, is dropped with the run." Step 1 (`:202`) says only that a run "starts"; the run-start write is enumerated at `:233` as `verdict: Running`, `suiteDigest`, `endpointPath` and `cardDigest`, and `cases[]` is not among what it touches. `:277` defines an entry as `{id, outcome, reason, claim}` — it carries neither digest, so a stale entry is indistinguishable from a fresh one.

**The scenario, which is the one Q3 leaves open.** Q3 (a) makes a suite edit the only re-run path: "a re-run always follows a visible edit of the spec or the suite" (`:418`), and `:219` says "a re-run is still possible … after any visible edit of the suite, which gives a new pair".

1. Suite `[a, b, c]` against candidate C. The run completes: `a` pass, `b` pass, `c` fail. `verdict: Failed`, the pair goes into `failed[]`.
2. The owner fixes `c`'s expectation. A new suite digest, so a new pair, not in `failed[]`.
3. Step 1 starts a run: the run-start write sets `suiteDigest` to the new one, `verdict: Running`, `endpointPath`, `cardDigest`. `cases[]` still holds `a`, `b`, `c` from the old suite.
4. Step 10's pick: "the first suite `id` … that has no entry in `cases[]`". Every id has one. **No case is sent.**
5. Step 7's predicate: "every suite `id` has exactly one entry in `cases[]`" is already true. Read as a predicate, the verdict is recomputed from the previous suite's outcomes — 2 of 3, `Failed` — and the new pair joins `failed[]`, permanently refusing the edit that was meant to re-open it. Read with `:219`'s second clause ("written in the same patch as that case's record"), no record ever happens, so the run stays `Running` for ever and the Agent reads `EvalRunning` while nothing is sent.

Either branch breaks the slice's only escape from a failed pair. The second branch is also the rule-8 outcome `:132` names.

**Why this is A12's to close even though the omission predates it.** Under A11 the verdict was triggered by a record, and "the next unrecorded case" had the same hole in the pick. A12 made the verdict a predicate over the same unbounded record, so the hole now writes a verdict as well as stopping the run. And `:224` is exactly where a reader of A12 looks for the rule. The ninth critique held A11 to this standard on its own MAJOR 2 — "Nothing says that the next case is chosen by `id`" — and the same standard applies here: nothing says `cases[]` belongs to one pair.

**No row sees it.** Row `:487` ends "An edit of the suite then starts a run", and its mutations are about `failed[]`. Row `:497` asserts that a suite edit under a held candidate "starts a new run within one `CardDriftInterval`", not that any case is re-sent. Row `:528`, the new reorder row, keeps the pair unchanged by construction, so it never crosses a digest change.

**Fix.**
1. State at `:202` or `:233` that the run-start write clears `cases[]` and `verdict` for the new pair, in the same patch that records `suiteDigest`, `endpointPath` and `cardDigest` — and add them to the read-back list at `:233`, so a stale CRD that prunes the clear does not leave the old entries standing.
2. Say at `:277` that `cases[]` belongs to the pair `status.eval` records, which reconciles it with `:227`'s "the same `revisionDigest`, `suiteDigest` and `claim` at that case's entry".
3. Extend row `:487`: after the suite edit, the stub sees all three cases again and the new verdict is computed from the new run's outcomes. Mutation: leave `cases[]` standing at run start — the stub's hit count catches it.

## MINOR

### m1 — MINOR: `:138` lists an exit from the exempt state that cannot happen

`:138`, against `authtxn.go:1603-1621`, `:1498-1533`.

- `:138`: "The exempt state goes when a spec edit abandons the `Lock`, when `recreateRoute` displaces it, or when the gate is removed."
- `recreateRoute` has four call sites. Three are in `reconcileServed` (`authtxn.go:1508`, `:1529`), which `:157` itself says "is not reached while a `Lock` is recorded". The fourth is inside `runLock`'s `if reCreate` branch (`:1608`), which is entered only for a re-creation — and clause 1 of the exemption is `isReCreation`'s test **negated** (`:123`), so an exempt `Lock` never takes it.
- An exempt J2 or K2 `Lock` takes the `else` branch, where a deleted or stripped route is simply re-made by `ensureServingRoute` (`authtxn.go:1615-1621`). Nothing displaces it.
- The sentence is inherited from the ninth critique's own list, but the design states it as fact about the code.
- **Fix.** Drop the clause, or replace the list with the real exits: the `Lock` advances past `ProbingAfter`, a card is recorded for `status.activeRevisionDigest` (clause 4), a spec edit abandons it, or the gate is removed. Add that any pass on which clauses 1 to 4 fail clears the hold, which is the rule that actually does the work.

### m2 — MINOR: on a stale CRD the exempt state names the wrong cause, and the `blocked` write is re-attempted every pass

`:140`, against `:234`, `:238`, `:282`, `:233`.

- `:282` says a stale CRD "prunes `blocked` too", which is why `EvalRecordNotKept` is derived from the CRD schema instead. `blockedFor` is pruned with it.
- In the exempt state, `:140`'s first clause then fires: `EvalWaitingForAuth`, "the eval controller has not yet evaluated clause 5 for this `Lock`". The real cause is the pruned CRD, and the Agent reconciler holds the schema-check answer in the same pass (`:234`). Both reasons are set "in the gate branch" (`:292`, `:293`) and no precedence is stated between them. The ninth critique noted the missing precedence and did not count it; A12 adds a source to the set and a state in which the wrong answer is permanent rather than transient. Rule 8.
- Separately: `blocked` and `blockedFor` are outside step 11's read-back (`:233` lists `verdict`, `suiteDigest`, `endpointPath`, `cardDigest` and claims). On a stale CRD the stored value always reads back empty, so a computed `EvalWaitingForAuth` always "differs" and is re-attempted on every pass — a no-op at the API server, so no churn, but an unbounded patch stream that nothing notices. `:233`'s "This read-back is the eval controller's only guard against a stale CRD" is what makes it invisible.
- **Fix.** State that `EvalRecordNotKept` and `EvalRecordUnverifiable` precede every reading of `status.eval`, `blockedFor` included, in the gate branch. Say that a `blocked` write is attempted only after the schema check has passed, or add `blocked` and `blockedFor` to the read-back at `:233`.

### m3 — MINOR: the fail-closed reading is stated for the exempt state only, and `:141` states the match differently from `:139`

`:141`, against `:139`, `:140`.

- `:139` defines the match over three parts: the candidate digest, the suite digest and the current `Lock`'s deadline. `:141` says the gate branch outside the exempt state applies `blocked` "only while `blockedFor` matches the current **pair**" — two of the three. An implementer following `:141` writes a different comparison from the one `:139` specifies.
- Under `:139`'s stricter reading, `:141`'s default is fail-open where `:140`'s is fail-closed: an unmatched `blockedFor` outside the exempt state "takes the table's rows as before". So a standing `EvalBindingUnsupported` or `EvalAwaitingCard`, written while an exempt `Lock` stood, stops applying the moment that `Lock` ends, and the gate branch shows `EvalRunning` while the eval controller is still refusing the card — the outcome `:132` names as loud and wrong.
- The window is one eval pass, bounded at about 5 s by m1's restored requeue, which is the reason this is a MINOR and not the MAJOR its A11 ancestor was. The design neither states nor bounds it.
- **Fix.** Make `:141` use `:139`'s three-part match verbatim. State the residual lag and its 5 s bound where `:143` costs the rest, or make `:141`'s default `blocked`'s own last reason until the next eval pass rewrites `blockedFor`.

### m4 — MINOR: the new 5 s requeue is costed for clause 5's reads and for nothing else

`:115`, `:143`, against `:203`, `:196`, `card.go:316-341`.

- `:115` now applies `RequeueAfter: AuthProbeInterval` to every pass that sends no case, listing four causes: a transaction, a hold, **a run-start refusal**, and a paused candidate. `:143` costs clause 5's three reads at that rate. Nothing costs the other two.
- A run-start refusal pass performs a full card fetch from the candidate's Service (`:203`, "the next pass tries again"). Under Q2 (a) a refused candidate is held indefinitely, so a candidate whose card carries no usable `HTTP+JSON` 1.0 interface — the state row `:526` now pins — is fetched every 5 s for ever. Before A12 the ninth critique measured the actual rate as the card drift re-read, 5 minutes; this is sixty times that, and it is the pattern `card.go`'s own comment records as a mistake already made here: "Fetching on every reconcile was the first implementation and was wrong twice over … A card is not a per-reconcile input" (`card.go:320-326`).
- Every waiting pass also reads the EvalSuite uncached (`:196`), so a crashlooping candidate under `EvalPaused` — which `:220` says "stays held for good" — costs an uncached `get` every 5 s indefinitely.
- Nothing here is unsound, and `:359`'s "a suite edit is noticed within 5 minutes" still stands for a candidate with a final verdict, which is the only state `:196`'s `CardDriftInterval` requeue covers. The cost is simply unstated.
- **Fix.** Either cost the run-start fetch and the suite read at `AuthProbeInterval` beside `:143`, or give the run-start-refusal and paused branches a slower requeue — `CardRetryInterval`, 15 s, or `CardDriftInterval` — and say why clause 5's branch keeps 5 s.

### m5 — MINOR: `:219`'s new predicate counts a `Sending` entry as an entry

`:219`, against `:277`, `:224`, `:228`.

- `:277` makes `Sending` a value of `outcome`, held "between a claim and its record". So an entry with `outcome: Sending` satisfies "every suite `id` has exactly one entry in `cases[]`".
- Read as a standalone predicate — which is how `:219`'s first clause now reads — a build that evaluates it at the top of a pass writes a verdict counting the in-flight case as not passed, and on a `Failed` verdict records the pair in `failed[]` permanently, before the candidate has answered.
- `:219`'s second clause ("written in the same patch as that case's record") pins a faithful build, and only one case is in flight per Agent, so the two clauses together are safe. The first clause alone is not, and it is the one a reader lifts.
- **Fix.** "Once every suite `id` has exactly one entry in `cases[]` **with a recorded outcome**". Leave `:224`'s pick as it is — there a `Sending` entry must count, because it is claimed.

### m6 — MINOR: the A12 log cites two test rows by line numbers that have moved, which is the defect A10 fixed

`:736`, `:741`, against `:708`.

- `:736` says the ninth critique found "rows :504 and :505 killing their mutation", and `:741` says "Rows :504 and :505 assert from the moment the `Lock` reaches `ProbingAfter`". Those were the Gateway-level and foreign-traffic rows at `ee3eb1c`. At `f420db4` line 504 is the hit-count row and line 505 is the claim-with-no-outcome row, so both references now point at the wrong tests.
- The design already carries the rule against this, from the same author: `:708`, "*(Reference corrected by A10: A9 cited that row by a line number, :448, which had moved.)*"
- Alongside it, `:507` still writes "the test itself writes `Interrupted` at `cases[k]`", the positional notation A12 removed from `:227`.
- **Fix.** Name the rows by their content — "the Gateway-level hold row and the foreign-traffic row" — in `:736` and `:741`, and change `cases[k]` at `:507` to "that case's entry".

## What was verified

- **`AuthProbeInterval` is 5 s and lives where `:115` says.** `authtxn.go:47-50`.
- **The `Lock`'s deadline is set once, on entering the transaction's first stage, and is never rewritten.** `enterLock` at `ProbingBefore` (`:981-985`), `lockMissingPolicy` at `ApplyingPolicies` (`:1567-1569`), `recreateRoute` at `PreparingRoute` (`:1553-1555`). No stage transition touches `tx.Deadline`. So `:139`'s premise is exact.
- **The deadline is non-nil for every transaction this operator enters**, so `blockedFor.lockDeadline` empty does mean "no `Lock`", as `:139` says. Every reader guards `tx.Deadline != nil` anyway.
- **Clause 5's reads still match design 03's.** `stands()` is true in exactly the three cases `:131` lists (`authabove.go:101-103`); the NotFound split is exact — `listPolicies(..., true)` returns NotFound as empty for the run namespace (`authforeign.go:43`, `:92-94`), and the Gateway half lists with `false` (`authabove.go:221`).
- **`:143`'s cost breakdown is right.** The run-namespace route read happens only after a non-empty list (`authforeign.go:47-50`), through the cached client (`:52`); the Gateway `get` and both lists go through `r.reader()`.
- **`:177`'s scoping is exact**, and the gate branch's read and `runLock`'s re-creation read are the same cached `r.Get` (`httproute.go:297-311`), so the two halves of the sentence describe one client.
- **Row `:519`'s premise still holds.** `ensureService` has one call, for the desired revision under no pin (`agent_controller.go:575`).
- **`writeStatus` returns early on an equal status** (`agent_controller.go:1782-1785`), which is what lets rows `:523` and `:526` bound the resourceVersion with a `Lock` probing every 5 s.
- **`blocked`'s write does not disturb the claim or the nonce.** It is the sole writer's write to `status.eval` under the same optimistic lock, and the record step's re-apply condition reads only that case's entry.
- **The README row** now records the ninth critique's FAIL, A12, and that it awaits a tenth critique. That is accurate at `f420db4`.
- **The Status line, the §1.1 heading and `:14`** carry no critique counts, and the counts are in the §14 table. m6 of the ninth critique is closed.
- **Rule 7.** No new sentence claims something is enforced that is not. Every new rule is stated as owed with the slice. The one new claim about existing code that is false is m1's.
- **Rule 8.** The new condition text is the subject of MAJOR 1's consequence, m2 and m3.
- **A11's consolidation survives A12.** The diff replaces two sentences and adds five bullets, three table cells, three rows and one log entry. No rule, exit, non-exit or pinned row is dropped.

## Noted, not counted

- **The `Lock` deadline is truncated to the second** (`authtxn.go:981`, `:1553`, `:1567`), so two transactions on one Agent entered inside the same second share a `blockedFor.lockDeadline`. To matter they would also need the same candidate and suite digests, and a spec edit is what re-enters a `Lock`, which changes the candidate. The residual harm is one eval pass of a wrong condition, with sends still fail-closed at `:150`. Worth a clause in `:139` if the drafter wants the identity airtight; not a defect as the states reach today.
- **`:143`'s "A waiting pass comes round at most once per `AuthProbeInterval`, 5 s, plus once per Agent event"** reads oddly: the requeue is a floor, not a ceiling, and the second half concedes it. "At least once per 5 s, and more often on an Agent event" would say the same thing without the contradiction.
- **`:140` sends "no suite `id` has an entry" to `EvalRunning` before any run has started**, so the gate branch names a pair `status.eval` has not yet recorded. It must compute the pair itself, which it can. Worth half a sentence.
- **The gate branch's reasons still have no stated precedence** across the table as a whole, beyond the exempt state's three-way order at `:140`. m2 counts the one collision that is now permanent; the rest are the ninth critique's noted item, unchanged.
- **Leaving designs 02 and 03 unchanged is deliberate** and is not counted.

## The smallest set of changes

1. **MAJOR 1:** add `blockedFor` to `:234`'s checked fields and to `:370`'s owed list; `:346` "seven" → "eight"; name it in row `:524`.
2. **MAJOR 2:** state that the run-start write clears `cases[]` and `verdict` for the new pair and reads them back; scope `cases[]` to the recorded pair at `:277`; extend row `:487` to assert the re-sent cases.
3. **m1 to m6**, as stated above.

**design 16 §1.1 (A12): FAIL — 0 BLOCKER, 2 MAJOR, 6 MINOR**
