# ADR-0018: Graphiti adapter managed default backend = FalkorDB (license caveat documented)
- **Status**: accepted · 2026-08-20
- **Context**: Graphiti backends: Neo4j 5.26 (GPLv3 CE, JVM-heavy), FalkorDB 1.1.2 (lightweight, **SSPLv1 — source-available, not OSI**), Neptune (AWS-only); Kuzu deprecated. Design 01 §9 Q1.
- **Decision**: Managed default = FalkorDB (+1 stateful pod per managed graph, plus tier; doctrine rule 2 governs the platform substrate, not per-graph workload state). SSPL caveat documented prominently: fine self-hosted/internal; Neo4j CE profile is one value flip for SSPL-banning enterprises. No lock-in: backend swappable per graph, provider swappable per kgp, contract conformance enables third-party providers. Future Apache AGE provider (graph in our Postgres, zero new deps) on the roadmap as the doctrine-pure option.
- **Consequences**: BYO mode unaffected; enterprise conversations get an honest license answer with a supported alternative.
