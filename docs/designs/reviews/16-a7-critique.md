# Critique: design 16 A7 — the first slice (§1.1), revised a fourth time

- **Date**: 2026-09-14
- **Target**: `docs/designs/16-evalsuite.md` §1.1 and amendment A7, at `8774d48` (branch `design-16-first-slice`). A7 answers `reviews/16-a6-critique.md` (REVISE: 0 BLOCKER, 1 MAJOR, 4 MINOR). The earlier records are `reviews/16-a3-critique.md`, `reviews/16-a4-critique.md` and `reviews/16-a5-critique.md`.
- **Independence**: an independent session. It wrote none of A4 to A7 and none of the four earlier critiques.
- **Read against**:
  - ADR-0024 and ADR-0030;
  - design 02's rollout and status;
  - design 03 §3.3.3: `ProbingBefore`, `ProbingAfter`, the attribution of a `401`, `beforeObserved` and `beforeRevision`, the `Lock` of a missing policy, and abandonment; and design 03 §5's rows;
  - the code:
    - `internal/controller/authtxn.go`: `authStep`, `abandon`, `abandonWaiting`, `persistStatus`, `reconcileServed`, `recreateRoute`, `lockMissingPolicy`, `runLock`, `lockServed`, `beforeRevisionFor` and `cardAttributes`;
    - `internal/controller/agent_controller.go`: the card fetch, the rollout switch and `reconcileGateway`'s place after it;
    - `internal/controller/card.go`: `recordCardAttempt` and `CardDriftInterval`;
    - `internal/controller/httproute.go`: `ensureServingRoute` and `currentServingRoute`;
    - `cmd/operator/main.go`.
- **Controller-runtime v0.24.1 source, read from the module cache**:
  - `pkg/internal/controller/controller.go`: `Start`, the queue shutdown and `processNextWorkItem`;
  - `pkg/controller/priorityqueue/priorityqueue.go`: `GetWithPriority` and `ShutDown`;
  - `pkg/controller/controller.go`: the priority-queue default.
- **Method**: every claim A7 makes about code or about controller-runtime was traced to its source. Nothing was run or mutated, because nothing in the slice is implemented.

**Verdict: REVISE — 0 BLOCKER, 1 MAJOR, 3 MINOR.**

A7 answers all five of the fourth critique's findings, and four of them close. The exemption A7 adds for R1 holds for J2 and K2 `Lock`s: the route follows promotion, and the promoted revision's card lets the `Lock` credit its `401`.

It does not hold for the `Lock` of a missing policy, which is the case the fourth critique used as its example and the case the new envtest names. That `Lock` never moves the route. So a promotion leaves the route on the revision with no card, and the `Lock` still cannot credit its `401`. The exemption then turns itself off, because it reads `status.activeRevisionDigest` while the attribution reads the route. The eval then waits for good. That is the MAJOR. The MINORs are about how exact the predicate is, and two test rows whose construction cannot kill their mutations.

## Closure of the fourth critique

| Finding | A7's answer | Closed? |
|---|---|---|
| R1: a cycle between the eval and a card-less `Lock` | An exemption keyed on four status fields: kind `Lock`, stage `ProbingAfter`, `beforeObserved` false, and no recorded card digest for `status.activeRevisionDigest`. The contention argument, the "what it buys" argument, the corrected composition bullet and Q12, and two envtests (`16-evalsuite.md:96-105`, `:112`, `:207`, `:231`, `:373`, `:420-421`) | **Partly closed.** It closes for J2 and K2 ("Verified sound"). It does not close for the missing-policy `Lock`, whose route does not follow promotion (S1) |
| r1: the claim on a cancelled context, and "no new claim" | The claim, send and record run synchronously on `context.WithoutCancel`, and `ctx.Err()` is checked before the claim; the shutdown test gets a second Agent (`:142`, `:414`) | **Closed in the text.** The mechanism is sound against the queue's shutdown path. The second Agent in the test cannot kill its own mutation (s3) |
| r2: `GracefulShutdownTimeout` rests on a default | Set explicitly to 30 s in `cmd/operator/main.go`, with a unit test that it is at least 25 s; the bound is restated as 5 + 10 + 5 = 20 s; the chart floor is 25 s (`:142`, `:298`, `:304`, `:417-418`) | **Closed.** `main.go` sets no timeout today (`cmd/operator/main.go:151-162`), so the owed setting is real, and 20 s fits inside 25 s |
| r3: pacing after a failed read-back | `RequeueAfter: CardDriftInterval`, nothing more written, and `at` moved to the verdict write, so a repeated run-start write stores no change (`:149`, `:425`) | **Closed.** The test row's construction is short (s4) |
| r4: two test rows | The handover is built by the test's own write of `Interrupted`, and the refused-`Adopt` row asserts the route's `backendRef` (`:415`, `:419`) | **Closed.** `refuseAdopt` does re-ensure the route on `status.activeRevision` (`authtxn.go:2039`), so the skip mutation is killed by the `backendRef` assertion |

## New findings

### S1 — MAJOR: the `Lock` of a missing policy never moves its route, so the promotion that A7's exemption earns cannot finish it, and the exemption then switches itself off

`16-evalsuite.md:96`, `:103-104`, `:231`, `:305`, `:373`, `:420`, `:435`.

A7's argument rests on one sentence: "The slice's route has one `backendRef`, and it follows `status.activeRevision`" (`:96`). Then "The route's `backendRef` then names a revision whose card is recorded, and the `Lock` credits its next `401` on it" (`:104`). That is true of J2's and K2's `Lock`. It is false of the `Lock` of a missing policy:

- **`runLock`'s re-creation branch never writes the route.** For a J2 or K2 `Lock` it calls `ensureServingRoute` on `status.ActiveRevision` every pass (`internal/controller/authtxn.go:1618-1620`). For a missing-policy `Lock` it takes the route as found, `rt = cur` (`:1602-1614`), as its doc comment says: "It writes nothing to the route" (`:1578-1581`). `lockServed` skips the route for a re-creation too (`:1891-1895`).
- **Nothing else moves the route during that `Lock`.** `authStep` sends any `Lock` in the slot to `runLock`. `reconcileServed`, the path that re-ensures a served Agent's route on the active revision (`:1524-1526`), is reached only when the slot holds no transaction. `ensureServingRoute` has no other caller that runs while a `Lock` is recorded (`grep ensureServingRoute( internal/controller`).
- **The attribution reads the route, not status.** `cardAttributes` matches `status.cards[]` against the route's `backendRefs` by revision name (`:1956-1974`).

Walk row `:420` through the code. The serving revision R has its card cleared, and `<agent>-auth` is deleted. The missing-policy `Lock` writes the policy, converges, and reaches `ProbingAfter` with a `401` it cannot attribute. The owner mints candidate C. The four clauses hold, so cases are claimed, C passes, and the rollout switch promotes C (`agent_controller.go:783-786`). Then:

1. `status.activeRevision` is C, but the route still names R.
2. `cardAttributes(rt)` looks for R's card, finds none, and the `401` is still not credited.
3. The exemption's fourth clause reads `status.activeRevisionDigest`, which is now C, and C's card is recorded. So the clause is false and the exemption ends.
4. R is no longer the desired revision, so its card is never re-read (`agent_controller.go:608`, `cardFetchDue` on `desiredDigest`).

The `Lock` is now wedged for good, and so is the gate. Every later candidate reads `EvalWaitingForAuth`, because the recorded `Lock` is no longer exempt. The route keeps serving R, the revision that is no longer active, and the promoted C serves nothing. The fourth critique's harm is back, in the case it was written for: a route whose authentication is unproven, whose remedy cannot land.

Three stated guarantees are false as a result:

- **Row `:420`** fails against the code as it stands. Its expected end state, "the `Lock` credits its `401` on the candidate's recorded card and reaches `Served`", cannot be reached.
- **Row `:435`** (from A5 and A6, not new) cannot be killed by its mutation. A missing-policy `Lock` has no `ProbingBefore`, so its `beforeObserved` is always false (`lockMissingPolicy`, `:1568-1569`), and "skip clearing `beforeObserved` on a gate's promotion" changes nothing. If R's card is recorded, the `Lock` credits on R, the revision the route still names, not on "the new revision's recorded card digest" as the row asserts. Composition bullet `:231` makes the same claim in prose.
- **"Design 03: nothing new"** (`:305`) and Q12's "The one wedge a new revision would clear … is exempt from the wait" (`:373`) are false for this `Lock`.

The gap is partly design 03's own. Design 03 §3.3.3 states the rule generically: "The slice's serving route has exactly one `backendRef`, and it follows `status.activeRevision`" (`03-policy-compiler.md:776`). The code does that for J2 and K2 only. On an ungated Agent the same state already wedges: a promotion during a card-less missing-policy `Lock` leaves the route on the old revision until an administrator acts. That is out of this slice's scope. What is in scope is that §1.1 builds its answer to R1 on it, and says design 03 owes nothing.

Under the standard this is MAJOR on both counts. An implementer must guess whether to change design 03's approved `runLock` to make row `:420` pass. And §1.1 states a guarantee that the code it cites does not keep.

**Fix.** Choose one, and say which:

1. **Owe design 03 an amendment.** The missing-policy `Lock` re-asserts the serving route on `status.activeRevision` every pass, as J2's and K2's already do. That is safe for the same reason it is safe for J2: the policy targets the route, so moving the `backendRef` changes which revision answers, not what the route admits. List it under "What the slice depends on" in place of "nothing new", and make row `:420` depend on it. Design 03's slice is approved by the human (ADR-0034 Amendment 4), so the amendment is theirs to approve, through design 03's own process.
2. **Exempt only J2 and K2.** Add `status.auth.mode` differs from `targetMode`, which is `isReCreation`'s test read from status. State the missing-policy wedge at `:96`, at `:231` and in `EvalWaitingForAuth`'s message. Name its one exit that works today: deleting `<agent>-serving` displaces the `Lock` with a route re-create (`recreateRoute`, `:1550-1560`). That `Create`'s `401` needs no card, and its `Publishing` names `status.activeRevision`. Removing the gate does not help, because the route still does not move. Re-argue Q12's cost.

Either way:

- key the exemption's fourth clause on the revision `cardAttributes` reads, or state why it equals `status.activeRevisionDigest` (true only once option 1 holds);
- rebuild row `:435` for a J2 `Lock` whose `beforeObserved` is set, where the mutation can bite, or give the missing-policy half an assertion on the route's `backendRef`;
- add to row `:420` the assertion that the route's `backendRef` names C, with the mutation "the missing-policy `Lock` does not re-assert the route" (option 1), or the assertion of the stated exit (option 2).

### s2 — MINOR: the predicate is not "exactly" the cycle state, and the argument names an exit that cannot happen while cases are claimed

`16-evalsuite.md:92`, `:96-103`.

- **It matches more than the cycle.** An abandoned J2 or K2 `Lock` that waits for a finalizer keeps its kind and stage in the slot (`abandonWaiting`, `authtxn.go:941-954`). So a `Lock` abandoned at `ProbingAfter`, not observed and with no serving card, meets all four clauses. `:92` says "A `Create` or `Lock` being abandoned counts too", so the two rules conflict. The predicate also matches a `Lock` held at `ProbingAfter` by a Gateway-level policy (A75) or by a foreign traffic policy on the route. In each of those cases the `Lock`'s own passes cannot finish it either, and a lost pass costs nothing. So the extra reach is harmless, but "exactly this state" (`:96`) is not true. Say that the predicate is a superset and why the superset is safe, or exclude abandonment by name. Abandonment is judged from the spec, not from status alone.
- **The card re-read exit does not exist while cases can be claimed.** `:96` and `:103` say the `Lock` can also finish "after a card re-read succeeds", after which the exemption ends. But the Agent reconciler fetches only the desired revision's card (`agent_controller.go:608-630`). Cases are claimed only while a candidate differs from the active revision (`:112`), so during every claim the serving revision is not desired, and its card is never re-read. That makes the exemption more necessary than A7 says, not less. State that, while a candidate exists, the only exits are a promotion, a pin, and removing the gate.
- **"At most one probe interval … per recorded outcome"** (`:103`) undercounts. A claim and a record are two patches per case, so a case can cost the `Lock` two passes, plus one for the run-start write.

### s3 — MINOR: the shutdown row's second Agent cannot kill its own mutation, because the queue drops queued items at shutdown

`16-evalsuite.md:142`, `:414`.

Row `:414` queues a second gated Agent "before the manager's context is cancelled", asserts that it gets no claim, and names the mutation "claim on the uncancelled context without the `ctx.Err()` check". Against controller-runtime v0.24.1 that mutation survives:

- **After cancellation, a queued item is never handed out.** On `ctx.Done()` the controller calls `Queue.ShutDown()` (`pkg/internal/controller/controller.go:343-346`). The default priority queue (`pkg/controller/controller.go:253`) sets `shutdown` there (`priorityqueue.go:475-478`). `GetWithPriority` returns `shutdown` at once when it is set (`:424-428`), and `processNextWorkItem` then stops (`controller.go:419-424`). So the second Agent gets no pass, and so no claim, with or without the check.
- **Before cancellation it is taken at once.** With `MaxConcurrentReconciles: 2` and the first worker holding the stub, the second worker takes the queued Agent immediately. It claims before the cancel, and a correct implementation then fails the row.

The `ctx.Err()` check guards only a pass that is already running between its live read and its claim when the cancel lands. That is the r1 window, and it is real. **Fix:** build the row with an injected hook that holds the second Agent's pass after its live read until the context is cancelled, then releases it. Its assertion is that no claim is written. Also state at `:142` that queued Agents get no pass after shutdown begins, which is verified here, so the check covers only a pass in flight.

### s4 — MINOR: the read-back row's bounds depend on whether the Agent reconciler runs, and the row does not say

`16-evalsuite.md:149`, `:425`.

Row `:425` shortens `CardDriftInterval` to 2 s. It then bounds the stub's card fetches at 6 over 10 s, and asserts that the Agent's resourceVersion does not change after the first run-start write. That is fine if the Agent reconciler is not running. If it is, and "while the schema check is not in the path" leaves that open, its own drift re-read of the candidate's card also hits the stub every 2 s, and each success restamps `fetchedAt` in `status.cards[]`. So:

- the stub counts the Agent reconciler's fetches too;
- the resourceVersion changes every 2 s;
- each change enqueues the eval controller, whose `For(&Agent{})` has no generation predicate, for one more card fetch.

A correct implementation then fails both bounds. **Fix:** say that the row runs the eval controller alone, or count only fetches made by the eval client and drop the resourceVersion bound in favour of "no write by the eval controller". The cost sentence at `:149`, "at most one card fetch and one no-op patch per gated Agent per `CardDriftInterval`", has the same gap: any status write by the Agent reconciler also triggers an eval pass. That cadence is bounded, because on a stable Agent it is the card drift re-read, but the sentence should say "per `CardDriftInterval` and per Agent event".

## Verified sound

- **For J2 and K2, the exemption breaks the cycle, and the `Lock` finishes by design 03's rule.**
  - `runLock` re-asserts the route on `status.ActiveRevision` every pass (`authtxn.go:1618-1620`). It clears `beforeObserved` and `beforeRevision` when the `backendRef` no longer names `beforeRevision` (`:1631-1636`), and `cardAttributes` then credits on the promoted revision's card.
  - The fourth promotion condition records that card in the promoting pass. The rollout switch runs before `reconcileGateway` (`agent_controller.go:783-804`), so the pass that promotes can credit the `401` in the same reconcile.
  - The exemption's fourth clause keys on `activeRevisionDigest`, and for these two kinds that is the revision the route names by the time the attribution reads it.
- **The stage and `beforeObserved` clauses exclude what they should.**
  - `ApplyingPolicies` and `Converging` are excluded, so N4's contention cannot delay the write that closes the route. A NACK or a policy found changed moves the stage back out of `ProbingAfter` (`:1688-1702`), which ends the exemption.
  - A J2 or K2 `Lock` with `beforeObserved` set can credit on its own passes while the route names `beforeRevision`, so it is rightly not exempt.
- **The eval's patches cannot interfere with attribution.** The eval controller writes only `status.eval`. Attribution reads `status.cards[]`, `beforeObserved` and the route, and none of them is in `status.eval`. The Agent reconciler's writes are full `Update`s at the resourceVersion it read (`persistStatus`, `:1017-1033`), so an eval patch costs it a pass and never loses a field.
- **No other cycle between the eval and a design 03 transaction.**
  - `Create` and the route re-create probe a prepared route with no `backendRefs`, so they need no card and no promotion.
  - The I1 hold comes before the gate.
  - A75's hold ends when the policy is removed, not with a promotion, so the eval waiting on it is Q12's stated cost, not a cycle.
  - A refused `Adopt` does not hold the eval, and its route follows promotion (`:2035-2043`).
  - The one other cycle is S1's, and it comes from the route, not from the predicate.
- **r1's mechanism is sound.** No pass starts after cancellation, because the queue is shut down (s3). A pass past its `ctx.Err()` check claims, sends and records on an uncancelled context, synchronously, and `Start` waits for its workers (`controller.go:324-326`). The 20 s bound fits inside an explicit 30 s timeout and the chart's 30 s grace period.
- **r3's fixed point holds.** On a stale CRD, a repeated run-start write carries the same values once `at` moves to the verdict, so the API server stores no change and the write itself does not re-enqueue the Agent.
- **The twelve questions are complete for the slice**, apart from S1. If option 1 is taken, S1 adds a decision the human owns on design 03's approved slice. Q12's recommendation stands once its cost sentence is corrected for the missing-policy `Lock`.
- **Rule 7 and mutations.** Every other owed row has a mutation that kills it. The exceptions are `:414`'s second mutation (s3), `:420` and `:435` (S1), and `:425`'s bounds as built (s4).

## The smallest set of changes to approvable

1. **S1.** Either owe design 03 an amendment that makes the missing-policy `Lock` re-assert the serving route on `status.activeRevision`, or exempt only J2 and K2 and state the missing-policy wedge with its route-deletion exit. Correct `:96`, `:104`, `:231`, `:305` and Q12's cost sentence to match. Key the fourth clause on the revision the attribution reads, or argue their equality. Rebuild rows `:420` and `:435` so each asserts the route's `backendRef`, and give each a mutation that can kill it.
2. **s2–s4.** A sentence each, plus two row constructions:
   - s2: call the predicate a safe superset, or exclude abandonment; drop the card re-read exit while a candidate exists; count two patches per case.
   - s3: hold the second Agent's pass between its live read and its claim with a hook, and state the queue's shutdown behaviour.
   - s4: run the read-back row without the Agent reconciler, or count only the eval client's fetches; restate the cost as per `CardDriftInterval` and per Agent event.
