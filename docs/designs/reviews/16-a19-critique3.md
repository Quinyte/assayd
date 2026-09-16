# Third critique: design 16 A19, third draft — the A77/A78 follow-up to the approved first slice (§1.1)

- **Date**: 2026-09-16
- **Target**: `docs/designs/16-evalsuite.md` §1.1 and amendment A19, and the design 16 row of `docs/designs/README.md`, at `38577e4` (branch `design-16-a77-followup`). Read as `git diff 8ad9722 38577e4` (this round) and `git diff origin/main...38577e4` (the whole amendment), and whole in the head.
- **Independence**: a third, independent session, plus one further forked pass whose findings are re-verified by hand below and marked where they are its and not this session's. Neither wrote A3–A19, the twelve earlier critiques of §1.1, `reviews/16-a19-critique.md`, `reviews/16-a19-recritique.md`, or design 03 A77 or A78. Every behavioural claim was traced to the tree at `38577e4`; nothing below rests on a prior critique's account of the code.
- **Scope**: the two MAJORs and five minors the third draft claims to close, the re-aimed row, the repaired justification, and anything the third draft introduces. Nothing in this slice is implemented, so nothing was run; the design 03 behaviour A19 describes **is** implemented and was read against its source and its envtest cases.
- **Read against**:
  - `internal/controller/authtxn.go`: the transaction dispatch (`:702-720`); `reconcileServed` (`:1498-1543`) and `recreateRoute` (`:1550`); `runLock`'s `reCreate` branch (`:1606-1617`), its `ProbingBefore` arm (`:1662-1680`), its `ApplyingPolicies` arm and `foreignAtOwnName` (`:1681-1692`), and the `StageConverging, StageProbingAfter` arm in order — intact read (`:1694`) → fresh NACK (`:1705`) → the `Converging` tuple (`:1716-1730`) → A75's Gateway read (`:1731-1739`) → the foreign check (`:1740-1750`) → the re-point (`:1751-1787`) → the route gate (`:1788-1826`) → probe (`:1827`) → `cardAttributes` (`:1839`); `reportForeign` (`:514-560`), `raiseIncomplete` (`:500`) and `incompleteOrder` (`:487`); `missingPolicyRouteNote` (`:1952-2073`) and its early return (`:1971-1974`); `lockServed`'s `reCreate` message (`:2106-2135`); `singleBackend` (`:2137`); `cardAttributes` (`:2187`); `cardedRevision` (`:2213`); `authPolicyPresent` (`:2226`); `authPolicyIntact` (`:2496`); `routeConverged` (`:2518`).
  - `internal/controller/authforeign.go`: `foreignTrafficPolicies` (`:37-69`) and **`servingRouteLabels` (`:107-121`)**.
  - `internal/controller/card.go`: `cardEntry` (`:385`), `hasCardFor` (`:405`), `recordCardAttempt` (`:412`), `pruneCards` (`:431`).
  - `internal/controller/agent_controller.go`: the absence of any route write outside `reconcileGateway` (`:816`).
  - `test/envtest/authgate_test.go`: `TestNoBackendMovesWhileThePolicyIsNotThisAgents` (`:305-344`).
  - `docs/designs/03-policy-compiler.md` §8.1 case 18 (a)–(h) (`:1382-1389`) and A77's mutation table (`:1468`).
- **Method**: the restored owed row rebuilt in the head pass by pass against `runLock`'s actual order, with each of its five mutations placed in the source and traced to the assertion said to kill it; the four options put to the human composed against the exemption's own conjunction of five clauses rather than against the prose describing them; every cost claimed for an option traced to the function that would make the read; the repaired justification checked against each of the three holds it covers; and §1.1 swept for the inference the first critique's BLOCKER killed, by property rather than by the five sites the earlier rounds enumerated.

**Verdict: FAIL — 0 BLOCKER, 3 MAJOR, 4 MINOR.**

Round 2's two MAJORs and all five of its minors are closed at the row and against the code. The restored row is buildable with every assertion true, its five mutations all compile and all die, and "When it names the exits" now carries a reason that holds. The three MAJORs are: the option block put to the human contains two options that are one rule (MAJOR 1); the cost that block states for (c) — the correction round 2 asked for, and which A19 wrote in faithfully — is false, and §1.1 says so thirty lines above it (MAJOR 2); and the inference the first critique's BLOCKER killed still stands in two places, one of them a decided question's own text, that neither earlier round enumerated (MAJOR 3). MAJOR 1 and MAJOR 2 are the same block and one edit; together they collapse the choice from four options to three and shrink the spread between them to almost nothing, which makes the question to the human sharper rather than larger.

---

## MAJOR 1 — (c′) is not a fourth option. It is (b), and the block's costs and recommendation contradict each other in consequence

`docs/designs/16-evalsuite.md:137-139`, with `:130` and the §14 copy at `:841`.

The exemption is the conjunction of **five** clauses: `:118` "The rule exempts the state in which all five of these hold", `:129` "While all five hold, cases are claimed as if no transaction were recorded". Clause 4 (`:127`) is one of them and is scoped to nothing — "`status.cards[]` records no non-empty digest for `status.activeRevision`". Widening **clause 1** admits re-creations to that conjunction; it does not suspend clause 4 for them. So for a missing-policy `Lock`:

- **(b)** — clause 1 becomes "kind is `Lock`" — exempts {`ProbingAfter`, `beforeObserved` false, **`status.activeRevision` uncarded**, no clause-5 hold}.
- **(c)** — clause 1 also requires the route's revision uncarded — is (b) ∧ that.
- **(c′)** — "widen on **clause 4 alone**" — is {`ProbingAfter`, `beforeObserved` false, `status.activeRevision` uncarded, no clause-5 hold}. **That is (b), restated.** "Widen on clause 4 alone" adds nothing to a clause every candidate for the exemption must already satisfy.

Three consequences, all in the text the human decides from.

**1. The recommendation contradicts itself.** `:139`: "**(c) — or (c′) where the extra read matters — is the answer to take, and not (b)**". (c′) *is* (b). A reader who follows that sentence takes the option the same sentence forbids.

**2. (c′)'s two comparative properties are inverted.** `:138`: "…over-inclusive against (c) in exactly one state … **so the eval waits a little longer than (c) would and never runs beside a `Lock` that cannot finish**." The state named is right — the route's revision carded, `status.activeRevision` not — and that `Lock` does leave it fast: `cardedRevision(status, status.ActiveRevision)` is false so the re-point is skipped (`authtxn.go:1771`), the route therefore does not move so `routeConverged` passes (`:1788`), and `cardAttributes` attributes on the route's carded revision (`:2187-2206`), so the `401` credits and the `Lock` writes `Served`. But (c′) ⊋ (c), and exempt means the eval **claims**: in that state (c) waits and (c′) claims, so the eval waits **less**, never longer. And running beside a `Lock` that cannot finish is what both widenings exist *for*; what (c′) adds over (c) is running beside one that **can**.

**3. (b)'s cost, and the residue boundary itself, are argued against a state clause 4 already forbids.** `:139`'s (b) row and `:130` both rest on "a `Lock` whose `status.activeRevision` is carded is a reached re-point away from `Served`, and exempting it would put eval patches beside a transaction that is about to write". No option can exempt that `Lock`: clause 4 fails on it. The only `Lock` (b) exempts that finishes on its own is the one whose *route's* revision is carded — precisely and only the state (c) removes. So (b) is made to look far worse than it is, and the argument for the residue boundary names the wrong half of "outside the residue".

This is rule 7 one level up: not a property the code lacks, but a distinction the design's own rule does not draw. It is also round 2's MAJOR 2 repeating in kind — a false property of an option in the paragraphs the human chooses from — and it entered through round 2's own proposed fix, which did not check that clause 4 binds the widened set.

**Fix.** Collapse to three options; one edit with MAJOR 2.

1. Strike (c′) at `:138` and every reference (`:139`, `:841`, and the README row's "(c′) widens on clause 4 alone and avoids that").
2. Restate **(b)** truthfully: clause 4 already keeps it off a `Lock` whose `status.activeRevision` is carded, so its whole excess over (c) is the one state where the *route's* revision is carded and the active one is not — a `Lock` that credits itself out within a pass or two. Cost: that one bounded window of N4's contention; no new read; no new shape beyond clause 1's own text.
3. Restate **(c)** as what it buys over (b): it closes that window. Say why its second half is needed at all — clause 4 was written as a proxy for `cardAttributes` that holds for J2 and K2 because their route always names `status.activeRevision` (`:127` says so), and widening clause 1 to re-creations is exactly where that equivalence breaks, because A77's re-point is conditional.
4. At `:130`, replace "a `Lock` whose `status.activeRevision` is carded" with the state that can actually be exempted and still finish: one whose route's revision is carded while `status.activeRevision` is not.

---

## MAJOR 2 — (c)'s stated cost is false: the eval controller already reads `<agent>-serving`, and §1.1 says so thirty lines above

*(Found by the forked pass; re-verified here against `authforeign.go` and the design's own `:154`.)*

`docs/designs/16-evalsuite.md:136`, repeated at `:841` and `docs/designs/README.md:24`.

> `:136` "The controller that evaluates clauses 1 to 5 is the **eval** controller, which reads the Agent, the EvalSuite and, for clause 5 alone, the Gateway and two policy lists. **It reads no `HTTPRoute` today** … The read is cheap and RBAC already grants it, but **it is a new object kind on the eval path**."

It is not a new object kind, and §1.1's own enumeration of what a clause-5 pass costs says so. `:154`:

> "only when that list is not empty, **one read of `<agent>-serving`**, for its labels, through the manager's cached client (`servingRouteLabels`)."

That is real: `foreignTrafficPolicies` (`authforeign.go:37-69`) calls `servingRouteLabels` (`:107-121`), which is an `r.Get` of the `HTTPRoute`, whenever the run-namespace policy list is non-empty — and for any Agent whose `<agent>-auth` exists it never is. So the object kind, the RBAC, the client and the helper are all on the eval path today, and `:156` moves `servingRouteLabels` onto the shared receiver with the rest, "with their bodies unchanged".

What (c) actually costs is smaller and different: clause 5 is evaluated only on a pass that has met clauses 1 to 4 (`:140`), and a missing-policy `Lock` fails clause 1, so today such a pass makes **no** route read at all. (c) needs the route for clause 1 itself, which moves the same `Get`, on the same cached client, earlier — onto every pass that finds a missing-policy `Lock` recorded, including passes that would never have reached clause 5. It can be shared with clause 5's `Get`.

This matters twice over. It is the only reason (c′) was invented, and it is the only thing that makes (c) look expensive beside (b). With MAJOR 1, the corrected block is three options whose whole spread is one bounded window of contention against one cached `Get` moved earlier — a much easier question, and one where the answer may well change.

Round 2's MAJOR 2 was right that the second draft's "adds no read" was false; its proposed replacement — "a read of `<agent>-serving` that it does not make today" — overshot in the other direction, and A19 wrote the overshoot in. It should be corrected to what `:154` already says rather than reverted.

**Fix.** At `:136`, `:841` and `README:24`: strike "reads no `HTTPRoute` today" and "a new object kind on the eval path". Say that clause 5 already reads `<agent>-serving` through `servingRouteLabels` whenever the run namespace holds any policy — which it does once the `Lock` has written `<agent>-auth` — so (c) moves that read from clause 5's passes to every pass with a recorded missing-policy `Lock`, on the same cached client, and can share the `Get`. Also strike "one `HTTPRoute` read per eval pass": the read is due only on passes that find such a `Lock`, and none otherwise.

---

## MAJOR 3 — the inference the first critique's BLOCKER killed still stands in two places, one of them Q12's own decision text

*(Found by the forked pass; both sites re-read here.)*

`docs/designs/16-evalsuite.md:187` and `:496`.

> `:187` "…**a promotion to a carded revision ends it**, because the missing-policy `Lock` re-points its route at `status.activeRevision` while that revision is carded. … a held candidate stops that re-read, **cannot itself be promoted**, and so closes the one exit the re-point opened. … **It is conditional, so it narrows the wedge rather than removing it**: where neither the revision the route names nor `status.activeRevision` has a recorded card digest…"
> `:496` "**Since design 03 A77 a new revision does clear it**, once that revision is `status.activeRevision` and its card digest is recorded, because the `Lock` then re-points its route onto it … and since A77 the state also ends with nobody acting once `status.activeRevision`'s card records."

Both drop the requirement that a pass **reach** the re-point, which is the whole content of the second draft's BLOCKER fix. The re-point is at `authtxn.go:1771`, below the intact read (`:1694`), the fresh-NACK check (`:1705`) and the foreign check (`:1740`). `:187` is worse than a loose sentence: its statement of *what A77 left conditional* names only the card condition, so the bullet that is §1.1's summary of the boundary carries the pre-fix account in full. §14 `:830` lists this bullet among the ones A19 rewrote, and the trailing tag says "Rewritten by A19" — so this is A19's own text, not preserved prose.

`:187` also carries flatly the claim round 1's MAJOR 4 removed everywhere else: "a held candidate … cannot itself be promoted". `:134`, `:174`, `:177` and `:831` all carry the two paths on which it can — a verdict already `Passed` before the `Lock` began, and the outcome of the case in flight when it began where that case is the run's last (step 7).

`:496` is a decided question's text and A19's own replacement for it (the pre-A19 line read "no new revision would clear it, because it does not move the route"), and it now contradicts Q13's `:500`, which carries "but only once a pass reaches the re-point at `ProbingAfter`" correctly. Two adjacent decision bullets describe the same shipped behaviour differently.

The earlier rounds swept five enumerated sites. This is the property, not the list, and it was never on it.

**Fix.** `:187`: "a promotion to a carded revision ends it **on the first pass that reaches the re-point**, which a foreign `traffic` policy on `<agent>-serving`, a fresh NACK or a `Lock` short of `ProbingAfter` prevents"; the conditional-narrowing sentence gains the same clause; and "cannot itself be promoted" becomes "usually cannot itself be promoted — except by the two paths above". `:496`: the same qualification on both sentences, with a pointer to `:500`. Then sweep §1.1 for "re-points its route at `status.activeRevision` while that revision is carded" as a *property*, not as a list of sites.

---

## MINOR 1 — `:183`'s new justification is false for two of the three holds it offers it for

`:183`: "…or what is stopping it is a hold that **this message's own stage clause points at** and design 03's condition on the Agent names in full — `ForeignTrafficPolicy`, a NACK, or a Gateway-level policy".

The stage clause is "a missing-policy `Lock` at `ProbingAfter`". A fresh NACK rewinds to `Converging` (`authtxn.go:1705-1714`), so there the stage does point at the hold. A foreign `traffic` policy on `<agent>-serving` breaks at `:1740` with the stage left **at `ProbingAfter`** — the state the design's own restored row builds at `:558`, which says in the same breath that the message names nothing about it. A Gateway-level hold is read at `:1731-1739` and breaks no pass at all. In two of three cases the stage clause points at nothing, and the generic `EvalWaitingForAuth` carries only kind and stage.

Smaller, beside it: the reason a reader finds is not uniformly `ForeignTrafficPolicy`. For a missing-policy `Lock`, `PolicyApplyIncomplete` is `AuthPolicyMissing` — `incompleteOrder` (`:487`) ranks it first and `raiseIncomplete` (`:500-511`) appends the foreign message rather than taking its reason; `authgate_test.go:337` asserts exactly that. Only `GovernanceSkipped` carries `ForeignTrafficPolicy` (`:555`).

The rule's **output** is right, and the drafter's own correction beside it — that `status.eval.blocked` is empty here, because a re-creation fails clause 1 so clause 5 never runs — is right and was checked. Only this clause is wrong.

**Fix.** "…or what is stopping it is a hold the Agent's own `GovernanceSkipped` names and `PolicyApplyIncomplete`'s message carries beside `AuthPolicyMissing` — a foreign `traffic` policy, a NACK, or a Gateway-level policy — which the eval's message does not repeat and must not answer with an administrator's outage."

---

## MINOR 2 — the re-aimed third mutation cites the wrong design 03 sub-case, and undersells itself

`:558`: "…or move the re-point above the foreign check, which the 'route still names R' assertion catches, and which is the ordering design 03 §8.1 case 18 **(h)** pins from the other side".

The mutation itself lands, and that closes round 2's MINOR 3 properly: with the foreign policy on `<agent>-serving` under its own name the stage stays at `ProbingAfter`, every pass breaks at `:1740`, and moving `:1751-1787` above it re-points onto a carded C. It compiles — a block move within one scope, with `above` and `held` already set at `:1732-1738`.

But case 18 (h) (`03-policy-compiler.md:1389`) pins the route **gate** against A75's Gateway read. It says in passing that "(c) pins the re-point against the foreign check", and (c) does not: `TestNoBackendMovesWhileThePolicyIsNotThisAgents` (`authgate_test.go:305-344`) holds the `Lock` at `ApplyingPolicies` and asserts that stage, so no pass in it enters the `ProbingAfter` arm, and the narrow move is invisible to it. (c)'s own mutation is the enlarged "re-point before the stage loop", which is the parenthetical A19 already has. **No shipped design 03 sub-case reaches this ordering**, which makes `:558` its only pin — worth claiming rather than deferring.

**Fix.** Strike the (h) clause: "…which design 03 §8.1 case 18 (c) pins only in its enlarged form — (c) holds at `ApplyingPolicies` and never enters the arm — so this row is the only pin on the narrow move." The wrong attribution inside design 03's own (h) bullet deserves a separate note to design 03.

---

## MINOR 3 — `:179` and `:500` enumerate the wrong foreign policy, and omit the one the owed row builds

*(Found by the forked pass; both sites re-read here.)*

`:179` ("Leaving it standing") and `:500` (Q13) both say the apart state survives "whenever no pass [reaches the re-point] — **a foreign `traffic` policy at `<agent>-auth`**, a fresh NACK, or a `Lock` short of `ProbingAfter`".

A policy stored *at* `<agent>-auth` fails the intact read (`authPolicyIntact:2496-2512`) and rewinds to `ApplyingPolicies` — it **is** the third item, not a fourth. The distinct fourth way, and the one `:558`'s main build now turns on, is a foreign `traffic` policy **on `<agent>-serving` under its own name**, which leaves the stage at `ProbingAfter` and breaks at `:1740`. `:178` ("Not exits") and `:834` both get this right; `:179` — the sentence the owed outage row exists to cover — names the mechanism `:558` explicitly rejects as "case 18 (c)'s state and not this one".

**Fix.** In both: "a foreign `traffic` policy on `<agent>-serving`, which breaks the pass at the foreign check with the stage at `ProbingAfter`; a fresh NACK; or a `Lock` short of `ProbingAfter`, which one stored at `<agent>-auth` produces."

---

## MINOR 4 — the Status line's generalisation is now false for the last two rows of the table it points at

*(Found by the forked pass; verified at `:3` and `:860-861`.)*

`:3`: "Its eighth critique passed A10, and **every critique since has been answered by the next amendment**, which is critiqued in turn. §14's table at the end is that record."

The table now ends with two rows answered by "A19, revised in place" and "A19, revised in place again". Revising in place is the right call and `:843` gives the right reason — but this is the sentence `AGENTS.md` and `CLAUDE.md` tell every reader to trust over any summary, and it now describes a process the table it points at contradicts.

**Fix.** "…answered by the next amendment, or, for an amendment that has not merged, by a revision of that amendment in place (§14, A19)."

---

## On the escalation of clause 1, for the human

**The framing is not right yet, and neither defect is verbal: four options are put and two of them are the same rule (MAJOR 1), and the cost that separates the remaining two is overstated (MAJOR 2).**

Everything else in the block is true and was checked. (a)'s four endings hold, correctly qualified. The "second direction" answer is right and correctly declines to argue either option from itself. `beforeRevision` really is set only in `ProbingBefore` (`authtxn.go:1673-1675`), which a re-creation never runs, so the route's revision genuinely is not in `status`.

Corrected, the question is three options with a very small spread:

- **(a) Leave it.** Fail-closed. Costs liveness on a gated Agent in the residue until one of four documented endings.
- **(b) Widen clause 1 to every missing-policy `Lock`.** Clause 4 keeps it off a `Lock` whose `status.activeRevision` is carded, so its entire excess over (c) is the single state where the route's revision is carded and the active one is not — a `Lock` that `cardAttributes` credits within a pass or two. One bounded window of the contention N4 named. No new read.
- **(c) Widen only in the residue.** Closes that window, by reading `<agent>-serving` at clause 1 — the same object, on the same cached client, that clause 5 already reads through `servingRouteLabels`, moved earlier onto passes that do not reach clause 5.

**My recommendation is (a)**, on the grounds both earlier critics gave and which survive the corrections: what a widening buys is the right to evaluate a candidate on an Agent that is paged and `Degraded`, and every other ending exists and is documented. If the human wants the liveness, the corrected costs make the two widenings nearly indistinguishable — (b) is free and costs one bounded window; (c) closes the window for a read that is nearly already made — and **either is defensible, where the current text says "not (b)"**. That reversal is why MAJOR 1 and MAJOR 2 are MAJORs and not notes: as written, the block argues the human away from an option on a cost clause 4 makes impossible, and toward one on a read `:154` shows is largely already there.

---

## Where the third draft is right, and should not be redone

- **Round 2's MAJOR 1 is closed at the row, and the row is now buildable.** I rebuilt the main build pass by pass. `reconcileServed` is unreachable while a transaction is recorded (`authtxn.go:702-720`), the `reCreate` branch never calls `ensureServingRoute` at the top (`:1606-1617`), and `agent_controller.go` writes no route outside `reconcileGateway` — so nothing but the re-point can move it. With `<agent>-auth` present and this Agent's the intact read passes; no NACK; the stage is not `Converging`; A75's read finds nothing; and `len(out.foreign) > 0` breaks at `:1740` with the stage left at `ProbingAfter`. `status.activeRevision` is C and C is carded, so `missingPolicyRouteNote` returns early at `:1971-1974` and names no exits and no revert — the row's assertion exactly. Clause 1 fails on a re-creation so clause 5 never runs and `blocked` stays empty, and `reportForeign` (`:534-556`) is what names the policy.
- **All five of the row's mutations compile and die.** Main build: "name the exits whenever the route's revision is uncarded" dies on the no-exits assertion; "move the re-point above the foreign check" dies on "still names R". Variant: "re-point unconditionally" dies on "left as found"; "compute the message from `status.activeRevision`" and "name `status.activeRevision` in the revert" die on "names R" and "the revert names R".
- **Moving the message assertions to the variant is the right repair, not a retreat.** The variant reaches `ProbingAfter` with C uncarded — the `Converging` tuple breaks at `:1716-1724` before any re-point, the promotion lands in that window, `recordCardAttempt` clears C's digest in place (`card.go:412-419`) — so the route is left as found and `missingPolicyRouteNote`'s default arm names both exits with the revert pointed at R. That is the state `:183` describes.
- **Round 2's MINOR 4 is closed exactly.** `lockServed`'s `reCreate` arm prints the digest's `fetchedAt` (`authtxn.go:2121-2124`), which `cardAttributes` returns (`:2196-2200`). `status.cards[]` is appended in order and `pruneCards` (`card.go:431-443`) preserves it, so R precedes C and the pre-A77 "first entry with a non-empty digest" mutation prints R's `fetchedAt` where the assertion demands C's. It compiles as a deletion of the backend comparison.
- **Round 2's MINOR 7 is closed better than it was asked.** Rather than relabelling the `Lock`'s own policy as case 18 (c) does, the row states the shape `foreignTrafficPolicies` requires and puts the policy under a name of its own — which is also what re-aims MINOR 3's mutation. One edit closed two findings.
- **MINOR 5 and MINOR 6 of round 2 are closed to the asks** (`:179`'s reach clause, `:312`'s two endings), and `:312` carries more than was asked.
- **The repaired justification's output is right.** Under a foreign policy the administrator's delete is worse than useless (the re-created route keeps the name, so `Create` cannot publish either) and a revert does not help because the pass still breaks at `:1740`; the same under a NACK; and a `Lock` short of `ProbingAfter` fails the first clause anyway. So in either case the exits are the wrong remedy, and the design's stated reason — not "a carded active revision ends the state" — is the right one.
- **Clause 4's re-key** was re-checked in both directions against `cardAttributes` (name-keyed via `WorkloadName`, `:2187`) and `hasCardFor`/`cardEntry` (digest-keyed, `card.go:385`, `:405`). The asymmetry argument holds.
- **The §14 log, the critique table and the README row are accurate against the diff**, and `:841` states (c′)'s over-inclusion neutrally rather than carrying `:138`'s inverted clause.

## Noted, not counted

- `:312`'s "in either other case" is awkward beside `:183`'s "in **either** case", but both resolve to the same two cases.
- `:580` and `:558` do not say the harness must report the Gateway at the route's new generation before the credit arrives, as case 18 (b) does with `acceptRoute`. Consistent shorthand across both rows; round 2 noted it twice.
- `:172`'s wedge row gives `GovernanceSkipped` as `AuthPolicyMissing`; in the apart main build `reportForeign` sets it to `ForeignTrafficPolicy` (`:555`). The row describes the plain wedge, so this is a boundary of the row rather than an error in it.
- Round 2's noted tension at `:185`, between the design's zero-`backendRef` wording and the operator's shared `backend == ""` arm (`authtxn.go:2044-2049`), is unchanged and still a tension rather than a defect.
