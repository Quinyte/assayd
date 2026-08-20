# Design 26: Tenant CR — the multi-tenancy fan-out (enterprise)

- **Status**: revised r3 — awaiting re-critique (r1 5 findings + r2 residuals R2-1/R2-2 addressed; reviews/26-review.md)
- **Phase**: enterprise · **Size**: L · **Date**: 2026-08-20
- **ADRs**: 0013 (layered tenancy, mostly inherited), 0015 (enterprise module) · interfaces: the seams every prior design named — 04 D4 (per-tenant RECEIPTS streams), 11 (EVENTS), 22 (APPROVALS), 05 (DIRECTORY), 06 (IdP orgs), 21 r2 f4 (runtime tenancy), 24 (per-tenant FGA stores), 18 (per-tenant pack allowlists), 03 (gateway partitions)
- **Research**: `docs/research/landscape-2026-08.md` §tenancy (vCluster dominant for hard isolation; Capsule for soft)

## 1. Purpose & scope

One object that fans out across every layer that already has a tenancy seam. **This design creates two new mechanisms and inherits everything else** (r1 f1/f2 — the thesis, tested and corrected): tenant-scoped traffic quotas extend the budget compiler, and hard mode adds per-tenant SPIRE with federation. Everything else is genuinely inherited — the payoff of ADR-0013's "mostly inherited": each prior design chose tenant-shaped primitives (NATS accounts, RLS, IdP orgs, gateway partitions), so the Tenant CR is a fan-out controller plus the isolation-mode choice. In scope: the CR, the fan-out matrix, the two isolation modes, lifecycle (create/suspend/delete), the seams' resolution. Out of scope: the primitives themselves (owned by their designs), compliance profiles (27).

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
| Control plane | soft: namespace + Capsule-style tenant policy + RBAC; hard: vCluster instance — agents/CRs live in the vCluster, **workload-managing operators run inside it, the policy compiler stays host-side** (§4's split table) |
| Messaging | NATS **account** + per-tenant streams (`RECEIPTS`, `EVENTS`) and buckets (`DIRECTORY`, `APPROVALS`, cursors) — created by the same bootstrap job design 07 runs at n=1 |
| Data | Postgres: per-tenant **roles + RLS** on the audit index and eval results. **DBOS state is NOT RLS-isolated in soft mode (r1 f3)** — one shared runtime process holds one DBOS runtime role, and RLS discriminates by connection role; soft-mode DBOS isolation is process-level only. Hard mode resolves it (per-tenant runtime + role). Per-tenant databases for IdP/FGA |
| Identity | Zitadel **organization** (06 `ensureTenant`) — the interface already exists for exactly this |
| Authz | per-tenant FGA **store** (24) — stores are OpenFGA's native isolation unit |
| Traffic | gateway **partition** — **a new compiler concern, honestly (r1 f1)**: a `TenantPolicyIntent` (tenant-scoped target kind) recorded as a design 03 amendment with its own row; quota enforcement inherits ADR-0020's **approximation tier** (rate ÷ replicas — tenant quotas are *not* exactly enforced at the gateway, same arithmetic as agent budgets), and the **exact tier is a per-tenant rollup in design 04's audit index** (rows already carry `tenant` — cheap, recorded as a 04 amendment). Budget *nesting* (per-agent within per-tenant) is a separate axis from approximate-vs-exact |
| Packs | per-tenant source allowlist (18) — a tenant cannot install from another's sources |
| Workflows | the 21 r2 f4 seam **resolved here**: hard mode runs a per-tenant `workflow-runtime` inside the vCluster; soft mode keeps the shared runtime — whose honest exposure is **three credential classes** (r1 f3): N tenants' JetStream consumer creds, N tenants' `workflow-actor` client credentials (the identities the gateway keys budgets/ReBAC on), and one shared DBOS role. **Hard mode is the answer for untrusted tenants** |

## 4. The two modes, honestly

- **Soft** (teams in one org): one cluster, namespace-per-tenant, shared platform operators and runtime. Isolation is policy-grade: RBAC + NetworkPolicy + NATS accounts + RLS + gateway partitions. Good for dozens of internal teams; **not** for tenants who get cluster API access.
- **Hard** (external customers): vCluster per tenant. **What is per-tenant vs shared (r1 f2 — the table the thesis needed)**:

| Component | Hard mode |
|---|---|
| API server, CRs, agent workloads | **per-tenant** (in the vCluster) |
| **workload-managing operators** (Agent/KG/Workflow/Model controllers) | **per-tenant, inside the vCluster** (R2-1): they *create* Deployments/Sandboxes, apply SVID-attestation labels, and write directory entries — all against the tenant's own API server. They hold **no host credentials**, and their cost is counted per-tenant below |
| **policy compiler** (gateway config) | **host-side** — it writes gateway CRs on the host cluster (which is why the write-direction problem is solved) and *reads* tenant CRs through the vCluster kubeconfig. It holds no tenant-workload write access |
| agentgateway | shared, partitioned (per-tenant listener set) |
| **SPIRE** | **per-tenant server inside the vCluster, federated to the host trust domain** (r1 f2 — vCluster syncs workloads into one host namespace, so a shared host SPIRE would collapse every tenant's agents into one identity segment). SPIFFE federation is **the second new mechanism** this design adds; recorded as a design 06 amendment |
| NATS, Postgres, Zitadel, OpenObserve | shared, isolated by account / role+RLS / organization / stream |
| workflow-runtime, OpenFGA (if plus) | **per-tenant** |

**Cost, by tier (r1 f4, R2-1-corrected)**: a core-only hard tenant ≈ vCluster control plane + per-tenant workload operators + SPIRE + agent workloads (~4–5 pods + workloads), *not* the full ≈8 (NATS/Postgres/Zitadel/OpenObserve/gateway/compiler are shared); a plus tenant adds workflow-runtime (+1–2) and OpenFGA+adapter (+1). The CR makes hard isolation a single decision rather than a bespoke project — that remains the point.

**Version skew (r1 f5)**: two `plume-contracts` ledgers exist (host + tenant). The **tenant-operator compares both at reconcile**; a gap >1 blocks **tenant upgrades** (never tenant traffic — a lagging tenant keeps serving), surfaced as `TenantVersionSkew` with both ledger versions named. N/N−1 (07) governs between installs, not just across upgrades.

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

Per-tenant credentials never co-mount (except the soft-mode runtime limitation, stated in §3 and mitigated by hard mode); the tenant-operator holds the only **provisioning** cross-tenant privilege and is auditable (receipted under `principal: tenant-op`); the host-side policy compiler holds cross-tenant *read* on tenant CRs plus host gateway write — never tenant-workload write (R2-1), so a compromised compiler cannot create workloads in any tenant; export-before-delete keeps audit trails intact; admins are IdP groups (contextual tuples, 24) — no separate tenant admin store.

## 8. Testing

Fan-out idempotency (create ×3 ⇒ same state); isolation battery per layer (account A cannot read B's streams/KV; RLS denies cross-tenant rows; gateway partition rejects cross-tenant principals; FGA store isolation; pack-source isolation); hard-mode e2e (vCluster provisioned, agent deployed inside, receipts land in the tenant account); suspend/resume; delete-without-export refusal; skew condition.

## 9. Decisions for async review

- **D1 — The Tenant CR adds exactly two new mechanisms** (tenant-scoped quota intents; per-tenant SPIRE + federation) **and inherits every other layer** — the ADR-0013 thesis, tested and corrected rather than asserted (r1 f1/f2).
- **D2 — Hard mode splits the control plane**: workload-managing operators per-tenant inside the vCluster; the policy compiler host-side (R2-1/R2-2). Cost is the §4 table's range (~4–5 pods core-only; +2–3 for a plus tenant), not the full core set — NATS/Postgres/Zitadel/OpenObserve/gateway are shared.
- **D3 — Soft mode's shared runtime holds three credential classes** (consumer creds, workflow-actor clients, one DBOS role) and its **DBOS state is not RLS-isolated** — the complete gap; hard mode is the answer for untrusted tenants.
- **D4 — Delete requires export-first + explicit confirmation**; IdP orgs deactivate, never delete.

## 10. Resulting ADRs

ADR-0026 (P5+ent) after critique PASS.
