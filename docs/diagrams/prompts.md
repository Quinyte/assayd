# Diagram prompts (for regeneration / amendment)

All three rendered with `nano_banana_pro` under the visualizer Ink + Gold design system
(`plugins/visualizer/shared/design/design-system.md`). Palette discipline: ink `#14141A`
carries the composition, gold `#C8A24B` appears exactly once per plate, grey-ink `#2A2A33`
for annotations, paper `#FAFAF7` ground. Typography: one geometric sans (Inter-like, weight 400).

Regenerate via the visualizer CLI (needs an unblocked `GEMINI_API_KEY`):

    bun run <plugin>/shared/tools/generate-image.ts --model nano-banana-pro \
      --prompt "<prompt below>" --size 2K --aspect-ratio <ratio> --output <file>.png

or via the higgsfield MCP `generate_image` with `{"model":"nano_banana_pro"}` — the route
used for this set, because the visualizer's Gemini key returned `API_KEY_SERVICE_BLOCKED`.

## 01 — Architecture (16:9)
Three horizontal bands. Band 1 CONTROL PLANE (faint `#F3ECD9` wash): Git repo → Argo CD →
plume operators. Band 2 DATA PLANE: Agent A / Agent B → **gold-outlined agentgateway
(A2A / MCP / LLM)** → MCP tools / Knowledge Graph / LLM providers. Band 3 SUBSTRATE:
NATS JetStream, Postgres, with an arrow down from the gateway into NATS.
Gold beat: the agentgateway box.

## 02 — Eval-gated rollout (16:9)
Left-to-right flow: git commit → candidate revision → HELD — no traffic → eval job
(annotated *golden set + replayed sessions*) → **gold decision diamond `gate`** →
pass ↗ canary 10% → full traffic; fail ↘ rollback + report.
Gold beat: the gate diamond.

## 03 — On-behalf-of sequence (4:3)
Five lifelines: User, agentgateway, IdP, Agent, MCP Tool. Messages: request + user token →
**gold: exchange RFC 8693 → gold: agent-scoped token** → A2A task → tool call →
call + scoped token → result.
Gold beat: the exchange arrow pair.

## Validation checklist applied to each
label accuracy and spelling · exactly one gold beat · flat colors, no gradient/shadow/3D ·
grid alignment · legibility at 100% · structure matches the design docs it depicts.
