# Design 17: Session replay engine

- **Status**: draft — awaiting critique
- **Phase**: P3 · **Size**: M · **Date**: 2026-08-20
- **ADRs**: 0021 (receipts, fidelity consumers, capture levels) · interfaces: 04 (the stream + object store), 16 (dataset sampler), 08 (`plume session` verbs), 02 (dev-revision targets)

## 1. Purpose & scope

Turns the receipt stream into three capabilities: **session reconstruction** (what happened), **replay** (run it again — for debugging and for eval datasets), and **sampling** (production traffic → regression cases, design 16's `fromSessions`). In scope: the session model, the two replay modes and their fidelity limits, the sampler, export/scrubbing. Out of scope: receipt capture (04 owns), eval verdicts (16).

## 2. Doctrine & charter gates

- **Plane**: slow (a library + Job logic in the operator/CLI; no service). **Pods**: 0 — reconstruction is a read path; sampling/extraction run inside the eval Job (16 §5); `plume session` verbs read directly.
- **Stateful deps**: none new — sessions are **views over the RECEIPTS stream + object store**, never a copy. ✓ **Primitives**: Event (consuming), Artifact (exported fixtures/datasets), Agent (replay drives A2A). ✓

## 3. The session model

A session = all receipts sharing a `task_id`, ordered by `ts` + span causality:

```
Session {
  task_id, tenant, ns, agent@revision,
  principal_chain,                        // from the first hop (ADR-0010)
  hops: [Receipt…],                       // the ordered story: llm | mcp_tool | kg_query | a2a…
  outcome: {status, termination_reason},  // from the terminal A2A receipt
  usage: {tokens, usd},                   // summed
  capture_level                           // the minimum across hops — governs replay fidelity (§4)
}
```

Reconstruction is a pure function of the stream (fidelity-consumer rule, ADR-0021: never the Postgres index). Cross-agent tasks (A2A handoffs) reconstruct as one session tree via `lineage.path`.

## 4. Replay — two modes, honest fidelity

| Mode | What runs | Requires | Guarantees |
|---|---|---|---|
| **Re-drive** (default) | The recorded *task input* is re-sent as a fresh A2A task through the gateway at a named target (dev revision, candidate, active) | `capture ≥ headers` on the initial hop (the task input body; `metadata`-only sessions are **not replayable** — listed as such, never silently skipped) | Same entry conditions; the world may answer differently — this is the *regression* mode (16 datasets) |
| **Mocked replay** | Re-drive, but the gateway routes the replay principal's tool/KG calls to a **replay-mock backend** that answers from the session's recorded responses (keyed by hop sequence + tool + input digest) | `capture: full` on the session's tool hops | Deterministic externals — this is the *debugging* mode: isolate the agent's reasoning from the world's drift |

Mocked-replay mechanics: the CLI/eval Job materializes the session's tool responses into a short-lived mock server (a Job sidecar-less process started by the verb, torn down after); the compiler's replay-principal route (one row, design 03 next revision) points that principal's backends at it. **Mock misses** (the replayed agent makes a call the recording never made) are a *finding*, not an error: recorded as divergence — the replay report's first-class output (`diverged_at: hop N, wanted: X`). KG calls prefer the honest alternative: replay against the **pinned graph version** live (versions are immutable — the original answers are reproducible without mocks); tool calls have no such property, hence mocks.

## 5. The sampler (16 `fromSessions`)

Stratified sampling over a window: strata by `(outcome.status, termination_reason, hop-count band)` by default (config per suite) — failures and weird paths are *over*-sampled deliberately (regression value concentrates there); PII posture inherited (samples reference the stream; the dataset artifact embeds session refs + input digests, not fresh copies of bodies). Non-replayable (metadata-level) sessions are excluded *and counted* — the dataset report says "N=200 sampled, 34 excluded (capture level)", never silence (the no-silent-caps rule).

## 6. Export & scrubbing

`plume session export <id>` → a self-contained fixture (inputs + recorded hops + outcome) for bug reports and golden-case promotion. Export **always** runs the credential-scrub denylist again (defense in depth) and honors an additional `--redact <pattern>` pass; exports are marked with their origin digests. `plume eval promote <session>` = export + append to the suite's curated set.

## 7. Failure modes

| Failure | Behavior |
|---|---|
| Session incomplete (receipt gaps — 04's `ReceiptsDegraded` window) | Reconstructed with explicit `gaps: [seq…]`; not replayable if the initial hop is missing; gaps surfaced, never smoothed |
| Object-store body pruned (retention) | Session lists hop metadata; replay refuses with `BodiesExpired` naming the retention boundary |
| Mock miss (divergence) | Report `diverged_at`, replay continues live-or-halt per `--on-divergence: halt|live` (default halt) |
| Replayed agent exceeds budget | The replay principal has its own small budget; halt with `budget` — replays cannot starve prod |
| Sampler finds zero matching sessions | `DatasetReady=False` upstream (16 §5) — loud |

## 8. Security

Replay principals are synthetic (`replay:<user>@<target>` in receipts — replays are themselves receipted, so the audit trail is recursive and complete). Replaying a session requires read access to that tenant's stream (design 08's read-only credential) **plus** invoke rights on the target agent (ReBAC when 24 lands). Mocked replay never contacts real tools — the mock route is the only backend the replay principal can reach (compiled, fail-closed). Exports carry origin digests so a tampered fixture is detectable.

## 9. Testing

Reconstruction golden tests from fixture streams (incl. gap and multi-agent lineage cases); re-drive e2e (recorded task → candidate → receipts show the new run); mocked-replay determinism (same session, two replays ⇒ identical hop sequence) + divergence fixture (modified agent ⇒ `diverged_at` correct); scrub assertions on export; sampler strata correctness + exclusion counting.

## 10. Decisions for async review

- **D1 — Sessions are views, never copies** (stream + object store are the single source; retention governs replayability honestly).
- **D2 — Two replay modes with declared fidelity**: re-drive (regression) and mocked (debugging); KG hops replay against pinned versions live instead of mocks — immutability makes mocks unnecessary exactly where correctness matters most.
- **D3 — Divergence is a finding, not an error** — the debugging mode's primary output.
- **D4 — Replays are receipted like everything else** (synthetic principals; recursive audit).

## 11. Resulting ADRs

ADR-0024 (P3) after critique PASS.
