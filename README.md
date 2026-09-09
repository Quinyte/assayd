# assayd 🪶

> **assayd** is a neutral internal codename. Renaming before any public release is a tracked task (`docs/decisions/0001`). Early drafts used "graphene".

A radically lightweight, Kubernetes-native agent platform. The domain is supplied as a pluggable, versioned knowledge graph; everything else is an open standard (A2A, MCP, OASF, SPIFFE, OTel GenAI, Gateway API, OCI) with a thin binding.

**Status: design phase.** No code until component designs are complete — see `docs/designs/README.md` for the design backlog and status.

## Orientation

| Doc | What |
|---|---|
| `docs/architecture.md` | The full architecture (canonical, markdown) |
| `docs/architecture.html` | Same doc, rendered with figures (open in a browser) |
| `docs/requirements.md` | Functional + non-functional requirements |
| `docs/decisions/` | ADRs — every locked decision, with context |
| `docs/designs/` | Per-component design docs (the current work) |
| `docs/research/` | Dated landscape research with sources |
| `CLAUDE.md` | How to work in this repo (doctrine, process, skills) |

## The one-slide version

1. Domain = pluggable, versioned **KnowledgeGraph** with semantic readiness.
2. **Eval-gated deployments** — agents and models earn traffic; sessions replay into regression sets.
3. **Drift as k8s conditions** + self-healing controllers.
4. Guarantees enforced **at the data plane** — truly any-SDK.
5. **≤ 8 pods core**, Postgres + NATS only, runs on minikube/k3d/kind/k3s.
6. 100% open standards, zero invented protocols. Extensible via the pattern charter (packs, sockets).
