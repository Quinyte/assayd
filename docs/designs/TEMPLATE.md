# Design: <component name>

- **Status**: draft | in-review | approved
- **Phase**: P1–P5 / ent · **Size**: S/M/L · **Author**: · **Date**:

## 1. Purpose & scope
What this component owns; what it explicitly does not.

## 2. Doctrine & charter gates (mandatory)
- Plane: slow (engine) / fast (technique) — justify if slow.
- Socket: provider slot / gateway filter / CRD+controller / template+skill.
- Pods added: N (justify if >0). Stateful deps touched: none beyond Postgres/NATS?
- Primitives used: Agent/Tool/Event/Resource/Artifact only?

## 3. Interfaces
CRD schema (spec/status/conditions) · contracts consumed/provided (versioned) · CLI verbs.

## 4. Behavior
Reconcile logic / data flow / sequence for the main paths. Mermaid where a picture earns it.

## 5. Failure modes & degraded states
Every downgrade surfaces as a condition (NFR-8). Idempotency, retries, crash-resume.

## 6. Security
Identity, authz checks, secrets, tenant scoping, receipt/audit impact.

## 7. Observability
Metrics, conditions, receipts emitted; alert rules shipped.

## 8. Testing & conformance
Unit/e2e strategy; conformance suite if this defines a contract.

## 9. Open questions & alternatives considered
## 10. Resulting ADRs
