# Review: Design 04 — Receipt tap + receipt envelope (`receipt/v1`)

- **Verdict**: **REVISE**
- **Reviewed**: `docs/designs/04-receipt-tap.md` (draft, 2026-08-20)
- **Independence note**: reviewed in a fresh session that did **not** author the draft. No independence caveat needed. (Same session produced `reviews/03-review.md`; design 03 is used here only for its interface claims, per its own REVISE status.)
- **Method**: all 8 critique lenses; consistency checked against architecture.md, approved designs 01+02, draft 03 (interfaces only), ADRs 0002/0003/0010/0013/0014; third-party claims verified by current-year web search (sources inline).

## Findings

### 1. BLOCKER — the dedup design is broken as specified: a fresh ULID per transform defeats `Nats-Msg-Id`, so "exactly-once stream append" does not hold

`04-receipt-tap.md:37,73,102`. The pipeline is at-least-once by construction: the gateway's OTLP exporter retries batches (§5 row 1 relies on it — "recovery backfills from gateway retry"). On any redelivery — tap crash after publish-attempt, ambiguous publish outcome, exporter retry after timeout — the tap transforms the same spans **again** and mints **new ULIDs** (`receipt_id: "01J…"`, generated at transform time). `Nats-Msg-Id: receipt_id` then differs between attempts, JetStream's dedup sees two distinct IDs, and duplicate receipts append. D3's claimed "at-least-once ingest, exactly-once stream append" is false exactly when it matters (during the failures it exists for). Downstream this double-counts cost — corrupting the audit index ("what did user X spend") and design 03's receipt-based budget backstop — and duplicates eval/replay data. This is the hash-before-fetch class bug (cf. design 02 review finding 1).

Two aggravations, verified this session: [JetStream dedup is a sliding window, default **2 minutes**](https://docs.nats.io/nats-concepts/jetstream/streams), while §5 contemplates *sustained* gateway-side buffering — redeliveries arriving after the window duplicate even with a stable ID; and the dedup map costs [~130–150 bytes per entry](https://www.synadia.com/insights/checks/nats-large-deduplication-window), so the window can't be naively set huge at 500 receipts/s.

**Fix**: (a) derive `receipt_id` **deterministically** from the span identity — e.g. UUIDv5/hash over `(trace_id, span_id)` — so every transform of the same span yields the same ID (ULID's timestamp+randomness is the wrong tool here; if ULID ordering is wanted for keys, derive the entropy bits from the span hash); (b) make object-store offload keys idempotent the same way (overwrite-same-content, not append); (c) explicitly size the stream `Duplicates` window ≥ the gateway exporter's maximum retry horizon and state the memory budget at the §5 rate thresholds; (d) add a redelivery e2e test (deliver the same batch twice → exactly one append, see finding 12).

### 2. MAJOR — the semconv "pin" has nothing to pin: GenAI conventions moved to an unreleased repo in June 2026

`04-receipt-tap.md:30,96`; also touches ADR-0003 and `docs/research/landscape-2026-08.md:15`. The design's evolution story ("Semconv version pinned (ADR-0003); the transform is the single place semconv renames are absorbed"; golden fixtures "per pinned semconv version") assumes a versioned convention release. Verified this session: [as of mid-2026 every `gen_ai.*` attribute is still Development-stability, and the conventions were moved out of the main repo](https://dev.to/azena-ai/opentelemetrys-genai-semantic-conventions-are-not-stable-yet-heres-what-actually-shipped-in-2026-3mke) — [semantic-conventions v1.42.0 (June 2026) deprecated/moved all `gen_ai.*` content to `open-telemetry/semantic-conventions-genai`, which has **no releases or tags and no finalized schema URL**](https://john-hodge.com/blog/opentelemetry-genai-semantic-conventions/). "Pin the version" is not currently an executable instruction; the landscape research note predates the split and is now stale on this point.

**Fix**: restate the pin as what is actually pinnable today — a commit SHA / dated snapshot of `semantic-conventions-genai` (or the last monolithic release, v1.41, as the frozen baseline), recorded in the design and in the golden-fixture set's name; note the repo-split rename wave as a known absorb-in-transform event; refresh the research digest line; ADR-0021 should state the pin mechanism, and ADR-0003's "convention version pinned" wording deserves a clarifying note.

### 3. MAJOR — one global `RECEIPTS` stream contradicts ADR-0013's account-based tenant isolation

`04-receipt-tap.md:63`. Subjects carry a `<tenant>` token inside a single stream, while the same line claims "tenant isolation = NATS accounts, ADR-0013". These compose incorrectly: a JetStream stream lives *within* an account, and accounts are full namespace boundaries — a per-tenant-account model (ADR-0013: "per-tenant JetStream isolation"; architecture §06 table says receipts specifically) implies per-tenant streams, not one shared stream with tenant subject prefixes. As written, receipt isolation degrades to subject-filter discipline in a shared account — a much weaker guarantee than the architecture promises for the *audit log*, of all things.

**Fix**: pick one and specify it. Recommended: per-tenant `RECEIPTS` stream in the tenant's account (tap publishes into each account via configured accounts/credentials; audit-index projector runs one durable consumer per tenant; retention/compliance overrides become naturally per-tenant). If core (single-team OSS) deliberately runs one account/stream and accounts only activate with the enterprise Tenant CR, say exactly that and mark the multi-account fan-out as the design-26 seam.

### 4. MAJOR — design 03's budget backstop consumer is missing from the read contract

`04-receipt-tap.md:65-69` vs `03-policy-compiler.md:57`. Draft 03's D3 makes "actual cost from receipts" the *exact* tier of budget enforcement, and 03's header lists 04 as an interface. But §3.4 names only designs 16/17 as consumers — and simultaneously rules "never the Postgres index" for consumers, while the per-agent daily spend the backstop needs is precisely the SQL-shaped aggregate the index exists for. No interface for spend aggregation (per-agent, per-day, reset semantics, staleness bound) is defined anywhere; 03-review finding 5 flagged the same gap from the other side.

**Fix**: add the budget backstop as a named consumer with an explicit contract — recommended: the audit-index projector maintains the per-agent daily spend aggregate (it already projects `usd`), exposed to the operator with a stated staleness bound (which is what makes 03-D3's "cutoff may trail by one reconcile interval" claim honest — index lag adds to that trail and must be included). Clarify that the "never the Postgres index" rule applies to *fidelity* consumers (eval/replay), not to aggregate consumers.

### 5. MAJOR — `capture.level: headers` persists credentials; no header-scrubbing rule exists

`04-receipt-tap.md:49-52,59,88`. At `headers` (and `full`) capture, request headers land in the stream/object store — and hop traffic headers include `Authorization` bearer tokens, OAuth client secrets in token exchanges, cookies, and MCP backend API keys. §6 covers PHI redaction (compliance profiles, gateway-side) but says nothing about *credentials*, which are a problem in every profile including core. A 30-day append-only, no-delete store of live bearer tokens, readable by any receipt consumer, is a credential trove.

**Fix**: a mandatory, always-on, non-configurable denylist of credential-bearing headers (`Authorization`, `Proxy-Authorization`, `Cookie`, `Set-Cookie`, `X-Api-Key`, plus the gateway's own auth headers) scrubbed at every capture level — ideally gateway-side (before export, like the compliance filters) so the tap never sees them; stated in §6 and covered by a golden fixture (finding 12).

### 6. MAJOR — embedding the tap in the operator binary couples control-plane availability to data-plane telemetry volume, and the failure table doesn't cover it

`04-receipt-tap.md:14,79-84`. D2's 0-pod choice is doctrine-honest, but the consequence is unexamined: at the design's own thresholds (500 receipts/s), OTLP ingest, transform, offload I/O, and bounded disk buffers all run inside the same process that reconciles every Agent CR. A telemetry spike or buffer bloat can OOM or GC-stall the binary and take *reconciliation* down with it — the failure table covers every tap-side outcome but not the operator-side blast radius, and no resource isolation (separate memory budget, listener backpressure, load-shedding priority) is stated. The correct failure bias also needs saying out loud: under pressure, shed telemetry (visible via `ReceiptsDegraded`) before starving reconcile.

**Fix**: add a failure row for "tap load degrades operator" with the mitigation (hard per-subsystem memory/queue caps, drop-telemetry-first priority, and the split-mode runbook as the escalation); state that the split trigger alert fires *before* the coupling becomes dangerous (the 250ms/500/s thresholds are good — tie them to this risk explicitly).

### 7. MINOR — "Research fact" not landed in `docs/research/` (claims verified, process not followed)

`04-receipt-tap.md:22`. Same process violation as 03-review finding 2: CLAUDE.md rule 4 requires research to land in dated files. The claims themselves check out this session — [agentgateway natively exports OTLP traces](https://agentgateway.dev/docs/standalone/main/integrations/observability/opentelemetry/) [following GenAI semconv, including MCP tool discovery/execution spans with parameters, results, and backend latency](https://agentgateway.dev/blog/2026-02-17-agentgateway-langfuse-integration/) — so this is landing work, not rework. **Fix**: fold into the same dated `docs/research/agentgateway-2.2-*.md` note 03-review already requires; add the semconv repo-split findings (finding 2) to the digest.

### 8. MINOR — object-store body refs are not the Artifact primitive

`04-receipt-tap.md:16`. The charter's Artifact primitive is **OCI** (architecture §15); JetStream object-store blobs are internal storage, not OCI artifacts. Claiming the primitive here dilutes the charter check that gates every design. **Fix**: drop the Artifact claim — Event (CloudEvents) alone is the primitive story; body refs are implementation detail. (JetStream object store itself is real and fine — [chunked large-object storage is a standard JetStream component](https://docs.nats.io/concepts/jetstream).)

### 9. MINOR — `ReceiptsDegraded` is another unrecorded design-02 condition delta, and its gap-detection mechanism is unstated

`04-receipt-tap.md:79`. Design 02's approved condition list doesn't include `ReceiptsDegraded` — the same silent-amendment pattern flagged in 03-review (findings 5). And the trigger — "gap counter from gateway export metrics" — implies the tap scrapes/consumes the gateway's exporter drop metrics, an ingest path (metrics, not traces) the design never specifies. **Fix**: record the condition addition as a design-02 delta (bundle with 03's, one amendment note or in ADR-0021); specify how export-gap metrics reach the tap (OTLP metrics on the same pipe? Prometheus scrape?).

### 10. MINOR — ADR header list incomplete

`04-receipt-tap.md:5`. The body relies on ADR-0003 (semconv pin, `:30`), ADR-0010 (on-behalf-of chain, `:41`), and ADR-0013 (NATS accounts, `:63`), but the header lists only 0002 and 0014. Same hygiene issue as 03-review finding 8. **Fix**: list them.

### 11. MINOR — pricing-table absence at the tap has no failure row

`04-receipt-tap.md:48,77-84`. `usage.usd_est` comes from the design-03 pricing ConfigMap; if it's missing/stale or a model matches no pattern, what does the tap emit — `null`, `0`, or an error? `0` silently corrupts the audit index's spend answers and the finding-4 backstop aggregate. **Fix**: one failure row — recommended `usd_est: null` + a `pricing: unresolved` marker + counter metric, and the spend aggregate must treat null as "unknown", never zero.

### 12. MINOR — test plan misses the properties the design's guarantees rest on

`04-receipt-tap.md:96`. No test for: dedup under redelivery (finding 1 — deliver one batch twice, assert one append), tenant isolation on the stream/subjects (finding 3), or header/credential scrubbing (finding 5). The listed tests are good but they exercise the happy path plus one outage. **Fix**: add these three; the redelivery test should run with the dedup window boundary (one redelivery inside, one outside) to make the window-sizing decision observable.

## Lens summary

1. **Doctrine**: 0 pods / existing deps only — genuinely clean on paper; finding 6 is the engineering debt of that cleanliness, not a gate violation.
2. **Charter**: slow plane justified (platform guarantee); finding 8 (Artifact mislabel) is the only charter blemish.
3. **Contract consistency**: findings 3, 4, 9, 10 — the recurring cross-design pattern (silently amending 02, under-specifying the 03 seam) continues from the 03 review.
4. **Hidden dependencies/circularity**: finding 1 is this lens's classic bug (ID minted after the fact it must dedupe); finding 9's metrics path is a hidden ingest dependency.
5. **Failure modes**: strong table — the split trigger with stated numbers is exemplary; findings 6 and 11 are the gaps.
6. **Security**: finding 5 is the serious one; mTLS/SVID-pinned listener, tenant-scoped bodies, and redact-before-tap are all sound. (Minor observation, no finding: how `hop.type` distinguishes `kg_query` from `mcp_tool` — presumably backend type from design-03 resources stamped into span attributes — deserves one clause.)
7. **Research freshness**: findings 2 and 7 — one confirmed claim not landed, one landed claim now stale in an important way (the un-pinnable semconv).
8. **Testability**: golden fixtures + semconv-upgrade dual-fixture test are excellent; finding 12 closes the gap between tests and claimed guarantees.

## Disposition

**REVISE.** This is a stronger draft than 03 — D1 (derive receipts from the gateway's own export rather than a bespoke intercept) is well-argued and now verified, the tiered delivery guarantee (D3) is honestly stated, and the failure table has real numbers. But finding 1 falsifies a guarantee the design explicitly claims (exactly-once append) on which audit integrity and 03's budget backstop both stand, and findings 2–6 each leave a promised property (pinned evolution, tenant-isolated audit, backstop feed, credential safety, control-plane availability) without a mechanism. Fix, land/refresh the research notes, re-run critique.
