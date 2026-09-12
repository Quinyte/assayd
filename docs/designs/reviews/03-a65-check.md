# Design 03 — scoped check of A65: the approval and K2 (2026-09-12)

**Scope.** A65 (`326a12f`), which writes in the human's approval of the first slice and K2 (ADR-0034 Amendment 4), and folds in the findings of `reviews/03-a64-check.md`. An independent reviewer read it, not the author. Line numbers are in `docs/designs/03-policy-compiler.md` at `326a12f`.

## Verdict: **REQUEST_CHANGES — 0 BLOCKER, 3 MAJOR, 7 MINOR.**

All three majors come from K2 not being carried into text written before it. Each is a sentence-level fix plus one half of case 17.

## MAJOR

1. **The abandonment rule contradicts K2 when a K2 `Lock` is abandoned** (720–723, §5 row 1234, against 744). 744 says a K2 `Lock` is "J2's `Lock` in every respect", and that an edit away from `apikey` abandons it, after which "the next reconcile is a refused `Adopt` again". The abandonment rule it points to was written for a `Lock` with a recorded mode, and every branch is false here. Status returns to "`mode: none`", but a K2 Agent has no recorded mode. The route "keeps the no-auth marker label", which the shipped emitter never wrote. `GovernanceSkipped` "returns to `AuthOptedOut`", where K2 needs `CompilerUpgradeUnsupported`. "`Adopt` cannot fire" is the opposite of what K2 needs. "A never-served Agent enters `Create`" invites a `Create` of `none` over the existing route. An implementer has to guess what the abandoning update writes. **Fix**: a K2 branch at 722–723 and row 1234. The abandoning update records the refused `Adopt` with `refusedMode` equal to the new desired mode, sets `CompilerUpgradeUnsupported`, and makes no marker claim. Extend case 17's third half.
2. **A sticky `refusedMode` contradicts the human's words, the author's boundary, and the `Adopt` message** (744, 742, 448, row 1229). An Agent first refused with spec `apikey` keeps `refusedMode: apikey` for good, so an owner who then edits `apikey` → `none` → `apikey` never triggers K2. That owner made exactly the edit the human named, after the refusal. The message tells every `Adopt`ed owner that editing to `apikey` locks the route, which is false for this group (rule 8). **Fix**: (a) set `refusedMode` to the desired mode on every reconcile where it is not `apikey`, so any observed move to `apikey` counts, and add a toggle half to case 17; or (b) keep it sticky, say toggles do not count, and drop the K2 sentence from the message. (a) matches K2 as the human worded it. (b) narrows K2 and needs the human.
3. **A bullet that K2 falsifies was left behind** (745). A65's K2 paragraph split the "The compiler then:" list. Its fourth bullet now sits detached and says no spec edit emits a policy or records a `mode`, "stated so that nobody 'fixes' it". That tells the implementer not to build K2. **Fix**: move it back above 744, with "except K2's edit to `apikey`, below".

## MINOR

1. 687 says `Lock` "is entered in two cases". K2 is a third. §1.1's row 47 lists two as well.
2. 454 and 741 say a refused `Adopt` records only `{kind: Adopt, stage: Refused}`. It now records `refusedMode` too.
3. 408 (`Refused`'s exit is "a human recreating the Agent") and 747 ("flip to `False` once the Agent is recreated") need "or K2's edit".
4. 730 and row 1237 give the reason a J2 `Lock`'s route is re-asserted as "its recorded mode is `none`". For a K2 `Lock` the reason is that no mode is recorded.
5. §1.1 (83) and 744 state the "already `apikey`" boundary flatly, inside paragraphs headed "decided by the human". Each should say it is the author's reading.
6. AGENTS.md and CLAUDE.md quote design 03's header as "not approved, and do not implement". Line 3 now says "not approved, and must not be implemented".
7. The README row still ends "**Do not implement**", which reads as a flat ban beside "first slice approved". Scope it to the rest.

## An older gap, not introduced by A65

If `status.auth` is lost after any `-auth` write (a `Create` or a K2 `Lock` past `written`), the route is published and an operator-emitted `<agent>-auth` exists with no record. `Adopt` does not fire, because a policy exists, and no other kind's trigger matches. K2 adds one more way to reach that state.

## Verified sound

- The `Adopt` trigger: a K2 `Lock` in the slot is a `Lock` transaction, so `Adopt` cannot fire mid-`Lock`. After a crash the `Lock` re-enters by 463.
- The re-creation test (454): with no recorded mode, `targetMode` never equals it, so a K2 `Lock` is not a re-creation and can be abandoned.
- The route re-create trigger (730) needs a recorded mode, so it is not due for a K2 Agent.
- `ProbingBefore` and `beforeRevision` work on the shipped route, which has one `backendRef`, and `status.cards[]` is recorded. Removing the marker at `Served` is a no-op on a route that never had it.
- The deadline is set on first entering `ProbingBefore`, so the J2 row applies.
- On the success path `GovernanceSkipped` goes `CompilerUpgradeUnsupported` → `AuthLockPending` → `Governed` or `AuthVerifiedOnOneReplica`.
- "Recorded afresh" cannot record `apikey`, because abandonment happens only when the desired mode is not `apikey`.
- A lost status re-derives `Adopt` with the current desired mode, which is the restore case the boundary names.
- The A64-check fixes are in place (730, rows 1237 and 1248; 667 and 708; the Status line).
- The approval text in the Status line, §1.1, README, ADR-0034 Amendment 4, AGENTS.md and CLAUDE.md says only §1.1's first slice is approved. The AGENTS.md and CLAUDE.md changes are byte-identical, and nothing claims to be implemented.
- Hygiene: no absolute paths or personal names; the project identity; under `reviews/`, only `03-a64-check.md` was added.
