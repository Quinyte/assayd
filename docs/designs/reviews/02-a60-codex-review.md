# Codex review — design 02 A60 and run-namespace propagation

- **Snapshot:** uncommitted working tree, 2026-09-03
- **Scope:** design 02 A60 and its folded body changes; design 07 A5; design 26 A1; design 06 A3; design 03 §3.2; the tenant-namespace research note; both same-family critiques
- **Verdict:** **REVISE — 10 BLOCKER, 10 MAJOR.** A60 does not close Codex r8 BLOCKER 7. It creates a durable binding authority, but SPIRE and Gateway do not consume that authority; both trust forgeable namespace labels instead. The binding protocol itself can still adopt an attacker-created namespace after a crash, and two teardown paths can either write into a recreated namespace or retain an old tenant's namespace forever.

## Execution evidence

`git diff --check` passes.

The narrow repository gate does not:

```text
$ go test ./api/v1alpha1 ./test/docs
--- FAIL: TestConditionVocabularyIsClosed
    schema_contract_test.go:232: design 02 §3.1 declares "RunNamespaceUnavailable" and no constant in this package does.
FAIL github.com/Quinyte/plume/api/v1alpha1
ok   github.com/Quinyte/plume/test/docs
```

There is no A60 implementation to mutate. That is not a waiver of the mutation rule: the implementation contract below requires independent fixtures that kill deletion of each authority check and state transition. The one new rule already covered by an executable closure test fails before mutation.

## BLOCKER 1 — The amendment makes the repository's enforced condition vocabulary red

**Files:** `docs/designs/02-agent-crd-operator.md:77-90,782` · `api/v1alpha1/agent_types.go:364-436,611-654`

A60 adds `RunNamespaceUnavailable` to the design's closed Agent condition list, but does not add `CondRunNamespaceUnavailable` to the Go vocabulary or `designConditions()`. The existing test correctly rejects the mismatch.

**Failure scenario:** committing this documentation set makes `make test` fail before design 03 implementation starts. If the test is bypassed, the future run-namespace controller has no typed condition it can set, so the terminal namespace-refusal path becomes silent or invents a literal outside the closed vocabulary.

**Specific fix:** add the typed constant and the `designConditions()` entry in the same change as A60, regenerate any derived artifact, and mutation-check both directions: deleting the design entry and deleting the Go entry must each fail. The constant may remain unwritten until A42 is implemented; the vocabulary cannot remain inconsistent meanwhile.

## BLOCKER 2 — The SPIFFE selector trusts a label that the binding authority never authenticates

**Files:** `docs/designs/07-umbrella-chart-ci.md:121-147` · `docs/designs/02-agent-crd-operator.md:136,392-394` · `docs/designs/26-tenant-cr.md:113`

The binding record proves which namespace the operator created. `ClusterSPIFFEID` never reads it. It selects every namespace carrying `plume.dev/pods-by: agent-operator`, while A5.2 calls that label a certificate that only the operator creates Pods there. A60 itself already establishes the opposite principle: a label needs only `update` to forge and is not provenance. Splitting `run-namespace` and `pods-by` prevents a vCluster sync namespace from being selected accidentally; it does not make the second label authoritative.

**Failure scenario:** the principal A60 explicitly models — one able to pre-create a Namespace and plant a self-granting RoleBinding — creates any namespace with `plume.dev/pods-by: agent-operator`, then creates a Pod labelled `plume.dev/agent-namespace: team-a` and `plume.dev/agent: reviewer`. The cluster-wide `ClusterSPIFFEID` selects it and issues the soft tenant's agent identity. The real operator can refuse the namespace forever and the impersonating Pod still gets the SVID, because SPIRE never consults the binding.

**Specific fix:** choose and specify an enforceable authority. The least invasive shape is a required cluster admission policy that reserves creation, mutation and removal of `plume.dev/pods-by` to the plume operator identities and denies tenant-created workload controllers/Pods in selected namespaces; it must account for ReplicaSet and Sandbox controllers creating child Pods. The stronger alternative is to supersede ADR-0019's templated-registration decision and register exact, binding-verified Pod UIDs. A second public label or a per-namespace selector by name is not a fix: a namespace recreator can copy both. Add a real-cluster negative test in which the scoped attacker can create Namespaces and RBAC but cannot obtain an SVID by stamping plume labels. Mutation: remove the admission binding or widen its username predicate; the test must fail.

I disagree with `02-a60-critique-r2.md`'s statement that the two-label split is “the right shape” and that the constant label is equivalent to `owned-by:<install-uid>`. Both are public, forgeable metadata. The split fixes selector conflation, not provenance.

## BLOCKER 3 — `allowedRoutes` turns the other forgeable label into permission to publish an unmanaged route

**Files:** `docs/designs/07-umbrella-chart-ci.md:149-163` · `docs/designs/03-policy-compiler.md:153` · `docs/designs/02-agent-crd-operator.md:136,143`

The Gateway admits every namespace labelled `plume.dev/run-namespace: "true"`. Again, the binding record is not in that decision. Gateway API's selector is cross-namespace consent, not proof that plume created or governs the namespace.

**Failure scenario:** the same principal pre-creates a namespace carrying `plume.dev/run-namespace: "true"`, grants itself rights there, and creates an `HTTPRoute` attaching to the shared Gateway. It can publish an arbitrary backend or pre-empt a plume hostname/path without any compiler-emitted auth, rate-limit, tool-filter or receipt policy. Row 1 of A60 makes the Agent `NotCreatedByOperator`, but the Gateway accepts the attacker's route independently.

**Specific fix:** the reserved-label admission control from BLOCKER 2 must also protect `plume.dev/run-namespace`, including the host tenant-operator as the only hard-mode writer. Additionally bind route creation targeting the plume Gateway to the compiler/operator identity; a legitimate selected namespace must not become an ungoverned route-authoring grant. Add negative tests for both pre-created soft namespaces and hard-mode host sync namespaces. Removing either label protection or the route-author restriction must be KILLED.

## BLOCKER 4 — The crash-gap “pristine” check is a TOCTOU adoption bypass

**Files:** `docs/designs/02-agent-crd-operator.md:159-161,174-176` · `docs/designs/reviews/02-a60-critique-r2.md` MAJOR 4

After `Create(Namespace)` succeeds but before the UID is stored, a crash leaves a public nonce on a Namespace and a `Creating` binding. The replacement reconciler accepts any nonce-matching namespace that is pristine at one instant. The threat already assumes a principal able to plant a self-granting RoleBinding. That principal can delete the genuine namespace, recreate an empty nonce-stamped one, let the pristine check pass, and create its RoleBinding immediately after the check or after `Bound`. `creationTimestamp >= binding.creationTimestamp` distinguishes neither namespace, and a point-in-time emptiness test does not freeze future writes.

**Failure scenario:** the operator restarts during a routine rollout at exactly the documented gap. The attacker recreates the namespace empty with the copied nonce. The replacement operator records the attacker's UID as `Bound`, then copies revision Secrets. The attacker creates its RoleBinding after the pristine list and reads the copies. Every subsequent UID comparison succeeds because the binding now vouches for the attacker's UID.

**Specific fix:** never adopt a namespace merely observed while the binding is `Creating`. Persist an attempt owner/lease epoch before creation; only the reconcile that receives the successful `Create` response may record that response's UID. On owner loss, ambiguous timeout or lease expiry, delete the unbound nonce-stamped namespace, rotate the nonce, and retry. A successor may clean up an attempt but may not bind it. A cluster admission guard that makes the run namespace unwritable to the attacker is an alternative only if it is an explicit dependency and is tested through the entire crash interval. Add the exact paused-after-Create attack and mutate away the owner/epoch check.

This rejects the fix prescribed by the same-family r2 critique. “Pristine” reduced one attack payload; it did not establish creator provenance.

## BLOCKER 5 — The handler can restore `Bound` on a replacement namespace without checking its UID

**Files:** `docs/designs/02-agent-crd-operator.md:173,178-180,182-188`

The decision list checks UID for `Bound`, but the `Terminating` handler has its own direct transition: if Agents or other resources exist, it CASes back to `Bound` and “proceed[s] as row 12”. It does not first require the present namespace UID to equal `runNamespaceUID`.

**Failure scenario:** an administrator or attacker deletes a legitimate run namespace. Row 10 moves the binding to `Terminating`. After deletion completes but before the handler observes absence, the attacker recreates the same name with its own RoleBinding. The handler sees a live Agent in source namespace `S`, restores `Bound`, and follows row 12 into label/mirror/material reconciliation in the replacement namespace. The UID check that would have rejected it is bypassed by the handler's direct path.

**Specific fix:** make “present namespace UID equals the binding UID” the first invariant inside every handler pass, before any flip to `Bound`. A mismatch must never write or restore; report `NotCreatedByOperator` and require the documented human recovery, or wait for the foreign namespace to be removed. The paused-delete/recreate test must assert that no Secret, ConfigMap, RoleBinding, workload or mirror is written. Mutating out the handler-local UID check must fail.

## BLOCKER 6 — Source recreation still cannot retire the old run namespace when old material remains

**Files:** `docs/designs/02-agent-crd-operator.md:172,182-188,192-198`

R2 correctly made the Agent list source-UID-aware, but the same handler separately counts workloads and copies belonging to any Agent other than “the one reconciling”. On source namespace recreation, the old namespace's orphaned workloads/copies are exactly what remains, and there is no old Agent object to reconcile. The Namespace-keyed entry point has no “one reconciling” Agent at all.

**Failure scenario:** source namespace `team-a` UID `u1` and its Agents disappear because finalizers were bypassed; run namespace `R` still contains `u1` workloads and revision copies. `team-a` is recreated as UID `u2`. Its first Agent moves the binding to `Terminating`. The UID-scoped Agent list is empty, but the resource list finds every `u1` object and flips the binding to `Bound`. The next reconcile detects the source UID mismatch and flips it back. The old tenant's route and credentials remain indefinitely, while the new tenant never gets a run namespace.

**Specific fix:** persist a termination cause/epoch. `SourceNamespaceRecreated` is irreversible cleanup of the old binding: resources owned by the old source are targets to delete, not evidence to restore `Bound`. The “other workload still draining” predicate is valid only for last-Agent teardown under the same source UID. Alternatively stamp and verify `sourceNamespaceUID` on every resource and count only resources backed by a currently live Agent in that same source UID. Add the row-4 test with old workloads and copies deliberately left behind; it must reach namespace deletion and a fresh binding.

## BLOCKER 7 — The proposed NetworkPolicy blocks two required plume paths

**Files:** `docs/designs/07-umbrella-chart-ci.md:165-174` · `docs/designs/02-agent-crd-operator.md:367-374,410-415` · `docs/designs/09-sdk-templates.md:23-25`

A5.4 admits ingress only from gateway Pods and egress only to the gateway plus DNS. The agent-operator fetches the candidate's Agent Card in-cluster before registration; it is not a gateway Pod. The reference SDK's shared A2A task store connects directly to JetStream through `PLUME_NATS_URL`; NATS is not the gateway.

**Failure scenario:** enabling the gateway causes the operator's card GET to time out under the new ingress policy, so every candidate eventually fails registration. If that were bypassed, every multi-replica template Agent loses its declared shared task store because NATS egress is denied. The gateway-on profile is less functional than the explicitly ungoverned profile and the resulting symptoms point at card/NATS failures, not NetworkPolicy.

**Specific fix:** define a complete producer/consumer traffic matrix before defining the policy. At minimum, allow card-fetch ingress from the operator's namespace and Pod identity to the Agent port, and allow egress to the tenant's NATS endpoints/ports used by the injected contract. If all traffic is instead to traverse the gateway, redesign card registration and the JetStream task-store contract accordingly; agentgateway cannot proxy NATS merely because the architecture says “all agent traffic”. Add k3d tests proving card registration and a shared task round trip with enforcement on, plus negative controls to forbidden destinations. Deleting either required rule must fail the test.

## BLOCKER 8 — Hard mode has no producer for the host namespace and rewritten Service name

**Files:** `docs/designs/26-tenant-cr.md:106-115,120` · `docs/designs/03-policy-compiler.md:20-38,77-92,133-153`

Design 26 says the host compiler writes into the vCluster's host sync namespace and references the synced Service “by its host name”. Design 03's input has only `target:{kind,ns,name}`, sourced from the Agent CR, and its output section knows only “run namespace”. It has no resolved host namespace, host Service name, vCluster identity or mapping generation. In vCluster single-namespace sync—the “one host namespace” shape A1 names—Service names are translated to opaque `vxxxxxxxxx` names; the official vCluster Service documentation states that translation explicitly. Nothing in this amendment tells the compiler how to discover the corresponding object without racing or confusing tenants.

**Failure scenario:** the compiler reads `Agent/team-a/reviewer` and emits a host `HTTPRoute` whose backend is the virtual Service name. The actual synced Service has a rewritten host name, so `ResolvedRefs=False`; every hard-mode Agent wedges before publication. A list-by-guess implementation can bind to the wrong synced Service after a recreate or vCluster migration.

**Specific fix:** make the mapping a versioned producer contract. The tenant-operator or vCluster integration must resolve and persist `{tenantUID, virtualNamespaceUID, virtualServiceUID, hostNamespace, hostServiceName, hostServiceUID, mappingGeneration}`; `PolicyIntent`/`TenantPolicyIntent` consumes that value, and the compiler refuses stale or ambiguous mappings. Measure both single-namespace and any supported multi-namespace sync mode before choosing the fields. Add a host/virtual e2e that recreates the Service and proves the route follows the new UID rather than a same-name stale object.

## BLOCKER 9 — The hard-mode teardown signal is invented in design 26 but absent from both owning contracts

**Files:** `docs/designs/26-tenant-cr.md:70,85,116` · `docs/designs/03-policy-compiler.md` (no `RoutesRevoked` producer) · `docs/designs/02-agent-crd-operator.md:77-90,402-405,455`

Design 26 assigns drain/revoke and `RoutesRevoked=True` to the compiler. Design 03 does not specify a hard-mode Agent deletion watch, vCluster client lifecycle, the exact resource-set deletion postcondition, or a status write. Design 02 does not include `RoutesRevoked` in its closed condition vocabulary and does not say which condition type carries reason `AwaitingRouteRevocation`; §5 only says an unnamed condition “names the stuck step”. This is not propagation: the consumer amendment describes a producer the producer design does not have.

**Failure scenario:** the tenant agent-operator sees deletion and waits for a condition no typed producer can set, wedging the finalizer forever. An implementer who invents the condition as a literal defeats the closed-vocabulary test; one who lets the agent-operator continue after a timeout deletes Pods while host routes still carry traffic.

**Specific fix:** amend design 03 with the hard-mode lifecycle input, client ownership, exact drain/revoke postcondition and the sole `RoutesRevoked` writer. Amend design 02's condition list and ownership rules with `RoutesRevoked` plus a named local waiting condition/reason. Require server-side apply to the single condition map entry with a dedicated field manager and no force; the current agent-operator uses whole-status `Update`, so the co-authoring migration must be designed to prevent lost updates. Pin observation to Agent UID and the exact host ResourceSet digest. Test compiler loss, stale True, concurrent status writes and partial host deletion.

## BLOCKER 10 — The amendment records a live cross-tenant principal collision and leaves the authorization design unchanged

**Files:** `docs/designs/26-tenant-cr.md:115,122` · `docs/designs/24-authz-provider.md:36,45-47`

Design 26 correctly notices that hard and soft tenants can have the same SPIFFE path in different trust domains, while design 24 maps an SVID to subjects shaped like `agent:pa-reviewer` and does not include trust domain or tenant in the mapping/cache key. Calling this “owed” does not make the cross-design contract safe.

**Failure scenario:** a hard tenant creates `team-a/reviewer` in its federated trust domain. The host ext-authz adapter canonicalizes it to the same FGA subject as the soft tenant's reviewer. Depending on store selection, it inherits the soft agent's `can_invoke`/`can_call` tuples or poisons a shared decision-cache entry. Federation makes the foreign SVID verifiable; the missing domain binding makes it indistinguishable.

**Specific fix:** amend design 24 in this set. Define the canonical machine subject and every cache/store key as including trust domain, tenant identity, Agent namespace and Agent name; reject an SVID whose trust domain is not mapped to exactly one Tenant UID. Add a two-domain collision e2e with identical path components. This is a security boundary, not follow-up documentation.

## MAJOR 1 — The binding record's declared state set excludes a state the protocol requires

**File:** `docs/designs/02-agent-crd-operator.md:150-157,173,187-188`

The record declares `Creating · Bound · Terminating`; rows and handler require `Deleting`. Because the record is an untyped ConfigMap, an implementer may silently treat the unknown value as zero/default or reject the record after writing it.

**Failure scenario:** the controller successfully CASes `Terminating→Deleting`, restarts, then decodes the documented enum and cannot re-enter step 5. The namespace and binding remain forever, or a permissive default reopens creation after deletion was committed.

**Specific fix:** include `Deleting` in the authoritative field table and make decoding strict. Add a state-version field and an explicit `BindingRecordInvalid` failure path; no unknown/missing state may default. Mutation-check every enum value and transition.

## MAJOR 2 — Row 4 permits the transition the delete fence explicitly forbids

**File:** `docs/designs/02-agent-crd-operator.md:172,187-190`

Source UID mismatch CASes to `Terminating` “from any state”, while the handler says nothing flips back from `Deleting`. `Deleting→Terminating` reopens the committed delete protocol.

**Failure scenario:** a source namespace is recreated while the old run namespace is already fenced `Deleting`. Its first Agent moves the record backwards to `Terminating`; concurrent handlers can then take the restore-to-`Bound` arm while another worker is deleting the namespace.

**Specific fix:** make `Deleting` absorbing until the namespace is absent and the binding is removed. Row 4 may enter `Terminating` only from `Creating`/`Bound`/`Terminating`; on `Deleting`, it waits. Add an exhaustive transition matrix test, not only per-row examples.

## MAJOR 3 — Mirroring ResourceQuota publishes a 2× “ceiling” and calls it enforcement

**Files:** `docs/designs/02-agent-crd-operator.md:139,175,202,778` · `docs/designs/26-tenant-cr.md:23-25,74-81`

A60 admits that the source and run namespaces can each consume the full quota, so the pair can reach twice the Tenant's stated limit. Design 26 still exposes `spec.quotas`, reports `QuotasEnforced`, and says quotas “bound” a soft-mode noisy neighbor. A value called a tenant ceiling is not a ceiling if the platform intentionally permits 2× it.

**Failure scenario:** `gpu: 1` allows one ordinary tenant Pod in the source namespace and one Agent Pod in the run namespace. `QuotasEnforced=True` while the tenant consumes two GPUs. The same applies to CPU, memory and object-count quota dimensions, with different operational effects.

**Specific fix:** make a human API decision: either define Tenant quota as per-namespace and rename/document the condition accordingly, or enforce an aggregate. An aggregate can use a tenant quota controller/admission mechanism, or partition individual resource dimensions between source and run namespaces where ownership is exclusive. Do not call two independent mirrors one ceiling. Test usage in both namespaces concurrently.

I disagree with both same-family critiques' acceptance of this trade. Stating the factor does not make the `QuotasEnforced` claim true.

## MAJOR 4 — “Watch RoleBindings to derive everyone who can read Agents” is not implementable Kubernetes authorization

**Files:** `docs/designs/02-agent-crd-operator.md:138` · `docs/designs/07-umbrella-chart-ci.md:145,193`

Kubernetes exposes authorization checks for a subject/request, not a complete inverse query returning every subject authorized for a resource. Agent read access may arise from Roles, ClusterRoles, aggregated roles, RoleBindings, ClusterRoleBindings, groups, impersonation or an external authorizer. Watching RoleBindings alone is incomplete, and revocation creates a credential-leak window if the derived RoleBinding is stale.

**Failure scenario:** a group can read Agents through a ClusterRoleBinding but never gets logs, while a user whose RoleBinding was removed retains direct read access in the run namespace until the derivation controller notices and reconstructs the set. Either availability or least privilege is wrong.

**Specific fix:** drop inverse authorization. Keep `plume logs` as the only supported path and perform `SubjectAccessReview` for the requesting identity, or add an explicit `logReaders`/tenant-admin subject contract that can be reconciled exactly. If direct `kubectl logs` is required, its authorization source must be enumerable and authoritative by design.

## MAJOR 5 — A DNS Service is not a NetworkPolicy peer

**File:** `docs/designs/07-umbrella-chart-ci.md:169-172`

A5.4 says values supply “the cluster DNS Service” for DNS egress, but `networking.k8s.io/v1 NetworkPolicyPeer` can select Pods/namespaces or an `ipBlock`; it cannot reference a Service. Converting a ClusterIP to an `ipBlock` is CNI/DNAT-order dependent and is not the portable shape the paragraph implies.

**Failure scenario:** the operator renders no valid peer from the supplied value, or permits the Service ClusterIP on k3d while another CNI evaluates the post-DNAT endpoint IP and drops DNS. Every outbound call then fails by name.

**Specific fix:** define the exact supported shape: preferably DNS namespace selector + Pod selector + TCP/UDP 53, with distro-specific label values supplied and validated; if endpoint IPs are used, reconcile EndpointSlices and state the churn semantics. Exercise the rendered policy against k3d and every CNI on which enforcement is claimed.

## MAJOR 6 — The compliance consumer is still allowed to report a bypassable gate as conforming

**Files:** `docs/designs/26-tenant-cr.md:29-33,109,122` · `docs/designs/27-compliance-packs.md:17-30,49-62`

`GatesTenantAdminBypassable=True` is added to Tenant, but design 27's posture contract does not consume it. Design 27 says every safeguard row is checkable and drift becomes `ComplianceViolation`; a hard-mode tenant admin can replace gated material by design and nothing changes the posture result.

**Failure scenario:** a hard tenant with the HIPAA pack reports all posture rows passing while its vCluster admin replaces revision material under a gated digest. The new Tenant condition is visible only to a reader who knows to ignore the compliance report.

**Specific fix:** amend design 27 now. The profile must either reject/mark non-conforming any tenant with `GatesTenantAdminBypassable=True`, or explicitly remove gate integrity from the claimed safeguard set and surface a failing/attested exception in the shipped posture document. Add a posture fixture for hard mode.

## MAJOR 7 — The accepted ADR still forbids the status write A1 now requires

**Files:** `docs/decisions/0026-p5-enterprise.md:4-6` · `docs/designs/26-tenant-cr.md:85,116` · `docs/architecture.md:205-215`

ADR-0026 says the host compiler has “tenant CR read only”; A1 gives it `agents/status patch` across vClusters. A60 also moves the workload/material boundary and introduces a cluster-wide binding protocol without any ADR or canonical architecture update. Under AGENTS.md, design-time decisions produce an ADR and architecture is canonical.

**Failure scenario:** the chart/RBAC implementer follows accepted ADR-0026 and omits status patch, wedging every hard-mode deletion. Another implementer follows A1 and silently widens the compiler across every tenant despite the accepted security decision saying it cannot write there.

**Specific fix:** supersede or amend ADR-0026 with the exact status-only write, threat consequence and field-ownership mechanism; record A42/A60's namespace and binding decision in ADR-0019 or a dedicated ADR; update canonical architecture with the soft/hard placement and teardown socket. Do not approve mutually authoritative contracts that disagree.

## MAJOR 8 — The binding authority is unversioned, unvalidated security state in a ConfigMap

**Files:** `docs/designs/02-agent-crd-operator.md:148-163,200`

“Nobody else may touch it” does not remove schema needs. The record controls namespace deletion, credential placement and tenant identity, yet has no `schemaVersion`, strict corruption behavior, required-field rules or digest. Manual recovery explicitly asks a human to synthesize one.

**Failure scenario:** an upgrade renames a state/field or a partial/manual write omits `sourceNamespaceUID`. A permissive decoder interprets empty UID/name values and reaches a different decision-list arm; a strict decoder errors forever with no named condition. Either can delete or adopt the wrong namespace.

**Specific fix:** use a small CRD with OpenAPI validation, or define a versioned strict ConfigMap wire format with required fields, exact enum, record digest and a terminal `BindingRecordInvalid` condition. Preserve decoders for live versions and specify migration. Mutation-test removal and corruption of every authority field.

## MAJOR 9 — The claimed workload “provenance” is predictable metadata, not provenance

**File:** `docs/designs/02-agent-crd-operator.md:137`

An object name is immutable after creation; it is not unforgeable before creation. The Agent name, revision name, labels and digest annotation are all predictable/public. A principal with create in the run namespace can pre-create the expected object and satisfy the stated ownership tuple.

**Failure scenario:** after gaining write through the crash-gap attack, a principal pre-creates the next expected Deployment name with copied labels/annotation. Code following this row treats the name as deletion authority and the other public metadata as corroboration, then updates/adopts or later deletes an object it did not create.

**Specific fix:** replace “unforgeable” with the narrower deletion-safety claim and define create collision behavior: `AlreadyExists` is never provenance; it must satisfy the complete desired object and a binding-backed creation record, or fail terminally without mutation. If no durable workload creation record is desired, rely on enforced namespace exclusivity and say that it is the actual authority.

## MAJOR 10 — The test plan names cases but not the mutations that prove the security predicates are reached

**File:** `docs/designs/02-agent-crd-operator.md:204,468`

The repository's history makes case names insufficient. A test can obtain `NotCreatedByOperator` because an earlier check failed and still pass after the UID/nonce predicate it claims to cover is deleted. A60's cases do not require each fixture to be wrong in exactly one way or require a transition result rather than a terminal value.

**Failure scenario:** the delete-and-recreate test uses a namespace whose nonce also differs. Deleting the UID comparison survives because the nonce path still refuses it. The test is green while the production UID authority is absent—the exact vacuity pattern AGENTS.md warns about.

**Specific fix:** add an explicit mutation ledger to the implementation acceptance criteria. At minimum independently delete/negate: nonce comparison, creator-attempt ownership, Bound UID comparison, handler-local UID comparison, source UID scoping, resource-drain count, `Terminating→Deleting` CAS, the no-flip-from-`Deleting` rule, and each reserved-label admission rule. Every fixture must differ from valid state in one predicate only; compile failures are `INVALID`, never `KILLED`.

## Findings from the same-family critiques that are genuinely closed

The corrected `index` template, whole-segment label values and `Exists` selectors match the verified spire-controller-manager behavior. The Gateway `allowedRoutes`/`ReferenceGrant` distinction is correct. The NamespaceTerminating 403 cause is correctly named. The state decision list now states precedence; the `Deleting` fence is the right concurrency shape once its reverse transition and UID holes are removed. Design 06 A3 and design 26 A1 honestly mark the virtual-Pod/host-Pod UID premise unmeasured and forbid implementation before the spike. Those corrections are real; they do not compensate for the independent authority and data-path failures above.

## Verdict

**REVISE — 10 BLOCKER, 10 MAJOR.** Do not implement A42/A60 or the design-03 compiler against this set. The first repair must make the binding authority effective at every consumer (SPIRE, Gateway and workload creation), remove adoption from the crash gap, and make the teardown machine UID-safe and cause-aware. Then complete the hard-mode producer contracts and re-run the review with the repository gate green and each security predicate mutation-killed.
