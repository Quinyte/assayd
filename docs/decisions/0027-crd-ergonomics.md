# ADR-0027: CRD ergonomics — nesting must discriminate, and the front door must stay short

- **Status**: accepted · 2026-08-22 (raised at the start of implementation, before any reconciler existed)
- **Context**: The Agent CRD is the platform's front door. Design 02 reached PASS on correctness — 20 conditions, 7 phases, and 10 spec fields all earn their place — but correctness and adoptability are different properties, and nothing in the design phase tested the second one. Every governed platform that became unusable did so one justified field at a time.

## Decision

Three rules, each enforced by a test rather than by review discipline.

**1. A wrapper earns its nesting only when it discriminates a kind.** `expose: {a2a: {…}}` keeps its wrapper: the arm names a protocol and a second arm (MCP exposure, ADR-0016) is coming, so the nesting carries information. `knowledge[].graphRef` and `tools[].mcpRef` lose theirs: a binding can point at exactly one kind of thing, so the wrapper was a nesting level that discriminated nothing while costing two lines per binding and teaching two names — `graphRef`, `mcpRef` — for one idea. Bindings are now inline:

```yaml
knowledge:
  - name: payer-policies
    version: "v12"
tools:
  - name: claims-system
```

**2. One vocabulary across CRDs.** A concept keeps its name wherever it appears. The Workflow step's tool reference (design 21) genuinely needs a sub-object — it groups a server, a method, and args — but its `mcpRef` sat beside a `name` meaning the *method*, which read as two things called name. It becomes `{server, name, args}`. The directory record (05) and the App grant (23) follow the Agent's `knowledge`.

**3. The minimal agent is the budget, not the accident.** Everything except `runtime.image` is optional or defaulted, and the API server — not the operator — fills in `replicas: 1`, `port: 8080`, and the A2A card path. A developer's first Agent is four lines of spec.

**4. Mistakes are rejected at apply time, in a message that says what to do.** Two rules that design 02 stated in prose but nothing enforced are now CEL on the schema — no webhook, no core pod, no reconcile-time surprise:

| A developer writes | They are told |
|---|---|
| neither `runtime` nor `external` (or both) | *set exactly one of spec.runtime (an agent this cluster runs) or spec.external (an agent running elsewhere)* |
| `sandbox` with `replicas: 3` | *spec.runtime.sandbox is a stateful singleton: set replicas to 1, or drop sandbox to scale out* |

## Consequences

- **Design 02 §3.1, design 01 §bind, and designs 05/11/21/23 are amended** to the flattened bindings and the single vocabulary; `architecture.md` §7 and the rendered HTML follow. `reviews/01-recritique.md` is left as written — a review record is history, not a document to keep current.
- **The rules are tests.** `api/v1alpha1/agent_dx_test.go` fixes the minimal-agent line ceiling and the defaults; `test/envtest/agent_dx_test.go` proves against a real API server that the minimal and realistic agents are accepted, that all four mistakes are rejected with the message above, and that status survives a subresource write. A future field that inflates the front door fails a test rather than passing a review.
- **The cost of this change is zero now and high later.** `v1alpha1`, no users, no stored objects. The same edit after v1beta1 is a conversion webhook.
- **Deliberately not changed**: the 20 conditions and 7 phases. Conditions are read by machines and by humans already debugging; `kubectl get ag` answers "what state is this in?" from the printer columns without reading one, which is the property that actually matters and is now tested.
