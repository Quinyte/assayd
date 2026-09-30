# ADR-0035: NFR-2 has two substrates, one sink and one set of keys; the allowlist matches exactly, counts storage by deny-by-default, and grows only by amendment

- **Status**: **accepted** · 2026-09-29 · **decided by the human on 2026-09-29**, through four questions put to them by the coordinating session (D1; D2; D3 and D4 together; D5, D6 and D7 together), on the third critique's PASS ([`reviews/0035-critique-r3.md`](../designs/reviews/0035-critique-r3.md): 0 BLOCKER, 0 MAJOR, 2 MINOR, both fixed in revision r3). The human took every recommended option: D1(a), D2(a), D3(a), D4(a), D5(c), D6(a) and D7(a). Each heading below quotes the answer. The options not taken are kept as provenance. History: the first critique returned REVISE, 2 BLOCKER, 7 MAJOR, 6 MINOR ([`reviews/0035-critique-r1.md`](../designs/reviews/0035-critique-r1.md)); the second REVISE, 0 BLOCKER, 4 MAJOR, 5 MINOR ([`reviews/0035-critique-r2.md`](../designs/reviews/0035-critique-r2.md)). **The allowlist test the decisions call for is built**, in the follow-up PR the decision owed: `TestStatefulDependencyAllowlist` now matches exactly and counts storage deny by default, and `TestStatefulAllowlistOnFixtures` shows on planted documents that it refuses and admits what it should (Consequences, *Built*). Until that PR it enforced the old substring check.
- **Amendment 1 (2026-09-30)**, decided by the human on 2026-09-30, narrows D7(a): an allowlist entry may cite only `D1`, `D2` or an `Amendment N`, and a `hostPath` exemption only `D4` or an `Amendment N`.
- **D1(a) is an exception to ADR-0002 rule 2** ("Postgres + NATS are the only stateful deps"), not a refinement of it. ADR-0002's Decision is not edited; its Amendment 1 points here.

## Context

NFR-2 (`docs/requirements.md`) says *"Postgres and NATS JetStream are the only stateful dependencies, ever"*. `TestStatefulDependencyAllowlist` (`test/chart/chart_test.go`) enforces something weaker:

- It allows **three** names: `postgres`, `nats` and `openobserve`. It files OpenObserve as "observability sink, not substrate", which comes from design 07 §2 and design 10 §2. The human has approved neither design. ADR-0022 records that an allowlist "with reasons" is CI-enforced, but it names no entries.
- It matches by **substring**, so `nats` admits `nats-sidecar-cache`.
- It reads only `StatefulSet`s and `PersistentVolumeClaim`s, and only in the **default render**.
- It **passes vacuously**, because the chart renders neither kind today.

*Note (2026-09-30): this list describes the test as it stood when the human decided. The follow-up PR replaced it with the one Consequences specifies; see* Built *there.*

Two upstream facts make the requirement false even before anything more is built:

- **CloudNativePG (CNPG), design 07's Postgres, renders no `StatefulSet`.** Its operator creates Pods and PVCs from a `postgresql.cnpg.io` `Cluster` (CNPG docs, via Context7). The current test would never see Postgres.
- **SPIRE, which is core tier (design 07 §3), holds state that is not Postgres.** Rendered on 2026-09-29 with release name `assayd` and `dataStore.sql.databaseType=postgres`, spire 0.30.2 produces `StatefulSet assayd-server` with PVC template `spire-data`. Its `KeyManager` is `disk`, the chart default, with `keys_path: /run/spire/data/keys.json`, so the CA signing keys live on that PVC. `persistence.type: pvc` is the chart's default, and its values file annotates it "(recommended)" and `emptyDir` "testing or nested child only".

What the corpus and the upstream charts say, found by grepping `docs/designs` and `docs/decisions` and by rendering the charts:

| State | Where it lives | Source |
|---|---|---|
| Durable workflows, eval results, audit index, Zitadel, SPIRE registrations | Postgres | ADR-0002 rule 2; ADR-0004; designs 06, 16, 20, 21; design 07 §2 |
| Events, KV directory, receipts, **object store** | NATS JetStream | ADR-0004; `architecture.md`'s substrate line and table; designs 04 (receipt bodies at `obj://`), 05, 14, 16 ("object store (dataset + report artifacts)"), 17 |
| SPIRE CA signing keys | a PVC on `StatefulSet assayd-server` at chart defaults | spire 0.30.2 render. **Design 07 §2's "one less PVC" is false at these defaults**: moving the datastore to Postgres leaves the key PVC in place. |
| Interior agent spans, component logs | OpenObserve **only** | design 10 §3 |
| One FalkorDB per managed graph; a BYO endpoint mode adds none | per-graph workload state | ADR-0018; designs 01 §2, 13 §2 |
| Per-Agent persistent scratchpad | an agent-sandbox `Sandbox`, whose controller creates PVCs from `volumeClaimTemplates` | design 02 §3.2; agent-sandbox docs, via Context7 |
| Candidates for **external** storage | 6-year archive of receipts and bodies; OpenObserve's HA chart (`ZO_S3_PROVIDER: s3` at 1.0.2 defaults); CNPG backups (the `cnpg/cluster` chart ships them disabled, with method `barmanObjectStore` when enabled) | ADR-0014; design 27 §4; chart renders and Context7 |
| **Open, plus tier**: Phoenix's storage | not stated | design 10 §2, design 20 §3 |
| **Open, plus tier**: OpenFGA's datastore | "the platform Postgres (own database)", per the design; whether its chart's defaults agree is not checked, and SPIRE's did not | design 24 §2 |
| **Open, plus tier**: Argo Workflows' artifact repository and workflow archive | not stated anywhere in the corpus | ADR-0004 names Argo, not its storage |
| CRs; design 02 §2's run-namespace `ConfigMap`, which is "not reconstructable by the operator" | the API server | design 02 §2 |

## D1 — The classes, and the entries — DECIDED: (a)

**Decided by the human on 2026-09-29: (a).** Their answer: "(a) Classes by authority (Recommended)". The other options below are kept as provenance; they were not taken.

The classes are defined by **authority**, not by uniqueness.

- A **decision** is a write the platform makes to a platform object: a promotion, a rollback, an admission verdict, or a condition on a CR. **Paging is not a decision**, because an alert asks a human to act. The exclusion was put to the human in the text of the option they chose: D1(a)'s description read, in part, "Paging is not a decision." The list below shows what counting paging would have cost.
- A **substrate** is a store the platform reads to take a decision, or to answer an audit question.
- A **sink** is a store nothing takes a decision or an audit answer from. Losing it loses graphs and pages, never audit (design 10 §2). A sink can hold data found nowhere else, as OpenObserve does for interior spans.
- **Key material** is a store whose contents sign, and are never read to decide. It is a class because D2(a) was taken.
- The **API server** is none of these. It is Kubernetes itself, a given of NFR-3, and outside NFR-2.

The options:

- **(a) Recommended.** Postgres and NATS JetStream are the substrate. OpenObserve is the one sink, bound to the chart **`openobserve-standalone`**. At 1.0.1 that chart renders one `StatefulSet assayd-openobserve-standalone` on local disk. The HA chart, `openobserve` 1.0.2, also renders its own CNPG `Cluster`, its own `StatefulSet assayd-nats`, four more `StatefulSet`s and an S3 store: a second Postgres, a second NATS, and an external service. NFR-2 would then read: "Postgres and NATS JetStream are the only stateful substrate. OpenObserve is the only stateful sink." Under D2(a) it adds: "SPIRE's CA signing keys are the only stateful key material."
- (b) Postgres and NATS only. OpenObserve leaves the chart. If it becomes external, D6(a) still needs an entry for it, so (b) with D6(a) means OpenObserve is either run with no durable storage or not run at all.
- (c) One flat list with no classes. This is cheaper. It drops the rule that keeps a sink from quietly becoming a record.

**What (a) puts in question.** Every consumer that design 10 §4's "Feeds" column and §5 name for the signals "derived at the tap/OpenObserve":

| Consumer | What it does with the signal | Under (a) |
|---|---|---|
| Design 20's drift SLO burn and "goal_met falling" | sets `Degraded` on the Agent, and auto-rolls-back | **in question**: a condition and a rollback are decisions |
| "Rollout observe" (design 10 §4) | not defined by any design; if it reads the signal to promote or hold, it decides | **in question**, and open |
| Shipped alert rules, evaluated in OpenObserve (§5) | page or ticket | out: paging is not a decision. If the sink is down, the pages stop too. Design 10 §7 says the dashboards go blind, and does not mention alerts |
| Budget, perf and swarm panels; ADR-0028 backstop visibility | display | out |
| Loop governance (design 22 §7) | `loop_depth` p95, for a human tuning `maxHops` | out |

So under (a), design 20 and whatever "rollout observe" becomes must take their signal from a substrate, or OpenObserve is substrate. **With (a) decided, both are now in question and owed**: design 20's rollback and `Degraded` condition, and "rollout observe", must each be re-specified before they are built. Paging stays outside "decision", so the shipped alert rules are not in question.

**What (a) made false**, corrected on 2026-09-29 with the decision:

- `architecture.md` §01 rule 2 and `architecture.html`'s "Two stateful deps, ever", and the HTML overview's "The substrate is the two stateful dependencies";
- the `07-what-runs` plate's footer in `docs/diagrams/prompts.md`, annotated;
- `docs/designs/TEMPLATE.md`'s stateful-deps prompt, and `docs/web/information-architecture.md` row 15, annotated;
- AGENTS.md's doctrine line "Postgres+NATS only";
- design 07 §2, for `stateful-allowlist.yaml` and "one less PVC";
- NFR-2.

ADR-0002 is not edited. This ADR records the exception.

## D2 — SPIRE's CA keys — DECIDED: (a)

**Decided by the human on 2026-09-29: (a).** Their answer: "(a) Key-material entry (Recommended)". The other options below are kept as provenance; they were not taken.

The keys do not fit D1's substrate: SVIDs are verified against the trust bundle, which is in the datastore (Postgres), not against the key file. What SPIRE does when the key file is lost is not measured here.

- **(a) Recommended.** An entry in a third class, **key material**, with the NFR-2 wording given under D1(a). This keeps the chart's default `pvc` persistence and names the state honestly. It depends on D1(a) or D1(b), and adds a class to either. Under D1(c) it is simply a fourth entry.
- (b) The memory key manager with `emptyDir`. The CA rotates on every server restart, and the chart's values call `emptyDir` "testing or nested child only". Under D4(a) it is not stateful, and NFR-2 needs no new wording.
- (c) A KMS key manager. The chart offers `awsKMS`, among others. The key then lives in an external, cloud-specific service, so D6 applies, and it conflicts with NFR-3's "no managed-identity assumptions in core".
- **Not an option**: filing the keys as substrate. That would make D1(a)'s "only stateful substrate" false on arrival.

D2(a) was taken, so the chart's SPIRE values must keep `keyManager.disk` and `pvc` persistence, and the allowlist must admit that PVC as key material. Without that entry, the first real work of NFR-2's check is to refuse `assayd-server`, under today's test and under D4(a).

## D3 — Matching — DECIDED: (a)

**Decided by the human on 2026-09-29: (a).** Their answer: "Exact match + deny-by-default (Recommended)", asked together with D4. The other options below are kept as provenance; they were not taken.

- **(a) Recommended.** Match an object exactly on Helm's `# Source:` path, plus its kind and name, rendered with release name `assayd`. An example is `assayd/charts/openobserve-standalone/templates/openobserve-statefulset.yaml`, `StatefulSet`, `assayd-openobserve-standalone`: in the umbrella chart Helm writes the `assayd/charts/` prefix, and that prefix is what separates `assayd/charts/nats/…` from `assayd/charts/openobserve/charts/nats/…`. Helm writes the path itself, so the key names the subchart without trusting any label. That keeps the platform's `StatefulSet assayd-nats` apart from the one the OpenObserve HA chart renders under the same name, which `helm template` accepts, exiting 0.
- (b) Match on kind and name only. This cannot tell the two `assayd-nats` apart.
- (c) Match on the `app.kubernetes.io/name` label. This trusts every upstream chart to set that label, and to set it truthfully.
- (d) Match by substring, as today. Not recommended, because it admits `nats-sidecar-cache`.

## D4 — What counts as stateful in a render — DECIDED: (a)

**Decided by the human on 2026-09-29: (a).** Their answer: "Exact match + deny-by-default (Recommended)", asked together with D3. The other options below are kept as provenance; they were not taken.

- **(a) Recommended. Deny by default.** A rendered object counts as stateful if it is any of these:
  - a pod template with a volume outside this set: `configMap`, `secret`, `projected`, `downwardAPI`, `emptyDir`, `image`, and `csi` with driver `csi.spiffe.io`. The set of pod-bearing kinds is `Pod`, `Deployment`, `ReplicaSet`, `StatefulSet`, `DaemonSet`, `Job` and `CronJob`. Anything outside the set counts, including `persistentVolumeClaim`, `ephemeral`, `hostPath`, `nfs`, `iscsi`, `cephfs`, `rbd`, the cloud-disk sources, and inline `csi` with any other driver;
  - a `StatefulSet` with `volumeClaimTemplates`. A `StatefulSet` is counted by its storage, not by its kind;
  - a `PersistentVolumeClaim` or `PersistentVolume`;
  - an object of a **storage-claiming kind**: `postgresql.cnpg.io` `Cluster`, `agents.x-k8s.io` `Sandbox`, and `extensions.agents.x-k8s.io` `SandboxTemplate`.

  **Exempt `hostPath` mounts are listed exactly**, by source path, kind, name and volume name. The first are SPIRE's: at 0.30.2, one on the agent `DaemonSet` for its socket, and four kubelet mounts on the `spiffe-csi-driver` `DaemonSet`. They carry sockets, not records.
- (b) Keep `StatefulSet` and `PersistentVolumeClaim` only. This leaves CNPG's Postgres invisible.

**What (a) cannot enforce:** it does not catch a storage-claiming kind nobody has listed. A render cannot know what another controller will create.

## D5 — State the operator creates per custom resource — DECIDED: (c)

**Decided by the human on 2026-09-29: (c).** Their answer: "Take all three (Recommended)", asked together with D6 and D7. The other options below are kept as provenance; they were not taken.

A chart render cannot see this state. Two cases are designed:

- ADR-0018's per-graph FalkorDB;
- design 02 §3.2's per-Agent sandbox scratchpad.

The options:

- (a) Generalize: anything the operator creates for a CR is workload state, outside NFR-2. This opens a hole: a per-Agent cache would need no entry.
- (b) Only ADR-0018's exemption. The scratchpad then needs an entry under D7.
- **(c) Recommended.** Name both cases as workload state outside NFR-2. Any other operator-created storage needs an entry.

**Enforced from this PR:** `test/chart/operator_storage_test.go`.

- `TestTheOperatorsRoleGrantsNoStorage` fails when the rendered operator role can create, update or patch a PVC, a PV, a `StatefulSet`, a CNPG `Cluster`, a `Sandbox` or a `SandboxTemplate`. Wildcards count. A grant the human has decided goes in `grantExempt`, keyed by role and kind. Binding the scratchpad under D5(c) will need one. An exemption that matches nothing fails the test.
- `TestTheOperatorGivesItsPodsNoVolumes` scans every non-test file under `internal/`, `cmd/`, `api/` and, if it exists, `pkg/`. That includes embedded YAML but not `testdata/`. It fails on text that gives a pod a volume: a typed `Volumes:` or `corev1.Volume`, any `…VolumeSource`, an unstructured `"volumes"` or `"hostPath"` key, a YAML `volumes:` key, or a PVC. The scan is case-insensitive. It skips `//` comments and `/* … */` blocks, tracked across lines, so a line starting with `*` is skipped only inside a block, and a pointer dereference such as `*dst = corev1.PodSpec{Volumes: …}` is read. A short variable declaration, `volumes := …`, does not match. **The scan does match inside string literals**, on purpose, because that is where a JSON or YAML pod overlay lives; a string that merely mentions `hostPath` fails it too. `volumeExempt` exempts by file and exact line content, so it covers every identical line in that file and never the whole file. An exemption that matches nothing fails.
- **Mutations.** Each was run on 2026-09-29, restored from a sha256-verified backup, and failed the test named:
  - a `persistentvolumeclaims` grant, and a stale `grantExempt` entry, fail `TestTheOperatorsRoleGrantsNoStorage`;
  - a grant matcher that returns nothing fails `TestStorageGrantsFindsWhatItLooksFor`;
  - an unstructured `hostPath` map, a JSON pod overlay in `card.go`, an embedded YAML `volumes:` under `internal/refgen`, a `Volumes []corev1.Volume` field in `api/`, and a line-exempted volume beside an unexempted one in the same file each fail `TestTheOperatorGivesItsPodsNoVolumes`;
  - a comment that names a PVC and a `hostPath` passes it;
  - a comment filter that drops everything fails `TestVolumeUsesFindsAPlantedVolume`;
  - r3's four (the third critique's two minors): skipping every line that starts with `*`, matching `volumes :=`, and dropping block-comment tracking each fail `TestVolumeUsesFindsAPlantedVolume`; the critic's probe, `*dst = corev1.PodSpec{Volumes: src.Volumes}` appended to `card.go`, fails `TestTheOperatorGivesItsPodsNoVolumes`.
- **What they cannot see**, as the test's own comment also states:
  - storage another controller creates from a kind not in the test's list;
  - a volume assembled at run time, whether read from a CR, a `ConfigMap` or the network, or built from strings that split its key;
  - code outside the four roots.

## D6 — Bring-your-own and external services — DECIDED: (a)

**Decided by the human on 2026-09-29: (a).** Their answer: "Take all three (Recommended)", asked together with D5 and D7. The other options below are kept as provenance; they were not taken.

- **(a) Recommended.** Scope NFR-2 to services the platform writes its own records to.
  - A BYO instance fills an entry's role: a BYO Postgres counts as the Postgres entry, and the chart then renders none.
  - An external store of another type that the platform writes to needs an entry. The candidates now are the 6-year archive, OpenObserve HA's S3 (not needed under D1(a)), and CNPG backups.
  - **Excluded by name**: design 11's Connector targets (`system: s3 | postgres | …`), which are the user's systems that the platform reads, and design 13's BYO graph endpoint, which is ADR-0018's workload state.
  - A render cannot see an external service, so only review enforces this, as it does NFR-1's written-justification half.
- (b) External services are outside NFR-2. Not recommended, because requiring an external S3 would then satisfy "only Postgres and NATS".

## D7 — How an entry is added — DECIDED: (a)

**Decided by the human on 2026-09-29: (a).** Their answer: "Take all three (Recommended)", asked together with D5 and D6. The other options below are kept as provenance; they were not taken.

- **(a) Recommended.** The change that introduces a stateful object does three things:
  1. adds it to the allowlist, with its class and reason;
  2. adds a numbered `## Amendment N` to this ADR, naming the design that calls for it;
  3. records the human's decision in that amendment.

  A test requires each entry to cite an existing heading of this file (`D1`–`D7`, or `Amendment N`). That enforces **where** a decision is recorded, not **that** one was made. Whether a human decided is still review's job.

  *Note (2026-09-30): narrowed by [Amendment 1](#amendment-1-2026-09-30--an-entry-cites-d1-d2-or-an-amendment). An entry may now cite only `D1`, `D2` or an `Amendment N`, and an exemption only `D4` or an `Amendment N`. The text above is kept as decided.*
- (b) Design 07 §2's rule: editing the allowlist in the same PR is enough, and a reviewer judges the reason. This is lighter, and nothing pins the reason to a record.

**Not the human's.** Where the allowlist lives is the author's call. The test change below keeps it in the Go test, beside the check. Design 07's `stateful-allowlist.yaml` is the alternative.

## Consequences

- **What changed with the decision.** NFR-2; §01 rule 2 in `architecture.md` and `architecture.html`, their comparison-table "Postgres+NATS only" and the HTML diagram's "the only stateful dependencies" label; AGENTS.md's doctrine line and the same line in `.claude/skills/critique-design/SKILL.md` (CLAUDE.md does not carry it); and design 07 §2, by a note that leaves its bullet standing. ADR-0002 carries Amendment 1, and its Decision is not edited. D5's two tests (`test/chart/operator_storage_test.go`) are already enforced.
- *Note (2026-09-30): the change the next bullet owes is built; see* Built *after it. That bullet is kept as written.*
- **The allowlist test change is decided and OWED, in a follow-up PR.** `TestStatefulDependencyAllowlist` is unchanged in this PR and still enforces the old three-entry substring check, which D3(a) and D4(a) replace. It passes vacuously today, so nothing the chart renders is wrongly admitted in the meantime. The change is one rewrite of `test/chart/chart_test.go`, with five parts:
  1. **The matcher and the classifier are tested on fixtures**: a fixture allowlist, fixture documents, and a positive control that must be admitted. A planted `StatefulSet assayd-nats-sidecar-cache`, `Deployment` with an `nfs` volume, `PersistentVolume` and CNPG `Cluster` must each be refused. A planted `emptyDir` must pass, and so must an exempt SPIRE socket. Mutations: restoring `strings.Contains`, or making the matcher refuse everything, each fail a fixture row.
  2. **The render matrix** is the cross product of `-f values-local.yaml` or not, and `gateway.enabled=true` with a `gateway.servingUrl` or not. Without the URL, `templates/operator.yaml` calls `fail`. `tier: plus` is attempted, and only its known refusal is accepted. Once plus renders, that row fails the test. The change that makes plus render must then add plus to the matrix, and must settle the three open plus-tier rows in Context. **What it cannot see:** a toggle nobody adds to the matrix.
  3. **Each entry** carries its class, its reason, its keys under D3(a), and a heading reference checked against this file.
  4. **Every named object must be rendered**, so a renamed subchart cannot leave an entry standing unused. The count of stateful objects read is logged. It prints only under `-v`, because `make chart` runs without it, so the fixtures, not the log, are what show that the check is not vacuous.
  5. **D2(a) and D6(a) add entries**: SPIRE's key PVC as key material when the SPIRE subchart lands, and any external store the platform writes its records to.
- ***Built*** *(2026-09-30, the follow-up PR). The five parts are in `test/chart/chart_test.go`. `TestStatefulDependencyAllowlist` renders the four rows of the matrix and reads their union. It attempts `tier: plus` on each row, and accepts only a refusal whose output contains "tier: plus is not implemented". It reads every document `helm template` prints on each row, and every file under `crds/` in the chart and its subcharts, which `helm template` does not print. It fails on a stateful object that no entry names exactly. It also fails on an entry or `hostPath` exemption that matches nothing, and on an entry whose class is not one of the three, which gives no reason, or which cites no `## D1`–`## D7` or `## Amendment N` heading of this file. *(Since Amendment 1, an entry may cite only `D1`, `D2` or an `Amendment N`, and an exemption only `D4` or an `Amendment N`.)* `statefulAllowlist` and `hostPathExempt` are both empty, because the chart renders no subchart. An entry for an object that is not rendered fails part 4, so the entries of part 5 arrive with their subcharts.*

  *`TestStatefulAllowlistOnFixtures` plants thirty-four documents, with a fixture allowlist and fixture exemptions. Its first row is the positive control. Every part of an entry's key and of an exemption's key has a row that differs only in that part and must be refused. Some readings the text left open were taken the strict way:*
  - *an exempt `hostPath` is keyed by five parts: the object's `# Source:` path, kind and name, the volume's name, **and** its host path;*
  - *a volume with more than one source counts. A volume with no source does not: the API server defaults it to `emptyDir`, which was measured. Before the 2026-09-30 review this bullet said it counted;*
  - *the first `# Source:` line of a document is the one Helm wrote, so a template cannot claim another's path by writing its own;*
  - *a document with an `items` array, whatever its kind, is read as its items, each under the document's Source. The API machinery reads any such document as a list, and `helm install` creates every item, so a `kind: ConfigMap` carrying `items: [a PVC]` creates the PVC. A list nested in a list is flattened too; Helm's builder refuses that shape, so this is stricter than Helm, not a case Helm installs;*
  - *`crds/` is read directly, in the chart and in every subchart, unpacked or `.tgz`, at any depth, keyed `assayd/crds/<file>` or `assayd/charts/<sub>/crds/<file>`, because `helm install` creates every object there and `helm template` prints them with no Source line, or not at all;*
  - *only `helm template`'s standard output is parsed; its standard error, where a refusal or a warning is written, is carried in the error.*

  *Mutations were run on 2026-09-29 and 2026-09-30, and each was restored from a sha256-verified backup. Each of these fails the fixture test:*
  - *restoring a name-only `strings.Contains` match, or refusing everything;*
  - *dropping the Source path, or the kind, from an entry's key;*
  - *dropping any one of the five parts from an exemption's key, or ignoring exemptions altogether;*
  - *a deny-list of PVC and `hostPath` in place of the allow set, or letting a volume with two sources pass, or counting one with none;*
  - *not counting `PersistentVolume`; not listing CNPG's `Cluster`, `Sandbox` or `SandboxTemplate`; not counting `volumeClaimTemplates`, or counting a `StatefulSet` by its kind;*
  - *dropping `Job`, `ReplicaSet` or `StatefulSet` from the pod-bearing kinds, or admitting any `csi` driver;*
  - *passing every heading, accepting any class, or dropping an exemption's reason-and-heading check;*
  - *dropping the unused-entry or unused-exemption check;*
  - *losing the Source line, or taking its last line rather than its first;*
  - *not flattening a document's items, flattening only kinds that end in `List`, or flattening one level only;*
  - *dropping the check that an entry names its source, kind and name.*

  *In the chart, each of these fails `TestStatefulDependencyAllowlist`:*
  - *a PVC in the default render, under the local profile only, or with the gateway on only;*
  - *an `nfs` `Deployment` at local plus gateway only;*
  - *a `StatefulSet assayd-nats-sidecar-cache`, which the old check admitted;*
  - *a PVC wrapped in a `kind: List`, or in the `items` of a `kind: ConfigMap`;*
  - *a PVC in a file under the chart's `crds/`;*
  - *a `tier: plus` that renders;*
  - *a gateway row without `gateway.servingUrl`;*
  - *an `openobserve-standalone` entry that is not rendered.*

  *Dropping a matrix row passes on today's chart, which renders nothing stateful on any row. With a PVC rendered only at local plus gateway, dropping that row passes too. That is the gap part 2 states.*

  *`TestCRDsDirectoriesAreRead` plants a chart with `crds/` files at two depths, a non-manifest, an unpacked subchart and a packaged one holding a nested subchart. Each of these fails it: reading nothing, not reading packaged or unpacked subcharts, reading `crds/` one level deep only, reading non-manifest files, and keying a subchart without its `charts/` segment. `TestHelmTemplateParsesStandardOutputOnly` runs a fake `helm` that writes a warning to standard error; restoring `CombinedOutput`, or dropping standard error from the error, fails it. In `TestCorePodBudget`, a nine-replica `Deployment` in a `ConfigMap`'s `items` now fails the budget, and so does one in a file under the chart's `crds/`, which the budget now reads too. Both tests read `crds/` through one helper that fails when it finds nothing, since the Agent CRD always ships there, and the budget also fails unless a `CustomResourceDefinition` is among what it counts, which `helm template` never prints; so removing the budget's `crds/` read, or making the helper return nothing, fails it with no plant. A packaged subchart is keyed by the top directory of its `.tgz`, which `helm package` names after the chart; a hand-built tarball with another top directory would get another key, as the test's comment states.*

  *What the test does not see, beyond part 2's gap, is stated in the test's comment:*
  - *a pod template in a kind outside D4(a)'s seven, such as a `ReplicationController`;*
  - *an object a template renders only when `.Capabilities` or `lookup` says the cluster has something, since no `--api-versions` is passed;*
  - *an object a template renders only on an upgrade, through `.Release.IsUpgrade` or `.Release.Revision`, since `helm template` renders a first install.*
- **Owed by the follow-up test PR, beyond the rewrite**: `codeOf` in `operator_storage_test.go` resets raw-string state on every line, so a `/*` inside a multi-line backtick string opens a block that blinds the scan to the lines after it (the record check's probe P8 survived). The follow-up must carry raw-string state across lines, or state the gap in the test and here. *Built (2026-09-30): `codeOf`, a hand-written lexer, is gone. `withoutComments` blanks a Go file's comments with `go/scanner`, the lexer Go's own parser uses, and keeps newlines. Carrying the raw-string state across lines was not enough. The follow-up's review found three more shapes that hid a line from a hand-written lexer: a rune holding a backtick, a rune holding a quote, and a string ending in an escaped backslash, each before a multi-line raw string holding `/*`. `TestVolumeUsesFindsAPlantedVolume` plants all four shapes, plus a `//` line inside a raw string. It also plants a volume after a `//` line, after a multi-line `/* */` block, and after a `*/` on the same line, and asserts each one's line number, so blanking cannot run past a comment's end or eat a newline. Each of these mutations fails it: not blanking comments at all; a textual `/* */` strip that ignores strings; restoring the old `codeOf`; blanking a `//` comment, or a block, to the end of the file; and blanking newlines inside a comment.*
- **Still open, and owed**: design 20's rollback and "rollout observe" (D1); what SPIRE does when its key file is lost (D2); and Context's three plus-tier rows, Phoenix, OpenFGA's datastore and Argo's storage, which the change that makes plus render must settle.
- **Revisit** when the chart first renders a subchart. That is when the vacuous pass ends.

## Amendment 1 (2026-09-30) — an entry cites D1, D2 or an amendment

**Decided by the human on 2026-09-30**, through a question put to them by the coordinating session. Their answer: "Tighten (Recommended)". The option they chose read:

> Entries may cite only D1, D2 or "Amendment N"; exemptions only D4 or "Amendment N". Any new dependency then needs an amendment recording your decision. It costs nothing today, since the allowlist is empty. Recorded as an amendment to ADR-0035.

**What it changes.** D7(a) let an entry cite any heading of this file, so an entry citing `D3` or `D5` passed with no amendment, although neither decided an entry. From this amendment:

- an allowlist entry may cite only `D1` or `D2`, which name the entries they decided (Postgres, NATS JetStream and OpenObserve under D1(a); SPIRE's CA key PVC under D2(a)), or an existing `## Amendment N`;
- a `hostPath` exemption may cite only `D4`, which decided SPIRE's socket mounts, or an existing `## Amendment N`;
- so any other stateful dependency, or any other exemption, needs an amendment here that records the human's decision.

D7(a)'s three steps are unchanged. The test still enforces **where** a decision is recorded, not **that** one was made.

**Enforced by** `TestStatefulAllowlistOnFixtures` in `test/chart/chart_test.go`. It holds an entry citing `D3` and an exemption citing `D1`, and requires both to be refused. It also holds an entry and an exemption citing `Amendment 1`, and requires neither citation to be refused. Mutations, each restored from a sha256-verified backup, fail it: letting an entry cite any heading; letting an exemption cite any heading; and refusing every `Amendment N`.
