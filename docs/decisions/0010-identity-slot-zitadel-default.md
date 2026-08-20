# ADR-0010: Identity is a provider slot; Zitadel default, Keycloak profile
- **Status**: accepted · 2026-08-20 (supersedes early Keycloak-default drafts)
- **Context**: Keycloak is CNCF incubating but JVM-heavy. Zitadel: single Go binary on our existing Postgres (~100MB), organizations-native (maps 1:1 to tenants), built-in audit; AGPL since 2025 (acceptable: used unmodified, self-hosted). Dex: federation only.
- **Decision**: IdentityProvider slot behind an OIDC contract. Default **Zitadel** (recipe + tenancy fit); **Keycloak** profile for enterprises running it; federation for BYO corporate IdPs. SPIRE remains the workload identity authority; OAuth token exchange yields on-behalf-of agent credentials.
- **Consequences**: One pod, reuses Postgres; AGPL noted in enterprise conversations with the Keycloak profile as the answer.
