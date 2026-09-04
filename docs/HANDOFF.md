# Handoff — plume, 2026-09-03 (end of the A42 session)

Written at the end of the session that implemented design 02 A42. Everything
here was verified by running it, not recalled. Read this, then `AGENTS.md`,
then the two design headers.

## The one-line state

**A42, the operator-owned run namespace, is IMPLEMENTED and proven on a real
cluster.** The env-source bypass design 02 §3.2 tracked since A20 is closed:
`make e2e` shows a principal with `create`/`delete` on ConfigMaps in the
Agent's namespace can delete a ConfigMap there and cannot delete or recreate
the revision's copy where it now lives. **Design 03 (the policy compiler) has
still not started and is still `RE-OPENED, do not implement`.**

## What is implemented, as opposed to written

| Amendment | What it does |
|---|---|
| A20 | revision identity covers the resolved **content** of every env source |
| A21 | `runtime.image` must be a digest-pinned OCI reference (CEL) |
| A35 | a revision reads its own **immutable copy** |
| A37 | the full digest is the revision identity; the 10-char name is a name |
| **A42 / A60 / A61** | workloads and copies live in `plume-run-<ns>`; the operator proves it created that namespace by a binding record (nonce before binding, namespace UID after); a Terminating/Deleting handler tears it down when its last Agent goes; Pod Security labels, ResourceQuota and LimitRange are mirrored; the chart ships two admission policies reserving the `plume.dev` namespace labels to the operator, and the operator fail-closes without them |
| A50 | the digest reaches `evalStatus` and `cards[]` |
| A53 | typed LLM endpoint identities |
| A56 | revision material is collected with its revision and with the Agent |
| A57 | the delete authority is the **name**, not a label |

Everything else in design 02 A1–A61 is design. `EnvSourceProtectionUnavailable`
is **no longer raised** (A61); the type stays in the vocabulary so a stale one
from an older operator is cleared on upgrade.

## What this session did, in order

1. **Cross-design pass, 3 and 4 of 4** (commit `5043394`): design 07 A5 and
   design 26 A1, plus design 02 A60, design 06 A3, design 24 A1, design 27 A1
   and design 03 A45. Three same-family critiques and one cross-family Codex
   review before commit; all findings applied; every review is a file under
   `docs/designs/reviews/02-a60-*`. The Codex pass found what the same-family
   passes had accepted — the labels SPIRE and the Gateway act on were never
   bound to the binding authority — and that is why the admission policies
   exist.
2. **A42 implementation** (this commit): `internal/controller/runnamespace.go`,
   the reconciler wiring, the chart, envtest, e2e. A 16-entry mutation ledger,
   all KILLED, is in design 02 §12 A61.
3. **Independent code review** (`reviews/02-a61-code-review.md`, 2 BLOCKER,
   9 MAJOR, 12 MINOR — every one applied). It found what the ledger had not
   named: the new condition never cleared; the crash-gap recovery wedged on
   its own Terminating namespace; row 4 from `Creating` wrote a record the
   decoder refuses; two workers could race rows 6/7; the UID was compared
   against the informer cache; the label policy missed two subresources and
   its params ConfigMap was a cluster-wide denial waiting to happen; a
   bare-name `parentRef` bypassed the route policy; a pre-A42 upgrade leaked
   copies. Eight of its mutations survived; each now has a test and all
   thirteen re-run mutations are KILLED. Self-review caught none of this.

## What happened 2026-09-04

Three things, in order, each committed separately.

1. **ADR-0029 and ADR-0026 Amendment 1** record the run namespace and the three
   things design 26 A1 changed about hard mode. The quota question was the
   user's and they chose the sentence over the controller: compute quotas are
   **per-namespace ceilings**, and `QuotasEnforced` says so.
2. **Design 02's body was consolidated** — 61 folded amendments rewritten into
   one self-contained body that carries no amendment numbers of its own. The
   inventory that drove it (`reviews/02-consolidation-inventory.md`) counted
   **149 findings across 488 lines**, 92 of them pure sedimentation.
3. **Three critique rounds on the consolidated body**, all in
   `reviews/02-consolidation-critique.md`: 3 BLOCKER / 9 MAJOR / 15 MINOR, then
   0 / 5 / 8, then 0 / 1 / 4. Every finding of all three is applied.

**Design 02 is NOT approved.** No critique of it has ever returned PASS, and
the status line and README now say "critique pending" rather than claiming
otherwise — the previous status flip to "approved" was itself a round-1
blocker, made in the same commit that deleted the sentence saying no round had
passed.

**Two code defects came out of documenting honestly**, both now fixed and
mutation-checked:

- the **leaf walker had no cycle guard** while the design said it did. A
  self-recursive type hung until killed, which is an INVALID mutation rather
  than a killed one — CI would time out instead of naming the field.
- **`RevisionMaterialUnavailable` could never clear**: set by the operator, in
  neither the owned nor the sticky set, so a recovered Agent reported
  `Ready=True` beside it forever. The test now asserts over *every* condition
  the operator solely writes, because this defect has appeared on three types.

## Decisions taken 2026-09-04

- **Tenant compute quotas are per-namespace ceilings** (design 26 A1, ADR-0026
  Amendment 1): the user chose the sentence over the controller.
- **ADR-0029** records A42/A60/A61; **ADR-0026 Amendment 1** records the
  compiler's one tenant-side write, the quota decision and
  `GatesTenantAdminBypassable`.

## The receipt exporter was measured, and it overturned three claims

`docs/research/agentgateway-otlp-attributes-2026-09.md` (primary sources, tag
`v1.4.1`, re-verify by 2026-10-15) answers what design 04 A4 asserted without
checking. Three things this corpus believed were **false**:

- **`hop.gatewayInstance` has no producer** — it has four, all emitted by
  default (`k8s.pod.name`, `service.instance.id`, `k8s.pod.ip`,
  `k8s.node.name`). `k8s.pod.name` is the stable one; `service.instance.id`
  embeds the Pod IP and moves on restart.
- **Nothing can attribute a receipt to a route** — `route` is emitted by
  default as `<ns>/<name>`, and design 03's names carry `-<rev>`, so it names
  the *revision*. Design 03 A49's witness is buildable now; only the drain
  allowance is still owed. `hop.route` is added to the envelope.
- **`hop.type` is derived from a stamped backend class** — no such stamping
  exists. There is a default `protocol` enum with no `kg_query`, so a KG and a
  tool backend both report `mcp`.

Two real gaps remain, and they are narrower and sharper than the old text:

- **Azure `deploymentName` is not emitted anywhere.** Two deployments on one
  endpoint differing only in deployment are one identity in receipts — which is
  exactly the identity design 02 A24 exists to keep apart. Anything keying
  spend, drift or fallback on the full tuple cannot do so for that pair.
- **No namespace attribution at all.** Every namespace on the wire is the
  gateway's or, under A42, the run namespace. The audit index has no `ns`
  column either, so two same-named Agents in different namespaces already share
  a budget row. Three options are laid out; one must be chosen.

## Design 03: five amendments that diagnose correctly and are NOT finished

`A46`–`A50` answer Codex r8's BLOCKERs 10–13 and MAJOR 6. **Three critique rounds
returned REVISE — 6, then 5, then 5 blockers** (`reviews/03-a46-a50-critique.md`),
and each round found the previous fix's defect *one step over*: round 1 "one
field or one design over", round 2 "one tense, one row, one document over",
round 3 "one clause over". The reviewer's own conclusion, reached with fresh
context and unprompted: **patching further is the wrong unit of work; design 03
needs the consolidation design 02 took at A62.** One rule is currently stated in
five places with no two agreeing, and the status line now says so.

What is sound and should be kept whatever happens next:

- **the diagnoses.** A46 (the record sealed an absence and froze it), A47 (an
  8 KiB cap on a producer's data), A48 (a tenant's shared limit was an
  Agent-scoped input, frozen N times), A49 (`Withdraw` proved convergence and
  claimed enforcement), A50 (a witness over a field with no producer).
- **the cross-design corrections they forced**, each checked against the
  producing document: design 04 A5 (`hop.endpoint` and `hop.gatewayInstance`
  have no producer at all, and `ns` attribution has no source and no index
  column), design 26 A2 (`TenantPolicyIntent` is one sentence in one table
  cell), design 20 A7, design 23 §3, architecture §05.
- **design 02 gained `status.revisions[]`** in its schema and a §5 row; it had
  been written to by design 03 and absent from its own owner.

What is not finished: the amendment text. Known-open blockers are listed in
round 3 — the seal's empty-list ambiguity, `identity` as a `Binding` breaking
A33's fleet-wide `CapabilityUnavailable`, the `-inputs` object failing design
02's three-gate deletion authority, `sealedBindings` non-injective for two
versions of one graph, and the seal firing after the canary begins.

## Still open

- **Design 02 §5's first table is the list of what is stated and unenforced** —
  sixteen rows, including the three admission guarantees that are not
  CEL-expressible, the NetworkPolicy, `plume logs`, SPIRE, the `Sandbox` API,
  the scratchpad, events/metrics/alerts, and "≥1 gate required in prod", which
  nothing requires: an Agent declaring no gate promotes with `GatesSkipped`.
  Read that table before believing any guarantee this design states.
- **The tool-name uniqueness rule is owed to design 11 §11**, which now records
  it; ADR-0027 has retracted its "enforced at admission" claim. Four documents
  depended on a rule no design wrote.

- **Design 02 is 61 amendments deep and none has passed review.** The rate at
  which rounds close N findings and open ~2 did not fall this session — three
  same-family rounds on A60 each found something the previous had introduced.
  Consolidating design 02 into one body was offered earlier and not chosen.
  The cross-family review is what converged it; it is worth running one on
  every amendment set from now on rather than after three same-family rounds.

## Also open

- **Codex a60 review, items not closed by this commit** (all enterprise /
  hard-mode, recorded as owed in the designs they belong to): the hard-mode
  host mapping and deletion lifecycle (design 03 A45), the vCluster SPIRE
  premise (design 06 A3, spike owed), the trust domain in design 24's subjects
  (design 24 A1 written, sweep of literals owed), design 27 consuming
  `GatesTenantAdminBypassable` (design 27 A1 written, fixture owed).
- **Real-cluster cases envtest cannot reach**: operator restart mid-protocol, a
  paused delete racing a new Agent, a chosen name collision. envtest has no
  namespace controller, so deletions never complete there; these need the e2e.
- **NetworkPolicy** (design 07 A5.4) lands with the gateway; the Sigstore
  opt-in stamp (A5.6) with the verifier; `plume logs` with design 08.
- **r8 blockers 10–13, majors 3, 6** are design 03's and are the next thing.
- **Cross-family review availability**: Codex accepted this session's request
  framed as a defensive review of our own unreleased repository. Keep that
  framing; see `reviews/README-review-availability.md`.

## Running tests — and the trap

```
make test      # 7 layers — the pre-commit gate; ran before every commit
make race      # internal/... api/...
make verify    # generation reproducible AND committed (fails on ANY uncommitted file)
make e2e                  # REAL k3d cluster — creates plume-local
make conformance-cluster  # REAL k3d cluster — creates plume-conformance
```

`make e2e` ran green on the final tree (three times this session, the last
after the code-review fixes); `make conformance-cluster` ran green once, before
those fixes — it imports nothing from the operator packages, so they cannot
have changed its result, but that is an argument and not a run. **Colima must be running** (`colima start`); it was not, and
the first e2e attempt failed on the Docker socket with an exit code the
wrapper reported as 0 — read the log, never the exit code. `make verify`
fails whenever the tree has uncommitted files; that is its "committed" check,
not a generation problem.

## Rules this session paid for

1. **When an amendment claims something about another design, read that
   design first.** Every blocker in the a60 rounds was a claim that failed
   against the producing text — A59's SPIFFE template did not even parse.
2. **A critique finds what the last fix introduced.** r2 found a hole in r1's
   fix, r3 in r2's. Read the new text as an attacker, not as the author.
3. **A mutation that does not compile is INVALID.** Four of sixteen were, on
   the first run, for unreachable code and unused variables; each was redone.
4. **The first test of a path finds the defect the design argument missed.**
   The handler excluded the reconciling Agent from its own confirming list.
   And the ledger only pins what it names: the independent review's eight
   surviving mutations were all on code the ledger had not listed. Write the
   ledger from the design's rows AND from every branch in the code.
5. `cp` for backups, never `git checkout`. Stage explicit paths. Read
   `git status --porcelain` first — another Claude session and the Codex
   reviewer share this worktree.
