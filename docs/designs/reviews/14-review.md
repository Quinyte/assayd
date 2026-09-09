# Review: Design 14 — Ingestion pipeline + extraction QA

- **Verdict**: **REVISE**
- **Reviewed**: `docs/designs/14-ingestion-pipeline.md` (draft, 2026-08-20)
- **Independence note**: reviewed in an independent session that did not author the draft.
- **Method**: all 8 lenses, with focused scrutiny (per this component's nature) on the DBOS-in-Job resume model and Gate A's statistics. Consistency vs designs 01 (+A2), 11 r2, 12 r2, 13 (draft, reviewed concurrently), 15 (draft), ADRs 0004/0009/0017/0020, `docs/research/connectors-2026-08.md` §DBOS.

## Findings

### 1. MAJOR — the extract stage collides with design 13's extraction-at-write (shared seam defect; primary analysis in 13-review finding 1)

`14-ingestion-pipeline.md:26,31` vs `13-graphiti-adapter.md` §3.2/§3.4 and design 01 §3.4. This pipeline extracts (LLM via gateway, per-doc idempotent) and then calls `write_batch` — which design 13 currently implements as `add_episode`, i.e. extraction *again*, adapter-side, after Gate A has already passed. Only one owner is possible, and this design's own architecture (Gate A **before any write** — D2) forces the answer: extraction is pipeline-side; `write_batch` must be the structured node/edge upsert design 01 specifies. **Fix**: state it explicitly here — the extract stage's output is *typed elements + provenance*, and the write stage performs **structured writes only** (no adapter-side extraction; cross-reference the 13 resolution). One sentence, but it pins the seam this whole pipeline stands on.

### 2. MAJOR — the 72-hour review-queue pause meets the Job execution model, and nobody says what the pod does for three days

`14-ingestion-pipeline.md:15,47`. §2 promises "0 standing pods for batch"; §5 pauses a run durably for up to `reviewTimeout: 72h` awaiting a human. Those compose only if the pause mechanics are specified, and they aren't: either the Job pod idles for days (a standing pod in all but name, evictable at any moment — recoverable via DBOS, but then it idles again), or the workflow parks and the pod should *exit* — in which case something must relaunch the Job when `assayd kg review` resolves the entry, and nothing is named as that something. The same question applies to the budget-exhaustion halt (`:54` — "resumable after window reset": resumed *by whom*?).

**Fix**: specify the park/resume lifecycle — on review-pause (or budget halt) the DBOS workflow enters a durable waiting state and **the Job exits cleanly** (0 pods, honestly); the operator watches the review queue / budget window and **relaunches the build Job on resolution**, where DBOS resume picks up mid-stage (deterministic workflow id `build-<graph>-vN+1` — already right for this). Add the pause/resume drill to §8's DBOS tests (pause → Job gone → resolve → relaunched → resumes at the paused stage).

### 3. MAJOR — Gate A's statistics can't support its bars: ~20 labeled docs cannot discriminate P/R at 0.9/0.85, and per-type scoring is noise

`14-ingestion-pipeline.md:40-43`. The gate scores precision/recall against bars like `entity_pr: 0.9/0.85` on a sample bootstrapped at ~20 docs, and "names the worst-scoring entity types". Run the numbers: 20 docs might yield 50–200 labeled entity instances total; at n=100, a true P of 0.9 has a 95% CI of roughly ±0.06 — the gate will quarantine good builds and pass bad ones on sampling noise alone, and the per-type breakdown (maybe 5–20 instances per type) is statistically meaningless — "worst-scoring type" will often just be "smallest type". A gate that flakes teaches operators to override it, which is worse than no gate.

**Fix**: make the statistics explicit. (a) Gate on the **Wilson lower bound** (or Jeffreys) of the pooled P/R being ≥ bar − stated tolerance, not the point estimate; (b) per-type scores are *reported* only for types with ≥ a minimum instance count (say 30) and never gate individually below it; (c) state the sample-growth loop — review-queue resolutions and probe failures feed `labels/` so n rises with graph age (the design already versions the sample; give it a growth engine); (d) `kg init`'s ~20-doc bootstrap is labeled explicitly as "gate advisory until n ≥ N" with the loud condition (`LabelsSparse`) rather than pretending 0.9 is measurable on day one. Trend-over-builds (§7 already does this) is the real early-warning signal — say the gate leans on it.

### 4. MINOR — "byte-identical staged artifacts" is not achievable through a live LLM stage

`14-ingestion-pipeline.md:36,67`. "Reruns of the same inputs are no-ops end-to-end" holds via content addressing, but the golden test "same inputs ⇒ byte-identical staged artifacts" runs through `extract` — an LLM call that is not byte-deterministic across provider updates even at temperature 0. **Fix**: scope the golden test — deterministic stages (normalize, resolve, gates, write) assert byte-identity *given a pinned fixture extraction* (mock/recorded LLM responses in CI); live-LLM runs assert gate outcomes and score ranges, never bytes. One sentence; keeps CI honest.

### 5. MINOR — `dir.changed` is the wrong trigger name

`14-ingestion-pipeline.md:20`. `dir.changed` is design 05's *directory* CloudEvent (agent registrations) — irrelevant to ingestion. The intended triggers are presumably: `assayd kg push`, the Connector schedule (11), design 11's connector events (source-changed webhooks), and design 20's staleness remediation. **Fix**: name the actual trigger set; drop the directory event.

### 6. MINOR — the build's budget principal has no declared home

`14-ingestion-pipeline.md:54,74`. D4 gives builds "their own budget principal — ADR-0020 machinery", but ADR-0020 compiles budgets from `PolicyIntent` produced for CRs; nothing states which object declares the build's budget (the KG CR? an operator-synthesized intent per run?) or what identity the Job presents at the gateway. Design 15 has the same gap for probe budgets (15-review f4). **Fix**: one statement — the KG CR carries `build.budget` (tokens/usd per run or per day); the operator synthesizes the PolicyIntent for the build principal (a platform service identity per 06) at Job launch; receipts attribute to `principal: build:<graph>@vN+1`.

## Lens summary

1. **Doctrine**: the Jobs-with-DBOS model is doctrine at its best (0 standing pods, Postgres-backed resume — research-grounded) — finding 2 is about making its hardest case (multi-day pause) true rather than approximate.
2. **Charter**: clean; behavior-as-data claims (readers from packs, prompts from ontology, gates from ontology, redaction from profiles) all check against 11/12/ADR-0014.
3. **Contract consistency**: finding 1 (the 13 seam) and finding 6; otherwise strong — quarantine semantics match 01/13, `(batch_id, seq)` idempotency matches 01, the handoff to 15 matches 01 §4's operator-promotes-on-probes.
4. **Hidden dependencies/circularity**: content-addressing all the way down is the right spine; the review queue's replay memory ("same pair ⇒ same answer") is a quiet gem — but note it needs an invalidation rule when the ontology majors (stale resolutions replaying against changed types; one line).
5. **Failure modes**: comprehensive — budget-halt, gate failures, node death, review timeout all have honest rows; finding 2's missing row is the pod lifecycle during pause.
6. **Security**: inherits 11's scoped write grants (01 A2) and the restricted DB role split from the research note; PII redaction placed at normalize per architecture §13. No new gaps found.
7. **Research freshness**: DBOS claims consistent with the landed note; nothing else external and load-bearing.
8. **Testability**: the DBOS kill-drill at every stage boundary is exactly the test this design owes; findings 3/4 are about making Gate A and the goldens assert what is actually measurable.

## Disposition

**REVISE.** The pipeline's spine — content-addressed stages, quarantine-not-degrade, gates derived from the ontology, never-guess resolution — is the strongest expression yet of the platform's "measured, loud, resumable" philosophy. The three MAJORs are all places where a true mechanism meets an unstated boundary condition: the extraction seam (shared with 13), the pause-vs-pod lifecycle, and a gate whose bars outrun its sample size. All have concrete fixes; none threaten the shape.

VERDICT: REVISE — 6 findings

---

## Re-review r2 (2026-08-20)

- **Verdict**: **PASS**
- **Independence note**: same independent session as r1; did not author the draft or revision.

### Per-finding disposition

| r1 | Severity | Disposition |
|---|---|---|
| 1 | MAJOR | **Resolved.** The extract stage is marked "THE extraction owner" with typed-elements-plus-provenance output; the write stage is "STRUCTURED writes only" with the 13 r2 cross-reference. The seam is pinned identically from both sides. |
| 2 | MAJOR | **Resolved.** Park/resume lifecycle fully specified: durable DBOS waiting state, Job exits cleanly (0 pods true even mid-review), operator watches the queue and relaunches on resolution via the deterministic workflow id; the same mechanic reused for the budget halt (the §6 row updated to match); park/relaunch drill added to §8. |
| 3 | MAJOR | **Resolved — thoroughly.** Wilson lower-bound gating with stated tolerance; per-type reporting floored at ≥30 instances and never individually gating below it; the sample-growth engine named (review-queue resolutions + failed-probe investigations); advisory mode with loud `LabelsSparse` until n ≥ 200; trend as the standing early-warning. The replay-memory invalidation on ontology majors (a lens note, not even a numbered finding) was adopted too. |
| 4 | MINOR | **Resolved.** Golden byte-identity scoped to deterministic stages with pinned/recorded LLM fixtures; live runs assert outcomes and ranges. |
| 5 | MINOR | **Resolved.** Trigger list corrected (`kg push`, Connector schedule, connector source-change events via 21, staleness remediation); `dir.changed` gone. |
| 6 | MINOR | **Resolved.** `build: {budget}` on the KG CR, operator-synthesized PolicyIntent, `principal: build:<graph>@vN+1` attribution, mirrored by design 15. (Editorial nit: the paragraph landed under §8 "Testing" — move it to §3 or §7 when convenient; content is right.) |

### Verdict

**PASS.** All six findings addressed; the Gate A rework in particular is the model answer — the gate now claims only what its sample can support, and grows the sample instead of pretending. Fold into ADR-0023 with the placement nit.
