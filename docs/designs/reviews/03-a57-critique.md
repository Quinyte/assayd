# Design 03 (policy compiler) — critique of A57, scoped to the first slice (§1.1)

- **Reviewer**: independent adversarial critique, per `.claude/skills/critique-design/SKILL.md` and AGENTS.md. Cold read, with no author context. This is the seventh critique of the consolidated body, and the second to read §1.1.
- **Target**: `docs/designs/03-policy-compiler.md` at `origin/design-03-a57` `bb8ca95` (PR #22); `docs/decisions/0034-slice-auth-follows-current-spec-narrows-only-on-a-probe.md`, including Amendments 1 and 2; `docs/research/agentgateway-prepared-route-401-spike-2026-09.md`; `docs/designs/reviews/03-a56-critique.md`.
- **Scope**: the human's decision G limits approval to the first slice drawn in §1.1. `Narrow`, `Loosen`, E2, F2's group rules, D2, `Withdraw`, the other concerns and the candidate route are out of scope. A finding is raised against them only where the slice depends on them or they contradict it.
- **Method**:
  - Each in-scope finding of the sixth critique that A57 claims fixed was checked in the body, at its source, and J2 was checked against ADR-0034 Amendment 2.
  - The slice was then walked as an implementer and as an adversary: `Create`, `Lock` in both of its cases, the prepared re-create of a deleted route, I1, `Adopt`, the probe, the envtest stub, and every condition and reason the slice sets.
  - That walk was checked against the code it lands in: `internal/controller/{httproute,card,conditions}.go`, `api/v1alpha1/agent_types.go`, `config/rbac/role.yaml`, `charts/assayd/templates/admission.yaml`, `cmd/operator/main.go` and `hack/e2e.sh`.
  - **k3d `assayd-local` was read, and nothing was created.** The Gateway `assayd-gateway/assayd` has two listeners, `http:8080` and `tools:8081`. No `AgentgatewayPolicy` exists anywhere in the cluster. The note's result was **not** re-measured on `http`: a server-side dry-run cannot answer an HTTP request, and a real measurement needs objects created, which this review was not permitted to do.
  - No file other than this one was written.

## Verdict for the first slice: **REVISE — 0 BLOCKER, 2 MAJOR, 7 MINOR.**

A57 closes all three MAJORs of the sixth critique in the body.

- A deleted `<agent>-auth` is now `Lock`, not `Adopt`.
- The slice's admission rule admits `apikey` with no `allowedGroups`.
- The `401`-before-`500` premise is measured.

J2 is written in faithfully. The slice is now close enough that the remaining defects are all in the one thing A57 added, `Lock`. There are two, and each makes an implementer guess:

- **`Lock`'s `ProbingBefore` blocks the write, and has no outcome if it never passes.** Its probe path is fixed while the Agent's card path is not. So an Agent with a custom `card.path` that is moved to `apikey` is never locked. It stays open, and nothing pages (MAJOR 1).
- **A revert during an unfinished `Lock` has no specified outcome.** It is the most likely thing an owner does after a lock breaks callers who hold no key (MAJOR 2).

---

## Closure against `reviews/03-a56-critique.md`, in scope

| Finding | This pass |
|---|---|
| **M1** — a deleted `-auth` or route had no outcome | **Closed.** `Adopt` keys on `status.auth.mode` (03:581, 03:690). A missing policy on a served Agent is `Lock` from `ApplyingPolicies` (03:664, 03:1185). The I1 bullet routes deletion out of the compile-failure rule (03:501). A deleted route is re-created prepared (03:684, 03:1186), and is honestly marked owed to design 07's emitter (03:72). Cases 7 and 8 each carry a mutation (03:1233–1234). The residue is MINORs 1 and 2 |
| **M2** — the owed CEL made `apikey` unwritable | **Closed.** The slice's admission rules are stated exactly (03:999–1001). The later rule is `!has(self.allowedGroups) \|\| self.auth == 'apikey'`, with a duty to keep admitting the slice's shape (03:1001). The correction is marked where it stood (03:1003). Design 02's admission case (03:1242) and the golden file (03:1236) are owed |
| **M3** — `401`-before-`500` unmeasured | **Closed by measurement** (the note, lines 17–21; 03:26, 03:597). The note's `tools`-not-`http` caveat is disclosed (03:602), and the `http` case is owed (03:1237). I read the two listeners in `hack/e2e.sh:265–290`: they differ only in port and in the `allowedRoutes` selector, and neither carries a listener-level policy. So relying on the note is reasonable, and the owed case is the right disposition |
| **MINOR 1** — the stub could let case 3's mutation survive | **Closed** (03:1226, 03:1229). The residue is MINOR 2 |
| **MINOR 2** — `PolicyCompileFailed` not owned | **Closed as a design statement** (03:50, 03:1235). The code change is owed. `internal/controller/conditions.go:47–81` still omits the type |
| **MINOR 6** — omitted wiring | **Closed** (03:51, 03:73) |
| **MINOR 9**, key label | **Closed** (03:1238) |
| **J2** | **Written in faithfully.** ADR-0034:45–49 says the same things as 03:35, 03:508 and 03:661–680. `apikey` → `none` stays refused (03:509). The ADR records the deleted-`-auth`-through-`Lock` routing as an author's call (ADR-0034:49), which is correct |

MINORs 3, 4, 5's gate, 7, 8 and the rest of 9 are recorded open (03:1288). MINOR 8 is re-raised below as MAJOR 2, because `Lock` makes it reachable on a serving route. MINOR 7 becomes MINOR 5 here.

---

## MAJOR

### M1 — `Lock`'s `ProbingBefore` blocks the write, has no outcome if it never passes, and probes a fixed path the Agent can move

**Where.**

- `ProbingBefore` for a J2 `Lock` sends an anonymous `GET /.well-known/agent-card.json` until one gets `200` with a recorded card digest (03:668). The policy is written only after it (03:583, 03:669).
- The stage advances on "`RequeueAfter`, until the deadline" (03:394). The deadline's outcome covers only "a J2 `Lock` whose probe **still gets `200`**" (03:680). That is `ProbingAfter`'s failure, not `ProbingBefore`'s.
- The operator fetches the card from `spec.card.path` (`internal/controller/card.go:165–167`), which is configurable (`api/v1alpha1/agent_types.go:146–149`). The probe path is fixed (03:593, 03:668).
- 03:676 attributes a bare `401` through "the operator's own card fetch … an anonymous `GET` of **the same path**".

**What breaks.**

- Take an Agent with `card.path: /card.json`, served under `auth: none`, whose owner sets `auth: apikey`.
- The probe reaches the backend on a path it does not serve. It gets `404`, never a matching `200`, so the policy is **never written**.
- The Agent sits at `GovernanceSkipped=True`, reason `AuthLockPending`, indefinitely. Design 10 says `GovernanceSkipped` "is never an alert" (`10:50`), and no `PolicyApplyIncomplete` is raised. The route the owner asked to lock stays open, with no page. That is NFR-8's silent degraded path.
- The same happens when the workload is down, or when a Gateway-level policy already answers `401` (the sixth critique's MINOR 5).
- For this Agent, 03:676's attribution argument is also false, because the card fetch and the probe hit different paths.
- And gating the write on the "before" answer delays closing a route the owner asked to close, in exchange for attribution the design already gives up in `Lock`'s other two entries (03:676).

**Fix.**

1. Probe `spec.card.path`, the path `card.go` fetches, in both `ProbingBefore` and `ProbingAfter`.
2. Make `ProbingBefore` observational for `Lock`: one round, record the answer, then write whatever it was. Without a digest-matching `200`, accept `ProbingAfter`'s `401` with the message "transition not observed", as 03:676 already does for a deleted policy.
3. State that the deadline covers **every** stage of `Lock`: `PolicyApplyIncomplete`, reason `AuthLockUnverified`, naming the stage and the last answer.
4. Owe an envtest case with a non-default `card.path`, and a mutation that probes the fixed path.

### M2 — A mode change during an unfinished `Lock`, or `Create`, has no outcome, and `Lock` makes the revert ordinary

**Where.**

- The only paths that delete `<agent>-auth` are the finalizer and "a compiled, applied change to `auth: none`, which is out of the slice" (03:501).
- `apikey` → `none` is refused against the **served** mode (03:509, 03:1184). During a `Lock`, the served mode is still `none` (03:672).
- A `Lock` can reach `Served` "while the key source holds no key in the Agent's group" (03:677). So a lock can refuse every existing caller.
- The sixth critique's MINOR 8, a mode change during an unfinished `Create`, is recorded open (03:1288).

**What breaks.** An owner moves a served Agent from `none` to `apikey`, callers start getting `401`, and the owner reverts to `none` before `Served`, or after `AuthLockUnverified`. Now:

- the desired mode, `none`, equals the recorded one, so this is not the refused transition;
- the written policy is outside the desired set, and 03:501 forbids deleting it.

An implementer has to choose between two outcomes:

- Keep the policy. The route may be locked while `status.auth.mode` says `none` and the marker label says unauthenticated (03:525). That is loud-and-wrong under rule 8.
- Delete it. The body does not permit that.

The same gap covers a never-served `apikey` Agent switched to `none` mid-`Create`.

**Fix.**

1. State that an `-auth` transaction short of `Served` is abandoned when the desired `-auth` returns to the recorded state.
2. State that the operator may delete an `<agent>-auth` whose `status.auth.mode` never recorded it. Deleting it returns to the recorded state: open for `Lock`, unpublished for `Create`. Clear `AuthLockPending`.
3. Owe an envtest case, with the mutation that keeps the policy, which the assertion "no stored `<agent>-auth` beside `mode: none` and the marker label" must fail.

For the human, separately: after `Served`, J2's lock is one-way. A lock that breaks callers can be undone only by recreating the Agent. That is the human's decision, and the `AuthLockPending` message should say it before the lock lands.

---

## MINOR

1. **`Adopt` keys on `mode` absent, not on `status.auth` absent.** `Create` attaches `backendRefs` at `Publishing` and records `mode` only at `Served` (03:398–399, 03:430).
   - Suppose a crash or a concurrent `<agent>-auth` delete lands between the two.
   - The next reconcile then finds a published route, no emitted `-auth` and no `mode`. That is `Adopt` (03:690), and it overwrites the `Create` record with `{Adopt, Refused}` (03:693). The route stays open for good, with no page.
   - **Fix**: trigger `Adopt` only when `status.auth` is absent altogether, as the sixth critique proposed. A missing policy beside a `Create` or `Lock` transaction re-enters that transaction.
2. **The pinned stub oracle cannot express two owed failure halves, and case 8's mutation can survive.** The oracle answers `401` whenever the stored policy equals the golden one (03:1226).
   - Case 4's "probe still failing at its deadline" and case 6's "policy withheld from the stub" (03:1230, 03:1232) need a stored policy the stub ignores, and the oracle has no such mode.
   - In case 8 the kept policy makes the stub answer `401` the moment the route exists. So a polling test may never observe the prepared state, and "re-create with `backendRefs`" survives (03:1234).
   - **Fix**: add a "not yet taken" switch. In case 8, hold it, assert on the **first created version** of the route, and then release it.
3. **Stale transaction lists.**
   - 03:563 lists `Create`, `Loosen`, `Withdraw`, `Narrow` and the refused `Adopt`, and omits `Lock`.
   - 03:573 names `Narrow` as the only in-place `-auth` write.
   - The §5 row at 03:1181 says "a probe, `Create`'s or `Narrow`'s".

   **Fix**: add `Lock` to all three.
4. **The `status.auth` schema contradicts its own text.**
   - `keySource`, `admittedGroups` and `appliedDigest` are shown as required (03:410–412).
   - 03:430 says `{mode: none}` carries none of them. Design 02 will build from the schema.

   **Fix**: mark them optional, and present exactly when `mode: apikey`.
5. **A served `auth: none` Agent still has no `GovernanceSkipped` reason** (the sixth critique's MINOR 7). It now matters more.
   - It is `Create`'s end state for `none` and J2 `Lock`'s start state.
   - §3.1's clearing rule lists four `True` reasons, and none of them is a deliberate opt-out (03:246).

   **Fix**: name one, for example `AuthOptedOut`.
6. **Design 10's page rationale is false for two of the slice's reasons.** `10:50` pages on `PolicyApplyIncomplete` because "a route is withheld, so traffic is being refused". For `AuthPolicyMissing` and `AuthLockUnverified` the route is open. The page still fires, because it keys on the type and a duration. **Fix**: record the corrected rationale as owed to design 10.
7. **Deleting an `Adopt`ed Agent's route is an unstated exit from `Adopt`.**
   - 03:684 re-creates a deleted route through `Create` "for an Agent with `status.auth`". A refused `Adopt` writes `status.auth` (03:693).
   - So deleting the route may lock an `Adopt`ed Agent without recreating it. `Adopt`'s message says "recreate the Agent" (03:694).

   **Fix**: say which.

---

## Verified sound

- **Re-creating a deleted `-auth` does not contradict I1.**
  - I1 forbids deleting or rewriting an **existing** `-auth` on a compile failure. Re-creating a missing one from `status.auth` (03:664) holds even when the current spec is uncompilable (`oauth`) or refused (`apikey` → `none`).
  - That is the right direction: the recorded lock comes back.
- **Skipping `ProbingBefore` for a deleted policy is sound.**
  - The re-created policy is identical to the deleted one, being a pure function of `status.auth`.
  - So a `401` after §3.3.2's tuple converges means the dataplane holds a config that contains that policy.
- **The card-fetch attribution is real, for the path it fetches.** `card.go:181–195` fetches anonymously from the revision Service's ClusterIP, not through the gateway. So a recorded digest proves the backend answered that path without a credential. The path is MAJOR 1.
- **Conditions.**
  - The slice's types exist (`agent_types.go:436–444`).
  - `GovernanceSkipped` is owned and sticky (`conditions.go:75`, `:102`), so `AuthLockPending` and `AuthPolicyMissing` flip rather than linger.
  - `PolicyApplyIncomplete` is owned and not sticky (`conditions.go:80`), so `AuthPolicyMissing` clears when the probe passes.
- **The dependency list is honest.**
  - `events` has `create, patch` only (`role.yaml:22–28`).
  - No `agentgatewaypolicies` RBAC is granted (`role.yaml:103–113`).
  - The admission policies reserve namespace labels and gateway routes only (`admission.yaml:25–59`, `:83–108`).
  - No `--gateway-serving-url` flag exists (`cmd/operator/main.go:74–103`).
  - Design 07's A6.10–A6.13 are uncritiqued (`07:3`).
  - `Create` and `Lock` mint no keys, so the slice needs no new `configmaps` verb. The "names the operator as a permitted writer" clause at 03:1245 belongs to `Narrow`, not to the slice.

## A57's author's calls

| Call | My view |
|---|---|
| `Lock` as a sixth transaction, with reasons `AuthLockPending`, `AuthLockUnverified` and `AuthPolicyMissing` | **Defensible.** `Narrow`'s `none` → `apikey` row is out of the slice and carries keyed-probe machinery. A separate kind keeps the slice self-contained, and the names fit the vocabulary. No human needed |
| The card-digest `200` pre-check, and the "transition not observed" fallback | **The fallback is defensible. The pre-check is defensible only as an observation, not as a gate on the write** (MAJOR 1). No human needed once that is fixed |
| A deleted `-auth` raises at once and does not withdraw the route | **Defensible.** It follows §3.2's argument that withdrawing on a foreign write hands an off switch to anyone who can delete a policy. It raises at once and pages after 5 minutes, not at once. No human needed |
| A deleted route is re-created without backends, costing an outage for the probe window | **Defensible.** The prepared shape is the measured one, and a re-created route with `backendRefs` would put its traffic before an unmeasured re-attachment. The cost is roughly 10 s of `500`, after a delete that had already caused an outage. `TestTheOperatorRecreatesARouteThatWasDeleted` must change. No human needed |
| `status.auth` gains `mode` | **Defensible and needed.** But key `Adopt` on the record being absent (MINOR 1), and fix the schema's optionality (MINOR 4). No human needed |
| The e2e is relabelled, not the constant | **Right.** A test value compiled into the operator would become every install's selector. It costs every gateway e2e a key. No human needed |
| **Are `Lock` and `Adopt` the same mechanism, and should `Adopt` stay refused?** | **The same mechanism, but not the same decision. Needs the human.** ADR-0034's stated reason for refusing `Adopt`, that it would trust conditions, no longer holds, as Amendment 2 says itself (ADR-0034:48). What separates them now is **consent**. `Lock` closes a route that its owner asked to close, or that the compiler had recorded as closed. `Adopt` would close, at upgrade, a route that nobody asked to change, and every caller without a key would start getting `401` (03:677: nothing proves a key exists). Un-refusing it trades an open route for an outage caused by an upgrade. That is an availability-versus-security choice. Its population today is the e2e harness alone (03:1005), so keeping it refused in the slice is defensible. ADR-0034 should restate why it stays refused, and MINOR 7 should be settled with it |

## The smallest set of changes to approvable

1. **`Lock`'s `ProbingBefore`** (M1): probe `spec.card.path`, make the before-probe non-blocking, give the deadline an outcome in every stage, and owe the custom-path case.
2. **An abandon rule for an unfinished `-auth` transaction** (M2), with the deletion it needs and its case.
3. **The stub's "not yet taken" switch** (MINOR 2), so that cases 4, 6 and 8 can fail.

MINORs 1 and 3–7 can ride along. The `Adopt` question goes to the human, and does not block the slice.

`03-policy-compiler.md (first slice, §1.1): REVISE`
