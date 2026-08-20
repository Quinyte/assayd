# Connector + graphiti + DBOS facts for designs 11–14 (2026-08)

## CloudEvents over NATS/JetStream (design 11 events facet)
- Official CloudEvents **NATS protocol binding**: the released revisions (≤ v1.0.2) state NATS supports **structured content mode only** ("NATS … does not currently support custom message headers, which are necessary for binary mode" — written pre-NATS-headers). plume therefore uses **structured mode** (spec-clean at any revision; envelope cost negligible at webhook rates), implemented via sdk-go `nats_jetstream/v2`. Re-check whether a later spec revision blesses header-based binary mode before changing the wire format — design 21's consumers parse this. https://github.com/cloudevents/spec/blob/v1.0.2/cloudevents/bindings/nats-protocol-binding.md · https://pkg.go.dev/github.com/cloudevents/sdk-go/protocol/nats_jetstream/v2

## graphiti-core (designs 12/13)
- **Custom entity & edge types are Pydantic models** passed to `add_episode`; Graphiti classifies extracted entities against them and populates typed attrs — ontology→Pydantic derivation is the extraction mapping. https://help.getzep.com/graphiti/core-concepts/custom-entity-and-edge-types
- **`add_episode_bulk`**: efficient batch ingestion, documented for **populating empty graphs / when edge invalidation is not required** — fits plume's immutable-snapshot builds (each version built fresh or copy-on-write, never mutated); NOT for live-mutation flows. https://help.getzep.com/graphiti/core-concepts/adding-episodes
- **`group_id` namespacing** scopes episodes + extracted entities — the version-namespace mechanism for `/kgp/<graph>/<version>` endpoints. https://help.getzep.com/graphiti/core-concepts/graph-namespacing

## DBOS (design 14 execution model)
- 2026: **dynamic queues** creatable at runtime via the database; queues are Postgres-backed and integrate with durable workflows (checkpointed steps). https://www.dbos.dev/blog/new-in-dbos-june-2026
- **Kubernetes guidance**: Deployment per active code version; each replica is an independent worker processing scheduled workflows + queue tasks; `dbos migrate` with an admin role, runtime with a restricted role. https://docs.dbos.dev/production/hosting-with-kubernetes
- plume consequence: batch ingestion runs as **k8s Jobs with DBOS inside** (crash-resume via Postgres; Job restart resumes the workflow) — zero standing pods for batch; continuous mode = per-connector worker Deployment (workload).
