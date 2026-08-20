# Review: Design 27 — Compliance profile packs (HIPAA first)

- **Verdict**: **REVISE**
- **Reviewed**: `docs/designs/27-compliance-packs.md` (draft, 2026-08-20)
- **Independence note**: reviewed in an independent session that did not author the draft.
- **Method**: all 8 lenses, with the requested focus: the safeguard map for overclaiming; hash-chaining's limits; the new `profile` facet vs `pack/v1`'s closed catalog; and ADR-0015's never-paywall-correctness rule.

## Findings

### 1. MAJOR — hash-chaining and the profile's own mandated two-replica tap are mutually exclusive as written

`27-compliance-packs.md:34` vs design 04 §5 and ADR-0021. The profile turns on per-(tenant, stream) hash chaining **in the tap** — and design 04's compliance row *mandates* "split-mode + **2 replicas**" for exactly this profile. Two tap replicas cannot both extend one hash chain: each would read a chain head, compute `prev_hash`, and publish concurrently, producing forks, gaps, or a silent race in the one artifact whose entire value is that it cannot be silently altered. The design's own failure table makes this worse in the right way — "chain break detected ⇒ `ChainIntegrityFailed` (page); **no auto-repair, ever**" — so the expected steady state of a correctly-configured compliance install is a paging alert.

This is also in tension with ADR-0021's delivery model: receipts are *effectively-once within a sized horizon*, with beyond-horizon duplicates possible and counted, and span batches transformed per-replica with no global order. A total order is a precondition for chaining, and nothing establishes one.

**Fix**: pick a single-writer construction and state it. Cleanest given the existing architecture: **chain downstream of the stream, not in the tap** — a single-writer chainer consumer (one durable JetStream consumer per tenant stream, which JetStream itself serializes) reads receipts in stream order, computes the chain over the stream's own sequence numbers, and writes chain records + signed checkpoint anchors. Tap replicas stay stateless and parallel (04's scaling story survives), the chain inherits the stream's total order, and "chained over the stream sequence" is a *stronger* auditor statement than "chained at ingest." Alternatives (chain-per-shard with anchors spanning shards; leader election among tap replicas) are viable but weaker — whichever is chosen, §4.1's limits paragraph should name the ordering basis explicitly.

### 2. MAJOR — four rows of the safeguard map promise verification the platform cannot perform, in the document auditors will read most literally

`27-compliance-packs.md:24-30`. §3's stated job is to "turn optional into mandatory **and verify it** — every row becomes an admission rule + a `ComplianceViolation` condition when drift appears." That holds for the compiled rows (egress allowlist, access controls, attribution, redaction placement). It does not hold for four:

- **Encryption at rest** (`:25`): the profile can mandate a StorageClass *name*; it cannot verify the underlying CSI encrypts anything. The admission rule checks a string.
- **Encryption in transit** (`:24`): "SPIRE mTLS everywhere; `ambient` profile for residual traffic" — but per ADR-0012 the core is deliberately meshless, so Postgres/NATS links are **not** mTLS unless the ambient profile is enabled, and §5's pack contents never mandate it. As written the row's "everywhere" is conditional on a profile the compliance profile doesn't require.
- **Audit controls — "every query/output/PHI access logged"** (`:22`): true at the *access* level, but with mandatory PHI redaction applied gateway-side before persistence (ADR-0014, 04 §6), the stored evidence proves that an access occurred, not what content was returned. That is a defensible and probably correct design — it just isn't what "every output logged" says to an auditor.
- **"PHI minimization / de-identification"** (`:26`): *de-identification* is a HIPAA term of art (Safe Harbor's 18 identifiers, or Expert Determination). Pattern-based redaction filters are minimization, not de-identification, and claiming the latter in a compliance artifact is the sharpest overclaim on the page.

**Fix**: mandate the ambient profile in the pack (one values row — it makes the in-transit claim true rather than conditional); relabel the redaction row as "PHI minimization" only; and move the two irreducible limits into §6, which is precisely the mechanism this design built for them — "the platform mandates the encrypted storage class; verifying that the CSI encrypts is your infrastructure's attestation" and "the audit trail proves access, not post-redaction content." §6 gets stronger, not weaker, by absorbing them.

### 3. MAJOR — the `profile` facet's application path crosses design 07's chart ownership and the platform's GitOps-first posture

`27-compliance-packs.md:49` vs design 07 §4 and design 08 D3. Requesting a new facet kind is handled exactly right (see lens 2 — named, not smuggled). The problem is what it compiles *to*: "**chart value overrides** (retention, encryption, capture levels)". Chart values are the umbrella chart's install-time input, owned by design 07 — which pins them under a cosign-signed, digest-pinned chart with `helm upgrade` pre-hooks, golden `helm template` snapshots, the budget-ledger check, and `helm rollback` semantics. A runtime-installed pack mutating them would either be impossible (values live in the release/GitOps repo, not in cluster state a controller reconciles) or would bypass every one of those controls — and silently invalidate the golden snapshots that CI diffs on every chart PR.

**Fix**: the `profile` facet **declares required values and emits a reviewed diff** rather than applying them: `plume compliance enable`'s existing **preflight** (§7 — already the right shape) outputs the exact values delta for a GitOps commit, and the posture document then *verifies* those values are in effect (drift ⇒ `ComplianceViolation`, which is already the design's model). Declare → review → apply → verify fits the platform's GitOps-first rule and keeps 07 the sole owner of chart values. The other two outputs (admission policies, posture doc) are CR-shaped and reconcile normally — no change needed.

### 4. MINOR — compliance-pack filters need an explicit priority band under design 18's merge rule

`27-compliance-packs.md:44` vs 18 r2 §5. Design 18's multi-source composition sorts filter contributions by `(priority band, source name)` with "**platform/compliance bands outrank pack bands** by default — ADR-0014 guards are never reorderable by a pack." This design ships ADR-0014's guards (`phi-redact`, `egress-allowlist`) *as a pack* — the one case that rule didn't anticipate. Nothing states which band they land in, and if they default to the pack band, an ordinary pack could be merged ahead of PHI redaction. **Fix**: state that compliance-profile packs are admitted to the **compliance band** by virtue of being first-party and source-allowlisted (18 D5's trust root doing real work), and add the case to 18's composition golden tests.

### 5. MINOR — the ADR-0015 claim is asserted but not demonstrated for the two capabilities that matter

`27-compliance-packs.md:14,83`. D2 says chaining and retention "land in core (flag-gated); the enterprise product is packaging, reporting, and support" — the right split, and correctly reasoned. What's missing is the one sentence that makes it checkable: **is `plume compliance verify` an OSS CLI verb, and is the chaining flag usable without the enterprise pack?** If chaining ships in core but its *verification tool* is enterprise, the platform has paywalled the correctness check while claiming not to — precisely the failure ADR-0015 exists to prevent. (The design's own logic says yes to both; it just never says it.) **Fix**: state explicitly that an OSS user can enable chaining and run `compliance verify` standalone — tamper-evident receipts without a license — and that what enterprise adds is the profile bundle, the mandatory-settings enforcement, the reports, and support. That sentence is also a good marketing asset, which is the usual sign a doctrine is being honored rather than argued around.

## Lens summary

1. **Doctrine**: clean — 0 new pods, existing object-storage tiering, everything configurable expressed as pack data.
2. **Charter** (requested scrutiny): the `profile` facet request is **handled exemplarily** — D1 names it as a `pack/v1` contract revision, calls it "the one closed-catalog exception this design requests", and doesn't smuggle it as a filter or a values hack. That is the closed-catalog discipline working as intended (18 D1 said new facet kinds are contract revisions; this is one, filed as one). Finding 3 is about the facet's *application path*, not its legitimacy.
3. **Safeguard map** (requested scrutiny): finding 2. The compiled rows are genuinely strong — the ADR-0014 egress allowlist compiled at the gateway is a real, mechanical HIPAA control, and "PHI-touching agents can only reach BAA-covered endpoints (in-cluster models included)" is the platform's best compliance sentence.
4. **Hash-chaining limits** (requested scrutiny): the *stated* limits are excellent and rare — order-integrity-not-completeness, ADR-0021 D1's boundary restated "rather than glossed", compensating controls named, no retroactive hardening, no auto-repair. Finding 1 is a mechanism defect underneath an honest description, which is the better failure to have.
5. **ADR-0015** (requested scrutiny): finding 5 — the split is correct in substance; only the demonstration is missing.
6. **Failure modes**: the strongest table in the enterprise set — "retention failures must not become deletion", preflight-or-nothing enablement, reports as digested artifacts so a missing one is visible. `ChainIntegrityFailed` with no auto-repair is exactly right (and finding 1 is what would make it fire spuriously).
7. **Research freshness**: the HIPAA §requirements are cited to the landed landscape note; nothing new is claimed about third parties. Adequate — though like design 25, an enterprise design leaning on a bootstrap-era section would benefit from a dated note when the profile is built.
8. **Testability**: good coverage (preflight fixture, per-row drift matrix, injected chain break at a known sequence, tiering-failure-does-not-delete, report goldens, 26 integration); add the finding-1 concurrency case — two tap replicas under load must not produce a chain break — which is the test that would have caught it.

## Disposition

**REVISE.** This design does the hardest thing in the set well: it draws a bright line between what the platform can prove and what it cannot, and ships that line as a product surface (D4, §6 — "a deliverable, not a disclaimer"). That honesty is why the overclaims in §3 stand out: they're four rows that §6 should already own. The chaining defect is real but has a clean fix that makes the auditor story *better* (chained over the stream's own sequence), and the `profile` facet needs only to declare-and-verify rather than apply. Fix these and the ADR-0014 claim — compliance as configuration — is defensible in front of an actual auditor.

VERDICT: REVISE — 5 findings
