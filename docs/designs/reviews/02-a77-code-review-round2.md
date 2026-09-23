<!--
SPDX-FileCopyrightText: 2026 Quinyte
SPDX-License-Identifier: Apache-2.0
-->

# Design 02 A77 — independent code review, round 2

**Subject** PR #54 `ensure-service-converges`, head `fdf6747`. Second independent round: the
first returned REVISE with a BLOCKER and six majors, all addressed, and the human then decided
the blocker's fix — so this change now makes the operator **delete a live Service in a run
namespace**, triggered by a `services/patch`. This round came at that path assuming it could be
made to destroy something it should not.

**Method** Own worktree at `fdf6747`, hash-verified pristine before every mutation and restored
by SHA-256 against `git show HEAD:` after every one. Baseline
`go test ./test/envtest/... ./internal/controller/...` — `envtest 300.1s ok`,
`internal/controller 4.2s ok`. Twelve mutations. Four adversarial states built and measured in
envtest against a real API server. Kubernetes semantics checked against the canonical API
reference rather than from memory.

**`make e2e` was not run.** Nothing in this diff changes a gateway, route or policy path, and
`hack/e2e.sh` on `main` has the shared-kubeconfig bug (fix unmerged in PR #61) while other
agents are active on this machine. No cluster was created or torn down.

## Verdict: REVISE. No BLOCKER.

**The delete path itself held.** Every state built to make it destroy a bystander failed to:
moving the replace ahead of provenance, dropping the UID precondition, and deleting on the
status disjunct are each killed by an existing test, and the UID precondition genuinely
converts a lost race into a report rather than a wrong delete. The dry-run is a real gate —
measured on a real API server, a dry-run `Create` against an already-taken name returns
`Invalid` from validation **before** `AlreadyExists`, so tolerating `AlreadyExists` does not
void it.

**"Passed every attack" and "is safe" are different statements here, and the gap is the point
of this review.** Two findings carry it. First, the absolute claim guarding the delete —
"Nothing this operator did not create is ever deleted here" — is **false**: the stamp it rests on
needs only `create`, exactly like the UID label it is contrasted with, and a forged-stamp object
was deleted and replaced in one reconcile. The test that pins the claim pins one half of it.
Second, the delete **authorises on a cached read**. No test in this repository can see that,
because envtest uses a direct client, so every guard was proven against a client the operator
does not use.

What is wrong is what the change **claims**, and what its tests **pin**. Four mutations
survived — each re-confirmed against the full suite, none of them flipping. Four claims are measurably false, and the most load-bearing safety
sentence on the destructive path — "a lost race NEVER returns nil" — is unpinned on two of
its three arms.

**Eight MAJOR, six MINOR, no BLOCKER.** `make verify`'s check passes: regeneration is
reproducible and committed, and `config/crd` and `charts/assayd/crds` are consistent.

## MAJOR 1 — "a lost race NEVER returns nil" is unpinned on two of its three arms

`internal/controller/service.go:341-342` (the claim), `:364-375` (the two unpinned arms),
`:377-385` (the pinned one). Design `docs/designs/02-agent-crd-operator.md:149`, `:482`,
mutation row `:1095`.

The claim is stated three times — in the function's own doc bullet ("A lost race NEVER returns
`nil`. Returning success let the pass go on to readiness, promotion and the route, so the
operator credited whatever had taken the name with the Agent's traffic and reported
`Ready=True` about it"), in §3.2's row, and in §5's row `:482` ("a **lost UID precondition
never returns success**, so the pass stops instead of promoting and routing over a stranger's
object"). `replaceUnrepairableService` has three failure arms after the dry-run. Only one is
measured.

| Arm | Mutation | Result |
|---|---|---|
| create failed after the delete (`:382`) | M5 — return `nil` | KILLED by `TestAFailedRecreateIsReportedAndDoesNotPromoteOverWhateverTookTheName` |
| **delete failed — the lost UID precondition (`:372`)** | **M9 — return `nil`** | **SURVIVED** |
| **delete returned NotFound — "vanished" (`:368`)** | **M12 — return `nil`** | **SURVIVED** |

Under M9 the shipped harm is reachable end to end: the operator reads its own stamped headless
Service, dry-runs, issues the delete, loses the precondition because the name now holds
somebody else's object, returns success, and the pass goes on through `workloadAvailable`, the
promotion and `reconcileGateway` — publishing a route whose `backendRef` names that name, with
`Ready=True`. That is the sentence `:482` says cannot happen, and nothing in the suite notices
when it does. M12 is the same harm through the NotFound door.

**The code is correct today.** The defect is that nothing holds it there, on the one path in
this operator that destroys a live object — and rule 1 says an unpinned assertion is this
repository's default failure mode, not bad luck.

The fixture for M9 already exists and is two lines short.
`TestTheReplaceDeletesTheObjectItReadAndNotTheName` (`test/envtest/service_test.go:1825`) builds
precisely this state and then asserts only the objects, explicitly tolerating any reconcile
outcome (`:1871`, "a lost race is allowed to error; destroying the bystander is not"):

```go
ready := condition(liveAgentPtr(t, a), assaydv1alpha1.CondReady)
if ready == nil || ready.Status != metav1.ConditionFalse ||
    ready.Reason != controller.CondReasonServiceReplaceFailed {
    t.Fatalf("the pass returned success after failing to delete, so it goes on to promote "+
        "and route over the object that took the name: %+v", ready)
}
```

M12 needs one new fake, modelled on `swapBeforeDelete` (`:1798`) but deleting without
recreating, then the same assertion. Add both rows to §12's mutation table, which currently
lists only the create arm.
## MAJOR 2 — the destructive delete decides on a CACHED read, against this repository's own A60/A61 rule

`internal/controller/service.go:110` (the read), `:364` (the delete), reached from
`agent_controller.go:616`.

`cmd/operator/main.go:154-156` disables the client cache for `ConfigMap`, `Secret` and
`Namespace` only, and `AgentReconciler.Client` is `mgr.GetClient()` (`:191`). `corev1.Service` is
therefore cached, so the `existing` object on which every guard in `replaceUnrepairableService`
reasons is an informer read that can be stale. Eight lines above that option list, this
repository already wrote down why that matters — for the object one layer over:

> "Namespaces are uncached too: the run-namespace protocol compares a namespace's UID against
> its binding record on every reconcile, and **a comparison against a cached object that a
> recreate has already replaced is no proof at all** (design 02 A60/A61)."
> — `cmd/operator/main.go:149-152`

That is the argument for this path too, and this path is the more destructive of the two: A60/A61
was about refusing to *act*, this is about deciding to *delete*.

Two consequences, neither measured and neither in §5:

**(a) A false `Degraded` blaming something that does not exist.** A successful replace writes
`status.serviceReplacedAt`, and that status write re-enqueues the Agent immediately —
`SetupWithManager` takes `For(&assaydv1alpha1.Agent{})` at `agent_controller.go:1850` with no
generation predicate, which the PR's own `carryGatewayReport` comment relies on (`:1390-1396`).
If the next pass's cached `Get` still returns the object just deleted, it is stamped, headless
and inside the cooldown, so the Agent reads `Ready=False` / `Degraded` /
`RevisionServiceReplaceHeld` telling its operator to "look for an admission policy that forces
spec.clusterIP, or a principal with services/patch on this namespace" — about a Service that is
healthy.

**(b) An unnecessary destroy of a healthy object of ours.** The UID precondition protects a
*bystander* — a different object at the name has a different UID — but not a stale *content*
read of the same object. A human who repairs the Service by the same two-patch route this PR
documents (`ExternalName`, then `ClusterIP` with a real address) keeps the UID. On a stale read
the precondition passes and the operator deletes that repair, costing a real traffic gap and a
new ClusterIP. That lands on precisely the object every refusal message sends a human to touch.

Neither is reachable in the suite: `test/envtest/reconciler_test.go:95` sets `Client: k8s`, a
direct client. So "the delete is reachable only for an object carrying this operator's stamp" is
proven about a client the operator does not use.

**Fix.** Re-read `existing` through `r.Reader` — the uncached `mgr.GetAPIReader()` the reconciler
already holds (`agent_controller.go:121-123`) — at the top of `replaceUnrepairableService`, and
re-run the stamp and shape test on that read before the dry-run. The stated reason for not
re-reading ("a second read could return a different object under the same name",
`service.go:324-325`) is exactly the case the UID precondition already covers, so it argues for
keeping the precondition — which stays — not against an uncached read. Either way §5 needs a row:
a destructive decision taken on cached data is a residue, not an implementation detail.
## MAJOR 3 — `internalTrafficPolicy` is asserted by no test, and §5 still says it is not read

Code: `internal/controller/service.go:74`, `:236`, `:291`, `:562`, `:625-630`.
Design: `docs/designs/02-agent-crd-operator.md:149` and `:1064` say it IS read; `:473` says it
is NOT; `:476`'s enumeration omits it; `:1047` explains why it was added.

**M8 — remove the field from the converge (`:236`) and from `equalService` (`:562`) — SURVIVED
the full `./test/envtest/... ./internal/controller/...` suite.** The string
`internalTrafficPolicy` appears nowhere in `test/envtest/service_test.go`.
`TestTheOperatorsOwnServicePatchedInPlaceIsConvergedBack`'s table has five rows — `externalIPs`,
`publishNotReadyAddresses`, `ExternalName`, `NodePort`, `LoadBalancer with a source range`
(`:474-520`) — and no sixth. §12's row `:1087` calls that test "five fields repaired", which is
accurate and is the tell: the enumeration has six fields and the fixture has five.

So the field this amendment added, on the argument that its `Local` value black-holes the
gateway's hop to the agent, is rule-5 code: it reads as load-bearing and nothing pins it.

**The Kubernetes half of the argument is right**, which is why this is worth fixing rather than
reverting. From the canonical API reference: `internalTrafficPolicy: Local` "routes traffic only
to endpoints on the same node as the client pod, **dropping traffic if no local endpoints
exist**" — a drop, not a steer, exactly as `:1047` says. And `trafficDistribution` is "a hint …
not required to guarantee strict adherence", so `PreferSameNode`/`PreferSameZone` degrade
locality rather than black-hole — also as `:1047` says, so leaving it un-read is the right call
for the right reason.

**But §5's residue row `:473` has not caught up.** Titled "The fields §3.2's shape check does NOT
read", it still lists `internalTrafficPolicy`, and justifies the whole list with "None of them
sends traffic anywhere but this revision's own Pods — they change which of those Pods, or by
what path" — which is the exact sentence `:1047` identifies as the error that let the field
through:

> `:1047` — "… That is a per-Agent denial of service with no report, which an earlier version
> of §5 bounded away as 'they change which of those Pods, or by what path'."

A reader consulting the residue table to learn what is unprotected is told the opposite of the
rule, under a justification the amendment repudiates one screen away. The row also says "those
five fields" while naming four.

**Fix.** Add the sixth row to the converge table:

```go
{"internalTrafficPolicy Local", func(s *corev1.Service) {
    s.Spec.InternalTrafficPolicy = ptr(corev1.ServiceInternalTrafficPolicyLocal)
}},
```

Strike `internalTrafficPolicy` from `:473`, fix its count, and replace the repudiated
justification for the fields that genuinely do remain un-read. Add `internalTrafficPolicy` to
`:476`'s enumeration. (Minor, same area: `:1047` and `service.go` give `PreferClose` and
`PreferSameNode` as the `trafficDistribution` pair; the current set is
`PreferSameZone`/`PreferSameNode`, with `PreferClose` a deprecated alias for the former.)
## MAJOR 4 — the refusal that is NOT a held replace reports itself as one, with a zero timestamp

`internal/controller/service.go:205-207` returns `shape` for the unstamped-vouched headless
object; `:490-501` renders `heldSince`, which that path never sets.

Built the state — unstamped, forged `assayd.dev/agent-uid`, headless, at a revision `status`
vouches for, which is the fixture `TestAnUnstampedVouchedServiceIsRefusedNotDeleted` uses — and
read the condition back from the API server:

```
REASON:  RevisionServiceReplaceHeld
MESSAGE: service …/msgprobe-126eb2a71a cannot be repaired in place. It is headless
  (spec.clusterIP: None) … This operator already deleted and recreated it at
  0001-01-01T00:00:00Z and it is unrepairable again, so it is being left alone rather than
  replaced a second time inside 10m0s — replacing it on every pass would be a delete, a new
  ClusterIP and a fresh traffic gap each time. Something is putting it back: look for an
  admission policy that forces spec.clusterIP, or a principal with services/patch on this
  namespace. To recover: stop whatever is rewriting it, then delete that Service and let the
  operator recreate it.
```

Every clause after the first sentence is false of this state. The operator never replaced it. It
was never repaired, so it cannot be "unrepairable again". Nothing is putting it back. The cause
is a principal with `services/create` who took the name, not a webhook forcing `clusterIP` — and
the reader is sent hunting for an admission policy that does not exist. The one fact that
matters, and the one the design spends a paragraph on at `:484` — **the operator is refusing
because it cannot establish it created this object** — is never said.

`service.go:491-492` is why the bug exists: "Reached only when the replace was HELD: every other
unrepairable Service is deleted and recreated rather than reported." That is not true of `:205`.

**The mechanism, stated precisely, because the fix depends on it.** `heldSince` is a value
`time.Time` (`:433`), not a pointer. `serviceShapeError` has exactly two construction sites:
`:349`, the cooldown branch, which sets `heldSince: status.ServiceReplacedAt.Time`; and `:515`,
inside `serviceNotRendered`, which sets only `ns`, `name` and `typ`. So on the `:205` path the
field is **never assigned by anything** and holds `time.Time`'s zero value, which `Error()`
renders through `:500` as `0001-01-01T00:00:00Z`. It is **unset**, not written-wrongly — there is
no path that writes a zero here. `serviceNotRendered` is a shape classifier and has no business
knowing about the cooldown, so it is not at fault either.

**But the timestamp is the symptom, not the defect, and fixing only it is not enough.** The
defect is that one error type carries two distinct situations — "we held the replace" and "we
cannot establish we created this object" — and *both the message and the **reason*** are rendered
as though only the first existed. `agent_controller.go:663-664` sets
`CondReasonServiceReplaceHeld` for **any** `serviceShapeError`, whichever site built it. So a
guard on `heldSince.IsZero()` inside `Error()` would fix the prose and leave
`Ready.Reason = RevisionServiceReplaceHeld` standing on an object that was never held — and the
reason is what an alert keys on, which is this PR's *own* argument for introducing
`revisionCollisionError.reason()` (design `:1067`: "the reason is what an alert keys on, so an
operator whose object was refused for carrying no stamp was told two projections had collided").
The same argument, one type over.

**Fix — and one part of it is a design question I am deliberately not answering.** The code half
is clear: give `serviceShapeError` a discriminator (a second `shapeFault`-style field, or split
the type) so that the controller selects both the reason and the message from it, exactly as
`reason()` does for `revisionCollisionError`. Assert the message, not only the reason, in
`TestAnUnstampedVouchedServiceIsRefusedNotDeleted` — it currently checks the reason alone, which
is how this passed.

**What I am not certain of, and will not pick:** which reason the not-ours case should carry.
§5's row `:484` explicitly says this class is `RevisionServiceReplaceHeld` — so the design
endorses the reason I am calling wrong. Either `:484` is stale in the same way `:473` and `:476`
are, and the case wants its own reason (or `Unstamped`, matching the provenance ground it
actually failed); or the design means the class deliberately and the message must carry the whole
distinction. That is a call for whoever owns A77, not for this review, and the fix differs
substantially between the two.

## MAJOR 5 — "Nothing this operator did not create is ever deleted here" is false, and the test pins only the weaker half

`internal/controller/service.go:180`, `:198-204`, `:322`; design `:149`, `:484`, mutation row
`:1096`; PR body ("stamp, not merely provenance").

The narrowing at `:205` requires `stamped && existingDigest == digest`. The stated warrant is
that the UID label "needs only `create`" while the stamp does not. **The stamp needs only
`create` too.** The digest is a pure function of the spec — this repository says so itself, in
this very file: "a pure function of that spec, no secret" (`agent_controller.go:478-484`) — and
it is readable straight off the revision's Deployment or Service annotation by anyone with `get`
in the run namespace, which is the principal §5:484 already models.

Measured. Planted an object with a forged `assayd.dev/agent-uid` **and** a forged
`assayd.dev/revision-digest`, headless, at the revision's name. One reconcile:

```
RESULT: the planted object WAS DELETED AND REPLACED
        (1be8fd34-…-5e4411bff8db -> 245034fc-…-fb03e043f4dc)
Ready=True/Available
```

`TestAnUnstampedVouchedServiceIsRefusedNotDeleted` does not pin the claim — it pins the
unstamped case only, under a comment that asserts the general one.

**This is not ranked a security blocker, and the reasoning matters**: the only object reachable
at that name is one the adversary planted, so forging the stamp buys them a *repair* where
leaving it unstamped buys them a wedge — strictly worse for them. The defect is rule 7. The
narrowing is worth keeping, because it protects the compatibility-adoption case the `vouched`
disjunct exists for; it is simply not an adversarial boundary and must not be written as one.

**Fix.** Say what the rule actually buys — the delete is reachable only for an object the
operator can *correlate* with this revision, which excludes the compatibility-adoption path and
does not exclude a forger. Correct `service.go:180` and `:198-204`, §3.2's row `:149`, §5's row
`:484`, mutation row `:1096`, and the PR body. Add the forged-stamp case to the test table so
the record and the measurement agree.

## MAJOR 6 — §5 still asserts the `loadBalancerIP` claim the same document calls false

`docs/designs/02-agent-crd-operator.md:476` against `:1046` and `service.go:248-253`.

Row `:476`: "The API server's own `dropTypeDependentFields` clears `externalTrafficPolicy`,
**`loadBalancerIP`**, `allocateLoadBalancerNodePorts`, `healthCheckNodePort`,
`loadBalancerClass` …". Row `:1046`: "An earlier version of this amendment also listed
`loadBalancerIP` as auto-cleared; **that is false** — a repaired Service keeps it." The code
comment agrees with `:1046`. Row `:476` is the uncorrected copy, and it was added by this PR
(`36581c2`).

Measured on envtest 1.36:

```
as LoadBalancer:      loadBalancerIP="10.0.0.7"  externalTrafficPolicy="Local"  healthCheckNodePort=30567
after type=ClusterIP: loadBalancerIP="10.0.0.7"  externalTrafficPolicy=""       healthCheckNodePort=0
```

`loadBalancerIP` survives; the other two are dropped. `:476` is the row that explains *why* a
field is absent from the enumeration, so a false entry there gives a false reason for an absence
— which is how `loadBalancerSourceRanges` became the tenth review's blocker in the first place.

Same row carries a stranded sentence — "… is not what this is (A77) Its carry rides the same
helper and is asserted by nothing …" — with no separator and no antecedent; it belongs with row
`:475`, which is about the `ServiceRejected` carry.

**Fix.** Delete `loadBalancerIP` from `:476`, add `internalTrafficPolicy` to its enumeration,
and move the stranded sentence back to `:475`.

## MAJOR 7 — the owner-edit escape is right to defer the repair and wrong to stay silent

`test/envtest/service_test.go:1728`; design `:483`.

**The test is well built and I would keep it.** It fails in three directions, each with a message
routing the fixer back through §5: promotion changing (`:1768`), the active revision's Service
being repaired (`:1773`), and `Ready` no longer reading True (`:1779`). Asserting a defect so
that a fix must come through the record is the right instinct, and the process argument for
deferring the *repair* — widening a destructive delete's reach in the same PR that introduces it
is the wrong order — I agree with, and would have made myself.

**What should not be deferred is the report.** The measured state is:

```
activeRevision="126eb2a71a"  phase="Ready"  Ready=True/Available  clusterIP="None"
```

An Agent whose serving route resolves to an address that does not exist, reporting `Ready=True`,
with nothing in `status` saying otherwise. That is NFR-8's silent degraded path and rule 8's
loud-and-wrong together, and §5:483 itself names the in-scope repair — "*replace, but never
create,* an unrepairable Service at `status.activeRevision`" — while shipping neither it nor a
report.

A read-only check of `status.activeRevision`'s Service widens the delete's reach not at all, and
does not change what `Ready` means if it lands on its own condition type. It is the cheap half,
and it is what stops this being invisible.

**Fix.** Take the third repair §5 already names, or raise a condition naming the broken
active-revision Service — its own type, not `Ready`. If the human prefers to ship it silent,
§5:483 must say that the Agent reports `Ready=True` over a dead address and that *nothing*
reports it, rather than describing the escape as a convergence-scope gap.

## MAJOR 8 — the `ServiceRejected` exit is reachable, its message is wrong there, and §5 says it is unreachable

`internal/controller/agent_controller.go:678-705`; design `docs/designs/02-agent-crd-operator.md:1102`.

§5's mutation row `:1102` says of dropping this exit's requeue and remedy clause: "**SURVIVED.**
Nothing reaches that exit once the coupled field is in the enumeration; §5 records the guard as
unmeasured rather than claiming it." That reasons only about the **converge**. It says nothing
about the **create**, and the create reaches it.

Measured — a fresh Agent under an admission policy that refuses Service creates, which is the
likeliest real deployment of a VAP or webhook over `services`:

```
REASON:  ServiceRejected
PHASE:   Degraded
MESSAGE: create service vaprefuse-126eb2a71a: services "vaprefuse-126eb2a71a" is forbidden:
  every Service must carry a cost-centre label. This operator could not repair its own Service
  in place. To recover: delete that Service and let the operator recreate it — and if an
  admission policy refused the repair, that policy will refuse the re-creation too, so fix or
  exempt it first.
```

Two defects in one line. The bound in `:1102` is false — the exit is reachable and nothing in the
suite touches it (`grep -rn ServiceRejected test/` returns one comment, at
`service_test.go:1352`), so the requeue and the remedy clause are unmeasured for a reason the
design gets wrong. And the message, which this PR added (`agent_controller.go:693-696`), is
loud-and-wrong on the path that actually reaches it: nothing was repaired **in place** because
nothing existed to repair, and "**delete that Service** and let the operator recreate it" sends
an operator to delete an object that was never created. Rule 8.

**Fix.** Branch the message on whether the rejection came from the create or the converge — the
call site knows, since `ensureService` wraps them differently (`service.go:120` vs `:269`) — and
say "the operator could not create its revision Service" with the policy as the remedy. Then
correct `:1102`: the exit is reachable from the create path, and the fixture is already in the
file (`refuseCreate`, `service_test.go:2117`) pointed at an Agent that has not settled.

## MINOR

1. **A successful replace does not check that what it created has a ClusterIP.**
   `service.go:377-385`. `r.Create` populates `desired` from the response, so a mutating webhook
   forcing `clusterIP: None` on create leaves `ensureService` returning `nil`, and the pass
   promotes and routes with `Ready=True` over a headless Service for one pass before the next one
   holds. One line closes it. This is the third "path out of the replace that still returns
   success" — distinct from M9 and M12, which return success after failing.
2. **M11 — the `since >= 0` clock-skew guard is pinned by nothing.** `service.go:348`. A
   `status.serviceReplacedAt` in the future (an NTP step back after the write) turns the bound
   off, so the replace runs every pass while the skew lasts — the loop the bound exists to stop,
   in combination with the webhook case it exists for. The guard is right and deliberate; §12's
   row `:1093` credits the future-dated direction to
   `TestTheReplaceBoundIsNotWritableByWhoeverBreaksTheService/a future-dated annotation …`, which
   writes an **annotation** the shipped code does not read — so it kills M3 and does not pin this.
   Pin it through the status subresource, and add a §5 sentence: the bound fails *open*
   (destructive) on backward skew, and living in `status` is what makes it unforgeable, not what
   makes it monotonic.
3. **`serviceReplacedAt` ships with no description.** Both CRD copies carry the doc comment on
   `serviceReplacedRevision` only, so `kubectl explain agent.status.serviceReplacedAt` returns
   nothing. (The two copies are otherwise consistent — `config/crd` and `charts/assayd/crds`
   diffs are byte-identical, checked.)
4. **The dry-run probe is not the object that gets created.** `service.go:357` copies `desired`
   before `:376` adds `ServiceReplacedAnnotation`, so a policy keyed on annotations passes the
   probe and refuses the create. Handled (reported), but the probe should carry what it probes for.
5. **Stale refuse-era prose.** `service.go:420-423`: "It is terminal by design … the remedy is a
   human deleting the object." Both states returning this type now requeue every minute
   (`RefusedServiceRecheck`), and `Error()` at `:491-492` states a scope the type doc contradicts.
6. **`PreferClose` is a deprecated alias.** `service.go` and `:1047` give `PreferSameNode` and
   `PreferClose` as the `trafficDistribution` pair; the current set is
   `PreferSameZone`/`PreferSameNode`, `PreferClose` aliasing the former.

## What I verified and found sound

- **The dry-run genuinely gates the delete.** The tolerance of `IsAlreadyExists` at
  `service.go:358` looked like it could hollow the gate out, since the object being replaced
  still holds the name. Measured against a real API server: a dry-run `Create` of an invalid
  Service at an occupied name returns `Invalid` from validation, **not** `AlreadyExists` —
  `rest.BeforeCreate` and the validating-admission callback both run before the registry reports
  the conflict. So admission policies and spec validity are genuinely exercised. M4 and M10 are
  killed, and a dry-run success followed by a real failure is reported (M5 killed).
- **The bound is unwritable by the adversary it exists to stop.** `agents/status` is granted only
  by the operator's own ClusterRole (`config/rbac/role.yaml:98`,
  `charts/assayd/files/operator-rules.yaml:93`); no user-facing role has it. M3 and M7 killed.
- **No path deletes a bystander.** M1, M2 and M6 are each killed, and the `swapBeforeDelete`
  fixture is a real race rather than a stub. A stale cached read makes the precondition *fail*
  rather than fire on the wrong object, so MAJOR 2's (b) is the only destroy it enables, and only
  against our own object.
- **Both Kubernetes claims behind the `internalTrafficPolicy` decision are correct**, checked
  against the canonical API reference rather than from memory.
## Mutations re-run

Twelve, each applied to `internal/controller/service.go` from a hash-verified pristine copy, each compiled and `go vet`-clean before running (a mutation that does not compile is INVALID, not SURVIVED). Restore verified by SHA-256 against `git show HEAD:` after every run. M1 and the four survivors were re-run against the **full** `./test/envtest/... ./internal/controller/...` suite; the rest against the service + collision families plus the controller unit tests, which is where every killing assertion lives.

| # | Mutation | Result |
|---|---|---|
| M1 | Move the replace AHEAD of the provenance switch | KILLED — `TestAnUnrepairableServiceThatIsNotOursIsRefusedAndNeverDeleted` (both grounds), `TestAPlantedServiceIsRefusedAndItsShapeIsNamed/headless` |
| M2 | Drop the UID precondition on the delete | KILLED — `TestTheReplaceDeletesTheObjectItReadAndNotTheName` |
| M3 | Read the bound from the annotation again | KILLED — both directions of `TestTheReplaceBoundIsNotWritableByWhoeverBreaksTheService`, plus `TestASecondUnrepairableServiceInsideTheWindowIsHeldNotReplacedAgain` and two shape cases |
| M4 | Drop the dry-run before the delete | KILLED — `TestTheReplaceIsNotAttemptedWhenTheReplacementWouldBeRefused` |
| M5 | A failed recreate returns `nil` | KILLED — `TestAFailedRecreateIsReportedAndDoesNotPromoteOverWhateverTookTheName` |
| M6 | Delete on the status disjunct too (drop the stamp narrowing) | KILLED — `TestAnUnstampedVouchedServiceIsRefusedNotDeleted` |
| M7 | Drop the cooldown bound entirely | KILLED — `TestASecondUnrepairableServiceInsideTheWindowIsHeldNotReplacedAgain` and four more |
| M10 | A refused dry-run returns `nil` | KILLED — `TestTheReplaceIsNotAttemptedWhenTheReplacementWouldBeRefused` |
| **M8** | **Drop `internalTrafficPolicy` from the converge and from `equalService`** | **SURVIVED** |
| **M9** | **A lost UID precondition returns `nil`** | **SURVIVED** |
| **M11** | **Drop the `since >= 0` clock-skew guard on the cooldown** | **SURVIVED** |
| **M12** | **The "vanished before it could be replaced" arm returns `nil`** | **SURVIVED** |

Every mutation the brief named is killed. The four survivors are the findings.
## The first round's majors — spot-checked

| # | Major | Landed? |
|---|---|---|
| 1 | `loadBalancerIP` no longer claimed auto-cleared | **PARTIAL** — `service.go:248-253` and design `:1046` corrected; design `:476` still carries the false claim verbatim, added by this PR (`36581c2`). Measured false (MAJOR 6) |
| 2 | `ServiceRejected` widened past `IsInvalid`, and its fixture | **PARTIAL** — `rejectedByAPIServer` (`agent_controller.go:1303-1317`) is a transient-allowlist and reports `Forbidden`. **No test exercises that exit at all**; `grep -rn ServiceRejected test/` returns one comment. §5:1102 excuses it with a bound that is not true (MINOR 1) |
| 3 | The leftover refuse-cut paragraph at the old `:1077` | **LANDED** — gone; `service.go:159-195` frames refusal as reversed history. One stale line survives at `:420-423` (MINOR 7) |
| 4 | The reason-rejection at the old `:1059` | **LANDED** — `revisionCollisionError.reason()` (`agent_controller.go:1268-1276`) returns `ForeignObject`/`Unstamped`/`DigestMismatch`, used by `reportCollision` (`:1427`), all three pinned by `TestEachWorkloadCollisionGroundReportsItsOwnReason` (`collision_test.go:483`) on both the workload and Service paths |
| 5 | `equalStrings` | **LANDED** — deleted tree-wide, with the residue recorded at `service.go:551-557` |
| 6 | The A97 typo | **LANDED** — no `A9[0-9]` anywhere in `docs/designs/`; numbering self-consistent |
| 7 | Leaked design deliberation in the user-facing message | **LANDED** — no string in `internal/controller/*.go` mentions review rounds, "the human decided" or deliberation. The one new user-facing string carrying an amendment number, `compileCarriedNote` (`agent_controller.go:1417-1419`), matches the established house convention (`carriedNote` ends "(design 03 A75)"; ten more shipped condition messages cite design numbers) |

## Design critique — A77 as it now reads

**Does it state what happens rather than what is intended?** Mostly yes, and the structural
honesty is real. §3.2's row `:149` — the first place a reader meets the rule — says plainly
"**One field cannot be repaired by an Update, and that object is DELETED and recreated**", and
§5 gives it a row of its own titled "**The operator DELETES its own revision Service here**"
(`:480`) that names the cost ("a new ClusterIP and a real gap") and says the gap is not measured
and that envtest cannot measure it. That is the right shape and it is where a reader will find
it.

**Are the residues named without dressing up?** The four the brief asks after are:

- *the silent overwrite* — `:479`, named at length, including the half that matters (the
  detection loss, not just the lost hand-edit), and attributed to the human's decision. Good.
- *the narrowed-not-closed name window* — `:482`, three narrowings named, "What remains is the
  round trip itself, which nothing here can make atomic". Good — except that one of its three
  narrowings is the unpinned one (MAJOR 1).
- *the owner-edit escape* — `:483`, measured, dated, with the state quoted and the in-scope fix
  named. Honest about the repair; silent about the report (MAJOR 7).
- *`trafficDistribution`* — reasoned correctly at `:1047`, contradicted at `:473` (MAJOR 3).

**Is anything present-tense and unenforced?** Three things, all in §5, and all of them are the
document failing to catch up with its own amendment rather than over-claiming about the future:
`:473` (the field is read), `:476` (`loadBalancerIP` is not cleared), `:1102` ("nothing reaches
that exit"). Rule 7 is about exactly this class. §5 is the row a future reader consults to learn
what is *not* guaranteed, which makes a stale row there more costly than a stale one in the body.

One smaller inconsistency: the rule table at `:1065` says "bounded to one replace per 10
minutes" where `:149` and `:481` correctly say "per revision per".

**Not a finding, recorded as incidental:** design 02's Status line (`:3`) still says "A72
(2026-09-10) is the first amendment since the consolidation" and `docs/designs/README.md` row 2
stops at A72, while §12 carries A73–A77. That staleness predates this PR (A73–A76 are on `main`)
and is `audit-docs` work, not A77's.
