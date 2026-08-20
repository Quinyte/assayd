# Design 11: Connector CRD (three facets)

- **Status**: draft — awaiting critique
- **Phase**: P2 · **Size**: M · **Date**: 2026-08-20
- **ADRs**: 0009 (three planes) · interfaces: 03 (tool Backends/policies), 05 (tools.* directory), 06 (credentials/OAuth), 14 (ingestion readers), 21-workflow (event triggers, P4 seam)
- **Research**: `docs/research/connectors-2026-08.md` — CloudEvents NATS/JetStream protocol binding is an official spec binding with a maintained Go SDK protocol; content modes structured/binary/batch.

## 1. Purpose & scope

One CR per system-of-record, three optional facets (ADR-0009): **tool** (agent-facing MCP), **ingestion** (bulk → KG), **events** (async → CloudEvents → Workflows). Credentials declared once. In scope: CRD schema, facet reconciliation, the event receiver, credential handling. Out of scope: reader implementations (design 14 executes them), MCP catalog content (packs), CDC (**explicitly deferred** — v1 events are webhook + poll; CDC arrives later as a reader/receiver type in a pack, recorded so it's a scope cut, not an omission).

## 2. Doctrine & charter gates

- **Plane**: slow (CRD + controller in the agent-operator binary — no new pod). Facet *content* (catalog images, reader types) is fast-plane pack data.
- **Pods**: 0 platform pods. Facets create **workload** pods only where unavoidable: the tool facet's MCP server Deployment (per connector, only if `tool.image` mode) and a continuous-ingestion worker (only if `ingestion.mode: continuous`). The **event receiver is a listener in the operator binary** (same pattern + shed-first caps as the tap, ADR-0021 D2; split-mode flag at the same style of threshold).
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
    image: ghcr.io/plume-catalog/fhir-mcp:1.2.0    # catalog (pack-delivered, signed) …
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
  conditions: [ToolReady, IngestionReady, EventsReady, CredentialsValid]
  toolBackend: …                     # emitted Backend name (design 03)
```

Facets are independent: any subset may be present; each gets its own condition; a facet error never degrades the others.

## 4. Facet reconciliation

- **tool**: `image` mode ⇒ operator creates the MCP-server Deployment (workload; signed-image admission applies) + design 03 emits `AgentgatewayBackend` and tool-filter policy from `toolAllowlist`; `endpointRef` ⇒ Backend only. Either way a `tools.<ns>.<name>` directory record is written (design 05) with provenance `connector`. Agents reference `tools: [{mcpRef: claims-system}]` — the Agent CR's mcpRef resolves to this facet.
- **ingestion**: the facet *declares*; **design 14 executes**. Batch ⇒ the operator materializes a suspended CronJob template (reader image + config + credentials mount) that design 14's snapshot builds invoke; continuous ⇒ a worker Deployment (workload pod) streaming into the staging version via the kgp admin surface. Reader types resolve from pack-registered reader images (signed).
- **events**: inbound path is `gateway route (HMAC/auth policy compiled by 03) → operator's receiver listener → CloudEvents (binary content mode) published to JetStream subject events.<ns>.<connector>.<ceType>` per the official NATS binding. `poll` mode: the receiver runs the poll on `schedule`, diffs by the reader's cursor semantics, emits the same CloudEvents. Consumers: Workflow triggers (design 21, P4) — until then events accumulate under stream retention (documented: events land from P2, are *actionable* from P4; retention default 7d).

## 5. Failure modes

| Failure | Behavior |
|---|---|
| Credentials invalid/rotated | `CredentialsValid=False`; affected facets degrade with their own conditions; tool Backend withheld (03 fail-closed ordering applies) |
| Catalog image unsigned | Admission rejects (same bar as agents) |
| Webhook signature invalid | 401 at gateway policy where verifiable; receiver double-checks HMAC and drops with counter — never publishes unverified events |
| Receiver overload | Shed-first caps (tap pattern); dropped events counted + `EventsDegraded`; bounded buffer |
| Event published, no consumer (pre-P4) | Retained per stream policy; documented, visible in `plume dir`/status — not silent loss |
| BYO endpoint unreachable | `ToolReady=False`; agents binding it get `Degraded` per design 02 |

## 6. Security

Credentials only in Secrets/ESO; facets mount narrowly (tool server gets creds, receiver gets only the HMAC secret). Webhook ingress is the *only* unauthenticated-principal path into the platform — hence mandatory signature verification + gateway rate limit + the receiver never trusting payloads (schema-validated, size-capped, mapped — never executed). Tool servers are workload pods under default-deny egress to their system + gateway only.

## 7. Observability

Per-facet conditions; receiver metrics (events in/verified/dropped); ingestion lag surfaced to KG's `KnowledgeStale` inputs (design 20 seam); tool Backend health via gateway.

## 8. Testing

CRD validation table tests; e2e (k3d): catalog tool facet → agent calls tool through gateway with allowlist enforced; webhook with valid/invalid HMAC → exactly the valid one lands as a CloudEvent on the stream; batch ingestion facet consumed by a design 14 fixture Job; facet-independence (break creds → all three conditions accurate).

## 9. Decisions for async review

- **D1 — Event receiver embedded in the operator binary** (tap precedent) with shed-first caps and a split-mode escape; per-connector receiver pods rejected as weight.
- **D2 — CDC deferred to a future reader/receiver pack type**; v1 = webhook + poll.
- **D3 — Events are live from P2 but actionable from P4** (Workflow triggers); retention makes the gap visible, not lossy.
- **D4 — Reader/catalog types are pack-registered content**, never platform code.

## 10. Resulting ADRs

Folded into ADR-0023 (P2 knowledge layer) after critique PASS.
