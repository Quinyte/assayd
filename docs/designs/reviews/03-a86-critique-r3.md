# Design 03 A86 — independent critique, round 3 (a86c3)

- **Target**: PR #69 at `3484e09`, on `main` `22badf1`. I reviewed it in the worktree `.claude/worktrees/a86-critique-r3`, detached at `3484e09`. The change is docs only: `git diff --stat 22badf1..3484e09 -- internal cmd api test charts` is empty. I did not push, comment or merge.
- **Method**: I read the revised §9 D6 (:1538-1564), A86's changed bullets, and ADR-0034's H2 (line 38) and Amendment 5 (lines 80-106). I checked each claim against the code at `22badf1`. I also **measured** the access that the K6 schema read depends on, in envtest (below).
- **Verdict**: **REVISE — 0 BLOCKER, 2 MAJOR, 4 MINOR.** Both MAJORs are wording fixes in the text the human decides from. Neither needs a new mechanism. All four round-2 MAJORs and five MINORs are closed.
- **Redaction**: the pattern of the owner-name scan is not reproduced here, because it spells a personal name. It is marked *[pattern redacted]*.

## Gates
- **`make docs`**: exit 0. **`make test`**: exit 0, with fmt, vet, unit, docs, conformance, envtest (335s), chart and release all passing. Afterwards `git status` matched the snapshot taken before the run, and `git diff --quiet 3484e09` was true.
- **Path scan** (`/Users/`, `/private/`, `/home/`) over `git diff 22badf1..3484e09`: 2 hits. Both are the committed review records' own sentences naming the scan's patterns. Benign.
- **Owner-name scan** *[pattern redacted]*, case-insensitive: 0 hits.
- **Authorship**: all three commits (`3ddfb34`, `94bcb1e`, `3484e09`) have author and committer `Quinyte Engineer <engineer@quinyte.com>`.
- **`reviews/03-a86-critique-r2.md`** matches my round-2 record verbatim once trailing whitespace is ignored.

## Measurement: can the operator's service account read the OpenAPI v3 schema? (question 1)
- **What I ran**: a scratch test, `a86c3-sar/zz_a86c3_openapi_test.go` (sha256 `a7dc4b3e…d2ef1bb16`), in `test/envtest`. The API server was kube-apiserver 1.36.2 from setup-envtest, running `--authorization-mode=RBAC` (controller-runtime v0.24.1's default) with default bootstrap RBAC. The test did two things:
  - two `SubjectAccessReview`s for `system:serviceaccount:assayd-system:assayd-operator`, in the groups `system:serviceaccounts` and `system:authenticated`, with **no RoleBinding**;
  - one GET, impersonating that identity.
- **Output**:
  - `SAR get /openapi/v3: allowed=true reason="RBAC: allowed by ClusterRoleBinding "system:discovery" of ClusterRole "system:discovery" to Group "system:authenticated""`
  - `SAR get /openapi/v3/apis/assayd.dev/v1alpha1: allowed=true` (same reason)
  - `impersonated GET ok: 132859 bytes; contains routeRefused=true policyUnattached=true keyRecorded=false`
- **Conclusion**:
  - **The access is real on default RBAC**: it comes from `system:discovery`, which is bound to `system:authenticated`. The chart needs no new grant.
  - The published schema does expose `status.auth`'s fields, so the presence test works in principle.
  - A cluster that has removed or hardened `system:discovery` makes the read fail, and the text sends a failed read to K2. That is the safe direction.
- **Restore**: the scratch test was removed afterwards. `git status` matched the snapshot taken before the run, and `git diff --quiet 3484e09` was true.
- **The design should cite this and drop "it is not measured here"** (MINOR 1).

## Round-2 findings — closed
- **MAJOR 1 (K6's reason claimed what the operator cannot know). CLOSED.**
  - The reason is now `ApiKeySourceNotRecorded`, with the flag `keyRecorded`.
  - Its message says only what status records, and "does not say whether keys ever existed".
  - That is true on all three paths: an old CRD, lost status followed by K2, and a restore without status.
- **MAJOR 2 (K6's old-CRD fallback). CLOSED in substance.**
  - A schema without the field, a failed read, or a read-back that shows pruning all lead to `ApiKeySourceEmpty` with `Ready` kept, which is K2 exactly.
  - The access it depends on is now measured (above).
  - MINORs 2 and 3 are about how the fallback is specified and pinned.
- **MAJOR 3 (K6's H2 cell). CLOSED.**
  - K6 now "contradicts H2 on the `Served` pass of a route re-create or a missing-policy `Lock` of an Agent whose status records a key". That is correct, and it agrees with row (k).
  - K1's cell now covers every `Create` and `Lock`.
  - One new inaccuracy about W1 is MAJOR 2 below.
- **MAJOR 4 (paging charged to K1 only). CLOSED.** :1547 says K1 and K2 page alike on a fresh install and "differ in `Ready` and in H2, and in nothing else". K2's "Against" now carries the paging too.
- **The five MINORs. CLOSED.**
  - There is a note for a failed `GET` (`; the live <agent>-auth could not be read (<error>)`), and "no `<agent>-auth` … found" is limited to NotFound or a foreign UID.
  - Row (b) is built on a `none` `Create`'s `Served` pass. I checked it: `recordServed` sets mode `none` there, so only the `apikey` gate prevents the new `GET`, and the mutation dies.
  - The latch residue is stated.
  - The opinion is cut.
  - The errored note is specified per fragment.

## Round-3 findings

### MAJOR 1 — The recommendation's headline, "K1 is true in every state, adds no state", is false as written
- **Where**: :1551 (K1 "For it": "Every message and every condition is true in every state"), :1562.
- **"True in every state."** Every condition K1 WRITES is true. But in the states A86 itself lists as residues, K1 leaves `Ready=True` standing while every caller gets `401` or `403`:
  - a `LIST` that fails on every pass (:1661, "the residue");
  - an out-of-band selector on an Agent whose digest no longer matches;
  - every entry rejected, if 1.5.0 reports `Valid` there (:1732, unmeasured);
  - a key set whose keys are all in other groups (:1807, "Group coverage");
  - keys held where agentgateway does not read them (:1806).

  These residues are common to K1, K2 and K6, so they do not change the ranking. But "true in every state" is the argument the recommendation rests on, and it is a universal claim this design has already falsified.
- **"Adds no state."** K1 adds `status.auth.keySourceEmpty`: API in an approved slice, which :1544 lists as shared. What K1 does not add is K6's *second* flag, the schema read and the design-10 exclusion.
- **Fix**: say "every condition K1 writes is true, and K1 has the silent residues every option shares (listed)". Say "adds no state beyond what every reporting option adds".

### MAJOR 2 — "A W1-sized H2 amendment, of the shape W1 already set" understates what K1 amends
- **Where**: :1551 (K1 ADR cell), :1562, :1587 and the ADR bullets above it.
- H2 has two sentences, and they are different claims:
  - (i) "a probe that passes on the replica it reached means `Ready=True`";
  - (ii) "A probed Agent is never worse off than an unprobed one".
- **W1 narrowed only (ii).**
  - Under Amendment 5, L1 holds every `Create` and `Lock` at `ProbingAfter` while a Gateway-level policy could have answered it. So no probe is ever *credited* while W1 would withhold `Ready`.
  - W1 applies only to "an Agent already `Served`" (ADR-0034 Amendment 5, line 92).
  - So no shipped rule makes `Ready=False` on the pass whose probe was credited.
- **K1 contradicts (i) directly**, on the `Served` pass of every `Create` and `Lock` with an empty key source. So does K6 on re-creations and on missing-policy `Lock`s. No amendment has touched (i) before.
- So the amendment is "W1-sized" only in being one named cause. Its shape is new: it is the first exception to sentence (i). The human should see that before choosing K1 over K2, because K2's one advantage is exactly that it keeps (i).
- **Fix**: state that K1 (and K6) amend H2's first sentence, which W1 did not, and cite W1 only as precedent for the second.

### MINOR
1. **"It is not measured here" is now measured** (:1760). Cite this record's envtest result: `system:discovery`, bound to `system:authenticated`, allows `get` on `/openapi/v3` and `/openapi/v3/apis/assayd.dev/v1alpha1` for a service account with no binding, on kube-apiserver 1.36.2 with default bootstrap RBAC. Keep the caveat that a cluster can remove or harden `system:discovery`, which the text already sends to K2.
2. **"Cached as the evaluation-suite detector caches its answer" imports the opposite failure policy** (:1760).
   - `EvalSuiteDetector.Installed` (`internal/controller/discovery.go:81-116`) resolves a failed lookup to the previous answer, or to `true` ("installed") when there is none. Read the same way here, that means "the CRD can store the field", and so K6's `NotRecorded` on an old CRD. That is the round-2 defect again.
   - The next sentence of the design says a failed read means K2, so the text contradicts itself.
   - **Fix**: cite the detector for its TTL only, and state this read's own failure policy: an error resolves to "cannot store", whatever was answered before.
3. **Row (m)'s K6 mutation, "ignore the schema answer", can survive.**
   - Row (m) builds on (a), which reads PRESENT before the delete. So `keyRecorded` is written, and the read-back sees it pruned. The read-back leg alone then yields `ApiKeySourceEmpty` with `Ready` kept, whether or not the schema is consulted.
   - Also, envtest's published schema is the CURRENT CRD, which carries `keyRecorded`, so the schema never answers "absent" unless the row stubs it.
   - **Fix**: give K6 its own row, where the key source is never PRESENT, the schema is stubbed to lack the field, and there is no read-back signal. Also say whether the read-back's "seen pruned" persists in process, and for how long.
4. **The read-back cannot detect the pruning of a field that was never written true.** `keyRecorded` false is omitted, so the read-back only fires after a PRESENT reading. The schema read covers this. State it, so no one relies on the read-back alone.

## Answers to the coordinator's four questions
1. **Is the schema read sound?**
   - The access works on default RBAC, as measured above.
   - A failed read fails safe to K2, per the text, but the cache precedent contradicts that (MINOR 2).
   - Staleness after `kubectl apply` of new CRDs is bounded by the cache TTL, and until it expires the Agent stays in K2, which is the safe direction.
   - A CRD downgrade within the TTL would briefly read "can store". The read-back covers that once a PRESENT reading is written.
2. **Is K1 "true in every state"?**
   - Not as written (MAJOR 1). It is true of what K1 writes.
   - The unknown paths (a failed LIST, a failed GET, a selector that is not `matchLabels`) write nothing and hold, correctly.
   - Entries that authenticate nothing read PRESENT, so K1 is silent there, which is an unstated residue for K1.
   - On an old CRD, K1 needs `keySourceEmpty` only for the hold. The report itself is re-derived statelessly on every pass, and on a pruning CRD the hold is lost, as :1756 states.
3. **Is D6 fair and even?**
   - Close. Paging, the H2 cells for re-creations, and K6's costs and residues are now even.
   - Two things remain, both favouring K1: MAJOR 1's universal claim, and MAJOR 2's "W1-sized", which hides that K1 is the first to amend H2's first sentence.
   - K2 is stated at its strongest, and its one real advantage is keeping exactly that first sentence.
4. **MINOR fixes, rows, one-edit mutations, and the per-fragment errored note**: all closed, except row (m)'s K6 mutation (MINOR 3).
