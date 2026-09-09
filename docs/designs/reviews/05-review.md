# Review: Design 05 — Agent directory (OASF on JetStream KV, OCI exchange)

- **Verdict**: **REVISE**
- **Reviewed**: `docs/designs/05-agent-directory.md` (draft, 2026-08-20)
- **Independence note**: reviewed in an independent session that did not author the draft (same session as re-reviews 03/04 r2).
- **Method**: all 8 critique lenses; consistency vs architecture.md, approved designs 01/02 (+ §11 amendments), r2 designs 03/04, ADRs 0003/0013/0019; OASF/ADS and JetStream KV claims verified by current-year search (sources inline).

## Findings

### 1. MAJOR — the card-serving path names a design-03 route that doesn't exist, and serving "from the embedded card" conflicts with ADR-0019's card source-of-truth

`05-agent-directory.md:50`. Three problems in one sentence. (a) Design 03 r2's concern-mapping table emits no card-serving route — "static route emitted by design 03" is a claim about another design's output that that design doesn't make. (b) Serving the well-known path "from the embedded card" means the gateway answers with a **directory snapshot**, but ADR-0019 fixed the card's source of truth as *the container*; a snapshot served at the discovery URL can diverge from the live card between card-drift re-registrations (design 02 §3.4), so A2A clients and the platform would disagree about what the agent's card *is*. (c) Even if snapshot-serving were chosen deliberately, a gateway "direct response with payload" capability is asserted without a source — nothing in `docs/research/agentgateway-2.2-2026-08.md` covers it.

**Fix**: simplest and SoT-preserving — the gateway *routes* `/.well-known/agent-card.json` to the agent container (which already serves it, design 02 §3.4); the directory's embedded card remains the validated, digest-pinned discovery copy for `assayd dir` / OASF export, not the wire answer. If snapshot-serving is genuinely wanted (e.g. to serve cards for scaled-to-zero or external agents), decide it explicitly: add the mapping row to design 03 §3.4, verify the direct-response capability with a citation, and state the freshness guarantee (digest match enforced at re-registration). Either way, design 03 needs the route row — record it there, not here.

### 2. MAJOR — MCP Registry federation has no trust story: unverified third-party records flow into the `assayd init` picker

`05-agent-directory.md:57`. `dir import` of OASF records gets cosign verification, but the MCP Registry federation path — "read-only import into `tools.*`" — states no verification at all beyond a provenance label. Public-registry entries are third-party content; a typosquatted or malicious tool entry imported into the catalog surfaces in the CLI wizard's tool picker (architecture §11: the wizard is how users choose tools), steering users toward binding a hostile MCP server. The directory being "discovery, never authz" (D2) does not defuse this: discovery *is* the attack surface when a human picks from it — D2 protects against stale grants, not poisoned suggestions.

**Fix**: state the federation trust model: (a) import is explicit and curated (an allowlist of registry namespaces, or per-entry operator approval), never a background sync; (b) imported entries are schema-validated and carry visible provenance that the wizard *displays* (per the wizard rule: recommendations with reasoning, never silent); (c) binding an imported tool still requires the normal Connector/MCPServer CR path with its admission checks — an imported record alone must not be bindable. A failure/abuse row for "malicious registry entry" belongs in §5.

### 3. MAJOR — the directory key layout silently diverges from approved design 02 §3.4 (third instance of the unrecorded-amendment pattern)

`05-agent-directory.md:43` vs `02-agent-crd-operator.md:83`. Design 02 (approved, ADR-0019) specifies the directory key as `agents/<ns>/<name>@<revision>`; design 05 lays out `agents.<ns>.<name>.<revision>` + an `.active` pointer. The dot form is almost certainly the *correct* one — KV keys are subject tokens under `$KV.<bucket>.<key>` ([KV sits on a stream; keys are subject-path tokens](https://docs.nats.io/nats-concepts/jetstream/key-value-store)), where `.` is the hierarchy separator that makes prefix-watch (`agents.<ns>.>`) work, and client-library key charsets are restrictive (the `@` in 02's format could not be confirmed as valid this session — flag it, don't assume it). But right or wrong, this is the third design in a row to amend approved design 02 by silent divergence (cf. 03-review f5, 04-review f9) — and this time the diverged artifact is an interface that CLI (design 08) and templates (design 09) will read. Also new-but-unstated in the layout: the `.active` pointer key and `tools.<ns>.<name>` don't exist in 02's description at all.

**Fix**: one amendment entry in design 02 §11 recording the canonical key layout (dot-separated tokens, the `.active` pointer, the `tools.*` space) with the subject-token rationale; verify the key-charset rule against the NATS client spec at implementation and note it. Design 05 then *cites* the amendment instead of contradicting the approved text.

### 4. MINOR — "KV history = built-in audit" over-claims: history is capped at 64 per key and defaults to 1

`05-agent-directory.md:44`. Verified this session: [JetStream KV history defaults to 1 with a hard maximum of 64 per key](https://docs.nats.io/nats-concepts/jetstream/key-value-store) (implemented as `max_msgs_per_subject`, [ADR-8](https://github.com/nats-io/nats-architecture-and-design/blob/main/adr/ADR-8.md)). The design neither sets the bucket's history depth nor acknowledges the cap — as written, the "built-in audit" is one revision deep. A bounded ring of ≤64 changes per key is a debugging convenience, not an audit trail; the platform's actual audit substrate is the receipt stream.

**Fix**: state the bucket history config (e.g. 10); rephrase as "bounded recent-change history"; if directory changes need real audit, emit a change event/receipt on registration writes (the operator is the single writer — one line of code) and say audit lives there.

### 5. MINOR — `assayd.status.phase` in the record has no stated write trigger, so it will routinely lie

`05-agent-directory.md:32,78`. Records are written at registration (design 02 §3.4), but `phase` changes at runtime (Ready → Degraded → Ready). Either the operator re-writes the record on every phase transition (write amplification and a new write path — unstated) or the field goes stale immediately. §7's "staleness shown in `assayd dir list`" acknowledges the symptom without fixing the cause; a picker showing `Ready` for a Degraded agent misleads exactly the users the directory serves.

**Fix**: pick one and state it — (a) operator updates the record on phase transitions (cheap: single writer, per-agent key), or (b) drop `phase` from the stored record and have read paths join live CR status. Given the wizard is the consumer, (a) is recommended.

### 6. MINOR — "Event" primitive mislabel: KV watch is not CloudEvents

`05-agent-directory.md:15`. Same class as 04-review finding 8: the charter's Event primitive is CloudEvents (architecture §15); KV watch feeds are NATS-native change notifications. Claiming the primitive dilutes the charter gate. **Fix**: drop the Event claim (Agent + Artifact are the honest primitive story); KV watch is implementation detail — or, if directory changes should be platform events, emit real CloudEvents onto JetStream and then the claim is true (pairs with finding 4's audit fix).

### 7. MINOR — the operator-HTTP read path in §2 is dangling

`05-agent-directory.md:14` vs `:48-51`. §2 declares "a read path served by the operator's existing HTTP listener", but §3.3's actual read paths are direct KV (CLI/operators) and the gateway (agents) — nothing uses an operator HTTP endpoint, and no interface, authn story, or consumer is defined for it. Undefined listening surface on the control-plane binary is exactly what the security lens exists to catch. **Fix**: delete the clause, or define the endpoint (consumer, authn, read-only contract) if something (the App layer?) actually needs it.

### 8. MINOR — research cited inline but not landed; OASF schema version is a placeholder

`05-agent-directory.md:6,25`. The ADS/OASF claims verified cleanly this session — [ADS = OASF schema layer + OCI/ORAS storage semantics + Sigstore signing](https://arxiv.org/abs/2509.18787), [multi-format A2A/MCP import-export](https://blogs.agntcy.org/technical/2026/02/19/directory-mcp-server.html), and the [hosted Outshift directory](https://docs.agntcy.org/dir/hosted-agent-directory/) all exist as described — but CLAUDE.md rule 4 wants a dated `docs/research/` file, not a header line (same process finding as 03/04 r1). And the record's `"oasf": "…/agent/v…"` leaves the pinned schema version literally as an ellipsis — the pin is the finding of record. **Fix**: land `docs/research/oasf-ads-2026-08.md` (the three sources above + the current OASF schema version) and write the actual version into §3.1.

### 9. MINOR — public-visibility ↔ Outshift linkage is ambiguous about *when* records leave the cluster

`05-agent-directory.md:55`. §3.4 ties the hosted Outshift directory to `expose.visibility: public` in a parenthetical, which can be read as "public agents get published to the hosted directory". Publishing a record externally is an outward-facing act; if it ever became automatic on a spec field, that's a consent problem. **Fix**: one sentence — external publication happens *only* via explicit `assayd dir export` (+ push); `expose.visibility: public` never publishes a record anywhere by itself.

## Lens summary

1. **Doctrine**: 0 pods, KV-only — clean, modulo finding 7's undefined listener.
2. **Charter**: Agent + Artifact (genuine OCI use — correctly the primitive this time) sound; finding 6 is the one mislabel.
3. **Contract consistency**: findings 1, 3, 5 — the design-02 divergence pattern recurs; the card-SoT tension with ADR-0019 is the most consequential.
4. **Hidden dependencies/circularity**: no circular constructions; single-writer/LWW with eventual consistency is honestly argued, and D2 ("discovery, never authz — staleness benign") is the right load-bearing boundary, correctly stated.
5. **Failure modes**: solid four rows; missing the malicious-import row (finding 2) and the history-depth reality (finding 4).
6. **Security**: findings 2, 7, 9; the export-signing/import-verification and external-agent least-privilege stories are good.
7. **Research freshness**: claims verified true (a first for this review series — the draft's citations were accurate), but not landed per rule 4 and one version unpinned (finding 8).
8. **Testability**: golden conversion, sign/verify cycle, tenant isolation, and a *measured* search-deferral trigger (>5k records / >100ms p95) — exemplary; add a phase-staleness and history-depth assertion when findings 4/5 resolve.

## Disposition

**REVISE.** The core decisions are right and well-bounded: D1 (adopt ADS formats without running ADS) buys portability for zero pods, D2's discovery-not-authz boundary is exactly the statement that makes eventual consistency safe, and D3 defers search on a measured trigger. But finding 1 leaves the discovery URL's serving mechanism resting on a route another design doesn't emit and a SoT rule it contradicts, finding 2 leaves the only third-party content path unverified, and finding 3 continues the silent-amendment habit against the very design this one is the storage half of. All three have small, concrete fixes — this should pass quickly on r2.

VERDICT: REVISE — 9 findings

---

## Re-review r2 (2026-08-20)

- **Verdict**: **PASS** (1 residual MINOR, non-blocking)
- **Independence note**: same independent session as r1; did not author the draft or the revision.

### Per-finding disposition

| r1 | Severity | Disposition |
|---|---|---|
| 1 | MAJOR | **Resolved for the main path.** Gateway *routes* the well-known path to the agent container — SoT preserved per ADR-0019; design 03 §3.4 gained the "Card discovery route" row; the directory's embedded card is explicitly never the wire answer. Residual **R2-a** below on the external-agent exception. |
| 2 | MAJOR | **Resolved.** Federation is an explicit curated verb (`dir import-tools --namespace <allowlisted>`), never background sync; schema-validated, provenance displayed at pick time per the wizard rule; imported records are not bindable — binding still requires the Connector/MCPServer CR path + admission. Failure row added. The "directory suggests, CRs grant" formulation is exactly right. |
| 3 | MAJOR | **Resolved.** Design 02 §11 A2 records the canonical dot-token layout (including `.active` and `tools.*`, with the `@`-charset caveat and the prefix-watch rationale); design 05 now cites the amendment instead of contradicting the approved text. |
| 4 | MINOR | **Resolved.** Bucket history pinned at 10 with the 64-cap/default-1 facts stated; audit reassigned to the `dir.changed` CloudEvents + receipts; history correctly demoted to convenience. |
| 5 | MINOR | **Resolved.** Operator re-writes the record on phase transitions (single writer, per-key — the cheap option recommended); test added. |
| 6 | MINOR | **Resolved — upgraded, even.** Real `dir.changed` CloudEvents emitted on registration writes, making the Event primitive claim true *and* serving as the finding-4 audit fix; KV watch demoted to implementation detail. |
| 7 | MINOR | **Resolved.** Dangling operator-HTTP read path deleted; read paths are KV-direct and gateway only. |
| 8 | MINOR | **Resolved.** `docs/research/oasf-ads-2026-08.md` landed (ADS sources + the JetStream KV mechanics); OASF pinned at 1.1.0 with a live verification note. |
| 9 | MINOR | **Resolved.** External publication is explicit-only (`dir export` + push); `expose.visibility: public` exposes an endpoint, never publishes a record. |

### Residual (new in r2, MINOR, non-blocking)

- **R2-a — the external/scaled-to-zero card exception still assumes an unverified gateway mechanism.** §3.3 has the gateway "serve" the CR-inline card override — a direct-response-with-payload capability no research note covers, and design 03's new row only says "routed to the agent container". Fix in place: for external agents whose endpoint serves a card, *route* to that endpoint (no new capability needed); only the inline-override case needs direct-response — verify that capability (add to the agentgateway research note) or serve the exception path differently, and extend the 03 row to say which.

### Verdict

**PASS.** All 9 findings genuinely addressed; several fixes (CloudEvents-as-audit, "directory suggests, CRs grant") improved the design beyond what the findings demanded. Fold into ADR-0022 with R2-a resolved at 03's next touch.
