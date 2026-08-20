# Design 06: Identity glue (SPIRE stack + IdP bootstrap + token exchange)

- **Status**: **approved** — critique PASS at r2 (reviews/06-review.md) · ADR-0022
- **Phase**: P1 · **Size**: M · **Date**: 2026-08-20
- **ADRs**: 0010 (IdP slot), 0011 (authz layers), 0013 (orgs↔tenants) · interfaces: 02 (labels/SVIDs, OAuth clients), 03 (JWT policy, exchange enforcement), 04 (principal chain), 08 (CLI login)
- **Research**: `docs/research/identity-2026-08.md` — Zitadel RFC 8693 (v2.49+, audience-narrowing) **and the r1-f1 verdict: agentgateway token exchange is OSS** (open-sourced post-2.2; `oauthTokenExchange` on AgentgatewayPolicy with `actorToken`, `audiences`, `cache`; IdP-agnostic incl. Zitadel/Keycloak). The chart pins an agentgateway version with OSS exchange (design 07).

## 1. Purpose & scope

Everything that makes "who is acting, for whom" true: the SPIRE stack (workload identity), the IdP bootstrap (human + external identity), and the **on-behalf-of token exchange** that turns a user's login into agent-scoped credentials with an auditable chain. Defines the **IdentityProvider profile interface** (the ADR-0010 slot). Out of scope: ReBAC decisions (design 24), gateway policy emission (03 — consumes what's provisioned here).

## 2. Doctrine & charter gates

- **Plane**: slow. **Pods**: SPIRE 2 + Zitadel 1 — already in the §17 budget; the glue itself is a bootstrap **Job** + operator code (0 standing pods).
- **Stateful deps**: Zitadel runs on the existing Postgres (own database, same server). ✓
- **Primitives/socket**: IdentityProvider is a provider slot with a versioned profile interface (`idp/v1alpha1`). ✓

## 3. Interfaces

### 3.1 The `idp/v1alpha1` profile interface (the slot)

What any IdP profile must provide; Zitadel and Keycloak adapters implement it:

```
IdPProfile {
  discovery():        OIDC discovery URL + JWKS           // gateway JWT policy input (design 03)
  ensureTenant(t):    org/realm for tenant t              // Zitadel: organization; Keycloak: realm
  ensureClient(spec): OAuth client {id, secretRef}        // kinds: app-login | external-agent | exposed-consumer | cli | agent-actor (r1 f2)
  ensureServiceAccount(name): machine identity + key      // operator, gate-controller, tap
  tokenExchange():    capability flag + endpoint          // RFC 8693 support; REQUIRED for on-behalf-of
  scim?():            optional directory-sync endpoint    // enterprise
  capabilities:       {provisioning: bool, tokenExchange: bool, …}   // byo profiles may set provisioning=false (r1 f7):
}                                                          // then ensure* run in verify-exists mode against pre-provisioned objects
```

Profiles declare capabilities; a profile without `tokenExchange` hard-fails install when any Agent uses on-behalf-of (fail-closed, not degraded — see §5).

### 3.2 Bootstrap (idempotent Job, chart hook)

Per install (and per tenant at design-26 time): **first-boot credential = the IdP chart's init machine-key Secret** (Zitadel supports declarative machine-key provisioning; Keycloak profile uses the realm-import admin flow) — the bootstrap Job consumes it and never persists elevated creds elsewhere (r1 f8). Roster: service accounts for **agent-operator and gate-controller only** (the receipt tap authenticates by SVID, not IdP — r1 f8), plus standing clients (CLI device-code client; App login clients per App CR — provisioned by the operator at reconcile, not at bootstrap). All secrets land in k8s Secrets (ESO-compatible); nothing in CRs. Re-runs converge; deletions are never automatic (identity objects outlive CRs deliberately — finalizers only deactivate).

### 3.3 On-behalf-of: the token exchange flow

```
user ──login(OIDC)──> App frontend ──user JWT──> gateway
gateway ──RFC 8693 exchange (subject=user token, actor=agent client)──> IdP
      <── agent-scoped token: sub=user, act={agent}, aud=narrowed to the tools/KG the agent may reach
gateway ──calls tools/KG with exchanged token──> backends
```

- The **gateway performs the exchange** — verified OSS (r1 f1): the compiler emits an `-exchange` concern policy (`oauthTokenExchange`: subject = inbound user JWT, **actorToken = the agent's own `agent-actor` OAuth client credential** (r1 f2 — provisioned by the operator at reconcile via `ensureClient(agent-actor)`, recorded as design 02 §11 A3), `audiences` = the compiled per-agent backend set, `cache` enabled with TTL ≤ exchanged-token lifetime — the caching story for the per-hop IdP dependency, r1 f4). Design 03 §3.4 gains the row. Agents never hold user tokens.
- **Audience narrowing is the safety rail**: Zitadel enforces that exchanged tokens never gain audiences absent from the inputs — the compiled `aud` set per agent is exactly its tool/KG backends.
- **`principal_chain` in receipts (design 04) is read from the `act` claim chain** — one source of truth for "who acted for whom".
- Agent↔agent (A2A) hops re-exchange per hop with **each hop's own agent-actor client** — the `act` chain names real agents, which is what design 04's `principal_chain` records; depth bounded by `maxHops` (design 22). Exchange cache keeps the IdP off the per-request hot path; sustained IdP outage ⇒ cached tokens serve until expiry, then fail closed (§5).
- SPIFFE and OAuth compose: workload authN is mTLS/SVID (transport); user-delegation is the exchanged JWT (request) — both checked at the gateway.

### 3.4 CLI login (design 08 consumer)

`plume login` = OIDC device-code flow against the CLI client; tokens cached in the OS keychain; `plume --as user@…` (impersonation for admins) maps to exchange with an impersonation grant — audited like everything else.

## 4. Behavior

Operator reconciles identity-touching CR fields through the profile interface (e.g. App CR ⇒ `ensureClient(app-login)`; external Agent ⇒ `ensureClient(external-agent)`). Profile adapters are idempotent, retryable, and record provisioned IDs in CR status (never secrets).

## 5. Failure modes & degraded states

| Failure | Behavior |
|---|---|
| IdP unavailable | Workload mTLS (SVID) paths unaffected — agent↔tool machine traffic continues; **human-auth and exchange paths fail closed** (401, `IdPUnavailable` condition on affected Apps/Agents). Never fall back to forwarding raw user tokens |
| Token exchange unsupported (profile capability false) | Install-time hard fail if any on-behalf-of usage exists; otherwise `OnBehalfOfUnavailable` condition, agents run with machine identity only |
| Bootstrap partial | Job idempotent; convergence on rerun; `IdentityBootstrapIncomplete` condition on the install |
| JWKS rotation | Gateway JWT policy uses discovery/JWKS with caching + rotation grace (webkeys); no manual key handling |
| Secret drift (client secret rotated in IdP) | Adapter detects on reconcile → updates k8s Secret → dependent policies re-applied (03 ordering) |
| Zitadel schema migration (its own DB on shared Postgres) | Runs in its pod lifecycle; platform Postgres unaffected (separate database); noted in runbook |

## 6. Security

Least privilege: per-component service accounts; exchange performed only at the gateway (agents never see user tokens); audience narrowing enforced by the IdP; impersonation grants restricted to admin role + fully receipted. Secrets only in k8s Secrets; adapters authenticate to IdP management APIs with the operator's service-account key (rotatable). AGPL note (ADR-0010): Zitadel used unmodified.

## 7. Observability

Bootstrap Job status; exchange latency/error metrics at the gateway; `IdPUnavailable`/`OnBehalfOfUnavailable`/`IdentityBootstrapIncomplete` conditions; audit: every provisioning action → k8s event + receipt (management calls go through the gateway too where feasible).

## 8. Testing

Profile-interface conformance fixtures run against **both** adapters (Zitadel testcontainer; **Keycloak testcontainer with the token-exchange feature flag enabled and pinned** — exchange is a non-default feature there; the profile enables it and conformance asserts it, r1 f3). e2e (k3d): login → App → agent → tool with correct `act` chain in receipts; IdP-down drill (machine paths alive, human paths 401 + condition); exchange audience-narrowing negative test (agent must not reach a backend outside its compiled `aud`).

## 9. Decisions for async review

- **D1 — Exchange at the gateway, never in agents**; agents never hold user tokens.
- **D2 — Profile capability flags with fail-closed install** when on-behalf-of is used without exchange support.
- **D3 — Identity objects are never auto-deleted** (deactivate on finalize); auditability beats tidiness.

## 10. Resulting ADRs

Folded into ADR-0022 (P1 infrastructure) after critique PASS.

## 11. Amendment notes recorded elsewhere (r1 f6/f9)

Design 02 §11 A3 records: `agent-actor` client provisioning at reconcile + new conditions `IdPUnavailable`, `OnBehalfOfUnavailable`, `IdentityBootstrapIncomplete`. Architecture §15's reserved-slot list gains `IdentityProvider`/`AuthzProvider` (erratum fixed in architecture.md/html).

## 12. Amendments

- **A1 (2026-08-20, from design 26 r1 f2)**: hard-mode tenancy runs a **SPIRE server per vCluster, federated to the host trust domain** (`ClusterFederatedTrustDomain` — spire-controller-manager's existing CRD); the host gateway validates federated SVIDs. vCluster's host-namespace workload syncing makes a single shared SPIRE unable to express per-tenant identity segments.
