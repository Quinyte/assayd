# Design 02 A77: fourth independent code review (PR #54, head `2b66c3b`)

**Scope:** `git diff 2a8f525...2b66c3b`, focused on `2b66c3b`, the recorded-UID authority for the delete, plus the answers to round three.

**Verdict: PASS (APPROVE).** There is no BLOCKER and no MAJOR. There are six MINOR findings. Three of them are wording that overstates what the record proves. One is a disclosed residue that I measured, with a cheap fix that narrows it.

The recorded UID holds as the delete's authority. I could not get it to authorise deleting an object the operator neither created nor adopted. Every path to a matching UID goes through one of three things: the operator's own create, adoption of an addressable object that passes provenance, or a write to `agents/status`.

## Gates at `2b66c3b`

- `make test` exits 0 (envtest took 319.4 s).
- `make race` exits 0.
- `make verify` exits 0 ("generation is reproducible and committed").
- `make e2e` was **not run**. The author runs e2e in parallel, and this round's findings are all reachable in envtest.
- `pandoc -f gfm` renders design 02 at HEAD with 0 `<p>|` paragraphs.
- The path and name scan is clean over the patches and the messages of all seven commits. The only hit is the word "scratchpad" inside the filed round-3 record.
- All seven commits have `Quinyte Engineer <engineer@quinyte.com>` as both author and committer.

## Mutations: 16 run, 16 killed, 0 invalid, 0 survived

Each mutation was applied to a backup-restored copy and had to pass `go vet ./internal/controller/ ./test/envtest/`. It then ran against the envtest subset matching `Service|Refus|Replace|Forged|Unstamped|Owner|ActiveRevision|Wedge|Record|Vanish|Cooldown|Retention|Collision|Carry|Reject|Stale|Admission|Vouched|Bound|Unaddressable`. Each file was restored and checked by SHA-256, 16 of 16.

**The author's mutations, re-run:** R01, R03, R07, R08, R09, R11, R23 and R24. All were killed.

**New mutations against the UID logic:**

| # | Mutation | Result |
|---|---|---|
| N1 | Adopt before the headless check (records a headless object on first sight) | killed |
| N5 | `pruneServiceRecords` drops every record, including the active revision's | killed |
| N7 | `recordServiceUID` appends instead of replacing (a stale first match survives) | killed |
| N8 | No adoption on the already-equal branch | killed |
| N11 | `ours` additionally requires the stamp | killed |
| N12 | `ours` additionally requires the agent-uid label | killed |
| N13 | The replace records the OLD UID | killed |
| N14 | The not-recorded refusal reports `Unstamped` | killed |

## The attacks asked for

1. **Adoption window, forged addressable plant, then headless.** Reproduced (probe P2). The stated concession is true, but it is wider than its framing (MINOR 2).
2. **A record for revision A matched against revision B's Service.** Not reachable. The lookup is by `(revision, digest)` and the object read is `<agent>-<rev>`. UIDs are unique, so no other object can match.
3. **A collision on the 40-bit name with a different digest.** Refused. `TestARecordForAnotherDigestDoesNotAuthoriseTheDelete` pins it and R09 kills it. The write side keys by revision only (MINOR 6).
4. **A forged status record.** The chart grants `agents/status` only to the operator's ClusterRole (`charts/assayd/files/operator-rules.yaml:93`). A principal granted it by wildcard can forge a record, and `ours` then skips provenance entirely. The run namespace is per source namespace, so the blast radius is that tenant's own run namespace. Reasoned, not measured (MINOR 5).
5. **Someone else deletes and recreates at the name (different UID).** It is refused, never deleted, and the record is not overwritten. `TestAForgedStampOnAnotherUIDIsRefusedAndNeverDeleted`, both rows, pin this, and R07 and N1 kill it.
6. **A stale cached Agent whose record predates a replace** (probe P3, my own). There was no delete. The status write failed with Conflict, so nothing wrong was persisted.
7. **`collectGarbage` pruning a needed record.** The desired revision is always made active or candidate before GC, so it is protected, and N5 is killed. Retained non-desired revisions keep their records, which is what a revert needs.
8. **Label-strip wedge.** It heals. All three rows of `TestAPatchOnlyWedgeOnTheOperatorsOwnServiceIsReplaced` pass, and N12 kills the label row.

## MINOR

### 1. One status Conflict on the replace pass leaves the operator's own replacement unrecorded, and the wedge comes back

**Where:**

- The replace records the new UID in memory at `service.go:522`.
- It is persisted only by the pass's final `writeStatus` (`agent_controller.go:1096`), after the card fetch and the gateway step.
- The bare-error exits between the two are `:761`, `:767` and `:788`, plus a Conflict at `:1088` or `:1097`.

**State (probe P1):**

1. A served `auth: none` Agent, gateway on.
2. `headlessByPatchAlone`.
3. One pass through a client whose next `Agents.Status().Update` returns Conflict.
4. Three normal passes.
5. `headlessByPatchAlone` again, then five passes.

**Output:**

```
replace pass err: update status of …/p1conf: Operation cannot be fulfilled on agents.assayd.dev "p1conf"
u1=185fd48e… u2=b6b0e072… record=185fd48e… replacedAt=<nil>
after 3 normal passes: record=185fd48e… (live b6b0e072…) phase=Ready
after re-headless + 5 passes: live=b6b0e072… clusterIP="None" phase=Degraded ready=False/RevisionServiceNotRecorded route=true
```

That is the operator's own object, refused as not its own until a human deletes it, with the route still published. Design 02 §5 (`:484`) discloses exactly this, including "A Conflict on the pass's status write reaches it", so rule 7 is met. I reproduced it rather than found it.

It is still a destructive act ordered ahead of its record. The controller's own comment at `:1099` calls that "backwards".

**Fix (narrows it; it cannot close it):** persist the record right after the Create that produced the UID. Do it with a dedicated status write that, on Conflict, re-reads the Agent and re-applies only `revisionServices`, plus `serviceReplacedRevision/At` on the replace path. Then do the same after the first create. That shrinks the window from "any Conflict or error later in the pass" to "the process dies between Create and the status write". Pin it with P1's conflicting client.

### 2. The adoption window opens on every new revision, not only at migration, and the type docs say the record proves creation

**Where:**

- `api/v1alpha1/agent_types.go:637` says the UID is "the one fact about a Service that proves this operator created it".
- `:702` reads "one revision Service this operator created".
- `:712` reads "as the API server returned it on the create".
- The comment on `adoptServiceRecord` (`service.go:348-368`) says "It is the migration path".
- The commit message's "Migration:" paragraph has the same framing.

**State (probe P2):**

1. An existing, settled Agent.
2. Its image is edited.
3. Before the next pass, a Service is planted at the new revision's name. It carries this Agent's UID label and the new digest stamp (both computable from the spec), `selector: {app: attacker}` and port 9999.
4. One pass.
5. `headlessByPatchAlone`, then one pass.

**Output:** `plant=3d78c896… record(rev2)=3d78c896…`, then `after headless: … deleted-and-replaced=true`.

The plant was recorded and later deleted, on an Agent that has nothing to do with migration. The revision name is predictable from the spec, so every spec edit opens this window.

The harm is bounded, as §5 argues. Only an object carrying this Agent's UID label and this digest can be adopted, so what gets deleted is the planter's own object, and its selector and ports are converged first. So this is wording, not a hole. But the field doc states a guarantee (proof of creation) that an adopted entry does not have. That is rule 7.

**Fix:**

- Say "created or adopted" in the three type comments, and regenerate.
- Replace "It is the migration path" with "It runs on the first sight of any revision's Service without a record, which includes every new revision, not only an upgraded Agent".
- Make §5 `:484` say the same.

### 3. The `RevisionServiceNotRecorded` reference entry omits its likeliest operator-owned cause and one exit

**Where:** `internal/refgen/reasons.yaml:775-793`.

- The state lists two causes: no record because the Agent predates the field, or "the operator's Service was deleted and another put at the name". It omits MINOR 1's cause, where the object at the name IS the operator's own replacement and the record was lost with a status write. It also omits a first create whose status write was lost before the object went headless.
- The operator field says "It stands until a human deletes the object". A human can also repair it in place back to addressable, the reverse of the two patches. The next pass then converges it, and adopts it if there is no record. (Reasoned from `service.go:235-316`, not measured.)

**Fix:** add both causes and the in-place-repair exit.

### 4. `tables_test.go` misses a break in a table's last row, and would flag two legitimate shapes

**Measured through `orphanedTableRows` directly:**

- Against the round-3 revision of design 02 (`ec98e5d`, the rebased `ddaf805`), it flags `[478 1094 1113 1122 1123 1124 1128]`, so it does catch the real breakage.
- Planting a blank line inside the `RevisionServiceNotRecorded` row of HEAD's §5 fails `TestNoMarkdownTableIsBrokenByItsOwnLayout` at `02-agent-crd-operator.md:487`. The file was restored and hash-verified.
- **False negative:** `"| a | b |\n|---|---|\n| x | first\n\n  second |\n\nprose\n"` returns `[]`. A blank line in the LAST row of a table orphans a line that does not start with `|`, and nothing follows it to be flagged.
- **False positives:** a `~~~` fence containing pipe lines returns `[2 4]`, because only ` ``` ` fences are recognised. A top-level row indented 1 to 3 spaces returns `[4]`, although GFM strips up to three leading spaces and continues the table.

Neither false positive occurs in the tree today, since the gate passes.

**Fix:**

- Recognise `~~~` fences.
- Flag a non-row, non-blank line that follows a blank line inside a table when it contains `|` and the line above the blank was a row.
- Compare indents only when either side is 4 or more spaces, or inside a list container.

Add the last-row case to `TestTheTableCheckCatchesBothBreaks`.

### 5. Whoever can write `agents/status` can authorise a delete, and the design does not say so

With `ours` true, provenance is skipped entirely, so a forged record deletes a headless object at `<agent>-<rev>` whatever it carries.

The chart grants `agents/status` only to the operator. A tenant given a wildcard Role over `assayd.dev` in their own namespace can write it. The effect stays inside that tenant's run namespace, and it needs a headless object at a name derived from that tenant's own spec. So it is not a cross-tenant hole.

Design §3.2 (`:245`) already treats `status` as "written only by this operator … under separate RBAC". **Fix:** state in §5's record row that the delete's authority is exactly as strong as the RBAC on `agents/status`.

### 6. "Adoption never overwrites a record" holds only per `(revision, digest)`

**Where:** `recordServiceUID` (`service.go:336-346`) replaces by revision alone, and `adoptServiceRecord` checks by revision and digest (`:374`).

**Consequence:** an adoption at digest D2 overwrites an existing entry for the same revision name at D1. That is reachable only through a 40-bit revision-name collision, so it is not a practical risk.

**Fix:** key the replacement by revision and digest too, or say "per revision and digest" in §5 and in the comment.

## Round-three findings: status

| # | Round-three finding | Status |
|---|---|---|
| 1 | `services/patch` wedge / forged-stamp delete | **Closed**, except the disclosed, measured residue (MINOR 1) |
| 2 | §5 table broken | **Closed.** pandoc shows 0 raw-pipe paragraphs, and the new gate fails on the planted break |
| 3 | Live read unpinned | **Closed.** `TestAStaleCachedReadDoesNotDeleteAnInPlaceRepair`; R11 killed |
| 4 | Unaddressable remedy says delete | **Closed.** The message says to revert or wait and "Leave the Service in place" |
| 5 | `ServiceRejected` update arm, requeue and carry | **Closed** per the author's R14 to R17. I did not re-run those four |
| 6 | Forbidden Get reported as `ServiceRejected` | **Closed.** `RevisionServiceUnreadable`; RBAC grants `get` on services (`operator-rules.yaml:24-35`) |
| 7 | Stale design text | **Closed** for every phrase round three quoted. None of "those five fields", "is never re-read", "five fields repaired", "Its carry rides" or the old test name remains |

## reasons.yaml

| Reason | Verdict |
|---|---|
| `RevisionServiceNotRecorded` | Everything it asserts about the operator is true. Its carries are via `carryGatewayReport`, which is a no-op with the gateway off. It returns before the gateway step and requeues at 1m. Its causes and exit are incomplete (MINOR 3) |
| `RevisionServiceUnreadable` | **True.** The `rejectedByAPIServer` filter matches the text; a transient read returns bare; the RBAC claim is true |
| `Unstamped` (rewritten) | **True.** `!stamped && !vouched` after the label check, reached only when `!ours` |
| `ServiceRejected` | **True,** including the honest note about RBAC Forbidden |
| `ForeignObject` | **True.** "absent label" is now included |
| `RevisionHashCollision` | **Corrected and true** |
| `RevisionServiceReplaceFailed` | **True.** All five stages match `service.go:483-537` |
| `RevisionServiceReplaceHeld` | **True.** It omits that it carries design 03's two conditions, which is an omission rather than an error |

## Probe files

These are in the scratchpad and none is committed:

- `pr54r4-probe_test.go.txt` (probes P1 to P3)
- `pr54r4-tableprobe_test.go.txt`
- `pr54r4-muts/*.json`
- `pr54r4-runmut.sh`
- `pr54r4-mutlog.txt`
- the gate logs `pr54r4-maketest.log`, `pr54r4-race.log` and `pr54r4-verify.log`
