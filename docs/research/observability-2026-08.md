# Observability backend facts for design 10 (2026-08)

- **OpenObserve**: single binary, ingests OTLP traces/metrics/logs natively; local-disk or object-storage backed; dashboards + alerts as JSON config. Chosen as sink (never system-of-record — ADR-0021). https://openobserve.ai/blog/llm-observability-tools/ · https://openobserve.ai/blog/llm-monitoring-best-practices/
- **agentgateway OTLP export**: see agentgateway-2.2-2026-08.md (traces + metrics on one pipe; GenAI semconv; MCP spans).
- Access-control note: OpenObserve ships with an initial admin credential that MUST be generated per-install (no defaults); SSO capability varies by build — verify at implementation.
