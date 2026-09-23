# Design 03 A86 — the author's forked self-critique (NOT independent)

- **What this is**: the output of a forked `critique-design` pass, run by the **author** of A86 on the author's own first draft, on 2026-09-23. It is **not** an independent critique. AGENTS.md rule 4 says why that matters: self-review has never caught what the independent pass caught. The independent critique of A86 is still owed.
- **What was reviewed**: the first draft of A86 in the working tree. That was the uncommitted diff of `docs/designs/03-policy-compiler.md` against `main` `9d197ef`, with A85 read from its unmerged branch `d03-items-9-11`. **The line numbers below are that draft's**, and they do not match the committed text.
- **Verdict**: **REVISE — 1 BLOCKER, 6 MAJOR, 10 MINOR.** A86 as committed answers every finding. Its last bullet in §11 says where each answer is.
- **How it was filed**: the critic returned its report to the author's session, and the author wrote it here with only light formatting. It was filed after the fact because the coordinator asked for it: findings are files. **Redactions**: one local absolute path, marked `[redacted: local worktree path]`. Nothing else was removed.

---

**design 03 A86: REVISE.** 1 BLOCKER, 6 MAJOR, 10 MINOR. The main finding is that a routine operator upgrade silently switches the new report off.

I reviewed the working-tree diff in `[redacted: local worktree path]/docs/designs/03-policy-compiler.md` against the code at `9d197ef` and A85's branch. In that draft, A86 is at lines 1553–1621, the §5 row at 1407, and §9 D6 at 1535–1544. `go test ./test/docs/...` passes.

I did not re-run the drafter's uncommitted envtest row. I confirmed its central finding by reading the code instead: nothing in `internal/controller` reads `APIKeySourceLabel`, and `SetupWithManager` watches no ConfigMap.

## BLOCKER

**B1. After any upgrade that changes the policy's render, the report goes permanently silent, and a claim already standing can never clear** (1571, 1407, 1565).
- **The gate:** A86 treats the key source as UNKNOWN whenever the policy check leaves the policy out, and that includes "does not render to `status.auth.appliedDigest`".
- **What triggers it:** `reassertServedPolicy` returns nil whenever this build's render differs from the recorded digest (`internal/controller/authtxn.go:2415-2421`). §3.3.3 (line 835) says that every upgrade which changes `compiler.AuthPolicy` causes this, on every pass, permanently. Nothing moves `appliedDigest` afterwards.
- **Silence:** after such an upgrade, no served Agent ever raises `ApiKeySourceEmpty`. The NFR-8 outage A86 exists to close is back, fleet-wide.
- **Stuck Degraded:** an Agent whose claim was standing at upgrade time stays `Ready=False`/`Degraded` forever, even after its keys are restored.
- **No reason given:** the digest check compares one render against another, and says nothing about which selector is live. So §5's "on every served API-key Agent in the run namespace" claims more than the code would do (rule 7).
- **Fix, one of:**
  - drop the digest clause from the key-source gate. Keep "exists and carries this Agent's UID", or read the `configMapSelector` from the live object, which `reassertServedPolicy` has already fetched;
  - or keep the gate, state the permanent hole and the permanent hold in §5, in D6 and in "What A86 does not do", and give the hold a way out.

## MAJOR

**M1. The `authKept` claim is false** (1594).
- A86 says a CRD that prunes `keySourceEmpty` is "refused… the first pass that raises the claim".
- That raising pass is a served Agent with an empty slot, which never calls `persistStatus`. Case 19 (o) says so in terms (line 1492). `authKept` is called only at `authtxn.go:1128-1146`.
- So on an old CRD the flag is pruned silently, and holds degrade silently. That is exactly the failure A81 guarded against.
- The new comparison can fire only on a later write that enters a transaction, and no case-20 row pins it (rule 5).
- **Fix:** state the real reach and add a pruning row like 19 (o), or drop the comparison.

**M2. §3.3.3 is not updated, but A86 rewrites what that section calls a "complete instruction"** (line 880).
- A86 adds a third writer, a live read and a watch. That contradicts "No new object, no new watch, no new requeue, and no new read" (line 893 and `authserved.go:30`).
- It also changes the claim store (three flags), the carry set, `incompleteOrder`, and the rule that the `Served` pass is not judged.
- A86 says "§3.3.3's held table gains that clause" (1582), but the draft does not add it, and "Body changed" (1620) leaves §3.3.3 out.
- The body is authoritative. After approval, an implementer would build the old behaviour.
- **Fix:** write the §3.3.3 changes into the body, marked proposed.

**M3. The mutation named for case 20 (b) cannot fail it** (1597).
- The mutation is "gate the read on `mode` being set rather than `apikey`". That changes nothing observable, because a served `none` Agent's `keySource` is empty (`servedRecord`, `authtxn.go:1557-1560`; the J2 abandonment writes `Mode` only).
- A86's own parse rule reads an empty `keySource` as UNKNOWN, so under the mutation the `none` Agent still raises nothing.
- **Fix:** mutate both guards — drop the mode gate and derive the selector from `compiler.KeySource(runNS)` — or delete one of the guards as unpinnable.

**M4. The watch is unpinned** (1592, 1597).
- Case 20 owes only a unit row over the map function, and every envtest row calls reconcile directly.
- So the suite stays green if the source is never registered, if ConfigMap updates are filtered with a generation predicate, or if the informer caches full objects instead of metadata.
- The repo pins every other watch with a manager-driven row: `test/envtest/gatewaywiring_test.go:516, 636, 812, 856`.
- **Fix:** add manager-driven rows for this watch in the same style, with mutations for a dropped source and for a full-object informer.

**M5. The held message is incoherent** (1581).
- "Rebuilt from the report's own text" would restate "a live list found none there" next to a note saying the key source was not re-read.
- The empty-case variant names ConfigMaps, and a claim store of one boolean cannot reconstruct those names.
- This is the exact defect A83 fixed for the policy half.
- **Fix:** give the held report its own lead, one that names no clause.

**M6. D6 leaves out the state the rule will fire in most often** (1539).
- assayd writes no keys, and the default Agent asks for `apikey`. Under K1, every Agent in a namespace whose administrator has not yet written a key set goes `Degraded` one pass after `Served`, and it will page once design 10 lands.
- The shared fixture `servedAPIKeyAgent` (`test/envtest/authcreate_test.go:386`) writes no key set. Every case-19 row that asserts `Ready=True` or "no condition raised" would change meaning.
- **Fix:** add this to the cost cells of K1 and K2, and to case 20's notes on the fixture.

## MINOR

1. **Contradictory message** (1577). "Nothing is withdrawn, because callers are already refused" is unconditional. It undoes the conditional 401 that A86 argues for: beside an unattached policy, the route may answer 200.
2. **Admission citation misattributed** (1555).
   - The quoted "DELETE is not reserved" comes from the comment on `assayd-gateway-policies` (`charts/assayd/templates/admission.yaml:197-200`). The reasons it gives are RBAC, and the namespace controller's own deletes at teardown.
   - The reason about the garbage collector's finalizer belongs to admitting UPDATEs (lines 111 and 231). `assayd-api-keys` says nothing about DELETE.
3. **Wrong model for the map function** (1592).
   - "As `nackToAgents` maps a NACK" is wrong: `nackToAgents` reads each policy live (`gatewaywiring.go:264`).
   - Reading policies from the cache breaks that cache's stated invariant, "Nothing reads policies from this cache but the watch" (`gatewaywiring.go:196-199`). A86 must update that comment.
4. **Metadata-only does not keep hashes out of memory** (1592). A key set written with client-side `kubectl apply`, as this repo's conformance does (`test/conformance/cluster_test.go:33`), carries its data in the `last-applied-configuration` annotation. Specify a cache transform that strips annotations and `managedFields`, or drop the claim.
5. **Wrong carried note** (1583).
   - On an early return, `carriedNote` says "did not read the Gateway again" (`authtxn.go:521`). That is the wrong account of a claim derived from a ConfigMap.
   - A86 also never mentions `carryGatewayReport` (`agent_controller.go:1496`), which carries `PolicyApplyIncomplete` whole on five exits.
6. **Case 20 (d) under-specifies its fixture** (1599). The mutation survives unless the rejected entry lacks `keyHash`. Specify the `{"key": …}` shape from A82's row 10.
7. **D6 table problems** (1541, 1535, 1544).
   - K3 says withdrawal "hands anyone… a switch". That is not true here: the delete itself already refuses every caller.
   - K3 says withdrawal "changes nothing for callers". That is wrong: a prepared route answers 500, not 401.
   - The original D6 option to re-probe was dropped without a reason (the operator holds no key).
   - The residue is unstated: keys that are present but that agentgateway does not load go undetected.
   - The old "No recommendation" sentence still sits beside the new recommendation.
8. **The hold on the `Served` pass** (1594). It is new code with no stated placement and no stated note. It is also unnecessary: that pass can simply run the LIST.
9. **Missing test and bound details.**
   - Say that the literal list in `TestSeedAndWithdrawAreTheSameSet` gains the new reason (`authserved_unit_test.go:342`).
   - Bound how many names the message's `<names>` list can hold.
   - The empty-key-set derivation (1590) should cite the measured rows 2 and 3.
10. **Housekeeping.**
    - Line 1621 points to a self-critique bullet "below" that does not exist.
    - The new `status.auth` field adds API to an approved slice, which D5(b) treated as a cost for the human to weigh. Surface it in D6.

## What holds up

I tried to break these and could not:
- **Detection:** the reproduction's headline finding is correct. No RBAC change is needed: the ClusterRole already grants cluster-wide `configmaps` list and watch.
- **Report semantics:**
  - Counting entries rather than parsing them correctly leaves rejected entries to A83's lead, as measured in A82 rows 10 and 14.
  - A failed LIST is correctly treated as unknown, never as empty.
- **Condition handling:**
  - Ranking the new reason last in `incompleteOrder` is argued correctly.
  - Not writing `GovernanceSkipped`, not even a note, is consistent with `holdGovernance` and `noteGovernance`.
  - `recordServed` does drop the flags today (`authtxn.go:1581`), so the carry is needed.
- **The call itself:** the K1 recommendation is sound once B1 and M6 are fixed.
