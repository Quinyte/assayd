# Independent re-critique: Design 02 — Agent CRD + agent-operator

- **Verdict**: **REVISE** — 10 findings (7 major, 3 minor)
- **Reviewed**: `docs/designs/02-agent-crd-operator.md` (status *approved*, ADR-0019, amendments A1–A8)
- **Independence**: this is the independent pass design 02 never had — its original critique ran in the session that authored it and carried an explicit caveat. Treated here as a fresh draft.
- **Method**: all 8 lenses, weighted toward the four requested areas: rollout state-machine races, card fetch/registration failure paths, amendment-vs-body coherence, and the honesty of Deployment/Sandbox materialization under `replicas>1`. Baseline: designs 01–27 + amendments, ADRs 0001–0026.

**Framing**: this is the first thing anyone will build, and eight amendments have accreted onto a body that was never revised to absorb them. Two patterns account for most findings: **the CRD is missing a field three later designs write against**, and **the rollout state machine was specified for the happy path of one candidate at a time**.

## Findings

### 1. MAJOR — the Agent CRD has no `llm` block, but designs 03, 20 and 25 all write against one

`02-…md:29-50` (the full spec) vs design 03 §3.1, design 25 §5, and this design's own A8. Design 03's `PolicyIntent` — the struct **this operator produces** (§3.6) — carries `llm: {providers[], egressAllowlist?}`, and compiles from it the LLM Backends, the egress allowlist (ADR-0014's BAA control), and the `usdPerDay` max-price calculation. Design 25 §5 states "Agent CRs reference it in **`llm.providers`**". A8 adds "optional **`runtime.llm.fallback`**". Yet the CRD schema in §3.1 has no `llm` field at any level: `runtime`, `card`, `knowledge`, `tools`, `budget`, `gates`, `expose` — and nothing else.

So the operator is specified to produce a policy input from a field that does not exist, and the platform's model-egress control — the mechanism ADR-0014 leans on for HIPAA and design 25 calls "the allowlist's easiest member" — has no declaration surface. A8 makes it worse by disagreeing with design 25 about placement: `runtime.llm.fallback` (nested under runtime) versus `llm.providers` (top level).

**Fix**: add the block to §3.1 and settle the path in one place — recommended `spec.llm: {providers: [...], egressAllowlist?: [...], fallback?: {provider, model}}`, with A8 restated to match. This is a CRD field the P1 implementation needs on day one.

### 2. MAJOR — a spec change during canary has no defined behavior, and the status model cannot represent it

`02-…md:53-54,73-77`. `status` carries **one** `candidateRevision`. §3.3 describes exactly one candidate at a time: spec change → parallel workload → Held → eval → weight shift. But the ordinary sequence "someone pushes a fix while a canary is at 10%" produces a third revision: active `A`, canary `C1` serving 10% of real traffic, and new candidate `C2`.

Nothing states the rule. If `C2` overwrites `candidateRevision`, `C1` is orphaned — still holding 10% of production traffic with no controller tracking it, or torn down mid-task. If `C2` is queued, nothing describes the queue, its depth, or what a third push does. Either way the field cannot hold the truth, and `kubectl get agents` (the stated UX) shows something false.

**Fix**: state the policy and make status express it. Recommended: a new spec generation **supersedes** the in-flight candidate — `C1`'s weight drains to 0 before deletion (respecting `taskTimeout`, the same drain design 22's kill switch uses), `GatesPassed` resets, and the abandoned revision is recorded in an event and a `supersededCandidates` status list so the transition is auditable rather than silent.

### 3. MAJOR — `revisionHistoryLimit: 2` is undefined in scope, and on the natural reading it eliminates the rollback target during rollouts

`02-…md:76,79,142`. "Old revision GC'd after `revisionHistoryLimit: 2`" and "Rollback = re-point to a retained previous revision (**instant**, no rebuild)" — the platform's headline recovery property, and ADR-0019's stated reason for the number.

But the limit's scope is never defined: two revisions **total**, or two **besides** the active one? On the first reading, during any rollout the two slots are consumed by active + candidate, so the previous active — the only rollback target that matters — is already eligible for GC at exactly the moment rollback is most likely. Design 20 confirms this is live: its failure table has "Rollback target GC'd → Action refused, named in the escalation", and its r2 revision added an operator-side **retention hold** on the last eval-passing revision while a `Degraded` window is open. That hold is recorded in design 20 only; design 02, which owns the GC machinery, doesn't mention it.

**Fix**: define the limit as "N retained revisions **in addition to** active and any in-flight candidate", state the interaction with design 20's retention hold here (it is this operator's behavior), and add a failure row.

### 4. MAJOR — Sandbox materialization and blue-green revisions collide: the candidate starts with an empty scratchpad and the old one is GC'd

`02-…md:65,73-77`. §3.2 justifies `Sandbox` precisely for agents that "execute code or **hold local state**" — singleton, stable identity, **persistent scratchpad**. §3.3 then materializes every new revision as a **parallel workload** and GCs the old one after promotion.

For a Deployment agent that is correct and cheap. For a Sandbox agent it means: the candidate boots with an empty scratchpad, is evaluated in that state (so the eval measures a cold agent that production never resembles), and on promotion the *populated* scratchpad of the outgoing revision is deleted with its workload. Neither section acknowledges the other, and the failure is silent — a stateful agent simply loses its state on every deploy.

**Fix**: state the semantics explicitly, whichever is chosen — scratchpad is **ephemeral per revision** (then say so loudly in §3.2, because it contradicts why Sandbox exists), or the volume is **revision-independent** and reattached on promotion (then say what happens during canary, when two revisions would want the same volume; a read-only attach for the candidate is the usual answer). Also add the eval consequence: design 16 gates a cold candidate against a warm active.

### 5. MAJOR — `replicas>1` without a shared task store is permitted silently, against the platform's own loud-degradation doctrine

`02-…md:32,64,67,144`. The CRD example ships `replicas: 2`; §3.2 says A2A servers are "stateless HTTP; replicas scale"; and the task-state convention is honest that the JetStream-KV store is a **template** convenience, "not platform-mandated". The honesty is in the right place — but there is no **detection** and no **condition**.

A BYO agent (the platform's founding promise: "any container that speaks A2A") set to `replicas: 3` with in-memory task state will fail non-deterministically: `GetTask`/`SubscribeToTask` land on a replica that has never heard of the task. That is not hypothetical — design 23's SSE reconnect was *revised* to depend on exactly those A2A verbs precisely so it would be implementation-agnostic, and design 17's re-drive replay targets tasks by id.

Everywhere else the platform makes degraded-but-allowed states loud: `SandboxDowngraded`, `CardUnsigned`, `GatesSkipped`, `GatesBypassed`, `LabelsSparse`, `PricingStale`. Here, silence.

**Fix**: detect and surface it. The A2A card is the natural declaration point (a capability/extension asserting shared task state); absent that assertion with `replicas>1`, set `TaskStateUnverified=True` with the consequence named, and have the wizard/CLI warn at deploy. Admission could optionally require the assertion for `expose: public`.

### 6. MAJOR — finalization is declared in scope and never specified, and design 26 now depends on it

`02-…md:11` lists "**finalization**" in scope; no section describes it. What happens on Agent delete is therefore undefined for every resource this operator creates: directory entries (design 05 says they GC "with their revisions" — by what mechanism?), gateway resources (ownerRefs per §3.6 — but see below), the `agent-actor` OAuth client (A3; design 06 D3 says identity objects **deactivate, never delete** — so who deactivates it?), SPIRE registration entries, and in-flight tasks.

This is now load-bearing: design 26 R3-a specifies a **finalizer on the tenant-side Agent CR** that blocks removal until host-side gateway cleanup confirms — a mechanism that must exist in this design and doesn't. And §3.6's ownerRef-based lifecycle is exactly what design 26 found cannot cross a vCluster boundary, so the fallback path (explicit reconciled teardown) belongs here too.

**Fix**: add a finalization section — ordered teardown (drain traffic → revoke routes/policies in design 03's reverse order → deactivate the OAuth client → GC directory entries and SPIRE labels), the finalizer that gates it, and the design-26 hard-mode variant.

### 7. MAJOR — the phase enum was never extended for the guard states the amendments and design 22 introduced

`02-…md:52,152` (A1) and design 22 §5. `status.phase` is `Pending|Held|Canary|Ready|Degraded`, and it is what `kubectl get agents` renders (§3.1's printer columns are the stated operator UX). Since then:

- **A1(c)** applies "weight-0 to serving revisions" on budget exhaustion and calls it "a guard state, **distinct from Held/Canary**" — but names no phase for it. An agent serving zero traffic because its budget blew shows as… `Ready`? `Degraded`? The design doesn't say, and the two mean very different things to an operator at 3am.
- **Design 22 §5** makes kill "a **guard state** in the rollout machine (02 A1's family): `Killed=True` condition, exit only by explicit revive" — but `Killed` appears in neither design 02's phase enum **nor its condition list**. A cross-design condition that its owning design never declared.

Weight-0 now means three different things (never-gated Held, budget guard, killed) with no way to distinguish them in the field every operator looks at first.

**Fix**: extend the enum (`Held|Canary|Ready|Degraded|BudgetHeld|Killed`, or a `guardState` sub-field) and add `Killed` to the declared conditions with a pointer to design 22.

### 8. MINOR — five body passages are stale against their own amendments or against approved designs

The amendments are properly recorded in §11; the body was never updated, so a reader who stops before §11 gets the wrong answer five times:

| Body says | Superseded by |
|---|---|
| `:83` directory key `agents/<ns>/<name>@<revision>` | **A2** (dot-separated tokens; A2 itself flags the `@` charset as unverified) |
| `:48` "`gates:` ≥1 required in prod (admission)" | **A4** (applies *iff* the EvalSuite CRD is installed; else `GatesSkipped`) |
| `:83` card validation = "parseable, name matches CR, A2A version supported" | **A6** (adds signature verification; `CardUnsigned` for BYO/external) |
| `:42` `scope:` comment "gateway-enforced (design 01 §6)" | **design 01 A1** (gateway-**injected**, provider-enforced) — the same stale phrasing the architecture audit chased through two sections |
| `:97` "agentgateway **2.2** … we target the 2.2 CRD surface only" | the open minimum-version item (06-review R2-a): OSS token exchange and design 25's virtual models both postdate 2.2 |

### 9. MINOR — card and registration gaps

- **`status.card` is singular** (`:55`) while two revisions coexist during every rollout, each with its own image and therefore its own card. Which one does it describe? The directory is correctly keyed per revision (A2); status is not.
- **No terminal state for an unregistrable candidate** (`:119`): "retry w/ backoff; alert after 10m", and design 10 alerts at "candidate held >1h" — but nothing ever fails the candidate. Held revisions accumulate against `revisionHistoryLimit` (finding 3) and hold a slot indefinitely. State a give-up threshold that deletes the candidate with `GatesPassed=False`-style finality, or say explicitly that human intervention is required and why.
- **No card-vs-CR cross-check**: the card advertises skills; the CR grants tools and KG scopes. A card promising a capability the CR doesn't grant registers cleanly and fails at runtime. Design 09 golden-tests this template-side only. Worth one validation, or an explicit note that it is deliberately unchecked.

### 10. MINOR — the failure table is missing the rows its own amendments created

`02-…md:116-124`. No row for: budget exhaustion → weight-0 guard (A1), rollback target GC'd (design 20 has it; this design owns the GC), kill during rollout (design 22), IdP unavailable at `agent-actor` provisioning (A3 adds the condition, no row), or spend-aggregate unavailable (A1's new reconcile input — what happens when design 04's index is lagging or down?). The table is the operator's runbook index; five of the conditions this design now declares have no entry.

## Lens summary

1. **Doctrine/charter**: clean — one budgeted pod, no state outside CR status + directory KV (A1 carefully preserves this by putting spend in design 04's index), primitives honest.
2. **Rollout state machine** (requested): findings 2, 3, 7. The core decision — operator-owned gateway-weighted blue-green rather than k8s rolling update — is correct and is what makes ADR-0006 possible at all; the gaps are all *concurrent* or *guarded* states the original happy-path spec never modeled. Note the machine handles the sequences it *did* model well: content-addressed revisions make operator crash-resume trivially safe (§5), and the SVID-bound candidate route (review 02 f2) closed the isolation hole properly.
3. **Card/registration** (requested): finding 9; the central decision — card SoT is the container, digest in status, drift triggers re-registration and never a new revision — is right, and the r1 blocker fix (hash from spec only) removed the circularity cleanly. A6 layers signature verification on without contradiction.
4. **Amendment coherence** (requested): finding 8 is the systematic answer — **no amendment contradicts another**, and A1's care about the no-extra-state invariant is exemplary; the failure is that eight amendments accumulated with zero body integration, so §3 is now a partly-false document that §11 silently corrects. Finding 1 is the sharper version: an amendment (A8) that presupposes a field the body never defined.
5. **Materialization honesty** (requested): findings 4 and 5. The Deployment/Sandbox split and the admission exclusivity rule are right; the honesty gap is that both stateful paths (Sandbox scratchpad, multi-replica task state) fail *silently* in a platform whose entire doctrine is loud degradation.
6. **Security**: strong — admission (cosign, prod gates, sandbox/replica exclusivity, public-expose approval), default-deny egress, operator-only directory writes, external secrets never in spec. Finding 6 is the gap: an undefined delete path is a security question too (a revoked agent whose routes outlive it).
7. **Research freshness**: agent-sandbox v1beta1, ClusterSPIFFEID + spiffe-csi, and the kgateway/agentgateway split were all verified in the original pass and still hold; only the 2.2 pin has drifted (finding 8).
8. **Testability**: envtest for revision math + condition transitions and a fake gate controller are the right harness. After these findings it needs: the spec-change-during-canary sequence, a Sandbox-revision scratchpad assertion, a `replicas>1`-without-task-store detection test, finalization ordering, and rollback-target-retention under `revisionHistoryLimit`.

## Disposition

**REVISE.** The load-bearing decisions have aged well — operator-owned revisions, card-as-code with a spec-only hash, one templated ClusterSPIFFEID, admission-enforced signing and gating — and nothing downstream had to fight them, which is the real test. But this design was written first and never re-opened: eight amendments sit in an appendix while §3 still tells the old story, one of them (A8) presupposes a CRD field that was never added even though three designs compile against it, and the state machine was specified for a single well-behaved candidate at a time. Findings 1–7 are all things the first implementer hits in week one: a missing field, a second candidate, a GC'd rollback target, a wiped scratchpad, a silently broken multi-replica agent, an undefined delete, and a phase that can't say why traffic stopped.

02: VERDICT REVISE — 10 findings
