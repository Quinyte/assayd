# Design 03 (policy compiler): third critique of A77, scoped to the first slice (§1.1)

- **Date**: 2026-09-16.
- **Reviewer**: an independent adversarial critique, following `.claude/skills/critique-design/SKILL.md` and `AGENTS.md`. This is the **third** critique of A77. The first returned FAIL (1 BLOCKER, 6 MAJOR, 13 MINOR); the second returned FAIL (0 BLOCKER, 4 MAJOR, 10 MINOR).
- **Target**: `docs/designs/03-policy-compiler.md`, `docs/decisions/0034-slice-auth-follows-current-spec-narrows-only-on-a-probe.md` and `docs/designs/README.md` at `design-03-lock-repoint` `e438898`. This round's revision is `f13796e..e438898`; the whole amendment is `origin/main...e438898`. Design 16's first slice was read at the current head of `design-16-first-slice` (`8d79c3c`, A18), not at the A13 head the amendment names.
- **Scope**: design 03's first slice is approved and implemented (ADR-0034 Amendment 4, slice PRs 4 and 5). A77 changes shipped behaviour, so it is held to the standard for an amendment to an approved design.
- **Method**: every clause of A77 and of ADR-0034 Amendment 6 traced into the code it lands in — `internal/controller/authtxn.go` (`reconcileGateway`, `gatewayStep`, `authStep`, `runLock`, `lockMissingPolicy`, `lockServed`, `recordServed`, `servedRecord`, `reportAboveServed`, `storedHold`, `seedStoredAbove`, `carriedNote`, `isReCreation`, `cardAttributes`, `beforeRevisionFor`, `singleBackend`, `routeConverged`, `policyConverged`, `authPolicyIntact`, `foreignAtOwnName`, `freshNack`, `foreignTrafficPolicies`), `internal/controller/httproute.go` (`servingRouteFor`, `ensureServingRoute`, `currentServingRoute`, `liveServingRoute`, `equalRoute`, `errRouteGone`, `collectRoutes`), `internal/controller/conditions.go` (`ownedTypes`, `stickyTypes`, `merge`), `internal/controller/card.go`, `internal/controller/agent_controller.go`; every §8.1 case 18 sub-case walked for buildability and for which mutations it reaches; every design 16 sentence A77 quotes re-located at that branch's **current** head; the ADR's shape compared against Amendments 1–5 and its untouched original text checked by diff. A forked `critique-design` pass was run and every assertion it made was re-derived by hand before use; two of its findings did not survive that check and are not raised (see "What I checked and did not raise"). Nothing was run against a cluster. No file in the repository was written or changed.

## Verdict: **FAIL — 0 BLOCKER, 5 MAJOR, 8 MINOR.**

The four MAJORs of the second round are closed, and three of them are closed well.

- **MAJOR 1 (ordering) is closed as asked, and the mechanism is as stated.** `03:773` now places both new steps "after all four reads and immediately before the probe", which matches `runLock`'s real order inside `case StageConverging, StageProbingAfter`: `authPolicyIntact` → `freshNack` → `gatewayAuthPolicies` (setting `held` and `out.aboveRead`) → the `out.foreign` check → probe. I verified the two code facts the justification rests on: `reportAboveServed` returns at its first line while `auth.Transaction != nil`, and `reconcileGateway` withdraws the carried seed on the way in (`carriedReason` → `unset`) and restores it only inside `if err != nil && !set`, under `case hasStored && !out.aboveRead`. So on the pass the gate breaks, `held` is already computed and the bottom of `runLock` still raises the hold. Every early-return and error path holds: `foreignAtOwnName`, a failing `ensureAuthPolicy`, a `createTarget` that does not render, a `!intact` rewind, a standing NACK and `errRouteGone` all leave before step 1; a `persistStatus` that loses on the gated pass returns `err != nil` with `out.aboveHold` set, and `reconcileGateway`'s first `switch` arm re-raises `GatewayAuthPolicy`. **But the rewind carries the same defect one pass later** (MAJOR 1 below).
- **MAJOR 2 (the terminal condition) is closed.** All four places now state both halves and they agree. I checked the claim itself: with no digest for the route's revision and none for `status.activeRevision`, a re-creation `Lock` sets `beforeObserved` never (no `ProbingBefore`) and `cardAttributes` returns false for ever, so the state is terminal; and `03:766`'s added sentence — "A route left as found on a revision that **is** carded still finishes" — is right.
- **MAJOR 3 (case 18 (b)) is closed, and the pair now works.** (b) is buildable: a failed card stub leaves r2 uncarded and a card failure does not withhold traffic (`card.go`, restated at `03:785`), so r2 still promotes. Both mutations compile and both are killed by (b): under "re-point unconditionally" the route moves to r2, so the message names r2 and says re-pointed, failing (b)'s message clause; under "`cardAttributes` returns true whenever the route carries any `backendRef`" the probe's `401` is credited on the route left on r1 and the `Lock` reaches `Served`, failing (b)'s outcome clause. And the survivals check out: (a) survives mutation 1 (r2 carded, condition satisfied either way) and mutation 2 (`cardAttributes` is genuinely true there); (c) never reaches the step; (d) is (a)'s build; (e) has no route; (f)/(g) take the non-re-creation branch. (a) kills "never re-point" — that is the shipped code, and with it the route stays on the uncarded r1 and never credits.
- **MAJOR 4 (the ADR) is closed.** Amendment 6 now attributes the re-point and one cost to the human on 2026-09-15 and puts the placement, the probe precondition, the digest condition, the terminal message and three drafter's calls under "Not the human's", each dated 2026-09-16 with its finding. That is Amendment 1's own shape ("Two clauses in D2 were the author's calls, not the human's… The decision text above is not edited"), the header follows Amendments 1–5's `## Amendment N (date, what)` form, the "Rejected" bullet that rejected the first draft's placement is gone, and the diff confirms the original Decision, Consequences and Amendments 1–5 are untouched: the only changes to that file across the whole amendment are the Status line and an appended Amendment 6.

Nine of the ten minors are closed, and I verified the two the questions single out: `servedRecord` does write `Mode: apikey` for every credited `Lock`, so (g)'s "no recorded mode **when it starts**" is now correct; and `03:353`'s scale sentence is accurate — the added read really is `ensureServingRoute`'s own `Get`, the `get` at the top of `runLock` really does happen every pass, and the owned-route list really does run only when both re-point conditions hold.

What is left is five things, and the first is the amendment's own remedy defeated by its own rewind.

---

## MAJOR 1 — the gate rewinds to `Converging`, and every pass after the gated one drops a standing A75 hold

`03:775`:

> "While it does not, **no probe is sent and no answer is taken**, and the transaction returns to `Converging`… Because it sits after A75's read and the foreign check, a pass it breaks has already read the Gateway, so a Gateway-level hold is still raised on that pass and `Ready` is still withheld for it — putting the gate earlier would clear a standing hold on every gated pass, since A75's report for a served Agent returns at once while a transaction is recorded."

Every word of that is true **of the pass that breaks**. It is false of the passes that follow, and the rewind the same rule introduces is what makes them follow.

Trace a J2 or K2 `Lock` under a standing Gateway-level counting policy whose route moves on a promotion:

- **Pass N** (`ProbingAfter`). Gateway read → `held = true`, `out.aboveRead = true` → foreign check → step 1 → step 2 fails → stage written `Converging` → the bottom of `runLock` takes `else if held` and sets `PolicyApplyIncomplete = GatewayAuthPolicy` with `withholdInOrder`. As specified.
- **Pass N+1**. The stored stage is now `Converging`. `seedStoredAbove` asks `storedHold`, which requires `tx.Stage == StageProbingAfter` — the comment above it says so in words ("beside a `Create` or `Lock` still at `ProbingAfter`… A transaction that has left `ProbingAfter` credited a `401` and has no hold to carry"), and after A77 a transaction can leave `ProbingAfter` having credited nothing. So nothing is carried. `runLock` enters the `StageConverging` arm, `routeConverged` fails at the new generation and `break steps` fires **before** `above = r.gatewayAuthPolicies(...)`. `held` is false and `out.aboveRead` is false. `reconcileGateway`'s restore is gated on `err != nil`, and the pass returns nil. `reportAboveServed` returns at once because `auth.Transaction != nil`. `CondPolicyApplyIncomplete` is owned and deliberately **not** sticky (`conditions.go`, `ownedTypes` / `stickyTypes`), so `merge` drops it.

So for every pass between the re-point and the Gateway's report, the Agent loses `PolicyApplyIncomplete=GatewayAuthPolicy` and stops withholding `Ready`, carrying only `GovernanceSkipped=AuthLockPending`, which design 10 never pages on (`10:50`). That is precisely the regression the quoted sentence says the ordering prevents, displaced by one pass — and the ordering argument cannot reach it, because it reasons only about within-pass placement.

Two consequences:

1. **`03:799` goes false.** "One held by a Gateway-level policy pages about 5 minutes after the hold is first seen, because A75 raises `PolicyApplyIncomplete=GatewayAuthPolicy` and withholds `Ready` at once." Design 10's alert keys on the condition type being open for 5 minutes. A condition that clears on the gated-and-rewound passes restarts that clock. In the state A77 itself names at `03:787` — "an Agent promoting faster than the Gateway reports never probes" — the transaction may not return to `ProbingAfter` at all, and the A75 hold is then never re-raised. The page survives only because the 4-minute deadline eventually forces `AuthLockUnverified` on every pass; so the honest description is that A77 converts A75's "at once, pages at 5 minutes" into "at the deadline, pages at about 9", under a different reason, and `Ready` flaps in between.
2. **A missing policy's `Lock` is masked and J2's and K2's are not.** The `reCreate` branch sets `PolicyApplyIncomplete=AuthPolicyMissing` unconditionally, so the hold's absence is invisible there. The two transactions that ship are the two that lose it.

This is not the second critique's MAJOR 1 restated: that one was about a gate placed above the read, which the drafter fixed. This is the rewind, which is new text in this round and which `03:775` calls "the consequence of the gate".

**Fix.** Two honest choices, and the design should name one. Either hold at `ProbingAfter` and put the unmet route condition in the message, so `storedHold` keeps working unchanged; or keep the rewind and say that `storedHold` must extend to a `Lock` at `Converging` carrying a stored `GatewayAuthPolicy` hold, correcting that function's stated premise. Then owe a case 18 (h): a J2 `Lock` under a standing Gateway-level counting policy whose route moves, asserting `PolicyApplyIncomplete=GatewayAuthPolicy` and a withheld `Ready` on the gated pass **and on the pass after it**, with two mutations — move the gate above the Gateway read, and leave the stage-rewind/`storedHold` pair as it stands. That also supplies the test the ordering claim currently lacks: (d) pins the gate against the re-point and (c) pins the re-point against the foreign check, but nothing pins the gate against A75's read, and the whole ordering is prose with no mutation behind it.

---

## MAJOR 2 — §1.1, the approval boundary, is untouched, and now carries a sentence A77 makes false

The diff of the whole amendment touches lines 3, 353, 624, 766, 773–780, 785, 787, 796–797, 1316, 1319, 1359, 1379 and 1419+. **Nothing in §1.1 (lines 22–86).** The "Body changed" list confirms it by omission.

`03:41`, under "**A J2 `Lock` needs no ordering against its edit's weight shift**":

> "`Lock` neither waits for that weight shift nor holds it, because the per-Agent `-auth` targets the route and so covers every revision the route serves at once."

After A77 it waits. The weight shift is the route's one `backendRef` moving, which changes `spec` and bumps `metadata.generation`; the gate then takes no probe answer until the Gateway reports at that generation. "nor holds it" is still true (`authHoldsPromotion` holds for I1 alone), but the first half is exactly the behaviour A77 introduces and `03:787` describes as a cost. The same paragraph's closing rule — "a promotion during the `Lock` changes the backend that can answer the probe, so it clears `beforeObserved`, and the `401` must then be attributed on the new revision's own digest" — is now incomplete in the same way: it must also wait for the Gateway.

Beside that, §1.1's "In the slice" table names neither the gate nor the re-point, while `03:1359` says **"Case 18 is owed and unwritten"** directly under "**The first slice (§1.1) owes these tests. Until they pass, nothing in the slice is done.**" So the approval boundary and the owed-test list disagree about what is inside the slice, and a reader asking "what did the human approve, and what did A77 add to it" finds neither rule where §1.1 sends them.

A75 is the precedent for how this is done here: its §11 entry lists "the sentences it made false in the Status line, §3.1, §3.2, §3.3.1, §3.3.3, §5 and §8.1" and corrects each. A77's list is careful and claims accuracy — and stops one section short.

**Fix.** Rewrite `03:41` ("`Lock` does not hold the weight shift, and after A77 it waits for the Gateway to report on it before it takes a probe answer"), extend the paragraph's closing rule, and add one §1.1 table row naming both rules with who decided each — the re-point the human's of 2026-09-15, the gate the drafter's of 2026-09-16 — so §1.1 and §8.1 agree on the slice's contents.

---

## MAJOR 3 — the gate is given a dataplane predicate, and its two residuals are stated nowhere

`03:785`:

> "**A77 qualifies both branches**: no answer is taken at all until the Gateway reports on the route at its current generation, so neither branch can credit a `401` measured against **a route the dataplane has not been told about**."

`Accepted` and `ResolvedRefs` at the current generation are written by the controller from its own translation. §3.3.2 says so in the section this sentence cites, and A77 restates the prohibition two bullets away at `03:787` ("saying otherwise would be the inference §3.3.2 forbids"). A route the *controller* has accepted at generation 2 may still be served by a proxy on generation 1. So the gate's own residual is the original defect in miniature, and it is unstated: the probe's anonymous `401` is then produced by the previous, possibly uncarded, revision's backend and is credited to `<agent>-auth` against the **new** revision's card digest, which is the exact shape the reproduction measured on `main`. The gate narrows the window from "the whole pass" to "the dataplane's lag behind the controller's report"; it does not close it.

A second, smaller leg. `03:775` claims the gate "holds after a lost or conflicted status write, after a restart, and **when someone other than the operator moved the route**". The first two hold — I re-checked them and they do. The third rests on the manager's cache: the gate and the attribution both read the route through `ensureServingRoute`'s `r.Get` or `currentServingRoute`'s, and when the cache is behind an out-of-band move, `equalRoute` holds against the stale object, so the gate compares a stale generation with its matching stale status, passes, and `cardAttributes` then reads the stale `backendRef`. This repo already draws that line — `liveServingRoute` exists because "a decision whose wrong answer publishes or abandons a route cannot rest on the informer cache" — and this decision's wrong answer records a one-way `apikey`. `03:624`'s own `What it is not` paragraph promises that the residue is "named where it bites — §3.3.3's one-pass window, and the same section's `ProbingAfter` gate"; at the gate it is not named, it is denied. Reachability is limited (admission reserves `<agent>-serving` to the operator and `admission.extraOperators`, design 07 A6.10/A6.15), but the clause is an absolute the read cannot support — rule 7, and the same shape the first critique's BLOCKER 1 removed.

**Fix.** At `03:785`, say control plane: "until the assayd Gateway reports on the route at its current generation". Add the residual beside it, in the section's own idiom: the gate closes the window between the route write and the Gateway's report, and leaves the window between that report and the proxy taking the new backend, where a still-served previous revision's own `401` can be credited — stated, not closed. At `03:775`, either read the route for the gate and the attribution through the uncached reader, or drop the third clause and name the stale-cache residue there, as `03:624` already promises it will be.

---

## MAJOR 4 — the two critiques A77 answers are cited five times and exist in no commit

`03:3`, `03:1419`, the README row, and ADR-0034's Status line and Amendment 6 all cite `reviews/03-a77-critique.md` and `reviews/03-a77-recritique.md`. Neither is in `docs/designs/reviews/`. I swept every `reviews/*.md` citation in `docs/`: 75 review files, and these two are the only dangling ones in the corpus — the single other miss is the illustrative string inside `docs/agent-protocol.md` itself.

`docs/agent-protocol.md:42`: "**Findings are files.** A review lands in `docs/designs/reviews/` and is committed… A finding delivered only as a message is lost the moment a session ends." `AGENTS.md` tells a reviewer to read prior reviews in that directory.

The cost is specific to this amendment rather than generic. §11 is now a catalogue of *(BLOCKER 1.)*, *(MAJOR 3.)*, *(Second critique, MAJOR 4.)* tags against documents nobody can open; ADR-0034 Amendment 6's "Not the human's" list — the answer to the second critique's MAJOR 4, and the one piece of this change that is about the record rather than the behaviour — cites those two files as its authority for what was found and when. A later reader cannot tell an answered finding from a relabelled one, and the next critic cannot check that a closure is a closure. Every other amendment in this chain, A52 through A76 and L1, has its reviews committed.

**Fix.** Commit both files. If they cannot be committed, delete all five citations and record the gap as a file, the way `reviews/README-review-availability.md` records a missing cross-family review — that precedent exists for exactly this.

---

## MAJOR 5 — §3.3.3's two new steps and its narrowed attribution carry no "not implemented" tag, and one of them describes shipped code in the past tense

`03:3` ends: "**It is not implemented**, and the sentences it adds say so where they stand."

Four of A77's body additions keep that promise — `03:766`, `03:787`, the deadline table's missing-policy row, and §5's row all carry the tag. The passages that carry the amendment's substance do not:

- `03:773`–`03:775`, the two new `ProbingAfter` steps: no tag.
- `03:777`, why `Create` needs no gate: no tag.
- `03:779`, the narrowed attribution: tagged *(Narrowed by A77. **The rule read** every rule and every ref and returned on the first recorded digest…)* — past tense, no "not implemented". `cardAttributes` still walks every rule and every ref and returns on the first recorded digest; `singleBackend` is used by `ProbingBefore` only.

§3.3.3's `Lock` is approved-slice text. An implementer or a code reviewer reading it gets the narrowed rule as the rule, with a parenthetical that reads as the record of a completed change — and `AGENTS.md` says code that diverges from an approved design is drift, so the natural next move is a drift finding against `cardAttributes`, or a "fix" to code the human never approved. `03:779` is also the one passage where the old behaviour is written as past; every other A77 parenthetical in this document uses the present ("`runLock` takes the route as it finds it", "design 03's message does neither today").

**Fix.** Three parentheticals: tag `03:773`–`03:775` and `03:777` as A77's and not implemented, and rewrite `03:779`'s to the present — "*(Narrowed by A77, and **not implemented**: `cardAttributes` reads every rule and every ref and returns on the first recorded digest…)*". Or drop the Status line's "and the sentences it adds say so where they stand", which is the weaker fix and leaves the past tense wrong.

---

## MINOR findings

1. **`03:787`'s "That is accepted" names no accepting party**, in the amendment whose second-critique MAJOR 4 was about exactly this hygiene. The gate's cost — "a `Lock` that cannot finish while the route keeps moving" — is a liveness cost on **shipped** behaviour, and ADR-0034 Amendment 6 puts only cost 1 to the human and lists the precondition under "Not the human's". Either say "the drafter accepts this; it is not among the costs the human took", or add it to what the human is asked. It cannot be left in the passive.
2. **The re-point under a standing A75 hold is the right call, and its cost is understated.** The three reasons at `03:774` hold: the hold is about which policy could answer the probe, `held` refuses the credit either way, and freezing the route would re-create for the hold's duration the exact rule-8 defect A77 exists to close — `status.activeRevision` naming a revision that receives nothing. What the amendment does not say is that A75's hold is *unbounded by construction* (its own accepted cost, and §5's row says so for the administrator's exit), so under a Gateway-level policy that W1 says "could widen or bypass the route's authentication" the operator goes on moving each new revision behind that route indefinitely, with no `401` ever credited. `03:787` bounds the exposure by "while the `Lock` is short of `Served`" and names only a NACK as what extends it. Name the hold there too.
3. **Two statements about design 16's branch are stale or false**, and one of them matters. `03:1439` says the head "is at A13 as this is written" — it is at A18 — and that "the approval commit is not pushed, so the branch head still reads 'not approved'". It is pushed: that branch's Status line and §1.1 heading both read "approved for implementation, by the human on 2026-09-14 (ADR-0024 Amendment 1)", and that ADR amendment is on `main`. The consequence is not cosmetic: A77 makes roughly eight sentences of an **approved** §1.1 false, not of a draft's. I re-located every sentence A77's damage list quotes at the current head and all of them are still there, so the list itself is accurate — only its framing is wrong.
4. **The `singleBackend` damage list misses a third place.** A77 names two design 16 bullets its narrowing breaks. A third, in the exemption subsection, states the crediting rule as "`status.cards[]` records a digest for a revision that **one of the route's `backendRefs`** names (`cardAttributes`)" — the same plural reading, attributed to the same function, and it is the sentence that sets up the exemption A77 already says needs revisiting. Add it.
5. **The follow-up amendment design 16 owes is recorded only in design 03's provenance.** The Status line says §11 is provenance and the body above is authoritative; nothing on design 16's branch, and nothing in `docs/designs/README.md`'s design-16 row, records the debt. That branch can merge without it. Either put a line in the README's design-16 row, or say in §9 that the debt is design 03's to chase.
6. **§11's A77 header still dates the amendment "2026-09-15, the human's decision"** while, by the ADR's own correction and the Status line's, most of its content is the drafter's calls of 2026-09-16. `03:3` and the ADR both say so; the provenance header — the place a later reader lands from a tag — does not.
7. **The rewrite of `Lock`'s `ProbingAfter` bullet dropped a shipped sentence.** On `main` that bullet read "the same anonymous request, each bounded by the 5 s card timeout and sent every 5 s, until one gets a `401` that can be attributed". The new `03:773` says only "immediately before the probe" and never says what the probe is or how often it goes; "until one is attributable" survives at `03:779`, and the 5 s cadence survives only in the deadline bullet's back-off clause and in A71's provenance. A `Lock` of a missing policy skips `ProbingBefore`, so its reader gets the request's shape from nowhere in the stage list. Put the clause back.
8. **`03:773` counts the foreign check as one of "all four reads".** `out.foreign` is listed live once per pass in `reconcileGateway`, before `authStep`; what `ProbingAfter` does with it is a check of an already-read list. Three reads and a check. The ordering claim is unaffected, but the list is what an implementer and case 18 (h) will be built from.

---

## The questions, answered

**(1) Ordering.** The mechanism is as stated and I re-derived both code facts; see the verdict. The new order preserves A75's hold on the gated pass, on every early-return and error path I could walk. It does **not** preserve it on the passes after the rewind — MAJOR 1.

**(2) The terminal condition.** All four places agree and the claim is true. `03:766`'s "Both halves are required", the deadline row's "either", §5's "and none for", and the ADR's "and" are one statement; and the added sentence about a route left as found on a carded revision is correct against `cardAttributes` and against `beforeObserved` never being set for a re-creation.

**(3) Pinning.** (b) is buildable, both mutations compile, and (b) kills both — the first on the message's "left as found" / r1 clause, the second on the `Served` outcome. (a) kills "never re-point", which is the shipped code. Mutation 1 is survived by (a), (c), (d), (e), (f) and (g), as claimed, and mutation 2 is survived by (a). The pair now fixes the digest condition from both sides, which is what the second critique asked for.

**(4) ADR-0034 Amendment 6.** The attribution is right, the shape is Amendments 1–5's, and the original text is untouched — verified by diff across the whole amendment: the only changes to that file are the Status line and an appended Amendment 6. One residue: the gate's cost is neither the human's nor explicitly the drafter's (MINOR 1).

**(5) The ten minors.** Closed: 1 (one tag on the "Safe because" bullet), 2 (`servedRecord` writes `Mode: apikey`, and (g) now says "no recorded mode when it starts"), 3 (the narrowing is carried into the deadline table's J2 row and both §5 rows), 4 (qualified to the write, with the clearing named in §11 and described in the body), 5 (the route-keeps-moving cost is stated, though it names no accepting party — MINOR 1), 6 (the two design 16 bullets are listed, though a third is missing — MINOR 4), 7 (the "R's name" row is scoped to one reading), 8 (§3.2's scale sentence is now accurate on both counts I checked), 9 (`Create`'s exemption is in the body and both halves check out: a prepared route's `Spec.Rules[0].BackendRefs` is nil whatever the revision, and a promotion rewrites only the revision label and the digest annotation, which do not move `metadata.generation`), 10 (the four reads are named ahead of the list, modulo MINOR 8).

**(6) The judgement call.** Sound, and I endorse it too — see MINOR 2 for the reasoning I could not break and for the cost it does not state. Adversarially, the strongest thing against it is not the exposure but MAJOR 1: under a standing hold the re-point is the event that produces the rewind, and the rewind is what drops the hold's own report. So the two interact, and the fix for MAJOR 1 is what makes the judgement safe to keep.

**(7) Anything new, and rules 7 and 8.** New this round: the rewind's effect on `storedHold` (MAJOR 1), §1.1 left behind (MAJOR 2), `03:785`'s dataplane predicate and the unstated gate residuals (MAJOR 3), the untagged new steps and the past-tense narrowing parenthetical (MAJOR 5), and MINORs 1, 2, 7 and 8, all of which are text this round added or rewrote. MAJOR 4 is not new but has never been raised. **Rule 7**: three unenforced bounds — "a route the dataplane has not been told about", "when someone other than the operator moved the route", and the Status line's "the sentences it adds say so where they stand". **Rule 8**: the terminal-versus-transient message is right and is now stated in both authoritative places with the correct condition; the rule-8 problem that remains is MAJOR 1's, where an Agent A75 makes `Degraded` reads `Ready` for the gated-and-rewound passes and the page keyed on the hold never reaches five minutes.

---

## Where the work is right

Three things in this round are better than they had to be.

The ordering answer is not a hedge. The drafter did not merely move the gate below A75's read; they wrote down *why*, naming `reportAboveServed`'s early return and `reconcileGateway`'s withdraw-and-restore, and both are exactly as described in the code. That is the reasoning that made MAJOR 1 findable at all — a vaguer sentence would have hidden the rewind behind it.

Case 18 (a) and (b) are now real mutation design rather than decoration. The pair fixes a two-sided condition with two mutations that each side survives, and the choice to assert the deadline *message* rather than the `Served` outcome is the only assertion an unconditional re-point cannot satisfy. Getting there required retitling a sub-case and admitting that its previous mutation was never reached.

And ADR-0034 Amendment 6 does the unglamorous thing: it gives back four items it had claimed for the human, dates them, cites the finding for each, and leaves the human's decision as the one sentence it was. This corpus has an amendment whose whole purpose was to re-attribute two clauses to a design's author; doing it in the other direction, unprompted by anything but a critique, is the same discipline.

The "Body changed" list is accurate this round — I checked every hunk against it, and every changed passage is listed and nothing unchanged is claimed. The design 16 damage list went from a summary to an inventory and every quote in it still holds at that branch's current head.

The gap to a PASS is a clause each for MAJOR 2, 3 and 5, two files or a recorded gap for MAJOR 4, and one real decision for MAJOR 1: hold at `ProbingAfter`, or extend `storedHold` and own the change to A75's premise.
