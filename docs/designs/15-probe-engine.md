# Design 15: Probe engine (semantic readiness)

- **Status**: revised r2 — awaiting re-critique (r1 REVISE, 4 findings addressed; reviews/15-review.md)
- **Phase**: P2 · **Size**: M · **Date**: 2026-08-20
- **ADRs**: 0005 (semantic readiness), 0017 (`kg.probe`), 0007 (drift seam) · interfaces: 01/13 (executes via `kg.probe`), 12 (probe content), 14 (promotion gate), 20 (continuous mode feeds drift)

## 1. Purpose & scope

The machinery that makes "Ready = the probe set passes" true (FR-12): runs probe sets against graph versions, writes the conditions, gates promotion, and — in continuous mode — feeds the drift controllers. In scope: execution model, scheduling, verdict semantics, flake handling, condition wiring. Out of scope: probe *content* (12), remediation (20).

## 2. Doctrine & charter gates

- **Plane**: slow (a controller concern inside the agent-operator — no new pod; probe *sets* are ontology data, fast-plane).
- **Pods**: 0 — probes execute as **short-lived Jobs** (promotion-time) and a **CronJob** (continuous mode) calling `kg.probe` **through the gateway** with a platform identity (ADR-0017: agents never call it; the platform does — and via the gateway so probe runs are receipted like everything else). **Stateful deps**: results in the KG CR status + a bounded history in Postgres (existing; trend queries). ✓
- **Primitives**: Tool (kg.probe via MCP), Resource, Event (probe.completed CloudEvents). ✓

## 3. Execution model

### 3.1 Promotion-time (the gate, design 14 handoff)

After `commit_version` succeeds: the operator launches a probe Job that executes the **full probe set 100% engine-side via the public query surface** (r1 f2 — at the one-shot admission decision, the provider grades nothing): each probe's `via` names its execution (r1 f1) and the engine judges matchers against the defined output shape. Verdict:

- **pass** (`pass_rate ≥ the ontology's health.pass_threshold` — design 12 §11 A1; the KG CR may tighten, never loosen (r1 f3); default 1.0 for hand-written known-answer probes — a failing known answer is a defect, not a statistic): operator may `promote`; `ProbesPassing=True`.
- **fail**: staging stays unpromoted (quarantine-adjacent — inspectable via its version endpoint under platform identity); failing probe ids + expected/actual in the KG CR status.

### 3.2 Continuous mode (the drift feed)

A CronJob (default hourly, KG CR-tunable) runs the probe set against the **active** version. Semantics differ deliberately: the active version passed at promotion, so failures now mean *the world moved or the graph rotted* → `KnowledgeStale=True` with the failing subset; design 20 owns remediation (typically: trigger a rebuild — design 14). Pass after fail clears the condition (the same-detector-verifies rule, ADR-0007).

### 3.3 Verdict mechanics — stakes-split verification (r1 f2)

- **Probes are mechanical by construction (r1 f1)**: `via` is **required** (design 12 §11 A1 — a probe without `via` fails validation); `via: {bundle, params}` matches against the bundle's `text`/`citations`, `via: {search: {query, top_k}}` against hit fields. `q` is the human-readable label (dashboards, reviews) — never the executable.
- **Promotion-time: 100% engine-side execution** through the public query surface; the provider self-grades nothing at admission.
- **Continuous mode: the cheap path** — `kg.probe` self-report with a **20% engine-side re-verification sample per run** (sound for a repeated game); any discrepancy ⇒ full re-verification. `kg.probe` stays in the contract unchanged (ADR-0017): the provider's self-check, the conformance suite's **standing parity assertion** (self-report must equal engine-side results), and continuous mode's workhorse.
- **Flake policy**: a failing probe retries ×2 with backoff inside the run; still-failing ⇒ real. Probes are deterministic queries against a frozen version — persistent flake means infra trouble, surfaced as `ProbeInfraError` (distinct from a semantic fail), which does **not** set `KnowledgeStale` (don't page domain owners for network blips).
- Latency: per-probe `latency_ms` recorded; p95 trend feeds the observability pack (a slowing graph is pre-drift signal).

## 4. Scheduling & cost

Probe sets are small by design (10s–100s); promotion runs are on-demand; continuous default hourly with jitter. Probe traffic passes the gateway under the platform probe identity with its **own budget — declared as `probes.budget` on the KG CR** (r1 f4, mirroring design 14's `build.budget`); receipts attribute `principal: probe:<graph>@<version>`. A probe storm cannot starve agent budgets. Continuous mode pauses automatically while a rebuild of the same graph is in flight (no probing a graph mid-replacement — noise).

## 5. Failure modes

| Failure | Behavior |
|---|---|
| Provider unreachable | `ProbeInfraError` (not `KnowledgeStale`); retries; sustained ⇒ the KG's own `Ready` degrades via design 01 §5 anyway |
| Probe set empty (ontology has none) | `ProbesPassing=Unknown` + loud install-time warning; promotion allowed only with `--allow-unprobed` (explicit, logged) — never silently green |
| Self-report vs re-verify discrepancy (continuous mode) | Full re-verification; persistent ⇒ `ProviderUntrusted` + page. Promotion is immune by construction (engine-side, r1 f2) |
| Engine Job evicted | Idempotent re-run (probes are read-only) |
| Threshold edited mid-flight | Next run uses the new ontology version's threshold; runs are pinned to the doc they started with |

## 6. Observability

`probe_pass_rate` per graph/version (trend — the drift feed), per-probe latency p95, `ProbeInfraError` counter, re-verify discrepancy counter (should be forever zero). Shipped alerts: `KnowledgeStale` (ticket), `ProviderUntrusted` (page), pass-rate declining 3 consecutive runs while still above threshold (early-warning ticket — drift before failure).

## 7. Testing

Fixture graph + probe set: **`q`-only probe fails validation; promotion verdicts produced with provider self-report disabled entirely** (r1 f1/f2); promotion pass/fail paths; continuous-mode transition (pass→fail→`KnowledgeStale`→pass→clear); flake vs real (injected network fault ⇒ `ProbeInfraError`, injected wrong answer ⇒ semantic fail); self-report lie fixture (adapter modified to misreport ⇒ re-verify catches, `ProviderUntrusted`); unprobed-ontology warning path; budget isolation (probe storm ⇒ agent budgets untouched).

## 8. Decisions for async review

- **D1 — Known-answer probes default to threshold 1.0** (a failing known answer is a defect); statistical thresholds are for `generate:`-expanded sets, per ontology.
- **D2 — Stakes-split verification**: promotion = 100% engine-side (provider grades nothing at admission); continuous = self-report + 20% re-verify + parity conformance (r1 f2).
- **D3 — Infra failures never masquerade as staleness** (`ProbeInfraError` ≠ `KnowledgeStale`).
- **D4 — Unprobed graphs can only promote explicitly** (`--allow-unprobed`, logged) — semantic readiness cannot be silently vacuous.

## 9. Resulting ADRs

Folded into ADR-0023 after critique PASS.
