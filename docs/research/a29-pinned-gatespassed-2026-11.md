# A29 option (b): pinned GatesPassed verification

Implementation snapshot: `3588cbbcabdb24cd6d5ed3f3fca8b3afd6bf13cc`, based on
`fe9a52a`. Decision: the human, 2026-11-19, “Go with A29 option b on assayd”,
[recorded on proposal #164](https://github.com/Quinyte/assayd/pull/164#issuecomment-7018260059).
The proposal and decision fold are included in the same implementation PR;
proposal #164 was not separately merged. Closes the behavior described in #139.

## Scope and measurement

The change re-derives `GatesPassed` for the revision serving on every pass that
reaches promotion with the gate enforced, including pins. It does not change
pin eligibility, traffic admission, evaluation, or the status behavior of early
exits and an absent EvalSuite CRD. The stated sticky-condition residues remain.

Measured on Go 1.26.3, darwin/arm64, with envtest Kubernetes 1.36.2. Envtest
provides a real API server and etcd, but no kubelet; availability and evaluation
records are written by the fixtures. These tests do not prove real pods serve.
No existing Kubernetes cluster or ambient kubeconfig was used.

The focused envtest selection passed (21.568 s): all six A29 shapes are covered
by `TestA29RestoredGateRederivesPinnedStatus` (R1),
`TestA29PinToRetainedRevisionReplacesCandidateStatus` (R2),
`TestA29SameServingRevisionRederivesPinnedStatus` (R3/R6), and the revised
`TestAServingPinThatPassedEarlierSaysOnlyWhatIsRecorded` (R4),
`TestAPinOnAGatedAgentIsAdmittedOrRefusedByQ5` (R5), and
`TestAPinToARevisionPassedHoldsRederivesGatesPassed` (the recorded-pass pin).
The controller's exact serving-message test passed too.

## Mutation ledger

These are bounded controller/status mutations, not security exploit tests.
Each was applied separately in a temporary copy of the implementation, compiled,
and failed a behavioral assertion. None was counted as killed merely because
compilation failed. Source files were restored from backup copies and compared
byte-for-byte after the runs; the implementation worktree was never mutated.

| Mutation | Expected regression | Observed result |
|---|---|---|
| Restore the old pinned skip before `case gating == gateEnforced` | Previous condition survives under a pin | **KILLED**: R1 retained `NoGatesDeclared`; R2 retained C's `EvalAwaitingCard`; R5 retained C's `True/EvalPassed` |
| Narrow the skip to option (a): same active revision, no stored `NoGatesDeclared` | A non-promoting pinned pass is skipped | **KILLED**: R3 had no condition, R4 retained `EvalFailed`, and R6 retained the unpinned wording |
| Disable the pinned tail (`if false && specPin != ""`) | Report says the next spec revision will be gated while pinned | **KILLED**: R2 and R6 rejected the unpinned tail |
| Change the tail to “It serves by pin because Q5 (a) admitted the pin” | Attribute a cause not supported for every pinned serving state | **KILLED**: R1 rejected the invented `Q5 (a)` cause |
| Restore the old serving-form closing clause in `servingPinBypass` | Bypass report incorrectly claims GatesPassed stays sticky | **KILLED**: R1 step 5 rejected the old clause after the pin moved to S |

The tests assert transitions and the serving revision's digest, not only a
condition value. The existing `settle` helper checks only phase and active/candidate revision
convergence, not resourceVersion stability. In R6, adding a gate to the serving revision produces the unpinned wording;
pinning that same revision replaces it with the pinned tail.

To reproduce a focused unmutated run:

```sh
unset USE_EXISTING_CLUSTER
export KUBECONFIG=/dev/null
export KUBEBUILDER_ASSETS="$("$(go env GOPATH)/bin/setup-envtest" use 1.36.x -p path)"
go test ./test/envtest/... -count=1 -timeout=15m \
  -run 'TestA29|TestAPinToARevisionPassedHoldsRederivesGatesPassed|TestAPinOnAGatedAgentIsAdmittedOrRefusedByQ5|TestAServingPinThatPassedEarlierSaysOnlyWhatIsRecorded'
```

## Independent-review follow-up

The cold review of `3588cbb` returned REVISE for a missing CRD-return boundary
discriminator, plus three minor findings. Its report is preserved verbatim in
`docs/designs/reviews/16-a29-option-b-codex-review.md`.

The CRD-return row now first preserves its original unchanged-eval
absence/recovery check. A second cycle writes a controlled Passed pair without
changing the spec while discovery is off: one absent-CRD pass must retain the
old false reading, and exactly the next enforced pass must report the new true
reading. This is a controlled status input, not an assertion that evaluation
runs while the CRD is absent. The test also pins the exact approved warning
suffix, and the listed unit-test comment now includes pinned revisions. The
combined decision/code packaging is explicitly labeled an implementer process
deviation, not a separate user decision or a docs-only predecessor commit.

The expanded focused suite passed (22.860 s); after preserving the original
unchanged-eval cycle, the CRD-return row passed again (8.080 s).

Additional isolated mutations, each compiled:

| Mutation | Result |
|---|---|
| Reassert on a pinned CRD-absent pass | **KILLED**: the excluded pass changed false to true |
| Skip reassertion for an active pin whose condition already observed this spec generation | **KILLED**: the first rediscovered pass incorrectly remained false |
| Remove only the pinned tail's early-exit qualifier | **KILLED** by the exact suffix assertion |
| Force a persisted condition change on steady-state passes | **KILLED**: Agent resourceVersion changed on the first repeated pass in both gate/pin orderings |
| Remove the `equalStatus` write guard alone | **SURVIVED**: identical Status().Update payloads are no-ops at the API server; resourceVersion cannot detect redundant update attempts |

The new `a29NoPersistedStatusChanges` helper explicitly checks unchanged Agent
resourceVersion over three further reconciliations after convergence. This
proves absence of persisted churn in those fixture states, not absence of API
write attempts. The surviving mutation is retained here as that limitation,
not counted as a kill. All mutation copies were restored and byte-compared.

Independent recheck of `70ddd58`: **PASS**, all original findings closed; report
`docs/designs/reviews/16-a29-option-b-codex-recheck.md`. The remaining research-note
wording minor is corrected above. No production code changed after the review.
