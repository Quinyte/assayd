# ADR-0028: The policy compiler binds agentgateway v1.4.1, proves convergence rather than enforcement, and publishes no budget bound at all

- **Status**: accepted · 2026-08-27 · **supersedes ADR-0020** · **amended 2026-08-27 (Amendment 1) — decision 3 was written before the execution spike and the spike refuted it**
- **Context**: ADR-0020 was written against "agentgateway 2.2". **No such release exists** — agentgateway ships `v1.x`, latest stable v1.4.1, and `2.2` was a kgateway-era documentation path that still returns HTTP 200 while serving content predating `AgentgatewayModel`, `virtualModel` and `oauthTokenExchange`. Every source ADR-0020 rested on was stale and looked live, so four internal critique rounds could not catch it; a cross-family review and then a primary-source re-verification did (`docs/research/agentgateway-v1.4.1-2026-08.md`, superseding `agentgateway-2.2-2026-08.md`).
- **Decision**:
  1. **Pin `v1.4.1`**, the floor for both OSS `oauthTokenExchange` and virtual models (both landed v1.4.0, not "2.2"). Absorb its breaking changes: Gateway API v1.6, `TCPRoute` v1, MCP request-phase guardrail rejections now HTTP 200. `AgentgatewayModel` is a **fourth CRD, experimental and off by default** — nothing may treat virtual models as stable.
  2. **The apply barrier proves control-plane convergence, not enforcement, and says so.** `Accepted=True/programmed` is retired; the predicate is the explicit per-kind tuple (`reason == Valid`, `observedGeneration` equality, Gateway-shaped `ancestorRef`, negative `StatusSummary` check, non-empty ancestors). A dataplane NACK surfaces as a Kubernetes Warning Event (`AgentGatewayNackError`), **never as a status condition**, so `Accepted`+`Attached` at the current generation can coexist with a proxy-rejected config. The operator watches that Event; absent it, no claim of proven enforcement may be made.
  3. **"Cuts early, never late" is withdrawn.** Token limits apply to future requests only — the request that crosses the budget completes — and `tokenize` (pre-dispatch estimation) is unreachable from Kubernetes: absent from the CRD API and hardcoded `false` on the xDS path. The gateway tier bounds *sustained rate*, not any single request. ~~It publishes a measured overshoot bound per request per replica instead of a ceiling.~~ **Corrected by Amendment 1: no bound is published.**
  4. **`burst` is not part of the token path.** It applies to `requests` limits only, so the emitted token bucket carries no burst allowance and the capacity arithmetic ADR-0020 implied is void. All emitted numeric fields are `*int32` and are compile-errors when they do not fit.
  5. **One concern per policy is now load-bearing, not stylistic.** Same-level conflicts iterate a randomly-seeded `HashSet`, so the winner varies **per replica and per restart** while both policies report Accepted+Attached. Research item R1 is closed by source, not by a reproducing test.
  6. **`egressAllowlist` compiles to Backend *construction*, not restriction** — the CRD has no egress or allowlist field anywhere. It requires a plume-owned provider→endpoint catalog, a ban on `dynamicForwardProxy`, and an effective-host predicate. **MCP tool filtering** moves to `spec.backend.mcp.authorization` (CEL over `mcp.tool.name`, `Allow`/`Require`), which filters `tools/list` items rather than only rejecting calls.
- **Consequences**: ADR-0020's two-tier budget guarantee is honestly weaker, and design 04's `usd_est` naming is now the accurate one — the receipt tier sums the same estimates the gateway tier uses, so neither is exact in USD and that must be stated wherever a budget is promised. Design 03 §3.3 and §3.5 are redesigned rather than amended; `docs/architecture.md` §01/§18 and its virtual-model line are corrected. Golden fixtures pin **tag v1.4.1**, never `main` — the commit the cross-family review pinned is unreleased, and `Burst` has no `Minimum` at the tag, so plume validates `burst >= 0` itself. Evaluation order, negative-burst runtime behaviour, and whether a NACK'd policy fails open remain **stated gaps** (research §9) and may not be encoded.

## Amendment 1 (2026-08-27) — decision 3's replacement bound was also false

Decision 3's replacement figure is **retracted** in turn. It was written before the execution spike,
which disproved it (`research/agentgateway-v1.4.1-spike.md` §2.7): against a 1,000-token hourly budget
where each request consumes 1,000 tokens, **100 concurrent requests all succeeded on one
replica** — 100,000 tokens admitted. Enforcement is a read-only availability check before
dispatch and a decrement after the response, so the excess tracks **concurrency**, which nothing
in `PolicyIntent` or the emitted policy constrains.

**No bound is published.** Gateway-tier excess is unbounded at v1.4.1; the receipt tier is the
only real limit. Bounding it would need a plume-side per-replica concurrency cap and a
request-size maximum, neither of which the CRD surface can express — a design change, owed and
undrafted.

Decision 2's consequence text also said NACK behaviour "remains open". The spike closed it
(§2.8): a NACK'd policy **retains the previous configuration** rather than failing closed, while
every Kubernetes condition reports converged at the current generation.

This is the third budget guarantee this ADR's lineage has retracted, and the first retracted by
measurement. It is recorded as an amendment rather than a supersession because the other five
decisions stand and were not written on the same premise.