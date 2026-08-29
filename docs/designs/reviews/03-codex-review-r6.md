# Design 03 amendment review — Codex round 6

**Verdict: REVISE — 6 BLOCKER, 8 MAJOR, 3 MINOR. The design is not implementation-ready.**

Review snapshot: `e3edf24`, covering the substantive commits `c4ea345`,
`2701d9a`, and `e3edf24` after round 5. I read `AGENTS.md`,
`docs/agent-protocol.md`, the canonical architecture, designs 02, 03, 07, 08,
10, and 20, ADR-0028, the replacement research note, both spike sections, the
prior amendment review, and Codex r5. I ran the full hermetic gate twice and the
real agentgateway v1.4.1 cluster suite twice, including a forced inconclusive
control.

The 429/503 positive control is sound for the claim it makes. The backend is
unchanged; the strict baseline first proves that the limiter can override that
backend's 503; only the policy is then changed. A clean update changing 429 to
503 therefore proves that the old limiter stopped rejecting. It does not need
503 to identify the new limiter's exact configuration. I do **not** retain r5's
objection to the traffic evidence.

That does not rescue the set. A28 acknowledges that reaction cannot close the
env-source race and then names a snapshot “fallback” that is neither created nor
referenced. A26 names two more stores/revision systems without specifying their
state machines. A27 says requiredness is in every binding but leaves it out of
the type. Three repaired tests still go green after valid mutations.

## Round-5 closure audit

| r5 finding | r6 judgement |
|---|---|
| BLOCKER 1, active/candidate policy shared | **PARTIAL.** One intent per revision is the right correction, but persisting an entire `ResourceSet` also freezes fields the design still says apply live; see BLOCKER 4. |
| BLOCKER 2, stale `Create` skips its first stage | **CLOSED.** The transaction is recomputed against the serving set and re-enters its kind-specific first stage. |
| BLOCKER 3, no path for non-spec tightening | **NOT CLOSED.** “Policy-input revision” is a name, not a revision lifecycle; see BLOCKER 3. |
| BLOCKER 4, guard loss wins before detection | **NOT CLOSED.** A28 accurately restates the race, then relies on snapshots A23 explicitly declined to create; see BLOCKER 1. |
| BLOCKER 5, controller presence is not signature enforcement | **CLOSED in design.** Design 07 A3 checks the effective namespace/policy/mode contract and requires an actual unsigned-image rejection. |
| BLOCKER 6, NACK retention unmeasured | **BEHAVIOUR REPRODUCED.** The real cluster discriminates 429 from 503. The command still passes when that measurement is skipped; see BLOCKER 6. |
| BLOCKER 7, lease name needs a not-yet-known hash | **CLOSED.** Recording a pre-hash lease UID before touching the source removes the circular dependency. Collision and sweep ownership remain under-specified; see MAJOR 3. |

I disagree with the statement that all 21 r5 findings are addressed. Two r5
blockers were renamed rather than mechanised. I also disagree with treating
`docs/designs/reviews/03-amendments-review.md` round 4's “0 BLOCKER” as evidence
for A26–A29: those mechanisms did not exist in that review snapshot. The same
review family had already converged before missing the nonexistent-release
error; agreement there cannot validate new state machines here.

## BLOCKER findings

### BLOCKER 1 — the unplanned guard-loss bypass is acknowledged and left open

**Files:** `docs/designs/02-agent-crd-operator.md:132`,
`docs/designs/02-agent-crd-operator.md:472-500`.

A23 deliberately chose seal-in-place and says **no bytes are copied**. A28 then
correctly proves that Binding removal, an authorised source edit, and Pod
replacement can serve changed material before any watch reaction. Its proposed
closure is “immutable revision snapshots,” but no snapshot exists before the
loss, no PodSpec references one, and no trusted old bytes can be reconstructed
after the editor wins. A reactive fallback can only copy the attacker's new
content or fail because the original is gone.

The condition table is consequently false outside a planned overlap. It says
`Ready` and the route are unaffected while the guard is absent and sources
match, but establishing “match” itself races an edit and a kubelet restart. A
missing HMAC verification key makes even that predicate unknowable.

**Concrete failure:** R1 passed evaluation with ConfigMap content `safe`. The
admission Binding is accidentally deleted. A principal with ordinary ConfigMap
update rights writes `unsafe`, deletes an R1 Pod, and the kubelet starts it with
`unsafe`. The operator later observes divergence and shifts weight to zero, but
R1 has already served under R1's old gated hash. There is no pre-existing
snapshot to fall back to.

**Specific fix:** choose and specify a pre-loss invariant. Either create and
retain immutable revision-scoped material **before publication** and make the
revision PodSpec reference it, with the security/GC/rollback contract already
analysed in `03-codex-review-b3.md`; or install a separately protected Pod
admission control that rejects replacement Pods unless every referenced source
is guarded and matches R1's recorded material. Delete “snapshot fallback” until
one of those mechanisms exists. Add the Binding-removal + edit + node-drain
cluster test as a release gate, not a future caveat.

### BLOCKER 2 — `requiresApproval` has no safe transaction

**Files:** `docs/designs/02-agent-crd-operator.md:114-123`,
`docs/designs/03-policy-compiler.md:251-277`,
`docs/designs/03-policy-compiler.md:292-295`.

`tools[].requiresApproval` remains policy-surface and is an input to
`PolicyIntent`, but the closed comparator registry has no approval concern.
A24 says an absent comparator is `Unknown → Tighten`; A25 says tightening must
mint a revision; the producer table offers a revision only for Agent-spec
**behaviour** fields. Therefore `false → true` reaches no transaction. Treating
it as `Loosen` or applying it live reintroduces the measured NACK-retains-old
configuration failure on an approval boundary.

**Concrete failure:** an operator changes a dangerous tool from direct execution
to `requiresApproval: true` after an incident. The field does not mint an Agent
revision. The compiler cannot prove a loosening and therefore cannot use
`Loosen`; it has no candidate to create. An implementation either wedges while
the direct route keeps serving, or mutates the serving interceptor and can NACK
while status claims the policy change was requested.

**Specific fix:** put `requiresApproval` on the behaviour surface and include it
injectively in the revision projection, or define a complete approval-policy
comparator and a real policy-input revision lifecycle. The safe, small fix is
the former: both directions gate, exactly as `budget` and `expose` now do.

### BLOCKER 3 — “policy-input revision” is not connected to any revision state machine

**Files:** `docs/designs/03-policy-compiler.md:266-275`,
`docs/designs/02-agent-crd-operator.md:66-74`,
`docs/designs/02-agent-crd-operator.md:183-194`.

The new row gives non-spec tightening a content-addressed “policy-input
revision” and says it is gated like any other. The only defined revision
lifecycle is the Agent behaviour revision keyed by `status.activeRevision` and
`status.candidateRevision`, with one workload, card, evaluation result,
promotion, rollback target, and retention entry per hash. The amendment defines
none of those mappings for a policy-only candidate.

**Concrete failure:** an MCP server withdraws one advertised tool while R1's
workload and Agent spec do not change. The operator mints policy candidate P2.
There is no status field that can hold `(R1,P2)`, no candidate workload/card to
evaluate, no rule saying which EvalRun result promotes P2, and no rollback/GC
identity that distinguishes R1+P1 from R1+P2. Reusing `candidateRevision`
collides with a simultaneous Agent-spec candidate; not using it makes the gate
invisible to design 16.

**Specific fix:** define one composite revision identity, for example
`{behaviourHash, policyInputHash}`, and carry it through status, candidate route,
EvalRun binding, promotion, rollback, supersession, receipt attribution, and GC.
Alternatively forbid non-spec tightening from changing a serving Agent until
its producer makes an explicit Agent behaviour revision. A noun and a hash are
not a lifecycle.

### BLOCKER 4 — persisting the whole `ResourceSet` freezes fields that are still live policy

**Files:** `docs/designs/03-policy-compiler.md:23-63`,
`docs/designs/03-policy-compiler.md:43-46`,
`docs/designs/02-agent-crd-operator.md:114-123`,
`docs/designs/02-agent-crd-operator.md:239`.

One `PolicyIntent` per behaviour revision fixes active/candidate separation. The
next rule is too broad: an active revision's entire applied `ResourceSet` is
persisted and is never reconstructed from current Agent spec. The set contains
policy-surface inputs such as `requiresApproval`; it also contains non-spec
resolution state and controller inputs. Those are supposed to change without a
new behaviour revision. Freezing the whole set ignores legitimate policy
changes; rebuilding it violates the new safety rule.

The persistence contract also has no home. Design 02 still says there is no
state outside CR status plus directory KV, but its status shape has no
ResourceSet or versioned applied intent. The live gateway objects are not a
lossless record of the input: derived rates are floored, and the receipt
backstop needs the active revision's original budget, not a reverse-engineered
gateway rate.

**Concrete failure:** R1 is active at a $10 budget. A held R2 raises it to $100
and a separate policy-surface approval edit is made. After an operator restart,
there is no specified durable record from which to restore R1's $10 backstop
and live approval policy without reading current spec. Reading current spec can
apply R2's ungated values to R1; refusing to read it drops the policy edit.

**Specific fix:** persist a versioned, integrity-checked **behaviour projection
and original budget** per revision, separately from a versioned live-policy
overlay. Specify the Kubernetes object/status schema, ownership, size bound,
schema migration, corruption response, rollback lookup, and GC. Recompile an
active revision only from its immutable projection plus the explicitly-owned
live overlay—never from undifferentiated current spec.

### BLOCKER 5 — the auth comparator ignores the identity and policy that define the admitted principals

**Files:** `docs/designs/03-policy-compiler.md:23-31`,
`docs/designs/03-policy-compiler.md:48-63`,
`docs/designs/03-policy-compiler.md:251-264`.

The `-auth` comparator orders only `none < oauth`. The emitted auth concern also
depends on `identity` and on effective issuer/audience/claim configuration from
design 06. Two OAuth policies can admit disjoint or wider principal sets while
both normalize to `oauth`. Reflexivity on the enum is not safety for the policy.

**Concrete failure:** a non-spec identity/IdP input changes the OAuth issuer or
audience from tenant A to a broader tenant set while `expose.auth` stays
`oauth`. The comparator sees `oauth == oauth`, classifies the update as
no-stricter, and permits an in-place `Loosen`. The serving route can now admit
principals that never passed the revision gate; if the new policy NACKs, the old
principal set remains while status records an attempted change.

**Specific fix:** normalize `-auth` to the complete effective policy—mode,
issuer, audiences, claim predicates, principal identity, and key/source
identity. Only exact equality or a proved superset of admitted principals may
be `Loosen`; every other change is `Unknown → Tighten` and must have the real
revision path from BLOCKER 3. Property-test changes to each field independently.

### BLOCKER 6 — the cluster gate exits zero when its load-bearing test is inconclusive

**Files:** `hack/conformance-cluster.sh:6-8`,
`hack/conformance-cluster.sh:44-53`,
`test/conformance/cluster_test.go:299-307`,
`test/conformance/cluster_test.go:398-418`.

The harness promises that skipped conformance fails loudly. The NACK test uses
`t.Skipf` for the one state where its discriminator provides no evidence. Go
treats a skipped test as success, and the shell preserves only the Go process's
zero exit code.

**Concrete failure:** an agentgateway release changes bucket reset behaviour so
the clean control remains 429. The only evidence supporting A19 becomes
inconclusive. `make conformance-cluster` prints `--- SKIP`, then `PASS`, exits 0,
and CI approves the dependency. This was reproduced, not inferred.

**Specific fix:** make an inconclusive control `t.Fatalf`, or emit a stable
machine-readable marker and make the harness reject any skipped cluster test.
Mutation-pin it: forcing `control = 429` must make `make conformance-cluster`
non-zero.

## MAJOR findings

### MAJOR 1 — requiredness is asserted in prose and absent from both binding contracts

**Files:** `docs/designs/03-policy-compiler.md:27-32`,
`docs/designs/03-policy-compiler.md:154-165`,
`docs/designs/02-agent-crd-operator.md:119-121`.

`Binding<T>` contains `{requested,resolved,state}`—not `required`. Design 02's
tool binding has no required marker either. Yet the aggregation table relies on
“is marked required” for both KG and Tool. The compiler cannot distinguish the
two cells A27 says it reports differently.

**Concrete failure:** an Agent declares two tools, one essential and one
optional. The essential binding becomes unresolvable. Both arrive with the same
three-field shape, so the compiler can report `Ready=True/Degraded` when the
contract requires `Ready=False`, or fail both and turn an optional outage into
an availability incident.

**Specific fix:** add `required: bool` to the internal binding and define its
producer. If users can mark it, amend the Agent CRD. If requiredness is inferred,
delete “marked required” and give a total deterministic rule for every kind.
Pin each Ready/phase/CLI cell with a transition test.

### MAJOR 2 — HMAC retirement has no barrier against a stale writer

**Files:** `docs/designs/02-agent-crd-operator.md:466-470`.

“No status remains under the old key ID” is a scan, not a write barrier. A
reconciler using a stale cached active-key view can write an old-key digest
after the rotation controller's final scan and before or after retirement. The
next verification then raises `VerificationKeyMissing` on content that never
changed.

**Concrete failure:** rotation publishes K2; worker W started earlier with K1.
The rotator rewrites the fleet, observes no K1 digests, and removes K1. W then
commits a retained revision status signed with K1. New revisions for that Agent
are permanently withheld until an operator restores the supposedly retired
key.

**Specific fix:** model key state as `active/verify-only/retired` with an epoch.
Every digest status write must compare-and-swap against the current active epoch
and retry with K2 on conflict. Retire K1 only after switching writers, draining
or fencing old epochs, and a final consistent rescan. Test a paused stale writer
across retirement.

### MAJOR 3 — forced lease collision ownership is not durable, and the old sweep rule remains authoritative

**Files:** `docs/designs/02-agent-crd-operator.md:451-462`.

The text says a pre-existing identical `leaseUID` “this Agent did not put” is
refused, but acquisition records only the UID before touching the source. After
a crash between add and association, the recovering Agent cannot tell from that
record alone whether it added the finalizer or collided with another holder.
Scanning all Agent statuses can reveal duplicates, but no duplicate-resolution
or release rule is stated. Separately, line 462 still instructs startup to drop
entries whose Agent UID **or revision** no longer exists—the exact rule A29
withdraws seven lines earlier.

**Concrete failure:** force two Agents' RNGs to return the same UID. Both record
it; one wins the source CAS and crashes. Recovery sees its own recorded UID and
the source finalizer, while the other Agent has the same record. If both later
associate and either releases, the set-valued finalizer disappears while the
other revision is retained. Following line 462 can also reclaim a valid
pre-revision acquisition after leader restart.

**Specific fix:** make ownership a globally unique API reservation (for example
a Lease/claim object created atomically and owned by the Agent), or encode a
holder identity that is independently verifiable and define duplicate refusal
before source mutation. Specify crash recovery for every acquisition step and a
forced-collision test. Delete the stale revision-existence sweep sentence.

### MAJOR 4 — fallback activation still has two incompatible authoritative mechanisms

**Files:** `docs/designs/03-policy-compiler.md:275`,
`docs/designs/03-policy-compiler.md:305`,
`docs/designs/20-drift-controllers.md:18-23`.

The new text requires fallback Backend pre-provisioning and activation by a
weight shift over converged material. The mapping table and design 20 still say
the controller flips status and **recompiles** the existing LLM Backend. Those
are not equivalent failure modes; one avoids a live Backend NACK and the other
is the exception A26 admits remains ungated.

**Concrete failure:** an implementer follows design 20 and recompiles the
serving Backend during drift. The dataplane NACKs. Kubernetes status remains
converged and the drifted primary keeps serving—the measured failure A19 was
written around—while `llmFallbackActive` reports remediation.

**Specific fix:** fold one mechanism everywhere. Name the two pre-provisioned
Backends, the exact weighted HTTPRoute field changed at activation, readiness
preconditions, what happens when fallback convergence is later lost, and the
rollback. Remove every “recompile existing Backend” instruction.

### MAJOR 5 — the repaired docs gate misses both a valid formatting mutation and the live stale claims

**Files:** `test/docs/superseded_test.go:199-201`,
`test/docs/superseded_test.go:219-234`,
`test/docs/superseded_test.go:285-306`,
`docs/designs/02-agent-crd-operator.md:103`,
`docs/designs/02-agent-crd-operator.md:114-128`.

`normalize` can collapse newlines, but the production scan splits the document
into lines **before** calling it. The fixture calls `normalize` on a multi-line
string directly, so it proves a path production never uses. A rendered
“acceptance\npoll” mutation survived. The gate also currently passes the live
body's `revisionHash(spec) — spec only` and `envFrom (by referent, not contents)`
claims immediately above the opposite A20 rule, because its regex knows only
“computed from spec alone” and “hashed by referent.”

**Concrete failure:** an implementer follows the authoritative table and hashes
only env referents. The same-name content bypass survives even though the docs
gate is green. A normal Markdown hard wrap is enough to hide another banned
guarantee.

**Specific fix:** render or normalize the **whole authoritative body**, then
scan rendered text with block boundaries represented explicitly. Add independent
fixtures using the two exact live stale phrasings and a fixture inserted through
the same `scanned()` production path. Do not test `normalize()` in isolation as
proof of corpus coverage.

### MAJOR 6 — the “exact CEL semantic” test is still lexical

**Files:** `test/conformance/crd_contract_test.go:217-235`.

The test checks three substrings. A valid CEL mutation replacing
`has(self.requests)` with `!has(self.requests)` retains every substring and
`size()==1`, but reverses the truth table: both-present and neither-present are
accepted while exactly-one is rejected. The vendored chart was repacked, its
pinned digest updated, the mutation compiled, and the conformance suite passed.

**Concrete failure:** an upstream or vendoring error ships that expression.
Plume's compiler emits one field as designed, but the API rejects it; manifests
with both fields are admitted. The claimed dependency contract is false while
the exact-semantic test is green.

**Specific fix:** evaluate the CEL rule against the four truth-table cases using
the Kubernetes CEL environment, or compare a parsed canonical AST to the exact
approved expression. Mutation-pin negation, operand duplication, and an extra
constant—not just `==` versus `>=`.

### MAJOR 7 — the status helper accepts a missing `observedGeneration` as current

**Files:** `test/conformance/cluster_test.go:39-45`,
`test/conformance/cluster_test.go:69-82`,
`test/conformance/testdata/agentgateway-crds-1.4.1.tgz` (Policy condition schema).

The helper's comment says it requires
`observedGeneration == metadata.generation`. The code marks a condition stale
only **if the field exists and differs**. The pinned CRD does not require
`observedGeneration`, so omission is schema-valid and is accepted as current.

**Concrete failure:** a controller revision omits `observedGeneration` after a
policy patch. `conds()` immediately consumes the previous `Accepted=True` and
`Attached=True`, recreating the stale-true test bug this helper claims to close.

**Specific fix:** require the field on every condition used by the assertion;
missing, wrong type, or unequal must remain non-current and eventually fail.
Add a unit fixture for missing generation and a cluster mutation that strips it.

### MAJOR 8 — an explicitly requested capability can disappear with no Agent condition

**Files:** `docs/designs/03-policy-compiler.md:65-78`,
`docs/designs/03-policy-compiler.md:154-184`.

`ProducerAbsent` suppresses a requested binding and emits no per-Agent
condition, delegating the tier gap to one operator-level report. That is valid
for an unused route class; it is not valid for an Agent that explicitly asked
for the capability. It also bypasses A27's required/optional aggregation because
the compiler drops the binding before classifying its impact.

**Concrete failure:** gateway governance is enabled, design 11 is not installed,
and an Agent declares its only critical tool. The tool route is omitted,
`PolicyCompileFailed` is absent, and the Agent can report `Ready=True`. A user
looking at that Agent sees no reason its advertised task cannot complete; only a
separate `plume doctor` invocation reveals a cluster tier gap.

**Specific fix:** keep the single operator-level producer condition to avoid
fleet noise, but add a per-Agent failure whenever that Agent has a requested
binding from the absent producer. Feed it through the same required/optional
aggregation as `Unresolvable`; do not erase a requested contract because its
controller is not installed.

## MINOR findings

### MINOR 1 — the finalizer length rationale names the wrong Kubernetes limit

**File:** `docs/designs/02-agent-crd-operator.md:451`.

The optional DNS prefix before `/` may be 253 characters; the qualified-name
segment after `/` is limited to 63. `src.<UUID>` still fits, so this does not
break the mechanism, but the stated limit would bless a future invalid name.

**Specific fix:** say the suffix is constrained to 63 characters and pin the
rendered finalizer with API admission.

### MINOR 2 — both design status headers describe obsolete amendment snapshots

**Files:** `docs/designs/02-agent-crd-operator.md:3`,
`docs/designs/03-policy-compiler.md:3`.

Design 02 still says A1–A19 are folded and A20–A21 are owed; design 03 says
A1–A16 and that r2's majors remain open. The bodies now contain A29 and A27
respectively. These headers are the first routing information an implementer
reads.

**Specific fix:** update both headers to this snapshot and the latest review
verdict; keep historical states only in amendment/review logs.

### MINOR 3 — singular revision input still has plural/Agent-era residue

**Files:** `docs/designs/03-policy-compiler.md:50-53`,
`docs/designs/03-policy-compiler.md:150`,
`docs/designs/03-policy-compiler.md:501`.

The provenance row still names `revisions`, and compile is repeatedly described
as “one Agent in” after A26 changed the unit to one revision. This obscures the
necessary active/candidate failure isolation.

**Specific fix:** use `revision` and “one revision intent in” consistently, and
state that a candidate compile/apply failure cannot mutate or delete the active
revision's persisted set.

## Mutation and execution ledger

All mutations were made only after copying the target to a backup. Every target
was restored with `cp`; `git checkout` was never used. “SURVIVED” below means the
mutation compiled and its claimed gate exited zero. No compile failure is counted
as evidence.

| ID | Change / execution | Result | Evidence |
|---|---|---|---|
| E0 | Restored baseline: `make test` before mutations | **REPRODUCED / PASS** | fmt, vet, unit, docs, hermetic conformance, envtest, chart lint and chart tests all passed. |
| M1a | Inserted `acceptance\npoll runs on the reconcile worker for up to 30s` into a scanned authoritative body | **KILLED** | The second line independently matched `on the reconcile worker`; docs test failed. This does not prove cross-line matching. |
| M1b | Inserted only the rendered banned phrase `acceptance\npoll verifies convergence` through the production scanned corpus | **SURVIVED** | `go test ./test/docs/... -count=1` passed. Production splits before normalizing; the fixture does not. |
| M2 | Repacked the pinned CRD chart with both local-rate ExactlyOne rules changed from `has(self.requests)` to `!has(self.requests)` and updated the pinned digest | **SURVIVED** | Mutation is valid CEL and the Go suite compiled; `go test ./test/conformance/... -count=1` passed. |
| E1 | Restored real cluster: `make conformance-cluster` | **REPRODUCED / PASS** | Strict rule enforced; poisoned loosening returned 429; clean loosening returned 503; all cluster tests passed. This supports the retention claim. |
| M3 | Forced `control = 429` immediately before the NACK test's inconclusive branch | **SURVIVED and REPRODUCED** | Real cluster printed `--- SKIP`, then `PASS`; `make conformance-cluster` exited 0. This reproduces BLOCKER 6. |
| E2 | Restored baseline after every mutation: chart SHA-256 and `make test` | **REPRODUCED / PASS** | Chart digest restored to `16a6f36c…3a8da3a`; full gate passed; worktree was clean before this review file was created. |

## Verdict

**REVISE.** The round closes the stale-Create stage, the signature-enforcement
contract, the circular lease identity, the condition-cause aggregation, and the
NACK traffic measurement. It does not close the guard-loss bypass or the
non-spec revision path, and it introduces a whole-ResourceSet persistence rule
that cannot coexist with the remaining live policy surface. The approval and
auth comparator gaps are in the dangerous direction: an incident-driven
tightening has no safe path, and an OAuth policy change can be misclassified as
no stricter.

Do not treat A26–A29 as implementation-ready until the six blockers have actual
state/storage/admission mechanisms and the three surviving test mutations are
killed.
