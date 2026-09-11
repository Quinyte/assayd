# Design 03 (policy compiler) — critique of A54, the response to the A53 critique

- **Reviewer**: independent adversarial critique, per `.claude/skills/critique-design/SKILL.md` and AGENTS.md. Cold read; no author context. Fourth critique of the consolidated body.
- **Target**: `docs/designs/03-policy-compiler.md` at `origin/design-03-a54` `0c22726` (PR #17). §1–§10 are the authoritative body; §11 A54 was read only to learn what it claims. Also `docs/decisions/0033-slice-auth-api-keys-per-agent-no-live-tightening.md`, including Amendment 1, and `docs/research/agentgateway-service-target-spike-2026-09.md`.
- **Method**: each of the 12 findings in `reviews/03-a53-critique.md` was checked **in the body**, at its source. `Narrow` was then read twice: as an implementer building the first slice (the per-Agent `-auth` beside the `<agent>-serving` route `internal/controller/httproute.go` already emits), and as an adversary looking for a probe that passes while enforcement is wrong. Repo facts were read in `internal/controller/{httproute,conditions,release,card}.go`, `api/v1alpha1/agent_types.go`, `config/rbac/role.yaml`, `charts/assayd/templates/{admission,operator}.yaml`, `hack/e2e.sh` and `test/e2e/authz_test.go`. **Three server-side dry-runs were run on k3d `assayd-local` (agentgateway 1.5.0), and nothing was created**:
  - §3.4.4's worked policy with `mode: Strict` and a two-group `||` expression, on `HTTPRoute`: `created (server dry run)`;
  - the empty-intersection policy, `matchExpressions: ['false']`: `created (server dry run)`;
  - the same `traffic` block on `kind: Service`: refused, `the 'traffic' field can only target a Gateway, ListenerSet, GRPCRoute, HTTPRoute, or InferencePool`.

  The vendored `test/conformance/testdata/agentgateway-crds-1.4.1.tgz` was extracted and read. No file other than this one was written.

## Verdict: **REVISE — 1 BLOCKER, 5 MAJOR, 6 MINOR.**

A54 closes all twelve findings of the third critique in the body, and it closes them at their sources. The sweep is real: every line the critique listed has changed, and the worked policy is admitted by the API server. The three human decisions are written where an implementer will look for them.

**The new transaction, `Narrow`, is what fails.** Its probe is well argued: a transition, a differential and a card digest together attribute where the withdrawn witness could not. But the machine around the probe has three holes that the first real promotion will hit:

- it wedges permanently when any spec edit lands mid-transaction (BLOCKER 1);
- it has no address to probe (MAJOR 2);
- the thing it records has no field to be recorded in (MAJOR 3).

Nothing here needs a new mechanism. The fixes are one re-entry rule, three producer statements and one ownership line.

---

## Closure against `reviews/03-a53-critique.md`

| Finding | This pass |
|---|---|
| **B1** — spike result reached one subsection | **Closed.** 03:56, 03:258–273, 03:277 and the worked policy at 03:837–862 (target `HTTPRoute <agent>-serving`, name `<agent>-auth`, no revision label, `mode: Strict`). I dry-ran 03:837–862 and it was admitted. The Service shape is refused. The 1.4.1 rule is at `agentgateway.dev_agentgatewaypolicies.yaml:10101–10108` in the tgz, verbatim. The `make conformance` case is owed at 03:1022. **Residue**: 03:1067 says the spike note "still dates the Service refusal to 1.5.0". `0c22726` edited that note (`spike:35` now cites 1.4.1), so the sentence is false at HEAD (MINOR 2) |
| **B2** — stricter `-auth` held, no condition, no exit | **Closed by decision** (`Narrow`, 03:463–509; ADR-0033 Amendment 1). "Fail-closed direction" is gone. `Narrow` brings its own defects: **BLOCKER 1**, **MAJOR 1–4** |
| **M1** — `Loosen` before the shift | **Closed.** 03:283 and 03:460: widen after weight 0 on a converged route. "Serving" means carrying weight (03:281), and that covers the canary |
| **M2** — `apiKeySource` had no provenance cell | **Closed.** 03:558 adds the declared-operator-input row. 03:599 excludes declared inputs from "any producer not listed". The call it embodies is flagged below (call 1) |
| **M3** — second `traffic` policy | **Closed in substance.** Detection is at 03:245–252, the §6 admission sentence is corrected at 03:1004, and `Adopt`'s trigger is "no operator-emitted `-auth`" (03:461, 03:513). **Residue**: only route-level targets are examined (MINOR 4). §6 still asserts a withdrawn claim about tightening (MINOR 1) |
| **M4** — `Adopt` contradiction, design 10 page | **Closed.** 03:515 makes the shipped emitter own `backendRefs` and states the unauthenticated promotions. 03:518 states the loop. 03:520 corrects "can page" against `10:50`, which I verified: "`GovernanceSkipped`, which is never an alert" |
| **M5** — key-source scope | **Closed as unmeasured.** 03:833 and 03:876 claim no isolation. §8.1 cases 3–4 are owed. The installed 1.5.0 CRD still states no namespace scope for `configMapSelector`, only "behavior is undefined" for duplicates |
| MINOR 1–5 | **Closed.** `mode: Strict` is emitted (03:854, 03:866). The CRD default `Strict` and the `Authorization: Bearer` default for `location` were both verified in the installed CRD. MINOR 2 and 3 are superseded by decisions (03:826, 03:196–200). 04:125 is cited correctly (verified: it still says "`route` names the revision"). The Research line covers 1.4.1 and 1.5.0 |

---

## BLOCKER

### B1 — `Narrow` wedges permanently on any generation bump after `ApplyingPolicies`, and the one exemption covers only a restart

**Where.** 03:335, 03:481, 03:500.

- **03:335.** On every reconcile, if `status.apply.generation` is not the CR's generation, "the record is dropped" and the transaction re-enters "*its own* first stage … `ProbingBefore` for a `Narrow`".
- **03:481.** `ProbingBefore` requires the refused key's "before" answer, `200`.
- **03:500.** A restart may find the refused principal already refused. "In that one case the transaction accepts the differential alone … **nothing else accepts it**."

**What breaks.** The narrowed policy is applied at `ApplyingPolicies`. After that, the refused key gets `403` for the rest of the transaction. Now suppose any edit bumps the generation before `Served`:

- a `runtime.replicas` change (policy surface, 03:129);
- a second spec edit that supersedes the candidate;
- the user fixing a typo while the promotion is in flight.

Re-entry at `ProbingBefore` then waits for a `200` that will never come again. At the deadline the Agent gets `AuthNarrowingUnverified`, `Ready=False` and `Degraded` (03:497). The weight shift waits forever (03:283, 03:463), and nothing clears it short of an operator restart or recreating the Agent.

A second path reaches the same wedge. A `Loosen` that NACKed leaves the stricter rule serving (03:648). A later `Narrow` back to that set then finds its "before" state already refused.

This is the defect the third critique's B2 named, "no exit", reached through the new transaction on an ordinary edit.

**Fix.** Make re-entry depend on what was applied, not on why the record was dropped. If the desired `-auth` digest equals the one `Narrow` was applying, and `ApplyingPolicies` was recorded as reached, re-enter at `ProbingAfter` and accept the differential alone under the message "transition not observed". Otherwise re-enter at `ProbingBefore`. That is the crash rule at 03:500, stated for every re-entry. This needs `status.apply` to record "policy written" durably (MAJOR 3). Add the mutation to §8.1 case 6: bump the generation after `ApplyingPolicies`; the transaction must still reach `Served`.

---

## MAJOR

### M1 — "The weight shift waits for `Served`" is a contract on machinery design 03 does not own, and nothing records it as owed

**Where.** 03:283, 03:287, 03:463. The shift is performed by the shipped emitter, which writes the serving revision's single `backendRef` at weight 100 (`internal/controller/httproute.go:179`, `:227–244`), and by design 02's revision machine. `grep -n Narrow docs/designs/02-agent-crd-operator.md` returns nothing. 03:1026–1030, "Owed to other layers by A54's decisions", lists admission tests, golden files and the e2e, but not this.

**What breaks.** An implementer building `-auth` beside the shipped route has no instruction to change the emitter. Promotion then swaps the `backendRef` while `Narrow` is still in `ProbingBefore`. For the length of that window the incoming revision serves under the outgoing revision's wider set: the fail-open the ordering exists to prevent.

**Fix.** Record under "Owed to other layers" that design 02 §3.3's promotion step, and design 07 A6.10's emitter, gate the `backendRef` change on the per-Agent `-auth` transaction reaching `Served`, with the rollback exception. Add an envtest case: with `Narrow` held in `ProbingAfter`, the route's `backendRefs` do not change.

### M2 — The probe has no address, and "N declared replicas" has no producer

**Where.** 03:470 says "through the Gateway to the Agent's hostname". 03:506 says "probed 1 of N declared replicas".

- **The address.** The only Gateway address the operator holds is `--gateway-url` (`charts/assayd/templates/operator.yaml:84–88`). The e2e sets it to the **`tools`** listener on `:8081` (`hack/e2e.sh:308–320`) and says so: "gateway.url is where agents send their EGRESS". `<agent>-serving` attaches to listener `http`, which is `:8080` (`hack/e2e.sh:266–267`). A probe sent to `--gateway-url` gets no route on both keys, so `ProbingBefore` never passes and **every** `Narrow` wedges.
- **The replica count.** N is `gatewayReplicas`, whose producer does not exist (03:150). "Declared" names nothing (rule 7).
- **What more than one replica means.** 03:640 refuses to record a clean fallback remediation when there is more than one replica. `Narrow` still reaches `Served` and records the narrowing on one replica's evidence, and the body never says it is weaker. The "slice runs one replica" is true only of the e2e harness's Gateway, because the chart ships none.

**Fix.** Name the serving listener's address as a declared input: a flag, or derived from `--gateway-name`/`--gateway-namespace` plus the Service label `gateway.networking.k8s.io/gateway-name` and the `http` listener's port, as the e2e derives it. Say what `Narrow` records when the replica count is unknown or greater than one. For consistency with 03:640, that means reaching `Served` with `PolicyApplyIncomplete` kept, under a distinct reason.

### M3 — The per-Agent `-auth`'s applied state has nowhere to live

**Where.** 03:484 records the narrowing "in `status.apply.stage` and in `status.revisions[].appliedDigest`". Three things contradict it:

- 03:56 says the per-Agent `-auth` "belongs to no single revision's set".
- 03:79 defines `appliedDigest` as per revision, over that revision's `ResourceSet`.
- `status.revisions[]` is not on the CRD: 0 hits for `json:"revisions` in `api/v1alpha1/agent_types.go`.

`status.apply` is singular (03:326). So a key-source `Narrow` that fans out over every Agent (03:507) collides with any Agent whose candidate `Create` is in flight.

**What breaks.** The comparator at 03:533 needs the applied `-auth` operand to classify `Loosen` against `Narrow`. The intersection rule at 03:282 needs it too. BLOCKER 1's fix needs "policy written" to survive re-entry. None of the three has a field.

**Fix.** Give the per-Agent `-auth` its own applied record, for example `status.auth: {appliedDigest, keySource, admittedGroups, transaction, stage}`, owed to design 02 beside `status.apply`. Or make `status.apply` a list keyed by transaction target. Delete the `status.revisions[].appliedDigest` claim for `-auth`.

### M4 — The disjoint-change outage is called "bounded by the two transactions' deadlines", and the deadline bounds nothing

**Where.** 03:285 against 03:339 ("The deadline is a condition, not a timeout … the machine stays in its stage"). If `Narrow` to `false` applies and then fails to verify (BLOCKER 1, MAJOR 2, a NACK), the weight shift waits forever and the Agent refuses every key forever. That is an unbounded total outage, stated as bounded (rule 7). The `false` expression is admitted by the API server (dry-run above), but its controller translation and dataplane effect are unmeasured.

**Fix.** Strike "bounded". State that the outage lasts until `Loosen` converges, with no upper bound. Name the condition the operator sees during the outage (`AuthNarrowingUnverified` is the wrong name for a total refusal that did take). Owe a `conformance-cluster` case for `false`. Then see call 3.

### M5 — A rollback to a wider revision re-admits a removed group, on a contested reading of ADR-0031 Amendment 1, and the call is not flagged

**Where.** 03:286: "Rolling back to one with **more** groups … re-admits the groups the newer revision removed … It is not ADR-0031 Amendment 1's 'restore a revoked credential': the credential is the key".

ADR-0031 Amendment 1 (`0031:19`) says more than "credential". It requires the operator to "recheck non-optional current security constraints, because a rollback is not authorization to restore a revoked credential **or a forbidden destination**." A group an administrator removed from `allowedGroups` is a current access decision. Re-admitting it on rollback is at least arguably the thing the Amendment forbids.

The design reads the Amendment narrowly and proceeds. That is a decision about an accepted ADR, and it is absent from A54's list of calls made without the human (03:1065).

**Fix.** Flag it for the human. The two coherent answers:

- (a) A rollback's `-auth` is the **intersection** of the target's set and the current spec's set. A rollback never re-admits, and a re-admission is a new gated revision.
- (b) Record (a)'s opposite in ADR-0031 as an amendment, with the reason.

---

## MINOR

1. **Stale "no in-place tightening" sentences.** §6 (03:1004) still says "§3.3.3 withdrew in-place tightening entirely and routes tightening through the revision gate instead". 03:601 says "§3.3.3 says no transaction tightens a serving route in place". 03:453 opens "So tightening is not a compiler transaction at all" and contradicts itself four sentences later. `Narrow` is an in-place tightening. **Fix**: sweep these for `Narrow`, and in §6 above all, since it is the security section.
2. **03:1067 is false at HEAD.** It says the spike note is unedited and dates the refusal to 1.5.0. Commit `0c22726` edited it (`spike:35`). **Fix**: correct the sentence.
3. **`false` is owed only a golden file** (03:1029). Add it to §8.1's `conformance-cluster` owed list. API admission is all that has been measured (above).
4. **Foreign detection sees route-level targets only** (03:245). The CRD admits `traffic` policies on a `Gateway` or `ListenerSet`, and whether a gateway-level `authorization` merges with the route's or is overridden by it is unmeasured. **Fix**: say detection covers route and selector targets only, and owe one case for a Gateway-targeted `traffic.authorization`. Also state what `Narrow` does while `ForeignTrafficPolicy` is set: its probe result is random per replica (03:243), so it should not start.
5. **Two `Narrow` edges are unspecified.**
   - `auth: none → apikey` (03:475) must remove the route's no-auth marker label (03:405), which is a route write, and `Narrow` has no route stage.
   - A key-source `Narrow` on an Agent that is `Adopt`/`Refused`, `auth: none` or `PolicyCompileFailed` has no stated behaviour. **Fix**: one sentence each.
6. **ADR-0033 Amendment 1 reverses the decision in the ADR's own title.** "Refuses to tighten a live route" becomes "patched in place under a probe" (`0033:22`, `:26`). AGENTS.md says to propose a superseding ADR rather than relitigate an ADR. **Fix**: supersede with ADR-0034, or retitle ADR-0033 and mark its Decision "superseded in part" at `0033:3`.

---

## The seven calls A54's author made without the human — flagged, not resolved

| Call | Where | My view |
|---|---|---|
| 1. A key-source change runs `Narrow` on every Agent | 03:149, 03:476, 03:558 | **Defensible, and needs the human.** It beats a fleet `Withdraw`. But the probe asserts that an old-selector key goes `200 → 401`, which means **every real key under the old selector stops at once, fleet-wide**. The design never says that keys must be copied under the new selector first. A selector change can also *widen*, and it is applied in place ungated. The alternative, refusing a live selector change at startup until Agents are recreated, is simpler and should be offered |
| 2. A foreign policy is reported, and a published route is not withdrawn | 03:249 | **Defensible; no human needed.** Whoever can write the policy already controls admission, so withdrawing on their write hands them an off switch. But "an unpublished route is not published" gives them the same switch over new Agents, and the interaction with `Narrow` is unstated (MINOR 4) |
| 3. A disjoint group change passes through an all-keys-refused window | 03:285 | **Consistent with the invariant, and needs the human**, because an ordinary edit becomes an outage whose bound is false (MAJOR 4). The alternatives: refuse a disjoint change at admission, requiring an overlapping intermediate step, or accept a short ungated window |
| 4. A rollback's weight shift proceeds at `Narrow`'s deadline | 03:287 | **Needs the human.** It is the single exception to the invariant the whole of §3.2 rests on. "Strictly worse" is asserted, not shown: when the incident *is* the group, serving the target under the wider set is the same exposure. And every narrowing rollback is delayed by the probe even when it succeeds. Decide it together with MAJOR 5 |
| 5. The operator mints its own probe keys | 03:504 | **Defensible; no human needed beyond the recorded cost.** The operator can already write the policy, which lets it admit anyone, so minting a key adds no reach it lacks. The residue is stated correctly: live credentials to every Agent sharing the group, for the length of the window. The key-label reservation owed to design 07 must name the operator (03:505) |
| 6. A 120 s deadline | 03:496 | **Unargued and unmeasured.** The repo's only measurement allows 2 minutes for *one* policy to take (`test/e2e/authz_test.go:261`). 120 s must cover `ProbingBefore`, apply, converge and `ProbingAfter` together. Make it a constant pinned by §8.1 case 6's measured timings. No human needed |
| 7. Post-crash recovery accepts the differential alone | 03:500 | **Defensible, and too narrow.** A `403` (not `401`) on a fresh key, while its sibling in the same `ConfigMap` gets the recorded card, is authorization refusing that group. The differential does most of the attributing. It should cover every re-entry after the policy write (BLOCKER 1), under a message consumers can tell apart. No human needed |

---

## Cross-design claims spot-checked (17)

| Claim | Source | Result |
|---|---|---|
| `traffic` cannot target a Service, at 1.5.0 and 1.4.1 (03:277) | dry-run; tgz `:10101–10108` | **holds**, reproduced |
| The worked policy with `mode: Strict` and two groups is admitted (03:837–868) | dry-run | **holds** (API admission only) |
| The `false` expression is emittable (03:285) | dry-run | **holds at admission**; translation and dataplane unmeasured |
| `mode` defaults to `Strict`; `location` defaults to `Authorization: Bearer`; `configMapSelector` states no scope (03:833, 03:866) | installed 1.5.0 CRD | **holds** |
| `status.cards[]` carries a per-revision digest over the served bytes (03:470, design 02 A68) | `agent_types.go:526–528`, `:568–570`; `card.go:240`; `TestTheDigestIsOverTheServedBytes` | **holds** |
| `Narrow` records into `status.revisions[].appliedDigest` (03:484) | `agent_types.go` (0 hits) | **does not hold** (MAJOR 3) |
| The operator already holds `configmaps` create/delete (03:505) | `config/rbac/role.yaml:8–21` | holds |
| Admission reserves namespaces and routes only (03:1004) | `admission.yaml:40`, `:96` | holds |
| `GovernanceSkipped` is owned and sticky (03:186, 03:520) | `conditions.go:74–75` | holds |
| Design 10 never alerts on `GovernanceSkipped`, and pages `PolicyApplyIncomplete` open for more than 5 m (03:497, 03:520) | `10:50` | holds |
| Design 20 still requires a distinct instance per replica (03:640) | `20:21` | holds |
| Design 04 still rests on `-<rev>` route names (03:614) | `04:125`; `hop.route` default at `04:120` | holds |
| The eval SVID is never the gate controller's (03:666) | `16:51` | holds |
| Design 06 carries no identity `Binding` (03:138) | `06` (0 hits) | holds |
| ADR-0030: unsupported capabilities rejected at the boundary; blue/green before canaries (03:196, 03:281) | `0030:5` | holds |
| A rollback re-admitting groups is not ADR-0031 Amendment 1's case (03:286) | `0031:19` | **contested** (MAJOR 5) |
| "Required is a demand" is quoted and departed from (03:818) | `write-spec/SKILL.md:21` | holds; the departure is argued |

## What I verified and found sound

- **The sweep is complete.** No body sentence describes a per-revision `-auth` or a Service target. The worked policy is the shape the API admits.
- **The probe's attribution argument is right.** A transition on the refused key, a same-round differential on a kept key and a card-digest `200` together exclude a backend that already refused the key, a failing gateway, and a `200` from something other than the Agent. The card digest is over the raw bytes the container serves, so a probe through a pass-through route can match it. The card path rides `PathPrefix: /` and its `-auth`, so the probe sends no task.
- **The ordering is right in both directions.** Narrow before weight, widen after weight 0, and each window fails toward denial. The canary is handled by the intersection over every serving revision.
- **Status honesty holds.** Nothing claims the narrowing before `Served`, and a NACK withdraws the claim (03:498).
- **`Adopt` stays coherent beside `Narrow`.** Its trigger (no operator-emitted `-auth`, no `status.apply`) cannot collide with `Narrow`, which needs an applied `-auth`. Leaving option (d) undecided is honest.
- **Every "not implemented" in A54 is true.**

## The smallest set of changes that would make the slice approvable

1. **One re-entry rule for `Narrow`** (B1): re-enter at `ProbingAfter` whenever the same `-auth` digest's policy was already written, and pin it with a generation-bump mutation.
2. **Give `-auth`'s applied state a home** (M3). This is also what B1's rule reads.
3. **Name the probe's address and its replica rule** (M2).
4. **Record the shift-waits contract as owed** to design 02 and 07's emitter, with its envtest case (M1).
5. **Strike "bounded"** from the disjoint case (M4), and put calls 1, 3 and 4 and MAJOR 5 to the human.

The MINORs can ride along.

`03-policy-compiler.md: REVISE`
