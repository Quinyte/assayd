# Design 03 A84: independent critique, round 4 (confirmation pass)

- **Date**: 2026-09-23.
- **Reviewer**: the same independent critic as round 3. It is not the author.
- **Target**: PR #62, head `6dd1d53`, rebased onto `main` `3ff741d`. Worktree `.claude/worktrees/a84-critique-r4`, detached, left clean and then removed by that exact path.
- **Verdict**: **REVISE: 0 BLOCKER, 1 MAJOR, 1 MINOR.**
- **In short**: every round-3 finding is answered, and every cell I re-drove matches the new tables. The MAJOR is a single clause in D7, the text the human will decide from. It says the item-11 defect is one "which neither gate shape fixes". Measured, both gate shapes remove that sentence from the cell, because under either shape the cell raises nothing. That is a cost, not a fix. But D7's job is to state accurately what each shape does, and this clause says the opposite of what happens. It is a one-clause correction, and nothing else stands between this amendment and a PASS.

## Round-3 findings: are they resolved?

| Round 3 | Status at `6dd1d53` |
|---|---|
| **Record redaction** | **The only change.** `diff` of my round-3 scratch record against `docs/designs/reviews/03-a84-critique-r3.md` shows exactly two changes: the added "Redaction on commit" header line, and the Scans bullet, where the three path prefixes are replaced by a marked redaction. Nothing else differs. |
| **MAJOR 1** (the generated reference) | **Resolved.** The `reasons.yaml` `disagreement:` entry now keeps only the residue, plus the item-11 defect. `conditions.md` is regenerated, and `make verify` reports "generation is reproducible and committed". The author's correction to my finding **is right**: `The fix is **OWED**` still exists, at design 03 §11, and the bold markers defeated my plain `grep "fix is OWED"`. The sentence is not stale only because A83's annotation sits right beside it ("A83 DELIVERED BOTH … this sentence is stale from that date"). My conclusion stood; my evidence for it was wrong. |
| **MAJOR 2** (the false sentence in the hedged lead) | **Resolved as recorded, not fixed**, which is acceptable because the fix is a shipped message string. Both hedged leads are now quoted in full in the second table, with the unknown-no-claim cell marked FALSE. It is owed as §8.1 item 11, `reasons.yaml` records it, and D7 is softened. The softened D7 sentence introduces the new MAJOR below. |
| **MINOR 1** (the `ObservedGeneration` exception) | **Resolved.** I re-drove four cells (below), and all match. The general paragraph, the `PolicyApplyIncomplete` hold rule, the table and the shared CLAUDE.md/AGENTS.md sentence all state the same rule: the generation is kept only where the held claim is the stored reason and nothing else writes the condition. Otherwise it takes the pass's generation, when a route reason is raised or held beside it, or when the route's stored reason gives way to a route that has come back. |
| **MINOR 2** (the summaries) | **Resolved.** The Status line and README now say "the only PRODUCT code A84 touches (it also adds envtest rows)". The Status line scopes argument (1) to "the blunt shape" and leads with (2) for both shapes. |
| **MINOR 3** (the dead `if g == nil`) | **Resolved.** It is deleted. |
| **MINOR 4** (the hash mappings) | **Resolved.** `28fd9d6→e5125a6` and `40fe057→17608b6` are added. The new `b554564→58636c2` has the same patch-id: `7760e1be4cb28ad7…` for both. |

## What I ran

- **Gates.** `make verify` exits 0. `make test` exits 0, with envtest `ok … 283.592s`.
- **The code is comment-only.** A `go/scanner` token stream with comments dropped:

  | File | Tokens | Hash |
  |---|---|---|
  | `3ff741d:internal/controller/authserved.go` | 2176 | `284c524b0c1c13b4` |
  | head | 2176 | `284c524b0c1c13b4` |
  | positive control (`routeOK := routeRep != reportBroken`, one token changed) | 2176 | `2fbd03581bb90157` |

  Under `internal/ cmd/ api/ config/ charts/`, the diff touches only `authserved.go` and `internal/refgen/reasons.yaml`, which is data.
- **Held `ObservedGeneration` cells, re-driven on envtest.** I used a throwaway test on an exported copy of the tree, not committed. Each cell raised at generation 1, then edited the Agent to generation 2 and ran one held pass:

  | Held-pass reading | Stored PAI reason | PAI | GS | Matches the table? |
  |---|---|---|---|---|
  | ACCEPTED | ANA | gen 1, ANA | gen 1 | yes |
  | ACCEPTED (my round-3 case) | SRNA | gen **2**, ANA | gen 1 | yes |
  | REFUSED | ANA | gen **2**, SRNA | gen 1 | yes |
  | UNKNOWN, no route claim | ANA | gen 1, ANA | gen 1 | yes |

  Round 3 already drove both ERRORED cells on the same product code (gen 1 for the policy claim alone, gen 2 for both claims).
- **UNKNOWN-no-claim with SRNA stored.** I could not reach it through the operator, and I agree no path does:
  - Every writer that clears `routeRefused` either rewrites PAI, as an ACCEPTED `judgeServed` pass does, or clears `policyUnattached` along with it: `recordServed` does, and so does a CRD that prunes both flags.
  - I then **forced it synthetically**, by writing `status.auth.routeRefused=false` directly over a stored SRNA. The held pass then gives PAI `AuthPolicyNotAttached` at generation **2**, and GS generation 1.
  - That is what the general rule predicts (the held claim is not the stored reason, so the generation is not kept). So even that cell does not contradict the text.
- **Item 11's falsifier on shipped code.** I ran item 11's cell (a first broken report on an UNKNOWN reading with no claim). PAI reads `AuthPolicyNotAttached`, and its message **contains** "names the route's own reading first". So the proposed row, asserting that the phrase is absent, fails on shipped code as §8.1 says, and a message that drops or replaces the phrase would pass it.
- **`CLAUDE.md` and `AGENTS.md`.** 8 of 15 paragraphs are byte-identical, both at `3ff741d` and at head, including "What remains untrue". Its new `ObservedGeneration` sentence matches the measured table.
- **Scans and authorship.**
  - No local path prefix appears in the `3ff741d..HEAD` diff (count 0).
  - A case-insensitive search for the owner's surname and given name finds nothing, with 0 hits in the diff, the commit messages and the tree at HEAD.
  - All commits have author and committer `Quinyte Engineer <engineer@quinyte.com>`.
- **Status, README and §11.** All three say A84 is "NOT approved", and all three give round 3 as "REVISE, 0 BLOCKER, 2 MAJOR, 4 MINOR".

## MAJOR

### MAJOR 1: D7 says neither gate shape removes the item-11 sentence, and both do

Where: design 03 §9 D7, the "What either shape would buy" sentence.

The sentence says the hedged lead, on UNKNOWN with no route claim standing, "sends the reader to a route reading `PolicyApplyIncomplete` does not carry, a shipped rule-8 defect owed as §8.1 item 11 **which neither gate shape fixes**."

Measured under both shapes, in round 3's full-suite mutant runs of `a first report on an unknown route reading raises`, which is exactly this cell:
- **M1 (G1):** `PolicyApplyIncomplete is absent, want True/AuthPolicyNotAttached`.
- **M2′ (G2):** the same.

So under either shape the cell raises nothing, and the false sentence is never written there. Later passes do not bring it back:
- Under G2, a standing claim goes to the held branch, whose lead ("a claim from an earlier pass is standing …") does not contain the sentence.
- Under G1, the `!routeOK` hedged leads can no longer be reached at all.

Gating therefore removes item 11's defect, but only by silencing the report on that cell, which is exactly the cost arguments (1) and (2) weigh. The clause as written tells the human that gating leaves the defect in place. That understates one effect of the option they are choosing between, and it is the decision text.

**Fix**: one clause, along these lines: "…owed as §8.1 item 11. Either gate shape removes that sentence from that cell only by raising nothing there, the cost (1) and (2) weigh; item 11's message fix removes it at no such cost." The recommendation is unchanged: (2) still carries it, and item 11 is orthogonal to the gate.

## MINOR

### MINOR 1: item 11's row asserts only an absence

Where: design 03 §8.1 item 11.

The row asserts that the message does NOT say "names the route's own reading first". A fix that drops the whole hedge passes it, and so does one that reverts to the `routeOK` lead ("route is accepted and SERVING", a worse falsehood). Today item 7's positive `"THIS PASS DID NOT READ IT AS ACCEPTED"` covers that on the same cell. But item 11 itself says item 7's assertion will change with the fix, so the cover may go away at exactly that moment.

**Fix**: have item 11's row also assert the positive, namely that the message says the route's reading at its current generation is unknown. Alternatively, state that item 7's hedge assertion must survive the fix.

## Checked and fair

**D7's other text is fair to both shapes.**
- Provenance: 0 at `53cf138`, and introduced at `22b65c2`, as verified in round 3.
- (1) is scoped: in full against G1, and as a deferral against G2.
- (2) holds against both shapes, because `policyReport` does not match on `controllerName`.
- (3) applies to G1 only.
- G2's deferral length is stated as unmeasured.
- It is marked "OPEN".

With MAJOR 1's clause corrected, I would give D7 a PASS as decision text.

**Every restore was checked.** Nothing in the worktree was mutated. The probe ran on a `git archive` copy, since deleted. `git status --porcelain` was empty before the worktree was removed.
