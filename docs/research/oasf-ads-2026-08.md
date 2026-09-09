# OASF / AGNTCY Agent Directory Service — facts for design 05 (2026-08)

- **OASF schema version pinned: 1.1.0** — verified live 2026-08-20: `schema.oasf.outshift.com/api/version` → `{"schema_version":"1.1.0","server_version":"1.1.1","api_version":"0.6.0"}`; GitHub releases v1.1.0 > v1.0.1 > v0.8.5. Minor bumps = schema changes; patch = server only. https://github.com/agntcy/oasf/releases
- **ADS architecture**: OASF schema layer + **OCI/ORAS registry storage semantics** + content addressing + **Sigstore signing** + taxonomy-driven capability discovery. https://arxiv.org/abs/2509.18787 · https://docs.agntcy.org/oasf/open-agentic-schema-framework/
- **Format conversion**: ADS imports/exports A2A cards and MCP server records with enrichment — an A2A card embeds cleanly in an OASF record. https://docs.agntcy.org/dir/hosted-agent-directory/
- **JetStream KV mechanics** (design 05 storage): KV = stream with subject-token keys (`$KV.<bucket>.<key>`, `.`-separated hierarchy, prefix watch); **history default 1, max 64 per key** (ADR-8, `max_msgs_per_subject`) — KV history is not an audit substrate. https://docs.nats.io/nats-concepts/jetstream/key-value-store · https://github.com/nats-io/nats-architecture-and-design/blob/main/adr/ADR-8.md
- assayd adopts ADS **formats + exchange semantics without running ADS** (0 pods); hosted Outshift directory is an explicit-export target only.
