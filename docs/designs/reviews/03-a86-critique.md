# Design 03 A86 — independent critique, round 1 (a86c1)

- **Target**: PR #69, head `3ddfb34`, on `main` `22badf1`. Worktree `.claude/worktrees/a86-critique`, detached at `3ddfb34` (docs-only diff; `git diff --stat 22badf1 3ddfb34 -- internal cmd api test charts` is empty, so the code is `22badf1`'s).
- **Method**: `/critique-design`, forked, plus my own checks on the nine questions the coordinator put, and the gates below. I did not push, comment or merge.
- **Verdict**: **REVISE — 0 BLOCKER, 5 MAJOR, 11 MINOR.**
- **Redaction**: the owner-name scan pattern is not reproduced here, because it spells a personal name. It is marked *[pattern redacted]* where it is used.

## Gates
- Reproduction: I copied the author's `kout_repro_test.go` (sha256 `51354df6…a87059`, the same before and after) into `test/envtest/zz_a86c1_repro_test.go` and ran `go test ./test/envtest/ -run TestKoutReproduceTheSilentKeySourceOutage`. **PASS, and the defect reproduced**:
  - phase `Ready`, `Ready=True/Available`, `PolicyApplyIncomplete` absent, and `GovernanceSkipped=False/AuthVerifiedOnOneReplica`, in all three states: key CM present, deleted, and re-created empty;
  - **15 ConfigMap reads over 5 passes, and none selected by `assayd.dev/api-keys`**: each pass did one `get` of the binding record and two `list`s by `assayd.dev/agent-uid`.
  - Static check: `APIKeySourceLabel` is referenced only in `internal/compiler/auth.go` (the render and `KeySource`), never in `internal/controller`. The file was removed afterwards, `git status` matches the pre-run snapshot, and `git diff --quiet 3ddfb34` is true.
  - The wrappers cover `r.Client` and `r.Reader`, which is every reader `reader()` returns (`runnamespace.go:847`).
- `make docs`: exit 0.
- `make test`: exit 0 (fmt, vet, unit, docs, conformance, envtest 335s, chart, release all ok). Output in `a86c1-make-test.txt`.
- Path scan (`/Users/`, `/private/`, `/home/`) over `git diff 22badf1..3ddfb34`: 0 hits. Owner-name scan *[pattern redacted]*, case-insensitive: 0 hits.
- Authorship: author and committer are `Quinyte Engineer <engineer@quinyte.com>`, with the co-author and session trailers.
- Self-critique `reviews/03-a86-selfcritique.md`: its title and "What this is" say it is the author's forked self-critique and NOT independent. Its one redaction, a local path, is marked `[redacted: local worktree path]`. **Honest.**

## Findings

### MAJOR 1 — On the pass that reaches `Served`, nothing gives the read its selector
- **Where**: design 03 :1581, :1585, :1603, :1702.
- A86's gate reads the key source on the pass where the slot empties, and the design names `reassertServedPolicy`'s owed second return as the only source of the live selector. That function is reached only from `reconcileServed` (`authtxn.go:1646`), and `authStep` reaches `reconcileServed` only when the slot is empty on the way in (`authtxn.go:803-820`).
- The `Served` pass runs `runCreate`→`recordServed` (`authtxn.go:1450`) or `runLock`→`recordServed` (`:2253`). Neither of those passes fetches the policy.
- Built as written, then, the `Served` pass has no policy object, which is A86's own UNKNOWN (:1594). Three consequences:
  - "A `Create` in a namespace with no keys … raises this on that same `Served` pass" (:1603, :1720, D6 K1) is false.
  - Row (k) passes anyway, on the hold. But the held `<why>` would then say "no policy of this Agent's was found" on the pass where the `Lock` has just written and verified it. That is rule 8.
  - The `Served` pass's cost goes uncounted.
- **Fix**:
  - Name the source for that pass: a live GET under the UID guard, with its cost, or the render's selector, since the transaction has just converged that digest.
  - Add a row: a `Create` with no keys raises the FRESH message on its `Served` pass.
  - Make (k) assert the fresh lead.

### MAJOR 2 — The `authKept` refusal that row (m) pins cannot fire on a real old-CRD install
- **Where**: :1672, :1704.
- `persistStatus` compares `status.Auth` with what comes back (`authtxn.go:1135`). But the flag is written after the step, and every later `persistStatus` starts from STORED status. On a CRD that prunes the flag, that stored value is always false, so want and got are both false and the check never refuses.
- Row (m) passes only because `claimPruningClient` is switched on after the flag has already been stored. That models a CRD downgrade, not "`helm upgrade` never updates `crds/`".
- #54 solved the same problem by reading the flag back from the `Status().Update` response right after the write, and logging the pruning (`service.go:404-408`).
- **Fix**: use #54's pattern in the write that sets the flag, or say plainly that an old CRD is never refused and only the hold is lost. Reword (m) as the downgrade it models.

### MAJOR 3 — The held report's note is unspecified, and every existing note is false for a key-source claim
- **Where**: :1622-1626, :1632, row (f) :1690.
- "that lead plus one note" never names the note. The notes that exist are `heldMark`/`heldNote` ("the Gateway has not reported since"), `unjudgedNote` and `erroredNote`, and each makes a Gateway claim about a fact read from a ConfigMap.
- The widened `carriedNote` still opens "carried as the last pass that read the Gateway stored it, at the generation it names" (`authtxn.go:521`). There is no generation for a ConfigMap LIST, so that clause is false for this reason.
- **Fix**: give this half its own marker and note text, for each `<why>`. Rewrite the whole `carriedNote`, not only its tail.

### MAJOR 4 — "A86 makes nothing in [ADR-0034] false" is false
- **Where**: :1557.
- ADR-0034 line 38 (H2): "a probe that passes on the replica it reached means `Ready=True` … A probed Agent is never worse off than an unprobed one."
- Under K1, a `Create` in a namespace with no keys passes its probe and reads `Ready=False`/`Degraded` on that same pass, while an unprobed `auth: none` Agent beside it reads `Ready`. That is the steady state of a new gateway-enabled install.
- ADR-0034 Amendment 3 (line 58) explicitly weighs "nothing proves that a key exists in the Agent's group", so the ADR did consider the no-key state.
- A80's precedent does not cover this, because A80 never raises on the probe's own pass.
- **Fix**: put the H2 conflict to the human as a cost of K1, and record an ADR-0034 amendment if K1 is taken.

### MAJOR 5 — D6 misstates K2, against NFR-8's own text (my finding; not in the fork)
- **Where**: D6 table, K2 row (:1545 area).
- The K2 cell says "`Ready=True` then is the silent path NFR-8 forbids". But NFR-8 (`docs/requirements.md:26`) says only: "any downgraded guarantee … surfaces as a CR condition, never silently".
- Under K2 the outage DOES surface, on `PolicyApplyIncomplete=True/ApiKeySourceEmpty`. By A86's own text (:1604), design 10 will page on that condition with no new rule. So K2 is not silent, and it breaches no NFR.
- K2's strongest case goes unstated:
  - it keeps H2 true (MAJOR 4);
  - it avoids paging every first install for an administrator's setup step;
  - it still surfaces and pages.
- K1 may still be right, because `Ready` is what most readers check. But the table has to let the human weigh that honestly. As written, it rules K2 out on a false ground.
- The middle option the forked pass raised (its MINOR 10) also belongs here: tell "never had keys" apart from "lost its keys".

### MINOR
1. **The precedence argument covers states that cannot occur** (:1640).
   - "An unranked reason, such as a deadline or a NACK, keeps its lead." Both are raised only on a Create's WAITING pass (`authtxn.go:1471-1491`), and the slot is non-empty as that pass leaves it. So neither can coexist with this half, whose gate is the slot empty as the pass leaves it.
   - Where an unranked reason did coexist, it would keep its lead only because it was written first. `raiseIncomplete` keeps whichever unranked reason is already set (`authtxn.go:601-612`), and `withholdInOrder` never displaces an unranked withhold. So "keeps its lead" describes write order, not rank.
   - `AuthPolicyMissing` is withdrawn on a `Lock`'s `Served` pass (`authtxn.go:314-317`), so ranking against it is also moot on the one pass that could meet it.
   - The ranking of the four reachable reasons, last, is right. I checked each: foreign or Gateway-level policies may widen the route, a refused route means nothing reaches the agent, and a broken policy may not enforce.
2. **Row (b)'s mutation is two edits**, against :1673's "one compiling edit" rule. So the `apikey` gate is not pinned on its own.
3. **The five-minute bound is false.** `cardRequeue` is set only `if ready` (`agent_controller.go:852-854`), so a failing LIST is not re-tried within any stated bound.
4. **The watch is cluster-wide.** It fires on labelled ConfigMaps in namespaces that are not run namespaces, where the label is not reserved, and each such event makes a live policy LIST. Drop events outside `RunNamespacePrefix` first, on `nackToAgents`' precedent (`gatewaywiring.go:281-285`). There is no hot loop: status writes produce no ConfigMap events, and there is no resync.
5. **The metadata transform is unpinned by row (b′).** Add a row that writes a key CM carrying `last-applied-configuration` and asserts the cached object lacks it. The unit row also needs two Agents.
6. **The fixture change reaches further than stated.** The J2/K2 `Lock` tests and direct `driveToServed` callers reach `Served` with no keys as well, and their `Ready=True` asserts would flip.
7. **The gate has no `Gateway.Enabled` clause.** A80 guards its gate this way (`authserved.go:622, 759`).
8. **The LIST's cost is understated.** It returns every key entry's data (uncached, full objects) on every pass of every served API-key Agent, N times over per key-set event. A `PartialObjectMetadata` list cannot count entries, so full objects are needed; state that.
9. **A LIST that keeps failing is silent.** A75's precedent of a report that does not page (`authtxn.go:390, 422-424`) fits this better than A80's.
10. **The message hard-codes agentgateway's entry format.** Pin it to 1.5.0's docs. context7 `/websites/agentgateway_dev` confirms the one-entry-per-data-key `{"keyHash":"sha256:…","metadata":{…}}` shape.
11. **K1's cost cell says "every new install"**, but `gateway.enabled` defaults to false (`charts/assayd/values.yaml:93-95`, "what P1 ships"). It is every new *gateway-enabled* install, and only its API-key Agents (every Agent without `expose`).

## Unknown vs absent — paths checked
- **RBAC**: the generated ClusterRole grants `configmaps` get/list/watch cluster-wide (`agent_controller.go:336`, `config/rbac/role.yaml:10`, chart copies `files/operator-rules.yaml`). A LIST denied by RBAC is an error, which is UNKNOWN, not empty. Correct as specified. The watch needs no new verb.
- **Cache race**: ConfigMaps are `DisableFor` (`cmd/operator/main.go:155`), so the LIST is live. Policies are unstructured, so they are uncached by default and the live-policy read is live too. No informer race can make an absent answer false.
- **Several CMs, one empty and one not**: PRESENT. Correct.
- **`binaryData`-only**: counted PRESENT, and A86 marks it as a residue. Correct direction.
- **Other namespaces**: the LIST is scoped to the policy's namespace (A74). Correct.
- **Selector the operator cannot evaluate**: UNKNOWN. Correct.
- **A CM with `deletionTimestamp`**: counted PRESENT. Conservative.
- **An entry with an empty value (`data: {k: ""}`)**: PRESENT here. Agentgateway presumably rejects it into `PartiallyValid`, which is the policy half's report. Not verified; covered by the rejected-entry residue.
- **No path found where "empty" is reported while the truth is unknown**, except MAJOR 1's inverse: an UNKNOWN reported under a false `<why>`.

## What holds
- The defect is real, and I reproduced it independently.
- K1 is a sound posture (it withdraws nothing), and consistent with C1 and D7.
- Counting entries rather than parsing them is right.
- `ApiKeySourceEmpty` last in `incompleteOrder` is right for every reason that can co-occur.
- No `GovernanceSkipped` write, and no note, is right.
- The metadata-only watch can be built: controller-runtime has `ByObject.Transform`.
- K3 and K5 are fairly rejected.
- The self-critique record is honestly labelled and redacted.
