# Review: Design 22 — Loop governance (hops, cycles, approvals, kill switch)

- **Verdict**: **REVISE** (minors only)
- **Reviewed**: `docs/designs/22-loop-governance.md` (draft, 2026-08-20)
- **Independence note**: reviewed in an independent session that did not author the draft.
- **Method**: all 8 lenses, with the requested focus: the in-proxy CEL claims verified against `docs/research/authz-2026-08.md`'s sources, and the approval retry model scrutinized against A2A/MCP client reality.

## Findings

### 1. MINOR — the cycle CEL and its stated default disagree

`22-loop-governance.md:22`. The expression `target in lineage.entries` denies **any** revisit of a prior participant; the prose says "Default = deny **immediate** revisit". Those are different policies (A→B→C→A is caught by the first, not the second), and `maxVisits: 2` only makes sense under occurrence-*counting*, a third formulation. **Fix**: pick the semantics (recommended: any-revisit-denied by default — the conservative one the expression already implements; `allowReentry` + `maxVisits` = occurrence count ≤ N, CEL-countable and stateless) and make prose, expression, and the reentry knob tell one story.

### 2. MINOR — "templates retry pending tools natively (09)" is a claim design 09 doesn't make

`22-loop-governance.md:35` vs design 09 §3. The approval UX for the platform's *primary* path rests on template loops retrying `APPROVAL_PENDING {retry_after}` — but 09's template contract (A2A server, card, loop, task store, tests) says nothing about typed-pending retry. Left unrecorded, the flagship path fails approvals exactly like the black-box agents the limitation paragraph worries about. **Fix**: add typed-pending retry (honoring `retry_after`, bounded by `taskTimeout`) to 09's template contract as an item — a 09 note + pack release, since templates are fast-plane; the honest-limitation paragraph then correctly describes only true black boxes.

### 3. MINOR — the approvals KV bucket has no tenancy placement

`22-loop-governance.md:14,31`. "Approvals in JetStream KV" — which account? The platform's precedent (receipts, EVENTS, directory: per-tenant buckets in tenant accounts, ADR-0013) decides this in one sentence; approval requests carry requester chains and tool args digests, which are tenant data. **Fix**: per-tenant `APPROVALS` bucket in the tenant account, created by the 07 bootstrap job — same words as 11 r2 used.

### 4. MINOR — the interceptor proxies approved calls: state the caps, or route back through the gateway

`22-loop-governance.md:33`. On approval, the interceptor "proxies the real call once" — putting the operator binary in the data path for the tool call *and its response*. The trusted-peer argument (gateway-authenticated, unlike 11's internet receiver) is correct as far as it goes, and approval-gated tools are inherently low-volume — but the tap/receiver discipline (hard caps, shed-first, split-mode escape) should be stated here too, or the proxy avoided entirely (on approval, the interceptor returns a single-use pass token/route hint and the *gateway* forwards the retry to the real backend — keeping the operator out of the response path). **Fix**: either one sentence of caps or the gateway-forwards alternative; both are small.

## Lens summary

1. **Doctrine/charter**: clean — 0 pods, approvals as CR-shaped data, and the interceptor correctly distinguished from 11's receiver on the trust axis (the design cites the distinction itself — the review series' vocabulary is being used against the right problems).
2. **CEL claims** (requested verification): consistent with the landed research — [in-proxy CEL compiled at config load over request attributes/JWT/ext-authz metadata](https://deepwiki.com/agentgateway/agentgateway/4-policy-system) covers exactly the stateless depth/cycle checks specified; no enterprise dependency (the note's ADR-0020-D1 discipline line checks out). The statelessness argument — "the lineage *is* the state, carried by the request, tamper-proofed by gateway ownership" — is the design's best insight and it is real.
3. **Approval retry model vs client reality** (requested scrutiny): the *shape* is right — typed retryable pending beats hanging connections in a stateless-HTTP world, single-use consume and fail-closed-on-lost-decision are sound, MRTR/`input_required` correctly parked as the v2 path rather than a premature dependency, and the black-box limitation is documented instead of hidden. Finding 2 is the one loose wire: the primary path's retry behavior lives in a design that doesn't know about it.
4. **Contract consistency**: ADR-0020's reserved rows (`maxHops`/cycles, `requiresApproval`) are now filled with semantics as planned; lineage-in-receipts (ADR-0021) makes every denial auditable; workflows in the lineage compose with 21 §5. Findings 2/3 are the gaps.
5. **Failure modes**: excellent — header-absence-as-tamper-evidence, idempotent request recreation keyed by args digest, fail-closed interceptor, kill-wins-over-canary, and the doctor lineage self-test.
6. **Security**: gateway-owned header with inbound stripping (the scope-header pattern, correctly reused); kill as a sticky guard state with explicit revive (D4) closes the reconcile-drift resurrection hole.
7. **Research freshness**: landed, cited, consistent (finding-free).
8. **Testability**: the matrices (depth/cycle/reentry, tamper, approval lifecycle incl. replay-denied, mid-canary kill) are complete; add finding 1's disambiguated semantics as fixtures once chosen.

## Disposition

**REVISE**, narrowly. The two hard problems — stateless in-proxy loop enforcement and stateless-HTTP-shaped approvals — are both solved with the right mechanisms and honest limitations. The four findings are a semantics disambiguation, a cross-design recording, a tenancy sentence, and a data-path discipline note. Quick r2.

VERDICT: REVISE — 4 findings
