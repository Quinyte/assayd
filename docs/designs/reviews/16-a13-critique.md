# Critique: design 16 A13 — the first slice (§1.1), the eleventh pass

- **Date**: 2026-09-16
- **Target**: `docs/designs/16-evalsuite.md` §1.1 and amendment A13, at `bb3a6a1` (branch `design-16-first-slice`), read whole and as `git diff f420db4 bb3a6a1`. A13 answers `reviews/16-a12-critique.md` (FAIL: 0 BLOCKER, 2 MAJOR, 6 MINOR). The earlier records are `reviews/16-a3-critique.md` to `reviews/16-a12-critique.md`.
- **Independence**: an independent session. It wrote none of A4 to A13 and none of the ten earlier critiques. Every code citation below was traced by hand in the tree at `bb3a6a1`; nothing was taken from an earlier record without re-reading the source. The one protocol claim that rests on an external standard was checked against the published A2A v1.0 specification, not from memory.
- **Scope**: the human's decisions on Q1–Q13 sit in a separate commit and are out of scope. Leaving designs 02 and 03 unchanged is deliberate and is not counted. Design 03's A77, on another branch, is not expected to be absorbed here and is not counted.
- **Read against**:
  - `internal/controller/card.go`: `CardDriftInterval = 5 * time.Minute`, a `const` (`:306-309`); `CardRetryInterval = 15 * time.Second`, in the same file's `const` block (`:70-82`); `cardFetchDue` and its comment "A card is not a per-reconcile input" (`:311-341`); `cardRequeueAfter` (`:343-356`).
  - `internal/controller/authtxn.go`: `AuthProbeInterval = 5 * time.Second` (`:47-50`); `AuthTransactionDeadline` and its "A constant, not a flag, as the design says; the reconciler's `AuthDeadline` field exists for tests" (`:42-46`); the four deadline writers, each `.Truncate(time.Second)` — `enterCreate` (`:751-757`), `enterLock` (`:983-985`), `recreateRoute` (`:1553-1555`), `lockMissingPolicy` (`:1567-1569`); `isReCreation` (`:787-796`); `abandons` (`:799-812`); the transaction switch, where `reconcileServed` is reached only when no transaction is recorded (`:702-719`); `reCreate := isReCreation(status.Auth)` (`:1593`) and the single `recreateRoute` call inside `if reCreate` (`:1602-1608`); `reconcileServed`'s two calls (`:1508`, `:1529`); the NACK branch setting `tx.Stage = StageConverging` (`:1697-1702`) and the not-intact branch setting `StageApplyingPolicies` (`:1690-1695`).
  - `internal/controller/agent_controller.go`: `writeStatus`'s early return on an equal status (`:1782-1785`); `AgentReconciler`'s fields — `AuthDeadline`, `CardFetchTimeout`, `CardClient`, `NackPageSize`, and **no interval field for the card or the requeues**; `For(&Agent{})` with `Watches(&appsv1.Deployment{}, …)` and no predicate (`:1824-1828`).
  - `api/v1alpha1/agent_types.go`: `EvalStatus` (`:633-652`) — `Score` "rendered verbatim in the Eval printer column" (`:634-637`), `Revision` "names what was gated, so a stale score is recognisable as stale" (`:640-642`); the printer columns (`:836-841`), of which `Eval` is `.status.eval.score` (`:839`).
  - `test/envtest/card_test.go`: the drift case is built by back-dating `status.cards[].fetchedAt` past the interval, never by changing the constant (`:195-198`).
  - `docs/research/a2a-v1.0-card-and-transport-2026-09.md`: §2's envelope list (`:144-157`); the official Go SDK row (`:18`).
- **Method**:
  - Every sentence A13 added or replaced, traced to its source or to the rule it changes.
  - The three `blockedFor` lists re-counted against the field table.
  - The `cases[]` clear walked against every other field of `EvalStatus`, against a claim in flight, against a leader handover, and against `failed[]`.
  - The m4 requeue split walked against every bound, row and cost claim that named the old rate.
  - Each new or changed row built in the head, and each named mutation run against the rule it is meant to kill.
  - Nothing was run, because nothing in the slice is implemented.

**Verdict: FAIL — 0 BLOCKER, 2 MAJOR, 8 MINOR.**

Both of the tenth critique's MAJORs are closed on their own terms, and every minor is closed or half-closed as noted below. The two MAJORs are: a clear that stops one field short of the user-facing one, and a build mechanism four owed rows name that does not exist.

## Closure of the tenth critique

| Finding | A13's answer | Closed? |
|---|---|---|
| MAJOR 1: `blockedFor` load-bearing but in neither the owed list nor the guard | `:236` (schema check), `:348` ("eight"), `:372` (design 02's owed list), row `:526` | **Closed.** The three lists and the field table now all say eight and all name `blockedFor`; the audit is below |
| MAJOR 2: nothing cleared `cases[]` across pairs | `:204`, `:226`, `:279`, row `:489` | **Closed as a rule.** At-most-once, the in-flight claim and `failed[]` all survive it; the audit is below. The residue is MAJOR 1 below, which is about the fields the clear does not reach |
| m1: `:138` lists an exit that cannot happen | `:138` drops `recreateRoute` and gives the reason from the code | **Closed, and exact.** `runLock`'s only `recreateRoute` call is inside `if reCreate` (`authtxn.go:1593`, `:1602-1608`), which clause 1 negates; `reconcileServed`'s two (`:1508`, `:1529`) are unreachable while a transaction is recorded (`:702-719`). The new list is still short — m1 below |
| m2: the stale CRD names the wrong cause, and `blocked` is re-attempted every pass | `:284` puts `EvalRecordNotKept` and `EvalRecordUnverifiable` before every read of `status.eval`; `:235` adds `blocked` and `blockedFor` to the read-back | **Closed.** Both halves: the cause is named first, and a failed read-back stops the pass at `RequeueAfter: CardDriftInterval`, so the patch stream is one per interval, not one per 5 s |
| m3: the match is stated two ways, and the window is unbounded | `:141` uses the three-part match and states the window | **Half closed.** The match is fixed and the window is stated. The bound it states is the constant A13's own m4 answer moved this branch off — m2 below |
| m4: the 5 s requeue is costed for clause 5's reads and nothing else | `:115` splits the requeue; `:143` costs the suite read and the slower branch | **Closed as a cost statement**, and the constant's name and value are right (`card.go:306-309`). Its consequences are m2 and MAJOR 2 below |
| m5: `:219`'s predicate counts a `Sending` entry | `:221` "with a recorded outcome", and `:226`'s pick left alone | **Closed for the verdict.** The sibling predicate in the gate branch was not tightened with it — m3 below |
| m6: the log cites two rows by moved line numbers | `:738`, `:743` name the rows by content; `:509` drops `cases[k]` | **Closed.** No line-number citation survives in the design except the self-describing historical one at `:710` |

## The MAJOR 1 audit (the tenth critique's, now closed)

| Place | Says | Consistent? |
|---|---|---|
| `:267` "gains **eight** fields", and the table at `:271-280` | eight new: `suiteDigest`, `endpointPath`, `cardDigest`, `blocked`, `blockedFor`, `verdict`, `cases[]`, `failed[]` | yes |
| `:236` step 11's schema check | the same eight, `cases` "with `claim` in its items" | yes |
| `:348` "In the slice" | "`status.eval`'s **eight** new fields, `blockedFor` included" | yes |
| `:372` design 02's owed list | the same eight, with the reason `blockedFor` is load-bearing | yes |
| `:235` the read-back | run-start write, **every `blocked` and `blockedFor` write**, every claim | yes |

`EvalRecordNotKept` now fires in the case it was written for: `:284` states that it and `EvalRecordUnverifiable` are derived from the CRD's schema **before** any read of `status.eval`, so a pruned `blockedFor` no longer reports `EvalWaitingForAuth` for ever on a `Lock` for which clause 5 is being evaluated. Row `:526`'s second CRD — "carrying every other new field but not `blockedFor`" — is buildable: envtest takes a hand-edited Agent CRD with `status.eval.blockedFor` removed from `openAPIV3Schema`. Its primary assertion (`EvalRecordNotKept`) is what kills "omit `blockedFor` from the checked set": under that mutation the schema check passes and the Agent reads something else. The row's second clause, "and not `EvalWaitingForAuth`", is decorative as written, because the row builds no exempt `Lock`, so the mutant would read `EvalRunning`; the kill does not depend on it.

## The MAJOR 2 audit (the tenth critique's, closed as a rule)

| Question | Answer, traced |
|---|---|
| Is at-most-once still honoured when a run is cleared mid-flight? | Yes. Controller-runtime admits one reconcile per object key at a time, and the send is synchronous inside the pass (`:228`), so the eval controller cannot clear its own in-flight claim. The clear can only land on a pass after the send returned |
| What happens to an in-flight `Sending` entry when the pair changes? | Only a hard stop or a handover can leave one. The record step re-applies "only while the claim still stands: the same `revisionDigest`, `suiteDigest` and `claim` at that case's entry, found by `id`, and `outcome` still `Sending`" (`:229`). After the clear there is no entry, so an old leader's record drops. `:226`'s new sentence — entries belong to the recorded pair — is what makes that reading unambiguous, which is what the tenth critique asked for |
| Can a verdict from the old pair survive? | Not as a verdict. `verdict` is cleared, `suiteDigest` and `revisionDigest` are overwritten, and `failed[]` is keyed by pair, so a reverted suite still reads `EvalPairAlreadyFailed`. `score` and `at` do survive — MAJOR 1 below |
| Does the clear race the optimistic lock? | No. The run-start write is a merge patch under `MergeFromWithOptimisticLock` after an uncached live read (`:193`), and the only other writer of the object, the Agent reconciler, writes a full `Update` at the resourceVersion it read (`:194`, `agent_controller.go:1782-1790`). Whichever lands first, the other conflicts and re-runs |
| Is the clear conditional, so a resumed run is not destroyed? | Yes — "when the pair a run starts for differs from the recorded one" (`:204`). An operator restart mid-run finds the same pair, does not clear, and resumes, which is row `:496` |
| Row `:489`'s extension — buildable, and does its mutation kill? | Yes. At the suite edit the recorded pair is still (B, S1), because the revert and the re-apply start no run. A correct build clears and re-sends all three cases, so the stub's hit count rises by three; "keep `cases[]` across a suite digest change" sends nothing and the count does not move. The mutation is a deletion of the clear, so it compiles |
| Does the clear add a patch, against `:152`'s restored bound? | No. It rides in the run-start patch (`:204`), so the bound "one per run start" is unchanged |

---

## MAJOR 1: the run-start clear stops at `cases[]` and `verdict`, so `score` — the `Eval` printer column — crosses the pair change while `revision` moves forward in the same patch

`16-evalsuite.md:204`, against `:235`, `:271`, `:272`, and `api/v1alpha1/agent_types.go:634-642`, `:839`.

**What A13 wrote.** `:204` is headed "**A run start clears the record of the previous pair.**" Its body says:

> When the pair a run starts for differs from the recorded one, the run-start write **clears `cases[]` and `verdict`** in the same patch that records **`revision`, `revisionDigest`**, `suiteDigest`, `endpointPath` and `cardDigest`.

Two of the previous pair's fields are not in that enumeration. `score` and `at` are left as they were, and `:235` makes the omission of `at` deliberate: "The run-start write carries nothing that changes from pass to pass, because `at` is written with the verdict, not at run start." `:272` leaves `score` "as today". Step 7 (`:221`) is the only writer of both.

**Why that is not an internal detail.** `score` is the default `kubectl get agents` output. `api/v1alpha1/agent_types.go:839` is `+kubebuilder:printcolumn:name="Eval",type=string,JSONPath=.status.eval.score`, and the field's own comment (`:634-635`) says it is "the headline number the gate produced, rendered verbatim in the Eval printer column". The CRD then names the safeguard that made a stale value safe (`:640`):

> `Revision` names what was gated, **so a stale score is recognisable as stale**.

A13's run-start write is precisely the write that moves `revision` and `revisionDigest` to the new pair. Before A13 the design never said when those two were written; `:204` pins them to run start for the first time, in the same patch that clears the outcomes `score` was computed from. The one property the CRD relies on to make a stale score detectable is removed by the sentence that claims to clear the previous pair's record.

**The failure, concretely.** A gated Agent's candidate B fails its suite 2 of 3. The owner fixes the failing case — Q3 makes that the only re-run path — and the run starts for the new pair. From that moment until the last case records, `kubectl get agents` shows:

```
NAME          PHASE   ACTIVE   CANDIDATE   EVAL   ...
pa-reviewer   Held    a-4f2c   b-9d01      2/3
```

`2/3` is the previous suite's score, beside a `Candidate` column naming the revision it was not computed for, with `revision` and `revisionDigest` inside `status.eval` already pointing at the new pair. There is nothing left that marks it stale. The window is the whole run — up to 20 × `caseTimeoutSeconds` — and it is unbounded if the candidate then becomes unavailable, because `:222` says a paused run "stays held for good". The owner checking whether their fix re-opened the gate reads the score of the run it replaced. `at` compounds it: the timestamp beside that score is the old verdict's.

This is the rule-8 shape the design applies to itself elsewhere — a status that is loud and wrong rather than silent — and it is user-facing output, which `write-spec` governs.

**Why this is A13's.** The argument A13 accepted for `cases[]` is the argument for `score` and `at`: `:279` now says "The list belongs to the pair `status.eval` records, and a run start for a different pair clears it." `score` and `at` belong to the same pair by the same reasoning, and the tenth critique's own standard applies — the heading `:204` writes is false about two of the fields it covers.

**Fix.**
1. `:204`: clear `score` and `at` with `cases[]` and `verdict`.
2. `:235`: add them to the read-back list. The no-churn argument survives unchanged: a cleared `at` is a constant, so a repeated run-start write on a stale CRD still stores no change, which is what row `:525`'s resourceVersion bound asserts.
3. Row `:489`: assert that `score` is empty while the re-opened run is `Running`. Mutation: leave `score` standing at run start, which the assertion catches.

---

## MAJOR 2: four owed rows are built by "shortening `CardDriftInterval`", which is a Go `const` with no test seam, and the slice's "every item is owed" list owes none

`16-evalsuite.md:499`, `:514`, `:515`, `:525`, against `internal/controller/card.go:306-309`, `:70-82`, the `AgentReconciler` struct in `internal/controller/agent_controller.go`, and the precedent at `internal/controller/authtxn.go:42-46`.

**The mechanism does not exist.** `CardDriftInterval` is declared `const CardDriftInterval = 5 * time.Minute` (`card.go:309`), and `CardRetryInterval` is a `const` in the same file (`card.go:79-81`). A Go typed constant cannot be reassigned from a test, and `-ldflags -X` reaches only string `var`s. `AgentReconciler` carries `AuthDeadline`, `CardFetchTimeout`, `CardClient`, `NackPageSize` and `NackMaxPages` — no interval field. Four rows say the interval is shortened:

- `:499` — "a suite edited under a held candidate with a final verdict starts a new run within one `CardDriftInterval` **(shortened in the test)**, with no Agent event";
- `:525` — "Over 10 s, **with `CardDriftInterval` shortened to 2 s**, the stub serves at most 6 card fetches";
- `:514`, `:515` — "clear R's card with a failing card stub and **`CardDriftInterval` shortened**".

**What is lost.** `:514` and `:515` name a mechanism that does not exist but describe a state that is reachable another way: `test/envtest/card_test.go:195` back-dates `status.cards[].fetchedAt` by `-CardDriftInterval - time.Minute`, and `cardFetchDue` (`card.go:327-341`) then reads the re-read as due. Those two rows need re-wording, not a seam. `:499` and `:525` are different, because the interval they must shorten is **the eval controller's own requeue**, which the design specifies as the package constant (`:196`, `:235`, and now `:115`), not as anything a test can reach:

- `:499` is the only row pinning `:196`'s and `:361`'s in-slice claim that "a suite edit is noticed within 5 minutes" — the claim that keeps an EvalSuite watch out of the slice. With no seam the test must either wait five minutes or not exist.
- `:525` is the only pin for the read-back, one of step 11's two guards, and `:235` says "each guard is pinned by a test of its own". Its zero-cases assertion survives without a seam, but its two quantitative bounds — "at most 6 card fetches" over 10 s, and the resourceVersion bound — are measurements of a 2 s requeue. At 5 minutes both pass vacuously, including for the mutation "no requeue after a failed read-back".

**Why A13 owns it now.** A13's answer to m4 routes two more branches through the same constant (`:115`: a run-start refusal and a paused candidate now requeue at `CardDriftInterval`), and rests two new cost claims on it (`:143`). Rows `:492` and `:528` join the set whose observation windows are shorter than the requeue they observe. The design's own precedent is one file away and is exactly the fix: `AuthTransactionDeadline` is a `const` whose comment reads "A constant, not a flag, as the design says; **the reconciler's `AuthDeadline` field exists for tests**" (`authtxn.go:42-46`), and rows `:518`, `:519` and `:522` correctly say "the deadline shortened in the test" because that field exists. The slice's `:369` list opens "**Every item is owed**" and `:391` owes `GracefulShutdownTimeout`, the moved policy readers and the A2A client's injection seam — and no requeue seam. That omission is the same class as the tenth critique's MAJOR 1: a load-bearing component that the lists an implementer is held to do not carry.

**Fix.**
1. `:391` ("This design"): owe an eval-controller field for its requeue intervals, on the `AuthDeadline` pattern — zero meaning `AuthProbeInterval` and `CardDriftInterval` — with the unit test that the zero value resolves to the constants.
2. `:514`, `:515`: replace "with `CardDriftInterval` shortened" with "by back-dating `status.cards[]`'s `fetchedAt` past `CardDriftInterval`, as `test/envtest/card_test.go` does", which is what builds that state.
3. `:499`, `:525`: keep "shortened in the test" once the field exists, and say which field.

---

## MINOR

### m1 — `:138`'s rewritten exit list is still short: a NACK, and a policy no longer intact, both leave `ProbingAfter`

`:138`, against `internal/controller/authtxn.go:1690-1702`.

- A13 replaces the list with: "The exempt state goes when the `Lock` advances past `ProbingAfter`, when a card is recorded for `status.activeRevisionDigest`, when a spec edit abandons the `Lock`, or when the gate is removed."
- A fresh NACK at `ProbingAfter` sets `tx.Stage = StageConverging` (`:1697-1702`), and a policy found no longer intact sets `tx.Stage = StageApplyingPolicies` (`:1690-1695`). Either fails clause 2, so either ends the exempt state, and neither is "advancing past `ProbingAfter`".
- Nothing is unsound: the same sentence states the general rule — "Any pass on which clauses 1 to 4 fail clears the hold, and that is the rule that does the work" — and that covers both. But A13 presents the enumeration as fact about the code, which is the defect the tenth critique's m1 was, one entry along.
- **Fix.** Add "when a NACK or a policy no longer intact returns the `Lock` to an earlier stage", or mark the four as examples of the general rule rather than the exits.

### m2 — `:141`'s stated bound names the constant A13's own m4 answer just moved that branch off

`:141`, against `:115`, `:134`, and `card.go:306-309`.

- `:141` bounds the fail-open window outside the exempt state: "The next pass comes within `AuthProbeInterval`, 5 s."
- The pass that wrote the refusal is, by `:134`'s precedence 1, a run-start refusal — the branch `:115` has just given `RequeueAfter: CardDriftInterval`, 5 minutes. The eval controller's own requeue puts the next pass sixty times further away than the sentence says.
- The window does close quickly, but through a mechanism the sentence does not name: the `Lock` ending is an Agent status write, `writeStatus` writes it because the status is no longer equal (`agent_controller.go:1782-1785`), and that write enqueues the Agent on the eval controller's `For(&Agent{})`. So the true bound is one Agent event, with `CardDriftInterval` as the backstop — not `AuthProbeInterval`, which no longer applies to this branch at all.
- This is rule 7's case, which the design invokes against itself at `:228` for `GracefulShutdownTimeout`: a bound stated from a constant the code does not use here.
- **Fix.** `:141`: "The `Lock` ending is itself an Agent status write, which enqueues the eval controller, so the window is one eval pass; the requeue backstop on this branch is `CardDriftInterval`."

### m3 — the gate branch's `EvalRunning` predicate was not tightened with the verdict's, so the last case's flight has no row

`:140`, `:302`, against `:221`, `:279`.

- A13's m5 fix makes the verdict fire "once every suite `id` has exactly one entry in `cases[]` **with a recorded outcome**" (`:221`), because `Sending` is a claim in flight.
- The sibling predicate in the gate branch was left at A12's wording: "`EvalRunning` while a suite `id` has no entry in `cases[]`" (`:140`), and the table's `running` row (`:302`) states no predicate at all. `Sending` is an entry (`:279`).
- So between the last case's claim and its record, `:140`'s enumeration offers nothing: not `EvalRunning`, and no verdict is written yet, so none of `EvalFailed`, `EvalPairAlreadyFailed` or the promotion branch applies. The observable behaviour is right only because `GatesPassed` is sticky (`:310`) and keeps the previous pass's `EvalRunning`. A build that follows `:140`'s chain literally is correct by accident.
- **Fix.** `:140`: "`EvalRunning` while a suite `id` has no entry **with a recorded outcome**", matching `:221`. Leave `:226`'s pick as it is, where a `Sending` entry must count.

### m4 — a failed run-start card fetch now retries at 5 minutes, not `CardRetryInterval`, and reports a mismatch that was never observed

`:115`, `:205`, `:299`, against `card.go:79-81`.

- `:115` justifies the slower branch: "Its inputs are the candidate's card and its availability, and each of those reaches the eval controller as an Agent event anyway." That holds for a recorded digest changing and for availability, both of which the Agent reconciler writes into Agent status (it watches Deployments, `agent_controller.go:1824-1828`). It does not hold for the branch's other cause: `:205` sends a run-start fetch whose *failure* — a refused connection, a timeout — to the same `EvalAwaitingCard` refusal. Nothing in Agent status changes, because the recorded digest is still valid and the shared predicate already required it. So the retry is a flat five minutes. Design 02's own constant for a failed card fetch is `CardRetryInterval`, 15 s (`card.go:79-81`), chosen for exactly this.
- Separately, `:205` maps a failed fetch and a digest mismatch onto one reason, and the table renders it as "the card fetched at run start does not match it" (`:299`). For a refused connection that names a cause that was not checked — the distinction the design itself draws at `:230` between `Unreachable` and `Interrupted`.
- **Fix.** Give a failed run-start fetch `CardRetryInterval`, and either a distinct message under `EvalAwaitingCard` or a reason of its own.

### m5 — "the read-back covers the clear" is a guard that cannot fire

`:204`, `:235`.

- A merge patch expresses the clear as `cases: null`, and the response carries `cases` absent. On an installed CRD that carries `cases`, the clear applied; on one that does not, `cases` was never stored, so the response is also absent — and the old entries it was meant to protect were never there to survive. There is no installed-CRD state in which the clear is pruned *and* stale entries remain observable, so this read-back passes unconditionally.
- Nothing is lost: the same patch's `suiteDigest`, `endpointPath` and `cardDigest` do catch a stale CRD, and the claim that follows catches what they miss (row `:525`). But `:204` claims a guard where there is none, and row `:525`'s mutation list names none for it.
- **Fix.** Say that the clear is covered by the other read-backs in the same patch, not that the read-back covers the clear.

### m6 — the Status line and §14's critique table still stop at A12

`:3`, `:14`, `:752-760`, against `:762` and `docs/designs/README.md:24`.

- `:3` reads: "Its eighth critique passed A10. **A11 and A12, which answer the eighth critique's minors and the ninth critique's findings, are under critique.**" The tenth critique returned FAIL on A12 and A13 answers it in the same commit. `:14` likewise still says "revised by A4 to **A12**".
- `:3` delegates the detail — "§14 logs every critique and the amendment that answers it" — and §14's table (`:752-760`) ends at `| reviews/16-a11-critique.md | FAIL … | A12 |`. There is no row for `reviews/16-a12-critique.md`, the record `:773` says is committed unmodified.
- `docs/designs/README.md:24` is correct and current. So the two sources disagree, and the one a contributor is told to trust over any summary is the wrong one — the same divergence §14 records at `:748` as the reason the history was moved there.
- This is the same class the tenth critique's m6 counted, so it is a MINOR here too, not more.
- **Fix.** `:3`: "A13 answers the tenth critique and is under critique." `:14`: "A4 to A13." `:752-760`: add `| reviews/16-a12-critique.md | FAIL: 0 BLOCKER, 2 MAJOR, 6 MINOR | A13 |`. Moving the table out of A12's bullet to the end of §14 would stop each amendment having to reach back into the previous one's entry.

### m7 — the Agent reconciler also reads the EvalSuite, and the reader it uses is unpinned while the owed RBAC is `get` only

`:196`, `:388`, against `:249`, `:297`, `:298`, `:140`, `:141`.

- `:196` pins the eval controller: "EvalSuites are read uncached, on every eval pass, with the manager's API reader. **No EvalSuite informer is started**, so an absent CRD cannot fail manager start."
- The Agent reconciler reads the suite too, on every pass of a gated Agent: the third promotion condition needs "the digest of the EvalSuite **as read in this pass**" (`:249`); `:297` and `:298` give it `EvalSuiteNotFound` and `EvalSuiteUnreadable` in the gate branch; and A13's own `:140` now has it "compute the pair itself, from the candidate's digest and **the suite it read**". Nothing says which reader that is.
- It matters twice. An implementer reaching for the manager's cached client starts a cluster-wide EvalSuite informer in the Agent reconciler's path — the thing `:196` exists to prevent — and `:388` owes "RBAC for `get` on `evalsuites`, **and nothing more**: suites are read uncached, by `get` alone, and no informer lists or watches them". A cached read needs `list` and `watch`, so it would be refused, and every gated Agent's gate branch would error under a reason the table does not carry.
- The cost is also unstated: while a `Lock` probes at 5 s, that is a second uncached `get` per Agent per 5 s beside the eval controller's, and `:143` costs only the eval controller's.
- **Fix.** State at `:196` that **both** controllers read suites through the manager's API reader, and add the Agent reconciler's read to `:143`'s cost.

### m8 — `:213`'s citation does not carry the blocking-default clause it attributes to the research

`:213`, against `docs/research/a2a-v1.0-card-and-transport-2026-09.md:144-157`.

- `:213`: "One `SendMessageRequest` per case, **as `docs/research/a2a-v1.0-card-and-transport-2026-09.md` §2 pins it** and `test/responder` accepts it: … **no `configuration`, so the call blocks until the task is terminal or interrupted, which is A2A 1.0's default.**"
- §2 of that document lists `SendMessageRequest`'s fields and records `configuration` as optional (`:148-149`). It says nothing about what omitting it means.
- **The claim is true.** Checked against the published A2A v1.0 specification: `SendMessageConfiguration.return_immediately` has `default: false`, and "If `false` (default), the operation MUST wait until the task reaches a terminal (`COMPLETED`, `FAILED`, `CANCELED`, `REJECTED`) or interrupted (`INPUT_REQUIRED`, `AUTH_REQUIRED`) state before returning" (the spec's Execution Mode section and `a2a.json`'s `SendMessageConfiguration` schema). So this is provenance, not correctness — but it is load-bearing: were the default the other way, every case would come back `TASK_STATE_SUBMITTED` and `:216`'s table would score it `fail/TaskNotCompleted`, failing every honest revision. The other half of the citation, "`test/responder` accepts it", is this repository's own fixture, which is the circularity `internal/controller/card.go` records as the reason the v0.x card shape survived undetected here.
- **Fix.** Add the blocking default, under the v1.0 field name `return_immediately`, to the research document's §2 and cite that line; drop `test/responder` as corroboration for a protocol default.

## Considered and rejected

- **The m4 requeue change does NOT disarm row `:528`.** The row asserts that a served-state exempt `Lock` under a card refusal leaves the Agent's resourceVersion unchanged over 10 s, and its mutation is "clear `blocked` whenever clause 5 finds no hold". A13 moved that branch's requeue to 5 minutes, which looks as though the mutant would make no write inside the window. It does not hold: the mutant makes two real changes per pass — the clear, then the run-start rewrite — and the design says twice that an eval patch enqueues the Agent again on the eval controller's own watch (`:194`, `:223`, which has no generation predicate). So the mutant drives itself in a tight loop on its own writes, independent of any requeue, and the resourceVersion bound catches it inside 10 s. A correct build writes once, computes the same value on its next pass, writes nothing, and goes quiet. The row still builds and still kills both of its mutations.
- **The clear does not break finality.** `failed[]` is untouched by the run-start write, so reverting the suite to a failed pair still reads `EvalPairAlreadyFailed`, and row `:489`'s first half is unaffected.
- **`:139`'s deadline premise is exact.** Every deadline writer truncates to the second — `enterCreate` (`authtxn.go:751`), `enterLock` (`:983`), `recreateRoute` (`:1553`), `lockMissingPolicy` (`:1567`) — and A13 names only the two that enter a `Lock`, which is right, because `blockedFor.lockDeadline` is a `Lock`'s and the other two record a `Create`. No stage transition rewrites `tx.Deadline`.

## What was verified

- **The constant names and values A13 relies on.** `CardDriftInterval = 5 * time.Minute` (`card.go:309`), `CardRetryInterval = 15 * time.Second` (`card.go:81`), `AuthProbeInterval = 5 * time.Second` (`authtxn.go:50`). The 5 s-to-5 min ratio A13 states as "sixty times as often" is right.
- **The card comment A13 quotes is verbatim.** "Fetching on every reconcile was the first implementation and was wrong twice over … A card is not a per-reconcile input" (`card.go:320-326`).
- **`recreateRoute` cannot displace an exempt `Lock`.** One call inside `runLock`'s `if reCreate` (`authtxn.go:1602-1608`), gated by `isReCreation` (`:1593`), which clause 1 negates; two in `reconcileServed` (`:1508`, `:1529`), which the transaction switch does not reach while a transaction is recorded (`:702-719`).
- **The gate branch can compute the pair before a run exists.** It already computes the suite digest for the third promotion condition (`:249`) and holds the desired revision's digest and `status.auth.transaction.deadline` in the same pass. So `:140`'s new sentence is implementable with no new read.
- **The eval controller's 5-minute branches do wake on an event.** The Agent reconciler watches Deployments (`agent_controller.go:1824-1828`) and writes availability into Agent status, and a card digest change is an Agent status write, so `:115`'s justification holds for the two causes it names. It does not hold for a failed fetch — m4.
- **`writeStatus` returns early on an equal status** (`agent_controller.go:1782-1785`), which is what lets rows `:525`, `:526` and `:528` bound the resourceVersion beside a `Lock` re-probing every 5 s.
- **The at-most-once protocol survives the clear.** One reconcile per object key, a synchronous send, and a record whose re-apply requires the claim to still stand (`:229`).
- **No stale line-number citation survives.** The only `:NNN` reference left in the design is the self-describing historical one at `:710`.
- **A11's consolidation still holds.** The diff replaces seven sentences, adds one paragraph, four clauses and three row extensions. No rule, exit, non-exit or pinned row is dropped; the one deletion, `recreateRoute` from the exits, is the tenth critique's m1 and is replaced by a correct statement.
- **Rule 7.** Every new claim about existing code is true. The two bounds that are not are m2's (the wrong constant) and MAJOR 2's (a seam that does not exist).
- **Rule 8.** The new `EvalRecordNotKept` precedence at `:284` is a rule-8 fix and is right. The new rule-8 residues are MAJOR 1, m3 and m4.

## Noted, not counted

- **The schema check is one level deep for `cases` and zero for `blockedFor`.** `:236` checks `cases` "with `claim` in its items" but `blockedFor` as a bare field. A CRD carrying `blockedFor` without `lockDeadline` would silently degrade `:139`'s three-part identity to two. Not reachable today, because the slice ships all three subfields in one generated CRD.
- **`:143`'s paused-candidate sentence attributes a card fetch to a branch that makes none.** "A pass held by a run-start refusal, or by a paused candidate … re-fetches the candidate's card and reads the suite once per 5 minutes." A paused-candidate pass fetches no card: the path was chosen at run start, and before a run starts the availability predicate has already failed. Half a sentence.
- **`:229`'s "a claim that no longer stands, because a new generation superseded the run" now has a second cause** — a run start for a different suite digest. The mechanism handles it, because the entry is gone; only the explanation names one cause.
- **The gate branch's row precedence is still unstated** beyond A13's `EvalRecordNotKept`-first rule and the exempt state's three-way order. The ninth and tenth critiques both noted this; A13 closed the one collision that was permanent.
- **`EvalRecordNotKept`'s new precedence does not reach `status.auth`.** `:284` orders it before every read of `status.eval`; the `EvalWaitingForAuth` row reads `status.auth.transaction`, so on a stale CRD with a `Create` recorded the CRD remedy is never named until the transaction ends. Both causes are true and the nearer one shows, so this is a choice. Worth a clause.
- **§2's doctrine gates still describe the full design** — "eval runs are **Jobs**", 0 standing pods, Postgres and an object store — and `:396-401`'s "What design 16 says that the slice cannot keep" lists no §2 line. Related: `:391` owes "the operator's A2A client" without answering "bind don't build" against the official Go SDK the project's own research pins, `a2aproject/a2a-go @ v2.5.0` (`docs/research/a2a-v1.0-card-and-transport-2026-09.md:18`), which `go.mod` does not carry. The slice does flag that it adds a second A2A client (`:333`), so this is a gate left unanswered rather than a claim that is wrong.
- **Leaving designs 02 and 03 unchanged is deliberate** and is not counted. Design 03's A77, on another branch, would remove the missing-policy wedge's "apart" case; §1.1 is not expected to have absorbed it and has not.

## The smallest set of changes

1. **MAJOR 1:** clear `score` and `at` at run start beside `cases[]` and `verdict`; add them to `:235`'s read-back; assert an empty `score` during the re-opened run in row `:489`.
2. **MAJOR 2:** owe an eval-controller requeue field at `:391`, on `AuthDeadline`'s pattern; re-mechanise rows `:514` and `:515` onto a back-dated `fetchedAt`; say which field rows `:499` and `:525` shorten.
3. **m1 to m8**, as stated above.

**design 16 §1.1 (A13): FAIL — 0 BLOCKER, 2 MAJOR, 8 MINOR**
