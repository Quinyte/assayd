# Review: Design 15 — Probe engine (semantic readiness)

- **Verdict**: **REVISE**
- **Reviewed**: `docs/designs/15-probe-engine.md` (draft, 2026-08-20)
- **Independence note**: reviewed in an independent session that did not author the draft.
- **Method**: all 8 lenses, with focused scrutiny (as asked) on the trust-but-verify probe model's soundness. Consistency vs designs 01 (+A1), 12 r2, 13/14 (drafts, reviewed concurrently), ADRs 0005/0007/0017/0020.

## Findings

### 1. MAJOR — probe *execution semantics* are undefined for `q`-only probes on providers that are forbidden to use an LLM

`15-probe-engine.md:32` and design 12 §3.6 vs ADR-0017. A probe is a natural-language question (`q: "Does policy P-1042 cover CPT 97110 without prior auth?"`) with matchers (`answer: {contains: "no"}`). Providers are contractually deterministic — no provider-side LLM (ADR-0017, conformance-enforced). So what does *executing* a `q`-only probe (no `via:`) mean? `kg.search(q)` returns hits and snippets, not an "answer"; matching `contains: "no"` against search snippets is semantically meaningless (and hilariously brittle — "not covered", "no prior auth needed", and "nothing found" all contain "no"). The `via: {bundle}` form is well-defined (execute the bundle recipe, match on `text`), but it's optional — and everything downstream (this engine's verdicts, 14's promotion gate, 16's golden-set derivation) inherits the ambiguity of the default case.

**Fix**: make `via` **required** — every probe names its execution (`via: {bundle, params}` or `via: {search: {query, top_k}}` with matchers defined against the specific output shape: bundle `text`/`citations`, or search hit fields). `q` becomes the human-readable label — what the probe *means*, displayed in dashboards and reviews — never the executable. This is a design 12 amendment (§3.6 + validation: probe without `via` ⇒ error) plus a rewrite of §3.3's first sentence here. It also makes finding 2 possible.

### 2. MAJOR — 20% sampled verification is unsound exactly where it matters most: the one-shot promotion gate

`15-probe-engine.md:23,32,46` and D2. The trust-but-verify arithmetic works for *repeated* games: continuous hourly runs re-verify 20% each time, so a persistently lying provider is caught in expectation within a handful of runs. But **promotion is a one-shot decision** — the platform's central admission signal (Ready = probes pass, FR-12). At promotion, a compromised or buggy provider self-reporting `pass` has an 80% chance *per probe* that the lie goes unverified in that run, and the graph promotes on unverified claims. Trusting the component under test to grade itself, at the moment of admission, inverts the platform's own zero-trust posture ("guarantees live outside the agent" — the same logic applies to providers). And once finding 1 makes every probe engine-executable via the public query surface, there is no reason to accept this: the engine can execute everything itself.

**Fix**: split the semantics by stakes — **promotion-time: 100% engine-side execution** via the query surface (bundles/search per `via`), engine judges matchers, provider grades nothing; **continuous mode: keep the cheap path** (`kg.probe` self-report + 20% re-verification — fine for a repeated game, and it keeps hourly cost low). `kg.probe` remains in the contract unchanged (ADR-0017 untouched): it's the provider's self-check, the conformance suite's parity probe (self-report must match engine-side results — a standing conformance assertion), and the continuous mode's workhorse. `ProviderUntrusted` machinery stays for the continuous path. This is strictly sounder and *cheaper in trust* than D2 as drafted.

### 3. MINOR — `ontology.health.threshold` doesn't exist in ontology/v1

`15-probe-engine.md:23` vs design 12 r2 (whole document). The verdict rule reads a field (`ontology.health.threshold`) that the normative spec never defines — 12's probes have ids/`q`/`via`/matchers and nothing else; the architecture sketch (§04) put thresholds in the *KG CR's* `health.probes`. Pick a home: **Fix** — recommended: the threshold lives in the ontology doc (it's semantic content, versioned with the probes it governs) as a new `health: {pass_threshold: 1.0}` section — a design 12 §11 amendment; the KG CR may *tighten* but never loosen it. Alternatively the KG CR owns it outright; either way one owner, stated in both designs.

### 4. MINOR — the probe principal's budget has no declared home (shared with 14-review finding 6)

`15-probe-engine.md:38`. "Its own budget … ADR-0020 machinery" — but ADR-0020 compiles from `PolicyIntent` per CR target, and nothing says what declares the probe principal's budget or what identity the probe Job presents. Same gap, same fix as design 14's build principal: the KG CR carries a `probes.budget` block; the operator synthesizes the intent for the platform probe identity; receipts attribute `principal: probe:<graph>@<version>`. State it once, cross-reference 14.

## Lens summary

1. **Doctrine**: clean — 0 standing pods (Jobs + CronJob), results in CR status + bounded Postgres history, probe traffic through the gateway so it's receipted like everything else.
2. **Charter**: correct (engine slow, probe sets fast-plane data); primitives honest.
3. **Contract consistency**: findings 1 and 3 are both 12/15 seam gaps; the promotion handoff matches 14 §3 and 01 §4 (operator promotes, provider doesn't); ADR-0007's same-detector-clears rule is honored in continuous mode.
4. **Hidden dependencies/circularity**: the model's one trust inversion is finding 2; otherwise the separation is clean (engine judges, provider executes — pending finding 1 making that sentence true).
5. **Failure modes**: genuinely excellent — `ProbeInfraError` ≠ `KnowledgeStale` (D3) is the distinction most systems never draw; empty-probe-set promoting only via explicit `--allow-unprobed` (D4) keeps semantic readiness from going vacuous; runs pinned to their doc version; pause-during-rebuild avoids self-inflicted noise.
6. **Security**: platform-identity execution per ADR-0017, own budget (finding 4's home aside), `ProviderUntrusted` as a page — right instincts; finding 2 closes the one real hole.
7. **Research freshness**: nothing external and load-bearing; n/a.
8. **Testability**: the fixture list is strong (lie fixture, flake-vs-real, budget isolation, unprobed path); after findings 1/2, add: a `q`-only probe fails validation, and promotion-time engine-side execution produces verdicts with the provider's self-report disabled entirely.

## Disposition

**REVISE.** The design's failure-mode thinking (infra ≠ staleness, explicit-unprobed, doc-pinned runs) is the best in the P2 batch, and the continuous-mode drift feed is well-shaped. But the two MAJORs sit at the model's root: probes aren't executable as specified (the `q`-only default has no defined semantics for LLM-free providers), and the trust-but-verify compromise samples away exactly the verification the one-shot promotion gate needs. Both fixes simplify the design rather than complicate it — `via`-required makes probes mechanical, and stakes-split verification deletes the promotion-time trust problem instead of statistically managing it.

VERDICT: REVISE — 4 findings

---

## Re-review r2 (2026-08-20)

- **Verdict**: **PASS** (1 wording nit)
- **Independence note**: same independent session as r1; did not author the draft or revision.

### Per-finding disposition

| r1 | Severity | Disposition |
|---|---|---|
| 1 | MAJOR | **Resolved.** `via` required, recorded where it belongs (design 12 §11 A1a) with both execution forms and shape-specific matchers; `q` demoted to display label; the `q`-only-fails-validation test added. Probes are now mechanical by construction. |
| 2 | MAJOR | **Resolved.** Stakes-split verification exactly as recommended: promotion = 100% engine-side via the public query surface (provider grades nothing at admission; "immune by construction" in the failure table); continuous = self-report + 20% re-verify + the standing conformance parity assertion; `kg.probe` and ADR-0017 untouched. D2 rewritten. |
| 3 | MINOR | **Resolved.** `health: {pass_threshold}` lives in the ontology doc (12 §11 A1b), versioned with its probes; KG CR may tighten, never loosen. |
| 4 | MINOR | **Resolved.** `probes.budget` on the KG CR mirroring 14's `build.budget`; `principal: probe:<graph>@<version>` attribution. |

### Nit (non-blocking)

§2's summary line still describes both Job kinds as "calling `kg.probe` through the gateway" — true for continuous mode only; promotion-time runs now execute via the public query surface (§3.1). One clause at ADR-0023 time.

### Verdict

**PASS.** Both MAJORs closed by the simplifying moves — mechanical probes and stakes-split trust — leaving a design whose failure-mode discipline was already the batch's best. Fold into ADR-0023 with the §2 clause.
