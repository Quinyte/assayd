# Critique — design 02 A60 · design 07 A5 · design 26 A1 · design 03 §3.2 (uncommitted, 2026-09-03)

- **Scope:** the working-tree diff of `docs/designs/02-agent-crd-operator.md`, `07-umbrella-chart-ci.md`, `26-tenant-cr.md`, `03-policy-compiler.md`, `README.md`, and the new `docs/research/tenant-namespace-primitives-2026-09.md`
- **Method:** read cold; every cross-design claim checked against the producing design; every claim about the repo checked against the repo (`cmd/operator/main.go`, `internal/controller/{agent_controller,material}.go`, `charts/plume/files/operator-rules.yaml`, `charts/plume/templates/namespace.yaml`, `test/chart/chart_test.go`); vCluster and spire-controller-manager behaviour checked against the research note and the vCluster docs where the docs answered
- **Verdict:** **REVISE — 3 BLOCKER, 5 MAJOR, 10 MINOR.** The binding record is the right instrument and most of it is right; it has one row missing and one crash point that its own correctness argument does not cover, and the SPIFFE trust boundary the two chart amendments define is broken by the third amendment in the same pass.

## Closure against Codex r8

| r8 finding | This pass |
|---|---|
| BLOCKER 7 — no durable proof the operator created the run namespace | **Partly closed.** The binding + nonce defeats pre-creation. It does not defeat delete-and-recreate, which the amendment claims it does (BLOCKER 2 below). r8's fix named `runNamespaceUID` in the record; A60 stores it and never reads it. r8's four tests (precreation, source delete/recreate, restart, chosen collision) are not owed anywhere (MAJOR 4) |
| MAJOR 3 — no concurrency protocol for deleting a shared run namespace | **Partly closed.** CAS-before-confirming-list is the right shape and the argument at 02:180 is correct for the no-crash case. It has no owner for the `Terminating` state after a crash between the CAS and the confirming list (BLOCKER 3). "Test a paused delete racing a new Agent" is not owed (MAJOR 4) |

---

## BLOCKER 1 — One label is two trust boundaries, and 26 A1 stamps it on a namespace an untrusted tenant fills with Pods

**Files:** `docs/designs/07-umbrella-chart-ci.md:131-132,143,156` · `docs/designs/02-agent-crd-operator.md:368` · `docs/designs/26-tenant-cr.md:112`

07 A5.2 renders exactly one cluster-wide `ClusterSPIFFEID` whose `namespaceSelector` is `plume.dev/run-namespace: "true"`, and its third bullet states the invariant that makes a label-derived identity sound: *"anyone who can create a Pod in a selected namespace can claim any agent's identity in any tenant … Widening the selector — to `All`, or to tenant namespaces — silently turns every tenant's Pod-create grant into cross-tenant impersonation."* 02:368 says the same. 07 A5.3 then uses **the same label** for Gateway listener admission.

26 A1 consequence 1 (26:112) has the tenant-operator stamp `plume.dev/run-namespace: "true"` on the **vCluster's host sync namespace** so the listener admits hard-mode routes. Every Pod in that namespace is created by vCluster on behalf of a tenant who, in hard mode, holds the vCluster kubeconfig (26 §4: soft is "not for tenants who get cluster API access"; hard is for them) and therefore chooses every label on every Pod it creates. The host `plume-agent` `ClusterSPIFFEID` now selects that namespace.

**Failure scenario:** a hard-mode tenant creates, in its vCluster, a Pod labelled `plume.dev/agent-namespace: team-a`, `plume.dev/agent: reviewer`. vCluster syncs it to the host sync namespace. The host controller-manager matches the namespace (label present), matches the Pod (`Exists` on both labels), renders `spiffe://<host-td>/agent/team-a/reviewer` — the identity of a **soft-mode** tenant's agent in the **host** trust domain — and the host SPIRE issues it. Design 24's `can_invoke`/`can_call` and design 03's admitted sets key on exactly that principal. This is the attack A5.2 says the selector exists to prevent, delivered by the amendment written in the same pass.

Whether vCluster carries the two `plume.dev/*` Pod labels to the host verbatim is not in the research note, is cited by neither design, and I could not confirm it from the vCluster docs (Context7, `/loft-sh/vcluster-docs`, no answer on label translation). The exploit is conditional on that; the defect is not: the amendment reuses a label whose *defined meaning* is "only the operator creates Pods here" on a namespace for which that is false by construction, and leaves the invariant to an undocumented property of a third-party syncer.

**Fix:** two labels for two boundaries. `plume.dev/run-namespace: "true"` stays the listener-admission label and may go on sync namespaces. The `ClusterSPIFFEID` selects on a second label the tenant-operator **never** stamps on a sync namespace — best: an exact match on `plume.dev/owned-by: <install-uid>` plus `matchExpressions: [{key: plume.dev/tenant-sync, operator: DoesNotExist}]` as the belt. 26 A1 gains the sentence "a sync namespace is never a SPIFFE-selected namespace on the host, because a tenant creates Pods in it", and 07 A5.2's third bullet names the sync namespace as the case that would have widened the selector. Owed: measure Pod-label sync on a vCluster before either direction is relied on.

## BLOCKER 2 — The recorded `runNamespaceUID` is never compared, and the nonce is public the moment it is stamped

**Files:** `docs/designs/02-agent-crd-operator.md:136,155-156,166-167,171`

02:156 generates a 128-bit nonce *before* the namespace exists and stamps it on the namespace. That defeats **pre-creation** — the only attack in which the attacker moves first. It defeats nothing else, because a stamped nonce is readable by any principal with `get` or `list` on Namespaces, which is one of the commonest grants a cluster hands out. 02:167 claims the "nonce absent or different" row catches *"a namespace deleted and recreated by someone with cluster rights"*. It does not.

**Failure scenario:** the attacker (`get`+`delete`+`create` on Namespaces — the same tier r8 BLOCKER 7 put in scope) reads `plume-run-team-a`'s nonce and labels, deletes the namespace, recreates it with the same labels, the same nonce, and a RoleBinding granting itself `admin`. The operator's next reconcile hits row 02:166 — *"namespace present, nonce annotation matches → binds, or stays bound; proceeds"* — and writes the next revision's Secret copies into a namespace the attacker administers. Row 02:171 ("`Bound`, namespace absent → new nonce") shows the design expected to *see* the absence; a recreate that beats the next reconcile is never absent. The binding stores `runNamespaceUID` (02:155) for exactly this comparison, and no row reads it. 02:136's header — *"the proof is a nonce the operator alone knew"* — is false after the first stamp.

**Fix:** every "proceeds" row compares the live namespace's `metadata.uid` to the binding's `runNamespaceUID`; mismatch is `NotCreatedByOperator`. State the division of labour in one sentence: the nonce defeats pre-creation (attacker moves first, cannot know it); the UID defeats recreation (attacker moves second, cannot reproduce it). Rewrite 02:136's header accordingly. Add the delete-and-recreate test r8 named.

## BLOCKER 3 — Teardown has a crash point between the CAS and the confirming list, and the `Terminating` state has no owner

**Files:** `docs/designs/02-agent-crd-operator.md:170,175-178,180,407`

The protocol: (1) live-list, (2) CAS `Bound→Terminating`, (3) live-list again and flip back if anyone appeared, (4) delete the namespace. 02:180: *"The order is what makes it correct: the binding changes state before the confirming list."* True — while the confirming list runs. The operator rolls on every upgrade (07 A4) and crashes are routine; consider a crash, or leader loss, after step 2 and before step 3.

On resumption the deleting Agent's finalizer reruns from step 1, reaches step 2, and the CAS precondition (`Bound`) is false. The only text for that state is the `Terminating` row at 02:170: *"as above: wait for the namespace to be gone, delete the binding, start fresh."* The namespace was never deleted. Two readings, both wrong:

- **"wait" literally:** nothing ever issues the delete. The binding is `Terminating` forever; the deleting Agent's finalizer either wedges or releases; every new Agent in `S` sits at `RunNamespaceUnavailable=Terminating`, whose §5 row (02:407) promises it *"clears by itself when the namespace is gone"* — it never goes. A tenant namespace is dead after one badly-timed restart, and the condition text says the opposite.
- **"as above" includes issuing the delete:** then whoever next reconciles in `S` deletes the namespace — including when a new Agent B was created between steps 1 and 2 and step 3 would have found it and flipped the binding back. B's live workloads and copies are deleted. That is r8 MAJOR 3's original failure, reached through the amendment that claims to close it.

**Fix:** `Terminating` becomes an idempotent state with an owner. Whoever reconciles any Agent in `S` and reads `Terminating`: (a) re-runs the live-list; (b) non-empty → CAS back to `Bound`; (c) empty and the namespace has no `deletionTimestamp` → issue the nonce+UID-verified delete; (d) namespace gone → delete the binding. The deleting finalizer's step 4 *is* a call to this handler, so a rerun after a crash is the same code path. Owe two tests: a reconciler paused between steps 2 and 3 with a new Agent created in the gap; and a paused delete racing a new Agent (r8's).

---

## MAJOR 1 — In hard mode the tenant admin is the principal A42 was built to exclude, and nothing says the gate is bypassable there

**File:** `docs/designs/26-tenant-cr.md:108` ("it holds by vCluster RBAC")

A42's whole claim is *"the editor has no rights where the material lives"* (02 §3.2). In hard mode the tenant holds the vCluster kubeconfig and is, inside it, cluster-admin: it can read the binding in the tenant operator's namespace, delete and recreate the run namespace with the nonce it read (BLOCKER 2 applies inside the vCluster too), and delete-and-recreate any copy. The host gateway sees only the synced Service. So a hard-mode tenant admin serves un-gated bytes under a gated revision's identity, and ADR-0006's gate is per-tenant bypassable. That may be acceptable — a tenant subverting its own governance is not cross-tenant — but the amendment says the opposite ("it holds"), and design 27's compliance profiles will be read as if the gate were unbypassable.

**Fix:** state it. Either (a) record that in hard mode the gate binds tenant *users* and not the tenant's vCluster admin, surfaced on the Tenant CR (a condition, e.g. `GatesTenantAdminBypassable=True` in hard mode), or (b) move the proof host-side: the compiler compiles a route only for a revision whose `RevisionRecord` digest it verified against material it can read, and says what it reads. Pick one.

## MAJOR 2 — Hard-mode teardown runs two finalizers with no order, so workloads die before routes drain

**Files:** `docs/designs/26-tenant-cr.md:115` · `docs/designs/02-agent-crd-operator.md:380`

26 A1 point 4: *"The tenant's agent-operator runs design 02 §3.7's steps inside the vCluster."* §3.7's first two steps are **drain traffic (weights 0)** and **revoke routes** — host-side writes that the tenant's agent-operator cannot make (26:108: it holds no host credentials). The compiler's R3-a finalizer (26 §5) revokes host resources on source deletion; the tenant operator's finalizer deletes workloads. Kubernetes runs finalizers concurrently and orders nothing.

**Failure scenario:** Agent deleted; the tenant operator deletes the Deployment in the same reconcile the compiler begins its reverse-order revoke; for the revoke's duration the route still weights a backend with no endpoints; in-flight A2A tasks get connection failures instead of the `taskTimeout` drain §3.7 promises. "Nothing is force-killed silently" is false in hard mode.

**Fix:** specify the order. The tenant operator's finalizer waits on a signal the compiler writes to the tenant-side CR (`RoutesRevoked=True`, or the compiler's finalizer being gone) before deleting workloads; or the compiler owns steps 1–2 and the tenant operator owns 3–5, written as a two-column version of §3.7 in 26 A1.

## MAJOR 3 — Per-tenant SPIRE inside the vCluster is unmeasured and structurally doubtful, and A1 point 3 builds on it as settled

**Files:** `docs/designs/26-tenant-cr.md:114,119` · `docs/designs/06-identity-glue.md:104`

The research note (§1) records that spire-controller-manager **always** adds a `k8s:pod-uid:<uid>` selector to every entry. A controller-manager inside the vCluster sees the **virtual** Pod's UID. The SPIRE agent that attests the workload runs on the host node and asks the host kubelet, which reports the **host** Pod's UID, name and namespace. Unless vCluster preserves Pod UIDs across the sync — its docs do not claim it and I could not confirm it — no entry ever matches and no hard-mode Agent ever gets an SVID. 26 §8's hard-mode e2e would find this the first time it runs; 26:119 says it has never run.

This inherits from design 26 §4 and design 06 A1, both approved on paper. A1 point 3 is the amendment that turns "per-tenant SPIRE federated to the host" into *"renders the same template … `index` form, whole-segment labels, `Exists` selectors, run-namespace `namespaceSelector`"* and presents it as settled. It is consistent with §4 and 06 A1; consistency with an unmeasured premise is not soundness.

**Fix:** mark it unmeasured in 26 A1 and 06 A1 and spike it (vCluster, spire-agent DaemonSet, controller-manager inside, one labelled Pod: does an SVID appear). If it fails, the fallbacks — the host controller-manager with a tenant-aware template, or `workloadSelectorTemplates` mapping to host selectors — both change A1 point 3 and BLOCKER 1's selector.

## MAJOR 4 — No test is owed for the state machine or the protocol

**Files:** `docs/designs/02-agent-crd-operator.md:182,444`

r8 BLOCKER 7's fix listed four tests; MAJOR 3's listed one. A60 adds nothing to §8 (02:444), and the implementation note (02:182) owes only the ConfigMap-principal e2e. A nine-row state table and a four-step protocol whose correctness is argued in prose (02:180) and pinned by nothing is this corpus's commonest defect one step earlier: tests that do not exist cannot fail, and BLOCKERs 2 and 3 are exactly the rows and the crash point a per-row envtest would have forced someone to write down.

**Fix:** §8 gains, by name: one envtest case per state-table row; one per crash point with a paused reconciler (after binding create; after namespace create; between teardown steps 2 and 3; after step 4); the cluster cases r8 named (pre-creation, source delete/recreate, operator restart, chosen collision, paused delete vs new Agent).

## MAJOR 5 — 07 A5.5's failure path names an observable that cannot fire for it, and the real failure is silent

**File:** `docs/designs/07-umbrella-chart-ci.md:176`

*"A tenant whose image needs more than `restricted` allows gets a rejected Pod and `WorkloadRejected`."* Two things are wrong. `WorkloadRejected` is the reason the controller sets when **its own** API call is refused (`internal/controller/agent_controller.go:270`); a Pod Security `enforce` rejection happens on the **ReplicaSet controller's** Pod create, which the operator does not observe — the Deployment is accepted, no Pod ever appears, and the reason lives in a ReplicaSet `FailedCreate` event. And the case cannot arise as described: the operator renders the entire securityContext itself (`agent_controller.go:785-832`, no volumes at all) and the Agent spec exposes none of it, so no tenant image "needs more than `restricted`". What *can* happen — a future field, a `-version` pin older than a field the operator uses — would be silent at the Agent level (NFR-8).

**Fix:** say the rendered Pod satisfies `restricted` by construction (verified) and that a PSA rejection is therefore only reachable through a future change; and either watch ReplicaSet `FailedCreate` and raise a condition, or state that this class of failure is currently invisible on the Agent.

---

## MINOR

1. **Nonce: label or annotation?** 02:156 "stamped on it as `plume.dev/binding-nonce`"; 02:166-167 "nonce annotation". Pick annotation (nobody should select on it) and say so once.
2. **`--operator-namespace` does not exist** (02:150, present tense). `cmd/operator/main.go` defines `metrics-bind-address`, `health-probe-bind-address`, `leader-elect`, `eval-suite-check-interval`. The operator already knows its namespace from the mounted serviceaccount namespace file — controller-runtime uses it for the leader lease — so the flag is a choice, not a need; write it in the future tense or drop it. (The 02:182 span covers it as design; the sentence still reads as a mechanism.)
3. **`namespaces` `update`/`patch` are unbounded** (07:190). The row bounds `delete` by name-shape + nonce and says nothing about `update`/`patch`, which cluster-wide lets a bug relabel any tenant namespace as a run namespace — and, after BLOCKER 1's fix, as a SPIFFE-selected one. Bound all three verbs by the shape, and say the code refuses otherwise.
4. **The read-only RoleBinding needs grants A5.7 does not list** (02:138, 07:184-195). RBAC escalation prevention: to grant `pods`/`pods/log`/`events` read, the operator must hold them (or `bind`). The ClusterRole has no `pods` verbs today. Owed anyway, but A5.7 claims to be the complete list of widenings.
5. **Mirrors are not in the creation row** (02:163 vs 02:139 "at creation and on every reconcile"). If `Bound` precedes the ResourceQuota/LimitRange mirror, the first Pod escapes quota. Put the mirrors before `Bound` in row 02:163.
6. **`plume-mirror-<name>` can exceed 253** (07:192, 02:139). ResourceQuota/LimitRange names are DNS subdomains up to 253; a 13-character prefix overflows for any source name over 240. Apply design 03 §3.2's truncate-and-hash rule or state the bound.
7. **"the read-only RoleBinding … must never carry a write verb"** (07:143, 02:368) has no enforcement. Add the chart/operator test that fails if the bound Role has any verb outside `{get,list,watch}` — rule 7 in RBAC form, which A5.7 itself invokes for NetworkPolicy verbs.
8. **Lost binding has no recovery** (02:164). If the operator's own `plume-run-binding-*` ConfigMap is lost (an admin mistake in `plume-system`, a restore), every Agent in `S` goes terminal and the documented recovery is deleting a namespace with live serving workloads. State a human re-bind procedure (recreate the binding from the namespace's UID and nonce, with the human vouching) or say the harsh path is chosen on purpose.
9. **02:142 says `NOTES.txt` "say[s] so"** in the present tense; there is no `NOTES.txt` (07 A1: owed). 07 A5.4 gets this right ("the `NOTES.txt` A1 owes states it"); align 02.
10. **Step 4 leaves `Terminating` bindings behind** (02:178): "cleaned up by the next Agent to arrive in `S`" — a namespace that never gets another Agent keeps its binding forever. Harmless, but BLOCKER 3's handler should run on a Namespace watch as well, and then this clutter goes too.

---

## Where the work is right

Tried to break these and could not:

- **The `index` correction and the whole-segment invariant** (02 §3.5, 07 A5.2, research §1): the caveat that fail-closed is a property of template *shape* is exactly right, and adding the `Exists` `podSelector` rather than relying on the empty-segment rejection is the correct hardening.
- **ReferenceGrant exemption** (03:153, 07 A5.3): matches the Gateway API v1 text the note quotes; Route→Gateway attachment needs the listener's consent only.
- **"ConfigMaps already bypass the operator's informer cache"** (02:159): true — `cmd/operator/main.go:73-89`, `DisableFor: ConfigMap, Secret`, and the comment there already gives the finalizer-sweep reason.
- **"`repairMetadata` calls `Update` and the shipped role grants no `update`"** (07:191): true — `internal/controller/material.go:389` and `charts/plume/files/operator-rules.yaml` (markers at `agent_controller.go:111`). No test reaches the restamp path, so this is a live latent bug worth its own PR regardless of A42.
- **The chart's namespace is `restricted`** (`charts/plume/templates/namespace.yaml`) and **the rendered Pod satisfies `restricted`** (`agent_controller.go:785-832`: non-root, RuntimeDefault seccomp, no escalation, all capabilities dropped, no volumes). Both A5.5 claims verified.
- **39 conditions** (02:756): counted 39.
- **A5.1's "the chart cannot render per-run-namespace objects"** is correct and is the sentence that makes the rest of A5 honest.
- **A5.4's CNI point** (k3s enforces NetworkPolicy by default; kind's kindnet does not) is right, and "assert on k3d, report unverified on kind" is the right shape for the e2e.
- **The quota decision, on attack item (4):** the 2× figure is a correct upper bound, and mirroring **cannot** reject a Pod that admitted before the move — every object that now counts in the run namespace (Pods, copies, Service, routes) counted in the source namespace before A42 under the same `hard` values, so run-namespace usage is a subset of pre-move source usage. The only rejections mirroring can introduce are ones a tenant inflicts on itself by editing its own quota, which is the tenant's own ceiling applied where their Pods run. That is the right call, and choosing it over a custom admission webhook is doctrine (library over server) applied correctly.
- **26 A1's consistency with §4 and 06 A1:** consistent in substance; withdrawing `plume.dev/source-uid` for `plume.dev/agent-uid` is the right vocabulary fix; the `NamespaceTerminating` handling matches the research note.

## Verdict

**design 02 A60 / design 07 A5 / design 26 A1 / design 03 §3.2: REVISE** — 3 BLOCKER, 5 MAJOR, 10 MINOR. Fix order: BLOCKER 1 (split the label) and BLOCKER 2 (compare the UID) are one-paragraph edits each; BLOCKER 3 needs the `Terminating` handler written as a row with an owner; MAJOR 4's tests are what would have caught 2 and 3.
