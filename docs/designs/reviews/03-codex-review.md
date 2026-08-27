# Design 03 amendments A1–A9 / design 02 A15–A19 — Codex cold review

**Verdict: REVISE — 7 BLOCKER, 7 MAJOR, 0 MINOR.**

Review snapshot: `bcc7a98`, after the amendment commits `ec0a8d5`, `046d83f`,
`5e3b043`, `a8be2ef`, and `bcc7a98`. Line numbers below are against that
snapshot. `7617384` landed design 02 A20/A21 while this review was running; those
later decisions are deliberately excluded rather than allowed to move the
review target.

Scope read independently before reading `03-amendments-review.md`: design 03
§§3.1, 3.3, 3.3.1, 3.5, 5, 8.1 and A1–A9; design 02 §§3.1, 3.3, 5 and A15–A19;
ADR-0020, ADR-0027; architecture §00/§01; and the amended rows in designs 01,
07, 08, 10 and the designs index. No plume implementation exists to mutate for
this design. I therefore checked the load-bearing third-party behavior against
agentgateway's published 2.2 API and pinned upstream source, and executed the
boundary arithmetic rather than treating the prose as proof.

## BLOCKER findings

### BLOCKER 1 — the acceptance barrier checks a condition that is true when the policy is not attached

**Files:** `docs/designs/03-policy-compiler.md:90-99,107,242-243,269-273`;
`docs/decisions/0020-policy-compiler.md:4`.

The specified creation order is not a fail-closed transaction. A direct
`AgentgatewayPolicy` targeting an `HTTPRoute` that does not exist is reported by
agentgateway as `Accepted=True` and `Attached=False`. That is not hypothetical:
the pinned upstream fixture
[`http-route-missing-not-attached.yaml`](https://github.com/agentgateway/agentgateway/blob/53f395260c66edae58857485765d5724680e8567/controller/pkg/agentgateway/plugins/testdata/trafficpolicy/http-route-missing-not-attached.yaml)
has exactly that result. The design waits only for `Accepted=True`, because the
route it targets is deliberately created later. It therefore treats the fixture
as success and creates the route. A bad target, section, or attachment can leave
the route reachable while the mandatory auth/tool-filter policy is unattached.
That is the authorization bypass A1 claims to make impossible.

The same predicate has two further holes:

- it does not require the condition's `observedGeneration` to equal the
  resource's current `metadata.generation`, so an old `Accepted=True` satisfies
  the poll immediately after a stricter spec is applied; and
- Backend `Accepted=True` is only a translation result. The pinned controller
  sets it after successful translation even when
  `LookupGatewaysForBackend` returns no gateways and emits no data-plane
  resource ([source](https://github.com/agentgateway/agentgateway/blob/53f395260c66edae58857485765d5724680e8567/controller/pkg/syncer/backend/backend_plugin.go#L214-L261)).

`Accepted=True/programmed` is not a predicate. These resources expose different
condition shapes and meanings; the slash conceals the missing contract.

**Concrete failure:** create a public A2A route whose OAuth policy targets the
not-yet-created route. Agentgateway returns `Accepted=True, Attached=False`.
Plume creates the route as step 3. If attachment remains pending or fails, the
public route exists without the OAuth guarantee while plume records the
mandatory concern as accepted.

**Specific fix:** make route publication a two-phase operation. Create an inert
route object first (no parent attachment or another mechanically unreachable
form), create its policies, and require the agentgateway-owned ancestor for the
exact target to have `Accepted=True` **and** `Attached=True`, both at the current
generation. Require Backend `Accepted=True` at its current generation, then
require the route's own current-generation `Accepted`/`ResolvedRefs` and the
published/programmed signal the pinned controller actually provides. Only then
attach or weight the route into traffic. Specify the exact condition tuple per
kind, including controller name, target reference, generation, and negative
conditions. Envtest must pin stale-true and accepted-but-unattached cases; e2e
must prove the route cannot pass traffic during either state.

### BLOCKER 2 — the protocol is silent on mandatory-policy updates to an already-serving route

**Files:** `docs/designs/03-policy-compiler.md:90-99,121,127`;
`docs/decisions/0020-policy-compiler.md:4`.

The document defines creation order and reverse removal, but not update order.
An existing route stays live while SSA changes a mandatory resource and while
the 30-second acceptance wait runs. Zero-weighting is described only after a
webhook/status failure is observed. That is too late for a tightening update.

**Concrete failure:** an Agent changes `expose.a2a.auth` from `none` to OAuth, or
a tool allowlist is narrowed. The operator updates the policy while the route
continues to serve under the old permissive configuration. If the new policy is
rejected, the route is zero-weighted only after the wait; if the old status is
mistaken for the new generation, it is never zero-weighted. Traffic in the gap
violates the new declared policy and ADR-0020's “no fail-open windows.”

**Specific fix:** specify a dependency-changing update transaction: make every
dependent route inert first and observe that state, update Backends/policies,
wait for the exact current-generation barriers from BLOCKER 1, then republish
the route. Define this for add, tighten, loosen, replace, and mixed
add/remove updates; do not infer safety from SSA field ownership. Add an e2e
transition test that continuously sends unauthenticated traffic while
`auth:none -> oauth` is applied and proves no request succeeds after the new
generation is accepted by the API server.

### BLOCKER 3 — the local token limiter cannot satisfy “cuts early, never late”

**Files:** `docs/designs/03-policy-compiler.md:139-141,199-201,208,220-222,229`;
`docs/decisions/0020-policy-compiler.md:4`; `docs/architecture.md:18`.

Agentgateway 2.2 explicitly says that input and output counts are not known
until completion and token limits “apply to future requests only”
([API reference](https://agentgateway.dev/docs/kubernetes/2.2.x/reference/api/#localratelimit)).
Its standalone documentation is even clearer: the over-limit response is still
returned and only later requests are limited
([rate-limit behavior](https://agentgateway.dev/docs/standalone/main/configuration/resiliency/rate-limits/#at-response-time)).
The design's bucket arithmetic can bound refill; it cannot stop the request that
crosses the bound.

**Concrete failure:** a bucket has one token remaining and admits an LLM request
whose completion consumes 50,000 tokens. The response is returned. Only the next
request receives 429. With two replicas, each can do this. The receipt backstop
also trails, so neither tier “cuts early.”

**Specific fix:** either reserve a conservative request cost before dispatch
(tokenized input plus an enforced maximum output), reconcile the reservation to
actual usage, and reject a request whose reservation exceeds the remainder; or
replace the hard-ceiling claim with an explicit, tested overshoot bound per
request and replica. If agentgateway cannot reserve output tokens, the
architecture and ADR must stop calling this conservative in the “never late”
sense. The design-07 traffic battery must include a single request larger than
the remaining bucket, not only repeated small requests.

### BLOCKER 4 — the arithmetic is not mapped into the real CRD and valid design inputs overflow it

**Files:** `docs/designs/03-policy-compiler.md:23-34,53,181-227,233`.

The design computes `rate` and `capacity` as int64-like values but never states
the emitted `LocalRateLimit` fields. In the pinned 2.2 API, `tokens` and `burst`
are `*int32`; `unit` is required; and `burst` is an allowance **above** the base
limit ([source](https://github.com/agentgateway/agentgateway/blob/53f395260c66edae58857485765d5724680e8567/controller/api/v1alpha1/agentgateway/agentgateway_policy_types.go#L3235-L3266)).
The six-digit USD grammar only proves the intermediate fits int64. It proves
nothing about the output type. `gatewayReplicas` is also an unconstrained `int`;
defaulting it to one does not reject zero.

Executed boundary case using inputs the design admits:

```
Uµ=999999999999, Pµ=1, replicas=1
tokensPerDay=999999999999000000
rate=11111111111100
capacity=39999999999960000
int32 max=2147483647
```

Both emitted numbers exceed int32 by orders of magnitude. With
`gatewayReplicas=0`, the same formula divides by zero. Also, naively emitting
`tokens=rate, unit=Seconds, burst=capacity` creates a starting ceiling of
`rate+capacity`, not the capacity the proof uses.

**Concrete failure:** a schema-valid high USD budget and a low-priced model
compile successfully through the documented int64 step, then cannot be
represented in `AgentgatewayPolicy`; a cast wraps or the API rejects the
resource. Separately, `--gateway-replicas=0` panics the reconcile path, violating
the total compiler contract.

**Specific fix:** validate the chart value and flag as `1..N`; use checked
multiply/divide; define the exact CRD mapping (for example `tokens=rate`,
`unit=Seconds`, `burst=capacity-rate` if that matches verified gateway
semantics); and compile-error unless every emitted field fits the real API type.
Derive boundary tests from imported CRD types, including max accepted, max+1,
zero replicas, and both-budget minimum selection. Do not duplicate an assumed
`int64` fixture type in tests.

### BLOCKER 5 — the “exact” USD backstop uses the same stale estimate as the approximation

**Files:** `docs/designs/03-policy-compiler.md:168-199,229,255`;
`docs/designs/04-receipt-tap.md:35-45,51-58`; `docs/architecture.md:18`.

Receipts call the value `usd_est` and derive it from the design-03 pricing
ConfigMap. The gateway approximation uses that same ConfigMap. Therefore the
“exact” backstop is exact only at summing its own estimates; it is not exact in
USD and is not independent of the approximation. The design then makes stale
pricing advisory and claims the receipt backstop bounds the damage. It does not.

**Concrete failure:** a vendor doubles a model's price while the chart-shipped
table remains 29 days old. The local limiter admits twice the intended real
spend, and every receipt prices the traffic at the same stale half-price. The
aggregate reaches `BudgetExhausted` only after roughly twice the actual USD
budget. Editing a price downward, accidentally or maliciously, defeats both
tiers at once.

**Specific fix:** choose and document one honest contract. For a hard USD bound,
obtain independently authoritative billed cost or use effective-dated,
immutable pricing versions and fail closed for USD-budgeted agents when pricing
is stale/unresolved; receipts must carry the pricing digest/version used. If
neither is acceptable, rename the field and architecture guarantee to estimated
USD and publish a maximum error/overshoot bound. A common mutable estimate
cannot be the backstop for itself.

### BLOCKER 6 — `egressAllowlist` has no defined language or enforceable translation

**Files:** `docs/designs/03-policy-compiler.md:23-30,47,72-74,117,127,147,187-191,210`;
`docs/designs/02-agent-crd-operator.md:49-55,124-126`.

The security control ADR-0014 rests on is represented as `egressAllowlist?` and
described as a Backend restriction, but the design never defines what one entry
is: DNS host, URL, provider identifier, model identifier, Backend reference, or
pattern. `providers[]` is explicitly opaque. No provider-to-endpoint catalog or
resolver is named, yet §3.5 requires the compiler to decide whether a structured
fallback “resolves inside” the list. Agentgateway's Backend schema exposes
concrete provider/host configuration; it has no generic `egressAllowlist` field
for the compiler to copy.

**Concrete failure:** given provider `azure/openai/gpt-4` and allowlist entry
`azure/openai`, two implementations can reasonably treat the entry as a provider
prefix or as a hostname-like identifier and emit different reachable hosts. A
managed provider default, custom host override, redirect, or fallback can then
leave the Backend reaching an endpoint the operator believed excluded while the
registry predicate still says `egressRestricted`.

**Specific fix:** define a canonical allowlist grammar and authoritative
provider-resolution catalog, including custom endpoints, ports, redirects, DNS,
and the `internal/*` exception. Define the exact `AgentgatewayBackend` shape
that contains only resolved allowed endpoints and the predicate that proves it.
Reject unresolved/ambiguous entries. Add golden collision fixtures and a
design-07 e2e that attempts traffic to an unlisted endpoint through the emitted
Backend. A boolean registry predicate is not enforcement until its witness is
specified.

### BLOCKER 7 — a 30-second in-worker poll makes policy failure a fleet control-plane denial of service

**Files:** `docs/designs/03-policy-compiler.md:90-99,259,269-273`.

Setting `MaxConcurrentReconciles` explicitly does not bound queue delay. Each
not-accepted Agent occupies one worker for up to 30 seconds, requeues, and does
it again. A namespace principal allowed to create Agents can keep more bad
objects than the worker count pending indefinitely; ordinary rollout, budget
hold, kill, and teardown reconciles then wait behind them. Increasing the worker
count only changes the number of bad objects required and increases pressure on
the API server.

The prior Claude critique forced the poll back onto the worker because an
off-worker goroutine could apply stale routes. That identifies a bad alternative,
not a binary choice. A staged reconcile preserves per-object serialization
without sleeping in a worker.

**Concrete failure:** with ten workers, create eleven Agents whose mandatory
policy never becomes Attached. Every worker spends almost all its time polling
those objects. A kill-switch update for a healthy Agent can be delayed by
minutes or arbitrarily longer as the bad fleet grows.

**Specific fix:** make apply progress an event-driven state machine. Apply the
current generation's dependencies, persist the desired resource-set digest,
generation, stage, and deadline in Agent status, and return. Watches on policy,
Backend, and route status enqueue the Agent; `RequeueAfter` handles the deadline.
On every reconcile, discard status for a non-current digest/generation before
advancing. Controller-runtime's per-key serialization remains intact, but no
worker waits. Add a fairness test with more permanently pending Agents than
workers and assert a separate kill/teardown reconcile completes within a stated
bound.

## MAJOR findings

### MAJOR 1 — the compiler input cannot distinguish “not requested” from “producer not installed”

**Files:** `docs/designs/03-policy-compiler.md:37-60,72,101-109,239-241`.

`Compile` is pure over `PolicyIntent`, but `PolicyIntent` carries neither
declared route demand nor producer/capability availability. An empty `tools[]`
therefore means both “the Agent requested no tools” and “the Agent requested a
tool but design 11 is unavailable and nothing resolved it.” The document assigns
different behavior to those states, but the function cannot observe which one
it is.

**Concrete failure:** an Agent declares `tools: [{name: claims-system}]` on a
cluster without the design-11 producer. The operator passes no resolved tool.
The compiler sees the same input as an Agent with no tools, emits no route and no
error, and the Agent may report Ready while a declared capability is absent.

**Specific fix:** add independently sourced route demand and producer state to
the compile request, or make unresolved requested bindings an operator error
before compilation. The not-emitted branch may apply only when the Agent did not
request the class. Pin all three states: not requested, requested+producer
absent, and requested+mandatory input missing.

### MAJOR 2 — `ReplicaUnverified` is declared as a P1 degradation when enforcement is declared off

**Files:** `docs/designs/03-policy-compiler.md:62-72,76-80,246-251`.

The state table says P1 has `gateway.enabled=false`, the compiler does not run,
and `GovernanceSkipped` is the complete loud signal. The failure table then says
an unobservable gateway produces `BudgetEnforcementDegraded=ReplicaUnverified`
and “This is the P1 state.” No budget limiter exists in that state, so its
replica divisor cannot be unverified.

**Concrete failure:** every stock P1 Agent can carry both
`GovernanceSkipped=GatewayDisabled` and `BudgetEnforcementDegraded=ReplicaUnverified`.
One says enforcement was deliberately not promised; the other says enforcement
degraded. Operators and alerts cannot tell whether there is an incident.

**Specific fix:** evaluate replica skew/unverified only when
`gateway.enabled=true`, the relevant Deployment is expected, and at least one
rate was derived for that Agent. Remove the P1 sentence and add the complete
condition matrix for enabled × deployment-observable × rate-emitted.

### MAJOR 3 — two writers cannot safely own rows inside one YAML scalar in a ConfigMap

**Files:** `docs/designs/03-policy-compiler.md:168-179,195-199,229`.

`data.models` is one multiline string. The chart owns external rows while the
model operator upserts `internal/*` rows into that same scalar. Kubernetes field
ownership stops at `data.models`; it cannot give the chart and operator
disjoint ownership of YAML rows inside the string. “The model-operator must not
touch `updated`” does not solve concurrent writes to `models`.

**Concrete failure:** a Helm upgrade refreshes external prices while the model
operator adds an internal model. One writer receives an SSA conflict or a
read-modify-write race loses the other's rows. The next compile either rejects
external Agents as unpriced or silently loses the internal zero-price row.

**Specific fix:** use separate chart-owned and operator-owned ConfigMaps with a
defined merge and duplicate-key rule, or make each model a distinct ConfigMap
data key whose ownership Kubernetes can track. Do not put two controllers inside
one scalar field.

### MAJOR 4 — structured fallback identity is collapsed through the collision the design already knows about

**Files:** `docs/designs/03-policy-compiler.md:170,187-191,208-210`.

The design correctly records that joining provider and model with `/` is
non-injective, then requires structured fallback to canonicalize using exactly
that join. `{provider: azure, model: openai/gpt-4}` and
`{provider: azure/openai, model: gpt-4}` both become
`azure/openai/gpt-4`. A “slash-containing model in both forms” golden cannot
prove two structured tuples stay distinct if the production key has already
collapsed them.

**Concrete failure:** the two tuples have different price and BAA endpoint
rules. Fallback lookup selects the same row for both, so one Agent is
underpriced or admitted to the wrong endpoint.

**Specific fix:** make the pricing key a structured tuple (nested map or an
injective length-prefixed encoding), or constrain provider names so `/` cannot
occur and enforce it at admission. Add the exact colliding pair above and assert
distinct lookup results or a compile error.

### MAJOR 5 — the mandatory-entry behavioral test can delete itself with the registry entry

**Files:** `docs/designs/03-policy-compiler.md:123-131,261-263`.

“For every registered class, one test per mandatory entry” is still satisfiable
by generating test cases from the registry. Delete `-auth` from the registry and
the corresponding behavioral subtest disappears. The golden catches a code-only
mutation, but the text explicitly claims the behavioral suite remains an
independent anchor when the table and registry are edited together; that is true
only if the cases are fixed independently of the production registry.

**Concrete failure:** remove tool-route `-auth` from the registry and matching
markdown row. A generated test enumerates only `-toolfilter`, all tests pass, and
the tool route is emitted without authentication.

**Specific fix:** require a static, reviewable set of security cases not
enumerated from production registry contents, plus a mutation harness that
deletes each production mandatory entry while leaving cases unchanged and
expects a compiled test failure (not an invalid build). Keep the golden for
code/table drift; it proves a different property.

### MAJOR 6 — route-scoped failures have no Ready/phase/condition aggregation contract

**Files:** `docs/designs/03-policy-compiler.md:105-109,235-251`;
`docs/designs/02-agent-crd-operator.md:225-251`;
`docs/designs/08-cli.md:84`.

The compiler deliberately lets independent routes continue, but the Agent has
one `Ready`, one phase, and one condition of each type. The amendments never say
what those fields report when A2A is serving and one tool route fails, nor how
multiple failed routes are aggregated. The CLI treats any
`PolicyCompileFailed`/`PolicyApplyIncomplete` as terminal for deploy, while the
design also says one bad tool must not take the Agent off air.

**Concrete failure:** A2A and one tool route are accepted; a second tool route is
unresolvable. Implementations can reasonably report `Ready=True/phase:Ready`,
`Ready=True/phase:Degraded`, or `Ready=False/phase:Pending`. A second failure can
overwrite the first condition message. All satisfy some paragraph and give
different automation behavior.

**Specific fix:** add a state table keyed by failed route class and whether it
is serving-required, covering `Ready`, `Progressing`, phase, CLI terminality,
and recovery. Specify deterministic aggregation of all failing resource paths
into the single condition and clear it only when none remain.

### MAJOR 7 — the mandated reflection test hangs on the schema change it calls a precondition

**Files:** `docs/designs/02-agent-crd-operator.md:147-161`.

The amendment states that `AgentSpec` must not gain a self-recursive type because
the walker has no type stack. A test intended to force classification should
report that violation, not recurse until stack exhaustion or hang CI. Calling it
a design precondition does not make the test safe.

**Concrete failure:** a future field embeds a recursive schema type. The
classification test never reaches an assertion; CI crashes or hangs instead of
naming the new field and required decision. That is an INVALID mutation, not a
killed one, and proves none of the classification property.

**Specific fix:** require a recursion stack keyed by type and path. Encountering
a cycle must fail immediately with the full field path and “recursive type needs
an explicit classifier/perturber” message. Perturbed variants must also be
API-valid and differ only at the named leaf, so admission failure cannot be
misreported as a killed classification mutation.

## Evidence ledger

| Check | Result | Evidence |
|---|---|---|
| Policy targeting absent HTTPRoute | **REPRODUCED** | Pinned upstream fixture reports `Accepted=True`, `Attached=False`, no policy output |
| Backend accepted without programming | **REPRODUCED** | Pinned controller sets `Accepted=True` after translation even when the referenced gateway iterator is empty |
| Stale condition risk | **REPRODUCED BY CONTRACT** | Upstream status conditions carry `ObservedGeneration`; the design's predicate does not compare it |
| Local token limit stops crossing request | **REFUTED** | Published 2.2 API says the crossing response is returned and only future requests are limited |
| Maximum admitted USD arithmetic fits emitted API | **REFUTED BY EXECUTION** | `rate=11,111,111,111,100`, `capacity=39,999,999,999,960,000`; both exceed `int32` |
| `gatewayReplicas=0` is total | **REFUTED** | Formula divides by zero; no schema/flag lower bound is specified |
| Mandatory-entry mutation is independently pinned | **SURVIVED BY SPECIFIED TEST SHAPE** | Registry-derived behavioral cases disappear when the production entry is deleted; no implementation exists yet to run a code mutation |
| Recursive-type mutation is killed | **INVALID BY SPECIFIED TEST SHAPE** | A walker without a recursion stack hangs/crashes; that is not an assertion failure |

No plume source mutation was performed: the compiler, registry, five condition
constants, gateway switch, chart dependency, NOTES, and leaf test do not exist
in the reviewed snapshot. Treating a non-compiling hypothetical implementation
as mutation evidence would violate AGENTS.md rule 3.

## Disagreement with the prior critiques

I disagree with `03-amendments-review.md` round 4's “0 blocker” and “substance
has converged” conclusion.

- Rounds 3–4 accepted “Backends included” as closing the apply hole. It does not:
  the actual policy API separates `Accepted` from `Attached`, and the required
  policy-first/route-last order guarantees the target is absent during the
  check. Backend `Accepted` also proves translation, not publication. This is
  not a request for another prose propagation pass; it changes the state machine.
- Round 3 required the 30-second poll to run on the worker to preserve ordering.
  I reject the forced choice between a blocking worker and a stale goroutine.
  A re-entrant, status-backed staged reconcile provides current-generation
  ordering without fleet starvation and is the Kubernetes-native mechanism.
- The prior arithmetic review fixed the 86,400/90,000 proof but stopped before
  mapping the result to the real `int32` CRD and before checking the gateway's
  response-time token semantics. The internal equation is now consistent; the
  claimed external guarantee is still false.
- The prior pricing review made `updated` chart-owned and called staleness
  advisory. It did not notice that both “tiers” consume the same estimate, or
  that the two writers share one scalar `data.models` field. The backstop cannot
  bound corruption or staleness in its own source.

I agree with the prior critiques where they retracted the asymmetric revision
gate and split `GovernanceSkipped` from `GatewayIncompatible`; those corrections
are sound. Agreement on those points is not evidence for the unrelated apply,
budget, or compiler-input contracts above.

## Verdict

**REVISE.** The current set can publish a route after proving only that its
mandatory policy parsed, not that it attached; it does not protect tightening
updates; and it promises budget ceilings the pinned gateway and CRD types cannot
provide. These are implementation-shaping defects, not owed-code placeholders.
Do not start design-03 implementation from A1–A9 until the seven blockers are
resolved in the design and the cross-design guarantees are made consistent.
