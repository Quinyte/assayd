# agentgateway v1.5.0 — the concurrency experiment, re-run

**Status**: 2026-09-08. **This discharges ADR-0030's dependency prerequisite for design 03.** The v1.5.0 delta note and the cluster spike both said this experiment had not been run and that the schema result did not stand in for it; it has now been run.

**Cluster**: k3d `plume-conc`, Kubernetes v1.33.6, Gateway API v1.6.0 standard, agentgateway charts at **1.5.0**, controller image `cr.agentgateway.dev/controller:v1.5.0` read back from the running Deployment. Same experiment shape as `agentgateway-v1.4.1-spike.md` §2.7 so the two are comparable: a mock LLM returning `usage.total_tokens: 1000` behind an `AgentgatewayBackend` with a `custom` provider, under `rateLimit.local: [{tokens: 1000, unit: Hours}]`. Each request therefore consumes the entire hourly budget. The mock sleeps 2s so requests overlap. One gateway replica throughout.

**A mock that could not do the job produced a false result first, and it is recorded because it nearly became the finding.** The first mock used Python's `HTTPServer`, which is single-threaded: 100 requests each sleeping 2s became a 200-second queue, and the run returned 48 × 503 and 37 connection failures. Those looked like the gateway shedding load. They were the mock saturating. `ThreadingHTTPServer` fixed it, and the control that should have come first — 20 requests straight at the mock, bypassing the gateway, completing in 3s — now does.

## 1. §2.7 reproduces exactly. Nothing about it changed at v1.5.0

| Test | v1.4.1 | v1.5.0 |
|---|---|---|
| Serial, budget already spent | `200`, `429`, `429` | **`200`, `429`, `429`** |
| 20 concurrent | 20 × `200` | **20 × `200`** |
| 100 concurrent | 100 × `200` | **100 × `200`** |

100 concurrent requests admitted **100,000 tokens against a 1,000-token budget**. The limiter works serially and does nothing under concurrency, at both releases. Design 03 §3.5's refusal to publish a figure was correct and remains correct on its original grounds.

## 2. `maxConcurrentRequests` bounds it, and this is the first bound this project can publish

`frontend.http.maxConcurrentRequests: 5` on the Gateway, fresh budget, 100 concurrent:

**5 × `200`, 95 × `503`.**

Exactly the cap. So the worst-case overshoot becomes:

```
maxConcurrentRequests × maxTokensPerRequest
```

— a number an operator chooses, rather than one an attacker chooses. That is a real improvement over "unbounded", and it is the only thing v1.5.0 changes here.

## 3. But the bound is SHARED, so it is not a per-Agent ceiling

Two routes on one Gateway, agent A driving 60 concurrent and agent B asking for 20 at the same time:

| | with `maxConcurrentRequests: 5` | without it (control) |
|---|---|---|
| A | 37 × `429`, 23 × `503` | 60 × `429` |
| **B** | **5 × `200`, 15 × `503`** | **20 × `200`** |

B is a different agent, on a different route, with its own budget, doing nothing wrong. Under the cap it is refused 15 times **because A was busy**. Without the cap it is served every time. The concurrency allowance is gateway-wide and first-come-first-served: **one noisy agent starves every other agent behind the same Gateway.**

## 4. The five things that had to be separated, and where each landed

| | Result |
|---|---|
| **Configurable scope** | Gateway only. The API server refuses the field on an `HTTPRoute`: `the 'frontend' field can only target a Gateway` |
| **Actual counter scope** | Gateway-wide and shared across routes — measured in §3, not inferred from §1 |
| **Concurrency enforcement** | Real and exact. A cap of 5 admitted exactly 5 |
| **Accounting lag** | Unchanged and still fatal to a ceiling: the `Tokens` doc comment says token counts are not known until a request completes, so every admitted request completes and is charged afterwards |
| **Budget guarantee** | **None.** A bound on simultaneous excess is not a bound on spend |

**`503` is not `429`, and design 03 must not conflate them.** The `503` is frontend load shedding, returned before any policy runs; the `429` is the rate limit acting. In §3's table A shows both, and they mean different things to an operator: one says "the gateway is full", the other says "this agent is out of budget".

## What design 03 may now say, and what it still may not

- **May**: worst-case token overshoot is `maxConcurrentRequests × maxTokensPerRequest` per Gateway, when the field is set. Recommending it as overload protection is supported.
- **May not**: call it a per-Agent budget ceiling, or emit it per Agent — it cannot be emitted per Agent, and if two agents share a Gateway it is not theirs. §3.5's "no figure is published" stands **as a per-Agent statement**, now on measured grounds rather than on the absence of the field.
- **Must**: state the starvation. A cap set low enough to bound one agent's overshoot usefully is low enough for that agent to deny service to its neighbours, and nothing in the gateway apportions it. Whether plume sets this field at all is a design decision with a real cost on both sides, and this note does not take it.
