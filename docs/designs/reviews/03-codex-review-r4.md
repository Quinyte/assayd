# Design 03 / design 02 amendment set — Codex fourth review

**Verdict: REVISE — 7 BLOCKER, 20 MAJOR, 2 MINOR. Do not start design-03 implementation.**

Review snapshot: `a74085b`, covering `91efa8e..a74085b` after the third
Codex review at `6e5da1a`, plus the scoped B3 review. I read the current
authoritative bodies, ADR-0028, architecture, designs 20 and 07/08/10 where
they consume the amended contracts, both v1.4.1 research artifacts, the prior
reviews, and the documentation gate. I also pulled the published
`agentgateway-crds:1.4.1` chart again and mutation-checked the repaired gate.

The set has moved materially. A17 fixes the cold-create deadlock and deletes
the explicit silent-NACK arm. A18/A24 replace the non-injective flat provider
join with a typed endpoint identity. A23 chooses the no-copy seal shape my
scoped review recommended after retracting my r3 snapshot prescription to copy
Secrets. The independent rule fixtures now kill deletion, regex weakening, and
over-broad retraction clauses.

That still is not an implementation threshold. A17 assumes that a negative
probe is caused by the new configuration when every listed outcome has other
causes, and it never proves that the route withdrawal which makes the probe
exclusive reached the dataplane. Its stale-state restart skips that withdrawal.
Its escape for a concern without a witness points policy-surface changes at a
revision path which deliberately does not hash them. A23 explicitly leaves the
shared-source ownership transaction undrafted, and on loss of its admission
guard it protects new revisions while leaving retained serving revisions
mutable. A18's model claim is directly false in the shipped v1.4.1 CRD:
`azureopenai` has no `model` field.

## BLOCKER findings

### BLOCKER 1 — `Quiescing` is accepted by the control plane, not proven at the dataplane

**Files:** `docs/designs/03-policy-compiler.md:107-113,164-174,190-203`;
`docs/research/agentgateway-v1.4.1-spike.md:99-113,149-200`.

A17 correctly puts a no-`backendRefs` route before a tightening, but it advances
`Quiescing` from the HTTPRoute status tuple. The spike measured only creation of
an inert route. It did not measure an update of a live route to that shape. The
same xDS channel has already been measured accepting a Kubernetes generation,
NACKing it in the dataplane, and retaining the old permissive configuration.
Nothing in A17 proves route updates are different.

**Concrete failure:** a public route is live under `auth:none`. The operator
updates it to the probe-only/no-backend shape; HTTPRoute reports
`Accepted=True, ResolvedRefs=True`, but the dataplane NACKs and retains the old
public match. Policies converge and the witness request runs while ordinary
unauthenticated traffic continues through the old route. The transaction claims
the route admitted nothing else, but that premise was never observed.

**Specific fix:** spike a live production-match -> no-backend/probe-only update
under a forced dataplane NACK. Require a dataplane-origin withdrawal witness for
the production identity before applying tightened policies. If agentgateway
cannot provide an attributable withdrawal result, do not mutate the live route:
use a generation-specific shadow route/Backend and an atomic, positively
witnessed cutover, or declare online tightening unsupported.

### BLOCKER 2 — every A17 witness can pass for a reason unrelated to the new policy

**File:** `docs/designs/03-policy-compiler.md:192-203`.

“Same route and same policy attachment” does not establish causation. The four
negative outcomes in the witness table all have independent producers: a
backend can return 401/403; a tool server can omit or reject a tool; DNS or the
destination can be down; an upstream can return 429 or the old bucket can
already be exhausted. A NACK that retains the old config can therefore coexist
with a passing witness. Publishing only widens the match after the false
positive.

**Concrete failure:** a tool-filter tightening is NACKed and the old permissive
filter remains. During the probe the MCP server is unhealthy and rejects the
removed tool itself. Both `tools/list` and `tools/call` match the table, so assayd
publishes; when the server recovers the old filter still exposes the tool.

**Specific fix:** define gateway-origin, policy-specific evidence rather than
HTTP outcomes alone. Each witness needs rejection provenance, a positive control
through the same Backend, fresh state where relevant, and an identifier tied to
the desired resource generation/config. Mutation/e2e must force the backend to
produce the same status while the policy is absent or NACKed and prove the
witness fails. Any concern for which upstream cannot provide such attribution
has no sound online witness.

### BLOCKER 3 — a superseding generation restarts after the transaction's safety stage

**File:** `docs/designs/03-policy-compiler.md:105-120,180-188`.

`status.apply` persists the transaction kind, but line 120 discards stale state
and unconditionally restarts at `ApplyingBackends`. That skips `PreparingRoute`
for a create and `Quiescing` for a tighten. It also loses the conservative
classification when an in-flight loosen is superseded by a tighten.

**Concrete failure:** generation N is loosening auth while the route stays live.
Before it publishes, generation N+1 narrows auth and changes a Backend. The
digest mismatch drops the record and starts N+1 at Backends, applying the
tightening under the live permissive route without `Quiescing`.

**Specific fix:** on stale state, recompute the transaction against the
currently serving applied set and enter that transaction's first stage:
`PreparingRoute` for Create, `Quiescing` for Tighten, `ApplyingBackends` only
for a proven Loosen. Persist the serving-set digest used for classification.
Mutation-test each stage superseded by each transaction kind.

### BLOCKER 4 — “use the revision path” is not a path for policy-surface changes

**Files:** `docs/designs/03-policy-compiler.md:203`;
`docs/designs/02-agent-crd-operator.md:103-121`.

A17 refuses an online tightening with no sound witness and says to mint a
candidate. Design 02 deliberately excludes `budget`, `expose`,
`tools[].requiresApproval`, `gates`, and `loop` from the revision projection and
applies them in place. For those inputs, writing the spec cannot mint the
candidate A17 requires. Reusing the same projected hash cannot create a second
content-addressed revision either.

**Concrete failure:** a public Agent changes `expose.a2a.auth` in a way for
which no attributable auth witness is available. Online application is refused;
the revision hash is unchanged; no candidate exists to gate or shift. The
change is permanently stuck despite the design presenting a recovery path.

**Specific fix:** design a separate policy-revision/shadow-route transaction
with its own digest, retention, gate, rollback and status, or reclassify every
no-witness input onto the behaviour surface. State which concern uses which
path; “mint a candidate” cannot override design 02's hash contract by prose.

### BLOCKER 5 — A23's shared-source ownership and release transaction is still undrafted

**Files:** `docs/designs/02-agent-crd-operator.md:130-132,399-410`;
`docs/research/agentgateway-v1.4.1-spike.md:268-276`.

The scoped B3 review made race-free reference ownership a precondition of the
seal. A23 explicitly records refcounting across two Agents and release on the
last retained revision as “owed, and not drafted.” This is the mechanism that
decides whether a retained active revision is protected, not a test detail.

**Concrete failure:** Agents A and B share ConfigMap C. A garbage-collects its
last referring revision and removes C's protection while B still retains and
serves a referring revision. An editor changes C; a node drain recreates B's
Pod with changed behavior under B's old gated hash. The original bypass returns.
The opposite race leaks a permanent finalizer and makes C undeletable.

**Specific fix:** specify a revision-scoped lease object and a CAS protocol:
acquire the lease before sealing; seal with `resourceVersion`; re-read UID and
content after acquisition before hashing/materializing; release only after an
authoritative list of all live revision leases and a CAS removal; retry if a
concurrent acquire changes the source. Cover Agent deletion, rollback,
retention changes, delete/recreate UID changes, and crash recovery. If this
cannot be proven race-free, A23 itself says to use snapshots.

### BLOCKER 6 — loss of the admission guard leaves active revisions exposed

**Files:** `docs/designs/02-agent-crd-operator.md:130,399-410`;
`docs/research/agentgateway-v1.4.1-spike.md:255-266`.

The spike measured that deleting the Binding immediately permits data updates.
A23 responds by withholding **new** revisions and setting a condition. Existing
retained workloads keep serving and still resolve the now-mutable object by
name. A finalizer prevents deletion; it does not prevent data update or removal
of itself when the policy is absent.

**Concrete failure:** an upgrade temporarily omits or skews the Binding. The
operator refuses R2, but R1 remains routed. A source editor changes R1's
ConfigMap and a node drain recreates R1 from the changed data under its old gate.
The condition is truthful but does not fail closed.

**Specific fix:** loss/skew of either trust-boundary object must immediately
quiesce every route whose retained revisions reference a sealed source, or a
separate independent admission control must continue protecting already-held
sources. Define recovery ordering: re-establish the guard, re-verify every
source UID/content against retained digests, then republish. Test binding loss
with an active Agent and a Pod replacement.

### BLOCKER 7 — A18's “every arm carries model” premise is false in the shipped CRD

**Files:** `docs/designs/03-policy-compiler.md:256-288,486`;
`docs/designs/02-agent-crd-operator.md:412-420`.

The published `agentgateway-crds:1.4.1` schema says `azureopenai` carries
`apiVersion`, `deploymentName`, and `endpoint`; it has no `model` field. For
`apiVersion: v1`, `deploymentName` is optional and the model may be supplied by
the request. The schema also says a scheme included in `endpoint` is stripped.
A18 nevertheless says every arm has a model field and therefore every
`models[]` permission can be compiled into its provider block. PolicyIntent at
line 29 is still the old flat shape as well.

**Concrete failure:** an allowlist permits only model `gpt-4o` on an Azure
OpenAI v1 endpoint. The emitted provider has no field in which to pin that
model; a request selects another deployment/model on the same endpoint. The
endpoint tuple passes while the model restriction is unenforced.

**Specific fix:** define a discriminated assayd type per upstream arm, including
normalization/defaults. For `azureopenai`, either require and treat a
non-v1 `deploymentName` as the constrained model identity, emit a separately
verified request rewrite/guard that pins v1 model selection, or reject
model-scoped entries for that arm. Update PolicyIntent and A24 examples. Add a
real-gateway e2e that requests a second model through the same Azure endpoint
and proves it is blocked.

## MAJOR findings

### MAJOR 1 — `PolicyIntent` still conflates no demand with failed resolution

**File:** `docs/designs/03-policy-compiler.md:23-60`.

An empty resolved slice represents both “not requested” and “requested but its
producer failed.” Those states have different required outcomes but are
identical to the pure compiler. An Agent requesting one unresolved tool can be
treated as an Agent requesting none. Carry declared demand plus resolution
state in the intent, or reject unresolved requested bindings before `Compile`;
test all three states independently.

### MAJOR 2 — valid routes with no rejection concern have no defined witness

**Files:** `docs/designs/03-policy-compiler.md:138-148,182-203`.

`Create` always enters `Witnessing`, but an A2A route with `auth:none` and no
derived rate has no newly required concern and no row in the witness table.
It can stall forever or be published by an unrecorded exception. Make witness
requirements a non-empty set derived from the transaction diff; skip the stage
explicitly when that set is empty, and test the default/no-auth/no-budget cases.

### MAJOR 3 — the Tighten/Loosen comparator is not implementable from “admitted set”

**File:** `docs/designs/03-policy-compiler.md:180-190`.

Rate limits are stateful quantities, transforms are functions, and timeouts and
approval rules are not simple admitted sets. No per-concern partial order or
normalization is specified. Two correct-looking implementations can classify
the same edit differently. Define a closed comparator registry per concern,
with `Unknown -> Tighten`, and property-test reflexivity/transitivity plus every
boundary/default transition.

### MAJOR 4 — the operator exception is wider than the seal needs

**Files:** `docs/designs/02-agent-crd-operator.md:130,401-408`;
`docs/research/agentgateway-v1.4.1-spike.md:222-238`.

The VAP exempts the operator from **data** mutation as well as protection-label
and finalizer maintenance. That unnecessarily creates a privileged way to
change a retained revision's behavior under its old hash. Restrict the operator
exception to exact protection-metadata transitions; data/binaryData must remain
unchanged while any lease exists. Legitimate behavioral changes should use a
new object/UID plus an Agent reference change. Mutation-test an operator data
patch separately from label/finalizer maintenance.

### MAJOR 5 — `EnvSourceProtectionUnavailable` has no condition/state contract

**Files:** `docs/designs/02-agent-crd-operator.md:67-85,235-265,408`.

A23 raises a condition not present in the 27-condition vocabulary or failure
table. Its phase, abnormal-true behavior, Ready effect, route effect, clearing
rule and CLI/alert consumers are undefined. Add it to the canonical condition
set and enumerate absent, skewed, restored-but-unverified, and restored-verified
states. This is separate from BLOCKER 6's missing fail-closed action.

### MAJOR 6 — `ReplicaUnverified` still calls the disabled P1 state degraded

**File:** `docs/designs/03-policy-compiler.md:392-399`.

Line 397 calls `ReplicaUnverified` “the P1 state,” while P1 has
`gateway.enabled:false`, runs no compiler, and reports `GovernanceSkipped`.
Scope replica conditions to gateway-enabled Agents with at least one derived
rate; pin the enabled × observable Deployment × derived-rate matrix.

### MAJOR 7 — the pricing table still has two writers for one YAML scalar

**File:** `docs/designs/03-policy-compiler.md:309-377`.

Chart-owned external rows and model-operator-owned `internal/*` rows share
`data.models`. SSA cannot own individual rows inside a string. A Helm upgrade
can erase operator rows; a read-modify-write race can erase refreshed external
prices. Use separate objects with deterministic merge/duplicate rules, or one
key/object per row, and test both write orders.

### MAJOR 8 — A24 deletes the flat identity but pricing still requires it

**Files:** `docs/designs/03-policy-compiler.md:288,309-330`;
`docs/designs/02-agent-crd-operator.md:412-420`.

The text says no canonical string exists, then the pricing ConfigMap remains
keyed `<provider>/<model>` and lookup is described against a typed tuple. This
is explicitly owed, but it blocks a deterministic compiler. Define a typed,
injective pricing schema with arm-specific identity/default normalization and a
single lookup rule before implementation. Pin the former slash collision and
two same-arm/different-instance rows.

### MAJOR 9 — design 20 still correlates drift and fallback by `(provider, model)`

**File:** `docs/designs/20-drift-controllers.md:18-38`.

A24 makes endpoint instance part of identity, but drift canaries, receipts,
baselines and fallback guards still collapse it to provider/model. Two Azure
endpoints with different deployment, region or BAA status can share a baseline
and remediation decision. Amend design 20 to use the full typed endpoint ID in
receipts, conditions, baselines and fallback activation; test two same-model
instances with only one drifted.

### MAJOR 10 — mandatory-entry tests can still disappear with the registry

**File:** `docs/designs/03-policy-compiler.md:150-158,411`.

“For every registered class” still permits tests generated from production
data. Deleting a mandatory entry deletes the generated negative case, exactly
the defect the docs gate has now repaired for itself. Require an independent,
static security-case catalog and mutation-run deletion of each production
entry while that catalog stays fixed.

### MAJOR 11 — route-scoped failures lack an Agent-level aggregation contract

**Files:** `docs/designs/03-policy-compiler.md:132-136,383-399`;
`docs/designs/02-agent-crd-operator.md:67-85,235-265`.

One optional tool route may fail while A2A continues, but the Agent has one
phase, one Ready condition, and one condition instance per type. Concurrent
route failures can overwrite each other's message, and clearing one can hide
another. Define required/optional route aggregation, stable multi-resource
details, Ready/phase precedence and clearing only after all scoped failures
recover.

### MAJOR 12 — the leaf walker still has no enforced recursion guard

**File:** `docs/designs/02-agent-crd-operator.md:153-171`.

Self-recursion is only a prose precondition. A future recursive type makes the
test hang or stack-overflow, which is INVALID rather than a useful failure. Keep
an active type/path stack, fail with the cycle path, and require an explicit
terminal classification for recursive types.

### MAJOR 13 — MCP allowlist emission is not total at upstream bounds

**Files:** `docs/designs/03-policy-compiler.md:294-305`;
`docs/research/agentgateway-v1.4.1-spike.md:22-39`.

The CRD requires 1..256 expressions and caps each at 16,384 bytes. Empty sets,
257 tools, escaping and hostile/overlong names still have no mapping. Define a
valid always-false Allow or named compile failure for empty, enforce bounds with
a CEL encoder, and golden-test 0/256/257 plus quoting, slashes and Unicode.

### MAJOR 14 — per-Agent receipt capture is still unrepresentable

**File:** `docs/designs/03-policy-compiler.md:235-236`.

`captureLevel` is per Agent, but v1.4.1 frontend tracing attaches only to a
Gateway. Two reconcilers can last-writer-win one Gateway policy. Choose one
gateway-scoped owner and a safe aggregation rule, or remove `captureLevel` from
PolicyIntent; pin multi-Agent ownership.

### MAJOR 15 — experimental virtual models remain the mandatory shift path

**Files:** `docs/designs/03-policy-compiler.md:232-233`;
`docs/decisions/0028-policy-compiler-against-v1-4-1.md:6-12`;
`docs/architecture.md:272,492`.

ADR-0028 says `AgentgatewayModel` is experimental and off by default; the
roadmap still requires it for weighted model shifting. Choose an explicit
feature/failure contract and compatibility battery, or use a stable Route/
Backend mechanism and amend design 25.

### MAJOR 16 — A20/A21/A23/A24 did not fully reach authoritative consumers

**Files:** `docs/designs/02-agent-crd-operator.md:101,115,223-224,239-242`;
`docs/designs/03-policy-compiler.md:23-34,403-423`;
`docs/architecture.md:494`.

The body still says env sources are hashed by referent, the reconcile outline
says signed-image admission is already enforced, PolicyIntent shows the old
flat LLM shape, security claims a “backstop bound,” and testing still specifies
an acceptance poll/wait bound after A15. Implementers can build old mechanisms
from current authoritative sections. Sweep and update the body, not only the
amendment logs; the docs-gate finding below explains why the current gate did
not catch them.

### MAJOR 17 — signature verification still has no chosen dependency or failure contract

**Files:** `docs/designs/02-agent-crd-operator.md:242,267-269,355,391`.

The design correctly retracts the CEL claim but leaves Sigstore policy-controller
versus Kyverno undecided, with no install tier, namespace selector, outage
behavior, exception path or condition. A digest pin is not a signature check.
Choose the dependency in design 07/ADR, define fail-closed behavior and BYO
scope, and integration-test unsigned and verifier-unavailable images.

### MAJOR 18 — evaluation order is asserted and disclaimed in the same mapping

**File:** `docs/designs/03-policy-compiler.md:207-219`.

The AuthN row asserts native order `auth -> rate-limit -> guards`; the Guards
row says order relative to rate-limit is unverified. Policy placement and cost/
leak behavior depend on which is true. Remove the assertion until the ordering
spike measures it, then pin the result with an observable multi-policy case.

### MAJOR 19 — the documentation gate is structurally pinned but semantically porous

**Files:** `test/docs/superseded_test.go:55-139,229-302`;
`docs/designs/03-policy-compiler.md:403,420`.

Independent fixtures now kill deletion, weakened matching and over-broad
rescues. But the current authoritative body contains `acceptance **poll**` and
`backstop bound`; both pass because the regex expects contiguous
`acceptance poll` and plural `backstop bounds`. Removing Markdown emphasis or
pluralizing one word makes the gate fail. This is a lexical tripwire, not the
claimed guarantee that superseded semantics cannot survive.

Normalize Markdown to plain text before matching and match semantic stems, or
represent supersessions as stable rule IDs attached to authoritative sections.
Add independent fixtures with emphasis/code/link boundaries, singular/plural,
hyphenation and reordered equivalents. Keep the now-good deletion mutations.

### MAJOR 20 — raw Secret-content digests are a guessing oracle

**File:** `docs/designs/02-agent-crd-operator.md:126,177,377-381`.

A20 records content digests in broadly readable Agent status. Hashes of
low-entropy Secret values such as feature flags, usernames or short tokens can
be guessed offline by anyone with Agent read access but no Secret read access.
Use a cluster-keyed HMAC for status/equality, keep any raw digest in a restricted
object, and define key rotation. Test that equal content remains comparable
without exposing a public verifier.

## MINOR findings

### MINOR 1 — review/status headers are stale

**Files:** `docs/designs/03-policy-compiler.md:3`;
`docs/designs/02-agent-crd-operator.md:3`.

Design 03 says A1-A16 and two Codex reviews despite A17/A18 and r3/r4; design 02
says A20/A21 are undrafted despite A20-A24. Update the metadata after the
substantive findings are resolved.

### MINOR 2 — architecture's execution status is now false

**File:** `docs/architecture.md:484-495`.

“Nothing is execution-validated” survived two real spikes. Replace it with an
evidence ledger naming what is measured and, more importantly, what remains
unmeasured: A17's live route transitions/witness attribution and A23's shared
leases/node drain.

## R3 and scoped-B3 closure ledger

| Prior finding | R4 judgment |
|---|---|
| r3 B1 staged create/update ordering | **PARTIAL** — `PreparingRoute` and `Quiescing` now exist, but stale restart bypasses them and route withdrawal is not dataplane-proven (B1, B3) |
| r3 B2 silent NACK | **PARTIAL** — the fail-open arm is deleted; the replacement witness is not causally attributable (B2, B4) |
| r3 B3 mutable env source | **PARTIAL** — seal-in-place is the right no-copy direction; shared ownership and guard-loss behavior remain load-bearing and undrafted (B5, B6) |
| r3 B4 endpoint grammar | **PARTIAL** — tuple identity fixes arm/instance ambiguity; Azure v1 model enforcement contradicts the shipped CRD (B7) |
| r3 B5 stale docs/gate | **PARTIAL** — exact rule mutations are now killed; semantic formatting variants and live stale statements survive (M16, M19) |
| r3 M1 demand vs producer | **OPEN** (M1) |
| r3 M2 ReplicaUnverified | **OPEN** (M6) |
| r3 M3 pricing ownership | **OPEN** (M7) |
| r3 M4 non-injective provider key | **CLOSED for Agent fields**, reopened at pricing and downstream identity propagation (M8, M9) |
| r3 M5 mandatory-test deletion | **OPEN** (M10) |
| r3 M6 route aggregation | **OPEN** (M11) |
| r3 M7 recursion guard | **OPEN** (M12) |
| r3 M8 MCP bounds | **OPEN** (M13) |
| r3 M9 tracing | **OPEN** (M14) |
| r3 M10 experimental models | **OPEN** (M15) |
| r3 M11 A20/A21 reach | **OPEN**, now includes A23/A24 (M16) |
| r3 M12 signature dependency | **OPEN** (M17) |
| r3 M13 evaluation order | **OPEN** (M18) |
| r3 M14 gate rule deletion | **CLOSED for exact rules**, semantic bypass remains (M19) |
| r3 M15 digest oracle | **OPEN** (M20) |
| r3 minor architecture execution claim | **OPEN** (minor 2) |
| scoped B3: snapshot vs seal | **My original r3 snapshot prescription is withdrawn.** The scoped review's seal recommendation survives the no-copy/rotation costs, conditional on the lease and guard-loss protocol that A23 still does not specify |

## Evidence and mutation ledger

All mutations compiled. A test failure below is a real `FAIL`, not an INVALID
build.

| Mutation / execution | Result | Classification |
|---|---|---|
| Baseline `go test ./test/docs/... -count=1` | PASS | baseline |
| Delete production `cuts-early-guarantee` rule while its independent fixture remains | `TestEveryRuleIsPinnedByAnIndependentFixture` fails: fixture has no rule | **KILLED** |
| Weaken that rule's banned regex so it no longer matches the fixture | fixture test fails: pattern weakened | **KILLED** |
| Broaden its allowed regex so it rescues its own assertion fixture | fixture test fails: retraction clause too broad | **KILLED** |
| Current authoritative `acceptance **poll**` with baseline gate | PASS | **SURVIVED / REPRODUCED** |
| Remove only the emphasis, yielding `acceptance poll` | gate fails on `in-worker-poll` | **KILLED**, proving formatting sensitivity rather than semantic coverage |
| Current authoritative singular `backstop bound` with baseline gate | PASS | **SURVIVED / REPRODUCED** |
| Change only to plural `backstop bounds` | gate fails on `pricing-damage-bounded` | **KILLED**, proving inflection sensitivity |
| Pull `oci://ghcr.io/agentgateway/charts/agentgateway-crds:1.4.1` and inspect `azureopenai` | fields are `apiVersion`, `deploymentName`, `endpoint`; no `model`; v1 may take model from request | **REPRODUCED** B7 against the shipped artifact |

The spike's A23 evidence is correctly bounded: data/label/finalizer/delete attacks
by a source editor are **REPRODUCED denied**; operator data mutation is
**REPRODUCED allowed**; deletion of the Binding **REPRODUCES the bypass**.
Refcounting, last-holder release and node drain remain explicitly UNMEASURED.

## Disagreement record

I continue to disagree with `03-amendments-review.md` round 4's “0 blocker” and
“the substance has converged.” That judgment was internally coherent and still
missed the upstream version/API contract; the current set repeats the same class
of error in the sentence “every arm carries model,” which the shipped v1.4.1 CRD
directly refutes.

I also disagree with A17's assertion that same-route placement makes attribution
sound. Placement proves only that the request traversed the route. It does not
prove which layer produced a negative result, nor that the route mutation which
excluded production traffic reached the dataplane. The old fail-open arm was
deleted, but an unproven witness is not positive evidence merely because it
stands in that arm's place.

Finally, I disagree with my own r3 snapshot prescription to copy every Secret.
The scoped review superseded it after pricing the retention and credential-copy
costs. Seal-in-place is the better shape if and only if shared ownership,
release, guard loss and node-drain behavior are made part of the mechanism and
measured. A23 records three of those as owed; therefore it has not yet crossed
that condition.
