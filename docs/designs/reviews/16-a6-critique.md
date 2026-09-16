# Critique: design 16 A6 — the first slice (§1.1), revised a third time

- **Date**: 2026-09-14
- **Target**: `docs/designs/16-evalsuite.md` §1.1 and amendment A6, at `49b7cc9` (branch `design-16-first-slice`). A6 answers `reviews/16-a5-critique.md` (REVISE: 0 BLOCKER, 2 MAJOR, 8 MINOR). The earlier records are `reviews/16-a3-critique.md` and `reviews/16-a4-critique.md`.
- **Independence**: an independent session. It wrote neither the draft nor any of A4, A5 and A6, and none of the three earlier critiques.
- **Read against**:
  - ADR-0024 and ADR-0030;
  - design 02's rollout and status;
  - design 03 §3.3.3: the deadline table, abandonment, the promotion during a `Lock`, and the attribution of a `401`;
  - the code:
    - `internal/controller/agent_controller.go` and `conditions.go`;
    - `authtxn.go`: `refuseAdopt`, `recreateRoute`, `lockMissingPolicy`, `runLock`'s `ProbingBefore` and `ProbingAfter`, `cardAttributes`, `recordServed`, `abandons` and `abandonWaiting`;
    - `api/v1alpha1/agent_types.go` (`AuthTransaction`, `EvalStatus`);
    - `cmd/operator/main.go`;
    - `charts/assayd/templates/operator.yaml`.
- **Controller-runtime v0.24.1 source, read from the module cache**:
  - `pkg/manager/internal.go`: `engageStopProcedure`, `initLeaderElector` and the defaults;
  - `pkg/internal/controller/controller.go`: `Start`, `processNextWorkItem` and `reconcileHandler`;
  - `pkg/controller/controller.go`: the priority-queue default.
- **Method**: every claim A6 makes about code or about controller-runtime was traced to its source. Nothing was run or mutated, because nothing in the slice is implemented.

**Verdict: REVISE — 0 BLOCKER, 1 MAJOR, 4 MINOR.**

A6 closes all ten of the third critique's findings, and the two mechanisms it adds hold against controller-runtime's source.

- **The graceful-shutdown send fits the window.** The lease is held and renewed until the case in flight has finished and been recorded.
- **Neither handover path can put two leaders on the same case.** On a graceful handover no new leader can start while the old one is still sending. On a lease lost to a partition, `outcome == Sending` is enough.

The one MAJOR is a cycle that A6's new wait rule makes explicit, and that §1.1 says cannot exist. On a gated Agent whose serving revision has no recorded card, a `Lock` can reach `Served` only through a promotion, and the eval waits for the `Lock` before it can earn one. The MINORs are about the shutdown mechanics' premises, a pacing gap that A6's m1 answer opened, and two test rows.

## Closure of the third critique

| Finding | A6's answer | Closed? |
|---|---|---|
| P1 the predicate matched a refused `Adopt` | The predicate is keyed on a `Create` or `Lock` in `status.auth.transaction`, with each kind's stages listed; a refused `Adopt` is excluded by name, and an abandonment in progress counts; the past-deadline wait is stated and becomes Q12; envtests with mutations (`16-evalsuite.md:92-95`, `:102`, `:197`, `:360-363`, `:408-409`) | **Closed.** The stage lists are complete. `recordServed` replaces `status.auth` whole, with no transaction (`authtxn.go:1455-1466`), so no `Served` record lingers. An abandonment keeps its kind in the slot until it finishes (`abandonWaiting`, `:941-948`). The re-creation `Create` and the missing-policy `Lock` are of the listed kinds (`:1554`, `:1568`). The refused `Adopt` exclusion is sound: `refuseAdopt` re-ensures the serving route on `status.activeRevision` on every pass (`:2033-2043`), so "each promoted revision serves on it" is true. What the wait rule still does not see is R1 |
| P2 graceful shutdown | Send and record on `context.WithoutCancel`, bounded at 10 s + 5 s, inside the 30 s `GracefulShutdownTimeout` and the chart's 30 s `terminationGracePeriodSeconds`; no new claim once shutdown begins; `Interrupted`, never `Unreachable`; Q11 restated; shutdown envtest and chart test (`:132`, `:134`, `:359`, `:404`, `:407`) | **Closed.** Verified against the source below. There are three residues: the claim's own round-trip, the "no new claim" rule, and the manager-side bound (r1, r2) |
| m1 two guards on one send | The eval controller drops its schema check; the read-back and the Agent reconciler's schema check are pinned separately (`:139`, `:412-413`) | **Closed.** Each row's mutation now kills it. Dropping the eval controller's schema check leaves a stale CRD with no guard before the write, and what a failed read-back does next is unstated (r3) |
| m2 the schema check's mechanics | `apiextensions.k8s.io/v1` registered; `v1alpha1`'s `schema.openAPIV3Schema`; uncached behind a TTL cache; `EvalRecordUnverifiable` on a failed read with nothing cached; the pruning sentence narrowed (`:140-144`, `:199`) | **Closed** |
| m3 Q11's mechanics | `Interrupted` in the outcome table; the ambiguous claim fails closed; the record requires `outcome == Sending`; leader-gating stated; a record that exhausted its bound is listed; Q3(b) never retries `Interrupted` (`:123`, `:133-136`, `:320`) | **Closed.** `outcome == Sending` is sufficient ("Verified sound") |
| m4 "never conflicts" | At most one outcome, of the case in flight when the transaction began (`:96`, `:221`) | **Closed** |
| m5 the `EvalWaitingForAuth` row | Phase and `Ready` as design 03's transaction sets them (`:197`) | **Closed** |
| m6 the generation clause | The eval controller's alone; no `GatesPassed` row depends on it (`:102`) | **Closed** |
| m7 no current suite | Only the current-candidate refusal applies, and a `failed[]` digest is admitted; the rule is off under `EvalSuiteCRDAbsent`; argued from rollback during an incident; envtest (`:331`, `:411`) | **Closed** |
| m8 three rows' construction | J2 holds the `Lock` at `ProbingAfter` through a withheld `401` until the candidate is available and carded; generation lag runs without the Agent reconciler; a `cardDigest` row (`:410`, `:419`, `:421`) | **Closed** |

## New findings

### R1 — MAJOR: a `Lock` on a gated Agent whose serving revision has no recorded card can finish only through a promotion, and the eval waits for the `Lock`, so "the `Lock` does not wait for the verdict" is false there and Q12's cost is misstated

`16-evalsuite.md:92-95`, `:221`, `:360-363`.

§1.1 says: "The `Lock` does not wait for the verdict. The verdict waits for the `Lock`" (`:221`). The third critique verified that no transaction waits on a promotion, from where each transaction takes its target. That is true of targets. It is not true of **completion**, because a `Lock` credits its `401` only on evidence tied to the revision the route serves:

- `ProbingAfter` credits a `401` only when `tx.BeforeObserved`, or when `cardAttributes` finds a recorded card digest for a revision the route's `backendRefs` name (`internal/controller/authtxn.go:1752-1757`, `:1956-1974`).
- `BeforeObserved` is set only "on the card the route's one backendRef names, by the digest `status.cards` records for that revision" (`:1654-1666`; `api/v1alpha1/agent_types.go:784-787`). A missing-policy `Lock` has no `ProbingBefore` at all: it is entered at `ApplyingPolicies` (`lockMissingPolicy`, `authtxn.go:1562-1575`).
- So when the serving revision has no recorded card, the `Lock` reaches `Served` only if a card re-read succeeds, or if a promotion moves the route's `backendRef` to a revision whose card is recorded. Design 03 §3.3.3 relies on the second path: its promotion-during-a-`Lock` rule credits "on the card digest `status.cards[]` records for the new revision" (`16-evalsuite.md:221`; `03-policy-compiler.md:1307`, `:1358`).

On a gated Agent that promotion needs a `Passed` verdict. The verdict needs claims. And A6's rule claims nothing while the `Lock` is recorded, whatever its stage or deadline (`:92`, `:95`). That is a cycle.

It is reachable without any race. A gated Agent's serving revision can lack a recorded card in three ways:

- **A failed drift re-read clears it.** `recordCardAttempt` does this (`:158`, `:226`). A card endpoint that keeps failing keeps it cleared.
- **The gate was added over a revision** that served ungated with a card that never validated (`ActiveRevisionNotEvaluated`, `:209`).
- **A pin that Q5 admits** selects a retained revision whose card was never recorded, because nothing in Q5 requires one.

Take a served API-key Agent in that state whose `<agent>-auth` is deleted. The missing-policy `Lock` re-writes the policy, gets a `401` it cannot attribute, passes its deadline, and pages `AuthLockUnverified`. Throughout, "the route keeps serving, open" (`03-policy-compiler.md:80`, `:787`). The owner's natural fix is to ship an image whose card validates, and that fix mints a candidate that holds under `EvalWaitingForAuth` for good. A missing-policy `Lock` cannot be abandoned by a spec edit either: its target is the recorded mode, and `apikey` → `none` is refused. J2 and K2 wedge the same way when `ProbingBefore` could not observe. They at least have abandonment as an exit.

Exits do exist:

- remove `spec.gates` (Q9's break-glass);
- pin to a retained revision whose card is recorded;
- for J2 or K2, revert the mode.

None of these is stated. `EvalWaitingForAuth` names the `Lock`, and the `Lock`'s message names an unattributable `401`. Nothing connects the two. Q12(a) states its cost as "an Agent with a wedged transaction gets no new revision until an administrator clears the cause, which is already true under A75's hold" (`:363`). That is wrong here, because the cause is the serving revision's card, which only a new revision clears. It is also circular: "already true under A75's hold" is true only because of this slice's own rule. An ungated Agent under A75's hold still promotes.

Under the standard, this is a stated guarantee that is false in a stable, reachable state. That state prolongs an unauthenticated route and blocks its remedy. It fails closed on the candidate, but not on the route.

**Fix.** Choose one, and say which:

1. **Exempt the case.** Claims proceed while a `Lock`'s only unmet condition is `cardAttributes`' "no card digest is recorded for a revision the route serves". The cost is at most one lost `Lock` pass per recorded outcome, which is what Q12(b) already accepts. The candidate's promotion is then the `Lock`'s way out.
2. **Keep the wait and state the cycle.** State it at `:95` and `:221`. Correct Q12(a)'s cost, drop "already true under A75's hold", and have `EvalWaitingForAuth`'s message name the three exits when the `Lock`'s blocker is attribution.

**Test** (envtest, either answer): a gated API-key Agent whose serving revision's card digest is cleared, and whose `<agent>-auth` is then deleted.

- Under (1), the candidate is evaluated and promoted, and the `Lock` credits its `401` on the candidate's recorded card. Mutation: wait on every `Lock`.
- Under (2), `EvalWaitingForAuth`'s message names removing the gate, a pin, and, for J2 or K2, reverting the mode. Mutation: the generic message.

### r1 — MINOR: a graceful stop can still leave a claim with no outcome, and "no new case is claimed once shutdown begins" has no stated mechanism and no test

`16-evalsuite.md:132`, `:134`, `:359`, `:404`.

- **The claim's own round-trip.** The claim is not among the operations A6 moves to `WithoutCancel`, so it runs on the reconcile context. That context is the controller's `Start` context, and shutdown cancels it (`pkg/internal/controller/controller.go:312`, `:461-475`). A cancellation that lands while the claim patch is in flight can commit the claim and lose the response. That is step 10's ambiguous claim, on a graceful stop. The next leader records `Interrupted` for a case the candidate never received. So "only a hard stop leaves a claim with no outcome" (`:134`, `:359`) is slightly over. The window is one patch round-trip per rollout, and it fails closed.
- **The "no new claim" rule is unpinned.** It holds today only because a claim issued on a cancelled context fails. An implementer who puts the whole pass on `WithoutCancel` keeps sending during the drain. Items still in the queue at shutdown can be handed to a worker with a cancelled context. That depends on the queue: the priority queue is the default here, `pkg/controller/controller.go:253`, and this review did not trace its shutdown path. The shutdown envtest (`:404`) uses one Agent, so it cannot see this.

**Fix.**

- Check `ctx.Err()` immediately before the claim, and issue the claim itself on the uncancelled context. A claim that starts then always completes, and none starts after cancellation. Or state the round-trip window beside the ambiguous claim.
- Add a second Agent to `:404`, queued before the cancellation. It gets no claim. Mutation: claim on the uncancelled context without the `ctx.Err()` check.
- Also state that the send runs synchronously inside the pass. A send in a goroutine is not waited for: `Start` waits only for its workers (`:324-326`).

### r2 — MINOR: the 15 s-inside-30 s bound rests on an unset manager default that no owed test pins (rule 7)

`16-evalsuite.md:132`, `:288`, `:407`.

The chart test pins the kubelet side, `terminationGracePeriodSeconds ≥ 20`. The manager side is `GracefulShutdownTimeout`, which the design relies on at its default of 30 s. It is pinned by nothing: "nothing in `cmd/` or `internal/` sets that". Envtest builds its own manager, so a later `GracefulShutdownTimeout: 10 * time.Second` in `cmd/operator/main.go` would survive every owed row. Then the manager stops waiting at 10 s and releases the lease. The process exits, and the case in flight becomes `Interrupted` on every rollout. That is the failure P2 closed.

**Fix.** Set `GracefulShutdownTimeout` explicitly in `main.go`, or build the manager options in a function a unit test can read. Owe a test that it is at least the case bound plus the record bound. Mutation: set it to 10 s.

### r3 — MINOR: with the eval controller's schema check removed, what a failed read-back does next is unstated, and the obvious reading re-fetches and rewrites on every pass

`16-evalsuite.md:128`, `:139`, `:412`.

A6's m1 answer removes the only guard that stopped a stale CRD before any write. Take a stale Agent CRD. Each eval pass finds no run for the pair, because `verdict` was pruned. It fetches the candidate's card (step 2), writes the run-start, and fails the read-back. The step says only that it "sends nothing". It does not say what the pass returns.

The pruned write still carries the older fields (`suite`, `revision`, `revisionDigest`, `at`, `api/v1alpha1/agent_types.go:633-652`). If `at` takes the run-start time, every write changes the object. Step 9 says the controller's own status write re-enqueues it at once, because `For(&Agent{})` has no generation predicate. The result is a per-Agent loop of one card fetch and one status patch at event speed, on two workers, for every gated Agent, until someone applies the CRD. That is the default state after `helm upgrade`. The Agent reconciler's `EvalRecordNotKept` names the cause, but does not stop the loop.

**Fix.** A failed read-back returns `RequeueAfter: CardDriftInterval`, and does not rewrite a run-start whose earlier write failed its read-back. Or run-start writes nothing that changes from pass to pass. Add to `:412` a bound on the stub's card fetches and on the Agent's resourceVersion over a fixed interval. Mutation: return `Requeue`, or nothing, after a failed read-back.

### r4 — MINOR: two owed rows leave their construction or their assertion short

`16-evalsuite.md:405`, `:408`.

- **The handover row (`:405`)** does not say how "an old leader's record arrives after the new leader wrote `Interrupted`". The simplest build needs no second manager. While the stub holds the case, the test itself writes `Interrupted`, keeping the nonce. The stub then answers. The record's first attempt conflicts, its re-read finds `Interrupted`, and it drops. Say so, so no implementer tries to stage two leaders.
- **The refused-`Adopt` row (`:408`)** asserts that the candidate "evaluates and promotes", which is `status.activeRevision`. A6's new claim is that "each promoted revision serves on it" (`:94`). Assert the route's one `backendRef` names the promoted revision's Service. Mutation: skip `ensureServingRoute` in `refuseAdopt`.

## Verified sound

- **The graceful-shutdown send fits, and the manager waits for it** (controller-runtime v0.24.1).
  - `reconcileHandler` passes the controller's `Start` context to `Reconcile` (`pkg/internal/controller/controller.go:461-475`), so the reconcile context is cancelled at shutdown, as §1.1 says.
  - After cancellation, `Start` blocks on `wg.Wait()` for its workers (`:324-326`). `engageStopProcedure` then stops the leader-election runnables under a shutdown context of `gracefulShutdownTimeout`, 30 s by default (`pkg/manager/internal.go:57`, `:509-514`; `pkg/manager/manager.go:575`).
  - A pass that sends on `WithoutCancel` synchronously is therefore waited for, for up to 30 s. Its 15 s fits inside that, and inside the chart's `terminationGracePeriodSeconds: 30` (`charts/assayd/templates/operator.yaml:55`). The chart has no `preStop` hook, and its rollout is `maxSurge: 0` (`:34-38`).
- **The lease outlives the send on a graceful stop.** `leaderElectionCancel()` runs in a `defer` that fires only after the shutdown context is done (`internal.go:543-554`). With `LeaderElectionReleaseOnCancel: true` (`cmd/operator/main.go:162`), the old leader keeps renewing through the drain and releases only after the case is recorded. A new leader cannot start while the old one is still sending.
- **An involuntary lease loss cannot put two senders on one case, and `outcome == Sending` is enough.**
  - `OnStoppedLeading` sets `gracefulShutdownTimeout` to 0 and returns an error (`internal.go:627-637`). The manager stops at once and the process exits, which kills the send.
  - The defaults give a renew deadline of 10 s against a lease duration of 15 s (`:54-55`). So the old leader gives up about 5 s before a new one can acquire the lease, barring clock skew.
  - If they overlap anyway, the new leader never re-sends a claimed case (Q11(a)). The old leader's first record attempt carries the claim's resourceVersion, so it conflicts with any later write. Its re-read then finds `Interrupted` and drops.
  - A later run for a new suite digest has a different `suiteDigest` and nonce, so a very late record cannot land on it either.
  - An old leader whose lease lapses during a graceful drain is the same case: its shutdown context was fixed before the field was zeroed, and the same condition drops its record.
- **The wait rule forms no cycle with `Create`.** `Create`'s probe is anonymous on a prepared route with no `backendRefs`, so its `401` needs no card and no promotion. The cycle in R1 is the `Lock`'s alone.
- **The refused-`Adopt` exclusion is safe.** After its first pass the record writes nothing that an eval patch could conflict with, and the route follows promotion as for an ungated Agent (`authtxn.go:2033-2060`).
- **Q12 is a real question and belongs to the human.** A transaction past its deadline stays in its stage and keeps working (`authtxn.go:1354-1370`, `:1833-1840`; design 03 §3.3.3). Choosing (b) costs a `Lock` its passes and can clear `beforeObserved`. Choosing (a) is fail-closed for the candidate. The recommendation is defensible once R1's case is carved out or stated.
- **The twelve questions are complete for the slice**, apart from R1's choice. Each question carries its costs. Q5's no-suite rule, Q11 and Q12 are argued from the code.
- **Rule 7 and mutations.** Every owed row carries a mutation that kills it: the four new rows in `:403-413` and the rebuilt `:419` and `:421`. The exceptions are r1's missing second Agent, r2's unpinned manager bound and r4's two rows.

## The smallest set of changes to approvable

1. **R1.** Either exempt a `Lock` whose only unmet condition is an unattributable `401` for want of a recorded serving card, or keep the wait and state the cycle and its three exits at `:95`, `:221` and in `EvalWaitingForAuth`'s message. Correct Q12(a)'s cost sentence either way. Add the missing-policy `Lock` envtest, with its mutation.
2. **r1–r4.** Each is a sentence and a test row or a mutation, and can be folded into the same revision:
   - r1: check `ctx.Err()` before the claim, run the claim on the uncancelled context, state that the send is synchronous, and add a second Agent to the shutdown test;
   - r2: pin `GracefulShutdownTimeout`;
   - r3: define the pacing after a failed read-back;
   - r4: build the handover row by a direct write, and assert the refused `Adopt`'s route.
