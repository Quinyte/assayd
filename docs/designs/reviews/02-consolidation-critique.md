# Design 02 — critique of the consolidated body (A62)

- **Scope**: `docs/designs/02-agent-crd-operator.md` header + §1–§11 (lines 1–493). §12 out of scope.
- **Method**: read the inventory (`reviews/02-consolidation-inventory.md`), then `git diff`, then the new body
  cold. Every present-tense claim checked against `api/v1alpha1`, `internal/controller`, `internal/revision`,
  `cmd/operator`, `charts/plume`, `test/`. Two enforcement claims mutation-checked; one claimed guard
  disproved by execution. Line numbers are the **new** file.
- **Verdict**: **REVISE — 3 BLOCKER, 9 MAJOR, 15 MINOR.**

The consolidation is, in substance, a large improvement. Of the six load-bearing invariants the inventory
named, five survive intact and one survives with a hole (see MAJOR 1). Thirteen of the inventory's fourteen
smaller invariants survive. The eight substantive decisions the inventory demanded were all *taken*, and
four of them were taken correctly and completely (A-6 content-vs-name authority, A-10 partially, the
`--operator-namespace` flag, the params object, the printer columns, the ordered-table renumbering, E-8's
leader-election claim, G-3/G-7/G-9/G-11/G-13's anchors). The body carries **zero** design-02 amendment
tags — verified by regex; the fifteen remaining `A<n>` tokens are all legitimate cross-design citations.

What it did not do is finish the job it advertises. §5 opens with a universal claim — *"Every guarantee this
design describes that nothing in the repository implements is here"* — and that claim is false in at least
four places, one of which is the ADR-0006 core guarantee. Two of the inventory's own unenforced-claim
findings (E-5, E-9) were carried through unchanged and are still stated in the present tense. And the
rewrite introduced a new falsehood into §5 itself.

---

## BLOCKER

### BLOCKER 1 — "≥1 gate required in prod" is stated three ways, enforced none of them, and the shipped behaviour is the fail-open one

`docs/designs/02-agent-crd-operator.md:65`, `:232`, `:428`

Three sites, three different mechanisms, and the code agrees with none:

- `:65` (§3.1 schema comment): "`≥1 required in prod where the EvalSuite CRD is installed` — **enforced at
  RECONCILE, not admission (§3.3)**"
- `:232` (§3.3): "so '≥1 gate required in prod' **is enforced at reconcile**: the operator asks the cluster
  whether the `EvalSuite` CRD is present … **where it is present and gates are declared**, the candidate
  holds at weight 0"
- `:428` (§5 failure table): "Prod gates missing where the EvalSuite CRD is installed | The candidate
  **holds at weight 0** with `GatesPassed=False`"

`:232` announces the rule and then describes a mechanism that cannot produce it: the hold it describes is
conditioned on *"gates are declared"*, so it says nothing about an Agent that declares none. `:428` asserts
the missing case holds. The code does the opposite:

```go
// internal/controller/agent_controller.go:1014
func (r *AgentReconciler) gatesSatisfied(agent *plumev1alpha1.Agent) bool {
	if !r.evalSuiteInstalled() {
		return true
	}
	return len(agent.Spec.Gates) == 0        // ← no gates ⇒ SATISFIED ⇒ promote
}
```

and `assessGates` (`:1036`) puts `len(agent.Spec.Gates) == 0` **first**, setting
`GatesSkipped=True / NoGatesDeclared` and letting the revision promote. **Concretely**: on a `prod`-profile
install with the EvalSuite CRD present, `kubectl apply` an Agent with `gates: []` and a new image; it
promotes to 100% traffic with no eval, reporting `GatesSkipped=True`. That is the ADR-0006 hole this design
exists to close, and §5 — the section whose stated job is to make exactly this visible — says the opposite
happens.

Two more unenforced claims ride in the same paragraph and are also absent from §5:

- `:232` "`local` profile only: `GatesBypassed=DevProfile`". The operator has **no notion of a profile** at
  all (`grep -n "profile" internal/controller/agent_controller.go` returns only `SeccompProfile`), and
  `CondGatesBypassed` is declared in `api/v1alpha1/agent_types.go:376` and **set nowhere** — it is one of
  only 11 of the 39 constants no code path ever writes.
- `:232` "A discovery failure resolves to 'installed'". `internal/controller/discovery.go:96` returns the
  **previous cached answer** when the detector is warmed, which may be `false`; only an unwarmed detector
  resolves to installed. (Minor on its own; listed here because it is the same paragraph.)

**Fix.** Decide which rule is real and say it once. Either (a) the operator refuses to promote an Agent
with zero gates when the EvalSuite CRD is installed — in which case `gatesSatisfied` and `assessGates` are
wrong and this is a code bug the design should name as owed; or (b) the rule is "declared gates are
enforced; an Agent that declares none is not gated, loudly", in which case `:65`'s "≥1 required in prod"
must be deleted, `:428`'s row must read *"promotes with `GatesSkipped=True/NoGatesDeclared`"*, and §5 must
gain a **stated-but-not-enforced** row for "≥1 gate required in prod" and one for
`GatesBypassed=DevProfile`. (b) matches the code and is almost certainly the intent; either way the three
sites must stop disagreeing.

### BLOCKER 2 — the leaf walker's recursion guard is claimed in the present tense and does not exist; it hangs

`docs/designs/02-agent-crd-operator.md:287`

> "'`AgentSpec` must not gain a self-recursive type' is a precondition, not a guard: nothing enforces it and
> the failure mode is a hang. … **So the walker carries a stack keyed by `(type, path)` and fails
> immediately on meeting a type already on it, naming the field that introduced the cycle.** A hang is an
> **INVALID** mutation, not a killed one — CI crashes instead of naming the new field."

`internal/revision/leaves.go:44` — `Leaves(t reflect.Type, prefix string, chain []int)` — has no stack
parameter, no visited set, and self-recurses at `:60` unguarded. **Disproved by execution**, not by
reading: a temporary test calling `Leaves` on `type cyc struct{ Next *cyc; Name string }` was killed at the
60-second timeout —

```
signal: killed
FAIL	github.com/Quinyte/plume/internal/revision	45.283s
```

— i.e. the paragraph's own stated failure mode is what happens. (The temporary file was removed; the tree
is clean.) This is inventory **E-5**, raised, carried through the rewrite verbatim, and *not* given a §5
row, so §5's opening universal claim is false on the exact item the inventory had already isolated.

**Fix.** Either implement the stack in `Leaves` (roughly six lines: a `map[typePath]bool` threaded through
the recursion, `t.Fatalf` on a repeat), or rewrite `:287` to *"the walker carries no cycle guard today; the
graph is acyclic and nothing enforces that it stays so, so a self-recursive type hangs CI rather than
naming the field"* and add the row to §5. Do not leave the sentence asserting a guard.

### BLOCKER 3 — the design was moved to "approved" without a passing critique

`docs/designs/02-agent-crd-operator.md:3`, `docs/designs/README.md:8`

Old: *"amendments **A1–A61** folded into the body, all critique-driven and **none critique-passed**: every
round has returned REVISE."*
New: *"**Status**: **approved; implemented in part.**"* — and README now reads **approved**.

Every review on file for this design returns REVISE: `02-a60-critique.md` (3 BLOCKER),
`-r2` (1 BLOCKER), `-r3` (1 BLOCKER), `02-a60-codex-review.md` (10 BLOCKER), `02-a61-code-review.md`
(2 BLOCKER), `02-codex-review.md` (7 BLOCKER). The only PASS lines in `02-recritique.md:205` predate
A24–A61 entirely. The consolidated body itself has never been reviewed until this pass. The project's own
gate (`.claude/skills/critique-design/SKILL.md`): *"A design may not move to approved without a passing
critique."*

The status flip is not a documentation tidy — it is the one claim in the header nothing in the corpus
supports, made in the same commit that removed the sentence saying so.

**Fix.** Revert the status to something true until a critique passes — e.g. *"body consolidated
2026-09-04, critique pending"* in both the header and README. Move to **approved** when a critique returns
PASS, and cite that review file.

---

## MAJOR

### MAJOR 1 — check 5 lost the `Terminating`/`Deleting` branch, and check 5 runs before check 6

`docs/designs/02-agent-crd-operator.md:161`

New check 5 (`sourceNamespaceUID` ≠ `S`'s current UID) specifies an action for two of the four binding
states:

> "From `Bound`: compare-and-swap to `Terminating` and run the handler. From `Creating`: nothing was ever
> bound … The Agent waits with `Terminating`"

The old row 4 had a third clause the rewrite dropped: *"From `Terminating`/`Deleting`: **the handler is
already on its way**."* Check 5 is evaluated **before** check 6 (`state Terminating or Deleting`), and
"the first that applies decides", so a binding in `Terminating` whose source namespace has been recreated
reaches check 5 and finds no rule. An implementer writing the table literally has a `Bound` branch, a
`Creating` branch, and a fall-through with no action — and a compare-and-swap **from `Bound`** fails when
the state is `Terminating`, so the naive reading errors instead of progressing.

The shipped code is correct, and only by falling through: `internal/controller/runnamespace.go:389–421`
tests `bindingCreating`, then `bindingBound`, then calls `r.runNamespaceHandler(ctx, b, nil)` unconditionally
— so `Terminating`/`Deleting` reaches the handler with no branch of its own. The design no longer says so.

**Fix.** Restore the clause: "From `Terminating` or `Deleting`: the handler is already running; fall to
check 6's behaviour."

### MAJOR 2 — §2's charter gate still under-counts operator state: revision material is durable, authoritative and unreproducible

`docs/designs/02-agent-crd-operator.md:18`

> "Operator state is the Agent's own `status`, the directory KV (design 05), and **one operator-owned
> `ConfigMap` per run namespace**: §3.2's binding record. That record is durable, authoritative and not
> reconstructable by the operator — losing it needs a human — so it is named here rather than counted as
> zero."

The list reads as exhaustive and is not. §3.3 has the operator create, per revision, an immutable
`ConfigMap`/`Secret` copy of every referenced env source (`:307–322`), holding **credential material**.
Those copies meet the same test §2 applies to the binding record: check 11 at `:167` says so in this
document's own words — *"a retained revision whose sources have drifted since it was gated **cannot be
reproduced**"* — and `:321` says losing one sends the revision's route to weight 0. `plume-mirror-*`
ResourceQuota/LimitRange objects are operator-owned too (reconstructable, so a lesser case).

Inventory **A-10** asked for exactly this and got half of it. A charter gate that names one non-reproducible
operator-owned object and omits the one holding secret bytes is the wrong half.

**Fix.** Add to §2: "and, per revision, immutable copies of every referenced `ConfigMap`/`Secret` in the run
namespace (§3.3) — also durable and, once the source has drifted, not reproducible; bounded by the retained
set and collected with the revision."

### MAJOR 3 — renumbering 1–14 silently invalidated every `Row N` reference in the shipped code and tests

`docs/designs/02-agent-crd-operator.md:155–170`, `:470`

The renumbering was correct and necessary (inventory A-13). But **24 in-code cross-references still use the
old numbering** and nothing in the change updates them:

- `internal/controller/runnamespace.go` — `// Row 1.` `:341`, `// Row 2.` `:357`, `// Row 3.` `:382`,
  `// Row 4.` `:389`, `// Row 5.` `:429`, `// Rows 7 and 8` `:446`, `// Row 6:` `:455`, `// Row 7:` `:483`,
  `// Row 8.` `:501`, `// Row 9.` `:506`, `// Row 10.` `:521`, `// Row 11.` `:533`, `// Row 12.` `:539`
- `test/envtest/runnamespace_test.go` — `:155`, `:186`, `:229`, `:269`, `:298`, `:327`, `:462`, `:794`,
  `:828`, `:864`, `:1034`

A maintainer following `runnamespace.go:455 // Row 6: create, then bind FROM THE CREATE RESPONSE` now lands
on new check 6, "state `Terminating` or `Deleting`" — and `runnamespace_test.go:155 // Row 1: a run namespace
with no binding is one this operator did not make` now names new check 2, whose text is a different rule from
new check 1's wait. §8 at `:470` makes the coupling explicit: *"one envtest case per numbered check in
§3.2"*, so every one of these is now a false citation of the design.

**Fix.** Renumber the comments in `runnamespace.go` and `runnamespace_test.go` in the same change, or the
design's numbers are decorative. Cheapest durable form: name each check (`checkBoundUIDDiffers`) rather than
number it, in both places.

### MAJOR 4 — §5, the honest list, states a falsehood: `registrationDeadline` is not an operator constant

`docs/designs/02-agent-crd-operator.md:96`, `:344`, `:411`

- `:96` "Both exist today only as **operator constants** — the retention limit defaults to 2"
- `:344` "retries continue with backoff for `registrationDeadline` (30 minutes; **an operator constant**,
  not a spec field — §3.1)"
- `:411` (§5) "`revisionHistoryLimit`, `registrationDeadline` as spec fields | **Operator constants**, not
  API fields (§3.1)"

`DefaultRevisionHistoryLimit = 2` exists (`internal/controller/agent_controller.go:57`). `registrationDeadline`
exists **nowhere**: `grep -rn "RegistrationDeadline\|registrationDeadline\|30 \* time.Minute" api internal cmd
charts test` returns nothing, and there is no card-fetch or registration code in `internal/controller` at
all. §12's own A14 entry still records it as "still unimplemented". The rewrite converted "unimplemented"
into "an operator constant" — a status upgrade, in the table whose entire purpose is to prevent them.

**Fix.** Split the row: `revisionHistoryLimit` — an operator constant, default 2, not user-settable;
`registrationDeadline` — **neither a field nor a constant; registration and card handling are unimplemented
(design 05/09 pending)**. Correct `:96` and `:344` the same way.

### MAJOR 5 — §8 asserts a transition-matrix test that does not exist, converting an owed item into claimed coverage

`docs/designs/02-agent-crd-operator.md:470`

> "**Run namespace**: one envtest case per numbered check in §3.2 **and a transition matrix over the four
> binding states**; one per crash point with a paused reconciler — after the record create, after the
> namespace create …, between teardown steps 2 and 3, and between handler steps 4 and 5."

§8 has a separate **Owed** bullet at `:473`, so the other bullets read as present coverage. There is no
transition-matrix test: `test/envtest/runnamespace_test.go` holds 29 individually-named cases and no
table-driven state matrix (`grep -in "matrix" test/` returns nothing). The old body listed it correctly as
owed — "One envtest case per numbered row above **and an exhaustive transition-matrix test over the four
states**" under the heading *"What §8 owes for this block, by name"*. The other named crash points do exist
(`:233` after the namespace create, `:415` between teardown steps 2 and 3, `:771` handler 4→5).

**Fix.** Move "a transition matrix over the four binding states" into the **Owed** bullet.

### MAJOR 6 — the Immutability row states a rule the operator breaks, in a branch the body never names

`docs/designs/02-agent-crd-operator.md:316`

> "Immutability | `immutable: true`, so not even the operator can rewrite it in place. Kubernetes then
> permits only delete-and-recreate, and **the operator does not recreate a published revision's material**"

It does. `internal/controller/material.go:355–367` (`repairMetadata`): when a copy's **content matches** but
`Immutable` is nil or false (`:362` for `ConfigMap`, `:366` for `Secret`), it calls `recreateMaterial`
(`:400`), which **deletes** the object and returns a retriable error so the next pass recreates it from the
source. Pinned by
`test/envtest/material_test.go:212 TestNonImmutableMaterialIsRecreated`.

The behaviour is defensible — an object with matching bytes and no immutable flag can only have been created
by someone who won the `AlreadyExists` race with byte-identical content, so recreating loses nothing — but
the body describes neither the branch nor the reasoning, and states the flat opposite rule. An implementer
following `:316` and `:319` would restamp a non-immutable copy (impossible; the field cannot be enabled after
creation) and wedge. Carried from the old body, but the consolidated body is now the sole authority.

**Fix.** Add a row or clause: "a copy whose content matches but which is **not** immutable is deleted and
recreated from the buffer — `immutable` cannot be enabled after creation, and matching content means nothing
is lost. Published material is otherwise never recreated."

### MAJOR 7 — §5's opening universal claim is false, and it is the sentence the whole consolidation rests on

`docs/designs/02-agent-crd-operator.md:400`

> "**Stated but not enforced.** Every guarantee this design describes that nothing in the repository
> implements is here, so a reader never has to infer it from silence."

Counter-examples found in one pass, none of which has a row:

| Stated in the body | Repository |
|---|---|
| `:287` the leaf walker's `(type, path)` recursion guard | absent; hangs (BLOCKER 2) |
| `:65`/`:232` "≥1 gate required in prod"; `GatesBypassed=DevProfile` | absent (BLOCKER 1) |
| `:289` map-kinded leaves "perturbed over their key set **and** their value set" | `internal/revision/leaves.go:74–81` changes key *and* value in **one** perturbation (inventory E-9, carried) |
| `:341` "writes it to the directory (JetStream KV …)"; `:458` "Directory writes are the operator's identity alone" | there is no `internal/directory` package and nothing writes a directory (inventory E-6, carried) |
| §3.4 in full — card fetch, validation, cross-check, `status.cards[]`, drift re-registration | no registration code exists in `internal/controller` |

The header at `:3` does name registration and identity as waiting on other designs, which softens the last
two — but then `:400`'s "every" is the wrong quantifier, and a reader who trusts it will trust the rows that
*are* wrong (MAJOR 4).

**Fix.** Either add the missing rows, or narrow the sentence: "Every guarantee this design states **in the
present tense** that nothing enforces is here; whole sections awaiting another design are named in the
header." Then actually add the four rows above.

### MAJOR 8 — `EnvSourceProtectionUnavailable` is now a member of a closed vocabulary with no definition anywhere in the body

`docs/designs/02-agent-crd-operator.md:88`, `:98`

The old body explained it at L113: *"`EnvSourceProtectionUnavailable` is **no longer raised**; the type
stays in §3.1's vocabulary so an operator built after A42 clears a stale one left by an operator built
before."* That sentence is gone. The condition now appears **only** inside the `conditions: [...]` list.

This matters mechanically, not stylistically. `internal/controller/conditions.go:54` keeps it in
`ownedTypes` precisely so `merge()` clears a stale `True` — and
`api/v1alpha1/schema_contract_test.go:190 TestConditionVocabularyIsClosed` fails in *both* directions, so
removing it from the design would fail the build and removing the constant would fail it too. A maintainer
tidying "a condition nothing sets and nothing documents" out of §3.1 breaks the only path that clears it on
upgraded clusters, and the body gives them no reason not to.

I checked the whole vocabulary: **11 of the 39** conditions appear nowhere in the body outside the list.
Ten of those eleven were also undocumented in the old body (they belong to designs 03/06/20 and are
plausibly theirs to define). `EnvSourceProtectionUnavailable` is the one the rewrite added to that set, and
it is the one the inventory named as must-not-lose.

**Fix.** Restore one sentence beside the list: "`EnvSourceProtectionUnavailable` is never raised by this
operator. It stays in the vocabulary so an operator built after the run-namespace move clears a stale `True`
left by one built before; `conditions.go`'s owned set is what does the clearing."

### MAJOR 9 — the tool-name uniqueness gap was moved, not closed: design 11 was never asked, and ADR-0027 still asserts the enforcement §3.1 just withdrew

`docs/designs/02-agent-crd-operator.md:102`

> "**The uniqueness that makes a discriminator-free binding safe is not enforced anywhere yet**, and no design
> owns the rule: **design 11 is asked for it in its own open items**, and until it lands a Connector facet and
> an `MCPServer` may collide on a name."

The first clause is the honest fix the inventory asked for (E-4/G-6). The second clause is a new false
citation. `docs/designs/11-connector-crd.md` contains **zero** occurrences of "unique", "MCPServer", "open
item" or "owed" — verified by grep over the whole file — and it has no open-items or amendment section at
all (its headings run §1–§10, ending at "Resulting ADRs"). Nobody has been asked. The old body cited design
11 §4 as the *enforcer*; the new body cites design 11 as the *owner of the owed item*. Both are citations to
a document that says nothing on the subject.

Two consequences make this worse than a dangling pointer:

- **`docs/decisions/0027-crd-ergonomics.md:22` — an accepted ADR this design's own header cites at `:5` —
  still states the retracted rule as settled**: *"A tool name may still be served by a Connector facet or an
  `MCPServer` — two kinds, **one namespace-unique name space enforced at admission** — which is why the
  flattening survives even though the original 'one kind' premise did not."* An implementer reading design
  02 §3.1 and ADR-0027 together gets opposite answers, and the ADR is the document that reads as the
  decision. ADR-0027:22 also still carries the `design 24 §4.1` anchor that design 02 just corrected to
  `§4` (inventory G-7), so the two-document fix G-7 called for is half done.
- **The `MCPServer` kind is owned by no design.** `docs/designs/03-policy-compiler.md:83` also attributes it
  to design 11 (*"resolved against a Connector facet or `MCPServer` (design 11)"*). Three documents now
  depend on a design-11 concept design 11 does not carry.

**Fix.** In `:102`, drop "design 11 is asked for it in its own open items" and say what is true: *"no design
defines the `MCPServer` kind or the uniqueness rule; an amendment against design 11 is owed before this
binding shape is safe."* Then either open that amendment or record the gap in design 11 — and amend
ADR-0027:22 in the same pass, or the design and the ADR it rests on contradict each other on a security
premise.

---

## MINOR

1. `:96` — "Two spec fields **in the examples above** are not on the schema". Neither `revisionHistoryLimit`
   nor `registrationDeadline` appears anywhere in §3.1's YAML block (verified by grep over `:25–:94`). The
   sentence points at examples that do not contain its subjects. Say "named elsewhere in this design".
2. `:145` — "**Nothing defaults** — a missing field … is `RunNamespaceUnavailable=BindingRecordInvalid`" is
   unqualified, but `runNamespaceUID` **cannot** exist while `Creating`. `decodeBinding`
   (`internal/controller/runnamespace.go:191–206`) is state-conditional: `runNamespaceUID` is optional in
   `Creating` and required in every other state. An implementer writing a strict decoder from the field table
   rejects every `Creating` record and wedges the operator on its first reconcile. Carried from the old body;
   now load-bearing because the body is self-contained. State the exception in the row.
3. `:163` — check 7 dropped the old row 6's `AlreadyExists` branch ("someone else holds the name; row 7 or 8
   decides on the next pass"). This is the create the "bind only from the `Create` response" rule exists to
   fence, so the one error return it can produce should not be left to inference. Code handles it at
   `runnamespace.go:462`.
4. `:232` — "A discovery failure resolves to 'installed'". `discovery.go:96` returns the **previous** cached
   answer when warmed. Say "a discovery failure never becomes an answer: the previous answer stands, or
   'installed' if there is none."
5. `:69–79` vs `:100` — §3.1's `status:` block shows `budget: {tokensRemaining, usdRemaining,
   windowResetsAt}` and no `eval` key, while the next paragraph sources printer columns from
   `status.eval.score` and `status.budget.usdSpentToday`. Both exist in code
   (`api/v1alpha1/agent_types.go:497`, `:568`) and both are missing from the design's own schema block.
   Carried, and §12's A11 entry claims they were added. Add them to the block.
6. `:178` — handler step 4 lost step 3's "A failed swap means the record moved: re-read and re-enter." Step 3
   keeps it; step 4 performs the same compare-and-swap and now says nothing.
7. `:279` — "That precedence is not academic — `tools` is a behaviour aggregate whose `requiresApproval` leaf
   is behaviour by an explicit row, `external` is behaviour and `external.inlineCard` is policy". The first
   example is not an example: aggregate and leaf agree, so no precedence question arises and the two rules
   the sentence contrasts give the *same* answer. Only `external.inlineCard` demonstrates the rule. (The old
   text said `requiresApproval` was "in the policy column", which contradicted rule 1 — the fix removed a
   contradiction and left a non-example.) `internal/revision/leaves.go:150–240` confirms the claim
   "no leaf inside a third-party struct is overridden today" is true.
8. `:197` — the label-authority section dropped the platform precondition the old body carried:
   "`ValidatingAdmissionPolicy` is GA from Kubernetes 1.30, the chart's floor". The chart still asserts it
   (`charts/plume/Chart.yaml:15 kubeVersion: ">=1.30.0-0"`). The mechanism the entire run-namespace security
   argument rests on now states no version premise.
9. `:5` — the ADR list drops **0025** (P4 governance) while §3.1 `:49` and §5 `:437` still depend on design
   22's approval interceptor and kill switch, and does not list **0014** (`:56`, `:274`) or **0026**
   (`:127`), both cited in the body. Adding 0027 was right; 0025's removal is a loss.
10. `:293` — "One exception, stated because **the headline claim** is otherwise false." The antecedent went
    with the amendment tag ("A12's headline claim"). Name the claim: "…because the compulsory-gating claim
    above is otherwise false."
11. `:35` — "`sandbox: {profile: gvisor}  # optional ⇒ kind=Sandbox (singleton); with replicas>1 (CEL)`" is a
    fragment missing its verb. Read `:109`, it means "rejected with `replicas>1`".
12. `:3` — "the operator-owned run namespace **run, and are tested** on a real cluster" does not parse.
13. `:207` vs `internal/revision/collision_test.go:14` — the body and `internal/revision/revision.go:247` say
    the chosen collision took **1.2 seconds**; the test file says **2.1 seconds**. The measurement is the
    argument, so the corpus should carry one number or say why there are two.
14. `:406` — the §5 row "`expose: public` requires an approval label | Nothing reads such a label" now
    disclaims a guarantee the body no longer states anywhere (correctly — inventory E-3). Keeping the row is
    defensible as a record that the claim was withdrawn, but as written it reads as a live guarantee. Say
    "withdrawn; the body no longer claims it."

15. `:448` — "no traffic if it is the sole knowledge source (**architecture §04**)". The anchor resolves
    (`docs/architecture.md:150`) but states a **stronger** rule than the one it is cited for: *"Agents bound
    to a non-Ready graph get no traffic"*, with no "sole" qualifier. For an Agent binding two graphs where
    one goes non-Ready, architecture §04 says no traffic and design 02 §5 says `KnowledgeBound=False` →
    `Degraded` with traffic continuing. Design 03 `:209` already names this pair as a place two documents
    contradict each other, and `:222`'s requiredness table has two conditions where design 02 has one. The
    old body attributed the whole sentence to design 01 A5 (inventory G-11); the rewrite moved the second
    half to a document that says something else. Cite the narrower rule where it actually lives, or state
    that design 02 narrows architecture §04 and why.

Also noted and deliberately not raised as findings: `:100` dropped the pinning test's name
(`TestPrinterColumnsMatchTheDesign`), and §8 dropped the old body's reconcile-logic envtest line (retention
arithmetic, supersede-during-canary, condition transitions, finalization ordering). Both are compressions,
not losses of substance.

---

## Regression audit — the six load-bearing invariants and the fourteen smaller ones

I tried to break each of these and could not, except where noted.

| Invariant | Verdict |
|---|---|
| **Collision guard** — name (40 bits) vs digest; 1.2s chosen collision; `status` is authority / annotation corroborates; absent annotation refused, never adopted; terminal before the workload is touched; owned so it clears | **INTACT** (`:207–213`). Both corollaries intact at `:217–218` — `AlreadyExists` is a race; availability alone does not authorize promotion. The `revisionHash(spec)`/`revision.go:12` dead anchor is gone (G-3 fixed) |
| **One-read rule** — one GET, one buffer, hash and Create from it; the two-read race; six-step order | **INTACT** (`:324–333`). Step 5 correctly restated under content-first: "verify kind, the immutable bit, and the bytes; restamp metadata that has drifted" — the owner-UID predicate is gone, exactly as the inventory's caveat required |
| **Retained set** — single definition, `revisionHistoryLimit + 2 + holds`, ≥4 at the default, history *additional*, a hold *extends* | **INTACT** (`:236–240`). Verified no competing count survives anywhere in the body |
| **Binding record** — two proofs; bind-only-from-the-`Create`-response; two-state fence; live reads; teardown ordering; human-only recovery | **INTACT except MAJOR 1**. All six sub-invariants present at `:147`, `:149`, `:181`, `:151`, `:189`, `:191`. The strict-decode row has MINOR 2's gap |
| **Label authority** — three consumers, `status`/`finalize` subresources, "not a params object", the fail-close, "presence by name is all that is checked", two labels two boundaries | **INTACT** (`:195–199`, `:362`). Verified against `charts/plume/templates/admission.yaml:38–58` and the second policy's identity set, which the new text describes more accurately than the old |
| **Classification table** — capability-not-compile-path; symmetric egress gate with the `{A}→{A,B}→{B}` walk; compulsory leaf-level classification; the perturber registry failing closed; the design-20 fallback exception | **INTACT except the recursion guard (BLOCKER 2) and the map rule (MAJOR 7)**. `:249`, `:274–276`, `:279–281`, `:286`, `:293` all survive. The fail-closed perturber is real — `leaves.go:92` `t.Fatalf` on an unknown kind — and `TestEveryAgentSpecLeafBehavesAsClassified` (`leaf_test.go:206`) fails in both directions, so "adding a field must fail the build" is enforced |

Fourteen smaller invariants (inventory §7): **thirteen intact** — no cross-namespace tool/graph reference
(`:102`), A20's three refusals (`:299–305`), the copy properties (`:313–320`), why a whole namespace
(`:116`), naive truncation forbidden (`:122`), quota+LimitRange mirroring with the 2× cost (`:127`), Pod
Security mirroring with its honest limit (`:126`), the route must move (`:129`), the SPIFFE template's
fail-closed shape with **both** halves (`:360`), identity does not move with the Pod (`:358`), finalization
ordering (`:376`), loud degradation and `GovernanceSkipped` as a separate type (`:445`), `PricingStale` ≠ a
missing pricing row (`:440`). The fourteenth — the condition vocabulary — survives as 39 types matching 39
constants exactly (checked programmatically) but loses `EnvSourceProtectionUnavailable`'s reason for
existing (MAJOR 8).

## Mechanisms I verified rather than took on trust

- **`:98` the closure test.** Claimed: "a test that parses *this list* out of *this document* and compares
  both directions". **Mutation-killed**: adding `TotallyBogusCondition` to §3.1's list makes
  `TestConditionVocabularyIsClosed` fail with *"design 02 §3.1 declares "TotallyBogusCondition" and no
  constant in this package does"*. Restored from a backup copy; `go test ./api/...` green.
- **`:287` the recursion guard.** Claimed present; **disproved by execution** (BLOCKER 2).
- **`:454` §6's CEL list.** All five enforced: `agent_types.go:13` (exactly-one-of), `:69` (sandbox vs
  replicas), `:89` (digest-pinned image), `:247–274` (per-arm instance blocks), `:577` (≤52 chars).
- **`:458` the operator's escalation.** Accurate — `charts/plume/files/operator-rules.yaml` grants
  `create/delete/get/list/update/watch` cluster-wide on `configmaps, limitranges, namespaces,
  resourcequotas, secrets`, and `agents/status` is a separate resource, so "written only … through the
  status subresource, under separate RBAC" (`:211`) holds. This is the old body's B-16 defect fixed.
- **`:456` workload hardening.** Accurate — `agent_controller.go:922–969` sets `RunAsNonRoot`,
  `SeccompProfile: RuntimeDefault`, `AutomountServiceAccountToken: false`, `ReadOnlyRootFilesystem`,
  `AllowPrivilegeEscalation: false`, `Capabilities.Drop: [ALL]`.
- **`:320` deletion authority is the name.** Accurate — `material.go` deletes only derived-name objects and
  `test/envtest/material_test.go:411 TestALabelledBystanderObjectIsNotDeleted` pins it.
- **`:471` the real-cluster bullets.** All three exist:
  `e2e_test.go:495 TestANamespaceEditorCannotReplaceTheRevisionsCopy`,
  `:598 TestNamespaceLabelsAreReservedToTheOperator`,
  `:636 TestOperatorRoleGrantsWhatTheRunNamespaceCodeCalls` (SubjectAccessReview per verb).
- **The CI trap the inventory predicted (§H).** `go test ./test/docs` is **green**. Both rescued sentences
  were restated positively — `:299` "A revision identity cannot be produced from a spec by itself" and
  `:307` "A revision reads its own immutable copy" — and `:222` uses `revisionHash(projection)`, which the
  `spec-only-hash` rule's regex does not match. The rewrite handled this correctly.
- **The de-amendmentization claim.** Verified by regex over lines 1–493: **zero** bare design-02 amendment
  tags. The fifteen surviving `A<n>` tokens are design 01 A1/A5, 06 A3, 07 A2/A3/A5.x and 26 A1.
- **Cross-design anchors the inventory flagged as dead.** Three are now correct and one is newly wrong:
  `:238`'s "design 03 §3.1" resolves — `03-policy-compiler.md:70` states *"one record per member of design
  02 §3.3's retained set — `revisionHistoryLimit + 2 + holds`, at least four at the default"*, so G-9 is
  fixed; `:276`'s "design 27 §5" resolves — `27-compliance-packs.md:37` is "Pack contents" and `:44` carries
  `egress-allowlist.yaml` in the `compliance` priority band; `:111`'s cold-candidate premise is correctly
  reclaimed as this design's own — `16-evalsuite.md` contains **zero** occurrences of "cold" or "warm", so
  E-12 is fixed by attribution rather than by citation; `:102`'s design-11 anchor is newly wrong (MAJOR 9);
  and `:448`'s architecture §04 anchor points at a stronger rule (MINOR 15). `:17`'s architecture §17 now
  names its document (`docs/architecture.md:415–417`, "≈ **8 pods**"), fixing G-13.

## Where the work is right

The binding protocol is still the best-fenced piece of concurrency reasoning in this corpus, and the
renumbered fourteen checks are **exhaustive over (state × namespace presence)** — I walked every cell:
`Creating` is covered by 7/8/9/10, `Bound` by 11/12/13/14, `Terminating`/`Deleting` by 6, and the
binding-absent cases by 1/2/3. No row is unreachable and no pair overlaps ambiguously, because 4 and 5
precede the state rows for the stated reason (`:153`) and 8 and 12 precede their nonce/UID siblings. The
one hole is MAJOR 1, and it is a dropped sentence rather than a wrong rule.

The content-vs-name authority split (`:319–320`) is the inventory's most dangerous finding — A-6 — and it is
now stated exactly as the shipped operator behaves, with the denial-of-service argument that forces it.
Moving the operator-privilege statement from a material-table row into §6 with the real verb set is a
straight improvement over a row that understated its own subject. And the four sentences the inventory
marked **[keep the counter-example]** as load-bearing all survived: the 1.2-second collision, the two-read
race, delete-and-recreate under `immutable: true`, and "sealing is a control, and a control that is removed
stops controlling."

---

**design 02 (consolidated body): REVISE** — 3 BLOCKER, 9 MAJOR, 15 MINOR.

Fix order: BLOCKER 1 first, because it is a live fail-open in the guarantee ADR-0006 exists for and it is
mis-described in the one table that promises honesty. BLOCKER 3 next — it is a one-line revert and it is
what lets the rest of this be reviewed rather than assumed. BLOCKER 2 and MAJOR 7 are the same defect at two
scales: §5's "every" is only worth writing if it is true, and it is cheaper to fix §5 than to keep
discovering what is missing from it. MAJOR 1 and MINOR 2 and 3 are three dropped sentences in §3.2 and cost
a paragraph between them. MAJOR 3 is a mechanical rename that must land in the same commit or the numbers
mean nothing.

---

# Round 2 — verifying closure of round 1

- **Scope**: the same body (`docs/designs/02-agent-crd-operator.md` §1–§11, now lines 1–516; §12 out of scope),
  plus the files the fixes touched: `docs/designs/11-connector-crd.md`, `docs/decisions/0027-crd-ergonomics.md`,
  `docs/designs/README.md`, `internal/revision/leaves.go`, `internal/revision/leaf_test.go`,
  `internal/controller/runnamespace.go`, `test/envtest/runnamespace_test.go`.
- **Method**: round 1 read first, then `git diff`, then the body cold. Every one of round 1's 27 findings
  decided against the repository. Two verified by execution: the cycle guard mutation-killed and restored
  from a `cp` backup, and the gates rule walked through `gatesSatisfied`/`assessGates` cell by cell. Full
  suite re-run for regressions: `go build`, `go vet`, `go test ./api/... ./internal/... ./test/docs/...`,
  and `make envtest` (60.6 s) — **all green**.
- **Verdict**: **REVISE — 0 BLOCKER, 5 MAJOR, 8 MINOR.**

**The substance has converged.** Twenty-two of round 1's twenty-seven findings are closed outright, five are
partial, none is open, and no BLOCKER survives — the first round in this design's history where that is true.
The two findings that needed execution both hold up: the cycle guard is real and its test kills a mutation
in 5.00 s with a named message rather than timing out, and the gates rule now says one thing at four sites
and that thing is what the operator does. Nothing in the load-bearing set regressed.

What the fixes introduced is a single recurring failure: **the change corrected a document and then left
design 02 describing the uncorrected version.** Three of the five MAJORs are that — ADR-0027 corrected
while §3.1 still says it needs correcting, `registrationDeadline` corrected at two sites and left wrong at
the third, the guard implemented while §3.3 still specifies a key that could not have worked. §12 at `:523`
claims "every finding is applied"; five were applied to most of their sites.

---

## Job 1 — closure ledger, all 27 findings

| # | Round 1 finding | Verdict | Evidence |
|---|---|---|---|
| **B1** | "≥1 gate in prod" stated three ways, enforced none | **CLOSED** | `:72` schema comment now "a DECLARED gate is enforced at reconcile (§3.3). Nothing requires an Agent to declare one — §5"; `:246–250` the three-outcome table; `:432` §5 row; `:456–457` two §5 failure rows. Walked against `agent_controller.go:1014` `gatesSatisfied` and `:1030` `assessGates`, cell by cell — see below |
| **B2** | recursion guard claimed, absent, hangs | **PARTIAL** | Guard implemented at `leaves.go:61–72`, mutation-killed (below). But `:305` still specifies the key as `(type, path)`, which cannot fire — MAJOR 1 |
| **B3** | moved to "approved" without a passing critique | **CLOSED** | `:3` "body consolidated 2026-09-04 …; critique pending. No critique of this design has returned PASS … so it is not approved and must not be cited as such"; `README.md:8` matches |
| **M1** | check 5 lost `Terminating`/`Deleting` | **CLOSED** | `:171` "From `Terminating` or `Deleting`: teardown is already under way, so fall through to check 6 rather than swapping again — a compare-and-swap *from* `Bound` would fail there". Read against `runnamespace.go:412–426`: the code inlines the handler call rather than literally reaching check 6, but the effect is identical (`runNamespaceHandler(ctx, b, nil)`, then `ReasonTerminating`), and the stated reason for not swapping is exactly why the code has no `bindingTerminating` arm |
| **M2** | §2 under-counted operator state | **CLOSED** | `:21` adds "per revision, an immutable copy of every referenced `ConfigMap` and `Secret` … These hold credential bytes"; `:22` adds the `plume-mirror-*` objects as the reconstructable case. New MINOR 3 on the count sentence |
| **M3** | 24 `Row N` references invalidated | **PARTIAL** | 23 of 24 renumbered and **each verified against the new table** (mapping old *n*→new: 1→2, 2→3, 3→4, 4→5, 5→6, 6→7, 7→9, 8→10, 9→11, 10→12, 11→13, 12→14). One site missed — MINOR 1 |
| **M4** | `registrationDeadline` called an operator constant | **PARTIAL** | `:104` and `:430` corrected to "neither a field nor a constant". `:362` still says "an operator constant" — MAJOR 2 |
| **M5** | §8 asserted a transition-matrix test | **CLOSED** | `:496` **Owed**: "an exhaustive transition matrix over the four binding states, rather than the per-check cases that exist" |
| **M6** | Immutability row stated a rule the operator breaks | **CLOSED** | `:329` "**One branch does recreate, and it is safe for a stated reason**: a copy whose bytes match but which is **not** immutable is deleted and recreated from the buffer" — matches `material.go:357–368` `repairMetadata` → `recreateMaterial` exactly, reasoning included |
| **M7** | §5's universal claim false | **PARTIAL** | Narrowed at `:418` to "in the present tense", and all four demanded rows added (`:429`, `:430`, `:432`, `:434`) and verified. Three fresh counter-examples survive — MAJOR 4 and MAJOR 5 |
| **M8** | `EnvSourceProtectionUnavailable` undefined | **CLOSED** | `:106`, one paragraph, naming the owned-condition set as the clearing mechanism and the closure test's two directions |
| **M9** | tool-name gap moved, not closed | **PARTIAL** | `11-connector-crd.md:88–100` is a real §11 that names the gap, the two consequences, and what is owed; `README.md:17` flags it; `0027-crd-ergonomics.md:22` carries an explicit correction. But `:112` now asserts ADR-0027 "still states the uniqueness as enforced at admission and must be corrected" — MAJOR 3 |
| m1 | "in the examples above" | **CLOSED** | `:104` "Two names this design uses elsewhere are not fields of this schema" |
| m2 | `runNamespaceUID` state-conditional | **CLOSED** | `:154` "One field is state-conditional and a decoder that misses it wedges the operator on its first reconcile" |
| m3 | check 7's `AlreadyExists` branch | **CLOSED** | `:172`, with the "never treated as success" reason. Nit at MINOR 6 |
| m4 | discovery failure resolution | **CLOSED** | `:244` "A discovery failure never becomes an answer: the previous answer stands, or 'installed' if there is none" — verbatim the behaviour at `discovery.go:88–99` |
| m5 | `status.eval` / `usdSpentToday` missing from the block | **CLOSED** | `:79–80` |
| m6 | handler step 4 lost the failed-swap clause | **CLOSED** | `:198` |
| m7 | `requiresApproval` non-example | **CLOSED** | `:297` now uses only `external`/`external.inlineCard` |
| m8 | VAP platform premise | **CLOSED** | `:209`; `charts/plume/Chart.yaml:15 kubeVersion: ">=1.30.0-0"` still backs it |
| m9 | ADR list | **CLOSED** | `:5` — 0025 restored, 0014 / 0026+A1 / 0029 added |
| m10 | "the headline claim" antecedent | **CLOSED** | `:311` "because the compulsory-gating claim above is otherwise false" |
| m11 | `sandbox` fragment | **CLOSED** | `:38` "rejected with `replicas>1` (CEL)" |
| m12 | `:3` does not parse | **CLOSED** | rewritten as "What *runs* and is tested on a real cluster: …" |
| m13 | 1.2 s vs 2.1 s | **CLOSED** | `:217` carries both and says why the argument survives the difference |
| m14 | `expose: public` row | **CLOSED** | `:424` "**Withdrawn** — the body no longer states it. Recorded here because it was claimed for several rounds" |
| m15 | architecture §04 anchor | **CLOSED** | `:471` now cites design 01 §5 (`01-…:136`, inside §5 at `:132` — verified, and it does carry both halves), names architecture §04's stronger rule, and says this design follows the narrower one rather than choosing silently |

**Totals: 22 CLOSED, 5 PARTIAL, 0 OPEN.**

### B2, by execution

`go test ./internal/revision/ -run 'TestASelfRecursiveTypeFailsTheWalkRatherThanHanging|TestATypeReachedTwiceOnDifferentBranchesIsNotACycle'` → PASS. Backed `leaves.go` up with `cp`, deleted the guard loop at `:63–71` (leaving `stack = append(stack, typ)` so the mutation compiles), re-ran:

```
    leaf_test.go:296: the leaf walk did not terminate on a self-recursive type. It hangs, so CI reports a
    timeout instead of naming the field that introduced the cycle — …
--- FAIL: TestASelfRecursiveTypeFailsTheWalkRatherThanHanging (5.00s)
```

**KILLED, and killed correctly**: the test names the defect in 5.00 s instead of dying at the suite timeout,
which is precisely the INVALID-vs-KILLED distinction `:305` argues for. The `fakeFataler` is what makes that
possible — a real `*testing.T` would `Goexit` and prove nothing about the message. Restored from the backup;
`go test ./internal/revision/...` green. The companion test
(`TestATypeReachedTwiceOnDifferentBranchesIsNotACycle`) is the necessary other half: a *visited set* rather
than a *path stack* would fail every real walk, and it pins that.

### B1, by walking the code

`gatesSatisfied` (`agent_controller.go:1014`) and `assessGates` (`:1030`) partition on (CRD present) × (gates declared):

| | gates declared | no gates |
|---|---|---|
| **CRD absent** | promote, `GatesSkipped=True/EvalSuiteCRDAbsent` | promote, `GatesSkipped=True/NoGatesDeclared` |
| **CRD present** | hold, `GatesPassed=False/GateControllerUnimplemented` | promote, `GatesSkipped=True/NoGatesDeclared` |

`:248` and `:250` describe three of the four cells correctly. The fourth is MINOR 2. The fourth code arm
(`EvalSuiteInstalled == nil` → `GateDetectionUnwired`) is unreachable through `NewAgentReconciler`, which
refuses a nil hook at `:116`, so "Three outcomes" is honest for a shipped operator.

---

## Job 2 — new defects

### MAJOR 1 — the implemented guard is keyed by type; §3.3 still specifies `(type, path)`, which can never fire

`docs/designs/02-agent-crd-operator.md:305`

> "So the walker carries a stack keyed by **`(type, path)`** and fails immediately on meeting a type already on it"

`leaves.go:61` carries `stack []reflect.Type` and matches on `seen == typ` alone. The difference is not
cosmetic: the path grows by one field name at every recursion, so on `type cyc struct{ Next *cyc }` the pairs
are `(cyc, "spec")`, `(cyc, "spec.Next")`, `(cyc, "spec.Next.Next")` — **all distinct, forever**. A guard
keyed by `(type, path)` never matches and the hang returns. I proved the difference is load-bearing above:
with no firing guard the walk runs past 5 s and the test reports it.

So an implementer building from §3.3 writes the guard the design specifies and gets the failure mode the
paragraph exists to prevent. The one thing `(type, path)` is presumably reaching for — that a type met twice
on *sibling* branches is not a cycle — is what a path *stack* already gives, and is pinned by
`TestATypeReachedTwiceOnDifferentBranchesIsNotACycle`; the design never states that requirement.

**Fix.** `:305` → "the walker carries a stack of the types on the current path and fails immediately on
meeting one already on it, naming the path that closed the cycle. A stack, not a visited set: `AgentSpec`
reaches several types on more than one branch, and those are not cycles." Delete `(type, path)`; it is also
repeated at `:523`.

### MAJOR 2 — `registrationDeadline` is corrected at two sites of three, and §3.4 keeps the status upgrade round 1 raised

`docs/designs/02-agent-crd-operator.md:362`, against `:104` and `:430`

- `:104` "**neither a field nor a constant**: registration and card handling are unimplemented"
- `:430` (§5) "**Neither a field nor a constant.**"
- `:362` (§3.4) "retries continue with backoff for `registrationDeadline` (30 minutes; **an operator
  constant**, not a spec field — §3.1)" — and it points at `§3.1`, which says the opposite.

`grep -rn "RegistrationDeadline\|registrationDeadline\|30 \* time.Minute" api internal cmd charts test`
still returns nothing. This is round 1's MAJOR 4 with two of its three sites fixed, and the surviving one is
the *cross-reference*, so a reader who follows the pointer lands on the contradiction. §12 at `:525` records
the correction as done.

**Fix.** `:362` → "retries continue with backoff for a `registrationDeadline` of 30 minutes; past it the
candidate is failed and deleted … **The deadline is neither a spec field nor an operator constant today
(§3.1, §5)** — nothing counts down."

### MAJOR 3 — §3.1 asserts ADR-0027 still needs the correction this same change made to it

`docs/designs/02-agent-crd-operator.md:112`, and `:425`

> "An amendment against design 11 is owed before this binding shape is safe, and **ADR-0027 … still states
> the uniqueness as enforced at admission and must be corrected with it.**"

`docs/decisions/0027-crd-ergonomics.md:22` no longer states it. It was corrected in this change and says so
explicitly: *"**Correction (2026-09-04): that uniqueness is enforced by nothing.** This ADR said 'enforced at
admission'; there is no such admission rule…"*. Design 02 is now the only document in the corpus asserting
that ADR-0027 asserts something it retracts on its own line.

Same defect at `:425`, §5: "Enforced nowhere, **and owned by no design** (§3.1)." `11-connector-crd.md:88`
is a section titled "Owed to this design", which opens *"This design must define the `MCPServer` kind and the
name-uniqueness rule"* and closes with an **Owed here** list. The rule is still undefined — that part of the
row is right — but it now has an owner, and the row denies it.

This is round 1's MAJOR 9 in mirror image: the finding was a false claim that design 11 had been asked; the
fix asked design 11 and left design 02 saying nobody had. A false cross-document citation is a false
cross-document citation in either direction, and this one is in the two paragraphs a reader consults to
decide whether the discriminator-free binding is safe.

**Fix.** `:112` → "…An amendment defining it is owed against design 11, which now records the gap in its
§11; ADR-0027 carries the same correction." `:425` → "Enforced nowhere. Design 11 §11 owns the owed
definition (§3.1); until it lands a collision is possible and the discriminator-free binding assumes it is
not."

### MAJOR 4 — §5's universal claim is false again: nothing emits an event, nothing exports a plume metric, and no alert ships

`docs/designs/02-agent-crd-operator.md:418` against `:238`, `:411`, `:458`, `:485`

§5 opens: *"Every guarantee this body states **in the present tense** that nothing in the repository
implements is here."* Four present-tense sites, no row, and not covered by the header's "waits on designs
03, 06, 05 and 16" carve-out — an event recorder waits on nothing:

- `:238` "the abandoned revision is appended to `status.supersededCandidates` **and emitted as an event**"
- `:411` (§4 reconcile outline) "update conditions + phase; **emit events**"
- `:458` (§5's own failure table) "recorded in `supersededCandidates` **+ event**"
- `:485` (§7) "**events on every transition**; metrics: reconcile latency, rollout duration, held-revision
  count, superseded-candidate count, card-drift count, registration-deadline failures, and gauges for
  `SandboxDowngraded` / … / `RunNamespaceUnavailable`. **Shipped alerts**: candidate held >1h, …"

`grep -rn "Eventf\|EventRecorder" internal/ cmd/` returns exactly one hit, and it is the code **conceding
the gap**:

```go
// internal/controller/agent_controller.go:1398
// The cap is not free. Design 02 §3.3 promises the abandoned revision is
// "appended to status.supersededCandidates AND emitted as an event" — status
// being the convenience and the event the durable record. No EventRecorder is
// wired yet, so past the cap the record is GONE rather than relocated.
```

So the repository names a design-02 promise, names the consequence (`maxSupersededCandidates = 10`; the
eleventh supersession silently drops the first), and §5 — whose whole job is to hold exactly that list —
does not carry it. No plume metric is registered anywhere (`prometheus`, `NewCounter`, `NewGauge` return
nothing outside vendor); only controller-runtime's own reconcile metrics are served via
`metricsserver.Options` in `main.go:112`. `charts/plume/templates/` contains no `PrometheusRule`, so
"Shipped alerts" ships nothing.

This is not a nit about telemetry. §5's opening sentence is the load-bearing claim of the whole
consolidation — round 1's MAJOR 7 said so, the fix narrowed the quantifier and added four rows, and the
narrowed quantifier is *still* false on the first thing I checked that was not already on round 1's list.
The honest list is only worth having if it is complete.

**Fix.** Add two §5 rows: "Events on transitions, and the superseded-candidate event | **No `EventRecorder`
is wired.** `status.supersededCandidates` caps at 10 and the eleventh supersession is lost rather than
relocated (`agent_controller.go`)"; and "The §7 metric set and shipped alerts | Only controller-runtime's
built-in reconcile metrics are exported; the chart ships no `PrometheusRule`." Or move §7's list under an
explicit "owed" heading. Do not leave `:485` reading as a manifest of what ships.

### MAJOR 5 — §4 claims server-side apply, which the operator does not use and which would dissolve §3.2's compare-and-swap fence

`docs/designs/02-agent-crd-operator.md:414`

> "Idempotent; **server-side apply with field ownership**. Operator state is CR status, the directory KV, and §3.2's binding record."

`grep -rn "FieldOwner\|ApplyPatchType\|ForceOwnership" internal/ cmd/ test/` returns nothing. Every write is
a plain `Create`/`Update`.

That is not an omission — it is the correct implementation, and §4 describes one that would break §3.2.
The binding protocol's fence is optimistic concurrency on `resourceVersion`:

```go
// internal/controller/runnamespace.go:590
if err := r.Update(ctx, b.obj); err != nil {
    b.state = from
    if apierrors.IsConflict(err) {
        return fmt.Errorf("binding %s moved while swapping %s→%s; requeueing to re-read: %w", …)
    }
```

§3.2 rests on that at four places — `:161` "the mutex plus live reads plus **compare-and-swap on the record**
is what actually fences the protocol", handler steps 3 and 4 ("A failed swap means the record moved: re-read
and re-enter"), and teardown step 2. A server-side apply does **not** conflict on a concurrent state change:
the operator owns `data.state` as its field manager and simply overwrites whatever a peer wrote. There is no
failed swap, so "re-read and re-enter" never fires, and the two-state `Terminating`→`Deleting` fence — whose
entire purpose is that "nothing flips back from `Deleting`" — stops fencing.

So the body states, in §4, a write mechanism that would defeat the concurrency argument §3.2 spends four
paragraphs establishing. It also has no §5 row, so it is a second instance of MAJOR 4.

**Fix.** `:414` → "Idempotent. Writes are `Create`/`Update` under optimistic concurrency — the binding
record's compare-and-swap (§3.2) depends on a `resourceVersion` conflict, which server-side apply does not
produce, so SSA is deliberately not used here."

---

## MINOR

1. `internal/controller/runnamespace.go:251` — the one site of 24 the renumber missed: *"without it a peer
   **mid-row 6** (namespace created, UID not yet recorded) is indistinguishable from a crashed predecessor,
   and **check 9** would delete its namespace."* Under the new table check 6 is "state `Terminating` or
   `Deleting`"; the intended one is check 7. The sentence contradicts itself two clauses later. It survived
   because "row" wraps across the line break (`mid-row\n// 6`), which a `// Row N` grep does not see. The
   other 23 sites are correct — I checked each against the new table, including the two that gained
   precision (`:395` now names both check 1's wait and check 3; `:446` correctly becomes check 8, a row that
   did not exist before). Vestigial "this row" also at `test/envtest/runnamespace_test.go:330`.
2. `:248` — the gates table's three rows are not a partition, and row 1 is wrong in the cell they overlap on.
   "CRD absent | rollouts proceed with a loud `GatesSkipped=True`, **naming the absent CRD**" is true only
   when gates are declared: `assessGates` puts `len(Spec.Gates) == 0` **first**, so an Agent with no gates
   and no CRD reports `NoGatesDeclared` and the message never mentions the CRD. Key row 1 "CRD absent, gates
   declared", or say "naming whichever of the two reasons applies".
3. `:18` — "Operator-owned state, named rather than counted as zero, because **two of the three kinds**
   cannot be reconstructed:" is followed by **four** bullets. Two are non-reconstructable, so the arithmetic
   only works if bullet 1 (`status` + the directory KV) is silently outside the count. The sentence is new
   with the M2 fix and the count was not updated with the list. Say "two of these" or "two of the three
   operator-owned object kinds — the binding record and the revision copies".
4. `:307` — "**Map-kinded leaves** … **are** perturbed over their key set and their value set" is a
   present-tense guarantee `leaves.go:103–110` does not meet (one perturbation moves key and value together),
   and unlike every sibling claim in §3.3 it carries no `(§5)` pointer. §5 `:434` does carry the row, so the
   contract holds — but a reader of §3.3 gets no signal. Append "(§5)".
5. `docs/designs/11-connector-crd.md:92` — "Checked against this document: there is no such rule here, **the
   string `MCPServer` appears nowhere**" is falsified by the paragraph it is in, and by `:90`, `:96`, `:99`.
   Say "appears nowhere in §§1–10". The same sentence's "**three** other documents" undercounts:
   `docs/designs/05-agent-directory.md:57` is a fourth, and it asserts more than the others — *"binding a
   tool still requires the Connector/MCPServer CR path **with admission checks**"*, an admission check that
   does not exist. Design 05 should be in §11's list.
6. `:172` — check 7's `AlreadyExists` branch says "**check 9 or 10** decides on the next pass". It can also
   be check 8: the squatting namespace may carry a deletion timestamp, which is the case check 8 was added
   for and which `runnamespace.go:446`'s own comment names. Say "checks 8, 9 or 10".
7. `internal/controller/agent_controller.go:1002–1013` — `gatesSatisfied` carries **two stacked doc
   comments**, and the first is the pre-fix one: *"With no EvalSuite CRD installed the gate requirement does
   not apply …; with it installed, the gate controller does not exist yet, so nothing can pass and the agent
   holds"* — which describes the CRD-present case as always holding, i.e. round 1's BLOCKER 1 reading. The
   second comment supersedes it. Delete the first; it is the exact sentence that made the design and the code
   look like they agreed when they did not.
8. §12 (out of scope, noted for the next pass): `:521` still says "`revisionHistoryLimit` and
   `registrationDeadline` are named as **operator constants**", contradicting `:104`; `:523` says "**every
   finding is applied**", which is true of 22 of 27; `:523` repeats the `(type, path)` key from MAJOR 1;
   and `:533` still cites "design 24 §4.1", the anchor `:112` and ADR-0027 both corrected to §4 (design 24
   has no §4.1 — its headings run §1–§11).

---

## Regression audit — nothing in the load-bearing set moved

I re-attacked each of round 1's six load-bearing invariants and the fourteen smaller ones. All still hold,
and two improved:

| Invariant | Round 2 |
|---|---|
| Collision guard | **INTACT** (`:217–227`), and MINOR 13 fixed *in place* rather than by deleting a number: `:217` now carries both timings and says why the argument survives the difference |
| One-read rule | **INTACT** (`:344–351`), step 5 unchanged |
| Retained set | **INTACT** (`:256–258`); still exactly one definition in the body. `collectGarbage` (`agent_controller.go:1094`) protects active + candidate *outside* the limit, matching |
| Binding record | **INTACT, and the round-1 hole is closed** — check 5's third branch is back at `:171` with the reason (a CAS from `Bound` would fail there), and MINOR 2's state-conditional decode is in the field table at `:154` |
| Label authority | **INTACT** (`:203–209`); `charts/plume/templates/admission.yaml:40` still names `namespaces`, `namespaces/status`, `namespaces/finalize`, and `:94` still resolves an omitted `parentRef` namespace to the request's |
| Classification table | **INTACT, and the guard is now real** — `:305`'s mechanism exists and is mutation-proven; only its stated key is wrong (MAJOR 1) |

Fourteen smaller invariants: **all fourteen intact**, including the one round 1 recorded as damaged —
`EnvSourceProtectionUnavailable` has its reason for existing back at `:106`. Condition vocabulary still
closes in both directions (`go test ./api/...` green). Zero design-02 amendment tags in lines 1–498,
re-verified by regex; the surviving `A<n>` tokens are design 01 A1, 07 A2/A3/A5.x and 26 A1.

Suite state after restoring the mutation: `go build ./...`, `go vet ./...`,
`go test ./api/... ./internal/... ./test/docs/...` and `make envtest` (60.6 s) all green.

## Where the work is right

The gates fix is the model for how this should go. Round 1 named three sites and a code path; the fix did not
pick the convenient one — it wrote a **three-outcome table** at `:246`, put the losing rule in §5 as a policy
nobody enforces, said *why* it cannot be admission (CRD-CEL cannot see another CRD), and pointed at the same
policy-controller binding §6 already owes for signatures. Three of four cells are exactly the shipped
behaviour and the fourth is a reason string. That is a design that stopped arguing with its own code.

The cycle guard is the second. It would have been cheaper to rewrite the sentence, and round 1 offered that
as an option. Instead the guard was implemented, with a `fakeFataler` so the test can assert the *message*
rather than assert from a process that died, with a 5-second race against a goroutine so the failure is a
named test failure rather than a suite timeout, and with a companion test pinning the
path-stack-not-visited-set distinction that is the only way to get the guard wrong in the other direction.
I tried to break it both ways and could not.

Design 11 §11 and the ADR-0027 correction are the right shape for an owed item: §11 states what three
documents assume, what the two consequences are, that only the *ergonomic* half of the flattening is
affected and the security half stands on design 24 §4, and what specifically is owed (the kind, the rule,
and a mechanism that cannot be CRD-CEL because it spans two kinds). The only thing wrong with that work is
that design 02 has not been told it happened.

---

**design 02 (consolidated body), round 2: REVISE** — 0 BLOCKER, 5 MAJOR, 8 MINOR.

Fix order: MAJOR 3 and MAJOR 2 first — both are one sentence each, both are the design describing a
correction that already landed, and both are in paragraphs a reader consults to decide whether something is
safe. MAJOR 1 next: the guard is right and its description is wrong, which is the worst way round, because
the code will keep passing while the spec teaches the hang. MAJOR 4 and 5 are one decision — whether §5's
sentence is a promise or a slogan. If it is a promise, events, metrics and the SSA line all get rows and
§4 gets corrected; if it cannot be kept, narrow it again to what it can hold and stop calling it "every".
MINOR 1 is a two-word edit in a code comment and should go in whichever commit touches `runnamespace.go`
next.

---

# Round 3 — did the round-2 fixes repeat the shape they were fixing?

- **Scope**: the same body (`docs/designs/02-agent-crd-operator.md` §1–§11, now lines 1–516; §12 out of scope
  except where a round-2 finding named it), plus the files round 2's fixes touched:
  `docs/designs/11-connector-crd.md`, `docs/decisions/0027-crd-ergonomics.md`, `internal/revision/leaves.go`,
  `internal/revision/leaf_test.go`, `internal/controller/agent_controller.go`,
  `internal/controller/runnamespace.go`, `internal/controller/conditions.go`,
  `internal/controller/material.go`, `cmd/operator/main.go`, `test/envtest/runnamespace_test.go`.
- **Method**: narrow and adversarial on the eight questions round 2's thirteen findings raise, each decided
  against the repository rather than against the prose. The renumbered check references swept exhaustively
  (all 32 sites, cell by cell against §3.2's fourteen rows). Full suite re-run: `go build ./...`,
  `go vet ./...`, `go test ./api/... ./internal/... ./test/docs/...`, `make envtest` (60.7 s) — **all green**.
- **Verdict**: **REVISE — 0 BLOCKER, 1 MAJOR, 4 MINOR.**

**Round 2's shape was not repeated.** Every one of the three "document corrected, design 02 left describing
the uncorrected version" defects is closed, and closed against the corrected documents rather than against a
promise to correct them. Ten of the thirteen findings are closed outright, three are partial, none is open.
The one MAJOR is not a new defect the fixes introduced — it is the same claim round 2 raised, still false:
§5's "every guarantee this body states in the present tense that nothing implements is here" survives its
third round without being true, and this time the counter-examples are two whole bullets of §3.2.

---

## Job 1 — closure ledger, all 13 of round 2's findings

| # | Round 2 finding | Verdict | Evidence |
|---|---|---|---|
| **M1** | guard implemented, `:305` still specified `(type, path)` | **CLOSED** | `:305` now "a **stack of the types on the current branch** … A key that included the path could never match". Matches `internal/revision/leaves.go:60–72` exactly: `stack []reflect.Type`, compared with `seen == typ` **before** `stack = append(stack, typ)`, `Fatalf` naming `prefix` (the path that closed it) and `typ`. See the sibling-branch walk below. `(type, path)` survives only at `:525`, where it is correctly framed as the key the body *had* specified |
| **M2** | `registrationDeadline` still "an operator constant" at `:362` | **CLOSED** | `:362` now "**neither a spec field nor an operator constant** (§3.1, §5) — nothing counts down", which is what `:104` and `:432` say. `grep -rn "RegistrationDeadline\|registrationDeadline\|30 \* time.Minute" api internal cmd charts test` still returns **nothing**. One restatement survives inside §5 itself — MINOR 2 |
| **M3** | §3.1 asserted ADR-0027 still needed the correction that had landed | **CLOSED** | `:112` now "ADR-0027 … **retracts** its 'enforced at admission' clause and points at the same owed item" — `0027-crd-ergonomics.md:22` carries exactly that retraction, verbatim. `:425` now "owed to **design 11 §11**, which records the gap" — `11-connector-crd.md:88` is `## 11. Owed to this design (2026-09-04, from design 02 §3.1 and ADR-0027)`, opening "This design must define the `MCPServer` kind and the name-uniqueness rule". Both citations now describe the current state of the cited document. An anchor nit inside design 11 — MINOR 3 |
| **M4** | §5 silent on events, metrics, alerts | **PARTIAL** | `:429` and `:430` added and both accurate: `grep -rn 'Eventf\|EventRecorder\|Recorder' internal cmd` → **one** hit, and it is `agent_controller.go:1396`, the comment conceding the gap; `grep -rn 'prometheus\|NewCounter\|NewGauge\|metrics\.Registry' internal cmd` → **nothing**; `charts/plume/templates/` holds no `PrometheusRule`. `maxSupersededCandidates = 10` confirmed at `agent_controller.go:62`. But §7 `:487` is unchanged (MINOR 2), and the claim the two rows exist to make true is still false (MAJOR 1) |
| **M5** | §4 claimed server-side apply | **CLOSED** | `:414` rewritten, and true of **every** write path: `grep -rn 'FieldOwner\|ApplyPatchType\|ForceOwnership\|\.Patch(' internal cmd test` returns only `test/envtest/counting_client_test.go:53,72`, a passthrough wrapper. Status writes are `r.Status().Update` (`agent_controller.go:1265`), material is `r.Create`/`r.Update`/`r.Delete` (`material.go:160`, `:392`, `:401`), the run namespace is `r.Create`/`r.Update`/`r.Delete` with a UID precondition (`runnamespace.go:461`, `:645`, `:660`), and no code clears a `resourceVersion`. `swapBinding` (`runnamespace.go:585–598`) is a real CAS with an `IsConflict` arm. The *reasoning* is imprecise — MINOR 1 |
| **m1** | `runnamespace.go:251` missed by the renumber | **CLOSED**, and the sweep is clean | `:251` now "a peer **mid-check-7** (namespace created, UID not yet recorded) … and **check 9** would delete its namespace. The Terminating/Deleting fence does not need this; **checks 7 and 9** do" — correct against the new table on all three numbers. I re-checked every remaining site against §3.2's fourteen rows: `runnamespace.go` `:335 :341 :357 :382 :389 :395 :396 :429 :446 :448 :455 :470 :483 :501 :506 :521 :533 :539`, `agent_controller.go:342`, and `runnamespace_test.go` `:155 :186 :197 :229 :269 :298 :327 :454 :462 :794 :817 :828 :864 :884 :1034` — **all correct**. `runnamespace_test.go:330`'s vestigial "this row" is now "this check". No stale `row`/`check` number survives in `internal/`, `test/`, `api/` or `charts/`; in `docs/` the only old numbers are in `reviews/02-a61-code-review.md`, which is a review record and correctly left as history |
| **m2** | gates table row 1 wrong in the overlapping cell | **CLOSED** | `:248` now "naming whichever reason applies — the absent CRD, **or no gates declared, which is checked first**". `assessGates` (`agent_controller.go:1030–1049`) branches in exactly that order: `len(Spec.Gates)==0` → `NoGatesDeclared`; `EvalSuiteInstalled == nil` → `GateDetectionUnwired`; `!evalSuiteInstalled()` → `EvalSuiteCRDAbsent`; default → `GateControllerUnimplemented`. Arm 2 stays unreachable through `NewAgentReconciler`, which refuses a nil hook at `:117`, so "Three outcomes" remains honest for a shipped operator |
| **m3** | §2's count vs its bullets | **CLOSED** | `:18` "because **two of these** cannot be reconstructed", over four bullets of which exactly two (the binding record, the revision copies) are stated as non-reconstructable |
| **m4** | map-leaf claim carried no `(§5)` | **CLOSED** | `:307` |
| **m5** | design 11 §11's self-falsifying sentence; design 05 missing | **PARTIAL** | `11-connector-crd.md:92` now "there is no such rule **in §§1–10**, the string `MCPServer` appears nowhere **in them**", and design 05 is added as the fourth document — under the wrong section number. MINOR 3 |
| **m6** | check 7's `AlreadyExists` said "check 9 or 10" | **CLOSED** | `:172` "checks 8, 9 or 10 decide on the next pass", matching `runnamespace.go:444–470` |
| **m7** | `gatesSatisfied`'s stale first doc comment | **CLOSED** | `agent_controller.go:1002–1015` carries one comment, and it describes the shipped partition ("evalSuiteInstalled reports true when the hook is unset, which lands here as 'assume the CRD is present'") |
| **m8** | §12 (out of scope, flagged) | **PARTIAL** | `:521`'s "operator constants" is corrected to "`revisionHistoryLimit` … the operator constant it is, and `registrationDeadline` as neither a field nor a constant"; the `design 24 §4.1` anchor is gone — `:112` and `:535` both say **§4**, which is `24-authz-provider.md:38` "Tuple lifecycle", correct; `(type, path)` at `:525` is now historical framing. What survives is `:525`'s "**every finding of both rounds is applied**", which this round contradicts on M4 |

**Totals: 10 CLOSED, 3 PARTIAL, 0 OPEN.**

### M1, by walking the guard against the sibling case

`leaves.go` passes `stack` **by value** and appends after the check. A child at depth *N+1* receives a slice
header of length *N+1* and appends at index *N+1*; a *sibling* child receives the same header and appends at
the same index, overwriting it — but neither ever reads past its own length, so each recursion sees exactly
the ancestors on its own path. That is a path stack, not a visited set, which is what `:305` now says and
what the sibling clause requires. `AgentSpec` really does reach types that way: `v1.LocalObjectReference` is
embedded in `ConfigMapKeySelector`, `SecretKeySelector`, `ConfigMapEnvSource` and `SecretEnvSource`, so it
sits on four sibling branches under `runtime.env`/`runtime.envFrom` — a visited set would fail every real
walk. Pinned in both directions by `leaf_test.go:282 TestASelfRecursiveTypeFailsTheWalkRatherThanHanging`
(mutation-killed in round 2, in 5.00 s, with the message asserted) and `:310
TestATypeReachedTwiceOnDifferentBranchesIsNotACycle`, with `TestEveryAgentSpecLeafBehavesAsClassified`
covering the real type as the third witness. **The sentence and the code now say the same thing.**

---

## Job 2 — what is still wrong

### MAJOR 1 — §5's universal claim is false for a third round, and this time it is two whole bullets of §3.2: no `Sandbox` is ever created, and no scratchpad exists at all

`docs/designs/02-agent-crd-operator.md:119`, `:121`, `:122`, against `:418`, `:457` and `:487`

§5 opens at `:418`: *"Every guarantee this body states **in the present tense** that nothing in the
repository implements is here, so a reader never has to infer it from silence."* The header's carve-out
covers "the gateway handoff, identity wiring, registration and the eval gate … designs 03, 06, 05 and 16".
Neither of these is in it — agent-sandbox and a PersistentVolumeClaim wait on no design.

- **`:119` "`runtime.sandbox` ⇒ agent-sandbox `Sandbox` (v1beta1) — singleton, stable identity, persistent
  scratchpad".** Nothing creates one. `deploymentFor` (`agent_controller.go:893–970`) is the only workload
  constructor in the tree; no agent-sandbox API is imported anywhere (`grep -rn "agent-sandbox\|sandbox.x-k8s.io"
  internal cmd go.mod` finds only two prose comments); and `assessSandbox` (`:1064`) sets
  `SandboxDowngraded=True` **unconditionally** for any Agent carrying `spec.runtime.sandbox` — there is no
  discovery step, unlike `EvalSuite`, which has a whole `discovery.go`. So §5's row at `:457` — *"Sandbox
  runtime absent | Hardened-Deployment fallback + `SandboxDowngraded`"* — states a **detection that never
  runs**. The repository concedes it in the function's own doc comment (`:1051`): *"The agent-sandbox CRD is
  not bound yet, so every sandboxed agent currently downgrades."*

  **Concretely**: install agent-sandbox, apply an Agent with `sandbox: {profile: gvisor}`. You get a
  Deployment and `SandboxDowngraded=True` naming a runtime that is present. §5 — the table whose stated job
  is to make exactly this visible — tells the reader the runtime must have been absent.

- **`:121`, the scratchpad bullet in full.** There is no volume. `deploymentFor`'s PodSpec has no `Volumes`
  and no `VolumeMounts` (`grep -n "Volumes\|VolumeMounts\|PersistentVolumeClaim" internal/controller/agent_controller.go`
  → nothing), and `CondScratchpadDegraded` appears in **no non-test file** under `internal/` or `cmd/`. So
  "revision-independent … reattached read-write to the promoted revision", "a candidate under eval attaches
  it **read-only**", and "`ScratchpadDegraded=True` names the reason" describe machinery that does not exist,
  §5's row at `:457` reads as a live degraded path, and §7 at `:487` registers a **gauge for a condition
  nothing ever sets**. The bullet's closing sentence — *"a candidate is therefore evaluated cold against a
  warm active"* — is the premise §12 records as reclaimed from design 16 as this design's own, and it rests
  on a warm scratchpad that is not there.

- Same table, smaller: **`:122` "and the CLI warns at deploy".** `cmd/` contains `operator` and nothing else;
  there is no CLI. §5 already knows this — `:426` carries the `plume logs` row for exactly that reason — so
  the omission is inconsistent rather than merely incomplete.

This is round 2's MAJOR 4 with different nouns, and it is worth saying why that matters rather than treating
it as a nit. `:418` is the sentence the whole consolidation is sold on; round 1 found four counter-examples,
round 2 narrowed the quantifier and added rows and then found two more, and round 3 found two more in §3.2 —
the *first* substantive section a reader meets. A list that has needed extending in every round it has been
checked is not yet an honest list; it is an incomplete one with an honest heading.

**Fix.** Two rows, and they are cheap:

- "`runtime.sandbox` materializing an agent-sandbox `Sandbox` | **Nothing binds agent-sandbox.** No `Sandbox`
  is ever created and the operator does not look for the runtime class: every Agent with `spec.runtime.sandbox`
  gets the hardened Deployment and `SandboxDowngraded=True`, on every cluster, installed or not."
- "The scratchpad — revision-independent volume, read-only candidate attach, `ScratchpadDegraded` |
  **No volume is created and `ScratchpadDegraded` is set nowhere.** §3.3's cold-candidate consequence is a
  statement about a design that has no code."

Then correct `:457`'s two rows to match, drop `ScratchpadDegraded` from §7's gauge list or point it at §5,
and either delete "and the CLI warns at deploy" from `:122` or give it a row.

---

## MINOR

1. **`:414`'s SSA reasoning is wrong about SSA, though its conclusion is right.** *"§3.2's fence is a
   compare-and-swap that needs a `resourceVersion` conflict to fire, and **SSA does not produce one**"* — an
   apply request that carries `metadata.resourceVersion` is subject to the same optimistic-concurrency check
   as an update, and an apply additionally 409s on **field-manager** conflicts unless forced. The reason the
   fence would dissolve here is narrower and stronger than the one given: every writer of the binding record
   is this one operator process under one field manager, so an apply of `data.state` would find no
   conflicting owner and would overwrite a peer's transition silently. As written, an implementer is taught
   a general falsehood in order to reach a correct local decision. Say: "…and an apply from the operator's
   own field manager produces none — SSA conflicts on another *manager's* fields, and every writer of this
   record is this process."

2. **§7 and §5's own failure table restate, in the present tense and with no pointer, three guarantees §5's
   first table denies.** `:487` "events on every transition; metrics: …; **Shipped alerts**: …" against
   `:429`/`:430`; `:238` "and emitted as an event" against `:429`; and — inside §5, twenty-two lines apart —
   `:454` *"failed and deleted at the registration deadline so no retention slot leaks"* against `:432`
   *"nothing counts down and no retention slot is freed on a stuck card fetch"*. §5's contract technically
   holds in each case, because the row exists. But every sibling claim in this body carries an inline `(§5)`
   — `:127` the NetworkPolicy, `:307` the map leaves, `:362` the deadline, `:383` the typed credential
   binding, `:470` SPIRE — and §7 is precisely where an operator goes to ask what they may alert on. Round 2
   offered "add the rows **or** move §7's list under an owed heading"; the rows landed and §7 did not move.
   Append `(§5)` at `:238` and `:487`, and at `:454` say "…at the registration deadline — **which nothing
   counts down** (first table)".

3. **`docs/designs/11-connector-crd.md:92` cites the wrong section of design 05.** *"design 05 **§4** goes
   further still — it says binding a tool 'requires the Connector/`MCPServer` CR path **with admission
   checks**'"*. That sentence is `docs/designs/05-agent-directory.md:57`, which is inside **§3** (§3 opens at
   `:17`, §4 at `:60`). This is the fix for round 2's MINOR 5, and it lands a wrong anchor in the one section
   of the corpus whose entire subject is four documents citing a rule nobody wrote. Say "design 05 §3".

4. **Noted, outside the eight questions and not a round-2 regression: `RevisionMaterialUnavailable` is set and
   can never clear.** `internal/controller/agent_controller.go:358` sets it; `conditions.go:44–66` lists it in
   neither `ownedTypes` nor `stickyTypes`, so `merge()`'s `default` arm (`:98`) carries it forward as "another
   controller's — leave it alone", forever. No other controller writes it. An Agent that recovers reports
   `Ready=True` beside a stale `RevisionMaterialUnavailable=True` — which is exactly what §3.3 `:227` argues
   against for `RevisionHashCollision` (*"the condition is **owned**, so it clears … an agent that reported
   `Ready=True` while still carrying a stale `True` collision condition is what teaches an operator that
   conditions mean nothing"*), and exactly the defect `conditions.go:62–64` records the A61 code review as
   having found for `RunNamespaceUnavailable`. The body never claims this one clears, so it is a code defect
   rather than a text defect. Add it to `ownedTypes`.

---

## Regression audit — nothing moved, and one thing improved

I re-attacked the six load-bearing invariants and the fourteen smaller ones against the code, not the prose.

| Invariant | Round 3 |
|---|---|
| Collision guard | **INTACT** (`:217–227`); `status` still the authority, the annotation still corroborating, both timings still carried |
| One-read rule | **INTACT** (`:344–351`); the six steps still match `material.go`, and step 4's `AlreadyExists` is still a race |
| Retained set | **INTACT** (`:256–258`); still exactly one definition, and `collectGarbage` (`agent_controller.go:1088+`) still protects active and candidate *outside* the limit |
| Binding record | **INTACT**; all fourteen checks re-walked against `runnamespace.go:325–545` and the handler against `:600–700`. `swapBinding`'s CAS survives the §4 rewrite — which was the point of MAJOR 5 |
| Label authority | **INTACT, and better than round 2 recorded it**: the startup half of "at startup and on every reconcile" is now real — `cmd/operator/main.go:154–166` registers a `RunnableFunc` that calls `labelAuthority` and logs, non-fatally, before the reconciler starts |
| Classification table | **INTACT**; the guard is real, its description now matches it, and both guard tests plus `TestEveryAgentSpecLeafBehavesAsClassified` are green |

Fourteen smaller invariants: **all fourteen intact**, including `EnvSourceProtectionUnavailable`'s reason for
existing (`:106`, and `conditions.go:54`'s owned entry with its comment). The condition vocabulary still
closes in both directions. Zero design-02 amendment tags in the body, re-verified.

Suite: `go build ./...`, `go vet ./...`, `go test ./api/... ./internal/... ./test/docs/...` and
`make envtest` (60.687 s) — all green. Nothing was mutated in this round; round 2's two mutation proofs
(the cycle guard, the gates partition) were re-read rather than re-run, and the tests that carry them pass.

## Where the work is right

The three "corrected elsewhere, uncorrected here" defects are the reason this round exists, and all three are
closed properly — not by softening the sentence but by going and reading the other document. `:112` now
quotes ADR-0027's retraction rather than asserting it is owed; `:425` names design 11 §11 as the owner
because design 11 §11 exists and says so; `:362` says the same words as `:104` and `:432`. That is the
failure mode round 2 named, and it did not recur.

The cycle-guard sentence is the best of the fixes. Round 2's finding was that a correct guard was described
by a key that could never fire — the worst way round, because the code keeps passing while the spec teaches
the hang. The rewrite does not just delete `(type, path)`: it says *why* a path-keyed guard cannot match
("the path grows at every step"), states the sibling-branch requirement the code has and the old text never
did, and leaves the discarded key in §12 as provenance rather than erasing it. I tried to break the stack
through slice aliasing between sibling recursions and could not; the semantics are correct for a reason the
sentence now states.

The renumber sweep is the other one. Thirty-two numbered cross-references across two source files and one
test file, every one of them checked and correct, including the two that gained precision — and the review
records left carrying the old numbers, which is the right call.

---

**design 02 (consolidated body), round 3: REVISE** — 0 BLOCKER, 1 MAJOR, 4 MINOR.

The substance has converged and the round-2 fixes did not repeat their own shape. One thing stands between
this design and a PASS, and it is the same thing it was in round 1 and round 2: §5 says "every" and means
"most". Either finish the list — the two rows above are twenty minutes — or stop claiming a quantifier the
document has never once satisfied and say instead what it does hold: "the guarantees this body states in the
present tense that nothing implements, as far as three critiques have found them." The first is better. The
second is at least true. What must not survive a fourth round is a sentence that promises completeness and
is disproved by the section directly above it.
