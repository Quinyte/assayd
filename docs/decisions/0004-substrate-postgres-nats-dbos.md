# ADR-0004: Substrate = Postgres + NATS JetStream; durability via DBOS, not Temporal
- **Status**: accepted · 2026-08-20
- **Context**: "Workflow engine" conflates three jobs: messaging/events, durable execution, batch DAGs. Temporal (run before as genesis-de) costs ~5 pods + operational discipline. DBOS is a library on Postgres; JetStream is one binary with KV/object store; Argo covers batch.
- **Decision**: JetStream = events, KV directory, object store, receipts (1 pod). DBOS = durable execution inside workflow/app pods (0 pods). Argo Workflows = batch DAGs, plus tier only, never agent loops. Temporal is the documented escape hatch if DBOS hits a cross-service orchestration wall — a wall to hit, not to pre-build for.
- **Consequences**: Zero standing workflow infrastructure in core; workflow state lives in Postgres.
