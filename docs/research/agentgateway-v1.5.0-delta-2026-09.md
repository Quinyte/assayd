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
