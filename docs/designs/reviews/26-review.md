# Review: Design 26 — Tenant CR (the multi-tenancy fan-out)

- **Verdict**: **REVISE**
- **Reviewed**: `docs/designs/26-tenant-cr.md` (draft, 2026-08-20)
- **Independence note**: reviewed in an independent session that did not author the draft.
- **Method**: all 8 lenses, organized around the requested test — **D1's thesis ("adds no new isolation primitives") checked seam by seam** against every design that named a tenancy seam, with particular attention to design 21's shared runtime and hard mode's vCluster operator sets.

## The thesis, tested seam by seam

| Seam | Claimed source | Holds? |
|---|---|---|
| RECEIPTS streams | 04 D4 (per-tenant stream in tenant account; core = n=1) | ✅ genuinely pre-existing |
| EVENTS streams | 11 r2 (same shape, 07 bootstrap creates) | ✅ |
| APPROVALS bucket | 22 r2 (per-tenant KV in tenant account) | ✅ |
| DIRECTORY bucket | 05 (bucket per NATS account) | ✅ |
| IdP orgs | 06 §3.1 `ensureTenant` | ✅ the interface exists *for this* |
| FGA stores | 24 (stores are OpenFGA's native isolation unit) | ✅ |
| Pack allowlists | 18 r2 §5 (per-tenant partitioning named) | ✅ |
| **Gateway partitions** | "03 compiles from the Tenant's quotas the same way it compiles Agent budgets" | ❌ **finding 1** — no such concern exists in 03 |
| **Workflow runtime** | 21 r2 f4 seam | ⚠️ **finding 3** — resolved for consumer creds, not for actor creds or the DBOS role |
| **Identity in hard mode** | (unclaimed — and that is the problem) | ❌ **finding 2** — SPIRE trust domains are a new mechanism |

Seven of ten hold cleanly, which is a real vindication of ADR-0013's "mostly inherited" and of every prior design that chose tenant-shaped primitives. The three that don't are below.

## Findings

### 1. MAJOR — "gateway partitions" is a new compiler concern, not an inherited primitive

`26-tenant-cr.md:42` vs design 03 §3.4 and ADR-0020. The Traffic row claims 03 "compiles from the Tenant's quotas the same way it compiles Agent budgets" — but design 03 has **no partition concept, no tenant-scoped listener set, and no Tenant CR input**. Its compiler takes `PolicyIntent` per *target CR* (Agent/Workflow/KG/App); a tenant-level quota is a new intent kind, a new policy scope (the tenant's whole principal set), and possibly a new listener topology. Three specific gaps:

- **Nothing produces the intent.** ADR-0020's compiler is "pure and total" over declared inputs; `Tenant.quotas` is not among them.
- **The approximation tier is unrestated.** ADR-0020 was explicit that gateway-side budgets are a *local approximation* (rate ÷ replicas) with the receipt backstop as the exact tier. A tenant quota of `tokensPerDay: 20M` inherits that approximation, but §6's "two-tier, like ADR-0020's own tiers" refers to something else entirely — budget *nesting* (per-agent within per-tenant), not approximate-vs-exact. As written, a reader concludes tenant quotas are exactly enforced. They cannot be, by the same arithmetic.
- **The exact tier has no aggregate.** Design 04's spend aggregation is *per agent*; a per-tenant daily aggregate (the backstop for tenant quotas) doesn't exist.

**Fix**: state honestly that this seam **does** add a mechanism, and specify it — a `TenantPolicyIntent` (or a tenant-scoped target kind) recorded as a design 03 amendment with its own row; the tenant-level exact tier as a per-tenant rollup in design 04's audit index (cheap — the rows already carry `tenant`); and the approximation caveat restated at tenant scope. D1 then reads truthfully: "adds no new isolation primitives **except tenant-scoped traffic quotas, which extend the existing budget mechanism**" — a far stronger claim than one a reader can falsify.

### 2. MAJOR — hard mode's shared/per-tenant boundary is underspecified, and at three points self-contradictory; workload identity is the sharpest

`26-tenant-cr.md:37,44,49`. §4 says the tenant gets "its own API server, **its own plume operator set**, its own runtime", while §3's matrix keeps NATS, Postgres, and Zitadel **shared** (account / roles / org). Those two statements can't both be complete, and three concrete consequences go unaddressed:

- **SPIRE / trust domains (the new mechanism the thesis denies).** Design 02 §3.5 issues SVIDs from one platform ClusterSPIFFEID templated on `.PodMeta.Namespace` + the agent label. vCluster runs tenant workloads as **host pods in a synced host namespace**, so a shared host SPIRE attests them with the *host* namespace — collapsing every tenant agent into one namespace segment and breaking the identity scheme's granularity. The alternative — a SPIRE server per vCluster — means N trust domains that the (shared) gateway must federate: SPIFFE federation is unquestionably a new mechanism, and nothing in designs 02/06 provides it.
- **Write direction across the vCluster boundary.** If the gateway is shared (as §3's "gateway partition" implies) but the operators run *inside* the vCluster, those operators must write gateway CRs on the **host** cluster — requiring host-cluster credentials that defeat the isolation hard mode exists for. So either the gateway is per-tenant too (a cost §4 doesn't count), or the compiler stays host-side (and the operator set is *not* per-tenant).
- **The cost statement contradicts the matrix.** §4 prices hard mode at "~the core pod set per tenant" (§17's ≈8, which includes NATS, Postgres, Zitadel, OpenObserve) — but §3 shares exactly those. The real per-tenant set is smaller in some places and larger in others (finding 4).

**Fix**: add a "what is shared vs per-tenant in hard mode" table — one row per §17 component — and resolve identity explicitly (recommendation: SPIRE server per vCluster with **federation to the host trust domain**, recorded as a design 06 amendment and priced into §4; the alternative, host-SPIRE with vCluster-aware selectors, needs a verified attestation story before it can be claimed). Whichever is chosen, D1 must acknowledge it: this seam adds a mechanism.

### 3. MAJOR — the Data row's RLS claim cannot hold under soft mode's shared runtime, and D3 understates its own gap

`26-tenant-cr.md:39,44,80` vs design 21 §8 and `connectors-2026-08.md` §DBOS. §3 claims per-tenant "roles + RLS policies on shared platform tables (**DBOS state**, audit index, eval results)". But a single shared `workflow-runtime` process connects to Postgres with **one** DBOS runtime role (the research note's own migrate/runtime role split assumes one app identity), so RLS — which discriminates by connection role — cannot separate tenants' DBOS state inside that process unless the runtime switches roles per workflow, which nothing states and DBOS's connection-pool model does not naturally do. A claimed isolation layer whose mechanism doesn't exist is exactly what the thesis must not contain.

Relatedly, D3's honest gap is narrower than the reality: the shared runtime holds not just "N tenants' consumer credentials" but also, after 21 r2, every tenant's **`workflow-actor` client credentials** (the very identities the gateway keys budgets and ReBAC on) *and* the shared DBOS role above.

**Fix**: either (a) scope the claim — soft mode's DBOS state is **not** RLS-isolated; isolation there is process-level and the row should say so, with hard mode as the answer (consistent with D3's spirit, just complete); or (b) specify per-tenant role switching (`SET ROLE` per workflow transaction) and test it. Update D3 to enumerate all three credential classes, since its whole value is being the honest gap.

### 4. MINOR — hard mode's cost is understated wherever a tenant runs plus-tier components

`26-tenant-cr.md:49`. A hard tenant using workflows adds a per-tenant `workflow-runtime` (§3, +1–2), and one using ReBAC adds the plus-tier OpenFGA pod + co-located adapter (24 §2) — since an in-vCluster operator set can hardly share the host's FGA store writer. "~the core pod set per tenant" is the floor, not the number. **Fix**: state the range by tier (core-only tenant vs plus tenant), which also makes the commercial conversation honest.

### 5. MINOR — `TenantVersionSkew` has no comparison mechanism

`26-tenant-cr.md:51`. A lagging tenant vCluster means **two** `plume-contracts` ledgers (07 §4) — host and tenant — and the N/N−1 rule now applies *between installs*, not just across an upgrade. Who compares them, what blocks on skew, and does a tenant one version behind still receive host-side compiled config? **Fix**: name the comparator (the tenant-operator reads both ledgers at reconcile), state what a >1 gap blocks (tenant upgrades, not tenant traffic), and surface it on the condition already defined.

## Lens summary

1. **Doctrine**: clean — 1 enterprise-module pod, no new stateful deps, and the open-core separation (consumes only public CRs/contracts, so OSS core carries no enterprise hooks) is exactly ADR-0015's shape.
2. **Charter**: Resource + Event only; no new primitives at the *charter* level even where new mechanisms appear (findings 1–2 are mechanisms, not primitives — worth distinguishing, and the design would be more defensible if it drew that line itself).
3. **The thesis** (requested test): **holds at 7 of 10 seams** — a genuinely strong result that vindicates ADR-0013 and the prior designs' discipline. It fails at gateway partitions (new compiler concern), workload identity in hard mode (SPIRE federation), and the RLS-under-shared-runtime claim. The fix is not to abandon D1 but to bound it: "no new isolation primitives; two new mechanisms, both extensions of existing ones, named here."
4. **Contract consistency**: findings 1/3/5; elsewhere excellent — every bucket/stream/store/org citation checks out against the design that promised it, which is exactly what makes the seven ✅ rows credible.
5. **Hidden dependencies**: finding 2's write-direction problem is the classic one (a control loop that must write across the isolation boundary it enforces).
6. **Failure modes**: strong — partial fan-out with per-layer conditions, cross-tenant reference denial as a *security event*, "move to hard mode is a CR field change" as the noisy-neighbor escalation, and the blast-radius framing of vCluster failure.
7. **Security**: §5's **export-before-delete** is the standout of this design — "deleting a tenant must not silently destroy an audit trail" made structural, plus `confirmDelete` and IdP-orgs-deactivate-never-delete (06 D3 at tenant scope). The tenant-operator as the sole cross-tenant privilege, receipted, is the right containment.
8. **Testability**: the per-layer isolation battery is the correct shape and should be the enterprise tier's flagship test suite; add, after the findings: a soft-mode DBOS-state isolation test (which will fail today — that's the point), a hard-mode SVID-path assertion, and a tenant-quota enforcement test that distinguishes approximate from exact tiers.

## Disposition

**REVISE.** The thesis is *mostly* earned, and the seven clean seams are the accumulated payoff of a design series that kept choosing tenant-shaped primitives — that result is worth stating loudly. But a thesis this strong has to be exact: two seams need mechanisms this design would rather not admit inventing (tenant-scoped traffic quotas, hard-mode workload identity), and one claims an isolation guarantee that the shared runtime cannot deliver. Bound D1 honestly, specify hard mode's shared/per-tenant table, and this becomes the strongest enterprise argument in the set.

VERDICT: REVISE — 5 findings
