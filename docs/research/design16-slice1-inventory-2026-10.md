# Design 16 §1.1, against the code: what is built and what is owed

**Taken 2026-10-12, against `main` at `a831ee8`**, for issue #82 (ADR-0030 build step 4). It reads design 16 §1.1 — the first slice, the only part of design 16 the human has approved (2026-09-14, ADR-0024 Amendment 1) — as amended through A19, whose one question the human answered on 2026-09-17, option (a). Nothing else in design 16 is inventoried, because nothing else may be built.

Each item is **BUILT**, **PARTLY BUILT** or **NOT BUILT**, with the evidence: a `file:line`, a test name, or the search that found nothing. A search for absence was run over `api/`, `internal/`, `cmd/`, `charts/`, `test/` and `hack/`. Rows are grouped by where the work lands, not in §1.1's order; each names the §1.1 passage it comes from.

## Summary

- **The slice is not built.** No `EvalSuite` Go type, no CRD, no eval controller, no `status.eval` field beyond the five design 02 already had (design 02 A50 added `revisionDigest`), and no A2A client in the operator exist. The gate branch still holds every gated candidate with no exit, `GatesPassed=False/GateControllerUnimplemented` (`internal/controller/agent_controller.go:2016`).
- **What §1.1 builds on is built.** Discovery of the EvalSuite CRD, the hold-on-unsatisfied-gate branch and its `active != desired` guard, the I1 hold, design 03's `Lock`, `Create`, re-point and route gate, the card record, and the e2e harness's CRD apply all exist and are tested. Every design 03 identifier §1.1 cites resolves in the code.
- **One owed item is already built**, and §1.1 still says it is owed: the card fetch refuses redirects (`internal/controller/card.go:265`, PR #38, merged 2026-09-14). See "§1.1 text the code has overtaken".
- **Of the 69 owed test rows, none is built.** Three have a partial precursor that a new row can extend.
- **The open points are settled but one.** The human decided two on 2026-10-13, the coordinator decided when the EvalSuite CRD ships, and the review of this inventory found that a reason list already exists. One stays owed: correcting §1.1's stale redirect sentences (#101). See "Decisions, and what stays owed", last.

## What §1.1 relies on, already built

| Item | Status | Evidence |
|---|---|---|
| `spec.gates` and `GateRef{evalSuiteRef}` on the Agent | BUILT, uncapped | `api/v1alpha1/agent_types.go:75-79`, `:429-432`; the CRD carries no `maxItems` (`charts/assayd/crds/assayd.dev_agents.yaml:240-253`) |
| `gates` on the policy surface, so adding one mints nothing | BUILT | `internal/revision/leaves.go:271` (`spec.Gates[].EvalSuiteRef` is `inPlace`) |
| `status.eval` with `score`, `suite`, `revision`, `revisionDigest`, `at`, and the `Eval` printer column on `score` | BUILT | `api/v1alpha1/agent_types.go:759-783`, `:1004` |
| Discovery of the EvalSuite CRD, TTL-cached, failing toward "installed" | BUILT | `internal/controller/discovery.go:46-155`; `TestEvalSuiteDetectorSeesTheCRDAppear`, `TestEvalSuiteDetectorHoldsWhenDiscoveryFails`, `TestEvalSuiteDetectorFindsTheCRDAtAnyVersion` (`test/envtest/discovery_test.go`) |
| `assessGates`: `GatesSkipped` `NoGatesDeclared` / `EvalSuiteCRDAbsent`, `GatesPassed=False/GateDetectionUnwired` | BUILT | `internal/controller/agent_controller.go:1997-2019` |
| The rollout switch's order: not-ready branches, the I1 hold, the gate, promotion; the gate's `active != desired` guard | BUILT | `internal/controller/agent_controller.go:927-985`; `TestAddingGatesToARunningAgentDoesNotHoldIt`, `TestHoldingOnGatesKeepsAServingAgentReady` (`test/envtest/reconciler_blockers_test.go`) |
| `GatesPassed` sticky and owned | BUILT | `internal/controller/conditions.go:71`, `:131` |
| The Agent reconciler's status write: a full `Status().Update` at the read resourceVersion, early return on an equal status, `status` started from a copy of what was read so `status.eval` is carried | BUILT | `internal/controller/agent_controller.go:440`, `:2311-2327` |
| The Agent reconciler has one worker: no `MaxConcurrentReconciles` anywhere | BUILT (as §1.1 states it) | `grep -rn MaxConcurrentReconciles internal cmd` finds nothing |
| Design 03's reads and transaction functions §1.1 names: `gatewayAuthPolicies`, `stands`, `foreignTrafficPolicies`, `servingRouteLabels`, `isReCreation`, `cardAttributes`, `cardedRevision`, `missingPolicyRouteNote`, `runLock`, `lockMissingPolicy`, `enterLock`, `recreateRoute`, `refuseAdopt`, `persistStatus`, `requireFreshAgent`, `withholdReady`, `authHoldsPromotion`, `currentServingRoute`, `liveServingRoute`, `reader` | BUILT | `internal/controller/authabove.go:101`, `:199`; `authforeign.go:37`, `:107`; `card.go:405` (`hasCardFor`); `authtxn.go:917`, `:1111`, `:1153`, `:1194`, `:1716`, `:1730`, `:1759`, `:2123`, `:2358`, `:2384`, `:2468`; `agent_controller.go:1144`, `:1165`; `httproute.go:318`, `:338`; `runnamespace.go:847` |
| The constants §1.1 binds to: `AuthProbeInterval` 5 s, `AuthTransactionDeadline` 4 min, `CardRetryInterval` 15 s, `CardDriftInterval` 5 min, `DefaultRevisionHistoryLimit` 2, and `AgentReconciler.AuthDeadline` as the test seam | BUILT | `internal/controller/authtxn.go:46`, `:50`; `card.go:81`, `:336`; `agent_controller.go:122`, `:179-181` |
| The card record and its re-read: `upsertCard`, `cardFetchDue`, `recordCardAttempt` | BUILT | `internal/controller/card.go:321`, `:354`, `:412` |
| The test seams §1.1 models the eval client on: `CardClient`, `AuthProbe` | BUILT | the `AgentReconciler` fields `CardClient` (`internal/controller/agent_controller.go:161-164`) and `AuthProbe` (`agent_controller.go:176-178`); the real clients they replace, both refusing redirects, are `cardHTTPClient` (`card.go:265`) and the prober's (`authprobe.go:107`) |
| The re-point ending the wedge with nobody acting, from design 03's side | BUILT | `TestACandidatesPromotionEndsTheWedgeWithNobodyActing` (`test/envtest/authgate_test.go:248`) |
| The e2e harness applies `charts/assayd/crds/`, so a CRD added there reaches the e2e | BUILT | `hack/e2e.sh:552-558` |
| `k8s.io/apiextensions-apiserver` a direct dependency; `get` on `customresourcedefinitions` | BUILT | `go.mod:11`; `internal/controller/agent_controller.go:362` |
| `get` on the Gateway and `list` on `agentgatewaypolicies` | BUILT | `charts/assayd/templates/rbac.yaml:79-89`; `internal/controller/agent_controller.go:322` |
| A2A 1.0 `POST /message:send` on `test/responder`, answering `echo: <text>` | BUILT | `test/responder/main.go:172`, `:262` |

## The `EvalSuite` kind (§1.1 "The EvalSuite, in the slice", Q7, Q8)

| Item | Status | Evidence |
|---|---|---|
| The `EvalSuite` Go type: `dataset.cases[]{id, message, expect{contains\|equals}}`, `metrics[]{name, threshold}`, `caseTimeoutSeconds` | NOT BUILT | `grep -rn 'type EvalSuite' api` finds nothing; `agent_types.go` is the only type file in `api/v1alpha1/` |
| Its CEL rules: 1–20 cases; `id` a DNS label, unique; `message` 1–4096; exactly one of `contains`/`equals`, 1–512; the echo refusal with its `has()` guards; at most one metric, `task_completion` only; `threshold` a decimal string in (0, 1], three places; `caseTimeoutSeconds` 1–10, default 10 | NOT BUILT | as above |
| The generated CRD, shipped in `charts/assayd/crds/` (Q7 (a)) | NOT BUILT | `charts/assayd/crds/` holds `assayd.dev_agents.yaml` only |
| The suite digest: SHA-256 of canonical JSON of `spec`, defaults applied, keys sorted, cases sorted by `id` | NOT BUILT | no EvalSuite type to digest |
| `spec.gates` capped at one by CEL, with a refusal that names the fix (Q4 (a)) | NOT BUILT | no `maxItems` or rule on `gates` (`charts/assayd/crds/assayd.dev_agents.yaml:240-253`) |
| The `GateRef` comment's unenforced "At least one is required in prod" corrected | NOT BUILT | `api/v1alpha1/agent_types.go:76`, rendered at `charts/assayd/crds/assayd.dev_agents.yaml:243` |

## `status.eval` (§1.1 "Status, conditions and phase")

| Item | Status | Evidence |
|---|---|---|
| `suiteDigest`, `endpointPath`, `cardDigest` | NOT BUILT | `EvalStatus` has five fields (`api/v1alpha1/agent_types.go:761-783`) |
| `blocked{reason, message}`, `blockedFor{revisionDigest, suiteDigest, lockDeadline}` | NOT BUILT | as above |
| `verdict` (`Running`, `Passed`, `Failed`) | NOT BUILT | as above |
| `cases[]{id, outcome, reason, claim}`, at most 20, sorted by `id`, `Sending` outcome | NOT BUILT | as above |
| `failed[]{revisionDigest, suiteDigest}`, at most 10, oldest evicted | NOT BUILT | as above |

## The Agent reconciler (§1.1 "Promotion", "Status, conditions and phase", step 11)

| Item | Status | Evidence |
|---|---|---|
| The gate branch answering from the four promotion conditions (verdict `Passed`; `revisionDigest` equals the desired digest; `suiteDigest` equals the suite read this pass; a card recorded for the desired digest) | NOT BUILT | `gatesSatisfied` returns `len(agent.Spec.Gates) == 0` once the CRD is installed (`internal/controller/agent_controller.go:1981-1986`) |
| Retiring `GateControllerUnimplemented` | NOT BUILT | `internal/controller/agent_controller.go:2016`; asserted by `TestDeclaredGatesHoldWhenNoGateControllerExists` (`test/envtest/reconciler_test.go:284`), which must be rewritten with it |
| The suite read uncached through the API reader, giving `EvalSuiteNotFound` and `EvalSuiteUnreadable` | NOT BUILT | no EvalSuite read anywhere |
| The `GatesPassed` reasons set in the rollout switch: `EvalRunning`, `EvalFailed`, `EvalPassed`, `EvalPaused`, `EvalHeldForAuth`, `EvalWaitingForAuth`, `EvalAwaitingCard`, `EvalBindingUnsupported`, `EvalPairAlreadyFailed`, `EvalRecordNotKept`, `EvalRecordUnverifiable`, `EvalSuiteNotFound`, `EvalSuiteUnreadable`, `ActiveRevisionNotEvaluated` | NOT BUILT | `grep -rn` for each reason over `internal` finds none; `EvalWaitingForAuth` appears only in a comment of `test/envtest/service_test.go:1867` |
| `EvalWaitingForAuth`'s message: transaction kind, stage, deadline passed; clause 5's `blocked.message`; the "not yet evaluated" text when `blockedFor` does not match; and, for the missing-policy wedge, R's name from the route's single `backendRef` and the exits, under "When it names the exits"' three clauses | NOT BUILT | — |
| `blocked` applied only while `blockedFor` matches on all three parts | NOT BUILT | — |
| `GatesPassed=False/NoGatesDeclared` when a gate is removed from an Agent that had `GatesPassed`; `GatesSkipped=True/NoGatesDeclared`'s message naming an overridden `Failed` or `Running` verdict (Q9 (a)) | NOT BUILT | `assessGates` sets `GatesSkipped` only (`internal/controller/agent_controller.go:2003-2005`) |
| The Agent CRD schema check: `apiextensions.k8s.io/v1` in the operator's scheme, the CRD read uncached behind a TTL cache, each of the eight fields checked under `status.eval`, `cases` items' `claim` included | NOT BUILT | the scheme registers client-go, assayd and Gateway API only (`cmd/operator/main.go:42-49`) |
| The pin rule on a gated Agent (Q5 (a)): refuse the current candidate's digest, and a digest in `failed[]` with the current suite digest; admit when no suite digest is current; refusal through the unresolvable-pin path as `PinnedRevisionNotEligible`; an admitted pin promotes with `GatesBypassed=True/PinnedRollback`; ungated Agents unchanged | NOT BUILT | `resolveReleasePin` checks selection only (`internal/controller/release.go:75-110`); the refusal path sets `Degraded=True/ReleasePinUnresolvable` (`release.go:110-123`); the gate branch has no pin input (`agent_controller.go:941`), so a pinned rollback on a gated Agent holds with no exit today, as §1.1 says |
| `authHoldsPromotion`'s comment that a pin "selects a revision that already served" corrected | NOT BUILT | `internal/controller/agent_controller.go:1139-1143` |

## The eval controller (§1.1 "Who runs the eval", "The two controllers", "How a run works")

| Item | Status | Evidence |
|---|---|---|
| A second controller in the manager, `For(&Agent{})` with no generation predicate, `MaxConcurrentReconciles: 2`, leader-gated, writing only `status.eval` by merge patch under `MergeFromWithOptimisticLock`, reading the Agent live before every write | NOT BUILT | the manager runs the Agent reconciler (`internal/controller/agent_controller.go:2434`) and the run-namespace reconciler (`:2501`) only |
| The shared send predicates, implemented once and called by both controllers, including `observedGeneration == generation` (eval controller only) | NOT BUILT | — |
| Run start: replaces the previous pair's record; sets `verdict: Running`; clears `score`, `at`, `cases[]`; keeps `failed[]`, `blocked`, `blockedFor` (step 1) | NOT BUILT | — |
| The run-start card fetch through the eval client, matched to `status.cards[]`, the `HTTP+JSON` 1.0 interface chosen and the path joined; `EvalAwaitingCard` with its two messages, `EvalBindingUnsupported` (step 2) | NOT BUILT | — |
| The A2A client: to the candidate Service's ClusterIP and `spec.runtime.port`, no redirect, `Proxy: nil`, `A2A-Version: 1.0`, `ROLE_USER`, fresh `messageId`, no `contextId`, no `configuration`, 1 MiB cap, `caseTimeoutSeconds` deadline, an injection seam (steps 3–5) | NOT BUILT | the redirect-refusing transport it can reuse exists (`directTransport`, `refuseRedirects`, `internal/controller/card.go:265`) |
| The outcome table and the integer threshold rule (steps 6–7) | NOT BUILT | — |
| `failed[]` and `EvalPairAlreadyFailed` (step 7, Q3 (a)) | NOT BUILT | — |
| Pausing on an unavailable candidate, resuming by `id` (step 8) | NOT BUILT | — |
| One case per pass, `RequeueAfter: 5s` backstop (step 9) | NOT BUILT | — |
| Read, claim, send, record; claim on `context.WithoutCancel`, `ctx.Err()` check before the claim, 5 s / `caseTimeoutSeconds` / 5 s bounds; record re-applied only while the claim stands and `outcome` is `Sending`; `Interrupted` for an orphaned claim, itself waiting on the no-transaction predicate (step 10, Q11 (a)) | NOT BUILT | — |
| Read-back of every run-start, `blocked`/`blockedFor` and claim write; on a missing field send nothing and return `CardDriftInterval` (step 11) | NOT BUILT | — |
| Waiting on any recorded `Create` or `Lock`; claiming beside a refused `Adopt` and beside an exempt J2/K2 `Lock` (clauses 1–4 from status, clause 5 from the Gateway and two policy lists); `blocked` computed once per pass in its precedence, written only on change (Q12 (a), Q13 (a), A19's clause 4 by revision name) | NOT BUILT | — |
| One requeue field whose zero value resolves to `AuthProbeInterval`, `CardRetryInterval` or `CardDriftInterval` per branch | NOT BUILT | — |
| `gatewayAuthPolicies` and `foreignTrafficPolicies` moved, bodies unchanged, onto a receiver both controllers can use, carrying the client, the uncached reader and the `GatewayConfig` | NOT BUILT | both are `*AgentReconciler` methods (`internal/controller/authabove.go:199`, `internal/controller/authforeign.go:37`), as is `servingRouteLabels` (`authforeign.go:107`) and the `listPolicies` both call (`authforeign.go:82`) |

## The operator process and the chart (§1.1 "What the slice depends on", design 07's items)

| Item | Status | Evidence |
|---|---|---|
| `GracefulShutdownTimeout` set explicitly to 30 s, with a unit test that it is at least 25 s | NOT BUILT | `ctrl.Options` at `cmd/operator/main.go:153-165` sets no `GracefulShutdownTimeout` |
| A chart test that keeps `terminationGracePeriodSeconds` at 25 s or more | NOT BUILT | the value is 30 (`charts/assayd/templates/operator.yaml:55`); `grep -rn terminationGrace test/` finds nothing |
| RBAC: `get` on `evalsuites`, and nothing more | NOT BUILT | no `evalsuites` marker among `internal/controller/agent_controller.go:287-362` |
| The Q6 (c) admission policy: a `ValidatingAdmissionPolicy` admitting an EvalSuite create or update only from a principal the `authorizer` says may `update` `agents` in that namespace, `failurePolicy: Fail`, a `Deny` binding | NOT BUILT | `charts/assayd/templates/admission.yaml` carries `assayd-namespace-labels`, `assayd-gateway-routes`, `assayd-gateway-policies` and `assayd-api-keys` only |
| The e2e harness applying the EvalSuite CRD | PARTLY BUILT | the harness applies everything in `charts/assayd/crds/` (`hack/e2e.sh:552-558`), so it is built the day the CRD lands there |
| The Agent CRD's new status fields applied on upgrade | PARTLY BUILT | the same apply covers the e2e; for a real `helm upgrade` nothing applies them, and §1.1 owes that to design 07 beside Q7 |
| Design 07 A5.4's amendment: the operator's ingress to agent Pods carries eval tasks, and "the eval runner arrives through the gateway" is no longer the premise | NOT BUILT | `docs/designs/07-umbrella-chart-ci.md:174` still says the eval runner's route "arrives through the gateway and needs no rule of its own". No code changes: the operator materialises no NetworkPolicy. Tracked, with design 07's other text changes, in #100 |
| `test/responder` env knobs: one changing the answer's prefix, one echoed into the task's metadata | NOT BUILT | the prefix is the literal `"echo: "` (`test/responder/main.go:262`); the `env()` helper it would use exists (`:488`) |
| Design 02's own text — §3.3's rollout steps 3–5, and `status.eval`, `spec.gates`, the gate branch, Q10 and Q5 as design 02 statements | NOT BUILT | §1.1 says it does not edit design 02, and design 02 is not approved. The human decided on 2026-10-13 that §1.1's approval covers these changes and that the text is tracked as a follow-up: #99 |
| "The one named vocabulary list" of reasons, with its check | BUILT (the mechanism) | `internal/refgen/reasons.yaml` (PR #59; 74 entries, `EvalSuiteCRDAbsent` and `GateControllerUnimplemented` among them), joined with the reasons the code sets by `internal/refgen/extract.go`; a reason the code sets with no entry, or an entry no code sets, fails `make reference` and so `make verify`. Each new gate reason needs its entry in the step that adds it (steps 5 and 8), and retiring `GateControllerUnimplemented` removes its entry. An earlier draft of this inventory said no reason list existed; the review of PR #98 found this one |

## The tests §1.1 owes

None of the 69 rows of "Tests the slice owes" is built; a `grep` for each row's subject in `test/` and `internal/` finds nothing. The table numbers the rows in §1.1's order and says which build step below each belongs to. Three rows have a precursor:

- **T27** (the CRD installed after the manager starts): `TestEvalSuiteDetectorSeesTheCRDAppear` proves the detector, not a manager with a gated Agent.
- **T63** (a gate added over a serving revision): `TestAddingGatesToARunningAgentDoesNotHoldIt` proves the no-hold half, not `ActiveRevisionNotEvaluated`.
- **T12** (the collision pair): the pinned pair is `internal/revision/collision_test.go`'s, and `test/envtest/collision_test.go` covers the workload guard, not a verdict.

| # | Layer | Row | Step |
|---|---|---|---|
| T1 | unit | the outcome table, one case per row | 3 |
| T2 | unit | the threshold boundary, `"0.666"` and `"0.667"` | 3 |
| T3 | unit | the suite digest's stability and sensitivity | 1 |
| T4 | unit | the request's header, role, `messageId`, no `contextId`, no `configuration` | 3 |
| T5 | unit | the endpoint join, and the card host never dialled | 3 |
| T6 | unit | a `307` is `ProtocolError` and is not followed | 3 |
| T7 | unit | a timeout is `Unreachable` within the bound | 3 |
| T8 | unit | the 1 MiB cap | 3 |
| T9 | unit | the echo refusal | 1 |
| T10 | envtest | a second revision holds `EvalRunning`, promotes only on `Passed` | 6 |
| T11 | envtest | a failing case holds with `EvalFailed`; no re-send | 6 |
| T12 | envtest | the collision pair does not promote | 5 |
| T13 | envtest | a stale suite digest promotes nothing and a new run starts | 6 |
| T14 | envtest | the card cleared after the last case: `EvalAwaitingCard` | 5 |
| T15 | envtest | finality, and the held first case of the re-run | 6 |
| T16 | envtest | the `failed[]` cap | 6 |
| T17 | envtest | a spec edit abandons the run | 6 |
| T18 | envtest | `EvalPaused`, and resuming at the second case | 6 |
| T19 | envtest | the I1 hold: no case, `EvalHeldForAuth` | 6 |
| T20 | envtest | a new gated Agent has no route until promoted | 6 |
| T21 | envtest | a route re-create `Create` publishes onto the promoted revision | 7 |
| T22 | envtest | a restart after two of three cases sends only the third | 6 |
| T23 | envtest | the card precondition, digest match, `EvalBindingUnsupported` | 6 |
| T24 | envtest | the two causes of `EvalAwaitingCard` | 6 |
| T25 | envtest | `EvalSuiteNotFound` and `EvalSuiteUnreadable` | 5 |
| T26 | envtest | a suite edit under a held candidate, via the requeue field | 6 |
| T27 | envtest | the CRD installed after the manager starts | 6 |
| T28 | envtest | pins on a gated Agent, and on an ungated one | 8 |
| T29 | envtest | removing `spec.gates` promotes a failed candidate, and says so | 5 |
| T30 | envtest | starvation: a J2 `Lock` finishes beside two sleeping tenants | 7 |
| T31 | envtest | two writers never lose an outcome | 6 |
| T32 | unit | a stale reader: case k sent once | 6 |
| T33 | envtest | the hit count, and the Agent reconciler's no-op write | 6 |
| T34 | envtest | an orphaned claim is `Interrupted`, never re-sent | 6 |
| T35 | envtest | graceful shutdown, with the hook before `ctx.Err()` | 6 |
| T36 | envtest | a handover: the record drops on `Interrupted` | 6 |
| T37 | unit | an ambiguous claim sends nothing | 6 |
| T38 | chart | `terminationGracePeriodSeconds` ≥ 25 | 2 |
| T39 | unit | `GracefulShutdownTimeout` ≥ 25 s | 2 |
| T40 | unit | the requeue field's zero-value resolution | 6 |
| T41 | envtest | a refused `Adopt` evaluates and promotes | 7 |
| T42 | envtest | a J2 `Lock` that can finish only through a promotion | 7 |
| T43 | envtest | the missing-policy wedge, its message and the working exit | 7 |
| T44 | envtest | the re-point ends the wedge with nobody acting, from the gate's side | 7 |
| T45 | envtest | the apart state with C carded (no exits named), and its uncarded-C variant | 7 |
| T46 | envtest | the exemption's edges | 7 |
| T47 | envtest | the Gateway-level hold, before and after the deadline, and the unreadable Gateway | 7 |
| T48 | envtest | the foreign traffic policy, and the failed foreign list | 7 |
| T49 | envtest | an orphaned last claim waits on a recorded `Lock` | 7 |
| T50 | envtest | R's name in the wedge message after R's Service is deleted | 7 |
| T51 | envtest | a `Lock` past its deadline keeps the eval waiting (Q12 (a)) | 7 |
| T52 | envtest | `cardDigest` records the run-start card | 6 |
| T53 | envtest | a pin while the suite is missing, and with the CRD absent | 8 |
| T54 | envtest | the read-back on a CRD without the new fields: 3 to 6 fetches in 10 s | 6 |
| T55 | envtest | the schema check, `blockedFor` included, and `EvalRecordUnverifiable` | 5 |
| T56 | envtest | `blocked` carries `EvalBindingUnsupported` and `EvalAwaitingCard` | 6 |
| T57 | envtest | an exempt `Lock` with an unusable binding: no write loop | 7 |
| T58 | envtest | a stale hold clears when the `Lock` is abandoned | 7 |
| T59 | envtest | a suite reordered mid-run | 6 |
| T60 | envtest | the CRD installs at 4096/512; the echo rule with its guards | 1 |
| T61 | envtest | Q6 (c) with impersonation | 9 |
| T62 | e2e | Q6 (c) on k3s v1.33.6 | 9 |
| T63 | envtest | `ActiveRevisionNotEvaluated`; `NoGatesDeclared` on removal | 5 |
| T64 | envtest | generation lag: the old candidate is sent nothing | 6 |
| T65 | envtest | admission refuses each malformed suite and a second gate, one rule per case | 1 |
| T66 | envtest | J2 on a gated Agent: zero hits until the `Lock` is `Served` | 7 |
| T67 | envtest | a missing-policy `Lock` recorded when a `Passed` candidate promotes | 7 |
| T68 | e2e | `TestARevisionIsEvaluatedBeforeItTakesTraffic` | 10 |
| T69 | e2e | a Gateway-level policy while a gated candidate passes: promoted, `Create` held | 10 |

## §1.1 text the code has overtaken

- **The card fetch's redirect hole is closed.** §1.1 step 4 says "**The card fetch has the same hole today, for `GET`:** `fetchOnce` uses `&http.Client{}` with no `CheckRedirect`", and "What the slice depends on" lists "the card fetch refusing redirects (§3.4), in a separate change" as owed. PR #38 (`19c48a7`, 2026-09-14) gave the card fetch `cardHTTPClient`, with `CheckRedirect: refuseRedirects` and `directTransport` (`internal/controller/card.go:255-265`). Both sentences are now false of the code, which is rule 7's shape. Correcting them is a change to approved text, so it is an amendment through design 16's process, not something to fix in passing. Tracked in #101.

## Build order

Each step is one PR, test-first, each test with its mutation (AGENTS.md rules 1–3), and each reviewed independently before merge.

1. **The `EvalSuite` kind and the schema.** The Go type, its CEL rules, the generated CRD in `charts/assayd/crds/`, the suite digest, the `spec.gates` cap, the eight `status.eval` fields, the `GateRef` comment, RBAC `get` on `evalsuites`. Rows T3, T9, T60, T65. The EvalSuite CRD is generated into `config/crd/`, which envtest loads, and **not** copied into `charts/assayd/crds/`: the `Makefile` copies only `assayd.dev_agents.yaml` there (`Makefile:158`), and the copy of the EvalSuite CRD is added in step 6 (the coordinator's decision, below). No behaviour changes.
2. **Shutdown bounds.** `GracefulShutdownTimeout` explicit at 30 s; the chart test. Rows T38, T39. Independent of everything else and could go first.
3. **The A2A eval client**, as a package with an injection seam: request, endpoint join, outcome table, threshold rule, redirect refusal, timeout, body cap. Rows T1, T2, T4–T8. No controller.
4. **Move `gatewayAuthPolicies`, `foreignTrafficPolicies` and what they call** onto a shared receiver, bodies unchanged. No row of its own; design 03's existing suite is the regression net.
5. **The Agent reconciler's gate branch.** The four promotion conditions, the uncached suite read, every `GatesPassed` reason and its precedence, the schema check and the `apiextensions` scheme registration, Q9's removal messages, and a `reasons.yaml` entry for each new reason. Testable with `status.eval` written by the test, as T12 already requires. Rows T12, T14, T25, T29, T55, T63. **It does not retire `GateControllerUnimplemented`**: until step 6 lands, the branch keeps that reason for every gated candidate with no verdict, so no pass ever reports `EvalRunning` with nothing running. Either steps 5 and 6 merge together, or step 5 keeps the old reason and step 6 swaps it out (the coordinator's decision, below).
6. **The eval controller, waiting on every recorded `Create` or `Lock`.** Predicates, run start, run-start card fetch, `blocked`/`blockedFor`, claim–send–record, `Interrupted`, `failed[]`, pause, the requeue field, read-back. **The EvalSuite CRD enters `charts/assayd/crds/` here**, and `GateControllerUnimplemented` is retired here, with its `reasons.yaml` entry and the tests that assert it (`TestDeclaredGatesHoldWhenNoGateControllerExists`, `test/envtest/reconciler_test.go:284`). Rows T10, T11, T13, T15–T20, T22–T24, T26, T27, T31–T37, T40, T52, T54, T56, T59, T64.
7. **The exemption and its neighbours.** Clauses 1–5, clause 5's precedence in `blocked`, the wedge message's exits, the refused `Adopt`. Rows T21, T30, T41–T51, T57, T58, T66, T67. Split from step 6 because it is the most intricate part of §1.1, and a reviewer should see it alone.
8. **The pin rule** (Q5 (a)) and `GatesBypassed=PinnedRollback`, with `reasons.yaml` entries for `PinnedRevisionNotEligible` and `PinnedRollback`. A refused pin uses the existing unresolvable-pin path, `Degraded=True` with the new reason, as the human decided. Rows T28, T53.
9. **The Q6 (c) admission policy.** Rows T61, T62.
10. **The e2e.** `test/responder`'s two knobs; T68 and T69 on k3d.
11. **The documents.** Design 16's correction above (#101, once critiqued and approved); design 02's and design 07's text changes, through their own processes (#99, #100); the state paragraphs of `AGENTS.md` and `CLAUDE.md`.

## Decisions, and what stays owed

1. **Building before designs 02 and 07 carry their text — decided by the human, 2026-10-13.** The question: §1.1 owes changes to designs 02 and 07, neither of which is approved, and steps 1, 5, 8 and 9 make design 02's §3.3 rollout steps 3–5 ("candidate deleted" on a fail) and design 07's A5.4 premise false of the code. The human chose **"Build under §1.1 (Recommended)"**: the human's approval of §1.1 covers the changes it requires in designs 02 and 07, so they are built under it, and each text change those designs need is tracked as a `follow-up` issue — design 02's in #99, design 07's in #100. Designs 02 and 07 stay unapproved overall.
2. **The reason list — no decision needed.** The review of PR #98 found that `internal/refgen/reasons.yaml` is the list §1.1 asks for, with `make reference` and `make verify` enforcing it (above). Each new reason gets its entry in the step that adds it.
3. **When the EvalSuite CRD enters `charts/assayd/crds/` — decided by the coordinator.** It ships there only in the step that lands the eval controller, step 6. Until then the gate branch keeps `GateControllerUnimplemented`, so no window reports `EvalRunning` with nothing running: either steps 5 and 6 merge together, or step 5 keeps the old reason and step 6 swaps it out. Q7 (a) is unchanged.
4. **The pin refusal's condition — decided by the human, 2026-10-13.** §1.1 says a refused pin goes "through the existing unresolvable-pin path, reason `PinnedRevisionNotEligible`"; that path sets `Degraded=True/ReleasePinUnresolvable` and phase `Degraded` (`internal/controller/release.go:110-123`). The human chose **"Degraded / PinnedRevisionNotEligible (Recommended)"**: the refusal reuses that path, with `Degraded=True` and the new reason `PinnedRevisionNotEligible`, phase `Degraded`, and the current release untouched.
5. **Correcting §1.1's redirect sentences — owed.** The correction ("§1.1 text the code has overtaken") changes approved text, so it is an amendment that needs design 16's critique and the human's approval. It changes no rule. Tracked in #101, labelled `needs-human-decision`. It blocks no build step.

No question among Q1 to Q13, and none of clause 1's options, is reopened by anything found here.
