# ADR-0035 — independent critique, round 2

- **Date**: 2026-09-29.
- **Reviewer**: the same independent critic as round 1 (not the author, not the author's forked self-critique), following `.claude/skills/critique-design`.
- **Target**: PR #75, branch `adr-nfr2-stateful-allowlist`, head `363fef0`, base `f3f06d1`.
- **Verdict**: **REVISE — 0 BLOCKER, 4 MAJOR, 5 MINOR.**
- **Provenance of this file**: the critic returned its report to the coordinating session and did not commit it (reviewers here do not push). The coordinator wrote it to a file verbatim so that it could be committed; the text below is the critic's report as returned.

---

**ADR-0035 round 2 (PR #75, head 363fef0): REVISE — 0 BLOCKER, 4 MAJOR, 5 MINOR.** Round 1's 2 blockers and 7 majors are closed in substance. What remains is new: two of the recommendations contradict each other (D1(a) and D2(a)), D1's sink rule collides with more of the corpus than just design 20, and the new volume-scan test claims more than it enforces. The ADR's text doesn't read as decided, and it still needs another round before it goes to the human.

**Gates at head (all pass):**
- `make docs` passes.
- `go test ./test/docs/... ./test/chart/...` passes.
- `make chart` passes (lint plus chart tests).
- The hygiene grep finds nothing in the ADR, the review record, `requirements.md` or the new test.
- Status reads "proposed · not decided". There are no "Rejected" labels, and "approved" or "decided" appear only in negations.
- Worktree `.claude/worktrees/pr75-critique-r2` was verified at 363fef0, left clean, and removed by exact path. Nothing was pushed or commented.

**1. Review record:** byte-exact. `docs/designs/reviews/0035-critique-r1.md` below its `---` is identical to the report I returned (checked with `diff`, trailing newline included).

**2. Round-1 findings.** All 15 are closed:
- B1: scratchpad named in D5, `Sandbox` and `SandboxTemplate` added to the kinds, two tests added.
- B2: new D2 heading.
- M1: classes now defined by authority, and the API server is excluded.
- M2: D1 binds `openobserve-standalone`, and D3 keys on `# Source:`. I rendered standalone 1.0.1: one `StatefulSet` on local disk, `ZO_LOCAL_MODE: "true"`, minio disabled.
- M3: table rows corrected. ADR-0014 and design 27 §4 are verified as the archive citations.
- M4: D4(a) now denies by default.
- M5: D7(a) now says it enforces "where, not that". Headings D1–D7 exist.
- M6: fixture-based test specified.
- M7: D6 scoped, with Connectors and bring-your-own graph endpoints excluded by name.
- All six minors are closed, including the Refines line (now "exception to"), the render matrix, the `-v` note and the doc-update list (which now includes AGENTS.md).
- Residues from B2, M1, M2 and M3 appear below as new findings.

**3. The new tests.** I ran every mutation myself, backed up and restored each file with a sha256 check, and never used `git checkout`.

Killed (8), plus 1 false positive:
- A1: a `persistentvolumeclaims` create grant.
- A2: a wildcard `*/*/*` rule.
- A3: `sandboxes` patch.
- A4: `statefulsets` added to the existing `apps` rule.
- A5: `storageGrants` returning nil (killed by the positive control).
- B1: a `Volumes:` hostPath added to the agent `PodSpec`.
- B5: `_test.go` files counted (positive control).
- B6: `volumeUses` returning nil (positive control).
- B4 (false positive): a comment that merely mentions `PersistentVolumeClaim` fails the test.

Survived:
- B2: an unstructured pod map with a `hostPath` key.
- B3′: a `PodSpec` overlay decoded from JSON with a `hostPath` volume (valid Go, in `card.go`).
- B7: dropping the `cmd` root. It is vacuous today, so this is harmless.
- C1: one file-level `volumeExempt` entry followed by a hostPath in the same file.

The two I wrote invalid first (they didn't compile) were rewritten, and the valid versions are the ones counted. The RBAC test discriminates well. The volume scan is brittle in both directions (see MAJOR 3 and 4).

### MAJOR

1. **D1(a) and D2(a) contradict each other** (ADR `:43` and `:60`). D1(a) rewrites NFR-2 as "Postgres and NATS JetStream are the only stateful substrate". D2(a), also recommended, adds SPIRE's CA-key PVC as class **substrate**, so taking both recommendations makes the new NFR-2 false on arrival. D1's own definition of substrate (`:37`, "a store the platform reads to take a decision or answer an audit question") doesn't fit signing keys either: SVIDs are verified against the bundle held in the datastore, not against the key PVC. **Fix:** either add a third class for key material, with its own NFR-2 wording, or make D2(a) an explicit exception to D1(a). Say which recommendations depend on each other.

2. **D1's sink rule collides with more than design 20** (`:38`, `:47`).
   - Design 10 §4 (`10-observability-pack.md:39`) says `task_success_rate` feeds "drift SLO burn (20), **rollout observe**". The ADR doesn't name the rollout consumer.
   - Design 10 also ships **alert rules** evaluated in OpenObserve (`:10`, `:40`, `:47`). If an alert counts as a "decision", every shipped alert breaks `:38`.
   - `:47` names only design 20.
   - **Fix:** define "decision" (a change to platform objects, such as rollback, promotion or admission; paging excluded or included), list every consumer from the §4 "Feeds" column, and say which of them (a) puts in question.

3. **The volume scan claims more than it enforces** (ADR `:102`, "fails on any volume in the operator's non-test Go"; `operator_storage_test.go:114`, `:151`). It's a token scan of `.go` files under `internal/` and `cmd/`. Mutations B2 (an unstructured `"hostPath"` map; the operator already builds unstructured objects in `authforeign.go` and `authkeysource.go`) and B3′ (a `PodSpec` decoded from JSON) both survive. It also doesn't read `api/`, a future `pkg/`, or `//go:embed` YAML. That breaks rule 7. **Fix:** state these blind spots in `:107` and the test comment, or extend the scan to lowercase `"volumes"`/`hostPath` keys, `api/`, and non-`.go` files under the roots.

4. **`volumeExempt` is keyed by file, so one exemption blinds a whole file** (`operator_storage_test.go:112-116`, `:129`). The comment says the first allowed `configMap` volume is added "by file". Mutation C1 shows that exempting `agent_controller.go`, the only file that builds a pod, lets a hostPath through. The false positive in B4 (a comment mentioning a PVC) pushes people toward exactly that exemption. **Fix:** exempt by file, line content or volume name, not by whole file, and make an exemption that matches nothing fail the test.

### MINOR

- **`:135` plus tier.** "Only its known refusal is accepted" doesn't say what happens once plus renders: does the test fail, or join the union? Also, the plus-tier state isn't inventoried for the human:
  - Phoenix (design 10 `:15`, design 20 `:23`);
  - OpenFGA's datastore (design 24 `:14`, `:51`);
  - Argo's artifact store (ADR-0004).
  - Add them to the table as open items.
- **`:68` Source path.** In an umbrella chart Helm writes `assayd/charts/openobserve-standalone/templates/openobserve-statefulset.yaml` (measured). The example drops `assayd/charts/`, and that prefix is exactly what separates `assayd/charts/nats` from `…/openobserve/charts/nats`.
- **`:25` and `:30` design 16.** Design 16's "object store (dataset + report artifacts)" (`16-evalsuite.md:595`) disappeared from the table and is now unclassified.
- **Test comment and `:103`.**
  - The test comment (`operator_storage_test.go:19-20`) says "before widening the exemption lists", but there is no RBAC exemption list. Under the recommended D5(c), binding the scratchpad will need one.
  - `:103` says "three mutations each failed the named test", but the matcher-returns-nothing mutation fails `TestStorageGrantsFindsWhatItLooksFor`, not the named test.
- **`:60` wording.** D2(a) calls `pvc` persistence "upstream-recommended". The chart makes it the default (`persistence.type: pvc`); it doesn't recommend it. Also, the ADR is 139 lines against the adr skill's "~15". ADR-0034 is precedent for a long options record, but it should be compressed once decided.

The file under review was `docs/decisions/0035-nfr2-stateful-dependency-allowlist.md` (plus `test/chart/operator_storage_test.go` and `docs/designs/reviews/0035-critique-r1.md`) at PR #75 head `363fef06866990db5aece99bad0ad485acda9178`. The worktree has been removed.
