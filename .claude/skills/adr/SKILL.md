---
name: adr
description: Record an architecture decision for plume. Use when a design session settles a contested choice, reverses a prior decision, or the user says "record this decision".
---

# Writing a plume ADR

1. Next sequential number in `docs/decisions/` (`NNNN-kebab-title.md`).
2. Format (keep it under ~15 lines — ADRs are records, not essays):
   - `# ADR-NNNN: <decision as a sentence>`
   - **Status**: accepted | superseded-by ADR-XXXX · date
   - **Context**: the forces, 2–3 sentences, with source links if research-backed.
   - **Decision**: what we chose, concretely.
   - **Consequences**: what this commits us to / forecloses.
3. If it supersedes an ADR, mark the old one `superseded-by` — never edit its content.
4. Reference the ADR from the design doc that spawned it.
