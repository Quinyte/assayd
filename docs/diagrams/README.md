# Diagrams

Eight plates, generated under the visualizer plugin's design system (Ink + Gold; geometric sans;
**one gold beat per composition**) via `nano_banana_pro`. Source prompts in `prompts.md`.
Gallery artifact: https://claude.ai/code/artifact/90cd9501-5d83-4921-8822-4fce4c36edb3

| Plate | Type | Shows | Gold beat | Ref |
|---|---|---|---|---|
| `01-architecture` | Architecture | Three planes: control (GitOps → operators), data (agents ⇄ gateway ⇄ tools/KG/LLM), substrate | `agentgateway` — every hop crosses it | architecture §02 |
| `02-eval-gated-rollout` | Flow | commit → candidate → HELD → eval job → gate → canary / rollback | the `gate` diamond | ADR-0006 · designs 02+16 |
| `03-onbehalf-sequence` | Sequence | RFC 8693 delegation: user token → agent-scoped token before any tool call | the exchange pair | ADR-0010 · design 06 |
| `04-kg-ingestion` | Flow | acquire → normalize → extract → resolve → Gate A → write → Gate B → Ready, both failures quarantined | `Gate A` — before any write | designs 12 + 14 |
| `05-governance-ring` | Concept | The user-owned agent loop inside the platform-enforced governance ring | the dashed ring | architecture §14 · design 22 |
| `06-api-surface` | Relationship map | The six CRDs and how they reference each other | `Agent` — the front door | architecture §03 |
| `07-what-runs` | Stack | Core (8 pods) / plus / enterprise, itemized | `agent-operator` — the only assayd code in core | architecture §17 · design 07 |
| `08-rollout-lifecycle` | State machine | Pending → Held → Canary → Ready, with Degraded and Killed guards | `Held` — the admission state | design 02 §3.3 · designs 20+22 |

`.webp` variants (1600px) are the embed-sized copies used by the gallery.

## ⚠ Plates 01 and 07 are STALE — they still say "plume"

`01-architecture` reads **"plume operators"** in the control-plane band; `07-what-runs` reads **"the only plume code"** under `agent-operator`. The prompts below and the table above were corrected at the rename; **the rendered images were not**, and `01-architecture` is embedded in `architecture.html`.

This is the failure ADR-0001 named in advance — a codename baked into something expensive to change. Text is cheap to rename with `sed`; a rendered image is not, which is exactly why the ADR said not to bake it in.

**Regenerating needs a decision, not just a re-run.** These were produced with `nano_banana_pro` under the visualizer plugin's design system, so re-prompting reproduces the style but not deterministically — and there is **no editable source**, only prompts and pixels. Two options, and the second fixes the cause rather than the instance:

1. **Re-prompt the two plates** from the corrected prompts. Fastest; keeps all eight visually consistent; leaves every future edit dependent on an image model.
2. **Author these two as SVG.** Deterministic, diffable, and renameable by editing text — but breaks visual consistency with the other six until they follow.

Until one is chosen, **do not treat plates 01 and 07 as current**, and prefer `architecture.md` §02 and §17, which say the same things in text and are correct.


## Validation notes (each plate was opened and checked)

Every plate passed on: label accuracy and spelling, exactly one gold beat, flat colors, grid alignment,
and structural agreement with the design docs it depicts. Two needed regeneration and were re-checked:

- **06** v1 reversed the `members` arrowhead and pointed `ingestion` at EvalSuite instead of Workflow — regenerated with explicit arrowhead-destination language. v2 carries one cosmetic artifact: a redundant unlabeled line beside the `gates` arrow (duplicates an existing correct edge; asserts nothing false).
- **08** v1 printed `recovered` twice, once misspelled `recoveried`, with duplicate arrows — regenerated with an explicit "each label appears exactly once" constraint. v2 sources the `SLO burn` transition from `Canary` rather than `Ready`; both are real paths (canary progression is SLO-judged, design 16 §4.4), and its title renders bolder than the weight-400 house rule.

Known simplification in **03**: the closing `result` arrow returns Tool → User in one hop, eliding
intermediate returns (standard UML convention). Every real return still crosses the gateway.
