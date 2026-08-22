# Venue scan: what it would take, and where

**Date**: 2026-08-22 · **Method**: scan briefed to be blunt about what is not publishable.

## The headline

**Nothing in this repo today is publishable at any peer-reviewed systems venue.** 27 designs + 27 ADRs with zero running code is a tech report. EuroSys rejects it for having no deployment (its Experience track requires "lessons learned from a practical deployment… supported by rigorous and quantitative analysis"); SOSP/OSDI/NSDI/SoCC/MLSys for having no numbers; [MLSys Industrial](https://mlsys.org/Conferences/2026/CallForIndustrialTrackPapers) — the one track with "no requirement for novelty" — disqualifies us twice, since "the first and most of the authors must be from industry" and it demands "detailed benchmarking at scale and/or in a production environment."

Of ~15 venues checked, exactly **one** archival peer-reviewed venue takes a system description with no quantitative evaluation: **HotOS**, and only as a 5-page provocation.

Two corrections to the working assumptions: **USENIX ATC is dead** ([discontinued after 2025](https://www.usenix.org/blog/usenix-atc-announcement)) — remove it from any plan. And KubeCon/SREcon are **not peer-reviewed**; they are the right marketing for a k8s-native platform and worth zero as publication record.

## The evidence bar, from papers that actually got in

[Parrot (OSDI '24)](https://arxiv.org/abs/2405.19888): baselines FastChat over both HF Transformers and vLLM in two configs each; 1×A100-80GB and 4×A6000; LLaMA-7B/13B; workloads including MetaGPT multi-agent programming and synthesized Bing Copilot traces; headline 11.7×.

[Autellix (MLSys '25)](https://arxiv.org/abs/2502.13965): **three** baselines, escalating — and they built the *strong* one themselves so reviewers could not say they beat a strawman. 8×A100, three model scales, workloads characterized by call-graph depth (6.66 → 159.7 LLM calls per program). Headline 4–15×.

The entry ticket: 1–8 A100/H100-class GPUs, 2–3 real baselines, 3–4 trace-driven workloads, one headline multiple. **Architecture novelty alone earns zero.**

## The two findings that change the plan

**1. The Symphony arc is the template.** [*Serve Programs, Not Prompts*](https://arxiv.org/abs/2510.25412) cleared **HotOS 2025** as a pure architectural vision with essentially no measurements; [the same idea with an implementation cleared SOSP 2025](https://arxiv.org/abs/2510.24051) months later. That is exactly the two-step path available here. HotOS is biennial — **HotOS 2027 deadline ≈ mid-Jan 2027**. It wants *one sharp contrarian claim*; a 27-component catalogue gets desk-rejected as a tech report.

**2. The "no benchmark exists" excuse expired this month.** [AgentSysBench](https://arxiv.org/abs/2608.15127) (15 Aug 2026) releases production traces from three applications, names six properties distinguishing agentic from conventional inference workloads, and shows 29–40% latency reductions from the implied design changes. By the time this platform runs, that is what a reviewer will ask why we did not use. **Instrument against it from day one rather than inventing our own metrics.**

## The structural disadvantage, stated plainly

Every strong 2026 agent-systems paper leans on **production traces from a commercial platform** — [SMetric](https://arxiv.org/abs/2607.08565) on Alibaba BAILIAN, [Aries](https://arxiv.org/abs/2607.29069) on a commercial agent platform. Unaffiliated and user-less, we compete on a dimension we cannot win. Either the platform gets in front of real workloads inside an organization, or we aim at venues whose currency is **ideas rather than traces**: HotOS, ICSE NIER, EuroMLSys.

## On the design-process paper

It is a live area, and the framing is well-defined — but the consensus already points our way, which cuts both ways. [*When Can LLMs Actually Correct Their Own Mistakes?*](https://arxiv.org/abs/2406.01297) (TACL) concludes **"no prior work demonstrates successful self-correction with feedback from prompted LLMs"**; [*LLMs Cannot Self-Correct Reasoning Yet*](https://arxiv.org/abs/2310.01798) (ICLR '24) is the canonical negative result. Our observation is *consistent with consensus* — credible, and therefore not novel on its own. The nearest neighbours are [agentic code review at 1.02M PRs](https://arxiv.org/abs/2607.13196) (efficiency gains, **no quality improvement**) and [DRCY](https://arxiv.org/abs/2603.15672), a production multi-agent *design*-review system that is both competitor and citation.

What referees would demand, and we do not have:
1. **A matched-effort control arm.** Independence and effort are perfectly correlated in our data (18.6× length differential). Without it the only defensible claim is "longer reviews find more," which is uninteresting.
2. **n ≥ 20–30 documents**, ≥2 model families, ≥2 seeds, effect size + CI. We have 27 designs — *just barely* enough if re-run under a real protocol.
3. **Ground truth.** Our severity labels were assigned by the same class of system under evaluation — circular. Needs blinded adjudication of which findings were true and material, with Cohen's κ and a false-positive rate. The first reviewer question is "how many of the minor findings were noise?"
4. Order/position effects, self-preference bias, prompt-sensitivity ablation.
5. **Preregistration** would de-risk this substantially; MSR and EMSE run registered reports.

Realistic home: **ICSE 2027 NIER** — 4 pages + 1 ref, strictly enforced, deadline **23 Oct 2026**, wants "groundbreaking new ideas supported by promising initial results" and explicitly does *not* mandate rigorous validation, with a mandatory Future Plans section. Then MSR/EMSE registered report or ASE 2027 for the full study.

## Deadlines that matter

| Venue | Deadline | Shape |
|---|---|---|
| **ICSE 2027 NIER** | **23 Oct 2026** | 4pp; preliminary results acceptable; Future Plans mandatory |
| **HotOS 2027** | ≈ **mid-Jan 2027** | 5pp; one contrarian claim; ~10% accept |
| **EuroMLSys 2027** | ≈ **Feb 2027** | 6pp, EuroSys-colocated; prototype + microbenchmarks clears it |
| ICSE 2027 SEIP | 23 Oct 2026 | needs industrial context we lack |
| SoCC 2027 | ≈ Apr 2027 | needs the running system |
| EuroSys 2028 fall | ≈ Sep 2027 | needs deployment + quantitative analysis |
| arXiv | any day | not a publication; the standard staging ground |
