# Design 27: Compliance profile packs (HIPAA first) — enterprise

- **Status**: revised r2 — awaiting re-critique (r1 REVISE, 5 findings addressed; reviews/27-review.md)
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
| Audit controls — every PHI **access** logged with attribution | the receipt stream (enforced-hop completeness). Note: with mandatory redaction applied before persistence, the trail proves *access*, not post-redaction content — stated in §6 (r1 f2) | 04 |
| Access controls (least privilege, per-object) | compiled policy + ReBAC chain checks | 03/24 |
| Encryption in transit | SPIRE mTLS on agent traffic; **the profile mandates the `ambient` profile** (r1 f2 — otherwise Postgres/NATS links are unencrypted in the meshless core, and the claim would be conditional on a setting nobody required) | 06/ADR-0012 |
| Encryption at rest | profile mandates the encrypted StorageClass **name**; verifying the CSI actually encrypts is infrastructure attestation — moved to §6 (r1 f2) | 07 |
| **PHI minimization** (not de-identification — a HIPAA term of art the platform does not claim, r1 f2) | redaction filters at gateway + ingestion normalize | 03/14 |
| Automatic logoff; emergency access | IdP session policy; break-glass role | 06 |
| Model-side ZDR/BAA constraint | **LLM egress allowlist compiled at the gateway** — PHI-touching agents can only reach BAA-covered endpoints (in-cluster models included, 25 §5) | 03/ADR-0014 |

The profile's job for all of the above is to **turn optional into mandatory and verify it** — every row above becomes an admission rule + a `ComplianceViolation` condition when drift appears (e.g. an Agent CR added a non-allowlisted provider ⇒ rejected, not warned).

## 4. The two genuinely new capabilities

1. **Tamper-evident receipts (hash-chaining)** — ADR-0021 reserved `chain: {prev_hash, hash}`; the profile turns it on. **Single-writer construction (r1 f1 — parallel tap replicas cannot share one chain)**: chaining happens **downstream of the stream, not in the tap** — a dedicated **chainer consumer** (one durable JetStream consumer per tenant stream; JetStream serializes it) reads receipts **in stream order**, computes the chain **over the stream's own sequence numbers**, and writes chain records + signed **checkpoint anchors** (head + count, to object storage and optionally to a customer-controlled immutable store). Tap replicas stay stateless and parallel (04's split-mode scaling survives intact), and "chained over the stream sequence" is a stronger auditor statement than "chained at ingest" — the ordering basis is explicit. Verification: `plume compliance verify --since` walks the chain and reports the first break with its sequence. **Honest limits, stated**: chaining proves *append-order integrity* of the stream sequence as recorded; it does not prove *completeness* against a compromised gateway (receipts derive from gateway export — ADR-0021 D1's boundary, restated here rather than glossed) — the compensating controls are the split-mode tap, gateway attestation (SVID), and the gap counters that make `ReceiptsDegraded` visible.
2. **Retention & reporting** — 6-year tiering of receipts + bodies to object storage (lifecycle policy shipped, not hand-rolled); **access-review reports** (who could reach what, from CR-derived tuples + Grant CRs + contextual role definitions — 24), **activity reports** (per-user/per-agent PHI-touching actions from the audit index), and **breach-support export** (a scoped, signed evidence bundle for an incident window). All as Jobs writing signed artifacts.

## 5. Pack contents

```yaml
pack: compliance-hipaa
provides:
  profile:                     # NEW facet kind (pack/v1 revision — §2)
    - hipaa.yaml               # the mandatory-settings document (§6) + admission rules
  filters: [phi-redact.yaml, egress-allowlist.yaml]   # priority band: `compliance` — outranks pack bands, unreorderable by any pack (design 18 merge rule; r1 f4)
  signals: [phi_access_rate.yaml, chain_integrity.yaml]
  dashboards: [compliance-posture.json]
```

**Application path — declare → review → apply → verify (r1 f3)**: the `profile` facet **never mutates chart values** (design 07 owns them under signed charts, pre-hooks, golden snapshots and rollback). Instead `plume compliance enable`'s **preflight** emits the exact **values delta for a GitOps commit**; the human applies it through the normal chart path; the **posture document** then *verifies* those values are in effect (drift ⇒ `ComplianceViolation`). The facet's other outputs are CR-shaped and reconcile normally. The `profile` facet compiles to: the required-values declaration, admission policies (the §3 mandatory rules), and the **posture document** the operator reconciles — every setting it mandates is checkable, and `plume compliance status` renders pass/fail per safeguard row with the CR/setting that proves it.

## 6. What a profile cannot do (the section that keeps the claim honest)

Stated up front in the pack README and `compliance status` output:

- It cannot sign your BAAs, or verify that a provider honors ZDR — the allowlist enforces *where* traffic goes; the contract behind that endpoint is yours.
- It mandates an encrypted StorageClass but **cannot verify the CSI encrypts** — that is your infrastructure's attestation to produce.
- The audit trail proves **access with attribution**, not post-redaction content: mandatory redaction runs before persistence, deliberately.
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
- **D2 — Hash-chaining and retention land in core (flag-gated); the enterprise product is packaging, reporting, and support** — ADR-0015's rule, honored precisely and **checkably (r1 f5)**: an OSS user can enable chaining and run `plume compliance verify` standalone — tamper-evident receipts require no license. Enterprise adds the profile bundle, mandatory-settings enforcement, the reports, and support.
- **D3 — Chain integrity is never auto-repaired**, and its limits (order-integrity, not completeness) are stated to auditors.
- **D4 — §6 "what it cannot do" ships in the product**, not just the docs.

## 11. Resulting ADRs

ADR-0026 (P5+ent) after critique PASS.
