# agentgateway 1.5.0: how long does a published route take to lock, and what else the slice's cluster cases found (2026-09-13)

- **Question** (design 03 §8.1, first-slice bullet; §3.3.3's deadline paragraph): J2's `Lock` writes `<agent>-auth` onto a route that is **already serving**, and trusts it only once an anonymous request goes `200` → `401`. How long does that take after the write, and does the route pass through anything but `200` and `401` on the way? §3.3.3's 4-minute deadline for `Lock` rested on one hand measurement of a *prepared* route and on a test tolerance. Nothing had timed a published route.
- **Answer: about 15 ms, and nothing else on the way.** Over 20 trials in two runs, the first anonymous `401` arrived **13.9–16.1 ms after the policy write started (median 14.3 ms)**. The write itself took about 3 ms. Each trial also checks and reports its own resolution: the request loop answered about every 0.25 ms, and the longest batch of answers its stream delivered could have held one for at most 4.4 ms. So each figure is good to about 5 ms. Every answer before the `401` was the backend's `200`, and the next 20 after it were all `401`.
- **Two earlier sets of figures in this note are withdrawn.** Independent reviews of this change found the fault in each, and both sets hold only as upper bounds.
  - **13–77 ms, median 19 ms**, was the measuring loop's own rhythm. Each write started straight after an answer arrived, from a loop that started one `curl` per request, about 65 ms apart. So the next answer was always about one loop interval away, and `kubectl apply` took about 45 ms of that interval.
  - **30–118 ms, medians 95 and 59 ms**, was a buffer's flushes. The fixed loop wrote its codes to `curl`'s stdout, which musl holds in a 1 KiB buffer, so answers reached the test 256 at a time. Its pinned `curl` was also a `linux/386` image, emulated on this arm64 host, which made every request about twice as slow. The loop now writes to stderr, runs `curl` natively, and fails any trial whose own resolution it cannot vouch for (the method, below).
- **What this means for the deadline: no change.** 4 minutes is about 15,000 times the worst case measured. The measurement does not argue for a longer deadline. It does not justify a shorter one either. It is one idle, single-replica cluster, and it says nothing of a loaded gateway, several replicas, or the operator's own path, where status convergence and a 5 s probe cadence come before the first probe. A shorter deadline would page sooner on a lock that is genuinely late, and would risk paging on one that is only slow. That trade has no evidence either way here, so the constant stays, and the design says why (design 03 A74).
- **Measured against:** agentgateway and agentgateway-crds **1.5.0** (controller image `cr.agentgateway.dev/controller:v1.5.0`, read back from the running Deployment), Gateway API **v1.6.0** standard, on k3d **v5.8.3** with its default k3s **v1.33.6-k3s1**, one server node and no agents, Docker 29.2.1, one gateway replica, on an otherwise idle arm64 laptop. The traffic Pod runs `curlimages/curl` 8.11.1's multi-arch image, natively.
- **Reproduce:** `make conformance-cluster`. The trials are `TestSliceAPublishedRouteLocksFrom200To401`, and each prints a `LOCK-LATENCY` line.
- **Re-verify:** whenever the e2e's agentgateway release moves, and by 2026-11-13 at the latest. `make test` enforces only part of the first: `TestTheSliceCasesRunTheE2EsAgentgatewayRelease` fails when `hack/e2e.sh`'s `AGW_VERSION` and `hack/conformance-cluster.sh`'s `SLICE_AGW_VERSION` differ. Nothing runs `make conformance-cluster` for you, in `make test` or in CI, so re-measuring is the upgrader's run.

## Method

Everything runs in the test's own namespace behind its own Gateway. The Gateway is shaped like `hack/e2e.sh`'s, with the serving listener named as the operator's routes name it (`http`, port 8080) and a second listener, `tools`, on 8081. No operator runs.

1. **The policy is the compiler's.** Each `<agent>-auth` is `compiler.AuthPolicy`'s output for an Agent in namespace `conf-team`: API-key authentication (`mode: Strict`, the constant key-source selector) and one CEL rule admitting group `conf-team`. It is exactly what the operator writes.
2. **The route is the emitter's shape**, written by the test: `<agent>-serving`, `parentRefs` to the `http` listener, the Agent's hostname, `PathPrefix: /`, and one `backendRef` to an `agnhost netexec` Service, which answers `200` at every path.
3. **Each trial is a new Agent**, with its own route and hostname, so no trial inherits another's configuration. The route must answer an anonymous `200` before the trial starts.
4. **A request loop runs inside the cluster**, from a Pod in the same namespace, through the Gateway's Service. Each `curl` process sends 500 requests through a URL glob, each with `Connection: close`, and the loop starts the next process until a stop file appears. There is a gap of one process start, a few milliseconds, every 500 requests.
   - **Each answer is written to `curl`'s stderr**, which is unbuffered, as its code and its `%{num_connects}`, and is stamped when it reaches the test through `kubectl exec`.
   - **The loop checks itself, and a trial fails if the check fails.** Any request reporting `num_connects` of 0 reused a connection, which a policy applied per connection could hide behind. Answers that reach the test within 50 µs of each other came in one write, so the longest such run, times the loop's interval, is how long its first answer can have waited. That **hold** must stay under 5 ms, and each trial reports it. With the interval, it is the trial's resolution.
   - **How the check was set.** Measured by hand first, through Docker: the loop's own `curl` to stderr gave 15 of 499 answers inside 50 µs, with no gap over 0.6 ms, and every request a connection of its own; to stdout, 496 of 499; and without `Connection: close`, 499 of 500 requests reused one. Through `kubectl exec` a few answers still arrive together, because the native `curl` answers faster than the stream forwards single lines. A first form of the check failed on the share of such arrivals, and so failed runs whose batches held an answer for about a millisecond. The hold is what matters, so the hold is what is checked.
5. **The write is one request.** The policy is created through client-go, on a connection warmed by a list just before, so the timing brackets that one request and nothing else. A random pause of up to 250 ms comes first, so the write does not start at a fixed point in the loop's rhythm.
6. **Latency is from the write's start to the arrival of the first `401`.** The API server commits somewhere inside the write, so this is an upper bound on the time from commit to enforcement. It is loose by the write's own duration and the trial's resolution. The time from the write's return is logged too. A `401` is accepted whenever it arrives, even before the write's response: no policy existed before the write started.
7. **Only `200` or `401` may appear.** Any other code while the policy lands fails the trial, because `Lock` keeps the route's `backendRefs` throughout (§3.3.3). After the first `401`, the next 20 answers must all be `401`, so a lock that flapped would fail. A key in the admitted group must still get `200` afterwards.

## Results

A hand run of `go test -tags cluster -run '^TestSlice'` on 2026-09-13, against a cluster provisioned as the script does:

| Trial | From write start (ms) | From write return (ms) | Write (ms) | Loop interval (ms) | Hold (ms) | Anonymous `200`s after the write |
|---|---|---|---|---|---|---|
| 1 | 14.6 | 11.9 | 2.7 | 0.26 | 1.06 | 44 |
| 2 | 14.8 | 11.7 | 3.1 | 0.25 | 1.26 | 37 |
| 3 | 14.2 | 11.4 | 2.8 | 0.24 | 1.22 | 36 |
| 4 | 14.5 | 11.8 | 2.7 | 0.26 | 1.06 | 41 |
| 5 | 14.6 | 11.5 | 3.1 | 0.21 | 1.04 | 37 |
| 6 | 14.0 | 11.3 | 2.7 | 0.23 | 1.17 | 29 |
| 7 | 14.0 | 11.3 | 2.7 | 0.24 | 4.38 | 31 |
| 8 | 14.2 | 11.4 | 2.9 | 0.21 | 1.88 | 37 |
| 9 | 15.2 | 12.2 | 3.0 | 0.25 | 2.79 | 35 |
| 10 | 14.3 | 11.3 | 3.0 | 0.25 | 2.24 | 36 |
| **n=10** | **min 14.0, median 14.4, max 15.2** | **min 11.3, median 11.5, max 12.2** | median 2.8 | median 0.25 | median 1.24, max 4.38 | 29–44 |

The `make conformance-cluster` run that closed the change, on a fresh cluster the same day:

| Trial | From write start (ms) | From write return (ms) | Write (ms) | Loop interval (ms) | Hold (ms) | Anonymous `200`s after the write |
|---|---|---|---|---|---|---|
| 1 | 14.5 | 11.7 | 2.8 | 0.25 | 0.76 | 40 |
| 2 | 14.3 | 11.4 | 2.9 | 0.24 | 0.98 | 38 |
| 3 | 14.2 | 11.5 | 2.7 | 0.24 | 1.91 | 38 |
| 4 | 14.3 | 11.5 | 2.7 | 0.24 | 1.43 | 38 |
| 5 | 14.8 | 11.3 | 3.4 | 0.22 | 1.09 | 35 |
| 6 | 14.6 | 11.6 | 3.0 | 0.24 | 0.98 | 40 |
| 7 | 16.1 | 13.0 | 3.2 | 0.26 | 3.31 | 26 |
| 8 | 13.9 | 11.4 | 2.6 | 0.25 | 2.00 | 41 |
| 9 | 14.4 | 11.5 | 2.8 | 0.25 | 1.00 | 38 |
| 10 | 14.2 | 11.6 | 2.7 | 0.22 | 1.35 | 39 |
| **n=10** | **min 13.9, median 14.3, max 16.1** | **min 11.3, median 11.5, max 13.0** | median 2.8 | median 0.24 | median 1.22, max 3.31 | 26–41 |

**Both runs together: 20 trials, 13.9–16.1 ms from the write's start.** The spread across trials, about 2 ms, is inside each trial's own resolution, so this measurement cannot say whether propagation itself varies. Twenty trials on one idle cluster is too few to state a tail.

The last column counts requests answered `200` after the write returned: 26 to 44 per trial. They are what the backend answered in the window between the write landing at the API server and the proxy taking the policy, to anyone. Before the stream was fixed this column was inflated, because answers buffered before the write were counted as arriving after it.

**The prepared route** (`TestSliceAPreparedRouteAnswers500Then401OnTheServingListener`), on the `http` listener this time, where the 2026-09-11 spike used `tools`. It answered `500` before the policy and `401` after it, and nothing else in between. An unknown key got `401`, and a valid key reached the missing backend and got `500`, as the spike found. The case also logs how long the `401` took after the policy reported `Attached`: 45–105 ms across the runs. That is **not a latency**. The case probes once a second through `kubectl exec`, and the first probe after `Attached` was already refused, so the figure is one probe's round trip. It bounds the wait from above and no more. The case asserts only that the `401` arrives within the 4-minute deadline.

## What else the same runs measured

These are design 03 §8.1's `cluster` cases that the first-slice bullet lists, and case 2, which §3.4.4 owes. Each is a test in `test/conformance/slice_*_cluster_test.go`, and each was killed by a mutation of its own (below).

- **Two groups (§8.1 case 2).** `compiler.AdmitGroupsExpression` renders `apiKey.group == "a" || apiKey.group == "b"`. With that rule, keys in `a` and `b` got `200`, a key in `c` got `403`, and anonymous got `401`.
- **A promotion (case 1, the gateway half).** The route was locked first. Then its one `backendRef` was moved from one revision's Service to the next, in place, and the per-Agent policy kept enforcing on the new revision. 26,999 anonymous requests were sent across the switch, and every one got `401`. Not exercised: a `Lock` racing a promotion, and a weighted two-backend shift. That the operator emits exactly one `traffic` policy is the operator's, and envtest's to pin.
- **A key set in another namespace (case 3).** A `ConfigMap` with the key-source label in namespace `conf-elsewhere` held a key in the admitted group. Then a canary key set was written in the policy's own namespace, and the case waited for the gateway to admit the canary. After that, the other namespace's key got `401` five times out of five, and a key in the same group in the policy's namespace got `200`. Case 7 adds a second namespace: a Gateway-level policy in the Gateway's namespace refused, with `401`, a key stored only in the route's namespace. The canary's proof rests on the controller reading `ConfigMap` events in order, which one cluster-wide watch delivers; that is not measured separately.
- **A duplicate `keyHash` under a different group (case 4).** It is not refused. Two `ConfigMap`s, `conf-dup-a` and `conf-dup-b`, held the same hash under two groups. Each of 8 writes swapped the groups, and every second pair reversed the order the two were written in. Each write also added a canary key of its own to each `ConfigMap`, and was taken as landed only once both canaries were admitted. Then 30 answers were taken, and all 30 had to agree.
  - The key never got `401`. After every landed write it was admitted (`200`) or refused (`403`) as a member of **one** of the two groups, all 30 answers the same.
  - Five runs of 8 landed writes each:

    | Run | Groups | `conf-dup-b` won | `conf-dup-a` won | Won by the one written last |
    |---|---|---|---|---|
    | hand run, emulated `curl` | swapped each write | 7 | 1 | 5 |
    | `make conformance-cluster`, emulated `curl` | swapped each write | 6 | 2 | 6 |
    | mutation M8b (below) | the same on every write | 6 | 2 | 4 |
    | hand run, native `curl` | swapped each write | 6 | 2 | 4 |
    | closing `make conformance-cluster` | swapped each write | 8 | 0 | 4 |
    | **total, 40 writes** | | **33** | **7** | **23** |

    This case does not use the request loop, so the loop's faults do not touch it, and the emulated runs stand.
  - Neither the name nor the write order explains every write. In the M8b run the groups never changed, only the canaries did, and the winner still moved twice. So "which one wins" is not stated here as a rule, and the CRD calls it undefined. What is stated is that a duplicate can put the credential in either group at any write to either `ConfigMap`, with nothing reporting it.
  - **An earlier version of this section is withdrawn.** It reported 15 writes over three runs, which followed neither names nor order. Those writes were taken as landed after a ten-second sleep, with nothing proving it, so they may have measured writes that had not yet reached the gateway. An independent review of this change found it, and its own run with canaries saw `conf-dup-b` win 12 writes out of 12.
- **An explicit `mode: Strict` (case 5).** The compiler's policy and the same policy with `mode` removed gave the same four answers: anonymous `401`, unknown key `401`, admitted key `200`, wrong-group key `403`. The API server stored the second with `mode: Strict`.
- **A Gateway-level policy beside `<agent>-auth` (case 7).** A second Gateway, with a policy in its own namespace targeting the Gateway: API-key authentication over a key set in that namespace, admitting group `gwgroup`.
  - With **only** the Gateway's policy, a **prepared** route answered anonymous `401`. The Gateway's key reached the missing backend and got `500`. So `Create`'s probe cannot tell this `401` from its own policy's (design 03 §3.3.3, the sixth critique's MINOR 5).
  - With only the Gateway's policy, a published route admitted the Gateway's key (`200`), and refused the route's key as unknown (`401`).
  - With `<agent>-auth` on the route as well, the **route's policy decided alone**. The Gateway's key got `401`, the route's key `200`, and a wrong-group key `403`. A combination requiring both policies would have refused the route's key, which the Gateway's key set does not hold. The two policies are not merged: the route's overrides the Gateway's.
  - Only this shape was measured, with both `apiKeyAuthentication` and `authorization` set at both levels. A Gateway-level policy setting one of the two, and a `ListenerSet` target, were not.

## Mutations

Each case was run with a change that must make it fail, and each change was restored from a copy afterwards. INVALID means the mutant did not build, which proves nothing. No mutant was INVALID.

**The first batch**, against the first version of each case:

| Case | Mutation | Result |
|---|---|---|
| prepared route | no policy written | killed: `500` for the whole 4-minute deadline |
| prepared route | the probe sent to the `tools` listener | killed: `404` |
| published route | the policy targets a route that does not exist | killed: 3,640 anonymous `200`s over 4 minutes and no `401` |
| published route | the route stripped of `backendRefs` as the policy lands | killed: an admitted key got `500` after the lock |
| two groups | only group `a` rendered | killed: group `b`'s key got `403` |
| promotion | promoted by writing a second route for the new revision | killed: the first anonymous request got `200` |
| explicit `Strict` | the compiler's policy emitted with `mode: Optional` | killed: anonymous got `403`, not `401` |
| key set elsewhere | the key set written into the policy's namespace | killed: the key got `200` |
| duplicate key | the key written in no selected `ConfigMap` | killed: `401` |
| Gateway-level policy | no Gateway-level policy | killed: anonymous got `500` on the prepared route |
| Gateway-level policy | no `<agent>-auth` beside it | killed: the Gateway's key got `200` |

The five mutations of `compiler.AdmitGroupsExpression` itself were killed by its unit tests: no sort, sorting the caller's slice, no repeat check, no empty-group check, and the first group only.

**The second batch**, against the cases after the first review:

| Case | Mutation | Result |
|---|---|---|
| published route | the policy targets a route that does not exist | killed: 344,001 anonymous `200`s over 4 minutes, and no `401` |
| published route | the route stripped of `backendRefs` as the policy lands | killed: an admitted key got `500` after the lock |
| promotion | promoted by writing a second route for the new revision | killed: anonymous request 95 got `200` |
| duplicate key | the key written in no selected `ConfigMap` | killed: `401` on all 30 answers |
| duplicate key | no conflicting group: both `ConfigMap`s give the admitted group | killed: admitted 8 times, refused 0 |
| duplicate key | the groups never swapped between writes | **survived**, correctly: the winner moved between writes anyway, so both outcomes still occurred. Its writes are the M8b row above |
| Gateway-level policy | no Gateway-level policy | killed: anonymous got `500` on the prepared route |
| Gateway-level policy | no `<agent>-auth` beside it | killed: the Gateway's key got `200` |
| Gateway-level policy | the route's key also stored in the Gateway's namespace | killed: it got `403`, not `401` |
| `AdmitGroupsExpression` | the empty-set check removed | killed: the error no longer said "the set is empty" |

The review had found that last mutation surviving: the CEL unparser refused the empty set anyway, with a message about the unparser. The test now requires each refusal's own message. `TestTheSliceCasesRunTheE2EsAgentgatewayRelease` was killed by moving `SLICE_AGW_VERSION` to 1.5.1.

**The third batch**, against the stream's checks and case 3's canary, after the second review:

| Case | Mutation | Result |
|---|---|---|
| published route | the route loses its backend just before the policy write | killed by the only-`200`-or-`401` rule: an anonymous request got `500` |
| published route | the codes written to stdout again | killed by the hold check: up to 171 answers in one write, a hold of 27 ms |
| published route | `Connection: close` removed | killed by the connection check: 1,996 of 2,000 requests reused a connection |
| key set elsewhere | the canary written in the other namespace | killed: the canary was never admitted, `401` |

The first published-route mutation is the one the second review asked for. Stripping the route after the write, in the first two batches, was caught by the admitted key's check instead, because the policy landed before the route lost its backend.

## Limits

- **One replica, one idle cluster.** Every latency is what one proxy took to receive its configuration, with nothing else happening. H2 (design 03 §3.3.3) is the rule for more replicas. It is not measured here.
- **Each figure is an upper bound on its trial**, loose by the write's duration and by the trial's resolution, which the trial reports and fails past 5 ms.
- **No operator.** The operator's `Lock` adds status convergence (§3.3.2's tuple) and a 5 s probe cadence before its first probe. This measures only what those wait for.
