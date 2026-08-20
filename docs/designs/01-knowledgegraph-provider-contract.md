# Design 01: KnowledgeGraphProvider contract (`kgp/v1alpha1`)

- **Status**: **approved** (2026-08-20, Q1–Q3 resolved below)
- **Phase**: P2 · **Size**: M · **Author**: design session 2026-08-20 · **Date**: 2026-08-20
- **ADRs**: 0005 (KG domain contract), 0003 (open standards), 0008 (provider slots)

## 1. Purpose & scope

The single interface every graph provider implements so that agents, the probe engine, the eval dataset builder, and the ingestion pipeline are provider-agnostic. This contract is the platform's highest-stakes interface: agents and packs are written against it, so it must survive provider churn (ADR-0008: contracts versioned N/N−1 with conformance).

**In scope**: the query surface (agent-facing), the admin surface (platform-facing), the ontology document schema, version semantics, the conformance suite, and the Graphiti reference mapping.
**Out of scope**: the ingestion pipeline internals (design 14), the probe engine scheduling (design 15), pattern pack contents (design 19).

## 2. Doctrine & charter gates

- **Plane**: slow — this is a socket *contract* (the rare, legitimate engine-side artifact). Implementations (adapters) are fast-plane.
- **Socket**: provider slot #1. Contract id `kgp/v1alpha1`; support window N/N−1.
- **Pods added**: 0 by the contract itself. Managed-mode providers add pods per graph (see §9 — contested).
- **Stateful deps**: none added by the contract. Managed backends are per-graph workload state, not platform substrate (interpretation of doctrine rule 2 — flagged in §9).
- **Primitives**: Tool (MCP), Resource (CRD), Artifact (OCI ontology/packs). No new primitives.

## 3. Interfaces

### 3.1 Transport

MCP, targeting the **2026-07-28 spec** (stateless core). Consequences we exploit:

- **Stateless request/response** fits version-scoped endpoints exactly — no session affinity anywhere.
- **`Mcp-Method` / `Mcp-Name` headers** let agentgateway route, meter, and budget `kg.*` calls without parsing bodies (policy compiler emits header-match rules).
- **`ttlMs`/`cacheScope` on `tools/list`** — providers advertise long TTLs since a version's tool surface is immutable.
- The gateway provides the 2025-era compat shim for older SDK clients; adapters implement only 2026-07-28.

### 3.2 Version-scoped endpoints (decision)

Every graph **version** is its own MCP endpoint:

```
http://<provider-svc>/kgp/<graph>/<version>/mcp     # query surface
http://<provider-svc>/kgp/<graph>/admin/mcp         # admin surface (not versioned; operates ON versions)
```

The Agent CR binding `{graphRef: {name, version}}` compiles to a route to that exact endpoint. An agent **cannot** name a version at call time — crossing versions is structurally impossible, caching is trivial, and `kg diff` compares two endpoints.
`version: active` is permitted only in the `local` profile (dev convenience); prod bindings must pin.

*Rejected alternatives*: version as a tool parameter (agents can wander; every tool schema polluted); gateway header injection (invisible in traces; harder to reason about).

### 3.3 Query surface — six tools (closed set)

Anything beyond these six is provider-specific bonus, invisible to portable agents (ADR-0005).

| Tool | Input (required → optional) | Output |
|---|---|---|
| `kg.search` | `query:str` → `top_k:int=8, entity_types:[str], filters:{attr:val}` | `hits: [{node_id, type, name, snippet, score, provenance_ref}]` |
| `kg.neighbors` | `node_id:str` → `relations:[str], direction:in\|out\|both, depth:int≤2, limit:int=50` | `edges: [{from, rel, to, attrs, valid_from?, valid_to?, provenance_ref}]` |
| `kg.get_context_bundle` | `bundle:str, params:{…}` → `token_budget:int=2000` | `{text:str, citations:[provenance_ref], structure?:json}` |
| `kg.cite` | `provenance_ref:str` | `{source_uri, source_span?, ingested_at, valid_from?, valid_to?, pipeline_run}` |
| `kg.schema` | — | the ontology document (§3.5), verbatim, plus `{contract: "kgp/v1alpha1", version, pattern?, embedder: {model, dim}}` — the embedder is version metadata so re-embedding is a version bump by construction |
| `kg.probe` | `set:str="all"` | `{results:[{id, pass, expected, actual, latency_ms}], pass_rate:float}` |

Notes:
- `kg.get_context_bundle` is the workhorse: agents call **named recipes**, not raw traversals. `structure` carries machine-readable payloads (e.g. the decision node for `next_step`), `text` carries the LLM-ready rendering within `token_budget`.
- `kg.probe` is in the query surface so readiness, `plume kg probe`, and conformance share one path — but gateway policy scopes it to platform identities (agents never call it).
- Every fact-bearing output carries a `provenance_ref`; `kg.cite` resolves it. No uncited facts can leave the provider.
- **Errors are a closed set** (MCP error `data.code`): `KG_NOT_FOUND · KG_SCOPE_DENIED · KG_VERSION_GONE · KG_BUNDLE_UNKNOWN · KG_BUDGET_EXCEEDED` — portable handling, no provider-specific error parsing.
- **Pagination**: v1alpha1 is `limit`-only by design; cursor pagination is a tracked v1beta1 item (review 01, finding 4).

### 3.4 Admin surface — five tools (platform-only)

Called by the ingestion pipeline (design 14) and the operator. Same MCP transport, separate endpoint + authz scope (SPIRE platform identity only; never routed to agents).

| Tool | Purpose |
|---|---|
| `kg.admin.begin_version` | open staging version `vN+1` (from empty or copy-on-write of `vN`) |
| `kg.admin.write_batch` | upsert nodes/edges with mandatory `provenance` per element; idempotent by `(batch_id, seq)` |
| `kg.admin.commit_version` | run ontology invariants; **fail → version quarantined**, never promoted; returns violation list |
| `kg.admin.promote` | mark `vN+1` active (readiness still requires probes — operator's call, not the provider's) |
| `kg.admin.load_artifact` | bulk load from a signed OCI artifact ref (nodes/edges/provenance in a columnar layout) — the high-throughput ingestion path; same invariant gate at commit |
| `kg.admin.drop_version` | delete a non-active version (retention policy enforced by operator) |

*Rejected alternative*: gRPC admin API — a second protocol for no capability gain; MCP keeps ingestion steps tool-callable and receipts uniform.

### 3.5 Ontology document (`ontology/v1`)

The reviewed, versioned domain contract. Stored as the CR-referenced ConfigMap/OCI artifact; served verbatim by `kg.schema`.

```yaml
ontology: payer-policies
version: v12
pattern: policy-rules            # optional lineage to a knowledge pattern pack
entities:
  - name: Policy
    keys: [policy_id]
    attrs: {effective_from: date, state: "enum[draft,active,retired]"}
relations:
  - "Policy COVERS Procedure"    # attrs allowed: {prior_auth: bool, age_range: str}
  - "Policy SUPERSEDES Policy"
invariants:                      # closed set of built-in types + escape hatch
  - {type: required_edge, subject: "Policy[state=active]", rel: COVERS, min: 1}
  - {type: acyclic, rel: SUPERSEDES}
  - {type: unique_key, entity: Policy}
  # - {type: custom, provider_query: "...", portable: false}   # escape hatch, marked
bundles:                         # named recipes agents call via kg.get_context_bundle
  - name: coverage_answer
    recipe: neighborhood_summary
    params: {root: "Policy", radius: 2}
  - name: next_step              # from sop-decision-tree pattern
    recipe: walk
    params: {machine: "Procedure", edge: "BRANCH", condition_attr: "condition"}
probes:
  - id: p-001
    q: "Does policy P-1042 cover CPT 97110 without prior auth?"
    expect: {answer: "no", cites: ["P-1042#4.2"]}
```

**Built-in invariant types (v1)**: `required_edge`, `acyclic`, `path_terminates`, `unique_key`, `cardinality`. **Built-in bundle recipes (v1)**: `walk`, `subtree`, `neighborhood_summary`, `timeline` — all **deterministic graph operations**: `text` is template-rendered from graph data, never LLM-generated provider-side (determinism is a conformance requirement; LLM rendering is the agent's job). Both sets are extended only by contract revision — `custom`/`raw` escape hatches exist but are marked `portable: false` and fail conformance's portability check (allowed, visible, discouraged).

## 4. Behavior

Main paths:

1. **Bind**: agent-operator reconciles `graphRef` → validates the endpoint serves `kgp/v1alpha1` at that version (via `kg.schema`) → compiles gateway route + scope filters → condition `KnowledgeBound`.
2. **Query**: agent → gateway (authz, budget, receipt, `Mcp-Method` metering) → version endpoint → response with provenance refs.
3. **Ingest**: pipeline → `begin_version` → `write_batch`× → `commit_version` (invariants) → operator runs probes → `promote` + `Ready`, else quarantine + `KnowledgeStale` stays.
4. **Rollback**: repoint bindings to `vN−1` endpoint (pure routing; provider untouched).

## 5. Failure modes & degraded states

| Failure | Behavior |
|---|---|
| Provider unreachable | Gateway returns typed MCP error; binding condition `KnowledgeBound=False`; agents bound to it are `Degraded`, receive no traffic if the graph is their only knowledge source |
| Commit invariant violation | Version quarantined with violation list in KG CR status; active version untouched |
| Probe regression on active version (drift) | `KnowledgeStale` condition; drift controller path (design 20) |
| Bundle over token_budget | Provider truncates by recipe-defined priority and sets `truncated: true` — never silent |
| Partial `write_batch` crash | Idempotent replay by `(batch_id, seq)`; DBOS resume on pipeline side |

## 6. Security

- Query surface reachable **only** via the gateway; per-agent scoping = entity-type/subgraph allowlist filters compiled from the Agent CR (ReBAC relation checks when AuthzProvider lands, design 24).
- Admin surface: SPIRE platform identity only; never in any agent route table.
- `kg.cite` can expose source content → gated by the same scope filters; compliance profiles may force cite-through-redaction.
- Provenance stores pointers to sources, not copies of sensitive payloads (ADR-0014).

## 7. Observability

Per-tool metrics at the gateway (count, latency, tokens-of-context served, truncation rate) keyed by `Mcp-Name`; KG CR conditions (`Ingesting/Validating/ProbesPassing/Ready/KnowledgeStale`); every query lands in the receipt stream (existing). Shipped alerts: probe pass-rate drop, bundle truncation spike, admin quarantine event.

## 8. Testing & conformance

`plume-kgp-conformance` (container + `plume kgp conformance --endpoint …`): runs a fixture ontology ("clinic-sop", ~40 nodes) + seeded corpus against any endpoint:

1. `kg.schema` round-trips the ontology; contract id correct.
2. `kg.search` recall ≥ threshold on seeded known-answer queries.
3. `kg.neighbors` exact edge sets; depth/direction/limit honored.
4. `kg.get_context_bundle`: every built-in recipe; determinism (same version+params → same citation set); budget truncation flagged.
5. `kg.cite` resolves every ref emitted by 2–4; no uncited facts.
6. `kg.probe` executes the fixture probe set with correct pass/fail.
7. **Version isolation**: write v2, assert v1 responses byte-stable.
8. Admin lifecycle: begin→write→commit(with a deliberate invariant violation → quarantine)→promote.
9. Latency budget (warm): search p95 < 800ms, bundle p95 < 1.5s (advisory in v1alpha1).
10. Portability check: warns on any `portable: false` ontology elements.

A provider "supports kgp/v1alpha1" iff the suite passes. The suite ships with the contract, versioned together.

## 9. Resolved questions (decided 2026-08-20)

**Q1 — Managed default backend: FalkorDB, eyes open on license.** FalkorDB is **SSPLv1 — source-available, not OSI open source** (its own docs are explicit). Why it still works as the default: (a) self-hosted/internal use — the overwhelming plume case — carries no SSPL obligations; (b) plume never modifies or redistributes it, so our Apache-2.0 core is untouched; (c) the SSPL service clause targets offering *the database itself* as a service, not apps using it internally — though conservative legal teams ban SSPL wholesale, which is why (d) the Neo4j CE (GPLv3) profile is one value flip away, and the caveat is documented, not buried. **No restriction/lock-in, at three levels**: backend swaps per graph within the Graphiti adapter (`falkordb | neo4j | neptune`); the provider swaps within kgp (`graphiti | cognee | byo`); and the contract itself is versioned with a conformance suite so third parties add providers. A future Apache AGE provider (graph inside our Postgres, zero new deps, no SSPL) stays on the roadmap as the doctrine-pure option.

**Q2 — Admin protocol: MCP only; no dual protocol.** Considered supporting both MCP and gRPC — rejected: two protocols mean two authz paths, two receipt paths, two conformance suites, which is precisely the weight failure doctrine rules 1/3 exist to stop. The throughput case gRPC would serve is served instead by **`kg.admin.load_artifact`** (bulk load from a signed OCI artifact — and it uses the Artifact primitive we already have). **Revisit trigger, measured not vibed**: if a real corpus load via `write_batch`/`load_artifact` cannot sustain ingestion at the scale of ~100k documents in a nightly window, a gRPC bulk lane becomes a v1beta1 proposal.

**Q3 — Escape hatches: allowed, marked `portable: false`**, conformance warns. As recommended.
## 10. Resulting ADRs

Recorded: ADR-0017 (kgp/v1alpha1 contract: 6+5 tools, version-scoped endpoints, ontology/v1) · ADR-0018 (Q1 decision) · note in ADR-0003 that MCP target is spec 2026-07-28.

## Appendix: Graphiti reference mapping

The adapter wraps **graphiti-core as a library** in the provider pod (Graphiti's own MCP server is memory-shaped — `add_episode`/`search_nodes` — not this contract).

| kgp tool | Graphiti implementation |
|---|---|
| `kg.search` | hybrid search (semantic + BM25 + graph) over nodes/facts, mapped to hits with episode-derived `provenance_ref` |
| `kg.neighbors` | typed edge traversal via the backend driver, bitemporal fields passed through (`valid_from/to` ↔ `t_valid/t_invalid`) |
| `kg.get_context_bundle` | adapter-side recipe engine over Graphiti queries (walk/subtree/summary/timeline) |
| `kg.cite` | episode + source metadata (bitemporal ingestion record) |
| `kg.schema` | stored ontology artifact (adapter-held, not derived from Graphiti) |
| `kg.probe` | adapter executes probe queries via the same paths agents use |
| admin surface | version = backend namespace/`group_id` per version; `write_batch` → Graphiti bulk add with entity resolution; invariants run as adapter queries at commit |

## 11. Amendments

- **A1 (2026-08-20, from design 03 r1 finding 6)**: KG scope enforcement mechanism made precise: the gateway **injects** a signed `X-Plume-KG-Scope` header (it does not parse MCP bodies — consistent with §3.1); **providers enforce scope across all four fact-bearing tools** (`search`, `neighbors`, `get_context_bundle`, `cite`) answering `KG_SCOPE_DENIED` for out-of-scope access. Consequence: providers are scope-enforcing, and the **conformance suite gains a scope-enforcement battery** (out-of-scope search/traversal/bundle/cite must deny). §6's "enforced at the gateway" reads as "gateway-injected, provider-enforced".
