# Design 13: Graphiti provider adapter (first kgp/v1alpha1 implementation)

- **Status**: draft — awaiting critique
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

Graph version `vN` ⇔ graphiti `group_id = "<graph>@vN"`. The adapter's HTTP mux serves `/kgp/<graph>/<version>/mcp` by binding each request's group_id from the path — version isolation is namespace isolation (research: group_id scopes episodes + extracted entities). `begin_version(from: vN)` copy-on-write: v1 implements **copy-forward** (bulk re-add of vN's episodes into vN+1's namespace) — simple, correct, O(corpus); an incremental CoW is a recorded optimization, not a v1 promise.

### 3.2 Extraction = ontology-derived Pydantic types

The design-12 derivation hands the adapter generated Pydantic entity/edge models (descriptions as docstrings = prompt substrate); the adapter passes them to `add_episode` / `add_episode_bulk` so extraction classifies against the ontology. **Bulk constraint honored**: `add_episode_bulk` only into *fresh* staging namespaces (our snapshot model guarantees this — the research-noted "empty graph / no edge invalidation" precondition is structural for us); incremental additions to a staging version use plain `add_episode`.

### 3.3 Query tools

| kgp tool | Implementation |
|---|---|
| `kg.search` | graphiti hybrid search (semantic+BM25+graph) scoped to the version's group_id; results mapped to hits with episode-derived `provenance_ref` |
| `kg.neighbors` | direct backend traversal (FalkorDB/Neo4j driver) filtered by relation types; bitemporal fields passed through |
| `kg.get_context_bundle` | adapter-side deterministic recipe engine (walk/subtree/neighborhood_summary/timeline) over typed edges — **no LLM calls in the adapter, ever** (ADR-0017); `text` is template-rendered; `require_scope` checked first |
| `kg.cite` | episode + source metadata from the provenance stored at write (§3.4) |
| `kg.schema` | the embedded ontology doc verbatim + `{contract, version, pattern?, embedder: {model, dim}}` |
| `kg.probe` | executes the probe set via the same query paths agents use; matchers per design 12 §3.6 |

**Scope enforcement (design 01 A1)**: every request carries the gateway-injected `X-Plume-KG-Scope` header (signed; adapter verifies the gateway's signature/SVID); all four fact-bearing tools filter to scoped entity types and answer `KG_SCOPE_DENIED` otherwise. The closed error set (ADR-0017) maps from adapter errors — never provider-native errors on the wire.

### 3.4 Admin tools & provenance

`write_batch`/`load_artifact` wrap typed episode adds with **mandatory provenance per element** (source_uri, span, pipeline_run) stored as episode metadata; idempotency by `(batch_id, seq)` recorded in a small adapter-local ledger table (in FalkorDB itself — no extra store). `commit_version` runs the invariant queries compiled from the ontology (design 12 derivations) against the staging namespace: violations ⇒ quarantine (namespace kept for inspection, never promoted). `promote` flips the adapter's version routing table; `drop_version` deletes a namespace (refused for active).

### 3.5 Embedder pinning

The KG CR names the embedding model; the adapter records `{model, dim}` in version metadata at `begin_version` and **refuses mixed-embedder writes into one version** (`KG_VERSION_GONE`-adjacent error, distinct code `KG_EMBEDDER_MISMATCH` — an adapter-level extension code, documented) — re-embedding is a version bump by construction (ADR-0017).

## 4. Managed-mode packaging

`provider: graphiti` in the KG CR ⇒ the operator deploys, per graph: the adapter Deployment (stateless; scales for read QPS) + FalkorDB (single instance, PVC) + NetworkPolicy (adapter ⇄ FalkorDB only; ingress from gateway only). Backend switch `graphiti.backend: falkordb | neo4j` honors ADR-0018's no-lock-in promise; the adapter uses graphiti's driver abstraction. LLM/embedding calls for **extraction** egress via the gateway's LLM route (they are receipted, budgeted hops — extraction cost is visible per pipeline run).

## 5. Failure modes

| Failure | Behavior |
|---|---|
| FalkorDB down | All tools return typed unavailable; KG CR `Ready=False` via probe failure; agents degrade per design 02 |
| Extraction LLM errors mid-batch | DBOS-side retry (design 14 owns); adapter idempotency makes replays safe |
| Copy-forward interrupted | Staging namespace incomplete; `commit_version` invariants fail ⇒ quarantine; rebuild resumes by batch idempotency |
| Scope header missing/unsigned | Deny-all (`KG_SCOPE_DENIED`) — absence of scope is never scope-everything |
| Backend switch on existing graph | Refused in-place; new backend = new graph build (documented; no silent migration) |
| Version routing table corruption | Rebuilt from namespace listing at startup (namespaces are the truth) |

## 6. Security

Ingress mTLS from gateway only; admin endpoint additionally requires platform SVIDs (ADR-0017). Provenance stores pointers, not payload copies (ADR-0014). FalkorDB has no exposed service beyond the adapter. SSPL posture per ADR-0018 (unmodified, self-hosted; Neo4j profile one flip away).

## 7. Testing

**The kgp conformance suite is the acceptance test** (design 01 §8, all 10 points + the scope battery from A1). Adapter-specific additions: copy-forward interruption/resume; embedder-mismatch refusal; bulk-vs-incremental path equivalence (same corpus, same graph modulo episode ids); backend matrix (falkordb required, neo4j weekly).

## 8. Decisions for async review

- **D1 — Copy-forward (bulk re-add) for `begin_version(from)`** in v1; incremental CoW recorded as optimization with a trigger (corpus > ~500k episodes or build > nightly window).
- **D2 — Extraction LLM traffic egresses via the gateway** — receipted and budgeted like all model traffic; a KG build has a visible cost.
- **D3 — `KG_EMBEDDER_MISMATCH` as a documented adapter extension code** (candidate for kgp/v1beta1 promotion).

## 9. Resulting ADRs

Folded into ADR-0023 after critique PASS.
