# Independent review: design 03 A85, §8.1 items 9 and 11 (commit `7226a09`)

This is the review record for A85. It was produced on 2026-09-23 by a fresh `general-purpose` reviewer that had no context from the implementing session, reviewing commit `7226a09` (rebased onto `c4e5020` as `0ffcca0`).

It is filed verbatim, as the reviewer returned it. There is one redaction: the absolute path of the reviewer's scratch copy is replaced by `[REDACTED: local scratch path]`, because it names a local user directory.

The findings are answered in commit `790fe88` (rebased as `500068e`) and in §11 A85. MAJOR 1 was first recorded as owed §8.1 item 12. On the coordinator's instruction it was then fixed in the same PR and discharged.

---

**Review of 7226a09 (design 03 A85, §8.1 items 9 and 11). Verdict: REVISE, with 0 BLOCKER, 1 MAJOR and 3 MINOR.**

I did not modify the worktree. All runs used a scratch copy at `[REDACTED: local scratch path]/d03fx-review-copy`.

**What I verified**
- **Build and suites:** `go build`, `go vet` and `internal/controller` tests pass. The full envtest suite passes at HEAD (288 s). `make reference` produces no drift.
- **M9** (drop `&& !governanceKeptAbove(conds)`): compiles. Unit tests pass. Envtest fails `TestAHeldPolicyClaimKeepsTheReasonAboveIt`, both subtests. This matches the commit.
- **M11** (`servedRouteLead` returns `routeNamed` for the unknown-no-claim cell): compiles. Unit fails only `TestThePolicyHalfSaysOnlyWhatWasReadOfTheRoute`. Envtest fails `TestTheHedgeSaysWhatWasReadOfTheRoute`, both subtests. `TestBothHalvesOnOnePassNameTheRouteFirst` passes, so the claim that "item 7's row passes under M11" holds.
- **My own mutations:**
  - `governanceKeptAbove` accepts only `ForeignTrafficPolicy`: the `a_GatewayAuthPolicy` subtest fails, so each reason has its own falsifier.
  - `servedRouteLead` returns `routeServing` on the unknown-no-claim cell: 2 subtests of `TestBothHalvesOnOnePassNameTheRouteFirst` and 2 of item 11's row fail.
- **Scope:** item 9's change is exactly the `unless` clause the item specified. Item 11 changes only the message and leaves what is raised alone, so D7 stands. I found no design change beyond those.
- **Call order:** `reportForeign` and `reportAboveServed` run before `judgeServed` (`authtxn.go:357-374`), so `governanceKeptAbove` sees the reasons asserted on the same pass. On a transient errored pass, `reportAboveServed` still runs before `holdServedJudgement`, so the fix covers that path too.

**MAJOR 1. `routeNamed` does not mean the route reason leads `PolicyApplyIncomplete`, but the new comments and design text say it does (rule 7), and the kept hedge is false in that cell (rule 8).**
- **Where:**
  - `internal/controller/authserved.go:440-446`: "puts ServingRouteNotAccepted at the head of PolicyApplyIncomplete".
  - `authserved.go:456-458`: `routeNamed` "so PolicyApplyIncomplete leads with the route's own reading".
  - `authserved.go:483-486`: the hedge text itself.
  - `docs/designs/03-policy-compiler.md:1510`, item 11's discharge: "`routeNamed` (…, so `ServingRouteNotAccepted` leads `PolicyApplyIncomplete`)".
  - The §3.3.3 bullet at `:835`: "says only what this pass read of the route".
- **Why:** `incompleteOrder` ranks `ForeignTrafficPolicy` and `GatewayAuthPolicy` above `ServingRouteNotAccepted`. In `raiseIncomplete`, an unranked reason already on the pass (a deadline or a NACK) keeps the lead.
- **Measured on envtest (throwaway row, deleted):** a served API-key Agent with the route refused, `<agent>-auth` unattached, and a widening Gateway-level policy. This is the listener-rename incident on a cluster with a Gateway-level policy.
  - `PolicyApplyIncomplete` reads reason `GatewayAuthPolicy`, and its segments are `GatewayAuthPolicy | ServingRouteNotAccepted | AuthPolicyNotAttached`.
  - The `AuthPolicyNotAttached` segment still says "PolicyApplyIncomplete names the route's own reading first".
  - `GovernanceSkipped` (reason `GatewayAuthPolicy`) carries the same false sentence.
- **Relation to earlier text:** the wording predates this commit, and item 11's original text made the same assumption. The tables exclude outranking reasons by their preface; the new comments and discharge text do not.
- **Fix, either of:**
  - (a) Change the `routeNamed` sentence to "PolicyApplyIncomplete also names the route's own reading (ServingRouteNotAccepted)", dropping "first". Add a unit row and an envtest row with `GatewayAuthPolicy` standing. This is a message change outside item 11's cell, so the human should agree to it.
  - (b) Keep the wording. Bound the comments, §3.3.3 and §8.1 item 11 to "where nothing outranks it in `incompleteOrder`", and record the outranked cell as a new owed §8.1 item, with this measurement as its falsifier.

**MINOR 1. The new sentence for an unknown reading is misleading on a cluster whose agentgateway controller is renamed, the case the commit itself cites (rule 8).**
- **Where:** `authserved.go:487-491`, and the matching cell in the §3.3.3 table.
- **Problem:** on that cluster the route status shows `Accepted=True` and `ResolvedRefs=True` at the current generation from agentgateway, just under a `controllerName` that assayd does not match. The message says the pass "found neither … reported by agentgateway's controller". An operator who checks the route sees both conditions true and has no pointer to the real cause.
- **Related ambiguity:** "nor either reported False" does not say at which generation, so a stale `False` also fits it.
- **Fix:** name what was matched, e.g. "no status entry for the assayd Gateway from controllerName agentgateway.dev/agentgateway, the only one assayd reads, reporting both at that generation, and none reporting either False at it". Update the table cell and the unit and envtest strings to match.

**MINOR 2. Test comments still describe mutations against the removed `routeOK`, so they cannot be applied as written (rule 3).**
- **Where:** `test/envtest/authserved_test.go:464-470` ("passes routeOK=true", "on !routeOK") and `:631-633` ("Mutation, one edit: make routeOK `routeRep != reportBroken`"); `internal/controller/authserved_unit_test.go:259` ("this lead's !routeOK block").
- **Fix:** restate each mutation in terms of `servedRouteLead` and `route != routeServing`. For `:633`: "make `servedRouteLead` return `routeServing` where it returns `routeUnread`". I measured that mutation and it is still killed.

**MINOR 3. The `route` argument is dead on every `clauseUnknown` call (rule 5).**
- **Where:** `authserved.go:759` (`holdServedJudgement`) and `authserved.go:727` (the held branch in `judgeServed`).
- **Problem:** `holdServedJudgement` passes `routeUnread` even when `stored.routeRefused` is set, where the correct value would be `routeNamed`. `clauseUnknown` never reads the argument, so no test can fail on either value. This was already true of `routeOK=false`; the new three-way type makes the wrong value look deliberate.
- **Fix:** add a comment at both calls saying the held lead ignores `route`, or pass `servedRouteLead(reportUnknown, stored)` so the value is at least correct.

**No finding:**
- `docs/designs/README.md`, `internal/refgen/reasons.yaml` (the pointer at `authserved.go:879` is correct) and `docs/reference/conditions.md` are consistent.
- The D7 mention of "`if routeOK`" is historical and acceptable, since A85's own M1 description maps it to the new code.
- The §3.3.3 third-table `GovernanceSkipped` cell and the `GovernanceSkipped` bullet match the code.

**REVISE on MAJOR 1.** Option (b), correcting the claims and recording the cell as owed, stays within item 11's scope. The three MINORs are small wording and comment edits.
