# Design 03 (policy compiler) — critique of the 2026-09-10 consolidation

**Reviewer**: independent adversarial critique, per the `critique-design` skill and AGENTS.md's "If you are here to review" section.
**Target**: `docs/designs/03-policy-compiler.md`, header + §1–§10 (lines 1–734 of the body; §11 is provenance and out of scope, matching the scope the consolidation inventory itself used).
**Method**: every claim about another file — code, chart, ADR, research note, or another design — was read at its source and checked against what the design says it says. File:line citations below are to the working tree at the time of this review. No file was edited.

## Verdict: **REVISE**

**6 BLOCKER, 4 MAJOR, 1 MINOR.**

The header's central claim — "The cross-design claims were verified before a line was rewritten... 179 claims... 146 held, 32 were false, 1 was unverifiable. Every false one was read at its source and corrected here" — does not hold up against a second reading. At least four of the pre-consolidation inventory's own twelve "must decide, not tidy" defects survive **verbatim, at the same textual shape**, in the body dated 2026-09-10. Two more defects — one a false statement about the actual shipped CRD, one a false analogy to a cross-referenced design — were not in the inventory's list at all and were found by reading the cited sources directly, which is exactly the failure mode 18% of this design's pre-consolidation cross-references exhibited. The document is honest about the four defects it states as OPEN (see "What I verified and found sound" below) — that discipline is real and should not be lost in a rewrite. But a document whose entire justification for existing is "we checked" needs the checking to have covered what it says it covered, and on this pass it did not.

---

## BLOCKER findings

### B1 — The LLM arm catalogue is asserted as "verified in the shipped CRD" and is wrong by two arms

**Location**: `03:553`, `03:563–571`, `03:590`.

**The claim**: `03:553` — "An allowlist entry is meaningless until it resolves to a concrete `LLMProvider` arm (`openai`, `anthropic`, `gemini`, `bedrock`, `vertexai`, `azure`, `azureopenai`, `custom`) plus an effective host." `03:590` repeats the same eight-member set and calls it "the closed set... **verified in the shipped CRD**." The instance-field table at `03:563–571`, introduced as "Verified in the **v1.4.1 `AgentgatewayBackend` schema**", lists five arms (`anthropic`/`openai`, `azureopenai`, `vertexai`, `bedrock`, `custom`) and omits two of the eight entirely.

**The evidence**: `api/v1alpha1/agent_types.go:204` — `+kubebuilder:validation:Enum=anthropic;openai;azureopenai;vertexai;bedrock;custom`, six arms, confirmed by the `const` block at `agent_types.go:208–213` (`ArmAnthropic`, `ArmOpenAI`, `ArmAzureOpenAI`, `ArmVertexAI`, `ArmBedrock`, `ArmCustom`). There is no `azure` arm distinct from `azureopenai`, and no `gemini` arm at all.

**Why it matters**: this is the enum the entire §3.4.1 egress-subset predicate, the §3.4.1.1 entry grammar, and §3.5's pricing-tuple match are built on. A reader implementing from this table would write a resolver branch for two arms that cannot exist and would still miss nothing real — but the document's claim to have checked "the shipped CRD" is the same claim its consolidation rests its authority on, and here it is false in the direction that is easiest to catch (`grep` on one file).

**Fix**: replace both catalogues with the six-member enum from `agent_types.go:204`, and delete `azure`/`gemini` from every place they appear (`03:553`, `03:590`, and any place §3.5's pricing catalog echoes them).

### B2 — §3.4.1.1's canonical YAML example and the claim it rests on use fields that do not exist

**Location**: `03:577–586`, in the section introduced at `03:563` as "Verified in the v1.4.1 `AgentgatewayBackend` schema."

**The claim**:
```yaml
- arm: azureopenai
  instance: {endpoint: acme.openai.azure.com, deploymentName: gpt4o-prod}
- arm: custom
  endpoint: {scheme: https, host: llm.internal.example.com, port: 8443, pathPrefix: /v1}
```

**The evidence**: `api/v1alpha1/agent_types.go:220–227` (`AzureOpenAIInstance`) has flat fields `Endpoint string`, `DeploymentName string`, `APIVersion string` — no `instance:` wrapper. `agent_types.go:243–250` (`CustomInstance`) has flat fields `Host string`, `Port int32`, `PathPrefix string` — no `endpoint:` wrapper and **no `scheme` field anywhere**; a repo-wide grep for `scheme` in `api/v1alpha1` returns nothing.

**Why it matters**: this is the example an implementer copies. Written this way, it produces a CR the schema rejects outright — not a bug in enforcement, a nonexistent shape.

**Fix**: rewrite the example against the real Go types: `{arm: azureopenai, endpoint: acme.openai.azure.com, deploymentName: gpt4o-prod}` and `{arm: custom, host: llm.internal.example.com, port: 8443, pathPrefix: /v1}`, no `scheme`.

### B3 — The apply-order contract and the stage table state opposite orderings, unfixed since before consolidation

**Location**: `03:234–239` (numbered list) vs. `03:247–254` (stage table) vs. `03:258` (the section's own explanation of why they disagree).

**The claim**: `03:234–239`, "Order is part of the contract: 1. **Backends** first. 2. **Policies** next. 3. **Routes / weight changes last**." The stage table three lines later has `PreparingRoute` — which creates the route itself, with `parentRefs` set and no `backendRefs` — as the **first** stage, before `ApplyingBackends`. `03:258` states outright why: "An earlier draft ordered Backends → Policies → converge → *then* create the route... a cold create reached `Converging` and could **never** leave... The route is therefore created first." The document knows the numbered list is wrong and states the reason in the very next subsection without correcting the list itself.

**Evidence this is not a semantic reconciliation** (i.e., "routes" in item 3 means only weight/traffic, not resource creation): item 1 says "Backends first," flatly, and `PreparingRoute` precedes `ApplyingBackends` in the stage table — so even reading item 3 charitably, item 1 is still contradicted by the table.

**This is the pre-consolidation inventory's A-1, verbatim.** The consolidation inventory (`docs/designs/reviews/03-consolidation-inventory.md:83–98`) flagged this exact contradiction at the pre-consolidation line numbers and prescribed the fix: "delete the numbered list. The contract is the stage table plus the two invariants it preserves." That fix was not applied.

**Fix**: delete the numbered "Order is part of the contract" list; state the two invariants (no route carries traffic before its mandatory concerns converge; removal is reverse-ordered) and point to the stage table as the single source of the order.

### B4 — "Two transaction kinds remain" and there are three, unfixed since before consolidation

**Location**: `03:385–390` ("Two transaction kinds remain:" — table with `Create`, `Loosen`) vs. `03:461–467` (`Withdraw` introduced as "a real transaction with stages," with its own `Withdrawing` stage never added to the two-row table).

**Evidence**: the recovery rule at `03:265` ("`PreparingRoute` for a `Create`, `ApplyingBackends` only for a proven `Loosen`") also enumerates only two kinds and has no entry point for a stale `Withdraw`.

**This is the pre-consolidation inventory's A-2, verbatim** (`03-consolidation-inventory.md:100–106`), prescribed fix: "one transaction table with three rows, in §3.3.3, stated once." Not applied — the body still states "two kinds remain" as if `Withdraw` were not itself a stated exception introduced later in the same section.

**Fix**: one three-row transaction table (`Create`, `Loosen`, `Withdraw`) in §3.3.3, and the stale-state recovery rule at `03:265` extended to name `Withdraw`'s entry stage (`Withdrawing`).

### B5 — `recordDigest`'s own field count is wrong in the type it appears in

**Location**: `RevisionRecord` struct, `03:65–80`; contradicted at `03:87`.

**The claim**: `03:79` — `recordDigest: string // SHA-256 over the six fields above, schemaVersion included`. Counting the fields actually listed above it in the same struct (`schemaVersion` L66, `hash` L67, `behaviourProjection` L68, `originalBudget` L70, `resolvedInputs` L71, `sealedBindings` L75, `appliedDigest` L78): **seven**, not six. `03:87` (the property table) then says the opposite of the inline comment: "`recordDigest` covers **every** field including both [`resolvedInputs` and `sealedBindings`]."

**This is the pre-consolidation inventory's A-3, verbatim**, marked there as "third round on this finding" (`03-consolidation-inventory.md:108–115`) — i.e., this specific miscount has now survived at least four review passes including the one that produced this consolidated body.

**Fix**: change the comment at `03:79` to "seven fields above."

### B6 — The testing table omits the one layer that is actually pinned, understating the design's real enforcement

**Location**: §8.1's three-layer table, `03:772–776`, and `03:780` ("**Until then design 03 has no e2e coverage at all.**").

**The claim**: the table lists only `unit`, `envtest`, `e2e`, with `e2e` marked "nothing | needs a data plane." No row exists for anything else, and the surrounding prose states flatly that design 03 has zero coverage until the agentgateway subchart lands.

**The evidence**: `test/conformance/` exists (`cluster_test.go`, `crd_contract_test.go`, `doc.go`, `status.go`, `status_test.go`, plus `testdata/`), and `Makefile:58–59` wires `conformance: go test ./test/conformance/... -count=1`, which `Makefile:75`'s `test:` target (`fmt vet unit docs conformance envtest chart`) runs on every `make test`. This is not a hypothetical future layer — it runs today, in CI, and it is what pins several of the riskiest claims this very design makes: the design's own text cites it twice, correctly, for narrow claims (`03:614`, "verified in the shipped chart and pinned by `test/conformance`"; `03:640`, "Owed to `make conformance-cluster`, where the harness already exists"). But §8.1's own testing-layer table — the section whose entire purpose is to tell a reader what ships and what does not — has no row for it at all, and the prose immediately generalizes to "no e2e coverage at all," which reads to anyone who has not also read `03:614`/`03:640` as "nothing here is checked against reality."

**This is the pre-consolidation inventory's B-7**, called there "the most consequential stale status in the body: it tells a reader design 03 has no enforcement when its riskiest external premises are the best-pinned things in the repo" (`03-consolidation-inventory.md:370–381`). It survives unfixed.

**Fix**: add a `conformance` row to the §8.1 table naming what it pins (the vendored-CRD contract: `LocalRateLimit`'s `unit` enum and `minimum: 1`, the `ExactlyOneOf(requests,tokens)` semantic, the MCP `Mcp.authorization` bounds, the four-CRD chart shape), and narrow "no e2e coverage at all" to what is actually still missing — a real data plane and the traffic-level assertions (429s, tool filtering, SVID rejection) that conformance's vendored-but-not-live CRDs cannot exercise.

---

## MAJOR findings

### M1 — Design 03 contradicts itself, and misattributes to design 16, on when the canary/eval principal is admitted

**Location**: `03:503` vs. `03:534` (both inside design 03's own body).

**The claim at `03:503`** (§3.3.3, "The canary must be admitted"): "the canary principal is admitted **at revision publish**, as part of the route's original gated admitted set — **like design 16's eval principal, which §3.4 already admits the same way**." This is offered as the justification for why admitting the canary principal mid-incident would be an ungated widening.

**Design 03's own §3.4 table, twelve lines later** (`03:534`): "Eval temporary grant | candidate-route admitted-set **add/remove at eval Job launch/end** (per-run eval SVID)" — i.e., a live, temporary grant added and withdrawn around each Job run, not a standing member of "the route's original gated admitted set."

**Checked against the actual source, design 16** (`docs/designs/16-evalsuite.md:51`): "the compiler adds that principal to the candidate route's admitted set **at Job launch, removing it at Job end** — a narrow, temporary, compiled grant." This confirms `03:534` and refutes `03:503`.

So design 03 states two different admission timings for the same principal in the same document, and the one used to justify the canary's own admission-at-publish design is the wrong one, sourced from a document that says the opposite of what it is cited for. (The same false analogy also appears in design 20; per an earlier independent pass, it is a shared, previously-uncaught error this consolidation restates as settled rather than re-deriving.)

**Why it matters**: §3.3.3 devotes an entire subsection to the risk that a live, in-place admission change to a route's principal set can NACK and silently retain the wrong configuration (the `Withdraw` transaction exists because of exactly this risk). The canary principal's justification borrows an analogy that, read correctly, is an *instance* of that same risk — a live add/remove around a Job — not a counterexample to it.

**Fix**: correct `03:503` to state the canary principal's actual justification without the design 16 analogy, or state honestly that the canary case differs from the eval case (revision-publish admission vs. Job-scoped admission) and defend that difference on its own terms.

### M2 — `gatewayReplicas`'s stated producer does not exist, contradicting its own "Available at P1: yes"

**Location**: field provenance table, `03:142`; load-bearing claim at `03:149` and `03:706`.

**The claim**: `03:142` — "`gatewayReplicas` | chart value `gateway.replicas` → operator flag `--gateway-replicas`, default `1` | **yes**" (Available at P1). `03:149` — "It is the divisor on every emitted rate limit (§3.4), so a wrong value **under-enforces by exactly the factor it is wrong by**." `03:706` — "`gatewayReplicas < 1` is likewise rejected **at the flag**."

**The evidence**: `charts/assayd/values.yaml:87–90`'s `gateway:` block has exactly `enabled: false` and `name: assayd` — no `replicas` key. `cmd/operator/main.go`'s actual flags (lines 66–78) are `operator-namespace`, `gateway-url`, `metrics-bind-address`, `health-probe-bind-address`, `leader-elect`, `eval-suite-check-interval` — no `--gateway-replicas`. Neither producer exists.

**Why it matters**: this is not a minor gap. The value divides every emitted rate limit, and the design says so in the same breath as claiming it is "Available at P1." A reader trusting the provenance table would believe the mechanism exists to be flag-tested; it does not.

**Fix**: mark `gatewayReplicas` "Available at P1? no" like `identity` and `knowledge[].endpoint`, or add the disclaimer AGENTS.md rule 7 requires ("neither the chart value nor the flag exists yet; this is the intended source").

### M3 — `status.apply` is asserted in the present tense with no owning schema anywhere, unlike its sibling `status.revisions[]`

**Location**: `03:256` ("`status.apply` carries `{transaction, digest, generation, stage, deadline}`") and throughout §3.3 (the staged-reconcile machinery reads and writes it as if it exists).

**The evidence**: `status.apply` does not appear anywhere in `api/v1alpha1/*.go` (confirmed by grep). It also does not appear anywhere in `docs/designs/02-agent-crd.md`, the document design 03 itself calls "the authoritative schema" (`03:670`). This matters by contrast: design 03's own `status.revisions[]` — a field belonging to the *same* apply machinery — is explicitly disclosed as absent, and by the *other* design: design 02 §5 (`docs/designs/02-agent-crd.md:475`) carries the row "`status.revisions[]` | **Not on the CRD.** The field is this design's and its contents are design 03 §3.1's; nothing writes or reads it until the compiler exists." No equivalent sentence exists for `status.apply` in either document.

**Why it matters**: this is exactly the shape AGENTS.md rule 7 exists to catch — "a design table... has shipped a promise nothing implemented. If it is not enforced, say so." The document's blanket header disclaimer ("the compiler does not exist") covers general non-implementation, but does not tell a reader that this specific field, unlike its sibling, is not even named as owed by the CRD's own authoritative design.

**Fix**: add a row for `status.apply` to design 02 §5's "stated but not enforced" table (the mechanism design 03's own convention uses for `status.revisions[]`), or state in design 03 §3.3 that `status.apply`'s schema is owed and by whom.

### M4 — A research finding explicitly recorded as owed to this design was never incorporated

**Location**: absent from §3.4.2 (MCP tool filtering, `03:602–622`) and from the rest of the body; the research note is `docs/research/mcp-session-statefulness-2026-09.md`.

**The evidence**: the research note's own opening line: "This note settles a question **design 03 was recorded as owing** (`research/agentgateway-v1.5.0-delta-2026-09.md`, addendum 3): whether assayd's MCP backends are `Stateful` or `Stateless`, and what a stateful session does to design 02's weighted revision shifting." It is dated 2026-09-09 — one day before this design's 2026-09-10 consolidation — and answers the question definitively: MCP revision `2026-07-28` removed protocol-level sessions from the wire entirely, which means "the weighted-shift hazard disappears at the protocol layer." A grep of the current design 03 body for "session" turns up nothing related to MCP statefulness at all (`03:26`, `03:392`, `03:873` use "stateful" only for rate-limit quantities, an unrelated sense).

**Why it matters**: the consolidation's stated method was to check every producing document and every research note before writing a line (`03:7`). This note is not a stray cross-design fact — it is a research artifact the design itself was on record as owing an answer to, resolved the day before the rewrite, and settling a question (does assayd's MCP backend posture interact badly with design 02's canary weight-shifting?) that bears directly on §3.4.2 and on the weighted-shift machinery §3.3 depends on. Its absence from the consolidated body means the consolidation's own commissioning process — "checked against the producing document's current text" — did not extend to research notes recorded as owed to design 03 specifically.

**Fix**: add a paragraph to §3.4.2 (or a new subsection) stating the MCP session note's conclusion: assayd's MCP backends are correctly stateless under `2026-07-28`, so no session-affinity hazard exists for design 02's weighted revision shifting, and `test/mcpserver`'s existing behavior needs no change. If the note in fact requires a design change (e.g., taking advantage of the new session-independent `tools/list` for eval-gated tool-server rollouts, which the note itself flags as newly checkable), say so.

---

## MINOR findings

### m1 — The `ProducerAbsent` enumeration for identity is ambiguous, and only partly covered by the OPEN-defect disclosure

**Location**: `03:36–38` (identity is "a Binding like the others") vs. `03:153` ("`ProducerAbsent` | the producing design is not installed **(11 for tools, 01/13 for KG)**" — a closed enumeration that omits design 06 for identity, even though the field provenance table at `03:132` marks identity "Available at P1? no" with design 06 as its producer).

The header's disclosed OPEN defect ("identity's binding-ness never reached design 06") covers the deeper problem — that design 06 has not been amended to treat identity as a binding at all — but does not resolve the narrower, mechanical question this enumeration raises: given that identity is declared a `Binding<T>` in §3.1 and design 06 is absent at P1, what state does an Agent's identity binding actually report? The enumeration at `03:153` structurally cannot answer "yes" for identity, since it isn't in the list, which leaves a gap between what §3.1 declares (identity seals like any other binding) and what §3.3.1's compile-error machinery can actually classify it as.

**Fix**: either add design 06 to the `ProducerAbsent` enumeration explicitly (if the intended per-P1 answer is "not emitted, no condition, same as an absent tools/KG producer"), or state explicitly that identity is exempted from this table pending the OPEN defect's resolution.

---

## What I verified and found sound

- **The four self-declared OPEN defects are accurately stated and match their sourced sections.** "The seal is an edge... seals late" (`03:113–114`), "The canary is an unsealed window on the production route... this design does not choose" (`03:115`), "`identity`'s binding-ness never reached design 06" (`03:132`, cross-checked — design 06 is not among this design's cited ADRs/interfaces and the claim is consistent with the field-provenance table), and "a revision promoted without a declared capability can never acquire it... Owed to design 02" (`03:442`) are all stated in the body rather than papered over, and nothing else in §1–§10 silently assumes any of the four is solved (the closest candidate, the canary-admission text at `03:503`, is wrong for the different reason in M1 above, not because it assumes the canary window is sealed).
- **ADR-0032 and the `toolAllowlist` contradiction it settles**: read directly (`docs/decisions/0032-tool-allowlist-is-required.md`) and cross-checked against design 11 §11 (`docs/designs/11-*.md:99–107`). The header's claim that ADR-0032 "settles the `toolAllowlist` contradiction design 11 §11 and this design each recorded against the other" is accurate, and design 03's own provenance table (`03:135`) correctly reflects the `minItems: 1` requirement and its consequence (the compile-error backstop "stops being the ordinary case").
- **`config/rbac/role.yaml`** grants `httproutes`/`httproutes/status` only (lines 104–120), with no `agentgatewaybackends`/`agentgatewaypolicies` grant anywhere in the file — exactly matching `03:216`'s claim.
- **`charts/assayd/values.yaml:87–90`** ships `gateway: {enabled: false, name: assayd}` — matching `03:169`'s claim that the chart carries the key with `false` shipped, not defaulted.
- **`charts/assayd/templates/NOTES.txt`** exists, matching `03:181`'s present-tense claim (correctly citing design 07 A6.9, which I independently confirmed added this file and its `gateway.enabled` branches).
- **ADR-0027's "four lines of spec"** citation (`03:333`) checks out against `docs/decisions/0027-crd-ergonomics.md`, rule 4: "A developer's first Agent is four lines of spec."
- **Design 07 A6.1, A6.2 and A6.8** citations (`03:218`, `03:206`, `03:523`) were each read at source (`docs/designs/07-*.md`) and accurately describe what those amendments record: the RBAC/admission-policy ordering failure, the parentRef-namespace mistake, and the MCP tool-list-filtering measurement, respectively.
- **The twelve `ConditionType` constants** the design names (`PolicyCompileFailed`, `PolicyApplyIncomplete`, `PolicyInputDrifted`, `CapabilityUnavailable`, `LLMFallbackUnavailable`, `GatewayIncompatible`, `GovernanceSkipped`, `BudgetEnforcementDegraded`, `RevisionRecordUnreadable`, `RevisionRecordUnsupported`, and others) are genuinely present in `api/v1alpha1/agent_types.go:398–471` — this slice of the design has actually landed in code, and the design does not overclaim what those constants do.
- **The reconciliation between §3.1 and §5 on "producing design absent" vs. "a declared binding's producer absent"** (the inventory's pre-consolidation A-8) is now correctly resolved: §3.1 (`03:150–158`) states both outcomes are kept and distinguishes them by whether the Agent requested the binding, and §5's two rows (`03:740`, `03:752`) match that distinction rather than contradicting each other as the pre-consolidation body did. This is a genuine fix, not a survival.
- **`go test ./test/conformance/...` and the Makefile wiring** were confirmed directly (`Makefile:44,58-59,75`; directory listing of `test/conformance/`), independent of the design's own claims about them.

**Files most relevant to acting on this critique**: `/Users/jagveersingh/Developer/plume/docs/designs/03-policy-compiler.md`, `/Users/jagveersingh/Developer/plume/docs/designs/reviews/03-consolidation-inventory.md`, `/Users/jagveersingh/Developer/plume/api/v1alpha1/agent_types.go`, `/Users/jagveersingh/Developer/plume/charts/assayd/values.yaml`, `/Users/jagveersingh/Developer/plume/cmd/operator/main.go`, `/Users/jagveersingh/Developer/plume/config/rbac/role.yaml`, `/Users/jagveersingh/Developer/plume/docs/designs/16-evalsuite.md`, `/Users/jagveersingh/Developer/plume/docs/designs/02-agent-crd.md`, `/Users/jagveersingh/Developer/plume/docs/research/mcp-session-statefulness-2026-09.md`, `/Users/jagveersingh/Developer/plume/Makefile`, `/Users/jagveersingh/Developer/plume/test/conformance/`.
