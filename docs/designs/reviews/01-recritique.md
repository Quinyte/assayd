# Independent re-critique: Design 01 — KnowledgeGraphProvider contract (`kgp/v1alpha1`)

- **Verdict**: **REVISE** — 10 findings (1 blocker, 6 major, 3 minor)
- **Reviewed**: `docs/designs/01-knowledgegraph-provider-contract.md` (status *approved*, amendments A1–A4)
- **Independence**: this is the independent pass design 01 never had — its original critique ran in the session that authored it and carried an explicit caveat. Treated here as a fresh draft.
- **Method**: all 8 lenses, weighted toward the five requested areas: sufficiency/minimality for the five named consumers, version-endpoint semantics under concurrency, error-taxonomy coverage, conformance-suite implementability, and whether A2 opens a hole. Baseline: designs 02–27 + amendments, ADRs 0001–0026.

**Framing**: this contract is consumed by agents, packs, design 14 (ingestion), design 15 (probes), design 16 (eval datasets) and design 13 (the reference adapter). The findings below cluster on one root cause: **the contract models versions as URL paths but never models them as objects** — it has no version *state*, no version *listing*, and no version *lifecycle interlocks*. Everything downstream assumed those existed.

## Findings

### 1. BLOCKER — A2's per-version admin grant is unenforceable by the mechanism that must enforce it, and `write_batch` has no stated precondition barring writes to the **active** version

`01-…md:192` (A2) vs `:39` (§3.2) and `:29` (§3.1). A2 grants "a route scoped to a single admin tool on a **single staging version** (e.g. a continuous-ingestion reader gets `write_batch` on `<graph>@vN+1` only)". Three facts make that grant impossible to honor as written:

1. **The admin endpoint is not version-scoped**: `/kgp/<graph>/admin/mcp` — "not versioned; operates ON versions" (§3.2). The target version is therefore a **field in the request body**.
2. **The gateway must not parse MCP bodies** — §3.1's whole routing premise, made binding by design 01 A1 and ADR-0020 ("The gateway does not parse MCP bodies"). It can match `Mcp-Name: kg.admin.write_batch` and the graph from the path; it *cannot* see the version.
3. **`write_batch` has no stated target precondition** (§3.4: "upsert nodes/edges with mandatory `provenance`"). Nothing says it accepts only versions in a staging state.

So a principal granted `write_batch` for `<graph>@vN+1` can in fact call `write_batch` against **any version of that graph, including the active one** — and design 11 hands exactly this grant to a *pack-provided, third-party reader image* running as a continuous-ingestion worker. The immutable-snapshot model, which every downstream guarantee rests on (rollback is "pure routing", conformance point 7 asserts prior versions are stable, design 15 promotes only after probes), has no enforcement behind it at the one surface that mutates data.

This is the blocker because it is silent: nothing errors, nothing alerts, and the corruption lands in a version agents are actively querying.

**Fix** — any one closes it, and (a) is the design's own trick applied where it was omitted:
- **(a) Version-scope the admin path**: `/kgp/<graph>/admin/<version>/mcp`. The gateway then enforces per-version grants by route, exactly as it enforces per-version query binding. `begin_version` (which has no version yet) stays on a graph-level path with platform-only authz.
- **(b) Inject an admin-scope header** (`X-Assayd-Admin-Scope: write_batch@vN+1`) and have the provider enforce it — the A1 pattern, reused.
- **(c) Regardless of the above**: state that `write_batch`/`load_artifact` accept only versions in `staging` state and reject otherwise with a new closed error (see finding 6). Defense in depth, and it is the provider-side invariant that makes the whole model true rather than conventional.

### 2. MAJOR — no version state, so an agent can bind an unpromoted or quarantined version

`01-…md:42` and `:121`. Version-scoped endpoints exist precisely so "an agent **cannot** name a version at call time" — but the agent (via its CR) *does* name a version at **bind** time, and nothing checks what that version is. `graphRef: {name, version: v13}` compiles to a route to `/kgp/<graph>/v13/mcp`, and §4's bind validation only "validates the endpoint serves `kgp/v1alpha1` at that version (via `kg.schema`)" — which a staging version answers perfectly well.

Consequences: an agent can be routed to a version that has not passed `commit_version` invariants, has not passed probes, or has been **quarantined** for failing them (design 13 §3.4 keeps quarantined namespaces queryable "for inspection"). The entire promotion gate — design 15's central purpose, and the `ProbesPassing` condition — protects only the `active` pointer, which pinned binds deliberately bypass.

**Fix**: give versions a state (`staging | active | superseded | quarantined`), expose it in `kg.schema`'s version metadata, and make the operator's bind validation refuse anything but `active`/`superseded` — with `quarantined` reachable only under platform identity. Pairs naturally with finding 3's listing tool.

### 3. MAJOR — nothing enumerates versions, though three consumers require it

`01-…md:78` and design 08 §3. The contract offers `drop_version` and assigns retention to the operator ("retention policy enforced by operator"), and design 08 ships a `assayd kg versions` verb — but **no tool lists versions**. The operator cannot compute what to retain or drop; the CLI verb has no contract behind it; and design 13's fallback ("version routing table rebuilt from namespace listing at startup") is adapter-internal, not contract surface, so it works only for that one provider.

Answering "what versions exist" from KG CR status instead would put the operator's record in competition with the provider's reality (design 13 states "namespaces are the truth"), and breaks entirely for BYO providers whose versions were created out of band.

**Fix**: add `kg.admin.list_versions` → `[{version, state, created_at, promoted_at, embedder, element_counts?}]`. It closes findings 2, 3 and 4 together, and the surface stays honest at 6 + 7.

### 4. MAJOR — `drop_version` has no in-use interlock, so retention can delete a version agents are bound to

`01-…md:78,124`. `drop_version` "delete a non-active version" — the only stated guard is *active*. But rollback (§4.4) works by repointing bindings to `vN−1`, and agents legitimately pin superseded versions for long periods. Retention driven by the operator can therefore delete a version an Agent CR still names, converting every query to `KG_VERSION_GONE`. The error being typed makes the outage *visible*, not *prevented*.

The platform gets this right elsewhere and the contrast is instructive: design 18 **refuses** pack uninstall while a filter is attached or a registration is referenced, and lists the dependents. The contract's most destructive operation has weaker protection than a pack uninstall.

**Fix**: state the interlock — `drop_version` refuses while any Agent CR binds the version (the operator knows its bindings) and while any in-flight query holds it; expose the refusal as a typed error and surface pinned-version retention pressure as a condition on the KG CR.

### 5. MAJOR — version-lifecycle concurrency is unspecified at three points

Requested scrutiny area; all three are genuinely open in the text.

- **`begin_version` allocation** (`:73`): "open staging version `vN+1`" — from what, atomically? Design 14's triggers include `kg push`, a Connector schedule, connector events and drift remediation (design 14 §3), so two builds racing is ordinary. Both compute `vN+1` from the same `vN`. Is the second a conflict, a no-op, or does it get `vN+2`? DBOS's deterministic workflow id (`build-<graph>-vN+1`) dedups two runs *with the same name*, which is precisely what two different triggers would produce — meaning the platform's protection here is accidental and would collapse the moment version allocation is anything but a counter.
- **Concurrent writers to one staging version** — A2 exists to create a second writer (a continuous-ingestion worker alongside pipeline batches). `(batch_id, seq)` idempotency makes *replay* safe but says nothing about *interleaving*: last-write-wins per element, or first? The contract states no merge or ordering semantics, so two conforming providers can produce different graphs from the same inputs.
- **`promote` atomicity across adapter replicas** — design 13 §4 scales the adapter "for read QPS". `promote` "flips the adapter's version routing table"; nothing says the flip is atomic cluster-wide. Version-pinned queries are unaffected (the design's real strength), but `drop_version`'s refuse-if-active check can then be evaluated against a stale view — one replica accepts a drop of the version another considers active.

**Fix**: state single-flight-per-graph `begin_version` with server-side allocation (or explicit `from`/`as` parameters and a documented conflict error); state write merge semantics; state that active-marking is atomic and that `drop`'s guard evaluates against the authoritative record, not a replica cache.

### 6. MAJOR — the closed error set is incomplete, and one approved design already returns a code it doesn't contain

`01-…md:64`. The set is `KG_NOT_FOUND · KG_SCOPE_DENIED · KG_VERSION_GONE · KG_BUNDLE_UNKNOWN · KG_BUDGET_EXCEEDED`, and portability rests on it being closed. Gaps:

- **No `KG_UNAVAILABLE`** — yet design 13 §5 promises "FalkorDB down | **All tools return typed unavailable**". An approved design returns an error the contract does not define; agents cannot distinguish "backend down" (retry) from "not found" (don't).
- **No commit-failure code or shape** — `commit_version` "returns violation list" (§3.4). Is a failed commit an MCP error or a successful call with a failure payload? Design 14's Gate B branches on it; conformance point 8 asserts it. Undefined shape for a decision two designs make.
- **No staging-state violation code** — needed by finding 1(c).
- **`KG_BUDGET_EXCEEDED` has no defined trigger**: gateway budgets are enforced at the gateway (ADR-0020), and bundle overflow **truncates** rather than errors (§5: "Provider truncates … sets `truncated: true` — never silent"). A code in a closed set with no producer is a portability trap — providers will invent uses for it.

**Fix**: add `KG_UNAVAILABLE`, `KG_VERSION_NOT_STAGING`, and a defined commit-failure representation; either give `KG_BUDGET_EXCEEDED` a stated trigger or remove it.

### 7. MAJOR — the conformance definition excludes BYO read-only providers that §9 and the architecture promise to support

`01-…md:158,162,166`. "A provider 'supports `kgp/v1alpha1`' **iff the suite passes**", and suite point 8 is the full admin lifecycle (begin → write → commit-with-violation → promote). But `provider: byo` is documented as "**any MCP endpoint**" (architecture §04) and §9's no-lock-in argument depends on third parties adding providers cheaply. A read-only corporate graph — the archetypal BYO case, and the one design 01 §9 lists as starting point 3 — can never implement `begin_version`/`promote`, so it can never claim support, so no agent can portably bind it.

**Fix**: define **conformance profiles** — `query` (tools 1–7 + the A1 scope battery + A3's walk case) and `full` (adds the admin lifecycle) — and make the KG CR/`kg.schema` declare which profile a provider satisfies. Version-mutating platform features (design 14 ingestion, design 15 promotion) require `full`; binding and querying require `query`. One paragraph, and it makes the BYO claim true.

### 8. MINOR — four ways the suite would let a non-conforming provider through, or fail a conforming one

`01-…md:149-162`.
- **Point 2's threshold is unspecified** ("recall ≥ threshold"): each implementer picks their own, so the gate means nothing until a number ships with the fixture.
- **Point 7 over-asserts**: "write v2, assert v1 responses **byte-stable**" requires determinism the contract mandates only for *bundles* (§3.5). `kg.search` returns floating-point `score` and has no stated tie-break ordering, so a conforming provider can fail point 7 for reasons the contract permits. Either mandate stable ordering + score formatting for `search`, or scope point 7 to bundles and citation sets.
- **No error-code battery**: portability is a stated contract property, yet nothing asserts that an unknown bundle yields `KG_BUNDLE_UNKNOWN`, a dropped version `KG_VERSION_GONE`, or a downed backend the (missing) unavailable code. A1 added scope-denial cases; the rest of the taxonomy is untested, so a provider can return provider-native errors and still pass.
- **Point 9 is not measurable on the fixture**: a p95 latency budget against "~40 nodes" is passed by any implementation, including one that would collapse at real corpus size. Fine as advisory — but then say the suite proves *conformance, not fitness*, because §8's "iff" currently implies otherwise.

### 9. MINOR — the contract states its own tool count three different ways

`01-…md:67` (§3.4 heading: "Admin surface — **five** tools"), the table beneath it (**six** rows: `begin_version, write_batch, commit_version, promote, load_artifact, drop_version`), and `:173` (§10: "ADR-0017 (kgp/v1alpha1 contract: **6+5** tools…)"). ADR-0017 as recorded says **six-tool admin surface**. For the document that defines a closed tool set, the count is the one thing that must be unambiguous.

### 10. MINOR — §3.5's ontology example is superseded by design 12 and no amendment says so

`01-…md:109-113`. The example's probe has no `via` (design 12 §10 A1 makes it **required** — "a probe without `via` fails validation") and uses `expect: {answer: "no"}` where design 12 §3.6 defines matcher objects (`{contains: "no"}`); there is no `health.pass_threshold`. Design 12 is the normative owner, and the architecture's §04 example was already corrected for exactly this — but the *contract document* still ships a non-conforming ontology as its illustration. A4 set the precedent for handling this (superseding the Graphiti appendix in place); this needs the same treatment.

## Lens summary

1. **Doctrine/charter**: clean — 0 pods from the contract, provider slot correct, MCP-only admin (Q2) is well-argued and the `load_artifact` answer to the throughput case is the right shape.
2. **Sufficiency & minimality** (requested): the six *query* tools are well-chosen and genuinely minimal — `get_context_bundle` as the workhorse with `structure`/`text` split is the strongest idea in the document, and A3's walk case makes it testable. The **admin surface is insufficient**: findings 2, 3, 4 are all the same missing concept (versions as first-class objects with state and enumeration), which four consumers silently assumed.
3. **Contract consistency**: findings 6, 9, 10 — plus the good news that A1–A4 are each properly recorded with their supersessions explicit, which is more discipline than most designs in the corpus showed.
4. **Concurrency/hidden dependencies** (requested): finding 5; the version-scoped-endpoint decision genuinely does make the *query* path race-free, which is why the unspecified *admin* path stands out.
5. **Failure modes**: the table covers the provider-facing cases well (truncation-never-silent, quarantine-not-degrade); missing rows for admin authz denial, concurrent-begin conflict, and drop-of-bound-version.
6. **Security** (requested, A2): finding 1 is the blocker; A1's scope model is otherwise sound and its conformance battery is the right enforcement.
7. **Research freshness**: MCP 2026-07-28 targeting, the `Mcp-Method`/`Mcp-Name` routing premise, and the FalkorDB SSPL analysis (Q1) all hold and are honestly caveated; Q1 is a model of how to document a license risk.
8. **Testability** (requested): finding 8; the suite's *shape* is right (ships with the contract, versioned together, "iff the suite passes") and points 4, 5 and 7 encode genuinely hard properties — it needs thresholds, an error battery, and profiles rather than redesign.

## Disposition

**REVISE.** The query surface, the version-scoped-endpoint decision and the determinism requirements have held up under two years of downstream design — every consumer built against them without needing a change, which is the strongest evidence a contract can have. What never got tested is the **admin half**: it lacks version state, version listing, lifecycle interlocks, concurrency semantics, and — after A2 stretched it to admit third-party writers — an enforceable authorization story. Finding 1 is a blocker because a pack-supplied reader image can silently overwrite live graph data; findings 2–5 are the missing object model behind it. The fixes are additive (one tool, one state field, one path change, three error codes, conformance profiles) and none disturbs the query surface that agents and packs already write against.

01: VERDICT REVISE — 10 findings

---

## Fix verification (2026-08-22)

- **Verdict**: **REVISE** — 9 outstanding. **The BLOCKER is genuinely closed**; 8 of 10 findings fixed, 1 partial, 1 not fixed (and made worse by its own amendment).
- **Verified against**: design 01 r2 (A5, A6), design 13 r2, design 14, design 20, ADR-0017.

### The blocker (f1) — closed

The fix works, and it works for the right reason. `/kgp/<graph>/admin/<version>/mcp` puts the version **in the path**, so A2's per-version grant becomes a route match on `(path, Mcp-Name)` — the gateway never needs to read a body to know which version a write targets, which was the exact impossibility. Lifecycle tools (`begin_version`, `list_versions`, `promote`, `drop_version`) correctly stay graph-level under platform identity, so a granted principal cannot reach them. The staging-only precondition on `write_batch`/`load_artifact` with `KG_VERSION_NOT_STAGING` is real defence in depth: even a mis-issued route cannot mutate the active graph. Design 11's claim ("a per-connector, per-staging-version route scoped to `write_batch` only") is now implementable rather than aspirational.

Two completeness gaps remain around it (R1, R2 below) — neither reopens the hole.

### Per-finding disposition

| # | Status | Evidence |
|---|---|---|
| **f1** BLOCKER | **Fixed** | §3.2 three-path split + §3.4 staging precondition + `KG_VERSION_NOT_STAGING`; A5.1 states the reasoning. See R1/R2. |
| **f2** version state | **Fixed** | `state ∈ staging\|active\|superseded\|quarantined` in `kg.schema`; A5.2 refuses binds to anything but `active`/`superseded`, `quarantined` platform-only. (§4's bind step not updated — R4.) |
| **f3** enumeration | **Fixed** | `kg.admin.list_versions` with the recommended shape `[{version, state, created_at, promoted_at, embedder, element_counts?}]`. |
| **f4** drop interlock | **Fixed** | `KG_VERSION_IN_USE`; refused while any Agent CR binds it or an in-flight query holds it; guard reads the authoritative record, not a replica cache; retention pressure surfaces as a KG CR condition. |
| **f5** concurrency | **Fixed** | All three sub-points: `begin_version` single-flight with `KG_VERSION_CONFLICT` and provider-side allocation; `write_batch` merge stated (last-write-wins per element key, batch-atomic); `promote` atomic with previous-active → `superseded` in the same operation. New interaction in R6. |
| **f6** error taxonomy | **Fixed** | `KG_UNAVAILABLE` (design 13's "typed unavailable" now exists), `KG_COMMIT_REJECTED` with `data.violations` (commit-failure shape defined), `KG_VERSION_NOT_STAGING`, `KG_VERSION_IN_USE`, `KG_VERSION_CONFLICT`; `KG_BUDGET_EXCEEDED` **removed** with the right reason ("budgets are the gateway's, never the provider's"). No other design referenced the removed code. |
| **f7** conformance profiles | **Fixed** | `query` / `full` declared in `kg.schema`; binding requires `query`, version-mutating features require `full`. (§8 body contradicts — R5.) |
| **f8** suite gaps | **PARTIAL — outstanding** | A6 adds state transitions, quarantined-not-bindable, `list_versions` completeness, drop-interlock refusal, and error mapping for **each new code**. Three of the four original sub-points remain — see R3. |
| **f9** tool count | **NOT FIXED — worse** | See R7. |
| **f10** stale ontology sketch | **Fixed** | A5.5 marks §3.5 superseded by design 12 and retained as illustration — the A4 precedent, correctly applied. |

### Outstanding

**R1 — `commit_version` has no state precondition.** `write_batch` and `load_artifact` are restricted to `staging`; `commit_version` — which lives on the same per-version path and *quarantines on failure* — is not. Calling it against an `active` version would re-run invariants on a live graph and could quarantine it. Exposure is limited (a `write_batch` grant doesn't carry `commit_version`, since the route matches on `Mcp-Name` too), but the precondition belongs in the same sentence as its siblings.

**R2 — `begin_version`'s `from` parameter was dropped in the rewrite.** The r1 row read "open staging version `vN+1` (**from empty or copy-on-write of `vN`**)"; the r2 row describes allocation and single-flight but no longer documents `from`. Design 13 r2 §3.1 calls `begin_version(from: vN)` and maps it to `GRAPH.COPY vN vN+1` — so the contract no longer documents a parameter its reference adapter depends on. An editing casualty of an otherwise-good fix; restore it alongside the allocation rule (the two are compatible: the caller names the *source*, the provider allocates the *number*).

**R3 — f8 is three-quarters open.** A6 covers the new surface well, but the original sub-points survive: (a) point 2's recall threshold is still unspecified, so each implementer picks their own gate; (b) point 7 still asserts v1 responses are **byte-stable**, a determinism the contract mandates only for bundles — `kg.search` returns floats and has no stated tie-break, so this can fail a *conforming* provider; (c) point 9's latency budget against ~40 nodes still can't distinguish an implementation that would collapse at corpus scale. Also, A6 asserts error mapping for "each **new** code" — the pre-existing codes (`KG_NOT_FOUND`, `KG_BUNDLE_UNKNOWN`, `KG_VERSION_GONE`) still have no conformance assertion.

**R4 — §4's bind step wasn't updated for f2.** It still reads "validates the endpoint serves `kgp/v1alpha1` at that version (via `kg.schema`)" — the state check that A5.2 makes the whole point of f2 is absent from the path description that implements it. (Design 02's bind behaviour is likewise unamended, though A5.2 arguably owns the rule.)

**R5 — §8 contradicts A5.4 on the definition of conformance.** The body still says "A provider 'supports `kgp/v1alpha1`' **iff the suite passes**" with point 8 (the admin lifecycle) as an unconditional gate — which is exactly what A5.4 says a `query`-profile provider need not pass. The definitional sentence of the contract now has two answers.

**R6 — single-flight `begin_version` collides with design 14's 72-hour review park.** With one staging version per graph and design 14 §5 parking a build for up to `reviewTimeout: 72h` awaiting a human, no other build can start for that graph for three days — including design 20's automatic `KnowledgeStale` rebuild, which would now receive `KG_VERSION_CONFLICT`. Neither design 14 nor design 20 has that path. Single-flight is defensible; the interaction needs recording (and probably a "supersede the parked build" or "queue" rule).

**R7 — f9 not fixed, and the amendment added a fifth count.** The document now states its tool count five ways: §3.3 heading "**six** tools" (correct for query), §3.4 heading "Admin surface — **five** tools" (unchanged, now wrong by two), §10 "**6+5** tools" (unchanged), A5.3 "**7 query-side + 7 admin-side**" (admin is right; **query is 6, not 7**), and ADR-0017 "six-tool admin surface" (now stale — `list_versions` makes seven). A5.3 also declares §3.3's "six tools" language superseded when it is the one correct statement in the set. **Fix**: 6 query + 7 admin, stated once in §3.3/§3.4's headings, with §10 and ADR-0017 corrected.

**R8 — design 13's `kg.schema` row is stale against the new contract** (corpus): it returns `{contract, version, pattern?, embedder}` and now must also return `state` and `profile` (§3.3). One row in design 13 §3.3.

**R9 — ADR-0017 needs the admin-surface count and the new paths** (corpus): it records "six-tool admin surface" and `/kgp/<graph>/<version>/mcp` only. The blocker fix changed the URL structure, which is exactly the kind of thing an ADR exists to carry.

### Verdict

**REVISE.** The substantive work is done and done well: the blocker is closed by the elegant fix (the design's own version-scoping trick applied where it had been omitted), and findings 2–7 collectively add the version object model the contract was missing — state, enumeration, interlocks, concurrency semantics, error coverage, and profiles. What remains is finishing work, but it includes one finding that regressed (R7 — the count is now stated five ways, one of them introduced by the fix), one that is three-quarters open (R3), and two contract-vs-consumer mismatches that would bite the first implementer (R2's dropped parameter, R5's contradictory conformance definition).

01: REVISE — 9 outstanding

---

## r3 verification (2026-08-22)

- **Verdict**: **PASS** — 1 outstanding (leftover text in a provenance section; every authoritative statement is now correct)
- **Verified against**: design 01 r3 (A5–A7), design 13 r2, design 14, design 20, ADR-0017 (amended).

### Disposition of the nine R items

| R | Status | Evidence |
|---|---|---|
| **R1** `commit_version` precondition | **Fixed** | §3.4's row now carries the same guard as its siblings — "precondition: target MUST be `staging` … so invariants can never re-run against a live graph". |
| **R2** dropped `from` parameter | **Fixed** | `begin_version(from: vN \| empty)` — "the caller names the **source**, the provider allocates the **number**". Reconciles the restored parameter with the allocation rule exactly, and design 13's `begin_version(from: vN)` → `GRAPH.COPY` call is documented again. |
| **R3** f8 remainder | **Fixed, all four** | Point 2 now states the gate (**recall ≥ 0.9**, "so implementers do not each pick a gate"); point 7 is rescoped to bundle byte-stability plus `search`/`neighbors` result *sets*, with the reason ("byte-stability elsewhere would fail a conforming provider"); point 9 adds a **100k-element synthetic corpus** alongside the fixture; and point 5 now asserts **every** closed-set code, "pre-existing and new" — the sub-point A6 had missed. |
| **R4** §4 bind step | **Fixed** | Bind now validates profile *and* that the bound version is `active`/`superseded`, refusing `staging`/`quarantined`. |
| **R5** §8 conformance definition | **Fixed** | "supports `kgp/v1alpha1` at profile `query` iff points 1–7, 9 and 10 pass, and at profile `full` iff point 8 also passes" — the contradiction with A5.4 is gone and the definitional sentence is now precise about which points gate which profile. |
| **R6** park vs single-flight | **Fixed** (A7) | A parked build holds the staging slot; a competing `begin_version` — "including design 20's automatic `KnowledgeStale` rebuild" — gets `KG_VERSION_CONFLICT` naming the parked build, and the operator **queues rather than supersedes** (`RebuildQueued` on the KG CR). The right call: superseding a build a human is reviewing would discard the review. |
| **R7** tool counts | **PARTIAL — outstanding** | Three of five statements corrected; two stale ones survive. See below. |
| **R8** design 13's `kg.schema` row | **Fixed** (corpus) | Design 13 §3.3 now returns `{contract, version, state, profile, pattern?, embedder}` and declares profile `full`. |
| **R9** ADR-0017 | **Fixed** (corpus) | Amended 2026-08-22 with the corrected surface (6 query + 7 admin), the version-scoped admin path and its rationale, version state, profiled conformance, and the full error-set correction. The ADR now carries the blocker fix, which is where a decision of that weight belongs. |

### Outstanding

**R7 (carried) — two stale tool-count statements survive alongside the corrected ones.** The authoritative places are now right: §3.3 "Query surface — **6 tools**", §3.4 "Admin surface — **7 tools**", and ADR-0017's amendment ("**6 query + 7 admin**"). But:

- **§10 contains both answers in one sentence**: "…(r2: the surface is **6 query + 7 admin**; admin mutating tools moved to `/kgp/<graph>/admin/<version>/mcp`) (kgp/v1alpha1 contract: **6+5 tools**, version-scoped endpoints, ontology/v1)". The new parenthetical was inserted without deleting the old one.
- **A5.3 still reads "7 query-side + 7 admin-side"** and still declares "§3.3's 'six tools' language is superseded here" — the query side is six, and §3.3's heading is the statement that is *correct*, so the amendment supersedes the right answer with a wrong one.

Both are one deletion each, and §12-style provenance sections are exactly where stale numbers hide. Fix them and the count is stated once, correctly, everywhere.

### New issues introduced by r3

**None.** I checked the three edits most likely to break something: the `from`-parameter restoration is compatible with single-flight allocation (source vs number are different concerns); the profile split does not weaken the `full` profile (point 8 still gates it, and designs 14/15 still require it); and A7's queue-don't-supersede rule composes with design 14's park semantics without touching them. The new `RebuildQueued` condition is declared here and flagged as belonging to designs 14/20 — worth landing there eventually, but it is recorded rather than assumed.

### Verdict

**PASS.** The blocker closed at r2 and r3 completed it properly — the `commit_version` guard removes the last way invariants could run against a live graph, and the restored `from` parameter reconnects the contract to its reference adapter. R3's conformance work is the standout: a stated recall gate, a determinism assertion scoped to what the contract actually mandates, a corpus size that can fail a bad implementation, and an error battery covering every code. With ADR-0017 amended, the corpus is consistent end to end. One deletion of stale text remains.

01: PASS — 1 outstanding
