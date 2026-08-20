# Review: Design 03 — Policy compiler

- **Verdict**: **REVISE**
- **Reviewed**: `docs/designs/03-policy-compiler.md` (draft, 2026-08-20)
- **Independence note**: reviewed in a fresh session that did **not** author the draft. No independence caveat needed.
- **Method**: all 8 critique lenses; consistency checked against architecture.md, approved designs 01+02 (and their reviews), ADRs 0003/0012/0014/0017–0019; agentgateway claims spot-verified by web search this session (see finding 2 sources).

## Findings

### 1. BLOCKER — non-atomic apply creates fail-open windows; the "nothing partially applied" claim only covers compile time

`03-policy-compiler.md:74-83`. The design carefully makes the *compile* step transactional (compile error ⇒ nothing applied) but says nothing about the *apply* step, which SSA-applies N separate resources (Backends, Policies, HTTPRoutes) with no transactionality. Failure scenario: a new `expose` HTTPRoute applies successfully but its `-auth` policy is rejected by the gateway webhook (or applies but reports `Accepted=False`) → the route serves **unauthenticated** traffic. Same class of hole for `-ratelimit` (unbudgeted traffic) and candidate-isolation policies (ungated revision reachable — undoing design 02 review finding 2). For a component whose stated purpose is "everything the platform promises at the gateway becomes real here" (§1), unspecified apply ordering is a security defect, not a detail.

**Fix**: specify fail-closed apply ordering — Backends → Policies → **verify each policy reports Accepted/programmed status** → Routes/weight changes last; on any policy apply/acceptance failure, do not attach (or zero-weight) the dependent route and set a condition (`PolicyApplyIncomplete` or similar). Add a failure-table row for "policy applied but not accepted by gateway" (status feedback loop) and for partial apply. State the same ordering for the *removal* direction (routes detach before their policies are deleted).

### 2. MAJOR — the design's central factual claims about agentgateway 2.2 are unsourced, not landed in `docs/research/`, and partially contradicted by current docs

`03-policy-compiler.md:48` ("The overlap rule (hard, from research)"), `:56` ("native OSS capability"), `:57` ("spend/budget-limit features are Solo Enterprise"), `:63` ("native order"), `:66` (`frontendPolicies`). CLAUDE.md rule 4 requires landscape/library findings to be searched with the current year, cited, and landed in dated `docs/research/` files. `docs/research/landscape-2026-08.md` contains none of these claims — the phrase "from research" points at nothing in the repo. These claims are load-bearing: D2 (the entire one-concern-per-policy emission strategy) rests on the silent-overwrite claim, and D1 (no enterprise dependency) rests on the OSS/enterprise feature split.

Spot-verification this session: the OSS/enterprise split is *directionally supported* — [budget and spend limits are documented under Solo Enterprise 2.2.x](https://docs.solo.io/agentgateway/2.2.x/llm/budget-limits/) and [token-based rate limiting exists in OSS](https://agentgateway.dev/docs/kubernetes/2.2.x/security/rate-limit-global/). But the silent-overwrite claim is **not confirmed**: current agentgateway docs describe a defined attach-point precedence (Gateway → Listener → Route → Route Rule → Backend) with overlapping rules *merging* into one decision per request — which at minimum complicates "both show ACCEPTED, creation order wins". If the overwrite behavior is real only for same-target/same-level/same-field policies, the doc must say exactly that, with a source or a reproducing test.

**Fix**: land a dated `docs/research/agentgateway-2.2-YYYY-MM.md` note with citations for: (a) exact overlap/merge/overwrite semantics at each attach level; (b) which rate-limit modes are OSS vs enterprise (see also finding 3); (c) the real CRD/field names (`AgentgatewayBackend`, `AgentgatewayPolicy`, `frontendPolicies`, tool-filter policy, prompt-guard policy); (d) the native evaluation order. Cross-reference it from §3.2/§3.3. If the overwrite claim survives verification, D2 stands; if not, D2's justification must be rewritten (one-concern-per-policy may still be desirable for diffability, but then say that honestly).

### 3. MAJOR — daily budget enforcement semantics don't hold up: token *bucket* ≠ design 02's approved fixed window, and local-vs-global rate limiting is unaddressed (possible doctrine violation hiding here)

`03-policy-compiler.md:56` vs `02-agent-crd-operator.md:46` (approved: "per-day windows reset 00:00 UTC; remaining budget in status", review 02 finding 3). Two distinct problems:

- **Window semantics**: a token bucket refills continuously at a rate; it does not "reset at 00:00 UTC". Either the gateway supports fixed calendar windows (cite it), or the compiled semantics differ from the approved, documented user-facing semantics — which would require amending design 02/ADR-0019, not silently diverging.
- **Counter locality**: the architecture runs agentgateway at 1–2 pods (§17). A *local* (per-instance) token limit under-enforces by up to 2× across replicas; a *global* limit needs shared counter state — agentgateway's global rate limiting uses an external rate-limit service ([OSS global rate-limit docs](https://agentgateway.dev/docs/kubernetes/2.2.x/security/rate-limit-global/), [local-vs-global issue #1911](https://github.com/agentgateway/agentgateway/issues/1911)), and typical Envoy-lineage RLS deployments back onto Redis — which doctrine rule 2 **forbids** (Postgres+NATS only) — and would also add a pod to the §17 budget. The design claims "Pods added: 0. Stateful deps: none" (§2) without confronting this.

Also unanswered: how "remaining budget in status" (design 02 §3.1) gets fed from the gateway's counters back to the operator.

**Fix**: research and state which rate-limit mode is compiled (local/global), where counter state lives, and reconcile with the doctrine gate — acceptable resolutions include: local limits divided by replica count as the gateway-side *approximation* with the receipt backstop as the exact enforcer (making the two-tier D3 story carry tokens too, not just usd), or an RLS backed onto the existing Postgres. Whatever is chosen, align the window semantics with design 02 or amend it explicitly, and state the status feed for remaining budget.

### 4. MAJOR — `usdPerDay` compilation is undefined for multi-model agents, contradicting the claimed "total" property

`03-policy-compiler.md:57,74`. `PolicyIntent.llm.providers[]` is plural and the pricing table is per-model — but the output is a single token-equivalent limit. Which model's price converts $40/day into tokens when the agent may call gpt-x at $0.01/1k and a local model at ~$0? Any single choice is wrong in one direction; the design doesn't state the rule, yet claims every field "either maps or returns a compile error — no silently ignored fields" (§4). Related gap in the failure table (`:82`): "pricing table missing" is covered, but **partial** coverage — a model in `llm.providers[]` matching no `model_pattern` — is not.

**Fix**: define the rule explicitly. Recommended: conservative compile — token equivalent computed at the **max** price across the agent's allowed models (gateway cutoff can only be early, never late; the receipt backstop is the exact tier per D3), and *unmatched model pattern ⇒ compile error* (a new failure row). Golden-file fixtures should include a multi-provider agent.

### 5. MAJOR — the "operator-side backstop" is an undesigned hidden dependency and an unrecorded delta to approved design 02

`03-policy-compiler.md:57` ("actual cost from receipts; overrun ⇒ weight-0 + `BudgetExhausted` condition"). This one clause quietly commits the agent-operator to: (a) consuming and aggregating the receipt stream (design 04 — not yet designed) as a *reconcile input*, where approved design 02 §4 states the operator's inputs are the CR + owned objects and "no state outside CR status + directory KV"; (b) a running daily-spend counter whose storage, crash-resume, and 00:00 UTC reset are unstated; (c) a new Agent condition `BudgetExhausted` absent from design 02's approved condition list (`:56-57`); and (d) zero-weighting the **active** revision — a traffic action design 02 reserves for the rollout state machine. None of this is wrong per se — but a library design (03) is amending an approved operator design (02) by side effect, without recording the delta.

**Fix**: name the owner and the seam. Recommended: design 04's receipt pipeline owns cost aggregation (e.g. a per-agent daily spend materialized where receipts already land — Postgres) and exposes it as data the operator reads during reconcile; design 03 records only "backstop consumes aggregated spend, interface owned by design 04". Record the design-02 deltas explicitly (new condition, new reconcile input, weight-0-on-exhaustion behavior) — a one-paragraph amendment note in 02 or the forthcoming ADR-0020 is enough, but it must be written down.

### 6. MAJOR — KG scope enforcement mapping is incomplete: `kg.get_context_bundle` and `kg.cite` are unscoped, and response-body stripping is an unverified gateway capability

`03-policy-compiler.md:61`. The mapping constrains `kg.search/neighbors` params and "strips out-of-scope results" — but the six-tool surface (design 01 §3.3) has two more fact-bearing exits: `kg.get_context_bundle` (the workhorse — its `text`/`citations` can carry any entity type the bundle recipe touches) and `kg.cite` (design 01 §6 explicitly requires cite "gated by the same scope filters"). As mapped, an agent scoped to `[Policy, Procedure]` retrieves out-of-scope entities through any bundle. Separately, "strips out-of-scope results" requires the gateway to parse and rewrite MCP response bodies per entity type — a capability the design asserts without a source, and one in tension with design 01 §3.1's premise that the gateway operates on `Mcp-Method`/`Mcp-Name` headers "without parsing bodies". If response filtering isn't a real 2.2 capability, the whole scope-enforcement-at-the-gateway promise (design 01 §6, architecture §13) needs a different mechanism — e.g. the gateway *injecting* a trusted scope parameter that the provider enforces.

**Fix**: extend the mapping row to all four fact-bearing tools; verify (with citation, per finding 2) that agentgateway 2.2 can filter MCP responses — if not, compile scope as a gateway-injected, provider-enforced request constraint and record the trust-model consequence (provider becomes scope-enforcing; conformance must test it). Either way `KG_SCOPE_DENIED` (design 01's error) should be named as the deny behavior.

### 7. MINOR — `knowledge.scope.bundles?` has no producer

`03-policy-compiler.md:31`. The approved Agent CRD (design 02 §3.1) defines `scope: {entityTypes}` only; nothing produces `bundles?`. A compiler input no operator populates is dead contract surface — or an unflagged CRD change. **Fix**: drop it, or mark it reserved with a note that it implies a design-02 schema addition.

### 8. MINOR — header ADR list: ADR-0014 is relied on but not listed; ADR-0012's relevance is unstated

`03-policy-compiler.md:5,31,63`. The body cites ADR-0014 twice (LLM egress allowlists, guards) but the header lists 0003, 0012, 0019. ADR-0012 (no-mesh) is at best indirectly relevant and never referenced in the body. **Fix**: add 0014; drop 0012 or justify it in one clause.

### 9. MINOR — deterministic name scheme `<name>-<concern>[-<revision>]` can exceed Kubernetes name limits

`03-policy-compiler.md:40`. A near-63-char agent name + `-toolfilter` + `-<hash>` overflows label limits and can approach object-name limits; determinism then breaks exactly when it matters (byte-identical golden files). **Fix**: define a truncate-plus-hash-suffix rule in the design and cover it with a golden-file fixture at max name length.

### 10. MINOR — `PricingStale` has no stated home, and the pricing ConfigMap has no write-authz story

`03-policy-compiler.md:70`. ConfigMaps don't carry conditions — which object surfaces `PricingStale`? Also, the ConfigMap is "user-overridable": anyone who can edit it can inflate token equivalents past `usdPerDay` until the receipt backstop catches up — worth one sentence in §6 (RBAC on the ConfigMap; backstop bounds the damage). **Fix**: name the condition's carrier (e.g. every Agent CR with a usd budget, or an operator-level object) and add the RBAC note.

### 11. MINOR — e2e coverage gaps against the design's own promises

`03-policy-compiler.md:95`. The e2e list tests the gateway tier but not: the receipt-backstop path (overrun ⇒ weight-0 + `BudgetExhausted`), `PricingStale` surfacing, or the policy-rejected feedback loop (finding 1's `Accepted=False` case). D3 declares two-tier enforcement as *user-facing semantics* — the second tier deserves a test. **Fix**: add these three assertions (backstop test can drive synthetic receipts).

## Lens summary

1. **Doctrine**: provisionally clean (library, 0 pods) — **contingent on finding 3** (a global rate-limit service or Redis-backed counter would violate rule 2 and the pod budget).
2. **Charter**: clean — slow plane, library-in-operator matches the §12 component plan; five primitives respected; pricing table is legitimately a versioned data artifact.
3. **Contract consistency**: findings 3, 4, 5, 7, 8 — the recurring failure mode is design 03 silently amending approved design 02 semantics (budget window, condition set, operator inputs).
4. **Hidden dependencies/circularity**: finding 5 (receipts backstop); no circular hash-class bugs found; the pure-function claim holds for compile itself.
5. **Failure modes**: findings 1, 4, 10 — apply-time atomicity is the big absence; compile-time coverage is genuinely good.
6. **Security**: findings 1, 6, 10; the admission-deny on non-operator gateway writes and compiled candidate isolation are sound.
7. **Research freshness**: finding 2 — the design's sharpest claims are exactly the unsourced ones; partial contradiction found on the overlap claim.
8. **Testability**: findings 9, 11 — golden files + property test + overlap check are a strong base.

## Disposition

**REVISE.** The core shape is right — pure-function compiler as a library, one-concern-per-policy for diffability, explicit OSS-only stance, honest two-tier budget semantics (D3 is the best paragraph in the doc). But finding 1 is a fail-open hole in the platform's central security promise, and findings 2–6 show the two load-bearing decisions (D1, D2) resting on unlanded research while quietly amending an approved design. Fix, land the research note, re-run critique.
