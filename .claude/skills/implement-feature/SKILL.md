---
name: implement-feature
description: Implement one assayd feature test-first. Use when writing or changing operator, compiler, CLI, or API-type code. Enforces the loop — read the design → failing test → implement → mutation-check → independent critic → merge — and refuses to finalize untested work.
when_to_use: implement, build, write the reconciler, add a feature, start coding
paths: api/**, internal/**, cmd/**, test/**, config/**, charts/**
allowed-tools: Bash(make *) Bash(go *) Bash(git status *) Bash(git diff *) Bash(git show *)
---

One feature at a time, through the whole loop. A feature is not done when it works; it is done when a test proves it works and an independent reviewer failed to break it.

## The loop

1. **Read the design.** The design doc is the spec — `docs/designs/`. If the design is silent on something you need, the design is what changes first, as a numbered amendment. Code that diverges from an approved design without an amendment is drift, whatever it does.
2. **Write the failing test first**, and watch it fail for the stated reason. A test that passes before you implement is testing the wrong thing.
3. **Implement** the smallest change that makes it pass.
4. **Mutation-check.** Delete the rule the test claims to enforce and confirm the test fails. Restore from a backup copy — `cp file file.bak` first, and never `git checkout`, which discards uncommitted work in the same file. Two tests in this repo's first harness passed with their subject deleted; that is the default outcome, not an unlucky one.
5. **`make test`** — fmt, vet, unit, envtest. `make race` for anything touching shared state.
6. **Self-review** against `review-code`'s lenses.
7. **Independent critic** — invoke `review-code`, which forks its own context. Self-review has never once caught what the independent pass caught.
8. **Fix everything**, or record what you are not fixing and why. Then commit.

## Test layers

- **unit** (`api/`, `internal/`) — pure logic and schema properties. Assert against the *generated* artifact, never a fixture defined in the test file: a fixture-counting test proves only that the fixture is unchanged.
- **envtest** (`test/envtest/`) — defaulting, CEL validation, status subresources, reconciler behaviour against a real API server. This is where a rule becomes true.
- **e2e** (`test/e2e/`) — k3d. A skipped e2e is an untested feature: the runner fails loudly rather than skipping.

## Binding patterns

- Ports and adapters: the core takes interfaces, adapters bind the world. A reconciler that calls a cloud SDK directly is untestable by construction.
- Pure core, imperative shell. Decisions in functions that take values and return values; effects at the edge.
- `ctx` first, always, and honored — every loop checks cancellation.
- Wrap errors with what was being attempted (`fmt.Errorf("fetch card for %s: %w", …)`). Never discard one.
- No global mutable state. No `init()` side effects.
- Idempotency by construction: derive names and keys from inputs so a re-run converges rather than duplicating.
- Conditions over silence: a degraded path sets a condition naming the consequence (NFR-8).
- Structured logging with the object key; never log a secret, a token, or a card body.

## Refuse to finalize

Untested code · a test that passes when its subject is deleted · a `TODO` where an error should be · a divergence from the design with no amendment · generated files not regenerated (`make verify`) · a new field that changes what a principal can name, without a test that pins the boundary.
