# Design 03 A86 — independent critique, round 4 (a86c4)

- **Target**: PR #69 at `610cc59`, on `main` `22badf1`. I reviewed it in the worktree `.claude/worktrees/a86-critique-r4`, detached at `610cc59`.
- **Scope**: what changed since `3484e09`, then D6 read once more as a whole, as the human will read it. The change is docs only: `git diff --stat 22badf1..610cc59 -- internal cmd api test charts` is empty. I did not push, comment or merge.
- **Verdict**: **PASS — 0 BLOCKER, 0 MAJOR, 2 MINOR.**
  - The two MINORs are one-phrase wording fixes in the D6 table. Neither changes the decision or tilts it toward the recommendation.
  - They can be made in place without another review round. The findings below give the exact text.
- **Redaction**: the pattern of the owner-name scan is not reproduced here, because it spells a personal name. It is marked *[pattern redacted]*.

## Gates
- **`make docs`**: exit 0.
- **`make test`**: exit 0. fmt, vet, unit, docs, conformance, envtest (334s), chart and release all passed.
- **Worktree**: afterwards `git status` matched the snapshot taken before the run, and `git diff --quiet 610cc59` was true.
- **Path scan** (`/Users/`, `/private/`, `/home/`) over `git diff 22badf1..610cc59`: 3 hits. Each is a committed review record's own sentence naming the scan's patterns. Benign.
- **Owner-name scan** *[pattern redacted]*, case-insensitive: 0 hits.
- **Authorship**: all four commits (`3ddfb34`, `94bcb1e`, `3484e09`, `610cc59`) have author and committer `Quinyte Engineer <engineer@quinyte.com>`.
- **Round-3 record**: `reviews/03-a86-critique-r3.md` matches my round-3 record verbatim, ignoring trailing whitespace (the `diff` is empty).

## The round-3 findings — all closed
- **MAJOR 1. CLOSED.** The K1 cell and step 3 now say "every condition K1 writes is true". They list the five silent residues every option shares, each of which leaves `Ready=True` while callers get `401` or `403`:
  - a failing `LIST`;
  - an out-of-band selector;
  - every entry rejected;
  - no key in the Agent's group;
  - keys held where agentgateway does not read them.

  K1 is also stated to add "no state beyond what every reporting option adds (`keySourceEmpty`)".
- **MAJOR 2. CLOSED.**
  - H2 is split into (i) and (ii).
  - W1 is described as narrowing (ii) only, and only for an Agent already `Served`, with L1 given as the reason no credited probe has ever been followed by `Ready=False`.
  - K1 is "the first exception to sentence (i)" on every `Create` and `Lock`. K6 is the first exception on re-creations and missing-policy `Lock`s.
  - Keeping (i) is stated, in the K2 cell and in step 2, as K2's one real advantage.
  - "W1-sized" is gone. The amendment is named: "a key source that selects no key".
  - I re-checked this against ADR-0034 line 38 and Amendment 5, and it is accurate.
- **The MINORs. CLOSED.**
  - My `system:discovery` measurement is cited, with the caveat for a hardened cluster.
  - The schema read has its own failure policy: an error means "cannot store", whatever was answered before. The detector is cited for expiry only, and the reason is given: its opposite failure policy is the round-2 defect.
  - Row (p) stubs the schema. One run returns a schema without the field and one returns an error, with the key source never PRESENT. Its two mutations are one edit each ("ignore the schema answer"; "resolve a schema error to 'can store'"), and each kills its run.
  - Row (m) now says openly that a schema mutation survives it, and why.
  - The note that the read-back cannot see a flag never written `true` is added. The read-back's in-process "cannot store" and how long it lasts are stated.

## D6 read as a whole
- **The framing question is fair.** "Should a probe that passed still mean `Ready=True` when the operator can see that no key can authenticate?" is the real K1-versus-K2 difference, now that paging (identical), NFR-8 (both meet it) and the residues (shared) are stated evenly.
- **Each option's H2 cell matches ADR-0034's text:**
  - K1 contradicts (i) on the probe's pass, and (ii) after a later loss.
  - K2 keeps both sentences.
  - K6 contradicts (i) on re-creations and missing-policy `Lock`s only, and not on a first `Create`, J2 `Lock` or K2 `Lock`, whose status records no key.
  - K3 is as K1.
- **K6's costs and residues are all stated:**
  - the latch on entries that authenticate nothing;
  - lost status and restore;
  - the schema read and its failure policy;
  - the design-10 exclusion it depends on.
- **The recommendation says both K1 and K2 are defensible**, and that neither silences the outage.

## MINOR
1. **The K1 cell's "Against" says "`Degraded` is withheld on the very `Served` pass whose probe passed"** (design 03 :1551). It is `Ready` that is withheld, and `Degraded` that is raised. The sentence has been this way since `3484e09`, and I missed it in round 3. **Fix**: "`Ready` is withheld, and `Degraded` raised, on the very `Served` pass …".
2. **The K2 cell's "For it" still says "No new state"** (:1552). K2 raises `ApiKeySourceEmpty` and adds `keySourceEmpty` exactly as K1 does; :1544 lists both as shared. Round 3's MAJOR 1 made K1's cell say "no state beyond what every reporting option adds". K2 needs the same words, or the two cells state one fact two ways. The error favours K2, the option not recommended, so it does not tilt the recommendation. **Fix**: "No state beyond what every reporting option adds".
