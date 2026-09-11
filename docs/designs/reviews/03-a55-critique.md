# Design 03 (policy compiler) — critique of A55, the response to the A54 critique

- **Reviewer**: independent adversarial critique, per `.claude/skills/critique-design/SKILL.md` and AGENTS.md. Cold read; no author context. Fifth critique of the consolidated body.
- **Target**: `docs/designs/03-policy-compiler.md` at `origin/design-03-a55` `2e139e6` (PR #19). §1–§10 are the authoritative body; §11 A55 was read only to learn what it claims. Also `docs/decisions/0034-slice-auth-follows-current-spec-narrows-only-on-a-probe.md`, `docs/decisions/0033-slice-auth-api-keys-per-agent-no-live-tightening.md` (Status line), and `docs/research/agentgateway-service-target-spike-2026-09.md`.
- **Method**: each of the 12 findings in `reviews/03-a54-critique.md` was checked **in the body**, at its source. The new mechanisms (D2's accepted selector, E2's sequencing, F2's current-spec rule, the `written` re-entry rule, `--gateway-serving-url`, the "1 of N" rule, `status.auth`) were then read twice: as an implementer building the first slice, meaning the per-Agent `<agent>-auth` beside the `<agent>-serving` route `internal/controller/httproute.go` already emits, and as an adversary looking for a fail-open or a wedge. Repo facts were read in `internal/controller/{httproute,conditions,release}.go`, `api/v1alpha1/agent_types.go`, `internal/revision/golden_test.go`, `cmd/operator/main.go`, `charts/assayd/values.yaml`, `config/rbac/role.yaml`, `hack/e2e.sh` and `test/e2e/authz_test.go`. The vendored `test/conformance/testdata/agentgateway-crds-1.4.1.tgz` was extracted and read. **Two server-side dry-runs were run on k3d `assayd-local` (agentgateway 1.5.0), and nothing was created**:
  - §3.4.4's worked policy, `mode: Strict`, two-group `||` expression, targeting `HTTPRoute`: `created (server dry run)`;
  - the same `traffic` block targeting `kind: Service`: refused, `the 'traffic' field can only target a Gateway, ListenerSet, GRPCRoute, HTTPRoute, or InferencePool`.

  `kubectl explain` on the installed CRD confirmed `apiKeyAuthentication.mode` is `Optional | Permissive | Strict`. No file other than this one was written.

## Verdict: **REVISE — 1 BLOCKER, 5 MAJOR, 5 MINOR.**

A55 closes all twelve findings of the fourth critique in the body, and closes most of them well. `status.auth` is the right shape, the probe's address is now real, E2 removes the all-refused window, and F2 removes the rollback re-admission. The writing is clearer than any previous round.

**The first slice is not approvable yet, and the reason is not `Narrow`.** The slice's most ordinary path is `Create` of `<agent>-auth` on a route the operator already emits. Two defects sit on it:

- a served Agent whose `-auth` input becomes uncompilable has no specified fate, and the natural implementation deletes its policy (BLOCKER 1);
- `Create` publishes on the conditions that §3.3.2 measured lying, while §6 claims the create path has no unauthenticated window (MAJOR 2).

The later transactions (group `Narrow`/`Loosen`, E2, F2 rollback, the D2 migration) carry two more MAJORs, and they cannot arise until design 02 lands `apikey` and `allowedGroups`. The boundary is drawn at the end.

---

## Closure against `reviews/03-a54-critique.md`

| Finding | This pass |
|---|---|
| **B1** — `Narrow` wedged on re-entry after the write | **Closed in the body** (03:336, 03:360, 03:378, 03:535–544). An ordinary edit no longer sends the machine back to `ProbingBefore`. **But the fix opens a new wedge.** `written` is set *before* the write, and re-entry with an unchanged target goes to `ProbingAfter`, which skips the write (MAJOR 1). Re-entry to a non-narrowing target is also unspecified (MINOR 2) |
| **M1** — the shift-waits contract had no owner | **Closed as owed** (03:301, 03:1107–1108). The envtest case is owed at 03:1109. In the slice, `none → apikey` mints a revision (`expose` is behaviour surface, `02:732`), so the slice needs this hold. Design 03 §3.3 is enough for an implementer to build it |
| **M2** — the probe had no address; N had no producer | **Address closed** (03:517): a serving-listener flag distinct from `--gateway-url`, which matches `hack/e2e.sh:266–267, :283–284, :308–320`. **The replica rule is new, and wrong in effect** (MAJOR 3) |
| **M3** — the applied `-auth` had nowhere to live | **Closed** (03:344–369, 03:605). `status.revisions[].appliedDigest` is gone for `-auth`. Residue: MINOR 4 (`afterShift` names no revision) and MINOR 5 (status as the only record of the source in force) |
| **M4** — "bounded" | **Closed by E2** (03:291, 03:307, 03:525). "bounded by the two" survives only in the retraction at 03:307. `false` is never emitted for `-auth` |
| **M5** — a rollback re-admitted a removed group | **Closed by F2** (03:287). The reading of ADR-0031 Amendment 1 (`0031:19`) is now coherent. A residue F2 does not state is MAJOR 4 |
| MINOR 1 — stale "no in-place tightening" | **Closed** (03:484, 03:496, 03:1081) |
| MINOR 2 — 03:1067 false at HEAD | **Closed**, marked in place (03:1190) |
| MINOR 3 — `false` golden file | **Dissolved** (03:1106) |
| MINOR 4 — foreign detection route-level only | **Closed** (03:253–255; §8.1 case 7 at 03:1100) |
| MINOR 5 — two `Narrow` edges | **Closed for the marker** (03:448). **Not closed for the key source**: 03:909 says "An Agent that is `PolicyCompileFailed` has never been served", and BLOCKER 1 shows one that has |
| MINOR 6 — ADR-0033 title | **Closed**: ADR-0034 supersedes it, and `0033:3` is marked `superseded-by`. But ADR-0034 over-attributes (MAJOR 5) |

---

## BLOCKER

### B1 — A served Agent whose `-auth` input becomes uncompilable has no specified fate, and the design's own sweep deletes its policy

**Where.** 03:889 and the §5 row at 03:1069 say an Agent whose `auth` is `oauth` is `PolicyCompileFailed`, reason `AuthInputAbsent`, "and its serving route is not published". 03:909 says such an Agent "has never been served". 03:436 says a compile failure means "the affected routes are not emitted". 03:231 runs "a label-and-name sweep every reconcile".

**What breaks.** Take a four-line Agent in the slice. It has no `expose` block, and it is served under the derived `<agent>-auth`. Its owner adds `expose: {a2a: {visibility: org}}`. The CRD still carries `+kubebuilder:default=oauth` (`api/v1alpha1/agent_types.go:381–382`), and C2's removal of that default is owed to design 02 and not implemented. So the API server persists `auth: oauth`, and the `-auth` compile fails. Now:

- The route is published, so "not published" describes nothing. The body does not say whether the route is withdrawn, or kept serving under the last policy, or kept serving under no policy.
- `<agent>-auth` is no longer in the desired set. It passes the name-and-label check, so the 03:231 sweep deletes it. The route keeps its `backendRefs`, because nothing says to remove them. **The Agent is now reachable with no key.** It reports `PolicyCompileFailed` and `Ready=False`, and `GovernanceSkipped` has no stated value.
- The same edit mints a candidate. Nothing holds its weight shift, because no `Narrow` is running.
- `auth: oauth` written on purpose, and any later concern whose input disappears, reach the same state.

This is the fail-open the whole of §3.3.1 exists to prevent. It arrives through the most ordinary edit on the CRD as it ships today, on the slice's own path, and the most natural implementation of the design's own rules produces it.

**Fix.** One rule, stated in §3.3.1 and §3.4.4:

- A compile failure of the per-Agent `-auth` never deletes or rewrites a served `<agent>-auth`. The policy stays at the set `status.auth` records as `Served`, and the sweep excludes it while `status.auth` names it.
- The route keeps its `backendRefs`. It is not withdrawn: the policy it serves under is still enforced, so withdrawing would be an outage for a typo.
- A weight shift minted by the same edit is held, as for `Narrow`.
- `PolicyCompileFailed` names the field and says the previous policy is still enforced.
- Correct 03:909.
- Owe an envtest case, with a mutation that lets the sweep delete `<agent>-auth` on a compile failure.

---

## MAJOR

### M1 — The `written` rule re-enters at `ProbingAfter` without writing, so a crash in the window it was designed for wedges `Narrow`

**Where.** 03:530: the status update entering `ApplyingPolicies` sets `written` "**before** the policy is written. So a crash between the two reads as written." 03:542: "re-enters at `ApplyingPolicies` if the target changed, and at `ProbingAfter` if it did not."

**What breaks.** The operator crashes after the status update and before the write. That can be an OOM, a lost leader lease, or a `helm upgrade` of the operator during a rollout. The restarted operator sees `written`, and the target has not changed, so it enters `ProbingAfter`. The narrowed policy was never written, so the removed probe keeps getting `200`. The deadline is a condition, not a timeout (03:382), so the Agent sits at `AuthNarrowingUnverified`, `Ready=False` and `Degraded`, with the weight shift held. This is the fourth critique's BLOCKER 1, reached through its own fix. Only a re-assert that runs outside the stage machine would heal it, and the stage table does not provide one.

**Fix.** Once `written` is set, re-enter at `ApplyingPolicies` in every case. The write is create/update over owned fields (03:259), so repeating it is a no-op when it already landed. `ProbingBefore` stays skipped. Add a mutation to the envtest list at 03:1109: crash between the status update and the write, and the transaction must still reach `Served`.

### M2 — `Create` publishes `<agent>-auth` on conditions alone, and §6 claims the create path has no unauthenticated window

**Where.** The `Create` row at 03:502 (`… → Converging → Publishing`, no probe). §6 at 03:1081: fail-closed ordering "removes the unauthenticated/unbudgeted windows **on the create path**". Against both:

- 03:482 measured that a NACK'd policy "retains the old config; it does not fail closed";
- 03:188 limits `GovernanceSkipped=False` to "control-plane convergence … not dataplane enforcement";
- `test/e2e/authz_test.go:255–258` exists because "Attached is a statement by the control plane about configuration, not about the proxy that serves the request".

**What breaks.** On `Create` the old config on a proxy is *no policy*. If a replica NACKs `<agent>-auth`, or has not yet taken it when `Publishing` attaches `backendRefs`, that replica serves the Agent with no key. A NACK then returns the machine to `Converging` (03:384), but the `backendRefs` are already attached, so the route stays published. §6's sentence is a present-tense guarantee that the design's own measurements contradict (rule 7). This is the slice's central path.

**Fix.** The probe that already exists closes this cheaply. A prepared route with no `backendRefs` answers `500` (03:373, spike §2.6). Before `Publishing`, an anonymous `GET` of the card path on the prepared route must return `401`. Neither a backend nor a 500-answering route can produce a `401`, so the answer is attributable, and it uses `--gateway-serving-url`, which the slice needs anyway. The replica limit applies as for `Narrow`. Owe a `conformance-cluster` case asserting that auth answers before the missing-backend `500`. If the human declines the probe, strike "on the create path" from §6 and state the window.

### M3 — The "1 of N" rule makes every narrowed Agent permanently un-`Ready` on every install, and it inverts the evidence ordering against `Create`

**Where.** 03:563–564, 03:1065. The rule is `gatewayReplicas` more than 1 **or unknown** → `Served`, but `PolicyApplyIncomplete` stays and `Ready=False`, "until a later `Served` on one replica". 03:152 and 03:564 say `gatewayReplicas` has no producer, so every count is unknown. `--gateway-replicas` is missing from the owed lists at 03:1107–1108.

**What breaks.**

- **Every install is "unknown" today**, the one-replica e2e included. So every `Narrow`, the slice's `none → apikey` among them, ends with the Agent `Ready=False`, and design 10 pages after 5 minutes (`10:50`). Nothing clears it until a flag nobody owns lands. §8.1 case 6 will pass ("must still reach `Served`") on an Agent that is un-`Ready`.
- **The evidence ordering is inverted.** `Create` publishes with no probe at all and reports `Ready=True` (M2). `Narrow` observes the refusal on a real replica and reports `Ready=False`. Less evidence yields the healthier status.
- **The page stops meaning anything.** A permanent page on every HA install trains operators to silence `PolicyApplyIncomplete`, the same page that must fire for a real `AuthNarrowingUnverified`.
- **The claimed precedent does not show what it is cited for.** 03:703 withholds a *remediation* claim (`llmFallbackActive`), not `Ready`.

**Fix.** Put it to the human (call 4 below). The coherent alternative: partial verification sets a distinct, non-paging reason, on a type that does not feed `Ready`, and `Create` gets the same claim level. Add `gateway.replicas` → `--gateway-replicas` to the slice's owed flags at 03:1108, so that one-replica installs get a full proof.

### M4 — The reason given for gating the shift on `Narrow` is broken by E2's ordering and by F2's rollback, and neither break is stated

**Where.** 03:288 justifies call 3: "a release which narrows its audience may be safe only for the narrower one, for example a new skill fit for `audit` and no one else. So the release and its audience change land together." Against it:

- 03:291 (E2): `Loosen` to the union runs **after** the shift, and `Narrow` after that;
- 03:287 (F2): a rollback "changes no auth".

**What breaks.**

- **E2.** `[payments] → [audit]` with a new audit-only skill mints R2. R2 takes all the weight while the policy admits `[payments, audit]`, and `payments` reaches R2's audit-only skill until `Narrow` reaches `Served`. That is exactly the harm 03:288 cites. 03:291 states that the removed group "keeps its access". It does not state that the removed group gains access to the new release.
- **F2.** R1 carries a skill fit only for `audit`. An edit widens to `[audit, payments]`, removes the skill, and promotes as R2. A pinned rollback to R1 then serves R1's skill to `payments` under the current spec. ADR-0034's Consequences do not name this cost.

**Fix.** State both as costs in §3.2 and in ADR-0034's Consequences, or choose differently. For E2 the only alternatives are: `Loosen` before the shift, which admits `b` to R1 ungated (the third critique's M1); holding the shift through both transactions; or dropping 03:288's coupling claim. That is the human's choice (call 3).

### M5 — ADR-0034 records two of the author's own calls as decisions the human took

**Where.** `0034:3` says "Decided by the human in three rounds". The D2 bullet at `0034:15` includes "Only then does each Agent move, under the probe. An Agent that has never been served takes the new source at once." A55 lists exactly those two as "Calls this amendment made without the human" (03:1156–1157). `0034:21` also records call 3's ordering as design 03's commitment.

**What breaks.** An accepted ADR is where the next reader stops. This one carries unconfirmed calls under the human's name, and AGENTS.md's rule that "the user decides, with reasoning shown" becomes unauditable. ADR-0034 is new in this PR, so it can still be corrected.

**Fix.** Mark those clauses "proposed by design 03 A55; not yet decided", or have the human confirm them before merge.

---

## MINOR

1. **The key-source probe cannot be built as written, and its re-entry proof does not attribute.** 03:515 writes "one `ConfigMap` per selector involved, named `<agent>-auth-probe`". The key-source row (03:523) involves two selectors, and one name in one namespace is one object, with one label set. Separately, `configMapSelector` is `matchLabels`-only, with subset semantics ("Labels that must be present on each selected ConfigMap", tgz `agentgateway.dev_agentgatewaypolicies.yaml:6872`). So an old selector `{a: "true"}` also selects every `ConfigMap` a new selector `{a: "true", b: "x"}` selects. Under the differential alone (03:542), a freshly minted old-source key gets `401` because it has not been read yet, and the new-source key gets `200` from the *old* policy. That is a false pass. 03:542's attribution argument ("a `403`, not a `401` … sibling in the same `ConfigMap`") holds only for the group row. **Fix**: name them `<agent>-auth-probe-old` and `-new`, and cover both names with the name-and-label deletion rule. Refuse an accepted selector whose `matchLabels` overlaps the recorded one, or require the full transition for that row.
2. **Re-entry to a target that is not a narrowing is unspecified.** Suppose `Served` is `[a, b]`, `[a]` has been written and is unverified, and an edit restores `[a, b]`. The comparator reads `status.auth` (03:605), finds it `Equal`, and 03:292 says "runs no auth transaction". The transaction keyed on the old `targetDigest` (03:369) is orphaned, the probe `ConfigMap` holding live credentials stays, and the policy object may keep `[a]` while status says `[a, b]`. **Fix**: while `written` is set, the applied operand is the policy object, not `status.auth`. A transaction whose target stops being a `Narrow` is abandoned by rule: delete the probe `ConfigMap`, re-assert, clear.
3. **The owed-flag lists are incomplete.** 03:1108 owes `gateway.servingUrl` and `gateway.apiKeys.acceptedSelector` to design 07. It omits `gateway.apiKeys.selector` → `--gateway-api-key-selector` (03:878) and `gateway.replicas` → `--gateway-replicas` (03:152, 03:564), and 03:564 says the latter is owed "before `Narrow` can report a full verification anywhere". **Fix**: add both.
4. **`afterShift: bool` (03:362) names no revision.** A superseding edit that leaves `-auth` unchanged keeps the transaction (03:369), and the `Loosen` then waits on a revision that will never shift. **Fix**: record the revision hash it waits on, or state the derivation: the `Loosen` fires when the revision whose projection carries the target `auth` holds all the weight.
5. **D2 edges.**
   - `status.auth` is the only record of the source in force (03:905), and "no `status.auth`" means "never served" (03:908). A restore that drops status, which is ordinary for backup tools, makes a served Agent take the new source at once and bypasses D2. **Fix**: when `status.auth` is absent, read the recorded source from the live `<agent>-auth` object.
   - An accepted selector that is left set auto-accepts a later revert to that same selector (03:907), and moves Agents created during the refused window without confirmation. **Fix**: state it, or clear the acceptance once the migration it named completes.

---

## The six calls A55's author made without the human — flagged, not resolved

| Call | My view |
|---|---|
| 1. Agents created during a refused key-source change take the new source at once (03:908) | **Defensible.** A new Agent has no callers to cut off. But a mistyped selector silently binds new Agents to an empty key set: `Create` runs no probe (M2), so they are `Ready=True` and every caller gets `401`. Add a fleet-level Event or condition while two sources are in force. The human needs to see it only because ADR-0034 already claims the human decided it (M5) |
| 2. After confirmation, Agents migrate via `Narrow` rather than by recreation (03:907) | **Defensible.** The probe is the right evidence, and recreation is an outage per Agent. The key-source row is the weakest-attributed row (MINOR 1). The same M5 provenance problem applies |
| 3. `Narrow` runs at the edit and gates that edit's shift; `Loosen` waits for the shift (03:288–289) | **The direction is defensible: a revocation should not wait for a gate. The stated reason does not hold** (M4). **Needs the human.** Choose between "a release and its audience land together", which then binds E2 and rollbacks, and "a revocation is prompt, and the coupling is not promised" |
| 4. With more than one gateway replica, every narrowed Agent stays `Ready=False` and pages (03:563–564) | **Not defensible as written** (M3). With `gatewayReplicas` unowned, it is every install. **Needs the human**: it chooses between a permanent page on HA installs and a weaker `Ready` claim |
| 5. With the probe address unset, or a foreign policy present, `Narrow` writes and holds (03:253, 03:517) | **Defensible; no human needed.** Writing moves toward denial, and status stays honest. But with the flag unset every narrowing promotion is held indefinitely, so make `--gateway-serving-url` required when the compiler runs, and refuse to start without it, as the operator already does for absent `HTTPRoute` CRDs (03:185) |
| 6. The recorded preference that design 02 move `auth` and `allowedGroups` to the policy surface (03:299) | **Defensible to record, and the reason is incomplete. Needs the human, with design 02.** On the policy surface a widening is "in place" at once (03:618), so the property 03:289 relies on, that a widening reaches production only with the gated revision it arrived in, disappears. "The gate never reads the admitted groups" is true of design 16's eval, but the gate is also the only approval step on an audience change |

The 4-minute deadline (03:556) is argued from `authz_test.go:261` and `:268`, marked unmeasured, and owed to case 6. No human needed.

---

## Cross-design and repo claims spot-checked (17)

| Claim | Source | Result |
|---|---|---|
| `traffic` may not target a Service, at 1.4.1 and 1.5.0 (03:282) | tgz `:10101–10108`; k3d dry-run | **holds**, reproduced |
| The worked policy with `mode: Strict` and two groups is admitted (03:913–944) | k3d dry-run | **holds** (admission only) |
| `configMapSelector` is `matchLabels`, states no namespace scope, and a duplicate key is "undefined" (03:896, 03:952) | tgz `:6844–6875` | **holds**. The subset semantics feed MINOR 1 |
| `mode` is `Optional`/`Permissive`/`Strict` (03:942) | `kubectl explain` on 1.5.0 | holds |
| `<agent>-serving` attaches to listener `http` (03:267, 03:517) | `httproute.go:52`, `:177`, `:205` | holds |
| `--gateway-url` is the `tools` listener on 8081; `http` is 8080 (03:517) | `hack/e2e.sh:266–267`, `:283–284`, `:308–320` | holds |
| 2-minute enforcement allowance, 5 s poll (03:556) | `authz_test.go:261`, `:268` | holds |
| Ten operator flags, no `--gateway-replicas` (03:152) | `cmd/operator/main.go:74–106` | holds |
| `values.yaml` has no `replicas`, `apiKeys` or `servingUrl` (03:152, 03:955) | `charts/assayd/values.yaml` `gateway:` | holds |
| `expose.a2a.auth` is `Enum=none;oauth`, default `oauth` (03:879) | `agent_types.go:381–384` | holds, and it is BLOCKER 1's trigger |
| `expose.a2a.auth` is projected; `expose` is behaviour surface (03:294) | `golden_test.go:22`, `:96`; `02:732` | holds |
| A pinned rollback is `spec.release.targetRevisionDigest` (03:287) | `agent_types.go:355–364`; `release.go:76–79` | holds |
| ADR-0031 Amendment 1 requires rechecking current security constraints (03:287) | `0031:19` | holds; F2's reading is consistent |
| `status.cards[]` carries a per-revision digest (03:515) | `agent_types.go:528`, `:556–570` | holds |
| `PolicyInputDrifted` is an existing condition type (03:906) | `agent_types.go:438` | holds |
| Design 02 §5 has a "not on the CRD" row for `status.revisions[]` and none for `status.apply` or `status.auth` (03:344) | `02:475` | holds |
| Design 10 pages `PolicyApplyIncomplete` open for more than 5 minutes (03:557) | `10:50` | holds, and it is M3's page |

Also checked: `configmaps` create and delete are granted (`config/rbac/role.yaml:8–20`); design 02's promotion step is "weights shift" (`02:258`); write-spec's "Required is a demand" (`write-spec/SKILL.md:21`); the model-fallback replica rule (03:703). **Design 07's A6.10–A6.13, which the slice's emitter rests on, are "not yet critiqued"** (`07:3`).

## What I verified and found sound

- **`status.auth` is the right record.** It is keyed on `targetDigest`, not the generation, so an unrelated edit does not disturb a transaction, and the comparator's applied operand for `-auth` now exists.
- **E2 removes the empty set outright.** No `-auth` compiles to nothing, and the golden-file debt is withdrawn honestly.
- **F2 is coherent today, without design 02 moving first.** `-auth` is compiled from current spec, outside the revision overlay (03:131). A pinned rollback changes only which `backendRef` carries weight. Projecting `auth` costs a gate cycle per edit, and it does not contradict F2.
- **The serving-listener address is correct.** It is the one the route actually attaches to, and "flag unset" is reported by name rather than as a probe failure.
- **D2's refusal is correct for Agents already served.** They keep being enforced exactly as before, `Ready` is untouched, and the acceptance names a selector rather than a boolean.
- **Every "not implemented" and "unmeasured" in A55 is true.** The fourth critique's line references in A55 check out.

## Is the first slice alone approvable?

**Not yet, and the gap is small.** With today's CRD there is no `apikey` value and no `allowedGroups`, so every Agent's admitted set is its namespace's group, which cannot change. The group `Narrow`/`Loosen` rows, E2 and F2's group cases therefore cannot arise in the slice. What does arise is:

- `Create`;
- the `auth: none` marker;
- `none → apikey`, a `Narrow` on the no-credential row;
- `apikey → none`, a `Loosen`;
- `→ oauth` (BLOCKER 1);
- `Adopt` refused, `AuthInputAbsent` and `ConcernNotBuilt`;
- `ForeignTrafficPolicy`;
- the key source.

**The boundary.** The slice is approvable once:

- BLOCKER 1, MAJOR 1 and MAJOR 2 are fixed;
- the human has taken call 4 (MAJOR 3), and `--gateway-replicas` is in the slice;
- the slice fixes the key-source selector as a constant with no flag, so D2 is unreachable, and says so.

The slice still depends on design 02 carrying `status.auth` and the `budget` CEL rule, and on design 07's uncritiqued A6.10–A6.13.

**Not approvable yet**: group narrowing and widening, E2, F2's rollback semantics and the D2 migration. They need MAJOR 4, MAJOR 5, MINORs 1, 2, 4 and 5, and design 02's `allowedGroups`.

## The smallest set of changes that would make the slice approvable

1. **A served `<agent>-auth` survives a compile failure** (B1): no delete, no rewrite, the sweep excludes it, and the shift is held. Correct 03:909 and owe the mutation.
2. **Re-enter at `ApplyingPolicies` whenever `written` is set** (M1), with the crash-window mutation.
3. **Probe `Create` before `Publishing`**: an anonymous `GET` of the card path must go from `500` to `401` on the prepared route. Or strike §6's "on the create path" (M2).
4. **Put call 4 to the human, and owe `--gateway-replicas`** (M3, MINOR 3).
5. **Draw the slice boundary in §8.1**: fixed selector, no D2, no group transactions.

Relabel ADR-0034's two unconfirmed clauses before merge (M5). The rest can ride along.

`03-policy-compiler.md: REVISE`
