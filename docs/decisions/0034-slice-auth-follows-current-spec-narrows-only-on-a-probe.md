# ADR-0034: The slice's gateway auth is one API-key policy per Agent that follows the current spec, and it narrows in place only on a probe

- **Status**: accepted · 2026-09-11 · **supersedes ADR-0033** (including its Amendment 1). Decided by the human in three rounds: on `reviews/03-a52-recritique.md` (target, input, `Adopt`), on `reviews/03-a53-critique.md` (A1, B2, C2), and on `reviews/03-a54-critique.md` (D2, E2, F2). Design 03 A55 writes the last three in. · **Amended 2026-09-11 (Amendment 1)**: two clauses of D2 are re-attributed to design 03's author, and the human's G, H2 and I1 are recorded. · **Amended 2026-09-12 (Amendment 2)**: the human's J2 is recorded. · **Amended 2026-09-12 (Amendment 3)**: the human's decision that `Adopt` stays refused is recorded, with its reason, consent. · **Amended 2026-09-12 (Amendment 4)**: the human approved design 03's first slice and decided K2.
- **Context**: ADR-0033 was titled "the compiler refuses to tighten a live route". Its Amendment 1 made the policy tighten in place, so the title stated a reversed decision. AGENTS.md says a reversed decision gets a superseding ADR, so this one records the whole current set in one place. The fourth critique of design 03 found three more defects that were decisions and not corrections:
  - a key-source change cut every old-source key fleet-wide at once;
  - a disjoint group change passed through a window in which every caller was refused, and the design called that window bounded;
  - a rollback to a wider revision re-admitted a group that an administrator had removed.
- **Decision**:
  - **Target** (spike): `-auth` is **one `AgentgatewayPolicy` per Agent**, `<agent>-auth`, targeting the per-Agent serving `HTTPRoute`. agentgateway's CRD refuses a `traffic` policy on a Service, at 1.5.0 and at the vendored 1.4.1 (`research/agentgateway-service-target-spike-2026-09.md`).
  - **Input**: the slice's one identity mechanism is **API keys**. `traffic.apiKeyAuthentication` reads a chart-level set of `sha256:` hashes, and a CEL `authorization` rule admits the Agent's groups.
  - **`Adopt` stays refused.** A first policy is never written onto a route that is already serving, and the Agent says so.
  - **A1 — narrowing is applied in place and trusted only on a probe** (design 03 `Narrow`). A key in a removed group must go from `200` to `403` while a kept key still gets the Agent's own card.
  - **B2** — `spec.budget` is refused at admission while nothing enforces it.
  - **C2** — `expose.a2a.auth` is required, with no default.
  - **D2 — a change to the chart's key source is refused while the old source is live.** Every Agent already served under the old source keeps it and reports `PolicyInputDrifted`, reason `ApiKeySourceChangeRefused`, telling the administrator to copy keys to the new source. The administrator confirms by setting `gateway.apiKeys.acceptedSelector` to the new selector exactly. Only then does each Agent move, under the probe. An Agent that has never been served takes the new source at once.
  - **E2 — a disjoint group change is make-before-break.** `[a] → [b]` runs as two transactions: `Loosen` to `[a, b]`, then `Narrow` to `[b]`. The policy never admits nobody.
  - **F2 — `-auth` follows the Agent's current spec, never a revision.** A rollback restores a revision's workload material and nothing else. It never re-admits a group the current spec removed, and it runs no probe.
- **Consequences**:
  - **Patched in place, under a probe.** ADR-0033's "contain, don't patch" does not hold for `-auth`. The operator mints short-lived probe keys, so it is a principal-minting identity for each probe window.
  - **Keys are still shared secrets with manual rotation.** A key-source move now costs a manual confirmation step as well.
  - **Owed to design 02.** It owes the admitted-groups field, the CEL rules and a `status.auth` record. Today `expose` is on design 02's behaviour surface, so an edit to `auth` or `allowedGroups` also mints a revision. Design 02 must either keep that, in which case design 03 orders the auth transactions around the edit's weight shift, or move both fields to the policy surface, which is a revision-hash migration. F2 does not depend on which it chooses.
  - **ADR-0031 Amendment 1 is applied, not edited.** Its "never recompute the target from current spec" governs a revision's retained material. `-auth` belongs to no revision, and F2 is that amendment's "recheck current security constraints" for groups.
  - **Revisit at the next agentgateway upgrade.** The Service-target refusal is a CEL rule in the vendored CRD. If an upgrade lifts it, per-revision auth becomes possible and this ADR is revisited.
  - **Nothing here is implemented.** No policy is emitted, and neither chart value nor probe exists.

## Amendment 1 (2026-09-11, from `reviews/03-a55-critique.md` MAJOR 5, and the human's decisions on that critique)

**(a) Two clauses in D2 were the author's calls, not the human's.** The Status line says this ADR was "decided by the human in three rounds". D2's last two sentences were not decided by the human:

- "Only then does each Agent move, under the probe."
- "An Agent that has never been served takes the new source at once."

Design 03 A55 listed both as calls it made without the human. They are **author's calls, not decided by the human**, and they stay open until D2 becomes reachable. The same holds for one Consequences clause: "design 03 orders the auth transactions around the edit's weight shift". That ordering is A55's call 3, and its stated reason does not hold (design 03 §3.2; the critique's M4, open). The decision text above is not edited.

**(b) The human decided three more things on 2026-09-11, on the fifth critique. Design 03 A56 writes them in.**

- **G — only the first slice can be approved for implementation.** The slice is one per-Agent `<agent>-auth`, emitted by `Create`, with a compiled-in key source and an admitted set that cannot change (design 03 §1.1). So D2, E2, A1's group narrowing and F2's group semantics are specified, not approved. Nothing is approved until the human approves it after a passing critique.
- **H2 — a probe that passes on the replica it reached means `Ready=True`**, with the informational reason `AuthVerifiedOnOneReplica` on `GovernanceSkipped=False`. A probe that fails means `Ready=False` and a page. A probed Agent is never worse off than an unprobed one.
- **I1 — a served Agent whose `-auth` input becomes uncompilable keeps its last good `-auth`.** The sweep never deletes it on a compile failure. The Agent reports `PolicyCompileFailed`, and its route keeps serving. Today's CRD defaults `auth` to `oauth`, so adding `expose.a2a` to a served Agent must not open it.

Nothing in this amendment is implemented.

## Amendment 2 (2026-09-12, the human's decision on `reviews/03-a56-critique.md`)

**J2 — a served Agent switched from `auth: none` to `auth: apikey` is locked in place, and trusted only after a probe.** The operator writes `<agent>-auth` onto the route that is already serving. An anonymous request must then go from `200` to `401`. On the `401` the Agent is governed, and `GovernanceSkipped` clears. If the request still gets `200` at the deadline, the Agent reports `PolicyApplyIncomplete` with a named reason, and pages. Status never claims the route is locked. This is safe because the route was already open: a failed apply leaves it exactly as it was, and the probe keeps status honest. It is the probe-verified pattern of A1. It replaces design 03 A56's call 1 in that direction, and brings the `none → apikey` transition into the first slice. Design 03 A57 writes it in as the transaction `Lock`.

- **The reverse stays refused.** `apikey → none` would open a locked route. It is refused with reason `AuthTransitionNotBuilt`, and fails closed.
- **How J2 sits beside "a first policy is never written onto a route that is already serving".** That clause of the Decision still governs `Adopt`: the operator finds a serving route with no policy, which it did not create and which no spec edit asked it to change, for example after a compiler upgrade. J2 is the explicit exception, for a transition the Agent's owner requested. The two use the same mechanism: write, converge, then an anonymous `200 → 401`. They differ only in trigger, so the mechanism is no longer the reason `Adopt` stays refused. Whether `Adopt` should follow is **not decided here**. Design 03 A57 flags it for its next critique and for the human.
- **Not the human's.** Design 03 A57 also sends a served Agent's deleted `<agent>-auth` through the same transaction (the sixth critique's M1). That is an author's call, recorded here so that this ADR is not read as deciding it.

Nothing in this amendment is implemented.

## Amendment 3 (2026-09-12, the human's decision on `reviews/03-a57-critique.md`)

**The human decided on 2026-09-12 that `Adopt` stays refused.** The seventh critique asked whether `Lock` and `Adopt`, which are one mechanism, should be one decision (`reviews/03-a57-critique.md`, "A57's author's calls"). They are not. The Decision above refuses `Adopt`: a first policy is never written onto a route that is already serving. That stands. Its reason is restated here, because the reason recorded beside it no longer holds.

- **The old reason is gone.** It was that the obvious patch trusted conditions, which a NACK can make lie (design 03 §3.3.2). Amendment 2 already says so. J2's `Lock` writes a first policy onto a serving route and trusts it only after an anonymous `200` → `401`, and `Adopt` could use the same probe.
- **The reason now is consent.** `Lock` closes a route that its owner asked to close, by editing `auth` from `none` to `apikey`, or one the compiler had recorded as closed and that was deleted out of band. `Adopt` would close, at a compiler upgrade, a route that nobody asked to change. Every caller without a key would start getting `401`, and nothing proves that a key exists in the Agent's group. So un-refusing `Adopt` trades an open route for an outage caused by an upgrade. That is an availability-against-security choice. The human's decision keeps the open route, reported as ungoverned by `GovernanceSkipped=CompilerUpgradeUnsupported`, and does not cause the outage.
- **Who it reaches.** Today, only the e2e harness's routes, because `gateway.enabled` ships `false`.

**J2's consequence, stated.** Once a `Lock` reaches `Served`, it is one-way in the first slice, because `apikey → none` is refused. So a lock that breaks callers who hold no key can be undone only by recreating the Agent. This restates what J2 and its refused reverse already imply, and changes neither. The seventh critique named it as the human's to weigh. Design 03 A58 states it in the design's §1.1, and the `AuthLockPending` message says it before the lock lands.

**Not the human's.** Design 03 A58 made two calls beside this decision. They are recorded here so that this ADR is not read as deciding them.

- **Deleting an `Adopt`ed Agent's route does not lock it.** The route is re-created as before, unauthenticated, and the refusal holds (design 03 §3.3.3). This follows from the decision above, but the human did not take it.
- **Abandonment, beside I1.** Design 03 A58 abandons a `Create` or J2 `Lock` that has not reached `Served` when the desired mode changes. It deletes the policy that transaction wrote, which `status.auth` never recorded as served. I1 says a served Agent keeps its last good `-auth`, and that the sweep never deletes it on a compile failure. The two do not meet. The deletion is keyed on the unfinished transaction's own record of its write. It never touches a policy `status.auth` records as served, or a policy `status.auth` does not mention. So a `Lock` abandoned by an edit to `oauth` returns to the recorded `mode: none`, which is that Agent's last good `-auth`.

Nothing in this amendment is implemented.

## Amendment 4 (2026-09-12, the human's approval of design 03's first slice, and K2)

**The human approved design 03's first slice for implementation on 2026-09-12.** Approval came after the thirteenth critique returned PASS on it (`reviews/03-a63-critique.md`: 0 BLOCKER, 0 MAJOR, 3 MINOR), and after A64 folded in those minors. Only §1.1's slice is approved. The rest of design 03 is not.

**K2: an owner's edit to `apikey` is consent, and ends `Adopt`'s refusal.** The owner of an `Adopt`ed Agent who edits `auth: none` → `apikey` asked for the route to be closed. Amendment 3's reason for refusing `Adopt`, that nobody asked, does not cover them. So the edit takes the Agent through J2's `Lock`, and it is trusted only after an anonymous `200` → `401`. `Adopt` otherwise stays refused, as Amendment 3 decided.

**Not the human's.** Design 03 A65 reads K2 as covering an edit observed after the refusal, from whatever mode the refusal recorded. An Agent whose spec already said `apikey` when it was first refused stays refused. That reading is the author's, recorded here so that this ADR is not read as deciding it.

Nothing in this amendment is implemented.
