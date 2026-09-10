# Design 11: Connector CRD (three facets)

- **Status**: **approved** — critique PASS at r2 (reviews/11-review.md) · ADR-0023
- **Phase**: P2 · **Size**: M · **Date**: 2026-08-20
- **ADRs**: 0009 (three planes) · interfaces: 03 (tool Backends/policies), 05 (tools.* directory), 06 (credentials/OAuth), 14 (ingestion readers), 21-workflow (event triggers, P4 seam)
- **Research**: `docs/research/connectors-2026-08.md` — **released NATS binding (v1.0.2) blesses structured mode only** (r1 f4, corrected + pinned); sdk-go `nats_jetstream/v2` is the implementation vehicle.

## 1. Purpose & scope

One CR per system-of-record, three optional facets (ADR-0009): **tool** (agent-facing MCP), **ingestion** (bulk → KG), **events** (async → CloudEvents → Workflows). Credentials declared once. In scope: CRD schema, facet reconciliation, the event receiver, credential handling. Out of scope: reader implementations (design 14 executes them), MCP catalog content (packs), CDC (**explicitly deferred** — v1 events are webhook + poll; CDC arrives later as a reader/receiver type in a pack, recorded so it's a scope cut, not an omission).

## 2. Doctrine & charter gates

- **Plane**: slow (CRD + controller in the agent-operator binary — no new pod). Facet *content* (catalog images, reader types) is fast-plane pack data.
- **Pods**: 0 platform pods. Facets create **workload** pods only where unavoidable: the tool facet's MCP server Deployment (per connector, only if `tool.image` mode) and a continuous-ingestion worker (only if `ingestion.mode: continuous`). The **event receiver defaults to split-mode** (r1 f1): the tap analogy breaks on trust — the tap parses gateway-generated OTLP from one SVID-authenticated peer, while the receiver parses *internet-origin* payloads; HMAC authenticates the sender, not the payload shape. So whenever any Connector has an events facet, the receiver runs as its own `--mode event-receiver` deployment with a **minimal mount set** (HMAC secrets + tenant NATS publish creds only — no operator RBAC, no IdP keys, no connector system creds); in-operator embedding is permitted only in the `local` profile. Hardening contract either way: schema-validated, size- and time-capped, no reflective mapping.
- **Stateful deps**: none new. **Primitives**: Tool (MCP), Event (CloudEvents), Resource, Artifact (catalog images). ✓

## 3. CRD schema

```yaml
kind: Connector
metadata: {name: claims-system, namespace: claims}
spec:
  system: {kind: fhir | s3 | postgres | http | custom, url: …}
  credentials:                       # ONCE, for all facets
    secretRef: claims-creds          # or ESO-managed; oauth: {clientRef} for user-delegated (design 06)
  tool:                              # facet 1 — the ONLY agent-facing surface (ADR-0009)
    image: ghcr.io/assayd-catalog/fhir-mcp:1.2.0    # catalog (pack-delivered, signed) …
    # endpointRef: {url: …}                        # … or BYO running MCP server
    toolAllowlist: [read_claim, search_claims]     # narrows what the server offers
  ingestion:                         # facet 2 — bulk, never MCP
    reader: {type: fhir-bulk, config: {…}}         # reader types are pack-registered
    mode: batch                      # batch (Jobs, design 14) | continuous (worker Deployment)
    schedule: "0 2 * * *"            # batch mode
  events:                            # facet 3 — async in
    receiver: webhook                # webhook | poll   (CDC deferred, §1)
    auth: {hmacSecretRef: claims-webhook-secret}   # webhook signature verification, mandatory
    map:
      - {match: {path: "/claims", header?: …}, ceType: "com.acme.claim.updated"}
status:
  conditions: [ToolReady, IngestionReady, EventsReady, EventsDegraded, CredentialsValid]
  toolBackend: …                     # emitted Backend name (design 03)
```

Facets are independent: any subset may be present; each gets its own condition; a facet error never degrades the others.

## 4. Facet reconciliation

- **tool**: `image` mode ⇒ operator creates the MCP-server Deployment (workload; signed-image admission applies) + design 03 emits `AgentgatewayBackend` and tool-filter policy from `toolAllowlist`; `endpointRef` ⇒ Backend only. Either way a `tools.<ns>.<name>` directory record is written (design 05) with provenance `connector`. Agents reference `tools: [{name: claims-system}]` — the Agent CR's tool binding resolves to this facet.
- **ingestion**: the facet *declares*; **design 14 executes**. Batch ⇒ the operator materializes a suspended CronJob template (reader image + config + credentials mount) that design 14's snapshot builds invoke; continuous ⇒ a worker Deployment (workload pod) — which runs **third-party pack code and therefore never holds platform admin identity** (r1 f2): the compiler emits a **per-connector, per-staging-version gateway route scoped to `kg.admin.write_batch` only**; `begin/commit/promote/drop` remain with the pipeline/operator. A hostile reader can at worst write bad data into a staging version that Gate B + quarantine already exist to catch. (Consequence — kgp admin authz is per-tool, not all-or-nothing — recorded as design 01 §11 A2; the same rule pre-decides design 14's batch Jobs.) Reader types resolve from pack-registered reader images (signed).
- **events**: inbound path is `gateway route (rate-limit + size-cap policy compiled by 03; HMAC verification is the receiver's job unless a gateway capability is verified — the 03 row states which) → event receiver → CloudEvents in **structured content mode** (the released NATS binding v1.0.2 defines structured only — r1 f4) published via sdk-go nats_jetstream`. **Delivery semantics (r1 f3, the ADR-0021 lesson applied)**: CloudEvents `id` is derived deterministically — the sender's delivery/event id where the system provides one (FHIR/GitHub/Stripe do), else a hash of the signed payload — published with `Nats-Msg-Id: <ce-id>` into a sized dedup window; webhook retries collapse to one event. `poll` cursors persist in JetStream KV (`connectors.<ns>.<name>.cursor`), advanced **after** publish (crash ⇒ re-poll ⇒ dedup absorbs). Ordering: per-connector subject order only — no cross-connector promises. Streams: a **per-tenant `EVENTS` stream in the tenant's account, created by the design-07 bootstrap job, retention 7d default** (r1 f6). Consumers: Workflow triggers (design 21, P4) — events land from P2, are *actionable* from P4; the gap is visible in Connector CR status + an `events_pending` metric (r1 f7), never silent loss.

## 5. Failure modes

| Failure | Behavior |
|---|---|
| Credentials invalid/rotated | `CredentialsValid=False`; affected facets degrade with their own conditions; tool Backend withheld (03 fail-closed ordering applies) |
| Catalog image unsigned | Admission rejects (same bar as agents) |
| Webhook signature invalid | Gateway enforces rate-limit + size caps; **the receiver owns HMAC verification** and drops invalid with a counter — never publishes unverified events |
| Receiver overload | Shed-first caps; dropped events counted + `EventsDegraded`; bounded buffer |
| Webhook sender retries | Deduped by derived CE id within the window (r1 f3); beyond-window duplicates counted (`late_redelivery` pattern from ADR-0021) |
| Event published, no consumer (pre-P4) | Retained per stream policy; visible in Connector CR status + `events_pending` metric (r1 f7) |
| BYO endpoint unreachable | `ToolReady=False`; agents binding it get `Degraded` per design 02 |

## 6. Security

Credentials only in Secrets/ESO; facets mount narrowly (tool server gets creds, receiver gets only the HMAC secret). Webhook ingress is the *only* unauthenticated-principal path into the platform — hence mandatory signature verification + gateway rate limit + the receiver never trusting payloads (schema-validated, size-capped, mapped — never executed). Tool servers are workload pods under default-deny egress to their system + gateway only.

## 7. Observability

Per-facet conditions; receiver metrics (events in/verified/dropped); ingestion lag surfaced to KG's `KnowledgeStale` inputs (design 20 seam); tool Backend health via gateway.

## 8. Testing

CRD validation table tests; e2e (k3d): catalog tool facet → agent calls tool through gateway with allowlist enforced; webhook valid/invalid HMAC → exactly the valid one lands; **same webhook delivered twice → exactly one event** (r1 f3); poll crash/restart → no loss, no dupes within window; worker credential negative test (reader image cannot call `commit/promote`); batch ingestion facet consumed by a design 14 fixture Job; facet-independence.

## 9. Decisions for async review

- **D1 — Event receiver is split-mode by default** (internet-origin parsing never lives in the control-plane binary; r1 f1); `local`-profile embedding only. One shared receiver, not per-connector pods.
- **D2 — CDC deferred to a future reader/receiver pack type**; v1 = webhook + poll.
- **D3 — Events are live from P2 but actionable from P4** (Workflow triggers); retention makes the gap visible, not lossy.
- **D4 — Reader/catalog types are pack-registered content**, never platform code.

## 10. Resulting ADRs

Folded into ADR-0023 (P2 knowledge layer) after critique PASS.

## 11. Owed to this design (2026-09-04, from design 02 §3.1 and ADR-0027)

**This design must define the `MCPServer` kind and the name-uniqueness rule that three other documents already depend on, and nothing today enforces.**

An Agent binds a tool by bare name (`tools: [{name: claims-system}]`) with **no kind discriminator and no namespace field**. Design 02 §3.1 and ADR-0027 decision 3 both justify that shape by asserting that a tool name is unique across Connector tool facets and `MCPServer` CRs within a namespace, "enforced at admission". Checked against this document: there is no such rule in §§1–10, the string `MCPServer` appears nowhere in them, and there is no admission policy or webhook anywhere in the repository that could carry it. Design 03 §3.1 resolves a tool reference "against a Connector facet or `MCPServer` (design 11)" on the same assumption, and design 05 §3 goes further still — it says binding a tool "requires the Connector/`MCPServer` CR path **with admission checks**", naming an admission check that does not exist. Four documents, one absent rule.

So the binding shape currently rests on a premise no design states. Two consequences, and the second is a security one:

- A Connector facet and an `MCPServer` may take the same name in one namespace. Which one an Agent's `tools[].name` resolves to is then undefined, and the compiler picks one.
- The *absence* of a `namespace` field is a deliberate security property — design 24 §4 derives the `can_call` tuple from the binding, so a cross-namespace reference would authorize itself. That property is unaffected by the gap. What the gap touches is only whether a within-namespace name resolves to one thing.

**Also owed here (design 03 A51, 2026-09-04)**: a `maxItems` bound on `spec.tool.toolAllowlist`. Design 03 seals that list into every revision record of every Agent bound to the connector, and its retained set multiplies it, so an unbounded list is a real storage cost. An earlier note in design 03 owed this design a bound on a tool server's discovered tool catalogue; there is no such concept here — this design has no `tools/list` discovery and no tool catalogue — and the bound belongs on the CRD field this design does declare. Design 03 also depends on a property this document should state outright: **`toolAllowlist` is optional, and omitting it means no tool filter is emitted at all** — the agent gets whatever the server offers. That is the one genuinely uncontracted surface in the tool facet and §5 should name it as a failure mode.

**That sentence is contradicted by the design it names, and the contradiction is unresolved (2026-09-09).** Design 03 `03:102` says the opposite outcome in as many words — "**Undeclared is a compile error, not a wider filter**": design 11 makes the field optional, but design 03 §3.4's tool row makes `-toolfilter` **mandatory**, and §3.3.1 turns a mandatory concern with an absent input into `PolicyCompileFailed` with the route withheld. It also denies the dependency this paragraph asserts — "this compiler never reads a server's surface in any case" — and records the reverse obligation: "**Owed to design 11**: state that omitting `toolAllowlist` makes the tool facet uncompilable under gateway governance". So each document says the other owes it the opposite statement.

The two cannot both describe what a user sees. Omitting the field either yields an agent with **every tool the server offers** or an agent with **no route at all**, and those are opposite failure modes: one is a silent over-grant, the other a loud outage.

**What is measured, and what it does not settle** (design 07 A6.8, `research/agentgateway-v1.5.0-delta-2026-09.md`): with **no** `AgentgatewayPolicy` attached, agentgateway lists and permits every tool the MCP server offers — so this paragraph is correct *about the gateway*. Design 03's claim is about assayd's **compiler**, which would refuse to emit rather than emit nothing. Both are therefore true of different layers, and that is exactly why the user-visible outcome is still undecided: nothing measured here says which layer gets to decide. **This is a design decision and neither document may settle it unilaterally** — design 03 was RE-OPENED when this was written — it has since been consolidated (2026-09-10) and remains not approved — and its consolidation was one place it could land, or an ADR was.

**SETTLED by ADR-0032 (2026-09-10), and this paragraph's claim is WITHDRAWN.** The decision takes neither side: `toolAllowlist` becomes **required with `minItems: 1`**, so an omitted or empty list is rejected at `kubectl apply` and neither outcome — every tool, or no route — is reachable at all. `+required` alone would not do it, because controller-tools accepts an empty slice; `MinItems=1` is part of the decision. Design 03's `PolicyCompileFailed` is kept as the backstop for a CR that predates the constraint, and its "owed to design 11" is discharged by the ADR rather than by an edit here. **The `maxItems` bound design 03 A51 owes this design is a separate question and is still owed** — ADR-0032 sets the floor, not the ceiling.

**Owed here**: the `MCPServer` kind (or an explicit statement that it is another design's), the uniqueness rule across both kinds, and the mechanism that enforces it — which cannot be CRD-level CEL, since it spans two kinds, so it is an admission policy or a controller-side refusal with a named condition. Until it lands, design 02 §5 and ADR-0027 both record the gap rather than asserting the enforcement.

