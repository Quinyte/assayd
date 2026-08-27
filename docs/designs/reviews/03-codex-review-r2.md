# Design 03 / design 02 amendment set — Codex re-review

**Verdict: REVISE — 8 BLOCKER, 12 MAJOR, 1 MINOR. Do not start design-03 implementation.**

Review snapshot: `fb181f0`, covering the changes after the first Codex review at
`bcc7a98` (`7617384..fb181f0`). I read the current authoritative bodies, not only
the amendment logs: design 03 §§3.1, 3.3, 3.3.1, 3.3.2, 3.4, 3.4.1, 3.4.2,
3.5, 5, 8.1 and A1–A11; design 02 §§3.1, 3.3, 4–6 and A15–A21; ADR-0020,
ADR-0027 and ADR-0028; architecture; the new v1.4.1 research note; and the
knock-on design/index rows.

The set moved substantially. The non-existent “2.2” baseline is gone; the
per-kind convergence tuple, current-generation checks, real `int32` emission
shape, hourly unit, no-token-burst result, and the admission that neither budget
tier is an exact USD ceiling are material corrections. They close the original
emitted-shape blocker and the decision-level half of the exact-USD blocker.
They do not close the implementation gate. Five original blockers remain open
or have been replaced by an invalid guarantee; all seven original majors remain
open; A20/A21 add two security blockers; and the new primary-source note itself
contains the next false bound.

## BLOCKER findings

### BLOCKER 1 — the update transaction still has no observable success barrier

**Files:** `docs/designs/03-policy-compiler.md:101-115,289-302`;
`docs/decisions/0028-policy-compiler-against-v1-4-1.md:7,12`;
`docs/research/agentgateway-v1.4.1-2026-08.md:192-229,602-613`.

The new tuple proves control-plane translation, which is better and correctly
named. It cannot prove the tightening transaction in line 115 reached the
proxy. At v1.4.1, a NACK is emitted only as a Kubernetes Event whose involved
object is the **Gateway**. The publisher carries a gateway name, type URL, error
text and timestamp; it carries no Policy/Route UID, no plume resource-set
digest, and no generation. Watching that uncorrelated negative stream cannot turn
absence of an Event into positive acknowledgement of one update. An old NACK
can be attributed to the new apply, and a new NACK can arrive after plume has
republished the route. The research note also explicitly leaves open whether a
NACK retains the old permissive config or fails closed.

**Concrete failure:** a tool filter is narrowed. Plume makes the route inert,
applies the policy, observes every current-generation Kubernetes condition, sees
no NACK yet, and republishes. The proxy then NACKs the policy and continues its
previous configuration. The newly denied tool remains callable while the Agent
can report a converged policy. The same failure applies to `auth: none → oauth`.

**Specific fix:** do not implement the tightening path until its enforcement
state is knowable. Obtain an upstream ACK/status contract correlated to Gateway
config version, or retain the route in a mechanically verified inert state and
run a generation-correlated synthetic request that must be denied before
publication. Specify the result of a NACK (old config retained versus fail
closed) from an executable v1.4.1 test. A Gateway Event may raise degradation;
it cannot be the success barrier.

### BLOCKER 2 — “create the route inert” is not a state-machine specification

**Files:** `docs/designs/03-policy-compiler.md:92-115,318-322`.

The design never defines the exact `HTTPRoute` representation of “inert”, the
predicate that observes it, or the transition that makes the policy attachable.
Those properties pull in opposite directions: a policy targeting a route with
no usable Gateway attachment remains `Attached=False`; a route attached to a
Gateway can satisfy the new policy tuple but is already reachable unless its
backend/weight/listener shape is proven inert. On update, merely writing zero
weight is not enough; the old proxy configuration can remain live until the
same unobservable dataplane convergence from BLOCKER 1 occurs.

**Concrete failure:** one implementation uses no `parentRefs`, so every policy
waits forever for a Gateway ancestor. Another uses a parent with a zero-weight
backend, observes route `Accepted=True`, then assumes the old live route has
been withdrawn even though the proxy has not ACKed that change. Both implement
the words in §3.3.2 and give opposite safety properties.

**Specific fix:** define separate persisted apply stages and exact manifests for
create and update: the inert route object, the expected Route/Policy ancestors,
the observation proving withdrawal, and the atomic publication edit. Pin each
stage against the v1.4.1 controller and proxy in e2e, including continuous
traffic during `none → oauth` and allowlist narrowing. If no one representation
can be both attachable and observably unreachable, change the mechanism rather
than leaving “inert” to the implementer.

### BLOCKER 3 — the replacement overshoot bound is false under concurrency

**Files:** `docs/designs/03-policy-compiler.md:261-274`;
`docs/decisions/0028-policy-compiler-against-v1-4-1.md:8`;
`docs/architecture.md:18`;
`docs/research/agentgateway-v1.4.1-2026-08.md:266-305`.

The published bound is
`gatewayReplicas × maxOutputTokens(model)`, justified as one crossing request
per replica. The pinned source does not reserve even one token on the Kubernetes
path. `check_llm_request` reads `available_refill()` and returns `Ok` whenever it
is greater than zero; the decrement happens only after a response. Therefore
every concurrent request on one replica can observe the same positive bucket
and pass. The bound is concurrency, not replica, limited. No concurrency limit
exists in `PolicyIntent` or the emitted policy. The formula also omits input
tokens even though upstream counts both, and no source for
`maxOutputTokens(model)` exists in the pricing catalog or intent.

**Concrete failure:** with one token left on one gateway replica, 1,000 parallel
requests pass the read-only availability check before any finishes. Each has a
large prompt and completion. Excess is approximately the sum of all 1,000
requests, not one maximum completion. The documented status/CLI bound can be
wrong by three orders of magnitude and is unbounded absent a request-size and
concurrency limit.

**Specific fix:** either add an enforced per-replica concurrency bound and
enforced input/output token maxima, then publish a formula including all
admitted requests and both token directions, or state that excess is unbounded
at v1.4.1. Add a concurrent traffic test; a serial “one crossing request” test
does not test the failure. Do not call the figure measured until a measurement
and its load envelope exist.

### BLOCKER 4 — egress enumeration is unfinished and conflates permission with selection

**Files:** `docs/designs/03-policy-compiler.md:29,47,72-74,127-147,182-194,250-252`;
`docs/designs/02-agent-crd-operator.md:49-53,108-126`;
`docs/decisions/0028-policy-compiler-against-v1-4-1.md:11`.

The known open issue remains blocking: no allowlist entry grammar, catalog
contents, catalog versioning, DNS/redirect rule, or custom-endpoint identity is
defined. A11 also adds a separate semantic error. It requires the Backend's
provider set to equal the allowlist. `llm.providers` is the requested provider
set; `egressAllowlist` is a ceiling on what may be reached. Equality turns every
permitted-but-unrequested provider into an active Backend destination and leaves
the relationship between the two fields undefined.

**Concrete failure:** a compliance pack permits providers `{A,B,C}` while one
Agent requests only `{A}`. The specified Backend enumerates `{A,B,C}`. A request
that selects B now has a reachable destination despite B never being in the
Agent's behavioural provider set. Conversely, an endpoint-style allowlist cannot
possibly equal a provider/model set. Both readings are reasonable under the
undefined grammar.

**Specific fix:** define typed entries and a versioned provider-to-effective-
endpoint catalog first. Emit exactly the resolved requested set
`providers ∪ active fallback`; prove it is a subset of the resolved permission
ceiling; never add a destination merely because it is allowed. Define host,
port, TLS/SNI, redirects, DNS changes, custom providers and `internal/*`; reject
every unresolved or ambiguous element. Pin the emitted Backend and a real denied
egress attempt in e2e.

### BLOCKER 5 — A20 still permits behavioural config to change under a gated revision

**Files:** `docs/designs/02-agent-crd-operator.md:97-167,225-230,358-366`.

A20's premise is false in two independent ways. Kubernetes immutability prevents
updating an existing object's data; it does not bind a name to contents. The
official contract says an immutable ConfigMap or Secret can be deleted and
recreated. The projection hashes only the name, so an immutable ConfigMap can be
recreated under the same referent with different data and consumed on the next
pod restart under the old revision. Second, Kubernetes does not know that Secret
keys are “credentials, not behaviour”. `envFrom.secretRef` and
`env[].valueFrom.secretKeyRef` can carry a system prompt, provider URL, feature
flag or command option just as easily as a credential. A mutable Secret is a
trivial version of the original bypass.

**Concrete failure:** an actor unable to edit the Agent deletes immutable
`agent-config`, recreates it as immutable with a new `SYSTEM_PROMPT`, and causes
a reschedule. Or the actor edits that key in an Opaque Secret referenced by
`envFrom`. In both cases the same spec projection, revision name and gate result
serve different behaviour.

**Specific fix:** bind behavioural env content, not just object names, into an
immutable revision snapshot/digest and watch referent UID/content changes. If
live credential rotation must remain ungated, expose narrowly typed credential
references or an explicit allowlist of credential env keys; do not exempt an
entire Kubernetes kind. Treat a missing optional referent as unresolved until a
specific safe rule is defined. Mutation-test ConfigMap delete/recreate and a
behavioural value stored in a Secret.

### BLOCKER 6 — A21's local-profile exception cannot be expressed by CRD CEL

**Files:** `docs/designs/02-agent-crd-operator.md:29-35,225-231,256-258,368-372`;
`docs/architecture.md:81-103`.

A21 says the schema's CEL rule requires a digest and that the local profile “may
relax it for `:dev` loops”. CRD validation has no Helm profile, namespace tier or
operator flag in its evaluation context. The same CRD schema applies cluster-
wide. A global CEL alternative accepting `:dev` would also accept that mutable
tag in production namespaces; omitting the alternative makes the promised local
relaxation nonexistent.

**Concrete failure:** an implementer encodes
`image.matches('@sha256:...$') || image.endsWith(':dev')` to satisfy both
sentences. A production Agent uses `registry/agent:dev`; retagging and reschedule
then replace gated code under an unchanged revision—the exact A21 bypass.

**Specific fix:** make digest pinning unconditional in the CRD with an exact
64-lowercase-hex grammar. The local CLI/build loop must resolve/import each build
to a digest and write that digest. If a mutable-tag exception is truly required,
design a separate namespace-scoped admission mechanism with an explicit dev
trust boundary; it cannot be described as profile-sensitive CEL.

### BLOCKER 7 — the 30-second in-worker poll remains a fleet denial of service

**Files:** `docs/designs/03-policy-compiler.md:90-99,306-322`.

The first review's BLOCKER 7 is unchanged. `MaxConcurrentReconciles` changes how
many malicious or permanently pending Agents are needed; it does not bound queue
delay. Each bad object occupies a worker for 30 seconds, requeues and repeats.
Kill, budget-hold and finalization reconciles share that queue.

**Concrete failure:** create more Agents with never-attaching policies than the
worker count. They continuously consume every worker. A kill switch or teardown
for an unrelated healthy Agent waits behind them for minutes or indefinitely as
the bad set grows.

**Specific fix:** persist resource-set digest, generation, stage and deadline in
status; apply once and return. Resource watches enqueue the Agent, and
`RequeueAfter` handles deadlines. Reject stale stage records on every reconcile.
Pin fairness with more permanently pending Agents than workers while a separate
kill/finalize transition must complete inside a stated bound.

### BLOCKER 8 — the authoritative body still instructs implementers to build superseded guarantees

**Files:** `docs/designs/03-policy-compiler.md:3-5,15,95-96,119-123,151-180,237-276,300-333`.

The body says it is authoritative, but A10/ADR-0028 did not reach it. It still:

- cites the superseded `agentgateway-2.2-2026-08.md` for the load-bearing Backend
  check (line 95);
- claims traffic flows only through fully policied paths and that fail-closed
  ordering removes all unauthenticated/unbudgeted windows (lines 96 and 302),
  despite the admitted NACK gap;
- asserts an unverified native evaluation order (line 154);
- calls the receipt path “exact”, says the gateway cuts early/never late, and
  says the backstop bounds stale/tampered pricing (lines 155-157, 241-250, 276);
- reintroduces token-bucket capacity and a per-second floor after A10 established
  `unit: Hours` and no token burst (lines 269 and 274);
- cites ADR-0020 as current and says R1 still awaits a run in places where A10
  says ADR-0028 and source-closed (lines 178, 322-333); and
- emits receipts through nonexistent `frontendPolicies` (line 180).

**Concrete failure:** two implementers can follow the authoritative mapping table
and A10 respectively and emit different rate-limit resources, conditions and
tests. The former can ship a `cuts early` status promise, poll the wrong API
contract, and mark NACK-vulnerable traffic fully protected. This is not harmless
history; these are the normative mapping, security and test sections.

**Specific fix:** perform a whole-document replacement sweep before implementation.
Remove every stale guarantee and old-note/ADR reference, delete the capacity and
per-second rules, replace the tracing path, and make every security statement
say exactly “control-plane convergence” until BLOCKER 1 is resolved. A grep gate
should reject the superseded research filename, `frontendPolicies`, `cuts early`,
`exact tier`, and current references to ADR-0020 in this design.

## MAJOR findings

### MAJOR 1 — `PolicyIntent` still cannot distinguish no demand from a missing producer

**Files:** `docs/designs/03-policy-compiler.md:37-60,72,121-125,284-288`.

An empty resolved `tools[]` still means both “the Agent requested no tool route”
and “the Agent requested a tool but its producer/resolution is unavailable”. The
pure compiler cannot implement the document's different results from identical
input. An Agent can therefore be Ready with a declared capability silently
absent. Add declared route demand and producer state to the input, or make every
unresolved requested binding an operator error before `Compile`. Test not
requested, requested/unresolved, and requested/missing mandatory concern as
three distinct states.

### MAJOR 2 — `ReplicaUnverified` still calls deliberate P1 absence a degradation

**Files:** `docs/designs/03-policy-compiler.md:62-80,293-298`.

Line 296 still says `ReplicaUnverified` “is the P1 state”, while lines 68 and 293
say P1 has `gateway.enabled=false`, no compiler and only
`GovernanceSkipped=GatewayDisabled`. No limiter exists whose divisor can be
unverified. Scope replica conditions to `gateway.enabled=true` and an actually
derived rate; add the enabled × observable Deployment × rate-emitted matrix.

### MAJOR 3 — two writers still own one scalar pricing field

**Files:** `docs/designs/03-policy-compiler.md:210-241`;
`docs/architecture.md:264-272`.

External and `internal/*` rows remain inside the single `data.models` YAML
scalar. Kubernetes ownership cannot split rows within one string. A Helm refresh
and model-operator upsert conflict or lose one another. Use separate chart- and
operator-owned objects with a deterministic merge/duplicate rule, or one
Kubernetes map key/object per price row. Do not rely on a read-modify-write race.

### MAJOR 4 — fallback lookup still uses a known non-injective key

**Files:** `docs/designs/03-policy-compiler.md:229-233,250-252`.

Structured `{provider, model}` still canonicalizes with `/`, so
`{azure, openai/gpt-4}` collides with `{azure/openai, gpt-4}`. Those tuples can
carry different prices and egress endpoints. Use a structured/nested key or an
injective encoding, or forbid `/` in provider names at admission. The exact
colliding pair must produce distinct lookups or a compile error.

### MAJOR 5 — the mandatory-entry test can still delete itself

**Files:** `docs/designs/03-policy-compiler.md:139-147,308-322`.

“For every registered class/entry” still permits tests generated from the
production registry. Delete `-auth` from the tool entry and its generated test
disappears; edit the rendered table and the golden also passes. Require a static
security-case catalog independent of the production registry, plus a mutation
harness that deletes each production entry while cases remain fixed and expects
a test assertion failure.

### MAJOR 6 — route-scoped failure still has no Agent aggregation contract

**Files:** `docs/designs/03-policy-compiler.md:121-147,284-298`;
`docs/designs/02-agent-crd-operator.md:225-253`;
`docs/designs/08-cli.md:75-88`.

One failed tool route may coexist with a serving A2A route, but the Agent has one
phase, one `Ready`, and one condition per type; the CLI makes any policy failure
terminal. The documents do not define which result wins or how multiple failing
routes share one condition. Add a state table keyed by serving-required versus
optional route, define deterministic aggregation of all resource paths, CLI
terminality and recovery, and clear the condition only when no scoped failure
remains.

### MAJOR 7 — the leaf-classification test still hangs on recursion

**Files:** `docs/designs/02-agent-crd-operator.md:143-161`.

The mandated walker still has “no type stack” and treats self-recursion as a
precondition. A future recursive type hangs or stack-overflows instead of failing
an assertion; that mutation is INVALID, not killed. Require a recursion stack
keyed by type/path and fail immediately with the cycle path and required explicit
classifier/perturber.

### MAJOR 8 — MCP allowlist emission is not total for empty or hostile tool names

**Files:** `docs/designs/03-policy-compiler.md:27,44,127-145,196-206`;
`docs/research/agentgateway-v1.4.1-2026-08.md:433-492`.

At v1.4.1 `AuthorizationPolicy.matchExpressions` has `MinItems=1` and each CEL
expression has a 16,384-byte maximum. The design does not define an empty
advertised set, CEL string escaping, expression count/size overflow, or tool
names containing quotes/backslashes. A zero-tool server therefore either emits
an API-invalid policy or is withheld despite being a valid empty server; an
incorrect workaround that omits the Allow rule restores upstream's default-allow
behavior. A hostile name can make the policy fail to parse.

Compile an empty allowed set to one valid always-false `Allow` expression (or
withhold the route with a named error), use a real CEL string encoder or a single
membership expression, and enforce upstream count/length limits. Golden-test
empty, 256/257 tools, maximum expression length, quotes, backslashes and Unicode.

### MAJOR 9 — the receipt mapping names a field and attachment scope that do not exist

**Files:** `docs/designs/03-policy-compiler.md:179-180`;
`docs/research/agentgateway-v1.4.1-2026-08.md:529-543`.

v1.4.1 has no `frontendPolicies`; tracing is
`AgentgatewayPolicy.spec.frontend.tracing`, and a frontend policy may target only
a Gateway, not a per-Agent route. Implementing line 180 either fails schema
validation or collapses per-Agent capture intent into one gateway-scoped policy
without an aggregation/ownership rule. Replace the field path and define one
gateway/listener-scoped tracing owner plus how per-Agent `captureLevel` is
represented, or remove per-Agent capture from `PolicyIntent`.

### MAJOR 10 — experimental virtual models are still specified as the stable shift mechanism

**Files:** `docs/designs/03-policy-compiler.md:173-178`;
`docs/architecture.md:264-272,490-492`;
`docs/decisions/0028-policy-compiler-against-v1-4-1.md:6`.

ADR-0028 says `AgentgatewayModel` is a fourth CRD, experimental and disabled by
default, and nothing may treat it as stable. The mapping table and canonical
architecture still mandate virtual-model weighted shifting as the only model
canary mechanism, without a feature gate, install contract or fallback. A
default v1.4.1 install lacks the CRD and cannot implement design 25's rollout.
Either explicitly enable/pin the experimental CRD with compatibility tests and
state the risk, or design a stable Backend/Route mechanism and amend design 25.

### MAJOR 11 — A20/A21 did not propagate through their own authoritative consumers

**Files:** `docs/designs/02-agent-crd-operator.md:3,209-230`;
`docs/architecture.md:81-103`;
`docs/designs/08-cli.md:59-62`.

Design 02's reconcile outline still says admission already enforced a signed
image; architecture's canonical Agent still uses a mutable tag and schema-invalid
budget shorthands; the CLI says design-02 admission requires a cosign signature.
A21 explicitly says no signature admission exists. An implementer following the
reconcile outline can skip the only check because it is “already enforced”, and
the canonical example now teaches a manifest A21 rejects. Update every consumer:
digest-only examples, honest signature language, and no precondition in reconcile
until the chart mechanism actually exists.

### MAJOR 12 — A21's signature-verification dependency is unchosen and unpriced

**Files:** `docs/designs/02-agent-crd-operator.md:231,256-258,368-372`;
`docs/designs/07-umbrella-chart-ci.md:41-55`;
`docs/architecture.md:36-45,183-187,415-421`.

Design 07 says it will ship “a Sigstore policy-controller or a Kyverno
`verifyImages` binding”. Those are not interchangeable YAML choices: each needs
a controller and lifecycle/availability contract, and shipping one adds core
pods to a budget already stated as approximately eight. If the controller is
assumed external, a binding alone does not verify anything. Choose one mechanism,
state whether plume installs or requires it, price its pods and failure mode, and
make install fail or loudly degrade when the verifier is absent. “Or” is not a
supply-chain enforcement design.

## MINOR finding

### MINOR 1 — amendment and ADR metadata is internally stale

**Files:** `docs/designs/03-policy-compiler.md:3-6,331-337`;
`docs/designs/02-agent-crd-operator.md:3,332-358`.

Design 03 says A1–A10 although A11 is present, omits ADR-0028 from its ADR list,
and §10 still names ADR-0020 as resulting. Design 02's status says A20/A21 are
owed and undrafted although the same file contains them. Correct the headers and
resulting-ADR section so the next reviewer can identify the live contract without
reading the full provenance log.

## First-review closure ledger

| First review finding | R2 result | Reason |
|---|---|---|
| BLOCKER 1 — `Accepted=True` barrier | **PARTIAL / OPEN** | Per-kind tuple fixes stale/unattached status, but no dataplane ACK exists and inert publication is undefined (R2 B1–B2) |
| BLOCKER 2 — serving-route update order | **SPECIFIED / BLOCKED** | Tightening order is written, but cannot be observed safely until NACK behavior and inert withdrawal are solved |
| BLOCKER 3 — “cuts early” impossible | **REPLACED / OPEN** | Claim withdrawn, but replacement overshoot bound is false under concurrency (R2 B3) |
| BLOCKER 4 — real CRD shape/overflow | **CLOSED** | `Hours`, no token burst, `int32` checks and replicas lower bound are now explicit |
| BLOCKER 5 — exact USD uses same estimate | **CLOSED AT DECISION LEVEL; BODY STALE** | ADR/architecture now admit estimated USD; authoritative design body still says exact/bounded (R2 B8) |
| BLOCKER 6 — egress language/mechanism | **OPEN** | Enumeration is the right primitive; grammar/catalog and provider-vs-permission semantics are unresolved (R2 B4) |
| BLOCKER 7 — worker-poll DoS | **OPEN** | No event-driven reconcile state machine was added (R2 B7) |
| MAJOR 1 — demand vs missing producer | **OPEN** | R2 M1 |
| MAJOR 2 — P1 `ReplicaUnverified` contradiction | **OPEN** | R2 M2 |
| MAJOR 3 — two writers in `data.models` | **OPEN** | R2 M3 |
| MAJOR 4 — slash-colliding fallback key | **OPEN** | R2 M4 |
| MAJOR 5 — registry-generated test deletes itself | **OPEN** | R2 M5 |
| MAJOR 6 — route failure aggregation | **OPEN** | R2 M6 |
| MAJOR 7 — recursive walker mutation is invalid | **OPEN** | R2 M7 |

## Evidence and mutation ledger

| Check / mutation | Result | Evidence |
|---|---|---|
| Stable dependency baseline | **REPRODUCED** | `refs/tags/v1.4.1` resolves to `163ea214…`; `v1.5.0-beta.1` exists but is not the pinned stable contract |
| One crossing request per replica | **REFUTED / REPRODUCED BY SOURCE** | v1.4.1 `check_llm_request` performs only `available_refill() > 0` before dispatch and no decrement; concurrent callers all pass |
| NACK can be correlated to one plume apply | **REFUTED / REPRODUCED BY SOURCE** | v1.4.1 `nack/publisher.go` emits a Warning Event on the Gateway with type URL/error text, no resource-set or generation identity |
| Empty MCP Allow expression set | **REPRODUCED** | v1.4.1 `AuthorizationPolicy.MatchExpressions` is required with `MinItems=1`; the design defines no empty-set mapping |
| Immutable referent name binds contents | **REFUTED / REPRODUCED BY CONTRACT** | Kubernetes documents that immutable ConfigMaps/Secrets cannot be edited but can be deleted and recreated |
| OTLP field `frontendPolicies` | **REFUTED / REPRODUCED BY SOURCE** | v1.4.1 exposes `spec.frontend.tracing`; frontend policies target Gateway, not a per-Agent route |
| Mandatory registry entry deleted while cases derive from registry | **SURVIVED BY SPECIFIED TEST SHAPE** | Deleting the entry deletes its generated case; editing the rendered row keeps the golden green |
| Recursive type added to leaf graph | **INVALID BY SPECIFIED TEST SHAPE** | A walker with no type stack hangs/overflows before an assertion; this is not KILLED |
| A20 immutability rule / A21 digest CEL implementation mutation | **UNAVAILABLE, NOT KILLED** | Repo grep confirms neither rule exists in Go/generated CRD yet; there is no compiling subject to mutate |
| Design-03 compiler mutation suite | **UNAVAILABLE, NOT KILLED** | `PolicyIntent`, emitter registry, apply state machine and compiler do not exist in code at this snapshot |

No repository source mutation was represented as evidence. There is no design-03
implementation to mutate, and treating a hypothetical non-compiling change as a
kill would violate AGENTS.md rule 3.

## Disagreement with prior reviews and the new research conclusion

I still disagree with `docs/designs/reviews/03-amendments-review.md` round 4's
“0 blocker” and “the substance has converged”. That conclusion measured internal
propagation against a false external baseline. A10 proves the distinction: all
four rounds missed that the cited release did not exist. R2 now has the correct
tag, but the same failure class recurs one layer deeper.

Most importantly, I disagree with the new research note's conclusion that the
gateway cuts late “by one request per replica per bucket”, and with ADR-0028's
“measured overshoot bound per request per replica”. The source excerpt printed in
that note refutes the conclusion immediately above it: the Kubernetes path reads
availability without consuming it. Nothing serializes requests at one per
replica. The formula also invents `maxOutputTokens(model)` without a producer and
omits input tokens. This is not a residual documentation issue; it is the budget
contract replacing the one the first review disproved.

I also disagree that A11 “discharged” the egress mechanism debt. Enumeration is
the correct upstream primitive, but a resolver with no language or catalog is not
an implementable control, and equality with the allowlist confuses a permission
ceiling with the Agent's requested provider set.

The correct judgement is therefore: **the set has moved substantially, but it
has not crossed the implementation threshold. REVISE.**
