# Design 03 A89 — independent code review, round 1

- **Date**: 2026-09-28.
- **Reviewer**: an independent code reviewer (not the author, not the author's forked self-review), following `.claude/skills/review-code`.
- **Target**: PR #73, branch `d03-d8-r1`, head `9dc0857`, base `58edca3`.
- **Verdict**: **APPROVE — 0 BLOCKER, 0 MAJOR, 3 MINOR.**
- **Provenance of this file**: the reviewer returned its report to the coordinating session and did not commit it (reviewers here do not push). The coordinator wrote it to a file verbatim so that it could be committed; the text below is the reviewer's report as returned, with the worktree path shortened to its repository-relative form.

---

**PR #73 code review, head 9dc0857: APPROVE.** I found no BLOCKER and no MAJOR, and three MINORs are listed below. The design critique of A89's text is a separate gate, and it still has to pass before merge.

**Correctness, checked against what you asked**
- **Counting.** `countKeySource` (`internal/controller/authkeysource.go` ~L247) returns PRESENT on the first ConfigMap whose `data` has any entry. A `data` entry with an empty-string value counts, and the unit test pins that. Entries are counted and never parsed, which is consistent with A86. `binaryOnly` counts ConfigMaps whose entries are all under `binaryData`, because any ConfigMap with a `data` entry has already returned PRESENT. So "N of them hold entries under binaryData" is exact. In agentgateway v1.5.0, `traffic_plugin.go` builds the key set from `cm.Data` only, which confirms the fact the change rests on.
- **Message size.** The message has a fixed shape: at most 5 names and one integer, with nothing appended. The held path is still `holdIncomplete` → `heldMessage(keySourceHeldLead, keySourceHeldMark+note)`, rebuilt from constants on each pass. The stored message is not carried forward, so A81's growth bug cannot come back through this change. A side effect: a held report does not say "binaryData". That is acceptable.
- **Pluralisation.** "1 of them holds" and "2 of them hold" are correct, and both are pinned.
- **Watch on a move between `data` and `binaryData`.** The key-set source (`gatewaywiring.go`, `source.Kind(c, KeySetWatchObject(), EnqueueRequestsFromMapFunc(r.KeySetRequests))`) has no predicate, so the MODIFIED event from an in-place update reaches `KeySetRequests`. I checked this by running it: a temporary manager-driven envtest moved the entries in place from `data` to `binaryData`, then back.
  - The move to `binaryData` raised `ApiKeySourceEmpty`, with the binaryData clause, within the 8 s window and before the 15 s card requeue.
  - The move back cleared it.
  - The test passed in 4.8 s, and I deleted the file afterwards.
  - The committed tests do not cover this at any layer. Envtest calls `reconcileOnce` by hand, and the e2e deletes and recreates the ConfigMap. See MINOR 1.

**Mutations.** I ran these in the worktree against `TestAKeySourceIsCountedNotParsed`, restoring the file from a sha256-verified backup and never with git checkout. All compiled and all were killed:
- M1: count `binaryData` too
- M2: count ConfigMaps (`if true`)
- M3: pass 0 as the `binaryData` count into the message
- M4: `binaryData` vetoes `data`
- M5: the count is always 1

The review-code fork also ran 9/9 unit and 6/6 envtest (case 20 (c)) mutations in a scratch copy, all killed.

**Gates**
- `make test`: pass (envtest 382 s).
- `make verify`: pass (generated files are reproducible and committed).
- CI on head: e2e (k3d) and e2e (kind) pass. The unit/envtest CI job was still pending when I checked.
- I did **not** run `hack/e2e.sh`, because six other k3d clusters are up. CI's k3d run executed the new row: that test went from 10.2 s to 32.9 s.
- Hygiene grep: no hits in the diff or the tree, outside the excepted critique file.
- Author and committer are both Quinyte Engineer <engineer@quinyte.com>.

**MINOR**
1. **`test/e2e/keysource_test.go` ~L80 and ~L101-119: the e2e cannot show which state its `401` comes from.** `writeAPIKeys(binary=true)` deletes and then recreates the key set. Both the deleted state and the binaryData-only state answer 401, so a 401 does not prove the gateway has loaded the binaryData ConfigMap. The comment "the gateway has had it for as long" guesses about two separate caches. It also means no committed test exercises the in-place move through the watch.
   - Fix: update the ConfigMap in place, moving the entries from `data` to `binaryData`. The previous state answers 200, so poll until the first 401, then assert it stays 401.
   - Also: the failure text blames "reads binaryData" for any non-401 code, including curl's `000` and 5xx. It should say that only for 200.
2. **`docs/designs/03-policy-compiler.md` ~L1572, ~L1576 against ~L1606: A89 says every copy states what runs where, and that is false.** D8's (R1) row still says `TestSliceAKeyHeldOnlyInBinaryDataIsNotRead` "fails first on that release, which is what the conformance gate is for". The recommendation still says the conformance case "guards the assumption". CI does not run that suite, and neither passage has an A89 in-place note.
   - Fix: add in-place notes, or narrow A89's Reviews claim.
3. **`docs/install.md:689`: a row this PR rewrote says "the wrong label" is "reported by nothing".** That is false when the mislabelled ConfigMap is the only one: the live list finds no labelled ConfigMap and raises `ApiKeySourceEmpty` (the absent case). It is silent only when another correctly labelled key set holds a `data` entry. This breaks rule 8 (no condition may be "loud and wrong").
   - Fix: say when it is silent.

**Housekeeping.** The worktree `.claude/worktrees/pr73-review-r1` was removed by its exact path, and no envtest processes are left over. Nothing was pushed, commented or merged, and I did not touch `pr73-critique-r1`.
