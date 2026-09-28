# Design 03 A89 — independent critique, round 2

- **Date**: 2026-09-28.
- **Reviewer**: the same independent critic as round 1 (not the author, not the author's forked self-critique), following `.claude/skills/critique-design`.
- **Target**: PR #73, branch `d03-d8-r1`, head `61fe56b`, base `58edca3`.
- **Verdict**: **PASS — 0 BLOCKER, 0 MAJOR, 1 MINOR.**
- **Provenance of this file**: the critic returned its report to the coordinating session and did not commit it (reviewers here do not push). The coordinator wrote it to a file verbatim so that it could be committed; the text below is the critic's report as returned.

---

## Round-2 critique of design 03 A89 (PR #73, head 61fe56b against base 58edca3): PASS, 0 BLOCKER, 0 MAJOR, 1 MINOR

All three MAJORs and all six MINORs from round 1 are closed, and so are the code review's three MINORs, which overlap them. The PASS has two conditions:
- **The k3d e2e is still pending.** When I checked, CI at 61fe56b showed `e2e (k3d)`, `e2e (kind)` and the build/unit/envtest job all pending. The rewritten k3d row is the evidence behind MAJOR 1 and has not run yet. Merge needs it green.
- **Merge also needs the human's approval of A89's text.** A89 now says this itself.

I ran this in `.claude/worktrees/pr73-critique-r2` at HEAD 61fe56b, confirmed, and removed it by exact path afterwards. I pushed nothing and commented nothing.

**Gates:**
- `make docs` passes, and so does `go test ./test/docs/...`.
- The unit test `TestAKeySource*` passes.
- `test/e2e` compiles.
- The worktree stayed clean.
- AGENTS.md and CLAUDE.md share 8 paragraphs, each byte-identical by per-paragraph sha256, and no "What…" paragraph differs.

### The points you asked about
- **MAJOR 1 (the e2e's `200` → `401` is attributable): closed.**
  - The permitted key gets `200` through the gateway first (`askThroughGateway "keysrc-before"`, `test/e2e/keysource_test.go` ~L90).
  - `moveAPIKeys` (`test/e2e/auth_helpers_test.go` ~L80-106) then does get-modify-`Update` on the same ConfigMap under `RetryOnConflict`, as the key admin. It moves the same bytes from `data` to `binaryData` and never deletes.
  - The test then waits for the `ApiKeySourceEmpty` message and the Ready reason, polls to the first `401` within a minute, and holds `401` for three more probes 2 s apart.
  - Finally it moves the entries back in place and requires `Available` and `200`.
  - The `401` is attributable. With no delete, a gateway on the old snapshot answers `200`. The `401` can only come from the new snapshot, whose key set is empty because `binaryData` is not read.
  - Only a `200` is blamed on agentgateway reading `binaryData`; `000` and `5xx` get messages of their own. The old delete-and-recreate helper `writeAPIKeys` is gone.
- **MAJOR 2 (mixed key sets): closed.** I grepped every A89 note outside A89 itself. Each place that claims `binaryData` keys are now reported also says that a `binaryData` key beside any `data` entry stays silent:
  - ADR-0034:140;
  - design 03 at 1409 (§5), 1551 (D6 K1), 1568 (D8), 1843 ("Settled"), 1935 and 1985;
  - A89's "What changes" and "does not do";
  - `docs/install.md:190`, `:688` and `:689`;
  - `reasons.yaml:77-79`;
  - the shared paragraph in AGENTS.md and CLAUDE.md.

  The Status line (03:3), the README row and the research note state only the case where every entry is under `binaryData`, which is accurate and not an overclaim.
- **MAJOR 3 (the human's answer and the approval path): closed.**
  - The Status line (03:3), A89's header (~03:1584), its Reviews bullet for MAJOR 3 and `README.md:11` all record the human's 2026-09-28 answer, "Keep the binaryData message (Recommended)".
  - Each also says merge waits for the human's approval of A89's text after a critique PASS, on the precedent of A85 and A86, and that the approval is not recorded as given.
- **The review records:** `docs/designs/reviews/03-a89-critique-r1.md` is byte-identical to the report I returned. I compared everything after the provenance block's `---` in Python, and the provenance block is the only addition. I cannot check `03-a89-code-review-r1.md` byte for byte, because I never had the code reviewer's output. Its header matches the same provenance form, and its content matches what A89 cites.
- **D8: restored.** Its sentence at 03:1568, including "It is **not yet implemented** … stays unreported.", is character-identical to 58edca3. The implementation appears only as an appended italic *(A89: …)* note, which also states the mixed-set residue.
- **The diff against 58edca3:**
  - I compared character by character, line by line, for design 03. ADR-0034 has 0 removals.
  - In design 03, removed characters appear on five lines only:
    - 3 (Status);
    - 1409 and 1506 (inside A88's own italic notes, where "counts"/"does" became "counted"/"did" and "not yet"/"still" were removed);
    - 1587 and 1593 (A88's §11 text).
  - All of those are unapproved A88 text, or placeholders written for this amendment. Every other changed line (895, 1551, 1568, 1572, 1576, 1595, 1603, 1713, 1764, 1804, 1853, 1896, 1946, by 58edca3 numbering) has 0 characters removed, so approved text is only added to.
- **The `ASSAYD_E2E_AGW_VERSION` export: wired correctly.**
  - `hack/e2e.sh:505` exports `ASSAYD_E2E_AGW_VERSION="${AGW_VERSION}"`, which is the bare `1.5.0` form from line 158 and matches the message text.
  - The export sits in the k3d-with-gateway block beside `ASSAYD_E2E_GATEWAY_NS`, in the same shell that runs `go test` at line 585.
  - The test calls `requireGateway` first, so a run without the gateway skips before the read. It fails loudly if the variable is unset and builds the expected text as "agentgateway " + version + " does not read".
  - A89 "does not do" records the upkeep on a version bump, and `authkeysource.go:284-287` carries a comment saying the same.

### Other round-1 items
- **MINOR 1 (hard-coded version):** closed, as above.
- **MINOR 2 (conformance case named as the guard):** closed. There are notes on D8's (R1) cost cell (03:1572) and its recommendation (03:1576).
- **MINOR 3 (e2e described as operator-only):** closed. The test's doc comment now says the second half measures both the operator and the gateway, and A87's e2e bullet has a note (03:1642).
- **MINOR 4 (surviving mutation):** closed. The unit test now asserts "in one of them or in a new ConfigMap in ns labelled a=b", and the mutation table's seventh row records it.
- **MINOR 5 (plural and what is counted):** closed. The spec now gives "1 of them holds" for one and says `<b>` counts ConfigMaps, not entries (03:1590 and 1803).
- **MINOR 6 (install.md wrong label, unrecorded review):**
  - Closed. `install.md:689` now says a wrongly labelled key set that is the only one reads `ApiKeySourceEmpty`, and that it is reported by nothing only beside a correctly labelled set with a `data` entry. That matches the code.
  - The code review now has a record file.

### MINOR (new)
1. **03:~1605: "The review ran ten mutations of its own, from a scratch copy" has no identifiable source.** It sits beside two reviews, neither of which gives ten. The independent code review record (`03-a89-code-review-r1.md:23-30`) reports five in the worktree, plus its fork's 9 unit and 6 envtest mutations. The author's forked self-review has no record. Name which review ran them, and give a count that can be checked against a record, or drop the number.

**design 03 A89: PASS.** It still needs the pending k3d e2e to go green at 61fe56b and the human's approval of A89's text.
