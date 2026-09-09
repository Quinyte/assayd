# Design 07: Umbrella chart, profiles, e2e CI

- **Status**: **approved** — critique PASS at r2 (reviews/07-review.md) · ADR-0022 · amendments A1–A5 below, A5 (2026-09-03) not yet critiqued
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

**Owed**: the SPIRE and agentgateway subcharts that would render A5.2 and A5.3; `NOTES.txt` (A1) gaining the NetworkPolicy sentence; the e2e cell that proves NetworkPolicy enforcement on k3d and reports it unverified on kind; A5.9's policies and their negative e2e, with the A42 implementation.
