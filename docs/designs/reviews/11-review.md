# Review: Design 11 — Connector CRD (three facets)

- **Verdict**: **REVISE**
- **Reviewed**: `docs/designs/11-connector-crd.md` (draft, 2026-08-20)
- **Independence note**: reviewed in an independent session that did not author the draft.
- **Method**: all 8 lenses; consistency vs architecture §12, approved designs 01–10 (+ §11 amendments), ADRs 0009/0013/0020–0022, `docs/research/connectors-2026-08.md`; CloudEvents NATS binding verified by current-year search (sources inline).

## Findings

### 1. MAJOR — the tap precedent doesn't transfer: the event receiver parses *internet-origin* payloads inside the control-plane binary

`11-connector-crd.md:15,51,78`. D1 embeds the webhook receiver in the operator "same pattern + shed-first caps as the tap, ADR-0021 D2" — but the analogy breaks on the axis that matters. The tap's listener accepts exactly one peer: the gateway, authenticated by SVID, sending gateway-*generated* OTLP. The event receiver's ultimate origin is arbitrary external systems on the internet; HMAC verification authenticates the *sender*, not the *payload shape*, and the parsing/mapping of attacker-influenceable bytes happens inside the process that holds the operator's RBAC, the IdP service-account key, and every connector's mounted secrets. A parser bug in the receiver is a control-plane compromise — a risk class the tap never had. The shed-first caps address load, not exploitation.

**Fix**: confront the trust differential explicitly. Recommended: the events facet defaults to **split-mode** — the receiver runs as the `--mode` deployment (the mechanism already exists) whenever any Connector has an events facet with external exposure, with a minimal mount set (HMAC secrets only, no operator RBAC beyond publishing); in-operator embedding stays available for `local` profile. Alternatively, keep it embedded but state the hardening contract (memory-safe parse path only, hard size/time caps — size caps exist —, no reflection-based mapping) *and* accept the residual risk in writing. Either is defensible; silence is not.

### 2. MAJOR — continuous-ingestion workers hand kgp-admin write access to pack-provided reader images

`11-connector-crd.md:50` vs design 01 §3.4 ("Admin surface: SPIRE platform identity only"). Continuous mode creates a worker Deployment "streaming into the staging version via the kgp admin surface" — running a **pack-registered reader image** (third-party code by design, D4). Design 01 restricted the admin surface to platform identity precisely so agents and workloads can't touch it; this design would issue that identity (or equivalent credentials) to pack code, making every reader image a potential graph-corruption vector with `begin/write/commit/promote/drop` reach.

**Fix**: scope it structurally. Recommended: workers get a **per-connector, per-staging-version credential** — a gateway-mediated route to *only* `kg.admin.write_batch` on the one staging version (the compiler emits the route + policy; commit/promote stay with the pipeline/operator, design 14) — so a hostile reader can at worst write bad data into a staging version that the invariant gate + quarantine already exist to catch (design 01 §3.4). Record the design-01 consequence (admin-surface authz is per-tool, not all-or-nothing) as a 01 §11 amendment, and note the same rule pre-decides design 14's batch Jobs.

### 3. MAJOR — the events facet has no delivery-semantics story: webhook retries duplicate, poll cursors have no home

`11-connector-crd.md:51,59-61`. Webhook senders retry on timeout — that's the protocol's normal behavior — and the receiver as specified publishes each delivery as a fresh CloudEvent: duplicates on the stream, which in P4 become duplicate Workflow triggers with real-world side effects. The platform has already learned this lesson the hard way (04 r1 finding 1 / ADR-0021's derived-identity rule), and this design doesn't apply it. Separately, `poll` mode "diffs by the reader's cursor semantics" — but the receiver lives in a stateless binary and no storage for the cursor is named; a crash either replays (duplicates) or skips (loss), unstated.

**Fix**: apply the ADR-0021 pattern: CloudEvents `id` derived deterministically from source-event identity (sender's delivery/event id where the system provides one — FHIR/GitHub/Stripe all do — else a hash of the signed payload), published with `Nats-Msg-Id: <ce-id>` and a sized dedup window; poll cursors persist in JetStream KV (`connectors.<ns>.<name>.cursor` — the bucket exists) with write-after-publish ordering stated; ordering guarantees stated honestly (per-connector subject ordering, no cross-connector promises). Add the redelivery e2e test.

### 4. MAJOR — "binary content mode … per the official NATS binding" contradicts the released binding spec

`11-connector-crd.md:6,51` and `docs/research/connectors-2026-08.md:4`. Verified this session: the released CloudEvents NATS binding versions state NATS supports **structured mode only** — ["NATS will only support structured data mode, as the NATS protocol does not currently support custom message headers, which are necessary for binary mode"](https://github.com/cloudevents/spec/blob/v1.0.2/cloudevents/bindings/nats-protocol-binding.md). Modern NATS *does* have headers (this design's own `Nats-Msg-Id` use depends on them), so the main-branch spec may have moved — but the design asserts binary mode *as* spec compliance, and the research note claims "content modes structured/binary/batch" without pinning the spec revision that says so. If the released binding is the reference, the wire format is wrong; if main-branch is, the note must cite the revision. Design 21's consumers will parse whatever this decides.

**Fix**: verify which spec revision (if any) blesses binary-over-NATS-headers and pin it in the research note ([sdk-go's `nats_jetstream/v2`](https://pkg.go.dev/github.com/cloudevents/sdk-go/protocol/nats_jetstream/v2) capabilities are the practical tiebreak); if only structured mode is spec-clean, use structured mode — the payload-in-envelope cost is negligible at webhook rates and spec compliance is the point of using CloudEvents at all.

### 5. MINOR — the compiled gateway pieces have no design-03 rows

`11-connector-crd.md:51,57`. The webhook ingress route ("gateway route (HMAC/auth policy compiled by 03)") and the finding-2 kgp-admin scoped route are gateway config, and 03 §3.4 is the ledger of compiled concerns — neither appears there. Also honest-but-vague: "401 at gateway policy *where verifiable*" — whether agentgateway can verify HMAC signatures is an unverified capability claim; the receiver's mandatory double-check makes it defense-in-depth, but the row should say which part the gateway actually does (rate-limit + size cap at minimum, HMAC only if verified). **Fix**: add the rows (03 §11 or its next revision), with the HMAC-at-gateway capability either verified in the research note or explicitly left to the receiver.

### 6. MINOR — the EVENTS stream is unnamed and un-tenanted

`11-connector-crd.md:51,61`. Events publish to "JetStream subject `events.<ns>.<connector>.<ceType>`" — on what stream, in which account, created by whom? The receipts precedent (ADR-0021: per-tenant stream in the tenant's account; 07's bootstrap job creates streams) answers all three; this design should say the same words: per-tenant `EVENTS` stream, tenant account, chart bootstrap creates it, retention 7d default per stream config. **Fix**: one sentence plus the 07 bootstrap-job mention.

### 7. MINOR — condition-list and visibility nits

`11-connector-crd.md:41,60,61`. §5 uses `EventsDegraded` but §3's conditions list doesn't declare it (ToolReady/IngestionReady/EventsReady/CredentialsValid) — add it. And "visible in `plume dir`/status" — the directory lists tools, not event backlogs; the truthful surface is the Connector CR status + a receiver metric (design 10's catalog could carry an `events_pending` signal once design 21 consumes them). **Fix**: two wording corrections.

## Lens summary

1. **Doctrine**: 0 platform pods with workload pods honestly labeled; finding 1 is the doctrine trade-off (rule 3's library-over-server) meeting its limit — embedding is right for trusted peers, questionable for internet-facing parsing.
2. **Charter**: clean — ADR-0009's three planes mapped exactly; facet content as pack data (D4) is the right split; primitives genuine.
3. **Contract consistency**: findings 2 (design 01's admin rule), 5 (03 rows), 6 (ADR-0013/0021 precedent); the tool facet's directory/Backend/allowlist wiring composes correctly with 03/05, and facet independence with per-facet conditions is well-designed.
4. **Hidden dependencies**: finding 3's cursor state is the classic unowned-state bug; D3's events-live-P2/actionable-P4 gap is honestly documented rather than hidden — good.
5. **Failure modes**: solid table (facet isolation, unsigned-catalog rejection, pre-P4 retention); missing the duplicate-delivery row (finding 3).
6. **Security**: findings 1 and 2 are both real trust-boundary issues; the mandatory HMAC + never-execute-payloads + narrow secret mounts show the right instincts elsewhere.
7. **Research freshness**: the binding claim is cited but wrong-or-unpinned (finding 4) — the one place the landed note overstates.
8. **Testability**: good e2e set (HMAC positive/negative, facet independence); add redelivery-dedup and receiver-overload-shed assertions when findings 3/1 resolve.

## Disposition

**REVISE.** The facet model, credential-once design, and scope honesty (CDC deferred *explicitly*, events actionable-from-P4 *documented*) are strong. But three MAJORs are trust-boundary gaps — internet bytes in the control plane, admin credentials in pack code, and unhandled webhook retries — and the fourth is a cited spec claim the released spec contradicts. All have concrete fixes, two of which reuse mechanisms the platform already built (split-mode, derived-identity dedup).

VERDICT: REVISE — 7 findings

---

## Re-review r2 (2026-08-20)

- **Verdict**: **PASS**
- **Independence note**: same independent session as r1; did not author the draft or revision.

### Per-finding disposition

| r1 | Severity | Disposition |
|---|---|---|
| 1 | MAJOR | **Resolved.** Split-mode is the default for the events facet (`--mode event-receiver`, one shared deployment) with a minimal mount set (HMAC secrets + tenant publish creds; no operator RBAC, no IdP keys, no system creds); in-operator embedding restricted to `local`; the hardening contract stated either way. D1 records the trust rationale in the design's own words — the tap-analogy break is now the argument, not the oversight. |
| 2 | MAJOR | **Resolved.** Workers get a compiler-emitted route scoped to `kg.admin.write_batch` on one staging version; `begin/commit/promote/drop` stay platform-only; design 01 §11 A2 records the per-tool authz consequence ("platform identity only" = default, narrow grants = exception); negative test added (reader image cannot call commit/promote); pre-decides design 14's Jobs as asked. |
| 3 | MAJOR | **Resolved.** Derived CloudEvents `id` (sender event id, else signed-payload hash) + `Nats-Msg-Id` + sized window; poll cursor persisted in KV, advanced after publish (crash ⇒ re-poll ⇒ dedup absorbs); per-connector ordering only, stated; failure row + duplicate-delivery and poll-crash e2e tests. The ADR-0021 pattern, correctly transplanted. |
| 4 | MAJOR | **Resolved.** Structured content mode adopted; the research note corrected and pinned to the released binding v1.0.2 with the pre-NATS-headers context explained and a re-check note before any wire-format change. Honest fix. |
| 5 | MINOR | **Resolved.** Design 03 gained both rows (webhook ingress: rate-limit + size-cap, HMAC explicitly receiver-side pending capability verification; scoped kgp-admin grant). |
| 6 | MINOR | **Resolved.** Per-tenant `EVENTS` stream, tenant account, design-07 bootstrap job, 7d retention. |
| 7 | MINOR | **Resolved.** `EventsDegraded` added to the conditions list; visibility corrected to Connector CR status + `events_pending` metric. |

### Nit (non-blocking)

§4 references "Gate B" — design 14 terminology not yet approved at this doc's writing; once 14 lands, make the reference a citation (`design 14 Gate B`).

### Verdict

**PASS.** All seven findings addressed, the two trust-boundary MAJORs structurally (split-mode with minimal mounts; scoped single-tool admin grants) rather than by caveat. Fold into ADR-0023 with the Gate-B citation nit.
