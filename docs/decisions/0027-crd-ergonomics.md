# ADR-0027: CRD ergonomics — nesting must discriminate, and the front door must stay short

- **Status**: accepted · 2026-08-22, **r2 after independent critique returned REVISE** (1 blocker, 4 major). Raised at the start of implementation, before any reconciler existed.
- **Context**: The Agent CRD is the platform's front door. Design 02 reached PASS on correctness — its conditions, 7 phases, and 10 spec fields all earn their place — but correctness and adoptability are different properties, and nothing in the design phase tested the second one. Every governed platform that became unusable did so one justified field at a time.

## Decision

Three rules, each enforced by a test rather than by review discipline.

**1. A wrapper earns its nesting only when it selects between alternatives.** `expose: {a2a: {…}}` keeps its wrapper: the arm names a protocol and a second arm (MCP exposure, ADR-0016) is coming, so the nesting carries a choice. `knowledge[].graphRef` and `tools[].mcpRef` lose theirs: each named a *protocol*, not a choice, and cost two lines per binding while teaching two names — `graphRef`, `mcpRef` — for one idea. Bindings are now inline:

```yaml
knowledge:
  - name: payer-policies
    version: "v12"
tools:
  - name: claims-system
```

**2. One vocabulary across CRDs.** A concept keeps its name wherever it appears. The Workflow step's tool reference (design 21) genuinely needs a sub-object — it groups a server, a method, and args — but its `mcpRef` sat beside a `name` meaning the *method*, which read as two things called name. It becomes `{server, name, args}`. The directory record (05) and the App grant (23) follow the Agent's `knowledge`.

**3. A binding resolves in the agent's own namespace, and there is no field to say otherwise.** Design 24 §4.1 derives the `can_call` ReBAC tuple *from* the binding, so a cross-namespace reference would authorize itself: anyone able to create an Agent in one namespace could reach a tool in another. A tool name may still be served by a Connector facet or an `MCPServer` — two kinds, one namespace-unique name space enforced at admission — which is why the flattening survives even though the original "one kind" premise did not.

**4. The minimal agent is the budget, not the accident.** Everything except `runtime.image` is optional or defaulted, and the API server — not the operator — fills in `replicas: 1`, `port: 8080`, and the A2A card path. A developer's first Agent is four lines of spec.

**5. Mistakes are rejected at apply time, in a message that says what to do.** Two rules that design 02 stated in prose but nothing enforced are now CEL on the schema — no webhook, no core pod, no reconcile-time surprise:

| A developer writes | They are told |
|---|---|
| neither `runtime` nor `external` (or both) | *set exactly one of spec.runtime (an agent this cluster runs) or spec.external (an agent running elsewhere)* |
| `sandbox` with `replicas: 3` | *spec.runtime.sandbox is a stateful singleton: set replicas to 1, or drop sandbox to scale out* |

## Consequences

- **Design 02 §3.1, design 01 §bind, and designs 05/11/21/23 are amended** to the flattened bindings and the single vocabulary; `architecture.md` §7 and the rendered HTML follow. `reviews/01-recritique.md` is left as written — a review record is history, not a document to keep current.
- **The rules are tests, and the tests are mutation-checked.** `api/v1alpha1/schema_contract_test.go` asserts *schema properties* — no top-level field is required, no field outside a named allowlist is required anywhere, the printer-column set equals design 02 §3.1's, and — **erratum (2026-08-26)** — for the condition vocabulary, considerably less than this ADR originally claimed. What `TestConditionVocabularyIsClosed` asserts is a **hard-coded length of 21** and no duplicates. It does not compare names to anything: a mutation renaming `CondPricingStale` to `TotallyWrongName` survived it. Design 02 §3.1 now declares **25** (A15), so the test checks a number the design no longer states, and nothing is enforced — not even cardinality. A first erratum said "it asserts the length declared in design 02 §3.1", which was the same species of claim; it is corrected rather than removed so the pattern stays visible. The fix design 02 A15 owes is the `designConditions()`-vs-§3.1 comparison, and the count deliberately lives in one place because restating it here is how it drifted. `test/envtest/agent_dx_test.go` proves against a real API server that the minimal and realistic agents are accepted, that the four common mistakes are rejected with the messages above, that a cross-namespace tool reference is rejected under `fieldValidation=Strict` (what kubectl sends) and pruned without it, and that the fields behind `Eval` and `Cost/Day` persist. Each assertion was verified by mutation — the rule it names was deleted and the test observed to fail — because the first version of this suite contained two that could not fail at all.
- **CI runs them.** `.github/workflows/ci.yml`; before it existed, "enforced by a test rather than by review discipline" meant "enforced by remembering to type `make`".
- **The cost of this change is zero now and high later.** `v1alpha1`, no users, no stored objects. The same edit after v1beta1 is a conversion webhook.
- **What the critique changed.** The first draft of this ADR justified the flattening with "a binding points at exactly one kind of thing." That is false for tools — design 02's original line read `mcpRef: {name: claims-system}   # Connector tool facet or MCPServer`, two kinds, and the amendment had deleted the comment saying so. The flattening stands on the corrected premise above; the resolution rule it depends on is now stated in design 02 §3.1 rather than left implicit. The same pass shipped `tools[].namespace` as an ergonomic afterthought, which was a privilege-escalation path; it is deleted, and `TestToolBindingCannotReachAnotherNamespace` keeps it deleted.
- **Deliberately not changed**: the condition vocabulary and the 7 phases. Conditions are read by machines and by humans already debugging; `kubectl get ag` answers "what state is this in?" from the printer columns without reading one. That argument was initially unsupported — the code shipped `Candidate` where design 02 specifies `EVAL` and `COST/DAY`, the two a developer would otherwise dig through conditions for, and no status field existed to source them. `status.eval` and `status.budget.usdSpentToday` now exist and the column set is pinned to the design.
