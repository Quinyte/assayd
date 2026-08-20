# Design 24: AuthzProvider — ReBAC via OpenFGA (`authz/v1alpha1`)

- **Status**: revised r2 — awaiting re-critique (r1 REVISE, 4 findings addressed; reviews/24-review.md)
- **Phase**: P4 · **Size**: M · **Date**: 2026-08-20
- **ADRs**: 0011 (three authz layers; this is layer 2) · interfaces: 03 (ext-authz wiring), 06 (`act` chains; **workflow-actor identity per 21 r2** — its budget/authz bite depends on that fix, r1 f4), 22 (approvers roles via contextual tuples), 02/11 (tuple sources), 17 (decision replay), 26 (per-tenant stores)
- **Research**: `docs/research/authz-2026-08.md` — agentgateway OSS supports **Envoy-compatible ext-authz gRPC** (CheckRequest → OK/Denied + dynamic_metadata usable in in-proxy CEL); OPA/Cerbos precedents for gateway-governed agents; OpenFGA = CNCF Zanzibar-style ReBAC (ADR-0011 grounding).

## 1. Purpose & scope

Layer-2 authorization made real: relationship checks at the gateway per hop, with delegation chains as first-class subjects. Defines the `authz/v1alpha1` slot (OpenFGA first, SpiceDB-shaped alternatives behind it), the FGA model, tuple lifecycle, the ext-authz adapter, and the tier story. Out of scope: build-time RBAC/CEL admission (layer 1, shipped across designs), policy-as-config (layer 3, CR fields).

## 2. Doctrine & charter gates

- **Plane**: slow seam (adapter + tuple reconciler), fast content (the authorization *model* is versioned data). **Pods**: the budgeted plus-tier OpenFGA pod — the **ext-authz adapter runs as a second container in the same pod** (localhost check latency, zero extra pods). **Stateful deps**: OpenFGA on the platform Postgres (own database — the Zitadel pattern). ✓
- **Primitives**: Resource (Grant CR), Event (`grant.changed`). ✓ AuthzProvider joins the shipped-slot list (architecture erratum, the 16 precedent).

## 3. The authorization model (FGA store, versioned artifact)

```
type user
type agent
  relations: define can_invoke: [user, agent, workflow]
type workflow
  relations: define can_trigger: [user, agent]
type tool
  relations: define can_call: [agent, workflow]
type graph
  relations: define can_query: [agent, workflow, app]
type app
  relations: define member: [user] / admin: [user]
type role            # approvers etc. (22): define assignee: [user]
```

Deliberately **coarse**: objects are CRs (agents, tools, graphs), not rows — entity-type scope stays compiled in the gateway header (design 01 A1/03), where it already works; ReBAC answers *who may reach which object*, not *which entities inside it* (the split is stated so nobody re-litigates it per feature).

**Chain semantics (r1 f2)**: delegated (`act`-bearing) traffic checks **every link** — each link is attested by the exchange chain (06); **pure machine hops check the immediate link only** (`caller can_invoke/can_call target`, subject = the verified SVID) — lineage entries are governance telemetry, never authz subjects (unattested). **Delegation checks**: a hop with `act` chain `user:jag → agent:pa-intake → agent:pa-reviewer` calling `tool:claims-system` requires **every link**: `check(user can_invoke pa-intake)` ∧ `check(pa-intake can_invoke pa-reviewer)` ∧ `check(pa-reviewer can_call claims-system)` — the chain is checked, not just the tip; revoking the *user's* access kills the whole delegated action immediately (ADR-0011's promise, mechanical).

## 4. Tuple lifecycle — CRs are the source of truth

Two producers, no third:

1. **Operator-reconciled tuples** (owned, rebuildable — CR-resident facts only): `Agent.tools` → `can_call`; `expose` consumers → `can_invoke`; Workflow triggers → `can_trigger`; App role *definitions*. **User↔role/member relations are NOT tuples** (r1 f1 — group claims live in tokens, not CRs, and core has no directory sync): they resolve as **contextual tuples at check time** — the ext-authz adapter passes the verified JWT's group/role claims into the FGA check request (OpenFGA-native contextual tuples; research note updated) — no sync, no staleness, revocation rides token lifetime (already the stated number). Named-user exceptions outside IdP groups remain Grant CRs. `plume authz rebuild` reconstructs exactly the CR-derived set.
2. **Grant CRs** (ad-hoc, auditable): `kind: Grant {subject, relation, object, expiry?}` — GitOps-reviewed, receipted, expirable. **No hand-written tuples, ever** — the FGA API's write surface is operator-only (network-policied), so the audit story stays whole.

## 5. The ext-authz adapter

Envoy-compatible `Check` gRPC (agentgateway OSS capability, cited): maps (SVID / JWT `act` chain, `Mcp-Name`, route target CR) → the §3 check set → OK/Denied (+ `dynamic_metadata: {checked_chain}` for downstream CEL/receipts). Engineering: checks batched per request (FGA `batch-check`); decision cache **TTL 2s** keyed by (chain, object) — bounded staleness for revocation (stated: revoke latency ≤ cache TTL + token lifetime, the honest number); p95 budget 10ms in-cluster (localhost FGA). Attached per-route by a 03-emitted policy — **only routes with ReBAC-relevant targets** get the ext-authz hop (health checks and static routes don't pay it).

## 6. Tiers, failure, migration

- **Plus tier** (with OpenFGA). **Core tier**: layer 2 absent — compiled policy (layers 1+3) is the whole story, surfaced as `ReBACAvailable=False` at install (the `GatesSkipped` honesty pattern; no silent difference between tiers).
- **FGA/adapter down**: routes carrying the ext-authz policy **fail closed** (403 + `AuthzUnavailable` condition); operators may pre-declare `authz: {failOpen: never}` — there is no fail-open knob, and that's a decision, not an omission.
- Enabling ReBAC on a running install: `plume authz enable` → rebuild tuples from CRs → shadow mode (checks evaluated + logged, not enforced — `wouldDeny` metric) → enforce. Shadow-first is mandatory (the design-20 "learn before you act" pattern applied to authz).

## 7. Failure modes

| Failure | Behavior |
|---|---|
| FGA down | Fail closed on ReBAC routes; `AuthzUnavailable`; page |
| Tuple drift (CR edited, reconcile lagging) | `AuthzDrift` condition + rebuild verb; checks use current tuples (bounded staleness, stated) |
| Grant expiry | Tuple removed at expiry sweep (1m resolution); receipts show the last allowed use |
| Model version bump | Versioned migrations; **decision replay**: recorded check inputs (the checked-chain metadata receipts already carry) re-evaluated against the new model via 17's read path — no traffic driven (r1 f3); verdict diff gates enforce |
| Cache staleness on revoke | Bounded: TTL 2s + token lifetime; documented as *the* revocation latency number |
| Shadow mode forgotten | `wouldDeny > 0` for 7d ⇒ ticket ("enforce or explain") |

## 8. Security

Adapter/FGA reachable only from the gateway (NetworkPolicy); FGA writes only from the operator + Grant reconciler; every decision's checked chain lands in receipt metadata (denials are auditable with *why*); Grant CRs are the only ad-hoc path and carry expiry pressure (unbounded grants warn).

## 9. Testing

Model fixtures: the delegation-chain matrix (every-link vs broken-link); revocation latency test (revoke → deny within TTL+lifetime); rebuild determinism (CRs ⇒ identical tuple set); shadow-mode parity (wouldDeny == enforced denials on replayed traffic); adapter conformance against a mock FGA (the `authz/v1alpha1` slot's suite — SpiceDB adapter must pass identically); fail-closed drill.

## 10. Decisions for async review

- **D1 — Coarse ReBAC objects (CRs), entity-scope stays compiled** — the two mechanisms don't overlap.
- **D2 — Every link of the `act` chain is checked**, not the tip.
- **D3 — FGA materializes CR-derived facts + Grant CRs; user-context resolves per-check via contextual tuples; no hand-written tuples; CR-set rebuildable.**
- **D4 — No fail-open knob exists.** Shadow-first enablement is mandatory.
- **D5 — Adapter co-located in the FGA pod** (0 extra pods, localhost latency).

## 11. Resulting ADRs

ADR-0025 (P4) after critique PASS.
