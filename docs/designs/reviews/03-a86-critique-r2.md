# Design 03 A86 — independent critique, round 2 (a86c2)

- **Target**: PR #69 at `94bcb1e`, on `main` `22badf1`. I reviewed it in the worktree `.claude/worktrees/a86-critique-r2`, detached at `94bcb1e`. The change is docs only: `git diff --stat 22badf1..94bcb1e -- internal cmd api test charts` is empty. I did not push, comment or merge.
- **Method**: I read the revised §9 D6 (:1538-1566), all of A86 (:1575-1842), and §5's new row (:1409) at `94bcb1e`. I checked every closure against the code at `22badf1`, not against the author's summary.
- **Verdict**: **REVISE — 0 BLOCKER, 4 MAJOR, 5 MINOR.** All five round-1 MAJORs are closed. The four new MAJORs are all in K6 and D6's table, which is the text the human will decide from.
- **Redaction**: the pattern of the owner-name scan is not reproduced here, because it spells a personal name. It is marked *[pattern redacted]*.

## Gates
- **`make docs`**: exit 0.
- **`make test`**: exit 0. fmt, vet, unit, docs, conformance, envtest (334s), chart and release all passed. Afterwards `git status` matched the snapshot taken before the run, and `git diff --quiet 94bcb1e` was true.
- **Path scan** (`/Users/`, `/private/`, `/home/`) over `git diff 22badf1..94bcb1e`: 1 hit. It is the committed round-1 record's own sentence naming the scan's patterns, not a path. Benign.
- **Owner-name scan** *[pattern redacted]*, case-insensitive: 0 hits.
- **Authorship**: both commits have author and committer `Quinyte Engineer <engineer@quinyte.com>`.
- **`reviews/03-a86-critique.md`** is byte-identical to my round-1 record once trailing whitespace is ignored (the `diff` is empty). Its redaction marker is intact.

## The five round-1 MAJORs — closed
- **M1, no selector on the `Served` pass. CLOSED** (:1632-1634, :1669).
  - The design now adds one live `GET` of `<agent>-auth` after `recordServed`, under the UID guard, and counts it in the cost.
  - Row (a′) (:1761) asserts the fresh `ApiKeySourceNeverObserved` message with `Ready=True` on the `Served` pass. Its mutation, skipping the `GET`, is one edit.
  - Row (k) (:1785-1790) asserts the fresh message and forbids both the held lead and the "no `<agent>-auth` … found" note.
  - I confirmed against the code: `recordServed` is reached from `runCreate` (`authtxn.go:1450`) and `runLock` (`:2253`), and neither fetches the policy. The new `GET` is therefore needed, and it is placed correctly.
- **M2, the `authKept` refusal could not fire. CLOSED** (:1747-1749, row (m) :1792).
  - `authKept` no longer compares the new fields.
  - The status write that sets them reads them back from the `Status().Update` response, as #54 does (`service.go:404-408`), and logs a pruning with a pointer to `charts/assayd/crds/`.
  - Row (m) now prunes the fields on every write from the first pass, which is the real `helm upgrade` case.
  - "An old CRD is never refused" is stated.
- **M3, notes that talk about the Gateway. CLOSED** (:1696-1708).
  - The half has its own `keySourceHeldMark`, three cause notes, and an errored note.
  - `carriedNote` is rewritten whole, and the new text is true for both sources.
  - Row (f) asserts "no Gateway note" and has a mutation that reuses `heldNote`.
- **M4, ADR-0034.** The false claim that A86 leaves ADR-0034 untouched is withdrawn, and the H2 conflict is now stated per option. **Closed as a finding, but one of the new per-option statements is false:** see new MAJOR 3.
- **M5, D6 unfair to K2. CLOSED as filed.** K2 now opens with its strongest case, and the NFR-8 misreading is gone.
  - **But my round-1 M5 was partly wrong, and the rebuilt table carries my error.** I said K2 "avoids paging every first install". It does not: under K2, `PolicyApplyIncomplete=True/ApiKeySourceEmpty` stands on every fresh install, and design 10's rule would page on it exactly as under K1. See new MAJOR 4.
- **All eleven MINORs are addressed in the text.** The precedence restatement (:1716-1724) is now correct: it argues only over the four reasons that can co-occur. The watch filter (:1734, row b‴) is sound; filtering on the prefix is only a cost bound, not a security boundary, and the design uses it only that way. The one-edit mutations hold, with one exception: MINOR 2 below.

## Round-2 findings

### MAJOR 1 — `ApiKeySourceNeverObserved` asserts something the operator cannot know, on paths it can recognise (rule 8)
- **Where**: :1554 (K6 cell), :1681, :1693, :1745, :1749.
- The design defends the reason's name on one path only: an Agent created after the keys were already lost, which really has never observed a key. The message says "this Agent has never observed a key". That is false on three other paths:
  - **An old CRD.** `keySourceObserved` is pruned on every write, so an Agent that saw keys every pass for months reads "never observed" the moment they are deleted. The operator KNOWS this is happening, because the read-back (M2's fix) detects the pruning on that same write.
  - **Status lost, then K2 `Lock`.** `refuseAdopt` and K2's `enterLock` build `status.auth` from `storedClaims` (`authtxn.go:1095-1098`). An Agent that observed keys before its status was lost comes back from K2 with `keySourceObserved=false`.
  - **Restore from backup without status.** This goes the same way, through `Adopt`.
- I checked the abandonment path, and it cannot wipe the flag on a served Agent. `abandons` excludes re-creations (`authtxn.go:757-760`), and the J2 arm (`:1017`) is only ever reached from mode `none`, which never observes.
- **Fix**:
  - Make the reason and the message say what the operator holds: "this Agent's status records no earlier reading that found a key" (for example, `ApiKeySourceNotRecorded`).
  - When the read-back has seen the CRD prune the field, raise `ApiKeySourceEmpty` rather than claim no observation. See MAJOR 2.
  - Add a row for the old CRD's message.

### MAJOR 2 — "On an old CRD … K6 falls back to K2" is false where it matters most
- **Where**: :1554, :1749, row (m) :1792.
- K2 raises `ApiKeySourceEmpty`. K6 on an old CRD raises `ApiKeySourceNeverObserved`.
- K6's stated purpose (:1554 "For it", :1672) is that design 10 excludes `NeverObserved` from paging.
- So on an old CRD, which is every install upgraded with `helm upgrade` until someone applies `crds/` by hand, a real lost key set under K6:
  - keeps `Ready=True`;
  - is excluded from paging;
  - carries a false message (MAJOR 1).
- That is strictly weaker than K2, which would page. The fallback is to a posture the D6 table does not list.
- **Fix**: on a CRD the read-back has seen prune the field, raise `ApiKeySourceEmpty` with `Ready` kept, which is K2 exactly. Then state that fallback and pin it in row (m).

### MAJOR 3 — K6's ADR-0034 cell is false: K6 also withholds `Ready` on a probe's own passing pass
- **Where**: :1554 ("Keeps H2 on the probe's pass, because a new Agent has observed nothing yet"), :1581-1582.
- The claim holds for a first `Create` only. A served Agent with `keySourceObserved=true` can pass a probe again:
  - a route re-create's `Create` (`recreateRoute`);
  - a missing-policy `Lock` (`lockMissingPolicy`).
- Each of these reaches `Served` on an anonymous `401` that an empty key source also produces. Under K6 that `Served` pass withholds `Ready` with `ApiKeySourceEmpty`.
- **Row (k) itself asserts `Ready=False` on the missing-policy `Lock`'s `Served` pass.** So the table tells the human K6 keeps H2 on the probe's pass, while the design's own test row pins the opposite. The same gap is in :1581 ("K1 on every `Create`" — `Lock`s too).
- **Fix**: say K6 contradicts H2 on the probe's pass for re-creations and missing-policy `Lock`s of an Agent that has observed keys, and scope the proposed amendment to that.
- **And cite the precedent.** ADR-0034 Amendment 5's W1 (lines 92-96) already makes a served, probed API-key Agent `Degraded` beside a Gateway-level policy, where an unprobed `auth: none` neighbour is not. So H2's "never worse off" was already narrowed without being named. Citing this tells the human how small the amendment is.

### MAJOR 4 — D6 charges the cost of paging on setup to K1 only; K2 pays it identically
- **Where**: :1552 (K1, "Against"), :1553 (K2, "For"/"Against"), :1564.
- K1's "Against" says fresh installs "will page once design 10 lands. That is a setup step read as an incident".
- K2 raises the same condition with the same reason on the same fresh install. Under design 10's rule as :1546 states it, K2 pages identically. Yet K2's "For" lists "Design 10 will page on it" and "It degrades no fresh install", and its "Against" omits the paging.
- :1564 repeats the asymmetry.
- Only K6, together with a design-10 exclusion, avoids paging on setup. That exclusion is itself a design-10 change, and it has MAJOR 2's cost.
- This came from my round-1 M5, which asserted the same wrong thing. I am correcting it here.
- **Fix**: move "pages on every fresh gateway-enabled install once design 10 lands" into K2's "Against" too. Say plainly that K1 and K2 differ only in `Ready` and H2, not in paging.

### MINOR
1. **The note taxonomy has no entry for a failed policy `GET`** (:1702-1706). On the `Served` pass, the new `GET` can fail. The three notes cover a LIST failure, an unreadable selector, and "no `<agent>-auth` … found". The last would be false for a `GET` error, which is rule 8. Add a fourth note, or route the error to the errored note.
2. **Row (b)'s mutation can survive** (:1762). On a steady-state pass of a served `auth: none` Agent, `reconcileServed` never calls `reassertServedPolicy` (`authtxn.go:1623` is gated on `apikey`). So with the `apikey` gate dropped there is still no `GET` and no `LIST`, and the row passes under the mutation. Build the row on a `none` `Create`'s `Served` pass, where only the gate prevents the new `GET`.
3. **The latch counts entries that authenticate nothing** (:1745). `keySourceObserved` is set by any PRESENT reading: a rejected entry, an entry with an empty value, or a `binaryData`-only entry. So an Agent that never had a working key can read "observed, then lost" and page as an incident. State it as a residue.
4. **"It is what an operator would want told"** (:1554, K6 "For it") is an unmeasured opinion inside a decision table. Cut it, or attribute it.
5. **"never sits beside A80's `erroredNote`"** (:1706) is ambiguous. On an errored pass where both halves hold, the composed `PolicyApplyIncomplete` message carries A80's fragment and this half's fragment, and each has its own errored note. Say "in this half's fragment" if that is the meaning.

## K6 soundness summary (the coordinator's questions)
- **Is per-Agent "observed" state sound?**
  - It is sound for re-creation after keys existed: the message is true for that Agent object.
  - Renaming or moving is delete-and-create, so it is the same case.
  - It is unsound on an old CRD, and after status loss followed by K2 or restore: MAJOR 1.
  - Nothing makes "never observed" read as "lost" except MINOR 3's latch on non-working entries.
- **Is failing toward "never observed" the safe direction?** Under NFR-8, yes: the report still surfaces on `PolicyApplyIncomplete`. But combined with the design-10 exclusion that motivates K6, it fails toward no page. On old CRDs, which are the default after `helm upgrade`, that is every real incident (MAJOR 2).
- **Is every K6 message true in both states?**
  - `ApiKeySourceEmpty`'s message is true: it claims only the current list.
  - `NeverObserved`'s message is false on the MAJOR 1 paths.
- **Are the ADR-0034 statements accurate?**
  - K1's: yes. It could also cite `Lock`s.
  - K6's: no (MAJOR 3).
  - K2 and K4: "keeps H2" is true.
- **Are K1, K2 and K6 presented evenly?** Not yet. Paging is mis-attributed (MAJOR 4), K6's old-CRD fallback is overstated (MAJOR 2), and K6's H2 cell is overstated (MAJOR 3). Every error favours K6 and K2 over K1. Fixed, the recommendation of K6 may still stand, but the human should see K6's real costs:
  - a latch that misreads on three paths;
  - a design-10 exclusion that silences the old-CRD incident;
  - an H2 amendment that also covers re-creations.
