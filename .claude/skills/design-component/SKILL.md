---
name: design-component
description: Produce or revise a plume component design doc. Use when the user asks to design a component, pick the next design, or review a design. Enforces the lightweight doctrine and pattern-charter gates.
when_to_use: design a component, pick the next design, draft a design doc
paths: docs/designs/**
---

# Designing a plume component

1. **Load context**: read `docs/architecture.md` (at minimum §01, §12, §15 and the sections covering this component), the relevant ADRs in `docs/decisions/`, and any existing design docs it interfaces with. Check `docs/designs/README.md` for dependency order — contracts before consumers; if the user didn't name a component, propose the topmost not-started item.
2. **Research freshness**: if the design binds a third-party component (gateway, provider, runner…), verify its current state with a web search (current year in the query) before committing to its API. Land findings in `docs/research/` if they change anything.
3. **Write the doc** at `docs/designs/NN-<component>.md` following `TEMPLATE.md`. The doctrine/charter gate section is mandatory and must be honest — a design that adds a pod, a stateful dep, or a sixth primitive needs explicit justification or an RFC note.
4. **Design the interface first**: CRD spec/status/conditions and contract versions get more care than internals. Show 2–3 alternatives considered with a recommendation and reasoning — the user chooses; never silently pick for contested calls.
5. **Critique gate (mandatory)**: run the `critique-design` skill on the draft. A design may not be marked approved without a PASS review in `docs/designs/reviews/`. Prefer an independent pass (fresh session/subagent) over self-review; note the independence caveat either way.
6. **Close the loop**: update the status table in `docs/designs/README.md` (with review link); record decisions via the `adr` skill; list follow-on design impacts.

Style: concrete YAML/schema sketches over prose; mermaid only where a picture shows a mechanism; failure modes and degraded states are not optional sections.
