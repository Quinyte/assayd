# A80's policy half, measured: what a broken `<agent>-auth` does to an accepted route

**Measured 2026-09-22**, on a throwaway k3d cluster with Gateway API v1.6.0 and
agentgateway 1.5.0 — the release `hack/e2e.sh` and phase 2 of
`hack/conformance-cluster.sh` install — against a hand-built fixture in the
shape of `test/conformance`'s slice cases. **No operator runs in it**: every
object is written by hand or by `internal/compiler`, so what is measured is
agentgateway, not assayd.

## Why it was run

Design 03 §5's policy row describes a served `<agent>-auth` the assayd Gateway
reports it does not attach, on a route it still accepts, and says of it: "the
state is inferred from §3.3.2's reporting shape and is not measured, and
building it did not measure it". §8.1 case 19 (f) writes that report into a
Gateway status envtest controls and measures the operator's *reaction*; the
listener-rename walkthrough (`a80-served-route-walkthrough-2026-09.md`) fires
both halves at once, because renaming the listener detaches the route AND the
policy, so it cannot separate them either. §8.1 names the missing case in
terms: "a served Agent whose `<agent>-auth` the controller reports it does not
attach on an ACCEPTED route, recording whether the route then answers `200` or
`401`".

That last number is the whole question. §5 says the Agent should report that
"the route may be answering with no credential required". Nothing had checked
whether it is.

## The fixture

A Gateway with the operator's own listener name (`http`) in its own namespace;
a run namespace holding a labelled key ConfigMap (`assayd.dev/api-keys: "true"`,
one `sha256:` hash in group `conf-team`, one in group `rogue`), an `agnhost`
backend, and a `curl` Pod that sends every request from inside the cluster. The
Agent's serving route is written in `servingRouteFor`'s shape and PUBLISHED
(one `backendRef`); its `<agent>-auth` is `compiler.AuthPolicy`'s output,
unedited. The baseline, before every mutation below:

```
route probe-serving generation 1, controllerName agentgateway.dev/agentgateway
  Accepted      True  Accepted      gen=1
  ResolvedRefs  True  ResolvedRefs  gen=1
policy probe-auth generation 1
  ancestor gateway.networking.k8s.io Gateway conf-slice
  Accepted True Valid    gen=1  "Policy accepted"
  Attached True Attached gen=1  "Attached to all targets"
anonymous 401 · key in the admitted group 200 · key in another group 403
```

## What was tried, and what each attempt reported

Every row was applied to that baseline and read back after 25 s, with the route
re-read each time. "Digest-equal" means the policy's own bytes were not touched,
so it still renders to what `compiler.Digest` gives — A80's precondition.

| # | stimulus | digest-equal | policy report, at its own current generation | route | anon |
|---|---|---|---|---|---|
| 1 | `matchExpressions: ["apiKey.group =="]` (CEL that does not parse) | no | `Accepted=True` **`PartiallyValid`** gen 2, "authorization matchExpression is not a valid CEL expression"; `Attached=True` **gen 1** | `Accepted=True` | **401** |
| 2 | `configMapSelector` matching no ConfigMap | no | `Accepted=True` `Valid`, `Attached=True`, both current | `Accepted=True` | **401** (every key 401) |
| 3 | the selected key ConfigMap DELETED | yes | `Accepted=True` `Valid`, `Attached=True` — nothing reported at all | `Accepted=True` | **401** (every key 401) |
| 4 | `targetRefs[0].sectionName: no-such-rule` | no | ancestor **`StatusSummary`**; `Accepted=True` `Valid`; **`Attached=False` `Pending`**, "sectionName … not found in HTTPRoute" | `Accepted=True` | **200** |
| 5 | `matchExpressions: ["nosuchvar.group == …"]` (unknown CEL variable) | no | `Accepted=True` `Valid`, `Attached=True` — it compiles | `Accepted=True` | **401** (admitted key 403: the runtime error denies) |
| 6 | `targetRefs[0].name` → a route that does not exist | no | ancestor **`StatusSummary`**; **`Attached=False` `Pending`**, "HTTPRoute … not found" | `Accepted=True` | **200** |
| 7 | `traffic.extAuth` with neither `grpc` nor `http` | — | refused at admission (`ExactlyOneOf`) | — | — |
| 7b | `traffic.extAuth.grpc` at a Service that does not exist | no | `Accepted=True` **`PartiallyValid`** gen 2, "failed to build extAuth: unable to find the Service"; `Attached=True` **gen 1** | `Accepted=True` | **401** (admitted key 403) |
| 8 | `targetRefs` → a `Service` | — | refused at admission: "the 'traffic' field can only target a Gateway, ListenerSet, GRPCRoute, HTTPRoute, or InferencePool" | — | — |
| 9 | the ROUTE deleted, then re-created | yes | while the route is gone: `StatusSummary`, `Attached=False`. On re-create, the first sample — 0.5 s cadence — already read the route `Accepted=True` **and** the policy `Attached=True` together | — | 404 → 401 |
| 10 | a rejected entry (`{"key": …}`, no `keyHash`) added to the SELECTED key ConfigMap | **yes** | `Accepted=True` **`PartiallyValid`** gen 1, naming the ConfigMap and the entry; `Attached=True` gen 1; ancestor still the real Gateway | `Accepted=True` | **401** (admitted key 200, other group 403) |
| 11 | a SECOND `traffic` policy on the same route, `Allow: ["true"]` | yes | `<agent>-auth` `Valid`/`Attached`; the second policy `Valid`/`Attached` too; the rule WIDENS the route (a key in another group goes 403 → 200) | `Accepted=True` | 401 |
| 13 | `targetRefs` → an `InferencePool` whose CRD is not installed | no | ancestor **`StatusSummary`**; **`Attached=False` `Pending`**, "InferencePool … not found" | `Accepted=True` | **200** |
| 14 | a SECOND labelled key ConfigMap holding a rejected entry | **yes** | as row 10, naming the second ConfigMap | `Accepted=True` | **401** |

Rows 10 and 14 heal when the entry goes; rows 4 and 6 heal when the target is
restored. Row 12 was folded into row 11 and is not numbered here.

**Nothing reached `Accepted=False`.** On agentgateway 1.5.0, every translation
failure a `traffic` policy could be driven into here reports `Accepted=True`
with reason `PartiallyValid`; non-attachment is reported instead by
`Attached=False` on the synthetic `StatusSummary` ancestor. §5 enumerates
`Accepted=False` among the tuple breaks and it is a shape this probe could not
produce — an absence, not a proof that no input produces it.

**Two conditions of one ancestor can sit on different generations.** Rows 1 and
7b left `Accepted` on the new generation and `Attached` on the old one. The
conformance helper `statusIsCurrent` requires ALL of an ancestor's conditions to
be current, so a case built on either row would wait forever; the two cases that
shipped are built on rows 14 and 4, whose conditions move together.

## The two states that matter, and their two different numbers

- **Row 14 — the break A80's precondition admits.** The policy is byte-unchanged
  and still renders to `appliedDigest`; an administrator's key ConfigMap is what
  broke it, and the key set is outside every gate the operator holds (§3.4.4).
  The Gateway reports `Accepted=True` with a reason other than `Valid`, at the
  policy's current generation — §5's second listed tuple break — while the route
  reads `Accepted=True`/`ResolvedRefs=True` at its own. **The route answers 401.**
  The admitted key still gets 200 and a key in another group still gets 403: the
  valid entries load and both controls hold. It is fail-CLOSED.

- **Row 4 — the break that is a bypass.** The Gateway attaches `<agent>-auth` to
  nothing, on the synthetic ancestor, while accepting the route. **The route
  answers 200, with the backend's own body.** A80's fail-open half is real at
  the gateway. Rows 6 and 13 are the same shape and the same 200.

  Reaching it took an edit to the policy's own `targetRefs`, and this note's
  first version read that as putting the state outside §5's precondition. **That
  was backwards.** The precondition is render against render:
  `reassertServedPolicy` digests what `compiler.AuthPolicy` renders and compares
  it to `appliedDigest`, itself a render digest — §3.3.3 says "is not a digest of
  the stored object" — so an out-of-band edit is invisible to it. The state IS
  judged, and the sequence is **entered, reported, repaired on one pass**: the
  guards pass, `writeAuthPolicy` overwrites the drifted `spec`, and the repaired
  object is what `judgeServed` judges, on a status whose last word is still the
  synthetic ancestor. So the same pass raises `AuthPolicyNotAttached` and closes
  the hole it reports, and the 200 is a window, not a standing hole. Pinned in
  envtest, where the operator runs (§8.1 case 19 (f)), with two mutations against
  the operator itself. **A80's premise is satisfied, not falsified.**

- **Row 15 — what the synthetic ancestor MEANS, and the mechanism behind the
  negative result.** Give a policy two `targetRefs` of which one does not
  resolve, and 1.5.0 writes BOTH ancestors at the policy's current generation:
  the synthetic one, `Attached=False`, naming the ref that failed, and the real
  Gateway's, `Accepted=True`/`Valid` and **`Attached=True`**, for the one that
  did — with the route enforcing `401`. Nobody had recorded that. It means the
  synthetic ancestor says "at least one target did not resolve", not "this
  policy attached to nothing". An `<agent>-auth` has exactly ONE `targetRef`, so
  for it the two coincide, and the only ways to make that ref fail are to remove
  the route — which fires the route half — or to edit the ref, which the next
  pass repairs. That is why no stimulus reached a standing bypass: a reason, not
  an absence.

## What this shows

1. **A80's policy half is a real, reportable state, not an artefact of the
   reporting shape.** Row 14 reaches it with a digest-equal policy on an
   accepted route, so the operator's `AuthPolicyNotAttached` report is
   reachable and is not dead code.
2. **The consequence that report announces is not true of the state that
   reaches it.** In row 14 the route refuses anonymous requests. §5's
   "the route may be answering with no credential required" is a hedge that
   holds, but the reachable shape is the one where it is false.
3. **The bypass is real, and the operator both reports it and closes it.** A
   policy the Gateway attaches to nothing leaves a published route wide open —
   200 to anyone — and the precondition does NOT exclude it, because the digest
   comparison is render against render. One pass raises `AuthPolicyNotAttached`
   and repairs the spec: entered, reported, repaired. The 200 is a window
   between the edit and the next reconcile.
4. **The operator's own report names the wrong cause in the reachable shape.**
   In row 14 the Gateway says `Attached=True`, reason `Attached`, "Attached to
   all targets". `internal/controller`'s `policyUnattachedMessage` opens "this
   Agent's route is accepted and SERVING while the assayd Gateway reports that
   it **does not attach** the `<agent>-auth` policy", under reason
   `AuthPolicyNotAttached`, and takes the Agent to `Ready=False`, phase
   `Degraded`, `GovernanceSkipped=True` — while the same route is measured
   refusing anonymous requests. The trailing "may be answering with no
   credential required" is a hedge and survives; the lead is not. That is
   AGENTS.md rule 8 — a condition naming a plausible cause that was never
   checked — one clause over from the one A81 already fixed with its `routeOK`
   branch. Recorded as **OWED** in design 03 §5 and A82, not fixed here: the
   message needs a third lead and the `PartiallyValid` clause probably needs a
   reason of its own, and both add user-facing vocabulary to an approved slice.
5. **Row 3 is a silent total outage, and nothing in the design reports it.**
   Deleting the selected key `ConfigMap` — external, digest-equal, an
   administrator's action — takes EVERY key to `401` while the policy reports
   `Accepted=True`/`Valid`, `Attached=True` and the route reports accepted. So
   `policyReport` holds, `routeReport` holds, `judgeServed` raises nothing, and
   every served Agent in the namespace stays `Ready=True` through a complete
   authentication outage for every principal. That is the degraded-and-silent
   path NFR-8 and rule 8 forbid, and §3.4.4's "the key set is outside every
   gate" is about widening, not outage. Recorded in design 03 §5 as **owed**.
6. **One rejected entry breaks every Agent in the run namespace.** Every
   `<agent>-auth` selects key sets by the same constant label, and a run
   namespace is shared, so rows 10 and 14 are namespace-wide events, not
   per-Agent ones. With finding 4 that means one administrator's typo can put
   every served API-key Agent in a namespace into `Degraded`, each announcing a
   bypass that is not happening.
7. **The two halves could not be separated in the other direction either.**
   Renaming the Gateway's listener, run again here, reproduces the walkthrough:
   `StatusSummary`, `Attached=False`, and the route `Accepted=False` on the same
   pass. That is the conflated state A81 ordered route-first, and it is the
   state the new cases' route gate refuses to measure (mutation B3 below).

## What it does not show

- **It does not show that no input reaches an unattached, digest-equal policy on
  an accepted route.** THREE stimuli reached non-attachment by editing the
  policy's spec — rows 4, 6 and 13. A fourth reached it with the policy
  byte-unchanged: **row 9**, deleting the route. What keeps row 9 out of §5's
  policy row is the route's ABSENCE, not the digest, and `policyReport`
  deliberately does not generation-gate the synthetic ancestor, so an HTTPRoute
  deletion is an ordinary event that passes through this state. On re-creation
  the first 0.5 s sample already read the route accepted and the policy attached
  together, so no window was OBSERVED — at that cadence, which is not a proof of
  none. A fifth input could exist: an agentgateway upgrade that resolves targets
  differently, a second controller, a partial write. The claim is bounded by
  what was tried, and what was tried is the table above.
- **`Attached=False` on the REAL Gateway ancestor was produced by nothing
  tried**, the same standing `Accepted=False` has. It is the shape §8.1 case 19
  (f)'s `unattachPolicy` writes, so that fixture drives a state 1.5.0 was
  measured not to produce; `summarisePolicy` is the reachable sibling. The
  fixture is kept — the arm is live and the CRD asserts the shape — and the
  `PartiallyValid` row is added beside it. `policyReport`'s reason-mismatch arm
  was never untested: `authserved_unit_test.go` drives it and `make unit` is in
  CI. What was missing is an ENVTEST fixture, the layer where the reason, the
  message and the conditions are composed.
- **The bypass needs an identity that can write a policy in a run namespace.**
  `assayd-gateway-policies` reserves that to the operator and
  `admission.extraOperators` (design 07 A6.7). This cluster installs no chart
  and no admission policy, so every stimulus here was applied as cluster-admin.
  What is measured is agentgateway's behaviour; who can provoke it on a real
  install is the chart's question and is not measured here.
- **A second instance of the rule-8 defect, from row 15.** `policyReport`
  short-circuits on the synthetic ancestor wherever it appears, so on that shape
  the operator would report `AuthPolicyNotAttached` against a contemporaneous
  `Attached=True` at the same generation, with the route measured enforcing. The
  comment justifying the short-circuit — "the last thing the Gateway said is
  that this policy attached to nothing" — is false there. Unreachable for a
  one-`targetRef` `<agent>-auth` today; recorded as design 03 **D5(c)**.
- **Certainty about the shapes still unproduced needs agentgateway's SOURCE**,
  not more stimuli. Fifteen inputs bound the claim; the mechanism in row 15
  explains it.
- **It does not show what the OPERATOR does in either state.** No operator runs
  here. §8.1 case 19 (f) is still the only thing that measures the reaction, and
  it still writes the report rather than producing it.
- **It measures one gateway replica**, as everything else in this repository
  does, and one release, 1.5.0.
- **`Accepted=False` on a policy stays unmeasured**, so the branch of the
  operator's judgement that keys on it rests on the CRD's documentation alone.

## The cases, and their mutations

`test/conformance/slice_attach_cluster_test.go`, run by
`make conformance-cluster`'s phase 2:

- `TestSliceAPolicyBrokenByItsKeySetStaysAttachedAndKeepsRefusing` — row 14,
  asserting the report, the accepted route, the unchanged digest, and 401.
- `TestSliceAnUnattachedAuthPolicyLeavesAnAcceptedRouteOpen` — row 4, asserting
  the synthetic ancestor, the accepted route, **200 with a body**, that the
  stimulus really drifted the stored object, and that removing the
  `sectionName` restores the 401.
- `TestSliceAPartlyResolvedPolicyReportsBothAncestors` — row 15, asserting both
  ancestors at one generation, the real one reading `Attached=True`, and the
  route enforcing `401`.

Each mutation is one edit, rebuilt, run against both cases, and restored from a
backup copy.

| mutation | result |
|---|---|
| write a VALID entry in the second key ConfigMap instead of a rejected one | KILLED — the report never becomes `PartiallyValid` |
| expect 200 from the anonymous request in the key-set case | KILLED — it got 401, three times |
| drop the bogus `sectionName` from the unattached case's patch | KILLED — the policy never reports `Attached=False` |
| expect 401 from the anonymous request in the unattached case | KILLED — 29 consecutive 200s |
| replace the unattached case's stimulus with the LISTENER RENAME | KILLED at the route gate: "route … reads Accepted=False … this case measures the POLICY half and needs an ACCEPTED route" — which is what makes the case a separation of the halves and not a second walkthrough |
| expect the synthetic ancestor under another `group` | KILLED — `policyReport` keys the fail-open signal on `group == "agentgateway.dev"` as well as the name, and an upstream rename of either takes A80's policy half silent while the bypass stays real |
| expect the real Gateway ancestor in another `namespace` | KILLED — `policyReport` compares all four ref fields, so all four are asserted |
| wrong `controllerName` in the route gate | KILLED — the gate's match on agentgateway's controller is live |
| the canary's hash does not match its key | KILLED — the freshness control fires when the ConfigMap is not what the case thinks |
| case (C) stops requiring the real ancestor to read `Attached=True` | KILLED |
| case (C) expects a partly resolved policy to stop enforcing | KILLED — 29 consecutive 401s |

Five more, against the untagged transcription in `ancestor.go`, run by `make conformance` with no cluster:

| mutation | result |
|---|---|
| call `Accepted=Unknown` a break — the drift the transcription actually had | KILLED |
| count an absent condition as known | KILLED |
| stop treating the synthetic ancestor as a break on its own | KILLED |
| let `converged` accept a reason other than `Valid` | KILLED |
| let `converged` accept the synthetic ancestor | KILLED |
| read the synthetic signal from the GROUP alone | KILLED |
| read the synthetic signal from the NAME alone | KILLED |

And two against the OPERATOR, run in envtest against the repair row §8.1 case 19 (f)
gains — the only mutations in this change that touch product code:

| mutation | result |
|---|---|
| `reassertServedPolicy` digests the STORED object instead of the render — what A82's first draft believed the code did | KILLED: the guard rejects the edited policy and nothing is repaired |
| `writeAuthPolicy` leaves a drifted `spec` alone | KILLED |

Every mutation here edits the TEST, not product code — no operator runs in this
suite — so what they pin is agentgateway's output and the cases' assertions.
Nothing here fails if `policyReport` changes.

The report's three answers — broken, holding, and the unknown between them — and
§3.3.2's stricter converged tuple are pinned by
`test/conformance/ancestor_test.go`, untagged and inside `make test`, **which no
CI job runs**: `.github/workflows/ci.yml` runs `verify`, `vet`, `unit`, `race`,
`envtest`, `chart`, `chart-conform` and `e2e`. The rows exist for the reason
`statusIsCurrent` gives in `status.go`: the `Unknown` and absent-condition arms
are unreachable on 1.5.0, which writes both conditions with a `True`/`False`
status, so a cluster run can never exercise them and they would otherwise be
defensive code no test can pin. They are a TRANSCRIPTION of `policyReport`, not
a call into it — that function is unexported — and **the transcription had
already drifted once**: an earlier `broken` called `Accepted=Unknown` a break
where `policyReport` counts it as known and not broken, so a cluster case could
have gone green on a state the operator treats as clearing. A real cross-check
needs `policyReport` exported or moved, and is owed.

All thirteen `TestSlice` cases pass together on one cluster after these, and the
whole `make conformance-cluster` gate — both phases, both clusters provisioned
from scratch — passes with no SKIP: phase 2 at 28 PASS, the three new cases at
23.3 s, 2.1 s and 1.6 s.

The `PartiallyValid` report appeared **26 ms** after the ConfigMap was written,
on a warm cluster and on a cold one alike; the rest of the key-set case's 45.7 s
is fixture, publication and probing. The case logs that number because the
selected key set is found by a label re-list rather than by a watch on a named
object, so it was the step most likely to be slow and is measured rather than
assumed. Against the 2-minute wait the margin is four orders of magnitude,
which is the answer to "is this timeout about to flake".
