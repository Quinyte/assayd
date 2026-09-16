# The agent contract

What a container must do to run as an assayd Agent, and what the operator gives it. Each rule below is read from the operator's code or the Agent CRD, and each says what happens when it is broken.

The reference implementation is `test/responder`, the e2e's agent. It is a test fixture, not design 09's SDK template. It is the only agent any test here runs, so it is the only one this contract has been measured against.

How an agent calls an MCP tool through the gateway is below, in *Calling an MCP tool*. How the tool is published behind the gateway is `docs/install.md` section 6.

## The container

| Rule | What happens otherwise |
|---|---|
| **The image is pinned by digest**, lowercase: `<registry>[:port]/<repo>[:tag]@sha256:<64 hex>` (`spec.runtime.image`). | The Agent CRD refuses the object at apply time. |
| **The image is pullable from a registry at every Pod start.** The operator sets `imagePullPolicy: Always`. | The Pod does not start. A locally imported image is not used. |
| **The process runs as a non-root user**, with a read-only root filesystem, every capability dropped, no privilege escalation, and the `RuntimeDefault` seccomp profile. The operator mounts no volume and no ServiceAccount token. | The container fails to start, or fails when it writes to disk. The responder declares `USER 65532:65532`. A numeric user lets the kubelet check `runAsNonRoot`. |
| **It serves on one port**, `spec.runtime.port`, `8080` by default. The container port is named `a2a`. The revision's Service publishes the same port, and the Agent's route forwards every path on the Agent's hostname to it. | Nothing reaches the agent. |

**The operator renders no readiness or liveness probe.** A revision counts as available once its Deployment reports an available replica, which is soon after the process starts. The card fetch below then retries until the card answers. The responder's `/healthz` is not used by the operator.

## The Agent Card

The operator reads the card from the container. The container is the source of truth, not anything written in the Agent's spec (ADR-0019).

- **Path**: `GET /.well-known/agent-card.json` on the serving port, or the path in `spec.card.path`. It is only ever a path, and **the Agent CRD refuses anything else at apply time** (design 02 A76): it must start with `/`, use only the unreserved characters, the sub-delimiters, `:` and `@`, and be at most 1024 characters. A `?`, `#` or `%` is refused because this field is one path and cannot express what you would mean by them: the operator escapes the value into the request URL, so a `?` arrives as a literal `?` **inside the path**, and `/card%20.json` asks you for a path whose name contains `%20`. Everything the rule admits arrives exactly as written. What the rule does **not** refuse: dot segments (`/../../card.json`) and a host-shaped path (`//elsewhere.example.com/card.json`). Neither moves the fetch — the operator writes the revision's own address first — and both are sent as written, so **your own server decides what they mean**: a Go `http.ServeMux` cleans the path and answers `307`, which the operator reports as `Registered=False`, reason `CardRedirected` (below), not as a `404`. An Agent stored before the rule keeps its path, under CRD validation ratcheting, and for that one the old behaviour still holds.
- **Answer directly, with `200`.** Serve the card at the card path itself. The operator does not follow a redirect (`301`, `302`, `303`, `307` or `308`), and it takes no proxy from its environment. A redirect counts as a failed fetch: `Registered=False`, reason `CardRedirected`, with the message `the card path <url> answered <code>, a redirect, and redirects are not followed: the operator reads the card only from the revision's own Service, never from an address the agent names. Serve the card at the path itself. Retrying every 15s; this does not withhold traffic`. It is retried like any other failed fetch.
- **When**: after the revision is available. The operator fetches through the revision's Service, one attempt per reconcile with a 5-second timeout, and retries every 15 seconds until it succeeds.
- **Credentials**: none. Serve the card to anonymous requests. The operator verifies an API-key policy by sending an anonymous `GET` of this path through the gateway and requiring `401`. When it locks a served `auth: none` Agent to `apikey`, it first sends one such request and compares a `200` answer with the card's recorded digest.

The card is A2A **1.0**. The operator checks the following, and reports the outcome as the `Registered` condition:

| Check | `Registered=False` reason |
|---|---|
| The card path answers without a redirect | `CardRedirected` |
| The fetch succeeds | `CardUnreachable` |
| The body is valid JSON | `CardUnparseable` |
| `name` equals the Agent's `metadata.name` | `CardNameMismatch` |
| `supportedInterfaces` is not empty (a v0.x card, with a top-level `url` and `protocolVersion`, fails here) | `CardNoInterfaces` |
| At least one interface has `protocolVersion: "1.0"` | `CardProtocolUnsupported` |

A card that passes is `Registered=True`, reason `CardValidated`, and its SHA-256 over the exact bytes served is recorded in `status.cards`.

**A failed card fetch does not stop the agent from serving.** It is reported as `Registered=False` with one of the reasons above, and retried. Design 02 §3.4 makes an unregistrable card a registration failure, not a serving one.

**Serve the same bytes every time.** The operator keeps re-reading the card. If a revision's card changes without a spec change, `Registered` stays `True` with reason `CardDrifted`. A field that changes per request, such as a timestamp, reads as drift on every fetch.

**Not checked yet**: the card's signature (every Agent carries `CardUnsigned=True`, reason `NoSigningConfigured`), its skills against the Agent's grants, the registration deadline, `supportedInterfaces[].url`, and `protocolBinding`.

## The A2A binding

The operator does not speak A2A on the serving path. It does not inspect requests: the route forwards every path on the Agent's hostname to the agent. So the binding is the agent's choice, and only one binding has been measured end to end, the responder's.

The responder implements one method of A2A 1.0's HTTP+JSON binding, `SendMessage`:

| | |
|---|---|
| Request | `POST /message:send` |
| Required header | `A2A-Version: 1.0`. The spec reads a request without it as version 0.3, so the responder refuses it with `400`. |
| Request body | A `SendMessageRequest`: `{"message":{"messageId":"…","role":"ROLE_USER","parts":[{"text":"…"}]}}` |
| Response | A `SendMessageResponse` carrying a task, `Content-Type: application/a2a+json` |
| Card declaration | `supportedInterfaces: [{"url": …, "protocolBinding": "HTTP+JSON", "protocolVersion": "1.0"}]` |

The e2e sends requests with `Content-Type: application/json`, and the responder does not check the request's media type. It implements no other method: no `GetTask`, no streaming, no push notifications.

Through the gateway, a caller reaches the agent with the `Host` header `<agent>.<agent-namespace>.<gateway.hostnameSuffix>` (`assayd.internal` by default). Under `auth: apikey`, or with no `expose` block, the caller also sends `Authorization: Bearer <key>`. The gateway checks the key and does not forward a request that fails. The agent can still receive an unauthenticated request directly: no NetworkPolicy is created, so anything that can reach the agent's Service bypasses the gateway. `docs/install.md` walks through a request.

## What the operator injects

| Variable | Value | When |
|---|---|---|
| `ASSAYD_GATEWAY_URL` | The chart's `gateway.url`: where an agent sends outbound tool calls through the gateway. | Only when `gateway.url` is set. |

**The `ASSAYD_` prefix is the operator's.** The Agent CRD refuses a `spec.runtime.env` name, or an `envFrom` prefix, that starts with `ASSAYD_`. The operator also appends its variables after yours, so its value wins in the container either way.

**Nothing restricts where an agent's traffic goes.** `ASSAYD_GATEWAY_URL` tells the agent where the gateway is. It does not stop the agent calling anything else.

Design 02 §11 names two more variables, `ASSAYD_KG_ENDPOINTS` and `ASSAYD_NATS_URL`. Neither is injected: nothing exists yet to produce a value for either. An agent should treat them as absent.

## Calling an MCP tool

Only one MCP client has been measured through the gateway: the responder's `callTool` (`test/responder/main.go`), in `TestAnAgentCompletesATaskByCallingAToolThroughTheGateway`. What follows is what it does and what the gateway answered.

| | |
|---|---|
| Endpoint | `POST <ASSAYD_GATEWAY_URL>/mcp`, MCP Streamable HTTP |
| `Host` header | The tool route's hostname, such as `mcp.demo-tools.example`. The gateway routes on it, and **the operator does not inject it**: tell the agent some other way. The responder reads `MCP_TOOL_HOST`, which is the fixture's name, not assayd's. |
| Headers | `Content-Type: application/json` and `Accept: application/json, text/event-stream` on every request. `MCP-Protocol-Version` on every request after `initialize`. `Mcp-Session-Id`, echoed from the `initialize` response, when the server issued one. |
| Sequence | `initialize` with `protocolVersion: "2025-06-18"`, then `notifications/initialized`, then `tools/call`. |
| Response | Either JSON or Server-Sent Events, and the client cannot choose. agentgateway answers a successful exchange as SSE, even when the server answered JSON, and answers its own errors as plain JSON. On SSE, take the `data:` event whose `id` matches the request, not the first one. |
| A tool the gateway's allowlist refuses | JSON-RPC error `Unknown tool: <name>`, which does not say that an allowlist refused it. The tool is also missing from `tools/list`. |
| Credentials | None. No identity is attached to the call, because design 06 has no implementation. |

**`ASSAYD_GATEWAY_URL` unset means no tool calls.** The responder fails the task rather than calling anything else, and a real agent should too: it has no other address the gateway governs. Nothing enforces that, since no egress NetworkPolicy is created.

## Sources

| Statement | Where it is enforced |
|---|---|
| Digest-pinned image; `ASSAYD_` prefix refused; default port `8080`; the card path — its default, its grammar and its 1024-character bound; Agent name at most 52 characters | `api/v1alpha1/agent_types.go` (CRD validation and defaults) |
| What the card path admits, what it refuses, and that a stored one stays editable | `TestTheCardPathGrammarAndBoundAdmitWhatTheySay`, `TestMistakesAreRejectedWithActionableMessages` and `TestAStoredCardPathStaysEditable` in `test/envtest/` |
| A card path that reads as a host does not move the fetch | `TestTheCardPathCannotNameTheHost` and `TestTheProbesCardPathCannotNameTheHost` in `internal/controller/card_test.go` |
| Pull policy, security context, container port, no probes, no volumes | `internal/controller/agent_controller.go`, the rendered Deployment |
| Service port | `internal/controller/service.go` |
| Route forwards every path on the Agent's hostname | `internal/controller/httproute.go` |
| Card fetch, checks, reasons, retry interval, timeout | `internal/controller/card.go`, `internal/controller/agent_controller.go` |
| Anonymous probe of the card path | `internal/controller/authprobe.go` |
| Injected variables | `internal/controller/injectedenv.go` |
| The responder's binding | `test/responder/main.go`; A2A 1.0 shapes in `docs/research/a2a-v1.0-card-and-transport-2026-09.md` |
| The MCP call: endpoint, headers, sequence, SSE handling | `callTool` in `test/responder/main.go`; measured through agentgateway 1.5.0 in `test/e2e/mcp_test.go` |
| `Unknown tool: <name>` for a refused tool | `TestAnAgentCompletesATaskByCallingAToolThroughTheGateway` in `test/e2e/mcp_test.go` |
| A refused tool's removal from `tools/list` | `TestAnAgentCallsAnMCPToolThroughTheGateway` in `test/e2e/mcp_test.go` |
