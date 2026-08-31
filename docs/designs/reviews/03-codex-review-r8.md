# Design 03 / operator integration — Codex cold review r8

- **Snapshot:** `e4bde8f` (the seven commits listed in the request, including `9219d71`)
- **Scope:** design 02 A36–A46, design 03 A36–A43, design 04 A4, design 20 A4–A6, and the named API/controller/revision/tests
- **Verdict:** **REVISE — 13 BLOCKER, 6 MAJOR, 3 MINOR. Do not start design-03 implementation from this contract.**

This verdict is pinned to `e4bde8f`. The shared worktree acquired unrelated,
uncommitted implementation edits while this report was being written. I did not
inspect those edits as fixes, restore them, or stage them.

The r8 set made real progress. The revision digest is now full-width wherever
the current controller actually decides active-versus-candidate, the missing
`egressAllowlist` and env-selector leaves are projected, and the reflected leaf
inventory kills deletion of either. I also agree with the code comment that the
remaining name comparison at `agent_controller.go:228` is an **equivalent
mutant**, not an uncovered decision; the proof and mutation result are below.

That does not make the set safe. Three code paths still let bytes or a workload
that did not earn a gate result serve under an old revision. The new cross-design
amendments then repeat the exact producer/consumer integration failure called
out in the request: A44 has no durable proof that the operator created a run
namespace, A45 has not reached the design that emits routes, `resolvedInputs`
freezes a producer's initial absence, `Withdraw` relies on the status signal the
cluster spike already disproved, and design 04 names witness fields without
defining how the gateway produces trustworthy values for them.

## Closure audit against r7

| r7 finding | r8 result |
|---|---|
| B1 — 40-bit identity | **PARTIAL.** Full digests now drive the normal controller decisions, but legacy adoption and gate/result references still fall back to the short name. BLOCKERs 3 and 5. |
| B2 — `llm.egressAllowlist` omitted | **CLOSED.** Projection removal is killed. |
| B3 — env selector leaves omitted | **CLOSED.** The complete selector is projected and the per-leaf test kills removal. |
| B4 — resolved env content absent in code | **OPEN and explicitly admitted by A43.** BLOCKER 4. |
| B5 — revision material not immutable by reference | **Design direction replaced by A35/A42, not shipped.** The replacement has a provenance defect and incomplete route integration. BLOCKERs 7–9. |
| B6 — retained records did not fit | **CLOSED in the bound, but the GC sentence still names three roles rather than the retained set.** MAJOR 4. |
| B7/B8 — non-spec drift | **PARTIAL.** A41 creates an applied operand, but freezes recovery and puts tenant state in the wrong owner. BLOCKERs 10–11. |
| B9 — fallback witness | **NOT CLOSED.** The envelope acquired nouns, not a producing contract or a replica-complete witness. BLOCKER 13. |
| M1/M2/M4/M5 | **Closed in intent.** The new acquisition order, schema version, rollback-copy rule, and full endpoint key are the right direction. |
| M3 — projection tests aggregate rather than leaves | **CLOSED for inventory, not for reachability.** The new leaf test uses API-invalid specimens. MAJOR 2. |

## BLOCKER findings

### BLOCKER 1 — mutable image tags still change executable code under an unchanged gated revision

**Files:** `api/v1alpha1/agent_types.go:69-73`,
`config/crd/plume.dev_agents.yaml:436-440`,
`internal/controller/agent_controller.go:527-534`,
`docs/designs/02-agent-crd-operator.md:30,362,480-486`

The authoritative design says `runtime.image` is rejected unless it is an exact
lowercase `@sha256:` digest. The API has only `MinLength=1`, the generated CRD
has no image pattern/CEL, and the controller deliberately sets `PullAlways`.
The real-cluster suite itself creates and runs `registry.k8s.io/pause:3.10`.

**Failure scenario:** gate `registry.example/agent:prod` while it resolves to X,
retag it to Y, then drain the node. The replacement Pod pulls Y while the Agent
spec, full revision digest, gate result, and status all still identify X's
revision. No Agent update is required.

**Specific fix:** add exact admission validation for
`@sha256:[0-9a-f]{64}`, correct the false signature-enforcement description,
regenerate both CRDs/chart artifacts, and add envtest cases proving a digest is
accepted and tags are rejected. Delete the rule and require that test to fail.

### BLOCKER 2 — an `AlreadyExists` create race promotes an attacker-created workload

**Files:** `internal/controller/agent_controller.go:355-372,552-565`

After a `GET` returns NotFound, `Create` returning `AlreadyExists` is treated as
success. The next reconcile's availability check fetches by predictable name
and checks only `AvailableReplicas`. It does not verify owner UID, full digest,
or Pod template.

**Failure scenario:** a principal with Deployment create races the operator
between its GET and Create, installs a same-name Deployment whose status is
Available, and wins. A deterministic client-wrapper reproduction promoted that
object and set the attacker's revision active without ever validating what was
running.

**Specific fix:** `AlreadyExists` must never mean success. Re-fetch and require
the full owner/digest/template invariant, or return a collision and requeue.
`workloadAvailable` must also validate UID/owner, full digest, and exact owned
template before its status can authorize promotion. Pin the GET/Create race
with a deterministic wrapper test.

### BLOCKER 3 — the explicit digestless-workload migration reopens the chosen-collision bypass

**Files:** `internal/controller/agent_controller.go:377-390`,
`test/envtest/collision_test.go:357-391`

The controller treats a missing `plume.dev/revision-digest` annotation as a
legacy object to adopt and stamp from the **current** Agent spec. The test pins
that behavior. The migration has no trusted record of which old projection
actually created the Deployment.

**Failure scenario:** safe spec S and malicious spec M share the 40-bit workload
name. While S is active, strip the annotation and update the Agent to M. The
operator derives M's public full digest, rewrites the Pod template to M, and
stamps it. I reproduced this exact sequence; the resulting Deployment ran
`ghcr.io/attacker/backdoor:1.0.0` under S's revision name and gate history.

**Specific fix:** plume is unreleased, so remove the adoption branch and fail
closed on a missing digest. If a future upgrade migration is required, persist
the old full identity before accepting spec changes and remint/regate an
unverifiable object. Never reconstruct legacy identity from current spec.

### BLOCKER 4 — A20/A35/A42 remain design-only, so env content still changes under an old gate

**Files:** `internal/revision/revision.go:18-23`,
`internal/controller/agent_controller.go:624-675`,
`docs/designs/02-agent-crd-operator.md:99-107`

A43 accurately admits this gap: revision hashing sees referent names, and the
workload reads the user-owned object in place. The new
`EnvSourceProtectionUnavailable` condition is useful observability; it is not
enforcement.

**Failure scenario:** edit a referenced ConfigMap from a safe system prompt to
an injected prompt, then replace a Pod. The replacement serves the new content
under the old full digest and old gate result. The actor needs no Agent write.

**Specific fix:** implement the documented order, A20 then A35 then A42, with a
real-cluster test that edits the source and replaces/drains the Pod at every
stage. Keep the condition until the invariant is actually enforced; do not
count the warning as closure.

### BLOCKER 5 — the full digest stops before the gate/result and audit contracts

**Files:** `api/v1alpha1/agent_types.go:341-347,368-396`,
`docs/designs/16-evalsuite.md:42-48,70,93,102-103`

`EvalStatus.Revision`, `CardStatus.Revision`, and
`SupersededCandidates[]` carry only a revision *name*. Design 16 binds the Job,
report, temporary principal, promotion signal, and receipt identity to
“candidate revision” without requiring the full digest. A37 says the name is
not an identity, but that correction did not reach the component that grants
production traffic.

**Failure scenario:** S earns a verdict for short name H. S's workload or status
is later absent, and a malicious colliding projection M presents the same H. A
gate controller comparing only `EvalStatus.Revision == H` can promote M using
S's verdict. The workload collision tests do not protect a decision made before
or outside workload inspection.

**Specific fix:** carry a typed `{name, digest}` revision reference through
EvalRun input, report, `EvalStatus`, `GatesPassed`, candidate principal, events,
receipts, supersession, cards, rollback, and promotion. Add a test proving a
verdict for S's digest cannot authorize colliding M even when no workload exists.

### BLOCKER 6 — the shipped LLM API cannot represent A24's endpoint identity

**Files:** `api/v1alpha1/agent_types.go:169-183`,
`config/crd/plume.dev_agents.yaml:186-212`,
`docs/designs/02-agent-crd-operator.md:50-56,494-500`

The API still exposes flat `[]string` providers/egress allowlist and a fallback
with only `{provider, model}`. The design requires a typed union carrying arm
and instance fields symmetrically for providers, allowlist, fallback, pricing,
and receipts.

**Failure scenario:** two Azure OpenAI deployments use the same arm and model
but different endpoint/deployment/region/BAA posture. They collapse to one CRD
identity, so the compiler cannot prove requested-subset-of-permitted, select the
right fallback, or attribute price/drift without guessing.

**Specific fix:** land the breaking typed endpoint union before design 03,
regenerate CRDs/chart values/examples, and update the revision projection,
golden, and per-leaf tests. Every arm-specific identity leaf must be symmetric
across provider, fallback, allowlist, price, and receipt schemas.

### BLOCKER 7 — A44 has no durable proof that the operator created a run namespace

**Files:** `docs/designs/02-agent-crd-operator.md:109-120`,
`docs/designs/03-policy-compiler.md:139`

The namespace's only stated provenance is the forgeable label
`plume.dev/owned-by: <install-uid>`. Kubernetes does not record “created by this
controller,” and after restart the controller cannot distinguish its namespace
from one pre-created with the same public label. The 8-hex truncation suffix is
also only 32 bits. A44 imports design 03's “collision-checked” rule without
persisting the source namespace name/UID needed to perform that check.

**Failure scenario:** a namespace-create principal pre-creates the predictable
run namespace with the install label and a RoleBinding granting itself access.
After restart, plume accepts it and writes copied Secrets. Independently, two
chosen long source names collide in the 32-bit suffix; the second maps into the
first tenant's run namespace and the labels cannot distinguish them.

**Specific fix:** treat labels as observability, not proof. Persist an
operator-owned binding `{sourceNamespaceName, sourceNamespaceUID,
runNamespaceUID, installUID}` created in the same state machine as Namespace
creation; any unmatched `AlreadyExists` is terminal. Use at least 128 bits (or
the full digest) for the suffix and explicitly test precreation, source
delete/recreate, restart, and a chosen name collision.

### BLOCKER 8 — A45 has not reached the route producer and contradicts its ownership model

**Files:** `docs/designs/02-agent-crd-operator.md:118-126,305,309`,
`docs/designs/03-policy-compiler.md:135-150`

A45 says Service and HTTPRoute move to the run namespace and explicitly marks a
design-03 amendment owed. Design 03 still emits resources “via SSA with
ownerRefs.” A cross-namespace Agent owner reference is invalid. It also defines
neither the route namespace nor the Gateway listener's `allowedRoutes` policy
for those new namespaces.

**Failure scenario:** implement A42/A45 literally. The HTTPRoute either carries
an invalid owner reference and becomes orphaned/unowned, or the operator drops
the reference without a replacement provenance/GC mapping. If the Gateway
listener admits only its former namespace set, the moved route is rejected and
every Agent wedges before publication.

**Specific fix:** amend design 03 before implementation: define each resource's
namespace, listener namespace admission/selector, no-ownerRef provenance,
watch/cache lookup, status ownership, finalizer/sweep behavior, RBAC, and
collision handling. Add conformance for a run-namespace route reaching its
same-namespace Service through the actual Gateway.

### BLOCKER 9 — moving the route breaks the receipt/budget tenancy key

**Files:** `docs/designs/02-agent-crd-operator.md:109-127`,
`docs/designs/04-receipt-tap.md:27-36,49-59,109`

The receipt envelope has `ns`, subjects are `receipts.<ns>.<agent>...`, and the
budget backstop aggregates per Agent. A45 changes the route/workload namespace
from `claims` to `plume-run-claims`, but neither design defines a trusted source
Agent namespace/UID attribute or how the transform obtains it. A4 adds endpoint
and gateway instance only.

**Failure scenario:** the transform derives `ns` from the HTTPRoute/backend or
Pod namespace. Spend lands under `plume-run-claims`, while the Agent and
backstop lookup remain under `claims`; the exact-tier budget sees no spend and
fails open. If it rewrites prefixes heuristically, truncation/hashing and source
namespace recreation make attribution ambiguous.

**Specific fix:** have design 03 stamp the source Agent namespace/name/UID on
emitted resources, and make design 04 define the exact authenticated attribute
and mapping used by the transform. Never infer it from the run namespace name.
An e2e receipt from a moved route must land in the source Agent's stream and trip
that Agent's backstop.

### BLOCKER 10 — immutable `resolvedInputs` freezes a missing producer forever

**Files:** `docs/designs/03-policy-compiler.md:48-75,79-102,309-326`

`resolvedInputs` records every producer output **at revision mint** and is
immutable. `ProducerAbsent` and `Unresolvable` are valid states at that moment.
When the producer later appears or recovers, the non-spec change compares
against that frozen value and lands in `Loosen`/`Unknown`, both of which are
frozen by A37. A no-op Agent write produces the same behavior digest, so there
is no route back into minting.

**Failure scenario:** create an Agent referencing MCP while the producer is not
installed. The record freezes absence. Install the producer and resolve the
tool; the Agent remains `CapabilityUnavailable` indefinitely because the only
authoritative record is immutable and the new capability is forbidden from
entering it.

**Specific fix:** do not establish the immutable applied baseline until all
serving-required bindings resolve and the candidate is publishable, or define a
typed provisional record whose resolution fields may transition only until
first successful publication. Producer-absent → installed → resolved must be a
tested recovery path, with any newly granted behavior still passing its gate.

### BLOCKER 11 — tenant budgets are persisted and transacted under the wrong owner

**Files:** `docs/designs/03-policy-compiler.md:57-59,91,336-350`,
`docs/designs/26-tenant-cr.md:35-43`

Design 03 places `consumerBudgets` inside each Agent's immutable
`RevisionRecord.resolvedInputs` and runs a per-route `Withdraw`. Design 26 says
the producer is a separate tenant-scoped `TenantPolicyIntent`. One shared
tenant limit cannot have N independently authoritative per-Agent applied
operands or transactions.

**Failure scenario:** lower one tenant's budget while 100 Agents serve. Some
Agent records freeze the old value, some withdraw, some fail. No object owns the
set-wide desired/applied state, so the compiler can report completion while a
subset still serves the old quota, and concurrent Agents can overwrite a shared
tenant policy.

**Specific fix:** define a `TenantPolicyRecord` and apply status on the Tenant
compiler target, with its own schema version, digest, route membership snapshot,
transaction, retry/partial-failure semantics, and exact-tier rollup. Remove
tenant budgets from the Agent revision record; Agents reference the applied
tenant policy identity rather than owning copies of it.

### BLOCKER 12 — `Withdraw` declares success from the same signal proven to lie on NACK

**Files:** `docs/designs/03-policy-compiler.md:263-280,343-355`

The measured dependency behavior says current-generation Accepted/Attached can
coexist with a dataplane NACK while the old permissive config keeps serving.
`Withdraw` nevertheless sets weight to zero, waits for control-plane generation
convergence, and then assumes the route is no longer serving. The Event has no
generation, and absence of an Event is not success. This is the deleted witness
problem in another form.

**Failure scenario:** a budget drops from 1000 to 1. The weight-zero update
NACKs; the old route continues serving 1000. Plume observes current-generation
status, applies the tightened policy to what it calls a non-serving route, and
records/retries from a state that never existed.

**Specific fix:** do not proceed on this status tuple. Use an independently
verifiable fail-close primitive—for example withdrawing/removing the selected
ready endpoints and positively verifying the dataplane has none—or state that
online withdrawal is best-effort and redesign the producer lifecycle around a
new isolated route/revision. Add a conformance case in which the withdrawal
NACKs and old traffic persists; the transaction must not advance.

### BLOCKER 13 — A4/A43 add witness fields but no trustworthy producer or replica-complete proof

**Files:** `docs/designs/03-policy-compiler.md:365-378`,
`docs/designs/04-receipt-tap.md:20-36,104-109`

Design 04 adds `hop.endpoint` and `hop.gatewayInstance` to JSON, but does not
name the exact agentgateway OTLP attributes, their stability, or a plume-owned
stamping mechanism that produces them. No current code or vendored conformance
asserts either. Even if `gatewayInstance` is a Pod identity, “distinct count ==
declared replicas” can be satisfied by dead, old Pod IDs during rollout while a
current replica remains unwitnessed; normal Service load balancing cannot force
one request through every replica.

**Failure scenario:** two canaries hit one current gateway Pod and one stale Pod
identity retained in receipts. The count reaches two for a two-replica Gateway,
`llmFallbackActive` becomes true, and the unwitnessed current replica continues
sending production traffic to the drifted primary.

**Specific fix:** design 04 must name and pin the exact producer attributes and
trust boundary. Snapshot the current Ready gateway Pod UIDs after the route
generation, restart the witness on membership change, and target every current
replica through the same production route (or explicitly admit that the proof
cannot be made). Conformance must cover two same-arm endpoints, two replicas,
restart/churn, and attribution from raw OTLP to the envelope.

## MAJOR findings

### MAJOR 1 — the condition vocabulary is an inventory, not an API boundary

**Files:** `api/v1alpha1/agent_types.go:457-500`,
`api/v1alpha1/schema_contract_test.go:150-230`

The new test correctly kills deletion/rename inside `designConditions()`, but
`[]metav1.Condition` still accepts arbitrary strings and condition writers take
`string`. Adding `c.set("TotallyWrongName", ...)` to a reachable controller path
compiled and the controller/API/envtest suite passed.

**Failure scenario:** a typo emits `PolicyApplyIncompelete`; alerting and CLI
consumers watch `PolicyApplyIncomplete`, so the degraded path is silent even
though the inventory test remains green.

**Specific fix:** enforce the enum at the CRD status boundary with CEL (or a
validated condition type), and add an envtest status update proving an unknown
type is rejected. Also statically inspect condition emission sites so a typo
cannot wait until runtime to fail.

### MAJOR 2 — the per-leaf classifier uses API-invalid specimens

**Files:** `internal/revision/leaf_test.go:312-344`,
`docs/designs/02-agent-crd-operator.md:239-255`

Each case starts from a zero `AgentSpec` and changes one leaf. Many resulting
specimens violate exactly-one-of runtime/external, enum, URL, or other schema
rules. The test proves that serialization contains a Go field; it does not prove
the classification of a reachable API transition, despite the design requiring
API-valid perturbations.

**Failure scenario:** defaulting or validation makes a leaf unreachable in the
form being hashed, or normalizes two valid forms differently from the raw Go
values. The test stays green while the real API path bypasses or wastes a gate.

**Specific fix:** construct a valid base and valid sibling specimens per leaf,
then validate both through the generated schema/envtest before comparing hashes.
If no valid perturbation exists, report it explicitly as INVALID rather than
claiming KILLED.

### MAJOR 3 — deleting a shared run namespace has no concurrency protocol

**File:** `docs/designs/02-agent-crd-operator.md:109-120`

One run namespace is shared by all Agents in a source namespace, but its GC rule
ends at “deleted when its last Agent is.” No namespace-scoped lock/refcount or
create-vs-delete protocol is specified.

**Failure scenario:** finalizer A observes itself as last and begins deleting the
run namespace while Agent B is concurrently created. B writes material/workload
before deletion completes; namespace deletion then removes B's live revision.

**Specific fix:** specify a namespace-scoped Lease/CAS refcount and a terminating
state. Creation must refuse/retry while teardown owns the namespace, and deletion
must recheck source namespace UID and the Agent membership after acquiring the
lock. Test a paused delete racing a new Agent.

### MAJOR 4 — RevisionRecord GC still contradicts the retained-set contract

**File:** `docs/designs/03-policy-compiler.md:65-73`

The size row correctly allocates one record per member of the retained set,
including history and holds. The GC row still deletes when a revision leaves
“all three roles.” History and holds are not three roles and are exactly why A36
changed the bound.

**Failure scenario:** a revision leaves active/candidate/named rollback roles but
remains held for a task or history retention. Its record is deleted, and a later
rollback/restart cannot reconstruct the revision the retention contract says is
available.

**Specific fix:** define GC solely as absence from the authoritative retained-set
union and delete record/material/resource references atomically. Test the maximum
overlap of active, candidate, history, and multiple holds.

### MAJOR 5 — short revision names remain in durable audit status

**Files:** `api/v1alpha1/agent_types.go:341-347,368-396`

Even apart from gate authorization (BLOCKER 5), cards and superseded candidates
remain keyed only by a chosen-collidable 40-bit name. Two distinct revisions
with H cannot be distinguished in audit status or cleanup decisions.

**Failure scenario:** an operator investigates a collision/supersession event and
cannot tell which projection a card or abandoned candidate belongs to; cleanup
may retain/delete the wrong associated record.

**Specific fix:** use one `RevisionRef{name,digest}` everywhere revision identity
is persisted. Printer columns may remain short; state and events may not.

### MAJOR 6 — the 8 KiB record bound is not backed by producer bounds

**File:** `docs/designs/03-policy-compiler.md:57-69`

`resolvedInputs` includes every resolved tool name, KG endpoint, identity policy,
and tenant budget. The design sets an 8 KiB aggregate cap but none of those
producer contracts has a compatible cardinality/size bound.

**Failure scenario:** a legitimate MCP server advertises a large tool catalog or
a tenant has many consumer budgets. The Agent becomes `PolicyCompileFailed`
solely because plume invented a status-storage cap that the producer was never
required to meet.

**Specific fix:** propagate explicit per-field bounds to producer schemas and
prove the worst case fits, or store a content-addressed immutable artifact and
keep only its digest/reference in status. Add a maximum-shape size test.

## MINOR findings

### MINOR 1 — the degradation message understates who can mutate immutable sources

**File:** `internal/controller/agent_controller.go:671-675`

The condition says “anyone with update” and recommends restricting update.
Immutable ConfigMaps/Secrets can still be deleted and recreated by a principal
with delete/create, preserving the name.

**Specific fix:** name `update`, `delete`, and `create`, and say explicitly that
the condition is observability, not a compensating control.

### MINOR 2 — canonical ordering is not total for duplicate-key list entries

**File:** `internal/revision/revision.go:327-334`

Tools sort only by name and knowledge bindings only by name/version. The API does
not enforce uniqueness, so two same-key entries with different approval/scope
retain input order. Reordering a semantically identical set can mint a revision.

**Specific fix:** enforce map-list uniqueness in the CRD, or define a total
comparator over every canonical field and reject conflicting duplicates.

### MINOR 3 — the real-cluster suite carries a stale skipped duplicate

**File:** `test/e2e/e2e_test.go:280-294`

`TestServiceAccountDefaultingDoesNotCauseChurn` always skips claiming the
operator is not deployed, while `TestOperatorDoesNotChurnAgainstRealAdmission`
immediately above now performs that assertion against the deployed chart.

**Specific fix:** delete the stale duplicate. A passing e2e run should not print
a false coverage gap.

## Mutation and execution ledger

All source mutations were made against backup copies and restored from those
copies, never with `git checkout`. A compile/build failure would have been marked
**INVALID**; none of the mutations below was INVALID.

| # | Mutation / attack | Result | Evidence |
|---|---|---|---|
| 1 | Delete assignment of `llm.egressAllowlist` from the behavior projection | **KILLED** | Compiled; golden, explicit add/remove/empty, and per-leaf tests failed. |
| 2 | Delete `budget.usdPerDay` from the projection | **KILLED** | Compiled; golden and per-leaf tests failed. |
| 3 | Change the no-workload gate branch from full digest comparison to the 40-bit name | **KILLED** | Compiled; `TestACollidingSpecIsGatedEvenWhenNoWorkloadExists` failed. |
| 4 | Change `ActiveRevisionDigest == desiredDigest` at snapshot line 228 to active-name equality | **SURVIVED — EQUIVALENT** | Full envtest passed. At that branch, the preceding `!ready && activeDigest != desiredDigest` case is false. If active is empty, paired digest is empty and both comparisons are false; if active is non-empty, the only reachable alternative is `activeDigest == desiredDigest`, whose paired name is the digest prefix, so both are true. Legacy non-empty-name/empty-digest takes the preceding branch. The mutant cannot alter a reachable result. |
| 5 | Suppress the promotion log line | **SURVIVED — LOG ONLY** | Controller/envtest passed. It guards no state transition or security property; no finding. |
| 6 | Remove `CondRevisionMaterialUnavailable` from `designConditions()` | **KILLED** | Vocabulary closure test failed by exact name. |
| 7 | Delete the `assessEnvSourceProtection` call | **KILLED** | Envtest failed on the abnormal-true condition transition. |
| 8 | Emit raw condition type `TotallyWrongName` from a reachable controller path | **SURVIVED** | Controller, API, and full envtest compiled and passed. MAJOR 1. |
| 9 | Strip a live workload's digest annotation, switch to a malicious chosen-colliding spec, reconcile | **REPRODUCED** | Workload was rewritten to `ghcr.io/attacker/backdoor:1.0.0`. BLOCKER 3. |
| 10 | Force GET→Create race: wrapper creates an attacker Deployment and returns `AlreadyExists` | **REPRODUCED** | Reconcile accepted the object; availability promoted the revision (`7d9a4b71b4`). BLOCKER 2. |
| 11 | Submit and run a mutable-tag image through the real cluster | **REPRODUCED** | Generated CRD accepted it; e2e ran `registry.k8s.io/pause:3.10`. BLOCKER 1. |

Baseline execution at `e4bde8f`:

- `make test` — **PASS**
- `make verify` — **PASS**; generated artifacts reproducible
- `make e2e` — **PASS** on a newly-created k3d cluster; workload became
  Available and the admission-defaulting no-churn transition passed

The green baseline therefore does not contradict the findings. It is direct
evidence for BLOCKER 1, and mutations 8–10 show three behaviors the green suite
does not reject.

## Disagreement with prior reviews

I disagree with `docs/designs/reviews/02-review.md`'s old PASS as evidence about
this snapshot. That review correctly fixed the original circular hash and
candidate-route principal, but it predates A21/A37/A42 and did not follow the
revision identity through image admission, EvalStatus, or migration. The real
CRD accepting a mutable tag and the two reproduced controller attacks outweigh
that verdict.

I agree with `02-recritique.md` that cross-design consumers must be checked
against the object that actually produces their fields, but disagree that its
later PASS can carry forward: the producer/consumer contracts changed again.
A41/A42/A43 introduce new unresolved inputs, namespaces, routes, and receipt
fields that review never assessed.

I agree with the independent same-family critique that A42 broke owner UID,
route namespace, `resolvedInputs`, and the receipt envelope. I disagree that
A44/A45/03-A41/03-A42/04-A4 close those failures. They mostly move the nouns:
the namespace still lacks durable provenance, design 03 still emits ownerRefs,
the route move has no receipt identity mapping, producer recovery is frozen,
withdrawal still trusts a disproved signal, and the witness fields still lack a
specified trustworthy producer. Those are implementation blockers, not polish.
