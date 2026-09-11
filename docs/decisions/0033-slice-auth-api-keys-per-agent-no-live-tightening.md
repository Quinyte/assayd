# ADR-0033: The slice's gateway auth is one API-key policy per Agent, and the compiler refuses to tighten a live route

- **Status**: accepted · 2026-09-11 · decided by the human on `reviews/03-a52-recritique.md` (B1, B3, B4), with B1 settled by `research/agentgateway-service-target-spike-2026-09.md`
- **Context**: Design 03's re-critique found three decisions the design had never made about the first policy the compiler would emit: the serving route's `-auth`.
  - **The target (B1).** Design 03 assumed per-revision policies. The operator ships one serving route per Agent (design 07 A6.10), so two revisions' policies would share one target, and agentgateway resolves that conflict randomly.
  - **The input (B3).** `-auth`'s input was design 06 identity, which has no implementation, so the design as written would withhold every Agent's route.
  - **Serving routes (B4).** No transaction covered adding a first policy to a route that is already serving.
- **Decision**:
  - **Target.** `-auth` is **one `AgentgatewayPolicy` per Agent**, targeting the per-Agent serving `HTTPRoute`. The preferred per-revision shape targeted each revision's Service, and agentgateway 1.5.0's CRD refuses `traffic` policies on a Service (measured). The policy follows the serving revision. A candidate whose auth would be looser is applied at promotion. A stricter one is **held**, not applied in place.
  - **Input.** The slice's one identity mechanism (ADR-0030) is **API keys**: `traffic.apiKeyAuthentication` over a chart-level set of `sha256:` key hashes, plus a CEL `authorization` rule over the Agent's admitted groups. This is the shape `TestTheGatewayRefusesADisallowedPrincipal` measures (401 / 403 / 200). Design 06 later replaces the key source without moving the policy.
  - **Serving routes.** A compiler that finds a serving route with no governing policy **refuses to change it** and says so on the Agent. The route is left exactly as it was, and the admin recreates the Agent. Applying in place and trusting only an enforcement probe (an anonymous request must get `401` before the Agent counts as governed) is recorded as the future path and is not implemented.
- **Consequences**:
  - **Contain, don't patch.** Every change stays on the side of "leave the known state serving". A candidate is contained rather than a live route patched, as with revisions under design 02 and design 16.
  - **API keys have a cost.** They are shared secrets with manual rotation, and the CRD owes an `apikey` value for `expose.a2a.auth` with an `allowedGroups` list (design 02).
  - **Existing gateway installs must recreate their Agents.** Installs that enabled the gateway before the compiler existed have to recreate their Agents to become governed. Today that is only the e2e harness, because `gateway.enabled` defaults to `false`.
  - **Revisit at the next agentgateway upgrade.** The Service-target refusal is a CEL rule in the vendored CRD. If an upgrade lifts it, per-revision auth becomes possible and this ADR is revisited.
