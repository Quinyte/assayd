# Review: Design 25 — model-operator + ModelHub

- **Verdict**: **REVISE**
- **Reviewed**: `docs/designs/25-model-operator.md` (draft, 2026-08-20)
- **Independence note**: reviewed in an independent session that did not author the draft.
- **Method**: all 8 lenses, with the requested focus: the gate-reuse claim vs design 16's agent-shaped assumptions; the KServe/Trainer/KitOps bindings vs the landed research; the gateway-LLM-Backend seam vs design 03. Two load-bearing integration claims verified by fresh search (sources inline).

## Findings

### 1. MAJOR — "the SAME machinery" overclaims: design 16's metric catalog and dataset sources are agent-shaped, and most of them are undefined for a model

`25-model-operator.md:31,45` vs design 16 §5/§6. The *flow* reuses cleanly — and that reuse is this design's reason to exist. The *metrics* do not. Every metric in 16's catalog assumes an agent:

- `task_completion` — [DeepEval's trajectory metric over an agent's complete ordered trace](https://deepeval.com/docs/metrics-task-completion). A raw InferenceService has no task and no trajectory.
- `tool_correctness` — "expected vs actual tool-call sequences from the eval run's **receipts**". A model calls no tools; there are no tool hops to compare.
- `faithfulness_to_kg` — citations resolved via `kg.cite` on the pinned graph version. A model has no KG binding (that's the agent's).
- `cost_regression` — "trailing 7-day median **per-task** cost" (16 r2). Models are priced per token, and §5 of this very design says an in-cluster model's `usd_est` may be `null` — so the metric's baseline is undefined by this design's own admission.

The dataset sources are equally agent-shaped: `fromSessions` samples recorded **A2A sessions** keyed by `task_id` and hop outcomes (17 §5), which a raw model never produces; ontology-derived seeds are "the graph's known answers, **asked through the agent**" (16 §5). What genuinely reuses is the flow (Held → gate → verdict → weight shift), the fail-closed rules, and the contracts (`evalrunner/v1`, `evalreport/v1`, content-addressed datasets and reports) — a strong claim on its own.

**Fix**: define a model-metric family and record it as a design-16 catalog extension (16 has no amendment section yet — this creates its §12/A1): quality on a curated golden set (mechanical matchers where the set is `via`-shaped, judged otherwise), latency/throughput under a stated load profile, refusal/safety rate, and **per-1k-token** cost regression against the previous kit. State that model datasets come from curated sets + (optionally) *replayed prompts extracted from agent sessions*, never raw session cases. Restate D1 as "one promotion **mechanic**, two metric families" — which keeps the design's point and makes it true.

### 2. MAJOR — the shadow InferenceService has no isolation story: the 02-f2 / 16-r2 lesson, unapplied

`25-model-operator.md:45`. The candidate is "a **shadow InferenceService** (scaled minimally), the EvalSuite drives it through the gateway LLM route under the eval principal". Two readings, both defective: if the shadow is registered as a gateway LLM Backend so the eval principal can reach it, then any agent whose `llm.providers` names that model can reach an **ungated model version** — precisely the hole design 02 review f2 closed for agent candidates; if it is *not* registered, the design never says how the eval principal reaches it at all. The platform already solved this exact problem twice (02 §3.3's SVID-bound candidate route; 16 r2's per-run eval principal added to the admitted set at Job launch and removed at Job end).

**Fix**: mirror 16 r2 — the shadow gets a **candidate-only LLM route** whose admitted set contains exactly the run's eval principal, compiled at Job launch and revoked at Job end; general Backend registration happens **only on pass**. Add it to 03's concern table beside the existing "Eval temporary grant" row (see finding 5).

### 3. MAJOR — the pricing seam contradicts two approved contracts, breaking self-hosted models' primary path

`25-model-operator.md:50` vs ADR-0020 (design 03 §3.4) and ADR-0021 (design 04 §3.2). Two conflicts in one sentence:

- **Compile rule**: ADR-0020 fixed "`usdPerDay` compiled at the max price across the agent's allowed models; **model matching no pattern ⇒ compile error**." So an Agent with a usd budget that names an unpriced in-cluster model **fails to compile** — its policies never apply, its routes never attach (03's fail-closed ordering). §5's promise that in-cluster models work "exactly like external providers — same budgets" is therefore false precisely for the unpriced case this design invites, and the failure lands at deploy time on the P5 feature's flagship use.
- **Receipt schema**: `pricing: internal` is a third value for a field ADR-0021 fixed as `resolved | unresolved`, i.e. an unrecorded `receipt/v1` change — and it is unnecessary, since `usd_est: null` + `unresolved` already carries "unknown" and aggregates already treat null as unknown (04 r2).

**Fix**: the model-operator **writes the pricing row** — at serve time it upserts `internal/<model>` into `plume-model-pricing` (a compute-derived rate, or an explicit `0` with a marker if the org doesn't chargeback), so compilation always succeeds and cost attribution stays honest; keep the receipt enum at two values. If a distinct `internal` marker is genuinely wanted, record it as a design 04 §11 amendment rather than introducing it in passing.

### 4. MINOR — the only P5 design with no dated research note, and its weight-shift mechanism is newer than the platform's pinned baseline

`25-model-operator.md:5,46`. Every other design in the series landed a dated `docs/research/` note; this one cites `landscape-2026-08.md` §Modelhub — the bootstrap-era section that carries the tree's own "re-verify before relying on this after ~2026-11" caveat — for the **furthest-out phase**. Verified this session, the bindings hold and are in fact firmer than the design states:

- [KitOps↔KServe is a real, documented integration](https://kitops.org/docs/integrations/kserve/): a `ClusterStorageContainer` registers a storage-initializer image that unpacks `kit://` `storageUri`s. Worth naming — it is a **cluster-scoped, chart-shipped object** (design 07's business), not something the operator creates per Model.
- The gateway-side weighted shift (D1) rides agentgateway's **virtual models** — [weighted/conditional/failover routing across real models, shipped in v1.3.0](https://agentgateway.dev/blog/2026-06-17-agentgateway-v1.3.0/). That is a *newer* capability than the "agentgateway **2.2** CRDs only" baseline ADR-0019(5) pins — the same stale-wording problem already tracked as **06-review R2-a**, now with a second dependent.

**Fix**: land `docs/research/modelhub-2026-08.md` (KServe RawDeployment + ClusterStorageContainer/`kit://`, Trainer v2 BuiltinTrainer status, Kueue, virtual-models version requirement) and fold the minimum-version wording into the R2-a fix so both dependents cite one pin.

### 5. MINOR — none of this design's compiled gateway concerns have design-03 rows

`25-model-operator.md:46,50` vs design 03 §3.4. LLM Backend registration for `expose.llmBackend`, the candidate-only shadow route (finding 2), and virtual-model weight shifting are all compiled gateway config, and the series' standing rule — enforced against designs 05, 10, 11, 16, 17, 23 — is that compiled concerns are **rows, not prose**. **Fix**: one "Model serving" row (Backend registration + virtual-model weights) plus the shadow-grant row.

### 6. MINOR — RawDeployment's doctrine rationale is unstated, and it is the load-bearing reason KServe passes the gate

`25-model-operator.md:34`. `inferenceService: {…}` is described as "KServe RawDeployment values passthrough" with no explanation. RawDeployment is what lets KServe run **without Knative/Istio** — which is the only reason binding KServe doesn't blow the ≤8-pod budget (doctrine rule 5) and doesn't contradict ADR-0012's meshless core. Unstated, a reader auditing the doctrine gate will reasonably assume KServe drags Knative in, and a future contributor may "helpfully" enable Serverless mode and quietly import a mesh. **Fix**: one clause in §2 or §4 making RawDeployment a stated constraint, not a default.

## Lens summary

1. **Doctrine**: clean on its face — one plus-tier operator pod, ledger-entered like 21's; serving/training pods are workloads. Finding 6 is the unstated premise the whole gate rests on.
2. **Charter**: ModelKits are the genuinely OCI-native Artifact case (the design says so, correctly); no new primitives; "ride, don't wrap" honored in D4's honest pin-when-upstream-lands.
3. **Gate reuse** (requested scrutiny): findings 1 and 2 — the flow and contracts reuse well, the metric catalog and candidate isolation do not. D1's instinct ("one promotion mechanic platform-wide") is right and worth keeping; it just needs the metric-family split to be true.
4. **Bindings vs landed research** (requested scrutiny): finding 4 — the claims verify, the research hygiene doesn't, and one capability outruns the pinned baseline.
5. **Gateway seam vs 03** (requested scrutiny): findings 3 and 5 — the pricing conflict is the real defect; D3 ("agents never dial models directly") is otherwise exactly the discipline that makes budgets/receipts/allowlists total, and §5's observation that self-hosted models are the BAA allowlist's easiest member is a genuine selling point.
6. **Failure modes**: good table; the `llm.fallback` reuse for in-cluster models (design 20's path covering both) is a nice composition, and "registry-only kits referenced by no one — GC is registry policy, not operator behavior" is the right restraint.
7. **Security**: kits signed and digest-pinned always, default-deny InferenceServices reachable only via the gateway — sound; finding 2 is the one gap.
8. **Testability**: the matrix is right in shape (gate-reuse e2e asserted for models, unsigned-kit refusal, fallback drill, Kueue preemption resume); after findings 1–3, add: a model-metric fixture suite, a shadow-reachability negative test (a non-eval principal must not reach the shadow), and a usd-budget compile test against an in-cluster model.

## Disposition

**REVISE.** The envelope-over-adopted-components shape is right, the bindings are real (better documented than the design claims), and D1/D2/D3 are the correct disciplines. But three MAJORs sit on the seams this design exists to create: the gate it reuses is agent-shaped in its metrics and datasets, the shadow candidate repeats an isolation hole the platform closed twice already, and the pricing seam would make self-hosted models undeployable under a usd budget. All three have contained fixes that mostly land as recorded extensions to designs 16, 03, and 04.

VERDICT: REVISE — 6 findings
