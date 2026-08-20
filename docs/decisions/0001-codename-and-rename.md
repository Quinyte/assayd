# ADR-0001: Neutral codename "plume"; rename before public release
- **Status**: accepted · 2026-08-20
- **Context**: "graphene" (early drafts) collides with graphene-python (GraphQL) and echoes GrapheneOS. Naming affects module paths, API groups, CLI binary.
- **Decision**: Build under throwaway codename **plume**. Finalize the public name (domain + no major OSS collision) before anything is published; API group and Go module paths are finalized at rename. Track as a pre-release task.
- **Consequences**: Internal docs/paths may say plume; nothing public may.
