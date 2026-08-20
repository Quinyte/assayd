# ADR-0013: Layered multi-tenancy, mostly inherited; Tenant CR as fan-out (enterprise)
- **Status**: accepted · 2026-08-20
- **Context**: Chosen components carry native tenancy: NATS accounts, Postgres RLS, Zitadel organizations, gateway partitions. Control-plane isolation: namespaces+Capsule (soft) vs vCluster (hard, 2026-dominant for external customers).
- **Decision**: Soft tenancy = namespace-per-tenant + policy. Hard tenancy = vCluster per tenant. Data-plane tenancy inherited (NATS accounts, RLS, IdP orgs, gateway partitions, per-tenant KG instances). Enterprise Tenant CR = one-object fan-out controller across all layers.
- **Consequences**: plume's own tenancy code is mostly the fan-out; isolation guarantees come from proven components.
