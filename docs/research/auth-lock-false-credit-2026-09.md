# A `Lock` credits a `401` its own policy did not produce, when the route moves in the crediting pass (2026-09-16)

- **Question** (design 03 A77, first critique MAJOR 1): design 03 §3.3.3 says a `Lock` records `mode: apikey` only after an anonymous request through the route gets a `401` **that can be attributed** to `<agent>-auth`. Attribution rests on `status.cards[]` recording a digest for a revision the route's `backendRefs` name, which is the operator's own evidence that that backend answers the card path anonymously. A77 was drafted to make the missing-policy `Lock` re-point its route at `status.activeRevision`, and re-pointing moves what the attribution reads. The question this note answers came out of that: **does the shipped code already credit a `401` produced by a backend the route has just stopped naming?**
- **Answer: yes, for J2's and K2's `Lock`s.** When a promotion moves the route's one `backendRef` in the same pass that probes, the request still reaches the revision the route has just left, that revision's own `401` is credited against the **new** revision's recorded card digest, and `status.auth` records `mode: apikey`. The record is one-way in the slice, and no later pass re-derives it. **The missing-policy `Lock` was driven at the same state and did not credit**, for the structural reason below.
- **Measured against:** the operator at `main` `3f37c25` — two commits after `v0.4.0` (`0303acb`), from which the operator differs only in a doc comment on one CRD field; the rest of the intervening diff is `docs/`, the two root guidance files (`AGENTS.md`, `CLAUDE.md`) and the two generated CRDs under `charts/` and `config/`. agentgateway 1.5.0's CRDs, Gateway API v1.6.0, controller-runtime v0.24.1. No cluster: envtest, with the Gateway's route and policy status written by the harness and the probe answered by an oracle (below).
- **Re-verify:** when design 03 A77 is implemented, which is what closes this; and at any change to `runLock`'s `ProbingAfter` arm, to `cardAttributes`, or to `ensureServingRoute`'s route write.

## Why this needed measuring rather than reading

The attribution rule is stated over the route object — "a digest for the revision the route's `backendRefs` name" — and the code reads that object **after** its own write in the same pass. Reading `runLock` makes the race look possible; it does not say whether a pass can actually reach `ProbingAfter` with a moved `backendRef`, whether A61's clearing of `beforeObserved` already covers it, or whether anything downstream corrects the record. Four independent readings of the code reached the same conclusion, which is evidence and not proof — a mechanism nobody has run is the shape this project keeps finding in its own tests.

## Method, and where the fixture is

**The fixture is not in the repository, and this section is what a reader needs to rebuild it.** It was run as one scratch file added to `package envtest` through `go test -overlay`, against `3f37c25` with nothing else changed: no file under `internal/`, `api/` or `cmd/` was touched for the measuring run, and the one patched run is marked as such below. It is deliberately not committed. It asserts the **defect**, so it fails by design and would be a permanently red test in `make test`; the tests that will pin this for good are design 03 §8.1 case 18 (f) and (g), which assert the corrected behaviour and are owed with A77's implementation.

What the scratch file contains, and nothing else:

- **A probe oracle, injected through the public `AuthProbe` field on `AgentReconciler`** — the same seam `test/envtest/authcreate_test.go` already injects through, so no overlay of the reconciler is needed for it. It answers **from the backend the Gateway has actually taken**, not from the route object, which is the whole point of the fixture and the one thing the suite's existing stub cannot say. It holds a `taken` workload name, which the test advances the way a converged gateway would, and a `policyTaken` flag. While `policyTaken` is false, **no `401` it returns can be `<agent>-auth`'s** — so a `Lock` that credits one is crediting evidence the policy did not produce. A named set of backends "refuse": they answer an anonymous request `401` on their own, which is an agent that authenticates in its own code, and is also why the operator's card fetch never recorded a digest for it.
- **One helper**, `acceptedGeneration`, reading the `observedGeneration` the harness wrote on the route's parent status.
- Everything else is an existing `test/envtest` helper: `promote`, `lockPass`, `reconcileOnce`, `acceptRoute`, `acceptPolicy`, `deletePolicy`, `markAvailable`, `recordCardFor`, `forgetCards`, `toMode`, `mustEdit`, `servingRoute`, `txOf`, `authOf`, `condition`.

Two revisions. **r1 refuses anonymous requests on its own and has no digest in `status.cards[]`** — those two facts are the same fact, and they hold for the whole run. r2 serves its card, and its digest is written into `status.cards[]` the way the operator's own fetch would (envtest has no cluster network, so the fetch is written rather than performed, and `cardFetchDue` then makes the pass skip it, which is what a pass whose fetch had just succeeded does). No Gateway-level policy and no foreign `traffic` policy, so A75's gate and §3.2's foreign check both pass and neither can be what holds or releases the credit.

## The sequence, for J2, and what it printed

| Pass | What the harness does | What the operator does | Probe answer |
|---|---|---|---|
| 1 | a served `auth: none` Agent on r1; `status.cards` cleared; the spec edited to `apikey` | `Lock` entered. `ProbingBefore` sends one anonymous request and gets **`401`** — r1's own refusal — and does not set `beforeObserved`, which needs a `200` whose digest matches a recorded one. The arm then falls through in the same pass: `ApplyingPolicies` writes `<agent>-auth`, and the pass ends at `Converging`, where the tuple cannot hold because the fresh policy reports no ancestors yet | **401** |
| 2 | accepts route and policy at their current generations | resumes at `Converging`, which now holds §3.3.2's tuple; `ProbingAfter` probes, gets **`401`** again, and **credits nothing** — `beforeObserved` is unset and `status.cards[]` records no digest for r1, so `cardAttributes` refuses | **401** |
| 3 | r2 becomes available and its digest is recorded; **the route's parent status is left at the old generation**, as it is for the interval a real gateway takes | the promotion sets `status.activeRevision: r2`; `runLock`'s non-re-creation branch re-asserts the route on r2, so `metadata.generation` goes 1 → 2; A61's clearing is gated on `tx.BeforeRevision != ""` and is skipped, because `ProbingBefore` never set it; `ProbingAfter` probes **in the same pass**, and the oracle's taken backend is still r1, which answers **`401`** on its own | **401** |
| — | — | `cardAttributes` reads the **post-write** route object, finds its single `backendRef` naming r2, finds r2's digest, and returns attributed | credited |

So the run is **`401` throughout**, and the three answers differ only in what the operator does with them. That is the sharper form of the defect than a `200 → 401` transition would have been: the same answer that is correctly refused at pass 2 is credited at pass 3, and the only thing that changed is which revision the route names.

**Verbatim, from the run:**

```
route generation 2, gateway accepted generation 1, probe answers [401 401 401],
status.auth=&{Mode:apikey KeySource:assayd-run-…/assayd.dev/api-keys=true
AdmittedGroups:[…] AppliedDigest:sha256:b2be67… Verified:0x… Transaction:<nil>}

REPRODUCED: the Lock recorded mode=apikey on a 401 the policy did not produce. The
gateway had not taken the policy (policyTaken=false) and still routed to
falsecredit-126eb2a71a, which refuses anonymous requests on its own; the route named
falsecredit-356f46a088, whose card digest attributed the 401. The route was accepted at
generation 1 and is at generation 2, so the operator never saw the gateway take the
backendRef it attributed against.

five passes later: status.auth=&{Mode:apikey …}, probes since the credit: 0
GovernanceSkipped after the credit: False/AuthVerifiedOnOneReplica — …it was published
only after an anonymous request through it got 401 on 1 of an unknown number of declared
gateway replicas…
apikey -> none after the false credit: True/AuthTransitionNotBuilt
```

**What no later pass corrects.** Five further passes sent **zero** probes: a served Agent with no transaction goes through `reconcileServed`, which checks the route and the policy and never re-probes, and `reportAboveServed` reads only the Gateway. `GovernanceSkipped` reads `False`, reason `AuthVerifiedOnOneReplica` — the Agent is reported as governed. An owner's edit back to `none` is refused with `AuthTransitionNotBuilt`, so the record cannot be walked back that way either.

## K2 reproduces identically; the missing-policy `Lock` did not credit

- **K2**: a refused `Adopt` whose owner edits to `apikey` takes the **same branch** of `runLock` — `k2 := status.Auth.Mode == ""` changes the message and nothing else, and `lockServed` → `servedRecord` writes `mode: apikey` for it as for J2. Run with a pre-existing unauthenticated route and no `status.auth`, it printed the same shape: `route generation 2, gateway accepted generation 1, probe answers [401 401 401]`, and `Mode:apikey`.
- **The missing-policy `Lock` does not reach the window, and the reason is structural**: its branch takes the route as found and never calls `ensureServingRoute` (`rt = cur`), so no backend moves under its probe and there is nothing for the attribution to misread. That is the one reason, and it is the one the shared `AGENTS.md`/`CLAUDE.md` clause rests on.
  **Separately, as an observation of this run and not as structure**: driven through the same promotion, that `Lock` sat at `Converging` and sent **zero** probes — `tx.Stage: Converging`, `probe answers=[]`, `accepted gen=2 route gen=3`. What held it was the harness, not the branch: the fixture moves the route by a promotion **before** deleting the policy and never accepts the new generation, so `routeConverged` fails for a reason that has nothing to do with the `Lock`. A missing-policy `Lock` entered on a settled route does leave `Converging` and probe without ending the pass, once the tuple holds — which is never the pass that enters it, since `lockMissingPolicy` writes a fresh `<agent>-auth` and `policyConverged` refuses a policy that reports no ancestors yet. It must reach `ProbingAfter`, or A77's own re-point there would be unreachable and §8.1 case 18 (a) could not be built.

## The minimal fix, measured

One change was tried, as a second overlay replacing `internal/controller/authtxn.go` with a ten-line patch and nothing else: **at `ProbingAfter`, re-check §3.3.2's route half — the Gateway reporting `Accepted` and `ResolvedRefs` at the route's current generation — and send no request and take no answer until it holds.** Under it, both reproductions stop at the same place:

```
J2:  route generation 2, gateway accepted generation 1, probe answers [401 401],
     status.auth=&{Mode:none … Transaction:0x…}
     NOT credited in the promotion pass: … tx=&{Kind:Lock … Stage:ProbingAfter …}
K2:  route generation 2, gateway accepted generation 1, probe answers [401 401],
     status.auth=&{Mode: … Transaction:0x…}
```

The third probe is never sent, the transaction stays at `ProbingAfter`, and `status.auth.mode` is unchanged — `none` for J2, empty for K2. **What was not measured**: that the `Lock` then reaches `Served` once the Gateway reports at the new generation. The fixture stops at the credit, so that is the design's claim and not this note's.

**Not measured, and stated because the design rests on it:** the variant that keys the guard on "the `backendRef` changed in this pass" rather than on the tuple. The argument against it — that a status write lost to a conflict, or a restart, leaves the next pass finding the route already moved, writing nothing, comparing nothing, and crediting — is derived from `persistStatus` and `equalRoute`, not run. Design 03 A77 specifies the precondition form on that argument.

## Consequences for design 03

- **§3.3.3's attribution is sound as a rule and unsound as implemented**, for J2 and K2. A77 is the specified fix — the route gate for every `Lock`, and the re-point for the missing-policy `Lock` — and **it is not implemented**. §8.1 case 18 (f) and (g) are the owed tests for the shipped defect, and (d) and (h) for the gate.
- **What this note does not measure.** It does not measure a real proxy. The oracle models the dataplane lagging its controller; the gate the fix adds proves only that the **controller** has reported at the route's current generation, so the window between that report and the proxy taking the new backend is narrowed and not closed — §3.3.2's own limit, and A77 states it as a residual rather than claiming it away.
- **One replica.** As with every probe in the slice, one answer is one replica's (H2).
