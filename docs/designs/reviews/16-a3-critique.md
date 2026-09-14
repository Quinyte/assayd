# Critique: design 16 A3 — the first slice (§1.1), blue/green, one EvalSuite

- **Date**: 2026-09-14
- **Target**: `docs/designs/16-evalsuite.md` §1.1 and amendment A3, at `1af0efd` (branch `design-16-first-slice`), plus the Status-line change and its `docs/designs/README.md` row.
- **Independence**: an independent session that did not write the draft. Read against ADR-0006, ADR-0024 and ADR-0030; `docs/designs/reviews/16-review.md`; design 02 §3.3 and §3.4; design 03 §1.1 and §3.3.3; design 07 A5.4 and A6.6; and the code: `internal/controller/agent_controller.go`, `authtxn.go`, `card.go`, `release.go`, `conditions.go`, `api/v1alpha1/agent_types.go`. A2A 1.0's `SendMessage` default was checked against the `a2aproject/a2a` specification, and Helm's `crds/` handling against the Helm docs.
- **Method**: every claim the draft makes about code or another design was traced to its source. Nothing was mutated or run, because nothing in the slice is implemented. Where a finding rests on reading code rather than running it, it says so.

**Verdict: REVISE — 0 BLOCKER, 8 MAJOR, 9 MINOR.**

The slice has the right shape. Holding a failed candidate instead of deleting it, binding the verdict to both digests, keeping the `active != desired` guard, and ordering the I1 hold before the gate are all correct against the code. What fails the standard is one of two things in each finding: a guarantee that the mechanism does not enforce (M1, M5, M6, M7, M8), or a piece an implementer would have to invent (M2, M3, M4).

## Findings

### M1 — MAJOR: "a verdict is final for its pair" is unenforced; one status slot cannot remember a pair once the run is superseded

`16-evalsuite.md:105`, `:159`, `:222`, `:239-240`.

`status.eval` is one slot (`EvalStatus`, `api/v1alpha1/agent_types.go:633`). The draft says supersession drops the old run's outcomes (`:159`). Nothing records a failed pair once the next run begins. Here is how the same pair gets re-run:

1. Revision B fails its suite and is held.
2. The owner reverts the spec to A. A is still active, so B is superseded and its outcomes are dropped.
3. The owner re-applies B. Revision B, the same suite digest, and a fresh run. No suite change and no new revision were needed.

The same thing happens by way of any C. And the draft already says a suite "touch", such as `caseTimeoutSeconds: 10 → 9`, gives a new pair (`:240`).

So `:222`'s "The slice makes the claim true only by making a verdict final for its pair" is false, and Q3's argument overstates what (a) buys. ADR-0006's retry-until-pass remains possible. The only thing (a) removes is the *automatic* retry.

**Fix.** Choose one:

- Keep a bounded record of failed pairs, for example `status.eval.failed[]` holding `{revisionDigest, suiteDigest}` with a cap and eviction of the oldest. Refuse to start a run for a recorded pair.
- Restate the claim honestly: "a re-run always follows a visible edit of the spec or the suite; nothing prevents retry-until-pass by edits." Q3 should then say exactly that.

Either way, add an envtest: A → B (fails) → A → B. The stub must see no case for the second B (under the first option), or the doc must say that it will (under the second).

### M2 — MAJOR: the eval runs on the operator's only reconcile worker; any tenant can trigger that, and it collides with design 03's transaction deadline

`16-evalsuite.md:104`, `:233`.

The draft states the per-case bound correctly. It does not state what the bound does to everyone else.

- **One pass holds the worker for more than one call.** A single pass can run the card fetch (5 s, `DefaultCardFetchTimeout`), one case (10 s), and an `-auth` probe (5 s, design 03 §3.3.3).
- **Any tenant can occupy the worker.** Two ways:
  - An Agent author ships an image that sleeps on `POST /message:send`. Every revision that author mints then costs 20 × 10 s.
  - Anyone with EvalSuite write re-digests the suite against a held candidate. Q2 keeps that candidate indefinitely, so each edit starts another 200 s run.
- **The delay multiplies.** With *k* such Agents, every other Agent's pass waits about *k* × 10 s.
- **It breaks design 03's deadline.** A J2 `Lock` needs several passes inside `AuthTransactionDeadline = 4 * time.Minute` (`internal/controller/authtxn.go:46`). At *k* = 5 it can miss the deadline. That tenant's Agent then goes to `PolicyApplyIncomplete/AuthLockUnverified`, with `Ready=False` and phase `Degraded`, and pages for another tenant's slow agent.
- **It contradicts an existing rule.** `card.go` already put this in writing: "the work queue is shared, so blocking here would let one unreachable agent delay every other agent's reconcile" (`agent_controller.go:620-622`).

Q1's list of costs names only "a case occupies the one reconcile worker for up to 10 s".

**Fix.** Add option (a′) to Q1: a separate eval controller in the same manager. It has its own queue and its own bounded workers, and it writes only `status.eval`, by a merge patch. The Agent reconciler only reads the verdict. That is still no new pod, no new image and no new RBAC. If (a) stays the recommendation, Q1 must state the cross-tenant starvation and the 4-minute deadline interaction, and the slice must bound eval concurrency globally.

Test to add: a J2 `Lock` reaches `Served` inside its deadline while two other Agents' stubs each sleep for `caseTimeoutSeconds`. Mutation: run the cases on the Agent worker.

### M3 — MAJOR: the case path comes from a card body that status does not keep

`16-evalsuite.md:90-91`.

The run needs the first `supportedInterfaces[]` entry with `HTTP+JSON` and `1.0`. `status.cards[]` records `name`, `version`, `digest`, `fetchedAt` and `sharedTaskState`, and no interfaces (`CardStatus`, `api/v1alpha1/agent_types.go:606-629`). An implementer has to guess between three options, and each breaks something:

- Re-fetch the card on every case or every run. The card may now digest differently from the recorded one. Which one wins?
- Keep it in memory. That is lost on a restart, which contradicts the resume rule at `:104`.
- Add a status field. The draft does not list one among design 02's owed fields (`:205`).

The join is also unspecified. "Path … followed by `message:send`" does not say what happens for a path of `/`, a trailing slash, a query or a fragment.

**Fix.**

- At run start, record the chosen path in `status.eval`, for example `endpointPath`. Take it from a fetch whose digest equals the recorded one, and use it for every case of that run.
- Specify the join: take only `url.Parse(...).Path`, trim a trailing `/`, and append `/message:send`. Refuse a query or a fragment as `EvalBindingUnsupported`.
- Add the field to design 02's owed list.

### M4 — MAJOR: redirects are followed, so "never to an address the agent chose" is unenforced

`16-evalsuite.md:91`, `:169`.

The operator's HTTP client today is `&http.Client{}` with no `CheckRedirect` (`internal/controller/card.go:258-259`). Go follows up to 10 redirects by default, and on `307`/`308` it re-sends a `POST` body. A candidate that answers `307 Location: http://<anything>` makes the operator re-POST the suite's message from the operator's network position. That position includes the API server, other tenants' Services and any link-local metadata endpoint. Nothing restricts the operator's egress (design 07 A5.4 governs agent Pods, not the operator). The outcome then records `ProtocolError` or `Mismatch`, which gives the candidate a one-bit oracle about the target.

The same hole exists in the card fetch today, for `GET`: a redirected document's `name` and `version` land in `status.cards[]`. That is outside the slice, but it should be recorded against design 02 §3.4.

**Fix.** The eval client refuses redirects: `CheckRedirect` returns `http.ErrUseLastResponse`, and any `3xx` is `ProtocolError`. Add a row to the outcome table. Unit test: a stub answering `307` to a second stub that counts hits, which must see zero. Mutation: the default client.

### M5 — MAJOR: "a Gateway-level policy changes no case's outcome" is false, and under Q3(a) such a change fails the revision for good

`16-evalsuite.md:157`, against `:167`.

Eval *ingress* does not cross the gateway. The candidate's *egress* does, as `:167` itself says: tool calls and LLM spend go through `ASSAYD_GATEWAY_URL`. So any case whose answer depends on a tool is subject to whatever the Gateway enforces while the run is in flight. That includes a Gateway-level authentication policy on the MCP listener, which is exactly A75's case, since the fixture's tool call is unauthenticated (AGENTS.md). A gateway outage counts too.

Neither digest covers that state, and under Q3(a) the resulting `Failed` verdict is final.

Is this what a gate should prove? Evaluating a candidate under the real egress policy is defensible. But the verdict is then a function of cluster state outside its binding, and the draft must say so.

**Fix.** Rewrite `:157` to say that eval requests do not cross the gateway, and that the candidate's own egress does, so a Gateway-level policy or a gateway fault can fail a case. Carry that cost into Q3 next to "a network blip".

### M6 — MAJOR: "the card precondition guarantees that digest is recorded for every revision the gate promotes" is false

`16-evalsuite.md:156`, `:90`, `:111-113`.

The precondition is checked when the run *starts*. The desired revision's card is re-read every drift interval, and a failed re-read *clears* the digest: `recordCardAttempt` sets `c.Digest = ""` (`internal/controller/card.go:385-392`). A 20-case run, or a run slowed by M2, can span a drift re-read. The three promotion conditions do not re-check the card, so the gate can promote a revision with no recorded digest.

This fails closed for the `Lock`: an unattributable `401` is refused, and the `Lock` goes to `AuthLockUnverified`. But the guarantee as written is false.

Separately, the precondition is **new enforcement** that the draft does not name. Today a card failure "never withholds traffic" (`agent_controller.go:600-606`, design 02 §3.4). Under the slice, a gated Agent whose card will not validate is held forever (`EvalAwaitingCard`).

**Fix.**

- Add a fourth promotion condition: `status.cards[]` records a digest for `desiredDigest` in this pass.
- List "a gated candidate needs a valid card to promote" among design 02's owed changes.
- Test: clear the candidate's card digest after its last case, and the candidate must not promote. Mutation: drop the fourth condition.

### M7 — MAJOR: Q5(a)'s premise is false; a pin can serve a gate-*failed* candidate, labelled as a rollback

`16-evalsuite.md:160`, `:243-244`, `:147`.

`resolveReleasePin` selects **any** owned workload whose annotation carries the pinned digest (`internal/controller/release.go:75-101`). Nothing checks that the revision has ever served. The comment "it selects a revision that already served" (`agent_controller.go:908-910`) is enforced nowhere.

Q2(a) retains the failed candidate, because `collectGarbage` protects `status.candidateRevision` and keeps the newest `DefaultRevisionHistoryLimit` others (`agent_controller.go:1552-1590`). So under Q5(a), setting `spec.release.targetRevisionDigest` to `status.candidateRevisionDigest` promotes the revision that just failed, with `GatesBypassed=True/PinnedRollback`. That reason names a rollback that is not one: loud and wrong, AGENTS.md rule 8.

There is also an easier bypass that the draft does not state. `gates` is on the policy surface, so deleting `spec.gates` promotes the held candidate on the next pass, under `GatesSkipped=NoGatesDeclared` (`gatesSatisfied`, `agent_controller.go:1458-1463`). "What the verdict proves" (`:168`) names only the suite writer.

Confirmed by reading, not by running: today's code holds a pinned rollback under a declared gate with no exit (`:160`). The gate branch has no pin input, and `authHoldsPromotion` does.

**Fix.**

- Q5(a) bypasses only a pin to a digest that is neither the current candidate nor the digest of a recorded `Failed` verdict. Otherwise the pin is refused with its own reason. Or prefer (c), once `status.revisions[]` exists.
- State that anyone with `agents/update` can skip the gate by removing it.
- Test: a pin to a failed candidate's digest does not promote. Mutation: no check.

### M8 — MAJOR: EvalSuite write becomes unauthenticated invocation of a real agent, and the operator becomes a second way in

`16-evalsuite.md:169`, `:168`, `:245-246`, `:211-215`.

`:169` says the message "reaches that author's own agent". Nothing enforces that. The suite resolves in the Agent's namespace, and anyone with EvalSuite write there can do the following to a held candidate:

- make the operator send it up to 20 arbitrary messages per suite edit. The candidate is held indefinitely under Q2(a), so it is a standing target;
- skip the API key and the group check that design 03's `<agent>-auth` requires of every caller through the route;
- drive the candidate's real tools and LLM spend, with no budget (`:192`);
- read back one pass/fail bit per case, per run.

Q6 frames the risk as "can pass a revision by weakening its suite". The invocation path is the larger grant, and the human must see it before choosing Q6(a).

At the network layer, design 07 A5.4 admits the operator's Pods for the card fetch and says the eval runner "arrives through the gateway and needs no rule of its own". The slice falsifies that premise. A NetworkPolicy works at L4, so the card-fetch allow rule now carries arbitrary A2A tasks. The draft's design 07 dependency list (`:211-215`) does not include amending A5.4.

**Fix.**

- State the invocation grant in "What the verdict proves", and in Q6's costs for (a).
- Add the A5.4 amendment to the owed list: the operator's ingress now carries eval tasks.
- Consider a Q6 variant that sends no case unless the suite's last writer can also update the Agent. A `managedFields` manager check is weak; an admission rule is the real form.

### m1 — MINOR: a candidate that crashes mid-run pauses the run; it does not fail its cases

`16-evalsuite.md:103`, `:90`.

The `!ready` branches come before the gate branch (`agent_controller.go:676-732`), and the run starts only on an available candidate. So a candidate that crashloops mid-run never has another case sent. It sits under `CandidateNotAvailable`, with no `EvalFailed`, for ever. `:103`'s "§9 calls a candidate that crashes under eval 'the gate working', and the slice agrees" does not describe that behaviour.

**Fix.** State the rule. Either an unavailable candidate pauses the run, which is what the code shape implies, and the draft says so, or it fails the remaining cases as `Unreachable`.

### m2 — MINOR: "requeues at once" and "promotion in the same pass" need a mechanism

`16-evalsuite.md:104`, `:146`.

`Result{Requeue: true}` goes through `AddRateLimited`, whose per-item backoff grows exponentially and is never reset while the run continues. The status write already triggers the next pass, because `For(&Agent{})` has no generation predicate.

The `passed` row says promotion happens in the pass that records the last outcome. The gate branch must therefore compute the verdict and then fall through to the promote branch.

**Fix.** Say both.

### m3 — MINOR: `GatesPassed` is assessed before the facts its new reasons depend on

`agent_controller.go:369` against `:598-599`.

`assessGates` runs before the card fetch and before `authHold` and the verdict are known. So `EvalHeldForAuth`, `EvalAwaitingCard`, `EvalRunning` and the rest must be set inside the switch.

**Fix.** Name where they are set.

### m4 — MINOR: the EvalSuite watch versus a CRD installed after start

`16-evalsuite.md:182`.

Design 02 §3.3 requires a CRD installed after the operator starts to take effect without a restart. A static `Watches(&EvalSuite{})` fails manager start when the CRD is absent.

**Fix.** Specify a source started on discovery, or name the drift-interval requeue (5 min) as the bound on noticing a suite edit.

### m5 — MINOR: Q7's cost is wrong for upgrades

`16-evalsuite.md:247-248`.

Helm installs `crds/` entries only at install time. `helm upgrade` neither upgrades nor adds them, as the Helm docs and this repo's own "helm upgrade never updates `crds/`" both say. So under (a), an upgraded install keeps `GatesSkipped=EvalSuiteCRDAbsent` until an administrator applies the CRD. "Every install enforces a declared gate" holds only for fresh installs, and "every Agent that already declares a gate stops promoting ungated on upgrade" does not happen.

**Fix.** Correct the costs of (a) and (b).

### m6 — MINOR: two owed tests can pass with their subject deleted

`16-evalsuite.md:263-264`.

- **The collision-pair case.** `collisionAgainstStatus` refuses first whenever the colliding name is the active or candidate revision (`agent_controller.go:1041-1051`). So "compare the name" survives unless the test seeds `status.eval` for a digest that is neither. Say how the test builds that state.
- **"The stub sees each case exactly once".** The draft itself allows a re-send when status write loses a race, so the test flakes. Assert "never re-sent after its outcome is recorded" instead.

### m7 — MINOR: tests missing from the table

Each needs its mutation:

- a redirect (M4);
- a timeout bound: a stub sleeping past `caseTimeoutSeconds` yields `Unreachable` within the bound (mutation: no deadline);
- the 1 MiB cap;
- `EvalSuiteNotFound` and `EvalSuiteUnreadable`;
- the watch requeue;
- a pinned rollback (Q5), including a pin to a failed candidate (M7);
- the finality toggle (M1);
- a card cleared before promotion (M6);
- a `Lock` deadline under a sleeping stub (M2);
- a `Create` in flight when a gated revision promotes;
- the Gateway-level-hold row.

### m8 — MINOR: the Status line paraphrases ADR-0030, and ADR-0024 is left unflagged

`16-evalsuite.md:3`.

The change itself is correct. The README row said "awaiting user approval", and ADR-0030 makes the README the arbiter. Two things need fixing:

- "Under ADR-0030 a PASS is evidence about the prose, not an approval" is not ADR-0030's text. It says "'Approved' no longer means buildable — every design carries its own Status and `docs/designs/README.md` arbitrates". Quote that.
- ADR-0024, which is *accepted*, still carries "rerun-shopping structurally impossible" and the eval-Job SVID model. The slice contradicts both. The draft should name ADR-0024 as the ADR the approval must amend.

### m9 — MINOR: an echo passes `contains`, and a candidate can tell it is being evaluated

`16-evalsuite.md:39`, `:167`.

- **Echo.** The draft's own minimal suite, message `hello` and `contains: "hello"`, passes an echo agent, and `test/responder` is an echo. A CEL rule `!self.message.contains(self.expect.contains)` on each case would refuse it, or the draft can state the trap.
- **Detection.** Eval requests come from the operator's Pod with no credential, while production requests come through the gateway. So an image whose author intends to cheat can tell eval traffic from production. `:167`'s "Nothing marks eval traffic" should say the traffic is still distinguishable, and that the gate does not defend against a dishonest image author.

## Verified sound

- **A gated first revision gets no route until it is promoted.** `reconcileGateway` enters no transaction and collects routes while `status.activeRevision` is empty (`internal/controller/authtxn.go:736-745`). The route follows `activeRevision`, which is written after the rollout switch (`agent_controller.go:797-804`). So `Create` runs only after a pass.
- **A promotion while a `Create` is in flight is harmless.** `Create`'s probe is anonymous, against a prepared route with no `backendRefs`, so the `401` never depends on which revision serves. Publication uses `activeRevision` at the moment of publishing. The draft should add that sentence, not change a rule.
- **The I1 hold precedes the gate.** The I1 branch (`agent_controller.go:734`) comes before the gate branch (`:747`), and I1 never applies to a never-served Agent (`authHoldsPromotion`, `Mode == ""`). The table's "cannot arise" is right.
- **A promotion during a `Lock`.** A held candidate is on no route. The `Lock` works on the active revision's route, and a promotion clears `beforeObserved` and `beforeRevision` (`authtxn.go:1628-1636`, design 03 §3.3.3 at line 776). The reliance is correct, apart from M6's timing gap.
- **"Candidate deleted" loops, and holding is the sound alternative.** Level-triggered reconciliation re-creates a deleted candidate from the desired spec. `collectGarbage` protects the candidate, so the hold keeps it without a new rule.
- **Q5's reading of today's code is right.** `gatesSatisfied` has no pin input.
- **The digest binding, and the retained `active != desired` guard.** `gates` stays on the policy surface, so adding a gate or editing a suite never evaluates the serving revision.
- **The integer threshold rule and the decimal-string threshold.** Both are sound, and the controller-gen float refusal is real.
- **A2A.** With no `configuration`, `SendMessage` is blocking by default (`returnImmediately` defaults to false in the 1.0 specification), so a conforming agent returns a terminal or interrupted task. `POST /message:send` and `A2A-Version: 1.0` match `docs/research/a2a-v1.0-card-and-transport-2026-09.md` and `test/responder`.
- **Response bounds.** The 1 MiB body cap and the per-case timeout bound what a huge or slow answer can do to the operator's memory. The recorded outcome is `{id, outcome, reason}` and never the answer text, so a candidate cannot write arbitrary content into status or spoof another case.
- **The contradictions list.** Items 1, 3, 4, 5 and 6 are recorded honestly, and none blocks the slice. Item 2 overclaims its own fix (M1).
- **Q2, Q4 and Q8.** Each recommendation is well argued.

## The smallest set of changes to approvable

1. **M1.** Either record failed pairs and refuse to re-run them, or restate finality as "a re-run follows a visible edit". Correct `:222` and Q3 to match.
2. **M2.** Add Q1(a′), a separate eval controller in the same manager, or state and bound the cross-tenant starvation and the 4-minute `Lock` deadline interaction.
3. **M3.** Record the chosen endpoint path in `status.eval` at run start, specify the path join, and add the field to design 02's owed list.
4. **M4.** No redirects: a `3xx` is `ProtocolError`.
5. **M5.** Rewrite the Gateway-level sentence: the candidate's egress crosses the gateway. Carry the cost into Q3.
6. **M6.** Add "a card is recorded for `desiredDigest` in this pass" as a fourth promotion condition, and list the card-gated promotion as a design 02 change.
7. **M7.** Q5(a) must refuse a pin to the current candidate or to a recorded `Failed` digest. State that removing `spec.gates` bypasses the gate.
8. **M8.** State the unauthenticated-invocation grant under Q6, and add design 07 A5.4's amendment to the owed list.
9. Add the tests in m6 and m7, each with its mutation.

The MINORs other than m6 and m7 are editorial and can be folded into the same revision.
