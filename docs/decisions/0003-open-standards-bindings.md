# ADR-0003: Open standards only; the platform is thin bindings
- **Status**: accepted · 2026-08-20
- **Context**: Agent-standard wars settled in 2025–26: A2A (Linux Foundation, 150+ orgs), MCP, OASF/AGNTCY, SPIFFE, OTel GenAI semconv (pre-stable), Gateway API, CloudEvents, OCI/ModelPack.
- **Decision**: Consume and publish over these exclusively. BYO-SDK contract = "container serving A2A + Agent Card". agentgateway is the single data plane (Gateway-API-conformant; A2A/MCP/LLM native). OTel GenAI adopted now with the convention version pinned. No invented protocols, ever.
- **Consequences**: Protocol churn is absorbed at the gateway; assayd's value concentrates in layers standards don't cover (domain contract, semantic admission).
