# ADR-0005: The domain is a versioned KnowledgeGraph contract (the differentiator)
- **Status**: accepted · 2026-08-20
- **Context**: No k8s agent platform treats the domain as data (kagent: prompts; Rossoctl: a knowledge-base service). Palantir AIP proves ontology-first at commercial scale. Graph engines (Graphiti/cognee/TrustGraph/Neo4j) all expose MCP already.
- **Decision**: KnowledgeGraph CR = provider (behind a 6-tool MCP surface: search, neighbors, get_context_bundle, cite, schema, probe) + reviewed ontology + ingestion ref + immutable versioned snapshots + semantic-readiness probes. Agents pin graph versions. The ontology derives the probe set, eval golden set, and drift baseline. Providers are a driver slot with conformance tests.
- **Consequences**: New vertical = new CR. The provider contract is the highest-stakes interface in the project; spec it before code.
