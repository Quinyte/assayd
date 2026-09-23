# Design 03 A84 — forked self-critique of the second revision

- **Date**: 2026-09-23.
- **Reviewer**: a forked `critique-design` pass run by the author of A84's second revision, in its own context. **It is a SELF-critique, not an independent one** — `AGENTS.md` rule 4 applies to it. The independent critique of the first revision is `03-a84-critique.md`.
- **Target**: branch `design-03-a84-precondition`, head `4d53669`, base `main` `425bd59` — a local commit that was never pushed: the branch was rebased onto `main` `2a8f525` before pushing, and the same change is `0ae3365` there.
- **Verdict**: **REVISE — 0 BLOCKER, 2 MAJOR, 5 MINOR.**
- **Provenance of this file**: the fork returned its report to the author, who wrote it here as returned, with local filesystem paths redacted (marked inline). Its line numbers are against `4d53669`.

---

**Verdict: `03 A84: REVISE — 0 BLOCKER, 2 MAJOR, 5 MINOR`**

I reviewed head 4d53669 and edited nothing there; `git status` is clean. All measurements ran in a copy of the tree *[path redacted]*. I restored the mutation there from a backup, and `cmp` shows it identical to head.

## What I tried to break and could not

- **Every stated cell of both tables matches the code.** A unit harness drives `judgeServed`, then `withholdReady`, then `conds.merge`, for all 12 combinations of route reading and clause, plus the HELD row under all three route readings. These all match:
  - the reason on `PolicyApplyIncomplete`, `Ready`, `Degraded` and `GovernanceSkipped`;
  - the `" | AuthPolicyNotAttached: "` composition;
  - each lead, including `clausePartlyValid` reading the same on every route reading;
  - where "The hole is announced" appears;
  - the route half's note being overwritten on `GovernanceSkipped`;
  - the claim store;
  - in the UNKNOWN row with `routeRefused` standing, `LastTransitionTime` kept and `ObservedGeneration` restamped (stored at 1, Agent at 2, written as 2).
- **§8.1 owed item 7 holds.**
  - Under M2′, `go build` is clean, the `internal/controller` unit tests pass, and the full envtest suite passes (281.9 s).
  - I wrote the owed row in the scratch copy. Under M2′ it fails with `policyUnattached` false and `PolicyApplyIncomplete` nil. On shipped code it passes.
- **Dropping the composition row is justified.** `>` and `>=` differ only on equal ranks, and the two reasons rank 3 and 4.
- **No shipped code ever gated the raise.** I checked both `main` commits that touched `authserved.go` (22b65c2 and 6ae20e1).
- **Smaller checks, all correct:**
  - The A82 annotation's count is right: A83 is mentioned 5 times, 2 inside bold, and every line is balanced.
  - `conditions.md` cites `:809`, which is right, and `go test ./test/docs/...` passes at head.
  - `authtxn.go:606` is the right line.
  - Both tables are well-formed, with 6 and 4 cells on every row.
  - D7's three arguments hold.

## MAJOR

**M1. The tables' assumption sentence is false for the HELD row, and it hides a real rule-8 defect** (`03-policy-compiler.md:837`).
- **What A84 says:** a `ForeignTrafficPolicy` or `GatewayAuthPolicy` on `GovernanceSkipped` "composes by `incompleteOrder` or `appendGovernance` as stated elsewhere".
- **What the code does:** on the held path, `holdGovernance` re-asserts a stored `GovernanceSkipped=AuthPolicyNotAttached` whole through `conds.carry`. That overwrites a `ForeignTrafficPolicy` or `GatewayAuthPolicy` the same pass just derived, because `reportForeign` and `reportAboveServed` run before it.
- **Measured on envtest** with a scratch row:
  1. A served API-key Agent, then `acceptRoute`, then `unattachPolicy`, then one reconcile. `GovernanceSkipped` reads `AuthPolicyNotAttached`.
  2. Strip `assayd.dev/agent-uid` from `<agent>-auth` and reconcile twice.
  3. The slot is empty. `PolicyApplyIncomplete` and `Ready` read `ForeignTrafficPolicy`. `GovernanceSkipped` reads True/`AuthPolicyNotAttached` with the held lead and never mentions the foreign policy.
  4. A planted "intruder" policy plus a moved `appliedDigest` gives the same result. The unit harness shows the same for a fresh `GatewayAuthPolicy`.
- **Why it matters:**
  - It breaks §5's rule at `:1395`: "a standing `ForeignTrafficPolicy` or `GatewayAuthPolicy` keeps the reason". It also breaks the guarantee in `appendGovernance`'s own comment.
  - The held message sends the reader to "GovernanceSkipped and any ForeignTrafficPolicy report", and `GovernanceSkipped` shows neither.
  - This is the exact takeover path A83's unjudged `why` names, and it lasts as long as the foreign policy stands.
  - A84 says the HELD row "was read from the code and was not driven", while `:1532` claims every cell was "measured". The error sits in that undriven row.
- **Fix:**
  - Correct the sentence at `:837`.
  - Add a §8.1 owed row: raise a policy-half claim, strip the UID label, and assert `GovernanceSkipped` reads `ForeignTrafficPolicy` with `AuthPolicyNotAttached` appended.
  - Route the code fix separately, since A84 is comment-only. `holdGovernance` should carry the condition whole only when the pass has not already asserted `GovernanceSkipped` True with `ForeignTrafficPolicy` or `GatewayAuthPolicy`, and should use `appendGovernance` otherwise. The root is A81's "re-asserted whole" rule at `:860`, which conflicts with §5.

**M2. `Ready` and `Degraded` do not name the policy half, though both passages A84 rewrote say they do.** The text predates A84 but is inside the amended bullet and row.
- `:835`: "`PolicyApplyIncomplete`, `Ready` and `Degraded` read `ServingRouteNotAccepted` instead, with `AuthPolicyNotAttached` named in the message".
- `:1395`: "…read `ServingRouteNotAccepted` and name this half in the message".
- **Measured:** in the REFUSED row and in the UNKNOWN row with `routeRefused` standing, the `Ready` and `Degraded` messages are `routeRefusedMessage` alone. `withholdInOrder` keeps the outranking failure's message and never composes. Only `PolicyApplyIncomplete`'s message names the policy half.
- The first table's "Ready, Degraded" column says nothing about their messages, so the tabling did not catch it.
- **Fix:** say that only `PolicyApplyIncomplete`'s message names the policy half, and add the message to that column.

## MINOR

1. **The UNKNOWN list is incomplete** (`:837`, `:1532`). It leaves out a missing leg: the unit table's own "only one leg reported at the current generation" row, and `routeReport`'s "no condition of the type". At `:1532`, "no entry for the assayd Gateway at its current generation" also mixes up an entry with a condition's generation.
2. **The tables rest on an uncommitted throwaway run.** No committed row pins these cells:
   - the route note being overwritten in the REFUSED row;
   - the `ObservedGeneration` restamp stated in the new hold-rule sentence (`:859`) and at `:855`.

   Either say which committed row pins which cell, or owe the matrix as a unit table. My harness is about 100 lines against `judgeServed` and needs no envtest.
3. **The `condIs` panic guard is owed only in §11** (`:1536`), not in §8.1's owed list. That is A84's own argument about item 7: an owed item that lives only in a §11 paragraph is one nobody counts.
4. **The "plainly" paragraph overclaims** (`:1535`). It says `GovernanceSkipped=AuthPolicyNotAttached` "means exactly one thing": the Gateway's last word, at the policy's current generation, is a broken tuple. That is false on three held paths:
   - a stale Gateway report;
   - an errored `-auth` step;
   - the unjudged path: a UID-label takeover, or an upgrade that changes the render digest. After such an upgrade the claim stands permanently, whatever the Gateway reports.

   The prior critique said to keep this paragraph verbatim; it needs the held-claim meaning added.
5. **Some provenance claims are wrong or contradictory.**
   - `:1528` says "no rule moves", but A84 removes a precondition from an approved slice's §5 rule and opens D7 over it. What did not move is the code.
   - `:1540` says lifting the matrix out was "all of that cell A84 wrote". At head, the §5 cell still grows from 16,322 to 18,045 characters, and the §3.3.3 bullet from 10,541 to 11,723.
   - `:1537`'s "(two envtest rows)" is contradicted by `:1538` without being marked superseded.

## Outside A84's scope, to route separately

The code defect behind M1 is in A81/A83 code (`holdGovernance` in `internal/controller/authserved.go`) and in §3.3.3 `:860`. It is a rule-8 error on the tier condition: `GovernanceSkipped` stays True, so it is not fail-open, but it names the wrong cause. The reproduction is in M1.

## Files

- Design: `docs/designs/03-policy-compiler.md`
- Code: `internal/controller/authserved.go`
- Scratch tests, not in the repo *[paths redacted]*: a unit harness against `judgeServed`, a scratch envtest row with a planted "intruder" policy and a moved `appliedDigest`, the owed row drafted as a test, and the UID-label takeover row.
