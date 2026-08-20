# Review: Design 12 — The ontology/v1 specification

- **Verdict**: **REVISE**
- **Reviewed**: `docs/designs/12-ontology-spec.md` (draft, 2026-08-20)
- **Independence note**: reviewed in an independent session that did not author the draft.
- **Method**: all 8 lenses, with focused scrutiny (per this component's nature) on the type system's extraction-mapping soundness, the invariant/selector grammar's decidability and well-formedness, and the derivation table's status as real contracts for consumers (13/14/15/16) that don't exist yet. Consistency vs designs 01–10 (+ amendments), ADRs 0017/0020/0023-pending, `docs/research/connectors-2026-08.md` §graphiti.

## Findings

### 1. MAJOR — `ref(Entity)` attrs duplicate the relation mechanism with unresolved semantics, and the design's own modeling rule argues against them

`12-ontology-spec.md:38,42` vs `:48` and design 01 §3.5. The example ontology models Policy succession *twice*: `successor: "ref(Policy)?"` as an attr here, and `Policy SUPERSEDES Policy` as a relation in the approved design 01 sketch. A scalar reference to another entity *is* an edge — the design's own D1 already rules "an array-shaped fact is a relation"; the identical logic applies to scalar refs, and allowing both creates: (a) **extraction ambiguity** — the LLM extractor (design 13) must decide per fact whether it's an attr or an edge, and two ontologies can model the same domain incompatibly; (b) **invariant blindness** — `acyclic` operates on relations, so a supersession cycle encoded through `successor` attrs passes the gate the relation was declared to enforce; (c) **unresolved value semantics** — what does a ref attr *store* (the target's key tuple? a node id? unresolved surface text pre-entity-resolution?) and what happens when resolution fails? None specified, yet the Pydantic derivation must answer all of it.

**Fix**: drop `ref()` from v1 (refs are relations — one modeling path, consistent with D1; revisit with ontology/v2 evidence), or fully specify it: stored value = target key tuple, resolution failure ⇒ the element fails the batch's referential gate, and every graph-shaped invariant (`acyclic`, `path_terminates`) explicitly does or does not traverse ref attrs. Dropping is the recommendation — it also simplifies finding 3.

### 2. MAJOR — the invariant grammar references relations by bare VERB, which the type system itself makes ambiguous

`12-ontology-spec.md:48,61-65`. §3.3 declares VERB uniqueness **per (Subject, VERB, Object)** — explicitly allowing the same VERB on different entity pairs (`Policy COVERS Procedure`, `Plan COVERS Region`). But the invariant examples then reference relations by bare VERB: `{type: acyclic, rel: SUPERSEDES}`, `{type: required_edge, …, rel: COVERS}`, and `path_terminates` via `NEXT|BRANCH` — under-determined the moment a VERB is reused. The same section's `cardinality` example uses the full-triple string form, so the spec exhibits *two* reference syntaxes without defining either as the norm or the disambiguation rule. For the document whose §4 promises "selector validity" checking, the selector-for-relations is itself unsound.

**Fix**: define one canonical relation reference — the full triple string (`"Policy SUPERSEDES Policy"`), with bare VERB permitted *only* when globally unique (validation error otherwise, JSONPath-located). Apply it uniformly to `rel:`, `via:`, and the `cardinality` invariant; regenerate the examples.

### 3. MAJOR — the Pydantic derivation is a contract in name only: no normative mapping rules for the consumers that will be built against it

`12-ontology-spec.md:100-110,132`. §3.8 declares the derivations "contracts, not conveniences", and §1 puts "the derivation contracts" in scope — but only the JSON-Schema derivation is actually specified. The extraction-critical one — entities+relations → **Pydantic entity/edge models** ([graphiti classifies extracted entities against Pydantic types and populates typed attrs](https://help.getzep.com/graphiti/core-concepts/custom-entity-and-edge-types), per the landed research) — has no normative rules: how enums map (Literal vs Enum class), how `date`/`datetime` and `?`-optionality map, what an *edge model* looks like (class per (S,V,O) triple? attrs? naming convention?), how entity+attr descriptions compose into the docstring that becomes the extraction prompt (D4 makes descriptions load-bearing — their *composition* is part of extraction quality), and what `ref()` becomes (finding 1). Golden fixtures ship (§6), but a contract defined only by golden files is reverse-engineered, not specified — and designs 13 and 14 will be written against this before any of them exists to push back.

**Fix**: add the normative mapping table (one subsection: attr-type → Pydantic type, edge-model shape and naming, docstring composition rule, optionality/None semantics), with the goldens demoted to what they should be — regression tests *of* the rules. Same treatment in one line each for the invariant-query and golden-set derivations (what is promised: e.g. "one executable check per invariant instance, evaluated on the staging version at commit" / "one seed item per probe with its `via` and matchers carried through").

### 4. MINOR — two cardinality mechanisms with no defined relationship

`12-ontology-spec.md:51,65`. Relations may declare `cardinality: "1..*"` (§3.3) *and* a `cardinality` invariant type exists (§3.4). Is the §3.3 field enforced at commit like an invariant, advisory documentation, or sugar? **Fix**: define the lowering — the relation-level field compiles to a cardinality invariant instance (single enforcement path, one implementation in providers); the invariant form remains for selector-scoped cases.

### 5. MINOR — the `via:` alternation grammar is used but never defined

`12-ontology-spec.md:63`. `path_terminates … via: NEXT|BRANCH` introduces `|`-alternation that appears nowhere in the grammar definitions ("each built-in type's parameters are fixed by this spec" — but this parameter's syntax isn't). Decidability is fine (termination over finite graphs); well-formedness isn't. **Fix**: define `via` as a list of relation references (finding 2's canonical form), with `A|B` string sugar explicitly normalized to the list — or drop the sugar.

### 6. MINOR — probe `generate:` needs a closed generator set, or packs stop being data

`12-ontology-spec.md:93`. `generate: per_branch` is "pattern-driven" — but who defines generator types? If pattern packs can introduce generators, the probe engine (15) must execute pack-provided *logic*, violating the packs-are-data rule the whole charter rests on. **Fix**: ontology/v1 defines a closed built-in generator set (`per_branch`, plausibly `per_entity_sample`), each with fixed parameters like invariant types; pattern packs may *use* them, never define them; unknown generators fail validation. New generators = contract revision, same as invariant types.

### 7. MINOR — `migrates:` is a one-word sketch of the spec's hardest grammar

`12-ontology-spec.md:114`. Breaking changes "require an explicit `migrates: {renames: {...}}` map" — renames of what, keyed how? Entities, attrs (dotted paths?), relations (the finding-2 triple form?), enum values? And renames are only one breaking-change class — the rule also names remove/retype/tighten, which a renames map can't express (is `migrates` only for renames, with removals just… allowed if declared?). **Fix**: define the map shape (`entities: {Old: New}`, `attrs: {"Policy.old": "Policy.new"}`, `relations: {"S OLD O": "S NEW O"}`, `enums: {...}`) and state what non-rename breaks require (explicit `removed:` acknowledgment list is enough — the point is loud intent, which is this design's own philosophy).

### 8. MINOR — no Security section; `custom` invariants are a query-injection surface into providers

`12-ontology-spec.md` (structure) vs TEMPLATE §6. The template requires a Security section; a *document spec* still has one to write: who may push ontology docs (ConfigMap RBAC / signed OCI + admission — mostly exists in 01/07, cite it), and the sharp edge — `{type: custom, provider_query: "…"}` is an arbitrary query string executed by the provider **at commit, with admin-side privileges**. `portable: false` marks it for conformance, not for safety. **Fix**: add the section; for custom queries state the containment (providers execute them read-only against the staging version under the commit's scope; a compliance/hardened profile may deny `custom` outright — one values flag).

## Lens summary

1. **Doctrine**: clean — a contract artifact, 0 pods, everything expressed in it is fast-plane.
2. **Charter**: correct (contract like kgp, ledger-listed per 07); finding 6 is the one place pack-as-data discipline could leak.
3. **Contract consistency**: strong against 01/03/05 — `require_scope` operationalizes design 01 A1's provider-side scope enforcement neatly, bundles/recipes match ADR-0017's closed sets, evolution honors 01's immutable versions. Findings 1/2/3 are internal soundness, not cross-design conflict.
4. **Hidden dependencies/circularity**: none structural; the self-contained pattern instantiation (D2 — copied, not referenced) deliberately *removes* a runtime dependency, and the reviewed-diff upgrade flow honors CLAUDE rule 6.
5. **Failure modes**: the doc-drift row ("impossible: snapshots embed their doc") is the right kind of impossible; table otherwise adequate for a spec.
6. **Security**: finding 8.
7. **Research freshness**: the graphiti-Pydantic claim is landed and cited (verified in the connectors note); nothing else external is load-bearing.
8. **Testability**: the spec-ships-executable posture (schema + violation corpus + goldens + round-trip byte-stability) is exactly right — finding 3 is about making the goldens tests *of* stated rules rather than the rules themselves.

## Disposition

**REVISE.** This is close to a very good contract: the closed type system, mandatory descriptions-as-prompt-substrate (D4), self-contained pattern instantiation, and loud evolution rules are all correct instincts. The three MAJORs share one root — the spec still contains under-determined constructs (`ref()`, bare-VERB references, unstated derivation rules) in exactly the places where downstream designs 13–16 will harden assumptions first and ask questions never. Tighten those and the rest is completion work.

VERDICT: REVISE — 8 findings
