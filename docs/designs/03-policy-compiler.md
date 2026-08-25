# Design 03: Policy compiler (PolicyIntent → agentgateway 2.2 resources)

- **Status**: **approved** — critique PASS at r2 (reviews/03-review.md; residuals R2-a/R2-b folded in) · ADR-0020. **Amendments A1–A6 (2026-08-25), folded into §§3–8. Critique returned REVISE (4 blocker, 8 major, 7 minor); all findings addressed and the amendments revised. Awaiting re-critique — they are not yet critique-passed** (§11)
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
  llm:         {providers[], egressAllowlist?, fallback?: {provider, model}, fallbackActive: bool}
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
| `llm.providers`, `llm.egressAllowlist`, `budget` | `spec.llm`, `spec.budget` | yes |
| `llm.fallback` | `spec.llm.fallback` (design 20's remediation target) | yes |
| `llm.fallbackActive` | `status.llmFallbackActive`, controller-set by design 20 §3 — **status, not spec**, so it mints no revision by design | yes |
| `expose[].protocol/visibility/auth` | `spec.expose.a2a` | yes |
| `expose[].consumerBudgets` | design 26 `TenantPolicyIntent` — **not** an `AgentSpec` field | no |
| `captureLevel` | chart value (design 04 §3), cluster-wide, not per-agent | no |
| `gatewayReplicas` | chart value `gateway.replicas` → operator flag `--gateway-replicas`, default `1` | yes |

A field whose source does not exist yet is **absent**, and absence is governed by §3.3.1 — it is never silently defaulted.

**Absent input and unbuilt producer are different things, and conflating them makes P1 uncompilable.** Read literally, the two rules above plus §3.3.1 say: `identity` is unavailable at P1 → the candidate route's `-auth` is mandatory with no opt-out → every Agent fails to compile, in the phase being implemented now. That is not the intent. The rule is:

- A route class **whose entire input set is unavailable because the producing design is not installed** is **not emitted at all**. No route, no policy, no error — there is nothing to govern and nothing serving.
- A route class that **is** emitted, with one mandatory concern's input absent, is the compile error of §3.3.1. This is the dangerous case: a route exists and a guarantee does not.

At P1 that makes exactly one class emittable — the A2A serving route, and only when `identity` resolves. Tool, KG and LLM egress routes have no producer until designs 11, 01/13 and 06 land, so the compiler emits nothing for them, and `plume doctor` reports the tier gap rather than a per-agent condition. **`llm.egressAllowlist` is the exception worth naming**: it is available at P1 and it is an ADR-0014 compliance control, so it compiles as soon as an LLM egress route does.

**`llm.egressAllowlist` mints no revision today, and must** (Codex review BLOCKER 1, `reviews/02-codex-review.md`). Design 02 A12's table classifies `llm.providers` and `llm.fallback` as behaviour surface and never names `egressAllowlist`; the projection drops it and a direct differential mutation (ledger M5) confirmed two specs differing only in that field hash identically. Widening an agent's egress allowlist changes what it can reach — which is what the behaviour surface *means* — so it is behaviour, and today it is neither classified nor projected. A12's `TestEveryFieldIsClassified` did not catch it because it reflects over `AgentSpec` while the field lives one level down in `LLMSpec`: classification has to be **leaf-level**, not top-level. Recorded here because this design consumes the field; the projection and the classification rule are design 02's to amend (A16, not yet drafted).

**Why `gatewayReplicas` is declared rather than read from the gateway Deployment.** It is the divisor on every emitted rate limit (§3.4), so a wrong value under-enforces by exactly the factor it is wrong by. Discovering it by watching the Deployment would make one HorizontalPodAutoscaler event recompile the rate policy of every Agent in the cluster — an unbounded fan-out this design does not size. Declaring it keeps compilation pure and keeps a gateway scale event from touching any Agent's resource set.

**This reverses a review resolution, and says so.** `reviews/03-review.md` R2-a required both that `gatewayReplicas` be declared *and* that "a gateway scale change triggers recompile of all rate-limit policies (else the ÷replicas approximation silently drifts)". A3 keeps the first half and reverses the second: there is no recompile. The drift R2-a worried about is real and is not eliminated — it is made **visible** instead of corrected, via the `BudgetEnforcementDegraded=ReplicaSkew` row in §5. That is a deliberate trade of automatic correctness for a bounded blast radius, and a human should weigh it rather than find it.

The claim is *not* that the blast radius is zero: §5 has the operator observing the gateway Deployment read-only, which is a cluster-wide watch. What is bounded is that the watch never rewrites policy — it only sets a condition.

### 3.2 Output & naming

Deterministic resource set via SSA with ownerRefs: `HTTPRoute`s (revision weights, candidate SVID-bound header route, expose routes), `AgentgatewayBackend`s (tool/KG/LLM), `AgentgatewayPolicy` × N — **one concern per policy** (`-auth`, `-ratelimit`, `-toolfilter`, `-guard`, `-transform`), disjoint fields, compiler-side conflict check.

**Why one-concern-per-policy** (r1 finding 2, honest version): agentgateway defines attach-point precedence with same-level merge semantics whose overlap behavior on identical fields is not crisply documented; splitting concerns avoids same-level/same-field ambiguity *by construction*, and independently yields reviewable diffs. A reproducing test for same-level/same-field overlap ships with implementation (research note, open item R1); if overlap proves benign the rule stays for diffability alone.

**Naming** (r1 f9): `<name>-<concern>[-<rev>]` with a total-length rule — if >63 chars, truncate `<name>` and append an 8-hex hash of the untruncated name; deterministic, collision-checked, golden-file fixture at max length.

### 3.3 Apply protocol — fail-closed (r1 finding 1, BLOCKER)

Order is part of the contract:

1. **Backends** first.
2. **Policies** next — then **verify acceptance**: poll each policy's status until `Accepted=True`/programmed (**bounded: 30s per resource-set apply, not per policy**, after which the remaining policies are treated as not-accepted and their routes withheld; the poll runs off the reconcile worker so a stuck gateway cannot stall the queue). A policy that fails webhook or reports not-accepted ⇒ **stop**: dependent routes are not created (or existing ones zero-weighted), condition `PolicyApplyIncomplete` with the resource + reason.
3. **Routes / weight changes last** — traffic only ever flows through fully-policied paths. Candidate isolation policies are verified before the candidate header route exists.
4. **Removal reversed**: routes detach (or zero-weight) before their policies/backends are deleted.

New failure rows in §5 cover applied-but-not-accepted and partial-apply.

### 3.3.1 Required concerns — a route is never created without its guarantees (A1)

§3.3 orders the concerns that **exist**. It says nothing about a concern that was never emitted, and that gap is the second half of the same hole ADR-0020 exists to close: a policy that fails acceptance stops the route, but a policy that was never compiled has nothing to fail, and the route is created onto an open door. §4's totality rule does not cover this either — "every field maps or errors" governs fields that are *present* in the `PolicyIntent`, not required concerns whose input is absent.

**The unit is one Agent.** `Compile` takes one `PolicyIntent` and returns one `ResourceSet`; one Agent's failure never touches another's. Design 02 §4 reconciles per-CR, and anything wider would let one malformed Agent stop the fleet.

**The rule.** Every route class names the concerns that must be present and `Accepted=True` before that route may be created. A mandatory concern whose input is absent is a **compile error** (`PolicyCompileFailed`, naming the missing input), never an omitted policy.

**The failure is route-scoped, not set-scoped.** An unresolvable tool withholds that tool's route and anything depending on it; the serving route and the card route still compile and apply. The alternative — one bad input voids the whole `ResourceSet` — trades a fail-open for a fail-stuck, where a typo in one tool name takes an agent off the air entirely. §5's rows say this consistently: `PolicyCompileFailed` means *the affected routes* are not emitted, never "nothing applied".

| Route class | Mandatory concerns | Notes |
|---|---|---|
| A2A serving route (`expose.a2a`) | `-auth`, plus `-ratelimit` when `budget` sets any limit | `auth: none` is an explicit opt-out — see below |
| Candidate header route | `-auth` — always, no opt-out | admitted set is one SVID (design 02 §3.3 step 3); an unauthenticated candidate route is a bypass of the whole gate |
| Tool (MCP) route | `-auth`, `-toolfilter` | a tool route without its filter grants the server's entire tool surface |
| KG (kgp) route | `-auth`, `-transform` (scope injection) | the provider enforces scope from a header the gateway must actually inject (§3.4) |
| LLM egress route | `-auth`, plus `-ratelimit` when `budget` sets any limit, plus `-transform` (credential scrub) | `llm.egressAllowlist` compiles to the Backend restriction, not to a policy |
| Reserved — telemetry tap (10), webhook ingress and kgp-admin (11), replay-mock (17), approval interceptor (22), app projections (23), InferencePool and shadow candidate (25), tenant partition (26) | **unclassified — must not be emitted** | Each is named in §3.4 and none has a mandatory set yet. Listing them as explicitly unclassified is the difference between a deferral and an omission: the registry below refuses to emit an unclassified class, so a future design must add its row before it can ship a route |
| Card discovery route | inherits the A2A serving route's `-auth`; `visibility: public` is the only case that drops it | Architecture §05 (Exposure) puts the card on the org/public A2A endpoint, so its reachability follows `expose.a2a.visibility`. **ADR-0019 does not make this route public** — it fixes the card's *source of truth* as the container, which is a different claim; an earlier draft cited it for this and was wrong. An agent at `visibility: cluster`, or with no `expose` block at all (the default), would otherwise disclose its name, version and skill inventory to any unauthenticated caller |

**Explicit opt-outs are emitted, not absent.** `expose.a2a.auth: none` drops the `-auth` requirement, but the resource set still carries an explicit no-auth marker for that route. A reviewer diffing two resource sets must be able to tell "this route is deliberately unauthenticated" from "this route lost its auth policy", and two absences look identical. The marker is a **label on the route**, not a policy: an empty `AgentgatewayPolicy` would be subject to §3.3's acceptance gate and could be webhook-rejected, which would make `auth: none` unserveable — the opt-out must not be able to fail.

**The table is compulsory, and the enforcement has a real anchor.** An earlier draft promised "a property test asserts that the set of route-class constants equals the set of table rows" — which is not mechanizable, because a markdown table is not a machine-readable artifact. In practice it degrades into two Go slices in one file that stay green when you delete from both, which is exactly the shape AGENTS.md rule 1 says is this repo's default failure. Design 02 A12's `TestEveryFieldIsClassified` works only because it reflects over a struct the compiler cannot avoid changing; route classes have no such struct.

So the anchor is built rather than assumed. Routes are emitted only through `emit(class RouteClass, …)`, which consults a **registry** mapping every `RouteClass` to its mandatory set; a class absent from the registry cannot be emitted. The registry — not this table — is authoritative, and a golden test **renders the registry to markdown and diffs it against the table above**, so the table is machine-checked and drifting from it fails the build. Deleting a registry entry fails the golden diff; deleting from both fails the exhaustive switch over `RouteClass` in Go.

**Compliance profiles extend the mandatory sets; they do not appear here.** `-guard` is absent from every row above, which is right for P1 and wrong for ADR-0014: the `hipaa` profile makes PHI redaction mandatory at the gateway, and "compliance is a profile you enable, not an integration you build" has to stay literally true. The credential-scrub `-transform` on the LLM row is a different concern and does not cover it. The table therefore has no profile dimension by design — a profile contributes additional mandatory concerns per route class through the same registry (design 27), and the omission is a scoping decision rather than a hole.

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

**Grammar.** Prices and `spec.budget.usdPerDay` are decimal strings matching `^[0-9]{1,7}(\.[0-9]{1,6})?$` — at most seven integer digits and six fractional, no exponent, no currency symbol, no `resource.Quantity` suffixes. Six fractional digits make all arithmetic below exact in integer **micro-USD**, so golden files pin exact integers rather than whatever a float happened to produce. **Seven integer digits is not cosmetic**: the computation multiplies by 10¹², so an unbounded integer part overflows int64 above ~9,223,372 and Go wraps it *silently* — an unbounded grammar would state an exactness the type cannot hold. The `usdPerDay` half of this grammar belongs on the CRD as CEL and is landed by design 02 A15, since §3.1 there is the authoritative schema; the ConfigMap half is validated at load. **Neither is implemented yet** — `config/crd/plume.dev_agents.yaml` currently has a bare `type: string`.

**Resolution**, in order: exact `<provider>/<model>` → `<provider>/*` → **compile error**. There is at most one wildcard row per provider (validated at load), so the order is total and no tie-break exists. A model that resolves to nothing is `PolicyCompileFailed` naming `spec.llm.providers[i]` — never a default price, because a guessed price is a guessed budget.

**`PricingStale` means the table is old, not that a row is missing.** A *missing row* is the compile error above. Design 02 §5's row said otherwise and is corrected by A15 — one condition cannot mean two things without meaning neither.

**What staleness can honestly claim.** The table is chart-shipped with `updated` baked in and **nothing refreshes it automatically**, so `updated > 30d` becomes permanently true on every install of a chart older than a month — an alarm that is always on for everyone, which trains operators to ignore the one signal that matters when prices actually move. Three narrowings make it mean something:

- It is raised only for agents whose usd budget resolves to an **externally-priced** model. `internal/<model>` rows are upserted by the model-operator at serve time (design 25 §4, D2b) and are never stale by this measure.
- Refreshing is a **documented human action** — `helm upgrade`, or editing the operator-writable ConfigMap directly. The condition's message names it. Nothing auto-refreshes external vendor prices, and this design does not pretend otherwise.
- The condition is **advisory**: it never withholds traffic or fails a compile. A stale table can only make the gateway tier's approximation drift, and the receipt backstop bounds that (§6).

**The computation.** Let `U` be `usdPerDay` and `P` the **conservative price**: the maximum, across every model the agent is allowed to use, of that model's `max(input, output)`. Maximum of both directions because the emitted limiter counts total tokens without distinguishing them, and maximum across models because §3.4's stated property is that the gateway cuts early, never late — the real traffic mix can only be cheaper.

`P` is taken over `providers[] ∪ {fallback}`, **not** `providers[]` alone. Design 20 activates `llm.fallback` by setting status, which mints no revision and fires no eval — deliberately, so an incident does not wait for a gate. If the fallback were outside `P`, activation would leave a limiter sized for a cheaper model and the gateway would cut **late**, breaking the one property §3.4 and D3 both promise. Pricing the fallback in up front also moves its failure to declaration time: **an unpriced `llm.fallback` is a compile error when the Agent is written**, never a `PolicyCompileFailed` raised mid-incident during the remediation A12 exempted from gating.

With `U`µ and `P`µ as integer micro-USD:

```
tokensPerDay(usd) = floor(Uµ × 1_000_000 / Pµ)
rate              = floor(tokensPerDay / (86_400 × gatewayReplicas))    # tokens/second, the bucket's refill
capacity          = rate × 3_600                                       # burst: one hour's refill
```

**Capacity is part of the emitted policy, not an afterthought.** §3.4 emits a token *bucket*, and a bucket has both a refill rate and a burst capacity; an earlier draft gave only the rate, which meant no golden file could be written at all. One hour's refill absorbs the bursts real agent traffic produces — a multi-tool task spends its tokens in seconds, not evenly across the day — while keeping §3.4's promise meaningful: at `capacity = tokensPerDay` the gateway would be a daily ceiling rather than a rate, and an agent could spend the whole budget in the first minute and then be dark until 00:00 UTC.

Two edges the arithmetic forces, both previously unstated:

- **Both budget fields may be set.** `tokensPerDay` and `usdPerDay` each derive a rate; the compiler emits **one** `-ratelimit` policy at the **minimum** of the two. Emitting two policies would put the same field in two policies and break §3.2's disjointness by construction.
- **A rate that floors to zero is a compile error.** `usdPerDay: "0.50"` against a model priced at `75.00`/M tokens with two gateway replicas floors to 0 tokens/second — a bucket that denies everything. Denial is fail-closed and therefore "safe", but an agent that answers nothing because of a rounding step is rule 8's loud-and-wrong: the condition would name a budget when the cause is arithmetic. `PolicyCompileFailed` names the floor and the smallest budget that clears it.

**RBAC**: the ConfigMap is operator-writable only by default; a tampered table can only delay the gateway tier — the receipt backstop bounds the damage (noted in §6).

## 4. Behavior

`Compile(intent) → ResourceSet` — one Agent in, one resource set out (§3.3.1) — is pure, deterministic (golden files), and **total**: every field maps or errors (`PolicyCompileFailed` with field path) — including the multi-model pricing rule and unmatched patterns (r1 f4). `Apply(ResourceSet)` follows §3.3. `Diff` drives SSA.

## 5. Failure modes & degraded states

| Failure | Behavior |
|---|---|
| Unmappable field / unresolvable model / unpriced `llm.fallback` / rate flooring to zero | `PolicyCompileFailed` naming the field path; the **affected routes** are not emitted and everything independent still applies (§3.3.1) |
| Mandatory concern for an **emitted** route class has no input (A1) | `PolicyCompileFailed` naming the missing input; that route and its dependents are withheld, the rest of the set applies (§3.3.1) |
| Route class whose **producing design is not installed** | Not emitted, no condition — there is nothing serving and nothing to govern (§3.1). `plume doctor` reports the tier gap; a per-agent condition here would fire on every Agent in every core-tier cluster |
| Policy webhook-rejected or `Accepted=False` after apply | §3.3 stop: dependent routes withheld/zero-weighted; `PolicyApplyIncomplete` |
| Partial apply (crash mid-sequence) | Re-entrant: next reconcile resumes ordering; routes were last, so no fail-open window existed |
| **Gateway CRDs absent, nothing ever applied** (cold start, A2) | `GatewayIncompatible` with reason `CRDsAbsent`, whose message **names the CRD set it checked** — Gateway API `HTTPRoute` *and* `AgentgatewayPolicy`/`AgentgatewayBackend`, which install independently (a cluster running Istio or Cilium has the first and not the others). **Nothing stands, because nothing was ever applied** — the row below is the regression branch and does not cover this. Outside the `local` profile this also withholds `Ready` and reports `phase: Pending` (design 02 A15, which carries the reasoning and the two things it owes). In the `local` profile the condition is raised and does not withhold |
| Gateway CRDs present but version-skewed, config previously applied | `GatewayIncompatible` with reason `VersionSkew`; last-applied stands |
| Gateway Deployment replicas **differ from** `--gateway-replicas` in either direction (A3) | `BudgetEnforcementDegraded` with reason **`ReplicaSkew`**, message naming both numbers. Scale *up* under-enforces by the ratio; scale *down* silently halves every agent's effective budget — both are drift and an earlier draft caught only the first. The operator observes `Deployment/<release>-agentgateway` in the release namespace, read-only, and does **not** recompile (§3.1) |
| Gateway Deployment not observable (absent, or installed out-of-band) | `BudgetEnforcementDegraded` with reason **`ReplicaUnverified`** — the declared divisor cannot be checked, so the `÷ replicas` bound is unverified rather than wrong. This is the P1 state and the state on any cluster where agentgateway is installed outside the chart; with the flag defaulting to `1`, an unobserved gateway running two replicas would otherwise under-enforce in silence |
| Policy mutated out-of-band | SSA ownership conflict → re-assert + event |
| Spend aggregation unavailable (design 04 down) | Gateway approximation tier still enforcing; `BudgetEnforcementDegraded` with reason **`AggregateUnavailable`** — distinct from `ReplicaSkew`/`ReplicaUnverified` above, because the remediations share nothing: one is a restart, the others a flag. A bare condition with three causes and one message is rule 8's loud-and-wrong |

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
| envtest | `Apply` ordering, SSA ownership and re-assert, the acceptance **poll**, the withhold-on-not-accepted path, ordered removal, and the §3.3's wait bound | What this layer uniquely proves is that **the status field path the operator polls exists in the real published CRD schema** — the only defence against a suite that passes against an invented status shape. (That CRDs install without their controller is true of any CRD and proves nothing.) There is no controller to write policy status, so the test writes it: what is under test is the operator's *reaction* to a status, not the gateway's production of one |
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

Folded into §§3–8 rather than left as patches, following the integration pass design 02's re-critique asked for: an implementer should read one spec, not a body plus a list of corrections. A1–A5 were raised at the start of implementation, **returned REVISE by independent critique** (4 blocker, 8 major, 7 minor), and revised as A6 records. They are recorded at their revised content.

- **A1 (2026-08-25, revised) — required concerns; a route is never created without its guarantees.** §3.3 ordered the concerns that exist and §4 promised totality over fields that are *present*; neither covered a mandatory concern whose input is absent, which emits no policy, has nothing to fail acceptance, and lets §3.3 create the route onto an open door. ADR-0020's headline property — "no fail-open windows" — did not follow from §3.3 alone. New §3.3.1 gives the per-route-class mandatory sets, scopes both the compile unit (one Agent) and the failure (one route and its dependents, never the whole set), requires explicit opt-outs to be emitted as route labels so a diff tells a decision from a loss, and anchors the classification in an emitter registry that a golden test renders back into the table.
- **A2 (2026-08-25, revised) — the cold-start branch, and the P1 slice.** §5's "Gateway CRDs absent ⇒ last-applied stands" described only a regression from a working state; on a cluster where the gateway was never installed nothing stands, and that is the only state a P1 cluster is ever in. §5 now splits the branches and names the CRD set it checked. The cold-start branch withholds `Ready` outside the `local` profile — design 02 A15 carries the operator half and the reasoning, including why A13 is being narrowed rather than contradicted. New §8.1 states what ships at P1 and what defers with the agentgateway subchart.
- **A3 (2026-08-25, revised) — `gatewayReplicas` gets a source, and every `PolicyIntent` field gets one too.** It was declared a compile input by this design, restated by ADR-0020 and normalized by design 02 §3.6, yet no CRD field, chart value or flag produced it — and it divides every rate limit. Now a chart value → operator flag, declared rather than discovered, because discovery would make one HPA event recompile the fleet. **This reverses review 03 R2-a's recompile requirement and §3.1 says so.** §3.1 also gains a provenance row for every `PolicyIntent` field, because several have no `AgentSpec` producer and would otherwise be defaulted by accident; §5 makes divisor drift loud in both directions and distinguishes it from an unobservable gateway.
- **A4 (2026-08-25, revised) — the usd→token computation made computable.** "Max price across the agent's allowed models" named no unit and no direction, `usdPerDay` had no grammar, and "pattern" was undefined with no tie-break — yet §4 claims determinism and §8 pins golden files. §3.5 now fixes the unit, the grammar (bounded at seven integer digits so the ×10¹² step cannot silently overflow int64), the resolution order, the conservative price over `providers[] ∪ {fallback}`, the **bucket capacity** the first draft omitted entirely, and the rule that both budget fields compile to one policy at the minimum rate. `PricingStale` is narrowed to what it can honestly claim, since nothing refreshes the table and an always-on alarm teaches operators to ignore it.
- **A5 (2026-08-25) — the KG scope header is not signed, and two documents still said it was.** §3.4 and design 13 §3.3 state that trust derives from the gateway's verified SVID on the mutually-authenticated provider connection and that no header signature exists or is needed. ADR-0020 decision (5) and design 01 A1 were never corrected; both now carry a pointer to their correction (ADR-0020 erratum, design 01 **A8**). This design's body was already right and is unchanged.
- **A6 (2026-08-25) — `PolicyIntent` carries `llm.fallback`, and `llm.egressAllowlist` is ungated.** Two findings from independent review, one per model family. **`fallback`**: design 20 §3 already states the operator "folds it into `PolicyIntent.llm` and recompiles", but the type in §3.1 had no slot for it — so the field design 20 depends on did not exist in the contract it depends on. Added, with `fallbackActive` beside it, and §3.5 prices the fallback into the conservative maximum so activation never makes the gateway cut late. **`egressAllowlist`** (Codex review BLOCKER 1, ledger M5): this design consumes it, design 02 A12 classifies it in neither column, and a differential mutation shows two specs differing only in that field hash identically — so widening an agent's LLM egress reaches production with no gate, on the control ADR-0014 rests on. A12's reflection test missed it because it walks `AgentSpec` while the field sits one level down in `LLMSpec`; classification has to be leaf-level. Recorded here because the compiler is the consumer; **the projection and the classification rule are design 02's to amend, and that amendment is not yet drafted.**
