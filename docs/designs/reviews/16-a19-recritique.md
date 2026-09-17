# Re-critique: design 16 A19, second draft — the A77/A78 follow-up to the approved first slice (§1.1)

- **Date**: 2026-09-16
- **Target**: `docs/designs/16-evalsuite.md` §1.1 and amendment A19, and the design 03 and design 16 rows of `docs/designs/README.md`, at `8ad9722` (branch `design-16-a77-followup`). Read as `git diff 8a9723f 8ad9722` (this round) and `git diff origin/main...8ad9722` (the whole amendment), and whole in the head.
- **Independence**: a second, independent session. It wrote none of A3–A19, none of the twelve earlier critiques of §1.1, none of `reviews/16-a19-critique.md`, and none of design 03 A77 or A78. Every behavioural claim below was traced by hand in the tree at `8ad9722`. Where the first critique and this one agree, the agreement was reached by re-reading the code, not by reading the first critique's finding.
- **Scope**: the eleven closures the second draft claims, plus anything the second draft introduces. Design 03, design 02 and ADR-0024 are not edited by A19 and are read only as authority. Nothing in this slice is implemented, so nothing was run; the design 03 behaviour A19 describes **is** implemented and was read against its envtest cases.
- **Read against**:
  - `internal/controller/authtxn.go`: `runLock` — the `reCreate` branch and its `recreateRoute` hand-off (`:1606-1617`), the `ProbingBefore` arm (`:1662-1680`), the `ApplyingPolicies` arm and `foreignAtOwnName` (`:1681-1692`), and the `StageConverging, StageProbingAfter` arm in order: intact read (`:1694`) → fresh NACK (`:1705`) → the `Converging` tuple (`:1716-1730`) → A75's Gateway read (`:1731-1739`) → the foreign check (`:1740-1750`) → **the re-point** (`:1751-1787`) → **the route gate** (`:1788-1826`) → probe (`:1827`) → `cardAttributes` (`:1839`); the `reCreate` message block (`:1898-1915`); `missingPolicyRouteNote` (`:1952-2073`), its early return at `:1971-1974` and its four `admits` arms (`:2044-2065`); `singleBackend` (`:2140`); `beforeRevisionFor` (`:2152`); `cardAttributes` (`:2187`); `cardedRevision` (`:2213`); `authPolicyPresent` (`:2226`); `reassertServedPolicy` (`:2243`); `authPolicyIntact` (`:2496`); `routeConverged` (`:2518`); `reconcileServed` (`:1498-1543`) and `recreateRoute` (`:1550`).
  - `internal/controller/card.go`: `cardEntry` (`:385`), `hasCardFor` (`:405`), `recordCardAttempt` (`:412`), `pruneCards` (`:431`).
  - `internal/controller/httproute.go`: `routePublished` (`:355`).
  - `internal/controller/authforeign.go`: `foreignTrafficPolicies` (`:37-68`).
  - `internal/controller/agent_controller.go`: the rollout switch (`:687-805`) and the `reconcileGateway` call after it (`:816`).
  - `test/envtest/authgate_test.go`: `mintRevision`, `backendOf`, `acceptedGen` (`:67-115`); case 18 (a) (`:124`), (b) `TestACandidatesPromotionEndsTheWedgeWithNobodyActing` (`:248-297`), (c) `TestNoBackendMovesWhileThePolicyIsNotThisAgents` (`:299-345`), (d) `TestTheRouteGateStopsThePassThatMovedTheRoute` (`:356-…`).
  - `docs/designs/03-policy-compiler.md` A77's §3.3.3 re-point bullet (`:789`) and A78; `docs/research/auth-lock-false-credit-2026-09.md`; `docs/decisions/0034-…` Amendment 6 (`:108`); `docs/decisions/0024-p3-semantic-admission.md` Amendment 1.
- **Method**: each of the eleven claimed closures checked against the code rather than against §14's account of it; the restored owed row built in the head pass by pass against `runLock`'s actual order, and each of its four mutations placed in the source and traced to the assertion said to kill it; the same for the rebuilt promotion row; the residue's two halves checked against what each controller reads; `runLock`'s `ProbingAfter` arm re-walked for every way a pass can end above the re-point.

**Verdict: FAIL — 0 BLOCKER, 2 MAJOR, 5 MINOR.**

The BLOCKER is properly closed, and closed at the root rather than at the five sentences. The ordering claim A19 now makes — the re-point sits after the intact read, the fresh-NACK check and the foreign check, so a carded `status.activeRevision` ends the apart state only once a pass **reaches** it — is exactly what `runLock` does, and the three ways A19 lists are the three ways the arm can end above `:1771`. MAJOR 3 and MAJOR 4 are closed correctly and against the code, and all seven minors are closed. What remains is one thing the restored row got wrong about its own state, and one false property in the framing that goes to the human.

---

## MAJOR 1 — the restored "apart" row asserts message behaviour that §1.1's own rule excludes in the state the row builds

`docs/designs/16-evalsuite.md:557` (the restored owed row), against `:182` ("When it names the exits") and `:311` (the normative `GatesPassed` cell that MAJOR 2 rewrote to match it).

The row's main build ends at step (6) with **C promoted and carded**, the route on an uncarded R, and the `Lock` held at `ApplyingPolicies` by a foreign `traffic` policy at `<agent>-auth`. It then asserts:

> `:557` "The next candidate D reads `EvalWaitingForAuth` naming R, and its revert names R, not C."

§1.1 says the message does neither of those things in that state. `:182`: the exits are named when "the recorded transaction is a missing-policy `Lock` **at `ProbingAfter`**; the route does not carry exactly one `backendRef`, or the revision its single `backendRef` names has no recorded card digest; **and `status.cards[]` records none for `status.activeRevision` either**". In the main build `status.activeRevision` is C and C is carded, so the third clause fails; and the `Lock` is at `ApplyingPolicies`, so the first fails too. `:311` now carries the same condition verbatim, and `:183` ties R's name to that clause. So D's message is the generic one — the transaction's kind and stage, plus clause 5's hold naming the foreign policy — and it names neither R nor a revert.

The shipped operator agrees, which is the check that settles it: `missingPolicyRouteNote` returns early at `internal/controller/authtxn.go:1971-1974` on `attributed || cardedRevision(status, status.ActiveRevision)`, and with C carded that second disjunct is true, so the note stops at "`<runNS>/<agent>-serving`'s single backendRef names revision R … and the route was left as found." No exits, no revert.

Two consequences, both inside the same row.

**Mutation 1 is not a mutation.** "Key the wedge's detection on `status.activeRevision`, which finds C's card and names no exit" describes, in this build, the output the spec already requires: the spec's conjunction is false here, so dropping the first conjunct changes nothing. The two readings diverge only where the route's revision **is** carded and `status.activeRevision` is not — a state this row does not build. **Mutation 2** ("name `status.activeRevision` in the revert") has nothing to bite on, because no revert is named.

**And `:182`'s justification is the inference the BLOCKER killed.** The clause reads "…and `status.cards[]` records none for `status.activeRevision` either, **since otherwise the re-point ends the state on a later pass and there are no exits to name**". That is the same "carded ⟹ the re-point ends it" step, and this row is the counterexample A19 restored one page earlier: no later pass reaches the re-point while the foreign policy stands. `:311` now carries the clause verbatim, so the refuted reason is in the normative table as well.

The rule's *output* is defensible — in every way the apart state can carry a carded `status.activeRevision` (a foreign `traffic` policy, a fresh NACK, a `Lock` short of `ProbingAfter`) the thing that actually stops it is a hold that the stage clause and `status.eval.blocked` already name, so naming the delete and the revert there would steer at the wrong remedy. But the reason written down is false, and the owed row was built on the false one.

**Fix.** Three parts, and they are one edit.
1. At `:557`, move the message assertions and mutations 1 and 2 to the **variant**, where C is uncarded and the `Lock` reaches `ProbingAfter`: there `cardAttributes` is false on R and `cardedRevision(C)` is false, so the exits are named, R is named, and the revert names R. That is the state `:182` describes, and the variant already builds it.
2. In the main build, assert what §1.1 requires: the generic `EvalWaitingForAuth`, with clause 5's hold naming the foreign policy, and **no** exits — with a mutation of "name the exits whenever the route's revision is uncarded", which the carded-C assertion catches. Keep the outage half of the row (three more revisions, R collected with its Service under a route that still names it) unchanged; it is right, and it is the coverage the BLOCKER was about.
3. At `:182` and `:311`, replace "since otherwise the re-point ends the state on a later pass" with the true reason: otherwise either the re-point ends it on a later pass, **or** what stops it is a hold this message's stage clause and `blocked` already name.

---

## MAJOR 2 — option (c) is put to the human as adding no read, and it adds one

`docs/designs/16-evalsuite.md:136`, repeated at `:838`.

> `:136` "**(c) Widen clause 1 only to a missing-policy `Lock` in the residue**: neither the revision the route's single `backendRef` names nor `status.activeRevision` carded. **It reads from `status` in the same pass as the other four clauses, adds no read**…"
> `:838` "…or widen only to one in the residue, which is the only state fitting the exemption's premise and is **readable from `status` with no new read**."

The second half of the residue is in status. The first half is not. "The revision the route's single `backendRef` names" is read from the `HTTPRoute` — that is what `singleBackend` (`authtxn.go:2140`) does, and `cardAttributes` (`:2187`) reads no other source. Nothing in `status.auth` records it: `beforeRevision` is set only by `ProbingBefore` (`:1673-1675`), which a missing-policy `Lock` never runs, and it is cleared on a promotion (`:1635-1640`).

The controller that must evaluate clauses 1 to 5 before a claim is the **eval** controller, and it reads the Agent, the EvalSuite, and — for clause 5 alone — the Gateway and two policy lists (`:139`, `:203`, `:204`). It reads no `HTTPRoute`. `:139` and `:203` make that separation deliberate: clause 5 is "the eval controller's alone", and the Agent reconciler "makes no Gateway or policy reads of its own". (c) inverts it: the eval controller would need `<agent>-serving`, which the Agent reconciler's gate branch already reads (`:181`) and the eval controller does not. That is one more object kind on the eval path, and it is precisely the shape of read the slice went to some trouble to keep on one side.

This is rule 7 — a property stated that nothing in the design provides — and it is in the two sentences the human will read to choose. It does not change the recommendation; it makes (a) look better, not worse.

**Fix.** At `:136` and `:838`, strike "adds no read" / "with no new read" and say what is true: the residue's second half is in status, its first half is the route's single `backendRef`, so (c) gives the eval controller a read of `<agent>-serving` that it does not make today — cheap, and RBAC already grants it, but a new read on the eval path, where clause 5's reads were kept deliberately separate. Worth naming beside it: a status-only approximation of (c) exists — widen on clause 4 alone — and it is over-inclusive only where the route's revision is carded and `status.activeRevision` is not, which is a state the `Lock` finishes out of within a pass or two. If the human wants (c) without the read, that is the variant to offer.

---

## MINOR 3 — one of the restored row's four mutations survives

`:557`, third mutation: "move the re-point above the foreign check, which the `backendRef` assertion catches".

`:166` fixes what "the foreign check" means: the check at `authtxn.go:1740`, inside the `ProbingAfter` arm. In this row's main build no pass reaches it. The pass enters the arm at the intact read (`:1694`), finds the relabelled policy's digest different from the target's (`authPolicyIntact` `:2496-2512`; the UID label is part of `compiler.Digest`, which is why case 18 (c) asserts `ApplyingPolicies` and not `ProbingAfter`), rewinds to `ApplyingPolicies` and `continue`s, and breaks there on `foreignAtOwnName` (`:1682-1685`). Moving `:1751-1787` above `:1740` therefore changes nothing this row can see, and the mutation is SURVIVED, not killed.

**Fix.** Aim it one rung higher — "move the re-point above the **intact read**" — which fires on the one pass that still enters the arm at `ProbingAfter` and moves the `backendRef` to C, and which the "route still names R" assertion does catch. That needs the build to say what case 18 (c)'s helper already does: (3) waits for the `Lock` to reach `ProbingAfter` before (4) stores the foreign policy.

---

## MINOR 4 — the rebuilt promotion row's new second mutation names no assertion that can see it

`:579`, second mutation: "credit on R's card, which the C-card attribution catches".

The sequence the row rests on is right and was re-verified: the rollout switch (`agent_controller.go:687-805`) runs before `reconcileGateway` (`:816`), so the promoting pass sets `status.ActiveRevision = C` and then re-points, because the re-point reads `status.ActiveRevision` and not the route (`authtxn.go:1771`); the write bumps the generation `routeConverged` (`:2518-2536`) compares, so that pass takes no answer; `backendOf == r2` immediately after one `reconcileOnce`, and the credit only after `acceptRoute`, is what `TestACandidatesPromotionEndsTheWedgeWithNobodyActing` measures (`authgate_test.go:282-293`). The first mutation, "re-point only after `Served`", is the shipped pre-A77 shape, compiles as a deletion of `:1751-1787`, and is killed by the mid-`Lock` `backendRef` assertion.

The second is not placed. Once the route names C, every reading of `cardAttributes` returns C — including its pre-A77 all-refs shape, since the route carries one ref. The only way the credit's *source* is observable is the `fetchedAt` the served message names (`:2130-2132`), and the row neither says the two cards carry distinguishable `fetchedAt`s nor asserts on that string.

**Fix.** State the mutation as `cardAttributes` returning the first entry in `status.cards[]` with a non-empty digest regardless of the backend it names, and give the row the assertion that kills it: R's and C's cards are recorded with different `fetchedAt`s, and the served `GovernanceSkipped` message names C's.

---

## MINOR 5 — the ungated sentence in "Leaving it standing" drops the qualification the same row just added

`:178`: "On an ungated Agent the same needs three promotions **of revisions whose cards never record**, because a promotion to a carded one fires the re-point and ends the state."

Two sentences earlier the row says a gated promotion "does not close it: Q10 puts a card on C, and **the re-point still has to be reached**". The ungated sentence is the same claim without the qualification, and it is the sentence shape the BLOCKER was about. A foreign `traffic` policy, a fresh NACK or a `Lock` short of `ProbingAfter` hold an ungated Agent exactly as they hold a gated one.

**Fix.** "…because a promotion to a carded one fires the re-point **on the first pass that reaches it** and ends the state."

---

## MINOR 6 — the normative cell's third ending is thinner than the body's

`:311`. The cell now carries `:182`'s condition verbatim, the revert scoped to a route that names one revision, and a third ending — all three of MAJOR 2's asks, correctly. What it says of the third ending is "the state also ends with nobody acting once `status.activeRevision`'s card records, which fires the re-point". The body's own row (`:173`) adds two things the cell drops: the re-point has to be **reached**, and on a gated Agent the candidate's own promotion is a further ending, by the two paths `:176` now names. A reader of the table alone — the reader `AGENTS.md` sends there, and the reason A78 calls this cell the sharpest instance — gets an ending that is unconditional and a list that is short by one.

**Fix.** One clause: "…once a pass reaches the re-point with `status.activeRevision`'s card recorded, or the candidate promotes on a verdict written before or in flight when the `Lock` began".

---

## MINOR 7 — the restored row's step (4) under-specifies the foreign policy

`:557` step (4) says "a `traffic` policy that is **not** this Agent's is stored at `<agent>-auth`". `foreignTrafficPolicies` (`internal/controller/authforeign.go:37-68`) also requires it to carry `spec.traffic` and to target `<agent>-serving`; without both, `out.foreign` is empty, `foreignAtOwnName` is false, and the pass does not hold. Case 18 (c), which the row cites, gets this by relabelling the `Lock`'s own written policy (`authgate_test.go:313-322`), so the build is right by reference — but the row states the order as "the whole row" and does not state the shape.

**Fix.** "…a `traffic` policy targeting `<agent>-serving` and carrying another Agent's UID, stored at `<agent>-auth` — case 18 (c) makes one by relabelling the `Lock`'s own."

---

## On the escalation of clause 1, for the human

**The recommendation is right, the three-way framing is the right shape, and (a) and (b)'s costs are stated correctly. One property of (c) is not** (MAJOR 2).

(a)'s four endings were each checked. The administrator's delete reaches `recreateRoute` through `runLock`'s re-creation branch (`authtxn.go:1606-1617`). The revert makes R desired again and restarts its card fetch, with A60's two conditions as `:173` states them. "`status.activeRevision`'s own card records **and a pass reaches the re-point**" is now correctly qualified. And the fourth — a candidate promoting on a verdict written before the `Lock`, or on the outcome of the case in flight when it began — is real: step 7 puts the verdict in the last case's record (`:238`), `:193` says so, and `:195`/`:157` say a promotion whose verdict is already written is stopped by neither a transaction nor a hold. Case 18 (c) confirms the promotion happens with the `Lock` held (`authgate_test.go:324-327`).

(b)'s cost is stated correctly: it exempts `Lock`s a card recording or a promotion is about to finish, buys nothing there, and puts eval patches beside a transaction one pass from writing.

**On the second direction** (`:132`), which the first critique found unanswered: the drafter's answer is right and well-sourced. Design 03 §3.3.3's own text says "an Agent promoting faster than the Gateway reports never probes" (`03-policy-compiler.md:789`), and that a `Lock` held back that way is the last one to earn more promotions for is the correct inference. It argues against widening, and A19 correctly declines to argue either option from it.

**What the human should be told that the text does not yet say**: (c) is not free of new reads. Its second half is in status; its first half is the route's single `backendRef`, which only the Agent reconciler reads today. So (c) costs the eval controller one `HTTPRoute` read per pass — small, already within RBAC, but a read, and the slice deliberately kept clause 5's reads on one side of that line. If the human wants (c)'s liveness without the read, widening on clause 4 alone is the status-only approximation; it is over-inclusive only where the route's revision is carded and `status.activeRevision` is not, and that `Lock` credits itself out within a pass or two. **My own recommendation is (a)**, on the same grounds the drafter gives: what (c) buys is the right to promote onto an Agent that is paged and `Degraded`, and every other ending is documented and reachable. If the human wants the liveness, (c) — or its status-only approximation — and not (b).

---

## Where the second draft is right, and should not be redone

- **The BLOCKER is closed at the root.** `:166`'s "**A carded C is not enough on its own: a pass has to reach the re-point**" is exactly `runLock`: the re-point is at `:1771`, after the intact read (`:1694`), the fresh NACK (`:1705`), the `Converging` tuple (`:1716-1724`) and the foreign check (`:1740`). The A75 Gateway read sits between the foreign check and the re-point (`:1731-1739`) and does **not** break the pass, so omitting it from the list of breaks is correct, not an oversight. The three ways `:166` lists — a pass that never reaches it, a drift re-read that clears the digest (`recordCardAttempt` clears `c.Digest` in place, `card.go:412-419`), and a promotion that never needed Q10's card — are the three, and the drift-clear path is correctly described as the ordinary case rather than a bypass. All five places the first draft carried the false premise (`:166`, `:178`, `:499`, `:557`, `:833`) now carry the true one.
- **The restored row's build order is right where it matters, and the reason is right.** `authPolicyPresent` (`:2226-2238`) matches on name alone, so a foreign policy present when `reconcileServed` runs sends the pass to `reassertServedPolicy` (`:1510-1521`, `:2243-2278`) and no `Lock` is entered — which is why (4) must follow (3), exactly as case 18 (c)'s comment says. The garbage-collection half is right: retention keeps `activeRevision`, `candidateRevision` and the two newest others, so R falls out after three more revisions, and nothing in `runLock`'s re-creation branch ever writes the route, so it stays on R throughout.
- **The variant is correct and is the row's best part.** At `Converging` the tuple breaks at `:1721-1724` before any re-point, a promotion in that window moves `status.activeRevision` to C, `recordCardAttempt` clears C's digest, and the `Lock` then reaches `ProbingAfter` with `cardedRevision(C)` false, so the route is left as found. "Re-point unconditionally" is killed there, and `:556`'s note that it belongs to the next row and not to that one is the right kind of care.
- **MAJOR 3's rebuild is correct against the code and against the shipped test**, including the detail that R's card being recorded changes nothing because the re-point reads `status.activeRevision` and not the route.
- **MAJOR 4 is closed in all four places** (`:134`, `:173`, `:176`, `:830`), and both paths check out against step 7 (`:238`), `:193` and `:195`.
- **All seven minors are closed.** The route gate is `ProbingAfter`'s in both places (`:409`, `:828`) with `ProbingBefore` explicitly excluded; the re-point and the credit are separated at `:173` and `:556`; the second direction is answered; clause 4's departure from A2 is written down with the condition for reversing it; the zero-`backendRef` split is right (`routePublished`, `httproute.go:355-362`, is false for such a route and `:1611` hands it to `recreateRoute`); Q13's framing sentence joins `:505`'s list; the design 03 README row is past tense; and `:837` takes the second of MINOR 11's two options for ADR-0024 explicitly.
- **Revising A19 in place rather than adding an A20 was the right call**, and `:840` gives the right reason. A19 has not merged; design 03 A77 answered eight critiques within itself. An A20 answering a critique of an unmerged amendment would split §1.1's provenance across two entries that a reader would have to compose, which is the failure mode §14's one-row-per-amendment rule exists to avoid. The critique row is added at `:857` with the verdict, and `reviews/16-a19-critique.md` is committed unmodified.

## Noted, not counted

- `:311`'s "(A or B) and C" reads correctly as written, and matches `missingPolicyRouteNote`'s `!attributed && !cardedRevision(active)` (`:1971-1974`). The comma-then-"and" is not ambiguous here.
- `:185` gives the zero-`backendRef` case wording the operator does not emit (its `backend == ""` arm at `:2045-2049` names the administrator for zero and multi alike), while `:183` says the eval message "reuses [the operator's wording] rather than inventing a second phrasing". That is the MINOR 9 fix doing what it was asked to do, and the eval message is design 16's own, so it is a tension rather than a defect — but a reader comparing the two will notice.
- `:579` does not say the test must report the Gateway at the route's new generation before the credit arrives, as case 18 (b) does with `acceptRoute`. `:556` has the same shorthand. It is an envtest-harness detail and consistent across both rows.
- `:828-840`'s log is accurate against the diff, including the count of owed rows (five, none withdrawn) and the fifth inventory miss being attributed to the critique rather than to the first draft.
