# Design 05: Agent directory (OASF on JetStream KV, OCI exchange)

- **Status**: draft — awaiting critique
- **Phase**: P1 · **Size**: S · **Date**: 2026-08-20
- **ADRs**: 0003 (OASF), 0013 (tenancy) · interfaces: 02 (writer), 08 (CLI reads), 09 (templates read tools), exposure (§05 arch)
- **Research**: AGNTCY ADS uses OASF records over OCI registry semantics with Sigstore signing and A2A/MCP import-export (docs.agntcy.org/dir, arxiv 2509.18787) — we adopt the *format and exchange semantics* without running ADS.

## 1. Purpose & scope

Where agents and tools are *found*: the queryable catalog behind `plume init`'s tool/agent pickers, A2A discovery, and App composition. **In scope**: record format, storage layout, read/write paths, external exchange. **Out of scope**: registration flow (design 02 owns), MCP tool catalogs' content (Connector packs).

## 2. Doctrine & charter gates

- **Plane**: slow (a platform guarantee) but **0 pods**: KV bucket in the existing NATS + a read path served by... the operator's existing HTTP listener (same binary pattern as design 04's tap). No directory service.
- **Stateful deps**: JetStream KV only. **Primitives**: Agent (cards), Artifact (OCI exchange), Event (change notifications are KV watch). ✓

## 3. Interfaces

### 3.1 Record: OASF-wrapped, A2A-derived

One record per registered agent revision, JSON, OASF `Agent` root object with plume extensions:

```json
{
  "oasf": "…/agent/v…",                 // pinned OASF schema version
  "name": "claims/pa-reviewer",
  "version": "1.4.2+rev.7f3a2",
  "skills": [...],                       // enriched from A2A card skills
  "locators": [{"type": "a2a", "url": "https://gw…/agents/claims/pa-reviewer"}],
  "extensions": [
    {"name": "plume.card",   "data": { /* verbatim A2A Agent Card */ }},
    {"name": "plume.status", "data": {"phase": "Ready", "graphRefs": ["payer-policies@v12"]}},
    {"name": "plume.provenance", "data": {"imageDigest": "…", "cardDigest": "…", "sigstore": "…"}}
  ]
}
```

Conversion A2A card → OASF record is deterministic (golden-tested); the card is embedded verbatim so A2A clients lose nothing.

### 3.2 Storage layout (JetStream KV)

- Bucket `DIRECTORY` per NATS account (= per tenant, ADR-0013).
- Keys: `agents.<ns>.<name>.<revision>` and pointer `agents.<ns>.<name>.active`; `tools.<ns>.<name>` for Connector tool facets and MCPServer registrations.
- KV history = built-in audit of directory changes; watchers get change feeds for free (CLI `--watch`, App composition refresh).
- GC with revisions per design 02 §3.4.

### 3.3 Read paths

- **CLI / operators**: direct KV reads (NATS client, tenant-scoped credentials).
- **Agents** (A2A discovery): the gateway serves `/.well-known/agent-card.json` per exposed agent from the embedded card (static route emitted by design 03) — agents never need KV access.
- **Search**: v1 is key-prefix + client-side filter over OASF skills (dozens–hundreds of records; fine). A search index is explicitly deferred until a measured need (>5k records or >100ms p95 list).

### 3.4 External exchange (OCI + Sigstore, ADS-compatible)

- `plume dir export <agent>` → OASF record as an OCI artifact, cosign-signed — the same registry + signing machinery as packs/ModelKits; layout follows AGNTCY ADS conventions so records are portable to any ADS-compatible directory (including the hosted Outshift directory for `expose.visibility: public`).
- `plume dir import <ref>` → verify signature → register as an **external agent** (design 02 path).
- Federation with the public **MCP Registry** for the tool catalog is read-only import into `tools.*` (provenance marked `mcp-registry`).

## 4. Behavior

Writes only from the agent-operator (registration, design 02 §3.4) — single writer, last-write-wins per key, KV history for audit. Reads are eventually consistent (KV replication) — acceptable: the directory is discovery metadata, not an authz source (authz is gateway policy; a stale directory entry cannot grant access).

## 5. Failure modes

| Failure | Behavior |
|---|---|
| KV unavailable | Registration blocked (`Registered=False`, design 02); serving traffic unaffected (routes don't depend on directory) |
| Record/card divergence | Impossible by construction: operator writes both from one fetch; card digest in provenance detects tamper |
| Import of unsigned/invalid record | Rejected at `dir import` (cosign verify + OASF schema validation) |
| OASF schema version bump | Records carry their schema version; converter supports N/N−1 (same policy as our own contracts) |

## 6. Security

Per-tenant buckets via NATS accounts; writer = operator identity only; exported records signed; imported records verified + registered as *external* (least privilege — OAuth client, no SVID). The directory never stores secrets (locators + metadata only).

## 7. Observability

Registration/GC counters, KV bucket size, import/export events. `plume dir list` shows staleness (record ts vs agent status).

## 8. Testing

Golden card→record conversion; KV layout round-trip; export→import cycle (sign+verify) e2e; MCP Registry import fixture; tenant isolation (account A cannot read B's bucket).

## 9. Decisions for async review

- **D1 — Adopt ADS *formats and exchange semantics* (OASF + OCI + Sigstore) without running ADS** — portability with zero standing infrastructure.
- **D2 — Directory is discovery, never authz** — staleness is therefore benign; this boundary is load-bearing and stated.
- **D3 — Search is deliberately primitive in v1** with a measured deferral trigger.

## 10. Resulting ADRs

Folded into ADR-0022 (P1 infrastructure decisions) after critique PASS.
