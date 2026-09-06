# v1.5.0 spike: interpretation review

2026-09-06. Snapshot: `d6e232879bf27908b0948b2122813e66e4ed625f`.

Scope: durable handoff of the session diagnosis requested by the human. This is not another whole-design review. Evidence: the implementation session, the committed spike report, ADR-0030, and upstream source at the two release tags. I did not rerun the cluster spike or mutation tests; cluster observations below are attributed to its author, not independently reproduced.

## MAJOR 1 — Translation validation is being mistaken for a dataplane acknowledgement

`docs/research/agentgateway-v1.5.0-spike-2026-09.md:39`, `:40`, `:52`; propagated into `docs/designs/03-policy-compiler.md:6`.

The report calls this a new distinguishing NACK signal and prescribes checking the reason. However, the controller itself produces the exact invalid-condition error during translation, and maps partial translation errors to `Accepted=True / PartiallyValid`. Both paths already exist in [v1.4.1 traffic_plugin.go](https://github.com/agentgateway/agentgateway/blob/v1.4.1/controller/pkg/agentgateway/plugins/traffic_plugin.go#L321) (condition check at line 1237) and [v1.5.0 traffic_plugin.go](https://github.com/agentgateway/agentgateway/blob/v1.5.0/controller/pkg/agentgateway/plugins/traffic_plugin.go#L322) (condition check at line 1247).

Failure scenario: a compiler treats `reason==Valid` as evidence that a proxy accepted configuration. A dataplane-only rejection need not trigger this controller-side CEL error. Simultaneous observation of `PartiallyValid` and a NACK does not establish a causal status update from the latter. Comparing a rate-limit error on one version with a CEL error on another also does not establish a release improvement.

Fix: distinguish admission, controller translation, and dataplane acceptance. Require fully valid translation, but retain the explicit limit that this is not positive dataplane acknowledgement. Run the same CEL stimulus on both tags before claiming a version change, and use a controller-valid/dataplane-invalid stimulus to investigate the acknowledgement question.

## MAJOR 2 — A failed expression is not evidence of request denial

`docs/research/agentgateway-v1.5.0-spike-2026-09.md:42`.

The fail-closed claim is supported by a log about replacing an expression with one that fails. [Expression construction](https://github.com/agentgateway/agentgateway/blob/v1.5.0/crates/agentgateway/src/cel/mod.rs#L321) describes an evaluation failure, not an HTTP denial. [Conditional policy selection](https://github.com/agentgateway/agentgateway/blob/v1.5.0/crates/agentgateway/src/store/policy.rs#L240) has a branch that skips the policy when its condition does not evaluate true.

Failure scenario: a reader equates a failed conditional expression with refusal of protected traffic, although the actual effect may instead be skipping the conditional transformation. This source inspection identifies the missing proof; it does not independently reproduce the spike's request outcome.

Fix: report expression substitution as observed, and request-level behaviour as unverified until traffic is tested. Include a healthy baseline, the malformed condition, observable backend arrival and response, and the repaired control. State what happens to the specific policy, not a blanket fail-closed guarantee.

## MAJOR 3 — Target scope does not settle the concurrency experiment

`docs/research/agentgateway-v1.5.0-spike-2026-09.md:16`, `:48`, `:56`.

The API rejection establishes that this frontend setting cannot target an HTTPRoute. It does not establish that a Gateway-wide limit cannot constrain a single Agent's concurrent traffic. A shared cap can constrain a subset without providing independent quotas or fairness. It also does not, by itself, establish any token or monetary budget bound.

Failure scenario: the project discards useful overload protection, or retains an obsolete concurrency claim, because it substitutes schema-target validation for runtime measurement.

Fix: separate configurable scope, actual counter scope, concurrency enforcement, accounting lag, and budget guarantees. Rerun the concurrent token-accounting experiment with the cap enabled and disabled; record replica count, maximum in-flight requests, per-request usage, completed usage and rejection source. Publish no numeric budget bound without all required assumptions enforced. Keeping the current no-guaranteed-budget-ceiling position is prudent; claiming this experiment proved that position is not.

## MAJOR 4 — The dependency prerequisite was declared complete without the required measurement

`docs/designs/03-policy-compiler.md:3`, `:6`; `docs/research/agentgateway-v1.5.0-spike-2026-09.md:5`.

[ADR-0030](../../decisions/0030-scope-reset-to-one-end-to-end-slice.md) explicitly ties revisiting v1.5.0 to its concurrency control and the old concurrency result. The report says that load experiment was not rerun. The changed CEL stimulus does not settle the former dataplane-only rejection question either.

Failure scenario: subsequent design work treats unresolved dependency behaviour as measured closure, propagating another false premise.

Fix: mark the revalidation partial, list the specific unresolved experiments, and remove the claim that the prerequisite is discharged. If the human wishes to narrow that prerequisite to the minimum policy slice, change the decision explicitly rather than silently treating an omitted experiment as completed.

## Verdict and next work

**REVISE these conclusions; not a stop on the whole product.** The reported absent-route and negative-burst admission observations remain useful. The report's explicit warning not to generalise the CEL result to rate limits is correct, but conflicts with its stronger summary and prescribed remedy.

Continue ADR-0030's narrow workload-materialization work, including retained-revision selection. Do not turn these four corrections into a requirement to finish all of design 03 before the first working slice. A productive team split is Claude implementing bounded slice increments and Codex independently checking their contracts and executable evidence. Findings travel as files; disagreements remain visible for the human. Coordinate file ownership and test runs; never sweep another agent's changes into a commit.
