# Code review — design 02 A42/A60/A61: the operator-owned run namespace

- **Reviewer**: Claude (Fable 5.1), fresh context, reading cold. Same family as the implementer; the cross-family pass is still owed by the project's own rule (AGENTS.md §"If you are here to review").
- **Scope**: the uncommitted working tree on `main` at `5043394` — `internal/controller/runnamespace.go` (new), `agent_controller.go`, `material.go`, `conditions.go`, `cmd/operator/main.go`, `charts/plume/templates/admission.yaml` (new), `operator.yaml`, `values.yaml`, `files/operator-rules.yaml`, `config/rbac/role.yaml`, `test/envtest/runnamespace_test.go` (new), `test/e2e/e2e_test.go`, `test/chart/chart_test.go`.
- **Spec**: design 02 §3.2 (the binding record, the twelve ordered checks, the Terminating/Deleting handler, the teardown), §3.7, §5; design 07 A5.7 (RBAC) and A5.9 (admission policies).
- **Verdict: REVISE — 2 BLOCKER, 9 MAJOR, 12 MINOR.**

## What was run

| Gate | Result |
|---|---|
| `make test` (fmt, vet, unit, docs, conformance, envtest, chart) | green, `EXIT=0`; envtest 55.8s |
| `make race` | green (`internal/controller` 4.0s under `-race`) |
| `make e2e` on k3d (`plume-local`, image `e2e-<ts>`, chart installed with `--wait`) | green — all nine tests including the three new ones: `TestANamespaceEditorCannotReplaceTheRevisionsCopy`, `TestNamespaceLabelsAreReservedToTheOperator`, `TestOperatorRoleGrantsWhatTheRunNamespaceCodeCalls` |
| `controller-gen rbac` regenerated to a scratch dir and diffed | `config/rbac/role.yaml` matches the markers; the chart's `operator-rules.yaml` rules block matches `config/rbac` |
| Rendered `admission.yaml` applied to the envtest API server (1.36) | the policy IS enforced there: CREATE with `pods-by`, UPDATE adding `pods-by`, merge-PATCH adding `run-namespace`, merge-PATCH adding the nonce annotation — all denied with the "reserved" message. Two writes were NOT denied — see MAJOR 1 |
| Probe tests (seven envtest, one unit; written for this review and deleted afterwards) | six reproduced a defect; two established baselines for mutations M1 and M2 below |
| Mutation ledger (this review's, below) | 12 mutations, each compile- and vet-checked, run against the full `internal/controller` unit suite and the full envtest suite with the probes removed |

Every mutation was restored from a `cp` backup and the restore byte-compared against the pre-mutation source. `git status` after the review is identical to before it, plus this file.

## Reading the twelve rows against the code

For the record, before the findings: rows 1, 2, 3, 5, 6, 8, 10, 11 and 12 do what the table says, in the table's order, with the first applicable row deciding. Row 2's `AlreadyExists` returns a retriable error rather than "re-read and continue", which converges to the same thing one pass later. Row 6 binds from the `Create` response only, mirrors before `Bound`, and never adopts. The handler's step 0 and step 1 are in the order the design demands (state guard, then UID-first, then the deletion-timestamp wait, then the scoped lists, then the two-swap fence). The finalizer's `self` exclusion is only in the finalizer; a live Agent reaching rows 4/5/10 counts, as A61's history entry says it must. `runNamespaceStillNeeded` scopes both lists by the binding's source UID (the r3 BLOCKER 1 fix is present and pinned).

The findings are where the code and the design part company, where the design itself has a hole the code inherited, and where a rule the code enforces is pinned by nothing.

---

## BLOCKER 1 — `RunNamespaceUnavailable` is never cleared: every Agent that ever waited carries it forever

**File**: `internal/controller/conditions.go:44-61` (`ownedTypes`); consumer `agent_controller.go:263-287`.

`CondRunNamespaceUnavailable` is not in `ownedTypes`. `merge()` therefore treats it as another controller's and carries it forward verbatim (`conditions.go:89-92`). Nothing ever writes it `False`.

**Failure scenario** (reproduced twice, envtest):
1. Agent settles. Binding is set `Terminating` (a finalizer crash, or row 4/10). Next reconcile: handler restores `Bound`, `RunNamespaceUnavailable=True/Terminating` written. Next reconcile: row 12, workload created, `Ready` re-asserted — and the object now reads `RunNamespaceUnavailable=True, reason=Terminating, "run namespace … is being torn down; this Agent waits for it to be gone"` next to `Ready=True`, permanently.
2. Policies absent → `LabelAuthorityAbsent`, `Degraded`. Policies installed, Agent recovers, binding written, workload created. `RunNamespaceUnavailable=True/LabelAuthorityAbsent, "…No run namespace is created until the chart's policies are present"` stays on the object forever. `Degraded` clears (it is owned); the condition that names the cause does not.

Design 02 §5 says the Terminating row "clears when the handler has removed the namespace and a fresh one is created". It never does. Alerts keyed on the condition fire on every Agent that has ever met row 4, 5, 7, 9 or 10 — which under normal operation is every Agent in a namespace that has ever been torn down and recreated. This is rule 8's "loud and wrong".

`TestATerminatingBindingFlipsBackWhenAnAgentArrives` and `TestRunNamespaceRefusesWithoutLabelAuthority` both drive the recovery and neither looks at the condition afterwards.

**Fix**: add `plumev1alpha1.CondRunNamespaceUnavailable: true` to `ownedTypes` (it is abnormal-true; absence is the signal, so it does not go in `stickyTypes`). Assert in both recovery tests that the condition is absent after the Agent proceeds. Mutation: remove the map entry; both assertions must fail.

## BLOCKER 2 — Row 7's recovery wedges terminally on its own Terminating namespace

**File**: `internal/controller/runnamespace.go:411-433` (the `Creating`, namespace-present arm).

Row 7 deletes the crash-gap namespace, rotates the nonce, and returns `Terminating` (non-terminal, `RequeueAfter: 5s`). But the status write that reports it produces an Agent watch event, and there is no predicate on `For(&Agent{})`, so the next pass runs immediately. On a real cluster a namespace deletion takes seconds; in that window the namespace is present, carrying the OLD nonce, with a deletion timestamp. The `Creating` arm checks only `ns.Annotations[nonce] == b.nonce` — now the rotated one — so the next pass is **row 8: `NotCreatedByOperator`, terminal, `Degraded`**, with a message telling the human to "delete that namespace if nobody made it on purpose, then annotate the Agents". The namespace is already deleting; when it is gone nothing re-triggers the Agent; the human must intervene to recover from a path the design says "costs one namespace churn".

**Reproduced** (envtest; the deleted namespace stays Terminating there, which is exactly the real-cluster window): pass 1 → deletion timestamp set, nonce rotated (as `TestANamespaceObservedWhileCreatingIsDeletedNotAdopted` asserts); pass 2 → `phase=Degraded reason=NotCreatedByOperator`. The existing test stops one pass short of the defect.

The same arm is what a second reconcile worker would meet at row 7 while a peer is mid-row-6 (MAJOR 4), and what row 4-from-`Creating` produces (MAJOR 3). The design's row 7/8 pair is silent about the Terminating interval; the code inherited the hole.

**Fix**: in the `Creating` arm, before the nonce comparison: `if !ns.DeletionTimestamp.IsZero() { return "", &runNamespaceError{reason: ReasonTerminating, message: "run namespace %s is being deleted; waiting for it to be gone before creating a fresh one"} }`. Waiting is safe whatever the namespace's provenance: once it is gone row 6 runs. Extend the row-7 test with a second `reconcileOnce` asserting `phase=Pending`, `reason=Terminating`, and `getBindingMaybe` still `Creating`. Amend design 02 §3.2's table with a row between 6 and 7: "`Creating`, namespace present, **deletion timestamp set** → wait; creates into it would 403 with cause `NamespaceTerminating`".

---

## MAJOR 1 — The label reservation does not cover the `status` and `finalize` subresources

**File**: `charts/plume/templates/admission.yaml:47-51` (`resources: ["namespaces"]`).

A `ValidatingAdmissionPolicy` resource rule matches the named resource only; `namespaces/status` and `namespaces/finalize` are separate match targets. The API server does not reset metadata on either Namespace subresource — measured below, not inferred — so labels ride through both.

**Reproduced** against the rendered policy on the envtest API server, as a non-operator identity, on a namespace the policy had just refused to label via UPDATE and PATCH:
- `PUT /api/v1/namespaces/<n>/status` with `plume.dev/pods-by: agent-operator` → `err=<nil>`, label present afterwards.
- `PUT /api/v1/namespaces/<n>/finalize` with `plume.dev/run-namespace: "true"` → `err=<nil>`, label present afterwards.

**Failure scenario**: a principal holding `update` on `namespaces/status` or `namespaces/finalize` (the built-in `system:controller:namespace-controller` has both; any platform ClusterRole that hands them to a namespace-provisioning controller or to "namespace admins" does too) mints a `pods-by: agent-operator` namespace the operator never made. The `ClusterSPIFFEID` selects on that label and issues an agent identity to whatever Pods that principal can then create there. That is the exact attack A5.9 exists to close, and the e2e negative test (`TestNamespaceLabelsAreReservedToTheOperator`) tests only the main resource CREATE. Design 07 A5.9's own sentence — "a label is writable by whoever can create or update the namespace" — includes these two update paths.

**Fix**: `resources: ["namespaces", "namespaces/status", "namespaces/finalize"]` in `matchConstraints`. The namespace controller's `finalize` writes change `spec.finalizers`, not labels, so `touched` is false for them and nothing regresses. Add both subresource writes to the e2e negative test (the cluster-admin identity it already uses exercises the same code path) and to a chart test that pins the three resources in the rendered rule.

## MAJOR 2 — Deleting the `plume-operators` ConfigMap denies every namespace create and update in the cluster

**File**: `charts/plume/templates/admission.yaml:69-77` (`paramRef` with `parameterNotFoundAction: Deny`) together with `failurePolicy: Fail`.

**Reproduced**: with the rendered policy in place, deleting `plume-system/plume-operators` and creating an unrelated, unlabelled namespace returned `namespaces "probe-unrelated-tenant" is forbidden: ValidatingAdmissionPolicy 'plume-namespace-labels' with binding 'plume-namespace-labels' denied request: failed to configure binding: no params found for policy binding with 'Deny' parameterNotFoundAction`.

**Failure scenario**: an operator uninstalls plume by `kubectl delete ns plume-system` — the most common uninstall on the planet. The ConfigMap goes with the namespace; the two policies and bindings are cluster-scoped and stay. From that moment no principal can create or update any namespace anywhere in the cluster, including `kube-system`, until someone diagnoses a plume policy on a cluster plume is no longer installed on and deletes it. `helm uninstall` happens to order it correctly (unknown kinds are deleted first), which is luck rather than design. The design says nothing about this coupling; the chart's values comment says the Gateway name is fixed "so that the first route anyone authors against it is already governed" but not that a ConfigMap in the operator's namespace is now load-bearing for the whole cluster's namespace writes.

**Fix** (recommended): drop `paramKind`/`paramRef` and render the operator identity list into the CEL from chart values — `variables.operators: "['system:serviceaccount:{{ .Values.namespace }}:{{ include "plume.operator.name" . }}'{{ range .Values.admission.extraOperators }}, {{ quote . }}{{ end }}]"` — so the policy is self-contained and the enterprise tenant-operator is appended by values, not by editing a live ConfigMap. If params are kept: `helm.sh/resource-policy: keep` on the ConfigMap, a `NOTES.txt` sentence, and a chart test that pins whichever choice is made. Either way, record the blast radius in design 07 A5.9.

## MAJOR 3 — Row 4 from `Creating` writes a record the decoder refuses, and refuses the operator's own crash-gap namespace

**File**: `internal/controller/runnamespace.go:355-360` (row 4's swap from `Creating`), `:203-205` (the decoder's `Terminating` requires a UID), `:614-619` (handler step 1).

Row 4 swaps to `Terminating` "from `Creating` or `Bound`". From `Creating` the binding has no `runNamespaceUID`, so `encode()` persists `state=Terminating, runNamespaceUID=""`.

**Reproduced** (unit): `binding{state: Creating}` → `state = Terminating` → `encode()` → `decodeBinding()` returns `binding record b is Terminating and has no runNamespaceUID`. The operator writes what it cannot read.

Two consequences:
1. If anything fails between that swap and the handler's binding `Delete` (a transient API error on the namespace `Get` at `:610`, or on the `Delete` at `:616`), the next reconcile reads the record and reports **`BindingRecordInvalid`, terminal**, on a record the operator wrote thirty seconds earlier, with a recovery message telling the human to correct it by hand. `RunNamespaceReconciler` and `releaseRunNamespaceIfLast` both treat that error as "leave it", so nothing ever repairs it.
2. When the crash-gap namespace exists (created in row 6, unrecorded, nonce matching — the case row 7 exists for) and the tenant namespace was meanwhile deleted and recreated, the handler's step 1 sees `ns.UID != ""`, deletes the binding, and the next pass meets **row 1: `NotCreatedByOperator`, terminal**, on a namespace the operator created. **Reproduced** in envtest: pass 1 → binding gone, namespace kept; pass 2 → `Degraded/NotCreatedByOperator`.

The design's row 4 says "from `Creating` or `Bound`", so the code is faithful to a spec row that is wrong for `Creating`: there is nothing to terminate when no namespace was ever bound.

**Fix**: in row 4, when `b.state == bindingCreating`: read the run namespace; if present and its nonce equals `b.nonce`, `deleteRunNamespace` it (row-7 authority: shape + nonce); then delete the binding and return `Terminating`. Never swap a `Creating` record to `Terminating`. Keep the decoder strict. Amend row 4 in the design: "from `Bound` only; a `Creating` binding whose source UID has moved is deleted after its crash-gap namespace, if any, is". Pin with the envtest case above (expect pass 2 to be row 2 → row 6 → `Bound`).

## MAJOR 4 — Rows 6 and 7 are not safe under two reconcile workers, and nothing says so

**File**: `internal/controller/runnamespace.go:388-433`; `SetupWithManager` (`agent_controller.go:1229-1283`) sets no `MaxConcurrentReconciles`, so today controller-runtime's default of 1 holds.

Design 02 §3.2 says twice that "a single reconcile worker is not a premise of this design's correctness", and argues it for the Terminating/Deleting fence — where it is true (I traced the finalizer-vs-finalizer, finalizer-vs-Namespace-reconciler and live-Agent-vs-handler interleavings and the two-swap fence holds). It is false for rows 6/7. Two Agents in one source namespace, two workers:

- A reads `Creating`, creates the namespace (row 6), has not yet swapped.
- B reads `Creating`, finds the namespace present with the binding's nonce, takes **row 7**: deletes A's namespace, rotates the nonce.
- A's `swapBinding(Bound)` conflicts (B moved the record) → retriable error. A's next pass: `Creating` with nonce N2, namespace present with N1 and a deletion timestamp → **row 8, terminal** (BLOCKER 2's arm).

A peer mid-row-6 is indistinguishable from a crashed predecessor by any observation the protocol makes. The only reason this cannot happen today is that the Agent controller runs one worker, and no comment, test or design sentence records that dependency. The handler's other writers (`RunNamespaceReconciler`, the finalizer) never touch a `Creating` binding, so they are not part of this race.

**Fix**: leader election already guarantees one operator process, so in-process serialization is sufficient: a `sync.Map` of per-run-namespace mutexes held across rows 2–12 of `ensureRunNamespace` makes the claim true. Add the sentence to the design. Alternatively, state the premise ("the Agent controller runs one worker; raising `MaxConcurrentReconciles` requires per-binding serialization") in the design and in `SetupWithManager` — but the design currently claims the opposite, so one of the two texts must change.

## MAJOR 5 — The UID that is "compared on every reconcile" is read from the informer cache

**File**: `internal/controller/runnamespace.go:307, :383, :610` (`r.Get` on the run `Namespace`); `main.go` disables the cache for ConfigMaps and Secrets only.

The design makes the binding read uncached "so every reconcile sees the latest state and the protocol below needs no second lock", and calls the namespace UID the proof that is "compared on every reconcile". The binding is read live; the namespace whose UID it is compared against is read from the cache. Rows 1, 7, 8, 9, 10, 11, 12 and handler step 1 all decide on a cached object.

**Failure scenario**: the principal the design's row-1 paragraph names — cluster-scoped `create`/`delete` on Namespaces plus the ability to bind rights inside one — deletes the bound run namespace and recreates it with its RoleBinding. Between the API server's recreate and the informer's next delivery, a reconcile of any Agent in `S` reads the old object (old UID) from the cache, passes row 12, and its `Create`s of Secret copies and the Deployment land in the new namespace. The window is informer lag — milliseconds — and the spec's own words are that the UID is the guarantee, not a probability. The reverse staleness (cache says present, API says gone) is benign: row 12 proceeds and every write 404s.

`r.reader()` already exists for exactly this purpose and costs one uncached GET; the reconcile already makes five.

**Fix**: use `r.reader().Get` for the run namespace in `ensureRunNamespace` and `runNamespaceHandler` (and for the source namespace at `:295`, whose UID row 4 decides on). Say in the design which reads are live. Not pinnable in envtest (no cache); pin by making the cached client refuse Namespaces — `DisableFor: []client.Object{&corev1.ConfigMap{}, &corev1.Secret{}, &corev1.Namespace{}}` in `main.go` — if a cluster-wide Namespace informer is not wanted at all; `RunNamespaceReconciler`'s `For(&Namespace{})` needs the informer, so the explicit reader is the cleaner fix.

## MAJOR 6 — The Gateway-route policy is bypassed by a bare-name `parentRef` in the Gateway's own namespace

**File**: `charts/plume/templates/admission.yaml:108` — `has(p.namespace) && p.namespace == params.data.gatewayNamespace`.

A `ParentReference.namespace` "when unspecified, refers to the local namespace of the Route" (Gateway API spec). An `HTTPRoute` created **in `plume-system`** with `parentRefs: [{name: plume}]` attaches to the plume Gateway and does not match `targetsPlume`, because `has(p.namespace)` is false. Only principals with write in `plume-system` can do that today, which is the operator's namespace — but the policy exists so that "a principal who somehow held rights in a run namespace still could not attach an ungoverned route" (design 02 §3.2, design 07 A5.9), and the Gateway's own namespace is the one place that principal set is not empty by construction (anyone who can create the operator's ConfigMaps or Secrets is already in it).

Dormant at P1 (no Gateway CRDs), which is exactly when it should be right: the e2e cannot exercise it, so only reading can.

**Fix**: `(has(p.namespace) ? p.namespace : request.namespace) == params.data.gatewayNamespace`. A chart test that renders the expression and asserts it contains `request.namespace` is a weak pin; the real one is an e2e cell that installs the Gateway CRDs, owed with design 03.

## MAJOR 7 — Upgrading from a pre-A42 operator leaks the old copies in the Agent's namespace and runs two workloads

**File**: `internal/controller/agent_controller.go:1182-1203` (`finalize` sweeps `runNS` only), `material.go:313-345` (`collectRevisionMaterial` sweeps `runNS` only).

A61 says an operator built after A42 "clears a stale [`EnvSourceProtectionUnavailable`] left by an operator built before", so upgrade is in scope. Before this change, copies were created in `agent.Namespace` with **no** `ownerReference` (A44 chose labels so this move "would not change the invariant") and were collected only by the finalizer's sweep of `agent.Namespace`. That sweep now looks in `plume-run-<ns>`. Pre-A42 copies in the Agent's namespace — immutable snapshots of Secret bytes — are collected by nothing, ever, including when the Agent is deleted. The pre-A42 Deployment does carry an `ownerReference` (the old `SetControllerReference`), so it is GC'd at Agent deletion, but until then it keeps running in the Agent's namespace beside the new one in the run namespace: two copies of every upgraded Agent.

**Failure scenario**: install the previous operator, create an Agent with a `secretRef`, upgrade, delete the Agent. `kubectl get secret -n <ns> -l plume.dev/agent-uid` shows the snapshot outliving the Agent that justified it — the exact leak A23/A56 called the cost of copying.

**Fix**: either (a) for one release, `finalize` and `collectGarbage` also sweep `agent.Namespace` using the existing `isRevisionMaterial` / `isRevisionWorkload` authority (name shape + UID; nothing is deleted for wearing a label) and delete a same-named `ownerReference`'d Deployment there; or (b) state in design 02 §3.2 and the chart's upgrade notes that there is no upgrade path from a pre-A42 operator (P1 is pre-release and this may be acceptable) — but say it. Rule 7: a claim of upgrade-awareness the code does not implement.

## MAJOR 8 — Two load-bearing paths are pinned by nothing: row 9, and the Namespace-keyed reconciler

**File**: `internal/controller/runnamespace.go:435-448` (row 9); `agent_controller.go:1285-1305` (`RunNamespaceReconciler`).

Mutation **M1** (row 9's `case` made unreachable): the full unit and envtest suites pass. Without row 9, `Bound` + namespace absent falls to row 11 and a cluster-rights deletion of a run namespace becomes **terminal `NotCreatedByOperator`** rather than the recreate the design promises — and no test notices. Row 9 is reachable in envtest (plant a `Bound` binding whose namespace does not exist before the first reconcile; my baseline probe did, and passed on the unmutated code).

Mutation **M2** (`RunNamespaceReconciler.Reconcile` returns immediately): the full unit and envtest suites pass. The design says the Namespace-keyed reconciler "exists precisely so the handler runs when `S` holds no Agent at all"; the manager tests start it and never give it a `Terminating` binding to act on. Its only construction is inside `SetupWithManager` and its field is unexported, so only a manager test can reach it. My baseline probe (`provisionRunNamespace` → binding `Terminating` → `startManager` → `eventually` deletion timestamp and `Deleting`) passed on the unmutated code, so it is testable in ~10 lines.

A61's history entry says "every row envtest can reach has a case in `test/envtest/runnamespace_test.go`". Row 9 can be reached and has none.

**Fix**: add both tests (the two probes, essentially verbatim), then re-run M1 and M2 and record them in the ledger.

## MAJOR 9 — The name authority for workload deletion is unpinned, and the test that claims to pin it passes with the name check deleted

**File**: `internal/controller/agent_controller.go:1157-1165` (`isRevisionWorkload`); `test/envtest/reconciler_blockers_test.go:220-262` (`TestOwnershipIsDecidedByControllerRefNotLabels`).

Design 02 §3.2 (A60 workload-provenance row) and A57: "the **name** is the deletion-safety authority — immutable after creation, so a victim object cannot be renamed into the shape"; the UID label is "corroboration, not authority". `isRevisionWorkload` implements that: revision label, UID label, then `d.Name == WorkloadName(agent.Name, rev)`.

Mutation **M8** (`return d.Name == WorkloadName(...)` → `return true`, so ownership is decided by the two labels alone): the full unit and envtest suites pass. The test whose failure message reads "ownership must be decided by the name — which nobody can forge by labelling (A60)" plants impostors carrying the agent and revision labels but **not** the UID label, so the UID check saves them and the name check is never the deciding predicate. Its name still says `ControllerRef`, which no longer exists. This is rule 1 verbatim: an unenforced rule converted into a documented guarantee.

Under A42 the practical exposure is small — nothing but the operator writes in a run namespace — but that is the same argument the design rejects for `AlreadyExists` ("that principal does not exist by construction … and `AlreadyExists` is never provenance"), and the UID is public. A stray `plume.dev/agent-uid` label anywhere the operator can list is one `update` away from being a delete.

**Fix**: plant impostors with the Agent's real UID label and a plausible revision label under a name that is not `<agent>-<revision>`; assert they survive `collectGarbage` and the finalizer. Rename the test to what it now proves. Re-run M8.

---

## MINOR

1. **`isNamespaceTerminating` is dead code** (`runnamespace.go:239-244`); the design's "the operator treats that cause as 'wait', never as an error to retry against" is not implemented — a create into a Terminating run namespace surfaces as a material error (reported on the object, retried, so not silent). Pin it in the material path or delete it and amend the sentence.
2. **Verbs granted that nothing calls** (`agent_controller.go:164-178` markers → `config/rbac/role.yaml`, `charts/plume/files/operator-rules.yaml`): `namespaces/patch`, `resourcequotas/patch`, `limitranges/patch`, and `list`/`watch` on both admission kinds (`LabelAuthorityPresent` uses the API reader's `Get`; no informer is started, as `main.go`'s comment says). Design 07 A5.7's own rule: "a verb granted ahead of the code that uses it is rule 7 in RBAC form". `TestOperatorRoleGrantsWhatTheRunNamespaceCodeCalls` checks nine of the ~20 verbs the code calls and asserts nothing is absent. Drop the four unused verbs from the markers; extend the SAR list to every call site and add a negative SAR for `namespaces/patch`.
3. **No startup check.** Design 02 §3.2 and design 07 A5.9: "at startup and on every run-namespace creation". `main.go` never calls `LabelAuthorityPresent`. The per-reconcile check is stricter in one direction (it runs on every reconcile, not only creation, so deleting the policies takes every Agent to terminal `Degraded` with no retry path but an annotation), and absent in the other. Amend the text to what the code does, or add the startup call.
4. **Decorative predicate** (`agent_controller.go:1268-1278`): `predicate.Funcs{DeleteFunc: func(...) bool { return true }}` is a no-op — nil members of `Funcs` already return true, and it is ANDed with `isRun`, which `NewPredicateFuncs` already applies to delete events. The comment claims it keeps deletes; it changes nothing. Delete it.
5. **Defensive, unpinned** (rule 5) — five mutations survived the whole suite: `deleteRunNamespace`'s name-shape check (M3; every caller already passes a `plume-run-` name, so it is unreachable — say so or drop it), `isMirror`'s annotation check (M4; plant a `plume-mirror-x` quota with no `mirror-source` annotation in the run namespace and assert it survives the sweep), `LabelAuthorityPresent`'s binding check (M6; the test installs policy and binding together — install the policy alone and assert `false`), `workloadAvailable`'s UID check (M7; `markAvailable` a same-named, digest-stamped Deployment carrying another UID and assert no promotion), and row 6's mirrors-before-`Bound` order (M21; not observable in envtest without fault injection — the design sentence "the mirrors come before `Bound` so the first Pod cannot precede its quota" should say it is unpinned, or the test should inject a failing `mirrorInto` and assert the binding stays `Creating`).
6. **Presence is not enforcement** (`runnamespace.go:252-272`): the fail-close check is satisfied by any `ValidatingAdmissionPolicy` and binding of the right names — the envtest test installs ones that "validate nothing". The design's "fail-closes on their absence" should say "on their absence by name; their content is the chart's". Checking `spec.validations` is not worth it; saying so is.
7. **`readBinding` claims "UNCACHED"** (`runnamespace.go:492-493`) by relying on `cmd/operator`'s `DisableFor`. Use `r.reader()` there so the property is local to the code that needs it, and MAJOR 5's fix then covers every read the protocol depends on.
8. **External Agents keep a run namespace alive** they never use: `runNamespaceStillNeeded` counts every Agent in `S`; `reconcileExternal` never enters `ensureRunNamespace`. Harmless today; say it or exclude `Spec.External != nil`.
9. **Rule 7 in a comment** (`admission.yaml:22-24`): "The tenant-operator (design 26 A1) is appended here when the enterprise module is installed" — nothing implements appending. MAJOR 2's fix (identities from values) makes this true.
10. **`LabelBinding` "so they can be listed"** (`runnamespace.go:68-70`): nothing lists them. Fine to keep; fix the comment.
11. **Row 1's message** offers a re-bind procedure that requires the human to know the source namespace's UID and to author a `schemaVersion: 1` ConfigMap with seven fields by hand; the message names three of them. Either name the fields or point at a documented procedure — design 02 §3.2 calls it "the re-bind procedure below" and there is no procedure below.
12. **`TestARefusedRunNamespaceIsNotReady`** (`test/envtest/runnamespace_test.go:713-715`) has an `if apierrors.IsNotFound(err) { t.Error("unreachable") }` after an `err != nil` check — a dead assertion.

---

## Mutation ledger (this review)

Method: `cp` backup → exact-string replacement (fails if the target is not unique) → `go build ./... && go vet ./internal/controller/ ./test/envtest/` → `go test ./internal/controller/` → `go test ./test/envtest/` → restore from backup → byte-compare. Probes removed from the tree for these runs so only the committed suite counts. INVALID means the mutant did not build or vet.

| # | Mutation | Result | Killed by |
|---|---|---|---|
| M1 | Row 9 unreachable (`Bound` + absent → row 11) | **SURVIVED** | — (MAJOR 8) |
| M2 | `RunNamespaceReconciler.Reconcile` returns immediately | **SURVIVED** | — (MAJOR 8) |
| M3 | `deleteRunNamespace` name-shape check removed | **SURVIVED** | — (MINOR 5) |
| M4 | `isMirror` annotation check removed | **SURVIVED** | — (MINOR 5) |
| M6 | `LabelAuthorityPresent` skips the binding check | **SURVIVED** | — (MINOR 5) |
| M7 | `workloadAvailable` UID-label check removed | **SURVIVED** | — (MINOR 5) |
| M8 | `isRevisionWorkload` name check removed (labels only) | **SURVIVED** | — (MAJOR 9) |
| M9 | Row 4 swaps to `Terminating` from any state | KILLED | `TestARecreatedSourceNamespaceTearsDownTheOldRunNamespace` (the second pass reopens `Deleting` to `Terminating`; the test's final-state assertion catches it — pinned, if by a side effect) |
| M21 | Row 6: `Bound` swapped before mirrors | **SURVIVED** | — (MINOR 5) |
| M24 | Handler step 0 (state guard) removed | KILLED | `TestManagerReconcilesAnAgentEndToEnd`, `TestManagerWatchesOwnedWorkloads` (the Namespace reconciler deleted a `Bound` namespace under a live Agent) |
| M25 | [ledger spot-check] row 7/8 nonce comparison removed | KILLED | `TestANamespaceWithTheWrongNonceIsRefusedWhileCreating` |
| M26 | [ledger spot-check] handler step 1 UID comparison removed | KILLED | `TestTheHandlerComparesTheUIDBeforeRestoringBound` |

Eight of twelve survived. Two of the survivors are rows the design calls load-bearing (M1, M2), one is the deletion authority the design names (M8), and five are defensive code rule 5 applies to (M3, M4, M6, M7, M21). The two A61-ledger spot-checks (M25, M26) are honest, and the A61 ledger's sixteen entries are not disputed — they pin what they name; the survivors here are what they did not name.

---

## What is right, and should not be relitigated

- The binding record, strict decode, nonce-then-UID split, bind-from-`Create`-only, delete-not-adopt, and the two-swap fence are implemented as designed and — apart from the rows above — pinned. `TestANamespaceObservedWhileCreatingIsDeletedNotAdopted`, `TestADeletedAndRecreatedRunNamespaceIsRefusedEvenWithTheNonceCopied`, `TestTheHandlerComparesTheUIDBeforeRestoringBound` and `TestADeletingBindingDoesNotFlipBack` are the right tests, each wrong in one way.
- The e2e is real evidence: real RBAC, an impersonated principal, an own-namespace control, and the admission denial from a cluster-admin identity. It ran green on k3d for this review.
- `isRevisionWorkload` and `ownedWorkloads` decide by name shape and UID; nothing in the run namespace is deleted for wearing a label (A57 carried over correctly). The `byAgentLabels` and `byNamespace` map functions can be triggered by anyone who can label a Deployment or write a quota anywhere, and what they trigger is an idempotent reconcile whose every write is gated by the binding — I found no path from a stray label to a delete.
- The generated RBAC is reproducible and the chart's copy matches it.

## Verdict

**REVISE.** The two BLOCKERs are each a few lines and each has a failing test written for it in this review (deleted; the shapes are in the findings). MAJOR 1 and MAJOR 2 are chart lines with cluster-wide consequences. MAJOR 3–5 need a design amendment in the same change, per AGENTS.md ("code that diverges from an approved design is drift … amend the design first, as a numbered amendment, in the same change") — in these three the design is what needs the amendment. After the fixes: re-run this ledger plus A61's, and run the cross-family pass the project's own rule asks for.
