# agentgateway v1.4.1 — load-bearing facts for design 03 (2026-08-27; re-verify by 2026-10-15)

**Replaces `agentgateway-2.2-2026-08.md`.** Delete that file when design 03 is amended; it is
wrong in ways that would have been encoded into golden fixtures.

Everything below is grounded in the published API reference, the CRD Go types, the dataplane Rust
source, or upstream test fixtures. Every source claim cites a URL and a tag or commit SHA.
Section 9 lists what could **not** be established from a primary source — those are gaps, not
inferences.

**Pinned reading points**
- Release tag **`v1.4.1`** (2026-07-29) — the current stable, and the version plume should pin.
- Commit **`3d74b332afcaae70479a030439c1d854df16abd2`** (2026-08-26) — `main`, cited only where
  it differs from v1.4.1 and the difference matters.
- Commit **`53f395260c66edae58857485765d5724680e8567`** (2026-08-26) — the commit the Codex review
  pinned. See §1 for why that matters.

---

## 0. What the old note got wrong

| Old claim | Status | Correction |
|---|---|---|
| "agentgateway 2.2" is the version to pin | **Wrong — the version does not exist** | agentgateway has never released a 2.x. Releases are `v1.x`; latest stable **v1.4.1**. "2.2" is a *documentation* path segment inherited from kgateway's release train. |
| Sourced from `agentgateway.dev/docs/kubernetes/2.2.x/...` | **Wrong — stale docs** | `2.2.x` is a frozen old doc version that still returns HTTP 200. It predates `AgentgatewayModel`, `virtualModel` and `oauthTokenExchange` entirely (§1). The whole note was written against documentation older than the features it claimed to pin. |
| Pin a minimum version for "OSS token exchange and virtual models" | **Right idea, no version given** | Both landed in **v1.4.0** (2026-07-27). The floor is v1.4.0; pin **v1.4.1**. |
| CRDs are `AgentgatewayBackend`, `AgentgatewayParameters`, `AgentgatewayPolicy` | **Incomplete** | A fourth kind, **`AgentgatewayModel`**, shipped in v1.4.0 — and it is **experimental and off by default**. |
| Budget/spend limits are "Solo Enterprise — OSS designs must not depend on them" | **Now wrong, but still unusable** | OSS API-key-scoped budgets ($ and token) landed on `main` after v1.5.0-beta.1. They are **standalone-mode only** (Postgres/SQLite dependency), not on the Kubernetes CRD surface, and unreleased. Conclusion for plume is unchanged; the *reason* is different. |
| Token-based limits: "token-based limiting for LLM traffic exists in OSS" | **True but dangerously incomplete** | It exists and it **cannot reject the crossing request** on the Kubernetes path. `tokenize` is hardcoded off there. §3. |
| OTLP tracing configured via `frontendPolicies` | **Wrong field path** | The field is `AgentgatewayPolicy.spec.frontend.tracing`. There is no `frontendPolicies`. §7. |
| MCP tool filtering via `Mcp-Name`/`Mcp-Method` headers | **Superseded** | A first-class CEL tool-filter exists: `spec.backend.mcp.authorization`, which filters `tools/list` items and rejects `tools/call`. §6. |
| R1 (same-level overlap): "one secondary source reports silent overwrite by creation order" | **Wrong mechanism, worse reality** | It is not creation order. Same-level conflicts resolve by **`std::HashSet` iteration order** — unspecified, and randomly seeded per process. §7. |
| "Evaluation order: authn → rate limiting → prompt guards" | **Not re-verified** | Not checked in this pass. Treat as unverified. §9. |

---

## 1. VERSION

- **Latest stable: `v1.4.1`, 2026-07-29.** Latest overall: `v1.5.0-beta.1`, 2026-08-25 (prerelease).
  Minor line history: v1.3.0 (2026-06-18) → v1.4.0 (2026-07-27) → v1.4.1 (2026-07-29).
  https://api.github.com/repos/agentgateway/agentgateway/releases
- **There is no agentgateway 2.2.** The `2.2.x` in the old note's URLs is a docs-site path.
  Verified by fetching the API reference at three doc versions and counting feature mentions:

  | doc version | HTTP | `AgentgatewayModel` | `virtualModel` | `oauthTokenExchange` |
  |---|---|---|---|---|
  | `2.2.x` | 200 | 0 | 0 | 0 |
  | `latest` | 200 | 40 | 12 | 29 |
  | `main` | 200 | 40 | 12 | 29 |

  `2.3.x` and `1.4.x` both 404. So `2.2.x` is a **frozen pre-v1.4 doc version that still serves
  200** — citing it looks successful and silently returns stale content. Use `latest` or `main`.
  https://agentgateway.dev/docs/kubernetes/latest/reference/api/

- **API group/version is unchanged and stable: `agentgateway.dev/v1alpha1`.**
  https://github.com/agentgateway/agentgateway/blob/v1.4.1/controller/api/v1alpha1/agentgateway/doc.go

- **The controller split described in the old note is real and settled.** Kinds now registered:
  `AgentgatewayPolicy` (`agpol`), `AgentgatewayBackend` (`agbe`), `AgentgatewayParameters`,
  and — new in v1.4.0 — **`AgentgatewayModel`** (`agmodel`).

- **Version floor for the two features plume's architecture pins (`docs/architecture.md:492`):
  v1.4.0.** Verified by fetching the CRD types at each tag:

  | tag | `AgentgatewayModel` type file | `oauthTokenExchange` | `virtualModel` |
  |---|---|---|---|
  | v1.3.1 | 404 (absent) | absent | absent |
  | v1.4.0 | present | present | present |
  | v1.4.1 | present | present | present |
  | v1.5.0-beta.1 | present | present | present |

  `oauthTokenExchange` appears in **two** places: `BackendAuth`
  (https://github.com/agentgateway/agentgateway/blob/v1.4.1/controller/api/v1alpha1/agentgateway/agentgateway_policy_types.go#L1494-L1496)
  and the `AgentgatewayModel` auth union. Doc comment: *"OAuth 2.0 token exchange (RFC 8693) /
  jwt-bearer (RFC 7523) authentication."* It is OSS — it is in the OSS repo's CRD types and the
  v1.4.0 release notes advertise it.

- ⚠️ **`AgentgatewayModel` is experimental and off by default.** From the v1.4.0 release notes:
  *"The Kubernetes deployment has a new (off by default) experimental `AgentgatewayModel` API"*.
  https://github.com/agentgateway/agentgateway/releases/tag/v1.4.0
  **Virtual models are therefore an experimental, opt-in surface.** Any plume design that treats
  virtual models as a stable traffic-shifting mechanism (architecture §272 does) must say so and
  carry a fallback. This is a change plume has not absorbed.

- **v1.4.0 breaking changes plume's chart must absorb:** Gateway API **v1.6** is now the build
  target and the controller uses **`TCPRoute` v1** (was `v1alpha2`) — *"Re-apply the Gateway API
  CRDs that match this release before you upgrade."* Also: MCP request-phase guardrail rejections
  now return **HTTP 200** with a JSON-RPC error body, not a non-200 status.

- **Artifacts to pin** (registry moved off the old paths):
  `cr.agentgateway.dev/agentgateway:v1.4.1`, `cr.agentgateway.dev/controller:v1.4.1`,
  charts `cr.agentgateway.dev/charts/agentgateway:v1.4.1` and
  `cr.agentgateway.dev/charts/agentgateway-crds:v1.4.1` (CRDs are a **separate chart** — relevant
  to plume's tiered install).

- ⚠️ **The Codex review pinned `53f3952`, which is a `main` commit dated 2026-08-26 — one day
  before this note and never part of any release.** Its citations are accurate *for main*. Where
  main and v1.4.1 differ, this note flags it (see §4 on `burst`). Do not build golden fixtures
  against `53f3952`.

---

## 2. APPLY / ATTACH STATUS — the complete contract

**Short answer to "which condition tuple, at which generation, proves a policy is genuinely
enforcing on live traffic?" — no available tuple proves that.** Every condition agentgateway
publishes is computed control-plane-side from translation and reference resolution. There is no
dataplane acknowledgement anywhere in the status surface (§2.4). The strongest *available*
predicate is defined in §2.5; it is strictly weaker than "enforcing", and design 03 must say so.

### 2.1 `AgentgatewayPolicy`

- **Status type is Gateway API `PolicyStatus`** — i.e. `status.ancestors[]`, not `status.conditions[]`.
  https://github.com/agentgateway/agentgateway/blob/v1.4.1/controller/api/v1alpha1/agentgateway/agentgateway_policy_types.go#L41
- **Exactly two condition types: `Accepted` and `Attached`.** Both are declared with their
  legal reasons:
  https://github.com/agentgateway/agentgateway/blob/v1.4.1/controller/api/v1alpha1/agentgateway/policy_types.go#L20-L32
  - `Accepted`: True ← `Valid`; also True ← `PartiallyValid`. False ← `Pending`, `Invalid`.
  - `Attached`: True ← `Attached`, `Merged`. False ← `Pending`, `Overridden`.
  - ⚠️ `Merged` and `Overridden` are documented as legal reasons but **have no Go constants and
    are never written** by the v1.4.1 translation path. Only `Valid`, `PartiallyValid`, `Invalid`,
    `Pending`, `Attached` are actually emitted.
- **`observedGeneration` IS set, on every condition, from `policy.Generation`.**
  `setConditions(generation, ...)` sets `ObservedGeneration: generation` on both branches.
  https://github.com/agentgateway/agentgateway/blob/v1.4.1/controller/pkg/agentgateway/plugins/status.go
  (The upstream golden fixtures show no `observedGeneration` only because fixture objects have
  `generation: 0`, which is omitted by `omitempty`. It is present in a real cluster.)

**The ancestor structure is the part that breaks a naive poller.**

- On success, one ancestor **per resolved Gateway** — `ancestorRef` is
  `{group: gateway.networking.k8s.io, kind: Gateway, name, namespace}`. **Not the targetRef.**
  A policy targeting an HTTPRoute reports its status against the *Gateway* that route is attached
  to. Confirmed by fixture `trafficpolicy/cors.yaml`.
- On attachment failure, **no real ancestor is emitted at all**. Instead a single **synthetic**
  ancestor is appended: `{group: agentgateway.dev, name: StatusSummary}` — no kind, no namespace —
  carrying `Attached=False, Reason=Pending`.
  https://github.com/agentgateway/agentgateway/blob/v1.4.1/controller/pkg/agentgateway/plugins/traffic_plugin.go#L299
  This is exactly the fixture already confirmed (`http-route-missing-not-attached.yaml`): the
  policy reports `Accepted=True` + `Attached=False` on `StatusSummary`, and `output: []`.
- **Consequence:** a poller that looks for `Attached=True` on an ancestor matching its *target*
  will never find one, success or failure. It must match on the resolved **Gateway**, and must
  separately treat a `StatusSummary` ancestor as failure.
- `Accepted` comes from `PolicyConditionMap(baseErr, ...)`, which is computed from
  `TranslatePolicyToAgw` **alone** — translation. It is set before any target or gateway is
  resolved, which is why `Accepted=True` coexists with `Attached=False`.

**Three further holes a design must pin:**

1. ⚠️ **An unsupported target kind produces NO ancestors and NO conditions.** `processTarget`
   returns early on `len(policyTargets) == 0` with only a log line, and adds nothing to
   `attachmentErrors`. `MergeAncestors` then prunes the controller's stale ancestors. The policy
   ends up with an **empty `status.ancestors`**. Absence of conditions is a distinct failure state
   — a poller that waits for `Attached=False` to detect failure will hang forever.
2. ⚠️ **Ancestors are capped at 16, and plume's entry can be evicted.** Past 16, `MergeAncestors`
   truncates and inserts a synthetic ancestor with condition type **`StatusSummarized`** and
   message `"%d AncestorRefs ignored due to max status size"`. A busy Gateway can push plume's
   ancestor out of its own policy status.
3. `Accepted=True` also covers **`PartiallyValid`** — translation errors occurred but *some*
   policies were produced. `Accepted=True` alone therefore does not mean the policy is whole.
   Design 03 must require `reason == Valid`, not merely `status == True`.

### 2.2 `AgentgatewayBackend`

- **Status is a flat `conditions[]` (max 8), not ancestors.**
  https://github.com/agentgateway/agentgateway/blob/v1.4.1/controller/api/v1alpha1/agentgateway/agentgateway_backend_types.go#L39
- **Exactly one condition type is ever written: `Accepted`.** No `Attached`. No `Programmed`.
  The printcolumn confirms `Accepted` is the only status surfaced.
- `observedGeneration` is set from `backend.Generation` on both branches.
- **Codex BLOCKER 1's Backend claim is CONFIRMED at v1.4.1.** `TranslateAgwBackend` calls
  `LookupGatewaysForBackend`, and if it returns nothing, `results` stays empty and **no data-plane
  resource is emitted** — yet the function returns `Accepted=True / Reason=Accepted /
  "Backend successfully accepted"` unconditionally.
  https://github.com/agentgateway/agentgateway/blob/v1.4.1/controller/pkg/syncer/backend/backend_plugin.go#L235-L260
  Backend `Accepted=True` proves **translation only**. It is not evidence of publication.
  There is no stronger Backend signal available.

### 2.3 Gateway API `HTTPRoute`

- Standard `status.parents[]`. The agentgateway controller writes exactly two condition types per
  parent: **`Accepted`** and **`ResolvedRefs`**, defaulted to True/`Accepted` and True/`ResolvedRefs`
  when nothing set them.
  https://github.com/agentgateway/agentgateway/blob/v1.4.1/controller/pkg/reports/status.go
- `observedGeneration` is set from `routeReport.observedGeneration` on every condition.
- **No `Programmed` condition on HTTPRoute** — `Programmed` exists only on `Gateway`, `Listener`
  and `ListenerSet`.
- ⚠️ A parentRef the controller does not own gets **no entry at all** (`parentStatusReport == nil
  → continue`). Same trap as §2.1 hole 1.
- Negative reasons observed in the translation path include `InvalidMatch`, `TranslationError`,
  `BackendError`, `IncompatibleFilters`, `RefNotPermitted`, `NoMatchingParent`.

### 2.4 Is there a "programmed"/published signal at all? No.

- `Programmed` is written **only** for `Gateway`/`Listener`/`ListenerSet`, and it means
  **the deployer successfully applied the Kubernetes objects** (Deployment/Service) — not that any
  config reached the proxy. `GatewayReasonDeploymentFailed` is its only False reason in the
  deployment path.
  https://github.com/agentgateway/agentgateway/blob/v1.4.1/controller/pkg/controller/gw_controller.go
- Upstream says so itself, in a comment directly above the condition map:
  `// Programmed: is the data plane "ready" (note: eventually consistent)` — and the real
  per-listener computation is **commented out**: `//setProgrammedCondition(gatewayConditions, ...)`.
  https://github.com/agentgateway/agentgateway/blob/v1.4.1/controller/pkg/agentgateway/translator/gateway_collection.go
- ⚠️ **Decisive: a dataplane NACK never reaches any status condition.** The proxy can reject a
  pushed config; the controller converts that into a **Kubernetes Warning Event on the Gateway
  object**, reason `AgentGatewayNackError`, and nothing else.
  https://github.com/agentgateway/agentgateway/blob/v1.4.1/controller/pkg/syncer/nack/publisher.go
  So a policy can hold `Accepted=True` + `Attached=True` at the current generation while the
  dataplane has rejected it. **Nothing in the status surface can detect this.**

### 2.5 The strongest predicate actually available

For a policy `P` targeting route `R` served by gateway `G`:

- `P.status.ancestors[]` contains an entry with `controllerName == <agw controller>` **and**
  `ancestorRef == {group: gateway.networking.k8s.io, kind: Gateway, name: G.name, namespace: G.ns}`,
  whose conditions include `Accepted=True (reason=Valid)` **and** `Attached=True (reason=Attached)`,
  **both with `observedGeneration == P.metadata.generation`**; and
- no ancestor named `StatusSummary` owned by that controller; and
- `P.status.ancestors` is non-empty (guards §2.1 hole 1); and
- for each Backend `B`: `Accepted=True (reason=Accepted)` at `observedGeneration == B.metadata.generation`; and
- for `R`: a `status.parents[]` entry for `G` with `Accepted=True` **and** `ResolvedRefs=True`, both
  at `observedGeneration == R.metadata.generation`; and
- `G`: `Accepted=True` and `Programmed=True` at `G.metadata.generation`.

**What this proves:** the control plane translated everything, resolved every reference, and
applied the gateway's Kubernetes objects. **What it does not prove:** that the proxy accepted the
config, or that any of it is enforcing on live traffic. Closing that last gap requires either
watching for `AgentGatewayNackError` Events on the Gateway, or an e2e probe that sends a request
that *must* fail. Design 03 should not claim more than the predicate delivers (rule 7).

---

## 3. TOKEN LIMITS — can the crossing request be rejected?

**On the Kubernetes CRD surface: no. Never. `tokenize` is unreachable and hardcoded off.**
Codex BLOCKER 3 is confirmed, and the mechanism is worse than the doc comment suggests.

**The doc comment, verbatim, at v1.4.1** (`LocalRateLimit.Tokens`):

> Number of LLM tokens per unit of time that are allowed. Requests exceeding this limit will fail
> with a `429` error.
>
> Both input and output tokens are counted. However, token counts are not known until the request
> completes. As a result, token-based rate limits will apply to future requests only.

**`tokenize` exists, and does exactly what a reservation needs — but only in standalone mode.**

The dataplane has two entry points that build a `NamedAIProvider`, and only two:

| construction site | mode | `tokenize` |
|---|---|---|
| `crates/agentgateway/src/types/local.rs` | standalone config file | `tokenize: p.tokenize` — user-settable |
| `crates/agentgateway/src/types/agent_xds.rs` | **xDS / Kubernetes control plane** | **`tokenize: false`** — hardcoded literal |

https://github.com/agentgateway/agentgateway/blob/v1.4.1/crates/agentgateway/src/types/agent_xds.rs#L1836

That file's own header states it is the Kubernetes path: *"We aim to generally specialize for the
native Go agentgateway control plane."* Corroborating evidence, all negative:

- `grep -rni tokenize controller/` → **no matches**. The Go CRD API has no such field.
- `grep -rn tokenize crates/protos/proto/` → **no matches**. It is not on the control-plane wire
  protocol, so no controller could set it even if the CRD exposed it.
- `tokenize` appears **0 times** in the published Kubernetes API reference at `2.2.x`, `latest`
  and `main`.

**What `tokenize: true` would buy (standalone only), and its exact limits.** In
`check_llm_request`:

```rust
if let Some(it) = req.input_tokens {
    // If we tokenized the request, check to make sure we permit that many tokens
    // We will add the response tokens in `amend_tokens`
    self.ratelimit.try_wait_n(it)          // -> 429 pre-dispatch if fewer than `it` remain
} else {
    // Otherwise, make sure at least 1 token is allowed.
    // Note this may lead to large over-allowance, especially with fast fill_intervals.
    let avail = self.ratelimit.available_refill();
    if avail > 0 { Ok(..) } else { Err(RateLimitExceeded) }
}
```
https://github.com/agentgateway/agentgateway/blob/v1.4.1/crates/agentgateway/src/http/localratelimit.rs#L88-L110

- **`tokenize: true` bounds INPUT tokens only, pre-dispatch.** `input_tokens` is a tiktoken
  estimate over the normalized messages (`num_tokens_from_messages`), computed in `to_llm_request`
  when and only when `tokenize` is set.
- **Output tokens are never reserved.** They are deducted *after completion* by `amend_tokens`,
  which removes `input_mismatch + output_tokens` and saturates at zero — it cannot drive the
  bucket negative, so overshoot is absorbed and forgotten, not carried forward.
- ⚠️ **A request whose estimated input exceeds bucket capacity is rejected permanently, not
  queued**: `try_wait_n` returns `Err` immediately when `n > self.parameters.capacity`. A single
  prompt larger than the bucket can never succeed, at any time.
- **When the estimate is wrong**, `amend_tokens` reconciles by the signed difference between the
  estimate and the provider's reported count, so the bucket converges — but only after the request
  has already been served.

**The `tokenize: false` path — i.e. every Kubernetes deployment — admits on `avail > 0`.** One
remaining token admits a request that then consumes 50,000. Upstream annotates this itself:
*"Note this may lead to large over-allowance, especially with fast fill_intervals."*

**Verdict for ADR-0014 / architecture.** A pre-dispatch reservation is **not achievable in OSS on
Kubernetes** at v1.4.1 or on `main`. The gateway tier cuts **late**, by one request per replica per
bucket, with an unbounded per-request overshoot. The "cuts early, never late" claim is false and
must be withdrawn or restated as a measured overshoot bound (Codex BLOCKER 3's second option is
the only honest one available). Reaching the first option would require an upstream change to
plumb `tokenize` through `resource.proto` and the CRD — a contribution, not a configuration.

---

## 4. RATE-LIMIT EMITTED SHAPE

**Field set confirmed at v1.4.1**, with one correction to the set given in the task:

```go
// +kubebuilder:validation:ExactlyOneOf=requests;tokens
type LocalRateLimit struct {
    Requests *int32  // +kubebuilder:validation:Minimum=1  (optional)
    Tokens   *int32  // +kubebuilder:validation:Minimum=1  (optional)
    Unit     LocalRateLimitUnit // +required
    Burst    *int32  // (optional)  -- NO Minimum at v1.4.1
}
```
https://github.com/agentgateway/agentgateway/blob/v1.4.1/controller/api/v1alpha1/agentgateway/agentgateway_policy_types.go#L3057-L3084

- `Unit` ∈ `Seconds` | `Minutes` | `Hours`. Required, no default.
- Type-level `ExactlyOneOf=requests;tokens` — confirmed.
- ⚠️ **Correction: `Burst` has NO `Minimum` at v1.4.1.** Verified in the *shipped CRD YAML*, not
  just the marker: at v1.4.1 `burst` is `{format: int32, type: integer}` with no `minimum`; on
  `main` it is `{format: int32, minimum: 0, type: integer}`.
  https://raw.githubusercontent.com/agentgateway/agentgateway/v1.4.1/controller/install/helm/agentgateway-crds/templates/agentgateway.dev_agentgatewaypolicies.yaml
  **A negative `burst` is schema-valid at v1.4.1.** Plume must validate `burst >= 0` itself rather
  than rely on the CRD. (The downstream effect of a negative burst is *not* verified — see §9.)

**The emitted mapping — this settles the burst arithmetic.** In
`processLocalRateLimitTraffic`:

```go
capacity      = *Requests  (type=REQUEST)  or  *Tokens (type=TOKEN)
rule.MaxTokens     = capacity + uint64(burst)      // burst is ADDED ON TOP
rule.TokensPerFill = capacity
rule.FillInterval  = 1s | 1m | 1h                  // exactly one unit
```
https://github.com/agentgateway/agentgateway/blob/v1.4.1/controller/pkg/agentgateway/plugins/traffic_plugin.go#L1696-L1704

**Initial fill and refill, from the dataplane bucket builder:**

```rust
Ratelimiter::builder(value.tokens_per_fill, value.fill_interval)
    .initial_available(value.max_tokens)
    .max_tokens(value.max_tokens)
```
https://github.com/agentgateway/agentgateway/blob/v1.4.1/crates/agentgateway/src/http/localratelimit.rs#L61-L63

Therefore, definitively:

- **`Burst` is an allowance ABOVE the base — confirmed.** Bucket capacity = `base + burst`.
- **The bucket starts FULL at `base + burst`**, because `initial_available == max_tokens`. This
  confirms Codex BLOCKER 4's warning verbatim: emitting `tokens=rate, burst=capacity` yields a
  **starting ceiling of `rate + capacity`**, not `capacity`. Any plume proof that reasons about
  initial capacity must use `base + burst`.
- **Refill adds `base` per interval**, clamped to `base + burst`; surplus is counted as `dropped`.
  First refill occurs one full interval after construction (`refill_at = now + fill_interval`).
- The builder rejects `max_tokens < refill_amount` (`MaxTokensTooLow`). Since
  `MaxTokens = base + burst >= base = TokensPerFill` for non-negative burst, this cannot fire
  through the normal path — which is precisely why the missing `burst >= 0` bound matters.

**Ceilings.**

- **The only ceiling is the CRD's `int32`.** Max expressible: `2,147,483,647` requests-or-tokens
  per unit, plus the same again as burst.
- The wire protocol is **`uint64`** (`MaxTokens`, `TokensPerFill`), and the dataplane bucket is
  `u64`. So the int32 CRD field is the *sole* binding constraint — there is no additional
  per-unit-of-time ceiling. `base + burst` cannot overflow uint64.
- Practically, `Hours` is the coarsest unit, so the maximum expressible sustained rate is
  ~2.1e9 tokens/hour per policy per replica. Design 03's compiler must clamp or compile-error
  above `int32` max rather than cast (Codex BLOCKER 4 stands).

---

## 5. EGRESS RESTRICTION

**Codex BLOCKER 6 is confirmed: there is no generic egress-allowlist field, anywhere.**

`grep -rni 'egress|allowlist' controller/api/` returns exactly **one** hit in the whole CRD API,
and it is unrelated — the MCP JSON-RPC `methods` allowlist (§6). There is no host allowlist, no
domain allowlist, no provider allowlist, and no model allowlist on `AgentgatewayBackend`.

**What the Backend schema actually exposes.** `AgentgatewayBackendSpec` is
`ExactlyOneOf=ai;static;dynamicForwardProxy;mcp;aws;a2a`.
https://github.com/agentgateway/agentgateway/blob/v1.4.1/controller/api/v1alpha1/agentgateway/agentgateway_backend_types.go#L58

For the LLM case, `ai` is an `AIBackend` holding either one `provider` (`LLMProvider`) or
`groups[]` of named providers (max 8 groups × 16 providers). `LLMProvider` is a
one-of over concrete providers — `openai`, `azureopenai`, `azure`, `anthropic`, `gemini`,
`vertexai`, `bedrock`, `custom` — plus explicit overrides:

- `host` (`ShortString`) — *"For custom providers without backendRef, host and port specify the
  target. For managed providers, host and port override the provider default."*
- `port` (int32, 1–65535), `path`, `pathPrefix`.

https://github.com/agentgateway/agentgateway/blob/v1.4.1/controller/api/v1alpha1/agentgateway/agentgateway_backend_types.go#L150-L282

**So the real mechanism for "this agent may only reach these endpoints" is enumeration, not
restriction.** A Backend is a *closed set of destinations by construction*: it can reach the
providers it names and nothing else. There is no field that filters a wider set down.
Consequences for design 03:

- The control is **per-Backend**, not per-policy. It is expressible only as: *emit a Backend whose
  provider list contains exactly the allowed endpoints, and ensure the route can reach no other
  Backend.* The witness is the emitted Backend's provider set plus route reachability — not a
  boolean predicate on a CR.
- ⚠️ **`dynamicForwardProxy` is the anti-control and must be forbidden by plume.** Its own doc
  comment: *"Warning: this backend type can send requests to arbitrary destinations. Proper access
  controls must be put in place when using this backend type."*
- ⚠️ **`host`/`port` override managed-provider defaults.** A Backend naming `openai` is not pinned
  to OpenAI's endpoint; a `host` override silently redirects it. Any plume predicate claiming
  `egressRestricted` must read the *effective* host, not the provider name.
- For a `Custom` provider, translation errors unless `providerBackend` or `hostOverride` is set —
  so custom providers are at least forced to be explicit.
- `AgentgatewayModel` adds a *different* and stricter surface: `baseURL` is validated by CEL to be
  an absolute http/https URL and is **explicitly blocked from localhost, loopback (127/8), and
  link-local (169.254/16)** — four separate CEL rules. `visibility: Internal|Public` also exists,
  where Internal models can only be selected by a virtual model. This is the closest thing to an
  egress control in the API, but it is on the **experimental** Model CRD, not Backend.

**Recommendation:** `llm.egressAllowlist` cannot compile to "a Backend restriction" because no such
restriction exists. It can only compile to *Backend construction* — a resolved, closed provider
list — and the design must define the provider→endpoint resolution catalog itself (agentgateway
supplies none), reject unresolved entries, and forbid `dynamicForwardProxy` and unexpected `host`
overrides. ADR-0014's BAA control needs rewording accordingly.

---

## 6. MCP TOOL FILTER

**Header matching is no longer the mechanism to reach for. There is a first-class tool-filter
policy.**

- **`AgentgatewayPolicy.spec.backend.mcp.authorization`** — a CEL `Authorization` evaluated at the
  MCP layer. Doc comment, verbatim:

  > MCP backend authorization. Unlike authorization at the HTTP level, which rejects unauthorized
  > requests with a `403` error, this policy works at the `MCPBackend` level.
  >
  > List operations, such as `list_tools`, will have each item evaluated. Items that do not meet
  > the rule will be filtered.
  >
  > Get or call operations, such as `call_tool`, will evaluate the specific item and reject
  > requests that do not meet the rule.

  https://github.com/agentgateway/agentgateway/blob/v1.4.1/controller/api/v1alpha1/agentgateway/agentgateway_policy_types.go#L2175-L2186

- **The CEL context exposes the tool name directly as `mcp.tool.name`**, and JWT claims alongside
  it. From the upstream fixture `backendpolicy/mcp-authz-route.yaml`, input → emitted output:

  ```yaml
  backend:
    mcp:
      authorization:
        policy:
          matchExpressions:
            - 'mcp.tool.name == "echo"'
            - 'jwt.sub == "test-user" && mcp.tool.name == "add"'
  # ->
  backend:
    mcpAuthorization:
      allow:
        - mcp.tool.name == "echo"
        - jwt.sub == "test-user" && mcp.tool.name == "add"
  ```

- **Semantics of `Authorization`** (`action` defaults to `Allow`):
  *"If at least one `Allow` rule is configured, requests are denied unless at least one allow rule
  matches."* — i.e. **allow-list, default-deny**, which is what a tool filter needs.
  ⚠️ Upstream explicitly warns against `Deny`: *"`Deny` is not recommended because expression
  failures fail to deny; prefer `Allow` or `Require`."* Design 03 must use `Allow`/`Require`.
  https://github.com/agentgateway/agentgateway/blob/v1.4.1/controller/api/v1alpha1/agentgateway/rbac.go
- **This filters `tools/list` as well as `tools/call`** — so a filtered tool is invisible, not just
  unreachable. Header matching could never do that.
- A second, orthogonal surface exists: **`backend.mcp.guardrails.processors[].methods`**, a
  JSON-RPC **method** allowlist (`tools/call`, `tools/list`, wildcards `tools/*`, `*/list`, `*`;
  most specific wins; unmatched methods **bypass**) routing selected methods to a remote gRPC
  policy server. That is method-level routing to an external decision point, not tool filtering.
- ⚠️ `Mcp-Method` / `Mcp-Name` headers **still exist** in the dataplane — now as **SEP-2243**
  headers under MCP protocol revision 2026-07-28 — and remain matchable from an HTTPRoute. But
  they are the weaker mechanism: header matching cannot filter `tools/list` results.
- ⚠️ CRD-level constraint that shapes emission: `backend.mcp` **may not target a `Service`**, and
  **may not target an `AgentgatewayBackend` `sectionName`**. Also
  `traffic.jwtAuthentication` and `backend.mcp.authentication` **may not appear in the same
  policy** — and `backend.mcp.authentication` is now **deprecated** in favour of
  `traffic.jwtAuthentication.mcp`, *"which ensures authentication runs before other policies such
  as transformation and rate limiting."* That ordering note is directly relevant to plume's
  mandatory-auth-before-toolfilter requirement.

---

## 7. Changes since 2026-08 affecting R1, OTLP, and token exchange

### R1 — one-concern-per-policy, and same-level/same-field overlap

**The design's one-concern-per-policy rule is now demonstrably load-bearing, not merely tidy.**

- **Cross-level precedence is now a documented, first-class field.** `PolicyStrategy.Inheritance`
  ∈ `Default` | `Override`. `Default` = more-specific attachment points may override less-specific
  ones (Gateway < Listener < Route < RouteRule). `Override` = this policy is authoritative and
  blocks more-specific policies from contributing that field. Valid **only on traffic policies**
  (CEL-enforced); frontend and backend merging does not use inheritance.
  https://github.com/agentgateway/agentgateway/blob/v1.4.1/controller/api/v1alpha1/agentgateway/agentgateway_policy_types.go#L146-L182
  This is new relative to the old note and gives plume a real mechanism for "the platform's policy
  wins over a tenant's".
- **Merging is field-level replacement, not deep merge or union.**
  `merge_with_inheritance` is literally `if self.inheritance_locked { return } *self = policy.clone()`.
  For `local_rate_limit`, the field is `RequestPolicy<Vec<RateLimit>>` — so **the whole vector is
  replaced**. Two same-level policies each carrying a rate limit do **not** compose; one silently
  erases the other.
  https://github.com/agentgateway/agentgateway/blob/v1.4.1/crates/agentgateway/src/store/policy.rs
- ⚠️ **R1 answered, and the old note's guess was wrong. Same-level order is not creation order —
  it is hash order.** The merge loop iterates
  `policies_by_target: hashbrown::HashMap<PolicyTarget, HashSet<PolicyKey>>`, where `HashSet` is
  `std::collections::HashSet` with the default `RandomState` hasher — **randomly seeded per
  process**.
  https://github.com/agentgateway/agentgateway/blob/v1.4.1/crates/agentgateway/src/store/binds.rs
  So when two policies at the same level set the same field, the winner is **unspecified, and may
  differ between gateway replicas and across restarts of the same replica** — while both policies
  report `Accepted=True` and `Attached=True`. This is strictly worse than "silent overwrite by
  creation order": it is silent, nondeterministic, and unobservable from status.
  **Design 03's one-concern-per-policy rule avoids a genuinely nondeterministic failure and should
  be documented as doing so, with this as the citation.**

### OTLP tracing

- ⚠️ **There is no `frontendPolicies`.** The correct path is
  **`AgentgatewayPolicy.spec.frontend.tracing`**.
  https://github.com/agentgateway/agentgateway/blob/v1.4.1/controller/api/v1alpha1/agentgateway/agentgateway_policy_types.go#L581
- `Tracing` fields: inlined `PolicyBackendEndpoint` (`backendRef` — *"Supported types: `Service`
  and `AgentgatewayBackend`"* — or `url`), `protocol` (`OTLPProtocol`, **defaults to `GRPC`**),
  `path` (HTTP only, defaults `/v1/traces`), `attributes` (`LogTracingAttributes`), `resources[]`.
  https://github.com/agentgateway/agentgateway/blob/v1.4.1/controller/api/v1alpha1/agentgateway/agentgateway_policy_types.go#L3273
- ⚠️ **A `frontend` policy can only target a `Gateway`** — CEL-enforced, for both `targetRefs` and
  `targetSelectors`. Tracing is therefore a **gateway-scoped** concern in plume's policy model and
  cannot be attached per-route or per-Agent. Several other frontend sub-fields (`tcp`,
  `networkAuthorization`, `tls`, `http`, `proxyProtocol`, `connect`) additionally forbid a
  `sectionName`; `tracing` is **not** in that list, so a listener `sectionName` is permitted for
  tracing specifically.

### OAuth token exchange in OSS

- Present and OSS from **v1.4.0**, at `BackendAuth.oauthTokenExchange` — *"OAuth 2.0 token exchange
  (RFC 8693) / jwt-bearer (RFC 7523) authentication."*
- `BackendAuth` is `AtMostOneOf=key;secretRef;passthrough;aws;azure;gcp;oauthTokenExchange`
  (plus `crossAppAccess`), so token exchange is **mutually exclusive** with the other backend auth
  kinds — plume cannot combine it with a static secret on the same Backend.
- Also new in v1.4.0 and adjacent: **`crossAppAccess`** (Cross App Access / Identity Assertion,
  ID-JAG) — MCP "Enterprise-Managed Authorization". Relevant to design 06 (identity glue), not
  previously noted.

### Budgets — status changed, conclusion unchanged

- OSS **API-key-scoped budgets** ($ or token) landed in commit `67906b4` (= the `v1.5.0-beta.1`
  tag commit, 2026-08-24). The author's own commit message is the primary source:
  > This adds budgets for an API key that can limit the spend ($ or token) for a given key with LLM
  > traffic. **This is exposed in standalone mode only right now as it has a database dependency.**
  > Budgets are stored in memory and flushed to the database every 5s. This implies **with
  > multi-replicas we may exceed the budget with a burst of traffic**, but in exchange we have no
  > database dependency on the request path.

  https://github.com/agentgateway/agentgateway/commit/67906b4bb9dd4359f0ee3ec4cf1fa215953dc683
- No `budget` field exists in the CRD API (`grep -rni budget controller/api/v1alpha1/` returns only
  `PodDisruptionBudget`). **Not reachable from Kubernetes.**
- Enforcement is `used >= limit` checked *before* a request, with `Block` or `Audit` actions — so
  it also **cuts late** by one request, plus a 5s flush window, plus admitted multi-replica
  overshoot. Even when it reaches Kubernetes it will not give plume a hard USD ceiling.
- **Conclusion for plume is unchanged: design 03 must not depend on gateway budgets.** But the
  old note's stated reason ("Enterprise-only") is now wrong, and ADR-0020 should cite the real one.

---

## 8. Consequences for design 03 (proposed, not decided)

1. **Retire the `Accepted=True/programmed` predicate.** Replace with the explicit per-kind tuple in
   §2.5, including `reason == Valid` (not just `status == True`), `observedGeneration` equality,
   the **Gateway**-shaped ancestorRef, the `StatusSummary` negative check, and the empty-ancestors
   check. State plainly that this proves control-plane convergence, **not** enforcement (rule 7).
2. **Add `AgentGatewayNackError` Event watching, or drop any claim of proven enforcement.** This is
   the only observable that closes §2.4, and it is an Event, not a condition.
3. **Withdraw "cuts early, never late."** On Kubernetes, OSS agentgateway cannot reserve tokens
   pre-dispatch. State a measured overshoot bound per request per replica instead.
4. **Fix the emitted rate-limit arithmetic for `initial = base + burst`**, clamp to int32, validate
   `burst >= 0` in plume (the v1.4.1 CRD does not), and reject `gatewayReplicas < 1`.
5. **Redefine `egressAllowlist` as Backend *construction*, not restriction** — with a plume-owned
   provider→endpoint catalog, a ban on `dynamicForwardProxy`, and an effective-host predicate.
6. **Switch the MCP tool filter to `backend.mcp.authorization` with `Allow`/`Require` CEL over
   `mcp.tool.name`**, and move MCP auth to `traffic.jwtAuthentication.mcp` (the `backend.mcp.
   authentication` field is deprecated and orders *after* rate limiting).
7. **Cite the HashSet nondeterminism as the justification for one-concern-per-policy.**
8. **Pin `v1.4.1`, and absorb the v1.4.0 breaking changes** (Gateway API v1.6, `TCPRoute` v1,
   MCP request-phase guardrail rejections now HTTP 200). **Flag that virtual models are
   experimental and off by default** — `docs/architecture.md:272` currently reads as though they
   are a stable mechanism.

---

## 9. Stated gaps — NOT verified, do not encode

- **Evaluation order** ("authn → rate limiting → prompt guards; guard-rejected requests still
  consume quota"). Carried over from the old note's secondary source (a blog). **Not re-verified
  against source in this pass.** The deprecation note on `backend.mcp.authentication` implies
  ordering is real and subtle; treat the specific order as unconfirmed.
- **Negative `burst` behaviour at v1.4.1.** That `burst: -1` is *schema-valid* is verified from the
  shipped CRD YAML. What the controller then emits is **not executed or verified** — by inspection
  `uint64(int32(-1))` would make `MaxTokens < TokensPerFill` and trip the dataplane builder's
  `MaxTokensTooLow`, but I did not run it. Plume should validate `burst >= 0` regardless.
- **Whether a NACK'd policy leaves the *previous* config serving or fails open.** Not traced.
  Material to BLOCKER 2 (tightening updates) — worth a follow-up before that fix is designed.
- **Doc-version label that `latest` resolves to.** `2.3.x` and `1.4.x` both 404; `latest` and
  `main` serve near-identical content. I could not determine a stable numbered alias for current
  docs. Cite `latest`/`main` and pin source by tag instead.
- **`Merged` / `Overridden` policy reasons.** Declared in the API's doc comments but never emitted
  by the v1.4.1 code paths read here. I did not exhaustively search every plugin; treat "never
  emitted" as high-confidence, not proven.
- **Solo Enterprise budget docs.** Not re-checked. The OSS finding above stands on its own.
- **Istio interop** (old note's last bullet). Not re-verified; no bearing on design 03.

---

## Evidence ledger

| Check | Result | Evidence |
|---|---|---|
| agentgateway 2.2 exists | **REFUTED** | No 2.x release in the GitHub release list; latest stable v1.4.1 |
| Old note sourced from current docs | **REFUTED** | `2.2.x` API reference contains 0 mentions of `AgentgatewayModel`/`virtualModel`/`oauthTokenExchange`; `latest` contains 40/12/29 |
| Token exchange + virtual models floor | **v1.4.0** | Raw CRD types fetched per tag; absent at v1.3.1, present at v1.4.0 |
| Virtual models are stable | **REFUTED** | v1.4.0 release notes: "(off by default) experimental" |
| Policy publishes `Accepted` + `Attached`, ancestor-scoped, with `observedGeneration` | **CONFIRMED** | `policy_types.go` L20–L32; `status.go` `setConditions` |
| `Attached=False` is reported on a synthetic `StatusSummary` ancestor, not the target | **CONFIRMED** | `traffic_plugin.go` L299; fixture `http-route-missing-not-attached.yaml` |
| Success ancestorRef is the **Gateway**, not the targetRef | **CONFIRMED** | `resolvePolicyAncestorRefs`; fixture `trafficpolicy/cors.yaml` |
| Unsupported target kind yields zero ancestors/conditions | **CONFIRMED** | `processTarget` early return, no `attachmentErrors` append |
| Backend `Accepted=True` without emitting data-plane resource | **CONFIRMED at v1.4.1** | `backend_plugin.go` L235–L260 — unconditional after translation |
| Backend publishes anything beyond `Accepted` | **REFUTED** | `AgentgatewayBackendStatus` is flat `conditions[]`; only `Accepted` written |
| HTTPRoute publishes `Accepted` + `ResolvedRefs`, no `Programmed` | **CONFIRMED** | `reports/status.go`, `addMissingParentRefConditions` |
| A "published/programmed" signal exists | **REFUTED** | `Programmed` only on Gateway/Listener/ListenerSet, = deployer applied objects; per-listener computation commented out |
| Dataplane NACK reaches status | **REFUTED** | `nack/publisher.go` emits a Kubernetes Warning Event only |
| `tokenize` reachable from the Kubernetes CRD | **REFUTED** | 0 hits in `controller/`; 0 in `crates/protos/proto/`; 0 in the published API reference; `agent_xds.rs` L1836 hardcodes `tokenize: false` |
| `tokenize:true` rejects the crossing request pre-dispatch | **CONFIRMED (standalone only)** | `try_wait_n(input_tokens)` in `check_llm_request` |
| Pre-dispatch reservation bounds OUTPUT tokens | **REFUTED** | Output deducted post-hoc by `amend_tokens`, saturating at zero |
| `Burst` is added above the base | **CONFIRMED** | `traffic_plugin.go` L1696: `MaxTokens = capacity + burst` |
| Bucket starts full at `base + burst` | **CONFIRMED** | `localratelimit.rs` L61–63: `initial_available(max_tokens)` |
| `Burst` has `Minimum=0` at v1.4.1 | **REFUTED** | Shipped v1.4.1 CRD YAML has no `minimum` on `burst`; `main` does |
| Per-unit ceiling beyond int32 | **REFUTED** | Wire + bucket are `uint64`; CRD `int32` is the sole bound |
| `AgentgatewayBackend` has a generic egress allowlist | **REFUTED** | Zero `egress`/`allowlist` hits in `controller/api/`; only enumerated providers + host/port overrides |
| First-class MCP tool filter exists | **CONFIRMED** | `backend.mcp.authorization`, CEL over `mcp.tool.name`; fixture `mcp-authz-route.yaml` |
| Same-level overlap resolves by creation order | **REFUTED** | Iteration is over `std::HashSet<PolicyKey>` (RandomState) — unspecified, per-process random |
| OTLP tracing lives at `frontendPolicies` | **REFUTED** | Field is `spec.frontend.tracing`; frontend policies may only target a Gateway |
| OSS budgets are Enterprise-only | **REFUTED, conclusion unchanged** | `67906b4`: OSS but "standalone mode only", unreleased, no CRD field |

**Re-verify by 2026-10-15**, or immediately if agentgateway cuts v1.5.0 final — `main` already
carries CRD changes (`burst` minimum, and more) not in v1.4.1.
