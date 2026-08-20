# CLAUDE.md — plume

plume ("graphene" in early drafts) is a lightweight, Kubernetes-native agent platform. **We are in the design phase: no implementation code is written until component designs are complete and approved.** The current work is producing design docs in `docs/designs/`.

## Ground rules

1. **Read `docs/architecture.md` before designing anything.** It is canonical. `docs/decisions/` holds the ADRs behind it — do not relitigate an ADR casually; propose a superseding ADR instead.
2. **The lightweight doctrine gates every design** (architecture §01): bind don't build · Postgres+NATS only · library over server · reconcile loop is the product · ≤8 pods core · tiered install.
3. **The pattern charter gates every feature** (architecture §15): name the plane (slow=engine / fast=technique) and the socket (provider slot / gateway filter / new CRD / template+skill). Five primitives only: Agent(A2A), Tool(MCP), Event(CloudEvents), Resource(CRD), Artifact(OCI). A feature needing a sixth primitive or an engine change requires an RFC.
4. **Research must be current.** Never answer landscape/library questions from memory — search with the current year, cite sources, and land findings in `docs/research/` (dated files).
5. **Naming**: "plume" is a throwaway codename. Never let it leak into contract names that are expensive to change without flagging it (API groups, module paths get finalized at rename).
6. **The user decides, with reasoning shown.** Wizards, designs, and proposals present options with trade-offs and a recommendation — never a silent choice, never a blank menu.

## Process: designing a component

Use the `design-component` skill (`.claude/skills/design-component/`). Every design doc follows `docs/designs/TEMPLATE.md` and must pass the doctrine + charter gates above. Update the status table in `docs/designs/README.md` when a design starts/completes.

Decisions made while designing → record with the `adr` skill (`docs/decisions/`, next sequential number).

## Repo conventions

- Docs are markdown; diagrams as mermaid fences or referenced figures in `docs/architecture.html`.
- Commit style: conventional commits (`docs(designs): ...`, `docs(adr): ...`).
- One design doc per component from the component plan (architecture §12).
