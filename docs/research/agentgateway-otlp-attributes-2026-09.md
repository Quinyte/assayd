# agentgateway v1.4.1 — what the OTLP trace export actually identifies (2026-09-04; re-verify by 2026-10-15)

**Written for design 04 A5**, which records that `hop.endpoint` and `hop.gatewayInstance` were added with
no producer, and that `ns`/`tenant` attribution has no stated source. It also answers design 03 A50 and
design 20 A7, which withdrew the fallback witness because both fields had no input.

**Companion notes** (not re-derived here): `agentgateway-v1.4.1-2026-08.md` (the tracing field is
`AgentgatewayPolicy.spec.frontend.tracing` and frontend policies may target only a `Gateway`) and
`otel-genai-semconv-2026-08.md` (the pin is a commit-SHA snapshot of `semantic-conventions-genai`, with
monolithic **v1.41** as the frozen fallback baseline).

**Pinned reading point**: tag **`v1.4.1`**, tree SHA `163ea2146acb7b82082acea30ed691b29079095f`, read from a
shallow clone of the tag. Every claim below cites a file and line range at that tag. Semconv claims cite
`open-telemetry/semantic-conventions` **v1.41.0**, the frozen fallback baseline.

---

## 0. Executive answer

| Question | Answer |
|---|---|
| (1) Endpoint identity | **Partial, emitted by default.** Arm and model yes; **instance is the upstream HOST only** (`endpoint`), and it is a *non-semconv* key. Azure `deploymentName`, Vertex `projectId`, and the priority-group provider name are **not emitted anywhere** |
| (2) Gateway replica identity | **Emitted by default** — four resource attributes (`k8s.pod.name`, `service.instance.id`, `k8s.pod.ip`, `k8s.node.name`), because the controller's own Deployment template sets the downward-API env vars. `k8s.pod.uid` is **not** emitted and needs `AgentgatewayParameters.spec.env` |
| (3) Namespace attribution | **The Agent's namespace is not emitted, by any mechanism.** Every namespace on the wire is either the gateway Pod's or the **run** namespace |
| (4) Route identity | **Emitted by default** — `route` = `<route namespace>/<route name>`, plus `route_rule`, `listener`, `gateway`. Design 03 A49 and design 04 A5 both understate what is available |

Two corrections to standing design text fall out of this and are listed in §6.

---

## 1. How a span gets its attributes at all

Three producers, and only three. Knowing which is which is the whole of "can assayd stamp this".

1. **A fixed, hard-coded `kv` vector** built per request, then handed to `Tracer::send`.
   `crates/agentgateway/src/telemetry/log.rs#L1354-L1600`. This is the same vector that feeds the access
   log, the OTLP log exporter and the SQL log store — the trace is one consumer of it.
   `Tracer::send` adds `url.scheme` and `network.protocol.version` and nothing else.
   `crates/agentgateway/src/telemetry/trc.rs#L278-L346`.
2. **Per-request CEL**, from `spec.frontend.tracing.attributes` — `add[]` (max 64) evaluated against the
   full request context, `remove[]` (max 32) which suppresses entries from the fixed vector.
   Suppression is `LoggingFields::has` at `log.rs#L347-L351`, applied by the first filter in
   `Tracer::send`. CRD types: `Tracing` at
   `controller/api/v1alpha1/agentgateway/agentgateway_policy_types.go#L3273-L3318`,
   `AttributeAdd` at `#L3229-L3234`.
3. **Resource attributes**, computed **once at Tracer construction**, from a defaults block plus
   `spec.frontend.tracing.resources[]` CEL. `trc.rs#L164-L226` and `#L567-L636`.
   ⚠️ The `resources[]` executor is `cel::Executor::new_empty()` (`trc.rs#L189`,
   `cel/types.rs#L601-L603`) — **no request context at all**. A resource expression can only be a
   constant. `request.*`, `backend.*`, `llm.*` all evaluate to nothing and the pair is silently dropped.

**Consequence for assayd, stated once**: the only per-request stamping surface is producer 2, and it lives
on a **Gateway-scoped** policy. A per-Agent constant cannot be stamped; only a CEL expression over
something already in the request can.

---

## 2. (1) Endpoint identity — what identifies WHICH endpoint served an LLM request

### 2.1 Emitted by default

| assayd needs | attribute key | value at v1.4.1 | source |
|---|---|---|---|
| arm (provider) | **`gen_ai.provider.name`** | `AIProvider::provider()` | `log.rs#L1477-L1480`, `llm/mod.rs#L750-L765` |
| model (requested) | **`gen_ai.request.model`** | the **client's** `model` field, verbatim from the request body | `log.rs#L1481-L1484`; `crates/llm/src/types/completions.rs#L328-L361` |
| model (served) | **`gen_ai.response.model`** | the `model` the provider returned | `log.rs#L1485-L1490` |
| instance — **host only** | **`endpoint`** | `<host>:<port>` of the resolved upstream | `log.rs#L1376`; assigned at `proxy/httpproxy.rs#L2234-L2236`; `Display for Target` at `types/agent.rs#L3056-L3065` |
| hop class | **`protocol`** | `http` \| `tcp` \| `a2a` \| `mcp` \| `llm` | `log.rs#L1399`; enum at `cel/types.rs#L464-L471` |
| cost | `agw.ai.usage.cost.total`, and on traces only `agw.ai.usage.cost.{input,output,cache_read,cache_write,reasoning,input_audio,output_audio}` | | `log.rs#L1514-L1517`, `#L1303-L1317` |

`gen_ai.usage.input_tokens` / `.output_tokens` / `.cache_read.input_tokens` / `.cache_creation.input_tokens`
are also emitted (`log.rs#L1491-L1512`), plus the `gen_ai.request.*` parameter set.

`endpoint` is the resolved connector target. For a managed provider with no `host` override it is
`AIProvider::default_connector_target()` (`llm/mod.rs#L985-L996`), i.e. the provider's own host:

| arm | `gen_ai.provider.name` | `endpoint` host | instance fields visible in it |
|---|---|---|---|
| `azureopenai` / `azure` | `azure` | `<resourceName>.openai.azure.com:443` or `<resourceName>.services.ai.azure.com:443` | the Azure **resource**, i.e. the CRD's `endpoint` |
| `vertexai` | `gcp.vertex_ai` | `<region>-aiplatform.googleapis.com:443` (or `aiplatform.googleapis.com` for unset/`global`, `aiplatform.{us,eu}.rep.googleapis.com`) | **region only — `projectId` is absent** |
| `bedrock` | `aws.bedrock` | `bedrock-runtime.<region>.amazonaws.com:443` | region |
| `openai` | `openai` | OpenAI default host | — |
| `anthropic` | `anthropic` | Anthropic default host | — |
| `gemini` | `gcp.gemini` | Gemini default host | — |
| `custom` | `custom`, or `providerOverride` | the required `host` override | whatever the host encodes |

Sources: `crates/llm/src/azure.rs#L38,L97-L106`, `crates/llm/src/vertex.rs#L29,L166-L180`,
`crates/llm/src/bedrock.rs#L28,L62-L69`, `crates/llm/src/{openai,anthropic,gemini,copilot,custom}.rs#L15`.

### 2.2 NOT emitted — plainly

- **`server.address` / `server.port` are never set.** Grep the kv vector: the key is `endpoint`, a
  single `host:port` string, and it is **not** an OTel semantic convention. semconv v1.41.0 lists
  `server.address` as `recommended` on the GenAI client span and `server.port` as conditionally required
  beside it (`model/gen-ai/spans.yaml#L32-L38`). A assayd transform that maps `endpoint` → `server.address`
  must split it itself and record that it is doing so.
- **Azure `deploymentName` is not emitted, under any key.** In the CRD it is
  `AzureOpenAIConfig.deploymentName` (`controller/api/v1alpha1/agentgateway/agentgateway_backend_types.go#L397-L417`),
  and the controller maps it to the dataplane provider's **`model`** field, not to an identity field
  (`controller/pkg/syncer/backend/backend_plugin.go#L489-L500`). In the dataplane it survives only as a
  path segment: `azure.rs#L74-L86` builds `/openai/deployments/{model}/{suffix}?api-version=...`.
  The path on the span (`http.path`, `log.rs#L1380`) is the **downstream** path, captured at request
  ingress before any provider rewrite (`proxy/httpproxy.rs#L721-L731`) — so the deployment segment never
  reaches telemetry.
- **Vertex `projectId` is not emitted.** It is in the request path, not the host (`vertex.rs#L16-L26`).
- **The priority-group provider name is not emitted.** `NamedLLMProvider.name` is the failover unit and
  the `sectionName` for policy targeting (`agentgateway_backend_types.go#L197-L214`); the selected
  provider is chosen at `httpproxy.rs#L2026-L2037` and its name is used **only** to look up sub-backend
  policies. Nothing carries it to the log or the span. So a Backend with two providers in one group
  produces receipts that cannot say which one served, except insofar as their hosts differ.
- **The `AgentgatewayBackend` name is not a span attribute.** `backend.name` exists in **CEL**
  (`cel/types.rs#L412-L421`, value `<namespace>/<backend name>` per `types/agent.rs#L1648-L1666`) and is a
  Prometheus label (`log.rs#L1145-L1150`), but it is absent from the trace kv vector.

### 2.3 The design 02 A24 question, answered directly

> Is the deployment/instance distinguishable for two Azure OpenAI deployments on different endpoints with
> the same model name?

**Yes — by `endpoint`**, which differs (`ai-gateway.openai.azure.com:443` vs
`ai-gateway-2.openai.azure.com:443`). That is exactly the upstream example in the CRD doc comment at
`agentgateway_backend_types.go#L160-L178`, and it is the case design 02 A24 exists for. So design 04 A4's
owed conformance case — *two same-arm, same-model endpoints produce distinguishable receipts* — **can be
written today and will pass**, provided the transform keys on `endpoint` and not on `gen_ai.*` alone.

**But the neighbouring case fails.** Two deployments on the **same** Azure resource differing only in
`deploymentName` (upstream's own `apiVersion != v1` shape, where `deploymentName` is CEL-required —
`agentgateway_backend_types.go#L398`) are **indistinguishable**: same `gen_ai.provider.name`, same
`endpoint`, and `gen_ai.request.model` carries the client's string, not the deployment. Assayd's
`LLMEndpoint{arm: azureopenai, endpoint, deploymentName}` is therefore **not** fully recoverable from the
export. Design 02 A53's CEL rule that `azureopenai` carries no `model` because *"the deployment is the
identity"* is right about the CRD and wrong about the telemetry: the identity the CRD insists on is the
one field the export drops.

### 2.4 A semconv value drift worth pinning now

`gen_ai.provider.name` is a **closed enum** in semconv v1.41.0
(`model/gen-ai/registry.yaml#L8-L76`). agentgateway matches it for `openai`, `anthropic`, `gcp.gemini`,
`gcp.vertex_ai`, `aws.bedrock` — and **does not** for Azure: it emits **`azure`**, where the enum has
`azure.ai.openai` and `azure.ai.inference`. `copilot` and `custom` are also off-enum. The design 04
transform must map, not pass through, and the mapping is lossy in the same place §2.3 is: agentgateway
collapses the CRD's `azureopenai` and `azure` arms onto one dataplane provider whose name is `azure`, so
the transform cannot recover which CRD arm was configured from the export alone.

---

## 3. (2) Gateway replica identity

**Emitted by default, as OTLP *resource* attributes**, computed once per proxy process at
`trc.rs#L567-L636` and attached to every `ResourceSpans` payload (`trc.rs#L509-L558` for the custom gRPC
exporter; the HTTP exporter carries the provider's resource in the usual way).

| resource attribute | value | env var | source |
|---|---|---|---|
| `k8s.pod.name` | the Pod name | `POD_NAME` | `trc.rs#L595` |
| `k8s.namespace.name` | the **gateway Pod's** namespace | `NAMESPACE` | `trc.rs#L596` |
| `k8s.node.name` | node name | `NODE_NAME` | `trc.rs#L597` |
| `k8s.pod.ip` | Pod IP | `INSTANCE_IP` | `trc.rs#L598-L601` |
| **`service.instance.id`** | `agentgateway~<podIP>~<podName>.<ns>~<ns>.svc.cluster.local` | derived | `trc.rs#L603-L606`; grammar at `crates/core/src/env.rs#L24-L46` |
| `host.name` | `cfg.self_addr` hostname | — | `trc.rs#L607-L610` |
| `service.name` | the **Gateway** name (xDS config), else `OTEL_SERVICE_NAME`, else `agentgateway` | `GATEWAY` | `trc.rs#L612-L631`, `#L219-L223` |
| `service.namespace` | the **Gateway's** namespace | `NAMESPACE` | `trc.rs#L616-L621` |
| `service.version` | build version | — | `trc.rs#L186-L189` |

**These arrive without configuration** on any controller-deployed Gateway: the controller renders an
embedded Helm chart (`controller/pkg/helm/embed.go`) whose container template sets `NODE_NAME`,
`POD_NAMESPACE`, `POD_NAME`, `NAMESPACE`, `GATEWAY` and `INSTANCE_IP` from the downward API by default —
`controller/pkg/helm/agentgateway/templates/_helpers.tpl#L170-L235`. Each is skipped only if the user
supplies an env var of the same name. So **assayd's chart has to do nothing** for §2's four Pod-identity
attributes to appear.

**`k8s.pod.uid` is NOT emitted and there is no code path that would emit it.** If assayd wants it, the
route is env config, and it **is** reachable from the CRD surface, not only from the Deployment:
`AgentgatewayParameters.spec.env` is a `[]corev1.EnvVar`
(`controller/api/v1alpha1/agentgateway/agentgateway_parameters_types.go#L161-L175`), merged into the
rendered Deployment at both GatewayClass and Gateway scope
(`controller/pkg/deployer/agentgateway_parameters.go#L164,L455-L462`), so a `valueFrom.fieldRef` and a
`$(VAR)` expansion work:

```yaml
spec:
  env:
    - name: POD_UID
      valueFrom: { fieldRef: { fieldPath: metadata.uid } }
    - name: OTEL_RESOURCE_ATTRIBUTES
      value: "k8s.pod.uid=$(POD_UID)"
```

`OTEL_RESOURCE_ATTRIBUTES` is parsed at `trc.rs#L570-L584` as the **lowest** precedence layer — the
config-derived attributes above override any key that collides, and `k8s.pod.uid` collides with none.
Upstream's own doc comment discourages `$(VAR_NAME)` expansion (`agentgateway_parameters_types.go#L161-L170`),
which is a caveat to record rather than a blocker.

**Grammar, since design 04 A5 asks for it explicitly.** Neither candidate is stable across a restart:

- `k8s.pod.name` — for a Deployment, `<gateway>-<replicaset-hash>-<5 chars>`; a restarted Pod gets a new
  name. Stable for a Pod's lifetime.
- `service.instance.id` — **contains the Pod IP as well as the Pod name**, so it changes when either
  changes, and it is not a bare identifier: it is a four-field `~`-separated string ending in a
  cluster-local FQDN. Set only when `POD_NAME` *and* `NAMESPACE` are both non-empty (`trc.rs#L603-L605`).
- `k8s.pod.uid` (if configured) — genuinely unique per Pod and never reused, which is the property a
  rollout-survival test wants; it is also the one that requires configuration.

None of the three is stable *across* a rollout, which is the property design 03 A50 and design 20 A7
already concluded a per-replica count cannot have. **Naming a producer does not revive those witnesses** —
A50's second reason (nothing routes a canary to a chosen replica through a Service) is untouched by this
note, and stands.

---

## 4. (3) Namespace attribution

**Nothing in the export identifies the Agent's own namespace.** Exhaustively, the namespaces on the wire:

| where | key | whose namespace |
|---|---|---|
| resource | `k8s.namespace.name` | the **gateway Pod's** |
| resource | `service.namespace` | the **Gateway's** (`cfg.xds.namespace`, `trc.rs#L612-L615`) |
| span | `gateway` | the Gateway's, as `<ns>/<name>` (`types/agent.rs#L810-L816`) |
| span | `route` | the **HTTPRoute's**, as `<ns>/<name>` (`types/agent.rs#L770-L773`) |
| CEL only | `proxy.route.namespace`, `proxy.gateway.namespace`, `backend.name` | route / Gateway / Backend namespace (`cel/types.rs#L141-L173`, `#L412-L421`) |

Under design 02 A42 the route, the Backend and the workload Service all live in `assayd-run-<ns>`, so
**every** namespace above is either the gateway's or the run namespace. Design 04 A5's statement of the
problem is exactly right, and this note adds that there is no attribute it overlooked.

**The available mechanism is a naming convention, not an attribute.** Design 03 §3.2 already emits routes
and Backends into `assayd-run-<agent-namespace>` under a deterministic name
(`<name>-<concern>[-<rev>]`, truncation to a 16-hex suffix past 63 chars). If the **run namespace name**
is a total, injective function of the Agent namespace — which design 02 A42's `assayd-run-<ns>` scheme is,
modulo the same truncation rule — then `route`'s namespace segment is a decodable encoding of the Agent's
namespace, and the transform can invert it without a Kubernetes lookup. **That inversion must be
specified, not assumed**: the truncation-and-hash rule is not invertible by string manipulation, so the
transform needs either a lookup table the operator maintains or a Kubernetes read.

That is the honest shape of the choice design 04 A5 owes:

- **Option A — invert the name.** Stateless, matches §4's "stateless per batch", but fails on any
  truncated namespace and needs a stated fallback.
- **Option B — read the Agent.** §2 already puts the tap inside the agent-operator binary, where a client
  and an informer cache are in hand; key the cache by the `route` attribute, or by the
  `assayd.dev/agent-uid` label design 03 §3.2 stamps on every emitted resource. This is a real change to
  §4's "stateless" claim and should be written as one.
- **Option C — stamp it.** §5 below. Costs a per-route policy and leaks the value upstream unless paired
  with a removal.

Whichever is chosen, design 04 A5's other owed item is independent of it and is confirmed here: the audit
index's row set carries **no `ns` column**, so two same-named Agents in different namespaces share one
budget row today regardless of which option lands.

---

## 5. (4) Route identity — and the correction it forces

**Emitted by default, and richer than either consuming design assumes.** Built at
`log.rs#L1126-L1140`, emitted at `log.rs#L1366-L1375`:

| attribute | value | grammar |
|---|---|---|
| **`route`** | the serving HTTPRoute | **`<namespace>/<name>`** — `RouteName::as_route_name`, `types/agent.rs#L770-L773` |
| **`route_rule`** | the matched rule's `name`, when the rule has one | bare rule name, `Option` (`types/agent.rs#L757-L768`) |
| `listener` | the listener name | bare name (`types/agent.rs#L794-L800`) |
| `gateway` | the serving Gateway | **`<namespace>/<name>`** — `ListenerName::as_gateway_name`, `types/agent.rs#L814-L816` |

All four are `DefaultedUnknown`, so an unmatched request omits them rather than emitting a placeholder.

`route_rule` is the Gateway API `rules[].name` field; v1.4.0 moved the build target to Gateway API v1.6,
so it is available for assayd to set.

**This contradicts design 03 A49 and design 04 A5 in assayd's favour.** A49 says *"the envelope carries no
route, revision or generation… nothing can attribute one"* and defers its withdrawal-escalation test on
that basis; design 04 A5's third paragraph says the tap has no route identity. The **envelope** indeed has
no route field — that part is true and the field is still owed — but the **export** carries a fully
qualified route identity with no stamping and no configuration. And because design 03 §3.2's names are
deterministic and carry `-<rev>` on revision routes, `route` names the *revision*, not merely the Agent.
So A49's sound positive witness — *a receipt attributing traffic to a route assayd withdrew* — is
**buildable now**: add `hop.route` to `receipt/v1` from the `route` attribute (plus `hop.routeRule` from
`route_rule` if rule-level attribution is wanted), and the deferred conformance case is unblocked once
the drain allowance A49 also names is specified. No new agentgateway capability is required.

---

## 6. What a assayd-owned stamping mechanism would have to look like

Only for the things §2–§4 say are genuinely absent: `deploymentName`, `projectId`, the selected
priority-group provider, and the Agent's own namespace.

**The `hop.type` precedent, checked — and it is not what design 03 §3.1 says it is.** That clause reads
*"`hop.type` … is derived from the backend class stamped into span attributes by design-03-emitted
resources."* No stamping exists or is needed: agentgateway emits **`protocol`** by default
(`log.rs#L1399`), a closed 5-value enum `http|tcp|a2a|mcp|llm` derived from the Backend kind
(`types/agent.rs#L1681-L1688`). That covers `llm`, `mcp_tool`, `a2a` and `http` directly. It does **not**
cover `kg_query`: design 03 emits an `AgentgatewayBackend` for KG the same as for tools, so both report
the same `protocol`, and `kg_query` must be recovered from the Backend or route **name**, not from a
backend class. §3.1's sentence should be corrected in both halves — the stamping does not exist, and the
attribute that does exist is not sufficient for assayd's own hop taxonomy.

**The one real per-request stamping surface** is `spec.frontend.tracing.attributes.add[]`, CEL, on the
single Gateway-scoped tracing policy. Since a frontend policy may target only a Gateway, assayd cannot
attach a different constant per Agent; the expression must read something already in the request. Two
shapes work:

1. **Derive from what is already there.** `backend.name` (`<ns>/<AgentgatewayBackend name>`) and
   `proxy.route.{namespace,name,rule}` are in the CEL context (`cel/mod.rs#L360-L430`,
   `cel/types.rs#L141-L173`, `#L412-L421`), so one Gateway-scoped policy can lift them into span
   attributes:
   ```yaml
   frontend:
     tracing:
       attributes:
         add:
           - { name: assayd.backend, expression: "backend.name" }
   ```
   This costs one policy for the whole cluster and no per-route resource. It recovers the **Backend**,
   which is the closest available proxy for "which endpoint" when a Backend enumerates exactly one
   provider — and design 03 §3.4.1 already emits Backends by construction, one enumerated destination set
   per Agent. It does **not** recover which provider within a priority group served.
2. **Inject and lift.** A route-scoped `traffic.transformation.request.set[]`
   (`agentgateway_policy_types.go#L877`, `Transform` at `#L2441-L2470`) adds a header carrying the Agent's
   namespace/endpoint id; the Gateway tracing policy lifts it with
   `request.headers['x-assayd-agent']`. ⚠️ **A request transformation forwards the header upstream** — to
   the LLM provider — so this needs a paired removal at the backend transformation, and it is a new
   header on a serving path, i.e. a behaviour-surface change under design 02 A25's rule. Prefer shape 1.

**Neither shape can produce `deploymentName` or `projectId`**, because neither is in the CEL context: the
LLM context (`cel/types.rs#L1394-L1460`) exposes `streaming`, `requestModel`, `responseModel`, `provider`
and the token/cost counts, and nothing about the provider's instance configuration. Recovering those
requires assayd to **resolve them from its own compiled spec** — the `LLMEndpoint` it emitted into the
Backend — keyed by `(backend.name or route, gen_ai.provider.name, endpoint)`. That is a lookup the
operator can do without upstream change, and it is the honest answer for design 04 A5's `hop.endpoint`:
*the export names the arm, the host and the model; the deployment/project comes from assayd's own record of
what it compiled, joined on the Backend identity the export does carry.*

---

## 7. Findings that contradict current design text

1. **design 03 §3.1 (via design 04 §3.1)** — "the backend class stamped into span attributes by
   design-03-emitted resources". No such stamping exists. `protocol` is emitted by default and is a
   5-value enum that cannot express `kg_query`. Both halves of the sentence need correction.
2. **design 03 A49 / design 04 A5** — "nothing can attribute a receipt to a route assayd withdrew".
   `route` = `<ns>/<name>` is emitted by default, and design 03's deterministic names carry the revision.
   The envelope field is owed; the *producer* exists. A49's deferral reason should be narrowed to the
   drain allowance alone.
3. **design 04 A5 / design 20 A7** — "`hop.gatewayInstance` has no producer". It has four, all default:
   `k8s.pod.name`, `service.instance.id`, `k8s.pod.ip`, `k8s.node.name`. This does **not** revive the
   withdrawn witnesses — A50's per-replica-routing argument is independent and stands — but the reason
   given must change from "no input" to "no proof", or the amendment misstates the state of the world.
4. **design 04 A4's owed conformance case** — writable today and expected to pass on the
   different-endpoint case, and expected to **fail** on the same-endpoint/different-deployment case. The
   case should be written as both, so the limit is measured rather than assumed away.
5. **design 04 §3.1's semconv pin** — the transform must *map* `gen_ai.provider.name`, not pass it
   through: agentgateway emits `azure`, which is not in semconv v1.41.0's closed enum, and the mapping
   from `azure` back to the CRD's `azureopenai` vs `azure` arm is not recoverable from the export.
6. **design 04 §4's "stateless transform per batch"** — incompatible with `ns` attribution under design 02
   A42 unless the run-namespace name is invertible. §4 above states the three options; one must be chosen
   and §4 of design 04 amended to match.

---

## 8. Stated gaps — NOT verified

- **Whether the deployer's rendered Deployment is what a assayd install actually gets.** Verified that the
  controller's embedded chart sets the downward-API env vars by default. Not verified against a running
  cluster, and design 07 A1 records that assayd's chart does not yet carry the agentgateway subchart. A
  spike should assert the four resource attributes on a real export before design 04 depends on them.
- **MCP sub-spans.** `log.trace_spans` buffers spans created during MCP processing and flushes them after
  the request span (`log.rs#L1617-L1628`). Their attribute sets were **not** read. Design 04 §3.1 claims
  "MCP tool spans with params/results/latency"; the request span carries `mcp.method.name`, `mcp.target`,
  `mcp.resource.{type,uri}`, `gen_ai.tool.name`, `gen_ai.prompt.name`, `mcp.session.id`,
  `mcp.error.{code,message}` (`log.rs#L1426-L1449`), but per-tool sub-span shape is unverified.
- **`OTEL_SERVICE_NAME` / `OTEL_RESOURCE_ATTRIBUTES` precedence in a real Pod.** Read from source
  (`trc.rs#L567-L636`); the `$(VAR)` expansion ordering requirement in Kubernetes was not exercised.
- **Whether `route_rule` is populated when the HTTPRoute rule has no `name`.** By inspection it is
  `Option` and would be omitted; not exercised.
- **Sampling.** `randomSampling` / `clientSampling` / `filter` all gate whether a span exists at all
  (`trc.rs#L292-L296`). Design 04 treats the export as the receipt source; a sampled-out request produces
  **no receipt**. This note did not check what the chart's default sampling is, and it is load-bearing for
  an audit log.

## Evidence ledger

| Check | Result | Evidence |
|---|---|---|
| Export identifies the LLM arm | **CONFIRMED** | `gen_ai.provider.name`, `log.rs#L1477-L1480` |
| Export identifies the upstream host | **CONFIRMED** | `endpoint`, `log.rs#L1376`; `httpproxy.rs#L2234-L2236` |
| Export emits `server.address`/`server.port` | **REFUTED** | absent from the kv vector; the key is `endpoint`, non-semconv |
| Two Azure deployments on different endpoints are distinguishable | **CONFIRMED** | `endpoint` differs; `azure.rs#L97-L106` |
| Two Azure deployments on one endpoint are distinguishable | **REFUTED** | `deploymentName` → dataplane `model` → path only; `backend_plugin.go#L489-L500`, `azure.rs#L74-L86` |
| `gen_ai.request.model` carries the deployment | **REFUTED** | it is the client's body `model`; `types/completions.rs#L328-L332` |
| Vertex `projectId` is emitted | **REFUTED** | region is in the host, project is in the path; `vertex.rs#L16-L26,L166-L180` |
| The selected priority-group provider is emitted | **REFUTED** | used only for policy lookup; `httpproxy.rs#L2026-L2037` |
| `gen_ai.provider.name` value matches semconv for Azure | **REFUTED** | emits `azure`; enum has `azure.ai.openai` / `azure.ai.inference` (semconv v1.41.0 `registry.yaml#L43-L49`) |
| Replica identity is emitted by default | **CONFIRMED** | `trc.rs#L595-L610`; env from `_helpers.tpl#L170-L235` |
| `k8s.pod.uid` is emitted | **REFUTED** | no code path; needs `AgentgatewayParameters.spec.env` |
| Replica identity is settable via the CRD surface | **CONFIRMED** | `agentgateway_parameters_types.go#L161-L175`; `deployer/agentgateway_parameters.go#L164,L455-L462` |
| `service.instance.id` is stable across a Pod restart | **REFUTED** | contains Pod name *and* Pod IP; `core/src/env.rs#L29-L34` |
| Any attribute names the Agent's own namespace | **REFUTED** | every namespace is the gateway's or the route's (run) namespace |
| Route identity is emitted by default | **CONFIRMED** | `route` = `<ns>/<name>`, `log.rs#L1375`; `types/agent.rs#L770-L773` |
| Rule-level route identity is emitted | **CONFIRMED** | `route_rule`, `log.rs#L1371-L1374` |
| `hop.type` needs assayd stamping today | **CONFIRMED** | `protocol` is emitted but its enum has no `kg_query`; `cel/types.rs#L464-L471` |
| Per-request CEL stamping exists on the tracing policy | **CONFIRMED** | `attributes.add[]`, `agentgateway_policy_types.go#L3229-L3234`; evaluated at `trc.rs#L314-L327` |
| `resources[]` CEL can read the request | **REFUTED** | `Executor::new_empty()`, `trc.rs#L189`; constants only |
| Default attributes can be suppressed | **CONFIRMED** | `attributes.remove[]` via `LoggingFields::has`, `log.rs#L347-L351`, `trc.rs#L285-L287` |

**Re-verify by 2026-10-15**, or immediately if agentgateway cuts v1.5.0 final, or if the
`semantic-conventions-genai` repo cuts its first release (which would move the pin off the v1.41 baseline
and could rename `gen_ai.provider.name` again).
