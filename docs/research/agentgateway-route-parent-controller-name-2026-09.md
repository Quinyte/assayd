# The `controllerName` on a route's `status.parents` entry, agentgateway 1.5.0

**Measured 2026-09-22**, on the k3d e2e cluster `hack/e2e.sh` builds, mid-run,
for design 03 A81 (the review's MINOR 4). Every other measurement this design
cites has a file; this one did not until now.

## Why it was measured

`routeReport` (`internal/controller/authserved.go`) matches the assayd
Gateway's entry in a route's `status.parents` on `parentRef` **and**
`controllerName`. Gateway API keys a `RouteParentStatus` by both, so a second
controller may write its own entry for the same `parentRef`, and in A80's
judgement that entry is a **clearing** signal: another controller reporting
`Accepted=True` at the route's current generation would retract a true
`routeRefused` claim and put the Agent back to `Ready=True` on a route the
assayd Gateway is still refusing.

The name compared is `AgentgatewayControllerName`
(`internal/controller/authnack.go`), which the NACK check already treats as the
authority for "agentgateway's controller" — for an **Event source**, which is
not the same field. Inheriting it for this one without looking would have been
an assumption in a place where being wrong is silent.

## What was run

Against the running e2e cluster, with a served Agent's route published:

```
$ kubectl --context k3d-assayd-local get httproute -A \
    -o jsonpath='{range .items[*]}{.metadata.name}{" parents="}{.status.parents[*].controllerName}{"\n"}{end}'
principal-serving parents=agentgateway.dev/agentgateway

$ kubectl --context k3d-assayd-local get gatewayclass agentgateway \
    -o jsonpath='{.spec.controllerName}'
agentgateway.dev/agentgateway
```

## What it shows, and what it does not

**It shows** that on a default agentgateway 1.5.0 install — the one
`hack/e2e.sh` performs and the one the chart's docs assume — the GatewayClass's
`spec.controllerName` and the `controllerName` agentgateway writes onto a
route's `status.parents` entry are the same string, and that string is
`agentgateway.dev/agentgateway`, which is `AgentgatewayControllerName`.

**It does not show** that the string is fixed. agentgateway 1.5.0's chart
exposes `controllerName` as a value, documented for running several controllers
in one cluster. On an install that sets it, this loop matches nothing, every
route reads `reportUnknown`, and **A80's route half raises nothing at all** —
the defect A80 exists to report, standing and unreported. assayd exposes no
knob for it: `GatewayConfig` has no such field and the chart's `gateway:` map
none. That is stated in `03-policy-compiler.md` §3.3.3 and in the code, and the
fix that would not trade raising for clearing — telling "a parent entry exists
for our `parentRef` but none from our controller" apart from "no entry at all",
which does not flap on promotions — is recorded there as **owed**, because it
adds vocabulary to an approved slice.

Measured for the owed side too: one edit to the constant and
`TestAServedRouteTheGatewayRefusesIsDegraded` fails with `Ready=True` on a
refused route.
