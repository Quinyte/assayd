# ADR-0015: Apache 2.0 core; open-core by component; never paywall correctness
- **Status**: accepted · 2026-08-20
- **Context**: 2024–26 relicensing wave (HashiCorp BSL, Redis) damaged trust; AGPL taxes adoption; CNCF path requires OSI license. Realistic strip-mining protection at this stage is velocity + enterprise layer, not license.
- **Decision**: Core under Apache 2.0. Enterprise = separate proprietary modules (Tenant CR/hard multi-tenancy fan-out, compliance profiles + audit reporting, SSO/SCIM, fleet management, advanced governance, support). Split line: single team gets the full loop free; organizational scale is paid. Hard rule: evals, receipts, signing, drift stay open — never paywall correctness or safety.
- **Consequences**: Trust-led adoption funnel; enterprise revenue tied to org scale, not crippled core.
