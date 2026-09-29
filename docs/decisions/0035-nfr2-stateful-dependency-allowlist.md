# ADR-0035 (proposed): what NFR-2's stateful-dependency allowlist admits, how it matches, and how an entry is added

- **Status**: **proposed** · 2026-09-29 · **not decided.** The human asked for this draft on 2026-09-29, to be critiqued independently and then brought to them. The first critique returned REVISE, with 2 BLOCKER, 7 MAJOR and 6 MINOR findings ([`reviews/0035-critique-r1.md`](../designs/reviews/0035-critique-r1.md)). This revision, r1, answers that critique and is due for a second one. The human decides D1–D7 below. Every "Recommended" is the author's. Until the human decides, `TestStatefulDependencyAllowlist` and NFR-2's text stay as they are.
- **If the human takes D1(a)**: it is an **exception to** ADR-0002 rule 2 ("Postgres + NATS are the only stateful deps"), not a refinement of it. ADR-0002's text is not edited.

## Context

NFR-2 (`docs/requirements.md`) says *"Postgres and NATS JetStream are the only stateful dependencies, ever"*. `TestStatefulDependencyAllowlist` (`test/chart/chart_test.go`) enforces something weaker:

- It allows **three** names: `postgres`, `nats` and `openobserve`. It files OpenObserve as "observability sink, not substrate", which comes from design 07 §2 and design 10 §2. The human has approved neither design. ADR-0022 records that an allowlist "with reasons" is CI-enforced, but it names no entries.
- It matches by **substring**, so `nats` admits `nats-sidecar-cache`.
- It reads only `StatefulSet`s and `PersistentVolumeClaim`s, and only in the **default render**.
- It **passes vacuously**, because the chart renders neither kind today.

Two upstream facts make the requirement false even before anything more is built:

- **CloudNativePG (CNPG), design 07's Postgres, renders no `StatefulSet`.** Its operator creates Pods and PVCs from a `postgresql.cnpg.io` `Cluster` (CNPG docs, via Context7). The current test would never see Postgres.
- **SPIRE, which is core tier (design 07 §3), holds state that is not Postgres.** Rendered on 2026-09-29 with release name `assayd` and `dataStore.sql.databaseType=postgres`, spire 0.30.2 produces `StatefulSet assayd-server` with PVC template `spire-data`. Its `KeyManager` is `disk`, the chart default, with `keys_path: /run/spire/data/keys.json`, so the CA signing keys live on that PVC. The chart's own values describe `persistence.type`'s `emptyDir` as "testing or nested child only".

What the corpus and the upstream charts say, found by grepping `docs/designs` and `docs/decisions` and by rendering the charts:

| State | Where it lives | Source |
|---|---|---|
| Durable workflows, eval results, audit index, Zitadel, SPIRE registrations | Postgres | ADR-0002 rule 2; ADR-0004; designs 06, 16, 20, 21; design 07 §2 |
| Events, KV directory, receipts, **object store** | NATS JetStream | ADR-0004; `architecture.md`'s substrate line and table; designs 04 (receipt bodies at `obj://`), 05, 14, 17 |
| SPIRE CA signing keys | a PVC on `StatefulSet assayd-server` at chart defaults | spire 0.30.2 render. **Design 07 §2's "one less PVC" is false at these defaults**: moving the datastore to Postgres leaves the key PVC in place. |
| Interior agent spans, component logs | OpenObserve **only** | design 10 §3 |
| One FalkorDB per managed graph; a BYO endpoint mode adds none | per-graph workload state | ADR-0018; designs 01 §2, 13 §2 |
| Per-Agent persistent scratchpad | an agent-sandbox `Sandbox`, whose controller creates PVCs from `volumeClaimTemplates` | design 02 §3.2; agent-sandbox docs, via Context7 |
| Candidates for **external** storage | 6-year archive of receipts and bodies; OpenObserve's HA chart (`ZO_S3_PROVIDER: s3` at 1.0.2 defaults); CNPG backups (the `cnpg/cluster` chart ships them disabled, with method `barmanObjectStore` when enabled) | ADR-0014; design 27 §4; chart renders and Context7 |
| CRs; design 02 §2's run-namespace `ConfigMap`, which is "not reconstructable by the operator" | the API server | design 02 §2 |

## D1 — The classes, and the entries

The classes are defined by **authority**, not by uniqueness.

- A **substrate** is a store the platform reads to take a decision or to answer an audit question.
- A **sink** is a store nothing takes a decision or an audit answer from. Losing it loses graphs, never audit (design 10 §2). A sink can hold data found nowhere else, as OpenObserve does for interior spans.
- The **API server** is neither. It is Kubernetes itself, a given of NFR-3, and outside NFR-2.

The options:

- **(a) Recommended.** Postgres and NATS JetStream are the substrate. OpenObserve is the one sink, bound to the chart **`openobserve-standalone`**. At 1.0.1 that chart renders one `StatefulSet assayd-openobserve-standalone` on local disk. The HA chart, `openobserve` 1.0.2, also renders its own CNPG `Cluster`, its own `StatefulSet assayd-nats`, four more `StatefulSet`s and an S3 store: a second Postgres, a second NATS, and an external service. NFR-2 would then read: "Postgres and NATS JetStream are the only stateful substrate. OpenObserve is the only stateful sink."
- (b) Postgres and NATS only. OpenObserve leaves the chart. If it becomes external, D6(a) still needs an entry for it, so (b) with D6(a) means OpenObserve is either run with no durable storage or not run at all.
- (c) One flat list with no classes. This is cheaper. It drops the rule that keeps a sink from quietly becoming a record.

**What (a) puts in question.** Design 20 auto-rolls-back on SLO burn computed from design 10's signals, and those are derived "at the tap/OpenObserve". Under (a) that is a decision taken from a sink. Design 20 would have to take its signal from somewhere else, or OpenObserve would be substrate.

**What (a) makes false**, and what must be corrected if the human takes it:

- `architecture.md` §01 rule 2 and `architecture.html`'s "Two stateful deps, ever";
- AGENTS.md's doctrine line "Postgres+NATS only";
- design 07 §2, for `stateful-allowlist.yaml` and "one less PVC";
- NFR-2.

ADR-0002 is not edited. This ADR records the exception.

## D2 — SPIRE's CA keys

- **(a) Recommended.** An entry, class substrate: SVIDs are verified against these keys. This is the upstream-recommended `pvc` persistence, and it is honest about the state.
- (b) The memory key manager with `emptyDir`. The CA rotates on every server restart. Upstream labels this "testing or nested child only". Under D4(a) it does not count as stateful.
- (c) A KMS key manager. The chart offers `awsKMS`, among others. The key then lives in an external, cloud-specific service, so D6 applies, and it conflicts with NFR-3's "no managed-identity assumptions in core".

Whichever option is taken, the chart's SPIRE values must be set to match it. Left at upstream defaults, the first real work of NFR-2's check is to refuse `assayd-server`, under today's test and under D4(a).

## D3 — Matching

- **(a) Recommended.** Match an object exactly on Helm's `# Source:` path, plus its kind and name, rendered with release name `assayd`. An example is `openobserve-standalone/templates/openobserve-statefulset.yaml`, `StatefulSet`, `assayd-openobserve-standalone`. Helm writes the path itself, so the key names the subchart without trusting any label. That keeps the platform's `StatefulSet assayd-nats` apart from the one the OpenObserve HA chart renders under the same name, which `helm template` accepts, exiting 0.
- (b) Match on kind and name only. This cannot tell the two `assayd-nats` apart.
- (c) Match on the `app.kubernetes.io/name` label. This trusts every upstream chart to set that label, and to set it truthfully.
- (d) Match by substring, as today. Not recommended, because it admits `nats-sidecar-cache`.

## D4 — What counts as stateful in a render

- **(a) Recommended. Deny by default.** A rendered object counts as stateful if it is any of these:
  - a pod template with a volume outside this set: `configMap`, `secret`, `projected`, `downwardAPI`, `emptyDir`, `image`, and `csi` with driver `csi.spiffe.io`. The set of pod-bearing kinds is `Pod`, `Deployment`, `ReplicaSet`, `StatefulSet`, `DaemonSet`, `Job` and `CronJob`. Anything outside the set counts, including `persistentVolumeClaim`, `ephemeral`, `hostPath`, `nfs`, `iscsi`, `cephfs`, `rbd`, the cloud-disk sources, and inline `csi` with any other driver;
  - a `StatefulSet` with `volumeClaimTemplates`. A `StatefulSet` is counted by its storage, not by its kind;
  - a `PersistentVolumeClaim` or `PersistentVolume`;
  - an object of a **storage-claiming kind**: `postgresql.cnpg.io` `Cluster`, `agents.x-k8s.io` `Sandbox`, and `extensions.agents.x-k8s.io` `SandboxTemplate`.

  **Exempt `hostPath` mounts are listed exactly**, by source path, kind, name and volume name. The first are SPIRE's: at 0.30.2, one on the agent `DaemonSet` for its socket, and four kubelet mounts on the `spiffe-csi-driver` `DaemonSet`. They carry sockets, not records.
- (b) Keep `StatefulSet` and `PersistentVolumeClaim` only. This leaves CNPG's Postgres invisible.

**What (a) cannot enforce:** it does not catch a storage-claiming kind nobody has listed. A render cannot know what another controller will create.

## D5 — State the operator creates per custom resource

A chart render cannot see this state. Two cases are designed:

- ADR-0018's per-graph FalkorDB;
- design 02 §3.2's per-Agent sandbox scratchpad.

The options:

- (a) Generalize: anything the operator creates for a CR is workload state, outside NFR-2. This opens a hole: a per-Agent cache would need no entry.
- (b) Only ADR-0018's exemption. The scratchpad then needs an entry under D7.
- **(c) Recommended.** Name both cases as workload state outside NFR-2. Any other operator-created storage needs an entry.

**Enforced from this PR, whatever the human decides:** `test/chart/operator_storage_test.go`.

- `TestTheOperatorsRoleGrantsNoStorage` fails when the rendered operator role can create, update or patch a PVC, a PV, a `StatefulSet`, a CNPG `Cluster`, a `Sandbox` or a `SandboxTemplate`. Wildcards count. So the change that binds agent-sandbox for the scratchpad fails it.
- `TestTheOperatorGivesItsPodsNoVolumes` fails on any volume in the operator's non-test Go. Today there are none.
- Each has a positive control. Three mutations each failed the named test:
  - granting `persistentvolumeclaims` in `charts/assayd/files/operator-rules.yaml`;
  - planting a `Volumes:` field in `internal/controller`;
  - making the grant matcher return nothing.
- **What it cannot see:** storage that another controller creates from an object the operator may already write.

## D6 — Bring-your-own and external services

- **(a) Recommended.** Scope NFR-2 to services the platform writes its own records to.
  - A BYO instance fills an entry's role: a BYO Postgres counts as the Postgres entry, and the chart then renders none.
  - An external store of another type that the platform writes to needs an entry. The candidates now are the 6-year archive, OpenObserve HA's S3 (not needed under D1(a)), and CNPG backups.
  - **Excluded by name**: design 11's Connector targets (`system: s3 | postgres | …`), which are the user's systems that the platform reads, and design 13's BYO graph endpoint, which is ADR-0018's workload state.
  - A render cannot see an external service, so only review enforces this, as it does NFR-1's written-justification half.
- (b) External services are outside NFR-2. Not recommended, because requiring an external S3 would then satisfy "only Postgres and NATS".

## D7 — How an entry is added

- **(a) Recommended.** The change that introduces a stateful object does three things:
  1. adds it to the allowlist, with its class and reason;
  2. adds a numbered `## Amendment N` to this ADR, naming the design that calls for it;
  3. records the human's decision in that amendment.

  A test requires each entry to cite an existing heading of this file (`D1`–`D7`, or `Amendment N`). That enforces **where** a decision is recorded, not **that** one was made. Whether a human decided is still review's job.
- (b) Design 07 §2's rule: editing the allowlist in the same PR is enough, and a reviewer judges the reason. This is lighter, and nothing pins the reason to a record.

**Not the human's.** Where the allowlist lives is the author's call. The test change below keeps it in the Go test, beside the check. Design 07's `stateful-allowlist.yaml` is the alternative.

## Consequences

- **Until the human decides, only D5's two tests change anything.** `TestStatefulDependencyAllowlist` and NFR-2 stand as they are. NFR-2 cites this ADR as proposed.
- **The test change, contingent on D1(a), D3(a), D4(a) and D7(a), and not implemented.** It is one change to `test/chart/chart_test.go`, with five parts:
  1. **The matcher and the classifier are tested on fixtures**: a fixture allowlist, fixture documents, and a positive control that must be admitted. A planted `StatefulSet assayd-nats-sidecar-cache`, `Deployment` with an `nfs` volume, `PersistentVolume` and CNPG `Cluster` must each be refused. A planted `emptyDir` must pass, and so must an exempt SPIRE socket. Mutations: restoring `strings.Contains`, or making the matcher refuse everything, each fail a fixture row.
  2. **The render matrix** is the cross product of `-f values-local.yaml` or not, and `gateway.enabled=true` with a `gateway.servingUrl` or not. Without the URL, `templates/operator.yaml` calls `fail`. `tier: plus` is attempted, and only its known refusal is accepted. **What it cannot see:** a toggle nobody adds to the matrix.
  3. **Each entry** carries its class, its reason, its keys under D3(a), and a heading reference checked against this file.
  4. **Every named object must be rendered**, so a renamed subchart cannot leave an entry standing unused. The count of stateful objects read is logged. It prints only under `-v`, because `make chart` runs without it, so the fixtures, not the log, are what show that the check is not vacuous.
  5. **D2 and D6 add entries** (SPIRE's key PVC, and any external store) in whatever form the human picks.
- **Revisit** when the chart first renders a subchart. That is when the vacuous pass ends.
