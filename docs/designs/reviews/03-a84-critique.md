# Design 03 A84 — independent critique, round 1

- **Date**: 2026-09-23.
- **Reviewer**: an independent critic (not the author, not the author's forked self-critique), following `.claude/skills/critique-design`.
- **Target**: PR #62, branch `design-03-a84-precondition`, head `28fd9d6`, base `origin/main`.
- **Verdict**: **REVISE — 1 BLOCKER, 4 MAJOR, 5 MINOR.**
- **Provenance of this file**: the critic returned its report to the coordinating session and did not commit it (reviewers here do not push). The coordinator wrote it to a file verbatim so that it could be committed; the text below is the critic's report as returned.
- **Redaction on commit**: one, marked inline where it is — the regular expression of a path-and-name scan, which spells a personal name. Nothing else in the report is changed.

---

I ran a forked `/critique-design` and then independently re-verified every load-bearing finding against `origin/main` and against the code. All measurements below are mine.

## The central correction is CORRECT — I could not break it

Your brief's line numbers all check out on `origin/main`: `:577` is `routeOK := routeRep == reportHolding`; `:606-608` is the false comment; `:611-619` is the `reportBroken` branch; `:504` is the `PartiallyValid` "absence is the point" comment; §3.3.3's bullet is at `:835`, §5's row at `:1375`.

- `grep -n 'routeOK'` on head returns exactly two non-comment uses: `:629` and `:667`, **both arguments to `policyBrokenMessage`**. The only `if !routeOK` branches are inside that function, at `:482` (`clauseUnattached`) and `:495` (`clauseRejected`).
- In `case reportBroken`, `raiseIncomplete`, `appendGovernance`, `withholdInOrder` and `out.served = true` are **all unconditional** under every clause. A84's statement of what the code does is exactly right.
- **Scope verified mechanically.** I stripped all comments and blank lines from base and head with a string-aware tokenizer: `IDENTICAL after comment strip: True`. The "comment-only, no statement, expression, signature or control-flow edge moved" claim is true.
- **The three design-vs-code reasons all hold.** (1) gating would let an unknown *route* reading suppress a *policy* claim — `appendGovernance` is in the gated block, so the `GovernanceSkipped` tier record vanishes; (2) `routeReport` matches nothing on a renamed `controllerName`, so `routeOK` is false forever and the half is silenced permanently; (3) `appendGovernance(ReasonAuthPolicyNotAttached, …)` is unconditional and the route half only calls `noteGovernance`, so `GovernanceSkipped` does keep `AuthPolicyNotAttached` when the route half outranks it — a gate contradicts that directly. **The recommendation not to gate is right.**
- **M1 and M2 reproduce.** M1 (`if routeOK` around the four raise calls): `go build ./...` clean — **not INVALID** — and `TestBothHalvesOnOnePassNameTheRouteFirst` **FAILS**. M2 as recorded. The ungated raise **is** pinned by the refused reading.
- Authorship `Quinyte Engineer <engineer@quinyte.com>`. Path scan `git grep` over the diff for local paths and a personal name *[the pattern is redacted on commit, because it spells a personal name this public repository does not carry]*: **CLEAN**.
- `AGENTS.md`/`CLAUDE.md`: A84 touches neither; all five shared paragraphs are **byte-identical** between the files, and the "What remains untrue" paragraph states the trigger with no route clause, so A84 agrees with it rather than contradicting it.

## BLOCKER

**B1 — A84 replaces a false precondition with a false statement about the LEAD, in the authoritative body.**
`docs/designs/03-policy-compiler.md:835` (§3.3.3): *"**Where the route was read UNKNOWN**, the lead is the hedged one instead"*. `:1375` (§5): *"…with the hedged lead rather than the 'accepted and SERVING' one."* Both unqualified.

Within `case reportBroken` the clause can be `clauseUnattached`, `clauseRejected` or `clausePartlyValid`. Only the first two have an `if !routeOK` hedge (`:482`, `:495`). `clausePartlyValid` has none — `:504` says so in terms — so on an unknown reading of a `PartiallyValid` policy the lead is **neither** the hedged one **nor** "accepted and SERVING"; it is the same lead as on an accepted route. The sentence is false for one of the three clauses.

The preceding §3.3.3 sentence is correctly scoped ("on the two clauses in which the Gateway itself reports non-attachment, which since A83 is not all four"); the new sentence drops the scope and says "instead". §5 has no scope anywhere near it.

This is not a corner: `clausePartlyValid` is the clause **A84 itself** cites as the one A82 measured reachable ("`Accepted=True`/`PartiallyValid`, from a rejected entry in the administrator's key `ConfigMap`"). And A84 contradicts itself across sections again — its own §11 entry says *"`clausePartlyValid` is the one clause that does not consult it for the wording either"* — but §11's header reads **"provenance — the body above is authoritative"**, so the authoritative text is the wrong one. That is structurally the identical defect A84 exists to remove.

**Fix**: scope both sentences to the two clauses that have a hedge, matching the ACCEPTED sentence beside it. Better, replace the prose with a route-reading × clause matrix as a real table — which also discharges MINOR 5.

## MAJOR

**M1 — §8.1 owed item 7's second row names a falsifier that is already red. Measured.**
`:1475` says the second owed row is falsified by *"drop the `stored.routeRefused` hold in `judgeServed`'s `default` arm"*. I applied it (`if false && stored.routeRefused`), `go build ./...` clean:

```
--- PASS: TestBothHalvesOnOnePassNameTheRouteFirst (2.02s)
--- FAIL: TestAnUnknownReadingHoldsAStandingReport (7.36s)
    --- FAIL: .../the_route_half (1.45s)
    --- FAIL: .../the_claim,_not_the_condition (1.14s)
```

It is red **with the owed row absent**. Write the row, delete it again, the mutation stays red — it proves nothing about that row. §8.1's convention everywhere else is that the mutation is *that row's* falsifier. This is A84's own recorded MAJOR 2 (asserting a mutation without running it) committed a third time: M1 and M2 were re-measured, this one was not.
**Fix**: name a mutation the composition row uniquely kills (candidate: flip `incompleteRank(c.Reason) > incompleteRank(reason)` to `>=` in `authtxn.go:594`), or drop the row as not worth owing.

**M2 — the held route claim goes through `holdIncomplete`'s CARRY path, not `raiseIncomplete`.**
`:1375` and `:1511`: *"the route half HOLDS `ServingRouteNotAccepted` through `holdIncomplete`, which re-asserts it through `raiseIncomplete`."* Read `holdIncomplete` (`authserved.go:728-755`): when `PolicyApplyIncomplete` is **not** already set this pass and the stored condition matches, it does `conds.carry(held)` and **returns** before `raiseIncomplete`. The route half runs *before* the policy half in `judgeServed`, so in A84's own "ordinary incident" the condition is not yet set and the carry path is taken every time. `holdIncomplete`'s own comment calls `raiseIncomplete` "ONE EXCEPTION". Not cosmetic: carry is what preserves `LastTransitionTime` and `ObservedGeneration`, which §3.3.3 requires of a held report; naming `raiseIncomplete` asserts the generation is restamped by that call.
**Fix**: "`holdIncomplete` re-asserts the stored condition whole through `conds.carry`, keeping its `LastTransitionTime` and `ObservedGeneration`; the policy half's `raiseIncomplete` then composes beside it under `incompleteOrder`."

**M3 — A84's open decision is not in §9.**
`:1513` records "Gating the raise is NOT recommended… a posture change this amendment has no standing to make", and the Status line repeats it. §9 "Decisions for async review" holds **D1–D6 only** — no A84 entry. A82 opened D5 and D6 exactly this way, so the precedent is established. A human scanning §9 will not find the one decision A84 says is theirs. This is A84's own §8.1 argument — "an owed item that lives only in a §11 paragraph is one nobody counts" — applied to the owed test row but not to the open decision.
**Fix**: add **D7 — whether the fail-open `AuthPolicyNotAttached` raise should be gated on the route reading**, with the three arguments from `:1513` and the recommendation (no).

**M4 — the critique A84 cites has no artifact.**
The Status line, `README.md` and `:1517` all cite `critique-design` returning "REVISE: 1 BLOCKER, 3 MAJOR, 5 MINOR", and `:1517` reproduces nine findings by number. `git ls-files | grep -i a84` → **empty**. Every other critiqued amendment has a file (`03-a77-critique*.md` ×8, `03-a79`, `03-a80*` ×5, `03-a81-review.md`, `03-a83-review.md`), and the Status line's own sentence draws that contrast for A82/A83 then cites A84's critique with no path. `AGENTS.md:29`: **"findings are files."** The counts and findings are uncheckable — and MINOR 2 below shows one of them was taken wrong.
**Fix**: commit `docs/designs/reviews/03-a84-critique.md` and cite it from all three sites.

## MINOR

1. **`:1512` cites `:504` while `:1508` says line numbers were deliberately dropped** ("A84's own comment correction shifts every line below it"), and `:1517` repeats it. It resolves today only because `:504` sits *above* the edited comment. Use `policyBrokenMessage`'s `clausePartlyValid` arm.

2. **The A83-mention count A84 wrote in is wrong — and it "fixed" a correct number into an incorrect one.** `:1549` says *"the entry around it names A83 five times, four of them inside bold spans."* I counted the pre-A84 A82 entry by splitting on balanced `**` delimiters (150 delimiters, even/balanced): **5 total, 2 inside bold, 3 outside.** `:1517` records the finding as *"the A83-mention count was wrong (four bold spans, not two)"* — the original **two was right**. A84 took a critique finding without measuring it. Otherwise the A82 annotation is narrow and accurate, and it correctly records the "no forward pointer to A83" claim as false rather than repeating it. **Fix**: restore "two".

3. **The recorded gate result does not reproduce.** The commit message reports `TestAStoredCardPathStaysEditable` failing as pre-existing. With the Makefile's own pinned assets (`ENVTEST_K8S=1.36.x` → `1.36.2-darwin-arm64`):
   ```
   --- PASS: TestAStoredCardPathStaysEditable (0.08s)
   ok  github.com/Quinyte/assayd/test/envtest  6.088s
   ```
   It **passes**. It is unrelated to A84 either way — the code diff is provably comment-only — but the gate note is wrong and should be corrected or dropped rather than left as a standing "known failure".

4. **M1 panics rather than failing cleanly.** `test/envtest/authserved_test.go:627-629` dereferences `c.Message` after `condIs` returns nil, so the mutation produces `panic: runtime error: invalid memory address or nil pointer dereference` and aborts the rest of the package, hiding subsequent failures. A84 records the result as "with `PolicyApplyIncomplete` nil" without saying the row panics. **Fix**: `t.Fatalf` in `condIs`, or guard the dereference.

5. **Readability.** §5's policy row is now one table cell of **18,876 characters**; A84 adds ~2,655. The added content is a route-reading × clause matrix rendered as prose inside a table cell. `write-spec` asks for tables past three parallel cases. Lifting the matrix out also makes B1 impossible to write.

## On the brief's items 5 and 6

**Item 5 (the A82 sentence)** — the annotation is correctly narrow: it annotates rather than rewrites, states A83 delivered both, keeps (c) open consistently with D5, and explicitly records the "no forward pointer" over-statement as false. Only the bold count is wrong (MINOR 2).

**Item 6 (rules 7 and 8)** — A84 states what happens rather than what is intended everywhere except B1 and M2, which are fresh rule-7 errors inside a rule-7 correction. The "What the corrected rule means for an operator, plainly" sub-bullet is the strongest thing in the amendment and I would keep it verbatim: it says exactly what `GovernanceSkipped=AuthPolicyNotAttached` establishes, that it does **not** establish a credential is no longer required, that it does **not** establish the route is carrying traffic, and it directs the reader to the message lead rather than the reason. That satisfies rule 8.

## What to do

Fix B1 and M1–M4 and this is a clean, valuable correction — the core claim survived every attack I ran, the design-to-code direction is argued rather than asserted, and refusing the posture change on a fail-open condition is the right call. But I will not pass a rule-7 amendment that ships two new rule-7 errors into the authoritative body of an approved slice, with the correct statement demoted to the section whose own header says it is not authoritative.

**No files were changed.** Both mutations were restored from a byte-verified backup (`shasum -a 256` matched `1afe2562a8a7…` before and after each), my worktree was removed by exact path, and the author's `a84-precondition` worktree was not touched. I pushed nothing, commented nowhere, and merged nothing.
