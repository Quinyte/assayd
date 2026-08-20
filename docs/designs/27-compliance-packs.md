# Design 27: Compliance profile packs (HIPAA first) — enterprise

- **Status**: draft — awaiting critique
- **Phase**: enterprise · **Size**: M · **Date**: 2026-08-20
- **ADRs**: 0014 (compliance as configuration), 0015 (enterprise; never paywall correctness), 0018/0021/0024 (the mechanisms being configured) · interfaces: 18 (pack facets — this is a pack), 04 (receipt hardening), 03 (egress allowlists, redaction), 06 (session controls, break-glass), 26 (per-tenant profiles), 10 (retention posture)
- **Research**: `docs/research/landscape-2026-08.md` §HIPAA (BAA + ZDR, encryption, tamper-evident audit ≥6y, PHI minimization, per-action attribution, access review)

## 1. Purpose & scope

Makes ADR-0014's claim literally true — **"compliance is a profile you enable, not an integration you build"** — by shipping the profile as a pack whose facets configure existing mechanisms, plus the two capabilities that genuinely don't exist yet (receipt hash-chaining and the compliance reporting surface). In scope: the profile pack's contents, the safeguard→mechanism map, the honest gap list (what a profile cannot give you), the auditor-facing outputs. Out of scope: legal advice; BAA negotiation; the mechanisms themselves (their designs own them).

## 2. Doctrine & charter gates

- **Plane**: **fast** for everything configurable (the profile is pack data — facet kinds already exist: filters, signals, dashboards, plus a new `profile` facet kind = a contract revision to `pack/v1`, honestly noted as the one closed-catalog exception this design requests). **Slow** for the two new mechanisms (§4), which land in core designs as *capabilities* — flag-gated, so the OSS core keeps them (ADR-0015: never paywall correctness; the *packaging, reporting, and support* are the enterprise product).
- **Pods**: 0 new (chaining runs in the tap; reports are Jobs). **Stateful deps**: object storage tiering for long retention (existing pattern). ✓

## 3. Safeguard → mechanism map (what's already true)

| 2026 HIPAA technical safeguard | plume mechanism | Design |
|---|---|---|
| Unique user identification; per-action attribution | on-behalf-of `act` chains in every receipt | 06/04 |
| Audit controls — every query/output/PHI access logged | the receipt stream (enforced-hop completeness) | 04 |
| Access controls (least privilege, per-object) | compiled policy + ReBAC chain checks | 03/24 |
| Encryption in transit | SPIRE mTLS everywhere; `ambient` profile for residual traffic | 06/ADR-0012 |
| Encryption at rest | storage classes + per-component config (profile sets them) | 07 |
| PHI minimization / de-identification | redaction filters at gateway + ingestion normalize | 03/14 |
| Automatic logoff; emergency access | IdP session policy; break-glass role | 06 |
| Model-side ZDR/BAA constraint | **LLM egress allowlist compiled at the gateway** — PHI-touching agents can only reach BAA-covered endpoints (in-cluster models included, 25 §5) | 03/ADR-0014 |

The profile's job for all of the above is to **turn optional into mandatory and verify it** — every row above becomes an admission rule + a `ComplianceViolation` condition when drift appears (e.g. an Agent CR added a non-allowlisted provider ⇒ rejected, not warned).

## 4. The two genuinely new capabilities

1. **Tamper-evident receipts (hash-chaining)** — ADR-0021 reserved `chain: {prev_hash, hash}`; the profile turns it on: the tap chains per (tenant, stream) sequence, publishes periodic **checkpoint anchors** (chain head + count, signed, to object storage — and optionally to an external immutable store the customer names). Verification: `plume compliance verify --since` walks the chain and reports the first break with its sequence. **Honest limits, stated**: chaining proves *append-order integrity* of what was recorded; it does not prove *completeness* against a compromised gateway (receipts derive from gateway export — ADR-0021 D1's boundary, restated here rather than glossed) — the compensating controls are the split-mode tap, gateway attestation (SVID), and the gap counters that make `ReceiptsDegraded` visible.
2. **Retention & reporting** — 6-year tiering of receipts + bodies to object storage (lifecycle policy shipped, not hand-rolled); **access-review reports** (who could reach what, from CR-derived tuples + Grant CRs + contextual role definitions — 24), **activity reports** (per-user/per-agent PHI-touching actions from the audit index), and **breach-support export** (a scoped, signed evidence bundle for an incident window). All as Jobs writing signed artifacts.

## 5. Pack contents

```yaml
pack: compliance-hipaa
provides:
  profile:                     # NEW facet kind (pack/v1 revision — §2)
    - hipaa.yaml               # the mandatory-settings document (§6) + admission rules
  filters: [phi-redact.yaml, egress-allowlist.yaml]
  signals: [phi_access_rate.yaml, chain_integrity.yaml]
  dashboards: [compliance-posture.json]
```

The `profile` facet compiles to: chart value overrides (retention, encryption, capture levels), admission policies (the §3 mandatory rules), and a **posture document** the operator reconciles — every setting it mandates is checkable, and `plume compliance status` renders pass/fail per safeguard row with the CR/setting that proves it.

## 6. What a profile cannot do (the section that keeps the claim honest)

Stated up front in the pack README and `compliance status` output:

- It cannot sign your BAAs, or verify that a provider honors ZDR — the allowlist enforces *where* traffic goes; the contract behind that endpoint is yours.
- It cannot make an agent's *outputs* compliant (a model may still emit PHI into a non-PHI channel — that's what redaction filters and eval suites reduce, not eliminate).
- It cannot audit your organizational policies, training, or physical safeguards (HIPAA is broader than the technical safeguards this maps).
- It cannot retroactively harden receipts predating enablement (chaining starts at enablement; the checkpoint anchor records that boundary).

This list is a deliverable, not a disclaimer: an auditor reading it learns exactly where the platform's evidence ends and the customer's program begins.

## 7. Failure modes

| Failure | Behavior |
|---|---|
| Mandatory setting drifts (CR or values) | Admission rejects the change; if applied out-of-band, `ComplianceViolation` + the posture doc's failing row names it |
| Chain break detected | `ChainIntegrityFailed` (page); verify report names the sequence; no auto-repair, ever (a self-healing audit chain is not an audit chain) |
| Retention tiering fails | `RetentionDegraded`; receipts stay in the hot store (never dropped on a tiering error — retention failures must not become deletion) |
| Enabling the profile on a non-conforming install | `plume compliance enable` runs a **preflight** listing every violation first; no partial enablement |
| Report Job fails | Retried; reports are artifacts with digests — a missing report is visible, not assumed |

## 8. Security

The profile *is* security configuration; its own integrity: the pack is signed and source-allowlisted (18), the posture doc is operator-reconciled (drift-detectable), reports are signed artifacts, break-glass use is receipted and alerts immediately. Checkpoint anchors may be mirrored to a customer-controlled store the platform cannot rewrite.

## 9. Testing

Preflight fixture (non-conforming install ⇒ complete violation list); mandatory-setting drift matrix (each row of §3 ⇒ rejection + condition); chain verification (happy path; injected break at a known sequence ⇒ exact detection); retention-tiering drill incl. failure-does-not-delete; report goldens (access review from a fixture tuple/CR set); enablement on a running tenant (26 integration).

## 10. Decisions for async review

- **D1 — The profile is a pack**, requesting one new closed-catalog facet kind (`profile`) — the exception is named, not smuggled.
- **D2 — Hash-chaining and retention land in core (flag-gated); the enterprise product is packaging, reporting, and support** — ADR-0015's rule, honored precisely.
- **D3 — Chain integrity is never auto-repaired**, and its limits (order-integrity, not completeness) are stated to auditors.
- **D4 — §6 "what it cannot do" ships in the product**, not just the docs.

## 11. Resulting ADRs

ADR-0026 (P5+ent) after critique PASS.
