# Design 13: Graphiti provider adapter (first kgp/v1alpha1 implementation)

- **Status**: revised r2 — awaiting re-critique (r1 REVISE, 4 findings addressed; reviews/13-review.md)
- **Phase**: P2 · **Size**: M · **Date**: 2026-08-20
- **ADRs**: 0017 (contract), 0018 (FalkorDB default, license caveat) · interfaces: 01 (implements it), 12 (consumes Pydantic derivations), 14 (admin surface caller), 15 (probe execution)
- **Research**: `docs/research/connectors-2026-08.md` §graphiti (Pydantic custom types; `add_episode_bulk` empty-graph constraint; `group_id` namespacing)

## 1. Purpose & scope

The reference kgp provider: a container wrapping **graphiti-core as a library** (its own MCP server is memory-shaped — ADR-0017 appendix) that implements the full contract: 6 query tools, 6 admin tools, version-scoped endpoints, scope enforcement, probes. Also the managed-mode packaging (adapter + FalkorDB per graph). Passing the kgp conformance suite **is** this design's definition of done.

## 2. Doctrine & charter gates

- **Plane**: fast — a provider implementation behind the slot; ships as a signed image referenced by the KG CR (`provider: graphiti`), deployed per managed graph.
- **Pods**: workload pods per managed graph (adapter 1 + FalkorDB 1 — ADR-0018's recorded cost); BYO endpoint mode adds none. **Stateful deps**: FalkorDB is per-graph workload state, not substrate (ADR-0018). **Primitives**: Tool (MCP), Artifact (image). ✓

## 3. Mapping the contract onto graphiti

### 3.1 Versions = `group_id` namespaces

**Contract property: a version fork is a deterministic data-level copy — never re-extraction** (r1 f2). Backend mapping is an adapter detail: on **FalkorDB, version = graph-key-per-version** and `begin_version(from: vN)` = `GRAPH.COPY vN vN+1` (whole-graph copy incl. nodes/edges/schema/indices — cheap, deterministic); on Neo4j, a scoped subgraph copy or dump/restore per version. `group_id` remains available for intra-version organization but is not the version boundary. The adapter's HTTP mux serves `/kgp/<graph>/<version>/mcp` by binding the request's backend key from the path.

### 3.2 Typing without extraction (r1 f1 — the seam, pinned)

**Extraction lives in the pipeline (design 14), not the adapter.** `kg.admin.write_batch`/`load_artifact` are **structured node/edge upserts** exactly as design 01 §3.4 specifies — the adapter persists typed elements via graphiti's lower-level node/edge persistence (or the backend driver directly), attaching provenance per element; graphiti's `add_episode` extraction path is **not** the ingestion write path. The design-12 Pydantic models still configure the adapter's typing and search behavior (typed nodes, hybrid search over ontology types) — they just never trigger adapter-side LLM calls. Consequences: Gate A genuinely runs *before any write* (14 D2), the LLM caller is the build Job so budget attribution is automatic (14 D4), and the adapter contains **zero LLM dependencies** (embedding calls for search indexing use the pinned embedder via the gateway; extraction never happens here).

### 3.3 Query tools

| kgp tool | Implementation |
|---|---|
| `kg.search` | graphiti hybrid search (semantic+BM25+graph) scoped to the version's group_id; results mapped to hits with episode-derived `provenance_ref` |
| `kg.neighbors` | direct backend traversal (FalkorDB/Neo4j driver) filtered by relation types; bitemporal fields passed through |
| `kg.get_context_bundle` | adapter-side deterministic recipe engine (walk/subtree/neighborhood_summary/timeline) over typed edges — **no LLM calls in the adapter, ever** (ADR-0017); `text` is template-rendered; `require_scope` checked first |
| `kg.cite` | episode + source metadata from the provenance stored at write (§3.4) |
| `kg.schema` | the embedded ontology doc verbatim + `{contract, version, pattern?, embedder: {model, dim}}` |
| `kg.probe` | executes the probe set via the same query paths agents use; matchers per design 12 §3.6 |

**Scope enforcement (design 01 A1)**: every request carries the gateway-injected `X-Plume-KG-Scope` header — **trust derives from the mTLS connection's verified gateway SVID** (ingress is gateway-only, §6); no separate header signature exists or is needed (r1 f4; design 03's row wording aligned). All four fact-bearing tools filter to scoped entity types and answer `KG_SCOPE_DENIED` otherwise. The closed error set (ADR-0017) maps from adapter errors — never provider-native errors on the wire.

### 3.4 Admin tools & provenance

`write_batch`/`load_artifact` perform structured upserts with **mandatory provenance per element** (source_uri, span, pipeline_run) stored as episode metadata; idempotency by `(batch_id, seq)` recorded in a small adapter-local ledger table (in FalkorDB itself — no extra store). `commit_version` runs the invariant queries compiled from the ontology (design 12 derivations) against the staging namespace: violations ⇒ quarantine (namespace kept for inspection, never promoted). `promote` flips the adapter's version routing table; `drop_version` deletes a namespace (refused for active).

### 3.5 Embedder pinning

The KG CR names the embedding model; the adapter records `{model, dim}` in version metadata at `begin_version` and **refuses mixed-embedder writes into one version** (`KG_VERSION_GONE`-adjacent error, distinct code `KG_EMBEDDER_MISMATCH` — an adapter-level extension code, documented) — re-embedding is a version bump by construction (ADR-0017).

## 4. Managed-mode packaging

`provider: graphiti` in the KG CR ⇒ the operator deploys, per graph: the adapter Deployment (stateless; scales for read QPS) + FalkorDB (single instance, PVC) + NetworkPolicy (adapter ⇄ FalkorDB only; ingress from gateway only). Backend switch `graphiti.backend: falkordb | neo4j` honors ADR-0018's no-lock-in promise; the adapter uses graphiti's driver abstraction. LLM/embedding calls for **extraction** egress via the gateway's LLM route (they are receipted, budgeted hops — extraction cost is visible per pipeline run).

## 5. Failure modes

| Failure | Behavior |
|---|---|
| FalkorDB down | All tools return typed unavailable; KG CR `Ready=False` via probe failure; agents degrade per design 02 |
| Pipeline write retries | Adapter idempotency by `(batch_id, seq)` makes replays safe (extraction errors are wholly design 14's — no LLM here) |
| Copy-forward interrupted | Staging namespace incomplete; `commit_version` invariants fail ⇒ quarantine; rebuild resumes by batch idempotency |
| Scope header missing/unsigned | Deny-all (`KG_SCOPE_DENIED`) — absence of scope is never scope-everything |
| Backend switch on existing graph | Refused in-place; new backend = new graph build (documented; no silent migration) |
| Version routing table corruption | Rebuilt from namespace listing at startup (namespaces are the truth) |

## 6. Security

Ingress mTLS from gateway only; admin endpoint additionally requires platform SVIDs (ADR-0017). Provenance stores pointers, not payload copies (ADR-0014). FalkorDB has no exposed service beyond the adapter. SSPL posture per ADR-0018 (unmodified, self-hosted; Neo4j profile one flip away).

## 7. Testing

**The kgp conformance suite is the acceptance test** (design 01 §8, all 10 points + the scope battery from A1). Adapter-specific additions: fork determinism (`GRAPH.COPY` result equals source, byte-stable across repeats); interrupted-fork cleanup; embedder-mismatch refusal (wire code asserted); structured-write path never invokes an LLM (network-egress assertion in test harness); backend matrix (falkordb required, neo4j weekly).

## 8. Decisions for async review

- **D1 — Version fork = deterministic data copy** (FalkorDB `GRAPH.COPY`, key-per-version; Neo4j scoped copy) — never re-extraction (r1 f2).
- **D2 — The adapter is LLM-free** (r1 f1): extraction is pipeline-side; only embedding-for-search egresses via the gateway under the build/probe principals.
- **D3 — Embedder mismatch surfaces as closed-set `KG_VERSION_GONE` + `data.detail`** (r1 f3); first-class code deferred to kgp/v1beta1.

## 9. Resulting ADRs

Folded into ADR-0023 after critique PASS.
