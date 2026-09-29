# ADR-0035 — independent critique, round 3

- **Date**: 2026-09-29.
- **Reviewer**: the same independent critic as rounds 1 and 2 (not the author, not the author's forked self-critique), following `.claude/skills/critique-design`.
- **Target**: PR #75, branch `adr-nfr2-stateful-allowlist`, head `d456033`, base `f3f06d1`.
- **Verdict**: **PASS — 0 BLOCKER, 0 MAJOR, 2 MINOR.**
- **Provenance of this file**: the critic returned its report to the coordinating session and did not commit it (reviewers here do not push). The coordinator wrote it to a file verbatim so that it could be committed; the text below is the critic's report as returned.

---

**ADR-0035 round 3 (PR #75, head d456033): PASS — 0 BLOCKER, 0 MAJOR, 2 MINOR.** All four round-2 majors are closed, and so are the four minors that stand; I retract one of my round-2 minors (below). The two new minors are narrow defects in the test code. Neither blocks, and both should be fixed before merge. Apart from those, what remains is the open questions the ADR itself states, which the human has to weigh.

**Gates at head (all pass):**
- `make docs` passes.
- `go test ./test/docs/... ./test/chart/...` passes.
- `make chart` passes.
- The hygiene grep finds nothing in the ADR, both review records, `requirements.md` or the test.
- Status reads "proposed · not decided", and nothing reads as an approval.
- Worktree `.claude/worktrees/pr75-critique-r3` was verified at d456033, left clean, and removed by exact path. Nothing was pushed or commented.

**Review record:** byte-exact. `docs/designs/reviews/0035-critique-r2.md` below its `---` is identical to the report I returned (checked with `diff`).

**Round-2 findings:**
- **M1 (D1(a) and D2(a) contradicted each other): closed.**
  - D1 now defines a class called "key material" (`0035…md:43`), and D1(a) adds its own NFR-2 sentence (`:48`).
  - D2 explains why the keys are not substrate (`:75`). D2(a) states what it depends on (`:77`), and filing the keys as substrate is ruled out (`:80`).
  - It reads consistently across D1–D7 and Consequences item 5 (`:161`).
  - `requirements.md`'s NFR-2 pointer still summarises only substrate versus sink, but it is a summary that points to a proposed draft, so this is not a finding.
- **M2 (the sink rule reached more than design 20): closed.**
  - "Decision" is defined (`:40`), with paging excluded and the cost of reversing that stated.
  - Every consumer in design 10 §4 and §5 is tabled (`:54-60`). I checked the citations: §5 and §7 of design 10, §7 of design 22, §3 of design 20, and design 24's "own database".
  - The alerts row honestly says pages stop when the sink is down.
- **M3 (the volume scan claimed more than it enforced): closed.** It now covers `api/`, reads embedded YAML, matches keys case-insensitively, and lists its blind spots in both the test comment (`operator_storage_test.go:23-29`) and the ADR (`:127-130`).
- **M4 (file-level exemptions): closed.** Exemptions are now per line, a stale exemption fails the test, and `grantExempt` exists.
- **Minors: four closed, one retracted.**
  - Closed: the plus-tier open items (`:31-33`, `:158`), the `assayd/charts/` prefix in the Source path (`:86`), design 16 in the object-store row (`:25`), and the test comment and mutation wording (`:119-126`).
  - **Retracted:** I was wrong to object to "upstream-recommended". spire 0.30.2's `spire-server` values say `persistence.type … Valid options pvc (recommended), hostPath, emptyDir (testing or nested child only)`, and `:18` now quotes that.

**Mutations.** I ran each one myself, backed up and restored with sha256, and never used `git checkout`.

The round-2 cases:
- B2 (unstructured `hostPath` map): **killed**.
- B3′ (JSON `PodSpec` overlay in `card.go`): **killed**.
- B4 (a comment naming a PVC and a `hostPath`): now passes, so the false positive is gone.
- C1 (a whole file blinded by one exemption) is impossible now that exemptions are keyed by line.

Mutations of the test itself:
- Dropping `(?i)`: killed.
- `codeOf` always returning "": killed.
- A stale `volumeExempt` not reported: killed.
- All three are caught by `TestVolumeUsesFindsAPlantedVolume`.

The current tree produces no false positives. `internal/refgen/reasons.yaml` is now scanned and clean, and `testdata/` goldens are skipped.

### MINOR

1. **The comment filter also drops real code** (`operator_storage_test.go:176`). Any Go line beginning with `*` is treated as a comment, including a pointer dereference.
   - The probe `*dst = corev1.PodSpec{Volumes: src.Volumes}`, valid Go and the only line carrying a volume token, **survived**.
   - `*out = …` is the deepcopy idiom: `api/v1alpha1/zz_generated*.go` has 110 lines starting with `*`, and `api/` is now scanned.
   - The practical hole is narrow, because a new volume usually also adds a matched line such as the field declaration or `&in.Volumes`. But `:120` ("it skips comments") and the stated blind spots don't mention it.
   - **Fix:** skip a `*` line only inside a tracked `/* … */` block, or list this in the blind spots.
2. **The scan fails on names that aren't volumes** (`operator_storage_test.go:158`). These fail loudly, so they are the safe direction, but they push people toward exemptions.
   - `volumes := 3` matches `volumes"?\s*:`, and the probe **failed** the test.
   - An error string such as `"hostPath is not supported"` also **failed** it.
   - **Fix:** exclude `:=` (for example `\s*:([^=]|$)`), and say in the ADR that the scan does match inside strings. Also optional: a content-keyed line exemption covers every identical line in the file, while `:120` says "one line". I couldn't build a clean mutation that uses this.

### Open questions for the human (stated in the ADR, not defects)

- **D1:** the classes, including a third class only if D2(a) is taken. Whether paging counts as a decision. Design 20's drift rollback and the undefined "rollout observe" read from the sink.
- **D2:** SPIRE's CA keys — a key-material entry, the memory key manager, or a KMS.
- **D3:** exact matching on `# Source:` path, kind and name.
- **D4:** deny-by-default classification, and the kinds-that-claim-storage list it can't enforce for kinds nobody has listed.
- **D5:** exempting both the FalkorDB and scratchpad cases, versus FalkorDB only, versus generalising.
- **D6:** scoping NFR-2 to services the platform writes its records to.
- **D7:** how an entry is added. The test shows where a decision is recorded, not that one was made.
- **Open rows in the Context table:** Phoenix's storage, OpenFGA's datastore (chart defaults not checked), and Argo's artifact repository.
- **Where the allowlist lives:** marked "not the human's", which is right.

The files under review were `docs/decisions/0035-nfr2-stateful-dependency-allowlist.md`, `test/chart/operator_storage_test.go` and `docs/designs/reviews/0035-critique-r2.md` at PR #75 head `d456033d5a75bfff335c6052953516ca1e9d1aa3`. The worktree has been removed.
