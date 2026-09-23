# assayd

**A Kubernetes-native platform for running agents you can prove things about.**

An *assay* is the test that establishes whether metal is what it claims before it may be hallmarked. That is the thesis: a revision is evaluated, and **what was evaluated is what runs**, provable by content rather than by convention.

[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

> **Pre-1.0.** Releases are published and signed (`docs/supply-chain.md`), but read *What is true today* before assuming a capability. This project keeps an honest account of what it does and does not enforce, because it has already been burned once by a summary that claimed more than the code did.

## The idea

An agent's identity is the **content** of its resolved spec, not a tag. Change the spec and you get a new revision with a new digest; roll back and you get the exact bytes that were evaluated, not a re-render of today's inputs that happens to carry yesterday's label. Governance — budgets, authentication, which tools an agent may reach — belongs at the **gateway**, so it holds regardless of which SDK or language the agent was written in.

Everything the platform speaks is an existing standard: A2A, MCP, Gateway API, SPIFFE, OCI, OTel. There are no invented protocols.

## What is true today

Measured on a real cluster (k3d) by `make e2e`, not asserted:

- **An agent runs and answers.** The operator deploys it from a registry digest, mints a per-revision Service, and a request gets an answer back.
- **Traffic traverses a real gateway.** Gateway API + agentgateway; a request reaches the agent through the Gateway's own Service, carried by a route.
- **A disallowed principal is refused, by identity, under the policy the operator emits.** Each new API-key Agent's route is published only after an anonymous request through it gets `401`. A caller with a valid key for the wrong group gets `403` while a permitted one gets `200` on the same route — authentication and authorization are separate controls and are measured separately.
- **The gateway can be made the only way in.** Under a hand-authored ingress rule an ordinary namespace cannot reach an agent directly, while the gateway and the operator's card fetch still can.
- **An MCP tool call goes through the gateway**, with an allowlist that removes disallowed tools from discovery entirely.
- **The operator fetches and digests the A2A card the container serves** — the container is the source of truth, not a fixture handed to the operator.

## What is NOT true today

Stated plainly, because a promise nothing enforces is worse than an absent feature:

- **The policy compiler is one slice deep.** With `gateway.enabled`, the operator emits one policy per API-key Agent: API-key authentication and one rule admitting the group named for the Agent's namespace. That is all it compiles. No `AgentgatewayBackend` is emitted, so **no budget, rate limit or tool filter is enforced for any agent.** The MCP allowlist above is authored **by hand**. API keys are shared bearer secrets that an administrator writes and rotates by hand; nothing in assayd issues or expires one.
- **The chart ships no gateway.** `gateway.enabled` defaults to `false`, and on install the chart tells you your agents are ungoverned.
- **No NetworkPolicy is materialized**, so an agent Pod is directly reachable and gateway controls are bypassable by default.
- **Card signatures are not verified**, skills are not cross-checked against grants, and the registration deadline is not counted.
- **The knowledge-graph, eval-gate and drift work is designed, not built.** Designs are hypotheses until their Status line says otherwise.

`docs/designs/02-agent-crd-operator.md` §5 is the authoritative list. `SECURITY.md` repeats it, because someone assessing risk should not have to find it.

## Try it

```bash
make test    # unit, envtest, chart, docs, conformance
make e2e     # k3d: a real cluster, a real gateway, real agents
```

`make e2e` creates the cluster, builds and pushes the fixtures, installs Gateway API and agentgateway, and runs the suite. Needs Docker and k3d. Some tests **skip and report an axis unverified** rather than pass — NetworkPolicy enforcement belongs to the CNI, and on one that does not implement it a policy is accepted and silently does nothing.

**To install on your own cluster**, follow `docs/install.md`: the prerequisites the chart needs before it installs, the published chart, API keys, and one A2A task through the gateway, answered `200`, `401` or `403` by key. What a container must do to run as an Agent is `docs/agent-contract.md`.

## Orientation

| Doc | What |
|---|---|
| `docs/install.md` | Installing with a gateway, API keys, CRD upgrades, one A2A task end to end, and an MCP tool behind the gateway |
| `docs/agent-contract.md` | What a container must do to run as an Agent, what the operator injects, and how it calls an MCP tool |
| `docs/architecture.md` | The full architecture (canonical) |
| `docs/designs/README.md` | Per-component designs **and their real status** — start here |
| `docs/decisions/` | ADRs — every settled decision, with its context |
| `docs/research/` | Dated landscape research, with sources and re-verify dates |
| `AGENTS.md` | How to work in this repo: the rules, the commands, what each test layer can honestly claim |
| `CONTRIBUTING.md` · `GOVERNANCE.md` · `SECURITY.md` | Contributing (DCO), how decisions get made, how to report a vulnerability |

**"Approved" on a design does not mean settled or buildable.** Check the Status line of the document you are about to rely on: a design's own Status line is the source of truth, and `docs/designs/README.md` summarises it (ADR-0030 Amendment 1). The human has approved only design 03's first slice and its amendment A84, and design 16's first slice; every other design is not approved.

## Licence

Apache-2.0 — see [LICENSE](LICENSE). Contributions are under the [DCO](CONTRIBUTING.md#sign-your-commits-dco); there is no CLA.
