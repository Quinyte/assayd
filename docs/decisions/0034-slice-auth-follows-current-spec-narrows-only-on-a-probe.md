# ADR-0034: The slice's gateway auth is one API-key policy per Agent that follows the current spec, and it narrows in place only on a probe

- **Status**: accepted · 2026-09-11 · **supersedes ADR-0033** (including its Amendment 1). Decided by the human in three rounds: on `reviews/03-a52-recritique.md` (target, input, `Adopt`), on `reviews/03-a53-critique.md` (A1, B2, C2), and on `reviews/03-a54-critique.md` (D2, E2, F2). Design 03 A55 writes the last three in. · **Amended 2026-09-11 (Amendment 1)**: two clauses of D2 are re-attributed to design 03's author, and the human's G, H2 and I1 are recorded. · **Amended 2026-09-12 (Amendment 2)**: the human's J2 is recorded. · **Amended 2026-09-12 (Amendment 3)**: the human's decision that `Adopt` stays refused is recorded, with its reason, consent. · **Amended 2026-09-12 (Amendment 4)**: the human approved design 03's first slice and decided K2. · **Amended 2026-09-13 (Amendment 5)**: the human decided L1 for the Gateway-level auth gap, accepted its two costs, and decided W1. · **Amended 2026-09-15 (Amendment 6)**: the human decided that the `Lock` of a missing policy re-points its route at `status.activeRevision`, and accepted one cost. Design 03 A77 writes it in, and the placement, the probe precondition that fixes a false credit open today in J2's and K2's `Lock`s, the digest condition and the terminal message are A77's calls of 2026-09-16, recorded there under "Not the human's". Nothing implements any of it.
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

**Not the human's.** Design 03 reads K2 as covering any move to `apikey` that a reconcile observes after the refusal (A65, made to track by A66). An Agent whose spec already said `apikey` when it was first refused stays refused until it is observed under another mode and then under `apikey` again. That reading is the author's, recorded here so that this ADR is not read as deciding it.

Nothing in this amendment is implemented.

## Amendment 5 (2026-09-13, the human's decisions on the Gateway-level auth gap: L1, its costs, and W1)

**L1, detect and hold. The human decided it on 2026-09-13.** Design 03 A74 measured the gap on agentgateway 1.5.0. A `traffic` policy that targets the assayd Gateway and carries API-key authentication answers an anonymous request on an Agent's route with `401` before `<agent>-auth` exists. Neither `Create`'s probe nor `Lock`'s card-digest attribution can tell that `401` from `<agent>-auth`'s. So either could record `Served` under `apikey` while `<agent>-auth` had not taken, or never would.

- **Decision.** The operator reads the assayd Gateway and the policies on it. While a policy stands that targets the Gateway on a listener that can take the Agent's traffic, and that could answer an anonymous request with a status of its choosing, neither `Create` nor any `Lock` credits a `401` or records `Served`. The transaction holds, and the Agent says so loudly, naming the policy. Design 03 A75 gives the rule and its field table.
- **What it costs. The human accepted both costs on 2026-09-13.**
  - **Re-creations of served Agents hold too.** A served Agent whose route is deleted out of band stays unpublished, so it is off the air. A served Agent whose policy is deleted keeps its missing-policy `Lock` open, and pages. Both last until the Gateway-level policy is removed.
  - **ListenerSets.** Whenever the Gateway's `spec.allowedListeners.namespaces.from` is anything but `None`, which is `All`, `Same` or `Selector`, including a `Selector` that matches nothing, every new Agent holds, whether or not any ListenerSet or policy exists. A ListenerSet's listener can capture an Agent's traffic, and the slice does not inspect ListenerSets. A Gateway with no `allowedListeners` admits none. The chart ships no Gateway, so that default is Gateway API's, not the chart's.
  - **Held Agents page.** Every held Agent raises `PolicyApplyIncomplete`, and design 10 pages on it after 5 minutes, until an administrator removes the policy, scopes it off the Gateway, or sets `from: None`. An install that keeps such a policy on purpose holds every new Agent unpublished, and every J2 or K2 lock unproved.
  - No opt-out is built. Building one later is a new decision.
- **Rejected.** L2, detect and report while still recording `Served`, weakens what `Served` proves. L3, document the assumption, leaves status wrong.

**W1, a served Agent whose route a Gateway-level rule can widen stops reading `Governed`. The human decided it on 2026-09-13, on design 03 A75's measurement.** A Gateway-level `Allow` rule was measured admitting, on a route, a group its `<agent>-auth` refuses: `authorization` rules merge across attachment points. So, for an Agent already `Served`:

- its route keeps serving and is never withdrawn;
- a counting Gateway-level policy stays a note on `GovernanceSkipped`'s message only when it sets authentication alone: every counting field it sets is `apiKeyAuthentication`, `basicAuthentication` or `jwtAuthentication`, its `phase` is unset or `PostRouting`, and it does not set `Override`. The route's authentication replaces it: measured for `apiKeyAuthentication`, which is A74 case 7's shape, and unmeasured for `basicAuthentication` and `jwtAuthentication`;
- any other counting Gateway-level policy fails closed. The Agent reads `GovernanceSkipped=True` and `PolicyApplyIncomplete=True`, both reason `GatewayAuthPolicy`, and it pages, as intended. Both messages name the policy, and say it cannot be ruled out that the policy widens or bypasses the route's authentication, until it is removed, or rescoped off the Gateway or off its serving listener. For `authorization` and `Override` the widening is measured; for the rest, a transformation, a header modifier or an external processor that could hand the route a credential, it is unmeasured, and the message says so. `Ready` follows design 03 §3.3.1's aggregation for `PolicyApplyIncomplete` on a served Agent, which withholds it: `Degraded`.

The merge is asserted, not assumed: `TestSliceAGatewayLevelAllowRuleWidensTheRoute` fails if a later agentgateway stops merging, and then this amendment is revisited.

**Not the human's.** Design 03 A75 made these calls. They are recorded here so that this ADR is not read as deciding them:

- a held J2 or K2 `Lock` withholds `Ready` at once, so that design 03 §3.3.1's aggregation holds for it;
- which `spec` fields count, in A75's table, with every field the table does not call harmless counting;
- a Gateway read, or a policy list, that fails holds a transaction, like any other cause. On a served Agent a failed list raises `GovernanceSkipped=True`, reason `GatewayAuthPolicy`, and does not page; a failed `get` whose list succeeded is a note. The human asked for a recommendation here and took the reviewer's.

Design 03 A75 implements this amendment (`internal/controller/authabove.go`, `authtxn.go`). The opt-out is not built.

## Amendment 6 (2026-09-15, the human's decision on the missing-policy `Lock`'s route)

**The `Lock` of a missing policy re-points its route at `status.activeRevision`. The human decided that, and only that, on 2026-09-15.** Everything else below is design 03 A77's, made on 2026-09-16 in answering seven critiques, and is recorded under "Not the human's" so that a later reader does not cite it as decided.

Amendment 4's approved slice gave J2's and K2's `Lock`s a route that follows promotion and gave the third `Lock` — the one that re-creates an `<agent>-auth` deleted out of band — a route it never writes. `cardAttributes` attributes a `401` on the card of the revision the route's `backendRef` names, so a served Agent whose route names a revision with no recorded card can never finish that `Lock`: it holds `PolicyApplyIncomplete` and `GovernanceSkipped` under `AuthPolicyMissing`, `Ready=False`, phase `Degraded`, and design 10 pages for as long as the Agent exists, while `status.activeRevision` advances past a revision receiving no traffic. Design 16's first slice documents the state and its exits, and its Q13 accepted it there on 2026-09-14, directing the fix here.

- **The decision.** The `Lock` of a missing policy re-asserts `<agent>-serving` published on `status.activeRevision`, as J2's and K2's already do, so that a promotion during it moves traffic and the attribution reads a revision the operator has fetched a card from. Design 03 A77 gives the rule.
- **The cost the human accepted on 2026-09-15.** A revision that never carried traffic through the route becomes reachable on it while the `Lock` is short of `Served`. The route is open at that moment and nothing here claims otherwise: §3.3.2 proves control-plane convergence, never dataplane enforcement, and a NACK retains the previous configuration while every condition reports converged.
- **What the human did not decide, and is not asked to.** The wedge is narrowed, not removed. Where no card digest is recorded for the revision the route names **and** none for `status.activeRevision`, the `Lock` still never finishes, with the same conditions, the same page, and the same two exits — an administrator deleting `<agent>-serving`, an outage until `Create` republishes and unbounded while a Gateway-level or foreign policy holds that `Create`; or reverting the spec to the revision the route names, which does nothing for a card that never validates.

**Not the human's.** Design 03 A77 made these calls, on 2026-09-16, answering `reviews/03-a77-critique.md`, `reviews/03-a77-recritique.md`, `reviews/03-a77-critique3.md`, `reviews/03-a77-critique4.md`, `reviews/03-a77-critique5.md`, `reviews/03-a77-critique6.md` and `reviews/03-a77-critique7.md`. They are recorded here because they change what the decision above means, and they are **not** the human's accepted costs:

- **Where the re-point goes**: `ProbingAfter` only, after `<agent>-auth` is read equal to the target, after the NACK read, and after A75's Gateway read and the foreign-policy check — not `Converging`, which the first draft chose on a safety argument that was false there (the tuple has not held, and "enforcing" is the inference §3.3.2 forbids). *(First critique, BLOCKER 1; the ordering against A75's read, second critique, MAJOR 1.)*
- **A precondition of the probe, for every `Lock`**: no `Lock` takes a probe answer, or credits a `401`, until the assayd Gateway reports `Accepted` and `ResolvedRefs` on `<agent>-serving` at the route's current generation; until it does, the pass ends after `ProbingAfter`'s own reads — the policy, a fresh NACK, the Gateway and its policies — and the transaction **stays at `ProbingAfter`**, so A75's hold keeps being raised and design 10's clock is not restarted. **This fixes a defect in behaviour that already ships.** In J2's and K2's `Lock`s the route is re-asserted on `status.activeRevision` and probed in the same pass, so a promotion mid-transaction lets the previous revision's own `401` be credited to `<agent>-auth` against the new revision's card digest. A reproduction against `main` `3f37c25` measured it for both, and could not drive the missing-policy `Lock` there, which writes no route; it is recorded in `docs/research/auth-lock-false-credit-2026-09.md`. A61 does not cover it: its clearing is gated on `beforeRevision` and touches the observed branch only. The first draft keyed this on "the `backendRef` changed in this pass", which a lost or conflicted status write or a restart re-opens; the first two drafts also rewound the transaction to `Converging`, which dropped a standing Gateway-level hold on every pass after the gated one. What the gate proves is control-plane only, so it narrows the false-credit window to the proxy's lag behind the controller's report and leaves the cached route read beside it — both stated in design 03 §3.3.3 rather than claimed away. *(First critique, MAJOR 2; the ordering against A75's read, second critique, MAJOR 1; the rewind and the gate's residuals, third critique, MAJOR 1 and MAJOR 3.)*
- **The re-point is conditional** on a recorded card digest for `status.activeRevision`. Without it, a route on a carded r1 beside an uncarded `status.activeRevision` would move from crediting to paging for ever — the inverse of the defect. The price is that such an Agent keeps a route naming a revision that receives nothing. *(First critique, MAJOR 3.)*
- **The terminal message.** Where the state above stands, the message says it is terminal until a card records or an administrator acts, names both exits, and says which the state it reports admits; design 03's message does none of that today. *(First critique, MAJOR 4.)*
- **Narrowing a `Lock`'s attribution** to the revision the route's single `backendRef` names, so that the gate, the re-point and the attribution all read one route object; a route not carrying exactly one is unattributable, and passes the gate while the attribution refuses it.
- **Stating the write order** — the policy is written, or read equal to the target, before the `backendRef` moves — in design 03 §3.3.1, where it was a code comment only.
- **The shape of §8.1 case 18**, including the two sub-cases that pin the shipped J2 and K2 defect, the one that pins the digest condition, and the one that pins the gate against A75's Gateway read.
- **A liveness cost the human was not asked to take**: the gate turns a false credit into a `Lock` that cannot finish while the route keeps moving. The deadline pages rather than spinning, and a route that stops moving converges.

**Nothing implements this amendment.** Design 03 A77 specifies it and changes no code; §8.1 case 18 is owed and unwritten.
