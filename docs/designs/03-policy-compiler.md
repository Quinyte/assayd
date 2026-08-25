# Design 03: Policy compiler (PolicyIntent → agentgateway 2.2 resources)

- **Status**: **approved** — critique PASS at r2 (reviews/03-review.md; residuals R2-a/R2-b folded in) · ADR-0020. **Amendments A1–A5 (2026-08-25) raised at the start of implementation and folded into §§3–8; they have not yet been critique-passed** (§11)
- **Phase**: P1 · **Size**: M · **Date**: 2026-08-20
- **ADRs**: 0003, 0014, 0019 · interfaces: designs 02 (caller; amendments recorded there §11), 04 (spend aggregation owner), 22 (loop governance)
- **Research**: `docs/research/agentgateway-2.2-2026-08.md` (load-bearing claims, cited)

## 1. Purpose & scope

The single translation layer between plume's declarative intent (Agent/Workflow/KG/App CR fields) and agentgateway 2.2 configuration. Everything the platform promises "at the gateway" — budgets, tool access, KG scoping, exposure, candidate isolation — becomes real here, so **apply-time behavior is part of this design, not a detail** (r1 finding 1). **Out of scope**: loop-governance semantics (design 22; mappings reserved), receipt pipeline and spend aggregation (design 04 owns; consumed here).

## 2. Doctrine & charter gates

- **Plane**: slow (engine) — a **library** compiled into agent-operator (rule 3), no pod.
- **Pods added**: 0. **No external rate-limit service, no Redis** — see §3.4: gateway-side limits are deliberately *local approximations*; exact enforcement is the receipt backstop on existing Postgres (r1 finding 3).
- **Stateful deps**: none added. **Primitives**: Resource (CRD) in/out. ✓

## 3. Interfaces

### 3.1 Input: `PolicyIntent` (internal, versioned)

```
PolicyIntent {
  target:      {kind, ns, name}
  revisions:   [{hash, weight, candidate: bool}]
  identity:    {spiffeID | oauthClientID}
  tools:       [{backendRef, toolAllowlist?, requiresApproval}]
  knowledge:   [{endpoint, scope: {entityTypes}}]        // `bundles?` dropped (r1 f7)
  llm:         {providers[], egressAllowlist?}
  budget:      {tokensPerDay, usdPerDay, taskTimeout, maxHops}
  expose:      [{protocol, visibility, auth, consumerBudgets?}]
  captureLevel: metadata|headers|full
  gatewayReplicas: int          // declared, never discovered — see "Field provenance" below (A3)
}
```

**Field provenance (A3).** `PolicyIntent` is an internal type filled by the operator from several sources, not a projection of `AgentSpec`. Naming the source of each field is part of this contract, because a field with no producer compiles to a default nobody chose:

| Field | Source | Available at P1? |
|---|---|---|
| `target`, `revisions` | the Agent CR and the revision state machine (design 02 §3.3) | yes |
| `identity` | design 06 — SPIRE SVID or the `agent-actor` OAuth client | no |
| `tools[].backendRef`, `requiresApproval` | `spec.tools[]` resolved against a Connector facet or `MCPServer` (design 11) | binding only |
| `tools[].toolAllowlist` | the resolved tool server's advertised tool set (design 11 §4) — **not** an `AgentSpec` field | no |
| `knowledge[].endpoint` | the resolved `KnowledgeGraph` CR (design 01/13); `spec.knowledge[]` carries `{name, version}` only | no |
| `knowledge[].scope` | `spec.knowledge[].scope` | yes |
| `llm`, `budget` | `spec.llm`, `spec.budget` | yes |
| `expose[].protocol/visibility/auth` | `spec.expose.a2a` | yes |
| `expose[].consumerBudgets` | design 26 `TenantPolicyIntent` — **not** an `AgentSpec` field | no |
| `captureLevel` | chart value (design 04 §3), cluster-wide, not per-agent | no |
| `gatewayReplicas` | chart value `gateway.replicas` → operator flag `--gateway-replicas`, default `1` | yes |

A field whose source does not exist yet is **absent**, and absence is governed by §3.3.1 — it is never silently defaulted.

**Why `gatewayReplicas` is declared rather than read from the gateway Deployment.** It is the divisor on every emitted rate limit (§3.4), so a wrong value under-enforces by exactly the factor it is wrong by. Discovering it by watching the Deployment would make one HorizontalPodAutoscaler event recompile the rate policy of every Agent in the cluster — an unbounded fan-out this design does not size. Declaring it keeps compilation pure and the blast radius zero. The cost is that the declaration can drift from reality, which §5 makes loud rather than silent.

### 3.2 Output & naming

Deterministic resource set via SSA with ownerRefs: `HTTPRoute`s (revision weights, candidate SVID-bound header route, expose routes), `AgentgatewayBackend`s (tool/KG/LLM), `AgentgatewayPolicy` × N — **one concern per policy** (`-auth`, `-ratelimit`, `-toolfilter`, `-guard`, `-transform`), disjoint fields, compiler-side conflict check.

**Why one-concern-per-policy** (r1 finding 2, honest version): agentgateway defines attach-point precedence with same-level merge semantics whose overlap behavior on identical fields is not crisply documented; splitting concerns avoids same-level/same-field ambiguity *by construction*, and independently yields reviewable diffs. A reproducing test for same-level/same-field overlap ships with implementation (research note, open item R1); if overlap proves benign the rule stays for diffability alone.

**Naming** (r1 f9): `<name>-<concern>[-<rev>]` with a total-length rule — if >63 chars, truncate `<name>` and append an 8-hex hash of the untruncated name; deterministic, collision-checked, golden-file fixture at max length.

### 3.3 Apply protocol — fail-closed (r1 finding 1, BLOCKER)

Order is part of the contract:

1. **Backends** first.
2. **Policies** next — then **verify acceptance**: poll each policy's status until `Accepted=True`/programmed (bounded wait). A policy that fails webhook or reports not-accepted ⇒ **stop**: dependent routes are not created (or existing ones zero-weighted), condition `PolicyApplyIncomplete` with the resource + reason.
3. **Routes / weight changes last** — traffic only ever flows through fully-policied paths. Candidate isolation policies are verified before the candidate header route exists.
4. **Removal reversed**: routes detach (or zero-weight) before their policies/backends are deleted.

New failure rows in §5 cover applied-but-not-accepted and partial-apply.

### 3.3.1 Required concerns — a route is never created without its guarantees (A1)

§3.3 orders the concerns that **exist**. It says nothing about a concern that was never emitted, and that gap is the second half of the same hole ADR-0020 exists to close: a policy that fails acceptance stops the route, but a policy that was never compiled has nothing to fail, and the route is created onto an open door. §4's totality rule does not cover this either — "every field maps or errors" governs fields that are *present* in the `PolicyIntent`, not required concerns whose input is absent.

**The rule.** Every route class names the concerns that must be present and `Accepted=True` before that route may be created. A mandatory concern whose input is absent is a **compile error** (`PolicyCompileFailed`, naming the missing input), never an omitted policy.

| Route class | Mandatory concerns | Notes |
|---|---|---|
| A2A serving route (`expose.a2a`) | `-auth`, plus `-ratelimit` when `budget` sets any limit | `auth: none` is an explicit opt-out — see below |
| Candidate header route | `-auth` — always, no opt-out | admitted set is one SVID (design 02 §3.3 step 3); an unauthenticated candidate route is a bypass of the whole gate |
| Tool (MCP) route | `-auth`, `-toolfilter` | a tool route without its filter grants the server's entire tool surface |
| KG (kgp) route | `-auth`, `-transform` (scope injection) | the provider enforces scope from a header the gateway must actually inject (§3.4) |
| LLM egress route | `-auth`, plus `-ratelimit` when `budget` sets any limit, plus `-transform` (credential scrub) | |
| Card discovery route | none | `/.well-known/agent-card.json` is public by contract (ADR-0019) — stated so its emptiness reads as a decision, not an oversight |

**Explicit opt-outs are emitted, not absent.** `expose.a2a.auth: none` drops the `-auth` requirement, but the resource set still carries an explicit no-auth marker for that route. A reviewer diffing two resource sets must be able to tell "this route is deliberately unauthenticated" from "this route lost its auth policy", and two absences look identical.

**The table is compulsory, not defaulted.** Every route class this compiler emits — including the ones reserved in §3.4 for designs 10, 11, 17, 22, 23, 25 and 26 — appears in exactly one row before it may be emitted, and a property test asserts that the set of route-class constants and the set of table rows are equal. Adding a route class fails the build until someone classifies it. This is design 02 A12's rule applied to the other side of the seam, and for the same reason: an unclassified route reaching production ungoverned is silent, and silence is the failure mode.

### 3.4 Concern mapping

| Intent | Gateway mechanism (OSS only) | Notes |
|---|---|---|
| Routing | HTTPRoute + Backend | per-revision weights (design 02) |
| AuthN | JWT policy (IdP issuer) + SPIFFE mTLS | native order auth → rate-limit → guards (research note §evaluation-order) |
| `budget.tokensPerDay` | **local token-bucket rate limit**, rate = `budget/86400 ÷ gateway_replicas` | **approximation tier**: continuous refill ≠ calendar window, and local counters under-enforce ≤ replicas× on skew. Exact tier = receipt backstop (below). No external RLS / Redis — doctrine rule 2 (r1 f3) |
| `budget.usdPerDay` | token-equivalent local limit computed at the **max price across the agent's allowed models** (conservative: gateway cuts early, never late) (r1 f4) | pricing table §3.5; **model matching no pattern ⇒ compile error** |
| **Exact budget tier (both tokens & usd)** | not a gateway mechanism: **design 04's spend aggregation** (per-agent daily spend, 00:00 UTC windows, materialized where receipts land) is read by the operator each reconcile → overrun ⇒ `BudgetExhausted` condition + weight-0 via the rollout machinery | matches design 02's approved "windows reset 00:00 UTC; remaining in status" — status is fed from the aggregation, not gateway counters. Deltas recorded in design 02 §11 (r1 f3, f5) |
| `taskTimeout` | route timeout | |
| `maxHops` / cycles | lineage-header transform emitted here; semantics owned by design 22 | reserved |
| Tool allowlist | MCP tool-filter policy (`Mcp-Name` match — header-level, body-free) | research note §tool-filter |
| **KG scope** | **gateway-injected, provider-enforced** (r1 finding 6): compiler emits a request-transform policy injecting the `X-Plume-KG-Scope: {entityTypes}` header on **all** kgp routes (trust = the gateway's SVID on the provider connection — no separate header signature; design 13 r1 f4); the *provider* enforces it across all four fact-bearing tools (`search`, `neighbors`, `get_context_bundle`, `cite`) and answers out-of-scope with `KG_SCOPE_DENIED`. The gateway does not parse MCP bodies (consistent with design 01 §3.1); trust consequence: providers are scope-enforcing, and **kgp conformance gains a scope-enforcement battery** — recorded as amendments in design 01 §11 | |
| `requiresApproval` | route to approval interceptor | design 22 |
| Guards / egress | prompt-guard policies; LLM egress allowlist as Backend restriction | ADR-0014; guards consume budget after rate-limit (native order) — documented |
| Expose visibility | listener class cluster / org / public (+ OAuth clients, consumer budgets) | |
| Candidate isolation | header route matched only with gate-controller SVID | design 02 review f2; protected by §3.3 ordering |
| On-behalf-of exchange | `-exchange` policy: `oauthTokenExchange` (subject = user JWT, actorToken = agent-actor client, `audiences` = compiled backend set, cache ≤ token TTL) — **OSS-verified** | design 06 §3.3 (r1 f1) |
| Credential scrub | `-transform` policy stripping the mandatory header denylist **before export** | design 04 §6 (R2-b) |
| Card discovery route | `/.well-known/agent-card.json` routed to the **agent container** (SoT = code, ADR-0019) | design 05 review f1 |
| Webhook ingress (Connector events) | rate-limit + size-cap policy on the receiver route; HMAC verification stays receiver-side (gateway HMAC capability unverified — research note) | design 11 r1 f5 |
| Scoped kgp-admin grant | per-connector route to `write_batch` on one staging version (reader workers) | designs 11 f2 / 01 A2 |
| Eval temporary grant | candidate-route admitted-set add/remove at eval Job launch/end (per-run eval SVID) | design 16 r1 f1 |
| Replay-mock route | replay-principal backends → in-cluster mock Service, fail-closed | design 17 r1 f3/f4 |
| Model fallback | existing LLM Backend row re-compiled under controller-set `llmFallbackActive` (design 20) | |
| App projections | workflow-POST (idempotency forwarded), chat-SSE (A2A resubscribe), KG-read — OIDC + exchange applied | design 23 r1 f2 |
| Approval voucher check | single-use pass voucher CEL on approval-gated routes; gateway forwards the approved retry | design 22 r1 f4 |
| Generative model serving | **`InferencePool`** (GAIE) emitted for LLM pools + HTTPRoute to it — agentgateway OSS inference routing; llm-d schedules below the seam | design 25 A1 |
| Model serving | LLM Backend registration for `expose.llmBackend` + **virtual-model weighted shifting**; shadow candidate route admitted-set = the eval run principal only (launch→end) | design 25 r1 f2/f5 |
| Tenant quotas | `TenantPolicyIntent` (tenant-scoped target): partition listener set + quota policies; approximation tier per ADR-0020, exact tier = 04 A2 rollup | design 26 r1 f1 |
| Interior telemetry | OTLP Backend + per-agent route to the tap's forward-only listener (rate-limited — telemetry is traffic) | design 10 r1 f1; agents are default-deny |
| Receipts | OTLP tracing config (`frontendPolicies`), capture level → attribute verbosity | design 04 |

### 3.5 Pricing table and the usd→token computation (A4)

`ConfigMap plume-model-pricing` (chart-shipped, versioned). Used for usd→token compilation and receipt cost annotation.

**Shape.** One row per model, keyed `<provider>/<model>`, carrying two prices in **USD per 1,000,000 tokens** — the unit vendors publish, chosen so no price is written as `0.000003` and rounded into nothing:

```yaml
data:
  updated: "2026-08-19T00:00:00Z"        # RFC 3339; drives PricingStale
  models: |
    anthropic/claude-opus-5:   {input: "15.00",  output: "75.00"}
    anthropic/claude-sonnet-5: {input: "3.00",   output: "15.00"}
    anthropic/*:               {input: "15.00",  output: "75.00"}   # optional per-provider fallback
```

**Grammar.** Prices and `spec.budget.usdPerDay` are decimal strings matching `^[0-9]+(\.[0-9]{1,6})?$` — at most six fractional digits, no exponent, no currency symbol, no `resource.Quantity` suffixes. The grammar is CEL on the CRD for `usdPerDay`, so a malformed budget is rejected at `kubectl apply` with a message naming the fix; it is validated at load for the ConfigMap. Six digits is chosen because all arithmetic below is then exact in integer **micro-USD**, and golden files pin exact integers rather than whatever a float happened to produce.

**Resolution**, in order: exact `<provider>/<model>` → `<provider>/*` → **compile error**. There is at most one wildcard row per provider (validated at load), so the order is total and no tie-break exists. A model that resolves to nothing is `PolicyCompileFailed` naming `spec.llm.providers[i]` — never a default price, because a guessed price is a guessed budget.

**`PricingStale` means the table is old, not that a row is missing.** It is set when `updated` is more than 30 days ago, and carried on every Agent CR with a usd budget (r1 f10). A *missing row* is the compile error above and is not a stale-pricing condition. Design 02 §5's row said otherwise and is corrected by A15 — one condition cannot mean two things without meaning neither.

**The computation.** Let `U` be `usdPerDay` and `P` the **conservative price**: the maximum, across every model the agent is allowed to use, of that model's `max(input, output)`. Maximum of both directions because the emitted limiter counts total tokens without distinguishing them, and maximum across models because §3.4's stated property is that the gateway cuts early, never late — the real traffic mix can only be cheaper.

With `U`µ and `P`µ as integer micro-USD:

```
tokensPerDay(usd) = floor(Uµ × 1_000_000 / Pµ)
rate              = floor(tokensPerDay / (86_400 × gatewayReplicas))    # tokens/second, the bucket's refill
```

Two edges the arithmetic forces, both previously unstated:

- **Both budget fields may be set.** `tokensPerDay` and `usdPerDay` each derive a rate; the compiler emits **one** `-ratelimit` policy at the **minimum** of the two. Emitting two policies would put the same field in two policies and break §3.2's disjointness by construction.
- **A rate that floors to zero is a compile error.** `usdPerDay: "0.50"` against a model priced at `75.00`/M tokens with two gateway replicas floors to 0 tokens/second — a bucket that denies everything. Denial is fail-closed and therefore "safe", but an agent that answers nothing because of a rounding step is rule 8's loud-and-wrong: the condition would name a budget when the cause is arithmetic. `PolicyCompileFailed` names the floor and the smallest budget that clears it.

**RBAC**: the ConfigMap is operator-writable only by default; a tampered table can only delay the gateway tier — the receipt backstop bounds the damage (noted in §6).

## 4. Behavior

`Compile(intents) → ResourceSet` is pure, deterministic (golden files), and **total**: every field maps or errors (`PolicyCompileFailed` with field path) — including the multi-model pricing rule and unmatched patterns (r1 f4). `Apply(ResourceSet)` follows §3.3. `Diff` drives SSA.

## 5. Failure modes & degraded states

| Failure | Behavior |
|---|---|
| Unmappable field / unresolvable model / rate flooring to zero | `PolicyCompileFailed` naming the field path, nothing applied (§3.5) |
| Mandatory concern for a route class has no input (A1) | `PolicyCompileFailed` naming the missing input; the route is not emitted — not emitted *without* its policy (§3.3.1) |
| Policy webhook-rejected or `Accepted=False` after apply | §3.3 stop: dependent routes withheld/zero-weighted; `PolicyApplyIncomplete` |
| Partial apply (crash mid-sequence) | Re-entrant: next reconcile resumes ordering; routes were last, so no fail-open window existed |
| **Gateway CRDs absent, nothing ever applied** (cold start, A2) | `GatewayIncompatible` with reason `CRDsAbsent`. **Nothing stands, because nothing was ever applied** — the row below is the regression branch and does not cover this. Outside the `local` profile this also withholds `Ready` (design 02 A15): an agent whose budget, authn and tool-filter were never compiled is not serving under the guarantees `Ready` asserts. In the `local` profile it is recorded and does not withhold `Ready`, mirroring `GatesBypassed=DevProfile` (design 02 §3.3) |
| Gateway CRDs present but version-skewed, config previously applied | `GatewayIncompatible` with reason `VersionSkew`; last-applied stands |
| Gateway Deployment replicas exceed `--gateway-replicas` (A3) | Every emitted rate limit under-enforces by the ratio. The operator observes the Deployment read-only and sets `BudgetEnforcementDegraded` naming both numbers. It does **not** recompile — see §3.1 for why discovery is not the input |
| Policy mutated out-of-band | SSA ownership conflict → re-assert + event |
| Spend aggregation unavailable (design 04 down) | Gateway approximation tier still enforcing; `BudgetEnforcementDegraded` condition — exact tier gap visible, never silent |

## 6. Security

Emitted policies are the only capability-change path (admission denies non-operator writes on plume-labeled gateway resources). Fail-closed apply ordering (§3.3) removes the unauthenticated/unbudgeted windows. Scope header is added by the gateway *after* authn and cannot be supplied by clients (transform strips inbound occurrences). Pricing ConfigMap RBAC + backstop bound (§3.5).

## 7. Observability

Compile duration, resource-set size, diff churn, conflict rejections, **policy-acceptance wait time**, `PolicyApplyIncomplete`/`BudgetEnforcementDegraded` counts. Events on source CRs list touched resources.

## 8. Testing

Golden files (incl. max-length names, multi-provider budgets). Property tests: emitted policies per target are field-disjoint, and every route class appears in §3.3.1's table. e2e (k3d): budget 429 + receipt; tool filter; candidate route rejects non-SVID; public expose requires OAuth; **plus (r1 f11)**: synthetic-receipt backstop drive → `BudgetExhausted` + weight-0; `PricingStale` surfacing; deliberate bad policy → routes withheld + `PolicyApplyIncomplete`. Implementation ships the overlap reproducing test (research item R1).

### 8.1 What ships at P1, and what does not (A2)

The battery above needs agentgateway, SPIRE, design 04's receipt pipeline and a KG provider. At P1 the chart installs the agent-operator and nothing else (`charts/plume/Chart.yaml`), so most of it cannot run. Stating the split is the point: an unstated deferral becomes a skipped test, and a skipped test reads as a passing one.

| Layer | Ships with this design | Why it can |
|---|---|---|
| unit | `Compile` in full — golden files, name truncation at 63 chars, the §3.5 arithmetic incl. both edge rules, field-disjointness, route-class classification | `Compile` is pure; it needs no cluster |
| envtest | `Apply` ordering, SSA ownership and re-assert, the acceptance **poll**, the withhold-on-not-accepted path, ordered removal | agentgateway's **CRDs alone** install into envtest without a gateway running. There is no controller to write policy status, so the test writes it — which is the honest shape here, because what is under test is the operator's *reaction* to a status, not the gateway's production of one |
| e2e | nothing | needs a data plane |

**Deferred, and named so it is not mistaken for done**: everything traffic-level (429, tool filter, SVID rejection, OAuth on public expose), the synthetic-receipt backstop drive, and R1's same-level/same-field overlap test — all of which land with the agentgateway subchart (design 07). **Until then design 03 has no e2e coverage at all.** The one-concern-per-policy rule stands on diffability alone until R1 runs, exactly as §3.2 and D2 already say.

## 9. Decisions for async review

- **D1 — OSS-only** (unchanged; now cited in the research note).
- **D2 — One concern per policy**, justified by same-level ambiguity avoidance + diffability; overwrite claim downgraded to open research item R1 with a reproducing test (r1 f2).
- **D3 — Two-tier budgets for tokens *and* usd**: gateway = conservative local approximation (÷ replicas, max-price), receipts (design 04) = exact tier with the approved 00:00 UTC windows; the tier split is user-facing documented semantics (r1 f3/f4/f5).
- **D4 — KG scope is gateway-injected, provider-enforced** across all four fact-bearing tools; body parsing stays out of the gateway (r1 f6).

## 10. Resulting ADRs

ADR-0020 after re-critique PASS (determinism, one-concern rule + R1, fail-closed apply, two-tier budgets, scope injection). ADR-0020 carries an erratum (2026-08-25) correcting its "signed header" wording — see A5 below.

## 11. Amendments (provenance — the body above is authoritative)

Folded into §§3–8 rather than left as patches, following the integration pass design 02's re-critique asked for: an implementer should read one spec, not a body plus a list of corrections.

- **A1 (2026-08-25) — required concerns; a route is never created without its guarantees.** §3.3 ordered the concerns that exist and §4 promised totality over fields that are *present*; neither covered a mandatory concern whose input is absent, which emits no policy, has nothing to fail acceptance, and lets §3.3 create the route onto an open door. ADR-0020's headline property — "no fail-open windows" — did not follow from §3.3 alone. New §3.3.1 gives the per-route-class mandatory sets, makes a missing input a compile error rather than an omitted policy, requires explicit opt-outs (`auth: none`) to be *emitted* so a diff can tell a decision from a loss, and makes the classification table compulsory the way design 02 A12 made field classification compulsory — and for the same reason.
- **A2 (2026-08-25) — the cold-start branch, and the P1 slice.** §5's "Gateway CRDs absent / version skew ⇒ last-applied stands" described only a regression from a working state. On a cluster where the gateway was never installed nothing stands, and that is the only state a P1 cluster is ever in. §5 now splits the two branches; the cold-start branch withholds `Ready` outside the `local` profile (design 02 A15 carries the operator half) and is recorded without withholding inside it, mirroring `GatesBypassed=DevProfile`. New §8.1 states what ships at P1 and what defers with the agentgateway subchart, because §8's battery cannot run and an unstated deferral becomes a skipped test.
- **A3 (2026-08-25) — `gatewayReplicas` gets a source, and every other `PolicyIntent` field gets one too.** §3.1 declared `gatewayReplicas` a compile input, ADR-0020 restated it, and design 02 §3.6 said the operator normalizes it — but no CRD field, chart value or flag produced it, and it is the divisor on every rate limit. It is now a chart value → operator flag, declared rather than discovered, with the reasoning (discovery makes one HPA event recompile the fleet). §3.1 also gains the provenance table for all fourteen fields, because four of them (`tools[].toolAllowlist`, `expose[].consumerBudgets`, `captureLevel`, `knowledge[].endpoint`) have no `AgentSpec` producer and would otherwise be defaulted by accident. Declaration can drift from reality, so §5 makes the skew loud via `BudgetEnforcementDegraded`.
- **A4 (2026-08-25) — the usd→token computation made computable.** "Max price across the agent's allowed models" named no unit and no direction: a model has separate input and output prices, `usdPerDay` is a string with no stated grammar, and "pattern" was undefined with no tie-break — yet §4 claims determinism and §8 pins golden files. §3.5 now fixes the unit (USD per 1M tokens), the grammar (≤6 fractional digits, CEL-enforced on the CRD, exact in integer micro-USD), the resolution order (exact → `provider/*` → compile error, at most one wildcard per provider so no tie-break exists), and the conservative price (max over models of max(input, output)). Two edges the arithmetic forced and nothing had stated: both budget fields set ⇒ one policy at the **minimum** of the derived rates, since two would break §3.2's disjointness; and a rate flooring to zero ⇒ compile error, since a bucket that denies everything is fail-closed but names the wrong cause.
- **A5 (2026-08-25) — the KG scope header is not signed, and two documents still said it was.** §3.4 and design 13 §3.3 state that trust derives from the gateway's verified SVID on the mutually-authenticated provider connection and that **no header signature exists or is needed**; design 13 records that this design's wording was aligned to it. ADR-0020 decision (5) and design 01 A1 were never corrected and still read "signed header" — an implementer starting from the governing ADR would build a signing key and distribution path nothing else expects. Corrected as an erratum on ADR-0020 and as design 01 A5; this design's body was already right and is unchanged.
