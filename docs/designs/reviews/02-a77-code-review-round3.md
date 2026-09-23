# Design 02 A77: third independent code review (PR #54, head `ddaf805`)

**Scope:** `git diff 425bd59...ddaf805`, with the focus on `da320ef` (the coder's answer to round two) and `ddaf805` (five `reasons.yaml` entries and the `ServiceRejected` rewrite).

**Verdict: REVISE.** There is no BLOCKER and there are seven MAJOR findings.

The delete itself held. It is gated on the stamp, runs with a UID precondition, dry-runs first, and never returns success after a lost race. Of the eight round-two findings, the code parts of five are closed and pinned. The doc parts of three are not.

**Gates at `ddaf805`:**

- `make test` exits 0 (envtest took 307.7 s).
- `make verify` exits 0.
- `make e2e` was **not run**, because the shared-kubeconfig bug (PR #61) is not merged.
- The path and name scan is clean over every commit and over the patches.
- All five commits have `Quinyte Engineer <engineer@quinyte.com>` as both author and committer.

## Round-two findings: status

| # | Finding | Status | Evidence |
|---|---|---|---|
| 1 | Live read authorises the delete | **Code closed, pin missing** | See MAJOR 3. |
| 2 | M8, M9, M11 and M12 are pinned | **Closed** | See the table below. |
| 3 | The absolute "never deleted" claim is retracted | **Partly closed** | The code comments and `:149` say the truth now. `:486` and `:1075` still bound the wedge wrongly (MAJOR 1). |
| 4 | The not-ours refusal reads `Unstamped`, not `ReplaceHeld` | **Closed** | See the note below. |
| 5 | The `ServiceRejected` create remedy | **Closed in code** | Both arms were measured with a real VAP. The update arm is still unpinned (MAJOR 5). |
| 6 | The owner-edit escape is reported | **Reported, but the remedy is wrong** | See MAJOR 4. |
| 7 | `loadBalancerIP` is not claimed as auto-cleared | **Closed** | Checked at `:478`, `:1048` and `service.go:287`. |

**Mutations for item 2.** Each mutation was applied to a copy of `service.go`, then restored from the backup and checked by SHA-256 against HEAD. Each passed `go vet` and was run against every test in `service_test.go`.

| Mutation | Result | Killed by |
|---|---|---|
| M8 | killed | `TestTheOperatorsOwnServicePatchedInPlaceIsConvergedBack` |
| M9 (a Conflict from the precondition returns nil) | killed | `TestTheReplaceDeletesTheObjectItReadAndNotTheName` |
| M11 | killed | `TestAFutureReplaceTimeDoesNotHoldTheReplace` |
| M12 | killed | `TestAVanishedServiceDoesNotLetTheReplacePassAsSuccess` |

My first form of M12 put code after a `return`, and `vet` refused it. It was counted as INVALID and reshaped.

**The discriminator for item 4.** Only two sites return a `serviceShapeError` as an error:

- `service.go:242` sets `unowned: true`.
- `service.go:388` is the held case, and sets `heldSince`.

`serviceNotRendered`'s instance only rides as `shape` detail on `revisionCollisionError`. No construction site leaves `unowned` unset where it should be set. The opposite problem exists instead: MAJOR 1 shows `unowned` set on the operator's own object, which `status` vouches for.

## MAJOR

### 1. `services/patch` alone reaches the permanent `Unstamped` wedge on the operator's own Service

**Where:** `service.go:241-246`. Design `:149` says "rewritten and never destroyed". Design `:486` says the wedge needs "a principal with `services/create`". Design `:1075` says `vouched` exists so that a `services/patch` holder cannot "strip the stamp and wedge any Agent terminally".

**State:** an operator-created Service, then two strategic-merge patches with no create:

1. `{"metadata":{"annotations":{"assayd.dev/revision-digest":null}},"spec":{"type":"ExternalName","externalName":"elsewhere.example.com"}}`
2. `{"spec":{"type":"ClusterIP","externalName":null,"clusterIP":"None","clusterIPs":["None"]}}`

**Measured over 5 passes:** `sameUID=true clusterIP="None" stamped=false uidLabel=true phase=Degraded ready=False/Unstamped`. The requeue stayed at 1m forever. The route was still published, with its backendRef naming the Service. The message says the operator "cannot establish that it created it", but it did create it, with the same UID, and `status` vouches for it.

This is the terminal per-Agent outage that the 2026-09-22 delete-and-recreate decision was taken to remove, reached with `services/patch` alone.

**Fix.** Record the UID of the Service the operator creates, at create and at replace, in `status`. Authorise the delete on `existing.UID == status.<recordedUID>`. That is real proof of creation. It closes this wedge and also the forged-stamp delete. Otherwise, correct `:149`, `:486` and `:1075`, and take the premise back to the human.

### 2. `da320ef` broke design 02's §5 table

**Where:** `02:476` is a blank line inside the "Early exits" row. `:477` then begins a paragraph.

**Measured with `pandoc -f gfm`:** at `425bd59`, "Gateway resources for an external Agent" renders in a `<td>`. At `ddaf805` it renders inside `<p>` as `| | <strong>Gateway resources…`. Every later §5 row renders as raw pipe text, including the DELETE, name-window, owner-edit and `Unstamped` rows.

**Fix:** remove the blank line. Keep the paragraph inside the cell with `<br><br>`, or move it to §12.

### 3. The live read is pinned by nothing, and its comment says no test can see it

**Where:** `service.go:122-123` ("No test here can see the difference").

**Production wiring:** `cmd/operator/main.go:191` passes `mgr.GetAPIReader()`, and `NewAgentReconciler` refuses a nil reader. The read is genuinely uncached in production.

**Why a test can see it:** `r.Reader` is independent of `r.Client`, and the suite already sets it separately (`authabove_test.go`).

**Measured:**

- **Setup.** `Client` answers the Service Get with a stale headless snapshot, and `Reader = k8s`. A human repairs the Service in place, keeping the same UID.
- **At HEAD:** the repair survives.
- **Mutated** (`r.reader().Get` → `r.Get`, which vets): FAIL with "a STALE cached read … deleted the human's in-place repair (cbbea876… -> 9f027147…)".

**Fix:** commit that test and correct the comment.

### 4. The `ActiveRevisionServiceUnaddressable` remedy makes things worse

**Where:** `agent_controller.go:1390-1396` ("To recover: delete %s/%s …").

**Measured by following it** (delete the active Service without reverting):

- **Gateway on:** `phase=Pending ready=False/RouteApplyFailed` on every pass ("read the serving revision's Service … not found").
- **Gateway off:** `phase=Ready ready=True/Available` with **no Service at all**, and nothing reports it. That is NFR-8.

After a revert, the operator replaces the Service itself (it is stamped and desired again), so the delete is never needed. "Promote a new revision" is not an action a user has.

**Fix:** tell the reader to revert, or to wait for the new revision to become available, and never to delete. Assert that the message does not say "delete".

### 5. `ServiceRejected` is described as measured, but the update arm, the requeue and the carry are unpinned

**Where:** design `:475` ("both are measured") and `:477` ("the requeue and the remedy are measured"). The only committed test, `TestARefusedServiceCreateSaysThereIsNothingToDelete`, drives the create arm through a fake client, not through a VAP.

**My real-VAP probes.** The policy is "every Service must carry a cost-centre label", with Deny, scoped to the run namespace:

- **Create**, where the Service was deleted after the policy became active: `Degraded`, `ServiceRejected`, requeue 1m. The message reads "…could not CREATE this revision's Service, so there is no such object to delete…". Correct.
- **Update**, where `externalIPs: [203.0.113.9]` was patched and then the policy installed: `Degraded`, `ServiceRejected`, requeue 1m, and `externalIPs` still standing. The message reads "…could not repair its own Service in place. To recover: delete that Service…". Correct.

The fork's mutations X5, X19 (update arm given the create remedy), X15 (drop `RequeueAfter`) and X16 (drop the carry) all survived the full suite.

**Fix:** commit the update-arm test and assert `RequeueAfter`. Otherwise, correct `:475` and `:477`.

### 6. A refused Service `Get` is reported as `ServiceRejected` with the update remedy (rule 8)

**Where:** `service.go:143` wraps the Get error. `rejectedByAPIServer` accepts any APIStatus that is not transient, and `created` is false, so the pass takes the update message.

**Measured:** `Reader` returns Forbidden for the Service Get. The result is `ready=False/ServiceRejected` with the message "get service fget-…: … is forbidden: <nil>. This operator could not repair its own Service in place. To recover: delete that Service…". Nothing was written. Deleting would not help, because the operator cannot read.

This is newly reachable in this round: the live read needs RBAC `get` on services rather than the informer's list and watch.

The reference entry is wrong too. `ServiceRejected.state` says "answered a create or an update".

**Fix:** handle a Get error separately. Either return it bare, or report it with its own message that names RBAC. Correct the entry to match.

### 7. Design §5 and §12 still contradict the code, and round-two items were dropped silently

- **`:473`** still carries the repudiated "they change which of those Pods, or by what path" and "those five fields", in the same row that says the sentence was removed.
- **`:478`** omits `internalTrafficPolicy` from the enumerated set. The stranded "(A77) Its carry rides…" sentence is still there.
- **`:474`** says the active revision's Service "is never re-read … reported by nothing". It is now read, and it is reported when `clusterIP` is empty or `None`. The truth sits between the two rows. The report covers only ClusterIP. A selector, ports or `internalTrafficPolicy: Local` on the active revision, which `:473` itself calls a black-hole, is still reported by nothing.
- **`:485`** cites `TestAnOwnerEditEscapesOverABrokenActiveRevision` "asserts the defect". The test is now `TestAnOwnerEditOverABrokenActiveRevisionIsReported`, and it asserts the report.
- **`:1089`** says "five fields repaired".

## Your `reasons.yaml` entries

| Reason | Verdict |
|---|---|
| `ActiveRevisionServiceUnaddressable` | **Mostly true.** It reads live through `r.reader()`, writes nothing, and continues to `reconcileGateway`, so `traffic: not-withdrawn` is right. Two problems follow the table. |
| `ForeignObject` | **One false sentence.** The state says it "carries a different Agent's `agent-uid` label", but the code (`service.go:163`, `agent_controller.go:1695`) fires on *any* mismatch, including **no label at all**, which is the commonest plant. The code's own message is correct: "does not carry this Agent's UID". Suspicion (a) holds: the workload exit (`agent_controller.go:600`) returns `ctrl.Result{}` with no `RequeueAfter`. A `Deployment` watch through `byAgentLabels` can still enqueue on an event, so "schedules" is the accurate word. |
| `Unstamped` | **Suspicion (b) is confirmed: both paths exist.** On the collision path, `reportCollision` puts this reason on `RevisionHashCollision`, and `Ready` and `Degraded` read `RevisionHashCollision`. On the Service shape path (`service.go:242`), `Ready` and `Degraded` read `Unstamped` (measured). The sentence "cannot establish that it created it" is true of the code, but MAJOR 1 shows it can be the operator's own object. |
| `RevisionServiceReplaceHeld` | **True.** It matches `service.go:386-392` and returns before the gateway step. |
| `RevisionServiceReplaceFailed` | **Incomplete.** The state lists 2 of 4 stages. It omits the dry-run refusal ("would not be admitted"), which is the likeliest stage and deletes nothing, and a delete failure other than the precondition. The note "may name no object" is false for the dry-run arm, where the headless object still stands. |
| `ServiceRejected` | **Suspicion (c).** The excluded list matches `rejectedByAPIServer` exactly: Conflict, NotFound, AlreadyExists, ServerTimeout, Timeout, TooManyRequests, InternalError, ServiceUnavailable and UnexpectedServerError. The positive side reads as exhaustive ("an Invalid error, or a Forbidden from an admission policy or webhook") but is **any other APIStatus**, including RBAC Forbidden and Unauthorized. It also says "a create or an update", while a Get reaches it (MAJOR 6). |
| `RevisionHashCollision` (not yours, but now stale) | **Stale.** It still says "The same state as `DigestMismatch`" and "while `RevisionHashCollision=True` carries `DigestMismatch`". Since A77 that condition also carries `ForeignObject` and `Unstamped`. |

**Problems with the `ActiveRevisionServiceUnaddressable` entry:**

- "nothing the serving route sends to it arrives" is not established for `ExternalName`. I measured `clusterIP=""` on an ExternalName Service, both created directly and patched, so the check does catch it. Where the gateway sends such traffic is unknown.
- `Ready` can be displaced on the same pass by a non-transient `reconcileGateway` error (`agent_controller.go:995`). Probe C showed this.

## MINOR

1. **The `routeNames` note** (new in this PR, `agent_controller.go:1234`) keys off auth state, not the route. In probe A the route was published with `backendRefs` while the `Create` was at `Publishing`, and the message omitted "The route is not withdrawn".
2. **A misplaced comment.** The "one shape fault convergence cannot repair…" comment now sits above the `serviceReplaceError` branch (`agent_controller.go:640-645`). The `servedRouteNote` comment quotes a subjunctive that `shape.Error()` no longer contains.
3. **An unrecorded precedence change.** `Ready=False` is set before `reconcileGateway`, so `withholdReady` skips. On that pass `Ready` reads this reason and not A81's `ServingRouteNotAccepted`. That changes what approved design 03 says, and it is not recorded. This was reasoned from the code, not measured.
4. **Round-two minors not answered,** per the fork: no ClusterIP check after a replace; no CRD description on `serviceReplacedAt`; "terminal by design" at `service.go:466`; `PreferClose` at `:473`; "forever" at `:1106`.
5. **Fork survivors:** X8 (the `Degraded` condition at `agent_controller.go:961` is unasserted), X10 (the empty-ClusterIP arm is unpinned) and X9 (pinned Agents skip the read-only check).

## Probe files

These are scratchpad files and none is committed:

- `pr54-r3-probe_test.go.txt` (probes A to D)
- `pr54-r3-vap_test.go.txt`
- `pr54-r3-mut.py`
- `pr54-r3-runmut.sh`
- the gate logs `pr54-r3-make-{test,verify}.log`
