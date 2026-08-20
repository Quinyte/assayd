# Design 02: Agent CRD + agent-operator

- **Status**: approved (recommendations accepted per standing instruction, 2026-08-20; veto async)
- **Phase**: P1 · **Size**: L · **Date**: 2026-08-20
- **ADRs**: 0002, 0003, 0006 (gating), 0016 (expose/apps) · produces ADR-0019

## 1. Purpose & scope

The Agent CRD is the platform's front door: one object that turns "a container that speaks A2A" into a governed, discoverable, eval-gated workload. The agent-operator's Agent controller reconciles it into: workload, identity, directory entry, gateway routes/policy, and the rollout-gating machinery.

**In scope**: CRD schema, reconcile behavior, revision/rollout model, registration, identity wiring, external agents, finalization.
**Out of scope**: policy compilation internals (design 03), receipt tap (04), eval gate controller (16 — only the *holding mechanism* here), KG binding validation details (01/13).

## 2. Doctrine & charter gates

- **Plane**: slow (engine) — this is one of the three operators; justified as core.
- **Socket**: n/a (engine); everything it *binds* is sockets/standards.
- **Pods added**: the agent-operator pod itself (already in the §17 budget as the one plume-code pod). Workload pods are user workloads.
- **Stateful deps**: none. Operator state = CR status + JetStream KV (directory).
- **Primitives**: Agent(A2A), Tool(MCP), Resource(CRD), Artifact(OCI images). ✓

## 3. Interfaces

### 3.1 CRD schema (`plume.dev/v1alpha1` — group finalized at rename, ADR-0001)

```yaml
kind: Agent
metadata: {name: pa-reviewer, namespace: claims}
spec:
  runtime:                       # exactly one of image | external
    image: ghcr.io/acme/pa-agent:1.4.2     # must be cosign-signed (admission)
    replicas: 2                            # >1 ⇒ kind=Deployment (see 3.2)
    port: 8080                             # A2A server port (default 8080)
    sandbox: {profile: gvisor}             # optional ⇒ kind=Sandbox (singleton)
    resources: {…}                         # normal k8s resources
    env: [...]                             # non-secret; secrets via envFrom
  # external: {endpoint: https://…, auth: {oauthClientRef: …}}   # off-cluster agent
  card:
    path: /.well-known/agent-card.json     # served by the container (SoT = code)
  knowledge:
    - graphRef: {name: payer-policies, version: "v12"}
      scope: {entityTypes: [Policy, Procedure]}      # gateway-enforced (design 01 §6)
  tools:
    - mcpRef: {name: claims-system}                  # Connector tool facet or MCPServer
      requiresApproval: false
  budget: {tokensPerDay: 2M, usdPerDay: 40, taskTimeout: 10m, maxHops: 8}   # per-day windows reset 00:00 UTC; remaining budget in status
  gates:
    - evalSuiteRef: pa-regression                    # ≥1 required in prod (admission)
  expose:
    a2a: {visibility: org, auth: oauth}
status:
  phase: Pending|Held|Canary|Ready|Degraded
  activeRevision: pa-reviewer-7f3a2            # serving traffic
  candidateRevision: pa-reviewer-9c1d4         # held/canary (if rollout in progress)
  card: {name, version, fetchedAt, digest}
  conditions: [Registered, IdentityIssued, KnowledgeBound, GatesPassed,
               SandboxDowngraded, Ready, Degraded]
```

`kubectl get agents` printer columns: `PHASE · ACTIVE · EVAL(last score) · COST/DAY · AGE`.

### 3.2 Workload materialization (decision)

- **Default: Deployment** — A2A servers are stateless HTTP; replicas scale; fits every SDK.
- **`runtime.sandbox` set ⇒ agent-sandbox `Sandbox` (v1beta1)** — singleton, stable identity, persistent scratchpad, gVisor/Kata; for agents that execute code or hold local state. `replicas>1` + `sandbox` is an admission error.
- Sandbox runtime class missing (local distros) → fall back to hardened Deployment (seccomp, non-root, read-only rootfs) + condition `SandboxDowngraded=True` (NFR-8), never silent.
- **Task-state convention** (scale-out honesty): A2A task state is the agent's concern; the SDK templates (design 09) ship a JetStream-KV-backed task store so replicas>1 works out of the box, but the platform does not mandate it.

### 3.3 Revisions & the holding mechanism (entailed by ADR-0006)

k8s rolling update would mix old/new traffic and defeat eval gating — so the operator owns revisions:

1. Spec change → operator computes `revisionHash(spec)` — **spec only**; the card digest is status, and card drift triggers re-registration, never a new revision (review 02, finding 1) — → creates **parallel workload** `<name>-<hash>` alongside the active one.
2. Candidate registers, gets identity, passes readiness — but its gateway route weight is **0** (`phase: Held`).
3. The gate controller (design 16) runs the EvalSuite against the candidate **through the gateway with a candidate-only route header** — that header route is **bound to the gate controller's platform SVID** in gateway policy, so no agent or external principal can reach an ungated revision (review 02, finding 2). Eval traffic otherwise uses the identical path as prod traffic.
4. Pass → weight shifts (configurable canary steps, default `10 → 100`); `activeRevision` flips; old revision GC'd after `revisionHistoryLimit: 2`.
5. Fail → candidate deleted, report in status + PR annotation; active untouched.

Rollback = re-point to a retained previous revision (instant, no rebuild).

### 3.4 Registration & card handling (decision)

**The card is served by the container — source of truth is the agent's code**, not YAML. Operator fetches it after the candidate is Ready (in-cluster GET, 3 retries), validates minimal invariants (parseable, name matches CR, A2A version supported), stores digest in status, wraps it in an OASF record, and writes it to the **directory** (JetStream KV, key `agents/<ns>/<name>@<revision>`; entries GC'd with their revisions per `revisionHistoryLimit`). Card unreachable/invalid → `Registered=False`, rollout blocked. Card drift at runtime (digest change without spec change) → re-fetch + directory update + event. External agents: card fetched from the external endpoint; CR may inline a card override when the endpoint can't serve one.

### 3.5 Identity wiring

One platform-owned **ClusterSPIFFEID** (spire-controller-manager) with a selector template — no per-agent registrar code:

```
spiffe://<trust-domain>/agent/{{ .PodMeta.Namespace }}/{{ .PodMeta.Labels "plume.dev/agent" }}
```

Operator labels workload pods `plume.dev/agent: <name>` + `plume.dev/revision: <hash>`; SVIDs delivered via spiffe-csi. `IdentityIssued` condition reflects SVID presence. External agents get no SVID — they authenticate to the gateway with OAuth client credentials (IdP client minted by the operator).

### 3.6 Gateway handoff (interface with design 03)

The operator normalizes spec (tools, budgets, expose, KG scopes, revision weights) into an internal `PolicyIntent` struct and calls the **policy compiler as an in-process library**, which emits agentgateway **2.2** resources (`AgentgatewayBackend`, `AgentgatewayPolicy`, HTTPRoute) — the operator owns their lifecycle (ownerRefs). Note: agentgateway 2.2 split from kgateway; we target the 2.2 CRD surface only.

## 4. Behavior — reconcile outline

```
observe Agent CR + owned objects
→ validate (admission already enforced: signed image, prod gates present)
→ ensure workload for desired revision (Deployment | Sandbox)
→ ensure identity labels / oauth client (external)
→ candidate Ready? fetch+validate card → directory upsert
→ compile PolicyIntent → apply gateway resources (weights per rollout state)
→ hold/advance rollout per gate controller signals (status.gates)
→ update conditions + phase; emit events
```

Idempotent; all writes are server-side-apply with field ownership; no state outside CR status + directory KV.

## 5. Failure modes & degraded states

| Failure | Behavior |
|---|---|
| Image unsigned / gates missing in prod | Rejected at admission (CEL), never reconciled |
| Card fetch fails | `Registered=False`, candidate held, retry w/ backoff; alert after 10m |
| Sandbox runtime absent | Hardened-Deployment fallback + `SandboxDowngraded` |
| Gateway API unavailable | Routes unchanged (last applied stand); `Ready` degrades to stale-but-serving; retry |
| SPIRE down | Existing SVIDs valid until expiry; new revisions held (`IdentityIssued=False`) |
| KG binding endpoint invalid | `KnowledgeBound=False` → agent `Degraded`, no traffic if sole knowledge source (design 01 §5) |
| Operator crash | Reconcile resumes from CR/status; revisions are content-addressed (no double-create) |

## 6. Security

Admission: cosign verification, prod-gate presence, sandbox/replicas exclusivity, `expose: public` requires approval label. Workload pods: default-deny NetworkPolicy (egress = gateway only), non-root, read-only rootfs. Directory writes: operator identity only. External-agent OAuth secrets in k8s Secrets (ESO-compatible), never in spec.

## 7. Observability

Conditions as above; events for every transition; metrics: reconcile latency, rollout durations, held-revision count, card-drift count, `SandboxDowngraded` gauge. Shipped alerts: candidate held >1h, repeated card fetch failures, identity issuance failures.

## 8. Testing & conformance

envtest for reconcile logic (revision math, condition transitions); e2e on k3d+kind (NFR-3): deploy sample echo-A2A agent → registered/identity/routed; sandbox fallback path asserted on kind (no gVisor); rollout hold/advance with a fake gate controller; external-agent registration against a mock endpoint.

## 9. Decisions taken (veto async)

1. **Deployment default; Sandbox opt-in via `runtime.sandbox`** (singleton; mutually exclusive with replicas>1).
2. **Card SoT = container-served**, fetched+validated by operator; CR inline override only for external agents.
3. **Gateway-weighted blue-green revisions owned by the operator** (not k8s rolling update) — required by eval-as-admission; `revisionHistoryLimit: 2` for instant rollback.
4. **Identity via one templated ClusterSPIFFEID + pod labels** (spire-controller-manager + spiffe-csi); zero custom registrar code.
5. **SDK templates ship a JetStream-KV task store** so replicas>1 works out of the box; not platform-mandated.

## 10. Resulting ADRs

ADR-0019 (revision/rollout model + card SoT + workload materialization).

## 11. Amendments

- **A1 (2026-08-20, from design 03 r1 findings 3/5)**: budget backstop wiring recorded: (a) new Agent conditions **`BudgetExhausted`**, `BudgetEnforcementDegraded`, `PricingStale` (usd budgets), and **`ReceiptsDegraded`** (design 04: receipt-pipeline gaps affecting this agent); (b) the operator gains one additional reconcile input — the **per-agent daily spend aggregate owned by design 04** (read-only; stream remains source of truth; "no state outside CR status + directory KV" holds — spend state lives in design 04's audit index, not the operator); (c) on exhaustion the rollout machinery applies **weight-0 to serving revisions** until the 00:00 UTC window resets (a guard state, distinct from Held/Canary); (d) gateway-side budget enforcement is a conservative local approximation — the exact tier is the receipt backstop (design 03 D3).
- **A2 (2026-08-20, from design 05 review f3)**: canonical directory key layout is **dot-separated KV subject tokens**: `agents.<ns>.<name>.<revision>`, pointer `agents.<ns>.<name>.active`, plus the `tools.<ns>.<name>` space — supersedes §3.4's `agents/<ns>/<name>@<revision>` sketch (KV keys are subject-path tokens; `@` charset validity unverified — verify against the NATS client spec at implementation). Prefix-watch (`agents.<ns>.>`) is the reason.
- **A3 (2026-08-20, from design 06 r1 f2/f6)**: the operator provisions a per-agent **`agent-actor` OAuth client** at reconcile (`ensureClient(agent-actor)`, design 06) so exchanged tokens' `act` chains name real agents; new conditions `IdPUnavailable`, `OnBehalfOfUnavailable`, `IdentityBootstrapIncomplete`.
- **A4 (2026-08-20, from design 07 r1 f2)**: the gates-required admission rule applies iff the EvalSuite CRD is installed; core-tier rollouts proceed with `GatesSkipped=True` (loud); tier downgrade refused while any Agent is Held/Canary.
- **A5 (2026-08-20, design 08 r1 f2)**: condition `GatesBypassed=DevProfile` (local profile only; admission forbids in prod).
- **A6 (2026-08-20, design 09 r1 f2/f4)**: registration additionally verifies the A2A v1.0 card signature — required for plume-built agents (Sigstore keyless, builder identity), unsigned BYO/external cards register with loud `CardUnsigned`.
- **A7 (2026-08-20, design 09 r1 f5)**: the operator owns the injected env contract for agent workloads: `PLUME_GATEWAY_URL`, `PLUME_KG_ENDPOINTS` (per graphRef bindings), `PLUME_NATS_URL` + tenant task-store creds. Versioned with the CRD; templates consume, never define.
- **A8 (2026-08-20, design 20 r1 f2)**: optional `runtime.llm.fallback` (provider/model) — the `ModelDrifted` remediation target; controller-set `status.llmFallbackActive` folds into PolicyIntent on recompile.
