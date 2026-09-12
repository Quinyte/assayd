# Design 03 (policy compiler): critique of A58, scoped to the first slice (§1.1)

- **Reviewer**: an independent adversarial critique, following `.claude/skills/critique-design/SKILL.md` and AGENTS.md. It is a cold read with no author context. This is the eighth critique of the consolidated body, and the third to read §1.1.
- **Target**:
  - `docs/designs/03-policy-compiler.md` at `origin/design-03-a58` `0f613af` (PR #23);
  - `docs/decisions/0034-slice-auth-follows-current-spec-narrows-only-on-a-probe.md`, including Amendments 1–3;
  - `docs/research/agentgateway-prepared-route-401-spike-2026-09.md`;
  - `docs/designs/reviews/03-a57-critique.md`.
- **Scope**: the human's decision G limits approval to §1.1's slice. `Narrow`, `Loosen`, E2, F2's group rules, D2, `Withdraw`, the other concerns and the candidate route are out of scope. A finding is raised against them only where the slice depends on them or they contradict it.
- **Method**:
  - Each finding of the seventh critique was checked in the body, at its source.
  - The slice was then walked as an implementer and as an adversary: `Create`, both `Lock` entries, abandonment, the prepared re-create, I1, `Adopt`, the probe, the stub's "taken" switch, and every condition and reason the slice sets.
  - That walk was checked against the code it lands in: `internal/controller/{card,conditions,httproute}.go`, `api/v1alpha1/agent_types.go`, `config/rbac/role.yaml`, `cmd/operator/main.go` and `docs/designs/10-observability-pack.md`.
  - Nothing was run against a cluster. No file other than this one was written.

## Verdict for the first slice: **REVISE — 0 BLOCKER, 2 MAJOR, 6 MINOR.**

A58 closes both MAJORs and all seven MINORs of the seventh critique in the body. The slice is one step from approvable. Two defects remain, and each makes an implementer either guess or build a false status:

- **`Lock`'s "transition not observed" fallback accepts a bare `401` even when nothing proves the backend answers that path anonymously.** An Agent whose own backend refuses its card path anonymously reaches `Served`, reason `Governed`, on the backend's `401`, whether or not the policy took (MAJOR 1). The seventh critique listed that attribution as sound. It was sound only when a card digest is recorded, and I missed that condition too.
- **A58's own text holds a never-served `oauth` Agent's `Create` "at its first stage", and A58's new deadline covers every stage.** Read together, they page every such Agent about 9 minutes after the compiler is enabled, under the wrong cause (MAJOR 2).

Both fixes are one rule and one test each.

---

## Closure against `reviews/03-a57-critique.md`

| Finding | This pass |
|---|---|
| **M1**: `ProbingBefore` blocked the write, had no outcome and used a fixed path | **Closed.** Every probe requests `spec.card.path` (03:607), the path `card.go:165–168` fetches. `ProbingBefore` is one observation that never gates the write (03:398, 03:593, 03:680). Every stage of `Create` and `Lock` has a deadline outcome (03:694–702, §5 03:1216–1217), and the stalled-`Lock` page is timed honestly at about 9 minutes (03:702). The attribution text is corrected (03:688). Cases 11 and 12 carry mutations (03:1272–1273). The residue is MAJOR 1 below, a hole in the corrected paragraph |
| **M2**: a revert mid-transaction had no outcome | **Closed.** The abandonment rule is at 03:704–712. §3.3.1's delete rule now names three paths and is keyed on the transaction's own `written` record (03:511), so it cannot meet I1. The marker label's behaviour is covered (03:535, 03:708), and so are §5 (03:1218) and case 10, whose two mutations each fail a different half (03:1271). The residue is MINOR 2 and MAJOR 2 |
| MINOR 1: `Adopt` keyed on `mode` | **Closed** (03:440, 03:591, 03:724), with case 13 (03:1274) |
| MINOR 2: no "taken" switch in the stub | **Closed** (03:1261). Case 8 asserts on the first created version (03:1269). The residue is MINOR 3 |
| MINOR 3: stale transaction lists | **Closed** (03:573, 03:583, 03:1215) |
| MINOR 4: `status.auth` optionality | **Closed** (03:412–421, 03:440) |
| MINOR 5: no reason for a served `none` Agent | **Closed**: `AuthOptedOut` (03:250, 03:53). The residue is MINOR 5 |
| MINOR 6: design 10's rationale | **Closed as owed** (03:78) |
| MINOR 7: a route delete on an `Adopt`ed Agent | **Settled** (03:718), as an author's call |
| One-way lock | **Stated** in §1.1 (03:39) and in `AuthLockPending`'s message (03:518, 03:691), as ADR-0034 Amendment 3 asks |

---

## MAJOR

### M1: a bare `401` is accepted as `Lock`'s proof when nothing shows the backend answers that path anonymously

**Where.**

- Without `beforeObserved`, "a `401` alone is accepted" (03:688), for a J2 `Lock` whose before-request saw anything else, and for every `Lock` of a missing policy (03:683, 03:1220).
- The justification is conditional: "a serving revision **whose card digest is recorded** answered that path anonymously … so a `401` there now comes from something in front of the backend" (03:688). The rule does not carry that condition.
- A card digest can be absent from a revision that serves. `status.cards` has "one entry per live revision" (`api/v1alpha1/agent_types.go:526–528`), and it gets that entry only from a fetch that got a `200` and a valid card (`card.go:195–251`). "A failed fetch does NOT withhold traffic" (`card.go:51–54`).
- The subsection opens by withdrawing the old witness because "a 401 can come from the backend" (03:580).

**What breaks.**

1. Take a served `auth: none` Agent whose workload answers `401` to an anonymous `GET` of its card path. It serves anyway, and `status.cards` has no entry for it.
2. The owner sets `apikey`. `ProbingBefore` sees `401`, so `beforeObserved` is false, and the policy is written.
3. `ProbingAfter` then gets the backend's own `401` at once, before any gateway replica has read the policy, and whether or not it ever does.
4. The Agent reaches `Served` with `mode: apikey`, the marker label removed, and `GovernanceSkipped=False, Governed`. The message says "transition not observed", which reads as "weaker", not "unattributed".

That breaks J2's one guarantee, "Status never claims the route is locked" (ADR-0034:45; 03:691). A policy that is never taken and raises no NACK is exactly what the probe exists to catch (§3.3.2).

**Fix.**

1. Accept a `401` without `beforeObserved` only when `status.cards` records a digest for a revision the route currently serves.
2. Otherwise `ProbingAfter` cannot pass. At the deadline the Agent reports `AuthLockUnverified` (or `AuthPolicyMissing`), with a message saying the `401` cannot be attributed because the backend's anonymous answer on the card path was never observed.
3. Owe a third half to case 11: no card digest is recorded, and the stub's backend answers `401` at the card path. The Agent must not reach `Served`. The mutation is to accept a bare `401` unconditionally.

### M2: a never-served Agent whose `-auth` does not compile holds a `Create` that the new deadline pages on, under the wrong cause

**Where.**

- For `oauth`, "a never-served Agent's `Create` holds at its first stage" (03:709). §3.3.1 has the same shape: "its `Create` does not advance" (03:512).
- "The deadline is set on entering the first stage … so no stage can wait without one" (03:694). `Create` at the deadline in any stage, `PreparingRoute` included, raises `PolicyApplyIncomplete`, reason `AuthEnforcementUnverified` (03:698).
- Design 10 pages on `PolicyApplyIncomplete` open for more than 5 minutes, by type (`10-observability-pack.md:50`). `PolicyCompileFailed` is only a ticket there.
- Yet `status.auth.transaction.targetDigest` is "the policy being applied" (03:424), and `oauth` compiles no policy.

**What breaks.** Every stored Agent with an `expose.a2a` block carries `oauth`, because the API server persisted the old default (03:1039). An install that turns the compiler on has every such never-served Agent read literally as follows:

- a `Create` is entered, with no target;
- `PreparingRoute` may create a route that answers `500` on a live hostname indefinitely;
- 4 minutes later `AuthEnforcementUnverified` is raised, naming a stage and "unmet condition" that are not the cause;
- 5 minutes after that, a page.

That is rule 8's loud-and-wrong, fleet-wide, on the first enable. The other reading, no transaction, is equally consistent with 03:424. So the implementer has to guess, and case 12 (03:1273) can be written either way. A stored `spec.budget` (`ConcernNotBuilt`, 03:1224) is the same case.

**Fix.**

1. State that a `Create` or `Lock` is entered only for a desired `-auth` that compiles, one that has a `targetDigest`.
2. While the desired `-auth` does not compile, no transaction, no deadline and no prepared route exist. `PolicyCompileFailed` stands alone.
3. Replace "holds at its first stage" (03:709, 03:512) with that.
4. Owe a case: a never-served `oauth` Agent past the deadline carries no `PolicyApplyIncomplete` and no route. The mutation is to enter `Create` for an uncompilable target.

---

## MINOR

1. **The no-auth marker label has no key or value.** It is named at 03:535, 03:684, 03:708 and 03:716, and case 10 asserts on it (03:1271), but neither design 03 nor design 07 spells it out. The golden file and the case cannot be written without inventing one. **Fix**: name it, for example `assayd.dev/auth: none`, and say whether design 07's reservation covers it.
2. **Abandoning a `Create` past `Publishing` does not order its two writes.** It deletes the policy and detaches `backendRefs` (03:707–708). For an abandonment to `oauth`, deleting first leaves a route with backends and no policy until the detach, or until the next reconcile after a crash. §3.3 step 5 (03:383) implies the order, but the rule does not say it. **Fix**: detach first, then delete, and add a case-10 mutation that reverses them.
3. **The stub's oracle cannot express case 11's second half.** It answers only `401`, `500`, `200` or `404`, from route state (03:1261), but case 11 needs a `503` (03:1272), and M1's new half needs a backend `401`. **Fix**: add a backend-answer override to the stub.
4. **`ProbingAfter` has no stated per-request timeout or requeue interval** (03:401, 03:683). `ProbingBefore` has one (03:680). **Fix**: reuse `DefaultCardFetchTimeout` and state an interval, for example the 5 s the e2e polls at.
5. **The case count at 03:250 is stale.** It reads "four … and a fifth", but `ForeignTrafficPolicy` is a sixth reason under which `GovernanceSkipped` stays `True`. This is cosmetic.
6. **An operator-named `<agent>-auth` beside `mode: none`, with no transaction, has no rule.** Anyone with policy create in the run namespace can write one, because nothing reserves it (§6). It passes the name-and-label test (03:291), none of the three delete paths applies (03:511), and there is no recorded policy to re-assert. The route may then be locked while status says `AuthOptedOut`. That fails toward refusal. **Fix**: report it, for example as `ForeignTrafficPolicy`, or add it as a fourth delete path.

The sixth critique's MINORs 3, 4 and 5's gate stay recorded open (03:1332, 03:1338). They are not re-raised.

---

## Verified sound

- **Abandonment does not meet I1.** Deletion is keyed on the positive `written` record of a transaction short of `Served` (03:511, 03:707). A recorded policy, and one `status.auth` does not mention, are both untouchable. A re-create of a missing policy is never abandoned (03:706). Case 10(c)'s mutation (03:1271) separates "keyed on `written`" from "keyed on desired `none`".
- **"One status update" closes the `Adopt` race** (03:709). With the whole-record trigger (03:724), no reconcile can see an empty `status.auth` between abandonment and the next transaction.
- **The probe path.** The route matches `PathPrefix: /` (`internal/controller/httproute.go`, `servingRouteFor`), so every card path rides `-auth`. The 5 s bound is `card.go:78`.
- **Conditions.** The slice's types exist (`agent_types.go:436–444`). `GovernanceSkipped` is owned and sticky (`conditions.go:75`, `:102`). `PolicyApplyIncomplete` is owned and not sticky (`conditions.go:80`). `PolicyCompileFailed`'s ownership is honestly owed (03:53, case 9).
- **RBAC and flags are honestly owed.** §3.2 asks for the six `httproutes` verbs on `agentgatewaypolicies` (03:297). That includes `delete`, which abandonment needs. Today `role.yaml` grants none of them, and only `create, patch` on `events`. No `--gateway-serving-url` exists (`cmd/operator/main.go:74–103`, owed at 03:74).
- **The `Create` probe premise** rests on the measurement (the note, lines 17–25). The `http`-listener case is owed (03:1276).

## A58's author's calls

| Call | My view |
|---|---|
| A single before-request, bounded by the 5 s card timeout | **Defensible.** It is an observation, sent once per transaction, in the same shape as the card fetch (`card.go:55–69`). No human needed |
| Deadlines reuse the transaction's reason in every stage | **Defensible.** Design 10 pages by type, so per-stage reasons buy no routing, and the message carries the stage. It is also what makes MAJOR 2 bite, so fix that, not the call. No human needed |
| Abandonment also covers an edit to `oauth` | **Defensible, and consistent with I1.** The last *good* `-auth` of a `Lock`'s Agent is its recorded `none`, and an unprobed policy was never good. Keeping it would contradict status. The cost: a lock that had in fact taken is reopened by an `oauth` edit, and the owner gets only a `PolicyCompileFailed` ticket. No human needed, but the message should say the route is open again |
| An abandoned `Create` detaches the `backendRefs` it attached | **Defensible.** Nothing recorded the route as served. For `none` the detach is followed at once by a republish, which is a flap. It needs MINOR 2's ordering. No human needed |
| `AuthOptedOut`, set `True` | **Right.** `False` would read as governed for a route anyone can call, and `GovernanceSkipped` never alerts, so there is no fleet noise. No human needed |
| Deleting an `Adopt`ed Agent's route does not lock it | **Defensible.** It follows the consent decision. One question for the human, not a finding: the owner of an `Adopt`ed Agent who edits `none` → `apikey` has consented, and stays refused (03:729). Amendment 3's reason does not cover that case |

## The smallest set of changes to approvable

1. **M1**: gate the bare-`401` fallback on a recorded card digest for a revision the route serves, and add case 11's third half.
2. **M2**: no `-auth` transaction, deadline or prepared route while the desired `-auth` does not compile, with its case.

MINORs 1 and 2 should ride along. The rest may stay open.

`03-policy-compiler.md (first slice, §1.1): REVISE`
