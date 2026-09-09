# Design 25: model-operator + ModelHub

- **Status**: **approved** — critique PASS (reviews/25-review.md) · ADR-0026
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
    runtime: llm | classic | none          # A1: llm ⇒ LLMInferenceService (llm-d-backed);
                                           # classic ⇒ InferenceService (vLLM/Triton);
                                           # none ⇒ registry-only (a kit others reference)
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

`expose.llmBackend: true` ⇒ the operator registers the InferenceService as a gateway **LLM Backend** target; Agent CRs reference it in `llm.providers` exactly like external providers — same budgets, receipts, egress allowlists. **Pricing (r1 f3 — ADR-0028/0021 held)**: at serve time the model-operator **upserts an `internal/<model>` row into `assayd-model-pricing-internal`** — the operator-owned map, **not** the chart-owned `assayd-model-pricing` this line named until design 03 A51 caught it. Design 03 A21 split the two precisely so that a chart upgrade cannot revert an operator-written row; writing in-cluster rows into the chart-owned map would have lost every internal price on the next `helm upgrade`, and D2b's promise that compilation never fails on an unpriced internal model with it (a compute-derived rate, or explicit `0` with a marker where the org doesn't charge back) — so ADR-0028's "unmatched model ⇒ compile error" never fires on an in-cluster model and Agent policies always compile. The receipt enum stays `resolved | unresolved` (no third value; ADR-0021 unchanged). Compliance profiles' BAA egress allowlists (ADR-0014) naturally admit in-cluster models — **self-hosted models are the allowlist's easiest member**, stated because it's a selling point.

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

## 11. Amendments

- **A1 (2026-08-22, landscape correction)**: the v1 framing "llm-d only for one giant model at high throughput" is **stale and was a hidden dependency**. KServe v0.17 split its API and its generative path — **`LLMInferenceService` — is built on llm-d**, so binding KServe for LLM serving binds llm-d transitively regardless. Corrected design:
  - **`serve.runtime` splits by model kind**: generative ⇒ **`LLMInferenceService`** (llm-d-backed: KV-cache-aware routing, disaggregated prefill/decode, tiered KV offload, scale-to-zero); classic/predictive ⇒ **`InferenceService`** (the Triton/vLLM path already designed). The chart installs KServe's `llmisvc` component only at `plus` tier with generative serving enabled.
  - **Gateway binding is GAIE, not a plain Backend**: the compiler emits an **`InferencePool`** (Gateway API Inference Extension) for generative pools and routes to it from the HTTPRoute — **verified OSS in agentgateway** (ADR-0028 D1 holds). assayd keeps exactly what it always keeps at the gateway (budgets, receipts, authz, egress allowlists); llm-d does scheduling *below* that seam. Recorded as a design 03 §3.4 row.
  - **Weight**: +1 endpoint-picker (EPP) per inference pool, plus the vLLM serving pods — **workload** state per served model, the design-13 FalkorDB category, entered in the ledger. Nothing added to core.
  - **Bind contracts, not internals**: llm-d is CNCF *Sandbox* and moved v0.5→v0.6 in two months; P5 is the last phase, so assayd pins `LLMInferenceService` + `InferencePool` and treats llm-d as the implementation behind them — the provider-slot rule applied to serving.
  - Research: `docs/research/llm-d-2026-08.md`.


- **A2 (2026-08-28, from `reviews/03-codex-review-r4.md` MAJOR 15)**: weighted model shifting uses **Gateway API `backendRefs` weights across two `AgentgatewayBackend`s**, not `AgentgatewayModel`. The mandate lived in design 03's mapping table and ADR-0026 rather than in this design's body; both are corrected. ADR-0028 records that CRD as a **fourth kind, experimental and disabled by default** (`agentgatewayModels.enabled=true` is required to install it at all), so a default v1.4.1 cluster does not have it — and this design made it the mandatory mechanism for a *core rollout* path. Weighted `backendRefs` are stable Gateway API, present in every install, and already how design 02 §3.3 shifts revision weights, so the model path reuses a mechanism the platform depends on elsewhere rather than introducing an opt-in one. Virtual models remain an **opt-in enhancement** where an operator has enabled the CRD, with the conditional and failover strategies they add; nothing core requires them.
- **A3 (2026-09-04, from design 03 A51)**: §3 said the model-operator upserts an `internal/<model>` row into **`assayd-model-pricing`**. That is the chart-owned map, and design 03 A21 split the pricing table into two ConfigMaps precisely to stop that write: `assayd-model-pricing` is chart-owned and versioned, so an operator row written there is reverted by the next `helm upgrade` — taking with it D2b's promise that agent policy compilation never fails on an unpriced internal model, silently and one release later. The in-cluster rows belong in `assayd-model-pricing-internal`, which is operator-owned. Ownership was never the disagreement — D2b already assigns the operator the in-cluster rows — only the object, and this design named the wrong one because it predates the split and was never revisited. Design 03 §3.5 carries the corresponding note.
