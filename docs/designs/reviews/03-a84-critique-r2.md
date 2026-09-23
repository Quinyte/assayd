# Design 03 A84 — independent critique, round 2

- **Date**: 2026-09-23.
- **Reviewer**: an independent critic (not the author, not the author's forked self-critiques), following `.claude/skills/critique-design`.
- **Target**: PR #62, branch `design-03-a84-precondition`, head `40fe057`, base `main` `2a8f525`.
- **Verdict**: **REVISE — 0 BLOCKER, 2 MAJOR, 5 MINOR.**
- **Provenance of this file**: the critic returned its report to the coordinating session and committed nothing (reviewers here do not push). The coordinator wrote it to a file verbatim so it could be committed. Local scratch paths the critic cited are omitted, because they name a machine-local directory; each omission is marked.

---

The central correction holds, and so do both §3.3.3 tables including the HELD row — I rebuilt every cell from `authserved.go`/`authtxn.go` and drove HELD on envtest; nothing in the tables is wrong. What sends it back is the §9 D7 record the human will decide from, and a test the design declines to owe on the ground that no falsifier has been measured — I have now measured one (P1, M2).

Committed nothing, pushed nothing, posted nothing. The critic's worktree was created detached at `40fe057` and removed by exact path; the tree was clean before removal and `authserved.go` hashed back to `1afe2562…70c22` (= HEAD) after every mutation. Line numbers are `docs/designs/03-policy-compiler.md` at `40fe057`.

## Claims verified

- **1, tables.** Every cell derived by hand. REFUSED: rank 3 vs 4 goes to the `default` arm, the route leads, ` | AuthPolicyNotAttached: …` is appended, `withholdInOrder` keeps the route message, and `appendGovernance` overwrites the route note because `GovernanceSkipped` is `False` at that point. UNKNOWN with `routeRefused` standing: carry then `raiseIncomplete` compose. **The HELD row was driven on envtest** under ACCEPTED, UNKNOWN (no claim) and UNKNOWN (`routeRefused` standing); the critic's fork drove REFUSED. The held lead ("a claim from an earlier pass is standing, and this pass re-derived nothing…") is identical on every reading and never carries "accepted and SERVING" or "THIS PASS DID NOT READ IT AS ACCEPTED" — matches the code (`default` arm ignores `routeOK`). Fixture caveats: `summarisePolicy` is not generation-gated, so policy spec drift does not make it unknown (`unattachPolicy` is needed); and the HELD row does not mention the errored path (`holdServedJudgement`), which reaches the same lead with no route read.
- **2, pointers.** Grep of the design, README, `CLAUDE.md`, `AGENTS.md`: no remaining unqualified statement of the policy half's route clause as a precondition; "also read the route ACCEPTED" survives only as quoted history. `:834` "every unattached-policy report is permanently hedged" is correctly scoped.
- **3, item 7 mutations** (the critic's fork): M2′ survives the whole committed suite (286 s); the drafted row passes on shipped code and fails under M2′.
- **4, `ObservedGeneration`.** Measured: stored at generation 1, a `Spec.Card.Path` edit to 2, route UNKNOWN with `routeRefused` standing, policy broken → `obsGen=2`, `LastTransitionTime` unchanged. The author's claim is right; see MINOR 1.
- **6, redaction.** The committed file diffed against the coordinator's saved original: exactly two differences — the added "Redaction on commit" header line, and the scan-regex line now marked as redacted. Nothing else changed.
- **7(a)** correct: `withholdInOrder` keeps one message.
- **7(b)** reproduced independently on envtest: raise the claim, strip `assayd.dev/agent-uid`, two passes → `PolicyApplyIncomplete` and `Ready` read `ForeignTrafficPolicy` while `GovernanceSkipped` reads `True/AuthPolicyNotAttached` and never names the foreign policy. A real defect in code on `main`.
- **8, comment-only.** `go/scanner` token streams of `authserved.go`, base vs head: `2176 2176 true`; positive control (`==`→`!=`): `2176 2176 false`. `git diff --stat` touches that one `.go` file only.
- **9, `conditions.md`.** The only change is `authserved.go:796` → `:809` on the `appendGovernance()` line.
- **Gates.** `make test` (envtest 281.5 s) and `make verify` exit 0. Leak and owner-name scans empty. All three commits authored and committed by `Quinyte Engineer <engineer@quinyte.com>`.
- **Status line, README and §11** agree with one another and state "NOT approved" honestly.

## MAJOR

**M1 — D7 (`:1522`) argues against the weakest gate and omits where the route clause came from.**
- *Provenance.* `git show 53cf138:…03-policy-compiler.md | grep -c 'also read the route ACCEPTED'` → 0 (A80, the text the human decided (B1) against on 2026-09-21). At `22b65c2` (A81, PR #52) → 2. The clause was A81's unreviewed addition, not a decided rule; A84 *restores* A80's trigger. D7, §11, the Status line and the README all say "corrected the design to the code" and none names A81.
- *Arguments.* (1) and (3) hold only against M1 (wrap the raise in `if routeOK`). A84's own falsifier M2′ defers only a *first* raise on an UNKNOWN reading, holds any standing claim, and still raises on REFUSED. Against M2′, (1) shrinks to "a first report is deferred" and (3) does not apply. Only (2) — permanent silence on a renamed-`controllerName` cluster — bites, and it is sufficient on its own.
- *Fix:* put M2′ in front of the human with its real cost, scope (1) and (3) to M1, and add the A80→A81 provenance.

**M2 — round 1's BLOCKER cell is still unpinned, and the reason given for not owing it is now false.** `:837` says the tables are "recorded, not owed: no falsifier has been measured for those cells".
- *P1:* in the `clausePartlyValid` arm, `if !routeOK { lead = "…does not attach… THIS PASS DID NOT READ IT AS ACCEPTED: "; consequence = announcedNotClosed; break }`. `go test ./internal/controller/` → ok; full `test/envtest` → `ok 280.843s`. **P1 survives everything.**
- *Why it matters:* it re-enters A83's rule-8 defect — every refused or unknown pass announces a `PartiallyValid` policy as unattached, namespace-wide when the key ConfigMap is the cause.
- *Why a row works:* the unit table tests `clausePartlyValid` only with `routeOK=true`, and every partly-valid envtest fixture calls `acceptRoute` first. A 10-line row kills P1: `refuseRoute` + `partiallyValidPolicy`, assert "but not the whole of it", assert "does not attach" is absent. The critic's fork ran such a scratch row and it passes on shipped code.
- *Fix:* owe it as §8.1 item 10 with P1 as the falsifier, and correct `:837`.

## MINOR

1. **`ObservedGeneration` wording.** No contradiction with the per-condition bullet at `:859`, which states the exception. But the general paragraph after the tables ("keeping its `LastTransitionTime` and its `ObservedGeneration`") is still unqualified, and so is `CLAUDE.md`'s "§3.3.3 asks only that a held report keep its `LastTransitionTime` and `ObservedGeneration`". The code is the defensible side: the composed condition's policy half really was observed at the current generation, and `LastTransitionTime` — what design 10 reads — survives. Add a pointer from the general paragraph to the exception.
2. **The route half's `GovernanceSkipped` note.** The table at `:842` says it is overwritten (measured). `:834` ("gains a note") and §5 `:1395` ("with a note") say otherwise, unqualified. It is the same last-writer-wins overwrite that item 9 classes as a defect, left unclassified here.
3. **Where the HELD row lives.** `:837` says "except on the HELD row", but the HELD row is in the lead table, which does not tabulate `GovernanceSkipped`. The held-policy compositions — where item 9 lives, and where the carry overwrites the route note — appear in no table. The HELD row also omits the errored path.
4. **Code comment `authserved.go:615-617`.** "when the route half raised too" should read "raised or held"; the held case is the table's fourth row, the case the first draft's BLOCKER missed.
5. **Readability and a mislabel.** The §5 cell grew from 15,802 to 17,661 characters; §11 `:1542` quotes 16,221 and 18,080 as "that cell", but those are row lengths. At that size the cell is not readable — move the "read X until A84" history into §11.

## Scratch material

The critic's fork envtest rows, its `make test`/`make verify` logs and its token checker were left in the session scratchpad, which is machine-local and not part of the record. *[Paths omitted on commit.]* The critic's own HELD and generation rows were deleted after running.
