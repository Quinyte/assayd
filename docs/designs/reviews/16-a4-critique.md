# Critique: design 16 A4 — the first slice (§1.1), revised

- **Date**: 2026-09-14
- **Target**: `docs/designs/16-evalsuite.md` §1.1 and amendment A4, at `875c42f` (branch `design-16-first-slice`). A4 answers `reviews/16-a3-critique.md` (REVISE: 0 BLOCKER, 8 MAJOR, 9 MINOR).
- **Independence**: an independent session that wrote neither the draft, A4, nor the first critique.
- **Read against**: ADR-0024, ADR-0030; design 02's rollout and status; design 03 §3.3.3; and the code: `internal/controller/agent_controller.go` (Reconcile, the rollout switch, `assessGates`, `writeStatus`, `SetupWithManager`), `conditions.go`, `authtxn.go` (`persistStatus` and its call sites), `authprobe.go`, `release.go`, `api/v1alpha1/agent_types.go`, and `cmd/operator/main.go`. The CEL cost estimator was read in cel-go v0.32.0 (`checker/cost.go`).
- **Method**: every claim the design makes about code was traced to its source. Two claims were **measured**, on a throwaway envtest program that is not committed, against the repo's pinned envtest (`ENVTEST_K8S ?= 1.36.x`, here v1.36.2):
  - the echo-refusal CEL rule, at the design's bounds and at smaller ones;
  - Q6(c)'s `authorizer` check, as a ValidatingAdmissionPolicy with impersonated users.

  Everything else was read, not run. Where a finding rests on reading, it says so.

**Verdict: REVISE — 0 BLOCKER, 6 MAJOR, 7 MINOR.**

A4 answers the first critique faithfully. Every finding has a visible answer, and most answers are right. What fails the standard is new, and most of it comes from A4's main change, the second writer:

- **N1, N2.** The eval controller's send is not protected by the optimistic lock that protects its write. So cases are re-sent routinely, and on a stale CRD they are re-sent without end.
- **N3.** Two of the new reasons are observed by a controller that is not allowed to report them.
- **N4.** "Never delays a `Lock`" is false for the Agent under evaluation.

Two further findings are outside the second writer. Q5's eligibility rule refuses rollbacks it means to admit (N5). The echo rule cannot be installed at the bounds the design gives it: measured (N6).

## Closure of the first critique

| Finding | A4's answer | Closed? |
|---|---|---|
| M1 finality | `status.eval.failed[]`, capped at 10; `EvalPairAlreadyFailed`; Q3 and the D2 bullet restated; A→B→A→B and cap envtests (`:119`, `:266`, `:285-290`, `:339-340`) | **Closed.** The residual (eviction, and an edit of the suite) is stated honestly |
| M2 one worker | Q1(a′), a separate eval controller with two workers, now recommended; (a)'s costs stated; starvation envtest (`:90-99`, `:277-281`, `:353`) | **Closed for cross-tenant starvation.** The same-Agent interaction that (a′) creates is unaddressed (N4), and the per-tenant bound is overstated (n3) |
| M3 path | `endpointPath` chosen once per run from a card whose digest matches the recorded one; the join specified; owed to design 02 (`:104`, `:154`, `:244`) | **Closed for the path.** The run-start refusals it introduces cannot reach `GatesPassed` (N3) |
| M4 redirects | `CheckRedirect` → `ErrUseLastResponse`, `3xx` is `ProtocolError`, as `authprobe.go:77-78` already does; the card fetch's hole owed to design 02 §3.4 (`:106`, `:330`) | **Closed.** The run-start card fetch is new code in the slice, and which client it uses is unstated (n4) |
| M5 gateway | Rewritten: the candidate's egress crosses the gateway; the cost is carried into Q3 (`:192`, `:290`) | **Closed** |
| M6 card | Fourth promotion condition; Q10; owed to design 02; card-cleared envtest (`:130`, `:134`, `:247`, `:338`) | **Closed** |
| M7 pins, removed gates | Q5(a) refuses the candidate's digest, `failed[]` digests and `supersededCandidates`; Q9 states and decides the removal bypass (`:140-143`, `:294-297`, `:310-313`) | **Partly.** The removal bypass is closed. The pin rule refuses served revisions, and whether it applies to ungated Agents is unstated (N5) |
| M8 invocation grant | Stated under "what it grants"; Q6 reframed with (c) recommended; design 07 A5.4's amendment owed (`:204-205`, `:254`, `:298-302`) | **Closed.** Q6(c) is now measured on 1.36 by this review. It has no owed test (n1) |
| m1 paused run | `EvalPaused`, no deadline, the departure from §9 stated (`:120`) | **Closed** |
| m2 pacing | No `Requeue: true`; the patch drives the next pass; a satisfied gate falls through (`:121`, `:132`) | **Closed.** That patch-driven pacing is what makes N1's race systematic |
| m3 reason placement | `assessGates` keeps three reasons; every other is set in the switch, and the table names the branch (`:161-181`) | **Closed**, except the two reasons only the eval controller observes (N3), and one missing row (n2) |
| m4 CRD after start | Uncached reads, no informer, a 5-minute requeue while a verdict is final (`:97`, `:349-350`) | **Closed.** Only `get` is then needed, and RBAC over-grants (n6) |
| m5 Q7 costs | Corrected for `helm upgrade` (`:303-307`) | **Closed.** The same `crds/` problem applies to the *Agent* CRD's new status fields, and A4 does not carry it there (N2) |
| m6 weak tests | Collision-pair state specified; "never re-sent after its outcome is recorded" (`:336`, `:335`) | **Partly.** The collision-pair state must also neutralize the new eval controller (n5) |
| m7 missing tests | All eleven added, each with a mutation (`:329-358`) | **Closed** |
| m8 Status line | Quotes ADR-0030; the approval must amend ADR-0024 (`:3`, `:14`) | **Closed** |
| m9 echo, detection | CEL echo rule; eval traffic stated to be distinguishable (`:78`, `:203`) | **Closed in intent. The rule as specified cannot be installed** (N6) |

## New findings

### N1 — MAJOR: the send is outside the lock, so a routine race re-sends cases, and "a case whose outcome was recorded is never sent again" is unenforced

`16-evalsuite.md:94`, `:121`, `:335`.

The design says the eval controller writes "under an optimistic lock (`client.MergeFromWithOptimisticLock`), so it never acts on a status it did not read". The lock guards the **write**. The **action** is the send, and the send comes first. There are two consequences.

**1. A lost race discards an outcome for a case that was already sent, and this happens on almost every case.** The pacing at `:121` is driven by the eval controller's own status patch. That patch enqueues the Agent in **both** controllers. On each such event, the Agent reconciler changes status, because `GatesPassed=False/EvalRunning` names "k of N" and k has just moved. So `writeStatus` is not a no-op, and it issues a full `Status().Update` (`agent_controller.go:1782-1791`). Here is the sequence:

1. The eval patch for case k lands at resourceVersion n.
2. The eval controller reads n and sends case k+1. The send blocks for up to `caseTimeoutSeconds`.
3. The Agent reconciler writes "k of N" at n+1.
4. The eval controller's patch for case k+1 carries n and fails with `Conflict`. The outcome is dropped.
5. The next pass sees case k+1 unrecorded and sends it again.

`:121` admits a re-send only after "a crash between sending a case and recording its outcome". In fact the normal interleaving of the two writers the design introduces produces one. Every duplicate is a real A2A task, with real tool calls and LLM spend (`:202`).

**2. Cache lag re-sends a case whose outcome *is* recorded.** Agents are read through the manager's cache (`cmd/operator/main.go:151-153`: only ConfigMaps, Secrets and Namespaces are uncached). A pass that starts before the cache reflects the eval controller's own last patch sees case k as unrecorded and sends it. This can be a dirty re-queue from an event enqueued during the previous pass, or the `RequeueAfter: 5s` backstop. The lock then refuses the write, but only after the send. So the owed row "a case is never re-sent after its outcome is recorded" (`:335`) is a guarantee the mechanism does not provide. An envtest will rarely catch cache lag, so that row can pass while the property is false.

**Fix.** Protect the send, not only the write:

- **Claim before sending.** Write `status.eval.cases[k] = {id, outcome: Sending}`, or an `inFlight` field, by a merge patch under the optimistic lock, *before* the request. A successful locked write proves the pass read the latest status. A lost race then costs a retry, and no request is sent twice.
- **Keep the outcome on conflict.** Record it with a re-read and retry on `Conflict`, re-applying it only while the claim still stands (same `revisionDigest`, `suiteDigest` and claim). The eval controller is the only writer of `status.eval`, so a newer status written by the Agent reconciler never invalidates the outcome.
- **State what a crash after the claim does.** Either re-send the case (at least once), or fail it as `Unreachable` (at most once). This is the human's choice, and it belongs in Q3.

Design 03's `persistStatus` is this repo's precedent for writing ahead of the act (`authtxn.go:1008-1030`).

**Tests.**

- Unit: an Agent reader that returns a status without case k's outcome after it was recorded. The stub sees case k once. Mutation: claim nothing and trust the read.
- Envtest: the Agent reconciler writing on every eval patch, which is the design's own `EvalRunning` message. Every case is sent exactly once, and the test asserts the stub's hit count. Mutation: drop the claim.

### N2 — MAJOR: an Agent CRD that predates the new `status.eval` fields prunes them, and the eval controller then re-sends the first case every 5 seconds, forever

`16-evalsuite.md:121`, `:147-157`, `:243-244`, `:303-307`.

`suiteDigest`, `endpointPath`, `verdict`, `cases[]` and `failed[]` are new fields of the **Agent** CRD's status. The chart ships that CRD under `crds/`, and `helm upgrade` never updates it (`api/v1alpha1/agent_types.go` on `Auth`; AGENTS.md). Q7 treats this only for the *EvalSuite* CRD. Now take an upgraded install whose administrator has applied the EvalSuite CRD by hand, as Q7(a) itself tells them to, but not the new Agent CRD:

1. Every `cases[]` patch is pruned without an error.
2. That write changes nothing, so no watch event follows.
3. The `RequeueAfter: 5s` backstop brings the controller back. It sees case 1 unrecorded and sends it again.

Case 1 goes to a real agent every 5 seconds, with real tool calls and spend, for as long as the candidate exists. Promotion never comes, which is fail-closed. The loop itself is invisible: no condition names it, because `verdict` is pruned too.

Design 03 hit exactly this and built the guard: `persistStatus` checks that `status.auth` came back, and otherwise stops before the write it was meant to authorize, reason `AuthRecordNotKept` (`authtxn.go:1008-1030`). The slice inherits the hazard without the guard.

**Fix.**

- Before the first case of a run, the eval controller confirms that its run-start write (`verdict: Running`, `endpointPath`, `suiteDigest`) came back. If it did not, it sends nothing.
- Name the condition, for example `GatesPassed=False/EvalRecordNotKept`, with the `kubectl apply` remedy that design 03's message gives. It reaches `GatesPassed` through N3's channel, or the Agent reconciler detects the stale schema itself.
- Add "the Agent CRD's new status fields are applied on upgrade" to the owed design 07 items beside Q7.

**Test.** Envtest with an Agent CRD lacking the new fields. The stub sees zero cases, and the Agent names the cause. Mutation: drop the read-back.

### N3 — MAJOR: two `GatesPassed` reasons are observed only by the eval controller, which may not write conditions, and `status.eval` has no field to carry them

`16-evalsuite.md:94-96`, `:104`, `:161`, `:173-174`.

The split says the eval controller writes `status.eval` and nothing else, and the Agent reconciler "sets every `GatesPassed` reason". But two of the table's reasons are decided **inside the eval controller's run-start fetch**:

- `EvalBindingUnsupported`: no `HTTP+JSON` 1.0 entry, a URL with a query or a fragment, or one that does not parse;
- `EvalAwaitingCard`'s second half: "the card fetched at run start does not match" the recorded digest.

`status.cards[]` records no interfaces (`CardStatus`, `agent_types.go:606-629`), which is why `endpointPath` exists. So the Agent reconciler cannot derive either reason from anything it reads. And `status.eval` gains no field for "no run could start, and why". An implementer has three choices, and each breaks something:

- Let the eval controller write a condition. That breaks the single ownership of an owned, sticky type (`conditions.go`).
- Re-fetch the card in the Agent reconciler. That is the extra blocking work M2 removed, and it may get a different answer.
- Leave the Agent at `EvalRunning` or silent. That is loud-and-wrong or silent, against rule 8.

**Fix.** Add a field the eval controller writes and the Agent reconciler reads, for example `status.eval.blocked: {reason, message}`, cleared when a run starts. The table's two reasons then read from it, and so does N2's `EvalRecordNotKept`. List it among design 02's owed fields. The existing "card with no usable interface reads `EvalBindingUnsupported`" envtest (`:347`) then pins it. Mutation: the Agent reconciler ignores the field.

### N4 — MAJOR: "it never delays an Agent reconcile, a `Create` or a `Lock`" is false for the Agent being evaluated

`16-evalsuite.md:90`, `:95`, `:191`, `:353`, `:356`.

The claim holds across Agents. It does not hold within one. Each eval patch bumps the Agent's resourceVersion. The Agent reconciler writes status with a full `Update` at the resourceVersion it read. It does so at the end of each pass (`writeStatus`), and through `persistStatus`, which writes `status.auth` ahead of each gateway write at some twenty sites in `authtxn.go`. So a Lock pass that overlaps an eval patch fails with `Conflict`. The design reads that as a feature: "the pass re-runs from a fresh read" (`:95`). It is no clobber, and that part is right. But:

- Each conflict costs the Lock a pass.
- An observation made after a probe (5 s) and then lost to a conflict must be probed again.

The design's own composition puts the two on the same Agent at once. J2's edit mints a revision, and on a gated Agent "the `Lock` does not wait for the verdict, and the verdict does not wait for the `Lock`" (`:191`). A run can patch every few seconds for up to 20 × 10 s = 200 s of the `Lock`'s 240 s `AuthTransactionDeadline`. Whether the `Lock` misses its deadline depends on case latency against probe latency. That is not measured, and the `Lock` failing is exactly what M2 was about (`AuthLockUnverified`, which pages). The starvation envtest (`:353`) uses *other* Agents' stubs. The J2-on-gated envtest (`:356`) states no stub latency, so a stub that answers at once makes it pass whatever the interaction.

This is read, not run.

**Fix.** Choose one and state it:

- **(i)** The eval controller sends no case while `status.auth.transaction` is in flight on that Agent. The eval waits for at most one transaction deadline. The `Lock` never competes with it.
- **(ii)** Keep concurrency, narrow the claim to "never delays another Agent", and state the same-Agent cost.

Either way, give `:356` a stub that answers a case every second or so across the whole `Lock`, and assert that `Served` is reached inside the deadline. Mutation for (i): drop the transaction check.

### N5 — MAJOR: Q5(a)'s eligibility rule refuses rollbacks to revisions that served, and does not say whether it applies to ungated Agents

`16-evalsuite.md:143`, `:181`, `:219`, `:294-297`, `:351`.

Q5(a) refuses a pin whose digest is the current candidate, is in `failed[]`, or is in `supersededCandidates`. Its purpose is to refuse revisions that never served, and its own test row asserts that "a pin to a retained, served revision promotes" (`:351`). But neither record means "never served":

- **`failed[]` records a failed *pair*, not a revision that never served.** Revision B fails suite S1 and is recorded. The owner edits the suite, B passes S2, and B is promoted and serves. Then C serves. An incident follows, and the pin to B, a known-good served revision, is refused as `PinnedRevisionNotEligible`. Nothing removes B from `failed[]` on promotion, and removing it would re-open the (B, S1) pair that M1 closed.
- **`supersededCandidates` records any candidate abandoned in flight, including one that served earlier.** B serves, then A′ serves. The spec returns to B, which mints B's digest as a candidate again, and it is superseded before its verdict. The promotion branch removes a digest from the list only when it promotes (`agent_controller.go:790`). So a pin to B is refused.

The design states the residual only in the admitting direction, a revision "evicted from both … can be pinned even though it never served". It does not state the refusing direction, which blocks ADR-0019's instant rollback, the reason Q5 gives for (a).

Separately, **the scope is unstated**. `supersededCandidates` exists on every Agent. If the rule applies whether or not a gate is declared, the slice changes pinning for every ungated Agent too, which is outside a gate slice. If it applies only when a gate is declared, then "remove `spec.gates`, then pin" serves the failed candidate. That is Q9's bypass reached through a pin, and it should be said.

**Fix.**

- State that the rule applies only while a gate is declared, and that removing the gate lifts it.
- State the false refusals as a cost of (a), with the break-glass being to remove `spec.gates` and then pin.
- Or narrow the refusal to the current candidate and `failed[]` digests whose revision has never been `activeRevisionDigest`. That needs a record of served digests (a bounded `status.eval.promoted[]`, or (c)'s `status.revisions[]`), and Q5 should offer it.

**Test.** B fails S1, passes S2 and serves, and C serves. The pin to B promotes (or is refused, if the human accepts that cost). Mutation: key eligibility on `failed[]` alone.

### N6 — MAJOR: the echo-refusal rule cannot be installed at the design's bounds, and as written it refuses every case (measured)

`16-evalsuite.md:77-78`.

The table bounds `cases[]` to 20 items, `message` to 4096 characters, and each of `contains` and `equals` to 4096. It then puts `!self.message.contains(self.expect.contains)` and `self.message != self.expect.equals` on each case.

cel-go estimates `contains` as the product of the two strings' sizes (`checker/cost.go:786-790`). The probe measured three things on envtest v1.36.2:

- **At the design's bounds, the CRD itself is refused**: `estimated rule cost exceeds budget by factor of 5.4x`. That happens both as written and with `has()` guards.
- **Smaller bounds install.** With the guarded form, message 1024 / expectation 1024 installs, and so does message 4096 / expectation 512, 256 or 128.
- **Without `has()`, the rule as written refuses every well-formed case.** A `contains` case fails with `no such key: equals`, and an `equals` case with `no such key: contains`. With the guards, `contains-ok` and `equals-ok` are admitted and both echoes are refused.

An implementer must choose which bound to cut, and that changes what a suite can express.

**Fix.**

- Write the rule with its guards: `(!has(self.expect.contains) || !self.message.contains(self.expect.contains)) && (!has(self.expect.equals) || self.message != self.expect.equals)`.
- Choose bounds that install. For example, cap each expectation at 512 and keep the message at 4096. That was measured to install.
- The echo-refusal test (`:333`) is already owed. Add "the generated CRD installs on the envtest API server", which envtest does anyway, as the test that pins the bounds. Mutation: raise either bound back to 4096.

### n1 — MINOR: Q6(c) is implementable; the design's "unmeasured" is honest, now partly measured, and the recommended control has no owed test

`16-evalsuite.md:301-302`, `:255`.

This review measured Q6(c) on envtest v1.36.2. The policy was a ValidatingAdmissionPolicy with `authorizer.group('assayd.dev').resource('agents').namespace(request.namespace).check('update').allowed()`, `failurePolicy: Fail`, and a `Deny` binding. With an impersonated user:

| The user may | Result |
|---|---|
| create suites only | denied |
| `update` `agents` only under `resourceNames: [x]` | denied |
| `update` `agents` namespace-wide | admitted |

The chart's floor (`kubeVersion: ">=1.30.0-0"`) and the e2e's k3s v1.33.6 remain unmeasured. The `authorizer` variable is in VAP from 1.28, and VAP is GA in 1.30, so both should hold, but only a run shows it.

**Fix.**

- State the `resourceNames` result. An Agent editor scoped to named Agents cannot write suites, which errs toward refusal.
- Add an envtest row with impersonation, as `test/envtest/admission_reservation_test.go` already does, covering the three rows above. Mutation: drop the policy binding.

### n2 — MINOR: no `GatesPassed` row for a gate declared over a revision that was never evaluated

`16-evalsuite.md:161`, `:178`, `:183`.

A gate added to a serving Agent mints nothing, and the guard sends it to the promotion branch. `assessGates` no longer sets `GateControllerUnimplemented`. So the table's "`True/EvalPassed` … in the promotion branch" must be conditioned on `status.eval` naming `activeRevisionDigest` with `Passed`. Asserted merely because the promotion branch ran, it states a pass that never happened. Also say what `GatesPassed` reads in that state, since the type is sticky, and what it reads after Q9's removal promotes a failed candidate (a sticky `False/EvalFailed` beside the revision now serving).

### n3 — MINOR: the cross-tenant bound holds for one slow tenant, not for k

`16-evalsuite.md:90`.

"A slow tenant delays only other tenants' evals, by at most two cases' `caseTimeoutSeconds` at a time" is true for one. With k tenants whose stubs sleep, two workers give a benign case a wait of roughly k/2 × `caseTimeoutSeconds`. Since a run has no deadline, a 20-case run can take 20 × k/2 × 10 s. That is still no Agent reconcile delayed, but state that the bound is O(k).

### n4 — MINOR: which client the run-start card fetch uses, and where the path came from

`16-evalsuite.md:104`, `:106`.

The eval controller's card fetch at run start is new code in the slice. Say whether it uses the no-redirect client or reuses `fetchOnce`, with the `GET` redirect hole the design records. Consider also recording the card digest the path was taken from beside `endpointPath`. The fourth condition accepts any recorded digest, so after a drift re-read the promoted card need not be the one the run's path came from.

### n5 — MINOR: the collision-pair envtest must also keep the eval controller from acting

`16-evalsuite.md:336`.

The row seeds `status.eval` for S's digest while M is the candidate. The eval controller sees a candidate with no run for (M, suite), so it starts one and overwrites `status.eval`. Then either the stub passes M legitimately, and the test fails without its mutation, or it fails M, and the test passes without testing the name comparison. Say that the eval controller is not started, or that its A2A client refuses every request, in that test.

### n6 — MINOR: RBAC over-grants `list` and `watch` on `evalsuites`

`16-evalsuite.md:257`.

With suites read uncached by `get` alone, and no informer (`:97`), nothing needs `list` or `watch`. Rule 5 applies: grant `get`, or say what will list or watch.

### n7 — MINOR: the eval controller's predicate trusts `status.candidateRevisionDigest`, which lags the spec

`16-evalsuite.md:96`.

After a spec edit, until the Agent reconciler's next pass records the new candidate, the eval controller keeps sending the old candidate's cases. Condition 2 keeps any of that from promoting anything, but the sends are real. State it, or have the shared predicate also compare against the desired digest.

## Verified sound

- **No clobber between the two writers.**
  - The Agent reconciler builds status from `agent.Status.DeepCopy()` (`agent_controller.go`, Reconcile). No code path constructs a fresh `AgentStatus`: the only assignment is `writeStatus`'s `agent.Status = *status`. So `status.eval` is carried as read.
  - The write is a full `Status().Update` at the read resourceVersion, so a racing eval patch makes it `Conflict` rather than write back a stale `status.eval`.
  - A merge patch computed from a base in which only `status.eval` changed carries only `status.eval` and the resourceVersion. So the eval controller cannot touch `status.auth`, `cards[]` or conditions.
  - A JSON merge patch replaces `cases[]` and `failed[]` whole, which is safe under the lock.
  - The eval controller writes no condition. `GatesPassed` stays owned and sticky, with one author (`conditions.go`).
- **The four promotion conditions are read consistently.**
  - Verdict, revision digest and suite digest come from one object, written by one patch, so they agree with each other.
  - The suite is compared against a fresh uncached read, which errs toward holding.
  - The card digest is the Agent reconciler's own field, updated earlier in the same pass.
  - One caveat that is not a finding, because every pass already behaves this way: `reconcileGateway` moves the route before the end-of-pass status write. So a promoting pass that then conflicts has moved the route without recording it, and the next pass reconverges. The verdict it acted on was earned by that same pair.
- **The single-worker premise.** `SetupWithManager` sets no `MaxConcurrentReconciles`, so controller-runtime's default of one applies. And `For(&Agent{})` has no predicate, so a status write enqueues the next pass (`agent_controller.go:1825-1828`).
- **RBAC for the patch.** `agents/status` already carries `get;update;patch` (`agent_controller.go:214`).
- **`authprobe.go` refuses redirects** as `:106` says (`authprobe.go:77-78`).
- **Q6(c)** is implementable at the repo's envtest version (n1).
- **Q3, Q9 and Q10** are argued well, with their costs stated. Q9(a) is honest that a gate is not a control against the Agent's own editor.
- **The "cannot keep" corrections to D2 and to the `Lock` composition** now say exactly what the mechanism does.

## The smallest set of changes to approvable

1. **N1.** Claim a case under the optimistic lock before sending it, and record the outcome with retry-on-conflict. State what a crash after the claim does, under Q3. Add the stale-reader unit test and the hit-count envtest.
2. **N2.** Read back the run-start write before the first case, and name the stale-CRD condition. Owe the Agent CRD's upgrade beside Q7.
3. **N3.** Add a `status.eval` field for why no run could start, and read `EvalBindingUnsupported`, the run-start card mismatch and N2's reason from it.
4. **N4.** Either the eval controller waits for an in-flight `-auth` transaction on the same Agent, or the claim is narrowed to other Agents. Give `:356` a stub with real latency.
5. **N5.** State Q5(a)'s scope (gated Agents only) and its false refusals of served revisions, with the break-glass. Or add a served-digest record, and offer it in Q5.
6. **N6.** Guard the echo rule with `has()`, and cut the bounds to ones that install (for example, expectations at most 512).
7. The MINORs are editorial or add a test, and can be folded into the same revision.
