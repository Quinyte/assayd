# assayd — Requirements

Derived from architecture v0.1 (2026-08-20). FR = functional, NFR = non-functional. Each requirement is testable; NFRs are release gates.

## Non-functional (the identity of the platform)

- **NFR-1 Weight**: core control plane ≤ 8 pods; exactly one pod of assayd-authored code in core. Every added pod requires a written justification in an ADR.
- **NFR-2 Stateful deps**: Postgres and NATS JetStream are the only stateful dependencies, ever.
- **NFR-3 Runs anywhere**: unmodified `helm install` on minikube, k3d, kind, k3s and any conformant cluster. CI runs full e2e on k3d + kind on every merge. No LoadBalancer/cloud-storage/managed-identity assumptions in core.
- **NFR-4 Time-to-first-agent**: empty directory → agent answering from a knowledge graph on a local cluster in **< 15 minutes**.
- **NFR-5 Open standards only**: A2A, MCP, OASF, SPIFFE, OTel GenAI (pinned semconv), Gateway API, CloudEvents, OCI. Zero invented protocols; zero bundled agent framework or UI in core.
- **NFR-6 Extension without release**: a new technique ships as a pack (signed OCI artifact) with no platform release and no agent redeploys.
- **NFR-7 Tiering**: `core` and `plus` independently installable/removable; enterprise modules separate.
- **NFR-8 Graceful degradation is visible**: any downgraded guarantee (e.g. sandbox fallback on local distros) surfaces as a CR condition, never silently.

## Functional

### Agents & runtime
- **FR-1**: An agent is any container serving A2A + an Agent Card; registration wires identity (SPIRE SVID), gateway routes, and an OASF directory entry with no agent-code changes.
- **FR-2**: External A2A endpoints can register as agents without running on the cluster.
- **FR-3**: Agent sandboxing via agent-sandbox CRD profiles (gVisor/Kata), with visible downgrade (NFR-8).

### Knowledge
- **FR-10**: KnowledgeGraph CR binds a provider behind the 6-tool MCP surface (`search, neighbors, get_context_bundle, cite, schema, probe`).
- **FR-11**: Graphs are versioned as immutable snapshots; agents pin versions; `kg diff` renders deltas for review.
- **FR-12**: Semantic readiness — a graph is Ready only when its probe set passes; non-Ready graphs receive no agent traffic.
- **FR-13**: Graph creation paths: (a) knowledge-pattern pack + connector, (b) proposed ontology from samples (propose → human review → lock, never silent), (c) BYO graph wrapped in an adapter.
- **FR-14**: Ingestion pipeline with two gates (ontology invariants; extraction precision/recall vs labeled sample); failing batches quarantine.
- **FR-15**: Knowledge patterns shipped as packs; `sop-decision-tree` produces walkable procedure graphs with `next_step(state)`, auto-derived invariants and per-branch probes.

### Correctness
- **FR-20**: Eval-as-admission — a new Agent/Model version receives no traffic until its EvalSuite passes; pass → gateway-weighted canary → full; fail → rollback with report in CR status.
- **FR-21**: Recorded sessions are replayable and samplable into eval datasets.
- **FR-22**: Drift detectors (KG probes, canary prompts, SLO burn, embedding distance) write CR conditions; controllers remediate (re-ingest, pin model, rollback); recovery is re-verified.

### Security & governance
- **FR-30**: Every agent pod holds a SPIRE SVID; humans authenticate via the IdP slot (Zitadel default, Keycloak profile); user tokens exchange into on-behalf-of agent credentials.
- **FR-31**: Every hop crosses the gateway and emits a receipt (request/response, tools, models, cost, identity chain) to JetStream.
- **FR-32**: Budgets (tokens/$/time) declared on the Agent CR are enforced at the gateway; overruns terminate with a named reason.
- **FR-33**: Loop governance: hop limits, A2A cycle detection via task lineage, `requiresApproval` pauses as durable workflow steps, kill switch.
- **FR-34**: Three authz layers: k8s RBAC + CEL admission (build-time); ReBAC via AuthzProvider slot (OpenFGA default) checked per hop; policy-as-config.
- **FR-35**: Supply chain: signed images/cards/ModelKits; unsigned rejected by admission.

### Exposure & apps
- **FR-40**: `expose` blocks publish Agents (A2A/MCP), Workflows (MCP tools, REST/SSE), KGs (read-only MCP) via the gateway; external consumers are authenticated principals with budgets and receipts.
- **FR-41**: App CR = frontend + generated API (workflow POST endpoints, agent SSE chat) + pinned member versions, released/rolled back as a unit; typed client generated from schemas.

### Workflows
- **FR-50**: Declarative Workflow CRs compile to DBOS programs; code-first DBOS supported; triggers = JetStream events, cron, A2A tasks; local test with injected fixture events.

### Enterprise
- **FR-60**: Tenant CR fans out namespace/vCluster, NATS account, Postgres RLS role, IdP org, gateway partition, quotas.
- **FR-61**: Compliance profiles (hipaa first) as packs: hash-chained receipts + retention, model-egress allowlists at the gateway, mandatory redaction, break-glass with audit.

## Explicit non-goals (v1)
- No bundled UI (CLI first; App frontends are user-owned).
- No proprietary agent framework or protocol.
- No mesh in core (`ambient` ztunnel profile only).
- No Temporal (DBOS; Temporal is the documented escape hatch).
