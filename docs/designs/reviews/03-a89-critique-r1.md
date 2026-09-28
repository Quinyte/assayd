# Design 03 A89 — independent critique, round 1

- **Date**: 2026-09-28.
- **Reviewer**: an independent critic (not the author, not the author's forked self-critique), following `.claude/skills/critique-design`.
- **Target**: PR #73, branch `d03-d8-r1`, head `9dc0857`, base `58edca3`.
- **Verdict**: **REVISE — 0 BLOCKER, 3 MAJOR, 6 MINOR.**
- **Provenance of this file**: the critic returned its report to the coordinating session and did not commit it (reviewers here do not push). The coordinator wrote it to a file verbatim so that it could be committed; the text below is the critic's report as returned, with the worktree path shortened to its repository-relative form.

---

## Critique of design 03 A89 (PR #73, head 9dc0857 against base 58edca3): REVISE, 0 BLOCKER, 3 MAJOR, 6 MINOR

I ran this in the worktree `.claude/worktrees/pr73-critique-r1` at HEAD 9dc0857, confirmed, and removed it by exact path afterwards. `critique-design` ran as a forked pass and I checked its findings by hand. I pushed nothing, commented nothing and wrote no review file.

**What holds:**
- **The code:** `countKeySource` (`internal/controller/authkeysource.go:247-260`) returns PRESENT on the first ConfigMap with any `data` entry. Otherwise it counts the ConfigMaps that hold only `binaryData` and returns EMPTY.
- **The source claim:** agentgateway at v1.4.1, v1.5.0 and `main` builds its key set from `cm.Data` alone.
- **Unit and envtest:** unit and envtest case 20 (c) pass, and the old-rule mutation ("count binaryData too") is killed at both layers.
- **Docs gates:** `make docs` passes, `go test ./test/docs/...` is ok, and the regenerated docs are identical to what is committed.
- **AGENTS.md and CLAUDE.md:** they share 8 paragraphs, each byte-identical by per-paragraph sha256, and no "What…" paragraph differs between them.
- **CI:** the k3d and kind e2e runs passed.

### Answers to your eight questions
1. **Scope (R1):** there is no new reason, condition, gate or admission change. The only additions are the count and the message. The richer message is borderline; see MAJOR 3.
2. **Old rule still stated as current:** I found no copy that still states the old rule, with or without a note. Exceptions:
   - The D8 (R1) cost cell and its recommendation (03:1572, 1576) still name the conformance case as the guard (MINOR 2).
   - Several new notes overstate the fix (MAJOR 2).
   - The remaining mentions are in test code, review history or measurement records, and none states the rule as current.
3. **Approved sentences:** I ran a word-level diff against 58edca3. Only additions land in approved text: A86, §3.3.3, §5, §8.1, D6, A87 and ADR-0034 Amendment 7 lines 134 and 140. The one removal in A87's "What is built" is a word-diff artifact: "parsed)," becomes "parsed; *A89…*)," and nothing is lost. The only text actually removed is placeholder text in unapproved A88 material:
   - "not yet implemented" in the Status line, D8, A88 (3) and A88's "Body changed";
   - the D8 clause "the change to … comes in a separate amendment and code PR";
   - a stray "is" in the Status line.
   D8 is a decision record. The human's quoted answer is kept, but a note would have been safer than rewriting its sentence (MINOR).
4. **Rule 7:** the code matches A89's text, except MINOR 5.
5. **The install.md wrong-label row (`docs/install.md:689`) is false as written.** A ConfigMap without the label is not listed. So if it is the only key set, the list comes back empty, the absent-case message is used, and the Agent reads `ApiKeySourceEmpty`. It is "reported by nothing" only when another correctly labelled ConfigMap with a `data` entry exists. The error predates A89, but A89 rewrote this sentence and kept it.
6. **The hard-coded "agentgateway 1.5.0":** it follows the design's convention of pinning a measured fact to the version it was measured on. Its upkeep is not recorded, and the e2e matches the literal string (MINOR 1).
7. **The approval wording:** "The decision is the human's; A89's text … is not approved and goes through critique" is accurate but does not say what happens after critique. A85 and A86 each changed the approved slice and were approved by the human after a PASS. A87 was a pure implementation record, but A89 reverses an approved "Settled" bullet and adds user-facing text. See MAJOR 3.
8. **AGENTS.md and CLAUDE.md:** byte-identical, as above.

### MAJOR
1. **The e2e row's `401` does not show that agentgateway ignores `binaryData`, yet A89 makes it the guard** (03:1604, AGENTS.md/CLAUDE.md, `reasons.yaml`:97-98).
   - `writeAPIKeys` (`test/e2e/auth_helpers_test.go`) deletes the ConfigMap before recreating it. A gateway that has seen the delete but not the recreate also answers `401`.
   - No `200` is measured just before this step.
   - The comment at `test/e2e/keysource_test.go:103-104` ("the gateway has had it for as long") reasons from what the operator read, not what the gateway read.
   - A88's conformance case needed a canary under `data` to attribute this same `401`.
   - Fix: measure `200` first, then move the entries from `data` to `binaryData` in the same ConfigMap in place, with no delete. Poll until the key gets `401`, then hold it for three probes. The `200` → `401` change is attributable, because a gateway with a stale view would still answer `200`.
2. **"No longer silent" is false for mixed key sets.** The notes are at ADR-0034:140, design 03:1919 and 1969, and `docs/install.md:689` ("entries under `binaryData` instead of `data`… read `ApiKeySourceEmpty`").
   - Any `data` entry in any labelled ConfigMap makes the whole source PRESENT.
   - A88's own measured shape shows it: a canary under `data` beside the admitted key under `binaryData` still reads `Ready=True` while that key gets `401`.
   - Fix: qualify each note. A key source whose every entry is under `binaryData` reads empty. A `binaryData` key beside any `data` entry stays silent, like the existing "no key in the Agent's group" residue. Add the same to `reasons.yaml`.
3. **The message and the approval path go beyond what the human was asked.**
   - The (R3) row (03:1574) priced "its own amendment, critique and case 20 rows, for a message that says more than (R1)'s".
   - A89 ships a message naming `binaryData`, and pays every one of those costs except the new reason.
   - The (R1) cost cell only anticipated the "no entry in data or binaryData" wording changing.
   - A89's argument at 03:1589 is reasonable, but it is the implementer's call on something the human was told belonged to the rejected option.
   - A89 also reverses an approved sentence, and the precedents are that such changes get the human's approval after a PASS.
   - Fix: state that merge waits for the human to approve A89's text. Put one question to the human: keep the message that names `binaryData`, or change the count only and keep A86's plain empty-case message.

### MINOR
1. The hard-coded "agentgateway 1.5.0" in the message (`authkeysource.go:288`) is asserted literally by the e2e. After an `AGW_VERSION` bump, the stale version would still pass. Either assert against `AGW_VERSION` or write "(measured on 1.5.0)", and record the upkeep.
2. The D8 (R1) cost cell (03:1572) and its recommendation (03:1576) still name the conformance case as the guard, with no A89 note, yet A89's Reviews bullet (03:1606) says every copy was corrected.
3. `test/e2e/keysource_test.go:29` still says the test "measures the OPERATOR, not agentgateway", and A87's e2e bullet (03:1626) has no A89 note, but the new row probes the gateway.
4. One mutation survives both unit and envtest: dropping "in one of them or in a new ConfigMap in <ns> labelled <selector>" from the `binaryData` fix. Assert that clause in `TestAKeySourceIsCountedNotParsed`.
5. 03:1589 and 03:1787 give only the plural "`<b>` of them hold", but the code emits "holds" for one, and the tests check "1 of them holds". 03:1589 also says `binaryData` *entries* are counted, when the code counts *ConfigMaps*.
6. The install.md wrong-label error (point 5 above). Separately, the independent code review A89 cites (03:1606) has no file in `docs/designs/reviews/` and no PR comment, so the mutations and source reads it claims cannot be checked.

**design 03 A89: REVISE**, with MAJOR 1–3 to close before merge. MAJOR 3 also needs the human's approval of A89's text.
