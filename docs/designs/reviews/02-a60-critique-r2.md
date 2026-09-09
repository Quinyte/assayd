# Critique r2 — design 02 A60 · design 07 A5 · design 26 A1 · design 06 A3 (uncommitted, 2026-09-03)

- **Scope:** the same working-tree diff after the fixes from `reviews/02-a60-critique.md` were applied — `docs/designs/02-agent-crd-operator.md` (§3.2 binding block, §3.5, §3.7, §5, §8, A60), `07-umbrella-chart-ci.md` (A5), `26-tenant-cr.md` (A1, §3, §5), `06-identity-glue.md` (A3), `03-policy-compiler.md:153`, `README.md`
- **Method:** read cold against the prior critique; each of its 3 BLOCKERs and 5 MAJORs checked for a mechanism in the new text, not a mention; then the new text attacked on its own — the `Terminating` handler's step order, the nonce/UID division and its stated window, the `RoutesRevoked` signal, and present-tense claims. Repo facts checked in `cmd/operator/main.go`, `internal/controller/agent_controller.go`, `internal/controller/conditions.go`; cross-design claims checked in designs 03, 06, 16, 24, 26, 27 and `docs/research/tenant-namespace-primitives-2026-09.md`
- **Verdict:** **REVISE — 1 BLOCKER, 6 MAJOR, 12 MINOR.** All eight prior BLOCKER/MAJOR findings have a mechanism now; seven are closed. The `Terminating` handler that closes the old BLOCKER 3 introduces a new one: its confirming list undoes the recreated-tenant row two lines above it. The rest of the new findings are the protocol's remaining unfenced edges and two sentences that still assert what the text beside them contradicts.

## Closure against r1

| r1 finding | This pass |
|---|---|
| BLOCKER 1 — one label, two trust boundaries | **Closed.** `ClusterSPIFFEID` selects `assayd.dev/pods-by: agent-operator` with `tenant-sync DoesNotExist` (07:132-134); the listener keeps `run-namespace` (07:149-158, 03:153); 26 A1 point 1 (26:113) says a sync namespace never carries `pods-by`; 02:165 stamps both; 02:384 states the boundary. Label-sync on vCluster is marked unmeasured and the split holds either way. The chosen constant label rather than r1's `owned-by: <install-uid>` is equivalent — both need namespace `update` to forge |
| BLOCKER 2 — UID stored, never read | **Closed in the block, not in the row above it.** 02:159 divides the labour correctly; 02:170-171 compare the UID; the delete-and-recreate test is owed at 02:196. But 02:136 — the table row r1 asked to be rewritten — still titles the proof "a nonce the operator alone knew" and says the nonce is "compared on every reconcile", which 02:159 says it is not (MAJOR 5). And the "milliseconds" window at 02:159 is not milliseconds (MAJOR 4) |
| BLOCKER 3 — `Terminating` had no owner | **Closed for the crash it named, and the handler is wrong for a row it did not consider.** The handler (02:177-182) is idempotent, has three entry points, and a crash between teardown steps 2 and 3 now re-enters it. Its step 3 flips back to `Bound` whenever any Agent exists in `S` — including the Agent whose reconcile just moved the binding to `Terminating` because `S` was recreated (BLOCKER 1 below). Its step 4 is also not fenced against a concurrent flip-back (MAJOR 2) |
| MAJOR 1 — hard-mode admin bypass unstated | **Closed.** `GatesTenantAdminBypassable` in 26 §3 (26:32), explained at 26:109. Design 27, the named consumer, is not amended (MINOR 7) |
| MAJOR 2 — two finalizers, no order | **Closed as an order; the grant it names is wrong.** Compiler drains and revokes, patches `RoutesRevoked=True`, tenant operator waits (26:116). "The one tenant-side write the compiler holds" is false by R3-a's own text (MAJOR 6) |
| MAJOR 3 — per-tenant SPIRE unmeasured | **Closed.** 26:115 and 06:106 both record the `pod-uid` selector doubt, owe the spike, name the fallbacks, and forbid building on it |
| MAJOR 4 — no tests owed | **Closed.** 02:196 and 02:460 name per-row, per-crash-point and cluster cases. One of r8's five is dropped and the list is misattributed (MINOR 4) |
| MAJOR 5 — A5.5 named an observable that cannot fire | **Closed and verified.** `WorkloadNotAvailable` is the reason at `agent_controller.go:341` for a revision with no replicas yet; `WorkloadUnavailable` (`:335`) is the Degraded form for an *active* revision — A5.5's sentence is right for the first-revision case it describes. The `FailedCreate` gap is stated as an NFR-8 gap and owed |
| MINOR 1–10 | All ten closed as r1 asked: annotation (02:156), flag in future tense (02:150), namespaces write verbs bounded (07:192), `pods`/`pods/log`/`events` row (07:193), mirrors before `Bound` (02:165), mirror names hashed (02:194), verb-pinning test owed (07:145, 02:460), lost-binding recovery (02:192), `NOTES.txt` in future tense (02:142), Namespace watch (02:177) |

---

## BLOCKER 1 — The `Terminating` handler re-adopts the recreated tenant's old run namespace, or livelocks trying not to

**Files:** `docs/designs/02-agent-crd-operator.md:173,181,423`

Row 02:173: binding's `sourceNamespaceUID` ≠ the current source namespace's UID → "the tenant was deleted and recreated; the old run namespace is a different tenant's. The binding is compare-and-swapped to `Terminating` and the `Terminating` handler below takes over; the Agent waits with `RunNamespaceUnavailable=Terminating`."

Handler step 3 (02:181): "live-list Agents in `S` through the uncached API reader, excluding any with a deletion timestamp. **Non-empty** → compare-and-swap back to `Bound` and proceed as `Bound`: someone arrived, the namespace stays."

`S` is a name. The Agent that triggered row 02:173 lives in `S`, is not deleting, and is in that list.

**Failure scenario:** `team-a` (UID u1) had run namespace `R`, bound with `sourceNamespaceUID: u1`, holding u1's RoleBindings and Secret copies. `team-a` is deleted with the Agent finalizers bypassed — the case the row exists for — and recreated as u2 by a different team. Their first Agent `Y` reconciles: row 02:173 fires, CAS to `Terminating`, the handler runs in the same reconcile. Step 1: `R` present. Step 2: no deletion timestamp. Step 3: list Agents in `team-a` → `Y` → CAS back to `Bound` → "proceed as `Bound`". Row 02:170: `R`'s UID matches `runNamespaceUID` → "reconciles labels and mirrors; proceeds" — `Y`'s copies and Deployment are written into u1's namespace, under u1's RoleBindings. That is the refusal A44 wrote at 02:136 ("its RoleBindings were built for a different tenant's subjects"), reversed by the handler that was added to make A44 safe. The other reading — "proceed as `Bound`" re-enters the table from the top — hits 02:173 again and flaps `Terminating`↔`Bound` on every reconcile; §5's row at 02:423 promises the condition "clears by itself when the namespace is gone", and it never goes.

The row is reachable only when Agent finalizers did not run — a human removing them, or a `kubectl delete ns --force`-style recovery — which is exactly when the old namespace's contents are least trustworthy. r1 MAJOR 4's per-row envtest would have forced this to be written down; the row test is owed, so the design must carry the answer.

**Fix:** the handler's live-list is scoped by **UID**, not name: "Agents whose namespace's `metadata.uid` equals the binding's `sourceNamespaceUID`". When the source namespace was recreated that set is empty by construction, step 4 deletes `R`, step 1 deletes the binding, and `Y`'s next reconcile hits "no binding, no run namespace" and gets a fresh one. Say it in step 3 and in row 02:173 ("the handler's list is empty for a recreated tenant, so it deletes"). Add the row's envtest to 02:196 by name: source namespace recreated under a live `Bound` binding → old run namespace deleted, new one created, no write into the old one.

---

## MAJOR 1 — The state table's rows overlap and no precedence is stated; the wrong order pools two tenants

**File:** `docs/designs/02-agent-crd-operator.md:163-175`

Eleven rows are presented as "Reconcile finds → the operator does", as if disjoint. They are not. "Binding names a different source namespace for this run name" (02:172) and "`Bound`, namespace present, UID matches" (02:170) are both true at once for the second of two tenants whose names truncate-and-hash to one run name; so are 02:173 and 02:170. An implementer who evaluates state rows first gets, for the colliding tenant: `Bound`, present, UID matches → proceeds → writes tenant B's Secret copies into tenant A's run namespace. That is the non-injective pooling 02:133's Naming row calls "the exact property this scheme exists to avoid", and A32 in this same design already records the principle: two rules for one case is worse than either, because an implementer picks one.

**Fix:** state the order once, above the table: (1) run-name identity — 02:172, (2) source identity by UID — 02:173, (3) binding state rows. Or restructure the table as a decision list with those three tiers. Then the row test for 02:172 asserts nothing is written by the second tenant even when its binding state would otherwise say proceed.

## MAJOR 2 — Handler step 4's delete is not fenced against a concurrent flip-back

**Files:** `docs/designs/02-agent-crd-operator.md:181-182,190` · `internal/controller/agent_controller.go` (no `MaxConcurrentReconciles`; default 1)

02:190: "No state is reachable in which the delete is issued without a list having run after the swap." True, and insufficient: the list can be stale by the time the delete is issued, and the thing that makes it stale is the handler itself. Two runs of the handler — Agent `A`'s finalizer and Agent `B`'s first reconcile, or `A`'s finalizer and the Namespace-watch entry point — interleave as: `A` step 3 lists, empty. `B` is created. `B` reads `Terminating`, runs the handler, step 3 lists, finds itself, CAS `Terminating→Bound`, "proceed as `Bound`", writes copies and a Deployment. `A` step 4: "delete the run namespace, after checking its name has the `assayd-run-` shape and its UID matches the binding" — both still true — deletes it under `B`.

`B` recovers: its next reconcile sees `Bound` + absent (after the namespace finishes terminating) and re-mints. So this is an outage for `B`, not a loss, and it needs two handler runs in flight, which the code today (one worker, leader-elected) cannot produce. The design does not state one worker as a premise; 03:174 in the sibling design discusses raising `MaxConcurrentReconciles`, and the Namespace-watch entry point 02:177 adds is a second path into the handler whatever the worker count. A protocol whose safety depends on a concurrency setting it does not name is not the protocol 02:190 describes.

**Fix:** one more state. Step 3, empty → CAS `Terminating→Deleting` (fails if anyone flipped to `Bound` in between; the handler then proceeds as `Bound`). Only `Deleting` issues the delete, and **nothing flips back from `Deleting`** — a reader waits as in step 2. The flip-back and the commit-to-delete are then the same CAS on the same object, which is what closes the window; the shape-and-UID check stays as the belt. A crash after the CAS re-enters the handler, reads `Deleting`, and reissues the (idempotent) delete. Add the case to 02:196: two handler runs interleaved at step 3/4.

## MAJOR 3 — The finalizer excludes draining Agents from its count, so the last-but-one deletes the namespace under a drain

**Files:** `docs/designs/02-agent-crd-operator.md:186,181,396` (§3.7)

02:186: "Live-list Agents in `S`, excluding this one and excluding any with a deletion timestamp — an Agent being deleted is not a reason to keep the namespace; its own finalizer tolerates the namespace being gone." Handler step 3 (02:181) excludes deleting Agents the same way.

§3.7 (02:396) orders each finalizer as drain (weights 0, **respect `taskTimeout`**) → revoke → deactivate → GC → delete workloads → namespace question. So a deleting Agent `B` is, for the length of its `taskTimeout`, still serving in-flight tasks from Pods in the run namespace. Agent `A`'s finalizer, running concurrently, lists Agents in `S` excluding deleting ones → none → CAS → handler → same exclusion → deletes the namespace. `B`'s Pods die mid-drain. "Tolerates the namespace being gone" is true of `B`'s *delete* calls, which return `NotFound`; it is not true of `B`'s drain, and §3.7's "nothing is force-killed silently" is false whenever two Agents in one namespace are deleted within a `taskTimeout` of each other — a `kubectl delete -f dir/` is exactly that.

The exclusion exists to solve r8's "each sees the other and neither deletes". It solves it at the cost of the drain.

**Fix:** count what is still *running*, not what is still *declared*. "Last" = no non-deleting Agent in `S` **and** no workload carrying `assayd.dev/agent-uid` left in the run namespace (live-list Deployments/Sandboxes, uncached). A draining Agent's Deployment exists until its own finalizer's delete step, so `A` sees it and releases without deleting; `B`, whose delete step ran last, finds both lists empty and deletes. Two finalizers both past their workload-delete step both find empty lists, both CAS, one wins — the race r8 named still resolves. Apply the same predicate in handler step 3.

## MAJOR 4 — The "milliseconds" window is the crash-recovery interval, and it has a cheap bound

**Files:** `docs/designs/02-agent-crd-operator.md:159,168`

02:159: "The one window the UID does not cover is the milliseconds between the namespace `Create` returning and the binding recording its UID — a crash there leaves `Creating` with a nonce-stamped namespace, and a recreator who read the nonce and moved inside that window is bound. … it is the size of one API write."

Without a crash there is no attack: the operator holds the UID from the `Create` response and writes it next. The sentence's own attack requires the crash, and then the window is not one API write — it is the whole time the binding sits in `Creating`: the outage, the A4 rolling update (old Pod gone before the new one starts, by design), the crashloop. Row 02:168 — `Creating`, present, nonce matches → `Bound` — binds to whatever wears the nonce when the operator returns. The prior critique's BLOCKER 2 scenario (read nonce, delete, recreate with the nonce and a self-granting RoleBinding) works unchanged inside that interval, and the text quantifies it as milliseconds. A stated window is only honest if its size is.

**Fix:** state the real size, then bound it. Two checks on row 02:168, both from server-assigned fields so clock skew is irrelevant: (a) `namespace.metadata.creationTimestamp − binding.metadata.creationTimestamp ≤ bound` (seconds — the genuine namespace was created within one reconcile of the binding; a recreation during an outage is later); (b) the namespace is **pristine**: no Roles, RoleBindings or Pods in it. Either failing → `NotCreatedByOperator`. The attacker's goal is a RoleBinding in a namespace the operator writes into; (b) denies that at bind time and (a) denies a recreate that beat the operator back by more than the bound. What remains — a recreate inside `bound` seconds that adds its RoleBinding after the bind — needs `rolebindings create` in the new namespace, which namespace `create` does not confer; say so, so the tier in scope is named.

## MAJOR 5 — 02:136 still says the nonce is the proof and is compared on every reconcile; 02:159 says the opposite

**Files:** `docs/designs/02-agent-crd-operator.md:136,421,192`

r1 BLOCKER 2's fix: "Rewrite 02:136's header accordingly." The row is now titled "…and the proof is a nonce the operator alone knew (A60)" and ends "a 128-bit nonce … stamped on the namespace at creation, and compared on every reconcile. A pre-creator cannot know it." 02:159, twenty-three lines later: "the nonce is consulted only while the binding is `Creating`, and from `Bound` onward every reconcile compares the live namespace's `metadata.uid`." The body is authoritative (02:3) and says two things. §5's row at 02:421 names only "its nonce disagrees with the binding" as the `NotCreatedByOperator` trigger and omits the UID, which is the check that fires for every attack after the first reconcile. 02:192 has the human re-bind "from the namespace's own `metadata.uid` and `assayd.dev/binding-nonce`, in `Bound`" — copying a value `Bound` never reads.

**Fix:** retitle 02:136 "…and the proof is the binding record (A60)"; replace its last two sentences with the two-proof sentence from 02:159. 02:421: "no binding, nonce mismatch while `Creating`, or UID mismatch while `Bound`". 02:192: the human supplies the UID; the nonce is not needed and saying so tells the reader the model is understood.

## MAJOR 6 — "The one tenant-side write the compiler holds" is false by R3-a, and the write R3-a implies is spec-write

**Files:** `docs/designs/26-tenant-cr.md:116,70,85` · `internal/controller/conditions.go:39-44`

26:116: the compiler patches `RoutesRevoked=True` onto the tenant-side Agent's status — "the one tenant-side write the compiler holds, `agents/status` `patch` in the vCluster, named here as the widening of §7's 'read only' that it is."

R3-a (26:70): "a **finalizer on the tenant-side CR** blocks removal until host cleanup confirms." A finalizer is `metadata.finalizers` on the Agent, not its status. If the compiler places it, the compiler holds `agents` `patch`/`update` in every vCluster — and CRDs have no `finalizers` subresource, so that verb reaches `spec`. §7 (26:85): "the host-side policy compiler holds cross-tenant *read* on tenant CRs plus host gateway write — never tenant-workload write (R2-1), so a compromised compiler cannot create workloads in any tenant." With `agents patch` it can set any tenant's `spec.image`, and the tenant's own operator will create the workload for it. The claim fails transitively. This was latent in the approved text; A1 restates the grant set as minimal and makes it load-bearing.

Two smaller things in the same paragraph. "If the compiler is gone, the tenant-side finalizer waits **and says so**" — with which condition and reason? None is named; NFR-8 wants the name. And `conditions.go:39-44` clears any type in `ownedTypes` that a pass did not assert; if the tenant operator lists `RoutesRevoked` as owned, it erases the compiler's signal on its next status write.

**Fix:** A1 already contains the better mechanism: the tenant operator's finalizer waits on `RoutesRevoked`, which *is* "blocks removal until host cleanup confirms". So R3-a's finalizer is the **tenant operator's**, the compiler places none, and `agents/status` `patch` becomes the only tenant-side verb — true as written once said. Amend 26:70 to name the placer. Name the waiting condition (`RoutesRevoked=False, reason: CompilerUnreachable` or equivalent, on the tenant-side Agent). State that `RoutesRevoked` is a foreign condition to the tenant operator — never in its owned set. Note that `agents/status` still lets the compiler write `activeRevision` and every other status field the operator treats as state (02:412 "no state outside CR status"); in hard mode the compiler already writes the routes, so that adds little, but say it rather than let §7's "read" stand unqualified.

---

## MINOR

1. **The Namespace-watch entry point has no reconcile key when `S` is empty** (02:177). The Agent controller reconciles Agents; a Namespace event with no Agent in `S` maps to nothing, which is precisely the case the watch was added for (r1 MINOR 10). Either the binding gets its own reconciler keyed by run-namespace name, or say the watch enqueues a synthetic key. And the watch predicate must be `pods-by: agent-operator` plus the `assayd-run-` name shape, not `run-namespace` — host sync namespaces carry `run-namespace` too (26:113) and must not wake this handler.
2. **No row for `Bound` + namespace with a deletion timestamp** (02:170). A namespace deleted by cluster rights is `Terminating` for seconds to minutes before it is absent; the `Bound`/present/UID-matches row "proceeds" into it and every create fails 403. 02:766 says the operator treats `NamespaceTerminating` as "wait"; the table should have the row, with `RunNamespaceUnavailable=Terminating`.
3. **"re-minted from the same buffers because the desired digest has not changed"** (02:175) is true only if the user's sources still hash to the record's digest. After a restart there are no buffers; the operator re-reads sources, and if they drifted since gating the published revision cannot be reproduced — A41's rule (02:269) sends it to weight 0 with `RevisionMaterialUnavailable`. Say "re-minted if the sources still match the record; otherwise A41 applies", so a cluster-rights deletion of a run namespace is recorded as the material loss it can be.
4. **The "five cases Codex r8 named"** (02:196) are not r8's five: "chosen collision" is dropped and "run namespace delete-and-recreate with the nonce copied" (r1 BLOCKER 2's) is substituted. Keep both and attribute them.
5. **`NotCreatedByOperator` is terminal and the re-bind has no trigger** (02:192). The binding is an uncached ConfigMap — no informer, no watch — so a human recreating it re-reconciles nothing until the resync period. Add to the procedure: "then annotate each Agent in `S`, or wait for resync"; `cmd/operator/main.go:58-61` already documents that pattern for the EvalSuite cache.
6. **Teardown step 2's CAS failing because the binding is already `Terminating`** (02:187) must be stated as "proceed to step 3", not "the other finalizer won" — after a crash there is no other finalizer. And step 3's "release the finalizer without waiting for deletion to finish" (02:188) conflicts with handler step 2's "wait (requeue)" on a rerun; say the finalizer releases once the handler has *issued* the delete or found it issued.
7. **Design 27 is the named consumer of `GatesTenantAdminBypassable` and is not amended** (26:109: "design 27's compliance profiles must not read hard mode as if the gate were admin-proof"). `27-*.md` mentions neither hard mode nor ADR-0006's gate. The sentence that says propagation is the failure this pass keeps finding should propagate.
8. **"never force-proceeds"** (26:116) does not survive a tenant delete: §5 tears the vCluster down, which deletes its API server and every Agent finalizer with it. Scope the sentence to Agent deletion.
9. **§7's body still says "read"** (26:85) while A1 says it widens it. Design 26 appends amendments rather than folding them; a one-line pointer in §7 keeps a reader of the body from citing "never tenant write" as current.
10. **Two trust domains, one path shape** (26:115, 24:36). A1 point 3 restates that a hard tenant's SPIRE issues `spiffe://<tenant-td>/agent/<ns>/<agent>`, federated to the host. Design 24's subjects are `agent:pa-reviewer` — no namespace, no trust domain — and the ext-authz adapter's SVID→subject mapping (24:47) does not say the trust domain is part of it. If it is not, a hard tenant mints `…/agent/team-a/reviewer` in its own domain and, unless per-tenant store selection derives from the trust domain, collides with a soft tenant's principal. Pre-existing in 26 §4 and 06 A1; A1 point 3 is where the next reader will look.
11. **Point 2's "not available to them there either"** (26:114) is true only of direct host access. The host copy is vCluster's sync of a virtual Secret the tenant admin controls; replacing the virtual object replaces the host copy. The Hard row at 26:109 admits it; point 2 reads as if it were not so.
12. **`Creating`, namespace absent → "creates it"** (02:167) — with the nonce read from the binding, not from memory. Two first reconciles in `S` racing on the binding create: the loser must stamp the winner's nonce, or the winner's `AlreadyExists` path (02:167, "the nonce row decides") rejects the namespace the loser made.

---

## Where the work is right

Tried to break these and could not:

- **The two-label split** (07:132-134, 02:165, 26:113) is the right shape and the `DoesNotExist` belt is the right belt. `pods-by: agent-operator` certifies one property, the sentence at 02:384 states it, and 26 A1 point 1 says the sync namespace never carries it. A tenant Pod with `agent-namespace: team-a` synced to the host is now issued nothing in the host domain, whatever vCluster does with labels.
- **The nonce/UID division of labour** (02:159) is correct as a model — nonce for the attacker who moves first, UID for the one who moves second — and rows 02:170-171 do compare the UID. MAJOR 4 and 5 are about the window's stated size and a row that was not rewritten, not about the model.
- **The teardown argument at 02:190** is correct for what it covers: with the CAS before the confirming list, a creator that read `Bound` is in the list. MAJOR 2 is the case where the creator read `Terminating` instead.
- **The three handler entry points** (02:177) are the right three, and step 2's `NamespaceTerminating` handling matches the research note §4 and the constant in `k8s.io/api`.
- **A5.5 as rewritten** is accurate to the code: the security context is rendered whole (`agent_controller.go:785-832`), `WorkloadNotAvailable` is the reason a never-available revision gets (`:341`), and the ReplicaSet `FailedCreate` gap is stated as a gap.
- **A5.7's bounds** are real bounds: `namespaces` write verbs are keyed to name shape plus binding UID, `configmaps`/`secrets` `update` is on operator-shaped names, `networkpolicies` is withheld until the code exists. The `repairMetadata` finding is still a live latent bug worth its own PR.
- **The `index` template** is identical in 02:373, 07:130, and 26:115, and the whole-segment invariant plus `Exists` selectors are stated in all three — the research note's contradiction 1 and caveat 2 are both closed.
- **39 conditions** (02:78-90): counted 39. **16-hex** (02:194): design 03:159 says 16-hex. **`Converging`** (26:113): A45 says `Converging` (02:143). **`WorkloadNotAvailable`**: exists.
- **06 A3 and 26 A1 point 3** say "unmeasured", say why, and forbid building on it. That is the honest form.
- **The lost-binding rule** (02:192) chooses the harsh path and says so; refusing to vouch for a namespace the operator cannot prove it made is the correct default.
- **`GatesTenantAdminBypassable`** is the right instrument — a condition that is always `True` in hard mode is a fact about the mode, and a Tenant CR reader gets it without reading this review.

## Verdict

**design 02 A60 / design 07 A5 / design 26 A1 / design 06 A3: REVISE** — 1 BLOCKER, 6 MAJOR, 12 MINOR. Fix order: BLOCKER 1 (scope the handler's list by source UID) and MAJOR 1 (state row precedence) are the two that pool or re-adopt another tenant's namespace; MAJOR 2 and 3 are one state and one predicate each; MAJOR 4 and 5 are the sentences r1 asked for and the window they describe; MAJOR 6 is a grant that §7 has been wrong about since R3-a. Designs 07 A5 and 06 A3 carry no findings of their own beyond the MINOR pointers and can pass with the 02/26 fixes.
