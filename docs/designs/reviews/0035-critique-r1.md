# ADR-0035 — independent critique, round 1

- **Date**: 2026-09-29.
- **Reviewer**: an independent critic (not the author, not the author's forked self-critique), following `.claude/skills/critique-design`.
- **Target**: PR #75, branch `adr-nfr2-stateful-allowlist`, head `4fd4452`, base `f3f06d1`.
- **Verdict**: **REVISE — 2 BLOCKER, 7 MAJOR, 6 MINOR.**
- **Provenance of this file**: the critic returned its report to the coordinating session and did not commit it (reviewers here do not push). The coordinator wrote it to a file verbatim so that it could be committed; the text below is the critic's report as returned.

---

**ADR-0035 (PR #75, head 4fd4452): REVISE.** Two blockers and seven majors. The draft should not go to the human yet, because D1 and D4 rest on premises that the corpus and the upstream charts contradict.

**Gates at head:** `make docs` passes, and `go test ./test/docs/...` passes. The hygiene grep finds nothing in the ADR or in `requirements.md`. The Status line reads "proposed", and nothing in the ADR reads as an approval. The worktree at `.claude/worktrees/pr75-critique-r1` has been removed by exact path. Nothing was pushed or commented. The critique rendered the upstream charts (spire 0.30.2, nats 2.15.0, openobserve 1.0.2, openobserve-standalone 1.0.1, cnpg/cluster 0.8.1) with release name `assayd` in its own scratchpad. I checked the key corpus citations myself.

**What holds up:**
- All five Context bullets are accurate: substring match, two kinds only, one render, vacuous today.
- CNPG uses no `StatefulSet`. Its "Storage" docs and FAQ say so (read via Context7).
- The example `StatefulSet assayd-nats` is exactly what nats 2.15.0 renders.
- D4's grep result is correct: no PVC, `volumeClaimTemplates`, `hostPath` or `emptyDir` in `internal/ api/ cmd/ charts/`.
- The residues stated in D3 (line 58) and D5 (line 72) are honest.
- The NFR-2 edit in `requirements.md` is correct and does not pre-empt the decision: it labels ADR-0035 a proposed draft and leaves the text and the test standing.

### BLOCKER

**B1 — D4 misses the one per-Agent storage the corpus already designs** (`0035…md:60-65`)
- Design 02 §3.2 (`02-agent-crd-operator.md:128,130`) specifies `runtime.sandbox` as an agent-sandbox `Sandbox` with a revision-independent "persistent scratchpad", reattached read-write. I verified those lines. Upstream `Sandbox` carries `VolumeClaimTemplates`.
- So D4(a)'s "per-Agent cache with a PVC" is not hypothetical, and D4(b) would silently make design 02's scratchpad need an amendment and a human decision.
- D4(b)'s envtest looks only for PVC, `StatefulSet` and `hostPath`. It cannot see a `Sandbox`, because agent-sandbox's controller creates the PVC and envtest does not run that controller.
- D3's list of kinds that claim storage also omits `Sandbox`.
- D4(b)'s gate is a "must" placed on a future change, which rule 7 forbids.
- **Fix:**
  - Name the scratchpad in D4 and in Consequences.
  - Add `agents.x-k8s.io` `Sandbox` to the storage-claiming kinds.
  - Turn line 65's grep into a test now, so it fails on the first change that creates storage.

**B2 — SPIRE breaks D1(a) at upstream defaults** (`:21`, `:36`, `:51`)
- SPIRE is core tier (design 07 §3). The table cites design 07 §2's "one less PVC", and that claim is false at upstream defaults.
- spire 0.30.2 renders `StatefulSet assayd-server` with PVC template `spire-data` even with `dataStore.sql.databaseType=postgres`. `keyManager.disk` is on by default, so the CA signing keys live on that PVC.
- Removing it takes a memory key manager plus emptyDir persistence, which upstream labels test-only. The result is still a `StatefulSet`, which D3(a) counts whatever its storage.
- So "Nothing else in the chart holds state" is false the day SPIRE lands.
- Today's test would also refuse `assayd-server`. The first real work NFR-2's check does would be refusing SPIRE.
- **Fix:** give the human SPIRE's CA-key state as an explicit option: an entry and class, the memory key manager with CA rotation on restart, or a KMS (which falls under D5). Correct the table row.

### MAJOR

**M1 — D1(a)'s definitions contradict the corpus** (`:34-35`)
- "A sink holds only copies" is false for OpenObserve. Design 10 §3 (`10-observability-pack.md:25,30`, verified) sends interior agent spans and component logs only to OpenObserve.
- Line 34's rule for substrate ("state nothing else can reconstruct") therefore makes OpenObserve substrate.
- The same rule makes the API server substrate. Design 02 §2 has a run-namespace `ConfigMap` that is "not reconstructable by the operator".
- **Fix:** define the classes by authority, not uniqueness: no decision or audit answer is ever taken from a sink (design 10 §2: "loses graphs, never audit"). Exclude the API server explicitly, as a given of NFR-3.

**M2 — The OpenObserve chart is unbound, and D2(a)'s `(kind, name)` is not unique** (`:23`, `:44`)
- OpenObserve's HA chart 1.0.2 renders its own CNPG `Cluster`, its own `StatefulSet assayd-nats` with a PVC, four more StatefulSets with PVCs, and `ZO_S3_PROVIDER: s3`.
- An umbrella chart with both renders two `StatefulSet assayd-nats`, and `helm template` exits 0. One entry would admit both.
- **Fix:**
  - Key entries on Helm's `# Source:` path plus kind and name. Helm writes that path itself, which also answers D2(b)'s concern about trusting upstream labels.
  - Have D1 name the OpenObserve chart (HA or standalone).

**M3 — The object-store row is wrong, and D5 targets the wrong designs** (`:25`, `:72`, `:97`)
- `architecture.md:41,55,168` (verified) puts the object store in JetStream.
- The table misses designs 04 (P1 receipt bodies at `obj://`), 14 and 17.
- The real external-store candidates are:
  - ADR-0014 and design 27's 6-year archive "to object storage";
  - OpenObserve HA's S3;
  - CNPG backups, whose default is `barmanObjectStore`.
- **Fix:** correct the row, and retarget D5 and Consequences at those candidates.

**M4 — D3(a) lists what to catch, so it misses some storage and over-counts plumbing** (`:51-55`)
- It misses `nfs`, `iscsi`, `cephfs`, `rbd`, the cloud-disk sources, inline `csi` volumes, and the `PersistentVolume` kind.
- It counts SPIRE's hostPath sockets: the agent DaemonSet, and four kubelet mounts on the CSI driver. Each would need a D6 amendment and a human decision while holding no record.
- It counts a storage-less `StatefulSet` and gives no reason.
- **Fix:**
  - Deny by default: allow `configMap`, `secret`, `projected`, `downwardAPI`, `emptyDir`, `image`, and `csi` with driver `csi.spiffe.io`. Count everything else.
  - Add `PersistentVolume`.
  - List exempt `hostPath` mounts exactly.
  - Count a `StatefulSet` by its storage, not its kind.

**M5 — D6(a) is oversold** (`:81-82`, `:93`)
- D6(b) is criticised because "no test sees whether a human decided". That is equally true of (a), whose test sees only a heading the author writes.
- Consequence 4 says to "cite D1". D1 is bold text, not a heading, so the specified check would fail on the ADR's own three entries.
- **Fix:** say that (a) enforces where a decision is recorded, not that it was made. Make D1–D6 headings, or have the check accept `**Dn —`.

**M6 — The specified test cannot discriminate** (`:90-94`)
- With the entries empty (consequence 5), mutation 1 ("restore `Contains`") changes nothing, because there is nothing to compare against, and a matcher that refuses everything also passes.
- Adding a real entry breaks consequence 5's rule that every named object must be rendered.
- **Fix:** test the matcher against a fixture allowlist and fixture documents, with a positive control that must be admitted. Treat the planted CNPG `Cluster` the same way.

**M7 — D5(a) is scoped too widely, and its premise is false** (`:27`, `:69`)
- "External service of a type no entry names needs an entry" catches:
  - design 11's Connector targets (`system: s3 | postgres`);
  - design 13's BYO graph endpoint. That one contradicts D4(b), which keeps ADR-0018's exemption.
- "The only BYO arm is `idp: byo`" is false: `13-graphiti-adapter.md:15` has "BYO endpoint mode" (verified), and ADR-0018 says "BYO mode unaffected".
- D1(b) together with D5(a) is contradictory: an external OpenObserve would still need an entry.
- **Fix:** scope D5 to "a service the platform writes its own records to", exclude Connectors and BYO graph endpoints by name, and state which options depend on each other.

### MINOR

- **`:1` title.** It states the recommendation as if decided, and "every object that can hold state" contradicts lines 58 and 72. Retitle it as a proposal.
- **`:46`, `:70`.** "Rejected:" labels on items that are the human's to decide. Write "Not recommended, because…".
- **`:92` render matrix.**
  - `gateway.enabled=true` fails to render without `gateway.servingUrl` (the `fail` in `templates/operator.yaml:91`).
  - The local profile lives in `values-local.yaml`.
  - Nothing discovers new toggles, and single-toggle unions miss combinations.
  - Tier `plus` "stays out until it renders" has no mechanism. Attempt the render, and accept only the known refusal.
- **`:94` vacuity log.** It is invisible in CI: `Makefile:130` runs the chart tests without `-v`, so a passing test's `t.Logf` never prints.
- **Missing doc updates.** Consequences omit the docs D1(a) would falsify: `architecture.md:41` and `architecture.html:437` ("Two stateful deps, ever"), and design 07 §2's `stateful-allowlist.yaml` and "one less PVC".
- **`:4` "Refines".** Turning "the only stateful deps" into substrate plus a sink is an exception to the human's rule 2, not a refinement. The Status and Refines lines should say so.

The file under review, now removed with the worktree, was `docs/decisions/0035-nfr2-stateful-dependency-allowlist.md` at PR #75 head `4fd4452ec7143af0e64c66a7e2c0b56b5a4cad1b`.
