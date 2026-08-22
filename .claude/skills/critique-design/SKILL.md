---
name: critique-design
description: Adversarial review of a plume design doc or ADR before it can be approved. Use after a draft is complete, when the user asks to review/validate/critique a design, or before amending an approved design. A design may not move to approved without a passing critique.
when_to_use: design review, critique, validate a design, approve a design, "is this design right", before amending an approved design
context: fork
background: false
effort: xhigh
---

Review the design named in the arguments. You are reading it cold, without the author's context — which is the point: the author's own review systematically passes what an independent pass catches. Every blocker this corpus has recorded was found this way, and the author had already self-reviewed each one.

Assume the design is wrong and find where. Politeness that softens a finding is a failure.

## Read before judging

The design, the ADRs it cites, the designs it depends on, and any `docs/designs/reviews/` file for it. Grep the corpus for every claim about another design — "design 24 mints the tuple", "admission rejects this" — and verify it against that design's actual text. Cross-design claims are where errors hide, because nobody owns both sides.

Run things. Read the generated artifacts, not the prose about them. If a rule is claimed to be enforced, find the enforcement and delete it to check that something fails; if nothing fails, the rule is decorative. Restore by copying a backup file, never `git checkout` — the working tree may hold uncommitted work.

## Attack list

- **Claims with no mechanism.** A prose "must" that no schema, admission rule, controller, or test enforces.
- **Stated premises.** Grep for the evidence. A premise that was true when written may have been falsified by a later design — or by an edit that deleted the counter-example.
- **Tests that cannot fail.** The commonest defect in this repo. A test asserting a substring exists somewhere in a file, or counting the lines of a fixture defined above the assertion, proves nothing. Prove it by mutation.
- **Capability shipped under another banner.** A field added "for ergonomics" that changes what a principal can reach. Ask of every new field: who can now name what, and what derives authority from that name?
- **Fail-open windows.** Ordering where a route, policy, or grant exists before its guard does.
- **Determinism.** Any identifier, hash, or dedup key that depends on something not knowable at the time it is computed.
- **Silence.** A degraded path that does not raise a condition (NFR-8).
- **Doctrine.** Six rules: bind don't build · Postgres+NATS only · library over server · the reconcile loop is the product · ≤8 core pods · tiered install.
- **Readability.** The `write-spec` rules. A spec a newcomer cannot follow is a defect, not a style preference.

## Output

`BLOCKER` / `MAJOR` / `MINOR` findings, each with `file:line`, what breaks concretely, and a specific fix. Then a verdict line: `<design>: PASS — n outstanding` or `<design>: REVISE`.

Say plainly where the work is right, but only after you have genuinely tried to break it, and never as a cushion around a finding. If the whole change is wrong, say the whole change is wrong.
