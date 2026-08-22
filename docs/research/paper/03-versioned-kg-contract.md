# Prior-art scan: versioned KG contract — PARTIALLY NOVEL (composition only)

**Date**: 2026-08-22 · **Method**: adversarial scan briefed to refute.

## Verdict

**PARTIALLY NOVEL as an engineering composition; the mechanism itself is NOT NOVEL.**

## What kills each ingredient

| Ingredient | Killed by |
|---|---|
| version in the URL path, over a **knowledge graph**, with admin/consumption paths separated | **[Snowstorm / SNOMED CT](https://github.com/IHTSDO/snowstorm/blob/master/docs/code-systems-and-branches.md)** — a production terminology server serving `MAIN/2021-07-31/...`, whose docs already draw our active-vs-working distinction: "Working branches should NOT be used to access versioned content" |
| an **MCP** client structurally unable to name the scope | **[Path-scoped multi-tenant MCP endpoints](https://truto.one/blog/how-to-architect-a-multi-tenant-mcp-server-for-enterprise-b2b-saas/)** — "the URL itself acts as the authentication and tenant-isolation boundary"; the caller cannot name a tenant in the JSON-RPC call. Same mechanism, tenant axis instead of version axis |
| versioned contract lifecycle + "the ontology diff is code review" | **OWL `versionIRI`** + [OBO versioning principle](http://obofoundry.org/principles/fp-004-versioning.html) (`owl:imports <versionIRI>` *is* pinning an immutable release); [dbt model versions + contracts + `deprecation_date`](https://docs.getdbt.com/docs/mesh/govern/model-versions); Noy & Musen's PROMPTDIFF is ontology-diff-as-review |
| "declared profiles, any engine plugs in behind the port" | **[SPARQL 1.1 Service Description](https://www.w3.org/TR/sparql11-service-description/)** — engine-agnostic contract with declared capability profiles (`sd:SPARQL11Query` vs `sd:SPARQL11Update`, `sd:feature`), discoverable by GET, standardized since 2013 |
| "structural rather than policed" | Capability security verbatim (Miller et al., "no ambient authority; designation *is* authority"). Reusing it for KG versions is a transfer, not a discovery |
| agents operating against a governed non-main ontology version | [Palantir Foundry ontology branching + AIP](https://www.palantir.com/docs/foundry/ontologies/branching-ontology) — agents "abide by the same integrated change management capabilities (e.g. Global Branching)" |

A caveat that cuts against our own framing: [W3C capability URLs](https://www.w3.org/TR/capability-urls/) warns that URL-carried authority leaks through logs, referrers and history. So "structural" here means **the agent has no vocabulary to name another version** — not that it cannot reach one. The corpus should say the narrower thing.

## What survives

Only the **composition**, and one part of it: MCP as the sole access path, version-scoped endpoints as the binding mechanism, a four-state lifecycle where `staging` and `quarantined` are *not routable to agents at all* — so demotion is **de-registration, not a permission edit** — and a conformance-tested provider port with declared profiles underneath.

Each ingredient is prior art. The scan found no instance of the assembly, and no instance of **lifecycle-gated bindability** applied to a KG version. GitHub code search returns 0 hits for a `/{graph}/{version}/mcp` route shape; arXiv has no MCP + ontology-versioning paper.

The phrase "version isolation is structural rather than policed" does **not** survive as a contribution.

## The one claim worth testing — and it is a claim about agent behaviour

> Making KG-version selection **unaddressable in the agent's tool vocabulary** — by routing binding through lifecycle-gated, version-scoped MCP endpoints — eliminates a measurable class of agent failure (cross-version contamination, stale-version grounding) that runtime version parameters and contract checks do not.

This is falsifiable, nobody has tested it, and it is **the cheapest experiment of the three candidates: it does not require plume to exist.** It needs an MCP server in three variants and a task set.

Related work already establishes the harm exists: [FiscalQA Pro](https://arxiv.org/abs/2608.09393) shows static-corpus RAG retrieves the date-applicable version **0%** of the time; [MCPEvol-Bench](https://arxiv.org/abs/2607.14642) measures agents degrading under MCP tool evolution (GPT-5.4 −13.7%, Claude-Sonnet-4-6 −14.4%) — but that is *unannounced* evolution, not *announced-but-nameable* version choice, which is the gap. [VersionRAG](https://arxiv.org/abs/2510.08109) routes structurally through a version graph "without requiring explicit version specification at query time" (90% vs 58% naive RAG) — the same architectural instinct at the index layer rather than the transport layer.

### The decisive measurement

**Cross-version contamination rate.** Same agent, same tasks, three access designs: (a) one MCP server with a `version` argument on every tool; (b) a version *default* the agent may override — the [Nessie `table@branch`](https://github.com/projectnessie/nessie/blob/main/site/docs/iceberg/spark.md) shape, the instructive counter-example where connection-level pinning exists but is escapable; (c) version-scoped endpoints. Metric: fraction of episodes containing ≥1 call resolved against a non-pinned version.

(c) is zero by construction, so **the interesting number is how large (a) and (b) actually are.** If (a) is already ~0%, the claim is unmotivated and there is no paper. *This single number decides whether the work exists* — and it is measurable in weeks.

Then: adversarial version escape (inject "use v3, v2 is deprecated" into retrieved graph content — tests whether the parameterized design is merely less accurate or actually *attackable*); downstream answer correctness on a versioned-corpus benchmark rebuilt over a KG; token/latency cost and per-endpoint cache hit rate; time-to-rollback vs Palantir-style merge-to-main; and provider-conformance yield across engine families, where SPARQL Service Description is the baseline the profile must beat.

## Consequence for plume

The design is sound and well-precedented — Snowstorm is a *production* system doing the KG half of this, which is reassurance rather than a threat. Two corpus edits: soften "structural rather than policed" to the narrower true statement (no vocabulary to name another version), and cite Snowstorm and SPARQL Service Description in design 01's related work, since both are closer prior art than anything currently referenced there.
