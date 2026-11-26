# assayd progress and delivery review

Date: 2026-09-30. Reviewer: Codex, independent of the implementation session. Snapshot: `f3f06d1acbc19b9008d0b5c952f41109379f3b2d`, the September 28 commit. This assesses product progress and delivery priorities, not every change since the previous review. Recommendations are not new approvals or amendments.

**Verdict: REVISE delivery priorities. Keep the implementation and the narrowed architecture.** assayd has moved from an operator with mostly prospective promises to a working integration prototype: real agents, routes, authentication, and MCP traffic. It has not yet become the evaluated-release product that justifies building it. The next milestone should demonstrate that product, not extend the platform or perfect every diagnostic condition.

## What has actually advanced

| Capability | Evidence and boundary |
|---|---|
| Workload and release material | Digest-pinned containers, revision Services, protected run namespaces and immutable env-source copies exist. The fresh k3d run passed workload execution, env-source deployment/deletion, and refusal of namespace-editor replacement of a revision copy. |
| Gateway routing | The operator emits the serving route. The fresh run passed an answer through that route and deletion/recreation of the route. This is substantially stronger than a test hand-authoring the route. |
| Authentication and authorization | The compiler emits API-key authentication and namespace-group authorization. The fresh run passed permitted-key, wrong-group and anonymous outcomes, a Gateway-level hold, empty-key-source degradation/recovery, and locking a previously unauthenticated Agent. These are real controls, not merely a rendered manifest. |
| MCP interoperability | The fresh run passed the direct MCP exchange through the gateway. The repository also contains the agent-completes-a-task-by-calling-a-tool test (`test/e2e/mcp_test.go:179`); its final result was not recovered in this run. The test itself authors the MCP backend and allowlist (`:196`, `:300`). |
| Rollback selection | The pin exists. `test/envtest/release_test.go:29` now explicitly promotes R2 before requesting R1, addressing the old vacuity. Eligibility from evaluation evidence remains unavailable (`internal/controller/release.go:56`, design 02 §5). This review did not independently mutation-check rollback. |
| Operational honesty | Status and recovery paths now cover several measured gateway and key-source failures. The README explicitly separates the manual capabilities from operator-enforced ones. Neither status reporting nor a successful anonymous probe establishes complete enforcement across all paths and replicas. |

I disagree with simply carrying forward the September 5 diagnosis that this is mostly an exercise in specifying unbuilt components. The scope reset produced executable integration work. The admission, material-isolation, real-traffic and transition tests are valuable and should be kept.

## Remaining delivery findings

### MAJOR 1 The approved evaluation gate is still absent

Evidence: `internal/controller/agent_controller.go:1977–2014`; `docs/designs/16-evalsuite.md:14–26`. The design says, “Nothing in it is implemented yet.” The implementation still emits `GateControllerUnimplemented`. With the EvalSuite CRD absent it skips gating; with the CRD present and a declared gate it holds rather than running an evaluation.

Concrete consequence: a user cannot demonstrate the core sequence of a failing candidate being rejected, a passing candidate earning traffic, and the verdict being bound to the released digest. Retaining immutable bytes is useful, but it is not evidence that those bytes passed an evaluation.

Specific recommendation: make the already-approved design-16 first slice the next delivery milestone. Implement its bounded schema, separate evaluation controller and digest-bound verdict, then prove failed-candidate retention and successful blue/green promotion on k3d. Do not add canaries, an LLM judge, multiple runners or production replay to this milestone. The approved slice evaluates the candidate's Service directly; do not advertise it as evaluation through production gateway policy.

This is an acknowledged missing product capability, not a newly discovered violation of the shipped core-tier contract.

### MAJOR 2 The demonstrated security boundary is still manually assembled

Evidence: `README.md:32–34`, `test/e2e/netpol_test.go:25–42`, `test/e2e/mcp_test.go:196–198` and `:299–300`. The NetworkPolicy test explicitly says it does not implement the operator materializer. The operator emits neither NetworkPolicy nor the MCP backend/tool-filter resources used by the fixture.

Concrete consequence: installing the chart with gateway authentication does not prevent another Pod from contacting the workload directly. Likewise, an agent that knows a tool server's direct address is not compelled to use the gateway allowlist. The fixture using only the gateway address proves the chosen path, not confinement to that path.

Specific recommendation: before claiming a governed or untrusted multi-user deployment, approve and implement a minimal ingress-policy binding for the supported installation, preserving the operator's card/eval access. Measure both the permitted and blocked controls on an enforcing CNI. Keep outbound/tool governance explicitly unsupported until its separate binding exists. In the meantime the eval milestone can run in a controlled single-team environment with the manually applied network rule; this finding is not a demand to finish the entire security platform first.

This boundary is already disclosed. It is a blocker to a production-governance claim, not a reason to pretend the integration progress is worthless.

### MAJOR 3 The product is still demonstrated by purpose-built fixtures

Evidence: `docs/install.md:238` instructs the user to build the repository's echo responder; `test/e2e/mcp_test.go:273–315` checks deterministic fixture artifacts and tool refusal. Those are good infrastructure tests. They do not establish useful domain performance or that a separately developed agent integrates without special accommodation.

Concrete consequence: all protocol and lifecycle tests can pass while assayd provides no demonstrated improvement in a team's ability to reject a bad release or recover a useful agent. There is no product-value measurement to tell the team when infrastructure work is sufficient.

Specific recommendation: choose the first real task and its independently reviewed cases now. After the mechanical gate works, run one independently developed agent on that task with a pinned knowledge/input snapshot, deliberately introduce a regression, and measure rejection and recovery. Start with a simple retrieval baseline; do not make a KnowledgeGraph CRD, ontology pipeline or second framework a prerequisite. Then add the second framework to test portability. This follows ADR-0030's sequence rather than restarting architectural design.

### MINOR 1 Current operational instructions still contradict implemented status reporting

Evidence: `docs/install.md:72–82` says a served Agent keeps reporting Ready after a listener rename and that nothing reports it. `internal/controller/authserved.go` and the later A80–A83 record implement the served-route judgement. Separately, `docs/designs/02-agent-crd-operator.md:447` still says card handling has no code, despite `internal/controller/card.go`.

Concrete consequence: an operator following the install guide expects the wrong incident response, while a contributor reading the design can treat implemented registration work as absent.

Specific recommendation: correct these specific live instructions against code and the existing incident tests. Do not launch another corpus-wide amendment campaign. A passing phrase/approval gate is not semantic verification of the whole document.

## Is the process better

Yes at producing evidence; not yet reliably at choosing the next deliverable. Reviews have found real faults, and the fixes now often have executable transition tests and cluster experiments. The prior review of A81 (`03-a81-review.md`) independently reproduced a growing status message that could freeze writes, and its listener experiment exposed an incorrect causal message. Those are worth fixing, not cosmetic busywork.

But implementation of the approved gate has still not started in this snapshot. Between September 14 and the snapshot, the Git history contains 34 commits: 16 with a `docs` prefix, 7 `feat`, 8 `fix`, and 3 `test`. Subject-keyword grouping finds 17 concerning auth/policy/design 03 and 6 concerning web/reference/diagrams; groups overlap. These counts are not hours spent, and they do not establish that any particular fix was avoidable. They do show what landed while the next product milestone did not.

My recommendation is to protect a gate-delivery workstream. Interrupt it for demonstrated safety defects affecting the supported path, not every newly imaginable edge case or documentation improvement. Keep one concrete acceptance scenario visible. Reviews should try to falsify that scenario and its security boundary. Changes to already-approved trade-offs remain the human's decisions; this review does not silently reverse them.

## Size and scope measurements

Counts are physical lines, including comments and blanks, from the frozen archive. Generated Go is excluded from production Go; tests are all Go files ending `_test.go`. Markdown counts cover `docs/`, not root documentation or the website's separate source. This is not a productivity score.

| Category | Files | Lines |
|---|---:|---:|
| Handwritten production Go under api, internal, cmd | 37 | 18,591 |
| Go test files | 118 | 45,080 |
| All docs Markdown | 246 | 46,241 |
| Component designs | 27 | 7,791 |
| Review Markdown | 129 | 23,913 |

The September 5 review measured 4,978 production-Go lines and 10,419 test lines. Those categories have grown about 3.7× and 4.3× respectively; the current production total includes the new reference generator as well as runtime code. The docs/production ratio fell from about 4.14 to 2.49. It is no longer accurate to say code has barely moved. Reviews still account for about 52% of docs lines, and the narrow eval slice alone occupies roughly 580 physical lines of specification before implementation. The problem is not an arbitrary document ratio; it is the unclosed release-evaluation milestone.

## Verification and limits

The shared checkout moved from `22b65c2` to `f3f06d1` during inspection. I made a frozen archive at `/tmp/assayd-status-review.hIpP7x`; subsequent assessment and tests used that copy. No shared implementation file was edited. Earlier results from the moving checkout are not counted as verification of this snapshot.

- `gofmt -l api internal cmd test`: clean.
- `make -o fmt test`: vet, unit packages, docs and hermetic conformance passed on the frozen snapshot. Envtest was still running at the last captured output. Its final result and later chart/release-workflow results were lost when the session was interrupted. **Full gate outcome unverified**, not PASS.
- `make race`: PASS on all production Go test packages, including controller, compiler, revision, API and reference generator.
- `make e2e`: a fresh isolated k3d cluster ran the newly built operator. Captured PASS results include the real gateway auth outcomes, route recreation, protected material, auth Lock, empty-key-source recovery, and the direct MCP exchange. The final suite outcome was not recovered after interruption. **Full e2e outcome unverified**, not PASS. Cleanup completion was also not recovered.
- `make verify`, the separate conformance-cluster suite, kind, website build, and a new ecosystem comparison were not run for this assessment.

Two sampled mutations were made only in the frozen copy, each compiled and produced explicit test failures:

| Mutation | Result |
|---|---|
| Change the emitted authorization action from Allow to Deny | KILLED by the golden policy and explicit policy-shape tests |
| Delete the emitted authorization block, retaining an explicit unused-value sink so it still compiles | KILLED by the same tests, including the missing match-expression assertion |

Both were restored from `/tmp/assayd-status-review-auth.go.bak`, never by Git checkout. The selected tests passed after the first restoration. After interruption, `cmp` verified the final source byte-identical to the backup; the second restoration's test completion was not recovered. This sample validates those assertions, not the entire suite or every security property.

## Recommended next checkpoint

One independently developed agent; one small suite; one demonstrably bad revision refused; one good revision promoted; retained material selected for recovery; authentication still exercised through the serving route. Establish the network boundary for the supported deployment before calling it governed. Keep all unapproved platform scope deferred.

**Bottom line: the engineering progress is real. The product's distinctive result is still ahead. Build and measure that result next.**
