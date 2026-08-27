# Design 02: Agent CRD + agent-operator

- **Status**: **integrated r3** — amendments A1–A19 folded into the body. A15–A19 (2026-08-25/26) are **not yet critique-passed**: four rounds returned REVISE (the fourth with 0 blockers) and all findings are addressed, but no pass has returned PASS. A20–A21 are owed and undrafted — each needs a decision, not a correction (§12) (the independent re-critique asked for one integration pass rather than a rule telling implementers which half to believe). Amendment history is preserved in §12 for provenance only; **the body is authoritative**. · ADR-0019
- **Phase**: P1 · **Size**: L · **Date**: 2026-08-20, integrated 2026-08-22
- **ADRs**: 0002, 0003, 0006 (gating), 0016 (expose/apps), 0019 (this design), **0028** (compiled policy — supersedes 0020), 0025 (governance) · reviews: `reviews/02-review.md`, `reviews/02-recritique.md`

## 1. Purpose & scope

The Agent CRD is the platform's front door: one object that turns "a container that speaks A2A" into a governed, discoverable, eval-gated workload. The agent-operator reconciles it into workload, identity, directory entry, gateway routes/policy, and rollout gating.

**In scope**: CRD schema, reconcile behaviour, the revision/rollout state machine, registration, identity wiring, external agents, **finalization**.
**Out of scope**: policy compilation internals (design 03), receipt tap (04), eval execution (16 — only the *holding mechanism* here), KG binding validation details (01/13).

## 2. Doctrine & charter gates

- **Plane**: slow (engine) — one of the three core operators.
- **Pods added**: the agent-operator itself (the single plume-code pod in the §17 budget). Workload pods are user workloads.
- **Stateful deps**: none. Operator state = CR status + JetStream KV (directory). One additional **read-only** reconcile input: design 04's per-agent daily spend aggregate (the exact-tier budget backstop) — that state lives in design 04's audit index, not here.
- **Primitives**: Agent (A2A), Tool (MCP), Resource (CRD), Artifact (OCI images). ✓

## 3. Interfaces

### 3.1 CRD schema (`plume.dev/v1alpha1` — group finalized at rename, ADR-0001)

```yaml
kind: Agent
metadata: {name: pa-reviewer, namespace: claims}
spec:
  runtime:                       # exactly one of image | external
    image: ghcr.io/acme/pa-agent@sha256:3f9a…  # digest required (CEL, A21); signature NOT verified today
    replicas: 2                            # >1 ⇒ Deployment; see §3.2 task-state rule
    port: 8080                             # A2A server port (default 8080)
    sandbox: {profile: gvisor}             # optional ⇒ kind=Sandbox (singleton)
    resources: {…}
    env: [...]                             # non-secret; secrets via envFrom
  # external: {endpoint: https://…, auth: {oauthClientRef: …}}
  card:
    path: /.well-known/agent-card.json     # served by the container (SoT = code)
  knowledge:
    - name: payer-policies
      version: "v12"
      scope: {entityTypes: [Policy, Procedure]}   # gateway-INJECTED, provider-ENFORCED (design 01 A1)
  tools:
    - name: claims-system                         # a Connector tool facet OR an MCPServer CR;
                                                  # names are unique across both kinds in a
                                                  # namespace (admission), so no kind discriminator
                                                  # is needed — and none may be cross-namespace
      requiresApproval: false                     # true ⇒ approval interceptor (design 22)
  llm:                                            # designs 03/20/25 compile against this
    providers:                                    # typed since A24; see below
    - {arm: openai, model: gpt-x}
    - {arm: custom, model: pa-classifier, endpoint: {host: pa.svc, port: 8000}}
    egressAllowlist:                              # typed entries (A22/A24); profiles may pin (ADR-0014)
    - {arm: anthropic}
    - {arm: openai, models: [gpt-4o]}
    fallback: {arm: custom, model: pa-classifier, endpoint: {host: pa.svc, port: 8000}}  # design 20
  budget: {tokensPerDay: 2000000, usdPerDay: "40.00", taskTimeout: 10m, maxHops: 8}
                                                  # per-day windows reset 00:00 UTC; remaining in status
                                                  # usdPerDay is a decimal STRING: ^[0-9]{1,6}(\.[0-9]{1,6})?$ (A15, six digits per A18)
                                                  # tokensPerDay is an int64 — "2M" is not valid YAML for one
  loop: {allowReentry: false, maxVisits: 1}       # occurrence-counted reentry (design 22)
  gates:
    - evalSuiteRef: pa-regression                 # ≥1 required in prod iff EvalSuite CRD installed
  expose:
    a2a: {visibility: org, auth: oauth}
status:
  phase: Pending|Held|Canary|Ready|Degraded|BudgetHeld|Killed
  activeRevision: pa-reviewer-7f3a2
  candidateRevision: pa-reviewer-9c1d4            # at most one in flight
  supersededCandidates: [pa-reviewer-4b8e1]       # abandoned candidates, auditable
  cards:                                          # ONE PER LIVE REVISION — two coexist during rollout
    - {revision: pa-reviewer-7f3a2, name: …, version: …, fetchedAt: …, digest: …, signed: true}
    - {revision: pa-reviewer-9c1d4, name: …, version: …, fetchedAt: …, digest: …, signed: true}
  budget: {tokensRemaining: …, usdRemaining: …, windowResetsAt: …}
  conditions: [Registered, CardUnsigned, IdentityIssued, IdPUnavailable,
               OnBehalfOfUnavailable, IdentityBootstrapIncomplete,
               KnowledgeBound, GatesPassed, GatesSkipped, GatesBypassed,
               SandboxDowngraded, TaskStateUnverified, ScratchpadDegraded,
               BudgetExhausted, BudgetEnforcementDegraded, PricingStale,
               ReceiptsDegraded, Killed, Ready, Progressing, Degraded,
               PolicyCompileFailed, PolicyApplyIncomplete, GatewayIncompatible,
               ModelDrifted, GovernanceSkipped, EnvSourceUnresolved,
               EnvSourceProtectionUnavailable, RevisionMaterialChanged]
```

`kubectl get agents` printer columns: `PHASE · ACTIVE · CANDIDATE · EVAL(last score) · COST/DAY · AGE` (A14), sourced from `status.phase`, `status.activeRevision`, `status.eval.score`, `status.budget.usdSpentToday` and the creation timestamp. The set is a contract, pinned by `TestPrinterColumnsMatchTheDesign`: the case for a twenty-nine-condition status rests on these five answering the common questions, and `EVAL`/`COST/DAY` are precisely the two a developer could otherwise reach only by reading conditions.

**Tool and graph names resolve in the agent's own namespace, and only there.** A tool name may be served by a Connector tool facet or by an `MCPServer` CR; the two share one namespace-unique name space, enforced at admission (design 11 §4), which is why the binding carries no kind discriminator. There is deliberately no `namespace` field on either binding: design 24 §4.1 derives the `can_call` tuple **from** the binding, so a cross-namespace reference would authorize itself — anyone able to create an Agent in one namespace could reach a tool in another. Cross-namespace use requires consent published by the target namespace (the `ReferenceGrant` shape) and is out of scope until a design specifies it.

### 3.2 Workload materialization

- **Default: Deployment** — A2A servers are stateless HTTP; replicas scale; fits every SDK.
- **`runtime.sandbox` ⇒ agent-sandbox `Sandbox` (v1beta1)** — singleton, stable identity, persistent scratchpad; for agents that execute code or hold local state. `replicas>1` + `sandbox` is an admission error.
- **Sandbox runtime class absent** (local distros) ⇒ hardened Deployment (seccomp, non-root, read-only rootfs) + `SandboxDowngraded=True`. Never silent (NFR-8).
- **Scratchpad lifecycle**: the volume is **revision-independent** — reattached read-write to the promoted revision. A candidate under eval attaches it **read-only**, so a cold candidate cannot corrupt live state. This needs an access mode permitting concurrent attach (`ReadOnlyMany`/`ReadWriteMany`) or co-scheduling; where the storage class cannot provide it — notably `local-path` in the `local` profile, which is `ReadWriteOnce` and node-bound — the candidate runs **without** the scratchpad and `ScratchpadDegraded=True` names the reason. Eval consequence, stated: design 16 gates a **cold** candidate against a warm active; suites must not assume warm local state.
- **Task state with `replicas>1`** — detected, not assumed: the A2A card is the declaration point (a capability/extension asserting shared task state). Absent that assertion with `replicas>1`, the operator sets **`TaskStateUnverified=True`** with the consequence named, and the CLI warns at deploy. Admission may require the assertion for `expose: public`. Design 09's templates ship the JetStream-KV store **and emit the declaration**, so template-built agents never raise it.

### 3.3 Revisions & the holding mechanism (entailed by ADR-0006)

k8s rolling update mixes old/new traffic and would defeat eval gating, so the operator owns revisions:

1. Spec change → `revisionHash(spec)` — **spec only**; the card digest is status, and card drift triggers re-registration, never a new revision → creates a parallel workload `<name>-<hash>`.

   **The hash covers a projection of spec, not all of it (A12, r2).** Two surfaces share one CR:

   - **Behaviour surface** — a change to **what the agent can do, produce, or reach**. It must pass a gate, so it mints a revision.
   - **Policy surface** — a change to **how much, how fast, or who may call**, without changing capability. It is applied in place.

   The discriminator is *capability*, **not** the compile path: `knowledge[].scope`, `tools[].name` and `llm.providers` all compile to gateway resources via §3.6 and are all behaviour-surface, because narrowing what an agent may retrieve changes what it answers exactly as removing a tool does. An earlier wording defined the policy surface as "whatever §3.6 compiles", which would have classified those three as policy and reintroduced the very under-gating this amendment exists to prevent.

   Hashing the whole spec would make `replicas: 2 → 3` pay for an eval-and-canary cycle; hashing too little would let an operator swap the model or the knowledge-graph version under a running agent **with no gate**, hollowing out ADR-0006 and ADR-0005 together.

   | Behaviour surface — **mints a revision** | Policy surface — **applied in place** |
   |---|---|
   | `runtime.image` | `runtime.replicas` — a scale operation |
   | `runtime.env`, `runtime.envFrom` (by **referent**, not contents) | `runtime.port` — wiring; the operator dials it |
   | `runtime.sandbox.profile` | `runtime.resources` — see the note below |
   | `knowledge[].name`, `.version`, `.scope` | `card.path` — a path change is re-registration, exactly as card drift is |
   | `tools[].name` — a capability grant | `tools[].requiresApproval` — approval policy |
   | `llm.providers`, `llm.fallback`, **`llm.egressAllowlist`**, **`budget`** (A25), **`expose`** (A25) | `gates`, `loop` |
   | `external.endpoint` | `external.inlineCard` — a description, not a grant |
   | `external.oauthClientRef` — **identity**, see below | |

   Five rules that are easy to get wrong:

   1. **`external.oauthClientRef` is behaviour, not "credential rotation".** It selects *which client* an external agent authenticates as — the principal design 24 §3 keys `can_invoke`, `can_call` and the act-chain check on. Repointing it reaches a different set of tools. (Rotating the credential *behind* a client is a Secret update the hash never sees, which is correct and is what the earlier wording confused it with.)
   2. **Env sources are hashed by CONTENT, not by referent (A20, revised).** Every `ConfigMap` and `Secret` reachable through `envFrom` or `env[].valueFrom` contributes a digest of its resolved contents to the projection. Referent identity alone was never sufficient: anyone with `update` on a referenced object could replace a system prompt or a provider endpoint, the pod would restart, and the new behaviour would serve under the old revision and the old gate result — with no permission to touch the Agent at all.

   **The first fix for this was wrong and is retracted.** A20 originally required referenced ConfigMaps to be `immutable: true`, reasoning that a change would then need a *new* object and therefore a new referent. Kubernetes says otherwise: *"Once a ConfigMap is marked as immutable, it is not possible to revert this change nor to mutate the contents … You can **only delete and recreate** the ConfigMap."* Delete-and-recreate keeps the **same name**, so the referent and the hash are unchanged and the bypass survives untouched. Immutability binds contents to an *object*; it never bound a name to contents.

   **Hashing detects a change; it does not stop the serving revision consuming one (A23).** Content hashing mints a *candidate* when a source changes, but the retained active workload still references the object **by name**, so a node drain recreates an R1 Pod that reads the new content under R1's gated hash and route — no Agent write required. The digest recorded in status protects nothing by itself. So every referenced source is **sealed** for as long as a retained revision holds it: a `plume.dev/env-source-protection` finalizer plus a chart-shipped, fail-closed `ValidatingAdmissionPolicy` denying data change, delete, label removal and finalizer removal by every principal except the operator. **No bytes are copied.**

   **The exemption question is settled by fact, not by preference.** An earlier version said rotating a value inside a Secret is not a behaviour change; that exempted the bypass, since `envFrom.secretRef` carries a system prompt exactly as well as an API key. But the cost of removing it was also overstated here: **Kubernetes does not refresh a process environment when a ConfigMap or Secret changes.** `envFrom` and `env[].valueFrom` require container replacement either way — only *mounted volumes* update live, and this API does not expose them. There was never a no-restart rotation path on this surface to lose. What genuine credentials need is a **typed credential binding** whose stable facts (issuer, principal, audience, scope, destination) mint a revision while short-lived bytes rotate under a trusted issuer; that field does not exist yet and is owed (A23). Any union arm the implementation does not name explicitly must hash **injectively** (over-gate), never collapse to a constant — a constant let `fileKeyRef` repoints through ungated.
   3. **List order is not semantic.** Nothing in the corpus treats tool or provider order as meaningful — design 03 compiles tools to a set-semantics filter and providers to a commutative max-price — while kustomize, helm and `kubectl apply` round-trips all re-serialize lists. The projection therefore **sorts** `tools`, `llm.providers` and `knowledge`, so a re-serialized manifest does not re-gate. `env` keeps its order, which is semantic for interpolation.
   4. **`llm.egressAllowlist` is behaviour, and was in neither column (A16).** It is the set of endpoints an agent may reach, so widening it changes what the agent can do — the same argument A12 already makes for `knowledge[].scope`, where narrowing what an agent may retrieve changes what it answers exactly as removing a tool does. It is also the control ADR-0014's compliance profiles pin, so an ungated edit would let an operator widen egress on a HIPAA cluster with no eval and no gate. Design 03 §3.1 consumes it, which is how the omission surfaced. The gate is **symmetric** — any change to the set mints a revision — and compared as a set, since the projection already sorts `llm.providers`.

   **A17 briefly made this gate asymmetric and A18 retracts it.** The argument was that revoking an endpoint that lost BAA coverage would queue behind an eval cycle on every agent, so removal should apply in place while addition mints. It was wrong three ways, and the first is the one that matters: **`revisionHash(spec)` is computed from spec alone** (`internal/revision/revision.go:12`), and "is this set a subset of the previous one" needs `(old, new)`. The predicate is not expressible in a content-addressed digest without giving up the content-addressing §5's "no double-create" and instant rollback both rest on. Second, it laundered a widening: allowlist `{A}` gated and serving as R1 → edit to `{A,B}` mints candidate R2, Held at weight 0 → edit to `{B}`, a removal relative to `{A,B}`, applied in place, R2 abandoned by supersession — and R1, the *serving, gated* revision, now reaches `B`, which passed no eval. That is the ADR-0006 hole this rule exists to close. Third, the premise was false: design 27 §5 ships the BAA allowlist as a **pack filter** (`egress-allowlist.yaml`, `compliance` priority band), and ADR-0014 puts it "enforced at the gateway" — so a BAA list change is a pack/values change compiled into the LLM Backend at profile scope, not 200 Agent edits. The 200-candidate scenario cannot arise, so nothing was bought for the hole it opened.
   5. **`runtime.resources` is policy for a specific reason.** "Capacity, not behaviour" is too glib — an OOM-killed agent fails tasks and CPU throttling changes `taskTimeout` terminations, both observable in `task_completion`. The real argument is that resource regressions are caught by **design 20's behavioural drift path** (SLO burn on success rate → `Degraded`), and gating capacity changes would block incident response — the same reasoning that exempts fallback activation below.

   **Classification is compulsory, not defaulted, and it is LEAF-level (A16).** Every `AgentSpec` field is named in exactly one column, and `TestEveryFieldIsClassified` reflects over the struct to prove the table is exhaustive: adding a CRD field fails the build until someone classifies it and amends this table.

   That test walks **top-level** `AgentSpec` fields only, which is the wrong shape and made the exhaustiveness claim false. The table's rows are top-level names (`llm`, `runtime.env`), but behaviour lives in leaves several levels down inside embedded Kubernetes structs, and the projection drops leaves the table never named. Confirmed by direct differential mutation — two specs differing in exactly one leaf, same revision hash:

   | Leaf | Reaches the running pod? | Found by |
   |---|---|---|
   | `llm.egressAllowlist[]` | no — it reaches the **gateway**, and it is the ADR-0014 egress control | Codex M5; independently reproduced |
   | `runtime.envFrom[].configMapRef.optional` | yes | Codex M3 |
   | `runtime.envFrom[].secretRef.optional` | yes | Codex B1 |
   | `runtime.env[].valueFrom.resourceFieldRef.divisor` | yes | Codex M4 |
   | `runtime.env[].valueFrom.secretKeyRef.optional` | yes | Codex B1 |
   | `runtime.env[].valueFrom.configMapKeyRef.optional` | yes | Codex B1 |
   | `runtime.env[].valueFrom.fieldRef.apiVersion` | yes | Codex B1 |

   So the rule is: **classification and projection are over the transitive leaf graph of `AgentSpec`, not its top-level fields.** A table row may still name an aggregate (`runtime.env`), and the aggregate's classification is the **default** for every leaf beneath it — but an explicitly classified leaf **overrides** its aggregate, and the override must appear in the table above.

That precedence is not academic. `tools` is a behaviour aggregate and `tools[].requiresApproval` is in the policy column; `external` is behaviour and `external.inlineCard` is policy. Without the override rule, "the aggregate binds every leaf" and "fails unless the leaf is explicitly classified policy" give opposite answers for both, and toggling an approval flag would pay an eval-and-canary cycle. Overrides exist only for leaves of plume's own types; **no leaf inside a third-party struct is overridden today**, and one would have to be added to the table like any other.

   **The test must reflect over PATHS, and enumerate over TYPES.** These are `corev1` structs that gain fields between Kubernetes releases, and the trap has been sprung once already: A12 rule 2 records `envSourceRef` naming four arms while k8s v0.36.4 had five, so `fileKeyRef` collapsed to a constant and repointing `prod.env → staging.env` minted no revision. A hand-maintained *path* list reintroduces that on the next dependency bump, so paths are always discovered by reflection.

But reflection alone cannot perturb every leaf, and a walk that silently skips what it cannot reach is worse than no walk. **`resource.Quantity` holds its value in unexported fields**; the only reflect-settable leaf beneath `env[].valueFrom.resourceFieldRef.divisor` is `Format`, a display hint. A critic demonstrated the consequence: projecting `Divisor.Format` alone turns the leaf walk **green** while Codex M4 (`divisor: 1m` vs `1`) still hashes identically. The mandated shape was satisfiable without doing its job — the same defect this amendment set killed twice already, relocated.

So: a **type-keyed perturber registry**, and it **fails closed** — a type the walk reaches that it cannot perturb through a public API **fails the test** rather than being skipped. A registered perturber also **terminates the walk at that type**; without that the walk descends into `resource.Quantity`'s internals and the registry has to carry `math/big.nat`.

The graph was enumerated rather than estimated, and it corrects this amendment's first draft on every count. `AgentSpec`'s transitive graph has **68 type nodes, 38 named, depth 10, no cycles and no interface leaves** — so it terminates. (A leaf *count* is deliberately not stated: it depends on whether the walk terminates at a registered perturber's type or descends into it, and two independent walks produced different figures under different rules. The counting rule is what matters and it is above; the number would only look authoritative.) **Exactly one type carries unexported state: `resource.Quantity`**, at three paths, one of which (`env[].valueFrom.resourceFieldRef.divisor`) is on the behaviour side and therefore required today, not hypothetical. Of the four types the first draft named, three were wrong: `metav1.Time` and `intstr.IntOrString` are **not in `AgentSpec`'s graph at all**, and `metav1.Duration`'s `time.Duration` is exported and reflect-settable. A registry written literally from that text would have had four entries, one load-bearing, two pointing at types that never appear — and would have looked complete.

**The path walk is what catches drift; the registry is not, and the first draft claimed otherwise.** Measured across `k8s.io/api` v0.28 → v0.36: **+1 named type and +5 leaves in eight minor releases**, and **zero new unexported-state types**. The one arrival — `corev1.FileKeySelector` in v0.34 — has all-exported fields, so a registry keyed on unexported state would have stayed *silent* on exactly the class of regression A12 rule 2 records as having already shipped. Reflected paths caught it. So the claim is narrowed to what is true: enumerating **paths** is unsafe and they are always discovered; enumerating **types** is safe *because the set is one*, and it exists to stop a silent skip, not to announce a new field.

**Map-kinded leaves.** `ResourceRequirements.Limits/Requests` are `map[ResourceName]Quantity`. A map-kinded leaf is perturbed twice — over its key set (`{cpu:1} → {memory:1Gi}`) and over its value set (`{cpu:1} → {cpu:2}`) — and either must mint if the leaf is behaviour. The value perturbation *is* a `Quantity` perturbation, so the map rule composes with the registry rather than sitting beside it. Both map paths hang off `runtime.resources`, which is policy, so they are moot today — but "the walk prunes there" was doing double duty in the first draft: the **classification** walk must reach a field to classify it, so pruning is an output of classification and not an input, and it is the **projection** that never descends.

**One precondition, stated because the test shape depends on it**: `AgentSpec` must not gain a self-recursive type. `apiextensions-apiserver` is already a direct dependency and its `JSONSchemaProps` has twelve self-recursion points, which would hang a walk with no type stack. Adding such a field is a design decision, not a schema tweak.

   **A cheap reflection sweep is not this test.** One was run over the current projection: it reached 31 leaves, correctly showed the other 16 non-minting leaves are all genuine policy surface (`budget.*`, `expose.*`, `loop.*`, `runtime.replicas/port/resources`, `card.path`, `gates`, `tools[].requiresApproval`, `external.inlineCard`), and confirmed `llm.egressAllowlist` — but its depth cap and pointer seeding never reached `env[].valueFrom.*` at all, so it would have missed four of the seven leaves above. The gap between "a sweep that finds something" and "a sweep that proves absence" is the whole difficulty, and it is why this is a design rule with a required test shape rather than a one-off fix.

   An earlier version of A12 said new fields *default* to policy-surface and called that the safe direction — it is not. A behaviour field added without classification reaches production ungated, silently, which is precisely the ADR-0006 hole; the critic demonstrated it by adding a `SystemPrompt` field and watching the whole suite stay green.

   **Exception, stated because A12's headline claim is otherwise false.** Design 20's `ModelDrifted` remediation activates `llm.fallback` by setting **status**, not spec, so it mints no revision and no eval fires — deliberately, since an eval cycle mid-incident is the last thing wanted. The consequence is that **the model actually answering requests can change without a gate**, as incident response, with design 20's four guardrails (correlation, rate limit, receipt, override) as the compensating control. That is the right design; a reader who concluded from A12 that the serving model can *never* change ungated would be wrong.

   Hashing is over the projection's canonical JSON. The projection struct's field order, JSON tags and `omitempty` are part of the revision contract — `encoding/json` emits in declaration order, so a cosmetic reorder would re-mint every revision in every cluster. `TestGoldenDigest` pins the encoding; a change there is a **migration, not a test edit**.

2. Candidate registers, gets identity, passes readiness — gateway route weight **0** (`phase: Held`).
3. The gate controller (16) runs the EvalSuite against the candidate through the gateway on a **candidate-only route** whose admitted set is exactly the eval run's own SVID, granted at Job launch and revoked at Job end. Eval traffic otherwise uses the identical path as prod.
4. Pass → weights shift (canary steps, default `10 → 100`); `activeRevision` flips; superseded revisions GC'd per `revisionHistoryLimit`.
5. Fail → candidate deleted, report in status + PR annotation; `GatesPassed=False` naming the failing metrics.

**Spec change while a candidate is in flight**: the new generation **supersedes** the old candidate. `C1` drains to weight 0 respecting `taskTimeout` (the drain design 22's kill switch uses), `GatesPassed` resets, the abandoned revision is appended to `status.supersededCandidates` and emitted as an event. At most one candidate is ever in flight.

**`revisionHistoryLimit: 2` means two retained revisions *in addition to* active and any in-flight candidate** — otherwise a rollout would GC its own rollback target and destroy the instant-rollback property ADR-0019 exists for. Design 20's retention hold (pin the last eval-passing revision while `Degraded` is open) is this operator's behaviour and is honoured here.

**Rollback** = re-point to a retained revision (instant, no rebuild).

**Core tier**: the gates-required admission rule applies iff the EvalSuite CRD is installed; on core-only installs rollouts proceed with a loud `GatesSkipped=True`. `local` profile only: `GatesBypassed=DevProfile`.

### 3.4 Registration & card handling

**The card is served by the container — source of truth is the agent's code.** After a candidate is Ready the operator fetches it in-cluster (3 retries, backoff), validates (parseable, name matches CR, A2A version supported, **signature** per design 09: required for plume-built, `CardUnsigned=True` for BYO/external), stores a per-revision digest in `status.cards[]`, wraps it in an OASF record, and writes it to the directory (JetStream KV, `agents.<ns>.<name>.<revision>` plus the `.active` pointer; GC'd with revisions).

- **Card-vs-CR cross-check**: a card advertising a skill that requires a tool or graph the CR does not grant is a registration failure (`Registered=False`) naming the mismatch — otherwise it registers cleanly and fails at runtime.
- **Terminal unregistrable state**: retries continue with backoff for `registrationDeadline` (default 30m); past it the candidate is **failed and deleted**, freeing its retention slot, with the reason recorded. Nothing holds a slot indefinitely.
- **Card drift** at runtime (digest change without spec change) → re-fetch, directory update, event.
- **External agents**: card fetched from the external endpoint; a CR-inline override is permitted only where the endpoint cannot serve one.

### 3.5 Identity wiring

One platform-owned **ClusterSPIFFEID** (spire-controller-manager) with a selector template — no per-agent registrar code:

```
spiffe://<trust-domain>/agent/{{ .PodMeta.Namespace }}/{{ .PodMeta.Labels "plume.dev/agent" }}
```

Pods are labelled `plume.dev/agent` + `plume.dev/revision`; SVIDs delivered via spiffe-csi. The operator additionally provisions a per-agent **`agent-actor` OAuth client** (design 06) so exchanged tokens' `act` chains name real agents. External agents get no SVID — they authenticate with OAuth client credentials. Hard-mode tenancy uses per-tenant SPIRE federated to the host trust domain (design 26).

### 3.6 Gateway handoff (interface with design 03)

The operator normalizes spec (tools, budgets, `llm`, expose, KG scopes, revision weights, `gatewayReplicas`) into a `PolicyIntent` and calls the **policy compiler as an in-process library**, which emits agentgateway resources under fail-closed apply ordering; the operator owns their lifecycle (ownerRefs on the host cluster). The gateway CRD surface is pinned by the chart (design 07) at a minimum version carrying OSS token exchange and virtual models.

### 3.7 Finalization

On delete, an operator finalizer gates ordered teardown: **drain traffic** (weights 0, respect `taskTimeout`) → **revoke** routes and policies in design 03's reverse apply order → **deactivate** (never delete) the `agent-actor` OAuth client, per design 06 D3 → **GC** directory entries and SPIRE pod labels → release the finalizer. In-flight tasks drain or time out; nothing is force-killed silently. Design 26's hard mode splits this: workload teardown in the vCluster, route teardown host-side via the compiler's labelled resources.

## 4. Behavior — reconcile outline

```
observe Agent CR + owned objects + spend aggregate (read-only, design 04)
→ validate. **Admission has already enforced only what CEL can express** (A21): digest-pinned image, prod-gate presence, sandbox/replicas exclusivity, the `usdPerDay` grammar. It has **not** verified an image signature — that needs a Sigstore or Kyverno binding the chart does not ship (design 07 §3), so the reconciler must not treat signature as a settled precondition
→ ensure workload for the desired revision (Deployment | Sandbox); scratchpad per §3.2
→ ensure identity labels / agent-actor client / oauth client (external)
→ candidate Ready? fetch + validate + cross-check card → directory upsert
→ compile PolicyIntent → apply gateway resources (weights per rollout state)
→ hold/advance rollout per gate-controller signals; supersede in-flight candidate on new generation
→ evaluate budget backstop; guard states (BudgetHeld, Killed) win over rollout state
→ update conditions + phase; emit events
```

Idempotent; server-side apply with field ownership; no state outside CR status + directory KV.

## 5. Failure modes & degraded states

| Failure | Behavior |
|---|---|
| Image is a tag, not a digest | Rejected at admission by CEL; never reconciled (A21) |
| `envFrom`/`valueFrom` referent missing or unreadable | `EnvSourceUnresolved=True` naming it; **no revision is minted and no workload is created** (A20). A missing referent is *unresolved*, never a zero digest — a zero would let deleting an object mint the same hash as never having referenced it. `optional: true` does not change this: the operator cannot know what the pod will see |
| Rollback target's env content has changed since it was gated | Rollback is **refused**, naming the referent and both digests (A20). That revision's behaviour is no longer reproducible, and silently rolling back to a name whose contents moved is the bypass this amendment closes, arriving through the recovery path |
| Image unsigned | **Nothing enforces this today.** CEL cannot verify a signature and the chart ships no admission policy; the guarantee arrives with a Sigstore policy-controller or Kyverno binding, which A21 records as owed (§6) |
| Prod gates missing | Rejected at admission when the EvalSuite CRD is installed; never reconciled |
| Card fetch fails | `Registered=False`, candidate held, backoff; **failed and deleted at `registrationDeadline`** so no retention slot leaks |
| Card advertises ungranted capability | `Registered=False` naming the mismatch (§3.4) |
| Card unsigned (BYO/external) | Registers with `CardUnsigned=True` — the BYO promise holds, the gap is visible |
| Sandbox runtime absent | Hardened-Deployment fallback + `SandboxDowngraded` |
| Scratchpad access mode unavailable | Candidate runs without it + `ScratchpadDegraded`; active revision unaffected |
| `replicas>1` without shared-task-state declaration | `TaskStateUnverified` + CLI warning; admission may block for `expose: public` |
| New generation during canary | In-flight candidate drains to 0, recorded in `supersededCandidates` + event |
| Rollback target GC'd | Prevented by §3.3's retention arithmetic and design 20's hold; if it still occurs, rollback is refused and the escalation names the gap |
| Kill switch during rollout | `Killed` guard state wins over every rollout state; gate controller observes and abandons cleanly (design 22) |
| Budget exhausted (backstop tier — not exact in USD, ADR-0028) | `BudgetExhausted` + `phase: BudgetHeld`; serving weights 0 until the 00:00 UTC window resets |
| Spend aggregate unavailable (design 04 down) | Gateway approximation still enforcing; `BudgetEnforcementDegraded` — the exact-tier gap is visible |
| Pricing row missing for a usd budget | `PolicyCompileFailed` naming the model (design 03 §3.5). **Not** `PricingStale`, which means the table is older than 30 days — one condition cannot carry both meanings and mean either (A15) |
| Receipt pipeline gaps affecting this agent | `ReceiptsDegraded` (design 04) |
| IdP unavailable at `agent-actor` provisioning | `IdPUnavailable`; machine mTLS paths continue, human/on-behalf-of paths fail closed (design 06) |
| Gateway API **unreachable** (control plane down), CRDs still installed | Last-applied config stands; `Ready` degrades to stale-but-serving; retry. Scoped to unreachability: **deleting the CRDs garbage-collects the routes**, so nothing stands and design 03 §5's `CRDsAbsent` row governs instead (A18) |
| **Gateway CRDs absent, `gateway.enabled: true`** | A **broken install**: `GatewayIncompatible=CRDsAbsent`, `Ready=False`, `phase: Pending`, page — at every profile, `local` included |
| **`gateway.enabled: false`** — the declared-ungoverned tier (P1, `local`) | **`GovernanceSkipped=GatewayDisabled`**, a separate condition type, not a reason on an error type. `Ready` unaffected, no page. Same shape as `GatesSkipped` for a missing EvalSuite CRD and `ReBACAvailable=False` in design 24 — a tier that was never installed is not an incident, and consumers keyed on error conditions must not fire on it (A18) |
| Policy compile or apply fails for this agent | `PolicyCompileFailed` / `PolicyApplyIncomplete` (design 03 §3.3, §3.3.1); routes withheld, so no ungoverned traffic path exists |
| SPIRE down | Existing SVIDs valid until expiry; new revisions held (`IdentityIssued=False`) |
| KG binding invalid or not `active`/`superseded` | `KnowledgeBound=False` → `Degraded`; no traffic if it is the sole knowledge source (design 01 A5) |
| Finalization stalls (drain or revoke blocked) | Finalizer holds; condition names the stuck step; never force-deleted |
| Operator crash | Reconcile resumes from CR/status; revisions are content-addressed (no double-create) |

## 6. Security

Admission, **and what actually enforces each** (A21 — an earlier version of this line asserted all four as enforced and none was): digest-pinned image (CEL on the schema) · prod-gate presence (CEL) · sandbox/replicas exclusivity (CEL, ADR-0027) · `expose: public` requires an approval label (CEL). **Cosign signature verification is not CEL-expressible and is not shipped** — it needs a Sigstore policy-controller or Kyverno `verifyImages` binding on the workload namespaces, owed to design 07 §3. Digest-pinning is what makes that verification meaningful when it lands, because a signature is verified *against a digest*. Workload pods: default-deny NetworkPolicy (egress = gateway only), non-root, read-only rootfs. Directory writes: operator identity only. External-agent OAuth secrets in k8s Secrets (ESO-compatible), never in spec. The candidate-only eval route admits exactly one principal (design 16).

## 7. Observability

Conditions as above; events on every transition; metrics: reconcile latency, rollout duration, held-revision count, superseded-candidate count, card-drift count, registration-deadline failures, and gauges for `SandboxDowngraded` / `TaskStateUnverified` / `ScratchpadDegraded`. Shipped alerts: candidate held >1h, repeated card-fetch failures, identity issuance failures, budget-held agents.

## 8. Testing & conformance

envtest for reconcile logic (revision arithmetic including the retention rule, supersede-during-canary, condition transitions, finalization ordering). e2e on k3d+kind (NFR-3): sample echo-A2A agent → registered / identity / routed; sandbox fallback asserted on kind; scratchpad read-only attach and its degraded path; rollout hold/advance with a fake gate controller; spec-change-during-canary; kill during eval; external-agent registration against a mock endpoint; card cross-check negative test; registration-deadline expiry frees the slot.

## 9. Decisions (veto async)

1. **Deployment default; Sandbox opt-in** (singleton; mutually exclusive with `replicas>1`).
2. **Card SoT = container-served**, per-revision digests in status; inline override only for external agents.
3. **Gateway-weighted blue-green revisions owned by the operator** — required by eval-as-admission; retention counts *in addition to* active and candidate.
4. **Identity via one templated ClusterSPIFFEID + pod labels**, plus a per-agent `agent-actor` client; zero custom registrar code.
5. **Detection over assumption** for multi-replica task state, and **loud degradation** for every unavailable guarantee.
6. **Finalization deactivates identity objects, never deletes them** (design 06 D3).

## 10. Resulting ADRs

ADR-0019.

## 11. Interfaces this design owns for others

`status.cards[]` (design 05 directory), the injected env contract `PLUME_GATEWAY_URL` / `PLUME_KG_ENDPOINTS` / `PLUME_NATS_URL` + tenant creds (design 09 consumes, never defines), `PolicyIntent` construction (design 03), and the rollout state machine the gate controller drives (design 16).

## 12. Amendment history (provenance only — the body above is authoritative)

A1–A8 (2026-08-20) and A9–A10 (2026-08-22) recorded, in order: budget backstop wiring and its conditions; the canonical directory key layout; `agent-actor` client provisioning; the core-tier gating contract; `GatesBypassed`; card-signature verification; the injected env contract; `llm.fallback`; the seven behaviours from the independent re-critique; and a blanket supersession rule. **All are now folded into §§3–5** — the integration pass the re-critique asked for, so an implementer reads one spec rather than a body plus ten patches.

A11 (2026-08-22, **ADR-0027**) — the ergonomics pass, run at the start of implementation rather than after users existed. `knowledge[].graphRef` and `tools[].mcpRef` are **flattened to inline `{name, version}` / `{name, namespace}`**: a binding points at exactly one kind of thing, so the wrapper nested without discriminating. `expose.a2a` keeps its wrapper — the arm names a protocol and MCP exposure follows it. Two rules §3 stated in prose are now **CEL on the schema** (exactly-one-of runtime/external; sandbox excludes `replicas>1`), so they are rejected at `kubectl apply` with a message naming the fix rather than discovered at reconcile. §3.1 above shows the amended shape; the whole contract is pinned by `test/envtest/agent_dx_test.go` against a real API server.

A11 was revised after independent critique (REVISE: 1 blocker, 4 major). The first pass also added `tools[].namespace`, which design 24 §4.1 would have turned into a self-authorizing cross-namespace grant — deleted, and kept deleted by `TestToolBindingCannotReachAnotherNamespace`. The flattening's stated premise ("a binding points at one kind") was false for tools, which resolve against a Connector facet *or* an `MCPServer`; the corrected premise and the namespace-unique resolution rule are now in §3.1. `status.eval` and `status.budget.usdSpentToday` were added so this design's own printer columns have fields to read.

A12 (2026-08-22, **r2 after independent critique returned REVISE**) — **which spec fields mint a revision.** §3.3 said `revisionHash(spec)` and §3.6 sent `tools`, `budgets`, `llm`, `expose` and KG scopes into the live policy path; nothing reconciled the two, so an implementer had to guess. The dangerous guess was not the expensive one — hashing everything merely makes a replica bump pay for an eval cycle — but the cheap one, where `llm.providers` or `knowledge[].version` compile straight to gateway config and an operator swaps the model or the domain data under a running agent with no gate. §3.3 now carries the normative split, the rule that the projection is allowlist-shaped so new fields default to policy-surface, and the canonicalization. Raised while implementing the reconciler, before any code was written.

A12 r2 corrections (2026-08-22). The independent critique found the r1 table wrong in the dangerous direction on one row and incoherent on three more. **`external.oauthClientRef` was on the policy surface labelled "credential rotation"** — it is the identity selector design 24 keys authorization on, so repointing it was an ungated capability change, exactly the hole A12 was written to close. **The stated criterion was a mechanism test** ("whatever §3.6 compiles") while the table was built on a semantic one, so an implementer following the prose would have classified `knowledge[].scope`, `tools[].name` and `llm.providers` as policy. **`card.path` was behaviour** while §3.3 says card *content* may change with no gate at all — the same outcome given opposite treatment; it moves to policy. **The allowlist default was backwards**: classification is now compulsory and enforced by reflection over `AgentSpec`. Added: list-order insensitivity with its justification, the `runtime.resources` reasoning, the `external.inlineCard` row that r1 adjudicated nowhere, and the design-20 fallback exception without which A12's safety claim is simply false.

A13 (2026-08-22) — **`Progressing`, and what `Canary` is not.** The vocabulary had no way to say "a rollout is in flight", which forced a routine spec edit on a healthy agent to be reported as one of two lies. `Canary` was wrong because §3.3 step 4 fixes its meaning as *weights are shifting*, and before design 03 exists no weights shift at all — an operator reading `kubectl get ag` would see a canary progressing while the new pods CrashLoop. `Ready=False` was worse: the agent **is** serving, all traffic still held by the active revision, so every routine spec edit would trip any alert or readiness gate keyed on the canonical condition.

The state is now: `phase: Ready`, `Ready=True` naming the revision that serves, `Progressing=True` naming the one coming up, and `status.candidateRevision` set. `Canary` is reserved for the real weighted shift arriving with design 03.

`Progressing` is a **normal-true** condition, so it is sticky — set `False` on completion rather than removed. The same rule now applies to `Ready` and `GatesPassed`: an abnormal-true condition (`SandboxDowngraded`, `TaskStateUnverified`) may be dropped when it stops applying, because its absence *is* the signal, but dropping `GatesPassed` when an EvalSuite CRD is uninstalled would silently erase the record that a revision ever passed a gate. `AgentStatus.conditions` is also now `+listType=map +listMapKey=type`, so the API server rejects duplicate entries for every writer rather than only this operator.

Condition count: **21**.

A14 (2026-08-22) — **`CANDIDATE` returns to the printer columns.** A13 made a rollout in flight render as `phase: Ready`, which is correct — the agent *is* serving — but it removed the last thing that made an in-flight rollout visible at a glance. With `registrationDeadline` (§3.4) still unimplemented, a candidate that never becomes available then shows as a completely healthy agent, with `Progressing=True` visible only to someone who thinks to read conditions. The previous behaviour reported `Canary`, which was wrong in meaning but at least visible; the fix improved correctness and reduced discoverability, and the column restores it for one line of markers.

A15 (2026-08-25, from design 03 A1/A2/A6; revised after independent critique returned REVISE) — **four conditions the corpus already raises on this CR, and what `Ready` may claim without a gateway.** Design 03 §5 raises `PolicyCompileFailed`, `PolicyApplyIncomplete` and `GatewayIncompatible` here; design 08 §8 already prints the second as an Agent condition and design 22 §6 calls it a family. Design 20 §3 raises **`ModelDrifted`** "on affected Agents" and §4 reads it back for the correlation rule. `reviews/02-recritique.md` named two of these as outstanding; the first draft of this amendment added three and missed `ModelDrifted`, which would have reopened a closed list one amendment later. All four are **abnormal-true** — absence is the signal, so they may be dropped when they stop applying, unlike `Ready`, `Progressing` and `GatesPassed` (A13). Condition count: **25**. Design 22 §6's interceptor-down condition is still anonymous; naming it is design 22's call and is left as a carried open item rather than invented here.

**What the closure test actually enforces, stated because the previous sentence was false.** `api/v1alpha1/schema_contract_test.go` asserts only that the list has the declared length and no duplicates — it never compares names to anything. A critic mutation-checked it: renaming `CondPricingStale` to `TotallyWrongName` **survived**. So "the vocabulary is closed and mechanically pinned" was true of the cardinality and nothing else, and ADR-0027's claim that this rule was verified by mutation was wrong. The implementation change this amendment owes is to compare `designConditions()` against the list parsed from §3.1 above, the way the CRD golden already works. **Until that lands nothing is enforced, not even cardinality**: the test asserts a hard-coded 21 and `designConditions()` returns 21, while §3.1 above declares more — so the count is not merely unchecked, it is checked against the wrong number. No literal appears here on purpose; §3.1's list is the only place the count is stated, because restating it is how this sentence has been wrong three times running. Saying "the enforced bound is the count" was the same class of claim this paragraph exists to retract.

**`Ready` under a gateway that was never installed — keyed on what the install DECLARED, not on what discovery finds.** §5 described gateway trouble only as a regression ("last-applied stands"), which is vacuous when nothing was ever applied. An agent whose budget, authn and tool-filter were never compiled is not serving under the guarantees `Ready` asserts: architecture §02 puts every guarantee at a hop the traffic never took. But "the CRDs are missing" means two incompatible things, and an earlier draft collapsed them — which would have made **every Agent on a stock `helm install` permanently un-`Ready`**, since `values.yaml` ships `profile: prod` and the P1 chart ships no gateway. Design 03 §3.1 splits them on the chart's `gateway.enabled`:

- **`gateway.enabled: true`, CRDs absent** — a broken install. Someone declared governed traffic and did not get it. `GatewayIncompatible=CRDsAbsent` **withholds `Ready`**, `phase: Pending`, pages.
- **`gateway.enabled: false`** — a deliberately ungoverned tier, which is what P1 and `local` ship. **`GovernanceSkipped=GatewayDisabled`** — a distinct condition type since A18, because every consumer keys on the type — and **`Ready` is not withheld**: nothing was promised, so nothing is broken. Loud, not silent — the condition is on every Agent and the chart's `NOTES.txt` says agents are ungoverned.

`phase: Pending` rather than `Degraded`, because `Degraded` would hold design 20's retention pin open forever and let revisions accumulate without bound. **`Ready` is set `False` with reason `GatewayIncompatible`, never dropped** — A13 made it a sticky normal-true condition and an absent `Ready` reads as "not evaluated" to every consumer keyed on it.

**This narrows A13; it does not contradict it, and the distinction matters.** A13 rejected `Ready=False` for an agent *whose traffic already flows* — a routine spec edit on a healthy agent, where the active revision is serving through a working gateway. That reasoning is untouched. This is the disjoint case: traffic has never flowed through a governed path at all, so there is no serving revision for `Ready` to name.

**Two earlier framings are retracted here, because both were wrong and one was dangerous.** The first justified a `local`-profile carve-out by analogy to `GatesBypassed=DevProfile` — but that condition is *opt-in* (design 08 §5 reaches it by explicit flag on dev revisions) while `CRDsAbsent` is environmental, so the analogy borrowed authority from the wrong precedent. The second kept the carve-out on its own merits and keyed it on profile. Both were solving the wrong problem: **profile is about developer ergonomics, tier composition is about what was installed**, and `Ready` must key on the second. `gateway.enabled` says what the install promised; a promise unkept withholds `Ready` at *every* profile including `local`, and a promise never made withholds nothing. There is no carve-out left to expire, and no `--profile` flag is needed — which is fortunate, since `cmd/operator/main.go` declares four flags and none is `--profile`.

**What this owes, unimplemented and named so it is not assumed**: `gateway.enabled` does not exist in `charts/plume/values.yaml`, and the operator has no corresponding flag. Until both land, no part of this rule runs.

**Also corrected**: §5 said a missing pricing row raises `PricingStale`, while design 03 §3.5 defines `PricingStale` as the table being over 30 days old. Two meanings for one condition is rule 8's loud-and-wrong. A missing row is `PolicyCompileFailed`; staleness is age. And `spec.budget.usdPerDay` gains its decimal grammar as CEL here, since §3.1 is the authoritative schema — the §3.1 example's `usdPerDay: 40` was a manifest the API server would reject against `*string` and is fixed above. **Not changed here**: A12's classification table — that is **A16**, below.

A16 (2026-08-25, from `reviews/02-codex-review.md` BLOCKER 1 and design 03 A6) — **classification is leaf-level, not top-level.** A12 claimed exhaustiveness and `TestEveryFieldIsClassified` was cited as proving it. The test walks top-level `AgentSpec` fields; behaviour lives in leaves inside embedded `corev1` structs, and seven of them are projected nowhere. Direct differential mutations — one leaf changed, same revision hash — confirm each. The one that matters most is not the deepest: **`llm.egressAllowlist` was in neither column of A12's table at all**, and it is the endpoint set an agent may reach and the control ADR-0014's compliance profiles pin, so widening egress on a HIPAA cluster minted no revision and fired no eval. It is now classified behaviour, on the argument A12 already makes for `knowledge[].scope`.

The rule and the required test shape are folded into §3.3. The test **reflects over the live type graph and never enumerates arms** — A12 rule 2 records this trap springing once already, when `envSourceRef` named four arms against a five-arm struct and `fileKeyRef` collapsed to a constant. A hand-maintained leaf list reintroduces that on the next `k8s.io/api` bump.

**Why the design says this rather than the code just fixing it**: a leaf sweep run against the current projection reached 31 leaves and would have missed four of the seven, while looking like it had passed. Proving absence is the hard part, so the test shape is a contract, not an implementation detail.

Both were settled by the user on 2026-08-26 and are recorded as **A20** and **A21** below.

A17 (2026-08-26) — **A15 and A16 revised after the second critique.** Round 2 returned REVISE against this set; `docs/designs/reviews/03-amendments-review.md` carries both rounds and the standing Claude-vs-Codex disagreement. Three changes here, each because the previous text was wrong rather than incomplete.

**A16's mandated test could pass without working (BLOCKER 2).** `resource.Quantity` keeps its value in unexported fields, so the only reflect-settable leaf under `divisor` is `Format`; a critic projected `Format` alone, watched the leaf walk go green, and reproduced Codex M4 unchanged. Paths are still discovered by reflection — enumerating them is what broke on `fileKeyRef` — but **types** are now enumerated in a perturber registry that **fails closed**: a type in the graph with no perturber fails the test rather than being skipped. Map-kinded leaves are defined too.

**A16 stated two rules that contradicted each other (MAJOR 1).** "The aggregate binds every leaf" and "fails unless the leaf is explicitly classified policy" give opposite answers for `tools[].requiresApproval` and `external.inlineCard`. An aggregate's classification is now the **default** and an explicitly classified leaf overrides it — which is what the table already assumed and never said.

**~~`llm.egressAllowlist`'s gate is asymmetric~~ — RETRACTED BY A18.** Kept because amendments are kept, not because it is current. The rule was unimplementable in `revisionHash(spec)`, laundered a widening through a narrowing, and rested on a **false premise**: design 27 §5 ships the BAA allowlist as a pack filter in the `compliance` band and §3 makes it an admission rule, so a list change is *not* applied by editing every Agent and the 200-candidate scenario cannot arise. §3.3 rule 4 above is authoritative and symmetric.

**`Ready` is rekeyed on `gateway.enabled` (BLOCKER 3)**, retracting both of A15's earlier framings — see A15 above for why profile was the wrong key and what the declared-configuration split is. **ADR-0027's erratum was itself false** and is corrected there: the test asserts a hard-coded 21 while §3.1 declares more, so nothing is enforced, not even the count. (A19 removed the literal from this sentence too — it had been wrong in three consecutive rounds.)

**Still owed**: `gateway.enabled` and `NOTES.txt` in the chart (design 07 §3); the signature-verification binding (A21); the `designConditions()`-vs-§3.1 comparison; and the six condition constants, the digest CEL and the immutability check, none of which exists in code.

A18 (2026-08-26) — **round 3: the asymmetric gate retracted, the registry corrected against measurement, tier-absence given its own condition.** Three rounds of critique, all REVISE; `reviews/03-amendments-review.md` carries them.

**A17's asymmetric `egressAllowlist` gate is withdrawn (BLOCKER 4)** and §3.3 rule 4 now states why. It was unimplementable — `revisionHash(spec)` is computed from spec alone, and "is this set a subset of the previous one" needs `(old, new)` — and it laundered a widening through a narrowing onto the serving, gated revision, which is the ADR-0006 hole the rule exists to close. Its premise was also false: design 27 §5 ships the BAA allowlist as a **pack filter** in the `compliance` priority band and ADR-0014 puts it "enforced at the gateway", so a list change is a pack/values change compiled into the LLM Backend at profile scope — the 200-Agent-edit scenario that justified the asymmetry cannot arise. Nothing was bought for the hole it opened. This is the second time in this set that a rule was written in a shape the mechanism it attaches to cannot express.

**A16's perturber registry is corrected against a measured graph (MAJOR 9).** `AgentSpec`'s transitive graph is 68 nodes / 38 named / 57 leaves / depth 10, no cycles, no interface leaves. **Exactly one type carries unexported state** — `resource.Quantity` — and of the four the first draft named, `metav1.Time` and `intstr.IntOrString` are not in the graph at all while `metav1.Duration` is fully reflect-settable. Measured `k8s.io/api` drift is +1 type / +5 leaves across eight minor releases, and the one arrival (`FileKeySelector`) has all-exported fields — so the claim that the registry "announces a new type as a failure" was false for the only drift that has actually happened, and the **path** walk is what caught it. The claim is narrowed to what is true, a perturber now terminates the walk at its type, the map rule is shown composing with it, and the no-self-recursive-type precondition is stated.

**Tier-absence becomes `GovernanceSkipped`, its own condition type (BLOCKER 2).** It is **abnormal-true** under A13 — a tier that was never installed is signalled by the condition's presence, so it is dropped when the tier is turned on rather than flipped to `False`, exactly as `GatesSkipped` behaves. Condition count **26**. See design 03 A8.

**Also**: §3.1's `usdPerDay` comment said seven digits where design 03 says six — this file is the authoritative schema, so the overflow blocker was live here after being fixed there. §5's "Gateway API unavailable" row is scoped to *unreachable*, since deleting the CRDs garbage-collects the routes and stale-but-serving would then be loud-and-wrong.

A19 (2026-08-26) — **round 4 corrections.** REVISE with **0 blocker**; the substance has converged and what remained was reach. A17's asymmetric-gate bullet still stated the retracted rule in the present tense with no marker, asserting as fact the design-27 premise A18 declares false — now struck and marked, because an amendment log that reads as current is the same defect as round 1's misresolved citation. The "§3.1 declares 25" sentence was wrong for the **third** consecutive round, inside the paragraph whose only purpose is to state exactly what is enforced; the literal is deleted from both here and ADR-0027, and §3.1's list is now the single place the count lives. `GovernanceSkipped` is classified **abnormal-true** (dropped when the tier is turned on, not flipped to `False`), which A13 makes load-bearing and A18 omitted. The owed list is corrected to **five** condition constants. The 57-leaf figure is withdrawn — two independent walks produced different counts under different termination rules, and the rule is what matters, not a number that only looks authoritative.

**A20–A21 are now drafted** (`envFrom` ConfigMap contents; mutable image tags) — decided by the user on 2026-08-26, and neither has been critiqued.

A20 (2026-08-26, revised 2026-08-27 after `reviews/03-codex-review-r2.md` BLOCKER 5; both readings decided by the user) — **env sources are hashed by content.** The hole: `envFrom` accepts any ConfigMap or Secret, the projection stored only the reference, so an actor with no access to the Agent CR but `update` on a referenced object could change what the agent does while the revision, the gate result and the reported state all stayed still.

**The first fix was wrong in two independent ways, and both were mine.** Requiring `immutable: true` does not bind a name to contents — Kubernetes permits delete-and-recreate under the same name, which is the same referent and the same hash. And exempting Secrets as a Kind assumed Kubernetes distinguishes credentials from behaviour, which it does not. §3.3 rule 2 carries the retraction.

**What content hashing costs, stated because it is not free.** Three consequences, none of which the mechanism can avoid:

- **`revisionHash` is no longer computed from spec alone.** `internal/revision/revision.go:12` names that as load-bearing for content-addressing, "no double-create", and instant rollback. Those properties are preserved differently: the digest is recorded in `status` **when the revision is minted**, so a retained revision carries the digest it was gated with rather than re-reading the world.
- **Rollback can now be refused.** If a referent's content has moved since that revision was gated, its behaviour is no longer reproducible, so rollback fails loudly naming both digests instead of re-pointing at a name whose contents changed. Refusing is the fail-closed direction; silently rolling back would reintroduce the bypass through the recovery path.
- **Credential rotation now mints a revision and fires a gate.** This is the exemption A12 rule 2 previously granted and this amendment retracts. Rotating a key is now an eval cycle. That is a real operational cost, and the alternative — keeping a Kind-shaped exemption — is the hole itself.

**A missing referent is `EnvSourceUnresolved`, never a zero digest**, and `optional: true` does not soften it: a zero would make deleting an object hash identically to never having referenced it. Condition count: **27** (`EnvSourceMutable`, which the retracted immutability rule needed, is replaced by `EnvSourceUnresolved`).

**Owed, and not resolved here**: the operator must watch every referenced ConfigMap and Secret to notice a content change at all, which is new RBAC and a fan-out this amendment does not size; and whether the digest covers whole objects or only the keys actually projected — whole-object is safer and noisier, key-scoped is precise and needs `envFrom`'s wildcard semantics settled first. **A20 has had no critique in this form.**

A21 (2026-08-26, from `reviews/02-codex-review.md` BLOCKER 3; decided by the user) — **`runtime.image` must be digest-pinned, and this design's admission claims were false.** The API accepted any non-empty string, the controller sets `imagePullPolicy: Always`, and the CRD comment read "Image must be cosign-signed; admission rejects unsigned images" — generated verbatim into the shipped CRD at `config/crd/plume.dev_agents.yaml:437`. Gate `registry/agent:prod` while it points at X, retag to Y, reschedule: Kubernetes pulls Y while the spec, the revision hash and the gate result are all unchanged. The hash is not content-addressed, which is precisely the property `Rollback = re-point to a retained revision` depends on.

`runtime.image` now requires an `@sha256:` digest, as **CEL on the schema** — rejected at `kubectl apply` with a message naming the fix, costing no pod, and expressible because it reads only the Agent's own field. The digest grammar is exact: 64 lowercase hex characters.

   **Unconditional, with no local-profile relaxation** (revised 2026-08-27, `reviews/03-codex-review-r2.md` BLOCKER 6). An earlier version said the `local` profile may relax it for `:dev` loops. CRD validation has no Helm profile, namespace tier or operator flag in its evaluation context, and one schema applies cluster-wide — so a CEL alternative permitting `:dev` would permit that mutable tag in **production** namespaces, which is exactly the bypass A21 exists to close, while omitting the alternative makes the promised relaxation nonexistent. There was never a middle option. The developer loop resolves its own build to a digest and writes that digest, which is what CI already does. If a mutable-tag path is ever genuinely needed it requires a separate namespace-scoped admission mechanism with an explicit dev trust boundary, and cannot be described as profile-sensitive CEL.

**The honesty half is the larger correction.** §5 said an unsigned image is "Rejected at admission (CEL)" and §6 listed "cosign verification" among four enforced admission rules. **CEL cannot verify a signature, and `charts/plume/` ships no admission policy of any kind**, so all four were unenforced and one is not CEL-expressible at all. §5 and §6 now state, per rule, what actually enforces it. Signature verification needs a Sigstore policy-controller or a Kyverno `verifyImages` binding on the workload namespaces, **owed to design 07 §3** — and digest-pinning is what will make it meaningful when it lands, because a signature is verified against a digest. Until then the design says plainly that nothing verifies signatures.

A22 (2026-08-27, from design 03 A16) — **`llm.egressAllowlist` becomes a typed list.** It is `[]string` today, and design 03 §3.4.1.1 shows a bare string is ambiguous exactly where it decides reachability: given `azure/openai/gpt-4`, an entry `azure/openai` is a provider prefix to one implementer and a host to another, and the two emit different reachable endpoints. Each entry now carries exactly one of `provider` (with optional `models`) or `host`. This is a **breaking schema change**, correct to make before v1beta1 and not after.

It also sharpens the classification. A16 keeps the whole field on the **behaviour surface** and symmetric (§3.3 rule 4) — but note the field is a *ceiling*, not a selection: what an Agent may reach is `providers[] ∪ {fallback}` **∩** this list, and design 03 emits the requested set, never the permitted one. Widening the ceiling still mints a revision, because it widens what a subsequent `providers` edit could reach without further review.

**Owed**: `llm.providers` is still a flat `[]string` while `llm.fallback` is structured `{provider, model}`, which is the non-injective key Codex r2 MAJOR 4 flags — `{azure, openai/gpt-4}` and `{azure/openai, gpt-4}` canonicalize identically. Making `providers` typed in the same pass is the obvious fix and is **not** drafted here; it touches A12's projection and every example in this design.

A23 (2026-08-27, from `reviews/03-codex-review-b3.md`; decided by the user, then **measured**) — **generic env sources are sealed, not snapshotted.** A20 closed the detection half and left the serving half open: content hashing mints a candidate, but the active revision's Pod template still resolves the source **by name**, so a node drain serves changed content under the gated hash. Codex found it; I had not.

**Its prescribed fix — snapshot every source into revision-scoped copies — was revised by its own author on review.** Copying archives every historical credential for the retention horizon, adds an API/backup/audit/RBAC/GC surface per snapshot, and lets a rollback resurrect a credential its owner retired — the opposite of Kubernetes' own guidance on short-lived material. The chosen shape **seals the source in place** instead: finalizer plus a fail-closed `ValidatingAdmissionPolicy` denying update, delete, label removal and finalizer removal by everyone but the operator, for as long as a retained revision holds it.

**One of my two objections to snapshots was factually wrong**, and it is worth recording because it drove the question. I argued snapshots would freeze live credential rotation. They would not: Kubernetes never refreshes a process environment for `envFrom`/`valueFrom`, so container replacement was always required. Only mounted volumes rotate live and this API does not offer them. The real costs of snapshots are retention and blast radius, not rotation latency.

**Measured before adoption** (`research/agentgateway-v1.4.1-spike.md` §4), because the reviewer attached that caveat to its own recommendation. A principal with full ConfigMap rights was denied all four attacks — data change, label removal, finalizer removal, delete — while the operator retained access. Two further results changed the design rather than confirming it:

- **The obvious policy is bypassable.** A match inspecting only the *new* object let a label-removal opt itself out, after which the data change succeeded. The old-**or**-new match condition is load-bearing; a reasonable implementer writing the natural version ships a seal any holder of `update` can remove in one request.
- **Deleting the admission binding immediately reopened the bypass.** The Policy and Binding are **trust boundary**, not drift to be healed afterwards: the chart protects them, and the operator withholds new revisions and raises `EnvSourceProtectionUnavailable` when either is absent or skewed, verified at startup and every reconcile. The finalizer is not redundant — it covers exactly that window.

**Refcounting and guard-loss behaviour are specified by A26.** Still owed after that: the typed credential binding (so genuine credentials rotate without an eval, which sealing alone does not give), and the node-drain assertion, which needs a kubelet and is e2e-only. Snapshots remain the stated fallback if A26's lease cannot be shown race-free.

A24 (2026-08-27, from design 03 A18; supersedes A22's shape) — **`llm.providers` and `llm.fallback` become the same typed endpoint identity, which closes the non-injective key too.** A22 typed only `egressAllowlist`, leaving `providers[]` a flat `[]string` and `fallback` a `{provider, model}` object. That left two defects:

**The join is not injective** (Codex r3 MAJOR 4). `{provider: azure, model: openai/gpt-4}` and `{provider: azure/openai, model: gpt-4}` both canonicalize to `azure/openai/gpt-4`, so they select the same pricing row and the same egress decision while being different endpoints. `internal/revision/revision.go:66` already records this hazard in the other direction; typing both ends removes the join entirely rather than defining a split rule that a slash in a model name defeats.

**An arm is not an endpoint.** The shipped v1.4.1 CRD gives each provider arm its own instance fields — `azureopenai` carries `endpoint`/`deploymentName`/`apiVersion`, `vertexai` carries `projectId`/`region`, `bedrock` carries `region`/`guardrail`. Two Azure OpenAI resources share one arm, differ in endpoint and deployment, and may differ in BAA status. A flat string cannot select between them, so neither a request nor a permission could be expressed precisely enough for design 03's `egressEnumerated` predicate to mean anything.

Both fields therefore carry `{arm, instance fields, model}`, and `egressAllowlist` entries carry the same identity minus the model plus optional `models`. `providers`, `fallback` and the allowlist now share one type, compare as tuples, and need no canonical string at all.

**Breaking, and correct now**: this changes `llm` for every existing manifest, touches A12's projection (the field stays behaviour surface and symmetric), and invalidates the flat examples throughout this design — all of which is cheaper before v1beta1 than after. **Owed**: the pricing table (design 03 §3.5) is still keyed by the flat string it was given; keying it on the same tuple is the obvious follow-through and is not drafted here.

A25 (2026-08-27, from `reviews/03-codex-review-r4.md` BLOCKER 2 and 4; decided by the user) — **`budget` and `expose` move to the behaviour surface, because in-place tightening cannot be made safe.**

A12 drew the policy/behaviour line on *capability*: "how much, how fast, or who may call" was applied in place because it does not change what an agent can do. That reasoning assumed in-place application was **safe**. Two measurements say it is not:

- a NACK'd policy **retains the previous configuration** while every Kubernetes condition reports converged (spike §2.8), so a tightening can silently fail to apply; and
- the witness design 03 A17 introduced to detect that **cannot attribute** the result — a 401, a rejected tool, an unreachable host and a 429 all have producers other than the new policy, so a NACK and a passing witness can coexist (Codex r4 BLOCKER 2).

Design 03 A17's escape — "a concern with no sound witness goes through the revision path" — was then found to be **structurally unavailable** for exactly these fields: a policy-surface change mints no candidate, so there was nothing to gate (r4 BLOCKER 4). The design offered a witness that cannot attribute and a fallback that cannot exist.

**The scope is narrower than the argument suggests, and that is why this is affordable.** Of the concerns §3.3.1 makes mandatory, only two are fed by policy-surface fields — `-auth` from `expose`, and `-ratelimit` from `budget`. `tools[].name`, `knowledge[].scope` and `llm.egressAllowlist` are already behaviour surface, so narrowing a tool set, a KG scope or an egress list **already** mints a revision and already passes a gate. Two fields move; the rest of A12's table is unchanged.

**Symmetric, and deliberately so.** Both fields move wholly: any change mints, including a loosening. An asymmetric "tightening mints, loosening applies in place" rule is not expressible — `revisionHash(spec)` is computed from spec alone and a subset predicate needs `(old, new)`. A17 was retracted for exactly that reason and this amendment will not reintroduce it.

**The cost, stated plainly.** Raising a budget or changing `expose.a2a.visibility` now pays an eval-and-canary cycle. A12 rejected that as too expensive, and on its own terms it was right — a scale-shaped edit should not need an eval. What changed is not the cost but the alternative: the cheap path was measured to be unsound, and an ungated tightening that silently does not apply is worse than a slow one that does. `gates`, `loop`, `runtime.replicas/port/resources`, `card.path` and `external.inlineCard` stay on the policy surface, and design 20's status-driven fallback activation is untouched.

A26 (2026-08-27, from `reviews/03-codex-review-r4.md` BLOCKER 5 and 6) — **who holds a sealed source, and what happens when the guard goes away.** A23 named both as owed. The scoped B3 review had made the first a *precondition* of the seal rather than a test detail, and the second is a live gap in a mechanism already committed.

**The finalizer list is the reference count.** Rather than a side-car lease object with its own consistency problem, each retaining revision adds its **own** finalizer to the source — `plume.dev/src.<agent-uid8>.<revision8>` — and removes only that one. This is deliberate:

- Add and remove **must be an explicit read-modify-write carrying `resourceVersion`, with retry** — this is *not* free, and the obvious implementation is badly wrong. Measured: **20 concurrent naive JSON-patch appends left ONE finalizer**, losing 19 of 20 holds. The same 20 writers doing read-modify-write with `resourceVersion` and retry produced all 20. A26's first draft said the list was "compare-and-swap by construction"; it is compare-and-swap only if the writer supplies the precondition.
- "Release only after an authoritative list of all live leases" collapses to "remove your own entry", because the object *is* the list. The source is sealed while any `plume.dev/src.*` finalizer remains, and becomes deletable when the last one goes.
- The admission policy already denies finalizer removal by every principal but the operator (spike §4), so a holder cannot be evicted by the source's editor.
- Finalizers are durable in etcd, so a crashed operator's holds survive the crash. On startup the operator lists sealed sources and drops entries whose agent-UID or revision no longer exists — orphan reclamation is a startup sweep, not a liveness protocol.

Acquire order is fixed and matters: **seal first, then read.** Add the finalizer and observe it, *then* re-read the source's UID and content and hash it. Reading first would hash content that could change before the seal landed, which is the same before-and-after race the seal exists to close. The recorded material is `{uid, resourceVersion, selected keys, digest}`; a UID change means delete-and-recreate happened while unsealed and is treated as divergence, not as the same source.

**Guard loss does not keep serving blindly, and does not nuke the fleet either.** The spike measured that deleting the admission Binding immediately restores the editor's write (§4.2), and A23's response — withhold new revisions, raise `EnvSourceProtectionUnavailable` — protects only future revisions while retained ones keep resolving a now-mutable object by name. That is truthful and not fail-closed.

The correction distinguishes *possible* from *realised* compromise, because the operator already watches every sealed source for A20's content hashing:

| State | Response |
|---|---|
| Guard absent or skewed, every sealed source still matches its retained `{uid, digest}` | `EnvSourceProtectionUnavailable`; **no new revisions**; serving continues. The exposure is prospective, and taking a healthy fleet down for a routine upgrade window would be its own outage |
| Guard absent **and** any sealed source diverges from its retained digest or UID | **`RevisionMaterialChanged`, weight 0 for every revision referencing it**, `phase: Degraded`. The bypass has occurred, so the affected revisions stop serving — via the existing rollout weight machinery, not a new route mutation (A19 removed those) |
| Guard restored | Re-verify every retained source against its recorded material **before** republishing. Divergence found on restoration is treated as the row above, never repaired in place |

**The residual, stated because it is the honest part.** Detection rides the source watch, so a change that is applied and reverted between watch events is not observed, and a running Pod that already read the changed value keeps it. The seal is what makes that window small; it is not zero, and no arrangement of conditions makes it zero while the guard is absent.

**Vocabulary.** `EnvSourceProtectionUnavailable` was named by A23 and never added to §3.1's closed list — caught here by the count, which is what that list is for. With `RevisionMaterialChanged` the count is **29**; both are abnormal-true.

**Measured** (k3d, ConfigMap finalizers):

| Check | Result |
|---|---|
| 20 concurrent **naive** appends | **1 of 20 survived** — the obvious implementation silently drops holds |
| 20 concurrent `resourceVersion` CAS adds with retry | **20 of 20** |
| 19 concurrent releases while one holder remains | the remaining holder survived; each writer removed only its own entry |
| `delete` while one holder remains | blocked — `deletionTimestamp` set, object retained |

So the refcount is sound *with* CAS and unsound without, which makes the retry loop a contract rather than an implementation detail. The lossy variant is the one an implementer reaches for first, so it is named here and belongs in the mutation battery.

**Still unmeasured**: orphan reclamation after an operator crash, and the divergence→weight-0 transition. Codex's condition stands — if any part of this cannot be shown race-free, A23's fallback is snapshots.