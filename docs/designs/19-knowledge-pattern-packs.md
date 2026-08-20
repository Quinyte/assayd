# Design 19: Knowledge pattern packs (`sop-decision-tree` first)

- **Status**: revised r2 — awaiting re-critique (r1 REVISE, 6 findings addressed; reviews/19-review.md)
- **Phase**: P2–P3 · **Size**: M · **Date**: 2026-08-20
- **ADRs**: 0008 (packs/sockets), 0005 · interfaces: 12 (patterns instantiate ontology/v1), 08 (`kg init --pattern` wizard), 14 (starter labels, resolve defaults), 15 (auto-probes), 09-precedent (OCI-artifact delivery pre-design-18)

## 1. Purpose & scope

The pattern layer that turns "a pile of documents of shape X" into "a graph of shape Y" (architecture §13): each pack bundles an ontology **template**, extraction guidance, probe generators usage, bundle recipes usage, starter labels, and wizard metadata. Defines the pack-content contract and specifies the flagship `sop-decision-tree` pack fully; the other four (`policy-rules`, `episodic-memory`, `entity-catalog`, `qa-corpus`) ship as instances of the same contract. Out of scope: pack installer mechanics (18; P1-style OCI delivery per design 09's precedent), ontology grammar (12 owns).

## 2. Doctrine & charter gates

- **Plane**: **pure fast-plane** — packs are data: YAML templates + label fixtures + wizard metadata. **No code in packs** (design 12 r2 closed the generator loophole: packs *use* built-in generators/recipes, never define them). A pattern that needs a new recipe/generator/invariant type is a contract-revision proposal, not a pack release — stated so the boundary survives contact with enthusiasm.
- **Pods/stateful deps**: none. **Primitives**: Artifact (the pack). ✓

## 3. The pack-content contract (`knowledge-pattern/v1`)

```
<pack>/
  pattern.yaml          # metadata: name, version, when-to-recommend reasoning (wizard-rendered),
                        #   source shapes (mime/kinds), band of corpus sizes it suits
  ontology.template.yaml# ontology/v1 doc with {{vars}} + pattern-generic '*' relations (12 §3.3)
  variables.yaml        # each: name, question (wizard prompt), type, default?, why (reasoning shown)
  labels/               # starter extraction labels for the pattern's fixture shapes (14 Gate A seed)
  fixtures/             # tiny sample corpus + expected graph (conformance + demos)
  resolve.yaml          # entity-resolution defaults (similarity band, key strategies) — data for 14
  README.md             # human doc: what shape in, what shape out, what agents get
```

Validation: `plume pack`-side schema check + **instantiation check** — the template with default variables must produce a valid ontology/v1 doc (design 12 validation) and its fixtures must build a graph passing its own probes on the reference provider. A pack that can't pass its own fixtures doesn't publish.

## 4. The `sop-decision-tree` pack, fully specified

- **Ontology template** (per architecture Fig 8, now normalized to design 12 r2 grammar):
  - Entities: `Procedure` (keys: [procedure_id]), `Step` (keys: [procedure_id, step_no]), `Decision` (keys: [procedure_id, decision_id]), `Role` (keys: [role_name]), each with extraction-bearing descriptions.
  - Relations (canonical triples): `"Procedure STARTS_AT Step"` (1..1) · `"Step NEXT Step"` (0..1) · `"Step NEXT Decision"` (0..1) · `"Decision BRANCH Step"` (attrs: {condition: str}; the min-2 constraint lives solely in the cardinality invariant below — relation-level `2..*` is not in 12's grammar, r1 f2) · `"Step PERFORMED_BY Role"` · `"Step ON_EXCEPTION Step"`.
  - Invariants: `path_terminates from: Procedure via: ["Procedure STARTS_AT Step","Step NEXT Step","Step NEXT Decision","Decision BRANCH Step"]` · `cardinality "Decision BRANCH Step" min: 2` · `unique_key` per entity · `required_edge Procedure STARTS_AT min:1`.
  - Bundles: `next_step` (recipe: walk; params per 12 §11 A2: entry="Procedure STARTS_AT Step", sequence=["Step NEXT Step","Step NEXT Decision"], branch="Decision BRANCH Step", condition_attr=condition, state as the caller input; require_scope: [Procedure, Step, Decision]) — the walk schema was amended to express exactly this traversal (r1 f1), and the kgp conformance suite gained the full-walk case (01 §11 A3) · `full_path` (recipe: subtree) · `who_does` (recipe: neighborhood_summary rooted at Role).
  - Probes: `generate: per_branch` — same-version **mechanical-readiness** expansion (every branch walkable + cited; 12 A2 defines the emitted `via`) — plus template slots for 3–5 hand-written known-answer probes the wizard insists on, **including their execution**: the wizard asks which bundle answers each question (default `next_step`; search as fallback) so collected probes carry the now-required `via` (r1 f3/f4).
- **Variables**: `domain_name`, `procedure_granularity` (per-document vs per-section, with reasoning), `exception_handling` (model exceptions? default yes), `roles_tracked` (bool).
- **Starter labels**: 12 synthetic SOP snippets with labeled Step/Decision/BRANCH extractions — a **smoke set that runs Gate A in advisory mode under `LabelsSparse`** (well below 14 r2's n≥200 authority threshold, r1 f5); it catches gross prompt breakage, and gating *authority* arrives via the label growth engine as the user's own labels accumulate. The pack README states this handoff in exactly these terms.
- **What agents get** (README + wizard copy): `next_step(state)` — walk, don't retrieve; answers cite their path; invariants guarantee no dead ends, no single-exit decisions.

## 5. The other four packs (contract instances, one line of shape each)

| Pack | Source shape | Distinctive template elements |
|---|---|---|
| `policy-rules` | policies/contracts/regulations | `SUPERSEDES` + `acyclic`, effective-date attrs, `applies_when` bundle |
| `episodic-memory` | tickets/incidents/conversations | event entities keyed by time, bitemporal pass-through attrs, `timeline` bundle |
| `entity-catalog` | product/org/CRM exports | entity-heavy, relation-light; `unique_key` everywhere; lookup bundles |
| `qa-corpus` | FAQs/wikis/support threads | Question/Answer entities, `ANSWERS` relation, `neighborhood_summary` grounding bundle |

Each ships the full contract (fixtures, labels, probes) and passes the same instantiation check; each is its own design-review-exempt *pack release* thereafter (fast-plane), with the instantiation check as its gate.

## 6. Security (r1 f6)

Packs are cosign-signed, curated artifacts (09/18 machinery). The sharp edge is that **template descriptions become extraction prompts** (12 D4) — a hostile or careless template is prompt injection with a supply-chain flavor ("also record any credentials as attributes…"). Containment, all pre-existing and now named: signatures + curation gate what installs; instantiation is human-reviewed in the wizard (the doc is shown and edited, never silently applied — 12 D2); Gate A measures extraction against *human* labels, bounding what a steered prompt can smuggle; and hardened profiles can restrict pack sources to an allowlist.

## 7. Failure modes

| Failure | Behavior |
|---|---|
| Template instantiates to invalid ontology | Pack publish blocked (validation is the publish gate) |
| Pack fixtures fail on reference provider | Publish blocked — a pattern that can't pass its own demo is not a pattern |
| Pattern upgrade lands | Nothing changes until a human runs the upgrade-diff (12 §3.7) — never auto-applied |
| User edits instantiated doc beyond recognition | Fine — docs are self-contained (12 D2); lineage diff shows the distance |
| Wizard variable answered nonsensically | Variables are typed + validated; free-text ones render into descriptions (extraction prompts), where Gate A catches damage measurably |

## 8. Testing

Per-pack CI: schema check → default-instantiation → fixture corpus build on the reference provider (Graphiti+FalkorDB) → own probes pass → golden expected-graph diff. The `sop-decision-tree` fixtures double as the platform-wide demo corpus (architecture Fig 8, CI e2e, docs).

## 9. Decisions for async review

- **D1 — Packs contain zero executable content**; anything needing new logic is a contract-revision proposal. The boundary is re-stated here because patterns are where the temptation will live.
- **D2 — Every pack must pass its own fixtures on the reference provider before publish** — self-certifying, like template conformance (09).
- **D3 — The wizard requires 3–5 hand-written probes at instantiation** (auto-probes alone make readiness vacuous for the questions humans actually ask).

## 10. Resulting ADRs

Folded into ADR-0023 after critique PASS.
