# plume — Architecture v0.1

> **plume** is a neutral internal codename (previously "graphene" in early drafts — same project; rename before any public release is a tracked task). A rendered version of this document with figures lives at `docs/architecture.html`.

- **Status**: design draft · 2026-08-20
- **Thesis**: A radically lightweight, Kubernetes-native agent platform. The domain is a pluggable knowledge graph; everything else is an open standard with a thin binding.

---

## 01 · The lightweight doctrine

Every design decision passes six rules. This is the product: competitors ship platforms; plume ships a control plane.

1. **Bind, don't build.** CRDs + operators + a CLI. Zero proprietary protocols, zero bundled agent framework, zero bundled UI.
2. **Two stateful deps, ever.** Postgres + NATS. Durability, eval results, audit, KV, object store, queues — all in those two. No Kafka, no Redis, no Temporal cluster.
3. **Library over server.** If a capability can run as a library inside an existing pod (DBOS, DeepEval, OTel SDK), it never becomes a service.
4. **The reconcile loop is the product.** Versioning = CR generations + GitOps. Self-healing = conditions + controllers. plume adds *semantic* health to machinery Kubernetes already has.
5. **Weight budget is a spec.** Core control plane ≤ 8 pods. One `helm install` on any conformant cluster — minikube, k3d, kind, k3s included; local distros are a CI target, not a courtesy.
6. **Tiered install.** `core` (agents + KG + security + traces) → `plus` (evals, drift, modelhub). Each tier independently removable.

## 02 · System overview

Three planes:

- **Control plane** (GitOps): Git repo of CRs → Argo CD/Flux → **three plume operators** (agent, workflow, model) that reconcile CRs into bindings — routes, identity, gates. The CLI is sugar over CRs.
- **Data plane**: **agentgateway** — one data plane for A2A · MCP · LLM · HTTP traffic. AuthN via SPIFFE + OAuth; guardrail filters; rate limits; **policy compiled from Agent CR budgets/tools/expose blocks**. Every hop crosses it — which is where guarantees live, so they hold for black-box agents regardless of SDK.
- **Substrate**: **NATS JetStream** (events · KV directory · object store · session receipts) + **Postgres** (DBOS durable workflows · eval results · audit index).

Identity: SPIRE issues SVIDs to agent pods and the gateway; the platform IdP (Zitadel by default) handles humans + OAuth token exchange.

**BYO-SDK contract**: an agent is any container that serves A2A and exposes an Agent Card. LangGraph, CrewAI, ADK, Pydantic AI, Claude Agent SDK, or a bash script. Registration = the operator reads the card, publishes to the OASF directory (JetStream KV), issues identity, wires gateway routes.

**Color rule in all figures** (see architecture.html): accent = plume-built; grey = adopted open components. Very little is accent — that is the thesis.

## 03 · The API surface — six CRDs

| CRD | Declares | Reconciled by |
|---|---|---|
| `Agent` | BYO container speaking A2A, or external A2A endpoint — identity, sandbox, KG bindings, tools, budgets, eval gates, expose | agent-operator |
| `KnowledgeGraph` | The domain contract: provider, ontology, ingestion, versioning, semantic probes | agent-operator |
| `EvalSuite` | Semantic admission gates: runner, datasets, metrics, thresholds | agent-operator |
| `Workflow` | Durable orchestration binding — DBOS function or Argo DAG, triggered by JetStream events | workflow-operator |
| `Model` | ModelHub envelope: train → package → eval-gate → serve | model-operator |
| `App` | kro ResourceGraphDefinition composing all of the above | kro |

### Agent CR

```yaml
apiVersion: plume.dev/v1alpha1        # API group TBD at rename
kind: Agent
metadata: {name: prior-auth-reviewer}
spec:
  runtime:
    image: ghcr.io/acme/pa-agent:1.4.2   # ANY SDK — only contract: serves A2A
    sandbox: {profile: gvisor}            # → kubernetes-sigs/agent-sandbox
  card: {path: /.well-known/agent-card.json}
  identity: {}                             # SPIRE SVID injected
  knowledge:
    - graphRef: {name: payer-policies, version: "v12"}
  tools:
    - mcpRef: claims-system
  budget: {tokensPerDay: 2M, usdPerDay: 40}
  gates:
    - evalSuiteRef: pa-regression
  expose:
    a2a: {visibility: org, auth: oauth}
```

### The App CR — whole products, not just plumbing

An App is a **shippable product**: frontend + API + agents/workflows/graphs, released as one unit.

- **The backend mostly disappears.** Workflows exposed as `POST /api/<name>`; an agent's A2A task stream is the chat endpoint over SSE. BFF shrinks to nothing unless genuinely needed.
- **Human auth with zero custom code.** IdP OIDC login; gateway exchanges the user token into on-behalf-of agent credentials → per-user receipts and budgets.
- **Released as a unit.** Members pinned (agent v1.4.2, graph v12, frontend v2.1.0); agents inside still pass individual eval gates; rollback restores the coherent set.
- **Scaffolded.** `plume init app` → frontend template + typed TS client generated from Agent Cards / MCP schemas.

```yaml
kind: App
metadata: {name: prior-auth-desk}
spec:
  frontend: {image: ghcr.io/acme/pa-desk-ui:2.1.0, route: pa.acme.dev}
  api:
    - workflowRef: prior-auth-check     # → POST /api/prior-auth-check
    - agentRef: pa-reviewer             # → SSE /api/chat
  members:
    agents:  [{name: pa-reviewer, version: "1.4.2"}]
    graphs:  [{name: payer-policies, version: "v12"}]
  auth: {oidc: platform-idp, roles: [reviewer, admin]}
```

## 04 · The KnowledgeGraph contract — the differentiator

**The domain is data, not code.** New vertical = new `KnowledgeGraph` CR, everything else reused.

```yaml
kind: KnowledgeGraph
spec:
  provider: graphiti | cognee | trustgraph | neo4j | byo   # byo = any MCP endpoint
  endpoint: {mcp: "http://payer-kg:8080/mcp"}
  ontology: {configMapRef: payer-ontology}   # the DOMAIN CONTRACT
  ingestion: {workflowRef: policy-ingest}
  versioning: {strategy: snapshot}           # immutable version per ontology/corpus change
  health:
    probes:
      - {name: coverage, query: "known-answer probe set", threshold: 0.9}
```

Three properties nobody else has:

- **Versioned like a deployment.** Agents pin `graphRef.version`; roll forward = rollout, roll back = repoint; the ontology diff is code review.
- **Semantic readiness.** Ready = probe set passes, not pod-up. Agents bound to a non-Ready graph get no traffic.
- **The ontology is the eval scaffold.** Probe sets, golden sets, and drift baselines all derive from the one reviewed artifact.

## 05 · Communication & durability

"Workflow engine" is three jobs; conflating them is how platforms get heavy:

| Job | Binding | Why · weight |
|---|---|---|
| Agent↔agent messaging, events, KV, receipts | **NATS JetStream** | CNCF-graduated, one binary, KV + object store built in · 1 pod |
| Durable execution inside workflows | **DBOS** (library on Postgres) | Crash-resume/retries/idempotency via decorated functions; the anti-Temporal choice · 0 pods |
| Batch DAGs (ingestion, training, nightly evals) | **Argo Workflows** | plus tier; never for agent loops |

### Exposure — everything that runs here is also a server

Symmetry rule: consume open standards **and publish over the same ones**. A declarative `expose` block per CR, compiled to gateway listeners; external consumers are just another authenticated principal (IdP OAuth client, per-consumer budgets, receipts).

| Resource | Exposed as | Consumer sees |
|---|---|---|
| `Agent` → A2A | org/public A2A endpoint | Agent Card at `/.well-known/agent-card.json`, OASF listing, full task interface |
| `Workflow` → MCP | named MCP tool | `tools/call` starts the run; progress streamed — plume workflows usable from Claude Code/IDEs |
| `Agent` → MCP | single tool | for MCP-only clients |
| `Workflow/Agent` → HTTP | REST + SSE | app-facing projection with the logged-in user's OIDC context |
| `KnowledgeGraph` → MCP | read-only server | 6-tool surface with gateway-enforced subgraph scoping |
| `App` → bundle | product surface | curated versioned offering |

## 06 · Security plane — zero-trust, no mesh

**Guarantees live outside the agent.**

| Concern | Binding | Weight |
|---|---|---|
| Agent identity | **SPIRE** — X.509 SVIDs, auto-rotated | 2 pods |
| Human auth + delegation | **IdentityProvider slot** — default **Zitadel** (Go binary on our Postgres, organizations-native, built-in audit; AGPL, used unmodified); **Keycloak** profile (CNCF incubating); Dex-style federation for BYO IdPs. OAuth token exchange → on-behalf-of chains | 1 pod |
| Runtime authorization (ReBAC) | **AuthzProvider slot** — default **OpenFGA** (CNCF; reuses Postgres): user→agent→tool/subgraph relations + delegation chains checked at the gateway per hop; SpiceDB alternative | 1 pod · plus |
| All agent traffic | **agentgateway** — one Gateway-API-conformant data plane; budgets compiled from CRs | 1–2 pods |
| Isolation | **agent-sandbox** CRD (gVisor/Kata) + default-deny NetworkPolicy (agent reaches only the gateway) | 0 pods |
| Supply chain | **cosign/sigstore** signed images/cards/ModelKits + built-in `ValidatingAdmissionPolicy` | 0 pods |
| Guardrails | Gateway filters | 0 pods |

### Authorization has three layers

1. **Build-time (k8s RBAC + CEL admission)**: who may create/edit CRs; "prod agents must have eval gates", "images must be signed".
2. **Runtime (ReBAC via OpenFGA)**: may user U invoke agent A; may agent A call tool T / read subgraph S / delegate to B on U's behalf. Delegation chain is a first-class relation → immediate revocation.
3. **Policy-as-config**: `requiresApproval`, budgets, environment scoping, content rules.

### Service mesh — deliberately optional, by architecture

Core needs no mesh: all agent traffic already crosses one gateway (L7 policy + SPIRE mTLS + observability). 2026 facts: Istio ambient is GA and cheap (ztunnel, ~90% less memory); Istio announced experimental agentgateway support (KubeCon EU 2026). An **`ambient` profile** adds ztunnel-only L4 mTLS for residual traffic (Postgres/NATS); waypoints never (agentgateway is the L7).

### Multi-tenancy — layered, mostly inherited

| Layer | Soft (teams) | Hard (external customers) |
|---|---|---|
| Control plane | namespace-per-tenant + Capsule policy | **vCluster** per tenant |
| Messaging/receipts | **NATS accounts** — per-tenant JetStream isolation | same |
| Data | **Postgres RLS** per tenant + per-tenant KG instances | same |
| Identity | Zitadel **organizations** ↔ tenants 1:1 | same |
| Traffic | Gateway partitions: routes, budgets, egress allowlists per tenant | same |

Enterprise `Tenant` CR = one-object fan-out (namespace/vCluster, NATS account, RLS role, IdP org, gateway partition, quotas).

### Compliance profiles — HIPAA as configuration, not a project

Most HIPAA technical safeguards are emergent: per-action attribution (on-behalf-of receipts), complete audit (receipt stream), access controls (ReBAC), encryption in transit (SPIRE/ambient). The `compliance: hipaa` pack configures the rest: hash-chained receipts + 6-year retention, **BAA model-egress allowlists at the gateway**, mandatory PHI redaction (gateway + ingestion normalize), automatic logoff, break-glass role, encryption-at-rest classes. Extends to SOC 2 / GDPR.

## 07 · Harness & observability

- **Sessions & receipts**: every A2A task through the gateway recorded to JetStream — request/response, tool calls, model calls, cost, identity chain. Replayable. One stream = audit log + debugging substrate + eval-data collector.
- **Traces**: OTel **GenAI semantic conventions**, adopted now with the convention version pinned (pre-stable in mid-2026). Gateway emits spans per hop (even uninstrumented agents get traces); OpenLLMetry adds interior spans.
- **Backend**: **OpenObserve** (single binary: metrics+logs+traces). **Phoenix** optional in plus.
- **Golden signals**: task success rate, tool-error rate, tokens/task, cost/task, latency/hop, loop depth, handoff count — alert rules shipped in the chart.

## 08 · Evals — semantic admission

Agents and models **earn traffic**. Rollout: new version → HELD (no traffic) → Eval Job (DeepEval/Inspect) fed by the ontology-derived golden set + replayed production sessions → gate (score ≥ threshold) → pass: gateway-weighted canary → 100%; fail: rollback with the report in CR status + PR. Production traffic continuously becomes regression data.

```yaml
kind: EvalSuite
spec:
  runner: deepeval
  dataset:
    goldenSetRef: pa-golden-v3
    fromSessions: {sample: 200, since: 7d}
  metrics: [task_completion, tool_correctness, faithfulness_to_kg, cost_regression]
  gate: {minScore: 0.85, blocking: true}
```

## 09 · Drift & self-healing

Detectors write conditions; controllers act; humans see semantic health in `kubectl get agents`.

| Drift | Detector | Action |
|---|---|---|
| KG drift (`KnowledgeStale`) | ontology probe failures; ingestion lag | trigger ingestion Workflow → alert |
| Model drift (`ModelDrifted`) | hourly canary prompts; embedded-output distance | pin previous model / fallback route |
| Behavioral (`Degraded`) | SLO burn on success rate, cost/task | auto-rollback to last eval-passing version |
| Embedding | centroid distance (core); Phoenix (plus) | re-index the KG |

## 10 · ModelHub (plus tier)

`Model` CR: train → package → eval-gate → serve; same EvalSuite gate as agents.

| Stage | Binding |
|---|---|
| Versioning/packaging | **KitOps ModelKits** (CNCF ModelPack) — OCI artifacts, `kit://` URIs, cosign-signed |
| Serving | **KServe** InferenceService fronting **Triton**/vLLM; **Kueue** for GPUs; llm-d for one giant model |
| Training | **Kubeflow Trainer v2** TrainJob (SFT/DPO/GRPO; Unsloth landing as BuiltinTrainer) |
| Experiments | MLflow (tracking only) |

## 11 · Developer experience — the golden path

Target: **empty directory → agent answering from a KG on a local cluster in under 15 minutes.** Lifecycle = five commands: `init → dev → invoke → build → deploy`.

- **Scaffolded**: `plume init` interview (what/which graph/which tools/which SDK — recommendations with reasoning, never silent) → ~100-line agent **the user owns**.
- **BYO**: per-SDK templates wrap existing agents in A2A + card + Dockerfile.
- **External**: `plume agent register --endpoint`.
- **Dev loop**: `plume dev` — k3d/minikube, hot reload, live receipts in the terminal; `plume eval run --quick`.
- **Deploy**: `plume build` (buildpacks/ko + cosign + SBOM) → `plume deploy` (GitOps commit; rollout streams the eval gate).
- **Workflows**: declarative YAML (compiled to DBOS) or code-first DBOS; `plume workflow test --event fixtures/x.json`.
- **Apps**: `plume init app` — frontend + typed client + IdP login wired.

Wizard rule everywhere: *the platform asks, you choose* — a blank field the user can't understand is a product failure.

## 12 · Component plan

Sizes: S <1wk · M 1–3wk · L 3–6wk (solo). Phases → §18.

| Component | Owns | Binds | Size | Phase |
|---|---|---|---|---|
| CLI | wizard, dev loop, build/deploy, kg/eval/session/drift verbs | cobra, buildpacks/ko, cosign | L | P1+ |
| SDK templates | A2A wrapper + card + Dockerfile per SDK | a2a-sdk | M | P1 |
| agent-operator | Agent controller: sandbox/deploy, SPIRE entries, routes, directory reg, rollout gating | kubebuilder, agent-sandbox | L | P1 |
| Policy compiler | Agent CR budgets/tools/guardrails + expose → gateway config | agentgateway CRDs | M | P1 |
| Receipt tap | gateway taps → receipt envelopes → JetStream | NATS | M | P1 |
| Agent directory | OASF records in JetStream KV; federates MCP Registry | OASF | S | P1 |
| Identity glue | SPIRE registrar, IdP bootstrap (Zitadel org/Keycloak realm), token exchange | SPIRE, Zitadel/Keycloak | M | P1 |
| Umbrella chart | tiers, profiles (local/prod), e2e CI on k3d+kind | Helm | M | P1 |
| Observability pack | OTel wiring (pinned semconv), dashboards, alerts | OpenObserve | M | P1+ |
| Connector CRD | 3 facets: MCP server, ingestion source, event receiver | MCP, CloudEvents, ESO | M | P2 |
| Ontology spec | KG contract schema: entities/relations/invariants/probes | — | M | P2 |
| Provider adapters | Graphiti first; cognee; BYO passthrough — 6-tool MCP surface | MCP | M ea. | P2 |
| Ingestion pipeline | acquire→extract→resolve→validate→write + extraction QA | DBOS/Argo | L | P2 |
| Probe engine | semantic-readiness runner + conditions | — | M | P2 |
| EvalSuite controller | eval Jobs, dataset builder, gate orchestration | DeepEval/Inspect | L | P3 |
| Session replay | receipts → replayable fixtures | NATS | M | P3 |
| Pack format + installer | signed OCI bundles; `plume pack` verbs; contract checks | OCI, cosign | M | P3 |
| Knowledge pattern packs | sop-decision-tree, policy-rules, episodic-memory, entity-catalog, qa-corpus | packs | M | P2–P3 |
| Drift controllers | canary prompts, SLO burn, embedding distance; remediations | OpenObserve | L | P4 |
| workflow-operator | Workflow CR → DBOS/Argo; JetStream triggers | DBOS | M | P4 |
| Loop governance | hop limits, A2A cycle detection, kill switch, approval interrupts | gateway+operator | M | P4 |
| App layer | HTTP projection, typed client gen, app templates, release pinning | kro, IdP | M | P4 |
| AuthzProvider (OpenFGA) | ReBAC model, gateway check integration | OpenFGA | M | P4 |
| model-operator | Model CR envelope | KServe, Trainer, KitOps | L | P5 |
| Tenant CR | tenancy fan-out | vCluster, Capsule | L | ent |
| Compliance profile packs | hipaa/soc2/gdpr | packs | M | ent |

### Connectors — MCP-only? Three planes, one rule

**MCP is the only agent-facing connector surface.** But "connector" is three jobs:

| Plane | Shape | Protocol |
|---|---|---|
| Tool | agent ↔ system, sync | **MCP only** (public MCP Registry = catalog) |
| Ingestion | system → KG, bulk | Workflow steps with native readers (results reach agents via the KG's MCP surface) |
| Event | system → platform, async | **CloudEvents → JetStream** → Workflows |

One `Connector` CR declares a system-of-record with three optional facets; credentials once (Secrets/ESO; OAuth token exchange for user-delegated).

## 13 · Graph engineering

### The ontology document

One reviewed YAML artifact per domain — entities, relations, invariants, probes. Everything derives from it.

### Creating a graph — three starting points, zero infrastructure

**You bring documents and review an ontology; Kubernetes runs everything else.** Conditions: `Ingesting → Validating → ProbesPassing → Ready`.

1. **From a knowledge pattern**: `plume kg init --pattern sop-decision-tree --from connector/sharepoint-sops`.
2. **From scratch with a proposed ontology**: sample 20–50 docs → platform *proposes* (with reasoning) → human edits → lock v1. Never silently generated.
3. **BYO graph**: wrap in a provider adapter; gains probes + versioning.

### Knowledge patterns (packs)

| Pattern | Source | Graph shape | Agents get |
|---|---|---|---|
| `sop-decision-tree` | SOPs, runbooks | walkable procedure graph (Step/Decision/BRANCH(condition)/Role/ON_EXCEPTION) | `next_step(state)` — walk, don't retrieve; path citations; invariants (Decision ≥2 branches, paths terminate) + one auto-probe per branch |
| `policy-rules` | policies, contracts | clause/applicability/supersession | "does X apply, effective when" |
| `episodic-memory` | tickets, incidents | bitemporal event graph | "what changed, what did we know when" |
| `entity-catalog` | product/org/CRM | entity–relation | lookups, joins, traversals |
| `qa-corpus` | FAQs, wikis | question-intent | grounded answers w/ citations |

### Ingestion pipeline

acquire (connector facet) → normalize (chunk·dedup·PII) → extract (ontology-guided LLM) → resolve entities → **two gates** (invariants hold? extraction P/R ≥ bar on a labeled sample?) → write + provenance (source·ts·bitemporal) → snapshot vN → probes → Ready. Failing batches are quarantined — never silently degrade a live graph.

### The provider adapter interface (the whole pluggability contract)

Six MCP tools: `search` · `neighbors` · `get_context_bundle` · `cite` · `schema` · `probe`. Beyond six = provider-specific bonus, invisible to portable agents.

### Operations

Snapshot per change (namespace-per-version); `plume kg diff v11 v12`; re-embedding = version bump; PII redacted at normalize; per-agent subgraph scoping enforced at the gateway.

## 14 · Loop engineering

Two loops, separated: the **inner loop** (user-owned scaffold) and the **governance ring** (platform-enforced at the data plane — holds for black-box agents).

- **Inner loop** (~100 lines, yours): assemble (card + KG bundle + skills) → act (via gateway) → observe → stop-check. Skills = versioned, governed instruction assets loaded per task by a router. Named termination reasons: `goal_met · budget · max_depth · timeout · interrupted`. Receipt per iteration.
- **Governance ring**: budgets (tokens/$/wall-clock) → terminate with reason `budget`; hop limit + A2A **cycle detection** via task-lineage headers (A→B→A killed); `requiresApproval` tools pause as durable Workflow steps (agent just sees a slow tool); kill switch (drain routes, scale 0); loop metrics (depth, retries, termination-reason) with shipped alert rules.

## 15 · Extension model — the pattern charter

**Two planes, hard boundary**: slow plane = engine (operators, gateway binding, substrate — ships rarely); fast plane = **technique plane** (ontologies, skills, policies, filters, drivers, templates — versioned data artifacts). New technique = artifact publish, never a platform release.

**Seven meta-patterns** (Claude Code is the model): microkernel · behavior-is-data · closed primitive set (**five primitives**: Agent/A2A, Tool/MCP, Event/CloudEvents, Resource/CRD, Artifact/OCI — a sixth needs a core RFC) · lifecycle hooks (pre-hop/post-hop; pre-reconcile/post-rollout/on-condition) · convention discovery (labeled CRs, well-known OCI paths) · versioned contracts N/N−1 + conformance · **packs**.

**Four sockets + triage**: capability → provider slot (shipped: KnowledgeGraphProvider, IdentityProvider, AuthzProvider; reserved: MemoryProvider, EvalRunner, DriftDetector, SandboxProfile) · traffic → gateway filter · lifecycle → new CRD + separate controller · practice → template/skill. Fits none → core RFC (must stay rare).

**Packs**: one signed OCI artifact bundling filters/skills/templates/drivers/CRs with a manifest declaring socket contracts. `plume pack install context-compaction` → filter live on all agents, template + skill indexed, no redeploys, rollback = uninstall.

**Enforcement**: every feature proposal names its pattern + socket; engine changes presumed wrong until an RFC proves the five primitives can't express it.

## 16 · Competitive positioning

| | kagent (CNCF/Solo.io) | Rossoctl (ex-Kagenti, IBM) | Dapr Agents | plume |
|---|---|---|---|---|
| Agent model | prompt+tools config run by own ADK engine | framework-neutral intercept | Python framework on actors | any container speaking A2A |
| Domain | none (ops tools) | knowledge-base service | none | **versioned KG contract, ontology-first** |
| Eval gating | — | — | — | **eval-as-admission + session replay** |
| Drift/self-heal | — | partial | retries | **semantic conditions + controllers** |
| Footprint | moderate | heavy (16GB/4c dev) | Dapr runtime | **≤8 pods, Postgres+NATS only** |
| UI stance | UI early | UI-led | n/a | CLI + security first |

kagent answers "how do I run an agent on k8s"; plume answers "how do I run an agent I can trust with my business" — and kagent agents can register on plume (they speak A2A). Palantir AIP validates ontology-first at $-scale; plume is its open, lightweight, k8s-native expression. Eval-gated rollout is 2026 best-practice *as SaaS + scripts*; nobody ships it as a k8s primitive — 12–18-month window.

### Open source and enterprise — the split

**Apache 2.0 core** (CNCF path open, no AGPL adoption tax, no BSL community damage). Open-core by component, never by crippling the core. **Single team free; organizational scale paid**:

- OSS: the full loop for one team — runtime, KG + patterns, evals + gating, drift, receipts, packs, CLI, exposure, Apps.
- Enterprise: `Tenant` CR + hard multi-tenancy fan-out, compliance profiles + audit reporting, SSO/SCIM, fleet management, advanced governance, support.
- **Never paywall correctness or safety** — evals, receipts, signing, drift stay open. Trust is the funnel.

## 17 · Weight budget — core tier

agent-operator 1 (the only plume-code pod) · agentgateway 1–2 · SPIRE 2 · Zitadel 1 (on our Postgres) · NATS 1 · Postgres 1 · OpenObserve 1 ≈ **8 pods**.

Runs anywhere: no LoadBalancer requirement, `local-path` storage, no managed-identity deps; sandbox degrades gracefully (`SandboxDowngraded` condition, never silent); named `--profile local` collapses replicas explicitly. CI runs e2e on k3d + kind every merge.

## 18 · Build order

1. **P1 (wks 1–3)**: agent-operator + CLI skeleton + substrate chart. *Demo: two agents from different SDKs collaborate, fully traced.*
2. **P2 (wks 4–6)**: KnowledgeGraph CRD + Graphiti provider + probes. *Demo: swap domains by swapping one CR.*
3. **P3 (wks 7–9)**: EvalSuite + gated rollout + session replay. *The flagship demo.*
4. **P4+**: drift → budgets/governance → App layer → authz. **P5**: modelhub. **ent**: Tenant, compliance.

Discipline: the differentiation only exists once P2/P3 ship — never polish the runtime layer at their expense.

## 19 · Risks held honestly

| Risk | Posture |
|---|---|
| OTel GenAI semconv churn | pre-stable; pin version, absorb renames |
| A2A maturity | gateway absorbs protocol quirks in one place |
| kro young | App layer last; plain CRs work without it |
| Rossoctl convergence on security layer | compose it if it stabilizes; moat is §04+§08+§09 |
| DBOS ceiling | Temporal is the known escape hatch — a wall to hit, not pre-build for |
| Eval-gating idea in the air | 12–18-month first-mover window → build order discipline |
| Zitadel AGPL | used unmodified/self-hosted (fine); Keycloak profile exists for allergic enterprises |
