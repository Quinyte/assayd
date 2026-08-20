# ADR-0012: No mesh in core; optional ambient (ztunnel-only) profile
- **Status**: accepted · 2026-08-20
- **Context**: All agent traffic already crosses agentgateway (L7 policy, SPIRE mTLS, receipts) — a mesh duplicates it. Istio ambient is GA and cheap (~90% less memory than sidecars); Istio announced experimental agentgateway support (KubeCon EU 2026).
- **Decision**: Core is meshless. An `ambient` profile adds ztunnel-only L4 mTLS for residual non-agent traffic (Postgres/NATS) for encryption-everywhere enterprises. Waypoints never — agentgateway is the L7.
- **Consequences**: Zero mesh operational cost in core; a credible answer for mesh-mandating enterprises.
