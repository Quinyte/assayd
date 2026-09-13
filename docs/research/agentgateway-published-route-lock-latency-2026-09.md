# agentgateway 1.5.0: how long does a published route take to lock, and what else the slice's cluster cases found (2026-09-13)

- **Question** (design 03 §8.1, first-slice bullet; §3.3.3's deadline paragraph): J2's `Lock` writes `<agent>-auth` onto a route that is **already serving**, and trusts it only once an anonymous request goes `200` → `401`. How long does that take after the write, and does the route pass through anything but `200` and `401` on the way? §3.3.3's 4-minute deadline for `Lock` rested on one hand measurement of a *prepared* route and on a test tolerance. Nothing had timed a published route.
- **Answer: about a tenth of a second, and nothing else on the way.** Over 10 trials, the first anonymous `401` arrived **13–77 ms after the policy write returned (median 19 ms)**, or **60–123 ms after it started (median 64 ms)**. Every answer before it was the backend's `200`, and the next 20 answers after it were all `401`. The measurement is limited by its own resolution: the request loop answered about once every 64 ms.
- **What this means for the deadline: no change.** 4 minutes is about 2,000 times the worst case measured from the write's start. The measurement does not argue for a longer deadline. It does not justify a shorter one either: it is one idle, single-replica cluster, and it says nothing of a loaded gateway, several replicas, or the operator's own path, where status convergence and a 5 s probe cadence come before the first probe. A shorter deadline would page sooner on a lock that is genuinely late, and would risk paging on one that is only slow. That trade has no evidence either way here, so the constant stays, and the design says why (design 03 A74).
- **Measured against:** agentgateway and agentgateway-crds **1.5.0** (controller image `cr.agentgateway.dev/controller:v1.5.0`, read back from the running Deployment), Gateway API **v1.6.0** standard, on k3d **v5.8.3** with its default k3s **v1.33.6-k3s1**, one server node and no agents, Docker 29.2.1, one gateway replica, on an otherwise idle laptop cluster.
- **Reproduce:** `make conformance-cluster`. The trials are `TestSliceAPublishedRouteLocksFrom200To401`, and each prints a `LOCK-LATENCY` line.
- **Re-verify:** at every agentgateway upgrade, which `make conformance-cluster` does by bumping `SLICE_AGW_VERSION`, and by 2026-11-13 at the latest.

## Method

Everything runs in the test's own namespace behind its own Gateway. The Gateway is shaped like `hack/e2e.sh`'s, with the serving listener named as the operator's routes name it (`http`, port 8080) and a second listener, `tools`, on 8081. No operator runs.

1. **The policy is the compiler's.** Each `<agent>-auth` is `compiler.AuthPolicy`'s output for an Agent in namespace `conf-team`: API-key authentication (`mode: Strict`, the constant key-source selector) and one CEL rule admitting group `conf-team`. It is exactly what the operator writes.
2. **The route is the emitter's shape**, written by the test: `<agent>-serving`, `parentRefs` to the `http` listener, the Agent's hostname, `PathPrefix: /`, and one `backendRef` to an `agnhost netexec` Service, which answers `200` at every path.
3. **Each trial is a new Agent**, with its own route and hostname, so no trial inherits another's configuration. The route must answer an anonymous `200` before the trial starts.
4. **A request loop runs inside the cluster**: `curl` of the Agent's card path with its `Host` header, back to back, from a Pod in the same namespace, through the Gateway's Service. Each answer's code is streamed to the test and stamped when it arrives. The trial waits for three `200`s in a row, discards anything already received, and then writes the policy with `kubectl apply`.
5. **Latency** is from the moment `kubectl apply` returned to the arrival of the first `401`. The API server commit is somewhere inside the write, so the latency from the commit lies between that figure and the one from the write's start. Both are recorded. Streaming delay only makes the figure larger.
6. **Only `200` or `401` may appear.** Any other code while the policy lands fails the trial, because `Lock` keeps the route's `backendRefs` throughout (§3.3.3). After the first `401`, the next 20 answers must all be `401`, so a lock that flapped would fail. A key in the admitted group must still get `200` afterwards.

## Results

From the `make conformance-cluster` run on 2026-09-13:

| Trial | From write return (ms) | From write start (ms) | Loop interval (ms) | Anonymous `200`s after the write |
|---|---|---|---|---|
| 1 | 19 | 64 | 66 | 0 |
| 2 | 14 | 60 | 64 | 0 |
| 3 | 20 | 66 | 65 | 0 |
| 4 | 19 | 64 | 64 | 0 |
| 5 | 29 | 74 | 64 | 0 |
| 6 | 77 | 123 | 63 | 1 |
| 7 | 22 | 68 | 65 | 0 |
| 8 | 18 | 63 | 63 | 0 |
| 9 | 17 | 62 | 65 | 0 |
| 10 | 13 | 60 | 64 | 0 |
| **n=10** | **min 13, median 19, max 77** | **median 64, max 123** | | |

An earlier hand run on another cluster, the same day with the same code, gave 13–75 ms from the write's return, median 19 ms. A `200` after the write is a request answered in the window between the write returning and the proxy taking the policy: at most one per trial.

**The prepared route, for comparison** (`TestSliceAPreparedRouteAnswers500Then401OnTheServingListener`). On the `http` listener this time, where the 2026-09-11 spike used `tools`: `500` before the policy, then `401` about 100 ms after the policy reported `Attached` (109 ms in the hand run), and only `500` in between. An unknown key got `401`, and a valid key reached the missing backend and got `500`, as the spike found.

## What else the same run measured

These are design 03 §8.1's `cluster` cases, which the first-slice bullet lists. Each is a test in `test/conformance/slice_*_cluster_test.go`, and each was killed by a mutation of its own (below).

- **Two groups (§8.1 case 2).** `compiler.AdmitGroupsExpression` renders `apiKey.group == "a" || apiKey.group == "b"`. With that rule, keys in `a` and `b` got `200`, a key in `c` got `403`, and anonymous got `401`.
- **A promotion (case 1, the gateway half).** Moving the route's one `backendRef` from one revision's Service to the next, in place, kept the per-Agent policy enforcing on the new revision. 80 anonymous requests were sent across the switch, and all 80 got `401`. Exactly one `traffic` policy targeted the route throughout.
- **A key set in another namespace (case 3).** A `ConfigMap` with the key-source label in namespace `conf-elsewhere`, holding a key in the admitted group, left that key at `401` five times out of five. A key in the same group in the policy's namespace got `200`.
- **A duplicate `keyHash` under a different group (case 4).** It is not refused. The key never got `401`; it was admitted (`200`) or refused (`403`) as a member of one of the two groups, stably until the next write. Which group was not predictable. Two `ConfigMap`s, `conf-dup-a` and `conf-dup-b`, held the same hash, and each write swapped the groups they gave it:

  | Run | Answers per write, in order (`T` = admitted as `conf-team`, `R` = refused as `rogue`) |
  |---|---|
  | hand run 1 (3 writes) | T, R, R |
  | hand run 2 (6 writes) | R, T, R, T, T, T |
  | `make conformance-cluster` (6 writes) | R, T, R, R, R, T |

  The winner followed neither the names nor the write order. The first write of hand run 1 and the first of the `make` run are the same state, `conf-dup-a` = `conf-team` and `conf-dup-b` = `rogue`, on fresh objects: one was admitted and the other refused. That matches the CRD's own words, "the behavior is undefined".
- **An explicit `mode: Strict` (case 5).** The compiler's policy and the same policy with `mode` removed gave the same four answers: anonymous `401`, unknown key `401`, admitted key `200`, wrong-group key `403`. The API server stored the second with `mode: Strict`.
- **A Gateway-level policy beside `<agent>-auth` (case 7).** A second Gateway, with a policy in its own namespace targeting the Gateway: API-key authentication over a key set in that namespace, admitting group `gwgroup`.
  - With **only** the Gateway's policy, a **prepared** route answered anonymous `401`. The Gateway's key reached the missing backend and got `500`. So `Create`'s probe cannot tell this `401` from its own policy's (design 03 §3.3.3, the sixth critique's MINOR 5).
  - With only the Gateway's policy, a published route admitted the Gateway's key (`200`) and refused the route's key as unknown (`401`).
  - With `<agent>-auth` on the route as well, the **route's policy decided alone**. The Gateway's key got `401`, the route's key `200`, and a wrong-group key `403`. The two policies are not merged: the route's overrides the Gateway's.
  - Only this shape was measured, with both `apiKeyAuthentication` and `authorization` set at both levels. A Gateway-level policy setting one of the two, and a `ListenerSet` target, were not.

## Mutations

Each case was run with a change that must make it fail, and each change was restored from a copy afterwards. All 11 cluster mutations were killed, and none were INVALID. The five mutations of `compiler.AdmitGroupsExpression` itself (no sort, sorting the caller's slice, no repeat check, no empty-group check, the first group only) were killed by its unit tests.

| Case | Mutation | Result |
|---|---|---|
| prepared route | no policy written | killed: `500` for the whole 4-minute deadline |
| prepared route | the probe sent to the `tools` listener | killed: `404` |
| published route | the policy targets a route that does not exist | killed: 3,640 anonymous `200`s over 4 minutes, and no `401` |
| published route | the route stripped of `backendRefs` as the policy lands | killed: an admitted key got `500` after the lock |
| two groups | only group `a` rendered | killed: group `b`'s key got `403` |
| promotion | promoted by writing a second route for the new revision | killed: the first anonymous request got `200` |
| explicit `Strict` | the compiler's policy emitted with `mode: Optional` | killed: anonymous got `403`, not `401` |
| key set elsewhere | the key set written into the policy's namespace | killed: the key got `200` |
| duplicate key | the key written in no selected `ConfigMap` | killed: `401` |
| Gateway-level policy | no Gateway-level policy | killed: anonymous got `500` on the prepared route |
| Gateway-level policy | no `<agent>-auth` beside it | killed: the Gateway's key got `200` |

The published-route case's second mutation was caught by the admitted key's `200` check, not by the only-`200`-or-`401` rule. The policy lands in tens of milliseconds, so anonymous requests were already getting `401` from the policy when the backendless route took over.

## Limits

- **One replica, one idle cluster.** Both latencies are what one proxy took to receive its configuration, with nothing else happening. H2 (design 03 §3.3.3) is the rule for more replicas. It is not measured here.
- **Resolution about 64 ms.** A `401` is seen at most one loop interval after the proxy starts giving it, plus the streaming delay. Every figure is an upper bound on that trial.
- **No operator.** The operator's `Lock` adds status convergence (§3.3.2's tuple) and a 5 s probe cadence before its first probe. This measures only what those wait for.
