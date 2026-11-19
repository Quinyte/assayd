# Design 16 A20.28: four eval requeue results

**Verdict: PASS for this subset of [#115](https://github.com/Quinyte/assayd/issues/115).**
Findings: no BLOCKER, MAJOR or MINOR.

Reviewed test commit `e748538395aa36427e26a637ac62857cf3e8b3de` against
`700e2e63d07aa957f37f8c3044adc508a3030720`, and the subsequent evidence-only
commit `352a787313efc36b7d0ee71d3a901cdb5b7df47f`. This was a cold Codex
review in a separate detached worktree, **same GPT family as the implementer,
not cross-family**. No production files differ. The scope matches §1.1,
“What wakes the eval controller while it waits”, and the two A20.28 follow-ups
in the issue comments. It is not closure of the whole issue.

## Independent measurements

The four named envtests passed together on pristine source (10.517 s). A
single compiled Go source overlay then assigned each target path a distinct
nonzero duration. Splitting the shared I1/card condition retained its original
order. The results distinguish a test hitting the intended branch from one
merely reaching another wait with the same default. No tracked source was
modified by the overlay.

| Test | Substituted result observed at the added assertion | Expected |
|---|---:|---:|
| TestTheCardPreconditionHoldsTheRun | 23 s | CardDriftInterval |
| TestACandidateUnderTheI1HoldIsSentNothing | 17 s on each of three passes | CardDriftInterval |
| TestAMissingSuiteRequeuesWithoutStartingEval | 29 s | CardDriftInterval |
| TestAnOrphanedClaimIsInterrupted | 31 s | 5 s |

All four failed at the duration assertion, with no other test failures. The
three waits compare live Agent resourceVersions around the individual eval
pass and check no case was sent. The missing-suite test confirms NotFound,
then observes a send to the same candidate after suite creation. The orphan
test reads the failed case, reason and retained claim immediately after the
timed pass, and subsequently checks the score and absence of a repeat send.

The implementer's evidence note reports four separate zero-result mutations.
My different-duration overlay corroborates the branch mapping independently;
these are not four additional separately compiled mutations. Both the test
code and note distinguish a returned duration from actual scheduling, and an
unchanged resourceVersion from proof no no-op request was attempted. The
three held cases pin the mapping to the shared interval (currently five
minutes), not that constant's numeric definition independently.

I used local envtest 1.36.2 with no existing cluster, not a kubelet, manager
timing test or live environment. I did not independently run the aggregate
gates in this review. The remaining failed-pair, error and no-requeue classes
are outside this verdict.
