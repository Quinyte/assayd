<!-- Filed verbatim from the independent critique's record of 2026-09-23, with ONE marked redaction: line 14 of the original named the literal local path prefixes the identity scan searches for, which this repository's own identity scan refuses; they are replaced by a bracketed [REDACTED: …] note. Nothing else is changed. -->

# a85c1: independent critique of design 03 A85 and review of PR #65 at 2b4c37d

Worktree `.claude/worktrees/a85-review` (detached at 2b4c37d). Mutations and probes ran in rsync copies `a85c1-copy1..4`, not in the worktree.

## Verdict
- critique-design, design 03 A85: **PASS, 4 outstanding (all MINOR)**
- review-code, PR #65 (`git diff origin/main...2b4c37d`): **PASS**, with the same 4 MINORs

## Gates (on the worktree at 2b4c37d)
- `make test`: EXIT 0. envtest `ok 331.068s`; unit, docs, conformance, chart and release all ok (`a85c1-make-test.log`)
- `make race`: EXIT 0 (`a85c1-make-race.log`)
- `make verify`: "generation is reproducible and committed", EXIT 0. `git status --porcelain` is empty afterwards.
- `make e2e` (k3d, isolated cluster `assayd-e2e-1790175037-29100`, deleted afterwards): `ok test/e2e 304.437s`, EXIT 0. All gateway e2es PASS. The two skips are the expected ones (`TestE2EWasNotRun`, and `TestTheDeclaredUngovernedTierEmitsNoRoute`).
- Scans: `git diff origin/main...HEAD` and the touched files have no hits for [REDACTED: the three local path prefixes the identity scan searches for], or case-insensitive surname/first name. Clean.
- Authorship: all 3 commits have author and committer `Quinyte Engineer <engineer@quinyte.com>`, with the Co-Authored-By and Claude-Session trailers.
- Review record `docs/designs/reviews/03-items-9-11-review.md`: the body was diffed against the reviewer's original SubagentHandback, extracted from the session transcript. The only differences are the one marked redaction (`[REDACTED: local scratch path]`) and a trailing newline. It is verbatim.

## 1. Is "what is raised is unchanged" true?
The raise, hold and clear paths were diffed between origin/main and the head:
- `raiseIncomplete`, `holdIncomplete`, `withholdInOrder`, `noteGovernance`, `claims.writeTo` and the claim store are **byte-unchanged**.
- `appendGovernance` is refactored onto `governanceKeptAbove`, with the same predicate and the same `c` from `conds.get`. The behaviour is equivalent.
- `routeOK` becomes `route := servedRouteLead(routeRep, stored)`. Every use is `route != routeServing`, which is `routeRep != reportHolding`, so it is equivalent at every gate. It gates nothing: it only chooses message text.

The behavioural differences, in full:
- (a) The message text of the `clauseUnattached` and `clauseRejected` hedged leads (items 11 and 12).
- (b) `GovernanceSkipped` on a HELD policy pass where this pass already asserted `True` with `ForeignTrafficPolicy` or `GatewayAuthPolicy`. The reason, the message (the claim is appended) and `ObservedGeneration` (restamped to the pass's generation) all change. `LastTransitionTime` is kept. This covers three sources:
  - `reportForeign`
  - `reportAboveServed`'s widening arm
  - **`reportAboveServed`'s `unlisted` arm, a transient policy-list failure, which A85 does not enumerate.** See MINOR 3.
  - On the errored path, (b) is reachable only through `reportAboveServed` on a transient error.
- `PolicyApplyIncomplete`, `Ready`, `Degraded`, the phase and the claim store are unchanged on every path. D7's premise stands.

## 2. Item 9's composition: re-measured on envtest (throwaway probe, not committed)
Probe `zz_a85c1_probe_test.go` in copy4 (`a85c1-probe2.log`). The Agent generation was moved by an image edit in the same step as the policy drift. A sleep of at least 1.2 s ran before each measured pass so that `LastTransitionTime` resets would be visible.

| Cell | Measured | Third table |
|---|---|---|
| Row 1, ACCEPTED, held | PAI gen 1→1, LTT kept; GS `AuthPolicyNotAttached` gen 1→1, LTT kept | match |
| Row 2, REFUSED, held | PAI `ServingRouteNotAccepted`, gen 1→2, LTT kept, held claim appended; GS `AuthPolicyNotAttached` gen 1→1, LTT kept, route note absent | match |
| Row 3, UNKNOWN with `routeRefused` standing | PAI `ServingRouteNotAccepted` gen 1→2, LTT kept; GS `AuthPolicyNotAttached` gen 1→1 | match |
| Row 1 + same-pass Foreign (A85's cell) | GS `ForeignTrafficPolicy`, held claim appended with heldMark, gen 1→2, LTT kept; PAI and Ready `ForeignTrafficPolicy` | match with the `unless` |
| Stored reason Foreign, foreign removed | GS `AuthPolicyNotAttached`, gen 2→3, LTT kept (the fragment-under-another-reason rule) | match |
| Foreign + GW both stand | GS and PAI reason Foreign; segments `Foreign \| GatewayAuthPolicy: … \| AuthPolicyNotAttached: held` | §5's rule |
| Row 3 + widening GW | GS and PAI `GatewayAuthPolicy`, gen restamped, LTT kept; the route note SURVIVES on GS | outside the tables' scope (preface) |
| Held + policy list failing (`unlisted`) | GS `GatewayAuthPolicy` ("could not be listed") + held claim; reverts to `AuthPolicyNotAttached` once the list succeeds | not enumerated (MINOR 3) |

Convergence: for held + Foreign and held + GW over 3 further passes, the resourceVersion and message are byte-stable (291→291, 320→320). There is no churn and no growth.

## 3. Item 12's claim
Every pass that reaches `routeNamed` either raises or holds `ServingRouteNotAccepted` into PAI:
- `reportBroken` gives `raiseIncomplete(SRNA)`.
- unknown with `stored.routeRefused` gives `holdIncomplete(SRNA)`, which carries or raises.

`judgeServed` is the last writer of PAI in `reconcileGateway`. Nothing after it on the pass sets or unsets PAI: every other `conds.set(PolicyApplyIncomplete, …)` is in `gatewayStep`, before it. No counterexample was found. The only theoretical one is the 32768-rune truncation at `agent_controller.go:2340`, which would cut the tail of PAI and leave the hedge on GS. It is not practically reachable and is not a finding.

## 4. Item 11's controllerName
`AgentgatewayControllerName = "agentgateway.dev/agentgateway"` (`authnack.go:39`) is the constant `routeReport` compares at `authserved.go:97`, and the message interpolates that same constant (`authserved.go:497`). It is true.

## 5. Mutations
Every mutation is one compiling edit. Each was restored from a backup, and the SHA-256 was checked afterwards: `8ac41438…e169` every time, RESTORE-OK × 13. None was INVALID. Each was run through `internal/controller` and the full envtest suite (~330 s).

| ID | Edit | unit | envtest | Status |
|---|---|---|---|---|
| M9 | drop `&& !governanceKeptAbove(conds)` | ok | only `TestAHeldPolicyClaimKeepsTheReasonAboveIt`, both subtests | KILLED, as recorded |
| M11 | `servedRouteLead` returns `routeNamed` for `routeUnread` | only `TestThePolicyHalfSaysOnlyWhatWasReadOfTheRoute` | only `TestTheHedgeSaysWhatWasReadOfTheRoute`, both subtests | KILLED, as recorded |
| M12 | old "names … first" sentence back | only `TestThePolicyMessageNamesWhatTheGatewaySaid` | only `TestTheRouteHedgeHoldsWhateverLeads`, both subtests | KILLED, as recorded |
| M2′ | broken policy on unknown route goes to the held branch | ok | item 7 `a_first_report…raises` + item 11 row, both subtests | KILLED, as recorded |
| M1 | wrap the policy raise in `if route == routeServing` | ok | item 10 refused subtest; `TestBothHalves…` both unknown subtests; item 11 row ×2; item 12 row ×2 | KILLED, as recorded |
| P1 | partly-valid lead hedged as non-attachment when not serving | ok | only item 10's "… on a refused route" | KILLED, as recorded |
| N1 | `servedRouteLead` ignores `stored.routeRefused` | FAIL (servedRouteLead table) | ok (survives) | killed by unit only |
| N2 | stored claim checked before `reportHolding` | FAIL (servedRouteLead table) | ok (survives) | killed by unit only |
| N3 | `governanceKeptAbove` keeps only GatewayAuthPolicy | ok | 3 rows (item 9 foreign, item 12 foreign, the raised-foreign row) | KILLED |
| N4 | holdGovernance keeps the reason above but drops the held claim | ok | item 9 row, both subtests | KILLED |
| N5 | unknown-reading sentence stops naming the controllerName | ok | ok | **SURVIVES** (MINOR 2) |
| N6 | `governanceKeptAbove` = any True reason ≠ ANA | ok | ok | survives; equivalent on reachable states (no other True reason reaches a served apikey pass with the slot empty) |
| N7 | holdServedJudgement (errored path) keeps the pre-A85 whole-restore | ok | ok | **SURVIVES** (MINOR 1) |

Note on runs: M1's first run was killed when the coordinator cleared orphaned envtest processes. It was re-run in the foreground on a restored, hash-verified copy with the result above.

## Findings

**MINOR 1: item 9's fix on the errored path is unpinned, and a falsifier now exists.**
- Where: `internal/controller/authserved.go:776` (`holdServedJudgement` → `holdGovernance`).
- The third table's errored row ("as that row") and the prior review both say the `unless` covers a transient errored pass, where `reportAboveServed` still runs. N7 reverts only that call site to the pre-A85 whole-restore, and the full suite stays green.
- §3.3.3 says cells without a measured falsifier are "recorded rather than owed". N7 is a measured falsifier, so by that rule this cell is now owed a row.
- Fix: add an envtest row that raises the claim, plants a widening Gateway-level policy, and forces a transient route-write error (as case 19 (l) does). Assert GS reads `GatewayAuthPolicy` with the held claim appended. Or record it as an owed §8.1 item that cites N7.

**MINOR 2: the controllerName naming, the substance of the prior review's MINOR 1 fix, is unpinned.**
- Where: `authserved.go:497`.
- The claim in §8.1 item 11 and the A85 entry that the sentence "names the one controllerName assayd reads" can be deleted with every test green (N5). The unit table and item 11's envtest row assert only "the route's reading at its current generation is unknown".
- Fix: add `"controllerName agentgateway.dev/agentgateway"` to the `says` list of the `routeUnread` rows in `authserved_unit_test.go`, or to `mustContain` at `test/envtest/authserved_test.go:771`.

**MINOR 3: A85 does not enumerate one behavioural change: the `unlisted` arm.**
- Where: `authtxn.go:422-425`, composed by `governanceKeptAbove`.
- On a held pass where the Gateway-level policy list fails (transient), GS now reads `GatewayAuthPolicy` with the list-failure message and the held claim appended. Before A85 it read `AuthPolicyNotAttached` and the list failure was dropped. Measured above; it reverts on the next good pass.
- This is consistent with §5's "a standing GatewayAuthPolicy keeps the reason", and it matches what a fresh raise already did, so it is arguably correct. But the A85 entry and item 9 describe only the widening and foreign shapes, and nothing pins this one.
- Fix: say so in A85 ("including W1's unlisted arm, a transient list failure, whose reason then leads GovernanceSkipped for that pass"), and optionally add a row.

**MINOR 4: the PR #65 description is stale against the head.**
- It still says item 12 is "recorded as owed and waits on the human".
- It says "Do not merge yet: the amendment number A85 is provisional".
- It says the head's e2e is "in progress".
- The head discharges item 12, and the design says the coordinator confirmed the number.
- Fix: update the PR body before it goes to the human. For reference: e2e on 2b4c37d passed here, `ok 304.437s`.

**Not findings, noted:**
- The rows of the third table for REFUSED and UNKNOWN-with-routeRefused say `GovernanceSkipped` "carried whole" with no `unless`. The tables' preface scopes Foreign and GatewayAuthPolicy out, and row 1 carries the annotation, so this is consistent if slightly uneven.
- N1 and N2 are killed only by the unit table over `servedRouteLead`. No envtest row pins the UNKNOWN-with-routeRefused or ACCEPTED-with-stale-claim hedge wording end to end. That is acceptable under rule 1: the unit test pins the function the message reads.
- §8.1 items 9, 11 and 12 and the status line say "DISCHARGED" while A85 is PENDING. That is correct for an unmerged PR, provided the PR merges only after approval.
- The prior independent review of this PR ran on 7226a09. Item 12's fix and the rebase had no independent pass until this one. This pass covers them.
