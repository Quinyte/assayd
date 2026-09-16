# Critique: design 16 A10 — the first slice (§1.1), revised a seventh time

- **Date**: 2026-09-14
- **Target**: `docs/designs/16-evalsuite.md` §1.1 and amendment A10, at `729c920` (branch `design-16-first-slice`), read as `git diff dfe9509 729c920`. A10 answers `reviews/16-a9-critique.md` (REVISE: 0 BLOCKER, 1 MAJOR, 6 MINOR). The earlier records are `reviews/16-a3-critique.md` to `reviews/16-a9-critique.md`.
- **Independence**: an independent session. It wrote none of A4 to A10 and none of the seven earlier critiques. A forked first pass ran with the `critique-design` skill. Every claim it made was traced again by hand before it was counted. Where the two passes disagree, this record says so and gives the reason.
- **Read against**:
  - `internal/controller/authabove.go`: `gatewayAuth.stands()` (`:101-103`), `gatewayAuthPolicies` (`:199-247`).
  - `internal/controller/authforeign.go`: `foreignTrafficPolicies` (`:37-69`), `policyPageSize` (`:72`), `listPolicies` (`:82-102`), `servingRouteLabels` (`:107-121`).
  - `internal/controller/authtxn.go`:
    - the deadline and intervals (`:46`, `:50`);
    - `reconcileGateway` (`:287-333`), `reportAboveServed` (`:354-387`), `storedHold` and `seedStoredAbove` (`:393-420`);
    - `gatewayStep`'s foreign read (`:609-618`);
    - `runLock` (`:1589-1850`): the re-creation branch (`:1602-1613`), `ProbingBefore` (`:1654-1674`), the A75 read at `ProbingAfter` (`:1723-1742`), the second read after a creditable `401` (`:1763-1773`), the requeue (`:1790-1793`), and the J2/K2 tail (`:1831-1848`);
    - `cardAttributes` (`:1956-1974`).
  - `internal/controller/runnamespace.go`: `reader()` (`:847-852`).
  - `internal/controller/agent_controller.go`: `DefaultRevisionHistoryLimit` (`:65`), `ensureService` for the desired revision only (`:575`), `collectGarbage` (`:1552-1600`), `WorkloadName` (`:1795`), the HTTPRoute watch (`:1872`).
  - `internal/controller/httproute.go`: `routePublished` (`:334-341`).
  - `internal/controller/card.go`: `CardRetryInterval` (`:81`), `pruneCards` (`:404-417`).
  - `internal/controller/service.go`: `collectRevisionServices` (`:200-214`).
  - `internal/controller/gatewaywiring.go`: `GatewayWatchCacheOptions` (`:201-217`).
  - `cmd/operator/main.go:189`: the uncached reader passed to the reconciler.
  - `charts/assayd/templates/rbac.yaml:72-89`: the gateway-labels Role.
  - `charts/assayd/files/operator-rules.yaml:43-53`: `list` on `agentgatewaypolicies`, cluster-wide.
  - `test/envtest/authabove_test.go`: `gatewayUnreadable` and `policiesUnlistable` (`:477-503`).
- **Method**: every claim A10 makes about the code, the chart or the existing test doubles was traced to its source. Nothing was run or mutated, because nothing in the slice is implemented.

**Verdict: PASS — 0 BLOCKER, 0 MAJOR, 6 MINOR.**

A10 closes M1 with the seventh critique's first fix. The fifth clause calls `gatewayAuthPolicies(host).stands()` and `foreignTrafficPolicies`. On every `ProbingAfter` pass, whatever the deadline, those are the reads that decide whether `runLock` may credit. So the clause holds the eval when design 03 holds the `Lock`, before the deadline and after it. It adds no wedge of its own. m2 to m7 are closed.

The six MINORs are text or row-construction fixes, and none changes a rule:
- a window the external read leaves open;
- a contradiction about which controller evaluates the clause, and at what cost;
- a NotFound overstatement, which also leaves a row that can be built to fail a correct implementation;
- a stated reason that covers more than the rule it justifies;
- readability;
- two new rows whose construction is underspecified.

PASS means the text survived an adversarial read, not that it is approved. Approval is the human's, on Q1 to Q13, and it must amend ADR-0024.

## Closure of the seventh critique

| Finding | A10's answer | Closed? |
|---|---|---|
| M1: the fifth clause failed open at the deadline | The clause reads the hold itself (`:101`), says why it is not keyed on a reason (`:102`), and states its cost (`:103-108`). Rows `:469` and `:470` run past a shortened deadline | **Closed.** The reads match `runLock`'s. Details below the table. Caveats: m1, m2, m3 |
| m2: how the wedge's detection is read | The gate branch computes it from its own route read, which may be one pass stale; any-ref reading; R's name trimmed from the `backendRef`; a fallback (`:121-125`) | **Closed.** It matches `cardAttributes` (`authtxn.go:1958-1969`: any ref, any rule, `WorkloadName(agent, rev) == ref.Name`) and `WorkloadName` (`agent + "-" + rev`). The fallback's reachability is m6 |
| m3: GC in the apart state | Stated at `:129-135` and in Q13 (`:414`, `:418`) | **Closed.** The count of three is verified below the table |
| m4: Q13's premise | Restated (`:413`); option (c) added (`:417`) | **Closed.** `pruneCards` keeps every retained revision's entry (`card.go:404-417`). So an R card that a re-read records would persist while R is retained, and (c) needs no change outside design 02 |
| m5: `Interrupted` while a transaction is recorded | It waits on the no-transaction predicate (`:144`); row `:471` | **Closed as a rule.** The row's construction is m6 |
| m6: two rows | Row `:486` holds the `Lock` short of `Served`; row `:467` states D and R's card | **Closed.** Details below the table |
| m7: stale sentences | `:269`, `:292`, `:343`, `:658` updated; the chart cited at `:126`; the foreign policy added at `:127` | **Closed.** `:644`'s "A8 changes nothing in design 03" is A8's own log entry, and correct as history |

**M1.**
- **The same reads.** `runLock` sets `held = above.stands()` from a fresh read on every `ProbingAfter` pass (`authtxn.go:1725-1728`). It refuses any probe answer while `out.foreign` is non-empty (`:1732-1742`), and `out.foreign` comes from `gatewayStep`'s own fresh `foreignTrafficPolicies` call (`:611-615`).
- **The carried state decides nothing.** `storedHold`, `seedStoredAbove` and `carriedReason` (`:292-302`, `:393-420`) only carry the condition for a pass that returns early. None of them decides whether a `401` is credited. So the `Lock`'s hold uses no state the predicate cannot see.
- **The three cases.** `stands()` is true in exactly the three cases `:101` lists (`authabove.go:102`). `allowedListeners` enters through `admitsListenerSets` (`:219`).
- **The reader.** In production the uncached reader is set: `main.go:189` passes `mgr.GetAPIReader()`.
- **The rights.** The operator already holds both: `get` on the named Gateway (`rbac.yaml:85-89`), and `list` on `agentgatewaypolicies`, cluster-wide (`operator-rules.yaml:43-53`).
- **The rows.** A clause keyed on the reason, as A9's was, is killed by row `:469`'s interval after the deadline.

**m3.**
- `collectGarbage` protects only the active and candidate revisions, and keeps the two newest of the rest (`agent_controller.go:1556-1590`).
- With C active and D the candidate, R is kept. E makes the unprotected set {D, R}, and both are kept. F makes it {E, D, R}, and R is deleted.
- R's Service goes with it (`service.go:200-214`). `routePublished` checks only that some rule has `backendRefs` (`httproute.go:334-341`), so nothing notices.
- On an ungated Agent, three promotions (C, D, E) do the same.

**m6.** Row `:486`'s oracle withholds the `401` until `activeRevision == C` while the `Lock` is recorded.
- The mutation "hold promotion while a `Lock` is recorded" then deadlocks, so the row fails as it should.
- The mutation "skip the re-assertion" leaves the `backendRef` on R after `Served`, which the last assertion catches.

## What was verified

- **The clause cannot make the eval wait while design 03's `Lock` is free.** Each input the clause reads holds design 03 too:
  - an unreadable Gateway, or a failed Gateway-namespace list, holds `runLock`;
  - a failed foreign list makes `gatewayStep` return before `runLock` (`authtxn.go:611-614`);
  - a counting policy, or a Gateway that admits ListenerSets, holds at `ProbingAfter`;
  - a foreign policy refuses the probe (`:1732`).
- **Once the hold clears, the `Lock` either credits or is exempt.** Crediting clears the transaction. A `Lock` that still cannot attribute is in the exempt state. So the hold delays the eval, as `:101` says, and never wedges it.
- **The one asymmetry is timing.** `runLock` reads the Gateway a second time after a creditable `401` (`:1763`), and the predicate reads it once. That changes nothing durable.
- **Fail-closed on every error path but NotFound, which design 03 shares (m3).**
  - `gatewayAuthPolicies` never returns an error. A failed Gateway `get`, or a failed list, sets `unreadable`, and `unreadable` makes `stands()` true.
  - `foreignTrafficPolicies` returns an error for three failures: an `AuthPolicyName` failure, a list error other than NotFound, and a route `get` error other than NotFound. The predicate reads each as a hold.
- **The eval controller is woken when a hold clears.**
  - Before the deadline, `PolicyApplyIncomplete=GatewayAuthPolicy` is withdrawn.
  - After the deadline, `AuthLockUnverified`'s message loses the hold. `unmet` appends the hold only while `held` (`:1787-1789`), and carries the foreign list (`:1735`).
  - Either change is a status change, so the Agent event reaches the eval controller's `For(&Agent{})`.
- **"Why not a condition reason" (`:102`) is exact.** The deadline branch comes before the hold branch (`:1833-1848`).
- **The cost claims that `:103-108` states are right.**
  - The page size is 250 (`authforeign.go:72`).
  - The Gateway `get` and both lists go through `reader()` (`authabove.go:204`, `authforeign.go:90`).
  - The namespaces are the Gateway's and the run namespace.
  - The route read uses the cached client (`authforeign.go:110`), and the manager's cache holds HTTPRoutes because the Agent controller watches them (`agent_controller.go:1872`).
  - What it leaves out is m2.
- **The owed move is feasible.**
  - The two methods depend only on `reader()`, `r.Gateway`, `listPolicies` and `servingRouteLabels` (which calls `r.Get`). Everything else they call is a free function.
  - A small struct holding the client, the uncached reader and the `GatewayConfig` would carry them. Embedded in `AgentReconciler`, it would leave every existing call site unchanged.
  - Handed to the eval controller, the same struct gives it the same injected reader in envtest.
  - "Unchanged" is true of the function bodies, not of the receiver (m2).
- **The contention count (`:112`) still holds.** The clause writes nothing.
- **Row `:470` can add the foreign policy in any order.** `ProbingBefore` checks the foreign list only for a policy at the `Lock`'s own name (`authtxn.go:1674`). So a foreign policy on `<agent>-serving` stops the J2 `Lock` only at `ProbingAfter`.
- **Row `:472`'s second half can be built.** `ensureService` runs only for the desired revision (`agent_controller.go:575`). Once C is desired, a Service of R that the test deletes is not re-created.

## New findings

### m1 — MINOR: the fifth clause governs new claims only, so "the candidate holds until the hold clears" is too strong

`16-evalsuite.md:95`, `:101`, `:144`; Q12 at `:411`.

- **The first four clauses are covered by the claim's lock.** They are read from Agent status. The claim is a merge patch under the optimistic lock, at the live read's resourceVersion (step 10). So a transaction entered after that read makes the claim fail with `Conflict`.
- **The fifth clause is not.** It reads the Gateway and two policy lists, outside that lock. The eval controller re-reads them before every claim, so no claim is made on a stale read.
- **A hold that appears mid-case does not stop the case.** If it is the last case, its record carries `Passed` (step 7).
- **Nor does it stop the promotion.** The promoting pass checks only the four promotion conditions (`:199-202`), and none of them reads the hold. So a hold that appears during the last case, or between the `Passed` record and the promoting pass, does not stop the promotion.
- **What the text says instead.**
  - `:144` states this exception for a transaction.
  - `:269` accepts a promotion during a `Lock` when the verdict was already `Passed`.
  - `:95` and `:101` state the fifth clause with no exception.
  - `:95`'s "until the hold clears" is also true only on Q12 (a). On Q12 (b), a transaction past its deadline releases the eval whether or not a hold stands.
- **Where the fork's pass was wrong.** It bounded the window by the `Lock`'s next status write, 5 s or 15 s later. That window does not exist, because each claim re-reads. The exposure is the case in flight and the promoting pass.
- **Fix.**
  - At `:101` or `:144`, add one sentence: like the transaction rule, the fifth clause stops new claims. It stops neither the case in flight nor a promotion whose `Passed` verdict is already written.
  - Qualify `:95` to "for new claims, on Q12 (a)".

### m2 — MINOR: the gate branch must also evaluate the fifth clause, and the cost text counts only the eval controller

`:103-108`, against `:110`, `:150` and `:245`.

- **The text makes both controllers evaluate the clause.**
  - `:150`: both controllers decide "with the same predicates, implemented once and called by both".
  - `:245`: the gate branch sets `EvalWaitingForAuth` except for "the exempt `Lock`".
  - `:110`: the Agent reads `EvalRunning` while all five clauses hold.
- **Nothing reserves this clause for the eval controller.** A6 made the generation clause the eval controller's alone (`:150`), and no `GatesPassed` row depends on that clause. A `GatesPassed` row does depend on the fifth clause, and no carve-out exists for it.
- **So `:103`'s "only on an eval pass" is false.** On every pass in the exempt state, the Agent reconciler's gate branch makes the Gateway `get` and both lists.
  - That pass recurs every `AuthProbeInterval` (5 s) before the deadline, and every `CardRetryInterval` (15 s) after it (`authtxn.go:1790-1793`).
  - The same pass already makes these reads: `runLock` calls `gatewayAuthPolicies` (`:1726`), and `gatewayStep` calls `foreignTrafficPolicies` (`:611`). The gate branch doubles them.
- **The cheaper build breaks rule 8.** A gate branch that evaluates only the first four clauses would set `EvalRunning` while nothing is claimed. Rows `:469` and `:470` assert only the stub's hits, so that mutation survives.
- **Precision.**
  - `:106`'s route read happens only when the run-namespace list is non-empty (`authforeign.go:47-50`).
  - `:108`'s "unchanged" is true of the bodies, not of the receiver.
- **Fix.** Choose one, and state it:
  - (a) the eval controller writes the hold into `status.eval.blocked`, and the gate branch reads it there, as N3 already does for `EvalAwaitingCard`, with no second read;
  - (b) the gate branch evaluates the full predicate, and `:103` counts that cost.

  Either way, add to rows `:469` and `:470` an assertion of `GatesPassed=False/EvalWaitingForAuth` across the held interval. Its mutation is a gate branch that evaluates only four clauses.

### m3 — MINOR: a NotFound list reads as no foreign policies, not as a hold, and row `:470`'s variant can be built to fail a correct implementation

`:101`, `:470`.

- **`:101` overstates the foreign half.** It says `foreignTrafficPolicies` "returns an error on a list it cannot make, and the predicate reads that error as a hold".
- **What the code does with NotFound.** `foreignTrafficPolicies` lists with `notServedIsEmpty: true` (`authforeign.go:43`), so a NotFound returns no policies and no error (`:92-94`). `gatewayAuthPolicies` lists with `false` (`authabove.go:221`), so the same NotFound makes the Gateway half `unreadable`, and `stands()` true.
- **A real cluster still fails closed.** A kind that is not served returns NotFound in both namespaces, so the Gateway half holds. Only the sentence is wrong, and design 03 reads the foreign half the same way.
- **Row `:470`'s variant is underspecified.** It says only "a run-namespace policy list fails, through an injected reader". The suite's one failing-list double, `policiesUnlistable` (`test/envtest/authabove_test.go:491-503`), returns NotFound and is scoped to the Gateway's namespace.
  - Re-pointed at the run namespace, it makes a correct implementation read no foreign policy. The Gateway half is then clean and cases are claimed, so the row's zero-hit assertion fails a correct implementation.
  - Built with any error but applied to both namespaces, it makes the Gateway half hold instead. The mutation "read a failed foreign list as no hold" then survives.
- **Fix.**
  - At `:101`: an error other than NotFound holds. A NotFound reads as no policies, as design 03 reads it, and the Gateway half, whose list reads the same NotFound as unreadable, then holds.
  - For row `:470`: the injected error is not NotFound (Forbidden, or a 5xx). It applies to the run namespace only, and the Gateway namespace's list succeeds.

### m4 — MINOR: the fifth clause's reason covers served-Agent incidents the slice promotes beside

`:101` gives the reason: "A `Lock` held this way is an open incident. Promoting a new revision beside it changes what the gateway serves while the incident is open". Q12 at `:411` reasons the same way.

- **Two served-Agent incidents record no transaction.**
  - A served API-key Agent beside a widening Gateway-level policy (W1, `reportAboveServed`, `authtxn.go:354-377`) carries `PolicyApplyIncomplete=True/GatewayAuthPolicy`, pages, and is `Degraded`.
  - A served Agent beside a foreign traffic policy carries `ForeignTrafficPolicy`.
- **The slice evaluates and promotes beside both.** Design 16 never mentions W1.
- **The rule is sound.** It keys on a recorded transaction, and says so.
- **The reason is the problem.** It does not separate the case it holds from the cases it releases. So it will later be cited both to extend the hold and to delete it. write-spec's rule is to state the rule, then the reason.
- **Fix.**
  - State that the slice holds the eval for a recorded transaction, not for an open incident.
  - State that a served Agent under W1, or beside a foreign traffic policy, is evaluated and promoted, as an ungated one is.
  - Name that among Q12's costs.

### m5 — MINOR: "Who runs the eval" is too dense for a newcomer to follow

`:90-144`, above all `:96-143`.

- **The nesting is too deep.** The exemption is a five-clause predicate written in prose, nested four levels deep (`:96`, `:101`, `:103`, `:104`).
- **Several sub-bullets are history, not rules:**
  - `:102`, "A9 keyed this clause on…";
  - `:114`, "A6 said no transaction waits on a promotion";
  - `:115`, "A7's exemption covered this `Lock`, and it did not work";
  - `:117`, "A8 said … That was false".
- **write-spec asks for the opposite.** It wants self-contained text, one fact per sentence, and a table for more than three parallel cases. The log in §14 already keeps the amendment history.
- **Why it matters now.** The human approves the slice by answering 13 questions against this text.
- **Fix.** A consolidation pass that changes no rule:
  - put the five clauses in a table: the clause, what it reads, what it fails closed on, and the row that pins it;
  - move every "X said Y" sentence to its log entry, leaving only the tag;
  - put the wedge's harm, exits and non-exits in a table.

### m6 — MINOR: two new rows cannot see what they assert, as written

`:471`, `:472`, and the fallback text at `:125`.

- **Row `:471`: the orphaned last claim while a `Lock` is recorded.**
  - **The problem.** The row names neither the `Lock`'s kind nor how it is kept recorded.
    - A J2 or K2 `Lock` in the exempt state is treated by the predicate as no transaction. Recording `Interrupted` at once is then correct.
    - A missing-policy `Lock` whose route's revision is carded credits within a few 5 s passes. Whether the mutation "record `Interrupted` without the predicate" is seen then depends on whether the eval controller's first pass comes before the `Lock` reaches `Served`.
  - **Fix.** Build it the way row `:460` builds its handover: the test writes the orphaned claim itself. Use a non-exempt `Lock`, held short of `Served` by the probe oracle withholding the `401`, as row `:486` does. Release the oracle only after an observation interval.
- **Row `:472`'s first half, and the fallback at `:125`.**
  - **The problem.** A missing-policy `Lock` whose route is absent, or has no `backendRef`, does not stay a `Lock`: `runLock`'s re-creation branch replaces it with `recreateRoute` in the same pass (`authtxn.go:1607-1608`).
    - So the gate branch sees "a missing-policy `Lock` at `ProbingAfter` whose route has no R" only in the one-pass-stale window that `:122` describes.
    - On every later pass the transaction is a `Create`, and its message is generic under any implementation.
    - The mutation "name exits with no R" therefore shows only in the one status write of that pass. A test that reads the Agent after that pass will pass the mutant.
  - **Fix.**
    - At `:125`, state that the fallback is reachable only in that stale pass.
    - Build row `:472`'s first half on a watch that asserts on every status write whose `EvalWaitingForAuth` concerns the `Lock`. Or drop that half, and keep the deleted-Service half. That half can be built in either wedge state once C is desired (`agent_controller.go:575`).

## Noted, not counted

- **A gateway disabled while a transaction is recorded.**
  - When `gateway.enabled` goes from true to false, a recorded J2 or K2 `Lock` stays in `status.auth`, because design 03's teardown is not implemented (`authtxn.go:590-594`).
  - Under A7 to A9, the clauses that read only status exempted it. Under A10, the fifth clause's Gateway read decides, and with the gateway-labels Role not rendered, a Forbidden read holds the eval.
  - That is fail-closed. It is what A6's rule already does to every other transaction recorded in that state.
- **No requeue is stated while the eval controller waits on a transaction or a hold.** The Agent events above wake it, which is enough, but one sentence should say so.
- **Design 03's own messages in the wedge.** Its early-return `AuthPolicyMissing` text and `runLock`'s "so its route served with no key" (`authtxn.go:1816`) are design 03's code, outside this slice, as the seventh critique noted.
- **The choice to leave designs 02 and 03 unchanged** is Q13's to settle, and is not counted.

## The smallest set of changes before the human reads it

1. **m1**: add one sentence scoping the fifth clause to new claims, and qualify `:95` to "for new claims, on Q12 (a)".
2. **m2**: choose `blocked` or the full predicate in the gate branch, state its cost, and add a `GatesPassed` assertion to rows `:469` and `:470`.
3. **m3**: correct `:101` on NotFound, and state row `:470`'s error and its scope.
4. **m4**: state that the hold is for transactions, not incidents, and name W1 and foreign policies among Q12's costs.
5. **m5**: make the consolidation pass.
6. **m6**: rebuild row `:471` on a held, non-exempt `Lock`; scope `:125`'s fallback to the stale pass; and build or drop row `:472`'s first half.

**design 16 §1.1 (A10): PASS — 6 MINOR outstanding**
