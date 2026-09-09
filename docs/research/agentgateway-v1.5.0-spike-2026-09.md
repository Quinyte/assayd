# agentgateway v1.5.0 cluster spike — one of the two findings the API diff could not reach

**Status**: 2026-09-05; **substantially corrected 2026-09-06** after `reviews/03-v150-spike-astra-review.md`. **ADR-0030's prerequisite is NOT discharged by this note** — that ADR ties revisiting v1.5.0 to the *concurrency* result, and that experiment was not rerun. Read "What is still unknown" before using anything here.

**Four conclusions in the first version of this note were wrong or overreached**, and the corrections are in place below rather than appended. The largest: it claimed v1.5.0 changed how a NACK is reported. It does not. That claim compared a **rate-limit** stimulus at v1.4.1 against a **CEL** stimulus at v1.5.0 — not a controlled comparison — and the CEL path was never run on v1.4.1 at all.

Why this exists: `agentgateway-v1.5.0-delta-2026-09.md` diffed the two API surfaces at their tags and said plainly what it could not settle — §2.1 (the apply barrier) and §2.8 (a NACK'd tightening reporting converged) are behavioural, and an API diff cannot reach them. ADR-0030 blocks reopening design 03 until they are measured. **This measures §2.1 and not §2.8**, for the reason the §2.8 section gives.

**Cluster**: k3d `assayd-spike150`, Kubernetes v1.33.6, Gateway API v1.6.0 standard, `agentgateway-crds` and `agentgateway` Helm charts at **1.5.0**, controller image `cr.agentgateway.dev/controller:v1.5.0` read back from the running Deployment. Same shape as the v1.4.1 spike — but note that comparability requires the same *stimulus* too, which is where this note's first version went wrong. Every result below is an observation.

## Results against the v1.4.1 spike

| v1.4.1 finding | v1.5.0 | How it was measured |
|---|---|---|
| **§2.1** apply barrier passes on a policy that emitted nothing | **REPRODUCED, unchanged** | A policy targeting `HTTPRoute default/does-not-exist` reports `Accepted=True(Valid)` / `Attached=False(Pending)`, under the same synthetic `ancestorRef {group: agentgateway.dev, kind: Gateway, name: StatusSummary}`. Byte-identical to the v1.4.1 observation |
| **§2.3** a negative `burst` is accepted by API and controller | **FIXED** | The API server refuses: `spec.traffic.rateLimit.local[0].burst: Invalid value: -1: … should be greater than or equal to 0` |
| **§2.8** a NACK'd tightening reports fully converged | **NOT RETESTED, and not shown to have changed.** The original lever is refused at admission, so §2.8's experiment cannot be run at this version. A *different* stimulus was run and its result does not substitute — see below | The `burst: -1` poison is refused at admission |
| `maxConcurrentRequests` (new at v1.5.0) | **Configurable only on a Gateway.** That is a statement about *where it may be set*, and nothing more | Targeting an `HTTPRoute` is refused by the API server: `spec: Invalid value: "object": the 'frontend' field can only target a Gateway` |

## §2.8: what was actually observed, and why it does not answer the question

The v1.4.1 finding was that a NACK'd update reports `Accepted=True(Valid)` + `Attached=True(Attached)` at the current `observedGeneration` while the previous configuration keeps serving.

The old lever is closed, so a new stimulus was tried. Two candidates did **not** reject: `burst: 0` with `requests: 1` applied cleanly and enforced the tightening (traffic went `200` then `429 429 429…`), and `tokens` + `burst` together produced no NACK. A syntactically invalid CEL expression in `traffic.transformation.conditional[].condition` produced this:

```
Accepted=True   reason=PartiallyValid
  message=condition CEL expression is invalid: !!!broken(((
Attached=True   reason=Attached
```

**Three corrections to what the first version of this note concluded from that.**

1. **This is not new at v1.5.0, and this note should never have said it was.** Checked at both tags: `controller/pkg/agentgateway/plugins/traffic_plugin.go` contains the identical string `"condition CEL expression is invalid: %s"` and sets `PolicyReasonPartiallyValid` with `ConditionTrue` at **v1.4.1** (≈`:325`, `:1238`) as well as v1.5.0 (≈`:322`, `:1247`). The CEL stimulus was never run on v1.4.1. Comparing a rate-limit error on one release with a CEL error on another establishes nothing about a release change.

2. **`PartiallyValid` is controller-side *translation* validation, not a dataplane acknowledgement.** The controller produces it while translating the policy; it is emitted whether or not any proxy accepted anything. A dataplane-only rejection need not produce it. Observing `PartiallyValid` and a NACK in the same window does not establish that the first reports the second. So `reason == Valid` must **not** be read as evidence that a proxy took the configuration — which is precisely the inference the first version of this note prescribed.

3. **"Fail-closed" was asserted and is unverified.** It rested on a log line about *"replacing … with an expression that always fails"*. That describes an expression-evaluation failure, not an HTTP denial: agentgateway's conditional-policy selection skips a policy whose condition does not evaluate true, so the observable effect may be the conditional transformation being **skipped** rather than traffic being refused. Requests during that window returned `200`, and whether the transformation was applied was never checked. Nothing here supports a fail-closed guarantee.

**Three layers this note originally collapsed into one**, and any future experiment must keep apart: **admission** (the API server), **controller translation** (what produces `Valid` / `PartiallyValid`), and **dataplane acceptance** (the xDS ACK/NACK). The question §2.8 asks is about the third, and only the second was measured.

## What is still unknown, stated rather than assumed

- **Whether a *rate-limit* NACK also reports `PartiallyValid`.** The only known rate-limit poison was `burst: -1` and the API server now refuses it. One NACK path carrying a distinguishing reason does not establish that every path does, and design 03's machinery is built around the rate-limit path specifically. **Do not generalise `PartiallyValid` to rate limits without finding a lever and measuring it.**
- **Whether any v1.5.0 path still silently retains previous configuration.** Unobserved. The CEL path is not evidence either way: it is caught in controller translation, and what happens to the *request* was never checked.
- **§2.7's concurrency overrun was not re-run, and the schema result does not stand in for it.** "Configurable only on a Gateway" says where the field may be set. It does **not** establish that a Gateway-wide cap fails to constrain one Agent's concurrent traffic — a shared cap does constrain any single tenant, just without independent quotas or fairness — and it establishes nothing at all about a token or monetary bound. Five things must be separated and none were measured: configurable scope, actual counter scope, concurrency enforcement, accounting lag, and budget guarantee. **Keeping design 03 §3.5's no-guaranteed-ceiling position is right; claiming this experiment proved it is not.**
- **Whether a dataplane-only rejection is visible in status at all.** That needs a stimulus the controller translates cleanly and the dataplane refuses. The CEL stimulus is the opposite: the controller catches it.

## What design 03 owes when it reopens

1. **Require `reason == Valid`, and keep the limit attached to it.** `Accepted=True` alone is satisfied by a policy that emitted nothing (§2.1), so requiring the reason is a strict improvement. But it proves only that **controller translation** was fully valid — it is **not** positive dataplane acknowledgement, and §3.3.2 must say so where it states the rule, or this note will have replaced one false convergence signal with another.
2. **Keep §2.1's barrier finding as it stands** — it reproduced unchanged, so nothing there is stale.
3. **Delete any negative-`burst` handling.** The API refuses it now.
4. **Do not weaken A49's best-effort `Withdraw`.** `PartiallyValid` was observed on the CEL path only, it is not new to v1.5.0, and it reports translation rather than dataplane state. The rate-limit path `Withdraw` acts on has no available lever to test.
5. **`maxConcurrentRequests` is configurable only on a Gateway.** Do not discard it as useless overload protection and do not credit it with a budget bound; both would go beyond what was measured. §3.5's "no figure is published" stands on the original v1.4.1 measurement, not on this one.
