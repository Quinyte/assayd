# ADR-0014: Compliance as profiles (packs); HIPAA first
- **Status**: accepted · 2026-08-20
- **Context**: 2026 HIPAA guidance: BAA coverage, ZDR, encryption, access controls + tamper-evident audit (6-year retention), PHI minimization, per-action attribution. Most technical safeguards are emergent from plume's architecture (receipts, on-behalf-of identity, gateway chokepoint, ReBAC).
- **Decision**: `compliance: hipaa` profile pack configures the remainder: hash-chained receipt stream + 6-year retention to object storage, BAA model-egress allowlists enforced at the gateway, mandatory PHI redaction (gateway + ingestion normalize), automatic logoff, break-glass role with own audit, encryption-at-rest classes, access-review reports. SOC 2 / GDPR follow the same pattern. Enterprise tier.
- **Consequences**: "Compliance is a profile you enable, not an integration you build" — must remain literally true; any safeguard that needs code lands in core design, not the pack.
