# Design 01: KnowledgeGraphProvider contract (`kgp/v1alpha1`)

- **Status**: **revised r2** — independent re-critique (reviews/01-recritique.md): 1 blocker + 9 findings, all addressed · ADR-0017
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
http://<provider-svc>/kgp/<graph>/admin/<version>/mcp   # per-version admin: write_batch, load_artifact,
                                                        # commit_version (r2 f1 — BLOCKER fix)
http://<provider-svc>/kgp/<graph>/admin/mcp             # graph-level admin: begin_version, list_versions,
                                                        # promote, drop_version — platform identity only
```

The Agent CR binding `{name, version}` compiles to a route to that exact endpoint. An agent **has no vocabulary to name a version** at call time — caching is trivial, and `kg diff` compares two endpoints.

*Prior art, and the honest limit of the claim.* Version-in-the-path over a knowledge graph is established practice: [Snowstorm](https://github.com/IHTSDO/snowstorm/blob/master/docs/code-systems-and-branches.md) serves SNOMED CT at `MAIN/2021-07-31/...` and already separates consumption from authoring paths ("Working branches should NOT be used to access versioned content"). Path-scoped MCP endpoints as an isolation boundary are documented for *tenants*. Declared capability profiles behind an engine-agnostic port is [SPARQL 1.1 Service Description](https://www.w3.org/TR/sparql11-service-description/), standardized 2013. What is not established is the assembly, and specifically **lifecycle-gated bindability** — `staging` and `quarantined` versions are not routable to agents at all, so demotion is de-registration rather than a permission edit. Note also that [capability URLs leak](https://www.w3.org/TR/capability-urls/) through logs and history: the guarantee is that an agent cannot *name* another version, not that one is unreachable by other means. Say the narrower thing.
`version: active` is permitted only in the `local` profile (dev convenience); prod bindings must pin.

*Rejected alternatives*: version as a tool parameter (agents can wander; every tool schema polluted); gateway header injection (invisible in traces; harder to reason about).

### 3.3 Query surface — 6 tools (closed set)

Anything beyond these six is provider-specific bonus, invisible to portable agents (ADR-0005).

| Tool | Input (required → optional) | Output |
|---|---|---|
| `kg.search` | `query:str` → `top_k:int=8, entity_types:[str], filters:{attr:val}` | `hits: [{node_id, type, name, snippet, score, provenance_ref}]` |
| `kg.neighbors` | `node_id:str` → `relations:[str], direction:in\|out\|both, depth:int≤2, limit:int=50` | `edges: [{from, rel, to, attrs, valid_from?, valid_to?, provenance_ref}]` |
| `kg.get_context_bundle` | `bundle:str, params:{…}` → `token_budget:int=2000` | `{text:str, citations:[provenance_ref], structure?:json}` |
| `kg.cite` | `provenance_ref:str` | `{source_uri, source_span?, ingested_at, valid_from?, valid_to?, pipeline_run}` |
| `kg.schema` | — | the ontology document (§3.5), verbatim, plus `{contract: "kgp/v1alpha1", version, state, profile, pattern?, embedder: {model, dim}}` — **`state` ∈ `staging|active|superseded|quarantined`** (r2 f2), `profile ∈ query|full` (r2 f7) — the embedder is version metadata so re-embedding is a version bump by construction |
| `kg.probe` | `set:str="all"` | `{results:[{id, pass, expected, actual, latency_ms}], pass_rate:float}` |

Notes:
- `kg.get_context_bundle` is the workhorse: agents call **named recipes**, not raw traversals. `structure` carries machine-readable payloads (e.g. the decision node for `next_step`), `text` carries the LLM-ready rendering within `token_budget`.
- `kg.probe` is in the query surface so readiness, `plume kg probe`, and conformance share one path — but gateway policy scopes it to platform identities (agents never call it).
- Every fact-bearing output carries a `provenance_ref`; `kg.cite` resolves it. No uncited facts can leave the provider.
- **Errors are a closed set** (MCP error `data.code`): `KG_NOT_FOUND · KG_SCOPE_DENIED · KG_VERSION_GONE · KG_VERSION_NOT_STAGING · KG_VERSION_IN_USE · KG_VERSION_CONFLICT · KG_BUNDLE_UNKNOWN · KG_UNAVAILABLE · KG_COMMIT_REJECTED` (violations in `data.violations`). `KG_BUDGET_EXCEEDED` is **removed** — budgets are the gateway's, never the provider's (r2 f6) — portable handling, no provider-specific error parsing.
- **Pagination**: v1alpha1 is `limit`-only by design; cursor pagination is a tracked v1beta1 item (review 01, finding 4).

### 3.4 Admin surface — 7 tools (platform-only)

Called by the ingestion pipeline (design 14) and the operator. Same MCP transport, separate endpoint + authz scope (SPIRE platform identity only; never routed to agents).

| Tool | Purpose |
|---|---|
| `kg.admin.begin_version` | *(graph-level)* `begin_version(from: vN | empty)` — the caller names the **source**, the provider allocates the **number**. **Single-flight per graph** — a concurrent call returns `KG_VERSION_CONFLICT` naming the in-flight version. The provider allocates the number; callers never propose one (r2 f5) |
| `kg.admin.list_versions` | *(graph-level)* `[{version, state, created_at, promoted_at, embedder, element_counts?}]` — the enumeration bind-validation, `kg diff` and retention all need (r2 f3) |
| `kg.admin.write_batch` | *(per-version path)* upsert nodes/edges with mandatory `provenance`; idempotent by `(batch_id, seq)`. **Precondition: target version MUST be `staging`** — any other state refused with `KG_VERSION_NOT_STAGING` (r2 f1). Concurrent batches merge last-write-wins per element key; each batch is atomic within itself (r2 f5) |
| `kg.admin.commit_version` | *(per-version path)* **precondition: target MUST be `staging`** — `KG_VERSION_NOT_STAGING` otherwise, so invariants can never re-run against a live graph (r2 R1). Runs ontology invariants; **fail → version quarantined**, never promoted; returns violation list |
| `kg.admin.promote` | *(graph-level)* mark `vN+1` active **atomically** — the previous active becomes `superseded` in the same operation, so there is never a window with two active versions or none (r2 f5). Readiness still requires probes — the operator's call |
| `kg.admin.load_artifact` | bulk load from a signed OCI artifact ref (nodes/edges/provenance in a columnar layout) — the high-throughput ingestion path; same invariant gate at commit |
| `kg.admin.drop_version` | *(graph-level)* delete a version. **In-use interlock (r2 f4)**: refused with `KG_VERSION_IN_USE` while any Agent CR binds it (the operator passes its bindings) or an in-flight query holds it; the guard reads the authoritative version record, never a replica cache. Pinned-version retention pressure surfaces as a KG CR condition |

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

1. **Bind**: agent-operator reconciles a `knowledge[]` binding → validates via `kg.schema` that the endpoint serves `kgp/v1alpha1`, declares profile `query` or `full`, and reports the bound version in state **`active` or `superseded`** — `staging` and `quarantined` are refused (r2 f2/R4) → compiles gateway route + scope filters → condition `KnowledgeBound`.
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
2. `kg.search` recall **≥ 0.9 on the seeded known-answer set** (stated so implementers do not each pick a gate).
3. `kg.neighbors` exact edge sets; depth/direction/limit honored.
4. `kg.get_context_bundle`: every built-in recipe; determinism (same version+params → same citation set); budget truncation flagged.
5. `kg.cite` resolves every ref emitted by 2–4; no uncited facts. Every closed-set error code — pre-existing and new — has an assertion that the provider returns it in its stated condition (r2 R3).
6. `kg.probe` executes the fixture probe set with correct pass/fail.
7. **Version isolation**: write v2, then assert v1's *bundle* outputs are byte-stable and its `search`/`neighbors` result **sets** are unchanged — bundles are the only surface the contract mandates determinism for, so byte-stability elsewhere would fail a conforming provider (r2 R3).
8. Admin lifecycle: begin→write→commit(with a deliberate invariant violation → quarantine)→promote.
9. Latency budget (warm) on the fixture **and** on a 100k-element synthetic corpus — the small fixture alone cannot distinguish an implementation that collapses at scale: search p95 < 800ms, bundle p95 < 1.5s (advisory in v1alpha1).
10. Portability check: warns on any `portable: false` ontology elements.

A provider **supports `kgp/v1alpha1` at profile `query`** iff points 1–7, 9 and 10 pass, and **at profile `full`** iff point 8 (the admin lifecycle) also passes (A5.4). Binding and querying require `query`; the version-mutating platform features (designs 14, 15) require `full`. The suite ships with the contract, versioned together.

## 9. Resolved questions (decided 2026-08-20)

**Q1 — Managed default backend: FalkorDB, eyes open on license.** FalkorDB is **SSPLv1 — source-available, not OSI open source** (its own docs are explicit). Why it still works as the default: (a) self-hosted/internal use — the overwhelming plume case — carries no SSPL obligations; (b) plume never modifies or redistributes it, so our Apache-2.0 core is untouched; (c) the SSPL service clause targets offering *the database itself* as a service, not apps using it internally — though conservative legal teams ban SSPL wholesale, which is why (d) the Neo4j CE (GPLv3) profile is one value flip away, and the caveat is documented, not buried. **No restriction/lock-in, at three levels**: backend swaps per graph within the Graphiti adapter (`falkordb | neo4j | neptune`); the provider swaps within kgp (`graphiti | cognee | byo`); and the contract itself is versioned with a conformance suite so third parties add providers. A future Apache AGE provider (graph inside our Postgres, zero new deps, no SSPL) stays on the roadmap as the doctrine-pure option.

**Q2 — Admin protocol: MCP only; no dual protocol.** Considered supporting both MCP and gRPC — rejected: two protocols mean two authz paths, two receipt paths, two conformance suites, which is precisely the weight failure doctrine rules 1/3 exist to stop. The throughput case gRPC would serve is served instead by **`kg.admin.load_artifact`** (bulk load from a signed OCI artifact — and it uses the Artifact primitive we already have). **Revisit trigger, measured not vibed**: if a real corpus load via `write_batch`/`load_artifact` cannot sustain ingestion at the scale of ~100k documents in a nightly window, a gRPC bulk lane becomes a v1beta1 proposal.

**Q3 — Escape hatches: allowed, marked `portable: false`**, conformance warns. As recommended.
## 10. Resulting ADRs

Recorded: ADR-0017 (kgp/v1alpha1 contract: **6 query + 7 admin** tools, version-scoped query *and* mutating-admin endpoints, `ontology/v1`) · ADR-0018 (Q1 decision) · note in ADR-0003 that MCP target is spec 2026-07-28.

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
- **A2 (2026-08-20, from design 11 r1 f2)**: admin-surface authorization is **per-tool, not all-or-nothing**: the gateway may grant a workload principal a route scoped to a single admin tool on a single staging version (e.g. a continuous-ingestion reader gets `write_batch` on `<graph>@vN+1` only); `begin/commit/promote/drop` remain platform-identity-only. §3.4's "platform identity only" reads as the *default*, with compiler-emitted narrow grants as the exception.
- **A3 (2026-08-20, from design 19 r1 f1)**: the conformance suite's fixture ontology ("clinic-sop") gains a **full walk-recipe case** — execute the pattern's `next_step` bundle from entry and from a mid-machine `state`, asserting the branch node, rendered conditions in `structure`, and citations — so no provider can pass conformance without executing the flagship traversal.
- **A4 (2026-08-20, from architecture-v1 review F13)**: the Graphiti appendix below is **superseded by design 13 r2** on two points and is retained only as the original sketch: (a) versions are **backend keys per version** copied by `GRAPH.COPY`, not `group_id` namespaces; (b) `write_batch` is a **structured node/edge upsert** — the adapter is LLM-free and never calls `add_episode` on the ingestion path, because extraction belongs to design 14 where Gate A can precede the write.
- **A5 (2026-08-22, from the independent re-critique — reviews/01-recritique.md)**: five contract changes, listed together because they interlock.
  1. **BLOCKER f1 — the admin surface is version-scoped.** Mutating tools (`write_batch`, `load_artifact`, `commit_version`) live at `/kgp/<graph>/admin/<version>/mcp`, so A2's per-version grants are enforceable **by route** — the gateway never needs to read a body to know which version a write targets. Lifecycle tools (`begin_version`, `list_versions`, `promote`, `drop_version`) stay graph-level under platform identity. Defence in depth: `write_batch`/`load_artifact` accept **only `staging` versions** and refuse anything else with `KG_VERSION_NOT_STAGING`. Without this a pack-provided reader image (design 11) could write into the *active* graph — silently, since nothing errored and nothing alerted.
  2. **f2 — versions carry state.** `staging | active | superseded | quarantined`, exposed in `kg.schema`. **Bind validation refuses anything but `active` or `superseded`**; `quarantined` is reachable only under platform identity for inspection. Pinned binds previously bypassed the entire promotion gate.
  3. **f3/f4 — `list_versions` and the drop interlock**, as tabled above. The surface is now **6 query-side + 7 admin-side** — stated once in the §3.3 and §3.4 headings, which are authoritative (f9/R7).
  4. **f7 — conformance profiles.** `query` (tools 1–7 + the A1 scope battery + A3's walk case) and `full` (adds the admin lifecycle). A provider declares its profile in `kg.schema`; **binding and querying require `query`, version-mutating platform features (designs 14, 15) require `full`.** Without this, a read-only corporate graph — the archetypal BYO case §9 promises — could never claim support.
  5. **f10 — §3.5's ontology sketch is superseded by design 12** (`ontology/v1` normative: no `ref()` attrs, canonical triple relation refs, `via` required on probes, `health.pass_threshold`). It is retained as illustration only.
- **A6 (2026-08-22, f8 — conformance suite gaps)**: the suite additionally asserts version **state transitions** (staging → active → superseded), that a `quarantined` version is not bindable, `list_versions` completeness after a fork, drop-interlock refusal, and the closed-error mapping for each new code — the four ways a non-conforming provider would previously have passed.
- **A7 (2026-08-22, r2 R6)**: single-flight `begin_version` interacts with design 14's review park (up to `reviewTimeout: 72h`). Rule: a parked build **holds** the graph's staging slot, and a competing `begin_version` — including design 20's automatic `KnowledgeStale` rebuild — receives `KG_VERSION_CONFLICT` naming the parked build. The operator's response is **queue, never supersede**: the rebuild is recorded as pending on the KG CR (`RebuildQueued`) and launches when the park resolves or times out. Recorded here because neither design 14 nor design 20 carried the path.
