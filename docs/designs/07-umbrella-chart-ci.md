# Design 07: Umbrella chart, profiles, e2e CI

- **Status**: **approved** — critique PASS at r2 (reviews/07-review.md) · ADR-0022 · amendments A1–A6 below; A5 (2026-09-03), A6 (2026-09-09), A6.10 (2026-09-10), A6.11, A6.12 and A6.13 (2026-09-11) not yet critiqued
- **Phase**: P1 · **Size**: M · **Date**: 2026-08-20
- **ADRs**: 0002 (rules 5/6), 0012 (ambient profile), NFR-1/3/7/8 · interfaces: every P1 design (it packages them)

## 1. Purpose & scope

The single `helm install assayd` that stands up the core on anything from a laptop kind cluster to a prod fleet — plus the CI that makes NFR-3 ("runs anywhere") enforced rather than aspirational. In scope: chart structure, tiers, profiles, dependency handling, upgrade/versioning discipline, the e2e matrix. Out of scope: app-level packs (design 18 installs those).

## 2. Doctrine & charter gates

- **Plane**: slow (distribution of the engine). **Pods**: none of its own — it *is* the §17 budget's ledger: the chart renders exactly the budgeted set, and a CI check counts rendered pods against NFR-1 (a PR that adds a pod fails CI unless the budget doc changes in the same PR — the "written justification" gate made mechanical).
- **Stateful deps / disk**: CI checks rendered manifests against an **explicit versioned allowlist with reasons** (`stateful-allowlist.yaml`: `postgres`, `nats` — substrate (rule 2); `openobserve` — observability sink, not substrate; anything else fails). Adding an entry means touching the allowlist file in the same PR — the justify-in-PR property, made passable by the chart's own baseline (r1 f1). **SPIRE server's datastore points at the platform Postgres** (supported upstream) — one less PVC (r1 f1).
- **Charter line (r1 f8)**: slow-plane distribution; primitives Resource (rendered CRs/manifests) + Artifact (the chart itself as signed OCI); the chart *ships* sockets, defines none.

## 3. Chart structure

```
charts/assayd/                    # umbrella
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
- **CRDs**: shipped in the chart's `crds/` (install) + a `assayd upgrade crds` step for upgrades (Helm's CRD-upgrade gap handled explicitly, not silently).

**Two chart mechanisms design 03 depends on, owed here (A1, 2026-08-26).** Design 03 §3.1/§5 and design 02 A15/A18 make `Ready`, `GovernanceSkipped` and the whole compile/don't-compile decision turn on them, and **neither exists** in `charts/assayd/` today:

- **`gateway.enabled`** — the agentgateway subchart's `dependencies[].condition`, so the declared switch *is* the install and the two cannot diverge by typo. `Chart.yaml` has no `dependencies:` block yet, so **P1 ships `gateway.enabled: false` explicitly** rather than inheriting a core-tier default; design 03 §3.1's table then puts every P1 Agent in the declared-off row instead of the broken-install row. The operator needs the matching input.
- **`NOTES.txt`** — `charts/assayd/templates/` has none. It is one of the two legs of `GovernanceSkipped`'s NFR-8 loudness claim (the other is the per-Agent condition), so it must state, when `gateway.enabled` is false, that agents are running **ungoverned**: no budgets, no authn, no tool filtering at the gateway.

Both were asserted in the present tense by design 03 before this amendment, which is rule 7 — a bound stated that nothing enforces. They are recorded here because this design owns the chart.

**A third, from design 02 A21 (2026-08-26)**: `charts/assayd/` ships **no admission policy of any kind**, while design 02 §6 listed cosign verification among its enforced admission rules. Digest-pinning is now CEL on the Agent schema, but **signature verification is not CEL-expressible** and is decided by A2 below. Until it lands, no image signature is verified anywhere in this platform, and design 02 §5 says so.

## 4. Versioning & upgrade discipline

- Chart version = platform version (semver). Subchart bumps are renovate PRs that must pass the full matrix.
- **Contract compatibility gate (r1 f9)**: the chart writes a `assayd-contracts` ConfigMap at install — the ledger of **every** shipped socket contract (`kgp/v1alpha1`, `idp/v1alpha1`, `receipt/v1`, `ontology/v1`, `pack/v1`, `gateway-filter/v1`, `template/v1`, `knowledge-pattern/v1`, `evalrunner/v1`, `evalreport/v1`, semconv SHA — designs 16/18 note); the `helm upgrade` pre-hook and `assayd doctor` both read it and block/flag a >1-version jump (N/N−1 made operational).
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
| Partial install (hook failure) | Hooks idempotent; `helm upgrade` re-run converges; install status surfaced as a `assayd doctor` check (design 08) |
| CRD upgrade skew | Explicit `assayd upgrade crds` step; operator refuses to run against older CRD schema (version guard) |

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

- **Which**: **Sigstore policy-controller**, pinned. It is purpose-built for this one check; Kyverno is a general policy engine whose other capabilities assayd does not need and whose pods it would still pay for. Doctrine rule 1 (bind, don't build) and rule 5 (the ≤8-pod core budget, CI-enforced) point the same way.
- **Bundled?** **No — an external prerequisite, verified by preflight.** The chart does **not** declare a Helm dependency: a Helm dependency *is* installation, and saying "declares the dependency" while also saying "does not install" was a contradiction (A3, corrected). What the chart ships is a **preflight check** — a compatible version range, the Ready webhook, the namespace opt-in and a matching enforce-mode policy — which fails install under a compliance profile and raises `ImageSignatureUnverified` at core.
- **The pods are not free, and the ledger says so.** An earlier draft called the cost zero. It is zero *to assayd's* ≤8-pod core budget and not zero to the operator: policy-controller runs its own webhook and controller pods. **The compliance profile's documented weight includes them**, marked as required-but-not-owned, because a profile that is only cheap by moving pods outside the ledger is not cheap. And an organisation standardised on Kyverno **is** forced to run a second engine — that is a real cost of this choice, stated rather than elided; it is a doctrine trade (one purpose-built verifier over a general engine assayd would not otherwise use), not a free lunch.
- **Tier**: optional at `core`, **mandatory under any compliance profile** (ADR-0014). A profile that turns PHI redaction on while leaving image provenance unverified is not a compliance posture.
- **Presence is not enforcement, and the check is on the latter (A3).** policy-controller enforces **per namespace**, only where the namespace opts in with `policy.sigstore.dev/include=true`, and only against a matching `ClusterImagePolicy` in enforce mode. An installed, healthy controller therefore proves nothing about the namespace an Agent's workload actually runs in. The **effective enforcement contract** is what is verified, every element of it: a pinned compatible version · a Ready webhook with `failurePolicy: Fail` · the workload namespace carrying the opt-in label · a matching `ClusterImagePolicy` in **enforce** mode naming the required signing identity/authority · a defined no-match behaviour.
- **Any element absent or unverifiable**: at core, every Agent carries **`ImageSignatureUnverified`** — the platform states plainly that digests are pinned and signatures are not checked, rather than implying provenance it does not have. Under a compliance profile, **install fails**. A profile that passes because a controller exists, while the workload namespace was never opted in, is worse than no check: it reports provenance verified when nothing verified it.
- **Verifier installed but unavailable**: its own `failurePolicy: Fail` denies workload admission, so the failure is toward denial. assayd adds no second timeout on top; it surfaces the condition.
- **BYO scope**: the binding covers the namespaces assayd creates workloads in. Images outside that scope are not assayd's to vouch for, and this says so rather than implying platform-wide coverage.

**Owed**: the pinned version and the namespace-selector shape. The integration test must admit a **signed** fixture and reject an **unsigned** one **in the exact namespace an Agent workload uses** — testing it anywhere else would pass while the enforcement gap this amendment describes was live.

**This is a recommendation taken on doctrine, not a measured result** — it should be confirmed before design 07 is implemented, since it binds a third-party controller.

## A4 (2026-09-01) — the operator's rolling update must retire the old pod first

- The operator Deployment sets `strategy: RollingUpdate` with **`maxSurge: 0`, `maxUnavailable: 1`** — the old pod must go away before the new one starts, and that ordering is required rather than preferred. `cmd/operator`'s `readyz` check waits on `mgr.Elected()`, so with the default rolling update Kubernetes creates the new pod first, it cannot become ready until it holds the leader lease, and the old pod holds that lease and cannot be terminated until the new pod is ready. **Every upgrade of this operator deadlocked** until the rollout timed out. Measured on a real cluster on 2026-09-01, and it had been invisible for a specific reason worth recording: `hack/e2e.sh` reused one image tag with `pullPolicy: Never`, so `helm upgrade` saw an unchanged pod template and never rolled the pod at all. The operator under test had been running for **nine days** while every e2e run reported green against it. The tag is now unique per run, and `TestTheOperatorUnderTestIsTheOneJustBuilt` compares the deployed image to the one the run built, so a stale pod can never again look like a passing suite.

## A5 (2026-09-03, from design 02 A42/A44/A59/A60, design 03 A44, Codex r8 BLOCKERs 7–8) — what the chart ships for the operator-owned run namespace, and what it cannot

Design 02 A42 moves every Agent's workload, Service, routes and revision material out of the Agent's namespace into `assayd-run-<agent-namespace>`, a namespace the operator creates. Five things about that move were owed to this design because this design owns the chart. Each is answered below, and each answer states what is rendered **today** — which for most of them is nothing, because the subcharts that would carry the object do not exist yet (A1). Rule 7 of `AGENTS.md`: a bound the chart does not render is not a bound.

### A5.1 The chart renders nothing per run namespace — the operator does

A Helm chart renders at install time; run namespaces are created at reconcile time, one per namespace that ever holds an Agent. So **no object that must exist per run namespace can be a chart template**: not the NetworkPolicy, not the Pod Security labels, not a ResourceQuota. The operator materializes all of them (design 02 §3.2, A60), and the chart's part is to supply the **shape** through values and to grant the **RBAC** the operator needs to do so. Every "the chart ships X into the run namespace" sentence in earlier text is therefore wrong in the same way, and this amendment is where that is corrected.

### A5.2 `ClusterSPIFFEID` — the template, the selectors, and why the selector is the security boundary

When the SPIRE subchart lands, `templates/` renders exactly one platform-owned `ClusterSPIFFEID` (design 02 §3.5):

```yaml
apiVersion: spire.spiffe.io/v1alpha1
kind: ClusterSPIFFEID
metadata: {name: assayd-agent}
spec:
  spiffeIDTemplate: "spiffe://{{ .TrustDomain }}/agent/{{ index .PodMeta.Labels \"assayd.dev/agent-namespace\" }}/{{ index .PodMeta.Labels \"assayd.dev/agent\" }}"
  namespaceSelector:
    matchLabels: {assayd.dev/pods-by: agent-operator}
    matchExpressions:
      - {key: assayd.dev/tenant-sync, operator: DoesNotExist}
  podSelector:
    matchExpressions:
      - {key: assayd.dev/agent-namespace, operator: Exists}
      - {key: assayd.dev/agent, operator: Exists}
```

Three facts, verified against spire-controller-manager v0.7.0 and recorded in `docs/research/tenant-namespace-primitives-2026-09.md`:

- **`index` is mandatory.** The template engine is Go `text/template` with no functions added; `{{ .PodMeta.Labels "key" }}` does not parse and the webhook rejects it. Design 02 A59 wrote that form and A60 corrects it; this is the rendered one.
- **An absent label yields an empty segment, and an empty segment yields no SVID.** `index` returns `""` on a missing key; go-spiffe rejects `…/agent//x`; the controller logs a render failure, counts it in `status.stats.podEntryRenderFailures`, and creates no entry. That is fail-closed, but only because each label is a **whole** segment — so the `podSelector` above additionally requires both labels to exist, and any future edit that prefixes a segment with a literal must keep that invariant.
- **The `namespaceSelector` is the boundary, and it is a different label from the listener's.** The namespace segment is a label the Pod's creator chooses, so *anyone who can create a Pod in a selected namespace can claim any agent's identity in any tenant*. The selector therefore admits only namespaces labelled `assayd.dev/pods-by: agent-operator` — a label that certifies one property, that nothing but the operator creates Pods there — and **not** `assayd.dev/run-namespace`, which the Gateway listener selects on (A5.3) and which design 26 A1 also stamps on a vCluster's host sync namespace, where every Pod is created by vCluster on behalf of a tenant who chooses its labels. Reusing the listener label there would have let a hard-mode tenant Pod labelled `agent-namespace: team-a` be issued a soft-mode tenant's identity in the host trust domain; the critique caught it. The `tenant-sync` `DoesNotExist` clause is the belt: the tenant-operator stamps `assayd.dev/tenant-sync` on every sync namespace, so even a mislabelled one is excluded. **Neither label is a proof by itself** — a label is writable by whoever can create or update the namespace — so both are reserved to the operator identities by the admission policy in A5.9, and the operator refuses to create run namespaces while that policy is absent. The cross-family review was right that a split without a reservation fixes selector conflation and not provenance. The chart renders **no Role or RoleBinding granting Pod `create` in a `pods-by: agent-operator` namespace**, and the read-only RoleBinding design 02 §3.2 owes for `kubectl logs` must never carry a write verb — pinned by a chart test that fails on any verb outside `get`/`list`/`watch`, owed with the RoleBinding. Widening the selector — to `All`, or to tenant namespaces — silently turns every tenant's Pod-create grant into cross-tenant impersonation. The per-namespace hardening design 02 §3.5 records (one `ClusterSPIFFEID` per run namespace, namespace as a literal) is the answer if that boundary ever has to move.

**Today**: the chart carries no SPIRE subchart and renders no `ClusterSPIFFEID`; no Agent has an SVID; design 02 §5's `IdentityIssued=False` row is the state of every Agent on every install. The object above is the one the subchart must render, in the future tense, and the e2e that proves an Agent in `team-a` gets `…/agent/team-a/<name>` from a Pod in `assayd-run-team-a` is owed with it.

### A5.3 The Gateway listener admits run namespaces by label

When the agentgateway subchart lands (`gateway.enabled: true`), the chart's `Gateway` carries, on every listener that serves Agent routes:

```yaml
allowedRoutes:
  namespaces:
    from: Selector
    selector:
      matchLabels: {assayd.dev/run-namespace: "true"}
```

Gateway API v1.6.1: `from` is one of `All | Selector | Same` (default `Same`), and `selector` is required with `Selector`. A route in a namespace the listener does not admit is rejected, which is how design 03 A44 found that leaving the default would wedge every moved route. A route's `parentRef` to a Gateway in the chart's namespace **needs no `ReferenceGrant`** — the specification exempts Gateway–route attachment; only `backendRef`s and Secret references need one — so this selector is the whole of the cross-namespace consent the move needs. The gateway-scoped tracing policy stays in the Gateway's own namespace (design 03 §3.2).

An admitted namespace is not a route-authoring grant: A5.9's second policy admits an `HTTPRoute` whose `parentRefs` name the assayd Gateway only from the operator's identity, so a principal who somehow held rights in a run namespace still could not attach an ungoverned route.

**Today**: `gateway.enabled` is `false` and there is no Gateway (A1). Nothing is rendered.

### A5.4 The default-deny NetworkPolicy — operator-materialized, and absent at P1 on purpose

Design 02 §6 requires every Agent Pod to run under a default-deny NetworkPolicy whose only egress is the gateway. The chart has never rendered one, in any namespace; design 02 A42 named that gap and this amendment decides how it closes:

- **Who**: the operator, into each run namespace, at creation and on every reconcile (A5.1 says why it cannot be the chart).
- **Shape**: a **traffic matrix**, not a slogan, because "egress to the gateway only" as first written blocked two paths assayd itself requires (the cross-family review's finding). Ingress: the gateway's Pods, **and the operator's Pods** — the operator fetches the candidate's Agent Card in-cluster before registration (design 02 §3.4) and is not a gateway Pod; the eval runner's candidate-only route (design 16) arrives through the gateway and needs no rule of its own. Egress: the gateway's Pods; DNS; **and the tenant's NATS endpoint**, because the reference SDK's shared A2A task store (design 09) connects to JetStream directly through `ASSAYD_NATS_URL`, and agentgateway does not proxy NATS. Each peer is a `namespaceSelector` plus `podSelector` from values — a `NetworkPolicyPeer` can name Pods, namespaces or an `ipBlock`, and **never a Service**, so "the cluster DNS Service" is not a shape the API accepts; DNS is the `kube-dns` Pods in `kube-system` on TCP and UDP 53, with the label values supplied per distribution and validated at render. A policy that forgets DNS is a policy that blocks everything and looks like "the gateway is down"; one that forgets the operator makes every candidate fail registration and looks like a card bug.
- **When**: only with `gateway.enabled: true`. With the gateway off, "egress to the gateway only" is **no egress**, and every P1 Agent would lose the direct provider access the declared-ungoverned tier promises it. So at P1 **no NetworkPolicy is applied anywhere**, `GovernanceSkipped=GatewayDisabled` is on every Agent (design 03 §3.1), and the `NOTES.txt` A1 owes states it alongside "no budgets, no authn, no tool filtering".
- **Enforcement is the CNI's, not Kubernetes'.** A NetworkPolicy on a cluster whose CNI does not implement the API is accepted and does nothing. k3s enables a network-policy controller by default; kind's default CNI does not. The e2e matrix must therefore assert enforcement on k3d and must **not** claim it on kind, and a run that cannot observe a blocked connection reports the axis as unverified rather than green. The k3d cell proves the two required paths **with enforcement on** — a card registration and a shared-task round trip — and a negative control to a forbidden destination; deleting either allow rule must fail it.

**Today**: nothing is rendered or materialized. The control is decorative in every namespace, exactly as design 02 A42 said, and remains so until the gateway ships.

### A5.5 Pod Security, ResourceQuota and LimitRange are mirrored by the operator

The chart's own namespace runs `pod-security.kubernetes.io/enforce: restricted` (`templates/namespace.yaml`) and that does not follow a Pod into a run namespace. Design 02 A44/A60 make the operator mirror the source namespace's six Pod Security labels and every `ResourceQuota` and `LimitRange`. The chart's part is RBAC (A5.7). The rendered Agent Pod satisfies `restricted` **by construction** — the operator renders the whole security context itself (non-root, `RuntimeDefault` seccomp, no privilege escalation, all capabilities dropped, no volumes) and the Agent spec exposes none of it — so a tenant enforcing `restricted` gets Pods that admit, and a Pod Security rejection is reachable only through a future change: a new spec field, or a `-version` pin older than a field the operator uses. If it happens, **the operator does not see it**: the rejection lands on the ReplicaSet controller's Pod create, the Deployment is accepted, no Pod appears, and the reason is a `FailedCreate` event on the ReplicaSet. The Agent reports only `Ready=False, WorkloadNotAvailable`. That is an NFR-8 gap, stated: the operator does not watch ReplicaSet events today, and the fix — surfacing `FailedCreate` as a condition — is owed to design 02 §5.

### A5.6 Sigstore policy-controller opts in the run namespace, not the Agent's

A2/A3 say the verifier enforces per namespace, only where `policy.sigstore.dev/include=true` is set, and that the effective-enforcement check must run "in the exact namespace an Agent workload uses". Under A42 that namespace is the **run namespace**. So under a compliance profile the operator stamps the opt-in label on every run namespace it creates, and the preflight in A3 checks run namespaces — a check that passed on `team-a` while Pods ran unverified in `assayd-run-team-a` would be the exact false positive A3 warns about. The chart's part is the profile flag that tells the operator to stamp it.

**Today**: no verifier is bound (A2) and the operator stamps nothing.

### A5.7 RBAC — the escalations, named

The operator's `ClusterRole` (`files/operator-rules.yaml`, generated from the reconciler's markers and held equal by `TestChartRBACMatchesGeneratedRules`) grows by the following, and each is a real widening to say out loud rather than bury in a generated file:

| Grant | Why | What bounds it |
|---|---|---|
| `namespaces`: `get`, `list`, `watch`, `create`, `update`, `delete` | create and label run namespaces; **delete** them when their last Agent goes | RBAC cannot restrict a dynamic name, so all three write verbs are cluster-wide. The code writes — labels, annotations, or deletion — only to a namespace whose name has the `assayd-run-` shape **and** whose UID matches the operator's binding record (design 02 A60); everything else is refused and logged. Unbounded `update` would otherwise let one bug relabel a tenant namespace as SPIFFE-selected |
| `pods/log`: `get` | `assayd logs` streams a run-namespace Pod's logs under the operator's credentials after a `SubjectAccessReview` for the caller (design 02 §3.2, *Read access*) | **not granted now**; lands with design 08's `assayd logs` |
| `validatingadmissionpolicies`, `validatingadmissionpolicybindings` (`admissionregistration.k8s.io`): `get` | the operator checks that A5.9's policies exist before creating a run namespace and fail-closes if not | `get` only, through the uncached API reader — no informer on admission kinds is ever started |
| `configmaps`, `secrets`: `+update` | the binding record is compare-and-swapped, and A57's metadata repair already needed it — `repairMetadata` calls `Update` and the shipped role grants no `update` on either kind, so restamping a copy's labels is Forbidden on a real cluster while green in envtest, which runs as admin. Inferred from the role, and to be confirmed with `kubectl auth can-i` against the deployed chart when A42's e2e runs | the operator never updates a user's object; every `Update` is on a name of the operator's own shape |
| `resourcequotas`, `limitranges`: `get`, `list`, `watch`, `create`, `update`, `delete` | mirroring (A5.5) | mirrors are named `assayd-mirror-<name>` — truncate-and-hashed past 253 characters by design 03 §3.2's rule — in run namespaces only; the name is the deletion authority |
| `networkpolicies` (`networking.k8s.io`): `get`, `list`, `watch`, `create`, `update`, `patch`, `delete` | A5.4, when the gateway ships | not granted until the operator materializes one; a verb granted ahead of the code that uses it is rule 7 in RBAC form |

The `ClusterRole` today grants none of these. They land with the A42 implementation and the generated file, not before.

### A5.9 The admission policies that make the labels the operator's (ADR-0029)

The binding record (design 02 A60) proves which namespace the operator created, and none of the three things that act on run-namespace labels reads it: SPIRE selects on `assayd.dev/pods-by`, the Gateway admits on `assayd.dev/run-namespace`, the operator's Namespace reconciler keys on both. A label is writable by whoever can create or update the namespace. So the chart reserves them. Two `ValidatingAdmissionPolicy` objects and their bindings, cluster-scoped and static, so the chart can render them today; `ValidatingAdmissionPolicy` is GA from Kubernetes 1.30, which is `Chart.yaml`'s floor:

```yaml
apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata: {name: assayd-namespace-labels}
spec:
  failurePolicy: Fail
  matchConstraints:
    resourceRules:
      - {apiGroups: [""], apiVersions: [v1], operations: [CREATE, UPDATE],
         resources: [namespaces, namespaces/status, namespaces/finalize]}   # both subresources write metadata too
  variables:
    - name: reserved
      expression: "['assayd.dev/pods-by', 'assayd.dev/run-namespace', 'assayd.dev/tenant-sync', 'assayd.dev/agent-namespace', 'assayd.dev/owned-by']"
    - name: touched
      expression: >-
        variables.reserved.exists(k,
          (has(object.metadata.labels) && k in object.metadata.labels ? object.metadata.labels[k] : '') !=
          (oldObject != null && has(oldObject.metadata.labels) && k in oldObject.metadata.labels ? oldObject.metadata.labels[k] : ''))
        || ((has(object.metadata.annotations) && 'assayd.dev/binding-nonce' in object.metadata.annotations ? object.metadata.annotations['assayd.dev/binding-nonce'] : '') !=
            (oldObject != null && has(oldObject.metadata.annotations) && 'assayd.dev/binding-nonce' in oldObject.metadata.annotations ? oldObject.metadata.annotations['assayd.dev/binding-nonce'] : ''))
  validations:
    - expression: "!variables.touched || request.userInfo.username in ['system:serviceaccount:assayd-system:assayd-agent-operator']"
      message: "assayd.dev namespace labels and the binding nonce are reserved to the assayd operators"
```

The permitted identities are rendered **into** the expression from values — the operator's own ServiceAccount always, plus `admission.extraOperators`, which is where the tenant-operator goes when the enterprise module is installed (the only hard-mode writer of `run-namespace` and `tenant-sync` on a sync namespace, design 26 A1). Not a params ConfigMap: the first draft read them from one with `parameterNotFoundAction: Deny`, and the code review showed that deleting it — `kubectl delete ns assayd-system` with the policies still bound — denied every namespace create and update in the cluster. The second policy matches `HTTPRoute` creates and updates and denies any whose `parentRefs` name the assayd Gateway unless the requester is a permitted identity, so a namespace the listener admits is not a grant to author routes into it; a `parentRef` without a namespace refers to the **route's** namespace (Gateway API), so the expression resolves it as `request.namespace` rather than skipping it.

**The operator fail-closes on their absence, by name.** At startup it logs their absence, and on every reconcile it checks that both policies and bindings exist; if not, `RunNamespaceUnavailable=LabelAuthorityAbsent` on the Agent and no namespace is created, clearing when they appear. Presence is what is checked — a policy of the right name that validated nothing would pass — so the content is this chart's responsibility and the chart test pins it. `failurePolicy: Fail` means an unavailable admission chain denies the write rather than admitting it.

**Today**: the policies render (`templates/admission.yaml`), the operator fail-closes without them, and the e2e proves a cluster-admin identity outside the list cannot create a `pods-by`-labelled namespace. The chart test refuses to ship without them, without the subresources, or with a params object.

### A5.8 Weight budget and stateful allowlist

Unchanged. Nothing here adds a pod or a disk; the run namespace holds the same Agent Pods that ran in the tenant namespace before.

**Owed**: the SPIRE and agentgateway subcharts that would render A5.2 and A5.3; `NOTES.txt` (A1) gaining the NetworkPolicy sentence; the e2e cell that proves NetworkPolicy enforcement on k3d and reports it unverified on kind; A5.9's policies and their negative e2e, with the A42 implementation. **(Two of these are discharged — see A6.5 and A6.6. The list is left as written, because an amendment is a record; A6 carries what changed.)**

## A6 (2026-09-09) — the gateway path was authored and executed, and three things in A5 were wrong

ADR-0030 step 3 says: *first author and execute the exact resources; then encode that mapping.* This amendment is the first half. `hack/e2e.sh` now installs Gateway API v1.6.0 and agentgateway 1.5.0 on k3d, creates the Gateway, and `TestAnAgentAnswersThroughTheGateway` sends a request that an agent answers **through it**. Everything below was measured on that cluster, not derived.

### A6.1 The gateway RBAC has landed, ahead of the compiler and by one kind

A5.7's table lists no gateway kind, and the sentence under it — "The `ClusterRole` today grants none of these. They land with the A42 implementation and the generated file, not before" — **is now false for four of its six rows.** `namespaces`, `validatingadmissionpolicies`/`validatingadmissionpolicybindings`, `configmaps`/`secrets` `+update`, and `resourcequotas`/`limitranges` all shipped with A42. Only `pods/log` and `networkpolicies` are still ungranted, and each is still correctly so. Read that sentence as scoped to those two.

The table gains one row:

| Grant | Why | What bounds it |
|---|---|---|
| `httproutes` (`gateway.networking.k8s.io`): `get`, `list`, `watch`, `create`, `update`, `patch`, `delete`; `httproutes/status`: `get` | a route in the run namespace is what puts an agent behind the Gateway | routes are authored into `assayd-run-` namespaces only, and A5.9's policy independently refuses any author but the operator. `AgentgatewayBackend` and `AgentgatewayPolicy` are **not** granted: they land with the compiler that emits them, and a verb granted ahead of its code is rule 7 in RBAC form |

**This gap was invisible from either side.** A5.9's policy names the operator's ServiceAccount and therefore *permitted* it to author routes; the API server then refused the same request for want of RBAC. Neither half was wrong on its own and the path did not work — the admission reservation and the RBAC grant had simply never both existed at once. It surfaced only because a test finally sent traffic.

### A6.2 The reservation compared against the wrong namespace, and failed open

`assayd-gateway-routes` decided whether a route targets the assayd Gateway by comparing the `parentRef`'s namespace to **`.Values.namespace`** — the namespace the *operator* runs in. The Gateway's namespace is a different thing, and design 03 §3.2 already said so: gateway-scoped resources live in "the gateway's own namespace".

Moving the Gateway out of `assayd-system` made `targetsAssayd` false, the policy matched nothing, and an ordinary test identity attached a route to the Gateway. **The failure is silent and in the permissive direction** — no error, no event; the route is simply accepted, and a principal who can author one publishes an ungoverned path to a workload, which is the whole thing A5.9 exists to prevent.

The chart gains `gateway.namespace`, defaulting to **`.Values.namespace`** — the namespace the operator runs in, `assayd-system` by default — so a single-namespace install is unchanged, and the CEL compares against that. It is **not** the Helm release namespace: `hack/e2e.sh` installs the release into `default` while the operator runs in `assayd-system`, and an earlier version of this sentence called it "the release namespace", which is the very conflation this amendment exists to correct. `hack/e2e.sh` sets it on **every** distro, including those that install no Gateway, so the rendered policy always names the namespace a Gateway would occupy rather than falling back to the operator's.

### A6.3 agentgateway's proxy cannot run in the namespace this chart hardens

`charts/assayd/templates/namespace.yaml` enforces PodSecurity `restricted` on `assayd-system`. agentgateway v1.5.0's generated proxy pod sets no `seccompProfile`, so with the Gateway in `assayd-system` the data-plane Deployment was created and its ReplicaSet could never produce a pod:

```
violates PodSecurity "restricted:latest": seccompProfile (pod or container
"agentgateway" must set securityContext.seccompProfile.type to "RuntimeDefault" or "Localhost")
```

It is the **only** field it fails on, and assayd cannot supply it by configuring the vendor: `AgentgatewayParameters` v1alpha1's `.spec.deployment` overrides `metadata` — labels and annotations — and there is no pod `securityContext` anywhere in that CRD's schema. So the Gateway gets its own namespace at `baseline`, which the proxy does satisfy, and `assayd-system` stays `restricted`. This is design 03 §3.2's shape rather than a workaround for it, but the constraint is real and belongs in the record: **the chart cannot host the agentgateway data plane in `assayd-system` at any PSA level it is willing to run there.**

### A6.4 `Programmed=True` is not "the gateway can serve"

The harness waited on the Gateway's `Programmed` condition and got `True` — with a `LoadBalancer` Service whose EndpointSlice was empty, because the pod above never existed. The test then failed on a connection refused the harness had already reported as healthy. It now waits on the data-plane Deployment's rollout as well. Any future readiness check on a Gateway should do the same: `Programmed` describes the control plane's acceptance of the object, not the existence of anything serving it.

### A6.5 What A5.9 owed, and what is still owed

The negative e2e A5.9 owed now exists, in both halves: `TestNamespaceLabelsAreReservedToTheOperator` for the label policy, and `TestAnAgentAnswersThroughTheGateway`'s negative control for the route policy — which asserts the refusal names `assayd-gateway-routes`, because "it was refused" is not evidence about which rule refused it. That control is not decorative: it is what caught A6.2.

**Still owed, and not narrowed by this amendment**: the chart ships no Gateway (`gateway.enabled` defaults to `false` and is read by nothing), so the Gateway in the e2e is created by the harness. The **operator does not emit routes** — the test authors one while impersonating the operator's ServiceAccount, which proves the identity reservation and the RBAC grant and proves nothing about a compiler that does not exist. `AgentgatewayBackend`, `AgentgatewayPolicy` and the SPIRE subchart all still stand where A5 left them. (`NOTES.txt` and the NetworkPolicy enforcement cell no longer do — A6.9 and A6.6.)

### A6.6 The network-rules leg: A5.4's ingress half, executed by hand

Governance at the gateway is theatre while the gateway is bypassable, and until now this repository proved the opposite of what it wanted: `TestAnAgentAnswersARequestThroughItsRevisionService` reaches an agent from an ordinary namespace with no gateway involved. `TestTheGatewayIsTheOnlyWayIn` closes that, and is the first time this project has measured **ADR-0030's stop criterion for the slice — a disallowed principal failing against a permitted control.**

**This does not implement A5.4, and A5.4's "Today: nothing is rendered or materialized" still stands.** The policy is authored by the test, held for the length of one test, and deleted on the way out; the operator materializes nothing, and the `ClusterRole` still grants no `networkpolicies` — rule 7 held, no verb ahead of its code. Only the **ingress** half is exercised. The egress matrix (gateway, DNS, the tenant's NATS endpoint) is untouched, because no agent in this suite talks to JetStream and a rule nothing crosses is a rule nothing tests.

**A5.4's CNI claim is now measured rather than cited.** On k3d/k3s v1.33.6: a cross-namespace request went `200` → `000` under a deny-all ingress policy, and with one namespace allow-listed that namespace got `200` while another got `000` at the same moment. `hack/e2e.sh` exports `ASSAYD_E2E_NETPOL_ENFORCED` on the k3d lane only; elsewhere the test **skips with the axis reported unverified**, which is A5.4's own instruction and the only honest result on a CNI that accepts the policy and ignores it.

**A5.4 asks for a negative control and does not say how to build one that works.** This is the gap worth adding, because the obvious construction is wrong. k3s programs a NetworkPolicy's allow rules from the **source Pod's address**, so a Pod that connects the instant it starts can be refused before its own rule exists. In the spike behind this test an allow-listed namespace was refused three times running — including under `namespaceSelector: {}`, which permits everything — and was served immediately from a Pod that had been up ten seconds. `httpInCluster` curls on start and hides this behind retries, which is correct for a positive assertion and **silently fatal for a negative one**: a denial measured that way proves the race, not the policy. `probeSettled` therefore waits before connecting. A negative network control written without that settle is the network-layer form of "it was refused" not being evidence about which rule refused.

**A5.4's mutation requirement — "deleting either allow rule must fail it" — was run, not assumed.** Dropping the gateway peer failed the permitted path, with agentgateway itself reporting `upstream call failed: Connect: Connection refused`, so the denial was at the agent rather than the route. Dropping the operator peer failed card registration. Not applying the policy at all returned `code=200` on the direct probe, which is what protects the negative control from passing for the wrong reason. All three killed.

One observation for design 02 and design 10 rather than for here: with the operator peer removed, the Agent reached `phase: Ready` carrying a card entry whose `Digest` was **empty** — a recorded failed attempt, not a registration. A5.4 predicted the shape of this ("looks like a card bug"); a test that broke on the presence of any card entry would have passed straight through it, and this one nearly did.

### A6.7 The identity leg: the gateway refuses a principal

`TestTheGatewayRefusesADisallowedPrincipal` completes ADR-0030 step 3's hand-authored path. The network-rules leg proved the gateway cannot be bypassed, which is a precondition for governance rather than governance itself — with only that policy in place, whatever reaches the gateway is served. This is the other half: a caller presenting a **valid** credential that authenticates it as the wrong principal is refused **by the gateway, on the strength of who it is**, while a permitted principal is served on the same route seconds later.

The mechanism is agentgateway's own, hand-authored: `AgentgatewayPolicy` with `traffic.apiKeyAuthentication` reading a ConfigMap of `sha256:` key hashes with arbitrary `metadata`, and `traffic.authorization` carrying one CEL rule, `apiKey.group == "trusted"`. The policy lives in the run namespace because `targetRefs` "must be in the same namespace as the policy" — which is where design 03 §3.2 already puts it, **for the same reason and not a different one**: its table gives "a policy attaches to a route; they cannot be split across namespaces" (`03:161`). The `ReferenceGrant` argument belongs to the `HTTPRoute` row above it, not to this one; an earlier version of this paragraph attached it here and called design 03's reason unrelated when it is the same constraint measured from the other side.

**Nothing in assayd emits any of this, before or after.** No compiler exists, design 03 stays RE-OPENED, and no `agentgatewaypolicies` RBAC is granted. What has changed is that the mapping a compiler will have to produce is now known to be one the gateway honours.

Measured, and each distinction is load-bearing:

| Caller | Result |
|---|---|
| permitted principal (`group: trusted`) | `200`, and the body is the agent's own answer |
| **disallowed principal** (`group: rogue`) — a valid credential | **`403`** |
| no credential | `401` |
| unknown credential | `401` |

**`401` and `403` are different claims and the compiler must not conflate them**, exactly as A6.4's note on `503` versus `429` at the gateway. `401` is "I do not know you"; `403` is "I know you and no". Two mutations establish that the split is real rather than incidental: flipping the rule to permit `rogue` **inverted both outcomes**, so the rule is what decides; and removing `traffic.authorization` while keeping authentication left the anonymous caller at `401` and gave the disallowed principal **`200`**, so the `403` is authorization and not a side effect of authenticating.

Two operational notes for whoever writes the compiler. `Accepted=True` on an `AgentgatewayPolicy` is not enforcement — the policy reaches `Accepted` **and** `Attached` before the proxy applies it, so the test waits for an anonymous request to actually be refused; this is the same shape as A6.4's `Programmed=True` on a Gateway with no endpoints, and it is the second time in this amendment that a control-plane condition described configuration rather than behaviour. And `Attached` must be asserted alongside `Accepted`: a policy whose `targetRefs` name nothing is still valid, enforces nothing, and would leave every refusal below it reading as a success.

### A6.8 The MCP leg: a tool call through the gateway, and an allowlist that filters

`TestAnAgentCallsAnMCPToolThroughTheGateway` closes ADR-0030's last unmet clause. It could not be written before because there was no MCP server anywhere in the repository to call; `test/mcpserver` is one — Streamable HTTP, two tools, built and pushed by `hack/e2e.sh` beside the responder.

**Where the tool server lives, and why not in a run namespace.** A tool is not an agent. It sits in its own namespace behind a **second Gateway listener** (`tools`, port 8081) whose `allowedRoutes` selects on `kubernetes.io/metadata.name` rather than on an `assayd.dev/*` label — those keys are reserved to the operator by A5.9, and a test that minted one would be forging the authority that policy exists to hold. The route is still authored by the operator identity: `assayd-gateway-routes` reserves every route naming this Gateway, and a second listener is not a way around it.

**The allowlist is design 03's `toolAllowlist`, and it is stronger than the design describes.** `AgentgatewayPolicy.backend.mcp.authorization` with `mcp.tool.name == "echo_text"` does not merely refuse the call — **it removes the tool from `tools/list`**. An agent behind the allowlist never learns the other tool exists. Measured both ways: with the rule flipped to the other tool, the listing and both call outcomes inverted.

**A diagnosability trap worth naming before the compiler exists.** A refused tool comes back as JSON-RPC `-32602`, message **`Unknown tool: delete_everything`** — a forbidden tool is indistinguishable from one that was never there. That is right for non-disclosure and wrong for the operator asking why their agent cannot call a tool they can see in the server's own listing. Whatever design 03's compiler emits, `assayd doctor` (design 08) and the conditions in design 10 are the only places that difference can be recovered, because the gateway will not surface it.

**One undocumented requirement cost a debugging cycle and is recorded so it does not cost another.** An MCP target selected by `selector.services` requires the Service port to carry **`appProtocol: agentgateway.dev/mcp`**. Without it the `AgentgatewayBackend` reaches `Accepted=True`, the route resolves `ResolvedRefs=True`, and every request returns `503 mcp: no backends configured` — a backend that looks healthy from every status condition and has no targets. It is not in the CRD schema's descriptions; it is in agentgateway's own docs.

**What this does NOT complete.** ADR-0030's clause is "one agent completes one **A2A task** and one MCP tool call through the gateway". The MCP half is now literal — a real protocol exchange, `initialize`, `tools/list` and `tools/call`, against a server that speaks it. **The A2A half is not.** `test/responder` serves a conformant A2A **card** but answers on `/assayd-test/echo`, which its own source says plainly is not an A2A method: A2A v1.0 defines three bindings and a PascalCase method set, and no binding has that path. So an agent completes a *request* through the gateway, not an *A2A task*, and that remains owed — with the client that will call it, which is **design 08's**: `assayd invoke <agent>` is named there as an "A2A client for testing" (`08:22`) and design 08's own primitives line claims it (`08:14`). Not design 09, which owns the A2A *server* templates, and not design 03; `test/responder`'s source said "design 03's route" and was also wrong. The fixture is not the place to grow a method set.


### A6.9 `NOTES.txt`, owed since A1, and the first thing that reads `gateway.enabled`

A1 named `NOTES.txt` as **one of the two legs of `GovernanceSkipped`'s NFR-8 loudness claim** — the other being the per-Agent condition — and design 03 names it twice more, at §3.1 and §5, as the loud half of the declared-ungoverned tier. It did not exist. So for the whole of P1 the loudness claim rested on one leg, and the chart's own output said nothing about the fact that an agent installed from it is governed by nothing.

`charts/assayd/templates/NOTES.txt` now renders both branches. With `gateway.enabled: false` it states, in the terms A1 and A5.4 require, that agents run ungoverned — no budgets or rate limits, no gateway authn, no tool filtering, **and no NetworkPolicy in any namespace**, which is A5.4's sentence and the one that matters most, because it says the bypass A6.6 measured is open by default. It names the condition to look at and the command to read it. With `gateway.enabled: true` it says the CRDs must be installed separately and that **the policy compiler still does not exist**, so setting the value enforces nothing.

**It is also the first thing in this chart that reads `gateway.enabled` at all.** A6.5 recorded that the value "defaults to `false` and is read by nothing"; a value nothing consumes is a promise with no enforcement, which is the shape rule 8 exists to catch, and it sat that way from A9 (2026-08-26) until now. One consumer is not the gateway wiring — the subchart is still owed — but the flag now changes something observable rather than nothing.

### A6.10 (2026-09-10) — the mapping is encoded, for one resource, and design 03 is still not approved

ADR-0030 step 3 says: *first author and execute the exact resources; then encode that mapping.* A6 was the first half. This is the second, for exactly one resource.

**The operator now emits the revision's serving `HTTPRoute`.** `TestAnAgentAnswersThroughTheGateway` no longer authors a route: it creates an Agent, waits for the object the operator wrote, and sends its request through the Gateway's own Service to the hostname the operator chose. `TestTheOperatorRecreatesARouteThatWasDeleted` deletes that object and asserts a NEW one (different UID) comes back and still carries traffic — a transition, not a value, because a route that merely exists beside a gateway-enabled operator is not evidence the operator made it; this suite hand-authored one for weeks.

**What is emitted, precisely.** One `HTTPRoute` per Agent, named `<agent>-serving` under design 03 §3.2's `<name>-<concern>[-<rev>]` grammar and its 63-character truncate-plus-16-hex rule, in `assayd-run-<agent-namespace>` beside the Service; `parentRef` = the configured Gateway on listener `http`; `hostname` = `<agent>.<agent-namespace>.<suffix>`; one `backendRef` to the ACTIVE revision's Service at weight 100; labels `assayd.dev/agent-uid` and `assayd.dev/revision` plus the Agent's name and namespace; **no ownerReference**, because the Agent is in another namespace and a cross-namespace owner is treated as absent. Deletion is the operator's: a label-and-name sweep every reconcile, and the finalizer revokes the route **before** the workload, which is §3.7's reverse apply order.

**Three decisions this made that no design settles, stated as assayd's rather than the design's.**

- **The route is per AGENT, not per revision, and the deliverable that asked for one per revision was not followed.** Design 03 §3.2 lists "revision weights" as a property of one HTTPRoute and design 20's fallback row shifts `spec.rules[].backendRefs[].weight` on "the serving HTTPRoute", singular — a revision is a backendRef *within* one route. A route per revision has to choose which window to open during a rollout: create-then-delete puts two routes on one hostname and **a listener merges them**, so the revision being rolled out takes a share of production traffic with no weight ever shifted; delete-then-create leaves the hostname unrouted. One object whose backendRef is updated in place has neither, and `TestTheServingRouteFollowsThePromotedRevisionAndDoesNotMultiply` pins it on the route's UID.
- **The hostname is a FLAG, `--gateway-hostname-suffix`, default `assayd.internal`.** No design settles it: §3.2 fixes where the route lives and what names the object and is silent on the host it matches. It is a routing key matched against the `Host` header, not an address — nothing in the chart creates DNS for it, and `values.yaml` says so.
- **`gateway.enabled: true` with the Gateway API CRDs absent makes the operator REFUSE TO START.** §3.1's table asks for `GatewayIncompatible=CRDsAbsent` on each Agent, withholding `Ready`. That is not implemented; what is implemented is the same fail-closed answer one layer earlier and considerably louder. `NOTES.txt` was claiming the condition — a bound no code enforced, rule 7 — and now says what the code does. Registering a cluster-wide HTTPRoute watch against an absent CRD is what would otherwise fail, deep inside the cache and after the process reported healthy. `hack/e2e.sh` consequently installs Gateway API **before** the chart.

**What is NOT emitted, and the sentence A6.5 wrote still stands for all of it.** No `AgentgatewayPolicy`. No `AgentgatewayBackend`. No budget, no rate limit, no gateway authentication, no tool allowlist. `agentgatewaypolicies` and `agentgatewaybackends` remain ungranted in RBAC, correctly, because a verb granted ahead of its code is rule 7 in RBAC form. The weighted two-backend route (`TestWeightZeroActuallyContains`), the MCP tools route and the authorization policy are still hand-authored, because nothing emits those.

**So turning the gateway on publishes a path and enforces nothing, and the Agent says so.** `GovernanceSkipped` stays `True` with the gateway enabled, under reason `PolicyCompilerAbsent`; `GatewayDisabled` is §3.1's own word for the off row. Design 03 §3.1's table pairs the condition only with `gateway.enabled: false` and is silent on the enabled row, which reads as though the flag makes governance real. It does not, and `GovernanceSkipped=False` would be the loud-and-wrong of rule 8 on the exact claim the platform is built on. The condition is classified **normal-true** — owned and sticky — exactly as §3.1 requires, so it flips to `False` rather than vanishing when a compiler finally exists.

**Design 03 remains NOT approved. This encodes a hand-authored proof; it does not implement design 03.** §3.3.1 would in fact *refuse* to emit this route: the A2A serving class names `-auth` as a mandatory concern, its input has no producer until design 06, and an absent mandatory concern is a `PolicyCompileFailed` rather than an omitted policy. That refusal is not implemented, so the emitted route is an **unauthenticated** path to the agent — the same thing the hand-authored route was, now written by the operator instead of a test.

**Two e2e tests moved onto the emitted route rather than authoring their own, and one of them had to.** `TestTheGatewayRefusesADisallowedPrincipal` targets its hand-authored `AgentgatewayPolicy` at the operator's route. Left as it was, its own route would have sat beside the emitted one carrying no policy at all — so the `401` and the `403` would have been measured on one route while an unauthenticated path to the same workload stayed open one hostname over. `TestTheGatewayIsTheOnlyWayIn` moved for the weaker reason that the route it measures should be the one a real install serves. `attachRoute` had no callers left and is deleted.

**Measured, by mutation, on the k3d cluster and against a real API server.** Making the emitter a no-op fails both gateway e2es at the route wait. Rendering `--gateway-enabled=false` into the chart regardless of the value fails them identically, so the chart wiring is load-bearing rather than decorative. Emitting a hostname without the Agent's namespace fails at the assertion that reads the host **off the emitted object** — the first version derived the expected host by calling the operator's own `Hostname()`, which meant a mutation moved the expectation with the code and survived; that trap is now recorded in the helper's comment. At envtest: a no-op emitter, a no-op `assessGovernance`, a sweep that trusts the label instead of the name, and a finalizer that skips the route all fail their tests. Two mutations survived first and made the tests stronger — leaving `kind`/`group` unset on the backendRef produces an Update on **every reconcile of every Agent**, which a `resourceVersion` comparison cannot see because the API server does not bump the version on a no-op write (`countingClient`'s own comment says so, and this is the second time that lesson has been paid for); and comparing `GovernanceSkipped`'s reason against the constant rather than the literal `GatewayDisabled` let a rename pass at every layer at once.

**"Every Agent" needed a test to stay true.** §3.1 puts `GovernanceSkipped` on every Agent, and the reconciler has status paths that build their own condition set — the unresolvable spec, the unresolved env source, the external agent. The condition is *owned*, so a pass that does not assert it CLEARS it: an Agent that went degraded would lose the record of the tier it runs in exactly when someone went looking. That is the same defect the reconciler's own comment records for `SandboxDowngraded` and `GatesSkipped`, one condition later. `TestEveryAgentCarriesTheTierConditionOnEveryStatusPath` pins it, and dropping the assessor from one of those paths fails it.

**The declared-off row is measured too, on the lanes that ship it.** `TestTheDeclaredUngovernedTierEmitsNoRoute` asserts no route in the run namespace and `GovernanceSkipped=True/GatewayDisabled` with `Ready` **not** withheld. It runs where `gateway.enabled` is false — kind, and `ASSAYD_E2E_GATEWAY=0` on k3d — and skips where it is true, because the two rows of §3.1's table cannot both be true of one install.

**The independent review found seven things, and three of them were rules this project has already paid for.** Recorded because the pattern is the finding, not the individual defects.

- **The convergence comparison did not own the fields the writer writes.** `ensureServingRoute` assigns `spec.rules` wholesale, and the comparison looked at hostnames, parentRefs and backendRefs only — so a `RequestMirror` filter or a narrowed path match planted on a converged route by anyone with `httproutes` update in a run namespace was **invisible to the operator forever**: every request to that agent copied elsewhere, or every request but one 404'd, with nothing in the Agent's status saying so. A5.9's admission policy is the defence, and it is the *chart's* — a control this reconciler leaned on and did not itself perform, which is rule 5. Both fields are compared now, and `TestTheOperatorCorrectsDriftOnTheRouteItEmitted` fails when either stops being.
- **The comment defending that comparison was factually wrong**, and it is the reason the gap looked closed: it said an absent `matches` "comes back unchanged". The CRD carries `+kubebuilder:default={{path:{type:"PathPrefix",value:"/"}}}`, so it does not. The code was saved by not looking at the field, not by the reason given for not looking — rule 7 in comment form. `matches` is now rendered explicitly and compared.
- **The route-failure path set `phase: Degraded` without asserting `CondDegraded`** — verbatim the defect recorded two hundred lines above it in the same file, on the `WorkloadUnavailable` branch, with the note that the condition is owned and non-sticky so setting only the phase CLEARS it and design 10 alerts on the condition. An agent unreachable through the Gateway would have paged nobody. A lost race is now separated from a refusal in the same change: an `AlreadyExists` from a stale cache retries without touching `Ready`, because flipping it for informer lag is the other half of the same mistake.
- **The enabled `GovernanceSkipped` message asserted a route that does not exist** for external agents and for every agent in the window before its first promotion — a condition naming a plausible state nobody checked, rule 8, on the one claim the platform is built on. It is worded as a property of the install now.
- **A test comment claimed `assayd-gateway-routes` refuses an ordinary identity's DELETE.** It does not: the policy matches `operations: ["CREATE", "UPDATE"]`, so who may delete a route is an RBAC question and not that policy's. **Whether the reservation should cover DELETE is a real question for A5.9 and is owed**: anyone with `httproutes` delete in a run namespace can currently remove an agent's serving route, and the operator restores it on the next reconcile rather than refusing the delete. The false claim is gone; the question is not settled here.
- **Four new chart args had no chart test.** The mutation this amendment celebrates — rendering `--gateway-enabled=false` regardless of the value — was caught *only* on the k3d e2e lane, which is the expensive gate and the one a contributor does not run. `TestTheGatewayValuesReachTheOperator` and `TestAnUnsetGatewayNamespaceFallsBackToTheOperators` close it, and the second pins A6.2's exact regression from the operator's side: `gateway.namespace` must reach the binary as the same namespace `admission.yaml`'s `$gwNS` renders, fallback included.
- **The stickiness assertion read the map instead of observing `merge()`** — `if !stickyTypes[CondGovernanceSkipped]` compared a map to itself, and **deleting `merge()`'s entire sticky arm left it passing**. That is the ninth-and-tenth test in this repository to pass with its subject deleted, and it was guarding precisely the sentence above about the condition flipping to `False` rather than vanishing. It asserts the transition now, for a normal-true type and an abnormal-true one together.

**Still owed.** The chart ships no Gateway and no agentgateway subchart (A1) — the e2e's Gateway is still the harness's. §3.1's `true → false` transition runs no teardown: with the gateway declared off the operator emits nothing **and sweeps nothing**, so a route left by an install that was once enabled stays until the operator is re-enabled or a human removes it. Route ACCEPTANCE is not read: §3.1 says `Ready` "requires accepted routes" and the operator does not look at route status, so a route the listener rejects still leaves its Agent `Ready`. And `GatewayIncompatible=CRDsAbsent` is owed as the per-Agent form of the refusal above — with the operational cost of the refusal named rather than left to be discovered: it freezes *all* reconciliation and *all* deletion fleet-wide, not only governance. Design 03's §3.1 table has no row for `gateway.enabled: true` and does not carry the `PolicyCompilerAbsent` reason this operator writes; that belongs in design 03 and is not added here, because that document is not this amendment's to edit beyond its Status line. The listener an emitted route attaches to is the hard-coded `http` with no value behind it, and its failure mode is silent (`NoMatchingParent`, 404s, `Ready` unaffected) — `values.yaml` says so beside `gateway.name` rather than the chart pretending to configure it.

### A6.11 (2026-09-11) — the second review of A6.10: the route took its port from the wrong revision, and three tests could not fail

A second independent review of A6.10 returned **REVISE**: two blockers, three majors, six minors. It is recorded as its own amendment rather than folded into A6.10, because A6.10 already records the first review and a record is not rewritten.

**The route's port came from the desired spec; its backend came from the serving revision.** The emitter rendered `backendRef.name` from `status.activeRevision` and `backendRef.port` from `agent.Spec.Runtime`. Those describe different revisions for the whole of every rollout. A port is behaviour surface (ADR-0031), so a port edit mints R2 while R1 keeps answering on its old port, and `ensureService` renders only the desired revision's Service, so R1's Service never learns the new port. If R2 never came up — a bad image, a crashloop — the operator rewrote the serving route to `<agent>-R1:<new port>`. R1's Service does not publish that port. Every request through the gateway failed from then on, and nothing said so, because the operator does not read route status. An edit that was never promoted took down the revision that was still healthy. That is the failure the per-revision Service exists to prevent. A pinned rollback across a port change was worse: `ensureService` does not run at all under a pin.

The port is now read off the serving revision's Service, by the port name `a2a` (`servingBackendPort`). A missing Service or a missing port is an error, reported as `RouteApplyFailed` and `Degraded`, and the port is never guessed. `TestAPortChangeThatNeverComesUpDoesNotMoveTheServingRoute` edits the port, leaves R2 unavailable, and asserts that the route still names R1 at the port R1's Service publishes. It then promotes R2 and asserts that the route follows, port included. A unit test asserted the defect as correct: it compared the route's port with the spec while rendering against an unrelated revision. That test is replaced.

**`equalRoute` did not own `backendRefs[].filters` or `backendRefs[].namespace`.** This is the attack the first review caught at `rules[].filters`, one level down. The reviewer reproduced both on unmodified source. A `RequestMirror` on the backendRef survived every reconcile. A `namespace` on the ref also survived: it needs a `ReferenceGrant` nobody wrote, so the agent went off the air while it reported `Ready`. Both fields are compared now. `rules[].retry` and `rules[].sessionPersistence` are not gaps **on the standard channel**, whose CRD does not carry them — and the standard channel is the only one this repository installs or tests (envtest, `hack/e2e.sh`). Nothing requires it, though: an experimental-channel install keeps both fields and neither is compared.

**Three tests could not fail for the reason they claimed.**

- The `Ready=False` assertion on the route-failure path was nested inside `if Ready != True`. So `Ready=True`, the one outcome the test existed to forbid, skipped the check. Deleting the reconciler's `Ready=False` for that path left the test green. The assertion is now one guard.
- The drift test planted a mirror and a narrowed match in one update. The writer replaces `spec.rules` wholesale, so either comparison firing corrected both, and deleting either one left the test green. A6.10 said this pair was killed by mutation, and that was false.
- Nothing pinned the `parentRefs` and `hostnames` comparisons. Deleting either one left the whole envtest suite green.

`TestTheOperatorCorrectsDriftOnEachFieldOfTheRouteItEmitted` replaces the drift test. It has one planted drift per case — six cases when this amendment was written, eight after the re-review below. Each case first asserts that the drift survived the API server, because a field the CRD strips would make the case measure nothing. It then asserts that the whole spec returns to what it was.

**Design 03 §3.2's collision check now exists for routes, and it keys on the Agent's identity.** §3.2 says a name collision is "a compile error naming both inputs, never a silent reuse". Before this change, update authority was looser than delete authority. The sweep required the `agent-uid` label to match before it deleted a route. The converge path rewrote whatever it found under the name. Now a route at an Agent's name that carries another Agent's name or namespace labels is refused and left untouched. The refusal names both Agents and is reported as `RouteApplyFailed` and `Degraded`.

A route whose UID differs but whose name and namespace labels match this Agent belongs to a predecessor, and it is adopted. An Agent deleted while the gateway was declared off sweeps nothing, so refusing that route would block a recreated Agent forever behind a route nothing removes. A route with no `agent-uid` label is also adopted, whatever its other labels say. None of this is evidence against an attacker, because a label needs only `update` to forge. Forging the labels to differ gets a refusal for an Agent whose run namespace the forger can already write. Forging them to match gets an adoption that overwrites the forger's spec. `TestARouteAnotherAgentOwnsIsRefusedNotTakenOver` and `TestAPredecessorsRouteIsAdoptedNotRefused` pin the two halves.

**Removed, because nothing used them.**

- **RBAC:** `httproutes: patch` and `httproutes/status: get`. A6 granted them and no code ever called them: the emitter uses get, list, watch, create, update and delete, and route status is not read. A6.10 cited "a verb granted ahead of its code is rule 7 in RBAC form" for the agentgateway kinds and left these two in place.
- **Code:** `ownedRoutes`' no-matching-kind arm. It was meant to let the finalizer release if Gateway API were uninstalled under a running operator, but it could not fire. It runs only with the gateway enabled, which means `SetupWithManager` had already proved the CRD and started a cache-backed informer, and a `List` against a started informer answers from the cache, not the RESTMapper. Nobody has measured what uninstalling Gateway API under a running operator does; that is owed.

**`NOTES.txt` printed a hostname the operator might not use.** It interpolated `gateway.hostnameSuffix` unconditionally, while `operator.yaml` passes `--gateway-hostname-suffix` only when the value is set. An overlay that cleared the value got `<agent>.<ns>.` in the notes while the binary used `assayd.internal`. The notes now fall back to the same default. No test pins this, because `helm template` does not render `NOTES.txt`.

**Added to A6.10's "Still owed".** Design 10 §5 pages on `GatewayIncompatible=CRDsAbsent`, keyed on the reason. A6.10 made a missing CRD a refusal to start instead of that condition, so the page can never fire until the condition exists. The only signal is the operator pod's own CrashLoop.

**Measured by mutation, against a real API server.** Every mutation below compiles, and each one fails exactly the test named:

| Mutation | Finding | Result |
|---|---|---|
| Render the route's port from `port(agent.Spec.Runtime)` again | B1 | killed: `TestAPortChangeThatNeverComesUpDoesNotMoveTheServingRoute` |
| Stop comparing `backendRefs[].filters` | B2 | killed: only the `rules[].backendRefs[].filters` case |
| Stop comparing `backendRefs[].namespace` | B2 | killed: only the `rules[].backendRefs[].namespace` case |
| Stop comparing `rules[].filters` | M4 | killed: only the `rules[].filters` case |
| `equalMatches` always returns true | M4 | killed: only the `rules[].matches` case |
| `equalParentRef` always returns true | M5 | killed: only the `parentRefs` case |
| Per-hostname comparison never differs | M5 | killed: only the `hostnames` case |
| Drop the route-failure path's `Ready=False` | M3 | killed: `TestARouteThatCannotBeWrittenIsALoudDegradation` |
| Skip `routeCollision` | m9 | killed: `TestARouteAnotherAgentOwnsIsRefusedNotTakenOver` |
| Refuse on any UID mismatch, predecessors included | m9 | killed: `TestAPredecessorsRouteIsAdoptedNotRefused` |

Each drift mutation failed exactly one case, and no other. That is the property the single combined test lacked. The first two M5 mutations deleted the body of the comparison loop, which left the loop variable unused, so they **did not compile**. They are INVALID and prove nothing. They were replaced by the two rows above, which compile.

**The re-review of these fixes found one more gap of B2's class, and one comparison no test pinned.** `equalParentRef` compared five of a ParentReference's six fields, but not `port`, even though `ensureServingRoute` assigns the whole parentRef. A `parentRefs[0].port` planted on the unmodified head survived every reconcile. A parentRef's port must match the listener as well as its `sectionName`, so a wrong port attaches the route to no listener: the agent goes off the air while it reports `Ready`. The drift surviving was measured; the route matching no listener was not measured against a real gateway. Separately, nothing pinned `equalRoute`'s label comparison. Disabling it left every route test green. A route whose `agent-uid` label is edited is skipped by both the sweep and the finalizer, so it would outlive its Agent and keep publishing a hostname that reaches nothing. `port` is compared now. Both fields are cases in the drift table, and the table now asserts the four provenance labels as well as the spec:

| Mutation | Finding | Result |
|---|---|---|
| Stop comparing `parentRefs[].port` | re-review BLOCKER | killed: only the `parentRefs[].port` case |
| Never compare labels | re-review MINOR | killed: only the `metadata.labels[agent-uid]` case |

The same review corrected two sentences above: `retry` and `sessionPersistence` are not gaps only on the standard channel, and adoption is keyed on the `agent-uid` label alone.

### A6.12 (2026-09-11) — the A2A half is literal: an agent completes a `SendMessage` task through the gateway

A6.8 recorded what the MCP leg did not complete. ADR-0030's clause is "one agent completes one **A2A task** and one MCP tool call through the gateway", and A6.8 said the A2A half was not literal: `test/responder` served a conformant card and answered on `/assayd-test/echo`, which is on no A2A binding, so an agent completed a *request* through the gateway and not an *A2A task*. That record stands as what was true on 2026-09-09. This amendment is what changed.

**`test/responder` implements one A2A method, `SendMessage`, on the HTTP+JSON binding its card declares.** The request is `POST /message:send` carrying a `SendMessageRequest`. The response is a `SendMessageResponse` whose oneof carries a `Task` in `TASK_STATE_COMPLETED`. Field names and enum values are those protojson renders from `a2a.proto` @ `v1.0.1`. Every e2e request to an agent now uses it and sends `A2A-Version: 1.0`, and every answer is checked for a completed task — through `completedTask`, or, in the weighted split, by counting a backend only when its answer's state is `TASK_STATE_COMPLETED`. The five gateway tests do this: through the operator's route, after that route is recreated, under the authorization policy, beside the NetworkPolicy, and across the weighted split. So does the revision-Service test. `TestAnAgentAnswersThroughTheGateway` therefore completes an A2A task through the operator-emitted route. That is the A2A half of ADR-0030's clause for a **client**, not for an agent: the clause's subject is "one agent", and the MCP call in `TestAnAgentCallsAnMCPToolThroughTheGateway` (A6.8) is made by the harness's probe Pod, not by the responder, in a separate test against a separate backend. An agent that completes a task *by* calling a tool through the gateway is still owed.

**The state is asserted, not only the echo.** Every test used to pass when the body contained its own words. A 200 carrying any JSON, such as a gateway error page or a different agent's shape, would have passed a test that looked only for the text it sent. `completedTask` requires the oneof to carry a task, and requires that task's state.

**The responder refuses what is not a `SendMessageRequest`.** A body missing `messageId` or `role`, with the agent's role, or with no text part gets a 400. That includes the exact body every e2e sent before this amendment. A fixture that accepted it would let a caller that is not speaking A2A pass. The retired path returns 404, rather than staying beside the real method as a second, non-A2A way to get an answer.

**A6.8 said the fixture is not the place to grow a method set, and that was half right.** The half that stands is that the A2A *client* is design 08's (`assayd invoke`, `08:22`), and this fixture must not become the only thing in the repository that claims to speak A2A. Exactly one method is implemented, and the source enumerates what is not: `GetTask`, `ListTasks`, `CancelTask`, streaming (the card says `streaming: false`), push notifications, the extended card, `tenant`, `configuration`, and the binding's error mapping. Any A2A version but 1.0 is refused: spec §3.6 has a client send `A2A-Version` and an agent read an absent header as 0.3, so the responder refuses an absent or other value with a 400 naming `VersionNotSupportedError`. Answers carry `Content-Type: application/a2a+json`, which the HTTP+JSON binding says they SHOULD. A refusal is a 400 with a plain body, and a test may rely on the status code, not the shape. Two refusals are this fixture's choices rather than the proto's: text is its only input mode, and it holds a client's message to `ROLE_USER`, which the spec defines as a message "from the client to the server" and the proto does not enforce. What overrode the other half is that ADR-0030's stop criterion is literal, and an agent cannot complete an A2A task without a server that speaks A2A. Design 09 owns the real server templates; this fixture is not one.

**The review of this amendment found three things the first version claimed and did not do, and they are fixed rather than softened.** `countBackends` in the weighted-split test grepped only `"agent"` out of each answer, so a responder that never completed a task still passed it — the reviewer measured exactly that, 40 of 40; it now counts a backend only for a completed task and fails on any other. Nothing sent or checked `A2A-Version`, so by the spec's rule every request was a 0.3 request answered in 1.0 shapes; both sides now negotiate 1.0. And the sentence in CLAUDE.md and AGENTS.md said ADR-0030's clause was met, dropping its subject — the MCP call is the harness's, not an agent's; it now says so. Two responder behaviours were unmeasured — that each task gets its own id, and that every text part is echoed — and each has a test now.

**The operator still speaks no A2A.** It reads the card (design 02 A68, A71) and nothing else. Design 02 §5's row is corrected by design 02 A73.

**Measured by mutation.** Every mutation below compiles.

| Mutation (`test/responder`) | Killed by |
|---|---|
| Accept a request with no `messageId` | the `no messageId` case of `TestItRefusesWhatIsNotASendMessageRequest`. The old echo body is still refused under this mutation, because it lacks a `role` too. |
| Skip the `role` check | the `no role` and `the agent's role` cases |
| Answer `"ok"` instead of `TASK_STATE_COMPLETED` | `TestItCompletesASendMessageTask` |
| Encode a bare `Task` instead of the `SendMessageResponse` oneof | `TestItCompletesASendMessageTask` |
| Serve every method, not only POST | `TestItRefusesWhatIsNotASendMessageRequest` (the 405) |
| Drop the caller's `contextId` | `TestItCompletesASendMessageTask` |
| Accept a message with no text part | the `no text part` case |
| `test/responder` answers `TASK_STATE_WORKING`, measured on the e2e (k3d, `E2E_RUN=TestAnAgentAnswersThroughTheGateway$`) | `TestAnAgentAnswersThroughTheGateway` fails at `completedTask` with `the task is "TASK_STATE_WORKING", not TASK_STATE_COMPLETED`, through the gateway and on the operator's route |

**Run on k3d.** `make e2e`: 18 PASS, and the only SKIPs are the sentinel and the declared-off test. `ASSAYD_E2E_GATEWAY=0 make e2e`: 13 PASS, including `TestAnAgentAnswersARequestThroughItsRevisionService`, which completes a task through the revision Service; the gateway tests skip. Both runs exit 0.

**The review's fixes, measured.** Every mutation below compiles.

| Mutation | Killed by |
|---|---|
| `countBackends`'s responder answers `TASK_STATE_WORKING` (e2e, `E2E_RUN=TestWeightZeroActuallyContains$`) | `TestWeightZeroActuallyContains`: answers that identify a backend without completing an A2A task. The reviewer's run of this same mutation **passed, 40 of 40**, before the fix |
| The e2e helper sends no `A2A-Version` (e2e, `E2E_RUN=TestAnAgentAnswersThroughTheGateway$`) | `TestAnAgentAnswersThroughTheGateway` |
| The responder skips the version check | `TestItRefusesAnyVersionButOnePointZero` |
| The responder answers as `application/json` | `TestItCompletesASendMessageTask` |
| Every task gets the same id | `TestEachTaskGetsItsOwnID`. A first version of this mutation did not compile and is INVALID |
| Only the first text part is echoed | `TestEveryTextPartIsEchoed` |

Re-run on k3d after the fixes: `make e2e` 18 PASS, `ASSAYD_E2E_GATEWAY=0 make e2e` 13 PASS, both exit 0.

### A6.13 (2026-09-11) — an agent completes a task by calling a tool through the gateway

A6.12 made the A2A half of ADR-0030's clause literal for a **client**, and said so: the clause's subject is "one **agent**", and the MCP call in `TestAnAgentCallsAnMCPToolThroughTheGateway` was made by the harness's probe Pod, not by an agent. This amendment closes that gap, with test fixtures.

**The agent now makes the tool call itself.** `TestAnAgentCompletesATaskByCallingAToolThroughTheGateway` runs this sequence:

1. A caller sends one `SendMessage` to an agent through the operator's route.
2. The agent's `call_tool` skill makes an MCP `initialize`, `notifications/initialized` and `tools/call` exchange with `test/mcpserver`.
3. That exchange goes through the Gateway's `tools` listener, at the `ASSAYD_GATEWAY_URL` the operator injected.
4. The agent completes the task, and the tool's answer is the task's artifact.

So one agent completes one A2A task *and* one MCP tool call, both through the gateway.

**Two facts show the call went through the gateway and not around it.**

- The agent has no other address for tools. With `ASSAYD_GATEWAY_URL` unset, the skill fails the task instead of falling back; `TestWithNoGatewayTheToolCallFailsRatherThanGoingElsewhere` covers this.
- Once the gateway's allowlist excludes `delete_everything`, the agent's task fails with `TASK_STATE_FAILED`, carrying the gateway's `Unknown tool: delete_everything`. The fixture MCP server answers that tool normally, so the refusal can only be the gateway's. The allowed tool keeps completing.

**`gateway.url` is new in the chart, because `ASSAYD_GATEWAY_URL` was never injected.** The operator has had `--gateway-url` since design 02 §11, but no chart value reached it, so no agent on any install received the variable. Every agent's egress contract was empty. `gateway.url` now renders `--gateway-url` when set and renders nothing when unset. `TestTheGatewayURLReachesTheOperatorOnlyWhenSet` pins both. `hack/e2e.sh` sets it to the Gateway's `tools` listener, and the e2e asserts that the URL the agent reports back is the one the chart was given.

**Still not governed, and still hand-authored.** The MCP backend, its route and the allowlist remain hand-authored, and nothing in assayd emits them. The tool call is unauthenticated at the gateway: no identity is attached to the agent's egress, because design 06 has no implementation. Nothing enforces that an agent's egress goes *only* to the gateway either. No egress NetworkPolicy is materialized, so an agent that knew another address could use it. The fixture simply has no other address.

**Measured by mutation.** Every mutation below compiles.

| Mutation | Killed by |
|---|---|
| The chart stops passing `--gateway-url`, so no gateway is injected (e2e, `E2E_RUN=TestAnAgentCompletesATaskByCallingAToolThroughTheGateway$`) | `TestAnAgentCompletesATaskByCallingAToolThroughTheGateway`: the operator renders no `ASSAYD_GATEWAY_URL` into the workload. **Two first runs of this mutation survived**, and the reason was the mutation harness, not the test. The paragraph below explains |
| `callTool` drops the Host header, so the call reaches no tool route | `TestItCompletesATaskByCallingATool` |
| `callTool` sends `ping` instead of `initialize` | `TestItCompletesATaskByCallingATool` and `TestARefusedToolFailsTheTaskAndSaysWhy`. A first version of this mutation, which skipped the call entirely, did not compile and is INVALID |
| A gateway's JSON-RPC refusal is treated as success | `TestARefusedToolFailsTheTaskAndSaysWhy` |
| With no gateway injected, the call goes ahead anyway | `TestWithNoGatewayTheToolCallFailsRatherThanGoingElsewhere` |

**A mutation survived twice, and the first explanation for it was wrong.** The harness that removed the `--gateway-url` line kept its backup as `charts/assayd/templates/operator.yaml.b`. Helm renders every file under `templates/`, so the unmutated copy was rendered and installed beside the mutated one, and the operator kept the flag. That was confirmed on the cluster: the operator running under the "mutated" image still carried `--gateway-url`. Rendering the mutated chart with no backup present produced no flag. The first explanation recorded was a leftover Pod from the previous run, and that was not the cause. The test was hardened on the strength of that wrong explanation, and the hardening stays because it is defensive, not because it fixed anything. It waits until the previous Agent and all of its workloads are gone before creating a new one, because the same spec mints the same revision name. It also asserts `ASSAYD_GATEWAY_URL` on the Deployment *this* run's operator rendered, before any request is sent. With the backup kept outside the chart, the mutation is killed at that assertion. Mutation harnesses in this repository back up to a sibling file. That is harmless next to Go sources, which ignore the extension, and it is fatal inside `charts/*/templates/`.

**Run on k3d.** `make e2e`: 19 PASS. `ASSAYD_E2E_GATEWAY=0 make e2e`: 13 PASS, with the new test skipping, like every gateway test there. Both exit 0.

