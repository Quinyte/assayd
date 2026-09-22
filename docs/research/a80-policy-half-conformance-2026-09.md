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
  the gateway. But reaching it took an edit to the policy's own `targetRefs`, so
  the live policy no longer renders to `appliedDigest` — and A80's precondition
  therefore excludes it. Rows 6 and 13 are the same shape and the same 200.

## What this shows

1. **A80's policy half is a real, reportable state, not an artefact of the
   reporting shape.** Row 14 reaches it with a digest-equal policy on an
   accepted route, so the operator's `AuthPolicyNotAttached` report is
   reachable and is not dead code.
2. **The consequence that report announces is not true of the state that
   reaches it.** In row 14 the route refuses anonymous requests. §5's
   "the route may be answering with no credential required" is a hedge that
   holds, but the reachable shape is the one where it is false.
3. **The bypass is real, in the shape the precondition excludes.** A policy the
   Gateway attaches to nothing leaves a published route wide open — 200 to
   anyone. Nothing in this probe reached that shape with a policy the operator
   would still recognise as its own.
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
5. **The two halves could not be separated in the other direction either.**
   Renaming the Gateway's listener, run again here, reproduces the walkthrough:
   `StatusSummary`, `Attached=False`, and the route `Accepted=False` on the same
   pass. That is the conflated state A81 ordered route-first, and it is the
   state the new cases' route gate refuses to measure (mutation B3 below).

## What it does not show

- **It does not show that no input reaches an unattached, digest-equal policy on
  an accepted route.** It shows that four stimuli that reach non-attachment all
  change the policy's spec, and that the two external changes tried (deleting
  the key set; deleting and re-creating the route) do not. A fifth input could
  exist — an agentgateway upgrade that resolves targets differently, a second
  controller, a partial write. The claim is bounded by what was tried, and what
  was tried is the table above.
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
  digest has CHANGED, and that removing the `sectionName` restores the 401.

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

The report's three answers — broken, healthy, and the unknown between them —
are pinned by `test/conformance/ancestor_test.go`, untagged and inside
`make test`, for the reason `statusIsCurrent` gives in `status.go`: the arm
where an ancestor carries only one of the two conditions is unreachable on
1.5.0, which writes both, so a cluster run can never exercise it and it would
otherwise be defensive code no test can pin. The rule it encodes is
`policyReport`'s: an absent condition is **unknown**, not one of §5's four
breaks.

All twelve `TestSlice` cases pass together on one cluster after these, and the
whole `make conformance-cluster` gate — both phases, both clusters provisioned
from scratch — passes with no SKIP: phase 2 in 156 s, the two new cases at
45.7 s and 2.1 s.

The `PartiallyValid` report appeared **26 ms** after the ConfigMap was written,
on a warm cluster and on a cold one alike; the rest of the key-set case's 45.7 s
is fixture, publication and probing. The case logs that number because the
selected key set is found by a label re-list rather than by a watch on a named
object, so it was the step most likely to be slow and is measured rather than
assumed. Against the 2-minute wait the margin is four orders of magnitude,
which is the answer to "is this timeout about to flake".
