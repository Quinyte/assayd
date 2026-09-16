# Critique: design 16 A15 — the first slice (§1.1), the twelfth pass

- **Date**: 2026-09-16
- **Target**: `docs/designs/16-evalsuite.md` §1.1 and amendment A15, at `2b14d18` (branch `design-16-first-slice`), read whole and as `git diff bb3a6a1 2b14d18`. A15 answers `reviews/16-a13-critique.md` (FAIL: 0 BLOCKER, 2 MAJOR, 8 MINOR). The earlier records are `reviews/16-a3-critique.md` to `reviews/16-a13-critique.md`.
- **Independence**: an independent session. It wrote none of A4 to A15 and none of the eleven earlier critiques. Every code citation below was traced by hand in the tree at `2b14d18`. The one protocol claim that rests on an external standard was checked by fetching `specification/a2a.proto` at tag `v1.0.1` from the spec repository and reading the message, not by trusting the eleventh critique, which asserted the same thing.
- **Scope**: the human's decisions on Q1–Q13 sit in a separate unpushed commit and are out of scope, as is the approval log entry the drafter numbers A16. Leaving designs 02 and 03 unchanged is deliberate and is not counted.
- **Read against**:
  - `api/v1alpha1/agent_types.go`: `EvalStatus` (`:632-652`) — exactly five fields today, `Score`, `Suite`, `Revision`, `RevisionDigest`, `At`; `Score`'s comment "the headline number the gate produced, rendered verbatim in the Eval printer column" (`:634-635`); `Revision`'s "names what was gated, so a stale score is recognisable as stale" (`:640`); the printer column `Eval` → `.status.eval.score` (`:839`).
  - `internal/controller/card.go`: `CardRetryInterval = 15 * time.Second` in the `const` block (`:79-81`) with the comment "how soon a failed fetch is retried"; `CardDriftInterval = 5 * time.Minute`, a bare `const` (`:306-309`); `cardFetchDue`, whose failed-attempt branch returns on `CardRetryInterval` and whose registered branch returns on `CardDriftInterval` (`:311-341`); `cardRequeueAfter` (`:343-356`).
  - `internal/controller/authtxn.go`: `AuthTransactionDeadline = 4 * time.Minute` with "A constant, not a flag, as the design says; the reconciler's AuthDeadline field exists for tests" (`:42-46`); `AuthProbeInterval = 5 * time.Second` (`:47-50`); the fresh-NACK branch setting `tx.Stage = StageConverging` (`:1697-1706`) and the not-intact branch setting `tx.Stage = StageApplyingPolicies` (`:1685-1696`).
  - `internal/controller/agent_controller.go`: the `AgentReconciler` struct — `CardFetchTimeout`, `CardClient`, `AuthProbe`, `AuthDeadline` ("Zero means AuthTransactionDeadline, the design's constant; only tests set it", `:121-123`), `NackPageSize`, `NackMaxPages`, and **no interval field for any requeue**; `writeStatus`'s early return on an equal status (`:1782-1785`).
  - `test/envtest/card_test.go`: the drift case is built by back-dating `status.cards[0].fetchedAt` by `-CardDriftInterval - time.Minute` and `Status().Update`ing it (`:193-199`), never by changing the constant.
  - `specification/a2a.proto` @ `v1.0.1`, fetched: `message SendMessageConfiguration` (`:143-161`), whose fourth field is `bool return_immediately = 4;` carrying the comment the research document now quotes; `message SendMessageRequest` (`:648-658`) — `tenant`, `message` (REQUIRED), `configuration`, `metadata`.
  - `docs/research/a2a-v1.0-card-and-transport-2026-09.md` §2, and its new paragraph (`:151-160`).
- **Method**:
  - Every sentence A15 added or replaced, traced to its source or to the rule it changes.
  - The run-start enumeration counted against the field table, and the field table counted against the Go type.
  - The requeue field walked against every branch of §1.1 that returns a requeue, and against each row that shortens it.
  - Each new or changed row built in the head, and each named mutation run against the rule it is meant to kill.
  - The one external claim fetched and read, not recalled.
  - Nothing was run, because nothing in the slice is implemented.

**Verdict: PASS — 0 BLOCKER, 0 MAJOR, 7 MINOR.**

Both of the eleventh critique's MAJORs are closed, on their own terms and against the code. All eight of its minors are closed. The seven minors below are small and local: a count A15's own change invalidated, two rows whose mutations are not quite killed, a log entry A15 did not correct beside the one it did, and three sentences that point a reader slightly wrong. None of them changes a rule, and none of them would change what an implementer builds. **§1.1 is ready.**

## Closure of the eleventh critique

| Finding | A15's answer | Closed? |
|---|---|---|
| MAJOR 1: the run-start clear stops at `cases[]` and `verdict`, so `score` — the `Eval` printer column — crosses the pair change | `:204-209` (enumerated from the field table), `:211`, `:213`, `:244`, `:280-281`, row `:498` | **Closed.** The audit is below. Thirteen is the right number, the split is right, and the printer column cannot show a stale score across a pair change |
| MAJOR 2: four owed rows are built by shortening a Go `const` with no test seam, and no list owes one | `:400` (owed), `:115` (the field named), rows `:508`, `:522`, `:524`, `:525`, `:535` | **Closed.** The seam is owed in the right list, on the right precedent, and the two rows that needed only a stale card are re-mechanised onto a technique that exists in the tree. The residues are m1 and m2 below |
| m1: `:138`'s exit list is still short — a NACK, and a policy no longer intact | `:138` adds both, and reframes the four as "Examples of that rule, not a closed list" | **Closed, and exact.** `authtxn.go:1697-1706` sets `StageConverging` on a fresh NACK; `:1685-1696` sets `StageApplyingPolicies` when the policy is not intact. Neither is "advancing past `ProbingAfter`" |
| m2: `:141` bounds the fail-open window with the constant A13's own split moved that branch off | `:141` now gives the window as one Agent event, with `CardDriftInterval` as the backstop | **Closed.** The `Lock` ending is a status change, so `writeStatus` writes (`agent_controller.go:1783-1785`), and the eval controller's `For(&Agent{})` has no generation predicate (`:232`) |
| m3: the gate branch's `EvalRunning` predicate was not tightened with the verdict's | `:140` adds "with a recorded outcome"; the table row `:311` states the predicate; step 10's pick left alone | **Closed.** The last case's flight now has a row, and no longer rests on `GatesPassed` being sticky |
| m4: a failed run-start fetch retries at 5 minutes and reports a mismatch never observed | `:115` (the 15 s exception), `:214` (two causes, one reason, distinguished messages), `:308` (the table row), `:143` (the cost) | **Closed as a rule.** `CardRetryInterval` is design 02's own failed-fetch interval (`card.go:79-81`, `cardFetchDue:337`). The residue is m7 below: the message distinction is pinned by no row |
| m5: "the read-back covers the clear" is a guard that cannot fire | `:213` states the opposite and gives the reason; `:244`'s list carries no cleared field | **Closed.** And the drafter resolved a real tension in the eleventh critique, whose MAJOR 1 asked for `score` and `at` in the read-back while its m5 argued no clear can be read back. `:213` is the consistent answer |
| m6: the Status line and §14's critique table stop at A12 | `:3` and `:17` stop naming the latest amendment at all; the table moves to the end of §14 and gains two rows; `docs/designs/README.md:24` updated | **Closed.** The nit is m6 below |
| m7: the Agent reconciler also reads the EvalSuite, and its reader is unpinned | `:196` names both controllers and ties the reader to the `get`-only RBAC; `:143` costs the second read | **Closed.** Both halves of the asked fix, and the paused-branch noted item folded in |
| m8: the citation does not carry the blocking-default clause | `:217` cites `SendMessageConfiguration.return_immediately`; the research document's §2 records it (`:151-160`); `test/responder` dropped as corroboration | **Closed, and verified at source.** See "What was verified" |

## The MAJOR 1 audit (the eleventh critique's, now closed)

| Question | Answer, traced |
|---|---|
| Is thirteen the right number? | Yes. `EvalStatus` carries five fields today (`agent_types.go:633-652`), and the slice adds eight (`:276`, `:381`). The field table at `:278-289` lists all thirteen, in five rows |
| Does the enumeration account for all thirteen? | Yes, exactly once each. Records six — `suite`, `revision`, `revisionDigest`, `suiteDigest`, `endpointPath`, `cardDigest`; sets one — `verdict: Running`; clears three — `score`, `at`, `cases[]`; leaves three — `failed[]`, `blocked`, `blockedFor`. 6 + 1 + 3 + 3 = 13 |
| Is each assignment right? | Yes. `failed[]` is keyed by pair and carries finality (`:230`), so clearing it would destroy Q3. `blocked` and `blockedFor` are computed once per pass and written only on a change (`:133`, `:139`), so the run-start write must not touch them. `score` and `at` are written with the verdict (`:230`, `:281`) and belong to the pair the verdict was for |
| Can the printer column show a stale score across a pair change? | No. `revision` and `revisionDigest` are written **only** at run start (`:280`), and that is the same patch that clears `score` and `at`. There is no other writer of the pair identity, so no window exists in which `revision` names the new pair and `score` the old one. Before the run starts, both still name the old pair, which is the state design 02's `Revision` comment was written for |
| Does the heading now match the body? | Yes. "**A run start replaces the whole record of the previous pair**" (`:204`), and the body accounts for every field. The eleventh critique's complaint was that A13's heading claimed the record and its body cleared two fields |
| Does the read-back argument survive the two new clears? | Yes, and it is stronger. `:244`: "`score` and `at` are cleared by it rather than written by it, and a clear carries the same value every time." On a stale CRD `score` and `at` are old fields that are *not* pruned, so they are stored — cleared once, then identical on every repeated run-start write, which is what row `:535`'s resourceVersion bound needs |
| Is the finality row's mutation killed? | By one assertion, which is timing-dependent — m4 below. The mutation itself is a deletion of the clear, so it compiles |
| Does the clear make a stale CRD worse? | No, better. Under A13 a stale CRD stored the new `revision` beside the old `score`; under A15 it stores the new `revision` beside an empty `score`, and `:245` already reads that state as the tell — "a run-start write that left `revisionDigest` naming the candidate with no `verdict` would betray a stale CRD" |

## The MAJOR 2 audit (the eleventh critique's, now closed)

| Question | Answer, traced |
|---|---|
| Are the constants' names and values right? | Yes, all three. `AuthProbeInterval = 5 * time.Second` (`authtxn.go:50`), `CardRetryInterval = 15 * time.Second` (`card.go:81`), `CardDriftInterval = 5 * time.Minute` (`card.go:309`). All three are `const`, and `AgentReconciler` carries no interval field, so "shortened in the test" named nothing that exists |
| Is the precedent quoted correctly? | Yes, verbatim in substance: `authtxn.go:42-46` reads "A constant, not a flag, as the design says; the reconciler's AuthDeadline field exists for tests", and the field's own comment is "Zero means AuthTransactionDeadline, the design's constant; only tests set it" (`agent_controller.go:121-123`) |
| Is one field resolving to three defaults coherent, or does it need to be three? | **One is right.** Zero resolves per branch; a set value applies to every branch. No owed row needs two branches shortened to different values, and none needs one shortened while another stays long: row `:508` exercises only the final-verdict branch and row `:535` only the read-back-failure branch. Three fields would be three knobs no test turns independently, and the unit row at `:522` — which asserts the three-way resolution of the zero value — would lose its point, because three fields each defaulting to one constant assert nothing |
| Is the seam owed in the right list? | Yes. `:400`, under "**This design**", beside the eval controller itself. It is not design 02's (the field is on a controller design 02 does not build) and not design 07's (it is not chart or RBAC) |
| Are the two re-mechanised rows buildable? | Yes. `test/envtest/card_test.go:193-199` back-dates `status.cards[0].fetchedAt` by `-CardDriftInterval - time.Minute` through a status update and the re-read is then due (`cardFetchDue`, `card.go:327-341`). Rows `:524` and `:525` combine that with a failing card stub, which clears the digest through `recordCardAttempt` — exactly the state each needs, with no seam at all |
| Does the unit row's mutation compile and get killed? | Yes. "Resolve every branch to one interval" is a one-line collapse and compiles; the three-way assertion on the zero value distinguishes 5 s, 15 s and 5 min |
| Does row `:508`'s mutation get killed, now that it can be built? | Yes. "No requeue while a verdict is final" leaves the suite edit unnoticed — no EvalSuite watch exists (`:196`, `:370`) and the row stipulates no Agent event — so no run starts, and the row fails. Without the seam the row would have had to wait five real minutes, which is what the eleventh critique said |
| Does row `:535`'s payoff depend on the seam? | Yes, for one of its mutations. "Write `at` at run start" needs at least two passes inside the 10 s window to move the resourceVersion, and at five minutes there is only one. With the field at 2 s there are about five. The other quantitative mutation is m3 below |
| Do the rows still name `CardDriftInterval` where they should? | Yes. Row `:508` asserts "within one `CardDriftInterval` (the eval controller's requeue field shortened in the test)". Reading that as "within one requeue interval, whose zero value is `CardDriftInterval`" is the only coherent reading, and the parenthetical supplies it |

---

## MINOR

### m1 — `:400` says four owed rows shorten one of those intervals; A15's own change left two

`16-evalsuite.md:400`, against `:508`, `:522`, `:524`, `:525`, `:535`, and `:774`.

- The owed item reads: "**Four owed rows shorten one of those intervals**, and each is a Go `const` that no test can reach: without the field they would have to wait five minutes or not exist."
- Two rows shorten it: `:508` ("the eval controller's requeue field shortened in the test") and `:535` ("with the eval controller's requeue field set to 2 s"). The other two, `:524` and `:525`, are the ones A15 re-mechanised onto back-dating `status.cards[]`'s `fetchedAt`, and they now shorten nothing.
- A15's own log at `:774` gets it right: "The two rows that need only a stale card are re-mechanised onto the existing technique … The two that need a short requeue name the field." So the amendment contradicts itself, and the wrong half is in the list an implementer is held to.
- Nothing is built differently — the field is owed either way — but "Every item is owed" is the sentence that opens this list, and a count inside it that the same amendment invalidated is the class of drift the eleventh critique's MAJOR 1 was.
- **Fix.** `:400`: "Two owed rows shorten one of those intervals."

### m2 — the bullet that defines the field enumerates three branches; both rows that shorten it exercise a fourth and a fifth

`:115`, against `:196`, `:244`, `:508`, `:535`, `:400`.

- `:115` names three branches and closes: "Each of these three intervals is the zero value of the eval controller's requeue field ('What the slice depends on'), so a test can shorten it without touching a constant."
- Neither row that shortens the field is on one of those three. Row `:508` is a pass **held with a final verdict**, whose requeue is stated only at `:196` — "While a candidate is held with a final verdict, the controller requeues every `CardDriftInterval`, 5 minutes." Row `:535` is a pass whose **read-back failed**, whose requeue is stated only at `:244` — "returns `RequeueAfter: CardDriftInterval`".
- `:400`'s wording does reach them — "on the branches that return them" is general, and both return `CardDriftInterval` — so a careful implementer gets it right. But `:115` is where the field is introduced and where its scope reads as closed, and `:115`'s own lede says "every pass that sends no case returns a requeue" before enumerating three of the five kinds of pass that do. An implementer who builds the field for the three at `:115` leaves both owed rows unbuildable, which is the failure MAJOR 2 was raised to prevent.
- **Fix.** `:115`: "Every requeue the eval controller returns goes through that field; its zero value is the constant each branch names, including `:196`'s final-verdict requeue and step 11's after a failed read-back."

### m3 — row `:535` names "no requeue after a failed read-back" as a mutation, and both of its bounds are upper bounds

`:535`, against `:244`, `:703`.

- The row asserts "the stub serves **at most** 6 card fetches" over 10 s at a 2 s requeue, and that "the Agent's resourceVersion **does not change** after the first run-start write". Its mutation list includes "or no requeue, after a failed read-back".
- A build that returns no requeue makes one card fetch and one run-start write, then goes quiet. One is at most six, and one write is "the first run-start write". Both bounds pass. The mutation survives.
- Its sibling, "return `Requeue: true`", is killed: controller-runtime's default per-item rate limiter starts at 5 ms and doubles, so about a dozen fetches land inside 10 s, over the bound. And "write `at` at run start" is killed by the resourceVersion bound, which is the mutation the seam was needed for. So the seam did its work; this one clause did not come with it.
- The eleventh critique named this mutation by name as one the missing seam left passing vacuously, and A15's log says "the read-back row's quantitative bounds are restated against it", which reads as closure it does not have for this clause.
- **Fix.** `:535`: "the stub serves at least 3 and at most 6 card fetches", or "the stub is hit again within 3 s of the first".

### m4 — the finality row's empty-`score` assertion samples a window the test does not control

`:498`, against `:232`, `:525`.

- The row asserts "`status.eval.score` is empty for as long as that run reads `Running`", and that is the only assertion killing "leave `score` standing at run start".
- Under the mutant, `score` holds the previous pair's value from the run start until the verdict overwrites it. Correct and mutant differ only inside that window, and the row gives the test no way to hold it open: three cases at one per pass (`:232`), each a local stub round trip, can be over in milliseconds, and a poll that never samples a `Running` state makes the assertion vacuous and the mutant survive.
- The technique is already in this table: row `:525`'s sibling writes "The stub holds C's last case while the test deletes `<agent>-auth`". One clause borrows it.
- **Fix.** `:498`: "the stub holds the first case while the test reads `status.eval`, which is empty of `score` and `at` beside `verdict: Running`".

### m5 — A15 corrected A12's log entry and left A13's asserting a guard A15 removed

`:762`, `:767`, against `:213`, `:115`, `:758`.

- A13's entry still reads: "The run-start write now clears `cases[]` and `verdict` whenever the pair differs from the recorded one, in the same patch, **and the read-back covers the clear**." `:213` is A15 withdrawing exactly that: "**The clears are not read back on their own**, and the read-back list (step 11) does not carry them."
- The same entry's m4 line (`:767`) says "a run-start refusal and a paused candidate requeue at `CardDriftInterval`, 5 minutes, not 5 s", which `:115` has since split — a run-start refusal that read a card does, a failed fetch retries at 15 s.
- The log entries are written in the present tense as statements about the design, not as quotations of what an amendment once said, so a reader who greps finds two sentences in one file that disagree. And A15 has already shown the convention allows editing an earlier entry: it edited A12's to move the critique table out of it (`:758`).
- This is the class the eleventh critique's m6 counted — two places in the corpus disagreeing about one fact — so it is a MINOR here too.
- **Fix.** `:762`: strike "and the read-back covers the clear", or append "(withdrawn by A15: a cleared field and a pruned field read alike)". `:767`: "a run-start refusal that read a card, and a paused candidate, requeue at `CardDriftInterval`".

### m6 — the Status line points at the table for something the table does not carry

`:3`, against `:758`, `:787-801`.

- `:3`: "§14's table at the end is that record, and **it carries the earlier text of this line**; `docs/designs/README.md` carries the same history."
- The table at `:787-801` has three columns — critique, verdict, amendment — and carries no prose. The earlier text of the Status line is in A12's entry at `:758`: "Until A3 the Status line read '**approved** — critique PASS at r2 …'".
- A15 moved the table out of that entry and left the pointer behind it, so the first line of the design sends a reader to the wrong paragraph for the one thing it promises them. `write-spec`'s bar is that a reader should not need to already know the answer.
- **Fix.** `:3`: "§14's table at the end is that record, and §14's A12 entry carries the earlier text of this line."

### m7 — the distinguished failed-fetch message is a rule-8 fix that no row pins

`:214`, `:308`, against `:506`, `:522`, `:537`.

- A15's m4 answer adds a user-facing distinction: under one reason, `EvalAwaitingCard`, "The failed-fetch message names the error class", against a digest mismatch's message, because "Reporting a mismatch for a refused connection names a check that was never made".
- The interval half is pinned — `:522` asserts the zero value resolves to `CardRetryInterval` "after a failed run-start fetch". The message half is pinned nowhere. `:537` builds only the mismatch: "a run-start card that differs from the recorded digest reads `EvalAwaitingCard`". `:506` builds the mismatch and the unusable interface. No row builds a refused connection at run start.
- So a build that collapses both causes onto one message — the exact state A13 was in — passes every owed row. That is rule 8's case, and the design's own standard for this table is "Every case carries a mutation that must kill it".
- **Fix.** `:537`: add "a run-start fetch refused at the connection also reads `EvalAwaitingCard`, and its message names the error class rather than a digest mismatch", with the mutation "one message for both causes".

## Considered and rejected

- **The requeue field does not need to be three fields.** Set to one value it collapses every branch, and no owed row needs two branches at different shortened values or one shortened beside one long. Row `:508` sits only in the final-verdict branch and row `:535` only in the read-back-failure branch. Three fields would also empty the unit row at `:522`, whose whole content is that one zero value resolves three ways.
- **Clearing `at` does not weaken the read-back's no-churn argument.** The eleventh critique's MAJOR 1 fix asked for `score` and `at` in the read-back list, and A15 declined, on its own m5 reasoning. That is the right call, not a half answer: a merge patch's clear and a stale CRD's prune return the same absence, so the read-back could not tell them apart. The no-churn property the read-back row rests on survives because a clear is idempotent (`:244`).
- **`:141`'s "the backstop on this branch is `CardDriftInterval`" is not wrong for a failed fetch.** That sub-case backs off at 15 s, tighter than the stated backstop, so the bound holds as an upper bound. The sentence reads as exact, but nothing it claims is false.
- **Row `:508`'s "with no Agent event" is not disarmed by the Agent reconciler reading the suite.** The clause is a stipulation the test enforces, in the same way row `:535` stipulates "the eval controller runs alone, without the Agent reconciler". How it is enforced is the implementer's.
- **The A14 gap is coherent.** `:784` states it plainly — the number belonged to the approval's log entry while it sat on A13, and re-applying the approval over A15 moves it to A16 — and no other text in either file references an A14. `docs/designs/README.md:24` skips it the same way.

## What was verified

- **The protocol claim, at source, not from the previous record.** `specification/a2a.proto` at tag `v1.0.1` was fetched and read. `message SendMessageConfiguration` (`:143-161`) has `accepted_output_modes = 1`, `task_push_notification_config = 2`, `optional int32 history_length = 3`, and **`bool return_immediately = 4`** — the fourth field, as the research document says — with the comment, verbatim: "If `true`, the operation returns immediately after creating the task, even if processing is still in progress. If `false` (default), the operation MUST wait until the task reaches a terminal (`COMPLETED`, `FAILED`, `CANCELED`, `REJECTED`) or interrupted (`INPUT_REQUIRED`, `AUTH_REQUIRED`) state before returning." The research document's quotation is exact, the proto3-default reasoning is right, and `SendMessageRequest` (`:648-658`) carries `tenant`, `message` (REQUIRED), `configuration`, `metadata`, which is §2's existing list.
- **The research document's addition is in its style.** A bold lede, field-level detail, the normative text quoted rather than paraphrased, and a dated provenance parenthetical naming what was read and why it was added. It sits in §2 beside the `SendMessageRequest` field list it corrects, and it does not restate anything §2 already had. The document's "Primary sources, pinned" table already names `specification/a2a.proto` @ `v1.0.1` as the normative schema, so the citation resolves inside the document.
- **The three constants, and the absence of a field.** `AuthProbeInterval` (`authtxn.go:50`), `CardRetryInterval` (`card.go:81`), `CardDriftInterval` (`card.go:309`), each a `const`; `AgentReconciler` has `CardFetchTimeout`, `CardClient`, `AuthProbe`, `AuthDeadline`, `NackPageSize`, `NackMaxPages` and no interval field.
- **The `AuthDeadline` precedent is quoted correctly** (`authtxn.go:45`, `agent_controller.go:121-123`).
- **The two stage regressions m1 adds are real.** A fresh NACK at `ProbingAfter` sets `tx.Stage = StageConverging` (`authtxn.go:1698-1703`); a policy found not intact sets `tx.Stage = StageApplyingPolicies` (`authtxn.go:1689-1695`). Both fail clause 2, and neither is advancing past `ProbingAfter`.
- **The back-dating technique exists** (`test/envtest/card_test.go:193-199`), and `cardFetchDue` reads the re-read as due afterwards (`card.go:327-341`).
- **`writeStatus` returns early on an equal status** (`agent_controller.go:1783-1785`), which is what makes m2's "the window is one Agent event" true and what lets rows `:535` and `:538` bound the resourceVersion.
- **The printer column and the stale-score safeguard** are exactly as `:211` quotes them (`agent_types.go:634-635`, `:640`, `:839`).
- **The field table matches the Go type.** Five fields today, eight new, thirteen in the table, and the run-start enumeration accounts for each exactly once.
- **`docs/designs/README.md:24` is current and agrees with the design**: the eleventh critique's verdict, answered by A15, awaiting its twelfth. No stale line-number citation was introduced anywhere in the diff.
- **Rule 7.** Every new claim A15 makes about existing code is true. The only bound that is loose rather than wrong is `:141`'s, above.
- **Rule 8.** A15's new `EvalAwaitingCard` message split is itself a rule-8 fix and is right. The one residue is that no row pins it — m7.

## Noted, not counted

- **`:232`'s pacing requeue is a 5 s literal, not `AuthProbeInterval`.** A pass that *sent* a case returns `RequeueAfter: 5s`, which the requeue field does not cover and no row shortens. It predates A15 and nothing rests on it, but two 5 s values in one controller, one a constant and one a literal, will read as one to whoever implements it.
- **Nothing pins "both controllers read suites uncached".** A cached read in the Agent reconciler would be caught in production by the `get`-only Role and nowhere in envtest, which runs as admin. The honest statement is that this axis is unmeasurable at the layer the slice tests in; a cache-inspection assertion would be the only pin.
- **`:143`'s "a second uncached `get` per Agent per 5 s" is the worst case**, not the rate: the Agent reconciler's pass rate beside a `Lock` at `ProbingAfter` is `AuthProbeInterval`, but off a transaction it is the card drift rate. The sentence scopes itself with "beside a `Lock` probing at 5 s", so it is right as written.
- **The schema check is still one level deep for `cases` and zero for `blockedFor`**, as the eleventh critique noted. Unchanged, and still unreachable while the slice ships all subfields in one generated CRD.
- **§2's doctrine gates still describe the full design**, and `:400` still owes "the operator's A2A client" without answering "bind don't build" against the official Go SDK the project's own research pins. Both were noted by the eleventh critique and neither is A15's to close.

## The smallest set of changes

1. `:400`: "Two owed rows", not four (m1).
2. `:115`: say the field governs every requeue the eval controller returns, naming `:196`'s and step 11's (m2).
3. `:535`: give the fetch bound a floor (m3).
4. `:498`: hold the first case at the stub, so the empty-`score` window is the test's (m4).
5. `:762` and `:767`: correct A13's entry beside the correction A15 already made to A12's (m5).
6. `:3`: point at A12's entry for the earlier Status text, not at the table (m6).
7. `:537`: build a refused run-start connection and assert its message (m7).

**design 16 §1.1 (A15): PASS — 0 BLOCKER, 0 MAJOR, 7 MINOR.** The slice is ready for the human's decision on Q1–Q13.
