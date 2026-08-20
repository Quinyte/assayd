# Review: Design 18 — Pack format + installer (`pack/v1`)

- **Verdict**: **REVISE**
- **Reviewed**: `docs/designs/18-pack-installer.md` (draft, 2026-08-20)
- **Independence note**: reviewed in an independent session that did not author the draft.
- **Method**: all 8 lenses, with the requested focus: facet application against ADR-0020's one-concern policy rule; the 09/19 degenerate-pack constraint; uninstall in-use semantics. OCI/cosign claims checked against the landed research note (consistent; no fresh search needed).

## Findings

### 1. MAJOR — pack filters make gateway concerns multi-source, and ADR-0020's one-concern rule has no composition story for that

`18-pack-installer.md:54,78` vs ADR-0020(2) and design 03 §3.2. The one-concern-per-policy rule was designed for a world with **one author per concern**: the compiler derives each target's `-guard`/`-transform` policy from that target's own `PolicyIntent`. Pack filters break the premise: a filter facet is "PolicyIntent additions" that lands *inside an existing concern* on *targets the pack didn't define* — and the moment two packs (or a pack plus the agent's own compliance guards, ADR-0014) contribute to the same concern on the same target, someone must decide **merge order and conflict semantics**, and nobody currently does. Filter order is behaviorally load-bearing (compact-context before or after the prompt guard is a different system), the compiler's conflict check as specified ("no two policies touch the same config area") would either reject the second contributor outright (packs unusable with compliance profiles) or requires a merge rule it doesn't have. D3 correctly routes packs *through* the compiler — this finding is that the compiler doesn't yet know how to hold what packs hand it.

**Fix**: define deterministic multi-source composition in the compiler (recorded as an ADR-0020 amendment/note): each filter contribution declares an explicit `priority` (with documented bands: platform/compliance filters outrank pack filters by default — ADR-0014 guards must not be reorderable by a pack); the compiler merges contributions into the concern policy sorted by `(priority, source name)` — byte-deterministic, golden-testable; two contributions touching the **same field** remain a hard conflict (install error naming both sources, consistent with §7's collision row). Add the multi-pack composition case to both 03's golden tests and this design's §9.

### 2. MINOR — filter attachment scope and Pack CR scope are unstated

`18-pack-installer.md:50-59`. "Filter live on all agents" (architecture §15) — all agents in the cluster? The namespace? The tenant? And is the Pack CR cluster-scoped or namespaced (which decides who may install and how tenancy composes, ADR-0013)? **Fix**: state both — recommended: Pack CR cluster-scoped with install gated by RBAC + the source allowlist; filter facets support an optional selector (`namespaces`/labels), default all-agents; hard multi-tenancy (design 26) partitions by allowlist per tenant. Two sentences, but they determine the blast radius of an install.

### 3. MINOR — the 09/19 degenerate-pack transition is honored in spirit but the handover mechanics go unsaid

`18-pack-installer.md:10` vs design 09 r2 §1 / design 19 §3. The constraint ("formalize around the existing artifact without moving it") is explicitly honored — good. Unstated: what actually changes at P3 for the two existing degenerate packs. Does the chart create Pack CRs for the builtins (making them visible to `plume pack list` and the KV index), and does the CLI's direct-fetch path (09 r2's P1 mechanism) remain as the offline/pre-18 fallback or get retired? The wizard currently reads templates via direct fetch; after 18 it reads `packs.*` KV. **Fix**: one transition paragraph — chart installs builtin Pack CRs at upgrade; direct-fetch remains the bootstrap/offline path; wizard prefers the index when present.

### 4. MINOR — `requires.contracts` uses identifiers the design-07 ledger doesn't carry

`18-pack-installer.md:26` vs design 07 §4. The example ranges name `gateway-filter: ">=1 <2"` and `template: ">=1"` — but the `plume-contracts` ConfigMap lists `kgp/idp/receipt/ontology/pack/semconv` (+ the 16-review additions); there are no `gateway-filter` or `template` contract versions to check against, so the compatibility gate as illustrated can't evaluate. **Fix**: either version each facet's content contract and add them to the ledger (`template/v1`, `gateway-filter/v1`, `knowledge-pattern/v1` — design 19 already names its own), or scope `requires.contracts` to ledger-resident identifiers only and let `pack/v1`'s facet schema version cover the rest. Pick one and align the example.

### 5. MINOR — "installed facets keep working" when the registry is unreachable contradicts D2's no-second-copy rule for content facets

`18-pack-installer.md:59,73,87`. D2: content stays in the registry, KV records only digests. Failure row: "Registry unreachable post-install: installed facets keep working." Both can't be true for templates/patterns — scaffolding needs the *content* at use time, and with the registry down and no copy anywhere, `plume init` fails. (Filters/signals genuinely keep working — their content was applied into cluster state.) **Fix**: split the row by facet class (applied-state facets survive registry outage; content-served facets degrade with a named error), and permit a digest-verified **local cache** (a cache is not a second source of truth — it's disposable and verifiable) so the common case still works; D2's wording adjusts to "referenced by digest, cached never copied-as-record".

## Lens summary

1. **Doctrine**: clean — 0 pods, no new state (CRs + the registry itself), the installer as the fast-plane boundary's *enforcement mechanism* is the right framing.
2. **Charter**: the closed facet catalog (D1) applies the invariant-types discipline exactly where r1 reviews kept warning the temptation would live; primitives honest.
3. **Contract consistency**: findings 1, 3, 4; elsewhere strong — fail-closed facet ordering reuses 03's apply discipline, patterns-never-auto-propagate honors 12 §3.7, name-collision-as-error matches the platform's loud-intent habit.
4. **Hidden dependencies/circularity**: finding 5 is the one contradiction; digest-referencing otherwise keeps the registry as the single store cleanly.
5. **Failure modes**: good table; the in-use uninstall semantics (requested scrutiny) are genuinely well-designed — drain-first for filters, dependents-listed blocking for registrations, and the pattern-pack exemption is *correct by prior construction* (12 D2's self-contained docs mean uninstall can't orphan a graph — a payoff of a decision made two designs ago, worth calling out).
6. **Security**: the strongest section — signature always, source-allowlist trust root (D5, concretizing 19 §6), provenance display generalized from 05, two-layer image verification, filters-through-the-compiler closing the policy-backdoor hole, and no execution at install time. Finding 1 is the one place the enforcement mechanism outruns its spec.
7. **Research freshness**: OCI 1.1 referrers / cosign v3 defaults / ORAS discover all consistent with the landed note (registry-fallback flagged in doctor per the note's caveat). No gaps.
8. **Testability**: thorough (refusal matrix, collision, in-use, propagate/not-propagate split, rollback, e2e install-to-uninstall); add finding 1's multi-pack composition golden and a registry-outage-by-facet-class case per finding 5.

## Disposition

**REVISE.** This is a strong, security-literate design — the trust chain, the closed catalog, and the in-use uninstall semantics all hold up under the requested scrutiny. The single MAJOR is a real architectural gap rather than an oversight: packs are the first *multi-source* contributor to gateway concerns, and ADR-0020's one-concern rule needs a composition amendment before "filter live on all agents" can be both true and safe. The four minors are alignment and honesty fixes.

VERDICT: REVISE — 5 findings

---

## Re-review r2 (2026-08-20)

- **Verdict**: **PASS** (1 residual nit)
- **Independence note**: same independent session as r1; did not author the draft or revision.

### Per-finding disposition

| r1 | Severity | Disposition |
|---|---|---|
| 1 | MAJOR | **Resolved.** Filter contributions carry an explicit `priority` with defined **bands** — platform/compliance outrank pack contributions *by construction* (ADR-0014 guards unreorderable by packs, the exact property demanded); the compiler merges per concern sorted by `(priority band, source name)` — byte-deterministic; same-field contributions remain a hard install error naming both sources; recorded as an ADR-0020 amendment note + a design-03 golden-test case. The one-concern rule now has its multi-source story. |
| 2 | MINOR | **Resolved.** Pack CR cluster-scoped, install gated by RBAC + source allowlist; filter facets take an optional selector (default all-agents); hard tenancy partitions allowlists per tenant (design 26 seam). |
| 3 | MINOR | **Resolved.** `plume pack adopt <ref>` wraps the existing 09/19 artifacts in Pack CRs at the *same digest* — nothing moves, nothing re-signs — and the degenerate direct-fetch path is then retired. The handover is now mechanics, not vibes. |
| 4 | MINOR | **Resolved.** `requires.contracts` uses ledger-resident identifiers (`gateway-filter/v1`, `template/v1`), and design 07's ledger now enumerates every socket contract (verified). |
| 5 | MINOR | **Resolved.** The registry-outage row split by facet class; small text facets get a **content-addressed, hash-verified KV cache** (a cache that cannot drift — D2's intent preserved with honest wording); images registry-only for new pulls. **Residual nit**: template facets are *directories* — NATS KV values carry size limits (~1MB class); large template trees may not fit KV. The JetStream **object store** (already substrate, already used for receipt bodies) is the right cache home for anything beyond small text; one clause in D2/§7 choosing per-size or just defaulting content caches to the object store. |

### Verdict

**PASS.** The MAJOR — the platform's first multi-source policy composition — is resolved with exactly the deterministic, band-ordered, compliance-protected merge the finding demanded, recorded at ADR-0020 and tested at 03. Fold into ADR-0024 with the KV-vs-object-store cache clause.
