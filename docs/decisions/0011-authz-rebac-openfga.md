# ADR-0011: Three authz layers; ReBAC via AuthzProvider slot (OpenFGA default)
- **Status**: accepted · 2026-08-20
- **Context**: Agent platforms need "may U invoke A; may A call T / read subgraph S / delegate to B on U's behalf" — relationship-shaped. OpenFGA (CNCF; deep prior production experience) vs SpiceDB (Zanzibar-purist; OpenAI-scale proof).
- **Decision**: (1) Build-time: k8s RBAC + CEL ValidatingAdmissionPolicy ("prod agents must have gates", "signed images only"). (2) Runtime: AuthzProvider slot, default OpenFGA (reuses Postgres), checked at the gateway per hop; delegation chains as first-class relations → immediate revocation. (3) Policy-as-config on CRs (approvals, budgets, environment scoping).
- **Consequences**: ReBAC engine is plus-tier (1 pod); the gateway is the sole enforcement point.
