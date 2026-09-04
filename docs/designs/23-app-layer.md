# Design 23: The App layer (HTTP projection, typed clients, release pinning)

- **Status**: **approved** — critique PASS at r2 (reviews/23-review.md) · ADR-0025
- **Phase**: P4 · **Size**: M · **Date**: 2026-08-20
- **ADRs**: 0016 (BFF-less apps) · interfaces: 03 (projection routes), 06 (OIDC clients + exchange), 21 (workflow http triggers + input schemas), 08 (`init app` + client gen), architecture §03 (the App CR)

## 1. Purpose & scope

Makes ADR-0016 mechanical: the App CR's composition, the HTTP/SSE projection shapes, typed-client generation, member pinning, and the release/rollback semantics. Out of scope: frontend content (user-owned), auth internals (06), the wizard (08). **The projection routes are design-03 rows** (r1 f2): workflow-POST, chat-SSE, and KG-read projections added to 03 §3.4 alongside the other compiled concerns.

## 2. Doctrine & charter gates

- **Plane**: slow only at the seams — **kro composes; plume's own code is three thin pieces**: (1) an App *watcher* in the agent-operator (identity provisioning per 06, pin validation, status aggregation — no resource creation of its own), (2) projection rows in the 03 compiler, (3) CLI client-gen. The App **ResourceGraphDefinition ships in the chart**; instances are kro's job. Honesty note: the architecture's "App | kro" row stays true — plume adds *logic around* composition, never a composition engine.
- **Pods**: 0 platform (frontend is a user workload). **Stateful deps**: none. **Primitives**: Resource, Agent, Artifact (frontend image, generated client package). ✓

## 3. Projection shapes (compiled by 03, consumed by frontends)

| Member ref | Projected as | Shape |
|---|---|---|
| `workflowRef` | `POST /api/<name>` | body validated against the Workflow's `input.schema` (21) → starts a run → `202 {run_id}`; **`Idempotency-Key` forwarded to 21's run-id derivation** (generated client sends one by default — retries never double-run; r1 f3); `GET /api/<name>/runs/<id>` for status/result |
| `agentRef` (chat) | `POST /api/chat` + **SSE** `GET /api/chat/<task_id>/events` | A2A task lifecycle projected as SSE events (`status`, `message`, `artifact`, `done`) — a thin, *documented* mapping of A2A's own stream, not a new protocol |
| `graphRef` (optional, read-only) | `GET /api/kg/*` | the kgp query surface, scope-filtered per the App's declared entity types — for UIs that render graph context directly |

All routes: OIDC-authenticated (the App's client, 06), user token exchanged at the gateway (on-behalf-of — per-user receipts with zero app code; the per-consumer *quota* half has no compiler path and is owed to design 26 A2's tenant intent (design 03 A48), ADR-0016's core claim), CORS pinned to the App's `route` host, rate limits **when design 26 A2's tenant intent lands** — there is no per-consumer quota today.

## 4. The App CR (final shape) & release semantics

Per architecture §03, plus what P4 machinery makes enforceable:

- **Pinning**: `members` pin exact versions (agent revision-producing image tags, graph versions, frontend digest). Admission (prod profile): unpinned members rejected; **cross-member coherence** validated by the watcher — an App naming `pa-reviewer@1.4.2` + `payer-policies@v12` warns if that agent revision's `knowledge[]` binding pins a *different* graph version (`MemberSkew` condition — the coherent-set promise made checkable).
- **Release = the App CR change** (GitOps); member rollouts still run their own gates (an App bump to `agent@1.5.0` triggers that agent's eval-gated rollout — the App reaches `Ready` only when all members do; `plume deploy` streams the aggregate).
- **Rollback** = previous App spec; members individually re-point (instant for graphs/frontends; agents re-point to retained revisions per `revisionHistoryLimit` — the design 20 linkage note applies).
- Status aggregates member conditions (`MembersReady n/m`, `MemberSkew`, worst-member condition surfaced).

## 5. Typed client generation (`plume init app` / `plume app gen`)

**SSE transport (r1 f4)**: the generated client implements SSE over `fetch()` streams — native `EventSource` cannot send `Authorization` headers; no cookies, no tokens-in-query-strings, `Last-Event-ID` managed manually. Generated TS package from: Agent Cards (A2A skills → chat/task methods), Workflow `input.schema` (→ typed `POST /api/<name>` calls + run polling), optional kgp scope (→ typed KG read hooks). Properties: **generated code pins the source digests** (card digest, schema digest) — `plume app gen --check` in CI fails when the deployed members drift from the client the frontend was built against (the client-server skew gate, mechanical); no runtime dependency on plume (the client is plain fetch/SSE); regeneration is idempotent (golden-tested).

## 6. Failure modes

| Failure | Behavior |
|---|---|
| Member missing/unpinned (prod) | Admission reject / `MembersReady` false, named |
| Member skew (agent's graph ≠ App's graph) | `MemberSkew` warning condition — deploy proceeds (it may be intentional mid-migration) but never silently |
| Client/server drift | `app gen --check` fails CI; runtime 400s carry schema-version headers for diagnosis |
| SSE disconnects | Reconnect = the projection **re-subscribes via A2A itself** (`GetTask`/`SubscribeToTask` at the recorded task_id, through the gateway) and replays the delta as SSE; `Last-Event-ID` maps to the A2A event sequence, never a store cursor — **implementation-agnostic, the BYO promise holds** (r1 f1; the template task store stays invisible to the projection) |
| OIDC client misprovisioned | `IdPUnavailable`-family condition via 06; routes 401 fail-closed |
| Frontend up, members Held | `/api/*` returns typed 503 `MEMBER_NOT_READY` — the UI can render an honest state |

## 7. Security

All §3 routes live behind the gateway (the frontend itself may be public-static; the API never is without OIDC); user tokens never reach member workloads (exchange at the gateway, 06); the App's KG projection is scope-filtered independently of any agent's scope (declared on the App, compiled by 03). Generated clients embed no secrets.

## 8. Testing

RGD golden instantiation; projection e2e (login → chat SSE full lifecycle → workflow POST/poll → per-user receipts with correct `act` chains); pin/skew admission matrix; client-gen goldens + `--check` drift fixture; SSE reconnect replay; member-Held 503 path.

## 9. Decisions for async review

- **D1 — kro composes; plume's App code is watcher + compiler rows + client-gen only** (the architecture's claim, held).
- **D2 — SSE chat is a documented projection of A2A's own stream** — no new protocol.
- **D3 — Client packages pin source digests; `app gen --check` is the CI skew gate.**
- **D4 — `MemberSkew` warns, never blocks** (mid-migration is legitimate; silence isn't).

## 10. Resulting ADRs

ADR-0025 (P4) after critique PASS.
