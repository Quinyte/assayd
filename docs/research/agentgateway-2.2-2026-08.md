# agentgateway 2.2 — SUPERSEDED, DO NOT CITE (2026-08-27)

> **This note is superseded by [`agentgateway-v1.4.1-2026-08.md`](./agentgateway-v1.4.1-2026-08.md).**
>
> It was never re-verified, and the re-verification found it wrong on load-bearing points. In
> particular: **there is no agentgateway "2.2"** (releases are `v1.x`; latest stable **v1.4.1**),
> and every URL below points at `docs/kubernetes/2.2.x/`, a **frozen pre-v1.4 documentation
> version that still returns HTTP 200** — so the citations look live and silently serve stale
> content. The token-limit, apply/attach, egress, MCP-filter, OTLP-field-path and policy-overlap
> claims below are all corrected in the replacement note.
>
> Retained only so the reviews that cite it remain readable. Do not cite it in a design.

---

## Original content (2026-08; re-verify before implementation) — retained for provenance


- **Control plane split**: 2.2 separated agentgateway controllers from kgateway; kgateway APIs (TrafficPolicy) no longer supported for agentgateway. CRDs: `AgentgatewayBackend`, `AgentgatewayParameters`, `AgentgatewayPolicy`. https://agentgateway.dev/docs/kubernetes/2.2.x/reference/release-notes/ · https://kgateway.dev/blog/kgateway-v2.2-release-blog/
- **Rate limiting**: local (in-process token bucket) and global (external Envoy-style rate-limit service over gRPC) modes; token-based limiting for LLM traffic exists in OSS. https://agentgateway.dev/docs/standalone/main/configuration/resiliency/rate-limits/ · https://agentgateway.dev/docs/kubernetes/2.2.x/security/rate-limit-global/ · local-vs-global discussion: https://github.com/agentgateway/agentgateway/issues/1911
- **Budget/spend limits (token quotas per user/key/time-window)**: documented under **Solo Enterprise** 2.2.x — treat as enterprise; OSS designs must not depend on them. https://docs.solo.io/agentgateway/2.2.x/llm/budget-limits/
- **Evaluation order**: authn (JWT/OPA) → rate limiting → prompt guards; guard-rejected requests still consume quota. https://learncloudnative.com/blog/2026-07-16-agentgateway-rate-limiting
- **Policy overlap**: attach-point precedence Gateway → Listener → Route → RouteRule → Backend with same-level merge semantics; behavior for *same-level/same-field* overlapping policies is not crisply documented — one secondary source reports silent overwrite by creation order with both policies showing ACCEPTED. **Open item R1**: reproducing test at implementation time; design 03's one-concern-per-policy rule avoids the ambiguous case by construction either way.
- **Observability**: native OTLP trace export (`frontendPolicies` tracing config), OTel GenAI semantic conventions, MCP traffic tracing (tool discovery + calls + latency), prompt logging and cost tracking. https://agentgateway.dev/docs/standalone/main/integrations/observability/opentelemetry/ · https://agentgateway.dev/docs/kubernetes/main/observability/ · https://agentgateway.dev/blog/2026-02-17-agentgateway-langfuse-integration/
- **MCP awareness**: routes/filters can match `Mcp-Method` / `Mcp-Name` headers (MCP spec 2026-07-28 stateless HTTP) — header-level tool filtering without body parsing. https://blog.modelcontextprotocol.io/posts/2026-07-28/ · https://blog.kubesimplify.com/controlling-mcp-tools-with-agentgateway-on-kubernetes
- **Istio interop**: experimental agentgateway support announced KubeCon EU 2026 (ambient ecosystem converging). https://www.pulumi.com/blog/kubecon-eu-2026-recap/
