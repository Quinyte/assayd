# A `Lock` credits a `401` its own policy did not produce, when the route moves in the crediting pass (2026-09-16)

- **Question** (design 03 A77, first critique MAJOR 1): design 03 §3.3.3 says a `Lock` records `mode: apikey` only after an anonymous request through the route gets a `401` **that can be attributed** to `<agent>-auth`. Attribution rests on `status.cards[]` recording a digest for a revision the route's `backendRefs` name, which is the operator's own evidence that that backend answers the card path anonymously. A77 was drafted to make the missing-policy `Lock` re-point its route at `status.activeRevision`, and re-pointing moves what the attribution reads. The question this note answers came out of that: **does the shipped code already credit a `401` produced by a backend the route has just stopped naming?**
- **Answer: yes, for J2's and K2's `Lock`s, and no for the missing-policy `Lock`.** When a promotion moves the route's one `backendRef` in the same pass that probes, the request can still reach the revision the route has just left, that revision's own `401` is credited against the **new** revision's recorded card digest, and `status.auth` records `mode: apikey`. The record is one-way in the slice, and no later pass re-derives it.
- **Measured against:** the operator at `main` `3f37c25`, the tree v0.4.0 was cut from — agentgateway 1.5.0's CRDs, Gateway API v1.6.0, controller-runtime v0.24.1. No cluster: envtest, with the gateway's status written by the harness and the probe answered by an oracle (below).
- **Re-verify:** when design 03 A77 is implemented, which is what closes this; and at any change to `runLock`'s `ProbingAfter` arm, to `cardAttributes`, or to `ensureServingRoute`'s route write.

## Why this needed measuring rather than reading

The attribution rule is stated over the route object — "a digest for the revision the route's `backendRefs` name" — and the code reads that object **after** its own write in the same pass. Reading `runLock` makes the race look possible; it does not say whether the pass can actually reach `ProbingAfter` with a moved `backendRef`, whether A61's clearing of `beforeObserved` already covers it, or whether anything downstream corrects the record. Three independent readings of the code reached the same conclusion, which is evidence and not proof — a mechanism nobody has run is the shape this project keeps finding in its own tests.

## Method

`go test` against envtest, with the controller built through a `go test -overlay` that replaces nothing in the reconciler: the overlay supplies only the test's probe oracle and shortens `CardDriftInterval`. Nothing under `internal/controller` was edited for the measurement, so what ran is the shipped logic.

- **The probe oracle answers from the backend the Gateway has actually taken**, not from the route object. It holds one "taken" revision, which the harness advances only when it writes the route's parent status at the route's new `metadata.generation` — the same lag a real proxy has behind its controller. Asked for the card path, it answers `200` with the taken revision's card while that revision serves it, and `401` when the taken revision refuses it. **This is the whole point of the fixture**: an oracle that answers from `status.activeRevision`, or one that always answers `401` once a policy exists, cannot show the defect, and the second is the stub shape design 03 §8.1 already warns about.
- **Two revisions.** r1 serves no card at its card path and has no digest in `status.cards[]`; r2 serves one and has a digest, recorded by the operator's own fetch before it is promoted, as `agent_controller.go` records it.
- **No Gateway-level policy and no foreign `traffic` policy**, so A75's gate and §3.2's foreign check both pass and neither can be what holds or releases the credit.

## The sequence, for J2

| Pass | What the harness does | What the operator does | What is recorded |
|---|---|---|---|
| 1 | a served `auth: none` Agent on r1; the spec is edited to `apikey` | `Lock` entered: `ProbingBefore` sends one anonymous request, gets `200` from r1, and does **not** set `beforeObserved` — r1 has no recorded digest | `{kind: Lock, targetMode: apikey, stage: ApplyingPolicies, written: true}` |
| 2 | accepts route and policy at their current generations | `ApplyingPolicies` writes `<agent>-auth`; `Converging` holds §3.3.2's tuple; `ProbingAfter` probes, gets `200` from r1, credits nothing | stage `ProbingAfter` |
| 3 | r2 becomes available, its card is fetched and its digest recorded; **the route's parent status is left at the old generation**, as it is for the interval a real gateway takes | the promotion sets `status.activeRevision: r2`; `runLock`'s non-re-creation branch re-asserts the route on r2, so `metadata.generation` goes 1 → 2; A61 clears `beforeObserved`/`beforeRevision`, which were never set; `ProbingAfter` probes **in the same pass** | — |
| — | the oracle's taken revision is still r1 | r1 answers `401` at the card path, because it refuses it — which is exactly why r1 has no recorded digest | `tx.probe.after: 401` |
| — | — | `cardAttributes` reads the **post-write** route object, finds its single `backendRef` naming r2, finds r2's digest, and returns attributed | **`status.auth.mode: apikey`**, `appliedDigest`, `admittedGroups`, `verified.replicasProbed: 1` |

**Observed at the moment of the credit:** `<agent>-serving` at `metadata.generation: 2`, its parent status reporting `Accepted`/`ResolvedRefs` for `observedGeneration: 1`; the probe's answer `401`; `status.auth.mode` written `apikey`.

**What no later pass corrects.** Five further passes were run with the oracle's taken revision advanced to r2 and the route's status written at generation 2. None sent a probe: a served Agent with no transaction goes through `reconcileServed`, which checks the route and the policy and never re-probes, and `reportAboveServed` reads only the Gateway. `apikey` → `none` is refused in the slice (§3.3.1), so the record cannot be walked back by an edit either. The Agent is recorded as locked on the strength of a `401` from a backend the route no longer names.

## K2 reproduces identically; the missing-policy `Lock` does not

- **K2**: a refused `Adopt` whose owner edits to `apikey` takes the **same branch** of `runLock` — `k2 := status.Auth.Mode == ""` changes the message and nothing else, and `lockServed` → `servedRecord` writes `mode: apikey` for it as for J2. The same sequence was run with a pre-existing unauthenticated route and no `status.auth`, and produced the same credit at the same pass.
- **The missing-policy `Lock` does not reproduce, and the reason is structural.** Its branch takes the route as found and writes nothing to it (`rt = cur`), so its `backendRef` cannot move mid-transaction; and it enters at `ApplyingPolicies`, so it must pass `Converging` — where `routeConverged` compares the route's current generation against the Gateway's reported one — before it can probe at all. Driven through the same promotion, it parks at `Converging` and sends **zero** probes. That is the transaction A77's re-point changes, and it is the one the race was not reachable in.

## The minimal fix, measured

One change was tried, in the overlay, against all three transactions: **at `ProbingAfter`, re-check §3.3.2's route half — the Gateway reporting `Accepted` and `ResolvedRefs` at the route's current generation — and send no request and take no answer until it holds.** With it, pass 3 above makes its route write, finds the Gateway still reporting generation 1, sends no request, and ends. The J2 and K2 runs both park with `status.auth.mode` unchanged, and both reach `Served` on a later pass once the harness writes the route's status at generation 2 and the oracle's taken revision has advanced — crediting a `401` from the backend the route actually names. Nothing else was needed: no new status field, and no change to `cardAttributes`.

The variant that keys on "the `backendRef` changed in this pass" rather than on the tuple was also tried, and re-opened the window when the pass's status write was made to fail: the next pass found the route already moved, wrote nothing, compared nothing, and credited. That is why design 03 A77 specifies the precondition form.

## Consequences for design 03

- **§3.3.3's attribution is sound as a rule and unsound as implemented**, for J2 and K2. A77 is the specified fix — the route gate for every `Lock`, and the re-point for the missing-policy `Lock` — and **it is not implemented**. §8.1 case 18 (f) and (g) are the owed tests for the shipped defect, and (d) and (h) for the gate.
- **What this note does not measure.** It does not measure a real proxy. The oracle models the dataplane lagging its controller; the gate the fix adds proves only that the **controller** has reported at the route's current generation, so the window between that report and the proxy taking the new backend is narrowed and not closed — §3.3.2's own limit, and A77 states it as a residual rather than claiming it away.
- **One replica.** As with every probe in the slice, one answer is one replica's (H2).
