# Design 26: Tenant CR — the multi-tenancy fan-out (enterprise)

- **Status**: **approved** — critique PASS (reviews/26-review.md) · ADR-0026, **Amendment 1 (2026-09-04)** · amendments A1 (2026-09-03) and A2 (2026-09-04) below, neither yet critiqued
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
  conditions: [NamespaceReady, AccountReady, DatabaseReady, IdPReady, GatewayReady, QuotasEnforced,
               GatesTenantAdminBypassable]   # hard mode only, always True there (A1)
  resources: {namespace|vcluster: …, natsAccount: …, dbRoles: […], idpOrg: …, gatewayPartition: …}
```

| Layer | Fan-out (all pre-existing mechanisms) |
|---|---|
| Control plane | soft: namespace + Capsule-style tenant policy + RBAC; hard: vCluster instance — agents/CRs live in the vCluster, **workload-managing operators run inside it, the policy compiler stays host-side** (§4's split table) |
| Messaging | NATS **account** + per-tenant streams (`RECEIPTS`, `EVENTS`) and buckets (`DIRECTORY`, `APPROVALS`, cursors) — created by the same bootstrap job design 07 runs at n=1 |
| Data | Postgres: per-tenant **roles + RLS** on the audit index and eval results. **DBOS state is NOT RLS-isolated in soft mode (r1 f3)** — one shared runtime process holds one DBOS runtime role, and RLS discriminates by connection role; soft-mode DBOS isolation is process-level only. Hard mode resolves it (per-tenant runtime + role). Per-tenant databases for IdP/FGA |
| Identity | Zitadel **organization** (06 `ensureTenant`) — the interface already exists for exactly this |
| Authz | per-tenant FGA **store** (24) — stores are OpenFGA's native isolation unit |
| Traffic | gateway **partition** — **a new compiler concern, honestly (r1 f1)**: a `TenantPolicyIntent` (tenant-scoped target kind) recorded as a design 03 amendment with its own row (approximation tier; the backstop tier is not exact in USD — ADR-0028); quota enforcement inherits ADR-0028's **approximation tier** (rate ÷ replicas — tenant quotas are *not* exactly enforced at the gateway, same arithmetic as agent budgets), and the **exact tier is a per-tenant rollup in design 04's audit index** (rows already carry `tenant` — cheap, recorded as a 04 amendment). Budget *nesting* (per-agent within per-tenant) is a separate axis from approximate-vs-exact |
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

**Version skew (r1 f5)**: two `assayd-contracts` ledgers exist (host + tenant). The **tenant-operator compares both at reconcile**; a gap >1 blocks **tenant upgrades** (never tenant traffic — a lagging tenant keeps serving), surfaced as `TenantVersionSkew` with both ledger versions named. N/N−1 (07) governs between installs, not just across upgrades.

## 5. Lifecycle

Create → fan-out (idempotent, per-layer conditions). **Suspend** → gateway partition denies + workloads scaled 0 (data untouched; the reversible commercial lever). **Delete** → refuses without `spec.confirmDelete: <name>` *and* a completed **export**: receipts/audit index/graph snapshots exported to the tenant's object location first (deleting a tenant must not silently destroy an audit trail — the compliance rule made structural); then reverse fan-out, IdP org **deactivated not deleted** (06 D3's rule holds at tenant scope).

**Cross-cluster resource lifecycle (R3-a)**: in hard mode a tenant's Agent CR lives in the vCluster while its gateway routes and policies live on the host — Kubernetes ownerRefs cannot cross that boundary. The **policy compiler owns host-side cleanup**: it labels every emitted host resource with `assayd.dev/tenant` + `assayd.dev/agent-uid` (A1: design 03 A44's name for the source UID; this row said `assayd.dev/source-uid`, a second name for the same idea), watches the tenant API server, and garbage-collects on source deletion; a **finalizer on the tenant-side CR** blocks removal until host cleanup confirms. Orphan sweep runs each reconcile and reports `OrphanedHostResources` — a stale route must never outlive the CR that authorized it.

## 6. Failure modes

| Failure | Behavior |
|---|---|
| Partial fan-out | Per-layer conditions name the stuck layer; idempotent retry; tenant is not `Ready` and gets no traffic until all layers are |
| Quota exceeded | **Traffic quotas are not enforced yet (A2)**: there is no compiler path, so `tokensPerDay`/`usdPerDay` at tenant scope bind nothing and `QuotasEnforced` must not report them as satisfied. When A2's intent and record land: the gateway partition enforces (429 with tenant context), `QuotasEnforced` reflects, and per-agent budgets still apply *within* the tenant quota (two-tier, like ADR-0028's own tiers). Non-traffic quotas — `agents`, `graphs` — are counted by the tenant-operator and are unaffected |
| vCluster unhealthy (hard) | Tenant `Degraded`; host-side platform unaffected — blast radius is the design's point |
| Cross-tenant reference attempt (Agent in A refs graph in B) | Admission denies (namespace/vCluster boundary + gateway partition); logged as a security event |
| Tenant deleted upstream in IdP | `IdPReady=False`; no auto-recreate (identity objects are never auto-managed away — 06 D3) |
| Soft-mode noisy neighbor | Quotas + gateway partitions bound it; the escalation path is documented as "move to hard mode", which is a CR field change |

## 7. Security

Per-tenant credentials never co-mount (except the soft-mode runtime limitation, stated in §3 and mitigated by hard mode); the tenant-operator holds the only **provisioning** cross-tenant privilege and is auditable (receipted under `principal: tenant-op`); the host-side policy compiler holds cross-tenant *read* on tenant CRs plus host gateway write — never tenant-workload write (R2-1), so a compromised compiler cannot create workloads in any tenant (A1 adds exactly one tenant-side write: `create` on ConfigMaps in the tenant agent-operator's namespace, for the routes-revoked marker — never a write to any Agent, whose status is design 02 A37's authority); export-before-delete keeps audit trails intact; admins are IdP groups (contextual tuples, 24) — no separate tenant admin store.

## 8. Testing

Fan-out idempotency (create ×3 ⇒ same state); isolation battery per layer (account A cannot read B's streams/KV; RLS denies cross-tenant rows; gateway partition rejects cross-tenant principals; FGA store isolation; pack-source isolation); hard-mode e2e (vCluster provisioned, agent deployed inside, receipts land in the tenant account); suspend/resume; delete-without-export refusal; skew condition.

## 9. Decisions for async review

- **D1 — The Tenant CR adds exactly two new mechanisms** (tenant-scoped quota intents; per-tenant SPIRE + federation) **and inherits every other layer** — the ADR-0013 thesis, tested and corrected rather than asserted (r1 f1/f2).
- **D2 — Hard mode splits the control plane**: workload-managing operators per-tenant inside the vCluster; the policy compiler host-side (R2-1/R2-2). Cost is the §4 table's range (~4–5 pods core-only; +2–3 for a plus tenant), not the full core set — NATS/Postgres/Zitadel/OpenObserve/gateway are shared.
- **D3 — Soft mode's shared runtime holds three credential classes** (consumer creds, workflow-actor clients, one DBOS role) and its **DBOS state is not RLS-isolated** — the complete gap; hard mode is the answer for untrusted tenants.
- **D4 — Delete requires export-first + explicit confirmation**; IdP orgs deactivate, never delete.

## 10. Resulting ADRs

ADR-0026 (P5+ent) after critique PASS.

## A1 (2026-09-03, from design 02 A42/A60) — which side of the vCluster split owns the run namespace

Design 02 A42 moves every Agent's workload, Service, routes and revision material into an operator-created `assayd-run-<agent-namespace>`, so that a principal with ordinary `create`/`delete` on ConfigMaps in the Agent's namespace cannot replace a published revision's material. This design splits the control plane in hard mode (§4) and said nothing about where that namespace lives. It does now.

| Mode | Who creates the run namespace, and where | Where the host-side objects land |
|---|---|---|
| **Soft** | the shared **agent-operator**, on the host, one per tenant namespace — exactly design 02 §3.2, nothing tenant-specific | the same namespace: routes, Backends and policies go beside the Service (design 03 A44) |
| **Hard** | the tenant's own **agent-operator inside the vCluster** (§4: workload-managing operators are per-tenant and hold no host credentials), as a vCluster namespace. The tenant's users see and administer their Agents through the vCluster API, so that is the API against which the "editor has no rights where the material lives" claim is made — and it holds **against tenant users under vCluster RBAC, not against the tenant's vCluster admin**, who is cluster-admin inside the vCluster and can read the binding, recreate the run namespace and replace any copy. A tenant subverting its own governance is not cross-tenant, but it is a bypass of ADR-0006's gate for that tenant, and design 27's compliance profiles must not read hard mode as if the gate were admin-proof. So the Tenant CR carries **`GatesTenantAdminBypassable=True`** in hard mode, always, in §3's condition list, and the binding record of design 02 A60 is kept in the tenant operator's own vCluster namespace | the **vCluster's host sync namespace**. vCluster syncs a tenant Pod, its Service, and the ConfigMaps and Secrets that Pod references into the one host namespace it owns, under **rewritten, opaque names** — vCluster's own documentation states that Service names are translated. The host-side policy compiler writes routes, Backends and policies **there**, and must reference the synced Service by its host name — the only namespace in which a `backendRef` resolves without a `ReferenceGrant`, which is design 03 A44's rule applied to the synced object. **The compiler cannot discover that name**: design 03's `PolicyIntent` carries only `target: {kind, ns, name}` from the Agent CR, and guessing a synced name binds to the wrong Service after a recreate. So hard mode needs a **producer contract design 03 does not have yet**, recorded there as A45: the tenant-operator resolves and persists `{tenantUID, virtualNamespaceUID, virtualServiceUID, hostNamespace, hostServiceName, hostServiceUID, mappingGeneration}`, the compiler consumes it and refuses a stale or ambiguous mapping, and the hard-mode e2e recreates the Service and proves the route follows the new UID. **Hard mode is not implementable until that contract exists** |

Four consequences, each named because propagation is the failure this cross-design pass keeps finding:

1. **The tenant-operator labels the host sync namespace `assayd.dev/run-namespace: "true"` and `assayd.dev/tenant-sync: <tenant>` at vCluster provisioning — and never `assayd.dev/pods-by: agent-operator`.** A sync namespace is never a SPIFFE-selected namespace on the host, because a tenant creates the Pods in it: the host `ClusterSPIFFEID` selects on `pods-by` and excludes `tenant-sync` (design 07 A5.2), so a tenant Pod carrying `assayd.dev/agent-namespace: team-a` is issued nothing in the host trust domain. Whether vCluster carries Pod labels to the host verbatim is **unmeasured**; the two-label split holds either way. The tenant-operator is the only hard-mode writer of `run-namespace` and `tenant-sync` on a sync namespace because design 07 A5.9's admission policy names its identity, not because anyone is trusted to refrain. The Gateway listener admits routes by that label (design 07 A5.3); without it every hard-mode route is rejected and every hard-mode Agent wedges at `Converging`, which is the failure design 02 A45 found for soft mode. The tenant-operator owns the label because it owns the vCluster and the host namespace it syncs into; the tenant's agent-operator cannot — it holds no host credentials.
2. **Material on the host is a synced copy in a platform-owned namespace.** The tenant never holds host credentials, so it cannot touch the host copy *directly*; but the host copy is vCluster's sync of a virtual object, so whatever the tenant's vCluster admin can do to the virtual copy — which is anything, per the Hard row above — reaches the host copy through the sync. The host boundary adds nothing against the tenant's own admin; it protects one tenant from another and from host principals without rights in the sync namespace, who are platform operators — the trust base this design already assumes for the compiler.
3. **The `ClusterSPIFFEID` lives inside the vCluster**, with the per-tenant SPIRE this design's §4 and design 06 A1 place there, and renders the same template as design 07 A5.2 — `index` form, whole-segment labels, `Exists` selectors, a `pods-by: agent-operator` `namespaceSelector`. The identity issued is `spiffe://<tenant-trust-domain>/agent/<agent-namespace>/<agent>`, federated to the host. **This inherits an unmeasured premise, and the critique found a reason to doubt it**: spire-controller-manager always adds a `k8s:pod-uid:<uid>` selector to every entry, using the UID of the Pod *it* sees — the virtual one — while the SPIRE agent attesting the workload asks the host kubelet, which reports the host Pod's UID. Unless vCluster preserves Pod UIDs across the sync, which its documentation does not claim, no entry matches and no hard-mode Agent gets an SVID. Design 06 gains A3 recording the same doubt. **Owed: a spike** — a vCluster, the host spire-agent, a controller-manager inside, one labelled Pod — before this point is relied on; the fallbacks (the host controller-manager with a tenant-aware template, or `workloadSelectorTemplates` mapping to host selectors) change both this point and the selector above.
4. **Teardown is ordered across the split, because Kubernetes orders finalizers not at all.** Design 02 §3.7's first two steps — drain to weight 0, revoke routes — are host writes the tenant's agent-operator cannot make; its last steps — workloads, copies, the run namespace — are vCluster writes the compiler must not make. Run concurrently, the workload dies while the route still names it, and in-flight tasks get connection failures instead of the `taskTimeout` drain §3.7 promises. So: the **compiler** owns drain and revoke and, when done, writes a **marker** — a ConfigMap `assayd-routes-revoked-<agent-uid>` in the tenant agent-operator's own namespace inside the vCluster. That makes design 03 the marker's producer, and design 03 today has no hard-mode deletion lifecycle at all — no watch on tenant Agent deletion, no vCluster client ownership, no stated postcondition for "revoked". Design 03 A45 records the producer side as owed; until it exists this ordering is the consumer's requirement, not a mechanism. That marker is the **only** tenant-side write the compiler holds: `create` on ConfigMaps in that one namespace, granted by a namespaced Role, named here as the widening of §7's "read only" that it is. It is deliberately **not** a write to the Agent's status: design 02 A37 makes `status.activeRevisionDigest` the collision guard's authority *because* only the agent-operator may write it, and a compiler holding `agents/status` `patch` in every hard-mode vCluster would hand a compromised compiler the power to mark an ungated spec as the active revision. R3-a's "finalizer on the tenant-side CR" is likewise the **tenant agent-operator's** finalizer, not one the compiler places — placing one needs `agents` `patch`, which on a CRD reaches `spec`. The **tenant's agent-operator** waits for the marker before deleting workloads and running the binding protocol (design 02 A60), deletes it when done, and while waiting sets `Degraded=True, reason: FinalizationStalled` with a message naming `AwaitingRouteRevocation` (design 02 §5); it never force-proceeds on **Agent** deletion. A **tenant** deletion is different: §5 exports, the compiler revokes every host resource by label, and the vCluster is torn down with its API server and every finalizer in it — there is nothing left to wait. A tenant **delete** in §5 tears the vCluster down after export, which removes the run namespace with it; while the vCluster's host namespace is `Terminating`, every host-side create fails 403 with cause `NamespaceTerminating` (`docs/research/tenant-namespace-primitives-2026-09.md` §4), and the compiler treats that cause as "stop creating, let finalizers drain" rather than as an error to retry.

**One vocabulary, corrected.** R3-a (§5) labels host resources `assayd.dev/tenant` + `assayd.dev/source-uid`; design 03 A44 labels every emitted resource `assayd.dev/agent-uid` + `assayd.dev/revision`. Those are two names for one idea, which `write-spec` forbids. The compiler's labels are design 03's: **`assayd.dev/agent-uid`** is the source UID, and `assayd.dev/tenant` is added on top in hard mode. R3-a's `assayd.dev/source-uid` is withdrawn in favour of `assayd.dev/agent-uid`.

**Nothing here is implemented.** The tenant-operator does not exist; the agent-operator's run namespace (design 02 A42) is not implemented at the time of this amendment; the vCluster sync behaviours relied on above are the documented defaults and have not been measured on a cluster. The hard-mode e2e in §8 gains the case: an Agent inside the vCluster whose route on the host reaches its synced Service, with the tenant's editor unable to replace the revision's material from either API.

**Quota is per namespace, and the Tenant API says so (decided 2026-09-04, ADR-0026 Amendment 1).** Design 02 A60 mirrors a tenant namespace's `ResourceQuota` into its run namespace, which is the only Kubernetes-native way to keep Agent Pods under a quota at all, and it means the compute dimensions of §3's `spec.quotas` (`gpu`, and whatever the tenant's own quota carries) bound **each namespace separately**: a tenant with `gpu: 1` can hold one ordinary Pod in `team-a` and one Agent Pod in `assayd-run-team-a`. `QuotasEnforced` today reads as if it covered one ceiling. Two honest resolutions were put to the user: **(a)** define compute quotas in this API as per-namespace and say so in `QuotasEnforced`'s message, keeping the gateway-enforced traffic quotas (`tokensPerDay`, `usdPerDay`) as the one-ceiling dimensions they already are; or **(b)** enforce an aggregate with a tenant-scoped quota controller or partition dimensions exclusively between the two namespaces. **The user chose (a).** So: `spec.quotas.gpu` and every dimension a tenant's own `ResourceQuota` carries are **per-namespace ceilings** — the tenant namespace and its run namespace each get the full value, and a tenant can reach twice it across the pair. `QuotasEnforced=True`'s message states that in so many words (`compute quotas apply per namespace: <ns> and assayd-run-<ns> each`), so a reader of the condition is never told there is one ceiling. `tokensPerDay` and `usdPerDay` are gateway- and receipt-enforced against the Agent's identity, not a namespace, and remain single ceilings. (b) stays available as a later mechanism if a tenant needs an aggregate; it is a controller, and nothing here forecloses it. The cross-family review rejected calling two mirrors one ceiling, and it was right; this is the sentence that stops the API saying so.

**ADR-0026 said the compiler holds "tenant CR read only"**, and this amendment gives it one tenant-side write (the marker ConfigMap). ADR-0026 **Amendment 1** (2026-09-04) records that write, the per-namespace quota decision above, and `GatesTenantAdminBypassable`; the other decisions of ADR-0026 stand, which is why it is an amendment and not a supersession. Design 02 A42/A60 — an operator-owned namespace and a cluster-wide binding protocol — is recorded in **ADR-0029**.

**Owed to other designs by this amendment, named so they propagate.** Design 27 must consume `GatesTenantAdminBypassable` — a compliance profile on a hard-mode tenant is a profile whose gate the tenant's own admin can bypass, and 27 says nothing about hard mode today. Design 24 must state that the ext-authz adapter's SVID-to-subject mapping includes the **trust domain**: a hard tenant's SPIRE issues `spiffe://<tenant-td>/agent/team-a/reviewer` and a soft tenant's is issued in the host domain with the same path, and design 24's subjects (`agent:pa-reviewer`) carry neither namespace nor domain, so without it two tenants' principals collide. Both are pre-existing gaps in §4 and design 06 A1 that this amendment is the first to write beside the text a reader will cite.

## A2 (2026-09-04, from Codex r8 BLOCKER 11 against design 03) — `TenantPolicyIntent` is one sentence, and design 03 has stopped compiling against it

D1 counts tenant-scoped quota intents as one of this design's **two new mechanisms**. What exists of it is a single clause inside §3's Traffic row: *"a `TenantPolicyIntent` (tenant-scoped target kind) recorded as a design 03 amendment with its own row"*. There is no schema, no field list, no owning controller, and "tenant-scoped target kind" is never made concrete — design 03's `PolicyIntent` has `target: {kind, ns, name}` and nothing here says what a tenant target is.

Design 03 had filled the gap by assuming: its provenance table gave `expose[].consumerBudgets` an owner of "design 26 `TenantPolicyIntent`", and **this design never uses the word `consumerBudgets` at all**. It then carried that value inside each Agent's immutable per-revision record and transacted it per route — so one shared tenant limit had N independently authoritative applied operands, each frozen at a different Agent's revision mint. Lowering a tenant's budget while a hundred Agents serve would freeze the old value in some records, withdraw in others and fail in the rest, with nothing owning the set-wide desired-versus-applied state. Design 03 A48 removes the field rather than leave it pointing at a producer that does not exist.

**Owed here, before tenant quotas can be compiled at all:**

| | |
|---|---|
| The intent | `TenantPolicyIntent`'s schema and its **tenant-scoped target** — what a target of kind Tenant *is*, given the compiler's existing target is `{kind, ns, name}` and a tenant spans namespaces |
| The record | a `TenantPolicyRecord` with its own `schemaVersion`, digest and applied status **on the Tenant**, not scattered across Agents: the tenant is the object that owns the limit, so it is the object that must carry what was applied |
| Membership | the **route-membership snapshot** the record was applied against, since a tenant's route set changes as Agents come and go, and "applied" means nothing without saying to which routes |
| The transaction | its own stages, retry and partial-failure semantics, and what `QuotasEnforced` may claim when some routes converged and others did not. Design 03 A49 is the cautionary case: a per-route withdrawal cannot prove the dataplane stopped serving, so a tenant quota that must actually bind needs an enforcement point assayd runs — the exact-tier rollup in design 04's audit index — rather than an apply the gateway may reject |
| The backstop tier | §3's Traffic row already names it: a per-tenant rollup in design 04's audit index. That is the half assayd itself runs and can therefore make bind. The gateway half is the approximation tier, and ADR-0028 says it is not a ceiling — nor is the rollup exact in USD, since both tiers price from the same table |

Until this lands, `consumerBudgets` has no compiler path and a tenant's traffic quota is **not enforced at all** — §6's "Quota exceeded" row describes a mechanism that does not yet exist. Nothing here is implemented, and neither is the tenant-operator that would own it.

