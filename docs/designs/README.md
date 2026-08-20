# Component design backlog

Design-first: every component gets a design doc (per `TEMPLATE.md`) approved **before** implementation. Order follows the dependency chain, not the build phases alone — contracts before consumers.

| # | Component | Phase | Size | Design status |
|---|---|---|---|---|
| 1 | KnowledgeGraphProvider contract (6-tool MCP surface + ontology schema + probe format + conformance) | P2 | M | **approved** (`01-…`, ADR-0017/0018, review PASS) |
| 2 | Agent CRD + agent-operator | P1 | L | **approved** (`02-…`, ADR-0019, review PASS) |
| 3 | Policy compiler (CR → agentgateway config, incl. expose) | P1 | M | r2 — critique **PASS** (`03-…`, `reviews/03-review.md`; awaiting user approval + ADR-0020) |
| 4 | Receipt tap + receipt envelope schema | P1 | M | r2 — critique **PASS** (`04-…`, `reviews/04-review.md`; awaiting user approval + ADR-0021) |
| 5 | Agent directory (OASF on JetStream KV) | P1 | S | draft — critique **REVISE** (`05-…`, `reviews/05-review.md`) |
| 6 | Identity glue | P1 | M | draft — in review (`06-…`) |
| 7 | Umbrella chart, profiles, e2e CI | P1 | M | draft — in review (`07-…`) |
| 8 | CLI | P1+ | L | draft — in review (`08-…`) |
| 9 | SDK templates | P1 | M | draft — in review (`09-…`) |
| 10 | Observability pack | P1+ | M | draft — in review (`10-…`) |
| 11 | Connector CRD (3 facets) | P2 | M | not started |
| 12 | Ontology spec (entities/relations/invariants/probes) | P2 | M | not started |
| 13 | Graphiti provider adapter | P2 | M | not started |
| 14 | Ingestion pipeline + extraction QA | P2 | L | not started |
| 15 | Probe engine (semantic readiness) | P2 | M | not started |
| 16 | EvalSuite CRD + controller + dataset builder | P3 | L | not started |
| 17 | Session replay engine | P3 | M | not started |
| 18 | Pack format + installer | P3 | M | not started |
| 19 | Knowledge pattern packs (sop-decision-tree first) | P2–P3 | M | not started |
| 20 | Drift controllers | P4 | L | not started |
| 21 | workflow-operator + Workflow CRD | P4 | M | not started |
| 22 | Loop governance (hop limits, cycle detection, approvals, kill switch) | P4 | M | not started |
| 23 | App layer (HTTP projection, typed clients, release pinning) | P4 | M | not started |
| 24 | AuthzProvider (OpenFGA ReBAC model + gateway checks) | P4 | M | not started |
| 25 | model-operator + ModelHub | P5 | L | not started |
| 26 | Tenant CR fan-out | ent | L | not started |
| 27 | Compliance profile packs (hipaa) | ent | M | not started |

Update this table when a design starts / lands. Decisions made during design → new ADR.
