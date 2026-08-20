# Landscape research — 2026-08 (sources cited; re-verify before relying on this after ~2026-11)

## Closest systems (none is the whole thesis)
- **kagent** (CNCF sandbox, Solo.io; v0.10-rc): Agent CR = prompt+tools+LLM config run by its own ADK engine; MCP tools; UI; ops-agent focus. No domain contract, no eval gating, no drift, no budgets/receipts. https://github.com/kagent-dev/kagent
- **Rossoctl** (ex-Kagenti, IBM; v0.6): framework-neutral intercept data plane, A2A+MCP, SPIFFE, sandboxes, skills/memory/KB services, UI-led, heavy (16GB/4-core dev). https://github.com/rossoctl/rossoctl
- **Dapr Agents** (v1.0 GA 03/2026): Python framework on Dapr Workflows/Actors; scale-to-zero. A framework, not a BYO platform. https://github.com/dapr/dapr-agents
- **Palantir AIP**: ontology-first agents, proprietary — validates the thesis commercially. OSS coverage is a stitched stack (~70–80% of ontology capability). https://dataworkers.io/resources/palantir-ontology-open-source-alternative/

## Standards (settled)
- **A2A** under Linux Foundation; v1.0.1 05/2026; 150+ orgs; Agent Cards. https://www.linuxfoundation.org/press/a2a-protocol-surpasses-150-organizations-lands-in-major-cloud-platforms-and-sees-enterprise-production-use-in-first-year
- **AGNTCY/OASF** (Cisco→LF): capability schemas + agent directory. https://www.linuxfoundation.org/press/linux-foundation-welcomes-the-agntcy-project-to-standardize-open-multi-agent-system-infrastructure-and-break-down-ai-agent-silos
- **agentgateway** (LF, Rust, Gateway-API-conformant; MCP/A2A/LLM native; k8s CRDs). Istio announced experimental agentgateway support at KubeCon EU 2026. https://agentgateway.dev/
- **agent-sandbox** (kubernetes-sigs, SIG Apps): Sandbox CRD, gVisor/Kata backends. https://github.com/kubernetes-sigs/agent-sandbox
- **SPIFFE/SPIRE**: assumed 2026 standard for agent workload identity. https://stacklok.com/blog/agentic-identity-explained-how-to-apply-spiffe-and-relationship-based-authorization-to-ai-agents-in-2026/
- **OTel GenAI semconv**: still pre-stable mid-2026 (all gen_ai.* marked Development) — adopt + pin version. https://dev.to/azena-ai/opentelemetrys-genai-semantic-conventions-are-not-stable-yet-heres-what-actually-shipped-in-2026-3mke

## Durability / messaging
- DBOS = library on Postgres, 0 infra; Temporal mature but heavy; Restate middle. 2026 consensus: DBOS first, Temporal when you hit the wall. https://devstarsj.github.io/2026/04/03/durable-execution-temporal-restate-dbos-distributed-workflows-2026/
- NATS: CNCF graduated; Synadia NATS Agent Protocol + Agents SDK (2026). https://nats.io/blog/nats-native-protocol-for-ai-agents/

## Knowledge graphs / memory
- Graphiti (bitemporal, MCP server), cognee (top of 2026 memory benchmarks), LightRAG (cheap ingestion), TrustGraph (ontology-driven "Context OS"), Neo4j GraphRAG (scale). All expose MCP → provider-slot design. https://cognee.ai/blog/deep-dives/knowledge-graph-memory-benchmarks · https://trustgraph.ai/

## Evals / drift
- Eval-gated canary is 2026 best-practice as SaaS/CI/gateway hooks — NOT as k8s primitives (plume's window, est. 12–18mo). https://futureagi.com/blog/agent-rollout-strategies-2026/
- Market moves: OpenAI acquired Promptfoo; Braintrust $80M B. OSS: DeepEval, Inspect AI, Phoenix (OTel-native, embedding drift). https://www.braintrust.dev/articles/deepeval-alternatives-2026

## Identity / authz / tenancy / compliance
- Keycloak = CNCF incubating; Zitadel = Go binary on Postgres, orgs-native, AGPL since 2025; Dex = federation broker. https://skycloak.io/blog/keycloak-vs-zitadel-comparison/
- OpenFGA (CNCF) vs SpiceDB (Zanzibar-purist; OpenAI ChatGPT Enterprise, tens of billions of permissions). https://www.pkgpulse.com/guides/openfga-vs-permify-vs-spicedb-zanzibar-authorization-2026
- Istio ambient GA, ztunnel ~90% less memory vs sidecars. https://dev.to/rex_zhen_a9a8400ee9f22e98/service-mesh-in-2026-the-landscape-has-changed-istio-ambient-mode-update-179m
- Tenancy: vCluster dominant for hard isolation; Capsule for soft. https://northflank.com/blog/kubernetes-multi-tenancy
- HIPAA 2026: BAA + ZDR + encryption + tamper-evident audit (6yr) + PHI minimization + per-action attribution. https://www.swfte.com/hipaa-ai
- Licensing: Apache 2.0 + open-core; avoid BSL (2024–26 relicensing backlash). https://oneuptime.com/blog/post/2026-02-14-open-source-vs-open-core/view

## Modelhub
- KServe (CNCF) + Triton/vLLM; Kueue; llm-d for single giant models; Kubeflow Trainer v2 TrainJob (Unsloth landing as BuiltinTrainer); KitOps/ModelPack (CNCF) OCI packaging. https://blog.kubeflow.org/trainer/intro/ · https://github.com/kitops-ml/kitops
