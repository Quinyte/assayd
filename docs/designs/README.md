# Component design backlog

Design-first: every component gets a design doc (per `TEMPLATE.md`) approved **before** implementation. Order follows the dependency chain, not the build phases alone — contracts before consumers.

| # | Component | Phase | Size | Design status |
|---|---|---|---|---|
| 1 | KnowledgeGraphProvider contract (6-tool MCP surface + ontology schema + probe format + conformance) | P2 | M | **approved** (`01-…`, ADR-0017/0018, review PASS) |
| 2 | Agent CRD + agent-operator | P1 | L | **approved** (`02-…`, ADR-0019, review PASS) |
| 3 | Policy compiler (CR → agentgateway config, incl. expose) | P1 | M | r2 — critique **PASS** (`03-…`, `reviews/03-review.md`; awaiting user approval + ADR-0020) |
| 4 | Receipt tap + receipt envelope schema | P1 | M | r2 — critique **PASS** (`04-…`, `reviews/04-review.md`; awaiting user approval + ADR-0021) |
| 5 | Agent directory (OASF on JetStream KV) | P1 | S | r2 — critique **PASS** (`05-…`, `reviews/05-review.md`; awaiting user approval, ADR-0022) |
| 6 | Identity glue | P1 | M | r2 — critique **PASS** (`06-…`, `reviews/06-review.md`; awaiting user approval, ADR-0022) |
| 7 | Umbrella chart, profiles, e2e CI | P1 | M | r2 — critique **PASS** (`07-…`, `reviews/07-review.md`; awaiting user approval, ADR-0022) |
| 8 | CLI | P1+ | L | r2 — critique **PASS** (`08-…`, `reviews/08-review.md`; awaiting user approval, ADR-0022) |
| 9 | SDK templates | P1 | M | r2 — critique **PASS** (`09-…`, `reviews/09-review.md`; awaiting user approval, ADR-0022) |
| 10 | Observability pack | P1+ | M | r2 — critique **PASS** (`10-…`, `reviews/10-review.md`; awaiting user approval, ADR-0022) |
| 11 | Connector CRD | P2 | M | draft — critique **REVISE** (`11-…`, `reviews/11-review.md`) |
| 12 | Ontology spec | P2 | M | draft — critique **REVISE** (`12-…`, `reviews/12-review.md`) |
| 13 | Graphiti provider adapter | P2 | M | draft — in review (`13-…`) |
| 14 | Ingestion pipeline + extraction QA | P2 | L | draft — in review (`14-…`) |
| 15 | Probe engine | P2 | M | draft — in review (`15-…`) |
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
