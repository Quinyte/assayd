# Design 03 (policy compiler): critique of A63, scoped to the first slice (§1.1)

- **Date**: 2026-09-12.
- **Reviewer**: an independent adversarial critique, following `.claude/skills/critique-design/SKILL.md` and AGENTS.md. It is a cold read with no author context. This is the thirteenth critique of the consolidated body, and the eighth to read §1.1.
- **Target**:
  - `docs/designs/03-policy-compiler.md` at `origin/design-03-a60` `1a9c3fc`;
  - `docs/designs/reviews/03-a62-critique.md`, whose findings A63 answers.
- **Scope**: the human's decision G limits approval to §1.1's slice. `Narrow`, `Loosen`, E2, F2's group rules, D2, `Withdraw`, the other concerns and the candidate route are out of scope. A finding is raised against them only where the slice depends on them or they contradict them.
- **Method**:
  - Each finding of the twelfth critique was checked in the body, at its source.
  - A63's diff was then read line by line for new defects. Two things were traced through the rules they must live beside:
    - the new state-based trigger at 03:728 ("`status.auth` records a `mode` and `<agent>-serving` is absent, or is present with no `backendRefs` while no `Create` holds the slot"). I ran it against every slice path that can leave a route empty or absent: a J2 `Lock` in flight, an abandonment that strips `backendRefs` before it deletes a policy, a never-served `Create`, a refused `Adopt`, a `Create` in `Publishing`, and the operator's own route sweeps;
    - the status-first ordering, against the `Create` re-entry rule (03:461), the `written` flag (03:651, 03:693), the re-creation test (03:452) and the shipped emitter.
  - The slice's claims were checked against the code they land in: `internal/controller/{agent_controller,httproute}.go`, `api/v1alpha1/agent_types.go`, `charts/assayd/templates/admission.yaml` and `test/e2e/gateway_test.go`.
  - Nothing was run against a cluster. No file other than this one was written.

## Verdict for the first slice: **PASS — 0 BLOCKER, 0 MAJOR, 3 MINOR.**

A63 closes the twelfth critique's MAJOR 1. It takes both of that critique's fixes. The re-create now fires on the route's state, so the crash window the critique described is picked up on the next reconcile: an empty route beside a missing-policy `Lock`, or beside an empty slot. The `Create` is also recorded before the route is created. Case 16's crash half pins the state trigger with a mutation that fails it. MINORs 1–3 are closed too.

I could not construct a slice path on which the new trigger fires on a route that is legitimately empty, or legitimately absent. Two of the three defects below are overlaps between the new trigger and rules that existed before it:

- the trigger's guard names only `Create`, while the slot rule protects a J2 `Lock` too, and the absent branch has no guard at all (MINOR 1);
- §5's out-of-band-mutation row now contradicts the trigger for a route stripped of its `backendRefs` (MINOR 3).

The third defect: the new ordering rule is not load-bearing once the state trigger exists, and no test can fail on it (MINOR 2). Each of the three needs an unlikely fault to reach it, and each fails toward an unpublished route or a loud condition. None leaves an implementer guessing on a path the slice reaches in normal operation.

---

## Closure against `reviews/03-a62-critique.md`

| Finding | This pass |
|---|---|
| **M1**: the displacement fired on a deleted route, so a crash inside the re-create brought the wedge back | **Closed.** 03:728: "**The trigger is the route's state, not the delete event.** A re-create is due when `status.auth` records a `mode` and `<agent>-serving` is absent, or is present with no `backendRefs` while no `Create` holds the slot." Both halves of that critique's M1 now match. (1) An empty route beside a missing-policy `Lock`: a `Lock` is not a `Create`, so the re-create is due, and the `Lock` is displaced (03:728, "**A missing-policy `Lock` is displaced.**"). (2) An empty route beside an empty slot: due, and "With no transaction in flight, the re-create records the same `Create`". The ordering was taken as well: "The re-create records its `Create` in `status.auth.transaction` in one status update, and creates the prepared route only after that update succeeds." Case 16's crash half (03:1289) has the mutation "fire the re-create only on a route found absent". Under that mutation the `Lock` keeps the slot. Its `ProbingBefore` is skipped for a missing policy (03:692), and the empty route serves no revision (03:695), so its `401` is never attributed, and the case fails at the deadline as it must |
| MINOR 1: §5's deleted-route row said the opposite of the body | **Closed.** 03:1233: "A J2 `Lock` or a spec-driven `Create` in flight is not displaced. A missing-policy `Lock` is, and its `AuthPolicyMissing` clears." |
| MINOR 2: the fate of a displaced `AuthPolicyMissing`, and 03:254 against 03:728 | **Closed.** 03:728 clears it "in the status update that records the `Create`". 03:254 now names the exception: "a route re-create before its deadline. For an I1 Agent, `PolicyCompileFailed` carries it. For any other Agent, nothing carries it until the `Create`'s deadline". That agrees with 03:728's "Before the deadline, no other condition reports the unpublished window". The page-clock consequence is discussed under "A63's author's calls" |
| MINOR 3: case 16 neither held the switch nor read the first created version | **Closed.** 03:1289: "Both runs hold the stub's switch, and assert on the first created version of the re-created route, read from its watch event as in case 8." Unlike case 8 (03:1281: "Then the switch is released"), the case does not say when the switch is released. But "published only after the stub's anonymous `401`" requires the release, so the case is still expressible |

The earlier open items stay as the twelfth critique recorded them, and I do not raise any of them again. That includes the sentence at 03:728 saying a `none` Agent's "`Publishing` waits for the anonymous `401`": it predates A62, and A63 did not make it worse. The human's open question on an `Adopt`ed owner's `none` → `apikey` (03:83) does not block.

---

## MINOR

### 1. The trigger's guard names the wrong set: it lets a J2 `Lock` be "due", and it does not keep a recorded `Create` from being recorded again

**Where.**

- 03:728 (A63): "A re-create is due when `status.auth` records a `mode` and `<agent>-serving` is absent, or is present with no `backendRefs` while no `Create` holds the slot."
- 03:728, the slot rule that predates A63: "A J2 `Lock` or a spec-driven `Create` already in flight is not displaced. … for a J2 `Lock` the recorded mode is `none`, so the re-created route is published at once."
- 03:728 (A63): "The re-create records its `Create` in `status.auth.transaction` … A crash between the two leaves a `Create` with no route, which re-enters at `PreparingRoute` (§3.3)."
- 03:461: "A `Create` re-enters at `ApplyingPolicies` when its prepared route exists, and at `PreparingRoute` when it does not."

**What is left to a guess.**

- **The absent branch has no guard.** A63's ordering creates a new state: a re-creation `Create` recorded, and no route yet. That state matches two rules. By 03:461 the `Create` re-enters at `PreparingRoute`. By the trigger, "a re-create is due", and "the re-create records its `Create`". The slot rule does not settle which applies. It lists a J2 `Lock` and a *spec-driven* `Create` as not displaced, and a missing-policy `Lock` as displaced, and it says nothing of a re-creation `Create` already in the slot.
  - An implementer who records a fresh `Create` whenever the trigger holds resets `deadline` on every reconcile while the route create keeps failing.
  - The `Create`'s deadline outcome (03:710) then never arrives.
  - This fails loud, not silent, because the shipped reconcile raises `PolicyApplyIncomplete=RouteApplyFailed` on any non-transient route write failure (`internal/controller/agent_controller.go:749–751`). So the finding is a MINOR.
- **A J2 `Lock` is not a `Create`.** A route present with no `backendRefs` while a J2 `Lock` holds the slot therefore matches the trigger. The slot rule says the `Lock` is not displaced, so the `Create` the trigger records has nowhere to go.
  - Suppose an implementer leaves the `Lock` alone and does nothing else. The `Lock`'s `401` cannot be attributed on a route with no `backendRef`: `beforeObserved` counts only while the one `backendRef` names `beforeRevision`, and the digest rule needs a revision the route serves (03:695). So the Agent is off the air until its deadline, and after it too, because the machine "stays in its stage and keeps working" (03:714).
  - Reaching this state needs an update that strips `backendRefs` from a route whose `parentRefs` still name the assayd Gateway. `assayd-gateway-routes` refuses that update from anyone but the operator (`charts/assayd/templates/admission.yaml:86–106`). It is also an outage the strip itself caused, and it pages. So this is a MINOR.
- **"Present" is not read by §3.2's ownership rule.** "`<agent>-serving` is present" can be read as "an object of that name exists". The shipped emitter's ownership test is name **and** `assayd.dev/agent-uid` (`internal/controller/httproute.go:574–612`), and a foreign object of that name is refused as a collision (`httproute.go:322`, `routeCollision`). The trigger should say the same. Before A63 only an absent route triggered, so a foreign route never could.

**Fix.** One sentence at 03:728, and the same words in the 03:1233 row. For example: "A re-create is due when `status.auth` records a `mode`, `<agent>-serving` (by §3.2's name-and-UID rule) is absent or has no `backendRefs`, and the slot is empty or holds a missing-policy `Lock`. A `Create` already in the slot is not recorded again: it re-enters by §3.3's rule, and its deadline stands. A J2 `Lock` keeps the slot, and its route is re-created published, because its recorded mode is `none`."

### 2. "The status write comes first" is a rule that no test can fail

**Where.** 03:728 (A63): "**The status write comes first.** The re-create records its `Create` … and creates the prepared route only after that update succeeds." Case 16's crash half (03:1289) fabricates the other order: "simulated by writing the route with the old order".

**Why it is unpinned.**

- With the state trigger in place, the wedge closes under either order.
  - If the route is written first and the operator crashes, the next reconcile finds an empty route and a slot with no `Create` in it, so the re-create is due.
  - If status is written first and the operator crashes, the `Create` re-enters at `PreparingRoute`.
- The crash half's mutation targets the trigger, not the order. An implementation that keeps the shipped order passes every case in §8.1: route first, then status (`internal/controller/agent_controller.go:733`, then `:773`, which 03:728 itself cites).
- So the design states an ordering that nothing enforces. AGENTS.md rule 5 ("Either pin it or delete it") and rule 7 both apply. The ordering also creates the state MINOR 1 has to disambiguate: a `Create` recorded, with no route.

**Fix.** Choose one of the following.

1. **Pin it.** envtest runs one etcd, so resource versions are ordered across kinds. Assert that the re-created route's creation `resourceVersion` is greater than the Agent status update that recorded the `Create`. Mutation: create the route first.
2. **Demote it.** Say that the state trigger makes the order immaterial, and state the order as a preference. That also removes the reason for MINOR 1's first bullet.

### 3. §5's out-of-band-mutation row now contradicts the trigger for a stripped route

**Where.**

- 03:1244: "Emitted resource mutated out-of-band | the owned-field comparison detects it → re-assert by update + event. … A deletion of `<agent>-auth` or `<agent>-serving` is covered by their own rows above".
- 03:728 (A63): the re-create is due when the route "is present with no `backendRefs`". For an Agent recorded as `apikey`, the route is "published only after the anonymous `401`".

**The conflict.**

- Before A63, a route whose `backendRefs` had been stripped was an out-of-band mutation, and 03:1244 governed it alone. The shipped comparison re-asserts `backendRefs` at once: `equalRoute` compares their length and every owned field (`internal/controller/httproute.go:403–420`).
- After A63, the same route also matches the trigger, which runs a `Create` and publishes only after the probe.
- A strip is not a deletion, so 03:1244's pointer to "their own rows above" does not route it to 03:1233.
- The two rules differ only in whether the route is republished before a probe. The policy survived the strip and still targets the route by name. Reaching the state needs an operator-identity writer, because of the admission policy cited in MINOR 1. So this is a MINOR.

**Fix.** Add to 03:1244: "A route left with no `backendRefs`, for an Agent whose `status.auth` records a `mode`, is the re-create of §3.3.3, not a re-assert."

---

## Verified sound

- **The state trigger fires on no legitimately empty or absent route that the slice produces.**
  - **A J2 `Lock` in flight.** The route is never without `backendRefs`. A `Lock` writes the policy and "never the route's `backendRefs`" (03:459). Its abandonment leaves "the route … never without `backendRefs`" (03:720). Case 6's mutation "strip `backendRefs` during the transaction" pins both. The only way in is an out-of-band strip (MINOR 1).
  - **Abandonment that strips `backendRefs` before deleting a policy.** Only a `Create` short of `Served` does it (03:720). Such a `Create` has no `mode`, because "A `Create` records `mode` only at `Served`" (03:452). Neither re-creation is ever abandoned (03:718). The successor is entered in the same status update (03:721). For `none` the successor is a `Create`, which holds the slot. For `oauth` the route is deleted, and the Agent has no `mode`. In no ordering does a recorded `mode` meet an empty route and an empty slot.
  - **A never-served `Create`**, including one that crashed between `Publishing` and `Served`, has no `mode` (03:452). The trigger does not apply.
  - **A refused `Adopt`** records `{kind: Adopt, stage: Refused}` and no `mode` (03:739). The trigger does not apply, and 03:730's rule for a deleted `Adopt`ed route is untouched.
  - **A route mid-`Publishing`.** The `Create` holds the slot, so the guard excludes it. An implementer might instead write `Served` before attaching `backendRefs`. After a crash the next reconcile would find a recorded `mode`, an empty route and no `Create`, and would re-probe and publish. That costs one more probe round and fails safe.
  - **The operator's own route removals do not meet a recorded `mode`.**
    - `status.activeRevision` is assigned only on promotion and never cleared (`internal/controller/agent_controller.go:716` is the only assignment). So `reconcileServingRoute`'s delete-all sweep for an empty revision (`httproute.go:276–283`) never runs for a served runtime Agent.
    - A switch to `spec.external` goes to `reconcileExternal`, which touches no route (`agent_controller.go:278–279`, `:808–822`).
    - Deletion is dispatched to `finalize` before any other step (`agent_controller.go:260–262`), and `finalize` collects the route itself (`:1561`).
    - A disabled gateway returns before emitting anything (`httproute.go:262–271`).
- **Status-first is consistent with the rules around it.**
  - The recorded `Create` is a re-creation by 03:452's stored-field test ("a `Create` while `status.auth` records a `mode`"), so it is never abandoned (03:461, 03:718).
  - Its deadline is set on entering `PreparingRoute`, so a `Create` left with no route has a deadline outcome (03:706, 03:710).
  - Its later write of the policy is gated by `written` like any other (03:693), and is idempotent (03:651).
  - A displaced `Lock`'s `written` flag gates only abandonment's delete (03:719), which a re-creation never reaches.
  - A policy delete seen after the displacement re-enters the `Create` (03:603).
  - The ordering is the reverse of the shipped reconcile (`agent_controller.go:733`, then `:773`). 03:728 says so, and §1.1 owes the prepared re-create to design 07's emitter (03:77).
- **The crash half can be expressed in envtest and kills its mutation.** The test stops the manager, writes the missing-policy `Lock` and an empty `<agent>-serving`, and restarts. The stub answers `500` on a route with no `backendRefs` (03:1273). Under the mutation, the `Lock` can never attribute its `401` (03:692, 03:695), so the route stays unpublished at the deadline.
- **The bookkeeping is right.**
  - §1.1 says seven critiques have read it (03:24). They are `03-a56` through `03-a62`, which is seven.
  - The Status line records the twelfth critique and A63.
  - AGENTS.md, CLAUDE.md and `docs/designs/README.md` say "twelve critiques".
- **The shipped behaviour 03:728 describes is real.** `TestTheOperatorRecreatesARouteThatWasDeleted` (`test/e2e/gateway_test.go:335`) deletes the emitted route and requires one with a new UID. `ensureServingRoute` creates it with `backendRefs` on `NotFound` (`httproute.go:305–316`, rendered at `:227–249`).

## A63's author's calls

| Call | My view |
|---|---|
| Take both fixes, the state trigger and the status-first order | **The state trigger is right**, and it alone closes M1. The order is harmless but unpinned, and it creates the state MINOR 1 must disambiguate (MINOR 2). The choice is technical, and no human is needed |
| Clear a displaced `Lock`'s `AuthPolicyMissing` rather than keep it | **Acceptable, and the residue is stated honestly** (03:254, 03:728). The route is unpublished, so the "serving unauthenticated" message would be loud and wrong. One consequence is not written down. Clearing `PolicyApplyIncomplete` resets design 10's 5-minute clock (`10:50`). A prune seen policy-first therefore pages about 9 minutes after the displacement, not 5 minutes after the policy delete. 03:714 states the same arithmetic for a J2 `Lock`, and a sentence would do the same here. This is not a finding: 03:254 already says nothing reports the window before the deadline |
| Case 16's crash half is written by the test, in the old order | **Right for what it pins**, which is the trigger. It cannot pin the order (MINOR 2) |

## Changes that should land with approval, none of them blocking

1. **MINOR 1**: one sentence at 03:728 and 03:1233. The re-create is due only while the slot is empty or holds a missing-policy `Lock`. A `Create` already in the slot re-enters without being recorded again. A J2 `Lock` keeps the slot. "Present" is judged by §3.2's name-and-UID rule.
2. **MINOR 3**: one clause in 03:1244.
3. **MINOR 2**: pin the order with a resource-version assertion, or demote it to a preference.

None blocks. The slice's rules are specified well enough to implement without guessing on every path the slice reaches in normal operation, and I found no false guarantee.

`03-policy-compiler.md (first slice, §1.1): PASS`
