# Design 26: Tenant CR — the multi-tenancy fan-out (enterprise)

- **Status**: draft — awaiting critique
- **Phase**: enterprise · **Size**: L · **Date**: 2026-08-20
- **ADRs**: 0013 (layered tenancy, mostly inherited), 0015 (enterprise module) · interfaces: the seams every prior design named — 04 D4 (per-tenant RECEIPTS streams), 11 (EVENTS), 22 (APPROVALS), 05 (DIRECTORY), 06 (IdP orgs), 21 r2 f4 (runtime tenancy), 24 (per-tenant FGA stores), 18 (per-tenant pack allowlists), 03 (gateway partitions)
- **Research**: `docs/research/landscape-2026-08.md` §tenancy (vCluster dominant for hard isolation; Capsule for soft)

## 1. Purpose & scope

One object that fans out across every layer that already has a tenancy seam. **This design creates almost no new mechanism** — that is its thesis and the payoff of ADR-0013's "mostly inherited": each prior design chose tenant-shaped primitives (NATS accounts, RLS, IdP orgs, gateway partitions), so the Tenant CR is a fan-out controller plus the isolation-mode choice. In scope: the CR, the fan-out matrix, the two isolation modes, lifecycle (create/suspend/delete), the seams' resolution. Out of scope: the primitives themselves (owned by their designs), compliance profiles (27).

## 2. Doctrine & charter gates

- **Plane**: slow (enterprise controller — a separate binary/module per ADR-0015's open-core split; it consumes only public CRs and contracts, so the OSS core has no enterprise hooks to maintain).
- **Pods**: 1 (tenant-operator, enterprise module) + per-tenant workload costs of the chosen mode. **Stateful deps**: none new (uses Postgres/NATS/IdP that exist). ✓
- **Primitives**: Resource, Event (`tenant.provisioned/suspended`). ✓

## 3. The Tenant CR & fan-out matrix

```yaml
kind: Tenant
metadata: {name: acme}
spec:
  isolation: soft | hard          # namespaces+policy | vCluster (§4)
  quotas: {agents: 50, graphs: 10, tokensPerDay: 20M, usdPerDay: 400, gpu: {…}}
  idp: {orgName: acme}            # Zitadel organization (06)
  packSources: [builtin, acme-internal]     # 18's allowlist, per tenant
  compliance: {profile: hipaa}    # 27, optional
  admins: [group:acme-platform]
status:
  conditions: [NamespaceReady, AccountReady, DatabaseReady, IdPReady, GatewayReady, QuotasEnforced]
  resources: {namespace|vcluster: …, natsAccount: …, dbRoles: […], idpOrg: …, gatewayPartition: …}
```

| Layer | Fan-out (all pre-existing mechanisms) |
|---|---|
| Control plane | soft: namespace + Capsule-style tenant policy + RBAC; hard: vCluster instance (agents/CRs live in the vCluster; the platform operators run **inside** it — see §4) |
| Messaging | NATS **account** + per-tenant streams (`RECEIPTS`, `EVENTS`) and buckets (`DIRECTORY`, `APPROVALS`, cursors) — created by the same bootstrap job design 07 runs at n=1 |
| Data | Postgres: per-tenant **roles + RLS** policies on shared platform tables (DBOS state, audit index, eval results); per-tenant databases for IdP/FGA where those components require it |
| Identity | Zitadel **organization** (06 `ensureTenant`) — the interface already exists for exactly this |
| Authz | per-tenant FGA **store** (24) — stores are OpenFGA's native isolation unit |
| Traffic | gateway **partition**: listener set + routes + quota policies scoped to the tenant principal set (03 compiles from the Tenant's quotas the same way it compiles Agent budgets) |
| Packs | per-tenant source allowlist (18) — a tenant cannot install from another's sources |
| Workflows | the 21 r2 f4 seam **resolved here**: hard mode runs a per-tenant `workflow-runtime` (inside the vCluster; no cross-account credentials ever); soft mode keeps the shared runtime with per-tenant consumer credentials scoped to that tenant's account — stated limitation: soft mode's runtime holds N tenants' consumer creds, which is why **hard mode is the recommendation for untrusted tenants** (ADR-0013's own words, now mechanical) |

## 4. The two modes, honestly

- **Soft** (teams in one org): one cluster, namespace-per-tenant, shared platform operators and runtime. Isolation is policy-grade: RBAC + NetworkPolicy + NATS accounts + RLS + gateway partitions. Good for dozens of internal teams; **not** for tenants who get cluster API access.
- **Hard** (external customers): vCluster per tenant — its own API server, its own plume operator set, its own runtime. The tenant-operator provisions the vCluster, installs the plume chart inside it (pinned to the host's platform version), and wires the shared substrate by *account/role/store*, not by shared processes. Cost is honest: ~the core pod set per tenant (the chart's own budget, per tenant) — that is what hard isolation costs, and the CR makes it a single decision rather than a bespoke project.

**Version skew**: host and tenant vClusters are upgraded by the tenant-operator in waves; N/N−1 contract discipline (07) governs, and a tenant may lag one platform version — surfaced as `TenantVersionSkew`.

## 5. Lifecycle

Create → fan-out (idempotent, per-layer conditions). **Suspend** → gateway partition denies + workloads scaled 0 (data untouched; the reversible commercial lever). **Delete** → refuses without `spec.confirmDelete: <name>` *and* a completed **export**: receipts/audit index/graph snapshots exported to the tenant's object location first (deleting a tenant must not silently destroy an audit trail — the compliance rule made structural); then reverse fan-out, IdP org **deactivated not deleted** (06 D3's rule holds at tenant scope).

## 6. Failure modes

| Failure | Behavior |
|---|---|
| Partial fan-out | Per-layer conditions name the stuck layer; idempotent retry; tenant is not `Ready` and gets no traffic until all layers are |
| Quota exceeded | Gateway partition enforces (429 with tenant context); `QuotasEnforced` reflects; per-agent budgets still apply *within* the tenant quota (two-tier, like ADR-0020's own tiers) |
| vCluster unhealthy (hard) | Tenant `Degraded`; host-side platform unaffected — blast radius is the design's point |
| Cross-tenant reference attempt (Agent in A refs graph in B) | Admission denies (namespace/vCluster boundary + gateway partition); logged as a security event |
| Tenant deleted upstream in IdP | `IdPReady=False`; no auto-recreate (identity objects are never auto-managed away — 06 D3) |
| Soft-mode noisy neighbor | Quotas + gateway partitions bound it; the escalation path is documented as "move to hard mode", which is a CR field change |

## 7. Security

Per-tenant credentials never co-mount (except the soft-mode runtime limitation, stated in §3 and mitigated by hard mode); the tenant-operator holds the only cross-tenant privilege in the system and is auditable (receipted under `principal: tenant-op`); export-before-delete keeps audit trails intact; admins are IdP groups (contextual tuples, 24) — no separate tenant admin store.

## 8. Testing

Fan-out idempotency (create ×3 ⇒ same state); isolation battery per layer (account A cannot read B's streams/KV; RLS denies cross-tenant rows; gateway partition rejects cross-tenant principals; FGA store isolation; pack-source isolation); hard-mode e2e (vCluster provisioned, agent deployed inside, receipts land in the tenant account); suspend/resume; delete-without-export refusal; skew condition.

## 9. Decisions for async review

- **D1 — The Tenant CR adds no new isolation primitives** — it fans out choices already made; if a layer needed something new here, that layer's design was wrong (this is the ADR-0013 thesis, tested).
- **D2 — Hard mode = vCluster with its own operator set**; cost stated plainly (core pod set per tenant).
- **D3 — Soft mode's shared workflow runtime holds N tenants' consumer credentials** — the one honest gap; hard mode is the answer for untrusted tenants.
- **D4 — Delete requires export-first + explicit confirmation**; IdP orgs deactivate, never delete.

## 10. Resulting ADRs

ADR-0026 (P5+ent) after critique PASS.
