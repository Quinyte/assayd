# A20.28: four eval-controller requeue regression tests

Date: 2026-11-19. Base: `700e2e63d07aa957f37f8c3044adc508a3030720`.

This is bounded progress on #115, not closure of that issue. Design 16 §1.1,
“What wakes the eval controller while it waits”, already specifies these
intervals. No production behavior, interval, policy, or approval changes.
Historical amendment evidence is left unchanged.

## Measured cases

| Situation | Test | Default expected result |
|---|---|---|
| Candidate has no recorded card | `TestTheCardPreconditionHoldsTheRun` | `CardDriftInterval` (5 minutes) |
| Candidate is held by I1 | `TestACandidateUnderTheI1HoldIsSentNothing` | `CardDriftInterval` (5 minutes) |
| Referenced suite is missing | `TestAMissingSuiteRequeuesWithoutStartingEval` | `CardDriftInterval` (5 minutes) |
| An orphaned claim is recorded Interrupted | `TestAnOrphanedClaimIsInterrupted` | 5 seconds on the recording pass |

The three waiting cases also assert unchanged Agent resourceVersion and no
case send. This measures persisted state, not the absence of attempted
no-op API writes. The missing-suite row verifies NotFound before the pass,
then recreates the suite and observes one send for the same candidate.
The orphan row retains the existing outcome, claim and no-repeat-send checks.
No requeue override is used.

## Mutation evidence

Focused pristine envtest: PASS, 10.760 seconds. The fixture runs a real local
API server and etcd, not a kubelet or production cluster; availability and
claims use the existing controlled fixtures.

Four separately compiled mutations replaced only the corresponding return
with no requeue. The shared I1/card branch was split in those mutations so
the other condition kept its normal interval. All four were KILLED at the
new timing assertion, not by compilation errors:

- I1: returned 0 seconds, wanted 5 minutes (three passes).
- Missing recorded card: returned 0 seconds, wanted 5 minutes.
- Missing suite: returned 0 seconds, wanted 5 minutes.
- Recorded Interrupted: returned 0 seconds, wanted 5 seconds.

Mutations ran in a temporary source copy, restored from a backup and
byte-compared afterward. No mutant was staged or published. These are
correctness tests of timer results, not security exploit reproduction.

## Remaining scope

This does not claim all #115 requeue, error, event, or historical concurrency
rows are covered. It measures returned durations, not wall-clock scheduling
by a running manager. Independent review and full validation are reported
with this change and its pull request.
