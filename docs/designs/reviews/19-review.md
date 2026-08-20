# Review: Design 19 — Knowledge pattern packs (`sop-decision-tree` first)

- **Verdict**: **REVISE**
- **Reviewed**: `docs/designs/19-knowledge-pattern-packs.md` (draft, 2026-08-20)
- **Independence note**: reviewed in an independent session that did not author the draft.
- **Method**: all 8 lenses, with the requested focus: line-by-line expressibility of the `sop-decision-tree` template against ontology/v1 **as amended** (12 r2 + §11 A1 — canonical triple refs, closed generators, via-required probes, `health.pass_threshold`), and label claims against 14 r2's gate statistics.

## Findings

### 1. MAJOR — the flagship bundle is not expressible: `walk`'s parameter schema cannot describe the SOP traversal

`19-knowledge-pattern-packs.md:38` vs design 12 §3.5 and the pack's own relations (`:36`). The `next_step` bundle — the pack's entire pitch ("walk, don't retrieve") — uses `recipe: walk` with params `{machine: Procedure, edge: "Decision BRANCH Step", condition_attr: condition}`. But the SOP graph's traversal needs **three edge roles**: an entry edge (`Procedure STARTS_AT Step`), sequence edges (`Step NEXT Step`, `Step NEXT Decision`), and the conditional branch edge (`Decision BRANCH Step`). The walk schema (inherited verbatim from 12's sketch) names exactly *one* edge — there is no way to tell the recipe how to enter the machine or advance between steps, and no defined input for the caller's *state* (which step am I at?), which `next_step(state)` requires. The recipe engine (13 §3.3) implements "the param schema fixed in 12" — so as specified, the reference provider cannot execute the pack's headline bundle. This is an under-specification in 12 that only becomes visible at first real use — which is exactly what this pack is for.

**Fix**: amend design 12 §3.5 (a §11 A2) with walk's real schema — recommended: `{entry: <triple ref>, sequence: [<triple refs>], branch: <triple ref>, condition_attr, state: <input param: the current node key>}` — semantics: from `state` (or `entry` when absent), follow `sequence` edges, stopping at the first `branch` node with its conditions rendered into `structure` (design 01's `next_step` payload). Update the pack's params to match, and add a conformance case (design 01 suite) exercising the full walk on the clinic-sop fixture — the fixture already exists for exactly this.

### 2. MINOR — relation-level `2..*` is not in ontology/v1's cardinality vocabulary (and the pack already declares the constraint correctly elsewhere)

`19-knowledge-pattern-packs.md:36-37` vs design 12 §3.3, which enumerates exactly `1..1, 0..1, 1..*, *..*`. The template puts `cardinality 2..*` on `"Decision BRANCH Step"` — invalid per the closed grammar — while §4's invariants line *also* declares `{type: cardinality, rel: "Decision BRANCH Step", min: 2}`, which is valid and sufficient (and matches 12's own example). The pack's own instantiation check would catch this, which proves the check works but not the doc. **Fix**: drop the relation-level `2..*` (the invariant carries it), or — if generalized min-bounds are genuinely wanted at the relation level — amend 12's grammar to `<min>..<max|*>` explicitly. Dropping is simpler; the lowering rule (12 r2 §3.4) already makes the invariant the single enforcement path.

### 3. MINOR — `per_branch` expansion semantics are undefined, and same-version expansion can only test machinery, not truth

`19-knowledge-pattern-packs.md:39` vs designs 12 §3.6 / 15 r2. "One per BRANCH edge, expected next step + citation" — expanded *when*, against *which graph*? If the generator expands against the version under test, the probe asserts the graph contains what the graph contains: a tautology as a knowledge check (a mis-extracted branch generates a probe expecting the mis-extraction), though genuinely useful as a *machinery* check (recipes, indexes, citations resolve). If it expands against the prior version, it's a regression check with different semantics. Neither 12 nor 15 says. The design's own D3 (wizard insists on 3–5 hand-written probes) shows it senses the gap — now state it. **Fix**: define expansion as same-version at probe-set derivation, documented as a mechanical-readiness check ("every branch is walkable and cited"), with truth carried by hand-written probes (D3) and Gate A; one sentence each in 12's generator definition and this pack's §4.

### 4. MINOR — `via` (12 §11 A1) isn't threaded through the pack's probes

`19-knowledge-pattern-packs.md:39`. Ontology/v1 now requires `via` on every probe. Two loose ends: (a) generated probes — the `per_branch` generator's *contract* must state the `via` it emits (presumably `via: {bundle: next_step, params: {state: <branch's decision node>}}`), since a generated probe without `via` now fails validation; (b) the wizard's hand-written probe slots elicit question/answer/citation but not the execution (`via`) — the wizard must ask which bundle (defaulting sensibly to `next_step` or search) or the collected probes are invalid. **Fix**: one line in the generator definition (12) and one wizard variable here.

### 5. MINOR — "the Gate A floor" overstates 12 starter snippets against 14 r2's statistics

`19-knowledge-pattern-packs.md:41` vs design 14 r2 §4. Twelve synthetic snippets yield perhaps 40–80 labeled instances — far below 14 r2's n ≥ 200 authority threshold, so a freshly instantiated pattern graph runs Gate A in **advisory mode with `LabelsSparse`** by construction. That's fine — starter labels are a smoke set that catches gross prompt breakage — but "the Gate A floor" implies gating authority the sample cannot have. **Fix**: rewrite the sentence in 14 r2's vocabulary ("starter labels run the gate advisory/`LabelsSparse`; authority comes from the user's labels via the growth engine") in §4 and the pack README handoff text.

### 6. MINOR — no Security section; template descriptions are an extraction-prompt-injection surface

`19-knowledge-pattern-packs.md` (structure) vs TEMPLATE §6. Packs are signed artifacts (09/18 machinery) — cite it — but the sharper point deserves stating: per 12 D4, **entity/relation descriptions become the extraction prompt**. A hostile or careless pack template can steer what the extraction LLM produces ("also record any credentials you encounter as attributes…"), which is prompt injection with a supply-chain flavor. Mitigations already exist and just need naming: packs are cosign-signed and curated; instantiation is human-reviewed in the wizard (the doc is shown, never silently applied — 12 D2/architecture §13); Gate A measures extraction against human labels, bounding what a steered prompt can smuggle. **Fix**: add the section with those four sentences.

## Lens summary

1. **Doctrine**: exemplary — pure fast-plane, zero code, and D1 re-states the boundary exactly where "the temptation will live"; the contract-revision escape for new recipes/generators is the charter working as designed.
2. **Charter**: clean (Artifact primitive, packs socket per ADR-0008); this design is the first real consumer of 12 r2's closed sets, which is why it surfaced finding 1.
3. **Contract consistency**: the requested checks — canonical triple refs ✓ (used correctly throughout, including the `via:` list in `path_terminates`), closed generators ✓ (uses `per_branch`, defines nothing), bare-VERB uses are all globally unique ✓; findings 1/2/4 are the three places the template outruns or lags the amended grammar.
4. **Hidden dependencies/circularity**: finding 3 is the subtle one (self-referential probes); the self-contained instantiation model (copied, human-edited, lineage-only) correctly avoids runtime pack coupling.
5. **Failure modes**: good table — publish-gate-on-own-fixtures (D2) is the strongest line in the design ("a pattern that can't pass its own demo is not a pattern"); the free-text-variable row honestly routes damage to where it's measurable (Gate A).
6. **Security**: finding 6.
7. **Research freshness**: nothing external and load-bearing; the pack builds entirely on landed platform contracts. n/a.
8. **Testability**: per-pack CI (schema → instantiation → fixture build → own probes → golden graph diff) is a complete gate; after finding 1, the walk conformance case belongs in design 01's suite so every provider must execute it, not just Graphiti.

## Disposition

**REVISE.** The pack contract and the publish-gate discipline are exactly right, and the design correctly treats itself as data all the way down. The one MAJOR is the valuable kind: the first honest attempt to *use* ontology/v1's recipes revealed that `walk` — the recipe the flagship pattern exists to showcase — is under-parameterized for its own headline use case. Fix lands mostly as a design 12 amendment plus a conformance case; the five minors are alignment and wording against the freshly amended grammar and gate statistics.

VERDICT: REVISE — 6 findings

---

## Re-review r2 (2026-08-20)

- **Verdict**: **PASS** (1 residual nit)
- **Independence note**: same independent session as r1; did not author the draft or revision.

### Per-finding disposition

| r1 | Severity | Disposition |
|---|---|---|
| 1 | MAJOR | **Resolved across all three documents, exactly as recommended.** Design 12 §11 A2(a) defines walk's real schema (`entry`/`sequence`/`branch`/`condition_attr`/`state?`) with the traversal semantics spelled out; the pack's `next_step` params now use it; design 01 §11 A3 adds the conformance full-walk case (from entry *and* from mid-machine state, asserting branch node, rendered conditions in `structure`, citations) — so no provider can claim kgp support without executing the flagship traversal. The under-specification is closed at the contract, the consumer, and the enforcement point. |
| 2 | MINOR | **Resolved.** Relation-level `2..*` dropped; the min-2 constraint lives solely in the cardinality invariant, with the reason noted inline. |
| 3 | MINOR | **Resolved.** A2(b): `per_branch` expands same-version at probe-set derivation, explicitly labeled a *mechanical-readiness* check with truth carried by hand-written probes and Gate A — the tautology is now a documented property, not a hidden one. |
| 4 | MINOR | **Resolved.** The generator's contract emits `via: {bundle, params: {state: <decision-node key>}}` (A2b), and the wizard elicits each hand-written probe's execution (default `next_step`, search fallback) — collected probes satisfy the via-required rule by construction. |
| 5 | MINOR | **Resolved.** Starter labels restated in 14 r2's vocabulary — smoke set, advisory under `LabelsSparse`, authority via the growth engine — in both §4 and the README handoff. |
| 6 | MINOR | **Resolved — beyond the ask.** Security section added with the prompt-injection edge named and its pre-existing containments listed, plus a new one (hardened profiles may allowlist pack sources). |

### Residual nit (non-blocking)

A2(a)'s entry-mode semantics say "follow `entry` from the machine root when `state` is absent" — but a graph holds many machine instances (many Procedures), and the schema has no instance selector. Add `root?: <node-key>` with "exactly one of `state`/`root` required" to the walk schema (one line in 12 A2) so entry-mode calls are unambiguous; the conformance case (01 A3) should pass `root` explicitly.

### Verdict

**PASS.** All six findings resolved, the MAJOR at contract + consumer + conformance simultaneously — the right way to fix a gap that a first consumer exposed. Fold into ADR-0023 with the `root?` one-liner.
