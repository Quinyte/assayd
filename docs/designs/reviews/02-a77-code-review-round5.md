# Design 02 A77: round-5 confirmation of PR #54 at `70fda62`

**Scope:** `git diff c673668..70fda62` only.

**Verdict: APPROVE.** There is no BLOCKER and no MAJOR. Three MINORs stand; none of them blocks the merge.

## Gates

- `make test` exits 0.
- `make race` exits 0.
- `make verify` exits 0.
- `make e2e` was not run.
- The authorship of `70fda62` is `Quinyte Engineer <engineer@quinyte.com>` as both author and committer.
- The path and name scan of the patch is clean.

## Mutations

Each mutation had to pass `go vet` first. Each ran against the envtest suite and the `internal/controller` unit tests, and each file was restored and checked by SHA-256 (11 of 11).

| Mutation | Result | Notes |
|---|---|---|
| S01 to S06 (the author's) | all killed | |
| T1b: the first attempt is sent with an empty `resourceVersion`, so the write is unconditional and would clobber | killed | by both new pins |
| T2: the pass's copy of the Agent keeps its stale `resourceVersion` after a successful early write | killed | by 12 design 03 auth-transaction tests, so the success path's `resourceVersion` handoff is load-bearing and pinned |
| T3: the Conflict path refreshes the pass's copy, so the final write overwrites the concurrent write | killed | by `TestTheEarlyRecordWriteDoesNotClobberAConcurrentStatusWrite` |
| T4: the retry writes the record but drops `serviceReplacedRevision/At` | **SURVIVED** | see MINOR 1 |
| T1, my first form of T1b | discarded as an equivalent mutant under the test's timing | It read the live `resourceVersion` before the injected concurrent write, so it still conflicted |

## The attacks

- **Clobbering a concurrent write.** The first attempt carries the `resourceVersion` the pass read. Only `writeStatus` ever assigns `agent.Status`, so the first attempt sends exactly what is stored, plus the record. The retry re-reads live and applies only the record and the bound.
  - T1b and T3 are killed.
  - Conditions are covered by the pin. `activeRevision` and the auth transaction are covered by the same mechanism, because the retry touches no other field.
- **A wrong key on the retry.** The key is the `(rev, digest)` of the Service that was actually created. That is a true fact whatever the live spec now says, and the key is pruned with its revision.
  - Nit: the retry does not check `live.UID == agent.UID`. Because reconciles are serial per key, a delete and recreate of the Agent inside the pass needs a forced finalizer removal.
- **A pass that fails after the early write.** The record and the bound state only true facts, which are a create and its time. Nothing reads the record as proof of promotion, and `vouched` reads the active and candidate revisions.
- **A hot loop or a lost update.**
  - After a Conflict, the final write conflicts once, the pass backs off, and the next pass sees the record and makes no second create. It converges.
  - The early write runs only on a create or a replace. `TestReconcileIsIdempotent` passes.
- **The `observedGeneration` seed is legitimate, not vacuous.** It only makes the final write non-empty, so the injected Conflict has something to refuse. Without the early write the record dies with that write, which is why S01, S02 and S03 are killed by it.

## Wording and the other code

- **MINOR 6 is correct.** Records are keyed by revision and digest, and S06 is killed by the unit test.
- **MINOR 4 is correct.** Planted in design 02 and restored by hash:

  | Planted shape | Expected | Result |
  |---|---|---|
  | `~~~` fence containing pipes | ok | ok |
  | Cut-off last row | FAIL | FAIL at `:1150` |
  | Top-level row indented 3 spaces | ok | ok |
  | Table in a list item, row at another indent | FAIL | FAIL at `:1151` |

- **MINOR 2 is true.** The type documentation and §5 say "created or adopted".
- **MINOR 3 is true.** The `RevisionServiceNotRecorded` entry names the lost-write cause and the in-place-repair exit. The repair exit is exercised by the extended `TestAServiceWithNoRecordIsNeverDeleted`.
- **MINOR 5 is true.** The chart grants `agents/status` only to the operator, and the new sentence says so.

## MINOR (standing)

1. **The replace bound on the retry path is unpinned.**
   - **Mutation:** T4 (the retry drops `serviceReplacedRevision/At`) survives the suite.
   - **Consequence:** a Conflict on the early write loses the cooldown, and a re-patch then gets an immediate second replace.
   - **Fix:** in `TestTheEarlyRecordWriteDoesNotClobberAConcurrentStatusWrite`, assert that `ServiceReplacedRevision == rev` and that `ServiceReplacedAt` is set.
2. **A CRD that predates `status.revisionServices` silently prunes the record.** This dates from `2b66c3b` and I missed it in round 4.
   - **Cause:** `helm upgrade` never updates `crds/`, and a structural schema drops the unknown field with no error.
   - **Effect:** on such an install, the early write and the final write both succeed and store nothing. Every headless Service is then `RevisionServiceNotRecorded`, and the message's remedy, "the operator creates its own in its place and records it", is false (rule 8).
   - **Precedent:** `authtxn.go:1122-1156` already detects exactly this for `status.auth`.
   - **Fix:** detect it the same way, or state it in §5 and in the message.
   - **Evidence:** reasoned from the code and the `authtxn` precedent, not measured.
3. **Nit: the retry does not compare the live Agent's UID with the pass's.** See the wrong-key attack above.

## Probe files

These are in the scratchpad and none is committed:

- `pr54r5-muts/*.json`
- `pr54r5-runmut.sh`
- `pr54r5-mutlog.txt`
- the gate logs `pr54r5-maketest.log`, `pr54r5-race.log` and `pr54r5-verify.log`
