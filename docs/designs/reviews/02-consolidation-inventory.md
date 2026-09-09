# Design 02 — consolidation inventory

**Scope**: the authoritative body of `docs/designs/02-agent-crd-operator.md`, header + §1–§11 (lines 1–494).
§12 (lines 495–841) is provenance and is out of scope.

This is an **inventory pass, not a verdict pass**. No file was edited. Every claim below was checked
against `internal/controller`, `internal/revision`, `api/v1alpha1`, `charts/assayd`, `test/envtest`,
`test/chart` and `test/e2e` at the working tree of 2026-09-04.

## Counts

| Class | Count |
|---|---|
| **A — CONTRADICTION** (two body sentences that cannot both be true) | 13 |
| **B — STALE STATUS** (owed/future/"today" that is now done, or "implemented" that is not) | 19 |
| **C — NARRATED RETRACTION** (the body tells the story of an earlier wrong version) | 41 |
| **D — HISTORY LEAK** (an amendment number, review round or reviewer cited as if the reader has the history) | 26 named sites, on top of a blanket **139 bare `A<n>` tags across 89 body lines** |
| **E — UNENFORCED CLAIM** (present-tense claim nothing in the repo enforces, with no disclaimer) | 12 |
| **F — DUPLICATE** (same rule in two places, different wording) | 25 pairs |
| **G — DEAD ANCHOR** (a §x.y, field, test or file that does not exist, or does not say what is claimed) | 13 |

**149 findings across 488 body lines.** The dominant defect is not error but *sedimentation*: C + D + F
account for **92 of the 149**, and every one of those is a sentence a reader must decode against history
the consolidated body will not carry.

The substantive defects — the ones the rewrite must **decide**, not tidy — are eight:
**A-6** (§3.3 still specifies the pre-A57 material rule that shipped code deliberately does not implement),
**A-10** (the charter-gate line denies operator state that §3.2 creates),
**E-1 / E-2 / E-3** (three admission guarantees that no CEL rule, webhook or policy provides),
**E-4 / G-6** (the tool-name uniqueness rule that justifies a discriminator-free binding is owned by no design),
**G-8** (`assayd logs` is the only supported run-namespace read path and design 08 does not describe it),
and **E-12** (the cold-candidate eval premise is attributed to a design that does not state it).

---

## A — CONTRADICTION

**A-1. The `--operator-namespace` flag both does and does not exist.**
- L151: "The operator is to learn that namespace from a `--operator-namespace` flag, defaulting to the ServiceAccount namespace file every Pod mounts, and refuse to start with neither; **the flag does not exist yet**."
- L210: "The chart renders … and **passes `--operator-namespace` from the downward API**."
- Repo: `cmd/operator/main.go:62` defines it, `:80` refuses to start without it, `charts/assayd/templates/operator.yaml:70` passes `--operator-namespace=$(POD_NAMESPACE)`, and `test/chart/chart_test.go:608 TestOperatorIsToldItsNamespace` pins it.
- **Fix**: keep L151's rule (learn from the flag, default to the SA namespace file, refuse with neither); delete the "does not exist yet" clause and L210's restatement.

**A-2. A42 is both "blocked on unwritten amendments" and "implemented".**
- L115: "**A42 is the step blocked on unwritten amendments in designs 03, 06, 07 and 26**, and bundling the other two behind it keeps a live bypass live for however long that takes."
- L121 / L123 / L125 / L210: "**none of the three**: implemented (A61)…", "All three are implemented", "**A42 IS IMPLEMENTED**".
- **Fix**: delete the whole L115 delivery-order paragraph and the L117–121 step table. The sequencing argument was scaffolding for a delivery that has happened; the consolidated body should state only the invariant each step produces.

**A-3. The read-only RoleBinding is retracted as unimplementable, then owed, then pinned by a chart test.**
- L138: "An earlier version of this row had the operator derive 'every subject that can read Agents in the source namespace' … **That is not implementable** … Direct `kubectl logs` in a run namespace is **not supported**, and **nothing grants it**."
- L396: "no Role or RoleBinding granting Pod `create` in a run namespace is rendered by the chart, and **the read-only RoleBinding this section's table owes must stay read-only**."
- L474 (§8): "and **a chart test that fails if the read-only RoleBinding's Role ever carries a verb outside `get`/`list`/`watch`**."
- Repo: `charts/assayd/templates/rbac.yaml` renders one ClusterRole + ClusterRoleBinding for the operator and no per-namespace read-only RoleBinding; no such chart test exists in `test/chart/chart_test.go`.
- **Fix**: L138 is the surviving rule. Delete the clause in L396 and the §8 line in L474. L396's hardening note ("the hardening to take if the read-access RoleBinding ever grows a write verb") must be rewritten to say the RoleBinding does not exist.

**A-4. §6 asserts a NetworkPolicy that §3.2 says is not applied anywhere.**
- L142: "**The chart ships no NetworkPolicy at all today**, so that control is currently decorative in either namespace … **At P1 no NetworkPolicy is applied anywhere**."
- L466 (§6): "Workload pods: **default-deny NetworkPolicy (egress = gateway only)**, non-root, read-only rootfs."
- Repo: `grep -r NetworkPolicy charts/` returns nothing.
- **Fix**: §6 must state the control *and* that nothing applies it at P1, or point at the §3.2 row. A security section that lists an absent control as shipped is the exact shape NFR-8 exists to stop.

**A-5. The admission policy is both "not a params object" and rendered "with the `assayd-operators` params".**
- L144: "**Not a params object**: a params ConfigMap that went missing would have turned the policy into a cluster-wide denial of every namespace write."
- L210: "The chart renders design 07 A5.9's two admission policies **and the `assayd-operators` params**."
- Repo: `charts/assayd/templates/admission.yaml` renders the operator identities *into* the CEL (`$operators`, from `.Values.admission.extraOperators`); there is no `assayd-operators` object and no `paramKind` in either policy.
- **Fix**: delete "and the `assayd-operators` params" from L210.

**A-6. Revision-material provenance: label authority vs. A57's name-and-content authority.**
- L278 (Ownership / GC row): "**A label, not an `ownerReference` (A44)** — `assayd.dev/agent-uid` and `assayd.dev/revision-digest`, stamped at creation and covered by the `RevisionRecord` digest."
- L281 (Missing or wrong copy row): "if a copy is absent, or its `assayd.dev/agent-uid` **label** or content digest disagrees with the record (A44), the revision's route goes to **weight 0** with `RevisionMaterialUnavailable`."
- L295: "A copy named for this revision that this operator did not just create must satisfy the whole invariant — same kind, **matching `assayd.dev/agent-uid` and `assayd.dev/revision-digest` labels (A44)**, immutable, byte-identical — or it is `RevisionMaterialCollision` and the revision is not published."
- L134: "**every rule that tested a copy's owner UID is rewritten against a label** … the record is the *only* provenance."
- L137 states the opposite for workloads: "The name is the **deletion-safety** authority — immutable after creation, so a victim object cannot be renamed into the shape (A57)."
- Repo (A57, which the user confirms is implemented): `internal/controller/material.go:126–135` — "**CONTENT FIRST (A57).** `immutable: true` freezes data, not labels or annotations … The bytes are the only thing the Pod reads. If they match, this IS the material the revision was minted from, whatever the metadata says, so the operator **restamps** rather than refusing." And `:241` — "The **NAME** is what an attacker cannot forge … Everything else is corroboration."
- **This is the single most dangerous item in the inventory.** An implementer following L281/L295 as written re-introduces the denial-of-service A57 removed: a principal with `update` annotates a copy and Degrades the agent permanently.
- **Fix**: rewrite L278/L281/L295 to A57's rule — name = delete authority, content = integrity authority, labels/annotations = corroboration that is restamped on mismatch — and say so once.

**A-7. Classification is leaf-level, and the test that proves it walks top-level only.**
- L308: "**Classification is compulsory, not defaulted, and it is LEAF-level (A16).** Every `AgentSpec` field is named in exactly one column, and **`TestEveryFieldIsClassified` reflects over the struct to prove the table is exhaustive**."
- L310, the very next sentence: "That test walks **top-level** `AgentSpec` fields only, which is the wrong shape and **made the exhaustiveness claim false**."
- Repo: both are half-true, which is worse. `internal/revision/classification_test.go:53 TestEveryFieldIsClassified` still walks top-level fields, descending only into `Runtime` and `External` (`descend` map, line 51). The leaf-level guarantee lives in a *different* test — `internal/revision/leaf_test.go:206 TestEveryAgentSpecLeafBehavesAsClassified` over `internal/revision/leaves.go`'s `Leaves()` walk and the 80-entry `specLeafClass` map, plus `test/envtest/leaf_admission_test.go`.
- **Fix**: state the leaf rule once and name the test that actually enforces it. Delete L310.

**A-8. The printer-column set is six columns, five sources, and called "these five".**
- L93: "printer columns: `PHASE · ACTIVE · CANDIDATE · EVAL(last score) · COST/DAY · AGE` (A14), sourced from `status.phase`, `status.activeRevision`, `status.eval.score`, `status.budget.usdSpentToday` and the creation timestamp. … the case for a status carrying this many conditions rests on **these five** answering the common questions."
- Six columns; five sources listed; `status.candidateRevision` — which the CRD does source (`api/v1alpha1/agent_types.go:592`) — is missing from the source list, and "five" is wrong either way.
- **Fix**: six columns, six sources, "these six".

**A-9. Two different four-item lists of what admission enforces.**
- L414 (§4): "**Admission has already enforced only what CEL can express** (A21): digest-pinned image, prod-gate presence, sandbox/replicas exclusivity, **the `usdPerDay` grammar**."
- L466 (§6): "Admission, and what actually enforces each (A21 …): digest-pinned image (CEL on the schema) · prod-gate presence (CEL) · sandbox/replicas exclusivity (CEL, ADR-0027) · **`expose: public` requires an approval label (CEL)**."
- Both present themselves as the complete enumeration and their fourth items differ. Repo says both fourth items are absent, and so is the second — see E-1, E-2, E-3.
- **Fix**: one list, in §6, with the mechanism against each; §4 points at it.

**A-10. "Stateful deps: none. Operator state = CR status + JetStream KV" vs. the binding record.**
- L18: "**Stateful deps**: none. **Operator state = CR status + JetStream KV (directory)**. **One additional read-only reconcile input**: design 04's per-agent daily spend aggregate."
- L151: "For every source namespace `S` the operator keeps a **binding**: a `ConfigMap` named `assayd-run-binding-<run-namespace>` in the **operator's own namespace**." This is durable, authoritative, non-CR, non-KV operator state whose loss is unrecoverable without a human (L204). §3.2 also reads ResourceQuotas, LimitRanges, Namespaces and two ValidatingAdmissionPolicies, so "one additional read-only input" is false.
- L424 repeats the false form: "**no state outside CR status + directory KV**."
- **Fix**: §2 must name the binding record as operator-owned state, and L424 must be corrected. This is a doctrine claim ("Postgres+NATS only"/stateful deps) and it is currently wrong in the charter-gate section.

**A-11. The step-table residuals are stated in the present tense against implemented steps.**
- L119: "**A20** … a source edit now mints a candidate and is gated before promotion; **today the edit is *invisible***" — the same cell asserts both.
- L120: "**A35** … Residual: a namespace editor **can still** delete and recreate the copy."
- L99/L113 say A20, A35 and A42 are all implemented and that the delete-and-recreate path is closed.
- **Fix**: delete the table (see A-2).

**A-12. "Mirroring is not implemented until the sentence in §3.2's implementation note says it is."**
- L139 ends with that instruction; L210's implementation note says "**ResourceQuota and LimitRange mirroring**" runs, and `test/envtest/runnamespace_test.go:620 TestQuotaAndLimitRangeAreMirrored` and `:1036 TestMirrorsLandBeforeTheBindingIsBound` pin it.
- A self-referential status latch is not a rule. **Fix**: delete the sentence.

**A-13. The ordered-check table has thirteen rows, two of them numbered 7, and is called twelve.**
- L177 and L178 are both "| 7 |". L210: "**the twelve ordered checks above**"; L208: "**One envtest case per numbered row above**"; L174 cites "row 7's authority" ambiguously.
- **Fix**: renumber to 1–13 and correct both references. Until then no reader can say which case "row 7" names.

---

## B — STALE STATUS

Ground truth used: `A20, A21, A35, A37, A42, A50, A53, A56, A57` are implemented, plus what the working
tree independently shows (`A16`, `A25`, `A30`, `A39`, `A41`, `A47`, `A48`, `A51`/`A52`, `A60` are in
code and tested). Nothing else is.

**B-1. L3 (header) — the implemented list omits A57.**
> "**A20, A21, A35, A37, A42, A50, A53, A56 are IMPLEMENTED**"

A57 is implemented (`internal/controller/material.go`, content-first + name-authority) and is named two
clauses earlier as a code-review finding. **Fix**: add A57 — or, better, delete the whole implemented
list from a body that is supposed to be status-free and let §12 carry it.

**B-2. L3 — "none critique-passed: every round has returned REVISE" and "Latest review: `reviews/03-codex-review-r8.md`".**
A status line that narrates review history belongs in §12. It also cites a *design 03* review file as
this design's latest review (see G-7).

**B-3. L5 — the reviews list is `reviews/02-review.md`, `reviews/02-recritique.md` only.**
`docs/designs/reviews/` also holds `02-codex-review.md`, `02-a60-critique.md`, `02-a60-critique-r2.md`,
`02-a60-critique-r3.md`, `02-a60-codex-review.md`, `02-a61-code-review.md`.

**B-4. L119 — "today the edit is *invisible*".** A20 is implemented (`internal/revision/revision.go` `Digest`
folds `EnvSourceDigests`; refuses on `MissingSources`/`UnknownEnvArms`). See A-11.

**B-5. L120 — "a namespace editor **can still** delete and recreate the copy".** Closed by A42/A61; proved by
`test/e2e/e2e_test.go:495 TestANamespaceEditorCannotReplaceTheRevisionsCopy`.

**B-6. L121 — "**none of the three**: implemented (A61) **after the design 03, 07 and 26 amendments and A60**".**
A residual column whose entry is a delivery note. Delete with the table.

**B-7. L123 — "the cluster test A42 owed **is in `test/e2e`**".** True but written as the closing of an owed
item. State the test as the contract, not as a debt discharged.

**B-8. L133 — "**§8 owes a max-length fixture**".**
`internal/controller/runnamespace_unit_test.go:17 TestRunNamespaceNameIsADNSLabelAndInjectiveAtTheLimit`
exercises two 63-character source names sharing a 36-char prefix and asserts injectivity and the 63-byte
bound; `:44 TestMirrorNameStaysWithinADNSSubdomain` covers L206's mirror names. **Fix**: name the tests.

**B-9. L139 — "**Mirroring is not implemented until the sentence in §3.2's implementation note says it is**".** See A-12.

**B-10. L151 — "the flag does not exist yet".** See A-1.

**B-11. L208 — the whole "**What §8 owes for this block, by name**" block.**
Almost all of it is discharged: `test/envtest/runnamespace_test.go` carries 29 cases including the
per-row cases, the crash-gap cases (`TestANamespaceObservedWhileCreatingIsDeletedNotAdopted`,
`TestTheCrashGapRecoveryWaitsRatherThanWedging`, `TestMirrorsLandBeforeTheBindingIsBound`), the UID-first
handler check, the source-UID scoping, the `Deleting` no-flip-back rule and the strict decode
(`internal/controller/runnamespace_unit_test.go:56 TestBindingRecordDecodesStrictly`); the real-cluster
cases are in `test/e2e/e2e_test.go:495/598/636`. The sixteen-entry mutation ledger was run per L210.
**Fix**: this block is a work order, not a spec. It belongs in §8 as a *coverage* statement, with the
three genuinely-uncovered cases L210 names carried forward.

**B-12. L210 — the implementation note itself.**
Correct as of today, but it is a changelog paragraph inside §3.2. Its two durable halves — what the run
namespace code guarantees, and the three cases envtest cannot reach (operator restart mid-protocol, the
paused delete racing a new Agent, the chosen name collision) — belong in §3.2 and §8 respectively; the
rest belongs in §12.

**B-13. L226 / L228 — A47 and A48 are stated as corrections in flight.**
Both are implemented: `internal/controller/agent_controller.go:554` (`RevisionDigestAnnotation`), `:573`
(the absent-annotation refusal message), `:610–617` (terminal `RevisionHashCollision` on
Ready/Degraded), `internal/controller/conditions.go:60` (owned, so it clears). The *rules* survive; the
"an earlier version of this rule …" framing does not.

**B-14. L256 — "**Changing this table is a hash migration, and the code has now caught up (A30).**"**
`internal/revision/revision.go` projects `Budget`, `Expose` and `toolBind.RequiresApproval`; the four
migrations are recorded in `internal/revision/golden_test.go:105–141` (MIGRATION 1 = A25+A30,
2 = A37, 3 = A53, 4 = A20). **Fix**: keep "changing this table is a migration"; delete the catch-up story.

**B-15. L310–320 — the seven-leaf "not projected" table is stale.**
> "Confirmed by direct differential mutation — two specs differing in exactly one leaf, same revision hash"

All seven are projected now. `llm.egressAllowlist` is a projected, sorted set (`revision.go` `llm.EgressAllowlist`);
`env[].valueFrom.*` and `envFrom[].*` are covered because `envSource`/`envFromSource` marshal the *whole*
upstream selector; all seven appear in `specLeafClass` as `mints` (`internal/revision/leaves.go:150–200`).
**Fix**: the table is history. The surviving rule is the one at L322 ("classification and projection are
over the transitive leaf graph") and the leaf-vs-aggregate override precedence at L322–324.

**B-16. L282 — the operator-privilege row understates the shipped privilege, and names the wrong namespace.**
> "the operator needs `create` on `Secrets` **in agent namespaces**, on top of the `get` A20 already required"

`charts/assayd/files/operator-rules.yaml` grants `create, delete, get, list, update, watch` on
`configmaps, limitranges, namespaces, resourcequotas, secrets` cluster-wide. `delete` is required by A56's
sweep and §3.7; `configmaps` by A35's copies and the binding record; and under A42 the copies are in the
run namespace, not agent namespaces. A row whose stated purpose is to expose the escalation surface
currently understates it. **Fix**: state the actual verb set and the actual namespaces.

**B-17. L299 / L301 — "the typed credential binding … does not exist yet and is owed (A23)".**
Still true (no such field in `api/v1alpha1`), but the sentence carries A23's retraction with it. Keep the
owed item; drop the A23 archaeology.

**B-18. L398 — "**Done and owed:** design 07 A5 records the `ClusterSPIFFEID` **the chart renders**".**
Also L140: "**Design 07 A5** records the template and selectors **the chart renders**".
`grep -ri spiffe charts/` matches nothing, and design 07 A5.2 is explicitly future tense: "*When* the
SPIRE subchart lands, `templates/` renders exactly one platform-owned `ClusterSPIFFEID`", closing
"**Today**: the chart carries no SPIRE subchart and renders no `ClusterSPIFFEID`; no Agent has an SVID"
(design 07 :242 lists the SPIRE subchart as Owed). Design 02 states in the present tense what design 07
states in the future tense, twice — and then, one clause later at L398, admits "it needs SPIRE, which
nothing installs today". **Fix**: "design 07 A5 specifies the `ClusterSPIFFEID` the chart will render;
no SPIRE subchart ships today, so no Agent has an SVID."

**B-19. L466 — "an earlier version of this line asserted all four as enforced and none was".**
The corrected line still asserts two that are not enforced (E-1, E-3). A line whose subject is "what
actually enforces each" is the last place a stale claim may sit.

---

## C — NARRATED RETRACTION

Every item below is the body arguing with a previous version of itself. In a consolidated body each one
collapses to the rule that holds plus, where the *reason* is load-bearing, the counter-example — with the
history moved to §12. Marked **[keep the counter-example]** where deleting the story would delete the
argument.

| # | Line | Quote (opening) | Collapses to |
|---|---|---|---|
| C-1 | 115 | "An earlier sentence here called the three 'one implementation' and said partial delivery 'closes nothing'. That is false in two of the three directions…" | delete entirely (A-2) |
| C-2 | 127 | "`immutable: true` forbids an update and permits exactly that, **the same distinction that refuted A20**" | **[keep the counter-example]** — the delete-and-recreate fact is why A42 exists; drop "refuted A20" |
| C-3 | 134 | "This matters more than it reads — A35's ownership row, A41's missing-copy check, A39's `AlreadyExists` invariant and A38's rollback test all read 'owner UID', and under A42 that predicate is unsatisfiable." | the rule: no run-namespace object carries an ownerReference; provenance is name + label + record |
| C-4 | 136 | "**The label is not the proof** — Codex r8 BLOCKER 7: Kubernetes records no 'created by'…" | **[keep the counter-example]** — the "a label needs only `update` to forge" fact; drop the citation |
| C-5 | 138 | "An earlier version of this row had the operator derive 'every subject that can read Agents…' **That is not implementable**" | **[keep the counter-example]** — "Kubernetes answers 'may this subject do this?' and never 'who may do this?'" is the reason `assayd logs` is the path |
| C-6 | 149 | "Codex r8 BLOCKER 7 and MAJOR 3 are the same gap seen twice: A44 said 'created, never adopted' and gave the operator nothing durable to check that with" | "the binding record exists because a label is forgeable and a namespace has no creator field" |
| C-7 | 163 | "…and the cross-family review showed the RoleBinding simply arrives after the check." | **[keep the counter-example]** — why an "empty now" check is not a proof |
| C-8 | 177 | "Refusing here wedged the operator's own recovery terminally, because the pass after the delete met its own Terminating namespace with a rotated nonce." | the rule: a deletion timestamp means wait, whatever the nonce |
| C-9 | 187 | "…a handler that deleted the binding there would race row 9's rebuild into row 1's terminal refusal with no attacker and no crash." | **[keep the counter-example]** |
| C-10 | 202 | "The window r8 described — B writes material while A deletes — needs B to have read `Bound` before A swapped…" | the ordering rule + why it is safe; drop "r8 described" |
| C-11 | 222 | "…and an earlier justification defended that with the number of revisions that coexist — which answers an accidental-collision question nobody asked." | **[keep the counter-example]** — chosen vs accidental collision is the whole point of A37 |
| C-12 | 226 | "An earlier version of this rule made the annotation authoritative and said nothing about who may write it." | **[keep the counter-example]** — the `deployments/patch` attack is why status is the authority |
| C-13 | 228 | "The adoption branch existed for a workload predating the field, and there is no such workload — assayd is unreleased…" | the rule: an absent annotation is refused, never adopted |
| C-14 | 232 | "It was in neither the owned nor the sticky set, which meant a repaired Agent reported `Ready=True` while still carrying a `True` collision condition…" | the rule: `RevisionHashCollision` is owned, so it clears |
| C-15 | 241 | "An earlier wording defined the policy surface as 'whatever §3.6 compiles', which would have classified those three as policy…" | **[keep the counter-example]** — "the discriminator is capability, not the compile path" needs its foil |
| C-16 | 256 | "`internal/revision` classified `budget` and `expose` as policy-surface — the pre-A25 split — … The table above said the opposite for over a round" | "changing this table is a hash migration"; see B-14 |
| C-17 | 262 | "It was policy-surface, and design 03's closed comparator registry has no approval concern — so `false → true` classified as `Unknown → Tighten`…" | **[keep the counter-example]** — the "no transaction at all" argument is why both directions mint |
| C-18 | 264 | "(Rotating the credential *behind* a client is a Secret update the hash never sees, which is correct and is **what the earlier wording confused it with**.)" | drop the last clause |
| C-19 | 267 | "**The first fix for this was wrong and is retracted.** A20 originally required referenced ConfigMaps to be `immutable: true`…" | **[keep the counter-example]** — "immutability binds contents to an *object*; it never bound a name to contents" is load-bearing and appears nowhere else |
| C-20 | 269 | "**Hashing detects a change; it does not stop the serving revision consuming one (A23).**" | **[keep]** as the reason A35 exists, without the A23 tag |
| C-21 | 278 | "…the retained set is defined once below (A40) — *not* three, **which counted named roles and contradicted this design's own retention arithmetic**" | drop the clause; the retained-set definition is the rule |
| C-22 | 297 | "**This reverses A23's 'no bytes are copied', and the reason it was reversed matters more than the reversal.**" | **[keep the counter-example]** — "sealing is a control, and a control that is removed stops controlling; a copy is an invariant" is the strongest sentence in §3.3 |
| C-23 | 299 | "No number is written here: 'three' survived A40's own rewrite twenty lines below the definition, which is how a reader going top-to-bottom meets the wrong count first." | delete; the retained set is defined once |
| C-24 | 301 | "**The exemption question is settled by fact, not by preference.** An earlier version said rotating a value inside a Secret is not a behaviour change…" | **[keep the counter-example]** — "Kubernetes does not refresh a process environment when a ConfigMap or Secret changes" is the fact the rule rests on |
| C-25 | 305 | "**A17 briefly made this gate asymmetric and A18 retracts it.**" (whole paragraph, ~250 words) | **[keep the counter-example]** — the `{A} → {A,B} → {B}` laundering walk is the reason the gate is symmetric; the A17/A18 frame goes |
| C-26 | 310 | "That test walks **top-level** `AgentSpec` fields only, which is the wrong shape and made the exhaustiveness claim false." | delete (A-7) |
| C-27 | 326 | "…the trap has been sprung once already: A12 rule 2 records `envSourceRef` naming four arms while k8s v0.36.4 had five, so `fileKeyRef` collapsed to a constant…" | **[keep the counter-example]** — it is the reason paths are discovered by reflection |
| C-28 | 328 | "A critic demonstrated the consequence: projecting `Divisor.Format` alone turns the leaf walk **green** … The mandated shape was satisfiable without doing its job — the same defect this amendment set killed twice already, relocated." | **[keep the counter-example]** — the reason the perturber registry fails closed |
| C-29 | 332 | "The graph was enumerated rather than estimated, and **it corrects this amendment's first draft on every count** … Of the four types the first draft named, three were wrong" | the measured facts (68 nodes, depth 10, exactly one unexported-state type); drop the first-draft autopsy |
| C-30 | 334 | "**The path walk is what catches drift; the registry is not, and the first draft claimed otherwise.**" | the measurement (v0.28→v0.36: +1 type, +5 leaves, 0 new unexported-state types) and the narrowed claim |
| C-31 | 336 | "…but '**the walk prunes there**' was doing double duty in the first draft" | the rule: classification reaches every leaf; projection never descends into policy aggregates |
| C-32 | 338 | "An earlier draft made '`AgentSpec` must not gain a self-recursive type' a *precondition* — which is not a guard" | **[keep the counter-example]** — `JSONSchemaProps`'s twelve self-recursion points, and INVALID ≠ KILLED |
| C-33 | 344 | "**A cheap reflection sweep is not this test.** One was run over the current projection: it reached 31 leaves…" | **[keep]** as the "sweep that finds something vs. sweep that proves absence" argument |
| C-34 | 346 | "An earlier version of A12 said new fields *default* to policy-surface and called that the safe direction — it is not; the critic demonstrated it by adding a `SystemPrompt` field…" | **[keep the counter-example]** — this is the argument for compulsory classification |
| C-35 | 361 | "**Three documents had been counting it differently**: this rule says history is *additional*… while design 03 A28 capped… and A35 bounded…" | the retained-set definition and its size; drop the three-way autopsy |
| C-36 | 388 | "**The template uses `index` (A60, correcting A59).** A59 wrote `{{ .PodMeta.Labels "…" }}`, which Go's `text/template` does not accept as a map lookup…" | the corrected template + the fact that spire-controller-manager's webhook rejects a non-parsing template |
| C-37 | 392 | "**The path segment is a LABEL, not `.PodMeta.Namespace` (A59), and this is an authorization change disguised as a move.**" | **[keep the counter-example]** — the `…/agent/assayd-run-team-a/reviewer` walk is why the label exists |
| C-38 | 396 | "One label carrying two trust boundaries was the critique's first blocker." | the rule: `pods-by` and `run-namespace` are distinct labels because they carry distinct trust boundaries |
| C-39 | 398 | "An earlier note listed a design 06 amendment as owed for this; checking the text rather than the note, the SPIFFE template was never design 06's." | delete |
| C-40 | 432 | "**Irrelevant (A38).** … The pre-A35 rule refused the rollback here, which would extend an incident by declining a recovery that is perfectly available" | the rule: a rollback target's *source* drifting is irrelevant; only its copies matter |
| C-41 | 466 | "(A21 — an earlier version of this line asserted all four as enforced and none was)" | delete; state what enforces each |

Two more that read as retraction but are really *forward* statements and should survive as rules:
L101 ("A revision identity can no longer be produced from a spec alone") and
L147 ("A41's detection stays, now testing the **label**, not an owner reference") — the latter must be
rewritten to A57's name-and-content authority (A-6).

---

## D — HISTORY LEAK

### D-0. The blanket measurement

The body (lines 7–494) carries **181 `A<n>` tokens on 96 lines**. Stripping legitimate cross-design
citations (`design 03 A44`, `design 07 A5.9`, `design 26 A1`, …) leaves **139 bare design-02 amendment
tags on 89 lines** — roughly one in every four body lines. Forty-two distinct amendment numbers are
cited. A self-contained body carries none of them: it states the rule and, where the rule is
counter-intuitive, the counter-example that forces it.

Body lines carrying a bare design-02 amendment tag:
30, 50, 53, 59, 69, 72, 93, 99, 109, 111, 113, 115, 119, 120, 121, 123, 125, 127, 134, 135, 136, 137,
138, 139, 140, 141, 142, 143, 144, 147, 149, 180, 208, 210, 222, 226, 228, 234, 236, 248, 251, 252, 256,
262, 265, 267, 269, 271, 277, 278, 280, 281, 282, 284, 295, 297, 299, 301, 303, 305, 308, 326, 334, 338,
346, 348, 361, 365, 388, 392, 396, 404, 408, 414, 430, 431, 432, 433, 434, 435, 436, 437, 438, 439, 452,
455, 457, 466, 474.

Note that **§5's failure-mode table (L430–462) tags fourteen of its thirty-three rows with an amendment
number**. A failure-mode table is the single most-read artefact in an operator design; a reader
diagnosing `RunNamespaceUnavailable=NameCollision` at 3am does not need to know it was A60.

### D-1..D-26. Named sites — a review round, a reviewer, or an amendment as the *subject* of a sentence

| # | Line | The leak |
|---|---|---|
| D-1 | 3 | "Latest review: `reviews/03-codex-review-r8.md` (13 BLOCKER, 6 MAJOR), plus two independent code reviews whose findings are A47, A52 and A57" |
| D-2 | 3 | "amendments **A1–A61** folded into the body, all critique-driven and **none critique-passed**: every round has returned REVISE" |
| D-3 | 115 | "**They ship in an order, and each step shrinks the bypass (A46).**" — an amendment as the subject |
| D-4 | 136 | "**The label is not the proof** — Codex r8 BLOCKER 7" |
| D-5 | 144 | "**Label authority (A60, from the cross-family review)**" — a table row header naming a review |
| D-6 | 149 | "**Codex r8 BLOCKER 7 and MAJOR 3** are the same gap seen twice" |
| D-7 | 163 | "the cross-family review showed the RoleBinding simply arrives after the check" |
| D-8 | 202 | "**The window r8 described**" |
| D-9 | 208 | "**What §8 owes for this block, by name (A60, from the critiques).**" |
| D-10 | 208 | "Codex r8's four (pre-creation with a self-granting RoleBinding, …) plus **r8 MAJOR 3's** paused delete racing a new Agent, plus **the first critique's** run-namespace delete-and-recreate" |
| D-11 | 210 | "**A42 IS IMPLEMENTED (A61's implementation note).**" and "the sixteen-entry mutation ledger **in the A61 history entry** was run" — the body pointing into §12 for its own evidence |
| D-12 | 226 | "**`status` is the authority, and the annotation corroborates it (A47).**" |
| D-13 | 228 | "An **absent** annotation is **refused**, not adopted (A48)." |
| D-14 | 236 | "**The hash covers a projection of spec, not all of it (A12, r2).**" — a review *round* in a rule heading |
| D-15 | 256 | "The table above said the opposite **for over a round**" |
| D-16 | 262 | "Both directions now mint a revision, exactly as `budget` and `expose` do (design 03 A31)." — the "now" and the "exactly as" both need history |
| D-17 | 265 | "**Env sources are hashed by CONTENT, not by referent (A20, revised).**" |
| D-18 | 305 | "**A17 briefly made this gate asymmetric and A18 retracts it.**" |
| D-19 | 314–320 | the "Found by" column: "Codex M5; independently reproduced", "Codex M3", "Codex B1", "Codex M4" — a reviewer's finding IDs as a spec column |
| D-20 | 328 | "**A critic demonstrated** the consequence" |
| D-21 | 332 | "it corrects **this amendment's first draft** on every count" |
| D-22 | 334 | "**the first draft claimed otherwise**" |
| D-23 | 344 | "**A cheap reflection sweep is not this test.** One was run over the current projection" |
| D-24 | 346 | "**the critic demonstrated it** by adding a `SystemPrompt` field and watching the whole suite stay green" |
| D-25 | 388 | "**The template uses `index` (A60, correcting A59).**" |
| D-26 | 396 | "One label carrying two trust boundaries was **the critique's first blocker**." |

---

## E — UNENFORCED CLAIM

Scoped to claims that name a *mechanism* — admission, CEL, a test, RBAC, the chart — where the mechanism
is absent from the repo and the body carries no "nothing enforces this yet".

**E-1. Prod-gate presence is not enforced at admission, and cannot be.**
- L63: "`evalSuiteRef: pa-regression` — **≥1 required in prod iff EvalSuite CRD installed**"
- L369: "**Core tier**: the gates-required **admission rule** applies iff the EvalSuite CRD is installed"
- L414: "Admission has already enforced … **prod-gate presence**"
- L440: "Prod gates missing | **Rejected at admission** when the EvalSuite CRD is installed; never reconciled"
- L466: "**prod-gate presence (CEL)**"
- Repo: no `ValidatingAdmissionPolicy` on Agents (`charts/assayd/templates/admission.yaml` covers Namespaces
  and HTTPRoutes only) and no `XValidation` for `gates` in `api/v1alpha1/agent_types.go`. CRD-level CEL
  cannot observe whether another CRD is installed, so this is not merely unbuilt — it is not expressible
  in the mechanism named. What is implemented is *reconcile-time*: `internal/controller/discovery.go`
  `EvalSuiteDetector` + `agent_controller.go:460` hold the candidate at weight 0, or `GatesSkipped`.
- **Fix**: state the real mechanism (a detector plus a hold), in one place, and delete "admission"/"CEL"
  from all five sites.

**E-2. The `usdPerDay` grammar is not on the schema.**
- L59: "usdPerDay is a decimal STRING: `^[0-9]{1,6}(\.[0-9]{1,6})?$` (A15, six digits per A18)"
- L414: "Admission has already enforced … **the `usdPerDay` grammar**"
- Repo: `api/v1alpha1/agent_types.go:312` is a bare `*string` with no `+kubebuilder:validation:Pattern`
  and no `XValidation`; the generated CRD (`charts/assayd/crds/assayd.dev_agents.yaml:81–85`) has `type: string`
  and nothing else. Any string is accepted today, and it flows into the revision projection
  (`internal/revision/revision.go` `budget.USDPerDay`) and design 03's pricing compile.

**E-3. `expose: public` requires no approval label.**
- L466: "`expose: public` requires an approval label (CEL)"
- L216 / L446: "**Admission may require** the assertion for `expose: public`" — a "may" is not a rule at all.
- Repo: `Visibility` is `+kubebuilder:validation:Enum=cluster;org;public` with `+kubebuilder:default=cluster`
  and no further rule; nothing reads an approval label anywhere.

**E-4. Namespace-unique tool names are not enforced at admission.**
- L95: "the two share one namespace-unique name space, **enforced at admission (design 11 §4)**, which is
  why the binding carries no kind discriminator."
- Repo: there is no `Connector` or `MCPServer` CRD, no webhook, and no admission policy.
- **Worse, the cited design does not contain the rule either.** `11-connector-crd.md` §4 is "Facet
  reconciliation" and the strings "unique" and "MCPServer" appear zero times in the whole document
  (G-6). So the uniqueness rule is asserted by design 02, enforced by nothing, and owned by no design.
  It is load-bearing for the security argument in the same paragraph — a binding with no kind
  discriminator and no namespace field is safe *only* if names are unique across both kinds. Either
  design 11 must gain the rule, or design 02 must state that nothing enforces it yet.

**E-5. The leaf walker carries no recursion guard.**
- L340: "So **the walker carries a stack keyed by `(type, path)`**. Meeting a type already on the stack
  **fails immediately**, naming the full field path…"
- Repo: `internal/revision/leaves.go` `Leaves()` recurses on struct kinds with no visited set and no
  stack. It terminates today only because `AgentSpec`'s graph is acyclic — which L338 correctly calls a
  precondition rather than a guard, one paragraph before asserting the guard exists.

**E-6. "Directory writes: operator identity only" (L466).**
`internal/directory/` is an empty package; nothing writes the directory and nothing constrains who does.

**E-7. "Workload pods: default-deny NetworkPolicy (egress = gateway only)" (L466).** See A-4.

**E-8. "Leader election guarantees one operator process" (L165).**
The per-run-namespace mutex's safety argument rests on this. Leader election is a `Lease` with a
lease-duration and a renew-deadline; a partitioned former leader can still be executing a reconcile after
a new leader is elected. The protocol may well be safe anyway (every check is a live read against the
API server, and row 7's delete is name- and nonce-verified), but the sentence as written asserts a
property `coordination.k8s.io` does not provide. **Fix**: state what leader election gives (one
*intended* writer) and what actually fences the protocol (live reads + compare-and-swap on the binding).

**E-9. The map-kinded-leaf rule is not what the code does.**
- L336: "A map-kinded leaf is perturbed **twice** — over its key set (`{cpu:1} → {memory:1Gi}`) and over
  its value set (`{cpu:1} → {cpu:2}`)"
- Repo: `internal/revision/leaves.go` `distinct()`'s map case changes the key *and* the value in one
  perturbation. Moot today because both map paths hang off `runtime.resources` (policy), which the
  design says — but the rule as stated is not the rule as implemented.

**E-10. "the operator's RBAC therefore needs `pods/log` `get` for itself" (L138).**
Not granted in `charts/assayd/files/operator-rules.yaml`. L210 admits the read-access path is not
implemented; this sentence should say so where it stands.

**E-11. The condition vocabulary's closure rule is asserted nowhere in the body.**
§3.1 lists 39 condition types as "a contract", but no body sentence says the vocabulary is *closed*, who
closes it, or where. The mechanism exists and is load-bearing (`api/v1alpha1/agent_types.go` typed
`ConditionType` constants + `internal/controller/condition_literal_test.go`, and the CEL enum was
deliberately withdrawn because one unrecognised type rejects the entire status write). This is the
inverse defect: an enforced rule with no claim. The rewrite should add it.

**E-12. The cold-candidate premise is not in the design it is attributed to.**
- L215: "**Eval consequence, stated**: design 16 gates a **cold** candidate against a warm active; suites
  must not assume warm local state."
- The words "cold" and "warm" appear **nowhere in `16-evalsuite.md`**. The consequence may well be true —
  it follows from this design's own read-only-scratchpad rule — but it is stated as design 16's decision
  and design 16 has not made it. Either design 16 gains the rule or design 02 states it as its own
  consequence and stops attributing it.

---

## F — DUPLICATE

Same rule, two or more places, different wording. Left column is the site the rewrite should keep.

| # | Keep | Also at | The rule |
|---|---|---|---|
| F-1 | 105 | 431 | env source missing/unreadable ⇒ `EnvSourceUnresolved`, no revision, no workload — including the "a zero would let deleting an object mint the same hash as never having referenced it" clause, repeated almost verbatim |
| F-2 | 271–283 | 109 | A35: copies made at mint, before publication, from the same buffer; the workload references the copy |
| F-3 | 125 | 113, 127 | revision workloads and material live in `assayd-run-<agent-namespace>` |
| F-4 | 267 | 127, 277 | `immutable: true` permits delete-and-recreate, so immutability binds contents to an object, never a name to contents — argued three times |
| F-5 | 133 | 206 | design 03 §3.2's truncate-and-hash rule, once for the namespace name and once for mirror names |
| F-6 | 280–281 | 147, 438 | copies are rewritten non-optional; a missing/mismatched copy ⇒ weight 0 / `RevisionMaterialUnavailable`; rollback refused |
| F-7 | 474 (§8) | 208 | the owed/covered test list for the run-namespace block, in full, twice |
| F-8 | 215 | 445 | scratchpad access-mode fallback ⇒ candidate runs without it + `ScratchpadDegraded` |
| F-9 | 216 | 446 | `replicas>1` without a shared-task-state declaration ⇒ `TaskStateUnverified` + CLI warning |
| F-10 | 213 | 31, 480 | `sandbox` ⇒ singleton; `replicas>1` + `sandbox` is an admission error |
| F-11 | 214 | 444 | sandbox runtime class absent ⇒ hardened Deployment + `SandboxDowngraded` |
| F-12 | 359 | 482 | `revisionHistoryLimit` counts **in addition to** active and candidate |
| F-13 | 373 | 443 | card signature: required for assayd-built, `CardUnsigned=True` for BYO/external |
| F-14 | 376 | 441 | `registrationDeadline` expiry ⇒ candidate failed and deleted, slot freed |
| F-15 | 375 | 442 | card advertising an ungranted capability ⇒ `Registered=False` naming the mismatch |
| F-16 | 95 | 45–46 (YAML comment) | tool names resolve in the agent's own namespace; one name space across Connector facets and `MCPServer`; no kind discriminator |
| F-17 | 466 | 30, 414, 439 | image signature is not verified; needs the Sigstore policy-controller binding — stated four times |
| F-18 | 230 | 137, 295 | `AlreadyExists` is a race/never provenance/not an adoption — three formulations, three places, one rule |
| F-19 | 161–163 | 136 | the nonce defeats pre-creation, the UID defeats delete-and-recreate; the table row states the whole argument the prose block then states again |
| F-20 | 139 | 176, 206 | ResourceQuota / LimitRange / Pod Security mirroring — the decision, the ordering ("mirrors before `Bound`") and the naming, split across three sites |
| F-21 | 392–394 | 140 | the SPIFFE path segment is the `assayd.dev/agent-namespace` label, not `.PodMeta.Namespace` |
| F-22 | 142 | 466 | default-deny NetworkPolicy, egress to the gateway only (and see A-4) |
| F-23 | 143 | 404 | Service and route move with the workload; the compiler owns them by label and name, not `ownerReference` |
| F-24 | 457 | 142 | `GovernanceSkipped=GatewayDisabled` is what a `gateway.enabled: false` install reports |
| F-25 | 190, 198 | — | the handler's step 3 and the finalizer's step 1 are the same live-list-and-scope procedure written twice; the finalizer step even says "as the handler's step 3 does" and then restates it |

Two near-duplicates worth merging rather than deleting: L228's collision rule and L230's two corollaries
are one rule ("the operator may only act on evidence it produced"); and L284–293's six-step acquisition
order restates L271's "the copy is what is hashed" as a procedure. Both should stay, adjacent, once.

---

## G — DEAD ANCHOR

Cross-design anchors were verified against the cited documents, not against design 02's description of
them.

**G-1. L354, L359, L363, L365 — `revisionHistoryLimit`.**
> "superseded revisions GC'd per `revisionHistoryLimit`" · "**`revisionHistoryLimit: 2` means two retained
> revisions *in addition to* active and any in-flight candidate**" · "Its size is `revisionHistoryLimit + 2 + holds`"

§3.1's schema does not declare the field, and it does not exist in `api/v1alpha1/agent_types.go` or the
generated CRD. What exists is a compile-time constant, `internal/controller/agent_controller.go:57
DefaultRevisionHistoryLimit = 2`. Either §3.1 gains the field or the body must say the value is not
user-settable at P1 — the retained-set arithmetic is stated in terms of it.

**G-2. L376, L441, L470, L476 — `registrationDeadline` (default 30m).**
Same defect: named as a spec field with a default in §3.4, cited in §5, §7 and §8, absent from §3.1's
schema and from `api/v1alpha1` entirely. §12's A14 entry already records it as "still unimplemented".

**G-3. L305 — "`revisionHash(spec)` is computed from spec alone (`internal/revision/revision.go:12`)".**
Line 12 of that file is prose inside the package doc comment. The claim is also false since A20: the
signatures are `Digest(spec assaydv1alpha1.AgentSpec, resolved Resolved)` and `Hash(spec, resolved)`, and
both **refuse** without the resolved sources. The A18 argument this citation supports (the subset
predicate needs `(old, new)` and is not expressible in a content-addressed digest) is still correct —
but its evidence line now points at the wrong thing and states a superseded signature. See §H.

**G-4. L474 (§8) — "the read-only RoleBinding's Role".** No such object; no such chart test. See A-3.

**G-5. L177/L178 — two table rows numbered 7**, referenced as "row 7's authority" (L174) and as "the
twelve ordered checks" (L210) over thirteen rows. See A-13.

**G-6. L95 — "enforced at admission (design 11 §4)".**
`11-connector-crd.md:47` §4 exists and is titled "Facet reconciliation". It carries **no uniqueness
rule**; the strings "unique" and "MCPServer" appear **zero times in the whole of design 11**. This is the
worst anchor in the body, because the sentence it supports is a security argument: it is why a tool
binding is permitted to carry neither a kind discriminator nor a namespace. See E-4.

**G-7. L95 — "design 24 §4.1 derives the `can_call` tuple **from** the binding".**
`24-authz-provider.md` has **no §4.1**. §4 (:38) is "Tuple lifecycle" and the content is its item 1
("`Agent.tools` → `can_call`"). The claim is true; the anchor is not. Design 24's own A1 propagates the
same bad number, so fixing it is a two-document change.

**G-8. L138 — "**The supported path is `assayd logs`** (design 08)".**
`08-cli.md` contains no `assayd logs` and no `SubjectAccessReview`; its CLI surface at :26 is
`agent list|status|logs|register`. The same non-existent command is cited by design 07 A5.7 and by
ADR-0029, so three documents now depend on a design-08 feature design 08 does not describe. This is the
*only* supported read path for run-namespace Pod logs, so the gap is user-visible.

**G-9. L365 — "it is what bounds `status.revisions[]` records (design 03 A28)".**
Design 03 A28's current text carries only the 8 KiB bound; the record-count rule moved to design 03
**A36** ("An earlier cap of three counted named roles"). L361's use of A28 is fine because it is written
in the past tense; L365's is stale.

**G-10. L143 and L404 — "design 03 A44".**
Design 03 has **two amendments numbered A44** (:777 GC keys on the retained set; :778 namespaces of
emitted resources). Design 02 always means the second. An unqualified `A44` cannot resolve.

**G-11. L460 — "no traffic if it is the sole knowledge source (design 01 A5)".**
Design 01 A5.2 covers bind validation refusing a non-`active`/`superseded` binding; the
"no traffic if sole knowledge source" half is **not in design 01** — it is design 03 A27 / architecture §04.
Half a rule is attributed to the wrong document.

**G-12. L3 — "Latest review: `reviews/03-codex-review-r8.md`".**
The file exists and *does* cover design 02 (its scope line reads "design 02 A36–A46, design 03 A36–A43,
design 04 A4, design 20 A4–A6", and the 13/6 counts match). But it is titled "Design 03 / operator
integration" and its verdict sentence — "Do not start design-03 implementation from this contract" —
gates design 03, not design 02. Citing it as design 02's latest review is defensible and misleading at
once; the consolidated header should either name it precisely or drop the review-history line entirely.

**G-13. L17 — "§17 budget".**
Not dead: `docs/architecture.md:415` is "## 17 · Weight budget — core tier" and it does say
"agent-operator 1 (the only assayd-code pod) … ≈ **8 pods**". But the document is never named at the
citation, and design 02 has eleven sections of its own, so a reader hits `§17` in §2 and has nowhere to
go. (Designs 06, 07 and 10 cite it the same unqualified way; fixing it here is a corpus-wide habit, not a
design-02 bug.)

---

## H — CI trap the rewrite will hit (not a defect in the design)

`test/docs/superseded_test.go` scans every design's **authoritative body** for assertions of superseded
guarantees, and rescues a line that mentions the retraction (`retract|supersed|no longer|earlier|reverses`).
Exactly **two** design-02 body lines currently pass on that rescue:

| Rule | Line | Banned phrase present | Rescued by |
|---|---|---|---|
| `spec-only-hash` | 305 | "computed from spec alone" | "A18 **retract**s it" |
| `seal-not-copy` | 297 | "no bytes are copied" | "This **reverses** A23's…" |

Both rescues are the very narration this consolidation is deleting (C-22, C-25). **Delete the narration
without rewriting the sentence and `go test ./test/docs` goes red.** In both cases the fix is the same
and is an improvement: state the rule positively — the digest covers the spec projection *and* the
resolved contents of every referenced source; a revision reads its own immutable copy — and let §12 keep
the reversal.

---

## Load-bearing — must survive the rewrite verbatim in substance

These are the invariants a critic will attack if they are softened. For each: what it is, where it lives
in the current body, what enforces it, and the sentence that must not be lost.

### 1. The collision guard — name ≠ identity (§3.3, L222–232)
- **Rule**: `revisionHash` truncates to 40 bits and is a **name**; `status.activeRevisionDigest` /
  `status.candidateRevisionDigest` carry the full SHA-256 and are **the only values compared** to decide
  whether two specs are the same revision. A desired revision whose **name** matches a recorded one while
  its **digest** does not is a terminal `RevisionHashCollision` **before the workload is touched at all**
  — no adoption, no rewrite. Status is written only by the operator through the status subresource under
  separate RBAC; the annotation only corroborates. An **absent** annotation is **refused**, never adopted.
- **Must not be lost**: "The projection is **attacker-controlled**, so the question is a *chosen*
  collision, and forty bits costs about `2^20` trials: a colliding pair of specs … was found in **1.2
  seconds** on a laptop." The measurement is the argument.
- **Also must not be lost**: the two corollaries at L230 — `AlreadyExists` on create is a race, not a
  success; and **availability alone does not authorize promotion** ("a squatting object reports
  `AvailableReplicas` just as well").
- **Enforced**: `internal/revision/revision.go` (`Digest` vs `Hash`, `HashLength`),
  `internal/controller/agent_controller.go:554/573/610–617`, `internal/controller/conditions.go:60`
  (owned, so it clears), `internal/revision/collision_test.go`, `test/envtest/collision_test.go`.

### 2. The one-read rule (§3.3, L284–295)
- **Rule**: one GET produces one immutable byte buffer used for **both** the hash and the Create. The
  order is fixed: GET once (recording uid + resourceVersion) → canonicalize and buffer → compute the full
  digest over those buffers → Create the immutable copies **from those buffers, never from a second
  read** → read back and verify → only then publish.
- **Must not be lost**: "The natural two-read implementation … races an editor between the reads … The
  copy would then be faithfully immutable and faithfully wrong."
- **Caveat for the rewrite**: step 5's "read back and verify **kind, owner UID**, the immutable bit, and
  the bytes" must be restated under A57 — content decides; the operator restamps metadata rather than
  refusing (see A-6). The *ordering* is what must survive; the owner-UID predicate must not.
- **Enforced**: `internal/controller/material.go` (`sourceBuffer`, `ensureOneCopy`, `createMaterial`).

### 3. The retained set (§3.3, L359–367)
- **Rule**, and it must stay a single definition with everything sized from it:
  > **retained set** = `{active}` ∪ `{candidate}` ∪ *the `revisionHistoryLimit` most recent superseded
  > revisions* ∪ *explicit holds*.
  Size `revisionHistoryLimit + 2 + holds`, **at least four at the default**. It bounds
  `status.revisions[]` records and A35's copies alike. A design-20 retention hold **extends** the set; it
  does not occupy a history slot.
- **Must not be lost**: "a hold that displaced a history entry would silently shorten the rollback window
  during exactly the incident the hold exists for", and the reason history is *additional* to active and
  candidate ("otherwise a rollout would GC its own rollback target").
- **Watch**: no number may be written anywhere else. The body has been wrong here before (C-21, C-23, C-35).
- **Enforced**: `internal/controller/agent_controller.go:53–57, 1123`. Note the field itself does not
  exist (G-2) and there is no maximum-overlap transition test (L365 still owes it).

### 4. The binding record (§3.2, L149–204)
- **Rule**: for every source namespace `S`, a strictly-decoded `ConfigMap`
  `assayd-run-binding-<run-namespace>` in the operator's own namespace, carrying `sourceNamespace`,
  `sourceNamespaceUID`, `runNamespace`, `runNamespaceUID`, a 128-bit `nonce`, `state`
  (`Creating`·`Bound`·`Terminating`·`Deleting`) and `schemaVersion: 1`. **Nothing defaults**; a missing
  field, unknown state or undecodable version is terminal `RunNamespaceUnavailable=BindingRecordInvalid`.
- **The two proofs must stay two**: the **nonce** defeats pre-creation (the attacker moves first and
  cannot stamp a value that does not exist yet); the **UID** defeats delete-and-recreate (the attacker
  moves second and cannot reproduce a UID the API server chose). The nonce is consulted only while
  `Creating`; from `Bound` onward every reconcile compares the live namespace's `metadata.uid`.
- **The bind rule must survive verbatim in substance**: "**a namespace is bound only by the reconcile that
  received its `Create` response**, from that response's UID." A successor finding `Creating` and a
  namespace present **never binds it** — nonce matching ⇒ delete, rotate, retry; nonce differing ⇒ refuse.
- **The two-state fence must survive**: `Terminating` → `Deleting` is a compare-and-swap and nothing flips
  back from `Deleting`, "because with only `Terminating`, a creator could read it, list, find itself, flip
  to `Bound` and write material in the instant between another worker's empty list and its delete."
- **The read discipline must survive**: record, run namespace and source namespace are read **live**
  through the API reader, never from the informer cache.
- **The teardown ordering must survive**: the binding changes state **before** the confirming list, and
  the finalizer releases once the handler has issued the delete or restored `Bound`.
- **The human-only recovery must survive**: a lost binding is `NotCreatedByOperator` for every Agent in
  `S`, and the operator "never vouches for a namespace it cannot prove it created". The harsh path is chosen.
- **Enforced**: `internal/controller/runnamespace.go`, `runnamespace_unit_test.go`,
  `test/envtest/runnamespace_test.go` (29 cases), `cmd/operator/main.go` (uncached reader,
  `--operator-namespace`).

### 5. Label authority (§3.2, L144)
- **Rule**: three consumers act on run-namespace labels and none reads the binding — the
  `ClusterSPIFFEID` selects on `assayd.dev/pods-by`, the Gateway listener admits on
  `assayd.dev/run-namespace`, and the Namespace reconciler keys on both. So the chart ships a
  `ValidatingAdmissionPolicy` and binding on Namespaces **and their `status` and `finalize`
  subresources** denying any create/update that sets or changes `assayd.dev/pods-by`,
  `assayd.dev/run-namespace`, `assayd.dev/tenant-sync`, `assayd.dev/agent-namespace`, `assayd.dev/owned-by`
  or the `assayd.dev/binding-nonce` annotation from anyone but the identities rendered **into** the policy.
  A second policy admits Gateway-parented `HTTPRoute`s from the operator only.
- **Must not be lost**: "**Not a params object**: a params ConfigMap that went missing would have turned
  the policy into a cluster-wide denial of every namespace write."
- **Must not be lost**: the **fail-close** — the operator checks at startup and on every reconcile that
  both policies and their bindings exist and refuses to create a run namespace with
  `RunNamespaceUnavailable=LabelAuthorityAbsent` if they do not, "a label nobody reserves must not be
  treated as evidence" — together with the honest limit: "**Presence is what is checked**; the content is
  the chart's, and a policy of the right name that validated nothing would pass the check."
- **Must not be lost**: the two-labels-two-boundaries rule at L396 — `pods-by: agent-operator` means only
  the operator creates Pods there; `run-namespace` is the Gateway's, and a vCluster host sync namespace
  carries it while a *tenant* creates the Pods there.
- **Enforced**: `charts/assayd/templates/admission.yaml`, `test/chart/chart_test.go:517`,
  `test/e2e/e2e_test.go:598 TestNamespaceLabelsAreReservedToTheOperator`,
  `test/envtest/runnamespace_test.go:517 TestRunNamespaceRefusesWithoutLabelAuthority`.

### 6. The classification table (§3.3, L245–350)
- **Rule**: the discriminator is **capability**, not the compile path. Behaviour surface mints a revision
  and passes a gate; policy surface is applied in place. Classification is **compulsory**, never defaulted,
  and is over the **transitive leaf graph** of `AgentSpec` — with the aggregate's class as the default for
  its leaves and an explicit leaf override that must appear in the table.
- **Every row of the table is load-bearing**, in particular the four that were wrong at least once:
  `external.oauthClientRef` (behaviour — identity, not credential rotation), `llm.egressAllowlist`
  (behaviour, **symmetric**), `budget` and `expose` (behaviour), `tools[].requiresApproval` (behaviour in
  **both** directions), and `runtime.resources` (policy, because drift is caught by design 20 and gating it
  would block incident response).
- **Must not be lost**: the symmetric-gate argument's laundering walk (`{A}` gated → `{A,B}` candidate held
  → `{B}` "removal" applied in place → R1 now reaches ungated `B`); "changing this table is a hash
  migration"; list-order insensitivity with `env` excepted as semantic; the canonical-JSON /
  declaration-order contract; unknown union arms hash **injectively** (over-gate), never to a constant;
  and the design-20 fallback **exception** without which A12's headline claim is false.
- **Enforced**: `internal/revision/revision.go` (projection, sorting, `UnknownEnvArms`, `MissingSources`),
  `internal/revision/leaves.go` (`Leaves`, `specLeafClass`, fail-closed `distinct`),
  `classification_test.go`, `leaf_test.go`, `golden_test.go` (4 migrations),
  `test/envtest/leaf_admission_test.go`.

### 7. Also load-bearing, and easy to lose in a rewrite

- **No cross-namespace tool or graph reference** (L95): "design 24 §4.1 derives the `can_call` tuple
  **from** the binding, so a cross-namespace reference would authorize itself." The absence of a
  `namespace` field is a security property, not an omission.
- **A20's three refusals** (L105–107): missing/unreadable referent, unrecognised arm, and content folded
  in fixed sorted order with `data` and `binaryData` tagged separately. "A missing referent is
  *unresolved*, never a zero digest."
- **A35's copy properties** (L275–281): kind preserved (a Secret never becomes a ConfigMap — "kind is what
  RBAC keys on"), `immutable: true`, name derived from inputs so re-reconcile converges, and copies
  **always rewritten non-optional** ("`optional: true` on a copy means deleting the copy starts a
  replacement Pod with the variable simply absent — a silent change … achieved by a delete rather than
  an edit").
- **Why a whole namespace** (L127): "a ConfigMap or Secret can only be used by Pods in its own namespace.
  So material can only be out of an editor's reach if the *Pod* is too." And: "`ownerReference` does not
  help: it does not protect deletion."
- **Naive truncation is forbidden** (L133): non-injective, would pool two tenants' material.
- **Quota and LimitRange mirroring, with its cost stated** (L139): a tenant's ceiling now applies per
  namespace and the pair can reach **2×** the stated limit — and the Tenant API says so. Mirrored
  **together**, because a CPU quota rejects any Pod without a CPU request and the LimitRange supplies the
  default.
- **Pod Security label mirroring, with its honest limit** (L139): "Changing a namespace's labels does not
  re-evaluate running Pods … so a tightened level takes effect on the next Pod, and this design says so
  rather than implying eviction."
- **The route must move with the workload** (L143): a cross-namespace `backendRef` yields
  `ResolvedRefs=False, reason: RefNotPermitted`, which would wedge every Agent at every install; and
  emitting a `ReferenceGrant` is foreclosed by §3.1's own out-of-scope rule.
- **The SPIFFE template's fail-closed shape** (L385–390): every label-derived value is a **whole path
  segment**, so an absent label renders an empty segment and go-spiffe rejects the ID — "a template
  writing `agent-{{ index … }}` would render a valid ID from an absent label" — *and* the `podSelector`
  additionally requires both labels to exist. Both halves, or the property is gone.
- **The identity does not move when the Pod does** (L392–394): the path segment is the
  `assayd.dev/agent-namespace` label carrying the **Agent's own** namespace.
- **Finalization ordering** (L408): drain (respecting `taskTimeout`) → revoke routes/policies in reverse
  apply order → **deactivate, never delete**, the `agent-actor` client → GC directory + SPIRE labels →
  delete workloads, copies, and the run namespace if last, by the binding protocol → release. "Nothing is
  force-killed silently." Under A42 nothing has an ownerReference, so *nothing else will* collect any of it.
- **Loud degradation, always** (L214, L457, NFR-8): `SandboxDowngraded`, `ScratchpadDegraded`,
  `TaskStateUnverified`, `GatesSkipped`, `GovernanceSkipped`, `BudgetEnforcementDegraded` — and the rule
  that a tier never installed is **not an incident** (`GovernanceSkipped` is a separate condition type,
  not a reason on an error type, "so consumers keyed on error conditions must not fire on it").
- **`PricingStale` ≠ a missing pricing row** (L452): "one condition cannot carry both meanings and mean either."
- **The printer-column contract** (L93): six columns, pinned by
  `api/v1alpha1/schema_contract_test.go:156 TestPrinterColumnsMatchTheDesign`.
- **The condition vocabulary** (§3.1, L77–90): **39** types, matching exactly the 39
  `ConditionType` constants in `api/v1alpha1/agent_types.go:367–450` (verified count-for-count today). The rewrite must not drop
  `EnvSourceProtectionUnavailable` — L113's reason for keeping it (so a post-A42 operator can clear a
  stale one) is correct and is enforced by `internal/controller/conditions.go`'s `ownedTypes`.

---

## One recommendation for the rewrite

The body's failure mode is uniform: **it argues with its own history in the present tense.** Three
mechanical passes would remove most of this inventory before any judgement is required.

1. Delete every sentence whose subject is an amendment, a draft, a review round, or a reviewer (C + D):
   ~60 sentences, and the counter-examples marked **[keep]** above are the only things to rescue from them.
2. Delete every status latch — "today", "owes", "does not exist yet", "not implemented until", "is to" (B):
   19 sites, listed above.
3. Merge the 25 duplicate pairs (F) toward §3, leaving §5's table and §9's decisions as pure
   cross-references.

What is left is a genuinely strong design. Every invariant in the load-bearing list survived an
adversarial read, the run-namespace binding protocol is the most carefully fenced piece of concurrency
reasoning in this corpus, and its thirteen ordered checks with a precedence rule and a stated reason for
that precedence are correct as written. The problem is not the design. It is that a reader has to
excavate it.

The eight substantive findings are listed under **Counts** above. Three of them (E-4/G-6, G-8, E-12)
are gaps in *other* designs that design 02 has been papering over with a citation; they cannot be fixed
inside this body, and the rewrite should either state the gap plainly or open the amendment against the
owning design before design 03 starts.
