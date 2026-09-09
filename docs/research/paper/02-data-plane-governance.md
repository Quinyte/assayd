# Prior-art scan: data-plane agent governance — NOT NOVEL (all four sub-claims)

**Date**: 2026-08-22 · **Method**: adversarial scan briefed to refute. Search quota ran out after 4 queries; the rest was direct WebFetch against arXiv and RFC/vendor docs, so academic and standards coverage is good and the vendor-blog long tail (Kong, Cloudflare, Helicone, Portkey) is thin.

## Verdicts

| Sub-claim | Verdict | Killed by |
|---|---|---|
| (a) token/cost budgets at the proxy | **NOT NOVEL** | `EnterpriseAgentgatewayBudget` — per-day token *and* USD budgets, as a k8s CRD, in an Envoy-class data plane, with a terminal Block action ([Solo.io](https://www.solo.io/blog/from-tokens-to-budgets-cost-management-in-solo-enterprise-for-agentgateway)) |
| (b) stateless in-proxy lineage loop governance | **NOT NOVEL as mechanism**; a narrow transposition at best, and *materially weaker than claimed* | [RFC 8586 `CDN-Loop`](https://www.rfc-editor.org/rfc/rfc8586.html) + [Data443](https://data443.com/blog/agent-to-agent-proxy-security/) |
| (c) single-use voucher / durable typed pending | **NOT NOVEL** | [MojoAuth HITL patterns](https://mojoauth.com/blog/human-in-the-loop-authorization-patterns-for-autonomous-agents) — single-use consumption, CIBA durable pending, retryable `authorization_pending`, gateway-side consumption |
| (d) receipts, deterministic IDs, SPIFFE identity, hash chain | **NOT NOVEL** | [aiAuthZ](https://arxiv.org/abs/2607.05518) — off-host gateway, single-use nonces, verifiable receipts, SHA-256 hash-chained audit log |
| **core thesis** (data plane, not SDK) | **NOT NOVEL** | [OpenPort](https://arxiv.org/abs/2602.20196) "model- and runtime-neutral server-side gateway"; [Proof-Carrying Agent Actions](https://arxiv.org/abs/2606.04104) runtime-neutral governance across "local tools, SDKs, hosted platforms, and API gateways" — this is the *consensus position* of the 2026 agent-security literature |

## (b) was the strongest candidate. Four attacks landed.

1. **The mechanism is an RFC.** RFC 8586 is claim (b) with "CDN" for "agent": a gateway-owned header carrying hop lineage, each intermediary appending its own id and denying if its id already appears, stateless, no cycle-detection service, explicitly not client-modifiable. "The lineage IS the state" is the CDN-Loop design rationale verbatim.
2. **The stripping is Envoy doctrine.** Gateway-owns-and-strips-client-supplied-trust-headers is `x-request-id` sanitization and `xff_num_trusted_hops`, canonical since ~2017.
3. **Opt-in occurrence-counted reentry is BGP `allowas-in`.** BGP's default is any-revisit-denied (reject if own ASN in AS_PATH); `allowas-in <1-10>` is exactly opt-in reentry with an occurrence count, shipped for ~two decades. *(The scan could not fetch a vendor URL for this before its quota ran out — verify before citing.)*
4. **The technical one — see below.**

## Attack 4 is a live defect in design 22, not just a paper problem

AS_PATH works because BGP propagation is a **path**. Agent call graphs are **trees with concurrency**. A gateway-owned lineage header describes only the **ancestor chain** of one request, so it bounds *ancestral* revisits and nothing else.

Two sibling branches that each legitimately call agent X once produce two lineages each containing X once. No stateless check can see that X ran twice. A breadth-2, depth-6 fan-out where each leaf re-enters a critic agent once stays inside `maxHops` **and** inside any `maxVisits` occurrence budget — while invoking that critic **64 times**.

Design 22 §1 states its purpose as "multi-agent runaway stopped at the data plane" and §3 as "the lineage *is* the state." Both are true for the **ancestor relation** and false for **volume**. Volumetric runaway is bounded only by the per-day budget, which design 22 §1 explicitly puts out of scope — and a per-day budget can be exhausted in seconds by a fan-out storm, which is precisely the incident class in [arXiv:2606.04056](https://arxiv.org/abs/2606.04056) (63 catalogued LLM-agent budget-overrun incidents; "a single retry loop can spend thousands of dollars").

**This is a design finding independent of the paper question.** It needs an amendment to design 22: state that lineage governance bounds *cyclic* runaway, that *volumetric* runaway is bounded only by budgets, and that fan-out amplification is a known uncovered case — or add a mechanism that covers it (which would need shared state, forfeiting the design's central advantage). See `docs/designs/22-loop-governance.md` §3, and D2.

## If (b) were still to be published

Narrowest defensible claim: bounding **ancestral** delegation cycles with zero shared state via per-callee-identity occurrence budgets in-proxy over a gateway-owned lineage header — transposing RFC 8586 and BGP `allowas-in` to agent delegation, where published alternatives are stateful per-task call graphs (Data443) or framework-internal depth caps (LangGraph `recursion_limit`, CrewAI `max_iter`, AutoGen `max_turns`) — **with the concurrency limitation characterised, not hidden**.

Frame as systems engineering with a stated limitation, never as a new primitive, and cite RFC 8586 / `allowas-in` / RFC 5321 §6.3 / RFC 3261 §16.6 in the first two paragraphs. *A reviewer who finds CDN-Loop before you cite it will reject on that alone.*

Required demonstrations: (1) a topology where framework depth caps and a global hop counter both pass and per-identity occurrence budgeting is the only thing that halts it — if it cannot be constructed, (b) has no reason to exist; (2) the fan-out amplification table — fix depth, sweep branching factor, plot invocations against what the lineage check observed, then show budgets closing the gap; (3) header-integrity under a prompt-injected agent that forges, truncates, or replays lineage, including the confused-deputy case where A launders a call through B to shed its lineage entry; (4) header growth bound and fail-open-vs-closed behaviour at the size limit (fail-open here is total bypass); (5) p99 latency vs the stateful call-graph baseline, since "no shared state" is the only claimed advantage.

## Consequence for assayd

Architecturally the bindings are *validated* — this is "bind, don't build" working. Two actions:

1. **Amend design 22** for the fan-out gap (a real defect, above).
2. **Stop calling data-plane enforcement a differentiator** in external writing. Claim *integration* — one identity and one enforcement point across budgets, approvals, lineage, receipts — and expect a reviewer to note that OpenPort and Proof-Carrying Agent Actions already claim that integration too.
