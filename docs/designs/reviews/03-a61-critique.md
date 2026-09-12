# Design 03 (policy compiler): critique of A61, scoped to the first slice (§1.1)

- **Date**: 2026-09-12.
- **Reviewer**: an independent adversarial critique, following `.claude/skills/critique-design/SKILL.md` and AGENTS.md. It is a cold read with no author context. This is the eleventh critique of the consolidated body, and the sixth to read §1.1.
- **Target**:
  - `docs/designs/03-policy-compiler.md` at `origin/design-03-a60` `92255af`;
  - `docs/designs/reviews/03-a60-critique.md`, whose findings A61 answers.
- **Scope**: the human's decision G limits approval to §1.1's slice. `Narrow`, `Loosen`, E2, F2's group rules, D2, `Withdraw`, the other concerns and the candidate route are out of scope. A finding is raised against them only where the slice depends on them or they contradict it.
- **Method**:
  - Each finding of the tenth critique was checked in the body, at its source.
  - A61's diff was then read line by line for new defects. Four interactions were traced through the rules they must live beside: `beforeRevision` against re-entry after a crash, against the missing-policy `Lock` (which has no before-probe), against a route re-create during a `Lock`, and against a rollback that returns to the before-revision. The one-slot rule, "a re-create does not displace an in-flight transaction", was checked for any path where a target from the spec overrides `status.auth`.
  - The slice's claims were checked against the code they land in: `internal/controller/{agent_controller,card,httproute}.go`, `internal/revision/leaves.go` and `api/v1alpha1/agent_types.go`.
  - Nothing was run against a cluster. No file other than this one was written.

## Verdict for the first slice: **REVISE — 0 BLOCKER, 2 MAJOR, 4 MINOR.**

A61 closes the tenth critique's MAJOR 1 in substance, and closes its MAJOR 2 for the scenario that critique gave. It also closes MINORs 1, 2 and 5, and MINOR 3 in part. Two new defects sit in the sentences A61 added:

- **The one-slot rule wedges a served `apikey` Agent whose route and policy are both deleted.** This is the ordinary result of the label prune that 03:687 names as a cause. "A re-create does not displace an in-flight transaction … it continues on the new route" (03:727) leaves a missing-policy `Lock` holding the slot while the route is re-created *prepared*. `Lock` has no `Publishing` stage (03:406). Its `401` can never be attributed on a route with no `backendRefs` (03:694). So the route stays unpublished for good, and the only exit is to recreate the Agent (MAJOR 1).
- **`beforeRevision` records the revision the route names, not the revision whose card was served.** `beforeObserved` matches a digest from *any* retained revision (03:691). `beforeRevision` is read from the route object (03:444–446). The design then calls it "the revision that served that card" (03:699). While the gateway lags a promotion, those are different revisions. The newly promoted revision's own `401` then passes as the lock, which is the same false lock the tenth critique's M2 described, one step earlier (MAJOR 2). The tenth critique's fix asked for "the revision digest whose card the before-answer matched". A61 recorded something else.

Both fixes are one or two sentences, plus one named case each. With both fixes in, I would judge the slice approvable.

---

## Closure against `reviews/03-a60-critique.md`

| Finding | This pass |
|---|---|
| **M1**: the abandonment rule did not exempt the route re-create | **Closed in substance.** 03:717 exempts both re-creations and says why: "Each takes its target from the state `status.auth` records as served … so a mismatch with the desired mode is expected". §5's row matches (03:1229). Case 15 gains the mutation "abandoning a re-create on a desired-mode mismatch fails (a) and (b)" (03:1287), and that mutation describes the defect. **One sentence was missed.** The stale-state rule still says "An unfinished `Create` or J2 `Lock` whose target mode no longer matches the desired one is not re-entered: it is abandoned" (03:460). A route re-create is an unfinished `Create` whose target mode differs by construction (MINOR 1) |
| **M2**: `beforeObserved` attributed a `401` from a revision the before-probe never reached | **Closed for the scenario given, and a narrower one is opened.** A promotion now clears `beforeObserved`, and a `401` counts only "while the route's one `backendRef` still names `beforeRevision`" (03:699, 03:444–446). Case 11 gains the fourth half and its mutation (03:1283). The argument at 03:41 and 03:61 is corrected. But `beforeRevision` is taken from the route, not from the answer, and the two can differ (MAJOR 2) |
| MINOR 1: the `-auth` record's key named a digest A60 made optional | **Closed.** The record is keyed on its target, "`targetMode`, plus `targetDigest` when the mode is `apikey`", compared "with the desired `-auth`" for a spec-driven transaction and "with `status.auth`" for a re-creation (03:451). 03:460 now points there. What the text does not say is how the operator tells the two kinds apart from stored fields (MINOR 2) |
| MINOR 2: "never reaches this table" was false for an I1 Agent | **Closed.** 03:705 scopes the sentence to a never-served Agent and names both routes an I1 Agent takes into the table. §5's deleted-route row now matches 03:727 (03:1232) |
| MINOR 3: `Ready` named the wrong cause during an I1 Agent's route re-create | **Closed for `PolicyCompileFailed`, and ambiguous for `PolicyApplyIncomplete`.** 03:727 has `PolicyCompileFailed`'s message say that the route is being re-created and is unpublished. It also promises that `PolicyApplyIncomplete`'s message says so, but no `PolicyApplyIncomplete` exists on a `Create` before its deadline (03:709, 03:1227) (MINOR 3) |
| MINOR 4: `status.auth.transaction` has one slot, and a route delete can land during a `Lock` | **Closed as asked, and the sentence opens MAJOR 1.** 03:727: "If a `Lock` or `Create` is already in flight, it continues on the new route". That is safe for the J2 `Lock`, whose recorded mode is `none`, so its route is published at once. It is not safe for the missing-policy `Lock`, whose recorded mode is `apikey`, so its route is re-created prepared |
| MINOR 5: §1.1's critique count was stale | **Closed** (03:24) |

The earlier open items stay as the tenth critique recorded them, and none is re-raised. The human's open question on an `Adopt`ed owner's `none` → `apikey` (03:83) does not block.

---

## MAJOR

### M1: a missing-policy `Lock` keeps the slot while the route is re-created prepared, and nothing ever publishes it

**Where.**

- 03:727 (A61): "**A re-create does not displace an in-flight transaction.** `status.auth.transaction` has one slot. If a `Lock` or `Create` is already in flight, it continues on the new route, because its policy targets the route by name." The same paragraph says that an Agent recorded as `apikey` "is prepared again … and published only after the anonymous `401`". §5 03:1232 says the same: "An in-flight transaction is not displaced."
- 03:406: `Publishing` is entered by "`Create`, `Loosen`, `Withdraw`", and not by `Lock`. `Lock`'s stages end at `Served` (03:691–695, 03:47).
- 03:694: "A `401` is attributed when `beforeObserved` is set, or when `status.cards[]` records a digest for a revision the route's `backendRefs` currently serve."
- 03:691: `ProbingBefore` "is skipped for a missing policy". So a missing-policy `Lock` never sets `beforeObserved`.
- 03:687 names the cause of a missing policy: "an Argo or Helm prune, a `kubectl delete -l`, or anyone with policy delete in the run namespace".

**What breaks.**

1. Take a served Agent with `status.auth.mode: apikey`. A label-selected prune deletes `<agent>-auth` and `<agent>-serving`. Both carry the operator's `assayd.dev/agent` label (`internal/controller/httproute.go:192`, and §3.2's labels for the policy, 03:45). The two deletes arrive as separate watch events. The same state arises when a policy deleted on its own is followed, within the `Lock`'s window, by a route delete.
2. The policy delete is seen first. A missing-policy `Lock` enters at `ApplyingPolicies` and takes the one slot (03:687, 03:1231).
3. The route delete is seen next. 03:727 re-creates the route with `parentRefs` and no `backendRefs`, because the recorded mode is `apikey`. It records no `Create`, because the slot is taken, and the `Lock` "continues on the new route".
4. The `Lock` reaches `ProbingAfter`. The gateway answers `401` once the policy attaches, as the prepared-route spike measured. The envtest stub's oracle does the same: `401` while the golden policy is stored and taken (03:1272). The route has no `backendRefs`, so it serves no revision, so no digest applies, and `beforeObserved` is unset. **The `401` is never attributed** (03:694).
5. At the deadline, `AuthPolicyMissing` gets the stage appended, and "the machine stays in its stage and keeps working" (03:711, 03:713). It never leaves. Even if it reached `Served`, no stage of `Lock` attaches `backendRefs`, and the route re-create fires only on a *deleted* route (03:727). So the route would stay prepared after that too.

The result is a permanent outage of a served, locked Agent, caused by a routine prune. The only way out is to recreate the Agent, and nothing in the text names that action for this case.

The opposite order has the same gap. Suppose the route delete is seen first, and a re-create `Create` takes the slot. Nothing says whether a missing-policy `Lock` may displace it. If it may, steps 3–5 follow. So the implementer has to guess, in both orders, which transaction owns a missing route and a missing policy together.

The J2 `Lock` does not have this problem. Its recorded mode is `none`, so its route is re-created "published at once with its marker" (03:727). The `Lock` then continues on a route that has `backendRefs`, and `beforeRevision` still matches. The defect is specific to a recorded `apikey`.

**Fix.**

1. In 03:727 and §5 03:1232, say which transaction owns the slot. For example: "For an Agent recorded as `apikey`, the route re-create owns the slot. It displaces a missing-policy `Lock`, whose target is the same record, and its `ApplyingPolicies` re-writes `<agent>-auth` from `status.auth`. A missing-policy `Lock` is entered only while `<agent>-serving` exists with `backendRefs`. A J2 `Lock`, which exists only under a recorded `none`, continues, because its route is published at once." Say also which stage an in-flight re-create `Create` resumes at when its route is deleted again. It should be `PreparingRoute`, by 03:460's re-entry rule.
2. Add a half to case 15, or to case 8: delete `<agent>-auth` and `<agent>-serving` from a served `apikey` Agent, in each order, with the switch held and then released. The route ends published, after the stub's `401`, and `<agent>-auth` equals the golden policy. Mutation: let a missing-policy `Lock` keep the slot through a route re-create.

---

### M2: `beforeRevision` is read from the route, but `beforeObserved` accepts any retained revision's card

**Where.**

- 03:691: `ProbingBefore` sets `beforeObserved` when the answer is "`200` with a body whose digest equals **a** card digest in `status.cards[]`".
- 03:444–446 (A61): `beforeRevision` is "the revision the route's one backendRef **named** when that answer came".
- 03:699 (A61): "It counts only while the route's one `backendRef` still names `beforeRevision`, **the revision that served that card**."
- 03:61 (A61): "A promotion during the `Lock` clears `beforeObserved`, so the probe **never** credits the lock with a new backend's own `401`."

**Why the two differ.**

- `status.cards[]` holds one entry per retained revision, and "two coexist during a rollout" (`api/v1alpha1/agent_types.go:526–528`). The previous active revision stays retained (`internal/controller/agent_controller.go:1393–1398`, `:1415–1427`, `:1450`).
- A revision can be promoted with no digest. A failed fetch clears the digest (`internal/controller/card.go:385–391`) and withholds no traffic (`agent_controller.go:544–549`). Promotion then flips `status.activeRevision` (`:707–716`), and the route is rewritten to it in the same reconcile (`:728–734`, `httproute.go:227`, `:236`).
- The route object moves before the gateway does. The design relies on this lag everywhere else: it is why `Converging` and `ProbingAfter` exist at all (03:664 measured it at under 10 s for a policy). With more than one gateway replica, each replica lags on its own, and 03:668 accepts that a probe reaches whichever replica the load balancer picks.

So the before-answer can come from the previous revision while the route already names the new one.

**What breaks.** Each step is an ordinary edit, applied as a pipeline would apply it.

1. A served `auth: none` Agent serves R0, whose card digest is recorded. The owner bumps the image. That mints R1, whose backend answers its card path `401` to anonymous callers. Case 11's third half treats that as in scope (03:1283). R1 becomes ready, its card fetch fails with an empty digest, and it is promoted. The route now names R1.
2. Seconds later, before the gateway has programmed R1, the owner applies `auth: apikey`. `Lock` enters. `ProbingBefore` reaches R0 through the lagging gateway and gets `200` with R0's card. R0's digest is in `status.cards[]`, so `beforeObserved` is set, and `beforeRevision` is recorded as **R1**, the revision the route names.
3. The gateway catches up to R1. `ProbingAfter` gets R1's own `401`. The route still names R1, which equals `beforeRevision`, so the `401` counts. The J2 edit's R2, if it is not yet promoted, changes nothing.
4. `Served` records `mode: apikey` with "transition observed", the marker is removed, and `GovernanceSkipped` goes `False`. The gateway enforces nothing.

The window is the gateway's programming lag after a promotion, so it is narrow. But 03:699 states an identity, "the revision that served that card", that the recording rule does not establish, and 03:61 says "never". Under this review's standard, both are false guarantees.

**Fix.**

1. Tie the match to the recorded revision. At 03:691, set `beforeObserved` only when the `200`'s digest equals the digest `status.cards[]` records **for the revision the route's `backendRef` names**, and record that revision as `beforeRevision`. Then a `200` from any other backend leaves `beforeObserved` unset. That includes a lagging replica's older revision, and a revision with no digest. The probe falls to the digest rule instead. This is the tenth critique's own wording: "the revision digest whose card the before-answer matched".
2. Add a fifth half to case 11. The route names r1, which has no digest, and the stub's before-answer is r0's recorded card, as a lagging gateway would serve it. The stub then answers r1's backend `401`. The Agent never reaches `Served`. Mutation: set `beforeObserved` on a match against any digest in `status.cards[]`.

---

## MINOR

1. **The stale-state rule still abandons a route re-create** (03:460): "An unfinished `Create` or J2 `Lock` whose target mode no longer matches the desired one is not re-entered: it is abandoned (§3.3.3)."
   - The tenth critique's fix named this sentence alongside 03:717 and §5. A61 changed the other two.
   - The cross-reference to §3.3.3 makes the intent recoverable. But read on its own, the sentence says the opposite of 03:717.
   - **Fix**: "An unfinished spec-driven `Create` or J2 `Lock` … Neither re-creation is abandoned (§3.3.3)."
2. **Nothing says how the operator tells a re-creation from a spec-driven transaction** (03:451: "For a spec-driven transaction it is compared with the desired `-auth`. For either re-creation it is compared with `status.auth`").
   - The record carries no flag (03:426–447). The distinction can be computed: a re-creation is a `Create` beside a recorded `mode`, or a `Lock` whose `targetMode` equals the recorded `mode`. The key comparison and the abandonment exemption both depend on it.
   - **Fix**: state that derivation in one sentence at 03:451.
3. **A61's message sentence names a condition a re-create `Create` does not raise before its deadline.**
   - 03:727: "While the re-created route is unpublished, the Agent's messages say so: `PolicyApplyIncomplete`'s and, for an I1 Agent … `PolicyCompileFailed`'s too."
   - A `Create` raises `PolicyApplyIncomplete` only at its deadline (03:709, 03:1227). So for a served, non-I1 `apikey` Agent whose route was deleted, nothing says the route is unpublished for the first 4 minutes. By contrast, the missing-policy case raises `AuthPolicyMissing` at once (03:702, 03:711).
   - **Fix**: either raise `PolicyApplyIncomplete` at entry for a route re-create, as `AuthPolicyMissing` is raised, with its own reason, or say that the sentence describes the deadline's message.
4. **Cases 6 and 11 no longer say how the test drives the rollout, and A61 made the outcome depend on it.**
   - Case 6 asserts that the edit's "weight shift is not held" and that the Agent reaches `Served` "only after the stub's answer turns from `200` to `401`" (03:1278). Once the minted revision is promoted during the `Lock`, `Served` also needs a digest recorded for that revision (03:699). Envtest has no kubelet, so the test must write that digest itself, and case 6 does not say to.
   - Case 11's first half asserts "transition observed" (03:1283). That now holds only if the test keeps r2 unpromoted until `Served`.
   - In a real cluster the J2 edit's revision is commonly promoted while the lock propagates, so "transition not observed" will be the common message. The text should say so, so that nobody reads that message as a fault.
   - **Fix**: case 6 records r2's digest and expects "transition not observed" once the shift lands. Case 11's first half holds r2 unready. One sentence at 03:699 says which message the ordinary J2 path usually yields.

---

## Verified sound

- **`beforeRevision` survives a crash.** It lives in `status.auth.transaction`, beside `written` (03:436–446). After `written`, re-entry skips `ProbingBefore` (03:460, 03:691), so the recorded pair stands. Before `written`, `ProbingBefore` re-runs and records a fresh pair. The code writes the route before the status in the same reconcile (`agent_controller.go:733`, then `:773`). So a crash between them leaves the route on the new revision while status still names the old one. The rule is written as a comparison with the route at attribution time ("counts only while the route's one `backendRef` still names `beforeRevision`"), not only as a clear on promotion, and that comparison catches this. The implementer must evaluate the comparison at attribution, not rely on the clear alone. The text supports that reading.
- **The missing-policy `Lock` needs no `beforeRevision`.** It skips `ProbingBefore` (03:402, 03:691), never sets `beforeObserved`, and attributes only through the digest rule, as 03:699 says. The one defect on this path is M1's route re-create, not `beforeRevision`.
- **A rollback to the before-revision fails safe.** A promotion away from `beforeRevision` clears `beforeObserved` for good, and a later return to that revision does not restore it. The `401` then needs that revision's digest, which is retained because it is active again (`agent_controller.go:1393–1395`). That is conservative. Promotions are the operator's own writes (`:707–716`), so no promote-and-return can happen unseen between two of its reconciles.
- **A re-create's target never comes from the spec.** On a served Agent in the slice, the only spec-driven transaction that can be in flight is a J2 `Lock` (recorded `none`, target `apikey`). The route re-create does not override it: it publishes under the recorded `none` with its marker (03:727), and the `Lock`'s target is the one J2 legitimately takes from the spec. A never-served `Create` has no recorded `mode`, so 03:727 does not apply to it. So no path lets the spec override `status.auth` for a re-creation.
- **The serving route has exactly one `backendRef`, and it follows `status.activeRevision`** (`httproute.go:227`, `:236`, `:238–241`; `agent_controller.go:728–734`). So "the route's one `backendRef`" (03:699) is well defined in the slice. The `backendRef` names the revision's Service, `WorkloadName(agent.Name, rev)`, so `beforeRevision` can be compared by revision name.
- **The J2 edit mints a revision**, as 03:41 says (`internal/revision/leaves.go:261`).
- **Case 15's new mutation fails both halves it names.** Under "abandon a re-create on a desired-mode mismatch", (a) would publish with the marker, or delete the route, and (b) would flap. Both contradict the end states the case asserts (03:1287).
- **The constants cited are real**: `CardRetryInterval` 15 s (`card.go:81`), `CardDriftInterval` 5 min (`card.go:309`), and `FetchedAt` on the CRD (`agent_types.go:567`).

## A61's author's calls

| Call | My view |
|---|---|
| Tie `beforeObserved` to a revision, rather than hold the J2 promotion until `Served` | **Right in direction.** Holding the promotion would stall an ordinary rollout behind a probe. But the revision must be the one whose card matched, not the one the route names (M2). The choice is technical, and no human is needed |
| A re-create never displaces an in-flight transaction | **Right for the J2 `Lock`, wrong for the missing-policy `Lock`** (M1). A re-create under a recorded `apikey` must own the slot, because only `Create` publishes. No human is needed |
| Abandonment exempts both re-creations, keyed on where the target came from | **Right.** It is the only reading under which case 15's end states hold. One sentence at 03:460 is left behind (MINOR 1) |

## The smallest set of changes to approvable

1. **M1**: under a recorded `apikey`, the route re-create owns the slot and displaces a missing-policy `Lock`. A missing-policy `Lock` is entered only while the route exists with `backendRefs`. Add the "both deleted, in either order" half to case 15 or case 8, with its mutation.
2. **M2**: set `beforeObserved` only on a match against the digest recorded for the revision the route names, and record that revision as `beforeRevision`. Correct "never" at 03:61. Give case 11 a fifth half and its mutation.

MINORs 1 and 2 should ride along, because each is one sentence and each touches text M1 changes. MINORs 3 and 4 may stay open.

`03-policy-compiler.md (first slice, §1.1): REVISE`
