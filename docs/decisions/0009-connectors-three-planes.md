# ADR-0009: Connectors — MCP-only agent-facing; three planes total
- **Status**: accepted · 2026-08-20
- **Context**: Forcing bulk ingestion and eventing through MCP is shape-wrong; but bespoke per-agent API clients destroy uniform governance.
- **Decision**: Tool plane (agent↔system, sync) = MCP only, via the gateway (public MCP Registry = catalog). Ingestion plane (system→KG, bulk) = workflow steps with native readers; agents see results via the KG's MCP surface. Event plane (system→platform, async) = CloudEvents → JetStream → Workflow triggers. One Connector CR declares a system-of-record with the three optional facets; credentials once.
- **Consequences**: genesis-connectors-scale machinery collapses into catalog images + workflow readers.
