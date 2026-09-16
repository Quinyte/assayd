# Critique: design 16 A8 — the first slice (§1.1), revised a fifth time

- **Date**: 2026-09-14
- **Target**: `docs/designs/16-evalsuite.md` §1.1 and amendment A8, at `42ee095` (branch `design-16-first-slice`). A8 answers `reviews/16-a7-critique.md` (REVISE: 0 BLOCKER, 1 MAJOR, 3 MINOR). The earlier records are `reviews/16-a3-critique.md` to `reviews/16-a6-critique.md`.
- **Independence**: an independent session. It wrote none of A4 to A8 and none of the five earlier critiques. A forked first pass ran with the `critique-design` skill, and every claim it made was then traced again by hand. Where the two passes disagree, this record gives the reason.
- **Read against**:
  - design 03 §3.3.3: the `Lock` of a missing policy, the route re-create and its displacement of that `Lock` (`03-policy-compiler.md:804`), the deadline table (`:788`), and the failure-mode rows (`:1310-1311`);
  - design 02 §3.4, and its read-access row (`02-agent-crd-operator.md:144`);
  - the code:
    - `internal/controller/authtxn.go`: `authStep`, `isReCreation`, `abandonWaiting`, `reconcileServed`, `recreateRoute`, `lockMissingPolicy`, `runLock`, `lockServed` and `cardAttributes`;
    - `internal/controller/agent_controller.go`: the card fetch and the rollout switch;
    - `internal/controller/card.go`: `recordCardAttempt` and `cardFetchDue`;
    - `charts/assayd/templates/rbac.yaml` and `admission.yaml`.
- **Controller-runtime v0.24.1, read from the module cache**:
  - `pkg/controller/controller.go`: the priority-queue default;
  - `pkg/internal/controller/controller.go`: `Start`, the queue shutdown and `processNextWorkItem`;
  - `pkg/controller/priorityqueue/priorityqueue.go`: `GetWithPriority` and `ShutDown`.
- **Method**: every claim A8 makes about the code or controller-runtime was traced to its source. Nothing was run or mutated, because nothing in the slice is implemented.

**Verdict: REVISE — 0 BLOCKER, 1 MAJOR, 5 MINOR.**

A8 closes S1 as it chose to: the exemption now selects exactly J2 and K2 `Lock`s, and for those two kinds the reading of `status.activeRevisionDigest` is sound. The wedge A8 then states is real, and its main exit works in the code. But the text misstates the wedge in three ways: its harm, who can take its exit and what that exit costs, and which revision's card decides it. That is the MAJOR. The MINORs are three test rows, the reasoning (not the conclusion) of the safe-superset bullet, and one sentence about the queue.

## Closure of the fifth critique

| Finding | A8's answer | Closed? |
|---|---|---|
| S1 (MAJOR): the exemption failed for the missing-policy `Lock` | Option 2: the exemption is limited to J2 and K2 by `isReCreation`'s test negated; the wedge is stated with its route-deletion exit; composition, dependencies and Q12 are corrected; three rows are rebuilt (`16-evalsuite.md:96-117`, `:219`, `:243`, `:317`, `:385`, `:432-433`, `:448`) | **Closed as a predicate.** The added clause equals `!isReCreation` for a `Lock` (`authtxn.go:791-797`). A missing-policy `Lock` records `targetMode` equal to the recorded mode (`:1568-1569`), so it is excluded. The wedge is stated wrongly (M1), and two of the three rows cannot be built as written (m2, m3) |
| s2: "exactly", the card re-read exit, and the count | Safe superset; re-read exit dropped; two patches per case plus one per run start (`:103-104`) | **Closed in substance.** Dropping the re-read exit is right. The superset's reasoning is partly wrong, and one patch is uncounted (m5) |
| s3: the shutdown row could not kill its mutation | A hook holds the second Agent's pass between its live read and its claim; the text states that queued Agents get no pass (`:154`, `:426`) | **Closed.** The row kills its mutation. The queue sentence is slightly too strong (m6) |
| s4: the read-back row's bounds | The eval controller runs alone; the cost counts other Agent events (`:161`, `:438`) | **Closed for the stated gap.** The same row has an older defect in how it simulates a stale CRD (m4) |

## What was verified

- **The predicate selects exactly J2 and K2.**
  - The three `Lock` kinds are told apart by stored fields. A missing-policy `Lock` has mode `apikey` and target `apikey` (`authtxn.go:1568-1569`). J2 has mode `none` and target `apikey`. K2 has no recorded mode.
  - "`status.auth.mode` is empty, or differs from `targetMode`" is therefore true for J2 and K2 only. It is `isReCreation` negated, which also returns false on an empty mode (`:792`).
- **For J2 and K2, `status.activeRevisionDigest` is the revision attribution reads, and the re-pointing claim is true.**
  - `runLock`'s non-re-creation branch calls `ensureServingRoute` on `status.ActiveRevision` on every pass (`authtxn.go:1616-1620`), before the probe and before `cardAttributes` (`:1757`). It clears `beforeObserved` and `beforeRevision` when the one `backendRef` moves (`:1631-1636`).
  - The rollout switch assigns the active revision (`agent_controller.go:786`) before `reconcileGateway` runs (`:804`). So the promoting pass re-points the route and can credit in the same reconcile.
- **Nothing moves the missing-policy `Lock`'s route.**
  - `authStep` sends any recorded `Lock` to `runLock` (`authtxn.go:705-707`), so `reconcileServed` is not reached while one is recorded.
  - `runLock` takes the route as found for a re-creation (`:1602-1614`), and `lockServed` skips the route for one (`:1891`).
  - After `Served`, `reconcileServed` re-ensures the route on the active revision (`:1524-1525`).
- **Deleting `<agent>-serving` does hand over, as stated.** `runLock`'s re-creation branch finds the route absent or unpublished and calls `recreateRoute` (`:1608`, `:1550-1560`), which displaces the `Lock`, as design 03 §3.3.3 specifies (`03-policy-compiler.md:804`). That `Create` probes a prepared route with no `backendRefs`, so it needs no card, and it publishes on `status.activeRevision`.
- **Removing the gate and pinning are not exits.** Both move `status.activeRevision` or let a candidate promote, and neither moves this `Lock`'s route. After that, the old revision is no longer desired, so its card is never re-read (`agent_controller.go:608`, `cardFetchDue` on the desired digest).
- **Reverting the spec is an exit only for a transient card failure.** It makes the serving revision desired again. Its cleared digest (`card.go:389`) is then retried every `CardRetryInterval`, 15 s (`card.go:337`, `:81`). A lasting failure never records a card.
- **"The card re-read exit" was rightly dropped.** Only the desired revision's card is fetched (`agent_controller.go:608`).
- **`AuthPolicyMissing` pages from entry** (`authtxn.go:1814-1829`; `03-policy-compiler.md:788`), as `:109` says.
- **The shutdown row works.**
  - The default queue is the priority queue: the manager's config is copied in (`pkg/controller/controller.go:137-138`), `ptr.Deref(options.UsePriorityQueue, true)` selects it (`:260`), and nothing in `cmd/` or `internal/` sets `UsePriorityQueue` or `NewQueue`.
  - With `MaxConcurrentReconciles: 2`, a hook that holds the second pass before its `ctx.Err()` check, then a cancel, then a release, leaves a correct implementation with no claim. The named mutation writes one on the uncancelled context. The row kills it.
- **The read-back row's new bounds are right once the eval controller runs alone.** Six fetches in 10 s at a 2 s interval covers the first fetch and five requeues.
- **Q12's rewrite is right.** The missing-policy wedge stands under either answer, because a claim after the deadline would earn a promotion that still does not move the route. Q1–Q12 are still twelve, and A8 adds none.
- **Rules 7 and 8.** A8 states no new bound that nothing enforces. The one new user-facing string, `EvalWaitingForAuth`'s exit, is M1(b) and M1(c).

## New findings

### M1 — MAJOR: the wedge is misstated in its harm, its exit, and the revision it keys on

`16-evalsuite.md:108`, `:112`, `:116`, `:219`, `:243`.

A8's own framing is that design 03 owns the wedge and the human decides whether to amend design 03. That choice is not a defect. But §1.1 now asks the human to accept the wedge in the slice, and its description of the wedge is wrong in three ways.

**(a) The harm. `:108` says the route "keeps serving with no `<agent>-auth` on it, so it is open". In the wedge state that is false.**
- The wedge is a missing-policy `Lock` at `ProbingAfter`. That `Lock` enters at `ApplyingPolicies` with `Written: true` (`authtxn.go:1568-1569`), and it writes the policy there (`:1678`). It confirms that the route and the policy have converged (`:1711`) before it moves to `ProbingAfter` (`:1717`).
- So in the wedge, `<agent>-auth` is in place and has been accepted, and an anonymous probe gets `401`. What is missing is proof that the `401` came from the policy.
- The code's own message says the policy "has been re-created" (`:1816`). §1.1 contradicts itself: the second clause of the exemption, three bullets earlier (`:96-101`), says that at this stage "the policy is already written and converged".
- The error matters because of what the text does with it. It recommends an exit that takes the Agent off the air, justified by a route that is said to be open when it is probably enforcing.

**(b) The exit. `:112` and the message at `:219` name a delete the addressee cannot make, and give it a bound it does not have.**
- `<agent>-serving` lives in `assayd-run-<ns>`. Users hold no rights there by default: "the operator does not grant them any", and run-namespace objects are "reachable only by a cluster administrator" (`02-agent-crd-operator.md:144`). The chart's RBAC grants the operator only (`charts/assayd/templates/rbac.yaml`). The admission policy covers only `CREATE` and `UPDATE` on `httproutes` (`admission.yaml:95`), so whether a delete is allowed is decided by RBAC alone.
- `EvalWaitingForAuth` is read by the Agent's owner, who by default cannot take "the one working exit". Q12's cost sentence already says "until an administrator clears the cause" (`:385`). `:112` should say the same.
- `:112` says the outage lasts "from the delete until `Create` publishes". Under a Gateway-level hold, the re-created route stays unpublished until that policy goes (`03-policy-compiler.md:804`, `:1311`; ADR-0034 Amendment 5). So the outage has no bound.

**(c) The revision. "Whose serving revision has no recorded card" (`:219`) and "revert the spec to the serving revision" (`:116`) do not say which revision, and the design's own composition case sets the two apart.**
- Attribution reads the revision the route names (`cardAttributes`, `authtxn.go:1956-1974`).
- `:243` describes the natural case: "a missing-policy `Lock` that starts while a passed candidate awaits its promoting pass". In the code, that `Lock` is entered in the promoting pass itself, after the switch. The switch sets `activeRevision` to C (`agent_controller.go:786`). Then `reconcileServed` finds the policy absent and enters the `Lock` (`authtxn.go:1515`) before it would re-ensure the route (`:1525`). The route still names R.
- If R has no recorded card, that `Lock` wedges. Meanwhile `status.activeRevision` is C, whose card the fourth promotion condition guarantees.
- A message keyed on `status.activeRevision` then falls back to the generic text and names no exit. "Revert to the serving revision" points at C, where it does nothing. Reverting to R would work.
- `:243` says that in this case the route follows only "after the `Lock` reaches `Served`". It does not say that with R uncarded the `Lock` never gets there.

Why MAJOR: the implementer of `EvalWaitingForAuth` must guess which revision the condition tests, and the two readings give different messages on a reachable state (rule 8: name the real cause). And the paragraph offered to the human in place of a design 03 amendment states a route state that the code does not produce.

**Fix.**
- At `:108`, say what is true: the policy is re-written and converged, and its enforcement cannot be attributed. The route answers `401` to anonymous callers, but the operator cannot prove the `401` is the policy's. It pages as `AuthPolicyMissing`, under design 03's message.
- At `:112` and in the message, name who can take the exit (a principal allowed to delete in the run namespace, by default an administrator). Add the Gateway-level caveat to the outage bound.
- Key the message, and the words "serving revision" at `:116`, on the revision the route's `backendRef` names, which is what `cardAttributes` reads, not on `status.activeRevision`.
- Add to `:243` the case where the route's revision has no card: the same wedge, with the same exit, entered in the promoting pass.

### m2 — MINOR: the wedge row (`:433`) fails on a correct implementation

The row makes a candidate available and carded, and only then deletes `<agent>-auth`.
- Until the delete, no transaction is recorded and every shared predicate holds. The eval claims cases, and "the stub sees zero hits" fails.
- If the run passes before the delete lands, C is promoted. `reconcileServed` moves the route to C (`authtxn.go:1525`), the later `Lock` credits on C's card, and the wedge is never reached.
- The row also clears "the serving revision's card digest" without saying how. Once a candidate exists, the serving revision is not desired, so no re-read can clear it (`agent_controller.go:608`).

**Fix.** Build the state in this order:
1. Clear R's card while R is still desired, with a failing card stub and `CardDriftInterval` shortened.
2. Delete `<agent>-auth`, and wait for the `Lock` to reach `ProbingAfter`.
3. Only then mint the candidate.

The mutation, dropping the J2-or-K2 clause, is then killed by the zero-hit assertion.

The J2 row (`:432`) is buildable, but it has the same ordering constraint and should state it: R's card is cleared by a failing re-read before the J2 edit, because after the edit R is not desired. Both of its mutations compile and are killed.

### m3 — MINOR: row `:448`'s second mutation survives, and the row depends on a rule the text does not state

- As m1's case shows, on the natural path the `Lock` is entered in the same pass as the promotion, after it (`agent_controller.go:786`, then `:804`; `authtxn.go:1515`). So "hold promotion while a `Lock` is recorded" never sees a recorded `Lock` when it promotes, and the promotion assertion does not catch it.
- The mutation is a plausible implementation, because the four promotion conditions (`:176-180`) say nothing about transactions.
- To build the `Lock` before the promoting pass, the policy must be deleted while the last case is in flight, so that the in-flight case records its outcome after the `Lock` is recorded. That rests on a rule §1.1 never states: whether the verdict write that follows an in-flight case is subject to the no-transaction rule. `:105` says only that the rule "stops new claims".
- **Fix.** State whether the verdict is written while a `Create` or `Lock` is recorded. If it is, build the row with the policy deleted during the last case, and keep the mutation. If it is not, drop the mutation, because no construction reaches it.

### m4 — MINOR: the read-back row (`:438`) simulates a stale CRD in a way that fails a correct implementation

- The row strips the new `status.eval` fields "from every patch response". The envtest API server has the new CRD, so it still stores them.
- On the next pass, the uncached live read sees `verdict: Running` and `endpointPath`, so the controller moves on to a claim. The claim stores `Sending` with a fresh nonce, which changes the resourceVersion. Its read-back fails, and the pass after that finds a claim with no outcome and records `Interrupted`.
- A correct implementation therefore breaks "the resourceVersion does not change after the first run-start write", and burns cases as `Interrupted`.
- **Fix.** Make the simulation faithful. Strip the fields from the patch request, so that nothing is stored, as a pruning CRD would. Or install an Agent CRD without the fields, which is possible now that the row runs the eval controller alone.

### m5 — MINOR: the safe-superset bullet reaches the right conclusion for partly wrong reasons (`:103-104`, against `:95` and `:385`)

- **Abandonment does finish on its own passes.** `abandonWaiting` re-checks every 30 s, and the abandonment completes once the finalizer lets go (`authtxn.go:937-953`). The conclusion, that a lost pass costs nothing, holds for another reason: abandonment has no deadline and is safe to re-run. Say that.
- **The Gateway-level case contradicts `:95`.** `:95` says a Gateway-level hold "holds the eval", and that "the candidate holds meanwhile, which is fail-closed". The superset now lets a J2 or K2 `Lock` held that way be exempt, so a candidate can be promoted while an A75 incident is open. That is the outcome Q12's reasoning argues against ("promoting a new revision beside it changes what the gateway serves while the incident is open"). This may be the right choice, because such a `Lock` still cannot finish without a promotion once the policy goes. But `:95` and `:385` must then carve it out, or the predicate must exclude a `Lock` whose `PolicyApplyIncomplete` reason is `GatewayAuthPolicy`.
- **The count.** "Two patches per case, and one per run start" leaves out the verdict write, unless the verdict is written in the last case's record patch. Say which.

### m6 — MINOR: "an Agent still queued when shutdown begins gets no pass at all" (`:154`) is slightly too strong

- When the manager's context is cancelled, a separate goroutine calls `Queue.ShutDown()` (`pkg/internal/controller/controller.go:343-346`).
- Between the cancel and that call, a worker returning from a pass calls `GetWithPriority` while `shutdown` is still false (`priorityqueue.go:425`). It can receive a queued item, with `shutdown` false (`:445-446`), and run that item's reconcile on the cancelled context.
- After `ShutDown`, an item received returns `shutdown` true, and the worker stops (`:446`; `controller.go:419-424`). So the sentence is true only from `ShutDown` onwards.
- Nothing breaks: the `ctx.Err()` check also covers a pass that starts in that window. The sentence's conclusion, "the check guards only a pass that is already running", should be widened to include it.
- The row (`:426`) should also say that the hook sits before the `ctx.Err()` check. A hook placed between the check and the claim would make a correct implementation claim.

## Noted, not counted

A8's log says the orchestrator chose option 2, and §1.1 leaves a design 03 amendment to the human (`:117`, `:317`). The brief for this critique rules out counting that choice as a defect. The human may still want it asked directly. `:16` promises that "wherever another answer would change a rule, the rule names its question", and AGENTS.md:69 asks that no choice be silent. A Q13, "accept the missing-policy wedge in the slice, or amend design 03 first", with a recommendation, would record it where the other twelve decisions are.

## The smallest set of changes to approvable

1. **M1.**
   - Correct `:108`: the policy is in place, and its enforcement is unattributed.
   - Name the administrator as the one who can take the exit, and add the Gateway-level caveat to the outage (`:112`, and the message at `:219`).
   - Key the message and the revert exit on the revision the route names.
   - Add the uncarded-route-revision case to `:243`.
2. **m2 to m6.** A sentence each, plus these row constructions:
   - Order the wedge row: clear R's card, delete the policy, reach `ProbingAfter`, then mint the candidate. State the same order in the J2 row.
   - State whether a verdict is written while a transaction is recorded, and build row `:448` to match, or drop its second mutation.
   - Strip the fields from the request in the read-back row, not from the response.
   - Give the right reason for abandonment, and reconcile the Gateway-level case with `:95` and Q12.
   - Widen `:154`'s queue sentence to cover the window before `ShutDown`, and place the hook before the check.

**design 16 §1.1 (A8): REVISE**
