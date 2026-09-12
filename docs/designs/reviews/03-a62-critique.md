# Design 03 (policy compiler): critique of A62, scoped to the first slice (§1.1)

- **Date**: 2026-09-12.
- **Reviewer**: an independent adversarial critique, following `.claude/skills/critique-design/SKILL.md` and AGENTS.md. It is a cold read with no author context. This is the twelfth critique of the consolidated body, and the seventh to read §1.1.
- **Target**:
  - `docs/designs/03-policy-compiler.md` at `origin/design-03-a60` `b481f52`;
  - `docs/designs/reviews/03-a61-critique.md`, whose findings A62 answers.
- **Scope**: the human's decision G limits approval to §1.1's slice. `Narrow`, `Loosen`, E2, F2's group rules, D2, `Withdraw`, the other concerns and the candidate route are out of scope. A finding is raised against them only where the slice depends on them or they contradict it.
- **Method**:
  - Each finding of the eleventh critique was checked in the body, at its source.
  - A62's diff was then read line by line for new defects. Three things were traced through the rules they must live beside:
    - the displacement rule (a route re-create over a recorded `apikey` displaces a missing-policy `Lock`), against crash and re-entry, and against a route delete that lands while the `Lock` is in `ApplyingPolicies`;
    - the re-creation test at 03:452, against every way the slice can put a transaction in the slot;
    - the tightened `beforeObserved` rule, against the fields the CRD actually has.
  - The slice's claims were checked against the code they land in: `internal/controller/{agent_controller,card,httproute}.go`, `api/v1alpha1/agent_types.go` and `test/envtest/card_test.go`.
  - Nothing was run against a cluster. No file other than this one was written.

## Verdict for the first slice: **REVISE — 0 BLOCKER, 1 MAJOR, 3 MINOR.**

A62 closes the eleventh critique's MAJOR 2 fully, and closes its MAJOR 1 for the case that critique described, where the two watch events arrive in either order. It also closes MINORs 1–4. The re-creation test at 03:452 misclassifies nothing I could construct. The tightened `beforeObserved` rule can be implemented with the fields `status.cards[]` already has.

One defect remains, and it sits in the trigger A62 chose. The displacement fires on "a deleted `<agent>-serving`" (03:728). Nothing says the status update that records the displacing `Create` comes before the prepared route is created. The shipped reconcile writes the route first and the status after (`internal/controller/agent_controller.go:733`, then `:773`). So a crash between those two writes leaves the missing-policy `Lock` in the slot beside a route that exists with no `backendRefs`. The trigger no longer fires, because the route is no longer deleted, and the eleventh critique's M1 wedge comes back after one crash (MAJOR 1). The eleventh critique's fix carried the sentence that would have closed this ("A missing-policy `Lock` is entered only while `<agent>-serving` exists with `backendRefs`"), and A62 did not adopt it.

The fix is one sentence plus one crash half in case 16. With it in, I would judge the slice approvable.

---

## Closure against `reviews/03-a61-critique.md`

| Finding | This pass |
|---|---|
| **M1**: a missing-policy `Lock` kept the slot while the route was re-created prepared | **Closed for the case given.** 03:728: "**A missing-policy `Lock` is displaced.** When the recorded mode is `apikey`, the route re-create records `{kind: Create, targetMode: apikey, targetDigest}` from `status.auth` in the slot." The displacing `Create` has the `Publishing` stage that `Lock` lacks (03:406). The route-first order is covered by 03:603: "A missing policy beside a `Create` or `Lock` transaction re-enters that transaction instead". Case 16 (03:1289) runs both orders, with a mutation that the policy-first run fails. **Not closed across a crash** inside the re-create (MAJOR 1). §5's row was not updated (MINOR 1) |
| **M2**: `beforeRevision` was read from the route, but `beforeObserved` accepted any retained revision's card | **Closed.** 03:692 sets `beforeObserved` only when the card's digest equals "the digest `status.cards[]` records for the revision the route's one `backendRef` names at that moment", and says that "a card that matches only another revision's digest does not set it". 03:41, 03:62, 03:444–447 and 03:700 agree. Case 11's fifth half (03:1284) is the lagging-gateway scenario, and its mutation ("set `beforeObserved` on a match with any digest") fails it: under that mutation `beforeRevision` becomes r2, and r2's own `401` would count. Implementability is checked under "Verified sound" |
| MINOR 1: the stale-state rule still abandoned a route re-create | **Closed** (03:461: "A re-creation is the exception. Its target comes from `status.auth`, so it is compared with `status.auth` and never abandoned") |
| MINOR 2: nothing said how a re-creation is told apart | **Closed** (03:452). The test reads only stored fields. It is checked under "Verified sound" |
| MINOR 3: the message sentence named a condition that a `Create` does not raise before its deadline | **Closed as a stated residue.** 03:728 now says "Before the deadline, no other condition reports the unpublished window, which the delete caused". That contradicts 03:254, which is folded into MINOR 2 below |
| MINOR 4: cases 6 and 11 did not say how the test drives the rollout | **Closed.** Case 6 records the minted revision's card (03:1279). The envtest can do that through the existing card-client redirect (`test/envtest/card_test.go:154`). Case 11's first half holds r2 unready. 03:1284 says that "transition not observed" is the usual message on a real cluster |

The earlier open items stay as the eleventh critique recorded them, and I do not raise any of them again. The sentence at 03:728 that reads "An Agent recorded as `none` is published at once with its marker. Its kept or re-created `<agent>-auth` converges on it, and `Publishing` waits for the anonymous `401`" reads as if a `none` Agent waits on a policy. It was already there before A62 (A61's line 727), and A62 did not make it worse. The human's open question on an `Adopt`ed owner's `none` → `apikey` (03:83) does not block.

---

## MAJOR

### M1: the displacement fires on a deleted route, so a crash inside the re-create brings back the wedge that A62 closed

**Where.**

- 03:728 (A62): "**A deleted `<agent>-serving` is re-created as a prepared route.** … **A missing-policy `Lock` is displaced.** When the recorded mode is `apikey`, the route re-create records `{kind: Create, …}` from `status.auth` in the slot. … either delete can be seen first, so the order of the two deletes does not matter."
- 03:461: "A `Create` re-enters at `ApplyingPolicies` when its prepared route exists, and at `PreparingRoute` when it does not." This rule assumes the `Create` has already been recorded.
- 03:651 and 03:693: the one ordering rule the design states for a write is the `written` flag, set "in the status update that ENTERS `ApplyingPolicies`, before the write". Nothing says the same for `PreparingRoute`'s route create, or for the status update that displaces a `Lock`.
- 03:695: a `Lock`'s `401` "is attributed when `beforeObserved` is set, or when `status.cards[]` records a digest for a revision the route's `backendRefs` currently serve". 03:692: `ProbingBefore` "is skipped for a missing policy". 03:459: a `Lock` never writes the route's `backendRefs`.
- The code the slice lands in writes the route and then the status, in the same reconcile: `reconcileServingRoute` at `internal/controller/agent_controller.go:733`, then `writeStatus` at `:773`. Its own comment says this order is deliberate ("The route is emitted AFTER the switch above", `:728`).

**What breaks.**

1. A served Agent is recorded as `apikey`. A label-selected prune deletes both objects, and the policy delete is seen first. A missing-policy `Lock` takes the slot and re-creates `<agent>-auth` (03:688).
2. The route delete is seen next. The implementer follows the shipped reconcile's order. The reconcile creates the prepared route, and the operator crashes, or loses its lease, before the status update that would replace the `Lock` with the `Create`.
3. The next reconcile finds `<agent>-serving` present, with `parentRefs` and no `backendRefs`, and finds the missing-policy `Lock` in the slot. The route is not deleted, so the displacement at 03:728 does not fire. The policy exists, so 03:603's re-entry changes nothing.
4. The `Lock` reaches `ProbingAfter`. The prepared route answers `401` once the policy attaches. That `401` is never attributed, because `beforeObserved` is unset and the route serves no revision (03:695). `Lock` has no `Publishing` stage, and nothing else attaches `backendRefs`. At the deadline the Agent reports `AuthPolicyMissing` and "stays in its stage and keeps working" (03:714). This is the eleventh critique's M1, reached through one crash instead of through an ordering.

The implementer might instead have the emitter re-assert the published shape on a route that "exists but differs" (§5's owned-field re-assert, 03:1244). Then the route is published before any probe, which 03:728 forbids for a recorded `apikey`. Either way the text leaves the outcome to a guess, and one of the two guesses is a permanent outage of a locked Agent.

**The same crash also hits the re-create with no transaction in flight.** A served `apikey` Agent loses only its route. The reconcile creates the prepared route, then crashes before recording the `Create`. The next reconcile finds a prepared route, a present policy, a recorded `mode`, and an empty slot. No trigger matches: `Lock` needs a missing policy (03:688), the re-create needs a deleted route (03:728), and `Adopt` needs no `mode` (03:603). That half predates A62. It is raised here because A62's "the order of the two deletes does not matter" depends on it, and because one fix closes both.

This is the same kind of defect the `written` flag exists for (the fifth critique's M1, recorded at 03:651): a crash between a status record and the write it authorizes.

**Fix.** Either of the following closes it. The first matches the rest of the design's level-triggered rules.

1. **Key the re-create on the route's state, not on its absence.** At 03:728: "The re-create fires when `status.auth` records a `mode`, no re-creation `Create` is in flight, and `<agent>-serving` is absent **or exists with no `backendRefs`**. A missing-policy `Lock` whose route has no `backendRefs` is displaced the same way." This is the eleventh critique's dropped sentence, turned from an entry rule into a displacement rule.
2. **Or order the writes.** "The status update that records the re-create's `Create`, displacing any missing-policy `Lock`, precedes the create of the prepared route, as `written` precedes the policy write." The fix must also tell the emitter never to create or strip a prepared route without that record.

Add a crash half to case 16. In the policy-first run, stop after the prepared route is created and before the status write. The Agent still reaches `Served` and its route is published. Mutation: key the displacement on a `NotFound` route alone, which the crash half must fail.

---

## MINOR

1. **§5 still says the opposite of 03:728.**
   - 03:1233, the deleted-route row, still says "An in-flight transaction is not displaced". The body now says "**A missing-policy `Lock` is displaced**" (03:728).
   - The missing-policy row (03:1232) says nothing about displacement either.
   - The eleventh critique's fix named "03:727 and §5 03:1232", and A62 changed only the body. The row cites §3.3.3, so the intent can be recovered, which is why this is a MINOR, the same way that critique's MINOR 1 was.
   - **Fix**: "A J2 `Lock` or a spec-driven `Create` in flight is not displaced. A missing-policy `Lock` is, by a `Create` taken from `status.auth`."
2. **Nothing says what happens to `AuthPolicyMissing` when its `Lock` is displaced, and 03:254 now contradicts 03:728.**
   - At entry, the missing-policy `Lock` raised `PolicyApplyIncomplete` and `GovernanceSkipped=True`, both with reason `AuthPolicyMissing`. The message "says the route is serving unauthenticated" (03:703, 03:712).
   - After the displacement, the route is unpublished, so that message is false. The implementer must pick one of two options. Keeping the condition is loud and wrong under rule 8. Clearing it restarts the page clock behind the `Create`'s 4-minute deadline, and sets `GovernanceSkipped` to "vacuously `False` for an Agent with no published route" (03:254).
   - 03:254 goes on to say "`Ready` carries why". A62's own sentence at 03:728 says that before the deadline "no other condition reports the unpublished window". Both cannot hold.
   - **Fix**: one sentence at 03:728. For example: "On displacement, `PolicyApplyIncomplete=AuthPolicyMissing` is kept until `Served`, and its message says the route is being re-created and is unpublished." Then scope 03:254's "`Ready` carries why" to exclude the re-create window, or say which condition carries it.
3. **Case 16 asserts that "the route comes back prepared", but neither holds the switch nor reads the first created version.**
   - Case 8 (03:1281) explains why that assertion needs both: "Without the switch, the kept policy makes the stub answer `401` the moment the route exists, and a polling test may never see the prepared version."
   - In case 16's policy-first run, the `Lock` has already re-created the policy before the route is deleted. So the case has the exact weakness that case 8 guards against.
   - The mutation still fails, because it is observed at the deadline. So M1 of the eleventh critique stays pinned. Only the "prepared" half is weak.
   - **Fix**: hold the switch through the re-create, and assert on the first created version from the watch event, as case 8 does.

---

## Verified sound

- **`beforeObserved` can be implemented with the CRD's fields.**
  - A card entry names its revision twice: `CardStatus.Revision` (`api/v1alpha1/agent_types.go:557`), and `RevisionDigest` (`:561`). The digest is recorded beside them (`:570`).
  - The route's one `backendRef` names the Service `WorkloadName(agent.Name, rev)` (`internal/controller/httproute.go:236`), which is `agent + "-" + rev` (`agent_controller.go:1614`). So the revision name comes back by stripping the Agent's prefix. The route also carries the revision digest as an annotation (`httproute.go:302`), so a lookup through `cardEntry` by `RevisionDigest` (`card.go:358–365`) works too.
  - Entries are upserted by `RevisionDigest` (`card.go:294–303`). Two retained revisions cannot share a name, because the operator refuses a 40-bit name collision (`agent_controller.go:861–866`). So "the digest recorded for the revision the route names" identifies exactly one entry.
  - A failed-attempt entry carries an empty `Digest` (`card.go:385–391`), so it never matches, and a revision with no digest leaves `beforeObserved` unset, as 03:692 intends.
  - The before-revision's entry survives the J2 edit, because the active revision is protected from pruning (`agent_controller.go:1395`, `:1421`, `:1450`).
- **The byte-identical-card residue is honest and bounded.** A J2 edit often leaves the card unchanged, so the new revision may share the old one's digest. Then a lagging gateway's old-revision card can set `beforeObserved` against the new revision. But the new revision's own digest was recorded from an anonymous `200` (`card.go`, `fetchAndValidateCard`), so its `401` would be accepted by the digest rule anyway (03:700). What differs is only whether the message says "observed" or "not observed". 03:692 states the residue, so it is not a false guarantee.
- **The re-creation test (03:452) misclassifies no slice path.**
  - A J2 `Lock` targets `apikey` over a recorded `none`, so it never matches.
  - A never-served `Create`, including one that crashed between `Publishing` and `Served`, has no `mode` (03:452).
  - An abandonment enters its successor in the same status update (03:721), so the slot never pairs a spec-driven transaction with a `mode` it did not come from.
  - A displaced `Lock`'s successor is a `Create` beside a recorded `mode`, which is a re-creation, as it should be.
  - The only spec-driven transaction that can reach a recorded `mode` does so at `Served`, where abandonment no longer applies (03:718).
- **A route delete during the `Lock`'s `ApplyingPolicies` is safe, provided the record lands first.** The displacing `Create` starts a fresh record with a fresh deadline and repeats the policy write at its own `ApplyingPolicies`. That write is idempotent (03:651). Re-creations are never abandoned (03:718), so the `Lock`'s `written` flag, which only gates abandonment's delete (03:719), is not needed after the hand-off. The only unsafe window is the one in M1.
- **The route-first order of case 16 is specified.** The re-create `Create` takes the slot. The later policy delete re-enters that `Create` at its write (03:603), and does not open a `Lock`.
- **The J2 `Lock` keeps its slot correctly across a route delete.** Its recorded `none` publishes the re-created route at once, and the route names the same active revision (`httproute.go:227–241`, `agent_controller.go:733`). So `beforeRevision` still matches.
- **Case 11's fifth half and case 6 can be expressed in envtest.** The stub's backend override supplies r1's card and then r2's `401` (03:1273). The test controls card digests through `r.CardClient = redirect{…}` (`test/envtest/card_test.go:154`), so it can record r1's digest and none for r2.

## A62's author's calls

| Call | My view |
|---|---|
| A route re-create over a recorded `apikey` displaces a missing-policy `Lock` | **Right.** Only `Create` publishes, and the `Create` does everything the `Lock` would have done. But the trigger must be the route's state, not a delete event (M1). The choice is technical, and no human is needed |
| `beforeObserved` matches only the digest of the revision the route names | **Right**, and it is the tenth critique's original wording. The identical-card residue is stated |
| Promise the unpublished-window message only where a condition carries it | **Acceptable as a residue.** But the displaced `AuthPolicyMissing` needs a stated fate, and 03:254 must stop saying `Ready` carries it (MINOR 2) |

## The smallest set of changes to approvable

1. **M1**: key the route re-create, and the displacement of a missing-policy `Lock`, on "`<agent>-serving` absent **or** present with no `backendRefs`, while `status.auth` records a `mode` and no re-creation `Create` is in flight", or order the `Create`'s status record before the route create. Give case 16 a crash half, with the mutation "displace only on a `NotFound` route".

MINOR 1 should go in with it, because it is one sentence in the §5 row that M1's fix touches anyway. MINORs 2 and 3 may stay open.

`03-policy-compiler.md (first slice, §1.1): REVISE`
