# Design 16: EvalSuite CRD, gate controller, dataset builder

- **Status**: **not approved.** Critique PASS at r2 (`reviews/16-review.md`) · ADR-0024. Until A3 this line read "**approved**", while `docs/designs/README.md`, which arbitrates status under ADR-0030, said "awaiting user approval". Under ADR-0030 a PASS is evidence about the prose, not an approval. **§1.1 drafts a first slice (A3, 2026-09-14): ADR-0030 step 4's narrow gate, blue/green only, one EvalSuite. It is awaiting critique, and is approved only when the human approves it after a critique returns PASS.** Nothing this design specifies is implemented: the operator's hold on a declared gate, and `status.eval`, are design 02's.
- **Phase**: P3 · **Size**: L · **Date**: 2026-08-20
- **ADRs**: 0006 (eval-as-admission), 0019 (revision holding), 0020 (candidate isolation), 0021 (receipts) · interfaces: 02 (rollout machine), 12 §3.8 (golden-set seeds), 15 (probe/eval distinction), 17 (session sampling), 18 (runner images via packs)
- **Research**: `docs/research/evals-2026-08.md` — DeepEval 4.x: programmatic `evaluate()`/`evals_iterator()` APIs, built-in agentic metrics (task completion, tool correctness), pytest-style CI integration; Inspect AI as the second runner.

## 1. Purpose & scope

The flagship: semantic admission (FR-20). A new Agent/Model revision earns traffic by passing its EvalSuite against the candidate *through the production path*. In scope: the EvalSuite CRD, the **EvalRunner slot** (`evalrunner/v1`), the gate controller's interaction with design 02's rollout machine, the dataset builder, metric semantics, verdict rules, nightly trend runs. Out of scope: session substrate (17), drift consumption of trends (20), runner pack packaging (18).

### 1.1 The first slice — blue/green, one suite (draft, awaiting critique)

**This section is a draft. It is not approved, and nothing in it is implemented.** It answers ADR-0030's build step 4, "a narrow design-16 gate, blue/green before canaries". It is approved only when the human approves it after a critique returns PASS on it. That approval is recorded in an ADR, the way ADR-0034 Amendment 4 recorded design 03's first slice. The rest of this design stays specified, not approved, whatever happens to this section. *(Added by A3.)*

**Eight questions are the human's, not this draft's (Q1–Q8, at the end of this section).** The slice below is drawn on the recommended answer to each. Wherever another answer would change a rule, the rule names its question.

**What the slice is.** An Agent names one EvalSuite in `spec.gates`. A revision minted by the Agent's current spec is evaluated before it takes any traffic. The agent-operator sends each of the suite's cases to the candidate revision's own Service as one A2A `SendMessage` task, and compares the answer with the case's expectation. If enough cases pass, the revision is promoted wholesale: `status.activeRevision` flips, and the serving route's one `backendRef` moves to it on the same pass, as for any promotion today (design 07 A6.10). If not, the candidate is held, named by no route, while the active revision keeps serving. There are no canaries and no traffic split, so the slice never sets phase `Canary`.

**Why this is the smallest gate that is still real.** It keeps three properties:

- The candidate is the real revision workload: the digest-pinned image it would serve with, its immutable material, and its injected environment (design 02 §3.2).
- Each case is a real A2A 1.0 task, on the binding the candidate's own card declares, not a bespoke echo path.
- The verdict alone decides promotion, and it is bound to the full revision digest it was earned on, not the forty-bit name (A2).

What it gives up is in the "Out of the slice" table: no runner image, no Job, no judge, no dataset artifact, and no path through the gateway.

#### The EvalSuite, in the slice

The slice's schema is a strict subset of §3's, so the full schema can add fields later without migrating a stored suite (Q8). Minimal:

```yaml
apiVersion: assayd.dev/v1alpha1
kind: EvalSuite
metadata: {name: smoke, namespace: team-a}
spec:
  dataset:
    cases:
      - {id: hello, message: "hello", expect: {contains: "hello"}}
```

Realistic, with the one Agent field the slice reads:

```yaml
apiVersion: assayd.dev/v1alpha1
kind: EvalSuite
metadata: {name: claims-regression, namespace: claims}
spec:
  dataset:
    cases:
      - id: deductible-lookup
        message: "What is the deductible on policy P-1042?"
        expect: {contains: "500"}
      - id: refuses-travel
        message: "Book me a flight to Lisbon."
        expect: {contains: "cannot help with travel"}
      - id: health
        message: "status"
        expect: {equals: "ok"}
  metrics:
    - {name: task_completion, threshold: "0.66"}
  caseTimeoutSeconds: 10
```

```yaml
# on the Agent claims/pa-reviewer, beside the rest of its spec
gates:
  - evalSuiteRef: claims-regression
```

The rules, each owed as CEL on the CRD, with a refusal that names the fix:

| Field | Rule |
|---|---|
| `dataset.cases` | 1 to 20 items. An empty list is refused at admission, because an eval with no cases would pass every revision. This moves §7's "a suite with zero cases fails closed" from a condition to admission |
| `cases[].id` | a DNS label, unique within the suite |
| `cases[].message` | 1 to 4096 characters, sent as the task's one text part |
| `cases[].expect` | exactly one of `contains` or `equals`, each 1 to 4096 characters. Compared with the answer text, case-sensitive, with no normalization |
| `metrics` | optional, at most one entry, and its `name` must be `task_completion`. Absent means `threshold: "1"`: every case must pass |
| `metrics[].threshold` | a decimal string greater than 0 and at most 1, with at most three decimal places. It is a string because controller-gen refuses float fields without `allowDangerousTypes`, and §3's `threshold: 0.85` is a float (below, "What design 16 says that the slice cannot keep"). A threshold of 0 would pass every revision, so it is refused |
| `caseTimeoutSeconds` | 1 to 10, default 10 (Q1 says why 10) |
| every other §3 field: `runner`, `target`, `dataset.goldenSetRef`, `dataset.fromSessions`, `gate`, `budget`, and any other metric | absent from the slice's schema. kubectl's default server-side field validation refuses an unknown field. A client that does not ask for that validation has the field pruned in silence. That is the API server's behaviour for every CRD, not a rule this slice adds |

On the Agent, `spec.gates` holds at most one `GateRef` (Q4). `evalSuiteRef` resolves in the Agent's own namespace. The suite carries no `target` in the slice, so the Agent's reference is the only binding between the two (Q8).

The **suite digest** is the SHA-256 of the canonical JSON of the suite's `spec`, with defaults applied, object keys sorted, and cases sorted by `id`. A change to any case's message or expectation, to the threshold, or to the timeout changes it. Reordering cases does not.

#### How a run works

1. **When a run starts.** A run starts for the candidate only when every one of these holds: the Agent declares a gate; the EvalSuite CRD is installed (design 02 §3.3's discovery); the candidate is available; no I1 hold stands on it (design 03 §3.3.1); and `status.cards[]` records a validated card for the candidate's full digest. The card is a precondition for two reasons. It names the binding and path to use. It also guarantees design 03's `Lock` a recorded card digest for any revision the gate promotes (below, "How it composes with what exists").
2. **Where the request goes.** To the candidate revision's Service, by its ClusterIP and `spec.runtime.port`, as the card fetch does (`internal/controller/card.go`). The path is the path of the first `supportedInterfaces[]` entry whose `protocolBinding` is `HTTP+JSON` and whose `protocolVersion` is `1.0`, followed by `message:send`. The host and port in that entry's URL are ignored: a card is written by the agent, and the operator sends nothing to an address the agent chose. With no such entry, no run starts, reason `EvalBindingUnsupported`. Q1 decides whether the request goes through the gateway, or from a Job, instead.
3. **What is sent.** One `SendMessageRequest` per case, as `docs/research/a2a-v1.0-card-and-transport-2026-09.md` §2 pins it and `test/responder` accepts it: the header `A2A-Version: 1.0`; a `message` with a fresh `messageId`, `role: ROLE_USER`, and one text part carrying `cases[].message`; and no `contextId`, so every case opens a fresh context. No credential is sent. The candidate is on no route, and design 03 puts `-auth` on the route, so nothing between the operator and the candidate asks for one.
4. **What a case's outcome is.** The answer text is every text part of `task.artifacts[].parts[]`, or of `message.parts[]` when the response is a message, joined by one space, in order. The body is read up to 1 MiB.

   | Answer | Outcome | Reason |
   |---|---|---|
   | `200`; `task.status.state` is `TASK_STATE_COMPLETED`, or the response is a message; the answer text satisfies `expect` | pass | — |
   | the same, and the answer text does not satisfy `expect` | fail | `Mismatch` |
   | `200`, and the task is in any other state | fail | `TaskNotCompleted` |
   | any other status, a body over 1 MiB, or a body that is not a `SendMessageResponse` | fail | `ProtocolError` |
   | a refused connection, or no answer within `caseTimeoutSeconds` | fail | `Unreachable` |

   An unreachable candidate fails its case like any other. §9 calls a candidate that crashes under eval "the gate working", and the slice agrees. The cost is that one network blip fails a good revision, until its owner re-runs it the way Q3 decides.
5. **Pacing.** One case per reconcile pass. After each case, its outcome is written to `status.eval.cases[]`, and the pass requeues at once. A case never holds a worker longer than `caseTimeoutSeconds`. After an operator restart, the run resumes at the first case with no recorded outcome for the same pair of revision digest and suite digest. A case whose outcome was recorded is never sent again. A crash between sending a case and recording its outcome sends that case again. One case per pass, because the operator sets no `MaxConcurrentReconciles`, so controller-runtime's default of one worker applies, and every other Agent waits while a case runs. At 20 cases of 10 s, one candidate can occupy that worker for 200 s in total, 10 s at a time.
6. **The verdict.** After the last case, `score` is passed ÷ total. The run passes when `passed × 1000 ≥ t × total`, where `t` is the threshold in thousandths. That is integer arithmetic, so no rounding decides a boundary. The verdict is recorded with both digests, and is final for that pair (Q3).

#### Promotion

The rollout step keeps its order (`internal/controller/agent_controller.go`): the not-ready branches, then the I1 hold, then the gate, then promotion. The slice changes only what the gate branch asks. Today, with a gate declared and the EvalSuite CRD installed, that branch holds with no exit, reason `GateControllerUnimplemented`. In the slice it lets the candidate promote only when all three of these are true, read in the same pass:

- `status.eval.verdict` is `Passed`;
- `status.eval.revisionDigest` equals the desired revision's full digest (A2);
- `status.eval.suiteDigest` equals the digest of the EvalSuite as read in this pass.

If any is false, the candidate holds. The branch keeps its guard, `status.activeRevisionDigest != desiredDigest`. So adding a gate, or editing the suite, never evaluates the revision already serving. `gates` is on design 02's policy surface, so adding one mints nothing, and the serving revision is evaluated only if a later spec mints it again.

**A failed verdict holds the candidate. It does not delete it (Q2).** The phase is `Held`, and no route names the candidate, until a new generation supersedes it (design 02 §3.3) or the suite's digest changes. Design 02 §3.3's rollout step 5, and §4 step 5 here, say "candidate deleted". The slice cannot keep that: the desired spec still names the revision, so the next pass would re-create it and evaluate it again (below, "What design 16 says that the slice cannot keep").

#### Status, conditions and phase

`status.eval` (design 02's `EvalStatus`) gains three fields, owed to design 02:

| Field | Meaning |
|---|---|
| `suite`, `revision`, `revisionDigest`, `at` | as today |
| `score` | as today, rendered `passed/total` |
| `suiteDigest` | new: the suite digest the run used |
| `verdict` | new: `Running`, `Passed` or `Failed` |
| `cases[]` | new: at most 20 `{id, outcome, reason}`, in suite order |

Only the operator writes it, through the status subresource. A principal who can write `agents/status` can forge a pass, exactly as it can forge `activeRevisionDigest`. Design 02 §3.3's argument that "`status` is the authority" covers both.

For a revision the gate evaluates:

| State | `GatesPassed` | Phase | `Progressing` | `Ready`: an earlier revision serving / none serving |
|---|---|---|---|---|
| no gate declared | not set; `GatesSkipped=True/NoGatesDeclared`, as today | as today | as today | as today |
| EvalSuite CRD absent | not set; `GatesSkipped=True/EvalSuiteCRDAbsent`, as today | as today | as today | as today |
| the suite does not exist | `False/EvalSuiteNotFound` | `Held` | `True/AwaitingGates` | `True/Available` / `False/AwaitingGates` |
| the suite cannot be read (an API error) | `False/EvalSuiteUnreadable` | `Held` | `True/AwaitingGates` | `True/Available` / `False/AwaitingGates` |
| an I1 hold stands | `False/EvalHeldForAuth` | `Held` | `True/AuthInputUncompilable`, as today | `True/Available` / cannot arise: I1 applies only to a served Agent |
| no card recorded for the candidate's digest | `False/EvalAwaitingCard` | `Held` | `True/AwaitingGates` | `True/Available` / `False/AwaitingGates` |
| no `HTTP+JSON` 1.0 interface on the card | `False/EvalBindingUnsupported` | `Held` | `True/AwaitingGates` | `True/Available` / `False/AwaitingGates` |
| running | `False/EvalRunning`, naming k of N | `Held` | `True/AwaitingGates` | `True/Available` / `False/AwaitingGates` |
| failed | `False/EvalFailed`, naming each failed case and its reason, the score against the threshold, and both digests | `Held` | `True/AwaitingGates` | `True/Available` / `False/EvalFailed` |
| passed | `True/EvalPassed`, naming both digests and the score | `Ready`, after promotion in the same pass | `False/RolloutComplete` | `True/Available` |
| a pinned rollback (Q5) | unchanged; `GatesBypassed=True/PinnedRollback` | as the pin resolves today | as today | as today |

`GatesPassed` stays owned and sticky (`internal/controller/conditions.go`). After a promotion it keeps describing the revision `status.eval` names, until the next candidate resets it. The slice retires the reason `GateControllerUnimplemented` and keeps `GateDetectionUnwired`. On Q5's recommended answer, `GatesBypassed` gets its first reason. Every reason above belongs in the one named vocabulary list, with its test (write-spec), and that is owed. A failed gate while an earlier revision serves leaves `Ready=True`. The slice adds no alert of its own.

#### How it composes with what exists

- **`candidateRevision` and `activeRevision`.** As above. The candidate is recorded in `status.candidateRevision` and `candidateRevisionDigest` while it is evaluated and while it is held. At most one is in flight, as today.
- **A new Agent, and design 03's `Create`.** `Create` is entered only once a revision serves: while `status.activeRevision` is empty, the operator enters no `-auth` transaction and collects the Agent's routes (`internal/controller/authtxn.go`). So a new gated Agent is evaluated first, promoted on a pass, and only then prepared, given its policy, probed and published. A new Agent whose first revision fails has no route at all. Its time to first answer is the run plus `Create`.
- **The I1 promotion hold.** It comes before the gate, and no case is sent while it stands. Such a candidate cannot be promoted while the hold stands. The edit that fixes its `-auth` mints a new revision anyway, because `expose` is on the behaviour surface. So a run would be spent on a revision that can never serve.
- **A promotion during a `Lock` (J2, K2, and a missing policy).** The gate adds no ordering. A `Lock` works on the serving route, which names the active revision. A held candidate is named by no route, so neither `ProbingBefore` nor `ProbingAfter` can reach it. J2's edit mints a revision, because `expose.a2a.auth` is behaviour surface, and on a gated Agent that revision is evaluated before it can be promoted. The `Lock` does not wait for the verdict, and the verdict does not wait for the `Lock`. A pass that lands while a `Lock` is short of `Served` is a promotion during a `Lock`, which design 03 §3.3.3 already handles: the promotion clears `beforeObserved` and `beforeRevision`, and a later `401` is credited only on the card digest `status.cards[]` records for the new revision. The slice's card precondition guarantees that digest is recorded for every revision the gate promotes.
- **A Gateway-level auth policy (design 03 A75).** Eval requests do not cross the gateway, so a Gateway-level policy changes no case's outcome. A new Agent that passes its gate and then meets a Gateway-level hold is promoted and stays unpublished, reason `GatewayAuthPolicy`, as today.
- **`GatesSkipped` and `GatesPassed`.** As the table says. Only the gate branch's question changes. The discovery rule stays: a failed discovery reads as "installed", so a gated Agent holds rather than promoting ungated.
- **Supersession.** A new generation abandons the run. Outcomes recorded for the old digest are dropped with it, and the new candidate starts at its first case. The old candidate is appended to `status.supersededCandidates`, as today.
- **A pinned rollback (`spec.release.targetRevisionDigest`).** Q5 decides. As this draft reads today's code, a pinned rollback holds with no exit whenever a gate is declared and the EvalSuite CRD is installed, because the gate branch has no exception for a pin. The I1 hold has one (`authHoldsPromotion`). That reading is unmeasured.
- **Card drift on a held candidate.** No new run. A card change without a spec change mints no revision (design 02 §3.4), and the verdict covers the revision's behaviour.

#### What the verdict proves, and what it does not

- **It proves** that the candidate container, at the digest named, answered the suite's cases as recorded, from inside the cluster, at the time recorded.
- **It does not prove the production path.** Eval requests do not cross the gateway, the route's `-auth`, or any policy on the route. §1's promise, that a revision passes its EvalSuite "through the production path", is not kept by the slice (Q1).
- **The candidate's own egress is real.** Every revision gets `ASSAYD_GATEWAY_URL` (design 02 §3.2). A tool call the candidate makes while answering a case goes through the gateway to a real tool, with real side effects, and any LLM spend is real. Nothing marks eval traffic, and no receipt records it. A suite must be written as if every case runs in production, because its tool calls do.
- **Whoever can write the suite can make the gate pass.** Nothing reserves `EvalSuite` writes in a namespace. So anyone with write on EvalSuites in the Agent's namespace, usually the Agent's own editor, can pass a revision by weakening its suite. The slice's gate catches a regression an honest owner did not intend. It is not separation of duties (Q6).
- **The operator becomes an A2A client.** It sends only to a Service it owns, at an address it reads from the API server, and never to an address from a card or a suite. The message text is written by the suite's author, and it reaches that author's own agent. `test/responder` records that the A2A client is design 08's, as `assayd invoke`. The slice adds a second one, inside the operator, limited to one method (Q1).

#### In and out

| In the slice | Where |
|---|---|
| The `EvalSuite` kind, in the subset above: `dataset.cases`, `metrics` with `task_completion` only, `caseTimeoutSeconds`, and their admission rules | above |
| `spec.gates` with at most one entry, resolved in the Agent's namespace | above, Q4 |
| A run in the operator: one A2A 1.0 `SendMessage` per case, over `HTTP+JSON`, to the candidate's Service, one case per pass, resumable | above, Q1 |
| The mechanical case outcome, the suite digest, the integer threshold rule, and a verdict final for its pair of digests | above, Q3 |
| Promotion only on a `Passed` verdict whose revision digest and suite digest match this pass; wholesale, onto the route's one `backendRef` | above |
| A failed candidate held, not deleted | above, Q2 |
| `status.eval`'s three new fields; the `GatesPassed` reasons in the table; `GatesBypassed=PinnedRollback` if Q5 says so | above |
| A watch on EvalSuites that re-queues every Agent whose gate names the changed suite | owed with the slice |

| Out of the slice: specified, not approved | Why it is not in the slice |
|---|---|
| Canary steps, weighted shifts, SLO-judged progression (D1's canary half), phase `Canary` | ADR-0030 step 4 is blue/green before canaries. The serving route has one `backendRef` |
| The `evalrunner/v1` slot, DeepEval, Inspect, `byo`, runner Jobs and images, the runner trust bar, `evalreport/v1` and the contracts ledger (§8) | the slice's runner is in the operator (Q1) |
| The eval Job's own SVID, the candidate header route, its compiled temporary grant, and the eval principal's posture (§4 step 2, §6) | design 06's identity is not built, and design 03 §1.1 puts the candidate header route outside its approved slice |
| Evaluation through the gateway | the same; Q1 |
| The dataset builder (§5): `goldenSetRef`, ontology-derived seeds, session-derived cases, content-addressed dataset artifacts, `DatasetReady` | cases are inline. Design 12 is a hypothesis under ADR-0030, and design 17 is not built |
| `tool_correctness`, `faithfulness_to_kg`, `cost_regression`, judged metrics and `custom_geval`, `gate.minScore` and weighted means, non-blocking suites (§6, §7) | each needs receipts (design 04), the knowledge graph (designs 01, 13), a spend aggregate (design 04) or a judge, and the slice has none of them. With one metric, §7's mean is that metric |
| The judge, `JudgeUnavailable`, the eval `budget` and its exhaustion (§9) | the slice calls no judge and spends nothing itself. The candidate's own spend under eval is real, and the gate does not limit it (above) |
| The `EvalRun` kind, Postgres result rows, report artifacts, PR annotations, the `eval.completed` event, eval receipts | the verdict lives in Agent status |
| Nightly runs against the active revision (§4 step 6) | no trend consumer exists (design 20) |
| `EvalSuite.spec.target` | the Agent's reference is the only binding (Q8) |
| Model targets (A1) | design 25 is a hypothesis under ADR-0030 |
| "At least one gate in prod", and `GatesBypassed=DevProfile` | nothing enforces either (design 02 §5), and the slice does not add them |
| More than one gate per Agent | Q4 |
| §6's flake policy: infra-failed cases retried once | Q3 |
| Re-evaluating the serving revision when a gate is added or its suite changes | `gates` is policy surface (above, "Promotion") |

**What the slice depends on. Every item is owed.**

- **Design 02**:
  - `status.eval`'s `suiteDigest`, `verdict` and `cases[]`;
  - `spec.gates` capped at one entry by CEL, with a refusal that names the fix (Q4);
  - the gate branch of the rollout step answering from the verdict, as "Promotion" states, in place of today's hold with no exit;
  - a rule for a pinned rollback under a declared gate (Q5);
  - the `GateRef` field comment in `api/v1alpha1/agent_types.go`, which says "At least one is required in prod". Nothing enforces that bound, as design 02 §5 already says;
  - §3.3's rollout steps 3 to 5, which describe a candidate-only route, canary steps and deletion on a fail, amended to name this slice. Design 02 is itself not approved, and this design does not edit it.
- **Design 07**:
  - the chart carrying the `EvalSuite` CRD (Q7);
  - RBAC for `get, list, watch` on `evalsuites`;
  - the e2e harness applying the CRD, because `helm upgrade` never updates `crds/`;
  - two env knobs on `test/responder`, one that changes its answer's prefix and one echoed into the task's metadata, so the e2e can tell a failing revision and two passing ones apart.
- **This design**: the `EvalSuite` Go type and its generated CRD; the operator's A2A client, with an injection seam like `CardClient` and `AuthProbe` so envtest can stand in for the candidate; the watch above; and the tests below.
- **Design 03**: nothing new. The slice relies on §3.3.3's rule for a promotion during a `Lock`, and on the I1 hold, both built by design 03's slice PRs (A71, A72).

#### What design 16 says that the slice cannot keep

- **"Candidate deleted" on a fail** (§4 step 5; design 02 §3.3, rollout step 5) cannot be implemented as written. The reconcile is level-triggered, and the desired spec still names the deleted revision, so the next pass re-creates the workload and runs the eval again. The slice holds the candidate instead (Q2).
- **D2's "rerun-shopping structurally impossible"** does not follow from binding digests. Binding makes a rerun attributable, not impossible: §6's own flake policy retries infra-failed cases, and nothing in this design stops a second run on the same digests. ADR-0006 names retry-until-pass as a bias amplifier, and says handling it is unbuilt. The slice makes the claim true only by making a verdict final for its pair (Q3).
- **"Through the production path"** (§1) and **"the gate controller never proxies eval traffic"** (§4 step 2) both assume the candidate header route and a per-run identity, and neither exists. On Q1's recommended answer, the operator is itself the source of eval traffic.
- **Two references join a suite and an Agent**: `EvalSuite.spec.target.agentRef` (§3), and the Agent's `spec.gates[].evalSuiteRef` (design 02). Nothing says what happens when they disagree (Q8).
- **`threshold: 0.85` is a float** (§3), and controller-gen refuses float fields unless `allowDangerousTypes` is set. The slice uses a decimal string.
- **§10's first sentence** still gives the eval Job "the candidate-route credential (gate-controller SVID)". That is the identity model r1's MAJOR withdrew, and r2 asked for the sentence to be struck before ADR-0024 (`reviews/16-review.md`, R2-a). It is still there. It is outside the slice, and is recorded here rather than edited.

#### Questions for the human

Each has a recommended answer. This draft decides none of them.

- **Q1 — Where do eval requests go?**
  - (a) From the operator itself, to the candidate's Service, one case per pass. No new pod, image or RBAC. It reuses the path the card fetch already uses, which `TestTheGatewayIsTheOnlyWayIn`'s hand-authored ingress rule already admits. The costs: no gateway on the path; the operator becomes an A2A client; and a case occupies the one reconcile worker for up to 10 s, which a slow LLM-backed agent may need more than.
  - (b) From a Job in the run namespace, to the candidate's Service. The shared worker is not held, and cases can run longer. The costs: a runner image to build, sign and pin; Job RBAC; a pod in a namespace where the operator is today the only pod author (design 02 §3.5's `pods-by` label); and a new ingress allow rule.
  - (c) Through the gateway, on design 03's candidate header route. This keeps §1's promise. The costs: design 03 work outside its approved slice, and an eval principal, which needs design 06 or a key the operator holds.
  - **Recommended: (a).** It is the smallest real gate, and the verdict states what it does not prove. (c) is the next step, once design 03's candidate route is approved.
- **Q2 — What does a failed verdict do to the candidate?** (a) Hold it at zero traffic, with its workload kept for inspection, until a new generation or a new suite digest. (b) Scale it to zero replicas, and keep its material. (c) Delete it, as §4 step 5 says.
  - **Recommended: (a).** (c) loops, as above. (b) saves capacity, but a candidate at zero replicas cannot be inspected, and it needs a scale rule the rollout step does not have.
- **Q3 — Is a verdict final for its pair of revision digest and suite digest?** (a) Final: a re-run needs a new suite digest or a new revision. (b) §6's flake policy: a case that failed as `Unreachable` is retried once, and only `Mismatch` or `TaskNotCompleted` is final. (c) An explicit re-run field on the Agent.
  - **Recommended: (a)**, because ADR-0006 names retry-until-pass as the one property that distinguishes this gate, and says it is unbuilt. The known cost: a network blip fails a good revision, and its owner must touch the suite to re-run it. (b) is the natural next amendment, once non-determinism is handled on purpose.
- **Q4 — One gate per Agent: how is it enforced?** (a) CEL caps `spec.gates` at one entry at admission. (b) Accept several, and hold any Agent with more than one, reason `MultipleGatesUnsupported`.
  - **Recommended: (a)**, because an error at apply time names the fix. An Agent stored with several gates before the cap keeps them under CRD validation ratcheting, and holds as in (b).
- **Q5 — Is a pinned rollback evaluated?** (a) No. It promotes, with `GatesBypassed=True`, reason `PinnedRollback`, and no run. (b) Yes, like any candidate, which delays a rollback during an incident by a whole run. (c) Only a revision with a recorded pass is served. That needs `status.revisions[]`, which is not on the CRD (ADR-0031 Amendment 1).
  - **Recommended: (a)**, because instant rollback is ADR-0019's reason for retaining revisions, and ADR-0031 Amendment 1 already records the gate-evidence check as unbuildable. By this draft's reading, today's code holds the rollback with no gate controller to release it, so a pinned rollback under a declared gate never promotes.
- **Q6 — Who may write an EvalSuite?** (a) Anyone with RBAC on EvalSuites in the namespace, and the slice states that its gate is a regression gate, not separation of duties. (b) An admission policy reserves EvalSuite writes to named principals, as `assayd-api-keys` reserves key sets.
  - **Recommended: (a).** A reservation with no reviewer workflow behind it only moves the same trust somewhere else.
- **Q7 — Does the chart install the EvalSuite CRD?** Its presence is design 02's switch for whether declared gates hold. Helm installs everything under `crds/` unconditionally and never templates it, so a chart value cannot switch it. (a) Ship it in `crds/`, so every install enforces a declared gate. (b) Ship it as a separate manifest that an administrator applies to turn the tier on. (c) Ship it under `templates/`, behind a value, where `helm uninstall` deletes the CRD and every EvalSuite with it.
  - **Recommended: (a).** An Agent that declares a gate has asked for one. The cost: every Agent that already declares a gate stops promoting ungated on upgrade. P1 is unshipped, so no install carries that cost yet.
- **Q8 — The suite's schema and binding.** (a) The strict subset of §3 above, with a decimal-string `threshold`, no `target`, and the Agent's `evalSuiteRef` as the only binding. (b) A flat schema for the slice alone (`cases`, `minPassed`), migrated when the full schema lands. (c) Keep `target` too, and refuse a run when it does not name the Agent.
  - **Recommended: (a).** (b) buys a simpler first schema at the price of a migration. (c) adds a second reference that can only disagree with the first.

#### Tests the slice owes

Every case carries a mutation that must kill it. A mutation that does not compile is INVALID, not KILLED (AGENTS.md rules 1 and 3).

| Layer | Case | The mutation that must kill it |
|---|---|---|
| unit | the outcome table, one case per row, against a recorded response of each shape | accept any task state as completed, so a `TASK_STATE_FAILED` case passes |
| unit | the threshold rule at its boundary: 2 of 3 passes `"0.666"` and fails `"0.667"` | `≥` changed to `>`; or a float comparison in place of the integer one |
| unit | the suite digest: stable under key order and case order; changed by each of a message, an expectation, the threshold and the timeout | leave one of those four out of the digest |
| unit | the request: the `A2A-Version` header, `ROLE_USER`, a fresh `messageId` per case, no `contextId`, and a path taken from the card with a host and port that are not | send to the card URL's host |
| envtest | a gated Agent's second revision holds, `EvalRunning`, while the first serves; it promotes only after every case has an outcome and the verdict is `Passed` | make the gate branch ignore the verdict, so the candidate promotes before its last case |
| envtest | a failing case holds the candidate with `EvalFailed` naming it; the active revision is unchanged over later passes; the stub sees each case exactly once | count `Unreachable` as a pass; or re-run on every pass, which the stub's call count catches |
| envtest | a `Passed` verdict recorded for one member of `internal/revision/collision_test.go`'s pinned collision pair does not promote the other (§3's owed case, A2) | compare `status.eval.revision`, the name, in place of the digest |
| envtest | a verdict for a suite digest that no longer matches the suite promotes nothing, and a new run starts | drop the suite-digest check |
| envtest | a spec edit after the first of three cases abandons the run, and the new candidate starts at its first case | key recorded outcomes on the case id alone |
| envtest | a candidate under an I1 hold is sent no case, and reads `EvalHeldForAuth` | move the gate branch before the I1 hold |
| envtest | a new gated Agent has no `HTTPRoute` while its first revision runs or fails, and gets `Create`'s prepared route only after a pass | have the emitter follow the desired revision in place of the active one |
| envtest | an operator restarted after two of three cases sends only the third | keep outcomes in memory in place of status |
| envtest | no case is sent before the candidate's card is recorded; a card with no `HTTP+JSON` 1.0 interface reads `EvalBindingUnsupported` | drop the card precondition |
| envtest | admission refuses two gates, an empty case list, duplicate case ids, a threshold of `"0"`, and two expectations on one case | delete each CEL rule in turn; each deletion must fail its own case, and only its own |
| envtest | J2 on a gated Agent: the `Lock` reaches `Served` on the active revision while the gate holds the edit's revision; a pass during the `Lock` promotes, and the `401` is credited on the new revision's recorded card digest | make the `Lock` wait for the gate; or skip clearing `beforeObserved` on a gate's promotion |
| e2e, k3d | `TestARevisionIsEvaluatedBeforeItTakesTraffic`: a gated API-key Agent passes and is published. An edit to the responder's answer prefix mints a revision that fails and holds, and for 60 s every answer through the gateway is the first revision's. An edit that restores the prefix and changes the metadata marker passes, and once the route names it, 20 consecutive answers through the gateway carry the new marker | make the gate branch always pass, which lets the failing revision's prefix through the gateway; or make the matcher always pass |

## 2. Doctrine & charter gates

- **Plane**: slow (gate controller = part of the agent-operator; the *runner* is a provider slot, fast-plane images; datasets/metrics/thresholds are data).
- **Pods**: 0 standing — eval runs are **Jobs**; nightly runs are CronJobs. **Stateful deps**: Postgres (eval results — substrate per ADR-0002), object store (dataset + report artifacts). ✓
- **Primitives**: Resource (CRDs), Artifact (datasets/reports as content-addressed OCI artifacts), Agent (the runner drives real A2A tasks), Event (`eval.completed`). ✓ **EvalRunner joins the charter's reserved-slot list as shipped** (architecture §15 erratum, like IdentityProvider before it).

## 3. The EvalSuite CRD

```yaml
kind: EvalSuite
metadata: {name: pa-regression, namespace: claims}
spec:
  runner: {type: deepeval}                # evalrunner/v1 slot: deepeval | inspect | byo image
  target: {agentRef: pa-reviewer}         # what this suite gates (or modelRef, P5)
  dataset:
    goldenSetRef: pa-golden-v3            # curated + ontology-derived (§5); content-addressed artifact
    fromSessions: {sample: 200, since: 7d, strata: {by: hop_outcome}}   # design 17 sampler
  metrics:
    - {name: task_completion, threshold: 0.85}
    - {name: tool_correctness, threshold: 0.9}
    - {name: faithfulness_to_kg}          # mechanical: citations verified via kg.cite (§6)
    - {name: cost_regression, maxIncrease: 15%}
    - {name: custom_geval, judge: {model: gpt-x@pinned, promptRef: pa-judge-v2}}   # LLM-judge, pinned
  gate: {minScore: 0.85, blocking: true}
  budget: {tokensPerRun: 500k, usdPerRun: 5}    # the eval principal's own budget (ADR-0028 pattern)
status:
  lastRun: {report: oci://…@sha256:…, datasetDigest: …, judgeDigest: …, scores: {…}, verdict: pass}
  conditions: [DatasetReady, LastRunPassed]
```

**A revision reference is `{name, digest}` everywhere in this design (A2).** Design 02 A37 separated the two: the ten-character `name` is what a workload is called and what `kubectl get agents` prints, and the **full digest** is the identity. Forty bits is not a security boundary against an attacker-controlled projection — a chosen collision between a safe and a malicious spec was found in **1.2 seconds** — and this design is where that distinction earns its keep, because a verdict here is what grants production traffic.

Comparing a bare `revision` name would let a colliding projection be promoted on the verdict a different one earned, and that decision is made **before any workload is inspected**, so design 02's workload-level collision guard never sees it. The typed reference is therefore carried through every step below: the EvalRun input, the report's bound digests, `GatesPassed`, the candidate route's admitted principal, the events, and the receipt attribution. A step that carries only the name is a step where the gate can be spent on the wrong revision.

**Owed:** the `EvalSuite`/`EvalRun` CRDs do not exist yet, so this is a contract for the types when they are written rather than a description of a schema. Design 02 already carries the digest in `status.evalStatus.revisionDigest` and `status.cards[].revisionDigest`, which is the half that could be built without them (design 02 A50).

## 4. The gate flow (with design 02's rollout machine)

1. Candidate reaches `Held` (02 §3.3) → gate controller sees `gates:` on the Agent CR → `DatasetReady?` (build/refresh if stale, §5) → launches the **eval Job**. The controller reads `status.candidateRevisionDigest`, not `status.candidateRevision`, and the EvalRun records both (A2).
2. The Job drives **real A2A tasks through the gateway** at the candidate header-route. **Identity model (r1 f1)**: SVIDs are per-pod attested identities, never lent — the eval Job pod gets **its own SVID** via the existing label template (`spiffe://…/eval/<suite>/<run>`), and the compiler adds that principal to the candidate route's **admitted set at Job launch, removing it at Job end** — a narrow, temporary, compiled grant (the design 01 A2 pattern applied to routes; recorded as a design 03 row + ADR-0028 note). The gate controller never proxies eval traffic. **Runner trust bar** (runners are third-party pack images): signed image required (18), and the eval principal's compiled reachability is exactly {candidate route, pinned KG version, judge LLM egress} — fail-closed; the budget bounds the blast radius. Eval traffic exercises the identical path as prod, and every eval task produces **receipts** (§6).
3. Runner writes the structured report (per-case results + scores) → report artifact (content-addressed) + Postgres rows + CR status; verdict per §7.
4. **pass** → controller signals the operator, naming the **digest** it evaluated: the operator promotes only if that digest still matches its own candidate, so a verdict cannot be spent on a revision that changed underneath it (A2). Weight shift begins (canary steps). **Canary progression is judged on golden signals (SLOs), not re-evaluation** — the eval gate runs once, pre-canary; live-traffic degradation is the drift/SLO machinery's job (10/20). Stated to kill scope creep.
5. **fail** → candidate deleted (02), report linked in status + PR annotation; `GatesPassed=False` with the failing metrics named.
6. **Nightly**: CronJob runs the full suite against the *active* revision → trend rows (drift feed, 20); never gates.

## 5. The dataset builder

Datasets are **content-addressed artifacts** — a report always names the exact dataset digest it ran against (reproducibility is a first-class property):

- **Ontology-derived seeds**: design 12 §3.8 — one case per probe, `via` + matchers carried verbatim. These are *floor* cases (the graph's known answers, asked through the agent).
- **Curated cases**: `golden/` files in the agent's repo (the design-09 golden-task test is the seed), promoted via `assayd eval promote <session>`.
- **Session-derived cases** (17): stratified sample of recorded production tasks (inputs replayed; recorded outcomes as reference). Sampling is pinned at build time — the dataset artifact embeds the chosen sessions, so reruns are stable.
- Builder runs as part of the eval Job's first step (no standing pod); rebuilt when refs change or `since:` windows roll; `DatasetReady=False` names what's missing (e.g. zero sessions matching strata — loud, not empty-pass).

## 6. Metric semantics (the honest column)

| Metric | Source | Nature |
|---|---|---|
| `task_completion` | runner judgment per case (DeepEval agentic metric / matchers for ontology-derived cases) | judged (LLM for open cases, mechanical for `via`-matched) |
| `tool_correctness` | **receipts of the eval run** — normative v1 variant: **target-sequence match** (works at any capture level); argument-aware matching available when the eval posture captures bodies | mechanical |
| `faithfulness_to_kg` | every citation in the agent's answer resolved via `kg.cite` on the *pinned graph version*; uncited claims counted | mechanical |
| `cost_regression` | eval-run receipts vs the active revision's **trailing 7-day median per-task cost** (from the design-04 aggregate; window/statistic stated in the report) (r1 f4) | mechanical |
| `custom_geval` etc. | LLM-judge with **pinned model + versioned prompt**; judge config digest in the report | judged |

**Eval-principal posture is pinned independently of the agent's** (r1 f2): capture `full` on eval-principal traffic (reports store at receipt capture rules) and KG scope mirroring the target agent's — compiled with the temporary grant, so `kg.cite` resolves and argument-aware checks are possible. Judge calls go through the gateway under the eval principal (receipted, budgeted). **Flake policy**: infra-failed cases retry ×1; semantically-failed cases never retry (that's the signal). Agent stochasticity is bounded by convention (templates default eval-mode temperature) but not assumed: the verdict rule (§7) tolerates case-level noise via the threshold, not reruns-until-green — rerun-shopping is structurally impossible because the report binds (dataset digest, judge digest, candidate revision).

## 7. Verdict rule

`gate.minScore` applies to the **weighted mean of metric scores** (equal weights v1); each metric's own `threshold` is a hard floor — one floor breach fails regardless of the mean. Blocking suites hold the rollout (02); non-blocking suites annotate only. A suite with zero cases **fails closed** (`DatasetReady=False` — an empty eval must never pass an agent).

## 8. The `evalrunner/v1` slot

A runner is a Job image implementing: input `(dataset artifact ref, target endpoint + credentials, metric config, budget)` → output `(report JSON schema evalreport/v1: per-case {id, input_digest, outcome, per-metric scores, receipts refs}, summary scores)`. DeepEval adapter first (its `evaluate()` API maps directly); Inspect AI second; `byo` = any image honoring the contract. **`evalrunner/v1` and `evalreport/v1` join the `assayd-contracts` ledger** (design 07 §4 — r1 f3; the ledger carries every socket contract, and doctor/upgrade checks cover them). Conformance: a fixture dataset + mock agent where expected scores are known; parity across runners on the mechanical metrics (judged metrics are runner-specific by nature — documented, not hidden).

## 9. Failure modes

| Failure | Behavior |
|---|---|
| Runner Job crash | Job restart; per-case idempotency by case id (re-running completed cases is safe — results content-addressed) |
| Judge model unavailable | Judged metrics `error`, mechanical metrics complete; verdict **fails closed** with `JudgeUnavailable` named |
| Eval budget exhausted | Run halts; verdict fail-closed (`budget`); operator alert — an eval that can't afford to finish never passes anyone |
| Dataset refs missing/stale | `DatasetReady=False`, rollout stays Held, loudly |
| Candidate crashes under eval | Receipts show it; verdict fail with the crash cases named — that's the gate working |
| Nightly run fails | Trend gap marked; never affects serving |

## 10. Security

Eval Jobs hold: the candidate-route credential (gate-controller SVID), read access to the pinned KG version, the eval budget. They never hold prod user tokens (synthetic principals: `eval:<suite>@<candidate>` in receipts). Reports may embed prompts/outputs → stored at the receipts capture level, same redaction rules (ADR-0014).

## 11. Testing

Fixture agent (scripted A2A responder) + fixture suite: pass/fail/floor-breach/empty-dataset/judge-down/budget-exhausted paths; report reproducibility (same digests ⇒ same verdict); e2e on k3d: the flagship demo — `assayd deploy` streams `HELD → eval 0.89 ✓ → canary 10% → 100%`, then a deliberately-broken revision fails with the report in the PR annotation.

## 12. Decisions for async review

- **D1 — The eval gate runs once, pre-canary; canary progression is SLO-judged** (no re-eval mid-rollout) — scope pinned.
- **D2 — Datasets and reports are content-addressed artifacts**; a verdict always names (dataset, judge, revision) digests — rerun-shopping structurally impossible.
- **D3 — Fail-closed everywhere**: empty dataset, judge down, budget out ⇒ the candidate does not pass.
- **D4 — EvalRunner is a provider slot** (`evalrunner/v1`), DeepEval first, conformance with cross-runner parity on mechanical metrics.

## 13. Resulting ADRs

ADR-0024 (P3) after critique PASS.

## 14. Amendments

- **A1 (2026-08-20, from design 25 r1 f1)**: the **model metric family** joins the catalog (metrics are target-kind-scoped): `golden_quality` (mechanical on `via`-shaped sets, judged otherwise), `latency_p95` / `throughput` under a declared load profile, `refusal_safety_rate`, `cost_regression_per_1k_tokens` (never per-task). Model datasets = curated sets + optionally prompts *extracted* from agent sessions; raw A2A session cases are agent-only. The flow, fail-closed rules, and contracts are shared unchanged.
- **A2 (2026-09-01, from design 02 A50)**: every revision reference in this design is `{name, digest}`, and the **digest** is what decides. Design 02 A37 established that the ten-character name is a name — a chosen collision against its forty bits took 1.2 seconds — and A50 carried the digest into `evalStatus` and `cards[]` but could reach no further, because this design owns the component that actually grants production traffic. A gate controller comparing `evalStatus.revision == H` can promote a colliding projection on the verdict another one earned, and it makes that decision **before any workload exists to inspect**, so design 02's workload-level guard is not in the path. The reference is carried through EvalRun input, the report's bound digests, `GatesPassed`, the candidate principal, events and receipts; promotion re-checks the digest so a verdict cannot be spent on a revision that changed underneath it. The `EvalSuite` and `EvalRun` CRDs do not exist yet, so this is the contract their types must satisfy rather than a description of a schema — **owed at implementation**, and §8 owes the case that proves a verdict for one digest cannot authorize a colliding one.
- **A3 (2026-09-14, ADR-0030 build step 4) — a first-slice draft, and the Status line corrected.** Adds §1.1, a narrow gate the human can approve on its own, drawn the way design 03 §1.1 drew design 03's. An Agent names one EvalSuite. Its candidate revision is evaluated before it takes any traffic, then promoted wholesale or held. There are no canaries and no traffic split. Each of the suite's cases is sent by the operator as one A2A 1.0 `SendMessage` to the candidate's Service, one case per reconcile pass, and compared mechanically with a `contains` or `equals` expectation. The verdict is bound to the full revision digest and a suite digest. Promotion re-reads both in the pass that promotes. §1.1 also states how the slice composes with design 03's `Create`, its `Lock`, the I1 hold and A75's Gateway-level hold. It gives the conditions, reasons and phases that result, what is in the slice and what is out, what the slice depends on (every item owed), and the tests owed, each with its mutation.
  - **Nothing is decided.** Q1–Q8 in §1.1 are the human's: where eval requests go, what a failure does to the candidate, whether a verdict is final, how one gate per Agent is enforced, whether a pinned rollback is evaluated, who may write a suite, whether the chart installs the CRD, and the suite's schema and binding. The slice is drawn on each question's recommended answer, and says so where an answer would change a rule.
  - **Six statements in this design cannot be kept at the slice's scope**, and §1.1 lists them with their reasons. A deleted failing candidate would be re-created by the next reconcile. Binding digests does not make rerun-shopping impossible. "Through the production path" needs a candidate route and an identity that do not exist. Two references join a suite and an Agent. `threshold` is a float, which controller-gen refuses. §10 still carries the identity model r1 withdrew, which r2's residual R2-a asked to strike before ADR-0024 and nobody did.
  - **The Status line said "approved".** `docs/designs/README.md` said "awaiting user approval", and under ADR-0030 the README arbitrates and a PASS is not an approval. The line now says "not approved". The text it replaced is quoted in the Status line, rather than deleted.
  - Nothing in A3 is implemented. No review file was edited.
