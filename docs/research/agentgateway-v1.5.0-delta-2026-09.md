# agentgateway v1.5.0 — what changed against the v1.4.1 spike

- **Date**: 2026-09-05 · **Re-verify by**: 2026-11-01
- **Method**: primary source only. Release metadata from the GitHub releases API; both API surfaces read at their tags (`controller/api/v1alpha1/agentgateway/agentgateway_policy_types.go` at `v1.4.1` and `v1.5.0`) and diffed. **No cluster was run for this note** — see "What this note cannot settle".
- **Why**: ADR-0030 blocks resuming design 03 until the v1.4.1 spike is re-checked against the current release. Design 03 is titled for and pinned to v1.4.1 (released 2026-07-29). **v1.5.0 was released 2026-08-27** — before the spike's own 2026-10-15 re-verify date, so nothing was overdue; the release simply happened.

## The delta that matters

| `agentgateway-v1.4.1-spike.md` finding | Status at v1.5.0 | Evidence |
|---|---|---|
| §2.3 A negative `burst` is accepted by the API **and** the controller | **FIXED** | `Burst` gains `+kubebuilder:validation:Minimum=0` at v1.5.0; v1.4.1 has no minimum. The API now rejects it. |
| §2.7 The token budget is unbounded under concurrency (100 concurrent requests admitted 100× a one-replica budget) | **BOUNDED, NOT FIXED** | `MaxConcurrentRequests *int32` is new at v1.5.0 and **absent at v1.4.1**: "Requests over the limit are rejected immediately with a 503 response. Unset means unlimited." |
| §2.5 A daily window is not expressible | **STANDS** | `LocalRateLimitUnit` is still exactly `Seconds \| Minutes \| Hours`. |
| One concern per policy — `requests` and `tokens` cannot share one | **STANDS** | `+kubebuilder:validation:ExactlyOneOf=requests;tokens`, unchanged. |
| "Cuts early, never late" is unachievable (ADR-0028) | **STANDS** | The `Tokens` doc comment is byte-identical: "token counts are not known until the request completes. As a result, token-based rate limits will apply to future requests only." |
| `tokenize: true` is unreachable from Kubernetes | **STANDS** | `tokenize` appears **zero** times in the v1.5.0 Kubernetes API surface. |

Other new types at v1.5.0, not assessed here: `JwtSignAuth`, `JwtSigningAlg`, `BackendTLSCertificateSource`, `BackendTunnelMode`, `LocalCACertificateRef`, `PolicyBackendEndpoint`, `Duration`. `JwtSignAuth` may bear on design 06 and is worth its own read before that design resumes.

## `maxConcurrentRequests` is weaker than it looks — read this before designing on it

It is a field on **`FrontendHTTP`**, alongside `maxBufferSize` and the HTTP/1.1 header cap. That is **listener-level connection tuning, not a per-route or per-Agent policy**. Consequences design 03 must not paper over:

- The bound is **shared across every Agent behind that listener**. One Agent can consume the whole allowance, so it caps blast radius at the gateway and attributes nothing to an Agent.
- It therefore **does not restore a per-Agent budget ceiling**, and §3.5's "no figure is published at all" stands as a per-Agent statement. What changes is that the worst case stops being unbounded: an operator can cap total in-flight requests, so the overrun factor becomes a number the operator chose rather than a number the attacker chose.
- Rejection is a **503 at the frontend**, before any budget or auth policy runs. It is a load control, not a governance control, and must not be described as one.

## What this note cannot settle

The spike's two most load-bearing findings are **behavioural**, and an API diff cannot reach them:

- **§2.1** — the apply barrier passes on a policy that emitted nothing (`Attached=False` on a policy whose target route does not exist).
- **§2.8** — a NACK'd tightening silently retains the previous configuration while every condition reports converged. This is the finding A49 reduced `Withdraw` to best-effort over, and the one design 03 §3.3.2 is built around.

Both need a real v1.5.0 cluster with the existing conformance cases re-run against it. Until that happens, **assume they still hold** — v1.5.0's release notes describe changed policy merging, which could move §2.8 in either direction, and a changed merge path is a reason to measure rather than to hope.

## What design 03 owes when it reopens

1. Retitle off v1.4.1, or state deliberately why it pins an older release.
2. Keep §3.5's negative results; they survive. Correct only the unboundedness clause, and correct it to a *listener-shared* bound, not a per-Agent one.
3. Re-run §2.1 and §2.8 on v1.5.0 before any further work on the apply barrier or `Withdraw` — those are the two mechanisms most of A46–A50 exist to serve.
4. Delete any negative-`burst` handling written for v1.4.1; the API now refuses it.

## Addendum, 2026-09-09 — two properties measured on a live cluster

Found while making assayd's e2e send real traffic through a Gateway (k3d, Kubernetes v1.33.6, Gateway API v1.6.0, agentgateway charts 1.5.0, `cr.agentgateway.dev/agentgateway:v1.5.0`). Neither is in the release notes and both change where the data plane can be installed.

**1. The managed proxy cannot run in a PodSecurity `restricted` namespace, and cannot be configured to.** The generated proxy pod sets no `seccompProfile`, so admission refuses it:

```
violates PodSecurity "restricted:latest": seccompProfile (pod or container
"agentgateway" must set securityContext.seccompProfile.type to "RuntimeDefault" or "Localhost")
```

That is the only field it fails on — everything else `restricted` demands, it already satisfies. And it cannot be supplied from outside: `AgentgatewayParameters` v1alpha1 exposes `daemonSet`, `deployment`, `env`, `horizontalPodAutoscaler`, `image`, `istio`, `logging`, `modelCatalog`, `podDisruptionBudget`, `rawConfig`, `resources`, `service`, `serviceAccount`, `shutdown`, `spiffe` and `workload`, and `.spec.deployment` overrides `metadata` only — labels and annotations. There is no pod `securityContext` anywhere in the schema. A namespace at `baseline` works.

**2. `Programmed=True` does not mean anything is serving.** The Gateway reported `Accepted=True` and `Programmed=True` with the message "Successfully programmed Gateway", and every listener condition healthy, while its `LoadBalancer` Service had an empty EndpointSlice and the data-plane Deployment sat at `0/1` with `ReplicaFailure` from the constraint above. `Programmed` describes the control plane's acceptance of the object. Wait on the data-plane Deployment's rollout as well before sending traffic — the failure mode is otherwise a green readiness check followed by a connection refused.

## Addendum 2, 2026-09-09 — the authorization surface, measured

Same cluster. `AgentgatewayPolicy` v1alpha1's `spec.traffic` carries `apiKeyAuthentication`, `basicAuthentication`, `jwtAuthentication`, `extAuth` and `authorization`; `spec.frontend` additionally carries `networkAuthorization` (CEL on the downstream connection, before protocol handling). `targetRefs` selects **same-namespace objects only**.

**`authorization` is default-deny once any `Allow` rule exists.** The schema says it in as many words — "If at least one `Allow` rule is configured, requests are denied unless at least one allow rule matches" — and it behaves that way. `action` is `Allow`, `Deny` or `Require`; `Require` rules are cumulative. The schema itself warns against `Deny`, because "expression failures fail to deny", so an expression that errors admits the request. Prefer `Allow` or `Require`.

**`apiKeyAuthentication` from a ConfigMap takes hashes only.** Each entry is a JSON object with `keyHash` in `sha256:<hex>` form plus optional `metadata`; a raw `key` is rejected from a ConfigMap because ConfigMaps are not confidential (`secretRef`/`secretSelector` exist for the confidential case). The `metadata` object is flattened onto the `apiKey` CEL variable, so `{"metadata":{"group":"trusted"}}` is matched as `apiKey.group == "trusted"`. Credentials default to the `Authorization` header with a `Bearer ` prefix.

**The status codes, measured on one route with one rule** (`apiKey.group == "trusted"`): permitted principal `200`; **valid credential in the wrong group `403`**; no credential `401`; unknown credential `401`. Inverting the rule to permit the other group inverted both outcomes. Removing `authorization` while keeping `apiKeyAuthentication` left anonymous at `401` and moved the wrong-group caller to `200` — so authentication produces the `401` and authorization produces the `403`, and a design that treats them as one signal is wrong about which control fired.

**`Accepted` + `Attached` is not enforcement.** Both conditions went `True` on the policy before the proxy applied it; an anonymous request was still answered `200` for several seconds afterwards. Poll for the behaviour, not the condition. `Attached` also has to be checked at all: a policy whose `targetRefs` name nothing is valid and enforces nothing.

## Addendum 3, 2026-09-09 — the MCP surface, measured

Same cluster, with a two-tool Streamable HTTP MCP server (`test/mcpserver`) behind an `AgentgatewayBackend`.

**`appProtocol: agentgateway.dev/mcp` on the Service port is REQUIRED and is not in the CRD schema.** An MCP target selected by `selector.services.matchLabels` matches nothing without it, and the failure is silent in every place an operator would look: the `AgentgatewayBackend` reports `Accepted=True`, the `HTTPRoute` reports `Accepted=True` and `ResolvedRefs=True`, and requests return `503` with `mcp: no backends configured`. Only the gateway's request log names the cause. The requirement appears in agentgateway's own documentation, not in the API.

**`backend.mcp.authorization` filters discovery, it does not only refuse calls.** With `action: Allow` and `mcp.tool.name == "echo_text"`, `tools/list` returned **only** `echo_text` — the other tool was removed from the listing, not merely refused on call. The schema states this ("List operations... will have each item evaluated. Items that do not meet the rule will be filtered") and it behaves that way. Inverting the rule inverted the listing and both call outcomes.

**A refused tool call is reported as a nonexistent one.** JSON-RPC `-32602`, `Unknown tool: <name>`. There is no "forbidden" in the response and nothing distinguishes policy refusal from a typo. Non-disclosure by design; a debugging trap for whoever operates it.

**The response framing is not the upstream server's.** agentgateway answers a successful MCP exchange as **Server-Sent Events** (`event: message` / `data: {...}`) even when the upstream server replied with plain `application/json`, and answers its **own** errors (the `503` above, and the `-32602` refusal) as bare JSON. A client that assumed either framing would silently see nothing on the other.

**`sessionRouting` defaults to `Stateful`.** This bears on design 03 rather than on the gateway: design 03 shifts traffic between revisions by moving Gateway API `backendRefs` weights, and a stateful MCP session pinned to a revision either breaks when the weight moves or holds traffic on the old revision and defeats the shift. `Stateless` removes the tension and is what the fixture uses. FastMCP's `stateless_http=True` is the same idea from the server side — it also drops `GET` from the allowed methods, since a server with no session tracking has no stream to offer — and **FastMCP 4's headline feature is the MCP sessionless protocol**, so the ecosystem is moving toward stateless being the ordinary case. **assayd has not decided this**, and the fixture does not decide it: design 03 owes the choice.
