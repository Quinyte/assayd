# agentgateway 1.5.0: how long does a published route take to lock, and what else the slice's cluster cases found (2026-09-13)

- **Question** (design 03 §8.1, first-slice bullet; §3.3.3's deadline paragraph): J2's `Lock` writes `<agent>-auth` onto a route that is **already serving**, and trusts it only once an anonymous request goes `200` → `401`. How long does that take after the write, and does the route pass through anything but `200` and `401` on the way? §3.3.3's 4-minute deadline for `Lock` rested on one hand measurement of a *prepared* route and on a test tolerance. Nothing had timed a published route.
- **Answer: within about 16 ms, and nothing else on the way.** Over 10 trials, the first anonymous `401` arrived **14.0–16.1 ms after the policy write started (median 14.6 ms)**. The write itself took about 3 ms. Every answer before the `401` was the backend's `200`, and the next 20 after it were all `401`. **These are upper bounds, and no tighter precision is claimed.** Every error in the method adds delay after the write's start, so none can make a figure too small. How much too large each figure is has not been established. The stream's answers are not evenly spaced (below), and a policy that lands in a gap between them is seen late by up to that gap.
- **Three earlier claims in this note are withdrawn.** Independent reviews of this change found each.
  - **13–77 ms, median 19 ms**, was the measuring loop's own rhythm. Each write started straight after an answer arrived, from a loop that started one `curl` per request, about 65 ms apart. So the next answer was always about one loop interval away, and `kubectl apply` took about 45 ms of that interval.
  - **30–118 ms, medians 95 and 59 ms**, was a buffer's flushes. The next loop wrote its codes to `curl`'s stdout, which musl holds in a 1 KiB buffer, so answers reached the test 256 at a time. Its pinned `curl` was also a `linux/386` image, emulated on this arm64 host.
  - **"Each figure is good to about 5 ms"** was no bound. It multiplied the longest run of coalesced answers by the mean interval between answers. But answers are not evenly spaced: every `curl` restart leaves a gap of a few milliseconds, one process has gaps of about 3 ms, and a policy that lands in one is seen late by up to that gap, which no run records.
- **What this means for the deadline: no change.** 4 minutes is about 15,000 times the worst case measured. The measurement does not argue for a longer deadline. It does not justify a shorter one either. It is one idle, single-replica cluster, and it says nothing of a loaded gateway, several replicas, or the operator's own path, where status convergence and a 5 s probe cadence come before the first probe. A shorter deadline would page sooner on a lock that is genuinely late, and would risk paging on one that is only slow. That trade has no evidence either way here, so the constant stays, and the design says why (design 03 A74).
- **Measured against:** agentgateway and agentgateway-crds **1.5.0** (controller image `cr.agentgateway.dev/controller:v1.5.0`, read back from the running Deployment), Gateway API **v1.6.0** standard, on k3d **v5.8.3** with its default k3s **v1.33.6-k3s1**, one server node and no agents, Docker 29.2.1, one gateway replica, on an otherwise idle arm64 laptop. The traffic Pod runs `curlimages/curl` 8.11.1's multi-arch image, natively.
- **Reproduce:** `make conformance-cluster`. The trials are `TestSliceAPublishedRouteLocksFrom200To401`, and each prints a `LOCK-LATENCY` line.
- **Re-verify:** whenever the e2e's agentgateway release moves, and by 2026-11-13 at the latest. `make test` enforces only part of the first: `TestTheSliceCasesRunTheE2EsAgentgatewayRelease` fails when `hack/e2e.sh`'s `AGW_VERSION` and `hack/conformance-cluster.sh`'s `SLICE_AGW_VERSION` differ. Nothing runs `make conformance-cluster` for you, in `make test` or in CI, so re-measuring is the upgrader's run.

## Method

Everything runs in the test's own namespace behind its own Gateway. The Gateway is shaped like `hack/e2e.sh`'s, with the serving listener named as the operator's routes name it (`http`, port 8080) and a second listener, `tools`, on 8081. No operator runs.

1. **The policy is the compiler's.** Each `<agent>-auth` is `compiler.AuthPolicy`'s output for an Agent in namespace `conf-team`: API-key authentication (`mode: Strict`, the constant key-source selector) and one CEL rule admitting group `conf-team`. It is exactly what the operator writes.
2. **The route is the emitter's shape**, written by the test: `<agent>-serving`, `parentRefs` to the `http` listener, the Agent's hostname, `PathPrefix: /`, and one `backendRef` to an `agnhost netexec` Service, which answers `200` at every path.
3. **Each trial is a new Agent**, with its own route and hostname, so no trial inherits another's configuration. The route must answer an anonymous `200` before the trial starts.
4. **A request loop runs inside the cluster**, from a Pod in the same namespace, through the Gateway's Service. Each `curl` process sends 500 requests through a URL glob, each with `Connection: close`, and the loop starts the next process until a stop file appears. Each answer is written to `curl`'s stderr, which is unbuffered, as its code and its `%{num_connects}`, and is stamped when it reaches the test through `kubectl exec`.
5. **The loop is checked, and a trial fails if the check fails.** The check fails if a request got no answer at all (`curl`'s `000`), which is a failure of the gateway, the network or the Pod and not a policy decision. It fails if a request reused a connection (`num_connects` of 0), which a policy applied per connection could hide behind. And it fails if more than half the answers arrived in runs of 32 or more within 50 µs of each other, which is what a buffered stream does. Measured by hand first, through Docker: the loop's own `curl` to stderr gave 15 of 499 answers inside 50 µs, and every request a connection of its own; to stdout, 496 of 499; and without `Connection: close`, 499 of 500 requests reused one. Through `kubectl exec` a few answers still arrive together now and then, because `curl` answers faster than the stream forwards single lines, but they are a small share of the answers.
6. **The check detects faults. It does not measure precision.** Each trial reports what the stream saw instead:
   - the **bracket**: from the arrival of the last `200` to the arrival of the first `401`, or from the write's start if no `200` arrived after it. The policy landed inside it, as the stream saw it;
   - the stream's **longest gap** between two answers.

   Both describe the stream around the landing. Neither is a bound on the gateway, because an answer is stamped when it arrives, not when it was answered.
7. **The write is one request.** The policy is created through client-go, on a connection warmed by a list just before, so the timing brackets that one request and nothing else. A random pause of up to 250 ms comes first, so the write does not start at a fixed point in the loop's rhythm.
8. **Latency is from the write's start to the arrival of the first `401`.** The API server commits somewhere inside the write, so this is an upper bound on the time from commit to enforcement. A `401` is accepted whenever it arrives, even before the write's response: no policy existed before the write started.
9. **Only `200` or `401` may appear.** Any other code while the policy lands fails the trial, because `Lock` keeps the route's `backendRefs` throughout (§3.3.3). After the first `401`, the next 20 answers must all be `401`, so a lock that flapped would fail. A key in the admitted group must still get `200` afterwards.

## Results

**Cited here: one run**, the `make conformance-cluster` run that closed the change, on 2026-09-13. Its lines are quoted exactly as the test printed them. Earlier runs gave figures of the same size, but their logs were not kept, so they are not cited.

```
slice_cluster_test.go:861: LOCK-LATENCY trial=1 from_write_start_ms=14.7 from_write_return_ms=11.9 write_ms=2.9 probe_interval_ms=0.25 bracket_ms=0.24 max_gap_ms=2.91
slice_cluster_test.go:861: LOCK-LATENCY trial=2 from_write_start_ms=14.4 from_write_return_ms=11.4 write_ms=3.0 probe_interval_ms=0.25 bracket_ms=0.29 max_gap_ms=0.76
slice_cluster_test.go:861: LOCK-LATENCY trial=3 from_write_start_ms=14.0 from_write_return_ms=11.1 write_ms=2.8 probe_interval_ms=0.21 bracket_ms=0.29 max_gap_ms=0.73
slice_cluster_test.go:861: LOCK-LATENCY trial=4 from_write_start_ms=14.4 from_write_return_ms=11.7 write_ms=2.7 probe_interval_ms=0.24 bracket_ms=0.26 max_gap_ms=3.06
slice_cluster_test.go:861: LOCK-LATENCY trial=5 from_write_start_ms=15.2 from_write_return_ms=12.0 write_ms=3.1 probe_interval_ms=0.23 bracket_ms=0.19 max_gap_ms=3.27
slice_cluster_test.go:861: LOCK-LATENCY trial=6 from_write_start_ms=14.7 from_write_return_ms=11.9 write_ms=2.9 probe_interval_ms=0.24 bracket_ms=0.26 max_gap_ms=3.42
slice_cluster_test.go:861: LOCK-LATENCY trial=7 from_write_start_ms=15.4 from_write_return_ms=12.5 write_ms=3.0 probe_interval_ms=0.24 bracket_ms=0.44 max_gap_ms=3.39
slice_cluster_test.go:861: LOCK-LATENCY trial=8 from_write_start_ms=14.3 from_write_return_ms=11.5 write_ms=2.9 probe_interval_ms=0.24 bracket_ms=0.30 max_gap_ms=0.67
slice_cluster_test.go:861: LOCK-LATENCY trial=9 from_write_start_ms=14.2 from_write_return_ms=11.5 write_ms=2.7 probe_interval_ms=0.24 bracket_ms=0.04 max_gap_ms=3.65
slice_cluster_test.go:861: LOCK-LATENCY trial=10 from_write_start_ms=16.1 from_write_return_ms=13.4 write_ms=2.8 probe_interval_ms=0.24 bracket_ms=3.17 max_gap_ms=3.17
slice_cluster_test.go:871: LOCK-LATENCY n=10 from_write_start: min_ms=14.0 median_ms=14.6 max_ms=16.1; from_write_return: min_ms=11.1 median_ms=11.8 max_ms=13.4; write: median_ms=2.9; probe_interval: median_ms=0.24; bracket: median_ms=0.28 max_ms=3.17; max_gap: median_ms=3.11 max_ms=3.65; deadline=4m0s
```

- **Latency:** 14.0–16.1 ms from the write's start, median 14.6 ms; 11.1–13.4 ms from its return, median 11.8 ms. Upper bounds.
- **Bracket:** 0.04–3.17 ms, median 0.28 ms. In nine trials the last `200` and the first `401` arrived within half a millisecond of each other. In trial 10 they were 3.17 ms apart, which is that trial's longest gap: the policy landed while the stream was quiet, and that trial's figure is late by up to that much.
- **Longest gap:** 0.67–3.65 ms per trial, which is the `curl` restart every 500 requests and the occasional slow request.
- **The open window.** After the write returned, the route stayed open to anyone for about 12 ms more, the from-return figure. Counting the `200`s in that window measures the loop's request rate, not the gateway, so it is not reported.

**The prepared route** (`TestSliceAPreparedRouteAnswers500Then401OnTheServingListener`), on the `http` listener, where the 2026-09-11 spike used `tools`. It answered `500` before the policy and `401` after it, and nothing else in between. An unknown key got `401`, and a valid key reached the missing backend and got `500`, as the spike found. The case logs how long the `401` took after the policy reported `Attached`. That is **not a latency**, and it is an upper bound only. The case probes once a second through `kubectl exec`, and the first probe after `Attached` was already refused, so the figure is one probe's round trip. It asserts only that the `401` arrives within the 4-minute deadline:

```
slice_cluster_test.go:476: prepared route: refused within 51ms of the policy reporting Attached (an upper bound: one probe round trip at 1 s cadence)
```

## What else the same run measured

These are design 03 §8.1's `cluster` cases that the first-slice bullet lists, and case 2, which §3.4.4 owes. Each is a test in `test/conformance/slice_*_cluster_test.go`, and each was killed by a mutation of its own (below).

- **Two groups (§8.1 case 2).** `compiler.AdmitGroupsExpression` renders `apiKey.group == "a" || apiKey.group == "b"`. With that rule, keys in `a` and `b` got `200`, a key in `c` got `403`, and anonymous got `401`.
- **A promotion (case 1, the gateway half).** The route was locked first. Then its one `backendRef` was moved from one revision's Service to the next, in place, and the per-Agent policy kept enforcing on the new revision. Every answer was read as it arrived. Not exercised: a `Lock` racing a promotion, and a weighted two-backend shift. That the operator emits exactly one `traffic` policy is the operator's, and envtest's to pin.

  ```
  slice_cluster_test.go:1035: promotion: 26999 anonymous requests across it, 26583 of them after the new revision was first seen, every one refused with 401
  ```
- **A key set in another namespace (case 3).** A `ConfigMap` with the key-source label in namespace `conf-elsewhere` held a key in the admitted group. Then a canary key set was written in the policy's own namespace, and the case waited for the gateway to admit the canary. After that, the other namespace's key got `401` five times out of five, and a key in the same group in the policy's namespace got `200`. Case 7 adds a second namespace: a Gateway-level policy in the Gateway's namespace refused, with `401`, a key stored only in the route's namespace. The canary's proof rests on the controller reading `ConfigMap` events in order, which one cluster-wide watch delivers; that is not measured separately.
- **A duplicate `keyHash` under a different group (case 4).** It is not refused. Two `ConfigMap`s, `conf-dup-a` and `conf-dup-b`, held the same hash under two groups. Each of 8 writes swapped the groups, and every second pair reversed the order the two were written in. Each write also added a canary key of its own to each `ConfigMap`, and was taken as landed only once both canaries were admitted. Then 30 answers were taken, and all 30 had to agree.
  - The key never got `401`. After every landed write it was admitted (`200`) or refused (`403`) as a member of **one** of the two groups, all 30 answers the same.
  - The run's lines:

    ```
    slice_keys_cluster_test.go:228: DUPLICATE-KEY write 1: a=conf-team b=rogue, written a then b: HTTP 403, so conf-dup-b won
    slice_keys_cluster_test.go:228: DUPLICATE-KEY write 2: a=rogue b=conf-team, written a then b: HTTP 403, so conf-dup-a won
    slice_keys_cluster_test.go:228: DUPLICATE-KEY write 3: a=conf-team b=rogue, written b then a: HTTP 403, so conf-dup-b won
    slice_keys_cluster_test.go:228: DUPLICATE-KEY write 4: a=rogue b=conf-team, written b then a: HTTP 200, so conf-dup-b won
    slice_keys_cluster_test.go:228: DUPLICATE-KEY write 5: a=conf-team b=rogue, written a then b: HTTP 200, so conf-dup-a won
    slice_keys_cluster_test.go:228: DUPLICATE-KEY write 6: a=rogue b=conf-team, written a then b: HTTP 200, so conf-dup-b won
    slice_keys_cluster_test.go:228: DUPLICATE-KEY write 7: a=conf-team b=rogue, written b then a: HTTP 403, so conf-dup-b won
    slice_keys_cluster_test.go:228: DUPLICATE-KEY write 8: a=rogue b=conf-team, written b then a: HTTP 200, so conf-dup-b won
    slice_keys_cluster_test.go:231: DUPLICATE-KEY winners by name map[conf-dup-a:2 conf-dup-b:6], by write order map[written first:6 written last:2]
    ```
  - Neither the name nor the write order explains every write. Earlier runs, whose logs were not kept, leaned towards `conf-dup-b` too and also split. In one of them, a mutation run in which the two groups never changed, only the canaries did, the winner still moved. So "which one wins" is not stated here as a rule, and the CRD calls it undefined. What is stated is that a duplicate can put the credential in either group at any write to either `ConfigMap`, with nothing reporting it.
  - What the committed assertion rules out: a gateway that refuses a duplicate, takes the union of its groups, or takes their intersection. It does not state which entry wins.
  - **An earlier version of this section is withdrawn.** It reported writes taken as landed after a ten-second sleep, with nothing proving it, so they may have measured writes that had not yet reached the gateway. An independent review of this change found it, and its own run with canaries saw `conf-dup-b` win 12 writes out of 12.
- **An explicit `mode: Strict` (case 5).** The compiler's policy and the same policy with `mode` removed gave the same four answers: anonymous `401`, unknown key `401`, admitted key `200`, wrong-group key `403`. The API server stored the second with `mode: Strict`.
- **A Gateway-level policy beside `<agent>-auth` (case 7).** A second Gateway, with a policy in its own namespace targeting the Gateway: API-key authentication over a key set in that namespace, admitting group `gwgroup`.
  - With **only** the Gateway's policy, a **prepared** route answered anonymous `401`. The Gateway's key reached the missing backend and got `500`. So `Create`'s probe cannot tell this `401` from its own policy's (design 03 §3.3.3, the sixth critique's MINOR 5).
  - With only the Gateway's policy, a published route admitted the Gateway's key (`200`), and refused the route's key as unknown (`401`).
  - With `<agent>-auth` on the route as well, the **route's policy decided alone**. The Gateway's key got `401`, the route's key `200`, and a wrong-group key `403`. A combination requiring both policies would have refused the route's key, which the Gateway's key set does not hold. The two policies are not merged: the route's overrides the Gateway's.
  - Only this shape was measured, with both `apiKeyAuthentication` and `authorization` set at both levels. A Gateway-level policy setting one of the two, and a `ListenerSet` target, were not.

## Mutations

Each case was run with a change that must make it fail, and each change was restored from a copy afterwards. INVALID means the mutant did not build, which proves nothing. No mutant was INVALID. The mutation runs' logs were not kept either; what each was killed by is recorded here as it was read at the time.

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

**The second batch**, after the first review:

| Case | Mutation | Result |
|---|---|---|
| published route | the policy targets a route that does not exist | killed: 344,001 anonymous `200`s over 4 minutes, and no `401` |
| published route | the route stripped of `backendRefs` as the policy lands | killed: an admitted key got `500` after the lock |
| promotion | promoted by writing a second route for the new revision | killed: anonymous request 95 got `200` |
| duplicate key | the key written in no selected `ConfigMap` | killed: `401` on all 30 answers |
| duplicate key | no conflicting group: both `ConfigMap`s give the admitted group | killed: admitted 8 times, refused 0 |
| duplicate key | the groups never swapped between writes | **survived**, correctly: the winner moved between writes anyway, so both outcomes still occurred |
| Gateway-level policy | no Gateway-level policy | killed: anonymous got `500` on the prepared route |
| Gateway-level policy | no `<agent>-auth` beside it | killed: the Gateway's key got `200` |
| Gateway-level policy | the route's key also stored in the Gateway's namespace | killed: it got `403`, not `401` |
| `AdmitGroupsExpression` | the empty-set check removed | killed: the error no longer said "the set is empty" |

`TestTheSliceCasesRunTheE2EsAgentgatewayRelease` was killed by moving `SLICE_AGW_VERSION` to 1.5.1.

**The third batch**, after the second review:

| Case | Mutation | Result |
|---|---|---|
| published route | the route loses its backend just before the policy write | killed by the only-`200`-or-`401` rule: an anonymous request got `500` |
| published route | the codes written to stdout again | killed: up to 171 answers in one write |
| published route | `Connection: close` removed | killed: 1,996 of 2,000 requests reused a connection |
| key set elsewhere | the canary written in the other namespace | killed: the canary was never admitted, `401` |

**The fourth batch**, after the third review, against the checks it changed:

| Case | Mutation | Result |
|---|---|---|
| published route | the codes written to stdout again | killed by the buffer check: 998 of 1,000 answers in runs of 32 or more |
| published route | `Connection: close` removed | killed: 1,497 of 1,500 requests reused a connection |
| promotion | the stream stopped at the promotion | killed: no answers after the new revision was first seen |

## Limits

- **One replica, one idle cluster.** Every latency is what one proxy took to receive its configuration, with nothing else happening. H2 (design 03 §3.3.3) is the rule for more replicas. It is not measured here.
- **Upper bounds only.** How tight each figure is, is not established: the stream's answers are uneven, and a policy landing in a gap is seen late by up to it. Each trial reports its bracket and its longest gap so the reader can judge.
- **No operator.** The operator's `Lock` adds status convergence (§3.3.2's tuple) and a 5 s probe cadence before its first probe. This measures only what those wait for.
