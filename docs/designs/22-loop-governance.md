# Design 22: Loop governance (hops, cycles, approvals, kill switch)

- **Status**: **approved** — critique PASS at r2 (reviews/22-review.md) · ADR-0025
- **Phase**: P4 · **Size**: M · **Date**: 2026-08-20
- **ADRs**: 0020 (reserved mappings now filled), 0021 (lineage in receipts) · interfaces: 03 (emits everything here), 06 (per-hop re-exchange pairs with lineage), 21 (workflows in the lineage), 08 (`assayd approve`), architecture §14 (the governance ring, made mechanical)
- **Research**: agentgateway OSS: in-proxy **CEL** over request context (compiled at config load), Envoy-compatible ext-authz for external decisions — `docs/research/authz-2026-08.md`

## 1. Purpose & scope

The governance ring's mechanics (FR-33): multi-agent **cyclic** runaway stopped at the data plane (volumetric runaway is budgets' job — §3), approvals as durable pauses, the kill switch. Everything here is **emitted by design 03** and enforced by the gateway — agents need nothing, which is the point. Out of scope: budgets (ADR-0028, shipped), ReBAC (24).

## 2. Doctrine & charter gates

- **Plane**: slow (compiled policy + one interceptor mode); approval *policies* are CR data. **Pods**: 0 — the approval interceptor is an operator-binary listener mode (control-plane concern, gateway-authenticated peer — the tap pattern's *trusted* variant, unlike design 11's internet-facing receiver). **Stateful deps**: approvals in a **per-tenant `APPROVALS` KV bucket in the tenant account, created by the 07 bootstrap job** (r1 f3 — the ADR-0013 words). ✓
- **Primitives**: Resource, Event (`approval.requested/decided`), Tool (the intercepted calls). ✓

## 3. Lineage: stateless enforcement in-proxy (the trick)

The gateway stamps `X-Assayd-Lineage: <task_id>;a=agentA@rev,workflow:pa-check,agentB@rev` — appended per A2A/workflow hop by a 03-emitted transform (the header is gateway-owned: inbound client values stripped, same rule as the scope header). Enforcement is **in-proxy CEL** (compiled at config load — no external call, no state):

- **maxHops**: `lineage.split(',').size() > agent.maxHops` ⇒ deny `LOOP_DEPTH_EXCEEDED` (the receipt's `hop.status: denied` carries it; the caller gets a typed A2A error).
- **Cycle (r1 f1 — one semantics)**: default = **any revisit denied** (`target in lineage.entries` — the conservative rule the expression implements; A→B→C→A is caught). Opt-in reentry = **occurrence counting**: `loop: {allowReentry: true, maxVisits: 2}` compiles to `lineage.count(target) < maxVisits` — CEL-countable, stateless, bounded. Prose, expression, and knob now tell one story.
- Workflows appear in the lineage as `workflow:<name>` entries (21 §5) — a workflow↔agent ping-pong is caught by the same rule.

Why stateless matters: no cycle-detection service, no shared state, no new pod — the lineage *is* the state, carried by the request, tamper-proofed by gateway ownership of the header. Receipts already record `lineage` (ADR-0021), so every denial is auditable with its full path.

**What this does NOT bound — stated because the omission is easy to miss (A1).** The lineage header describes the **ancestor chain of one request**, so every rule above bounds *ancestral* revisits and nothing else. Agent call graphs are trees with concurrency, not paths: two sibling branches that each legitimately call agent X once produce two lineages each containing X once, and no stateless check can see that X ran twice. A breadth-2, depth-6 fan-out whose leaves each re-enter one agent stays inside `maxHops` **and** inside any `maxVisits` occurrence budget while invoking that agent **64 times**.

So this design bounds **cyclic** runaway. **Volumetric** runaway is bounded only by the budgets of ADR-0028 — which §1 puts out of scope, and which a fan-out storm can exhaust in seconds. Covering fan-out would require counting invocations across concurrent siblings, i.e. shared state, forfeiting the property that makes this design cost 0 pods; the deliberate choice is to leave it to budgets and say so. `assayd doctor`'s lineage self-test therefore proves cycle enforcement, never cost enforcement. The prior art for the mechanism is [RFC 8586 `CDN-Loop`](https://www.rfc-editor.org/rfc/rfc8586.html) and BGP `allowas-in`, both of which share exactly this limitation for exactly this reason.

## 4. Approvals: durable pause, retry-shaped

`requiresApproval: true` tools (Agent CR / workflow `approval` steps) route — via a 03-emitted route — to the **approval interceptor** (operator listener mode):

1. First call → interceptor writes a durable `ApprovalRequest` (KV: requester chain, tool, args digest, TTL from policy) → emits `approval.requested` (notification surface: CLI, later App UI) → returns MCP error `APPROVAL_PENDING {approval_id, retry_after}` — **a retryable, typed pending**, not a hang (stateless-HTTP-friendly; MCP 2026-07-28's MRTR `input_required` is the recorded v2 upgrade path for clients that speak it).
2. `assayd approve <id>` (authz: the `approvers` role via 24 when present; namespace RBAC in core) records the decision + decider.
3. The caller's retry: approved ⇒ the interceptor returns a **single-use pass voucher** consumed by a gateway CEL check, and **the gateway forwards the retry to the real backend** — the operator binary stays out of the tool-call data path entirely (r1 f4; the proxy alternative rejected). Denied ⇒ `APPROVAL_DENIED`, terminal. Timeout ⇒ `approval_timeout` (21's branchable failure).

**Typed-pending retry joins the design-09 template contract** (r1 f2 — a 09 note + pack release; honoring `retry_after`, bounded by `taskTimeout`), so the flagship path handles approvals by construction. Honest limitation, now correctly scoped: **a true black-box agent that treats `APPROVAL_PENDING` as a hard failure will fail the task** — the approval still protected the action (fail-closed); the DX cost lands on non-template agents and is documented, not hidden. Workflow `approval` steps don't have this problem (the interpreter waits durably — 21).

## 5. Kill switch

`assayd agent kill <name>` (and drift-controller escalations): weight-0 all revisions + candidate-route revoked + optional `--scale-zero`; in-flight tasks get `taskTimeout` to drain, then 503. Kill is a **guard state** in the rollout machine (02 A1's family): `Killed=True` condition, exit only by explicit `assayd agent revive` — never by reconcile drift. Receipted under the invoking principal.

## 6. Failure modes

| Failure | Behavior |
|---|---|
| Lineage header absent (first hop) | Gateway initializes it — absence past the first hop is a policy violation ⇒ deny (tamper evidence) |
| Interceptor down | `requiresApproval` calls fail closed (`APPROVAL_UNAVAILABLE`); unguarded tools unaffected; condition on affected Agents |
| Approval KV entry lost | Retry recreates the request (args digest keys it — idempotent); a *decided* entry lost before consume ⇒ re-approval required (fail-closed, logged) |
| Approver never acts | TTL ⇒ `approval_timeout`; requests age visible in `assayd approve --list` |
| CEL/limits misconfigured (all traffic denied) | `PolicyApplyIncomplete`-family surfacing via 03's acceptance checks; `assayd doctor` runs a lineage self-test through the gateway |
| Kill during canary | Kill wins over every rollout state; gate controller observes and abandons cleanly |

## 7. Observability

Denial counters by code (`LOOP_DEPTH_EXCEEDED`, `LOOP_CYCLE`) — a *rising* cycle-denial rate is itself a design smell surfaced as a ticket alert; approval latency/age histograms; kill/revive events. `loop_depth` p95 (10's signal) pairs with the limit for tuning.

## 8. Testing

CEL fixtures: depth/cycle/reentry-opt-in matrices; header-tamper test (client-supplied lineage stripped); approval lifecycle e2e (pending → approve → single-use pass → replay denied), timeout, deny, interceptor-down fail-closed; kill/drain/revive drill incl. mid-canary; doctor lineage self-test.

## 9. Decisions for async review

- **D1 — Lineage enforcement is stateless in-proxy CEL** over a gateway-owned header; no cycle service exists.
- **D2 — Reentry is opt-in and bounded** (`allowReentry`/`maxVisits` on the CR), never default. Bounded **along the ancestor chain**; concurrent sibling fan-out is bounded by budgets, not here (§3).
- **D3 — Approvals are retry-shaped typed pendings** (MRTR as the recorded v2); black-box-agent limitation documented, protection fail-closed regardless.
- **D4 — Kill is a sticky guard state** exited only by explicit revive.

## 10. Resulting ADRs

ADR-0025 (P4) after critique PASS.

## Amendments

A1 (2026-08-22) — **the fan-out gap named.** An adversarial prior-art scan of the lineage mechanism (`docs/research/paper/02-data-plane-governance.md`) established two things. First, the mechanism is [RFC 8586 `CDN-Loop`](https://www.rfc-editor.org/rfc/rfc8586.html) transposed to agent calls, with header-stripping being ordinary Envoy doctrine and occurrence-counted opt-in reentry being BGP `allowas-in`; the design is well-precedented, which is ADR-0003 working, and should not be described as invented. Second — the substantive finding — a lineage header describes only an **ancestor chain**, so §3's rules never see concurrent sibling fan-out. §1's "multi-agent runaway stopped at the data plane" and §3's "the lineage *is* the state" were true for the ancestor relation and false for volume. Both are now scoped, and §3 carries the uncovered case explicitly rather than leaving a reader to infer coverage that does not exist.
