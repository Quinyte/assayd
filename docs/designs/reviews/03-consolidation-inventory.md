# Design 03 — consolidation inventory

**Scope**: the authoritative body of `docs/designs/03-policy-compiler.md`, header + §1–§10 (lines 1–734).
§11 (lines 735–837) is provenance and is out of scope.

This is an **inventory pass, not a verdict pass**. No file was edited. Every claim below was checked
against `api/v1alpha1`, `internal/revision`, `internal/controller`, `cmd/operator`, `charts/plume`,
`config/crd`, `test/conformance`, `test/docs` and the three agentgateway research notes at the working
tree of 2026-09-04, HEAD `71b5f5b` — i.e. **after** the round-3 critique and after the corrections that
commit made to designs 03, 04 and 20, so several round-3 findings are narrowed or closed below rather
than repeated. Every cross-design claim was checked against the producing document's current text, not
against design 03's description of it. `go test ./test/docs` and `go test ./test/conformance` were run;
both pass.

**Ground truth for design 03**: *nothing in it is implemented.* `internal/compiler/` is an empty
directory. There is no `PolicyIntent`, no `RevisionRecord`, no `status.apply`, no `status.revisions[]`,
no `ResourceSet`, no pricing ConfigMap, no `--gateway-replicas` flag. Four things design 03 *originated*
have shipped, all inside other people's code: the revision-name truncate-and-hash rule
(`internal/controller/runnamespace.go:118`), the classification moves it forced into design 02's
projection (`internal/revision/leaves.go`), the twelve `ConditionType` constants it names
(`api/v1alpha1/agent_types.go:398–433`), and the vendored-CRD conformance suite that pins its upstream
premises (`test/conformance/`). Design 02's A42 run namespace and its revision material are implemented
and tested; design 03's premises about them are, where checked, true.

---

## Counts

| Class | Count |
|---|---|
| **A — CONTRADICTION** (two body sentences that cannot both be true) | 24 |
| **B — STALE STATUS** (owed/future/"today" that is now done, or stated-as-done that is not) | 14 |
| **C — NARRATED RETRACTION** (the body arguing with an earlier version of itself) | 47 |
| **D — HISTORY LEAK** (an amendment, round, reviewer or spike as the subject of a sentence) | 28 named sites, on top of a blanket **177 bare `A<n>` tags across 131 body lines** |
| **E — UNENFORCED CLAIM** (present-tense claim of a mechanism nothing provides, no disclaimer) | 17 |
| **F — DUPLICATE** (same rule in two or more places, different wording) | 26 sets |
| **G — DEAD ANCHOR** (a §x.y, field, test, file or cross-design reference that does not exist or does not say what is claimed) | 23 |

**179 findings across 734 body lines.** Two shapes dominate and they are different from design 02's.
C + D + F account for **101 of the 179** — the same sedimentation. But design 03 carries something
design 02 did not: **24 live contradictions, twelve of them inside one section**, and the round-3
critique's diagnosis is confirmed exactly — the same rule is stated five times and no two agree.

The substantive defects the rewrite must **decide**, not tidy, are twelve. The first is the largest
thing this pass found:

- **G-17** — `tools[].toolAllowlist` is a **declared field on the Connector CR** (`11:30`, "narrows what
  the server offers"), not "the resolved tool server's advertised tool set" as §3.1's provenance table
  says. **The entire non-spec-producer machinery — `resolvedInputs`, the seal, "frozen",
  `PolicyInputDrifted`, the `Withdraw` transaction — is built on the premise that this input comes from
  a producer with no way to mint a revision.** Design 11 makes it somebody's spec.
- **A-1** — §3.3's numbered apply-ordering contract ("Backends first … Routes last") is the pre-A15
  order, and the stage table eight lines below it creates the **route first**. §5 still argues safety
  from "routes were last".
- **A-2** — §3.3.3 says "**Two transaction kinds remain**" and then adds a third eighty lines later;
  §3.3's recovery rule enumerates two and omits the third.
- **A-3** — `recordDigest` covers "the **six** fields above" (L70) and "**every** field" (L78) over seven.
- **A-4** — §3.5 states the token bucket has **no burst** (L646, L460) and then devotes a paragraph to
  sizing its **capacity** (L656).
- **A-5** — the `Withdraw`/canary receipt witness "has no producer … no input at all" and, four
  sentences later, "has four defaults … the reason is no proof, not no input" (L444); §8.1 then defers
  the test for the reason L418 already disproved.
- **A-13** — the `PolicyIntent` type has no field for `behaviourProjection`, `resolvedInputs` or the
  live overlay, yet §3.1 and §3.3.3 both say the compiler recompiles from them, and §4 calls `Compile`
  **pure**.
- **E-1 / G-5** — `status.apply` is required by the apply machine, declared by no schema, and owed to
  nobody.
- **E-4 / G-9** — `gatewayReplicas` is the divisor on **every** rate limit; its stated producers
  (`gateway.replicas`, `--gateway-replicas`) do not exist, and its provenance row says "Available at
  P1? **yes**".
- **G-6** — plume's `LLMArm` enum has **six** arms; §3.4.1/§3.4.1.1 write the catalogue and the subset
  predicate over **eight**, and the instance-field table omits the `azure` arm's five fields.
- **G-7** — §3.4.1.1's `egressAllowlist` YAML example uses `instance:` and `endpoint: {scheme: …}`.
  Neither key exists; `scheme` is not a field anywhere in `api/v1alpha1`.
- **E-9 / A-19** — §3.5's "a rate below 1 token/second is a compile error" and its own worked example
  disagree about which quantity is compared; the example computes **138 tokens/hour**, which the CRD
  accepts and which does not "deny everything".

---

## A — CONTRADICTION

**A-1. The apply order is stated twice and the two orders are opposite.**
- L187–192: "Order is part of the contract: 1. **Backends** first. 2. **Policies** next. 3. **Routes /
  weight changes last** — traffic only ever flows through fully-policied paths."
- L202 (the stage table, fifteen lines later): "`PreparingRoute` | SSA the route in its **inert** shape:
  `parentRefs` set, **no `backendRefs`**".
- L211 states why the route must be first: "a cold create reached `Converging` and could **never** leave:
  the stage that creates the route sat behind the barrier that needed it."
- L194 then anchors the barrier to the dead numbering: "**Between 2 and 3** sits a convergence barrier".
  In the stage machine the barrier sits between `ApplyingPolicies` and `Publishing` — stages 3 and 5 of
  five — so the anchor resolves to nothing.
- The "the inert route is not traffic" defence does not save item 1: "**Backends first**" is flatly
  contradicted by `PreparingRoute` preceding `ApplyingBackends`, and it is item 1 that an implementer
  builds from.
- **Fix**: delete the numbered list. The contract is the stage table plus the two invariants it
  preserves — *no route carries traffic before its mandatory concerns converge* and *removal is
  reverse-ordered*. The barrier sits between `ApplyingPolicies` and `Publishing`, not "between 2 and 3".

**A-2. "Two transaction kinds remain" and there are three.**
- L327: "**Two transaction kinds remain:**" followed by a two-row table (`Create`, `Loosen`).
- L405–409: "`Withdraw` is therefore a real transaction with stages", with its own table adding
  `Withdrawing`.
- L403 narrates the collision it created and does not fix the sentence: "§3.3.3's table says two
  transaction kinds remain so `status.apply.transaction` had no value for it".
- **Fix**: one transaction table with three rows, in §3.3.3, stated once.

**A-3. `recordDigest` covers six fields and every field, over seven fields.**
- L70: "`recordDigest:    string      // SHA-256 over the six fields above, schemaVersion included`"
- L78: "`recordDigest` covers **every** field including both of them — so a forged seal … is a digest
  mismatch"
- The fields above it are `schemaVersion`, `hash`, `behaviourProjection`, `originalBudget`,
  `resolvedInputs`, `sealedBindings`, `appliedDigest`: **seven**. The disputed one is `sealedBindings`,
  the operand the freeze keys on. Third round on this finding.
- **Fix**: say seven, in the comment an implementer types.

**A-4. The token bucket has no burst, and a paragraph sizes its capacity.**
- L646: "**`burst` applies only to `requests`** … so the token bucket has no burst allowance and the
  `rate × 3600` capacity of earlier drafts is void".
- L460: "**No burst**: `burst` applies to `requests` limits only, so the capacity arithmetic an earlier
  draft carried is void (A10)".
- L656: "**Capacity is part of the emitted policy, not an afterthought.** §3.4 emits a token *bucket*,
  and a bucket has both a refill rate and a burst capacity … at `capacity = tokensPerDay` the gateway
  would be a daily ceiling rather than a rate".
- `test/conformance/crd_contract_test.go:181` pins the upstream doc comment that makes L646 true: `burst`
  is settable only beside `requests`, and `ExactlyOneOf=requests;tokens` forbids carrying both. **There
  is no capacity knob on a token limit at all** — capacity is implicitly one hour's refill, because that
  is the only window `unit` can express. L656 therefore reasons at length about choosing a parameter the
  emitted policy cannot carry, and its worked alternative (`capacity = tokensPerDay`) is not expressible
  in either direction.
- **Fix**: delete L656 entirely. It is the last surviving fragment of the pre-ADR-0028 arithmetic and it
  is the paragraph an implementer would build a golden file from. The surviving true sentence inside it —
  that an hourly window is a rate rather than a daily ceiling, so an agent cannot spend everything in the
  first minute and go dark until 00:00 UTC — belongs in §3.5's arithmetic as a one-line consequence of
  `unit: Hours`.

**A-5. The receipt witness has no producer and has four producers, in one table cell.**
- L444: "First, `hop.gatewayInstance` has **no producer**: design 04 introduces the field and names no
  agentgateway attribute and no plume-owned stamping mechanism for it (design 04 A5 records the gap), so
  today the check has no input at all."
- L444, four sentences later: "**The reason is no proof, not no input.** An earlier draft of this
  amendment said both fields had no producer; measured against tag `v1.4.1`, `hop.gatewayInstance` has
  four defaults and `hop.endpoint` has three of its four parts, so the canary *can* attribute".
- `docs/research/agentgateway-otlp-attributes-2026-09.md:190–200` confirms the second: four resource
  attributes, all emitted by default, no configuration.
- **Fix**: state the surviving rule once — the per-replica claim is withdrawn **because nothing routes a
  canary to a chosen replica through a Service**, not for want of a field — and delete the "no producer"
  clause.

**A-6. §8.1 defers a test for a reason §3.3.3 disproved.**
- L418: "An earlier draft of this amendment deferred it on the ground that nothing could attribute a
  receipt to a route, and a primary-source pass disproved that … **What is still owed is narrower and is
  only the drain allowance**".
- L720: "A49's withdrawal-escalation case … **cannot be written until design 04 carries a route or
  revision identity on the envelope and a drain allowance** (design 04 A5), **because nothing today can
  attribute a receipt to a route plume withdrew**."
- **Fix**: §8's deferral reason is the drain allowance alone.

**A-7. One-concern-per-policy stands on a source finding, and on diffability, in three places.**
- L179: "**R1 is closed by source, and the answer is worse than 'undocumented'** … One-concern-per-policy
  is load-bearing, not a diffability preference, and **no reproducing test is owed**."
- L708 (§8): "R1 needs no reproducing test — it is closed by source (§3.2)."
- L722 (§8.1): "R1's same-level/same-field overlap test — all of which land with the agentgateway
  subchart (design 07). … **The one-concern-per-policy rule stands on diffability alone until R1 runs,
  exactly as §3.2 and D2 already say.**"
- §3.2 and D2 say the opposite. **Fix**: keep L179's rule; delete L722's clause.

**A-8. Two §5 rows give opposite answers to "the producing design is not installed".**
- L682: "Route class whose **producing design is not installed** | Not emitted. **No *per-class*
  condition** — one would fire on every Agent in every core-tier cluster and say nothing actionable."
- L694: "A declared binding's producer is absent (A46) | `CapabilityUnavailable` + `Ready=False`."
- L121/L126 reconcile them ("only where that Agent requested something from the absent producer"); the
  §5 rows do not, and §5 is where an operator looks. The reconciliation is also now false for `identity`
  — see A-9.
- **Fix**: one §5 row: not emitted and no condition where nothing was declared; `CapabilityUnavailable`
  + `Ready=False` where it was.

**A-9. `identity` is a binding, and the binding-state table's producer list does not include its producer.**
- L26: "`identity: Binding<{spiffeID | oauthClientID}>` — a Binding like the others (A46)".
- L121: "`ProducerAbsent` | the producing design **is not installed (11 for tools, 01/13 for KG)**" — a
  closed enumeration, design 06 absent from it.
- L126: "`CapabilityUnavailable` is added per Agent **only where that Agent requested something from the
  absent producer**". **No Agent declares an identity and every Agent has one**, so read as written, at
  every install with design 06 absent, every Agent carries `CapabilityUnavailable` and `Ready=False` —
  the exact outcome L682 refuses in those words.
- L122: "`Unresolvable` | **the producer exists and the name does not resolve**". An SVID has no name to
  resolve, so L260's "unresolvable identity ⇒ `PolicyCompileFailed`" has no trigger.
- **Fix**: the rewrite must decide whether `identity` is a binding at all, and if it is, say what
  `ProducerAbsent` and `Unresolvable` mean for a producer with no request string, and carve it out of the
  per-Agent condition.

**A-10. The `-inputs` object's bound is one MiB and 4 KiB.**
- L81: "The bound that remains is the object's own — **one MiB** — and exceeding *that* is a compile
  error naming the producer and the measured size, which is **a bound a producer can be held to**."
- L92/L93 (nine lines below): an **external** Agent and a **hard-mode** Agent are "**always inline**, and
  a value over **4 KiB** is a compile error naming the binding".
- Two of the three cases in the storage table have no object and a bound 256× tighter than the one the
  size row names as final.
- **Fix**: the size row must name both bounds and which mode selects each.

**A-11. The candidate route's admitted principal is named three ways.**
- L272: "admitted set is **one SVID** (design 02 §3.3 step 3)".
- L470 (§3.4): "Candidate isolation | header route matched only with **gate-controller SVID**".
- L476 (§3.4): "Eval temporary grant | candidate-route admitted-set add/remove at eval Job launch/end
  (**per-run eval SVID**)".
- L379/L445: "**the eval principal**".
- Design 02 §3.3 step 3 (`02:243`) says "**the eval run's own SVID**, granted at Job launch and revoked at
  Job end". L470's gate-controller is wrong; the gate controller launches the Job, it is not the
  principal on the route.
- **Fix**: one name — the eval run's SVID — everywhere.

**A-12. The concern set is five concerns, and six are emitted.**
- L177: "`AgentgatewayPolicy` × N — **one concern per policy** (`-auth`, `-ratelimit`, `-toolfilter`,
  `-guard`, `-transform`), disjoint fields, compiler-side conflict check."
- L471 (§3.4): "On-behalf-of exchange | **`-exchange` policy**: `oauthTokenExchange`".
- `-exchange` is in neither the concern list, nor §3.3.1's mandatory-set table, nor §3.3.3's comparator
  registry — which L351 calls closed ("A concern absent from the registry … classif[ies] as `Tighten`").
- **Fix**: one enumeration of the policy concerns, and either a comparator row for `-exchange` or an
  explicit statement that it is never compared.

**A-13. `Compile` is pure, and its input type cannot carry what it recompiles from.**
- L674 (§4): "`Compile(intent) → ResourceSet` … is **pure**, deterministic (golden files), and **total**".
- L47: "The operator instead persists a **`RevisionRecord`** per live revision and **recompiles from that
  record plus an explicitly-owned live overlay**".
- L386: "**the compiler emits R1's filter from the recorded allowlist**"; L368: "the comparator's applied
  operand for non-spec provenance is `resolvedInputs`".
- The `PolicyIntent` schema at L23–40 has **no** `behaviourProjection`, no `resolvedInputs`, no
  `sealedBindings` and no overlay field. Either the intent gains them, or `Compile` is not the seam that
  recompiles, or it is not pure.
- **Fix**: decide the seam and write one input type that carries it.

**A-14. The KG requiredness predicate makes its own first clause vacuous.**
- L244: "KG | **the binding is the Agent's sole knowledge source** (design 01 A5), **or** the Agent
  declares any KG binding at all (A32)".
- The second disjunct subsumes the first completely. Two lines earlier L235 already states the total
  rule: "**a binding an Agent declared is required.**"
- **Fix**: one row — every declared KG binding is required — and delete the sole-source clause (whose
  cross-design citation is also wrong, G-14).

**A-15. §3.3's recovery rule enumerates the transactions and omits one.**
- L213: "the transaction is **recomputed** … then entered at *its own* first stage: **`PreparingRoute`
  for a `Create`, `ApplyingBackends` only for a proven `Loosen`**."
- `Withdraw`'s first stage is `Withdrawing` (L409). A stale-digest recovery on a `Withdraw` has no
  defined entry point, and the failure mode is exactly the one A26 exists to prevent: attaching a
  tightened policy to a route whose weight was never dropped.

**A-16. `status.apply` is a closed five-field tuple and the design requires a sixth.**
- L209: "`status.apply` carries `{transaction, digest, generation, stage, deadline}`".
- L215: "**The route identity the transaction uses is persisted with the stage**, so recovery knows which
  route it must prepare."
- There is no route-identity field in the tuple, and L215's whole argument rests on it.

**A-17. §5's crash row argues safety from the ordering §3.3 replaced.**
- L684: "Partial apply (crash mid-sequence) | Re-entrant: next reconcile resumes ordering; **routes were
  last**, so no fail-open window existed".
- Routes are created **first** (L202). The safety property survives — the route is inert, with no
  `backendRefs` — but the stated reason is the pre-A15 one, and an implementer checking the invariant
  will check the wrong thing.

**A-18. §8's property test is the shape §3.3.1 spends four paragraphs rejecting.**
- L289: "An earlier draft answered that with 'for every registered class, one test per mandatory entry' —
  **cases generated from the registry, which deletes its own check along with the entry**."
- L291: "So the security cases are a **static catalog**, written independently of the registry".
- L708 (§8): "Property tests: … **every `RouteClass` has a registry entry**; and each entry is pinned
  behaviourally".
- "Every `RouteClass` has a registry entry" enumerates from production data and passes when a class and
  its entry are deleted together. §8 also never names the static catalog or the mutation harness §3.3.1
  mandates.
- **Fix**: §8 must name the catalog, the two-direction check, and the mutation harness. This is the one
  finding in this inventory that the repo's own test doctrine (AGENTS.md rule 1) would reject on sight.

**A-19. The rate floor rule and its worked example compare different quantities.**
- L661: "**A rate below 1 token/second is a compile error.** `usdPerDay: "0.50"` against a model priced
  at `75.00`/M tokens with two gateway replicas floors below 1 token/second — **a bucket that denies
  everything**. The test is `rate < 1`, not `rate == 0`".
- The emitted quantity is per **hour** (L643: `tokens = floor(tokensPerDay / (24 × gatewayReplicas))`,
  `unit: Hours`). The example computes `tokensPerDay = floor(500000 × 10⁶ / 75 000 000) = 6666`, then
  `tokens = floor(6666 / 48) = **138`. 138 is not below 1, does not deny everything, and
  `test/conformance/crd_contract_test.go:185–192` pins the CRD's `minimum: 1`, which 138 clears.
- Read as a per-second test, the rule rejects every budget under 86 400 tokens/day — a large class of
  lawful budgets — on the false ground that they "deny everything".
- **Fix**: state the guard on the **emitted int32** and rewrite the example so it actually trips it.

**A-20. The card route both "inherits" nothing and is emitted with the serving route's auth.**
- L277: "**only emitted when `expose.a2a` is set.** It then carries the same `-auth` as the serving
  route, except at `expose.a2a.auth: none`, which drops it for both together" — and, in the same cell,
  "An earlier draft said 'inherits', which made the default Agent fail to compile a route whose parent
  did not exist."
- The surviving rule and the retracted one are one sentence apart in one table cell; the cell is 180
  words and states four separate rules plus two ADR corrections.

**A-21. The naming rule's collision outcome is stated twice, differently.**
- L181/L183: "deterministic, **collision-checked** … a suffix collision is **a compile error naming both
  inputs**, never a silent reuse."
- `internal/controller/runnamespace.go:117`, which imports this rule: "**A collision is still detected,
  by the binding record**, never silently reused" → `RunNamespaceUnavailable=NameCollision`.
- Two mechanisms for one rule; the shipped one is not a compile error and cannot be, because the name is
  computed before anything is compiled.

**A-22. §3.4.1's table of arms and §3.4.1.1's are different tables.**
- L495 and L532 name **eight** arms (`openai azureopenai azure anthropic gemini vertexai bedrock custom`).
- L507–513's instance-field table — the one the endpoint-identity tuple is built from, introduced as
  "Verified in the v1.4.1 `AgentgatewayBackend` schema" — lists **five** rows and omits `azure` and
  `gemini`. See G-6.

**A-23. "Nothing is stored while a value can still move" versus the recovery path that recomputes it.**
- L65: "**Nothing is stored while a value can still move**".
- L692 (§5): "Sealed value held **by reference**, object missing, and the **current** resolution hashes to
  the recorded digest | **Recreated from the current resolution.**"
- L380: "the comparator must **not** fall back to comparing the current value against itself".
- The §5 row is right and the two rules are reconcilable (the digest arbitrates), but the body never says
  so, and the three sentences sit 300 lines apart.

**A-24. The charter gate enumerates the reconcile inputs and the body adds five it does not list.**
- L16 (§2): "**Stateful deps**: none added. **Primitives**: **Resource (CRD) in/out.** ✓"
- The body adds: a **cluster-wide** watch on `HTTPRoute`, `AgentgatewayBackend` and
  `AgentgatewayPolicy` (L172); a watch on **Events** on every Gateway it emits to (L219); a **read-only
  watch on the gateway Deployment** (L689); and **two ConfigMap** reads per compile (L586). L153 concedes
  one of them in the body and nowhere else: "§5 has the operator observing the gateway Deployment
  read-only, **which is a cluster-wide watch**. … The claim is *not* that the blast radius is zero."
- "Stateful deps: none added" is true and is not the claim at issue; "Primitives: Resource (CRD)
  in/out ✓" is presented as the complete input set and is not. Design 02's §2 was corrected on exactly
  this shape at its A62 (it had denied operator state that §3.2 created).
- The Deployment watch's own scope is stated twice and differently: L153 "**which is a cluster-wide
  watch**" versus L689 "observes `Deployment/<release>-agentgateway` **in the release namespace**". The
  cost argument at L153 only holds for the first; the second is a single-object watch and costs nothing
  worth conceding.

---

## B — STALE STATUS

**B-1. L135 — "`values.yaml` no `gateway` key".**
> "The chart is to carry `gateway.enabled` as the agentgateway subchart's `dependencies[].condition` in
> `Chart.yaml` — **neither exists yet**: `Chart.yaml` has no `dependencies:` block and **`values.yaml` no
> `gateway` key**"

`charts/plume/values.yaml:87–89` ships `gateway: {enabled: false, name: plume}`. The `Chart.yaml` half is
true. **Fix**: state the rule (the switch is the subchart's condition, so switch and install cannot
diverge) and the one thing still owed (the `dependencies:` block, design 07).

**B-2. L147 — "`llm.egressAllowlist` mints no revision today, and must."**
> "today it is **neither classified nor projected** … the projection and the classification rule are
> **design 02 A16, now drafted**."

Both landed. `internal/revision/leaves.go:224–235` classifies thirteen `EgressAllowlist` leaves as
`mints`; `internal/revision/revision.go:372–378` projects the set, sorted. The whole paragraph is a
work order for a job that is done, and it sits in §3.1 as if it were a rule.

**B-3. L147 — "A12's `TestEveryFieldIsClassified` did not catch it because it reflects over `AgentSpec`."**
Still true of that test, and no longer the enforcement. The leaf guarantee is
`internal/revision/leaf_test.go:206 TestEveryAgentSpecLeafBehavesAsClassified` over `leaves.go`'s
`Leaves()` walk, plus `test/envtest/leaf_admission_test.go`. **Fix**: name the test that enforces it, or
drop the sentence — it is design 02's rule and design 02 now states it.

**B-4. L281 — "Design 02 A12's `TestEveryFieldIsClassified` works only because it reflects over a struct
the compiler cannot avoid changing."** Same defect, in the sentence that justifies design 03's emit
registry. The analogy is still sound; the test named is not the one that carries it.

**B-5. L612 — "Neither is implemented yet — `config/crd/plume.dev_agents.yaml` currently has a bare
`type: string`."** Verified true (`config/crd/plume.dev_agents.yaml:85`, and the chart copy at
`charts/plume/crds/plume.dev_agents.yaml:85`). Keep the fact; move the status latch. Note the generated
description at both sites still cites **ADR-0020**, which ADR-0028 supersedes.

**B-6. L582 — "Owed to `make conformance-cluster`, where the harness already exists."** Correct
(`Makefile:59`, `hack/conformance-cluster.sh`, `test/conformance/cluster_test.go` behind `//go:build
cluster`). This is the right register and should survive; it is listed here only because it is the
single "owed" line in the body written against a target that exists.

**B-7. L708/L718/L722 — §8 has no row for the layer that shipped.**
> "| e2e | **nothing** | needs a data plane |" and "**Until then design 03 has no e2e coverage at all.**"

`test/conformance/` exists, runs in `make test` (`Makefile:73`), and pins six of this design's upstream
premises against a **vendored, digest-pinned** v1.4.1 chart — `TestLocalRateLimitShape` (the exact `unit`
enum, `minimum: 1`, the `ExactlyOneOf(requests,tokens)` CEL semantic, the doc comment ADR-0028 rests on),
`TestNoEgressFieldExists`, `TestAzureOpenAIHasNoModelField`, `TestMCPAuthorizationBounds`,
`TestFourCRDsShip`. `cluster_test.go` adds seven measured cases including
`TestNackRetainsTheOldConfigAndReportsConverged` and `TestPolicyOnAbsentRouteIsAcceptedButNotAttached` —
the two measurements §3.3.2 and §3.3.3 are built on. §8.1's three-layer table omits all of it.
**This is the most consequential stale status in the body**: it tells a reader design 03 has no
enforcement when its riskiest external premises are the best-pinned things in the repo.

**B-8. L556 — "verified in the shipped chart and pinned by `test/conformance`."** Correct
(`crd_contract_test.go:292–306`). Keep, and generalise: §8 should say which claims that suite pins.

**B-9. L293 — "`test/docs/superseded_test.go`'s `ruleFixtures` is the working precedent in this repo."**
Correct and current. Keep.

**B-10. L295 — "this repo runs `go vet` with no `.golangci.yml`."** Verified (`Makefile:44`, no
`.golangci.yml`). Correct today; it is a repo-state assertion inside a design and will rot.

**B-11. L143 — "`charts/plume/templates/` has no `NOTES.txt` today."** Verified true. Keep the owed item,
drop the "today".

**B-12. L50 — "this lives in `status.revisions[]` on the Agent, written by the operator."**
Present tense, no disclaimer. Design 02 §5 carries the honest row — `02:443`: "`status.revisions[]` |
**Not on the CRD.** The field is this design's and its contents are design 03 §3.1's; **nothing writes or
reads it until the compiler exists**." Design 03, which owns the contents, says nothing.

**B-13. L586/L622 — the pricing ConfigMaps are described as chart-shipped, in the present tense.**
> "**Two ConfigMaps** … `plume-model-pricing`, **chart-owned and versioned** … The table is
> **chart-shipped** with `updated` baked in"

`grep -r plume-model-pricing charts/ config/ internal/` returns nothing. Neither map exists, nothing is
owed to design 07 for them, and §5 has no row for "the table is not installed".

**B-14. L3 (header) — the status line is now accurate and the Research line is not.**
The amendment range (A1–A50) and the three-round blocker counts (6, 5, 5) check out against
`reviews/03-a46-a50-critique.md`. The **Research** line names only
`agentgateway-v1.4.1-2026-08.md`; the body's load-bearing measurements come from
`agentgateway-v1.4.1-spike.md` (§2.1, §2.6, §2.7, §2.8) and `agentgateway-otlp-attributes-2026-09.md`
(§5), neither of which the header names.

---

## C — NARRATED RETRACTION

Every item is the body arguing with a previous version of itself. In a consolidated body each collapses
to the rule that holds plus, where the *reason* is load-bearing, the counter-example — with the history
moved to §11. Marked **[keep the counter-example]** where deleting the story deletes the argument.

| # | Line | Quote (opening) | Collapses to |
|---|---|---|---|
| C-1 | 43 | "An earlier shape carried `revisions[]` alongside a **single** `budget`, `expose`, `tools` and `llm` at Agent scope." | **[keep the counter-example]** — "a pure compile either rewrote R1's serving policy before R2 was gated … or preserved R1 and could not compile R2 at all" is why the unit is a revision |
| C-2 | 47 | "An earlier rule froze the whole applied `ResourceSet` under the revision hash. That was too broad in one direction and too narrow in the other" | **[keep the counter-example]** — "the set also encodes policy-surface fields and non-spec resolution state that are *supposed* to change" |
| C-3 | 82 | "**The earlier claim** — that a shape change simply changes the hash, so no version is needed — holds for new candidates and not for the **retained** ones." | **[keep]** — the retained-R1-across-upgrade case is the whole argument for `schemaVersion` |
| C-4 | 85 | "*not* 'all three roles' (A44). History entries and design 20 holds are not roles, and they are exactly why the bound stopped being three" | the rule: a record dies when its revision leaves the retained set |
| C-5 | 124 | "Suppressing the per-Agent condition entirely **was right for the noise it avoided and wrong for what it erased**." | **[keep the counter-example]** — the Ready=True-with-no-tools walk |
| C-6 | 130 | "**Read literally, the two rules above plus §3.3.1 say**: … every Agent fails to compile, in the phase being implemented now. **That is not the intent.**" | the two-branch rule (not emitted vs compile error); drop the self-diagnosis |
| C-7 | 147 | "**`llm.egressAllowlist` mints no revision today, and must** (Codex review BLOCKER 1…)" | delete — done (B-2) |
| C-8 | 151 | "**This reverses a review resolution, and says so.** `reviews/03-review.md` R2-a required both that…" | **[keep the counter-example]** — "the drift is made **visible** instead of corrected" is a real trade a reader must weigh; drop R2-a |
| C-9 | 157 | "This section is where that becomes implementable, **and until now it said neither**." | delete |
| C-10 | 166 | "**The line below said 'SSA with ownerRefs'**, and under A42 that is not available" | **[keep the counter-example]** — `OwnerRefInvalidNamespace` silently leaking every resource |
| C-11 | 179 | "**R1 is closed by source, and the answer is worse than 'undocumented'**" | **[keep]** — the randomly-seeded `HashSet` fact is the whole justification; drop "R1" |
| C-12 | 183 | "**The suffix was 8 hex.** 32 bits is not enough for a name that is also a security boundary, which is the lesson A37 recorded" | **[keep the counter-example]** — 16 hex, and why: a run-namespace collision pools two tenants' material |
| C-13 | 196 | "**An earlier draft polled for up to 30s *on the reconcile worker*** … Raising `MaxConcurrentReconciles` changes how many bad objects are needed, not the outcome." | **[keep the counter-example]** — worker starvation is why the barrier is a state machine. **CI trap: see §H** |
| C-14 | 211 | "**An earlier draft ordered Backends → Policies → converge → *then* create the route.**" | **[keep the counter-example]** — the cold-create deadlock is why `PreparingRoute` exists; and it is the sentence that must replace L187–192 (A-1) |
| C-15 | 213–215 | "**An earlier draft restarted unconditionally at `ApplyingBackends`. A19 removed the `Tighten` transaction and I recorded that as dissolving this finding; that was wrong, and stated here because the retraction matters more than the fix.**" | **[keep the counter-example]** — the superseding-generation walk; delete the first-person autopsy |
| C-16 | 225 | "§4's totality rule does not cover this either" | the rule: a required concern with no input is a compile error |
| C-17 | 227 | "**Describing the unit as 'one Agent' survived A26 and obscured exactly that isolation.**" | the rule: the unit is one revision; a candidate's failure never touches the active revision |
| C-18 | 231 | "**An earlier draft made every tool, KG and LLM route *optional*, so an Agent whose sole knowledge source was unreachable reported `Ready=True`.**" | **[keep the counter-example]** — "answering HTTP while every declared capability is gone is not semantic readiness" |
| C-19 | 233 | "**The table below once said a binding was required when it 'is marked required'. Nothing marks it**" | **[keep the counter-example]** — two cells in an identical shape is why declaration is the rule |
| C-20 | 237 | "**inventing that meaning is how the optional column got read as the default**" | the rule: declaration is the demand |
| C-21 | 256 | "**An earlier draft gave every serving-required failure the reason `PolicyApplyIncomplete`, which would have named a cause that was never checked**" | the two-cause split and the precedence |
| C-22 | 277 | "**An earlier draft cited [ADR-0019] for this and was wrong** … An earlier draft said 'inherits', which made the default Agent fail to compile" | **[keep the counter-example]** — a `visibility: cluster` agent leaking its skill inventory; split the cell (A-20) |
| C-23 | 279 | "**two absences look identical**" | **[keep]** — the reason `auth: none` is a label, not an empty policy |
| C-24 | 281 | "**An earlier draft promised 'a property test asserts that the set of route-class constants equals the set of table rows' — which is not mechanizable**" | **[keep the counter-example]** — "two Go slices in one file that stay green when you delete from both" |
| C-25 | 285 | "**An earlier draft scoped them to `AgentgatewayPolicy` kinds, which cannot express the one control ADR-0014 rests on**" | **[keep the counter-example]** — the `hipaa` cluster emitting an unrestricted LLM route |
| C-26 | 287–289 | "**What actually pins the sets, and the shape it must NOT take** … That is the identical defect this repo has now shipped twice" | **[keep the counter-example]** — "cases enumerated from production data cannot pin production data" is the strongest sentence in §3.3.1 |
| C-27 | 295 | "**An earlier draft claimed 'deleting from both fails the exhaustive switch over `RouteClass` in Go': Go has no exhaustive switch checking**" | the rule: profiles ship their own catalog; keep the Go fact as a footnote |
| C-28 | 301 | "**`Accepted=True/programmed` was not a predicate; the slash hid a missing contract**" | the tuple; **[keep]** the `http-route-missing-not-attached.yaml` fixture |
| C-29 | 313 | "**An earlier draft offered 'or accept that a silent NACK leaves the old rule serving', which was a measured fail-open rather than an option; its replacement, a witness probe, could not attribute its own result. Both are withdrawn.**" | **[keep the counter-example]** — "absence of an Event is not success" |
| C-30 | 315 | "**Update ordering was part of the contract, and A19 removed the case it governed.**" | delete; the surviving rule is in §3.3.3 |
| C-31 | 319 | "A17 specified a `Tighten` transaction — quiesce the live route, converge, prove a witness, republish. **It is withdrawn.**" | **[keep]** the three reasons; drop A17. **CI trap: see §H** |
| C-32 | 322 | "A 401 can come from the backend, a rejected tool from an unhealthy MCP server … (**Codex r4 BLOCKER 2**)" | **[keep the counter-example]** — the un-attributable witness is why only positive witnesses count |
| C-33 | 323 | "**A17's escape for an unwitnessable concern — 'use the revision path' — was unavailable for exactly the fields that needed it (r4 BLOCKER 4).**" | delete; A25 moved the fields |
| C-34 | 345 | "**The earlier ordering compared `expose.a2a.auth` as an enum**, so two OAuth policies admitting disjoint principal sets both normalized to `oauth`" | **[keep the counter-example]** — the tenant-widening IdP move |
| C-35 | 349 | "**Comparing raw presence instead would read a field gaining its default as a change**" | **[keep]** — normalization is part of each comparator |
| C-36 | 353 | "(**Tenant consumer budgets were a fourth until A48 removed them**: a tenant's shared limit is not an Agent-scoped input.)" | delete |
| C-37 | 355 | "**The earlier one had a row for non-spec *tightening* and none for non-spec *widening*, and the missing cell inherited the live path**" | **[keep the counter-example]** — the `wire_funds` walk is the reason the table is total |
| C-38 | 364 | "**The rule said the compiler emits R1's filter from 'the recorded allowlist' and called it A28's persistence rule applied to a new case. It was not**" | **[keep]** — "no non-spec producer output was persisted anywhere" |
| C-39 | 370 | "**Freezing requires something to have been gated, and until A46 it could freeze an absence.**" | **[keep the counter-example]** — "The Agent stayed `CapabilityUnavailable` for as long as it existed" |
| C-40 | 372 | "**Reaching the apply machine's `Served` stage is not the trigger and cannot be**" | **[keep the counter-example]** — route-scoped failure reaching `Served` unresolved |
| C-41 | 386 | "**This is not a new mechanism — it is A28's persistence rule applied to the case the old table let slip past it.**" | delete; it restates L368 (F-4) |
| C-42 | 390 | "**A 'policy-input revision' was a noun and a hash, not a lifecycle (A29).** An earlier row gave non-spec tightening a content-addressed candidate and said it was 'gated like any other'. Nothing supported that." | **[keep the counter-example]** — the six missing lifecycle stages are the argument for withholding |
| C-43 | 394 | "**A29 argued the general case from one example** … That reasoning is sound *for that producer* and was then applied to a generic row where it is false." | **[keep]** — "a condition is observability, not enforcement" |
| C-44 | 403 | "**Written as 'the route's weight goes to zero' it collided with three rules in this same body**" | **[keep the counter-example]** — the 1,000-vs-1 req/hour tenant; drop the three-way collision autopsy (and fix A-2) |
| C-45 | 413–418 | "**The stage list above says … and then applies the tightened policy to what it calls a non-serving route.** … An earlier draft of this amendment deferred it on the ground that nothing could attribute a receipt to a route, **and a primary-source pass disproved that**" | **[keep]** the reduction and the positive-witness rule; delete the draft autopsy (and fix A-5, A-6) |
| C-46 | 431/435 | "**That is the identical loud-and-wrong outcome A34 says it removed, relocated from the Backend to the route.**" · "**Two incompatible ones were authoritative at once**" | **[keep the counter-example]** — a recompile mid-incident is the live apply that can NACK |
| C-47 | 444/446 | "**A43 required 'a distinct instance per replica' and that requirement is withdrawn** … **An earlier row gave rollback 'the reverse weight shift, gated on the primary's drift clearing' and no canary**" | **[keep]** the two reasons and the reverse-shift postcondition; delete the draft history (and fix A-5) |

Six more that read as retraction and are really **forward** rules that must survive:
L279 (explicit opt-outs are emitted, not absent), L291 (the static catalog), L351 (Unknown ⇒ Tighten,
including first application), L368 (`resolvedInputs` is the applied operand), L382 (the laundering
escape hatch, named rather than hidden), and L421 (failing to restore traffic is the safe direction).

Also stated as retraction and correctly so, but needing a positive restatement because the CI gate
rescues them on the retraction word: L489 ("was specified as compiling to 'a Backend restriction'"),
L534 ("A11 said the emitted Backend's provider set must *equal* the allowlist"), L546 ("`Mcp-Name`
header matching was the mechanism when the superseded note was written"), L568 ("An earlier draft
asserted a native `auth → rate-limit → guards` order"), L590 ("An earlier draft keyed this on
`<provider>/<model>`"), L610 ("An earlier draft derived the true threshold … and then wrote a *seven*-digit
grammar"), L614 ("That draft canonicalized `{provider, model}` with a `/`"), L646 ("earlier drafts
assumed"), L650 ("'Cuts early, never late' is withdrawn"), L652 ("An earlier draft published
`gatewayReplicas x maxOutputTokens(model)`").

---

## D — HISTORY LEAK

### D-0. The blanket measurement

The body (lines 1–734) carries **197 `A<n>` tokens on 138 lines**. Stripping legitimate cross-design
citations (`design 02 A24`, `design 07 A5`, `design 25 A1`, …) leaves **177 bare design-03 amendment
tags on 131 body lines** — better than one in six body lines. **Forty-five distinct amendment numbers**
are cited, the heaviest being A46 (10), A37 (9), A19 (8), A38 (8), A11 (7), A24 (6), A44 (6), A48 (6).

Body lines carrying a bare design-03 amendment tag:
3, 23, 26, 34, 38, 39, 43, 47, 50, 54, 59, 66, 77, 79, 81, 82, 85, 87, 99, 104, 113, 116, 121, 124, 126,
147, 151, 157, 166, 173, 175, 181, 183, 185, 196, 213, 215, 219, 223, 227, 231, 233, 244, 246, 256, 275,
281, 287, 295, 299, 311, 313, 315, 317, 319, 323, 325, 334, 338, 345, 347, 353, 355, 361, 364, 366, 370,
372, 378, 380, 386, 388, 390, 394, 399, 403, 409, 411, 413, 415, 418, 421, 425, 427, 429, 431, 433, 435,
442, 444, 445, 446, 460, 465, 468, 469, 475, 478, 482, 483, 485, 487, 499, 501, 503, 505, 530, 534, 544,
566, 584, 586, 588, 590, 614, 616, 620, 635, 646, 652, 663, 674, 681, 685, 689, 694, 700, 710, 717, 720,
733.

**Ten of the body's twenty-five section headings carry an amendment number**, so the table of contents is
itself an amendment log: §3.3 (A15), §3.3.1 (A1), §3.3.2 (A10), §3.3.3 (A19), §3.4.1 (A11), §3.4.1.1
(A16), §3.4.2 (A11), §3.4.3 (A20), §3.5 (A4), §8.1 (A2). Design 02's consolidated body carries none.

**`A44` cannot resolve.** §11 defines it twice — `03:835` ("`RevisionRecord` GC keys on the retained set")
and `03:836` ("this design now knows where its resources live"). The body cites bare `A44` at L85 (the
first) and at L157, L166, L168 and L175 (the second). No reader can tell which.

### D-1..D-28. Named sites — a round, a reviewer, a spike or an amendment as the *subject* of a sentence

| # | Line | The leak |
|---|---|---|
| D-1 | 3 | "**A46–A50 (2026-09-04) answer Codex r8's BLOCKERs 10–13 and MAJOR 6 and are NOT closed** — three critique rounds … returned REVISE with 6, then 5, then 5 blockers" |
| D-2 | 3 | "**The reviewer's judgement, which this design records rather than argues with**" |
| D-3 | 10 | "apply-time behavior is part of this design, not a detail (**r1 finding 1**)" |
| D-4 | 15 | "exact enforcement is the receipt backstop on existing Postgres (**r1 finding 3**)" |
| D-5 | 147 | "(**Codex review BLOCKER 1**, `reviews/02-codex-review.md`) … **a direct differential mutation (ledger M5)** confirmed" |
| D-6 | 151 | "**`reviews/03-review.md` R2-a required both that** …" |
| D-7 | 179 | "**Why one-concern-per-policy** (**r1 finding 2**, honest version)" and "**R1 is closed by source**" |
| D-8 | 181 | "**Naming** (**r1 f9**, widened by A44)" |
| D-9 | 211 | "**The spike measured** that a policy whose target route does not exist reports `Attached=False` (§2.1)" |
| D-10 | 215 | "**A19 removed the `Tighten` transaction and I recorded that as dissolving this finding; that was wrong**" — first person, and a review round as the subject |
| D-11 | 219 | "**A NACK is the sixth input, and it is not optional** (§3.3.2, A14)" — an amendment as the subject of a rule heading |
| D-12 | 233 | "**Requiredness is DECLARATION, not a marker (A32).** The table below once said…" |
| D-13 | 289 | "**That is the identical defect this repo has now shipped twice: once in the docs gate**" |
| D-14 | 311 | "**What this proves is control-plane convergence, and not enforcement — measured, not argued (A14)**" |
| D-15 | 313 | "the **witness probe** … Both are withdrawn" |
| D-16 | 322 | "`Publishing` then widened the match on a false positive (**Codex r4 BLOCKER 2**)" |
| D-17 | 323 | "because a policy-surface change mints no candidate (**r4 BLOCKER 4**)" |
| D-18 | 332/334 | "**`Loosen` needs a comparator, and 'admitted set' is not one (A24)**" |
| D-19 | 355 | "**The table is TOTAL over provenance × comparator result (A37).** The earlier one had a row for…" |
| D-20 | 364 | "**'Frozen' needs somewhere to freeze FROM, and until A41 there was nowhere (A41)**" |
| D-21 | 370 | "**Freezing requires something to have been gated, and until A46 it could freeze an absence**" |
| D-22 | 403 | "**'Fail closed' is a THIRD TRANSACTION, not a weight patch (A42).** Written as 'the route's weight goes to zero' it collided with three rules **in this same body**" |
| D-23 | 413 | "**`Withdraw` withdraws at the control plane, and cannot prove the dataplane followed (A49).** The stage list above says…" |
| D-24 | 418 | "**An earlier draft of this amendment** deferred it … and **a primary-source pass** disproved that" |
| D-25 | 444 | "**A43 required 'a distinct instance per replica' and that requirement is withdrawn** … **An earlier draft of this amendment** said both fields had no producer" |
| D-26 | 459–485 | §3.4's Notes column is a review-finding index: "r1 finding 6", "design 13 r1 f4", "design 06 §3.3 (r1 f1)", "design 04 §6 (R2-b)", "design 05 review f1", "design 11 r1 f5", "designs 11 f2 / 01 A2", "design 16 r1 f1", "design 17 r1 f3/f4", "design 22 r1 f4", "design 25 r1 f2/f5", "design 23 r1 f2", "design 10 r1 f1", "design 02 review f2" — **14 of 31 rows** |
| D-27 | 499 | "which is **Codex BLOCKER 6's substance**" |
| D-28 | 708/722 | "**plus (r1 f11)**" and "**R1's same-level/same-field overlap test**" |

§3.4's Notes column deserves the same call design 02's inventory made about its failure-mode table: a
concern-mapping table is what an implementer reads first, and a reader wiring `-exchange` does not need
to know it was design 06's r1 finding 1.

---

## E — UNENFORCED CLAIM

Scoped to claims that name a *mechanism* — the chart, admission, a flag, a test, a status field — where
the mechanism is absent and the body carries no disclaimer. Claims that are simply unimplemented because
design 03 is unimplemented are **not** listed; the body says so in its header. These are the ones where
the body names a producer that does not exist and does not say so.

**E-1. `status.apply` is required, declared nowhere, and owed to nobody.**
- L209: "`status.apply` carries `{transaction, digest, generation, stage, deadline}`"; the whole staged
  reconcile (L200–215), the recovery rule and §5's crash row read and write it.
- It is not in `api/v1alpha1`, not in design 02 §3.1 (the authoritative schema), and — unlike
  `status.revisions[]` — not in design 02 §5's "stated but not enforced" table. A status field with no
  owning schema and no owed item.

**E-2. `status.llmFallbackActive` is the same shape.**
- L111: "`status.llmFallbackActive`, **controller-set by design 20 §3** — status, not spec, so it mints no
  revision by design | **yes**".
- The string does not appear in `api/v1alpha1` or anywhere in design 02. Its "Available at P1? **yes**"
  is false in the only sense that matters — nothing can set it.

**E-3. The pricing ConfigMaps are stated as shipped.** See B-13. §3.5's entire arithmetic, `PricingStale`,
the two-writer argument and the load-error rules all rest on two objects nothing renders.

**E-4. `gatewayReplicas` — the divisor on every rate limit — has no producer.**
- L114: "`gatewayReplicas` | chart value **`gateway.replicas`** → operator flag
  **`--gateway-replicas`**, default `1` | **yes**".
- `charts/plume/values.yaml`'s `gateway:` block has `enabled` and `name` only.
  `cmd/operator/main.go:62–69` defines `operator-namespace`, `metrics-bind-address`,
  `health-probe-bind-address`, `leader-elect` and `eval-suite-check-interval`. No replicas flag.
- L648 compounds it: "**`gatewayReplicas < 1` is likewise rejected at the flag**, not discovered by
  dividing by zero."
- L149 makes the value load-bearing: "It is the divisor on every emitted rate limit (§3.4), so a wrong
  value **under-enforces by exactly the factor it is wrong by**."

**E-5. §6 claims an admission control the chart does not render.**
- L700: "Emitted policies are the only capability-change path (**admission denies non-operator writes on
  plume-labeled gateway resources**)."
- `charts/plume/templates/admission.yaml:88` matches `resources: ["httproutes"]` only, and keys on
  `parentRefs` naming the plume Gateway, not on a plume label. `agentgatewaypolicies` and
  `agentgatewaybackends` are covered by nothing. So the two kinds that carry the *capability* — the
  auth policy and the tool filter — are exactly the two the claim does not reach.

**E-6. "Fairness is a test, not an assertion" names no test and states no bound.**
- L221: "More permanently-pending Agents than workers, while an unrelated Agent's kill transition must
  complete **within a stated bound**. A design that merely says it does not block has not shown it."
- The bound is not stated anywhere, and §8/§8.1 do not list the test.

**E-7. The deadline has no value and no owner.**
- L205/L209/L217: `Converging` advances on "`RequeueAfter` for the deadline"; `status.apply` carries a
  `deadline`; "Exceeding it sets `PolicyApplyIncomplete`". Nothing says what the deadline is, who sets
  it, or whether it is configurable. Compare `registrationDeadline` in design 02, which at least has a
  stated default.

**E-8. Two "load error" paths raise no condition — NFR-8 silence.**
- L606: "Two rows matching on the same field count is a **load error**, not a runtime tie-break".
- L668: "**An identity appearing in both is a load error**, never a precedence rule".
- Neither names a condition, and §5 has no row for either, nor for "the pricing table is not installed".
  A degraded path with no condition is exactly what NFR-8 exists to stop, and §3.5 elsewhere is careful
  about this (`PricingStale`, L620).

**E-9. The rate floor guard cannot be implemented from what is written.** See A-19.

**E-10. "the collision check stays regardless: a suffix collision is a compile error naming both inputs"
(L183)** — nothing computes or records a name-collision set. The one shipped consumer of this rule
(`internal/controller/runnamespace.go:117`) detects the collision by a *binding record*, not a compile
error, because the name is computed long before any compile.

**E-11. "the compiler enforces [max 8 groups × 16 providers], and a compile error past it" (L491)** —
correct against upstream (`docs/research/agentgateway-v1.4.1-2026-08.md:392`) and enforced by nothing;
§8's golden-file list does not include the group bound.

**E-12. "Golden fixtures pin 0, 1, 256 and 257 tools, and names carrying quotes, backslashes, slashes and
non-ASCII" (L564)** — stated in the present tense; no such fixtures exist. §8.1's unit row does not name
them either.

**E-13. "golden-file fixture at max length" (L181)** — same shape; §8 repeats it ("Golden files (incl.
max-length names…)") and nothing exists.

**E-14. "Property tests … for reflexivity and transitivity, plus every boundary" (L351)** and "Each field
is property-tested **independently**" (L347) — the `-auth` normalization has six components (L347) and
no producer for five of them at P1 (identity is "Available at P1? no"), so the mandated per-field
property test cannot be written for most of the fields it names, and the body does not say so.

**E-15. "the mandatory header denylist" (L472)** is named as the content of a *mandatory* concern
(`-transform` on the LLM egress route, L275) and is defined nowhere in this design. A mandatory concern
whose predicate has no definition cannot be checked before the route is created.

**E-16. `expose[].protocol` has no producer.**
- L38: `expose: [{protocol, visibility, auth}]`; L112: "`expose[].protocol/visibility/auth` |
  `spec.expose.a2a` | yes".
- `api/v1alpha1/agent_types.go:334–347`: `ExposeSpec` has one optional field, `A2A *ExposeProtocol`, and
  `ExposeProtocol` carries `Visibility` and `Auth` only. The protocol *is* the arm name; there is no
  field to read, and no second arm for the list shape to hold.

**E-17. "a golden test renders the registry to markdown and diffs it against the table above" (L287)**
versus L281's own rule that "a markdown table is not a machine-readable artifact". Diffing against the
table requires parsing the table. Either the design has a markdown-extraction contract (it does not say
so) or the golden test compares the rendered registry against a checked-in fixture, which is a different
and weaker claim than "the table is machine-checked".

---

## F — DUPLICATE

Same rule, two or more places, different wording. Left column is the site the rewrite should keep.

| # | Keep | Also at | The rule |
|---|---|---|---|
| **F-1** | — | **70 · 77–81 · 87–93 · 374–380 · 691–694** | **What the record holds, when it freezes, and what the digest covers.** The round-3 critique's headline finding, confirmed: five statements, no two agreeing. L70 says six fields; L78 says every field. L81 says the bound is one MiB; L91–93 say 4 KiB with no object for two of three modes. L79 says the seal is a transition evaluated once; L66 gives it no durable marker. L379 says an unsealed never-active revision "carries no production traffic"; design 02 §3.3 step 4 says the canary does. **The rewrite must state this rule once, in §3.1, and let §3.3.3 and §5 cite it.** |
| F-2 | 137–141 | 685, 687, 688, 690 | the `(gateway.enabled × CRDs)` table, restated as three §5 rows |
| F-3 | 143 | 141, 145, 687, 690 | `GovernanceSkipped=GatewayDisabled` is a distinct type, `Ready` is not withheld, and it is loud (condition + NOTES + doctor) — five sites |
| F-4 | 368 | 386 | "frozen means recompile from `resolvedInputs`, never from the cluster" — L386 restates it and adds "This is not a new mechanism", which is a third statement of the same thing |
| F-5 | 311–313 | 315, 321, 403, 413, 425, 431, 452 | **a NACK retains the old configuration while every condition reports converged** — eight sites, four of which re-derive the consequence from scratch |
| F-6 | 317–325 | 313, 388, 700 | there is no in-place tightening; tightening is a gated spec change |
| F-7 | 487–491 | 275, 468, 534–538, 637 | egress is Backend **construction**, not restriction |
| F-8 | 496 | 275, 542 | `dynamicForwardProxy` is never emitted |
| F-9 | 495 | 530, 616, 618 | an unresolvable entry is `PolicyCompileFailed` naming it, **never dropped** — four near-identical sentences with the same justification |
| F-10 | 542 | 637 | `internal/*` resolves inside any allowlist by construction |
| F-11 | 505–517 | 35–36, 537, 588, 614 | the endpoint identity is the tuple `{arm, instance fields, model}` and no string is joined or split |
| F-12 | 258–263 | 680–683 | `PolicyCompileFailed` vs `PolicyApplyIncomplete`, split by cause, with a precedence |
| F-13 | 81 | 85 | the retained set bounds the records (design 02 §3.3) |
| F-14 | 632 | 271, 275 | `-ratelimit` is mandatory on a **derived rate**, not on the presence of a budget field |
| F-15 | 143 | 411, 421, 696 | design 10's alerts and design 08's `deploy` stream key on the condition **type**, so a second reason cannot escalate |
| F-16 | 663–668 | 586, 622–626 | two ConfigMaps, why one cannot work, and where `updated` lives |
| F-17 | 628 | 275, 536, 635 | `P` and the emitted set are taken over `providers[] ∪ {fallback}` |
| F-18 | 652–654 | 460, 728 (D3) | the gateway tier's excess is unbounded and no figure is published |
| F-19 | 179 | 177, 660, 708, 722, 727 (D2) | one concern per policy (see A-7 for the contradiction) |
| F-20 | 219 | 261, 313, 452, 683 | a NACK raises `PolicyApplyIncomplete` and can never advance anything |
| F-21 | 79 | 66–67, 372, 694 | the seal is **per binding**, not per record |
| F-22 | 121–126 | 682, 694 | `CapabilityUnavailable`: fleet-level fact vs Agent-level contract (see A-8) |
| F-23 | 192 | 141, 170, 688 | reverse-order removal — routes detach before policies and Backends |
| F-24 | 166–170 | 168 | ownership is a label, the **name** is the deletion authority, GC is the operator's |
| F-25 | 435–448 | 429, 478 | the model-fallback mechanism: two pre-provisioned Backends and a weight shift, no recompile — stated in §3.3.3's prose, its own table, and §3.4's row |
| F-26 | 442 | 433, 446 | the canary is a **positive** witness attributed by full endpoint identity, and the reverse shift carries the same postcondition |

Two near-duplicates worth **merging rather than deleting**: L130–133's two-branch rule (not emitted vs
compile error) and L145's P1 walk-through are one rule and its worked example, and belong adjacent; and
L283's emit registry and L291's static catalog are the mechanism and its pin, currently separated by two
paragraphs of retraction.

---

## G — DEAD ANCHOR

**G-1. §2.1, §2.6, §2.7, §2.8 — unqualified, and design 03's §2 has no subsections.**
- L211: "The spike measured … reports `Attached=False` (**§2.1**) … in the shape **§2.6** measured as
  attachable-but-not-serving."
- L301: "no policy emitted at all (**§2.1**)"; L311: "eight requests … all returned 200 (**§2.8**)";
  L321: "a NACK'd policy **retains the previous configuration** … (**§2.8**)".
- Design 03's §2 is "Doctrine & charter gates", five lines, no subsections. These are the *spike's*
  sections. L425, L460 and L652 cite the same sections correctly as "spike §2.7 / §2.8"; the four above
  do not. A reader following `§2.1` lands on the charter gate.

**G-2. `internal/revision/revision.go:66` does not say what L614 claims.**
> "which `internal/revision/revision.go:66` already records as non-injective: `{azure, openai/gpt-4}` and
> `{azure/openai, gpt-4}` collided on one row"

Line 66 is `Sandbox   string \`json:"sandbox,omitempty"\`` — a struct field. The comment is at
`internal/revision/revision.go:359–362`. The *claim* is true; the line number is not.

**G-3. §11 defines `A44` twice; the body cites it five times.** See D-0.

**G-4. The header's ADR list omits the live ADR and names one the body never cites.**
- L5: "**ADRs**: 0003, 0014, 0019".
- §10 (L733): "**ADR-0028** (2026-08-27) **is the live decision**". The body cites 0014, 0019, 0020,
  0027 and 0028. **0003 appears nowhere in the body.**

**G-5. The header's interface list names three designs; the body has hard interfaces with fifteen.**
- L5: "interfaces: designs 02 (caller…), 04 (spend aggregation owner), 22 (loop governance)".
- The body binds 01, 05, 06, 07, 08, 10, 11, 13, 16, 17, 20, 23, 25, 26 and 27 as well, several of them
  as *producers of mandatory inputs* (06 for `identity`, 11 for `toolAllowlist`, 01/13 for
  `knowledge[].endpoint`). A reader routing on the header will not open the design that owns the input
  that makes every A2A route compile.

**G-6. The arm set is six in the schema and eight in §3.4.1/§3.4.1.1.**
- L495: "an allowlist entry is meaningless until it resolves to a concrete `LLMProvider` arm (**`openai`,
  `anthropic`, `gemini`, `bedrock`, `vertexai`, `azure`, `azureopenai`, `custom`**)".
- L532: "the closed set **`openai azureopenai azure anthropic gemini vertexai bedrock custom`**, verified
  in the shipped CRD".
- Both are correct about *agentgateway* (verified in the vendored CRD: eight provider arms). They are
  wrong about *plume*: `api/v1alpha1/agent_types.go:193` is
  `+kubebuilder:validation:Enum=anthropic;openai;azureopenai;vertexai;bedrock;custom` — **six**. `azure`
  and `gemini` are unrepresentable in `llm.providers`, in `llm.fallback` and in `egressAllowlist`, so the
  catalogue the design specifies carries two rows nothing can name, and an operator whose org uses Azure
  AI Foundry (the `azure` arm, `resourceType: Foundry`) cannot express it at all.
- Compounding it: L507–513's instance-field table, introduced as "Verified in the v1.4.1
  `AgentgatewayBackend` schema", omits both. The `azure` arm carries **five** instance fields
  (`resourceName`, `resourceType`, `projectName`, `apiVersion`, `model`) — so an implementer building the
  identity tuple from the design's table produces a **non-injective identity for `azure`**, which is the
  exact defect the tuple exists to prevent, for the fourth time in this lineage.

**G-7. §3.4.1.1's `egressAllowlist` example is not a valid manifest.**
- L525: "`- arm: azureopenai` / `  instance: {endpoint: acme.openai.azure.com, deploymentName: gpt4o-prod}`"
- L527: "`- arm: custom` / `  endpoint: {scheme: https, host: llm.internal.example.com, port: 8443, pathPrefix: /v1}`"
- The shipped schema keys the instance block by the **arm name** (`azureopenai: {…}`, `custom: {…}`) and
  has no `instance:`, no `endpoint:` wrapper and **no `scheme` field anywhere** (`CustomInstance` is
  `{host, port, pathPrefix}`, `agent_types.go:232–239`). Design 02 §3.1's own example
  (`02:60,62-63`) shows the correct shape.
- L537 propagates the phantom field into the predicate: "the tuple **`{arm, scheme, host, port,
  pathPrefix, instance fields, model}`**".

**G-8. `expose.llmBackend` is not a field.**
- L482: "Model serving | LLM Backend registration for **`expose.llmBackend`**".
- `ExposeSpec` has one field, `a2a`. Design 02's classification table names no `llmBackend`. §3.3.1's
  route-class table has no row for it and it is not in the "Reserved — unclassified" list, so under
  L283's registry rule it is a class that cannot be emitted and is not declared unemittable either.

**G-9. `gateway.replicas` and `--gateway-replicas`.** See E-4.

**G-10. `plume-model-pricing` / `plume-model-pricing-internal`.** See B-13/E-3.

**G-11. "Design 02 §3.3's deletion authority is a name shape, and its shape today is
`<agent>-<revisionHash>-env-<n>`" (L95) is true and insufficient.**
The shipped authority (`internal/controller/material.go`'s `isRevisionMaterial`, mutation-confirmed by
round 3) gates on **three** things: the `env-<n>` name shape, a non-empty `plume.dev/revision-digest`
annotation, and a non-empty `plume.dev/source` annotation set to `ref.Kind + "." + ref.Name`. L95
specifies the `-inputs` object as carrying **two labels and no annotations**, so extending only the name
shape leaves it failing two of three gates. The owed item as written does not close the leak it names.

**G-12. `test/conformance` is cited once (L556) and is invisible to §8.** See B-7.

**G-13. `research/agentgateway-v1.4.1-spike.md` and `research/agentgateway-otlp-attributes-2026-09.md` are
cited in the body and absent from the header's Research line.** See B-14.

**G-14. "design 01 A5" for the sole-knowledge-source rule (L244).** Design 02's consolidation inventory
recorded this same misattribution (its G-11): design 01 A5.2 covers bind validation refusing a
non-`active`/`superseded` binding; the "no traffic if sole knowledge source" half is design 03 A27 /
architecture §04. Design 03 states the correct provenance itself at L231 ("architecture §04 says … and
design 02 §5 says …") and then cites design 01 A5 for it thirteen lines later.

**G-15. The convergence tuple's stated discriminator does not discriminate.**
- L307: "The success ancestor is the Gateway, **not** the `targetRef`."
- The *synthetic failure* ancestor is also Gateway-kinded: `{group: agentgateway.dev, kind: Gateway,
  name: StatusSummary}` (`docs/research/agentgateway-v1.4.1-spike.md:52`). A predicate matching a
  "**Gateway**-shaped `ancestorRef`" matches both. The design covers it in the next clause ("its presence
  is a negative signal"), so the rule is right and the tuple cell's own wording is not implementable as
  written — it must name `G.name`/`G.ns` and reject the name `StatusSummary`.

**G-16. Design 17 depends on a per-principal budget A48 removed the only mechanism for.**
- `17-session-replay.md:57`: "Replayed agent exceeds budget | **The replay principal has its own small
  budget**; halt with `budget` — replays cannot starve prod".
- Design 03's §3.4 replay row (L477) emits "replay-principal backends → in-cluster mock Service,
  fail-closed" and no rate limit. `PolicyIntent.budget` is Agent-scoped, and per-consumer budgets were
  removed at A48 as "not Agent-scoped" (L113, L399, L469, L483). So a design-17 guarantee has no compiler
  path and design 03 does not say so. Design 17's other two claims check out:
  `17-session-replay.md:50` (card SoT, design 05) and `:40` (the mock route "recorded as a design 03 §3.4
  row now, alongside the eval temporary-grant row") both match design 03's rows exactly.

### Cross-design anchors, verified against the producing document

**G-17. `tools[].toolAllowlist` is a declared Connector field, not a tool server's advertised set — and
the whole non-spec-producer machinery rests on the opposite premise.**
- L106: "`tools[].toolAllowlist` | **the resolved tool server's advertised tool set** (design 11 §4) —
  **not** an `AgentSpec` field | no"
- `11-connector-crd.md:30` (§3, the CRD schema): "`toolAllowlist: [read_claim, search_claims]     #
  **narrows what the server offers**`" — a field on `Connector.spec.tool`.
- `11-connector-crd.md:49` (§4, the sentence design 03 cites): "design 03 emits `AgentgatewayBackend` and
  tool-filter policy **from `toolAllowlist`**".
- Design 11 contains **no** advertised-tool-set concept, no `tools/list` discovery and no catalogue. Its
  six uses of "catalog" all mean **catalog container images** (`11:10,14,16,28,58,82`).
- **This is the largest single finding in this pass, because it is the premise under A37 → A41 → A46 →
  A49.** `resolvedInputs`, the seal, the three-state table, "frozen", `PolicyInputDrifted` and the
  `Withdraw` transaction all exist because a tool allowlist is said to come from a producer that "cannot
  mint a revision" (L353: "Telling those producers to mint a revision points at a mechanism they do not
  have"). A `Connector` CR field **is** somebody's spec: an edit to it is an API write with an owner, an
  audit trail and a controller that could mint. And §3.3.3's headline failure scenario — "an MCP server
  that **begins advertising** `wire_funds` produces a superset" (L355) — cannot arise from a field the
  Connector author writes.
- A real residual survives and the design never separates it from the false one: `toolAllowlist` is
  optional, so where none is declared the server's actual surface *is* the filter's input and *can* grow
  beneath a gated revision. **Fix**: the rewrite must state which of the two cases each rule governs, and
  either design 11 gains a discovered-set concept or design 03's provenance row is corrected to name the
  Connector CR.

**G-18. `plume doctor` is design 08 **§7**, not §8 — cited as §8 three times.**
- L143, L682, L687: "`plume doctor` reports it (**design 08 §8**)".
- `08-cli.md:64` is "`## 7. plume doctor`"; §8 is the failure-mode section. The rule design 03 depends on
  is at `08:70`, inside §7, and it is **correct and reciprocal**: "`doctor` also reports the **tier
  gap** … Design 03 relies on this verb as the sole report for a whole class of un-emitted governance, so
  it is named here rather than assumed."

**G-19. The owed item at L382 is not recorded in design 16.**
- L382: "**Owed to design 16**: whether a suite must cover every tool its candidate can reach, or the
  gate's claim is narrower than it reads."
- `16-evalsuite.md` carries A1 only, about the model metric family. The debt exists on the side that
  cannot pay it. (Design 03's *premise* about design 16 is true: `16:51` — "The Job drives **real A2A
  tasks through the gateway** at the candidate header-route" — and `16:51` also confirms design 03's
  eval-principal row exactly: "the compiler adds that principal to the candidate route's **admitted set
  at Job launch, removing it at Job end**".)

**G-20. Design 11 was never told the catalogue bound, and §11 is about a different debt.**
- L81/L95 owe design 11 "a stated catalogue bound" so the one-MiB number "is the producer's rather than
  plume's".
- `11-connector-crd.md:88` §11 is "Owed to this design (2026-09-04, **from design 02 §3.1 and
  ADR-0027**)" and its subject is the absent `MCPServer` kind and the tool-name uniqueness rule — a
  different gap, which design 03 itself depends on at L105 ("resolved against a Connector facet or
  `MCPServer` (design 11)") and which `11:92` names design 03 as one of the four documents resting on a
  premise no design states.

**G-21. Design 04 A5 already says what L444 and L720 deny.**
- `04-receipt-tap.md:119`: "`hop.gatewayInstance` | `k8s.pod.name`, `service.instance.id`, `k8s.pod.ip`,
  `k8s.node.name` | **four, all default**".
- `04-receipt-tap.md:125`: "**A49's deferral now rests on the drain allowance alone.**"
- So A-5 and A-6 are not merely internal: design 03's body contradicts the **current text of the producing
  design** in both places. The rest of L444 is confirmed by `04:111–120` — the Azure `deploymentName`
  limit is real and `hop.endpoint` does carry three of four parts.

**G-22. Design 20's remediation row still specifies the check design 03 A50 withdrew.**
- `20-drift-controllers.md:21`: "The witness is bounded (A6): one receipt names one gateway replica via
  `hop.gatewayInstance` (design 04 A4), so **activation requires a distinct instance per replica** or
  reports `LLMFallbackUnavailable/PartiallyProgrammed`" — and "`llmFallbackActive` reports remediation
  **only after that positive evidence**", which L444 says is unobtainable on any multi-replica cluster.
- Design 20's own **A7** (`20:79`) states the withdrawal correctly and in design 03's exact terms ("the
  reason is **no proof rather than no input** … nothing routes a canary to a chosen replica through a
  Service"). The amendment log and the body disagree inside one document, and the body is the half an
  implementer builds from. Round-3 BLOCKER 3, still open.
- Design 03's other design-20 claims check out: `20:38` gives the canary the platform principal
  **`drift:model-canary`** in §5 as L445 says; `20:40–48` §6 has a "Fallback provider also drifted" row
  and **no** "fallback was never reachable" row, exactly as L637 states.

**G-23. Architecture §05 and design 23 still advertise the field A48 removed.**
- `docs/architecture.md:166` contradicts itself inside one sentence: "per-consumer budgets are **not**
  part of it — design 03 A48 removed them … external consumers are just another authenticated principal
  (IdP OAuth client, **per-consumer budgets**, receipts)."
- Design 03's own §3.4 row (L469) and the removed-field row (L113) are correct; the propagation is not.

**Verified correct, so the rewrite does not "fix" them**: design 07 A5.3's listener shape
(`07:154–158`, `matchLabels: {plume.dev/run-namespace: "true"}`) matches L175; design 27 §3's closing
rule ("**rejected, not warned**", `27:30`) matches L297; design 05's card row (`05:50`, SoT preserved per
ADR-0019) matches L473; design 17's mock route (`17:40`, "recorded as a design 03 §3.4 row now, alongside
the eval temporary-grant row") matches L477; design 01 A8 (`01:209`, "**No header signature exists or is
needed**") matches L466; architecture §17 is "Weight budget — core tier" (`architecture.md:415`) and
§05's "Exposure" subsection does put the card on the org/public A2A endpoint (`architecture.md:164,170`);
ADR-0020 is marked `superseded-by ADR-0028` and ADR-0028 is `accepted · supersedes ADR-0020`;
ADR-0014's "**enforced at the gateway**" wording is indeed still uncorrected
(`0014-compliance-profiles.md:4`), exactly as L499 says; and design 26 A2 exists with the title and scope
L483 gives it (`26:128`).

**One reinforcement for A-1**: the numbered ordering at L187–192 is **ADR-0020 decision (3)** verbatim —
"Fail-closed apply ordering: Backends → Policies → verify Accepted → Routes last"
(`0020-policy-compiler.md:4`). ADR-0020 is superseded and frozen; its ordering is still standing in the
live body above the stage machine that replaced it.

---

## H — CI trap the rewrite will hit (not a defect in the design)

`test/docs/superseded_test.go` scans every design's **authoritative body** for assertions of superseded
guarantees and rescues a block that carries the retraction. `go test ./test/docs` is green today. Seven
design-03 body blocks pass **only** on that rescue, and six of the seven rescues are narration this
consolidation deletes:

| Rule | Line | Banned phrase present | Rescued by |
|---|---|---|---|
| `in-worker-poll` | 196 | "on the reconcile worker" | "An **earlier draft** polled…" |
| `inert-tightening` | 319 | "quiesce the live route" | "**It is withdrawn.**" |
| `header-tool-filter` | 546 | "`Mcp-Name`" | "when the **superseded** note was written" |
| `cuts-early-guarantee` | 650 | "Cuts early, never late" | "is **withdrawn**" |
| `egress-as-restriction` | 489 | "a Backend restriction" | "**No such field exists**" |
| `egress-as-restriction` | 637 | "the gateway's Backend restriction blocks" | "under **construction**-not-restriction" |
| `overshoot-bound` / `excess is bounded` | 460, 652 | "the excess is bounded by…", "No overshoot bound" | "the excess is **unbounded**" |

**Delete the narration without rewriting the sentence and `go test ./test/docs` goes red.** In every case
the fix is the same and is an improvement: state the rule positively. The barrier is an event-driven
state machine because a sleeping worker starves the fleet; a serving route is never mutated, so tightening
runs through the revision gate; tool filtering is `backend.mcp.authorization` CEL, which also prunes
`tools/list`; the token limiter applies to future requests only and the crossing request completes; the
control is Backend construction because the CRD has no egress field; the gateway tier's excess is
unbounded and tracks concurrency. Let §11 keep the reversals.

`test/docs`'s `header-tool-filter` rule is `onlyIn: 03-policy-compiler.md`, so L546 is the only body in
the corpus that can trip it.

---

## Load-bearing — must survive the rewrite verbatim in substance

These are the invariants a critic will attack if they are softened. For each: the rule, the sentence that
must not be lost, and what enforces it — **nothing, in most cases, and that is said plainly**.

### 1. The apply ordering and the fail-closed barrier (§3.3, L185–221)
- **Rule**: the route is created **inert** (`parentRefs`, no `backendRefs`), then Backends, then
  Policies, then a convergence barrier over **every resource a mandatory entry names, Backends
  included**, then `Publishing` attaches `backendRefs` or shifts weight. Removal is reversed: routes
  detach or zero-weight before their policies and Backends are deleted. Progress is **watch-driven**; the
  operator applies once and returns.
- **Must not be lost**: "Each not-yet-converged Agent occupies a worker for the whole bound, requeues,
  and does it again, so **a principal who may create Agents can keep more permanently-pending objects
  than there are workers and starve every other reconcile — kill, budget-hold, finalization — behind
  them.** Raising `MaxConcurrentReconciles` changes how many bad objects are needed, not the outcome."
- **Must not be lost**: "a cold create reached `Converging` and could **never** leave: the stage that
  creates the route sat behind the barrier that needed it" — this is why `PreparingRoute` exists, and it
  is the sentence that must replace the dead numbered list (A-1).
- **Must not be lost**: "**The deadline is a condition, not a timeout.** … the machine stays in its
  stage; it does not abandon or roll back. **Withheld routes stay withheld, which is the fail-closed
  direction.**"
- **Must not be lost**: the recovery rule — a stale digest or generation drops the record and re-enters
  **the transaction's own first stage**, because "reconcile N's recorded stage would let reconcile N+1
  publish a route whose policies belong to the previous generation — **which is the fail-open the
  blocking poll existed to prevent, arriving through the status field instead**."
- **Also load-bearing and currently unstated**: which stage a `Withdraw` re-enters (A-15), and the route
  identity persisted with the stage (A-16).
- **Enforced**: nothing. `test/conformance/cluster_test.go:119
  TestPolicyOnAbsentRouteIsAcceptedButNotAttached` and `:171 TestInertRouteIsAttachableAndAnswers500`
  pin the two upstream facts the ordering rests on, and **that is the whole of the enforcement**. The
  ordering itself, the barrier, the deadline and the recovery rule have no test and no code.

### 2. The convergence tuple, and what it cannot prove (§3.3.2, L299–315)
- **Rule**, per kind, all at `observedGeneration == metadata.generation`:
  `AgentgatewayPolicy` — `Accepted=True` with `reason: Valid` **and** `Attached=True` on a
  **Gateway**-shaped `ancestorRef`, with empty ancestors read as **failure, not pending**;
  `AgentgatewayBackend` — `Accepted=True`, which proves **translation only**;
  `HTTPRoute` — `Accepted=True` **and** `ResolvedRefs=True`, because there is no `Programmed` condition.
- **Must not be lost**: "**A NACK'd policy retains the old config; it does not fail closed.**" And the
  measurement behind it: "A tightening update carrying a dataplane-invalid value was observed reporting
  `Accepted=True`, `Attached=True` at `observedGeneration == generation` while the proxy rejected it and
  **the previous permissive rule kept serving**: eight requests under a limit that should permit one all
  returned 200."
- **Must not be lost**: "the Event … carries **no generation and no UID**, so an old NACK cannot be told
  from a new one and **absence of an Event is not success**. That makes the Event sufficient to raise
  degradation and insufficient as a success barrier."
- **Must not be lost**: "`Accepted=True` also covers `PartiallyValid`" is the reason `reason: Valid` is
  required — currently carried only in the research note, not in the design's own table.
- **Three things the research note has and the design does not**, and the rewrite should add them:
  (a) the ancestor list is **capped at 16** and plume's entry can be **evicted**, replaced by a
  truncation ancestor carrying condition type `StatusSummarized`
  (`agentgateway-v1.4.1-2026-08.md:155–157`) — under the design's own rule, eviction reads as failure,
  which is fail-closed but leaves an Agent permanently un-`Ready` on a busy Gateway, for a reason no
  condition message can explain;
  (b) "**Gateway-shaped `ancestorRef`**" is not by itself the discriminator — the *synthetic failure*
  ancestor is also `kind: Gateway` (`agentgateway-v1.4.1-spike.md:52`,
  `{group: agentgateway.dev, kind: Gateway, name: StatusSummary}`); only the **name** separates it, so the
  predicate must match `G.name`/`G.ns` and reject the name `StatusSummary`, which the design's table
  states only as prose;
  (c) the strongest available predicate also requires the **Gateway's own** `Accepted=True` +
  `Programmed=True` and a `controllerName` match, neither of which the design's table carries.
- **Enforced**: `test/conformance/cluster_test.go:143 TestAttachedPolicyReportsTheRealGatewayAncestor`,
  `:119`, `:359 TestNackRetainsTheOldConfigAndReportsConverged`, plus
  `docs/research/agentgateway-v1.4.1-spike.md` §2.1, §2.2, §2.8. **This is the best-evidenced claim in the
  design**, and the cluster suite is where it lives.

### 3. No in-place tightening, and the closed comparator registry (§3.3.3, L317–351)
- **Rule**: `Tighten` is not a compiler transaction. Every tightening mints a revision and runs design
  02's blue-green machinery — the candidate gets **its own** routes and policies, is gated at weight 0,
  and weights shift only on pass. "The old route is never mutated, so there is nothing to quiesce,
  nothing to withdraw, and no NACK window on a serving path."
- **Must not be lost**: the three reasons the `Tighten` transaction was removed — the NACK measurement,
  the un-attributable witness ("A 401 can come from the backend, a rejected tool from an unhealthy MCP
  server, an unreachable host from DNS, a 429 from an upstream or an already-empty bucket"), and the
  structural fact that the revision-path escape was unavailable for exactly the policy-surface fields
  that needed it.
- **Must not be lost**: "**a `Loosen` that NACKs leaves the *stricter* old rule serving — a failure
  toward denial**", and its corollary at L452 that it still raises `PolicyApplyIncomplete`, "because a
  loosening that silently did not apply is a stale guarantee even when it is a safe one."
- **The registry must stay closed, and must stay one entry per concern**: `-auth` on the effective
  admitted-principal policy; `-ratelimit` numeric on the derived rate; `-toolfilter` set inclusion;
  `egressEnumerated` set inclusion on endpoint identities; `taskTimeout` numeric; `-transform` and
  `-guard` **never**, because no ordering exists over functions.
- **Must not be lost**: "**`oauth == oauth` is not evidence about who is admitted.**" And the walk:
  "An identity or IdP input moving the issuer or audience from one tenant to a broader tenant set is
  precisely that shape: the enum is unchanged, the admitted set is wider, and the update takes the
  in-place path onto a serving route that no gate ever approved for those principals."
- **Must not be lost**: "**Unknown maps to `Tighten`** … a **first application** — where there is no
  applied set to compare against — [classifies] as `Tighten`, so adding a concern without a comparator
  entry **fails safe** rather than silently qualifying for the in-place path." And L450: "Being wrong
  toward `Create` costs a gate cycle. Being wrong toward `Loosen` is the bypass this section exists to
  remove."
- **Must not be lost**: "**Normalization is part of each comparator, because a default is not an
  absence.**"
- **Enforced**: nothing. `-exchange` is missing from the registry (A-12) and the `-auth` normalization's
  six components have no producer at P1 (E-14).

### 4. Requiredness and aggregation (§3.3.1, L223–267)
- **Rule**: every route class names the concerns that must be present and `Accepted=True` **before that
  route may be created**; a mandatory concern whose input is absent is a **compile error**, never an
  omitted policy. **A binding an Agent declared is required** — "Writing `tools: [x]` is the demand;
  nothing in the CRD expresses 'and it is fine if this one is missing'."
- **Must not be lost**: the two-branch rule that keeps P1 compilable — "A route class **whose entire
  input set is unavailable because the producing design is not installed** is **not emitted at all**. No
  route, no policy, no error … A route class that **is** emitted, with one mandatory concern's input
  absent, is the compile error. **This is the dangerous case: a route exists and a guarantee does not.**"
- **Must not be lost**: "**The failure is route-scoped, not set-scoped.** … The alternative — one bad
  input voids the whole `ResourceSet` — trades a fail-open for a fail-stuck, where a typo in one tool
  name takes an agent off the air entirely."
- **Must not be lost**: "**Conditions aggregate by cause, not by route**", the two-condition split and
  the fixed precedence (`PolicyCompileFailed` outranks `PolicyApplyIncomplete`, "because nothing was
  applied to fail"); and "**One condition instance carries every failing path, and clears only when none
  remain** … the message lists the failing resources in a **stable order** … so a second failure appends
  rather than replacing."
- **Must not be lost, and it is the security argument of the section**: "A tool route without its filter
  grants the server's entire tool surface — and because the filter also prunes `tools/list`, its absence
  makes every tool *visible* as well as callable"; "an unauthenticated candidate route is a bypass of the
  whole gate"; "The Backend entry is **construction, not restriction** … a destination is never added
  because it is permitted."
- **Must not be lost**: "**Explicit opt-outs are emitted, not absent** … two absences look identical.
  The marker is a **label on the route**, not a policy: an empty `AgentgatewayPolicy` would be subject to
  §3.3's acceptance gate and could be webhook-rejected, **which would make `auth: none` unserveable — the
  opt-out must not be able to fail**."
- **Must not be lost — the whole mutation doctrine at L281–295**: the emit registry rather than a
  checklist ("deleting a registry entry **disables** the route instead of ungating it"); mandatory
  entries ranging over **resources**, not only policies; the **static catalog** written independently of
  the registry, checked in **both directions**; "**Cases enumerated from production data cannot pin
  production data**"; a **compiled test failure**, since a build error is INVALID not KILLED; and per-
  **profile** catalogs, because "deleting the `hipaa` profile's `-guard` extension leaves every base case
  green and the profile silently stops requiring PHI redaction."
- **Also load-bearing**: "**Reserved — unclassified — must not be emitted**", and the reason: "Listing
  them as explicitly unclassified is the difference between a deferral and an omission."
- **Enforced**: nothing. And §8 currently mandates the shape §3.3.1 rejects (A-18).

### 5. The two budget tiers, and every retracted guarantee (§3.4, §3.5, D3)
- **Rule**: the gateway tier is a **local approximation** — a token bucket at
  `⌊tokensPerDay ÷ (24 × gatewayReplicas)⌋` with `unit: Hours`, no burst, no external RLS and no Redis.
  The backstop tier is design 04's spend aggregate read each reconcile → `BudgetExhausted` + weight-0.
- **Must not be lost, and all three are retractions the corpus is CI-gated on**:
  1. "**'Cuts early, never late' is withdrawn, and nothing replaces it.** … *'token counts are not known
     until the request completes. As a result, token-based rate limits will apply to future requests
     only.'* **The request that crosses the budget completes and is returned**; only the next one gets a
     429." Plus: `tokenize: true` exists and is **hardcoded `false` on the xDS path**, so it is
     unreachable from Kubernetes.
  2. "**No overshoot bound is published, because the measured excess is unbounded.** … **20 concurrent
     requests all returned 200, and 100 concurrent requests all returned 200** — 100,000 tokens admitted
     against a 1,000-token budget, on **one** gateway replica. … **the excess tracks *concurrency***,
     which neither `PolicyIntent` nor the emitted policy constrains."
  3. "**the receipt tier does not bound that, because it prices from the same table** … A stale or
     tampered table skews both tiers identically" — and §6's consequence: "**no backstop**".
- **Must not be lost**: "a per-day budget is **not directly expressible** — hourly is the coarsest
  available window"; "`ExactlyOneOf=requests;tokens` means one policy cannot carry both"; "at tag v1.4.1
  `burst` carries **no `Minimum`**, so a negative burst is schema-valid and plume validates `burst >= 0`
  itself".
- **Must be deleted**: the capacity paragraph at L656 (A-4).
- **Enforced**: `test/conformance/crd_contract_test.go:163 TestLocalRateLimitShape` pins the exact `unit`
  enum, `minimum: 1`, the `ExactlyOneOf` **CEL semantic** (`size() == 1`, not a substring), the int32
  ceiling and the doc comment ADR-0028 rests on — with mutation notes in the test recording two earlier
  versions that survived a valid mutation. `cluster_test.go:233 TestNegativeBurstIsAcceptedEverywhere`
  and `:251 TestDailyUnitIsRejected` measure the rest. `test/docs`'s `cuts-early-guarantee`,
  `overshoot-bound`, `exact-usd-tier` and `pricing-damage-bounded` rules keep all three retractions from
  returning to the corpus. **This is the second-best-enforced part of the design.** The plume-side half —
  the divisor, the emitted arithmetic, `BudgetExhausted` — is enforced by nothing (E-4).

### 6. The pricing arithmetic and its edge rules (§3.5, L584–670)
- **Rule**: one row per **endpoint identity** tuple, two prices in **USD per 1,000,000 tokens**, matching
  **most-specific-wins**; two rows matching on the same field count is a **load error**.
  `P` = the maximum, across every model the agent is allowed to use, of that model's `max(input, output)`,
  over `providers[] ∪ {fallback}`. `tokensPerDay(usd) = floor(Uµ × 10⁶ / Pµ)`;
  `tokens = floor(tokensPerDay / (24 × gatewayReplicas))`.
- **Must not be lost**: "**Six integer digits, and the number is load-bearing.** … An earlier draft
  derived the true threshold (~9,223,372) correctly and then wrote a *seven*-digit grammar, which admits
  `9999999` … which **wraps to a negative** `tokensPerDay` — and the zero-rate guard below tested
  `rate == 0`, so a negative rate escaped it entirely. **A regex cannot express `U ≤ 9223372`; six digits
  is the largest bound a regex *can* express that is also safe.**" The arithmetic checks out:
  `999999.999999 → 9.99999×10¹⁷ < 2⁶³−1`.
- **Must not be lost**: "**`P = 0`** … A zero price is not an error and not a missing row: a free model
  imposes no monetary ceiling … **`-ratelimit` is mandatory on a *derived rate*, not on the presence of a
  budget field** — keying it on the field would make a usd budget over an all-free model set withhold the
  route." And "**Empty model set** … `PolicyCompileFailed` names `spec.budget.usdPerDay` — there is no
  `providers[i]` to name."
- **Must not be lost**: "**Both budget fields may be set** … the compiler emits **one** `-ratelimit`
  policy at the **minimum** of the two. Emitting two policies would put the same field in two policies
  and break §3.2's disjointness by construction."
- **Must not be lost**: "`P` is taken over `providers[] ∪ {fallback}` … **an unpriced `llm.fallback` is a
  compile error when the Agent is written**, never a `PolicyCompileFailed` raised mid-incident"; and its
  twin, "**`llm.fallback` must be one of the providers the emitted Backend enumerates** … under
  construction-not-restriction the failure is **total** rather than partial: a fallback outside the
  enumerated set is not merely blocked, it is **unreachable**."
- **Must not be lost**: "**`PricingStale` means the table is old, not that a row is missing.**"; the three
  narrowings that make staleness mean something (externally-priced agents only, a documented human
  refresh, advisory-never-withholding); and "**Two ConfigMaps, because two controllers cannot own rows
  inside one string** … Kubernetes field ownership stops at `data.models`".
- **Must be fixed, not preserved**: the rate-floor rule and its example (A-19).
- **Enforced**: `PricingStale` and `PolicyCompileFailed` exist as `ConditionType` constants
  (`agent_types.go:382,398`) and nothing raises them. The int32 ceiling and `minimum: 1` are pinned by
  conformance; the six-digit grammar is on **no** schema (`agent_types.go:312` is a bare `*string`,
  confirmed in both generated CRDs) and design 02 §5 records that gap.

### 7. Egress as construction (§3.4.1, §3.4.1.1)
- **Rule**: there is no egress, host, domain, provider or model allowlist field on
  `AgentgatewayBackend`. The control is: emit a Backend enumerating exactly the endpoints the Agent
  **requested**, prove that set is a **subset** of what the allowlist **permits**, and ensure the route
  can reach no other Backend. `dynamicForwardProxy` is never emitted.
- **Must not be lost**: "**Requested ⊆ permitted, and never equality.** … Under equality, a compliance
  profile permitting `{A,B,C}` for an Agent that requested only `{A}` would emit a Backend that can reach
  B and C — **turning a permission into a destination**. … **a destination is never added because it is
  permitted.**"
- **Must not be lost**: "**The predicate reads the effective host, not the provider name.**
  `host`/`port` **override managed-provider defaults**, so a Backend naming `openai` is not pinned to
  OpenAI's endpoint." And "**The predicate compares tuples, not hostnames**" — two ports on one host, two
  Azure deployments behind one endpoint.
- **Must not be lost**: "**an entry that resolves to nothing is `PolicyCompileFailed` naming it — never
  dropped, because a dropped entry silently widens nothing but a dropped *deny* silently widens
  everything.**"
- **Must not be lost**: "**What this guarantee does not cover, stated rather than implied.** The
  predicate is over **names**, not addresses: plume does not resolve DNS, so a permitted hostname that
  later repoints is outside the control."
- **Must not be lost**: "**`models` is enforceable on some arms and not others** … `azureopenai` does not
  [carry `model`] — it carries `apiVersion`, `deploymentName` and `endpoint`, and at `apiVersion: v1` the
  model may be supplied by the request, so nothing in the provider block pins it."
- **Must be fixed**: the arm set, the instance-field table and the YAML example (G-6, G-7, A-22).
- **Enforced**: `test/conformance/crd_contract_test.go:244 TestNoEgressFieldExists` (exactly one
  `allowlist` hit, the MCP methods list) and `:262 TestAzureOpenAIHasNoModelField` — which also pins that
  `anthropic`, `vertexai` and `bedrock` **do** carry `model`. The plume-side CEL is real:
  `agent_types.go:247–251,270–274` enforce arm↔instance matching, `azureopenai` carrying no `model`, and
  a `models`-narrowed `azureopenai` entry requiring a `deploymentName`. **This is the one design-03 rule
  with enforcement on both sides of the interface.** The subset predicate itself has none.

### 8. One concern per policy (§3.2, D2)
- **Rule**: `AgentgatewayPolicy` × N, one concern each (`-auth`, `-ratelimit`, `-toolfilter`, `-guard`,
  `-transform`, and `-exchange` — see A-12), disjoint fields, compiler-side conflict check.
- **Must not be lost, and it is the whole justification**: "same-level conflicts iterate a **randomly-
  seeded `HashSet`**, so the winning policy varies **per replica and per restart** while both report
  `Accepted`+`Attached`. **One-concern-per-policy is load-bearing, not a diffability preference.**"
- **Must not be lost**: the ordering consequence — "`backend.mcp.authentication` is deprecated in favour
  of [`traffic.jwtAuthentication.mcp`] *'which ensures authentication runs before other policies'* —
  directly the ordering §3.3.1 needs for `-auth` before `-toolfilter`. **The two may not appear in the
  same policy, which one-concern-per-policy already prevents.**"
- **Enforced**: the source finding is recorded at `docs/research/agentgateway-v1.4.1-2026-08.md:518–519`.
  Field-disjointness is an owed property test. Delete L722's contradicting clause (A-7).

### 9. MCP tool filtering (§3.4.2)
- **Rule**: `AgentgatewayPolicy.spec.backend.mcp.authorization`, CEL over `mcp.tool.name`,
  **`action: Allow` never `Deny`**, because "*'`Deny` is not recommended because expression failures fail
  to deny'* — a CEL error under `Deny` fails **open**, which is the one failure mode this design cannot
  accept."
- **Must not be lost**: "A tool the agent may not call is **absent from `tools/list`**, not merely
  rejected on call — an agent cannot attempt what it cannot see."
- **Must not be lost — the three total-emission cases**: "**no tools allowed** | one expression `false`.
  **Not an empty list** — `minItems: 1` rejects it — and **not an omitted policy**, which restores
  upstream's default-allow and turns 'no tools' into *every* tool"; "**more than 256 tools** |
  `PolicyCompileFailed` naming the count; truncating would silently deny the remainder while reporting
  success"; "**a name with a quote, backslash or newline** | a real CEL string encoder, never
  concatenation."
- **Enforced**: `test/conformance/crd_contract_test.go:292 TestMCPAuthorizationBounds` pins `minItems: 1`,
  `maxItems: 256` and `maxLength: 16384` against the vendored chart. The golden fixtures at L564 do not
  exist (E-12).

### 10. The A46–A50 set's diagnoses — what is right and must survive
Three critique rounds rejected the *text*; the **diagnoses** were accepted every round and the rewrite
must carry them:
- **A46**: a record written at mint can seal an **absence**. "an Agent created while design 11 is not
  installed recorded 'no tools resolved' as the value every later comparison was made against … **The
  Agent stayed `CapabilityUnavailable` for as long as it existed.**" The seal must key on the revision
  **carrying traffic**, and must be **per binding**, "because §3.3.1's failure is route-scoped: a
  revision can go active with one tool resolved and another's producer absent."
- **A46's fail-open corollary**, which is the sharpest sentence in §3.3.3: "The comparator must **not**
  fall back to comparing the current value against itself — that classifies every drift as `Equal`,
  never freezes, and **lets a resolved sibling's catalogue widen a live route with no gate**."
- **A47**: "an Agent failing `PolicyCompileFailed` because a legitimate MCP server advertises many tools
  would be **plume enforcing a storage cap the producer was never told about**." And the mode-keyed —
  not size-keyed — storage table, which round 3 called "the right structure".
- **A48**: "**One shared tenant limit cannot have N independently authoritative applied operands**".
- **A49**: the reduction — "the withdrawal is **applied and converged at the control plane** … and is
  **best-effort at the dataplane**, and every consumer of `PolicyInputDrifted=RouteFailedClosed` must
  read it as *'plume refused, and cannot prove the refusal reached traffic.'*" And: "**presence proves
  failure even where absence proves nothing**."
- **A50**: "**A declined option with a named cost, not an impossibility**" — per-Pod canary addressing
  costs N direct dials plus a membership watch and bypasses the production route the canary exists to
  traverse. And the surviving reason for the withdrawal: **nothing routes a canary to a chosen replica
  through a Service** (not "no field" — A-5).
- **The laundering paragraph (L382)** — round 3 called it "the best writing in this change" and it is:
  "an operator who sees `PolicyInputDrifted` and edits any behaviour field for any reason mints a
  revision that picks the widened catalogue up — gated on its *behaviour*, not on the catalogue. … It is
  the intended semantics … but **'the eval evaluated the new catalogue' holds only if the suite exercises
  the new tool, which nothing enforces.**"
- **The residual at L384**, which must not be quietly dropped: "A revision that went active without a
  capability its Agent declared **can never acquire it** … `CapabilityUnavailable` is permanent for that
  revision, correctly, and there is no automatic path to a gated one."
- **§5's A46 row and the two run-namespace rows (L691–694)** — round 3 verified all three as correct,
  total, and reconciled with §3.3.1. Keep them; note MINOR 7 below on the condition they raise.

**One caveat the rewrite must apply to all of the above.** Every diagnosis in this set is sound *given*
that a tool allowlist is a non-spec producer's output. **G-17 shows the producing design says otherwise**:
`toolAllowlist` is a declared `Connector.spec.tool` field. That does not make the machinery wrong — the
residual case (no `toolAllowlist` declared, so the server's own surface is the input) is real, and
`knowledge[].endpoint` and `identity` are genuinely non-spec — but it means the rewrite cannot simply
carry §3.3.3 forward. It has to state, per input, whether the value is declared or discovered, and only
the discovered ones need `resolvedInputs`, the seal and the freeze. Applied to a declared field, the
freeze is worse than useless: it refuses a Connector author's deliberate, audited narrowing on the
ground that the author had no way to ask for it.

### 11. Things easy to lose that are load-bearing elsewhere
- **The route must move with the workload** (L161): a cross-namespace `backendRef` yields
  `ResolvedRefs=False, RefNotPermitted`, "**Leaving the route behind wedges every Agent at every
  install.**" And a `parentRef` to a Gateway in another namespace needs **no** `ReferenceGrant`, "so
  admitting the run namespaces on the listener is the whole of the cross-namespace consent this move
  needs."
- **Ownership is a label and the NAME is the deletion authority** (L168) — the design's own statement of
  design 02 A57's rule, and the premise round 3 mutation-confirmed.
- **The naming rule** (L181–183): `<name>-<concern>[-<rev>]`, truncate past 63 and append **16 hex**.
  "32 bits is not enough for a name that is also a security boundary … design 02 A42 imports this rule to
  name **run namespaces**, where a collision **pools two tenants' material in one namespace**."
  **Enforced**: `internal/controller/runnamespace.go:118`, `MirrorName:130`, and
  `internal/controller/runnamespace_unit_test.go:17`. This is design 03's only shipped code rule.
- **`gatewayReplicas` is declared, never discovered** (L149): "Discovering it by watching the Deployment
  would make one HorizontalPodAutoscaler event **recompile the rate policy of every Agent in the
  cluster** — an unbounded fan-out this design does not size." And its honest cost: "The claim is *not*
  that the blast radius is zero … **What is bounded is that the watch never rewrites policy — it only
  sets a condition.**"
- **Declared-but-missing is an incident; declared-off is a documented tier** (L143): "**They are
  different condition types, not two reasons on one type** — design 10's alerts and design 08's `deploy`
  stream both key on the condition type, so a shared type would page and abort on the documented normal
  state of every P1 cluster. … Only the incident withholds `Ready`."
- **Evaluation order is not known and is not asserted** (§3.4.3): the two things that depend on the
  answer — "**whether an auth-rejected request consumes quota**. If the limiter runs first, a flood of
  unauthenticated requests exhausts a paying agent's budget — **a denial-of-wallet an attacker needs no
  credential to run**" and where a profile's `-guard` must attach — and the written-down experiment
  (`401,401,401` vs `401,429,429`). "**Until that runs, no row in §3.4 states an order, and no design
  sentence may depend on one.**" The harness exists (`Makefile:59`).
- **KG scope is gateway-injected, provider-enforced** (L466), with the trust consequence stated: "the
  gateway does not parse MCP bodies" and "**providers are scope-enforcing**".
- **`captureLevel` leaves `PolicyIntent` entirely** (L485): "leaving it in the intent invited **two
  reconcilers to last-writer-win one Gateway policy** — a per-Agent field silently deciding a
  cluster-wide setting."
- **Tenant quotas are not enforced** (L483): "**a tenant traffic quota is not enforced**" — one of the
  few present-tense negative claims in the body and exactly the right register.
- **`BudgetEnforcementDegraded`'s three reasons are three remediations** (L689, L690, L696):
  `ReplicaSkew` in **either** direction, `ReplicaUnverified` scoped to `gateway.enabled: true` with a
  derived rate, and `AggregateUnavailable`. "A bare condition with three causes and one message is rule
  8's loud-and-wrong."
- **`ReplicaUnverified` is not the P1 state** (L690): "An earlier draft called this 'the P1 state', which
  would have put both conditions on every stock Agent and **left an operator unable to tell a tier choice
  from an incident**." Keep the rule; drop the draft.

---

## What the A46–A50 rounds left OPEN — the rewrite must resolve these, not inherit them

From `reviews/03-a46-a50-critique.md` round 3 (5 BLOCKER, 6 MAJOR, 8 MINOR, "not converged; consolidate
rather than amend"). Verified against the current body — every one of these is still live.

| # | Open item | Where it sits now |
|---|---|---|
| **O-1** | **The seal is a transition with no durable marker.** L79 says the seal fires "at the single reconcile in which this revision's digest *becomes* `status.activeRevisionDigest`". A level-triggered reconciler has no memory of the previous reconcile, and A46 **deleted** the field that could carry it: `sealedBindings` is "ABSENT until the seal" (L66) and the seal of a revision where **zero** bindings resolved writes an **empty list**, indistinguishable after a round-trip from never having been written. Any implementable edge detector therefore seals **late** — the producer arriving months later gets sealed onto a live route. This is A24's own "an empty slice meant two things", one field over, in the amendment that cites A24. | L66–67, L79 |
| **O-2** | **The canary is an unsealed window on the production route.** L379 says an unsealed never-active revision "carries no production traffic" and "a candidate's route admits only the eval principal". Design 02 §3.3 step 4 (`02:244`) shifts weights **before** `activeRevision` flips, and design 02 reserves `phase: Canary` for exactly that shift, "arriving with design 03". So for the whole canary the revision is unsealed, serving real users at 10% then 100%, with bindings re-resolving every reconcile and the comparator not consulted. §3.3.3's three-state table has **no row for it**. | L379 vs `02:244` |
| **O-3** | **`sealedBindings`'s grammar is not injective, and has no value for `identity`.** L80's `<kind>/<name>` where `name` is "the binding's `requested` string … no join and no split is performed on it". `spec.knowledge[]` carries `{name, version}`; two versions of one graph resolve to different endpoints and collapse onto one key — representing the request needs the join the grammar forbids. And nothing in `AgentSpec` names an identity, so the third kind has no `name` segment. This would be the **fourth** non-injective key in this design's lineage, sitting on the operand the freeze keys on. | L80, L26, L108 |
| **O-4** | **The seal is a two-object write with no crash rule.** L79 puts a ConfigMap create and a status update in one sentence. A crash between them leaves the record permanently unsealed on an active revision — "nothing seals after it" (L79) — so per L380 the capability is withheld and "only a new revision can acquire it": **an Agent whose tools resolved cleanly loses every tool for the life of that revision because the operator restarted at the wrong moment**, silently, since `PolicyInputDrifted` names a producer that never moved. And the name-taken case (`<agent>-<revisionHash>-inputs`, a 40-bit truncation that is chosen-collidable in about a second) has no rule, because design 02's `RevisionMaterialCollision` answer is "the revision is not published" and the seal fires **after** publication. | L79, L95, L684 |
| **O-5** | **`identity`'s binding-ness did not propagate.** Design 06 is absent from L121's `ProducerAbsent` enumeration; `ProducerAbsent`/`Unresolvable` are undefined for an SVID; A33's per-Agent rule read literally fires on every Agent in every cluster; and design 06 was never amended — it contains no `Binding`, no resolution state, and its status line is unmarked. See A-9. | L26, L104, L121, L260 |
| **O-6** | **The `-inputs` object's owed item to design 02 is insufficient and was never delivered.** The shipped delete authority gates on three things (name shape + `plume.dev/revision-digest` + `plume.dev/source`); L95 specifies two labels and no annotations and asks only for the name shape. Design 02 was edited in the same change and carries no owed note. See G-11. | L95 |
| **O-7** | **The external-Agent row rests on a premise the same design refutes.** L92: "Its resolved inputs are an endpoint and an OAuth client identity, which do not approach the bound." `spec.tools[]` is a sibling of `external`, nothing excludes an external Agent from declaring tools, and L26/L62 put `toolAllowlist` in the sealed value unconditionally — so an external Agent fronting a large MCP catalogue is a **permanent `PolicyCompileFailed`**, a capability removed by a storage rule. | L92, L81 |
| **O-8** | **Design 20's remediation row still specifies the check A50 withdrew.** Round 3 BLOCKER 3: `20:21`'s detection clause was edited and its remediation clause — "activation requires a distinct instance per replica" over `hop.gatewayInstance` — was not. An implementer reads the row that specifies the whole path. | cross-design |
| **O-9** | **Narrowed since round 3, and still open.** `20:21` no longer says drift detection "cannot run" — it now says `hop.endpoint` is emitted for arm, model and host:port but **not** Azure's `deploymentName`, "so two deployments on one endpoint differing only in deployment are one identity to this read and **drift between them is invisible**". So the detector works except for that pair. But §6's failure table (`20:40–48`) still scopes `DriftDetectionDegraded` to "Canary CronJob fails (infra)" and has **no row for the Azure blind spot** — a permanently undetectable drift class with no condition naming it, which is the same NFR-8 shape one case smaller. | cross-design |
| **O-10** | **`recordDigest`: six vs seven.** Third round on one word. See A-3. | L70 vs L78 |
| **O-11** | **Design 02 still carries the size-keyed storage rule design 03 replaced with a mode-keyed one** (`02:92`'s comment), and shows only the by-reference form, so the **inline** shape is undefined on both sides of the interface. | cross-design |
| **O-12** | **§5's missing-object row raises the wrong condition.** L693 raises `RevisionRecordUnreadable`, which by L83 means the record failed its **digest**; here the record is intact and the *object* is gone. Design 02 has `RevisionMaterialUnavailable` for exactly that, and L693's claim that design 02's weight-0 path "is the same direction" as "what is already serving keeps serving" is not true — they are opposite outcomes for one event. A reader now has three answers to a lost run namespace. | L693 |
| **O-13** | **`llm.providers[]` and `llm.fallback` are `Binding<T>` and are excluded from `sealedBindings`'s closed kind set with no statement of why.** The answer is derivable (their provenance is spec, so the spec row of the A37 table governs) and is not written, on a row whose whole purpose is to be a closed set. | L26, L32–33, L80 |
| **O-14** | **Design 11 has never been told its catalogue bound**, and the change added a **second**, 256× tighter one (4 KiB) that design 11 has also never signed, while L81 still calls one MiB "a bound a producer can be held to". | L81, L95 |
| **O-15** | **Design 23 §3 and architecture §166 still contradict themselves inside one sentence on per-consumer budgets**, and design 26 §3's Traffic row and D1 still assert the `TenantPolicyIntent` mechanism A2 says does not exist. | cross-design |

Two round-3 MINORs are now **closed** and should not be re-inherited: the header's amendment range and
review citation (L3 now reads A1–A50 and cites the three-round critique), and the kind of the `-inputs`
object (L95 names `ConfigMap`, one MiB across `data`).

---

## One recommendation for the rewrite

Design 02's inventory found sedimentation. Design 03 has that **and** it has stopped being internally
consistent: twenty-four live contradictions, twelve of them inside §3.1 and §3.3, and the round-3
diagnosis — one rule, five statements, no two agreeing — is confirmed exactly. Three mechanical passes
remove roughly half of this inventory before any judgement is required:

1. **Delete every sentence whose subject is an amendment, a draft, a round or a reviewer** (C + D): ~47
   paragraphs and 28 named sites, plus the amendment numbers in ten section headings. The counter-examples
   marked **[keep]** above are the only things to rescue — and §H's seven blocks must be restated
   positively or the docs gate goes red.
2. **Delete every status latch and every claim whose producer does not exist without saying so** (B + E):
   14 + 17 sites, listed above. Four of them — `status.apply`, `status.llmFallbackActive`,
   `gatewayReplicas`'s producers and the pricing ConfigMaps — are not just unimplemented; they are
   **unowned**, and the rewrite has to assign them or state the gap.
3. **State each rule once** (F): 26 sets, and F-1 above all. §3.1 states what the record holds and when it
   freezes; §3.3.3 and §5 cite it.

Then the twenty-four contradictions become twenty-four decisions against a body that can hold them.

**One of them is not a tidying decision and should be taken first.** G-17 is upstream of §3.1's
provenance table, §3.3.3's whole second half, `RevisionRecord`'s two largest fields, three of the five
places F-1 names, and four of the fifteen open items. If `toolAllowlist` is a declared `Connector` field,
the rewrite is materially smaller than the current body — and if the rewrite is done before that question
is answered, it will consolidate the machinery around a premise the producing design contradicts, which
is precisely how the last fifty amendments accumulated.

Where the work is right, and it is right in more places than three REVISE rounds suggest: **§3.3.2 is the
most honestly-bounded claim in this corpus** — it measured its own barrier lying, said so, reduced the
claim to control-plane convergence and then built a conformance suite that fails when upstream moves.
**§3.3.1's mutation doctrine** ("cases enumerated from production data cannot pin production data", the
emit gate rather than a checklist, per-profile catalogs) is the strongest testing argument in the repo
and the reason its own §8 is a finding rather than a footnote. **§3.5's three retracted budget guarantees**
are each retracted with a measurement rather than an argument, and the corpus is CI-gated against their
return. **§3.4.1's construction-not-restriction** is the only design-03 rule enforced on both sides of the
interface. And the A46–A50 diagnoses are correct: every one of them names a real fail-open, and not one
was rejected on its substance in three rounds — only on its text.

The problem is not the design. It is that the design has been amended fifty times and has now recorded
its own inconsistency in its status line, which is where a document stops being a specification.
