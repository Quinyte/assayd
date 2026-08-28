# Design 03 / design 02 amendment set — Codex fifth review

**Verdict: REVISE — 7 BLOCKER, 14 MAJOR, 1 MINOR. Do not start design-03 implementation.**

Review snapshot: `588db34`, covering `7eb05bd..588db34` after the fourth
Codex review at `a74085b`. I read the current authoritative bodies, amendments
A19–A27, designs 07/20/25, ADR-0028, canonical architecture, both v1.4.1
research artifacts, the new conformance code, the docs gate, and the r4 review.
I ran `make test` and `make conformance-cluster`, then mutation-checked the new
hermetic CRD assertions and repaired docs gate. Every mutation was restored
from a backup; the worktree was clean before this file was written.

The set has moved materially and several decisions are better. Withdrawing the
unattributable witness is correct. Rejecting in-place tightening is the right
direction. A26 finally specifies a concurrent finalizer update protocol and
records the naive append result instead of calling CAS automatic. A27 closes
the operator data-write hole. A20 removes an unverified gateway-order claim.
A21 gives pricing the endpoint identity A24 requires. A24's MCP bound handling
is implementable.

That is not the implementation threshold. The replacement for A17 is not
representable by the compiler input: `PolicyIntent` has many revisions but one
global `budget` and `expose`, so it cannot keep R1's policy serving while it
builds R2's changed policy. The stale-state restart still skips
`PreparingRoute`; A19's claim that this finding dissolved is false. Non-spec
producers, especially `fallbackActive`, still recompile a live Backend and have
no revision path. A26 calls guard-loss exposure prospective even though the
editor-plus-Pod-replacement race can realize it before the controller reacts.
Sigstore presence does not prove the workload namespace is opted into
verification. A26's lease name also depends on the revision digest which can
only be computed after that lease seals the source. Finally, the new
real-cluster gate fails on the NACK behavior on
which the design relies, while the test named `RetainsOldConfig` never creates
old valid config or sends a request.

## Closure ledger from r4

| r4 finding | r5 judgement |
|---|---|
| BLOCKER 1–2 (quiesce/witness attribution) | **CLOSED by withdrawal.** A19 removes the mechanism instead of pretending negative HTTP outcomes prove causation. |
| BLOCKER 3 (stale restart skips safety stage) | **OPEN.** `03:118` still unconditionally restarts at `ApplyingBackends`; see BLOCKER 2. |
| BLOCKER 4 (revision escape unavailable) | **NOT CLOSED.** A25 reclassifies two fields, but the multi-revision compiler contract cannot represent their old and new values simultaneously; see BLOCKER 1. |
| BLOCKER 5 (seal refcount undrafted) | **DESIGN PRESENT, but circular.** The retrying read-modify-write is a real mechanism; its lease name requires a revision digest not available until after acquisition. See BLOCKER 7. |
| BLOCKER 6 (guard loss exposes retained revisions) | **OPEN.** A26 detects the compromise after it happens; it does not close the exposure window; see BLOCKER 4. |
| BLOCKER 7 (Azure OpenAI has no model) | **CLOSED.** The arm-specific rejection is now consistent with the pinned CRD. |
| MAJOR 1 | **NOT FOLDED.** A24's `{requested,resolved,state}` exists only in the amendment log; the authoritative `PolicyIntent` still has resolved slices only. |
| MAJOR 2, 4–6, 9, 12–15, 18 | **CLOSED in design.** The witness is gone; operator data mutation is denied; the condition/replica contracts, endpoint drift identity, recursion guard, MCP bounds, gateway-scoped tracing, backendRefs shifting, and order disclaimer are present. |
| MAJOR 3 | **NOT CLOSED.** A24 says “per-concern registry,” but no concern ordering is specified and the body still uses the rejected admitted-set rule. |
| MAJOR 7–8 | **AMENDMENT CORRECT, BODY CONTRADICTS IT.** A21 says two tuple-keyed ConfigMaps; §3.5 still says one ConfigMap, calls the tuple conversion owed, and assigns internal writes to it. |
| MAJOR 10 | **PARTIAL.** A static base catalog is better; profile-added mandatory entries have no independent catalog contract. |
| MAJOR 11 | **PARTIAL AND WRONG FOR REQUIRED CAPABILITIES.** Aggregation exists, but every tool/KG/LLM route is called optional and compile failures receive an apply-failure reason. |
| MAJOR 16, MINOR 1–2 | **OPEN.** Multiple authoritative and canonical statements still contradict the amendments, and the status headers remain stale. |
| MAJOR 17 | **PARTIAL.** A controller was chosen, but presence is not effective enforcement and the version/selector are still owed. |
| MAJOR 19 | **PARTIAL.** Independent rule fixtures kill deletion and regex weakening, but Markdown links still split banned phrases. |
| MAJOR 20 | **CLOSED for disclosure, incomplete for operation.** HMAC removes the guessing oracle; unversioned key rotation can fail the fleet. |

I disagree specifically with A19's statement that r4 BLOCKER 3 was
“dissolved.” Withdrawal removes `Quiescing`, but a `Create` still begins with
`PreparingRoute`, and line 118 still restarts a stale `Create` after that stage.
I also disagree with A26's “prospective” characterization: an absent Binding,
a permitted source edit, and a Pod replacement are sufficient to serve changed
material before the watch reaction. Those are mechanism contradictions, not
severity disagreements.

## BLOCKER findings

### BLOCKER 1 — the compiler cannot represent different policy for active and candidate revisions

**Files:** `docs/designs/03-policy-compiler.md:23-34,194-211`;
`docs/designs/02-agent-crd-operator.md:430-441`.

`PolicyIntent` contains `revisions[]`, but `budget`, `expose`, tools,
knowledge, and LLM policy inputs occur once at Agent scope. A25 now says any
budget or expose change creates a candidate with its own routes and policies
while the old route is never mutated. One global value cannot describe R1's
applied auth/budget and R2's desired auth/budget at the same time.

**Concrete failure:** R1 serves `auth: oauth`. The user changes to
`auth: none`, producing held candidate R2. The operator constructs the one
documented `PolicyIntent` from current spec, so both revision entries share
`expose.auth:none`. A pure compile either rewrites R1's serving policy before
R2 passes its gate, or preserves R1 and cannot compile R2. The widening can
therefore reach production ungated. The same mismatch affects the receipt
backstop: if R1 is held at a $10 budget and desired R2 raises it to $100, a
backstop comparing spend with current spec can clear R1's hold before R2 is
promoted.

**Specific fix:** make policy material revision-scoped. Either compile one
`PolicyIntent` per revision or change `revisions[]` to carry the normalized,
retained behavior-policy projection (including budget/expose and every other
revisioned compiler input). Persist the applied projection or `ResourceSet`
under the revision hash; never reconstruct an active revision from current
Agent spec. Key the backstop and route reconciliation to active-revision
material, with candidate eval using candidate material. Add an e2e transition
in both directions for auth and budget and prove R1 resources are byte-stable
until promotion.

### BLOCKER 2 — stale `Create` transactions still skip `PreparingRoute`

**File:** `docs/designs/03-policy-compiler.md:105-120,206-209,529-531`.

The stage table and transaction table require every new revision to begin at
`PreparingRoute`. Line 118 still drops stale state and restarts unconditionally
at `ApplyingBackends`. A19 removed `Tighten`; it did not remove the first stage
of `Create`.

**Concrete failure:** generation N has a candidate in `ApplyingPolicies`.
Generation N+1 supersedes it and names a new candidate route. The digest check
drops N's record, starts N+1 at `ApplyingBackends`, then attaches policies to a
route which has never been created. The spike already established that this
reports `Attached=False`; the state machine deadlocks on the same cold-create
barrier A17 was written to fix.

**Specific fix:** stale-state recovery must recompute the transaction and enter
its first stage. `Create` always restarts at `PreparingRoute`; only a proven
`Loosen` may start at `ApplyingBackends`. Persist the route identity/set used by
the transaction. Mutation-test supersession from every stage, including a new
route name, and assert that the first emitted write is the inert route.

### BLOCKER 3 — A19 has no revision path for non-spec producers and design 20 still mutates a serving Backend

**Files:** `docs/designs/03-policy-compiler.md:38-53,194-211,235-238`;
`docs/designs/20-drift-controllers.md:18-34,70-76`.

The provenance table explicitly includes inputs that are not Agent spec:
advertised tool sets, resolved KG endpoints, tenant consumer budgets, and
`status.llmFallbackActive`. The classification says anything not proven a
`Loosen` becomes a spec change that mints a revision. Those producers cannot
perform that spec change. Design 20 deliberately flips status and recompiles
the existing LLM Backend with no revision or gate. A model swap is not a
provable loosening under any admitted-set ordering.

**Concrete failure:** model drift sets `fallbackActive=true`. The operator
recompiles the Backend attached to the serving route. If agentgateway NACKs the
new provider block, the drifted primary stays live while plume records an
automated remediation. If it applies, a different answering model reaches
production without the candidate isolation A19 now claims every tightening
uses. Similarly, a tool server removing an advertised tool narrows a mandatory
filter but produces no Agent spec revision.

**Specific fix:** enumerate every `PolicyIntent` producer and assign a real
transaction. Inputs that can change behavior or tighten policy need a
content-addressed policy-input revision/candidate independent of who wrote the
input. If status-driven fallback remains the deliberate ADR exception, say
that A19 does not cover it and define a separate pre-provisioned, positively
verified cutover plus honest failure status. `Unknown -> Tighten` cannot point
at a spec mutation unavailable to the producer.

### BLOCKER 4 — guard-loss handling detects the bypass after changed material may already serve

**File:** `docs/designs/02-agent-crd-operator.md:443-470`.

A26 allows serving to continue while the admission guard is absent as long as
the last comparison still matches. That comparison and the later weight-zero
action are controller reactions. The spike established that removal of the
Binding makes data updates immediately admissible. A finalizer prevents final
deletion; it does not block `data` mutation when the admission policy is gone.

**Concrete failure:** a Helm upgrade temporarily removes the Binding. A user
who is legitimately allowed to edit the ConfigMap changes a system prompt and
deletes an R1 Pod (or a node drains) before the operator processes the source
watch. The replacement Pod reads changed data under R1's old gated hash and
route. The next reconcile detects divergence and sets weight 0, but the bypass
has already served requests. “Exposure is prospective” is false.

**Specific fix:** planned guard transitions must be overlap-only: establish and
verify the replacement guard before removing the old one. Unexpected guard
loss needs an independent invariant that blocks Pod creation or route serving
without relying on the same reconcile race. If no admission mechanism can
validate referenced content at Pod admission, use immutable revision snapshots
for behavior-bearing material or immediately deny all affected workload
admission/traffic through a separately protected control. Do not claim the
seal is fail-closed until the Binding-loss + edit + Pod-replacement race is
kubelet-tested.

### BLOCKER 5 — Sigstore “present” does not mean signatures are being verified

**Files:** `docs/designs/07-umbrella-chart-ci.md:92-105`;
`docs/designs/02-agent-crd-operator.md:495-503`.

A2 checks whether policy-controller is present and derives
`ImageSignatureUnverified` or compliance install success from that. Sigstore's
official policy-controller documentation says enforcement is per namespace and
only applies to namespaces which opt in with
`policy.sigstore.dev/include=true`. Presence therefore proves neither that a
workload namespace is selected nor that a matching enforcing
`ClusterImagePolicy` and authority exist. The pinned version and namespace
selector are explicitly still owed. See the
[official policy-controller overview](https://docs.sigstore.dev/policy-controller/overview/#configure-policy-controller-admission-controller-for-namespaces).

**Concrete failure:** policy-controller is installed and healthy, so the chart
passes a HIPAA profile and Agents do not receive
`ImageSignatureUnverified`. The plume workload namespace lacks the opt-in label
or has no matching enforcing image policy. An unsigned digest-pinned image is
admitted. The platform reports provenance verified when nothing checked it.

**Specific fix:** define and verify the effective enforcement contract, not
controller presence: pinned compatible version, ready webhook with
`failurePolicy: Fail`, namespace selector/opt-in, a matching enforce-mode
policy, no-match behavior, and the required signing identity/authority. Under a
compliance profile, install must fail if any element is absent. At core, set the
condition whenever effective enforcement is absent or unverifiable. The
integration test must admit a signed fixture and reject an unsigned image in
the exact namespace an Agent workload uses.

### BLOCKER 6 — the new cluster conformance gate neither reproduces nor tests old-config retention

**Files:** `test/conformance/cluster_test.go:180-230`;
`hack/conformance-cluster.sh:34-46`;
`docs/designs/03-policy-compiler.md:180-188`.

`make conformance-cluster` failed at `588db34`: both invocations in the script
timed out waiting for `AgentGatewayNackError`. More fundamentally,
`TestNackIsOnlyAnEventAndRetainsOldConfig` creates a poisoned policy from
scratch. It never creates a valid permissive policy, never proves traffic, never
patches that policy to the invalid tightening, and never sends a post-NACK
request. No assertion observes retained old configuration; the final assertion
rechecks the same cached control-plane condition checked twenty lines earlier.

**Concrete failure:** an upstream change drops old config or blackholes the
route on NACK. This test still passes whenever an Event appears, because it
observes no traffic. At the reviewed snapshot the Event did not appear at all,
so the design's mandatory Event watcher is not currently backed by its claimed
reproducing gate.

**Specific fix:** start a live backend; apply a valid permissive rate policy;
prove traffic and its limit; patch the same policy generation to the invalid
tightening; require the generation-correlated NACK evidence available from the
actual Event; then send enough requests to distinguish the retained old rule
from the desired new rule and from a blackhole. Preserve cluster logs on
failure. Run the suite once from a fresh cluster and fail on that result. Until
this reproduces, do not treat the Event schema or retention behavior as a
pinned implementation contract.

### BLOCKER 7 — the seal lease needs the revision hash before it can safely compute that hash

**File:** `docs/designs/02-agent-crd-operator.md:443-454`.

Each finalizer is named `src.<agent-uid8>.<revision8>`, but the same section
requires “seal first, then read”: source content is re-read and hashed only
after the finalizer is observed. A20 makes that content part of the revision
digest. `revision8` therefore does not exist when the operator must add the
finalizer which makes computing it safe. This recreates the circular revision
identity defect the design history already records.

**Concrete failure:** an implementer computes the revision hash first so it can
name the finalizer. A source editor changes the ConfigMap between that read and
the finalizer write; the candidate is named for old content but its Pod reads
new content. If the implementer obeys seal-first instead, it has no revision ID
with which to acquire the promised lease and must invent an undocumented
placeholder whose crash/GC behavior A26 does not cover.

**Specific fix:** give acquisition an identity independent of behavior content,
such as a random durable lease UID recorded on the Agent before the source
write. Add/observe that provisional lease, then read and hash the sealed source,
then atomically associate the lease UID with the resulting revision. If the
finalizer must be renamed, add the final name before removing the provisional
one under `resourceVersion`; crashes at every step must leave at least one
valid hold, and the orphan sweeper must understand both states.

## MAJOR findings

### MAJOR 1 — A24's resolution-state fix is absent from the authoritative input type

**Files:** `docs/designs/03-policy-compiler.md:23-53,538-542`.

The amendment says bindings now carry `{requested,resolved?,state}`, but the
authoritative `PolicyIntent` still contains only resolved `tools[]`,
`knowledge[]`, and LLM slices. An implementer reading the body still cannot
distinguish “not requested” from “requested but unresolvable.” Fold the state
into the type for every resolved binding, state which layer rejects each state,
and test all states independently.

### MAJOR 2 — the comparator registry has no specified comparators and the body retains the rejected rule

**Files:** `docs/designs/03-policy-compiler.md:206-211,538-543`.

A24 promises an explicit ordering per concern but enumerates none. The body
still says “every mandatory concern's admitted set is a superset,” the exact
rule A24 calls unimplementable. `Unknown -> Tighten` is also meaningless after
A19 unless a real revision producer exists. List the normalized comparison for
each live-change concern, including defaults and first application, or delete
`Loosen` and route all changes through the revision mechanism. Property tests
cannot substitute for defining the relation they test.

### MAJOR 3 — classifying all tool, KG, and LLM routes as optional reports broken declared capabilities Ready

**Files:** `docs/designs/03-policy-compiler.md:134-152`;
`docs/designs/02-agent-crd-operator.md:246-270`;
`docs/architecture.md:128-152`.

An Agent may be able to answer HTTP while every declared tool or its sole
knowledge source is unavailable, but that is not semantic readiness. Canonical
architecture says Agents bound to a non-Ready graph get no traffic, and design
02 says an invalid sole knowledge source means no traffic. A24 instead leaves
`Ready=True` for every tool/KG/LLM route failure. Make requiredness part of each
declared binding/capability and aggregate by the Agent's actual contract; at
minimum, a sole required knowledge or LLM path must withhold Ready/traffic.

### MAJOR 4 — compile failures are reported with an apply-failure reason

**File:** `docs/designs/03-policy-compiler.md:136-144,423-433`.

The aggregation table gives every serving-required route failure reason
`PolicyApplyIncomplete`, while the failure table correctly assigns unresolved
input and missing mandatory input to `PolicyCompileFailed`. Consumers will be
given a plausible cause never checked. Aggregate conditions by cause: compile
failure remains `PolicyCompileFailed`; accepted/attachment/deadline failure is
`PolicyApplyIncomplete`; Ready's reason must identify the actual winning cause
under a documented precedence.

### MAJOR 5 — truncated finalizer identities can collide and merge independent holds

**File:** `docs/designs/02-agent-crd-operator.md:443-452`.

`src.<agent-uid8>.<revision8>` provides only 64 combined bits and Kubernetes
treats finalizers as a set. Two holders which collide share one string; the
first release removes the second holder's lease and unseals a still-retained
source. Use a sufficiently long digest of the full Agent UID plus full revision
identity sized to the qualified-name segment limit, and collision-test it. A
collision must be detected/refused, never silently coalesced.

### MAJOR 6 — orphan sweeping can race a lease which has been acquired but not yet materialized

**File:** `docs/designs/02-agent-crd-operator.md:445-452`.

Acquire order adds the finalizer before the revision record/resource exists,
while startup sweeping removes entries whose revision does not exist. A leader
loss in that interval lets the new leader classify a valid in-progress hold as
orphan. Specify sweep-before-reconcilers ordering plus a durable acquisition
intent, or use a grace/owner state that cannot overlap a live acquisition. Test
operator death after each acquire step and concurrent startup reclamation.

### MAJOR 7 — HMAC key rotation is not an atomic “one pass”

**File:** `docs/designs/02-agent-crd-operator.md:452-464`.

Agent statuses cannot be rewritten atomically across the fleet. If the cluster
key changes before every retained digest is rewritten, unchanged sources fail
comparison and are taken to weight 0; if it changes after, newly written
digests cannot be verified with the old key. Key loss has no state contract.
Record a key ID beside each digest, retain old verification keys, write new
digests before retiring old keys, and define missing-key behavior and recovery.
Mutation/e2e must interrupt rotation halfway.

### MAJOR 8 — static security cases do not pin compliance-profile additions

**File:** `docs/designs/03-policy-compiler.md:158-172,536-537`.

The static base catalog is independent of the base registry, but HIPAA and
future profiles extend mandatory entries dynamically and are explicitly absent
from the table. Deleting the profile's `-guard` extension can leave every base
case green. Require an independent security-case catalog for each shipped
profile/pack and mutate each profile-added entry with its catalog untouched.

### MAJOR 9 — “required, not bundled” is not an operational dependency or an honest pod budget

**File:** `docs/designs/07-umbrella-chart-ci.md:92-105`.

The text says Sigstore is not installed, then says the chart declares the
dependency; a Helm dependency is installation/bundling, while an external
prerequisite needs a versioned capability check. It also calls the cost zero
only by moving required compliance pods outside plume's ledger. That may be a
reasonable doctrine choice, but it is not a zero-cost compliance profile, and
an organisation running Kyverno is in fact forced to install a second engine.
Choose one precise contract: bundled pinned subchart, or external prerequisite
with compatible versions and preflight. Account the required pods in the
profile's total operational weight even if plume does not own them.

### MAJOR 10 — A21 is contradicted inside the authoritative pricing section

**File:** `docs/designs/03-policy-compiler.md:333-375,534-536`.

The amendment says two tuple-keyed ConfigMaps. The body opens with one
`plume-model-pricing`, line 365 says tuple-keying is still owed, and line 373
says the model operator writes internal rows into that same map. Those are the
two r4 defects A21 claims to close. Fold the two concrete schemas, ownership,
merge/tie behavior, and staleness source into §3.5; delete the owed sentence and
single-writer instructions.

### MAJOR 11 — the hermetic CRD tests survived two valid dependency mutations

**File:** `test/conformance/crd_contract_test.go:157-205`.

Replacing every `Hours` enum value with `Weeks` and updating the pinned archive
digest still passed: the test asserts only length three and absence of `Day`.
Weakening both ExactlyOneOf CEL rules from `size() == 1` to `size() >= 1` also
passed: the test searches only for the words `requests` and `tokens`. These are
compiled, valid mutations, not build failures. Assert the exact normalized enum
set and parse/compare the exact CEL semantic expression (or exercise CRD
admission with both fields). Apply the same exactness to numeric minimum values,
not merely their presence.

### MAJOR 12 — the cluster harness runs a stateful suite twice and its inert-route test never checks HTTP 500

**Files:** `hack/conformance-cluster.sh:40-46`;
`test/conformance/cluster_test.go:103-177`.

The first `go test` is piped through grep and ignored; the whole suite is then
run again against the resources and Events the first run left behind. A stale
NACK Event can satisfy the enforcing run. Tests also depend on `conf-gw` and
`conf-route` created by earlier tests, so filtered execution does not establish
its own preconditions. Finally, `TestInertRouteIsAttachableAndAnswers500`
asserts only route status and sends no request. Run once with `tee` while
preserving the test exit code, isolate fixtures per test, and actually request
the inert path and assert 500 if that behavior remains a design claim.

### MAJOR 13 — Markdown-link syntax still bypasses the repaired documentation gate

**File:** `test/docs/superseded_test.go:181-195,294-320`.

The gate strips emphasis markers but not link destinations. A temporary
authoritative design line saying `The acceptance [poll](...) verifies each
emitted resource` passed `go test ./test/docs/...`, while the rendered sentence
states the banned acceptance-poll design. Normalize Markdown to rendered text
with a parser, not a growing punctuation regex, and add link/image/code-span
evasion fixtures independently of production rules.

### MAJOR 14 — the propagation sweep again left canonical contradictions

**Files:** `docs/designs/02-agent-crd-operator.md:103-128,219-229,246-275`;
`docs/designs/03-policy-compiler.md:186-211,333-375`;
`docs/architecture.md:180-196,260-275,438-494`.

Examples in authoritative text still say env sources are hashed by referent,
the gateway minimum carries virtual models, signature verification is
“Sigstore or Kyverno,” and a tightening update must make a serving route inert.
Canonical architecture still calls signature admission a built-in zero-pod
VAP, says traffic shifts through virtual models, lists ADR-0020 as live, and
says nothing is execution-validated. An implementer following the body builds
the withdrawn system. Perform a corpus-wide semantic propagation pass for
A19–A27 and add independent fixtures for each retracted load-bearing statement;
the current lexical gate does not cover these phrases.

## MINOR finding

### MINOR 1 — both design status headers predate the amendments under review

**Files:** `docs/designs/03-policy-compiler.md:1-6`;
`docs/designs/02-agent-crd-operator.md:1-6`.

Design 03 still says A1–A16 and r2's findings are current; design 02 says
A1–A19 are folded and A20–A21 are owed. Update the snapshot/amendment/review
status only after this review's disposition; do not mark the designs approved
while the blockers above remain.

## Mutation and execution ledger

| ID | Subject / mutation | Result | Evidence |
|---|---|---|---|
| E0 | Restored baseline: `make test` | **PASS** | fmt, vet, unit, docs, hermetic conformance, envtest, chart lint/tests all passed. This target does not run cluster conformance. |
| E1 | Unmodified `make conformance-cluster` at `588db34` | **REPRODUCED** | Failed twice in the script at `cluster_test.go:222`: no `AgentGatewayNackError` Event within 30s. The cluster was torn down by the script. |
| M1 | Pinned CRD: replace both `Hours` enum values with `Weeks`; repack and update `pinnedDigest` | **SURVIVED** | `go test ./test/conformance/... -count=1` passed. Mutation compiled and the artifact digest check passed. |
| M2 | Pinned CRD: weaken both `[requests,tokens]` validations from `size() == 1` to `size() >= 1`; repack and update `pinnedDigest` | **SURVIVED** | `go test ./test/conformance/... -count=1` passed. Mutation compiled and loaded. |
| M3 | Docs gate: weaken the production `acceptance poll` regex to `acceptance neverpoll` with independent fixtures untouched | **KILLED** | Compiled test failed in `TestNormalisationDefeatsFormattingEvasion`. This confirms the repaired independent fixture catches this production-rule weakening. |
| M4 | Add an authoritative design assertion with link-split text: `acceptance [poll](...)` | **SURVIVED** | `go test ./test/docs/... -count=1` passed after the phrase was reduced to only the link-split banned term. |
| E2 | Restored baseline after every mutation | **PASS** | Both hermetic suites passed, `make test` passed, original archive SHA-256 `16a6f36c…3a8da3a` was restored, and `git status --short` was empty before writing this review. |

No mutation above was classified from a compile failure. There were no INVALID
mutations. M1 and M2 changed the vendored dependency and its pinned digest so
the digest guard could not be mistaken for the subject test. All source and
archive restoration used backup copies, never `git checkout`.

## Verdict

**REVISE.** A19's strategic decision is defensible; its current data model is
not. The minimum implementation threshold is: revision-scoped policy material,
transaction-correct stale restart, a real path for every non-spec producer,
race-free behavior on guard loss, effective (not present) signature
verification, and a reproducible dataplane NACK/retention contract. Repair the
two survived conformance mutations and fold the amendments into authoritative
text before another review. Do not start design-03 implementation on this
snapshot.
