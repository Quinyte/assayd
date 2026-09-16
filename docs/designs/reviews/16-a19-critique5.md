# Fifth critique: design 16 A19, fifth draft — the A77/A78 follow-up to the approved first slice (§1.1)

- **Date**: 2026-09-16
- **Target**: `docs/designs/16-evalsuite.md` §1.1 and amendment A19, and the design 16 row of `docs/designs/README.md`, at `ee62725` (branch `design-16-a77-followup`). Read as `git diff 2fd356d ee62725` (this round) and `git diff origin/main...ee62725` (the whole amendment), and whole in the head.
- **Independence**: a fifth, independent session. It did not write A3–A19, the twelve earlier critiques of §1.1, or `reviews/16-a19-critique.md`, `reviews/16-a19-recritique.md`, `reviews/16-a19-critique3.md`, `reviews/16-a19-critique4.md`, or design 03 A77 or A78. Every behavioural claim below was traced to the tree at `ee62725`; nothing rests on a prior critique's account of the code — including round 4's, one of whose noted items is refuted below.
- **Scope**: the option block as a table, every cell of it reproduced against the code; the two items round 4 left noted and the drafter wrote in rather than dropped; round 4's five minors at each of their sites; and anything the fifth draft introduces. Nothing in this slice is implemented, so nothing was run; the design 03 behaviour A19 describes **is** implemented and was read against its source.
- **Read against**:
  - `internal/controller/authforeign.go`: `foreignTrafficPolicies` and its early return on an empty list (`:37-69`), `listPolicies` and `policyPageSize` (`:71-101`), `servingRouteLabels` (`:103-121`).
  - `internal/controller/authabove.go`: `gatewayAuthPolicies` (`:199-247`) — the Gateway `get` at `:204` and the Gateway-namespace list at `:221`, both through `r.reader()`.
  - `internal/controller/runnamespace.go`: `reader()` (`:847-852`).
  - `internal/controller/httproute.go`: `currentServingRoute` (`:315-332`, `r.Get`) beside `liveServingRoute` (`:334-348`, `r.reader()`), and the comment that says why the two exist.
  - `internal/controller/authtxn.go`: `runLock`'s re-creation entry and its route read (`:1605-1616`), the re-point and its two conditions (`:1751-1787`), the route gate (`:1788-1826`) and its own comment on the cache, the probe and `cardAttributes` (`:1827-1840`); `reconcileServed`'s route read (`:1503`); `singleBackend` (`:2137`); `cardAttributes` (`:2187`); `cardedRevision` (`:2207`); the interval constants `AuthProbeInterval` (`:50`), with `CardRetryInterval` and `CardDriftInterval` in `internal/controller/card.go` (`:81`, `:336`).
  - `internal/controller/agent_controller.go`: the absence of any route write outside `reconcileGateway`.
- **Method**: each of the fifteen cells of the option table traced to the function that would make the read or take the state, and cross-read against §1.1's own "What clause 5 costs" (`:158-163`), "What wakes the eval controller while it waits" (`:115`) and the recommendation (`:145`); the two written-in items verified against the source rather than against round 4's account of it; round 4's five minors re-read at every site a grep returns, not at the sites the §14 log lists; the rate claims checked against the three Go constants.

**Verdict: PASS — 0 BLOCKER, 0 MAJOR, 5 MINOR.**

Round 4's MAJOR 1 is closed where it mattered. "(b) is free" and "No new read" are gone; `:133` now states the governing fact — a missing-policy `Lock` fails clause 1 today, so **nothing makes clause 5's reads for that class**, and both widenings turn them on — and the three uncached reads are named, sourced to `r.reader()`, rated and bounded in the same paragraph. All five of round 4's minors are closed, each at both of its sites. The table was the right move: every finding below is a cell or a clause, and each is one line to fix.

What remains is five one-sentence findings. None of them changes which options exist, their ordering by cost, or the recommendation. Two of them are cost statements the human would read wrongly, and they should be corrected in the same pass that puts this over — but they are edits, not a round.

---

## MINOR 1 — "They stop when the run does" is false, and the case where the reads never stop is unstated

`docs/designs/16-evalsuite.md:141`, with the §14 copy at `:859` and against `:145`, `:115` and Q2 (a).

`:141`, the paragraph directly under the table: "The rate is clause 5's own: about once per `AuthProbeInterval`, 5 s, while a run is live, and once per `CardDriftInterval`, 5 minutes, while a candidate is held on a final verdict … **They stop when the run does.**"

The sentence contradicts the clause before it. A candidate held on a final verdict is a candidate held **after** the run ended — that is what a final verdict means, and `:115` is explicit that such a pass "requeues at `CardDriftInterval` to notice a suite edit". If the reads stopped when the run stopped, the 5-minute rate the same sentence states would never be reached. §14 states it without even the intervening clause to soften it (`:859`): "about 5 s while a run is live, 5 minutes while a candidate is held, **and gone when the run ends**."

The block's own correct bound is two bullets below, at `:145`: both widenings pay clause 5's reads "**for as long as a candidate is pending**". That is right, and it is the sentence `:141` should have said.

What neither says, and what the human is choosing on, is how long a candidate can be pending. Q2 (a) — the human's own decision — holds a failed candidate at zero traffic, and `:115` requeues it at `CardDriftInterval` for ever, to notice a suite edit. On such a pass clauses 1 to 4 still hold (the `Lock` is still recorded, still at `ProbingAfter`, `beforeObserved` still false, `status.activeRevision` still uncarded), so `:148-151`'s per-pass computation of `blocked` still evaluates clause 5, and still makes its reads. So the shape is:

- **`Passed` verdict** — the promotion cards the promoted revision, the re-point fires, the wedge ends and the reads end with it. This is the case round 4 described and the case `:141` generalises from.
- **`Failed` verdict** — the candidate is held until its owner edits the spec or the suite. Clause 5 keeps running at 5 minutes, indefinitely, for that Agent. Under (a) the same Agent makes **none** of those reads, for ever, because clause 1 fails.

That unbounded tail is the cost of widening on the non-happy path of an eval gate, which is not a corner: refusing a candidate is what the gate is for. `:141`'s comparison — "the same order as the cost §1.1 already accepts for an exempt J2 or K2 `Lock`" — is right on rate and wrong on duration, and duration is the one axis on which this class differs from J2 and K2: a J2 or K2 `Lock` reaches `Served` and stops; a missing-policy `Lock` in the residue is defined by not finishing. That is the wedge.

This is rule 7's shape — a bound stated that nothing enforces — in the paragraph the human sizes the cost from.

**Fix**, one sentence at `:141`, mirrored at `:859`. Strike "They stop when the run does." Say: they run for as long as a candidate is pending (`:145`'s own bound); a `Passed` verdict ends the wedge and the reads with it, and a candidate held on a `Failed` verdict is held until its owner acts (Q2 (a)), so on that path the 5-minute rate has no bound.

---

## MINOR 2 — the fourth read is billed to (c) alone and missing from (b)'s cell, and `:133` says so eleven lines above

`:139`, against `:133`, `:135-138`, `:143`, `:144`, `:145`, the §14 copies at `:859` and `:866`, and the design 16 row of `docs/designs/README.md`.

The table's marginal-reads column reads: (a) "none"; (b) "clause 5's **three uncached** reads … per clause-5 pass"; (c) "one **cached** `Get` of `<agent>-serving`, for clause 1".

Clause 5 makes **four** reads, not three. §1.1 itemises them itself at `:158-161`, and I reproduced each:

- `gatewayAuthPolicies` (`authabove.go:204`, `:221`) — the Gateway `get` and the Gateway-namespace paged list, both `r.reader()`, uncached;
- `listPolicies` for the run namespace (`authforeign.go:83-99`), `r.reader()`, uncached, 250 per page;
- **and, whenever that list is not empty, `servingRouteLabels` (`authforeign.go:103-121`) — an `r.Get` of `<agent>-serving` through the manager's cache.** `foreignTrafficPolicies` returns early only on an empty list (`:46-48`), and at `ProbingAfter` the `Lock` has written `<agent>-auth` into the run namespace, so the list is never empty. (c)'s own bullet at `:144` says exactly this.

So the fourth read is **clause 5's**, and both widenings turn clause 5 on. It is (b)'s as much as (c)'s. (c)'s real marginal is not a new read at all: it is the same cached `Get`, made earlier, and therefore on a **wider set of passes** — every pass that finds such a `Lock` recorded, including the ones that fail clauses 2 to 4 and so never reach clause 5, where (b) reads nothing. `:144` states that correctly ("What (c) changes is **when** … (c) moves the same cached `Get` onto every pass that finds such a `Lock` recorded, and the two can share it") and the cell does not.

The block contradicts itself on this in three places, and each time the prose is right and the cost line is wrong:

- `:133`: "Both widenings turn them on, and 'What clause 5 costs' below itemises them" — four reads — beside `:139`, which bills (b) three.
- `:145`: "both pay clause 5's three uncached reads … and (c) adds one cached `Get`" — the `Get` (b) also makes.
- `:866` and the README row: "(c) adds to that the one cached `Get` of `<agent>-serving` … which is **not a new object kind on the eval path**, since clause 5 already makes it whenever the run namespace holds a policy." The subordinate clause refutes the main one inside a single sentence.

This is the same class as rounds 2, 3 and 4 — the read accounting attached to the wrong option — for the fourth time, and this time in the cheapest of the four reads. It does not move the ordering: (a) < (b) ≤ (c) holds either way, and the fourth read never reaches the API server. The table's own note at `:141` says it exists "because prose kept losing one of the four", and `:133` says it is a table "so that an omission reads as an empty cell". The omission is not an empty cell; it is a cell short by one read.

**Fix**, two cells.

- (b): "clause 5's four reads — one Gateway `get` and two paged policy lists, all through the uncached `r.reader()`, and, since the run namespace then holds `<agent>-auth`, one cached `Get` of `<agent>-serving` (`servingRouteLabels`) — per clause-5 pass".
- (c): "no new read. The same cached `Get`, moved to clause 1, so made on every pass that finds such a `Lock` recorded, including those that fail clauses 2 to 4 and never reach clause 5."
- Then `:143`'s "and clause 5's three uncached reads", `:145`'s "(c) adds one cached `Get`", `:859`, `:866` and the README row follow.

---

## MINOR 3 — `runLock` does not read the route live, and the correction strengthens (c) rather than weakening it

`:144`, last clause, and round 4's noted item that it is written in from.

`:144` ends: "clause 1 would decide from the **cached** route while `runLock` reads the same object live, so (c) closes (b)'s window only to the freshness of the manager's cache."

`runLock` reads it cached. For a re-creation — which is the whole class (b) and (c) admit — the route comes from `currentServingRoute` at `authtxn.go:1607`, and `currentServingRoute` (`httproute.go:318-332`) is `r.Get`, the manager's cached client. `reconcileServed` reads it the same way (`:1503`). The uncached read is a **different function**, `liveServingRoute` (`httproute.go:338`), whose own comment says why it exists — "A decision whose wrong answer publishes or abandons a route cannot rest on the informer cache" — and its three call sites (`authtxn.go:648`, `:854`, `:888`) are `Adopt`'s trigger and abandonment, not the `Lock`'s route read. Design 03 A77's own comment on the route gate says it outright: "the route is read through the manager's cache, so a route someone else moved is compared stale against its matching stale status" (`authtxn.go:1806-1809`).

I disagree with round 4's noted item, which said "`runLock` reads the same route live through `currentServingRoute`" — that function is the cached one, and the fifth draft wrote the claim in without re-reading it. Say so explicitly: this is a disagreement with a prior review, not a restatement of it.

The conclusion the clause draws survives, and the true fact is **better for (c)** than the stated one: clause 1 and `runLock`'s own re-creation branch would read the same object through the same cache, so they cannot disagree with each other, and the residue is the staleness `runLock` already accepts for its own decisions and design 03 already states. What is left to say is only that the cache can lag the API server.

Left as it stands there is a concrete downstream cost: an implementer taking (c) reads "`runLock` reads it live" as a reason to give clause 1 an `r.reader()` read to match, which would add a fourth **uncached** read that no cell bills.

**Fix**, one clause: "clause 1 would decide from the manager's cache, which is where `runLock` reads the same object too (`currentServingRoute`; `liveServingRoute` is the uncached read, and the `Lock` does not use it), so the two cannot disagree with each other and (c) closes (b)'s window to the freshness of that cache."

---

## MINOR 4 — (a)'s contention cell says no eval patch ever lands beside a `Lock`'s writes, and §1.1 budgets exactly those patches

`:137`, against `:167`, and the same phrase at `:142`.

`:137`'s contention cell: "none: **no eval patch ever lands beside a `Lock`'s writes**". `:142` repeats it: "fail-closed: the eval claims nothing beside the `Lock`'s writes".

Under (a) the exemption still stands for a J2 or K2 `Lock` — that is the rule the twelve critiques read — so eval patches land beside those `Lock`s' writes by design, and §1.1 budgets them thirty lines below at `:167`: "an eval costs the `Lock` at most two probe passes per case, one per run start, and one per change of `blocked` or `blockedFor`."

In context the referent is plainly the re-creation `Lock` the block is about. But the table was built so that a cell reads standing alone, and this one states a property of the whole exemption that the exemption's own contention bullet denies. It is the vocabulary failure round 4 found in "the route is open", in a different word.

**Fix.** "no eval patch ever lands beside a **re-creation** `Lock`'s writes", at `:137` and `:142`.

---

## MINOR 5 — three citations in (c)'s bullet are each one step off

`:144`, against `:163`, `:417-418` and `:190`.

Each is small; together they are the one bullet a reader taking (c) has to act from, so each should point where it says.

1. **The quoted commitment is the wrong section's.** `:144` says "'What the slice depends on' commits to moving `foreignTrafficPolicies` and `gatewayAuthPolicies` 'with their bodies unchanged'". Those exact words are "What clause 5 costs"' (`:163`). "What the slice depends on" says "moved, **unchanged**, to where both controllers can call them" (`:417`) and "Moving them to where the eval controller can call them is **a move, not a change**" (`:418`). The commitment is real and is in both places; the quotation is attributed to the section that does not carry it.
2. **The carve-out is named one function short.** `:144` says sharing "needs `servingRouteLabels` to hand back the route rather than its labels". `servingRouteLabels` is not one of the two functions the commitment covers. If it returns the route, `foreignTrafficPolicies` — which **is** one of the two — changes with it, because it consumes the labels (`authforeign.go:50-64`) and would have to hand the route up to its caller. The conclusion holds; the sentence should name the function the commitment actually binds.
3. **"verbatim" is not verbatim.** (c)'s residue is "the revision its single `backendRef` names **is uncarded**"; "When it names the exits" (`:190`) reads "**has no recorded card digest**". Same disjunction, same truth conditions — I checked both against `cardAttributes` (`:2187`) and `cardedRevision` (`:2207`) — but not the same words, and this corpus uses "verbatim" as a claim.

**Fix.** Cite `:163` for the quoted words and `:417-418` for the commitment; name `foreignTrafficPolicies` in the carve-out; and either strike "verbatim" or copy `:190`'s wording.

---

## On the option table, for the human

**Every other cell reproduces.** I traced all fifteen.

- **(a) leave clause 1.** Exempts nothing new; no marginal read; its four endings are true conditions, and `:142` now carries round 4's qualification that the third cannot come true unaided while a candidate is pending. Correct.
- **(b) clause 1 becomes "kind is `Lock`".** Exempts every missing-policy `Lock` meeting clauses 2 to 5 — correct, because the exemption is a conjunction and widening clause 1 suspends nothing else (`:119`, `:126`, `:129`). Its excess over (c) really is one state, and I traced that state pass by pass: with the route's revision carded and `status.activeRevision` not, `cardedRevision(status, status.ActiveRevision)` is false at `authtxn.go:1771`, so the re-point is skipped; the route does not move, so `routeConverged` at `:1811` passes; the probe runs; `cardAttributes` (`:2187`) matches the route's single `backendRef` by `WorkloadName` against the carded entry and attributes. The `Lock` credits and reaches `Served` within a pass or two, exactly as the cell says. Its "what it ends" — the candidate's promotion, which cards the promoted revision under Q10 and so fires the re-point — matches `:181`. Its marginal reads are short by the fourth (MINOR 2).
- **(c) (b) restricted to the residue.** The residue is now stated with the disjunction, and it is the right one: `cardAttributes` returns unattributable both when `singleBackend` is `""` (`:2140-2144`) and when no card entry matches the named revision, and `cardedRevision` covers the second half. "Not a new object kind on the eval path" is accurate. Its contention cell — (a)'s none — is right, because the state (c) drops is precisely the one that contends. Its marginal-read cell mis-bills a shared read (MINOR 2) and its last two clauses need MINOR 3 and MINOR 5.
- **The rates are the code's.** `AuthProbeInterval` is `5 * time.Second` (`authtxn.go:50`), `CardDriftInterval` `5 * time.Minute` and `CardRetryInterval` `15 * time.Second` (`card.go:336`, `:81`), and `policyPageSize` is 250 (`authforeign.go:72`). The duration claim around them is MINOR 1.

**Is it fit to put to a human? Yes** — with MINOR 1 and MINOR 2 applied in the same pass, which is two lines and a cell. They are the two the human would read wrongly: one tells them a cost ends when on a `Failed` verdict it does not, and the other tells them a read is (c)'s when it is clause 5's and therefore both widenings'. Neither changes which options exist, their ordering, or the recommendation, so if the block went over uncorrected the human would still be choosing between the right three things on the right grounds — they would be given the size and the duration of a cost both widenings share, slightly wrong, in the same direction as four previous drafts. Correct them and the block is done.

**On the recommendation itself.** I reach (a) independently, as the four earlier critics did, and MINOR 1 is a further small argument for it: the class a widening admits is the one that does not finish, so its reads do not finish either on the path where the eval says no. The deciding paragraph's answer to that — that what the widening buys in the residue is the exit that ends the wedge **with nobody acting** — is correct and is now stated (`:145`), and it is the strongest thing that can be said for (b) or (c). Between them I would take (c) if either: its excess cost over (b) is a cached read the manager already holds, and what it buys is that the exemption never covers a `Lock` that `cardAttributes` would in fact have credited. That is the same asymmetry A19 gives for re-keying clause 4, applied to clause 1.

---

## Where the fifth draft is right, and should not be redone

- **Round 4's MAJOR 1 is closed at the root.** "No new read" and "(b) is free" are gone; `:133` states the governing fact before the table, `:139` names the three uncached reads and the reader they go through, `:141` gives the rate, and `:143` carries the correction note. The residual is the fourth read's attribution (MINOR 2), not the claim round 4 killed.
- **The table was the right form, and it earned itself.** Four rounds each found a defect in the same four paragraphs; this round's findings are one cell, one clause and three citations, which is what the form was for.
- **Round 4's five minors are each closed at both sites.** MINOR 1 at `:507`, with the sites' divergence recorded honestly in the text rather than only in §14; MINOR 2's disjunction at `:144`; MINOR 3's clause at `:142`, pointing at "The ending that needs nobody"; MINOR 4 — `:145` now reads "whose route is enforcing but unproven", matching `:178`'s bolded denial; MINOR 5 at `:856`, in the asked words.
- **Round 4's design 03 finding is recorded and not acted on**, at `:875` and in the README row, and routed to design 03's process. That is the right disposition: A19 does not edit design 03, and §1.1's owed row already claims to be the narrow ordering's only pin (`:565`).
- **Clause 4's re-key is still right against the code.** `cardedRevision` (`:2207`) and `cardAttributes` (`:2196`) both match a card entry by revision **name**, and the comment on `cardedRevision` says in the source that the two readings must never be able to disagree. `:126` states the departure from A2, its reason, and the condition for reversing it.
- **`beforeObserved` is still trivially false for the whole admitted class.** `tx.BeforeObserved` is set only in the `ProbingBefore` arm (`authtxn.go:1673-1675`), and `lockMissingPolicy` enters at `ApplyingPolicies`, so clause 3 does no work for a missing-policy `Lock` — as `:144` says.
- **The §14 log, the critique table and the README row are accurate against the diff**, including the new round-4 row and the fourth-critique bullet, but for the two cost sentences MINOR 1 and MINOR 2 name.

## Noted, not counted

- Under either widening the exemption's heading and `:121`'s table caption still say "a **J2 or K2** `Lock`". Cosmetic until a widening is taken, and A19 takes none. Round 4 noted the same.
- Under either widening, clauses 1 and 3 do no work for a missing-policy `Lock`, so the conjunction reduces to clauses 2, 4 and 5. `:143`'s "clauses 2 to 5 bind as they do today" is true and slightly flattering. Round 4 noted the same.
- Round 2's tension at `:185`, between the design's zero-`backendRef` wording and the operator's shared `backend == ""` arm (`authtxn.go:2044-2049`), is unchanged and still a tension.
- The marginal-reads column runs (a) → (b) → (c) as a rising cost ladder while the exempted-set column runs as a shrinking one. That is correct and deliberate, but a human skimming two adjacent columns in opposite directions may read (c) as buying less for more. One word in the column head — "Marginal reads over the row above (cumulative)" — would settle it.
- Five rounds have now each found the read accounting wrong in whichever option the last correction did not touch. The fifth is the smallest and the fourth read is the last one left to misplace, so the sequence terminates here; the property to sweep, if there is a sixth draft, is "does every statement of cost in this block list all four reads and name the passes each is made on".

**16 A19 (fifth draft): PASS.**
