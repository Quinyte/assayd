# agentgateway 1.5.0: how long does a published route take to lock, and what else the slice's cluster cases found (2026-09-13)

- **Question** (design 03 §8.1, first-slice bullet; §3.3.3's deadline paragraph): J2's `Lock` writes `<agent>-auth` onto a route that is **already serving**, and trusts it only once an anonymous request goes `200` → `401`. How long does that take after the write, and does the route pass through anything but `200` and `401` on the way? §3.3.3's 4-minute deadline for `Lock` rested on one hand measurement of a *prepared* route and on a test tolerance. Nothing had timed a published route.
- **Answer: about a tenth of a second, and nothing else on the way.** Over 20 trials in two runs, the first anonymous `401` arrived **30–118 ms after the policy write started (medians 95 ms and 59 ms)**. The write itself took about 3 ms, and the request loop answered about every 0.6 ms, so those two add only a few milliseconds of uncertainty. Every answer before the `401` was the backend's `200`, and the next 20 after it were all `401`.
- **The first figures this note published are withdrawn.** An earlier version reported 13–77 ms, median 19 ms. Those figures were the measuring loop's own rhythm, not the gateway's (the second review of design 03 A74). Each write started straight after an answer arrived, from a loop that started one `curl` per request, about 65 ms apart. So the next answer was always about one loop interval away, and `kubectl apply` took about 45 ms of that interval. The method below fixes both.
- **What this means for the deadline: no change.** 4 minutes is about 2,000 times the worst case measured. The measurement does not argue for a longer deadline. It does not justify a shorter one either. It is one idle, single-replica cluster, and it says nothing of a loaded gateway, several replicas, or the operator's own path, where status convergence and a 5 s probe cadence come before the first probe. A shorter deadline would page sooner on a lock that is genuinely late, and would risk paging on one that is only slow. That trade has no evidence either way here, so the constant stays, and the design says why (design 03 A74).
- **Measured against:** agentgateway and agentgateway-crds **1.5.0** (controller image `cr.agentgateway.dev/controller:v1.5.0`, read back from the running Deployment), Gateway API **v1.6.0** standard, on k3d **v5.8.3** with its default k3s **v1.33.6-k3s1**, one server node and no agents, Docker 29.2.1, one gateway replica, on an otherwise idle laptop cluster.
- **Reproduce:** `make conformance-cluster`. The trials are `TestSliceAPublishedRouteLocksFrom200To401`, and each prints a `LOCK-LATENCY` line.
- **Re-verify:** whenever the e2e's agentgateway release moves, and by 2026-11-13 at the latest. `make test` enforces the first: `TestTheSliceCasesRunTheE2EsAgentgatewayRelease` fails when `hack/e2e.sh`'s `AGW_VERSION` and `hack/conformance-cluster.sh`'s `SLICE_AGW_VERSION` differ.

## Method

Everything runs in the test's own namespace behind its own Gateway. The Gateway is shaped like `hack/e2e.sh`'s, with the serving listener named as the operator's routes name it (`http`, port 8080) and a second listener, `tools`, on 8081. No operator runs.

1. **The policy is the compiler's.** Each `<agent>-auth` is `compiler.AuthPolicy`'s output for an Agent in namespace `conf-team`: API-key authentication (`mode: Strict`, the constant key-source selector) and one CEL rule admitting group `conf-team`. It is exactly what the operator writes.
2. **The route is the emitter's shape**, written by the test: `<agent>-serving`, `parentRefs` to the `http` listener, the Agent's hostname, `PathPrefix: /`, and one `backendRef` to an `agnhost netexec` Service, which answers `200` at every path.
3. **Each trial is a new Agent**, with its own route and hostname, so no trial inherits another's configuration. The route must answer an anonymous `200` before the trial starts.
4. **A request loop runs inside the cluster**, from a Pod in the same namespace, through the Gateway's Service. Each `curl` process sends 500 requests through a URL glob, each on a new connection (`Connection: close`), so a policy applied per connection would still show on the next request. The loop starts the next process until a stop file appears. Each answer's code is streamed to the test and stamped when it arrives. There is a gap of one process start, tens of milliseconds, every 500 requests.
5. **The write is one request.** The policy is created through client-go, on a connection warmed by a list just before, so the timing brackets that one request and nothing else. A random pause of up to 250 ms comes first, so the write does not start at a fixed point in the loop's rhythm.
6. **Latency is from the write's start to the arrival of the first `401`.** The API server commits somewhere inside the write, so this is an upper bound on the time from commit to enforcement. It is loose by the write's own duration, the loop's interval, and the streaming delay. The time from the write's return is logged too. A `401` is accepted whenever it arrives, even before the write's response: no policy existed before the write started.
7. **Only `200` or `401` may appear.** Any other code while the policy lands fails the trial, because `Lock` keeps the route's `backendRefs` throughout (§3.3.3). After the first `401`, the next 20 answers must all be `401`, so a lock that flapped would fail. A key in the admitted group must still get `200` afterwards.

## Results

A hand run of `go test -tags cluster -run '^TestSlice'` on 2026-09-13, against a cluster provisioned as the script does:

| Trial | From write start (ms) | From write return (ms) | Write (ms) | Loop interval (ms) | Anonymous `200`s after the write |
|---|---|---|---|---|---|
| 1 | 118.3 | 115.4 | 2.8 | 0.57 | 20 |
| 2 | 97.9 | 95.3 | 2.6 | 0.55 | 64 |
| 3 | 43.1 | 40.0 | 3.1 | 0.57 | 190 |
| 4 | 92.8 | 90.0 | 2.8 | 0.55 | 78 |
| 5 | 104.0 | 101.4 | 2.6 | 0.56 | 45 |
| 6 | 115.7 | 113.1 | 2.6 | 0.58 | 17 |
| 7 | 67.0 | 64.1 | 2.9 | 0.55 | 127 |
| 8 | 66.4 | 63.7 | 2.8 | 0.57 | 243 |
| 9 | 31.0 | 28.2 | 2.8 | 0.56 | 215 |
| 10 | 106.7 | 103.9 | 2.8 | 0.55 | 38 |
| **n=10** | **min 31.0, median 95.3, max 118.3** | **min 28.2, median 92.6, max 115.4** | median 2.8 | median 0.56 | |

The `make conformance-cluster` run that closed the change, on a fresh cluster the same day:

| Trial | From write start (ms) | From write return (ms) | Write (ms) | Loop interval (ms) | Anonymous `200`s after the write |
|---|---|---|---|---|---|
| 1 | 42.9 | 40.0 | 2.8 | 0.56 | 187 |
| 2 | 66.7 | 63.7 | 2.9 | 0.57 | 243 |
| 3 | 54.7 | 51.6 | 3.0 | 0.57 | 0 |
| 4 | 89.5 | 86.5 | 3.0 | 0.59 | 83 |
| 5 | 43.6 | 40.8 | 2.8 | 0.56 | 185 |
| 6 | 62.7 | 59.9 | 2.8 | 0.58 | 0 |
| 7 | 84.7 | 81.9 | 2.8 | 0.56 | 93 |
| 8 | 33.0 | 30.0 | 3.0 | 0.56 | 210 |
| 9 | 29.7 | 26.6 | 3.1 | 0.59 | 226 |
| 10 | 78.9 | 76.1 | 2.8 | 0.55 | 102 |
| **n=10** | **min 29.7, median 58.7, max 89.5** | **min 26.6, median 55.8, max 86.5** | median 2.9 | median 0.57 | |

**Both runs together: 20 trials, 29.7–118.3 ms from the write's start.** The two medians, 95 ms and 59 ms, differ by more than the method's resolution, so what varies is the gateway's propagation, not the measurement. Twenty trials on one idle cluster is too few to state a tail.

The "anonymous `200`s after the write" column is the requests the backend answered between the write returning and the proxy taking the policy. At about 0.6 ms a request, 17–243 of them fit in each trial. Over that window, the route is still open to anyone.

**The prepared route** (`TestSliceAPreparedRouteAnswers500Then401OnTheServingListener`), on the `http` listener this time, where the 2026-09-11 spike used `tools`. It answered `500` before the policy and `401` after it, and nothing else in between. An unknown key got `401`, and a valid key reached the missing backend and got `500`, as the spike found. The case also logs how long the `401` took after the policy reported `Attached`: about 100 ms. That is **not a latency**. The case probes once a second through `kubectl exec`, and the first probe after `Attached` was already refused, so 100 ms is one probe's round trip. It bounds the wait from above and no more. The case asserts only that the `401` arrives within the 4-minute deadline.

## What else the same run measured

These are design 03 §8.1's `cluster` cases that the first-slice bullet lists, and case 2, which §3.4.4 owes. Each is a test in `test/conformance/slice_*_cluster_test.go`, and each was killed by a mutation of its own (below).

- **Two groups (§8.1 case 2).** `compiler.AdmitGroupsExpression` renders `apiKey.group == "a" || apiKey.group == "b"`. With that rule, keys in `a` and `b` got `200`, a key in `c` got `403`, and anonymous got `401`.
- **A promotion (case 1, the gateway half).** The route was locked first. Then its one `backendRef` was moved from one revision's Service to the next, in place, and the per-Agent policy kept enforcing on the new revision. 9,499 anonymous requests were sent across the switch, and every one got `401`. Not exercised: a `Lock` racing a promotion, and a weighted two-backend shift. That the operator emits exactly one `traffic` policy is the operator's, and envtest's to pin.
- **A key set in another namespace (case 3).** A `ConfigMap` with the key-source label in namespace `conf-elsewhere`, holding a key in the admitted group, left that key at `401` five times out of five. A key in the same group in the policy's namespace got `200`. Case 7 adds a second namespace: a Gateway-level policy in the Gateway's namespace refused, with `401`, a key stored only in the route's namespace.
- **A duplicate `keyHash` under a different group (case 4).** It is not refused. Two `ConfigMap`s, `conf-dup-a` and `conf-dup-b`, held the same hash under two groups. Each of 8 writes swapped the groups, and every second pair reversed the order the two were written in. Each write also added a canary key of its own to each `ConfigMap`, and was taken as landed only once both canaries were admitted. Then 30 answers were taken, and all 30 had to agree.
  - The key never got `401`. After every write it was admitted (`200`) or refused (`403`) as a member of **one** of the two groups, all 30 answers the same.
  - Three runs of 8 landed writes each:

    | Run | Groups | `conf-dup-b` won | `conf-dup-a` won | Won by the one written last |
    |---|---|---|---|---|
    | hand run | swapped each write | 7 | 1 | 5 |
    | `make conformance-cluster` | swapped each write | 6 | 2 | 6 |
    | mutation M8b (below) | the same on every write | 6 | 2 | 4 |
    | **total, 24 writes** | | **19** | **5** | **15** |

  - Neither the name nor the write order explains every write. In the third run the groups never changed, only the canaries did, and the winner still moved twice. So "which one wins" is not stated here as a rule, and the CRD calls it undefined. What is stated is that a duplicate can put the credential in either group at any write to either `ConfigMap`, with nothing reporting it.
  - **An earlier version of this section is withdrawn.** It reported 15 writes over three runs, which followed neither names nor order. Those writes were taken as landed after a ten-second sleep, with nothing proving it, so they may have measured writes that had not yet reached the gateway (the second review of A74). The reviewer's own run with canaries saw `conf-dup-b` win 12 writes out of 12.

- **An explicit `mode: Strict` (case 5).** The compiler's policy and the same policy with `mode` removed gave the same four answers: anonymous `401`, unknown key `401`, admitted key `200`, wrong-group key `403`. The API server stored the second with `mode: Strict`.
- **A Gateway-level policy beside `<agent>-auth` (case 7).** A second Gateway, with a policy in its own namespace targeting the Gateway: API-key authentication over a key set in that namespace, admitting group `gwgroup`.
  - With **only** the Gateway's policy, a **prepared** route answered anonymous `401`. The Gateway's key reached the missing backend and got `500`. So `Create`'s probe cannot tell this `401` from its own policy's (design 03 §3.3.3, the sixth critique's MINOR 5).
  - With only the Gateway's policy, a published route admitted the Gateway's key (`200`), and refused the route's key as unknown (`401`).
  - With `<agent>-auth` on the route as well, the **route's policy decided alone**. The Gateway's key got `401`, the route's key `200`, and a wrong-group key `403`. The two policies are not merged: the route's overrides the Gateway's.
  - Only this shape was measured, with both `apiKeyAuthentication` and `authorization` set at both levels. A Gateway-level policy setting one of the two, and a `ListenerSet` target, were not.

## Mutations

Each case was run with a change that must make it fail, and each change was restored from a copy afterwards. INVALID means the mutant did not build, which proves nothing.

| Case | Mutation | Result |
|---|---|---|
| prepared route | no policy written | killed: `500` for the whole 4-minute deadline |
| prepared route | the probe sent to the `tools` listener | killed: `404` |
| published route | the policy targets a route that does not exist | first batch: killed, 3,640 anonymous `200`s over 4 minutes and no `401` |
| published route | the route stripped of `backendRefs` as the policy lands | first batch: killed, an admitted key got `500` after the lock |
| two groups | only group `a` rendered | killed: group `b`'s key got `403` |
| promotion | promoted by writing a second route for the new revision | first batch: killed, the first anonymous request got `200` |
| explicit `Strict` | the compiler's policy emitted with `mode: Optional` | killed: anonymous got `403`, not `401` |
| key set elsewhere | the key set written into the policy's namespace | killed: the key got `200` |
| duplicate key | the key written in no selected `ConfigMap` | first batch: killed, `401` |
| Gateway-level policy | no Gateway-level policy | first batch: killed, anonymous got `500` on the prepared route |
| Gateway-level policy | no `<agent>-auth` beside it | first batch: killed, the Gateway's key got `200` |

The first batch ran against the first version of each case. The cases the review changed were mutated again (below). Of the published-route mutations, the second was caught by the admitted key's `200` check, not by the only-`200`-or-`401` rule. The policy lands before the route loses its backend, so anonymous requests were already getting `401`.

**The second batch**, against the cases as the review left them, on the hand run's cluster:

| Case | Mutation | Result |
|---|---|---|
| published route | the policy targets a route that does not exist | killed: 344,001 anonymous `200`s over 4 minutes, and no `401` |
| published route | the route stripped of `backendRefs` as the policy lands | killed: an admitted key got `500` after the lock |
| promotion | promoted by writing a second route for the new revision | killed: anonymous request 95 got `200` |
| duplicate key | the key written in no selected `ConfigMap` | killed: `401` on all 30 answers |
| duplicate key | no conflicting group: both `ConfigMap`s give the admitted group | killed: admitted 8 times, refused 0 |
| duplicate key | the groups never swapped between writes | **survived**, correctly: the winner moved between writes anyway, so both outcomes still occurred. Its writes are the third row of the table above |
| Gateway-level policy | no Gateway-level policy | killed: anonymous got `500` on the prepared route |
| Gateway-level policy | no `<agent>-auth` beside it | killed: the Gateway's key got `200` |
| Gateway-level policy | the route's key also stored in the Gateway's namespace | killed: it got `403`, not `401` |
| `AdmitGroupsExpression` | the empty-set check removed | killed: the error no longer said "the set is empty" |

The five mutations of `compiler.AdmitGroupsExpression` itself were killed by its unit tests: no sort, sorting the caller's slice, no repeat check, no empty-group check, and the first group only. The review found a sixth that survived: removing the empty-set check left the set still refused, by the CEL unparser, with a message about the unparser. The test now requires each refusal's own message. `TestTheSliceCasesRunTheE2EsAgentgatewayRelease` was killed by moving `SLICE_AGW_VERSION` to 1.5.1.

## Limits

- **One replica, one idle cluster.** Every latency is what one proxy took to receive its configuration, with nothing else happening. H2 (design 03 §3.3.3) is the rule for more replicas. It is not measured here.
- **The loop has gaps.** A new `curl` process starts every 500 requests, tens of milliseconds apart. A `401` that falls into one is seen that much later. Every figure is an upper bound on its trial.
- **No operator.** The operator's `Lock` adds status convergence (§3.3.2's tuple) and a 5 s probe cadence before its first probe. This measures only what those wait for.
