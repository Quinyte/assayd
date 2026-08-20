# Review: Design 13 — Graphiti provider adapter

- **Verdict**: **REVISE**
- **Reviewed**: `docs/designs/13-graphiti-adapter.md` (draft, 2026-08-20)
- **Independence note**: reviewed in an independent session that did not author the draft.
- **Method**: all 8 lenses; consistency vs designs 01 (+A1/A2), 02, 03, 12 r2, 14 (draft, reviewed concurrently), ADRs 0017/0018/0020; graphiti claims checked against the landed research note plus a fresh search where thin (FalkorDB copy mechanics — source inline).

## Findings

### 1. MAJOR — the adapter and the pipeline both claim extraction, and the adapter's version violates the kgp write contract

`13-graphiti-adapter.md:25,42` vs design 01 §3.4 and design 14 §3. Design 01 defines `kg.admin.write_batch` as "**upsert nodes/edges** with mandatory provenance per element" — a *structured* write. This design implements it as "typed **episode adds**" via `add_episode`/`add_episode_bulk` — graphiti's extraction path, which [classifies raw episodes against the Pydantic types using an LLM](https://help.getzep.com/graphiti/core-concepts/custom-entity-and-edge-types). Meanwhile design 14's pipeline has its *own* `extract` stage (LLM via the gateway) whose output flows to `write_batch`. As the two drafts stand, the corpus gets extracted twice by two components — and Gate A (extraction QA *before any write*, 14 D2) only works if extraction happens pipeline-side, because adapter-side extraction happens *at* write, after the gate. The seam must be pinned one way, and only one way preserves the approved contracts.

**Fix**: extraction lives in the pipeline (design 14's extract stage — where Gate A, the build's budget principal, and receipts already sit); `write_batch` is a **structured node/edge write** honoring 01 §3.4 — the adapter uses graphiti's lower-level node/edge persistence (bypassing episode extraction) or writes typed elements directly via the backend driver, attaching provenance as specified. §3.2's role shrinks to: the *Pydantic types* still configure search/typing, but `add_episode` is not the ingestion write path. This also cleanly resolves budget attribution: the LLM caller is the build Job, so 14 D4's "builds carry their own budget principal" is automatically true — no principal-propagation machinery needed. Record the resolution in both designs.

### 2. MAJOR — copy-forward as "bulk re-add of vN's episodes" re-runs LLM extraction per version fork: full-corpus cost and a non-deterministic base

`13-graphiti-adapter.md:21`. `begin_version(from: vN)` implemented by re-adding vN's episodes through `add_episode_bulk` means every incremental build starts by **re-extracting the entire corpus** — O(corpus) LLM tokens per fork (the exact cost D2 makes visible would be dominated by copies, not new content) — and, worse, extraction is not deterministic across runs, so the "copied" vN+1 base *differs from vN*: version semantics ("vN+1 = vN + delta") and `kg diff` both break at the root. The bulk-API constraint in the research note ("populating empty graphs") is honored, but the mechanism behind it isn't examined.

**Fix**: copy-forward must be a **data-level copy, never re-extraction**. Verified this session: [FalkorDB's `GRAPH.COPY <src> <dest>`](https://docs.falkordb.com/commands/graph.copy.html) copies a whole graph key with nodes, relationships, schema, and indices — deterministic and cheap. That implies version = **graph-key-per-version** rather than `group_id`-within-one-key for the FalkorDB backend (GRAPH.COPY operates on keys); for Neo4j, a scoped subgraph copy (`apoc`-style or driver-level) or dump/restore per version. Restate §3.1 accordingly: `group_id`/key mapping is a backend detail behind `begin_version`; the contract-level property is "fork is a deterministic data copy". (Finding 1's structured-write resolution makes this straightforward — structured data copies; episodes-as-source-of-truth wouldn't.)

### 3. MINOR — `KG_EMBEDDER_MISMATCH` as a wire-level extension code breaks the closed error set the reference adapter exists to model

`13-graphiti-adapter.md:46` vs design 01 §3.3 ("Errors are a **closed set** … portable handling, no provider-specific error parsing") and ADR-0017. The reference implementation emitting a non-contract code on the wire is the one adapter that must not — it would fail the conformance suite's own premise, and every other provider will copy what the reference does. **Fix**: return a closed-set code (`KG_VERSION_GONE` is the nearest semantic — the write targets a version state that cannot accept it) with `data.detail: "embedder_mismatch"` and the specifics in `data.message`; keep D3's v1beta1 promotion path for a first-class code. The *refusal behavior* is right — only the wire code changes.

### 4. MINOR — the scope header's "signed; adapter verifies the gateway's signature/SVID" conflates two mechanisms, neither specified

`13-graphiti-adapter.md:38`. Ingress is already mTLS-from-gateway-only (§6) — if the TLS peer *is* the gateway, the header needs no separate signature; if a signature is still wanted (defense against a compromised hop inside the pod boundary), its scheme (key, format, rotation) exists nowhere in 03/06/13. **Fix**: pick one and say it — recommended: trust derives from the verified gateway SVID on the connection; the word "signed" is dropped here and in design 03's row (or the signature scheme gets specified once, in 03, and referenced).

## Lens summary

1. **Doctrine/charter**: clean — fast-plane provider behind the slot, per-graph workload pods per ADR-0018, no new substrate; the idempotency ledger living inside FalkorDB (no extra store) is a nice doctrine touch.
2. **Contract consistency**: finding 1 is the serious one (01's write semantics + 14's pipeline); the rest maps faithfully — version-scoped endpoints, closed errors (modulo finding 3), embedder-in-metadata per ADR-0017, scope enforcement per 01 A1 with deny-on-absent (the right default), quarantine-not-promote on invariant failure.
3. **Hidden dependencies/circularity**: finding 2's non-determinism is the hidden one — a "copy" that isn't one; the routing-table-rebuilt-from-namespaces recovery is the right stateless posture.
4. **Failure modes**: good table — copy-forward interruption lands in quarantine + idempotent resume, backend switch refused in-place (no silent migration), deny-all on missing scope.
5. **Security**: mTLS-only ingress, platform-SVID admin (now with 01 A2's narrow grants), provenance-as-pointers per ADR-0014; finding 4 is a specification gap, not a hole.
6. **Research freshness**: `group_id`, bulk constraint, and Pydantic typing all check against the landed note; the copy mechanics were the thin spot — now grounded (GRAPH.COPY, fork caveat noted upstream: [it can fail transiently while the server can't fork](https://github.com/FalkorDB/FalkorDB/issues/872) — worth a retry note in §5).
7. **Testability**: conformance-suite-as-acceptance is exactly right; the adapter-specific additions (copy interruption, embedder mismatch, bulk/incremental equivalence, backend matrix) are well-chosen — add a determinism assertion on copy-forward (fork vN twice ⇒ identical results) once finding 2 resolves.
8. **Charter formality**: present and correct.

## Disposition

**REVISE.** The contract mapping is mostly faithful and the operational thinking (quarantine, deny-by-default scope, stateless routing recovery) is sound. But the two MAJORs sit at the adapter's core: the write path contradicts the approved contract and double-books extraction with design 14, and the version-fork mechanism silently converts "copy" into "re-extract". Both resolve together — extraction pipeline-side, writes structured, forks as data copies — and that resolution should be recorded in 13, 14, and a line in 01's amendment log.

VERDICT: REVISE — 4 findings
