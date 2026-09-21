# A80's incident, walked through on k3d: renaming the Gateway's listener

**Measured 2026-09-22**, on the k3d cluster `hack/e2e.sh` builds, against the
tree this walkthrough is committed with, with agentgateway 1.5.0 and the
operator built from that tree.

## Why it was run

`charts/assayd/values.yaml` and `docs/install.md` tell an administrator what a
served Agent reads when the Gateway's listener is renamed, and both end
"Measured on agentgateway 1.5.0". Until this run, **the only cluster
measurement of that scenario produced the OLD reason** — it was taken before
A81 reversed `incompleteOrder`, so the two administrator-facing files were
describing an outcome nobody had seen on a cluster. §8.1 case 19 (q) asserts
the new outcome, but envtest writes the Gateway's status itself; it cannot show
that agentgateway produces the state that row describes.

## What was run

A served API-key Agent (no `expose` block) in its own run namespace, taken to
`Ready`. Then `spec.listeners[0].name` on the assayd Gateway patched `http` →
`http-renamed`, read back after 45 s; then patched back, read back after 60 s.

## 1. Before — served, on an accepted route

```
phase: Ready
status.auth.mode: apikey   routeRefused: <unset>   policyUnattached: <unset>
Ready                  True   Available
GovernanceSkipped      False  AuthVerifiedOnOneReplica
route walk-serving generation 2, controllerName agentgateway.dev/agentgateway
  Accepted      True  Accepted      gen=2
  ResolvedRefs  True  ResolvedRefs  gen=2
policy walk-auth generation 1
  ancestor: gateway.networking.k8s.io Gateway assayd
```

## 2. After the rename — BOTH halves fire, and the route leads

```
phase: Degraded
status.auth.mode: apikey   routeRefused: true   policyUnattached: true
Ready                  False  ServingRouteNotAccepted
Degraded               True   ServingRouteNotAccepted
PolicyApplyIncomplete  True   ServingRouteNotAccepted
    the assayd Gateway is not accepting this Agent's serving route, so nothing
    reaches this Agent through the gateway: route …/walk-serving at generation
    2: Gateway assayd-gateway/assayd reports Accepted=False, reason
    NoMatchingParent: sectionName "http" not found. …
GovernanceSkipped      True   AuthPolicyNotAttached
    the assayd Gateway reports that it does not attach this Agent's
    <agent>-auth policy. Whether the route is answering with no credential
    required depends on the route, and THIS PASS DID NOT READ IT AS ACCEPTED: …
route walk-serving generation 2, controllerName agentgateway.dev/agentgateway
  Accepted      False  NoMatchingParent  gen=2
  ResolvedRefs  True   ResolvedRefs      gen=2
policy walk-auth generation 1
  ancestor: agentgateway.dev Gateway StatusSummary
```

Four things this shows that only a cluster can.

- **Both halves fire on one pass.** `routeRefused: true` and
  `policyUnattached: true` together. A80 assumed they could not, and ordered
  the reasons on that assumption; the ancestor on `walk-auth` flips from the
  assayd Gateway to agentgateway's synthetic `StatusSummary` in the same
  breath as the route's `Accepted=False`, which is the mechanism.
- **The route reason leads.** `Ready`, `Degraded` and `PolicyApplyIncomplete`
  all read `ServingRouteNotAccepted`. Under A80's order all three read
  `AuthPolicyNotAttached` and opened by announcing that the route might be
  answering with no credential required — on a pass that had just recorded
  `Accepted=False`.
- **The policy half's message hedges.** It says the pass did not read the route
  as accepted, instead of asserting it is "accepted and SERVING".
- **`GovernanceSkipped` still records the tier**, under the policy half, as §5
  requires.

## 3. After restoring the listener — it heals

```
phase: Ready
status.auth.mode: apikey   routeRefused: <unset>   policyUnattached: <unset>
Ready                  True   Available
GovernanceSkipped      False  AuthVerifiedOnOneReplica
route: Accepted True gen=2 · policy ancestor: gateway.networking.k8s.io Gateway assayd
```

Both claims clear, with no operator restart and no administrator action beyond
undoing the change.

## What it does not show

The **policy half alone** — a policy the Gateway does not attach on a route it
DOES accept — is still not measured, here or anywhere. In this incident the two
are inseparable: renaming the listener causes both. §8.1 case 19 (f) writes
that state's report and measures the operator's reaction to it, which is not
the same thing, and the `make conformance-cluster` case that would measure it
is still owed (§8.1).

This run also measures one gateway replica, as everything else here does.
