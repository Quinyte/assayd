# Design 07: Umbrella chart, profiles, e2e CI

- **Status**: revised r2 — awaiting re-critique (r1 REVISE, 9 findings — all addressed; reviews/07-review.md)
- **Phase**: P1 · **Size**: M · **Date**: 2026-08-20
- **ADRs**: 0002 (rules 5/6), 0012 (ambient profile), NFR-1/3/7/8 · interfaces: every P1 design (it packages them)

## 1. Purpose & scope

The single `helm install plume` that stands up the core on anything from a laptop kind cluster to a prod fleet — plus the CI that makes NFR-3 ("runs anywhere") enforced rather than aspirational. In scope: chart structure, tiers, profiles, dependency handling, upgrade/versioning discipline, the e2e matrix. Out of scope: app-level packs (design 18 installs those).

## 2. Doctrine & charter gates

- **Plane**: slow (distribution of the engine). **Pods**: none of its own — it *is* the §17 budget's ledger: the chart renders exactly the budgeted set, and a CI check counts rendered pods against NFR-1 (a PR that adds a pod fails CI unless the budget doc changes in the same PR — the "written justification" gate made mechanical).
- **Stateful deps / disk**: CI checks rendered manifests against an **explicit versioned allowlist with reasons** (`stateful-allowlist.yaml`: `postgres`, `nats` — substrate (rule 2); `openobserve` — observability sink, not substrate; anything else fails). Adding an entry means touching the allowlist file in the same PR — the justify-in-PR property, made passable by the chart's own baseline (r1 f1). **SPIRE server's datastore points at the platform Postgres** (supported upstream) — one less PVC (r1 f1).
- **Charter line (r1 f8)**: slow-plane distribution; primitives Resource (rendered CRs/manifests) + Artifact (the chart itself as signed OCI); the chart *ships* sockets, defines none.

## 3. Chart structure

```
charts/plume/                    # umbrella
  Chart.yaml                     # pinned dependency versions (renovate-managed)
  values.yaml                    # tier: core; profile: prod (defaults)
  values-local.yaml              # --profile local
  charts/                        # vendored/pinned subcharts
    agent-operator (ours)        # + CRDs, admission policies, receipt-tap mode config
    agentgateway                 # 2.2.x pinned
    spire (server+agent+csi+controller-manager)
    zitadel                      # on platform Postgres (own database); keycloak/ alt-profile
    nats                         # JetStream, accounts template
    postgres                     # CloudNativePG (r1 f3: bitnami free images discontinued; CNPG is the arm)
    openobserve
  templates/                     # cross-cutting: NetworkPolicies, ClusterSPIFFEID,
                                 # bootstrap Jobs (identity, streams/KV, pricing CM), budget-ledger test hook
```

- **Tiers (NFR-7)**: `tier: core | plus` — plus adds Argo Workflows, Phoenix, OpenFGA, eval runner images. **Core-tier gating contract (r1 f2)**: the gates-required admission rule applies **iff the EvalSuite CRD is installed**; on core-only installs rollouts proceed with a loud `GatesSkipped=True` condition (NFR-8 — visible, never silent), and prod-profile installs *warn at install time* that semantic admission is off. **Tier downgrade pre-hook refuses while any Agent is `Held`/`Canary`** (same spirit as the contract pre-hook). Recorded as design 02 §11 A4. Removal otherwise clean (pre-delete hooks verify no orphans).
- **Profiles**: `prod` (default; replicas, anti-affinity, PDBs) · `local` (single replica everything, `local-path` storage, NodePort/default gateway class, sandbox-fallback expected — the honest mode of NFR-8) · `ambient` (adds ztunnel-only overlay per ADR-0012) · IdP profile switch `idp: zitadel | keycloak | byo` (byo = discovery URL + pre-provisioned creds).
- **CRDs**: shipped in the chart's `crds/` (install) + a `plume upgrade crds` step for upgrades (Helm's CRD-upgrade gap handled explicitly, not silently).

## 4. Versioning & upgrade discipline

- Chart version = platform version (semver). Subchart bumps are renovate PRs that must pass the full matrix.
- **Contract compatibility gate (r1 f9)**: the chart writes a `plume-contracts` ConfigMap at install — the ledger of every shipped contract (`kgp/v1alpha1`, `idp/v1alpha1`, `receipt/v1`, `ontology/v1`, `pack/v1`, semconv SHA); the `helm upgrade` pre-hook and `plume doctor` both read it and block/flag a >1-version jump (N/N−1 made operational).
- Rollback: `helm rollback` restores the engine; CRs/GitOps state re-converge. **CRDs are never rolled back** (r1 f6): the co-versioning rule is that CRD schema changes are additive within N/N−1, so operator N−1 tolerates CRD N; the operator's version guard checks both directions.
- **The chart holds itself to the platform's supply-chain bar (r1 f7)**: published as a cosign-signed OCI chart, all images pinned by digest, SBOM attached — the same admission story agents get.

## 5. e2e CI (the NFR-3 enforcement)

Matrix on every merge (GitHub Actions):

| Axis | Values |
|---|---|
| Distro | **k3d**, **kind** (required) · **minikube + k3s** weekly (scheduled; k3s is doctrine-named — r1 f4) |
| Profile | local (both) · prod-shape on kind (replicas>1 where core allows) |
| Tier | core · core+plus (k3d only, time budget) |

Scenario per cell (the P1 demo, mechanized): install → deploy two sample agents from two SDK templates → A2A collaboration through the gateway → assert: receipts on the stream with correct principal chain; directory entries; SVIDs issued; `SandboxDowngraded` present (no gVisor in CI) and *only* that condition degraded; budget 429 on a driven overrun; teardown clean (no orphaned resources — namespace diff check). Plus the **budget-ledger check** — counting rule (r1 f5): sum of `spec.replicas` over rendered Deployments/StatefulSets at `prod` profile core tier, DaemonSets counted as 1 logical each, Jobs/CronJobs excluded; compared against the ledger table in `weight-budget.yaml` (the §17 table as data) — and the **stateful-allowlist check**, as separate fast jobs. Target wall-clock ≤ 20 min for the required cells (parallel).

## 6. Failure modes

| Failure | Behavior |
|---|---|
| Distro quirk (gateway class, storage class) | Profile knobs, not code branches; CI cell failure names the axis |
| Subchart upstream breaking change | Pinned versions; renovate PR fails matrix before merge |
| Partial install (hook failure) | Hooks idempotent; `helm upgrade` re-run converges; install status surfaced as a `plume doctor` check (design 08) |
| CRD upgrade skew | Explicit `plume upgrade crds` step; operator refuses to run against older CRD schema (version guard) |

## 7. Testing

The chart *is* tested by §5. Additionally: `helm template` golden snapshots per profile/tier (diff review on every chart PR), lint, kubeconform against pinned k8s versions (n, n-1, n-2).

## 8. Decisions for async review

- **D1 — The weight budget is CI-enforced** (rendered-pod count + stateful-dep grep fail PRs) — doctrine rule 5 as a merge gate, not a document.
- **D2 — Contract-version pre-hook makes N/N−1 operational** at upgrade time.
- **D3 — minikube is weekly, not per-merge** (k3d+kind cover the API surface; minikube exists to catch VM-driver quirks at lower cost). NFR-3 wording stays satisfied: the chart is identical; only CI cadence differs.

## 9. Resulting ADRs

Folded into ADR-0022 (P1 infrastructure) after critique PASS.
