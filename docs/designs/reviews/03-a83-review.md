# Design 03 (policy compiler): independent review of PR #55 and scoped critique of A83

- **Date**: 2026-09-23.
- **Reviewers, and the distinction matters more than the findings**: round 1 was the AUTHOR'S OWN forked self-review (`1695d14`); rounds 2 and 3 were an INDEPENDENT adversarial pass, which saw `3e22f4c` and `8359682` only. AGENTS.md rule 4 exists because self-review has never once caught what the independent pass caught, and an earlier version of this header merged the two into “an independent adversarial pass … two rounds” — erasing exactly that distinction, in the record kept to preserve it. The independent reviewer caught the mislabelling too. Both follow `.claude/skills/review-code` and `.claude/skills/critique-design`.
- **Target**: PR #55 (`a83-policy-half-message`), base `main` `80b63b5`; and amendment A83, which that PR adds and which had never been critiqued. A third independent round, at `8359682`, returned REQUEST CHANGES on four narrow items — this record's own mislabelling of round 1 among them — plus one surviving mutation, and is folded in below. `docs/designs/README.md` says in terms that A82 and A83 have not been critiqued — this round is that critique, so this file is its output and not a precondition of it.
- **Scope**: the clause split of `policyReport`'s broken answer, the message `policyBrokenMessage` builds from it, and A83 as an amendment to an approved slice's normative body.

## Verdicts

- **Round 1 — the author's forked self-review, at `1695d14`: REVISE** — 0 BLOCKER, 3 MAJOR, 3 MINOR.
- **Round 2 — the first INDEPENDENT pass, at `3e22f4c`: REQUEST CHANGES** — 0 BLOCKER, 2 MAJOR, 6 MINOR. In the reviewer's words: the change is right and close, the clause split is the correct fix for A82's measured defect, the ranking moves no answer, the `judged` gate is a real correction, and fixing the two majors earns an approve without a further round.
- **Round 3 — the independent pass again, at `8359682`: REQUEST CHANGES**, narrowly — 0 BLOCKER, 0 MAJOR by its own grading, four items: one markdown edit set, one test assertion, one ledger line, one header label. The reviewer said fixing these approves the diff alone with no further round.

Both rounds re-ran every gate independently. The three survivors A83 had named by then were confirmed genuinely pinned.

## What was verified FOR the change

**Answer-invariance was tested, not read.** `main`'s `policyReport` was extracted verbatim and run beside the new one over **29,624 generated inputs**: zero differences. The mechanism is that the old `known++` ran iff neither switch arm matched — which is exactly what `default: known++` does — and that continuing the loop instead of returning cannot move the answer, because any non-empty clause string forces `reportBroken` through the ranking switch and `known == 2` is unreachable while one is set.

**D5(b)'s justification is true of the code, not a rationalisation.** `servedClaims` is literally two booleans; `storedClaims`/`writeTo` read and write only those; `incompleteOrder`, `carriedReason`, `a80Carried`, `holdGovernance`'s stored-reason match and `refuseAdopt`'s seeding all key on the single reason string. §9 D5 states what would re-open it — the API addition — which is the honest form.

**The decision record is complete**: (B2), (B3), D5(c) and D6 are each stated as not-taken or still-owed.

## Round 1 (the author's own forked review) — three MAJORs, all fixed in the round

1. **A compiling single-edit mutation that SURVIVED the whole suite.** `clauseRejected`'s `!routeOK` hedge was reached by no test: the unit table's only rejected row passed `routeOK: true`, and `grep 'policyCondition("Accepted", "False"' test/ internal/` returned nothing. Deleting lines 436–443 compiled and left `go test ./internal/... ./api/...` and the envtest rows green. The arm is live — §5 keeps it for a release that starts producing `Accepted=False` — and unpinned it reaches A81's MAJOR 2 verbatim: "this Agent's route is accepted and SERVING" on a pass that recorded the route refused, with nothing able to fail on it. Rule 5.
2. **The partly-valid lead promised the Gateway's cause and delivered none, then supplied one the code never checked.** The reason-mismatch arm formatted `c.Reason` and dropped `c.Message`, where the `ConditionFalse` arm keeps it. `PartiallyValid` is the reason for all three causes the research note measured — a rejected key-`ConfigMap` entry, an authorization expression that does not parse, an extAuth Service that does not exist — so the message is the only field that separates them; and the lead asserted the `ConfigMap` as *the* cause plus a namespace-wide blast radius that is wrong for two of the three. An administrator hitting row 1 would be told to check a shared object for a per-policy fault, with the field naming the real one discarded.
3. **The held lead asserted two facts neither held path checked, and one state is reachable and permanent.** It said the Gateway "has not reported at the policy's current generation since" and that the claim "stands until the Gateway reports again". An errored pass read nothing, so it cannot know the first; and `judgeServed`'s default branch fires on `reportUnknown`, which includes `out.judgePolicy == nil` — `reassertServedPolicy` returns `(nil, nil)` for a foreign policy **and for a digest mismatch**, i.e. after any operator upgrade whose `compiler.AuthPolicy` renders differently. There the operator never reads the policy's status again, so the claim is re-emitted for ever under a message promising a report that cannot come, and the shared tail still asserted "the policy is present, carries this Agent's UID and renders to `status.auth.appliedDigest`" — false in both cases.

## Round 2 (the first independent pass) — two MAJORs, both fixed in the round

1. **The held message still contradicted itself on the path A83 added a lead for.** The "has not reported since" claim was removed from the `why` and left in `heldNote`, which `holdIncomplete`/`holdGovernance` append unconditionally. The reviewer composed the real 969-byte message and printed it: it said "**NOTHING here will clear this claim until that changes**" and then "**the Gateway has not reported since**". One message, both claims, the second never checked — and `heldNote`'s own doc asserting "this pass reached the step and read the object" false on that path. It survived because the row's guard forbade the `why`'s wording (`"…at its current generation since"`) while the live string was the shorter `"the Gateway has not reported since"`: a near-miss that reads as mutation-proof.

   **The design said the same thing and was wrong the same way**: §3.3.3 and §5 both said the held report says nothing about what the Gateway has done since "because neither held path checked either" — and §3.3.3 refuted itself eleven clauses later. There are **three** held paths, and two of them do say it, correctly.
2. **`judged: true` at the RAISING call site was unpinned.** Flipping the literal to `false` compiled and passed everything, including the full 282 s envtest. Every raised `AuthPolicyNotAttached` then silently lost `policyJudgedNote`, the sentence distinguishing the Agent's own policy from `AuthPolicyMissing`/`ForeignTrafficPolicy`. The unit table pinned the *function* for both `judged` readings; nothing pinned the *argument* where it is raised.

## Round 2 minors (independent)

1. **`known++`'s comment overgeneralised, and the reviewer disagreed with A83's own forked critique.** `_ = known` compiles and fails a unit row at once, because `known == 2 → reportHolding` is the only path that ever clears a standing `AuthPolicyNotAttached`. The arm's *placement* is inert; the *counter* is load-bearing. A83's forked critique had graded it a liability; the reviewer graded it pinned, and re-measured to say so.
2. **Two places still asserted the clause the split stopped asserting**: `api/v1alpha1/agent_types.go`'s `PolicyUnattached` description (and the generated CRDs, surfaced by `kubectl explain`) and `internal/controller/authtxn.go`'s reason comment — "the last report … said **the Gateway does not attach it**", where since A83 that boolean is also set on `clausePartlyValid` and the Gateway says `Attached=True`. The field **name** is API and stays; the descriptions are not.
3. §3.3.3 kept "on this pass", the phrase §11 says A83 rejected as overstating a reading the generation gate makes narrower. It matters: `clausePartlyValid` fires whenever no `Attached=False` exists **at the current generation**, and the research note records `Attached` lagging `Accepted` by a generation on 1.5.0.
4. `docs/designs/README.md` still carried the pre-fix ledger ("Eight mutations, seven killed and one survived") against §11's thirteen — under-reporting in the direction that matters, hiding that two of three survivors came from the independent pass, which is A83's own argument for itself.
5. §11's arithmetic did not close: thirteen claimed, twelve named. An unnamed mutation is an unauditable claim.
6. §11's "What the decision explicitly does NOT do" listed (B2), (B3), (C1) and D6 and omitted **D5(c)**, which (B4)'s own row includes.

## Round 3 (independent, at `8359682`) — REQUEST CHANGES, narrowly

Both of round 2's MAJORs verified genuinely fixed: all three held messages composed from the shipped constants and printed, and no message carries both claims any more. Answer-invariance re-run at that head — 29,624 inputs, 0 differences. All six MINORs confirmed applied rather than described. Four findings:

1. **`AGENTS.md` and `CLAUDE.md` were not touched, and the author's report said they were.** Three things were still live in both: round 2's MAJOR 1 design half, the survivor count, and MINOR 3's generation qualifier. The files are model-neutral precisely so a rule lives in one place, so §3.3.3 and `AGENTS.md` were giving opposite accounts of the held report and Codex, Cursor and Copilot saw only the wrong one. Mirrored, byte-identical.
2. **The MAJOR 1 fix left its mirror arm unpinned** — `note = heldNote` → `unjudgedNote` in the JUDGED arm compiles and passes everything, and the condition then says “this pass judged no policy” beside “The policy is present”. MAJOR 1 with the arms swapped, inside its own fix. Reproduced, pinned both ways, killed.
3. **This record mislabelled round 1** as the independent reviewer's when it was the author's forked self-review. Fixed in the header, the verdicts and the section titles.
4. The §11 and README ledgers needed the seventeenth mutation.

## Two the independent reviewer explicitly cleared

- `"has not reported at the policy's current generation since"` in the unit row's `saysNot` is **not** dead: it is a live forward guard against the phrase moving back into the lead. The reviewer disagreed with A83's forked critique here.
- The five citations of "A83's review" with no file under `docs/designs/reviews/`: the README says in terms that A82 and A83 have not been critiqued, and this round IS that critique, so the file is an output, not a precondition.

## An infrastructure bug found in passing, not caused by this PR

The reviewer's e2e run was corrupted by a concurrent run from another agent: `hack/e2e.sh` does `kubectl config use-context` on the shared default kubeconfig, so two runs flip the global current-context under each other. The harness's own self-check caught it — `TestTheOperatorUnderTestIsTheOneJustBuilt` reported the deployed operator was a different build — which is the check working. That run's e2e evidence for this PR is therefore inconclusive; nothing in the diff is covered by e2e anyway, since it is condition-message text. Filed separately, with `hack/e2e.sh:255`'s unquoted heredoc.

## What A83 did with all of it

Every MAJOR and every MINOR of both rounds is fixed in the round rather than recorded as owed. The mutation battery closed at **seventeen, none INVALID**: twelve killed outright and five that survived — the errored hold's policy branch, `clauseRejected`'s `!routeOK` hedge, `judged` at the held call site, `judged` at the raising call site, and the judged arm of the note selector that fixed round 2's MAJOR 1 — each then pinned by a new row and killed. **Four of the five came from the INDEPENDENT rounds or from re-mutating after their fixes; one came from the author's forked round.** That ratio is the point of rule 4 and is the reason this record now labels who found what. Both rounds also found the split's own words re-asserting, in `heldNote` and in the Gateway `message` A83 first dropped, the very claims the split had removed from the lead — which is the clearest available statement of why a message split needs an adversary rather than an author.
