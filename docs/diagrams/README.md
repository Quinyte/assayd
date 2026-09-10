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

Plates `01-architecture` and `07-what-runs` were re-rendered on 2026-09-10 from the prompts below, after the rename; the other six are unchanged from the original set.

## Validation notes (each plate was opened and checked)

Every plate passed on: label accuracy and spelling, exactly one gold beat, flat colors, grid alignment,
and structural agreement with the design docs it depicts. Two needed regeneration and were re-checked:

- **06** v1 reversed the `members` arrowhead and pointed `ingestion` at EvalSuite instead of Workflow — regenerated with explicit arrowhead-destination language. v2 carries one cosmetic artifact: a redundant unlabeled line beside the `gates` arrow (duplicates an existing correct edge; asserts nothing false).
- **08** v1 printed `recovered` twice, once misspelled `recoveried`, with duplicate arrows — regenerated with an explicit "each label appears exactly once" constraint. v2 sources the `SLO burn` transition from `Canary` rather than `Ready`; both are real paths (canary progression is SLO-judged, design 16 §4.4), and its title renders bolder than the weight-400 house rule.

Known simplification in **03**: the closing `result` arrow returns Tool → User in one hop, eliding
intermediate returns (standard UML convention). Every real return still crosses the gateway.
