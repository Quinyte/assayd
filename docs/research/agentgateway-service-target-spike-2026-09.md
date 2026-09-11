# agentgateway 1.5.0: can a traffic-auth policy target a revision's Service? (2026-09-11)

- **Question** (design 03, re-critique B1): the operator emits one serving `HTTPRoute` per Agent (design 07 A6.10), and a rollout weights one rule between the revisions' Services. Can each revision get its own `-auth` `AgentgatewayPolicy` by targeting its own **Service**, so that two revisions' auth never share a target?
- **Answer: no. The CRD refuses it.** A policy carrying `traffic` (where `apiKeyAuthentication` and `authorization` live) may target only a Gateway, ListenerSet, GRPCRoute, HTTPRoute or InferencePool.
- **Measured against:** agentgateway and agentgateway-crds **1.5.0** (Helm charts `agentgateway-1.5.0` and `agentgateway-crds-1.5.0`) on k3d `assayd-local`.
- **Re-verify:** at every agentgateway upgrade, because this is a CEL rule in the vendored CRD and can change.

## What was run

Two server-side dry-runs, so nothing was created, of the same policy: the API-key authentication and CEL authorization shape `test/e2e/authz_test.go` measures end to end. The only difference between them is the target kind.

```yaml
apiVersion: agentgateway.dev/v1alpha1
kind: AgentgatewayPolicy
spec:
  targetRefs: [{group: "", kind: Service, name: spike-target}]   # control: HTTPRoute
  traffic:
    apiKeyAuthentication: {configMapSelector: {matchLabels: {assayd.dev/api-keys: spike}}}
    authorization: {action: Allow, policy: {matchExpressions: ['apiKey.group == "a"']}}
```

| Target kind | Result |
|---|---|
| `Service` | **refused**: `spec: Invalid value: "object": the 'traffic' field can only target a Gateway, ListenerSet, GRPCRoute, HTTPRoute, or InferencePool` |
| `HTTPRoute` (control) | `created (server dry run)` |

The control is what makes the refusal evidence about the **target kind**. The same spec was admitted on a route, so the refusal came from the rule that names target kinds, and from nothing else in the policy. The installed CRD carries the rule verbatim:

```
has(self.traffic) && has(self.targetRefs)
  ? self.targetRefs.all(t, t.kind in ['Gateway','HTTPRoute','GRPCRoute','ListenerSet','InferencePool'])
  : true
```

A second copy covers `targetSelectors`. The CRD also forbids `backend.mcp` and `backend.ai` on a Service target, and it requires a Service `sectionName` to be a numeric port.

## The other per-revision shapes, and why each fails for the slice

| Shape | Why it fails |
|---|---|
| `traffic` policy on the revision's **Service** | the CRD refuses it (above) |
| `traffic` policy on a **route rule** (`sectionName`) | a rollout weights backends *within one rule*, so a rule-level policy covers every revision that rule splits between. This follows from the route's structure and was not separately measured |
| A **per-revision route** on a distinct match | two routes on one hostname are merged by the listener. Design 07 A6.10 refused that window when it made the serving route per-Agent |
| `backend.extAuth` on the revision's Service (allowed: the CRD forbids only `backend.mcp` and `backend.ai` on Services) | it calls an external authorization service, which is a different mechanism from the slice's API keys and needs a server nothing here provides. It is a possible shape for design 06's era, and **not measured** |

## Consequence for design 03

The slice's `-auth` is **one policy per Agent**, targeting the per-Agent serving `HTTPRoute`. It is the fallback the human chose for this outcome (2026-09-11). Design 03 A53 §3.2 records the rule that keeps it rollout-safe.
