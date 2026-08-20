# Design 04: Receipt tap + receipt envelope (`receipt/v1`)

- **Status**: **approved** — critique PASS at r2 (reviews/04-review.md) · ADR-0021
- **Phase**: P1 · **Size**: M · **Date**: 2026-08-20
- **ADRs**: 0002, 0003 (semconv — see pin note §3.1), 0010 (principal chain), 0013 (tenancy), 0014 (compliance hooks) · interfaces: 03 (OTLP config + pricing + **budget backstop consumer**), 16/17 (fidelity consumers), 26 (tenant fan-out seam)
- **Research**: `docs/research/agentgateway-2.2-2026-08.md` (OTLP export claims) · `docs/research/otel-genai-semconv-2026-08.md` (pin mechanism)

## 1. Purpose & scope

Every hop through the gateway becomes a durable, replayable **receipt** — audit log, debugging substrate, and eval-data collector in one stream. In scope: envelope schema, tap pipeline, identity/dedup, storage/tenancy, capture levels, the consumer contracts (including the design-03 budget backstop). Out of scope: replay mechanics (17), compliance hash-chaining/retention specifics (ADR-0014; hooks here).

## 2. Doctrine & charter gates

- **Plane**: slow. **Pods**: 0 in core — tap is a listener inside the agent-operator binary; `--mode receipt-tap` split at documented thresholds and under compliance profiles. The **operator-coupling risk is designed for** (§5, r1 f6): hard per-subsystem caps, shed-telemetry-first priority.
- **Stateful deps**: JetStream + Postgres (existing). **Primitives**: Event (CloudEvents). Body refs are internal storage, *not* the OCI Artifact primitive (r1 f8). ✓

## 3. Interfaces

### 3.1 Ingest: the gateway's own OTel export

agentgateway natively exports OTLP traces (GenAI conventions incl. MCP tool spans with params/results/latency, prompt logging, cost attributes) — the tap is an **OTLP receiver, not a proxy filter**; it fans out: (a) transform → receipts → JetStream; (b) forward raw OTLP → OpenObserve. One telemetry path, no audit/observability drift. `hop.type` (`kg_query` vs `mcp_tool` etc.) is derived from the backend class stamped into span attributes by design-03-emitted resources (one clause the r1 review asked for). Export-gap detection: the gateway's **OTLP metrics** ship on the same pipe; the tap consumes its exporter-drop counters (specified ingest path, r1 f9).

**The semconv "pin", made real (r1 f2)**: `gen_ai.*` conventions are Development-stability, and as of June 2026 they moved to `open-telemetry/semantic-conventions-genai`, which has **no releases** — a version pin is not currently executable. The pin is therefore a **commit-SHA snapshot** of that repo (recorded in this design, the transform's code, and the golden-fixture set name), with monolithic **v1.41** as the frozen fallback baseline. The repo-split rename wave is a known absorb-in-transform event. Research note updated; ADR-0021 states the mechanism; ADR-0003's wording gains a clarifying note.

### 3.2 Envelope `receipt/v1` — deterministic identity (r1 f1, BLOCKER fix)

```json
{
  "receipt_version": "v1",
  "receipt_id": "uuidv5(ns=plume-receipt, name=trace_id+span_id)",   // DERIVED, not minted
  "task_id": "a2a-task-…", "trace_id": "…", "span_id": "…",
  "tenant": "acme", "ns": "claims",
  "principal_chain": ["user:jag@acme", "agent:pa-reviewer@7f3a2"],
  "hop": {"type": "llm|mcp_tool|a2a|kg_query|http", "target": "…", "status": "ok|error|denied|budget_exceeded", "latency_ms": 412},
  "usage": {"tokens_in": 1810, "tokens_out": 220, "usd_est": 0.011, "pricing": "resolved|unresolved"},
  "capture": {"level": "metadata|headers|full", "request_ref": "obj://…", "response_ref": "obj://…"},
  "lineage": {"depth": 3, "path": ["pa-intake", "pa-reviewer"]},
  "ts": "…"
}
```

- **`receipt_id` is a pure function of the span** — every re-transform of a redelivered batch yields the same ID; `Nats-Msg-Id: receipt_id` then actually dedupes. Object-store keys derive the same way (idempotent overwrite-same-content).
- **Dedup window sized to the retry horizon**: stream `Duplicates` = gateway exporter max retry horizon (default config: 10m exporter horizon → 15m window). Memory: ~150B/entry → at the 500/s split threshold, 15m ≈ 450k entries ≈ **~70MB** — stated, budgeted, and alarmed; window and horizon ship as one tunable pair.
- `usd_est` from the design-03 pricing table; **unresolvable pricing ⇒ `usd_est: null` + `pricing: "unresolved"` + counter** — never 0; aggregates treat null as unknown (r1 f11).
- Bodies >8KB offload; `capture.level` compiled per-agent; unknown fields tolerated (additive; breaking ⇒ `receipt/v2`, N/N−1).

### 3.3 Storage & tenancy (r1 f3)

**Per-tenant `RECEIPTS` stream in the tenant's NATS account** — isolation is the account boundary, as ADR-0013 promises for the audit log, not subject-prefix discipline. Core (single-team OSS) runs exactly one tenant account (`default`), so core and enterprise are the *same* model at n=1; the multi-account credential fan-out is the design-26 (Tenant CR) seam. Subjects within a stream: `receipts.<ns>.<agent>.<hop_type>`. Retention 30d core; compliance overrides per-tenant (6y tiering + `chain: {prev_hash, hash}` reserved fields).

### 3.4 Consumers — two classes, three named consumers (r1 f4)

| Consumer | Class | Reads | Contract |
|---|---|---|---|
| Eval sampler (16), replay (17), `plume session *` | **Fidelity** | durable JetStream consumers on the stream — *never* the index | replay is exactly what was recorded |
| Audit-index projector (this design, same binary) | Aggregate | stream → Postgres rows `(receipt_id, task_id, tenant, agent, principal, ts, status, usd_est, pricing)` | rebuildable; lag metric |
| **Budget backstop (design 03 D3)** | Aggregate | the projector-maintained **per-agent daily spend aggregate** (00:00 UTC windows) read by the operator each reconcile | **staleness bound stated: backstop trail = reconcile interval + index lag (alarmed at >60s)** — this is what makes 03's "cutoff may trail" claim honest. Nulls count as unknown, surfaced in status |

The "never the index" rule applies to fidelity consumers only (clarified per r1 f4).

## 4. Behavior

Stateless transform per batch: group by task → map attributes (pinned snapshot) → derive IDs → scrub → offload → publish with `Nats-Msg-Id`. At-least-once ingest + deterministic IDs + sized window ⇒ effectively-once append within the stated horizon; beyond-horizon redelivery is possible and **counted** (`late_redelivery` metric) rather than denied.

## 5. Failure modes & degraded states

| Failure | Behavior |
|---|---|
| Tap unreachable | Gateway OTLP buffers/retries (bounded); sustained outage ⇒ possible loss, surfaced as `ReceiptsDegraded` on affected Agents via exporter-drop counters — never silent. Compliance: split-mode + 2 replicas + horizon-sized buffers |
| **Tap load degrades operator** (r1 f6) | Hard caps per subsystem (ingest queue, transform mem, offload buffer); **priority: shed telemetry before starving reconcile** (drops visible via `ReceiptsDegraded`); split-trigger alert (p95 transform >250ms or >500/s) fires *before* coupling becomes dangerous — the thresholds exist for this risk |
| JetStream publish fails | Bounded disk-backed buffer + retry; overflow drop-oldest + counter + `ReceiptsDegraded` |
| Redelivery beyond dedup window | Duplicate possible; `late_redelivery` counter + alert (window/horizon mistuned) |
| OpenObserve down | Receipts unaffected; forward branch buffers/drops with metric |
| Object-store failure | Receipt ships `request_ref: null` + `truncated` |
| Pricing unresolved | `usd_est: null` path (§3.2) |
| Index lag/loss | Rebuildable; lag metric; backstop staleness alarm |

## 6. Security

Tap listens mTLS, gateway SVID only. **Mandatory, non-configurable credential scrub at every capture level** (r1 f5): denylist (`Authorization`, `Proxy-Authorization`, `Cookie`, `Set-Cookie`, `X-Api-Key`, gateway auth headers) applied **gateway-side before export** (transform policy emitted by design 03) *and* re-checked in the tap (defense in depth) — the stream never contains live credentials in any profile. PHI redaction remains profile-driven and gateway-side. Bodies tenant-scoped by account; append-only; no delete API in core.

## 7. Observability

receipts/s, transform latency, publish failures, offload rate, dedup hits, `late_redelivery`, gap counter, index lag, per-subsystem cap pressure. Alerts: `ReceiptsDegraded`, gap>0 sustained, lag>60s, cap pressure, window mistuning. The tap never receipts itself.

## 8. Testing

Golden fixtures per pinned snapshot (+ dual-fixture upgrade test). **New (r1 f12)**: redelivery test — same batch twice, one inside window (one append) and once beyond (counted duplicate); tenant isolation (account A cannot read B's stream); credential-scrub fixture (every denylisted header absent at all capture levels). e2e: task → receipts with correct chain/cost/refs; tap kill/recover; backstop aggregate correctness incl. null-pricing receipts.

## 9. Decisions for async review

- **D1** — receipts derive from the gateway's export (interior agent spans enrich OpenObserve but are never receipts; receipts = enforced hops). Unchanged, now cited.
- **D2** — embedded-in-operator core / split-mode escalation, now with explicit blast-radius engineering (caps + shed priority).
- **D3** — delivery guarantee restated honestly: *effectively-once within a sized, alarmed dedup horizon*; tiered (core visible-gaps / compliance zero-gap sizing).
- **D4** *(new)* — per-tenant streams in tenant accounts; core = same model at n=1.
- **D5** *(new)* — semconv pin = commit-SHA snapshot (unreleased upstream), v1.41 fallback baseline.

## 10. Resulting ADRs

ADR-0021 after re-critique PASS (receipt/v1 identity + dedup horizon, tenancy streams, semconv pin mechanism, credential scrub, tiered delivery).

## 11. Amendments

- **A1 (2026-08-20, design 10 r1 f1)**: the tap runs two listeners — the **gateway-SVID-authenticated export listener** (sole receipt source) and a **forward-only listener** for agent interior OTLP (never mints receipts, regardless of span shape). Receipt discrimination is transport-derived; conformance test: gateway-lookalike agent spans ⇒ zero receipts.
