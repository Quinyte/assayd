# Design 25: model-operator + ModelHub

- **Status**: draft — awaiting critique
- **Phase**: P5 · **Size**: L · **Date**: 2026-08-20
- **ADRs**: ADR-0003 (KServe/Trainer/KitOps bindings), 0006 (same gate mechanic), 0024 (EvalSuite reuse) · interfaces: 16 (Model gating), 03 (LLM Backend targets), 07 (plus-tier charts), research: `landscape-2026-08.md` §Modelhub (KServe CNCF, Trainer v2 TrainJob w/ Unsloth landing, KitOps/ModelPack OCI)

## 1. Purpose & scope

The third operator (architecture §02/§10): the `Model` CR as a thin envelope over four adopted components — **train → package → eval-gate → serve** — with one promotion mechanic shared with agents (FR: models earn traffic too). In scope: the CRD, stage reconciliation, the serve-side handoff to agent LLM routing, gating reuse. Out of scope: training-framework internals (Trainer owns), eval mechanics (16 owns — reused verbatim), GPU scheduling policy (Kueue's job, bound not built).

## 2. Doctrine & charter gates

- **Plane**: slow (the operator — the architecture's named third; a plus-tier pod, ledger-entered like 21's). Everything staged through it is adopted components + data.
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
- **gate**: the design-16 gate flow with `target: {modelRef}` — the candidate is a **shadow InferenceService** (scaled minimally), the EvalSuite drives it through the gateway LLM route under the eval principal, verdict semantics identical (fail-closed, content-addressed reports). *One promotion mechanic for the whole platform* — stated as the design's reason to exist.
- **serve**: pass ⇒ KServe `InferenceService` (RawDeployment mode; runtime container per `serve.runtime`; `kit://` URI source per KitOps/KServe integration) rolls with **revision weighting at the gateway LLM Backend** (not KServe's own canary — one traffic-shifting mechanism platform-wide, the 02 discipline).

## 5. The serve↔agent seam

`expose.llmBackend: true` ⇒ the operator registers the InferenceService as a gateway **LLM Backend** target; Agent CRs reference it in `llm.providers` exactly like external providers — same budgets, receipts, egress allowlists, pricing-table entry (`internal/<model>` pattern rates for cost attribution; `usd_est` may be `null` + `pricing: internal` where the operator chooses not to price). Compliance profiles' BAA egress allowlists (ADR-0014) naturally admit in-cluster models — **self-hosted models are the allowlist's easiest member**, stated because it's a selling point.

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

- **D1 — One promotion mechanic**: models gate through design 16 unchanged; traffic shifts at the gateway Backend, never via a second canary system.
- **D2 — Kits are the only serving source** (signed, digest-pinned); no ad-hoc model mounts.
- **D3 — Agents never dial models directly** — in-cluster models are gateway LLM Backends like any provider.
- **D4 — Unsloth/TRL via Trainer BuiltinTrainers, pinned when upstream lands** (torchtune/TRL until then) — ride, don't wrap (the architecture's rule).

## 10. Resulting ADRs

ADR-0026 (P5+ent) after critique PASS.
