---
name: audit-docs
description: Mechanical consistency audit of the assayd docs tree — cross-references, status tables, naming. Use before commits that touch multiple docs, or when the user asks to check docs consistency.
when_to_use: check docs consistency, before a multi-doc commit, audit the corpus
paths: docs/**
---

# Docs audit checklist

1. `docs/designs/README.md` status table matches each design doc's actual Status line.
2. Every ADR referenced by a design exists; every design referenced by an ADR exists; numbering has no gaps/dupes.
3. Contract ids (`kgp/v1alpha1`, `ontology/v1`, pack schema versions) written identically everywhere (grep).
4. Condition names (`Ready`, `KnowledgeStale`, `SandboxDowngraded`, …) consistent across designs/architecture (grep for near-misses).
5. Superseded ADRs marked `superseded-by`; no design cites a superseded ADR as current.
6. Codename hygiene (ADR-0001): "assayd" not baked into anything expensive (finalize-at-rename items flagged as such).
7. research/ files past their re-verify date flagged.

Output findings as a short list with fixes applied where mechanical; anything judgment-bearing goes to critique-design.
