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
- **(b) Inject an admin-scope header** (`X-Plume-Admin-Scope: write_batch@vN+1`) and have the provider enforce it — the A1 pattern, reused.
- **(c) Regardless of the above**: state that `write_batch`/`load_artifact` accept only versions in `staging` state and reject otherwise with a new closed error (see finding 6). Defense in depth, and it is the provider-side invariant that makes the whole model true rather than conventional.

### 2. MAJOR — no version state, so an agent can bind an unpromoted or quarantined version

`01-…md:42` and `:121`. Version-scoped endpoints exist precisely so "an agent **cannot** name a version at call time" — but the agent (via its CR) *does* name a version at **bind** time, and nothing checks what that version is. `graphRef: {name, version: v13}` compiles to a route to `/kgp/<graph>/v13/mcp`, and §4's bind validation only "validates the endpoint serves `kgp/v1alpha1` at that version (via `kg.schema`)" — which a staging version answers perfectly well.

Consequences: an agent can be routed to a version that has not passed `commit_version` invariants, has not passed probes, or has been **quarantined** for failing them (design 13 §3.4 keeps quarantined namespaces queryable "for inspection"). The entire promotion gate — design 15's central purpose, and the `ProbesPassing` condition — protects only the `active` pointer, which pinned binds deliberately bypass.

**Fix**: give versions a state (`staging | active | superseded | quarantined`), expose it in `kg.schema`'s version metadata, and make the operator's bind validation refuse anything but `active`/`superseded` — with `quarantined` reachable only under platform identity. Pairs naturally with finding 3's listing tool.

### 3. MAJOR — nothing enumerates versions, though three consumers require it

`01-…md:78` and design 08 §3. The contract offers `drop_version` and assigns retention to the operator ("retention policy enforced by operator"), and design 08 ships a `plume kg versions` verb — but **no tool lists versions**. The operator cannot compute what to retain or drop; the CLI verb has no contract behind it; and design 13's fallback ("version routing table rebuilt from namespace listing at startup") is adapter-internal, not contract surface, so it works only for that one provider.

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
