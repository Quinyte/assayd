# Design 09: SDK templates (the BYO-SDK on-ramp)

- **Status**: draft — awaiting critique
- **Phase**: P1 · **Size**: M · **Date**: 2026-08-20
- **ADRs**: 0003, 0019 · interfaces: 02 (card/registration contract), 05 (tool discovery), 08 (wizard renders these), 14-loop-eng (architecture §14: the reference inner loop lives here)
- **Research**: A2A v1.0 (Apr 2026): stable spec, **signed Agent Cards** (publisher-domain signature), JSON-RPC + SSE, official SDKs in Python/JS/Java/C#/Go/Rust; python `a2a-sdk` uses AgentSkill/AgentCard/AgentExecutor. https://atlan.com/know/mcp/a2a-protocol-implementation-guide/

## 1. Purpose & scope

The ~100 lines a user owns: per-SDK templates that turn "my agent logic" into a plume-conformant container — A2A server, card, Dockerfile/`project.toml`, and the reference inner loop with skills. In scope: template contract, the per-SDK matrix, the reference loop's shape, card signing, task-state convention. Out of scope: the wizard (08), pack packaging mechanics (18 — templates ship *in* packs from day one, installed as the `sdk-templates` builtin pack).

## 2. Doctrine & charter gates

- **Plane**: **fast** — templates are pack content, versioned data; the platform ships zero framework code. Template updates are pack releases, no platform release. ✓
- **Pods/stateful deps**: none. **Primitives**: Agent (A2A), Tool (MCP), Artifact (pack/OCI). ✓

## 3. The template contract (what every template must produce)

A scaffold that, untouched, passes `plume dev` + registration:

1. **A2A v1.0 server** on `runtime.port` using the SDK's official A2A lib where one exists (python `a2a-sdk`, js, go); the template pins the lib version.
2. **Agent Card** served at `/.well-known/agent-card.json`, generated from one `card.yaml` the user edits (name/skills/description) — **signed at build time**: `plume build` adds the A2A v1.0 card signature (key from the platform's signing identity) alongside cosign image signing; design 02's registration verifies both (card signature + digest).
3. **The reference inner loop** (architecture §14), SDK-idiomatic: `assemble (card + KG bundles + skills) → act (via gateway env: `PLUME_GATEWAY_URL`, `PLUME_KG_ENDPOINTS`, injected by the operator) → observe → stop-check` with **named termination reasons** and a receipt-friendly structure (interior OTel spans via the SDK's OpenLLMetry integration, pre-wired but optional).
4. **Skills directory** (`skills/*.md`, frontmatter + instructions) + the tiny router (load-per-task by declared relevance) — behavior as reviewable data, the genie lesson.
5. **Task-state store**: JetStream-KV-backed A2A task store wired by default (`PLUME_NATS_URL`, tenant creds injected) so `replicas>1` works out of the box (design 02 §3.2); in-memory fallback flag for pure-local runs.
6. **Tests**: a golden-task test (`invoke fixture → expected termination reason + tool-call shape`) runnable by `plume workflow test`-style fixture injection — the seed of the agent's own eval set.
7. `project.toml` (buildpacks) or Dockerfile; non-root, read-only rootfs, port from env.

## 4. The SDK matrix (initial pack content)

| Template | Lang | A2A via | Loop style | Notes |
|---|---|---|---|---|
| `pydantic-ai` | py | a2a-sdk | native agent + our loop wrapper | wizard default for Python |
| `langgraph` | py | a2a-sdk | graph node wraps loop stages | BYO for existing LangGraph users |
| `crewai` | py | a2a-sdk | crew exposed as single A2A agent | |
| `claude-agent-sdk` | py/ts | a2a-sdk / js | SDK loop; ours only adds assemble/stop hooks | |
| `adk` | py | native (ADK speaks A2A) | thinnest wrapper | also the kagent-import path |
| `raw` | go | official go sdk | full reference loop, no framework | the conformance reference; smallest image |

Each template = one directory in the `sdk-templates` pack with `template.yaml` (wizard metadata: language, when-to-recommend reasoning, variables) + files with `{{var}}` substitution. **Template conformance suite**: every template in the pack must scaffold, build, register, and pass the golden-task test in CI (design 07 matrix uses two of them; the pack's own CI runs all).

## 5. Failure modes

| Failure | Behavior |
|---|---|
| SDK lib breaking change | Pack pins versions; pack CI catches on renovate bump; users upgrade by pack release |
| User deletes the loop / serves no card | Registration fails with named condition (design 02) — the contract is enforced at the platform edge, templates just make it easy |
| NATS creds absent (bare `docker run`) | Task store falls back in-memory with a loud log line; registration still requires the platform |
| Card/`card.yaml` drift from code skills | Golden-task test asserts card skills ↔ handler routes agreement |

## 6. Security

Templates never embed secrets; env-injected creds only. Signed cards + signed images (§3.2). Skills are data — the router refuses skill files outside the image (no runtime skill fetch in v1; pack-delivered skill updates require a rebuild — deliberate, revisit with signed skill bundles later).

## 7. Testing

The template conformance suite (§4) is the test. Golden scaffolds (rendered output snapshots) per template per pack release.

## 8. Decisions for async review

- **D1 — Templates live in a builtin pack from day one** (fast-plane; CLI carries none).
- **D2 — Card signing (A2A v1.0) rides `plume build`**; registration verifies signature + digest — two independent tamper checks.
- **D3 — JetStream task store default-on** in templates (not platform-mandated — design 02 D5 honored).
- **D4 — No runtime skill fetching in v1**; skills ship in the image, updates = rebuild.

## 9. Resulting ADRs

Folded into ADR-0022 after critique PASS.
