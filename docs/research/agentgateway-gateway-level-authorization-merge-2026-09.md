# A Gateway-level policy beside a route's `-auth`: authorization merges, and `Override` takes the route (agentgateway 1.5.0)

- **Date**: 2026-09-13. The quoted run started its test binary at 2026-09-13T18:35:23Z.
- **Measured against**: agentgateway and agentgateway-crds **1.5.0** (controller image `cr.agentgateway.dev/controller:v1.5.0`, read back from the running Deployment), Gateway API **v1.6.0** standard, on k3d with its default k3s, one server node and no agents, one gateway replica. The cluster was provisioned as `hack/conformance-cluster.sh`'s phase 2 provisions its own, and deleted afterwards.
- **Cases**: `test/conformance/slice_keys_cluster_test.go`: `TestSliceAGatewayLevelAllowRuleWidensTheRoute` and `TestSliceAGatewayLevelOverridePolicyTakesTheRoute`. A74's case 7, `TestSliceARoutePolicyOverridesAGatewayLevelOne`, ran in the same run and passed.
- **Why**: design 03 A75 and ADR-0034 Amendment 5. A74's case 7 found a Gateway-level policy "overridden, not merged" by a route's `<agent>-auth`. Its Gateway rule admitted a group whose key only the Gateway's key set held, so that key failed authentication on the route whichever way `authorization` combined. Whether a Gateway-level `authorization` rule merges with the route's was therefore not measured, and the CRD says "all rules are merged".
- **Re-verify**: at the next agentgateway upgrade, and no later than 2026-12-13. Both cases assert what was measured, so either fails if the behaviour changes.

## Method

Each case creates its own Gateway in the suite's Gateway namespace, shaped like `hack/e2e.sh`'s: the serving listener, `http` on 8080, and `tools` on 8081. On it the case creates two routes in the run namespace, each published to an `agnhost netexec` backend, which answers `200` at any path:

- **the route**, in `<agent>-serving`'s shape, carrying `<agent>-auth`, which is `compiler.AuthPolicy`'s output: `Strict` API-key authentication over the key sets carrying `assayd.dev/api-keys: "true"` in the run namespace, and one `authorization` rule admitting group `conf-team`;
- **a canary route** on the same Gateway, with no `-auth`. Its anonymous answer moves off `200` only when the Gateway-level policy is enforcing on this Gateway, and the case samples the route only after that.

The run namespace's key set holds `conf-team-key` in group `conf-team` and `conf-rogue-key` in group `rogue`. Every request is one `curl` from a Pod in the run namespace to the Gateway's Service, with the route's `Host`, `Authorization: Bearer <key>` when a key is sent, a 5 s timeout, and the card path.

## Results

### `authorization` merges: a Gateway-level `Allow` widens the route

Before any Gateway-level policy, `conf-rogue-key` got `403` from the route's rule, awaited and then required twice more, and `conf-team-key` got `200`.

The Gateway-level policy targets the Gateway, and allows `apiKey.group == "rogue"`. It was applied in two shapes, one after the other, with the canary back at `200` in between.

| Shape | Canary, anonymous | `conf-rogue-key` on the route | `conf-team-key` on the route |
|---|---|---|---|
| `authorization` alone | `200` → `403` | `200` ×5 | `200` ×3 |
| case 7's: API-key authentication over a key set in the Gateway's namespace, and `authorization` | `200` → `401` | `200` ×5 | `200` ×3 |

The run's lines:

```
slice_keys_cluster_test.go:459: authorization alone: the canary's anonymous answer went from 200 to 403, so the Gateway-level policy enforces
slice_keys_cluster_test.go:462: authorization alone: keyRogue on the route, under <agent>-auth and a Gateway-level Allow for its group: [200 200 200 200 200]
slice_keys_cluster_test.go:459: case 7's shape: the canary's anonymous answer went from 200 to 401, so the Gateway-level policy enforces
slice_keys_cluster_test.go:462: case 7's shape: keyRogue on the route, under <agent>-auth and a Gateway-level Allow for its group: [200 200 200 200 200]
--- PASS: TestSliceAGatewayLevelAllowRuleWidensTheRoute (6.41s)
```

So a key the route's own rule refuses is admitted on the route once a Gateway-level rule allows its group. The two rules combine as an OR. The route's authentication still decides who is authenticated: in case 7's shape the key is in the route's key set only, and it is admitted. The same result came from the case's first run, earlier the same day, which asserted the opposite and failed on it with `[200 200 200 200 200]` in both shapes.

### `strategy.inheritance: Override` takes the route from `<agent>-auth`

The Gateway's namespace holds a key set with `conf-gwover-key` in group `gwgroup`. Before any Gateway-level policy, `conf-gwover-key` got `401` on the route, because the route's key set does not hold it, and `conf-team-key` got `200`.

The Gateway-level policy is case 7's shape, allowing `apiKey.group == "gwgroup"`, with `strategy.inheritance: Override`. The canary went from `200` to `401`. Then:

```
slice_keys_cluster_test.go:575: the canary's anonymous answer went from 200 to 401, so the Override policy enforces
slice_keys_cluster_test.go:578: beside <agent>-auth and a Gateway-level Override policy: the Gateway's key [200 200 200 200 200], keyTeam [401 401 401 401 401]
--- PASS: TestSliceAGatewayLevelOverridePolicyTakesTheRoute (6.19s)
```

So under `Override` the Gateway's policy is the route's effective policy. The Gateway's key, which the route's own key set does not hold, is admitted, and the route's own key is refused. `<agent>-auth` no longer takes part. That is what the CRD's description of `Override` says.

## What design 03 takes from it

- A Gateway-level policy that sets `authorization`, or `strategy.inheritance: Override`, can widen a served Agent's route beside its `<agent>-auth`. Under W1 such an Agent reads `GovernanceSkipped=True` and `PolicyApplyIncomplete=True`, reason `GatewayAuthPolicy` (design 03 §3.2, "Gateway-level policies above the route").
- A Gateway-level policy that sets only authentication, and no `authorization` and no `Override`, is replaced on the route by `<agent>-auth`'s authentication. That is case 7's result, and it stands.

## Not measured

- A Gateway-level rule with `Deny` or `Require` actions beside the route's `Allow`.
- `phase: PreRouting`.
- More than one gateway replica.
- A ListenerSet, admitted or not.
- Whether the merge is an OR for more than two rules, or across more than two attachment points.
