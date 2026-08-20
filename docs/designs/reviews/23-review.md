# Review: Design 23 — The App layer (HTTP projection, typed clients, release pinning)

- **Verdict**: **REVISE**
- **Reviewed**: `docs/designs/23-app-layer.md` (draft, 2026-08-20)
- **Independence note**: reviewed in an independent session that did not author the draft.
- **Method**: all 8 lenses, with the requested focus: the kro-composes claim vs what the watcher actually does, and the SSE projection vs A2A's own stream.

## Findings

### 1. MAJOR — SSE reconnect replays from the template task store, which is a convention BYO agents don't owe you

`23-app-layer.md:46` vs design 02 §3.2 D5 and design 09 D3. The reconnect row replays events "from the A2A task state (**the template task store**, 09)" — but that store is explicitly *not platform-mandated* (02 D5: "the platform does not mandate it"), and the BYO contract is "any container speaking A2A." As written, the chat projection's reconnect works only for template-built agents, silently breaking the platform's founding promise for everyone else — and it also implies the projection layer reads an agent's *internal* KV state across a boundary nothing else crosses. The right mechanism is sitting in the protocol: A2A v1.0 has task query/resubscribe operations (`GetTask`, `SubscribeToTask` — the spec's own Layer-2 verbs), which are agent-implementation-agnostic by definition.

**Fix**: reconnect = the projection re-subscribes/queries **via A2A itself** at the recorded `task_id` (through the gateway, like everything), replaying the delta as SSE; `Last-Event-ID` maps to the A2A event/status sequence, not a store cursor. The template task store remains what it always was — the thing that makes an *agent's own* A2A task-state answers survive replicas — invisible to the projection. One row rewrite plus a clause in §3's mapping table.

### 2. MINOR — the projection routes have no design-03 rows

`23-app-layer.md:13,19-24` vs design 03 §3.4. "Projection rows in the 03 compiler" is claimed as one of plume's three thin pieces — correctly — but 03's concern table gained no App-projection row (workflow POST + run-status routes, chat SSE route, KG read routes, each with OIDC + exchange + CORS-pinned-to-route-host + `consumerBudgets`, and the App's own KG scope header). The series' standing rule: compiled concerns are rows, not prose. **Fix**: one "App projection" row in 03 §3.4 (or its amendment log) enumerating the three shapes and their attached policies.

### 3. MINOR — `POST /api/<name>` retry semantics are unstated (shared with 21-review finding 2)

`23-app-layer.md:20`. A client retrying a timed-out workflow POST starts a second run unless an idempotency mechanism exists; design 21's run-id derivation covers events but not http. **Fix**: the projection accepts an `Idempotency-Key` header seeding the run id (forwarded to 21's trigger; absent ⇒ new run, documented); the generated client sends one by default — which turns finding 3 into a non-issue for every generated frontend.

### 4. MINOR — browsers' native `EventSource` can't send the Authorization header the SSE route requires

`23-app-layer.md:21,24`. All routes are OIDC-authenticated — but the web platform's `EventSource` API cannot set request headers, the classic SSE-auth trap. The design's own architecture already contains the answer: the *generated client* controls the transport. **Fix**: one sentence in §5 — the generated client implements SSE over `fetch()` streams (headers supported, `Last-Event-ID` managed manually), never native `EventSource`; no cookie fallback needed. Without the sentence, the first frontend built outside the generated client hits a 401 wall and someone "fixes" it with tokens-in-query-strings.

## Lens summary

1. **kro-composes claim** (requested scrutiny): **holds**. The watcher's three jobs — identity provisioning (which *is* 06's "App login clients provisioned by the operator at reconcile", now located), pin/coherence validation, status aggregation — create no resources; the RGD ships in the chart and instances are kro's; the compiler and client-gen carry the rest. The architecture's "App | kro" row stays true, and the design polices its own honesty ("plume adds logic *around* composition, never a composition engine"). No finding.
2. **Doctrine/charter**: clean — 0 platform pods, no state, primitives honest.
3. **SSE vs A2A's stream** (requested scrutiny): the *mapping* is right — D2's "documented projection of A2A's own stream, not a new protocol" is exactly ADR-0016's spirit, and the event vocabulary (status/message/artifact/done) is a thin rename of A2A's task lifecycle. Finding 1 is the reconnect path betraying the same principle the happy path honors.
4. **Contract consistency**: findings 2/3; elsewhere strong — pinning + `MemberSkew` (warn-never-block, D4 — the right call for mid-migration reality), release-triggers-member-gates composition with 02/16, the design-20 retention linkage acknowledged, member-Held returning a typed 503 the UI can render honestly.
5. **Hidden dependencies**: the client-gen digest pinning + `app gen --check` CI gate is the standout — client/server skew made mechanical, the same move as the contracts ledger.
6. **Failure modes**: good table; finding 1 replaces the one wrong row.
7. **Research freshness**: nothing new and load-bearing (A2A stream semantics covered by the landed a2a note); n/a.
8. **Testability**: the projection e2e (login → SSE lifecycle → per-user receipts with correct `act` chains) is the ADR-0016 demo mechanized; add the reconnect-against-BYO-agent case once finding 1 lands — that's the test that would have caught it.

## Disposition

**REVISE.** The layer is admirably thin and honest about staying that way — the kro claim survives scrutiny, the client-gen skew gate is excellent, and the SSE mapping is protocol-respectful except in exactly one place: reconnect, where it quietly depends on a convention the platform swore not to mandate. Fix that through A2A's own verbs, add the compiler row and two one-liners, and this passes.

VERDICT: REVISE — 4 findings
