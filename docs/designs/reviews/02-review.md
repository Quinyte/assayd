# Review: Design 02 — Agent CRD + agent-operator
- **Verdict**: PASS after revisions (findings fixed in-place, 2026-08-20)
- **Independence caveat**: critiqued in the same session that authored the draft; re-critique in a fresh session before implementation starts.

## Findings

1. **BLOCKER — circular revision hash.** `revisionHash(spec.runtime, card digest)` depends on the card digest, which is only known after the revision's workload runs — a hash that changes after deploy would orphan its own workload. **Fix applied**: §3.3 — revision hash is computed from spec only; the card digest lives in status, and card drift triggers re-registration (directory update + event), never a new revision.
2. **MAJOR — candidate route header is an attack surface.** Eval traffic reaches the weight-0 candidate via a routing header; unrestricted, any caller could set the header and hit an ungated revision. **Fix applied**: §3.3 — the candidate header route is bound to platform identity (gate-controller SVID) in gateway policy; agent/external principals matching the header are rejected.
3. **MINOR — budget day boundary unspecified.** `tokensPerDay` needs a defined reset. **Fix applied**: §3.1 note — budgets reset at 00:00 UTC; window and remaining budget observable in status.
4. **MINOR — directory GC unstated.** Old-revision directory entries (`@<revision>`) accumulate. **Fix applied**: §3.4 — directory entries follow `revisionHistoryLimit`; GC on revision deletion.
5. **Doctrine/charter audit**: clean (operator is the budgeted core pod). **Freshness audit**: agent-sandbox v1beta1, agentgateway 2.2 split, ClusterSPIFFEID verified this session with sources.
