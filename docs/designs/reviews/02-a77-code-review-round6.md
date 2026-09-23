# Design 02 A77: round-6 confirmation of PR #54 at `ee52ae2`

**Scope:** `git diff 07f90d7..ee52ae2` only.

**Verdict: APPROVE.** There is no BLOCKER and no MAJOR. One MINOR stands.

## Gates

- `make test` exits 0 (envtest took 320.6 s).
- `make race` exits 0.
- `make verify` exits 0.
- `make e2e` was not run.
- The authorship of `ee52ae2` is `Quinyte Engineer <engineer@quinyte.com>` as both author and committer.
- The path and name scan of the patch is clean.

## Mutations: 8 run, 6 killed, 2 survived

Every mutation passed vet first, and each file was restored and checked by SHA-256 (8 of 8).

- **V01–V05 (the author's):** all killed. V01 is round 5's T4, and it is now killed by `TestTheEarlyRecordWriteDoesNotClobberAConcurrentStatusWrite`. V02 is killed by `TestTheEarlyRecordWriteDoesNotRecordOntoARecreatedAgent`.
- **V06, the keepable probe written for real instead of as a dry run:** killed by two tests. Without the dry run, the probe would persist the record, and the headless unrecorded object would be deleted on the next pass. The tests catch that dangerous form.
- **V07, a dry run that errors answers "not keepable":** **survived** (MINOR 1).
- **V08, keepability is asked even when a record exists:** survived, and I count it as equivalent. On a stale CRD there can never be a record. On a current CRD the answer is always yes. The only difference is one extra dry-run call.

## The attacks

1. **Side effects and admission.** `DryRunAll` persists nothing.
   - Webhooks on `agents/status` are called with `dryRun: true`. A webhook with `sideEffects` of `Some` or `Unknown` makes the dry run fail, which falls back to `RevisionServiceNotRecorded`. A deny does the same. Both are safe, and at worst the reason is the older, less specific one.
   - A stale `resourceVersion` gives a Conflict and the same fallback, and the next pass is correct.
   - The operator already holds `update` on `agents/status`.
   - This is reasoned from how dry run works; no webhook was installed.
2. **Is the answer trustworthy?** Yes. Pruning happens when the API server decodes the request, before admission, so the dry run returns the pruned object. The probe measured that answer directly (point 3).
3. **Stale-CRD paths reaching a record-dependent branch.** None. Probe P1 ran on the stale CRD:
   - Adoption on the converge path: the record is stored as nothing, and steady-state `resourceVersion` stays at `273 -> 273` over three passes, so there is no write churn and no hot loop.
   - Making the Service headless: `deleted=false reason=RevisionServiceRecordNotKept record=""`, and the refusal's steady state holds at `277 -> 277`.
   - The replace path needs `ours`, which needs a stored record, so it is unreachable.
   - The in-memory record from a create is never consulted later in the same pass.
4. **Does the CRD swap leak?** No test in `test/envtest` calls `t.Parallel()`, so the swap cannot overlap another test. Each package has its own API server. Cleanup restores the CRD and waits until the dry run keeps the record again. If the restore fails, the test fails loudly, and later failures could then be misattributed, which is acceptable for a test.
5. **Does the remedy work?** Yes (probe P1, continued). I applied the release CRD by letting the subtest's cleanup restore it, deleted the Service, and ran one pass:
   - `new=5a557110… record="5a557110…" clusterIP=10.0.171.104`.
   - A headless patch after that gives `replaced=true clusterIP=10.0.27.88 ready=Available`.

   It heals.
6. **Wording.** The `reasons.yaml` entry, §5 and §12 match the code. §5's phrase "the same mechanism `persistStatus` uses" is loose: `persistStatus` compares the answer to a real write, and this compares the answer to a dry run. The sentence itself says "asked before the refusal instead of after a write", so it is not false.

## MINOR

1. **The dry-run error fallback is documented but unpinned.** The `reasons.yaml` entry for `RevisionServiceRecordNotKept` says "A dry-run that errors falls back to `RevisionServiceNotRecorded`", and V07 survives. If it regressed, any error from the dry run (a Conflict, or a webhook with side effects) would report a stale CRD that is not there (rule 8).
   - **Fix:** in `TestAServiceWithNoRecordIsNeverDeleted`, or a sibling test, wrap the client so that dry-run status updates return an error, and assert `RevisionServiceNotRecorded`.

## Probe files

These are in the scratchpad and none is committed:

- `pr54r6-probe_test.go.txt`
- `pr54r6-muts/*.json`
- `pr54r6-runmut.sh`
- `pr54r6-mutlog.txt`
- the gate logs `pr54r6-maketest.log`, `pr54r6-race.log` and `pr54r6-verify.log`
