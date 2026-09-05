# agentgateway v1.5.0 cluster spike — the two findings the API diff could not reach

**Status**: 2026-09-05. Cluster phase complete for the questions ADR-0030 blocked design 03 on. One question from the v1.4.1 spike is now **unanswerable by its original method** and is recorded as such rather than assumed.

Why this exists: `agentgateway-v1.5.0-delta-2026-09.md` diffed the two API surfaces at their tags and said plainly what it could not settle — §2.1 (the apply barrier) and §2.8 (a NACK'd tightening reporting converged) are behavioural, and an API diff cannot reach them. ADR-0030 blocks reopening design 03 until they are measured. This measures them.

**Cluster**: k3d `plume-spike150`, Kubernetes v1.33.6, Gateway API v1.6.0 standard, `agentgateway-crds` and `agentgateway` Helm charts at **1.5.0**, controller image `cr.agentgateway.dev/controller:v1.5.0` read back from the running Deployment. Same shape as the v1.4.1 spike so the two are comparable. Every result below is an observation.

## Results against the v1.4.1 spike

| v1.4.1 finding | v1.5.0 | How it was measured |
|---|---|---|
| **§2.1** apply barrier passes on a policy that emitted nothing | **REPRODUCED, unchanged** | A policy targeting `HTTPRoute default/does-not-exist` reports `Accepted=True(Valid)` / `Attached=False(Pending)`, under the same synthetic `ancestorRef {group: agentgateway.dev, kind: Gateway, name: StatusSummary}`. Byte-identical to the v1.4.1 observation |
| **§2.3** a negative `burst` is accepted by API and controller | **FIXED** | The API server refuses: `spec.traffic.rateLimit.local[0].burst: Invalid value: -1: … should be greater than or equal to 0` |
| **§2.8** a NACK'd tightening reports fully converged | **CHANGED — see below.** The original lever is gone and a different NACK path now carries a distinguishing signal | The `burst: -1` poison is refused at admission, so §2.8's exact experiment cannot be run at this version |
| `maxConcurrentRequests` (new at v1.5.0) | **Per-Gateway only. Confirms it restores no per-Agent ceiling** | Targeting an `HTTPRoute` is refused by the API server: `spec: Invalid value: "object": the 'frontend' field can only target a Gateway` |

## §2.8 in full, because the change is partial and the remedy is not the obvious one

The v1.4.1 finding was that a NACK'd update reports `Accepted=True(Valid)` + `Attached=True(Attached)` at the current `observedGeneration` while the previous configuration keeps serving — every element of design 03 §3.3.2's convergence tuple satisfied by a config that never took effect.

**Two things had to be re-established separately: can the dataplane still reject a config the control plane accepted, and if so does status still lie?**

The old lever is closed, so a new one was needed. Two candidates did **not** reject: `burst: 0` with `requests: 1` applied cleanly and enforced the tightening (traffic went `200` then `429 429 429…`), and `tokens` + `burst` together — which §2.4 flagged as silently inert — produced no NACK either. A syntactically invalid CEL expression in `traffic.transformation.conditional[].condition` does, because the CRD cannot validate CEL and the dataplane must compile it.

**What v1.5.0 reports for that NACK:**

```
Accepted=True   reason=PartiallyValid
  message=condition CEL expression is invalid: !!!broken(((
Attached=True   reason=Attached
  message=Attached to all targets
```

against a healthy `Accepted=True(Valid)`. Removing the bad expression returns it to `Valid`, so the signal clears rather than sticking.

**What this changes, and what it does not.**

- A NACK is now **distinguishable from a healthy apply** — by the `reason` and by a `message` naming the fault. At v1.4.1 it was not. This is a real improvement and design 03 should use it.
- **`status` is still `True`.** A barrier that waits for `Accepted=True` is still satisfied by a NACK'd policy, exactly as §2.1 shows it is satisfied by a policy that emitted nothing. **The remedy is to check the reason, not the status** — which is a different fix from the one design 03's §3.3.2 assumed it needed.
- The dataplane still emits `type=Nack` and an `AgentGatewayNackError` Event, as at v1.4.1.
- For **this** path the failure is fail-closed, not silent retention: the log says it is *"replacing … with an expression that always fails"*. That is not the same as §2.8's "retains the previous configuration".

## What is still unknown, stated rather than assumed

- **Whether a *rate-limit* NACK also reports `PartiallyValid`.** The only known rate-limit poison was `burst: -1` and the API server now refuses it. One NACK path carrying a distinguishing reason does not establish that every path does, and design 03's machinery is built around the rate-limit path specifically. **Do not generalise `PartiallyValid` to rate limits without finding a lever and measuring it.**
- **Whether any v1.5.0 path still silently retains previous configuration.** The CEL path degrades to always-fail instead. Retention and always-fail are different failure modes with different consequences for A49's `Withdraw`, and only the second was observed.
- **§2.7's concurrency overrun was not re-run.** It needs an LLM backend to count tokens, and `maxConcurrentRequests` — the reason to revisit it — is per-Gateway, so it cannot bound a per-Agent budget regardless of the number. The measurement above settles the design question without re-running the load test.

## What design 03 owes when it reopens

1. **§3.3.2's convergence tuple must include the `reason`.** `Accepted=True` alone is satisfied by both a NACK'd policy and a policy that emitted nothing. `Accepted=True AND reason==Valid` separates the first; §2.1's `Attached` check separates the second. Both are needed and neither is sufficient alone.
2. **Keep §2.1's barrier finding as it stands** — it reproduced unchanged, so nothing there is stale.
3. **Delete any negative-`burst` handling.** The API refuses it now.
4. **Do not weaken A49's best-effort `Withdraw` on the strength of `PartiallyValid`.** It was measured on the CEL path only, and the rate-limit path — the one `Withdraw` acts on — has no available lever to test.
5. **`maxConcurrentRequests` is a listener-wide load control, not a governance one.** Measured, not read: the API server refuses it on a route. §3.5's "no figure is published" stands as a per-Agent statement.
