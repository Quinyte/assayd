# Is there a paper here? — scope decision

> **DECIDED 2026-08-22: not pursuing a paper.** The platform is the goal; a paper is
> opportunistic — if something genuinely new is invented while building, it gets
> written up then. The scans below are why: the architecture is well-precedented,
> and a project optimizing for publishability would start choosing mechanisms for
> how well they compare rather than how well they work, which is the opposite of
> what "bind, don't build" asks for. Nothing here is wasted: the scans caught a
> live defect in design 22 and two false claims in the corpus, and the one
> instrumentation item worth keeping (structured gate-decision records) is
> justified by debugging alone.

**Date**: 2026-08-22 · Four adversarial prior-art scans, each briefed to **refute** rather than support. Details in `01`–`04` alongside this file.

## Verdict on the three technical claims

| Claim | Verdict | Killed by |
|---|---|---|
| eval-as-admission | **NOT NOVEL** | Flagger implements the sequence verbatim; TFX owns gate-on-quality |
| data-plane governance (budgets · lineage · approvals · receipts) | **NOT NOVEL**, all four | agentgateway budgets · RFC 8586 · published HITL voucher patterns · aiAuthZ. The *core thesis* — enforce in the data plane, not the SDK — is the consensus position of the 2026 agent-security literature |
| versioned KG contract | **PARTIALLY NOVEL** — composition only | Snowstorm serves SNOMED CT at version-in-path URLs; SPARQL Service Description standardized declared profiles in 2013; "structural not policed" is capability security restated |

The pattern is consistent and worth stating plainly: **this platform's architecture is well-precedented, which is ADR-0003 working exactly as designed.** "Bind, don't build" produces a system made of other people's proven ideas. That is a good platform and a bad paper, and those are not in tension — they are the same fact seen from two directions.

## What survives, and what it would cost

Three narrow claims survived, ranked by cost-to-decide rather than by appeal:

**1. Cross-version contamination (from the KG scan) — the cheapest decisive test, and it does not require plume to exist.**
> Making KG-version selection unaddressable in the agent's tool vocabulary eliminates a class of failure that a version *parameter* does not.

Three MCP server variants (version-as-argument · overridable default · version-scoped endpoint), one task set, one number: what fraction of episodes contain a call resolved against a non-pinned version? The endpoint variant is 0% by construction, so **the interesting number is how large the other two are.** If the parameterized design is already ~0%, the claim is unmotivated and there is no paper — and we would have learned that design 01 solves a non-problem, which is worth knowing on its own. Weeks of work, no cluster, no operator. Adjacent results establish the harm is real ([FiscalQA Pro](https://arxiv.org/abs/2608.09393): static RAG retrieves the date-applicable version 0% of the time) while leaving this exact gap untested ([MCPEvol-Bench](https://arxiv.org/abs/2607.14642) measures *unannounced* evolution, not *announced-but-nameable* choice).

**2. Stochastic gates as a controller concern (from the eval scan).** Every existing gate assumes determinism; Flagger's failure *threshold* is a noise filter for infra flakes, so applied to a stochastic eval it becomes a bias amplifier — retry until pass. Needs the controller built, plus ≥30 rollouts × K seeded-regressed revisions against a Flagger baseline. Months.

**3. Ancestral-only lineage, honestly characterized (from the governance scan).** Weakest of the three, and its own scan advises framing it as systems engineering with a stated limitation, never as a primitive. Its real value was diagnostic — see below.

## The best thing these scans produced was not a paper

The governance scan found a **live defect in design 22**. A lineage header describes only an ancestor chain, so `maxHops` and `maxVisits` never see concurrent sibling fan-out: a breadth-2, depth-6 topology re-entering one agent per leaf passes every check while invoking it 64 times. §1's "multi-agent runaway stopped at the data plane" was false for volumetric runaway, which only budgets bound — and §1 puts budgets out of scope. **Amended (design 22 A1).** ADR-0006's "nobody expresses them as k8s rollout mechanics" and its 12–18-month first-mover window were both simply wrong; **corrected.** Design 01 now cites Snowstorm and SPARQL Service Description, and states the narrower true claim (an agent has no *vocabulary* to name another version — [capability URLs leak](https://www.w3.org/TR/capability-urls/)).

Running adversarial prior-art scans against your own novelty claims is worth doing **whether or not a paper follows**.

## The one asset that is finished — and its fatal flaw

The review corpus: **179 findings across 29 independent design-critique passes** (4 blocker / 69 major / 106 minor), extracted by `extract_reviews.py` in this directory, not by hand.

*(Correction: an earlier grep-based count of ~370 findings was wrong — it counted every occurrence of the word "MAJOR" in prose rather than finding headers. The venue scan was briefed with the bad numbers; its conclusions do not turn on them.)*

The paired within-design comparison is **n=2**, because only designs 01 and 02 were ever self-reviewed:

| design | pass | findings | blockers | lines | verdict |
|---|---|---|---|---|---|
| 01 | shared context | 4 | 0 | 11 | PASS |
| 01 | independent | 10 | **1** | 205 | REVISE |
| 02 | shared context | 4 | 1 | 11 | PASS |
| 02 | independent | 10 | 0 | 205 | REVISE |

2.5× the findings — but **18.6× the length**. Independence and effort are perfectly confounded. Partial defence: the 25 independent *first-pass* reviews averaged 83 lines yet still found 6.0 findings each, so depth alone does not explain the gap; against that, findings correlate with length at **r = 0.63** across those 25.

And the consensus already agrees with us, which removes the novelty: [*When Can LLMs Actually Correct Their Own Mistakes?*](https://arxiv.org/abs/2406.01297) (TACL) concludes "no prior work demonstrates successful self-correction with feedback from prompted LLMs." Our observation is *consistent with* established results — credible, therefore not new. The contribution would have to be the artifact and the matched-effort protocol, not the direction of the effect.

## Recommendation

**Do not write a paper now. Run the cross-version experiment (#1) instead** — two to three weeks, no dependency on the build, and it returns a number that either creates a paper or kills a design assumption. That asymmetry is why it goes first.

Then, in order:
- **Build the platform**, instrumented against [AgentSysBench](https://arxiv.org/abs/2608.15127) from day one. The "no benchmark exists" excuse expired this month; by the time this runs, that is what a reviewer will ask why we skipped.
- **HotOS 2027** (≈ mid-Jan 2027, 5pp, one contrarian claim) if the build yields a single sharp measured observation. [*Serve Programs, Not Prompts*](https://arxiv.org/abs/2510.25412) cleared HotOS as a vision with no measurements and [the implementation cleared SOSP months later](https://arxiv.org/abs/2510.24051) — that arc is the template. A 27-component catalogue gets desk-rejected.
- **ICSE 2027 NIER** (23 Oct 2026, 4pp) for the review-process work *only if* the matched-effort arm is run first. Submitting n=2 with a known 18.6× confound and a finding that matches consensus is a weak paper, and the deadline is 9 weeks out.

**Not worth pursuing**: the platform paper before there is a running system with traces (every strong 2026 agent-systems paper leans on production traces from a commercial platform — a dimension an unaffiliated author cannot win); an SLR (4–8 months solo, crowded); MLSys Industrial (requires industry authorship *and* production benchmarking); USENIX ATC (**discontinued after 2025**).

**Not publishing**: KubeCon and SREcon are not peer-reviewed. They are the right marketing for a k8s-native agent platform and worth zero as publication record. Worth doing for adoption; never counted as a paper.
