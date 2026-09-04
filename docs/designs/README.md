# Component design backlog

Design-first: every component gets a design doc (per `TEMPLATE.md`) approved **before** implementation. Order follows the dependency chain, not the build phases alone — contracts before consumers.

| # | Component | Phase | Size | Design status |
|---|---|---|---|---|
| 1 | KnowledgeGraphProvider contract (6-tool MCP surface + ontology schema + probe format + conformance) | P2 | M | **approved** (`01-…`, ADR-0017/0018, review PASS) |
| 2 | Agent CRD + agent-operator | P1 | L | **body consolidated 2026-09-04 (A62), critique pending — not approved** (`02-…`, ADR-0019, ADR-0029). 61 folded amendments rewritten into one self-contained body; §5 opens with every guarantee nothing enforces. CRD, revision identity by content, immutable material and the run namespace run and are e2e-proven on k3d; gateway/identity/registration/gate wait on designs 03, 06, 05, 16 |
| 3 | Policy compiler (CR → agentgateway config, incl. expose) | P1 | M | **re-opened** — ADR-0028 supersedes ADR-0020. Amendments A1–A50; **A46–A50 (2026-09-04) answer r8's last blockers and are NOT closed** — three rounds, REVISE each time (`reviews/03-a46-a50-critique.md`). **Consolidation recommended before further patching**, as design 02 took at A62. **Do not implement** |
| 4 | Receipt tap + receipt envelope schema | P1 | M | r2 — critique **PASS** (`04-…`, `reviews/04-review.md`; awaiting user approval + ADR-0021) |
| 5 | Agent directory (OASF on JetStream KV) | P1 | S | r2 — critique **PASS** (`05-…`, `reviews/05-review.md`; awaiting user approval, ADR-0022) |
| 6 | Identity glue | P1 | M | r2 — critique **PASS** (`06-…`, `reviews/06-review.md`; awaiting user approval, ADR-0022) |
| 7 | Umbrella chart, profiles, e2e CI | P1 | M | r2 — critique **PASS** (`07-…`, `reviews/07-review.md`; awaiting user approval, ADR-0022); amendments A1–A5, **A5 (2026-09-03) uncritiqued** — run-namespace mechanics the chart ships and the operator materializes |
| 8 | CLI | P1+ | L | r2 — critique **PASS** (`08-…`, `reviews/08-review.md`; awaiting user approval, ADR-0022) |
| 9 | SDK templates | P1 | M | r2 — critique **PASS** (`09-…`, `reviews/09-review.md`; awaiting user approval, ADR-0022) |
| 10 | Observability pack | P1+ | M | r2 — critique **PASS** (`10-…`, `reviews/10-review.md`; awaiting user approval, ADR-0022) |
| 11 | Connector CRD | P2 | M | r2 — critique **PASS** (`11-…`, `reviews/11-review.md`; awaiting user approval, ADR-0023); **§11 owed (2026-09-04)** — the `MCPServer` kind and the tool-name uniqueness three designs assume |
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
| 24 | AuthzProvider (OpenFGA) | P4 | M | r2 — critique **PASS** (`24-…`, `reviews/24-review.md`; awaiting user approval, ADR-0025; sequenced with 21 R2-a); **A1 (2026-09-03) uncritiqued** — trust domain in the machine subject |
| 25 | model-operator + ModelHub | P5 | L | r2 — critique **PASS** (`25-…`, `reviews/25-review.md`; awaiting user approval, ADR-0026) |
| 26 | Tenant CR fan-out | ent | L | r3 — critique **PASS** (`26-…`, `reviews/26-review.md`; **R3-a required pre-ADR-0026**: cross-cluster ownerRef lifecycle); **A1 (2026-09-03) uncritiqued** — run namespace ownership per mode |
| 27 | Compliance profile packs | ent | M | r2 — critique **PASS** (`27-…`, `reviews/27-review.md`; **R2-a required pre-ADR-0026**: chainer runtime home); **A1 (2026-09-03) uncritiqued** — hard-mode gate integrity is an attested exception |

Update this table when a design starts / lands. Decisions made during design → new ADR.
