---
name: research-latest
description: Current-state research for plume — landscape, competitor, library, or standard checks. Use before binding any third-party component or when the user asks "what's the latest on X". Never answer landscape questions from memory.
when_to_use: what is the latest on X, before binding a dependency, check current versions, landscape or competitor check
context: fork
background: false
agent: general-purpose
---

# Research discipline

1. Always search with the current month/year in the query; prefer primary sources (project repos, release notes, foundation announcements) over blog roundups.
2. Check the specifics that bite: license (and recent changes), governance (CNCF/LF status), release cadence + latest version, k8s-native story, weight (pods/deps), protocol conformance.
3. Land durable findings in `docs/research/<topic>-<YYYY-MM>.md` with source links; note the re-verify-by date.
4. If a finding contradicts an ADR or the architecture doc, say so explicitly and propose the superseding ADR — do not silently diverge.
