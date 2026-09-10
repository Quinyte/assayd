# CLAUDE.md — assayd

assayd ("graphene" in early drafts) is a lightweight, Kubernetes-native agent platform.

**Phase: implementing P1, under the scope reset of ADR-0030.** The agent-operator, its CRD, the chart and CI exist and pass on k3d and kind.

**Do not read "approved" as "settled."** An earlier version of this line said all 27 designs were approved and critique-passed. That was false, and it was the first thing every contributor read. Design 02's own header says it "is not approved and must not be cited as such"; design 03's says "critique pending — not approved, and do not implement", even though its body was consolidated on 2026-09-10 — a consolidated document is not an approved one. Check the Status line of the design you are about to touch, and trust that over any summary — including this one. `docs/designs/README.md` carries the table.

**What is actually true of the system today**: an agent answers a request. `test/responder` is a real A2A responder, built and pushed by `hack/e2e.sh`, deployed by the operator from a registry digest, and asked a question through its revision Service — the thing no test here could do until 2026-09-06, when the e2e still ran `registry.k8s.io/pause` for everything. **Traffic now traverses a real gateway.** `hack/e2e.sh` installs Gateway API v1.6.0 and agentgateway 1.5.0 on k3d, and `TestAnAgentAnswersThroughTheGateway` gets an agent's answer through the Gateway's own Service — killed by a mutation that changes only the `Host` header, which returns `route not found`, so the route is genuinely carrying it. Getting there closed two gaps that were invisible from either side alone: the operator passed A5.9's admission policy and was then refused by RBAC that granted no `httproutes`, and the policy itself compared a route's `parentRef` against the *operator's* namespace rather than the *Gateway's*, so moving the Gateway made the reservation match nothing and silently admit any author (design 07 A6).

**What remains untrue**: the chart ships no Gateway — `gateway.enabled` defaults to `false` and is read by nothing, so the e2e's Gateway is created by the harness — and **the policy compiler does not exist**: the test authors its route while impersonating the operator's ServiceAccount, which proves the path and the identity reservation and proves nothing about a compiler. No `AgentgatewayPolicy` or `AgentgatewayBackend` is emitted by anything, so no budget, rate limit or tool allowlist is enforced anywhere. The operator fetches, validates and digests the card the container serves (A68), but does **not** verify its signature, cross-check its skills against the CR's grants, or count the registration deadline — §5 carries those three individually. So the platform's central claim — that governance becomes real **at the gateway** — now has its *path* proven and its *governance* still unbuilt.

**The gateway is now provably the only way in — under a hand-authored rule, on k3d only.** `TestTheGatewayIsTheOnlyWayIn` applies design 07 A5.4's ingress half by hand, then measures ADR-0030's stop criterion for the slice: the gateway reaches the agent, an ordinary namespace does not, and the operator's card fetch still gets through. Three mutations kill it (drop either allow rule; apply no policy). It **skips, reporting the axis unverified, on any CNI that does not enforce NetworkPolicy** — kind's default does not, and a policy there is accepted and does nothing. The operator still materializes no NetworkPolicy and holds no `networkpolicies` RBAC (design 07 A6.6).

**And it refuses a principal.** `TestTheGatewayRefusesADisallowedPrincipal` hand-authors an `AgentgatewayPolicy` — API-key authentication over a ConfigMap of `sha256:` hashes, one CEL authorization rule — and measures the slice's stop criterion at the gateway rather than at the network: permitted principal `200` with the agent's own answer, **valid credential in the wrong group `403`**, no or unknown credential `401`. Inverting the rule inverts both outcomes; removing `authorization` while keeping authentication leaves anonymous at `401` and moves the wrong-group caller to `200`, so the `401` and the `403` are different controls and not one. Still hand-authored: **nothing in assayd emits a policy**, no `agentgatewaypolicies` RBAC is granted, and design 03 stays RE-OPENED (design 07 A6.7).

**And an MCP tool call goes through it.** `TestAnAgentCallsAnMCPToolThroughTheGateway` runs a real MCP exchange — `initialize`, `tools/list`, `tools/call` — against `test/mcpserver`, a two-tool Streamable HTTP server behind an `AgentgatewayBackend` on a second Gateway listener. A hand-authored `backend.mcp.authorization` allowlist (design 03's `toolAllowlist`) does more than refuse: it **removes the disallowed tool from `tools/list`**, so an agent never learns it exists, and a refused call returns `Unknown tool` rather than "forbidden" — good for non-disclosure, a trap for whoever debugs it. Flipping the rule inverts the listing and both calls. **The A2A half of ADR-0030's clause is still not literal**: `test/responder` serves a conformant A2A card but answers on `/assayd-test/echo`, which is not an A2A method, so an agent completes a *request* through the gateway and not yet an *A2A task* (design 07 A6.8). The next work is the rest of the narrow slice in ADR-0030, not the next design.

## Skills — use them, do not paraphrase them

Nine skills live in `.claude/skills/`. **A skill with a `paths:` key does not resolve until a matching file has been read in the session** — so `/implement-feature` returns "Unknown skill" on a cold start even in the right directory, and `/write-spec`, `/adr`, `/design-component` and `/audit-docs` behave the same way. That is a property of `paths`, not a broken session. `/verify-change`, `/review-code`, `/critique-design` and `/research-latest` carry no `paths` and always resolve.

| Skill | Use when |
|---|---|
| `implement-feature` | writing or changing any code under `api/ internal/ cmd/ test/ config/ charts/` |
| `review-code` | before merging a diff — forks its own context |
| `critique-design` | before approving a design or amending an approved one — forks its own context |
| `write-spec` | writing a design, an ADR, a CRD type, or any user-facing string |
| `research-latest` | any landscape, library or standard question — never answer from memory |
| `design-component` · `adr` · `audit-docs` | designing, recording a decision, checking doc consistency |
| `verify-change` | **after any amendment or fix** — the loop that closes it: gate → independent critique → cross-family critique → spike the unmeasured claim |

## Everything else lives in AGENTS.md

**Read `AGENTS.md`.** It carries the rules this project learned the hard way, the doctrine and charter gates, the commands, and what each test layer can honestly claim.

It is model-neutral on purpose: Codex, Cursor and Copilot read `AGENTS.md` and never see this file, so a rule duplicated here would drift into two versions and reviewers would be held to different standards depending on which model they happened to be.
