# ADR-0002: The lightweight doctrine (six rules)
- **Status**: accepted · 2026-08-20
- **Context**: Competing platforms (Rossoctl, kagent, Dapr) ship heavy stacks; prior experience (Genesis V3) showed the operational cost of platform weight. Lightweightness is the product identity, so it must be enforceable, not aspirational.
- **Decision**: (1) Bind, don't build. (2) Postgres + NATS are the only stateful deps. (3) Library over server. (4) The k8s reconcile loop is the product — versioning/self-healing/rollout via CRs, conditions, GitOps. (5) Weight budget as spec: core ≤ 8 pods, one assayd-code pod; every addition needs a written justification. (6) Tiered install core/plus.
- **Consequences**: Some conveniences are rejected on weight grounds; design reviews cite rule numbers.
