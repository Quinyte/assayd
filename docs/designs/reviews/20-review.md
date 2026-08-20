# Review: Design 20 — Drift controllers

- **Verdict**: **REVISE** (minors only — a quick r2)
- **Reviewed**: `docs/designs/20-drift-controllers.md` (draft, 2026-08-20)
- **Independence note**: reviewed in an independent session that did not author the draft.
- **Method**: all 8 lenses, with the requested focus: remediation guardrails vs ADR-0007, and the `revisionHistoryLimit` linkage.

## Findings

### 1. MINOR — the retention↔remediation linkage is named but not actionable

`20-drift-controllers.md:46` vs design 02 §3.3 (`revisionHistoryLimit: 2`). The GC'd-target row says "retention and remediation budgets are linked, stated" — but nothing *acts* on the link. With limit 2 and a 24h `correlationWindow`, two deploys in a day can GC the only correlated rollback target before behavioral drift surfaces. **Fix**: make the link mechanical — warn at promotion when retained revisions won't cover the correlation window's deploy cadence, and/or pin retention of the last *eval-passing* revision while any `Degraded` window is open (a hold the operator releases when the condition clears).

### 2. MINOR — `llm.fallback` is a design-02 CRD delta with no amendment recorded

`20-drift-controllers.md:21`. The new optional Agent CR field is flagged "(02 delta)" but not recorded in 02 §11 — the mechanism exists (A1–A7) and this is A8. **Fix**: one entry: the field, plus the condition (`ModelDrifted`) it pairs with.

### 3. MINOR — the fallback-route mechanism has no compiler story

`20-drift-controllers.md:21` vs design 03 §3.4. "Compiled fallback route" — compiled *how*, triggered *by what*? The clean mechanism is presumably: the controller flips a status/annotation the operator reads into `PolicyIntent.llm` and recompiles (reusing the Routing/Backend rows) — but that must be said, and 03's table deserves either a "Model fallback" row or a note that the existing LLM Backend row covers it under a controller-set input. **Fix**: one clause naming the intent path; one row/note in 03.

### 4. MINOR — canary-prompt budgets have no declared home

`20-drift-controllers.md:21,31`. Model-drift canaries are LLM calls under `principal: drift:<controller>` — receipted, good — but the budget home is unstated, and the platform's own pattern (14's `build.budget`, 15's `probes.budget`) demands one. Canaries are per-(provider, model), platform-level, so the home is chart values / operator config rather than a CR. **Fix**: one sentence naming it (`drift.canaryBudget` in values; operator synthesizes the intent), mirroring the established pattern.

### 5. MINOR — auto-rollback's target is undefined on core-tier installs

`20-drift-controllers.md:22` vs design 02 §11 A4 / design 07. "Last **eval-passing** revision" doesn't exist where gates run `GatesSkipped` (core tier). **Fix**: state the degraded rule — core-tier rollback targets the previous retained revision, with the escalation noting the weaker guarantee ("rolled back to last-serving, not last-proven") — the same honesty tier-split as everything else.

## Lens summary

1. **Doctrine/charter**: clean — 0 standing pods, baselines on existing Postgres, detector content as data, and D5 keeps the DriftDetector slot honestly reserved rather than invented early.
2. **Guardrails vs ADR-0007** (the requested scrutiny): genuinely sound — the four-part gate (correlation, rate limit, receipt, override) plus same-detector-verifies is ADR-0007 executed faithfully, and three decisions deserve singling out: D1's correlation rule ("otherwise the world changed, not the code — rollback would be superstition"), D3's one-way baseline ratchet (a re-baselined drift definition is drift denial), and D4's 14-day alert-only default (self-healing earns trust with evidence). No findings against the guardrail model itself.
3. **Contract consistency**: findings 2/3/5 — all recording gaps, not design gaps; the KG/embedding families delegate detection to 15/10 correctly (no duplicate detectors).
4. **Hidden dependencies**: finding 1 is the one unclosed loop (GC racing remediation).
5. **Failure modes**: strong — infra≠drift inherited from 15, flap-breaks-at-one-cycle, both-drifted paging.
6. **Security**: remediation receipted under its own principal; the override annotation is itself a recorded decision — right posture.
7. **Research freshness**: nothing external and load-bearing; n/a.
8. **Testability**: the per-family drills including the correlation *negative* test are exactly the suite the guardrails need.

## Disposition

**REVISE**, but the lightest in the series: the remediation model itself survives scrutiny intact — all five findings are recording/one-sentence gaps (an amendment, a budget home, a compiler note, a tier rule, an actionable retention link). This should pass r2 trivially.

VERDICT: REVISE — 5 findings

---

## Re-review r2 (2026-08-20)

- **Verdict**: **PASS**
- **Independence note**: same independent session as r1; did not author the draft or revision.

### Per-finding disposition

| r1 | Severity | Disposition |
|---|---|---|
| 1 | MINOR | **Resolved — mechanically, as asked.** Promotion warns when retained revisions can't cover the correlation window's deploy cadence, and the operator pins retention of the last eval-passing revision while any `Degraded` window is open (hold released on clear). The refusal path remains as the backstop. |
| 2 | MINOR | **Resolved.** `runtime.llm.fallback` + `status.llmFallbackActive` recorded as design 02 §11 A8 (verified). |
| 3 | MINOR | **Resolved.** The mechanism named: controller sets status, operator folds into `PolicyIntent.llm`, recompile rides the existing LLM Backend row — and 03 §3.4 gained the "Model fallback" row (verified). |
| 4 | MINOR | **Resolved.** `drift.canaryBudget` in chart values, operator-synthesized intent, `principal: drift:model-canary` — the 14/15 pattern at platform scope. |
| 5 | MINOR | **Resolved.** Core-tier rollback targets the previous retained revision with the escalation stating "last-serving, not last-proven" — the honesty formula, verbatim. |

### Verdict

**PASS.** All five recording gaps closed, the retention link made genuinely mechanical rather than merely restated. The guardrail model was already sound at r1; it is now also complete. Fold into ADR-0025.
