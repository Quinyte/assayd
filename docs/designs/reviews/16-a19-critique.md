# Critique: design 16 A19 — the A77/A78 follow-up to the approved first slice (§1.1)

- **Date**: 2026-09-16
- **Target**: `docs/designs/16-evalsuite.md` §1.1 and amendment A19, and the design 16 row of `docs/designs/README.md`, at `8a9723f` (branch `design-16-a77-followup`), read whole and as `git diff origin/main...8a9723f`. A19 answers design 03 A77 §11's inventory and A78, not a critique of §1.1.
- **Independence**: an independent session. It wrote none of A3–A19 and none of the twelve earlier critiques of §1.1, and none of design 03 A77 or A78. Every claim about shipped behaviour below was traced by hand in the tree at `8a9723f`; nothing is taken from A77's inventory, from A19's own account of it, or from the earlier records.
- **Scope**: §1.1 as approved text, its owed test rows, the §14 log, and the README row. Design 03 and design 02 are not edited by A19 and are read only as the authority A19 realigns against. Nothing in this slice is implemented, so nothing was run; the design 03 behaviour A19 describes **is** implemented, and was read and cross-checked against its envtest cases.
- **Read against**:
  - `internal/controller/authtxn.go`: `runLock` (`:1593-1935`) — the `reCreate` branch and its `recreateRoute` hand-off (`:1606-1622`), the `ProbingAfter` arm's order (intact read → NACK → A75 Gateway read → foreign check → **re-point** `:1769-1785` → **route gate** `:1811-1826` → probe → `cardAttributes` `:1838`); `missingPolicyRouteNote` (`:1952-2073`) and its four `admits` arms; `singleBackend` (`:2140-2145`); `cardAttributes` (`:2187-2206`); `cardedRevision` (`:2213-2226`); `reconcileServed` (`:1498-1549`) and its two `recreateRoute` sites; `authStep`'s dispatch (`:702-730`), which reaches `reconcileServed` only when no transaction is recorded; `routeConverged` (`:2518-2542`), which compares `ObservedGeneration` against `rt.Generation`.
  - `internal/controller/card.go`: `hasCardFor` (`:405-408`) and `cardEntry` (`:385-392`), both keyed on `RevisionDigest`; `recordCardAttempt` (`:412-431`); `upsertCard` (`:320-330`) and its comment on the 40-bit name.
  - `internal/controller/httproute.go`: `ensureServingRoute` (`:249-320`), which returns the post-`Update` object; `servingRouteFor`'s one rule and one `backendRef` (`:178-200`); `routePublished` (`:355-362`).
  - `internal/controller/agent_controller.go`: the rollout switch (`:686-805`) and its promotion default.
  - `internal/revision/collision_test.go:68` — "The name is 40 bits and must not be treated as more."
  - `test/envtest/authgate_test.go`: `mintRevision`, `backendOf`, `acceptedGen` (`:68-114`); case 18 (a) `TestTheMissingPolicyLockEndsWhenTheActiveRevisionIsCarded` (`:123-…`); `TestACandidatesPromotionEndsTheWedgeWithNobodyActing` (`:248-296`); case 18 (c) `TestNoBackendMovesWhileThePolicyIsNotThisAgents` (`:305-344`); case 18 (d) `TestTheRouteGateStopsThePassThatMovedTheRoute` (`:356-…`).
  - `docs/designs/03-policy-compiler.md` A77 (`:1431-…`), its design 16 inventory (`:1444-1457`), and A78 (`:1422-1430`); `docs/research/auth-lock-false-credit-2026-09.md`; the eight A77 critique records.
  - `docs/decisions/0024-p3-semantic-admission.md` Amendment 1.
- **Method**:
  - Every sentence A19 added or replaced, traced to the shipped function it cites, not to the inventory.
  - Each of the drafter's five claimed corrections to the inventory checked independently against the code.
  - Each owed test row that touches this state built in the head against `runLock`'s actual order, and each named mutation run against the rule it is meant to kill.
  - The exemption's five clauses read against `cardAttributes`, `cardedRevision` and `hasCardFor` for key agreement in both directions.
  - Q1–Q13 re-read for movement, with Q10, Q12 and Q13 read line by line.
  - The whole of `runLock`'s `ProbingAfter` arm walked for every way a pass can end above the re-point.

**Verdict: FAIL — 1 BLOCKER, 3 MAJOR, 7 MINOR.**

The four things A19 found against the code that A77's inventory missed are all real, and finding them took reading `authtxn.go` rather than the inventory. Clause 4's re-key is correct and its asymmetry argument is sound. The route-gate correction to "What it buys" is correct and is the only place in the design that states the gate right. Declining to widen clause 1 without the human, and saying plainly that the boundary is now under-inclusive, is the right call. But A19's own new central claim — that on a gated Agent the route and the active revision can no longer stay apart — is false, is contradicted by a test that ships in this repo and by A19's own adjacent row, and is what justifies deleting an owed test. Two more of the eighteen sentences A19 exists to fix are still false: the normative conditions table, and one owed row A77's inventory and A19 both missed.

---

## BLOCKER 1 — A19 withdraws an owed test row on a premise the shipped suite measures false, against the one instruction A77's inventory gave in bold

`docs/designs/16-evalsuite.md:172` ("Leaving it standing"), `:160` (the second wedge paragraph), `:493` (Q13's outage bullet), `:550` (the replacement owed row), `:826` (§14's justification for the withdrawal).

The new claim, in four forms:

> `:172` "The garbage-collection outage needs the route and the active revision to stay **apart**, which since design 03 A77 needs `status.activeRevision` to stay uncarded, and a gated promotion cannot leave it so: Q10's fourth condition and the re-point's condition read the same fact in the same pass."
> `:160` "So the route and the active revision stay apart only where a promotion bypassed that condition — a pinned rollback Q5 admits, or a promotion after `spec.gates` was removed (Q9) — or where a failing drift re-read cleared C's digest afterwards."
> `:493` "Since design 03 A77 the route and the active revision stay apart only while `status.activeRevision` has no recorded card digest, which a gated promotion under Q10 cannot leave."
> `:550` "the state that kills it — a promoted, uncarded `status.activeRevision` — is not buildable on a gated Agent."

All four rest on the inference *`status.activeRevision` carded ⟹ the re-point fires ⟹ the route and the active revision are not apart*. The inference drops the requirement that a pass **reach** the re-point. The re-point is at `internal/controller/authtxn.go:1769`, inside the `StageProbingAfter` arm, **after** the intact read (`:1693`), the fresh-NACK check (`:1705`) and the foreign-policy check (`:1738`). A `Lock` short of `ProbingAfter`, or a pass that breaks at any of those three, never re-points, however carded the active revision is.

**Measured false in this repo.** `test/envtest/authgate_test.go:305` `TestNoBackendMovesWhileThePolicyIsNotThisAgents` mints r2 **carded** (`mintRevision(t, r, a, true)`), stores a foreign policy at `<agent>-auth`, and then asserts, in one pass: `ActiveRevision == r2`, the `Lock` held at `ApplyingPolicies`, and `backendOf == r1`. Route on r1, `status.activeRevision` r2, r2 **carded**, apart — and design 03 A77 §11 says that state "is never taken over or deleted, and is unbounded".

**A19 contradicts its own adjacent row.** `:171` ("Not exits, and when they became exits") gets this exactly right: "Neither is an exit on a pass that never reaches the re-point: a foreign `traffic` policy on `<agent>-serving`, a fresh NACK, or a `Lock` still at `ApplyingPolicies` breaks the pass above it (design 03 §3.3.3, §8.1 case 18 (c))." One row later, `:172` asserts the state that row describes cannot arise.

**And it contradicts the inventory it is discharging.** `docs/designs/03-policy-compiler.md:1449`, in bold: "**Narrowed, not removed** — two over-claims the first draft made, withdrawn here. The 'Not exits' row … and the 'Leaving it standing' outage (R collected with its Service under a route that still names it) both **survive**, because a `Lock` held at `ApplyingPolicies` by a foreign policy at `<agent>-auth` never reaches the re-point … §8.1 case 18 (c) builds exactly that. So those two rows narrow to the held case; they do not vanish."

The cost is not cosmetic. On that premise A19 **withdraws** the owed row "the wedge with the route and the active revision apart" (`:826`), which was the only owed coverage of the state in which garbage collection deletes R and its Service under a route that still names it — authenticated callers failing on a missing backend while anonymous callers still get `401`. A19 deletes the test and records the outage as unreachable on a gated Agent.

Two further gaps in `:160`'s "only where" list, both of which the restored row should carry: the drift-clear path it does name is not a bypass at all (a Q10-compliant gated promotion lands while the `Lock` is still at `ApplyingPolicies`/`Converging` — the ordinary case, since the `Lock` is entered the pass the policy is found missing — and `recordCardAttempt` can clear C's digest before the `Lock` ever reaches `ProbingAfter`); and the promotion itself can be a gated one, by the in-flight-last-case route A19's own `:550` builds.

**Fix.** Restore the "wedge with the route and the active revision apart" owed row. Its build no longer needs the promoting-pass trick: clear C's digest with the failing card stub `:549` already uses, or hold the pass above the re-point as design 03 §8.1 case 18 (c) does. Rewrite `:160`, `:172` and `:493` to say what is true — the apart state survives A77 wherever a pass never reaches the re-point (a foreign `traffic` policy, a fresh NACK, a `Lock` short of `ProbingAfter`) and wherever a failed drift re-read clears `status.activeRevision`'s digest — and drop `:550`'s "not buildable on a gated Agent" sentence, keeping the rest of that row, which pins a different and correct fact.

---

## MAJOR 2 — the normative conditions table still carries the pre-A19 rule, untagged

`docs/designs/16-evalsuite.md:305`, the `GatesPassed` row of "Status, conditions and phase":

> "For a missing-policy `Lock` at `ProbingAfter` whose route's `backendRef` names a revision with no recorded card, the message also names the exits. An administrator of the run namespace deletes `<agent>-serving`, with the outage that follows. Or, for a transient card failure, the spec is reverted to that revision (A8, corrected by A9)"

A19 rewrote the prose rule at `:176` on three counts — the single `backendRef`, the new second half ("**and `status.cards[]` records none for `status.activeRevision` either**, since otherwise the re-point ends the state on a later pass and there are no exits to name"), and a third ending at `:167`. The conditions table was not touched: it keeps the plural reading A77 abolished, keeps the pre-A77 condition, names two exits where the body now carries three, and has no A19 tag. An implementer building to the table names the exits — and names an administrator's outage — for a state the re-point is about to end on its own, which is rule 8's loud-and-wrong and the exact defect design 03 A78 records `missingPolicyRouteNote` committing twice.

Design 03 A78 (`03-policy-compiler.md:1424`) makes this the sharpest class of miss on purpose: "The slice table is the sharpest instance, because §1.1 is the approved boundary and `AGENTS.md` tells every reader to trust a design's own Status line and table over any summary."

**Fix.** Rewrite `:305`'s clause to `:176`'s condition verbatim — the route does not carry exactly one `backendRef`, or the revision its single `backendRef` names has no recorded card digest, **and** `status.cards[]` records none for `status.activeRevision` — add the third ending, and tag it A19.

---

## MAJOR 3 — an owed row that A77 falsifies, missed by the inventory and by A19, now contradicting A19's own new row

`docs/designs/16-evalsuite.md:572`:

> "a missing-policy `Lock` recorded when a `Passed` candidate is promoted, **with the card of the route's revision R recorded** … **While the `Lock` runs, the route's `backendRef` still names R, and the `401` is credited on R's card. After `Served`, the route's `backendRef` moves to C** | skip the route's re-assertion once the `Lock` is `Served`, which the last assertion catches"

The re-point's condition is `reCreate && cardedRevision(status, status.ActiveRevision)` (`internal/controller/authtxn.go:1769`). It reads `status.activeRevision`, not the revision the route names. On a gated Agent C must be carded to be promoted (Q10's fourth condition, `:262`), so after the promotion `cardedRevision(C)` is true and the re-point fires at the first `ProbingAfter` pass. The route moves to C **while the `Lock` runs**; the `401` is credited on **C's** card; the move does not wait for `Served`. Every assertion in the row, and both of its mutations, are dead. R's card being recorded — the row's stated premise — changes nothing, because the re-point does not read R.

This is the fifth thing A77's inventory missed, and A19's "Four things the inventory missed" (`:827`) does not catch it either. §14's S1 entry (`:731`) still names the row ("the rebuilt promotion-during-a-missing-policy-`Lock` test"), and `:550`, added by the same amendment, asserts the opposite outcome from a state that differs only in whether R is carded.

**Fix.** Rewrite `:572` on A77: the route moves to C at the first `ProbingAfter` pass after the promotion, the route gate defers the request one pass, the `401` is credited on C's card, `Served` follows, and the `backendRef` never returns to R. Its mutation becomes "re-point only after `Served`", which the mid-`Lock` `backendRef` assertion kills. Add it to `:827`'s list.

---

## MAJOR 4 — A19 narrows A78's measured-harmful warning to a condition its own owed row falsifies

`docs/designs/16-evalsuite.md:170` ("The transient exit"), and the same formulation at `:167` and `:131`:

> `:170` "Design 03 A78's warning — that with a candidate pending a revert aborts the rollout that would have ended this on its own — bites here **only where that candidate's verdict was already `Passed` before the `Lock` began**; otherwise the gate is already holding that promotion, so there is no rollout to abort."
> `:167` "**on a gated Agent under this wedge it cannot promote**, because no case is claimed while the `Lock` is recorded, unless its verdict was already `Passed` before the `Lock` began."
> `:131` "on a gated Agent that residue ends only through an administrator's delete, a revert, or the active revision's own card recording, where an ungated Agent also has the next promotion."

A verdict can also become `Passed` **after** the `Lock` began. `:186` ("The case in flight when a transaction begins"): "A case in flight when a transaction is entered records its outcome after the transaction began." Step 7 puts the verdict in the last case's record. So a run whose last case was in flight when the `Lock` was entered writes `Passed` during the `Lock`, the candidate cards and promotes, and the rollout that would have ended the state is live. A19's own two new owed rows build exactly that: `:550` "The stub holds C's last case while the test deletes `<agent>-auth`, **so the `Lock` is recorded before the verdict**. The stub answers, and that case's record writes `Passed`."

The consequence is the one A78 measured. A reader in the residue, told the warning does not bite, reverts the spec — and `03-policy-compiler.md:1428` records that action as HARMFUL, "the third statement of this defect class, and the one that would have cost an operator something", because the revert aborts the rollout that ends the state. A19 imports A78's warning and then narrows it out of the very state A78 built.

**Fix.** At `:170`, `:167` and `:131`, replace "already `Passed` before the `Lock` began" with the two paths: a verdict already `Passed` before the `Lock` began, **or** the outcome of the case that was in flight when it began, where that case is the run's last (`:186`, step 7). At `:131`, add the candidate's promotion to the list of endings a gated Agent can still have.

---

## MINOR 5 — A19 writes the unqualified route-gate sentence A78 had just swept from eight places

`:403` (the Design 03 dependency row): "on A77's route gate, which stops **any `Lock` taking a probe answer** until the assayd Gateway reports `Accepted` and `ResolvedRefs`…". `:821` (§14's A19 header): "adds a route gate to **every** `Lock`: **no probe answer is taken** until the assayd Gateway reports…".

Design 03 A78 (`03-policy-compiler.md:1424`) narrowed exactly this sentence in eight places, including ADR-0034 Amendment 6, `AGENTS.md`, `CLAUDE.md` and the designs README, because it is false: the gate is `ProbingAfter`'s, and "J2's and K2's single `ProbingBefore` request is **not** gated". A19's own `:154` states it correctly, so the design now contradicts itself.

**Fix.** Insert `ProbingAfter` in both: "no `Lock` takes a `ProbingAfter` answer until…".

---

## MINOR 6 — two new sentences put the re-point and the credit in the same pass, the compression A19 was correcting

`:167` "The next `ProbingAfter` pass re-points the route onto that revision **and the `401` is attributed**, with no administrator and no revert." `:550` "C is promoted with its card recorded, and **the route still names R. On a later pass** the `Lock` re-points the route at C, the Gateway accepts it at its new generation…".

`test/envtest/authgate_test.go:248` `TestACandidatesPromotionEndsTheWedgeWithNobodyActing` measures the opposite ordering on both counts: one `reconcileOnce` promotes r2 **and** fires the re-point (`backendOf == r2` immediately after), and the credit arrives only after `acceptRoute` and a further reconcile, because the write bumps the generation `routeConverged` reads. A19 corrects precisely this compression at `:154` and then reproduces it twice.

**Fix.** `:167`: "…re-points the route onto that revision, and the `401` is attributed on a later pass, once the Gateway has reported at the new generation." `:550`: the re-point fires in the promoting pass when the `Lock` is already at `ProbingAfter`; the route gate then defers the request.

---

## MINOR 7 — A77's inventory item on the exemption is answered in one direction only, silently

`03-policy-compiler.md:1446`: "After A77 a missing-policy `Lock` can finish through a promotion, **and can be held back by one**, so the boundary is wrong **in both directions** and needs revisiting with the wedge subsection."

A19 answers the under-inclusive direction at length (`:131`, `:828`) and never mentions the second, though it claims to be "the amendment that owes" the whole inventory. If the drafter concluded the second direction is empty — a missing-policy `Lock` whose route named a carded revision cannot be pushed into the residue by a re-point, because the re-point only fires onto a carded revision — say so and say why.

---

## MINOR 8 — clause 4 now reads a revision by name alone, against A2, and the reason is not written down

`:126`. The re-key is right, and the asymmetry argument for it is right. What is missing is that it is a deliberate departure from A2's standing rule that "every revision reference in this design is `{name, digest}`", forced by `cardAttributes` itself being name-keyed — a design 03 residue, and one `internal/revision/collision_test.go:68` and `upsertCard`'s comment both call out as the weaker identity. If design 03 ever tightens `cardAttributes` to `{name, digest}`, clause 4 becomes over-inclusive again in the unsafe direction the bullet identifies.

**Fix.** One clause at `:126`: the clause reads the name alone because the function it must exactly negate does, and it follows `cardAttributes` if that is ever tightened.

---

## MINOR 9 — the zero-`backendRef` split sends a self-healing state at an administrator

`:179` ("The fallback") now puts a route with no `backendRef` in the specific message rather than the generic one, and `:177` says that message "offers only the administrator's exit". But `routePublished` (`internal/controller/httproute.go:355`) is false for a route with no `backendRef`, so `runLock`'s re-creation branch hands such a route to `recreateRoute` (`authtxn.go:1612`) before the `ProbingAfter` arm is ever reached — whenever `status.ActiveRevision` is set, which a served-under-`apikey` Agent has. The operator therefore emits that arm of `missingPolicyRouteNote` essentially only for a route with **two or more** refs. For the one stale pass the gate branch can see a zero-ref route, the honest message is that `recreateRoute` takes over in this same pass, not that an administrator must act.

**Fix.** At `:179`, say that a route with no `backendRef` reaches the specific message only in the stale pass `:178` describes, and that `recreateRoute` resolves it without an administrator; keep the administrator-only wording for the multi-ref case, which is where the operator emits it.

---

## MINOR 10 — Q13's own framing sentence is now over-broad and is not tagged

`:495`: "On a gated Agent, a missing-policy `Lock` whose route names a revision with no recorded card cannot finish." After A77 it can, whenever `status.activeRevision` is carded. `:434` keeps question text deliberately, and A19 rightly adds a dated follow-up at `:499` — but that follow-up names only the "fix that removes the wedge" sentence as stale, not the framing. A reader of the decisions section alone gets the wider, pre-A77 wedge.

**Fix.** Add the framing sentence to `:499`'s list of what the question records rather than asserts.

---

## MINOR 11 — two cross-references A19's own change falsifies, one of them in the file it edits

`docs/designs/README.md:11` (the design 03 row): design 16's first slice "**owes a follow-up amendment**, recorded in its own row and not edited here". A19 pays that debt in the very next row of the same file and leaves this one in the present tense. `docs/decisions/0024-p3-semantic-admission.md:26` (Amendment 1, Q13): "The design 03 amendment that would remove it goes through design 03's own process, and this amendment does not approve it" — that amendment has landed, as A77, and is conditional. Leaving the ADR text as the record of 2026-09-14 is defensible; leaving it with no pointer to ADR-0034 Amendment 6 is not, given §1.1 now carries exactly such a pointer.

**Fix.** Change the design 03 row's "owes" to "owed, paid by design 16 A19". Add one dated sentence to ADR-0024 Amendment 1's Q13 line, or record in A19's §14 that the ADR keeps its 2026-09-14 text and ADR-0034 Amendment 6 carries what landed.

---

## On the escalation of clause 1 (the drafter's one open question)

**The call is right, the reasoning is sound, and the framing is very nearly right.**

Widening clause 1 changes which Agents the eval claims cases beside, in an approved section, and it does so in the direction that adds concurrency with a transaction's status writes — precisely the contention N4 and A12's bound were built around. That is a rule change, not a correction, and Q13 (a) named design 03's own process as the route for exactly this class of change. The contrast A19 draws with clause 4 (`:827`) holds up under inspection: clause 4 cited `cardAttributes` as its own justification and then read a different key, and the two disagreements are asymmetric in the way the bullet says — a name-keyed read that finds a digest the digest-keyed read does not fails the clause and holds the eval, while the reverse exempts a `Lock` that `cardAttributes` would in fact have credited. Clause 1 has no such self-contradiction to repair.

"Fail-closed, costs liveness not safety" is the right characterisation. Widening clause 1 would let the eval run beside a missing-policy `Lock` on an Agent whose route is open and enforcing but unproven, and whose page is standing; leaving it means that Agent gets no new revision without an administrator. Neither choice changes what the gateway admits.

Two things the framing is missing, and the human should not decide without them:

1. `:131`'s statement of what the residue costs is under-inclusive in the same way MAJOR 4 is: a gated Agent's residue can also end through the candidate's own promotion, where that candidate's verdict came from the case in flight when the `Lock` began. Widening clause 1 is worth less than `:131` implies in that case, and more in the case where no such verdict exists.
2. The question is posed as a binary. There is a third answer worth naming: widen clause 1 only to a missing-policy `Lock` **in the residue** — neither the route's revision nor `status.activeRevision` carded — which is the state that fits the exemption's premise, and keep it excluded outside it. That is narrower than "admit re-creations", it is readable from status alone in the same pass as the other four clauses, and it does not exempt a `Lock` that a card recording or a promotion is about to finish on its own.

**My recommendation: leave clause 1 as it is for this slice, and put it to the human with the third option named.** The residue is the one state where widening buys anything, and it is a state whose other exits (the administrator's delete, the revert, the active revision's card) all exist and are documented. Exempting it buys the eval the right to run beside a `Lock` that will not finish, on an Agent that is already paged and `Degraded` — liveness for a candidate on an Agent nobody should be promoting onto until the page is cleared. That is a small gain against a real cost in concurrency with a transaction's status writes, and against the simplicity of "the exemption is J2 and K2 only", which twelve critiques have now read. If the human wants the liveness, the narrow third option is the one to take, not the full widening.

---

## Where the work is right, and should not be redone

- **The four inventory misses are real, and each was found against the code.** Clause 4 read `status.activeRevisionDigest`, which is `hasCardFor`'s key (`card.go:405`, `cardEntry` `:385`), while `cardAttributes` (`authtxn.go:2195`) and `cardedRevision` (`:2219`) both match on `c.Revision`; re-keying on the name is correct and the safe direction is the one A19 argues. `runLock` does have two `recreateRoute` call sites (`:1612`, `:1778`), both under `reCreate`, so A13's rule survives with only its count wrong, and the other two are in `reconcileServed` (`:1508`, `:1529`), which `authStep` reaches only when no transaction is recorded (`:702-730`). The route gate does defer a J2 or K2 credit past the promoting pass: `ensureServingRoute` returns the post-`Update` object, and `routeConverged` compares `ObservedGeneration` against the bumped `rt.Generation` — which is precisely the defect `research/auth-lock-false-credit-2026-09.md` measured at `3f37c25`, where the promoting pass credited the previous revision's own `401`. And a route with two `backendRef`s does get the specific arm, because `singleBackend` (`:2140`) returns `""` for anything but one rule with one ref.
- **Taking `:167`'s wording from `missingPolicyRouteNote` rather than inventing a second phrasing** is the right call given A78's record of that message being wrong twice, and the quoted behaviour matches the code's `candidate` arm exactly, A60's two conditions included.
- **No Q1–Q13 decision text is moved**, and Q12 (a) and Q13 (a) both still stand on their own reasons. The `:499` follow-up bullet is the right shape for a decision whose expectation has since landed differently.
- **The numbering and the log mechanics are correct**: A14 and A16 do not exist, A18 is the approval entry, A19 is next; no critique row is added to the table at `:831`, which is right, because A19 answers an inventory; the Status line is unchanged and nothing in it went false; and the A8/S1 entry at `:731` is annotated rather than rewritten, which keeps what A8 claimed while pointing at the correction — the convention A78 follows for A77's own superseded sentence.
- **The single-`backendRef` narrowing is carried into all three places A77 named**, and the "R's name in the wedge message" row is pinned to the reading A77 left open, which is the buildable one.
- **`reconcileServed`'s unreachability while a `Lock` is recorded**, which three sentences rest on, was re-read and is still true.

## Noted, not counted

- `:822`'s statement of the residue — "neither the revision the route's single `backendRef` names nor `status.activeRevision`" — presumes the route names a revision. §1.1's own `:176` and `:177` handle the no-single-ref case correctly, so this is the log's shorthand, not a gap in the rule.
- Three rows A19 changed carry no tag (`:166` "The route", the Agent's-conditions row, `:170` "The transient exit"), where five others do. Each is an addition rather than a retraction, so there is nothing to quote; §14 `:822` records them. Worth a house rule either way, since A8's own entry claims "each changed sentence is tagged there".
- `:753` (§14's m2 entry) keeps the plural-`backendRef` reading untagged beside `:731`, which A19 did tag. §14 is provenance and keeping the text is right; only the tag is missing.
- The `Adopt` and I1 rows, the `blockedFor` bullet, clause 5's reads, and the requeue field are untouched by A19 and were re-read; nothing in them went false under A77.
