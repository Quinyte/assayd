# Design 20: Drift controllers (detect → condition → remediate → verify)

- **Status**: **approved** — critique PASS at r2 (reviews/20-review.md) · ADR-0025
- **Phase**: P4 · **Size**: L · **Date**: 2026-08-20
- **ADRs**: 0007 (drift-as-reconciliation) · interfaces: 15 (KG detection; this design routes remediation), 10 (signals), 16 (nightly trends, last-eval-passing revisions), 04 (receipts/model ids), 02 (rollout machinery actions), 14 (rebuild trigger)

## 1. Purpose & scope

The self-healing layer (FR-22): detectors write conditions, controllers remediate, the same detectors verify recovery — `kubectl get agents` tells the semantic truth. In scope: the four detector families, remediation actions **with their guardrails**, correlation requirements, the verification loop. Out of scope: detection primitives owned elsewhere (15's probes, 10's signals), eval machinery (16).

## 2. Doctrine & charter gates

- **Plane**: slow (controllers in the agent-operator; detector *content* — canary prompt sets, thresholds — is data). **Pods**: 0 standing (CronJobs for canary prompts; controllers are watch loops). **Stateful deps**: baselines in Postgres (existing). ✓
- **Primitives**: Resource (conditions), Event (`drift.detected/remediated`), Tool (canary prompts via gateway). ✓ DriftDetector remains a *reserved* slot — v1 ships built-in detectors; the slot opens when a third-party detector shows up (recorded, not invented early).

## 3. The four families

| Family | Detector | Condition | Remediation (guarded, §4) |
|---|---|---|---|
| **KG drift** | design 15 continuous probes (owned there) + ingestion-lag metric vs the Connector schedule | `KnowledgeStale` on the KG CR | trigger a rebuild (14); if rebuild's Gate A also fails → escalate (the *domain* moved: ontology/labels need humans) |
| **Model drift** | hourly CronJob: fixed canary prompt set per **endpoint identity** actually in use — the `{arm, instance fields, model}` tuple of design 02 A24, read from receipts, **not** `(provider, model)` (A2); embedding-distance of outputs vs a pinned baseline snapshot (pinned embedder) | `ModelDrifted` on affected Agents | controller sets `status.llmFallbackActive` → the operator **shifts weight** to the already-converged `<agent>-llm-fallback` Backend (design 03 §3.3.3 A34). **No Backend is recompiled during an incident (A3)**: a live recompile can NACK, and the measured consequence is that the dataplane keeps the drifted primary serving while Kubernetes status reports converged — remediation claimed, not performed. Activation requires the fallback to be `Accepted=True` with `ResolvedRefs=True` at the current generation — and, because that only describes the state *before* the weight edit, a **postcondition** too (A5): convergence at the route's NEW generation, then the model canary run through the production route and attributed by full endpoint identity in the receipt. `llmFallbackActive` reports remediation only after that positive evidence. On timeout `ModelDrifted` stays, `LLMFallbackUnavailable` is raised, and it pages — recording a remediation that did not take is the failure this whole path was rebuilt to avoid. The witness is bounded (A6): one receipt names one gateway replica via `hop.gatewayInstance` (design 04 A4), so activation requires a distinct instance per replica or reports `LLMFallbackUnavailable/PartiallyProgrammed`; the canary principal is admitted at revision publish rather than added to a serving route mid-incident; and the **reverse** shift carries the same postcondition, since a shift back that did not land clears `llmFallbackActive` while traffic still goes to the weaker model, silently. `llm.fallback` recorded as design 02 §11 **A8** (r1 f2); no fallback declared ⇒ alert only |
| **Behavioral drift** | SLO burn on `task_success_rate` + `termination_reason != goal_met` rising (10's signals) | `Degraded` on the Agent | **auto-rollback — only when a deploy correlates** (§4): target = last eval-passing revision; **core tier** (GatesSkipped) targets the previous retained revision with the escalation stating the weaker guarantee — "last-serving, not last-proven" (r1 f5) |
| **Embedding drift** | nightly centroid distance over kg_query result embeddings vs the version's baseline (cheap, core); Phoenix visuals (plus) | `EmbeddingDrift` on the KG | re-index recommendation event; auto re-embed = version bump build only if `autoReembed: true` |

## 4. Remediation guardrails (what keeps self-healing from being self-harm)

Every automated action requires **all four**:

1. **Correlation**: auto-rollback fires only if the `Degraded` window started within `correlationWindow` (default 24h) of the active revision's promotion — otherwise the code didn't change, the world did, and rollback would be superstition. Model-fallback fires only when `ModelDrifted` names the model the agent actually uses (receipts).
2. **Rate limit**: max 1 automated action per target per 24h (`remediationBudget`); exceeded ⇒ escalate to page. Flapping is a page, never a loop.
3. **Receipt + event**: every action is receipted under `principal: drift:<controller>` and emits `drift.remediated` — self-healing is audited like any actor.
4. **Override**: `plume.dev/remediation: manual` annotation on any CR pauses automation for it (condition still set — detection never pauses); the annotation itself is a recorded decision.

**Verification (ADR-0007's loop)**: the *same detector* that set a condition clears it — rollback → SLO window re-evaluates; fallback → canary prompts re-run against the fallback; rebuild → probes. A condition that remediation can't clear within `verifyTimeout` escalates with the full action trail.

## 5. Baselines

**Canary budget home (r1 f4)**: platform-level — `drift.canaryBudget` in chart values; the operator synthesizes the PolicyIntent for `principal: drift:model-canary` (the 14/15 pattern at platform scope). Model-canary baselines snapshot at first use of a **full endpoint identity** — the `{arm, instance fields, model}` tuple of design 02 A24, the same key receipts and the detector row use (A4). *Not* `(provider, model)`: that key is non-injective, so two Azure OpenAI deployments sharing an arm and a model name — and differing in endpoint, deployment name, region and BAA posture — would share one baseline. A canary against the healthy one would then create or clear the baseline while the other was drifting, and fallback activation would target an endpoint that was never drifting while leaving the one that was. A2 removed that key from the detector row and left it here, which is the propagation failure this review set keeps finding. Baselines re-snapshot **only** on explicit `plume drift rebaseline` (a drifted baseline silently re-baselined would define drift away — the one-way ratchet is deliberate). Embedding baselines are per graph-version (immutable by construction). Behavioral baselines are the SLO targets (chart-shipped, CR-tunable).

## 6. Failure modes

| Failure | Behavior |
|---|---|
| Canary CronJob fails (infra) | `DriftDetectionDegraded` (the 15 pattern: infra ≠ drift); no condition changes on stale data |
| Fallback provider also drifted | Both named in `ModelDrifted`; no route change (no good target); page |
| Rollback target GC'd | Made rare **mechanically** (r1 f1): promotion warns when retained revisions can't cover the correlation window's deploy cadence, and the operator **pins retention of the last eval-passing revision while any `Degraded` window is open** (hold released when the condition clears). If still GC'd: refused + escalation names the gap |
| Remediation churn (rollback → re-promote → rollback) | Rate limit (§4.2) breaks the loop at one cycle; page with the sequence |
| Detector thresholds mistuned | Everything is data (chart values); `drift.detected` without action (alert-only mode) is the shipped default for the first 14 days of any install — **learn before you act**, stated |

## 7. Observability

`drift_conditions` gauge by family; remediation action/outcome counters; time-in-degraded histogram. Shipped alerts already mapped (10 §5); this design adds: remediation-budget-exceeded (page), verify-timeout (page), alert-only-mode expiring (ticket).

## 8. Testing

Per-family fixture drills: inject drift (swap canary baseline / poison success-rate / stale the KG) → condition → guarded action → verification clears; correlation negative test (Degraded with no recent deploy ⇒ no rollback, alert); rate-limit and flap tests; override annotation honored; alert-only default window honored.

## 9. Decisions for async review

- **D1 — Auto-rollback requires deploy correlation**; uncorrelated degradation never rolls back.
- **D2 — One automated action per target per day**; flapping pages.
- **D3 — Baselines ratchet one way** (explicit rebaseline only).
- **D4 — New installs run alert-only for 14 days** — self-healing earns trust with evidence first.
- **D5 — DriftDetector slot stays reserved**; built-ins only in v1.

## 10. Resulting ADRs

ADR-0025 (P4) after critique PASS.

## Amendments

A1 (2026-08-22) — **fallback activation is an ungated change to the answering model, and that is deliberate.** Remediation sets `status.llmFallbackActive`, so design 02's revision hash (A12, computed over a *spec* projection) does not see it: no revision is minted and no eval fires. That avoids an eval cycle mid-incident, which is correct. But it means the model actually answering requests changes with **no gate**, which design 02 A12 otherwise presents as the thing it prevents. The compensating controls are this design's existing four — correlation, rate limit, receipt, operator override — and they are load-bearing precisely because the gate is absent here. Recorded so that neither design can be read as promising a guarantee the pair does not provide.

## 11. Amendments

- **A2 (2026-08-28, from `reviews/03-codex-review-r4.md` MAJOR 9)**: drift detection, baselines, receipts, conditions and fallback activation key on the **full endpoint identity**, not `(provider, model)`. Design 02 A24 made an endpoint's identity `{arm, instance fields, model}` precisely because an arm plus a model name does not identify a destination: two Azure OpenAI deployments share `azureopenai` and a model name while differing in `endpoint` and `deploymentName`, and may differ in region and BAA status. Collapsing them here would have let **one baseline cover two endpoints** — so a canary against the healthy deployment could clear drift on the drifted one, and `llmFallbackActive` could remediate an endpoint that was never drifting while leaving the one that was. `ModelDrifted` names the identity it observed. Pinned by a test with two same-arm, same-model instances where only one has drifted.- **A3 (2026-08-29, from `reviews/03-codex-review-r6.md` MAJOR 4)**: fallback activation is a **weight shift over pre-provisioned Backends**, never a recompile. Two mechanisms were authoritative at the same time — design 03 §3.3.3 required pre-provisioning precisely so activation could not NACK, while this design's Model-drift row still told an implementer to recompile the serving LLM Backend. They are not variants: a recompile during an incident is a live apply, and design 03 A19's cluster measurement says a NACK'd apply leaves the **old** configuration serving while Kubernetes status reports converged. Following this row would therefore have kept the *drifted* primary answering while `llmFallbackActive` reported a remediation — the loud-and-wrong of rule 8, on the remediation path. `<agent>-llm-primary` and `<agent>-llm-fallback` are both emitted and converged at revision publish; activation sets `backendRefs[].weight` and nothing else, and is refused with `LLMFallbackUnavailable` if the fallback is not converged at the current generation.
- **A4 (2026-08-31, from `reviews/03-codex-review-r7.md` MAJOR 5)**: the canary **baseline** keys on the full endpoint identity too. A2 changed the detector row and the storage key here still read "snapshot at first use of a `(provider, model)`" — the exact non-injective key A2 forbids, left behind in the same document that forbids it. Two Azure OpenAI deployments sharing an arm and a model name would share one baseline, so a canary against the healthy deployment could create or clear the baseline while the other was drifting, and fallback would target an endpoint that was never drifting. Pinned by two same-arm, same-model instances whose baselines and drift states stay independent.
- **A5 (2026-08-31, from `reviews/03-codex-review-r7.md` BLOCKER 9)**: activation carries a **postcondition**, not only a precheck. The precondition describes the state before the weight edit; the edit itself is a live route mutation this stack cannot prove landed, since route status is control-plane convergence and v1.4.1 has no `Programmed` condition. `llmFallbackActive` now reports remediation only after convergence at the route's new generation **and** a model canary through the production route attributed by full endpoint identity in the receipt. On timeout `ModelDrifted` stays, `LLMFallbackUnavailable` is raised, and it pages — a recorded remediation that did not take is the outcome A3 and this amendment both exist to prevent.
- **A6 (2026-08-31, from an independent critique of design 02 A36–A43)**: three gaps in A5's verification, all in the direction of claiming more than was measured. **The witness was unbounded**: one receipt proves one gateway replica took the new weights, and design 03 §3.1 declares `gatewayReplicas` may exceed one, so the other N−1 could still be routing production traffic to the drifted primary while plume reported the dataplane converged — the receipt now carries `hop.gatewayInstance` (design 04 A4) and a partial result is named as such. **The canary was not admitted**: A5 required it to run through the *production* route, whose admitted set is the agent's SVID; adding the `drift:model-canary` principal at activation would widen a serving route's admitted set mid-incident, which is a behaviour-surface change. It is admitted at revision publish instead, like design 16's eval principal. **The reverse shift had no postcondition at all** — a shift back that did not land clears `llmFallbackActive` while traffic still goes to the fallback, typically a cheaper or weaker model, indefinitely and after the incident when nobody is watching. A5's own argument applied unchanged and had not been applied.
