# Design 03 — scoped check of A64, and review of PR #25 (2026-09-12)

**Scope.** A64 (`42d37cb`), which folds in the three MINORs of `reviews/03-a63-critique.md` (PASS on the first slice), and the hygiene of PR #25 as a whole. An independent reviewer read it, not the author. Line numbers are in `docs/designs/03-policy-compiler.md` at `42d37cb`.

## Verdict: **APPROVE — 0 BLOCKER, 0 MAJOR, 4 MINOR, 1 NIT.**

## A64 against the three MINORs

- **MINOR 1 (the trigger's guard): closed.** 03:728 takes the critique's text almost word for word. The slot must be empty or hold a missing-policy `Lock`. A `Create` already in the slot is not recorded again, and its deadline stands. A J2 `Lock` keeps the slot. It adds the name-and-UID reading, and a foreign route is a `routeCollision`. 03:1233 carries the same words.
- **MINOR 2 (the unpinned write order): closed, by the "demote" option.** 03:728 says the order is a preference that no test pins. The one body sentence that still claimed status-first (03:1233) is removed. Case 16's crash half still pins the state trigger.
- **MINOR 3 (§5's out-of-band row): closed.** 03:1244 sends a route with no `backendRefs`, for an Agent with a recorded `mode`, to the re-create.

**The guard agrees with the rest of §3.3.3 and §5.** "Slot empty" is well defined, because `transaction?` is absent in steady state (03:426). A `Create` already in the slot carries a recorded `mode`, so it is a re-creation (03:452), it is never abandoned (03:718), it re-enters by 03:461, and it keeps its deadline outcome (03:710). A refused `Adopt` has no `mode`, so the trigger cannot fire on it. The slot rule agrees with the guard at 03:728 and at 03:1233.

## New findings

1. **MINOR — the J2 `Lock` branch is worded against 03:1244.** 03:728 makes the re-create due only when the slot is empty or holds a missing-policy `Lock`, and then says a J2 `Lock`'s "route is re-created published", which is a re-create outside the trigger, with no `Create`. 03:1244 says a stripped route for any Agent with a recorded `mode` is the re-create, and a J2 `Lock`'s recorded mode is `none`. For a route that still exists, "re-created published" can only mean putting its `backendRefs` back, which is a re-assert. The outcome is the same either way, because the route is published at once. **Fix**: say so at 03:1244 and at 03:728.
2. **NIT — 03:665 against "its deadline stands".** 03:665 says `deadline` "is set on entering the first stage", and re-entry at `PreparingRoute` also enters the first stage. **Fix**: "set on *first* entering".
3. **MINOR (rule 7) — 03:24 claims a check that did not yet exist.** "A check scoped to A64 has read it" was written in the A64 commit, before any check, and nothing under `reviews/` recorded one. **Fix**: record this check, or reword.

## PR hygiene: all six pass

- (a) `03-a60`, `03-a61`, `03-a62` and `03-a63-critique.md` are each added by exactly one commit. No file under `reviews/` is modified or deleted.
- (b) The `AGENTS.md` and `CLAUDE.md` changes are byte-identical.
- (c) The README row agrees with 03:3.
- (d) No absolute filesystem path and no personal name appears in any added line.
- (e) All nine commits are authored and committed as the project identity.
- (f) Neither 03:3 nor 03:24 claims the slice is approved.

## Older errors, not introduced by this PR

4. **MINOR — 03:3 says "A59 has not been critiqued"**, while citing `reviews/03-a59-critique.md`.
5. **MINOR — 03:3 says the body "carries no amendment numbers of its own"**, while 03:728 and 03:1244 carry them.
