# Design 03 / design 02 amendment set — Codex third review

**Verdict: REVISE — 5 BLOCKER, 15 MAJOR, 1 MINOR. Do not start design-03 implementation.**

Review snapshot: `6e5da1a`, covering the ten commits after the second Codex
review at `fb181f0`. I read the current authoritative bodies, ADR-0028,
architecture, the v1.4.1 source research, the execution spike, the documentation
gate, the prior reviews, and the design-02 consumers. I also mutation-checked the
new documentation gate; the ledger is below.

The set has moved materially. A13 is the right correction: the published
overshoot formula is gone from design 03, and the spike demonstrates why. A15
removes the worker-starvation mechanism. A21 is now unconditional and
expressible. A16 corrects permission-versus-selection from equality to subset.
The spike also corrects my r2 wording: the NACK Event is partially correlated to
policy and route, not wholly uncorrelated.

That is not an implementation threshold. The new apply stages cannot complete a
cold create and do not perform the quiesce they require on update. The design
still offers silent fail-open as an alternative to positive dataplane evidence.
A20 changes the hash but not the active workload's mutable references, so the
authorization-equivalent revision bypass survives. A16's endpoint language is
still not rich enough to represent the upstream provider surface. Finally, the
accepted ADR and canonical architecture still publish the budget bound the spike
refuted, while the new gate passes.

## BLOCKER findings

### BLOCKER 1 — A15's staged reconcile is unreachable on create and skips quiescing on update

**Files:** `docs/designs/03-policy-compiler.md:90-119,155-171`.

The stage table applies Backends, then Policies, then evaluates the convergence
tuple, and only then enters `Publishing` to create or attach the Route. But the
measured v1.4.1 behavior is that a Policy whose Route does not exist reports
`Attached=False`. A cold create therefore reaches `Converging` and can never
reach `Publishing`. The inert, parented/no-backend Route measured in spike §2.6
is never created by any stage before the policy barrier.

Updates fail in the opposite direction. A live Route already exists, stale state
restarts at `ApplyingBackends`, and `ApplyingPolicies` changes the policy while
the Route remains live. No stage makes it no-backend first. That directly
contradicts line 171's requirement to make a tightening's dependent routes inert
before applying the change.

**Concrete failure:** a new Agent stays in `Converging` forever because its auth
Policy cannot attach to the Route that `Publishing` has not created. On an
existing Agent, `auth: none -> oauth` updates the Policy under the live public
Route; the old no-auth config serves throughout convergence and, on NACK, can
serve indefinitely.

**Specific fix:** specify separate cold-create and update transactions. Cold
create needs a persisted `PreparingRoute` stage that applies the exact
parented/no-backend Route and observes its Route tuple before Policies are
applied. Tightening needs a `Quiescing` stage before Backends/Policies, with the
exact no-backend manifest and an observation proving withdrawal. Persist the
transaction kind and stage-manifest digest, not only the final ResourceSet
digest. Add continuous-traffic e2e for cold create and each tightening class;
the expected transition is old behavior -> HTTP 500 outage -> new behavior,
never old behavior -> silently claimed convergence.

### BLOCKER 2 — the design still permits publication without positive dataplane evidence

**Files:** `docs/designs/03-policy-compiler.md:117-119,155-171,341-354`;
`docs/research/agentgateway-v1.4.1-spike.md:149-200`.

The spike closes the NACK branch decisively: current-generation
`Accepted=True, Attached=True` can coexist with a proxy NACK, and the previous
permissive rule keeps serving. The Event can raise degradation, but it has no UID
or generation and its absence proves nothing. Design 03 nevertheless says a
tightening “must verify by observation ... **or accept** that a silent NACK
leaves the old rule serving.” That is not a resolved transaction; the second arm
is an explicit authorization bypass. A “generation-correlated synthetic
request” is also not defined: the request carries no ResourceSet generation,
and a denial may be caused by an older policy or another layer.

**Concrete failure:** a tool allowlist is narrowed, all Kubernetes tuples become
current, and no NACK Event has arrived yet. The operator republishes. The proxy
later NACKs, retains the old filter, and the removed tool remains callable while
the Agent reports only control-plane convergence. An old NACK can also be
mistaken for the current generation and strand a healthy transaction because no
recovery predicate is defined.

**Specific fix:** remove the fail-open alternative. Either obtain a positive
upstream ACK correlated to an xDS/config generation, or define a policy-specific
canary route and a witness for every tightening concern (unauthenticated request,
denied tool, narrowed egress destination, rate exhaustion, transform result)
whose outcome is attributable only to the new ResourceSet. Keep the public Route
in the measured no-backend state until that proof succeeds. Specify old-Event
filtering, NACK recovery, and failure timeout. If a concern has no sound witness,
that tightening remains unavailable rather than publishing on absence of error.

### BLOCKER 3 — A20 hashes mutable sources but the serving revision still consumes them live

**Files:** `docs/designs/02-agent-crd-operator.md:99-128,173,233-237,367-379`.

Content hashing detects a changed ConfigMap or Secret and mints a candidate, but
the retained active workload still references the original object name. Nothing
freezes what that workload will read on its next Pod creation. Recording the old
digest in status and refusing rollback do not protect the currently serving
revision. The gap exists even with perfect watches: detection and candidate
creation occur while the old revision remains routed.

**Concrete failure:** R1 passed eval with `SYSTEM_PROMPT=safe` from ConfigMap
`agent-env`. An actor changes it to `SYSTEM_PROMPT=exfiltrate`. The operator
mints R2 and holds it for eval. A routine node drain recreates an R1 Pod; its
unchanged Pod template reads the new ConfigMap and serves the changed prompt
under R1's old hash, route and gate result. No Agent write is required.

**Specific fix:** snapshot every resolved env source into revision-scoped,
immutable ConfigMaps/Secrets and rewrite the revision PodSpec to reference those
snapshot names. The snapshot content digest must be part of the revision input;
source changes create a new snapshot/candidate but cannot change retained Pods.
Rollback should use the retained snapshots rather than re-read and refuse based
on the moving source. Specify source watches/RBAC and GC. E2e must change the
source while R2 is Held, delete an R1 Pod, and prove the replacement still sees
R1 content; repeat for ConfigMap delete/recreate and Secret data.

### BLOCKER 4 — A16's typed allowlist still cannot identify the destinations agentgateway can emit

**Files:** `docs/designs/03-policy-compiler.md:133-140,206-244`;
`docs/designs/02-agent-crd-operator.md:389-393`;
`docs/research/agentgateway-v1.4.1-2026-08.md:391-429`.

Provider-versus-host is not a sufficient endpoint grammar. The shipped API has
managed-provider instances whose endpoint depends on configuration, not only on
the provider arm: Azure OpenAI carries an endpoint/deployment, Vertex carries
project/region, and Bedrock carries region. The generic provider shape also has
`host`, `port`, `path` and `pathPrefix`; A16's host entry carries only a DNS name,
yet claims host/port overrides are checked. Two services on the same host and
different ports are indistinguishable. `models` is admitted by the grammar, but
`egressEnumerated` is defined only over effective hosts, so no rule says how the
model restriction is enforced in the Backend. The still-flat requested
`providers[]` cannot select these structured instances injectively.

The authoritative mandatory table at line 139 also still says the resolved
provider set **equals** the allowlist, while A16 says requested is a subset of
permitted. An implementer can follow either normative sentence.

**Concrete failure:** a profile permits `host: llm.example.com` intending TLS on
443. A requested custom provider emits `llm.example.com:8080`, where an
unapproved service runs; the host-only subset check passes. Separately, two Azure
OpenAI resources share the `azureopenai` arm but have different BAA status; the
arm-level catalog cannot distinguish them, so it either rejects a valid endpoint
or permits both.

**Specific fix:** make both requested providers and permission entries typed and
injective. Resolve stable endpoint IDs into a full tuple at least
`{provider-arm, scheme, host, port, path-prefix, provider-instance fields,
model}`; define redirect behavior and exact model enforcement. Parameterize the
catalog for Azure/Vertex/Bedrock/custom instances instead of mapping one arm to
one host set, and version/hash that catalog as compiler input. Make the mandatory
table say subset. Pin same-host/different-port, two-Azure-instance, region,
model-narrowing and override cases in goldens and denied-egress e2e.

### BLOCKER 5 — current authoritative documents still publish superseded guarantees, and the new gate passes them

**Files:** `docs/decisions/0028-policy-compiler-against-v1-4-1.md:1,8,12`;
`docs/architecture.md:18`;
`docs/designs/03-policy-compiler.md:15,139,178,218,291,315-328,342,354,358,371-380`;
`docs/designs/02-agent-crd-operator.md:18,99,113,219,250`;
`test/docs/superseded_test.go:55-109,199-220`.

ADR-0028 is accepted and live, yet its title and decision 3 promise a “measured
overshoot bound per request per replica.” Canonical architecture repeats it.
Design 03 A13 and §3.5 now say no bound exists. ADR-0028 consequence text also
says NACK behavior remains open after the spike measured it.

The design body still calls the receipt backstop exact, says pricing damage is
bounded, says fail-closed ordering removes all windows, retains an impossible
token “capacity” discussion despite the CRD exposing only tokens/unit, describes
an acceptance poll and wait bound after A15, says R1 still awaits e2e after §3.2
closes it by source, asserts equality after A16, and calls A11 unfinished after
A16. Design 02 still says `revisionHash(spec)` is spec-only and env sources are
hashed by referent, and its reconcile outline still assumes signed-image
admission that A21 says does not exist.

**Concrete failure:** one implementer follows accepted ADR-0028/architecture and
publishes a per-replica overshoot figure; another follows A13 and publishes no
bound. One implements an in-worker poll from §8.1; another implements A15. One
emits allowlist equality from the mandatory table; another emits subset from
A16. All can claim conformance to an authoritative document.

**Specific fix:** supersede or amend ADR-0028 so its title, decision 3 and
consequences encode A13/A14. Update architecture's canonical budget row. Sweep
the complete authoritative bodies of designs 02/03, including security,
testing, decision and failure sections; do not edit only the amendment log.
Expand the documentation gate to semantic rule IDs with independent fixtures
that pin every withdrawn assertion, including `overshoot bound`, `exact
enforcement`, old polling, equality and spec-only hashing. The gate must fail if
any production rule is deleted.

## MAJOR findings

### MAJOR 1 — `PolicyIntent` still conflates no demand with a missing producer

**File:** `docs/designs/03-policy-compiler.md:24-73`.

An empty resolved route input still represents both “the Agent requested none”
and “the Agent requested one but its producer is unavailable.” The design gives
those states different outcomes but sends the pure compiler identical input.

**Concrete failure:** an Agent declares a tool, resolution is unavailable, and
the compiler sees empty `tools[]`; it emits no route and no error, indistinguishably
from an Agent that requested no tool.

**Specific fix:** carry declared route demand and resolution state in
`PolicyIntent`, or make unresolved requested bindings an operator error before
`Compile`. Test absent demand, requested/unresolved, and emitted/missing concern
as distinct states.

### MAJOR 2 — `ReplicaUnverified` still contradicts the P1 disabled-gateway state

**File:** `docs/designs/03-policy-compiler.md:345-350`.

Line 348 calls `ReplicaUnverified` “the P1 state,” while P1 explicitly ships
`gateway.enabled=false`, runs no compiler, and reports only
`GovernanceSkipped`. No rate divisor exists to verify.

**Concrete failure:** a stock P1 Agent reports both a normal disabled-governance
condition and degraded budget enforcement for a limiter that was never emitted.

**Specific fix:** scope replica conditions to `gateway.enabled=true` and at
least one derived rate. Pin the enabled x observable Deployment x derived-rate
matrix.

### MAJOR 3 — the pricing ConfigMap still has two writers for one scalar

**File:** `docs/designs/03-policy-compiler.md:258-291`.

Chart-owned external rows and model-operator-owned `internal/*` rows share the
single `data.models` YAML scalar. SSA cannot give row-level ownership inside one
string.

**Concrete failure:** a Helm upgrade rewrites `models` and deletes operator
rows, or the operator read-modify-write races the chart and loses refreshed
external prices.

**Specific fix:** use separate chart- and operator-owned objects with a
deterministic merge and duplicate rule, or one key/object per row. Test both
write orders and conflicts.

### MAJOR 4 — requested provider and fallback keys remain non-injective

**Files:** `docs/designs/03-policy-compiler.md:279-283`;
`docs/designs/02-agent-crd-operator.md:389-393`.

Structured `{provider, model}` is still joined with `/` while `providers[]` is
opaque. `{azure, openai/gpt-4}` collides with `{azure/openai, gpt-4}`.

**Concrete failure:** the two tuples resolve to one price/endpoint key despite
different provider identity and egress policy.

**Specific fix:** make `providers[]` structured in A22's schema change and use a
length-prefixed/nested canonical encoding. The colliding pair must remain
distinct or be rejected at admission.

### MAJOR 5 — mandatory-entry tests can still disappear with the production registry

**File:** `docs/designs/03-policy-compiler.md:145-151,362`.

“For every registered class” does not require the behavioral case catalog to be
independent of the registry. A generator over production entries deletes the
test case when the entry is deleted.

**Concrete failure:** remove `-auth` from the tool registry and update the
rendered markdown; the golden passes and no generated `-auth` negative case
exists.

**Specific fix:** define a static security-case catalog outside production data
and mutation-run deletion of every production mandatory entry while that catalog
stays fixed.

### MAJOR 6 — route-scoped failures still have no Agent-level aggregation contract

**Files:** `docs/designs/03-policy-compiler.md:127-131,338-341`;
`docs/designs/08-cli.md:77-85`.

The compiler may withhold one tool route while the serving route continues, but
the Agent has one `Ready`, one phase and one condition per type; the CLI treats
any `PolicyCompileFailed`/`PolicyApplyIncomplete` as terminal.

**Concrete failure:** one optional tool fails and the A2A route serves. One
controller reports Ready, while `assayd deploy` aborts as terminal; two failed
routes race to overwrite one condition message and clearing one hides the other.

**Specific fix:** define required-versus-optional route aggregation, stable
multi-resource condition details, phase/Ready precedence, CLI terminality, and
clear only when every scoped failure recovers.

### MAJOR 7 — the leaf walker still has no enforced recursion guard

**File:** `docs/designs/02-agent-crd-operator.md:153-167`.

Self-recursion is stated as a precondition, not detected. The test mandated to
catch schema drift hangs or stack-overflows when the precondition is violated;
that is INVALID, not a failed assertion.

**Concrete failure:** a future CRD field introduces a recursive schema type;
`make test` hangs instead of naming an unclassified field.

**Specific fix:** maintain an active type/path stack, fail with the cycle path,
and require an explicit terminal classifier/perturber for recursive types.

### MAJOR 8 — MCP allowlist emission is still not total at upstream bounds

**Files:** `docs/designs/03-policy-compiler.md:246-256`;
`docs/research/agentgateway-v1.4.1-spike.md:22-39`.

The shipped CRD requires 1..256 expressions, each at most 16,384 bytes. Empty
tool sets, 257 tools, escaping, and overlong hostile names still have no mapping.

**Concrete failure:** an empty valid MCP server causes an invalid empty Allow
policy; omitting the policy to make admission pass restores default allow.

**Specific fix:** emit a valid always-false Allow for empty, or fail the route
with a named compile error; use a CEL encoder and enforce count/length bounds.
Golden-test empty, 256/257, quotes, backslashes, Unicode and max length.

### MAJOR 9 — per-Agent receipt capture remains unrepresentable

**File:** `docs/designs/03-policy-compiler.md:203-204`.

The body now names the correct field, but frontend tracing attaches only to a
Gateway, while `PolicyIntent.captureLevel` is per Agent. The design explicitly
leaves the owner/aggregation rule unresolved.

**Concrete failure:** two Agents request `metadata` and `full`; two reconcilers
write one Gateway policy and last-writer wins, or one setting silently applies to
the entire gateway.

**Specific fix:** choose one gateway-scoped tracing owner and a safe aggregation
rule, or remove `captureLevel` from per-Agent intent. Pin multi-Agent ownership.

### MAJOR 10 — experimental virtual models remain the mandatory stable shift path

**Files:** `docs/designs/03-policy-compiler.md:200-201`;
`docs/decisions/0028-policy-compiler-against-v1-4-1.md:6`;
`docs/architecture.md:264-272,490-492`.

ADR-0028 says `AgentgatewayModel` is experimental/off by default, while the
design and architecture require virtual-model weighted shifting.

**Concrete failure:** a default v1.4.1 install lacks the enabled experimental
surface and cannot execute a model canary described as mandatory.

**Specific fix:** explicitly enable and compatibility-test the experimental CRD
with a feature/failure contract, or design a stable Route/Backend shift and amend
design 25.

### MAJOR 11 — A20/A21 still did not propagate through their authoritative consumers

**Files:** `docs/designs/02-agent-crd-operator.md:99,113,219`;
`docs/architecture.md:83-103`; `docs/designs/08-cli.md:59-62`.

Design 02 still says spec-only hashing, referent-only env hashing and already
enforced signed-image admission. Architecture's canonical Agent uses a mutable
tag and invalid budget shorthand; the CLI says design-02 admission requires a
signature although no verifier exists.

**Concrete failure:** generated examples fail A21 CEL, while a controller author
skips signature handling because the reconcile outline says admission already
did it.

**Specific fix:** update every consumer to digest images, valid budget grammar,
content/snapshot hashing and honest “signature verification not installed”
language. Add examples to schema-validation tests.

### MAJOR 12 — signature verification still has no chosen dependency or failure contract

**Files:** `docs/designs/02-agent-crd-operator.md:235-238,381-387`;
`docs/designs/07-umbrella-chart-ci.md:41-48`.

“Sigstore policy-controller or Kyverno” is not a mechanism. They have different
controllers, lifecycle, pod cost and failure behavior; a binding does nothing if
its controller is absent.

**Concrete failure:** the chart ships Kyverno policy YAML into a cluster without
Kyverno, reports successful installation, and accepts unsigned workloads.

**Specific fix:** choose and pin one installed-or-required verifier, price it,
define install/admission behavior when absent, and e2e an unsigned workload and
verifier outage.

### MAJOR 13 — the design both asserts and disclaims an unmeasured evaluation order

**Files:** `docs/designs/03-policy-compiler.md:175-187,252-256`;
`docs/research/agentgateway-v1.4.1-spike.md:202-206`.

The AuthN row asserts `auth -> rate-limit -> guards`; the Guards row says order
relative to rate limit is an open gap and “not asserted here.” The MCP-specific
documentation supports auth before transformation/rate limiting for that path,
not a global guard order.

**Concrete failure:** unauthenticated traffic reaches a costly guard or consumes
a shared budget before rejection, enabling a budget/compute denial of service
contrary to the implementation's assumed order.

**Specific fix:** remove the global order claim until the spike measures it, or
measure each load-bearing policy combination and bind only the proven scopes.

### MAJOR 14 — the documentation gate's rule catalog is mutation-vacuous

**File:** `test/docs/superseded_test.go:55-109,199-220`.

Deleting the `cuts-early-guarantee` rule left the suite green. The test has no
independent list of required rule IDs, so the detector can lose the rule it
claims to enforce. Its phrase list is also too narrow to catch the live
“measured overshoot bound,” “exact enforcement,” polling and equality claims.

**Concrete failure:** a refactor deletes one `rules` entry; `make test` passes and
the next stale guarantee is no longer gated. This mutation survived exactly.

**Specific fix:** pin expected rule IDs in an independent fixture, require an
explicit corpus fixture that each rule kills, and add semantic rules for every
current retraction. Mutation-run deletion of each rule and detector branch.

### MAJOR 15 — A20 exposes raw content digests as a Secret-guessing oracle

**File:** `docs/designs/02-agent-crd-operator.md:124-128,367-379`.

A20 makes Secret contents revision input and records the digest in Agent status.
Agent readers commonly have broader access than Secret readers. A raw digest of
low-entropy Secret data lets them test guesses offline; the revision name/hash
may expose the same oracle again.

**Concrete failure:** a user may read Agent status but not `provider-config`.
The Secret contains a small environment choice, tenant ID, or human-chosen token;
the user hashes candidate canonical objects until one matches the published
digest.

**Specific fix:** use a cluster-keyed HMAC for revision identity and any visible
digest, keep the key outside Agent-readable state, and never place raw Secret
digests or source values in conditions/events. Pin deterministic HMAC behavior
and key-rotation semantics.

## MINOR finding

### MINOR 1 — architecture still says nothing has been execution-validated

**File:** `docs/architecture.md:484-494`.

The v1.4.1 spike is execution validation of seven behaviors. Leaving “Nothing is
execution-validated” as a current open item hides the strongest evidence in this
review.

**Specific fix:** replace it with the measured list and the one remaining order
question; distinguish dependency-spike evidence from assayd implementation e2e,
which is still absent.

## R2 closure ledger

| R2 finding | R3 result | Reason |
|---|---|---|
| B1 — no observable success barrier | **OPEN / REPRODUCED** | NACK behavior is measured; the design still permits publication without positive evidence (R3 B2) |
| B2 — inert route unspecified | **PARTIAL, MECHANISM INVALID** | The no-backend Route is measured, but A15 never creates it before the barrier and never quiesces updates (R3 B1) |
| B3 — false overshoot bound | **CLOSED IN A13; LIVE CORPUS OPEN** | “Unbounded/no figure” is correct; ADR-0028 and architecture still publish the refuted bound (R3 B5) |
| B4 — egress unfinished | **PARTIAL / OPEN** | Typed subset is right; endpoint identity, provider instances, port/model semantics and flat requested providers remain unresolved (R3 B4, M4) |
| B5 — A20 immutable-name bypass | **REPLACED / OPEN** | Content hashing detects change but does not freeze the serving workload's source (R3 B3) |
| B6 — local digest exception | **CLOSED** | Digest pinning is unconditional |
| B7 — worker-poll DoS | **CLOSED** | A15 returns between stages and requires a fairness test |
| B8 — stale authoritative body | **OPEN** | New stale claims remain in the accepted ADR, architecture and both design bodies; the gate passes (R3 B5) |
| M1–M8 | **OPEN** | R3 M1–M8 |
| M9 — receipt field/scope | **PARTIAL / OPEN** | Field path fixed; Gateway scope versus per-Agent intent is explicitly unresolved (R3 M9) |
| M10 — experimental virtual models | **OPEN** | R3 M10 |
| M11 — A20/A21 propagation | **OPEN** | R3 M11 |
| M12 — signature dependency | **OPEN** | R3 M12 |

## Evidence and mutation ledger

| Check / mutation | Result | Evidence |
|---|---|---|
| Documentation gate baseline | **PASS** | `go test ./test/docs/... -count=1` passed at `6e5da1a` |
| Inject `The gateway cuts early and never late.` into design 03's authoritative body | **KILLED** | `TestNoSupersededGuaranteeInAnAuthoritativeBody` emitted the `cuts-early-guarantee` error and the package printed `FAIL` |
| Delete the production `cuts-early-guarantee` entry from `rules` | **SURVIVED** | The same `go test` command passed; no independent fixture requires that rule to exist |
| Current accepted ADR and architecture publish “measured overshoot bound” | **REPRODUCED** | ADR-0028:1,8 and architecture:18 contain it while the unmutated documentation gate passes |
| Cold-create A15 transition | **REPRODUCED FROM MEASUREMENT + SPEC** | Spike §2.1 shows absent Route => `Attached=False`; A15 requires `Attached=True` before its later `Publishing` stage creates the Route |
| Tightening NACK retains permissive config | **REPRODUCED ON CLUSTER** | Spike §2.8: current-generation accepted/attached, NACK Event, 8/8 requests returned 200 under a 1-request tightening |
| Concurrency bound | **REFUTED / REPRODUCED ON CLUSTER** | Spike §2.7: 100/100 concurrent 1,000-token requests passed a 1,000-token budget on one replica; A13 correctly publishes no figure |
| A20/A15/A16 implementation mutations | **UNAVAILABLE, NOT KILLED** | These subjects do not exist in code; inventing a non-compiling mutation would be INVALID, not evidence |

All file mutations were restored from explicit backup copies; no `git checkout`
was used.

## Disagreement and corrections

I disagree again with `docs/designs/reviews/03-amendments-review.md` round 4's
“0 blocker” and “the substance has converged.” The same failure class survives
after executable evidence arrived: the live ADR and canonical architecture still
state the formula the spike disproved, and a gate created specifically to prevent
that propagation passes. Internal consistency review is not evidence that the
upstream transaction is safe.

I also disagree with A14's statement that the update transaction is “unblocked
to the extent that it can be honest about what it cannot prove.” Honesty closes a
documentation defect. It does not close an authorization defect when the design
still allows publication under the known old permissive rule. “Accept silent
NACK” is a measured fail-open branch, not a valid implementation option.

I correct my own r2 wording: the Event is not wholly uncorrelated. The measured
key names policy and route. That improves attribution for raising degradation
and changes none of the success-barrier finding because the key still lacks UID
and generation, and absence remains non-evidence.

The right judgment is: **the spike paid for itself, A13/A15/A16 contain real
progress, and the set still cannot be implemented safely. REVISE.**
