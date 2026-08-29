# Design 03: Policy compiler (PolicyIntent → agentgateway v1.4.1 resources)

- **Status**: **RE-OPENED, do not implement** — `ADR-0028` (supersedes ADR-0020) after the research note was found to name a release that does not exist. **Amendments A1–A16 (2026-08-25/27)**, folded into §§3–8: four Claude critiques and two Codex cross-family reviews, all REVISE, then §§3.3/3.4/3.5 rebuilt against agentgateway **v1.4.1** and against an execution spike that measured seven previously-argued behaviours (`research/agentgateway-v1.4.1-spike.md`). **No pass has returned PASS**; Codex r2's twelve majors are open (`reviews/03-codex-review-r2.md`) (§11)
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
PolicyIntent {                      // ONE revision's material — see below (A26)
  target:      {kind, ns, name}
  revision:    {hash, weight, candidate: bool}
  identity:    {spiffeID | oauthClientID}
  tools:       [Binding<{backendRef, toolAllowlist?, requiresApproval}>]
  knowledge:   [Binding<{endpoint, scope: {entityTypes}}>]
  llm:         {providers[]: Binding<EndpointID>, egressAllowlist?: [AllowEntry],
                fallback?: Binding<EndpointID>, fallbackActive: bool}
  // Binding<T> = {requested: string, resolved?: T, state: Resolved|ProducerAbsent|Unresolvable}
  //   an empty slice means "requested none"; it can no longer mean "resolution failed" (A26)
  // EndpointID  = {arm, <arm's instance fields>, model?}   — design 02 A24, §3.4.1.1
  // AllowEntry  = EndpointID minus model, plus optional models[]
  budget:      {tokensPerDay, usdPerDay, taskTimeout, maxHops}
  expose:      [{protocol, visibility, auth, consumerBudgets?}]
  gatewayReplicas: int          // declared, never discovered — see "Field provenance" below (A3)
}
```

**One `PolicyIntent` per revision, not one per Agent (A26).** An earlier shape carried `revisions[]` alongside a **single** `budget`, `expose`, `tools` and `llm` at Agent scope. That cannot express what A25 requires: with `budget` and `expose` on the behaviour surface, a change mints a candidate whose policy differs from the active revision's, and the active revision must keep serving its own until the candidate passes its gate. One global value cannot describe both at once — so a pure compile either rewrote R1's serving policy before R2 was gated, letting a widening reach production ungated, or preserved R1 and could not compile R2 at all.

So the operator constructs **one intent per live revision** — the active one, and the candidate if any — and `Compile` returns that revision's `ResourceSet`. Two further rules follow, and both are contract:

- **The applied `ResourceSet` is persisted under its revision hash**, and an active revision's resources are **never reconstructed from current Agent spec**. Reconstruction is how a spec edit silently rewrites what a gated revision is serving; the retained material is the record of what passed the gate.
- **The receipt backstop reads the *active* revision's budget**, not the current spec's. Otherwise raising a held candidate's budget from \$10 to \$100 would clear the active revision's `BudgetExhausted` hold before the candidate was ever promoted — the spend never moved, only an ungated field did.

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
| `gatewayReplicas` | chart value `gateway.replicas` → operator flag `--gateway-replicas`, default `1` | yes |

**Every binding carries its resolution state, because an empty slice meant two things (A24).** A resolved `tools: []` represented both *"requested no tools"* and *"requested a tool whose producer could not resolve it"* — opposite required outcomes, identical to a pure function, so an Agent asking for a tool it never got could compile as one asking for none and report `Ready`. Each binding now arrives as `{requested, resolved?, state}`:

| `state` | Meaning | Compiler |
|---|---|---|
| `Resolved` | the producer returned an endpoint | emit the route class |
| `ProducerAbsent` | the producing design is not installed (11 for tools, 01/13 for KG) | **do not emit**; no per-Agent condition — the tier gap is reported once at operator level (§5) |
| `Unresolvable` | the producer exists and the name does not resolve | `PolicyCompileFailed` naming the binding — the Agent asked for something real and did not get it |

`Compile` never sees an ambiguous empty list, and the three states are pinned independently. A field whose source does not exist yet is **absent**, and absence is governed by §3.3.1 — it is never silently defaulted.

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

### 3.3 Apply protocol — fail-closed, and event-driven (A15)

Order is part of the contract:

1. **Backends** first.
2. **Policies** next.
3. **Routes / weight changes last** — traffic only ever flows through fully-policied paths. Candidate isolation policies are converged before the candidate header route exists.
4. **Removal reversed**: routes detach (or zero-weight) before their policies and backends are deleted.

Between 2 and 3 sits a convergence barrier over **every resource a mandatory entry names, Backends included** (§3.3.1) — never `Accepted=True` alone, which a policy targeting a not-yet-created route reports while emitting nothing (§3.3.2). §5 covers applied-but-not-converged and partial apply.

**The barrier is a state machine, not a wait** (A15). An earlier draft polled for up to 30s *on the reconcile worker*, reasoning that controller-runtime's one-reconcile-per-object was the only thing serializing the sequence. It is — but a blocking wait is the wrong way to keep it. Each not-yet-converged Agent occupies a worker for the whole bound, requeues, and does it again, so a principal who may create Agents can keep more permanently-pending objects than there are workers and starve every other reconcile — kill, budget-hold, finalization — behind them. Raising `MaxConcurrentReconciles` changes how many bad objects are needed, not the outcome.

So the operator **applies once and returns**, and progress is driven by watches:

| Stage | What it does | What advances it |
|---|---|---|
| `PreparingRoute` | SSA the route in its **inert** shape: `parentRefs` set, **no `backendRefs`** | route status watch |
| `ApplyingBackends` | SSA the Backends for the desired set | Backend status watch |
| `ApplyingPolicies` | SSA the Policies — they can attach, because the route now exists | Policy status watch |
| `Converging` | evaluate §3.3.2's per-kind tuple over every mandatory resource | status watches; `RequeueAfter` for the deadline |
| `Publishing` | attach `backendRefs` / shift weight so the route carries traffic | route status watch |
| `Served` | steady state | spec change, or a NACK Event |

`status.apply` carries `{transaction, digest, generation, stage, deadline}` — the **transaction kind** as well as the digest, because the stages differ (§3.3.3).

**`PreparingRoute` exists because the policy barrier cannot be met without it.** An earlier draft ordered Backends → Policies → converge → *then* create the route. The spike measured that a policy whose target route does not exist reports `Attached=False` (§2.1), so a cold create reached `Converging` and could **never** leave: the stage that creates the route sat behind the barrier that needed it. The route is therefore created first, in the shape §2.6 measured as attachable-but-not-serving. Controller-runtime's per-key serialization is untouched, because each reconcile still returns before the next begins — what is removed is the sleeping.

**Stale state is discarded, and recovery re-enters the transaction's FIRST stage (A26).** On every reconcile, if `status.apply.digest` is not the digest of the currently desired resource set, or its `generation` is not the CR's current generation, the record is dropped — and the transaction is **recomputed against the currently serving applied set**, then entered at *its own* first stage: `PreparingRoute` for a `Create`, `ApplyingBackends` only for a proven `Loosen`.

An earlier draft restarted unconditionally at `ApplyingBackends`. A19 removed the `Tighten` transaction and I recorded that as dissolving this finding; **that was wrong, and stated here because the retraction matters more than the fix**. Removing `Tighten` did not remove `Create`'s first stage. A superseding generation naming a new candidate route would drop the old record, start at `ApplyingBackends`, and attach policies to a route that had never been created — which the spike measured as `Attached=False` (§2.1), deadlocking on exactly the cold-create barrier A17 was written to fix. The route identity the transaction uses is persisted with the stage, so recovery knows which route it must prepare. Without that rule, reconcile N's recorded stage would let reconcile N+1 publish a route whose policies belong to the previous generation — which is the fail-open the blocking poll existed to prevent, arriving through the status field instead.

**The deadline is a condition, not a timeout.** Exceeding it sets `PolicyApplyIncomplete` naming the resource and its unmet condition, and the machine stays in its stage; it does not abandon or roll back. Withheld routes stay withheld, which is the fail-closed direction.

**A NACK is the sixth input, and it is not optional** (§3.3.2, A14). `AgentGatewayNackError` is a Gateway-scoped Warning Event and the *only* observable of a dataplane rejection. The operator watches Events on the Gateways it emits to, parses the key — `policy/traffic/<ns>/<name>:<section>:<ns>/<route>` — and maps it to the owning Agent. A NACK naming a resource in the current desired set raises `PolicyApplyIncomplete` and returns the machine to `Converging`. It **cannot** advance anything: the key carries no generation, so a NACK proves a rejection happened, never that one did not.

**Fairness is a test, not an assertion.** More permanently-pending Agents than workers, while an unrelated Agent's kill transition must complete within a stated bound. A design that merely says it does not block has not shown it.

### 3.3.1 Required concerns — a route is never created without its guarantees (A1)

§3.3 orders the concerns that **exist**. It says nothing about a concern that was never emitted, and that gap is the second half of the same hole ADR-0028 exists to close: a policy that fails acceptance stops the route, but a policy that was never compiled has nothing to fail, and the route is created onto an open door. §4's totality rule does not cover this either — "every field maps or errors" governs fields that are *present* in the `PolicyIntent`, not required concerns whose input is absent.

**The unit is one Agent.** `Compile` takes one `PolicyIntent` and returns one `ResourceSet`; one Agent's failure never touches another's. Design 02 §4 reconciles per-CR, and anything wider would let one malformed Agent stop the fleet.

**The rule.** Every route class names the concerns that must be present and `Accepted=True` before that route may be created. A mandatory concern whose input is absent is a **compile error** (`PolicyCompileFailed`, naming the missing input), never an omitted policy.

**Requiredness is a property of the binding, not of the route class (A27).** An earlier draft made every tool, KG and LLM route *optional*, so an Agent whose sole knowledge source was unreachable reported `Ready=True`. That contradicts two canonical documents: architecture §04 says an Agent bound to a non-Ready graph gets no traffic, and design 02 §5 says an invalid **sole** knowledge source means no traffic (`KnowledgeBound=False` → `Degraded`). Answering HTTP while every declared capability is gone is not semantic readiness — it is the loud-and-wrong of rule 8, reported as health.

So each declared binding carries its own requiredness, and the Agent aggregates over its **actual contract**:

| Route | Required when |
|---|---|
| A2A serving, card | always, where `expose.a2a` is set |
| KG | **the binding is the Agent's sole knowledge source** (design 01 A5), or is marked required |
| LLM egress | the Agent declares `llm.providers` at all — an agent that cannot reach a model cannot answer |
| Tool | the binding is marked required; an Agent declaring several tools degrades on one, and fails on all |

An Agent with no declared capability of a kind has nothing required of that kind. The failing set is then split into required and optional, and aggregated as:

| Failing | `Ready` | `phase` | CLI |
|---|---|---|---|
| any serving-required route | `False`, reason **by cause** (below) | `Pending` (create) or `Degraded` (was serving) | terminal for `deploy` |
| optional routes only | stays `True` | `Degraded` | reported, **not** terminal |
| none | `True` | `Ready` | — |

**Conditions aggregate by cause, not by route (A27).** An earlier draft gave every serving-required failure the reason `PolicyApplyIncomplete`, which would have named a cause that was never checked when the real one was a compile error. The two conditions are separate and both may be set:

| Cause | Condition |
|---|---|
| unresolved input, missing mandatory input, arithmetic, unresolvable identity | `PolicyCompileFailed` |
| not converged at the deadline, `Accepted`/`Attached` false, a NACK Event | `PolicyApplyIncomplete` |

`Ready`'s reason names the **winning** cause under a fixed precedence — `PolicyCompileFailed` outranks `PolicyApplyIncomplete`, because nothing was applied to fail — so a consumer reading one reason is never told the wrong one.

**One condition instance carries every failing path, and clears only when none remain.** A condition is one per type, so concurrent failures would otherwise overwrite each other's message and recovering one would hide the other. The message lists the failing resources in a **stable order** (route class, then name), so a second failure appends rather than replacing, and `PolicyApplyIncomplete` is cleared only when the set is empty — not when the most recent one recovers.

**The failure is route-scoped, not set-scoped.** An unresolvable tool withholds that tool's route and anything depending on it; the serving route and the card route still compile and apply. The alternative — one bad input voids the whole `ResourceSet` — trades a fail-open for a fail-stuck, where a typo in one tool name takes an agent off the air entirely. §5's rows say this consistently: `PolicyCompileFailed` means *the affected routes* are not emitted, never "nothing applied".

| Route class | Mandatory concerns | Notes |
|---|---|---|
| A2A serving route (`expose.a2a`) | `-auth`, plus `-ratelimit` **when a rate is actually derived** (§3.5) | `auth: none` is an explicit opt-out — see below |
| Candidate header route | `-auth` — always, no opt-out | admitted set is one SVID (design 02 §3.3 step 3); an unauthenticated candidate route is a bypass of the whole gate |
| Tool (MCP) route | `-auth` (as `traffic.jwtAuthentication.mcp`), `-toolfilter` (as `backend.mcp.authorization`, `action: Allow`) | A tool route without its filter grants the server's entire tool surface — and because the filter also prunes `tools/list`, its absence makes every tool *visible* as well as callable (§3.4.2) |
| KG (kgp) route | `-auth`, `-transform` (scope injection) | the provider enforces scope from a header the gateway must actually inject (§3.4) |
| LLM egress route | `-auth`, plus `-ratelimit` **when a rate is actually derived**, plus `-transform` (credential scrub), plus `AgentgatewayBackend(egressEnumerated)` | The Backend entry is **construction, not restriction** (§3.4.1). The predicate holds when the Backend enumerates the **requested** set (`providers[] ∪ {fallback}`) and every effective host in it — override included — is a **subset** of what the allowlist permits; a destination is never added because it is permitted (§3.4.1.1, A16). No `dynamicForwardProxy` is ever emitted |
| Reserved — telemetry tap (10), webhook ingress and kgp-admin (11), replay-mock (17), approval interceptor (22), app projections (23), InferencePool and shadow candidate (25), tenant partition (26) | **unclassified — must not be emitted** | Each is named in §3.4 and none has a mandatory set yet. Listing them as explicitly unclassified is the difference between a deferral and an omission: the registry below refuses to emit an unclassified class, so a future design must add its row before it can ship a route |
| Card discovery route | **only emitted when `expose.a2a` is set.** It then carries the same `-auth` as the serving route, except at `expose.a2a.auth: none`, which drops it for both together | Architecture §05 (Exposure) puts the card on the org/public A2A endpoint, so its reachability follows `expose.a2a.visibility`. **ADR-0019 does not make this route public** — it fixes the card's *source of truth* as the container, which is a different claim; an earlier draft cited it for this and was wrong. An agent at `visibility: cluster` would otherwise disclose its name, version and skill inventory to any unauthenticated caller. **With no `expose` block — ADR-0027's four-line Agent, and the default — there is no external card route at all**: nothing is externally reachable, so there is nothing to authenticate and nothing to leak. An earlier draft said "inherits", which made the default Agent fail to compile a route whose parent did not exist. Note the gate is `auth`, not `visibility`: the two are different enums and `visibility: public, auth: oauth` is reachable and stays authenticated |

**Explicit opt-outs are emitted, not absent.** `expose.a2a.auth: none` drops the `-auth` requirement, but the resource set still carries an explicit no-auth marker for that route. A reviewer diffing two resource sets must be able to tell "this route is deliberately unauthenticated" from "this route lost its auth policy", and two absences look identical. The marker is a **label on the route**, not a policy: an empty `AgentgatewayPolicy` would be subject to §3.3's acceptance gate and could be webhook-rejected, which would make `auth: none` unserveable — the opt-out must not be able to fail.

**The table is compulsory, and the enforcement has a real anchor.** An earlier draft promised "a property test asserts that the set of route-class constants equals the set of table rows" — which is not mechanizable, because a markdown table is not a machine-readable artifact. In practice it degrades into two Go slices in one file that stay green when you delete from both, which is exactly the shape AGENTS.md rule 1 says is this repo's default failure. Design 02 A12's `TestEveryFieldIsClassified` works only because it reflects over a struct the compiler cannot avoid changing; route classes have no such struct.

So the anchor is built rather than assumed. Routes are emitted only through `emit(class RouteClass, …)`, which consults a **registry** mapping every `RouteClass` to its mandatory set; a class absent from the registry cannot be emitted. Because it is the emit gate rather than a checklist, deleting a registry entry disables the route instead of ungating it — the failure direction is closed.

**Mandatory sets range over emitted *resources*, not only policies.** An earlier draft scoped them to `AgentgatewayPolicy` kinds, which cannot express the one control ADR-0014 rests on: `llm.egressAllowlist` compiles to a **Backend**, not a policy, so "the LLM Backend must enumerate only allowed endpoints" (§3.4.1) was outside the stated extension point entirely, and a `hipaa` cluster could emit an LLM egress route with no BAA restriction and nothing in this design would stop it. A mandatory entry therefore names a resource and a predicate over it — `AgentgatewayPolicy(-auth)`, `AgentgatewayBackend(egressEnumerated)` — and both are checked before the route is created.

**What actually pins the sets, and the shape it must NOT take (A23).** A golden test renders the registry to markdown and diffs it against the table above, so the table is machine-checked. That catches divergence, not wrongness: the dangerous mutation is deleting `-auth` from the tool route's mandatory set *and* editing the row to match, which the golden diff passes.

An earlier draft answered that with "for every registered class, one test per mandatory entry" — **cases generated from the registry**, which deletes its own check along with the entry. That is the identical defect this repo has now shipped twice: once in the docs gate, where removing a rule left the suite green, and once here. Cases enumerated from production data cannot pin production data.

So the security cases are a **static catalog**, written independently of the registry and reviewable as a list: *tool route without `-auth` is withheld*, *LLM egress route without `egressEnumerated` is withheld*, and one line per mandatory entry in the table. A class present in the registry with no case in the catalog fails; a case naming an entry the registry no longer has fails. Both directions, or deleting an entry and its case together passes again.

`test/docs/superseded_test.go`'s `ruleFixtures` is the working precedent in this repo — independent fixtures that fail on deletion, on a weakened matcher, and on an over-broad exemption. The mutation harness deletes each production mandatory entry with the catalog untouched and requires a **compiled test failure**; a build error is INVALID, not KILLED (AGENTS.md rule 3).

**Every profile ships its own catalog, because the base one cannot pin what a profile adds (A27).** Compliance profiles extend the mandatory sets *dynamically* (§3.3.1, design 27) and are deliberately absent from the base table — so deleting the `hipaa` profile's `-guard` extension leaves every base case green and the profile silently stops requiring PHI redaction. A profile or pack that adds a mandatory entry therefore ships a security catalog **for its own additions**, and the mutation harness deletes each profile-added entry with that catalog untouched. A profile whose additions have no catalog fails to install rather than shipping unpinned. An earlier draft claimed "deleting from both fails the exhaustive switch over `RouteClass` in Go": Go has no exhaustive switch checking, this repo runs `go vet` with no `.golangci.yml`, and an exhaustive switch would prove only that every class is *handled*, never that its mandatory set is *right*.

**Compliance profiles extend the mandatory sets; they do not appear here.** `-guard` is absent from every row above, which is right for P1 and wrong for ADR-0014: the `hipaa` profile makes PHI redaction mandatory at the gateway, and "compliance is a profile you enable, not an integration you build" has to stay literally true. The credential-scrub `-transform` on the LLM row is a different concern and does not cover it. The table therefore has no profile dimension by design — a profile contributes additional mandatory **entries** per route class through the same registry (design 27), which is expressible now that entries range over resources: the `hipaa` profile adds `AgentgatewayPolicy(-guard)` to the LLM egress row, on top of the `egressEnumerated` entry the base profile already carries. Admission (design 27 §3) rejects a non-conforming Agent CR, and this registry stops a conforming CR from compiling an unrestricted route; the two are different seams and both are needed.

### 3.3.2 What the barrier can and cannot prove (A10)

`Accepted=True/programmed` was not a predicate; the slash hid a missing contract, and the condition it named is **true in exactly the state §3.3 guarantees**. A policy whose target route does not yet exist reports `Accepted=True, reason: Valid, "Policy accepted"` beside `Attached=False`, with `output: []` — no policy emitted at all (upstream fixture `http-route-missing-not-attached.yaml`). Since routes are created last, the old barrier was satisfied by a policy that had produced nothing.

**The convergence tuple**, per kind, all at `observedGeneration == metadata.generation`:

| Kind | Required | Traps this closes |
|---|---|---|
| `AgentgatewayPolicy` | `Accepted=True` with `reason: Valid` **and** `Attached=True`, on a **Gateway**-shaped `ancestorRef` | The success ancestor is the Gateway, **not** the `targetRef`. Failures emit a synthetic `StatusSummary` ancestor — its presence is a negative signal. An unsupported target kind yields **zero ancestors and zero conditions**, so a poller waiting for `Attached=False` waits forever; empty ancestors is a failure, not a pending state |
| `AgentgatewayBackend` | `Accepted=True` at the current generation | This proves **translation only**. The controller sets it even when no gateway resolves and no dataplane resource is emitted |
| `HTTPRoute` | `Accepted=True` **and** `ResolvedRefs=True` | There is no `Programmed` condition to wait for |

**What this proves is control-plane convergence, and not enforcement — measured, not argued** (A14). A dataplane NACK is published as a Kubernetes **Warning Event** (`AgentGatewayNackError`) on the **Gateway**, never as a status condition. A tightening update carrying a dataplane-invalid value was observed reporting `Accepted=True`, `Attached=True` at `observedGeneration == generation` while the proxy rejected it and **the previous permissive rule kept serving**: eight requests under a limit that should permit one all returned 200 (`research/agentgateway-v1.4.1-spike.md` §2.8). **A NACK'd policy retains the old config; it does not fail closed.**

So the operator **must** watch that Event for its emitted resources and raise `PolicyApplyIncomplete` on it — mandatory, not defensive. The event key (`policy/traffic/<ns>/<name>:<section>:<ns>/<route>`) names the policy and the route, so a NACK **can** be attributed to a resource plume emitted; it carries **no generation and no UID**, so an old NACK cannot be told from a new one and **absence of an Event is not success**. That makes the Event sufficient to raise degradation and insufficient as a success barrier. Until upstream publishes a positive dataplane acknowledgement, this design claims control-plane convergence only — and **no transaction tightens a serving route in place** (§3.3.3, A19). An earlier draft offered "or accept that a silent NACK leaves the old rule serving", which was a measured fail-open rather than an option; its replacement, a witness probe, could not attribute its own result. Both are withdrawn. A tightening is a spec change that mints a revision and is gated on a candidate route, so the serving path is never the thing being converged. Two-phase publication — create the route inert, converge its policies, then attach — is what the ordering in §3.3 becomes; the alternative is a barrier that passes on an unattached policy.

**Update ordering was part of the contract, and A19 removed the case it governed.** An earlier draft required a tightening change (`auth: none → oauth`, a narrowed tool filter) to withdraw its dependent routes before applying, converge, then republish. There is no such transaction now: tightening is a gated spec change on a candidate route (§3.3.3), so a serving route is never the thing being converged and never made inert. What remains is `Create` and `Loosen`, and a `Loosen` that NACKs leaves the *stricter* old rule serving — a failure toward denial.

### 3.3.3 There is no in-place tightening (A19)

A17 specified a `Tighten` transaction — quiesce the live route, converge, prove a witness, republish. **It is withdrawn.** Two measurements and one structural fact removed it:

- a NACK'd policy **retains the previous configuration** while every condition reports converged (§2.8), so an in-place tightening can silently fail to apply;
- the witness that was meant to catch that **cannot attribute** its own result. A 401 can come from the backend, a rejected tool from an unhealthy MCP server, an unreachable host from DNS, a 429 from an upstream or an already-empty bucket. A NACK and a passing witness coexist, and `Publishing` then widened the match on a false positive (Codex r4 BLOCKER 2); and
- A17's escape for an unwitnessable concern — "use the revision path" — was unavailable for exactly the fields that needed it, because a policy-surface change mints no candidate (r4 BLOCKER 4).

**So tightening is not a compiler transaction at all.** Design 02 A25 moves `budget` and `expose` to the behaviour surface, which are the only two policy-surface fields feeding a mandatory concern; `tools[].name`, `knowledge[].scope` and `llm.egressAllowlist` were already there. Every tightening therefore mints a revision and runs through design 02 §3.3's existing blue-green machinery: the candidate gets **its own** routes and policies, is gated at weight 0, and weights shift only on pass. The old route is never mutated, so there is nothing to quiesce, nothing to withdraw, and no NACK window on a serving path.

Two transaction kinds remain:

| Kind | When | Stages |
|---|---|---|
| `Create` | a revision's routes do not exist yet — a new Agent, or a gated candidate | `PreparingRoute` → Backends → Policies → `Converging` → `Publishing` |
| `Loosen` | the new set is **provably** no stricter than the applied one | Backends → Policies → `Converging` → `Publishing`, route stays live |

**`Loosen` needs a comparator, and "admitted set" is not one (A24).** Rate limits are stateful quantities, transforms are functions, timeouts are neither, so "every concern's admitted set is a superset" is not implementable and two reasonable implementations classify the same edit differently. The comparator is a **closed registry, one entry per concern**:

| Concern | Ordering | `Loosen` when |
|---|---|---|
| `-auth` | `none` < `oauth` | auth is removed or weakened |
| `-ratelimit` | numeric on the derived rate | the new rate is **≥** the applied one |
| `-toolfilter` | set inclusion on tool names | the new set is a **superset** |
| `egressEnumerated` | set inclusion on endpoint identities | the new set is a **superset** |
| `taskTimeout` | numeric | the new timeout is **≥** the applied one |
| `-transform`, `-guard` | **no ordering exists** | never — functions, not sets |

**Normalization is part of each comparator, because a default is not an absence.** Before comparing, both sides are normalized to their **effective** value: an unset `expose.a2a.auth` is `oauth` (the schema default), an unset `budget` field is *no limit*, an unset `taskTimeout` is the gateway's own default. Comparing raw presence instead would read a field gaining its default as a change, and a field losing an explicit value equal to the default as a loosening.

**Unknown maps to `Tighten`.** A concern absent from the registry, a comparison it cannot make, and a **first application** — where there is no applied set to compare against — all classify as `Tighten`, so adding a concern without a comparator entry fails safe rather than silently qualifying for the in-place path. Property-tested for reflexivity and transitivity, plus every boundary: a rate unchanged, a set unchanged, a default appearing or disappearing, and `nil` against a value.

**But `Tighten` means "mint a revision", and not every producer can (A25).** §3.1's provenance table lists inputs that are **not Agent spec**: a tool server's advertised set (11), a resolved KG endpoint (01/13), tenant consumer budgets (26), and `status.llmFallbackActive`, which design 20 sets deliberately. Telling those producers to mint a revision points at a mechanism they do not have — so a tool server withdrawing a tool would narrow a mandatory filter with no path at all.

| Producer | Path |
|---|---|
| Agent spec, behaviour surface | mints a revision; gated (design 02 §3.3) |
| Agent spec, policy surface, provable `Loosen` | in place |
| **Non-spec producer, tightening** | **policy-input revision** — a content-addressed candidate keyed on the *input*, gated like any other, because the Agent's spec did not change and cannot carry it |
| **`status.llmFallbackActive`** | **the stated ADR exception**, below |

**`llmFallbackActive` is not covered by this machinery, and saying so is the point.** Design 20 flips status and recompiles the serving Backend precisely so an incident is not delayed by an eval cycle — the exemption design 02 A12 already records as making the answering model changeable ungated. Claiming A19's isolation covers it would assert protection the path does not have. What it owes instead is **pre-provisioning**: the fallback's Backend is emitted and converged *before* drift is detected, so activation is a weight shift over already-converged material rather than a live recompile that can NACK and leave the drifted primary serving while status records a remediation.

Being wrong toward `Create` costs a gate cycle. Being wrong toward `Loosen` is the bypass this section exists to remove.

**A NACK during `Loosen` is bounded by what a loosening can do.** It leaves the *stricter* old configuration serving — a failure toward denial, which is the direction §3.3's ordering has always preferred. It still raises `PolicyApplyIncomplete` (§3.3.2), because a loosening that silently did not apply is a stale guarantee even when it is a safe one.

### 3.4 Concern mapping

| Intent | Gateway mechanism (OSS only) | Notes |
|---|---|---|
| Routing | HTTPRoute + Backend | per-revision weights (design 02) |
| AuthN | JWT policy (IdP issuer) + SPIFFE mTLS | **Evaluation order is not asserted here.** An earlier draft claimed a native `auth → rate-limit → guards` order taken from a secondary source, while the Guards row disclaimed the same ordering — see §3.4.3 |
| `budget.tokensPerDay` | **local token-bucket rate limit**, `tokens = ⌊tokensPerDay ÷ (24 × gateway_replicas)⌋`, `unit: Hours` (§3.5). **No burst**: `burst` applies to `requests` limits only, so the capacity arithmetic an earlier draft carried is void (A10) | **approximation tier**, and weaker than an earlier draft claimed: continuous refill ≠ calendar window, and the excess is bounded by **concurrency, not replica count** — v1.4.1 checks `available_refill() > 0` and decrements only after a response, so every concurrent request on one replica passes the same positive bucket. The `≤ replicas×` figure was wrong. **Resolved by measurement (A13)**: the excess is **unbounded** — 100 concurrent requests admitted 100x a one-replica budget (spike §2.7). No figure is published. Backstop tier = receipt aggregate (below). No external RLS / Redis — doctrine rule 2 |
| `budget.usdPerDay` | token-equivalent local limit computed at the **max price across the agent's allowed models** — conservative on *price*, the only dimension it is conservative on (ADR-0028) | pricing table §3.5; **an unresolvable model ⇒ compile error** |
| **Backstop budget tier (both tokens & usd)** | not a gateway mechanism, and **not exact in USD** — it sums the same `usd_est` estimates the gateway prices from (ADR-0028): **design 04's spend aggregation** (per-agent daily spend, 00:00 UTC windows, materialized where receipts land) is read by the operator each reconcile → overrun ⇒ `BudgetExhausted` condition + weight-0 via the rollout machinery | matches design 02's approved "windows reset 00:00 UTC; remaining in status" — status is fed from the aggregation, not gateway counters. Deltas recorded in design 02 §11 (r1 f3, f5) |
| `taskTimeout` | route timeout | |
| `maxHops` / cycles | lineage-header transform emitted here; semantics owned by design 22 | reserved |
| Tool allowlist | **`AgentgatewayPolicy.spec.backend.mcp.authorization`** — CEL over `mcp.tool.name`, `action: Allow` (default-deny once any allow rule exists). Filters `tools/list` items *and* rejects `tools/call`, so a denied tool is **invisible**, not merely unreachable (A11) | Header matching on `Mcp-Name` still exists as SEP-2243 but cannot filter a list response |
| **KG scope** | **gateway-injected, provider-enforced** (r1 finding 6): compiler emits a request-transform policy injecting the `X-Plume-KG-Scope: {entityTypes}` header on **all** kgp routes (trust = the gateway's SVID on the provider connection — no separate header signature; design 13 r1 f4); the *provider* enforces it across all four fact-bearing tools (`search`, `neighbors`, `get_context_bundle`, `cite`) and answers out-of-scope with `KG_SCOPE_DENIED`. The gateway does not parse MCP bodies (consistent with design 01 §3.1); trust consequence: providers are scope-enforcing, and **kgp conformance gains a scope-enforcement battery** — recorded as amendments in design 01 §11 | |
| `requiresApproval` | route to approval interceptor | design 22 |
| Guards / egress | prompt-guard policies; **`llm.egressAllowlist` compiles to Backend *construction*, not restriction** (§3.4.1, A11) — the CRD has no egress, host, domain or provider allowlist field anywhere | ADR-0014; ordering per §3.4.3 |
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
| Model serving | LLM Backend registration for `expose.llmBackend` + **`backendRefs`-weighted shifting** (A24 — *not* virtual models, which ADR-0028 records as experimental and off by default); shadow candidate route admitted-set = the eval run principal only (launch→end) | design 25 r1 f2/f5 |
| Tenant quotas | `TenantPolicyIntent` (tenant-scoped target): partition listener set + quota policies; approximation tier per ADR-0028, backstop tier = 04 A2 rollup | design 26 r1 f1 |
| Interior telemetry | OTLP Backend + per-agent route to the tap's forward-only listener (rate-limited — telemetry is traffic) | design 10 r1 f1; agents are default-deny |
| Receipts | OTLP tracing at **`AgentgatewayPolicy.spec.frontend.tracing`**, which may target **only a Gateway**. **`captureLevel` therefore leaves `PolicyIntent` entirely (A24)**: it is not per-Agent expressible at v1.4.1, and leaving it in the intent invited two reconcilers to last-writer-win one Gateway policy — a per-Agent field silently deciding a cluster-wide setting. Tracing is emitted **once per Gateway** by a single owner from a chart value, and design 04 records capture verbosity as a gateway-scoped setting rather than an Agent one | design 04 |

### 3.4.1 Egress is enumeration, not restriction (A11)

`llm.egressAllowlist` was specified as compiling to "a Backend restriction". **No such field exists** — one grep of the whole CRD API returns a single unrelated hit (the MCP JSON-RPC method allowlist). There is no host, domain, provider or model allowlist on `AgentgatewayBackend`, so nothing can filter a wider set down to an allowed one.

What the schema does expose is a **closed set of destinations by construction**. `AgentgatewayBackendSpec` is `ExactlyOneOf=ai;static;dynamicForwardProxy;mcp;aws;a2a`; the `ai` arm holds one `LLMProvider` or `groups[]` of them (**max 8 groups × 16 providers** — a bound the compiler enforces, and a compile error past it). A Backend reaches the providers it names and nothing else. So the control compiles to: *emit a Backend enumerating exactly the endpoints the Agent **requested**, prove that set is a **subset** of what the allowlist **permits**, and ensure the route can reach no other Backend.*

Three consequences the old wording hid:

- **plume owns the provider→endpoint catalog; agentgateway supplies none.** An allowlist entry is meaningless until it resolves to a concrete `LLMProvider` arm (`openai`, `anthropic`, `gemini`, `bedrock`, `vertexai`, `azure`, `azureopenai`, `custom`) plus an effective host. An entry that resolves to nothing is `PolicyCompileFailed` naming it — never dropped, because a dropped entry silently widens nothing but a dropped *deny* silently widens everything.
- **`dynamicForwardProxy` is the anti-control and is forbidden outright.** Its own doc comment: *"this backend type can send requests to arbitrary destinations."* The compiler never emits it and rejects any intent that would require it.
- **The predicate reads the effective host, not the provider name.** `host`/`port` **override managed-provider defaults**, so a Backend naming `openai` is not pinned to OpenAI's endpoint. `egressEnumerated` is therefore a predicate over the resolved endpoint set — provider default, or the override where one is set — and a `custom` provider must carry `providerBackend` or `hostOverride` or translation fails anyway.

**This is a design change, not a wording fix, and it is not finished here.** The catalog's contents, its versioning, and how it stays correct as vendors move endpoints are unspecified — and the allowlist entry grammar (host? provider id? model id?) is still undefined, which is Codex BLOCKER 6's substance. What A11 settles is the *mechanism*: enumeration, with a plume-owned resolver and a `dynamicForwardProxy` ban. ADR-0014's "enforced at the gateway" wording needs the same correction and does not yet have it.

#### 3.4.1.1 The entry grammar, the catalog, and the subset rule (A16)

A11 settled the mechanism and left the language undefined, which is not an implementable control. Three things follow.

**Entries carry the provider's own identity, because a name and a host are both too weak (A18).** An earlier draft used `provider:` or `host:`, which the shipped CRD refutes: a managed provider's destination depends on **instance** fields, not on the arm alone. Verified in the v1.4.1 `AgentgatewayBackend` schema:

| Arm | Instance fields it carries |
|---|---|
| `anthropic`, `openai` | `model` |
| `azureopenai` | `apiVersion`, `deploymentName`, `endpoint` |
| `vertexai` | `model`, `projectId`, `region` |
| `bedrock` | `model`, `region`, `guardrail` |
| `custom` | `backendRef`, `formats`, `model` |

Two Azure OpenAI resources therefore share the `azureopenai` arm and differ in `endpoint` and `deploymentName` — and may differ in BAA status. An arm-level entry cannot tell them apart, so it must either reject a valid endpoint or permit both. A host-only entry is no better: it carries no port, and two services on one host at different ports are indistinguishable.

Entries are therefore an **endpoint identity**, discriminated by arm and carrying that arm's instance fields:

```yaml
egressAllowlist:
  - arm: anthropic                              # every model this arm serves
  - arm: openai
    models: [gpt-4o, gpt-4o-mini]               # narrowed
  - arm: azureopenai
    instance: {endpoint: acme.openai.azure.com, deploymentName: gpt4o-prod}
  - arm: custom
    endpoint: {scheme: https, host: llm.internal.example.com, port: 8443, pathPrefix: /v1}
```

**`models` is enforceable on some arms and not others, and the difference is in the table above (A19).** `anthropic`, `openai`, `vertexai`, `bedrock` and `custom` each carry a `model` field, so a narrowed set compiles into the emitted provider block. **`azureopenai` does not** — it carries `apiVersion`, `deploymentName` and `endpoint`, and at `apiVersion: v1` the model may be supplied by the request, so nothing in the provider block pins it. A18 claimed "every arm carries its own `model` field" **twenty lines below the table that shows otherwise**, which would have let a `models:` permission on an Azure endpoint pass the tuple check while enforcing nothing. So: a `models`-scoped entry is **rejected at compile time for `azureopenai`** unless a non-v1 `deploymentName` is given and treated as the constrained model identity. Pinning v1 model selection would need a request-level guard this design does not emit, and inventing one here is what the retraction above exists to stop. Anything unresolvable is `PolicyCompileFailed` naming the entry — **never dropped**, because a dropped entry silently widens the emitted set.

**The catalog is plume's, because agentgateway ships none.** A `ConfigMap plume-provider-endpoints`, chart-shipped and versioned exactly like the pricing table (§3.5), maps each `LLMProvider` arm — the closed set `openai azureopenai azure anthropic gemini vertexai bedrock custom`, verified in the shipped CRD — to its effective host(s). It carries the same staleness and single-writer rules as the pricing table, and for the same reason: two writers inside one YAML scalar cannot be given disjoint ownership.

**Requested ⊆ permitted, and never equality.** A11 said the emitted Backend's provider set must *equal* the allowlist. That conflates two different things: `llm.providers` is what the Agent **requested**, `egressAllowlist` is a **ceiling** on what it may reach. Under equality, a compliance profile permitting `{A,B,C}` for an Agent that requested only `{A}` would emit a Backend that can reach B and C — turning a permission into a destination. The rule is:

- the emitted Backend enumerates the resolved **requested** set, `providers[] ∪ {fallback}` (§3.5);
- `egressEnumerated` holds iff every emitted provider's **resolved endpoint identity** — the tuple `{arm, scheme, host, port, pathPrefix, instance fields, model}` — is matched by an allowlist entry;
- a destination is **never** added because it is permitted.

The predicate compares **tuples, not hostnames**. An earlier draft compared effective hosts, which cannot separate two ports on one host or two Azure deployments behind one endpoint.

**What this guarantee does not cover, stated rather than implied.** The predicate is over **names**, not addresses: plume does not resolve DNS, so a permitted hostname that later repoints is outside the control, and pinning addresses would break every managed provider. `host`/`port` overrides are themselves checked against the allowlist, since a managed-provider Backend is not pinned to its vendor's endpoint (§3.4.1). `internal/*` resolves inside any allowlist by construction — an in-cluster Service is not egress. And `dynamicForwardProxy` is never emitted at all.

### 3.4.2 MCP tool filtering is first-class CEL (A11)

`Mcp-Name` header matching was the mechanism when the superseded note was written. At v1.4.1 there is a real one: **`AgentgatewayPolicy.spec.backend.mcp.authorization`**, a CEL `Authorization` at the MCP layer whose context exposes `mcp.tool.name` and JWT claims together.

It is strictly stronger than header matching in the way that matters: *"List operations, such as `list_tools`, will have each item evaluated. Items that do not meet the rule will be filtered."* A tool the agent may not call is **absent from `tools/list`**, not merely rejected on call — an agent cannot attempt what it cannot see, and header matching could never do that.

Three rules the emission must follow:

- **`action: Allow`, never `Deny`.** Once any allow rule exists the policy is default-deny, which is the shape a tool allowlist needs. Upstream warns that *"`Deny` is not recommended because expression failures fail to deny"* — a CEL error under `Deny` fails **open**, which is the one failure mode this design cannot accept.
- **Auth moves to `traffic.jwtAuthentication.mcp`.** `backend.mcp.authentication` is deprecated in favour of it *"which ensures authentication runs before other policies such as transformation and rate limiting"* — directly the ordering §3.3.1 needs for `-auth` before `-toolfilter`. The two may not appear in the same policy, which one-concern-per-policy already prevents.
- **Targeting constraints**: `backend.mcp` may not target a `Service`, and may not target an `AgentgatewayBackend` `sectionName`. Both are compile-time checks.

**Emission is total at the CRD's bounds** — `minItems: 1`, `maxItems: 256`, `maxLength: 16384` per expression, verified in the shipped chart and pinned by `test/conformance`. Three cases an earlier draft left undefined, each with a wrong-but-plausible answer:

| Case | Emission |
|---|---|
| **no tools allowed** | one expression `false`. **Not an empty list** — `minItems: 1` rejects it — and **not an omitted policy**, which restores upstream's default-allow and turns "no tools" into *every* tool |
| **more than 256 tools** | `PolicyCompileFailed` naming the count; truncating would silently deny the remainder while reporting success |
| **a name with a quote, backslash or newline** | a real CEL string encoder, never concatenation. A broken expression under `action: Allow` denies everything — a self-inflicted outage — and under `Deny` fails **open**, which is why §3.4.2 forbids `Deny` |

Golden fixtures pin 0, 1, 256 and 257 tools, and names carrying quotes, backslashes, slashes and non-ASCII.

### 3.4.3 Evaluation order is not known, and is not asserted (A20)

An earlier draft asserted a native `auth → rate-limit → guards` order in the AuthN row **and disclaimed the same ordering two rows below**, in the Guards row. Both cannot be design. The assertion came from a blog post carried over by the superseded research note; the v1.4.1 re-verification listed it as one of the claims it could **not** establish from a primary source, and the design kept it anyway.

It is removed rather than softened, because two things depend on the answer:

- **whether an auth-rejected request consumes quota.** If the limiter runs first, a flood of unauthenticated requests exhausts a paying agent's budget — a denial-of-wallet an attacker needs no credential to run. If auth runs first, it cannot.
- **where a compliance profile's `-guard` must attach** for PHI redaction to see a request that a rate limit would otherwise have already rejected (ADR-0014).

**The experiment, written down so it is reproducible rather than re-derived.** One route carrying both a failing `traffic.jwtAuthentication` and `rateLimit.local: [{requests: 1, unit: Hours}]`. Send three unauthenticated requests:

| Observed | Conclusion |
|---|---|
| `401, 401, 401` | auth precedes the limiter, and rejected requests **do not** consume quota |
| `401, 429, 429` | the limiter precedes auth, or counts rejected requests — the denial-of-wallet path is real |

Until that runs, no row in §3.4 states an order, and no design sentence may depend on one. Owed to `make conformance-cluster`, where the harness already exists.

### 3.5 Pricing table and the usd→token computation (A4)

**Two ConfigMaps** (A21; see "Two ConfigMaps" below for why one cannot work): `plume-model-pricing`, chart-owned and versioned, carrying external endpoints and the single `updated` timestamp; and `plume-model-pricing-internal`, operator-owned, carrying in-cluster endpoints. Both are read and merged for usd→token compilation and receipt cost annotation.

**Shape (A21).** One row per **endpoint identity** — the same `{arm, instance fields, model}` tuple `providers[]`, `fallback` and the allowlist use (design 02 A24) — carrying two prices in **USD per 1,000,000 tokens**, the unit vendors publish, chosen so no price is written as `0.000003` and rounded into nothing.

An earlier draft keyed this on `<provider>/<model>`. A24 deleted that string everywhere else, so the table was the last thing requiring a canonical join — and the join is the non-injective one: `{azure, openai/gpt-4}` and `{azure/openai, gpt-4}` collapse to one row while being different endpoints with potentially different prices and different BAA status. Two Azure deployments on one endpoint collapse the same way.

Rows are therefore a list, not a map, and matching is on the tuple:

```yaml
data:
  updated: "2026-08-19T00:00:00Z"        # RFC 3339; drives PricingStale
  models: |
    - id: {arm: anthropic, model: claude-opus-5}
      price: {input: "15.00", output: "75.00"}
    - id: {arm: azureopenai, instance: {endpoint: acme.openai.azure.com, deploymentName: gpt4o-prod}}
      price: {input: "5.00",  output: "15.00"}
    - id: {arm: anthropic}                          # arm-wide default: no model field
      price: {input: "15.00", output: "75.00"}
```

**Matching is most-specific-wins over the tuple**, which is total and admits no tie: a row whose `id` is a subset of the requested identity matches, and the row matching on the most fields wins. Two rows matching on the same field count is a **load error**, not a runtime tie-break — the ambiguity is caught where a human can fix it rather than resolved silently per request.

**Grammar.** Prices and `spec.budget.usdPerDay` are decimal strings matching `^[0-9]{1,6}(\.[0-9]{1,6})?$` — at most **six** integer digits and six fractional, no exponent, no currency symbol, no `resource.Quantity` suffixes. Six fractional digits make the arithmetic exact in integer **micro-USD**, so golden files pin exact integers rather than whatever a float happened to produce.

**Six integer digits, and the number is load-bearing.** The computation multiplies micro-USD by 10⁶, so the largest admissible value must satisfy `U × 10¹² ≤ 2⁶³−1`. At six digits the maximum is `999999.999999` → `999999999999 × 10⁶ = 9.99999999999×10¹⁷`, comfortably inside int64's `9.223×10¹⁸`. An earlier draft derived the true threshold (~9,223,372) correctly and then wrote a *seven*-digit grammar, which admits `9999999` → `9.99999999999×10¹⁸`, which **wraps to a negative** `tokensPerDay` — and the zero-rate guard below tested `rate == 0`, so a negative rate escaped it entirely. A regex cannot express `U ≤ 9223372`; six digits is the largest bound a regex *can* express that is also safe, which is why the schema carries it rather than the code. At $999,999.99 per agent per day the ceiling is far above any real budget.

The `usdPerDay` half belongs on the CRD as CEL and is landed by design 02 A15, since §3.1 there is the authoritative schema; the ConfigMap half is validated at load. **Neither is implemented yet** — `config/crd/plume.dev_agents.yaml` currently has a bare `type: string`.

**Resolution is by endpoint identity, and no string is ever joined or split (A18).** Design 02 A24 makes `providers[]`, `fallback` and every allowlist entry the same tuple — `{arm, instance fields, model}` — so the flat-string key an earlier draft resolved by longest prefix no longer exists. That draft canonicalized `{provider, model}` with a `/`, which `internal/revision/revision.go:66` already records as non-injective: `{azure, openai/gpt-4}` and `{azure/openai, gpt-4}` collided on one row while being different endpoints, and a golden fixture pinning "a slash-containing model in both forms" could not have detected it, because production had already collapsed them.

Lookup matches on the tuple. An entry matching nothing is `PolicyCompileFailed` naming it; a wildcard is expressed as an entry that omits `model`, not as a string suffix. The pricing rows are keyed on that same tuple — an earlier draft carried a "still owed" note here, which A21's schema above discharges.

A model that resolves to nothing is `PolicyCompileFailed` naming the offending `spec.llm.providers[i]` (or `spec.llm.fallback`) — never a default price, because a guessed price is a guessed budget.

**`PricingStale` means the table is old, not that a row is missing.** A *missing row* is the compile error above. Design 02 §5's row said otherwise and is corrected by A15 — one condition cannot mean two things without meaning neither.

**What staleness can honestly claim.** The table is chart-shipped with `updated` baked in and **nothing refreshes it automatically**, so `updated > 30d` becomes permanently true on every install of a chart older than a month — an alarm that is always on for everyone, which trains operators to ignore the one signal that matters when prices actually move. Three narrowings make it mean something:

- It is raised only for agents whose usd budget resolves to an **externally-priced** model. In-cluster endpoints are upserted by the model-operator at serve time (design 25 §5) into its **own** ConfigMap and are never stale by this measure. `updated` lives in the chart-owned map alone, so an in-cluster model going live cannot clear the alarm that fires when vendor prices move.
- Refreshing is a **documented human action** — `helm upgrade`, or editing the operator-writable ConfigMap directly. The condition's message names it. Nothing auto-refreshes external vendor prices, and this design does not pretend otherwise.
- The condition is **advisory**: it never withholds traffic or fails a compile. A stale table skews the gateway tier's approximation — and the receipt tier does **not** bound that, because it prices from the same table (ADR-0028). A stale or tampered table skews both tiers identically.

**The computation.** Let `U` be `usdPerDay` and `P` the **conservative price**: the maximum, across every model the agent is allowed to use, of that model's `max(input, output)`. Maximum of both directions because the emitted limiter counts total tokens without distinguishing them, and maximum across models because the real traffic mix can only be cheaper. That is conservatism on **price alone**, and says nothing about *when* the limiter acts — the claim ADR-0028 withdrew.

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

**No overshoot bound is published, because the measured excess is unbounded** (A13). An earlier draft published `gatewayReplicas x maxOutputTokens(model)`, reasoning that one crossing request per replica can be in flight. That was measured against v1.4.1 and is false: with a 1,000-token hourly budget where each request consumes exactly 1,000 tokens, **20 concurrent requests all returned 200, and 100 concurrent requests all returned 200** — 100,000 tokens admitted against a 1,000-token budget, on **one** gateway replica (`research/agentgateway-v1.4.1-spike.md` §2.7). Enforcement is a read-only availability check before dispatch and a decrement after the response, so every request that starts while the bucket is positive is admitted. Serial traffic is limited correctly; concurrent traffic is not limited at all until the first response lands.

So the gateway tier bounds **sustained** rate and nothing else, and the excess tracks *concurrency*, which neither `PolicyIntent` nor the emitted policy constrains. Publishing any figure would be the third retracted budget guarantee in this design. **The honest statement, and what the CLI and `status.budget` say, is that gateway-tier excess is unbounded at v1.4.1 and the receipt tier is the only real limit.** Bounding it would require a plume-side per-replica concurrency cap and a request-size maximum — neither exists in the CRD surface — and that is a design change, not a wording fix.

**Capacity is part of the emitted policy, not an afterthought.** §3.4 emits a token *bucket*, and a bucket has both a refill rate and a burst capacity; an earlier draft gave only the rate, which meant no golden file could be written at all. One hour's refill absorbs the bursts real agent traffic produces — a multi-tool task spends its tokens in seconds, not evenly across the day — while keeping §3.4's promise meaningful: at `capacity = tokensPerDay` the gateway would be a daily ceiling rather than a rate, and an agent could spend the whole budget in the first minute and then be dark until 00:00 UTC.

Two edges the arithmetic forces, both previously unstated:

- **Both budget fields may be set.** `tokensPerDay` and `usdPerDay` each derive a rate; the compiler emits **one** `-ratelimit` policy at the **minimum** of the two. Emitting two policies would put the same field in two policies and break §3.2's disjointness by construction.
- **A rate below 1 token/second is a compile error.** `usdPerDay: "0.50"` against a model priced at `75.00`/M tokens with two gateway replicas floors below 1 token/second — a bucket that denies everything. The test is `rate < 1`, not `rate == 0`: with the grammar bounded at six digits a negative rate is now unreachable, but a guard that only catches exact zero was how the overflow above stayed invisible, and the cheap predicate is the total one. Denial is fail-closed and therefore "safe", but an agent that answers nothing because of a rounding step is rule 8's loud-and-wrong: the condition would name a budget when the cause is arithmetic. `PolicyCompileFailed` names the floor and the smallest budget that clears it.

**Two ConfigMaps, because two controllers cannot own rows inside one string (A21).** Kubernetes field ownership stops at `data.models`; it cannot give the chart and the model-operator disjoint ownership of YAML rows within that scalar, so a Helm upgrade refreshing external prices and a model-operator upserting an in-cluster row would conflict or lose each other's work in a read-modify-write race. So:

- **`plume-model-pricing`** — chart-owned, external endpoints, `updated` lives here and only here;
- **`plume-model-pricing-internal`** — operator-owned, in-cluster endpoints the model-operator upserts at serve time (design 25 §5).

The compiler reads both and merges. **An identity appearing in both is a load error**, never a precedence rule: silently preferring one writer is how a stale external row would shadow a live internal one, or the reverse. Staleness is evaluated on the chart-owned map alone, so an internal upsert cannot clear the alarm that fires when vendor prices move.

**RBAC**: both are operator-writable only by default. A tampered table skews the gateway tier **and the receipt tier together**, since both price from it — so the receipt tier is **no longer** describable as a backstop for pricing at all, and §6 states that rather than implying protection that does not exist.

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
| Gateway Deployment not observable (absent, or installed out-of-band) | `BudgetEnforcementDegraded` with reason **`ReplicaUnverified`** — the declared divisor cannot be checked, so the `÷ replicas` bound is unverified rather than wrong. **Scoped to `gateway.enabled: true` with at least one rate actually derived**: at P1 the compiler does not run and no limiter exists, so a divisor cannot be unverified there and the Agent carries `GovernanceSkipped` alone. An earlier draft called this "the P1 state", which would have put both conditions on every stock Agent and left an operator unable to tell a tier choice from an incident |
| Policy mutated out-of-band | SSA ownership conflict → re-assert + event |
| Spend aggregation unavailable (design 04 down) | Gateway approximation tier still enforcing; `BudgetEnforcementDegraded` with reason **`AggregateUnavailable`** — distinct from `ReplicaSkew`/`ReplicaUnverified` above, because the remediations share nothing: one is a restart, the others a flag. A bare condition with three causes and one message is rule 8's loud-and-wrong |

## 6. Security

Emitted policies are the only capability-change path (admission denies non-operator writes on plume-labeled gateway resources). Fail-closed apply ordering (§3.3) removes the unauthenticated/unbudgeted windows **on the create path**; it does not remove them on a change to a serving route, which is why A19 withdrew in-place tightening entirely and routes tightening through the revision gate instead. Scope header is added by the gateway *after* authn and cannot be supplied by clients (transform strips inbound occurrences). Pricing ConfigMap RBAC (§3.5) — and **no backstop**: both tiers price from that table, so tampering skews them together (ADR-0028).

## 7. Observability

Compile duration, resource-set size, diff churn, conflict rejections, **policy-acceptance wait time**, `PolicyApplyIncomplete`/`BudgetEnforcementDegraded` counts. Events on source CRs list touched resources.

## 8. Testing

Golden files (incl. max-length names, multi-provider budgets). Property tests: emitted policies per target are field-disjoint; every `RouteClass` has a registry entry; and each entry is pinned behaviourally — remove a mandatory entry's input, assert the route is withheld (§3.3.1). The table is checked by golden render, not by a property test: markdown is not a machine-readable artifact, which is why §3.3.1 replaced that promise. e2e (k3d): budget 429 + receipt; tool filter; candidate route rejects non-SVID; public expose requires OAuth; **plus (r1 f11)**: synthetic-receipt backstop drive → `BudgetExhausted` + weight-0; `PricingStale` surfacing; deliberate bad policy → routes withheld + `PolicyApplyIncomplete`. R1 needs no reproducing test — it is closed by source (§3.2).

### 8.1 What ships at P1, and what does not (A2)

The battery above needs agentgateway, SPIRE, design 04's receipt pipeline and a KG provider. At P1 the chart installs the agent-operator and nothing else (`charts/plume/Chart.yaml`), so most of it cannot run. Stating the split is the point: an unstated deferral becomes a skipped test, and a skipped test reads as a passing one.

| Layer | Ships with this design | Why it can |
|---|---|---|
| unit | `Compile` in full — golden files, name truncation at 63 chars, the §3.5 arithmetic incl. both edge rules, field-disjointness, route-class classification | `Compile` is pure; it needs no cluster |
| envtest | `Apply` ordering, SSA ownership and re-assert, the **staged-reconcile transitions** of §3.3 (each stage advances only on its watch, stale digest or generation restarts the machine), the withhold-on-not-converged path, ordered removal | What this layer uniquely proves is that **the status field path the operator polls exists in the real published CRD schema** — the only defence against a suite that passes against an invented status shape. (That CRDs install without their controller is true of any CRD and proves nothing.) There is no controller to write policy status, so the test writes it: what is under test is the operator's *reaction* to a status, not the gateway's production of one |
| e2e | nothing | needs a data plane |

**Deferred, and named so it is not mistaken for done**: everything traffic-level (429, tool filter, SVID rejection, OAuth on public expose), the synthetic-receipt backstop drive, and R1's same-level/same-field overlap test — all of which land with the agentgateway subchart (design 07). **Until then design 03 has no e2e coverage at all.** The one-concern-per-policy rule stands on diffability alone until R1 runs, exactly as §3.2 and D2 already say.

## 9. Decisions for async review

- **D1 — OSS-only** (unchanged; now cited in the research note).
- **D2 — One concern per policy**, now justified by **nondeterminism in source** rather than by absent documentation: overlapping same-level policies resolve through a randomly-seeded `HashSet`, varying per replica and per restart. R1 is closed (ADR-0028).
- **D3 — Two tiers, neither a ceiling, and no figure** (ADR-0028 + Amendment 1, replacing ADR-0020's D3): the gateway bounds sustained rate and cannot stop a crossing request; the receipt tier is exact only at summing its own `usd_est` estimates, priced from the same table the gateway uses, so it is **not exact in USD and not independent of the approximation**. A spike measured 100 concurrent requests admitting 100× a one-replica budget, so **no figure is published at all** (§3.5).
- **D4 — KG scope is gateway-injected, provider-enforced** across all four fact-bearing tools; body parsing stays out of the gateway (r1 f6).

## 10. Resulting ADRs

**ADR-0028** (2026-08-27) is the live decision — v1.4.1 binding, convergence-not-enforcement, the withdrawn budget ceiling, no token burst, one-concern-per-policy as load-bearing, egress as Backend construction. It **supersedes ADR-0020**, whose content is frozen and whose "signed header" wording carries an erratum (A5).

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
- **A13 (2026-08-27) — the overshoot bound is withdrawn, refuted by measurement.** A9's replacement for the withdrawn "cuts early, never late" was `gatewayReplicas × maxOutputTokens(model)`. An execution spike against v1.4.1 measured it: against a 1,000-token hourly budget where each request consumes exactly 1,000 tokens, **20 concurrent requests all succeeded, then 100 concurrent requests all succeeded** — 100,000 tokens admitted on **one** replica. Serial traffic is limited correctly (429 once spent), so the limiter works; concurrent traffic is not limited at all until the first response lands, because the check is read-only before dispatch and the decrement happens after. The excess tracks concurrency, nothing constrains concurrency, and so **no bound is published**. This is the third budget guarantee this design has retracted and the first retracted by measurement rather than review — which is the argument for the spike, not against it. Bounding it needs a plume-side concurrency cap and request-size maximum that the CRD surface cannot express; that is owed and undrafted.
- **A14 (2026-08-27) — the NACK branch is measured, and it is the fail-open one.** Research §9 left open whether a NACK'd policy retains the previous config or fails closed; §3.3.2 was specified but not safe to build without it. An execution spike forced the case with `burst: -1` — a value the API server and the controller both accept and the dataplane rejects — and observed the answer: the proxy NACKs, **the old configuration keeps serving**, and every Kubernetes-visible signal reports converged at the current generation. A tightening from 1000 req/hour to 1 req/hour never applied while the CR said `Accepted=True, Attached=True`. This is Codex r2 BLOCKER 2 reproduced end to end. The Event is the only observable and is Gateway-scoped; its key attributes a NACK to the emitting policy and route but carries no generation, so it can raise degradation and cannot confirm success. §3.3.2 now says exactly that, and the update transaction is unblocked to the extent that it can be honest about what it cannot prove.
- **A15 (2026-08-27) — the apply barrier is an event-driven state machine.** §3.3 polled for up to 30s on the reconcile worker. Each not-yet-converged Agent held a worker for the full bound and requeued, so a principal who may create Agents could keep more permanently-pending objects than there are workers and starve kill, budget-hold and finalization behind them; `MaxConcurrentReconciles` changes the number of bad objects needed, not the outcome (Codex r2 BLOCKER 7). The operator now applies once and returns, with `status.apply = {digest, generation, stage, deadline}` and watches driving progress. Per-key serialization is untouched — what is removed is the sleeping. Two rules make it safe: **stale state is discarded before anything advances** (a recorded stage whose digest or generation is not current is dropped and the machine restarts, or reconcile N's stage would let N+1 publish a route policied by the previous generation — the same fail-open by another route), and **the deadline sets a condition rather than abandoning**, so withheld routes stay withheld. The NACK Event is a sixth input and cannot advance anything: it proves a rejection happened, never that one did not (A14). Fairness is required as a *test* — more pending Agents than workers while an unrelated kill completes within a stated bound.
- **A16 (2026-08-27) — the egress entry grammar, the catalog, and requested-⊆-permitted.** A11 settled that egress is enumeration and left the language undefined, which Codex r2 BLOCKER 4 correctly called unimplementable. Entries become **typed** (`provider` + optional `models`, or `host`), because a bare string is ambiguous exactly where it decides reachability. The provider→endpoint **catalog is plume's**, chart-shipped and versioned like the pricing table, since agentgateway ships none. And A11's requirement that the Backend's provider set **equal** the allowlist is corrected: that conflated the Agent's **requested** providers with the **ceiling** the allowlist expresses, so a profile permitting `{A,B,C}` for an Agent requesting `{A}` would have made B and C reachable. The emitted Backend enumerates the requested set and must be a **subset** of the permitted one; a destination is never added because it is allowed. The DNS limit is stated rather than implied — the predicate is over names, not addresses.
- **A17 (2026-08-27) — the transaction, and the witness that replaces a fail-open arm.** Codex r3 BLOCKER 1 and 2, both of which A15 had created or left.

  **A15 could not complete a cold create.** Its stages applied Backends and Policies, converged, and only then created the route — but the spike measured that a policy whose target route does not exist reports `Attached=False` (§2.1). The stage that creates the route sat behind the barrier that needed it, so a new Agent reached `Converging` and could never leave. `PreparingRoute` now creates the route first, in the attachable-but-not-serving shape §2.6 measured. (`Quiescing`, added here for tightening updates, is **withdrawn by A19** — there is no in-place tightening for it to serve.)

  **Transaction kind is now persisted, because the stages differ.** `Create` costs nothing; `Tighten` quiesces first and the route answers **HTTP 500** for its whole convergence window; `Loosen` keeps the route live. Anything not *provably* a loosening is a `Tighten` — fail-closed by default, because being wrong that way costs availability while the other way serves the old permissive rule. The 500 is documented rather than discovered, since §2.6 measured it and an operator who is not told will read it as an outage.

  **The fail-open arm is deleted.** §3.3.2 said a tightening "must verify by observation — **or accept** that a silent NACK leaves the old rule serving". That second arm is not an option: §2.8 measured exactly that state reporting fully converged while the removed rule stayed callable. It was replaced by a `Witnessing` stage — **also withdrawn, by A19**. The witness could not attribute its own result (every negative outcome has a producer other than the new policy), and its escape hatch pointed policy-surface changes at a revision path that structurally could not mint one.
- **A18 (2026-08-27) — allowlist entries carry endpoint identity, and the flat key is gone.** Codex r3 BLOCKER 4's remainder. A16's `provider:`-or-`host:` entry is refuted by the shipped v1.4.1 CRD: each provider arm carries its own **instance** fields (`azureopenai`: `endpoint`/`deploymentName`/`apiVersion`; `vertexai`: `projectId`/`region`; `bedrock`: `region`/`guardrail`), so two Azure OpenAI resources share one arm, differ in endpoint and deployment, and may differ in BAA status — an arm-level entry must reject a valid endpoint or permit both. A host-only entry carries no port, so two services on one host are indistinguishable. Entries are now `{arm, instance fields}` plus optional `models`, and `egressEnumerated` compares **resolved tuples, not hostnames**. `models` turns out to be genuinely enforceable rather than decorative: every arm carries its own `model` field, so a narrowed set compiles into the emitted provider block, and a model the arm cannot express is a compile error. §3.5's flat pricing key and its longest-prefix rule are deleted with the string they resolved — design 02 A24 types `providers` and `fallback` to the same identity, which also closes Codex r3 MAJOR 4's non-injective join. **Owed**: the pricing ConfigMap is still keyed on the flat string.
- **A19 (2026-08-27) — there is no in-place tightening, and A17's witness is withdrawn.** Codex r4 BLOCKER 2 and 4. The witness A17 introduced to replace a fail-open arm **cannot establish causation**: a 401 can come from the backend, a rejected tool from an unhealthy MCP server, an unreachable host from DNS, a 429 from an upstream or an already-empty bucket — so a NACK that retains the old permissive rule coexists with a passing witness, and `Publishing` widens the match on a false positive. Its escape for an unwitnessable concern — "use the revision path" — was structurally unavailable for exactly the fields that needed it, because a policy-surface change mints no candidate. A witness that cannot attribute and a fallback that cannot exist is not a mechanism.

  So tightening leaves the compiler. Design 02 A25 moves `budget` and `expose` — the only two policy-surface fields feeding a mandatory concern — to the behaviour surface, and every tightening becomes a spec change gated on a candidate route by machinery that already exists and already works. `Quiescing` and `Witnessing` are deleted; `Create` and `Loosen` remain. **The serving route is never mutated**, which dissolves the live-route-withdrawal question r4 BLOCKER 1 raised and the stale-restart-skips-a-safety-stage question in BLOCKER 3, rather than answering them.

  Also fixed here: A18 asserted "every arm carries its own `model` field" **twenty lines below its own table showing `azureopenai` does not** — the pattern this review set exists to catch, in the sentence claiming to be grounded in the CRD. A `models`-scoped entry for `azureopenai` is now a compile error unless a non-v1 `deploymentName` supplies the constrained identity.
- **A20 (2026-08-28) — evaluation order is removed, not softened.** The AuthN row asserted a native `auth → rate-limit → guards` order **while the Guards row two rows below disclaimed the same ordering**; both cannot be design. The assertion came from a blog post carried by the superseded research note, and the v1.4.1 re-verification listed it among the claims it could *not* establish. New §3.4.3 states why the answer matters — if the limiter runs first, a flood of unauthenticated requests exhausts a paying agent's budget, a denial-of-wallet needing no credential — and writes the discriminating experiment down so it is reproducible rather than re-derived. No row states an order until it runs.
- **A21 (2026-08-28) — the pricing table is keyed on endpoint identity, and is two ConfigMaps.** A24 deleted the `<provider>/<model>` string everywhere else and left the pricing table as the last thing requiring the non-injective join, so `{azure, openai/gpt-4}` and `{azure/openai, gpt-4}` still collapsed to one row with potentially different prices and BAA status (r4 MAJOR 8). Rows are now a list matched most-specific-wins on the tuple, and an equal-specificity tie is a **load error** rather than a silent per-request choice. Separately (r4 MAJOR 7), the chart and the model-operator cannot own rows inside one YAML scalar — Kubernetes field ownership stops at `data.models` — so external and in-cluster prices live in **two** ConfigMaps with an identity appearing in both being a load error, and staleness evaluated on the chart-owned one alone so an internal upsert cannot clear the vendor-price alarm.
- **A22 (2026-08-28) — propagation sweep for r4 MAJOR 6 and 16.** `PolicyIntent` still showed the flat `llm` shape A24 replaced; the `ReplicaUnverified` row called itself "the P1 state" when P1 runs no compiler and therefore has no divisor to leave unverified, which would have put a degradation condition and a tier-choice condition on the same stock Agent; design 02's reconcile outline still told implementers admission had "already enforced" a signed image, which A21 says nothing does. Each was in an authoritative body while the amendment log said otherwise — the failure mode that recurs in every round of this review set, which is why the docs gate now normalises formatting (r4 MAJOR 19) rather than matching raw bytes.
- **A23 (2026-08-28) — the mandatory-entry test may not be generated from the registry.** r4 MAJOR 10. An earlier draft pinned each entry with "for every registered class, one test per mandatory entry", which deletes its own check along with the entry — the identical defect this repo has now shipped **twice**, once in the docs gate (removing a rule left the suite green) and once here. Cases enumerated from production data cannot pin production data. The security cases are now a **static catalog**, reviewable as a list, failing in both directions: a registered class with no case, and a case naming an entry the registry no longer has. `test/docs/superseded_test.go`'s `ruleFixtures` is the working precedent, and the mutation harness requires a **compiled** failure — a build error is INVALID, not KILLED.
- **A24 (2026-08-28) — the last of r4's contract gaps.** Five holes where a sentence read as a rule but could not be implemented from what it said.

  **An empty slice meant two things** (MAJOR 1). A resolved `tools: []` represented both "requested none" and "requested one and its producer failed" — opposite required outcomes, identical to a pure function, so an Agent asking for a tool it never got could compile as an Agent asking for none and report `Ready`. Bindings now carry `{requested, resolved?, state}` over `Resolved | ProducerAbsent | Unresolvable`, and the three are pinned independently.

  **"Admitted set" is not a comparator** (MAJOR 3). Rate limits are stateful quantities, transforms are functions, timeouts are neither — so "every concern's admitted set is a superset" was not implementable and two reasonable implementations would classify the same edit differently. Replaced by a **closed per-concern registry** with an explicit ordering each, and **Unknown → `Tighten`**: a concern with no comparator entry, a comparison the registry cannot make, and a first application all fail safe. Property-tested for reflexivity and transitivity.

  **Route failures had no aggregation contract** (MAJOR 11). One Agent has one `Ready`, one phase and one condition per type, so concurrent route failures overwrote each other's message and recovering one hid the other. Routes are now **serving-required** or **optional**, with a table for `Ready`/phase/CLI, a stable ordering in the message so a second failure appends, and clearing only when the failing set is **empty**.

  **MCP emission was not total at the CRD's bounds** (MAJOR 13). `minItems: 1` means an empty allowed-tool set has no empty-list representation, and omitting the policy would restore upstream's default-allow — turning "no tools" into "every tool". It now emits a single `false`. Over 256 tools is a compile error rather than a silent truncation, and names go through a real CEL encoder because a broken expression denies everything under `Allow` and would fail **open** under `Deny`.

  **`captureLevel` was per-Agent and unrepresentable** (MAJOR 14). Frontend tracing attaches only to a Gateway at v1.4.1, so two reconcilers would last-writer-win one policy — a per-Agent field silently deciding a cluster-wide setting. It leaves `PolicyIntent`; tracing is emitted once per Gateway from a chart value.

  **Virtual models were the mandatory shift path** (MAJOR 15). ADR-0028 records `AgentgatewayModel` as experimental and **off by default**, yet design 03's mapping table and ADR-0026 required it for weighted shifting — an opt-in API as a core rollout mechanism, absent from a default install. Now Gateway API `backendRefs` weights, which design 02 §3.3 already uses for revision weights; virtual models are an opt-in enhancement (design 25 A2).
- **A26 (2026-08-28) — the intent is per-revision, and stale recovery re-enters the transaction's first stage.** Codex r5 BLOCKER 1 and 2.

  **A25 was not representable by the compiler input.** `PolicyIntent` carried `revisions[]` beside a **single** `budget`, `expose`, `tools` and `llm` at Agent scope — so once A25 put `budget` and `expose` on the behaviour surface, the one thing the intent could not express was the state A25 exists to create: an active revision serving its own policy while a candidate is built with a different one. A pure compile either rewrote R1's serving policy before R2 was gated, letting a widening reach production **ungated**, or preserved R1 and could not compile R2. The fix that closed the tightening hole could not run. The intent is now **one per revision**, the applied `ResourceSet` is persisted under its revision hash, an active revision's resources are **never reconstructed from current spec**, and the receipt backstop reads the *active* revision's budget — otherwise raising a held candidate's budget would clear the active revision's `BudgetExhausted` hold while the spend had not moved.

  **A19's claim that r4 BLOCKER 3 "dissolved" was false, and the retraction matters more than the fix.** Removing `Tighten` removed a transaction; it did not remove `Create`'s first stage. Stale recovery still restarted unconditionally at `ApplyingBackends`, so a superseding generation naming a new candidate route would attach policies to a route that had never been created — `Attached=False`, the cold-create deadlock A17 was written to fix, reached by another path. Recovery now recomputes the transaction against the currently serving applied set and enters *its* first stage, with the route identity persisted alongside the stage. I asserted a finding was dissolved without checking; the review found it, twice in a row, in the same section.
- **A27 (2026-08-29) — r5 majors 1–4, 8 and 10, in the body rather than the log.** MAJOR 1: the resolution state A26 described in prose was **absent from the type** — `PolicyIntent` still carried bare resolved slices, so an implementer reading the authoritative input still could not tell "requested none" from "resolution failed". Bindings are now `Binding<T> = {requested, resolved?, state}` in the type itself. MAJOR 2: the comparator registry promised an ordering per concern and enumerated none; each is now listed, with **normalization to effective values** so a field gaining its schema default is not read as a change, and first application stated as `Tighten`. MAJOR 3: classifying **every** tool, KG and LLM route as optional reported `Ready=True` for an Agent whose sole knowledge source was gone — contradicting architecture §04 and design 02 §5, both of which say that agent gets no traffic. Requiredness is now a property of the binding, aggregated over the Agent's actual contract. MAJOR 4: every serving-required failure was reported as `PolicyApplyIncomplete`, naming an apply failure when the cause was a compile error; conditions now aggregate **by cause** with a stated precedence. MAJOR 8: the static security catalog pinned the base registry only, so deleting a compliance profile's `-guard` extension left every base case green — each profile now ships its own catalog and fails to install without one. MAJOR 10: §3.5 opened with one ConfigMap, called tuple-keying "owed", and had the model-operator writing into the chart-owned map — the three defects A21 claimed to close, still in the section A21 amended.