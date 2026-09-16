# Critique: design 16 A5 — the first slice (§1.1), revised again

- **Date**: 2026-09-14
- **Target**: `docs/designs/16-evalsuite.md` §1.1 and amendment A5, at `c130bea` (branch `design-16-first-slice`). A5 answers `reviews/16-a4-critique.md` (REVISE: 0 BLOCKER, 6 MAJOR, 7 MINOR). The first critique is `reviews/16-a3-critique.md`.
- **Independence**: an independent session. It wrote neither the draft, A4, A5, nor either earlier critique.
- **Read against**:
  - ADR-0024 and ADR-0030;
  - design 02's rollout and status;
  - design 03 §3.3 and §3.3.3: the transaction record, `Adopt`/`Refused`, the deadline outcomes and A75;
  - the code: `internal/controller/agent_controller.go` (the rollout switch, `writeStatus`, `withholdReady`, `SetupWithManager`, the RBAC markers), `authtxn.go` (`authStep`, `refuseAdopt`, `persistStatus`, `requireFreshAgent`, the deadline branches), `httproute.go` (`transientRouteWrite`), `discovery.go`, `conditions.go`, `api/v1alpha1/agent_types.go`, `cmd/operator/main.go`, `go.mod` and `charts/assayd/crds/assayd.dev_agents.yaml`.
- **Method**: every claim A5 makes about code was traced to its source. Nothing was run or mutated, because nothing in the slice is implemented. Where a finding rests on how controller-runtime v0.24.1 behaves (the reconcile context on shutdown, and the leader-election default), it says so. The echo rule's bounds were **not** re-measured here: they rest on the re-critique's envtest v1.36.2 measurement.

**Verdict: REVISE — 0 BLOCKER, 2 MAJOR, 8 MINOR.**

A5 answers the re-critique faithfully, and the two mechanisms at its centre hold.

- **The claim-then-send protocol makes a double send impossible**, across workers, across processes, and across a leader handover ("Verified sound").
- **No deadlock can form between the eval and design 03's transactions**, because no transaction waits on a promotion.

Both MAJORs are about the edges of those mechanisms, not their core:

- **P1.** The serialisation predicate, "`status.auth.transaction` is unset", also matches a refused `Adopt`, which is a standing record and not a transaction in flight. So every gated Agent whose route was published before the compiler never evaluates a revision, and nothing says so. The same predicate also waits indefinitely on any transaction past its deadline, not only on A75's hold.
- **P2.** What a case sent while the manager shuts down records is unspecified. Controller-runtime cancels the reconcile context on shutdown, so the obvious implementation fails the in-flight case on every rolling update of the operator, possibly as `Unreachable`, and Q11's "rare" rests on the opposite.

## Closure of the second critique

| Finding | A5's answer | Closed? |
|---|---|---|
| N1 the send outside the lock | Step 10's read-live, claim-under-lock, read-back, send, record-while-the-claim-stands; `EvalRunning` no longer names k of N; Q11; stale-reader unit test and hit-count envtest (`16-evalsuite.md:96-97`, `:124-130`, `:342-345`, `:383-385`) | **Closed.** No double send is possible ("Verified sound"). What a claim left by a *graceful* shutdown records is P2, and Q11's mechanics have gaps (m3) |
| N2 a stale Agent CRD | The eval controller reads back its run-start write and every claim; the Agent reconciler checks the installed CRD's schema and sets `EvalRecordNotKept`; the upgrade is owed to design 07; envtest (`:131-134`, `:277`, `:386`) | **Closed.** The hazard is real: the shipped CRD's `status.eval` carries five fields and no `x-kubernetes-preserve-unknown-fields` (`charts/assayd/crds/assayd.dev_agents.yaml:1297-1325`). The envtest's first mutation survives (m1), and the schema read's mechanics are unstated (m2) |
| N3 reasons only the eval controller sees | `status.eval.blocked {reason, message}`, read by the Agent reconciler; owed to design 02; envtest (`:169`, `:176`, `:190-191`, `:387`) | **Closed** |
| N4 the evaluated Agent's own `Lock` | Option (i): no case is sent while `status.auth.transaction` is set, and the Agent reads `EvalWaitingForAuth`. The claim is narrowed to other Agents. The J2 envtest now has a 1 s stub (`:92`, `:98`, `:186`, `:209`, `:394`) | **Closed for the `Lock`.** The predicate captures more than transactions in flight (P1). "Never conflicts" is slightly over (m4). The J2 row does not say how its window is built (m8) |
| N5 pin eligibility | Gated Agents only; refuse the current candidate and a failure against the **current** suite; the false refusals and the break-glass stated; (a″) offered; test row rebuilt (`:156`, `:315-321`, `:379`) | **Closed.** "The current suite digest" is undefined while the suite cannot be read (m7) |
| N6 the echo rule cannot install | `has()` guards; expectations capped at 512; an install test with two mutations (`:78`, `:388`) | **Closed**, on the re-critique's measurement, which this review did not repeat |
| n1 Q6(c) | Measurement stated with the `resourceNames` result; envtest and a k3s v1.33.6 e2e owed (`:326`, `:389-390`) | **Closed** |
| n2 an unevaluated serving revision | `ActiveRevisionNotEvaluated`; a removed gate flips to `False/NoGatesDeclared`; envtest (`:182`, `:197`, `:391`) | **Closed** |
| n3 the O(k) bound | Stated, with no run deadline (`:90`, `:252`) | **Closed** |
| n4 the run-start client | The eval client; `cardDigest` recorded (`:106`, `:168`) | **Closed.** No test asserts `cardDigest` (m8) |
| n5 the collision-pair test | The eval controller is not started (`:364`) | **Closed** |
| n6 over-granted RBAC | `get` only (`:278`) | **Closed** |
| n7 generation lag | An `observedGeneration == generation` predicate; the env-source window stated; envtest (`:98`, `:392`) | **Closed for the eval controller.** What the Agent reconciler reports when it evaluates that clause is unstated (m6), and the envtest does not say how its window is built (m8) |

## New findings

### P1 — MAJOR: the serialisation predicate also matches a refused `Adopt`, so a gated Agent whose route predates the compiler never evaluates a revision, and the text does not say so

`16-evalsuite.md:92`, `:98`, `:186`, `:209`.

The shared predicate is "**no `-auth` transaction is in flight on the Agent**, meaning `status.auth.transaction` is unset" (`:98`). But `status.auth.transaction` does not hold only transactions in flight:

1. **A refused `Adopt` lives there for good.** `refuseAdopt` writes `status.auth = {transaction: {kind: Adopt, stage: Refused, refusedMode}}` and nothing else (`internal/controller/authtxn.go:2059-2060`). `AuthTransaction` says that "an Adopt only ever sits at stage Refused" (`api/v1alpha1/agent_types.go:744-745`). Design 03's stage table calls `Refused` "nothing — no write to the route or to any policy", and names only two exits: recreating the Agent, or K2's edit to `apikey` (`03-policy-compiler.md:480`). Every Agent whose route was published before the compiler, or whose `status.auth` was lost, carries this record for as long as its owner leaves it (`GovernanceSkipped=CompilerUpgradeUnsupported`).
2. **A transaction past its deadline stays there too.** "At the deadline the machine stays in its stage and keeps working" (`03-policy-compiler.md:790`), and so does the code (`authtxn.go:1354-1370`, `:1833-1840`). That covers a NACK, a route that never goes `Accepted`, and a `ProbingBefore` that never observes.

Take an upgraded install with the compiler on. An Agent declares a gate and was served before the compiler, so its route reads `Adopt`/`Refused`. Once the EvalSuite CRD is applied, every revision its owner mints is held: `GatesPassed=False/EvalWaitingForAuth`, "naming the transaction's kind and stage", which is `Adopt`, `Refused`. The row says "an `-auth` transaction is in flight" (`:186`), but none is. None will finish on its own, and the text names no exit.

The only exits are to lock the route to `apikey`, which is one-way in the slice, or to remove the gate. Those are two unrelated decisions welded together, and the design never offers the weld as a choice. The reason A5 gives for serialising does not apply either. Serialising exists so that eval patches do not make a transaction's status writes conflict (`:92`). A refused `Adopt` writes nothing after its first pass: `persistStatus` goes through `writeStatus`, which returns early on an equal status (`agent_controller.go:1783`).

The indefinite wait is stated for one case only, "while a Gateway-level hold keeps a transaction at `ProbingAfter`" (`:92`). An implementer who follows the predicate literally builds the wedge. One who reads the rationale excludes `Adopt`. Either way they are guessing.

**Fix.**

- Define the predicate as "a `Create` or `Lock` is recorded in `status.auth.transaction`", and exclude `Adopt`/`Refused` by name. Or keep it, and state the wedge and its two exits as a cost the human accepts.
- State that any `Create` or `Lock` past its deadline also holds the eval indefinitely. It is already paged, through `PolicyApplyIncomplete`.
- **Test.** A gated Agent with a refused `Adopt` evaluates and promotes its candidate, under the first answer, or reads the stated reason, under the second. Mutation: the predicate keyed on `transaction != nil`.

### P2 — MAJOR: what a case sent during a graceful shutdown records is unspecified, and the default fails the case on every operator rollout, which Q11's "rare" contradicts

`16-evalsuite.md:124-130`, `:112-118`, `:342-345`.

Step 10 names three ways a claim is left with no outcome: "a crash, a restart, or a lost leader election" (`:129`). Q11 recommends at-most-once because "an operator restart mid-case is rare, and its cost under (a) is one suite edit" (`:345`).

A restart is not rare. It happens on every `helm upgrade`, every node drain, and every rolling update of the operator's Deployment.

On a graceful stop, controller-runtime (v0.24.1, `go.mod:11`) cancels the context it passes to `Reconcile`, then waits up to `GracefulShutdownTimeout` for in-flight reconciles. That is 30 s by default, and neither `cmd/` nor `internal/` sets it. This is read from controller-runtime's behaviour, not run here. The design names no context for the send or the record. So the obvious implementation, which uses the reconcile context for both, does this at SIGTERM:

1. The request is aborted mid-flight, after the candidate may already have received it and started a task.
2. It is recorded as `Unreachable`, if the implementer maps a transport error through the outcome table (`:118`). That reason names a cause that was never observed, against rule 8. Or it is recorded as `Interrupted` by the next leader, if the record patch fails on the same cancelled context.

Either way the case fails. Under the default threshold of `"1"` (`:79`), the revision fails, and its pair goes into `failed[]` for good (`:121`). Each rolling update can fail up to two runs, one per worker.

The leader lease is released only after runnables return (`LeaderElectionReleaseOnCancel`, `cmd/operator/main.go:162`), so there is room to finish. A case is bounded by 10 s, and one patch fits well inside 30 s.

**Fix.**

- Send, and record, on a context that is not cancelled by manager shutdown, bounded by `caseTimeoutSeconds` plus the record's retry. State that the graceful-shutdown window covers it.
- State that only a hard stop leaves an unrecorded claim: SIGKILL, OOM, node loss, or a lease lost to a partition. Q11's "rare" then holds.
- State that a send cut short by cancellation, if one happens, is `Interrupted` and never `Unreachable`. Add `Interrupted` to the outcome table (m3).
- **Test** (envtest): cancel the manager's context while the stub is holding a case. The case records the stub's answer, and the next manager sends nothing. Mutation: send on the reconcile context.

### m1 — MINOR: the stale-CRD envtest's first mutation cannot kill it, because two guards stop the same send

`16-evalsuite.md:132-133`, `:386`.

The eval controller has two guards: it reads back what it writes, and it "makes the same check" of the CRD's schema, "and sends nothing" (`:133`). The owed row installs a CRD without the new fields and lists "drop the read-back" as a mutation. With the read-back deleted, the schema check still stops every send. So the stub still sees zero cases, the Agent still reads `EvalRecordNotKept`, and the mutation survives (rules 1 and 5).

**Fix.** Either drop the eval controller's schema check and keep the read-back as its guard, or pin each guard separately. The read-back needs a state the schema check passes: a CRD that carries `cases` and `blocked` but not `claim`, `endpointPath` or `cardDigest`, or an injected client that strips fields from the patch response. Mutation: drop the read-back, and the stub then sees case 1 repeatedly.

### m2 — MINOR: the schema check is implementable, but its mechanics are unstated, and the reason for it is overstated

`16-evalsuite.md:133`, `:176`.

- **RBAC is present.** `customresourcedefinitions` `get;list;watch` is granted (`agent_controller.go:285`, `charts/assayd/files/operator-rules.yaml:57`).
- **But the operator reads no CRD object today.** `EvalSuiteDetector` uses the discovery API (`internal/controller/discovery.go`), and the scheme registers no `apiextensions` types (`cmd/operator/main.go:44-50`). `k8s.io/apiextensions-apiserver` is already a direct dependency, so registering them costs nothing. What the design must still say:
  - **Which reader.** A read through the manager's cached client starts a cluster-wide informer on every CRD, which is the argument `main.go:133-150` makes against caching Secrets. An uncached read is a GET of a 74 KB object (`charts/assayd/crds/assayd.dev_agents.yaml`) on every pass of every gated Agent, and every eval patch triggers such a pass. A TTL cache, like the detector's, fits.
  - **Which version.** The version to check is `v1alpha1`, under `spec.versions[].schema`.
  - **What a failed read yields.** Hold under `EvalRecordNotKept`, or under a reason of its own.
- **"A pruned write cannot carry its own reason" is true only of the new fields.** The stale CRD keeps `status.eval.{suite, revision, revisionDigest, at, score}`, and conditions and Events survive (`events: create;patch` is granted, `agent_controller.go:269`). A run-start write that leaves `revisionDigest` naming the candidate and no `verdict` is a signature the Agent reconciler could read with no CRD access at all. The schema check is still a sound choice, so this is a sentence to narrow, not a mechanism to change.

### m3 — MINOR: Q11's mechanics have gaps

`16-evalsuite.md:110-130`, `:342-345`.

- **`Interrupted` is not in the outcome table** (`:112-118`), which the unit test enumerates "one case per row" (`:353`).
- **An ambiguous claim fails a case that was never sent.** A claim patch that the API server commits, but whose response is lost (a timeout, a 5xx, a reset), is not a `Conflict`. The pass sends nothing. The next pass finds the claim with no outcome, and records `Interrupted` for a case the candidate never received. It fails closed. State it, or keep the nonces of ambiguous claims in memory and treat such a claim as unsent within the process.
- **A handover can overwrite `Interrupted`.** Two managers can briefly overlap in a handover: a lease lost to a partition, with the old process not yet exited. The old leader's record step re-applies "while the claim still stands: the same `revisionDigest`, `suiteDigest`, and `claim`" (`:128`). If the new leader's `Interrupted` write keeps the nonce, the old leader overwrites it after the new leader may already have computed the verdict. `cases[]` then disagrees with `verdict`. Require `outcome == Sending` as well. Also state that the eval controller is leader-gated, which is controller-runtime's default and is not overridden anywhere in `cmd/` or `internal/`, even though at-most-once does not depend on it.
- **The list of causes is incomplete.** Step 10's causes (`:129`) omit a record that exhausted its retries.
- **Q3(b)'s retry of an `Unreachable` case does not say whether it covers `Interrupted`.** Say whether it does.

### m4 — MINOR: "an eval patch never makes the `Lock`'s status writes conflict" is slightly over

`16-evalsuite.md:92`, `:209`.

The check is made at the live read before the claim. So a case already in flight when a `Create` or `Lock` is entered records its outcome after the transaction began. That record bumps the resourceVersion. The next Agent pass then either fails its `Update` with `Conflict`, or stops at `requireFreshAgent` with `errStaleAgent` (`authtxn.go:1051-1061`), which `reconcileGateway` does on every gateway pass. For a `Create`, or for a held, re-created or past-deadline `Lock`, that pass withholds `Ready` with "this pass lost a race" (`agent_controller.go:837-869`). The cost is one pass, bounded by one case. Say "at most one outcome, of the case in flight when the transaction began", instead of "never".

### m5 — MINOR: the `EvalWaitingForAuth` row's phase and `Ready` contradict design 03's aggregation

`16-evalsuite.md:186`.

The row gives phase `Held` and `Ready=True/Available`. But a re-creation `Create`, a missing-policy `Lock`, a J2 or K2 `Lock` under A75's hold, and any `Lock` past its deadline each withhold `Ready` and set `Degraded` (`withholdReady`, `agent_controller.go:932-947`; `03-policy-compiler.md:342`, `:779`). Say "phase and `Ready` as design 03's transaction sets them", and keep only `GatesPassed` and `Progressing` in the row.

### m6 — MINOR: what the Agent reconciler reports when it evaluates the generation clause is unstated

`16-evalsuite.md:98`.

The predicates are "implemented once and called by both". In the Agent reconciler's first pass after a spec edit, the status it read has `observedGeneration < generation`. It sets `observedGeneration` later in the same pass, at many sites (for example `agent_controller.go:875`, `:884`). If it evaluates the clause on the status it read, the predicate is false and no `GatesPassed` row covers that state. If it evaluates the status it is building, the result depends on order. Say that the generation clause is the eval controller's alone, or that the Agent reconciler treats it as true because it is the observer.

### m7 — MINOR: Q5(a)'s "current suite digest" is undefined when there is no current suite

`16-evalsuite.md:315`.

The refusal keys on a `failed[]` entry "paired with the **current** suite digest". While the suite is `EvalSuiteNotFound` or `EvalSuiteUnreadable`, there is no current digest. Say whether a pin is then refused or admitted: refusing errs closed, and admitting keeps instant rollback. Say also whether the rule applies under `GatesSkipped=EvalSuiteCRDAbsent`, where a gate is declared but skipped.

### m8 — MINOR: three owed rows do not say how their state is built, and one field has no test

`16-evalsuite.md:392`, `:394`, `:168`.

- **J2 serialisation (`:394`).** "The stub sees no case while `status.auth.transaction` is set" kills "drop the transaction predicate" only if the candidate is available, with its card recorded, while the `Lock` is still in flight. A probe stub that answers at once lets the `Lock` reach `Served` before the candidate is even available, and the mutation survives. Hold the `Lock` at a stage, for example with a probe stub that withholds the `401`, until the candidate is available and carded. Then assert zero hits across that interval.
- **Generation lag (`:392`).** Say that the eval controller runs without the Agent reconciler, so the old candidate stays recorded after the edit.
- **`cardDigest`.** No row asserts that `cardDigest` is recorded. It has no reader, so rule 5 asks for either a test or its removal.

## Verified sound

- **A case cannot be sent twice.**
  - **Two workers, one process.** `MaxConcurrentReconciles: 2` never hands one key to two workers at once: controller-runtime's workqueue holds a key that is being processed. So a later pass on the same Agent always follows the earlier pass's return.
  - **Two processes, or a handover.** Both read resourceVersion n and patch at n. The optimistic lock admits one claim and refuses the other, and "a claim that fails with `Conflict` sends nothing" (`:126`). A claim found with no outcome is never re-sent under Q11(a), so an old leader still mid-send and a new leader cannot both send case k.
  - **The read-back against the Agent reconciler.** The read-back is taken from the patch response, not a second GET, so nothing can come between them. Nor can the Agent reconciler erase a claim: its only status write is a full `Status().Update` at the resourceVersion it read (`writeStatus`, `agent_controller.go:1782-1791`), and its only other patch touches finalizers (`:1776`). A read made before the claim conflicts.
- **No deadlock between the eval and design 03's transactions.** Every transaction keys on `status.activeRevision` and the serving route, never on a candidate's promotion.
  - A `Create` is entered only once `ActiveRevision != ""` (`authtxn.go:736`).
  - J2 and K2 need `ActiveRevision` (`:632`, `:714`).
  - The two re-creations take their target from `status.auth` (`03-policy-compiler.md:599`).
  - So a new gated Agent has no transaction until its first revision is promoted, and on a served Agent the eval waits for the transaction while the transaction waits for nothing the eval holds. The wait under A75's hold is stated honestly as indefinite and fail-closed (`:92`). P1 is about what else the predicate matches, not about a cycle.
- **An eval patch that only records a case leaves the Agent reconciler's write a no-op.** `EvalRunning` no longer changes per case, and `writeStatus` returns on an equal status (`agent_controller.go:1783`). A stale cache after an eval patch is transient: `errStaleAgent` is in `transientRouteWrite` (`httproute.go:487-489`), so an Agent with no transaction changes no condition on it.
- **The pruning hazard of step 11 is real**, as N2 said. The shipped Agent CRD has no `x-kubernetes-preserve-unknown-fields` anywhere, and `status.eval` carries only `at`, `revision`, `revisionDigest`, `score` and `suite` (`charts/assayd/crds/assayd.dev_agents.yaml:1297-1325`).
- **RBAC.** `agents/status` `get;update;patch` (`agent_controller.go:214`) covers the merge patch. `customresourcedefinitions` `get` is already granted. `evalsuites` `get` alone suffices for uncached reads with no informer.
- **Q5(a) as narrowed is coherent with its test row.** (B, S1) is not paired with the current S2, so B's pin promotes. A digest that failed the current suite is refused. Ungated Agents are untouched. (a″) is fairly costed.
- **Q11(a) is the right default** once P2's graceful path is specified. A case is a real task with real side effects, and A2A gives no de-duplication the slice can rely on.
- **The eleven questions are complete for the slice**, apart from P1's choice, which should be stated either as a rule or as a twelfth question. Each carries its costs, and Q1, Q3, Q5, Q9, Q10 and Q11 are argued from the code rather than from intent.

## The smallest set of changes to approvable

1. **P1.** Key the serialisation predicate on a `Create` or `Lock` in `status.auth.transaction`, not on any transaction, or state the `Adopt` wedge and its exits as a decision. State that a transaction past its deadline also holds the eval indefinitely. Add the refused-`Adopt` envtest with its mutation.
2. **P2.** Specify the send's and the record's context: not cancelled by manager shutdown, and bounded inside the graceful-shutdown window. A send cut short by cancellation is `Interrupted`, never `Unreachable`. Restate Q11's "rare" as "a hard stop". Add the shutdown envtest with its mutation.
3. The MINORs are a sentence or a test row each, and can be folded into the same revision.
