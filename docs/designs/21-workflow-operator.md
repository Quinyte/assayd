# Design 21: workflow-operator + Workflow CRD

- **Status**: draft — awaiting critique
- **Phase**: P4 · **Size**: M · **Date**: 2026-08-20
- **ADRs**: 0004 (DBOS, three jobs), 0009 (events actionable from P4) · interfaces: 11 (EVENTS stream triggers), 03 (expose/principal routes), 06 (workflow principals/on-behalf-of), 22 (approval step), 23 (HTTP projection), 14 (precedent: DBOS-in-Job)
- **Research**: `docs/research/connectors-2026-08.md` §DBOS (dynamic queues; k8s Deployment-per-version guidance; each replica an independent worker)

## 1. Purpose & scope

Makes `Workflow` real (FR-50): declarative CRs compiled to durable programs, code-first DBOS apps registered as first-class citizens, and JetStream events finally *actionable* (closing design 11's P2→P4 gap). In scope: the CRD, the interpreter-vs-codegen decision, the runtime topology, triggers, step semantics, the code-first path. Out of scope: approval mechanics (22 owns; consumed as a step), HTTP/MCP exposure compilation (03/23).

## 2. Doctrine & charter gates

- **Plane**: slow — this is the second of the architecture's three operators. **Pods (budget change, justified)**: `workflow-operator` (Go controller) + `workflow-runtime` (Python DBOS interpreter Deployment, the standing workers event triggers require) = **+2 pods, plus tier**, entered in `weight-budget.yaml` in the same PR (design 07's mechanical gate — this is the justification). Batch-only installs (no event triggers, no standing workflows) may run runtime at 0 replicas; the operator scales it 0→N on first trigger registration (scale-to-zero honesty).
- **Stateful deps**: Postgres (DBOS — existing), JetStream (triggers — existing). ✓ **Primitives**: Resource, Event (CloudEvents triggers), Agent (steps call agents via A2A), Tool (steps call MCP). ✓

## 3. The Workflow CRD

```yaml
kind: Workflow
metadata: {name: prior-auth-check, namespace: claims}
spec:
  runtime: declarative                  # declarative | external (code-first, §6)
  input:
    schema: {claim_id: str, amount: float}     # typed — the 23 client-gen + validation source
  triggers:
    - event: {stream: EVENTS, ceType: "com.acme.claim.updated"}   # 11's consumers, at last
    - cron: "0 6 * * *"
    - http: {}                          # exposed per §05 arch (POST /api/prior-auth-check via 03/23)
  steps:                                # v1 step kinds — CLOSED set
    - {id: fetch,   tool:  {mcpRef: claims-system, name: read_claim, args: {...}}}
    - {id: review,  agent: {agentRef: pa-reviewer, task: {...}}}
    - {id: gatecheck, branch: {on: "review.outcome", cases: {approve: [notify], deny: [escalate]}}}
    - {id: escalate, approval: {approvers: role:reviewer, timeout: 48h}}     # design 22's interceptor
    - {id: notify,  event: {ceType: "com.acme.pa.decided", data: {...}}}
  onFailure: {retries: 3, backoff: exponential, deadLetter: true}
  budget: {tokensPerRun: 100k, usdPerRun: 2}
status:
  conditions: [Registered, TriggersBound, RuntimeReady]
  runs: {active: n, lastOutcome: …}
```

Step kinds v1 (**closed**, like every plume vocabulary): `tool · agent · branch · approval · event · transform` (pure data mapping, CEL-expressions over prior step outputs). No loops in declarative v1 (bounded fan-out via `forEach: {over, limit}` only) — unbounded control flow belongs in code-first; stated to keep declarative workflows *analyzable* (the CLI can render them; budgets are boundable).

## 4. Interpreter, not codegen (the decision)

The declarative compiler produces a **step-graph document** (content-addressed), and the `workflow-runtime` runs a **generic DBOS interpreter**: one workflow function that walks the graph, each step a DBOS step (checkpointed), queues via DBOS dynamic queues (created per Workflow at registration — the research-cited runtime capability). Why not codegen: generated code is a second artifact to version/build/deploy per CR change; the interpreter keeps workflows pure data (fast-plane), hot-registerable, and one image. Determinism: the interpreter is versioned; a run pins (interpreter version, graph digest) — replayable and auditable like everything else. Runtime upgrades follow DBOS's k8s guidance (Deployment per interpreter version during drains; new runs to the new Service selector).

## 5. Execution semantics

- **Identity**: each run executes under a workflow principal (`workflow:<name>@<run>`; SVID via runtime pod + run-scoped attribution in receipts). Triggered-by-a-user (http with OIDC) runs carry the on-behalf-of chain (06) — a workflow calling an agent propagates the user's `act` chain exactly like agent→agent hops.
- **Steps through the gateway, always**: agent steps are A2A tasks; tool steps are MCP calls; both receipted, budgeted (the CR's own budget), governed (22's hop/lineage rules count workflow hops — a workflow is a first-class actor in the lineage).
- **Event triggers**: durable JetStream consumers per trigger (created by the operator on the tenant's EVENTS stream); CloudEvents `id` dedup means a redelivered event starts **no** second run (DBOS workflow id = `wf-<name>-<ce-id>` — idempotent by construction, the ADR-0021 pattern once more).
- **`deadLetter`**: exhausted runs park in a DLQ subject + `plume workflow dlq` verbs; never silent loss.

## 6. Code-first (`runtime: external`)

A user's own DBOS app (any language DBOS supports), deployed as *their* workload Deployment, registered by a Workflow CR pointing at its queue name: the operator wires triggers → **enqueue into the app's DBOS queue** (dynamic queues make this registration-time, no redeploy) and compiles the same principal/budget/expose posture. The platform never runs user code in the shared interpreter; the shared runtime is for declarative graphs only. `plume workflow test --event fixture.json` drives either path locally (dev cluster).

## 7. Failure modes

| Failure | Behavior |
|---|---|
| Runtime pod death mid-run | DBOS resume from checkpoint (the 14 drill, same machinery) |
| Trigger consumer lag/failure | `TriggersBound=False` + lag metric; events retained on the stream (11's retention) — nothing lost, visibly delayed |
| Step target Degraded (agent weight-0) | Step fails per `onFailure` policy; run parks or dead-letters; workflow never bypasses agent gating |
| Budget exhausted mid-run | Halt + park (`budget`); operator relaunch after window (the 14 §5 mechanic, shared) |
| Approval timeout | Step fails with `approval_timeout`; branch may handle it explicitly (the SOP-shaped honest path) |
| Interpreter/graph version skew | Runs pin their graph digest; in-flight runs finish on their pinned graph; new runs use the new graph |
| Redelivered trigger event | No-op (idempotent workflow id) — counted |

## 8. Security

Runtime pod holds only: DBOS Postgres role (restricted, per the research note's role split), JetStream consumer creds, gateway client identity. Step args validated against `input.schema` + step schemas at admission (a workflow cannot smuggle arbitrary endpoints — tools/agents are refs, resolved through the same CR machinery as everything else). External runtimes are workload pods under the standard default-deny posture.

## 9. Testing

Interpreter golden runs (graph fixtures ⇒ step traces, byte-stable); resume drills at each step kind; trigger dedup (same event twice ⇒ one run); on-behalf-of propagation e2e (user → http trigger → workflow → agent → tool, `act` chain complete in receipts); DLQ path; budget park/relaunch; external-runtime registration + enqueue.

## 10. Decisions for async review

- **D1 — Interpreter over codegen**: workflows stay data; one runtime image; runs pin (interpreter version, graph digest).
- **D2 — +2 plus-tier pods**, ledger-entered; runtime scales 0→N with trigger registrations.
- **D3 — Declarative v1 has no unbounded control flow** (closed step kinds, bounded `forEach`); code-first is the escape hatch, in the user's own pods.
- **D4 — Workflows are first-class governed actors**: own principals, budgets, lineage participation, receipts.

## 11. Resulting ADRs

ADR-0025 (P4) after critique PASS.
