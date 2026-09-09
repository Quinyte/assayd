# MCP session statefulness at a gateway — the spec removed the problem

**Status**: 2026-09-09. **Re-verify by 2026-12-09.** This note settles a question design 03 was recorded as owing (`research/agentgateway-v1.5.0-delta-2026-09.md`, addendum 3): whether assayd's MCP backends are `Stateful` or `Stateless`, and what a stateful session does to design 02's weighted revision shifting.

**The short answer, and it is stronger than expected.** The question is not "which should assayd choose." **MCP revision `2026-07-28` removed protocol-level sessions from the wire entirely** — `Mcp-Session-Id` is gone, not deprecated. So the choice assayd was deferring has been made upstream, and the correct assayd position is to ratify what `test/mcpserver` already does rather than to decide between two live options.

---

## 1. The spec: `2026-07-28` is a stateless core

Revision timeline, confirmed: `2024-11-05` → `2025-03-26` → `2025-06-18` → `2025-11-25` → **`2026-07-28`** (current and final; RC locked 2026-05-21, published 2026-07-28). No other 2026 revision exists. ([changelog](https://modelcontextprotocol.io/specification/2026-07-28/changelog) · [release post](https://blog.modelcontextprotocol.io/posts/2026-07-28/) · [RC post](https://blog.modelcontextprotocol.io/posts/2026-07-28-release-candidate/))

What `2026-07-28` changes, and every item bears on this repo:

| Change | SEP | Consequence for assayd |
|---|---|---|
| **`Mcp-Session-Id` removed**, along with the `initialize` / `notifications/initialized` handshake. Each request self-describes via `_meta` (`protocolVersion`, `clientCapabilities`, `clientInfo`) | 2575, 2567 | There is no session to pin. The weighted-shift hazard disappears at the protocol layer |
| New mandatory **`server/discover`** RPC advertises versions/capabilities pre-connection | 2575 | A cheap, session-free probe — a better card-discovery analogue than anything design 03 currently compiles |
| **List endpoints are normatively session-independent** — `tools/list` "no longer varies per-connection" | 2567 | Two revisions' tool sets become directly comparable. This is what makes an eval-gated tool-server rollout checkable at all |
| `tools/list` results carry **`ttlMs` / `cacheScope`** | 2549 | Cross-instance caching is now safe, because lists are no longer session-scoped |
| **SSE resumability removed** — `Last-Event-ID` and SSE event IDs are gone. "A broken response stream loses the in-flight request; clients MUST re-issue it as a new request with a new request ID" | — | Drain semantics get simpler: there is no resumable stream whose loss is a correctness bug |
| Server→client push restructured: GET stream + `resources/subscribe` replaced by **`subscriptions/listen`**, one long-lived POST-response stream, opt-in per type | — | The one remaining long-lived connection, and the only thing pod termination still has to drain |
| **Roots, Sampling, Logging Deprecated**; server-initiated sampling/elicitation replaced by **Multi Round-Trip Requests (MRTR)** — a tool returns `resultType: "input_required"`, the client re-issues the *same* request with `inputResponses` | 2577, 2322 | The features that used to *require* statefulness now have a stateless form. This removes the standard counter-argument |
| **`Mcp-Method` / `Mcp-Name` headers required** on Streamable HTTP POSTs | 2243 | **Directly relevant**: a gateway can route and authorize without parsing JSON bodies. ADR-0020 decision 5's rule that "the gateway never parses MCP bodies" is now supported by the protocol rather than worked around |
| **HTTP+SSE** transport (deprecated since `2025-03-26`) formally reclassified Deprecated under a new 12-month-minimum deprecation lifecycle | 2596 | Deprecated, **not** removed — the old transport is still legal, so a mixed fleet is a real state, not a hypothetical |

The rationale document is [**SEP-2567, "Sessionless MCP via Explicit State Handles"**](https://modelcontextprotocol.io/seps/2567-sessionless-mcp) (Final, 2026-03-11), and it is worth reading in full before design 03 resumes. Two of its findings matter here:

- **Session lifetime was never consistently defined by clients.** "ChatGPT creates a fresh session for every individual tool call, and Claude.ai did the same until recently… Almost no clients resume a prior session after a disconnect or restart." A property no client relied on was never load-bearing.
- A **1000-repository automated survey found only 0.7% of open-source MCP servers used the session ID for proxy/gateway sticky routing** — against ~90% with no application-level session reference at all. The sticky-routing use case assayd was worried about is the rarest one in the corpus.

Sessions were removed as **a clean break with no deprecation window**; protocol-version negotiation is the transition mechanism.

## 2. SDKs: stateless was already the escape hatch, and is now the only mode

Per SEP-2567: "All official SDKs except PHP already provide a stateless mode… This SEP makes that mode the *only* option for servers speaking the new protocol version."

| SDK | Stateless switch |
|---|---|
| TypeScript | `StreamableHTTPServerTransport({ sessionIdGenerator: undefined })` ([migration guide](https://ts.sdk.modelcontextprotocol.io/v2/migration/support-2026-07-28)) |
| Python / FastMCP | `stateless_http=True`, `json_response=True` (moved from constructor to `run()` in SDK v2; `FastMCP` → `MCPServer`) |
| Spring AI | `WebMvcStatelessServerTransport`, `spring.ai.mcp.server.protocol=STATELESS`. Spring AI 2.0 GA 2026-06-12 deprecated SSE for Streamable HTTP |
| Go | Stateless mode present in both `mark3labs/mcp-go` and the official `modelcontextprotocol/go-sdk` — **exact flag name unverified** |

The clearest statement of the *old* trade-off comes from AWS Bedrock AgentCore's devguide, still pinned to `2025-11-25`: "Features like elicitation, sampling, and progress notifications require stateful MCP sessions. Enable stateful mode by setting `stateless_http=False`." That is precisely the cost MRTR now removes.

## 3. Gateways: agentgateway already handles both, and assayd's pin is new enough

**agentgateway v1.4 (released 2026-07-27 — one day before the spec) added dual-protocol support**: it "checks the backend for stateful clients and handles MCP 2026-07-28 requests without protocol sessions, with the protocol version on each request determining which path it follows" ([agentgateway blog](https://agentgateway.dev/blog/2026-08-03-new-mcp-spec-revision/)). assayd pins **1.5.0**, so it has this.

For the still-live stateful path ([session docs](https://agentgateway.dev/docs/kubernetes/latest/mcp/session/)): agentgateway does **not** terminate the client-facing session — it proxies `Mcp-Session-Id`, and internally encodes session state **AES-256-GCM-encrypted into the session ID itself**, so no central store is needed and any proxy replica can decode it. Envoy AI Gateway uses the same self-contained-token approach ([writeup](https://theagentrouter.ai/blog/mcp-in-envoy-ai-gateway/)). Note two things: the token still **pins to one backend**, and agentgateway's stateful session routing "require[s] the use of non-static, `selector`-based targets."

Microsoft's OSS `mcp-gateway` goes further and deploys MCP servers as **StatefulSets with headless Services** to guarantee session→instance. That is worth naming because it would breach this repo's stateful-workload allowlist (design 07 §5, `TestStatefulDependencyAllowlist`) — a stateful MCP posture is not free in assayd's weight budget, it is a CI-enforced ledger entry.

**Unverified / corrective**: Kong AI Gateway's MCP Proxy plugin (Kong 3.12) affinity mechanism could not be confirmed from primary docs. Higress markets an "MCPRoute API" — **no GEP for an `MCPRoute` kind exists in `kubernetes-sigs/gateway-api`**; treat it as a Higress-proprietary CRD, not a standard Gateway API type.

## 4. The crux — and the primary source is GEP-1619, not anything MCP

**Nobody has written up MCP sessions × weighted canary specifically.** Every MCP-side source (AWS, agentgateway, Envoy AI Gateway, SEP-2567) frames session-vs-load-balancer pain as *horizontal scaling / round-robin*, never as progressive delivery between two live revisions. The question sits in a seam. But the generic half is specified, normatively, and it is decisive:

**Kubernetes Gateway API [GEP-1619](https://gateway-api.sigs.k8s.io/geps/gep-1619/)** states:

> "When a persistent session is established and traffic splitting is configured across services, the persistence to a single backend MUST be maintained across services, **even if the weight is set to 0**."

and, stated as precedence rather than interaction: **"a persistent session takes precedence over traffic split weights when selecting a backend after route matching."** Its conformance expectations split cleanly in two:

- **Before a session exists**, a fresh request with no session token **does** respect the weights — the GEP's conformance test cites "~70/30 within statistical tolerance."
- **Once a session is established**, every subsequent request from that client **MUST** route to the same backend "regardless of weight configuration." A MUST, not a tendency.

**The consequence design 02 has not accounted for: a canary that reaches weight 100 does not reclaim sessions that predate it.** Every session opened during the candidate's low-weight window stays pinned to whatever it first landed on — including the *superseded* revision — for that session's whole life, and nothing errors to say so. The rollout completes, the status says the new revision is serving, and an unbounded set of clients is still talking to the old one.

And where persistence is configured on only some backends of a weighted split, GEP-1619 explicitly declines to decide, listing three permitted implementation behaviours including "apply session persistence for only servicev1, **potentially causing all traffic to eventually migrate to servicev1**" — "this GEP leaves the decision to the implementation."

**So the interaction is not merely awkward, it is specified to defeat the weight.** For assayd this is direct: design 02 §3.3 shifts revisions by moving `backendRefs` weights (`10 → 100`), and design 02 sends a revision to **weight 0** in at least four failure paths (`RevisionMaterialUnavailable`, missing copy, gate hold, `BudgetExhausted`). Under GEP-1619, a pinned session **keeps being served by a weight-0 revision** — which means weight 0 would stop being a containment mechanism. That is the sharpest finding in this note: assayd uses weight 0 as a *safety* action, and session persistence is specified to ignore it.

The enumerated failure modes, for the record:

1. **Session on A, next request weighted to B.** B has no record of the session ID and returns 404; the client must re-`initialize` mid-conversation. The spec's 404 semantics were designed for "session ended" and get triggered by routing mismatch instead.
2. **Affinity "fixes" it by exempting the session from the weights** (GEP-1619, above). Long-lived, low-QPS sessions then pin disproportionate load to whichever revision won the coin flip at session start, so nominal weight and effective traffic share diverge silently — fatal for an eval-gated rollout whose whole premise is that weight *means* current traffic share.
3. **In-flight streams during scale-down.** Not MCP-specific but real: **Argo Rollouts has an open documented gap** — "as soon as a Rollout promotes the canary to stable and the Service endpoints change, existing connections are immediately terminated" ([argo-rollouts#3761](https://github.com/argoproj/argo-rollouts/issues/3761)). Service endpoint swaps are not drain-aware; draining has to come from the mesh/gateway layer. Under `2026-07-28` the only long-lived stream left is `subscriptions/listen`.
4. **Tool-set drift mid-session.** Hosts fetch `tools/list` once and do not recheck until `list_changed` fires, which "not every server implementation sends reliably." **`2026-07-28` fixes the caching half** (lists are session-independent and carry `ttlMs`/`cacheScope`) but **does not fix cross-revision drift**: a canary revision's tool set can legitimately differ from stable's, and a client holding A's cached schema can still call B. This one survives statelessness and assayd still owes it an answer — and it is the same shape as design 03 §3.1's `Loosen` cell.

## 5. What this means for assayd

1. **Ratify `Stateless`, and record why.** `test/e2e/mcp_test.go:235` already sets `sessionRouting: Stateless` with a comment saying assayd has not decided. It is now decidable: the spec removed the alternative. The fixture stops being a shortcut and becomes the specified behaviour.
2. **Design 03 owes one line, not a decision.** Its MCP backends compile with `sessionRouting: Stateless`; any Connector requiring a pre-`2026-07-28` stateful server is **ineligible for weighted shifting** and must be marked so, because GEP-1619 says its sessions will ignore the weights — including weight 0.
3. **The weight-0 finding needs to reach design 02.** Weight 0 is used there as a containment action on four failure paths. If assayd ever admits a session-persistent backend, weight 0 stops containing. Either statelessness is an invariant the compiler enforces, or design 02's weight-0 paths need a second mechanism (route withdrawal, per A38's `Withdraw`) — the current text assumes weight 0 is sufficient and, for persistent sessions, it is specified not to be.
4. **`Mcp-Method` / `Mcp-Name` are a gift to ADR-0020 decision 5.** The rule that the gateway never parses MCP bodies is now protocol-supported rather than a self-imposed constraint. Worth re-reading that decision against SEP-2243.
5. **Statefulness would cost a weight-budget ledger entry.** The prevailing stateful pattern is StatefulSet + headless Service (Microsoft's gateway), which design 07 §5's CI job would refuse without an allowlist edit in the same commit.
6. **Do not adopt Gateway API `sessionPersistence` for MCP backends at all.** GEP-1619's precedence rule is incompatible with a weighted canary for as long as any session outlives the canary window — which a long-running agent session routinely will. And where only *some* backends in a split carry persistence, the GEP explicitly declines to prescribe an outcome. That is not a gap assayd can read its way around; it is left open by the standard, which is a further argument for not touching the mechanism while assayd's own backends can simply be stateless.
7. **For an upstream that has not yet moved to `2026-07-28`, `sessionRouting: Stateless` is the bridge**, not a compromise: it synthesizes a fresh per-request initialization against a stateful upstream, and it sidesteps the `selector`-vs-`static` target footgun entirely, because there is no session to route by target selection in the first place.
8. **Genuinely stateful tool needs go to the spec's own answer** (SEP-2567): mint an explicit handle from a tool call and have the caller pass it back as an ordinary argument. That is application data — it flows through the policy compiler like any other tool argument, survives gateway hops, revisions and pod restarts, and creates no routing dependency at all.
9. **Track the 12-month HTTP+SSE deprecation clock** (SEP-2596) as a dated fact in design 03 or `docs/architecture.md`, so assayd's transport story does not silently age out with the deprecated transport.
10. **Tool-set drift across revisions is NOT solved by statelessness** and remains owed. Worth noting it is the *same shape* as design 03 §3.1's `Loosen`/`Tighten` problem one layer down — a session is itself a cache of what a backend offered at initialization — so the "frozen, never silently widened" discipline design 03 already argues for `toolAllowlist` is the same discipline this needs. That is corroboration for design 03's existing posture, not a new problem for it.

**What this note does not measure.** All of the above is desk research plus the repo's own prior spike. Nobody here has run a `2026-07-28` client against agentgateway 1.5.0, and the dual-path claim is agentgateway's blog rather than an observation on this cluster. Per AGENTS.md, the honest status is: the spec text is verified, the gateway behaviour is **not**. A spike that drives a `2026-07-28` exchange through the pinned gateway — and, separately, one that shifts `backendRefs` weight to 0 under an established stateful session and observes whether traffic actually stops — is what would turn items 1 and 3 into properties. The second of those is the one that could change a design.
