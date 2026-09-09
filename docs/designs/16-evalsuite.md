# Design 16: EvalSuite CRD, gate controller, dataset builder

- **Status**: **approved** — critique PASS at r2 (reviews/16-review.md) · ADR-0024
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
  budget: {tokensPerRun: 500k, usdPerRun: 5}    # the eval principal's own budget (ADR-0028 pattern)
status:
  lastRun: {report: oci://…@sha256:…, datasetDigest: …, judgeDigest: …, scores: {…}, verdict: pass}
  conditions: [DatasetReady, LastRunPassed]
```

**A revision reference is `{name, digest}` everywhere in this design (A2).** Design 02 A37 separated the two: the ten-character `name` is what a workload is called and what `kubectl get agents` prints, and the **full digest** is the identity. Forty bits is not a security boundary against an attacker-controlled projection — a chosen collision between a safe and a malicious spec was found in **1.2 seconds** — and this design is where that distinction earns its keep, because a verdict here is what grants production traffic.

Comparing a bare `revision` name would let a colliding projection be promoted on the verdict a different one earned, and that decision is made **before any workload is inspected**, so design 02's workload-level collision guard never sees it. The typed reference is therefore carried through every step below: the EvalRun input, the report's bound digests, `GatesPassed`, the candidate route's admitted principal, the events, and the receipt attribution. A step that carries only the name is a step where the gate can be spent on the wrong revision.

**Owed:** the `EvalSuite`/`EvalRun` CRDs do not exist yet, so this is a contract for the types when they are written rather than a description of a schema. Design 02 already carries the digest in `status.evalStatus.revisionDigest` and `status.cards[].revisionDigest`, which is the half that could be built without them (design 02 A50).

## 4. The gate flow (with design 02's rollout machine)

1. Candidate reaches `Held` (02 §3.3) → gate controller sees `gates:` on the Agent CR → `DatasetReady?` (build/refresh if stale, §5) → launches the **eval Job**. The controller reads `status.candidateRevisionDigest`, not `status.candidateRevision`, and the EvalRun records both (A2).
2. The Job drives **real A2A tasks through the gateway** at the candidate header-route. **Identity model (r1 f1)**: SVIDs are per-pod attested identities, never lent — the eval Job pod gets **its own SVID** via the existing label template (`spiffe://…/eval/<suite>/<run>`), and the compiler adds that principal to the candidate route's **admitted set at Job launch, removing it at Job end** — a narrow, temporary, compiled grant (the design 01 A2 pattern applied to routes; recorded as a design 03 row + ADR-0028 note). The gate controller never proxies eval traffic. **Runner trust bar** (runners are third-party pack images): signed image required (18), and the eval principal's compiled reachability is exactly {candidate route, pinned KG version, judge LLM egress} — fail-closed; the budget bounds the blast radius. Eval traffic exercises the identical path as prod, and every eval task produces **receipts** (§6).
3. Runner writes the structured report (per-case results + scores) → report artifact (content-addressed) + Postgres rows + CR status; verdict per §7.
4. **pass** → controller signals the operator, naming the **digest** it evaluated: the operator promotes only if that digest still matches its own candidate, so a verdict cannot be spent on a revision that changed underneath it (A2). Weight shift begins (canary steps). **Canary progression is judged on golden signals (SLOs), not re-evaluation** — the eval gate runs once, pre-canary; live-traffic degradation is the drift/SLO machinery's job (10/20). Stated to kill scope creep.
5. **fail** → candidate deleted (02), report linked in status + PR annotation; `GatesPassed=False` with the failing metrics named.
6. **Nightly**: CronJob runs the full suite against the *active* revision → trend rows (drift feed, 20); never gates.

## 5. The dataset builder

Datasets are **content-addressed artifacts** — a report always names the exact dataset digest it ran against (reproducibility is a first-class property):

- **Ontology-derived seeds**: design 12 §3.8 — one case per probe, `via` + matchers carried verbatim. These are *floor* cases (the graph's known answers, asked through the agent).
- **Curated cases**: `golden/` files in the agent's repo (the design-09 golden-task test is the seed), promoted via `assayd eval promote <session>`.
- **Session-derived cases** (17): stratified sample of recorded production tasks (inputs replayed; recorded outcomes as reference). Sampling is pinned at build time — the dataset artifact embeds the chosen sessions, so reruns are stable.
- Builder runs as part of the eval Job's first step (no standing pod); rebuilt when refs change or `since:` windows roll; `DatasetReady=False` names what's missing (e.g. zero sessions matching strata — loud, not empty-pass).

## 6. Metric semantics (the honest column)

| Metric | Source | Nature |
|---|---|---|
| `task_completion` | runner judgment per case (DeepEval agentic metric / matchers for ontology-derived cases) | judged (LLM for open cases, mechanical for `via`-matched) |
| `tool_correctness` | **receipts of the eval run** — normative v1 variant: **target-sequence match** (works at any capture level); argument-aware matching available when the eval posture captures bodies | mechanical |
| `faithfulness_to_kg` | every citation in the agent's answer resolved via `kg.cite` on the *pinned graph version*; uncited claims counted | mechanical |
| `cost_regression` | eval-run receipts vs the active revision's **trailing 7-day median per-task cost** (from the design-04 aggregate; window/statistic stated in the report) (r1 f4) | mechanical |
| `custom_geval` etc. | LLM-judge with **pinned model + versioned prompt**; judge config digest in the report | judged |

**Eval-principal posture is pinned independently of the agent's** (r1 f2): capture `full` on eval-principal traffic (reports store at receipt capture rules) and KG scope mirroring the target agent's — compiled with the temporary grant, so `kg.cite` resolves and argument-aware checks are possible. Judge calls go through the gateway under the eval principal (receipted, budgeted). **Flake policy**: infra-failed cases retry ×1; semantically-failed cases never retry (that's the signal). Agent stochasticity is bounded by convention (templates default eval-mode temperature) but not assumed: the verdict rule (§7) tolerates case-level noise via the threshold, not reruns-until-green — rerun-shopping is structurally impossible because the report binds (dataset digest, judge digest, candidate revision).

## 7. Verdict rule

`gate.minScore` applies to the **weighted mean of metric scores** (equal weights v1); each metric's own `threshold` is a hard floor — one floor breach fails regardless of the mean. Blocking suites hold the rollout (02); non-blocking suites annotate only. A suite with zero cases **fails closed** (`DatasetReady=False` — an empty eval must never pass an agent).

## 8. The `evalrunner/v1` slot

A runner is a Job image implementing: input `(dataset artifact ref, target endpoint + credentials, metric config, budget)` → output `(report JSON schema evalreport/v1: per-case {id, input_digest, outcome, per-metric scores, receipts refs}, summary scores)`. DeepEval adapter first (its `evaluate()` API maps directly); Inspect AI second; `byo` = any image honoring the contract. **`evalrunner/v1` and `evalreport/v1` join the `assayd-contracts` ledger** (design 07 §4 — r1 f3; the ledger carries every socket contract, and doctor/upgrade checks cover them). Conformance: a fixture dataset + mock agent where expected scores are known; parity across runners on the mechanical metrics (judged metrics are runner-specific by nature — documented, not hidden).

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

Fixture agent (scripted A2A responder) + fixture suite: pass/fail/floor-breach/empty-dataset/judge-down/budget-exhausted paths; report reproducibility (same digests ⇒ same verdict); e2e on k3d: the flagship demo — `assayd deploy` streams `HELD → eval 0.89 ✓ → canary 10% → 100%`, then a deliberately-broken revision fails with the report in the PR annotation.

## 12. Decisions for async review

- **D1 — The eval gate runs once, pre-canary; canary progression is SLO-judged** (no re-eval mid-rollout) — scope pinned.
- **D2 — Datasets and reports are content-addressed artifacts**; a verdict always names (dataset, judge, revision) digests — rerun-shopping structurally impossible.
- **D3 — Fail-closed everywhere**: empty dataset, judge down, budget out ⇒ the candidate does not pass.
- **D4 — EvalRunner is a provider slot** (`evalrunner/v1`), DeepEval first, conformance with cross-runner parity on mechanical metrics.

## 13. Resulting ADRs

ADR-0024 (P3) after critique PASS.

## 14. Amendments

- **A1 (2026-08-20, from design 25 r1 f1)**: the **model metric family** joins the catalog (metrics are target-kind-scoped): `golden_quality` (mechanical on `via`-shaped sets, judged otherwise), `latency_p95` / `throughput` under a declared load profile, `refusal_safety_rate`, `cost_regression_per_1k_tokens` (never per-task). Model datasets = curated sets + optionally prompts *extracted* from agent sessions; raw A2A session cases are agent-only. The flow, fail-closed rules, and contracts are shared unchanged.
- **A2 (2026-09-01, from design 02 A50)**: every revision reference in this design is `{name, digest}`, and the **digest** is what decides. Design 02 A37 established that the ten-character name is a name — a chosen collision against its forty bits took 1.2 seconds — and A50 carried the digest into `evalStatus` and `cards[]` but could reach no further, because this design owns the component that actually grants production traffic. A gate controller comparing `evalStatus.revision == H` can promote a colliding projection on the verdict another one earned, and it makes that decision **before any workload exists to inspect**, so design 02's workload-level guard is not in the path. The reference is carried through EvalRun input, the report's bound digests, `GatesPassed`, the candidate principal, events and receipts; promotion re-checks the digest so a verdict cannot be spent on a revision that changed underneath it. The `EvalSuite` and `EvalRun` CRDs do not exist yet, so this is the contract their types must satisfy rather than a description of a schema — **owed at implementation**, and §8 owes the case that proves a verdict for one digest cannot authorize a colliding one.
