# Security policy

## Reporting a vulnerability

**Use [GitHub Private Vulnerability Reporting](https://github.com/Quinyte/assayd/security/advisories/new).** It is the primary channel because it needs no mailbox to stay alive and it keeps the report, the fix and the advisory in one place. If you cannot use it, email **engineer@quinyte.com** with `assayd security` in the subject.

Please do not open a public issue, and please do not open a pull request that fixes a vulnerability before it is disclosed — a fix commit is a disclosure, and a careful reader watches commits.

**What helps most**, in rough order: the version or commit, a reproduction, and what an attacker gains. A proof of concept is welcome and is not required; a clear description of the capability is often faster to act on than an exploit.

## What to expect

| | Target |
|---|---|
| Acknowledgement that a human has read it | 3 business days |
| An assessment — affected versions, severity, whether we agree it is a vulnerability | 10 business days |
| Fix or a stated plan with dates | 90 days from the report |

If we disagree that a report is a vulnerability, we will say so and say why, rather than letting it age out.

**Disclosure is coordinated.** We ask for 90 days before public disclosure, extendable by agreement if a fix is genuinely hard, and we will credit you in the advisory unless you ask us not to. If a vulnerability is already being exploited, that clock does not apply — tell us and we will move.

**This project is single-maintainer** (`MAINTAINERS.md`). Response times above are targets held by one person, and are stated so you can calibrate rather than guess.

## What is in scope

The operator, its CRDs, the admission policies and the Helm chart in this repository. Also in scope: anything that lets a principal reach an agent, a tool or a model it was not granted, and anything that lets a workload run bytes other than the ones that were evaluated — those two are the properties assayd exists to hold.

**Out of scope**: agentgateway, SPIRE, Kubernetes itself and the other upstreams — report those to their projects, though we would like to know if assayd's use of one creates a problem the upstream does not have on its own.

## What is NOT yet enforced — read this before assuming a guarantee

assayd is pre-1.0 and a number of things it is *designed* to do are not implemented. Reporting one of these as a vulnerability is welcome but will be answered with "known, and here is the record":

- **The policy compiler does not exist.** No `AgentgatewayPolicy` or `AgentgatewayBackend` is emitted by anything, so token budgets, rate limits, gateway authentication and tool filtering are **not enforced** for any agent.
- **The chart ships no gateway.** `gateway.enabled` defaults to `false`; agents run ungoverned and the chart's `NOTES.txt` says so on install.
- **No NetworkPolicy is materialized**, in any namespace, so an agent Pod is reachable directly and any control a gateway would apply is bypassable.
- **Agent Card signatures are not verified**, advertised skills are not cross-checked against the CR's grants, and the registration deadline is not counted.

`docs/designs/02-agent-crd-operator.md` §5 is the authoritative list, and `CLAUDE.md` states the current position in a paragraph maintained for exactly this purpose. A guarantee this project states in the present tense and does not enforce is a documentation bug, and **we do want those reported** — quietly shipping an unenforced promise is the failure mode this project has already paid for once.

## Supply chain

Release images are signed with keyless [cosign](https://docs.sigstore.dev/) and carry an SBOM. Verify before you run: an image assayd deploys is pinned by digest, because a tag is not content (ADR-0019).

## Regulatory

**assayd is stewarded by Quinyte.**

Under the EU Cyber Resilience Act, an **open-source software steward** is an entity that supports a CRA-in-scope free-and-open-source product *for commercial purposes* without placing it on the market itself. That is Quinyte's position for the Apache-2.0 core: stewards are **not** subject to CRA fines, and carry three duties — a documented policy for secure development and vulnerability handling (this document), cooperation with market-surveillance authorities on request, and reporting of actively exploited vulnerabilities and severe incidents on the manufacturer clock (24-hour early warning, 72-hour notification, 14-day final report after a patch ships).

**A commercial offering would be a different category.** ADR-0015 describes paid enterprise modules. If and when any of those is placed on the EU market, Quinyte is a **manufacturer** for that product, not a steward for it, and the heavier obligations attach — conformity assessment, technical documentation and CE marking, in force from 11 December 2027. The free core does not inherit those by being adjacent to it. Nothing is on the market today.

**This is a stated position, not a legal opinion.** Which category attaches turns on facts about how the project is supported and distributed, and it should be confirmed by counsel before the first commercial release rather than inherited from this paragraph.

Reporting obligations began 11 September 2026 and attach on the day a product is placed on the market. This policy exists so that day is not the day the process is invented.
