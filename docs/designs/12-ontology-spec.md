# Design 12: The ontology/v1 specification

- **Status**: draft — awaiting critique
- **Phase**: P2 · **Size**: M · **Date**: 2026-08-20
- **ADRs**: 0005, 0017 (kgp contract carries the document) · interfaces: 01 (served by `kg.schema`), 13 (drives Graphiti extraction), 14 (drives pipeline gates), 15 (probes), 16 (golden-set derivation), 19 (patterns instantiate it)
- **Research**: `docs/research/connectors-2026-08.md` §graphiti — custom entity/edge types in graphiti-core are **Pydantic models** passed to `add_episode`; the ontology→extraction mapping is therefore mechanical.

## 1. Purpose & scope

The full, normative definition of the `ontology/v1` document — the domain contract everything derives from (design 01 §3.5 sketched it; this design *is* it). In scope: the type system, each section's grammar and semantics, validation (JSON Schema + semantic checks), pattern lineage, evolution rules, and the derivation contracts (what probes/golden-sets/Pydantic models are generated from what). Out of scope: authoring UX (kg init wizard, design 08/13), storage (ConfigMap/OCI — design 01).

## 2. Doctrine & charter gates

- **Plane**: this is a **contract** (like kgp) — slow by nature, deliberately small; everything expressed *in* it is fast-plane data. Versioned `ontology/v1`, N/N−1, listed in the `plume-contracts` ledger (design 07).
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
      successor: "ref(Policy)?"   # typed reference (validated to an existing entity type)
    description: "A payer's coverage policy document"   # REQUIRED — it is the extraction prompt seed
```

Attr types (closed set v1): `str · int · float · bool · date · datetime · enum[…] · ref(EntityName)` + `?` optionality. No nesting, no arrays in v1 (an array-shaped fact is a relation — by design; recorded as the modeling rule).

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

### 3.4 Invariants (closed set + escape)

```yaml
invariants:
  - {type: required_edge, subject: "Policy[state=active]", rel: COVERS, min: 1}
  - {type: acyclic, rel: SUPERSEDES}
  - {type: path_terminates, from: Procedure, via: NEXT|BRANCH}
  - {type: unique_key, entity: Policy}
  - {type: cardinality, rel: "Decision BRANCH Step", min: 2}
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
  - generate: per_branch          # pattern-driven auto-probes (sop-decision-tree): one per BRANCH edge
```

### 3.7 Pattern lineage

`pattern: <name>@<version>` records instantiation from a knowledge pack. The instantiated doc is **self-contained** (the pack's template is copied in, then edited) — no runtime pack dependency; lineage exists for diffing (`plume kg diff --against-pattern`) and upgrade tooling (a pattern upgrade proposes a doc diff, human reviews — never auto-applied).

### 3.8 Derivations (contracts, not conveniences)

| Derived artifact | From | Consumer |
|---|---|---|
| JSON Schema validation | this spec (shipped `ontology-v1.schema.json`) | `plume kg push`, admission on the ConfigMap/artifact |
| **Pydantic entity/edge models** | entities+relations (+descriptions as docstrings) | Graphiti extraction (design 13) — mechanical, golden-tested |
| Invariant queries | invariants | `kg.admin.commit_version` gate (designs 01/14) |
| Probe set | probes (incl. `generate:` expansions) | probe engine (15), semantic readiness |
| Golden-set seeds | probes + bundles | EvalSuite derivation (16) |
| Scope metadata | entities + `require_scope` | policy compiler KG-scope (03) |

### 3.9 Evolution rules

- **Additive** (new entity/relation/attr`?`/bundle/probe): same major, new graph version — normal snapshot flow.
- **Breaking** (remove/rename/retype anything, tighten cardinality): requires an explicit `migrates: {renames: {...}}` map in the new doc *and* produces a new graph version; `kg diff` renders the break loudly. No in-place mutation, ever (design 01 versioning).
- `ontology/v2` (the contract itself) follows platform N/N−1 with the converter living in the operator.

## 4. Validation pipeline

Two layers, both at `kg push` *and* admission: (1) **structural** — JSON Schema; (2) **semantic** — referential closure (refs/relations name declared entities), selector validity, recipe-param schemas, probe matcher sanity, `require_scope ⊆ entities`, key attrs exist and are non-optional. Errors carry JSONPath locations. A doc that validates is *compilable*; whether the graph satisfies it is the pipeline's job (14).

## 5. Failure modes

| Failure | Behavior |
|---|---|
| Invalid doc pushed | Rejected at push/admission with JSONPath errors; existing versions untouched |
| Pattern pack upgraded | Nothing happens until a human runs the upgrade-diff flow (§3.7) |
| `custom` invariant on a provider that can't run it | Commit-time error naming the invariant; conformance's portability warning already flagged it |
| Doc drifts from deployed snapshot | Impossible: snapshots embed their doc verbatim (`kg.schema` serves it); the CR references *inputs*, versions freeze them |

## 6. Testing

The spec ships executable: `ontology-v1.schema.json` + a semantic-validation test corpus (valid + each violation class), golden Pydantic derivations, golden probe expansions (`generate: per_branch` on a fixture SOP graph), and round-trip (doc → kg.schema → doc, byte-stable). The kgp conformance suite (design 01) consumes the same fixture ontology.

## 7. Decisions for async review

- **D1 — No nested/array attrs in v1**: array-shaped facts are relations; keeps extraction, invariants, and diffs simple. Revisit only with ontology/v2 evidence.
- **D2 — Instantiated pattern docs are self-contained** (copied, not referenced) — no runtime coupling to packs; upgrades are reviewed diffs.
- **D3 — Probe citations required by default** (`cites: none` must be explicit).
- **D4 — Descriptions are mandatory on entities/relations** — they are the extraction prompt substrate, not documentation garnish.

## 8. Resulting ADRs

Folded into ADR-0023 (P2 knowledge layer) after critique PASS.
