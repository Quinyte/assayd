# Design 05: Agent directory (OASF on JetStream KV, OCI exchange)

- **Status**: revised r2 — awaiting re-critique (r1 REVISE, 9 findings — all addressed; reviews/05-review.md)
- **Phase**: P1 · **Size**: S · **Date**: 2026-08-20
- **ADRs**: 0003 (OASF), 0013 (tenancy) · interfaces: 02 (writer), 08 (CLI reads), 09 (templates read tools), exposure (§05 arch)
- **Research**: `docs/research/oasf-ads-2026-08.md` (ADS/OASF claims, cited; **OASF schema 1.1.0 verified live**) — we adopt the *format and exchange semantics* without running ADS.

## 1. Purpose & scope

Where agents and tools are *found*: the queryable catalog behind `plume init`'s tool/agent pickers, A2A discovery, and App composition. **In scope**: record format, storage layout, read/write paths, external exchange. **Out of scope**: registration flow (design 02 owns), MCP tool catalogs' content (Connector packs).

## 2. Doctrine & charter gates

- **Plane**: slow (a platform guarantee) but **0 pods**: a KV bucket in the existing NATS. Read paths are direct KV and the gateway only (§3.3) — no directory service, no extra listener (r1 f7).
- **Stateful deps**: JetStream KV only. **Primitives**: Agent (cards), Artifact (OCI exchange), Event — **true CloudEvents**: every registration write also emits a `dir.changed` CloudEvent onto JetStream (r1 f6), which doubles as the durable directory audit (r1 f4). KV watch remains an implementation detail. ✓

## 3. Interfaces

### 3.1 Record: OASF-wrapped, A2A-derived

One record per registered agent revision, JSON, OASF `Agent` root object with plume extensions:

```json
{
  "oasf": "1.1.0",                       // pinned; schema.oasf.outshift.com reports schema_version 1.1.0 (verified 2026-08-20)
  "name": "claims/pa-reviewer",
  "version": "1.4.2+rev.7f3a2",
  "skills": [...],                       // enriched from A2A card skills
  "locators": [{"type": "a2a", "url": "https://gw…/agents/claims/pa-reviewer"}],
  "extensions": [
    {"name": "plume.card",   "data": { /* verbatim A2A Agent Card */ }},
    {"name": "plume.status", "data": {"phase": "Ready", "graphRefs": ["payer-policies@v12"]}},   // operator RE-WRITES on phase transitions (r1 f5) — single writer, per-agent key, cheap
    {"name": "plume.provenance", "data": {"imageDigest": "…", "cardDigest": "…", "sigstore": "…"}}
  ]
}
```

Conversion A2A card → OASF record is deterministic (golden-tested); the card is embedded verbatim so A2A clients lose nothing.

### 3.2 Storage layout (JetStream KV)

- Bucket `DIRECTORY` per NATS account (= per tenant, ADR-0013).
- Keys per the **canonical layout recorded in design 02 §11 A2** (r1 f3): dot-separated subject tokens `agents.<ns>.<name>.<revision>`, pointer `agents.<ns>.<name>.active`, `tools.<ns>.<name>` — prefix-watch (`agents.<ns>.>`) is the rationale; key-charset rule verified against the NATS client spec at implementation.
- **Bucket history = 10** (JetStream KV caps history at 64/key, default 1 — r1 f4): a bounded recent-change convenience, *not* audit. Audit = the `dir.changed` CloudEvents (§2) + receipts. Watchers get change feeds free (CLI `--watch`, App refresh).
- GC with revisions per design 02 §3.4.

### 3.3 Read paths

- **CLI / operators**: direct KV reads (NATS client, tenant-scoped credentials).
- **Agents** (A2A discovery): the gateway **routes** `/.well-known/agent-card.json` to the *agent container*, which already serves it — SoT preserved per ADR-0019 (r1 f1; route row added to design 03 §3.4). The directory's embedded card is the validated, digest-pinned discovery copy for `plume dir`/export — never the wire answer. External/scaled-to-zero agents: the CR-inline card override (design 02 §3.4) is served by the gateway as the documented exception, digest-checked at registration.
- **Search**: v1 is key-prefix + client-side filter over OASF skills (dozens–hundreds of records; fine). A search index is explicitly deferred until a measured need (>5k records or >100ms p95 list).

### 3.4 External exchange (OCI + Sigstore, ADS-compatible)

- `plume dir export <agent>` → OASF record as an OCI artifact, cosign-signed — the same registry + signing machinery as packs/ModelKits; layout follows AGNTCY ADS conventions so records are portable to any ADS-compatible directory (including the hosted Outshift directory for `expose.visibility: public`).
- `plume dir import <ref>` → verify signature → register as an **external agent** (design 02 path).
- **MCP Registry federation is explicit and curated** (r1 f2): `plume dir import-tools --namespace <allowlisted>` — never a background sync; entries are schema-validated, provenance-marked, and the wizard *displays* provenance with its recommendation (never silent). **An imported record alone is not bindable**: binding a tool still requires the Connector/MCPServer CR path with admission checks — the directory suggests, CRs grant.
- **External publication is explicit-only** (r1 f9): records leave the cluster solely via `plume dir export` + push. `expose.visibility: public` exposes an *endpoint*; it never publishes a directory record anywhere by itself.

## 4. Behavior

Writes only from the agent-operator (registration, design 02 §3.4) — single writer, last-write-wins per key; audit = the `dir.changed` CloudEvents (§2), history is convenience only. Reads are eventually consistent (KV replication) — acceptable: the directory is discovery metadata, not an authz source (authz is gateway policy; a stale directory entry cannot grant access).

## 5. Failure modes

| Failure | Behavior |
|---|---|
| KV unavailable | Registration blocked (`Registered=False`, design 02); serving traffic unaffected (routes don't depend on directory) |
| Record/card divergence | Impossible by construction: operator writes both from one fetch; card digest in provenance detects tamper |
| Import of unsigned/invalid record | Rejected at `dir import` (cosign verify + OASF schema validation) |
| Malicious/typosquatted registry entry | Curated-namespace allowlist at import; provenance displayed at pick time; binding requires the CR path + admission — a poisoned suggestion cannot self-grant (r1 f2) |
| OASF schema version bump | Records carry their schema version; converter supports N/N−1 (same policy as our own contracts) |

## 6. Security

Per-tenant buckets via NATS accounts; writer = operator identity only; exported records signed; imported records verified + registered as *external* (least privilege — OAuth client, no SVID). The directory never stores secrets (locators + metadata only).

## 7. Observability

Registration/GC counters, KV bucket size, import/export events. `plume dir list` shows staleness (record ts vs agent status).

## 8. Testing

Golden card→record conversion; KV layout round-trip; export→import cycle (sign+verify) e2e; curated import fixture incl. rejected non-allowlisted namespace; tenant isolation (account A cannot read B's bucket); **phase-transition record refresh** and **history-depth** assertions (r1 f4/f5); `dir.changed` CloudEvent emission.

## 9. Decisions for async review

- **D1 — Adopt ADS *formats and exchange semantics* (OASF + OCI + Sigstore) without running ADS** — portability with zero standing infrastructure.
- **D2 — Directory is discovery, never authz** — staleness is therefore benign; this boundary is load-bearing and stated.
- **D3 — Search is deliberately primitive in v1** with a measured deferral trigger.

## 10. Resulting ADRs

Folded into ADR-0022 (P1 infrastructure decisions) after critique PASS.
