# Design 14: Ingestion pipeline + extraction QA

- **Status**: draft — awaiting critique
- **Phase**: P2 · **Size**: L · **Date**: 2026-08-20
- **ADRs**: 0004 (DBOS), 0017 (admin surface), 0009 (ingestion plane) · interfaces: 11 (readers), 12 (gates derive from ontology), 13 (admin caller), 15 (probes gate promotion), 20 (staleness seam)
- **Research**: `docs/research/connectors-2026-08.md` §DBOS (Jobs-with-DBOS execution model; dynamic queues; migrate/runtime role split)

## 1. Purpose & scope

How a snapshot gets built: acquire → normalize → extract → resolve → gate → write → commit — with **measured extraction quality** and quarantine-not-degrade semantics (architecture §13, Fig 7). In scope: the pipeline's execution model, stage contracts, the two gates, the labeled-sample discipline, PII at normalize, run observability. Out of scope: reader implementations (11), invariant/probe *content* (12/15), provider write mechanics (13).

## 2. Doctrine & charter gates

- **Plane**: slow (the pipeline engine is platform code) — but every stage's *behavior* is data: readers (pack-registered), extraction prompts (from ontology descriptions), gates (from ontology), redaction rules (profile packs).
- **Pods**: **0 standing for batch** — a snapshot build is a k8s **Job running DBOS-instrumented Python** (crash ⇒ Job restarts ⇒ DBOS resumes from the Postgres checkpoint; the research-backed model). Continuous mode uses design 11's per-connector worker. **Stateful deps**: Postgres (DBOS state — existing), object store (intermediate artifacts). ✓
- **Primitives**: Event (run lifecycle CloudEvents), Artifact (staged batch artifacts for `load_artifact`), Resource. ✓

## 3. Execution model

`plume kg push` / schedule / `dir.changed`-style triggers ⇒ the operator creates a **build Job** for `<graph> vN+1`:

```
Job (DBOS workflow "build-<graph>-vN+1", per-run DB schema role: restricted)
  acquire   : reader invocations (11) → raw docs to object store (content-addressed)
  normalize : chunk · dedupe (content hash) · PII redaction (profile filters) · language/type tagging
  extract   : ontology-derived Pydantic types (12) via gateway LLM route (receipted — 13 D2);
              batched; per-doc idempotent (doc content hash = DBOS step key)
  resolve   : entity resolution — key-based merge (ontology keys) first, embedding-similarity
              assist second (threshold in ontology pattern defaults); ambiguous ⇒ review queue, not guess
  GATE A    : extraction QA on the labeled sample (§4) — P/R ≥ bars or QUARANTINE
  write     : admin surface (13): begin_version → write_batch/load_artifact (idempotent (batch_id, seq))
  GATE B    : commit_version — ontology invariants; violations ⇒ QUARANTINE with the violation list
  handoff   : probe engine (15) runs; operator promotes only on ProbesPassing (01 §4)
```

Each stage emits a `kg.build.<stage>` CloudEvent; the whole run is one DBOS workflow so any crash resumes mid-stage, never restarts the corpus. Reruns of the same inputs are no-ops end-to-end (content addressing all the way down).

## 4. Extraction QA — the labeled sample discipline (Gate A)

- Every graph maintains `labels/` — a small human-labeled extraction sample (docs → expected entities/relations). Bootstrap: `kg init` seeds ~20 docs with **proposed** labels the human confirms/edits (the ontology-proposal flow's byproduct); patterns ship starter labels for their fixture shapes.
- Gate A runs extraction on the sample every build and scores entity/relation **precision & recall against the ontology's bars** (`quality: {entity_pr: 0.9/0.85, relation_pr: 0.85/0.8}` — pattern defaults, per-graph overridable in the KG CR).
- Below bar ⇒ **quarantine before any write** — a bad prompt/model change never reaches even a staging namespace. The failure names the worst-scoring entity types (actionable, not just red).
- Sample drift: when the human edits labels or the ontology majors, the sample re-versions with the graph; `LabelsStale` condition if sample_version < ontology major.

## 5. Ambiguity & the review queue

`resolve` never guesses on ambiguous merges (similarity in the gray band): candidates land in a **review queue** (a JetStream KV bucket + `plume kg review` CLI verb) with the run paused *at that stage* (DBOS makes the pause durable). Human resolves ⇒ run resumes; queue older than `reviewTimeout` (default 72h) ⇒ build fails visibly (`ReviewTimedOut`), never auto-merges. Resolutions are recorded and replayed on future builds (same pair ⇒ same answer — the queue learns).

## 6. Failure modes

| Failure | Behavior |
|---|---|
| Reader/source down mid-acquire | DBOS retries with backoff; persistent ⇒ run fails `AcquireFailed`; existing versions untouched |
| LLM budget exhausted mid-extract | The build's own budget (a Workflow-principal budget, ADR-0020 machinery) halts the run cleanly (`budget` termination); resumable after window reset |
| Gate A fails | Quarantine pre-write + named worst offenders; `KnowledgeStale` remains if this was a staleness-triggered rebuild |
| Gate B fails | Staging namespace quarantined for inspection (13); violation list in KG CR status |
| Job/node death | DBOS resume from checkpoint — the whole design rests on this and it is tested (§8) |
| Review queue timeout | `ReviewTimedOut`, build failed visibly |
| Object store intermediate loss | Stages re-derive from content-addressed inputs upstream |

## 7. Observability & cost

Run-level: stage durations, docs in/out per stage, dedupe ratio, extraction P/R per entity type (the QA gate's scores are *metrics*, trended across builds — extraction quality drift is visible before it fails), LLM tokens/usd per run (receipts — a build shows its bill), review-queue depth/age. Conditions on the KG CR: `Ingesting/Validating/ProbesPassing/Ready` (01) + `LabelsStale`, `ReviewTimedOut`.

## 8. Testing

DBOS resume drill (kill the Job at every stage boundary + mid-extract; assert exactly-once effects); golden pipeline run on the fixture corpus (same inputs ⇒ byte-identical staged artifacts); Gate A fixture with a deliberately broken prompt (must quarantine pre-write); ambiguity fixture driving the review queue (pause/resume/timeout paths); e2e on k3d: `kg push` → Ready with probes green, then a poisoned doc batch → quarantine with named violations.

## 9. Decisions for async review

- **D1 — Batch builds are Jobs-with-DBOS** (0 standing pods; resume-not-restart); continuous mode is the connector worker (11).
- **D2 — Gate A (extraction QA) runs before any write** — the cheapest place to stop a bad model/prompt change.
- **D3 — The review queue never guesses and never silently expires**; resolutions replay on future builds.
- **D4 — Builds carry their own budget principal** — a KG build is a governed, receipted workload like any agent.

## 10. Resulting ADRs

Folded into ADR-0023 after critique PASS.
