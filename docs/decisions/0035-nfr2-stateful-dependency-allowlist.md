# ADR-0035: NFR-2's stateful-dependency allowlist names two substrates and one sink, matched exactly, over every object that can hold state

- **Status**: **proposed** · 2026-09-29 · **not decided.** The human asked for this draft on 2026-09-29. It goes to an independent critique first, and then to the human, who decides D1–D6 below. Until the human decides, nothing here binds anything: `TestStatefulDependencyAllowlist` and NFR-2's text stay as they are, and every "Recommended" below is the author's.
- **Refines, if the human takes it**: ADR-0002 rule 2 ("Postgres + NATS are the only stateful deps"). It does not supersede ADR-0002, whose other five rules are untouched, and ADR-0002's text is not edited.

## Context

NFR-2 (`docs/requirements.md`) was written as *"Postgres and NATS JetStream are the only stateful dependencies, ever"*. `TestStatefulDependencyAllowlist` (`test/chart/chart_test.go`) enforces something else, and weaker:

- **Three entries, not two.** `postgres` and `nats` are "substrate (rule 2)". `openobserve` is "observability sink, not substrate". That third entry comes from design 07 §2 and design 10 §2, neither of which the human has approved. ADR-0022 records that a stateful allowlist "with reasons" is CI-enforced, and that OpenObserve is a "sink never record". It does not name the entries, and it does not say how a third stateful dependency squares with ADR-0002 rule 2.
- **A substring match.** `matchesAllowlist` uses `strings.Contains`, so `nats` admits `nats-sidecar-cache`, and `postgres` admits `not-postgres-redis`.
- **Two kinds only.** It reads rendered `StatefulSet`s and `PersistentVolumeClaim`s. A `Deployment` that mounts a PVC or a `hostPath` is not read.
- **Postgres would be invisible to it.** Design 07 §3 chooses CloudNativePG (CNPG) for Postgres. CNPG does not use `StatefulSet`s. Its operator creates Pods and PVCs itself from a `postgresql.cnpg.io/v1` `Cluster` object (CNPG docs, "Storage" and "Custom Pod Controller", read 2026-09-29 through Context7). So a chart that installs Postgres the way design 07 says renders neither kind this test reads. The `postgres` entry would never match anything, and neither would any other storage an operator CR claims.
- **One render only.** The test renders the chart's defaults. A stateful object behind `profile: local`, `gateway.enabled` or any other value is never rendered, so never read.
- **Vacuous today.** The chart renders zero `StatefulSet`s and zero PVCs, so the check has never refused anything.

What the corpus already says, found by grepping `docs/designs` and `docs/decisions`:

| Dependency | Class the corpus gives it | Where |
|---|---|---|
| Postgres | substrate | ADR-0002 rule 2; ADR-0004 (DBOS, workflow state); design 06 (Zitadel on it); design 07 §2 (SPIRE's datastore on it, "one less PVC"); designs 16, 20, 21 |
| NATS JetStream | substrate | ADR-0002 rule 2; ADR-0004 (events, KV, object store, receipts); designs 04, 05, 21 |
| OpenObserve | sink, not substrate | design 07 §2; design 10 §2 and D1 ("sink, never system-of-record"); ADR-0022's Observability line ("OpenObserve = sink never record") |
| FalkorDB, one per managed graph | per-graph workload state, not substrate | ADR-0018 ("doctrine rule 2 governs the platform substrate, not per-graph workload state"); designs 01 §2, 13 §2 |
| "object store" | not stated | designs 16 §2 and 27 §2 name one; ADR-0004 gives JetStream an object store; `architecture.html` puts OpenObserve "on object storage". None says whether it is JetStream's or an external S3-compatible service. |

No design specifies bring-your-own (BYO) Postgres or NATS. The only BYO arm in the corpus is `idp: byo` (design 07 §3).

## Decision (proposed — each item is the human's)

**D1 — the entries and their classes.**

- **(a) Recommended.** Three entries in two classes.
  - **Substrate**: Postgres and NATS JetStream. Substrate is where the platform keeps its record: state that nothing else can reconstruct.
  - **Sink**: OpenObserve. A sink holds only copies or derivations of state recorded elsewhere. Losing it loses no record. A decision the platform takes from sink data must fail to "no decision" when the sink is gone, and say so on a condition (NFR-8). It must never fail to a different decision.
  - NFR-2's wording becomes "Postgres and NATS JetStream are the only stateful **substrate**. OpenObserve is the only stateful **sink**. Nothing else in the chart holds state."
- (b) Two entries. OpenObserve leaves the allowlist, so it must be external or must run without durable storage.
- (c) One flat list with no classes. This is cheaper, and it drops the distinction that stops a "sink" from quietly becoming a record.

The sink rule is already under strain, and deciding (a) makes that visible. Design 20 auto-rolls-back on SLO burn computed from design 10's signals, which are derived "at the tap/OpenObserve". Nobody has checked whether that rollback fails to "no decision" when OpenObserve is down. Design 10 §7 says only that the platform's blindness "is visible via CR conditions".

**D2 — matching.**

- **(a) Recommended.** Match exactly. Each entry names every object it admits as `(apiVersion/kind, name)`, as rendered with the test's fixed release name, `assayd`. For example: `StatefulSet assayd-nats`. Anything not named is refused. Rendered names carry the release name, so the list is exact for the render the test makes, and it changes when a subchart's naming changes, which is the point.
- (b) Match exactly on the `app.kubernetes.io/name` label. This survives renames, but it trusts every upstream chart to set that label, and to set it truthfully.
- (c) Keep the substring match. Rejected: it admits `nats-sidecar-cache`.

**D3 — what counts as stateful in a render.**

- **(a) Recommended.** An object counts if it is any of these:
  - a `StatefulSet`, whether or not it has `volumeClaimTemplates`;
  - a `PersistentVolumeClaim`;
  - a pod-bearing workload (`Deployment`, `DaemonSet`, `ReplicaSet`, `Job`, `CronJob`, `Pod`) whose pod spec has a `persistentVolumeClaim`, `ephemeral` or `hostPath` volume. An `ephemeral` volume creates a PVC, and `hostPath` writes to the node;
  - an object of a **storage-claiming kind**: a kind whose controller creates PVCs for it. The list starts with one kind, `postgresql.cnpg.io/v1` `Cluster`, and the change that adds a subchart with such a kind adds that kind to the list.
- `emptyDir`, including `medium: Memory`, does **not** count. It dies with the pod.
- (b) Keep `StatefulSet` and `PersistentVolumeClaim` only. This leaves CNPG's Postgres invisible, and a `Deployment` with a PVC unread.

The part of (a) that nothing can enforce, stated: a storage-claiming kind that nobody adds to the list is not caught. A render cannot know what another controller will create.

**D4 — state the operator creates per custom resource.** A chart render cannot see it. ADR-0018 already decided one case: a managed graph's FalkorDB is per-graph workload state, outside rule 2. The question is how far that reaches.

- (a) Generalize ADR-0018. Anything the operator creates for a custom resource is workload state, outside NFR-2. This is simple, and it opens a hole: a per-Agent cache with a PVC would need no entry.
- **(b) Recommended.** Keep ADR-0018's exemption to what it names, the per-graph backends of designs 01 and 13. Any other storage the operator creates needs an entry under D6. The change that first makes the operator create a PVC, a `StatefulSet` or a `hostPath` volume adds an envtest that fails on an unlisted one, because the chart test cannot see it.

Today the operator creates none of these. Grepping `internal/`, `api/`, `cmd/` and `charts/` for `PersistentVolumeClaim`, `volumeClaimTemplates`, `hostPath` and `emptyDir` finds nothing.

**D5 — bring-your-own and external services.**

- **(a) Recommended.** An external instance fills an entry's **role**. A BYO Postgres counts as the Postgres entry. When BYO is selected, the chart must not render its own. An external service of a type that no entry names is a stateful dependency and needs its own entry. An S3-compatible object store that the platform requires is an example.
- (b) External services are outside NFR-2, because they add no pods. Rejected: then "only Postgres and NATS" can be met by requiring an external S3.

Under (a), a chart render cannot see an external dependency. Enforcing it is review discipline, as NFR-1's written-justification half already is, and this ADR says so. D5 also makes one open question binding: whether designs 16 and 27's "object store" is JetStream's or an external service. If it is external, it needs an entry before either design is built.

**D6 — how an entry is added.**

- **(a) Recommended.** The change that introduces a stateful object does all three of these:
  1. adds the object to the allowlist, with its class and a reason;
  2. adds a numbered amendment to this ADR, naming the design that calls for the object;
  3. gets the human's decision, recorded in that amendment. A new **substrate** entry also amends ADR-0002 rule 2's reading, so it needs the human in any case.

  The test enforces the link: every allowlist entry cites an `ADR-0035` amendment heading, and the test fails if that heading is not in this file. That is the same mechanism `test/docs/approval_parts_test.go` uses to pin the human's approvals.
- (b) Design 07 §2's rule: editing the allowlist in the same PR is enough, and a reviewer judges the reason. This is lighter, and it is today's rule. No test sees whether a human decided.

**Not the human's.** Where the allowlist lives is the author's call. Design 07 §2 names a `stateful-allowlist.yaml`, which does not exist. The test keeps a Go map today. The test change below keeps it in the Go test, beside the check, so that one place changes. Design 07's file is the alternative, and changing to it costs nothing.

## Consequences

- **Nothing changes until the human decides.** This PR does not change `TestStatefulDependencyAllowlist`. NFR-2 cites this ADR as proposed only.
- **The test change, contingent on D1(a), D2(a), D3(a), D4(b) and D6(a), and not implemented.** It is one test change in `test/chart/chart_test.go`, with a mutation for each clause:
  1. Replace `matchesAllowlist`'s `strings.Contains` with equality on `(apiVersion/kind, name)`. A fixture renders a planted `StatefulSet assayd-nats-sidecar-cache`, and the exact check refuses it. Mutation: restore `Contains`, and the fixture passes, so the mutation is caught.
  2. Read every kind D3(a) lists, including pod specs' `volumes` and the storage-claiming kind list. Mutations: a planted `Deployment` mounting a PVC, a planted `hostPath` volume, and a planted CNPG `Cluster` must each fail the test. A planted `emptyDir` must pass it.
  3. Render every profile and every chart value that changes what renders (`profile: local`, `gateway.enabled=true`), and take the union of the renders. `tier: plus` fails to render today (`TestUnimplementedTierIsRefusedNotIgnored`), and that stays out until it renders. Mutation: a stateful object rendered only under `profile: local` must fail the test.
  4. Each entry carries its role, its class, its reason, the objects it admits, and a reference into this file. The three entries D1 decides cite `D1`. Every later entry cites its amendment's heading. A new check reads this file and fails on a reference with no matching heading. Mutation: cite a missing amendment.
  5. Report vacuity, and do not hide it. The test logs how many stateful objects it read in each render. It also asserts the positive: every object the allowlist names is actually rendered. So an entry whose subchart was removed, or renamed, fails the test instead of standing unused. While the chart renders no substrate, the three entries admit no object yet, and the log says the check read nothing.
- **With the test change, the check can see Postgres under CNPG.** Without it, NFR-2's "it will start doing real work the first time the chart deploys its own substrate" is false for Postgres.
- **D1(a) puts design 20's rollback on notice.** Design 20 must show that its rollback fails to "no decision" when OpenObserve is down, or that it does not read sink data.
- **D5(a) blocks designs 16 and 27** from shipping an external object store until one of two things happens: it gets an entry, or it is shown to be JetStream's.
- **Revisit** when the chart first renders a subchart. That is when the vacuous pass ends and D2(a)'s exact names can be written down.
