# Design 07: Umbrella chart, profiles, e2e CI

- **Status**: **approved** — critique PASS at r2 (reviews/07-review.md) · ADR-0022
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
    agentgateway                 # pinned ≥ the OSS token-exchange release (design 06/research)
                                 # gated by dependencies[].condition: gateway.enabled (design 03 A8)
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

**Two chart mechanisms design 03 depends on, owed here (A1, 2026-08-26).** Design 03 §3.1/§5 and design 02 A15/A18 make `Ready`, `GovernanceSkipped` and the whole compile/don't-compile decision turn on them, and **neither exists** in `charts/plume/` today:

- **`gateway.enabled`** — the agentgateway subchart's `dependencies[].condition`, so the declared switch *is* the install and the two cannot diverge by typo. `Chart.yaml` has no `dependencies:` block yet, so **P1 ships `gateway.enabled: false` explicitly** rather than inheriting a core-tier default; design 03 §3.1's table then puts every P1 Agent in the declared-off row instead of the broken-install row. The operator needs the matching input.
- **`NOTES.txt`** — `charts/plume/templates/` has none. It is one of the two legs of `GovernanceSkipped`'s NFR-8 loudness claim (the other is the per-Agent condition), so it must state, when `gateway.enabled` is false, that agents are running **ungoverned**: no budgets, no authn, no tool filtering at the gateway.

Both were asserted in the present tense by design 03 before this amendment, which is rule 7 — a bound stated that nothing enforces. They are recorded here because this design owns the chart.

**A third, from design 02 A21 (2026-08-26)**: `charts/plume/` ships **no admission policy of any kind**, while design 02 §6 listed cosign verification among its enforced admission rules. Digest-pinning is now CEL on the Agent schema, but **signature verification is not CEL-expressible** and is decided by A2 below. Until it lands, no image signature is verified anywhere in this platform, and design 02 §5 says so.

## 4. Versioning & upgrade discipline

- Chart version = platform version (semver). Subchart bumps are renovate PRs that must pass the full matrix.
- **Contract compatibility gate (r1 f9)**: the chart writes a `plume-contracts` ConfigMap at install — the ledger of **every** shipped socket contract (`kgp/v1alpha1`, `idp/v1alpha1`, `receipt/v1`, `ontology/v1`, `pack/v1`, `gateway-filter/v1`, `template/v1`, `knowledge-pattern/v1`, `evalrunner/v1`, `evalreport/v1`, semconv SHA — designs 16/18 note); the `helm upgrade` pre-hook and `plume doctor` both read it and block/flag a >1-version jump (N/N−1 made operational).
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

## A2 (2026-08-28, from `reviews/03-codex-review-r4.md` MAJOR 17) — the verifier is Sigstore policy-controller, required not bundled, and its absence is loud

"Sigstore **or** Kyverno" is not a design. They are different controllers with different pods, lifecycles and outage behaviour, and a `verifyImages` binding without its engine verifies nothing.

- **Which**: **Sigstore policy-controller**, pinned. It is purpose-built for this one check; Kyverno is a general policy engine whose other capabilities plume does not need and whose pods it would still pay for. Doctrine rule 1 (bind, don't build) and rule 5 (the ≤8-pod core budget, CI-enforced) point the same way.
- **Bundled?** **No — an external prerequisite, verified by preflight.** The chart does **not** declare a Helm dependency: a Helm dependency *is* installation, and saying "declares the dependency" while also saying "does not install" was a contradiction (A3, corrected). What the chart ships is a **preflight check** — a compatible version range, the Ready webhook, the namespace opt-in and a matching enforce-mode policy — which fails install under a compliance profile and raises `ImageSignatureUnverified` at core.
- **The pods are not free, and the ledger says so.** An earlier draft called the cost zero. It is zero *to plume's* ≤8-pod core budget and not zero to the operator: policy-controller runs its own webhook and controller pods. **The compliance profile's documented weight includes them**, marked as required-but-not-owned, because a profile that is only cheap by moving pods outside the ledger is not cheap. And an organisation standardised on Kyverno **is** forced to run a second engine — that is a real cost of this choice, stated rather than elided; it is a doctrine trade (one purpose-built verifier over a general engine plume would not otherwise use), not a free lunch.
- **Tier**: optional at `core`, **mandatory under any compliance profile** (ADR-0014). A profile that turns PHI redaction on while leaving image provenance unverified is not a compliance posture.
- **Presence is not enforcement, and the check is on the latter (A3).** policy-controller enforces **per namespace**, only where the namespace opts in with `policy.sigstore.dev/include=true`, and only against a matching `ClusterImagePolicy` in enforce mode. An installed, healthy controller therefore proves nothing about the namespace an Agent's workload actually runs in. The **effective enforcement contract** is what is verified, every element of it: a pinned compatible version · a Ready webhook with `failurePolicy: Fail` · the workload namespace carrying the opt-in label · a matching `ClusterImagePolicy` in **enforce** mode naming the required signing identity/authority · a defined no-match behaviour.
- **Any element absent or unverifiable**: at core, every Agent carries **`ImageSignatureUnverified`** — the platform states plainly that digests are pinned and signatures are not checked, rather than implying provenance it does not have. Under a compliance profile, **install fails**. A profile that passes because a controller exists, while the workload namespace was never opted in, is worse than no check: it reports provenance verified when nothing verified it.
- **Verifier installed but unavailable**: its own `failurePolicy: Fail` denies workload admission, so the failure is toward denial. plume adds no second timeout on top; it surfaces the condition.
- **BYO scope**: the binding covers the namespaces plume creates workloads in. Images outside that scope are not plume's to vouch for, and this says so rather than implying platform-wide coverage.

**Owed**: the pinned version and the namespace-selector shape. The integration test must admit a **signed** fixture and reject an **unsigned** one **in the exact namespace an Agent workload uses** — testing it anywhere else would pass while the enforcement gap this amendment describes was live.

**This is a recommendation taken on doctrine, not a measured result** — it should be confirmed before design 07 is implemented, since it binds a third-party controller.- **A4 (2026-09-01)**: the operator Deployment sets `strategy: RollingUpdate` with **`maxSurge: 0`, `maxUnavailable: 1`** — the old pod must go away before the new one starts, and that ordering is required rather than preferred. `cmd/operator`'s `readyz` check waits on `mgr.Elected()`, so with the default rolling update Kubernetes creates the new pod first, it cannot become ready until it holds the leader lease, and the old pod holds that lease and cannot be terminated until the new pod is ready. **Every upgrade of this operator deadlocked** until the rollout timed out. Measured on a real cluster on 2026-09-01, and it had been invisible for a specific reason worth recording: `hack/e2e.sh` reused one image tag with `pullPolicy: Never`, so `helm upgrade` saw an unchanged pod template and never rolled the pod at all. The operator under test had been running for **nine days** while every e2e run reported green against it. The tag is now unique per run, and `TestTheOperatorUnderTestIsTheOneJustBuilt` compares the deployed image to the one the run built, so a stale pod can never again look like a passing suite.
