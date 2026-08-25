# Component design backlog

Design-first: every component gets a design doc (per `TEMPLATE.md`) approved **before** implementation. Order follows the dependency chain, not the build phases alone — contracts before consumers.

| # | Component | Phase | Size | Design status |
|---|---|---|---|---|
| 1 | KnowledgeGraphProvider contract (6-tool MCP surface + ontology schema + probe format + conformance) | P2 | M | **approved** (`01-…`, ADR-0017/0018, review PASS) |
| 2 | Agent CRD + agent-operator | P1 | L | **approved** (`02-…`, ADR-0019, review PASS) |
| 3 | Policy compiler (CR → agentgateway config, incl. expose) | P1 | M | **approved** (`03-…`, ADR-0020, review PASS); amendments A1–A6 (2026-08-25) revised after REVISE, pending re-critique |
| 4 | Receipt tap + receipt envelope schema | P1 | M | r2 — critique **PASS** (`04-…`, `reviews/04-review.md`; awaiting user approval + ADR-0021) |
| 5 | Agent directory (OASF on JetStream KV) | P1 | S | r2 — critique **PASS** (`05-…`, `reviews/05-review.md`; awaiting user approval, ADR-0022) |
| 6 | Identity glue | P1 | M | r2 — critique **PASS** (`06-…`, `reviews/06-review.md`; awaiting user approval, ADR-0022) |
| 7 | Umbrella chart, profiles, e2e CI | P1 | M | r2 — critique **PASS** (`07-…`, `reviews/07-review.md`; awaiting user approval, ADR-0022) |
| 8 | CLI | P1+ | L | r2 — critique **PASS** (`08-…`, `reviews/08-review.md`; awaiting user approval, ADR-0022) |
| 9 | SDK templates | P1 | M | r2 — critique **PASS** (`09-…`, `reviews/09-review.md`; awaiting user approval, ADR-0022) |
| 10 | Observability pack | P1+ | M | r2 — critique **PASS** (`10-…`, `reviews/10-review.md`; awaiting user approval, ADR-0022) |
| 11 | Connector CRD | P2 | M | r2 — critique **PASS** (`11-…`, `reviews/11-review.md`; awaiting user approval, ADR-0023) |
| 12 | Ontology spec | P2 | M | r2 — critique **PASS** (`12-…`, `reviews/12-review.md`; awaiting user approval, ADR-0023) |
| 13 | Graphiti provider adapter | P2 | M | r2 — critique **PASS** (`13-…`, `reviews/13-review.md`; awaiting user approval, ADR-0023) |
| 14 | Ingestion pipeline + extraction QA | P2 | L | r2 — critique **PASS** (`14-…`, `reviews/14-review.md`; awaiting user approval, ADR-0023) |
| 15 | Probe engine | P2 | M | r2 — critique **PASS** (`15-…`, `reviews/15-review.md`; awaiting user approval, ADR-0023) |
| 16 | EvalSuite + gate controller | P3 | L | r2 — critique **PASS** (`16-…`, `reviews/16-review.md`; awaiting user approval, ADR-0024) |
| 17 | Session replay engine | P3 | M | r2 — critique **PASS** (`17-…`, `reviews/17-review.md`; awaiting user approval, ADR-0024) |
| 18 | Pack format + installer | P3 | M | r2 — critique **PASS** (`18-…`, `reviews/18-review.md`; awaiting user approval, ADR-0024) |
| 19 | Knowledge pattern packs | P2–P3 | M | r2 — critique **PASS** (`19-…`, `reviews/19-review.md`; awaiting user approval, ADR-0023) |
| 20 | Drift controllers | P4 | L | r2 — critique **PASS** (`20-…`, `reviews/20-review.md`; awaiting user approval, ADR-0025) |
| 21 | workflow-operator + Workflow CRD | P4 | M | r2 — critique **PASS** (`21-…`, `reviews/21-review.md`; **R2-a required pre-ADR-0025**: missing 06 `workflow-actor` record) |
| 22 | Loop governance | P4 | M | r2 — critique **PASS** (`22-…`, `reviews/22-review.md`; awaiting user approval, ADR-0025) |
| 23 | App layer | P4 | M | r2 — critique **PASS** (`23-…`, `reviews/23-review.md`; awaiting user approval, ADR-0025) |
| 24 | AuthzProvider (OpenFGA) | P4 | M | r2 — critique **PASS** (`24-…`, `reviews/24-review.md`; awaiting user approval, ADR-0025; sequenced with 21 R2-a) |
| 25 | model-operator + ModelHub | P5 | L | r2 — critique **PASS** (`25-…`, `reviews/25-review.md`; awaiting user approval, ADR-0026) |
| 26 | Tenant CR fan-out | ent | L | r3 — critique **PASS** (`26-…`, `reviews/26-review.md`; **R3-a required pre-ADR-0026**: cross-cluster ownerRef lifecycle) |
| 27 | Compliance profile packs | ent | M | r2 — critique **PASS** (`27-…`, `reviews/27-review.md`; **R2-a required pre-ADR-0026**: chainer runtime home) |

Update this table when a design starts / lands. Decisions made during design → new ADR.
