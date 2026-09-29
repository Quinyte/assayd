# ADR-0002: The lightweight doctrine (six rules)
- **Status**: accepted · 2026-08-20 · **Amendment 1 (2026-09-29) — rule 2 has an exception, decided by the human in ADR-0035. The Decision below is not edited; read Amendment 1 with it.**
- **Context**: Competing platforms (Rossoctl, kagent, Dapr) ship heavy stacks; prior experience (Genesis V3) showed the operational cost of platform weight. Lightweightness is the product identity, so it must be enforceable, not aspirational.
- **Decision**: (1) Bind, don't build. (2) Postgres + NATS are the only stateful deps. (3) Library over server. (4) The k8s reconcile loop is the product — versioning/self-healing/rollout via CRs, conditions, GitOps. (5) Weight budget as spec: core ≤ 8 pods, one assayd-code pod; every addition needs a written justification. (6) Tiered install core/plus.
- **Consequences**: Some conveniences are rejected on weight grounds; design reviews cite rule numbers.

## Amendment 1 (2026-09-29) — rule 2's exception, recorded in ADR-0035

Rule 2 says Postgres and NATS are the only stateful deps. That was already false: the allowlist admitted OpenObserve, and SPIRE's default chart keeps its CA signing keys on a PVC. The human decided on 2026-09-29, in [ADR-0035](0035-nfr2-stateful-dependency-allowlist.md), that Postgres and NATS JetStream are the only stateful **substrate**, OpenObserve the only stateful **sink**, and SPIRE's CA signing keys the only stateful **key material**. The classes, how the allowlist matches, and how an entry is added are ADR-0035's. The Decision above is not edited; rule 2 is read with this exception.
