# agentgateway v1.4.1 execution spike — measured, not read

**Status**: 2026-08-27. Offline phase complete; cluster phase partly complete — three questions remain and are named in §3.

Why this exists: six review rounds found real defects in design 03's prose, and the
three that mattered most turned on *behaviour of a dependency nobody had run*.
`docs/architecture.md`'s own open items say it — "nothing is execution-validated.
These plans survived adversarial review, not running code." This spike answers the
questions review cannot, by observation rather than argument.

Two phases, and the split matters: everything in §1 was settled **offline from the
shipped chart**, which is cheaper and stronger than the docs site because it is the
artifact a cluster actually installs.

## 1. Settled from the shipped v1.4.1 chart (no cluster required)

Source: `oci://ghcr.io/agentgateway/charts/agentgateway-crds:1.4.1`,
digest `sha256:e82095934b8119b2199e08a8fb2b00a32e2dd4835f565715ef36c91595af7cb3`.
The dataplane chart `oci://ghcr.io/agentgateway/charts/agentgateway:1.4.1` reports
`appVersion: 1.4.1`, independently confirming the release numbering.

| Question | Answer, from the shipped CRD | Bearing |
|---|---|---|
| Which CRDs ship? | `agentgatewaybackends`, `agentgatewaymodels`, `agentgatewayparameters`, `agentgatewaypolicies` — **four** | `AgentgatewayModel` is real; ADR-0028's "fourth CRD" holds |
| `LocalRateLimit` numeric widths | `requests`, `tokens`, `burst` all `format: int32` | Design 03 §3.5's int32 clamp is required, not defensive |
| Does `burst` have a minimum? | **No `minimum` at all.** `requests` and `tokens` both carry `minimum: 1`; `burst` carries none | A negative burst is schema-valid at the tag. The `main` commit the first Codex review pinned has `minimum: 0`, so **that finding was right for `main` and wrong for the release** — plume must validate `burst >= 0` itself (A10) |
| Is a per-day window expressible? | `unit` is `required`, `enum: [Hours, Minutes, Seconds]` | A daily budget cannot be emitted directly; §3.5's hourly emission is forced, not chosen |
| Can one policy carry both limits? | `x-kubernetes-validations`: `[has(self.requests),has(self.tokens)].filter(x,x==true).size() == 1` | ExactlyOneOf confirmed |
| What does the CRD say about *when* token limits act? | Verbatim: *"token counts are not known until the request completes. As a result, token-based rate limits will apply to future requests only."* | The withdrawn "cuts early, never late" is contradicted by the schema the cluster installs, not merely by a docs page |
| Is there any egress/allowlist field on `AgentgatewayBackend`? | **No.** One grep hit in the whole file, and it is the unrelated MCP JSON-RPC `methods` allowlist | Codex BLOCKER 6 and design 03 §3.4.1 confirmed: the control is Backend *construction* |
| Backend arms | `ExactlyOneOf: ai static dynamicForwardProxy mcp aws a2a` | `dynamicForwardProxy` exists and must be forbidden by plume (§3.4.1) |
| LLM provider arms | `ExactlyOneOf: openai azureopenai azure anthropic gemini vertexai bedrock custom` | The provider→endpoint catalog plume owes has exactly these arms to resolve into |
| MCP `matchExpressions` bounds | `minItems: 1`, `maxItems: 256`, per-item `maxLength: 16384` | **Codex r2 MAJOR 8 confirmed**: an empty allowed-tool set has no representation, and 257 tools overflows. Both need a stated mapping before emission |

**One hazard the CRD does not prevent.** `burst` is documented as an allowance above
the *request*-per-unit, and the published guide says it "only works with `requests`,
not with `token` rate limits" — but the schema permits `tokens` and `burst` together.
A policy setting both is schema-valid and its burst is silently inert. Plume must
never emit that pair.

## 2. Measured on a real v1.4.1 cluster

k3d `plume-spike`, Gateway API v1.6.0 standard, `agentgateway-crds` and `agentgateway`
Helm charts at 1.4.1, controller `agentgateway.dev/agentgateway`. Every result below is an
observation, not a reading.

### 2.1 The apply barrier passes on a policy that emitted nothing — reproduced

An `AgentgatewayPolicy` targeting an `HTTPRoute` that does not exist:

```yaml
ancestorRef: {group: agentgateway.dev, kind: Gateway, name: StatusSummary}   # synthetic
conditions:
- {type: Accepted, status: "True",  reason: Valid,   message: "Policy accepted", observedGeneration: 1}
- {type: Attached, status: "False", reason: Pending, message: "Policy is not attached: HTTPRoute default/does-not-exist not found"}
```

Design 03's original barrier waited for `Accepted=True`. **It is satisfied here**, on a policy
that produced no configuration — and §3.3's ordering *guarantees* this state, because routes
are created last. Confirmed on the release, not inferred from a fixture.

### 2.2 Success and failure differ in the ANCESTOR, not only the condition

The same policy against a route that exists:

```yaml
ancestorRef: {group: gateway.networking.k8s.io, kind: Gateway, name: gw, namespace: default}
conditions:
- {type: Accepted, status: "True", reason: Valid,    message: "Policy accepted"}
- {type: Attached, status: "True", reason: Attached, message: "Attached to all targets"}
```

Two consequences for §3.3.2's tuple. The success ancestor is the **Gateway**, never the
`targetRef`. And a poller reading `ancestors[0].conditions` **without checking the ancestor's
shape** treats the synthetic `agentgateway.dev/Gateway/StatusSummary` and the real
`gateway.networking.k8s.io/Gateway/gw` identically — which is how `Accepted=True` came to look
like a success signal. `observedGeneration` is set on every condition, so the
current-generation check is available.

### 2.3 A negative `burst` is accepted by the API **and** by the controller

`burst: -1` with `tokens: 100, unit: Hours` was admitted by the API server, and the controller
then reported `Accepted=True`. Nothing rejects it anywhere. This settles the research §9 gap in
the direction that requires plume to validate `burst >= 0` itself, and confirms the first Codex
review's `minimum: 0` reading was true of `main` and false of the tag.

### 2.4 `tokens` + `burst` together is accepted, and silently inert

The published guide says `burst` "only works with `requests`, not with `token` rate limits".
The schema permits both and the cluster accepts the pair: it is valid, it converges, and its
burst does nothing. **plume must never emit that combination** — no error will catch it.

### 2.5 A daily window is not expressible

`unit: Days` is rejected at admission: *"Unsupported value: \"Days\": supported values:
\"Hours\", \"Minutes\", \"Seconds\""*. Design 03 §3.5's hourly emission is **forced by the API**,
not chosen.

### 2.6 An "inert" route exists — and it fails as an error, not a refusal

Codex r2 BLOCKER 2 asks whether any `HTTPRoute` shape is both policy-attachable and observably
unreachable. A route with `parentRefs` but **no `backendRefs`**:

- reports `Accepted=True`, `ResolvedRefs=True`;
- **accepts a policy attachment** (`Attached=True` on the real Gateway ancestor);
- and returns **HTTP 500** to a live request.

The shape exists, but "inert" is the wrong word: the route is *reachable and answering*, just
erroring. That is safe for a **tightening** update — a 500 leaks nothing — but it means every
tightening transition takes the route **down** for the convergence window rather than serving
the previous configuration. §3.3.2 must state that trade rather than implying a seamless swap:
correctness here costs availability, and an operator who is not told will read the 500s as an
outage.

## 3. Still open — not settled in this pass

| Question | Blocks | Why it resisted |
|---|---|---|
| What does the proxy do with a NACK'd policy — retain the previous config, or fail closed? | Codex r2 BLOCKER 1/2 | Requires forcing a dataplane rejection the control plane still accepts; no manifest reaches that state directly |
| How far over budget do N concurrent requests go? | Codex r2 BLOCKER 3 | Needs a real or mocked LLM backend plus a load generator. The read-only `available_refill()` check means the answer is a measurement, not a formula |
| What is the real evaluation order (authn → rate limit → guards)? | design 03 §3.4 asserts an order inherited from a blog | Needs a policy set that can observe which stage rejected first |

**Reproduce**: `k3d cluster create plume-spike`, Gateway API v1.6.0 standard-install,
`helm install` both 1.4.1 charts into `agentgateway-system`. **Tear down**:
`k3d cluster delete plume-spike`. It is separate from `plume-local`, so `make e2e` is unaffected.
