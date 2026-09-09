# Review: Design 07 — Umbrella chart, profiles, e2e CI

- **Verdict**: **REVISE**
- **Reviewed**: `docs/designs/07-umbrella-chart-ci.md` (draft, 2026-08-20)
- **Independence note**: reviewed in an independent session that did not author the draft.
- **Method**: all 8 lenses, with extra weight (per the component's nature) on consistency against every approved/passed design and on whether the CI gates are mechanically enforceable as specified.

## Findings

### 1. MAJOR — the stateful-dep CI grep fails the chart's own core render

`07-umbrella-chart-ci.md:14`. The gate — fail on "any other StatefulSet/PVC-bearing workload" beyond Postgres + NATS — is aimed at doctrine rule 2, but rule 2 governs *platform substrate dependencies*, and the check as written tests something else: disk usage. The chart's own core set trips it: **OpenObserve** (architecture §07/§17, core, 1 pod) stores metrics/logs/traces on local disk — a PVC in any durable install; **SPIRE server** typically carries a datastore (sqlite on a PVC by default). So the gate either fails every render of the chart it ships in, or someone adds a silent exception and the gate stops meaning anything. An enforcement mechanism that cannot pass its own baseline is the exact failure the testability lens exists to catch.

**Fix**: define the check against an explicit, versioned allowlist with *reasons* (`postgres`, `nats` — substrate; `openobserve` — observability data, not substrate, per doctrine rule 2's enumeration; anything else fails). Adding to the allowlist requires touching the allowlist file — preserving the "justify in the same PR" property. Separately worth one line: point SPIRE server's datastore at the platform Postgres (it supports it) — one less PVC and doctrine-purer.

### 2. MAJOR — `tier: core` semantics collide with eval-as-admission: prod agents require gates that core cannot run

`07-umbrella-chart-ci.md:35` vs design 02 §3.1/§6 and architecture §01 rule 6 / §08. Evals are plus-tier (rule 6), yet design 02's admission requires ≥1 eval gate on prod agents and the rollout machinery holds every candidate at weight-0 until the gate controller passes it. A core-tier prod install therefore either rejects every prod Agent at admission or strands every candidate `Held` forever — and `helm upgrade --set tier=core` on a running plus install ("cleanly drops plus components") strands in-flight rollouts mid-canary with no stated behavior. The design says tiers are "independently removable" and verifies orphaned CRs, but the *semantic* orphan — gating promises with no gate runner — is the one that matters.

**Fix**: state the core-tier gating contract explicitly. Recommended: admission's gates-required rule is conditional on the EvalSuite CRD being installed; on a core-only install, rollouts proceed with a loud `GatesSkipped=True` condition (NFR-8's visible-degrade pattern — never silent); tier *downgrade* pre-hook refuses while any rollout is in a `Held`/`Canary` state (same spirit as the contract-version pre-hook). Record whichever is chosen as a design-02 §11 delta — this changes approved admission behavior.

### 3. MINOR — "CNPG or bitnami" is an unresolved either-or, and one arm is effectively dead

`07-umbrella-chart-ci.md:29`. CLAUDE.md rule 6: options come with reasoning and a recommendation, never a menu. And the menu contains a stale option: verified this session, the [Bitnami public catalog was gutted effective 2025-08-28](https://github.com/bitnami/charts/issues/35164) — images moved to a frozen `bitnamilegacy` repo, updates behind Broadcom's paid Bitnami Secure — making a *pinned, renovate-managed* bitnami postgres subchart a dead end ([migration context](https://www.chkk.io/blog/bitnami-deprecation)). **Fix**: pick [CloudNativePG](https://github.com/cloudnative-pg) (CNCF, operator-managed, the community's post-Bitnami default) with one sentence of reasoning; note the trade (CNPG is an operator + CRDs, slightly heavier than a plain StatefulSet — still the right call for lifecycle/backup).

### 4. MINOR — k3s is named by the doctrine but absent from the matrix

`07-umbrella-chart-ci.md:51` vs architecture §01 rule 5 ("minikube, k3d, kind, k3s included"). The omission is probably fine — k3d *is* k3s-in-docker, so per-merge k3d coverage is k3s API coverage — but that's an argument the design must make, not the reviewer. **Fix**: one line in D3 ("k3s is covered via k3d by construction; bare-metal k3s quirks ride the minikube weekly cadence" or similar).

### 5. MINOR — the budget-ledger counting rule is undefined

`07-umbrella-chart-ci.md:13`. "Counts rendered pods against NFR-1" — counting what, exactly? The §17 budget is "≈8 pods" with ranges (agentgateway 1–2), the prod profile sets replicas>1, and SPIRE agent is a DaemonSet (node-count-dependent). A mechanical gate needs a mechanical metric. **Fix**: define it — count *workloads* (Deployments/StatefulSets/DaemonSets) against a named-workload budget list, ignoring replica scaling; the ledger file names each allowed workload, and a render containing an unlisted workload fails. (This also makes finding 1's allowlist and this one the same file — cleaner.)

### 6. MINOR — rollback with newer CRDs is the skew the version guard doesn't cover

`07-umbrella-chart-ci.md:43,64`. The guard covers "operator refuses to run against *older* CRD schema", but `helm rollback` produces the opposite: older operator + newer CRDs (Helm never rolls back `crds/`). Newer schemas are usually tolerable (unknown fields ignored) but that's an assumption, not a stated contract. **Fix**: state the rollback skew rule — CRD schema changes within a support window must be forward-compatible (older operator ignores newer optional fields); a breaking CRD change bumps the version guard *both* directions and blocks naive rollback with a named error.

### 7. MINOR — the chart doesn't apply the platform's own supply-chain bar to itself

`07-umbrella-chart-ci.md:19-33` vs architecture §06 (cosign-signed images, admission-enforced). Agent images must be signed; the platform's own subchart images are referenced by pinned *version*, not digest, and the chart itself is unsigned. **Fix**: pin subchart images by digest (renovate handles digest bumps), sign + publish provenance for the umbrella chart, and note whether the shipped `ValidatingAdmissionPolicy` exempts or also covers platform images (either is defensible; silence is not).

### 8. MINOR — §2 omits the charter's socket/primitives line

`07-umbrella-chart-ci.md:11-14`. Every design states socket + primitives (TEMPLATE §2); this one only does plane/pods/stateful. The honest answers are easy ("socket: n/a — distribution of the engine; primitives: none introduced — packages Resource-bearing components") — write them, since the gate table in `audit-docs` will flag the hole anyway.

### 9. MINOR — the contract-compatibility ledger is missing two contracts and its storage mechanism

`07-umbrella-chart-ci.md:42`. The ledger lists `kgp/v1alpha1`, `idp/v1alpha1`, `receipt/v1`, semconv SHA — but not **`ontology/v1`** (design 01 §3.5, a versioned contract agents and packs depend on) nor the **OASF 1.1.0 pin** (design 05). And "compares against the running install" needs a stated record (a ConfigMap written at install with the contract set — name it, since the pre-hook must read something). **Fix**: complete the list; name the record.

## Lens summary

1. **Doctrine**: the *intent* is the best in the series — doctrine rules 2/5 as merge gates is exactly "the reconcile loop is the product" applied to the repo itself; findings 1/5 are about making the gates actually computable.
2. **Charter**: finding 8 (missing template line); otherwise n/a-clean.
3. **Contract consistency**: findings 2, 3, 4, 9 — the chart faithfully packages designs 01–06 (bootstrap jobs cover identity/streams/KV/pricing; accounts template matches 04 D4; spire set matches 02 §3.5; the contract-gate idea operationalizes N/N−1) with the tier/eval collision as the one real conflict.
4. **Hidden dependencies**: renovate + pinned subcharts is sound; finding 3's dead upstream is the hidden dependency that already rotted.
5. **Failure modes**: good table; finding 6 (rollback skew) is the missing row.
6. **Security**: finding 7 — hold the chart to its own bar.
7. **Research freshness**: the bitnami arm was the stale claim (verified, cited); agentgateway 2.2.x chart pinning is consistent with the landed research note.
8. **Testability**: the matrix scenario is concrete and decidable (assertions name conditions, streams, and a 20-min wall-clock); golden `helm template` snapshots per profile/tier are the right regression net; findings 1/5 are the two gates that aren't yet mechanically well-defined.

## Disposition

**REVISE.** The design's central idea — the chart as the doctrine's enforcement point, with the weight budget and stateful-dep rules as CI gates — is exactly right, which is why the two MAJORs matter: as specified, one gate fails its own baseline (OpenObserve's PVC) and the tier model breaks approved admission semantics (gates with no gate runner). Both have clean fixes (a reasoned allowlist file; a stated core-tier gating contract recorded as a 02 delta). The rest is completion work.

VERDICT: REVISE — 9 findings

---

## Re-review r2 (2026-08-20)

- **Verdict**: **PASS** (1 residual MINOR)
- **Independence note**: same independent session as r1; did not author the draft or revision.

### Per-finding disposition

| r1 | Severity | Disposition |
|---|---|---|
| 1 | MAJOR | **Resolved.** `stateful-allowlist.yaml` — explicit, versioned, with reasons (`postgres`/`nats` substrate, `openobserve` sink) — replaces the bare grep; adding an entry means touching the file in the same PR, preserving the justify-in-PR property while passing the chart's own baseline. Bonus: SPIRE server's datastore moved to platform Postgres (one less PVC, doctrine-purer) — the suggested note, taken. |
| 2 | MAJOR | **Resolved.** The core-tier gating contract is exactly the recommended shape: gates-required admission iff the EvalSuite CRD is installed; `GatesSkipped=True` loud on core-only rollouts; prod-profile installs warn; tier downgrade refused while any Agent is Held/Canary; recorded as design 02 §11 A4. |
| 3 | MINOR | **Resolved.** CNPG chosen with the reason cited (bitnami free images discontinued). |
| 4 | MINOR | **Resolved — beyond the ask.** k3s added as a real weekly cell rather than argued away via k3d. |
| 5 | MINOR | **Resolved.** Counting rule fully mechanical: Σ `spec.replicas` over Deployments/StatefulSets at prod/core, DaemonSets = 1 logical, Jobs excluded, compared against `weight-budget.yaml` (the §17 table as data). |
| 6 | MINOR | **Resolved.** CRDs never rolled back; schema changes additive within N/N−1; the version guard checks both directions. |
| 7 | MINOR | **Resolved.** Cosign-signed OCI chart, digest-pinned images, SBOM — "the same admission story agents get". |
| 8 | MINOR | **Resolved.** Charter line added (plane/primitives; ships sockets, defines none). |
| 9 | MINOR | **Mostly resolved** — `assayd-contracts` ConfigMap named, `ontology/v1` added. Residual **R2-a**: the OASF 1.1.0 pin (design 05) is still absent from the ledger, and `pack/v1` is forward-declared before design 18 exists (fine, but mark it reserved). Cosmetic: D1 still says "stateful-dep grep" — update the word to match §2's allowlist. |

### Residual (MINOR, non-blocking)

- **R2-a** — add the OASF pin to the `assayd-contracts` ledger; mark `pack/v1` as reserved-until-design-18; fix D1's stale "grep" wording.

### Verdict

**PASS.** Both MAJORs closed structurally — the allowlist makes the doctrine gate self-consistent, and the tier/gating contract turns the collision into defined, loud behavior recorded where it belongs (02 §11 A4). Fold into ADR-0022 with R2-a.
