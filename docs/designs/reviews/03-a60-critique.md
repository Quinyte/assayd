# Design 03 (policy compiler): critique of A60, scoped to the first slice (§1.1)

- **Date**: 2026-09-12.
- **Reviewer**: an independent adversarial critique, following `.claude/skills/critique-design/SKILL.md` and AGENTS.md. It is a cold read with no author context. This is the tenth critique of the consolidated body, and the fifth to read §1.1.
- **Target**:
  - `docs/designs/03-policy-compiler.md` at `origin/design-03-a60` `82ff085`;
  - `docs/designs/reviews/03-a59-critique.md`, whose findings A60 answers.
- **Scope**: the human's decision G limits approval to §1.1's slice. `Narrow`, `Loosen`, E2, F2's group rules, D2, `Withdraw`, the other concerns and the candidate route are out of scope. A finding is raised against them only where the slice depends on them or they contradict it.
- **Method**:
  - Each finding of the ninth critique was checked in the body, at its source.
  - A60's diff was then read line by line for new defects. Each new sentence was read against the rules it has to live beside: the entry predicate, the abandonment rule, the stale-state rule, the deadline table, §5 and §8.1.
  - The slice's claims were checked against the code they land in: `internal/controller/{agent_controller,card,httproute,conditions}.go`, `internal/revision/{leaves,revision}.go` and `api/v1alpha1/agent_types.go`.
  - Nothing was run against a cluster. No file other than this one was written.

## Verdict for the first slice: **REVISE — 0 BLOCKER, 2 MAJOR, 5 MINOR.**

A60 closes the ninth critique's MAJOR 1 at the entry predicate and in the re-create paragraph. It also closes all three MINORs as asked. Two defects remain, and both sit next to what A60 changed:

- A60 makes the route re-create a second exception to the entry predicate. The abandonment rule still exempts only the first. Read as written, that rule abandons every re-create A60 added, starting on its first reconcile. So case 15's two scenarios come back as the failures the ninth critique described: the refused `apikey` → `none` gets carried out, and an I1 Agent's route gets deleted again (MAJOR 1).
- A60 says a J2 `Lock` "neither waits for that weight shift nor holds it … so either order is safe" (03:41). That covers whether the route is open. It does not cover whether the probe can attribute its `401`. `beforeObserved` records the old revision's answer. It then attributes a `401` from whatever revision the route serves by the time `ProbingAfter` runs. So a new revision whose backend refuses its card path can prove a lock that was never enforced (MAJOR 2). The ninth critique made the same argument in its MINOR 2, and I disagree with it (below).

Each fix is one or two sentences and one named mutation. With both fixes, I would judge the slice approvable.

---

## Closure against `reviews/03-a59-critique.md`

| Finding | This pass |
|---|---|
| **M1**: the entry predicate excluded `auth: none` and did not exempt the route re-create | **Closed at the predicate. Open at the abandonment rule.** "Compiles" is now defined by mode: `none` always compiles, `apikey` compiles when its canonical policy renders, and nothing else compiles (03:520). The transaction record gains `targetMode`, and `targetDigest` is present exactly when that mode is `apikey` (03:428–432). The re-create paragraph takes its target from `status.auth` (03:724), and case 15 pins that (03:1284). The fix's second item named two exceptions to *entry*. The abandonment rule, a separate gate on the same transactions, still names one (MAJOR 1) |
| MINOR 1: "at most one drift interval ago" | **Closed.** The sentence is now scoped to the desired revision, and the message names `fetchedAt` (03:696). This is true to the code. The operator fetches a card only for `desiredDigest` once that revision is ready (`agent_controller.go:551`, `:563`), and requeues on the drift interval only then (`:614–615`). The active revision's entry survives pruning, because the active revision is always in the retained set (`agent_controller.go:1394–1395`, `:1450`; `card.go:404–417`) |
| MINOR 2: `Lock` and the J2 edit's weight shift never ordered | **Closed as asked, and the reason given is incomplete.** 03:41, 03:62 and case 6 (03:1275) now say that `Lock` does not hold the shift, and "never without `backendRefs`". The argument covers whether the route is open, and misses attribution (MAJOR 2) |
| MINOR 3: probing at 5 s forever after the deadline | **Closed.** Past the deadline the probe backs off to `CardRetryInterval`, 15 s (03:661). The value matches `card.go:81` |

The ninth critique's open items stay as it recorded them: the eighth critique's MINOR 6, and the sixth critique's MINORs 3, 4 and 5's gate. None is re-raised. The human's open question about an `Adopt`ed owner's `none` → `apikey` (03:83) does not block, for the reason the ninth critique gave.

---

## MAJOR

### M1: the abandonment rule does not exempt the route re-create, so it abandons every re-create case 15 describes

**Where.**

- 03:520 names two exceptions to the entry predicate: "**both are re-creations that take their target from `status.auth`, never from the spec.** One is a `Lock` that re-creates a missing `<agent>-auth`. The other is a `Create` that re-creates a deleted `<agent>-serving`."
- 03:724: "**Its target is the recorded state in `status.auth`, not the spec** … That holds even if its spec now says `none`, which is refused, or does not compile (I1)."
- The abandonment rule's trigger, at 03:714, fires when "An `-auth` `Create`, or a J2 `Lock`, has not reached `Served`, and the Agent's desired `-auth` mode no longer equals the transaction's target." It exempts one case only: "**A `Lock` that re-creates a missing policy is never abandoned.** Its target is the state `status.auth` records as served, not the spec."
- The stale-state rule says the same without the exemption: "An unfinished `Create` or J2 `Lock` whose target mode no longer matches the desired one is not re-entered: it is abandoned" (03:457). §5's row agrees: "A `Lock` that re-creates a missing policy is not abandoned" (03:1226).

A route re-create is a `Create`. In every case where 03:724's clause "even if its spec now says" matters, its `targetMode` differs from the desired mode by construction. So every rule that governs abandonment abandons it.

**What breaks.** Each case below is reachable in the slice. Each is one of case 15's own setups.

1. **Case 15(a), the refused `apikey` → `none`, carried out after all.**
   - A locked Agent (`status.auth.mode: apikey`) is edited to `none`. The edit is refused (03:527). Its route is then deleted.
   - A re-create `Create` is entered with `targetMode: apikey`. The desired mode is `none`, so 03:714 fires on the first reconcile.
   - "What runs next" then enters "whatever the desired spec now calls for", and "for `none`, a never-served Agent enters `Create` and is published at once with its marker label" (03:717).
   - This Agent is served, so it matches none of 03:716–717's branches ("for a `Lock`" and "for a `Create` the recorded state is an unpublished route"). So the implementer has to guess.
   - The most direct reading publishes the route with `assayd.dev/auth: "none"` and records `mode: none`. Meanwhile 03:715 keeps the recorded policy ("a policy `status.auth` records as served is never touched"), so the policy still attaches. Status and the marker then say "unauthenticated" on a route that may be enforcing. That is rule 8's loud-and-wrong, and it is the lock the slice calls one-way (03:39), undone by a route delete.
2. **Case 15(b), an I1 Agent's route deleted again, repeatedly.**
   - A served `apikey` Agent is edited to `oauth` (I1), and its route is deleted.
   - The re-create `Create` has `targetMode: apikey`. The desired mode is `oauth`, so 03:714 fires.
   - "For an abandonment to a mode that does not compile, the route is deleted rather than detached" (03:716). "What runs next" for `oauth` is `PolicyCompileFailed` "and enters no transaction" (03:717).
   - On the next reconcile the route is missing and `status.auth` records a mode, so 03:724 re-creates it, and 03:714 abandons it again. The Agent flaps between a prepared route and no route, and is never published. That is the outage A60 says it removed, "an I1 Agent's route comes back under its last good policy" (03:520, §11 03:1316).
3. **A served `none` Agent edited to `oauth`, whose route is then deleted.** The re-create targets `none`, and the desired mode is `oauth`. So the rule abandons it and deletes the route, where 03:724 says the route "is published at once with its marker".

The same collision reaches the stale-state key. 03:457 keys an `-auth` record "on the desired policy's digest". A re-create's target is `status.auth.appliedDigest`, not the desired policy. So for a refused or I1 spec, the key never matches (MINOR 1).

**Why case 15 does not settle it.** Case 15's end states would fail under the abandoning reading. (a) asserts "never published with the marker". (b) asserts that the route is published. So the tests contradict the rule text, and the implementer has to pick which of the two is wrong. Case 15 names only "taking the re-create's target from the spec" as its mutation. The abandoning implementation keeps the target from `status.auth`, so that mutation does not describe it.

**Fix.**

1. In 03:714, 03:457 and §5 03:1226, exempt both re-creations. For example: "Abandonment applies only to a transaction whose target came from the spec: a `Create` for an Agent whose `status.auth` records no `mode`, and a J2 `Lock`. The two re-creations of §3.3.1, of a missing `<agent>-auth` and of a deleted `<agent>-serving`, take their target from `status.auth` and are never abandoned." The distinction can be computed from what is stored. A re-create is a `Create` beside a recorded `mode`, or a `Lock` whose `targetMode` equals the recorded `mode`.
2. Add a mutation to case 15: "abandon a re-create when the desired mode differs from its target", which (a) and (b) must fail.

---

### M2: `beforeObserved` attributes a `401` from a revision the before-probe never reached, and A60 now makes that reachable by design

**Where.**

- 03:41 (A60): "`Lock` neither waits for that weight shift nor holds it, because the per-Agent `-auth` targets the route … The route is open before the `Lock` and closed after it, whichever revisions it serves, so either order is safe." 03:62 repeats the reason.
- 03:688: `ProbingBefore` sets `beforeObserved` when its one answer is "`200` with a body whose digest equals a card digest in `status.cards[]`".
- 03:691: in `ProbingAfter`, "A `401` is attributed when `beforeObserved` is set, **or** when `status.cards[]` records a digest for a revision the route's `backendRefs` currently serve."
- 03:696 justifies the first branch: "When `beforeObserved` is set, the probe is a transition: the same anonymous request was served the Agent's own card, and is now refused." That holds only while the same backend answers both requests.
- 03:699: "**Status never claims the lock early.**"

**What breaks.** The failure is a J2 edit that also changes the image, applied in one `kubectl apply`. That is an ordinary edit.

1. A served `auth: none` Agent serves revision R1, whose card digest is recorded.
2. The owner changes `expose.a2a.auth` to `apikey` and bumps the image in one edit. That mints R2 (`internal/revision/leaves.go:261`). R2's image answers its card path `401` to anonymous callers. Case 11's third half already treats "a backend's own `401`" as in scope (03:1280).
3. `Lock` enters. `ProbingBefore` reaches R1, because R2 is not ready and the route follows `status.activeRevision` (`agent_controller.go:619–620`, `:728–734`). It gets `200` with R1's card, so `beforeObserved` is set.
4. The policy is written and `Converging` passes on conditions. §3.3.2 measured that conditions can report converged while the old configuration serves (03:587). "A replica that has not taken the policy and raises no Event is not seen" (03:670).
5. R2 becomes ready. Its card fetch gets `401`, so R2 has no digest (`card.go:385–391`). A failed fetch withholds no traffic (`agent_controller.go:544–549`), so R2 is promoted in the same reconcile (`:706–716`). The route's single backendRef moves to R2 (`httproute.go:227`).
6. `ProbingAfter` gets R2's own `401`. `beforeObserved` is set, so the `401` is attributed. `Served` records `mode: apikey` with "transition observed", the marker is removed, and `GovernanceSkipped` goes `False`. The gateway enforces nothing.

That is the eighth critique's M1, a backend's `401` accepted as proof of a lock (`reviews/03-a58-critique.md`). This time it gets in through the first branch of the rule instead of the second. Under A59 an implementer could have held the shift and been safe. A60 removes that option.

**I disagree with the ninth critique's MINOR 2**, which offered this same reason: "Either choice is safe, because the route is open before and after." That holds for the traffic, and fails for the probe. Here "closed after" is what `ProbingAfter` exists to establish. So it cannot also be the premise that makes the ordering irrelevant.

**Fix.** The slice's emitter serves exactly one revision ("there is never" a second backendRef, `httproute.go:238–240`). So the narrow rule can be implemented:

1. Record, with `beforeObserved`, the revision digest whose card the before-answer matched. `beforeObserved` attributes a `401` only while the route's backendRef is still that revision. After a promotion, only the second branch applies: a digest recorded for the revision now served. Alternatively, hold the J2 edit's promotion until `Served`, which is what `Narrow` does (03:602). Either way, correct the reason at 03:41 and 03:62.
2. Add a fourth half to case 11. With the switch held, the before-answer is R1's card. R2 is then promoted with no digest, and the backend override answers `401`. The Agent never reaches `Served`. Mutation: attribute on `beforeObserved` whatever revision the route now serves.

---

## MINOR

1. **The `-auth` record's key still names a digest that A60 made optional** (03:448: "Its transaction is keyed on `targetDigest`"; 03:457: "keyed on the desired policy's digest").
   - A `none` target has no `targetDigest` (03:431).
   - A re-create's target is `status.auth`, not the desired policy. So its key is not the desired digest either.
   - **Fix**: keyed on `targetMode`, plus `targetDigest` when the mode is `apikey`, and for a re-create compared with `status.auth` rather than the spec. This rides with M1.
2. **"An Agent whose serving route does not compile has no deadline and never reaches this table" (03:702) is false for an I1 Agent.**
   - Under either exception an I1 Agent enters a transaction with a deadline. The missing-policy `Lock` reaches the table's third row, and the route re-create its `Create` row.
   - §5's deleted-route row (03:1229) still says "published only through `Create`'s probe", while 03:724 publishes a recorded `none` at once and takes the target from `status.auth`.
   - **Fix**: "a *never-served* Agent whose …" at 03:702, and make 03:1229 match 03:724.
3. **During an I1 Agent's route re-create, `Ready` names the wrong cause.**
   - `PolicyCompileFailed` outranks `PolicyApplyIncomplete` "because nothing was applied to fail" (03:511). So a stalled re-create of an I1 Agent reports `Ready` under `AuthInputAbsent`.
   - The actual outage is the unpublished route, and "The route keeps serving under it" (03:520) is false for that window.
   - The page still fires, because design 10 keys on the condition type (03:710). So this is wording, not a lost signal.
   - **Fix**: the `PolicyCompileFailed` message, or the `Create` row's message, says that the route is being re-created and is unpublished.
4. **`status.auth.transaction` has one slot, and a route delete can land during an in-flight J2 `Lock`.**
   - The Agent records `mode: none` and a `Lock` transaction. 03:724 then re-creates the route "through `Create`" with target `none`.
   - Two readings converge safely. In one, the re-create publishes at once with no record, and the `Lock` continues on the new route, because its policy targets the route by name. In the other, the `Create` displaces the `Lock`, and the J2 difference re-enters `Lock` after `Served`.
   - The text does not say which. This predates A60, but A60's "second exception" makes the re-create a transaction with a target, which sharpens the question.
   - **Fix**: one sentence. A route re-create does not displace an in-flight transaction. Where there is none, it records `{kind: Create, targetMode, targetDigest}` from `status.auth`.
5. **§1.1's critique count is stale** (03:24: "Three critiques have read this section … A59 answers the third, and no critique has read A59"). Four had read it by A60, and A60 answers the fourth. The Status line (03:3) is current. **Fix**: update the sentence.

---

## Verified sound

- **"Compiles" by mode is implementable without guessing at entry** (03:520, 03:428–432). Case 14 (03:1283) and case 15(c) (03:1284) pin both sides, and 15(c)'s mutation, "requiring a `targetDigest` for every target", describes the defect it replaces.
- **Case 15's end states are right.** Under the mutation it names, (a) ends published with the marker and (b) never publishes, so both fail. Whether the re-created route's *first* version is prepared stays with case 8, which reads it from the watch event under the held switch (03:1277). Case 15 does not need to repeat that.
- **The J2 edit does mint a revision**, as 03:41 and 03:696 say. `spec.Expose.A2A.Auth` is marked `mints` (`internal/revision/leaves.go:261`) and enters the behaviour projection (`internal/revision/revision.go:372–377`).
- **The serving route carries exactly one backendRef** (`httproute.go:227`, `:238–240`), and it follows `status.activeRevision` (`agent_controller.go:728–734`). So "a revision the route's `backendRefs` currently serve" (03:691) means one revision in the slice, and M2's narrow fix can be implemented. The ninth critique's call that a narrower rule was unimplementable holds only for weighted routes, and the slice emits none.
- **The digest-age sentence** (03:696) matches the code: fetch and requeue happen only for the desired, ready revision (`agent_controller.go:551`, `:614–615`), and `FetchedAt` is on the CRD (`api/v1alpha1/agent_types.go:567`).
- **The backoff** (03:661) uses a constant that exists at the stated value (`card.go:81`) and does not change the pre-deadline 5 s interval in the stage table (03:405).
- **Ownership is still honestly owed.** `PolicyCompileFailed` is absent from `ownedTypes` (`conditions.go:47–81`), and 03:55 and case 9 say so.

## A60's author's calls

| Call | My view |
|---|---|
| `none` "always compiles", with target `{targetMode: none}` | **Right.** It is the only definition under which a never-served `none` Agent is published. The refusal of a served `apikey` → `none` stays a separate rule (03:527), so "compiles" does not imply "is entered". No human needed |
| Scope the digest age instead of bounding it, and report `fetchedAt` | **Defensible.** A freshness bound would stall a `Lock` whenever the new revision crashloops. Reporting the age is rule 8's minimum. No human needed |
| `Lock` does not hold its edit's weight shift | **Wrong as reasoned** (M2). It can stand if `beforeObserved` is tied to the revision it observed. Holding the shift is the alternative. The choice is technical, and no human is needed |
| Back off to `CardRetryInterval` after the deadline | **Defensible.** It reuses an existing constant, and a late enforcement can still pass. No human needed |

## The smallest set of changes to approvable

1. **M1**: exempt both re-creations from abandonment at 03:714, 03:457 and 03:1226. Add the mutation "abandon a re-create on a desired-mode mismatch" to case 15.
2. **M2**: tie `beforeObserved` to the revision whose card it saw, or hold the J2 promotion until `Served`. Correct 03:41 and 03:62, and give case 11 its fourth half with the mutation.

MINORs 1 and 2 should ride along, because each is one sentence and MINOR 1 touches the same text as M1. MINORs 3–5 may stay open.

`03-policy-compiler.md (first slice, §1.1): REVISE`
