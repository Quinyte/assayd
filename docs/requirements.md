# assayd — Requirements

Derived from architecture v0.1 (2026-08-20). FR = functional, NFR = non-functional.

**"Each requirement is testable; NFRs are release gates" is what this line used to say, and both halves were false.** They are kept here rather than quietly replaced, because a requirements document that lowers its bar without saying so is the defect this correction exists to fix.

- **A release runs no tests of its own.** `.github/workflows/release.yml` has two jobs, `image` and `chart`. Neither runs `go test`. Nothing about an NFR gates a tag: the tests run in `.github/workflows/ci.yml`, on pushes to `main` and on pull requests, so a release inherits whatever the merge commit last proved — and nothing re-checks it at publish time.
- **The FR ids are not traceable to anything.** A grep for a standalone `FR-` id across `api/ internal/ cmd/ test/ config/ charts/ hack/` returns **zero** matches; every apparent hit is a substring of `NFR-`. Only **NFR-3** and **NFR-8** are cited in code at all. So no test names the requirement it satisfies, and no requirement names the test that proves it.

**What is true**: some of these are enforced by a named test, and the ones that are say which below. The rest are targets. A requirement with no named test is a target, however it is worded — treat it that way, and add the test rather than the adjective.

## Non-functional (the identity of the platform)

- **NFR-1 Weight**: core control plane ≤ 8 pods. **Enforced** by `TestCorePodBudget` (`test/chart/chart_test.go`), which sums `spec.replicas` over the rendered Deployments and StatefulSets, adds one per DaemonSet, and compares against `const budget = 8`; raising the budget means editing that constant in the same commit, which is what makes the ADR justification unavoidable rather than customary. The second half — *exactly one pod of assayd-authored code in core* — is **not enforced**: the test logs the per-workload breakdown and asserts nothing about who wrote each pod. It is true today because the chart renders one workload, not because anything checks. Architecture §17 carries the target ledger, which sums 8 at its low bound and 9 at its high.
- **NFR-2 Stateful deps**: **the enforced rule is an allowlist of three, not a pair.** `TestStatefulDependencyAllowlist` (`test/chart/chart_test.go`) refuses any rendered `StatefulSet` or `PersistentVolumeClaim` whose name is not `postgres`, `nats` or `openobserve` — the third recorded in the test as "observability sink, not substrate". The original wording was *"Postgres and NATS JetStream are the only stateful dependencies, ever"*, and it is kept here because the gap is the point: **"ever" and a three-entry allowlist cannot both stand.** The distinction the allowlist draws — substrate versus sink — may well be the right one; it has never been written down as a decision, so the requirement and the test have disagreed in silence. Anything else that wants a disk has to be named in the allowlist, in the change that introduces it, and that part works exactly as intended.
- **NFR-3 Runs anywhere**: unmodified `helm install` on any conformant cluster, with no LoadBalancer, cloud-storage or managed-identity assumptions in core. **Two distros are exercised, and only one of them fully.** `hack/e2e.sh` accepts `DISTRO=k3d` and `DISTRO=kind` and exits non-zero on anything else, and `.github/workflows/ci.yml` runs both on every merge; **minikube and k3s are named as targets and nothing runs them.** The earlier wording said CI runs *full* e2e on k3d + kind, which is true of k3d only — the kind lane declares three axes unverified rather than passing them:
    - `ASSAYD_E2E_RESPONDER_SKIP` — the responder image must be pushed to a registry wired into the cluster at create time, and kind clusters are created outside `hack/e2e.sh`. So the tests where a real agent answers, serves its card and completes an A2A task run on k3d only (design 02 §5).
    - `ASSAYD_E2E_GATEWAY_SKIP` — `hack/e2e.sh` installs Gateway API and agentgateway only on k3d, so every gateway test is **unverified** on kind rather than broken.
    - `ASSAYD_E2E_NETPOL_SKIP` — **this one is a property of kind, not a gap in assayd.** NetworkPolicy enforcement belongs to the CNI, and kind's default CNI does not implement it: a policy applied there is accepted and does nothing, so a test asserting it would pass while proving nothing. Skipping and saying so is the honest outcome (design 07 A5.4).

    What kind does cover on every merge: CRD install and admission, workload materialization, the run-namespace and label reservations, env-source protection, operator RBAC, and that the operator under test is the one just built. That is the "runs on another distro" claim this requirement is really making, and it holds.
- **NFR-4 Time-to-first-agent**: empty directory → agent answering from a knowledge graph on a local cluster in **< 15 minutes**.
- **NFR-5 Open standards only**: A2A, MCP, OASF, SPIFFE, OTel GenAI (pinned semconv), Gateway API, CloudEvents, OCI. Zero invented protocols; zero bundled agent framework or UI in core.
- **NFR-6 Extension without release**: a new technique ships as a pack (signed OCI artifact) with no platform release and no agent redeploys.
- **NFR-7 Tiering**: `core` and `plus` independently installable/removable; enterprise modules separate. **`plus` does not render at all today**, so this requirement is unmet and removability is untested — one tier cannot be independent of another that does not exist. `charts/assayd/templates/_helpers.tpl` calls `fail` on `tier: plus`, because that tier would add Argo Workflows, Phoenix, OpenFGA and eval-runner images that are not built, and rendering it as if it worked would be worse than refusing. The refusal itself **is enforced**, by `TestUnimplementedTierIsRefusedNotIgnored` (`test/chart/chart_test.go`) — so what is pinned is that the install fails loudly, which is NFR-8's rule applied to a missing tier rather than a degraded one.
- **NFR-8 Graceful degradation is visible**: any downgraded guarantee (e.g. sandbox fallback on local distros) surfaces as a CR condition, never silently.

## Functional

**No `FR-` id below is cited by any test, or by any code.** Grepping `api/ internal/ cmd/ test/ config/ charts/ hack/` for a standalone `FR-` id returns nothing — every apparent hit is a substring of `NFR-`. So these are a statement of intent with no traceability: you cannot ask which test proves FR-20, and no test says which requirement it serves. Several are implemented in part, and the partial ones are the easiest to misread. FR-1's registration reads the card and wires a gateway route, and issues no identity and publishes no directory entry. **FR-35 is the one to be careful with**: nothing in this repository verifies a signature at all — not an image's, not a card's. `spec.runtime.image` is refused unless it is **digest-pinned** (CEL on the schema, `api/v1alpha1/agent_types.go`), which is a different control and a precondition for the real one, since a signature is verified against a digest. The CRD comment that used to claim "must be cosign-signed; admission rejects unsigned images" shipped verbatim in the generated CRD and is recorded there as false. `CardUnsigned` is set on every Agent, with reason `NoSigningConfigured`, so the card half of the absence is announced rather than silent (NFR-8). The image half is **not** announced: `ImageSignatureUnverified` is declared in the condition vocabulary (`api/v1alpha1/agent_types.go:526`) and **nothing sets it** — the two references in the tree are its declaration and its entry in the vocabulary list. The honest summary of what runs is architecture §18. Read this list as the target surface.


### Agents & runtime
- **FR-1**: An agent is any container serving A2A + an Agent Card; registration wires identity (SPIRE SVID), gateway routes, and an OASF directory entry with no agent-code changes.
- **FR-2**: External A2A endpoints can register as agents without running on the cluster.
- **FR-3**: Agent sandboxing via agent-sandbox CRD profiles (gVisor/Kata), with visible downgrade (NFR-8).

### Knowledge
- **FR-10**: KnowledgeGraph CR binds a provider behind the 6-tool MCP surface (`search, neighbors, get_context_bundle, cite, schema, probe`).
- **FR-11**: Graphs are versioned as immutable snapshots; agents pin versions; `kg diff` renders deltas for review.
- **FR-12**: Semantic readiness — a graph is Ready only when its probe set passes; non-Ready graphs receive no agent traffic.
- **FR-13**: Graph creation paths: (a) knowledge-pattern pack + connector, (b) proposed ontology from samples (propose → human review → lock, never silent), (c) BYO graph wrapped in an adapter.
- **FR-14**: Ingestion pipeline with two gates (ontology invariants; extraction precision/recall vs labeled sample); failing batches quarantine.
- **FR-15**: Knowledge patterns shipped as packs; `sop-decision-tree` produces walkable procedure graphs with `next_step(state)`, auto-derived invariants and per-branch probes.

### Correctness
- **FR-20**: Eval-as-admission — a new Agent/Model version receives no traffic until its EvalSuite passes; pass → gateway-weighted canary → full; fail → rollback with report in CR status.
- **FR-21**: Recorded sessions are replayable and samplable into eval datasets.
- **FR-22**: Drift detectors (KG probes, canary prompts, SLO burn, embedding distance) write CR conditions; controllers remediate (re-ingest, pin model, rollback); recovery is re-verified.

### Security & governance
- **FR-30**: Every agent pod holds a SPIRE SVID; humans authenticate via the IdP slot (Zitadel default, Keycloak profile); user tokens exchange into on-behalf-of agent credentials.
- **FR-31**: Every hop crosses the gateway and emits a receipt (request/response, tools, models, cost, identity chain) to JetStream.
- **FR-32**: Budgets (tokens/$/time) declared on the Agent CR are enforced at the gateway; overruns terminate with a named reason.
- **FR-33**: Loop governance: hop limits, A2A cycle detection via task lineage, `requiresApproval` pauses as durable workflow steps, kill switch.
- **FR-34**: Three authz layers: k8s RBAC + CEL admission (build-time); ReBAC via AuthzProvider slot (OpenFGA default) checked per hop; policy-as-config.
- **FR-35**: Supply chain: signed images/cards/ModelKits; unsigned rejected by admission.

### Exposure & apps
- **FR-40**: `expose` blocks publish Agents (A2A/MCP), Workflows (MCP tools, REST/SSE), KGs (read-only MCP) via the gateway; external consumers are authenticated principals with budgets and receipts.
- **FR-41**: App CR = frontend + generated API (workflow POST endpoints, agent SSE chat) + pinned member versions, released/rolled back as a unit; typed client generated from schemas.

### Workflows
- **FR-50**: Declarative Workflow CRs compile to DBOS programs; code-first DBOS supported; triggers = JetStream events, cron, A2A tasks; local test with injected fixture events.

### Enterprise
- **FR-60**: Tenant CR fans out namespace/vCluster, NATS account, Postgres RLS role, IdP org, gateway partition, quotas.
- **FR-61**: Compliance profiles (hipaa first) as packs: hash-chained receipts + retention, model-egress allowlists at the gateway, mandatory redaction, break-glass with audit.

## Explicit non-goals (v1)
- No bundled UI (CLI first; App frontends are user-owned).
- No proprietary agent framework or protocol.
- No mesh in core (`ambient` ztunnel profile only).
- No Temporal (DBOS; Temporal is the documented escape hatch).
