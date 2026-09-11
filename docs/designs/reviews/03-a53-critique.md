# Design 03 (policy compiler) — critique of A53, the response to the A52 re-critique

- **Reviewer**: independent adversarial critique, per `.claude/skills/critique-design/SKILL.md` and AGENTS.md "If you are here to review". Cold read; no author context. Third critique of the consolidated body.
- **Target**: `docs/designs/03-policy-compiler.md` at `origin/design-03-a53` `c4eb13b` (PR #15), §1–§10 as the authoritative body, §11 A53 read only to learn what it claims. Also `docs/decisions/0033-slice-auth-api-keys-per-agent-no-live-tightening.md` and `docs/research/agentgateway-service-target-spike-2026-09.md`.
- **Method**: each of the 14 findings in `reviews/03-a52-recritique.md` was checked **in the body**, at its source. I then read the body as an implementer of the first slice would: emit the serving route's `-auth` `AgentgatewayPolicy` beside the `<agent>-serving` route the operator already writes (`internal/controller/httproute.go`). Repo facts were read in `internal/controller/{httproute,conditions,release}.go`, `api/v1alpha1/agent_types.go`, `charts/assayd/templates/admission.yaml`, `test/e2e/authz_test.go` and `cmd/operator/main.go`. The spike was **re-run by me**: two server-side dry-runs on k3d `assayd-local`, plus the installed 1.5.0 CRD and the vendored `test/conformance/testdata/agentgateway-crds-1.4.1.tgz`. Nothing was created in the cluster. No file other than this one was written.

## Verdict: **REVISE — 2 BLOCKER, 5 MAJOR, 5 MINOR.**

A53 is the best response this design has had. Twelve of the fourteen findings are closed in the body at their source, and the three decisions are written where an implementer will look for them. The spike is sound: I reproduced it.

**The slice is still not approvable, for two reasons.**

- **The spike's result reached one subsection and not the document.** Seven body sentences still say the `-auth` target is pending or per-revision. One of them is §3.4.4's worked policy, the only golden example of the slice's one resource. It targets a `Service`, the shape the API server refuses.
- **The per-Agent rule has a direction with no exit.** A stricter `-auth` is "held". The body names no condition for it and gives it no way out. Narrowing `allowedGroups` is the only way to take a group's access away from an Agent, and a rollback to a revision with fewer groups is also stricter. The body calls holding "the fail-closed direction". It is the opposite: the wider set stays admitted.

Both are fixable with prose and one decision. No new mechanism is needed.

---

## Closure against `reviews/03-a52-recritique.md`

| Finding | This pass |
|---|---|
| **B1** — per-revision model against a per-Agent route | **Decided, not swept.** 03:243–256 adopts the per-Agent route and places every resource. 03:262–271 records the spike's outcome. 03:543 retracts the `-<rev>` witness premise. **But** 03:56, 03:250–251, 03:258, 03:433–434, 03:758–765 and 03:911 still describe the target as undecided or per-revision. See **BLOCKER 1** |
| **B2** — old ordering in five sentences | **Closed.** 03:335 and 03:391 gate *publication*. 03:310 retracts "inert". 03:421 uses the 500-answering wording. 03:883 (§5) rests on `Publishing` being last and names the prepared route a crash can leave |
| **B3** — slice `-auth` unspecified | **Closed in substance** (§3.4.4, 03:730–788; comparator 03:474; provenance 03:147–149; 03:194 no longer withholds every route). **Residue**: the example's target (B1), `mode` (MINOR 1), and the namespace scope of the key source (MAJOR 5) |
| **B4** — first policy on a serving route | **Closed by decision** (`Adopt`, 03:441–459; §5 03:889). Implementability residue in **MAJOR 4** |
| **M1** — §3.1 table vs shipped operator | **Closed.** 03:179–184 adds the compiler column, the `PolicyCompilerAbsent` row, interim refuse-to-start and the unimplemented teardown. 03:186 gives the clearing predicate and its narrow claim |
| **M2** — present-tense watches | **Closed.** 03:224–225 state them as owed, on the shipped label key (`agent_controller.go` mapping) |
| **M3** — SSA vs create/update | **Closed.** 03:227, :235, :239, :297, :875, :896. `PreparingRoute` is never entered for an existing route (03:297) |
| **M4** — exposure without `expose.a2a` | **Closed.** 03:147, :349, :377, :383 adopt the shipped behaviour and keep the no-anonymous-card property by authentication |
| **M5** — refuted keying argument | **Closed.** 03:190 withdraws it. 03:546 re-derives the single reason on its own terms |
| MINOR 1–5 | **Closed.** Ten flags (`cmd/operator/main.go`, counted: 10). 22 `func Test` in `test/e2e`, which matches 03:912. Four kinds at 03:423. The eval SVID at 03:595. The Gateway rule is harness-only at 03:231. Design 20 still carries the per-replica clause at 03:569, verified at `20:21` |

---

## BLOCKER findings

### B1 — The spike's decision is contradicted by seven body sentences, including the slice's only worked policy, which targets the shape the CRD refuses

**Where.**

- **03:756–775**, §3.4.4's "emitted policy", the one golden example of the slice's one resource:
  - `targetRefs: [{group: "", kind: Service, name: <agent>-<rev>}]`, commented "§3.2: pending the spike. The preferred shape is shown";
  - `name: <agent>-auth  # plus -<rev> only if §3.2's spike selects per-revision`;
  - labels `assayd.dev/revision: <rev>`.
- 03:56 "Whether the serving route's `-auth` is per revision or per Agent is not decided (§3.2)".
- 03:250 granularity row: "**pending — the subsection below** | `<agent>-auth[-<rev>]` | pending".
- 03:251 "owed with the spike's result, and must not collide with a per-revision serving `-auth`".
- 03:258 "Whichever shape the spike selects…".
- 03:433–434 "if the spike in §3.2 puts `-auth` per Agent, promotion rewrites it in place … that is why per-revision is the preferred shape". This contradicts 03:268, which applies a looser `-auth` at promotion on purpose.
- 03:911 §8.1 owes "two live revisions' `-auth` on one hostname never share a target".

**Measured.** I applied 03:762–765's target to the live CRD by server-side dry-run:

```
The AgentgatewayPolicy "critic-svc" is invalid: spec: Invalid value: "object":
the 'traffic' field can only target a Gateway, ListenerSet, GRPCRoute, HTTPRoute, or InferencePool
```

The same spec on `kind: HTTPRoute` returned `created (server dry run)`, with a two-group `||` expression. The rule is in the installed 1.5.0 CRD at the `x-kubernetes-validations` beside `targetRefs`. It is also, verbatim, in the vendored **1.4.1** CRD (`agentgateway-crds/templates/agentgateway.dev_agentgatewaypolicies.yaml:10101–10108` in the tgz). So the refusal is not new at 1.5.0, and the spike note and ADR-0033 understate how stable it is.

**What breaks.** An implementer writing the `-auth` golden file copies 03:754–775, the one place the design shows the resource. Every Agent then gets `PolicyApplyIncomplete`, because the policy is refused at admission, and no route publishes. If they notice the comment, they have a naming rule (`[-<rev>]`) and a label (`revision: <rev>`) for a per-Agent object whose serving revision changes at every promotion. The owed conformance case at 03:258/911 now tests a property that holds by construction, since there is one policy. It misses the property that matters for a per-Agent target: across a rollout, exactly one `traffic` policy attaches to `<agent>-serving`. That is MAJOR 3's hole.

**Fix.**

1. Rewrite 03:756–775 with `targetRefs: [{group: gateway.networking.k8s.io, kind: HTTPRoute, name: <agent>-serving}]` and `name: <agent>-auth`. State the label rule for a per-Agent resource: no revision label, or the serving digest with its update rule.
2. Change 03:250 to "per Agent | `<agent>-auth` | the serving route". Point 03:251 at `<agent>-candidate-<rev>-auth`, which cannot collide.
3. Delete or retract 03:56, :258 and :433–434.
4. Replace 03:911's owed case with: across a promotion, exactly one `traffic` policy targets `<agent>-serving`, and a second `traffic` policy on it is detected (MAJOR 3).
5. Add a `make conformance` case over the vendored CRD that asserts the Service-target refusal, so the "revisit at the next upgrade" in ADR-0033 is a test that fails and not a reminder. Grep the body for `-<rev>]`, `pending`, `preferred`, `spike selects`.

### B2 — "A stricter candidate is held" names no condition and has no exit, and it is fail-open with respect to the change requested

**Where.** 03:269–271 ("held … the Agent reports it under the condition named in §3.3.3 … Holding that change is the fail-closed direction"). ADR-0033 "Decision / Target".

**What breaks.**

- **No condition exists.** §3.3.3 (03:425–577) names `Refused`/`CompilerUpgradeUnsupported`, `PolicyInputDrifted`/`RouteFailedClosed` and `PolicyApplyIncomplete`. None of them is a held candidate. A candidate that passed its gate and sits at weight 0 forever, with no reason on the object, is the silence rule 8 forbids.
- **No exit.** `expose` is behaviour surface (`02:301`), so narrowing `allowedGroups` mints a candidate. The gate passes it. §3.2 then holds it, and nothing in 03 or in design 02's revision machine ever releases it. The only way to take a group's access away from an Agent becomes "recreate the Agent". Revoking a single *key* still works, by editing the `ConfigMap` (03:784), which is outside every gate. So the governed path is the one that cannot revoke.
- **It is not fail-closed.** Holding the narrowing leaves the **wider** admitted set serving. The failure is in the direction of the access the administrator just asked to remove.
- **Rollback is unspecified.** A pinned rollback (`spec.release.targetRevisionDigest`, `internal/controller/release.go:76–100`, design 02 A66) to a revision whose `allowedGroups` were narrower is a stricter `-auth` at promotion. It is not a candidate, so 03:269 does not reach it. Either it rewrites the serving policy in place, which is the tightening §3.3.3 forbids, or it is held, and an incident rollback is refused. The body picks neither.

**Fix.** This is a decision for the human. Two coherent answers:

- **(a)** Treat a stricter `-auth` at promotion like `Adopt`'s option (d). Apply in place, converge, then require a **transition probe**: a wrong-group key goes from `200` to `403` before the Agent's status says the narrowing took. The prior state is the wider set that is already serving, so a NACK changes nothing a caller observes, and status stays honest until the probe passes. The attribution argument at 03:459 carries over unchanged.
- **(b)** Keep the hold, but name the condition, a reason (for example `AuthNarrowingHeld`) and an explicit exit. Owe design 02 a "gated but held" revision state. Say what a rollback to a narrower revision does.

Either way, delete "fail-closed" from 03:271 and ADR-0033.

---

## MAJOR findings

### M1 — `Loosen` at promotion applies the wider policy while the old revision is still the one serving, and "the serving revision" is undefined during a canary

03:268 applies a looser `-auth` "at promotion, as `Loosen`", and `Loosen`'s stages are Backends → **Policies** → `Converging` → **`Publishing`** (03:440). So R2's wider admitted set is live on `<agent>-serving` while `status.activeRevision` is still R1, whose gated intent is narrower. For that window the route is **less governed than the active revision's record says**. The window lasts as long as convergence takes, which the deadline bounds and nothing else does.

Separately, 03:266 says the policy "follows the SERVING revision's intent". But 03:121 records that design 02 shifts weight before `activeRevision` flips, so during a canary two revisions serve. ADR-0030 step 4 puts canaries after blue/green, which makes this latent, not absent.

**Fix.** For per-Agent concerns at promotion, order it **weight first, then loosen**. R2 briefly answers under R1's stricter `-auth`, which is a failure toward denial. Define the policy's source revision as `status.activeRevisionDigest`.

### M2 — `apiKeySource` is a new provenance with no cell in the table the body calls TOTAL

03:482 says "The table is TOTAL over provenance × comparator result". A53 adds `apiKeySource` (03:49, :149, :744), an operator flag: not Agent spec, not a non-spec producer on another CR, and not status. The comparator treats a change of key-source identity as `Unknown → Tighten` (03:474). No spec moved, so no revision mints. "Any producer not listed → fail closed" (03:528) then sends a `helm upgrade` that edits `gateway.apiKeys.selector` to a `Withdraw`, meaning weight 0, on **every Agent in the cluster**. The alternative reading is an in-place rewrite of every serving `-auth`. The body picks neither.

**Fix.** Add a row for declared operator-level inputs. `gatewayReplicas` has the same shape and already carries its own "no recompile, report skew" rule (03:200–204). State whether a selector change is refused at the flag, applied as `Adopt`-style option (d) with a probe, or treated as a fleet `Withdraw` with its outage stated.

### M3 — Nothing stops a second `traffic` policy on `<agent>-serving`, which makes the compiler's `-auth` nondeterministic, and §6 claims otherwise

- 03:899 (§6), present tense: "admission denies non-operator writes on assayd-labeled gateway resources".
- `charts/assayd/templates/admission.yaml` reserves `namespaces` and `httproutes` (`:96`) and nothing else. 03:785 concedes this for key `ConfigMap`s.

An `AgentgatewayPolicy` with `traffic.authorization` authored by anyone with `agentgatewaypolicies` write in the run namespace targets the same route as `-auth`. 03:237's own evidence says the winner is then a randomly seeded `HashSet`, per replica and per restart, while both report `Accepted`/`Attached`. Today's e2e does exactly this: `authz_test.go:176–212` hand-authors `assayd-e2e-authz` on the operator's route. The same case also leaves `Adopt`'s trigger undefined. "No governing policy" (03:441) is false when a foreign policy exists, and no other kind applies.

**Fix.**

- Before `-auth` ships, add a third admission policy that reserves `agentgatewaypolicies` whose `targetRefs` name an assayd-emitted route to the operator identity. Alternatively, have the compiler list the `traffic` policies on its targets and refuse, with a named reason, when a foreign one is present.
- Define `Adopt`'s trigger as "no **operator-emitted** policy".
- Correct §6 to the present truth (rule 7).

### M4 — `Adopt` is implementable against the condition vocabulary, but its own text contradicts itself and its page has no consumer

- **Vocabulary: sound.** Reasons on `GovernanceSkipped` are constants beside the type's other reasons (`httproute.go:636–654`). `GovernanceSkipped` is owned and sticky (`conditions.go:75`, `:102`), so `CompilerUpgradeUnsupported` and a `False`/`Governed` state fit without a new type.
- **Self-contradiction.** 03:448 says `Adopt` "writes **nothing** to the route" and in the same bullet "including its `backendRef` following promotion". Promotion is a route write, so some writer keeps running for an Adopted Agent. State that the shipped A6.10 emitter keeps owning the route's `backendRefs` for an Adopted Agent. State also that new revisions of that Agent keep promoting onto an unauthenticated route until it is recreated.
- **Stale-state loop.** Every spec edit bumps the generation, drops the record (03:315) and re-derives `Adopt`. That is consistent, but it should be said in one sentence so nobody "fixes" it.
- **The page.** 03:452 says design 10 "can page on `CompilerUpgradeUnsupported`". Design 10 says `GovernanceSkipped` "is never an alert" (`10:50`), and it keys on the reason only for `GatewayIncompatible=CRDsAbsent`. An unauthenticated serving route that only ever shows up as a normal-true tier condition is silent in practice. **Fix**: record the design 10 amendment as owed, or drop "can page".

### M5 — The namespace-derived default group is a tenancy boundary only if `configMapSelector` is namespace-scoped, and nobody has measured that

- 03:750: "a key minted for group `payments` admits to every Agent in namespace `payments` **and to nothing else**".
- 03:785 bounds the bypass to "`configmaps` write in a run namespace".

The CRD states no scope. Its description says "Selects multiple Kubernetes `ConfigMap` resources" and "**If the same key is defined in multiple ConfigMaps, the behavior is undefined**" (installed 1.5.0 CRD, `apiKeyAuthentication.configMapSelector`). If selection spans namespaces, then a labelled `ConfigMap` anywhere mints a principal for **any** group. Separately, duplicating a known key's hash under a different `group` makes that key's group undefined. That is re-grouping a credential, not merely adding one. The e2e measures one `ConfigMap` in the policy's own namespace (`authz_test.go:153–160`) and nothing about scope.

**Fix.** Owe two `conformance-cluster` cases before 03:750's "nothing else" is stated:

- a labelled `ConfigMap` in a different namespace does **not** admit;
- a duplicate `keyHash` with a different `group` is refused, or its outcome is stated.

Until both pass, mark 03:750 and 03:785 unmeasured.

---

## MINOR findings

1. **`mode` is emitted by omission.** The CRD defaults `apiKeyAuthentication.mode: Strict` and also admits `Optional` and `Permissive`. The design's own rule is "a default is not an absence" (03:702), and it emits `sessionRouting: Stateless` explicitly for that reason. **Fix**: emit `mode: Strict` in 03:767 and pin it in the golden. State that `location` defaults to `Authorization: Bearer`.
2. **The `oauth` default is persisted, so "unset" never reaches the compiler.** `Auth` carries `+kubebuilder:default=oauth` (`agent_types.go:381–384`). Every Agent written with an `expose.a2a` block has `auth: oauth` stored, so 03:476's "an unset `expose.a2a.auth` is `oauth`" describes nothing the compiler sees. Moving the default to `apikey` does not migrate stored objects, and they compile to `PolicyCompileFailed` (03:194). **Fix**: state it, and say whether design 02's change migrates.
3. **`budget` → `PolicyCompileFailed` (03:186) has no distinguishing reason.** It also has no stated precedence against `Adopt` for an already-serving Agent. ADR-0030 step 1 says "rejected **at the boundary**", which reads as admission, not as withdrawing a route at compile. **Fix**: name the reason (for example `ConcernNotBuilt`), and state that `Adopt`'s refusal wins because it writes nothing.
4. **Design 04's premise is owed at `04:125`, not `04:122`.** The line moved. The claim itself holds: design 04 still says "`route` names the revision". Trivial.
5. **The header still titles the design for "agentgateway v1.4.1 resources"** (03:1). §3.4.4's evidence and the spike are 1.5.0. The target rule holds at both (B1), so say so rather than leave the reader to check.

---

## The three calls A53's author made without the human — flagged, not resolved

| Call | Where | My view |
|---|---|---|
| An Agent declaring `budget` gets `PolicyCompileFailed` under the API-key slice | 03:186 | **Defensible and loud**, consistent with §4's totality and with 03:377's "`-ratelimit` when a rate is derived", whose `gatewayReplicas` producer does not exist anyway. But it is asymmetric. With `gateway.enabled: false` the same field is accepted and silently unenforced. Turning the gateway on makes those Agents un-`Ready`. The human should choose between that and admission-time rejection. See MINOR 3 |
| `expose.a2a.auth` default moves `oauth` → `apikey` | 03:476, :745 | **Right direction, understated cost.** It changes the product's default identity story from OAuth to shared bearer secrets, and it does not reach stored objects (MINOR 2). It belongs in ADR-0033's decision list or in design 02, not as a request from design 03 |
| `allowedGroups` `maxItems: 16` | 03:745 | **Harmless, and unargued.** The CEL rule `has(self.allowedGroups) == (self.auth == 'apikey')` needs no cost bound. 16 × a 63-character label keeps the expression far below `maxLength: 16384`. State the reason, and pin 16 and 17 in the golden and admission tests |

---

## Cross-design claims spot-checked (15)

| Claim | Source | Result |
|---|---|---|
| `traffic` cannot target a Service at 1.5.0 (03:262, ADR-0033, spike) | server dry-run on `assayd-local`; installed CRD | **holds**, reproduced; the rule is also in vendored **1.4.1** `:10101` |
| The HTTPRoute control is admitted | dry-run | **holds**, with a two-group `\|\|` expression |
| The serving route is per Agent for the two-routes-one-hostname reason (03:243) | `07:357` (A6.10) | holds |
| ADR-0033's decisions match 03:262–271, :441–459, :730–788 | `0033` | holds, and carries B2's "held" defect with it |
| `expose` is behaviour surface (03:433) | `02:301` | holds |
| `allowedGroups`/`apikey` owed to design 02 (03:148, :745) | `02` (0 hits) | holds; design 02 records no matching debt |
| Design 06 never took `identity` as a `Binding<T>` (03:138) | `06` (0 hits) | holds |
| Design 04 A5's `-<rev>` premise is owed (03:543) | `04:125` | holds; line cite drifted (MINOR 4) |
| Design 20 still requires a distinct instance per replica (03:569) | `20:21` | holds |
| The eval SVID is never the gate controller's (03:595) | `16:51` | holds |
| Design 10 keys `CRDsAbsent` on the reason (03:190) | `10:50` | holds |
| Design 10 can page on `CompilerUpgradeUnsupported` (03:452) | `10:50` ("`GovernanceSkipped`, which is never an alert") | **does not hold** (MAJOR 4) |
| ADR-0030: one identity mechanism; unsupported capabilities rejected at the boundary (03:186, :732) | `0030:5` | holds as a quote; "boundary" is contested (MINOR 3) |
| ADR-0032: `minItems: 1`, no ceiling (03:94) | `0032:5` | holds |
| Admission reserves route authorship (03:229) and nothing for policies (03:785); §6 says gateway resources (03:899) | `admission.yaml:96` | 03:229 and :785 **hold**; 03:899 **does not** (MAJOR 3) |

## What I verified and found sound

- **The spike is real.** I re-ran it and got the same refusal and the same control. It holds at 1.4.1 too.
- **The API-key mechanism matches what is measured.** `traffic.apiKeyAuthentication.configMapSelector` + `authorization {action: Allow, matchExpressions}` over `apiKey.group` is exactly `authz_test.go:176–212`, `sha256:` `keyHash` entries included (`:145–160`). `apiKeyAuthentication` exists in the vendored 1.4.1 CRD as well.
- **The `-auth` comparator for `apikey` (03:474) is well specified.** It normalizes after derivation, it excludes the keys as an operand for the right reason, and it property-tests each field independently.
- **The `GovernanceSkipped` clearing predicate (03:186)** is narrow, honest and representable in the shipped condition machinery.
- **`Adopt` as a decision** is the conservative one: it changes nothing a caller observes. Its future path (d) is argued correctly, because it asserts a transition and not a value (03:459).
- **Every "not implemented" in §3.4.4, §3.3.3 and §5 is true.** No policy is emitted, `apikey` is not a CRD value (`agent_types.go:381`), and no `agentgatewaypolicies` RBAC exists.

## The smallest set of changes that would make the slice approvable

1. **Sweep the spike's result through the body** (B1): 03:56, :250–251, :258, :433–434, :756–775, :911. Rewrite the worked policy to target `HTTPRoute <agent>-serving`, with a per-Agent label rule. Add a vendored-CRD conformance test for the Service refusal.
2. **Decide the stricter-`-auth` direction** (B2): either (a) in-place apply with a wrong-group `200 → 403` transition probe, or (b) a named held state with a condition, an exit, a design 02 debt and a rollback rule. Remove "fail-closed". Decide the promotion order for `Loosen`: weight first (M1).
3. **Close the provenance table over `apiKeySource`** (M2).
4. **Reserve or detect foreign `traffic` policies on emitted routes** before `-auth` ships. Correct §6. Define `Adopt`'s trigger as "no operator-emitted policy" (M3, M4).
5. **Owe the two key-source scope cases** and mark 03:750/785 unmeasured until they pass (M5).
6. **Fix `Adopt`'s "writes nothing"** and record the design 10 amendment as owed (M4).

The MINORs can ride along.

`03-policy-compiler.md: REVISE`
