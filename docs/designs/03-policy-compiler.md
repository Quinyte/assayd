# Design 03: Policy compiler (PolicyIntent → agentgateway 2.2 resources)

- **Status**: **approved** — critique PASS at r2 (reviews/03-review.md; residuals R2-a/R2-b folded in) · ADR-0020
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
  gatewayReplicas: int          // R2-a: declared compile input; scale change ⇒ recompile of rate policies
}
```

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
| **KG scope** | **gateway-injected, provider-enforced** (r1 finding 6): compiler emits a request-transform policy injecting a signed `X-Plume-KG-Scope: {entityTypes}` header on **all** kgp routes; the *provider* enforces it across all four fact-bearing tools (`search`, `neighbors`, `get_context_bundle`, `cite`) and answers out-of-scope with `KG_SCOPE_DENIED`. The gateway does not parse MCP bodies (consistent with design 01 §3.1); trust consequence: providers are scope-enforcing, and **kgp conformance gains a scope-enforcement battery** — recorded as amendments in design 01 §11 | |
| `requiresApproval` | route to approval interceptor | design 22 |
| Guards / egress | prompt-guard policies; LLM egress allowlist as Backend restriction | ADR-0014; guards consume budget after rate-limit (native order) — documented |
| Expose visibility | listener class cluster / org / public (+ OAuth clients, consumer budgets) | |
| Candidate isolation | header route matched only with gate-controller SVID | design 02 review f2; protected by §3.3 ordering |
| Credential scrub | `-transform` policy stripping the mandatory header denylist **before export** | design 04 §6 (R2-b) |
| Card discovery route | `/.well-known/agent-card.json` routed to the **agent container** (SoT = code, ADR-0019) | design 05 review f1 |
| Receipts | OTLP tracing config (`frontendPolicies`), capture level → attribute verbosity | design 04 |

### 3.5 Pricing table

`ConfigMap plume-model-pricing` (chart-shipped, versioned). Used for usd→token compilation and receipt cost annotation. `PricingStale` (age > 30d) is carried as a condition **on every Agent CR with a usd budget** (r1 f10). **RBAC**: the ConfigMap is operator-writable only by default; a tampered table can only delay the gateway tier — the receipt backstop bounds the damage (noted in §6).

## 4. Behavior

`Compile(intents) → ResourceSet` is pure, deterministic (golden files), and **total**: every field maps or errors (`PolicyCompileFailed` with field path) — including the multi-model pricing rule and unmatched patterns (r1 f4). `Apply(ResourceSet)` follows §3.3. `Diff` drives SSA.

## 5. Failure modes & degraded states

| Failure | Behavior |
|---|---|
| Unmappable field / unmatched model pattern / missing pricing with usd budget | `PolicyCompileFailed`, nothing applied |
| Policy webhook-rejected or `Accepted=False` after apply | §3.3 stop: dependent routes withheld/zero-weighted; `PolicyApplyIncomplete` |
| Partial apply (crash mid-sequence) | Re-entrant: next reconcile resumes ordering; routes were last, so no fail-open window existed |
| Gateway CRDs absent / version skew | `GatewayIncompatible`; last-applied stands |
| Policy mutated out-of-band | SSA ownership conflict → re-assert + event |
| Spend aggregation unavailable (design 04 down) | Gateway approximation tier still enforcing; `BudgetEnforcementDegraded` condition — exact tier gap visible, never silent |

## 6. Security

Emitted policies are the only capability-change path (admission denies non-operator writes on plume-labeled gateway resources). Fail-closed apply ordering (§3.3) removes the unauthenticated/unbudgeted windows. Scope header is added by the gateway *after* authn and cannot be supplied by clients (transform strips inbound occurrences). Pricing ConfigMap RBAC + backstop bound (§3.5).

## 7. Observability

Compile duration, resource-set size, diff churn, conflict rejections, **policy-acceptance wait time**, `PolicyApplyIncomplete`/`BudgetEnforcementDegraded` counts. Events on source CRs list touched resources.

## 8. Testing

Golden files (incl. max-length names, multi-provider budgets). Property test: emitted policies per target are field-disjoint. e2e (k3d): budget 429 + receipt; tool filter; candidate route rejects non-SVID; public expose requires OAuth; **plus (r1 f11)**: synthetic-receipt backstop drive → `BudgetExhausted` + weight-0; `PricingStale` surfacing; deliberate bad policy → routes withheld + `PolicyApplyIncomplete`. Implementation ships the overlap reproducing test (research item R1).

## 9. Decisions for async review

- **D1 — OSS-only** (unchanged; now cited in the research note).
- **D2 — One concern per policy**, justified by same-level ambiguity avoidance + diffability; overwrite claim downgraded to open research item R1 with a reproducing test (r1 f2).
- **D3 — Two-tier budgets for tokens *and* usd**: gateway = conservative local approximation (÷ replicas, max-price), receipts (design 04) = exact tier with the approved 00:00 UTC windows; the tier split is user-facing documented semantics (r1 f3/f4/f5).
- **D4 — KG scope is gateway-injected, provider-enforced** across all four fact-bearing tools; body parsing stays out of the gateway (r1 f6).

## 10. Resulting ADRs

ADR-0020 after re-critique PASS (determinism, one-concern rule + R1, fail-closed apply, two-tier budgets, scope injection).
