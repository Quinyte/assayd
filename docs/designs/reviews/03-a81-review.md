# Design 03 (policy compiler): independent review of PR #52 and scoped critique of A81

- **Date**: 2026-09-22.
- **Reviewer**: an independent adversarial pass, following `.claude/skills/review-code` and `.claude/skills/critique-design`.
- **Target**: PR #52 (`a80-served-route-status`) at `85efb13`, base `main` `53cf138`; and amendment A81, which that PR added and which had never been critiqued.
- **Scope**: the delta `a1e62fd` → `85efb13` (two rounds of review answers), and A81 as an amendment to an approved slice's normative body.

## Verdicts

- **Code review: REQUEST CHANGES** — 0 BLOCKER, 3 MAJOR, 9 MINOR. The earlier BLOCKER is genuinely and completely fixed, reproduced both ways. The three MAJORs are new, and one is the same rule-8 shape as the defect the PR exists to fix, measured on a real cluster.
- **A81 critique: FAIL** — on new text, not on the earlier gaps. The three points the first review called incomplete are now complete, and A81's honesty about `refuseAdopt`, `authKept` and its own BLOCKER is exemplary.

## The blocker fix, reproduced both ways

Driven in envtest, reverting `internal/controller/authserved.go` to `a1e62fd` by file copy.

| arm | `a1e62fd` | `85efb13` |
|---|---|---|
| transient (`errStaleAgent`) | 872 → 1116 → 1360 …, **+244 B/pass**, saturates 32 592 B, marker count 131 | **714 B, flat over 200 passes** |
| non-transient (`policyUpdateForbidden`) | 1109 → 1588 → 2067 …, **+479 B/pass**, 29 370 B at pass 60 | **949 B, flat over 60 passes** |

The freeze is real. At `a1e62fd`, after saturation at errored pass 129, a spec edit produced `Agent.assayd.dev "probefrozen" is invalid: [status.conditions[3].message: Too long: may not be more than 32768 bytes, …]`, so `observedGeneration`, phase, `Ready`, `Degraded` and the card conditions all stop moving.

Nothing else appends to a held message: `raiseIncomplete` and `appendGovernance` compose only from the pass's own condition set, `carried()` de-duplicates by suffix on stable input, and `noteGovernance` appends to a message rebuilt every pass. The both-halves-hold path was measured flat at `a1e62fd` (1356 B over 12 passes), so the growth was confined to the two errored arms as A81 says. Rebuilding from the fallback loses nothing §3.3.3 requires: it asks for `LastTransitionTime` and `ObservedGeneration`, and both are carried and asserted.

## Mutations re-derived

Each is one compiling edit; the tree was restored by copy and verified byte-identical afterwards.

| mutation | owning row | result |
|---|---|---|
| `holdIncomplete` appends to the stored message | 19 (m) | KILLED, both arms |
| delete `storedClaims(agent).writeTo(status)` from `refuseAdopt` | 19 (n) | KILLED |
| drop the two comparisons from `authKept` | 19 (o) | KILLED |
| gate on the post-step slot alone | 19 (p) | KILLED |
| drop `controllerName` from `routeReport` | unit, two rows | KILLED |
| drop a reason from `carriedReason` | `TestSeedAndWithdrawAreTheSameSet` | KILLED |
| delete `holdGovernance`'s whole-restore branch | 19 (k), fourth half | KILLED |
| generation-gate the `StatusSummary` ancestor | unit row | KILLED |
| re-add `stored.writeTo(status)` in `holdServedJudgement` | — | SURVIVED, expected: confirms the rule-5 deletion was a true no-op |
| make `heldMessage`'s cut loop a no-op | — | SURVIVED (MINOR 1) |
| drop the two claim carries at the J2 abandonment and `enterLock` | — | SURVIVED (MINOR 2) |
| rename `AgentgatewayControllerName` | — | three route-half rows fail with `Ready=True` (MAJOR 1) |

## What was measured on a cluster

On k3d with agentgateway 1.5.0, today: the GatewayClass's `spec.controllerName` and a served route's `status.parents[].controllerName` both read `agentgateway.dev/agentgateway`, at the route's current generation — so the design's controller-name measurement is true.

A80's own incident was then driven end to end: renaming the Gateway's `http` listener on a served Agent moved it to phase `Degraded`, `Ready=False`, `Degraded=True`, `PolicyApplyIncomplete=True`, with `status.auth.routeRefused: true`; restoring the listener healed it and cleared the claim. **The amendment's headline claim holds on a real cluster.**

## MAJOR 1 — the `controllerName` comparison is called fail-safe and is fail-open

§3.3.3 calls a cluster whose controller is named otherwise "fail-safe, and a standing report would then never clear". The second clause is vacuous: the comparison gates **raising** as well as clearing, so no report can stand there at all. Measured — one edit to the constant, and the route-half rows fail with `Ready=True` on a route reported `Accepted=False/NoMatchingParent`. That is A80's defect restored whole, with nothing raised; the earlier ref-only match did report it.

It is a documented configuration: agentgateway 1.5.0's chart exposes `controllerName` with a comment about running multiple controllers, while `GatewayConfig` has no such field and the chart's `gateway:` map exposes none.

Graded MAJOR rather than BLOCKER deliberately: the constant was already a load-bearing assumption before this PR, assayd has never claimed to support a renamed controller, and the default install was measured fine. The fix is to state the truth; better, to distinguish "a parent entry exists for our `parentRef` but none from our controller" from "no entry at all", which does not flap on promotions.

## MAJOR 2 — in the primary incident the headline conditions name the wrong cause

Measured on k3d, a served API-key Agent with the listener renamed: phase `Degraded`, claims `{policyUnattached: true, routeRefused: true}`, and `Ready`, `Degraded`, `GovernanceSkipped` and `PolicyApplyIncomplete` all reading `AuthPolicyNotAttached`, opening with "this Agent's route is accepted and SERVING while the assayd Gateway reports that it does not attach the `<agent>-auth` policy, so the route may be answering with no credential required" — while the same pass recorded `Accepted=False`, reason `NoMatchingParent`, visible only in one appended tail.

The cause is structural, not contrived: renaming the listener detaches the route, and agentgateway then writes the synthetic ancestor on the policy — captured verbatim, `ancestorRef {group: agentgateway.dev, name: StatusSummary}`, `Attached=False`, reason `Pending`, "Policy is not attached: HTTPRoute … is not attached to any Gateway". So both halves fire together in A80's own incident, and the policy reason outranks the route reason.

Two design statements fall with it: "which are the only states in which the appended ranks are exercised at all" (false — they stand beside each other with neither of the other two present), and "the condition's message names every reason that stands either way, so it buys nothing" (true of `PolicyApplyIncomplete`, false of `Ready` and `Degraded`, which carry one failure).

## MAJOR 3 — two administrator-facing places still say the defect is unreported

`charts/assayd/values.yaml` and `docs/install.md` both still tell an administrator that an Agent served before the Gateway changed keeps saying `Ready` and nothing reports it. Measured false today. These are the two places an administrator reads about this exact scenario.

## Minors

1. `heldMessage`'s cut loop is dead code — every fallback is freshly built and can contain no marker — and the design attributes the bound to it; the bound comes from rebuilding from a fresh fallback, which row (m) pins.
2. The two claim carries at the J2 abandonment and at `enterLock` over a refused `Adopt` are unpinned; deleting both leaves the suite green.
3. `policyReport` makes no `controllerName` comparison while `routeReport` now does, and the asymmetry is unstated — the same clearing hazard, on the fail-open half.
4. "It does not heal" is false as written: one pass whose `-auth` step completes rebuilds the message and the write lands; the freeze persists only while the step keeps erroring.
5. §3.3.3 cites case 19 (l) for the bound; (m) is the row that pins it.
6. Case 19 (n)'s "the whole suite stays green without this row" is false — (o) also fails under that mutation, because it depends on the carry to reach a writing pass.
7. Three stale "twelve rows" counts in text this PR edited.
8. The rule-7 correction about reading the Gateway's listeners reaches four of six places.
9. `TestA80SReasonsRankLast` is a change-detector: its expectation is a literal copy of the list, so it cannot catch a wrong order and will fail on any legitimate insertion.

Also noted: no `docs/research/` note recorded the controller-name measurement, though every other measurement in this document cites a file.

## A81 as a design record

The three points the first review called incomplete are complete. Corrections 1 and 2 are stated against A80's own claims rather than around them, and correction 2 admits the hold still degrades silently for the class it matters most for. The BLOCKER is recorded with its measured numbers, its mechanism, its pin, and the fact that it was A81's own. "What it does not touch" is stated well.

It fails on new text: §3.3.3's "fail-safe" is false of the code and was added without critique; "the only states in which the appended ranks are exercised at all" is falsified by behaviour A81 itself built, in A80's own incident, and was not corrected as two other A80 statements were; and the round's corrections are recorded incompletely (minors 4, 5, 6 and 8).

One reflection for the record. A81's "no e2e was added" is defensible on its stated reasoning — but MAJOR 2 fell out of driving the amendment's own incident on a cluster in about ten minutes, and it is the sharpest finding of the round. The `make conformance-cluster` case §8.1 already owes would have caught it.
