# A20.27: gate and EvalSuite read boundaries

Date: 2026-11-20. Base: `61bdbaab8b27d492c6293d50308dd3e15929983a`.
Refs #115, specifically [A20.27](https://github.com/Quinyte/assayd/issues/115#issuecomment-6265515378).
This change adds tests, not production behavior or a design decision.

## Scope and live contract

Design 16 §1.1 “What clause 5 costs” and historical A20.27 specify the ordinary
Agent promotion gate and the eval controller's early read boundaries. Later
A29 derives GatesPassed from stored status/history without these API reads.
This is not a claim about every pin path: Q5's separate pin admission logic
can read a suite for a non-serving resolved pin.

The added reader wraps the real envtest reader and records the relevant
uncached Get calls, object types and suite key. Direct reconcile calls are
synchronous. Positive controls require the expected reads, so an unconnected
counter cannot satisfy the test. The Agent schema checker is fresh where
necessary; a cached answer must not masquerade as an avoided gate call.
This does not promise a physical CRD read on every candidate pass: production
still uses its existing schema-check TTL.

## Coverage

- `TestAgentPromotionGateReadsSchemaBeforeSuiteOnlyForCandidate`: adding a
  gate to the revision already serving reads neither Agent CRD nor suite;
  a different candidate with an unavailable schema read reads only the CRD;
  a successful schema reply missing status.eval also reads only the CRD;
  a fresh compatible schema check reads CRD then the named suite.
- `TestAnUnavailableCandidatePausesTheRun`: eligible passes read the suite,
  two paused passes do not, and resumption reads it again. Existing send and
  retry checks remain.
- `TestTheCardPreconditionHoldsTheRun`: no suite Get without the recorded card;
  restoring the card permits a suite Get. Existing refusal and requeue checks
  remain.
- `TestACandidateUnderTheI1HoldIsSentNothing`: three held passes read no suite;
  the same reader observes a Get in an eligible control. Existing held-agent
  and no-send checks remain; the control does not weaken its auth state.

Schema-error and incompatible-schema replies are test-local injections.
The latter modifies a copied reply, never the installed cluster CRD.
Envtest uses local etcd/API server, no kubelet or production context.

## Measured mutation evidence

The four core focused tests passed together in 11.776 seconds. The final
Agent schema row, including the incompatible successful reply, passed in
7.651 seconds. Full repository validation is reported on the pull request.

Six isolated extra-read mutations compiled and were KILLED by exact read-list
assertions, not by build failure:

| Mutation | Discriminator |
|---|---|
| Read schema and suite for already-serving desired digest | Expected no reads; observed Agent CRD then suite |
| Read suite after schema-read error | Expected only Agent CRD; observed suite too |
| Read suite after incompatible schema reply | Expected only Agent CRD; observed suite too |
| Read suite on unavailable candidate exit | Expected none across paused passes; observed two suite Gets |
| Read suite on I1-held exit | Expected none on each pass; observed suite Get |
| Read suite on card-less exit | Expected none; observed suite Get |

Mutations ran in an isolated temporary source copy and were restored from
backups and byte-compared. No production mutation was staged or published.
These are correctness/read-cost checks, not vulnerability reproduction.

## Limits

This is bounded A20.27 progress and does not close umbrella #115. It does not
add A20.26 pin semantics, extend A20.28 timers, prove manager scheduling, or
measure every possible API call. Independent review remains a separate gate.
