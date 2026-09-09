# Design 02 implementation — Codex cold review

**Date:** 2026-08-25

**Reviewer:** Codex (`codex-critic`), deliberately a different model family from the implementer and previous reviewers

**Scope:** `api/v1alpha1/agent_types.go`, generated Agent CRDs, `internal/revision/`, `internal/controller/`, `cmd/operator/`, `charts/assayd/`, and the envtest/chart/e2e tests

**Verdict:** **REVISE — 7 BLOCKER, 4 MAJOR, 0 MINOR**

This was a cold implementation review against `docs/architecture.md`, design 02 and amendments A11–A14. It also accounts for the current A15 text where that text directly governs the implementation under review. All mutations were restored from backup copies, never with `git checkout`.

## Findings

### BLOCKER 1 — Behaviour hashing is not leaf-complete

**Files:** `internal/revision/revision.go:216-250`, `internal/revision/classification_test.go:24-49,92-134`

The classifier treats `Runtime.Env`, `Runtime.EnvFrom`, and `LLM` as atomic fields, while the projection silently drops semantic children:

- `SecretKeySelector.Optional`
- `ConfigMapKeySelector.Optional`
- `ObjectFieldSelector.APIVersion`
- `ResourceFieldSelector.Divisor`
- `EnvFromSource.Optional`
- `LLMSpec.EgressAllowlist`

Direct differential tests changed `EnvFrom.ConfigMapRef.Optional`, `ResourceFieldRef.Divisor`, and `LLM.EgressAllowlist`; all retained the same revision hash. The first two already reach the rendered PodSpec, so the controller updates the active revision in place without an eval.

**Concrete failure:** change a resource-derived prompt limit from divisor `1m` to `1`, or change a required configuration source to `optional=true`. The existing gated revision changes behaviour without minting a candidate.

**Fix:** make classification leaf-level, project every semantic child, and mutation-test every nested leaf independently. Explicitly classify `EgressAllowlist` before the policy compiler consumes it. Aggregate labels such as `Runtime.Env` and one representative mutation do not prove the aggregate's children are projected.

### BLOCKER 2 — Mutable ConfigMap contents bypass revisioning by design

**Files:** `docs/designs/02-agent-crd-operator.md:96-123`, `api/v1alpha1/agent_types.go:96-100`, `internal/revision/revision.go:216-250`

A12 hashes env sources “by referent, not contents,” but `EnvFrom` permits arbitrary ConfigMaps, not merely rotatable credentials.

**Concrete failure:** an actor unable to modify an Agent but allowed to update its referenced ConfigMap replaces a system prompt, provider endpoint, or feature configuration. A pod restart consumes the new behaviour under the old revision and old gate result.

**Fix:** amend A12. Separate live credential references from behavioural configuration. Behavioural ConfigMaps must be immutable/versioned or represented by a content digest included in the revision. It is reasonable not to hash Secret credential contents when live credential rotation is required; extending that exception to arbitrary configuration is not reasonable.

### BLOCKER 3 — Mutable image tags make the revision hash non-content-addressed

**Files:** `api/v1alpha1/agent_types.go:71-73`, `config/crd/assayd.dev_agents.yaml:436-440`, `charts/assayd/crds/assayd.dev_agents.yaml:436-440`, `internal/controller/agent_controller.go:443-462`

The API accepts any non-empty image string. The generated CRD has no digest restriction, and the chart contains no signature admission policy. `ImagePullPolicyAlways` makes a mutable tag explicitly re-resolve on every pull.

**Concrete failure:** gate `registry/agent:prod` while it points to image X, retag `prod` to Y, then reschedule the pod. Kubernetes pulls Y while the Agent spec, revision hash, and gate result remain unchanged.

The API and generated CRD claim “admission rejects unsigned images,” but this repository enforces no such bound.

**Fix:** require `@sha256:` image references at Agent admission, or resolve tags to verified digests before hashing and workload creation. Signature admission must verify that digest. Add generated-artifact and live-admission tests proving rejection of tags and unsigned content.

### BLOCKER 4 — Prod agents with no gates are deliberately promoted

**Files:** `internal/controller/agent_controller.go:496-545`, `test/envtest/reconciler_blockers_test.go:504-523`, `api/v1alpha1/agent_types.go:55-59`

When EvalSuite is installed, `gatesSatisfied` returns true precisely when `spec.gates` is empty. A focused envtest reproduced promotion with `EvalSuiteInstalled=true` and no gates.

That contradicts the API contract: at least one gate is required in prod iff EvalSuite exists. The existing test actively entrenches the bypass with “should promote regardless of wiring.” The chart knows `profile`, but the operator does not receive it.

The agent reconciler also declares and writes `GatesPassed`, even though the eventual gate controller must author the result.

**Concrete failure:** install the eval tier in a prod cluster and create an Agent with no `spec.gates`. It becomes active without an eval and reports `GatesSkipped=NoGatesDeclared`.

**Fix:** plumb an explicit profile/tier into the controller. In prod, reject or hold missing gates when EvalSuite is installed. Only an explicit local bypass may promote and must set `GatesBypassed=DevProfile`. The agent reconciler should consume, not overwrite, the gate controller's `GatesPassed` condition.

### BLOCKER 5 — A cached “EvalSuite absent” answer permanently authorizes promotion

**Files:** `internal/controller/discovery.go:72-100`, `cmd/operator/main.go:55-59`, `internal/controller/agent_controller.go:220-247`

A focused envtest warmed the detector with “absent,” installed the EvalSuite CRD, then created an Agent with a declared gate. The cached answer promoted it with `GatesSkipped=EvalSuiteCRDAbsent`.

TTL expiry does not repair the authorization reliably: CRD installation does not enqueue Agents, and once the desired revision becomes active, the `ActiveRevision != desired` guard prevents the same revision from being held retroactively. The flag help text explicitly admits that untouched Agents are not reconciled when the answer flips.

**Concrete failure:** the core tier is running, discovery caches absence, and an administrator installs EvalSuite. Any gated Agent created before cache expiry can promote ungated and remain active after the detector eventually changes its answer.

**Fix:** watch CRD establishment/removal, synchronously invalidate discovery, and enqueue every Agent. A negative cache must never authorize a rollout across a capability transition. Prefer explicit tier configuration over dynamic discovery for this safety decision.

### BLOCKER 6 — Pod-template metadata is outside drift ownership

**Files:** `internal/controller/agent_controller.go:306-367`

`podSpecEquivalent` compares the PodSpec exactly and converges against a real k3d API server. That core comparison is sound and its deletion mutation was killed. However, `templateEquivalent` ignores `PodTemplateSpec.ObjectMeta`. A focused envtest injected a pod-template annotation and reconciliation left it intact.

**Concrete failure:** someone with `deployments/update` adds an annotation recognized by a mutating webhook. Updating the template starts a ReplicaSet whose Pods receive an injected sidecar. The stored Deployment PodSpec still matches the operator's desired PodSpec, so the annotation and injection survive every reconciliation.

**Fix:** own and compare template labels and annotations exactly after explicitly normalizing any server/controller-owned metadata. Enumerate all operator-owned DeploymentSpec fields instead of describing PodSpec equality as reconciliation of the “whole template.”

### BLOCKER 7 — Guard states do not guard anything

**Files:** `internal/controller/agent_controller.go:142-274`, `internal/controller/conditions.go:38-67`, `docs/designs/02-agent-crd-operator.md:179-199`

The reconciler never checks `Killed`, `BudgetExhausted`, `PhaseKilled`, or `PhaseBudgetHeld` before ensuring workloads and choosing an ordinary rollout phase.

A focused envtest reconciled an Agent with `Killed=True` and `phase: Killed`; it became `phase: Ready`, `Ready=True`. A workload scaled down by another guard controller would also be restored to the requested replica count.

Additionally, this reconciler declares `Degraded` as owned even though its own comment says design 20's drift conditions belong to another controller.

**Concrete failure:** the kill controller marks an Agent killed and removes traffic or replicas. The agent reconciler runs afterward, recreates/restores the workload and reports it Ready, defeating the sticky explicit-revive contract.

**Fix:** evaluate guard inputs before workload creation or promotion. Killed and budget-held states must force their zero-traffic/workload policy and win every phase transition until their defined exit. Establish single-writer ownership for each condition; introduce a separate workload-availability condition if necessary.

### MAJOR 1 — Several reachable state-machine cells lie

**Files:** `internal/controller/agent_controller.go:166-265,480-493`, `docs/designs/02-agent-crd-operator.md:253-265`

The reachable cells exercised in this review are:

| Reachable state | Reported state | Assessment |
|---|---|---|
| No active; desired unavailable | `Pending`, `Ready=False` | Correct |
| Desired equals active; desired unavailable | `Degraded`, `Ready=False` | Correct; mutation-pinned |
| Old active; desired unavailable; old active available | `Ready`, `Progressing=True` | Correct intended state |
| Old active and desired both unavailable | `Ready=True`, “old revision is serving” | Wrong: old active is never checked |
| No active; desired available; gates fail | `Held`, `Ready=False` | Correct |
| Old active; desired available; gates fail | `Held`, `Ready=True` | Wrong phase: A13 requires `Ready` while the old revision serves |
| Desired available; gates pass or skip | `Ready=True`, “serving” | Wrong in prod when registration, identity, policy and gateway were never checked |
| Any rollout cell plus `Killed`/`BudgetHeld` | Ordinary rollout result | Wrong: guard state is overwritten |
| External first creation | `Pending`, `Ready=False` | Honest |
| Runtime changed to external | `Pending`, but old workload/status survive | Wrong |

The “serving” reason is based solely on Deployment `AvailableReplicas`. The e2e pause-container fixture can therefore become `Ready=True` without an A2A server. Current A15 explicitly requires missing gateway CRDs to withhold Ready outside the local profile.

**Fix:** query both active and desired availability, implement the A13 phase rule, and make Ready depend on the actual registration, identity, policy and gateway prerequisites for the selected profile. Every branch must assert the conditions and reasons it owns for the current generation.

### MAJOR 2 — Runtime-to-external conversion leaves the old agent running

**Files:** `internal/controller/agent_controller.go:128-133,293-303`

The external branch returns before listing or garbage-collecting owned workloads. It also retains `activeRevision`, `candidateRevision`, and rollout conditions.

A focused envtest converted a running in-cluster Agent to `spec.external`; the old Deployment remained active while status reported external registration pending.

**Concrete failure:** an operator moves an Agent to an externally hosted endpoint expecting the in-cluster code to stop. The old in-cluster revision continues running and may remain independently reachable, while status describes a different lifecycle.

**Fix:** treat runtime/external changes as lifecycle transitions. Remove or scale down all old in-cluster revisions before acknowledging the external state, and explicitly clear incompatible revision, card and rollout status.

### MAJOR 3 — The live-cluster no-churn test survives deletion of its claimed rule

**Files:** `internal/controller/agent_controller.go:383-388,427-442`, `test/e2e/e2e_test.go:214-229,280-294`

Removing the explicit `ServiceAccountName: "default"` compiled, was built into the operator image, was deployed to k3d, and passed the complete e2e suite. The supposed churn test only sleeps; it does not force another Agent reconciliation. The older ServiceAccount-specific test is unconditionally skipped even though the operator is deployed.

**Concrete failure:** the code loses a field that the test comments call load-bearing, yet CI remains green. More generally, any normalization mismatch that needs a second reconcile is invisible if no reconcile occurs during the sleep.

**Fix:** force several live reconciliations, for example by repeatedly changing harmless Agent metadata, and assert that the Deployment spec/generation does not move. Separately unit-test which fields `deploymentFor` intentionally owns. Remove or implement the stale skipped test.

### MAJOR 4 — Invalid container ports are admitted and become silent reconcile failures

**Files:** `api/v1alpha1/agent_types.go:83-86`, `config/crd/assayd.dev_agents.yaml:441-445`, `charts/assayd/crds/assayd.dev_agents.yaml:441-445`

The Agent CRD accepts any `int32` port, including negative values and values above 65535. Deployment admission then rejects the rendered workload, and reconciliation returns an error without an Agent condition explaining that the CR itself is invalid.

**Concrete failure:** an Agent with `runtime.port: 70000` is accepted, never materializes a workload, and remains without a precise terminal condition. The invalid value is retried as an operational failure rather than rejected at the source.

**Fix:** add kubebuilder `Minimum=1` and `Maximum=65535`, regenerate both CRD copies, and test rejection against the generated artifact and envtest API server.

## Mutation ledger

Only compile-valid subject mutations are classified as KILLED or SURVIVED. Sandbox/cache failures were rerun outside the sandbox and were not treated as mutation outcomes. Temporary tests were removed and every source mutation was restored with `cp` from a backup.

| ID | Mutation / executable reproducer | Command or layer | Result | Evidence / consequence |
|---|---|---|---|---|
| M1 | Delete `External.OAuthClientRef` from the revision projection | `go test ./internal/revision -count=1` | **SURVIVED**, subsequently **FIXED** in `4dfa371` | The original test changed runtime→external and endpoint simultaneously, so endpoint alone changed the hash. This mutation was accidentally committed by another agent, reached main green, and exposed a real authorization/eval bypass. Commit `4dfa371` restores the projection and mutation-verifies the test in both directions. |
| M2 | Delete `LLM.Providers` from the projection | `go test ./internal/revision -count=1` | **KILLED** | `TestGoldenDigest` and `TestBehaviourSurfaceMintsARevision/llm.providers` failed. |
| M3 | Change only `EnvFrom.ConfigMapRef.Optional` (`false` → `true`) | Temporary direct differential test in `internal/revision` | **REPRODUCED** | Both specs had the same revision hash. |
| M4 | Change only `ResourceFieldRef.Divisor` (`1m` → `1`) | Temporary direct differential test in `internal/revision` | **REPRODUCED** | Both specs had the same revision hash. |
| M5 | Change only `LLM.EgressAllowlist` | Temporary direct differential test in `internal/revision` | **REPRODUCED** | Both specs had the same revision hash. |
| M6 | Force `podSpecEquivalent` to return true and remove the now-unused equality import so the mutant still compiles | Focused envtest drift suite | **KILLED** | Existing isolated PodSpec drift tests failed. Exact PodSpec ownership is pinned. |
| M7 | Delete the `CondDegraded=True` assertion from the active-workload-unavailable branch | Focused envtest `TestDegradedPhaseAssertsTheDegradedCondition` | **KILLED** | The test failed on the absent condition. |
| M8 | Active A exists; desired B is unavailable; set both A and B to zero available replicas | Temporary envtest | **REPRODUCED** | Reconciler reported `Ready=True` and said A was serving without checking A. |
| M9 | Active A serves; candidate B is available but held on gates | Temporary envtest | **REPRODUCED** | Reconciler reported `phase: Held`; A13 requires `phase: Ready`, `Ready=True`, `Progressing=True`. |
| M10 | Seed `Killed=True`, `phase: Killed`, then reconcile an available workload | Temporary envtest | **REPRODUCED** | Phase was overwritten to `Ready`; `Ready=True` was asserted. |
| M11 | Add an arbitrary annotation to the owned Deployment's pod template | Temporary envtest | **REPRODUCED** | Reconciliation preserved the annotation because equivalence ignores template metadata. |
| M12 | Convert a running runtime Agent to an external Agent | Temporary envtest | **REPRODUCED** | Old Deployment and stale revision status survived the early external return. |
| M13 | EvalSuite reported installed; create an Agent without `spec.gates` | Temporary envtest | **REPRODUCED** | Agent promoted rather than being rejected or held. |
| M14 | Warm discovery with EvalSuite absent, install the EvalSuite CRD, then create an Agent with a declared gate during the cache TTL | Temporary envtest with real API server discovery | **REPRODUCED** | Agent promoted with `GatesSkipped=EvalSuiteCRDAbsent` after the CRD existed. |
| M15 | Delete explicit `ServiceAccountName: "default"` from `deploymentFor` | Full `make e2e`, compiling/building/loading/deploying the mutant | **SURVIVED**, **OPEN** | All e2e tests passed. The claimed live-admission churn invariant is not pinned. |

No mutation that failed to compile was counted as evidence. The M6 unused import was removed as part of the mutant specifically so it remained compile-valid.

## Execution record

The following passed on the unmutated implementation and again after restoration where applicable:

- `make test`
- `make verify`
- `make race`
- `make e2e`

`make e2e` built and loaded the operator into k3d. After M15, the original controller file was restored from its backup and the clean image was rebuilt, redeployed and rerun successfully, so the cluster was not left running mutated code.

## Disagreement with prior reviews

I disagree with the final PASS in `docs/designs/reviews/02-recritique.md` and with the confidence implied by `docs/designs/reviews/02-review.md` being fully resolved.

1. **A12 completeness was checked at the wrong shape.** The previous reviews traced the top-level design table. The executable implementation aggregates Kubernetes structs, and compile-valid mutations prove semantic children evade the projection. Agreement that every top-level row is named is not evidence that every reachable behaviour field affects the hash.

2. **“By referent, not contents” is not universally safe.** It is defensible for live credential rotation. It is an eval bypass for arbitrary ConfigMap-provided behavioural configuration. The earlier reviews accepted the sentence without separating those two cases.

3. **A13's design conclusion is correct, but the implementation contradicts it.** A serving active revision plus an in-flight or gate-held candidate must remain `phase: Ready`. The gate-held implementation writes `phase: Held`, and the existing test checks the conditions but omits the phase.

4. **The earlier reviews did not establish that reported reasons were actually checked.** “Revision X is serving” is asserted from desired Deployment availability without checking the old active Deployment, and `Ready=True` is asserted without registration, identity, policy or gateway readiness. Those are exactly the loud-and-wrong states NFR-8 forbids.

The OAuth mutation is unusually strong evidence for this disagreement: it survived the supposedly covering test, was accidentally committed, reached main green, and removed the identity selector from the revision hash. The later fix does not make the original PASS sound; it demonstrates why the PASS was unsound.

## Verdict

**REVISE.** The current implementation permits behaviour changes, image changes, missing gates, stale discovery answers, template-metadata injection and kill-state overwrite to cross the rollout boundary without the guarantees the Agent status claims. The full gate being green does not mitigate these findings; one live-cluster invariant still survives deletion, and several bypasses were reproduced directly against an API server.
