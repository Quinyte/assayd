# Design 03 A84: independent critique, round 3

- **Date**: 2026-09-23.
- **Reviewer**: an independent critic. It is not the author and not one of the author's forked self-critiques. It followed `.claude/skills/critique-design`, ran one forked `critique-design` pass, and then reproduced and extended that pass by hand.
- **Target**: PR #62, head `b554564`, base `main` `4f96ca7`. Worktree `.claude/worktrees/a84-critique-r3`, detached at the PR head, left clean and then removed.
- **Verdict**: **REVISE: 0 BLOCKER, 2 MAJOR, 4 MINOR.**
- **Redaction on commit**: one, marked inline where it is — three path prefixes listed in the Scans bullet. Nothing else in the report is changed.
- **In short**: the correction at A84's core is right, and I could not break it. The code change is comment-only. Every mutation behaves exactly as §8.1 says. Every round-2 finding is answered. Every cell of the held table I drove matches, except one. The PR goes back for two MAJOR findings. Both are false or stale text sitting on the cell and the entry A84 itself touches, and neither needs a product-code change.

## Round-2 findings: are they resolved?

| Round 2 | Status at `b554564` | How it was checked |
|---|---|---|
| M1: provenance, and a fair D7 | **Resolved.** | `grep -c "also read the route ACCEPTED"` gives 0 at `53cf138`, 2 at `22b65c2` and 2 at `4f96ca7`. `git log -S` on design 03 shows the clause first entered at `22b65c2`. A80's §5 row at `53cf138` has the trigger "policy tuple broken … on a pass with no transaction in the slot", with no route gate, and "accepted and serving" is only descriptive there. The phrase appears in no review record before A84's. D7 now states both gate shapes (G1 and G2), scopes arguments (1) to (3) to each, and says G2's deferral length is unmeasured. Argument (2) holds against G2, because `policyReport` does not match on `controllerName` (see the comment at `authserved.go` about the ancestor controllerName being "TIDIED"), so on a cluster with a renamed controller the policy is still judged while the route reads unknown forever. |
| M2: write item 10 | **Resolved.** | P1 fails only that subtest, on 7 assertions (see below). |
| MINOR 1: the `ObservedGeneration` exception | **Partly resolved.** A second exception is missing. | See MINOR 1 below. |
| MINOR 2: the route note | **Resolved.** The "not a defect" call is correct. | See below. |
| MINOR 3: the held table | **Resolved.** One cell is wrong. | Drove the errored row too (see below). |
| MINOR 4: the comment says "raised or held" | **Resolved.** | Token streams unchanged. |
| MINOR 5: history moved to §11, and lengths labelled | **Resolved.** | Read. |

## What I ran

- **Gates.** `make verify` exits 0 ("generation is reproducible and committed"). `make test` exits 0, with envtest `ok … 284.554s`.
- **The code is comment-only.** A `go/scanner` token stream of `internal/controller/authserved.go`, with comments dropped:

  | File | Tokens | Hash |
  |---|---|---|
  | `main` `4f96ca7` | 2176 | `284c524b0c1c13b4` |
  | head `b554564` | 2176 | `284c524b0c1c13b4` |
  | positive control (M1 applied) | 2184 | `4cbca0b7cfc8804f` |

  Of the non-test code under `internal/ cmd/ api/ config/ charts/`, the diff touches only this file.
- **Mutations.** Each was run on an exported copy of the tree, never in the worktree. Every mutant compiled (`go vet` clean), `internal/controller` stayed green under all four, and each then ran the full envtest package:

  | Mutation | envtest result |
  |---|---|
  | **M1** (`if !routeOK { break }` at the top of the policy switch's `case reportBroken`), with head's `condIs` | FAIL, 284.1 s. Failing subtests: `…on_a_refused_route` (item 10), `an_unknown_route_reading_hedges_too`, and `a_first_report_on_an_unknown_route_reading_raises`. Nothing else fails. This matches item 8's "after". |
  | **M1** with `main`'s `condIs` (no nil guard) | `panic: runtime error: invalid memory address or nil pointer dereference` at `authserved_test.go:656`. The package FAILs at 155.2 s, and the item-7 subtest never reports. This matches item 8's "before". |
  | **M2′** (the policy switch value becomes `reportUnknown` when `rep == reportBroken` and `routeRep` is neither holding nor broken) | FAIL, 283.4 s, on exactly one subtest: `a_first_report_on_an_unknown_route_reading_raises`. This matches item 7. |
  | **P1** (the `clausePartlyValid` arm, on `!routeOK`, takes the hedged non-attachment lead plus `announcedNotClosed`) | FAIL, 284.1 s, on exactly one subtest: `…on_a_refused_route`, with **7** `authserved_test.go:` assertion lines. This matches item 10. |
- **Held and errored compositions, driven on envtest by a throwaway test that was not committed.** In each case both claims or one claim were raised at generation 1, the Agent was edited to generation 2 (`Spec.Runtime.Image = secondImage`), and then one pass ran. Numbers are generations; "kept" means the stored value survived.

  | Case | PAI | GS | Ready | Matches the table? |
  |---|---|---|---|---|
  | ERRORED (via `staleAgentReader`), policy claim only | `AuthPolicyNotAttached`, gen **1** kept, errored marker present | `AuthPolicyNotAttached`, gen **1** kept | gen 2 | Yes: the unknown-no-claim row says kept. |
  | ERRORED, both claims | `ServingRouteNotAccepted`, gen **2** restamped | `AuthPolicyNotAttached`, gen **1** kept | `ServingRouteNotAccepted` | Yes: the unknown-with-routeRefused row says restamped. |
  | ACCEPTED plus held policy, but with the stored PAI reason being `ServingRouteNotAccepted` (the path out of A80's own incident) | `AuthPolicyNotAttached`, gen **2**, held marker present | gen 1 kept | — | **No.** The table says `ObservedGeneration` is kept. See MINOR 1. |

  **The errored row, which the author did not drive, is confirmed as stated.**
- **Rebased commit identities.** Checked with `git patch-id --stable`:

  | Recorded hash | Rebased hash | Patch-id | Commit |
  |---|---|---|---|
  | `4d53669` | `63cdbae` | `63eb7738808d`, same | "answer the independent critique" |
  | `0ae3365` | `63cdbae` | `63eb7738808d`, same | "answer the independent critique" |
  | `28fd9d6` | `e5125a6` | `d32a09b1949e`, same | "not gated on the route" |
  | `40fe057` | `17608b6` | `63e58d4c4b7d`, same | "Ready does not name" |

  §11's mapping of the selfcritique2 hash to `63cdbae` is correct.
- **`CLAUDE.md` and `AGENTS.md`.** 8 of CLAUDE.md's 15 paragraphs are byte-identical in AGENTS.md, both at `main` and at head, including "What remains untrue". The added lines in the two files' diffs are identical (`cmp` exit 0).
- **Scans.** `git diff 4f96ca7..HEAD` has no local filesystem path prefix *[the three prefixes the critic listed are redacted on commit: written literally they are what this repository's leak scan matches, so the file could not be committed with them]*. A case-insensitive search for the owner's surname and given name finds nothing in the diff, the commit messages, or the whole tree at HEAD. All 5 commits have author and committer `Quinyte Engineer <engineer@quinyte.com>`.
- **Status, README and §11.** All three say A84 is "NOT approved". The round-2 counts (0/2/5) agree in all three.

## MAJOR

### MAJOR 1: the generated reference still reports the disagreement A84 resolves

Where: `internal/refgen/reasons.yaml:662`, which is rendered as `docs/reference/conditions.md:377`.

The `AuthPolicyNotAttached` entry has a `disagreement:` field that opens "Design 03 §5 still frames this row as firing 'on a pass that also read the route ACCEPTED at its current generation'". It goes on to say that A82's "The fix is OWED" bullet still stands.

At head, both statements are false:

- `grep -n "also read the route ACCEPTED" docs/designs/03-policy-compiler.md` matches only line 3 (Status), 1534 (D7), 1544 and 1556 (§11). None of these is §5 or §3.3.3.
- `grep -n "fix is OWED"` has no match.

The page still counts this reason among its "5 reason(s) carry a recorded disagreement" (`conditions.md:1540`).

A84 regenerated `conditions.md` for a line shift and left the one entry that exists to record this defect. §11's census of the sites carrying the false sentence stops at five and omits this one. `make verify` passes because the drift gate compares the page with its generator, not the prose with the design.

**Fix:**
- Rewrite that `disagreement:` entry: remove the two resolved paragraphs, and keep the "What genuinely remains" residue.
- Regenerate the reference.
- Add `reasons.yaml` to §11's census.

### MAJOR 2: on the cell A84 tables and item 7 pins, the policy message makes a false claim about itself

Where: `internal/controller/authserved.go:485` (and the `clauseRejected` twin just below it), and `docs/designs/03-policy-compiler.md:843` and `:850`.

On `!routeOK`, the hedged lead says "PolicyApplyIncomplete names the route's own reading first". On the UNKNOWN reading with no `routeRefused` standing (first table, row 3, "as ACCEPTED"), that lead is PolicyApplyIncomplete's **own** message, with reason `AuthPolicyNotAttached`. It carries no route reading, and Ready and Degraded carry the same text. So the message points the operator at a report that does not exist. The fork measured this on envtest. I confirmed it from the code path: `routeOK=false`, the route half raises nothing, and the policy half's message is the whole condition.

Why this matters for A84:

- **It is permanent on a renamed-`controllerName` cluster.** There the route reads unknown on every pass, and that is exactly the scenario D7's argument (2) rests on.
- **D7 relies on this message being honest.** It says gating would buy "nothing on the message, which the hedged lead already keeps from claiming a route it did not read". But on this cell the lead claims a route report that is not there.
- **The table hides it.** The second table (`:850`) elides exactly this sentence as "…".
- **Item 7 pins it.** Item 7 asserts the hedged lead positively, so the false sentence is now pinned rather than flagged.

The defect predates A84 and is shipped message text. That is a rule-8 defect, and A84 already classes item 9 as the same kind.

**Fix:**
- Quote the full lead in the table.
- Record the defect beside item 9.
- Owe a message fix as a new §8.1 item, with a falsifier: a row asserting that the unknown-no-claim cell does not say PolicyApplyIncomplete names a route reading.
- Soften D7's "keeps from claiming a route it did not read" until the fix lands.

## MINOR

### MINOR 1: the held table's ACCEPTED cell, and the "one exception" sentence, miss a second exception

Where: `03-policy-compiler.md:859` (held table, row 1), `:868` ("with one exception"), `CLAUDE.md:11` and `AGENTS.md:13`.

What I measured (see the third row of the driven table above):
- Both halves were raised on a REFUSED pass, so PAI was `ServingRouteNotAccepted` at generation 1, with the policy claim as a fragment.
- The Agent was then moved to generation 2, the route was read ACCEPTED, and the policy was held.
- Result: PAI reads `AuthPolicyNotAttached` at generation **2**, which the table cell ("`ObservedGeneration` … kept") says cannot happen.

The cause: when the stored reason is not `AuthPolicyNotAttached`, the claim is re-raised through `raiseIncomplete` (the path `:870` already describes correctly), so nothing is "carried whole".

The CLAUDE.md/AGENTS.md sentence, "except where another reason composes beside a held `PolicyApplyIncomplete` on the same pass", does not cover this, because nothing composes beside it on that pass.

This is the third round on this same sentence. **Fix:** condition the cell on the stored reason, and name the second exception in `:868` and in both shared paragraphs.

### MINOR 2: two Status and README statements are now false

- **"The only code A84 touches".** The Status line (`:3`), the README row (`README.md:11`) and §11 "Body changed" (`:1551`, "the comment aside, is nothing") all say the code comment is "the only code A84 touches". The fourth revision also changed `condIs` and added two envtest subtests. Say "the only PRODUCT code".
- **Argument (1) without scope.** Status and README also give argument (1) ("gating would let an UNKNOWN reading of the route clear a claim about the POLICY") with no scope. That holds only for G1: under G2 a standing claim is held. D7 scopes it correctly. The summaries should too, or should lead with (2), which is what carries the recommendation.

### MINOR 3: a test check that can no longer fail

Where: `test/envtest/authserved_test.go:478`, `if g == nil { t.Fatal("GovernanceSkipped is absent") }`, added in `e40f350`.

It is dead since item 8, because `condIs` now calls `t.Fatalf` on an absent condition before returning. Under this repo's own rule, a check nothing can fail reads as load-bearing. **Fix:** delete it.

### MINOR 4: §11's hash mapping is incomplete

Where: `03-policy-compiler.md:1557`.

It says the hashes in review records "are mapped here". It maps selfcritique2's `4d53669`/`0ae3365` → `63cdbae`, but for round 1 (`28fd9d6`) and round 2 (`40fe057`) it says only "pushed before later rebases".

I verified by patch-id that they are `e5125a6` and `17608b6`. **Fix:** state those two mappings.

## Checked and not a finding

- **The route-note "not a defect" classification is right.** Where the policy half raises or holds beside a standing route claim, the route's `GovernanceSkipped` note is lost. But in every such composition, PolicyApplyIncomplete, Ready and Degraded all carry `ServingRouteNotAccepted`, so no cause is hidden. Item 9 is different: an overwritten `ForeignTrafficPolicy` is carried by no other condition, so that cause disappears. The contrast holds.
- **D7 is fair to both shapes.**
  - (1) bites G1 in full (the claim is suppressed, and `appendGovernance` sits inside the gated block) and G2 only as a deferral.
  - (2) holds against both shapes, as verified above.
  - (3) hits G1 only.
  - G2's deferral length is openly unmeasured.
  - The recommendation rests on (2) alone. MAJOR 2 above qualifies one sentence in the "what either shape would buy" clause.
- **Every restore was hash-verified.** No file in the worktree was ever mutated. At the end, `git hash-object` equals `HEAD:` for `authserved.go`, `authcreate_test.go` and `authserved_test.go`, and `git status --porcelain` is empty.
