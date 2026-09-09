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

`26-tenant-cr.md:37,44,49`. §4 says the tenant gets "its own API server, **its own assayd operator set**, its own runtime", while §3's matrix keeps NATS, Postgres, and Zitadel **shared** (account / roles / org). Those two statements can't both be complete, and three concrete consequences go unaddressed:

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

`26-tenant-cr.md:51`. A lagging tenant vCluster means **two** `assayd-contracts` ledgers (07 §4) — host and tenant — and the N/N−1 rule now applies *between installs*, not just across an upgrade. Who compares them, what blocks on skew, and does a tenant one version behind still receive host-side compiled config? **Fix**: name the comparator (the tenant-operator reads both ledgers at reconcile), state what a >1 gap blocks (tenant upgrades, not tenant traffic), and surface it on the condition already defined.

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

---

## Re-review r2 (2026-08-20)

- **Verdict**: **REVISE** — all five r1 findings resolved, but the fix to finding 2 introduced a new inconsistency on the isolation boundary itself
- **Independence note**: same independent session as r1; did not author the draft or revision.

### Per-finding disposition

| r1 | Severity | Disposition |
|---|---|---|
| 1 | MAJOR | **Resolved — thoroughly, and honestly.** The Traffic row now says outright that this is "**a new compiler concern, honestly**": a `TenantPolicyIntent` tenant-scoped target kind (landed as 03 §3.4's "Tenant quotas" row — verified), quota enforcement explicitly inheriting ADR-0020's **approximation tier**, and the exact tier as a per-tenant rollup recorded in **design 04 §11 A2** (verified). It even separates the two axes I flagged as conflated ("Budget *nesting* is a separate axis from approximate-vs-exact"). §1 and D1 are restated around the corrected thesis. |
| 2 | MAJOR | **Resolved in §4 — see the new residual below.** The shared/per-tenant table exists and answers the cost and substrate questions; **design 06 §12 A1** records per-vCluster SPIRE federated to the host trust domain (verified) — and does better than the finding asked by naming the upstream mechanism, `ClusterFederatedTrustDomain` from spire-controller-manager, which means federation is *configuration of an existing CRD*, not a net-new build. That materially strengthens D1: the second "new mechanism" is thinner than feared. |
| 3 | MAJOR | **Resolved — exemplary honesty.** The Data row now states plainly that **DBOS state is not RLS-isolated in soft mode** (one shared runtime, one role; RLS discriminates by connection role) with hard mode as the resolution, and D3 enumerates all three exposed credential classes (consumer creds, `workflow-actor` clients, the shared DBOS role). This is the design admitting a limitation it could have left buried. |
| 4 | MINOR | **Resolved.** §4 prices hard mode by tier: core-only ≈ vCluster control plane + SPIRE + workloads (~3–4 pods), plus tenants add workflow-runtime and OpenFGA+adapter. |
| 5 | MINOR | **Resolved.** Two ledgers, tenant-operator compares both at reconcile, a >1 gap blocks **tenant upgrades but never tenant traffic**, both versions named on the condition. |

### New findings (introduced by the r2 revision)

#### R2-1. MAJOR — hard mode now says three different things about where the assayd operators run and who holds cross-tenant privilege

`26-tenant-cr.md:37,54,81`. The fix to r1 f2 moved operators host-side, but the surrounding text didn't move with it, and the new placement doesn't survive contact with what operators actually do:

- **§3 (Control plane row)**: "the platform operators run **inside** it — see §4".
- **§4 (the new table)**: "assayd operators + policy compiler | **host-side** … They watch the tenant's API server **read-only** via the vCluster's kubeconfig".
- **§7**: "the tenant-operator holds the **only** cross-tenant privilege in the system".

The three cannot all hold. Worse, §4's own resolution is inadequate as stated: design 02's operator does not merely *watch* — it **creates** Deployments/Sandboxes, applies pod labels for SVID attestation, and writes directory entries. In hard mode those objects must be created in the **tenant's** API server, so the operator needs write access, not read-only. And if a shared host-side operator holds write kubeconfigs for every tenant vCluster, then it is cross-tenant privileged too — falsifying §7 and weakening hard mode's blast-radius story exactly where it is sold (a compromised shared agent-operator would reach every tenant's API server).

**Fix**: state the split the mechanics imply — **workload-managing operators run inside the vCluster** (they create Deployments/Sandboxes/labels against the tenant's own API server, hold no host credentials, and are part of the per-tenant cost); **the policy compiler stays host-side** (it writes gateway CRs on the host, which is why r1 f2's write-direction problem is solved) and reads tenant CRs through the vCluster kubeconfig. Then correct §3's row to match, and correct §7 to say what remains true: the tenant-operator holds the only *provisioning* cross-tenant privilege. If instead the intent is host-side operators with per-tenant write kubeconfigs, that is defensible — but §7 must then be rewritten and the blast radius stated plainly.

#### R2-2. MINOR — D2's wording is now contradicted twice by §4

`26-tenant-cr.md:90`. "Hard mode = vCluster with **its own operator set**; cost stated plainly (**core pod set per tenant**)" — but §4's corrected table makes operators host-side (or split, per R2-1) and prices a core-only hard tenant at ~3–4 pods precisely because NATS/Postgres/Zitadel/OpenObserve are *shared*. Both halves of the decision line are stale relative to the fix. **Fix**: restate D2 from §4's table once R2-1 settles the placement.

### Verdict

**REVISE.** Every r1 finding is genuinely resolved — the thesis correction is exact, the SPIRE answer is better-grounded than expected, and the DBOS admission is the kind of honesty this series exists to produce. But the r2 fix left the design's central subject — what is isolated from what, and who holds the keys — described three incompatible ways, and the "read-only" qualifier cannot support what the operators must do. This is a small, well-bounded fix (name the split, correct two sentences), and it must land before ADR-0026: an enterprise isolation boundary is not a thing to record ambiguously.

VERDICT: REVISE — 2 findings

---

## Re-review r3 (2026-08-20)

- **Verdict**: **PASS** (1 required residual + 2 smaller ones)
- **Independence note**: same independent session as r1/r2; did not author the draft or any revision.

### Per-residual disposition

| r2 | Severity | Disposition |
|---|---|---|
| R2-1 | MAJOR | **Resolved cleanly, and consistently across all four places.** §4's table gains two rows that state the split precisely: **workload-managing operators (Agent/KG/Workflow/Model controllers) run per-tenant inside the vCluster** — creating Deployments/Sandboxes, applying SVID-attestation labels, writing directory entries against the tenant's own API server, holding **no host credentials** — while the **policy compiler stays host-side**, writing host gateway CRs (which is what solved the write-direction problem) and *reading* tenant CRs through the vCluster kubeconfig. §3's Control plane row now matches rather than contradicts, and §7 is rewritten to the honest claim: the tenant-operator holds the only **provisioning** cross-tenant privilege, the compiler holds cross-tenant read plus host gateway write and never tenant-workload write, "so a compromised compiler cannot create workloads in any tenant". The three-way inconsistency is gone and the blast-radius statement is now derivable from the table. |
| R2-2 | MINOR | **Resolved.** D2 restated as "Hard mode **splits the control plane**", with the cost corrected to the §4 range (~4–5 pods core-only, +2–3 for a plus tenant) and the shared set enumerated (NATS/Postgres/Zitadel/OpenObserve/gateway). The cost figure also moved 3–4 → 4–5 to absorb the now-per-tenant operators — an internally consistent revision rather than a patched sentence. |

### New findings (surfaced by the r3 split — none is a regression of R2-1/R2-2)

#### R3-a. MAJOR *(required before ADR-0026)* — ownerRef-based lifecycle cannot cross the vCluster boundary, so deleted agents can leave live gateway config

`26-tenant-cr.md:54-55` vs design 02 §3.6 and design 03 §3.2. Both approved designs tie emitted gateway resources to their source CR by **ownerRef** — 02: "the operator owns their lifecycle (ownerRefs)"; 03: "Deterministic resource set via SSA **with ownerRefs**". Kubernetes owner references are namespace/cluster-local: a `HTTPRoute`/`AgentgatewayPolicy` on the **host** cluster cannot be owned by an Agent CR living in the **tenant's** API server. So in hard mode, when a tenant deletes an Agent, garbage collection never fires and its routes, policies, and Backends persist on the host — stale capability outliving the object that authorized it, which is precisely the property design 03 §6 exists to guarantee ("emitted policies are the only path by which agent capability changes").

**Fix**: state that in hard mode the host-side compiler reconciles deletions **explicitly** — a finalizer on the tenant-side CR (or a reconcile-time diff of live host resources against the tenant's CR set) drives teardown in 03's reverse order (routes detach before policies/backends, per §3.3), with an orphan-sweep as the backstop. One paragraph in §4, plus a note against 02 §3.6 / 03 §3.2 that ownerRef GC is the *soft-mode / single-cluster* mechanism and hard mode substitutes explicit reconciliation. This is unambiguous engineering — it just has to be written down before the ADR records a lifecycle that doesn't work in the mode this design adds.

#### R3-b. MINOR — the compiler's privilege sentence needs "status write", and the split is an unrecorded structural change to designs 02/03

`26-tenant-cr.md:55,82`. Two precision gaps in the otherwise-good §7 sentence: (a) the host-side compiler doesn't only *read* tenant CRs — it writes **conditions** onto them (`PolicyApplyIncomplete` per 03 §3.3, plus `BudgetExhausted` / `BudgetEnforcementDegraded` / `PricingStale` per 02 §11 A1), so its privilege is "cross-tenant read + **status write**, never spec/workload write" — still a strong claim, just an accurate one. (b) The split cuts design 02 §4's single reconcile loop (workload → identity → card → **compile+apply** → rollout → conditions) across two clusters and relocates design 03's "library compiled into agent-operator, no pod" to a host-side process while the operator runs per-tenant. That resolves cleanly on the platform's own precedent — a `--mode` flag on the existing host agent-operator (as with `--mode receipt-tap` and `--mode event-receiver`), so "Pods added: 0" survives — but it should be said, and the loop-split recorded as a 02/03 amendment. **Fix**: one clause in §7, one note in §4.

#### R3-c. Nit — §8 doesn't test the split's security properties

`26-tenant-cr.md:86`. The r3 revision added two checkable claims — in-vCluster operators hold no host credentials; the host compiler cannot create workloads in any tenant — and the isolation battery tests neither. Both are cheap negative tests and belong beside the existing per-layer battery (along with R3-a's orphan check: delete an Agent in a vCluster, assert zero surviving host gateway resources).

### Verdict

**PASS.** R2-1 and R2-2 are resolved completely and coherently — the split is the one the mechanics implied, it is stated identically in all four places, and §7's privilege claim is now derivable rather than asserted. R3-a is a genuine mechanical consequence that this split surfaces rather than causes (ownerRefs were always cluster-local; hard mode is the first context where that matters), and it has an unambiguous fix. Land R3-a's paragraph and R3-b's clause, then record ADR-0026.
