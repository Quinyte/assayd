# Design 18: Pack format + installer (`pack/v1`)

- **Status**: **approved** — critique PASS at r2 (reviews/18-review.md) · ADR-0024
- **Phase**: P3 · **Size**: M · **Date**: 2026-08-20
- **ADRs**: 0008 (packs are the fast plane's delivery vehicle) · interfaces: 07 (contracts ledger), 08 (`plume pack` verbs, wizard consumption), 09 (template packs — the recorded constraint: formalize around the existing artifact), 19 (knowledge patterns), 16 (runner images), 11 (reader/catalog types), 10 (dashboards/signals)
- **Research**: `docs/research/oci-packs-2026-08.md` — OCI 1.1 referrers API finalized (2024; Harbor/Quay/ECR support landed 2024-25); cosign v3 defaults to referrers + the new bundle format; `oras discover` audits the attestation chain.

## 1. Purpose & scope

The fast plane's delivery vehicle made real: one signed OCI artifact carrying declared **facets**, installed/uninstalled as a unit, compatibility-checked against the running contracts. Formalizes what designs 09 and 19 already ship as "degenerate packs" **without moving their artifacts** (the recorded constraint). In scope: the manifest, facet catalog, the Pack CR + reconciliation, compatibility/trust rules, uninstall semantics. Out of scope: facet *content* rules (each facet's owning design), the pack marketplace/registry curation (future).

## 2. Doctrine & charter gates

- **Plane**: the installer is slow-plane (CR + controller in the agent-operator; CLI verbs); **everything a pack carries is fast-plane by definition** — this design is the boundary's enforcement mechanism.
- **Pods**: 0. **Stateful deps**: none (installed-pack inventory = Pack CRs; artifact cache = the OCI registry itself). ✓
- **Primitives**: Artifact (the pack), Resource (Pack CR + facet-applied resources). ✓

## 3. The `pack/v1` manifest

```yaml
# pack.yaml — at the artifact root
pack: context-compaction
version: 0.3.0
description: …
requires:
  contracts: {gateway-filter/v1: ">=1 <2", template/v1: ">=1"}   # checked vs plume-contracts (07) — the ledger carries EVERY socket contract id (gateway-filter/v1, template/v1, knowledge-pattern/v1, evalrunner/v1, …), an 07 note (r1 f4)
  tier: core                                                # or plus — refuses install below
provides:                       # the CLOSED facet catalog (v1)
  filters:      [compact-context.yaml]        # gateway-filter configs → applied via policy compiler
  skills:       [when-to-summarize.md]        # loop skills → indexed for templates/wizard
  templates:    [loop-with-compaction/]       # scaffold templates (design 09 layout)
  patterns:     [.]                           # knowledge patterns (design 19 layout)
  readers:      [fhir-bulk.yaml]              # Connector reader-type registrations (11) — config + image ref
  catalog:      [fhir-mcp.yaml]               # Connector tool-catalog entries (11) — image refs
  runners:      [deepeval-runner.yaml]        # evalrunner registrations (16) — image ref + contract ver
  dashboards:   [compaction-panel.json]       # observability content (10)
  signals:      [context_efficiency.yaml]     # signals.yaml additions (10)
```

**09/19 handover mechanics (r1 f3)**: the existing template/pattern artifacts are already OCI + signed — at design-18 rollout, `plume pack adopt <ref>` wraps each in a Pack CR *referencing the same artifact digest* (nothing moves, nothing re-signs); the degenerate direct-fetch path is then retired from the CLI. Facet catalog is **closed** in pack/v1 — a new facet kind is a contract revision (the same discipline as invariant types; packs must stay data). Every image referenced by a facet must itself be cosign-signed (admission enforces at use, the installer verifies at install — two layers).

## 4. Artifact layout & trust

- OCI artifact (ORAS-pushed): manifest + content layers; **cosign v3 signature via OCI 1.1 referrers** (the registry-native chain; `oras discover` audits it). SBOM attached the same way for packs carrying image refs.
- **Trust rules**: signature required, always; the signer identity must match the configured **pack-source allowlist** (`plume-pack-sources` ConfigMap: builtin sources shipped, org sources added explicitly; hardened profiles lock it — design 19 §6's containment made concrete). Provenance displayed at install and in every wizard surface that offers pack content (the design-05 rule generalized).
- Registries without referrers support: cosign's fallback storage works but `doctor` flags it (attestation-chain audit degraded).

## 5. Install/uninstall — the Pack CR

`plume pack install <ref>` = CLI fetch → verify (signature, source allowlist, contract ranges vs the 07 ledger, tier) → **create a `Pack` CR**; the operator reconciles facets:

| Facet | Applied as |
|---|---|
| filters | Filter *contributions* with an explicit **`priority`** → the compiler merges multi-source contributions per concern **sorted by (priority band, source name)** — byte-deterministic; **platform/compliance bands outrank pack bands by construction** (ADR-0014 guards are never reorderable by a pack); two contributions touching the same *field* = hard install error naming both sources (r1 f1; recorded as an ADR-0028 amendment note + 03 golden-test case). Optional `selector` (namespaces/labels) scopes attachment; default all-agents (r1 f2) |
| skills / templates / patterns | Indexed in the directory (`packs.*` KV space) — CLI/wizard read from there; content stays in the registry (the CR records the digest; no second copy) |
| readers / catalog / runners | Type registrations (KV) consumed by designs 11/16 when a CR names them |
| dashboards / signals | Merged into the 10-pack content (name-collision ⇒ install error, never silent override) |

**Scope (r1 f2)**: the Pack CR is **cluster-scoped**; install gated by RBAC + the source allowlist; hard multi-tenancy (26) partitions allowlists per tenant. Properties: **install is atomic per facet-class with fail-closed ordering** (03's apply discipline reused — filters verify acceptance before anything advertises the pack as installed); the Pack CR's status lists每 facet's application state; `plume pack list` = `kubectl get packs`. **Uninstall** = delete the CR → reverse order teardown; **refused while in-use**: a filter attached to live routes drains first; registrations named by existing CRs (a Connector using a pack reader) block with the users listed. Instantiated knowledge patterns are *not* in-use links (docs are self-contained — 12 D2): uninstalling a pattern pack never touches existing graphs.

## 6. Versioning & upgrade

Pack upgrades are new CR versions (`spec.ref` digest change): facets re-reconcile with the same fail-closed ordering; **patterns never auto-propagate** (12 §3.7 — upgrade-diff is human-run); filters/dashboards do propagate (that's their point — the "new technique Tuesday" path) with the previous digest retained for one-command rollback (`plume pack rollback`). Contract-range violations on upgrade refuse before touching anything.

## 7. Failure modes

| Failure | Behavior |
|---|---|
| Unsigned / signer not allowlisted | Install refused at CLI *and* admission (defense in depth) |
| Contract range unsatisfied | Refused with the ledger comparison shown |
| Facet apply fails mid-install | Fail-closed ordering: nothing user-visible advertises until its dependencies applied; Pack CR status names the stuck facet; re-reconcile resumes |
| Name collision (dashboard/signal/reader type) | Install error naming both owners — never silent override |
| Registry unreachable post-install | Behavioral facets keep working (applied state is in CRs/policies); **small text facets (skills/templates/patterns/signals/dashboards) are content-addressed-cached in KV at install** — a hash-verified cache cannot drift, so D2's no-*mutable*-copy intent holds while wizard reads survive registry outages (r1 f5); image facets (readers/runners/catalog) need the registry for *new* pulls only |
| Uninstall while in-use | Refused with the dependent CRs listed |

## 8. Security

Signature + source allowlist + provenance display (§4); facet-specific bars re-checked at use (reader/runner/catalog images through the same admission as agent images); filters go through the compiler — a pack cannot emit gateway config the compiler wouldn't (the one-concern rule and conflict checks apply to pack filters too, closing the "pack as policy backdoor" hole). Pack content never executes at install time — reconciliation applies *declarations*; execution happens only where each facet's owning design already governs it.

## 9. Testing

Manifest schema + golden installs (each facet class); contract-range refusal matrix; collision fixture; in-use uninstall refusal; upgrade propagate/not-propagate split (filter updates live, pattern requires human diff); rollback; signature/allowlist negative tests; e2e: install the `context-compaction` example end-to-end — filter live on agents, skill indexed, template appears in the wizard — then uninstall clean.

## 10. Decisions for async review

- **D1 — Closed facet catalog in pack/v1**; new facet kinds are contract revisions.
- **D2 — Pack content is digest-referenced; small text facets get a content-addressed KV cache** (hash-verified — cannot drift); images stay registry-only (r1 f5).
- **D3 — Pack filters route through the policy compiler** — packs cannot bypass the gateway-config discipline.
- **D4 — Upgrade propagation split**: behavioral facets (filters/dashboards) propagate with rollback; authored-content facets (patterns) never auto-propagate.
- **D5 — Source allowlist is the trust root** for third-party packs; hardened profiles lock it.

## 11. Resulting ADRs

ADR-0024 (P3) after critique PASS.
