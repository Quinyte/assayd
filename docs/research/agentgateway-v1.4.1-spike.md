# agentgateway v1.4.1 execution spike — measured, not read

**Status**: 2026-08-27. Offline phase complete; cluster phase complete for seven of eight questions. One remains, named in §3.

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

### 2.7 The token budget is unbounded under concurrency — measured

A mock LLM returning `usage.total_tokens: 1000` per call, behind an `AgentgatewayBackend`
with a `custom` provider, under `rateLimit.local: [{tokens: 1000, unit: Hours}]`. Each
request therefore consumes the **entire hourly budget**. The mock sleeps 2s so requests
overlap.

| Test | Result |
|---|---|
| Serial, budget already spent | `429`, `429` — the limiter works |
| **20 concurrent** | **20 × HTTP 200** = 20,000 tokens against a 1,000-token budget |
| **100 concurrent** | **100 × HTTP 200** = 100,000 tokens against a 1,000-token budget |

One gateway replica throughout. The overshoot tracked **concurrency exactly** and never
touched replica count.

This is decisive for design 03. The bound ADR-0028 published — `gatewayReplicas ×
maxOutputTokens(model)`, on the reasoning that one crossing request per replica can be in
flight — predicts 1,000 tokens of excess here. The measurement is **100,000**. The formula is
wrong by the concurrency factor, and since nothing in `PolicyIntent` or the emitted policy
limits concurrency, **the excess is unbounded**.

The mechanism is visible in the numbers: enforcement is a read-only availability check before
dispatch and a decrement after the response, so every request that starts while the bucket is
positive is admitted. Serial traffic is limited correctly; concurrent traffic is not limited
at all until the first response lands.

**Consequence**: design 03 may not publish an overshoot figure. Either plume enforces a
per-replica concurrency cap and a request-size maximum — neither of which exists in the CRD
surface, so both would have to be plume-side — or the honest statement is that gateway-tier
excess is unbounded at v1.4.1 and the receipt tier is the only real limit. That is the third
budget guarantee this design has had to retract, and the first one retracted by measurement
rather than by review.

### 2.8 A NACK'd tightening silently does not apply, while every condition says converged

The hardest question, and the lever was a config the control plane accepts and the dataplane
does not: `burst: -1`, which §2.3 showed is admitted by both the API server and the controller.

**Sequence.** A healthy policy at `requests: 1000, unit: Hours` serving `200`s. Patch it to
`requests: 1, burst: -1` — a *tightening* update carrying the poison in the same edit.

**What Kubernetes reports:**

```
policy p: Accepted=True(Valid)  Attached=True(Attached)
          generation=4          observedGeneration=4
```

Every element of §3.3.2's convergence tuple is satisfied, at the current generation.

**What actually happened:**

```
agent_xds::client  type=Nack  error="invalid rate limit: max tokens cannot be less than the refill amount"
krtxds            "ADS: ACK ERROR"  code=OK
Event  Warning  AgentGatewayNackError  gateway/gw
  [{"key":"policy/traffic/default/p:rl-local:default/llm-route",
    "error":"error: invalid rate limit: max tokens cannot be less than the refill amount"}]
```

**And the traffic:** eight consecutive requests under a limit that should permit one all
returned **HTTP 200**. The tightening never applied; the previous permissive configuration
kept serving.

Three findings, each load-bearing for design 03:

1. **A NACK'd policy RETAINS the previous config — it does not fail closed.** Research §9 left
   this open and it is the branch that matters: on a tightening update, the *old permissive*
   rule keeps serving indefinitely while the CR reports converged. That is Codex r2 BLOCKER 2,
   reproduced end to end.
2. **The Event is the only observable, and it is a Gateway-scoped Warning.** No condition on
   the policy, the route or the Backend changes. §3.3.2's requirement to watch
   `AgentGatewayNackError` is therefore **mandatory**, not belt-and-braces.
3. **Correlation is partial — better than "uncorrelated", worse than sufficient.** The event key
   `policy/traffic/default/p:rl-local:default/llm-route` names the **policy** (`default/p`) and
   the **route** (`default/llm-route`), so plume can attribute a NACK to a resource it emitted.
   It carries **no generation and no UID**, so an old NACK cannot be distinguished from a new
   one, and absence of an Event cannot be read as success. A bounded wait plus resource
   attribution is workable for *raising* degradation; it is not a success barrier.

**Consequence for §3.3.2.** The convergence tuple is necessary and not sufficient, and this is
now measured rather than argued. Until a positive dataplane acknowledgement exists upstream,
design 03 may claim only control-plane convergence — and a tightening transaction must either
verify by observation (a generation-correlated synthetic request that must be denied before
publication) or accept that a silent NACK leaves the old rule serving.

## 3. Still open — not settled in this pass

| Question | Blocks | Why it resisted |
|---|---|---|
| What is the real evaluation order (authn → rate limit → guards)? | design 03 §3.4 asserts an order inherited from a blog | Needs a policy set that can observe which stage rejected first |

**Reproduce**: `k3d cluster create plume-spike`, Gateway API v1.6.0 standard-install,
`helm install` both 1.4.1 charts into `agentgateway-system`. **Tear down**:
`k3d cluster delete plume-spike`. It is separate from `plume-local`, so `make e2e` is unaffected.
