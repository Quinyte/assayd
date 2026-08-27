# Design 03: Policy compiler (PolicyIntent → agentgateway v1.4.1 resources)

- **Status**: **approved** — critique PASS at r2 (reviews/03-review.md; residuals R2-a/R2-b folded in) · **ADR-0028** (supersedes ADR-0020). **Amendments A1–A10 (2026-08-25/26), folded into §§3–8. Four independent critiques: REVISE 4/8/7, 4/12/6, 5/10/10, then 0/5/6 — "the substance has converged". Every finding is addressed and `reviews/03-amendments-review.md` carries all four. No pass has returned PASS — not yet critique-passed** (§11)
- **Phase**: P1 · **Size**: M · **Date**: 2026-08-20
- **ADRs**: 0003, 0014, 0019 · interfaces: designs 02 (caller; amendments recorded there §11), 04 (spend aggregation owner), 22 (loop governance)
- **Research**: `docs/research/agentgateway-v1.4.1-2026-08.md` (primary-source verified 2026-08-27; re-verify by 2026-10-15). The former note is **superseded and must not be cited** — it named a release that does not exist

## 1. Purpose & scope

The single translation layer between plume's declarative intent (Agent/Workflow/KG/App CR fields) and agentgateway v1.4.1 configuration. Everything the platform promises "at the gateway" — budgets, tool access, KG scoping, exposure, candidate isolation — becomes real here, so **apply-time behavior is part of this design, not a detail** (r1 finding 1). **Out of scope**: loop-governance semantics (design 22; mappings reserved), receipt pipeline and spend aggregation (design 04 owns; consumed here).

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

**Whether the compiler runs at all is declared, not discovered.** The chart is to carry `gateway.enabled` as the agentgateway subchart's `dependencies[].condition` in `Chart.yaml` — **neither exists yet**: `Chart.yaml` has no `dependencies:` block and `values.yaml` no `gateway` key, and these chart deltas are owed to design 07 §3. Stated in the future tense because nothing enforces it today rather than a free-standing boolean — so the switch and the install cannot diverge by typo, and `true` + CRDs absent can only mean a genuinely broken install. Architecture §17 puts agentgateway at core tier, so it is `true` once design 07 ships the subchart; **until then the chart has no such dependency and `values.yaml` must ship `false`**, which is what makes P1 the declared-off row rather than the broken-install row. This removes the discovery ambiguity that made "the gateway is missing" mean two incompatible things:

| `gateway.enabled` | Gateway CRDs | Meaning | Behaviour |
|---|---|---|---|
| `true` | present | normal | compile, apply, `Ready` requires accepted routes |
| `true` | **absent** | **a broken install** — someone declared the gateway and it is not there | `GatewayIncompatible=CRDsAbsent`, **withhold `Ready`**, page (design 10). Fail-closed, because the operator's intent was governed traffic |
| `false` | either | **a deliberately ungoverned tier** — what P1 ships and what `local` uses | compile nothing; every Agent carries **`GovernanceSkipped=GatewayDisabled`**; **`Ready` is not withheld**. A `true → false` **transition** first runs §5's reverse-order teardown — the table is total over states, not over transitions |

Declared-but-missing is an incident. Declared-off is a documented tier. **They are different condition types, not two reasons on one type** — design 10's alerts and design 08's `deploy` stream both key on the condition type, so a shared type would page and abort on the documented normal state of every P1 cluster. `GatesSkipped` and design 24's `ReBACAvailable` are the same shape for the same reason. Only the incident withholds `Ready`, so a stock `helm install` never produces a fleet that is permanently un-`Ready` — and an operator who asked for a gateway and did not get one is never told everything is fine. `GovernanceSkipped` is loud rather than silent: the condition is on every Agent, the chart's `NOTES.txt` is to state that agents are ungoverned (**owed to design 07 §3 — `charts/plume/templates/` has no `NOTES.txt` today**), and `plume doctor` reports it (design 08 §8).

**At P1, `gateway.enabled` is `false` — shipped so, not defaulted so — and the compiler does not run.** When it is turned on, exactly one route class is emittable — the A2A serving route, and only once design 06 supplies `identity`; until then its mandatory `-auth` input is absent and §3.3.1 makes that a `PolicyCompileFailed`, which is correct and is *not* the "not emitted" branch. Tool, KG and LLM egress routes have no producer at all until designs 11, 01/13 and 06 land, so they are the not-emitted branch. **`llm.egressAllowlist` is therefore not compiled at P1 either** — it rides the LLM egress route — but it is classified behaviour in design 02 A16 today, because gating it is the revision projection's job and does not wait on this compiler.

**`llm.egressAllowlist` mints no revision today, and must** (Codex review BLOCKER 1, `reviews/02-codex-review.md`). Design 02 A12's table classifies `llm.providers` and `llm.fallback` as behaviour surface and never names `egressAllowlist`; the projection drops it and a direct differential mutation (ledger M5) confirmed two specs differing only in that field hash identically. Widening an agent's egress allowlist changes what it can reach — which is what the behaviour surface *means* — so it is behaviour, and today it is neither classified nor projected. A12's `TestEveryFieldIsClassified` did not catch it because it reflects over `AgentSpec` while the field lives one level down in `LLMSpec`: classification has to be **leaf-level**, not top-level. Recorded here because this design consumes the field; the projection and the classification rule are **design 02 A16**, now drafted.

**Why `gatewayReplicas` is declared rather than read from the gateway Deployment.** It is the divisor on every emitted rate limit (§3.4), so a wrong value under-enforces by exactly the factor it is wrong by. Discovering it by watching the Deployment would make one HorizontalPodAutoscaler event recompile the rate policy of every Agent in the cluster — an unbounded fan-out this design does not size. Declaring it keeps compilation pure and keeps a gateway scale event from touching any Agent's resource set.

**This reverses a review resolution, and says so.** `reviews/03-review.md` R2-a required both that `gatewayReplicas` be declared *and* that "a gateway scale change triggers recompile of all rate-limit policies (else the ÷replicas approximation silently drifts)". A3 keeps the first half and reverses the second: there is no recompile. The drift R2-a worried about is real and is not eliminated — it is made **visible** instead of corrected, via the `BudgetEnforcementDegraded=ReplicaSkew` row in §5. That is a deliberate trade of automatic correctness for a bounded blast radius, and a human should weigh it rather than find it.

The claim is *not* that the blast radius is zero: §5 has the operator observing the gateway Deployment read-only, which is a cluster-wide watch. What is bounded is that the watch never rewrites policy — it only sets a condition.

### 3.2 Output & naming

Deterministic resource set via SSA with ownerRefs: `HTTPRoute`s (revision weights, candidate SVID-bound header route, expose routes), `AgentgatewayBackend`s (tool/KG/LLM), `AgentgatewayPolicy` × N — **one concern per policy** (`-auth`, `-ratelimit`, `-toolfilter`, `-guard`, `-transform`), disjoint fields, compiler-side conflict check.

**Why one-concern-per-policy** (r1 finding 2, honest version): agentgateway defines attach-point precedence with same-level merge semantics whose overlap behavior on identical fields is not crisply documented; splitting concerns avoids same-level/same-field ambiguity *by construction*, and independently yields reviewable diffs. **R1 is closed by source, and the answer is worse than "undocumented"**: same-level conflicts iterate a randomly-seeded `HashSet`, so the winning policy varies **per replica and per restart** while both report `Accepted`+`Attached`. One-concern-per-policy is load-bearing, not a diffability preference, and no reproducing test is owed.

**Naming** (r1 f9): `<name>-<concern>[-<rev>]` with a total-length rule — if >63 chars, truncate `<name>` and append an 8-hex hash of the untruncated name; deterministic, collision-checked, golden-file fixture at max length.

### 3.3 Apply protocol — fail-closed (r1 finding 1, BLOCKER)

Order is part of the contract:

1. **Backends** first.
2. **Policies** next — then **verify convergence of every resource a mandatory entry names, Backends included** (A18, A10). §3.3.1's entries range over resources, not only policies, so a Backend applied at step 1 and then rejected by the gateway would otherwise let its route be created — the `hipaa` egress-restricted Backend is exactly that case, and it was silent. **`AgentgatewayBackend` acceptance status is not in the research note** (`agentgateway-2.2-2026-08.md` cites no `Accepted` condition for Backends), so this is a load-bearing claim about a third-party CRD that §8.1's envtest row must verify against the real published schema before it is relied on. Acceptance is polled for: poll each resource's status until it satisfies the **per-kind convergence tuple** of §3.3.2 — never `Accepted=True` alone, which a policy targeting a not-yet-created route reports while emitting nothing (A10) (**bounded: 30s per resource-set apply, not per policy**, after which the remaining resources are treated as not-accepted and their routes withheld). The poll runs **on the reconcile worker**: controller-runtime's one-reconcile-per-object is the only thing serializing this sequence, and moving the wait off it would let reconcile N's outstanding poll apply old routes after reconcile N+1 had installed new isolation policies — review 03 finding 1, restored. The path is re-entrant, so a stuck gateway does not deadlock: withhold the dependent routes, set `PolicyApplyIncomplete`, return, requeue. It does **cost fleet-wide latency** — `agent_controller.go` calls `Complete(r)` with controller-runtime's default of one concurrent reconcile, so a 30s poll serializes across every Agent. This rule therefore requires `MaxConcurrentReconciles` to be set explicitly; claiming the queue is unaffected would be a bound nothing enforces. A **resource** that fails webhook or reports not-accepted ⇒ **stop**: dependent routes are not created (or existing ones zero-weighted), condition `PolicyApplyIncomplete` with the resource + reason.
3. **Routes / weight changes last** — traffic only ever flows through fully-policied paths. Candidate isolation policies are verified before the candidate header route exists.
4. **Removal reversed**: routes detach (or zero-weight) before their policies/backends are deleted.

New failure rows in §5 cover applied-but-not-accepted and partial-apply.

### 3.3.2 What the barrier can and cannot prove (A10)

`Accepted=True/programmed` was not a predicate; the slash hid a missing contract, and the condition it named is **true in exactly the state §3.3 guarantees**. A policy whose target route does not yet exist reports `Accepted=True, reason: Valid, "Policy accepted"` beside `Attached=False`, with `output: []` — no policy emitted at all (upstream fixture `http-route-missing-not-attached.yaml`). Since routes are created last, the old barrier was satisfied by a policy that had produced nothing.

**The convergence tuple**, per kind, all at `observedGeneration == metadata.generation`:

| Kind | Required | Traps this closes |
|---|---|---|
| `AgentgatewayPolicy` | `Accepted=True` with `reason: Valid` **and** `Attached=True`, on a **Gateway**-shaped `ancestorRef` | The success ancestor is the Gateway, **not** the `targetRef`. Failures emit a synthetic `StatusSummary` ancestor — its presence is a negative signal. An unsupported target kind yields **zero ancestors and zero conditions**, so a poller waiting for `Attached=False` waits forever; empty ancestors is a failure, not a pending state |
| `AgentgatewayBackend` | `Accepted=True` at the current generation | This proves **translation only**. The controller sets it even when no gateway resolves and no dataplane resource is emitted |
| `HTTPRoute` | `Accepted=True` **and** `ResolvedRefs=True` | There is no `Programmed` condition to wait for |

**What this proves is control-plane convergence, and not enforcement.** A dataplane NACK is published as a Kubernetes **Warning Event** (`AgentGatewayNackError`), never as a status condition — so every condition above can be satisfied at the current generation while the proxy has rejected the config. The operator therefore **watches that Event** for its emitted resources and raises `PolicyApplyIncomplete` on it. Absent that watch, no part of this design may claim proven enforcement (rule 7). Two-phase publication — create the route inert, converge its policies, then attach — is what the ordering in §3.3 becomes; the alternative is a barrier that passes on an unattached policy.

**Update ordering is part of the contract too**, not only create and remove. A tightening change (`auth: none → oauth`, a narrowed tool filter) must make dependent routes inert *before* the change is applied, converge, then republish. Applying the policy under a live route leaves the old permissive configuration serving for the whole convergence window.

### 3.3.1 Required concerns — a route is never created without its guarantees (A1)

§3.3 orders the concerns that **exist**. It says nothing about a concern that was never emitted, and that gap is the second half of the same hole ADR-0020 exists to close: a policy that fails acceptance stops the route, but a policy that was never compiled has nothing to fail, and the route is created onto an open door. §4's totality rule does not cover this either — "every field maps or errors" governs fields that are *present* in the `PolicyIntent`, not required concerns whose input is absent.

**The unit is one Agent.** `Compile` takes one `PolicyIntent` and returns one `ResourceSet`; one Agent's failure never touches another's. Design 02 §4 reconciles per-CR, and anything wider would let one malformed Agent stop the fleet.

**The rule.** Every route class names the concerns that must be present and `Accepted=True` before that route may be created. A mandatory concern whose input is absent is a **compile error** (`PolicyCompileFailed`, naming the missing input), never an omitted policy.

**The failure is route-scoped, not set-scoped.** An unresolvable tool withholds that tool's route and anything depending on it; the serving route and the card route still compile and apply. The alternative — one bad input voids the whole `ResourceSet` — trades a fail-open for a fail-stuck, where a typo in one tool name takes an agent off the air entirely. §5's rows say this consistently: `PolicyCompileFailed` means *the affected routes* are not emitted, never "nothing applied".

| Route class | Mandatory concerns | Notes |
|---|---|---|
| A2A serving route (`expose.a2a`) | `-auth`, plus `-ratelimit` **when a rate is actually derived** (§3.5) | `auth: none` is an explicit opt-out — see below |
| Candidate header route | `-auth` — always, no opt-out | admitted set is one SVID (design 02 §3.3 step 3); an unauthenticated candidate route is a bypass of the whole gate |
| Tool (MCP) route | `-auth` (as `traffic.jwtAuthentication.mcp`), `-toolfilter` (as `backend.mcp.authorization`, `action: Allow`) | A tool route without its filter grants the server's entire tool surface — and because the filter also prunes `tools/list`, its absence makes every tool *visible* as well as callable (§3.4.2) |
| KG (kgp) route | `-auth`, `-transform` (scope injection) | the provider enforces scope from a header the gateway must actually inject (§3.4) |
| LLM egress route | `-auth`, plus `-ratelimit` **when a rate is actually derived**, plus `-transform` (credential scrub), plus `AgentgatewayBackend(egressEnumerated)` | The Backend entry is **construction, not restriction** (§3.4.1): the predicate holds when the Backend's resolved provider set equals the allowlist, no `dynamicForwardProxy` is present, and every effective host — override included — is in the allowlist |
| Reserved — telemetry tap (10), webhook ingress and kgp-admin (11), replay-mock (17), approval interceptor (22), app projections (23), InferencePool and shadow candidate (25), tenant partition (26) | **unclassified — must not be emitted** | Each is named in §3.4 and none has a mandatory set yet. Listing them as explicitly unclassified is the difference between a deferral and an omission: the registry below refuses to emit an unclassified class, so a future design must add its row before it can ship a route |
| Card discovery route | **only emitted when `expose.a2a` is set.** It then carries the same `-auth` as the serving route, except at `expose.a2a.auth: none`, which drops it for both together | Architecture §05 (Exposure) puts the card on the org/public A2A endpoint, so its reachability follows `expose.a2a.visibility`. **ADR-0019 does not make this route public** — it fixes the card's *source of truth* as the container, which is a different claim; an earlier draft cited it for this and was wrong. An agent at `visibility: cluster` would otherwise disclose its name, version and skill inventory to any unauthenticated caller. **With no `expose` block — ADR-0027's four-line Agent, and the default — there is no external card route at all**: nothing is externally reachable, so there is nothing to authenticate and nothing to leak. An earlier draft said "inherits", which made the default Agent fail to compile a route whose parent did not exist. Note the gate is `auth`, not `visibility`: the two are different enums and `visibility: public, auth: oauth` is reachable and stays authenticated |

**Explicit opt-outs are emitted, not absent.** `expose.a2a.auth: none` drops the `-auth` requirement, but the resource set still carries an explicit no-auth marker for that route. A reviewer diffing two resource sets must be able to tell "this route is deliberately unauthenticated" from "this route lost its auth policy", and two absences look identical. The marker is a **label on the route**, not a policy: an empty `AgentgatewayPolicy` would be subject to §3.3's acceptance gate and could be webhook-rejected, which would make `auth: none` unserveable — the opt-out must not be able to fail.

**The table is compulsory, and the enforcement has a real anchor.** An earlier draft promised "a property test asserts that the set of route-class constants equals the set of table rows" — which is not mechanizable, because a markdown table is not a machine-readable artifact. In practice it degrades into two Go slices in one file that stay green when you delete from both, which is exactly the shape AGENTS.md rule 1 says is this repo's default failure. Design 02 A12's `TestEveryFieldIsClassified` works only because it reflects over a struct the compiler cannot avoid changing; route classes have no such struct.

So the anchor is built rather than assumed. Routes are emitted only through `emit(class RouteClass, …)`, which consults a **registry** mapping every `RouteClass` to its mandatory set; a class absent from the registry cannot be emitted. Because it is the emit gate rather than a checklist, deleting a registry entry disables the route instead of ungating it — the failure direction is closed.

**Mandatory sets range over emitted *resources*, not only policies.** An earlier draft scoped them to `AgentgatewayPolicy` kinds, which cannot express the one control ADR-0014 rests on: `llm.egressAllowlist` compiles to a **Backend**, not a policy, so "the LLM Backend must enumerate only allowed endpoints" (§3.4.1) was outside the stated extension point entirely, and a `hipaa` cluster could emit an LLM egress route with no BAA restriction and nothing in this design would stop it. A mandatory entry therefore names a resource and a predicate over it — `AgentgatewayPolicy(-auth)`, `AgentgatewayBackend(egressEnumerated)` — and both are checked before the route is created.

**What actually pins the sets.** A golden test renders the registry to markdown and diffs it against the table above, so the table is machine-checked. That catches divergence, not wrongness: the dangerous mutation is deleting `-auth` from the tool route's mandatory set *and* editing the row to match, which the golden diff passes. So each entry is pinned **behaviourally** — for every registered class, one test per mandatory entry that removes that entry's input and asserts the route is withheld. That is mutation-shaped and cannot be satisfied by editing a table. An earlier draft claimed "deleting from both fails the exhaustive switch over `RouteClass` in Go": Go has no exhaustive switch checking, this repo runs `go vet` with no `.golangci.yml`, and an exhaustive switch would prove only that every class is *handled*, never that its mandatory set is *right*.

**Compliance profiles extend the mandatory sets; they do not appear here.** `-guard` is absent from every row above, which is right for P1 and wrong for ADR-0014: the `hipaa` profile makes PHI redaction mandatory at the gateway, and "compliance is a profile you enable, not an integration you build" has to stay literally true. The credential-scrub `-transform` on the LLM row is a different concern and does not cover it. The table therefore has no profile dimension by design — a profile contributes additional mandatory **entries** per route class through the same registry (design 27), which is expressible now that entries range over resources: the `hipaa` profile adds `AgentgatewayPolicy(-guard)` to the LLM egress row, on top of the `egressEnumerated` entry the base profile already carries. Admission (design 27 §3) rejects a non-conforming Agent CR, and this registry stops a conforming CR from compiling an unrestricted route; the two are different seams and both are needed.

### 3.4 Concern mapping

| Intent | Gateway mechanism (OSS only) | Notes |
|---|---|---|
| Routing | HTTPRoute + Backend | per-revision weights (design 02) |
| AuthN | JWT policy (IdP issuer) + SPIFFE mTLS | native order auth → rate-limit → guards (research note §evaluation-order) |
| `budget.tokensPerDay` | **local token-bucket rate limit**, `tokens = ⌊tokensPerDay ÷ (24 × gateway_replicas)⌋`, `unit: Hours` (§3.5). **No burst**: `burst` applies to `requests` limits only, so the capacity arithmetic an earlier draft carried is void (A10) | **approximation tier**: continuous refill ≠ calendar window, and local counters under-enforce ≤ replicas× on skew. Exact tier = receipt backstop (below). No external RLS / Redis — doctrine rule 2 (r1 f3) |
| `budget.usdPerDay` | token-equivalent local limit computed at the **max price across the agent's allowed models** (conservative: gateway cuts early, never late) (r1 f4) | pricing table §3.5; **model matching no pattern ⇒ compile error** |
| **Exact budget tier (both tokens & usd)** | not a gateway mechanism: **design 04's spend aggregation** (per-agent daily spend, 00:00 UTC windows, materialized where receipts land) is read by the operator each reconcile → overrun ⇒ `BudgetExhausted` condition + weight-0 via the rollout machinery | matches design 02's approved "windows reset 00:00 UTC; remaining in status" — status is fed from the aggregation, not gateway counters. Deltas recorded in design 02 §11 (r1 f3, f5) |
| `taskTimeout` | route timeout | |
| `maxHops` / cycles | lineage-header transform emitted here; semantics owned by design 22 | reserved |
| Tool allowlist | **`AgentgatewayPolicy.spec.backend.mcp.authorization`** — CEL over `mcp.tool.name`, `action: Allow` (default-deny once any allow rule exists). Filters `tools/list` items *and* rejects `tools/call`, so a denied tool is **invisible**, not merely unreachable (A11) | Header matching on `Mcp-Name` still exists as SEP-2243 but cannot filter a list response |
| **KG scope** | **gateway-injected, provider-enforced** (r1 finding 6): compiler emits a request-transform policy injecting the `X-Plume-KG-Scope: {entityTypes}` header on **all** kgp routes (trust = the gateway's SVID on the provider connection — no separate header signature; design 13 r1 f4); the *provider* enforces it across all four fact-bearing tools (`search`, `neighbors`, `get_context_bundle`, `cite`) and answers out-of-scope with `KG_SCOPE_DENIED`. The gateway does not parse MCP bodies (consistent with design 01 §3.1); trust consequence: providers are scope-enforcing, and **kgp conformance gains a scope-enforcement battery** — recorded as amendments in design 01 §11 | |
| `requiresApproval` | route to approval interceptor | design 22 |
| Guards / egress | prompt-guard policies; **`llm.egressAllowlist` compiles to Backend *construction*, not restriction** (§3.4.1, A11) — the CRD has no egress, host, domain or provider allowlist field anywhere | ADR-0014. Evaluation order relative to rate-limit is a **stated gap** (research §9) and is not asserted here |
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

### 3.4.1 Egress is enumeration, not restriction (A11)

`llm.egressAllowlist` was specified as compiling to "a Backend restriction". **No such field exists** — one grep of the whole CRD API returns a single unrelated hit (the MCP JSON-RPC method allowlist). There is no host, domain, provider or model allowlist on `AgentgatewayBackend`, so nothing can filter a wider set down to an allowed one.

What the schema does expose is a **closed set of destinations by construction**. `AgentgatewayBackendSpec` is `ExactlyOneOf=ai;static;dynamicForwardProxy;mcp;aws;a2a`; the `ai` arm holds one `LLMProvider` or `groups[]` of them (**max 8 groups × 16 providers** — a bound the compiler enforces, and a compile error past it). A Backend reaches the providers it names and nothing else. So the control compiles to: *emit a Backend whose provider set is exactly the resolved allowlist, and ensure the route can reach no other Backend.*

Three consequences the old wording hid:

- **plume owns the provider→endpoint catalog; agentgateway supplies none.** An allowlist entry is meaningless until it resolves to a concrete `LLMProvider` arm (`openai`, `anthropic`, `gemini`, `bedrock`, `vertexai`, `azure`, `azureopenai`, `custom`) plus an effective host. An entry that resolves to nothing is `PolicyCompileFailed` naming it — never dropped, because a dropped entry silently widens nothing but a dropped *deny* silently widens everything.
- **`dynamicForwardProxy` is the anti-control and is forbidden outright.** Its own doc comment: *"this backend type can send requests to arbitrary destinations."* The compiler never emits it and rejects any intent that would require it.
- **The predicate reads the effective host, not the provider name.** `host`/`port` **override managed-provider defaults**, so a Backend naming `openai` is not pinned to OpenAI's endpoint. `egressRestricted` is therefore a predicate over the resolved endpoint set — provider default, or the override where one is set — and a `custom` provider must carry `providerBackend` or `hostOverride` or translation fails anyway.

**This is a design change, not a wording fix, and it is not finished here.** The catalog's contents, its versioning, and how it stays correct as vendors move endpoints are unspecified — and the allowlist entry grammar (host? provider id? model id?) is still undefined, which is Codex BLOCKER 6's substance. What A11 settles is the *mechanism*: enumeration, with a plume-owned resolver and a `dynamicForwardProxy` ban. ADR-0014's "enforced at the gateway" wording needs the same correction and does not yet have it.

### 3.4.2 MCP tool filtering is first-class CEL (A11)

`Mcp-Name` header matching was the mechanism when the superseded note was written. At v1.4.1 there is a real one: **`AgentgatewayPolicy.spec.backend.mcp.authorization`**, a CEL `Authorization` at the MCP layer whose context exposes `mcp.tool.name` and JWT claims together.

It is strictly stronger than header matching in the way that matters: *"List operations, such as `list_tools`, will have each item evaluated. Items that do not meet the rule will be filtered."* A tool the agent may not call is **absent from `tools/list`**, not merely rejected on call — an agent cannot attempt what it cannot see, and header matching could never do that.

Three rules the emission must follow:

- **`action: Allow`, never `Deny`.** Once any allow rule exists the policy is default-deny, which is the shape a tool allowlist needs. Upstream warns that *"`Deny` is not recommended because expression failures fail to deny"* — a CEL error under `Deny` fails **open**, which is the one failure mode this design cannot accept.
- **Auth moves to `traffic.jwtAuthentication.mcp`.** `backend.mcp.authentication` is deprecated in favour of it *"which ensures authentication runs before other policies such as transformation and rate limiting"* — directly the ordering §3.3.1 needs for `-auth` before `-toolfilter`. The two may not appear in the same policy, which one-concern-per-policy already prevents.
- **Targeting constraints**: `backend.mcp` may not target a `Service`, and may not target an `AgentgatewayBackend` `sectionName`. Both are compile-time checks.

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

**Grammar.** Prices and `spec.budget.usdPerDay` are decimal strings matching `^[0-9]{1,6}(\.[0-9]{1,6})?$` — at most **six** integer digits and six fractional, no exponent, no currency symbol, no `resource.Quantity` suffixes. Six fractional digits make the arithmetic exact in integer **micro-USD**, so golden files pin exact integers rather than whatever a float happened to produce.

**Six integer digits, and the number is load-bearing.** The computation multiplies micro-USD by 10⁶, so the largest admissible value must satisfy `U × 10¹² ≤ 2⁶³−1`. At six digits the maximum is `999999.999999` → `999999999999 × 10⁶ = 9.99999999999×10¹⁷`, comfortably inside int64's `9.223×10¹⁸`. An earlier draft derived the true threshold (~9,223,372) correctly and then wrote a *seven*-digit grammar, which admits `9999999` → `9.99999999999×10¹⁸`, which **wraps to a negative** `tokensPerDay` — and the zero-rate guard below tested `rate == 0`, so a negative rate escaped it entirely. A regex cannot express `U ≤ 9223372`; six digits is the largest bound a regex *can* express that is also safe, which is why the schema carries it rather than the code. At $999,999.99 per agent per day the ceiling is far above any real budget.

The `usdPerDay` half belongs on the CRD as CEL and is landed by design 02 A15, since §3.1 there is the authoritative schema; the ConfigMap half is validated at load. **Neither is implemented yet** — `config/crd/plume.dev_agents.yaml` currently has a bare `type: string`.

**Resolution is by literal key, longest-prefix, and the compiler never splits a model string.** `spec.llm.providers[]` entries are opaque strings (`openai/gpt-x`, `azure/openai/gpt-4`); a key is either that string exactly or a literal wildcard ending `/*`. Order: exact → longest matching wildcard → **compile error**. Longest-prefix is total and admits no tie, so the "at most one wildcard per provider" rule an earlier draft relied on is unnecessary and is dropped — `azure/*` and `azure/openai/*` may both exist and the longer wins.

Splitting is avoided deliberately: `internal/revision/revision.go:66` already records the hazard in the other direction, where joining on `/` let `{provider: azure, model: openai/gpt-4}` and `{provider: azure/openai, model: gpt-4}` collide. `llm.fallback` is structured `{provider, model}` and canonicalizes to `provider + "/" + model` for lookup; a golden fixture pins a slash-containing model name in both forms. That `providers[]` is flat while `fallback` is structured is a real schema inconsistency and the right time to fix it is before v1beta1 — recorded as an open item, not resolved here.

A model that resolves to nothing is `PolicyCompileFailed` naming the offending `spec.llm.providers[i]` (or `spec.llm.fallback`) — never a default price, because a guessed price is a guessed budget.

**`PricingStale` means the table is old, not that a row is missing.** A *missing row* is the compile error above. Design 02 §5's row said otherwise and is corrected by A15 — one condition cannot mean two things without meaning neither.

**What staleness can honestly claim.** The table is chart-shipped with `updated` baked in and **nothing refreshes it automatically**, so `updated > 30d` becomes permanently true on every install of a chart older than a month — an alarm that is always on for everyone, which trains operators to ignore the one signal that matters when prices actually move. Three narrowings make it mean something:

- It is raised only for agents whose usd budget resolves to an **externally-priced** model. `internal/<model>` rows are upserted by the model-operator at serve time (design 25 §5) and are never stale by this measure. **`updated` is chart-owned**: the model-operator writes `internal/*` rows and must not touch it. Two writers on one timestamp would otherwise mean an unrelated in-cluster model going live silently clears the only alarm that fires when vendor prices move.
- Refreshing is a **documented human action** — `helm upgrade`, or editing the operator-writable ConfigMap directly. The condition's message names it. Nothing auto-refreshes external vendor prices, and this design does not pretend otherwise.
- The condition is **advisory**: it never withholds traffic or fails a compile. A stale table can only make the gateway tier's approximation drift, and the receipt backstop bounds that (§6).

**The computation.** Let `U` be `usdPerDay` and `P` the **conservative price**: the maximum, across every model the agent is allowed to use, of that model's `max(input, output)`. Maximum of both directions because the emitted limiter counts total tokens without distinguishing them, and maximum across models because §3.4's stated property is that the gateway cuts early, never late — the real traffic mix can only be cheaper.

Two degenerate cases are defined rather than left to panic, because §4 calls `Compile` **total** and integer division does not forgive:

- **`P = 0`.** Design 25 D2b lets the model-operator write an `internal/<model>` row priced at explicit `0` where the org does not charge back. An agent whose allowed models are all free gives `P = 0`, and `Uµ × 10⁶ / 0` panics in the reconcile path. A zero price is not an error and not a missing row: a free model imposes no monetary ceiling, so the usd-derived rate is unbounded and the emitted limit is the `tokensPerDay`-derived one, or none. **`-ratelimit` is mandatory on a *derived rate*, not on the presence of a budget field** — keying it on the field would make a usd budget over an all-free model set withhold the route, falsifying design 25 §5's guarantee that in-cluster models always compile.
- **Empty model set.** A `usdPerDay` with no `providers[]` and no `fallback` has no maximum to take. `PolicyCompileFailed` names `spec.budget.usdPerDay` — there is no `providers[i]` to name — and says a usd budget requires at least one priced model.

`P` is taken over `providers[] ∪ {fallback}`, **not** `providers[]` alone. Design 20 activates `llm.fallback` by setting status, which mints no revision and fires no eval — deliberately, so an incident does not wait for a gate. If the fallback were outside `P`, activation would leave a limiter sized for a cheaper model and the gateway would cut **late**, breaking the one property §3.4 and D3 both promise. Pricing the fallback in up front also moves its failure to declaration time: **an unpriced `llm.fallback` is a compile error when the Agent is written**, never a `PolicyCompileFailed` raised mid-incident during the remediation A12 exempted from gating.

The same argument applies to reachability, and an earlier draft made the pricing check and missed it. When `llm.egressAllowlist` is set, **`llm.fallback` must be one of the providers the emitted Backend enumerates** (§3.4.1) — under construction-not-restriction the failure is total rather than partial: a fallback outside the enumerated set is not merely blocked, it is unreachable, because the Backend has no route to it at all. Checked at compile time on the same footing as pricing, failing with `PolicyCompileFailed` naming `spec.llm.fallback`. **`internal/*` resolves inside any allowlist by construction** — an in-cluster Service is not an egress endpoint, and design 25 §5 calls self-hosted models "the allowlist's easiest member"; the check applies only to externally-resolved providers, or design 02 §3.1's own `fallback: {provider: internal, …}` example would fail it. Otherwise design 20 activates a fallback the gateway's Backend restriction blocks: `ModelDrifted` reports the remediation applied while the agent serves nothing, which is rule 8's loud-and-wrong at the worst possible moment. Design 20 §6 has a row for "fallback provider also drifted" and none for "fallback was never reachable".

With `U`µ and `P`µ as integer micro-USD:

```
tokensPerDay(usd) = floor(Uµ × 1_000_000 / Pµ)
tokens            = floor(tokensPerDay / (24 × gatewayReplicas))       # emitted as unit: Hours
```

**The emitted shape is fixed by the real CRD, and it is narrower than earlier drafts assumed** (A10, ADR-0028). `LocalRateLimit` carries `requests`, `tokens`, `unit` and `burst`; `unit` is **required** and is one of `Seconds|Minutes|Hours`, so **a per-day budget is not directly expressible** — hourly is the coarsest available window and is what plume emits. `ExactlyOneOf=requests;tokens` means one policy cannot carry both a request limit and a token limit. **`burst` applies only to `requests`** ("This setting only works with `requests`, not with `token` rate limits"), so the token bucket has no burst allowance and the `rate × 3600` capacity of earlier drafts is void — as is the 90 000 divisor that existed to pay for it.

`requests`, `tokens` and `burst` are all **`*int32`**. A computed value outside `1 … 2 147 483 647` is a compile error naming the field; that ceiling caps an expressible daily budget at ~5.15×10¹⁰ tokens per replica. At tag v1.4.1 `burst` carries **no `Minimum`**, so a negative burst is schema-valid and plume validates `burst >= 0` itself rather than relying on the API. `gatewayReplicas < 1` is likewise rejected at the flag, not discovered by dividing by zero.

**"Cuts early, never late" is withdrawn, and nothing replaces it** (ADR-0028). The property was never achievable: `LocalRateLimit.tokens`' own doc comment says *"token counts are not known until the request completes. As a result, token-based rate limits will apply to future requests only."* The request that crosses the budget **completes and is returned**; only the next one gets a 429. Pre-dispatch estimation exists in agentgateway (`tokenize: true`) but is **unreachable from Kubernetes** — absent from the CRD API, absent from every published k8s doc page, and hardcoded `false` on the xDS path. Earlier drafts of this section proved an arithmetic invariant that was internally exact and externally irrelevant, because the mechanism it constrained does not enforce at request time.

**What is published instead is an overshoot bound.** The gateway tier bounds *sustained* rate. Worst-case excess over a window is `gatewayReplicas × maxOutputTokens(model)` — one in-flight crossing request per replica, each able to return up to the model's maximum completion — and that figure is stated wherever a budget is promised, in the CLI and in `status.budget`. It is a bound, not a ceiling, and calling it conservative would be the loud-and-wrong of rule 8.

**Capacity is part of the emitted policy, not an afterthought.** §3.4 emits a token *bucket*, and a bucket has both a refill rate and a burst capacity; an earlier draft gave only the rate, which meant no golden file could be written at all. One hour's refill absorbs the bursts real agent traffic produces — a multi-tool task spends its tokens in seconds, not evenly across the day — while keeping §3.4's promise meaningful: at `capacity = tokensPerDay` the gateway would be a daily ceiling rather than a rate, and an agent could spend the whole budget in the first minute and then be dark until 00:00 UTC.

Two edges the arithmetic forces, both previously unstated:

- **Both budget fields may be set.** `tokensPerDay` and `usdPerDay` each derive a rate; the compiler emits **one** `-ratelimit` policy at the **minimum** of the two. Emitting two policies would put the same field in two policies and break §3.2's disjointness by construction.
- **A rate below 1 token/second is a compile error.** `usdPerDay: "0.50"` against a model priced at `75.00`/M tokens with two gateway replicas floors below 1 token/second — a bucket that denies everything. The test is `rate < 1`, not `rate == 0`: with the grammar bounded at six digits a negative rate is now unreachable, but a guard that only catches exact zero was how the overflow above stayed invisible, and the cheap predicate is the total one. Denial is fail-closed and therefore "safe", but an agent that answers nothing because of a rounding step is rule 8's loud-and-wrong: the condition would name a budget when the cause is arithmetic. `PolicyCompileFailed` names the floor and the smallest budget that clears it.

**RBAC**: the ConfigMap is operator-writable only by default; a tampered table can only delay the gateway tier — the receipt backstop bounds the damage (noted in §6).

## 4. Behavior

`Compile(intent) → ResourceSet` — one Agent in, one resource set out (§3.3.1) — is pure, deterministic (golden files), and **total**: every field maps or errors (`PolicyCompileFailed` with field path) — including the multi-model pricing rule and unmatched patterns (r1 f4). `Apply(ResourceSet)` follows §3.3. `Diff` drives SSA.

## 5. Failure modes & degraded states

| Failure | Behavior |
|---|---|
| Unmappable field / unresolvable model / unpriced `llm.fallback` / rate flooring to zero | `PolicyCompileFailed` naming the field path; the **affected routes** are not emitted and everything independent still applies (§3.3.1) |
| Mandatory concern for an **emitted** route class has no input (A1) | `PolicyCompileFailed` naming the missing input; that route and its dependents are withheld, the rest of the set applies (§3.3.1) |
| Route class whose **producing design is not installed** | Not emitted. No *per-class* condition — one would fire on every Agent in every core-tier cluster and say nothing actionable. The tier gap is reported once at operator level and by `plume doctor` (design 08 §8) |
| Policy webhook-rejected or `Accepted=False` after apply | §3.3 stop: dependent routes withheld/zero-weighted; `PolicyApplyIncomplete` |
| Partial apply (crash mid-sequence) | Re-entrant: next reconcile resumes ordering; routes were last, so no fail-open window existed |
| **Gateway CRDs absent, `gateway.enabled: true`** — whether or not anything was applied before (A18) | `gateway.enabled: true` and the CRDs are missing, so this is **a broken install, not a tier choice** — the operator asked for governed traffic and did not get it. `GatewayIncompatible=CRDsAbsent`, message **naming the CRD set it checked** (Gateway API `HTTPRoute` *and* `AgentgatewayPolicy`/`AgentgatewayBackend`, which install independently — a cluster running Istio or Cilium has the first and not the others). **Withholds `Ready`**, reports `phase: Pending`, pages. Deleting the CRDs garbage-collects every route, so nothing stands here either — an earlier draft scoped this row to cold start and left "uninstall the subchart while Agents serve" falling through to design 02 §5's *stale-but-serving*, which is rule 8's loud-and-wrong. §3.1's `(enabled × CRDs)` table is total and answers this unconditionally |
| Gateway CRDs present but version-skewed, config previously applied | `GatewayIncompatible=VersionSkew`; last-applied stands |
| `gateway.enabled: false` — the deliberately ungoverned tier (P1, `local`) | The compiler does not run. Every Agent carries **`GovernanceSkipped=GatewayDisabled`** and **`Ready` is not withheld**: nothing was promised, so nothing is broken. A per-Agent condition is right here and a per-*class* one is not (row above): this one is actionable — turn the tier on — and is a distinct type so nothing keyed on errors fires. Loud rather than silent: the per-Agent condition today, plus the `NOTES.txt` and `plume doctor` report owed to design 07 §3 and design 08 §8 |
| `gateway.enabled` flipped `true → false` with routes already applied | The transition first runs §3.3's **reverse-order removal** — routes detached before their policies and backends — and only then sets `GovernanceSkipped`. Without it the condition and `NOTES.txt` would both assert the agents are ungoverned while their routes kept serving |
| Gateway Deployment replicas **differ from** `--gateway-replicas` in either direction (A3) | `BudgetEnforcementDegraded` with reason **`ReplicaSkew`**, message naming both numbers. Scale *up* under-enforces by the ratio; scale *down* silently halves every agent's effective budget — both are drift and an earlier draft caught only the first. The operator observes `Deployment/<release>-agentgateway` in the release namespace, read-only, and does **not** recompile (§3.1) |
| Gateway Deployment not observable (absent, or installed out-of-band) | `BudgetEnforcementDegraded` with reason **`ReplicaUnverified`** — the declared divisor cannot be checked, so the `÷ replicas` bound is unverified rather than wrong. This is the P1 state and the state on any cluster where agentgateway is installed outside the chart; with the flag defaulting to `1`, an unobserved gateway running two replicas would otherwise under-enforce in silence |
| Policy mutated out-of-band | SSA ownership conflict → re-assert + event |
| Spend aggregation unavailable (design 04 down) | Gateway approximation tier still enforcing; `BudgetEnforcementDegraded` with reason **`AggregateUnavailable`** — distinct from `ReplicaSkew`/`ReplicaUnverified` above, because the remediations share nothing: one is a restart, the others a flag. A bare condition with three causes and one message is rule 8's loud-and-wrong |

## 6. Security

Emitted policies are the only capability-change path (admission denies non-operator writes on plume-labeled gateway resources). Fail-closed apply ordering (§3.3) removes the unauthenticated/unbudgeted windows. Scope header is added by the gateway *after* authn and cannot be supplied by clients (transform strips inbound occurrences). Pricing ConfigMap RBAC + backstop bound (§3.5).

## 7. Observability

Compile duration, resource-set size, diff churn, conflict rejections, **policy-acceptance wait time**, `PolicyApplyIncomplete`/`BudgetEnforcementDegraded` counts. Events on source CRs list touched resources.

## 8. Testing

Golden files (incl. max-length names, multi-provider budgets). Property tests: emitted policies per target are field-disjoint; every `RouteClass` has a registry entry; and each entry is pinned behaviourally — remove a mandatory entry's input, assert the route is withheld (§3.3.1). The table is checked by golden render, not by a property test: markdown is not a machine-readable artifact, which is why §3.3.1 replaced that promise. e2e (k3d): budget 429 + receipt; tool filter; candidate route rejects non-SVID; public expose requires OAuth; **plus (r1 f11)**: synthetic-receipt backstop drive → `BudgetExhausted` + weight-0; `PricingStale` surfacing; deliberate bad policy → routes withheld + `PolicyApplyIncomplete`. R1 needs no reproducing test — it is closed by source (§3.2).

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
- **D2 — One concern per policy**, now justified by **nondeterminism in source** rather than by absent documentation: overlapping same-level policies resolve through a randomly-seeded `HashSet`, varying per replica and per restart. R1 is closed (ADR-0028).
- **D3 — Two tiers, neither a ceiling** (ADR-0028, replacing ADR-0020's D3): the gateway bounds sustained rate and cannot stop a crossing request; the receipt tier is exact only at summing its own `usd_est` estimates, priced from the same table the gateway uses, so it is **not exact in USD and not independent of the approximation**. Both facts are user-facing documented semantics, and the published figure is an overshoot bound (§3.5).
- **D4 — KG scope is gateway-injected, provider-enforced** across all four fact-bearing tools; body parsing stays out of the gateway (r1 f6).

## 10. Resulting ADRs

ADR-0020 after re-critique PASS (determinism, one-concern rule + R1, fail-closed apply, two-tier budgets, scope injection). ADR-0020 carries an erratum (2026-08-25) correcting its "signed header" wording — see A5 below.

## 11. Amendments (provenance — the body above is authoritative)

Folded into §§3–8 rather than left as patches, following the integration pass design 02's re-critique asked for: an implementer should read one spec, not a body plus a list of corrections. A1–A5 were raised at the start of implementation, **returned REVISE by independent critique** (4 blocker, 8 major, 7 minor), and revised as A6 records. They are recorded at their revised content.

- **A1 (2026-08-25, revised) — required concerns; a route is never created without its guarantees.** §3.3 ordered the concerns that exist and §4 promised totality over fields that are *present*; neither covered a mandatory concern whose input is absent, which emits no policy, has nothing to fail acceptance, and lets §3.3 create the route onto an open door. ADR-0020's headline property — "no fail-open windows" — did not follow from §3.3 alone. New §3.3.1 gives the per-route-class mandatory sets, scopes both the compile unit (one Agent) and the failure (one route and its dependents, never the whole set), requires explicit opt-outs to be emitted as route labels so a diff tells a decision from a loss, and anchors the classification in an emitter registry that a golden test renders back into the table.
- **A2 (2026-08-25, revised) — the cold-start branch, and the P1 slice.** §5's "Gateway CRDs absent ⇒ last-applied stands" described only a regression from a working state; on a cluster where the gateway was never installed nothing stands, and that is the only state a P1 cluster is ever in. §5 now splits the branches and names the CRD set it checked. The broken-install branch withholds `Ready` at every profile and the declared-off tier withholds nothing — A18 replaced this amendment's original profile-keyed framing; design 02 A15 carries the operator half and why A13 is narrowed rather than contradicted. New §8.1 states what ships at P1 and what defers with the agentgateway subchart.
- **A3 (2026-08-25, revised) — `gatewayReplicas` gets a source, and every `PolicyIntent` field gets one too.** It was declared a compile input by this design, restated by ADR-0020 and normalized by design 02 §3.6, yet no CRD field, chart value or flag produced it — and it divides every rate limit. Now a chart value → operator flag, declared rather than discovered, because discovery would make one HPA event recompile the fleet. **This reverses review 03 R2-a's recompile requirement and §3.1 says so.** §3.1 also gains a provenance row for every `PolicyIntent` field, because several have no `AgentSpec` producer and would otherwise be defaulted by accident; §5 makes divisor drift loud in both directions and distinguishes it from an unobservable gateway.
- **A4 (2026-08-25, revised) — the usd→token computation made computable.** "Max price across the agent's allowed models" named no unit and no direction, `usdPerDay` had no grammar, and "pattern" was undefined with no tie-break — yet §4 claims determinism and §8 pins golden files. §3.5 now fixes the unit, the grammar (bounded at **six** integer digits so the ×10¹² step cannot silently overflow int64 — A4 first wrote seven, which admits 9 999 999 and wraps; corrected by A18), the resolution order, the conservative price over `providers[] ∪ {fallback}`, the **bucket capacity** the first draft omitted entirely, and the rule that both budget fields compile to one policy at the minimum rate. `PricingStale` is narrowed to what it can honestly claim, since nothing refreshes the table and an always-on alarm teaches operators to ignore it.
- **A5 (2026-08-25) — the KG scope header is not signed, and two documents still said it was.** §3.4 and design 13 §3.3 state that trust derives from the gateway's verified SVID on the mutually-authenticated provider connection and that no header signature exists or is needed. ADR-0020 decision (5) and design 01 A1 were never corrected; both now carry a pointer to their correction (ADR-0020 erratum, design 01 **A8**). This design's body was already right and is unchanged.
- **A6 (2026-08-25) — `PolicyIntent` carries `llm.fallback`, and `llm.egressAllowlist` is ungated.** Two findings from independent review, one per model family. **`fallback`**: design 20 §3 already states the operator "folds it into `PolicyIntent.llm` and recompiles", but the type in §3.1 had no slot for it — so the field design 20 depends on did not exist in the contract it depends on. Added, with `fallbackActive` beside it, and §3.5 prices the fallback into the conservative maximum so activation never makes the gateway cut late. **`egressAllowlist`** (Codex review BLOCKER 1, ledger M5): this design consumes it, design 02 A12 classifies it in neither column, and a differential mutation shows two specs differing only in that field hash identically — so widening an agent's LLM egress reaches production with no gate, on the control ADR-0014 rests on. A12's reflection test missed it because it walks `AgentSpec` while the field sits one level down in `LLMSpec`; classification has to be leaf-level. Recorded here because the compiler is the consumer; **the projection and the classification rule are design 02's to amend, and that amendment is not yet drafted.**
- **A7 (2026-08-26) — the second critique's findings, and one decision taken on enterprise grounds.** Round 2 returned REVISE (4 blocker, 12 major, 6 minor) against A1–A6 as revised; `reviews/03-amendments-review.md` carries both rounds. Two blockers were the same failure class as round 1, which is the finding that mattered: a bound stated but not enforced, and a test shape that goes green without working.

  **`gateway.enabled` replaces profile-keyed behaviour (BLOCKER 3).** §5 gave opposite treatments to one state — one row refused a per-agent condition because it "would fire on every Agent in every core-tier cluster", and the row below did exactly that for the same state. With `values.yaml` defaulting to `profile: prod` and the P1 chart shipping no gateway, A15's rule would have made **no Agent ever reach `Ready` on a stock install**. The enterprise resolution is to stop discovering intent and declare it: a gateway that was promised and is missing is an incident (`CRDsAbsent`, withhold `Ready`, page, at every profile); a gateway that was never part of the tier is a documented state (`GovernanceSkipped=GatewayDisabled` since A8, `Ready` unaffected, loud on every Agent). Profile is developer ergonomics; tier composition is what was installed; `Ready` keys on the second. No carve-out, no `--profile` flag, no sunset to forget.

  **The arithmetic (BLOCKER 1, MAJOR 4).** §3.5 derived the int64 threshold (~9,223,372) and then wrote a grammar admitting 9,999,999, which wraps to a negative `tokensPerDay` that the `rate == 0` guard let through. Now six integer digits — the largest bound a regex can express that is also safe — and the guard is `rate < 1`. Separately `capacity = rate × 3600` on a rate sized against 86,400 let 4.17% per replica over budget through, breaking the "cuts early, never late" invariant the same section invokes; dividing by 90,000 makes the burst come out of the budget rather than on top of it, exactly.

  **Mechanism where prose was standing in.** Mandatory sets now range over emitted **resources**, not only policies, so ADR-0014's Backend-level BAA restriction is expressible in the registry that claimed to extend it (MAJOR 6). The registry's entries are pinned **behaviourally** — remove an entry's input, assert the route is withheld — because the golden diff passes when someone edits the table and the set together, and the "exhaustive switch" backstop the previous draft cited does not exist in Go. Also fixed: `P = 0` and the empty model set, both of which panicked or had no error to name in a function §4 calls total (MAJOR 2); pricing keys resolved by longest literal prefix so nothing is ever split (MAJOR 3); `llm.fallback` must resolve inside `egressAllowlist` or design 20 remediates onto an endpoint the gateway blocks (MAJOR 8); `updated` is chart-owned so an `internal/*` upsert cannot clear the staleness alarm (MAJOR 11); the card route is emitted only when `expose.a2a` is set, since the default Agent has no parent to inherit auth from and inheritance made it uncompilable (MAJOR 12); and the off-worker poll clause, added unnumbered in `ec0a8d5`, is deleted — it removed the per-object serialization that is §3.3's only ordering guarantee (BLOCKER 4).
- **A8 (2026-08-26) — round 3: propagation, and one decision retracted.** The third critique returned REVISE (5 blocker, 10 major, 10 minor) and named the pattern: *"the reasoning is converging; what is not converging is reach."* Three blockers were one-line edits in files A7's commit never opened — the six-digit grammar was fixed here and left at seven in design 02 §3.1, which this design calls the authoritative schema; the 90 000 divisor was fixed in §3.5 and left at 86 400 in §3.4's row; and the condition split was made without updating design 10's alert or design 08's terminal set, so every Agent on a stock P1 install would have paged and every `plume deploy` aborted. All fixed at every site, not only at the point of edit.

  **Tier-absence is now its own condition type, `GovernanceSkipped` (BLOCKER 2).** `GatewayDisabled` was a *reason* on `GatewayIncompatible`, and every consumer in the corpus keys on the condition **type**. `GatesSkipped` and design 24's `ReBACAvailable` already establish the right shape: a tier that was never installed is not an incident. Condition count **26**.

  **`gateway.enabled` is the subchart's `dependencies[].condition` (BLOCKER 5)**, not a free-standing boolean — so declared and installed cannot diverge by typo, and "default `true` at core tier" no longer reproduces the never-`Ready` fleet on a chart that ships no gateway.

  **Also fixed**: acceptance is verified for every resource a mandatory entry names, since §3.3 polled only policies while §3.3.1's entries now range over Backends — a rejected egress-restricted Backend would have had its route created anyway, silently (ADR-0020 decision (3) amended); `-ratelimit` is mandatory on a *derived rate* rather than on a budget field, which was falsifying design 25's guarantee that in-cluster models always compile; `internal/*` resolves inside any allowlist, or design 02's own canonical `fallback` example failed its new reachability check; `CRDsAbsent` covers uninstall-while-serving, not only cold start; `true → false` runs reverse-order teardown; the no-stall claim is replaced with the honest fleet-latency cost and the `MaxConcurrentReconciles` it requires; §8 no longer promises the property test §3.3.1 spends a paragraph calling impossible; and four wrong-section citations are corrected — in an amendment set whose round-1 blocker was a misresolved citation.
- **A9 (2026-08-26) — round 4: the first pass with no blockers, and the last of the reach.** REVISE with **0 blocker**, 5 major, 6 minor; the reviewer's words were "the substance has converged". Every fix was a one-line edit, and five of the eleven were the same failure that has now survived four consecutive fix commits: the paragraph gets fixed and the sentence beside it does not. §3.3 step 2 is the sharpest example — A8 prepended "acceptance of every resource a mandatory entry names, Backends included" and left all three normative clauses saying *policy*, so an implementer coding from the mechanism would still have created a route over a rejected Backend. Fixed. Also: `gateway.enabled` and `NOTES.txt` were written in the present indicative and exist nowhere — now stated in the future tense and **owed to design 07 §3 (A1)**, which is where the chart lives; `AgentgatewayBackend` acceptance status is flagged as an unresearched third-party claim §8.1 must verify; the `(enabled × CRDs)` table is noted total over *states*, not transitions; and one new wrong-section citation, minted by the commit that corrected four, is fixed.
- **A10 (2026-08-27, ADR-0028 supersedes ADR-0020) — the version was wrong, and two guarantees went with it.** This design was written against "agentgateway 2.2". **There is no 2.x** — releases are `v1.x`, latest stable **v1.4.1**, and `2.2` was a kgateway-era docs path that still returns HTTP 200 while serving content predating `AgentgatewayModel`, `virtualModel` and `oauthTokenExchange`. Every citation resolved and served stale content, which is why four internal critique rounds could not reach it and a cross-family review plus a primary-source re-verification did.

  **§3.3's barrier proved nothing** (new §3.3.2). `Accepted=True` is reported by a policy targeting a not-yet-created route — with `output: []`, no policy emitted — and this design *creates routes last*, so the barrier was satisfied by exactly the state it existed to exclude. Replaced with an explicit per-kind convergence tuple at the current generation, including the Gateway-shaped ancestor, the synthetic-`StatusSummary` negative check, and the zero-ancestors case that would otherwise hang a poller forever. It proves **control-plane convergence, not enforcement**: a dataplane NACK is a Kubernetes Warning Event, never a condition, so the Event is watched and the limit is stated rather than glossed. Update ordering is specified alongside create and remove.

  **§3.5's arithmetic was exact and irrelevant.** Token limits apply to future requests only — the crossing request completes — and `tokenize` is unreachable from Kubernetes. "Cuts early, never late" is withdrawn and replaced by a stated overshoot bound. Separately `burst` applies only to `requests`, so the token bucket has no burst and the 90 000 divisor that existed to pay for it is void; `unit` is required and offers only Seconds/Minutes/Hours, so a daily budget is emitted hourly; and every emitted field is `*int32`, with `burst >= 0` validated by plume because tag v1.4.1 has no `Minimum`.

  **R1 is closed by source** and is worse than the open item assumed: same-level conflicts iterate a randomly-seeded `HashSet`, so the winner varies per replica and per restart. One-concern-per-policy is load-bearing.

  **Named as owed when A10 was written, and discharged by A11**: the `egressAllowlist` mechanism and the MCP tool filter. Research §9's gaps — evaluation order, negative-burst runtime behaviour, whether a NACK'd policy fails open — **may not be encoded**; the last one blocks the update-ordering design above.
- **A11 (2026-08-27) — the two redesigns A10 named as owed.** Both were mechanism errors inherited from the superseded research note, not wording.

  **Egress (§3.4.1, Codex BLOCKER 6).** `llm.egressAllowlist` compiled to "a Backend restriction" and **no such field exists** — the CRD has no host, domain, provider or model allowlist anywhere. The only expressible control is a Backend that is a *closed set of destinations by construction*, so the mandatory entry becomes `egressEnumerated`: the resolved provider set equals the allowlist, `dynamicForwardProxy` is never emitted, and the predicate reads the **effective host** because `host`/`port` silently override managed-provider defaults. plume must own the provider→endpoint catalog; agentgateway supplies none. **Still open, and stated rather than papered over**: the entry grammar and the catalog's versioning — which is the substance of BLOCKER 6 and is not closed by A11.

  **MCP tool filtering (§3.4.2).** `Mcp-Name` header matching is superseded by `backend.mcp.authorization`, CEL over `mcp.tool.name`, which **filters `tools/list` items as well as rejecting calls** — a denied tool becomes invisible rather than merely unreachable, which header matching could never do. Emission uses `action: Allow` and never `Deny`, because upstream warns a CEL expression failure under `Deny` fails **open**. Auth moves to `traffic.jwtAuthentication.mcp`, which is documented to run *before* transformation and rate limiting — the ordering §3.3.1 needs and previously assumed without a citation.