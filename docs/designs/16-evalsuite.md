# Design 16: EvalSuite CRD, gate controller, dataset builder

- **Status**: draft — awaiting critique
- **Phase**: P3 · **Size**: L · **Date**: 2026-08-20
- **ADRs**: 0006 (eval-as-admission), 0019 (revision holding), 0020 (candidate isolation), 0021 (receipts) · interfaces: 02 (rollout machine), 12 §3.8 (golden-set seeds), 15 (probe/eval distinction), 17 (session sampling), 18 (runner images via packs)
- **Research**: `docs/research/evals-2026-08.md` — DeepEval 4.x: programmatic `evaluate()`/`evals_iterator()` APIs, built-in agentic metrics (task completion, tool correctness), pytest-style CI integration; Inspect AI as the second runner.

## 1. Purpose & scope

The flagship: semantic admission (FR-20). A new Agent/Model revision earns traffic by passing its EvalSuite against the candidate *through the production path*. In scope: the EvalSuite CRD, the **EvalRunner slot** (`evalrunner/v1`), the gate controller's interaction with design 02's rollout machine, the dataset builder, metric semantics, verdict rules, nightly trend runs. Out of scope: session substrate (17), drift consumption of trends (20), runner pack packaging (18).

## 2. Doctrine & charter gates

- **Plane**: slow (gate controller = part of the agent-operator; the *runner* is a provider slot, fast-plane images; datasets/metrics/thresholds are data).
- **Pods**: 0 standing — eval runs are **Jobs**; nightly runs are CronJobs. **Stateful deps**: Postgres (eval results — substrate per ADR-0002), object store (dataset + report artifacts). ✓
- **Primitives**: Resource (CRDs), Artifact (datasets/reports as content-addressed OCI artifacts), Agent (the runner drives real A2A tasks), Event (`eval.completed`). ✓ **EvalRunner joins the charter's reserved-slot list as shipped** (architecture §15 erratum, like IdentityProvider before it).

## 3. The EvalSuite CRD

```yaml
kind: EvalSuite
metadata: {name: pa-regression, namespace: claims}
spec:
  runner: {type: deepeval}                # evalrunner/v1 slot: deepeval | inspect | byo image
  target: {agentRef: pa-reviewer}         # what this suite gates (or modelRef, P5)
  dataset:
    goldenSetRef: pa-golden-v3            # curated + ontology-derived (§5); content-addressed artifact
    fromSessions: {sample: 200, since: 7d, strata: {by: hop_outcome}}   # design 17 sampler
  metrics:
    - {name: task_completion, threshold: 0.85}
    - {name: tool_correctness, threshold: 0.9}
    - {name: faithfulness_to_kg}          # mechanical: citations verified via kg.cite (§6)
    - {name: cost_regression, maxIncrease: 15%}
    - {name: custom_geval, judge: {model: gpt-x@pinned, promptRef: pa-judge-v2}}   # LLM-judge, pinned
  gate: {minScore: 0.85, blocking: true}
  budget: {tokensPerRun: 500k, usdPerRun: 5}    # the eval principal's own budget (ADR-0020 pattern)
status:
  lastRun: {report: oci://…@sha256:…, datasetDigest: …, judgeDigest: …, scores: {…}, verdict: pass}
  conditions: [DatasetReady, LastRunPassed]
```

## 4. The gate flow (with design 02's rollout machine)

1. Candidate reaches `Held` (02 §3.3) → gate controller sees `gates:` on the Agent CR → `DatasetReady?` (build/refresh if stale, §5) → launches the **eval Job**.
2. The Job drives **real A2A tasks through the gateway** at the candidate header-route (bound to the gate controller's SVID — 02 review f2): eval traffic exercises the identical path as prod, and every eval task produces **receipts**, which is where tool/cost metrics come from (§6).
3. Runner writes the structured report (per-case results + scores) → report artifact (content-addressed) + Postgres rows + CR status; verdict per §7.
4. **pass** → controller signals the operator: weight shift begins (canary steps). **Canary progression is judged on golden signals (SLOs), not re-evaluation** — the eval gate runs once, pre-canary; live-traffic degradation is the drift/SLO machinery's job (10/20). Stated to kill scope creep.
5. **fail** → candidate deleted (02), report linked in status + PR annotation; `GatesPassed=False` with the failing metrics named.
6. **Nightly**: CronJob runs the full suite against the *active* revision → trend rows (drift feed, 20); never gates.

## 5. The dataset builder

Datasets are **content-addressed artifacts** — a report always names the exact dataset digest it ran against (reproducibility is a first-class property):

- **Ontology-derived seeds**: design 12 §3.8 — one case per probe, `via` + matchers carried verbatim. These are *floor* cases (the graph's known answers, asked through the agent).
- **Curated cases**: `golden/` files in the agent's repo (the design-09 golden-task test is the seed), promoted via `plume eval promote <session>`.
- **Session-derived cases** (17): stratified sample of recorded production tasks (inputs replayed; recorded outcomes as reference). Sampling is pinned at build time — the dataset artifact embeds the chosen sessions, so reruns are stable.
- Builder runs as part of the eval Job's first step (no standing pod); rebuilt when refs change or `since:` windows roll; `DatasetReady=False` names what's missing (e.g. zero sessions matching strata — loud, not empty-pass).

## 6. Metric semantics (the honest column)

| Metric | Source | Nature |
|---|---|---|
| `task_completion` | runner judgment per case (DeepEval agentic metric / matchers for ontology-derived cases) | judged (LLM for open cases, mechanical for `via`-matched) |
| `tool_correctness` | **receipts of the eval run** — expected vs actual tool-call sequences | mechanical |
| `faithfulness_to_kg` | every citation in the agent's answer resolved via `kg.cite` on the *pinned graph version*; uncited claims counted | mechanical |
| `cost_regression` | eval-run receipts vs the active revision's trailing per-task cost | mechanical |
| `custom_geval` etc. | LLM-judge with **pinned model + versioned prompt**; judge config digest in the report | judged |

Judge calls go through the gateway under the eval principal (receipted, budgeted). **Flake policy**: infra-failed cases retry ×1; semantically-failed cases never retry (that's the signal). Agent stochasticity is bounded by convention (templates default eval-mode temperature) but not assumed: the verdict rule (§7) tolerates case-level noise via the threshold, not reruns-until-green — rerun-shopping is structurally impossible because the report binds (dataset digest, judge digest, candidate revision).

## 7. Verdict rule

`gate.minScore` applies to the **weighted mean of metric scores** (equal weights v1); each metric's own `threshold` is a hard floor — one floor breach fails regardless of the mean. Blocking suites hold the rollout (02); non-blocking suites annotate only. A suite with zero cases **fails closed** (`DatasetReady=False` — an empty eval must never pass an agent).

## 8. The `evalrunner/v1` slot

A runner is a Job image implementing: input `(dataset artifact ref, target endpoint + credentials, metric config, budget)` → output `(report JSON schema evalreport/v1: per-case {id, input_digest, outcome, per-metric scores, receipts refs}, summary scores)`. DeepEval adapter first (its `evaluate()` API maps directly); Inspect AI second; `byo` = any image honoring the contract. Conformance: a fixture dataset + mock agent where expected scores are known; parity across runners on the mechanical metrics (judged metrics are runner-specific by nature — documented, not hidden).

## 9. Failure modes

| Failure | Behavior |
|---|---|
| Runner Job crash | Job restart; per-case idempotency by case id (re-running completed cases is safe — results content-addressed) |
| Judge model unavailable | Judged metrics `error`, mechanical metrics complete; verdict **fails closed** with `JudgeUnavailable` named |
| Eval budget exhausted | Run halts; verdict fail-closed (`budget`); operator alert — an eval that can't afford to finish never passes anyone |
| Dataset refs missing/stale | `DatasetReady=False`, rollout stays Held, loudly |
| Candidate crashes under eval | Receipts show it; verdict fail with the crash cases named — that's the gate working |
| Nightly run fails | Trend gap marked; never affects serving |

## 10. Security

Eval Jobs hold: the candidate-route credential (gate-controller SVID), read access to the pinned KG version, the eval budget. They never hold prod user tokens (synthetic principals: `eval:<suite>@<candidate>` in receipts). Reports may embed prompts/outputs → stored at the receipts capture level, same redaction rules (ADR-0014).

## 11. Testing

Fixture agent (scripted A2A responder) + fixture suite: pass/fail/floor-breach/empty-dataset/judge-down/budget-exhausted paths; report reproducibility (same digests ⇒ same verdict); e2e on k3d: the flagship demo — `plume deploy` streams `HELD → eval 0.89 ✓ → canary 10% → 100%`, then a deliberately-broken revision fails with the report in the PR annotation.

## 12. Decisions for async review

- **D1 — The eval gate runs once, pre-canary; canary progression is SLO-judged** (no re-eval mid-rollout) — scope pinned.
- **D2 — Datasets and reports are content-addressed artifacts**; a verdict always names (dataset, judge, revision) digests — rerun-shopping structurally impossible.
- **D3 — Fail-closed everywhere**: empty dataset, judge down, budget out ⇒ the candidate does not pass.
- **D4 — EvalRunner is a provider slot** (`evalrunner/v1`), DeepEval first, conformance with cross-runner parity on mechanical metrics.

## 13. Resulting ADRs

ADR-0024 (P3) after critique PASS.
