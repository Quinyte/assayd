---
name: implement-feature
description: Implement one plume feature test-first. Use when writing or changing operator/compiler/CLI code. Enforces the loop — failing test → implement → review → critic → merge — and refuses to finalize untested work.
---

# The implementation loop

**No feature is finalized or merged until its tests exist, run, and pass in CI.** Tests come first, not after. This is the standing instruction, not a preference.

## The loop, per feature

1. **Read the design.** The approved design in `docs/designs/` is the spec — the body is authoritative (amendments are folded in). If the design is silent on what you need, that is a design gap: stop and raise it rather than inventing behaviour in code.
2. **Write the failing test first.**
   - *Unit* (`internal/...`, `_test.go`): pure logic — the policy compiler, revision arithmetic, card validation. Table-driven. Golden files for anything the design calls deterministic.
   - *envtest* (`test/envtest/`): reconcile behaviour against a real API server — conditions, phases, ownership, finalizers.
   - *e2e* (`test/e2e/`): the real cluster path on k3d — routes served, receipts landing, degraded conditions surfacing.
   Run it. **It must fail for the right reason** before you write the implementation.
3. **Implement the smallest thing that passes**, following §Design patterns below.
4. **Make the whole suite green**: `make test` (unit + envtest) locally, `make e2e` before merge.
5. **Self-review** against `review-code`'s checklist.
6. **Independent critic pass** — spawn/prompt the critic agent on the diff. It reviews as an adversary, not an author. Findings are fixed or explicitly deferred with a reason in the PR body.
7. **Merge** only when: tests green, critic PASS, design honoured, no untracked deviation.

## Design patterns (binding)

- **Ports and adapters.** Every external system sits behind an interface owned by the consuming package — gateway client, directory store, IdP, KG provider, receipt sink. Reconcile logic depends on interfaces, never concrete SDKs, so it is testable without a cluster.
- **Pure core, imperative shell.** Decision logic (what should the state be?) is pure functions over inputs; effects (apply, patch, publish) live at the edges. The policy compiler is the archetype: `Compile(intents) → ResourceSet` is pure and golden-tested; `Apply` is the shell.
- **Errors carry context** (`fmt.Errorf("...: %w", err)`), never swallowed, never `panic` in reconcile paths. Typed sentinel errors where callers branch on them.
- **Context everywhere.** `ctx` is the first parameter of anything that does I/O; honour cancellation.
- **No global mutable state.** Dependencies are injected through constructors. This is what makes parallel tests possible.
- **Structured logging** via `logr` from the context; log actionable events, not narration.
- **Idempotency by construction.** Server-side apply with field ownership; content-addressed identities (revision hashes, receipt IDs) rather than generated ones — the corpus has two blockers that came from minting IDs at the wrong moment.
- **Conditions over silence.** Every unavailable guarantee sets a condition (NFR-8). If code degrades behaviour without a condition, that is a bug even when it "works".

## Refusals

Do not mark a feature done if: tests were written after the fact to match the implementation; a test asserts current behaviour rather than designed behaviour; e2e was skipped because "it works locally"; or the critic's findings were closed without being fixed or recorded.
