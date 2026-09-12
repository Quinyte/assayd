# Design 03 — scoped check of A66: K2 carried through (2026-09-12)

**Scope.** A66 (`331dc4c`), which answers `reviews/03-a65-check.md`. An independent reviewer read it, not the author. Line numbers are in `docs/designs/03-policy-compiler.md` at `331dc4c`.

## Verdict: **APPROVE — 0 BLOCKER, 0 MAJOR, 7 MINOR.**

## The A65 check's findings

- **MAJOR 1 (abandoning a K2 `Lock`): closed.** 721 says "J2 `Lock` (K2's included)". 723 gives the K2 branch: the refused `Adopt` comes back with `refusedMode` set to the new desired mode and `CompilerUpgradeUnsupported`, and no marker label is claimed. 724 says the Agent never enters `Create` over the existing route. The §5 row at 1235 matches, and case 17's third half (1295) asserts it.
- **MAJOR 2 (a sticky `refusedMode`): closed, by option (a).** 746 says the field tracks the desired mode and a desired `apikey` never overwrites it. The message at 743 has both branches, the §5 row at 1230 agrees, and case 17's fourth half has a mutation.
- **MAJOR 3 (the stranded bullet): closed** at 744, with K2's exception.
- **MINORs 1–7: closed** (687, 690 and row 47; 454 and 742; 408 and 748; 731 and 1238; 83, 746 and ADR-0034; AGENTS.md and CLAUDE.md; README). The schema comment at 448 was missed (new MINOR 3).

## New findings, all MINOR

1. **A crash in a K2 abandonment can leave the route locked while status says it is open** (723–724, 525). The abandonment writes status and then deletes the policy. A crash between the two leaves the `Adopt` record beside an operator-written `<agent>-auth`, which no trigger matches, and which 525 says is never deleted. It is the K2 variant of the gap A59 recorded open for J2 (1374). It fails toward refusal and needs a crash. **Fix**: for both `Lock` kinds, delete the policy before the abandoning status update. The `Lock` stays in the slot with `written` set, so a crash re-runs the abandonment and the delete is idempotent, and the route is open throughout. At least, state the K2 variant in the body.
2. **Abandoning a K2 `Lock` to `oauth` is unclear** (724). "A `Lock`'s Agent keeps serving under its recorded `none`", and the "serves unauthenticated again" message, read as covering K2, which has no recorded mode. "`CompilerUpgradeUnsupported` outranks `AuthInputAbsent`" ranks reasons of two condition types, while 1056 and 1242 say an `Adopt`ed Agent is `Adopt` instead of `PolicyCompileFailed`. **Fix**: scope those sentences to a J2 `Lock`, and say a K2 `Lock` abandoned to `oauth` is `Adopt` with `refusedMode: oauth`, raises no `PolicyCompileFailed`, and does not withhold `Ready`.
3. **The schema comment at 448 describes the old sticky field.** **Fix**: "the last desired mode observed that was not `apikey`".
4. **The message's second branch can fail on a fast toggle** (743). Controller-runtime coalesces queued events, so an `apikey` → `none` → `apikey` made before a reconcile is never observed under `none`. **Fix**: tell the owner to wait until `refusedMode` reads `none` before editing back.
5. **An edit from `oauth` counts, and §1.1 does not mark that as the author's reading** (83, 746). The human's words were `none` → `apikey`, and `oauth` is what most stored Agents carry, so this is the common K2 path. ADR-0034 marks it. **Fix**: mark it at both places.
6. **724 says "`Adopt` cannot fire" beside "a K2 `Lock`'s abandonment enters the refused `Adopt`"**, and 723's "deleted as below" points above.
7. **Nits.** 254 lists `AuthLockPending` among routes the compiler did publish, but a K2 `Lock`'s route was not. 733's "`Adopt`'s action stays: recreate the Agent" omits K2. Case 17 writes `status.auth` for `status.auth.transaction`. An `Adopt`ed Agent with a stored `spec.budget` cannot enter K2 (526), though the message promises the edit works. The Status line's A58 sentences now follow "A66 answers it." (not introduced by A66).

## Verified sound

- **A K2 `Lock` in flight.** The slot holds the `Lock`, so `refusedMode` is gone, and the abandonment writes it again. `Adopt` cannot fire while the `Lock` holds the slot. A crash after `written` re-enters at the write. A missing policy re-enters. A route delete during the `Lock` is re-asserted (731).
- **Tracking makes the message's branches exact.** In a steady `Adopt`, `refusedMode` equals the desired mode unless that mode is `apikey`, so the K2 test is deterministic from stored fields across a restart.
- **A lost status** re-derives `Adopt` from the current desired mode, the stated boundary. The older lost-status gap is no worse.
- **Abandonment returns to the right states.** A K2 `Lock` goes to `Adopt`, never to `mode: none` or `Create`. 525's delete paths cover it, since it holds a positive `written` record and no served policy.
- AGENTS.md and CLAUDE.md are byte-identical. The README agrees with the Status line. Rule 7 holds. No absolute paths or personal names. Project identity. Only additions under `reviews/`.
