---
name: critique-design
description: Adversarial review of a plume design doc BEFORE it can be approved. Use after a design draft is complete, or when the user asks to review/validate/critique a design. A design may not move to approved without a passing critique.
---

# Critiquing a plume design

**Stance**: you are not the author. Hunt for what's wrong; politeness is a failure mode. If this session authored the draft, say so in the report (independence caveat) and prefer a fresh session/subagent when available.

Run every lens; report findings severity-tagged (BLOCKER / MAJOR / MINOR) with file:line and a concrete fix:

1. **Doctrine gate audit** (architecture §01): pods added justified? stateful deps beyond Postgres/NATS? library-over-server violated? weight budget touched?
2. **Charter audit** (§15): correct plane and socket? only the five primitives? anything that should be a pack but landed in the engine?
3. **Contract consistency**: field/condition/tool names consistent with every *approved* design and ADR; references (design numbers, ADR numbers, contract ids) resolve; versioning semantics coherent.
4. **Hidden dependencies & circularity**: does anything depend on a value produced later (hash-before-fetch class bugs)? hidden LLM/network dependencies inside components claimed deterministic?
5. **Failure-mode coverage**: every external call has a failure row; every degradation has a condition (NFR-8); idempotency/crash-resume stated.
6. **Security lens**: every new route/endpoint has an identity + authz story; anything reachable by agents that shouldn't be; secrets handling.
7. **Research freshness**: claims about third-party components verified against current sources (search with current year if stale or unsourced); licenses checked, not recalled.
8. **Testability**: conformance/e2e assertions actually decidable? latency/threshold numbers stated where promised?

**Output**: `docs/designs/reviews/NN-review.md` — verdict `PASS | REVISE`, findings list, independence note. On REVISE: author fixes, re-run. On PASS: only then may the design status become approved and ADRs be recorded. Update the backlog table with the review link.
