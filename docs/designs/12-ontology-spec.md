# Design 12: The ontology/v1 specification

- **Status**: **approved** — critique PASS at r2 (reviews/12-review.md) · ADR-0023
- **Phase**: P2 · **Size**: M · **Date**: 2026-08-20
- **ADRs**: 0005, 0017 (kgp contract carries the document) · interfaces: 01 (served by `kg.schema`), 13 (drives Graphiti extraction), 14 (drives pipeline gates), 15 (probes), 16 (golden-set derivation), 19 (patterns instantiate it)
- **Research**: `docs/research/connectors-2026-08.md` §graphiti — custom entity/edge types in graphiti-core are **Pydantic models** passed to `add_episode`; the ontology→extraction mapping is therefore mechanical.

## 1. Purpose & scope

The full, normative definition of the `ontology/v1` document — the domain contract everything derives from (design 01 §3.5 sketched it; this design *is* it). In scope: the type system, each section's grammar and semantics, validation (JSON Schema + semantic checks), pattern lineage, evolution rules, and the derivation contracts (what probes/golden-sets/Pydantic models are generated from what). Out of scope: authoring UX (kg init wizard, design 08/13), storage (ConfigMap/OCI — design 01).

## 2. Doctrine & charter gates

- **Plane**: this is a **contract** (like kgp) — slow by nature, deliberately small; everything expressed *in* it is fast-plane data. Versioned `ontology/v1`, N/N−1, listed in the `assayd-contracts` ledger (design 07).
- **Pods/stateful deps**: none. **Primitives**: Artifact (the document), Resource (referenced by KG CR). ✓

## 3. The document, normatively

### 3.1 Header

```yaml
ontology: payer-policies          # DNS-label name
version: v12                      # graph-version label this doc produces (immutable once snapshotted)
pattern: policy-rules@1.0.0       # optional lineage: pattern pack + version (§3.7)
```

### 3.2 Entities — the type system

```yaml
entities:
  - name: Policy                  # UpperCamel, unique
    keys: [policy_id]             # ≥1; composite allowed: [payer_id, policy_id]
    attrs:
      policy_id: str
      effective_from: date
      state: "enum[draft,active,retired]"
      amount_limit: "float?"      # ? = optional
    description: "A payer's coverage policy document"   # REQUIRED — it is the extraction prompt seed
```

Attr types (closed set v1): `str · int · float · bool · date · datetime · enum[…]` + `?` optionality. **No `ref()` attrs** (r1 f1): a reference to another entity *is* an edge — model it as a relation (one modeling path; keeps extraction unambiguous and graph invariants sound). No nesting, no arrays (array-shaped facts are relations). Revisit only with ontology/v2 evidence.

### 3.3 Relations

```yaml
relations:
  - "Policy COVERS Procedure"     # 'Subject VERB Object' — VERB is SCREAMING_SNAKE, unique per (S,V,O)
  - rel: "Decision BRANCH Step"   # long form when attrs/cardinality needed
    attrs: {condition: str}       # typed like entity attrs
    cardinality: "1..*"           # optional: 1..1, 0..1, 1..*, *..*(default)
    description: "A conditional branch out of a decision point"
```

Both forms normalize to the long form. Subject/Object must name declared entities (or `*` for pattern-generic relations inside pattern packs only).

**Canonical relation reference (r1 f2)**: everywhere a relation is referenced (`rel:`, `via:`, cardinality invariants), the canonical form is the **full triple string** `"Policy SUPERSEDES Policy"`; bare VERB is accepted *only when globally unique* across the document (validation error otherwise, JSONPath-located).

### 3.4 Invariants (closed set + escape)

```yaml
invariants:
  - {type: required_edge, subject: "Policy[state=active]", rel: COVERS, min: 1}
  - {type: acyclic, rel: "Policy SUPERSEDES Policy"}
  - {type: path_terminates, from: Procedure, via: ["Step NEXT Step", "Decision BRANCH Step"]}   # via = list of canonical refs; "A|B" sugar normalizes to the list (r1 f5)
  - {type: unique_key, entity: Policy}
  - {type: cardinality, rel: "Decision BRANCH Step", min: 2}   # the §3.3 relation-level `cardinality:` field is sugar that LOWERS to this invariant — one enforcement path (r1 f4)
  - {type: custom, provider_query: "…", portable: false}     # escape (ADR-0017)
```

Selector grammar `Entity[attr=value]` (equality only, v1). Each built-in type's parameters are fixed by this spec; unknown types/params fail validation.

### 3.5 Bundles

```yaml
bundles:
  - name: next_step
    recipe: walk                  # walk | subtree | neighborhood_summary | timeline
    params: {machine: Procedure, edge: BRANCH, condition_attr: condition}
    require_scope: [Procedure, Step, Decision]   # entity types a caller must be scoped for (design 03 KG-scope)
```

Recipes are deterministic graph operations (ADR-0017; no provider-side LLM). Each recipe's param schema is fixed here; `require_scope` lets the provider enforce scope on bundles cheaply (design 01 A1).

### 3.6 Probes

```yaml
probes:
  - id: p-001                     # stable; referenced in review threads and dashboards
    q: "Does policy P-1042 cover CPT 97110 without prior auth?"
    via: {bundle: coverage_answer, params: {…}}   # optional: probe a bundle, not just search
    expect:
      answer: {contains: "no"}    # matchers: exact | contains | regex | numeric: {value, tol}
      cites: ["P-1042#4.2"]       # required unless `cites: none` is explicit — uncited probes are a smell
  - generate: per_branch          # generators are a CLOSED built-in set (r1 f6): per_branch · per_entity_sample — packs may USE them, never define them; unknown ⇒ validation error; new generators = contract revision
```

### 3.7 Pattern lineage

`pattern: <name>@<version>` records instantiation from a knowledge pack. The instantiated doc is **self-contained** (the pack's template is copied in, then edited) — no runtime pack dependency; lineage exists for diffing (`assayd kg diff --against-pattern`) and upgrade tooling (a pattern upgrade proposes a doc diff, human reviews — never auto-applied).

### 3.8 Derivations (contracts, not conveniences)

| Derived artifact | From | Consumer |
|---|---|---|
| JSON Schema validation | this spec (shipped `ontology-v1.schema.json`) | `assayd kg push`, admission on the ConfigMap/artifact |
| **Pydantic entity/edge models** | entities+relations (+descriptions as docstrings) | Graphiti extraction (design 13) — mechanical, golden-tested |
| Invariant queries | invariants | `kg.admin.commit_version` gate (designs 01/14) |
| Probe set | probes (incl. `generate:` expansions) | probe engine (15), semantic readiness |
| Golden-set seeds | probes + bundles | EvalSuite derivation (16) |
| Scope metadata | entities + `require_scope` | policy compiler KG-scope (03) |

### 3.9 The Pydantic derivation, normatively (r1 f3)

| Ontology construct | Pydantic mapping |
|---|---|
| entity `Name` | `class Name(BaseModel)`; **docstring = the entity description verbatim** (it seeds the extraction prompt) |
| attr `str/int/float/bool` | `str/int/float/bool` |
| attr `date` / `datetime` | `datetime.date` / `datetime.datetime` |
| attr `enum[a,b,c]` | `Literal["a","b","c"]` (values verbatim; no Enum classes — keeps prompts readable) |
| attr `T?` | `Optional[T] = None` |
| keys | listed in the model's docstring line `Keys: …` (extraction hint) + carried in model config for resolve (14) |
| relation `(S, VERB, O)` | `class SVerbO(BaseModel)` — name = `S + Verb.title().replace("_","") + O`; docstring = relation description; typed attrs as above |
| docstring composition | entity/relation description, then one line per attr: `attr_name (type): from enum values where applicable` — this composition **is** part of extraction quality (D4) and is golden-tested as a regression of these rules, not their definition |

One-line contracts for the other derivations: **invariants** → one executable check per invariant *instance*, evaluated against the staging version at `commit_version`, violations reported with the invariant's index+selector; **golden-set seeds** → one seed item per probe carrying `via` and matchers through unchanged (16 consumes verbatim).

### 3.10 Evolution rules

- **Additive** (new entity/relation/attr`?`/bundle/probe): same major, new graph version — normal snapshot flow.
- **Breaking** (remove/rename/retype, tighten cardinality): requires explicit acknowledgment in the new doc (r1 f7) — `migrates.renames` for renames: `{entities: {Old: New}, attrs: {"Policy.old": "Policy.new"}, relations: {"S OLD O": "S NEW O"}, enums: {"Policy.state": {old_val: new_val}}}`; non-rename breaks require a `migrates.removed:` list naming every removed/retyped element. Anything that disappears without appearing in `renames` or `removed` is a validation error — loud intent is the rule. Produces a new graph version; `kg diff` renders the break; no in-place mutation ever.
- `ontology/v2` (the contract itself) follows platform N/N−1 with the converter living in the operator.

## 4. Validation pipeline

Two layers, both at `kg push` *and* admission: (1) **structural** — JSON Schema; (2) **semantic** — referential closure (refs/relations name declared entities), selector validity, recipe-param schemas, probe matcher sanity, `require_scope ⊆ entities`, key attrs exist and are non-optional. Errors carry JSONPath locations. A doc that validates is *compilable*; whether the graph satisfies it is the pipeline's job (14).

## 5. Security (r1 f8)

Who may push: ontology docs travel as ConfigMaps/OCI artifacts under the existing RBAC + signed-artifact admission (designs 01/07); tampering is a signature/ownership event, not a schema question. The sharp edge is **`custom` invariants — an arbitrary query string executed by the provider at commit**: containment rules are (a) providers execute custom queries **read-only against the staging version under the commit's scope**, never against active versions; (b) `portable: false` marks conformance, not safety — so (c) hardened/compliance profiles may **deny `custom` outright** (one values flag, admission-enforced). Probe queries are engine-mediated and matcher-judged (design 15) — no injection surface.

## 6. Failure modes

| Failure | Behavior |
|---|---|
| Invalid doc pushed | Rejected at push/admission with JSONPath errors; existing versions untouched |
| Pattern pack upgraded | Nothing happens until a human runs the upgrade-diff flow (§3.7) |
| `custom` invariant on a provider that can't run it | Commit-time error naming the invariant; conformance's portability warning already flagged it |
| Doc drifts from deployed snapshot | Impossible: snapshots embed their doc verbatim (`kg.schema` serves it); the CR references *inputs*, versions freeze them |

## 7. Testing

The spec ships executable: `ontology-v1.schema.json` + a semantic-validation test corpus (valid + each violation class), golden Pydantic derivations, golden probe expansions (`generate: per_branch` on a fixture SOP graph), and round-trip (doc → kg.schema → doc, byte-stable). The kgp conformance suite (design 01) consumes the same fixture ontology.

## 8. Decisions for async review

- **D1 — No nested/array attrs and no `ref()` attrs in v1**: all entity-to-entity facts are relations — one modeling path (r1 f1). Revisit only with ontology/v2 evidence.
- **D2 — Instantiated pattern docs are self-contained** (copied, not referenced) — no runtime coupling to packs; upgrades are reviewed diffs.
- **D3 — Probe citations required by default** (`cites: none` must be explicit).
- **D4 — Descriptions are mandatory on entities/relations** — they are the extraction prompt substrate, not documentation garnish.

## 9. Resulting ADRs

Folded into ADR-0023 (P2 knowledge layer) after critique PASS.

## 10. Amendments

- **A1 (2026-08-20, from design 15 r1 f1/f3)**: (a) probe **`via` is required** — `via: {bundle, params}` or `via: {search: {query, top_k}}`; a probe without `via` fails validation; `q` is the display label, never the executable. Matchers are defined against the named output shape (bundle `text`/`citations`; search hit fields). (b) New `health: {pass_threshold: 1.0}` section — the promotion threshold is ontology content, versioned with its probes; the KG CR may tighten, never loosen.
- **A2 (2026-08-20, from design 19 r1 f1/f3/f4)**: (a) the **`walk` recipe's real parameter schema**: `{entry: <triple ref>, sequence: [<triple refs>], branch: <triple ref>, condition_attr: str, state?: <node-key input param>}` — semantics: start at `state` (or follow `entry` from the machine root when absent), advance along `sequence` edges, stop at the first node with `branch` edges and render their conditions into the bundle's `structure` payload (design 01's `next_step` shape). (b) **`per_branch` generator contract**: expands **same-version at probe-set derivation** — a *mechanical-readiness* check (every branch walkable and cited; truth is carried by hand-written probes and Gate A) — and emits `via: {bundle: <the pattern's walk bundle>, params: {state: <the branch's decision-node key>}}` so generated probes satisfy A1's via-required rule.
