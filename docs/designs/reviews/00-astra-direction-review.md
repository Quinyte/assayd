# plume direction and process review

Date: 2026-09-05. Repository examined: `b86af9974dd8efa1dc9a773b8977848674a77ab1`. The worktree was clean when this review began. This is a project assessment, not another design-02 defect inventory. The recommendations below are proposed decisions; this review does not amend any approved contract.

**Verdict: REVISE the scope, sequencing, and process. Keep the architectural core. Do not continue building the full 27-design platform on the present plan.**

There is real engineering here: the operator runs, its tests catch meaningful failures, and the revision-material work has closed real holes. But the project is optimizing the specification of a platform before demonstrating its distinctive benefit. The review process finds defects and also manufactures substantial follow-up work. Its current unit of progress is too often an amendment that survives another reading, rather than a user task that survives a deployment, failure, and rollback.

**Yes: this project should build more and document less.** Specifically, stop treating the entire speculative platform as a prerequisite for implementing one useful, safely bounded path through it. That is a change in scope and evidence, not permission to ship known bypasses.

## 1. Is the architecture sound?

### A sound core inside an oversized product

Kubernetes resources for desired state, a reconciler for lifecycle, standard protocols for interoperability, and agentgateway for traffic are sensible choices **for teams already operating Kubernetes**. An operator can add value by keeping a released agent, its configuration, its knowledge version, and its evaluation evidence associated across failures. Those are concrete operational problems. Kubernetes is not automatically the right starting point for a team whose problem is simply running an agent.

The strongest architectural sentence is still “**The reconcile loop is the product**” (`docs/architecture.md:43`). The weakest is the assumption that everything surrounding that loop remains a thin binding. Design 01 explicitly owns “the query surface … the admin surface … the ontology document schema, version semantics, the conformance suite” (`docs/designs/01-knowledgegraph-provider-contract.md:11`). Design 13 adds an “adapter-side deterministic recipe engine” (`docs/designs/13-graphiti-adapter.md`, §3.3). These are substantial product semantics. Putting them over MCP does not make their implementation, compatibility, or security inexpensive.

The scope then expands to a workflow runtime, an App composition layer, a training/serving operator, a pack ecosystem, authorization integration, tenant provisioning across clusters, and compliance reporting. This is several products sharing infrastructure. The documents themselves name the work: design 21 creates “`workflow-operator` … + `workflow-runtime`”; design 25 owns “**train → package → eval-gate → serve**”; design 26 fans out “across every layer”; design 27 adds “receipt hash-chaining and the compliance reporting surface” (each design's §§1–2). Calling their incremental standing-pod count zero does not eliminate the engineering or operational cost.

The complete architecture is therefore **not yet a sound commitment**. Its central mechanisms are plausible, but the claimed guarantees and supported combinations exceed what the project has established. A much smaller system built from these mechanisms is worth testing.

### The September 2026 ecosystem invalidates the current positioning

I checked primary sources, including release metadata and tagged code, rather than accepting the architecture's competitor table.

| Claim or alternative | Evidence checked now | Consequence for plume |
|---|---|---|
| kagent is confined to its own declarative runtime | kagent **v0.10.0 was released on 2026-09-04**. Its tagged `AgentSpec` has a BYO arm; the comment says it deploys a user-provided image serving A2A. | Framework neutrality and A2A containers are shared capabilities, not a moat. |
| Kubernetes rollout gating is an unoccupied category | Flagger documents pre-rollout hooks before canary traffic. Argo Rollouts documents a preview Service and pre-promotion analysis. | Reuse or adapt mature rollout mechanics; distinguish the evaluation content and operational experience. |
| Agent infrastructure and evaluation are mostly unassembled | AWS AgentCore documents framework-independent runtime capabilities, identity, gateways, observability, and evaluation services. | Customers also have a managed alternative. Self-hosting and portability need to earn their operational cost. |
| agentgateway v1.4.1 is the present ecosystem ceiling | **v1.5.0 was released on 2026-08-27**. Its release notes describe SPIFFE Workload API support, changed policy merging, and standalone API-key budgets; its tagged Kubernetes API includes `maxConcurrentRequests`. | Reassess the dependency before building workaround machinery. Keep release pinning, but do not freeze product strategy around an old limitation. |

Sources: [kagent v0.10.0 release](https://github.com/kagent-dev/kagent/releases/tag/v0.10.0), [tagged BYO API](https://github.com/kagent-dev/kagent/blob/v0.10.0/go/api/v1alpha2/agent_types.go#L58), [Flagger webhooks](https://docs.flagger.app/main/usage/webhooks), [Argo blue/green](https://argo-rollouts.readthedocs.io/en/stable/features/bluegreen/), [AgentCore overview](https://docs.aws.amazon.com/bedrock-agentcore/latest/devguide/what-is-bedrock-agentcore.html), [agentgateway v1.5.0 release](https://github.com/agentgateway/agentgateway/releases/tag/v1.5.0), [tagged concurrency field](https://github.com/agentgateway/agentgateway/blob/v1.5.0/controller/api/v1alpha1/agentgateway/agentgateway_policy_types.go#L804). GitHub's releases API supplied the release dates; search results were stale enough to show older releases first.

These checks establish capabilities and prior art, not comparative security or performance. I did not run kagent or AgentCore in this review, and I do not assert either implements plume's exact revision guarantees. In particular, agentgateway's new budgets are documented for **standalone deployments with hybrid storage**; that does not establish a Kubernetes policy replacement or a strict spend ceiling. Nor does v1.5.0 establish that the NACK/convergence problem is solved. Test the specific contract before adopting it.

The repository already knows its novelty claim is wrong. Architecture §16 says “**nobody ships it as a k8s primitive — 12–18-month window**” (`docs/architecture.md:405`). ADR-0006 explicitly retracts that claim and says “**Not a differentiator**” (`docs/decisions/0006-eval-as-admission.md:4-6`). This is consequential beyond documentation consistency: the roadmap is selling urgency around a competitive window its own decision record rejected.

I would position the experiment as **reproducible, evidence-backed releases of knowledge-dependent agents on infrastructure the customer controls**. That is a testable proposition. “An agent I can trust with my business” is too broad to be an engineering acceptance criterion. A kagent-managed agent should be a prospective integration target, not a straw-man competitor defined by a capability it already has.

### What to keep and what to cut

| Keep in the next product slice | Cut from the delivery commitment until a demonstrated use requires it |
|---|---|
| One Agent lifecycle, full revision identity, protected configuration material, real rollback | General workflow interpretation and event-trigger orchestration (21), App composition/client generation (23), ModelHub/training/serving (25) |
| A small agentgateway binding for the exact supported route/auth/tool cases | A universal compiler covering every resource, provider arm, filter source, tenant mode, fallback, and compliance concern |
| One knowledge provider, immutable versions, independently checked domain tasks | Multiple graph backends, the complete ingestion platform, ontology proposal UX, five pattern packs, and a marketplace/installer (parts of 11–14 and 18–19) |
| One evaluation runner and durable release evidence | Multiple runner adapters, full production-session replay, automatic dataset building, and nightly machinery before the first gate works |
| Measured network isolation and one machine-identity path | Hard tenancy/vCluster federation and provisioning (26), multiple IdP profiles, broad delegated-user flows, enterprise compliance packaging (27) |
| Honest operational status, minimal traces, tests on an actual cluster | Automatic drift remediation/fallback (20), a large CLI wizard framework, complete dashboard and signal packs |

“Cut” means remove these as prerequisites and supported promises, leaving the research archived. It does not mean delete implemented security controls to get a demo working. The run namespace and immutable material already exist; use and repair them instead of opening another design cycle to replace them.

Retain Postgres/NATS as preferred integrations where they solve a demonstrated requirement. Do not require both, plus an IdP and observability stack, before the first task can complete. The doctrine should constrain total operations, not reward accounting categories. Architecture §17's own listed components sum to **8–9**, depending on gateway replicas, while it prints “≈ **8 pods**” (`docs/architecture.md:417`). The present chart test measures **2/8**, entirely the operator; none of the future subcharts is present. That passing test says nothing about the complete stack's eventual footprint. Count required controllers, node agents, storage, memory, and recovery procedures when the actual installation exists.

### Narrow the promise itself

A revision digest identifies declared material; a trustworthy execution record must establish that the evaluation actually used it. Neither proves that an arbitrary container will always behave identically: upstream services, model implementations, concurrency, resource pressure, clocks, and user input remain variable. Extending the digest until it purports to freeze the world is an endless project.

The same distinction applies to knowledge and evals. Architecture says “**New vertical = new `KnowledgeGraph` CR, everything else reused**” (`docs/architecture.md:130`). That is a hypothesis to test on a domain, not an established property. Design 16 calls citation resolution a mechanical `faithfulness_to_kg` metric (`docs/designs/16-evalsuite.md:72`); a citation that resolves can still fail to support the answer. Its “**same digests ⇒ same verdict**” test statement (`:103`) can hold for fixed mechanical fixtures, not generally for a stochastic live agent. And merely binding three digests to a report (`:76`, `:108`) does not prevent repeatedly executing the same inputs and choosing a favorable report; attempt creation and verdict selection must enforce that rule.

These are reasons to move the evaluation experiment forward. Use a human-reviewed holdout set independent of the ontology and extraction prompts. Measure failure rejection, false rejection, reproducibility limits, and recovery time against an ordinary agent plus existing rollout tooling. If plume cannot improve those outcomes or make them materially easier to operate, the remaining platform does not justify itself.

## 2. Is the process working, or manufacturing work?

**Hypothesis (b) best describes the current direction: the amendment/review loop is generating enough new obligations and errors to obstruct convergence. Hypothesis (a) explains some valuable work within it.** I cannot infer a numeric defect-generation rate from blocker counts: scopes change, findings overlap, and some findings are later corrected. The evidence supports the qualitative diagnosis, not a fabricated rate.

The rigorous part is real. The recorded gateway spike found **100 successful concurrent requests consuming 100,000 tokens against a 1,000-token budget** on one replica (`docs/research/agentgateway-v1.4.1-spike.md:115-140`). Its NACK experiment found that a rejected tightening retained old configuration despite reassuring conditions (§2.8). Revision content, full digests, collision refusal, protected material, and admission behavior now have substantial code and tests. During this review a fresh k3d run passed the material-protection and namespace-label tests. Abandoning adversarial review would throw away the process's strongest source of corrective evidence.

But it is not enough to say every defect found proves the process is rigorous. Several pieces of evidence point the other way.

**The project promotes a state it cannot substantiate.** `AGENTS.md:7` and `CLAUDE.md:5` both say “**All 27 designs are approved and critique-passed**.” Design 02's own header says “**it is not approved and must not be cited as such**”; design 03 says “**RE-OPENED, do not implement**.” The design index agrees with those exclusions. These are the first instructions given to new contributors, so the process starts by teaching false confidence and then requires a long review to undo it. Approval has become a stale label rather than a bounded decision about a particular revision and dependency set.

**The written verification algorithm puts the expensive guessing before the measurement.** `.claude/skills/verify-change/SKILL.md:7` says “**Stop at the first stage that fails**.” Its steps 3 and 4 are independent and cross-family critique; step 5 is “**Spike every load-bearing claim**” (`:17-21`). A design that repeatedly fails review can therefore keep rewriting without reaching the experiment that would settle its premise. The same file says “Measure before asserting” later. The numbered procedure should implement that principle: dependency experiment first, integration evidence next, review of the resulting change after that.

**Corrections are often replaced by new theories about unbuilt components.** Design 03's header records three rounds returning “**6, then 5, then 5 blockers**,” each finding the adjacent defect (`docs/designs/03-policy-compiler.md:3`). Its input ownership still required a correction from a discovered catalogue to `Connector.spec.tool.toolAllowlist` (`:102`). Opening design 11 §3 shows the field directly. This was not an inscrutable distributed-systems fact; it was an unchecked interface. A compiler input type actually populated by a caller would have forced the question much earlier.

**The reviewers are part of the failure mode.** The spike records that the first Codex review's minimum constraint was true on unreleased `main` and false at the pinned tag (`docs/research/agentgateway-v1.4.1-spike.md:26`). The scoped B3 review says the original universal-snapshot prescription was “**overbroad**” (`docs/designs/reviews/03-codex-review-b3.md`, §“Copying credentials”). The latest consolidation inventory initially reported 23 dead anchors; its verification addendum says “**All of them resolve**” and attributes false reports to the checker's regex (`docs/designs/reviews/03-consolidation-inventory.md:1351-1361`). An independent reviewer is a useful error detector, not a source of infallible architecture.

**Tests can faithfully enforce a wrong decision.** The preceding consolidation review's mutation ledger records that changing port/resources/loop classifications to revision-minting was killed by tests that pinned the existing projection. That establishes consistency between the test inventory and code; it does not establish that the semantic classification was safe. Its material-condition mutation similarly failed because the test expected the wrong condition. More tests and more mutation checks do not replace choosing the right property.

**The product-level feedback loop is missing.** Architecture's P1 demo is “**two agents from different SDKs collaborate, fully traced**” (`docs/architecture.md:427`). The e2e implementation explicitly says “**the agent contract (an A2A card) is not exercised here**” (`test/e2e/e2e_test.go:159-161`). This is an honest infrastructure test. It is not evidence for the demo, much less for safer business decisions. The project has spent 62 amendments specifying an operator before giving that operator an ordinary Service-backed task path.

The revision of the docs gate illustrates the distinction. Its tests are useful regression tripwires for listed phrases. They do not prove the absence of every false guarantee. The previous review deleted the “not enforced” NetworkPolicy row and the gate survived; this run also passes while the canonical architecture retains the novelty claim its ADR retracts. Stop spending repeated review cycles trying to turn a phrase filter into a semantic proof of the corpus.

### What to stop, and what to use instead

1. **Stop requiring the whole 27-design dependency web to pass before one slice can be built.** Define the supported slice and its threat model, reject unsupported requests, and review that executable boundary. An unimplemented enterprise integration should block claiming enterprise support; it should not block a single-team release experiment that does not use it.
2. **Stop treating every reviewer prescription as an implementation order.** Record the counterexample, reproduce or falsify it, then choose among fixes against the product's costs. Preserve the independent finding, but append corrections when evidence refutes it. The agent protocol's ban on conversational persuasion is useful; it must not become a ban on correcting a reviewer through reproducible evidence.
3. **Stop expanding normative prose in five places for one rule.** Give each interface one owner, preferably an actual type/schema plus producer-consumer test. Update the short explanatory text in the same change. Consumers link to that owner instead of restating a parallel schema. Keep amendment history as history, not a second instruction stream.
4. **Stop using a new condition, registry, or “owed” note as closure.** An unresolved producer is a blocked capability. A regression test must exercise the producer and consumer together, or the capability stays explicitly unsupported.
5. **Move the spike ahead of the architecture built on it.** Read the pinned artifact, run the minimal positive and negative controls, then specify the supported behavior. Keep the existing conformance suite; add cases when they protect the slice. Do not invent exhaustive future registries to demonstrate exhaustiveness over features that do not exist.
6. **Use a worktree per agent now.** The protocol itself calls that “**the durable fix**” (`docs/agent-protocol.md`, §“Never git add -A”). Explicit staging is necessary but cannot keep tests from observing a peer's in-flight edit. The recorded accidental commit of a live mutation is enough evidence; another procedural warning is not the remedy.

After two failed passes on the same contract, require a decision: run an experiment, reduce scope, or redesign the interface. Do not automatically request round three on another prose patch. Track unresolved **root causes**, newly working user scenarios, and integration regressions. A falling count of review findings is neither necessary nor sufficient for a useful product.

I disagree with the previous review's final instruction if read as a project plan: “Resolve the 13 blockers … and run another independent pass” was an assessment of the complete design under its declared scope. It should not now mean “finish every feature implicated by those blockers before building anything.” Close a finding by implementing the necessary contract **or by withdrawing the unsupported promise and excluding that capability**. Do not withdraw the underlying failure scenario merely to obtain a PASS.

## 3. Is the design-to-code ratio defensible?

I counted tracked files at the review snapshot before adding this file. Counts are physical lines, including comments and blank lines; nonblank counts are shown separately. No generated HTML, images, archives, or vendored CRD tarball inflated the prose count. The documentation subcategories overlap the all-documents row and must not be added to it.

| Category | Files | Physical lines | Nonblank lines |
|---|---:|---:|---:|
| All Markdown under `docs/` | 141 | 20,600 | 15,077 |
| The 27 primary design documents | 27 | 4,384 | 3,046 |
| Review archive under `docs/designs/reviews/` | 53 | 12,837 | 9,402 |
| ADRs | 29 | 247 | 219 |
| Handwritten production Go under `api/`, `internal/`, `cmd/` | 11 | 4,978 | 4,657 |
| Go `_test.go` files under `api/`, `internal/`, `cmd/`, `test/` | 32 | 10,419 | 9,694 |
| Non-test Go helpers under `test/` | 2 | 54 | 53 |
| Handwritten chart files | 9 | 491 | 469 |
| Generated Go, CRD/RBAC manifests, and chart copies | 5 | 3,038 | 2,957 |
| `hack/` and workflow files | 4 | 521 | 460 |

Production Go excludes `_test.go` and `zz_generated` files. The chart count excludes its generated CRD and RBAC copies. Root instructions and `.claude/` skills are excluded from `docs/`; this makes the prose estimate conservative. These are repository volume measurements, not a claim that comments are executable code or that lines measure effort.

The important ratios are:

- Primary design / production Go: **0.88:1**.
- All `docs/` Markdown / production Go: **4.14:1**.
- All `docs/` Markdown / production Go plus test-file lines: **1.34:1**.
- Test-file lines / production Go: **2.09:1**.
- Reviews constitute **62.3%** of all Markdown lines under `docs/`, and are **2.93 times** the length of the 27 designs combined.

Those figures do **not** justify saying there is no implementation or that documentation inherently dwarfs engineering. The test investment is appropriate for a security-sensitive controller. The design/code ratio alone is defensible.

**The scope-to-evidence ratio is not.** There is one implemented application CRD (`Agent`, plus its list type), one command (`cmd/operator`), and controller/revision packages. The chart describes itself accurately: “**today it installs only the agent-operator, because that is the only component that exists**” (`charts/plume/Chart.yaml:4-7`). Searches find no Agent Service constructor, policy compiler implementation, runtime `PLUME_GATEWAY_URL` injection, or rollback request API. There are 27 designs claiming a complete platform around that one operating subsystem.

The repository also makes the concentration visible: `internal/controller/runnamespace.go` is 978 lines, `agent_controller.go` 1,409, and the Agent API 670. Much of the implemented effort is now protecting and administering the platform's own lifecycle state. That work is valuable, but it has yet to carry the task whose release it is meant to protect.

History since 2026-08-20 contains **167 commits**: **99 documentation-only**, **34 touching handwritten production Go**, **48 touching tests**, and **45 touching the review archive**. The last three categories overlap. That is approximately sixteen days of repository history, not evidence of years of failure, and commits do not measure hours. Nevertheless, combined with the repeated non-converging fixes and absent task path, it supports reallocating work toward integration now.

Some documents are already load-bearing: the revision classification is consumed by tests, the condition list is compared with API constants, and the dependency research has produced executable conformance checks. Much of the rest describes obligations that no producer or consumer can yet exercise. Preserve the former. Demote the latter from “approved specification” to scoped hypotheses/backlog, and stop paying to keep every hypothetical combination synchronized.

## 4. The shortest path to an end-to-end system

**I would not follow “close all 13 blockers, consolidate all of design 03, then implement all of design 03.”** It makes a broad specification the gate for finding out whether a much smaller product is useful. I would obtain one explicit scope-reset decision, then build the path below. That decision should amend the architecture/build order and authorize the reduced supported contract; it is not something this review silently enacts.

Architecture already states the right priority: “**the differentiation only exists once P2/P3 ship — never polish the runtime layer at their expense**” (`docs/architecture.md:432`). Follow that sentence instead of the component-by-component schedule.

Choose the domain benchmark at the start, not after the infrastructure is finished. A read-only internal SOP assistant is a suitable first case: a reviewed policy changes between two snapshots, and human-written tasks distinguish the correct answer before and after. Run a plain-agent/retrieval baseline immediately. The later milestones integrate that same workload; they must not substitute a more convenient demo when the original one exposes a weakness.

| Order | Build or resolve | Observable completion criterion |
|---|---|---|
| 1 | A single-team, managed-container slice: one namespace model, one pinned gateway release, one supported LLM endpoint shape if needed, one machine-identity mechanism. Fix the current port/loop classifications and material-cleanup defect; take the three decisions in §5. | Supported fields have working semantics; unsupported capabilities are rejected or explicitly unavailable. The future enterprise matrix is not instantiated. |
| 2 | Complete workload materialization: revision-scoped Services, real card fetch, the small runtime environment contract, retained revision reconstruction and selection. | A real A2A responder answers through its revision Service. An update can retain R1 while R2 starts; source-config changes and restarts cannot rewrite R1. A requested rollback selects R1's retained material. |
| 3 | Integrate the smallest policy/compiler path with the real gateway, identity, and enforced network rules. First author and execute the exact resources; then encode that mapping. | One agent completes one A2A task and calls one MCP tool through the gateway. A disallowed principal/tool/destination fails while a permitted control succeeds. Candidate selection cannot be forged by a production caller. Required card/DNS paths work with isolation enabled. |
| 4 | Bring a narrow design-16 gate forward: one runner Job, one frozen curated dataset, digest-bound results, explicit attempt/retry rules, and **blue/green promotion before multi-step canaries**. | A deliberately bad candidate fails while R1 continues answering. A good candidate promotes; stale/forged results cannot promote another digest. Restart the controller during the transition. Roll back after a failed release without rebuilding from changed source Secrets. |
| 5 | Add one real domain: a read-only, versioned knowledge source and the minimal producing resource contract needed to resolve it. Use a curated snapshot before building general extraction/ingestion. | A reviewed v1→v2 knowledge change changes expected answers; an intentionally bad version is refused; rollback restores the previous evidence and behavior on the benchmark. Independent holdout tasks test the domain value. |
| 6 | Exercise a second framework, preferably including a kagent BYO agent, and compare against kagent plus an existing delivery/eval setup. Add minimal receipt capture only to the extent needed for the release evidence. | Framework interoperability is demonstrated, and a user can explain what plume made easier: identifying tested material, rejecting a bad change, or recovering a release. Installation and recovery cost are measured. |

This is not a suggestion to mock the integration while reporting it complete. Scripted responders are appropriate for deterministic lifecycle tests; a real agent and domain task are necessary for the product experiment. A mock passing the gate does not validate the semantic evaluator.

Before adopting a custom progressive-delivery controller, run a bounded compatibility experiment with **one** mature option—Argo Rollouts or Flagger—not two parallel implementations. Their preview/pre-promotion capabilities are established; their fit with plume's retained content and this gateway must be measured. If they fit, let one component own traffic progression and let plume own release material and eval evidence. If they do not, write down the demonstrated mismatch and keep the first custom flow to blue/green. Do not let two controllers own the same route weights.

The gateway experiment must include initial publication, rejected configuration, deletion/recreation, and rollback. A Kubernetes `Accepted` condition is not proof that the dataplane changed. Narrowing to one replica makes the first experiment smaller; it does not make a bad barrier correct. Evaluate v1.5.0 against the existing pinned conformance cases before selecting it, and do not transpose a newly documented capability into a safety claim without execution.

For the first governed slice, installing the eval functionality must be part of the supported installation. An explicit local development mode may remain ungated; an absent CRD must not silently turn a supposedly governed production install into that mode. The current code openly reports “**this rollout is NOT eval-gated**” when the CRD is absent (`internal/controller/agent_controller.go:1043`). That is honest status for today's operator, not a substitute for the proposed product's gate.

Leave unspecified until a real use forces the answer: cross-cluster tenancy, generic pack composition, every cloud provider's endpoint union, automatic model fallback, full workflow/App/Model lifecycles, long-retention compliance reporting, all six SDK templates, and a universal CLI interview engine. Leave flexible implementation choices flexible. **Do not leave authorization, ownership, the released material, result identity, crash recovery, or rollback selection unspecified for the chosen slice.** Those are its core contract.

Stop criteria matter. If a real user cannot complete the narrowed task without hand-patching internal status, the slice is unfinished. If a normal kagent deployment plus an existing gate achieves the same release/recovery result with less work, prefer an integration or contribution over a second platform. If the knowledge benchmark shows no useful advantage over a simpler retrieval baseline, stop expanding the ontology platform. No amount of design approval answers those questions.

## 5. Recommendations on the three human decisions

### (a) Resources: reject `resourceFieldRef`, retain live capacity repair, narrow the guarantee

**Choose rejection for this product slice.** Reject every `runtime.env[].valueFrom.resourceFieldRef` at admission, including supported upstream variants, with a precise message. Test the generated CRD's rejection against an API server. Keep ordinary CPU/memory requests and limits live-editable and record their changes as operational events. Move `runtime.port` and the loop permission fields into the gated projection separately; neither is an ordinary capacity repair.

This deliberately chooses the narrower alternative to the previous review's default prescription. The concrete bypass is real: the selector is hashed and its resource value is not. But “gate every resource adjustment” has an immediate cost during OOM or throttling incidents. For a first product, accepting an arbitrary resource-derived env variable is less valuable than retaining a straightforward capacity-repair path.

The important qualification is that rejection **does not make resources behaviorally invisible**. A program can observe available CPU or memory without `resourceFieldRef`; throttling and OOM change task outcomes. Design 02 already acknowledges that “**an OOM-killed agent fails tasks and CPU throttling changes `taskTimeout` terminations**” (`docs/designs/02-agent-crd-operator.md:304`). Do not replace one excessive claim with “capacity cannot change behavior.” State that the gate covers named release material and capability configuration; live capacity is a documented operational exception. Monitor it with real health/task checks. Do not cite unimplemented design-20 automation as its present backstop.

If the actual customer requirement is “no deliberate input or resource-envelope change without approval,” choose gated resources instead. That is a different operating contract and sacrifices routine live repair. There is no choice that preserves both arbitrary resource-sensitive behavior and a claim that every such change was evaluated.

### (b) Rollback: add a durable desired-revision field to Agent spec

**Choose a spec field first**, conceptually `spec.release.targetRevisionDigest`, selecting a retained full digest. An unset field means follow the ordinary desired spec; a set field means remain pinned to that retained release until the user explicitly changes or clears it. This is a proposed field, not an existing API.

That matches design 08's rule that “**every verb is sugar over CRs and published contracts**” (`docs/designs/08-cli.md`, §3), works in GitOps, and makes rollback an enduring desired state rather than a transient event which reconciliation immediately reverses. A dedicated request CR is justified later if rollback needs separately delegated RBAC, approval, scheduling, or an operation history that cannot fit the Agent contract. Adding one now creates another lifecycle and another race with desired spec without removing the need to define which release should stay active.

The minimal semantics must be explicit:

- Resolve within the current Agent UID's retained set and compare the **full digest**. The short workload suffix is display/addressing only.
- Select the retained release record, workload configuration, and immutable copies. Never recompute the rollback target from current source ConfigMaps/Secrets.
- While pinned, reconcile that selected release. Do not let a candidate reconstructed from the latest spec immediately supersede the rollback. Cancel or hold the existing candidate under one documented rule; report the request generation and outcome in status.
- Treat the selection field as control intent, outside the behavior hash. Keep its target retained while selected, without granting indefinite retention to arbitrary nonexistent digests.
- Refuse missing/corrupt material and revisions lacking the required gate evidence for a governed install. A previous ungated release is only eligible in the explicitly ungated mode and must be described as such.
- Recheck non-optional current security constraints; a rollback is not authorization to restore a revoked credential or forbidden destination. Report a refusal rather than silently substituting a different target.
- Clearing the pin explicitly resumes reconciliation of current desired spec and its gate. GitOps users must put the pin in the source of truth or their reconciler will revert a manual patch.

Design 20 currently promises “**auto-rollback — only when a deploy correlates**” (`docs/designs/20-drift-controllers.md:22`). Defer that automation. It can later submit the same intent under a defined authority/conflict policy; it must not gain a second route-selection path by writing active status directly. First prove manual recovery under source drift and controller restart.

### (c) External Agents: use the same protected per-source namespace for policy resources

**When external support is implemented, allocate/retain the existing run namespace for any Agent that needs protected gateway resources, including external Agents.** External Agents contribute routes, Backends, and policies, but no managed workload or env-source snapshots. The namespace's lifetime should follow all dependent managed resources, not the existence of a Deployment.

This reuses the ownership and admission boundary already selected in ADR-0029, whose decision places “**workloads, Services, routes and copies**” in the protected namespace (`docs/decisions/0029-operator-owned-run-namespace.md`, Decision). It is simpler than inventing another policy namespace, avoids granting source-namespace editors control of governance objects, and avoids putting every customer's objects together in the operator's own namespace. Route/backend targets still need exact validation and the existing listener/admission rules; a protected namespace alone does not authenticate an external endpoint.

The producer changes belong together: the external reconcile must acquire the binding; design 03 must consume that resolved namespace; finalization and last-Agent teardown must count external resource owners; mixed managed/external Agents must share it without premature deletion. Test an external-only namespace and deletion of the last managed Agent while an external Agent remains. External OAuth identity still comes from its own verified producer, not from a fictitious Pod SVID.

**Defer external Agents from the first end-to-end slice.** Keep the architectural choice above, but do not implement it merely to make a broad document pass. A remote endpoint is independently mutable; plume cannot promise the same frozen executable-material guarantee as for a digest-pinned managed container without an additional version/attestation contract. Describe external registration as routing and observed evaluation of a remote service unless and until that stronger contract exists.

## Evidence and execution record

I read the entry instructions, architecture, design index, the complete consolidated design-02 body, relevant design-03 sections, producer documents for the claims used here, the relevant ADRs, and the review/critique history. Broader roadmap designs were sampled at their scope, ownership, and dependency sections. This is not a claim that all 27 designs have received another correctness audit.

| Check | Result | Interpretation |
|---|---|---|
| `gofmt -l api internal cmd test` | No files reported | Formatting already clean; no source rewriting needed. |
| `make -o fmt test` | PASS | The normal vet/unit/docs/offline-conformance/envtest/chart gate, with its mutating formatting target suppressed after the read-only formatting check. Envtest completed in 60.999s. |
| `CLUSTER=plume-astra-review-20260905 make e2e`, isolated temporary kubeconfig | PASS on a newly created k3d cluster | Eight substantive tests passed; the inverse “suite not run” sentinel skipped because the suite ran. E2e test execution took 49.962s, excluding provisioning/build. Tests verified the freshly built operator image. |
| `go test ./test/chart -run '^TestCorePodBudget$' -count=1 -v` | PASS: `2/8`, operator only | Current rendered footprint, not the planned stack. |
| Searches under `api/ internal/ cmd/` | No Service constructor, rollback request surface, runtime gateway-env injection, or policy compiler implementation found | Consistent with the previous review and the chart's declared scope. |
| Tracked-file line counts and Git history classification | Results in §3 | Generated copies separated; prose and test counts not presented as executable LOC. |
| Upstream releases API, tagged kagent/agentgateway code, official rollout and AgentCore documentation | Results in §1 | Current capability evidence, not a tested replacement architecture. |

No mutations were made in this review: the instruction to modify only this review file takes precedence. Historical mutation results are attributed to their recorded reviews; they are not claimed as newly reproduced here. I did not run `make verify`, which regenerates files, or the gateway cluster-conformance suite. The fresh e2e cluster and its attached test storage were removed after the run; existing clusters were not targeted. No repository file other than this review was changed, staged, or committed.

**Final decision recommendation: fund the narrowed release/evaluation/knowledge experiment; stop expanding the full platform specification. Continue only on evidence from the end-to-end path and a real domain benchmark. REVISE.**
