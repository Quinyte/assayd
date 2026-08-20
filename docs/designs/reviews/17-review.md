# Review: Design 17 — Session replay engine

- **Verdict**: **REVISE**
- **Reviewed**: `docs/designs/17-session-replay.md` (draft, 2026-08-20)
- **Independence note**: reviewed in an independent session that did not author the draft.
- **Method**: all 8 lenses, with the requested focus: replay-mock routing against design 03's compiled-routes-only rule and ADR-0021's capture levels; the sampler against the fidelity-consumer rules.

## Findings

### 1. MAJOR — mocked replay is silent about LLM hops, so its determinism guarantee is false as stated

`17-session-replay.md:38-40,66`. Mocked replay's promise is "deterministic externals — isolate the agent's reasoning from the world's drift," and §9 asserts "same session, two replays ⇒ identical hop sequence." But the mock covers **tool/KG calls only** — the design never says what happens to the agent's **LLM hops**, and those are the *least* deterministic externals in the system: live LLM calls (even temperature 0, even pinned models) drift across runs and provider updates, so two mocked replays will diverge at the first model call and the §9 determinism test cannot pass. There is no coherent middle: either LLM responses are mocked from the recording too (true re-execution — the agent's code is exercised against a fully frozen world; requires `full` capture on LLM hops, which the mode already requires for tools), or the mode's guarantee must be rewritten as "deterministic *tools/KG*, live model" — a much weaker and differently-useful mode (it tests *robustness to model drift*, not reasoning isolation).

**Fix**: mock LLM hops from the recording by default in mocked replay (keyed like tool hops; a model call the recording never saw = divergence, the mode's first-class finding — which is exactly the interesting signal when debugging "why did the agent do that"); offer `--live-llm` as the explicit drift-probing variant with the determinism claim dropped for it; fix the §9 test accordingly.

### 2. MAJOR — re-drive's capture requirement contradicts ADR-0021's levels: the task input is a *body*, and bodies exist at `full`, not `headers`

`17-session-replay.md:37` vs design 04 §3.2. The fidelity table says re-drive requires "`capture ≥ headers` on the initial hop (**the task input body**)" — but in the receipt contract, `headers` captures headers; request/response *bodies* (the offloaded `request_ref`) are what `full` provides. As written, the design's spine — the fidelity table — promises replayability for a capture level that does not record the thing replay needs, and every downstream count ("34 excluded (capture level)") would be computed against the wrong boundary. **Fix**: either require `full` on the initial hop for re-drive (honest, no contract change), or — if capturing just the initial task body at `headers` level is genuinely wanted as a cheaper replayability tier — amend design 04 (a §11 entry: `headers` = headers + the initial A2A task body, with the size/offload rule) and let the compiler document the semantic. Pick one; the table and the sampler's exclusion rule follow from it.

### 3. MAJOR — the CLI's mock server can't be where the design puts it: the gateway cannot route to "a process started by the verb" on a laptop

`17-session-replay.md:40` vs design 03 (backends are cluster-reachable endpoints; compiled routes only). Mocked replay's mechanics run the mock as a local process spun up by the CLI verb, with the compiler pointing the replay principal's backends at it. Inside an eval Job that works (the mock is cluster-resident, in-pod or as a service). From a developer's laptop — the primary debugging UX — a gateway `Backend` cannot target a CLI-local process; there is no compiled route to a machine the cluster can't reach. **Fix**: the mock always runs **in-cluster** as a transient, namespaced pod/Job with a Service (the CLI verb creates it, streams results, tears it down — the same lifecycle discipline as everything else); the compiled Backend targets its Service; the CLI is a controller of the replay, never a data-plane endpoint. This also keeps recorded bodies inside the cluster instead of shipping them to laptops by default — a privacy bonus worth stating.

### 4. MINOR — the design-03 row is promised to "the next revision" instead of recorded now

`17-session-replay.md:40`. The replay-principal route ("one row, design 03 next revision") is exactly the cross-design commitment this review series has repeatedly converted from claim to record — to the design's credit it *names* the gap rather than asserting the row exists, but the mechanism for this is a concurrent design-03 amendment, not a promissory note. **Fix**: land the 03 row (replay-principal Backend → the transient mock Service; the *only* backend that principal can reach, fail-closed — §8 already states the property) in 03's amendment log as part of this design's approval, like 01 A2/A3 before it.

## Lens summary

1. **Doctrine**: exemplary — 0 pods, sessions as *views* over the stream + object store (D1), never a copy; replayability governed honestly by retention.
2. **Charter**: clean (Event consumed, Artifact exported, Agent driven); no new sockets.
3. **Contract consistency**: the session model reads the receipt envelope faithfully (task_id grouping, lineage trees, principal chain from first hop); finding 2 is the one place it misreads the contract; finding 4 is process hygiene.
4. **Hidden dependencies/circularity**: KG-hops-replay-against-pinned-versions (D2) is the standout insight — immutability makes mocks unnecessary exactly where correctness matters most; finding 1 is the same insight *not* applied to the one hop class that most needs a decision.
5. **Failure modes**: strong — gaps surfaced never smoothed, `BodiesExpired` names the retention boundary, divergence policy explicit (`--on-divergence`), replay budget bounded.
6. **Security**: recursive receipting of replays (D4) is the right kind of thorough; synthetic principals, tenant-scoped read + invoke rights, mock-only reachability, tamper-evident exports — all sound; finding 3's in-cluster fix improves the body-locality story further.
7. **Research freshness**: nothing external and load-bearing; n/a.
8. **Testability**: good suite (reconstruction goldens with gap/lineage cases, divergence fixture, scrub assertions, exclusion counting); §9's determinism test is currently unpassable (finding 1) — after the fix it becomes the mode's defining regression.
9. **Sampler check** (requested): clean — reads the stream (fidelity rule honored, never the index), stratified with deliberate failure over-sampling, exclusions counted per the no-silent-caps rule, sampling pinned into the dataset artifact for reproducibility. No findings.

## Disposition

**REVISE.** The session model, the views-not-copies discipline, divergence-as-finding, and recursive receipting are all exactly right, and the sampler passes its scrutiny untouched. But the three MAJORs cluster on the mocked-replay mode: its determinism claim ignores LLM hops, its capture prerequisite misreads the receipt contract, and its mock placement doesn't route. All three have clean fixes that make the mode simpler and stronger — mock the model too, require `full` (or amend 04 deliberately), run the mock in-cluster.

VERDICT: REVISE — 4 findings

---

## Re-review r2 (2026-08-20)

- **Verdict**: **PASS** (1 wording residual)
- **Independence note**: same independent session as r1; did not author the draft or revision.

### Per-finding disposition

| r1 | Severity | Disposition |
|---|---|---|
| 1 | MAJOR | **Resolved.** LLM hops mocked by default in mocked replay — the fully-frozen-world guarantee is now true re-execution; `--live-llm` is the explicit drift-probing variant with the determinism claim dropped for it; the §9 determinism test is updated and now passable ("the test that r1 f1 made passable" — the design says it itself). A model call the recording never saw is divergence, which is exactly the interesting debugging signal. |
| 2 | MINOR→ | **Resolved** (was MAJOR). Re-drive requires `capture: full` on the initial hop — the honest option, no contract change; below-full sessions listed as non-replayable with the reason. **Residual R2-a**: §5's sampler text still says "Non-replayable (**metadata-level**) sessions are excluded" — with the corrected boundary, `headers`-level sessions are excluded too; update the parenthetical (and the example count's label) to "below-`full` capture". One word, but it's the exclusion boundary the dataset reports will print. |
| 3 | MAJOR | **Resolved.** The mock is in-cluster: short-lived mock Job + Service created by the verb/eval Job, seeded from content-addressed refs, torn down after; the CLI is a controller, never a data-plane endpoint. Recorded bodies stay in-cluster — the privacy bonus, taken. |
| 4 | MINOR | **Resolved.** The replay-mock route is recorded in design 03 §3.4 now (verified: "Replay-mock route — replay-principal backends → in-cluster mock Service, fail-closed"), alongside the eval temporary-grant row. |

### Verdict

**PASS.** All three MAJORs closed with the simplifying fixes — mock the model too, require `full`, run the mock in-cluster — and the cross-design row landed as a record, not a promise. Fix the §5 boundary wording at ADR-0024 time.
