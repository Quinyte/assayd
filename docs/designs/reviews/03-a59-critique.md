# Design 03 (policy compiler): critique of A59, scoped to the first slice (§1.1)

- **Reviewer**: an independent adversarial critique, following `.claude/skills/critique-design/SKILL.md` and AGENTS.md. It is a cold read with no author context. This is the ninth critique of the consolidated body, and the fourth to read §1.1.
- **Target**:
  - `docs/designs/03-policy-compiler.md` at `origin/design-03-a59` `4e756bb` (PR #24);
  - `docs/decisions/0034-slice-auth-follows-current-spec-narrows-only-on-a-probe.md`, including Amendments 1–3;
  - `docs/research/agentgateway-prepared-route-401-spike-2026-09.md`;
  - `docs/designs/reviews/03-a58-critique.md`.
- **Scope**: the human's decision G limits approval to §1.1's slice. `Narrow`, `Loosen`, E2, F2's group rules, D2, `Withdraw`, the other concerns and the candidate route are out of scope. A finding is raised against them only where the slice depends on them or they contradict it.
- **Method**:
  - Each finding of the eighth critique was checked in the body, at its source.
  - The slice was then walked once more, as an implementer and as an adversary, looking only for guess-points and false guarantees. The walk covered `Create`, both `Lock` entries, abandonment, the prepared re-create, I1, the refused `apikey` → `none`, `Adopt`, the probe, and the entry predicate A59 added.
  - That walk was checked against the code it lands in: `internal/controller/{card,agent_controller,httproute,conditions}.go`, `api/v1alpha1/agent_types.go`, `charts/assayd/templates/admission.yaml`, `config/rbac/role.yaml`, design 02 A25 and design 10 §5.
  - Nothing was run against a cluster. No file other than this one was written.

## Verdict for the first slice: **REVISE — 0 BLOCKER, 1 MAJOR, 3 MINOR.**

A59 closes both MAJORs of the eighth critique in the body, and five of its six MINORs. The sixth is recorded open, with a reason that holds. One defect remains, and it was introduced by the M2 fix.

A59 defines when a transaction may be entered as "a desired `-auth` that compiles, which is one with a `targetDigest`", and names the `Lock` re-create as "the one exception" (03:515, 03:426). That definition is wrong at both edges:

- It excludes every `auth: none` `Create`, which has no policy to digest.
- It does not exempt the prepared re-create of a deleted route, which 03:719 keys on the recorded mode.

So the text contradicts itself on two slice paths. One literal reading executes the refused `apikey` → `none` through a route delete. The other leaves an I1 Agent unpublished while its message says the route keeps serving (MAJOR 1).

The fix is one sentence and one envtest case. With it, I would judge the slice approvable. Nothing else found here would make an implementer guess, or make the slice claim a guarantee it does not enforce.

---

## Closure against `reviews/03-a58-critique.md`

| Finding | This pass |
|---|---|
| **M1**: a bare `401` accepted as `Lock`'s proof | **Closed in the rule, not only the argument.** `ProbingAfter` accepts a `401` only when `beforeObserved` is set, or when `status.cards[]` records a digest for a revision the route's `backendRefs` currently serve. Any other `401` does not pass (03:686). The stage table (03:403), the deadline table's J2 and missing-policy rows (03:702–703) and §5 (03:1220, 03:1223) say the same. Case 11 has its third half, whose mutation is "accept a bare `401` unconditionally" (03:1275). The stub's backend override makes it expressible (03:1264). The justification cites real code: the fetch goes to the Service's ClusterIP (`card.go:181–193`), a digest is recorded only from a `200` with a valid card (`card.go:240–251`), and a failed attempt clears it (`card.go:385–391`). One clause of that justification over-claims (MINOR 1) |
| **M2**: a never-served uncompilable Agent entered `Create` and paged | **Closed for the case raised** (03:515, 03:426, 03:697, 03:712; §5 03:1227–1228; §1.1 03:48, 03:75; case 14 at 03:1278, with its mutation). The new predicate's wording opens the residue in MAJOR 1 |
| MINOR 1: the marker had no key | **Closed.** `assayd.dev/auth: "none"`, and no other value is written (03:538). The reservation claim is true in effect: `assayd-gateway-routes` refuses a `CREATE` or `UPDATE` by any identity but the operator's of a route whose `parentRefs` name the Gateway (`admission.yaml:86–107`). Golden file owed (03:1279), and case 10 asserts it (03:1274) |
| MINOR 2: abandonment's write order | **Closed.** `backendRefs` go first, then the policy. For an uncompilable mode the route is deleted, then the policy (03:711). Case 10(d)'s mutation reverses the order (03:1274) |
| MINOR 3: the stub could not answer `503` or a backend `401` | **Closed** (03:1264) |
| MINOR 4: no `ProbingAfter` timeout or interval | **Closed**: 5 s per request, 5 s requeue (03:403, 03:686) |
| MINOR 5: stale reason count | **Closed**: six (03:252) |
| MINOR 6: an operator-named `<agent>-auth` beside `mode: none` | **Recorded open** (03:1320). The reason holds: reporting it as `ForeignTrafficPolicy` would fire between an abandonment's status update and its delete. It fails toward refusal. It is not re-raised |
| Call 3: the message on a `Lock` abandoned to `oauth` | **Done** (03:712), case 10(e) |

---

## MAJOR

### M1: A59's entry predicate excludes `auth: none` and does not exempt the route re-create, so two slice paths contradict it

**Where.**

- 03:515: "A `Create` or `Lock` is entered only for a desired `-auth` that compiles, **which is one with a `targetDigest`** … A `Lock` that re-creates a missing policy is **the one exception**: its target is compiled from `status.auth`, not from the spec."
- 03:426–427: `targetDigest` is "the policy being applied. **Always set**: a transaction is entered only for a target that compiles."
- Against the first half:
  - A `none` Agent has no policy to digest. `status.auth` carries its digest fields only when `mode` is `apikey` (03:414–423, 03:443).
  - Yet "a `Create` of a `none` route records `{mode: none}` at `Served`" (03:443).
  - And "for `none`, a never-served Agent enters `Create` and is published at once" (03:712).
- Against the second half: "**For an Agent whose `status.auth` records a `mode`**, the route goes back through `Create` from `PreparingRoute` … Its kept or re-created `<agent>-auth` converges on it" (03:719). That re-create keys on the recorded state, not the desired spec, and 03:515 does not list it as an exception. §1.1 owes design 07 an emitter that "re-creates a deleted route the same way for an Agent with `status.auth`" (03:75). That covers I1 Agents too.

**What breaks.** Each case below is reachable in the slice.

1. **The refused `apikey` → `none`, executed by a route delete.**
   - A served `apikey` Agent is edited to `none`. The edit is refused (`AuthTransitionNotBuilt`, 03:522), and the recorded policy stays.
   - Someone with route delete deletes `<agent>-serving`.
   - Under 03:515 the re-create is an ordinary `Create`, whose target comes from the desired spec. Desired `none` compiles, because the `-auth` concern is not a *different* mandatory concern.
   - So a `Create` of `none` is entered and "published at once with its marker label" (03:712, 03:719). `Served` records `mode: none`.
   - A lock the slice calls one-way (03:39) is opened by a route delete, and status then agrees with the open route.
   - Under 03:719 instead, the kept policy converges and the route waits for `401`. The two texts give opposite outcomes.
2. **An I1 Agent left unpublished while its message says the route keeps serving.**
   - A served `apikey` Agent is edited to `oauth`, so I1 applies, and then its route is deleted.
   - Under 03:515 no `Create` is entered, because the desired `-auth` does not compile and a route re-create is not "the one exception".
   - The emitter re-creates a prepared route (03:75). Nothing ever publishes it.
   - The Agent carries only `PolicyCompileFailed`, which design 10 routes to a ticket (`10-observability-pack.md:50`). The message still "says that the previous `-auth` is still enforced" (03:517), and I1 says "the route keeps serving under it" (03:515).
   - That is an indefinite outage, reported as a ticket that describes the opposite. Rule 7 and rule 8 both apply.
3. **Every `none` `Create` read literally.** "Compiles" is defined as "has a `targetDigest`". `none` has none, so by the definition a never-served `none` Agent enters no `Create` and is never published. Nobody will build it that way, but the definition then has to be ignored to build anything. That is the guess this finding is about. The same gap leaves the abandonment trigger (03:709) comparing "the transaction's target" mode with no field that records a mode for a `none` target.

Case 8 (03:1272) covers the route re-create only where desired equals recorded, so no case pins either outcome.

**Fix.**

1. State the predicate by mode, not by digest. A `Create` or `Lock` is entered only when the desired `-auth` mode is one the slice compiles, `apikey` or `none`, **as a transition the slice carries**, and no other mandatory concern of the serving route is `PolicyCompileFailed`.
2. Name two exceptions, both compiled from `status.auth` and so always compilable:
   - the `Lock` that re-creates a missing policy;
   - the `Create` that re-creates a deleted `<agent>-serving` for an Agent whose `status.auth` records a `mode`. Its target is the recorded state, whatever the desired spec says. So for the refused `none` it is the recorded policy, and for an I1 `oauth` Agent it is the recorded `apikey` or `none`.
3. In the `status.auth` schema, say what `targetDigest` holds for a `none` target (for example: absent, with `kind` and a new `targetMode` naming the mode). Make the abandonment rule compare `targetMode`.
4. Owe case 15, both halves with the switch held, then released:
   - (a) A served `apikey` Agent edited to `none` and refused has `<agent>-serving` deleted. The re-created route is prepared, `<agent>-auth` is unchanged, and `backendRefs` attach only on `401`. It never carries the marker label, and `status.auth.mode` stays `apikey`.
   - (b) A served `apikey` Agent edited to `oauth` has its route deleted. The route is re-created, prepared, and published on `401` under the recorded policy.
   - Mutation: compute the re-create's target from the desired spec. (a) and (b) must each fail.

---

## MINOR

1. **"At most one drift interval ago" holds only for the desired revision** (03:691).
   - The operator fetches a card only for `desiredDigest`, and only once that revision is ready (`agent_controller.go:551`, `:563`). It requeues on the drift interval only for that revision (`:614–615`).
   - The route follows `status.activeRevision` (`agent_controller.go:728`), which lags the desired revision until the desired one is available (`:619–620`).
   - A J2 edit mints a revision, because `expose` is on design 02's behaviour surface (design 02 A25, `02-agent-crd-operator.md:732`; 03:516). So during a `Lock` the serving revision is often not the desired one. Its digest is then as old as the last fetch made while it was desired, and nothing bounds that age. A crashlooping new revision keeps it stale indefinitely.
   - The rule at 03:686 still attributes on that digest. It is implementable as written, but the stated residue is larger than the text says.
   - **Fix**: scope the sentence to the revision being fetched. Either say the age of another serving revision's digest is its `fetchedAt`, unbounded, and name that in the message, or accept a digest only while its `fetchedAt` is within `CardDriftInterval`. The second makes the bound true, at the cost of a stall where the first only reports.
2. **`Lock` and the J2 edit's own revision are never ordered.**
   - §1.1 puts "the ordering of auth transactions around a weight shift" out of the slice, because "no group can change" (03:60). But a J2 mode edit is in the slice, and it mints a revision (item 1).
   - `Narrow` holds the edit's weight shift until `Served` (03:597). `Lock`'s row says nothing (03:596).
   - Case 6 says the route "keeps its `backendRefs` throughout" (03:1270). That can be read as "never stripped" or as "unchanged", and the second would hold the promotion.
   - Either choice is safe, because the route is open before and after. A59's call 2, "a digest for any served revision", already assumes two live revisions during a `Lock`.
   - **Fix**: say that a `Lock` does not hold the J2 edit's weight shift, and why. Correct the reason at 03:60, and word case 6 as "never without `backendRefs`".
3. **Once past its deadline, a stalled `Create` or `Lock` sends one probe every 5 s for as long as it stays stalled** (03:686, 03:705: "the machine stays in its stage and keeps working"). At slice scale this is harmless. **Fix**: none required. Optionally back off to `CardRetryInterval` after the deadline.

The eighth critique's MINOR 6 stays recorded open (03:1320). The sixth critique's MINORs 3, 4 and 5's gate stay open as recorded. None is re-raised.

---

## Verified sound

- **`Create`** is specified end to end:
  - the stages (03:44, 03:592);
  - the prepared route and the `500` → `401` attribution, which rests on the measurement (the research note, lines 17–25; 03:612);
  - re-entry at the write (03:452, 03:642);
  - the golden policy: name, namespace, labels, target, `mode: Strict` emitted, the constant selector and the namespace group (03:1066–1091, 03:1095);
  - the deadline outcome in every stage (03:701).
  An implementer can write case 2, case 3's golden assertion and case 4 without guessing.
- **The constant selector makes D2 unreachable, and the namespace group makes E2 and F2's group rules unreachable** (03:30–31). Neither claim depends on anything owed.
- **I1 and abandonment do not meet.** Deletion is keyed on the positive `written` record of a transaction short of `Served` (03:514, 03:710). Case 10(c)'s mutation separates that from keying on desired `none` (03:1274).
- **The `Adopt` trigger on the whole record** (03:727) with case 13 (03:1277). The "one status update" abandonment (03:712) keeps `Adopt` from firing in between.
- **Conditions and ownership.** `PolicyApplyIncomplete` is owned and not sticky, and `GovernanceSkipped` is owned and sticky (`conditions.go:75–80`, `:92–102`). `PolicyCompileFailed`'s ownership is honestly owed (03:53, case 9).
- **RBAC and flags are honestly owed.** `events` is granted only `create, patch` today (`config/rbac/role.yaml:25–28`). No `agentgatewaypolicies` grant exists, and 03:299 and 03:76 say so. `--gateway-serving-url` does not exist, and 03:74 says so.
- **H2 without `--gateway-replicas`.** Every count is unknown, so every pass reads `AuthVerifiedOnOneReplica`, which is true (03:665).

## A59's author's calls

| Call | My view |
|---|---|
| The marker is `assayd.dev/auth: "none"`, and no other value is written | **Defensible.** The route reservation covers it for any attached route (`admission.yaml:86–107`). A route whose `parentRefs` are removed is unattached and carries no traffic, and the owned-field comparison re-asserts it. No human needed |
| A digest for any revision the route serves attributes the `401` | **Defensible.** An anonymous probe cannot tell which weighted backend would have answered, so any narrower rule is unimplementable. Its freshness is weaker than stated (MINOR 1). No human needed |
| A `Create` abandoned to a mode that does not compile deletes its prepared route | **Defensible, and consistent** with "no prepared route while the serving route does not compile". Nothing was served on that hostname, so the only cost is a `500` becoming a `404`. The route goes before the policy (03:711). No human needed |
| "Compiles" covers the serving route's other mandatory concerns, so a stored `spec.budget` is the same case | **Right, and necessary**: otherwise a never-served `budget` Agent pages under the wrong cause, which was the eighth critique's M2 by another door. The predicate's *definition* is what MAJOR 1 corrects, not this scope. No human needed |
| `ProbingAfter` repeats every 5 s | **Defensible.** It matches the e2e poll (`test/e2e/authz_test.go:268`) and the card timeout, and the deadline bounds the page. MINOR 3 is optional. No human needed |

**The open human question** (03:81) is whether an `Adopt`ed Agent's owner who edits `none` → `apikey` should be locked. **It does not block the slice.** Behaviour is fully specified: `Adopt` is re-derived on every reconcile, the Agent stays refused, and the action is "recreate the Agent" (03:731–732). It reaches only routes published before the compiler, which today means the e2e harness's (`gateway.enabled` ships `false`). It is a consent decision, not a guess-point. Amendment 3's reason does not cover this owner, so it is the human's to take whenever they choose, and the slice can be implemented either way.

## The smallest set of changes to approvable

1. **M1**: state the entry predicate by mode, and name the route re-create as a second exception whose target comes from `status.auth`. Define `targetDigest`, or a `targetMode`, for a `none` target, and owe case 15 with its mutation.

MINORs 1 and 2 should ride along, because each is one sentence. MINOR 3 may stay open.

`03-policy-compiler.md (first slice, §1.1): REVISE`
