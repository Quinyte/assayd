# Design 03 A84: independent critique, round 5 (final check)

- **Date**: 2026-09-23.
- **Reviewer**: the round-3 and round-4 independent critic. It is not the author.
- **Target**: PR #62, head `12da465`, which is a fast-forward from `6dd1d53` (`git merge-base --is-ancestor` passes), on `main` `3ff741d`. Worktree `.claude/worktrees/a84-critique-r5`, detached, removed by that exact path.
- **Verdict**: **PASS: 0 BLOCKER, 0 MAJOR, 0 MINOR.**

## Round-4 findings

- **MAJOR 1 (D7's "neither gate shape fixes"): resolved.**
  - D7 now says: "Either gate shape removes that sentence from that cell only by raising nothing there — measured: under M1 and under M2′ the cell's `PolicyApplyIncomplete` is absent — the cost (1) and (2) weigh; item 11's message fix removes it at no such cost."
  - That matches the round-3 mutant logs for that subtest (`PolicyApplyIncomplete is absent, want True/AuthPolicyNotAttached` under both).
  - The recommendation is unchanged, and still rests on (2).
- **MINOR 1 (item 11's row asserts only an absence): resolved.**
  - The row now asserts both that the phrase "names the route's own reading first" is absent AND, positively, that the message says the route's reading at its current generation is unknown. It gives the reason: two wrong fixes pass the absence alone.
  - The falsifier is well-formed. Round 4 measured that shipped code makes that cell's message contain the phrase, so the row fails today. A correct fix passes both halves. Neither of the two wrong fixes passes the positive half.
  - The row still notes that item 7's positive assertion moves with the fix, and that M2′ must be re-run then. That is accurate.

## D7, read whole, as the human will read it

- **Provenance** is accurate: the clause is absent at `53cf138`, and was introduced at `22b65c2`. This was verified in round 3.
- **Both shapes** are stated.
- **Arguments** are scoped:
  - (1) applies in full to G1, and only as a deferral to G2.
  - (2) applies to both shapes, because `policyReport` does not match on `controllerName`.
  - (3) applies to G1 only.
- **What is unmeasured** is admitted: G2's deferral length.
- **The item-11 interaction** is now stated correctly.
- It is marked **OPEN**.

It is fair to both options.

**The author's M1 nuance, left unwritten, does not mislead.** In the author's M1, `claims.policyUnattached = true` sits outside the `if`. The M1 I ran put `if !routeOK { break }` first, so it stored nothing. Either way, the cell's `PolicyApplyIncomplete` is absent on the raising pass, so D7's "measured" statement is true of both variants.

The stored flag in the author's variant would let a later pass whose policy reading is unknown surface a claim that was never raised. That is a property of one mutation's line placement, not of the gate. D7 defines G1 by behaviour ("raises nothing unless the route reads accepted"), and a real G1 would be specified with its claim store at implementation time. A human choosing between "gate" and "do not gate" is not misled by its absence. Recording it would be a nice-to-have, not a condition.

## Checks

- **The round-4 record is identical.** `diff` of my scratch record against `docs/designs/reviews/03-a84-critique-r4.md` gives no output. It is committed verbatim, with no redaction needed.
- **The code is comment-only.** Since `6dd1d53` the diff touches only `03-policy-compiler.md`, `README.md` and the r4 record. Token stream of `authserved.go` with comments dropped:

  | File | Tokens | Hash |
  |---|---|---|
  | `3ff741d` | 2176 | `284c524b0c1c13b4` |
  | head | 2176 | `284c524b0c1c13b4` |
  | positive control (one token changed) | 2176 | `2fbd03581bb90157` |
- **Gates.** `make verify` exits 0 ("generation is reproducible and committed"). `make test` exits 0, with envtest `ok … 287.965s`.
- **Scans.** Across `3ff741d..HEAD` there are 0 local-path-prefix hits, and 0 case-insensitive hits for the owner's surname or given name in the diff, the commit messages or the tree.
- **Authorship.** Every commit's author and committer is `Quinyte Engineer <engineer@quinyte.com>`.
- **Status line, README and §11** all say "NOT approved", and all record round 4 as "REVISE, 0 BLOCKER, 1 MAJOR, 1 MINOR".
- **Worktree hygiene.** Nothing was mutated, and the worktree was clean on removal.
