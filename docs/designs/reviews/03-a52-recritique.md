# Design 03 (policy compiler) — re-critique of A52, the response to the consolidation critique

- **Reviewer**: independent adversarial critique, per `.claude/skills/critique-design/SKILL.md` and AGENTS.md "If you are here to review". Cold read; no author context.
- **Target**: `docs/designs/03-policy-compiler.md` at `origin/main` `ab15af4` — header and §1–§10 (the authoritative body), with §11 A52 read only to learn what it claims to have changed.
- **Method**: each of the 11 findings in `reviews/03-consolidation-critique.md` was checked **in the body**, not in A52's narrative, and against the source the finding cited. I then attacked the body as an implementer of the likely first slice would read it: emitting the serving route's `-auth` `AgentgatewayPolicy` beside the `HTTPRoute` the operator already emits (`internal/controller/httproute.go`, design 07 A6.10–A6.12). I spot-checked 17 cross-design claims at their source. Repo facts were read in `internal/controller/{httproute,agent_controller,conditions}.go`, `cmd/operator/main.go`, `api/v1alpha1/agent_types.go`, `config/rbac/role.yaml`, `charts/assayd/{values.yaml,templates/operator.yaml}`, `hack/e2e.sh`, `test/e2e/*.go`, `test/conformance/*` and the vendored `agentgateway-crds-1.4.1.tgz`. No file other than this one was written.

## Verdict: **REVISE — 4 BLOCKER, 5 MAJOR, 5 MINOR.**

A52 is honest work, and on its own terms most of it landed. Nine of the eleven findings are closed in the body at their source. B3 is closed only in §3.3; the old ordering survives in §3.3.1's normative rule and in §5. B6 is closed in substance, but its e2e row was stale on the day it was written. **The design is still not approvable. The reason is no longer the consolidation's bookkeeping. The reason is that the body has never been read against the code that now implements part of it.**

A6.10 landed the same day as A52. It made one of this design's resources real, and made three decisions this design does not contain:

- the serving route is per **Agent**, not per revision;
- `gateway.enabled: true` with the CRDs absent **refuses to start**;
- the enabled tier reports `GovernanceSkipped=PolicyCompilerAbsent`.

A6.10 handed the second and third back to this design by name ("that belongs in design 03 and is not added here", 07 A6.10 "Still owed"). A52 did not pick them up. Nor did it notice the first: a per-Agent route breaks the per-revision `ResourceSet` model that §3.1, §3.2 and §3.3.3 rest on. **Three of the four blockers are about the very first policy anyone would emit.**

---

## Closure against `reviews/03-consolidation-critique.md`

| Finding | This pass |
|---|---|
| **B1** — arm catalogue listed eight | **Closed.** 03:561 and 03:602 name the six-member `LLMArm` set; `api/v1alpha1/agent_types.go:204` is `Enum=anthropic;openai;azureopenai;vertexai;bedrock;custom`. Keeping `{azure, openai/gpt-4}` at 03:677/701 is correctly argued: it is a key-collision illustration, not a catalogue |
| **B2** — YAML example used non-existent wrappers | **Closed.** 03:571–598 match the Go types exactly: `azureopenai:{endpoint(required),deploymentName,apiVersion}` (`agent_types.go:220–226`), `vertexai:{projectId,region}` (:229–233), `bedrock:{region,guardrail}` (:236–240), `custom:{host,port,pathPrefix}` (:243–249). The cited CEL rule `(self.arm == 'custom') == has(self.custom)` is real (:261, :284). The §3.5 pricing example (03:687) uses the same shape |
| **B3** — apply-order list contradicted the stage table | **Closed in §3.3 only** (03:239–247 now prepare → Backends → Policies → converge → publish). **The same contradiction survives in §3.3.1's rule sentence and in §5**. That is BLOCKER 2 below |
| **B4** — "two transaction kinds" | **Closed.** One three-row table (03:396–400). The stage table gains `Withdrawing` and an "Entered by" column (03:255–263). Stale-state recovery names `Withdraw`'s entry stage (03:274). The kinds and stages are consistent row by row. Residue: §3.3.2 still says "What remains is `Create` and `Loosen`" (03:382), which is MINOR 2 |
| **B5** — `recordDigest` field count | **Closed.** 03:79–84 say seven and name them; the struct lists seven |
| **B6** — §8.1 had no conformance row | **Closed in substance.** 03:805–806 add both conformance lanes. All five named conformance tests exist (`test/conformance/crd_contract_test.go`), and `Makefile:58–59,75` wires `conformance` into `make test`. 03:811 replaces "no coverage at all". **But the new e2e row was false on arrival** (MINOR 1) |
| **M1** — canary/eval analogy backwards | **Closed.** 03:511 defends the canary on candidate-vs-production grounds and quotes `16:51` correctly. 03:542 (eval row) now agrees with it |
| **M2** — `gatewayReplicas` producer absent | **Closed.** 03:147 says "no", and 03:735 and 03:776 say the flag and the Deployment watch do not exist. The flag inventory it quotes is stale (MINOR 1) |
| **M3** — `status.apply` had no owning schema | **Closed** by the critique's second option: 03:265 states the field is owed to design 02 and that nothing enforces it. Design 02 §5 (02:475) still has no row for it. The critique allowed either fix, so this is acceptable |
| **M4** — MCP session research not incorporated | **Closed.** §3.4.2.1 (03:636–651) is a real incorporation. `test/e2e/mcp_test.go:233–238` sets `sessionRouting: Stateless`. `TestWeightZeroActuallyContains` and `TestGatewayAPIHasNoSessionPersistenceHere` exist (`test/e2e/weight_test.go`). The two OPEN halves are stated as open |
| **m1** — `ProducerAbsent` omitted identity | **Closed.** 03:158 names design 06 and states identity as the exception to the not-emitted branch, consistently with 03:167–170 and 03:188 |

---

## BLOCKER findings

### B1 — The per-revision resource model does not survive the per-Agent serving route the operator already emits, and the first `-auth` policy has no defined target

**Where.** Four places assume per-revision resources:

- 03:53–55: "one intent per live revision … `Compile` returns that revision's `ResourceSet`".
- 03:233: naming `<name>-<concern>[-<rev>]`, with no rule for when `-<rev>` is present.
- 03:392: "the candidate gets **its own** routes and policies … The old route is never mutated".
- 03:484: "§3.2's names are deterministic and carry `-<rev>`, so the attribute names the revision's route and not merely the Agent".

**Shipped.** `internal/controller/httproute.go:130–145` names the serving route `<agent>-serving` with **no revision**. `servingRouteFor` (:227–245) moves its single `backendRef` between revisions in place. Design 07 A6.10 records this as a decision "no design settles", taken deliberately: two routes on one hostname are merged by the listener. So the route is per-Agent. Design 20's fallback row agrees ("the serving HTTPRoute", singular), and so does 03:506.

**What breaks, concretely, for the slice.** An implementer compiling `-auth` from one revision's `PolicyIntent` emits `<agent>-auth-<rev>` targeting `<agent>-serving`. The alternative is a revisionless `<agent>-auth`. Both fail:

- **Per-revision policies.** During every weight shift and every promotion, R1's and R2's `-auth` target the same `HTTPRoute`. Two same-level policies then set `traffic.jwtAuthentication`/`authorization` on one target. §3.2's own evidence (03:231) says such a conflict resolves through a randomly-seeded `HashSet`, "per replica and per restart, while both report `Accepted` and `Attached`". The design forbids exactly this, and it is created on every rollout.
- **One per-Agent policy.** No revision's `ResourceSet` owns it. Promotion then rewrites the serving route's authentication in place. If R2's policy is stricter, that is the in-place tightening §3.3.3 (03:386–392) exists to forbid, and a NACK leaves R1's looser rule serving while status says converged (spike §2.8, 03:378).

Downstream, the §3.3.3 `Withdraw` witness (03:484) and design 04 A5 (`04:122`, "§3.2's deterministic names carry `-<rev>`, so `route` names the revision") both rest on a naming premise the shipped route falsifies. On a per-Agent route, `hop.route` names the Agent, not the revision.

**The API does offer a way out, and the design uses none of it.** The vendored v1.4.1 `AgentgatewayPolicy` CRD lets a `targetRef` name "a `Route` (optionally, with a `sectionName` indicating the route rule), or a `Service` or `Backend`" (`agentgateway.dev_agentgatewaypolicies.yaml:66–68`, `:6728`). A per-revision policy could therefore target the revision's own Service, or a per-revision rule. But weights split *within* one rule, so rule-level targeting cannot separate two revisions sharing a rule. Whether `traffic.*` concerns attach to a Service target and are evaluated per backend is unmeasured.

**Fix.**

1. State in §3.2 which resources are per-Agent and which per-revision. Adopt the shipped per-Agent serving route, or refute A6.10's reason for it.
2. For each per-revision concern, name its `targetRef` kind: route rule by `sectionName`, the revision's Service, or a per-revision route on a distinct match. Owe a `conformance-cluster` case proving two revisions' `-auth` on one hostname do not share a target.
3. Correct 03:484, and have design 04 A5's premise corrected with it.

### B2 — B3's contradiction survives in the rule sentence of §3.3.1 and in §5

**Where.**

- 03:294, the normative rule: "Every route class names the concerns that must be present and `Accepted=True` **before that route may be created**."
- 03:350: "both are checked **before the route is created**."
- 03:771 (§5): "Partial apply … **routes were last**, so no fail-open window existed."
- 03:380: "Two-phase publication — **create the route inert**". 03:269 says calling it inert "was wrong".
- 03:366: "Since routes are created last".

**What breaks.** §3.3 now says the route is created first (`PreparingRoute`, 03:241, 03:257), because a policy on an absent route reports `Attached=False` and a cold create can never leave `Converging` (03:267). Built as 03:294 states it, the -auth slice deadlocks on exactly the case B3 was about. 03:771's comfort ("routes were last") is the reason stated for there being no fail-open window, and it is now false: a crash after `PreparingRoute` leaves a route answering 500 on a live hostname (03:269). The critique's B3 fix said to state the invariant once. A52 rewrote the list and left the four sentences that restate the old order.

**Fix.** Rewrite 03:294 and 03:350 as "before that route is **published** (its `backendRefs` attached)". Rewrite 03:771's justification in terms of `Publishing` being last. Change "inert" at 03:380 to the 03:269 wording. Mark 03:366 as describing the retired barrier. Grep the body for "created last", "routes last" and "inert".

### B3 — The `-auth` concern the first slice would emit has no specified input, shape or mechanism

**Where.**

- 03:336: the A2A serving route's mandatory `-auth`.
- 03:137 and 03:188: its input is design 06 `identity`, absent at P1, so "§3.3.1 makes that a `PolicyCompileFailed`".
- 03:525 (§3.4 AuthN): "JWT policy (IdP issuer) + SPIFFE mTLS".
- 03:415: the comparator normalizes `{mode, issuer, audiences[], claim predicates, principal identity, key/JWKS source identity}`.

**What breaks.**

- Implemented as written, the compiler must **withhold** every Agent's serving route. No design 06 exists, so `-auth` input is always absent. That takes every Agent the operator serves today off the gateway: 03:188 read against `httproute.go:259–284`.
- The only `-auth` mechanism measured in this repo is `traffic.apiKeyAuthentication` over a label-selected ConfigMap of `sha256:` hashes plus a CEL `authorization` rule (`test/e2e/authz_test.go:183–197`). Nothing in `PolicyIntent`, the CRD, §3.4 or the `-auth` comparator maps to it. `expose.a2a.auth` is `Enum=none;oauth` (`agent_types.go:381`), and an API key is neither.
- ADR-0030:5 commits the slice to "one identity mechanism". The design names none it could compile.
- An implementer cannot write the golden file for `-auth` from this document: which issuer, which JWKS or key source, which CEL predicate, which principal set.

**Fix.** Add a §3.4 row that specifies the slice's `-auth` end to end:

- its intent input (a new `Binding<T>` or a chart-level key source);
- the emitted `AgentgatewayPolicy.spec.traffic` shape;
- how `authorization`'s admitted principals derive from the Agent;
- its normalization in the 03:415 comparator.

Or state explicitly that the slice waits on design 06 and that A6.10's unauthenticated route is the P1 end state. Either way, reconcile 03:188 with the route the operator already publishes.

### B4 — No transaction covers adding a first mandatory policy to a route that is already serving

**Where.**

- 03:396–400: `Create` is "a revision's routes do not exist yet"; `Loosen`; `Withdraw` is "a non-spec tightening".
- 03:419: "a **first application** … classif[ies] as `Tighten`".
- 03:423–430: provenance × comparator table.
- 03:392: "there is no in-place tightening".

**What breaks.** A cluster running today's operator with `gateway.enabled: true` has, per Agent, a **serving, unauthenticated** `<agent>-serving` route (`httproute.go:684–693` says so in the condition message). Upgrading to an operator carrying the compiler must attach `-auth` to that live route.

- It is not `Create`, because the route exists.
- It is not `Loosen`.
- Under 03:419 it is `Tighten`, and `Tighten` means "mint a revision". But no Agent spec moved, so no revision mints. No provenance row describes "the operator gained a compiler". Every cell of 03:425–430 is keyed on a producer, and an operator upgrade is none of them.

An implementer will reach for the obvious patch: SSA the policy onto the serving route. That is precisely the in-place tightening whose NACK the spike measured retaining the old, open configuration while every condition reports converged (03:378, spike §2.8). The first slice would then record authentication that is not enforced.

**Fix.** Name the transition. Candidates, each with its cost:

- (a) `Withdraw` (weight → 0, converge, apply, restore): a bounded outage per Agent at upgrade, with the dataplane caveat of 03:479–485.
- (b) a fresh `Create` under a new route identity, with the old route removed only after the new one publishes: this reintroduces the two-routes-one-hostname window A6.10 refused.
- (c) state that enabling the compiler over already-serving routes is unsupported and refuse it with a named condition.

Pick one in §3.3.3 and give it a `status.apply.transaction` value.

---

## MAJOR findings

### M1 — §3.1's `(enabled × CRDs)` table contradicts shipped behaviour in three cells, and design 07 handed the correction to this design by name

**Where.** 03:176–180, 03:772, 03:775, and the header at 03:3 ("under … §3.1's `GovernanceSkipped` row").

| Cell | 03 says | The operator does |
|---|---|---|
| `true` × present | "compile, apply, `Ready` requires accepted routes" (03:178) | emits a route and no policy; `GovernanceSkipped=True/PolicyCompilerAbsent` (`httproute.go:684`); route status is **not read** (`agent_controller.go:733–739`, `values.yaml` comment beside `gateway.name`) |
| `true` × absent | per-Agent `GatewayIncompatible=CRDsAbsent`, withhold `Ready`, page (03:179, 03:772) | **refuses to start** (`agent_controller.go:1655–1667`); checks `HTTPRoute` only, not the `AgentgatewayPolicy`/`Backend` set 03:772 requires. Design 10's page (`10:50`, keyed on the reason) can never fire (07 A6.11 "Added to A6.10's Still owed") |
| `true → false` | reverse-order teardown before `GovernanceSkipped` (03:180, 03:775) | emits nothing **and sweeps nothing** (`httproute.go:262–271`) |

`PolicyCompilerAbsent` appears 0 times in design 03. The header says the operator follows a §3.1 row, but the reason it writes is, in the code's own words, "the row §3.1 does not have" (`httproute.go:642`). Design 07 A6.10 says this "belongs in design 03 and is not added here". A52 added nothing.

The table also never says **when `GovernanceSkipped` goes `False`**. A slice that emits only `-auth` still enforces no budget, rate limit or tool filter. It is undefined whether that Agent is "governed", and a wrong answer is rule 8 on the platform's central claim.

**Fix.** Add the enabled-without-compiler row (reason `PolicyCompilerAbsent`), and state the predicate that clears `GovernanceSkipped`, per Agent, in terms of which mandatory concerns converged. Record the refuse-to-start behaviour as the current, interim form of `CRDsAbsent`, together with its fleet-wide cost (07 A6.10), and widen the CRD set that check reads before any policy is emitted. Mark the teardown row as unimplemented.

### M2 — §3.2 states, in the present tense, watches and status reads that do not exist

**Where.** 03:218: "Status ownership is **unchanged**: the operator **reads** route/Backend/policy status". 03:219: "The operator watches `HTTPRoute`, `AgentgatewayBackend` and `AgentgatewayPolicy` cluster-wide, **which it already does** … the cache key is `assayd.dev/agent-uid`".

**Shipped.** Only `HTTPRoute` is watched, and only when the gateway is enabled (`agent_controller.go:1668`). The mapping keys on `assayd.dev/agent` plus `assayd.dev/agent-namespace`, not the UID (`agent_controller.go:1621–1628`). Route status is not read (`httproute.go:50–51`, `agent_controller.go:733–739`). This is rule 7 in the section an implementer of the slice reads first. The slice needs an `AgentgatewayPolicy` watch, which in turn needs a `get/list/watch` grant that does not exist (`config/rbac/role.yaml:103–113` grants `httproutes` only).

**Fix.** Rewrite 03:218–219 as owed: which kinds get watched, on what key, and that the status read is the first thing §3.3.2's barrier needs and does not exist today.

### M3 — The design mandates SSA; the operator uses create/update, and the RBAC it cites lacks `patch`

**Where.** 03:229 ("Deterministic resource set via SSA"), 03:257 ("SSA the route with `parentRefs` set and no `backendRefs`"), 03:761 ("`Diff` drives SSA"), 03:782 ("SSA ownership conflict → re-assert"), and 03:221 ("**Only `httproutes` is granted**").

**Shipped.** `ensureServingRoute` is create/update, chosen deliberately (`httproute.go:286–288`), with drift detected by a hand-owned field comparison (`equalRoute`, :362–427). A6.11 **removed** `httproutes: patch` (07 A6.11 "Removed"). `role.yaml:107–113` has `create, delete, get, list, update, watch`. Server-side apply is a `PATCH`, so a compiler built to §3.3's stage table is refused by the RBAC 03:221 presents as sufficient.

There is a worse interaction on the per-Agent route. "SSA the route … with no `backendRefs`" (`PreparingRoute`) under the operator's field manager, applied to an existing serving route, removes the backendRefs it owns. That takes the serving revision off the air to prepare a candidate. It is B1's consequence one field over.

**Fix.** State whether the compiler uses SSA or the shipped create/update-with-owned-fields pattern (rule 6 argues for owned fields). If SSA, 03:221 must name `patch` as owed. `PreparingRoute` must be restated for a route that already exists.

### M4 — The shipped route contradicts §3.3.1's exposure rule, and neither document says so

**Where.** 03:308: "A2A serving, card | always, **where `expose.a2a` is set**". 03:342: "With no `expose` block — ADR-0027's four-line Agent, and the default — **there is no external card route at all: nothing is externally reachable**".

**Shipped.** `reconcileServingRoute` and `servingRouteFor` (`httproute.go:259–284`, :169–250) contain no reference to `spec.expose`. Every Agent with a serving revision gets a gateway route, including the four-line default Agent. A6.10 lists its three undecided choices (per-Agent, hostname, refuse-to-start) and this is not among them.

Which rule governs changes the slice's scope. If 03:342 governs, the four-line Agent must lose its route when the compiler lands. If the shipped behaviour governs, `-auth`'s "`auth: none` opt-out" and its schema default (03:417, "unset `expose.a2a.auth` is `oauth`") must say what an Agent with **no** `expose` block compiles to.

**Fix.** Decide, in §3.3.1, whether a route is emitted without `expose.a2a`, and what its `-auth` input is. Record the divergence in whichever document loses.

### M5 — A refuted premise survives in two sentences, 300 lines below its own retraction

**Where.** 03:184 withdraws the claim that design 10 and design 08 key on the condition **type**: "Design 10 §5 keys on the **reason**, in those words and twice". That is verified at `10:50`: "`GatewayIncompatible=CRDsAbsent` … keyed on the **reason**". Yet 03:477 still reads "Design 10's alerts and design 08's `deploy` stream key on the type", and 03:487 reads "because design 10's alerts and design 08's `deploy` stream key on the **type**, so an escalation that is invisible to every consumer is not an escalation". Both are used to justify a single reason (`RouteFailedClosed`) on `PolicyInputDrifted`.

That is the kind of false cross-design claim the consolidation's 179-claim sweep (03:7) says it caught. Design 10 can in fact page on a second reason, so the argument against an escalation reason is void as stated.

**Fix.** Delete the keying argument at 03:477 and 03:487. Either re-derive the single-reason choice on its own terms, or add the escalation reason.

---

## MINOR findings

1. **A52's corrections were stale against the tree they cite.**
   - 03:807 and 03:811 say `test/e2e` has **18** tests and that the e2e covers "nothing this design emits, because nothing emits it … Every one of those resources is written by the test". At `ab15af4` there are **21** `func Test` in `test/e2e`, and the serving route is operator-emitted (07 A6.10; `TestAnAgentAnswersThroughTheGateway` waits for it). The design's own header (03:3) says so.
   - 03:147 lists six operator flags. `cmd/operator/main.go:74–106` registers ten, including `--gateway-enabled/-name/-namespace/-hostname-suffix`.
   - 03:147 says the `gateway:` block carries "`enabled` and `name`". It also carries `namespace` and `hostnameSuffix` (`values.yaml:87–150`).
   - 03:174 still describes `gateway.enabled` only as a future subchart condition. It is rendered today as `--gateway-enabled` (`templates/operator.yaml:78`).

   **Fix:** refresh against `ab15af4`.
2. **03:382** "What remains is `Create` and `Loosen`" — B4's two-kind count, one subsection earlier. **Fix:** name all three, or point to 03:396.
3. **03:536**, the Candidate isolation row: "header route matched only with **gate-controller** SVID". Design 02 §3.3 step 3 (`02:257`) and `16:51` both say the admitted set is the **eval run's own** SVID, and 16:51 says "the gate controller never proxies eval traffic". 03:337 and 03:542 agree with them, so this row names the wrong principal on a security route. **Fix:** "the eval run's SVID, granted at Job launch, revoked at Job end".
4. **03:225** "The chart's Gateway sets `allowedRoutes.namespaces.from: Selector`" is present tense. 03:227 and 07 A1 say the chart ships no Gateway; the setting lives in the e2e harness (`hack/e2e.sh:216–266`). **Fix:** "the Gateway must set …; today only the harness's does".
5. **03:510** says the "distinct instance per replica" requirement "is **withdrawn**". Design 20's body still requires it: "activation requires a distinct instance per replica or reports `LLMFallbackUnavailable/PartiallyProgrammed`" (`20:21`). **Fix:** amend design 20 §3's row, or say in 03 that design 20 still carries it.

---

## Cross-design claims spot-checked (17)

| Claim in 03 | Source | Result |
|---|---|---|
| Eval principal added at Job launch, removed at end (03:511, :542) | `16:51` | holds |
| Design 10 keys `CRDsAbsent` on the reason; `GovernanceSkipped` never an alert (03:184) | `10:50` | holds — **and refutes 03:477/487** (M5) |
| Design 08's terminal list is reason-qualified (03:184) | `08:84` | holds |
| Design 02 §5 discloses `status.revisions[]`, not `status.apply` (03:265) | `02:475` | holds |
| Retained set `revisionHistoryLimit + 2 + holds` (03:93) | `02:279` | holds |
| Route moves with the Service; cross-ns `backendRef` needs `ReferenceGrant` (03:206) | `02:148` | holds |
| Candidate route admits one SVID (03:337) | `02:257` | holds; **03:536 contradicts it** (MINOR 3) |
| `capture.level` compiled per-agent in 04 (03:551) | `04:48` | holds |
| `hop.route` default; deferral rests on drain allowance (03:484, :809) | `04:120–125` | holds as a quote; **its `-<rev>` premise is falsified by the shipped route** (B1) |
| `toolAllowlist` declared on the Connector at `11:30`; no `MCPServer` kind (03:138, :140) | `11:30`, `11:90–109` | holds |
| ADR-0032: required, `minItems: 1`, no `maxItems` (03:93, :140) | `0032:5–6` | holds |
| ADR-0028 decision 2 weakens the acceptance signal (03:290) | `0028:7` | holds |
| ADR-0020 (3) is the fail-closed ordering (03:290) | `0020:4` | holds |
| ADR-0030's prerequisite discharged (03:13) | `0030:6`; `research/agentgateway-v1.5.0-concurrency-2026-09.md:3` | holds |
| Sole-KG agents get no traffic (03:309) | `01:136` | holds |
| Design 26 denies the host compiler `agents/status` (03:126) | `26:116` | holds |
| Design 20 A3 removed every Backend recompile (03:501) | `20:21`, `20:76` | holds; **the per-replica clause did not propagate** (MINOR 5) |

## What I verified and found sound

- **A52's code-facing corrections are exact** where they cite types: the `LLMArm` enum, all four instance structs, both CEL rules, and `endpointIdentity`'s non-injectivity note (`internal/revision/revision.go:91–92`).
- **§3.4.2.1** is a genuine incorporation, not a mention. It states its two open halves and names the tests that measure what *is* measured, and those tests exist.
- **The conformance rows are true.** Every named test exists, and the lane runs in `make test`. The `cluster` lane is honestly marked as outside that gate.
- **§3.2's placement and deletion-authority rules are implemented faithfully** for the one resource that exists, and are stronger in code than in prose:
  - run namespace;
  - no ownerReference;
  - name-is-deletion-authority, with `ownedRoutes` requiring name **and** UID (`httproute.go:599–609`);
  - 63-character truncation with a 16-hex hash over the whole name (`EmittedName`, :110–128);
  - the collision refusal (`routeCollision`, :552–566);
  - the `targetRefs` same-namespace constraint (03:208), confirmed in the vendored CRD at `:6696–6698`.
- **The `GovernanceSkipped` normal-true classification** 03:182 argues is implemented (`conditions.go:74–102`), and A6.10 pins the transition rather than the map.
- **The four OPEN defects** in the header are still stated in the body where they arise (03:119–120, :137, :452). Nothing in §1–§10 silently assumes one is solved.

## The smallest set of changes that would make this approvable

1. **§3.2: resource granularity.** Declare the serving route per-Agent (or refute A6.10). Give every per-revision concern a `targetRef` that two revisions cannot share, and owe the `conformance-cluster` case that proves it. Fix 03:484 and have design 04 A5 corrected. (B1, M3's `PreparingRoute` half)
2. **Sweep the ordering residue**: 03:294, :350, :366, :380, :771, :382. (B2, MINOR 2)
3. **Specify the slice's `-auth`**: input, emitted shape, admitted-principal derivation, comparator normalization. Or declare that the slice waits on design 06, and reconcile 03:188 with the route already serving. (B3)
4. **Name the transaction for a first mandatory policy on a route that already serves.** (B4)
5. **§3.1/§5**: add the `PolicyCompilerAbsent` row and the predicate that clears `GovernanceSkipped`; record refuse-to-start and no-teardown as the interim reality; make 03:218–221 owed rather than present tense, including the `patch` verb SSA would need and the watch/RBAC the policy status read needs. (M1, M2, M3)
6. **Decide exposure without `expose.a2a`** (M4), and **delete the refuted keying argument** at 03:477/487 (M5).

The MINORs can ride along. None of the six items needs a new mechanism; each needs a decision written where the implementer will look for it.

`03-policy-compiler.md: REVISE`
