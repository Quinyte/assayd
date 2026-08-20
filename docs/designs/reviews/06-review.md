# Review: Design 06 — Identity glue (SPIRE stack + IdP bootstrap + token exchange)

- **Verdict**: **REVISE**
- **Reviewed**: `docs/designs/06-identity-glue.md` (draft, 2026-08-20)
- **Independence note**: reviewed in an independent session that did not author the draft.
- **Method**: all 8 lenses; consistency vs architecture.md, designs 02/03/04 (+ amendments), ADRs 0010/0011/0013; Zitadel token-exchange, Keycloak feasibility, and gateway-exchange claims verified by current-year search (sources inline).

## Findings

### 1. MAJOR — the gateway-performed token exchange may be a Solo Enterprise feature, and design 03 has no row that compiles it

`06-identity-glue.md:44-54`. §3.3's entire flow rests on "the **gateway performs the exchange** (policy emitted by design 03)". Two problems, one of them potentially fatal to the flow as drawn:

- **Tiering unverified — and the signals point at enterprise.** Verified this session: agentgateway token-exchange documentation lives under **Solo Enterprise** ([2.2.x MCP token exchange](https://docs.solo.io/agentgateway/2.2.x/mcp/token-exchange/overview/), [security token exchange](https://docs.solo.io/agentgateway/latest/security/token-exchange/)); a July 2026 OSS-side post announces [token exchange, jwt-assertion, and Entra OBO](https://agentgateway.dev/blog/2026-07-12-agentgateway-token-exchange-jwt-assertion-entra-obo/); and the community [auth-patterns guide explicitly tiers patterns OSS-vs-Enterprise](https://github.com/rvennam/agentgateway-auth-patterns). Which exact exchange mode (external-IdP RFC 8693 exchange with actor tokens) is OSS in 2.2 is *not established* — and design 03 D1 (approved by re-critique) forbids depending on enterprise features. If the RFC 8693 exchange path is enterprise-tiered, §3.3 needs a redesign (ext-auth hook, an OSS exchange step, or exchange in the App/BFF seam), not a caveat.
- **No design-03 mapping row.** The exchange policy — IdP token endpoint, the actor credential presented, the compiled per-agent `aud` set — is gateway config, which by 03's own thesis only the compiler emits. 03 §3.4 has AuthN (JWT/mTLS) but nothing for exchange. Same cross-design gap class as the card route (05-review f1) — the mechanism must be a row in 03, and `docs/research/agentgateway-2.2-2026-08.md` must gain the exchange facts with its OSS/enterprise verdict.

**Fix**: verify the tiering with citations *before* anything else in this design settles; add the 03 row; if enterprise-only, present the redesign options with a recommendation (per CLAUDE.md rule 6) — this is precisely the decision the user must see.

### 2. MAJOR — the actor credential for in-cluster agents doesn't exist anywhere

`06-identity-glue.md:45-46` vs `:28-29` and design 02 §3.5. The exchange request presents `actor=agent client` — but in-cluster agents have **SVIDs, not OAuth clients** (design 02: OAuth clients are minted only for *external* agents), and §3.1's `ensureClient` kinds (`app-login | external-agent | exposed-consumer | cli`) include no per-agent actor kind. So for the platform's primary case — an in-cluster agent acting on behalf of a user — the gateway has no actor credential to present, and the flow as specified cannot execute. This is not pedantry: the `act` chain in the exchanged token is what design 04's `principal_chain` reads; whatever credential model is chosen defines what audit actually records.

**Fix**: decide and specify. Options: (a) per-agent OAuth clients (`ensureClient(agent-actor)` kind; operator provisions at reconcile — recorded as a design 02 §11 delta), giving true per-agent `act` identities; (b) a single platform exchange client with the agent identity asserted via a separate claim/SVID-derived assertion — cheaper, but the `act` chain then names the platform, not the agent, and receipts must compensate. (a) is the recommendation if the IdP client count is acceptable; either way the A2A per-hop re-exchange (`:53`) inherits the same question for every hop's actor.

### 3. MINOR — Keycloak delegation requires non-default features; the profile and conformance must pin them

`06-identity-glue.md:22,85`. Good news first — the claims verify: [Zitadel implements RFC 8693 exchange from v2.49 with impersonation/delegation and downscoping](https://zitadel.com/docs/guides/integrate/token-exchange), and Keycloak's [standard token exchange V2 is fully supported and enabled by default](https://www.keycloak.org/securing-apps/token-exchange). But Keycloak's *delegation* semantics (`act`/`may_act` — what §3.3 actually needs) **require the `parameterized-scopes` feature to be enabled** plus `may_act` pre-authorization, and [identity chaining arrived in Keycloak 26.5](https://www.keycloak.org/2026/01/jwt-authorization-grant) — a default Keycloak install would fail this design's conformance suite. **Fix**: the Keycloak adapter section states the required feature flags and minimum version; the testcontainer conformance fixture pins them; the chart's `idp: keycloak` profile sets them.

### 4. MINOR — per-hop exchange makes the IdP a synchronous data-plane dependency with no caching story

`06-identity-glue.md:53`. Every A2A hop re-exchanges → an IdP round-trip per hop per task, at request rate, against 1 Zitadel pod. The IdP-down row is honest (fail closed, machine paths alive), but steady-state load and latency are unaddressed. **Fix**: state exchanged-token caching at the gateway (keyed per `(subject, actor, aud)` until token expiry), expected exchange rates at the design's traffic assumptions, and an exchange-latency alert; note the cache is a *positive-result* cache only (revocation semantics unchanged).

### 5. MINOR — research cited inline, not landed (rule 4)

`06-identity-glue.md:6`. The Zitadel facts sit in the header; the Keycloak feature-flag facts and the gateway-exchange tiering question (finding 1) have no home at all. **Fix**: land `docs/research/idp-token-exchange-2026-08.md` (Zitadel v2.49+ exchange, Keycloak V2 + delegation flags + 26.5 chaining, gateway exchange tiering verdict) and cite it.

### 6. MINOR — new Agent conditions not recorded as a design-02 delta

`06-identity-glue.md:68-70`. `IdPUnavailable` and `OnBehalfOfUnavailable` land on Agents (and Apps); design 02 §11 is the established mechanism for exactly this (A1 did it for the budget conditions). **Fix**: one A3 entry.

### 7. MINOR — the `byo` IdP profile cannot implement `idp/v1alpha1` as specified

`06-identity-glue.md:24-33` vs design 07 §3 (`idp: zitadel | keycloak | byo`) and ADR-0010 (Dex-style federation). `ensureTenant/ensureClient/ensureServiceAccount` presuppose a management API; byo is "discovery URL + pre-provisioned creds" — the profile's methods are unimplementable there. **Fix**: add a declared `static` capability mode to the interface (provisioning methods return not-supported; a runbook documents the manual equivalents; install validates the pre-provisioned objects exist) — otherwise the slot claim is false for one of its three announced profiles.

### 8. MINOR — bootstrap details: first-boot credential and the service-account roster

`06-identity-glue.md:37-39`. (a) The chicken-and-egg is unstated: how does the bootstrap Job authenticate to a *fresh* Zitadel? (Zitadel's first-instance machine-key/admin bootstrap is the answer — say it, it's the thing that breaks quietly in CI.) (b) The roster provisions a `receipt-tap` service account, but in core the tap is in-process with the operator and authenticates via SPIRE mTLS, not OIDC — justify what it authenticates *to* with this identity, or drop it (provision at split-mode time if needed). (c) `gate-controller` is a P3 component — pre-provisioning is fine, but say it's deliberate. **Fix**: three sentences.

### 9. MINOR — IdentityProvider is missing from the charter's reserved-slot list (architecture erratum)

`06-identity-glue.md:16` vs architecture §15 ("KnowledgeGraphProvider first; reserved: MemoryProvider, EvalRunner, DriftDetector, SandboxProfile"). The slot is legitimate — §06's table and ADR-0010 establish it — but the charter's enumeration doesn't include it, and the charter is what gates designs. **Fix**: note the §15 list addition as an architecture erratum (not this design's bug, but this design is where it surfaces).

## Lens summary

1. **Doctrine**: clean — pods already budgeted, Zitadel on existing Postgres, glue is Job + operator code.
2. **Charter**: slot design is proper (versioned `idp/v1alpha1`, capability flags); finding 9 is a charter-list erratum, finding 7 a slot-completeness gap.
3. **Contract consistency**: findings 1, 2, 6 — the exchange mechanism's compile-side is unowned and the actor model contradicts design 02's client-minting rules.
4. **Hidden dependencies/circularity**: findings 4 (IdP in the data path) and 8a (bootstrap credential); no circular constructions.
5. **Failure modes**: the IdP-down row is the best in the series — machine paths alive, human paths fail closed, never forward raw user tokens. Findings 4/8 are the gaps.
6. **Security**: D1 (agents never hold user tokens) and audience narrowing are the right rails; impersonation is gated + receipted. No new listener surfaces.
7. **Research freshness**: Zitadel claim verified true; Keycloak true-with-flags (finding 3); the gateway-exchange tiering is the one load-bearing unknown (finding 1); nothing landed (finding 5).
8. **Testability**: dual-adapter conformance from day one and the negative audience test are exemplary; add the Keycloak flag pinning (finding 3) and an exchange-cache behavior test once finding 4 resolves.

## Disposition

**REVISE.** The security architecture is genuinely good — exchange-at-the-gateway, fail-closed capability flags, machine/human path separation, and identity-objects-never-deleted are all the right calls. But the two MAJORs sit at the flow's foundation: the mechanism may be enterprise-tiered (which 03's approved D1 forbids depending on), and the actor credential for the primary case doesn't exist in any design. Both must be settled with sources and recorded deltas before this can pass.

VERDICT: REVISE — 9 findings

---

## Re-review r2 (2026-08-20)

- **Verdict**: **PASS** (1 residual MINOR)
- **Independence note**: same independent session as r1; did not author the draft or revision.

### Per-finding disposition

| r1 | Severity | Disposition |
|---|---|---|
| 1 | MAJOR | **Resolved.** The research note lands the verdict with specifics: token exchange was open-sourced post-2.2 (data-plane + controller PRs cited), `oauthTokenExchange` on AgentgatewayPolicy with `actorToken`/`audiences`/`cache`, IdP-agnostic; the Solo Enterprise docs are correctly re-classified as adjacent conveniences. Design 03 §3.4 gained the `-exchange` row; design 07 pins the chart ≥ the OSS-exchange release. Residual **R2-a** below on version wording. |
| 2 | MAJOR | **Resolved.** New `agent-actor` client kind in `ensureClient`, provisioned per agent at reconcile, recorded as design 02 §11 A3; per-hop re-exchange uses each hop's own actor client, so the `act` chain names real agents — exactly what design 04's `principal_chain` needs. |
| 3 | MINOR | **Resolved.** Keycloak testcontainer runs with the token-exchange feature flag enabled and pinned; the profile enables it. |
| 4 | MINOR | **Resolved.** Exchange cache with TTL ≤ exchanged-token lifetime; outage semantics stated (cached tokens serve until expiry, then fail closed). |
| 5 | MINOR | **Resolved.** `docs/research/identity-2026-08.md` landed. |
| 6 | MINOR | **Resolved.** Conditions recorded in 02 §11 A3. |
| 7 | MINOR | **Resolved.** `capabilities.provisioning=false` with verify-exists mode for byo — the static-profile mode asked for. |
| 8 | MINOR | **Resolved** (a: IdP chart's init machine-key Secret named as the first-boot credential; b: receipt-tap dropped from the roster — it authenticates by SVID). Sub-point (c), noting gate-controller pre-provisioning as deliberately P3-forward, was skipped — harmless, not worth a round. |
| 9 | MINOR | **Resolved.** Architecture §15 now ships `IdentityProvider`/`AuthzProvider` in the slot list; §11 records where the errata landed. |

### Residual (new in r2, MINOR, non-blocking)

- **R2-a — "agentgateway 2.2 only" wording is now stale.** ADR-0019(5) and design 03's title/framing say the compiler targets the 2.2 CRD surface *only*, while the exchange capability requires pinning ≥ the post-2.2 OSS-exchange release (07 §3 already does). Reconcile the wording once (e.g. "2.2+ CRD surface; minimum pinned version owned by the chart") when recording ADR-0022 — otherwise the docs contradict the chart pin they mandate.

### Verdict

**PASS.** Both MAJORs resolved the hard way — the tiering question was answered with evidence rather than hedged, and the actor-credential model was built rather than papered over. Fold into ADR-0022 with R2-a's wording fix.
