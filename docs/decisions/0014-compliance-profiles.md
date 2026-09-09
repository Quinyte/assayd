# ADR-0014: Compliance as profiles (packs); HIPAA first
- **Status**: accepted · 2026-08-20
- **Context**: 2026 HIPAA guidance: BAA coverage, ZDR, encryption, access controls + tamper-evident audit (6-year retention), PHI minimization, per-action attribution. Most technical safeguards are emergent from assayd's architecture (receipts, on-behalf-of identity, gateway chokepoint, ReBAC).
- **Decision**: `compliance: hipaa` profile pack configures the remainder: hash-chained receipt stream + 6-year retention to object storage, BAA model-egress allowlists enforced at the gateway, mandatory PHI redaction (gateway + ingestion normalize), automatic logoff, break-glass role with own audit, encryption-at-rest classes, access-review reports. SOC 2 / GDPR follow the same pattern. Enterprise tier.
- **Consequences**: "Compliance is a profile you enable, not an integration you build" — must remain literally true; any safeguard that needs code lands in core design, not the pack.

## Amendment 1 (2026-09-04, design 03 §3.4.1 / A11)

**"BAA model-egress allowlists enforced at the gateway" names an enforcement point that does not exist.** Checked against the compiler that would have to emit it: agentgateway has no egress, host, domain or provider allowlist field anywhere in its CRD surface, so there is no policy to attach and nothing to enforce. What assayd actually does is **construct** the LLM Backend from an enumerated set of resolved endpoints and ban `dynamicForwardProxy`; an endpoint outside the set is unreachable because it was never registered, not because a rule rejected it. The restriction is real and the compliance property holds. The mechanism is Backend construction, not gateway enforcement, and the difference matters to anyone auditing this ADR against the code: they will look for a policy and find none.

Two things this does not change. The `hipaa` profile still adds mandatory PHI redaction as a real gateway policy, which *is* enforcement. And the profile still contributes its mandatory entries through design 03's registry, so a conforming Agent cannot compile an unrestricted LLM egress route — design 03 §3.4.1 records that entries range over emitted resources precisely so `AgentgatewayBackend(egressEnumerated)` is checkable.

**Still open**: the allowlist entry grammar (host, provider id, or model id) is undefined, as is the endpoint catalog's contents, versioning and upkeep. Until that lands, no BAA claim should be read as machine-checked.
