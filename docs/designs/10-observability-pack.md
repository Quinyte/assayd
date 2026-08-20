# Design 10: Observability pack (OTel wiring, dashboards, golden signals, alerts)

- **Status**: draft — awaiting critique
- **Phase**: P1+ · **Size**: M · **Date**: 2026-08-20
- **ADRs**: 0002, 0003 (semconv SHA pin — ADR-0021), 0021 (single telemetry path) · interfaces: 04 (the tap fans out to OpenObserve), 07 (chart ships this), 20-drift (consumes these metrics)

## 1. Purpose & scope

Everything a operator sees: the OpenObserve deployment, the telemetry routing, the golden-signal metric definitions, shipped dashboards and alert rules. In scope: pipeline topology, signal catalog, dashboard/alert content as versioned data, retention. Out of scope: receipts (04 owns; this consumes the same export), drift *actions* (20).

## 2. Doctrine & charter gates

- **Plane**: mixed, split cleanly: the *pipeline topology* is slow (engine wiring); **dashboards, alert rules, and signal definitions are fast-plane data** shipped as chart values / a builtin pack — new signals or panels never need a platform release.
- **Pods**: OpenObserve 1 (budgeted §17). Phoenix optional (`plus`). **Stateful deps**: OpenObserve uses object storage/local disk — it is the observability *sink*, explicitly not a platform-of-record (receipts are; ADR-0021). Losing it loses graphs, never audit. ✓

## 3. Topology — one path, three signals

```
agents (optional interior OTel: OpenLLMetry, pinned semconv SHA)
   └─OTLP──► gateway ──OTLP (traces+metrics)──► tap (design 04)
                                                 ├─► JetStream receipts (enforced hops only)
                                                 └─► OpenObserve (traces + metrics + logs)
operators/CLI logs ──OTLP/file──────────────────────► OpenObserve
```

- Everything OTLP; nothing scrapes agents. The tap is the single fan-out (ADR-0021 D1) — observability and audit cannot drift.
- Interior agent spans (reasoning steps) land in OpenObserve joined by `trace_id` to gateway hop spans; they are never receipts.
- Semconv: the same pinned SHA as design 04 everywhere; dashboards reference attributes through a **name-map layer** (one values file) so a semconv rename is a one-file change, not forty panel edits.

## 4. The golden-signal catalog (fast-plane data, versioned)

Derived at the tap/OpenObserve from hop spans — definitions live in one `signals.yaml`:

| Signal | Definition | Feeds |
|---|---|---|
| `task_success_rate` | terminal A2A task status ok ÷ total, per agent/revision | drift SLO burn (20), rollout observe |
| `tool_error_rate` | hop status error ÷ tool hops | alerts |
| `tokens_per_task`, `usd_per_task` | usage sums per task (null-pricing excluded, counted) | budget panels, ADR-0020 backstop visibility |
| `latency_per_hop` p50/p95 | by hop.type | perf panels |
| `loop_depth` | max lineage.depth per task | loop governance (22) |
| `handoff_count` | a2a hops per task | swarm visibility |
| `termination_reasons` | counter by named reason (09 templates emit) | "goal_met falling" drift signal |
| `receipts_health` | gap counter, late_redelivery, index lag (04) | ReceiptsDegraded alert |

## 5. Shipped dashboards & alerts (content, not aspiration)

Dashboards (OpenObserve JSON, chart-shipped, name-mapped): **Fleet** (agents × phase/eval/cost/success), **Agent drill-down** (signals + recent receipts link), **Task trace** (gateway + interior spans joined), **Platform health** (operator, tap, gateway, SPIRE, IdP probes), **Budget** (spend vs limits, PricingStale flags). Alert rules (shipped, tunable): success-rate SLO burn (page), `ReceiptsDegraded` (page), candidate held >1h (ticket), tool-error spike (ticket), `termination_reason != goal_met` rising (ticket), cert/SVID issuance failures (page), backstop staleness >60s (ticket). Every alert names the condition/design it maps to — alerts and CR conditions tell one story.

## 6. Retention & profiles

Core: traces 7d, metrics 30d, logs 7d (values-tunable). `local` profile: 24h everything, memory-lean single replica. Compliance profiles do **not** extend OpenObserve retention (audit lives in receipts — 04); they may *shorten* trace body retention instead (PHI surface reduction).

## 7. Failure modes

| Failure | Behavior |
|---|---|
| OpenObserve down | Tap's forward branch buffers/drops with metric (04); receipts unaffected; `doctor` flags; dashboards blind but platform blind-flying is visible via CR conditions (`kubectl get agents` still tells the truth — by design) |
| Semconv rename wave | Name-map layer + tap transform absorb; dashboards updated by data change |
| Signal definition bug | signals.yaml is versioned data — fix ships as chart/pack patch, no binary release |
| Cardinality blowup (per-task labels) | Signals aggregate per agent/revision, never per task-id in metrics; task granularity lives in traces only — stated rule |

## 8. Testing

Rendered-dashboard schema validation in chart CI; alert-rule unit tests against synthetic metric fixtures (each shipped alert must fire on its fixture and stay silent on baseline — alerts are tested code, not YAML hope); e2e: drive a task, assert the Fleet dashboard's queries return it and `receipts_health` is green.

## 9. Decisions for async review

- **D1 — OpenObserve is sink, never system-of-record**; compliance never extends its retention.
- **D2 — Signal/dashboard/alert content is fast-plane data** behind a semconv name-map — a rename or new signal is a data patch.
- **D3 — Metrics never carry per-task cardinality**; task granularity is traces-only.
- **D4 — Alerts ship tested** (fixture-fired in CI) and map 1:1 to conditions/designs.

## 10. Resulting ADRs

Folded into ADR-0022 after critique PASS.
