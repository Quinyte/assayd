# Review: Design 16 — EvalSuite CRD, gate controller, dataset builder

- **Verdict**: **REVISE**
- **Reviewed**: `docs/designs/16-evalsuite.md` (draft, 2026-08-20)
- **Independence note**: reviewed in an independent session that did not author the draft.
- **Method**: all 8 lenses, with the requested focus: the gate flow against design 02's rollout machine and ADR-0020's candidate isolation; metric mechanics against the receipt schema (ADR-0021) and design 12 §3.8's seeds; DeepEval claims verified by fresh search (sources inline).

## Findings

### 1. MAJOR — the eval Job cannot "hold the gate-controller SVID": the candidate-isolation identity model is broken as written, and it hands candidate access to third-party runner images without a trust statement

`16-evalsuite.md:45,93` vs design 02 §3.3 (review f2) and design 06 §3.5. Two entangled defects:

- **The SPIFFE model doesn't work that way.** SVIDs are per-pod attested identities (ClusterSPIFFEID selector on pod labels — 06), not bearer credentials one workload can lend another. §10's "Eval Jobs hold: the candidate-route credential (gate-controller SVID)" is not implementable without breaking workload identity — and if it *were* implemented (mounting the controller's key material into Jobs), it would defeat the point of SVIDs. Meanwhile §4.2 routes eval traffic "at the candidate header-route (bound to the gate controller's SVID)" while the traffic's actual origin is the Job pod. Either the route policy rejects the eval traffic (isolation working, evals broken) or the identity is being shared (evals working, isolation broken).
- **The runner is third-party code.** Runner images are pack-delivered (`deepeval | inspect | byo` — §3, design 18 `runners` facet). Whatever identity reaches ungated candidates is being granted to code the platform didn't write — the same trust class as design 11's readers (r1 f2 there), and it needs the same treatment.

**Fix**: per-run eval identity — the Job pod gets its own SVID via the existing label template (`eval:<suite>@<run>` shape, matching the §10 receipt principal); the compiler adds that principal to the candidate header-route's admitted set **at Job launch and removes it at Job end** (a narrow, compiled, temporary grant — the design 01 A2 pattern applied to routes); the gate controller itself never proxies eval traffic. State the runner-image trust bar explicitly: signed image (18's facet rule), the candidate route + pinned KG version + judge egress are the *only* things the eval principal can reach (compiled, fail-closed), and the budget bounds the blast radius. Record the candidate-route admitted-set change as an ADR-0020/design-03 note.

### 2. MINOR — the mechanical metrics silently assume a capture level and KG scope the eval principal isn't stated to have

`16-evalsuite.md:65-66` vs ADR-0021 / design 04 §3.2 and design 01 A1. `tool_correctness` compares "expected vs actual tool-call *sequences*" from receipts — at the default `metadata` capture level, receipts carry `hop.target` (sequence: yes) but no arguments; if expected sequences include arguments, the eval run needs capture ≥ `headers`/`full`, which the candidate inherits from the *agent's* compiled `captureLevel` — unstated. Similarly `faithfulness_to_kg` has the runner calling `kg.cite` on the pinned version, which is scope-gated (01 A1): the eval principal's KG scope must mirror the agent's or cites deny. **Fix**: one statement — the eval principal's compiled posture is pinned independently of the agent's: capture `full` on eval-principal traffic (the report already stores at receipt capture rules, §10), KG scope mirroring the target agent's. And say which `tool_correctness` variant is normative (target-sequence at metadata vs argument-aware at full).

### 3. MINOR — `evalrunner/v1` and `evalreport/v1` are new contracts that never join the ledger

`16-evalsuite.md:78` vs design 07 §4. The `plume-contracts` ConfigMap exists so every shipped contract is version-checked at upgrade (N/N−1 operational); this design mints two contracts and doesn't add them. **Fix**: add both to the ledger list (and `doctor` gets them for free, per 08 r2).

### 4. MINOR — cost_regression's baseline window is unstated

`16-evalsuite.md:67`. "The active revision's trailing per-task cost" — trailing over what window, from which source? (Presumably the design-04 aggregate/audit index — a legitimate aggregate-class read — over some N days.) A regression gate against an undefined baseline isn't reproducible, which D2 otherwise guarantees. **Fix**: pin it — e.g. 7d trailing mean from the audit index, and the report records the baseline value + window alongside the digests.

## Lens summary

1. **Doctrine**: clean — 0 standing pods (Jobs/CronJobs), results in Postgres (substrate), datasets/reports as content-addressed artifacts.
2. **Charter**: the EvalRunner slot lands correctly (provider slot, conformance, N/N−1) with the §15 erratum handled the established way.
3. **Contract consistency**: finding 1 is the serious one; the rest is strong — the gate flow otherwise matches 02's machine exactly (Held → gate → weight shift / candidate deleted), 07 A4's tier contract composes, the 12 §3.8 seed consumption is verbatim-faithful, and D1's scope pin (eval once, canary is SLO-judged) kills the right scope creep.
4. **Hidden dependencies/circularity**: D2's digest-binding (dataset, judge, revision) makes rerun-shopping structurally impossible — the design's best property; finding 4 is the one unpinned number.
5. **Failure modes**: fail-closed everywhere (empty dataset, judge down, budget out) is exactly right; "candidate crashes under eval ⇒ that's the gate working" is the correct sentiment, stated.
6. **Security**: finding 1; the synthetic principal naming and report-at-capture-level redaction are right.
7. **Research freshness**: DeepEval claims verified — [Task Completion](https://deepeval.com/docs/metrics-task-completion) and [Tool Correctness](https://deepeval.com/docs/metrics-tool-correctness) are real built-in agentic metrics, and [`evals_iterator` collects traces and runs metrics as claimed](https://deepeval.com/docs/metrics-introduction); the research note is accurate. One nuance worth a line there: DeepEval's own ToolCorrectness works from its trace records — plume's is computed from *receipts*, which is why cross-runner parity on mechanical metrics is achievable (§8 already implies this; make it explicit).
8. **Testability**: the fixture-agent matrix covers every verdict path; report reproducibility asserted on digests; the flagship e2e is the P3 demo mechanized. Add: an isolation test (a non-eval principal replaying the header route during an eval run must be rejected — finding 1's regression test).

## Disposition

**REVISE.** The eval machinery itself — content-addressed datasets, honest judged-vs-mechanical metric taxonomy, fail-closed verdicts, the once-pre-canary scope pin — is the flagship design it needs to be. The one MAJOR is real and central: candidate isolation was design 02's hardest-won security property, and this design's identity story for reaching the candidate un-does it as written. The fix (per-run compiled principal grants) uses only machinery that already exists.

VERDICT: REVISE — 4 findings
