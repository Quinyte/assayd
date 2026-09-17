# Fourth critique: design 16 A19, fourth draft — the A77/A78 follow-up to the approved first slice (§1.1)

- **Date**: 2026-09-16
- **Target**: `docs/designs/16-evalsuite.md` §1.1 and amendment A19, and the design 16 row of `docs/designs/README.md`, at `2fd356d` (branch `design-16-a77-followup`). Read as `git diff 38577e4 2fd356d` (this round) and `git diff origin/main...2fd356d` (the whole amendment), and whole in the head.
- **Independence**: a fourth, independent session, plus one forked pass whose findings are re-verified by hand below and **disagreed with on grading in four places**, said so explicitly. Neither wrote A3–A19, the thirteen earlier critiques of §1.1, `reviews/16-a19-critique.md`, `reviews/16-a19-recritique.md`, `reviews/16-a19-critique3.md`, or design 03 A77 or A78. Every behavioural claim below was traced to the tree at `2fd356d`; nothing rests on a prior critique's account of the code.
- **Scope**: the three MAJORs and four minors the fourth draft claims to close; the three options as they now read, each cost reproduced against the code; the swept property, swept again as a property; and anything the fourth draft introduces. Nothing in this slice is implemented, so nothing was run; the design 03 behaviour A19 describes **is** implemented and was read against its source and its envtest cases.
- **Read against**:
  - `internal/controller/authtxn.go`: `runLock`'s entry and its `reCreate` branch (`:1593-1641`), its `ProbingBefore` arm (`:1662-1680`), its `ApplyingPolicies` arm and `foreignAtOwnName` (`:1681-1692`, `:958-975`), and the `StageConverging, StageProbingAfter` arm in order — intact read (`:1694`) → fresh NACK (`:1705`) → the `Converging` tuple (`:1716-1730`) → A75's Gateway read (`:1731-1739`) → the foreign check (`:1740-1750`) → the re-point (`:1751-1787`) → the route gate (`:1788-1826`) → probe (`:1827`) → `cardAttributes` (`:1839`); the `reCreate` condition set (`:1897-1914`); `lockMissingPolicy` (`:1564-1574`); `incompleteOrder`/`raiseIncomplete` (`:487-511`); `reportForeign` (`:524-560`); `reportAboveServed` (`:354-386`); `singleBackend` (`:2137`); `cardAttributes` (`:2187`); `cardedRevision` (`:2213`); `authPolicyIntact` (`:2496`); `routeConverged` (`:2518`).
  - `internal/controller/authforeign.go`: `foreignTrafficPolicies` (`:37-69`), `listPolicies` (`:80-100`) and `servingRouteLabels` (`:107-121`).
  - `internal/controller/agent_controller.go`: the absence of any route write outside `reconcileGateway`.
  - `test/envtest/authgate_test.go`: `TestNoBackendMovesWhileThePolicyIsNotThisAgents` (`:305-344`).
  - `docs/designs/03-policy-compiler.md` §8.1 case 18 (a)–(h) (`:1381-1389`) and §11 A77/A78.
- **Method**: the option block composed against the exemption's own conjunction rather than the prose describing it; **each option's cost traced to the function that would make the read, and to §1.1's own "What clause 5 costs" (`:150-153`)**; the killed inference swept by property across §1.1, §14 and the decided questions, and the sweep checked at every site the grep returns rather than at the sites the log lists; round 3's four minors each re-read at both of the sites round 3 named; and design 03 §8.1 case 18 (h)'s attribution tested against case 18 (c)'s own text and its envtest.

**Verdict: FAIL — 0 BLOCKER, 1 MAJOR, 5 MINOR.**

Round 3's MAJOR 1 and MAJOR 3 are closed at the root, and closed well. `(c′)` is gone from §1.1, §14 and the README; `:131` now argues the boundary against the state clause 4 permits and not the one it forbids; the killed inference is swept from `:173`, `:177`, `:178`, `:186` and Q12's decided text at `:495`, and `:186` loses "cannot itself be promoted" for the qualified form. MAJOR 2's correction is accurate against `authforeign.go:107` and against `:153`. Round 3's MINOR 1, MINOR 2 and MINOR 4 are closed, two of them better than asked.

What remains is one MAJOR and five one-sentence findings. **The MAJOR is round 3's MAJOR 2 with the options swapped**: the cost correction was written into (c) and not carried across to (b), so the block now bills (c) for one *cached* `Get` and tells the human that (b) is "free" and makes "**No new read**" — when what either widening actually turns on, for a class that pays none today, is clause 5, whose three *uncached* API-server reads §1.1 itemises fifteen lines below. Two of the minors are round 3 findings applied at one of their two sites, one of them recorded in §14 as applied at both.

---

## MAJOR 1 — (b)'s "No new read" is false, and the read either widening actually turns on is clause 5's, which §1.1 bills fifteen lines below

`docs/designs/16-evalsuite.md:135`, with `:136`, `:137`, `:150-153`, the §14 copy at `:842`, and the design 16 row of `docs/designs/README.md`.

`:135` gives (b)'s cost as "one bounded window of the contention N4 named and A12 bounded … **No new read, and no new shape beyond clause 1's own text.**" `:137` restates it for the human: "**(b) is free** and costs one bounded window of contention; (c) closes that window for a cached `Get` that clause 5 largely already makes."

(b) is not free, and `:136` — the very next bullet, corrected this round — says why:

> `:136` "clause 5 runs only on a pass that has met clauses 1 to 4, and a missing-policy `Lock` fails clause 1 **today**, so **no such pass reads the route at all**".

That sentence is right, and its consequence is larger than the route read it is about. Clause 5 is what a widening switches **on**. Today a missing-policy `Lock` fails clause 1, so for that Agent the eval controller evaluates no clause 5 and makes **none** of its reads. `:150-153` itemises what one clause-5 pass costs:

- one `get` of the Gateway, and a paged `list` of `AgentgatewayPolicy` in the **Gateway's** namespace;
- a paged `list` of `AgentgatewayPolicy` in the **run** namespace;
- only when that list is not empty, one cached `Get` of `<agent>-serving`.

The first three go through `r.reader()`, the **uncached** reader, so each reaches the API server — `:155` says so, and `listPolicies` (`authforeign.go:80-100`) and `gatewayAuthPolicies` both take it. Only the fourth is the cached `r.Get` in `servingRouteLabels` (`authforeign.go:107-121`).

So the marginal read cost of **(b) over (a)** is three uncached API-server reads per clause-5 pass, where today there are zero. The marginal cost of **(c) over (b)** is the fourth read, cached, moved earlier. The block states the second and denies the first. `:150` gives the rate: a waiting pass comes round at least once per `AuthProbeInterval`, 5 s, "and more often on an Agent event, so these reads run at about that rate for each exempt `Lock`".

Three things follow, all in the paragraphs the human decides from.

**1. The stated ordering of the options by cost is inverted.** As written, (a) and (b) are both free and (c) costs a read. In fact (a) is free, (b) costs three uncached reads per pass, and (c) costs those plus one cached one. The read the block bills is the cheapest of the four.

**2. "No new read" is the same error round 3 corrected, unscoped.** Round 3's MAJOR 2 killed "(c) reads no `HTTPRoute` today / a new object kind on the eval path" by holding it against `:153`. (b)'s "No new read" has to be held against `:150-153` in exactly the same way, and was not. `:135` even sets up the contradiction itself — "clauses 2 to 5 bind as they do today" — and then denies the read that binding costs.

**3. It is not unbounded, and the block should say that too.** I do **not** accept the forked pass's "indefinitely". The exemption only matters while a candidate is pending; with a candidate held on a final verdict the pass rate drops to `CardDriftInterval`, 5 minutes (`:115`), and where the eval runs to a `Passed` verdict the promotion ends the wedge and the reads with it. So the true shape is: three uncached reads at about 5 s for the length of a run, then at 5 minutes while a candidate is held — the same order as the cost §1.1 already accepts for an exempt J2 or K2 `Lock`. That is a defensible cost. It is not "free", and it is not (c)'s alone.

This matters to the decision in one direction and not the other: it makes both widenings look cheaper than they are beside (a), which is the option the block recommends. It does **not** change the (b)-versus-(c) ordering, because the three reads are common to both.

**Fix**, one paragraph.

1. At `:135`, strike "No new read". Say that (b) is what turns clause 5 on for a missing-policy `Lock` — one uncached Gateway `get` and two uncached paged policy lists per clause-5 pass, per `:150-153`, where today there are none — at about 5 s while a run is live and 5 minutes while a candidate is held, and that this is (b)'s cost and (c)'s alike.
2. At `:136`, say (c) adds the cached `Get` **on top of** that, and keep the rest of the bullet, which is correct.
3. At `:137`, replace "(b) is free" with the corrected spread: both widenings pay clause 5's uncached reads for as long as a candidate is pending; (c) adds one cached `Get` and closes (b)'s one contention window.
4. Mirror at `:842` and in the README row, both of which carry the same two claims.

---

## MINOR 1 — round 3's MINOR 3 is applied at `:178` and not at Q13, and §14 records it applied at both

`docs/designs/16-evalsuite.md:499`, against `:178`; the record at `:837`.

`:178` is corrected exactly as asked: "a foreign `traffic` policy **on `<agent>-serving`**, which breaks the pass at the foreign check with the stage left at `ProbingAfter`; a fresh NACK; or a `Lock` short of `ProbingAfter`, which one stored **at** `<agent>-auth` produces by failing the intact read".

`:499` — Q13's "Leaving it standing" — is byte-identical to the third draft (`git show 38577e4:docs/designs/16-evalsuite.md | sed -n '500p'`) and still reads "the outage survives wherever none does — **a foreign `traffic` policy at `<agent>-auth`**, a fresh NACK, or a `Lock` short of `ProbingAfter`". This round's `@@ -493,7 +492,7 @@` hunk touched Q12's decided line only.

The defect is round 3's, re-verified here: a policy stored *at* `<agent>-auth` fails `authPolicyIntact` (`:2496-2512`, digest inequality) and rewinds to `ApplyingPolicies`, where `foreignAtOwnName` (`:1682`) breaks the pass — it **is** the third item. The distinct fourth way, which `:178`, `:173`, `:177` and `:186` all now name and which `:557`'s owed row is built on, is a foreign policy on `<agent>-serving` under its own name, the only one that leaves the stage at `ProbingAfter`.

What makes this worth a finding rather than a note is `:837`: "'Leaving it standing' **and Q13** both listed the foreign policy as one 'at `<agent>-auth`' … and omitted the `<agent>-serving` one the owed row turns on." The log records a correction that was made at one of the two sites. A reader auditing §14 finds it closed and never looks.

I disagree with the forked pass's grading of this as MAJOR. The substance is a one-clause enumeration in a parenthetical whose main statement — "only once a pass reaches the re-point at `ProbingAfter`" — is correct, and round 3 graded the identical defect MINOR at both sites. The false closure record is the aggravating fact, and it is fixed by the same edit.

**Fix.** Apply `:178`'s wording at `:499`, and it is closed.

---

## MINOR 2 — (c)'s residue has no truth value for a route not carrying exactly one `backendRef`, and that makes `:135`'s "one state" reading-dependent

`:136`, with `:135`, `:131`, `:842` and the README row.

(c) is "neither **the revision the route's single `backendRef` names** nor `status.activeRevision` carded". A route that does not carry exactly one `backendRef` names no such revision (`singleBackend`, `:2137`, returns `""` for `len(Rules) != 1 || len(BackendRefs) != 1`), so the first conjunct has no truth value there.

§1.1 knows that state and writes for it twice — `:119` ("a route that does not carry exactly one `backendRef` names no revision and is unattributable, whatever `status.cards[]` records") and `:182`, whose own residue test uses the explicit disjunction: "the route does not carry exactly one `backendRef`, **or** the revision its single `backendRef` names has no recorded card digest". (c) drops that disjunct.

Two readings, and the text picks neither:

- **"the route names no carded revision"** — the reading `:182` uses. Then the unattributable route is in the residue, (b) and (c) both exempt it, and `:135`'s "its whole excess over (c) is **one state**" holds.
- **"there is a single `backendRef` and its revision is uncarded"** — then (b) exempts the unattributable route and (c) does not, which is a **second** excess state, and one where (c) is strictly worse: such a `Lock` can never credit at all (`cardAttributes` returns unattributable whatever `status.cards[]` holds), so it is the strongest case for exempting.

`:119` says neither slice's emitter writes a second `backendRef`, and admission reserves `httproutes` writes in a run namespace, so the state is out of reach inside the slice — which is why this is a MINOR and not the MAJOR the forked pass graded it. But it is a rule being put to a human, and it is undefined on a state the same section writes a message arm for.

**Fix.** At `:136`, use `:182`'s disjunction verbatim: "the route does not carry exactly one `backendRef`, or the revision its single `backendRef` names is uncarded; and `status.cards[]` records none for `status.activeRevision`". `:135`'s "one state" then holds as written.

---

## MINOR 3 — (a)'s third ending cannot arise unaided in the state (a)'s cost describes, and `:173` says so in A19's own words

`:134`, against `:173`.

`:134`: "on a gated Agent in the residue no candidate is evaluated until one of four things happens — an administrator deletes `<agent>-serving`; the spec is reverted to the revision the route names; **`status.activeRevision`'s own card records and a pass reaches the re-point**; or a candidate promotes on a verdict that was already `Passed` …"

The state presupposes a candidate. `:173`, rewritten this round, states the consequence: "The operator retries that fetch only while `status.activeRevision` is the **desired** revision … **A pending candidate is the desired revision instead, so nothing is fetching the active revision's card then**" (`cardFetchDue`, design 03 A60). So in the state `:134` describes, ending 3's condition cannot come true without someone making the active revision desired again — which in the non-apart case *is* ending 2, and in the apart case is a third owner action the list does not name.

I disagree with the forked pass, which graded this MAJOR on the ground that ending 3 is ending 2 restated. `:134` lists **conditions**, each of which is genuinely sufficient, and does not claim any of them arises unaided; the row that does make that claim, `:173`, carries the qualification in full. What is wrong is only that `:137` leans on "(a)'s four endings, which all exist and are documented" without it, so the human counts four self-executing endings where the residue-with-a-candidate state has one (ending 4).

**Fix.** One clause at `:134`: "…`status.activeRevision`'s own card records and a pass reaches the re-point — which needs that revision to be desired again, since a pending candidate is the desired revision instead (the row above)".

---

## MINOR 4 — `:137` calls the route "open", which `:170` bolds that it is not

`:137`: "…on an Agent that is already paged and `Degraded`, **whose route is open and enforcing but unproven**".

`:170`: "So the policy is in place and accepted, and an anonymous probe gets `401`. **The route is not open.** What is missing is proof that the `401` is the policy's."

"Open" is a term of art in this corpus, and it means unauthenticated — design 03's own missing-policy message says "its route served with no key" of the state *before* the `Lock` writes the policy. At `ProbingAfter` the policy is written, converged and enforcing, which is exactly why `:170` bolds the denial. The sentence the recommendation rests on says the opposite of the table row thirty lines above it. The phrase is preserved from the third draft and no round has flagged it; it survives into the fourth because the sweeps have been of inferences, not of vocabulary.

**Fix.** "whose route is enforcing but unproven".

---

## MINOR 5 — §14's table header still carries the generalisation the Status line was corrected for

`:845`: "**The critiques of §1.1, each answered by the next amendment.** This is the record the Status line points at."

Round 3's MINOR 4 is closed at `:3`, correctly and in the asked words. But `:845` is the same sentence, three lines above a table whose last three rows read "A19, revised in place", "…again" and "…a third time", and it is the header of the record the corrected Status line points at. This is round 3's own lesson applied one line short: the property, not the site.

**Fix.** "The critiques of §1.1, each answered by the next amendment, or by a revision in place of an amendment that has not merged."

---

## On the three options, for the human

**The block is nearly fit, and one paragraph short of it.** Three options, no duplicate, no inverted comparative, and a recommendation that no longer forbids what it recommends. I reproduced every cost:

- **(a) Leave clause 1.** Fail-closed; no eval patch ever lands beside a transaction's writes. Its endings are the administrator's delete, the revert to the route's revision, `status.activeRevision`'s card recording with a pass reaching the re-point, and a promotion on a verdict written before the `Lock` began or by the case in flight when it did. All four are true conditions; only the last arises unaided in the state described (MINOR 3). **Marginal reads: none.**
- **(b) Clause 1 becomes "`transaction.kind` is `Lock`".** Clause 4 keeps it off any `Lock` whose `status.activeRevision` is carded — I checked this is a property of the conjunction and not of the prose (`:119` "the state in which all five of these hold", `:129` "While all five hold"), and clause 4 (`:126`) is scoped to nothing. So (b)'s excess over (c) is the one state where the **route's** revision is carded and the active one is not. I traced that state pass by pass: `cardedRevision(status, status.ActiveRevision)` is false, so the re-point at `:1771` is skipped; the route therefore does not move, so `routeConverged` at `:1811` passes; the probe runs; `cardAttributes` (`:2187`) matches the route's single `backendRef` against the carded entry by `WorkloadName` and attributes. The `Lock` credits and reaches `Served` within a pass or two — the design's claim, and it is right. **Marginal reads: clause 5's three uncached ones, which the block denies (MAJOR 1).**
- **(c) Widen only in the residue.** Closes that window by reading the route at clause 1. The "not a new object kind" correction is accurate: `foreignTrafficPolicies` (`authforeign.go:37-69`) calls `servingRouteLabels` (`:107-121`) whenever the run-namespace list is non-empty, and that is an `r.Get` of the `HTTPRoute` through the manager's cached client — and at `ProbingAfter` the `Lock` has written `<agent>-auth`, so the list is never empty. The "why the second half is needed" argument is also right and is the sharpest sentence in the block: clause 4 is a proxy for `cardAttributes` that holds for J2 and K2 because their route always names `status.activeRevision`, and A77's re-point is conditional, so the equivalence breaks exactly where clause 1 is widened to re-creations. **Marginal reads: (b)'s, plus one cached `Get`.** Its residue needs `:182`'s disjunction (MINOR 2).

**Is it fit to put to a human? Not yet, and the gap is one paragraph.** The human is choosing on cost, and the block currently tells them one option is free when it is not, and bills the cheapest of four reads against the option that is. Correct `:135`, `:136`, `:137`, `:842` and the README row as MAJOR 1 sets out, apply the four one-sentence minors, and it is fit — and the corrected spread is still small enough that the recommendation survives it, because the three uncached reads are common to both widenings and the ordering between them does not move.

**On the recommendation itself.** I reach (a) independently, as the three earlier critics did, and on the same ground: what a widening buys is the right to evaluate a candidate on an Agent that is paged and `Degraded`, and every other ending exists. One thing the deciding paragraph does not say, and should: in the residue the promotion a widening enables is the exit that **ends the wedge with nobody acting** — `:131`, `:173` and `:186` all establish it, Q10's fourth promotion condition cards the promoted revision, `cardedRevision` is then true and the re-point fires. `:137` calls that "liveness for a promotion nobody should be making until the page is cleared", which is a coherent reading of Q12's own rationale and which I do not call false — but a human reading `:137` alone sees the cost of widening and not the whole of its benefit. I disagree with the forked pass, which graded this a MAJOR on the ground that the premise is refuted by `:173`; it is not refuted, it is incomplete, and the argument is present two bullets above. One clause at `:137` closes it.

---

## A fresh finding against merged text, confirmed: design 03 §8.1 case 18 (h)'s attribution

The drafter found this and left it for design 03's process, and `:557` states the correct version. **Confirmed.**

`docs/designs/03-policy-compiler.md:1389`, case 18 (h), last sentence: "This is the sub-case that pins the gate against A75's read, which is otherwise prose: (d) pins the gate against the re-point and **(c) pins the re-point against the foreign check**, and nothing else reaches this ordering."

Case 18 (c) (`:1385`) builds a policy at `<agent>-auth` carrying another Agent's UID, and asserts "The `Lock` holds at `ApplyingPolicies`". `TestNoBackendMovesWhileThePolicyIsNotThisAgents` (`test/envtest/authgate_test.go:305-344`) asserts `tx.Stage != "ApplyingPolicies"` as a failure. Traced: with a foreign policy at that name, `authPolicyIntact` (`:2496`) returns `intact` false at the **top** of the `StageConverging, StageProbingAfter` arm (`:1694`), the stage rewinds and `continue`s to the `ApplyingPolicies` arm, and `foreignAtOwnName` (`:1682`) breaks the pass. Nothing below the intact read is reached, so the `ProbingAfter` foreign check (`:1740`) and the re-point (`:1751`) are both invisible to (c) — moving the one above the other leaves (c) passing. (c)'s own stated mutation, "re-point before the stage loop", is the enlarged move that hoists the re-point out of the arm entirely, which (c) does kill, because the route would then move before the `ApplyingPolicies` break.

So (c) pins the re-point against the **stage loop**, not against the `ProbingAfter` foreign check, and — the part that actually bites — **(h)'s "nothing else reaches this ordering" is what fails**: nothing in design 03's shipped suite pins the narrow ordering at all. `:557`'s claim that design 16's owed row is its only pin is therefore correct, and worth the note it now makes.

The only reading on which (h)'s sentence survives is that "the foreign check" means `foreignAtOwnName` at `ApplyingPolicies` rather than the `len(out.foreign) > 0` check at `ProbingAfter` — but (h)'s sentence is a chain describing the `ProbingAfter` arm's internal order (gate / A75 read / re-point), so that reading makes the chain incoherent. Either way the coverage claim is wrong.

This is design 03's to fix, in design 03's process, and it should be raised there rather than left to be found by whoever next reads (h) for its coverage.

---

## Where the fourth draft is right, and should not be redone

- **Round 3's MAJOR 1 is closed at the root, not patched.** `(c′)` is gone from `:133-137`, from `:842` and from the README row (grep returns one occurrence in the whole corpus, the withdrawal note at `:133`). `:131` now argues the boundary against "the **route's** revision carded while `status.activeRevision` is not", which I verified is the state a widened clause 1 can exempt and that still finishes, and the premise "the exemption is a conjunction of five clauses, and widening clause 1 suspends none of the other four" is exactly right against `:119`, `:126` and `:129`.
- **MAJOR 2's correction is accurate and correctly sourced.** `foreignTrafficPolicies` returns early on an empty list and otherwise calls `servingRouteLabels`, which is `r.Get` — cached, unlike the `r.reader()` lists beside it. "Not a new object kind on the eval path" holds, and `:136`'s statement of what (c) actually changes — *when* the read is made — is the right correction. The error is that the same accounting was not carried to (b) (MAJOR 1).
- **MAJOR 3 is closed by property, and I re-swept it independently.** `:173`, `:177`, `:178`, `:186` and Q12's `:495` all now require a pass to **reach** the re-point; `:186` loses "cannot itself be promoted" for "usually … except by the two paths"; Q12's text now matches Q13's `:499` instead of contradicting it. The three mechanisms named are the right three against `runLock`'s order — a fresh NACK (`:1705`), a foreign policy on `<agent>-serving` (`:1740`, stage left at `ProbingAfter`), and a stage short of `ProbingAfter`. A Gateway-level hold is correctly **excluded**: `held` is set at `:1734` and breaks nothing above the re-point. My own grep for the property found one uncorrected site, `:499`, which is MINOR 1; `:830`, `:505`, `:502` and `:834` were each read and are each either correct or preserved question text.
- **Round 3's MINOR 1 is closed better than asked.** `:182` now attributes the hold to "`GovernanceSkipped`… and `PolicyApplyIncomplete`'s message beside `AuthPolicyMissing`, which outranks it (`incompleteOrder`)" and adds the sentence that kills the old claim outright: "Only a NACK moves the stage; a foreign policy and a Gateway-level hold leave it at `ProbingAfter`, so the stage clause does not point at them." I checked all three: `reportForeign` (`:555`) sets `GovernanceSkipped=ForeignTrafficPolicy` and `raiseIncomplete`'s default arm appends it after `AuthPolicyMissing` (rank 0 beats rank 1); a NACK's reason is unranked, so `incompleteOrder`'s comment puts it in the message; and a Gateway-level hold reaches the `reCreate` message through `unmet` and `missingPolicyRouteNote`'s `held` argument, since `reportAboveServed` returns early while a transaction is recorded.
- **Round 3's MINOR 2 is closed better than asked.** `:557` no longer cites case 18 (h), says what (h) actually pins, says why case 18 (c) cannot reach the narrow ordering, and claims the row as its only pin — which the section above confirms.
- **Round 3's MINOR 4 is closed at `:3` in the asked words**, and the new Status wording describes the process the table shows. Only the table's own header was left (MINOR 5).
- **The §14 log, the critique table and the README row are accurate against the diff**, including the new row, the "three critiques" header at `:828`, and the README's "case 18 (c) measures the counterexample", which is true: in (c) `status.activeRevision` is r2 and carded, the route still names r1, and the apart state stands because no pass reaches the re-point.
- **`beforeObserved` really is false for every re-creation**: `lockMissingPolicy` (`:1568`) enters at `ApplyingPolicies`, and `tx.BeforeObserved` is set only in the `ProbingBefore` arm (`:1673-1675`), which that kind never runs. So clause 3 is satisfied trivially by the class both widenings admit, and `:136`'s "`beforeRevision` is `ProbingBefore`'s, and a missing-policy `Lock` never runs that stage" is right.

## Noted, not counted

- `:136`'s "and the two can share it" sits against `:155`, which commits to moving `foreignTrafficPolicies` and `gatewayAuthPolicies` onto a shared receiver "with their bodies unchanged". Sharing the `Get` needs `servingRouteLabels` to hand back the route rather than its labels, i.e. a body change. `:155` describes what is owed under (a) and (c) is a hypothetical rule change, so this is a tension rather than a contradiction — but if (c) is taken, `:155` needs the carve-out.
- (c) decides clause 1 from the **cached** route, while `runLock` reads the same route live through `currentServingRoute`. So (c) closes (b)'s window only to the freshness of the manager's cache. `:170` and design 03 §3.3.2 are careful about exactly this staleness elsewhere; the option bullet is silent. It does not change the ordering of the options.
- Under either widening the exemption's heading and `:121`'s table caption still say "a **J2 or K2** `Lock`". Cosmetic until a widening is taken, and A19 takes none.
- Under either widening, clauses 1 and 3 do no work for a missing-policy `Lock` — it never runs `ProbingBefore` — so the conjunction reduces to clauses 2, 4 and 5. `:135`'s "clauses 2 to 5 bind as they do today" is true and slightly flattering.
- Round 2's noted tension at `:185`, between the design's zero-`backendRef` wording and the operator's shared `backend == ""` arm (`authtxn.go:2044-2049`), is unchanged and still a tension rather than a defect.
- Four rounds have each found a defect in the same four paragraphs. That is a signal about the form as well as the content: the option block would survive as a five-column table — option, exempted set, marginal reads, contention, what it ends — and every finding above, this round's MAJOR included, is a cell in it.

**16 A19 (fourth draft): REVISE.**
