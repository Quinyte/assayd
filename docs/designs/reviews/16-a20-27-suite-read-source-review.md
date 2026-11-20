# A20.27 suite-read coverage: independent source review

Date: 2026-11-20.
Reviewed commit: `0d2818446cc47d62f97a74818e427feceada06a6`.
Base: `61bdbaab8b27d492c6293d50308dd3e15929983a`.

**Verdict: PASS.** No BLOCKER or MAJOR findings were reported; no code change
was requested. This records the independent source review supplied through
the coordinating thread, not an additional review by the implementer.
The reviewer did not execute tests or reproduce the documented mutation
kills. No cross-family or cold-context independence is claimed here.

## Findings and scope

- The paused, I1-held and card-less no-read assertions instrument only the
  eval controller. The ordinary Agent promotion test uses no pin, keeping
  Q5's separate pin-admission reads outside its claim.
- Fresh reconcilers prevent a warm schema cache from making the no-read
  checks vacuous. Assertions distinguish zero reads, CRD-only reads and
  ordered Agent-CRD then named-EvalSuite reads, alongside status conditions.
- Eligible positive controls include before/after a pause, the restored
  card and the I1 test's separate eligible candidate using the same reader.
- The wrapper delegates to the real API server. The incompatible schema
  fixture deep-copies the reply rather than modifying the installed CRD.
- Four test/document files change, including generated condition-reference
  links. No production code changes.

## Verification limits

This review covers the typed uncached Get calls the wrapper records, not
arbitrary API paths or manager scheduling. The execution and mutation
results in `docs/research/a20-27-suite-read-tests-2026-11.md` are implementer
measurements, not independently reproduced results. Full local validation
and exact-head CI remain separate merge gates. #115 is not closed by this
bounded subset, and no new design decision is made.
