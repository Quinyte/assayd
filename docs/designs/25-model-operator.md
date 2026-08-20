# Design 25: model-operator + ModelHub

- **Status**: revised r2 — awaiting re-critique (r1 REVISE, 6 findings addressed; reviews/25-review.md)
- **Phase**: P5 · **Size**: L · **Date**: 2026-08-20
- **ADRs**: ADR-0003 (KServe/Trainer/KitOps bindings), 0006 (same gate mechanic), 0024 (EvalSuite reuse) · interfaces: 16 (Model gating), 03 (LLM Backend targets), 07 (plus-tier charts)
- **Research**: `docs/research/modelhub-2026-08.md` (dated note: KServe RawDeployment + chart-shipped `ClusterStorageContainer` for `kit://`, Trainer v2 BuiltinTrainers, Kueue, agentgateway **virtual models** minimum version)

## 1. Purpose & scope

The third operator (architecture §02/§10): the `Model` CR as a thin envelope over four adopted components — **train → package → eval-gate → serve** — with one promotion mechanic shared with agents (FR: models earn traffic too). In scope: the CRD, stage reconciliation, the serve-side handoff to agent LLM routing, gating reuse. Out of scope: training-framework internals (Trainer owns), eval mechanics (16 owns — reused verbatim), GPU scheduling policy (Kueue's job, bound not built).

## 2. Doctrine & charter gates

- **Plane**: slow (the operator — the architecture's named third; a plus-tier pod, ledger-entered like 21's). Everything staged through it is adopted components + data.
- **KServe runs in RawDeployment mode — a stated constraint, not a default** (r1 f6): it is what lets KServe run **without Knative/Istio**, which is the only reason binding it keeps the ≤8-pod budget (rule 5) and ADR-0012's meshless core intact. Serverless mode is forbidden; the chart pins it and CI asserts no Knative CRDs are required.
- **Pods**: model-operator 1 (plus tier, ledger PR). Serving/training pods are KServe/Trainer workloads. **Stateful deps**: none new (MLflow optional-plus for tracking, its own chart). ✓
- **Primitives**: Resource, Artifact (**ModelKits — the genuinely OCI-native case**), Event (stage transitions). ✓

## 3. The Model CRD

```yaml
kind: Model
metadata: {name: pa-classifier, namespace: claims}
spec:
  source:                              # exactly one
    kit: oci://registry/models/pa-classifier:1.3.0     # pre-built ModelKit (the common case)
    # train:                           # or a training stage produces the kit
    #   runtime: {framework: torchtune|unsloth|trl}    # Trainer v2 BuiltinTrainer names
    #   base: oci://…base-kit
    #   dataset: {kitRef | objectRef}
    #   trainJob: {resources, queue: gpu-queue}        # Kueue queue binding
  gates:
    - evalSuiteRef: pa-classifier-suite   # design 16, target: {modelRef} — the SAME machinery
  serve:
    runtime: vllm | triton | none          # none = registry-only (a kit others reference)
    inferenceService: {minReplicas: 0, gpu: {…}}       # KServe RawDeployment values passthrough
  expose: {llmBackend: true}               # registers as a gateway LLM Backend target (§5)
status:
  phase: Training|Packaging|Held|Canary|Serving|RegistryOnly
  activeKit: sha256:…                      # digest-pinned, always
  conditions: [KitReady, GatesPassed, ServingReady]
```

## 4. Stage reconciliation

- **train** (optional): operator materializes a Trainer v2 `TrainJob` (framework per spec; Unsloth/TRL as BuiltinTrainers — landing upstream, pinned when released; until then torchtune/TRL) under a `train:<model>@<run>` principal with its own budget (the 14/15/20 pattern; GPU cost is receipted where the LLM egress is ours, and TrainJob resource usage lands in run status regardless). Output → **package**: a KitOps ModelKit assembled (weights + config + tokenizer + provenance: TrainJob ref, dataset digests), cosign-signed, pushed. Every stage transition is a CloudEvent.
- **gate**: the design-16 **flow**, not its metric catalog (r1 f1 — the honest split): reused verbatim are the state machine (Held → gate → verdict → weight shift), the fail-closed rules, the contracts (`evalrunner/v1`, `evalreport/v1`), and content-addressed datasets/reports. **Model metrics are a separate family** — recorded as design 16's §12 A1: `golden_quality` (mechanical where the set is `via`-shaped, judged otherwise), `latency_p95`/`throughput` under a stated load profile, `refusal_safety_rate`, and `cost_regression_per_1k_tokens` vs the previous kit (never per-task — models have no tasks). Datasets are curated sets plus, optionally, **prompts extracted from agent sessions** — never raw A2A session cases (17's sessions are agent-shaped).
- **candidate isolation (r1 f2 — the 02-f2 / 16-r2 lesson)**: the shadow InferenceService gets a **candidate-only LLM route** whose admitted set is exactly the run's eval principal, compiled at Job launch and revoked at Job end; general LLM Backend registration happens **only on pass**. No agent can reach an ungated model version.
- **serve**: pass ⇒ KServe `InferenceService` (RawDeployment; runtime container per `serve.runtime`; `kit://` `storageUri` resolved by the **chart-shipped `ClusterStorageContainer`** — a cluster-scoped design-07 object, not per-Model operator output) rolls with **revision weighting at the gateway LLM Backend via agentgateway virtual models** (not KServe's own canary — one traffic-shifting mechanism platform-wide). Virtual models postdate the ADR-0019(5) "2.2 CRDs only" wording — folded into the **06-review R2-a** minimum-version fix so both dependents cite one pin (r1 f4).

## 5. The serve↔agent seam

`expose.llmBackend: true` ⇒ the operator registers the InferenceService as a gateway **LLM Backend** target; Agent CRs reference it in `llm.providers` exactly like external providers — same budgets, receipts, egress allowlists. **Pricing (r1 f3 — ADR-0020/0021 held)**: at serve time the model-operator **upserts an `internal/<model>` row into `plume-model-pricing`** (a compute-derived rate, or explicit `0` with a marker where the org doesn't charge back) — so ADR-0020's "unmatched model ⇒ compile error" never fires on an in-cluster model and Agent policies always compile. The receipt enum stays `resolved | unresolved` (no third value; ADR-0021 unchanged). Compliance profiles' BAA egress allowlists (ADR-0014) naturally admit in-cluster models — **self-hosted models are the allowlist's easiest member**, stated because it's a selling point.

## 6. Failure modes

| Failure | Behavior |
|---|---|
| TrainJob fails/preempted (Kueue) | Status + retry per spec; no kit produced ⇒ nothing downstream moves |
| Kit unsigned / provenance incomplete | `KitReady=False`; admission refuses serving an unsigned kit (the agent-image bar, applied) |
| Gate fails | Shadow service torn down; `GatesPassed=False` + report (16 semantics verbatim) |
| Serving OOM/crash | KServe/k8s machinery; gateway Backend health ejects it; agents fall back per `llm.fallback` (design 20's path — the drift machinery covers in-cluster models identically) |
| GPU starvation | Kueue queueing visible in status; `ServingReady=False` with the queue position |
| Registry-only kits referenced by no one | Fine — kits are artifacts; GC is registry policy, not operator behavior |

## 7. Security

Kits signed + digest-pinned always (`activeKit`); serving images from the pinned runtime set; InferenceServices are workload pods under default-deny (reachable via the gateway Backend only — agents never dial models directly, which is what makes budgets/receipts/allowlists total). Training reads datasets via object refs under the train principal.

## 8. Testing

CRD matrix (kit-only, train+serve, registry-only); gate reuse e2e: model candidate held → suite → pass → gateway weight shift (the agent demo's mechanics, asserted for models); unsigned-kit refusal; fallback drill (kill serving pod → agents on `llm.fallback` → drift canaries verify); Kueue preemption resume.

## 9. Decisions for async review

- **D1 — One promotion mechanic, two metric families**: models reuse design 16's flow/contracts/fail-closed rules; model metrics are their own catalog (16 A1). Traffic shifts at the gateway, never via a second canary system.
- **D2 — Kits are the only serving source** (signed, digest-pinned); no ad-hoc model mounts.
- **D2b — The operator owns in-cluster model pricing rows** so agent policy compilation cannot fail on an unpriced internal model (r1 f3).
- **D3 — Agents never dial models directly** — in-cluster models are gateway LLM Backends like any provider.
- **D4 — Unsloth/TRL via Trainer BuiltinTrainers, pinned when upstream lands** (torchtune/TRL until then) — ride, don't wrap (the architecture's rule).

## 10. Resulting ADRs

ADR-0026 (P5+ent) after critique PASS.
