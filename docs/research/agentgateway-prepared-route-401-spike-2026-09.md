# agentgateway 1.5.0: does a prepared route answer 401 before its missing-backend 500? (2026-09-11)

- **Question** (design 03 sixth critique, MAJOR 3): the first slice's `Create` prepares the per-Agent serving route with `parentRefs` and **no `backendRefs`**, attaches `<agent>-auth`, and publishes only after an anonymous request gets `401`. That is sound only if an unauthenticated request is refused with `401` *before* the proxy reaches the missing backend and answers `500`. Nothing had measured it: spike §2.6 measured the `500` with no auth policy; the e2e measured `401` only on a route with backends.
- **Answer: yes.** With the `-auth` policy attached, an anonymous request and a request with an unknown key both get `401`; a valid key reaches the missing backend and gets `500`. Before the policy attaches, every request gets `500`. So an anonymous `401` proves authentication is enforcing on that route before any backend exists.
- **Measured against:** agentgateway and agentgateway-crds **1.5.0** on k3d `assayd-local`, via the Gateway `assayd` `tools` listener.
- **Re-verify:** at every agentgateway upgrade.

## What was run

In the test's tools namespace; every object was deleted afterwards and none were left behind.

1. A ConfigMap labelled `assayd.dev/api-keys: prep-spike` holding one `sha256:` key hash with metadata group `trusted`.
2. A prepared `HTTPRoute`: `parentRefs` to the `tools` listener, one hostname, one rule with a `PathPrefix /` match and **no `backendRefs`**, authored as the operator's ServiceAccount (the `assayd-gateway-routes` admission policy reserves every route naming this Gateway to that identity).
3. An `AgentgatewayPolicy` targeting that route: `traffic.apiKeyAuthentication` (`mode: Strict`, `configMapSelector` on the label above) and `authorization` (`action: Allow`, `apiKey.group == "trusted"`) — the shape of design 03 §3.4.4's worked policy.
4. Probes from a Pod with the route's `Host` header: no credential, an unknown key, and the valid key.

| Request | Before the policy | 10 s after the policy (`Accepted=True Attached=True`) |
|---|---|---|
| no credential | 500 | **401** |
| unknown key | 500 | **401** |
| valid key | 500 | 500 (authorised, then no backend) |

## Consequences for design 03

- **The `Create` probe is sound.** An anonymous `401` on a prepared route proves the policy is enforcing, and the before/after change (`500` → `401`) is observable, so the probe cannot pass early.
- **A deadline input.** Enforcement took under 10 s from applying the policy, on a single-replica gateway — one measurement, well inside the 2 minutes `test/e2e/authz_test.go` allows. Keep the deadline well above it; this is evidence, not a bound.
- **The probe proves authentication, not the group rule.** A valid key still gets `500` on a prepared route, so the authorisation decision is invisible until the route has a backend; the slice's e2e must pin the `403` for a wrong-group key after publishing.
