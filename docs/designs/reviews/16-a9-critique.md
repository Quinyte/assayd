# Critique: design 16 A9 — the first slice (§1.1), revised a sixth time

- **Date**: 2026-09-14
- **Target**: `docs/designs/16-evalsuite.md` §1.1 and amendment A9, at `dfe9509` (branch `design-16-first-slice`), read as `git diff 42ee095 dfe9509`. A9 answers `reviews/16-a8-critique.md` (REVISE: 0 BLOCKER, 1 MAJOR, 5 MINOR). The earlier records are `reviews/16-a3-critique.md` to `reviews/16-a7-critique.md`.
- **Independence**: an independent session. It wrote none of A4 to A9 and none of the six earlier critiques. A forked first pass ran with the `critique-design` skill. Every claim it made was traced again by hand before it was counted. Where the two passes disagree, this record says so and gives the reason.
- **Read against**:
  - design 03: the A75 condition rules (`03-policy-compiler.md:341`), the deadline table (`:787`), and the NACK rule in A72 (`:1494`);
  - design 02 §3.2, the read-access row (`02-agent-crd-operator.md:144`);
  - the code:
    - `internal/controller/authtxn.go`: `reconcileGateway`'s -auth step (`:280-340`), `raiseIncomplete` and `incompleteOrder` (`:483-512`), `reportForeign` (`:529-563`), `isReCreation` (`:791`), `reconcileServed` (`:1498`), `recreateRoute`, `lockMissingPolicy` (`:1564`), `runLock` (`:1589-1849`), and `cardAttributes` (`:1956`);
    - `internal/controller/agent_controller.go`: the rollout switch and the promotion (`:760-800`), the lost-race branch (`:840-880`), `withholdReady` (`:932`), `collectGarbage` (`:1552`), `WorkloadName` (`:1795`), and the watches (`:1825-1874`);
    - `internal/controller/httproute.go`: `routePublished` (`:334`) and `assessGovernance`'s `Lock` branches (`:700-720`);
    - `internal/controller/card.go`: `pruneCards` (`:404`); `internal/controller/service.go`: `collectRevisionServices` (`:200`);
    - `charts/assayd/templates/rbac.yaml`, `namespace.yaml` and `admission.yaml`.
- **Controller-runtime v0.24.1, read from the module cache**: `pkg/internal/controller/controller.go`, the goroutine that calls `Queue.ShutDown()` on the cancel (`:343-346`) and `processNextWorkItem` (`:419-424`).
- **Method**: every claim A9 makes about the code, the chart or controller-runtime was traced to its source. Nothing was run or mutated, because nothing in the slice is implemented.

**Verdict: FAIL (REVISE) — 0 BLOCKER, 1 MAJOR, 6 MINOR.**

A9 closes M1 as the sixth critique asked. The wedge's harm now matches the code, its exit is the administrator's, and the text keys it on the revision the route names. m2, m4 and m6 are closed, and so are m3 and m5 in their main points. Q13 is well formed. The MAJOR is A9's own fix for m5. The fifth clause is keyed on a condition reason that the code, and design 03's text, replace at the transaction's deadline. So the property A9 states, "the candidate holds until the hold clears", holds for four minutes and then quietly fails open, and the test row it adds cannot see this. The MINORs cover four things: how the wedge's revision is derived, one harm the text leaves out, one premise of Q13, and a few row constructions and stale sentences.

## Closure of the sixth critique

| Finding | A9's answer | Closed? |
|---|---|---|
| M1(a): the harm | The wedge is restated from the code, with the conditions the Agent carries (`16-evalsuite.md:110-114`) | **Closed.** `lockMissingPolicy` enters at `ApplyingPolicies` with `Written: true` (`authtxn.go:1566-1569`). `runLock` reaches `ProbingAfter` only after `routeConverged` and `policyConverged` (`:1706-1718`). Its re-creation tail sets `GovernanceSkipped=True` and `PolicyApplyIncomplete=True`, both `AuthPolicyMissing`, with the quoted message (`:1813-1829`). It sets `out.withhold` to `AuthPolicyMissing`, and `out.served = true` (`:1808`). So `withholdReady` sets `Ready=False` and `Degraded=True` with the same reason, and phase `Degraded` (`agent_controller.go:932-948`). On a gated Agent with a held candidate, the Held branch first sets `Ready=True/Available` (`:766-768`), so `withholdReady` does act. Past the deadline, only `deadlineNote` is added (`authtxn.go:1810-1813`). The composition bullet (`:252`), Q12 (`:394`) and the table row (`:228`) agree, and no sentence still calls the route open. One caveat is noted below: on a pass that returns early, `assessGovernance` writes a different message |
| M1(b): the exit | An administrator of the run namespace; the message addresses that administrator; the outage is unbounded under a Gateway-level hold (`:115-118`, `:228`) | **Closed.** `rbac.yaml` binds only the operator's ServiceAccount, in a ClusterRoleBinding and two Roles in the Gateway's namespace. `namespace.yaml` creates no binding, and no Go source under `internal/` or `cmd/` creates a RoleBinding. Admission reserves only `CREATE` and `UPDATE` on `httproutes` (`admission.yaml:95-96`, `:135`). So a delete is RBAC's alone, and by default only a cluster administrator holds that right. The Gateway-level caveat is stated (`:116`). A smaller omission, a foreign traffic policy, is in m7 |
| M1(c): the revision | Detection, message and revert are keyed on the revision the route's `backendRef` names (`:109`, `:114`, `:122`, `:228`, `:252`) | **Closed in substance.** That is the revision `cardAttributes` reads (`authtxn.go:1956-1974`). The promoting-pass case is real: `reconcileServed` checks the policy (`:1510-1516`) before `ensureServingRoute` (`:1525`), after the switch has set `activeRevision` (`agent_controller.go:786`). The text does not say how the revision is derived, or what happens when the route has no `backendRef` or several (m2) |
| m2: row ordering | Both rows state their order (`:445-446`) | **Closed.** Clear R's card while R is desired, delete the policy, reach `ProbingAfter`, then mint C. Both mutations of the wedge row compile and are killed |
| m3: the verdict with a transaction recorded | The verdict is written in the same patch as the last case's record (step 7, `:156`). The in-flight bullet says no other verdict is written while a transaction is recorded (`:127`). The row is rebuilt (`:463`), and an "apart" row is added (`:447`) | **Closed as a rule.** One exception, `Interrupted`, is not covered (m5). The two rows need one more constraint each (m6) |
| m4: the read-back row | An Agent CRD without the fields is installed (`:453`) | **Closed.** Pruning stores nothing, so the read-back fails as a stale install would. `at` is an old field, so the "write `at` at run start" mutation still moves the resourceVersion and is killed |
| m5: the superset | Abandonment's reason is corrected (`:104`). The verdict adds no patch (`:105`). A fifth clause excludes `GatewayAuthPolicy` and `ForeignTrafficPolicy` (`:101`) | **Abandonment and the count are closed. The fifth clause is not** (M1 below) |
| m6: the queue window and the hook | Widened (`:162`); the hook sits before the `ctx.Err()` check (`:436`) | **Closed.** The cancel wakes a goroutine that calls `Queue.ShutDown()` (`controller.go:343-346`). Until then, `GetWithPriority` can still hand out an item with `shutdown` false (`:420`). The check covers both kinds of pass |
| Noted: Q13 | Q13 added, recommended (a) (`:395-398`) | **Added.** Its premises are checked under m4 |

## What was verified

- **The M1(a) condition list is exactly what the code sets in the wedge**, on a full pass. The trace is in the table above.
- **Nothing moves the missing-policy `Lock`'s route, and the apart state is stable.**
  - `runLock`'s re-creation branch takes the route as found while it is published (`authtxn.go:1602-1614`).
  - At `ProbingAfter`, `cardAttributes(rt)` looks for R's card and finds none, so the stage never credits.
  - Deleting the route displaces the `Lock` through `recreateRoute` (`:1608`), whose `Create` publishes on `status.activeRevision`, which is C in the apart case.
- **Deleting `<agent>-auth` enters the `Lock` promptly.** Policies are watched through `gatewaySources`, mapped by the Agent's labels (`agent_controller.go:1864-1874`). So rows :446, :447 and :463 can record the `Lock` well within `caseTimeoutSeconds`.
- **The revert exit works as stated.** Once R is desired again, its card is fetched. A recorded card lets the `Lock` credit on R (`cardAttributes`). In the apart case R becomes a candidate, and the `Lock` then finishes without any promotion.
- **Row :447's mutations are real.** Keying detection on `status.activeRevision` finds C's card, so the message names no exit. Naming `status.activeRevision` in the revert names C. The row's assertions catch both, once the next candidate is built as m6 says.
- **The verdict count (`:105`) holds** for sends. A run costs one run-start patch, and each case costs a claim and a record. The verdict and a `failed[]` append ride in the last record. The one exception is m5.
- **Q13's premises.** "It leaves the route enforcing" is true: the policy is converged and accepted. "It pages" is true: `PolicyApplyIncomplete` is raised at entry (`authtxn.go:1825`). "It exists without a gate too" is true: an ungated Agent wedges the same way after any promotion, and with no promotion it wedges whenever R's card failure lasts. The first premise understates the trigger (m4).
- **The fifth clause cannot wedge the eval.** The -auth step re-derives `GatewayAuthPolicy` and `ForeignTrafficPolicy` on every full pass (`authtxn.go:297-302`). Carrying them on early-return passes (`seedStoredAbove`) lasts only until the next full pass. W1 (`reportAboveServed`) raises `GatewayAuthPolicy` only when no transaction is recorded (`:357`), so it never meets a `Lock`. The clause fails in the other direction instead (M1).

## New findings

### M1 — MAJOR: the fifth clause fails open at the `Lock`'s deadline, so the property A9 states for it is false

`16-evalsuite.md:95`, `:101`, `:449`, and the log at `:637`.

**What A9 says.**
- `:95`: a J2 or K2 `Lock` held by a Gateway-level policy "is not exempted…: the hold's incident outranks the exemption, so the candidate holds until the hold clears".
- `:101`: the clause reads "the Agent's `PolicyApplyIncomplete` is not `True` with reason `GatewayAuthPolicy` or `ForeignTrafficPolicy`", and "the hold delays the eval and never wedges it".

**What the code does with that reason.**
- **A Gateway-level hold loses its reason at the deadline.** In `runLock`'s J2/K2 tail, the deadline branch comes before the hold branch (`authtxn.go:1834-1848`):
  ```go
  if deadlinePassed || out.nack != "" {
      conds.set(CondPolicyApplyIncomplete, True, ReasonAuthLockUnverified, msg)
      ...
  } else if held {
      conds.set(CondPolicyApplyIncomplete, True, ReasonGatewayAuthPolicy, msg)
  ```
  `AuthTransactionDeadline` is 4 minutes (`:46`). Design 03 specifies exactly this: "At the deadline … `AuthLockUnverified` (J2, K2) replaces `GatewayAuthPolicy`, and the message still names the cause" (`03-policy-compiler.md:341`). After the deadline, the hold appears only in the message.
- **A foreign policy is masked the same way.** `reportForeign` raises its reason through `raiseIncomplete` (`authtxn.go:553`). `AuthLockUnverified` is not in `incompleteOrder` (`:487`), so `raiseIncomplete` keeps it as the reason and appends `ForeignTrafficPolicy` to the message (`:508-510`). The lost-race path behaves the same (`:316`, `:321`).
- **Whatever the clause can read, the outcome is the same.** No field of `status.auth.transaction` records the hold (`storedHold`, `:393`, reads only the condition's reason). So a predicate "read from the Agent's status alone" cannot tell a J2 `Lock` that is held past its deadline from one that is merely unattributed past it. Row :444 of the design says the latter must be exempt.

**What breaks.** An A75 hold, or a foreign traffic policy, ends only when someone removes or rescopes the policy. Any such hold on a J2 or K2 `Lock` in the exempt state that lasts beyond 4 minutes, which is nearly every one, then satisfies all five clauses. Cases are claimed, and a passed candidate is promoted while the incident is open. That is the outcome `:95`, `:101` and Q12's reasoning (`:394`) each say cannot happen. It is rule 7's case: a stated bound that the specified predicate does not enforce.

**The owed test cannot catch it.** Row :449 observes zero hits only while `PolicyApplyIncomplete=GatewayAuthPolicy` stands, and never runs past a shortened deadline. No row covers the `ForeignTrafficPolicy` half, so a mutation that drops it survives.

**Where the fork's pass is wrong.** It also said "a NACK does it at once". A NACK moves the `Lock` back to `Converging` (`authtxn.go:1697-1706`), which the second clause already excludes. That part is not counted.

**Fix, one of:**
1. Key the clause on the hold itself, not on a reason. The shared predicate would run the same reads the -auth step runs: `gatewayAuthPolicies(host).stands()`, and the foreign-policy list. State the cost, one Gateway `get` and one policy list per eval pass. The operator already holds both rights (`rbac.yaml`, the `-gateway-labels` Role). The predicate is then no longer "read from status alone", so say that too.
2. Or state honestly that the clause holds only until the transaction's deadline. After it, the reason is `AuthLockUnverified`, the exemption applies, and a candidate can be promoted beside the hold, as on an ungated Agent. Correct `:95`, `:101`, the log at `:637`, and Q12's cost sentence to match.
3. Asking design 03 to record the hold in `status.auth.transaction` would also work, but it is a design 03 change.

Whichever is chosen, extend row :449 past a shortened deadline, asserting the chosen behaviour, and add a `ForeignTrafficPolicy` row.

### m2 — MINOR: the wedge's detection names its input but not how to read it (`:114`, `:228`)

"The Agent reconciler computes that message from the route it reads", but four details are missing.
- **Where, and against what.** The message is set in the gate branch, inside the rollout switch. That branch runs before `reconcileGateway` in the same pass (`agent_controller.go:786`, then `:804`). So it needs its own read of `<agent>-serving`, and it sees the transaction as recorded at the start of the pass. If the route was stripped or deleted since, the same pass later replaces the `Lock` with a route re-create (`authtxn.go:1605-1608`). The written status then pairs a `Create` in `status.auth` with an `EvalWaitingForAuth` message about a `Lock` for one pass. The text should accept that, or order the message after the -auth step.
- **Zero `backendRef`s, or several.** A route with none, or one that cannot be read, has no R. Say that the message falls back to the generic text then. `cardAttributes` credits if any `backendRef` in any rule names a carded revision. So the condition should read "no `backendRef` names a revision whose card is recorded", not "the one `backendRef`".
- **How R's name is obtained.** R has no `status.cards[]` entry. In the apart case it also appears nowhere in status: it is no longer active, candidate or superseded. So the name must come from the `backendRef` itself: the Service's `LabelRevision`, or its name with the `WorkloadName` prefix `<agent>-` trimmed (`agent_controller.go:1795`). Say which. The Service may already be gone (m3), so name-trimming is the robust choice.
- **Fix.** Add one sentence covering all four: when and where it is computed, the any-ref reading, the fallback, and the source of R's name.

### m3 — MINOR: in the apart state, garbage collection deletes the revision the route still names (`:109`, `:118`, `:398`)

- `collectGarbage` protects only `status.activeRevision` and `status.candidateRevision`, and keeps the `DefaultRevisionHistoryLimit` (2) newest of the rest (`agent_controller.go:1558-1590`, `:65`). The revision's Service goes with it (`service.go:200-214`).
- In the apart state, R is neither active nor candidate. On the gated Agent, the owner's edits keep minting held candidates. Once a third revision after C is minted, R is the third-newest unprotected revision and is deleted, with its Service. This takes three edits: D, then E superseding D, then F superseding E.
  - The fork's pass counted two edits; three is right.
- The route still names R's Service. The `Lock`'s re-creation branch checks only `routePublished` (`authtxn.go:1605`), and `ProbingAfter` does not re-check convergence. So nothing notices.
- Authenticated callers then fail on a missing backend, while the Agent reads `AuthPolicyMissing`. Anonymous callers still get `401`, so "enforcing" stays true.
- `:118` offers "leave the wedge standing" as an option, and Q13 rests on "it leaves the route enforcing". Neither says that, in the apart case, standing costs an outage after three edits.
- The same happens on an ungated Agent after three promotions. That half is design 03's, and is noted below.
- **Fix.** State this cost in the apart bullet, at `:118`, and in Q13. Or record, as owed to design 02 or 03, that GC protects the revision the serving route names.

### m4 — MINOR: Q13's first premise understates the trigger (`:398`)

- "The wedge needs a card failure and a deleted policy at once" reads as a coincidence of two events. It is not one for an Agent whose container never serves a card that validates.
- Today a card failure never withholds traffic (`:189`, design 02 §3.4). So an ungated revision, or one served before the gate was added, or one promoted by a pin or by removing the gate (Q9), can serve with no card ever recorded.
- On such an Agent, the deleted policy alone produces the wedge, and no revert can end it.
- **Fix.** Say so in Q13. Also consider listing a third option: re-read the card of the revision the serving route names while a `Lock` waits on it. That would be a change to card handling (design 02 §3.4), not to design 03, and it would end the transient case on gated Agents. It does not help a card that never validates.

### m5 — MINOR: an `Interrupted` record can carry a verdict while a transaction is recorded (`:127`, against `:133` and `:165`)

- `:127` says "No other verdict is written while a `Create` or `Lock` is recorded, because no case is claimed."
- Recording `Interrupted` for a claim found with no outcome (step 10, `:165`) is not a claim. The shared predicates decide only "whether a case may be sent" (`:133`).
- If the orphaned claim belongs to the last case, the new leader's record of it carries the verdict, whether or not a `Lock` is recorded. It is also an eval patch outside the in-flight bullet's "at most one outcome".
- **Fix.** State that an `Interrupted` record also waits on the no-transaction predicate. Or count it in `:127` and in the contention argument.

### m6 — MINOR: two rebuilt rows can fail a correct implementation

- **Row :463 (a `Lock` recorded at the promotion).** R's card is recorded, so the missing-policy `Lock` credits as soon as it converges and the probe gets `401`. That can happen before the stub releases C's last case. If it does, C is promoted with no `Lock` recorded. The "hold promotion while a `Lock` is recorded" mutation is then not exercised, and the `backendRef`-names-R assertion during the `Lock` has nothing to observe.
  - **Fix.** The test holds the `Lock` short of `Served`, by withholding the policy's accepted status or the probe oracle's `401`, until it has seen `activeRevision == C` with the `Lock` still recorded. The envtests already write gateway statuses by hand.
- **Row :447 (the apart state).** "`EvalWaitingForAuth` on the next candidate" needs three things the row does not state.
  - The next candidate D must be minted and available: the not-ready branches come before the gate (step 8), so otherwise D reads `EvalPaused` or a not-ready reason.
  - D must be carded, because the table states no row precedence and has an `EvalAwaitingCard` row.
  - The card stub must fail for R only.
  - **Fix.** State all three.

### m7 — MINOR: stale or incomplete sentences

- `:252`, the composition bullet, and `:275`, the in-slice row, describe the J2/K2 exemption without the fifth clause.
- `:326` still reads "A8 changes nothing in design 03".
- The log at `:635` cites "Row :448", which is now `:463`.
- `:115` says design 02 records "run-namespace objects as reachable only by a cluster administrator". The read-access row (`02-agent-crd-operator.md:144`) says that of run-namespace logs. The conclusion still holds by the chart's RBAC, so cite that instead.
- `:116` bounds the outage only for a Gateway-level hold. A foreign traffic policy on `<agent>-serving` also keeps the re-created route's `Create` from publishing (design 03 A72, "`Publishing` now also waits while a foreign policy stands"), and the re-created route keeps the name it targets. So the outage is unbounded then too. Add it.

## Noted, not counted

- **Design 03, not this slice.** On an ungated Agent, a wedged missing-policy `Lock` survives any number of promotions. After three of them, GC deletes the revision its route names, and the Agent reports `AuthPolicyMissing` while the route has no backend (m3). The human may want this in front of them when answering Q13.
- **Design 03's early-return message.** On a pass that returns before the -auth step, `assessGovernance` writes `AuthPolicyMissing` with the message "the route serves with no key until an anonymous request through it gets an attributed 401" (`httproute.go:711-717`). In the wedge, A9 has now shown that this is false. It is design 03's code and outside this slice. A9's list at `:111` describes the full pass only, and could add "on a full pass".
- **The choice to leave design 03 unchanged** is Q13's to settle, and is not counted.

## The smallest set of changes to approvable

1. **M1.** Choose one:
   - Key the fifth clause on the Gateway and foreign-policy reads themselves, and state their cost.
   - Or state that it holds only until the deadline, and correct `:95`, `:101`, `:637` and Q12.

   Then extend row :449 past a shortened deadline, and add a `ForeignTrafficPolicy` row.
2. **m2 to m7.** One sentence each, plus the row constraints:
   - m2: specify how the wedge's detection is computed: where in the pass, the any-ref reading, the fallback, and where R's name comes from.
   - m3: state the GC cost in the apart case, at `:118` and in Q13.
   - m4: correct Q13's first premise, and consider the card re-read option.
   - m5: gate `Interrupted` records on the no-transaction predicate, or count them.
   - m6, row :463: hold the `Lock` short of `Served` until the promotion is seen.
   - m6, row :447: state that D is available and carded, and that only R's card fails.
   - m7: fix `:252`, `:275`, `:326`, `:635` and `:115`, and add the foreign-policy case at `:116`.

**design 16 §1.1 (A9): FAIL (REVISE)**
