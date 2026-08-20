# ADR-0016: Apps are shippable products; BFF-less by default
- **Status**: accepted · 2026-08-20
- **Context**: Users build products, not agent plumbing. Workflows/agents exposed over HTTP already form an API; token exchange already yields per-user on-behalf-of chains.
- **Decision**: App CR = frontend container + generated API (workflow → POST endpoint; agent A2A stream → SSE chat) + pinned member versions (agents/graphs/frontend) released and rolled back as a unit + IdP OIDC roles. `init app` scaffolds frontend + typed TS client from Agent Cards/MCP schemas. plume's own additions limited to HTTP projection, client gen, release pinning (kro composes the rest).
- **Consequences**: Zero hand-written auth/backend for the common case; agents inside an App still pass individual eval gates.
