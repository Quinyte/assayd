# Design 02: Agent CRD + agent-operator

- **Status**: **integrated r3** — amendments A1–A10 folded into the body (the independent re-critique asked for one integration pass rather than a rule telling implementers which half to believe). Amendment history is preserved in §12 for provenance only; **the body is authoritative**. · ADR-0019
- **Phase**: P1 · **Size**: L · **Date**: 2026-08-20, integrated 2026-08-22
- **ADRs**: 0002, 0003, 0006 (gating), 0016 (expose/apps), 0019 (this design), 0020 (compiled policy), 0025 (governance) · reviews: `reviews/02-review.md`, `reviews/02-recritique.md`

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
    image: ghcr.io/acme/pa-agent:1.4.2     # cosign-signed (admission)
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
    providers: [openai/gpt-x, internal/pa-classifier]
    egressAllowlist: [...]                        # compliance profiles may pin (ADR-0014)
    fallback: {provider: internal, model: pa-classifier}   # ModelDrifted target (design 20)
  budget: {tokensPerDay: 2M, usdPerDay: 40, taskTimeout: 10m, maxHops: 8}
                                                  # per-day windows reset 00:00 UTC; remaining in status
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
               ReceiptsDegraded, Killed, Ready, Degraded]
```

`kubectl get agents` printer columns: `PHASE · ACTIVE · EVAL(last score) · COST/DAY · AGE`, sourced from `status.phase`, `status.activeRevision`, `status.eval.score`, `status.budget.usdSpentToday` and the creation timestamp. The set is a contract, pinned by `TestPrinterColumnsMatchTheDesign`: the case for a twenty-condition status rests on these five answering the common questions, and `EVAL`/`COST/DAY` are precisely the two a developer could otherwise reach only by reading conditions.

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
   | `llm.providers`, `llm.fallback` | `budget`, `gates`, `expose`, `loop` |
   | `external.endpoint` | `external.inlineCard` — a description, not a grant |
   | `external.oauthClientRef` — **identity**, see below | |

   Four rules that are easy to get wrong:

   1. **`external.oauthClientRef` is behaviour, not "credential rotation".** It selects *which client* an external agent authenticates as — the principal design 24 §3 keys `can_invoke`, `can_call` and the act-chain check on. Repointing it reaches a different set of tools. (Rotating the credential *behind* a client is a Secret update the hash never sees, which is correct and is what the earlier wording confused it with.)
   2. **Env sources are hashed by referent.** Repointing at a different Secret is a behaviour change; rotating the value inside one is not. Any union arm the implementation does not name explicitly must hash **injectively** (over-gate), never collapse to a constant — a constant let `fileKeyRef` repoints through ungated.
   3. **List order is not semantic.** Nothing in the corpus treats tool or provider order as meaningful — design 03 compiles tools to a set-semantics filter and providers to a commutative max-price — while kustomize, helm and `kubectl apply` round-trips all re-serialize lists. The projection therefore **sorts** `tools`, `llm.providers` and `knowledge`, so a re-serialized manifest does not re-gate. `env` keeps its order, which is semantic for interpolation.
   4. **`runtime.resources` is policy for a specific reason.** "Capacity, not behaviour" is too glib — an OOM-killed agent fails tasks and CPU throttling changes `taskTimeout` terminations, both observable in `task_completion`. The real argument is that resource regressions are caught by **design 20's behavioural drift path** (SLO burn on success rate → `Degraded`), and gating capacity changes would block incident response — the same reasoning that exempts fallback activation below.

   **Classification is compulsory, not defaulted.** Every `AgentSpec` field is named in exactly one column, and `TestEveryFieldIsClassified` reflects over the struct to prove the table is exhaustive: adding a CRD field fails the build until someone classifies it and amends this table. An earlier version of A12 said new fields *default* to policy-surface and called that the safe direction — it is not. A behaviour field added without classification reaches production ungated, silently, which is precisely the ADR-0006 hole; the critic demonstrated it by adding a `SystemPrompt` field and watching the whole suite stay green.

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
→ validate (admission already enforced: signed image, prod gates, sandbox/replicas exclusivity)
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
| Image unsigned / prod gates missing | Rejected at admission (CEL); never reconciled |
| Card fetch fails | `Registered=False`, candidate held, backoff; **failed and deleted at `registrationDeadline`** so no retention slot leaks |
| Card advertises ungranted capability | `Registered=False` naming the mismatch (§3.4) |
| Card unsigned (BYO/external) | Registers with `CardUnsigned=True` — the BYO promise holds, the gap is visible |
| Sandbox runtime absent | Hardened-Deployment fallback + `SandboxDowngraded` |
| Scratchpad access mode unavailable | Candidate runs without it + `ScratchpadDegraded`; active revision unaffected |
| `replicas>1` without shared-task-state declaration | `TaskStateUnverified` + CLI warning; admission may block for `expose: public` |
| New generation during canary | In-flight candidate drains to 0, recorded in `supersededCandidates` + event |
| Rollback target GC'd | Prevented by §3.3's retention arithmetic and design 20's hold; if it still occurs, rollback is refused and the escalation names the gap |
| Kill switch during rollout | `Killed` guard state wins over every rollout state; gate controller observes and abandons cleanly (design 22) |
| Budget exhausted (exact tier) | `BudgetExhausted` + `phase: BudgetHeld`; serving weights 0 until the 00:00 UTC window resets |
| Spend aggregate unavailable (design 04 down) | Gateway approximation still enforcing; `BudgetEnforcementDegraded` — the exact-tier gap is visible |
| Pricing row missing for a usd budget | `PricingStale`; compile error if the model matches no pattern (ADR-0020) |
| Receipt pipeline gaps affecting this agent | `ReceiptsDegraded` (design 04) |
| IdP unavailable at `agent-actor` provisioning | `IdPUnavailable`; machine mTLS paths continue, human/on-behalf-of paths fail closed (design 06) |
| Gateway API unavailable | Last-applied config stands; `Ready` degrades to stale-but-serving; retry |
| SPIRE down | Existing SVIDs valid until expiry; new revisions held (`IdentityIssued=False`) |
| KG binding invalid or not `active`/`superseded` | `KnowledgeBound=False` → `Degraded`; no traffic if it is the sole knowledge source (design 01 A5) |
| Finalization stalls (drain or revoke blocked) | Finalizer holds; condition names the stuck step; never force-deleted |
| Operator crash | Reconcile resumes from CR/status; revisions are content-addressed (no double-create) |

## 6. Security

Admission: cosign verification, prod-gate presence, sandbox/replicas exclusivity, `expose: public` requires an approval label. Workload pods: default-deny NetworkPolicy (egress = gateway only), non-root, read-only rootfs. Directory writes: operator identity only. External-agent OAuth secrets in k8s Secrets (ESO-compatible), never in spec. The candidate-only eval route admits exactly one principal (design 16).

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
