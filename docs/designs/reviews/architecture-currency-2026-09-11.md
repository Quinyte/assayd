# architecture.md currency audit — 2026-09-11

Scope: `docs/architecture.md` §01–§17 only (§18–§20 already corrected in a prior pass).
Method: every present-indicative sentence classified as (a) true today, (b) designed/target
stated as current with no qualifier, or (c) false or citing a source that doesn't say it.
Only (b) and (c) are reported. Ground truth: `api/v1alpha1/agent_types.go` (only CRD is
`Agent`), `internal/controller/*.go`, `cmd/operator/main.go`, `charts/assayd/` (`helm
template` output), `go.mod`, `test/chart/chart_test.go`, `test/e2e/*.go`, AGENTS.md/CLAUDE.md
current-position paragraphs, `docs/designs/README.md` status table.

**STATUS: complete.** All of §01–§17 checked; §01, §03, §12 carried no findings (see "Verified true" below).

## §02 · System overview (lines 47–59)

| line | quoted claim | cat | evidence | proposed fix |
|---|---|---|---|---|
| 51 | "three core assayd operators (agent, workflow, model) — plus the enterprise tenant-operator" | c | `cmd/` has only `operator` (agent-operator); `api/v1alpha1/` has only `agent_types.go` — no Workflow/Model/Tenant types or controllers exist | Add "Designed, not shipped: only agent-operator exists today" |
| 52 | "**policy compiled from Agent CR budgets/tools/expose blocks**" | c | AGENTS.md:13 "the policy compiler does not exist"; no `AgentgatewayPolicy`/`AgentgatewayBackend` type or emitter anywhere; `gateway-api` is not even a `go.mod` dependency | Mark as target: "policy compiler does not exist yet (§03)" |
| 53 | "**Substrate**: NATS JetStream … + Postgres …" stated as current | b | `helm template charts/assayd \| grep '^kind:'` renders no StatefulSet/NATS/Postgres; `go.mod` has no nats/jetstream dependency | Prefix with "Designed, not shipped" |
| 55 | "Identity: SPIRE issues SVIDs to agent pods and the gateway; the platform IdP (Zitadel by default) handles humans + OAuth token exchange." | c | SPIRE appears only in code comments reserving labels for a future SPIRE consumer (`internal/controller/runnamespace.go:59,87,275,319`); no SPIRE/Zitadel Deployment renders from the chart; no OAuth token exchange code exists | Mark as target, not current |
| 57 | "Registration = the operator reads the card, publishes to the OASF directory (JetStream KV), issues identity, wires gateway routes." | c | Card read: true. Directory publish: `internal/controller/agent_controller.go:53` says "the gateway and directory are not implemented". Identity issuance: not implemented (see L55). Gateway routes: no HTTPRoute-construction code exists in `internal/controller/*.go` (only an RBAC marker comment, `agent_controller.go:167-181`); `gateway-api` is not a `go.mod` dependency; AGENTS.md:17 confirms the e2e test itself hand-authors the route while impersonating the operator's ServiceAccount | Rewrite: "the operator reads the card today; directory publish, identity issuance and route creation are designed, not shipped" |

## §04 · The KnowledgeGraph contract (lines 130–155) — SECTION-LEVEL FINDING

| line | quoted claim | cat | evidence | proposed fix |
|---|---|---|---|---|
| 130–155 (whole section) | "New vertical = new `KnowledgeGraph` CR, everything else reused," probes/versioning/ontology described as current, `kind: KnowledgeGraph` example | b | `api/v1alpha1/` contains only `agent_types.go` — no `KnowledgeGraph` type exists at all, unlike `Agent` (§03 correctly says "One CRD exists today: Agent"). §04 carries no equivalent disclaimer | Add a "Designed, not shipped — no KnowledgeGraph CRD exists" banner matching §03's |

## §05 · Communication & durability (lines 156–176)

| line | quoted claim | cat | evidence | proposed fix |
|---|---|---|---|---|
| 162–164 | table binds NATS JetStream / DBOS / Argo Workflows as current infrastructure | b | `go.mod` has no nats/jetstream/dbos/argo dependency; `charts/assayd/templates/_helpers.tpl:10` fails the render with "tier: plus is not implemented yet — it adds Argo Workflows, Phoenix, OpenFGA … none of which are built" | Prefix table with "Designed, not shipped" |
| 170–177 | Exposure table marks only 2 of 6 rows "*Roadmap*" (Workflow→MCP, Agent→MCP), implying the other 4 (Agent→A2A, KG→MCP, Workflow/Agent→HTTP, App→bundle) are current | c | KnowledgeGraph and App/Workflow CRDs do not exist at all (only `Agent` exists, confirmed in §03/§04 findings); the "org/public" A2A exposure, OASF listing and full task interface are also unimplemented (no OASF/directory code, confirmed §02 finding) | Mark all rows but the proven gateway path as *Roadmap* too, or add one blanket qualifier above the table |

## §06 · Security plane (lines 179–221)

| line | quoted claim | cat | evidence | proposed fix |
|---|---|---|---|---|
| 183–191 | table rows for SPIRE, IdP (Zitadel), AuthzProvider (OpenFGA), agentgateway budgets-compiled-from-CRs, agent-sandbox+default-deny-NetworkPolicy, cosign/sigstore+VAP — presented as current bindings with no qualifier before the table | c | SPIRE/Zitadel/OpenFGA: none deployed (`_helpers.tpl:10` fail-render for `plus`; no chart resources). Budgets compiled from CRs: policy compiler doesn't exist (§02 finding). agent-sandbox: `internal/controller/agent_controller.go:1258-1260` — "The agent-sandbox CRD is not bound yet, so every sandboxed agent currently downgrades." Default-deny NetworkPolicy: AGENTS.md:15 "the operator still materializes no NetworkPolicy and holds no `networkpolicies` RBAC." cosign/sigstore: `agent_controller.go:568` — "the card is not signature-verified: design 09's Sigstore card signing is not [implemented]"; the two `ValidatingAdmissionPolicy` objects the chart does render (`assayd-namespace-labels`, `assayd-gateway-routes`) enforce label/route reservation, not image or card signatures | Add a "Designed, not shipped" qualifier above the table, matching §03/§17's pattern |
| 189 | "Isolation \| **agent-sandbox** CRD (gVisor/Kata) + default-deny NetworkPolicy (agent reaches only the gateway) \| 0 pods" | c | See above — neither half is true today | Rewrite as target; note `SandboxDowngraded` condition fires today instead |
| 190 | "Supply chain \| **cosign/sigstore** signed images/cards/ModelKits + built-in `ValidatingAdmissionPolicy`" | c | No signature verification exists (`agent_controller.go:568`); the shipped VAPs are unrelated (namespace labels / gateway routes) | Rewrite as target |

## §07 · Harness & observability (lines 223–230)

| line | quoted claim | cat | evidence | proposed fix |
|---|---|---|---|---|
| 225 | "**Sessions & receipts**: every A2A task through the gateway recorded to JetStream …" | b | No receipt code exists anywhere in `internal/`/`cmd/` (only a stray match inside a generated CRD file's field name); no NATS dependency in `go.mod`; receipt tap is design 04, listed in the component plan as P1 not-yet-built | Prefix with "Designed, not shipped" |
| 226–228 | "**Traces**: OTel GenAI semantic conventions … Gateway emits spans per hop … **Backend**: OpenObserve … **Golden signals**: … alert rules shipped in the chart, each fixture-tested in CI" | c | No OTel/OpenObserve reference anywhere in `go.mod` or `charts/assayd/values.yaml`; no alert-rule fixtures exist in the chart | Prefix section with "Designed, not shipped"; the "shipped in the chart, fixture-tested in CI" clause is flatly false and should be removed or moved to a target list |

## §08 · Evals — semantic admission (lines 232–247)

| line | quoted claim | cat | evidence | proposed fix |
|---|---|---|---|---|
| 234 | "Agents and models **earn traffic**. Rollout: new version → HELD (no traffic) → Eval Job (DeepEval/Inspect) … → gate (score ≥ threshold) → pass: gateway-weighted canary → 100%; fail: rollback …" stated as current | c | `EvalSuite` CRD does not exist (only `Agent` exists). The controller's own code contradicts this directly: `internal/controller/agent_controller.go:1252-1254` sets `CondGatesPassed=False/GateControllerUnimplemented`, "spec.gates are declared but the gate controller (design 16) is not implemented; the revision holds at zero traffic rather than promoting ungated." No gateway-weighted canary mechanism exists (no `AgentgatewayBackend`/weights emitted, per §02 finding) | Prefix with "Designed, not shipped"; note the actual current behavior is "declared gates hold the revision at zero traffic forever" |

## §09 · Drift & self-healing (lines 249–260)

| line | quoted claim | cat | evidence | proposed fix |
|---|---|---|---|---|
| 249–258 | "Detectors write conditions; controllers act; humans see semantic health in `kubectl get agents`." + drift table (KnowledgeStale, ModelDrifted, Degraded, Embedding detectors/actions) stated as current | c | `CondModelDrifted` is declared as a constant (`api/v1alpha1/agent_types.go:443`, "// design 20") but is never set or read anywhere in `internal/controller/*.go` outside the constant list — no detector or remediation controller exists; `KnowledgeStale` doesn't appear in code at all (no KnowledgeGraph CRD exists, per §04 finding) | Prefix with "Designed, not shipped" |

## §10 · ModelHub (plus tier) (lines 262–274)

| line | quoted claim | cat | evidence | proposed fix |
|---|---|---|---|---|
| 264–272 | "`Model` CR: train → package → eval-gate → serve …" + bindings table (KitOps, KServe, Kubeflow Trainer, MLflow) stated as current | b | No `Model` type exists in `api/v1alpha1/` at all; no model-operator exists (`cmd/` has only `operator`, the agent-operator); `charts/assayd/templates/_helpers.tpl:10` fails the render for the `plus` tier this belongs to, saying explicitly "none of which are built" | Prefix with "Designed, not shipped" (the "As designed" tag two paragraphs down implies the top isn't — but the top paragraph and table need the same disclaimer) |

## §11 · Developer experience — the golden path (lines 276–288)

| line | quoted claim | cat | evidence | proposed fix |
|---|---|---|---|---|
| 276–286 | "Target: empty directory → agent answering … Lifecycle = five commands: `init → dev → invoke → build → deploy`." + `assayd init`, `assayd dev`, `assayd build`, `assayd deploy`, `assayd init app`, `assayd workflow test` described as current tooling | b | No CLI exists in the repository at all — `cmd/` contains only `operator`; no `assayd` binary, no cobra command tree, nothing named `cli` anywhere in the tree | Prefix section with "Designed, not shipped — no CLI exists yet" (CLI is P1+, size L, per §12's own component table) |

## §12 · Component plan (lines 290–333)

No findings. This section is framed as a roadmap ("Phases → §18", a `Phase` column of P1…ent for every row) and does not assert current-fact claims — verified true as a plan, not audited as a status report.

## §13 · Graph engineering (lines 335–371)

| line | quoted claim | cat | evidence | proposed fix |
|---|---|---|---|---|
| 335–371 (whole section) | ontology document, `assayd kg init --pattern …`, `assayd kg diff v11 v12`, provider adapter's six MCP tools, ingestion pipeline stages — described as current capability | b | No `KnowledgeGraph` CRD exists (§04 finding); no CLI exists at all (§11 finding), so `assayd kg init`/`assayd kg diff` cannot run; no provider adapter code exists in the repo | Add a "Designed, not shipped" banner matching §03/§04's pattern |

## §14 · Loop engineering (lines 373–380)

| line | quoted claim | cat | evidence | proposed fix |
|---|---|---|---|---|
| 373–378 | "Two loops, separated …" — governance ring (budgets terminate with reason `budget`, hop limit + cycle detection, approval interrupts, kill switch) described as current, platform-enforced | b | Design 22 (loop governance) is one of the designs the ADR-0030 scope reset marks unimplemented/hypothesis-adjacent (`docs/designs/README.md` row 22 has no A6-style "landed" language, and no lineage-CEL, approval-voucher or kill-switch code exists in `internal/controller/*.go`) | Prefix with "Designed, not shipped" |

## §15 · Extension model — the pattern charter (lines 382–394)

| line | quoted claim | cat | evidence | proposed fix |
|---|---|---|---|---|
| 388 | "capability → provider slot (**shipped**: KnowledgeGraphProvider, IdentityProvider, AuthzProvider, EvalRunner; …)" | c | Zero matches anywhere in the repository (`.go` files) for `KnowledgeGraphProvider`, `IdentityProvider`, `AuthzProvider`, or `EvalRunner` — none of these interfaces/types exist in code. The word "shipped" is a direct, checkable false claim | Change "shipped" to "planned" or remove the word entirely |
| 390 | "`assayd pack install context-compaction` → filter live on all agents, template + skill indexed, no redeploys, rollback = uninstall." | b | No `Pack` CRD exists (only `Agent`); no CLI exists (§11 finding) — this command cannot run | Prefix with "Designed, not shipped" |

## §16 · Competitive positioning (lines 396–407)

| line | quoted claim | cat | evidence | proposed fix |
|---|---|---|---|---|
| 398–405 | comparison table's bold "assayd" column: "eval-as-admission + session replay", "semantic conditions + controllers", "≤8 pods, Postgres+NATS only" | b | None of eval-admission (§08), session replay (design 17, unimplemented), drift controllers (§09), or the Postgres+NATS substrate (§02/§05 — neither is deployed by the chart) exist today; only the CRD/chart/gateway-path slice from ADR-0030 is real | Add a footnote that the differentiator column states the target, not what ships today; the gateway-path row is the only one presently true |

## §17 · Weight budget — core tier (lines 417–427)

| line | quoted claim | cat | evidence | proposed fix |
|---|---|---|---|---|
| 425 | "The budget is CI-enforced, not aspirational: a job counts rendered pods against `weight-budget.yaml` and a second job checks stateful workloads against a reasoned allowlist — a PR that adds either fails unless it edits the ledger in the same commit." | c | No file named `weight-budget.yaml` exists anywhere in the repo. The actual mechanism is `test/chart/chart_test.go`'s `TestCorePodBudget` (`const budget = 8`, line 88) and `TestStatefulDependencyAllowlist` (an in-Go `allowed` map), run via `make chart` in CI (`.github/workflows/ci.yml:61`, "chart doctrine (pod budget, stateful allowlist, RBAC drift)"). The underlying claim — that CI fails a PR that raises the count without also editing the check — is TRUE; only the cited artifact name is wrong | Replace "`weight-budget.yaml`" with "the `TestCorePodBudget`/`TestStatefulDependencyAllowlist` checks in `test/chart/chart_test.go`" |

---

## Verified true — checked and sound

- §03's "Designed, not shipped" banner and its claim that `Agent` is the only CRD today: confirmed (`api/v1alpha1/` has only `agent_types.go`; `config/crd/assayd.dev_agents.yaml` is the only CRD manifest).
- §12 Component plan: framed as a phased roadmap, not a status claim — no findings.
- §17's core claim that pod count and stateful-dependency drift are CI-enforced (only the *file name* cited is wrong — see finding above).
- The Agent CR's `Card`/`Runtime`/`Release` fields (§03 YAML) correspond to real fields in `api/v1alpha1/agent_types.go` (`CardSpec`, `AgentRuntime`, `ReleaseSpec`) — the YAML example is inside §03's own "target surface" disclaimer, so fields that don't yet exist (e.g. `identity: {}`, which has no counterpart in `AgentSpec`) are already covered by that qualifier and not separately flagged.
- §01's doctrine rules (bind-don't-build, two stateful deps, library-over-server, reconcile-loop-is-the-product, weight budget, tiered install) are normative principles, not claims that the system currently implements every tier — read as doctrine rather than a status report, consistent with how AGENTS.md cites this section.
- The chart's actual rendered kinds (`ClusterRole`, `ClusterRoleBinding`, `Deployment`×1, `Namespace`, `PodDisruptionBudget`, `ServiceAccount`, `ValidatingAdmissionPolicy`×2, `ValidatingAdmissionPolicyBinding`×2) match CLAUDE.md's "what remains untrue" paragraph — no drift found there.
- Pinned versions cross-checked: `hack/e2e.sh:45` pins `AGW_VERSION="${AGW_VERSION:-1.5.0}"` (agentgateway) and installs Gateway API v1.6.0 (`hack/e2e.sh:263`, `apiVersion: gateway.networking.k8s.io/v1`) — §01–§17 do not name a stale agentgateway/Gateway API version, an old A2A v0.x shape, or MCP sessions as current, so no finding on that axis within this scope.

## Counts

**By category**
- (b) designed/target stated as current, no qualifier: 10
- (c) false or citing a source that doesn't say it: 13
- **Total findings: 23**

**By section**
| Section | (b) | (c) | Total |
|---|---|---|---|
| §02 System overview | 1 | 4 | 5 |
| §04 KnowledgeGraph contract | 1 | 0 | 1 |
| §05 Communication & durability | 1 | 1 | 2 |
| §06 Security plane | 0 | 3 | 3 |
| §07 Harness & observability | 1 | 1 | 2 |
| §08 Evals | 0 | 1 | 1 |
| §09 Drift & self-healing | 0 | 1 | 1 |
| §10 ModelHub | 1 | 0 | 1 |
| §11 Developer experience | 1 | 0 | 1 |
| §13 Graph engineering | 1 | 0 | 1 |
| §14 Loop engineering | 1 | 0 | 1 |
| §15 Extension model | 1 | 1 | 2 |
| §16 Competitive positioning | 1 | 0 | 1 |
| §17 Weight budget | 0 | 1 | 1 |
| **Total** | **10** | **13** | **23** |

