# Design 03 amendment review — Codex round 7

**Verdict: REVISE — 9 BLOCKER, 5 MAJOR. Do not start design-03 implementation.**

Review snapshot: `2c1168b`, covering `303b6f1`, `ef2fa6e`, `c52940c`, and
`2c1168b`. I reviewed design 03 A28–A35, design 02 A30–A35, design 20 A3,
`internal/revision`, `test/docs`, `test/conformance`, and
`hack/conformance-cluster.sh` in a detached clean worktree pinned to that
snapshot. Commit `9219d71` landed in the shared worktree while this review was
running; it is outside this snapshot and this verdict.

The repaired tests are real improvements. The docs-rule deletion, the missing
`observedGeneration` mutation, the `requiresApproval` deletion, and an unrelated
admission rejection all make their claimed gates fail. The clean hermetic gate
and the real agentgateway v1.4.1 cluster suite pass, including the 429/503 NACK
discriminator. I withdraw r6 BLOCKER 6 and r6 MAJORs 5–7.

The revision boundary is still unsafe. A chosen collision against its 40-bit
identity took 1.53 seconds on this machine. Separate direct tests reproduced
that `llm.egressAllowlist` and Kubernetes env-source leaves still hash
identically when changed. A35 also calls a same-namespace, name-resolved object
an invariant even though the exact delete-and-recreate rule that defeated A20
applies to the copy too.

## Round-6 closure audit

| r6 finding | r7 judgement |
|---|---|
| BLOCKER 1, unplanned env-source mutation | **NOT CLOSED.** A35 chooses pre-created copies, but the copy is still a same-kind, same-namespace object resolved by name and is delete/recreate-able by the principals the design does not isolate from it. See BLOCKER 5. |
| BLOCKER 2, `requiresApproval` has no transaction | **CLOSED.** Both directions enter the projection, and deleting the projection is killed independently in both directions. |
| BLOCKER 3, policy-input revision is only a noun | **REPLACED, NOT CLOSED.** A29 deletes the imaginary lifecycle, correctly, but the generic withhold/live-loosen rules are unsafe for producers already in the input contract. See BLOCKERs 7 and 8. |
| BLOCKER 4, whole-`ResourceSet` persistence | **PARTIAL.** Persisting input plus a closed live overlay is the right shape. The legal revision set does not fit its three-record bound, and the record is versionless. See BLOCKER 6 and MAJOR 2. |
| BLOCKER 5, auth comparator compares an enum | **CLOSED in design.** The effective principal policy is compared field by field. |
| BLOCKER 6, inconclusive cluster evidence exits zero | **CLOSED and re-run.** Clean cluster run passed; the forced wrong-error path failed. |
| MAJOR 1, binding requiredness absent | **CLOSED.** Every declared binding is required; `ProducerAbsent` now reaches the Agent condition aggregation. |
| MAJORs 2–3, seal lease/key machinery | **SUPERSEDED by A35.** I did not press the deliberately owed retirement sweep. |
| MAJOR 4, two fallback mechanisms | **TEXTUALLY CLOSED.** There is now one mechanism, but its live route mutation has no applied-state proof; see BLOCKER 9. |
| MAJORs 5–7, repaired gates still vacuous | **CLOSED for the mutations claimed.** See the ledger. |
| MAJOR 8 and MINORs 1–3 | **CLOSED.** |

I disagree with the new status text saying A35 “closes” r6 BLOCKER 1. A human
choosing an option settles the doctrine trade-off; it does not confer a
Kubernetes property the option lacks. I also disagree that A29 closes the
policy-input problem: it removes a nonexistent lifecycle, then treats one MCP
withdrawal example as proof for every non-spec producer and leaves non-spec
loosening unclassified. Earlier same-family amendment critiques cannot validate
A28–A35; those mechanisms were not in their reviewed snapshots, and that review
line had already converged before missing the nonexistent dependency release.

## BLOCKER findings

### BLOCKER 1 — a deliberately chosen 40-bit revision collision bypasses the eval gate

**Files:** `internal/revision/revision.go:50-54`,
`internal/revision/revision.go:171-186`.

The hash truncates SHA-256 to ten hex characters and justifies 40 bits with the
number of revisions that happen to coexist. That is the wrong threat model. The
projection is attacker-controlled, and 40-bit chosen collisions need about
`2^20` trials, not dozens of stored revisions.

I reproduced a collision between two API-valid-looking specs in 1.53 seconds:

```text
safe image digest + PAD=safe-20514
malicious image digest + PAD=evil-448104
revision hash: 74c23218d0
```

**Concrete failure:** the safe member passes evaluation as revision H. The
principal then writes the malicious colliding spec. The existing controller
computes the same desired H (`internal/controller/agent_controller.go:142`),
renders the workload from current spec (`:162`, `:306-340`), and skips candidate
gating because `activeRevision == desired` (`:220-252`). The Deployment named H
is rewritten to the malicious image while status remains on the already-passed
revision H.

**Specific fix:** use the full digest as the security identity, or at least 128
bits in every persisted comparison. A shortened suffix may remain display-only.
Persist and compare the full digest plus projection before ever adopting or
updating an object with the same short name; a mismatch is a terminal
`RevisionHashCollision`, never “same revision.” Pin the fixed collision pair as
a regression test.

### BLOCKER 2 — `llm.egressAllowlist` is still absent from the shipped projection

**Files:** `internal/revision/revision.go:117-120`,
`internal/revision/revision.go:242-248`,
`docs/designs/02-agent-crd-operator.md:159`.

The design classifies the allowlist as symmetric behaviour. The implementation
projects only providers and fallback. A direct differential test changing only
`EgressAllowlist` failed: both specs hash identically.

**Concrete failure:** an operator widens a HIPAA Agent from `internal/*` to also
permit an external provider. No revision is minted, so the egress reachability
change reaches the compiler without an eval result for it.

**Specific fix:** add the typed allow entries to `llm`, canonicalize their set
semantics without flattening endpoint identities, and add add/remove
differential tests whose surrounding LLM block is identical. Do not close this
with the aggregate `LLM` mutation; that is the vacuity that hid it.

### BLOCKER 3 — named Kubernetes env arms still drop behaviour-bearing leaves

**Files:** `internal/revision/revision.go:87-101`,
`internal/revision/revision.go:273-307`,
`docs/designs/02-agent-crd-operator.md:166-178`.

The raw fallback over-gates unknown union arms, but every explicitly named arm
drops fields. `SecretKeyRef.Optional`, `ConfigMapKeyRef.Optional`,
`FieldRef.APIVersion`, `ResourceFieldRef.Divisor`, and both `envFrom` optional
bits are absent. The design body itself lists these as the defects A16 is meant
to close. A direct test changing only
`runtime.envFrom[].configMapRef.optional` reproduced an identical hash.

**Concrete failure:** R1 references an existing prompt ConfigMap with
`optional:false`. The principal changes only the field to `optional:true`; the
hash stays R1 and the current controller rewrites R1's Pod template. After the
source disappears, a replacement Pod starts without the prompt rather than
failing, serving different behaviour under R1's prior gate result. Changing a
resource divisor similarly changes a process input without minting a revision.

**Specific fix:** project every transitive leaf of every named arm injectively,
including pointer presence, or canonical-marshal the complete upstream selector
types. Generate one differential test per transitive leaf; aggregate and
per-union-arm tests do not prove leaf coverage.

### BLOCKER 4 — resolved env content and A35 copies do not exist in the running revision path

**Files:** `internal/revision/revision.go:18-23`,
`internal/revision/revision.go:80-82`,
`internal/revision/revision.go:171-186`.

The file honestly labels content hashing unimplemented, then `Hash(spec)` has no
client, resolved material, or copied bytes from which it could implement A20 or
A35. The nested comment still states the withdrawn rule that Secret rotation
“must not re-gate.” This is not harmless future scaffolding: `internal/revision`
is the identity used by the existing operator.

**Concrete failure:** an editor changes a referenced ConfigMap from a safe
system prompt to an injected one and replaces a Pod. The Agent spec and referent
name do not move, so the hash remains R1 and the replacement workload consumes
the changed object under R1's gate result.

**Specific fix:** replace `Hash(spec)` with a mint operation that resolves every
source once, produces the immutable revision material, rewrites the PodSpec, and
hashes the exact bytes it will publish. Callers must not be able to ask for a
production revision identity from spec alone. Add the edit + Pod replacement
cluster test; envtest has no kubelet and cannot make this claim.

### BLOCKER 5 — A35's copy is not immutable by reference

**Files:** `docs/designs/02-agent-crd-operator.md:142-153`.

A35 repeats the exact distinction that refuted A20—`immutable:true` prevents an
update but permits delete-and-recreate—and then treats a new name as an
invariant. The name is neither secret nor capability-protected: it is present in
the revision PodSpec, and the copy remains the same Secret/ConfigMap kind in the
same namespace. A normal namespace Role granting create/delete on that kind
grants it for originals and plume copies alike. `ownerReference` does not
protect deletion. “The operator does not recreate it” says nothing about the
editor.

The missing-copy case is also unspecified. If the copied reference preserves
`optional:true`, deleting the copy lets a replacement Pod start without the
gated material. If it is non-optional, the revision loses availability; neither
outcome appears in the state/failure tables.

**Concrete failure:** a namespace editor lists the Deployment, deletes immutable
`agent-H-env-0`, and creates a malicious ConfigMap with the same name. A node
drain causes R1's Pod to resolve the malicious object by name and serve it under
R1's old hash. No Agent write is required. With an optional reference, deletion
alone changes the process environment silently.

**Specific fix:** put revision workloads and their material in an
operator-controlled namespace/security domain where source editors have no
create/update/delete rights, or use a material-delivery primitive that pins an
immutable version/UID rather than a Kubernetes object name. Kubernetes Pod env
references have no UID/resourceVersion field, so renaming alone cannot supply
that property. Always rewrite successfully resolved copies as non-optional and
define missing/UID-mismatch as route weight zero plus a condition. If an
admission guard is chosen instead, stop calling the result an invariant: guard
loss reopens the same bypass A35 was selected to remove.

### BLOCKER 6 — the legal revision set cannot fit the three-record/material bound

**Files:** `docs/designs/03-policy-compiler.md:61-69`,
`docs/designs/02-agent-crd-operator.md:149-155`,
`docs/designs/02-agent-crd-operator.md:213-217`.

A28 caps `status.revisions[]` at three records: active, candidate, rollback
target. A35 repeats three live revisions as its credential-copy bound. The
authoritative rollout rule says `revisionHistoryLimit: 2` means **two retained
revisions in addition to active and candidate**. That is four live revisions
before design 20's independent retention hold is considered.

**Concrete failure:** R1 and R2 are retained, R3 is active, and R4 is a
candidate. All four are legal and required for the documented rollback
contract. The compiler must now reject R4 for exceeding the record bound, delete
one promised rollback target, or retain material for a revision whose input
record it cannot store. Every choice violates an authoritative rule.

**Specific fix:** define the retained set once as a union of active, candidate,
the configurable history window, and explicit holds. Size both records and
copies from that set—at default, at least four—not from named roles. Specify
whether a design-20 hold occupies a history slot or extends the set, and test
the maximum-overlap transition.

### BLOCKER 7 — a non-spec loosening is an ungated capability grant

**Files:** `docs/designs/03-policy-compiler.md:286-313`.

The comparator calls a larger tool set or endpoint set `Loosen` and applies a
Loosen in place. The provenance table covers non-spec **tightening** only. It has
no row for a non-spec widening, which is the dangerous direction for eval
gating.

**Concrete failure:** an MCP server bound to R1 begins advertising a new
`wire_funds` tool. `tools[].toolAllowlist` is produced from that advertised set,
not Agent spec. The new superset classifies as `Loosen`, so the live tool filter
publishes it without an Agent revision or eval. R1 can now call a capability it
was never evaluated with. The same shape applies to an externally resolved
endpoint set growing.

**Specific fix:** select transaction by provenance before comparator result. A
non-spec widening must be frozen to the revision's recorded input and either
mint a real composite revision through the full lifecycle or be refused until
an explicit Agent behaviour edit. Add a total table for non-spec `Equal`,
`Loosen`, `Tighten`, and `Unknown`; no cell may inherit the live path implicitly.

### BLOCKER 8 — withholding non-spec tightening is fail-open for producers already in the contract

**Files:** `docs/designs/03-policy-compiler.md:73-88`,
`docs/designs/03-policy-compiler.md:305-321`.

A29 argues withholding is safe from one example: an MCP server withdrawing a
tool already enforces the loss at the source. The same generic row covers tenant
consumer budgets and identity/IdP inputs, neither of which is necessarily
enforced by its producer. Those are not hypothetical future producers; they are
already named in `PolicyIntent` provenance.

**Concrete failure:** design 26 lowers a tenant's consumer budget from 1,000 to
1 request/hour. The policy compiler is the enforcement point. A29 withholds the
tighter policy, so the active route keeps permitting 1,000 indefinitely while
`PolicyInputDrifted` merely reports the overspend. An IdP audience revocation can
likewise leave the old broader admitted-principal policy serving.

**Specific fix:** replace the generic row with a producer-by-producer safety
contract. Withholding is permitted only when the source independently enforces
the same restriction and a conformance test proves it. Every other tightening
must fail the affected route closed or use a real composite revision lifecycle;
a condition is observability, not enforcement.

### BLOCKER 9 — fallback activation moved the unverified live mutation from a Backend to the route

**Files:** `docs/designs/03-policy-compiler.md:327-337`,
`docs/designs/20-drift-controllers.md:18-22`,
`docs/designs/20-drift-controllers.md:34`.

A34 correctly removes live Backend recompilation, but activation still mutates
the serving `HTTPRoute` weights. Its precondition checks that the fallback and
route converged **before** the weight edit. It specifies no current-generation
postcondition and no data-plane evidence that the new weights took effect.
Design 03 already says route status proves control-plane convergence, not
enforcement, and that this agentgateway version exposes no `Programmed`
condition.

**Concrete failure:** `ModelDrifted` sets `status.llmFallbackActive`; both
Backends passed the precheck. The 100/0 → 0/100 route patch is not programmed or
the dataplane retains the old route. Plume records the fallback activation while
the drifted primary keeps answering—the same loud-and-wrong incident outcome
A34 says it removed.

**Specific fix:** make activation a staged reconcile with separate desired and
applied state. After the route generation changes, require current-generation
route convergence and run the model canary through that production route;
attribute success by the full endpoint identity in the receipt. Unlike the
withdrawn negative witness, a receipt naming the fallback positively identifies
the producer. Set an applied/remediated condition only after that proof; on
timeout keep `ModelDrifted`, raise `LLMFallbackUnavailable`, and page.

## MAJOR findings

### MAJOR 1 — A35 does not specify an atomic source-read/copy/hash acquisition

**Files:** `docs/designs/02-agent-crd-operator.md:142-150`.

The copy name needs `revisionHash`; the projection hashes the copy's content;
and the copy is said to equal the original at mint. This is implementable only
if one GET produces one immutable byte buffer used for both the hash and the
Create. The design does not state that. A natural two-read implementation hashes
the original, reads it again to create the copy, and races an editor between the
reads.

**Concrete failure:** the first GET reads safe bytes and computes H. An editor
writes malicious bytes before the second GET. The operator creates
`agent-H-env-0` with malicious bytes and publishes a workload whose name and
gate identity say H even though H was computed over safe content.

**Specific fix:** specify the acquisition order: GET once with UID and
resourceVersion; canonicalize and buffer exact bytes; compute the full revision
digest; Create immutable copies from those exact buffers; read back and verify
kind, owner UID, immutable bit, and bytes; only then publish. `AlreadyExists`
must verify the complete invariant or fail `RevisionMaterialCollision`, never
adopt by name.

### MAJOR 2 — a versionless `RevisionRecord` cannot support the migrations it is meant to survive

**Files:** `docs/designs/03-policy-compiler.md:48-68`.

The record explicitly has no version and claims a projection-shape change simply
changes the hash. That handles new candidates, not retained active and rollback
records. A new operator must still decode and recompile the old projection. A
permissive decoder can reinterpret missing/unknown fields under new defaults; a
strict decoder takes every old active revision down the corruption/manual-action
path on upgrade. `recordDigest` detects bytes changing, not which schema those
unchanged bytes use.

**Concrete failure:** a later release changes endpoint identity or adds a
behaviour leaf. After operator upgrade, R1 remains active and its old record is
needed to rebuild a deleted gateway resource. The new decoder either accepts the
old JSON with zero-value semantics and emits a different policy for R1, or
refuses it and cannot self-heal. Neither is a schema migration.

**Specific fix:** add `schemaVersion` covered by `recordDigest`, retain an
immutable decoder/compiler for every version that can remain in the retention
window, and define upgrade compatibility and retirement. Unsupported versions
must fail explicitly without being labelled corruption.

### MAJOR 3 — the revised projection tests still prove aggregates, not leaves

**Files:** `internal/revision/revision_test.go:61-205`,
`internal/revision/classification_test.go:91-162`,
`internal/revision/golden_test.go:12-40`.

The budget test changes only `tokensPerDay`; the expose test changes only
`visibility`; the aggregate classification mutation does the same. The golden
fixture claims to exercise every projected field but omits `usdPerDay`,
`taskTimeout`, `maxHops`, `expose.a2a.auth`, and `egressAllowlist`.

Two valid, compiling production mutations survived the entire
`internal/revision` suite: deleting the `USDPerDay` assignment and deleting the
`Auth` assignment. The already-absent allowlist and env optional bits likewise
had no production test that failed until direct review tests were added.

**Concrete failure:** a refactor drops `usdPerDay` from `project()`. Every test
stays green and changing the cost ceiling no longer mints a revision, recreating
the budget widening A25 was introduced to gate.

**Specific fix:** derive a transitive leaf inventory independently of the
projection, and perturb each leaf with identical enclosing objects on both
sides. Require every valid leaf mutation to change or preserve the hash according
to its explicit classification. Make the golden fixture populate every leaf,
but do not treat the golden alone as semantic coverage.

### MAJOR 4 — the authoritative rollback failure row still implements pre-A35 semantics

**Files:** `docs/designs/02-agent-crd-operator.md:264-271`.

The failure table says rollback is refused when the original env content has
changed. A35's point is that rollback uses the retained revision copy and can
run the original bytes even when the user's object changed or disappeared.
Both statements are in the authoritative body.

**Concrete failure:** a credential or prompt changes after R1, R2 becomes
degraded, and the operator follows the failure table. It refuses an otherwise
reproducible R1 rollback even though R1's copy exists, extending an incident and
contradicting A35's accepted rollback cost.

**Specific fix:** replace the row with copy semantics: rollback requires the
retained copy set to exist, match its full digest/owner UID, and remain
unmodified; original-source drift is irrelevant. Missing or mismatched copy
refuses rollback and names the revision material. Add an independent docs rule
fixture so the referent-drift refusal cannot return unnoticed.

### MAJOR 5 — design 20 still keys baseline creation on the flat identity A2 removed

**Files:** `docs/designs/20-drift-controllers.md:18-22`,
`docs/designs/20-drift-controllers.md:36-38`,
`docs/designs/20-drift-controllers.md:74-76`.

The detector row and A2 require the full endpoint identity, but the live
baseline rule still snapshots “at first use of a `(provider, model)`.” That is
the exact non-injective key A2 says is forbidden.

**Concrete failure:** two Azure OpenAI deployments share provider/model but have
different endpoint, deployment name, region, and BAA posture. The healthy one
creates or clears the shared baseline while the other is drifting, so fallback
activation targets the wrong endpoint or clears `ModelDrifted` incorrectly.

**Specific fix:** change the baseline rule and every storage key to the canonical
full endpoint identity used by receipts and A24. Pin two same-arm/same-model
instances whose baselines and drift states remain independent.

## Mutation and execution ledger

All mutations were made in the detached snapshot after copying the target to a
backup and were restored from that backup. No `git checkout` was used. Every
code mutation below compiled unless explicitly described as a characterization
reproduction; none is being credited merely for a compiler failure.

| ID | Change / command | Result | Meaning |
|---|---|---|---|
| B0 | `make test` on clean `2c1168b` | **PASS** | Full hermetic gate passes before mutation. |
| C0 | `make conformance-cluster` against agentgateway v1.4.1 | **PASS** | All cluster tests passed; NACK'd loosen remained 429 and clean loosen returned 503. |
| M1 | Search two `2^21` valid-image-digest spec families that differ in safe/malicious image and inert env padding | **REPRODUCED** | Chosen 40-bit collision `74c23218d0` found in 1.53s. This is an executed attack, not a compile mutation. |
| M2 | Add a differential test changing only `llm.egressAllowlist` | **REPRODUCED** | Test failed because both hashes were equal. |
| M3 | Add a differential test changing only `envFrom.configMapRef.optional` | **REPRODUCED** | Test failed because both hashes were equal. |
| M4 | Delete the `USDPerDay` assignment from `project()` | **SURVIVED** | `go test ./internal/revision -count=1` passed. Mutation compiled and removed the exact rule claimed. |
| M5 | Delete the `Expose.A2A.Auth` assignment from `project()` | **SURVIVED** | `go test ./internal/revision -count=1` passed. Mutation compiled and removed the exact rule claimed. |
| M6 | Delete the `RequiresApproval` assignment from `project()` | **KILLED** | Both on/off differential cases and the golden digest failed. |
| M7 | Delete the production `seal-not-copy` docs rule while retaining its independent fixture | **KILLED** | `TestEveryRuleIsPinnedByAnIndependentFixture` failed because the fixture had no rule. |
| M8 | Treat missing `observedGeneration` as current in `statusIsCurrent` | **KILLED** | Both missing-generation cases failed in the hermetic conformance suite. |
| M9 | Replace the both-limits cluster fixture with an unrelated YAML-parse rejection | **KILLED** | The mutated Go suite compiled and the real cluster run failed specifically because the error was not the `ExactlyOneOf(requests,tokens)` error. This validates commit `2c1168b`'s “which error” guard. |

The shared tree was clean when this file was written. A35's explicitly owed seal
retirement sweep was not reviewed as an omission and is not a finding here.
