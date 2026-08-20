# Review: Design 01 — kgp/v1alpha1
- **Verdict**: PASS after revisions (findings fixed in-place, 2026-08-20)
- **Independence caveat**: critiqued in the same session that authored the draft; re-critique in a fresh session before implementation starts.

## Findings

1. **MAJOR — hidden LLM dependency vs determinism claim.** `neighborhood_summary`/`walk` recipes could plausibly be implemented with provider-side LLM summarization, which breaks the conformance determinism requirement and smuggles an LLM dependency into providers. **Fix applied**: §3.5 now mandates that built-in recipes are deterministic graph operations — `text` is template-rendered, never LLM-generated, provider-side; LLM rendering is the agent's job.
2. **MAJOR — embedder identity absent from version metadata.** Re-embedding = version bump (architecture §13) is unenforceable unless the embedding model id/dimensions are recorded per version. **Fix applied**: `kg.schema` output now includes `embedder: {model, dim}` in version metadata; conformance asserts presence.
3. **MINOR — no error taxonomy.** Scope-denied vs not-found vs version-gone were unspecified; agents can't handle errors portably. **Fix applied**: §3.3 note adds the closed error set `KG_NOT_FOUND / KG_SCOPE_DENIED / KG_VERSION_GONE / KG_BUNDLE_UNKNOWN / KG_BUDGET_EXCEEDED` (MCP error data.code).
4. **MINOR — no pagination.** `limit` without cursors on search/neighbors. Accepted for v1alpha1; documented as a v1beta1 item rather than silently absent. **Fix applied**: note in §3.3.
5. **Doctrine/charter audit**: clean (0 pods from contract; slot correct; five primitives). **Freshness audit**: MCP 2026-07-28, Graphiti backends, FalkorDB SSPL all verified this session with sources.
