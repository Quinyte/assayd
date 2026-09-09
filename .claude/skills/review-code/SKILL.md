---
name: review-code
description: Adversarial review of a assayd code diff before merge. Use after implementing a feature and before merging, or when the user asks to review code. Complements critique-design, which reviews specs; this reviews the code and its tests.
when_to_use: code review, review this diff, before merging, "is this code right"
context: fork
background: false
effort: xhigh
---

Review the diff named in the arguments — `git show <ref>` or the working tree. You are reading it cold. Assume it is wrong and find where.

## Verify before reviewing

Run `make test`. A review of code you have not seen run is a guess. Require `go test -race` evidence for anything touching shared state.

**Mutation-check every assertion the diff adds.** For each new test, delete the rule it claims to enforce and confirm the test fails. A test that passes with its subject removed is worse than no test: it converts an unenforced rule into a documented guarantee. Restore mutations from a backup copy — never `git checkout`, which will silently discard uncommitted work in the same file.

## Lenses

1. **Correctness** — walk the actual control flow with a concrete input, including the error paths. Do not read the happy path and infer the rest.
2. **The design** — does this match the design doc it implements? If it diverges, is the divergence recorded as an amendment, or did the code just drift?
3. **Concurrency** — shared state, map access, goroutine lifetime, context cancellation. Every controller path takes a `ctx` and honors it.
4. **Idempotency** — a reconciler runs again on the same input. Does it converge, or does it double-write, re-emit, or thrash?
5. **Failure** — what happens when the API server, the gateway, or the store is unavailable mid-operation? Partial application with no condition raised is a defect (NFR-8).
6. **Authority** — what can a principal now name, and what derives authority from that name? A new reference field is an authorization change whatever the commit message calls it.
7. **Tests** — do they prove their names? Is any assertion mutation-proof? Is the property asserted about the real artifact (generated CRD, live API server) or about a fixture in the test file?
8. **Ergonomics** — for anything a user types: the `write-spec` rules. Errors name the fix.

## Output

`BLOCKER` / `MAJOR` / `MINOR` with `file:line`, a concrete failure scenario, and a specific fix. Verdict: `PASS` or `REVISE`. Do not soften.
