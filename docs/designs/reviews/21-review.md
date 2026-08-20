# Review: Design 21 — workflow-operator + Workflow CRD

- **Verdict**: **REVISE**
- **Reviewed**: `docs/designs/21-workflow-operator.md` (draft, 2026-08-20)
- **Independence note**: reviewed in an independent session that did not author the draft.
- **Method**: all 8 lenses, with the requested focus: the interpreter model vs DBOS semantics, the +2 pod change vs design 07's ledger gate, and trigger idempotency vs design 11.

## Findings

### 1. MAJOR — one shared runtime pod cannot present per-workflow identities, so the claimed per-workflow budgets and authz have nothing to bite on

`21-workflow-operator.md:14,51-52` — the design 16 r1 f1 lesson, resurfacing. §5 claims each run "executes under a workflow principal (`workflow:<name>@<run>`; SVID via runtime pod + run-scoped attribution in receipts)" — but the `workflow-runtime` Deployment is **one shared pod set for every declarative workflow**, and SPIFFE identity is pod-attested: every workflow's traffic reaches the gateway wearing the *same* runtime SVID. Receipt attribution via labels is fine for audit, but the gateway can't enforce **the CR's own budget** (§3) or design 24's `workflow can_trigger/can_call` checks on a principal it cannot distinguish — per-workflow rate limits keyed on a shared SVID are one shared bucket, and a header claiming the workflow name would be exactly the client-supplied assertion the platform strips everywhere else.

**Fix**: adopt the design 06 A3 pattern — the operator provisions a per-workflow **`workflow-actor` OAuth client** (`ensureClient` gains the kind; recorded as a 06/02-style amendment); the runtime presents the workflow's client credential per request (machine-triggered) or the exchanged token (on-behalf-of, where the act chain already discriminates), and the compiler keys budgets/authz on that principal. Alternative (heavier, honest to name): per-workflow runtime replicas — rejected sensibly on weight grounds, but say so. Either way, the gateway-enforced properties this design promises must attach to a gateway-visible identity.

### 2. MINOR — run-id derivation is specified for event triggers only; cron and http runs need their own idempotency story

`21-workflow-operator.md:53` (and design 23 §3's `POST /api/<name>`). `wf-<name>-<ce-id>` is the right construction for events — and correctly catches *beyond-dedup-window* redeliveries too, which the stream alone can't. But cron runs (id from the scheduled instant, so a rescheduled/replayed tick is a no-op) and http runs (client retries of the POST — accept an `Idempotency-Key` header seeding the run id; without one, each POST is a new run, stated) are underived. **Fix**: one table — trigger kind → workflow-id derivation → retry semantics; design 23's projection forwards the idempotency header (shared item, cross-referenced in 23's review).

### 3. MINOR — the "park + operator relaunch" borrowing doesn't match a standing runtime

`21-workflow-operator.md:67`. The budget-exhaustion row borrows "the 14 §5 mechanic" — but 14's mechanic is *Job exits, operator relaunches*, which exists because batch builds have no standing pods. The runtime is standing: a parked run should simply wait durably in-runtime (DBOS durable sleep/events — the same mechanism the approval step presumably uses, which also deserves one naming sentence). **Fix**: restate the row as in-runtime durable wait until the window resets; name the DBOS primitive the approval step waits on while at it.

### 4. MINOR — the shared runtime's tenancy seam is unstated

`21-workflow-operator.md:14,74`. The runtime holds "JetStream consumer creds" — for per-tenant `EVENTS` streams in per-tenant *accounts* (11 r2/ADR-0013). One shared runtime crossing tenant account boundaries holds every tenant's credentials — fine at core's n=1, but the design 26 seam should be named now (per-tenant runtime scaling or per-tenant credential isolation), the way 04 D4 and 18 §5 did. **Fix**: one sentence.

## Lens summary

1. **Doctrine — the +2 pod change** (requested scrutiny): handled exactly as design 07's gate demands — ledger-entered in the same PR, plus-tier, with scale-to-zero honesty for batch-only installs. The architecture always named three operators; the *runtime* pod is the genuinely new weight and its justification (standing workers are what event triggers *are*) is sound. Clean.
2. **Charter**: the closed step-kind set with no unbounded control flow in declarative v1 (D3) is the analyzability discipline applied correctly; code-first as the escape hatch *in the user's own pods* keeps the shared runtime pure.
3. **Interpreter vs DBOS semantics** (requested scrutiny): sound — the walk is deterministic given (interpreter version, graph digest, checkpointed step outputs), which is precisely DBOS's determinism contract; branch/transform decisions derive from checkpointed outputs; Deployment-per-interpreter-version during drains follows the cited guidance; runs pinning their graph digest handles skew honestly. The interpreter-over-codegen argument (workflows stay data, hot-registerable, one image) is the right call, well made. No findings.
4. **Trigger idempotency vs 11** (requested scrutiny): the event path is *better* than required — deterministic workflow ids give consumer-level dedup beyond the stream window; finding 2 is about extending the same rigor to the other two trigger kinds.
5. **Contract consistency**: findings 1/4; elsewhere strong — steps-through-the-gateway-always, workflows as first-class lineage actors (22), gating never bypassed (the Degraded-target row), DLQ never-silent.
6. **Security**: refs-only step targets + schema-validated args at admission closes the arbitrary-endpoint hole; finding 1 is the identity gap.
7. **Research freshness**: DBOS dynamic queues and the k8s guidance are consistent with the landed note; the queues-per-Workflow-at-registration use is exactly what the note's capability enables.
8. **Testability**: strong list (resume drills per step kind, dedup, act-chain e2e, DLQ, external-runtime path); add finding 1's regression once resolved (two workflows, distinct budgets, assert independent 429s).

## Disposition

**REVISE.** The interpreter decision, the closed step vocabulary, and the event-trigger idempotency are all first-rate — and the pod-budget change is a model of how design 07's gate was meant to be used. The one MAJOR is the platform's own recurring lesson (16 r1 f1) in a new coat: gateway-enforced promises need gateway-visible identities, and a shared pod has exactly one. The fix mechanism already exists (06's actor-client kind).

VERDICT: REVISE — 4 findings

---

## Re-review r2 (2026-08-20)

- **Verdict**: **PASS** (1 residual — **required before ADR-0025**: a claimed record that does not exist)
- **Independence note**: same independent session as r1; did not author the draft or revision.

### Per-finding disposition

| r1 | Severity | Disposition |
|---|---|---|
| 1 | MAJOR | **Resolved in this design; the claimed cross-design record is missing.** §5's fix is exactly right — per-workflow `workflow-actor` OAuth clients presented per request, budgets/authz keyed on that gateway-visible principal, on-behalf-of discriminated by the `act` chain, the per-workflow-pods alternative rejected on weight *and recorded*. But §5 says design 06's `ensureClient` "gains the kind — recorded as 06's amendment", and **verification shows it was not**: 06's kind list still reads `app-login | external-agent | exposed-consumer | cli | agent-actor`, and 06 §11 carries no workflow-actor entry. The series' standard is claims-match-records (17 r1 f4 was held to exactly this). Residual **R2-a**: add `workflow-actor` to 06 §3.1's kinds and one amendment line — one word plus one sentence, but it **must land before ADR-0025 is recorded**, because right now the design cites a record that doesn't exist. |
| 2 | MINOR | **Resolved.** The run-id table covers all four trigger kinds with retry semantics each — event (beyond-window included), cron (scheduled instant), http (`Idempotency-Key`, forwarded by 23's projection, sent by the generated client by default), manual (CLI always keys). |
| 3 | MINOR | **Resolved.** Budget exhaustion = in-runtime durable wait (DBOS durable sleep — the same primitive the approval step waits on, now named); the 14 Job-exit borrowing explicitly disclaimed. |
| 4 | MINOR | **Resolved.** The tenancy seam named as a core-at-n=1 statement with the design-26 decision point (per-tenant replicas vs credential isolation) — the 04 D4 precedent, correctly reused. |

### Verdict

**PASS**, conditioned on R2-a. The substantive design is complete and correct — identity, idempotency, park semantics, tenancy all resolved with the right mechanisms. The one open item is purely a recording gap, but it is the kind this review series exists to police: fix design 06 before ADR-0025 turns the claim into an accepted decision.
