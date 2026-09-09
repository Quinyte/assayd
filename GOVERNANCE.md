# Governance

assayd is maintainer-governed. This document describes what that means today, honestly, rather than describing a structure the project does not yet have.

## Today: one maintainer

`MAINTAINERS.md` lists one person at one company. Decisions are made by that maintainer, in the open, and recorded — which is the part that makes single-maintainer governance survivable for a while and is not optional here.

**This is a stated risk.** A project with one maintainer and one company behind it is a bus factor of one and has no independent review. The precedent worth knowing: Weaveworks, the commercial steward that coined GitOps, shut down in February 2024, and Flux survived only because its maintainership was already spread across several companies. Recruiting maintainers from outside Quinyte is a precondition for a credible foundation submission, not a later nicety.

## How decisions are recorded

Every contested or reversible choice becomes an **ADR** in `docs/decisions/`: the context, the decision, and what it commits us to. An ADR is superseded, never edited — `superseded-by` points forward and the original text stays. This is the mechanism, not a formality: when two designs contradicted each other on whether an omitted tool allowlist grants everything or nothing, neither document was allowed to settle it, and ADR-0032 did.

Component designs live in `docs/designs/` and carry their own **Status** line, which is authoritative over any summary of it. Reviews in `docs/designs/reviews/` are history and are never edited to match a later decision.

**Amendments are numbered and kept.** Evidence against a decision is addressed in the text, never deleted. Several documents here carry a paragraph withdrawing an earlier claim of their own; that is the intended behaviour.

## How changes are made

Pull request, reviewed by a maintainer, merged when the tests pass and the claims hold. `CONTRIBUTING.md` has the three rules that actually get changes sent back.

There is no lazy-consensus window, no voting, and no committee, because with one maintainer those would be theatre. They arrive with the second and third maintainers.

## Adding maintainers

Existing maintainers invite, on sustained contribution and demonstrated judgement — specifically the judgement `CONTRIBUTING.md` describes, which is about evidence rather than volume. A maintainer who becomes inactive for an extended period moves to emeritus and can return on request.

## Foundation

The intended home is the **Linux Foundation's Agentic AI Foundation (AAIF)**, recorded in ADR-0015 Amendment 1. The reason is dependency alignment rather than prestige: agentgateway — assayd's pinned data plane — and MCP are both governed there, so assayd's two hardest external dependencies sit together somewhere assayd does not.

Submission is **not imminent** and has named preconditions, each currently unmet: maintainers from more than one company, a trademark search cleared on the name (a foundation expects to receive a clean mark, and a free domain is not clearance), and a signed contribution agreement. Apache-2.0, which either venue requires, is already in place.

## Code of conduct

`CODE_OF_CONDUCT.md`, enforced by the maintainers. Reports go to the address in that document.

## Security

`SECURITY.md`. It also lists, deliberately, the guarantees this project states and does not yet enforce — read that section before assuming one.
