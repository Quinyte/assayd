# Design 03 (policy compiler) — critique of A56, scoped to the first slice (§1.1)

- **Reviewer**: independent adversarial critique, per `.claude/skills/critique-design/SKILL.md` and AGENTS.md. Cold read, with no author context. This is the sixth critique of the consolidated body, and the first to read §1.1.
- **Target**: `docs/designs/03-policy-compiler.md` at `origin/design-03-a56` `80502c0` (PR #20); `docs/decisions/0034-slice-auth-follows-current-spec-narrows-only-on-a-probe.md`, including Amendment 1; `docs/designs/reviews/03-a55-critique.md`.
- **Scope**: the human's decision G (ADR-0034 Amendment 1) limits approval to the first slice drawn in §1.1. `Narrow`, `Loosen`, E2, F2's group rules, D2, `Withdraw`, the other concerns and the candidate route are out of scope. A finding is raised against them only where the slice depends on them or they contradict it.
- **Method**:
  - Each in-scope finding of the fifth critique was checked in the body, at its source.
  - The slice was then walked as an implementer: every stage of `Create` for `<agent>-auth`, the probe, I1, `ForeignTrafficPolicy`, `Adopt`, and the conditions it sets.
  - That walk was checked against what the operator does today: `internal/controller/{httproute,conditions,agent_controller}.go`, `api/v1alpha1/agent_types.go`, `config/rbac/role.yaml`, `charts/assayd/templates/admission.yaml`, `cmd/operator/main.go`, `test/e2e/authz_test.go` and `docs/research/agentgateway-v1.4.1-spike.md` §2.6.
  - **One server-side dry-run was run on k3d `assayd-local`** (agentgateway v1.5.0, one gateway replica), and nothing was created. §3.4.4's golden policy (03:987–1011), with the slice's constant selector `{assayd.dev/api-keys: "true"}`, `mode: Strict` and the single-group expression, targeting an `HTTPRoute`, returned `created (server dry run)`. `kubectl explain` confirmed the `configMapSelector` text §3.4.4 quotes: `keyHash` only, and a duplicate key is "undefined".
  - No file other than this one was written.

## Verdict for the first slice: **REVISE — 0 BLOCKER, 3 MAJOR, 9 MINOR.**

A56 closes every in-scope finding of the fifth critique in the body:

- I1 shuts the fail-open through the persisted `oauth` default.
- Every `-auth` re-entry repeats the write.
- `Create` publishes only after an anonymous request gets `401`.
- H2 puts the one-replica qualification on a condition type that feeds neither `Ready` nor a page.

§1.1 is the clearest scoping section this design has had. The slice is close, but three things still make an implementer guess, or make the slice unbuildable:

- **An out-of-band delete of `<agent>-auth` has no specified outcome.** Read literally, it lands in the refused `Adopt`, which leaves the route open indefinitely (MAJOR 1).
- **The CEL §3.4.4 owes design 02 makes the slice's own `apikey` unwritable** (MAJOR 2).
- **The one premise every publication rests on is unmeasured**: that `401` answers before the missing-backend `500` on a prepared route (MAJOR 3).

---

## Closure against `reviews/03-a55-critique.md`, in scope

| Finding | This pass |
|---|---|
| **B1** — a served `-auth` deleted on a compile failure | **Closed** (03:490–499, 03:963, 03:983, the §5 row at 03:1145, and the §8.1 case at 03:1182). The sweep keeps the object "whatever `status.auth` says" (03:492). ADR-0034 Amendment 1(b) records I1. The residue is a deletion that no compile failure causes (MAJOR 1) |
| **M1** — re-entry skipped the write | **Closed** (03:411–413, 03:430, 03:605–612, the §8.1 case at 03:1184). The residue is whether the owed test can fail (MINOR 1) |
| **M2** — `Create` published on conditions | **Closed** (03:390, 03:565, 03:582–587, 03:1140). §6 now states the multi-replica window instead of denying it (03:1157). The residue is MAJOR 3 and MINORs 4–5 |
| **M3** — "1 of N" made every install un-`Ready` | **Closed by H2** (03:630–638, 03:239, 03:1139). `AuthNarrowingPartiallyVerified` survives only in the retraction at 03:638 and in A55's provenance at 03:1263 |
| **M5** — ADR-0034 over-attributed | **Closed**. Amendment 1(a) re-attributes D2's two clauses and the ordering clause in the Consequences. The decision text is untouched, as it should be |
| **MINOR 3** — owed flags | **Closed** (03:1195). `--gateway-serving-url` is now required whenever the compiler runs (03:580) |

---

## MAJOR

### M1 — Deleting a served `<agent>-auth`, or its route, out of band has no specified outcome. The literal rule is the refused `Adopt`, which leaves the Agent open with no page

**Where.**

- `Adopt`'s trigger is "a published serving route with **no operator-emitted `-auth` and no `status.apply`**" (03:567, 03:650).
- The slice records its transaction in `status.auth`, not `status.apply` (03:38, 03:395–421). So in the slice, `status.apply` is **always** absent.
- I1 re-asserts only "an out-of-band **mutation** of the kept object" (03:492).
- §5 covers "Emitted resource mutated out-of-band" (03:1152). Nothing covers deletion.

**What breaks.**

- Someone deletes `<agent>-auth` from a served Agent: an Argo or Helm prune, a `kubectl delete -l`, or anyone with policy delete in the run namespace, which nothing reserves yet (§6).
- On the next reconcile the route is published, no operator-emitted `-auth` exists, and `status.apply` is absent. That is `Adopt`: no write, `GovernanceSkipped=True`, reason `CompilerUpgradeUnsupported`, "recreate the Agent" (03:650–654).
- The route now serves with no key. `GovernanceSkipped` "is never an alert" (03:657, design 10 §5). The reason names a compiler upgrade that never happened, which is rule 8's loud-and-wrong.
- The other reading re-creates the policy from `status.auth` and trusts its conditions. That is the in-place first application on a serving route that §3.3.2 says no transaction may trust (03:547).
- The same gap exists for `<agent>-serving`. The shipped emitter re-creates a deleted route with `backendRefs` at once (`internal/controller/httproute.go:307–318`, exercised by the e2e). §1.1 owes a prepared, backend-less route only for "a new Agent" (03:65). Whether an existing `<agent>-auth` is enforced on the re-created route before it carries traffic is unmeasured.

**Fix.**

- Key `Adopt`'s trigger on **`status.auth` absent**. For `-auth`, "no compiler has ever recorded a transaction" means exactly that. Record `Adopt` in `status.auth`, or add the `status.apply` schema to §1.1's design-02 dependencies (03:57–61), which omit it today although `Adopt` writes it (03:653).
- State the missing case. A served Agent (`status.auth` present) whose `<agent>-auth` is missing re-creates it from `status.auth` and re-enters `Create` at `ApplyingPolicies`. That runs `Converging` and the anonymous `401` probe, with `PolicyApplyIncomplete` raised until the probe passes. Re-creating moves toward closed, and the probe keeps status honest: `Adopt` option (d)'s own argument (03:659–664).
- A re-created `<agent>-serving` for an Agent with a `Served` `-auth` goes through `PreparingRoute` and the same probe. Otherwise, state the window.
- Owe envtest cases, each with a mutation. Delete `<agent>-auth` from a served Agent: it must come back, and the Agent must not become `Adopt`. Delete the route: it must not carry `backendRefs` before the probe.

### M2 — The admission rule §3.4.4 owes design 02 makes the slice's `apikey` unwritable

**Where.**

- §1.1 says the slice owes design 02 "`apikey` in its enum. In the slice, `apikey` means the namespace-derived group, because `allowedGroups` is absent" (03:59). It also says the slice does not add `allowedGroups` (03:31).
- §3.4.4, which §1.1 cites for the mechanism, owes design 02 `allowedGroups` with `minItems: 1` and the CEL `has(self.allowedGroups) == (self.auth == 'apikey')`, with its message (03:953, 03:961).

**What breaks.** Suppose design 02 implements the rule §3.4.4 states. Then `auth: apikey` without `allowedGroups` is refused at admission, and in the slice `allowedGroups` never exists. So any Agent that writes an `expose.a2a` block, where `auth` is required and has no default (03:955), can choose only `none` (unauthenticated) or `oauth` (`AuthInputAbsent`). No Agent with an `expose` block can be authenticated in the slice. A56's call 2 sees the conflict only for the *later* rule (03:1249). The body still states the biconditional as what is owed.

**Fix.** In §3.4.4, state the slice's admission rules exactly:

- enum `none;oauth;apikey`, with no default and `has(self.auth)`;
- no `allowedGroups` field.

Replace the biconditional with `!has(self.allowedGroups) || self.auth == 'apikey'`, with a note on the stored shape it must keep admitting. Owe a golden file in which `apikey` with no `allowedGroups` compiles to the namespace group.

### M3 — The premise that gates every publication is stated as fact and has never been measured

**Where.** 03:582 says: "Once `<agent>-auth` is enforced, an anonymous request gets `401` before it reaches the missing backend … a route with no backend cannot produce a `401`, so the policy produced it." What has actually been measured:

- spike §2.6 (`docs/research/agentgateway-v1.4.1-spike.md:99–106`): a backend-less route accepts an attachment and answers `500`, with no auth policy on it;
- the e2e: `401` for an anonymous caller only on a route *with* backends (`test/e2e/authz_test.go:109–112`, `:259–266`).

03:587 owes the case.

**What breaks.** Suppose agentgateway answers a route with no backends before request policies run. Then `ProbingAfter` never sees a `401`, and every new Agent becomes `AuthEnforcementUnverified`, `Ready=False`, and pages after 5 minutes (03:1140, `10:50`). This fails closed, so it is not a BLOCKER. But it is the slice's **only** transaction (03:565), so the slice would ship an operator that publishes nothing. It is also rule 7: a present-tense sentence nothing has measured.

**Fix.** Run the `conformance-cluster` case **before** approval. It is one test on the k3d harness that already exists, and it needs no assayd code: a prepared route plus the golden policy, then an anonymous `GET`. Write the result and its latency into §3.3.3. If the answer is `500`, the probe needs a different shape before the slice can be approved.

---

## MINOR

1. **The stubbed probe can let case 3's mutation survive.** §8.1 runs "envtest, with the probe stubbed" (03:1180–1184). If the stub returns `401` unconditionally, "re-enter at `ProbingAfter`" reaches `Served` with no policy written, and the test passes: rule 1. **Fix**: specify the stub's oracle. It returns `401` only when the stored `<agent>-auth` equals the golden policy, and `500` otherwise.
2. **`PolicyCompileFailed` is not an owned condition** (`internal/controller/conditions.go:47–81`). Once raised, `merge()`'s default arm carries it forward forever (`conditions.go:116–119`). After an I1 owner reverts the edit, the Agent keeps `PolicyCompileFailed=True`. This is the bug class that file documents for `RevisionHashCollision`. **Fix**: §1.1 classifies it as owned, abnormal-true and not sticky, and a case is owed with the mutation that removes it from `ownedTypes`.
3. **`Create` has reasons for two of its failures and not the rest.** §1.1 lists `AuthEnforcementUnverified` and `ForeignTrafficPolicy` (03:44). A NACK on `Create` (03:436) has no named reason, and neither does `Accepted=False` or an empty ancestor list at the deadline (03:539). The `Narrow` NACK row (03:628) is out of the slice. **Fix**: name them.
4. **`200` and a misdirected `404` are unhandled.** 03:582 covers `500` and `404` only.
   - A `200` on `Create`'s probe means the route is serving with no key, for example after re-entry past `Publishing` with the policy lost. It must raise at once, not wait 4 minutes.
   - A `404` while §3.3.2's route tuple is `Accepted`/`ResolvedRefs` means `--gateway-serving-url` is wrong, not "the route is not attached" (03:1140). That is rule 8.

   **Fix**: state both.
5. **Attribution fails under a Gateway-level auth policy.** A Gateway- or ListenerSet-level `traffic.apiKeyAuthentication` also answers `401`, and detection deliberately skips it (03:306). `Create` then publishes on a probe `<agent>-auth` did not produce, and the Agent is reachable by any group's key. **Fix**: qualify 03:582's "so the policy produced it", and make case 7 a gate on the slice rather than one owed case among others (03:1187).
6. **§1.1 omits work its cited sections require.**
   - The startup refusal must also check the `AgentgatewayPolicy` CRD (03:236).
   - The `AgentgatewayPolicy` watch and the Warning-Event watch for the NACK (03:284).
   - `events get, list, watch` RBAC (03:286). Today only `create, patch` are granted: `config/rbac/role.yaml:22–28`, `agent_controller.go:229`.
   - The finalizer's order: the route goes before the policy (03:372). The reverse order opens the route for the gap.

   **Fix**: list all four in §1.1.
7. **`auth: none` has no `GovernanceSkipped` reason.** The clearing rule ties the `False` reasons to a probe (03:239), and `none` runs none (03:587). `Governed` and `AuthVerifiedOnOneReplica` both misdescribe an opted-out route. **Fix**: name its reason.
8. **A mode change during an unfinished `Create` is unspecified.** Take a derived `apikey` Agent, not yet published, whose owner moves to `none`. Only the finalizer or an applied change to `none`, which is out of the slice, may delete `<agent>-auth` (03:492). `AuthTransitionNotBuilt` covers served Agents only (03:497). The route could then publish under a leftover policy while its marker label says unauthenticated. **Fix**: an Agent that has never been served restarts `Create`, and may delete a `<agent>-auth` that never reached `Served`.
9. **The test list has gaps.**
   - `Adopt` is in the slice and has no owed test (03:1179–1188).
   - The golden policy (03:1016) is not in the slice list.
   - The e2e's key `ConfigMap` is labelled `"e2e"` (`test/e2e/authz_test.go:157`, `:189`), and the slice's constant is `"true"` (03:30). Replacing `assayd-e2e-authz` (03:1188) must relabel it. Say so.

   Latent in H2 as well: `Governed` rests on a declared replica count, and the planned default of `--gateway-replicas` is `1` (03:203). When that flag lands, an HA install that never sets it would claim `Governed`. Default the flag to unknown.

---

## A56's six calls made without the human

| Call | My view |
|---|---|
| 1. A served Agent's mode change is refused, with `AuthTransitionNotBuilt`, and the fix is to recreate it (03:497, 03:1248) | **Split. Needs the human.** Refusing `apikey → none` is defensible, because it fails closed. Refusing `none → apikey` keeps an Agent open that its owner asked to close, and the only exit is a delete, which is an outage and loses revision history. The design's own `Adopt` (d) argument (03:659–664) shows a safe path: write, then an anonymous `200 → 401` probe. The prior state was already open, and the path needs only `Create`'s probe and no keys. Offer it, or have the human accept "recreate" |
| 2. `apikey` without `allowedGroups` means the namespace group (03:1249) | **Defensible. The body contradicts it** (M2). No human needed once §3.4.4 is fixed |
| 3. I1's hold, and a mutated kept policy left as found when `status.auth` is absent (03:1250) | **Defensible.** The hold is correctly listed as owed to designs 02 and 07 (03:61, 03:65). "Left as found" occurs only after a status loss, and the message says so |
| 4. The operator refuses to start with the compiler on and `--gateway-serving-url` unset (03:580) | **Defensible**, and it matches the `HTTPRoute` refusal. A *wrong* URL is the remaining trap (MINOR 4). `cmd/operator/main.go` has no such flag today |
| 5. The anonymous probe proves authentication, not the group rule (03:584) | **Defensible, and stated honestly.** The group rule sits in the same object, and the slice's e2e pins `403` on the emitted policy (03:1188). The attribution caveat is MINOR 5. No human needed |
| 6. The reason names, and the 4-minute deadline applied to `Create` (03:1253) | **Defensible.** The names fit the vocabulary. The 4 minutes was derived from `Narrow`'s two propagations (03:626), and `Create` waits on one. That is harmless, because the deadline is a condition, and M3's measurement should replace it. No human needed |

## Verified sound

- **The emitted policy is admitted as specified.** k3d dry-run: `created (server dry run)`. The target is the `HTTPRoute` that ADR-0034's spike requires.
- **I1's trigger is real.** `expose.a2a.auth` is `Enum=none;oauth` with `default=oauth` (`api/v1alpha1/agent_types.go:381–384`). I1 closes the hole: the policy is kept, the route keeps serving, the minted revision is held, and the Agent reports it.
- **The condition types the slice sets exist**: `PolicyCompileFailed`, `PolicyApplyIncomplete` and `GovernanceSkipped` (`agent_types.go:436`, `:437`, `:444`). `GovernanceSkipped` is owned and sticky (`conditions.go:75`, `:102`), so H2's placement flips rather than lingers. Design 10 pages on `PolicyApplyIncomplete` and only tickets on `PolicyCompileFailed` (`10:50`), so I1 opens a ticket, not a page. That is right for a contained edit.
- **§1.1's dependency list is otherwise honest.**
  - Design 07's A6.10–A6.13 are uncritiqued (`07:3`).
  - No `agentgatewaypolicies` RBAC exists (`config/rbac/role.yaml:103–113` grants `httproutes` only).
  - The admission policies reserve namespace labels and gateway routes, not policies or the key label (`charts/assayd/templates/admission.yaml:25–59`, `:83–108`).
  - No `--gateway-serving-url` flag exists.
  - Every item is correctly marked owed, and `--gateway-replicas` is correctly not a prerequisite.
- **The slice's reach argument holds** (03:28–33). With the selector a constant and no `allowedGroups`, `Narrow`, `Loosen`, E2 and D2 cannot arise. F2's "a rollback changes no auth" is trivially true.

## The smallest set of changes to approvable

1. **Deletion of `<agent>-auth` and `<agent>-serving`** (M1): key `Adopt` on `status.auth`, re-create and re-probe a missing `-auth`, prepare and probe a re-created route, and owe both mutations.
2. **One admission rule for the slice in §3.4.4** (M2).
3. **Measure `401`-before-`500` on a prepared route**, and write the result in (M3).
4. **Pin the envtest stub's oracle**, and make `PolicyCompileFailed` owned (MINORs 1–2).

MINORs 3–9 can ride along.

`03-policy-compiler.md (first slice, §1.1): REVISE`
