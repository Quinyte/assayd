# A2A v1.0 — the Agent Card and the task transport, field by field (2026-09)

Supersedes the coarse claims in `a2a-2026-08.md` for anything at field level. That
file said "card at `/.well-known/agent-card.json`; JSON-RPC 2.0 + SSE" — both true,
and both too coarse to have caught the two things assayd got wrong.

**Re-verify by: 2027-03.** A2A is on a ~2-3 month patch cadence within 1.0.x.

## Primary sources, pinned

| What | Where |
|---|---|
| Spec repo, released tags | `a2aproject/A2A` — `v1.0.0` (2026-03-12), `v1.0.1` (2026-05-28) |
| Normative schema | `specification/a2a.proto` @ `v1.0.1` |
| Prose spec | `docs/specification.md` @ `v1.0.1` |
| Serialization rule | `adrs/adr-001-protojson-serialization.md` |
| Official Go SDK | `a2aproject/a2a-go` @ `v2.5.0` (2026-08-18) — `a2a/agent.go`, `a2a/core.go` |
| v0→v1 card shim | `a2a-go` `a2acompat/a2av0/agentcard.go` |

**The `.proto` is now the single source of truth.** `specification/json/` contains only
a `README.md` at `v1.0.1` — the standalone JSON Schema is gone. JSON is derived from
the proto by **protojson**, which is what fixes field names to lowerCamelCase and, as
below, enum values to their full SCREAMING_SNAKE proto names.

## 1. The Agent Card

Path is unchanged: **`GET /.well-known/agent-card.json`** (spec §8.6, and the IANA
well-known URI registration template in §13). Responses carry
`Content-Type: application/a2a+json`.

### The eight REQUIRED top-level fields

From `AgentCard` in `a2a.proto`, fields marked `(google.api.field_behavior) = REQUIRED`:

`name`, `description`, `supportedInterfaces`, `version`, `capabilities`,
`defaultInputModes`, `defaultOutputModes`, `skills`.

Optional: `provider`, `documentationUrl`, `securitySchemes`, `securityRequirements`,
`signatures`, `iconUrl`.

### `protocolVersion` is NOT a top-level field

This is the finding assayd needed. In v1.0 the card has **no top-level `url` and no
top-level `protocolVersion`**. Both moved into `supportedInterfaces[]`, an ordered list
(first entry preferred) of `AgentInterface`:

```json
{
  "name": "responder",
  "description": "…",
  "version": "0.1.0",
  "supportedInterfaces": [
    {
      "url": "https://agent.example.com/a2a",
      "protocolBinding": "JSONRPC",
      "protocolVersion": "1.0"
    }
  ],
  "capabilities": {},
  "defaultInputModes":  ["text/plain"],
  "defaultOutputModes": ["text/plain"],
  "skills": []
}
```

`AgentInterface` requires `url`, `protocolBinding`, `protocolVersion`; `tenant` is
optional (an opaque routing string a client must echo in requests when set).

**`protocolVersion` carries `"1.0"`.** Confirmed literally, not inferred:
`a2a-go` `a2a/core.go` has `const Version ProtocolVersion = "1.0"`. It is MAJOR.MINOR,
not the release triple — the proto comment says *"Use the latest supported minor version
per major version. Examples: `"0.3"`, `"1.0"`"*. So **assayd's supported-set string is
correct and its location is wrong.** The e2e fixture and the operator agreed on a value
that a real v1.0 card does not carry at that key at all.

`protocolBinding` is an open string; the three official values are `JSONRPC`, `GRPC`,
`HTTP+JSON` (`a2a-go` `TransportProtocol*` constants).

### Why assayd drifted: this is the v0.x shape

`a2acompat/a2av0/agentcard.go` preserves the pre-1.0 card, and it is exactly what assayd
implements — top-level `url` and `protocolVersion`, plus `preferredTransport` and
`additionalInterfaces`. The compat layer's job is to fold those into
`supportedInterfaces[]`. assayd is reading a v0.x card and calling it v1.0.

### `capabilities` is real — but has only four fields, and `sharedTaskState` is not one

`AgentCapabilities` in full:

| Field | Type |
|---|---|
| `streaming` | optional bool |
| `pushNotifications` | optional bool |
| `extensions` | repeated `AgentExtension` |
| `extendedAgentCard` | optional bool |

**`sharedTaskState` does not exist.** `grep -i shared` over `a2a.proto` returns nothing;
over `specification.md` it returns only prose ("shared `contextId`", "shared tasks").
Design 02 §3.2 keys `TaskStateUnverified` off `capabilities.sharedTaskState`, which is a
field assayd invented. Nothing in A2A v1.0 asserts anything resembling shared task state.

Also gone: **`stateTransitionHistory`**, which assayd's responder declares. It existed in
v0.x; it is absent from v1.0's proto and prose entirely.

If design 02 wants that assertion, the spec-sanctioned vehicle is
`capabilities.extensions[]` — an `AgentExtension` is `{uri, description, required, params}`,
where `uri` identifies a assayd-defined extension. That keeps the card valid instead of
adding an unrecognized key.

## 2. The transport

Design 09's "JSON-RPC + SSE" is **directionally right but under-specified**: v1.0 defines
*three* bindings, and the card declares which one an interface speaks.

### Method names are PascalCase, not `message/send`

Spec §9.1: *"Method Naming: PascalCase method names matching gRPC conventions (e.g.,
`SendMessage`, `GetTask`)"*. The v0.x slash style (`message/send`, `tasks/get`) is gone.
The §9.3 skeleton still shows a `"category/action"` placeholder — that is stale prose in
the spec itself; every worked example in §9.4 uses PascalCase.

The full `A2AService` surface, with the method name and the HTTP+JSON path each maps to:

| JSON-RPC method | HTTP+JSON binding | Streams |
|---|---|---|
| `SendMessage` | `POST /message:send` | no |
| `SendStreamingMessage` | `POST /message:stream` | yes |
| `GetTask` | `GET /tasks/{id}` | no |
| `ListTasks` | `GET /tasks` | no |
| `CancelTask` | `POST /tasks/{id}:cancel` | no |
| `SubscribeToTask` | `GET /tasks/{id}:subscribe` | yes |
| `CreateTaskPushNotificationConfig` | `POST /tasks/{id}/pushNotificationConfigs` | no |
| `GetTaskPushNotificationConfig` | `GET /tasks/{id}/pushNotificationConfigs/{id}` | no |
| `ListTaskPushNotificationConfigs` | `GET /tasks/{id}/pushNotificationConfigs` | no |
| `DeleteTaskPushNotificationConfig` | `DELETE /tasks/{id}/pushNotificationConfigs/{id}` | no |
| `GetExtendedAgentCard` | `GET /extendedAgentCard` | no |

**There is no `POST /v1/tasks` in any binding.** assayd's responder endpoint matches
nothing in v1.0. Note also that under JSON-RPC the HTTP path is irrelevant — every method
POSTs to the single interface `url`; the paths above are the HTTP+JSON binding only.

### Envelope

Requests are ordinary JSON-RPC 2.0 — `{jsonrpc, id, method, params}` — with `params`
holding the request message, `Content-Type: application/json`. A2A service parameters
(`A2A-Version`, `A2A-Extensions`) ride as **HTTP headers**, not in `params` (§9.2).

`SendMessageRequest`: `message` (REQUIRED), plus optional `tenant`, `configuration`,
`metadata`.

`Message`: `messageId` (REQUIRED), `role` (REQUIRED), `parts` (REQUIRED), plus optional
`contextId`, `taskId`, `metadata`, `extensions`, `referenceTaskIds`.

`Part` is a oneof over `text` / `raw` (base64 bytes) / `url` / `data`, with optional
`metadata`, `filename`, `mediaType`.

`SendMessageResponse` is a **oneof: `task` or `message`** — not a bare task object.

### Streaming is JSON-RPC over SSE

Confirmed, and specifically: `SendStreamingMessage` returns HTTP 200 with
`Content-Type: text/event-stream`, where **each SSE `data:` frame is a complete JSON-RPC
response envelope** carrying a `StreamResponse` as its `result`:

```text
data: {"jsonrpc":"2.0","id":1,"result":{ /* StreamResponse */ }}
```

`StreamResponse` is a oneof: `task` | `message` | `statusUpdate` | `artifactUpdate`.
(The HTTP+JSON binding also uses SSE but puts the bare protocol object in `data:`, with
no JSON-RPC envelope — §11.7. gRPC uses native server streaming.)

### Task states are SCREAMING_SNAKE on the wire

protojson serializes enum values by proto name. `TaskState` is therefore
`TASK_STATE_SUBMITTED`, `TASK_STATE_WORKING`, `TASK_STATE_COMPLETED`, `TASK_STATE_FAILED`,
`TASK_STATE_CANCELED`, `TASK_STATE_INPUT_REQUIRED`, `TASK_STATE_REJECTED`,
`TASK_STATE_AUTH_REQUIRED`, `TASK_STATE_UNSPECIFIED`. Confirmed in `a2a-go` `a2a/core.go`
(`TaskStateCompleted TaskState = "TASK_STATE_COMPLETED"`).

**Not** the lowercase `"completed"` assayd's responder emits, and not the lowercase
lifecycle `a2a-2026-08.md` recorded.

## 3. What this contradicts in assayd

Nothing here contradicts an ADR — ADR-0019 (card served by the container) and ADR-0030
(narrow slice) are untouched. It contradicts implementation and two design claims:

1. **`internal/controller/card.go`** — `fetchedCard.ProtocolVersion` reads a top-level key
   that a v1.0 card does not have, so every real v1.0 card decodes as `""` and fails the
   closed supported-set. The check must read `supportedInterfaces[].protocolVersion` and
   accept an interface whose value is `"1.0"`. The set's *contents* were right.
2. **`internal/controller/card.go`** — `Capabilities.SharedTaskState` parses a field that
   does not exist, so design 02 §3.2's `TaskStateUnverified` condition is presently keyed
   off a value that is always `false` for any conformant agent. Either drop the condition
   or re-key it onto a assayd `AgentExtension` URI.
3. **`test/responder/main.go`** — serves a v0.x card (top-level `url`/`protocolVersion`,
   `stateTransitionHistory`, no `supportedInterfaces`/`defaultInputModes`/
   `defaultOutputModes`), answers a bespoke `POST /v1/tasks`, and reports
   `state: "completed"`. All three need to move to the shapes above.
4. **Design 09** — "A2A v1.0 as JSON-RPC + SSE" should name the binding (`JSONRPC`) and
   note that the card advertises it in `supportedInterfaces[].protocolBinding`, since
   `GRPC` and `HTTP+JSON` are equally conformant and a gateway route has to know which.
