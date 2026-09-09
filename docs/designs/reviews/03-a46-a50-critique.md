# Design 03 A46–A50 + design 04 A5 + design 26 A2 — independent critique

- **Scope**: the five uncommitted amendments answering Codex r8 BLOCKERs 10–13 and MAJOR 6.
- **Read cold** against designs 02, 04, 11, 16, 20, 23, 26, `docs/architecture.md`, `docs/requirements.md`, and the shipped `internal/controller/material.go` / `runnamespace.go`.
- **Verdict: REVISE — 6 BLOCKER, 10 MAJOR, 5 MINOR.**

Every finding below is a cross-design claim checked against the producing document, which is the rule this lineage was told to apply. Three of the six blockers are the *same* defect the amendments were written to remove, committed one field or one design over.

---

## BLOCKER findings

### BLOCKER 1 — A50 keeps a proof that rests on the other field A5 just declared absent

**Files:** `docs/designs/03-policy-compiler.md:416,418` · `docs/designs/04-receipt-tap.md:110`

A50 withdraws the replica-complete check because `hop.gatewayInstance` has no
producer, and then asserts: *"with `gatewayReplicas == 1` a positive canary is a
proof"* (03:418). The row directly above it — A40's postcondition, untouched —
says the canary result is *"attributed by the **full endpoint identity in the
receipt**"* (03:416). That is `hop.endpoint`. Design 04 A5, written in the same
change, says of **both** fields: *"no agentgateway OTLP attribute is named for
either field, no assayd-owned stamping mechanism is defined"* (04:110), and
*"Until then the fields are absent"* (04:114).

So A50 withdrew one of two claims that rested on the same missing producer and
promoted the other to a proof.

**Failure scenario:** a single-replica gateway. `ModelDrifted` fires, weights
shift `100/0 → 0/100`, the canary runs through the production route. The receipt
carries `hop.target` — which 04:109 explicitly says *"is retained for display and
is **not** an identity"* — and no `hop.endpoint`. The operator cannot attribute
the response to `<agent>-llm-fallback`, activation never satisfies its
postcondition, `LLMFallbackUnavailable` is raised on timeout and pages, and
`ModelDrifted` stays. On **every** cluster, not only the HA ones A50 carves out.
`llmFallbackActive` still cannot legitimately go true — the exact sentence A4 was
written to fix, and A5 restates verbatim.

**Fix:** A50 must withdraw the *whole* receipt-attributed postcondition, not the
replica half of it, until design 04 names a producer for `hop.endpoint`. State
what the fallback path may claim in the interim (candidly: nothing beyond
control-plane convergence at the new generation), and make the `gatewayReplicas
== 1` sentence conditional on 04's owed research pass landing.

### BLOCKER 2 — A49's positive witness of failure has no field to key on and no consumer contract

**Files:** `docs/designs/03-policy-compiler.md:392` · `docs/designs/04-receipt-tap.md:29-40,109` · `docs/designs/04-receipt-tap.md:63` (§3.4)

A49's one remaining claim is: *"a receipt attributing traffic to the withdrawn
route at or after the converged generation proves the withdrawal did **not**
take"* (03:392). Checked against the producing design, three things are missing.

1. **The envelope carries no route identity.** `receipt/v1` (04:29-40) has
   `tenant`, `ns`, `principal_chain`, `hop.{type,target,status,latency_ms,
   endpoint,gatewayInstance}`, `usage`, `capture`, `lineage`, `ts`. There is no
   route, no revision, no policy name. `hop.target` is disqualified by design 04
   itself (04:109). An Agent has several routes — serving, card, candidate
   header, per-tool, KG, LLM egress — and A38's one remaining withdrawal
   producer is design 06's identity path, which moves `-auth` on the **serving**
   route: `hop.type: a2a`, indistinguishable from a healthy Agent's traffic.
2. **The operator has no query surface.** Design 04 §3.4 gives the operator
   exactly one receipt consumer — the audit-index **per-agent daily spend
   aggregate** — and the row set is `(receipt_id, task_id, tenant, agent,
   principal, ts, status, usd_est, pricing)`. No route, no hop type, no
   generation. Fidelity-class consumers read the stream and the rule is *"never
   the index"*; the operator is not one of them. A49 therefore invents a
   consumer of design 04 that neither design 04 nor A5 knows it owes.
3. **`ts` is not a generation, and there is no drain window.** The receipt's
   `ts` is wall clock at the hop; the withdrawal's operand is a Kubernetes
   generation. Even granting a recorded convergence timestamp, a request that
   was legitimately admitted *before* convergence completes after it — design 02
   §3.3 drains supersession *"respecting `taskTimeout`"* (default 10m) for
   exactly this reason.

**Failure scenario, both directions.** False negative: an IdP audience is
narrowed, the weight-0 patch NACKs, the old permissive policy keeps admitting
the revoked audience — and every receipt it produces is byte-indistinguishable
from a healthy one, so `WithdrawalNotEnforced` never fires and A49's only
claimed guarantee is inert. False positive: the withdrawal *does* land, one
in-flight 9-minute task completes, its receipt carries a `ts` after the
converged generation, and assayd pages `WithdrawalNotEnforced` on a withdrawal
that worked.

**Fix:** A49 owes design 04 (a) a route or revision identity on the envelope and
(b) a fidelity-class query contract for "receipts on route R after time T", both
recorded as owed on design 04 the way A5 records the `hop.endpoint` gap; and it
owes a stated drain window of at least `taskTimeout` before a receipt counts as
evidence. Failing that, say plainly that `Withdraw` has **no** dataplane
observable at all — which is the honest reduction — rather than substituting a
witness that cannot be computed.

### BLOCKER 3 — A46 seals at a stage that is reached while the binding is still unresolved, and opens an indefinite ungated-widening window

**Files:** `docs/designs/03-policy-compiler.md:63,74,356,666` vs `:116,191,251`

A46's seal trigger is *"this revision first reaches `Served`"* (03:63). `Served`
is defined in exactly one place: 03:191, the **apply transaction's** steady
state over the *emitted* resource set. It is not a revision state — design 02's
phase vocabulary is `Pending|Held|Canary|Ready|Degraded|BudgetHeld|Killed`
(02:77) and contains no `Served`.

Two rules in this same design say the transaction reaches `Served` with a
declared binding unresolved:

- 03:116 — *"A route class **whose entire input set is unavailable because the
  producing design is not installed** is **not emitted at all**. No route, no
  policy, no error."*
- 03:251 — *"**The failure is route-scoped, not set-scoped.** An unresolvable
  tool withholds that tool's route and anything depending on it; **the serving
  route and the card route still compile and apply.**"*

So the desired set excludes the missing route, the transaction over what remains
runs `PreparingRoute → … → Publishing → Served`, and `sealed` flips. Nothing
gates the machine on `Ready`; nothing sets the route weight to 0 for
`CapabilityUnavailable`. A46's claim that the revision *"never reaches `Served`"*
(03:356) and §5's row *"the revision never publishes"* (03:666) are contradicted
by §3.3.1 two hundred lines above them.

**Failure scenario A — BLOCKER 10 survives verbatim.** Create an Agent with
`tools: [claims-system]` while design 11 is not installed. Tool route not
emitted; serving route emitted, converged, published, `Served`. `resolvedInputs`
seals with `ProducerAbsent`. Install design 11: the change is absent → resolved,
a widening, and A37's non-spec `Loosen` cell says **frozen** (03:344). The Agent
carries `CapabilityUnavailable` for life. This is the scenario the amendment
exists to close, unchanged.

**Failure scenario B — a fail-open window A46 creates.** `tools: [x, y]`, `y`'s
producer absent, `x` resolved and its route live. Under the *intended* reading
the record stays unsealed indefinitely — and 03:352 says the comparator's applied
operand for non-spec provenance **is** `resolvedInputs`. An unsealed record is
re-resolved every reconcile, so the comparator compares the current value against
itself: always `Equal`, never `Loosen`, never frozen. `x`'s tool server begins
advertising `wire_funds`; the live tool filter widens on a **serving** route with
no gate and no freeze. That is precisely the hole A37 was written to close,
reopened for any cluster that has not installed every producer — which is every
P1 cluster.

**Fix:** name the seal trigger as a predicate this design owns and can state
totally — e.g. *sealed when the revision's digest becomes
`status.activeRevisionDigest` **and** every declared binding is `Resolved`* — and
reconcile it with §3.3.1: either the transaction does not reach `Served` while a
required binding is unresolved (which contradicts the route-scoped rule and must
be argued), or the design says the two rules conflict and which one wins. Also
state what the comparator does while unsealed, because "re-resolve every
reconcile" is not a safe default on a route that is carrying traffic.

### BLOCKER 4 — A46 and A47 are jointly unimplementable: a provisional value in an immutable object

**Files:** `docs/designs/03-policy-compiler.md:64,74,75` · `docs/designs/02-agent-crd-operator.md:329-347` · `internal/controller/material.go:53,244`

A46 requires `resolvedInputs` to be *"re-resolved every reconcile"* while
unsealed (03:64). A47 stores it as *"an immutable object in the run namespace,
created and collected with that revision's env-source copies (design 02 §3.3)"*
(03:75). Design 02 §3.3's rules for that material are:

- `immutable: true`, *"so not even the operator can rewrite it in place"* (02:334);
- name derived: `<agent>-<revisionHash>-env-<n>` (02:331), and *"the operator does
  not recreate a published revision's material"*;
- an existing object whose content does not match the buffer is
  **`RevisionMaterialCollision` and the revision is not published** (02:337);
- **deletion authority is the name** — *"Nothing is ever deleted for wearing a
  label"* (02:338).

The shipped code makes this concrete. `MaterialName` is
`fmt.Sprintf("%s-%s-env-%d", agent, rev, index)` (`material.go:53`), and
`isRevisionMaterial` — *"the authority for every delete"* — requires that exact
name shape **and** a non-empty `assayd.dev/source` annotation
(`material.go:244-262`). A resolvedInputs object has no source object.

**Failure scenario:** R2 is minted; the object is created with tool set `{a}`.
Before `Served`, the tool server advertises `{a,b}`. The next reconcile
re-resolves. The object cannot be updated (immutable). Recreating it under the
same derived name trips design 02's content-mismatch rule → `RevisionMaterialCollision`
→ **the revision is never published**, so it never seals, so it re-resolves
forever. Give the object a content-addressed name instead and every unsealed
reconcile leaks one that `collectRevisionMaterial` will refuse to touch
(`material.go:332`, logging *"refusing to collect an object that is not this
agent's revision material"*) — an unbounded leak in a namespace design 02 says
Kubernetes GC does not reach (02:133).

**Fix:** state the object's **kind**, its **derived name**, and its **create
order relative to the seal** — design 02's six-step *"One read, one buffer, one
publish"* (02:344-349) is the precedent and the standard. The clean shape is:
the provisional value lives in status (it is small while producers are absent),
the object is created **once, at the seal**, from the buffer that was resolved
for the publishing reconcile, and `isRevisionMaterial` gains an explicit second
name shape. A crash between the seal write and the object create must also be
specified, or the record points at nothing on a revision that just published.

### BLOCKER 5 — A47 moves the compiler's operand into a namespace the compiler cannot read in hard mode

**Files:** `docs/designs/03-policy-compiler.md:75` · `docs/designs/26-tenant-cr.md:55,106,120`

Design 26 §4: the **policy compiler is host-side** and *"reads tenant CRs through
the vCluster kubeconfig. It holds no tenant-workload write access"* (26:55). §7:
*"the host-side policy compiler holds cross-tenant **read on tenant CRs** plus
host gateway write"* (26:106). Design 26 A1 adds *exactly one* tenant-side write
— a marker ConfigMap — and calls it *"the widening of §7's 'read only' that it
is"* (26:120), which is the standard this corpus holds such changes to. A1 also
places the hard-mode run namespace **inside the vCluster**.

`resolvedInputs` was a field of `status.revisions[]` — part of the tenant CR the
compiler already reads. A47 moves it into a ConfigMap in the run namespace. A
ConfigMap is not a CR, and the run namespace in hard mode is not on the host.

**Failure scenario:** a hard-mode tenant, any Agent, any non-spec producer drift.
The compiler reads `{name, digest}` out of status, has no grant to `get` the
object, and cannot recompile the frozen revision at all — so A37's freeze, the
mechanism the whole amendment chain exists to make possible, does not work in
hard mode. The alternative reading (grant the read silently) is an unnamed
widening of the compiler's tenant-side access, which A1 went out of its way not
to do for a single `create`.

**Fix:** keep the operand reachable through the CR surface, or amend design 26 A1
and ADR-0026 in the same change to grant the compiler a namespaced `get` on that
one object, name the widening, and say which side creates it in hard mode
(the agent-operator is inside the vCluster; the compiler is not).

### BLOCKER 6 — A50 is not propagated to the design that performs the activation

**Files:** `docs/designs/20-drift-controllers.md:21,79` · `docs/designs/03-policy-compiler.md:418`

Design 20's Model-drift row still reads: *"The witness is bounded (A6): one
receipt names one gateway replica via `hop.gatewayInstance` (design 04 A4), so
**activation requires a distinct instance per replica** or reports
`LLMFallbackUnavailable/PartiallyProgrammed`"* (20:21), and design 20 A6 (20:79)
carries the same requirement. A50 withdraws it. `git diff docs/` touches 03, 04
and 26 and not 20.

**Failure scenario:** the remediation *lives* in design 20. An implementer builds
it from design 20 and implements a check that has been withdrawn, over a field
design 04 A5 says does not exist. Design 20's own A2 and A4 exist because this
exact propagation failed twice already — A2 fixed the detector row and left the
baseline row carrying the forbidden key, and A4 records that as *"the propagation
failure this review set keeps finding"*.

**Fix:** amend design 20 row 21 and A6 in the same change, and mark design 20's
status line as carrying an uncritiqued amendment, as 04 and 26 now are.

---

## MAJOR findings

### MAJOR 1 — A48 leaves three live consumers of the field it deletes, two of them inside design 03

**Files:** `docs/designs/03-policy-compiler.md:337,443` · `docs/designs/23-app-layer.md:9,24` · `docs/architecture.md:166`

- 03:337 — A25's paragraph still enumerates the non-spec inputs as *"a tool
  server's advertised set (11), a resolved KG endpoint (01/13), **tenant consumer
  budgets (26)**, and `status.llmFallbackActive`"*.
- 03:443 — §3.4's concern-mapping table, which is what an implementer compiles
  from, still reads *"Expose visibility | listener class cluster / org / public
  (+ OAuth clients, **consumer budgets**)"*.
- 23:24 — design 23 §3: *"All routes: … rate limits per `consumerBudgets`"*, and
  23:9 makes those projection routes *"design-03 rows … added to 03 §3.4"*.
- `docs/architecture.md:166` advertises *"per-consumer budgets"* as part of the
  `expose` symmetry rule.

**Failure scenario:** an implementer reading §3.4 emits a consumer-budget policy
from a field §3.1 no longer carries, and §3.1's own totality rule (*"every field
maps or errors"*) cannot catch it because the field is gone from the input
rather than unmapped. Separately, design 23's ADR-0016 headline — *"per-user
receipts/budgets with zero app code"* — now has no compiler path, no producer,
and no owed note anywhere.

There is also a conflation worth naming. `expose[].consumerBudgets` is a
**per-consumer quota on one Agent's exposed API** — Agent-scoped by construction,
which is why architecture §Exposure puts it in the per-CR `expose` block. A48's
argument ("a tenant's shared limit cannot have N authoritative per-Agent
operands") refutes the *provenance* the old table asserted (design 26
`TenantPolicyIntent`), not the field's existence. Deleting the field deletes a
capability two documents advertise, on the strength of an argument about a
different capability.

**Fix:** strike 03:337 and 03:443 in the same change. Then either record the
owed item on design 23 §3 and `docs/architecture.md` §Exposure — the way A48
records one on design 26 — or state explicitly that a per-consumer quota on an
Agent's own expose block is a distinct mechanism that survives A48 and needs its
own producer and CRD field.

### MAJOR 2 — A49 leaves standing the sentence that made `Withdraw` legal

**File:** `docs/designs/03-policy-compiler.md:379,383` vs `:301-309,387`

03:379, untouched: *"`Withdraw` is therefore a real transaction with stages, and
it **works because a weight-0 route is no longer a serving route** — so applying
to it is not the forbidden operation."* The forbidden operation is §3.3.3's *"no
transaction tightens a serving route in place"* (03:303). A49 (03:387) says that
premise cannot be known: the weight-0 patch may have NACK'd and *"leaves the
route serving at its old weight"*. The stage list at 03:383 is unchanged and
still applies the tightened policy at the same point.

So `Withdraw` now knowingly performs the operation §3.3.3 forbids, on a route
that may be live, while the paragraph justifying it still asserts the opposite.
A49 also addresses only a NACK on the weight-0 patch; a NACK on the
`ApplyingPolicies` stage leaves the old permissive policy serving, and
`Publishing` then restores full weight to it.

**Fix:** rewrite or strike 03:379. State what `ApplyingPolicies` may do when the
route may still be live, and whether this is a deliberate exception to §3.3.3
(with the argument for why it is safer than not withdrawing at all) or a reason
to withdraw `Withdraw` entirely. Cover the NACK on the policy apply, not just on
the weight patch.

### MAJOR 3 — a new *reason* on an existing type cannot page differently

**File:** `docs/designs/03-policy-compiler.md:392,418` vs `:385`

A49: the reason *"escalates to `WithdrawalNotEnforced` **and it pages**"*
(03:392). A50: activation *"reports `LLMFallbackUnavailable` with reason
`PartiallyProgrammed` **and pages**"* (03:418). Twelve lines above the first,
this design states its own consumer contract: *"Design 10's alerts and design
08's `deploy` stream **key on the type**"* (03:385, repeated at 03:130).

`PolicyInputDrifted` already carries reason `RouteFailedClosed` on the same path,
so the escalation changes nothing an alert consumer can observe;
`LLMFallbackUnavailable` already pages. Both "and it pages" clauses claim an
escalation the alerting layer cannot express.

**Fix:** say the escalation is visible in the condition *message* only, or
introduce a distinct condition type — and if a type, add it to design 02
§3.1's closed vocabulary (02:88-101), which is where the vocabulary is closed
and where a test parses it out of the document in both directions.

### MAJOR 4 — `PartiallyProgrammed` becomes a permanent, unactionable page on every HA install

**File:** `docs/designs/03-policy-compiler.md:418` · `docs/designs/20-drift-controllers.md:34` · `docs/requirements.md:14`

Under A50, with `gatewayReplicas > 1` activation *always* reports
`LLMFallbackUnavailable/PartiallyProgrammed` and pages, and no evidence can ever
clear it — the proof is withdrawn, not deferred. Design 20's verification loop
then escalates every remediation: *"A condition that remediation can't clear
within `verifyTimeout` escalates with the full action trail"* (20:34). NFR-8
requires a degraded guarantee to surface as a condition; the corpus reads that
as *the condition names the human action*. There is none here — nothing an
operator does produces the missing evidence.

`LLMFallbackUnavailable` is also the wrong word: the fallback is available and
serving; what is missing is the proof.

**Failure scenario:** any production cluster with two gateway replicas. Every
model-drift remediation pages, forever, with no action that resolves it —
which trains operators to ignore the one condition that also means "the fallback
genuinely never converged".

**Fix:** either name the action ("scale the gateway to one replica to obtain a
proof, or accept the bound"), or make the multi-replica case an abnormal-true
informational condition that does **not** page, and give it a name that says
what is true — the remediation is applied and its coverage is unproven.

### MAJOR 5 — A50's impossibility claim is stronger than what it demonstrates

**File:** `docs/designs/03-policy-compiler.md:418,707`

A50 says the requirement is *"withdrawn as **unachievable**"* and *"the first
retracted for being unattributable **in principle** rather than in practice"*.
What is actually shown is narrower and correct: *ordinary canaries through a
Service* cannot cover the replica set, and stale instance identities inflate a
distinct-count.

Per-replica addressing is ordinary Kubernetes. The gateway's `EndpointSlice`
enumerates the current Ready Pods; the operator already observes
`Deployment/<release>-agentgateway` read-only (03:663); dialling each Ready Pod
directly and restarting the witness on membership change is exactly r8 BLOCKER
13's own suggested fix, and it *would* be a proof, because route programming is
per-Pod. r8 permitted the withdrawal ("or explicitly admit that the proof cannot
be made") — but the design should decline it on cost and scope, not declare a
door closed that is open.

**Fix:** replace "unachievable" and "in principle" with the real reason — this
design declines to build per-Pod canary addressing (an EndpointSlice read, N
direct dials, a membership-change restart, and the NetworkPolicy to allow it) —
and say what that costs, so a later design is not told the mechanism is
impossible.

### MAJOR 6 — losing the run namespace now destroys a serving revision unrecoverably

**Files:** `docs/designs/03-policy-compiler.md:665` · `docs/designs/02-agent-crd-operator.md:177,339`

03:665 sends a missing or digest-mismatched `resolvedInputs` object to design
02's `RevisionMaterialUnavailable`, which (02:339) *"sends the revision's route to
**weight 0**"*. Design 02's recovery for a cluster-rights namespace deletion
(02:177, check 11) re-mints material *"from the current sources **if they still
hash to the desired digest**"*. That works for env copies, whose sources usually
have not moved. It can never work here: the operand's entire purpose is to
survive a producer that *has* moved, so a re-resolution that matched the recorded
digest would be the case where the freeze was unnecessary.

**Failure scenario:** `kubectl delete ns assayd-run-team-a` — or r8 MAJOR 3's
still-open last-Agent GC race — and every Agent in `team-a` goes to weight 0
permanently, with no stated recovery. Before A47, `status.revisions[]` lived on
the Agent CR, survived namespace loss entirely, and was captured by any CR
backup.

**Fix:** state the recovery. Either the record keeps the bytes as a fallback when
they fit under a stated size, or the design says the revision must be re-gated by
a human and names the condition that says so in those words. Also distinguish
the recoverable sub-case: if the object is missing but the *current* resolution
hashes to the recorded digest, it can be recreated, and today's row refuses it.

### MAJOR 7 — external Agents hold no run namespace, so A47 gives them nowhere to put the object

**Files:** `docs/designs/03-policy-compiler.md:57-60,75` · `docs/designs/02-agent-crd-operator.md:191,816`

Design 02: *"An **external** Agent never uses the run namespace and is counted by
neither list"* (02:191), and the A61 code-review record confirms *"external Agents
do not hold a run namespace"* (02:816). An external Agent still has
`identity` — a non-spec producer output, listed in `resolvedInputs` (03:57-60),
and the mandatory input for the `-auth` concern on its expose routes.

**Failure scenario:** an Agent with `external.endpoint`, `expose.a2a` and design
06 installed mints a revision. The record's `{name, digest}` points into a
namespace that either was never created for this Agent or is torn down by a
handler that counts neither the Agent nor its objects.

**Fix:** state where an external Agent's revision material lives, or exclude
external Agents from the by-reference form and keep their (necessarily small)
`resolvedInputs` in status, with the size argument stated.

### MAJOR 8 — the §8 row asserts three tests that cannot fail and one that cannot run at that layer

**File:** `docs/designs/03-policy-compiler.md:689` · `:692`

- *"a receipt attributing traffic to a withdrawn route at or after its converged
  generation must escalate to `WithdrawalNotEnforced`"* — as an **envtest**, the
  test supplies the receipt. It pins the operator's reaction to an input
  production cannot produce (BLOCKER 2) and passes under every attribution story,
  including none. §8.1 (03:692) also defers *"everything traffic-level"* and the
  *"synthetic-receipt backstop drive"*, and states *"**Until then design 03 has no
  e2e coverage at all**"*.
- *"a large resolved catalogue compiles, and the record stays small"* — true by
  construction the moment the field is a reference. It cannot fail.
- *"a revision whose declared binding is unresolved never reaches `Served`"* —
  asserts behaviour §3.3.1 contradicts (BLOCKER 3). Written as stated it either
  cannot be made to pass, or it pins the wrong rule.

Only *"seal at mint instead of at `Served`, and the recovery case wedges"* is a
mutation that can kill.

**Fix:** keep the seal mutation. Move the withdrawal-escalation case to the
deferred list with its prerequisite named (a route identity on the envelope).
Replace the size case with the one that can fail — a resolved catalogue **over**
the object bound must produce the compile error naming the producer and the
measured size — and delete the third until §3.3.1 and A46 agree.

### MAJOR 9 — the record's digest arithmetic is stale, and `sealed`'s integrity is unstated

**File:** `docs/designs/03-policy-compiler.md:65,74`

`recordDigest` is documented as *"SHA-256 over the **six** fields above,
schemaVersion included"* (03:65). The record now has seven fields above it:
`schemaVersion`, `hash`, `behaviourProjection`, `originalBudget`,
`resolvedInputs`, `sealed`, `appliedDigest`. A46 added one and did not adjust the
count.

This is not a typo. If `sealed` is outside the digest, a flip is undetectable and
the one-way property has no integrity check at all — the corruption row (03:76)
would not catch it. If it is inside, the Immutability row's *"`appliedDigest` is
the only field that may still change"* (03:74) is wrong for both `sealed` (once)
and `recordDigest` (every time).

**Fix:** say seven, say explicitly that `sealed` is covered by `recordDigest`,
and correct the Immutability row to name the fields that may still change after
the seal.

### MAJOR 10 — design 04 A5 locates the `ns` consequence in the wrong consumer, and walks past a live non-injectivity

**File:** `docs/designs/04-receipt-tap.md:116` vs `:63` (§3.4)

A5's diagnosis of the `ns` producer gap is right and important. Its consequence
is misattributed: *"the per-agent backstop looks up a **stream** that is always
empty and fails open"*. The backstop does not read the stream — design 04 §3.4
routes it through the **audit-index per-agent daily spend aggregate**, and
fidelity consumers are the ones told *"never the index"*.

Checking that aggregate's row set closes a gap A5 misses entirely:
`(receipt_id, task_id, tenant, agent, principal, ts, status, usd_est, pricing)` —
**there is no `ns` column at all**. So per-agent spend is keyed on an agent
*name*, which is unique only within a namespace. Two Agents named `pa-reviewer`
in `claims` and `billing` already share one spend row, one budget window and one
`BudgetExhausted` decision — today, independently of where `ns` comes from. That
is the same non-injective-key defect this lineage has now removed from three
other places, sitting in the backstop that design 03 calls its exact tier.

**Fix:** correct the mechanism in A5 (the index, not the stream), and add the
index key to what A5 owes: rows keyed `(tenant, ns, agent)` with `ns` in the row
set, plus the test that two same-named Agents in different namespaces keep
independent budgets. A5's "fails open" conclusion is right and its route to it is
not.

---

## MINOR findings

### MINOR 1 — the one-MiB bound is still assayd's, and no producer was told

**File:** `docs/designs/03-policy-compiler.md:75` · `docs/designs/11-connector-crd.md:30,49`

A47 calls one MiB *"a bound a producer **can** be held to"*. Design 11 states no
cardinality or size bound on a tool catalogue anywhere, and no amendment is owed
to it in this change — so the new bound is exactly as un-negotiated as the 8 KiB
one MAJOR 6 objected to, just 128× larger. The object's **kind** is also never
named, so *"the object's own"* limit has no referent (a ConfigMap's is 1 MiB
across `data`; etcd's is 1.5 MiB; a Secret's differs in what counts).

**Fix:** name the kind. Then either owe design 11 a stated catalogue bound, or
say plainly that this is assayd's storage limit, that no producer contract backs
it, and that the compile error names assayd's limit rather than the producer's
breach of a contract it never signed.

### MINOR 2 — the seal's justification claims a gate that never sees the value

**File:** `docs/designs/03-policy-compiler.md:356,711`

A46 twice says the freeze protects *"a value a **gated, serving** revision was
evaluated with"*. Under the mechanism the seal fires at the apply machine's
`Served`, which for a candidate is the weight-0 header route created **before**
design 16's EvalRun (design 02 §3.3 steps 2–3); and design 16 gates agent
behaviour and never sees the compiled tool filter. "Evaluated with" overstates it
in both directions.

**Fix:** say the seal captures the material the revision **published with**. That
is true, is checkable, and is still the right operand for A37.

### MINOR 3 — `status.revisions[]` is still absent from the owning design's schema

**Files:** `docs/designs/02-agent-crd-operator.md:74-101` · `docs/designs/03-policy-compiler.md:48`

Design 03 says the record *"lives in `status.revisions[]` on the Agent"`; design
02 §3.1's status block — the closed contract, pinned by a test — does not declare
it. Pre-existing, but A46 adds a field to that record and A47 changes another,
and neither pass reaches the design that owns the schema.

**Fix:** add `revisions[]` to design 02 §3.1 in the same pass, with the shape
design 03 §3.1 defines.

### MINOR 4 — §8's envtest cell is one run-on with three amendments interleaved

**File:** `docs/designs/03-policy-compiler.md:689`

The cell opens with A49's case, interrupts itself with *"— the **reference** rule
of A47 —"*, resumes A47, interrupts again with *"— and the **seal** rule of
A46 —"*, then rejoins the pre-existing list mid-sentence. A newcomer cannot tell
which clause belongs to which rule, or which of them is the mutation. `write-spec`
treats this as a defect, not a style preference.

**Fix:** one row (or one short list item) per rule, with the required mutation on
its own line.

### MINOR 5 — design 26 A2 leaves §6 and `QuotasEnforced` asserting the mechanism it says does not exist

**File:** `docs/designs/26-tenant-cr.md:31,77,144`

A2 correctly names §6's *"Quota exceeded"* row as describing a mechanism that
does not exist (26:144), and then leaves the row (26:77) reading *"Gateway
partition enforces (429 with tenant context); `QuotasEnforced` reflects"*, with
`QuotasEnforced` still in §3's condition list (26:31) and no statement of what it
may claim for traffic quotas. Design 26 A1 set the better precedent: it changed
`QuotasEnforced`'s **message** in the body when it changed what the condition
could truthfully say.

**Fix:** mark the row and the condition in the body — *traffic quotas are not
enforced; `QuotasEnforced` speaks only to the compute dimensions* — rather than
leaving the contradiction for a reader to resolve by amendment date.

---

## Where the work is right

Said after genuinely trying to break each of these, not as a cushion.

- **A46's diagnosis is exactly right, and the shape of the answer is right.** A
  record that freezes at mint freezes states that are legitimate at mint;
  provisional-until-published is the correct structure, and the residual it names
  (A32's optional column, owed to designs 26 and 27) is the honest one. Only the
  trigger and its interaction with §3.3.1 are wrong.
- **A47 takes the second of MAJOR 6's two offered fixes and takes it cleanly.**
  Moving the unbounded field out of status and keeping a digest is right; keeping
  `behaviourProjection`'s 8 KiB cap because assayd owns `AgentSpec`'s size is
  exactly the right line to draw. The claim that design 02 A42's machinery
  *"already ships and tests"* is **true at HEAD** — I checked, and `836052d`
  landed it with `material_test.go` and `runnamespace_test.go` — so r8 BLOCKER
  4's "design-only" is stale, not this amendment's error.
- **A48's cross-check of design 26 is accurate on every point.** Design 26 genuinely
  never uses the word `consumerBudgets`; §3's Traffic row genuinely is one clause
  (26:43); D1 genuinely counts it as one of two new mechanisms (26:10). The
  argument that one shared limit cannot have N sealed per-Agent operands is
  correct and well made.
- **A49's identification of the contradiction is correct and the reduction is the
  right instinct.** `Withdraw` did claim enforcement from the exact signal §3.3.2
  measured lying, and saying so is worth more than the witness it substituted.
- **A50's first ground is verified.** I read design 04 end to end: §3.1's only
  attribute-stamping clause is for `hop.type`, and there is no producer for
  either A4 field. The withdrawal is warranted; only its stated reason and its
  surviving half are wrong.
- **Design 04 A5 is accurate about design 04's own state** on every claim I
  checked — the single stamping clause, §4's transform with no Agent lookup, `ns`
  as a subject segment, and the design 02 A42 consequence for namespace
  attribution. It is the strongest of the five documents.
- **Design 26 A2 is accurate about design 26's own state**, and the owed table
  (intent, record, membership snapshot, transaction, backstop tier) is the right
  list.

---

**03 A46–A50 / 04 A5 / 26 A2: REVISE — 6 BLOCKER, 10 MAJOR, 5 MINOR.**

---

# Round 2

- **Scope**: the same uncommitted amendment set after the round-1 fixes — `git diff docs/` at 322 lines across `architecture.md` and designs 02, 03, 04, 20, 23, 26.
- **Method**: closure decided against the repository and the producing designs, never against the amendment prose. Design 02's condition-closure test was mutated to confirm it can fail (`PolicyInputDrifted` removed from 02:98 ⇒ `TestConditionVocabularyIsClosed` fails; restored from a copy, `git diff --stat` back to 10 insertions, suite green).
- **Verdict: REVISE — 5 BLOCKER, 8 MAJOR, 8 MINOR. It has not converged.**

Round 1's own summary was that three of six blockers were the same defect committed one field or one design over. **That shape repeated.** BLOCKER 3's replacement predicate reintroduces the ungated widening one *tense* over — the trigger is written as a standing conjunction rather than an event. BLOCKER 4/5's two-form storage exists in the amendment log and in design 02's status comment and **nowhere in design 03's authoritative body**, and its key is size, not the two modes it was written for. BLOCKER 6's propagation reached design 20's amendment log and not design 20's row. BLOCKER 1's withdrawal reached design 03 and not design 04's own statement of it.

## Job 1 — closure of the 21 findings

| # | Finding | Status |
|---|---|---|
| B1 | A50 keeps a proof resting on the absent field | **PARTIAL** — closed in 03, reopened in 04 (new MAJOR 1) |
| B2 | A49's witness has no field, no consumer, no drain | **CLOSED** |
| B3 | A46 seals at a stage reached while unresolved | **PARTIAL** — see new BLOCKER 1, BLOCKER 4 |
| B4 | provisional value in an immutable object | **PARTIAL** — the provisional write is gone; kind, name and the design-02 side are not (new BLOCKER 3) |
| B5 | operand unreachable by the host-side compiler in hard mode | **OPEN** (new BLOCKER 3) |
| B6 | A50 not propagated to design 20 | **PARTIAL** (new BLOCKER 5) |
| M1 | live consumers of `consumerBudgets` | **OPEN** — 03:453 survives, and A48 claims it does not (new MAJOR 2) |
| M2 | the sentence that made `Withdraw` legal | **CLOSED** |
| M3 | a new reason cannot page differently | **CLOSED** |
| M4 | `PartiallyProgrammed` a permanent unactionable page | **PARTIAL** — action named; the misnomer and the clearing path are not (new MINOR 6) |
| M5 | A50's impossibility claim stronger than shown | **PARTIAL** — the body now contradicts itself (new MAJOR 6) |
| M6 | run-namespace loss destroys a serving revision | **PARTIAL** (new MAJOR 4) |
| M7 | external Agents have no run namespace | **PARTIAL** — the carve-out is size-keyed (new BLOCKER 3) |
| M8 | §8 asserts tests that cannot fail | **CLOSED** |
| M9 | `recordDigest` arithmetic stale, `sealed` integrity unstated | **OPEN** (new MAJOR 3) |
| M10 | design 04 A5 mislocates the `ns` consequence | **CLOSED** — the index key `(tenant, ns, agent)` and its test are now owed |
| m1 | one-MiB bound un-negotiated, kind unnamed | **PARTIAL** (new MINOR 3) |
| m2 | the seal's justification claims a gate | **PARTIAL** — fixed in prose, reintroduced in the new table (new MINOR 1) |
| m3 | `status.revisions[]` absent from design 02 | **CLOSED** — and it brought two new defects (BLOCKER 2, MAJOR 5) |
| m4 | §8's envtest cell is a run-on | **OPEN, worse** (new MINOR 4) |
| m5 | design 26 §6 / `QuotasEnforced` | **CLOSED** for §6; §3's Traffic row and D1 untouched (new MINOR 7) |

Six closed, ten partial, five open.

## BLOCKER findings

### BLOCKER 1 — the seal predicate is a standing conjunction, so the absent producer's arrival seals an ungated widening onto a live route

**File:** `docs/designs/03-policy-compiler.md:76` vs `:366`, `:368`, `:676`

03:76: *"A binding seals when **both** hold: the revision's digest is `status.activeRevisionDigest` … **and** that binding's state is `Resolved`."* There is no temporal quantifier on either conjunct. A binding's `state` is recomputed every reconcile (§3.1's binding table). So for an active revision whose tool binding is `ProducerAbsent` today, the moment design 11 is installed the conjunction becomes true and **the binding seals** — with the catalogue that just arrived, on a route that is already carrying production traffic, with no gate.

Three statements in this document give three different answers to that event:

- **03:76** (the predicate, and the only *normative* statement of the mechanism): seal.
- **03:366** (the three-state table): *"the producer arriving later is an ungated widening exactly as A37 describes"* — withhold, `PolicyInputDrifted`.
- **03:676** (§5's row): *"installing the producer resolves the binding on the next reconcile and the revision publishes with it"* — apply.

03:368's residual paragraph asserts the second (*"can never acquire it"*), which is only true under the reading the predicate does not state. An implementer compiles the predicate.

**Failure scenario — BLOCKER 10 survives, in the opposite direction.** `tools: [claims-system]`, design 11 absent. Revision R1 mints, the serving and card routes compile and apply (§3.3.1, route-scoped), R1 becomes `activeRevisionDigest`. Two months later an operator installs design 11 and the tool server advertises `{read_claim, wire_funds}`. The predicate's second conjunct flips; the record seals `{read_claim, wire_funds}`; the tool route is emitted and the filter is applied to a live Agent. The gate never ran, no spec moved, and `sealedBindings` now records the widened set as *"what the gate approved"*. That is A37's exact hole, arriving through the amendment written to close it.

**Fix:** state the trigger as an **event with a fixed evaluation instant** and say which: *a binding seals at the reconcile in which the revision's digest first becomes `status.activeRevisionDigest`, and only if its state is `Resolved` **at that reconcile**; a binding that is not `Resolved` then is never sealed for that revision.* Then reconcile 03:676 with 03:366 — one of them is wrong and the design must say which. Add the mutation to §8: install the producer after activation and assert the record's `sealedBindings` does **not** grow.

### BLOCKER 2 — `identity` cannot ever seal, and it is the one producer A38 still fails closed on

**Files:** `docs/designs/03-policy-compiler.md:26,31,60,76,366,384`

03:60 says the sealed object holds *"tools[].toolAllowlist, knowledge[].endpoint, **identity**"*. 03:76 says a binding seals only when *"that binding's state is `Resolved`"*. But `identity` is **not a `Binding<T>`**: 03:26 declares it as `identity: {spiffeID | oauthClientID}`, and 03:31's `Binding<T>` comment covers `tools`, `knowledge` and `llm` only. §3.1's binding-state table (03:106-107) names producers 11, 01 and 13 and never design 06. `identity` therefore has no `state`, can never satisfy the second conjunct, and can never enter `sealedBindings`.

Per the three-state table, *"a binding not in that list has no applied operand at all"* (03:76). So the `-auth` comparator — whose ordering (03:322) is *"on the effective admitted-principal policy … `{mode, issuer, audiences[], claim predicates, principal identity, key/JWKS source identity}`"* — permanently has no applied operand for its non-spec half.

**Failure scenario.** Design 06 is installed and the Agent serves. The IdP rotates its JWKS URL, or an audience is added. The comparator has nothing to compare against ⇒ §3.3.3's *"a comparison it cannot make … classify as `Tighten`"* ⇒ non-spec × `Tighten` ⇒ A38's per-producer table, whose design 06 row is **"fail the affected route closed"** (03:384) ⇒ `Withdraw` on the serving route. Every identity input change — including a benign JWKS rotation — withdraws the Agent's serving route, and A49 has just established that the withdrawal cannot be proved to have landed. A46 destroyed the operand for the *only* producer A38 still fails closed on, which is the one path A49 explicitly left as the remaining fail-closed case.

**Fix:** either make `identity` a `Binding<T>` in 03:26 with design 06 added to §3.1's binding-state table (which also gives `CapabilityUnavailable` a producer for the identity case it already implies), or state explicitly that `identity`'s sealed value is captured by a different rule — and name that rule and its trigger. Do not leave a field listed as sealed-object content that the seal predicate cannot admit.

### BLOCKER 3 — the two-form storage is not in design 03, and its key is size where the problem is mode

**Files:** `docs/designs/03-policy-compiler.md:57,77,722` · `docs/designs/02-agent-crd-operator.md:92` · `docs/designs/26-tenant-cr.md:55,106` · `internal/controller/material.go:53,244-262`

The fix for BLOCKER 5 and MAJOR 7 is two-form storage. It appears in exactly two places: design 02's status **comment** (02:92, *"inline below 4 KiB, by reference above it"*) and design 03's **amendment log** (03:722). Design 03's authoritative body says the opposite, twice and unconditionally:

- 03:57 — *"a **REFERENCE**, not the bytes"*;
- 03:77 — *"It is therefore stored **by reference**: an immutable object in the run namespace … with only `{name, digest}` in status."*

03:3 and §11's own heading say *"the body is authoritative"* and *"Amendments … for provenance only"*. So by this design's own rule the two-form storage does not exist, and an implementer building §3.1 builds the reference-only form — the one A47 itself says is unreachable in two real cases.

Worse, the rule that *is* written is keyed on **size**, and both problems are keyed on **mode**:

- **Hard mode.** Design 26 §4 (26:55): the policy compiler is *host-side*, *"reads tenant CRs through the vCluster kubeconfig"*, *"holds no tenant-workload write access"*. §7 (26:106) grants cross-tenant read on **tenant CRs**. Design 26 A1 grants exactly one tenant-side write and calls it *"the widening of §7's 'read only' that it is"*. No `get` on ConfigMaps in the vCluster run namespace is granted anywhere. A hard-mode Agent whose resolved catalogue is 5 KiB takes the reference form and the compiler cannot read its own operand — A37's freeze does not work in hard mode. The 4 KiB threshold does not exempt it.
- **External Agents.** 02:200 — *"An **external** Agent never uses the run namespace and is counted by neither list"*; 02:826 — *"external Agents do not hold a run namespace"*. `spec.tools[]` is a sibling of `external` in the CRD (02:52), not excluded by it, so an external Agent may declare a large catalogue. Above 4 KiB the record points into a namespace that does not exist. No condition is named for it (NFR-8).

And the inline form's **shape** is defined nowhere. 03:57 declares `resolvedInputs: {name, digest}?`; 02:92 shows the same `{name: …, digest: …}` and annotates it with a threshold. Neither says what the field looks like when it carries bytes.

Separately, the object's design-02 half was never written. 03:77 claims it is *"created and collected with that revision's env-source copies (design 02 §3.3)"*. Design 02 §3.3's material rules are entirely about env copies: the name is `<agent>-<revisionHash>-env-<n>` with *"`n` the source's index"* (02:340), and *"Deletion authority: the name"* (02:347) — *"the operator deletes only objects whose **name** has the derived … shape … corroborated by the `assayd.dev/agent-uid` label and the source annotation"*. The shipped code is the same: `isRevisionMaterial` (`material.go:244-262`) requires the `env-<n>` name shape **and** a non-empty `assayd.dev/source` annotation. A `resolvedInputs` object has no source object. It is therefore never collected — an unbounded leak in a namespace 02:135 says Kubernetes GC does not reach — and §5's row sending it to design 02's `RevisionMaterialUnavailable` (03:675) names a condition design 02 raises only for objects it knows about. Design 02 was edited in this very change and its material rules were not extended.

**Fix:** put the two-form rule in §3.1 as a rule, not a comment: the schema shape of both forms, the threshold, and — separately from the threshold — *"an external Agent and a hard-mode tenant always use the inline form, and a value that exceeds the inline bound in those cases is `PolicyCompileFailed` naming the mode"*, or grant the compiler the namespaced `get` and amend design 26 A1 and ADR-0026 in the same change as A1 did for its one write. Then give design 02 §3.3 the object's kind, its derived name, its place in *"One read, one buffer, one publish"*, and a second name shape for `isRevisionMaterial` — or the row at 03:77 is a claim on a design that does not carry it.

### BLOCKER 4 — §5's A46 row contradicts §3.3.3's own three-state table and §3.3.1

**File:** `docs/designs/03-policy-compiler.md:676` vs `:251,358,366,368`

03:676: *"A declared binding's producer is absent, so **the revision never publishes** (A46) | `CapabilityUnavailable` + `Ready=False`, `phase: Pending`."*

This is the sentence round 1's BLOCKER 3 identified at 03:666 and asked to reconcile with §3.3.1. It was not reconciled; it was carried into a new row. §3.3.1:251 still says *"**The failure is route-scoped, not set-scoped** … the serving route and the card route still compile and apply"*, and 03:358 — added in this round — says so in the amendment's own words: *"a transaction reaches `Served` with a declared binding unresolved."* 03:366 then devotes a whole table row to *"**Unsealed on an active revision** — the producer was absent when it went active"*, a state 03:676 says is unreachable.

It is reachable, and nothing prevents it. Design 02's rollout (02:243-244) is *"Candidate registers, gets identity, passes readiness — gateway route weight 0 … Pass → weights shift; `activeRevision` flips"*. No step consults `Ready`, `CapabilityUnavailable` or the compiler's conditions, and a **first** revision of a fresh Agent has no candidate machinery at all. `Ready=False` does not stop the digest being written.

The two also disagree about the outcome: 03:676 says installing the producer *"resolves the binding … and the revision publishes with it"*; 03:368 says *"A revision that went active without a capability its Agent declared **can never acquire it**"*. Both cannot hold for the same reachable state.

**Fix:** rewrite 03:676 to the state that is actually reachable — *the serving and card routes publish, the absent producer's route is not emitted, the record's binding stays unsealed, `CapabilityUnavailable` + `Ready=False`, `phase: Pending` on create or `Degraded` if it was serving (§3.3.1's aggregation gives both), and 03:368's residual governs recovery*. If the intent is instead that such a revision must not become `activeRevisionDigest`, that is a rule design 02's promotion path does not have and this change must add it there.

### BLOCKER 5 — design 20's remediation row and A6 still carry the requirement A7 withdraws

**Files:** `docs/designs/20-drift-controllers.md:21,79` vs `:81`

Round 1's fix was: *"amend design 20 row 21 and A6 in the same change."* A7 was appended (20:81) and the status line flagged. **20:21 is unchanged** and still reads: *"The witness is bounded (A6): one receipt names one gateway replica via `hop.gatewayInstance` (design 04 A4), so **activation requires a distinct instance per replica** or reports `LLMFallbackUnavailable/PartiallyProgrammed`."* The same row still specifies the postcondition as *"attributed by full endpoint identity in the receipt. `llmFallbackActive` reports remediation only after that positive evidence"* — which A7, four lines of the same file later, says is impossible on any cluster. 20:79's A6 carries the withdrawn requirement verbatim.

This design's own **A4** (20:80) records this exact shape as *"the propagation failure this review set keeps finding"*, and A2 and A4 both **edited the body rows** rather than only appending. A7 did not.

**Failure scenario:** the remediation is built from design 20. An implementer reads the Model-drift row — the row that specifies the whole path — and implements a per-replica distinct-instance check over `hop.gatewayInstance`, a field design 04 A5 says has no producer. The check never satisfies, `LLMFallbackUnavailable/PartiallyProgrammed` fires on every activation, and design 20 §4's verification loop escalates it forever.

**Fix:** edit 20:21 and 20:79 in this change, the way A2 and A4 did, with A7 as the provenance entry rather than the mechanism.

## MAJOR findings

### MAJOR 1 — design 04 A5 still states the claim design 03 A50 withdrew

**File:** `docs/designs/04-receipt-tap.md:114` vs `docs/designs/03-policy-compiler.md:429`

04:114: *"design 03 A50 withdraws the replica-complete proof … and **claims a fallback remediation only where `gatewayReplicas == 1`**."* Design 03 A50 now says the opposite: *"`llmFallbackActive` cannot legitimately go true **anywhere** until design 04 names both producers, and this design says that rather than shipping a remediation that reports itself"* (03:429). This is round-1 BLOCKER 1's sentence, moved into the producing design.

**Fix:** 04:114 should read that design 03 A50 claims **no** fallback remediation on any cluster until both producers exist, and that the one-replica bound is what becomes available afterwards.

### MAJOR 2 — §3.4's concern-mapping table still consumes `consumerBudgets`, and A48 asserts it does not

**File:** `docs/designs/03-policy-compiler.md:453` vs `:721`

03:453 is unchanged: *"| Expose visibility | listener class cluster / org / public (+ OAuth clients, **consumer budgets**) |"*. A48's log entry (03:721) states the field was removed *"including from **§3.4's concern-mapping table** and A25's enumeration, which a critique caught still consuming it two hundred lines apart"*. A25's enumeration was fixed; §3.4's row was not, and the amendment claims otherwise. A claimed closure that did not happen is worse than an open one, because the next reviewer trusts it.

§3.4 is the table an implementer compiles from, and §4's totality rule cannot catch this: the field is gone from the *input*, so there is nothing to leave unmapped.

**Fix:** strike `consumer budgets` from 03:453, or correct A48 to say which sites were actually reached.

### MAJOR 3 — `recordDigest` still says six over seven fields, and the two-write model breaks the immutability row

**File:** `docs/designs/03-policy-compiler.md:68,75`

03:68 is unchanged: *"SHA-256 over the **six** fields above."* The fields above it are now `schemaVersion`, `hash`, `behaviourProjection`, `originalBudget`, `resolvedInputs`, `sealedBindings`, `appliedDigest` — **seven**. A46 added `sealedBindings` and A47 reshaped `resolvedInputs`; neither adjusted the count.

A46 also makes it structurally wrong. 03:75 says *"`resolvedInputs` and `sealedBindings` are **absent** until the seal, and are immutable after it; `appliedDigest` is then the only field that may still change."* If the digest covers seven fields, then the seal changes three of them plus `recordDigest` itself, so `appliedDigest` is not the only field that changes and `recordDigest` is not immutable. If `sealedBindings` is outside the digest, the one-way property has no integrity check at all and 03:79's corruption row cannot detect a forged seal — which matters, because `sealedBindings` is now the operand the comparator keys the freeze on.

**Fix:** say seven, say explicitly that `resolvedInputs` and `sealedBindings` are covered by `recordDigest`, and rewrite 03:75 to name the fields that change at the seal (`resolvedInputs`, `sealedBindings`, `recordDigest`) and the one that changes after it (`appliedDigest`, and `recordDigest` with it).

### MAJOR 4 — the run-namespace-loss recovery is still unstated, and the recoverable sub-case is still refused

**Files:** `docs/designs/03-policy-compiler.md:675` · `docs/designs/02-agent-crd-operator.md:186,348`

03:675 is unchanged and still routes a missing or digest-mismatched object to design 02's `RevisionMaterialUnavailable`, which (02:348) *"sends the revision's route to **weight 0**"*. Design 02's recovery for a cluster-rights namespace deletion (02:186, check 11) re-mints material *"from the current sources **if they still hash to the desired digest**"* — which works for env copies, whose sources persist, and can never work for `resolvedInputs`, whose source is the producer that moved.

The inline form would rescue small values, but design 03's body does not have the inline form (BLOCKER 3), and this row does not distinguish the two forms. Round 1 also asked for the recoverable sub-case — object missing, current resolution still hashes to the recorded digest, so it can be recreated — and the row still refuses it.

**Fix:** split the row. Missing object + current resolution matching the recorded digest ⇒ recreate. Missing object + drifted resolution ⇒ state the human action and the condition that names it in those words. Say which form each case applies to.

### MAJOR 5 — `sealedBindings` is the comparator's key and has no grammar; design 02 invents one

**Files:** `docs/designs/03-policy-compiler.md:64,76` · `docs/designs/02-agent-crd-operator.md:93`

03:64 declares `sealedBindings: [string]` and 03:76 makes it *"the operand the comparator uses; a binding not in that list has no applied operand at all"*. Design 03 never says what the string is. Design 02's new status block picks one — `sealedBindings: [tools/claims-system]` (02:93) — while the same block's comment says *"design 03 §3.1 owns the record's contents"*.

This is the non-injective-key shape this lineage has now removed from three other places, sitting on the key that decides whether a live route's filter freezes. Under `<kind>/<name>`, is a KG binding keyed on `name` alone or `{name, version}` — 02:48-49 shows `knowledge[]` carrying both, and two versions of one graph resolve to different endpoints. Is `identity` a member (BLOCKER 2)? Is an `llm.providers` entry? A key that cannot distinguish two bindings freezes one against the other's value.

**Fix:** define the grammar in 03:64 — the kind discriminator, the fields that make it injective within one revision, and the closed set of kinds that may appear — and make design 02's example render it rather than invent it.

### MAJOR 6 — A50 says "unachievable" and "in principle" in the same breath as the mechanism that achieves it

**File:** `docs/designs/03-policy-compiler.md:429,719`

03:429 opens *"that requirement is **withdrawn as unachievable**"* and closes, ten lines later, *"A per-replica proof is **not impossible in principle** — an operator that resolved the gateway's `EndpointSlice` and dialled each Pod directly could obtain one."* §11's A50 (03:719) still ends *"this is the first retracted for being unattributable **in principle** rather than in practice"* — the exact phrase round 1 asked to replace, in the same paragraph that now concedes the EndpointSlice route.

The added concession is the right content. Leaving the two superlatives standing means a later design is told both that the door is closed and that it is open, and the row is where an implementer stops reading.

**Fix:** replace *"withdrawn as unachievable"* with *"withdrawn: this design declines to build per-Pod canary addressing"* and strike the *"in principle"* clause from 03:719. Name the cost — an `EndpointSlice` read, N direct dials, a membership-change restart, and the NetworkPolicy to permit them.

### MAJOR 7 — the three-state table's middle row is false for a candidate, and its behaviour has no transaction

**File:** `docs/designs/03-policy-compiler.md:365` vs `:303-310`, `docs/designs/02-agent-crd-operator.md:243`

03:365: *"**Unsealed on a revision that has never been active** | none | **nothing is serving**, so nothing can be widened ungated. The binding **re-resolves each reconcile** and the revision compiles with whatever is current."*

A candidate is serving. Design 02 §3.3 step 3 (02:243) gives it *"a **candidate-only route** whose admitted set is exactly the eval run's own SVID"*, and §3.3.1's route table makes `-auth` mandatory on it *"always, no opt-out"*. That route is created, policied and converged. So re-resolving each reconcile mutates an **applied, converged** ResourceSet on a live route.

Which transaction does that? §3.3.3 has `Create`, `Loosen` and `Withdraw`. A producer widening on an unsealed binding has no applied operand, so the comparator cannot classify it, so §3.3.3's own rule sends it to `Unknown → Tighten` — and the non-spec `Tighten` cell is A38's per-producer contract, which for a tool server is *withhold* against a sealed value that does not exist. The design states a behaviour with no transaction and no comparator path, on the one route class it says may never lack its guarantees. Round 1 asked for exactly this (*"state what the comparator does while unsealed"*); the answer was given for row 3 and not for row 2.

**Fix:** say which transaction a re-resolution on a not-yet-active revision runs, and what happens when the re-resolved set is *narrower* than what the candidate route is already serving to the eval principal.

### MAJOR 8 — one unrelated spec edit launders a frozen widening past the freeze, and the design does not name it

**File:** `docs/designs/03-policy-compiler.md:364,365,368`

R1 is active and sealed with `{read_claim}`. The tool server begins advertising `wire_funds`; A37 freezes R1 correctly and raises `PolicyInputDrifted`. The operator, acting on the condition, edits any behaviour field — an image tag, a replica count is policy, so say the system prompt. R2 mints. Per 03:365 R2 *"compiles with whatever is current"*, so R2's candidate route carries `{read_claim, wire_funds}`. Design 16 gates behaviour through the gateway and, as 03:358 itself says, *"never reads a tool catalogue"*. R2 passes, promotes, becomes `activeRevisionDigest`, and seals the widened set as *"what the gate approved"* (03:364).

This is the `{A}→{A,B}→{B}` laundering shape design 02 A62 keeps as a counter-example, applied to the freeze. It is arguably the intended semantics of "a new revision compiles with whatever is current" — but it is the escape hatch from A37, it is reachable by one `kubectl edit`, and it is stated nowhere. 03:368's residual discusses only the never-available case and asserts *"there is no automatic path to a gated one"*, which reads as a general property and is not one.

**Fix:** state it in §3.3.3 beside the freeze: a producer widening is frozen for the *active* revision and is picked up, ungated on the catalogue, by the next revision — and say whether that is acceptable, or whether the freeze must also apply to a candidate until 03:368's owed projection change lands.

## MINOR findings

1. **`docs/designs/03-policy-compiler.md:364,366`** — the new table reintroduces the overclaim 03:358 just removed. Row 1: *"compared against what **the gate approved**"*; row 3: *"The revision **was gated without it**"*. 03:358 says the seal captures *"the material the revision published with — **not** material a gate inspected"*, and for a first revision under `GatesSkipped=NoGatesDeclared` (design 02, 02:249) there is no gate at all. Use "published with" in the table too.
2. **`docs/designs/03-policy-compiler.md:362`** — the table's header column reads *"Binding, on an active revision"* and two of its three rows are about revisions that are not active. Rename the column, or split the table.
3. **`docs/designs/03-policy-compiler.md:77,722`** — the object's **kind** is still never named, so *"the object's own — one MiB"* has no referent (a ConfigMap's 1 MiB spans `data`; etcd's is 1.5 MiB; a Secret's counts differently). A47 says *"design 11 is owed a stated catalogue bound"*; design 11 is untouched in this change and has an *"Owed to this design"* §11 (11:88) that is exactly where it belongs.
4. **`docs/designs/03-policy-compiler.md:699`** — round 1's MINOR 4 asked for one row per rule. The cell is now longer: three amendments' cases run together, then rejoin the pre-existing list with a bare em-dash mid-cell (*"…must not advance the transaction. — `Apply` ordering, SSA ownership…"*). A newcomer cannot tell which clause belongs to which rule. This is a `write-spec` defect, not a style preference.
5. **`docs/designs/03-policy-compiler.md:3`** — the status line still reads *"Amendments **A1–A45** (2026-08-25/09-03)"* and *"Latest verdict: `reviews/03-codex-review-r6.md`"*. Five amendments and two review rounds are missing from the header a reader trusts first.
6. **`docs/designs/03-policy-compiler.md:429`** — the action is now named ("accept partial programming and watch receipts, or scale to one replica"), but neither names a state that **clears** the condition, and design 20 §4 (20:34) escalates any condition remediation cannot clear within `verifyTimeout`. `LLMFallbackUnavailable` is also still the wrong word for "the fallback is serving and its coverage is unproven".
7. **`docs/designs/26-tenant-cr.md:43`** — §6's row was fixed; §3's Traffic row still asserts the whole mechanism (*"a `TenantPolicyIntent` (tenant-scoped target kind) recorded as a design 03 amendment with its own row"*) that A2 says is one sentence with no schema, and D1 still counts it as one of two new mechanisms.
8. **`docs/architecture.md:166` · `docs/designs/23-app-layer.md:24`** — both fixes contradict themselves inside one sentence. Architecture: *"per-consumer budgets are **not** part of it … external consumers are just another authenticated principal (IdP OAuth client, **per-consumer budgets**, receipts)."* Design 23: the sentence says the quota half does not exist and still ends *"rate limits per `consumerBudgets`"*, in a clause with three levels of nested parentheses. Design 23's status line is also not marked as carrying an uncritiqued amendment, while 04, 20 and 26 are.

## Where the work is right

Said after trying to break each of these.

- **A49 is now honest, and it is the best of the five.** The reduction to *"applied and converged at the control plane"*, the explicit *"no witness of any kind today, in either direction"*, the retraction of the second condition reason with the reason it cannot page, and the deferred §8 case with its prerequisite named — that is the shape the rest of this set should have taken. 03:386's rewrite of the `Withdraw` justification is exactly right: it keeps the transaction, names the residual exposure, and does not pretend it is zero.
- **The seal trigger is a real field.** `status.activeRevisionDigest` exists at 02:79, carries the full SHA-256, and is the value design 02 A47 already made the collision guard's non-forgeable authority. Moving off the apply machine's `Served` was correct and the field chosen is the right one. What is wrong is the tense, not the choice.
- **§8's replacement mutations can kill.** Sealing on `Served` instead of on `activeRevisionDigest`, an over-bound catalogue producing the compile error, and a NACK advancing the withdrawal transaction are three mutations that fail if the rule is absent. The size case that could not fail is gone.
- **MAJOR 10's fix went further than asked.** Design 04 A5 now locates the consequence on the audit index, and adds the `(tenant, ns, agent)` key and its two-same-named-Agents test to what is owed — a defect that pre-dates this amendment set and that nothing in the round-1 finding obliged it to fix.
- **Design 26 A2 and design 02's §5 row are correctly shaped.** A2 names what is owed as a table rather than prose and says plainly *"not enforced at all"*; 02:443's row (*"Not on the CRD … nothing writes or reads it until the compiler exists"*) is the right register for a field that exists on paper. Design 02's condition vocabulary needed no new type, which I verified by mutation.
- **The condition-closure test is real.** Removing `PolicyInputDrifted` from 02:98 fails `TestConditionVocabularyIsClosed` with a message naming the direction. Every condition design 03 asserts — `PolicyInputDrifted`, `CapabilityUnavailable`, `RevisionMaterialUnavailable`, `RevisionRecordUnreadable`, `LLMFallbackUnavailable` — is in that list, and `WithdrawalNotEnforced` correctly never entered it.

---

**Round 2 — 03 A46–A50 / 02 / 04 A5 / 20 A7 / 23 / 26 A2 / architecture: REVISE — 5 BLOCKER, 8 MAJOR, 8 MINOR. Not converged.**

---

# Round 3

- **Scope**: the same amendment set after the round-2 fixes — `git diff docs/` at 383 lines across `architecture.md` and designs 02, 03, 04, 20, 23, 26.
- **Method**: every round-2 finding decided against the repository and the producing design, never against the amendment prose. Design 02's name-shape deletion authority — the rule design 03:95 builds its owed item on — was checked by mutation: removing the `env-<n>` name loop from `isRevisionMaterial` (`internal/controller/material.go:257-263`) makes `TestALabelledBystanderObjectIsNotDeleted` fail; restored from a copy, the test passes and `git status` is back to seven modified docs.
- **Verdict: REVISE — 5 BLOCKER, 6 MAJOR, 8 MINOR. It has not converged, and this design is now the wrong unit of work.**

**The answer to the only question that mattered: yes, a third time.** Round 1's phrase was "one field or one design over"; round 2's was "one tense over, one design over, one row over, one document over". Round 3's is **one clause over**. The pattern has sharpened rather than dispersed: in every case this round the fix landed exactly on the string the reviewer quoted and nowhere else.

- Round 2 said *"edit 20:21"*. 20:21 was edited — the `hop.endpoint` clause at the front of the row. The `hop.gatewayInstance` clause at the back, which round 2 quoted **verbatim**, is untouched (BLOCKER 3).
- Round 2 said *"make `identity` a `Binding<T>` **and** add design 06 to §3.1's binding-state table"*. The type changed at 03:26. The table at 03:121 still enumerates "11 for tools, 01/13 for KG" (MAJOR 4).
- Round 2 said *"say seven"*. 03:78 was rewritten to say "every field". 03:70 — the schema comment four lines above, which is what an implementer types — still says **six** (MAJOR 3).
- Round 2 attacked the seal's *tense*. The tense is fixed; the **instant** is now wrong, because the trigger fires after the canary rather than before it (BLOCKER 2), and the transition has no durable marker so the only implementable edge detector re-fires (BLOCKER 1).

## Job 1 — closure of the 21 round-2 findings

| # | Round-2 finding | Status |
|---|---|---|
| B1 | seal predicate is a standing conjunction | **PARTIAL** — the tense is fixed; the instant and the marker are not (new BLOCKER 1, BLOCKER 2, BLOCKER 5) |
| B2 | `identity` cannot seal | **PARTIAL** — type changed, table not (new MAJOR 4); grammar admits a kind it cannot key (new BLOCKER 4) |
| B3 | two-form storage not in the body, keyed on size | **PARTIAL** — excellent in design 03; design 02 still carries the size rule (new MAJOR 1) |
| B4 | §5's A46 row contradicts §3.3.1 | **CLOSED** |
| B5 | design 20's row and A6 carry the withdrawn requirement | **OPEN** (new BLOCKER 3) |
| M1 | design 04 A5 states the withdrawn claim | **CLOSED** |
| M2 | §3.4 still consumes `consumerBudgets` | **CLOSED** |
| M3 | `recordDigest` says six over seven | **OPEN** (new MAJOR 3) |
| M4 | run-namespace-loss recovery unstated | **CLOSED** — condition choice residue only (new MINOR 7) |
| M5 | `sealedBindings` has no grammar | **PARTIAL** — a grammar exists and is not injective (new BLOCKER 4) |
| M6 | A50 "unachievable"/"in principle" | **PARTIAL** — body fixed, §11 not (new MINOR 1) |
| M7 | three-state row 2 false for a candidate | **PARTIAL** — the eval-principal case answered, the canary skipped (new BLOCKER 2) |
| M8 | one spec edit launders a frozen widening | **CLOSED** — and it is the best paragraph in the change |
| m1 | table reintroduces the "gate approved" overclaim | **CLOSED** |
| m2 | table header column names the wrong scope | **CLOSED** |
| m3 | object kind unnamed, design 11 untold | **PARTIAL** — kind named; design 11 still untold and now owes a second bound (new MINOR 6) |
| m4 | §8's envtest cell is a run-on | **OPEN, longer again** (new MINOR 3) |
| m5 | status line stale at A1–A45 / r6 | **OPEN** (new MINOR 2) |
| m6 | `LLMFallbackUnavailable` misnomer, no clearing path | **OPEN**, and design 20 now has a silent dead detector (new MAJOR 5) |
| m7 | design 26 §3 Traffic row and D1 | **OPEN** (new MINOR 5) |
| m8 | architecture / design 23 self-contradiction | **OPEN, verbatim** (new MINOR 4) |

Eight closed, six partial, seven open.

## BLOCKER findings

### BLOCKER 1 — the seal is a transition with no durable marker, so the only implementable edge detector seals late

**Files:** `docs/designs/03-policy-compiler.md:66-67,79`

03:79 makes the seal *"a **transition, evaluated once**, and never a standing condition: at the single reconcile in which this revision's digest **becomes** `status.activeRevisionDigest` …"*. A level-triggered reconciler holds no memory of the previous reconcile. The only way to detect that edge across a restart is durable state: *active, and this record is not yet sealed*. The design provides no marker for the second half.

A46 **deleted** the one field that could have been it. Round 1's record carried `sealed: bool`; A46 replaced it with `sealedBindings: [string]`, which 03:66 says is *"ABSENT until the seal"*. When zero bindings are `Resolved` at the instant — the case A46 exists for — the seal writes an **empty list**, which after a status round-trip is indistinguishable from the field never having been written. This is A24's own lesson (*"an empty slice meant two things"*) reapplied one field over, in the amendment that cites A24's table.

**Failure scenario — round-2 BLOCKER 1's scenario, surviving its own fix.** `tools: [claims-system]`, design 11 absent, design 06 absent, so **no** binding is `Resolved` at promotion. The seal fires and writes `sealedBindings: []`. Two months later design 11 is installed and the tool server advertises `{read_claim, wire_funds}`. Any implementation of "not yet sealed" keyed on the record's own contents sees an unsealed record on an active revision, seals the catalogue that just arrived, and the tool route is emitted onto a route that has been carrying production traffic since. No gate ran. `sealedBindings` now records the widened set as the material the revision published with.

**Fix:** restore an explicit marker — `sealedAt: timestamp` or `sealed: bool` — written **unconditionally** at the seal whether or not any binding resolved, covered by `recordDigest`, and state the predicate as *the seal fires at the reconcile where the digest is `status.activeRevisionDigest` **and the marker is absent**, and never again*. §8's mutation is then writable: promote with zero bindings resolved, install the producer, assert `sealedBindings` does not grow.

### BLOCKER 2 — the seal instant is after the canary, so the canary is an ungated widening window, and the table asserts the opposite

**Files:** `docs/designs/03-policy-compiler.md:379` vs `docs/designs/02-agent-crd-operator.md:244,555,557`

03:379: *"**Unsealed on a revision that has never been active** | none | **nothing carries production traffic**, so nothing can be widened ungated. The binding **re-resolves each reconcile** and the revision recompiles as a **`Create`** — the comparator is not consulted … **a candidate's route admits only the eval principal**."*

Both premises are false for an entire named phase. Design 02 §3.3 step 4 (02:244): *"Pass → **weights shift** (canary steps, default `10 → 100`); `activeRevision` **flips**"* — the shift precedes the flip. Design 02 A13 fixes what that phase means and whose it is: *"`Canary` was wrong because §3.3 step 4 fixes its meaning as **weights are shifting**"* (02:555) and *"`Canary` is reserved for the real weighted shift **arriving with design 03**"* (02:557).

So for the whole canary — a state this design owns — the revision is not `activeRevisionDigest`, is therefore unsealed, is serving real users on the **production** route at weight 10 then 100, and row 2 says its bindings re-resolve every reconcile with the comparator not consulted.

**Failure scenario.** R2 passes its gate and enters canary at 10%. The tool server begins advertising `wire_funds`. The next reconcile re-resolves and recompiles as a `Create` over a new desired set; `wire_funds` reaches a route carrying 10% of production traffic. Design 16's EvalRun finished at step 3 and saw the narrower set — so the row's own justification (*"the eval then judges the narrowed set … the gate must see what would actually serve"*) is exactly what does not happen. This is round-2 MAJOR 7 committed one **phase** over: the fix answered the eval-principal case and skipped the state between the eval and the flip.

**Fix:** move the trigger to the first non-zero production weight, or keep the flip and give `phase: Canary` its own row — stating which transaction a re-resolution runs on a route already carrying users, and whether a widening during canary aborts the rollout. Do not leave a state design 02 says design 03 owns unnamed in design 03's own table.

### BLOCKER 3 — design 20's Model-drift row still specifies the check A7 withdrew, in the sentence round 2 quoted

**File:** `docs/designs/20-drift-controllers.md:21`

Round 2's fix was *"edit 20:21 and 20:79 in this change, the way A2 and A4 did."* 20:21 was edited — the detection clause at the front now reads *"`hop.endpoint` has no producer in design 04 (A7), so this read has no input today and drift detection cannot run"*. The remediation clause at the back is untouched and still reads, word for word what round 2 quoted:

> *"The witness is bounded (A6): one receipt names one gateway replica via `hop.gatewayInstance` (design 04 A4), so **activation requires a distinct instance per replica** or reports `LLMFallbackUnavailable/PartiallyProgrammed`"*

The same row also still states the postcondition as *"attributed by full endpoint identity in the receipt. `llmFallbackActive` reports remediation only after that positive evidence"* — which 03:444 now says is unobtainable on **every** cluster, one replica or many.

**Failure scenario:** unchanged from round 2 and now in its third round. The remediation lives in design 20. An implementer reads the row that specifies the whole path and builds a per-replica distinct-instance check over a field design 04 A5 says has no producer.

Design 20's own **A4** records this shape as *"the propagation failure this review set keeps finding"*, and A2 and A4 both edited the body rows. A7 appended and then, this round, edited one clause of one row.

**Fix:** rewrite the entire remediation clause of 20:21 to A7's state — no `gatewayInstance`, no per-replica requirement, no positive-evidence postcondition — and leave A6/A7 in §11 as provenance.

### BLOCKER 4 — `sealedBindings`'s new grammar is not injective for `knowledge` and has no value at all for `identity`

**Files:** `docs/designs/03-policy-compiler.md:80,26,108` · `docs/designs/02-agent-crd-operator.md:48-51,93`

03:80: *"`<kind>/<name>` with `kind` ∈ `tools`, `knowledge`, `identity` and `name` the binding's `requested` string — the same string `Binding<T>` carries, so **no join and no split is performed on it** and two bindings cannot collapse onto one key. This is the comparator's key: a non-injective one here would be the fourth in this design's lineage."*

It is the fourth.

- **`knowledge` collapses.** 02:48-51 declares `knowledge[]` entries as `{name, version, scope}`; nothing in design 02 forbids two entries with one name at two versions (I grepped for uniqueness constraints — there are none on `knowledge`). 03:108's own provenance row says *"`spec.knowledge[]` carries `{name, version}` only"*. `Binding<T>.requested` is a single `string` (03:31), so representing a two-field request requires exactly the **join the grammar forbids**. Under the grammar as written, `{payer-policies, v12}` and `{payer-policies, v13}` both key `knowledge/payer-policies`. Design 01 versions graphs, so the two resolve to different endpoints. The seal writes one; the comparator compares the other against it; a v13 endpoint move is judged `Loosen`/`Tighten` against v12's sealed value and frozen or withdrawn on the wrong evidence.
- **`identity` has no name.** Nothing in `AgentSpec` names an identity; 03:104's producer is design 06's SVID, which is an *output*, not a request. So the grammar's third kind has no `name` to render. Design 02's example (02:93) shows only `tools/claims-system` and cannot render the other two.

**Fix:** define `requested` per kind rather than asserting one string covers all three — `tools/<name>`, `knowledge/<name>@<version>`, and `identity` as a singleton kind with no name segment — and say plainly that a grammar forbidding joins cannot express a two-field request, because that is why this row is wrong. Then make design 02's example render all three.

### BLOCKER 5 — the seal is a two-object write with no crash rule, and its failure is silent permanent capability loss

**Files:** `docs/designs/03-policy-compiler.md:79,95,684` · `docs/designs/02-agent-crd-operator.md:226,347`

Round 1's BLOCKER 4 asked for this in these words: *"A crash between the seal write and the object create must also be specified, or the record points at nothing on a revision that just published."* Two rounds later nothing states it. 03:79 puts *"every binding … is written to `sealedBindings` and **the sealed object**, and `resolvedInputs` is written in the same status update"* — a ConfigMap create and a status update, two API calls, in one sentence. §5's only crash row (03:684) is the apply machine's.

**Failure A — crash between them.** On restart the digest is already `activeRevisionDigest`, so per 03:79 *"nothing seals after it"*: the record is permanently unsealed on an active revision, and per 03:380 the capability is **withheld** and *"only a new revision can acquire it"*. An Agent whose tools resolved cleanly at promotion loses every tool for the life of that revision because the operator restarted at the wrong moment — silently, since `PolicyInputDrifted` names a producer that never moved. The created ConfigMap leaks (MAJOR 2's path).

**Failure B — the name is taken.** The object is `<agent>-<revisionHash>-inputs` (03:95). `revisionHash` is the ten-character truncation, which 02:226 says is *chosen*-collidable at about `2^20` trials. Design 02's rule for a pre-existing material object with different content is `RevisionMaterialCollision` and *"the revision is not published"* — unavailable here, because the seal fires **after** promotion. The design states no rule.

**Fix:** with BLOCKER 1's marker in place, make the seal idempotent and ordered — object first, status second, so a retry re-creates or content-verifies against the recorded digest — and give the name-taken case its own row, since design 02's answer cannot be borrowed for a write that happens after publication.

## MAJOR findings

### MAJOR 1 — design 02 still carries the size-keyed rule design 03 replaced with a mode-keyed one

**File:** `docs/designs/02-agent-crd-operator.md:92` vs `docs/designs/03-policy-compiler.md:87-93`

02:92, added in **this change**: `resolvedInputs: {name: …, digest: …}   # inline below 4 KiB, by reference above it`. Design 03's new table says an external Agent and a hard-mode tenant are **always inline, regardless of size** — the whole point of round-2 BLOCKER 3 was that the deciding property is mode, not size. The schema block a reader trusts first therefore states the rule round 2 rejected, and shows only the by-reference form, so the **inline** shape is still undefined on both sides of the interface.

Round-2 BLOCKER 3 named 02:92 explicitly. The fix was made in design 03 and not in the comment the finding pointed at.

**Fix:** render both forms in 02:92 and replace the comment with *"inline for external Agents and hard-mode tenants and below 4 KiB otherwise; by reference above it — design 03 §3.1"*.

### MAJOR 2 — the owed item to design 02 is under-specified against the shipped enforcement, and design 02 did not receive it

**Files:** `docs/designs/03-policy-compiler.md:95` · `internal/controller/material.go:244-263` · `docs/designs/02-agent-crd-operator.md:340,347`

03:95 correctly names the gap and says *"**Owed to design 02**: extend that name-shape authority to cover `-inputs`"*. Two problems.

First, the owed item is not sufficient. `isRevisionMaterial` — which I confirmed by mutation is the real delete authority — gates on **three** things, not one: the `env-<n>` name shape, a non-empty `assayd.dev/revision-digest` annotation, and a non-empty `assayd.dev/source` annotation, which `material.go:107` sets to `ref.Kind + "." + ref.Name` of the source object. A `-inputs` object has no source object. 03:95 specifies the object as carrying two **labels** and no annotations. Extending only the name shape leaves it failing two of three gates, so it still leaks — in a namespace shared by every Agent in the source namespace (`RunNamespaceName`, `runnamespace.go:118`), which 02:135 says Kubernetes GC does not reach.

Second, design 02 was edited in this change and carries **no** owed note. 02:340 still declares the name as `<agent>-<revisionHash>-env-<n>` and 02:347 still declares the derived `env-<n>` shape as the sole deletion authority. The debt exists only on the side that cannot pay it.

**Fix:** state what the `-inputs` object carries in place of a source annotation (its own marker, or an explicit exemption), and put the owed item in design 02 §3.3 — the design that owns the rule and that this change already opened.

### MAJOR 3 — `recordDigest` still says six over seven fields, and the schema comment now contradicts the rule four lines below it

**File:** `docs/designs/03-policy-compiler.md:70` vs `:78`

Round-2 MAJOR 3 asked for one word. 03:78 was rewritten — *"`recordDigest` covers **every** field including both of them"* — and 03:70, the schema comment, still reads *"SHA-256 over the **six** fields above, schemaVersion included"*. The fields above it are `schemaVersion`, `hash`, `behaviourProjection`, `originalBudget`, `resolvedInputs`, `sealedBindings`, `appliedDigest`: **seven**.

So the document states the digest's domain twice and the two statements disagree by exactly one field, and the disputed field is `sealedBindings` — the operand the freeze keys on and the thing 03:78 says a forged seal is caught by. An implementer compiles the comment. Third round on this finding.

**Fix:** say seven.

### MAJOR 4 — `identity` became a `Binding<T>` and the two rules that key on binding-ness were not followed

**Files:** `docs/designs/03-policy-compiler.md:26,121,260` · `docs/designs/06-identity-glue.md`

Round-2 BLOCKER 2's fix had two halves: change the type, **and** add design 06 to §3.1's binding-state table. Only the first landed. 03:121's `ProducerAbsent` row still reads *"the producing design is not installed (**11 for tools, 01/13 for KG**)"* — a closed enumeration that now omits the producer of a field the same section calls a Binding. Two consequences follow that nobody chose:

- **A33's rule sits in that row**: `CapabilityUnavailable` is set on *"**every Agent that requested a binding from that producer**"*. No Agent declares an identity; every Agent has one. Read as written, at P1 with design 06 absent, every Agent in every cluster carries `CapabilityUnavailable` and `Ready=False` — precisely the outcome §5's route-class row (03:682) refuses in those words: *"one would fire on every Agent in every core-tier cluster and say nothing actionable."*
- **`Unresolvable` does not map.** It is defined as *"the producer exists and the name does not resolve"*. An SVID has no name to resolve. So 03:260's *"unresolvable identity ⇒ `PolicyCompileFailed`"* has no trigger, and the third state of the binding vocabulary is undefined for the new kind.

And design 06 was never told. `docs/designs/06-identity-glue.md` contains no `Binding`, no resolution state, no `ProducerAbsent`/`Unresolvable`, no amendment, and its status line is unmarked while 04, 20 and 26 carry *"uncritiqued"*. Design 03 has declared the shape of another design's output without amending it — the same failure round 1's BLOCKER 6 and round 2's BLOCKER 5 both found, one design over for the third time.

**Fix:** add design 06 to 03:121's enumeration, say what `ProducerAbsent` and `Unresolvable` mean for an SVID, carve identity out of A33's per-Agent condition (or accept and state the fleet-wide consequence), and record the owed resolution-state contract on design 06 with its status line marked.

### MAJOR 5 — design 20's model-drift detector is now dead and no condition says so

**Files:** `docs/designs/20-drift-controllers.md:21,44-48,3`

20:21 now states *"**`hop.endpoint` has no producer in design 04 (A7), so this read has no input today and drift detection cannot run**."* §6's failure table has `DriftDetectionDegraded` scoped to *"Canary CronJob fails (infra)"* and nothing else. An entire detector family is inoperative on every install and raises **nothing**; 20:3's status line mentions only the fallback witness, not that detection itself cannot run.

NFR-8's rule is that a degraded guarantee surfaces as a condition naming the human action. This silence was introduced by the propagation itself — the amendment that made the design honest about its input made it silent about the consequence.

**Fix:** add a §6 row — the model-drift detector has no input until design 04 A5's producers land, `DriftDetectionDegraded` with a distinct reason naming design 04 — and say it on 20:3.

### MAJOR 6 — the external-Agent row rests on a premise the same design refutes, and §3.1's size row still names the wrong bound

**File:** `docs/designs/03-policy-compiler.md:92,81`

03:92: *"an external Agent runs elsewhere and has no run namespace … **Its resolved inputs are an endpoint and an OAuth client identity, which do not approach the bound.**"*

`spec.tools[]` is a sibling of `external` in the CRD (02:52) and nothing excludes an external Agent from declaring tools or knowledge; 03:26 and 03:62 put `tools[].toolAllowlist` and `knowledge[].endpoint` in the sealed value unconditionally. So an external Agent fronting a large MCP catalogue takes the 4 KiB inline path and is a **permanent `PolicyCompileFailed`** — a capability removed by a storage rule, justified by a sentence describing a different Agent. Round 1's MAJOR 7 asked what an external Agent's sealed value is; the answer given asserts what it contains rather than deriving it.

Separately, 03:81's Size bound row still says *"The bound that remains is the object's own — **one MiB**"*, which is false for two of the three cases in the table nine lines below it, where the bound is 4 KiB and there is no object.

**Fix:** say that an external Agent may declare tools and knowledge, that its sealed value is bounded at 4 KiB because no object is available, and that a catalogue over it is a compile error naming the binding **and the mode** — then correct 03:81 to name both bounds.

## MINOR findings

1. **`docs/designs/03-policy-compiler.md:737`** — §11's A50 still ends *"this is the first retracted for being unattributable **in principle** rather than in practice"*, four sentences after conceding *"'Unachievable in principle' was also too strong"*. Round-2 MAJOR 6 asked to strike it. 03:444 was fixed; §11 was not. One paragraph now argues with itself.
2. **`docs/designs/03-policy-compiler.md:3`** — the status line still reads *"Amendments **A1–A45** (2026-08-25/09-03)"* and *"Latest verdict: `reviews/03-codex-review-r6.md`"*. Five amendments and three critique rounds are missing from the header a reader routes on. Round 1 raised it; round 2 raised it; unchanged.
3. **`docs/designs/03-policy-compiler.md:717`** — §8's envtest cell is still one run-on: three amendments' cases interleaved, then rejoining the pre-existing list with a bare em-dash mid-cell (*"…must not advance the transaction. — `Apply` ordering, SSA ownership…"*). Round 1 MINOR 4, round 2 MINOR 4, longer again. `write-spec` treats this as a defect.
4. **`docs/architecture.md:166` · `docs/designs/23-app-layer.md:24`** — both still contradict themselves inside one sentence, verbatim as round 2 quoted. Architecture: *"per-consumer budgets are **not** part of it … external consumers are just another authenticated principal (IdP OAuth client, **per-consumer budgets**, receipts)."* Design 23 says the quota half *"does not exist yet"* and ends *"rate limits per `consumerBudgets`"*, in a clause with three nesting levels. Design 23's status line is still unmarked while 04, 20 and 26 carry *"uncritiqued"*.
5. **`docs/designs/26-tenant-cr.md:43,93`** — §6's row was fixed. §3's Traffic row still asserts the whole `TenantPolicyIntent` mechanism (*"recorded as a design 03 amendment with its own row"*) and D1 still counts it as one of this design's *"two new mechanisms"* — both of which A2 says do not exist. Round-2 MINOR 7, unchanged.
6. **`docs/designs/03-policy-compiler.md:81` · `docs/designs/11-connector-crd.md:88`** — the kind is now named (`ConfigMap`, one MiB across `data`), which closes half of round-2 MINOR 3. Design 11 still has not been told: its §11 *"Owed to this design"* is untouched, and the change adds a **second**, 256× tighter bound (4 KiB, for external and hard-mode Agents) that design 11 has also never signed. 03:81 still calls one MiB *"a bound a producer can be held to"*.
7. **`docs/designs/03-policy-compiler.md:693`** — the missing-object-and-drifted row raises `RevisionRecordUnreadable`, which by 03:64 means the record failed its **digest**; here the record is intact and the object is gone. Design 02 has `RevisionMaterialUnavailable` for exactly that. The row's claim that design 02's weight-0 path *"is the same direction"* as *"what is already serving keeps serving"* is not true — they are opposite outcomes for one physical event, and a reader now has three answers to a lost run namespace.
8. **`docs/designs/03-policy-compiler.md:26,32-33,80`** — `llm.providers[]` and `llm.fallback` are declared `Binding<T>` and are excluded from `sealedBindings`'s closed kind set with no statement of why. The answer is derivable — their provenance is spec, so the spec row of the A37 table governs and there is nothing non-spec to seal — and it is not written, on a row whose whole purpose is to be a closed set.

## Where the work is right

Said after genuinely trying to break each of these.

- **The laundering paragraph (03:382) is the best writing in this change.** It names the `{A} → {A,B} → {B}` shape, admits it is reachable by one `kubectl edit`, states it is the intended semantics rather than hiding it, and then owes design 16 the question that makes the claim honest — *"'the eval evaluated the new catalogue' holds only if the suite exercises the new tool, which nothing enforces."* Round-2 MAJOR 8 answered in full and then some.
- **§5's A46 row (03:694) is now correct, total and reconciled with §3.3.1.** It states that the transaction reaches `Served`, that publication is not what the seal keys on, and it gives both the never-active and the active branch. Round-2 BLOCKER 4 closed properly.
- **§5's run-namespace rows (03:691-693) split the recoverable sub-case exactly as round 1 and round 2 asked**, and the *"a value matching the recorded digest **is** that value however it was obtained"* rule is right and is the content-first rule design 02 already applies.
- **03:444 now ends *"A declined option with a named cost, not an impossibility"*.** That is the correct register, and the cost it names — N direct dials plus a membership watch, bypassing the production route the canary exists to traverse — is a real argument rather than a shrug.
- **04:114 now states design 03's actual claim.** It is the one cross-design propagation in this round that reached its target, and it went further than asked in round 1 by owing the `(tenant, ns, agent)` index key.
- **The mode-keyed two-form table (03:87-93) is the right structure and is in the body as a rule.** I checked the hard-mode row against design 26 §4 (26:56) and §7 (26:85): the compiler *does* hold cross-tenant read on tenant CRs through the vCluster kubeconfig, status *is* on the CR, and declining the namespaced ConfigMap read rather than taking it silently is the right call and matches what A1 did for its single `create`.
- **Design 02's new §5 row is accurate.** I grepped `api/v1alpha1/` — there is no `Revisions` field, no `RevisionRecord` type, no `SealedBindings`, no `ResolvedInputs`. *"Not on the CRD … nothing writes or reads it until the compiler exists"* is true.
- **Design 02's name-shape deletion authority is genuinely enforced, which is why 03:95's gap matters.** Removing the `env-<n>` loop from `isRevisionMaterial` fails `TestALabelledBystanderObjectIsNotDeleted`; restored from a copy, it passes. Design 03's premise about design 02 is checked and true.

## The unit of work is wrong

Three rounds, twenty-one findings, and the residue is the same shape each time because the process is fixing **quoted strings, not rules**. The evidence is mechanical: this round's fixes landed on 20:21 (one clause of it), 03:78 (not 03:70), 03:26 (not 03:121), and 03:444 (not 03:737) — in every case the exact span a reviewer pasted, and nowhere else. A fourth round will move the residue again rather than remove it.

Design 03 is now in the state design 02 was in before A62. Its status line is frozen at A45 and cites a review three rounds stale. Its §11 contradicts its own body. The same rule — what the record holds, when it freezes, what the digest covers — is stated five times, in the schema comment (03:70), the property table (03:78-81), the storage table (03:87-93), the three-state table (03:378-380) and §5 (03:691-694), and no two of the five agree. That is not a design an implementer can compile; it is a sedimentation log.

**Recommendation: stop amending and consolidate, as design 02 did in `b2a1bdc`.** Rewrite §3.1, §3.3.3 and §5 as one body with one statement of each rule and no amendment numbers in the prose, move A1–A50 to §11 as pure provenance, and re-issue the header. The five BLOCKERs above are then findings against a document that can hold a fix, rather than five more strings to patch.

---

**Round 3 — 03 A46–A50 / 02 / 04 A5 / 06 / 20 A7 / 23 / 26 A2 / architecture: REVISE — 5 BLOCKER, 6 MAJOR, 8 MINOR. Not converged; consolidate rather than amend.**
