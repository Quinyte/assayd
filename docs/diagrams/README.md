# Diagrams

Generated with the visualizer plugin's design system (Ink + Gold; geometric sans; **one gold beat per composition**), rendered via `nano_banana_pro`. Source prompts are recorded in `prompts.md` so any diagram can be regenerated or amended.

| File | Type | Shows | Gold beat |
|---|---|---|---|
| `01-architecture.png` | Architecture | The three planes: control (GitOps → operators), data (agents ⇄ gateway ⇄ tools/KG/LLM), substrate (NATS + Postgres) | `agentgateway` — every hop crosses it (architecture §02) |
| `02-eval-gated-rollout.png` | Flow | Semantic admission: commit → candidate → HELD → eval job → gate → canary/rollback (ADR-0006, designs 02+16) | the `gate` diamond — the admission decision |
| `03-onbehalf-sequence.png` | Sequence | RFC 8693 delegation: user token exchanged into an agent-scoped token before any tool call (ADR-0010, design 06) | the exchange pair — what makes `act` chains real |

`.webp` variants (1600px) are the embed-sized copies used by the gallery artifact.

**Known simplification** in `03`: the final `result` arrow returns Tool → User in one hop, eliding the intermediate returns through gateway and agent (standard UML return-message convention). Every real return still crosses the gateway.
