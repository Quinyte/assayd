# Review: Design 24 — AuthzProvider (ReBAC via OpenFGA, `authz/v1alpha1`)

- **Verdict**: **REVISE**
- **Reviewed**: `docs/designs/24-authz-provider.md` (draft, 2026-08-20)
- **Independence note**: reviewed in an independent session that did not author the draft.
- **Method**: all 8 lenses, with the requested focus: chain-check semantics vs design 06's `act` chains, tuple materialization vs Grant CRs, and fail-closed vs core-tier honesty. ext-authz claims checked against the landed `authz-2026-08.md` note (consistent, cited — no fresh search needed).

## Findings

### 1. MAJOR — "IdP group claims" as a tuple *producer* is unimplementable as written; the mechanism you want is contextual tuples at check time

`24-authz-provider.md:42`. §4.1 lists "App `auth.roles` + **IdP group claims** → member/admin/role assignments" among *operator-reconciled tuples* — but group claims live in **tokens**, not CRs. The operator reconciles CRs; it never sees a user's groups except inside a JWT at request time, and materializing user→role tuples from groups would require a directory-sync mechanism the platform deliberately lacks in core (06's `scim?()` is optional/enterprise). As specified, the human half of the model — App members, approver roles (22 §4.2 depends on this!) — has no tuple source, and `plume authz rebuild` cannot "reconstruct from CRs" what was never in CRs.

**Fix**: split the sources honestly. CR-derived tuples stay as designed (agents/tools/graphs/workflows — all CR-resident). User↔role/member relations resolve via **contextual tuples**: the ext-authz adapter passes the verified JWT's group/role claims as contextual tuples in the FGA check request (an OpenFGA-native capability — add it to the research note with a citation at r2) — no sync, no staleness, revocation rides token lifetime (already the stated revocation number). Explicit exceptions (a named user granted a role outside IdP groups) remain Grant CRs. `rebuild` then correctly claims only the CR-derived set, and D3's wording adjusts ("materialization of CRs + Grants; user-context resolved per-check").

### 2. MINOR — machine-only hops need one sentence on what "the chain" is

`24-authz-provider.md:36,47`. D2's every-link rule is defined over the `act` chain — which exists only on on-behalf-of traffic. Pure machine hops (agent→agent with SVIDs, no user) have no `act` chain; they *do* have 22's lineage header, but lineage is governance telemetry, not an authenticated authz subject (entries upstream of the immediate caller aren't attested to the adapter). **Fix**: state the rule — machine hops check the immediate link (`caller can_invoke/can_call target`, subject = the verified SVID); every-link applies to delegated (`act`-bearing) traffic where each link *is* attested by the exchange chain (06). One sentence; prevents someone "helpfully" checking unattested lineage entries as subjects.

### 3. MINOR — the model-bump "replay" reuses the wrong machinery's name

`24-authz-provider.md:62`. "Shadow-mode replay of recent traffic against the new model before enforce (the **16** replay machinery, reused)" — 16 is eval (drives live tasks); what authz migration needs is *decision* replay: re-evaluating recorded check inputs (the checked-chain metadata that receipts already carry, §8) against the new model — 17's session/receipt reading, not 16's runner, and no traffic is driven at all. **Fix**: reword to "replay recorded check inputs from receipts (17's read path) against the new model; compare verdicts" — cheaper and safer than the sentence currently implies.

### 4. MINOR — design 21's shared-runtime identity gap propagates here (cross-reference, not a new defect)

`24-authz-provider.md:22-28` vs 21-review finding 1. The model's `workflow` subjects (`can_trigger`, `can_call: [workflow]`) assume the gateway can identify *which* workflow is calling — which the shared runtime SVID cannot support until 21's actor-client fix lands. Nothing to change in this design beyond a dependency note; recorded here so ADR-0025 sequences the two fixes together.

## Lens summary

1. **Doctrine/charter**: clean — the adapter as a second container in the budgeted FGA pod (D5) is the zero-extra-pods move done right; OpenFGA on platform Postgres per the Zitadel pattern; AuthzProvider joins the shipped-slot list by the established erratum route.
2. **Chain-check semantics** (requested scrutiny): D2's every-link rule is the correct realization of ADR-0011's promise — revoking the user kills the whole delegated action, mechanically — and it composes exactly with 06's `act` nesting (each link attested by an exchange). Finding 2 is the one under-specified case, and it's a boundary statement, not a flaw.
3. **Tuple materialization vs Grants** (requested scrutiny): the two-producer rule (reconciled + Grant CRs, no hand-writes, network-policied write surface, rebuildable with `AuthzDrift`) is the right architecture — finding 1 is the one source that doesn't fit the frame, with a clean native fix. Grant expiry pressure ("unbounded grants warn") is a nice touch.
4. **Fail-closed vs core tier** (requested scrutiny): exemplary honesty on both axes — core's `ReBACAvailable=False` is the `GatesSkipped` pattern correctly reused (no silent tier difference); "there is no fail-open knob, and that's a decision, not an omission" (D4) plus mandatory shadow-first is the strongest safety posture in the design set. The stated revocation number (cache TTL + token lifetime) is the honest arithmetic most systems hide.
5. **Contract consistency**: the coarse-objects/compiled-scope split (D1) is stated precisely so the 01 A1 mechanism and ReBAC never overlap — the "stated so nobody re-litigates it" instinct is process-mature. ext-authz per-route attachment only where relevant respects the latency budget.
6. **Failure modes**: strong — bounded staleness stated everywhere it exists, shadow-mode-forgotten gets a ticket, model migrations are versioned artifacts.
7. **Research freshness**: the landed note covers ext-authz gRPC compatibility ([OSS, Envoy-compatible, dynamic_metadata into CEL](https://agentgateway.dev/docs/standalone/main/configuration/security/external-authz/)) and the no-enterprise-dependency check; finding 1's fix adds one citation (contextual tuples) at r2.
8. **Testability**: the matrix is right (every-link vs broken-link, revocation latency, rebuild determinism, shadow parity, adapter conformance with a second-implementation clause); add finding 1's contextual-tuple case and finding 2's machine-hop case.

## Disposition

**REVISE.** This is the most safety-mature design in the P4 set — fail-closed without a knob, shadow-first by mandate, honest revocation arithmetic. The one MAJOR is real: the human half of the model has no implementable tuple source, and the fix (contextual tuples from verified claims) is native, cheaper, and *more* correct on revocation than the sync it replaces. Three small statements complete it.

VERDICT: REVISE — 4 findings

---

## Re-review r2 (2026-08-20)

- **Verdict**: **PASS** (inherits 21's R2-a as a sequencing dependency)
- **Independence note**: same independent session as r1; did not author the draft or revision.

### Per-finding disposition

| r1 | Severity | Disposition |
|---|---|---|
| 1 | MAJOR | **Resolved.** §4.1 rewritten precisely: user↔role/member relations are *not* tuples — they resolve as OpenFGA **contextual tuples** passed per-check from the verified JWT (the research note gained the citation — verified: openfga.dev contextual-tuples doc); named-user exceptions stay Grant CRs; `rebuild` correctly claims only the CR-derived set; D3 updated. The human half of the model is now implementable, sync-free, and *better* on revocation than what it replaced. |
| 2 | MINOR | **Resolved.** The boundary stated: delegated traffic checks every attested link; pure machine hops check the immediate link with the verified SVID as subject; lineage entries are governance telemetry, never authz subjects. |
| 3 | MINOR | **Resolved.** Model-bump verification is decision replay — recorded check inputs from receipt metadata re-evaluated via 17's read path, no traffic driven; the 16 misattribution gone. |
| 4 | MINOR | **Resolved as a dependency note.** The header now names the 21 r2 workflow-actor fix as what gives `workflow` subjects their bite — which means this design inherits **21's R2-a** (the missing design-06 `workflow-actor` recording): ADR-0025 should land both together, as the r1 finding asked. |

### Verdict

**PASS.** The contextual-tuples fix is the model resolution of this batch — native, cheaper, and strictly more correct on revocation. The design's safety posture (no fail-open knob, mandatory shadow-first, honest revocation arithmetic) stands unchanged. Fold into ADR-0025 alongside 21's R2-a.
