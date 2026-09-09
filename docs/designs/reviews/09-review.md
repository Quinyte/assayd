# Review: Design 09 — SDK templates (the BYO-SDK on-ramp)

- **Verdict**: **REVISE**
- **Reviewed**: `docs/designs/09-sdk-templates.md` (draft, 2026-08-20)
- **Independence note**: reviewed in an independent session that did not author the draft.
- **Method**: all 8 lenses; consistency vs architecture §11/§14, designs 02/05/07/08, ADRs 0003/0019; A2A v1.0 and a2a-sdk claims verified by current-year search (sources inline).

## Findings

### 1. MAJOR — the "builtin pack" delivery mechanism doesn't exist until P3

`09-sdk-templates.md:10,61` vs the backlog (pack format + installer = design 18, **P3**) and design 08 D1. Templates are P1 — the design 07 CI matrix scaffolds from two of them on every merge — but they "ship *in* packs from day one, installed as the `sdk-templates` builtin pack", and the pack format, signing, and installer are a P3 design. Meanwhile 08 D1 explicitly forbids the obvious workaround (CLI-embedded template content). As specified, P1 has templates that nothing can deliver.

**Fix**: state the interim mechanism explicitly and make it forward-compatible with design 18. Recommended: the templates live in a versioned OCI artifact (cosign-signed — machinery that exists in P1) at a well-known ref; the CLI fetches + verifies it directly in P1 (a degenerate "pack" with only a template facet); design 18 later formalizes the manifest without moving the artifact. One paragraph — but it must be written, because it constrains what design 18 may change.

### 2. MAJOR — card signing: no key-management story, and registration verification silently amends approved design 02

`09-sdk-templates.md:22,62` vs design 02 §3.4. Two gaps in one feature. (a) "key from the platform's signing identity" names no identity: is it Sigstore keyless (like the image signing it rides alongside), a platform-held key (who provisions it — design 06 provisions none), or the *publisher-domain* signature A2A v1.0 actually specifies ([signed Agent Cards verify agent identity across organizational boundaries](https://a2a-protocol.org/latest/announcing-1.0/) — a domain-bound trust model, which a cluster-internal platform key would not satisfy for external consumers)? Key custody, rotation, and what verifiers outside the cluster should trust are all unstated. (b) "design 02's registration verifies both (card signature + digest)" — approved 02 §3.4 validates parseable/name/A2A-version only; signature verification is a new registration gate, unrecorded. And the policy question it forces is unanswered: BYO/external agents didn't run `assayd build` — is an unsigned card rejected (breaking the BYO promise), or accepted with a condition?

**Fix**: specify the signing identity and trust model (recommendation: Sigstore keyless with the same OIDC builder identity as image signing for platform-internal verification; publisher-domain signing offered at `expose: public` time where A2A v1.0's model actually applies); define the verification policy (required for assayd-built, optional-with-`CardUnsigned` condition for BYO/external); record the 02 §3.4 delta as §11 A5-or-next.

### 3. MINOR — research verified true, but cited from a secondary blog and not landed

`09-sdk-templates.md:6`. The claims check out — [A2A v1.0 is the stable LF release with signed Agent Cards, JSON-RPC/gRPC/REST bindings, and official SDKs](https://a2a-protocol.org/latest/announcing-1.0/), with [AgentCard/AgentSkill in the core data model](https://tyk.io/learning-center/a2a-protocol-architecture-and-technical-specification/) and `pip install a2a-sdk` real — but the design's only citation is a third-party vendor guide, and nothing is landed in `docs/research/` (rule 4). **Fix**: land `docs/research/a2a-v1-2026-08.md` with the primary sources (a2a-protocol.org announcement + spec) and cite it; the signed-card *mechanics* (what exactly is signed, JWS format, where the key/domain binding lives) belong there because finding 2's fix depends on them.

### 4. MINOR — signature policy for agents that never ran `assayd build` (split from finding 2 for tracking)

`09-sdk-templates.md:22`. Stated in 2(b); tracked separately because it changes user-facing behavior for the BYO path — the platform's founding promise ("any container speaking A2A") must not quietly grow a signing prerequisite. **Fix**: the explicit required/optional matrix per agent origin (assayd-built | BYO in-cluster | external), each with its condition.

### 5. MINOR — operator-injected env contract is used but not owned anywhere

`09-sdk-templates.md:23,25`. Templates consume `ASSAYD_GATEWAY_URL`, `ASSAYD_KG_ENDPOINTS`, `ASSAYD_NATS_URL` + tenant creds "injected by the operator" — a real interface between 02's workload materialization and every template, defined in neither design. If the names drift, every template breaks silently. **Fix**: name the env contract (a short versioned table — variable, source, secret-or-not) in this design as the owning doc, and note it as a 02 delta line (the operator's workload spec grows the injection).

## Lens summary

1. **Doctrine**: the design's core is doctrine at its best — templates as fast-plane pack data, zero platform framework code, "the ~100 lines the user owns".
2. **Charter**: clean (fast plane, Agent/Tool/Artifact); finding 1 is a phasing hole, not a charter violation.
3. **Contract consistency**: findings 2, 4, 5 — the 02 registration delta and the unowned env contract; otherwise strong (task store honors 02 D5; wizard metadata matches 08 §4; card.yaml↔handler golden test closes the drift design 02's digest check can't see).
4. **Hidden dependencies/circularity**: finding 1 (P1 depends on a P3 mechanism); the in-memory task-store fallback with a loud log line is the right honesty.
5. **Failure modes**: good — "the contract is enforced at the platform edge, templates just make it easy" is the correct division; add the signing-key-unavailable-at-build row when finding 2 resolves.
6. **Security**: no-runtime-skill-fetch (D4) is a defensible, well-reasoned restriction with the revisit named; env-only creds right; finding 2 is the gap.
7. **Research freshness**: claims true (verified), citation hygiene not (finding 3).
8. **Testability**: the template conformance suite (every template must scaffold→build→register→pass golden-task in CI) is exactly how a template contract stays true; golden scaffolds per release seal it.

## Disposition

**REVISE.** The template contract and SDK matrix are well-shaped and the fast-plane packaging is the right instinct — one phase too early. The two MAJORs are both "the mechanism this rides on isn't there yet": no delivery vehicle in P1, no signing identity or recorded verification policy. Both have contained fixes that mostly constrain later designs (18, and the 02 amendment log).

VERDICT: REVISE — 5 findings

---

## Re-review r2 (2026-08-20)

- **Verdict**: **PASS**
- **Independence note**: same independent session as r1; did not author the draft or revision.

### Per-finding disposition

| r1 | Severity | Disposition |
|---|---|---|
| 1 | MAJOR | **Resolved.** P1 delivery specified as the recommended degenerate pack: versioned, cosign-signed OCI artifact at a well-known ref, fetched + verified by the CLI with P1-existing machinery; explicitly recorded as a constraint on design 18 (formalize the manifest *around* the same artifact, don't move it). The phasing hole is closed and the forward promise is written down. |
| 2 | MAJOR | **Resolved.** Signing identity: Sigstore keyless with the image-signing builder identity — one trust root, no key custody; publisher-domain signatures deferred to `expose: public` where A2A v1.0's cross-org model actually applies (the distinction the r1 finding drew, adopted). Verification policy: required for assayd-built, unsigned BYO/external registers with loud `CardUnsigned`. Registration-gate change recorded as design 02 §11 A6. |
| 3 | MINOR | **Resolved.** `docs/research/a2a-2026-08.md` landed with primary sources (a2a-protocol.org announcement, SDK org) and the assayd signing model stated alongside the facts it rests on. |
| 4 | MINOR | **Resolved** (via §3.2 + A6): the origin matrix collapses to assayd-built (required) vs everything unsigned (`CardUnsigned`, loud) — simpler than the three-way split the finding sketched, and the BYO promise explicitly holds. Acceptable. |
| 5 | MINOR | **Resolved.** The injected env contract is owned by design 02 (§11 A7: variables, sources, versioned with the CRD); templates consume, never define. Ownership landed on the right side of the seam (injection is reconcile behavior). |

### Verdict

**PASS.** Both MAJORs closed with the recommended structures, and the fixes correctly landed most of their weight as recorded constraints on other designs (18's manifest, 02's amendment log) rather than local caveats. Fold into ADR-0022.
