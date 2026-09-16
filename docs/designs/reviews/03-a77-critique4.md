# Design 03 (policy compiler): fourth critique of A77, scoped to the first slice (§1.1)

- **Date**: 2026-09-16.
- **Reviewer**: an independent adversarial critique, following `.claude/skills/critique-design/SKILL.md` and `AGENTS.md`. This is the **fourth** critique of A77. The first returned FAIL (1 BLOCKER, 6 MAJOR, 13 MINOR); the second FAIL (0 BLOCKER, 4 MAJOR, 10 MINOR); the third FAIL (0 BLOCKER, 5 MAJOR, 8 MINOR).
- **Target**: `docs/designs/03-policy-compiler.md`, `docs/decisions/0034-slice-auth-follows-current-spec-narrows-only-on-a-probe.md` and `docs/designs/README.md` at `design-03-lock-repoint` `92b5420`. This round's revision is `e438898..92b5420`; the whole amendment is `origin/main...92b5420`. **Design 16's first slice is now merged**: `origin/main` is at `7ad39bf` (PR #40), and `origin/main:docs/designs/16-evalsuite.md` is byte-identical to the branch head the third critique read, so every design 16 quote was re-located on `main`.
- **Scope**: design 03's first slice is approved and implemented (ADR-0034 Amendment 4, slice PRs 4 and 5). A77 changes shipped behaviour, so it is held to the standard for an amendment to an approved design.
- **Method**: every clause of this round's revision traced into the code it lands in — `internal/controller/authtxn.go` (`runLock`, `reconcileGateway`, `gatewayStep`, `reportAboveServed`, `storedHold`, `seedStoredAbove`, `carriedReason`, `carried`, `routeConverged`, `policyConverged`, `authPolicyIntact`, `cardAttributes`, `singleBackend`, `beforeRevisionFor`, `lockPendingMessage`, `lockServed`, `servedRecord`, `isReCreation`), `internal/controller/httproute.go` (`ensureServingRoute`, `currentServingRoute`, `liveServingRoute`, `equalRoute`), `internal/controller/conditions.go` (`ownedTypes`, `stickyTypes`), `card.go`, `agent_controller.go` (`SetupWithManager`'s route watch). Case 18 (h) walked for buildability against the existing envtest harness (`test/envtest/authabove_test.go`'s `heldJ2`, `gatewayAuthPolicy`, `removePolicy`; `authlock_test.go`'s `lockPass`; `authcreate_test.go`'s `acceptRoute`), and both its mutations walked by hand against `runLock` and `reconcileGateway`. A forked `critique-design` pass was run and every assertion it made was re-derived by hand before use. Nothing was run against a cluster. No file in the repository was written or changed.

## Verdict: **FAIL — 1 BLOCKER, 4 MAJOR, 7 MINOR.**

**The decision this round had to make is the right one, and it is correct in the code.** I did not take the prose for it. `storedHold` requires `tx.Stage == StageProbingAfter`; holding there means the `case StageConverging, StageProbingAfter` arm re-enters at `ProbingAfter` on every pass, skips the tuple branch, reaches `above = r.gatewayAuthPolicies(...)`, recomputes `held` and sets `out.aboveRead`, and the bottom of `runLock`'s `else if held` re-raises `PolicyApplyIncomplete=GatewayAuthPolicy` with `withholdInOrder`. `CondPolicyApplyIncomplete` is in `ownedTypes` and not in `stickyTypes`, so the rewind really would have dropped it. `03:801`'s "A77's route gate does not interrupt that clock" is true as written. **The third critique's MAJOR 1 is answered correctly**, and MAJORs 3, 4 and 5 are closed:

- **MAJOR 3 is closed well.** "A route the dataplane has not been told about" is gone; `03:788` now says the gate is "a control-plane fact and nothing more — §3.3.2's own limit" and states both residuals, the proxy's lag and the cached route read, as residuals rather than as absences. I checked the second: `currentServingRoute` and `ensureServingRoute` both use `r.Get`, while `liveServingRoute` uses `r.reader()`, so "a live read would remove it, at one more `get` per pass" is exactly right. The false third clause at `03:776` is withdrawn.
- **MAJOR 4 is closed.** All three review files are in `docs/designs/reviews/`, and every `reviews/03-a77-*` citation in `docs/` resolves. They carry no absolute paths and no personal names.
- **MAJOR 5 is closed.** `03:775`, `03:776`–`777`, `03:779` and `03:781` all carry the not-implemented tag, and `03:781`'s parenthetical is in the present tense and accurate: `cardAttributes` does still read every rule and every ref.
- **Case 18 (h) is sound, and buildable.** `TestAJ2LockHoldsWhileAGatewayLevelAuthPolicyStands` already builds a J2 `Lock` parked at `ProbingAfter` under a Gateway-level counting policy with `Ready=False` and `Degraded`; (h) adds a promotion and withholds `acceptRoute`. Both mutations compile and both are killed. Mutation 1 (gate above the Gateway read) leaves `out.aboveRead` false on a clean pass, `reconcileGateway` unsets the carried seed on the way in and restores it only under `err != nil && !set`, and `reportAboveServed` returns at its first line while `auth.Transaction != nil` — so the hold is gone on the gated pass. Mutation 2 (rewind) leaves the stored stage `Converging`, `storedHold` returns false, and the `StageConverging` arm breaks on `routeConverged` before the A75 read — so the hold is gone on the pass after, and (h)'s `stage` assertion kills it outright.
- **The deadline message works through `unmet`, as the drafter says.** The gate sets `unmet` from `routeConverged`'s `why`, which names the route, its current generation, the condition and the generation the Gateway reported for; `unmet` is threaded into `lockPendingMessage` and into the missing-policy message together with `tx.Stage`, which now reads `ProbingAfter`. No new field and no stage rename are needed.
- **The ADR is the best-behaved artefact in the change.** Amendment 6 attributes one decision and one cost to the human, dates and cites every drafter's call, adds the liveness cost under "Not the human's" as the third critique's MINOR 1 asked, and leaves Amendments 1–5 and the original Decision untouched.

What is not done is the **edit**. The rewind A77 removed this round is still specified in four places, two of them the owed tests, and the same shape — a correction appended, the corrected sentence left standing — recurs three more times. That pattern, not any one clause, is what this round did not sweep.

---

## BLOCKER 1 — the rewind A77 just removed is still specified in four places, two of them owed test sub-cases, and case 18 is now unsatisfiable

`03:777`, `03:801`, `03:1389` and ADR-0034 Amendment 6 say the transaction **stays at `ProbingAfter`**. Four sites still say it rewinds:

- **`03:789`, the authoritative body**, in §3.3.3's "Safe because the route was already open" bullet: "it converts a false credit into a `Lock` that cannot finish while the route keeps moving, **because each promotion rewinds the transaction** and an Agent promoting faster than the Gateway reports never probes." This is the sentence A77 edited *this round* — it appended "**The drafter accepts that, and it is not among the costs the human took**" to close the third critique's MINOR 1 — without deleting the mechanism clause the same round removed. Two bullets above it, `03:777` forbids exactly this.
- **`03:1425`, §11's statement of the rule**: "Until they hold, no probe is sent and no answer is taken, **and the transaction returns to `Converging`** so the message names the unmet stage" — followed, six clauses later in the same bullet, by "**The transaction stays at `ProbingAfter`** while the gate holds it."
- **`03:1385`, case 18 (d)**: "The pass that re-points takes no probe answer — the stub records zero hits after the write — and **the transaction reads `Converging`**." Byte-identical to `e438898`.
- **`03:1387`, case 18 (f)**: "under A77 it takes no answer, **reads `Converging`**, and reaches `Served` only after the Gateway reports at the new generation." Byte-identical to `e438898`.

The test consequence is the hard one. `03:1389` (h) asserts `status.auth.transaction.stage` is **still `ProbingAfter`**, names as its second mutation "**rewind the transaction to `Converging` on a gated pass**", and claims "both killed here and **survived by (a)–(g)**". (d) and (f) do not merely survive that mutation — they **require** it. Under the mutation they pass; under the behaviour A77 now specifies they fail. So as the text stands, (d) and (f) can only be green when (h) is red, and (h) can only be green when (d) and (f) are red. An implementer building case 18 to the text gets a suite that cannot pass, and the cheapest way out is the one two sub-cases already point at — make the stage read `Converging` — which reinstates the third critique's MAJOR 1 verbatim, on shipped J2 and K2 `Lock`s.

I confirmed (a), (b), (c), (e) and (g) genuinely survive both mutations, so (h)'s survival claim holds for five of the seven; it is false for the two that were not swept.

**Fix.** Four deletions and one re-check. At `03:789`, drop "because each promotion rewinds the transaction" — the cost is real, but its cause is the gate holding the pass short of the probe, not a rewind. At `03:1425`, drop "and the transaction returns to `Converging` so the message names the unmet stage"; the sentence that follows already says the opposite and says why. In (d) and (f), replace "the transaction reads `Converging`" with "the transaction stays at `ProbingAfter`", keeping (d)'s stub-hit-count assertion, which is what those two sub-cases actually pin. Then re-state (h)'s survival claim against the corrected (d) and (f).

---

## MAJOR 1 — §1.1's bolded lead is now the negation of its own paragraph, and the out-of-slice table keeps the withdrawn reason

`03:41` opens **"A J2 `Lock` needs no ordering against its edit's weight shift."** The paragraph under it now says: "It is not free for what the probe proves, and **after A77 the `Lock` does wait on the shift** before it takes a probe answer… That is why the table below can leave 'the ordering of auth transactions around a weight shift' out of the slice without leaving J2 undefined: **the slice has one ordering rule, and it is the gate**."

The third critique's MAJOR 2 asked for `03:41` to be rewritten. The body sentence and the closing rule were rewritten, correctly and with the right parenthetical. The bold lead was not, and it is now false — A77 adds precisely an ordering against the weight shift. This is §1.1, the approval boundary, and the bold leads are what a reader scans.

`03:63`, the out-of-slice table, carries the same residue in its reason column: "The one mode change in the slice, J2's `Lock`, **needs no ordering**: it does not hold its edit's weight shift". The conclusion (E2 and F2's ordering rules stay out) survives, but on `03:41`'s *new* justification, not this one. Two sentences in the same section now give opposite reasons for the same table row.

The new slice-table row at `03:49` is good and is the thing the third critique's MAJOR 2 most needed: it names both rules, attributes the re-point to the human's decision of 2026-09-15 and the gate to the drafter's call of 2026-09-16 — which matches ADR-0034 Amendment 6 exactly — and says neither is implemented and that §8.1 case 18 owes their tests. §1.1 and §8.1 now agree on the slice's contents.

**Fix.** Retitle `03:41` to something like "**A J2 `Lock` does not hold its edit's weight shift, but after A77 it waits on it**", and replace `03:63`'s "needs no ordering" reason with the one `03:41` now gives.

---

## MAJOR 2 — design 16's first slice merged to `main` eighteen minutes before this commit, and A77 calls it unmerged three times, in a README row it added this round

`origin/main` is at `7ad39bf`, "docs(designs): design 16 first slice — narrow eval gate (approved by the human) (#40)", committed 2026-09-16 05:41. `92b5420` is 05:59. The slice is merged; `origin/main:docs/designs/16-evalsuite.md` is 833 lines, carries the approved §1.1, and is byte-identical to the branch head the third critique read — so **the damage list itself is accurate on `main`**, every quote included, and I re-located all of them there. What is false is the framing around it:

- **`03:1434`**: "That slice is on the **unmerged branch** `design-16-first-slice`".
- **`03:1447`**: "**No cross-reference is added to the body.** Design 16's first slice is unmerged, so a §3.3.3 or §5 pointer to a section number on a branch would be the moved cross-reference write-spec refuses. The citation stays here, in provenance, **until that branch lands**." The branch has landed, so the stated reason for keeping the reference out of the body has expired.
- **`docs/designs/README.md:24`, added this round**: "Its first slice, on the **unmerged** `design-16-first-slice` (PR #40)…". This row is also built on the pre-merge base: it is the old design-16 row (`r2 — critique PASS … awaiting user approval, ADR-0024`) with A77's sentence appended, while `main`'s row now records the twelve critiques A3–A17 and the human's approval. Merging this branch replaces a true, detailed row with a shorter one that still says "awaiting user approval". `docs/designs/README.md` is the table CLAUDE.md says arbitrates.

This is the same finding the third critique raised as its MINOR 3 in its earlier form — the drafter took the "approved" half and not the "merged" half, which only became true after that critique was written, and 18 minutes before this commit.

**Fix.** Say "merged to `main` as `7ad39bf` (PR #40)" at `03:1434`; either add the §3.3.3 or §5 cross-reference now that `03:1447`'s objection is gone, or give a different reason; and rebase the README design-16 row onto `main`'s current one rather than appending to the stale one.

---

## MAJOR 3 — the design 16 bullet corrects the "not pushed" claim and then repeats it, in the same bullet

`03:1434` opens: "**It is approved**: that branch's Status line and §1.1 heading read 'approved for implementation, by the human on 2026-09-14 (ADR-0024 Amendment 1)', and that ADR amendment is on `main`. So the sentences below are an approved §1.1's, not a draft's… *(An earlier draft of this bullet said the approval commit was unpushed and the branch still read 'not approved'; it is pushed, and it does not.)*"

The same bullet closes: "The human approved the slice on 2026-09-14; **the approval commit is not pushed, so the branch head still reads 'not approved'**."

The correction was appended and the corrected sentence was left standing — BLOCKER 1's shape again. It matters here rather than being cosmetic, because whether design 16's §1.1 is a draft or approved text on `main` is exactly what sets the weight of A77's follow-up debt, and the bullet answers that question twice, oppositely, thirty lines apart. The third critique's MINOR 3 is therefore half-closed.

**Fix.** Delete the final sentence.

---

## MAJOR 4 — the Status line and §11's header still say two critiques; the README and the ADR's own list say three

`03:3` is byte-identical to `e438898` in this sentence: "**Two critiques returned FAIL** (`reviews/03-a77-critique.md`: 1 BLOCKER, 6 MAJOR, 13 MINOR; `reviews/03-a77-recritique.md`: 0 BLOCKER, 4 MAJOR, 10 MINOR), both answered in §11", and earlier, "A77's own calls, made on 2026-09-16 in answering **two** critiques". `docs/designs/README.md:11`, updated this round, reads "**three critiques FAIL** … `reviews/03-a77-critique3.md`: 0 BLOCKER, 5 MAJOR, 8 MINOR), **all** answered in §11". §11's A77 header at `03:1422` also names two and says the re-critique "closed the BLOCKER and five of the six MAJORs", with no account of the third round — while the bullets beneath it are tagged *(Third critique, MAJOR 1.)*, *(Third critique, MAJOR 3.)*, pointing at a round the header does not say happened. ADR-0034 Amendment 6's intro says "answering **two** critiques" while its "Not the human's" paragraph, corrected this round, names three.

CLAUDE.md and `AGENTS.md` both tell a reader to check the Status line of the design and trust it over any summary. Right now it under-reports a FAIL critique and disagrees with the table that arbitrates. `03:1452`'s "Body changed" list claims the Status line among the things this round changed; the diff has no hunk at line 3.

**Fix.** Carry the third critique's citation and counts into `03:3`, `03:1422` and the ADR's Amendment 6 intro.

---

## MINOR findings

1. **`storedHold`'s premise is now load-bearing prose, and the design does not say where it stops holding.** `03:777` and `03:1425` rest the whole design on "`storedHold` carries a Gateway-level hold only for a transaction at `ProbingAfter`". That premise is intact and needs no code change — but the hold is still dropped whenever anything *else* lands the transaction at `Converging` with an unmet tuple. The reachable one is an out-of-band edit of `<agent>-auth` during the gate window: `authPolicyIntact` returns `!intact`, `runLock` rewinds to `ApplyingPolicies`, rewrites, reaches `Converging`, and `routeConverged` then fails on the just-moved generation and breaks **before** `gatewayAuthPolicies`, so `held` is false, `out.aboveRead` is false and `storedHold` carries nothing. This ships today and A77 does not create it or widen it for J2 and K2 — but the amendment now cites the premise as its safety argument, so name the residual beside it, as `03:788` does for the gate's two.
2. **The commit message's "the gate re-checks the tuple on every pass" overstates what the body says, and the body is right.** `03:776` says "§3.3.2's **route half** is re-checked", which is exact: `policyConverged` is not re-run at `ProbingAfter`, before or after A77. The companion claim, "the policy half cannot have changed, since a re-point does not touch the policy", is true of the re-point and not of the policy's ancestor status, which the gateway controller can retract without the policy changing (`policyConverged` also fails on a synthetic `StatusSummary` ancestor, written "when the policy attached to nothing"). The consequence is bounded — an unattached policy produces no `401`, so this is liveness and not a false credit, and `authPolicyIntact` still catches every spec change each pass — but the reasoning should not be carried into the design in the commit message's stronger form.
3. **Case 18 (h) does not say the Gateway's reported generation is held.** (d) says "held at the pre-re-point value by the test" and (f) says "held at the pre-promotion value"; (h) says only "whose route then moves on a promotion". Without that clause a harness that accepts the route between the two passes lets mutation 2 survive, and (h) — the only sub-case that pins the ordering at all — passes on a state that never gated.
4. **`03:1443` says "two bullets" and "both" while listing three.** The third critique's MINOR 4 was taken — the exemption subsection's crediting rule is now in the list, and I re-located it on `main` at design 16 line 119 — but the count in the lead and the "so **both** rest on a reading that no longer exists" were not updated.
5. **ADR-0034 Amendment 6 says the gated pass "ends after its four reads"** while `03:774`, corrected this round for the third critique's MINOR 8, says "three reads and one check". `03:773`'s own "after all four" is fine because the sentence before it defines the four; the ADR has no such antecedent.
6. **`03:781`: "so the gate, the re-point and the attribution all key on one object"** reads, in a sentence about `singleBackend`, as though all three used it. The gate keys on the route's `metadata.generation` against the Gateway's reported generation, and the re-point on `status.activeRevision` plus a recorded digest; only the attribution and `ProbingBefore` use `singleBackend`. One word ("one route") fixes it.
7. **`03:776` does not say which condition carries the gate's unmet reason.** "the pass ends there, naming the Gateway's silence on that generation as its unmet condition" — for a J2 or K2 `Lock` short of its deadline that is `GovernanceSkipped=AuthLockPending`'s message, which design 10 never pages on (`10:50`), and only at the deadline does it become `PolicyApplyIncomplete=AuthLockUnverified`. §3.3.3 names the carrying condition everywhere else it makes a claim of this kind, and `03:801` says so two bullets later for the Gateway-hold case; say it at the gate too.

---

## The questions, answered

**(1) MAJOR 1, and case 18 (h).** The remedy is right and I verified every code fact it rests on, including the three the third critique named. Nothing the rewind protected is lost *relative to shipped behaviour*: the `Converging` arm's `policyConverged` was never re-run at `ProbingAfter` on `main` either, so the rewind would have **added** a per-pass policy re-check rather than preserved one. The strong form of "the policy half cannot have changed" is not true (MINOR 2), but its failure mode is liveness, not a false credit, and `authPolicyIntact` runs every pass. The out-of-band policy edit path is the one where the hold still drops, through `!intact` rather than through the gate (MINOR 1). **(h) is buildable, both mutations compile, and both are killed**; (a), (b), (c), (e) and (g) survive them, and **(d) and (f) do not survive mutation 2 — they require it** (BLOCKER 1).

**(2) The deadline message.** `unmet` carries it. `routeConverged`'s `why` names the route, its current generation, the failing condition and the generation the Gateway reported for; `runLock` threads `unmet` into `lockPendingMessage` and into the missing-policy message with `tx.Stage`, now `ProbingAfter`. The route watch in `SetupWithManager` wakes the next pass when the Gateway writes the route's status, so "the next pass sends it once the Gateway reports" is real and not a requeue-interval promise.

**(3) MAJOR 2, §1.1.** `03:41`'s body and closing rule are corrected and correct; its bold lead and `03:63` are not (MAJOR 1). The new slice-table row states the attribution and the not-implemented status correctly, and matches the ADR.

**(4) MAJOR 3.** Accurate. The dataplane predicate is gone, the gate is restated as what the tuple proves, both residuals are stated in the section's own idiom and neither is claimed closed, and the "someone else moved the route" absolute is withdrawn. I re-derived the cached-read residual: `ensureServingRoute` returns `&existing` from the manager's cache on the no-drift path, and `liveServingRoute` is the uncached reader the design says the slice does not spend.

**(5) MAJOR 4.** All three files are committed, and every citation in `docs/` resolves — `03:3`, `03:1422`, `03:1424`, `03:1451`, the README row and the ADR. The only residue is that `03:3` and `03:1422` cite two of the three (MAJOR 4 above).

**(6) MAJOR 5.** Closed. All four passages carry the tag, and `03:781`'s parenthetical is present-tense and true of `cardAttributes` as it stands.

**(7) The eight minors.** Closed: 1 (`03:789` names the drafter and the ADR lists the liveness cost under "Not the human's"), 2 (the unbounded hold is stated at the re-point, beside the two reasons for taking it, and the alternative it is taken over), 4 (the third `singleBackend` sentence is in, modulo the count — MINOR 4), 5 (the debt is in the README's design-16 row, modulo "unmerged" — MAJOR 2), 6 (§11's header now separates the human's 2026-09-15 decision from the drafter's 2026-09-16 calls), 7 (the request's shape and 5 s cadence are back in the `ProbingAfter` bullet), 8 (three reads and one check). **Half-closed: 3** — design 16 is approved and the list is reframed against an approved §1.1, but the withdrawn "not pushed" sentence still stands in the same bullet (MAJOR 3), and "unmerged" is now false in three places (MAJOR 2).

**(8) Anything new, and rules 7 and 8.** New this round: the surviving rewind in the body, §11 and two owed sub-cases (BLOCKER 1); §1.1's stale lead and out-of-slice reason (MAJOR 1); design 16's merge (MAJOR 2); the un-deleted "not pushed" sentence (MAJOR 3); the critique count (MAJOR 4); and MINORs 1–7. **Rule 7**: I found no new unenforced bound — the three the third critique named are all withdrawn or qualified, and `03:789`'s "a route that stops moving converges, because `ensureServingRoute` writes only on drift" checks out against `equalRoute`'s short-circuit. What replaces them is not an over-claim but a contradiction: the design states its central new rule both ways. **Rule 8**: the terminal-versus-transient message is right and unchanged; `03:801` is now true, not merely asserted, because the hold is re-raised on every gated pass by the read the gate sits after. The gate's own silence before the deadline is the ordinary `AuthLockPending` case the section already discloses, and wants one clause naming the carrying condition (MINOR 7).

---

## Where the work is right

The hard part of this round was a judgement, not a patch: given a gate that must not credit and a hold that must not clear, either extend `storedHold`'s premise or stop rewinding. The drafter took the second, which is the one that changes no shipped function and leaves `storedHold`'s doc comment true as written — and then wrote down the mechanism in enough detail that it can be checked line by line, which is how I checked it. `03:777` is a paragraph that earns the phrase "load-bearing rather than tidy".

`03:788` is the other thing worth naming. Asked to replace a false absolute, the drafter did not substitute a weaker absolute: the gate is stated as a control-plane fact, the window it narrows is described, the two residuals are named with their reachability and with the trade not taken ("A live read would remove it, at one more `get` per pass; the slice does not take that trade, and says so rather than claiming an absolute the read cannot support"). That sentence is the corpus's standard, written out.

And committing the three review files closes the one finding that was about the record rather than the behaviour. `docs/agent-protocol.md` says findings are files; three rounds of findings are now files, and a fifth reader can check every closure claimed above against them.

The gap to a PASS is a sweep, not a decision. Every MAJOR here and the BLOCKER are the same failure: the answer was written where the answer goes, and the sentence it supersedes was left where it stood. Grep the amendment for `Converging`, for "unmerged", for "not pushed", and for "two critiques", and the round is done.
