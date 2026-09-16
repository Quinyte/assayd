# Design 03 (policy compiler): critique of A77, scoped to the first slice (§1.1)

- **Date**: 2026-09-16.
- **Reviewer**: an independent adversarial critique, following `.claude/skills/critique-design/SKILL.md` and `AGENTS.md`. Cold read, no author context. This is the **first** critique of A77.
- **Target**:
  - `docs/designs/03-policy-compiler.md` at `design-03-lock-repoint` `b28b91f` (two commits: the amendment, then "correct the design 16 premise, and put the credit race where a critic sees it");
  - `docs/designs/README.md` at the same head;
  - `docs/designs/16-evalsuite.md` on the unmerged `design-16-first-slice`, read at `f420db4` as A77 cites it, and re-checked against that branch's current head.
- **Scope**: design 03's first slice is **approved and implemented** (ADR-0034 Amendment 4, slice PRs 4 and 5). A77 changes shipped behaviour in two of its transactions, so it is held to the standard for an amendment to an approved design, not to a draft's. `Narrow`, `Loosen`, budgets, rate limits, tool allowlists and the Backend are out of scope except where the slice depends on them.
- **Method**:
  - every clause of A77 traced into the code it lands in: `internal/controller/authtxn.go` (`authStep`, `runLock`, `reconcileServed`, `recreateRoute`, `lockMissingPolicy`, `isReCreation`, `abandons`, `cardAttributes`, `beforeRevisionFor`, `singleBackend`, `routeConverged`, `policyConverged`, `authPolicyIntact`, `authPolicyPresent`, `foreignAtOwnName`, `persistStatus`, `requireFreshAgent`), `internal/controller/httproute.go` (`ensureServingRoute`, `currentServingRoute`, `liveServingRoute`, `equalRoute`, `errRouteGone`, `collectRoutes`, `assessGovernance`), `internal/controller/authforeign.go`, `internal/controller/card.go` (`cardFetchDue`), `internal/controller/agent_controller.go` (`authHoldsPromotion`, `collectGarbage`), `internal/compiler/auth.go` (the policy's `targetRefs`);
  - every sentence A77 quotes from design 16 located in that branch's text and read in its own subsection;
  - `docs/decisions/` searched for the human decision A77 cites;
  - one independent reproduction, run by the coordinator against `main`, was treated as data and re-derived by hand from the code before it was used.
  - Nothing was run against a cluster. No file in the repository was written or changed.

## Verdict: **FAIL — 1 BLOCKER, 6 MAJOR, 13 MINOR.**

The defect A77 names is real and I could not break the core of the mechanism. The missing-policy `Lock` writes nothing to its route (`authtxn.go`, `runLock`'s re-creation branch, `rt = cur`), `cardAttributes` reads only the route's `backendRefs`, and `authHoldsPromotion` holds for I1 alone — so an Agent whose serving route names an uncarded revision pages under `AuthPolicyMissing` for as long as it exists while `status.activeRevision` advances past it. Re-pointing after the policy write, never before, is the right call, and the drafter's second rule is the best thing in the amendment: without it the re-point credits r1's own `401` in exactly the case it was built for. Finding that the same hole is open in a shipped transaction is worth more than the wedge fix.

But the amendment does not yet hold as a change to approved behaviour:

- the sentence that carries its entire cost argument — the route is not open because the policy is "written, converged and enforcing" — is false at the placement the drafter chose, and is contradicted two bullets above it by the drafter's own restatement of §3.3.2 (BLOCKER);
- the scope of the live hole is misstated (J2 only; it is J2 **and** K2, and not the missing-policy `Lock`), and §8.1 owes no case for either, so an implementer who guards the `reCreate` branch alone passes every owed test and fixes nothing that ships (MAJOR 1);
- the rewind is keyed on a within-pass event and records nothing durable, so a lost status write, a restart, or a route someone else moved re-opens the window it names (MAJOR 2);
- the re-point can move attribution **off** a carded revision and wedge a `Lock` that finishes today — the inverse of the case it fixes, stated nowhere (MAJOR 3);
- the residual wedge is in §11 only, and the Status line says §11 is provenance (MAJOR 4);
- the design 16 damage list misses the paragraph that constructs the case A77 removes, and calls reachable state unreachable (MAJOR 5);
- the human decision that changes an approved slice has no ADR, unlike all five before it (MAJOR 6).

Answers to the seven questions put to this critique are under "The questions, answered" below.

---

## BLOCKER 1 — the safety claim that justifies the re-point's placement is false at that placement, and is contradicted in the same amendment

`03:779` (§3.3.3, "Safe because the route was already open") and `03:1408` (§11, "What it costs"), identically:

> "**For a missing policy's `Lock` the route is not open when that happens**: the re-point is placed after `ApplyingPolicies`, so `<agent>-auth` is written, **converged and enforcing** … The one window in which the route is genuinely open is between the out-of-band delete and that write, and no pass re-points in it."

Three things are wrong with it, in ascending order of seriousness.

**1. At the placement A77 chooses, the policy is not converged.** `03:770` puts the re-point "at `Converging` and at `ProbingAfter`, after `<agent>-auth` has been read present and equal to the target for that pass". In `runLock` that read is `authPolicyIntact`, the first statement of `case StageConverging, StageProbingAfter`. `routeConverged`/`policyConverged` run *after* it, and only under `if tx.Stage == StageConverging`. So on the first `Converging` pass the policy has been written and read intact, and the tuple has **not** held — and that is the pass on which A77 first moves the backend.

**2. "Enforcing" is the inference §3.3.2 exists to forbid**, and A77 restates the prohibition itself at `03:771`/`03:1406`: "The tuple proves control-plane convergence only, so a window remains after it holds (§3.3.2). It is stated, not closed." `policyConverged`'s own comment says `reason == Valid` "is the controller's translation, not a proxy's acknowledgement". A design cannot say in one bullet that the tuple never proves enforcement and in the next that the re-point is safe because the policy is enforcing.

**3. The ordering against the NACK read is unspecified, and the consequence is unbounded.** `freshNack` runs inside the same `case`, between `authPolicyIntact` and the tuple check. §3.3.2 and §8.1 record that a NACK retains the old dataplane config while the control plane reports converged. A77 never says whether the re-point precedes or follows the NACK read. If it precedes it, then under a standing NACK — which holds the transaction at `Converging` indefinitely — every pass moves a backend onto a route the dataplane is serving with no `<agent>-auth` at all.

A fourth, smaller leg: `03:770`'s absolute — "**no pass moves a backend onto the route while the run namespace holds no `<agent>-auth`**" — rests on a cached read. `authPolicyIntact` uses the manager's client, not `r.reader()`, and the design already records the same check-then-write race for `reconcileServed`. "Read present earlier in the pass" is not "present at the write". Where this design needs a read to be authoritative it says "read live" (`liveServingRoute`, `listPolicies`); this new write is given an absolute the read cannot support.

**Fix.** Place the re-point at **`ProbingAfter` only**, after the tuple has held at the route's current generation and after the NACK read — which is exactly the safety the bullet claims, and which the existing loop already gives for free: the pass that re-points then fails `routeConverged` at the new generation and breaks before probing. Replace "written, converged and enforcing" with what is true: the route was already open before the re-point and stays open until the dataplane takes `<agent>-auth`; the marginal cost is that a revision which never carried traffic through this route becomes reachable on it for the dataplane window, and for as long as a NACK stands. State the NACK ordering explicitly, and make it a named mutation in case 18.

---

## MAJOR 1 — the scope of the live hole is misstated, and nothing owes a test for it

`03:1406`: "J2's route follows promotion already (A72), and its **digest branch has the same hole open today, unrecorded**"; `03:1402`: "the rule changes approved, implemented behaviour **in two places, not one**."

The hole is real. I derived it by hand and it matches an independent reproduction against `main`:

- `creditable := perr == nil && answer.Code == 401 && (observed || attributed)`;
- `attributed` comes from `cardAttributes(agent, status, rt)`, and `rt` is what `ensureServingRoute` **returned**, i.e. the post-write object naming the new revision;
- A61's clearing is gated on `tx.BeforeRevision != ""` and touches only the `observed` branch;
- `routeConverged` runs only under `if tx.Stage == StageConverging`, so at `ProbingAfter` the route can be rewritten to a new generation and probed in the same pass.

The reproduction's measured output — route generation 2, Gateway still reporting `Accepted` for generation 1, probe answers `[401 401 401]`, `status.auth` recorded as `apikey` — is the shape the code predicts, and nothing corrects it afterwards: five further passes sent zero probes, and the record is one-way.

But the count is wrong in both directions.

- **K2 is not named.** `runLock`'s non-re-creation branch is one branch; `k2 := status.Auth.Mode == ""` only changes the message. K2 takes the identical write, the identical A61 clearing and the identical `cardAttributes` call, and the reproduction says K2 reproduces identically. A77 names J2 alone.
- **The missing-policy `Lock` is not one of the "two places" of *implemented* behaviour.** It writes nothing to the route today, so its `backendRef` cannot change mid-transaction; the reproduction could not drive it there (it parks at `Converging`, zero probes). The implemented places are J2 and K2; the missing-policy `Lock` is a specified place.

The consequence is not cosmetic. **§8.1 case 18 (`03:1371`) is titled "The `Lock` of a missing policy follows `status.activeRevision`", and all five sub-cases (a)–(e) are built on that `Lock`.** `03:1351` says "five of its assertions describe behaviour no code has yet". So an implementer who puts the rewind guard inside the `reCreate` branch passes every owed case while J2's and K2's shipped hole survives untouched — and the amendment's own headline claim is enforced by nothing. That is the corpus's "a test that cannot fail" shape, raised to the amendment.

**Fix.** Say "J2's and K2's `Lock`s", and say that the missing-policy `Lock` is the specified place and J2/K2 the implemented ones. Owe a case 18 (f) on a J2 `Lock` at `ProbingAfter` whose route moves r1→r2 in the crediting pass, with a mutation that gates the rewind on `isReCreation` and must be killed. Mark it, in §8.1, as the one case in 18 that covers behaviour that ships today.

---

## MAJOR 2 — the rewind is keyed on a within-pass event and records nothing, so it does not close the window it names

`03:771`/`03:1406`: "A pass that changes the route's one `backendRef` credits no `401` in that pass. **It records the change**, returns the transaction to `Converging`, and lets §3.3.2's tuple hold again at the route's new generation before the next probe."

"Records the change" names no field, and there is nothing to name. `ensureServingRoute` returns `(route, created, err)` — no "changed" signal — and `status.auth.transaction` holds no probed backend: `beforeRevision` is empty in exactly the case the rule is written for (a re-creation `Lock` has no `ProbingBefore`; a J2 `Lock` whose before-probe saw nothing never sets it). So the only thing the rule can record is the stage, and the stage is written by the same `persistStatus` that can fail.

Three sequences then re-open the window:

1. **A lost status write.** Pass N moves the `backendRef`, sets the stage back to `Converging`, and its `persistStatus` loses to `Conflict`. That is not hypothetical here — design 16's slice documents precisely this: an eval record bumps the resourceVersion and "the transaction's next `Update` loses to `Conflict`, or stops at `requireFreshAgent`". Pass N+1 reads the stage still at `ProbingAfter`, finds the route already at r2 (`equalRoute` true, no write, no change), probes, and credits with the Gateway a generation behind.
2. **A restart** between the route write and the status write leaves the same state.
3. **A route someone else moved** between passes: the operator's pass changes nothing, so no rewind fires, and the tuple is never re-checked at `ProbingAfter`.

The rule the code needs is a **precondition of `ProbingAfter`, not a reaction to a change**: no probe answer is taken unless §3.3.2's tuple holds at the route's current generation. That is durable across restarts and lost writes, needs no new field and no change detection — and the coordinator's reproduction measured exactly that minimal fix (re-check `routeConverged` at the top of `ProbingAfter`) parking both the J2 and the K2 repro with the mode unchanged. A77 specifies the weaker, more complex form.

**Fix.** Restate the rule as: at `ProbingAfter`, in any `Lock`, §3.3.2's route half is re-checked before the probe, and a route whose status is not reported at its current generation takes no answer — with the transaction returned to `Converging` so the message names the right stage. Then either drop "records the change" or name the field (`transaction.probedBackend`) and say what reads it.

---

## MAJOR 3 — the re-point can move attribution **off** a carded revision, wedging a `Lock` that finishes today

A77 argues the re-point strictly improves the evidence: `03:1407`, "its evidence gets younger … the bounded case is the usual one instead of the exception"; `03:1409`, "An Agent whose **active** revision **also** has no recorded digest is wedged exactly as before."

The word "also" scopes the residual to the both-uncarded case, and the inverse case is stated nowhere:

> `<agent>-serving` names r1, and `status.cards` **records a digest for r1**. `status.activeRevision` is r2, which has **no** digest. `<agent>-auth` is deleted.

Today: the `Lock` probes on r1, `cardAttributes` attributes on r1's digest, the `Lock` reaches `Served`, and `reconcileServed` then re-points the route to r2 in the normal way. After A77: the route is re-pointed to r2 before the probe, `cardAttributes` returns false, and the Agent pages under `AuthPolicyMissing` for good.

The state is reachable by the same entry A77 itself describes — `reconcileServed` checks the policy before it re-ensures the route, so the `Lock` can be entered in the promoting pass with the route still on r1 — and design 16's own Q13 lists how a revision comes to have no card at all: "served ungated, served before the gate was added, or promoted by a pin". A card fetch that fails after promotion, or a drift re-read that clears the digest, gets there too.

This also bears on design 16. Its exemption was narrowed to non-`isReCreation` precisely because the missing-policy `Lock` "cannot finish through a promotion" (§14, S1). After A77 it **can** finish through a promotion in one direction, and can be **broken** by one in the other. The boundary is wrong in both.

**Fix.** State the inversion as a cost, beside "What it costs": the re-point attributes on `status.activeRevision`, so a `Lock` whose route named a carded revision and whose active revision is uncarded now wedges where it would have finished; and it is the same trade J2 and K2 already take. Say what the message must then name (that the re-point happened, and which revision the attribution is waiting on). And say that design 16's exemption boundary needs revisiting in its follow-up amendment, not only its wedge subsection.

---

## MAJOR 4 — the residual wedge lives in provenance only, and the message stays non-terminal

`03:1409` states the residual honestly. The **body** does not carry it.

- §3.3.3's third `Lock` case (`03:764`) frames the wedge entirely inside the "not implemented" parenthetical: "*(Added by A77 … and **not implemented**: `runLock` takes the route as it finds it, so an Agent whose serving route names a revision `status.cards` has no digest for cannot finish this `Lock` at all …)*".
- §5's row (`03:1311`) does the same: "**… (A77) — not implemented.** **Until it is**, an Agent whose route names a revision with no recorded card digest never finishes this `Lock` … Two exits: …".

So once A77 is implemented, both readings evaporate and the authoritative body says nothing about the state that still never finishes. The Status line (`03:3`) is explicit that "§11 keeps every amendment as provenance; **the body above is authoritative**".

There is a rule-8 consequence on top. After A77 the operator **can** tell the terminal case from the transient one: it re-pointed to `status.activeRevision` and `cardAttributes` still returned false, which no future pass will change without a card. It reports the same `AuthPolicyMissing` with the same non-terminal message shape, and — unlike design 16's `EvalWaitingForAuth` — design 03's message names no exit at all. A condition that reads "waiting" for a state nothing will end is the "loud and wrong" case, not the silent one.

**Fix.** Carry the residual and its two exits into §5's row and §3.3.3's case, as text that survives implementation. Make the deadline message say the re-point has already happened and that the attribution is waiting on a digest for `status.activeRevision`, and name the administrator's delete of `<agent>-serving` and the revert.

---

## MAJOR 5 — the design 16 damage list misses the paragraph that constructs the case A77 removes, and calls reachable state unreachable

A77 is right not to edit design 16, right that it owes a follow-up amendment, and right that the two envtest rows it names become unbuildable. `03:1413`–`03:1418` quote verbatim, and I confirmed the wedge subsection is unchanged between the head A77 cites and that branch's current head. But `03:1412` claims the list is the mechanism, "not its margins", and it is incomplete.

**Missing, and each load-bearing:**

- **§1.1, "The missing-policy wedge", second paragraph** — the paragraph that *builds* the apart case: "It is not always `status.activeRevision`. `reconcileServed` checks the policy before it re-ensures the route, so a missing-policy `Lock` can be entered in the very pass that promotes. The switch has then set `status.activeRevision` to the promoted revision C, while **the route still names R**. If R has no recorded card, **that `Lock` wedges even though C's card is recorded**." Every sentence after the first goes false. This is the single paragraph A77's whole fix is about, and it is not on the list.
- **Q12's recommendation**: "A missing-policy `Lock` whose route names a revision with no recorded card is not exempt, and **no new revision would clear it, because it does not move the route**." After A77 a new revision does clear it, and Q12's recommended answer rests on that reason.
- **the dependency row**: "**Design 03**: nothing … It does not rely on that rule for a missing-policy `Lock`, **whose route does not follow promotion**."
- **the exemption subsection** and its §14 S1 rationale ("That `Lock` never moves its route"), for the reason in MAJOR 3.
- **"The wedge is design 03's, not the gate's", first sentence**: "On an ungated Agent, the same state wedges once any new revision is promoted." A77 quotes the bullet's last two sentences and leaves its first.

**Over-stated, and A77 contradicts itself:**

- `03:1415` declares the "Leaving it standing" row — R collected with its Service under a route that still names it — "**Unreachable**, because the route names `status.activeRevision`, which `collectGarbage` protects." `collectGarbage` does protect `status.activeRevision`, and `authHoldsPromotion` does hold for I1 alone, so both halves of the reason are right. But A77's own new `Converging` text at `03:770` says "A `Lock` held at `ApplyingPolicies` — by a foreign policy at the Agent's own name, or by a write that fails — leaves the route naming the revision it was already serving", and `foreignAtOwnName` breaks out of the stage loop before any re-point. A policy at `<agent>-auth` that carries another UID is neither taken over nor deleted, so that hold is unbounded — the route stays on R, promotions continue, and after three more revisions R is collected with its Service. **Case 18 (c) builds exactly that state.** The row narrows; it does not vanish.
- `03:1414`'s "**Both become exits**" carries the same over-reach, and a pin is an exit only when the pinned revision has a recorded digest — a qualification A77 states two bullets later and drops here.

**Mis-cited:** `03:1416` attributes "A missing-policy `Lock` does not move the route…" to §1.1, "The case in flight when a transaction begins". It is under "How it composes with what exists"; "The case in flight when a transaction begins" is a different subsection with different content.

**Fix.** Add the five missing items; downgrade "Unreachable" and "Both become exits" to "narrowed", with the `ApplyingPolicies` hold named as what keeps the old row alive; fix the subsection attribution.

---

## MAJOR 6 — a human decision that changes an approved slice, recorded in no ADR

`03:3` and `03:1402`: "A77 (2026-09-15) **changes the approved slice again, on the human's decision of that day**". `docs/decisions/` contains nothing for 2026-09-15 and nothing naming A77. ADR-0034's amendments run 1 to 5; there is no Amendment 6.

Every prior human decision that touched this approved slice got one — G/H2/I1 (Amendment 1), J2 (Amendment 2), `Adopt` stays refused (Amendment 3), the approval and K2 (Amendment 4), L1 and W1 (Amendment 5, one week ago, for a change of the same kind). Design 16's Q13 (a) — the instruction A77 executes — says to take it "through design 03's own process", which in this corpus has meant an ADR-0034 amendment every single time. `AGENTS.md`: "`docs/decisions/` holds the ADRs. Do not relitigate an ADR casually."

**Fix.** Write ADR-0034 Amendment 6: the re-point, the rewind rule, and the costs the human accepted — the residual wedge (MAJOR 3 and MAJOR 4), and the exposure window (BLOCKER 1). Cite it from `03:3`, `03:1402` and the README row.

---

## MINOR findings

1. **`03:771` is a rule, sitting inside the stage list.** "The stages:" now reads `ProbingBefore` → `ApplyingPolicies` → `Converging` → *"A pass that changes the route's one `backendRef`…"* → `ProbingAfter` → `Served`. Move it below the list, or mark it plainly as a cross-stage rule.
2. **`03:1422`'s "Body changed" is inaccurate in both directions.** It lists "§3.3.3's … `ApplyingPolicies` … stages"; that bullet is unchanged in the diff. It omits §3.3.3's "A `401` on a published route has more than one possible source" bullet (`03:777`) — the place where A61's crediting rule for J2 is stated, which A77 now qualifies. A reader who goes to the attribution rule gets the unqualified one, with no pointer.
3. **`03:1407` — "After the re-point the route names the revision the operator is fetching."** The operator fetches and drift-re-reads only the **desired** revision's card (`cardFetchDue(status, desiredDigest, …)`), and `status.activeRevision` lags desired through every rollout. A60's unbounded case still applies then. "Usual instead of the exception" hedges the conclusion; the premise is stated flatly and is false whenever a candidate is pending.
4. **`03:1408` — "one route write per pass of a transaction that used to make none."** `ensureServingRoute` `Get`s and compares with `equalRoute`, and writes only on drift, so the steady-state cost is one `Get` per pass, not one write. The uncosted part is `collectRoutes`' owned-route list, which §3.2's scale bullet should pick up beside A75's reads.
5. **The rule is keyed on `singleBackend`; the attribution it guards is not.** `singleBackend` returns "" unless the route has exactly one rule with exactly one `backendRef`; `cardAttributes` walks **every** rule and **every** ref and returns on the first recorded digest. A route momentarily carrying two refs is guarded by neither. Say which reading is authoritative — the clean answer is to make attribution read the route's single `backendRef` and treat "not exactly one" as unattributable, which also makes the rewind's key and the attribution's key the same object.
6. **Case 18 (d) cannot be built as written.** "The route names r1, r2 is promoted, both have digests, and the stub answers `401` throughout." With r1 carded and the stub at `401`, the first pass that reaches `ProbingAfter` credits on r1 and records `Served` before any promotion, so the state the case asserts is unreachable. The case needs r1 **uncarded** (or a probe answer that is not `401`) until after the promotion. As the only sub-case pinning the rewind, this matters.
7. **Case 18 (b)'s mutation is not pinned.** "Credit a `401` whenever the transaction re-pointed the route" names no field and, read literally, is not killed by (b): in (b) the re-point happens at `Converging`, where no probe is sent, and on the next pass nothing re-points. Restate it against the attribution (e.g. "treat a re-pointed route as attributed").
8. **Case 18 (c) has a load-bearing, unstated build order.** The foreign policy must be written **after** the `Lock` is recorded: `authPolicyPresent` matches on name alone, so a foreign policy present when `reconcileServed` runs sends the pass to `reassertServedPolicy` and no `Lock` is entered at all. Design 16's equivalent row spells its build out in three numbered steps and says why; case 18 should too.
9. **The deadline table's addition (`03:789`) names the revision only "at the deadline".** For a re-creation `Lock` the message is built once and carried before and at the deadline, so the split is not implementable as stated. And `singleBackend` returns "" when the route does not carry exactly one ref, so the message may name nothing; say what it says then.
10. **`03:1411` pins a moving branch head.** `f420db4` was the head when A77 was written and when this critique was assigned; that branch has since advanced to A13. The wedge subsection is byte-identical across the two and every quote still holds, but "amendments A3–A12" is already stale. Name the PR, not the SHA.
11. **`03:3` and the README row say "It is **Q13 (b)**".** The human decided Q13 as **(a)**, and (b)'s full text makes the slice's approval depend on the amendment — which A77 does not do, and could not, since that slice was approved first. `03:1411` half-qualifies this; the Status line and the README row do not. Say "the amendment Q13 (b) described, taken separately as (a) directed".
12. **The ordering rule A77 leans on is not in the design.** `03:1404` admits it: "No amendment and no review recorded one. The only rule in the neighbourhood is `reconcileServed`'s ordering" — a code comment. `03:770` then tells an implementer it "applies here too" and gives them nowhere to read it. State it in §3.3.1 or §3.3 and cite it.
13. **A77 is silent on `collectRoutes` in the no-active-revision case.** `03:770` says an Agent with no `status.activeRevision` has "its route taken as found"; the collection that A77 attaches to the re-point is left undefined for that path.

---

## The questions, answered

**(a) Is the ordering argument sound?** The *direction* is: `reconcileServed`'s invariant is "never move the `backendRef` while the policy has been changed out of band", and requiring `authPolicyIntact` before the write is a stronger test than `reconcileServed`'s own (which writes the policy and does not confirm it). I walked every stage order the loop can take and found no path that writes the route with the policy absent or wrong: entry at `ApplyingPolicies` (from `lockMissingPolicy`, or from a `!intact` rewind) flows to `Converging` in the same pass and re-points only after the policy write; `foreignAtOwnName` and a failing `ensureAuthPolicy` break or return before it; a `createTarget` that does not render returns before anything; `currentServingRoute` returning nil-or-unpublished displaces to `recreateRoute`; and abandonment cannot interleave, because `abandons` returns false for every re-creation (`isReCreation`). The `errRouteGone` edge is handled correctly: `ensureServingRoute` refuses to create a published API-key route and the caller re-prepares. Re-entering the `case` twice in one pass (Converging → ProbingAfter) makes the re-point run twice; `equalRoute` makes the second a no-op.
The *placement* is not sound as argued: at `Converging` the tuple has not held, the NACK ordering is unspecified, and the read is cached — BLOCKER 1. Moving the re-point to `ProbingAfter` only makes the argument true and costs nothing.

**(b) Does the rewind close the false-credit window?** For the sequence it describes, within one pass, yes: the re-point bumps the route's generation, `routeConverged` compares the parents' `observedGeneration` to it, and the pass breaks before probing. It closes it for the missing-policy `Lock` and, if the guard is written in the shared branch, for J2 and K2. It does **not** close: a lost or conflicted status write, a restart, or a route moved by someone else between passes (MAJOR 2); nor a route carrying more than one `backendRef` (MINOR 5). Gateway lag beyond the tuple is explicitly stated and not closed, which is honest. **Livelock**: I could not construct one. The tuple holds at a route that stops moving, and `ensureServingRoute` writes only on drift, so a stable `status.activeRevision` converges; a route that keeps moving is an Agent that keeps promoting, and the 4-minute deadline pages rather than spins. The 12-step bound in `runLock` catches a pass that oscillates.

**(c) What the `401` proves after a re-point.** The core argument is right, and I checked it in the renderer: the policy's `targetRefs` name the `HTTPRoute` by name, with no revision, so nothing about the policy depends on which revision the route names, and a credited `401` establishes the same thing before and after. The treatment of card-digest age is right in substance and over-stated in one clause (MINOR 3): the bound is on the **desired** revision, and `status.activeRevision` is the desired one only once a rollout has settled. And the direction of the change is not uniformly good — MAJOR 3.

**(d) The five owed tests.** (a), (c) and (e) can be built as described, and their named mutations compile and would be killed — (a)'s mutation is literally the shipped code, and (c)'s "re-point before the stage loop, as J2's is" is a real mutation with a real kill. (c) needs an unstated build order (MINOR 8). **(d) cannot be built as described** (MINOR 6) — and it is the only sub-case pinning the rewind. (b)'s mutation is too loosely stated to be known to kill (MINOR 7). And no case exists for the shipped J2/K2 hole at all (MAJOR 1). "No e2e is owed" is justified: what changes is *when* a route write happens, which the probe stub drives and a cluster cannot cheaply reproduce.

**(e) Cost and honesty.** The cost is identified in the right place — a revision that never carried traffic becomes reachable on the route — and the reason for accepting it (pinning to an older revision buys no closure while costing the promotion) is the same reason §3.3.3 already gives for not withdrawing the route, which is consistent. But the safety qualifier attached to it is false (BLOCKER 1), one cost is stated backwards (MINOR 4), and one cost is not stated at all (MAJOR 3). Rule 8: after A77 a terminal state is reported with a non-terminal message and no exit (MAJOR 4). Rule 7: "converged and enforcing" is a bound the code does not enforce, and `03:770`'s "no pass moves a backend … while the run namespace holds no `<agent>-auth`" is an absolute a cached read cannot support. The "specified, not implemented" discipline is otherwise good: every added sentence in §3.3.3, §5 and §8.1 carries the tag, and the Status line says it twice. The one place the tag is doing too much work is §5 and §3.3.3, where it is *also* carrying the residual wedge (MAJOR 4).

**(f) What A77 says about design 16.** Not editing design 16 is right, saying it owes a follow-up amendment is right, the two unbuildable envtest rows are right, and "what stays true" is right about Q13 as a record and about the routing row. The list misses the paragraph that constructs the apart case, Q12's reason, the dependency row, the exemption boundary and one sentence of "The wedge is design 03's"; it over-states "Unreachable" and "Both become exits"; and one quote is attributed to the wrong subsection. MAJOR 5.

**(g) Does A77 leave the wedge where it says?** Yes, for the case it names. With the route on `status.activeRevision` and no digest recorded for it, `cardAttributes` returns false and `beforeObserved` is never set for a re-creation `Lock` (no `ProbingBefore`), so the `Lock` cannot credit — the same stage, the same two conditions, the same page. And the apart case with a carded active revision is genuinely removed. What A77 does **not** say is that it also **adds** an apart case in the other direction (MAJOR 3), and that a `Lock` held at `ApplyingPolicies` keeps the old apart behaviour, and with it design 16's outage row (MAJOR 5).

---

## Where the work is right

I tried to break the mechanism and could not. Every load-bearing code claim checked out: the re-creation branch takes the route as found while J2's and K2's re-assert on `status.activeRevision` every pass; `cardAttributes` reads only the route's `backendRefs`; `authHoldsPromotion` returns non-empty for I1 alone, so `status.activeRevision` really does advance past a wedged `Lock`; `collectGarbage` really does protect it; the policy's `targetRefs` really do carry no revision; `abandons` really does exempt re-creations; and `errRouteGone` really is the route re-create's. The defect is real, terminal, and pages for as long as the Agent exists.

Two things are better than the amendment had to be. The first is the rewind rule itself — an author fixing only the wedge would have shipped a false-credit machine, and the drafter found it, named it, and put it where a critic reads it first. The second is that the drafter went looking in the shipped transaction and found the hole open there too; that is the most valuable sentence in the document, and it is what turns a wedge fix into a correctness finding against code that is already merged. The honesty of "What it does not fix", "It is stated, not closed", and the "not implemented" tag on every added sentence is the standard this corpus asks for.

The gap between that and a PASS is narrow in effort and wide in consequence: move the write one stage later, say J2 **and** K2, owe a case for them, key the guard on the tuple rather than on a change, and write the ADR.
